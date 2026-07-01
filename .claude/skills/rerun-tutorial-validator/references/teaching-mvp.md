# Deep teaching — shipping it as a library (the MVP checklist)

The seven days build the mechanism; turning it into a library others depend on is a separate step,
and most of the work is not code. An MVP clears four bars: someone can install it, the README doesn't
overstate it, they can write one real workflow, and enough scaffolding exists to make adopting it
reasonable. Teach these as judgment, not chores.

---

## Make it honest (blocks publishing)

**Why it blocks.** A durable-execution library that overstates its guarantee costs an adopter real
money the first time a process dies in the wrong place. The mechanism is faithful, so the wording must
match it.

1. **Move off "exactly once."** State the guarantee as it is: durable and resumable, at-least-once for
   side effects, exactly-once *only* when steps are idempotent. A step repeats only if the process
   dies after its side effect runs and before its journal entry commits.
2. **Add a Non-goals & Limitations section.** Name the at-least-once window, the absence of a built-in
   retry policy / timeouts / cancellation, the lock-polling scaling ceiling, and that this is not a
   Temporal replacement. Adopters trust a library that states its limits over one that hides them.

*The gate warns if the README says "exactly once" without an idempotency caveat.*

---

## Make it usable for one real workflow

**The one feature that separates a demo from a library.** Today a workflow takes only `*W` and returns
`error`, and `Start` accepts no argument — so you can run only parameterless workflows and can't read
a result or even the failure reason back out.

- Thread an **input** into `Start` and journal it as the run's seed, so replay sees the same value.
- Let the workflow read it with `Input[T]`.
- Turn the workflow's return into a journaled **result** you fetch with `Result[T]`.
- Persist the terminal error instead of dropping it.

Real workflows take data in and hand a result back. Everything else is packaging; this is capability.

---

## Package it as a module

3. Module path is already `github.com/sylvester-francis/rerun` — real, not a placeholder. Keep imports
   consistent across examples and tests.
4. Put a real name and year in `LICENSE` (this repo: Apache-2.0 + NOTICE).
5. **Run SQLite (and Postgres, if built) against `storetest.Contract` on a real database.** Either they
   pass and you keep the persistence claim, or you label them experimental until they do. The in-memory
   store is verified by the suite; disk and network backends are only verified once you run them for
   real. This is the Day 4 honesty exception, closed.
6. Add a `doc.go` package comment so the pkg.go.dev page reads well; commit `go.mod` and `go.sum`.

---

## Make it adoptable

7. Finalize the README around a single honest sentence on what rerun is, then install, the smallest
   example, the determinism rule, persistence, the non-goals, and a Status line stating v0.x with an
   unstable API.
8. Add `CONTRIBUTING.md`, `SECURITY.md`, `CHANGELOG.md`.
9. Tag `v0.1.0` and push. Stay on v0 so you owe no stability promise while the surface is still moving.

*The gate warns on missing `CONTRIBUTING.md` / `SECURITY.md` / `CHANGELOG.md` / `doc.go`.*

---

## Past the cut line (deliberate v0.2 fast-follow, not launch blockers)

- A built-in `RetryPolicy` with backoff — *ergonomics, not a missing capability*: the retry pattern
  already works as a loop over `Do`, each attempt its own journaled step (the capstone proves it).
- Per-step timeouts.
- Cancellation of a running run.
- A public list/get surface over the store for an admin view.

Ship the honest minimum first; add these as real workflows ask for them. Teach the builder to
recognize the cut line — shipping the minimum honestly beats shipping more, later, dishonestly.
