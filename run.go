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

import "context"

// Start creates a Pending run and launches it in its own goroutine.
func (e *Engine) Start(ctx context.Context, workflow, runID string) error {
	panic("rerun: not implemented")
}

// exec acquires the run lock, loads the journal, runs the workflow, and
// records the terminal status.
func (e *Engine) exec(ctx context.Context, r Run) { panic("rerun: not implemented") }

// Recover relaunches every incomplete run so workflows resume after a restart.
func (e *Engine) Recover(ctx context.Context) error { panic("rerun: not implemented") }
