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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sylvester-francis/rerun"
)

func TestDo_LiveExecution(t *testing.T) {
	eng, store, _ := setup(t)
	eng.Handle("wf", func(w *rerun.W) error {
		_, err := rerun.Do(w, "s", func(context.Context) (string, error) {
			return "hello", nil
		})
		return err
	})
	ctx := context.Background()
	must(t, eng.Start(ctx, "wf", "r1"))
	waitDone(t, store, "r1")

	logs, err := store.LoadLogs(ctx, "r1")
	must(t, err)
	if len(logs) != 1 {
		t.Fatalf("want 1 journal entry, got %d", len(logs))
	}
	if logs[0].Tag != "s" || string(logs[0].Payload) != `"hello"` {
		t.Fatalf("journal entry = tag %q payload %s, want s/\"hello\"", logs[0].Tag, logs[0].Payload)
	}
}

func TestDo_ReplaySkipsExecution(t *testing.T) {
	eng, store, _ := setup(t)
	var live int32
	eng.Handle("wf", func(w *rerun.W) error {
		rerun.Do(w, "s", func(context.Context) (int, error) {
			atomic.AddInt32(&live, 1)
			return 1, nil
		})
		return nil
	})
	ctx := context.Background()
	must(t, eng.Start(ctx, "wf", "r1"))
	waitDone(t, store, "r1")
	if got := atomic.LoadInt32(&live); got != 1 {
		t.Fatalf("first run: live=%d, want 1", got)
	}

	must(t, store.Finish(ctx, "r1", rerun.Running))
	must(t, eng.Recover(ctx))
	waitDone(t, store, "r1")
	if got := atomic.LoadInt32(&live); got != 1 {
		t.Fatalf("after replay: live=%d, want 1 (step must not re-execute)", got)
	}
}

func TestDo_ReplayReturnsJournaledValue(t *testing.T) {
	eng, store, _ := setup(t)
	var counter int32
	var mu sync.Mutex
	var seen []int32
	eng.Handle("wf", func(w *rerun.W) error {
		// A deliberately changing value: replay must return the journaled 1,
		// never a freshly-computed 2.
		v, _ := rerun.Do(w, "s", func(context.Context) (int32, error) {
			return atomic.AddInt32(&counter, 1), nil
		})
		mu.Lock()
		seen = append(seen, v)
		mu.Unlock()
		return nil
	})
	ctx := context.Background()
	must(t, eng.Start(ctx, "wf", "r1"))
	waitDone(t, store, "r1")

	must(t, store.Finish(ctx, "r1", rerun.Running))
	must(t, eng.Recover(ctx))
	waitDone(t, store, "r1")

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 || seen[0] != 1 || seen[1] != 1 {
		t.Fatalf("values seen = %v, want [1 1] (replay must return the journaled value)", seen)
	}
}

func TestDo_ReplayPreservesError(t *testing.T) {
	eng, store, _ := setup(t)
	var runs int32
	var mu sync.Mutex
	var replayErr error
	eng.Handle("wf", func(w *rerun.W) error {
		_, err := rerun.Do(w, "boom", func(context.Context) (int, error) {
			atomic.AddInt32(&runs, 1)
			return 0, errors.New("declined")
		})
		mu.Lock()
		replayErr = err
		mu.Unlock()
		return err
	})
	ctx := context.Background()
	must(t, eng.Start(ctx, "wf", "r1"))
	waitStatus(t, store, "r1", rerun.Failed)

	must(t, store.Finish(ctx, "r1", rerun.Running))
	must(t, eng.Recover(ctx))
	waitDone(t, store, "r1")

	r, _ := store.Get("r1")
	if r.Status != rerun.Failed {
		t.Fatalf("recovered status = %v, want Failed (journaled error must replay)", r.Status)
	}
	mu.Lock()
	defer mu.Unlock()
	if replayErr == nil || !strings.Contains(replayErr.Error(), "declined") {
		t.Fatalf("replayed error = %v, want a StepError containing 'declined'", replayErr)
	}
	if got := atomic.LoadInt32(&runs); got != 1 {
		t.Fatalf("step ran %d times, want 1 (replay must not re-run the failed step)", got)
	}
}

