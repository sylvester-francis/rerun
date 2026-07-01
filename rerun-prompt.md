# CLAUDE.md — `rerun` Build Validation Guardrail

> **What this file is.** A validation guardrail for hand-building the `rerun` durable-execution
> library by following `Building-rerun-in-7-Days.md`. It is a rubric, not a spec to generate from.
> Its job is to let you (and Claude, acting as reviewer) confirm that each day's step was
> implemented *correctly* — right contract, right invariants, right milestone output — without
> Claude writing the code.

> **⛔ PRIME DIRECTIVE — DO NOT IMPLEMENT.**
> The human writes every line of Go. Claude does **not** author, scaffold, "fix up," complete, or
> paste implementation code for `rerun` — not `store.go`, not a test, not a one-line patch, not a
> `go.mod`. Claude's only role here is to **validate, review, and explain**. If asked to write
> implementation code, decline and offer a review of the human's own code instead. The single
> allowed exception is quoting a *tiny* snippet already present in the tutorial to point at a
> discrepancy (e.g. "the tutorial's `LoadLogs` has `ORDER BY seq`; yours doesn't").

---

## How to use this file (assistant instructions)

**Your role:** senior Go reviewer for a correctness-critical library. You audit the human's
implementation against the tutorial and this guardrail. You do not build it for them.

**When the human says "check Day N" / "review my Store" / "did I do this right":**
1. Read their actual files (the real Go, not this doc).
2. Map each file to its **contract** in §7 (Days) or §8 (Part II).
3. Run the day's **milestone command** and compare stdout to the **expected output** verbatim.
4. Report **pass/fail per gate**, each finding anchored to a tutorial section and a file:line.
5. Flag every **Trap** in the relevant gate that the code has fallen into.
6. If something is ambiguous, ask — do not assume the human meant the tutorial's version.

**Hard rules for the reviewer:**
- **Evidence before assertions.** Never say a gate passes until you have run the command and seen
  the expected output. "It looks right" is not a pass. Paste the output you observed.
- **A day is not done** until its milestone program runs *and* every prior day still passes
  (`go test -race ./...` stays green). Later days must not regress earlier ones.
- **Determinism first.** Before anything else, check §2. Most real bugs in this codebase are
  determinism/replay violations, and they fail silently unless the tag check catches them.
- **Prefer the tutorial's names and shapes.** Test names, tags, panic strings, and interface
  signatures should match the tutorial (§7–§9). Divergence is a finding to raise, not to accept
  silently — the mutation gate (§9) is keyed to specific test names.
- **Keep this file current.** When you catch the human (or yourself) repeating a mistake, propose
  a one-line addition to §10 so it isn't repeated.

---

## Table of contents

1. Project Overview — the *why*
2. The One Rule: Determinism — **read before reviewing anything**
3. Architecture & Public API — the *map*
4. Repository Layout — the *map* (files)
5. Tech Stack & Validation Commands
6. Panic vs. Error — the classification guardrail
7. Day-by-Day Validation Gates (Days 1–7) — **the core**
8. Extended Gates: Part II, the Hard Problems (beyond 7 days, optional)
9. Mutation Testing Gate
10. Do-Not / Anti-Patterns — guardrails
11. Ship & MVP Acceptance Checklist
12. Validation Review Workflow — how a review pass runs
13. Validation Status Tracker

---

## 1. Project Overview — the *why*

`rerun` is a small Go library that runs a multi-step process to completion and, when the machine
crashes partway through and restarts later, **resumes from where it left off** instead of starting
over. The mechanism is **journal-and-replay**: a workflow is an ordinary Go function whose steps
record their results to a durable log; re-running the function after a crash replays completed
steps from the log instead of executing them again.

- **The whole idea, in one line:** persist *the result every completed step produced*, not *which
  step you reached*. Recovery is just running the function again.
- **Module path:** `github.com/sylvester-francis/rerun` (already correct for this repo — do not
  swap it for a placeholder).
- **Scale of the thing:** a few hundred lines, standard-library-only core, no third-party deps in
  the engine. One optional backend (`modernc.org/sqlite`, pure-Go, cgo-free).

