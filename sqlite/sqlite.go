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

// Package sqlite is a persistent rerun.Store backed by SQLite.
//
// The implementation will use the pure-Go, cgo-free driver modernc.org/sqlite
// (imported for its side effect as `_ "modernc.org/sqlite"`), keeping the
// result a single static binary. Add that require to go.mod when implementing.
package sqlite

import (
	"context"
	"database/sql"
	"io"
	"sync"

	"github.com/sylvester-francis/rerun"
)

// Store is a SQLite-backed rerun.Store. The mutex backs Guarder.
type Store struct {
	db *sql.DB
	mu sync.Mutex
}

// New opens (or creates) the database at path and ensures the schema exists.
func New(path string) *Store { panic("rerun: not implemented") }

func (s *Store) Create(ctx context.Context, r rerun.Run) error {
	panic("rerun: not implemented")
}

func (s *Store) Append(ctx context.Context, runID string, l rerun.Log) error {
	panic("rerun: not implemented")
}

func (s *Store) Finish(ctx context.Context, runID string, st rerun.Status) error {
	panic("rerun: not implemented")
}

func (s *Store) LoadLogs(ctx context.Context, runID string) ([]rerun.Log, error) {
	panic("rerun: not implemented")
}

func (s *Store) Incomplete(ctx context.Context) ([]rerun.Run, error) {
	panic("rerun: not implemented")
}

func (s *Store) Acquire(ctx context.Context, runID string) (io.Closer, error) {
	panic("rerun: not implemented")
}
