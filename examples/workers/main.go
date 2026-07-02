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

// Command workers is Part II hard problem 1: exactly-once dispatch across
// concurrent workers. Four goroutines race to recover the same twelve runs; the
// Guarder try-lock ensures each run's side effect fires exactly once. Swapping
// internal.NewMemStore() for postgres.New(dsn) makes the same demo genuinely
// multi-process — the in-memory lock stands in for a Postgres advisory lock, and
// the dispatch logic is identical (proven by the Postgres store contract).
package main

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sylvester-francis/rerun"
	"github.com/sylvester-francis/rerun/internal"
)

func main() {
	store := internal.NewMemStore()
	eng := rerun.New(store)

	var finalized int32
	eng.Handle("job", func(w *rerun.W) error {
		rerun.Do(w, "work", func(context.Context) (int, error) { return 1, nil })
		rerun.Do(w, "finalize", func(context.Context) (int, error) {
			atomic.AddInt32(&finalized, 1) // the side effect that must happen exactly once
			return 1, nil
		})
		return nil
	})

	ctx := context.Background()
	const n = 12

	// Seed twelve runs that finished "work" but crashed before "finalize".
	ids := make([]string, n)
	for i := range n {
		id := fmt.Sprintf("job-%d", i)
		ids[i] = id
		store.Create(ctx, rerun.Run{ID: id, Workflow: "job", Status: rerun.Running, Created: time.Now()})
		store.Append(ctx, id, rerun.Log{Seq: 0, Tag: "work", Payload: []byte(`1`)})
	}

	fmt.Println("12 jobs, 4 concurrent workers racing the same store")

	// Four independent recoverers at once, standing in for four processes. The
	// lease, not luck, is what keeps finalize from running more than once per job.
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			eng.Recover(ctx)
		}()
	}
	wg.Wait()
	waitAllDone(store, ids)

	completed := 0
	for _, id := range ids {
		if r, ok := store.Get(id); ok && r.Status == rerun.Done {
			completed++
		}
	}
	fmt.Printf("jobs completed: %d\n", completed)
	fmt.Printf("finalize executed live: %d (expected 12: exactly once per job)\n", atomic.LoadInt32(&finalized))
}

func waitAllDone(s *internal.MemStore, ids []string) {
	for range 5000 {
		done := true
		for _, id := range ids {
			if r, ok := s.Get(id); !ok || (r.Status != rerun.Done && r.Status != rerun.Failed) {
				done = false
				break
			}
		}
		if done {
			return
		}
		time.Sleep(time.Millisecond)
	}
}
