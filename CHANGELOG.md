# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html). While the
version is `0.x` the public API may change between minor releases.

## [0.1.0] - 2026-07-01

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
- Workflow input: an optional seed passed to `Start`, journaled and read back
  with `Input[T]`.
- Part II hard problems: multi-process leasing (non-blocking try-lock
  `Guarder`), durable timers, external signals (`Signaler`, `Deliver`,
  `Wait[T]`), and safe versioning across deploys (`Version`).
- A dependency-free mutation tester (`tools/mutate`) that gates the suite: five
  real faults killed, one documented equivalent mutant surviving.
- Observability via the `Observer` seam; `StepError` so a step's failure
  survives being journaled and replayed.

### Known limitations (v0.2 fast-follow)

- Typed workflow results (`Result[T]`) and persisted terminal errors are not yet
  provided; return a result today by journaling it as a final `Do` step and
  reading it through the store.
- No built-in retry policy, per-step timeouts, or run cancellation. The retry
  pattern already works as a loop over `Do`.
- The `postgres` and `sqlite` backends share the module, so importing only the
  core still pulls their (pure-Go) drivers into the module graph. Splitting
  backends into submodules is under consideration.
