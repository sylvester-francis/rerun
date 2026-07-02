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
	"sync"
	"testing"

	"github.com/sylvester-francis/rerun"
)

func TestInput_ThreadedAndReplayed(t *testing.T) {
	eng, store, _ := setup(t)
	var mu sync.Mutex
	var seen []string
	eng.Handle("greet", func(w *rerun.W) error {
		name, err := rerun.Input[string](w)
		if err != nil {
			return err
		}
		mu.Lock()
		seen = append(seen, name)
		mu.Unlock()
		rerun.Do(w, "hello", func(context.Context) (string, error) { return "hi " + name, nil })
		return nil
	})
	ctx := context.Background()
	must(t, eng.Start(ctx, "greet", "r1", "Ada"))
	waitDone(t, store, "r1")

	// The seed must survive a crash and replay identically.
	must(t, store.Finish(ctx, "r1", rerun.Running))
	must(t, eng.Recover(ctx))
	waitDone(t, store, "r1")

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 || seen[0] != "Ada" || seen[1] != "Ada" {
		t.Fatalf("input seen = %v, want [Ada Ada] (seed must survive recovery)", seen)
	}
}

func TestInput_AbsentYieldsZero(t *testing.T) {
	eng, store, _ := setup(t)
	var mu sync.Mutex
	var got string
	var ran bool
	eng.Handle("wf", func(w *rerun.W) error {
		v, err := rerun.Input[string](w)
		mu.Lock()
		got, ran = v, true
		mu.Unlock()
		return err
	})
	must(t, eng.Start(context.Background(), "wf", "r1")) // no seed
	waitDone(t, store, "r1")

	mu.Lock()
	defer mu.Unlock()
	if !ran || got != "" {
		t.Fatalf("Input without a seed = %q (ran=%v), want empty string", got, ran)
	}
}
