// Copyright 2026 Sylvester Francis
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package rerun

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// W is the handle threaded through a workflow. It carries the loaded journal
// and a cursor so Do can tell replay from live execution.
type W struct {
	RunID string

	seq    int
	logs   []Log
	input  []byte
	replay bool
	eng    *Engine
	ctx    context.Context

	// inStep is set while a Do/step is in flight. A workflow body is
	// single-threaded; a second concurrent Do panics rather than corrupting seq.
	inStep atomic.Bool
}

// Do runs a step exactly once. On the first run it executes fn, journals the
// result, and returns it; on replay it returns the journaled result without
// executing fn. It panics if the tag diverges from the journal: that means the
// workflow is no longer deterministic, and every later positional match would
// be meaningless.
func Do[T any](w *W, tag string, fn func(context.Context) (T, error)) (T, error) {
	if strings.HasPrefix(tag, reservedPrefix) {
		panic(fmt.Sprintf("rerun: tag %q uses the reserved %q prefix", tag, reservedPrefix))
	}
	return doStep(w, tag, fn)
}

// doStep is Do without the reserved-prefix check, for the engine's own
// journaled values (Return). Everything else about a step is identical.
func doStep[T any](w *W, tag string, fn func(context.Context) (T, error)) (T, error) {
	if !w.inStep.CompareAndSwap(false, true) {
		panic("rerun: concurrent Do on one workflow: workflow bodies are single-threaded; run concurrent work inside a single Do")
	}
	defer w.inStep.Store(false)
	if w.replay && w.seq < len(w.logs) {
		return replayStep[T](w, tag)
	}
	return liveStep(w, tag, fn)
}

func replayStep[T any](w *W, tag string) (T, error) {
	l := w.logs[w.seq]
	if l.Tag != tag {
		panic(fmt.Sprintf(
			"rerun: determinism broken at seq %d in run %s: journal=%q code=%q",
			w.seq, w.RunID, l.Tag, tag,
		))
	}
	w.seq++

	var v T
	if err := w.eng.codec.Unmarshal(l.Payload, &v); err != nil {
		panic(fmt.Sprintf("rerun: journal corrupt at seq %d in run %s: %v", w.seq-1, w.RunID, err))
	}
	if l.Err != "" {
		return v, &StepError{Tag: l.Tag, Msg: l.Err}
	}
	return v, nil
}

func liveStep[T any](w *W, tag string, fn func(context.Context) (T, error)) (T, error) {
	// Fast-fail once the run is cancelled or parked: executing new work against a
	// dead context helps nobody, and journaling it would race the run's terminal
	// status. Nothing is journaled — if the run is ever resumed (park), the step
	// simply runs then.
	if w.ctx.Err() != nil {
		var zero T
		return zero, w.ctx.Err()
	}
	w.replay = false
	v, err := fn(w.ctx)

	b, merr := w.eng.codec.Marshal(v)
	if merr != nil {
		panic(fmt.Sprintf("rerun: marshal failed at seq %d in run %s: %v", w.seq, w.RunID, merr))
	}
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	l := Log{Seq: w.seq, Tag: tag, Payload: b, Err: errStr, At: w.eng.clock.Now()}
	// The journal write is detached from the run's cancellation: a step that
	// already ran must be recorded even if a cancel landed mid-flight.
	pctx, done := w.eng.pctx(w.ctx)
	defer done()
	if serr := w.eng.store.Append(pctx, w.RunID, l); serr != nil {
		panic(fmt.Sprintf("rerun: journal write failed at seq %d in run %s: %v", w.seq, w.RunID, serr))
	}
	w.eng.obs.OnStep(w.RunID, l)

	w.seq++
	return v, err
}

// Sleep is a durable delay. It journals an absolute deadline as an instant step,
// then waits only the time still remaining until that deadline, recomputed on
// every run. A restart before the deadline waits out the remainder; a restart
// after it returns immediately. The wait itself is never journaled — it is a
// function of the deadline and the clock, so it is always recomputed, never
// stored.
func Sleep(w *W, d time.Duration) error {
	deadline, _ := Do(w, fmt.Sprintf("sleep:%v", d), func(context.Context) (int64, error) {
		return w.eng.clock.Now().Add(d).UnixNano(), nil
	})
	remaining := time.Unix(0, deadline).Sub(w.eng.clock.Now())
	if remaining <= 0 {
		return nil
	}
	select {
	case <-w.eng.clock.After(remaining):
		return nil
	case <-w.ctx.Done():
		return w.ctx.Err()
	}
}

// DoTimeout is Do with a per-step deadline: fn runs under a context that cancels
// after d. Whatever fn returns — a value, its own error, or a deadline error if
// it honors the context — is journaled as the step's outcome, so replay is
// deterministic and a timed-out step is never silently retried into a success.
func DoTimeout[T any](w *W, tag string, d time.Duration, fn func(context.Context) (T, error)) (T, error) {
	return Do(w, tag, func(ctx context.Context) (T, error) {
		cctx, cancel := context.WithTimeout(ctx, d)
		defer cancel()
		return fn(cctx)
	})
}
