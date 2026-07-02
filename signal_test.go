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
	"testing"

	"github.com/sylvester-francis/rerun"
)

func approvalWorkflow(w *rerun.W) error {
	ok, err := rerun.Wait[bool](w, "approval")
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("not approved")
	}
	return nil
}

func TestSignal_DeliveredWhileWaiting(t *testing.T) {
	eng, store, _ := setup(t)
	eng.Handle("approval", approvalWorkflow)
	ctx := context.Background()

	must(t, eng.Start(ctx, "approval", "r1"))
	must(t, eng.Deliver(ctx, "r1", "approval", true))
	waitStatus(t, store, "r1", rerun.Done)
}

func TestSignal_ReplayReturnsJournaledWithoutWaiting(t *testing.T) {
	eng, store, _ := setup(t)
	eng.Handle("approval", approvalWorkflow)
	ctx := context.Background()

	// The signal is already in the journal; recovery must return it without any
	// delivery. If Wait ignored the journal, this run would block forever.
	must(t, store.Create(ctx, rerun.Run{ID: "r1", Workflow: "approval", Status: rerun.Running}))
	must(t, store.Append(ctx, "r1", rerun.Log{Seq: 0, Tag: "signal:approval", Payload: []byte(`true`)}))
	must(t, eng.Recover(ctx))
	waitStatus(t, store, "r1", rerun.Done)
}
