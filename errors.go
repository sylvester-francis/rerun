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
	"errors"
	"fmt"
)

// StepError carries a failed step's tag and message so the failure survives
// being journaled and is reconstructed on replay. It is the error type every
// replayed step failure takes: the message is preserved, but a step's original
// concrete error type is not.
//
// Because of that, a workflow must not branch on an error's type or value —
// errors.As or errors.Is against a custom type or sentinel passes on the live
// run and fails on replay (where the error is always a *StepError), a
// determinism bug as real as a diverging tag. Branch only on whether a step
// errored; to steer control flow on why it failed, journal the reason as a value
// inside the Do and branch on that value.
type StepError struct {
	Tag string
	Msg string
}

// Error renders the failed step's tag and message.
func (e *StepError) Error() string {
	return fmt.Sprintf("rerun: step %q: %s", e.Tag, e.Msg)
}

// ErrEngineClosed is returned by engine methods called after Shutdown.
var ErrEngineClosed = errors.New("rerun: engine is shut down")

// ErrRunExists reports a Create for a run ID the store already has — the signal
// Start callers use to dedupe retried requests.
var ErrRunExists = errors.New("rerun: run already exists")

// ErrSeqConflict reports an Append at a (run, seq) position that is already
// journaled: another worker owns the run (a lost lease) or the code shrank. The
// engine parks the run when it sees this.
var ErrSeqConflict = errors.New("rerun: journal sequence conflict")
