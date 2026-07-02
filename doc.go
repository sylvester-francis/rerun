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

// Package rerun is a lightweight durable execution library. Workflows survive
// crashes, restarts, and deploys by journaling every step to a pluggable store;
// on recovery the engine replays the journal, skipping completed steps, and
// resumes exactly where it left off.
//
// The whole API fits on a napkin. Do runs a step once and journals its result;
// Sleep is a durable delay; Recover resumes every unfinished run after a
// restart. A workflow is an ordinary Go function.
//
//	e := rerun.New(sqlite.New("rerun.db"))
//	e.Handle("signup", func(w *rerun.W) error {
//		id, _ := rerun.Do(w, "create-account", func(ctx context.Context) (string, error) {
//			return createAccount(ctx)
//		})
//		rerun.Sleep(w, time.Hour)
//		rerun.Do(w, "welcome-email", func(ctx context.Context) (struct{}, error) {
//			return struct{}{}, sendEmail(ctx, id)
//		})
//		return nil
//	})
//	e.Start(ctx, "signup", "user-42")
//	// ...and after a restart, in the same process:
//	e.Recover(ctx)
//
// # Determinism
//
// A workflow body must be deterministic in its control flow and its step tags.
// Anything nondeterministic — the current time, a random number, a read whose
// result steers a branch — must be captured inside a Do so its value is
// journaled and replayed, never recomputed. On replay, a tag that diverges from
// the journal panics, loudly and early, because a silent divergence corrupts
// every later step.
//
// # Guarantees
//
// rerun is durable and resumable; at-least-once for side effects; exactly-once
// only when steps are idempotent. A step repeats only if the process dies in the
// narrow window after its side effect runs and before its journal entry commits,
// which is why production steps are written to be idempotent. See the README's
// Non-goals for what rerun deliberately does not do.
//
// # Backends
//
// Persistence is the Store seam, and swapping a backend changes no engine code:
// internal.MemStore for tests and single-process programs, package sqlite for a
// single persistent node, package postgres for multi-process execution behind an
// advisory-lock lease. Any Store that passes storetest.RunStoreContract is a
// drop-in.
package rerun
