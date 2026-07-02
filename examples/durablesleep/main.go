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

// Command durablesleep shows a durable Sleep: the wait happens once on the
// first run and is skipped on recovery.
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/sylvester-francis/rerun"
	"github.com/sylvester-francis/rerun/internal"
)

func waitDone(s *internal.MemStore, id string) {
	for i := 0; i < 5000; i++ {
		if r, ok := s.Get(id); ok && (r.Status == rerun.Done || r.Status == rerun.Failed) {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func main() {
	store := internal.NewMemStore()
	eng := rerun.New(store) // real wall clock

	eng.Handle("reminder", func(w *rerun.W) error {
		rerun.Sleep(w, 1*time.Second)
		rerun.Do(w, "fire", func(context.Context) (struct{}, error) {
			fmt.Println("  [live] reminder fired")
			return struct{}{}, nil
		})
		return nil
	})

	ctx := context.Background()

	fmt.Println("first run (must actually wait out the 1s sleep):")
	t0 := time.Now()
	eng.Start(ctx, "reminder", "s1")
	waitDone(store, "s1")
	fmt.Printf("first run took %v\n", time.Since(t0).Round(100*time.Millisecond))

	fmt.Println("simulating crash, then recovering:")
	store.Finish(ctx, "s1", rerun.Running)
	t1 := time.Now()
	eng.Recover(ctx)
	waitDone(store, "s1")
	fmt.Printf("recovery took %v (sleep already satisfied, so it skips the wait)\n", time.Since(t1).Round(100*time.Millisecond))
}
