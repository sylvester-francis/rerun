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
	"time"

	"github.com/sylvester-francis/rerun"
)

func TestCancel_UnwindsParkedSleep(t *testing.T) {
	eng, store, clk := setup(t)
	eng.Handle("wf", func(w *rerun.W) error {
		return rerun.Sleep(w, time.Hour)
	})
	ctx := context.Background()
	must(t, eng.Start(ctx, "wf", "r1"))
	clk.BlockUntil(1) // parked on the sleep

	must(t, eng.Cancel(ctx, "r1"))
	waitStatus(t, store, "r1", rerun.Cancelled)
}

func TestCancel_NotRunningErrors(t *testing.T) {
	eng, _, _ := setup(t)
	if err := eng.Cancel(context.Background(), "nonexistent"); err == nil {
		t.Fatal("Cancel of a run not running in this process should error")
	}
}
