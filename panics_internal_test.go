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

// White-box tests for the engine's panics. They live in package rerun so they
// can construct a W and reach unexported internals, and they exercise the
// panics synchronously — never inside exec's goroutine, where a panic would
// kill the whole test process instead of failing one test.
package rerun

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestLookup_UnknownPanics(t *testing.T) {
	e := New(nil)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on unknown workflow")
		}
		if !strings.Contains(fmt.Sprint(r), "unknown") {
			t.Fatalf("panic %v does not mention 'unknown'", r)
		}
	}()
	e.lookup("nope")
}

func TestReplayStep_DeterminismPanic(t *testing.T) {
	w := &W{
		RunID:  "r1",
		logs:   []Log{{Seq: 0, Tag: "expected-tag", Payload: []byte(`0`)}},
		replay: true,
		eng:    New(nil),
		ctx:    context.Background(),
	}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on tag mismatch")
		}
		if !strings.Contains(fmt.Sprint(r), "determinism") {
			t.Fatalf("panic %v does not mention 'determinism'", r)
		}
	}()
	Do(w, "actual-tag", func(context.Context) (int, error) { return 0, nil })
}

func TestReplayStep_CorruptPayloadPanics(t *testing.T) {
	w := &W{
		RunID:  "r1",
		logs:   []Log{{Seq: 0, Tag: "s", Payload: []byte(`not-json`)}},
		replay: true,
		eng:    New(nil),
		ctx:    context.Background(),
	}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on corrupt journal payload")
		}
		if !strings.Contains(fmt.Sprint(r), "corrupt") {
			t.Fatalf("panic %v does not mention 'corrupt'", r)
		}
	}()
	Do(w, "s", func(context.Context) (int, error) { return 0, nil })
}

func TestVersion_RollbackPanics(t *testing.T) {
	// A run journaled at version 3 replayed against a build that only knows up to
	// version 2 must panic loudly rather than silently take a wrong branch.
	w := &W{
		RunID:  "r1",
		logs:   []Log{{Seq: 0, Tag: "version:add-fraud-check", Payload: []byte(`3`)}},
		replay: true,
		eng:    New(nil),
		ctx:    context.Background(),
	}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on version above max")
		}
		if !strings.Contains(fmt.Sprint(r), "unsupported by this build") {
			t.Fatalf("panic %v does not mention the rollback guard", r)
		}
	}()
	Version(w, "add-fraud-check", 1, 2)
}

func TestLiveStep_MarshalPanics(t *testing.T) {
	// A step returning an unserializable value is a programmer error: it cannot
	// be journaled, so Do panics rather than silently dropping durability.
	w := &W{RunID: "r1", eng: New(nil), ctx: context.Background()}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic marshaling an unserializable result")
		}
		if !strings.Contains(fmt.Sprint(r), "marshal failed") {
			t.Fatalf("panic %v does not mention the marshal failure", r)
		}
	}()
	Do(w, "s", func(context.Context) (chan int, error) { return make(chan int), nil })
}
