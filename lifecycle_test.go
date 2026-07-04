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

// The defect this pins: a caller's context ending must not touch the run.
func TestStart_RunOutlivesCallerContext(t *testing.T) {
	eng, store, _ := setup(t)
	release := make(chan struct{})
	eng.Handle("wf", func(w *rerun.W) error {
		_, err := rerun.Do(w, "slow", func(context.Context) (int, error) {
			<-release
			return 1, nil
		})
		return err
	})
	ctx, cancel := context.WithCancel(context.Background())
	must(t, eng.Start(ctx, "wf", "r1"))
	cancel() // the "HTTP request" ends
	time.Sleep(20 * time.Millisecond)
	close(release)
	waitStatus(t, store, "r1", rerun.Done)
}

// Shutdown parks: no terminal status, resumable by a later engine.
func TestShutdown_ParksInFlightRuns(t *testing.T) {
	store := internal.NewMemStore()
	clk := newFakeClock()
	a := rerun.New(store, rerun.WithClock(clk))
	wf := func(w *rerun.W) error {
		if err := rerun.Sleep(w, time.Hour); err != nil {
			return err
		}
		_, err := rerun.Do(w, "after", func(context.Context) (int, error) { return 1, nil })
		return err
	}
	a.Handle("wf", wf)
	must(t, a.Start(context.Background(), "wf", "r1"))
	clk.BlockUntil(1)

	must(t, a.Shutdown(context.Background()))
	r, ok := store.Get("r1")
	if !ok || r.Status != rerun.Running {
		t.Fatalf("after shutdown run is %v, want Running (parked, not cancelled)", r.Status)
	}

	clk2 := newFakeClock()
	b := rerun.New(store, rerun.WithClock(clk2))
	b.Handle("wf", wf)
	must(t, b.Recover(context.Background()))
	clk2.BlockUntil(1)
	clk2.Advance(2 * time.Hour)
	waitStatus(t, store, "r1", rerun.Done)
}

func TestShutdown_StartAfterwardsErrors(t *testing.T) {
	eng, _, _ := setup(t)
	eng.Handle("wf", func(w *rerun.W) error { return nil })
	must(t, eng.Shutdown(context.Background()))
	if err := eng.Start(context.Background(), "wf", "r1"); !errors.Is(err, rerun.ErrEngineClosed) {
		t.Fatalf("Start after Shutdown = %v, want ErrEngineClosed", err)
	}
}

// Only Engine.Cancel produces Cancelled; a completed-under-cancel run is Done.
func TestCancel_StillMarksCancelled(t *testing.T) {
	eng, store, clk := setup(t)
	eng.Handle("wf", func(w *rerun.W) error { return rerun.Sleep(w, time.Hour) })
	must(t, eng.Start(context.Background(), "wf", "r1"))
	clk.BlockUntil(1)
	must(t, eng.Cancel(context.Background(), "r1"))
	waitStatus(t, store, "r1", rerun.Cancelled)
}

// A business failure racing shutdown converges to Failed on the next claim
// (spec D2): the step error is journaled, so the resumed run re-fails cleanly.
func TestShutdown_BusinessErrorConvergesToFailed(t *testing.T) {
	store := internal.NewMemStore()
	a := rerun.New(store)
	var calls int32
	wf := func(w *rerun.W) error {
		_, err := rerun.Do(w, "boom", func(context.Context) (int, error) {
			atomic.AddInt32(&calls, 1)
			return 0, errors.New("kaboom")
		})
		return err
	}
	a.Handle("wf", wf)
	must(t, store.Create(context.Background(), rerun.Run{ID: "r1", Workflow: "wf", Status: rerun.Running, Created: time.Now()}))
	must(t, store.Append(context.Background(), "r1", rerun.Log{Seq: 0, Tag: "boom", Payload: []byte(`0`), Err: "kaboom", At: time.Now()}))
	must(t, a.Recover(context.Background()))
	waitStatus(t, store, "r1", rerun.Failed)
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("journaled failing step re-executed %d times, want 0", calls)
	}
}

// A cancel that lands, then a crash, must still cancel: the request is durable
// and honored at the next claim without executing the workflow.
func TestCancel_SurvivesRestartViaClaimCheck(t *testing.T) {
	store := internal.NewMemStore()
	clk := newFakeClock()
	a := rerun.New(store, rerun.WithClock(clk))
	var ran int32
	wf := func(w *rerun.W) error {
		atomic.AddInt32(&ran, 1)
		return rerun.Sleep(w, time.Hour)
	}
	a.Handle("wf", wf)
	must(t, a.Start(context.Background(), "wf", "r1"))
	clk.BlockUntil(1)
	must(t, a.Cancel(context.Background(), "r1"))
	waitStatus(t, store, "r1", rerun.Cancelled)

	// "Crash": pretend the terminal write was lost, as if the process died
	// between the context cancel and Finish committing.
	must(t, store.Finish(context.Background(), "r1", rerun.Running))
	atomic.StoreInt32(&ran, 0)

	b := rerun.New(store, rerun.WithClock(newFakeClock()))
	b.Handle("wf", wf)
	must(t, b.Recover(context.Background()))
	waitStatus(t, store, "r1", rerun.Cancelled)
	if atomic.LoadInt32(&ran) != 0 {
		t.Fatalf("cancelled run executed %d times at re-claim, want 0", ran)
	}
}
