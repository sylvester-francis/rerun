#!/usr/bin/env bash
# rerun tutorial completeness gate.
#
# Deterministic checks shared by the rerun-tutorial-validator skill (check mode)
# and .githooks/pre-push. Exit 0 only when the working tree is a complete,
# correct rerun build that is safe to push.
#
#   Exit 0  safe to push
#   Exit 1  a blocking gate failed (incomplete or broken work)
#
# RERUN_GATE env:
#   off      skip every check (bypass)
#   strict   polish/MVP warnings also block
#   (unset)  correctness gates block; polish issues are warnings only
#
# Run standalone:
#   bash .claude/skills/rerun-tutorial-validator/scripts/gate.sh
#
# This gate never writes to your tree (no `go mod tidy`, no formatting).
set -uo pipefail

mode="${RERUN_GATE:-}"
if [ "$mode" = "off" ]; then
  echo "rerun-gate: skipped (RERUN_GATE=off)"
  exit 0
fi

root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$root" || { echo "rerun-gate: cannot enter repo root"; exit 1; }

# Colors only on a TTY so hook logs stay clean.
if [ -t 1 ]; then
  R=$'\033[31m'; G=$'\033[32m'; Y=$'\033[33m'; B=$'\033[1m'; Z=$'\033[0m'
else
  R=''; G=''; Y=''; B=''; Z=''
fi

blk=0    # blocking failures
warns=0  # non-blocking warnings

pass()   { printf '  %s✓%s %s\n' "$G" "$Z" "$1"; }
fail()   { printf '  %s✗%s %s\n' "$R" "$Z" "$1"; blk=$((blk + 1)); }
warn()   { printf '  %s!%s %s\n' "$Y" "$Z" "$1"; warns=$((warns + 1)); }
detail() { printf '%s\n' "$1" | sed 's/^/      /'; }
section() { printf '\n%s%s%s\n' "$B" "$1" "$Z"; }

# Optional timeout wrapper so a hung program cannot wedge a push.
TO=""
if command -v timeout  >/dev/null 2>&1; then TO="timeout 90";
elif command -v gtimeout >/dev/null 2>&1; then TO="gtimeout 90"; fi

# run <label> <expected-substring|""> <cmd...>
# Fails if the command errors, or (when given) its output lacks the substring.
run() {
  local label="$1" want="$2"; shift 2
  local out rc
  out="$($TO "$@" 2>&1)"; rc=$?
  if [ $rc -ne 0 ]; then
    fail "$label"
    detail "$(printf '%s\n' "$out" | tail -8)"
    return 1
  fi
  if [ -n "$want" ] && ! printf '%s' "$out" | grep -qF -- "$want"; then
    fail "$label — ran, but expected output missing: \"$want\""
    detail "$(printf '%s\n' "$out" | tail -8)"
    return 1
  fi
  pass "$label"
  return 0
}

# --- preflight ---------------------------------------------------------------
if ! command -v go >/dev/null 2>&1; then
  printf '%s✗ rerun gate: Go toolchain not found; cannot verify completeness.%s\n' "$R" "$Z"
  echo   "  Install Go, or bypass this push with: git push --no-verify"
  exit 1
fi

