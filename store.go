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
	"io"
	"time"
)

// Status is a run's lifecycle state.
type Status int

const (
	Pending Status = iota
	Running
	Done
	Failed
	// Cancelled is terminal, like Done and Failed. It is appended after Failed
	// so the existing values keep their numbers in journals and databases.
	Cancelled
)

func (s Status) String() string {
	switch s {
	case Pending:
		return "Pending"
	case Running:
		return "Running"
	case Done:
		return "Done"
	case Failed:
		return "Failed"
	case Cancelled:
		return "Cancelled"
	default:
		return fmt.Sprintf("Status(%d)", int(s))
	}
}

// Log is one immutable journal entry: the result a completed step produced.
// Persisting the result a step produced — not merely the fact that a step ran —
// is what lets recovery replay a step instead of executing it a second time.
type Log struct {
	Seq     int
	Tag     string
	Payload []byte
	Err     string
	At      time.Time
}

// Run is the metadata for a single workflow instance.
type Run struct {
	ID       string
	Workflow string
	Status   Status
	Created  time.Time
}

// Writer is the hot path: creating a run and appending step results as they
// complete.
type Writer interface {
	Create(ctx context.Context, r Run) error
	Append(ctx context.Context, runID string, l Log) error
	Finish(ctx context.Context, runID string, s Status) error
}

// Reader is the cold path: loading a journal to replay it and finding the runs
// a restart left unfinished.
type Reader interface {
	LoadLogs(ctx context.Context, runID string) ([]Log, error)
	Incomplete(ctx context.Context) ([]Run, error)
}

// Guarder leases a run to one worker. Acquire is a non-blocking try-lock:
// acquired is false when another worker already holds the run, so recovery
// across many processes never replays the same run twice. A single-process
// store that never contends may always report acquired.
type Guarder interface {
	Acquire(ctx context.Context, runID string) (release io.Closer, acquired bool, err error)
}

// Store is the whole persistence seam. It is split three ways so a consumer can
// depend on only the face it uses — a dashboard on Reader, a lease manager on
// Guarder, a log shipper on Writer — and so any backend is a drop-in the engine
// cannot tell apart.
type Store interface {
	Writer
	Reader
	Guarder
}
