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

// Command goldengen writes the v0.1 golden fixtures. It is pinned to the
// released rerun v0.1.1 module so the fixtures record what that version
// actually persisted. Run once; the artifacts are committed and never
// regenerated (see spec D16).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sylvester-francis/rerun"
	"github.com/sylvester-francis/rerun/sqlite"
)

type expect struct {
	Runs []runExpect `json:"runs"`
}

type runExpect struct {
	ID        string `json:"id"`
	Workflow  string `json:"workflow"`
	Final     string `json:"final"`
	Result    any    `json:"result,omitempty"`
	ResultErr string `json:"resultErr,omitempty"`
}

func main() {
	dir := "../../testdata/golden/v0_1"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		panic(err)
	}
	db := filepath.Join(dir, "golden.db")
	_ = os.Remove(db)
	store := sqlite.New(db)
	e := rerun.New(store)
	ctx := context.Background()

	// steps + input + result: the bread-and-butter run.
	e.Handle("order", func(w *rerun.W) error {
		id, _ := rerun.Input[string](w)
		txn, err := rerun.Do(w, "charge", func(context.Context) (string, error) {
			return "txn-" + id, nil
		})
		if err != nil {
			return err
		}
		rerun.Return(w, txn)
		return nil
	})
	// journaled step error -> Failed with terminal error.
	e.Handle("declined", func(w *rerun.W) error {
		_, err := rerun.Do(w, "charge", func(context.Context) (string, error) {
			return "", errors.New("card declined")
		})
		return err
	})
	// sleep + retry + version: the tag-shape features.
	e.Handle("features", func(w *rerun.W) error {
		v := rerun.Version(w, "golden-change", 1, 1)
		_, _ = rerun.Do(w, "v", func(context.Context) (int, error) { return v, nil })
		if err := rerun.Sleep(w, time.Millisecond); err != nil {
			return err
		}
		out, err := rerun.Retry(w, "flaky",
			rerun.RetryPolicy{MaxAttempts: 2, Backoff: rerun.FixedBackoff(time.Millisecond)},
			func(context.Context) (string, error) { return "ok", nil })
		if err != nil {
			return err
		}
		rerun.Return(w, out)
		return nil
	})
	// signal: delivered before Wait so the run completes unattended.
	e.Handle("approval", func(w *rerun.W) error {
		ok, err := rerun.Wait[bool](w, "go")
		if err != nil || !ok {
			return err
		}
		rerun.Return(w, "approved")
		return nil
	})

	// v0.1.1's SQLite lease is a single process-wide mutex (the very defect M1
	// Task 5 fixes), so runs started concurrently would race for one lock and all
	// but one would be silently dropped. Drive them one at a time — Start, then
	// wait for the run to leave the incomplete set and the lease to release — so
	// each produces a real, complete v0.1.1 journal.
	drive := func(wf, id string, in ...any) {
		must(e.Start(ctx, wf, id, in...))
		waitSettled(store, ctx, id)
	}
	must(e.Deliver(ctx, "r-approval", "go", true))
	drive("order", "r-order", "4711")
	drive("declined", "r-declined")
	drive("features", "r-features")
	drive("approval", "r-approval")

	// A mid-flight run, exactly the way the workers example fakes a crash:
	// journal written through the v0.1.1 store API, run left Running.
	must(store.Create(ctx, rerun.Run{ID: "r-midflight", Workflow: "order", Status: rerun.Running, Created: time.Now()}))
	must(store.Append(ctx, "r-midflight", rerun.Log{Seq: -1, Tag: "rerun:input", Payload: []byte(`"99"`), At: time.Now()}))
	must(store.Append(ctx, "r-midflight", rerun.Log{Seq: 0, Tag: "charge", Payload: []byte(`"txn-99"`), At: time.Now()}))

	exp := expect{Runs: []runExpect{
		{ID: "r-order", Workflow: "order", Final: "Done", Result: "txn-4711"},
		{ID: "r-declined", Workflow: "declined", Final: "Failed", ResultErr: "card declined"},
		{ID: "r-features", Workflow: "features", Final: "Done", Result: "ok"},
		{ID: "r-approval", Workflow: "approval", Final: "Done", Result: "approved"},
		{ID: "r-midflight", Workflow: "order", Final: "Done", Result: "txn-99"},
	}}
	b, err := json.MarshalIndent(exp, "", "  ")
	must(err)
	must(os.WriteFile(filepath.Join(dir, "expect.json"), b, 0o644))
	fmt.Println("golden fixtures written")
}

// waitSettled blocks until runID is no longer incomplete (has reached a terminal
// status) and then gives the single-mutex lease a moment to run its deferred
// unlock, so the next Start acquires it cleanly instead of being dropped.
func waitSettled(store *sqlite.Store, ctx context.Context, runID string) {
	for range 500 {
		runs, err := store.Incomplete(ctx)
		must(err)
		incomplete := false
		for _, r := range runs {
			if r.ID == runID {
				incomplete = true
			}
		}
		if !incomplete {
			time.Sleep(50 * time.Millisecond) // let the lease's deferred unlock run
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	panic("run " + runID + " never settled")
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
