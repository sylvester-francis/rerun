# Contributing to rerun

Thanks for your interest. rerun is a small, correctness-critical library, so the
bar is: changes keep it correct, and the tests prove it.

## Prerequisites

- Go 1.25 or newer (the pure-Go SQLite backend sets the module floor; the core
  engine itself only needs generics, Go 1.18+).
- Docker, only if you want to run the Postgres backend tests locally.

## The local bar

```sh
make          # go vet + go test -race + the mutation gate
make pg-test  # Postgres store contract against an ephemeral container (needs Docker)
```

A change is ready when `make` is green. `go test -race ./...` must stay green,
`go vet ./...` must be clean, and `go run ./tools/mutate` must report
`PASS: every mutant matched expectation`.

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
  unknown/duplicate workflow, un-marshalable result). Return errors for
  operational conditions (a step's business failure, a store write failure, a
  cancelled context).
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

## Pull requests

Keep them focused. Explain what changed and why, and confirm `make` is green.
By contributing you agree your work is licensed under Apache-2.0.