func TestMultiStepReplay_AllStepsSkipped(t *testing.T) {
	eng, store, _ := setup(t)
	var live int32
	eng.Handle("wf", func(w *rerun.W) error {
		for _, tag := range []string{"a", "b", "c"} {
			rerun.Do(w, tag, func(context.Context) (int, error) {
				atomic.AddInt32(&live, 1)
				return 1, nil
			})
		}
		return nil
	})
	ctx := context.Background()
	must(t, eng.Start(ctx, "wf", "r1"))
	waitDone(t, store, "r1")
	if got := atomic.LoadInt32(&live); got != 3 {
		t.Fatalf("first run: live=%d, want 3", got)
	}

	must(t, store.Finish(ctx, "r1", rerun.Running))
	must(t, eng.Recover(ctx))
	waitDone(t, store, "r1")
	if got := atomic.LoadInt32(&live); got != 3 {
		t.Fatalf("after full replay: live=%d, want 3 (no step may re-run)", got)
	}
}

// TestPartialReplay_ResumesMidway is the most important test in the suite: it
// seeds a half-finished journal and asserts only the unfinished step runs live.
// A fresh run has both sides of the replay guard false, so only resuming past a
// partial journal exposes the guard's && and < operators — this is the case
// that kills the guard mutants in the Day 6 gate.
func TestPartialReplay_ResumesMidway(t *testing.T) {
	eng, store, _ := setup(t)
	var live int32
	eng.Handle("wf", func(w *rerun.W) error {
		for _, tag := range []string{"a", "b", "c"} {
			rerun.Do(w, tag, func(context.Context) (int, error) {
				atomic.AddInt32(&live, 1)
				return 1, nil
			})
		}
		return nil
	})
	ctx := context.Background()

	// Crash after a and b were journaled, before c ran.
	must(t, store.Create(ctx, rerun.Run{ID: "r1", Workflow: "wf", Status: rerun.Running, Created: time.Now()}))
	must(t, store.Append(ctx, "r1", rerun.Log{Seq: 0, Tag: "a", Payload: []byte(`1`)}))
	must(t, store.Append(ctx, "r1", rerun.Log{Seq: 1, Tag: "b", Payload: []byte(`1`)}))
	must(t, eng.Recover(ctx))
	waitDone(t, store, "r1")

	if got := atomic.LoadInt32(&live); got != 1 {
		t.Fatalf("resume ran %d steps live, want 1 (only 'c')", got)
	}
}

func TestSleep_SurvivesRestart(t *testing.T) {
	eng, store, clk := setup(t)
	var fired int32
	eng.Handle("wf", func(w *rerun.W) error {
		rerun.Sleep(w, time.Hour)
		rerun.Do(w, "after", func(context.Context) (int, error) {
			atomic.AddInt32(&fired, 1)
			return 1, nil
		})
		return nil
	})
	ctx := context.Background()
	must(t, eng.Start(ctx, "wf", "r1"))
	clk.BlockUntil(1) // wait until the sleep registers its waiter
	clk.Advance(time.Hour)
	waitDone(t, store, "r1")
	if got := atomic.LoadInt32(&fired); got != 1 {
		t.Fatalf("post-sleep step fired %d times, want 1", got)
	}
}

func TestSleep_ReplaySkipsWait(t *testing.T) {
	eng, store, clk := setup(t)
	eng.Handle("wf", func(w *rerun.W) error {
		rerun.Sleep(w, 100*time.Hour)
		rerun.Do(w, "after", func(context.Context) (int, error) { return 1, nil })
		return nil
	})
	ctx := context.Background()
	must(t, eng.Start(ctx, "wf", "r1"))
	clk.BlockUntil(1)
	clk.Advance(100 * time.Hour)
	waitDone(t, store, "r1")

	// Recovery must not wait again: the deadline is already in the past, so no
	// clock advance is needed for the run to finish.
	must(t, store.Finish(ctx, "r1", rerun.Running))
	start := time.Now()
	must(t, eng.Recover(ctx))
	waitDone(t, store, "r1")
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("replay waited %v, want ~0 (a satisfied sleep is skipped)", elapsed)
	}
}
