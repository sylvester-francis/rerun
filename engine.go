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
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Func is a registered workflow body.
type Func func(w *W) error

// Engine orchestrates runs: it owns the workflow registry, the swappable seams
// (store, codec, clock, observer), and — since runs must outlive the contexts
// of whoever starts them — the root context every run descends from. Only
// Shutdown ends that root, and it ends it with a "parked" cause so in-flight
// runs stop without being marked terminal.
type Engine struct {
	store Store
	codec Codec
	clock Clock
	obs   Observer
	reg   map[string]Func
	mu    sync.RWMutex

	root       context.Context
	rootCancel context.CancelCauseFunc
	wg         sync.WaitGroup
	closed     atomic.Bool

	// cancels maps a running run to the cause-aware cancel func of its exec
	// context, so Cancel can unwind it with errCancelRequested. Guarded by cmu.
	cmu     sync.Mutex
	cancels map[string]context.CancelCauseFunc

	// cancelPoll is how often a running run checks the store for a cross-process
	// cancel request; zero disables the check (in-process Cancel only).
	cancelPoll time.Duration

	// storeTimeout bounds every journal and status write (see pctx). Zero means
	// no timeout.
	storeTimeout time.Duration
}

// The two ways an exec context ends besides a step's own failure. exec reads
// them back with context.Cause to decide between Cancelled and parking.
var (
	errCancelRequested = errors.New("rerun: run cancelled")
	errParked          = errors.New("rerun: run parked for later resumption")
)

// Opt configures an Engine at construction time.
type Opt func(*Engine)

// New builds an Engine over a Store, applying defaults (JSON codec, wall clock,
// no-op observer) before any options.
func New(s Store, opts ...Opt) *Engine {
	root, rootCancel := context.WithCancelCause(context.Background())
	e := &Engine{
		store:        s,
		codec:        jsonCodec{},
		clock:        wall{},
		obs:          noopObserver{},
		reg:          make(map[string]Func),
		root:         root,
		rootCancel:   rootCancel,
		cancels:      make(map[string]context.CancelCauseFunc),
		storeTimeout: 30 * time.Second,
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

// Shutdown parks the engine: the dispatcher-facing methods start refusing,
// every run's context is cancelled with errParked (runs unwind, stay
// incomplete in the store, and resume on a later engine), and Shutdown waits
// for the run goroutines to return or ctx to expire.
func (e *Engine) Shutdown(ctx context.Context) error {
	if !e.closed.CompareAndSwap(false, true) {
		return nil
	}
	e.rootCancel(errParked)
	done := make(chan struct{})
	go func() { e.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// spawn runs exec on its own goroutine under the engine root. Every run
// goroutine — Start, Recover, Redrive — must go through spawn so Shutdown
// can account for it.
func (e *Engine) spawn(r Run) {
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.exec(e.root, r)
	}()
}

// WithCodec overrides the serialization seam.
func WithCodec(c Codec) Opt { return func(e *Engine) { e.codec = c } }

// WithClock overrides the time seam.
func WithClock(c Clock) Opt { return func(e *Engine) { e.clock = c } }

// WithObserver overrides the observability seam.
func WithObserver(o Observer) Opt { return func(e *Engine) { e.obs = o } }

// WithCancelPoll enables cross-process cancellation: each running run checks the
// store for a cancel request every d (the store must implement Canceller). Zero,
// the default, disables the check so a run parks for free and only in-process
// Cancel applies. Cross-process cancellation is eventual, within one interval.
func WithCancelPoll(d time.Duration) Opt { return func(e *Engine) { e.cancelPoll = d } }

// WithStoreTimeout bounds every journal and status write. The write context is
// detached from the run's cancellation on purpose: a cancel must never be able
// to lose the record of work that already happened. The default is 30s.
func WithStoreTimeout(d time.Duration) Opt { return func(e *Engine) { e.storeTimeout = d } }

// pctx is the persistence context for a store write: it survives the run's
// cancellation and is bounded by the store timeout.
func (e *Engine) pctx(ctx context.Context) (context.Context, context.CancelFunc) {
	base := context.WithoutCancel(ctx)
	if e.storeTimeout <= 0 {
		return base, func() {}
	}
	return context.WithTimeout(base, e.storeTimeout)
}

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

// lookupSafe is lookup for the claim path: an unknown name returns (nil, false)
// for exec to park on rather than a panic. A run whose workflow is registered on
// a different worker in a heterogeneous fleet must not crash the worker that
// happens to claim it.
func (e *Engine) lookupSafe(name string) (Func, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	fn, ok := e.reg[name]
	return fn, ok
}

// register tracks a running run's cancel func so Cancel can reach it; unregister
// clears it when the run finishes.
func (e *Engine) register(runID string, cancel context.CancelCauseFunc) {
	e.cmu.Lock()
	e.cancels[runID] = cancel
	e.cmu.Unlock()
}

func (e *Engine) unregister(runID string) {
	e.cmu.Lock()
	delete(e.cancels, runID)
	e.cmu.Unlock()
}
