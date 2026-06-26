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
	"io"
	"time"
)

// Status is the lifecycle state of a Run.
type Status int

const (
	Pending Status = iota
	Running
	Done
	Failed
)

// Log is an immutable journal entry: one completed step.
type Log struct {
	Seq     int
	Tag     string
	Payload []byte
	Err     string
	At      time.Time
}

// Run is workflow-instance metadata.
type Run struct {
	ID       string
	Workflow string
	Status   Status
	Created  time.Time
}

// Writer is the hot path: it persists runs and journal entries.
type Writer interface {
	Create(ctx context.Context, r Run) error
	Append(ctx context.Context, runID string, l Log) error
	Finish(ctx context.Context, runID string, s Status) error
}

// Reader is the cold path: it loads journals and finds incomplete runs.
type Reader interface {
	LoadLogs(ctx context.Context, runID string) ([]Log, error)
	Incomplete(ctx context.Context) ([]Run, error)
}

// Guarder provides mutual exclusion for a run. Backends that don't need
// locking may return io.NopCloser(nil).
type Guarder interface {
	Acquire(ctx context.Context, runID string) (io.Closer, error)
}

// Store composes the three persistence interfaces. Any implementation is a
// drop-in: the engine cannot tell SQLite from Postgres from in-memory.
type Store interface {
	Writer
	Reader
	Guarder
}
