# Known issues and limitations

What `rerun` deliberately does not do (yet), and the one guarantee that is
fundamentally at-least-once. Current as of **v0.2.0**. For what shipped, see
[`CHANGELOG.md`](CHANGELOG.md); for the roadmap, see the
[open milestones](https://github.com/sylvester-francis/rerun/issues).

---

## Fundamental (by design)

### Side effects are at-least-once, not exactly-once

`rerun` replays a completed step's **result** exactly once — that is the whole
point of the journal. But the **side effect inside a step** (charging a card,
sending an email) can run more than once: if the process dies after the effect
happens but before its journal entry commits, recovery re-runs that step.

This window is irreducible without a distributed transaction spanning your side
effect and the journal, which `rerun` does not attempt. **Make your steps
idempotent** (use an idempotency key derived from the run ID + tag) and replay
makes them *effectively* exactly-once. Never claim unqualified exactly-once. The
crash-injection harness proves exactly-once at every step *boundary*; the
effect-then-journal window is documented, not asserted away.

### The SQLite backend is single-process for execution

The SQLite lease is an in-process, per-run lock. Two processes sharing one SQLite
file to **execute** runs is not supported (the `journal(run_id, seq)` primary key
fences the unsupported case rather than corrupting data). SQLite is the right
choice for durable single-process work; use the **Postgres** backend for
multi-process or multi-machine execution.

### Workflow bodies must be deterministic

Replay matches journaled steps to your code by tag and order. Anything
non-deterministic — the clock, randomness, a branch-steering read — must be
captured inside a `Do`. A workflow whose tag sequence diverges on replay is a
determinism violation: the run is marked `Stuck` (not silently mis-executed), and
`Redrive` re-admits it after you ship a fix. This is a guardrail, not a bug.

---

## Current limitations (roadmapped)

### No dispatcher yet — a sleeping run parks a goroutine &nbsp;(&rarr; v0.3.0)

Each in-flight run, including one blocked in `Sleep` or `Wait`, holds its own
goroutine and its lease. This scales comfortably to **thousands** of concurrent
long-lived runs, not millions. A store-polling dispatcher (`Engine.Run`,
`WithMaxConcurrent`) lands in **v0.3.0** (M2). The goroutine-free "Parker" model
for millions of timers is a recorded post-1.0 design
([#5](https://github.com/sylvester-francis/rerun/issues/5)); nothing forecloses
it.

### `Redrive` does not clear a durable cancel &nbsp;(&rarr; v0.3.0)

The claim-time cancel check honors a durable cancel request that is never
cleared, so `Redrive`-ing a run that was ever cancel-requested re-finishes it
`Cancelled` rather than re-running it. **Workaround:** start a fresh run ID.
Cancel-aware `Redrive` (verifying the run is actually `Stuck` and clearing the
request) arrives with M2's Inspector
([#2](https://github.com/sylvester-francis/rerun/issues/2)).

### Cross-process cancellation and signals are poll-based

In-process `Cancel` is immediate. Cross-process `Cancel` (via `WithCancelPoll` +
a `Canceller` store) is **eventual** — honored within one poll interval — and is
off by default. `Wait` for a signal also polls the store. Lower-latency
`LISTEN/NOTIFY` wake-ups on Postgres are planned for
[M3 (v0.4.0)](https://github.com/sylvester-francis/rerun/issues/3); polling
remains the floor.

### No retention or purge yet &nbsp;(&rarr; v0.3.0)

Journals grow unbounded; there is no built-in purge of terminal runs' rows until
M2's `Purger` capability
([#2](https://github.com/sylvester-francis/rerun/issues/2)). For now, prune with
your own SQL if journal size becomes a concern.

### Observability is minimal &nbsp;(&rarr; v0.3.0)

The only hook today is the `Observer` seam. Structured logging (`slog`),
OpenTelemetry, and richer lifecycle/telemetry observers land in M2/M3. A run that
sticks is reported through `Observer.OnFinish(id, Stuck)`, but the engine does
not yet emit a structured log line for it — that placeholder is filled in M2.

### Backends pull their drivers into the module graph &nbsp;(&rarr; v0.4.0)

Importing only the core still pulls the (pure-Go) SQLite and Postgres drivers into
the **module graph**, because they share one module. They stay out of your
**binary** unless you import them. The
[M3 module split (v0.4.0)](https://github.com/sylvester-francis/rerun/issues/3)
moves each backend to its own module so the core's dependency set is truly empty.

---

## Pre-1.0 API stability

While the version is `0.x`, minor releases may include **breaking API and
behavior changes** — v0.2.0 reversed several v0.1.x behaviors (parent-context
cancellation, panic containment, the SQLite lease; see the CHANGELOG "Changed"
list). What does **not** break: a journal or database written by any released
version replays correctly under every later version within a major, enforced by
the golden-journal fixtures. Your data is safe across upgrades; the API is not
frozen until v1.0.0.

---

## Explicitly out of scope

Admin UI or HTTP control plane; cron/scheduling DSLs; queues; multi-language SDKs;
cross-run orchestration (child workflows); automatic PII redaction; exactly-once
for non-idempotent side effects. `rerun` is the durable-execution *core idea*, not
the platform around it — the `Store` contract is the extension point.

---

Found a limitation not listed here, or hit one of these harder than expected?
[Open an issue](https://github.com/sylvester-francis/rerun/issues/new).
