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
	"testing"
	"time"

	"github.com/sylvester-francis/rerun"
	"github.com/sylvester-francis/rerun/internal"
)

func TestCancel_UnwindsParkedSleep(t *testing.T) {
	eng, store, clk := setup(t)
	eng.Handle("wf", func(w *rerun.W) error {
		return rerun.Sleep(w, time.Hour)
	})
	ctx := context.Background()
	must(t, eng.Start(ctx, "wf", "r1"))
	clk.BlockUntil(1) // parked on the sleep

	must(t, eng.Cancel(ctx, "r1"))
	waitStatus(t, store, "r1", rerun.Cancelled)
}

func TestCancel_NoCancellerErrors(t *testing.T) {
	// A store with no Canceller capability: cancelling a run that isn't running
	// in this process has nowhere to record the request, so it errors.
	eng := rerun.New(newMockStore())
	if err := eng.Cancel(context.Background(), "nonexistent"); err == nil {
		t.Fatal("Cancel with no local run and no Canceller store should error")
	}
}

func TestCancel_CrossProcess(t *testing.T) {
	store := internal.NewMemStore()
	clk := newFakeClock()
	// Two engines over one store stand in for two processes; only A runs the run.
	a := rerun.New(store, rerun.WithClock(clk), rerun.WithCancelPoll(5*time.Millisecond))
	b := rerun.New(store, rerun.WithClock(clk))
	wf := func(w *rerun.W) error { return rerun.Sleep(w, time.Hour) }
	a.Handle("wf", wf)
	b.Handle("wf", wf)

	ctx := context.Background()
	must(t, a.Start(ctx, "wf", "r1"))
	clk.BlockUntil(1) // A's run is parked on the sleep

	// The run isn't local to B, so Cancel records the request in the store; A's
	// poller observes it and unwinds the run.
	must(t, b.Cancel(ctx, "r1"))
	waitStatus(t, store, "r1", rerun.Cancelled)
}
