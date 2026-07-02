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
	"sync/atomic"
	"testing"

	"github.com/sylvester-francis/rerun"
)

func TestVersion_OldRunKeepsItsBranch(t *testing.T) {
	eng, store, _ := setup(t)
	var fraud int32
	eng.Handle("order", func(w *rerun.W) error {
		v := rerun.Version(w, "add-fraud-check", 1, 2)
		if v >= 2 {
			rerun.Do(w, "fraud-check", func(context.Context) (int, error) {
				atomic.AddInt32(&fraud, 1)
				return 1, nil
			})
		}
		rerun.Do(w, "charge", func(context.Context) (int, error) { return 1, nil })
		return nil
	})
	ctx := context.Background()

	// An in-flight run pinned to v1 replays its original branch: no fraud check.
	must(t, store.Create(ctx, rerun.Run{ID: "old", Workflow: "order", Status: rerun.Running}))
	must(t, store.Append(ctx, "old", rerun.Log{Seq: 0, Tag: "version:add-fraud-check", Payload: []byte(`1`)}))
	must(t, eng.Recover(ctx))
	waitDone(t, store, "old")
	if got := atomic.LoadInt32(&fraud); got != 0 {
		t.Fatalf("old run ran %d fraud checks, want 0 (pinned to v1)", got)
	}

	// A fresh run journals v2 and takes the new branch.
	atomic.StoreInt32(&fraud, 0)
	must(t, eng.Start(ctx, "order", "new"))
	waitDone(t, store, "new")
	if got := atomic.LoadInt32(&fraud); got != 1 {
		t.Fatalf("new run ran %d fraud checks, want 1 (v2)", got)
	}
}
