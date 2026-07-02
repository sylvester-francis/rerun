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

// Command recover shows crash recovery: a run with a partial journal resumes
// and executes only its unfinished step, replaying the rest.
package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/sylvester-francis/rerun"
	"github.com/sylvester-francis/rerun/internal"
)

func waitDone(s *internal.MemStore, id string) {
	for i := 0; i < 2000; i++ {
		if r, ok := s.Get(id); ok && (r.Status == rerun.Done || r.Status == rerun.Failed) {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func main() {
	store := internal.NewMemStore()
	eng := rerun.New(store)

	var liveRuns int32
	eng.Handle("pipeline", func(w *rerun.W) error {
		for _, tag := range []string{"extract", "transform", "load"} {
			tag := tag
			rerun.Do(w, tag, func(context.Context) (int, error) {
				atomic.AddInt32(&liveRuns, 1)
				fmt.Printf("  [live] %s\n", tag)
				return 1, nil
			})
		}
		return nil
	})

	ctx := context.Background()

	// Simulate a process that crashed AFTER extract+transform were journaled
	// but BEFORE load ran: seed a partial journal directly, status Running.
	fmt.Println("seeding a crashed run with a partial journal [extract, transform]:")
	store.Create(ctx, rerun.Run{ID: "j1", Workflow: "pipeline", Status: rerun.Running, Created: time.Now()})
	store.Append(ctx, "j1", rerun.Log{Seq: 0, Tag: "extract", Payload: []byte(`1`)})
	store.Append(ctx, "j1", rerun.Log{Seq: 1, Tag: "transform", Payload: []byte(`1`)})

	fmt.Println("calling Recover:")
	eng.Recover(ctx)
	waitDone(store, "j1")

	r, _ := store.Get("j1")
	fmt.Printf("status: Done? %v\n", r.Status == rerun.Done)
	fmt.Printf("steps executed live during recovery: %d (expected 1, only 'load')\n", atomic.LoadInt32(&liveRuns))
}
