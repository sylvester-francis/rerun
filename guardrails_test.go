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
	"testing"
	"time"

	"github.com/sylvester-francis/rerun"
)

// Cancelling a run whose step is mid-flight must journal the step's outcome
// (the work happened) and finish Cancelled — v0.1.1 panicked the process here.
func TestCancel_MidStepJournalWriteSurvives(t *testing.T) {
	eng, store, _ := setup(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	eng.Handle("wf", func(w *rerun.W) error {
		_, err := rerun.Do(w, "busy", func(context.Context) (int, error) {
			close(entered)
			<-release
			return 42, nil // ignores ctx and returns a real value after cancel
		})
		if err != nil {
			return err
		}
		_, err = rerun.Do(w, "cleanup", func(context.Context) (int, error) { return 1, nil })
		return err
	})
	must(t, eng.Start(context.Background(), "wf", "r1"))
	<-entered
	must(t, eng.Cancel(context.Background(), "r1"))
	close(release)
	waitStatus(t, store, "r1", rerun.Cancelled)

	logs, err := store.LoadLogs(context.Background(), "r1")
	must(t, err)
	found := false
	for _, l := range logs {
		if l.Tag == "busy" {
			found = true
		}
		if l.Tag == "cleanup" {
			t.Fatal("post-cancel Do executed and journaled; it must fast-fail")
		}
	}
	if !found {
		t.Fatal("mid-flight step's result was not journaled after cancel")
	}
}

func TestDo_PostCancelFastFailsWithCanceled(t *testing.T) {
	eng, store, _ := setup(t)
	got := make(chan error, 1)
	entered := make(chan struct{})
	eng.Handle("wf", func(w *rerun.W) error {
		rerun.Do(w, "first", func(ctx context.Context) (int, error) {
			close(entered)
			<-ctx.Done()
			return 0, ctx.Err()
		})
		_, err := rerun.Do(w, "second", func(context.Context) (int, error) { return 1, nil })
		got <- err
		return err
	})
	must(t, eng.Start(context.Background(), "wf", "r1"))
	<-entered
	must(t, eng.Cancel(context.Background(), "r1"))
	if err := <-got; !errors.Is(err, context.Canceled) {
		t.Fatalf("post-cancel Do returned %v, want context.Canceled", err)
	}
	waitStatus(t, store, "r1", rerun.Cancelled)
}

func TestDo_ReservedTagPanics(t *testing.T) {
	eng, _, _ := setup(t)
	caught := make(chan any, 1)
	eng.Handle("wf", func(w *rerun.W) error {
		defer func() { caught <- recover() }()
		rerun.Do(w, "rerun:sneaky", func(context.Context) (int, error) { return 1, nil })
		return nil
	})
	must(t, eng.Start(context.Background(), "wf", "r1"))
	p := <-caught
	if p == nil || !strings.Contains(p.(string), "reserved") {
		t.Fatalf("reserved-prefix tag: panic %v, want message naming the reserved prefix", p)
	}
}

func TestDo_ConcurrentUseOfWPanics(t *testing.T) {
	eng, _, _ := setup(t)
	caught := make(chan any, 2)
	eng.Handle("wf", func(w *rerun.W) error {
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				defer func() { caught <- recover() }()
				rerun.Do(w, "t", func(context.Context) (int, error) {
					time.Sleep(50 * time.Millisecond)
					return i, nil
				})
			}(i)
		}
		wg.Wait()
		return nil
	})
	must(t, eng.Start(context.Background(), "wf", "r1"))
	if p := <-caught; p == nil {
		if p = <-caught; p == nil {
			t.Fatal("concurrent Do on one W did not panic")
		}
	}
}
