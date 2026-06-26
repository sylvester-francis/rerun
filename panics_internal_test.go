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

// White-box tests for the engine's panics (determinism, corruption, duplicate
// registration). These live in package rerun to reach unexported internals.
package rerun

import "testing"

func TestPanic_DeterminismMismatch(t *testing.T) { t.Skip("not implemented") }

func TestPanic_DuplicateWorkflow(t *testing.T) { t.Skip("not implemented") }
