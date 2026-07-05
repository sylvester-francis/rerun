# ADR-0004: A crash parks a run; it does not fail it

Status: accepted

## Context

When a process dies or shuts down mid-run, the run's status has to say
"resumable," not "over." Conflating an interruption with a terminal outcome either
loses work (if it is dropped) or lies about it (if it is marked failed or
cancelled).

## Decision

A crash or `Shutdown` **parks** a run: it stays `Pending`/`Running` (incomplete),
writes no terminal status, releases its lease, and resumes on the next `Recover`.
Only `Cancel` produces `Cancelled`. A run the engine *refuses* to execute — a
determinism panic, a corrupt journal, a workflow that shrank — becomes `Stuck`,
excluded from recovery until `Redrive` re-admits it after a fix.

## Consequences

- Graceful shutdown loses nothing: parked runs are indistinguishable from
  crash-interrupted ones and resume the same way.
- A poisoned run is contained and does not crash-loop recovery — `Stuck` takes it
  out of the automatic set.
- Each status means exactly one thing: `Pending`/`Running` (resumable), `Done`,
  `Failed` (business error), `Cancelled` (by request), `Stuck` (needs an operator).
- The panic recover journals the stuck reason so `Result` can surface why before a
  `Redrive`.

## Alternatives considered

- **Mark interrupted runs `Failed` and lean on retries.** Rejected: it conflates
  "the machine stopped" with "the work failed," and re-running the whole run
  repeats completed side effects.
