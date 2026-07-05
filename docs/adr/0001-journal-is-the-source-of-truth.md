# ADR-0001: The journal is the source of truth

Status: accepted

## Context

A durable engine has to know where a crashed run was so it can continue. There
are two things it could persist: *which step the run reached* (a position or
checkpoint), or *the result each completed step produced*.

## Decision

Persist the **result** every completed step produced, in an append-only journal.
The workflow function is a cursor over that journal: on recovery the engine runs
the function again, and each `Do` returns its journaled result for a completed
step, executing live only past the end of the journal.

## Consequences

- Recovery is not guesswork. With the results in hand, a completed `charge`
  returns its transaction id from the journal — the card is never charged twice.
- The workflow is written once, as if crashes did not exist; the engine makes it
  crash-proof by recording and replaying.
- It makes determinism *the* rule (see
  [ADR-0002](0002-recovery-is-one-code-path.md)): the cursor must line up with the
  journal, so the body must issue the same steps in the same order every run.

## Alternatives considered

- **Checkpoint the position and re-run from there.** Rejected: re-running a step
  you only know a run "reached" repeats its side effect, and to avoid that you need
  its result anyway — so persist the result and skip the checkpoint.
