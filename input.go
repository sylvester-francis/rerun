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

import "fmt"

// reservedPrefix marks journal entries and tags that are engine metadata rather
// than workflow steps. Reserved metadata is journaled at a negative sequence
// number, so exec can separate it from the positional step journal (seq >= 0)
// that replay matches against — a run's seed never shifts step positions.
const reservedPrefix = "rerun:"

const (
	inputTag = reservedPrefix + "input"
	inputSeq = -1
)

// Input returns the value passed to Start for this run, decoded as T. It is
// journaled as the run's seed at Start, so replay sees the same value every
// time; a run started without input yields the zero value.
func Input[T any](w *W) (T, error) {
	var v T
	if len(w.input) == 0 {
		return v, nil
	}
	if err := w.eng.codec.Unmarshal(w.input, &v); err != nil {
		return v, fmt.Errorf("rerun: decode input for run %s: %w", w.RunID, err)
	}
	return v, nil
}
