# Prompt: Build `rerun` — A Lightweight Durable Execution Library for Go

## Role

You are a senior Go engineer building an open-source library from scratch. You write tight, opinionated, hand-crafted code — not AI slop. Every line has a reason. You favor short variable names in tight scopes, zero comments that restate the code, error messages that help debug, and panics where recovery is impossible.

---

## Project Overview

Build `rerun` — a lightweight durable execution library for Go. Workflows survive crashes, restarts, and deploys by journaling every step to a pluggable store. On recovery, the engine replays the journal — skipping completed steps — and resumes exactly where it left off.

**Design north star:** The entire API fits on a napkin. 3 concepts: `Do`, `Sleep`, `Recover`. No servers, no queues, no YAML. Under 1000 lines of core code. Pure-Go SQLite default (zero CGO via `modernc.org/sqlite`).

---

## Architecture Constraints

### Dependency Rule

```
Consumer code
    ↓ depends on
  rerun (interfaces + engine)
    ↓ depends on
  nothing
```

Backends and extensions never import the engine. The engine imports no adapter.

### SOLID Enforcement

| Principle | Requirement |
|---|---|
| **S** | `Engine` orchestrates. `W` replays. `Store` persists. `Codec` serializes. `Clock` tells time. Each changes for exactly one reason. |
| **O** | New backends via `Store`. New serialization via `Codec`. New observability via `Observer`. Engine source never changes. |
| **L** | Any `Store` implementation is drop-in. SQLite, Postgres, in-memory — the engine can't tell. `Guarder` returning `io.NopCloser` is a legitimate substitute. |
| **I** | `Writer`, `Reader`, `Guarder` are independently implementable. A monitoring tool imports `Reader` only. A test mock implements `Guarder` as no-op. |
| **D** | Engine depends on abstractions. Concrete implementations depend on those same abstractions. Nothing points downward. |

---

## File Structure (EXACT)

```
rerun/
├── go.mod              # module github.com/you/rerun
├── store.go            # Writer, Reader, Guarder, Store, Log, Run, Status
├── engine.go           # Engine, New, Handle, Opt, WithCodec, WithClock, WithObserver
├── workflow.go         # W, Do[T], Sleep, replayStep, liveStep
├── run.go              # Start, exec, Recover
├── hooks.go            # Observer interface + noopObserver
├── codec.go            # Codec interface + jsonCodec default
├── clock.go            # Clock interface + wall default
├── errors.go           # StepError
├── sqlite/
│   └── sqlite.go       # ~100 lines, implements Store using modernc.org/sqlite
├── internal/
│   └── memstore.go     # in-memory Store for testing
├── store_test.go       # contract tests: RunStoreContract(t, makeStore)
├── engine_test.go      # Handle, Start, Recover, failure status
├── workflow_test.go    # Do, Sleep, replay, determinism, multi-step
├── hooks_test.go       # Observer receives correct events, not called on replay
├── clock_test.go       # fakeClock tests
├── errors_test.go      # StepError behavior
├── sqlite/
│   └── sqlite_test.go  # runs RunStoreContract against SQLite
└── internal/
    └── memstore_test.go # runs RunStoreContract against memstore
```

---

## Detailed File Specifications

### `store.go` — The Persistence Contract

```go
package rerun
```

Define these types and interfaces exactly:

- `Status` — int enum: `Pending`, `Running`, `Done`, `Failed`
- `Log` — immutable journal entry: `Seq int`, `Tag string`, `Payload []byte`, `Err string`, `At time.Time`
- `Run` — workflow instance metadata: `ID string`, `Workflow string`, `Status Status`, `Created time.Time`
- `Writer` interface — hot path, 3 methods:
  - `Create(ctx context.Context, r Run) error`
  - `Append(ctx context.Context, runID string, l Log) error`
  - `Finish(ctx context.Context, runID string, s Status) error`
