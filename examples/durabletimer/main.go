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

// Command durabletimer shows a durable timer that, after a crash, waits only
// the time remaining until its journaled deadline. A manual clock makes a
// one-hour timer testable in microseconds; the deadline in the journal, not the
// duration in the code, decides how long recovery waits.
package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sylvester-francis/rerun"
	"github.com/sylvester-francis/rerun/internal"
)

func main() {
	clk := newManualClock()
	store := internal.NewMemStore()
	eng := rerun.New(store, rerun.WithClock(clk))
	eng.Handle("timer", func(w *rerun.W) error {
		rerun.Sleep(w, time.Hour)
		rerun.Do(w, "fire", func(context.Context) (struct{}, error) { return struct{}{}, nil })
		return nil
	})
	ctx := context.Background()

	// Case A: crashed forty minutes into a one-hour sleep, so the journaled
	// deadline is twenty minutes in the future.
	deadlineA := clk.Now().Add(20 * time.Minute).UnixNano()
	store.Create(ctx, rerun.Run{ID: "a", Workflow: "timer", Status: rerun.Running, Created: clk.Now()})
	store.Append(ctx, "a", rerun.Log{Seq: 0, Tag: "sleep:1h0m0s", Payload: []byte(fmt.Sprintf("%d", deadlineA))})
	eng.Recover(ctx)
	clk.blockUntil(1) // wait until the recovered run parks on its remaining 20m
	clk.Advance(20 * time.Minute)
	waitDone(store, "a")
	fmt.Printf("Case A (crashed mid-sleep): recovered run waited the 20m remainder of a 1h sleep, fired=%v\n", isDone(store, "a"))

	// Case B: the journaled deadline has already elapsed, so recovery waits not
	// at all.
	deadlineB := clk.Now().Add(-time.Minute).UnixNano()
	store.Create(ctx, rerun.Run{ID: "b", Workflow: "timer", Status: rerun.Running, Created: clk.Now()})
	store.Append(ctx, "b", rerun.Log{Seq: 0, Tag: "sleep:1h0m0s", Payload: []byte(fmt.Sprintf("%d", deadlineB))})
	eng.Recover(ctx)
	waitDone(store, "b")
	fmt.Printf("Case B (deadline already elapsed): resumed instantly with no wait, fired=%v\n", isDone(store, "b"))
}

func isDone(s *internal.MemStore, id string) bool {
	r, ok := s.Get(id)
	return ok && r.Status == rerun.Done
}

func waitDone(s *internal.MemStore, id string) {
	for range 5000 {
		if r, ok := s.Get(id); ok && (r.Status == rerun.Done || r.Status == rerun.Failed) {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

// manualClock is the fake clock from the test harness, in a main package:
// virtual time the demo advances by hand, with blockUntil to wait for a
// workflow to park before time moves.
type manualClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []clockWaiter
}

type clockWaiter struct {
	deadline time.Time
	ch       chan time.Time
}

func newManualClock() *manualClock { return &manualClock{now: time.Now()} }

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- c.now
		return ch
	}
	c.waiters = append(c.waiters, clockWaiter{deadline: c.now.Add(d), ch: ch})
	return ch
}

func (c *manualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	var rest []clockWaiter
	for _, w := range c.waiters {
		if !w.deadline.After(c.now) {
			w.ch <- c.now
		} else {
			rest = append(rest, w)
		}
	}
	c.waiters = rest
}

func (c *manualClock) blockUntil(n int) {
	for {
		c.mu.Lock()
		k := len(c.waiters)
		c.mu.Unlock()
		if k >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
}
