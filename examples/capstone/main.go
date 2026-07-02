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

// Command capstone is an end-to-end signup saga — create account, charge with
// retry, durable wait, welcome email — that crashes after the charge and
// recovers without charging the card again.
package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/sylvester-francis/rerun"
	"github.com/sylvester-francis/rerun/internal"
)

// Live side effects we want to prove happen exactly once across a crash.
var (
	charges int32 // times the payment processor was actually called
	emails  int32 // welcome emails actually sent
)

func waitDone(s *internal.MemStore, id string) {
	for i := 0; i < 5000; i++ {
		if r, ok := s.Get(id); ok && (r.Status == rerun.Done || r.Status == rerun.Failed) {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

// chargeCard is a retry pattern built on the Do primitive: each attempt is its
// own journaled step, so a transient failure is recorded and the next attempt
// is a new step. On replay the whole attempt sequence is reproduced from the
// journal without calling the processor again.
func chargeCard(w *rerun.W) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		txn, err := rerun.Do(w, fmt.Sprintf("charge:attempt-%d", attempt),
			func(context.Context) (string, error) {
				n := atomic.AddInt32(&charges, 1)
				if n < 2 { // first LIVE attempt simulates a processor timeout
					fmt.Printf("    [live] charge attempt %d: processor timeout\n", attempt)
					return "", fmt.Errorf("processor timeout")
				}
				fmt.Printf("    [live] charge attempt %d: ok (txn_%d)\n", attempt, n)
				return fmt.Sprintf("txn_%d", n), nil
			})
		if err == nil {
			return txn, nil
		}
		lastErr = err
	}
	return "", fmt.Errorf("charge failed after retries: %w", lastErr)
}

func signup(w *rerun.W) error {
	rerun.Do(w, "create-account", func(context.Context) (string, error) {
		fmt.Println("    [live] create-account")
		return "acct_1", nil
	})

	if _, err := chargeCard(w); err != nil {
		return err
	}

	rerun.Sleep(w, 500*time.Millisecond) // durable "wait a bit before welcoming"

	rerun.Do(w, "send-welcome-email", func(context.Context) (struct{}, error) {
		atomic.AddInt32(&emails, 1)
		fmt.Println("    [live] send-welcome-email")
		return struct{}{}, nil
	})
	return nil
}

func main() {
	store := internal.NewMemStore()
	eng := rerun.New(store)
	eng.Handle("signup", signup)
	ctx := context.Background()

	fmt.Println("=== clean first run ===")
	eng.Start(ctx, "signup", "u1")
	waitDone(store, "u1")
	fmt.Printf("after first run: charges(live)=%d emails(live)=%d\n\n",
		atomic.LoadInt32(&charges), atomic.LoadInt32(&emails))

	// The interesting case: a process that crashed AFTER the card was charged
	// but BEFORE the welcome email. Seed exactly that partial journal for a
	// second user and recover. The card must NOT be charged again, and recovery
	// must resume at the sleep and then send the email.
	fmt.Println("=== crash recovery for u2 (crashed after charge, before email) ===")
	atomic.StoreInt32(&charges, 0)
	atomic.StoreInt32(&emails, 0)
	store.Create(ctx, rerun.Run{ID: "u2", Workflow: "signup", Status: rerun.Running, Created: time.Now()})
	store.Append(ctx, "u2", rerun.Log{Seq: 0, Tag: "create-account", Payload: []byte(`"acct_1"`)})
	store.Append(ctx, "u2", rerun.Log{Seq: 1, Tag: "charge:attempt-0", Payload: []byte(`""`), Err: "processor timeout"})
	store.Append(ctx, "u2", rerun.Log{Seq: 2, Tag: "charge:attempt-1", Payload: []byte(`"txn_2"`)})

	eng.Recover(ctx)
	waitDone(store, "u2")
	r, _ := store.Get("u2")
	fmt.Printf("u2 status Done? %v\n", r.Status == rerun.Done)
	fmt.Printf("during recovery: charges(live)=%d (card NOT re-charged), emails(live)=%d (welcome sent once)\n",
		atomic.LoadInt32(&charges), atomic.LoadInt32(&emails))
}
