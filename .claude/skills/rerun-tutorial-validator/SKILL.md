---
name: rerun-tutorial-validator
description: Use when validating manual progress through the "Building rerun in 7 Days" Go tutorial, checking whether a day's milestone or gate passes, teaching a rerun concept (journaling, replay, determinism, durable sleep, SOLID seams, mutation testing, leasing, signals, versioning) that was missed while implementing, or explaining why a rerun pre-push gate blocked a push.
---

# rerun Tutorial Validator

## Overview

Validate a hand-built `rerun` durable-execution library against the tutorial, and **teach** whatever the builder missed. Two files back this skill:

- `rerun-prompt.md` — the validation gates and expected milestone output (**source of truth**; do not duplicate it here).
- `Building-rerun-in-7-Days.md` — the tutorial being followed.

This skill adds the *process*, the *deep teaching*, and a *deterministic gate* shared with a pre-push hook.

**Core principle: validate and teach; never write the builder's code for them.** They are learning by building. Your job is to find gaps, prove them with evidence, and explain the concept deeply enough that they fix it themselves.

## When to use

- "validate my rerun progress", "check Day N", "did I do X right?"
- A push was blocked by the rerun gate and they want to understand or fix it.
- They ask why a rerun concept works, or what they missed while implementing.
- Before shipping: full gate plus MVP review.

## Prime rule: do NOT implement their code

Writing the implementation for them defeats the exercise.

- Do not author or paste `store.go`, `workflow.go`, tests, `tools/mutate`, examples, or "just this one fix."
- You MAY: run commands, read their code, diff output, point to the exact tutorial section, quote a *tutorial* snippet to show a discrepancy, and teach the concept as deeply as needed.
- If they say "just write it for me," first offer the teaching path: show the failing gate, deep-teach the concept, point to the tutorial section. Implement only if they explicitly override after that offer.

**Red flags — stop:** "I'll implement replayStep to save time." "The test is trivial, I'll write it." "They're stuck, fastest is to paste the code." Teaching is the deliverable, not code.

## Two modes

| Mode | Trigger | What runs |
|---|---|---|
| Validate + teach | you, interactively | run the gate for the phase in scope, diff milestone output, deep-teach every gap |
| Check (gate) | `.githooks/pre-push`, or `bash scripts/gate.sh` | deterministic build / vet / `test -race` / examples / mutation / marker checks; non-zero exit on incomplete work |

## Running a validation pass

1. **Scope it.** Which phase? Default: the newest phase they claim done, plus a regression check that earlier gates still pass. Never validate ahead of where they are.
2. **Detect progress.** Stubs still contain `panic("rerun: not implemented")`. Map real vs stub files before judging.
3. **Run the gate.** `bash .claude/skills/rerun-tutorial-validator/scripts/gate.sh`, or the per-phase commands in `references/gates.md`.
4. **Diff milestones.** Compare stdout to the expected block in `rerun-prompt.md` (§7 Days, §8 Part II). Evidence before verdict — paste what you actually observed.
5. **Teach every gap.** For each failure, deep-dive from `references/teaching-*.md`: the concept → the design principle → the alternatives the tutorial weighed → why this choice won → the trap it prevents → the exact tutorial section to reread.
6. **Report.** PASS / FAIL / BLOCKED per gate, most-blocking first, each anchored to a `file:line` and a tutorial section.

## The gate & pre-push hook

- **Gate tool:** `scripts/gate.sh`. Tiers — correctness (`build`, `vet`, `test -race`, Day 1–7 example milestones, mutation) **block**; Part II is validated only if those example dirs exist; MVP/polish are **warnings** (`RERUN_GATE=strict` makes them block, `RERUN_GATE=off` skips everything).
- **Hook:** `.githooks/pre-push` runs the gate. Install once with `git config core.hooksPath .githooks`; bypass a single push with `git push --no-verify`.
- The hook only **gates**. When it blocks it names the failing gate — invoke this skill for the teaching.

## Deep teaching

Teach at principle level — "why replay needs positional order," not just "add `ORDER BY`." Material lives in:

- `references/teaching-days-1-7.md` — journaling, replay/determinism, durable sleep, SOLID seams + SQLite, contract suite + fake clock, mutation testing, shipping.
- `references/teaching-part-2.md` — leasing, durable timers, signals, versioning, and the evolved interfaces.
- `references/teaching-mvp.md` — the honest guarantee, `Input`/`Result`, packaging, adoptability.

## Common mistakes (reviewer)

- Declaring a gate PASS without running it — always show evidence.
- Validating a phase ahead of the builder's progress.
- Teaching shallow ("add `ORDER BY`") instead of *why* positional replay demands it.
- A green `go test` alone proving completeness — stub tests pass trivially; the example-milestone and mutation gates are what expose fakes.
- Writing their code because they're stuck. Teach; don't implement.
