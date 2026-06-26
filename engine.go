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

import "sync"

// Func is a registered workflow body.
type Func func(w *W) error

// Engine orchestrates runs: it owns the registry and the swappable seams.
type Engine struct {
	store Store
	codec Codec
	clock Clock
	obs   Observer
	reg   map[string]Func
	mu    sync.RWMutex
}

// Opt configures an Engine at construction time.
type Opt func(*Engine)

// New builds an Engine over a Store, applying defaults (JSON codec, wall
// clock, no-op observer) before any options.
func New(s Store, opts ...Opt) *Engine { panic("rerun: not implemented") }

// WithCodec overrides the serialization seam.
func WithCodec(c Codec) Opt { panic("rerun: not implemented") }

// WithClock overrides the time seam.
func WithClock(c Clock) Opt { panic("rerun: not implemented") }

// WithObserver overrides the observability seam.
func WithObserver(o Observer) Opt { panic("rerun: not implemented") }

// Handle registers a workflow. It panics on a duplicate name.
func (e *Engine) Handle(name string, fn Func) { panic("rerun: not implemented") }

// lookup returns a registered workflow, panicking on an unknown name.
func (e *Engine) lookup(name string) Func { panic("rerun: not implemented") }