# --- incompleteness markers --------------------------------------------------
section "Completeness markers"
# BLOCKING: explicit "not done" signals in Go source.
blockers="$(grep -RIn -E 'not implemented|panic\("TODO|t\.Skip\(' --include='*.go' \
  --exclude-dir='.git' . 2>/dev/null || true)"
if [ -n "$blockers" ]; then
  nblk="$(printf '%s\n' "$blockers" | grep -c .)"
  fail "unfinished Go code: ${nblk} not-implemented / t.Skip marker(s)"
  printf '%s\n' "$blockers" | head -n 10 | sed 's/^/      /'
  [ "$nblk" -gt 10 ] && printf '      … and %s more\n' "$((nblk - 10))"
else
  pass "no not-implemented / t.Skip markers"
fi
# WARNING: leftover TODO/FIXME comments.
todos="$(grep -RIn -E '// *(TODO|FIXME)' --include='*.go' --exclude-dir='.git' . 2>/dev/null || true)"
[ -n "$todos" ] && warn "TODO/FIXME comments present ($(printf '%s\n' "$todos" | wc -l | tr -d ' '))"

# --- build & static ----------------------------------------------------------
section "Build & static analysis"
run "go build ./..." "" go build ./...
run "go vet ./..."   "" go vet ./...

# --- tests (race) ------------------------------------------------------------
section "Tests"
run "go test -race ./..." "" go test -race ./...

# --- Day 1-7 milestones (examples must produce their real output) ------------
section "Day 1-7 milestones"
run "Day 1 skeleton"     "hello Ada"                             $TO go run ./examples/skeleton
run "Day 2 recover"      "steps executed live during recovery: 1" $TO go run ./examples/recover
run "Day 3 durablesleep" "skips the wait"                        $TO go run ./examples/durablesleep
run "Day 7 capstone"     "charges(live)=0"                       $TO go run ./examples/capstone

# --- Day 6 mutation gate -----------------------------------------------------
section "Mutation gate"
run "go run ./tools/mutate" "PASS: every mutant matched expectation" $TO go run ./tools/mutate

# --- Part II (validated only if those examples exist) ------------------------
part2=0
for d in workers durabletimer signals versioning; do
  [ -d "examples/$d" ] && part2=1
done
if [ $part2 -eq 1 ]; then
  section "Part II (present)"
  [ -d examples/workers ]      && run "HP1 workers"      "finalize executed live: 12" $TO go run ./examples/workers
  [ -d examples/durabletimer ] && run "HP2 durabletimer" "fired=true"                 $TO go run ./examples/durabletimer
  [ -d examples/signals ]      && run "HP3 signals"      "approved=true"              $TO go run ./examples/signals
  [ -d examples/versioning ]   && run "HP4 versioning"   "fraud checks run: 1"        $TO go run ./examples/versioning
fi

# --- MVP / polish (warnings; block only under RERUN_GATE=strict) -------------
section "MVP / polish (warnings)"
if [ -f README.md ]; then
  if grep -Eiq 'exactly[ -]once' README.md && ! grep -iq 'idempoten' README.md; then
    warn "README says \"exactly once\" without the idempotency caveat (see rerun-prompt.md §1)"
  else
    pass "README guarantee wording"
  fi
fi
for f in CONTRIBUTING.md SECURITY.md CHANGELOG.md doc.go; do
  [ -f "$f" ] || warn "missing $f (MVP adoptability checklist)"
done
if grep -q 'modernc.org/sqlite' go.mod 2>/dev/null && [ ! -f go.sum ]; then
  warn "go.mod requires modernc.org/sqlite but go.sum is absent — run: go mod tidy"
fi

# --- verdict -----------------------------------------------------------------
printf '\n%s\n' "----------------------------------------------------------------"
if [ "$mode" = "strict" ] && [ $warns -gt 0 ]; then
  blk=$((blk + warns))
  echo "RERUN_GATE=strict: warnings count as blocking."
fi
if [ $blk -gt 0 ]; then
  printf '%s✗ rerun gate: %d blocking issue(s) — push blocked.%s\n' "$R" "$blk" "$Z"
  echo   "  Understand & fix: run the rerun-tutorial-validator skill (it teaches each gap)."
  echo   "  Reference:        rerun-prompt.md (gates + expected output)."
  echo   "  Bypass one push:  git push --no-verify      Skip gate: RERUN_GATE=off git push"
  exit 1
fi
if [ $warns -gt 0 ]; then
  printf '%s! rerun gate passed with %d warning(s).%s Push allowed.\n' "$Y" "$warns" "$Z"
else
  printf '%s✓ rerun gate: complete. Push allowed.%s\n' "$G" "$Z"
fi
exit 0
