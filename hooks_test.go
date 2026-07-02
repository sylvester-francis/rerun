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

	"github.com/sylvester-francis/rerun"
)

func TestObserver_ReceivesAllEvents(t *testing.T) {
	spy := &spyObserver{}
	eng, store, _ := setupWith(t, rerun.WithObserver(spy))
	eng.Handle("wf", func(w *rerun.W) error {
		rerun.Do(w, "a", func(context.Context) (int, error) { return 1, nil })
		rerun.Do(w, "b", func(context.Context) (int, error) { return 2, nil })
		return nil
	})
	must(t, eng.Start(context.Background(), "wf", "r1"))
	waitDone(t, store, "r1")

	starts, steps, finishes := spy.snapshot()
	if starts != 1 {
		t.Fatalf("OnStart fired %d times, want 1", starts)
	}
	if steps != 2 {
		t.Fatalf("OnStep fired %d times, want 2", steps)
	}
	if len(finishes) != 1 || finishes[0] != rerun.Done {
		t.Fatalf("OnFinish = %v, want [Done]", finishes)
	}
}

func TestObserver_NotCalledDuringReplay(t *testing.T) {
	spy := &spyObserver{}
	eng, store, _ := setupWith(t, rerun.WithObserver(spy))
	eng.Handle("wf", func(w *rerun.W) error {
		rerun.Do(w, "a", func(context.Context) (int, error) { return 1, nil })
		rerun.Do(w, "b", func(context.Context) (int, error) { return 2, nil })
		return nil
	})
	ctx := context.Background()
	must(t, eng.Start(ctx, "wf", "r1"))
	waitDone(t, store, "r1")

	// A completed step is replayed, not re-run, so its observer event must not
	// fire a second time.
	must(t, store.Finish(ctx, "r1", rerun.Running))
	spy.clearSteps()
	must(t, eng.Recover(ctx))
	waitDone(t, store, "r1")

	_, steps, _ := spy.snapshot()
	if steps != 0 {
		t.Fatalf("OnStep fired %d times during replay, want 0", steps)
	}
}
