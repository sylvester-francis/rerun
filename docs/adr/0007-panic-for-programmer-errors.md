# ADR-0007: Panic on programmer errors, contained to the run

Status: accepted

## Context

Failures fall into two kinds. Operational conditions are the world misbehaving: a
step's business error, a cancelled context, a store outage. Programmer errors are
bugs: a non-deterministic workflow, a corrupt journal, an un-marshalable result.
Treating them the same either swallows bugs (return everything) or takes the
process down on a hiccup (panic on everything).

## Decision

**Panic on programmer errors; return errors for operational conditions.** A
determinism violation, a duplicate workflow registration, or a marshal failure
panics. A step's failure, a cancelled context, or a store error is returned. A
panic inside a run is **contained**: the engine's `recover` marks that one run
`Stuck` and the process keeps serving every other run — a panic never escapes a
run's goroutine.

## Consequences

- A determinism bug fails loud at the exact seq and tag, instead of silently
  handing one step's result to another and corrupting the run.
- One poisoned run cannot take down the thousands of healthy runs sharing the
  process.
- Operational failures park or return without drama and resume on a later claim.

## Alternatives considered

- **Return errors for everything, including determinism.** Rejected: a determinism
  violation makes every later positional match meaningless, so continuing is worse
  than stopping that run.
- **Let panics crash the process.** Rejected: one bad workflow would kill every
  healthy run in the process; containment is the whole point.
