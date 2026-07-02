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

package rerun_test

import (
	"errors"
	"testing"

	"github.com/sylvester-francis/rerun"
)

func TestStepError_Format(t *testing.T) {
	e := &rerun.StepError{Tag: "charge", Msg: "declined"}
	want := `rerun: step "charge": declined`
	if got := e.Error(); got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestStepError_Interface(t *testing.T) {
	var err error = &rerun.StepError{Tag: "t", Msg: "m"}
	var se *rerun.StepError
	if !errors.As(err, &se) {
		t.Fatal("StepError should satisfy error and unwrap via errors.As")
	}
}
