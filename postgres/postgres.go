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

// Package postgres is a persistent, multi-process rerun.Store backed by
// PostgreSQL via the pure-Go, cgo-free driver github.com/lib/pq. Its lease is a
// session-scoped advisory lock, so a lease held by a worker that dies is
// released automatically when that worker's connection drops — no expiry, no
// heartbeat, no reaper.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"hash/fnv"
	"io"

	"github.com/sylvester-francis/rerun"

	_ "github.com/lib/pq"
)

// Store is a Postgres-backed rerun.Store.
type Store struct {
	db *sql.DB
}

// New opens the database at dsn and ensures the schema exists. Unlike the
// SQLite store it does not cap the pool: Postgres handles concurrency, and the
// lease's exclusivity depends on a competing Acquire landing on a second
// connection so it observes the lock as held.
func New(dsn string) *Store {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		panic(fmt.Sprintf("postgres: open: %v", err))
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS runs (
			id       TEXT PRIMARY KEY,
			workflow TEXT NOT NULL,
			status   INTEGER NOT NULL,
			created  TIMESTAMPTZ NOT NULL
		);
		CREATE TABLE IF NOT EXISTS journal (
			run_id  TEXT NOT NULL,
			seq     INTEGER NOT NULL,
			tag     TEXT NOT NULL,
			payload BYTEA,
			err     TEXT,
			at      TIMESTAMPTZ NOT NULL,
			PRIMARY KEY (run_id, seq)
		);`); err != nil {
		panic(fmt.Sprintf("postgres: schema: %v", err))
	}
	return &Store{db: db}
}

func (s *Store) Create(ctx context.Context, r rerun.Run) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO runs (id, workflow, status, created) VALUES ($1, $2, $3, $4)`,
		r.ID, r.Workflow, int(r.Status), r.Created,
	)
	if err != nil {
		return fmt.Errorf("postgres: create %s: %w", r.ID, err)
	}
	return nil
}

func (s *Store) Append(ctx context.Context, runID string, l rerun.Log) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO journal (run_id, seq, tag, payload, err, at) VALUES ($1, $2, $3, $4, $5, $6)`,
		runID, l.Seq, l.Tag, l.Payload, l.Err, l.At,
	)
	if err != nil {
		return fmt.Errorf("postgres: append %s seq %d: %w", runID, l.Seq, err)
	}
	return nil
}

func (s *Store) Finish(ctx context.Context, runID string, st rerun.Status) error {
	_, err := s.db.ExecContext(ctx, `UPDATE runs SET status = $1 WHERE id = $2`, int(st), runID)
	if err != nil {
		return fmt.Errorf("postgres: finish %s: %w", runID, err)
	}
	return nil
}

func (s *Store) LoadLogs(ctx context.Context, runID string) ([]rerun.Log, error) {
	// ORDER BY seq is load-bearing: replay matches journal entries to Do calls
	// by position, so rows must return in sequence order.
	rows, err := s.db.QueryContext(ctx,
		`SELECT seq, tag, payload, err, at FROM journal WHERE run_id = $1 ORDER BY seq`,
		runID,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres: load logs %s: %w", runID, err)
	}
	defer rows.Close()

	var out []rerun.Log
	for rows.Next() {
		var l rerun.Log
		if err := rows.Scan(&l.Seq, &l.Tag, &l.Payload, &l.Err, &l.At); err != nil {
			return nil, fmt.Errorf("postgres: scan log %s: %w", runID, err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) Incomplete(ctx context.Context) ([]rerun.Run, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, workflow, status, created FROM runs WHERE status IN ($1, $2)`,
		int(rerun.Pending), int(rerun.Running),
	)
	if err != nil {
		return nil, fmt.Errorf("postgres: incomplete: %w", err)
	}
	defer rows.Close()

	var out []rerun.Run
	for rows.Next() {
		var r rerun.Run
		var st int
		if err := rows.Scan(&r.ID, &r.Workflow, &st, &r.Created); err != nil {
			return nil, fmt.Errorf("postgres: scan run: %w", err)
		}
		r.Status = rerun.Status(st)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Acquire takes a session-scoped advisory lock on a dedicated connection.
// pg_try_advisory_lock is non-blocking, so a competing worker (a different
// session) observes acquired=false. If this worker's process dies the
// connection drops and Postgres releases the lock automatically, which is what
// makes a crashed run recoverable across machines.
func (s *Store) Acquire(ctx context.Context, runID string) (io.Closer, bool, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("postgres: conn: %w", err)
	}
	var ok bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, key(runID)).Scan(&ok); err != nil {
		conn.Close()
		return nil, false, fmt.Errorf("postgres: try lock %s: %w", runID, err)
	}
	if !ok {
		conn.Close()
		return nil, false, nil
	}
	return &release{conn: conn, key: key(runID)}, true, nil
}

// Close releases the underlying connection pool.
func (s *Store) Close() error { return s.db.Close() }

type release struct {
	conn *sql.Conn
	key  int64
}

func (r *release) Close() error {
	_, err := r.conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, r.key)
	r.conn.Close()
	return err
}

// key hashes a run ID into the bigint that advisory locks operate on.
func key(runID string) int64 {
	h := fnv.New64a()
	h.Write([]byte(runID))
	return int64(h.Sum64())
}
