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
	e.lookup(workflow) // an unknown name at a Start call site is a programmer error: panic here, not later in the goroutine
	var seed []byte
	if len(in) > 0 && in[0] != nil {
		b, err := e.codec.Marshal(in[0])
		if err != nil {
			return fmt.Errorf("rerun: marshal input for run %s: %w", runID, err)
		}
		seed = b
	}
	// The seed rides inside the Run record, so a run and its input are created in
	// one atomic write — a crash can never leave a run without its input.
	r := Run{ID: runID, Workflow: workflow, Status: Pending, Created: e.clock.Now(), Input: seed}
	if err := e.store.Create(ctx, r); err != nil {
		return fmt.Errorf("rerun: create run %s: %w", runID, err)
	}
	e.obs.OnStart(r)
	e.spawn(r)
	return nil
}

// exec leases the run, replays its journal to the crash point, and runs the
// remainder live. Recovery reuses this exact path. Every failure inside it is
// contained to this run: a panic sticks the run, a transient store error or a
// lost lease parks it, and in no case does the process die.
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

	// Every terminal write below gets a persistence context created at the write,
	// not at exec entry: a body that runs longer than the store timeout must not
	// record its status against an already-expired deadline. pctx also detaches
	// from the run's cancellation so a cancel cannot lose a terminal record.

	defer func() {
		if p := recover(); p != nil {
			if _, isAbort := p.(runAbort); isAbort {
				return // park: lease releases, status stays incomplete
			}
			// A programmer error (determinism, corruption, marshal). Keep the
			// message loud, but confine the blast radius to this run.
			pctx, pdone := e.pctx(ctx)
			defer pdone()
			// Journal why it stuck, mirroring the Failed path, so Result can surface
			// the reason to an operator before a Redrive. The panic message already
			// pinpoints a determinism break by seq and tag; the run stays excluded
			// from recovery until Redrive re-admits it.
			e.store.Append(pctx, r.ID, Log{Seq: errSeq, Tag: errorTag, Err: fmt.Sprintf("stuck: %v", p), At: e.clock.Now()})
			e.store.Finish(pctx, r.ID, Stuck)
			e.obs.OnFinish(r.ID, Stuck)
		}
	}()

	// Honor a durable cancel before doing any work: cancels then survive restarts
	// even when polling is disabled — a request that landed before a crash is
	// finished at the next claim without ever re-running the workflow.
	if c, ok := e.store.(Canceller); ok {
		if req, cerr := c.CancelRequested(ctx, r.ID); cerr == nil && req {
			pctx, pdone := e.pctx(ctx)
			defer pdone()
			e.store.Finish(pctx, r.ID, Cancelled)
			e.obs.OnFinish(r.ID, Cancelled)
			return
		}
	}

	// When cross-process cancellation is enabled and the store supports it, watch
	// for a cancel request recorded by another process.
	if c, ok := e.store.(Canceller); ok && e.cancelPoll > 0 {
		go e.pollCancel(cctx, cancel, c, r.ID)
	}

	fn, ok := e.lookupSafe(r.Workflow)
	if !ok {
		// Not registered in THIS process. In a heterogeneous fleet another worker
		// may own this workflow, so park untouched — never stick.
		return
	}

	e.store.Finish(ctx, r.ID, Running)

	logs, err := e.store.LoadLogs(ctx, r.ID)
	if err != nil {
		return // transient store failure: park, a later claim retries
	}

	// Separate engine metadata (a terminal error at errSeq) from the positional
	// step journal that replay matches against. The seed rides in the Run record
	// now; a legacy rerun:input journal row is only the v0.1 fallback and never
	// overrides the atomic seed (the golden fixtures prove this path forever).
	input := r.Input
	haveErr := false
	steps := make([]Log, 0, len(logs))
	for _, l := range logs {
		if l.Seq < 0 {
			switch l.Tag {
			case inputTag:
				if input == nil {
					input = l.Payload
				}
			case errorTag:
				haveErr = true
			}
			continue
		}
		steps = append(steps, l)
	}

	w := &W{RunID: r.ID, logs: steps, input: input, replay: len(steps) > 0, eng: e, ctx: cctx}
	werr := fn(w)
	cause := context.Cause(cctx)
	pctx, pdone := e.pctx(ctx)
	defer pdone()
	switch {
	case werr == nil && !w.fastFailed.Load():
		// Genuinely completed: every step ran to completion, none fast-failed
		// against a dead context. A cancel or shutdown that merely raced the final
		// return loses here — the work is real and the journal is complete.
		if w.seq < len(w.logs) {
			panic(fmt.Sprintf(
				"rerun: run %s returned with %d unconsumed journal entries (next seq %d): the workflow shrank; gate the change with Version",
				r.ID, len(w.logs)-w.seq, w.seq,
			))
		}
		e.store.Finish(pctx, r.ID, Done)
		e.obs.OnFinish(r.ID, Done)
	case errors.Is(cause, errParked):
		// Parking is crash-equivalent: no status write, the lease releases on
		// return, and a later claim resumes from the journal. A workflow that
		// returned nil only because a step fast-failed under shutdown lands here.
		return
	case errors.Is(cause, errCancelRequested):
		e.store.Finish(pctx, r.ID, Cancelled)
		e.obs.OnFinish(r.ID, Cancelled)
	default:
		if !haveErr {
			// Record the terminal error once so Result can surface why it failed.
			e.store.Append(pctx, r.ID, Log{Seq: errSeq, Tag: errorTag, Err: werr.Error(), At: e.clock.Now()})
		}
		e.store.Finish(pctx, r.ID, Failed)
		e.obs.OnFinish(r.ID, Failed)
	}
}

// runAbort is the panic value liveStep uses to unwind a run the engine must
// stop executing (lost lease, failed journal write) without sticking it. It is
// a panic, not an error return, so user code cannot accidentally swallow it and
// keep executing steps.
type runAbort struct{ cause error }

// Redrive resets a Stuck run to Pending so the next Recover (or, from M2, the
// dispatcher) claims it again — the operator's lever after shipping a fix. M1
// note: this resets unconditionally and does not clear a durable cancel request,
// so a run that was ever cancel-requested is re-finished Cancelled at the next
// claim rather than re-run (start a fresh run ID in that case). M2's Inspector
// adds status verification and cancel-aware redrive.
func (e *Engine) Redrive(ctx context.Context, runID string) error {
	if e.closed.Load() {
		return ErrEngineClosed
	}
	return e.store.Finish(ctx, runID, Pending)
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
