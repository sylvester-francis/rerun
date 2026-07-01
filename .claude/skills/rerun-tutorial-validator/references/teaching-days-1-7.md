# Deep teaching — Days 1–7

For each concept: the principle, the alternatives the tutorial weighed, why this choice won,
the trap, and where to reread. Teach at this level — not "add the line," but why the line exists.

---

## Day 1 — Journal, then replay

**Concept.** A workflow is an ordinary Go function. Each step is wrapped in `Do`, which the first
time executes the work and writes its *result* to a durable journal; a later re-run of the same
function sees the entry and returns the stored result instead of re-executing.

**Principle.** *Persist the result every completed step produced, not which step you reached.* With
results in hand, recovery is not guesswork — you replay the function and any step that already has a
recorded result is skipped by handing back that result.

**Alternatives, and why journaling won.**
- *Status column* (`charged`/`reserved`/`sent`): tells you a step finished, not *what it produced*,
  so on restart you can only guess where to continue. Loses the data flowing between steps.
- *Job queue*: a job that crashes mid-execution is lost or redelivered and re-run from the top —
  re-charging the card. No per-step memory.
- *State-machine library*: encodes transitions but still leaves you to persist and restore the data
  between states. Journaling captures exactly that data as a byproduct of running.

**Why the seams are interfaces from day one.** `Store`, `Codec`, `Clock`, `Observer` are interfaces
with trivial defaults (`sync.Mutex` map, JSON, wall clock, no-op). Adding a seam is cheapest before
code depends on the concrete type. The `Clock` seam looks like over-engineering until Day 3, when it
is the only thing that makes a one-hour sleep testable in a millisecond.

**Traps.** `Do` panicking on `Marshal` failure is *correct* — an unserializable result is a
programmer error, not an operational one. `Start` must **not** block: it launches `exec` in a
goroutine so one process can drive thousands of parked runs; a caller starting 1000 workflows cannot
afford 1000 blocked goroutines.

**Reread:** Day 1 "The core idea: journal, then replay"; the `store.go` Design callout.

---

## Day 2 — Determinism & crash recovery

**Concept.** Recovery replays the function from the top and matches each `Do` to a journal entry
**by position**. `Do` gains a second branch: `if w.replay && w.seq < len(w.logs)` return the stored
entry, else execute live. `Recover` finds incomplete runs and relaunches each through the same
`exec` path a fresh `Start` uses.

**Principle.** *A workflow body must be deterministic in its control flow and step tags.* Anything
nondeterministic — time, randomness, a read that steers a branch — must be captured *inside* a `Do`
so its value is journaled and replayed, never recomputed. The step is the boundary between the
deterministic skeleton and the nondeterministic world.

**Why a panic on tag mismatch, not a warning.** If the tag a `Do` presents differs from the journal
at that position, replay would hand the wrong result to the wrong call — silent corruption. The
engine panics naming the seq and both tags, so a determinism bug fails loud at the *first*
divergence. A logged warning would let every subsequent match proceed on meaningless data and finish
"successfully" while corrupt.

**Errors are results too.** A step that failed is a step that happened. The journal stores the error
string; replay reconstructs a `StepError`. You must not re-run a declined charge hoping for a
different answer — replay reproduces the same failure deterministically.

**Why `Recover` reuses `exec`.** Recovery is the normal path with a pre-populated journal, not a
second code path with its own bugs. Fewer paths, fewer ways to be wrong.

**Alternatives.** Re-running everything on restart (double charges) or storing only status (can't
resume) both fail. Positional journaling with a determinism contract is what makes "just run it
again" correct.

**Traps.** The classic corruption: `if time.Now().Hour() < 12 { Do("morning") } else {...}` — the
branch can differ between run and replay. Fix: journal the decision inside a `Do`. Also: changing a
step's return type between deploys makes the old payload fail to unmarshal into the new `T`
(foreshadows versioning). Note `w.replay = false` in `liveStep` is redundant — Day 6 proves it.

**Reread:** Day 2 "The contract that makes replay possible"; `replayStep`.

---

## Day 3 — Durable time & the panic/error line

