# rerun

A lightweight **durable execution** library for Go. Workflows survive crashes, restarts, and deploys by journaling every step to a pluggable store. On recovery, the engine replays the journal — skipping completed steps — and resumes exactly where it left off.

The entire API fits on a napkin. Three concepts: `Do`, `Sleep`, `Recover`. No servers, no queues, no YAML. Under 1000 lines of core code, with a pure-Go SQLite default (zero CGO via `modernc.org/sqlite`).

## Why

Long-running workflows fail in the worst places — halfway through charging a card, between sending an email and recording that you sent it. `rerun` makes that safe. Wrap each side effect in `Do`, and the engine guarantees it runs **exactly once** across any number of process restarts. When a step succeeds, its result is journaled. When the process dies and comes back, completed steps return their journaled values instead of running again.

## Install

```sh
go get github.com/you/rerun
```

Requires Go 1.21+ (generics).

## Quick start

```go
package main

import (
	"context"
	"time"

	"github.com/you/rerun"
	"github.com/you/rerun/sqlite"
)

func main() {
	ctx := context.Background()

	store := sqlite.New("rerun.db")
	e := rerun.New(store)

	e.Handle("checkout", func(w *rerun.W) error {
		id, err := rerun.Do(w, "charge", func(ctx context.Context) (string, error) {
			return chargeCard(ctx)
		})
		if err != nil {
			return err
		}

		if err := rerun.Sleep(w, 24*time.Hour); err != nil {
			return err
		}

		_, err = rerun.Do(w, "email", func(ctx context.Context) (any, error) {
			return nil, sendReceipt(ctx, id)
		})
		return err
	})

	// Resume any workflows that were mid-flight when the process last died.
	e.Recover(ctx)

	// Start a new run.
	e.Start(ctx, "checkout", "order-4711")
}
```

If the process crashes after `charge` but before `email`, calling `Recover` on the next boot replays the journal: `charge` returns its stored result without re-charging, the `Sleep` is skipped because it already elapsed, and execution continues into `email`.

## Concepts

| Concept | What it does |
|---|---|
| `Do[T]` | Runs a step once, journals its result, and returns the journaled value on replay. |
| `Sleep` | A durable delay. Survives restarts and is skipped instantly during replay. |
| `Recover` | Re-launches every incomplete run so workflows resume where they stopped. |

### Determinism

Replay matches journaled steps to your code by **tag and order**. Your workflow function must call `Do`/`Sleep` in the same sequence on every run — no branching on wall-clock time or random values outside of a `Do`. A mismatch between the journal and the code is a programmer error and panics rather than corrupting state.

## Architecture

```
Consumer code
    ↓ depends on
  rerun (interfaces + engine)
    ↓ depends on
  nothing
```

Backends and extensions never import the engine; the engine imports no adapter. Responsibilities are split so each piece changes for exactly one reason:

- **`Engine`** orchestrates runs.
- **`W`** replays the journal.
- **`Store`** persists logs and run metadata.
- **`Codec`** serializes step results (JSON by default).
- **`Clock`** tells time (real clock by default; injectable for tests).
- **`Observer`** receives lifecycle events for logging and metrics.

### Pluggable storage

`Store` composes three small interfaces so consumers depend only on what they use:

- **`Writer`** — hot path: `Create`, `Append`, `Finish`.
- **`Reader`** — cold path: `LoadLogs`, `Incomplete`.
- **`Guarder`** — mutual exclusion: `Acquire`.

A monitoring dashboard imports `Reader` only. A test double implements `Guarder` as a no-op. Any `Store` implementation is drop-in — SQLite, Postgres, or in-memory — and the engine can't tell the difference.

The default `sqlite` backend is pure Go (no CGO). An in-memory store is provided for tests.

### Configuration

`New` takes functional options:

```go
e := rerun.New(store,
	rerun.WithCodec(myCodec),
	rerun.WithClock(myClock),
	rerun.WithObserver(myObserver),
)
```

## Testing

The store contract is shared, so every backend is held to the same behavior:

```go
func TestSQLiteStore(t *testing.T) {
	rerun_test.RunStoreContract(t, func() rerun.Store {
		return sqlite.New(t.TempDir() + "/test.db")
	})
}
```

Run the full suite with the race detector:

```sh
go test -race ./...
```

## License

See repository for license details.
