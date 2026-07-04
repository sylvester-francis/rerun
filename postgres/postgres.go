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
	"errors"
	"fmt"
	"hash/fnv"
	"io"

	"github.com/sylvester-francis/rerun"

	"github.com/lib/pq"
)

// isUnique reports whether err is a Postgres unique-violation (SQLSTATE 23505)
// on the named constraint, so Create and Append can map the driver's typed
// error onto rerun's sentinels without string matching.
func isUnique(err error, constraint string) bool {
	var pe *pq.Error
	return errors.As(err, &pe) && pe.Code == "23505" && pe.Constraint == constraint
}

// Store is a Postgres-backed rerun.Store.
type Store struct {
	db *sql.DB
}

// migrations is the ordered, append-only schema history. Migration 1 is the
// v0.1 schema verbatim; a database that predates schema_version is adopted at
// version 1 without modification. Never edit an entry — append.
var migrations = []string{
	`CREATE TABLE IF NOT EXISTS runs (
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
	);
	CREATE TABLE IF NOT EXISTS signals (
		id      BIGSERIAL PRIMARY KEY,
		run_id  TEXT NOT NULL,
		name    TEXT NOT NULL,
		payload BYTEA
	);
	CREATE INDEX IF NOT EXISTS signals_key ON signals (run_id, name, id);
	CREATE TABLE IF NOT EXISTS cancellations (run_id TEXT PRIMARY KEY);`,

	`ALTER TABLE runs ADD COLUMN IF NOT EXISTS input BYTEA;
	CREATE INDEX IF NOT EXISTS runs_status ON runs (status);`,
}

// migrate brings db up to the latest schema version, each step in its own
// transaction. A pre-schema_version v0.1 database (its runs table already
// present in the current schema) is adopted at version 1 and carried forward.
func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("schema_version: %w", err)
	}
	var v int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&v); err != nil {
		return fmt.Errorf("read version: %w", err)
	}
	if v == 0 {
		// A v0.1 database has tables but no version row: adopt it at 1. Scope the
		// probe to the current schema, where the unqualified DDL below also acts.
		var n int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'runs' AND table_schema = current_schema()`,
		).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			if _, err := db.Exec(`INSERT INTO schema_version (version) VALUES (1)`); err != nil {
				return err
			}
			v = 1
		}
	}
	for i := v; i < len(migrations); i++ {
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("migration %d: begin: %w", i+1, err)
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_version (version) VALUES ($1)`, i+1); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %d: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %d: commit: %w", i+1, err)
		}
	}
	return nil
}

// New opens the database at dsn and migrates it to the latest schema. Unlike the
// SQLite store it does not cap the pool: Postgres handles concurrency, and the
// lease's exclusivity depends on a competing Acquire landing on a second
// connection so it observes the lock as held. A v0.1 database is adopted and
// upgraded in place.
func New(dsn string) *Store {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		panic(fmt.Sprintf("postgres: open: %v", err))
	}
	if err := migrate(db); err != nil {
		panic(fmt.Sprintf("postgres: migrate: %v", err))
	}
	return &Store{db: db}
}

// Create inserts a new run row, returning rerun.ErrRunExists on a duplicate ID.
func (s *Store) Create(ctx context.Context, r rerun.Run) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO runs (id, workflow, status, created, input) VALUES ($1, $2, $3, $4, $5)`,
		r.ID, r.Workflow, int(r.Status), r.Created, r.Input,
	)
	if err != nil {
		if isUnique(err, "runs_pkey") {
			return fmt.Errorf("postgres: create %s: %w", r.ID, rerun.ErrRunExists)
		}
		return fmt.Errorf("postgres: create %s: %w", r.ID, err)
	}
	return nil
}

// Append inserts a journal row, returning rerun.ErrSeqConflict on a duplicate
// (run, seq) position.
func (s *Store) Append(ctx context.Context, runID string, l rerun.Log) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO journal (run_id, seq, tag, payload, err, at) VALUES ($1, $2, $3, $4, $5, $6)`,
		runID, l.Seq, l.Tag, l.Payload, l.Err, l.At,
	)
	if err != nil {
		if isUnique(err, "journal_pkey") {
			return fmt.Errorf("postgres: append %s seq %d: %w", runID, l.Seq, rerun.ErrSeqConflict)
		}
		return fmt.Errorf("postgres: append %s seq %d: %w", runID, l.Seq, err)
	}
	return nil
}

// Finish updates a run's status.
func (s *Store) Finish(ctx context.Context, runID string, st rerun.Status) error {
	_, err := s.db.ExecContext(ctx, `UPDATE runs SET status = $1 WHERE id = $2`, int(st), runID)
	if err != nil {
		return fmt.Errorf("postgres: finish %s: %w", runID, err)
	}
	return nil
}

// LoadLogs returns a run's journal entries ordered by sequence.
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

// Incomplete returns every run still Pending or Running.
func (s *Store) Incomplete(ctx context.Context) ([]rerun.Run, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, workflow, status, created, input FROM runs WHERE status IN ($1, $2)`,
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
		if err := rows.Scan(&r.ID, &r.Workflow, &st, &r.Created, &r.Input); err != nil {
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

// PushSignal and PopSignal implement rerun.Signaler: a durable, per-run,
// per-name FIFO mailbox. A signal delivered before the workflow reaches Wait
// waits in the table until Wait pops it, and survives a crash in between.
func (s *Store) PushSignal(ctx context.Context, runID, name string, payload []byte) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO signals (run_id, name, payload) VALUES ($1, $2, $3)`, runID, name, payload)
	if err != nil {
		return fmt.Errorf("postgres: push signal %s/%s: %w", runID, name, err)
	}
	return nil
}

// PopSignal removes and returns the oldest queued signal for (runID, name).
func (s *Store) PopSignal(ctx context.Context, runID, name string) ([]byte, bool, error) {
	// Delete-and-return the oldest matching signal in one statement. FOR UPDATE
	// SKIP LOCKED keeps concurrent poppers from taking the same row.
	var payload []byte
	err := s.db.QueryRowContext(ctx,
		`DELETE FROM signals WHERE id = (
			SELECT id FROM signals WHERE run_id = $1 AND name = $2
			ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1
		) RETURNING payload`, runID, name).Scan(&payload)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("postgres: pop signal %s/%s: %w", runID, name, err)
	}
	return payload, true, nil
}

// RequestCancel and CancelRequested implement rerun.Canceller: a durable cancel
// flag so Cancel can reach a run executing in another process.
func (s *Store) RequestCancel(ctx context.Context, runID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO cancellations (run_id) VALUES ($1) ON CONFLICT DO NOTHING`, runID)
	if err != nil {
		return fmt.Errorf("postgres: request cancel %s: %w", runID, err)
	}
	return nil
}

// CancelRequested reports whether a cancel has been recorded for the run.
func (s *Store) CancelRequested(ctx context.Context, runID string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM cancellations WHERE run_id = $1)`, runID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("postgres: cancel requested %s: %w", runID, err)
	}
	return exists, nil
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
