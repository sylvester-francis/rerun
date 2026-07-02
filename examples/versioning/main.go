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

// Command versioning is Part II hard problem 4: safe deploys across in-flight
// runs. One binary runs version-2 code that added a fraud check behind Version.
// An old run pinned to v1 replays its original branch and skips the check; a new
// run journals v2 and runs it. The journal, not the code, chooses the branch.
package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/sylvester-francis/rerun"
	"github.com/sylvester-francis/rerun/internal"
)

const maxV = 2 // the newest version this build knows

func main() {
	store := internal.NewMemStore()
	eng := rerun.New(store)

	var fraudChecks int32
	eng.Handle("order", func(w *rerun.W) error {
		v := rerun.Version(w, "add-fraud-check", 1, maxV)
		rerun.Do(w, "validate", func(context.Context) (int, error) { return 1, nil })
		if v >= 2 {
			rerun.Do(w, "fraud-check", func(context.Context) (int, error) {
				atomic.AddInt32(&fraudChecks, 1)
				return 1, nil
			})
		}
		rerun.Do(w, "charge", func(context.Context) (int, error) { return 1, nil })
		return nil
	})
	ctx := context.Background()

	fmt.Println("deployed code = v2 (adds a fraud check behind Version)")

	// An in-flight run started on v1: its journal pins the version to 1, so it
	// replays the old path and skips the fraud check the new code added.
	atomic.StoreInt32(&fraudChecks, 0)
	store.Create(ctx, rerun.Run{ID: "old", Workflow: "order", Status: rerun.Running, Created: time.Now()})
	store.Append(ctx, "old", rerun.Log{Seq: 0, Tag: "version:add-fraud-check", Payload: []byte(`1`)})
	eng.Recover(ctx)
	waitDone(store, "old")
	fmt.Printf("old run (pinned to v1) fraud checks run: %d\n", atomic.LoadInt32(&fraudChecks))

	// A fresh run journals the new version and takes the new path.
	atomic.StoreInt32(&fraudChecks, 0)
	eng.Start(ctx, "order", "new")
	waitDone(store, "new")
	fmt.Printf("new run (v2)           fraud checks run: %d\n", atomic.LoadInt32(&fraudChecks))
}

func waitDone(s *internal.MemStore, id string) {
	for range 5000 {
		if r, ok := s.Get(id); ok && (r.Status == rerun.Done || r.Status == rerun.Failed) {
			return
		}
		time.Sleep(time.Millisecond)
	}
}
