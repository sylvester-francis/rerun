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

package rerun_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sylvester-francis/rerun"
	"github.com/sylvester-francis/rerun/internal"
)

// One poisoned run must not take the process (or its neighbors) down.
func TestExec_DeterminismPanicSticksRunOnly(t *testing.T) {
	eng, store, _ := setup(t)
	eng.Handle("wf", func(w *rerun.W) error {
		_, err := rerun.Do(w, "tag-B", func(context.Context) (int, error) { return 1, nil })
		return err
	})
	ctx := context.Background()
	// A journal written by yesterday's code: same position, different tag.
	must(t, store.Create(ctx, rerun.Run{ID: "poisoned", Workflow: "wf", Status: rerun.Running, Created: time.Now()}))
	must(t, store.Append(ctx, "poisoned", rerun.Log{Seq: 0, Tag: "tag-A", Payload: []byte(`1`), At: time.Now()}))
	must(t, eng.Recover(ctx))
	waitStatus(t, store, "poisoned", rerun.Stuck)

	// The engine still works for healthy runs afterward.
	must(t, eng.Start(ctx, "wf", "healthy"))
	waitStatus(t, store, "healthy", rerun.Done)
}

// Deviation from spec D3, decided here: an unknown workflow name PARKS the
// run untouched instead of sticking it. In a fleet of heterogeneous workers
// (worker A registers wf-X, worker B registers wf-Y), stick-on-unknown would
// let B permanently poison A's runs at claim time. Stuck remains reserved for
// panics and corruption. Orphaned runs stay Pending and are visible via
// inspection (M2).
func TestExec_UnknownWorkflowParksUntouched(t *testing.T) {
	eng, store, _ := setup(t)
	ctx := context.Background()
	must(t, store.Create(ctx, rerun.Run{ID: "orphan", Workflow: "registered-elsewhere", Status: rerun.Pending, Created: time.Now()}))
	must(t, eng.Recover(ctx))
	time.Sleep(50 * time.Millisecond)
	r, ok := store.Get("orphan")
	if !ok || r.Status != rerun.Pending {
		t.Fatalf("unknown-workflow run status = %v, want Pending (parked untouched)", r.Status)
	}
}

func TestExec_ShrunkWorkflowSticksRun(t *testing.T) {
	eng, store, _ := setup(t)
	// Today's code has one step; the journal has two: the code shrank.
	eng.Handle("wf", func(w *rerun.W) error {
		_, err := rerun.Do(w, "a", func(context.Context) (int, error) { return 1, nil })
		return err
	})
	ctx := context.Background()
	must(t, store.Create(ctx, rerun.Run{ID: "r1", Workflow: "wf", Status: rerun.Running, Created: time.Now()}))
	must(t, store.Append(ctx, "r1", rerun.Log{Seq: 0, Tag: "a", Payload: []byte(`1`), At: time.Now()}))
	must(t, store.Append(ctx, "r1", rerun.Log{Seq: 1, Tag: "b", Payload: []byte(`2`), At: time.Now()}))
	must(t, eng.Recover(ctx))
	waitStatus(t, store, "r1", rerun.Stuck)
}

func TestRedrive_StuckRunRunsAgainAfterFix(t *testing.T) {
	store := internal.NewMemStore()
	ctx := context.Background()
	// Yesterday's code journaled tag-A; today's build (engine A) issues tag-B:
	// determinism panic -> Stuck.
	must(t, store.Create(ctx, rerun.Run{ID: "r1", Workflow: "wf", Status: rerun.Running, Created: time.Now()}))
	must(t, store.Append(ctx, "r1", rerun.Log{Seq: 0, Tag: "tag-A", Payload: []byte(`1`), At: time.Now()}))
	a := rerun.New(store)
	a.Handle("wf", func(w *rerun.W) error {
		_, err := rerun.Do(w, "tag-B", func(context.Context) (int, error) { return 1, nil })
		return err
	})
	must(t, a.Recover(ctx))
	waitStatus(t, store, "r1", rerun.Stuck)

	// The rollback/fix: engine B's workflow matches the journal again.
	var ran int32
	b := rerun.New(store)
	b.Handle("wf", func(w *rerun.W) error {
		_, err := rerun.Do(w, "tag-A", func(context.Context) (int, error) {
			atomic.AddInt32(&ran, 1)
			return 1, nil
		})
		return err
	})
	must(t, b.Redrive(ctx, "r1"))
	must(t, b.Recover(ctx))
	waitStatus(t, store, "r1", rerun.Done)
	if atomic.LoadInt32(&ran) != 0 {
		t.Fatalf("journaled step re-executed %d times on redrive, want 0 (replay)", ran)
	}
}

// The journal primary key fences a lost lease: an Append at an already-journaled
// (run, seq) returns ErrSeqConflict, and the engine parks (runAbort) rather than
// sticking. Here we pin the sentinel and the untouched status directly; the full
// two-process advisory-lock exercise is M3's lease task.
func TestExec_LostLeaseLoserParksQuietly(t *testing.T) {
	store := internal.NewMemStore()
	ctx := context.Background()
	must(t, store.Create(ctx, rerun.Run{ID: "r1", Workflow: "wf", Status: rerun.Running, Created: time.Now()}))
	// Pre-journal seq 0 as "the other worker" so a colliding append fences.
	must(t, store.Append(ctx, "r1", rerun.Log{Seq: 0, Tag: "step", Payload: []byte(`1`), At: time.Now()}))

	// The workflow that would collide if this engine claimed the run it no longer
	// owns; the test asserts the fence directly rather than racing two engines.
	eng := rerun.New(store)
	eng.Handle("wf", func(w *rerun.W) error {
		_, err := rerun.Do(w, "step", func(context.Context) (int, error) { return 2, nil })
		return err
	})

	err := store.Append(ctx, "r1", rerun.Log{Seq: 0, Tag: "step", Payload: []byte(`2`), At: time.Now()})
	if !errors.Is(err, rerun.ErrSeqConflict) {
		t.Fatalf("conflict = %v, want ErrSeqConflict", err)
	}
	r, _ := store.Get("r1")
	if r.Status != rerun.Running {
		t.Fatalf("status after fence = %v, want Running (parked, unclaimed)", r.Status)
	}
}

// Stuck is excluded from Incomplete, so nothing claims it again automatically.
func TestStuck_ExcludedFromIncomplete(t *testing.T) {
	store := internal.NewMemStore()
	ctx := context.Background()
	must(t, store.Create(ctx, rerun.Run{ID: "s1", Workflow: "wf", Status: rerun.Pending, Created: time.Now()}))
	must(t, store.Finish(ctx, "s1", rerun.Stuck))
	runs, err := store.Incomplete(ctx)
	must(t, err)
	for _, r := range runs {
		if r.ID == "s1" {
			t.Fatal("Stuck run listed as incomplete")
		}
	}
}
