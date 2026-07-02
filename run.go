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
)

// Start creates a Pending run and launches it in its own goroutine. It returns
// as soon as the run is durably created, so the caller is never blocked on
// execution and one process can drive many thousands of runs. An optional input
// is journaled as the run's seed and read back inside the workflow with Input.
func (e *Engine) Start(ctx context.Context, workflow, runID string, in ...any) error {
	r := Run{ID: runID, Workflow: workflow, Status: Pending, Created: e.clock.Now()}
	if err := e.store.Create(ctx, r); err != nil {
		return fmt.Errorf("rerun: create run %s: %w", runID, err)
	}
	if len(in) > 0 && in[0] != nil {
		b, err := e.codec.Marshal(in[0])
		if err != nil {
			panic(fmt.Sprintf("rerun: marshal input for run %s: %v", runID, err))
		}
		if err := e.store.Append(ctx, runID, Log{Seq: -1, Tag: inputTag, Payload: b, At: e.clock.Now()}); err != nil {
			panic(fmt.Sprintf("rerun: journal input for run %s: %v", runID, err))
		}
	}
	e.obs.OnStart(r)
	go e.exec(ctx, r)
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

	e.store.Finish(ctx, r.ID, Running)

	logs, err := e.store.LoadLogs(ctx, r.ID)
	if err != nil {
		e.store.Finish(ctx, r.ID, Failed)
		return
	}

	// Separate the run's seed and other engine metadata from the positional step
	// journal that replay matches against.
	var input []byte
	steps := make([]Log, 0, len(logs))
	for _, l := range logs {
		if strings.HasPrefix(l.Tag, reservedPrefix) {
			if l.Tag == inputTag {
				input = l.Payload
			}
			continue
		}
		steps = append(steps, l)
	}

	w := &W{RunID: r.ID, logs: steps, input: input, replay: len(steps) > 0, eng: e, ctx: ctx}
	fn := e.lookup(r.Workflow)
	if werr := fn(w); werr != nil {
		e.store.Finish(ctx, r.ID, Failed)
		e.obs.OnFinish(r.ID, Failed)
		return
	}
	e.store.Finish(ctx, r.ID, Done)
	e.obs.OnFinish(r.ID, Done)
}

// Recover relaunches every incomplete run through exec, resuming workflows a
// process restart left in flight.
func (e *Engine) Recover(ctx context.Context) error {
	runs, err := e.store.Incomplete(ctx)
	if err != nil {
		return fmt.Errorf("rerun: recover: %w", err)
	}
	for _, r := range runs {
		go e.exec(ctx, r)
	}
	return nil
}
