# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html). While the
version is `0.x` the public API may change between minor releases.

## [Unreleased]

M1 — Correct under failure. `rerun` is now safe to run in production: runs are
never lost, duplicated, spuriously cancelled, or able to crash the process —
proven by a SIGKILL crash-injection harness and a v0.1 golden-journal
compatibility gate.

### Added

- `Engine.Shutdown(ctx)` — parks every in-flight run (they stay incomplete and
  resume on a later engine) and refuses new `Start`/`Recover` with
  `ErrEngineClosed`.
- `Stuck` status and `Engine.Redrive(ctx, runID)` — a run the engine refuses to
  execute (determinism panic, corrupt journal, a workflow that shrank) is marked
  `Stuck` and excluded from recovery; `Redrive` re-admits it after a fix.
- `Run.Input []byte` — the `Start` seed now rides inside the run record and is
  written atomically with the run's creation.
- `WithStoreTimeout(d)` — bounds every journal and status write (default 30s).
- Error sentinels `ErrEngineClosed`, `ErrRunExists`, `ErrSeqConflict`.
- Versioned schema migrations for the SQLite and Postgres backends; a v0.1
  database is adopted at version 1 and upgraded in place.
- A v0.1.1 golden-journal compatibility gate (`TestGolden_*`) and a SIGKILL
  crash-injection harness (`crashtest`) that asserts exactly-once effects at
  every step boundary.

### Changed

- Parent-context cancellation no longer cancels runs: a run detaches from the
  context of whoever started it and outlives it. `Shutdown` parks in-flight
  runs; only `Cancel` produces `Cancelled`.
- Panics and transient store errors no longer kill the process: a poisoned run
  becomes `Stuck`, and a transient store error or a lost lease parks the run,
  while its neighbours keep running. A journal/status write also survives a
  cancellation that lands mid-step, so a cancel can never lose the record of
  work that already happened.
- The SQLite backend now executes distinct runs concurrently; its lease was a
  single process-wide mutex that silently serialized all execution.
- `Start` returns a marshal error instead of panicking on unmarshalable input,
  and leaves no half-created run behind.
- `Cancel` records the request durably when the store supports it, and a pending
  cancel is honored at the next claim even with polling disabled.
- A workflow that returns with unconsumed journal entries is a determinism
  violation and marks the run `Stuck`.

## [0.1.1] - 2026-07-02

Documentation only; the API and behavior are identical to v0.1.0.

### Changed

- Rewrote the example command and package doc synopses so they read as
  self-contained descriptions on pkg.go.dev, dropping the internal "day-N" and
  "Part II hard problem N" references left over from the build tutorial.

## [0.1.0] - 2026-07-02

Initial release: a lightweight durable execution engine with journal-and-replay
recovery.

### Added

- Core engine: `New`, `Handle`, `Start`, `Recover`, the generic `Do[T]`, and
  durable `Sleep`, with the functional options `WithCodec`, `WithClock`,
  `WithObserver`.
- Journal-and-replay recovery: completed steps replay from the journal on
  restart; only unfinished steps run live. Divergent step tags panic (the
  determinism guard).
- Durable `Sleep` that journals an absolute deadline and waits only the
  remaining time, so a restart mid-sleep resumes without waiting again.
- Pluggable persistence behind the `Store` seam (`Writer` + `Reader` +
  `Guarder`), with a shared, importable contract suite
  `storetest.RunStoreContract`.
- Backends: in-memory (`internal.MemStore`), SQLite
  (`sqlite`, pure-Go via `modernc.org/sqlite`), and PostgreSQL
  (`postgres`, multi-process via `pg_try_advisory_lock` on a dedicated
  connection).
- Workflow input and result: an optional seed passed to `Start` and read with
  `Input[T]`; a value handed back with `Return[T]` and read with `Result[T]`;
  the terminal error is now persisted rather than dropped.
- Reliability helpers, all built on `Do`: `Retry` with `RetryPolicy` and
  `FixedBackoff`/`ExpBackoff` (backoff waits use the durable `Sleep`, so they
  survive a crash); `DoTimeout` for a per-step deadline whose outcome is
  journaled; `Cancel` with a new `Cancelled` status — in-process by default, or
  cross-process (eventual) via the optional `Canceller` store capability and
  `WithCancelPoll`.
- Part II hard problems: multi-process leasing (non-blocking try-lock
  `Guarder`), durable timers, external signals (`Signaler`, `Deliver`,
  `Wait[T]`) with a durable mailbox on all three backends, and safe versioning
  across deploys (`Version`).
- A dependency-free mutation tester (`tools/mutate`) that gates the suite: five
  real faults killed, one documented equivalent mutant surviving.
- Observability via the `Observer` seam; `StepError` so a step's failure
  survives being journaled and replayed.

### Known limitations (v0.2 fast-follow)

- No distributed scheduler: a sleeping run parks a goroutine rather than being
  polled from the store, so concurrent sleeps scale to thousands, not millions.
  (Cross-process cancellation, which needs similar store polling, is available
  opt-in via `WithCancelPoll`.)
- The `postgres` and `sqlite` backends share the module, so importing only the
  core still pulls their (pure-Go) drivers into the module graph. Splitting
  backends into submodules is under consideration.
