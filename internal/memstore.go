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

// Package internal holds the in-memory Store: the engine's default and a test
// aid. It lives under internal/ so it is not part of the public API.
package internal

import (
	"context"
	"io"
	"sync"

	"github.com/sylvester-francis/rerun"
)

// MemStore is an in-memory rerun.Store backed by maps and a mutex.
type MemStore struct {
	mu   sync.Mutex
	runs map[string]rerun.Run
	logs map[string][]rerun.Log
}

// NewMemStore returns an empty MemStore.
func NewMemStore() *MemStore { panic("rerun: not implemented") }

func (s *MemStore) Create(ctx context.Context, r rerun.Run) error {
	panic("rerun: not implemented")
}

func (s *MemStore) Append(ctx context.Context, runID string, l rerun.Log) error {
	panic("rerun: not implemented")
}

func (s *MemStore) Finish(ctx context.Context, runID string, st rerun.Status) error {
	panic("rerun: not implemented")
}

func (s *MemStore) LoadLogs(ctx context.Context, runID string) ([]rerun.Log, error) {
	panic("rerun: not implemented")
}

func (s *MemStore) Incomplete(ctx context.Context) ([]rerun.Run, error) {
	panic("rerun: not implemented")
}

func (s *MemStore) Acquire(ctx context.Context, runID string) (io.Closer, error) {
	panic("rerun: not implemented")
}

// Get returns a run by ID. It is a test aid used by the examples.
func (s *MemStore) Get(id string) (rerun.Run, bool) { panic("rerun: not implemented") }
