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
	"sync"
)

// Func is a registered workflow body.
type Func func(w *W) error

// Engine orchestrates runs: it owns the workflow registry and the swappable
// seams (store, codec, clock, observer).
type Engine struct {
	store Store
	codec Codec
	clock Clock
	obs   Observer
	reg   map[string]Func
	mu    sync.RWMutex

	// cancels maps a running run to the cancel func of the context its exec
	// goroutine is using, so Cancel can unwind it. Guarded by cmu.
	cmu     sync.Mutex
	cancels map[string]context.CancelFunc
}

// Opt configures an Engine at construction time.
type Opt func(*Engine)

// New builds an Engine over a Store, applying defaults (JSON codec, wall clock,
// no-op observer) before any options.
func New(s Store, opts ...Opt) *Engine {
	e := &Engine{
		store:   s,
		codec:   jsonCodec{},
		clock:   wall{},
		obs:     noopObserver{},
		reg:     make(map[string]Func),
		cancels: make(map[string]context.CancelFunc),
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

// WithCodec overrides the serialization seam.
func WithCodec(c Codec) Opt { return func(e *Engine) { e.codec = c } }

// WithClock overrides the time seam.
func WithClock(c Clock) Opt { return func(e *Engine) { e.clock = c } }

// WithObserver overrides the observability seam.
func WithObserver(o Observer) Opt { return func(e *Engine) { e.obs = o } }

// Handle registers a workflow body under a name. It panics on a duplicate: two
// workflows sharing a name is a build-time programmer error, not a runtime
// condition to recover from.
func (e *Engine) Handle(name string, fn Func) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, dup := e.reg[name]; dup {
		panic("rerun: duplicate workflow: " + name)
	}
	e.reg[name] = fn
}

// lookup returns a registered workflow, panicking on an unknown name — a run
// referencing a workflow the engine never registered cannot be executed.
func (e *Engine) lookup(name string) Func {
	e.mu.RLock()
	defer e.mu.RUnlock()
	fn, ok := e.reg[name]
	if !ok {
		panic("rerun: unknown workflow: " + name)
	}
	return fn
}

// register tracks a running run's cancel func so Cancel can reach it; unregister
// clears it when the run finishes.
func (e *Engine) register(runID string, cancel context.CancelFunc) {
	e.cmu.Lock()
	e.cancels[runID] = cancel
	e.cmu.Unlock()
}

func (e *Engine) unregister(runID string) {
	e.cmu.Lock()
	delete(e.cancels, runID)
	e.cmu.Unlock()
}

// Cancel stops a run executing in this process: it cancels the context the
// workflow runs under, so a parked Sleep or an in-flight step unwinds and the
// run finishes Cancelled. It errors if the run is not running here — already
// finished, never started, or owned by another process. Cancellation is
// in-process only in v0.x; cross-process cancel is future work.
func (e *Engine) Cancel(ctx context.Context, runID string) error {
	e.cmu.Lock()
	cancel, ok := e.cancels[runID]
	e.cmu.Unlock()
	if !ok {
		return fmt.Errorf("rerun: run %s is not running in this process", runID)
	}
	cancel()
	return nil
}