**The honest guarantee (do not overstate it).** `rerun` is **durable and resumable**;
**at-least-once** for side effects; **exactly-once only when steps are idempotent**. A step repeats
only if the process dies in the narrow window *after its side effect runs and before its journal
entry commits* — which is why production steps are written to be idempotent. Any README or comment
that claims plain "exactly once" is a **finding** (see §10, §11).

---

## 2. The One Rule: Determinism — read before reviewing anything

> A workflow body **must be deterministic** in its control flow and its step tags. Anything
> non-deterministic — the current time, a random number, a read whose result steers a branch — must
> be captured **inside a `Do` step** so its value is journaled and replayed, never recomputed. The
> step is the boundary between the deterministic skeleton and the non-deterministic world.

**Wrong (corrupts on replay):**
```go
if time.Now().Hour() < 12 { rerun.Do(w, "morning", ...) } else { rerun.Do(w, "evening", ...) }
```

**Right (journal the decision):**
```go
morning, _ := rerun.Do(w, "is-morning", func(ctx context.Context) (bool, error) {
    return time.Now().Hour() < 12, nil
})
if morning { ... }
```

**Enforcement to verify exists:** during replay, if the tag a `Do` presents ≠ the tag stored at
that position, the engine **panics** naming the seq and both tags. A determinism bug must fail
**loud and early**, never degrade to a `Failed` status (that hides corruption — see §6, §10).

When reviewing *any* workflow the human writes, scan for uncaptured `time.Now()`, `rand`, map-range
order, goroutine races, or reads-that-branch outside a `Do`. These are the top source of bugs.

---

## 3. Architecture & Public API — the *map*

**The seams (interfaces the engine defines; backends satisfy them structurally):**

| Seam | Default | Purpose |
|---|---|---|
| `Store` = `Writer`+`Reader`+`Guarder` | `internal.MemStore` | persistence |
| `Codec` | `jsonCodec` (`encoding/json`) | serialization of step results |
| `Clock` | `wall` (`time`) | time; the seam that makes sleeps testable |
| `Observer` | `noopObserver` | lifecycle events (start/step/finish) |

**The dependency rule (verify it holds):** *dependencies point inward.* The engine depends only on
interfaces it defines. Concrete backends import `rerun`; **the engine never imports a backend**.
Adding a Postgres backend must change **zero** lines of engine code. If you find the engine
importing `sqlite/` or `internal/`, that's a structural finding.

**Interface segregation (why `Store` is split three ways):** a read-only consumer depends on
`Reader` alone; a locker on `Guarder` alone; a test double implements only what it exercises. The
split costs nothing (interfaces compose by embedding) and is the seam Day 4 and Part II cash in.

**Public API surface — keep it tiny.** The entire surface a user touches:
`New`, `WithCodec`, `WithClock`, `WithObserver`, `Handle`, `Start`, `Recover`, the generic
`Do[T]`, and `Sleep`. Everything else is an interface a backend implements or an internal detail.
Anything else exported is a finding unless justified (the `Status` constants are legitimately part
of the surface; `internal.MemStore` is deliberately *not* public API).

---

## 4. Repository Layout — the *map* (files)

Files appear on the day noted. Nothing should exist before its day.

```
rerun/                       module github.com/sylvester-francis/rerun
  store.go                   Run, Log, Status; Store/Writer/Reader/Guarder            (day 1)
  codec.go                   Codec seam, jsonCodec default                            (day 1)
  clock.go                   Clock seam, wall default                                 (day 1)
  hooks.go                   Observer seam, noopObserver default                      (day 1)
  engine.go                  Engine; New, Opt, With*, Handle, lookup                  (day 1)
  workflow.go                W, Do[T], replayStep, liveStep, Sleep                    (days 1-3)
  run.go                     Start, exec, Recover                                     (days 1-2)
  errors.go                  StepError (errors survive replay)                        (day 2)
  internal/
    memstore.go              in-memory Store; the default and a test aid              (day 1)
    memstore_test.go         runs the contract suite against memstore                 (day 5)
  storetest/
    storetest.go             importable Store contract suite (NOT a _test.go file)    (day 5)
  sqlite/
    sqlite.go                real, persistent Store backend                           (day 4)
  tools/
    mutate/main.go           dependency-free mutation tester                          (day 6)
  examples/
    skeleton/main.go         day 1 milestone
    recover/main.go          day 2 milestone
    durablesleep/main.go     day 3 milestone
    capstone/main.go         day 7 acceptance
  engine_test.go  workflow_test.go  clock_test.go  errors_test.go  hooks_test.go
  helpers_test.go            fakeClock, spyObserver, setup, waitDone, must            (day 5)
  panics_internal_test.go    white-box (package rerun) panic tests                    (day 5)
  README.md  LICENSE  Makefile  .github/workflows/ci.yml                              (day 7)
```

