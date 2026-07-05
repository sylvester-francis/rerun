# ADR-0006: Store is split three ways

Status: accepted

## Context

Different consumers of persistence need different slices of it. The hot execution
path writes; a monitoring dashboard only reads; a lease manager only needs mutual
exclusion. A single fat interface forces every consumer to depend on methods it
never calls.

## Decision

`Store` composes three small interfaces:

- `Writer` — `Create`, `Append`, `Finish` (the hot path)
- `Reader` — `LoadLogs`, `Incomplete` (read-only consumers)
- `Guarder` — `Acquire` (mutual exclusion)

A consumer depends only on the face it uses, and any `Store` is a drop-in the
engine cannot tell apart. The behavior every backend must honor is an importable
contract suite, `storetest.RunStoreContract`.

## Consequences

- A read-only dashboard depends on `Reader` and cannot accidentally write; a test
  double makes `Guarder` a no-op.
- Anyone can write a backend and prove it correct against the same bar the in-tree
  SQLite, Postgres, and in-memory stores meet.
- Optional capabilities (`Signaler`, `Canceller`) are separate interfaces a backend
  opts into, not required methods.

## Alternatives considered

- **One fat `Store` interface.** Rejected: it forces every consumer to depend on
  the whole surface, so a read-only tool would import the write path it never uses,
  and a backend could not advertise "I only do the read side."
