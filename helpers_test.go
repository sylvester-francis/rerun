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

package rerun_test

import (
	"sync"
	"testing"
	"time"

	"github.com/sylvester-francis/rerun"
	"github.com/sylvester-francis/rerun/internal"
)

func setup(t *testing.T) (*rerun.Engine, *internal.MemStore, *fakeClock) {
	t.Helper()
	store := internal.NewMemStore()
	clk := newFakeClock()
	eng := rerun.New(store, rerun.WithClock(clk))
	return eng, store, clk
}

func setupWith(t *testing.T, opts ...rerun.Opt) (*rerun.Engine, *internal.MemStore, *fakeClock) {
	t.Helper()
	store := internal.NewMemStore()
	clk := newFakeClock()
	all := append([]rerun.Opt{rerun.WithClock(clk)}, opts...)
	eng := rerun.New(store, all...)
	return eng, store, clk
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func waitDone(t *testing.T, s *internal.MemStore, runID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r, ok := s.Get(runID); ok && (r.Status == rerun.Done || r.Status == rerun.Failed) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("run %s did not finish within timeout", runID)
}

func waitStatus(t *testing.T, s *internal.MemStore, runID string, want rerun.Status) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r, ok := s.Get(runID); ok && r.Status == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("run %s did not reach status %v within timeout", runID, want)
}

// fakeClock is a virtual clock: After records a waiter on a timeline the test
// advances by hand, so a workflow that sleeps a day is testable in microseconds.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []fakeWaiter
}

type fakeWaiter struct {
	deadline time.Time
	ch       chan time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Now()}
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- f.now
		return ch
	}
	f.waiters = append(f.waiters, fakeWaiter{deadline: f.now.Add(d), ch: ch})
	return ch
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
	var remaining []fakeWaiter
	for _, w := range f.waiters {
		if !w.deadline.After(f.now) {
			w.ch <- f.now
		} else {
			remaining = append(remaining, w)
		}
	}
	f.waiters = remaining
}

// BlockUntil waits until n waiters are registered. A test must call it before
// Advance: Start runs the workflow in a goroutine, so the sleep's waiter may not
// exist yet, and advancing too early delivers the wakeup to no one.
func (f *fakeClock) BlockUntil(n int) {
	for {
		f.mu.Lock()
		count := len(f.waiters)
		f.mu.Unlock()
		if count >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

// spyObserver records every lifecycle event so tests can assert on them.
type spyObserver struct {
	mu       sync.Mutex
	starts   []rerun.Run
	steps    []rerun.Log
	finishes []rerun.Status
}

func (s *spyObserver) OnStart(r rerun.Run) {
	s.mu.Lock()
	s.starts = append(s.starts, r)
	s.mu.Unlock()
}

func (s *spyObserver) OnStep(id string, l rerun.Log) {
	s.mu.Lock()
	s.steps = append(s.steps, l)
	s.mu.Unlock()
}

func (s *spyObserver) OnFinish(id string, st rerun.Status) {
	s.mu.Lock()
	s.finishes = append(s.finishes, st)
	s.mu.Unlock()
}

func (s *spyObserver) snapshot() (starts, steps int, finishes []rerun.Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fin := make([]rerun.Status, len(s.finishes))
	copy(fin, s.finishes)
	return len(s.starts), len(s.steps), fin
}

func (s *spyObserver) clearSteps() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steps = nil
}
