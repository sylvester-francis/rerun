<!--
Copyright 2026 Sylvester Francis
Licensed under the Apache License, Version 2.0. See the LICENSE file.
-->

# Using `rerun` in your application

A practical guide to wiring `rerun` into a real Go service. If you want the
*why* behind durable execution, read [`durable-execution.md`](durable-execution.md)
first; this guide is the *how*.

The whole model in one sentence: **a workflow is an ordinary Go function whose
side effects are wrapped in `Do`; the engine journals each step's result and, on
restart, replays the journal so completed steps are never re-run.**

---

## 1. Install

```sh
go get github.com/sylvester-francis/rerun
```

The core needs Go 1.18+. The bundled SQLite and Postgres backends raise the
module floor to Go 1.25; if you only use the in-memory store or your own
`Store`, they never enter your build.

---

## 2. Define and run a workflow

Build one `*rerun.Engine` for your process, register each workflow once with
`Handle`, and `Start` runs by ID.

```go
package main

import (
	"context"
	"time"

	"github.com/sylvester-francis/rerun"
	"github.com/sylvester-francis/rerun/sqlite"
)

func main() {
	ctx := context.Background()
	e := rerun.New(sqlite.New("rerun.db"))

	e.Handle("signup", func(w *rerun.W) error {
		acct, err := rerun.Do(w, "create-account", func(ctx context.Context) (string, error) {
			return createAccount(ctx)
		})
		if err != nil {
			return err
		}
		rerun.Sleep(w, 24*time.Hour) // durable: survives a restart
		_, err = rerun.Do(w, "welcome-email", func(ctx context.Context) (struct{}, error) {
			return struct{}{}, sendEmail(ctx, acct)
		})
		return err
	})

	e.Recover(ctx)               // 1. resume runs left in flight by the last process
	e.Start(ctx, "signup", "u1") // 2. launch a new run
	select {}                    // keep the process alive
}
```

`Start` returns as soon as the run is durably created; the workflow runs on its
own goroutine, so one process drives thousands of concurrent runs.

---

## 3. Wire it into your service lifecycle

Two integration points, and that's the whole discipline:

**On startup, call `Recover` once**, after registering your workflows. It finds
every run left `Pending`/`Running` by the previous process and resumes it from
its journal:

```go
e := rerun.New(store)
registerWorkflows(e)
if err := e.Recover(ctx); err != nil {  // resume crashed runs
    log.Fatal(err)
}
```

**To kick off work, call `Start`** — typically from an HTTP handler, a queue
consumer, or a cron job:

```go
func handleSignup(w http.ResponseWriter, r *http.Request) {
    runID := "signup:" + userID(r)       // your idempotency key
    err := engine.Start(r.Context(), "signup", runID, userID(r))
    if err != nil {
        // Create fails if runID already exists — starting the same run twice
        // is rejected, which is how you dedupe retried requests.
        http.Error(w, "already started", http.StatusConflict)
        return
    }
    w.WriteHeader(http.StatusAccepted)
}
```

The **run ID is your idempotency key.** Derive it from the thing the workflow is
about (an order ID, a user ID) and starting the same run twice is safely
rejected by the store.

---

## 4. Passing input and reading a result

Thread data in through `Start`, read it back with `Input[T]`; hand a value out
with `Return[T]`, read it with `Result[T]`.

```go
e.Handle("charge-order", func(w *rerun.W) error {
    order, err := rerun.Input[Order](w) // the seed passed to Start
    if err != nil {
        return err
    }
    txn, err := rerun.Do(w, "charge", func(ctx context.Context) (string, error) {
        return charge(ctx, order)
    })
    if err != nil {
        return err
    }
    rerun.Return(w, Receipt{TxnID: txn}) // the run's result
    return nil
})

e.Start(ctx, "charge-order", "order-99", Order{ID: "order-99", Cents: 1999})

// ...later, once the run is Done:
receipt, err := rerun.Result[Receipt](ctx, e, "order-99")
```

The input is journaled as the run's seed, so replay always sees the same value.
`Result` reads through the store, so it works from any process; on a failed run
it returns the terminal error instead of a value.

---

## 5. Steps, errors, retries, and timeouts

**Every side effect and every nondeterministic read goes in a `Do`** with a
stable tag. A step's error is journaled too: a step that failed replays the
*same* error rather than re-running.

**Retry** is built in — each attempt is its own journaled step, and the backoff
uses the durable `Sleep`, so a crash mid-backoff resumes correctly:

```go
txn, err := rerun.Retry(w, "charge",
    rerun.RetryPolicy{MaxAttempts: 4, Backoff: rerun.ExpBackoff(time.Second, time.Minute)},
    func(ctx context.Context) (string, error) { return charge(ctx, order) })
```

**Per-step timeouts** run a step under a deadline. The outcome — value or
`DeadlineExceeded` — is journaled, so a timed-out step never silently re-runs
into a success on replay (your `fn` must honor `ctx`):

```go
res, err := rerun.DoTimeout(w, "fetch-quote", 5*time.Second,
    func(ctx context.Context) (Quote, error) { return getQuote(ctx) })
```

---

## 6. Durable waits and external events

**`Sleep`** is a durable delay — it journals an absolute deadline and, on
recovery, waits only the time still remaining (or returns instantly if the
deadline passed while the process was down).

**Signals** let a step's value arrive from outside — an approval click, a
webhook. `Wait[T]` blocks until a named signal arrives and journals it; `Deliver`
deposits one from anywhere (an HTTP handler, another service):

