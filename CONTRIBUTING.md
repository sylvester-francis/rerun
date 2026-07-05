# Contributing to rerun

Thanks for your interest. rerun is a small, correctness-critical library, so the
bar is: changes keep it correct, and the tests prove it.

## Prerequisites

- Go 1.25 or newer (the pure-Go SQLite backend sets the module floor; the core
  engine itself only needs generics, Go 1.18+).
- Docker, only if you want to run the Postgres backend tests locally.

## The local bar

```sh
make           # go vet + lint + go test -race + the mutation gate + doc-check
make lint      # gofmt cleanliness + staticcheck
make doc-check # fail on any undocumented exported symbol
make pg-test   # Postgres store contract against an ephemeral container (needs Docker)
```

A change is ready when `make` is green: `go vet ./...` clean, `gofmt` and
`staticcheck` clean, `go test -race ./...` green (this includes the SIGKILL
crash-injection harness under `crashtest/`), every exported symbol documented,
and `go run ./tools/mutate` reporting `PASS: every mutant matched expectation`.

CI runs the same bar across a ubuntu + macOS matrix, a Windows build, and the
Postgres contract against a service container; `govulncheck` runs nightly. The
`docs/using-rerun.md` guide and the landing site are published from `main`.

## What the mutation gate expects of a test

Coverage says a line ran; mutation says an assertion would notice it being
wrong. If you add engine behavior, add a test that *fails* when that behavior is
broken, and consider adding the corresponding fault to `tools/mutate`. If a
mutant survives, decide honestly whether it is a real gap (write a test) or an
equivalent mutant (record it in `tools/mutate` with a one-line reason, as the
`w.replay = false` mutant already is).

## Design rules

- **Determinism first.** Anything nondeterministic belongs inside a `Do` so its
  value is journaled and replayed. Never compute time, randomness, or a
  branch-steering read in the workflow body itself.
- **The dependency rule.** The engine depends only on the interfaces it defines
  (`Store`, `Codec`, `Clock`, `Observer`). Backends import `rerun`; the engine
  imports no backend. A new backend must change zero lines of engine code.
- **Panic vs. error.** Panic on programmer errors (determinism violation,
  duplicate workflow, un-marshalable result). Return errors for operational
  conditions (a step's business failure, a cancelled context). A panic inside a
  run is *contained* to that run — the engine's recover marks it `Stuck` and the
  process keeps serving its other runs; a panic must never escape a run's
  goroutine.
- **Style.** Short names in tight scopes; comments explain *why*, not *what*;
  `any`, never `interface{}`; `context.Context` first on anything that blocks or
  does I/O.

## A new backend

Implement `rerun.Store` and prove it with the shared contract:

```go
func TestMyStore(t *testing.T) {
    storetest.RunStoreContract(t, func() rerun.Store { return myStore() })
}
```

Optionally implement `rerun.Signaler` for external-event support.

## Proposing a change

Substantial changes (a new backend, a change to the engine, semantics, or a public
contract) start with an **Architecture Decision Record**, not a pull request:

1. Copy an existing record in [`docs/adr/`](docs/adr/) to a new numbered file with
   status *Proposed*, describing the problem, the options, and the decision.
2. Open the ADR as a pull request. Once it is approved, implement it and open the
   implementation pull request referencing the ADR.

Obvious small fixes (typos, docs, a clear bug with an obvious fix) can skip the ADR
and go straight to a focused pull request.

## Pull requests

Keep them focused, and confirm `make` is green. A PR that changes more than **20
files** is automatically blocked; split it into smaller PRs (an approved ADR can
carry the plan across them). By contributing you agree your work is licensed under
Apache-2.0.

## Forks

rerun is Apache 2.0, so you are free to fork and modify it. If your fork improves
rerun, please send the improvement back as a pull request instead of letting it
diverge. Upstreaming keeps everyone on one maintained line and gets your change
reviewed and released. This is a request, not a license term.