- `Reader` interface — cold path, 2 methods:
  - `LoadLogs(ctx context.Context, runID string) ([]Log, error)`
  - `Incomplete(ctx context.Context) ([]Run, error)`
- `Guarder` interface — mutual exclusion, 1 method:
  - `Acquire(ctx context.Context, runID string) (io.Closer, error)`
- `Store` interface — composes `Writer + Reader + Guarder`

Why three sub-interfaces: A monitoring dashboard only needs `Reader`. A write-ahead-log shipper only needs `Writer`. A test double implements `Guarder` as no-op without faking six methods. `io.Closer` for locks uses stdlib — `io.NopCloser(nil)` for backends that don't need locking.

---

### `engine.go` — Orchestration

```go
package rerun
```

- `Func` type: `func(w *W) error`
- `Engine` struct: `store Store`, `codec Codec`, `clock Clock`, `obs Observer`, `reg map[string]Func`, `mu sync.RWMutex`
- `New(s Store, opts ...Opt) *Engine` — sets defaults: `jsonCodec{}`, `wall{}`, `noopObserver{}`, allocates `reg` map, applies opts
- `Opt` type: `func(*Engine)`
- `WithCodec(c Codec) Opt`
- `WithClock(c Clock) Opt`
- `WithObserver(o Observer) Opt`
- `Handle(name string, fn Func)` — panics on duplicate names with message `"rerun: duplicate workflow: " + name`
- `lookup(name string) Func` — panics on unknown workflow with message `"rerun: unknown workflow: " + name`

---

### `workflow.go` — Replay Logic

```go
package rerun
```

- `W` struct: `RunID string` (exported), `seq int`, `logs []Log`, `replay bool`, `eng *Engine`, `ctx context.Context` (all unexported except RunID)
- `Do[T any](w *W, tag string, fn func(context.Context) (T, error)) (T, error)` — the core generic function
  - If replaying AND `w.seq < len(w.logs)`: call `replayStep[T]`
  - Otherwise: call `liveStep[T]`
- `replayStep[T]` — checks tag matches journal entry (panics with `"rerun: determinism broken at seq %d in run %s: journal=%q code=%q"` on mismatch), unmarshals payload, increments seq, returns journaled result and error
- `liveStep[T]` — sets `w.replay = false`, executes fn, marshals result, appends to store, calls `w.eng.obs.OnStep`, increments seq
- `Sleep(w *W, d time.Duration) error` — implemented as `Do(w, fmt.Sprintf("sleep:%v", d), ...)` using `w.eng.clock.After(d)` with `ctx.Done()` select
- Panics on marshal failure, journal corruption, and journal write failure — these are not recoverable states

---

### `run.go` — Start and Recover

```go
package rerun
```

- `Start(ctx context.Context, workflow, runID string) error` — creates Run with Pending status, calls `obs.OnStart`, launches `go e.exec(ctx, r)`
- `exec(ctx context.Context, r Run)` — acquires lock (defers Close), sets Running status, loads journal, creates `W`, looks up workflow func, runs it, finishes with Done or Failed status, calls `obs.OnFinish`
- `Recover(ctx context.Context) error` — queries `Incomplete()`, launches `go e.exec` for each

---

### `hooks.go` — Observer

```go
package rerun
```

- `Observer` interface: `OnStart(Run)`, `OnStep(string, Log)`, `OnFinish(string, Status)` — value types, not pointers. Observers cannot mutate engine state.
- `noopObserver` struct — satisfies Observer with empty methods

---

### `codec.go` — Serialization

```go
package rerun
```

- `Codec` interface: `Marshal(v any) ([]byte, error)`, `Unmarshal(data []byte, v any) error`
- `jsonCodec` struct — delegates to `encoding/json`

---

### `clock.go` — Time Abstraction

```go
package rerun
```

- `Clock` interface: `Now() time.Time`, `After(d time.Duration) <-chan time.Time`
- `wall` struct — delegates to `time.Now()` and `time.After(d)`

---

