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
	"fmt"
	"io"
	"sort"
	"strconv"
	"sync"

	"github.com/sylvester-francis/rerun"
)

// MemStore is an in-memory rerun.Store backed by maps and a mutex. It is the
// zero-dependency default, useful for tests and for single-process programs
// that do not need to outlive a restart.
type MemStore struct {
	mu        sync.Mutex
	runs      map[string]rerun.Run
	logs      map[string][]rerun.Log
	seen      map[string]bool
	held      map[string]bool
	signals   map[string][][]byte
	cancelReq map[string]bool
}

// NewMemStore returns an empty MemStore.
func NewMemStore() *MemStore {
	return &MemStore{
		runs:      make(map[string]rerun.Run),
		logs:      make(map[string][]rerun.Log),
		seen:      make(map[string]bool),
		held:      make(map[string]bool),
		signals:   make(map[string][][]byte),
		cancelReq: make(map[string]bool),
	}
}

// Create records a new run, rejecting a duplicate ID with rerun.ErrRunExists.
func (m *MemStore) Create(ctx context.Context, r rerun.Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.runs[r.ID]; dup {
		return fmt.Errorf("memstore: run %s already exists: %w", r.ID, rerun.ErrRunExists)
	}
	m.runs[r.ID] = r
	return nil
}

// Append adds a journal entry, rejecting a duplicate (run, seq) position with
// rerun.ErrSeqConflict.
func (m *MemStore) Append(ctx context.Context, runID string, l rerun.Log) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// The (run, seq) pair is a primary key everywhere else; enforce it here too
	// so a lost lease or a shrunken workflow fences on the same sentinel.
	k := runID + "\x00" + strconv.Itoa(l.Seq)
	if m.seen[k] {
		return fmt.Errorf("memstore: append %s seq %d: %w", runID, l.Seq, rerun.ErrSeqConflict)
	}
	m.seen[k] = true
	m.logs[runID] = append(m.logs[runID], l)
	return nil
}

// Finish sets a run's status; it errors if the run does not exist.
func (m *MemStore) Finish(ctx context.Context, runID string, s rerun.Status) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[runID]
	if !ok {
		return fmt.Errorf("memstore: run %s not found", runID)
	}
	r.Status = s
	m.runs[runID] = r
	return nil
}

// LoadLogs returns a run's journal entries sorted by sequence.
func (m *MemStore) LoadLogs(ctx context.Context, runID string) ([]rerun.Log, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	src := m.logs[runID]
	out := make([]rerun.Log, len(src))
	copy(out, src)
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

// Incomplete returns every run still Pending or Running.
func (m *MemStore) Incomplete(ctx context.Context) ([]rerun.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []rerun.Run
	for _, r := range m.runs {
		if r.Status == rerun.Pending || r.Status == rerun.Running {
			out = append(out, r)
		}
	}
	return out, nil
}

// Acquire is a non-blocking try-lock over an in-memory held set. It stands in
// for a distributed lease: many goroutines sharing one MemStore contend exactly
// as many processes sharing a database would, which is what lets the worker
// example prove exactly-once dispatch without a cluster.
func (m *MemStore) Acquire(ctx context.Context, runID string) (io.Closer, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.held[runID] {
		return nil, false, nil
	}
	m.held[runID] = true
	return closerFunc(func() error {
		m.mu.Lock()
		defer m.mu.Unlock()
		delete(m.held, runID)
		return nil
	}), true, nil
}

// Get returns a run by ID. It is a test aid used by the examples to read a run's
// status directly; it is not part of the Store contract.
func (m *MemStore) Get(id string) (rerun.Run, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	return r, ok
}

// PushSignal and PopSignal implement rerun.Signaler: a per-run, per-name mailbox
// queue. Delivery can land before the workflow reaches Wait, so events queue
// rather than hand off to a waiting goroutine — a signal delivered early sits in
// the mailbox until Wait polls and finds it.
func (m *MemStore) PushSignal(ctx context.Context, runID, name string, payload []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := sigKey(runID, name)
	m.signals[k] = append(m.signals[k], payload)
	return nil
}

// PopSignal removes and returns the oldest queued signal for (runID, name).
func (m *MemStore) PopSignal(ctx context.Context, runID, name string) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := sigKey(runID, name)
	q := m.signals[k]
	if len(q) == 0 {
		return nil, false, nil
	}
	p := q[0]
	m.signals[k] = q[1:]
	return p, true, nil
}

func sigKey(runID, name string) string { return runID + "\x00" + name }

// RequestCancel and CancelRequested implement rerun.Canceller: a durable flag
// that a run should be cancelled, so Cancel can reach a run executing elsewhere.
func (m *MemStore) RequestCancel(ctx context.Context, runID string) error {
	m.mu.Lock()
	m.cancelReq[runID] = true
	m.mu.Unlock()
	return nil
}

// CancelRequested reports whether a cancel has been requested for the run.
func (m *MemStore) CancelRequested(ctx context.Context, runID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cancelReq[runID], nil
}

// closerFunc adapts a release function to io.Closer.
type closerFunc func() error

func (f closerFunc) Close() error { return f() }
