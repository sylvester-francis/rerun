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
	"context"
	"testing"

	"github.com/sylvester-francis/rerun"
)

func TestStart_UnmarshalableInputReturnsError(t *testing.T) {
	eng, store, _ := setup(t)
	eng.Handle("wf", func(w *rerun.W) error { return nil })
	err := eng.Start(context.Background(), "wf", "r1", make(chan int)) // json can't marshal a channel
	if err == nil {
		t.Fatal("Start with unmarshalable input must return an error, not panic")
	}
	if _, ok := store.Get("r1"); ok {
		t.Fatal("failed Start must not leave a half-created run behind")
	}
}

func TestStart_InputTravelsInsideRun(t *testing.T) {
	eng, store, _ := setup(t)
	got := make(chan string, 1)
	eng.Handle("wf", func(w *rerun.W) error {
		v, err := rerun.Input[string](w)
		if err != nil {
			return err
		}
		got <- v
		return nil
	})
	must(t, eng.Start(context.Background(), "wf", "r1", "seed-42"))
	waitDone(t, store, "r1")
	if v := <-got; v != "seed-42" {
		t.Fatalf("input = %q, want seed-42", v)
	}
	r, _ := store.Get("r1")
	if len(r.Input) == 0 {
		t.Fatal("input was not persisted atomically inside the Run record")
	}
}
