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
	"errors"
	"fmt"
)

// Start creates a Pending run and launches it in its own goroutine. It returns
// as soon as the run is durably created, so the caller is never blocked on
// execution and one process can drive many thousands of runs. An optional input
// is journaled as the run's seed and read back inside the workflow with Input.
func (e *Engine) Start(ctx context.Context, workflow, runID string, in ...any) error {
	if e.closed.Load() {
		return ErrEngineClosed
	}
	r := Run{ID: runID, Workflow: workflow, Status: Pending, Created: e.clock.Now()}
	if err := e.store.Create(ctx, r); err != nil {
		return fmt.Errorf("rerun: create run %s: %w", runID, err)
	}
	if len(in) > 0 && in[0] != nil {
		b, err := e.codec.Marshal(in[0])
		if err != nil {
			panic(fmt.Sprintf("rerun: marshal input for run %s: %v", runID, err))
		}
		if err := e.store.Append(ctx, runID, Log{Seq: inputSeq, Tag: inputTag, Payload: b, At: e.clock.Now()}); err != nil {
			panic(fmt.Sprintf("rerun: journal input for run %s: %v", runID, err))
		}
	}
	e.obs.OnStart(r)
	e.spawn(r)
	return nil
}

// exec leases the run, replays its journal to the crash point, and runs the
// remainder live. Recovery reuses this exact path with a pre-populated journal,
// so resuming a crashed run is the normal path and not a second one with its own
// bugs. A run already held by another worker is skipped, not stolen.
func (e *Engine) exec(ctx context.Context, r Run) {
	release, acquired, err := e.store.Acquire(ctx, r.ID)
	if err != nil || !acquired {
		return
	}
	defer release.Close()

	// The run's signal context: cancelled by Engine.Cancel (with
	// errCancelRequested) or by Shutdown via the engine root (errParked). ctx
	// here is the engine root; the caller who started the run is long gone. ctx
	// is kept for recording the terminal status, since a cancel unwinds cctx.
	cctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	e.register(r.ID, cancel)
	defer e.unregister(r.ID)

	// When cross-process cancellation is enabled and the store supports it, watch
	// for a cancel request recorded by another process.
	if c, ok := e.store.(Canceller); ok && e.cancelPoll > 0 {
		go e.pollCancel(cctx, cancel, c, r.ID)
	}

	e.store.Finish(ctx, r.ID, Running)

	logs, err := e.store.LoadLogs(ctx, r.ID)
	if err != nil {
		e.store.Finish(ctx, r.ID, Failed)
		return
	}

	// Separate engine metadata (the seed at inputSeq, a terminal error at errSeq)
	// from the positional step journal that replay matches against.
	var input []byte
	haveErr := false
	steps := make([]Log, 0, len(logs))
	for _, l := range logs {
		if l.Seq < 0 {
			switch l.Tag {
			case inputTag:
				input = l.Payload
			case errorTag:
				haveErr = true
			}
			continue
		}
		steps = append(steps, l)
	}

	w := &W{RunID: r.ID, logs: steps, input: input, replay: len(steps) > 0, eng: e, ctx: cctx}
	fn := e.lookup(r.Workflow)
	werr := fn(w)
	cause := context.Cause(cctx)
	switch {
	case werr == nil:
		// A run that finished before noticing a cancel or shutdown is Done: its
		// work is real and its journal is complete.
		e.store.Finish(ctx, r.ID, Done)
		e.obs.OnFinish(r.ID, Done)
	case errors.Is(cause, errCancelRequested):
		e.store.Finish(ctx, r.ID, Cancelled)
		e.obs.OnFinish(r.ID, Cancelled)
	case errors.Is(cause, errParked):
		// Parking is crash-equivalent: no status write, the lease releases on
		// return, and a later claim resumes from the journal.
		return
	default:
		if !haveErr {
			// Record the terminal error once so Result can surface why it failed.
			e.store.Append(ctx, r.ID, Log{Seq: errSeq, Tag: errorTag, Err: werr.Error(), At: e.clock.Now()})
		}
		e.store.Finish(ctx, r.ID, Failed)
		e.obs.OnFinish(r.ID, Failed)
	}
}

// Recover relaunches every incomplete run through exec, resuming workflows a
// process restart left in flight.
func (e *Engine) Recover(ctx context.Context) error {
	if e.closed.Load() {
		return ErrEngineClosed
	}
	runs, err := e.store.Incomplete(ctx)
	if err != nil {
		return fmt.Errorf("rerun: recover: %w", err)
	}
	for _, r := range runs {
		e.spawn(r)
	}
	return nil
}
