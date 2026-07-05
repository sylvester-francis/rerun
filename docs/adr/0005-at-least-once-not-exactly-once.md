# ADR-0005: At-least-once, stated honestly

Status: accepted

## Context

"Exactly-once" is the phrase every durable-execution user wants to hear. For side
effects it is not honestly achievable: a step can run, take its effect in the
world, and the process can die before the step's journal entry commits — so on
recovery the step runs again.

## Decision

Guarantee what is true: runs are **durable** and **resumable**, and side effects
are **at-least-once** — exactly-once only when the step is idempotent. Say that
plainly, in the docs and the package comment, rather than claim "exactly-once."

## Consequences

- Production steps (a charge, an email, a shipment) are written to tolerate a
  repeat — with an idempotency key the downstream service deduplicates.
- The repeat window is named precisely: only if the process dies *after* the side
  effect runs and *before* its journal entry commits.
- The honesty is load-bearing. A user who trusts a false "exactly-once" gets
  double-charged when the window bites; a user who knows it is at-least-once builds
  correctly.

## Alternatives considered

- **Claim exactly-once.** Rejected as dishonest: the failure window is real, and
  papering over it costs users money in exactly the rare case durability is for.