### `errors.go` — Error Types

```go
package rerun
```

- `StepError` struct: `Tag string`, `Msg string`
- `Error() string` returns `fmt.Sprintf("rerun: step %q: %s", e.Tag, e.Msg)`

---

### `sqlite/sqlite.go` — Default Store (~100 lines)

```go
package sqlite
```

- Import `modernc.org/sqlite` (pure-Go, zero CGO)
- `Store` struct wrapping `*sql.DB` and `sync.Mutex` (for Guarder)
- `New(path string) *Store` — opens DB, creates tables:
  - `runs`: `id TEXT PRIMARY KEY, workflow TEXT, status INTEGER, created DATETIME`
  - `journal`: `run_id TEXT, seq INTEGER, tag TEXT, payload BLOB, err TEXT, at DATETIME, PRIMARY KEY (run_id, seq)`
- Implement all 6 methods of `rerun.Store`:
  - `Create` — INSERT, return error on duplicate
  - `Append` — INSERT into journal
  - `Finish` — UPDATE runs SET status
  - `LoadLogs` — SELECT FROM journal WHERE run_id ORDER BY seq
  - `Incomplete` — SELECT FROM runs WHERE status IN (Pending, Running)
  - `Acquire` — mutex lock, return a closer that unlocks
- Handle errors properly, wrap with context

---

### `internal/memstore.go` — Test Store

```go
package internal
```

- In-memory implementation using maps and slices, protected by `sync.Mutex`
- Implement all 6 methods of `rerun.Store`
- `Acquire` returns `io.NopCloser(nil)`
- `LoadLogs` returns a copy sorted by Seq
- `Create` returns error on duplicate ID

---

## Test Specifications

### `store_test.go` — Contract Tests

Export `RunStoreContract(t *testing.T, makeStore func() rerun.Store)` with these sub-tests:

1. **"create and load"** — create run, append 2 logs, load and verify count + order
2. **"logs return in seq order"** — insert out of order, verify LoadLogs returns sorted
3. **"incomplete filters correctly"** — create done + running + pending runs, verify Incomplete returns only non-done
4. **"finish transitions status"** — create run, finish as Failed, verify not in Incomplete
5. **"lock and release"** — acquire and close without error
6. **"duplicate create errors"** — create same ID twice, second must error

### `engine_test.go`

1. **TestHandle_DuplicatePanics** — register same name twice, assert panic with "duplicate"
2. **TestStart_UnknownWorkflowPanics** — start unregistered workflow, assert panic with "unknown"
3. **TestRecover_OnlyIncomplete** — create a Done run manually, recover, assert workflow func not called
4. **TestWorkflow_FailureSetsStatus** — workflow returns error, verify status is Failed

### `workflow_test.go`

1. **TestDo_LiveExecution** — step function called, returns correct value
2. **TestDo_ReplaySkipsExecution** — run, simulate crash (reset status to Running), recover, verify step not called again
3. **TestDo_ReplayReturnsJournaledValue** — verify replayed value matches original
4. **TestDo_ReplayPreservesError** — step that errors, replay returns same error
5. **TestDo_DeterminismPanic** — journal has tag "a", code has tag "b", assert panic with "determinism"
6. **TestSleep_SurvivesRestart** — sleep with fakeClock, advance, verify post-sleep step reached
7. **TestSleep_ReplaySkipsWait** — replay a run with 100h sleep, assert completes in <1s
8. **TestMultiStepReplay_AllStepsSkipped** — 3 steps, replay all, assert zero calls

### `hooks_test.go`

1. **TestObserver_ReceivesAllEvents** — spyObserver, verify OnStart(1), OnStep(2), OnFinish(Done)
2. **TestObserver_NotCalledDuringReplay** — verify OnStep not called during replay

### `clock_test.go`

1. **TestFakeClock_Advance** — create fakeClock, call After, advance, verify channel receives
2. **TestFakeClock_Now** — verify Now returns set time

