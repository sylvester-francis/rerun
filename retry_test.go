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
	"sync/atomic"
	"testing"
	"time"

	"github.com/sylvester-francis/rerun"
)

func TestRetry_SucceedsAfterFailures(t *testing.T) {
	eng, store, _ := setup(t)
	var calls int32
	eng.Handle("wf", func(w *rerun.W) error {
		v, err := rerun.Retry(w, "charge",
			rerun.RetryPolicy{MaxAttempts: 5, Backoff: rerun.FixedBackoff(0)},
			func(context.Context) (string, error) {
				if atomic.AddInt32(&calls, 1) < 3 {
					return "", errors.New("transient")
				}
				return "txn", nil
			})
		if err != nil {
			return err
		}
		rerun.Return(w, v)
		return nil
	})
	ctx := context.Background()
	must(t, eng.Start(ctx, "wf", "r1"))
	waitDone(t, store, "r1")

	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("fn called %d times, want 3 (two failures then success)", got)
	}
	got, err := rerun.Result[string](ctx, eng, "r1")
	must(t, err)
	if got != "txn" {
		t.Fatalf("result = %q, want txn", got)
	}
}

func TestRetry_ExhaustsReturnsError(t *testing.T) {
	eng, store, _ := setup(t)
	var calls int32
	eng.Handle("wf", func(w *rerun.W) error {
		_, err := rerun.Retry(w, "charge",
			rerun.RetryPolicy{MaxAttempts: 3, Backoff: rerun.FixedBackoff(0)},
			func(context.Context) (int, error) {
				atomic.AddInt32(&calls, 1)
				return 0, errors.New("nope")
			})
		return err
	})
	must(t, eng.Start(context.Background(), "wf", "r1"))
	waitStatus(t, store, "r1", rerun.Failed)

	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("fn called %d times, want 3 (exhausted)", got)
	}
}

func TestRetry_BackoffIsDurable(t *testing.T) {
	eng, store, clk := setup(t)
	var calls int32
	eng.Handle("wf", func(w *rerun.W) error {
		_, err := rerun.Retry(w, "charge",
			rerun.RetryPolicy{MaxAttempts: 3, Backoff: rerun.FixedBackoff(time.Hour)},
			func(context.Context) (int, error) {
				if atomic.AddInt32(&calls, 1) < 2 {
					return 0, errors.New("transient")
				}
				return 1, nil
			})
		return err
	})
	ctx := context.Background()
	must(t, eng.Start(ctx, "wf", "r1"))
	clk.BlockUntil(1)      // attempt 0 failed; parked on the 1h backoff Sleep
	clk.Advance(time.Hour) // let the backoff elapse
	waitDone(t, store, "r1")
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("fn called %d times, want 2", got)
	}

	// The backoff is journaled: recovery replays it (deadline already past)
	// without re-charging or re-waiting.
	must(t, store.Finish(ctx, "r1", rerun.Running))
	start := time.Now()
	must(t, eng.Recover(ctx))
	waitDone(t, store, "r1")
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("after replay fn called %d times, want 2 (no re-charge)", got)
	}
	if time.Since(start) > time.Second {
		t.Fatal("replay re-waited the backoff instead of skipping it")
	}
}

func TestRetry_CancelDuringBackoff(t *testing.T) {
	eng, store, clk := setup(t)
	eng.Handle("wf", func(w *rerun.W) error {
		_, err := rerun.Retry(w, "charge",
			rerun.RetryPolicy{MaxAttempts: 3, Backoff: rerun.FixedBackoff(time.Hour)},
			func(context.Context) (int, error) { return 0, errors.New("transient") })
		return err
	})
	ctx := context.Background()
	must(t, eng.Start(ctx, "wf", "r1"))
	clk.BlockUntil(1) // attempt 0 failed; parked on the backoff Sleep
	must(t, eng.Cancel(ctx, "r1"))
	waitStatus(t, store, "r1", rerun.Cancelled)
}

func TestExpBackoff(t *testing.T) {
	b := rerun.ExpBackoff(time.Second, 10*time.Second)
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{4, 10 * time.Second},  // doubling past the cap clamps to max
		{63, 10 * time.Second}, // large attempt: capped, no overflow
	}
	for _, c := range cases {
		if got := b(c.attempt); got != c.want {
			t.Errorf("ExpBackoff attempt %d = %v, want %v", c.attempt, got, c.want)
		}
	}
}
