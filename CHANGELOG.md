# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html). While the
version is `0.x` the public API may change between minor releases.

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
