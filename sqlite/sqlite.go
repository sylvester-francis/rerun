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
	"sync"

	"github.com/sylvester-francis/rerun"
	_ "modernc.org/sqlite"
)

// Store is a SQLite-backed rerun.Store. The mutex backs Guarder's try-lock.
type Store struct {
	db *sql.DB
	mu sync.Mutex
}

// New opens (or creates) the database at path and ensures the schema exists.
// The schema encodes two invariants directly: runs.id is a primary key (no
// duplicate run) and journal(run_id, seq) is a composite primary key (no two
// entries at the same position).
func New(path string) *Store {
	// WAL lets readers proceed alongside a writer; busy_timeout turns a
	// momentary lock into a short wait instead of an error.
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn) // modernc registers as "sqlite", not "sqlite3"
	if err != nil {
		panic(fmt.Sprintf("sqlite: open %s: %v", path, err))
	}
	db.SetMaxOpenConns(1) // single writer serializes cleanly and avoids "database is locked"

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS runs (
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
		CREATE INDEX IF NOT EXISTS signals_key ON signals (run_id, name, id);`); err != nil {
		panic(fmt.Sprintf("sqlite: schema: %v", err))
	}
	return &Store{db: db}
}

func (s *Store) Create(ctx context.Context, r rerun.Run) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO runs (id, workflow, status, created) VALUES (?, ?, ?, ?)`,
		r.ID, r.Workflow, int(r.Status), r.Created,
	)
	if err != nil {
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
		`SELECT id, workflow, status, created FROM runs WHERE status IN (?, ?)`,
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
		if err := rows.Scan(&r.ID, &r.Workflow, &st, &r.Created); err != nil {
			return nil, fmt.Errorf("sqlite: scan run: %w", err)
		}
		r.Status = rerun.Status(st)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Acquire is a non-blocking try-lock. SQLite is a single-writer database, so one
// mutex is the whole lease: a second Acquire while held reports acquired=false
// rather than blocking. A distributed backend replaces exactly this method with
// a cross-process lock (see the postgres package) and nothing else changes.
func (s *Store) Acquire(ctx context.Context, runID string) (io.Closer, bool, error) {
	if !s.mu.TryLock() {
		return nil, false, nil
	}
	return closerFunc(s.mu.Unlock), true, nil
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

// Close releases the underlying database handle.
func (s *Store) Close() error { return s.db.Close() }

// closerFunc adapts a niladic release function to io.Closer.
type closerFunc func()

func (c closerFunc) Close() error {
	c()
	return nil
}