**Placement encodes a support promise:** `internal/memstore.go` is under `internal/` on purpose —
it's the engine's own default and a test aid, *not* a backend you promise to support. `sqlite/` is
exported because it *is* supported. `storetest/` must be an importable package (not `_test.go`) so
`sqlite` and `internal` tests can both import the contract.

---

## 5. Tech Stack & Validation Commands

- **Go 1.22** is the verified toolchain (Appendix A). Minimum **1.18** for generics; use `any`, not
  `interface{}`.
- **Core:** standard library only — `encoding/json`, `sync`, `time`, `context`, `database/sql`.
- **Optional backend:** `modernc.org/sqlite` (pure-Go, registers as driver `"sqlite"`, **not**
  `"sqlite3"`). Resolve with `go mod tidy`.

**Validation commands (this is how gates are checked):**
```bash
go build ./...                 # every file compiles
go vet ./...                   # must be clean
go test -race ./...            # full suite, race detector on  (the bar)
go test -cover ./...           # coverage number (necessary, not sufficient — see §9)
go run ./tools/mutate          # mutation gate  (or: make mutate)
go run ./examples/skeleton     # day 1 milestone   (recover / durablesleep / capstone for 2/3/7)
```

> **Day 4 verification exception (carry it forward):** the build environment in the tutorial could
> not fetch `modernc.org/sqlite`, so the SQLite backend's *runtime* was not exercised there — it is
> code-shown and contract-pinned only. In **your** environment you must actually run
> `go mod tidy && go test -race ./sqlite/`. Until you do, treat the persistence claim as unverified.

---

## 6. Panic vs. Error — the classification guardrail

> **Panic on programmer errors.** A determinism violation, an unknown/duplicate workflow name, a
> journal that won't unmarshal into the declared type, a result that won't serialize. The program is
> built wrong; failing loud and early is the service.
>
> **Return errors for operational conditions.** A step's own business failure, a store write that
> fails, a cancelled context. Expected in a running system; the caller decides.

**Litmus:** could this happen in a correct program talking to a healthy world? Yes → error. Only if
the code or stored data is wrong → panic.

| Condition | Verdict |
|---|---|
| Card declined / typed business error | **error** |
| Store write / read-only DB file | **error** (operational) |
| `ctx` cancelled mid-step | **error** |
| Duplicate `Handle(name)` / unknown workflow | **panic** |
| Replay tag mismatch (determinism) | **panic** |
| `Marshal` fails / journal payload wrong shape | **panic** |
| Tag computed from `rand.Int()` → replay mismatch | **panic** (determinism bug by construction) |

**Finding:** any code that converts the determinism panic into a `Failed` status. That trades a
screaming failure for a silent one.

---

## 7. Day-by-Day Validation Gates (Days 1–7) — the core

Each gate: **Goal → Contract to verify → Milestone (command + expected output) → Traps → Done-when.**
Compare milestone stdout **verbatim**. `go test -race ./...` must stay green across all completed days.

### Day 1 — Walking skeleton (journal forward, no replay yet)

- **Goal:** types, in-memory store, and enough engine to run a workflow forward and journal each step.
- **Contract:** `store.go` defines `Status{Pending,Running,Done,Failed}`, `Log{Seq,Tag,Payload,Err,At}`,
  `Run{ID,Workflow,Status,Created}`, and `Writer`/`Reader`/`Guarder`/`Store`. `New` wires defaults
  (`jsonCodec`, `wall`, `noopObserver`, `reg` map). `Handle` **panics** on duplicate; `lookup`
  **panics** on unknown. `Do[T]` today *always* executes live: run fn → marshal → `Append` →
  `OnStep` → `seq++`. `Start` creates the run and launches `go e.exec`; `exec` marks Running, builds
  `W`, runs the func, records terminal status. `MemStore.LoadLogs` returns a **sorted copy**;
  `MemStore.Acquire` is a no-op `io.NopCloser(nil)`; `MemStore.Get` exists for examples/tests only.
