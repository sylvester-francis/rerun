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

// Regression tests for defects found by the M1 adversarial review.
package rerun_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/sylvester-francis/rerun"
	"github.com/sylvester-francis/rerun/sqlite"
)

// Bug B: a workflow body that runs longer than WithStoreTimeout must still
// record its terminal status. The persistence context for a terminal write is
// anchored at the write, not at exec entry, so a slow run does not write against
// an already-expired context and get silently left non-terminal.
func TestExec_LongRunStillRecordsTerminalStatus(t *testing.T) {
	store := sqlite.New(filepath.Join(t.TempDir(), "slow.db"))
	defer store.Close()
	eng := rerun.New(store, rerun.WithStoreTimeout(80*time.Millisecond))
	eng.Handle("wf", func(w *rerun.W) error {
		_, err := rerun.Do(w, "slow", func(context.Context) (int, error) {
			time.Sleep(250 * time.Millisecond) // exceeds the store timeout
			return 1, nil
		})
		return err
	})
	must(t, eng.Start(context.Background(), "wf", "r1"))

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		runs, err := store.Incomplete(context.Background())
		must(t, err)
		stillIncomplete := false
		for _, r := range runs {
			if r.ID == "r1" {
				stillIncomplete = true
			}
		}
		if !stillIncomplete {
			return // reached a terminal status
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("run never reached a terminal status: terminal write used an expired persistence context")
}

// Bug F: a run cancelled just before its `Return(v); return nil` tail must finish
// Cancelled — not Done. The workflow returns nil only because Return swallowed
// the fast-fail error, so werr==nil must not by itself mean success.
func TestCancel_BeforeReturnMarksCancelledNotDone(t *testing.T) {
	eng, store, _ := setup(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	eng.Handle("wf", func(w *rerun.W) error {
		rerun.Do(w, "gate", func(context.Context) (int, error) {
			close(entered)
			<-release
			return 1, nil // completes with a real value after the cancel lands
		})
		rerun.Return(w, "result-value")
		return nil
	})
	must(t, eng.Start(context.Background(), "wf", "r1"))
	<-entered
	must(t, eng.Cancel(context.Background(), "r1"))
	close(release)
	waitStatus(t, store, "r1", rerun.Cancelled)

	// It must not also have (incorrectly) recorded a result.
	if _, err := rerun.Result[string](context.Background(), eng, "r1"); err == nil {
		t.Fatal("cancelled run recorded a result; Return must not have journaled after cancel")
	}
}

// Bug E: Sleep must propagate the cancellation of its journaled step instead of
// returning nil (which would report the sleep completed and let the workflow run
// on past a cancel).
func TestSleep_AfterCancelPropagatesError(t *testing.T) {
	eng, store, _ := setup(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	sleepErr := make(chan error, 1)
	eng.Handle("wf", func(w *rerun.W) error {
		rerun.Do(w, "gate", func(context.Context) (int, error) {
			close(entered)
			<-release
			return 1, nil
		})
		err := rerun.Sleep(w, time.Hour) // called with an already-cancelled context
		sleepErr <- err
		return err
	})
	must(t, eng.Start(context.Background(), "wf", "r1"))
	<-entered
	must(t, eng.Cancel(context.Background(), "r1"))
	close(release)

	if err := <-sleepErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("Sleep after cancel returned %v, want context.Canceled", err)
	}
	waitStatus(t, store, "r1", rerun.Cancelled)
}
