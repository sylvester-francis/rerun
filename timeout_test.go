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
	"sync/atomic"
	"testing"
	"time"

	"github.com/sylvester-francis/rerun"
)

func TestDoTimeout_SuccessWithinDeadline(t *testing.T) {
	eng, store, _ := setup(t)
	var mu sync.Mutex
	var got int
	eng.Handle("wf", func(w *rerun.W) error {
		v, err := rerun.DoTimeout(w, "fast", time.Hour, func(context.Context) (int, error) {
			return 7, nil
		})
		mu.Lock()
		got = v
		mu.Unlock()
		return err
	})
	must(t, eng.Start(context.Background(), "wf", "r1"))
	waitDone(t, store, "r1")

	mu.Lock()
	defer mu.Unlock()
	if got != 7 {
		t.Fatalf("got %d, want 7", got)
	}
}

func TestDoTimeout_TimesOutAndReplays(t *testing.T) {
	eng, store, _ := setup(t)
	var deadlines int32
	eng.Handle("wf", func(w *rerun.W) error {
		_, err := rerun.DoTimeout(w, "slow", 10*time.Millisecond, func(ctx context.Context) (int, error) {
			select {
			case <-time.After(2 * time.Second):
				return 1, nil
			case <-ctx.Done():
				atomic.AddInt32(&deadlines, 1)
				return 0, ctx.Err()
			}
		})
		return err
	})
	ctx := context.Background()
	must(t, eng.Start(ctx, "wf", "r1"))
	waitStatus(t, store, "r1", rerun.Failed)
	if got := atomic.LoadInt32(&deadlines); got != 1 {
		t.Fatalf("fn hit its deadline %d times, want 1", got)
	}

	// The timeout outcome is journaled, so replay returns it without re-running.
	must(t, store.Finish(ctx, "r1", rerun.Running))
	must(t, eng.Recover(ctx))
	waitStatus(t, store, "r1", rerun.Failed)
	if got := atomic.LoadInt32(&deadlines); got != 1 {
		t.Fatalf("after replay fn ran %d times, want 1 (timeout is journaled)", got)
	}
}