**Concept.** A durable sleep is *not* `time.Sleep` (lost on restart, re-sleeps the full duration).
`Sleep` is a thin `Do` whose body waits on `clock.After(d)` or `ctx.Done()`. First run genuinely
waits; once journaled, replay returns the entry instantly. Sleeping is a step like any other.

**Why `Clock` was an interface from Day 1.** Production uses the wall clock; tests use a fake clock
that advances virtual time on command, so a week-long sleep runs in microseconds. The seam costs
nothing in production and makes the entire time dimension testable.

**Principle (panic vs error).** *Panic on programmer errors* — determinism violation, unknown or
duplicate workflow name, a journal that won't unmarshal into the declared type, an unserializable
result. These mean the program is built wrong. *Return errors for operational conditions* — a step's
business failure, a store write that fails, a cancelled context. Litmus: could this happen in a
correct program talking to a healthy world? Yes → error. Only if the code or stored data is wrong →
panic.

**Alternatives.** `time.Sleep` (not durable), an external scheduler (heavier, another moving part).
Journaling the sleep as a step wins for the single-process model. Part II Hard Problem 2 refines it
to journal the *deadline* so a mid-sleep crash doesn't re-wait the whole duration.

**Traps.** Durable ≠ "a timer fires in a dead process"; it means the time is *accounted for* — if the
process is down when the sleep expires, the sleep completes the moment recovery replays. Whether a
sleep is skipped depends only on whether its journal entry exists. And: converting the determinism
panic into a `Failed` status feels defensive but hides a corruption bug behind a status field.

**Reread:** Day 3 "Sleep is just a step"; the panic/error Principle.

---

## Day 4 — Pluggable storage (SOLID) & SQLite

**Concept.** Storage, serialization, time, and observation are all swappable without touching the
engine. A SQLite backend implements the same `Store` contract.

**Principle (dependency inversion).** Dependencies point inward. The engine depends only on
interfaces it defines; concrete backends import the engine to implement them; the engine never
imports a backend. A Postgres backend is a new package that changes zero lines of engine code.

**Interface segregation, justified.** `Store` splits into `Writer` + `Reader` + `Guarder` so a
consumer depends only on the slice it uses (a metrics reader takes `Reader` alone; a test double
implements only what it exercises). Small interfaces compose into the big one for free by embedding —
Go's structural typing makes the split weightless.

**What the schema encodes.** `runs.id` primary key enforces "no duplicate run" (the promise `Create`
makes) in the database. `journal(run_id, seq)` composite primary key means a run can never have two
entries at the same sequence number. Operational details that separate it from a toy: the
`modernc.org/sqlite` driver registers as `"sqlite"` (not `"sqlite3"`, the cgo `mattn` name); WAL +
`busy_timeout` let reads proceed beside a writer and turn a momentary lock into a short wait;
`SetMaxOpenConns(1)` serializes the single writer.

**Alternatives.** A `map` plus an append-only JSON-lines file is *also* durable — durability comes
from an append-only record, not a database. What SQLite buys over that is querying (find incomplete
runs without scanning), crash-atomic writes (WAL), and concurrency control — not durability per se.
`Acquire` is a mutex wrapped as an `io.Closer`; a distributed backend swaps that one method for
`pg_advisory_lock` and the engine is unchanged.

**Trap.** `LoadLogs` must end `ORDER BY seq`. Replay matches entries to `Do` calls by position, so
rows must return in sequence order. Omit it and the store passes casual testing but corrupts every
recovered run. Day 5's contract test inserts rows out of order specifically to catch this.

**Reread:** Day 4 "The dependency rule"; the `ORDER BY` Trap.

---

## Day 5 — A test harness you can trust

**Concept.** One contract suite, written once against the `Store` interface, run against every
backend. Plus a fake clock and the right way to test panics.

**Why the contract is an importable package (not `_test.go`).** Go forbids importing a test package
from another package, so a contract trapped in `rerun_test` could never be reused by the SQLite or
memstore tests. Placing `Contract(t, makeStore)` in an ordinary package lets every backend inherit it
with a three-line test.

**Fake time, and the piece everyone forgets.** The fake clock records each `After` as a waiter and
resolves them when the test calls `Advance`. `BlockUntil(n)` is essential: because `Start` runs the
workflow in a goroutine, the test must wait until that goroutine has actually registered its waiter
before advancing — advance too early and the wakeup is delivered to no one, the workflow hangs, and
the test times out.

