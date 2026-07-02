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

// Package storetest is the importable contract suite. Any rerun.Store
// implementation can prove itself correct by passing RunStoreContract, so the
// SQLite, Postgres, and in-memory backends are all held to one behavior.
//
// It lives in an ordinary package, deliberately not a _test.go file, because Go
// forbids importing a test package: a contract trapped in rerun_test could never
// be reused by the backend tests.
package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/sylvester-francis/rerun"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// RunStoreContract exercises a Store against the behavior every backend must
// honor: create-and-load, seq ordering, incomplete filtering, status
// transitions, lease exclusivity, and duplicate-create rejection.
func RunStoreContract(t *testing.T, makeStore func() rerun.Store) {
	ctx := context.Background()

	t.Run("create and load", func(t *testing.T) {
		s := makeStore()
		must(t, s.Create(ctx, rerun.Run{ID: "r1", Workflow: "wf", Status: rerun.Pending, Created: time.Now()}))
		must(t, s.Append(ctx, "r1", rerun.Log{Seq: 0, Tag: "a", Payload: []byte(`"x"`), At: time.Now()}))
		must(t, s.Append(ctx, "r1", rerun.Log{Seq: 1, Tag: "b", Payload: []byte(`"y"`), At: time.Now()}))
		logs, err := s.LoadLogs(ctx, "r1")
		must(t, err)
		if len(logs) != 2 {
			t.Fatalf("want 2 logs, got %d", len(logs))
		}
		if logs[0].Tag != "a" || logs[1].Tag != "b" {
			t.Fatalf("tags out of order: %q, %q", logs[0].Tag, logs[1].Tag)
		}
	})

	t.Run("logs return in seq order", func(t *testing.T) {
		// Insert out of order (2, 0, 1). A backend that forgot ORDER BY passes
		// casual testing but fails here — replay matches entries by position.
		s := makeStore()
		must(t, s.Create(ctx, rerun.Run{ID: "r2", Workflow: "wf", Status: rerun.Pending, Created: time.Now()}))
		must(t, s.Append(ctx, "r2", rerun.Log{Seq: 2, Tag: "c", Payload: []byte(`1`), At: time.Now()}))
		must(t, s.Append(ctx, "r2", rerun.Log{Seq: 0, Tag: "a", Payload: []byte(`2`), At: time.Now()}))
		must(t, s.Append(ctx, "r2", rerun.Log{Seq: 1, Tag: "b", Payload: []byte(`3`), At: time.Now()}))
		logs, err := s.LoadLogs(ctx, "r2")
		must(t, err)
		for i, l := range logs {
			if l.Seq != i {
				t.Fatalf("seq %d at index %d, ordering broken", l.Seq, i)
			}
		}
	})

	t.Run("incomplete filters correctly", func(t *testing.T) {
		s := makeStore()
		must(t, s.Create(ctx, rerun.Run{ID: "done1", Workflow: "wf", Status: rerun.Pending, Created: time.Now()}))
		must(t, s.Finish(ctx, "done1", rerun.Done))
		must(t, s.Create(ctx, rerun.Run{ID: "active1", Workflow: "wf", Status: rerun.Running, Created: time.Now()}))
		must(t, s.Create(ctx, rerun.Run{ID: "pending1", Workflow: "wf", Status: rerun.Pending, Created: time.Now()}))
		runs, err := s.Incomplete(ctx)
		must(t, err)
		ids := map[string]bool{}
		for _, r := range runs {
			ids[r.ID] = true
		}
		if ids["done1"] {
			t.Fatal("completed run returned as incomplete")
		}
		if !ids["active1"] || !ids["pending1"] {
			t.Fatal("missing an incomplete run")
		}
	})

	t.Run("finish transitions status", func(t *testing.T) {
		s := makeStore()
		must(t, s.Create(ctx, rerun.Run{ID: "r3", Workflow: "wf", Status: rerun.Pending, Created: time.Now()}))
		must(t, s.Finish(ctx, "r3", rerun.Failed))
		runs, err := s.Incomplete(ctx)
		must(t, err)
		for _, r := range runs {
			if r.ID == "r3" {
				t.Fatal("failed run still listed as incomplete")
			}
		}
	})

	t.Run("lock is exclusive then releasable", func(t *testing.T) {
		// Acquire, prove a second acquire is refused while held, release, prove
		// acquire succeeds again. This one case pins the in-memory held set, the
		// SQLite mutex, and the Postgres advisory lock to the same guarantee.
		s := makeStore()
		c1, ok, err := s.Acquire(ctx, "r1")
		must(t, err)
		if !ok {
			t.Fatal("first acquire should succeed")
		}
		if _, held, err := s.Acquire(ctx, "r1"); err != nil || held {
			t.Fatalf("second acquire should be refused while held (held=%v err=%v)", held, err)
		}
		must(t, c1.Close())
		c2, ok, err := s.Acquire(ctx, "r1")
		must(t, err)
		if !ok {
			t.Fatal("acquire should succeed after release")
		}
		must(t, c2.Close())
	})

	t.Run("duplicate create errors", func(t *testing.T) {
		s := makeStore()
		r := rerun.Run{ID: "dup", Workflow: "wf", Status: rerun.Pending, Created: time.Now()}
		must(t, s.Create(ctx, r))
		if err := s.Create(ctx, r); err == nil {
			t.Fatal("duplicate create should error")
		}
	})
}
