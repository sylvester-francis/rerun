<div align="center">

# rerun

**Durable execution for Go, in a few hundred lines.**

Run a multi-step process to completion. When the machine crashes halfway through and restarts hours later, resume from where it left off instead of starting over.

[![Go Reference](https://img.shields.io/badge/go.dev-reference-007d9c?logo=go&logoColor=white)](https://pkg.go.dev/github.com/sylvester-francis/rerun)
[![Go Version](https://img.shields.io/badge/go-1.21%2B-00ADD8?logo=go&logoColor=white)](https://go.dev/dl/)
[![Tests](https://img.shields.io/badge/go%20test-race-44cc11)](#testing)
[![Core deps](https://img.shields.io/badge/core%20deps-0-success)](#design)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

`Do` · `Sleep` · `Recover` — that's the whole API.

</div>

---

## The thirty-second pitch

Completed steps are replayed from a journal rather than re-executed, so with idempotent steps there are **no double charges, no skipped steps, and no half-finished state** left behind.

It's the guarantee you'd otherwise reach for Temporal to get — without the cluster. No servers, no queues, no YAML. `rerun` is the *core idea*, not the platform around it: under 1000 lines of engine, a pure-Go SQLite default (zero CGO via `modernc.org/sqlite`), and **zero third-party dependencies in the core**.

```go
e.Handle("checkout", func(w *rerun.W) error {
	txn, err := rerun.Do(w, "charge", func(ctx context.Context) (string, error) {
		return chargeCard(ctx, order) // runs once, ever — even across crashes
	})
	if err != nil {
		return err
	}

	rerun.Sleep(w, 24*time.Hour) // durable: survives restarts, skipped on replay

	_, err = rerun.Do(w, "receipt", func(ctx context.Context) (any, error) {
		return nil, sendReceipt(ctx, txn)
	})
	return err
})
```

If the process dies after `charge` but before `receipt`, the next boot's `Recover` replays the journal: `charge` returns its stored result **without re-charging**, the `Sleep` is skipped because its deadline already passed, and execution continues into `receipt`.

## How it works

A workflow is an ordinary Go function. Each step is wrapped in `Do`, which does one of two things:

```
  first run                          replay after a crash
  ─────────                          ────────────────────
  execute the work          ┌──►     journal entry exists for this tag?
  write result to journal   │        ├─ yes → return the stored result, skip the work
  return                    │        └─ no  → execute live, journal it, continue forward
                            └─────────────────────────────────────────────────────────
```

Recovery is just *running the function again*. Steps that completed before the crash replay instantly from the journal; the first step without an entry executes for real; everything after it runs forward normally. The function is written once, as if crashes didn't exist, and the engine makes it crash-proof by recording and replaying.

> **The thing you persist is not _which step you reached_, but _the result every completed step produced_.** With the results in hand, recovery isn't guesswork.

## Install

```sh
go get github.com/sylvester-francis/rerun
```

Requires Go 1.21+ (generics power the type-safe `Do[T]`).

## Quick start

```go
package main

import (
	"context"
	"time"

	"github.com/sylvester-francis/rerun"
	"github.com/sylvester-francis/rerun/sqlite"
)

func main() {
	ctx := context.Background()

	e := rerun.New(sqlite.New("rerun.db"))

	e.Handle("checkout", func(w *rerun.W) error {
		txn, err := rerun.Do(w, "charge", func(ctx context.Context) (string, error) {
			return chargeCard(ctx)
		})
		if err != nil {
			return err
		}

		if err := rerun.Sleep(w, 24*time.Hour); err != nil {
			return err
		}

		_, err = rerun.Do(w, "receipt", func(ctx context.Context) (any, error) {
			return nil, sendReceipt(ctx, txn)
		})
		return err
	})

	e.Recover(ctx)              // resume anything that was mid-flight before this boot
	e.Start(ctx, "checkout", "order-4711") // start a new run
}
```

## The API surface

The whole surface a user touches — small because the idea is small:

| Symbol | What it's for |
|---|---|
| `New(store, ...Opt)` | Build an engine over a `Store`. |
| `WithCodec` / `WithClock` / `WithObserver` | Functional options for serialization, time, and lifecycle events. |
| `Handle(name, fn)` | Register a workflow function. |
| `Start(ctx, name, runID)` | Launch a new run in its own goroutine. |
| `Recover(ctx)` | Re-launch every incomplete run after a restart. |
| `Do[T](w, tag, fn)` | Run a step once; return its journaled value on replay. |
| `Sleep(w, d)` | A durable delay that survives restarts and is skipped on replay. |

Everything else is an interface a backend implements, or an internal detail.

## The one rule: determinism

Replay matches journaled steps to your code **by tag and order**. Your workflow must issue the same sequence of `Do`/`Sleep` calls, with the same tags, every time it runs with the same inputs. Anything non-deterministic — the clock, a random number, a read whose result steers a branch — must be captured *inside* a `Do` so its value is journaled and replayed, not recomputed.

```go
// ✗ Trap: the branch can differ between the original run and the replay.
if time.Now().Hour() < 12 {
	rerun.Do(w, "morning", ...)
} else {
	rerun.Do(w, "evening", ...)
}

// ✓ Fix: journal the decision, then branch on the recorded value.
morning, _ := rerun.Do(w, "is-morning", func(ctx context.Context) (bool, error) {
	return time.Now().Hour() < 12, nil
})
if morning {
	rerun.Do(w, "morning", ...)
} else {
	rerun.Do(w, "evening", ...)
}
```

The engine enforces this rather than hope for it: if a `Do` presents a tag that doesn't match the journal at that position, it **panics with the exact position and both tags** — a determinism bug fails loudly at the first divergence instead of quietly producing wrong results.

> Errors are results too. A step that returns "card declined" on the first run reproduces that *same* error on replay — it does not re-run the charge hoping for a different answer.

## Patterns fall out of the primitive

`Do` is the only primitive; useful shapes are just loops and conditionals over it.

**Retry** — each attempt is its own journaled step:

```go
func chargeWithRetry(w *rerun.W) (string, error) {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		txn, err := rerun.Do(w, fmt.Sprintf("charge:attempt-%d", attempt),
			func(ctx context.Context) (string, error) { return charge(ctx) })
		if err == nil {
			return txn, nil
		}
		last = err
	}
	return "", fmt.Errorf("charge failed after retries: %w", last)
}
```

On replay the whole attempt sequence is reproduced from the journal without calling the processor again.

## Design

```
Consumer code
    ↓ depends on
  rerun (interfaces + engine)
    ↓ depends on
  nothing
```

Backends and extensions never import the engine; the engine imports no adapter. Each piece changes for exactly one reason:

- **`Engine`** orchestrates runs (one goroutine per run — thousands park cheaply on a `Sleep`).
- **`W`** replays the journal.
- **`Store`** persists logs and run metadata.
- **`Codec`** serializes step results (JSON by default).
- **`Clock`** tells time (wall clock by default; injectable for tests).
- **`Observer`** receives lifecycle events for logging and metrics (no-op by default).

### Pluggable storage, segmented by need

`Store` composes three small interfaces so consumers depend only on what they use:

| Interface | Methods | Who needs it |
|---|---|---|
| `Writer` | `Create`, `Append`, `Finish` | the hot path |
| `Reader` | `LoadLogs`, `Incomplete` | a monitoring dashboard, read-only |
| `Guarder` | `Acquire` | mutual exclusion; a test double makes it a no-op |

Any `Store` is drop-in — SQLite, Postgres, or in-memory — and the engine can't tell the difference. The contract suite ships as a **public package** (`storetest`) so anyone can write a backend and prove it correct against the same bar the in-tree backends meet:

```go
func TestSQLiteStore(t *testing.T) {
	storetest.RunStoreContract(t, func() rerun.Store {
		return sqlite.New(t.TempDir() + "/test.db")
	})
}
```

### Configuration

```go
e := rerun.New(store,
	rerun.WithCodec(myCodec),
	rerun.WithClock(myClock),
	rerun.WithObserver(myObserver),
)
```

## When it panics vs. returns an error

- **Panics** for *programmer errors* that can't be recovered: non-determinism (tag mismatch), journal corruption, duplicate workflow registration.
- **Errors** for *operational failures*: the store is unavailable, the context is cancelled, a step returns an error.

## Repository layout

```
rerun/
├── store.go            Run, Log, Status, the Store/Writer/Reader/Guarder interfaces
├── codec.go            serialization seam, JSON by default
├── clock.go            time seam, wall clock by default
├── hooks.go            Observer seam, no-op by default
├── engine.go           the engine: registry, New, Handle, options
├── workflow.go         Do, replayStep, liveStep, Sleep
├── run.go              Start, exec, Recover
├── errors.go           StepError, so errors survive replay
├── internal/memstore.go   in-process store (the default and a test aid)
├── storetest/             importable Store contract suite for any backend
├── sqlite/sqlite.go       a real, persistent backend (pure Go, zero CGO)
├── tools/mutate/          dependency-free mutation tester
└── examples/              skeleton · recover · durablesleep · capstone
```

## Testing

```sh
go test -race ./...   # the whole suite, race-clean
go vet ./...
make mutate           # mutation testing: known faults are killed
```

## Roadmap

The core engine is single-process. Beyond it lie the hard problems of distributed durable execution:

| Problem | Mechanism |
|---|---|
| **Multi-process execution** | A try-lock lease on `Guarder`; Postgres advisory locks for exactly-once dispatch across machines. |
| **Durable timers** | Journal the absolute deadline; on recovery, wait only the remainder. |
| **Signals & external events** | `Wait[T]` — a `Do` whose value arrives from an external mailbox. |
| **Versioning across deploys** | Journal the version and branch on the journaled value, so old and new runs coexist on one binary. |

## License

[MIT](LICENSE) © 2026 Sylvester Francis
