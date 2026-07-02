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

//go:build postgres

// These tests run against a real PostgreSQL database, so they are gated behind
// the `postgres` build tag and excluded from the default suite. Run them with a
// live database via `make pg-test`, which boots postgres:16-alpine in Docker and
// sets RERUN_PG_DSN.
package postgres_test

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/sylvester-francis/rerun"
	"github.com/sylvester-francis/rerun/postgres"
	"github.com/sylvester-francis/rerun/storetest"
)

func dsn(t *testing.T) string {
	t.Helper()
	d := os.Getenv("RERUN_PG_DSN")
	if d == "" {
		t.Fatal("RERUN_PG_DSN is not set; run the Postgres tests with: make pg-test")
	}
	return d
}

// TestPostgresStore runs the same contract every backend must pass against a
// real database. Passing here is what lets the multi-process claim be trusted.
func TestPostgresStore(t *testing.T) {
	d := dsn(t)

	// Ensure the schema exists, then start from a clean slate so repeated runs
	// against a persistent database do not collide.
	postgres.New(d).Close()
	raw, err := sql.Open("postgres", d)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`TRUNCATE runs, journal, signals`); err != nil {
		t.Fatal(err)
	}

	storetest.RunStoreContract(t, func() rerun.Store {
		return postgres.New(d)
	})
}

func TestPostgresSignaler(t *testing.T) {
	d := dsn(t)
	postgres.New(d).Close() // ensure the schema exists
	raw, err := sql.Open("postgres", d)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`TRUNCATE signals`); err != nil {
		t.Fatal(err)
	}

	storetest.RunSignalerContract(t, func() rerun.Signaler {
		return postgres.New(d)
	})
}

// TestPostgresStore_CrossProcessLease proves the lease spans processes: two
// independent Store instances (two connection pools, as two processes would
// have) contend for one run, and the advisory lock lets exactly one win.
func TestPostgresStore_CrossProcessLease(t *testing.T) {
	d := dsn(t)
	ctx := context.Background()

	a := postgres.New(d) // "process A"
	b := postgres.New(d) // "process B"
	defer a.Close()
	defer b.Close()

	relA, ok, err := a.Acquire(ctx, "lease-run")
	if err != nil || !ok {
		t.Fatalf("A should acquire the lease (ok=%v err=%v)", ok, err)
	}
	if _, held, err := b.Acquire(ctx, "lease-run"); err != nil || held {
		t.Fatalf("B must be refused while A holds the lease (held=%v err=%v)", held, err)
	}
	if err := relA.Close(); err != nil {
		t.Fatal(err)
	}
	relB, ok, err := b.Acquire(ctx, "lease-run")
	if err != nil || !ok {
		t.Fatalf("B should acquire the lease after A releases (ok=%v err=%v)", ok, err)
	}
	if err := relB.Close(); err != nil {
		t.Fatal(err)
	}
}
