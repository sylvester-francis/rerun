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

package rerun

import (
	"context"
	"fmt"
	"time"
)

// Signaler is an optional Store capability: a per-run mailbox for external
// events. A backend that implements it enables Wait and Deliver. Keeping it a
// separate interface — not a method on Store — is interface segregation: a
// backend with no use for signals simply does not implement it.
type Signaler interface {
	PushSignal(ctx context.Context, runID, name string, payload []byte) error
	PopSignal(ctx context.Context, runID, name string) (payload []byte, ok bool, err error)
}

func (e *Engine) signaler() Signaler {
	s, ok := e.store.(Signaler)
	if !ok {
		panic("rerun: this store does not support signals (it must implement Signaler)")
	}
	return s
}

// Deliver records an external event for a run. Call it from outside the
// workflow — an HTTP handler, a webhook, another service. Delivery may arrive
// before the workflow reaches its Wait; the event queues in the mailbox until
// Wait consumes it, so it is never lost.
func (e *Engine) Deliver(ctx context.Context, runID, name string, payload any) error {
	b, err := e.codec.Marshal(payload)
	if err != nil {
		return fmt.Errorf("rerun: deliver %s/%s: marshal: %w", runID, name, err)
	}
	return e.signaler().PushSignal(ctx, runID, name, b)
}

// Wait blocks the workflow until a signal named name arrives, then journals and
// returns its payload. On replay it returns the journaled payload without
// waiting, so a workflow that waited three days for an approval resumes with the
// approval already in hand and never waits again. It is an ordinary Do whose
// function happens to block on a mailbox instead of computing — which is why the
// received value is journaled and replayed like any other step result.
func Wait[T any](w *W, name string) (T, error) {
	return Do(w, "signal:"+name, func(ctx context.Context) (T, error) {
		sig := w.eng.signaler()
		for {
			raw, ok, err := sig.PopSignal(ctx, w.RunID, name)
			if err != nil {
				var zero T
				return zero, err
			}
			if ok {
				var v T
				if uerr := w.eng.codec.Unmarshal(raw, &v); uerr != nil {
					var zero T
					return zero, uerr
				}
				return v, nil
			}
			select {
			case <-ctx.Done():
				var zero T
				return zero, ctx.Err()
			case <-time.After(2 * time.Millisecond):
			}
		}
	})
}
