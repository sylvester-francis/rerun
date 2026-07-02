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
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sylvester-francis/rerun"
)

func TestHandle_DuplicatePanics(t *testing.T) {
	eng, _, _ := setup(t)
	eng.Handle("wf", func(w *rerun.W) error { return nil })
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on duplicate Handle")
		}
		if !strings.Contains(fmt.Sprint(r), "duplicate") {
			t.Fatalf("panic %v does not mention 'duplicate'", r)
		}
	}()
	eng.Handle("wf", func(w *rerun.W) error { return nil })
}

func TestRecover_OnlyIncomplete(t *testing.T) {
	eng, store, _ := setup(t)
	var calls int32
	eng.Handle("wf", func(w *rerun.W) error {
		atomic.AddInt32(&calls, 1)
		rerun.Do(w, "s", func(context.Context) (int, error) { return 1, nil })
		return nil
	})
	ctx := context.Background()

	// A Done run must not be relaunched by Recover.
	must(t, store.Create(ctx, rerun.Run{ID: "done1", Workflow: "wf", Status: rerun.Done, Created: time.Now()}))
	must(t, eng.Recover(ctx))
	time.Sleep(50 * time.Millisecond) // give any errant goroutine a chance to run
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("workflow ran %d times for a Done run, want 0", got)
	}
}

func TestWorkflow_FailureSetsStatus(t *testing.T) {
	eng, store, _ := setup(t)
	eng.Handle("wf", func(w *rerun.W) error {
		_, err := rerun.Do(w, "boom", func(context.Context) (int, error) {
			return 0, errors.New("kaboom")
		})
		return err
	})
	must(t, eng.Start(context.Background(), "wf", "r1"))
	waitStatus(t, store, "r1", rerun.Failed)
}

func TestWorkflow_SuccessSetsDone(t *testing.T) {
	eng, store, _ := setup(t)
	eng.Handle("wf", func(w *rerun.W) error {
		rerun.Do(w, "s", func(context.Context) (int, error) { return 1, nil })
		return nil
	})
	must(t, eng.Start(context.Background(), "wf", "r1"))
	waitStatus(t, store, "r1", rerun.Done)
}