### `errors_test.go`

1. **TestStepError_Format** — verify Error() output matches expected format
2. **TestStepError_Interface** — verify StepError satisfies error interface

### `sqlite/sqlite_test.go`

```go
func TestSQLiteStore(t *testing.T) {
    rerun_test.RunStoreContract(t, func() rerun.Store {
        return sqlite.New(t.TempDir() + "/test.db")
    })
}
```

### `internal/memstore_test.go`

```go
func TestMemStore(t *testing.T) {
    rerun_test.RunStoreContract(t, func() rerun.Store {
        return internal.NewMemStore()
    })
}
```

---

## Test Helpers Required

In test files, provide:

- `setup(t) (*Engine, *internal.MemStore, *fakeClock)` — creates memstore, fakeClock, engine
- `setupWith(t, ...Opt)` — same but with extra opts
- `waitDone(t, store, runID)` — poll store until status is Done or Failed, timeout after 2s
- `waitStatus(t, store, runID, status)` — poll until specific status
- `newFakeClock()` — returns `*fakeClock` with `Now()`, `After()`, `Advance(d)`
- `must(t, err)` — t.Helper + t.Fatal on error

---

## Mutation Testing Coverage

Every test exists to kill a specific mutant. Here is the mutation ↔ test mapping:

| Mutation | Killed By |
|---|---|
| Remove `w.seq++` in replay | TestMultiStepReplay_AllStepsSkipped |
| Flip `&&` to `\|\|` in replay guard | TestDo_LiveExecution |
| Remove `w.replay = false` in live path | TestSleep_SurvivesRestart |
| Swap `l.Tag != tag` to `==` | TestDo_DeterminismPanic |
| Remove panic on tag mismatch | TestDo_DeterminismPanic |
| Remove errStr assignment | TestDo_ReplayPreservesError |
| Change Finish(Done) to Finish(Failed) | TestWorkflow_FailureSetsStatus |
| Remove store.Append call | TestDo_ReplayReturnsJournaledValue |
| Remove obs.OnStep call | TestObserver_ReceivesAllEvents |
| Change Incomplete filter | TestRecover_OnlyIncomplete |

---

## Anti-Slop Rules

These are **hard requirements**. Violation of any means the output is rejected:

1. **No comments that restate the code.** Delete every `// Create a new engine`, `// This function does X`. Comments explain WHY, never WHAT.
2. **Short variable names in tight scopes.** `e` not `engine`, `l` not `log`, `s` not `store`, `r` not `run`, `w` not `workflow`. Only use descriptive names for exported types and package-level declarations.
3. **No empty interfaces unless unavoidable.** `Codec.Marshal(v any)` is acceptable because Go's JSON uses `any`. Don't spread it elsewhere.
4. **Error messages include context.** `"rerun: journal write failed at seq %d in run %s: %v"` not `"write failed"`.
5. **No `interface{}` — use `any`.** This is Go 1.21+.
6. **No unnecessary getters/setters.** Export the field or don't.
7. **No `utils` or `helpers` packages.** Everything has a home.
8. **No builder pattern, no method chaining.** Functional options only for `New()`.
9. **Panics for programmer errors** (non-determinism, corruption, duplicate registration). Errors for operational failures (store unavailable, context cancelled).
10. **`context.Context` is the first parameter** of every function that does I/O or could block.
11. **No `init()` functions.** Ever.
12. **Test names are `TestX_Behavior`** not `TestXShouldDoYWhenZ`. Keep them scannable.

---

## Output Format

Generate ALL files listed in the file structure above. Each file must be complete and compilable. The test suite must achieve:

- **100% line coverage** across all non-test files
- **100% mutation kill rate** for the mutations listed above
- All tests pass with `go test -race ./...`

Generate each file with a clear header comment showing the filename path, then the complete file contents. Do not truncate, abbreviate, or leave TODOs. Every file is production-ready.

Start with `go.mod`, then proceed through the file structure in order.