**Testing code that must panic.** The determinism and unknown-workflow panics fire inside the
goroutine `exec` spawns, where a panic kills the whole test process. The fix is white-box tests in
`package rerun` that construct a `W` directly and call the unexported `lookup` synchronously, so the
panic is recovered in the same goroutine. Use black-box (`package rerun_test`) by default; reach for
white-box only to touch unexported behavior or avoid a crashing goroutine.

**Alternatives.** Bespoke per-backend tests duplicate effort and drift; an in-package contract can't
be shared. The importable contract is the highest-leverage test a pluggable library has.

**Trap.** The "logs return in seq order" sub-test inserts `2, 0, 1` and asserts `0, 1, 2`. The
out-of-order insert *is* the point — it catches a backend that forgot `ORDER BY`. An in-order insert
would pass a broken store.

**Reread:** Day 5 "One contract suite, every backend"; the `BlockUntil` Trap.

---

## Day 6 — Mutation testing

**Concept.** Coverage says a line *ran*; mutation testing asks whether an assertion would *notice it
being wrong*. Introduce a small deliberate fault (a mutant), run the suite: killed = a test failed;
survived = a real bug of that shape would slip through. The fraction killed measures detection, not
execution.

**The lesson mutants teach.** The `&&`→`||` and `<`→`<=` flips in the `Do` guard are killed **only**
by the partial-replay test. On a fresh run both sides of the guard are false, so `&&` and `||` agree
and a naive "does a fresh run execute its step" test can't tell them apart. Only after replaying a
*partial* journal does the workflow continue to a step past the journal's end, where the correct
`&&` runs it live but the mutant tries to replay an entry that doesn't exist. That is why the
mid-crash recovery test is the single most important test in the suite.

**Principle (equivalent mutants).** Removing `w.replay = false` leaves the suite green — and that is
*correct*. The guard's `w.seq < len(w.logs)` already governs replay-versus-live, the cursor only
advances, and the journal doesn't grow during a run, so the flag is redundant. The tester *asserts
this mutant survives*, recording the equivalence once so a future reader never re-investigates it. A
perfect kill rate is a number to interrogate, not chase.

**Alternatives.** Chasing 100% line coverage measures the wrong thing. A general tool (`gremlins`,
`go-mutesting`) run periodically gives exhaustive operator coverage; triage its survivors into "real
gap, write a test" or "equivalent, document it." The shipped `tools/mutate` is the fast,
dependency-free, CI-gating core: pinned faults, expected outcomes, non-zero exit on any mismatch.

**Reread:** Day 6 "The idea"; the equivalent-mutant Principle.

---

## Day 7 — Shipping rerun

**Concept.** Turn the engine into a `go get`-able library and prove the guarantee end to end.

**Principle (tiny surface).** The whole public API is `New`, `WithCodec`/`WithClock`/`WithObserver`,
`Handle`, `Start`, `Recover`, `Do`, `Sleep`. Small because the idea is small; easy to learn, hard to
misuse. Everything else is an interface a backend implements or an internal detail.

**Placement encodes a support promise.** The in-memory store lives under `internal/` — it's the
engine's own default and a test aid, not a backend you promise to support, and `internal/` makes that
a compiler rule rather than a comment. The SQLite backend lives in exported `sqlite/` because it *is*
supported. Ship the contract suite as a **public** package — the single highest-value adoption
artifact, because it lets third parties prove a new backend correct against your bar.

**The capstone's real lesson.** The retry loop is *not* a new engine feature — it's a loop over `Do`,
each attempt its own journaled step. Useful patterns fall out of the primitive. On recovery from a
journal seeded just after a successful charge, the card charges **zero** times live and the run still
finishes, sending the welcome email once. That is the durable-resume property, demonstrated.

**The honest guarantee.** Durable and resumable; at-least-once for side effects; exactly-once only
when steps are idempotent. A step repeats only if the process dies after its side effect runs and
before its journal entry commits — which is why production steps are written idempotent.

**Reread:** Day 7 "The public API surface"; the ship checklist (also `rerun-prompt.md` §11).
