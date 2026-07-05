# ADR-0002: Recovery is one code path

Status: accepted

## Context

Many durable systems have a recovery routine that is separate from normal
execution — a second path that reconstructs state after a crash. Two paths drift:
the rarely-exercised recovery path is where the subtle bugs hide, because it does
not run on the happy path that every test covers.

## Decision

Recovery is just **running the workflow function again**. `exec` is the single
path for both a fresh run and a recovered one; replay skips completed steps by
returning their journaled results, and live execution begins at the first step the
journal does not have.

## Consequences

- There is no separate resume logic to keep in sync with execution.
- The same tests exercise first-run and recovery, because they are the same code.
- "A crash rewinds the tape; replay fast-forwards it" is literally true, not an
  analogy.

## Alternatives considered

- **A dedicated resume routine** that walks the journal and rebuilds state without
  re-entering the workflow. Rejected: it duplicates the execution logic, drifts
  from it over time, and is under-tested precisely because it runs only after a
  crash.
