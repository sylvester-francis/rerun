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

// Package sqlite is a persistent rerun.Store backed by SQLite via the pure-Go,
// cgo-free driver modernc.org/sqlite, so the result stays a single static
// binary with no C toolchain required.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/sylvester-francis/rerun"
	_ "modernc.org/sqlite"
)

// Store is a SQLite-backed rerun.Store. The mutex guards a per-run held set that
// backs Guarder's try-lock, so distinct runs lease independently.
type Store struct {
	db   *sql.DB
	mu   sync.Mutex
	held map[string]bool
}

// migrations is the ordered, append-only schema history. Migration 1 is the
// v0.1 schema verbatim; a database that predates schema_version is adopted at
// version 1 without modification. Never edit an entry — append.
var migrations = []string{
	`CREATE TABLE IF NOT EXISTS runs (
		id       TEXT PRIMARY KEY,
		workflow TEXT NOT NULL,
		status   INTEGER NOT NULL,
		created  DATETIME NOT NULL
	);
	CREATE TABLE IF NOT EXISTS journal (
		run_id  TEXT NOT NULL,
		seq     INTEGER NOT NULL,
		tag     TEXT NOT NULL,
		payload BLOB,
		err     TEXT,
		at      DATETIME NOT NULL,
		PRIMARY KEY (run_id, seq)
	);
	CREATE TABLE IF NOT EXISTS signals (
		id      INTEGER PRIMARY KEY AUTOINCREMENT,
		run_id  TEXT NOT NULL,
		name    TEXT NOT NULL,
		payload BLOB
	);
	CREATE INDEX IF NOT EXISTS signals_key ON signals (run_id, name, id);
	CREATE TABLE IF NOT EXISTS cancellations (run_id TEXT PRIMARY KEY);`,

	`ALTER TABLE runs ADD COLUMN input BLOB;
	CREATE INDEX IF NOT EXISTS runs_status ON runs (status);`,
}

// migrate brings db up to the latest schema version. A pre-schema_version v0.1
// database is adopted at version 1 (its tables already match migration 1) and
// then carried forward; a fresh database runs every migration in order.
func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("schema_version: %w", err)
	}
	var v int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&v); err != nil {
		return fmt.Errorf("read version: %w", err)
	}
	if v == 0 {
		// A v0.1 database has tables but no version row: adopt it at 1.
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='runs'`).Scan(&n); err != nil {
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
		// DDL and its version row commit together (SQLite has transactional DDL),
		// so a crash mid-migration rolls the DDL back and the migration re-runs
		// cleanly — a partial apply can never brick the database. This matters
		// because migration 2's ALTER TABLE ADD COLUMN is not idempotent.
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("migration %d: begin: %w", i+1, err)
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_version (version) VALUES (?)`, i+1); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %d: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %d: commit: %w", i+1, err)
		}
	}
	return nil
}

// New opens (or creates) the database at path and migrates it to the latest
// schema. The schema encodes two invariants directly: runs.id is a primary key
// (no duplicate run) and journal(run_id, seq) is a composite primary key (no two
// entries at the same position). A v0.1 database is adopted and upgraded in place.
func New(path string) *Store {
	// WAL lets readers proceed alongside a writer; busy_timeout turns a
	// momentary lock into a short wait instead of an error.
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn) // modernc registers as "sqlite", not "sqlite3"
	if err != nil {
		panic(fmt.Sprintf("sqlite: open %s: %v", path, err))
	}
	db.SetMaxOpenConns(1) // single writer serializes cleanly and avoids "database is locked"

	if err := migrate(db); err != nil {
		panic(fmt.Sprintf("sqlite: migrate %s: %v", path, err))
	}
	return &Store{db: db, held: make(map[string]bool)}
}

func (s *Store) Create(ctx context.Context, r rerun.Run) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO runs (id, workflow, status, created, input) VALUES (?, ?, ?, ?, ?)`,
		r.ID, r.Workflow, int(r.Status), r.Created, r.Input,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: runs.id") {
			return fmt.Errorf("sqlite: create %s: %w", r.ID, rerun.ErrRunExists)
		}
		return fmt.Errorf("sqlite: create %s: %w", r.ID, err)
	}
	return nil
}

func (s *Store) Append(ctx context.Context, runID string, l rerun.Log) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO journal (run_id, seq, tag, payload, err, at) VALUES (?, ?, ?, ?, ?, ?)`,
		runID, l.Seq, l.Tag, l.Payload, l.Err, l.At,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: journal.") {
			return fmt.Errorf("sqlite: append %s seq %d: %w", runID, l.Seq, rerun.ErrSeqConflict)
		}
		return fmt.Errorf("sqlite: append %s seq %d: %w", runID, l.Seq, err)
	}
	return nil
}

