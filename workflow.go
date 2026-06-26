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

package rerun

import (
	"context"
	"time"
)

// W is the workflow handle threaded through every step. It carries the
// loaded journal and the replay cursor.
type W struct {
	RunID string

	seq    int
	logs   []Log
	replay bool
	eng    *Engine
	ctx    context.Context
}

// Do runs a step exactly once. On the first run it executes fn, journals the
// result, and returns it. On replay it returns the journaled value without
// executing fn. It panics if the tag diverges from the journal.
func Do[T any](w *W, tag string, fn func(context.Context) (T, error)) (T, error) {
	panic("rerun: not implemented")
}

// replayStep returns the journaled result for tag, panicking on a tag mismatch.
func replayStep[T any](w *W, tag string) (T, error) { panic("rerun: not implemented") }

// liveStep executes fn, journals the result, and notifies the observer.
func liveStep[T any](w *W, tag string, fn func(context.Context) (T, error)) (T, error) {
	panic("rerun: not implemented")
}

// Sleep is a durable delay: it survives restarts and is skipped on replay.
func Sleep(w *W, d time.Duration) error { panic("rerun: not implemented") }
