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
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sylvester-francis/rerun"
	"github.com/sylvester-francis/rerun/internal"
)

// mockStore is a minimal Store with toggleable failures. It deliberately does
// not implement Signaler, so it also exercises the missing-capability path.
type mockStore struct {
	mu             sync.Mutex
	status         map[string]rerun.Status
	failCreate     bool
	failLoad       bool
	failIncomplete bool
}

func newMockStore() *mockStore { return &mockStore{status: map[string]rerun.Status{}} }

func (s *mockStore) Create(ctx context.Context, r rerun.Run) error {
	if s.failCreate {
		return errors.New("mock: create failed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status[r.ID] = r.Status
	return nil
}

func (s *mockStore) Append(ctx context.Context, runID string, l rerun.Log) error { return nil }

func (s *mockStore) Finish(ctx context.Context, runID string, st rerun.Status) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status[runID] = st
	return nil
}

func (s *mockStore) LoadLogs(ctx context.Context, runID string) ([]rerun.Log, error) {
	if s.failLoad {
		return nil, errors.New("mock: load failed")
	}
	return nil, nil
}

func (s *mockStore) Incomplete(ctx context.Context) ([]rerun.Run, error) {
	if s.failIncomplete {
		return nil, errors.New("mock: incomplete failed")
	}
	return nil, nil
}

func (s *mockStore) Acquire(ctx context.Context, runID string) (io.Closer, bool, error) {
	return io.NopCloser(nil), true, nil
}

func (s *mockStore) get(runID string) rerun.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status[runID]
}

func TestExec_LoadLogsErrorFailsRun(t *testing.T) {
	st := newMockStore()
	st.failLoad = true
	eng := rerun.New(st)
	eng.Handle("wf", func(w *rerun.W) error { return nil })
	must(t, eng.Start(context.Background(), "wf", "r1"))

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if st.get("r1") == rerun.Failed {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("run did not fail on a load error; status=%v", st.get("r1"))
}

func TestRecover_IncompleteErrorReturns(t *testing.T) {
	st := newMockStore()
	st.failIncomplete = true
	eng := rerun.New(st)
	if err := eng.Recover(context.Background()); err == nil {
		t.Fatal("Recover should return the store's Incomplete error")
	}
}

func TestDeliver_MissingSignalerPanics(t *testing.T) {
	eng := rerun.New(newMockStore()) // mockStore is not a Signaler
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Deliver on a non-Signaler store should panic")
		}
		if !strings.Contains(fmt.Sprint(r), "signal") {
			t.Fatalf("panic %v should mention signals", r)
		}
	}()
	_ = eng.Deliver(context.Background(), "r1", "approval", true)
}

func TestDeliver_MarshalError(t *testing.T) {
	eng := rerun.New(internal.NewMemStore())
	// A channel cannot be JSON-marshaled, so Deliver returns a marshal error
	// rather than pushing a broken payload.
	if err := eng.Deliver(context.Background(), "r1", "sig", make(chan int)); err == nil {
		t.Fatal("Deliver should return an error for an unmarshalable payload")
	}
}

func TestStart_InputMarshalPanics(t *testing.T) {
	eng := rerun.New(internal.NewMemStore())
	eng.Handle("wf", func(w *rerun.W) error { return nil })
	defer func() {
		if recover() == nil {
			t.Fatal("Start with an unmarshalable input should panic")
		}
	}()
	_ = eng.Start(context.Background(), "wf", "r1", make(chan int))
}

func TestInput_DecodeErrorFailsRun(t *testing.T) {
	eng, store, _ := setup(t)
	eng.Handle("wf", func(w *rerun.W) error {
		_, err := rerun.Input[int](w) // seed is a string, so decoding to int fails
		return err
	})
	must(t, eng.Start(context.Background(), "wf", "r1", "not-an-int"))
	waitStatus(t, store, "r1", rerun.Failed)
}

func TestSleep_ContextCancelled(t *testing.T) {
	// A run now outlives the caller's context (see
	// TestStart_RunOutlivesCallerContext); its own context ends only via Cancel,
	// which unwinds a parked Sleep into Cancelled.
	eng, store, clk := setup(t)
	eng.Handle("wf", func(w *rerun.W) error {
		return rerun.Sleep(w, time.Hour)
	})
	must(t, eng.Start(context.Background(), "wf", "r1"))
	clk.BlockUntil(1) // the sleep is parked
	must(t, eng.Cancel(context.Background(), "r1"))
	waitStatus(t, store, "r1", rerun.Cancelled)
}

func TestWithCodec_Applied(t *testing.T) {
	spy := &spyCodec{}
	eng, store, _ := setupWith(t, rerun.WithCodec(spy))
	eng.Handle("wf", func(w *rerun.W) error {
		rerun.Do(w, "s", func(context.Context) (int, error) { return 1, nil })
		return nil
	})
	must(t, eng.Start(context.Background(), "wf", "r1"))
	waitDone(t, store, "r1")
	if !spy.used() {
		t.Fatal("WithCodec was not applied; custom codec never invoked")
	}
}

func TestDefaultWallClock(t *testing.T) {
	// No WithClock: the engine uses the real wall clock (clock.go), and a short
	// real sleep exercises both Now and After.
	store := internal.NewMemStore()
	eng := rerun.New(store)
	eng.Handle("wf", func(w *rerun.W) error {
		return rerun.Sleep(w, time.Millisecond)
	})
	must(t, eng.Start(context.Background(), "wf", "r1"))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r, ok := store.Get("r1"); ok && r.Status == rerun.Done {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("run with the default wall clock did not finish")
}

func TestStatus_String(t *testing.T) {
	cases := map[rerun.Status]string{
		rerun.Pending:    "Pending",
		rerun.Running:    "Running",
		rerun.Done:       "Done",
		rerun.Failed:     "Failed",
		rerun.Cancelled:  "Cancelled",
		rerun.Status(99): "Status(99)",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("Status(%d).String() = %q, want %q", int(s), got, want)
		}
	}
}

func TestStart_CreateErrorReturns(t *testing.T) {
	st := newMockStore()
	st.failCreate = true
	eng := rerun.New(st)
	eng.Handle("wf", func(w *rerun.W) error { return nil })
	if err := eng.Start(context.Background(), "wf", "r1"); err == nil {
		t.Fatal("Start should return the store's Create error")
	}
}

func TestWait_ContextCancelled(t *testing.T) {
	// Wait unwinds when the run's context ends. Under engine-owned lifetimes that
	// happens via Cancel, not the caller's context.
	eng, store, _ := setup(t)
	eng.Handle("wf", func(w *rerun.W) error {
		_, err := rerun.Wait[bool](w, "never-arrives")
		return err
	})
	must(t, eng.Start(context.Background(), "wf", "r1"))
	time.Sleep(10 * time.Millisecond) // let Wait enter its poll loop
	must(t, eng.Cancel(context.Background(), "r1")) // cancel while it waits for a signal that never comes
	waitStatus(t, store, "r1", rerun.Cancelled)
}

type spyCodec struct {
	mu       sync.Mutex
	marshals int
}

func (c *spyCodec) Marshal(v any) ([]byte, error) {
	c.mu.Lock()
	c.marshals++
	c.mu.Unlock()
	return []byte(`0`), nil
}

func (c *spyCodec) Unmarshal(data []byte, v any) error { return nil }

func (c *spyCodec) used() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.marshals > 0
}
