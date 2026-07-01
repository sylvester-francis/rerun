# Deep teaching — Part II: the hard problems

Part II is beyond the 7-day core and is **optional**. The tutorial ships it as a separate
"hardened" tree. Validate a hard problem only if the builder has started it (its example dir
exists). The unifying idea is still Day 2: *anything nondeterministic becomes a journaled step.*

---

## Evolved interfaces (do not flag these against Day-1 signatures)

- **`Guarder.Acquire` gains a bool:** `Acquire(ctx, runID) (release io.Closer, acquired bool, err error)`
  — a non-blocking try-lock. The Day 1–7 tree uses the two-return form; the hardened tree uses
  three-return. They are different trees; don't "correct" one to the other.
- **`Sleep` is reworked** to journal an absolute deadline (`UnixNano`, an `int64`) as an instantaneous
  `Do`, then wait only the remaining time. It replaces the Day 3 `Sleep`. Same signature, so callers
  are untouched.

---

## HP1 — Multi-process execution

**Problem.** `Recover` walks incomplete runs and starts each. Run two copies of the service and both
recoverers replay the same run — every live step past the crash point runs twice (two charges, two
emails). The Day 1 engine is single-process by omission; `Guarder` was the seam left for this.

**Mechanism.** An exclusive lease per run. `exec` acquires before running and skips if it can't.
Crucially the lease is a **try-lock**, not a blocking lock: ten workers scanning the same backlog
must not all queue on the first run while the rest sit idle. Try-lock means "take it if free, else
skip to the next run," so work distributes itself.

**Why Postgres advisory locks.** `pg_try_advisory_lock` is non-blocking, session-scoped, and released
automatically when the session ends. That last property is the point: when a worker dies its
connection drops, Postgres frees the lock, and another worker acquires it — no lease expiry, no
heartbeat, no reaper. The catch is session scope: `database/sql` pools connections, so the backend
pins a dedicated `*sql.Conn` for the life of the lease, and must *not* cap the Postgres pool at one
connection (the exclusivity test needs a second connection to observe the lock as held).

**Why no status re-check is needed for correctness.** Replaying a fully journaled run executes no live
steps and produces no new side effects, so a redundant acquire is harmless. Real systems still add the
check as an optimization; rerun leans on replay being effect-free (the Day 2 property).

**Milestone.** `go run ./examples/workers` → `finalize executed live: 12` (12 runs, 4 concurrent
workers, race-clean). The lease, not luck, produced exactly-once dispatch.

---

## HP2 — Durable timers, done right

**Problem.** Day 3's `Sleep` blocks on the clock and survives a restart only in the lucky case. If the
crash lands *during* the sleep, nothing was journaled and recovery waits the full duration again — a
24-hour sleep that crashes at hour 23 sleeps another 24.

**Mechanism.** Record *when* the timer should fire, not *that* it fired, and record it **before**
waiting. The absolute deadline goes into the journal as the timer's first act (an instantaneous
`Do`); the wait is then always for `deadline − now`, recomputed on every run.

**Principle.** Separate the durable fact from the derived wait. The deadline is a fact to remember, so
it's journaled by an ordinary `Do` (inheriting everything Day 2 built). Waiting is *not* a fact — it's
a function of the deadline and the current time, so it's recomputed, never stored.

**Trap.** Order matters. Journal the deadline before the wait begins; journal after waiting and a
mid-sleep crash records nothing — back to Day 3.

**Scale note.** rerun's timer is still a parked goroutine (cheap, carries thousands). Past that,
production engines move deadlines into the store and poll for due ones so a sleeping workflow holds no
goroutine — `Incomplete` plus a `DueBefore` query is the seam where that attaches.

**Milestone.** `go run ./examples/durabletimer` → Case A waits only the 20-minute remainder of a
1-hour sleep after a mid-sleep crash (`fired=true`); Case B, deadline already elapsed, resumes
instantly. The deadline in the journal, not the duration in the code, decides both.

---

## HP3 — Signals & external events

**Problem.** Every step so far computes its own result. Real workflows wait on the world — an approval
click, a webhook, a "shipment scanned" event — arriving from outside the process, at an unknown time,
and it must survive a crash like any step.

**Mechanism.** `Signaler` is an **optional** `Store` capability (a per-run mailbox: `PushSignal` /
`PopSignal`). `Deliver` pushes an event from outside. `Wait[T]` is a `Do` whose function blocks on the
mailbox until a value arrives, then returns it — so the value is journaled on the way out, and replay
returns it from the journal without ever touching the mailbox again.

**Principle.** A signal is not a new kind of thing in the engine; it's a step whose value comes from an
external source. Because the received value is returned from a `Do`, durability is automatic. And
`Signaler` is a *separate* optional interface (type-assert, panic with a clear message if a store
can't support it) — the Day 4 interface-segregation rule applied again: capabilities are small
interfaces a backend opts into.

**Trap.** Delivery can land *before* the workflow reaches `Wait`. That's why `PushSignal` stores into a
queue rather than handing off to a waiting goroutine — the value sits in the mailbox until `Wait`
polls it. A signal delivered to a run that hasn't asked yet is not lost.

**Milestone.** `go run ./examples/signals` → Phase A (live delivery) and Phase B (journaled signal,
crash recovery) both `approved=true`. Phase B completed with no second delivery — the value came from
the journal, exactly what "waited three days, restarted twice" needs.

---

## HP4 — Versioning across deploys

**Problem.** Replay's contract is determinism. Ship a new version of a workflow while runs are in
flight — the new code inserts a step, an old run's journal doesn't have it, tags diverge, and a run
halfway to done can no longer finish. Changing a workflow's code is the most common way to break a
running workflow.

**Mechanism.** Make the version a journaled value. `Version(w, changeID, min, max)` journals `max` (the
newest version this build knows) on the first run and returns the journaled value on replay. New runs
record the new version and take the new branch; in-flight runs replay their old version and keep the
old branch. You gate the changed region on the returned int.

**Principle.** This is the change-marker pattern (Temporal's `GetVersion`, others' "patching"). Every
workflow change gets its own `changeID`; you never edit an old branch — you add a new one behind a
higher version and let old runs drain on the old path. Reusing a `changeID` and bumping `max` twice
breaks a run that recorded the first change but not the second.

**The `[min, max]` guard.** A rollback guard: if a run journaled version 2 and you deploy code that
only knows version 1, replay reads 2, finds it above `max`, and panics loudly rather than silently
taking the wrong branch. It turns an undetectable correctness bug into an obvious crash at the marker.

**Trap.** The marker only protects runs that *recorded* it. A run started before you introduced any
`Version` call has no marker, so the call's tag won't match the journal at that position. Introduce
the marker before you need it, or accept that pre-marker runs must finish on the old binary.

**Milestone.** `go run ./examples/versioning` → old run (pinned to v1) `fraud checks run: 0`, new run
(v2) `fraud checks run: 1`. Same binary, same function, two outcomes — the journal chose the branch.
