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
// being journaled and is reconstructed identically on replay.
type StepError struct {
	Tag string
	Msg string
}

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
