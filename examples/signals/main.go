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

// Command signals shows a step whose value comes from outside the workflow.
// Phase A delivers an approval live; Phase B recovers a run whose signal is
// already journaled and finishes with no delivery at all — the value comes from
// the journal, which is what "waited three days across a restart" needs.
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/sylvester-francis/rerun"
	"github.com/sylvester-francis/rerun/internal"
)

func main() {
	store := internal.NewMemStore()
	eng := rerun.New(store)
	eng.Handle("approval", func(w *rerun.W) error {
		ok, err := rerun.Wait[bool](w, "approval")
		if err != nil {
			return err
		}
		rerun.Do(w, "notify", func(context.Context) (bool, error) { return ok, nil })
		return nil
	})
	ctx := context.Background()

	// Phase A: a fresh run; the approval is delivered live. Delivery may land
	// before or after the workflow reaches Wait — the mailbox queues it either way.
	eng.Start(ctx, "approval", "a")
	eng.Deliver(ctx, "a", "approval", true)
	waitDone(store, "a")
	fmt.Printf("Phase A (live delivery): finished=%v approved=%v\n", isDone(store, "a"), approved(store, "a"))

	// Phase B: a run whose signal is already journaled. Recovery returns it from
	// the journal with no delivery — else Wait would block forever.
	store.Create(ctx, rerun.Run{ID: "b", Workflow: "approval", Status: rerun.Running, Created: time.Now()})
	store.Append(ctx, "b", rerun.Log{Seq: 0, Tag: "signal:approval", Payload: []byte(`true`)})
	eng.Recover(ctx)
	waitDone(store, "b")
	fmt.Printf("Phase B (journaled signal, crash recovery): finished=%v approved=%v\n", isDone(store, "b"), approved(store, "b"))
}

func isDone(s *internal.MemStore, id string) bool {
	r, ok := s.Get(id)
	return ok && r.Status == rerun.Done
}

func approved(s *internal.MemStore, id string) bool {
	logs, _ := s.LoadLogs(context.Background(), id)
	for _, l := range logs {
		if l.Tag == "signal:approval" {
			return string(l.Payload) == "true"
		}
	}
	return false
}

func waitDone(s *internal.MemStore, id string) {
	for range 5000 {
		if r, ok := s.Get(id); ok && (r.Status == rerun.Done || r.Status == rerun.Failed) {
			return
		}
		time.Sleep(time.Millisecond)
	}
}
