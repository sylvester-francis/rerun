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

package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/sylvester-francis/rerun"
	"github.com/sylvester-francis/rerun/sqlite"
	"github.com/sylvester-francis/rerun/storetest"
)

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteStore(t *testing.T) {
	storetest.RunStoreContract(t, func() rerun.Store {
		return sqlite.New(t.TempDir() + "/test.db")
	})
}

// Reopening a migrated database must be a no-op: migrate never re-runs a
// recorded migration (migration 2's ALTER is not idempotent) and never loses
// data.
func TestSQLiteMigrate_IdempotentAcrossReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mig.db")
	ctx := context.Background()
	s1 := sqlite.New(path)
	mustNoErr(t, s1.Create(ctx, rerun.Run{ID: "r1", Workflow: "wf", Status: rerun.Pending, Created: time.Now(), Input: []byte(`"seed"`)}))
	mustNoErr(t, s1.Close())

	for i := range 3 {
		s := sqlite.New(path) // must not error re-applying a migration
		runs, err := s.Incomplete(ctx)
		mustNoErr(t, err)
		found := false
		for _, r := range runs {
			if r.ID == "r1" {
				found = true
				if string(r.Input) != `"seed"` {
					t.Fatalf("reopen %d: input = %q, want \"seed\"", i, r.Input)
				}
			}
		}
		if !found {
			t.Fatalf("reopen %d: run r1 lost", i)
		}
		mustNoErr(t, s.Close())
	}
}

// A v0.1 database (original tables, no schema_version, no input column) is
// adopted at version 1 and upgraded in place without disturbing existing rows.
func TestSQLiteMigrate_AdoptsPreExistingV01Database(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v01.db")
	ctx := context.Background()

	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)")
	mustNoErr(t, err)
	_, err = raw.Exec(`
		CREATE TABLE runs (id TEXT PRIMARY KEY, workflow TEXT NOT NULL, status INTEGER NOT NULL, created DATETIME NOT NULL);
		CREATE TABLE journal (run_id TEXT NOT NULL, seq INTEGER NOT NULL, tag TEXT NOT NULL, payload BLOB, err TEXT, at DATETIME NOT NULL, PRIMARY KEY (run_id, seq));
		INSERT INTO runs (id, workflow, status, created) VALUES ('old', 'wf', 1, CURRENT_TIMESTAMP);`)
	mustNoErr(t, err)
	mustNoErr(t, raw.Close())

	// Opening with the new store adopts and migrates; Incomplete selects the new
	// input column, so it would fail outright if adoption skipped migration 2.
	s := sqlite.New(path)
	defer s.Close()
	runs, err := s.Incomplete(ctx)
	mustNoErr(t, err)
	found := false
	for _, r := range runs {
		if r.ID == "old" {
			found = true
			if len(r.Input) != 0 {
				t.Fatalf("legacy row input = %q, want empty", r.Input)
			}
		}
	}
	if !found {
		t.Fatal("legacy run was not adopted")
	}
}

func TestSQLiteSignaler(t *testing.T) {
	storetest.RunSignalerContract(t, func() rerun.Signaler {
		return sqlite.New(t.TempDir() + "/sig.db")
	})
}

func TestSQLiteCanceller(t *testing.T) {
	storetest.RunCancellerContract(t, func() rerun.Canceller {
		return sqlite.New(t.TempDir() + "/cancel.db")
	})
}
