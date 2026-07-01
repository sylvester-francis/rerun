# Gate map — phase → command → success marker

The authoritative gate definitions and full expected output live in `rerun-prompt.md`
(§7 Days, §8 Part II, §9 mutation, §11 ship). This table is the quick index the gate
script and a manual pass use. Run from the repo root.

## Days 1–7 (correctness — blocking)

| Phase | Command | Passes when stdout contains | Teach |
|---|---|---|---|
| Day 1 skeleton | `go run ./examples/skeleton` | `payload="hello Ada"`, `status: Done` | teaching-days-1-7 §Day 1 |
| Day 2 recover | `go run ./examples/recover` | `steps executed live during recovery: 1` | §Day 2 |
| Day 3 durable sleep | `go run ./examples/durablesleep` | `skips the wait` (recovery ≈ 0s) | §Day 3 |
| Day 4 SQLite | `go mod tidy && go test -race ./sqlite/` | suite `ok` (run locally; not in gate — no tree writes) | §Day 4 |
| Day 5 harness | `go test -race ./...` | all `ok`, none `FAIL` | §Day 5 |
| Day 6 mutation | `go run ./tools/mutate` | `PASS: every mutant matched expectation` | §Day 6 |
| Day 7 capstone | `go run ./examples/capstone` | `charges(live)=0`, `u2 status Done? true` | §Day 7 |

Umbrella checks the gate also runs: `go build ./...`, `go vet ./...`, and a scan for
`not implemented` / `t.Skip` markers.

> Day 4 note: the gate does **not** run `go mod tidy` (it never writes your tree). Run the
> SQLite contract test yourself once with `go mod tidy && go test -race ./sqlite/`. Until then
> the persistence claim is unverified — same honest exception the tutorial flags on Day 4.

## Part II (validated only if the example dir exists)

| Problem | Command | Passes when stdout contains | Teach |
|---|---|---|---|
| HP1 multi-process | `go run ./examples/workers` | `finalize executed live: 12` | teaching-part-2 §HP1 |
| HP2 durable timers | `go run ./examples/durabletimer` | `fired=true` (20m remainder honored) | §HP2 |
| HP3 signals | `go run ./examples/signals` | `approved=true` (both phases) | §HP3 |
| HP4 versioning | `go run ./examples/versioning` | `fraud checks run: 1` (new run) | §HP4 |

Part II evolves two interfaces — do not flag them against Day-1 signatures:
`Guarder.Acquire` gains an `acquired bool`; `Sleep` journals an absolute deadline. See
teaching-part-2 §Evolved interfaces.

## MVP / polish (warnings; block only under `RERUN_GATE=strict`)

- README states guarantee honestly (no bare "exactly once" without idempotency caveat).
- `CONTRIBUTING.md`, `SECURITY.md`, `CHANGELOG.md`, `doc.go` present.
- `go.sum` committed once `modernc.org/sqlite` is wired.
- Full ship checklist: `rerun-prompt.md` §11. Teach: teaching-mvp.