func (s *Store) Finish(ctx context.Context, runID string, st rerun.Status) error {
	_, err := s.db.ExecContext(ctx, `UPDATE runs SET status = ? WHERE id = ?`, int(st), runID)
	if err != nil {
		return fmt.Errorf("sqlite: finish %s: %w", runID, err)
	}
	return nil
}

func (s *Store) LoadLogs(ctx context.Context, runID string) ([]rerun.Log, error) {
	// ORDER BY seq is load-bearing: replay matches journal entries to Do calls
	// by position, so rows must return in sequence order.
	rows, err := s.db.QueryContext(ctx,
		`SELECT seq, tag, payload, err, at FROM journal WHERE run_id = ? ORDER BY seq`,
		runID,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: load logs %s: %w", runID, err)
	}
	defer rows.Close()

	var out []rerun.Log
	for rows.Next() {
		var l rerun.Log
		if err := rows.Scan(&l.Seq, &l.Tag, &l.Payload, &l.Err, &l.At); err != nil {
			return nil, fmt.Errorf("sqlite: scan log %s: %w", runID, err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) Incomplete(ctx context.Context) ([]rerun.Run, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, workflow, status, created, input FROM runs WHERE status IN (?, ?)`,
		int(rerun.Pending), int(rerun.Running),
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: incomplete: %w", err)
	}
	defer rows.Close()

	var out []rerun.Run
	for rows.Next() {
		var r rerun.Run
		var st int
		if err := rows.Scan(&r.ID, &r.Workflow, &st, &r.Created, &r.Input); err != nil {
			return nil, fmt.Errorf("sqlite: scan run: %w", err)
		}
		r.Status = rerun.Status(st)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Acquire is a non-blocking, per-run try-lock over an in-process held set.
// SQLite's single-writer I/O is already serialized by the connection cap; the
// lease's only job is run-level mutual exclusion, so distinct runs lease
// independently. Two *processes* sharing one SQLite file are not supported for
// execution — that is what the postgres backend is for — and the journal primary
// key fences the unsupported case.
func (s *Store) Acquire(ctx context.Context, runID string) (io.Closer, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.held[runID] {
		return nil, false, nil
	}
	s.held[runID] = true
	return closerFunc(func() {
		s.mu.Lock()
		delete(s.held, runID)
		s.mu.Unlock()
	}), true, nil
}

// PushSignal and PopSignal implement rerun.Signaler: a durable, per-run,
// per-name FIFO mailbox. A signal delivered before the workflow reaches Wait
// waits in the table until Wait pops it, and survives a crash in between.
func (s *Store) PushSignal(ctx context.Context, runID, name string, payload []byte) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO signals (run_id, name, payload) VALUES (?, ?, ?)`, runID, name, payload)
	if err != nil {
		return fmt.Errorf("sqlite: push signal %s/%s: %w", runID, name, err)
	}
	return nil
}

func (s *Store) PopSignal(ctx context.Context, runID, name string) ([]byte, bool, error) {
	// Delete-and-return the oldest matching signal in one statement, so a pop is
	// atomic (SetMaxOpenConns(1) also serializes writers).
	var payload []byte
	err := s.db.QueryRowContext(ctx,
		`DELETE FROM signals WHERE id = (
			SELECT id FROM signals WHERE run_id = ? AND name = ? ORDER BY id LIMIT 1
		) RETURNING payload`, runID, name).Scan(&payload)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("sqlite: pop signal %s/%s: %w", runID, name, err)
	}
	return payload, true, nil
}

// RequestCancel and CancelRequested implement rerun.Canceller: a durable cancel
// flag so Cancel can reach a run executing in another process.
func (s *Store) RequestCancel(ctx context.Context, runID string) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO cancellations (run_id) VALUES (?)`, runID)
	if err != nil {
		return fmt.Errorf("sqlite: request cancel %s: %w", runID, err)
	}
	return nil
}

func (s *Store) CancelRequested(ctx context.Context, runID string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM cancellations WHERE run_id = ?`, runID).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("sqlite: cancel requested %s: %w", runID, err)
	}
	return true, nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error { return s.db.Close() }

// closerFunc adapts a niladic release function to io.Closer.
type closerFunc func()

func (c closerFunc) Close() error {
	c()
	return nil
}
