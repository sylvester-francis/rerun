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
	"sync/atomic"
	"testing"

	"github.com/sylvester-francis/rerun"
)

func TestResult_ReturnedAndRead(t *testing.T) {
	eng, store, _ := setup(t)
	eng.Handle("wf", func(w *rerun.W) error {
		n, _ := rerun.Do(w, "compute", func(context.Context) (int, error) { return 42, nil })
		rerun.Return(w, n*2)
		return nil
	})
	ctx := context.Background()
	must(t, eng.Start(ctx, "wf", "r1"))
	waitDone(t, store, "r1")

	got, err := rerun.Result[int](ctx, eng, "r1")
	must(t, err)
	if got != 84 {
		t.Fatalf("Result = %d, want 84", got)
	}
}

func TestResult_FailedRunSurfacesError(t *testing.T) {
	eng, store, _ := setup(t)
	eng.Handle("wf", func(w *rerun.W) error {
		_, err := rerun.Do(w, "step", func(context.Context) (int, error) {
			return 0, errors.New("boom")
		})
		return err
	})
	ctx := context.Background()
	must(t, eng.Start(ctx, "wf", "r1"))
	waitStatus(t, store, "r1", rerun.Failed)

	_, err := rerun.Result[int](ctx, eng, "r1")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Result error = %v, want it to surface the terminal 'boom'", err)
	}
}

func TestResult_NoResultErrors(t *testing.T) {
	eng, store, _ := setup(t)
	eng.Handle("wf", func(w *rerun.W) error {
		rerun.Do(w, "step", func(context.Context) (int, error) { return 1, nil })
		return nil // never calls Return
	})
	ctx := context.Background()
	must(t, eng.Start(ctx, "wf", "r1"))
	waitDone(t, store, "r1")

	if _, err := rerun.Result[int](ctx, eng, "r1"); err == nil {
		t.Fatal("Result should error when no result was recorded")
	}
}

func TestResult_SurvivesRecovery(t *testing.T) {
	eng, store, _ := setup(t)
	var calls int32
	eng.Handle("wf", func(w *rerun.W) error {
		n, _ := rerun.Do(w, "compute", func(context.Context) (int, error) {
			atomic.AddInt32(&calls, 1)
			return 21, nil
		})
		rerun.Return(w, n*2)
		return nil
	})
	ctx := context.Background()
	must(t, eng.Start(ctx, "wf", "r1"))
	waitDone(t, store, "r1")

	must(t, store.Finish(ctx, "r1", rerun.Running))
	must(t, eng.Recover(ctx))
	waitDone(t, store, "r1")

	got, err := rerun.Result[int](ctx, eng, "r1")
	must(t, err)
	if got != 42 {
		t.Fatalf("Result after recovery = %d, want 42", got)
	}
	if c := atomic.LoadInt32(&calls); c != 1 {
		t.Fatalf("compute ran %d times, want 1 (replay must not re-run)", c)
	}
	// Return is journaled exactly once, not re-appended on replay.
	logs, _ := store.LoadLogs(ctx, "r1")
	n := 0
	for _, l := range logs {
		if l.Tag == "rerun:result" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("result journaled %d times, want 1", n)
	}
}