- **Milestone:** `go run ./examples/skeleton`
  ```
  running workflow r1:
    [live] make-name
    [live] greeting: hello Ada
  status: Done
  journal:
    seq=0 tag="make-name" payload="Ada"
    seq=1 tag="greeting" payload="hello Ada"
  ```
- **Traps:** `Do` panicking on `Marshal` failure is *correct* (unserializable result = programmer
  error). `Start` must **not** block — it returns immediately and the example polls (a caller with
  1000 workflows can't afford 1000 blocked goroutines).
- **Done when:** two steps execute, two ordered journal entries, run `Done`, and the output matches.

### Day 2 — Determinism and crash recovery

- **Goal:** replay from a partial journal; only the unfinished step runs live.
- **Contract:** `W` gains `logs []Log` and `replay bool`. `Do` branches:
  `if w.replay && w.seq < len(w.logs) { replayStep } else { liveStep }`. `replayStep` **checks the
  tag first** and panics on mismatch (`"rerun: determinism broken at seq %d in run %s: journal=%q
  code=%q"`), then unmarshals, then reconstructs a `*StepError` if `l.Err != ""`. `liveStep` is
  Day 1's body plus `w.replay = false`. `errors.go` defines `StepError{Tag,Msg}`. `exec` now
  `LoadLogs` before running and sets `replay: len(logs) > 0`. `Recover` finds `Incomplete()` runs
  and relaunches each through the *same* `exec` path (recovery is the normal path, pre-populated).
- **Milestone:** `go run ./examples/recover`
  ```
  seeding a crashed run with a partial journal [extract, transform]:
  calling Recover:
    [live] load
  status: Done? true
  steps executed live during recovery: 1 (expected 1, only 'load')
  ```
- **Traps:** errors are results too — a step that failed must replay the *same* error, not re-run.
  The determinism check must **panic**, not warn/continue (a bad match makes every later match
  meaningless).
- **Done when:** `extract`+`transform` replay with zero live calls, only `load` runs, run `Done`.

### Day 3 — Durable time and the panic/error line

- **Goal:** a sleep that survives a restart; the panic-vs-error rule stated and honored.
- **Contract:** `Sleep(w, d)` is a thin `Do` wrapper, tag `fmt.Sprintf("sleep:%v", d)`, body selects
  on `w.eng.clock.After(d)` vs `ctx.Done()`, returns `struct{}{}`. First run genuinely waits; once
  journaled, replay returns the entry without waiting. `time` import joins `workflow.go`. §6 (panic
  vs error) is the day's second half — verify the split is applied, not just described.
- **Milestone:** `go run ./examples/durablesleep`
  ```
  first run (must actually wait out the 1s sleep):
    [live] reminder fired
  first run took 1s
  simulating crash, then recovering:
  recovery took 0s (sleep already satisfied, so it skips the wait)
  ```
- **Traps:** durable ≠ "a timer fires in a dead process." It means the time is *accounted for*: if
  the process is down when the sleep expires, the sleep completes the moment recovery replays.
  Whether a sleep is skipped depends **only** on whether its journal entry exists.
- **Done when:** first run ≈ the real duration; recovery ≈ 0s.

### Day 4 — Pluggable storage (SOLID) + SQLite backend

- **Goal:** a real `Store` behind the same contract; the seams justified and exercised.
- **Contract:** `sqlite/sqlite.go` (`package sqlite`) implements all of `Store`. `runs.id` is a
  PRIMARY KEY (enforces "no duplicate run"); `journal(run_id, seq)` is a composite PRIMARY KEY.
  DSN sets `journal_mode(WAL)` + `busy_timeout(5000)`; `SetMaxOpenConns(1)` (single writer). Driver
  name is `"sqlite"`. **`LoadLogs` MUST end `ORDER BY seq`.** `Acquire` wraps `mu.Unlock` in a
  `closerFunc` adapter (an `io.Closer`).
- **Milestone / verification:** no runtime output in the tutorial (the one honest exception). In
  *your* env: `go mod tidy && go test -race ./sqlite/` must be green.
- **Traps:** **`ORDER BY seq` is the single most important clause** — omit it and the store passes
  casual testing but corrupts every recovered run (replay matches by position). Day 5's contract
  suite catches exactly this by inserting rows out of order.
- **Done when:** SQLite passes the `storetest.Contract` (see Day 5) on a real DB file.

### Day 5 — A test harness you can trust

- **Goal:** one contract suite every backend inherits; fake time; correct panic tests.
- **Contract:** `storetest/storetest.go` (`package storetest`, **not** a `_test.go`) exports
  `Contract(t, makeStore func() rerun.Store)` with sub-tests: *create and load*, *logs return in
  seq order* (inserts `2,0,1`, asserts `0,1,2`), *incomplete filters correctly*, *finish transitions
  status*, *lock and release*, *duplicate create errors*. `internal/memstore_test.go` and
  `sqlite/sqlite_test.go` each hand `Contract` a factory (SQLite opens in `t.TempDir()`).
  `helpers_test.go` provides `fakeClock` (`Now`/`After`/`Advance`/**`BlockUntil`**), `spyObserver`,
  `setup`, `waitDone`, `must`. `panics_internal_test.go` is **white-box** (`package rerun`) so it can
  reach unexported `lookup` and construct a `W` — panics tested *synchronously*, never inside `exec`'s
  goroutine (which would kill the test process).
- **Milestone:** `go test -race ./...`
  ```
  ok    github.com/sylvester-francis/rerun          1.094s
  ?     github.com/sylvester-francis/rerun/examples/capstone      [no test files]
  ?     github.com/sylvester-francis/rerun/examples/durablesleep  [no test files]
  ?     github.com/sylvester-francis/rerun/examples/recover       [no test files]
  ?     github.com/sylvester-francis/rerun/examples/skeleton      [no test files]
  ok    github.com/sylvester-francis/rerun/internal 1.013s
  ?     github.com/sylvester-francis/rerun/storetest [no test files]
  ```
- **Traps:** **`BlockUntil` is the one everyone forgets** — a test must wait until the workflow
  goroutine has registered its waiter before `Advance`, or the wakeup is delivered to no one and the
  test hangs. The out-of-order insert in *logs return in seq order* is the whole point; an in-order
  insert would pass a broken store.
- **Done when:** whole suite green **with `-race`**, contract runs against memstore (and SQLite in
  your env).

### Day 6 — Mutation testing

- **Goal:** measure whether the suite would actually catch a bug. See the dedicated gate in **§9**.
- **Milestone:** `go run ./tools/mutate` (or `make mutate`) — see §9 for the exact expected table.
- **Done when:** 5 real faults **killed**, 1 documented equivalent **survives**, `PASS`.

### Day 7 — Shipping (API, repo, acceptance run)

- **Goal:** package `rerun` as a `go get`-able library and prove the guarantee end to end.
- **Contract:** public surface is exactly §3's list. `README` opens with the 30-second pitch, the
  smallest runnable example, the determinism rule (with the journal-the-decision fix), and the
  backend-swap line `rerun.New(sqlite.New("rerun.db"))`. `Makefile` wires `test`/`vet`/`cover`/
  `mutate`; CI runs them. `LICENSE` chosen deliberately (this repo: Apache-2.0 + NOTICE). Ship the
  `storetest` contract as a **public** package.
- **Milestone (acceptance):** `go run ./examples/capstone`
  ```
  === clean first run ===
      [live] create-account
      [live] charge attempt 0: processor timeout
      [live] charge attempt 1: ok (txn_2)
      [live] send-welcome-email
  after first run: charges(live)=2 emails(live)=1

  === crash recovery for u2 (crashed after charge, before email) ===
      [live] send-welcome-email
  u2 status Done? true
  during recovery: charges(live)=0 (card NOT re-charged), emails(live)=1 (welcome sent once)
  ```
- **Traps:** the retry loop is *not* a new engine feature — it's a loop over `Do`, each attempt its
  own journaled step. Useful patterns fall out of the primitive; if the human added engine-level
  retry machinery, question it.
- **Done when:** recovery charges the card **0** times live and still finishes, sending the email
  once — plus the full §11 ship checklist.

---

## 8. Extended Gates: Part II — the Hard Problems (beyond 7 days, optional)

Part II is separate from the 7-day library and **evolves two interfaces**. Only apply these gates if
the human is building the hardened tree. Keep the two trees mentally distinct (the tutorial ships
them as `rerun.tar.gz` vs `rerun-hardened.tar.gz`).

> **⚠ Interface-evolution warnings (do not flag as bugs if Part II is in scope):**
> - **`Guarder.Acquire` gains a bool:** `Acquire(ctx, runID) (release io.Closer, acquired bool, err
>   error)` — a **non-blocking try-lock**. This differs from the Day 1 two-return signature. The
>   Day 1–7 tree uses the two-return form; the hardened tree uses three-return. Do not "correct" one
>   to the other across trees.
> - **`Sleep` is reworked** to journal the **absolute deadline** (`UnixNano`, an `int64`) as an
>   instantaneous `Do`, then wait only the *remaining* time. The Day 3 `Sleep` (which journals the
>   fact of sleeping) is replaced. Same signature, so callers are untouched.

| # | Problem | Mechanism to verify | Milestone (`go run ./examples/...`) expected line |
|---|---|---|---|
| 1 | Multi-process | try-lock lease on `Guarder`; `exec` skips if `!acquired`; Postgres `pg_try_advisory_lock` on a **dedicated `*sql.Conn`** (do **not** cap Postgres pool at 1) | `workers`: `jobs completed: 12` / `finalize executed live: 12 (expected 12: exactly once per job)` |
| 2 | Durable timers | journal the deadline **before** waiting; wait = `deadline − now`, recomputed each run; `remaining <= 0` returns instantly | `durabletimer`: Case A waited the **20m** remainder of a 1h sleep, `fired=true`; Case B resumed instantly, `fired=true` |
| 3 | Signals | `Signaler` is an **optional** interface (type-assert + panic if absent); `Wait[T]` is a `Do` that polls a mailbox; `Deliver` pushes from outside; delivery may precede `Wait` (queue, don't hand off) | `signals`: `Phase A (live delivery): finished=true approved=true` / `Phase B (journaled signal, crash recovery): finished=true approved=true` |
| 4 | Versioning | `Version(w, changeID, min, max)` journals `max`, replays the pinned value, panics if journaled version ∉ `[min,max]`; branch on the returned int; new `changeID` per change | `versioning`: `old run (pinned to v1) fraud checks run: 0` / `new run (v2) fraud checks run: 1` |

**Part II traps:** (1) don't add a status re-check for *correctness* — replay of a fully journaled
run has no side effects, so a redundant acquire is harmless (a real system adds it only as an
optimization). (2) The deadline must be journaled **before** the wait, or a mid-sleep crash records
nothing (back to Day 3). (3) `PushSignal` must **queue**, so a signal delivered before `Wait` isn't
lost. (4) The `[min,max]` guard is a rollback guard — deleting it turns a wrong-branch bug silent;
a `Version` marker only protects runs that *recorded* it.

**Runtime caveat:** as in Day 4, the Postgres backend's runtime is not exercised in the tutorial
text; the `storetest.Contract` (its "lock is exclusive then releasable" case) is what pins it. Run
it against a real database in your env before keeping the multi-process claim.

---

## 9. Mutation Testing Gate

Coverage says a line *ran*; mutation says an assertion would *notice it being wrong*. `tools/mutate`
applies each known fault to a fresh copy of the source, runs the unit suite, restores the file, and
asserts the outcome. It must **exit non-zero on any mismatch** so `make mutate` gates CI.

**Expected `go run ./tools/mutate` output (verbatim target):**
```
MUTANT                       OUTCOME   EXPECTED  STATUS    CATCHER / REASON
guard && -> ||               killed    killed    ok        TestPartialReplay_ResumesMidway
guard off-by-one < -> <=     killed    killed    ok        TestPartialReplay_ResumesMidway
determinism check != -> ==   killed    killed    ok        TestReplayStep_DeterminismPanic
drop journaled step error    killed    killed    ok        TestDo_ReplayPreservesError
success marked failed        killed    killed    ok        TestWorkflow_SuccessSetsDone
remove w.replay = false      survived  survived  ok        redundant: seq < len(logs) already gates replay

6 mutants: 5 killed, 1 survived (1 equivalent expected)
PASS: every mutant matched expectation
```

**Two lessons to confirm the human understands:**
- The `&&`→`||` and `<`→`<=` flips are killed **only** by the **partial-replay** test. A fresh run
  has both guard sides false, so `&&` and `||` agree — only resuming past a partial journal exposes
  them. This is why the mid-crash recovery test is the most important test in the suite.
- **`remove w.replay = false` is a genuine equivalent mutant** and must **survive**: the guard's
  `w.seq < len(w.logs)` already governs replay-vs-live, `seq` only advances, and the journal doesn't
  grow mid-run, so the flag is redundant. An honest equivalent is recorded **once** and never
  re-investigated. A perfect kill rate is a number to interrogate, not chase.

**Fuller mutant map** (extending `tools/mutate` with these is a Day 6 exercise):

| Mutation | Effect | Killed by |
|---|---|---|
| `w.seq++` removed in replay | cursor stuck, same entry forever | TestMultiStepReplay_AllStepsSkipped |
| `store.Append` removed | nothing journaled | TestDo_ReplaySkipsExecution |
| `obs.OnStep` removed | observability broken | TestObserver_ReceivesAllEvents |
| panic removed on tag mismatch | corruption goes silent | TestReplayStep_DeterminismPanic (white-box) |
| `Incomplete` includes terminal runs | recovery re-runs completed work | TestRecover_OnlyIncomplete |
| `Incomplete` drops Running | recovery misses crashed runs | TestDo_ReplaySkipsExecution |

> For exhaustive coverage, periodically run a general tool (`gremlins`, `go-mutesting`) and triage
> survivors into "real gap, write a test" or "equivalent, document it." Keep equivalents listed in
> code so a future reader doesn't re-investigate them.

---

## 10. Do-Not / Anti-Patterns — guardrails

**Correctness (these corrupt runs):**
1. Do **not** persist a status column *instead of* step results — a status tells you a step
   finished, not what it produced; you can't resume from it.
2. Do **not** compute anything non-deterministic outside a `Do` (time, `rand`, branch-steering
   reads, map-range order). Capture it inside a step. (§2)
3. Do **not** omit `ORDER BY seq` in any `LoadLogs`. (§7 Day 4)
4. Do **not** convert the determinism panic (or corruption/marshal panic) into a `Failed` status.
   (§2, §6)
5. Do **not** journal a sleep *after* waiting (Part II) — journal the deadline first. (§8)
6. Do **not** let `Start` block the caller — launch `exec` in a goroutine.
7. Do **not** make recovery a separate code path — it must reuse `exec`.

**Honesty (blocks shipping):**
8. Do **not** claim plain "exactly once." State: durable, resumable, at-least-once, exactly-once
   only with idempotent steps. Ship a **Non-goals & Limitations** section (at-least-once window; no
   built-in retry policy/timeouts/cancellation; lock-polling scaling ceiling; not a Temporal
   replacement). (§1, §11)

**Style (tutorial's grain — flag, don't rewrite for them):**
9. Comments explain **why**, never restate **what**. No `// create a new engine`.
10. Short names in tight scopes (`e`, `l`, `s`, `r`, `w`); descriptive names only for exported/
    package-level decls.
11. `any`, never `interface{}`. `context.Context` first param of anything that does I/O or blocks.
12. Functional options for `New` only — no builder pattern, no method chaining. No `init()`. No
    `utils`/`helpers` packages. No getters/setters — export the field or don't.
13. Test names are `TestX_Behavior`, scannable. Match the tutorial's names (the §9 gate depends on
    them).

---

## 11. Ship & MVP Acceptance Checklist

**Day 7 ship checklist (all must hold):**
- [ ] `go test -race ./...` green; `go vet ./...` clean.
- [ ] `make mutate` passes (5 killed, documented equivalent survives).
- [ ] `storetest.Contract` passes against **every** backend you ship (memstore + SQLite run for real).
- [ ] `README` has a runnable example, the determinism rule, and the backend-swap line.
- [ ] Public API is the §3 minimal set; everything else is `internal/` or an interface.
- [ ] `go.mod` declares the real module path; `go mod tidy` resolves `modernc.org/sqlite`.
- [ ] `LICENSE` exists and was chosen deliberately.
- [ ] The determinism contract appears where a new user sees it before writing their first workflow.

**MVP cut line (for turning it into a library others depend on):**
- **Make it honest:** README off "exactly once"; add Non-goals & Limitations. (§10.8)
- **Make it usable for one real workflow:** thread workflow **input** into `Start` (journaled as the
  run's seed), read it with `Input[T]`, return a journaled **result** fetched with `Result[T]`, and
  persist the terminal error. (The one feature separating a demo from a library.)
- **Package it:** real module path everywhere; name+year in LICENSE; run SQLite/Postgres against
  `storetest.Contract` on a real DB (or label them experimental); add `doc.go`; commit `go.mod` +
  `go.sum`.
- **Make it adoptable:** README (one honest sentence + install + smallest example + determinism +
  persistence + non-goals + `v0.x unstable` status); `CONTRIBUTING.md`, `SECURITY.md`,
  `CHANGELOG.md`; tag `v0.1.0` and push.

Everything past the cut line (built-in `RetryPolicy` with backoff, per-step timeouts, run
cancellation, a public list/get admin surface) is a deliberate **v0.2 fast-follow**, not a launch
blocker — the retry pattern already works as a loop over `Do`.

---

## 12. Validation Review Workflow — how a review pass runs

When asked to validate progress:
1. **Locate scope.** Which day(s) does the human claim complete? Default to auditing all completed
   days plus the newest.
2. **Static pass.** For each file in scope, check it against its §7/§8 contract and §10 anti-patterns.
   Report deviations as `file:line → finding → tutorial section`.
3. **Determinism pass.** Scan every workflow/example for §2 violations first.
4. **Dynamic pass.** Run each in-scope milestone command (§5) and diff stdout against the expected
   block. Run `go test -race ./...`. Run `go run ./tools/mutate` if Day 6+ is claimed.
5. **Report.** Per gate: **PASS / FAIL / BLOCKED**, with the observed output pasted as evidence.
   No gate is PASS without evidence (§"How to use this file").
6. **Cross-check exercises.** The tutorial's per-day exercises (worked solutions in Appendix C for
   Days 1–7, Appendix B for Part II) are extra probes — use them to challenge a claimed-complete day
   (e.g. Day 2 ex. 1: add a 4th step, predict live-execution count on recovery = 2).
7. **Regression check.** Confirm later work didn't break earlier gates.

Report format per finding:
```
[Day N · Gate name] FAIL
  where:   store.go:88  (LoadLogs)
  problem: missing ORDER BY seq
  ref:     tutorial Day 4 Trap; §7 Day 4, §10.3
  effect:  recovered runs corrupt under out-of-order row return
```

---

## 13. Validation Status Tracker

Update as gates are verified (with evidence — a pasted milestone output or `ok ... -race` line).

| Gate | Command | Status | Evidence / notes |
|---|---|---|---|
| Day 1 — skeleton journals | `go run ./examples/skeleton` | ☐ not verified | |
| Day 2 — resume from partial journal | `go run ./examples/recover` | ☐ not verified | |
| Day 3 — durable sleep survives restart | `go run ./examples/durablesleep` | ☐ not verified | |
| Day 4 — SQLite behind Store contract | `go mod tidy && go test -race ./sqlite/` | ☐ not verified | runtime unverified until run locally |
| Day 5 — suite green under race | `go test -race ./...` | ☐ not verified | |
| Day 6 — mutation gate | `go run ./tools/mutate` | ☐ not verified | 5 killed + 1 equivalent |
| Day 7 — capstone charges once | `go run ./examples/capstone` | ☐ not verified | |
| Ship checklist (§11) | manual | ☐ not verified | |
| Part II · 1 — multi-process | `go run -race ./examples/workers` | ☐ n/a unless in scope | |
| Part II · 2 — durable timers | `go run ./examples/durabletimer` | ☐ n/a unless in scope | |
| Part II · 3 — signals | `go run ./examples/signals` | ☐ n/a unless in scope | |
| Part II · 4 — versioning | `go run ./examples/versioning` | ☐ n/a unless in scope | |

---

*Source of truth: `Building-rerun-in-7-Days.md` (the tutorial). Where this guardrail and the
tutorial disagree, the tutorial wins — and fix this file. The prior contents of this path (an AI
code-generation build prompt) were preserved as `rerun-build-prompt.md`.*