```go
e.Handle("approval", func(w *rerun.W) error {
    ok, err := rerun.Wait[bool](w, "manager-approval") // blocks, survives restarts
    if err != nil || !ok {
        return err
    }
    return finishApproval(w)
})

// from your webhook handler, possibly days later and after restarts:
engine.Deliver(ctx, "approval:123", "manager-approval", true)
```

> **Note:** signals require a `Store` that implements `Signaler`. All three
> bundled backends do, so delivery is **durable**: an approval delivered while
> the process is down waits in the store and is there when the run recovers. A
> signal delivered before the workflow even reaches `Wait` is queued, not lost.

---

## 7. Cancellation

`Cancel` stops a run — a parked `Sleep` or a `ctx`-respecting step unwinds and
the run finishes with the `Cancelled` status:

```go
engine.Cancel(ctx, "order-99")
```

If the run is executing **in this process**, it's cancelled immediately. To
cancel a run executing on **another node**, enable polling — every bundled
backend records cancel requests durably (they implement `Canceller`):

```go
e := rerun.New(store, rerun.WithCancelPoll(time.Second))
```

Now `Cancel` on any engine records the request in the store, and the worker
running the run notices it within the poll interval and unwinds — cross-process
cancellation, eventual rather than instant. `WithCancelPoll` defaults to off, so
a parked run stays free until you opt in.

Even with polling off, a cancel is **durable** when the store implements
`Canceller`: `Cancel` records the request before unwinding the local run, so any
later claim of that run — after a restart, say — sees the request and finishes it
`Cancelled` without ever executing the workflow. A cancel that has landed cannot
be lost to a crash.

---

## 8. Versioning across deploys

When you change a workflow that has runs in flight, gate the change behind
`Version` so old runs keep their original path and new runs take the new one:

```go
e.Handle("order", func(w *rerun.W) error {
    v := rerun.Version(w, "add-fraud-check", 1, 2) // this build knows up to v2
    rerun.Do(w, "validate", validate)
    if v >= 2 {
        rerun.Do(w, "fraud-check", fraudCheck) // added in v2; old runs skip it
    }
    return rerun.Do(w, "charge", charge)
})
```

Give every change its own `changeID`; never edit an old branch, add a new one
behind a higher version and let old runs drain.

---

## 9. Choosing and configuring a backend

Persistence is the `Store` seam; the workflow code is identical against any of
them:

| Backend | Import | Use it for |
|---|---|---|
| In-memory | `internal.NewMemStore()` | tests, and single-process runs that need not outlive a restart |
| SQLite | `sqlite.New("app.db")` | one persistent node; pure Go, no CGO, a single file |
| Postgres | `postgres.New(dsn)` | multiple processes/machines sharing runs, with a real advisory-lock lease |

Swapping is one line. Configure the other seams with functional options:

```go
e := rerun.New(store,
    rerun.WithCodec(myCodec),       // default: JSON
    rerun.WithClock(myClock),       // default: wall clock (inject a fake in tests)
    rerun.WithObserver(myObserver), // default: no-op
)
```

Bring your own `Store` by implementing six methods and proving it with the
public contract suite:

```go
func TestMyStore(t *testing.T) {
    storetest.RunStoreContract(t, func() rerun.Store { return myStore() })
}
```

---

## 10. The two rules you must follow

**1 · Determinism.** Replay matches journaled results to `Do` calls by position
and tag, so the workflow body must issue the same steps, with the same tags,
every run. Anything nondeterministic — the clock, randomness, a read that steers
a branch — must be captured *inside* a `Do`:

```go
// ✗ Wrong: the branch differs between the first run and replay.
if time.Now().Hour() < 12 { rerun.Do(w, "morning", ...) } else { rerun.Do(w, "evening", ...) }

// ✓ Right: journal the decision, then branch on the recorded value.
morning, _ := rerun.Do(w, "is-morning", func(ctx context.Context) (bool, error) {
    return time.Now().Hour() < 12, nil
})
if morning { ... }
```

A divergent tag panics loudly at the exact position — a determinism bug fails
fast instead of silently corrupting a run.

**2 · Idempotency.** `rerun` is at-least-once for side effects: a step repeats
only if the process dies after its side effect runs but before its journal entry
commits. Write side-effecting steps (a charge, an email, a shipment) to tolerate
being run twice — use an idempotency key the downstream service deduplicates.
This is the one-line caveat behind the "exactly-once" headline.

---

## 11. Observability

Implement `Observer` to feed lifecycle events into your logs and metrics
(observers receive values and cannot mutate engine state):

```go
type metrics struct{}

func (metrics) OnStart(r rerun.Run)                 { started.Inc() }
func (metrics) OnStep(runID string, l rerun.Log)    { steps.Inc() }     // live steps only, not replays
func (metrics) OnFinish(runID string, s rerun.Status) { finished.WithLabelValues(s.String()).Inc() }

e := rerun.New(store, rerun.WithObserver(metrics{}))
```

`OnStep` fires only for steps that execute live, never on replay — so it counts
real work, not recovery.

---

## 12. What `rerun` does not do

`rerun` is `v0.x` and deliberately small. It has no distributed timer service (a
sleeping run parks a cheap goroutine, which scales to thousands, not millions),
and it is not a Temporal replacement (no UI, namespaces, or cross-language SDKs).
See the README's *Guarantees & non-goals* for the full boundary. Everything up to
that line — durable steps, retries, timeouts, cancellation, durable sleep,
signals, versioning, and pluggable persistence — is here today.
