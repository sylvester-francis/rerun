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

// Canceller is an optional Store capability: a durable record that a run should
// be cancelled. A backend that implements it lets Cancel reach a run executing
// in another process (see WithCancelPoll). Like Signaler, it is kept separate so
// the core Store contract stays minimal — a store without it supports only
// in-process cancellation.
type Canceller interface {
	RequestCancel(ctx context.Context, runID string) error
	CancelRequested(ctx context.Context, runID string) (bool, error)
}

// Cancel stops a run. When the store can record cancels durably, the request is
// recorded FIRST — so a crash between this call and the run's terminal write
// cannot resurrect the run: the next claim sees the request and finishes it
// Cancelled. Then, if the run is executing in this process, its context unwinds
// immediately. With neither a local run nor a Canceller store, there is nothing
// to cancel with, and that is an error.
func (e *Engine) Cancel(ctx context.Context, runID string) error {
	if e.closed.Load() {
		return ErrEngineClosed
	}
	c, durable := e.store.(Canceller)
	if durable {
		if err := c.RequestCancel(ctx, runID); err != nil {
			return err
		}
	}
	e.cmu.Lock()
	cancel, local := e.cancels[runID]
	e.cmu.Unlock()
	if local {
		cancel(errCancelRequested)
		return nil
	}
	if !durable {
		return fmt.Errorf("rerun: run %s is not running in this process and its store cannot record a cancel request", runID)
	}
	return nil
}

// pollCancel watches the store for a cancel request and cancels the run's
// context when one appears. It runs only when WithCancelPoll is set and the
// store is a Canceller, and stops as soon as the run's context is done.
func (e *Engine) pollCancel(cctx context.Context, cancel context.CancelCauseFunc, c Canceller, runID string) {
	t := time.NewTicker(e.cancelPoll)
	defer t.Stop()
	for {
		select {
		case <-cctx.Done():
			return
		case <-t.C:
			if req, err := c.CancelRequested(context.Background(), runID); err == nil && req {
				cancel(errCancelRequested)
				return
			}
		}
	}
}
