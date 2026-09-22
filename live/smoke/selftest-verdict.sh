#!/usr/bin/env bash
set -uo pipefail

# live/smoke/selftest-verdict.sh: proof for issue #1439.
#
# A scenario step called `kubectl auth can-i`, which exits non-zero when the
# answer is no, and under smoke.sh's `set -euo pipefail` the run ended on
# that line: no PASS line, no FAIL line, nothing naming the step. The same
# happens to `grep ... | evidence` when the grep matches nothing. The exit
# code was non-zero, but CLAUDE.md's rule is to read verdict lines and never
# exit codes, and a run that ends with neither has said nothing.
#
# This self-test runs the SHIPPED smoke.sh and lib.sh, copied into a scratch
# tree, over throwaway scenarios written here, with a stub choudoufu and a
# stub docker, and requires every way a run can end to end on exactly one
# verdict line with the exit status that line implies:
#
#   a death under set -e         FAIL naming the step, non-zero
#   grep | evidence, no match    the same
#   exit 0 outside a control     FAIL, exit 1
#   BREAK=1 with no caught line  FAIL naming the missing line, exit 1
#   BREAK=1, caught, exit 0      PASS naming the control, exit 0
#   BREAK=1, caught, runs on     the same (claim 39's shape)
#   BREAK_X=1 the scenario ignores   FAIL, exit 1
#   fail() by name               its one FAIL line, exit 1
#   the ordinary run             PASS, exit 0
#   a re-trapped EXIT            the status survives the scenario's teardown
#
# and then the CI reader, ci-run.sh, against logs with and without the lines.
# Last, one case is re-run against a copy of smoke.sh with the verdict
# removed and must fail there, so these checks are shown able to go red. No
# container is started and no network is used; it needs bash and python3.
#
# The verdict is the per-case lines below, not the exit code. Every case
# prints "ok:" per property it confirmed and "FAIL:" per property it did
# not, and the script exits non-zero if any FAIL was printed.
#
# Usage:
#   bash live/smoke/selftest-verdict.sh
#   SMOKE_SRC=<a copy of live/smoke> bash live/smoke/selftest-verdict.sh
#     runs the same cases against another tree, which is how the red was
#     shown: against live/smoke as of main before this issue, the first two
#     cases end with no verdict line and the exit-0 cases print PASS.

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SMOKE_SRC="${SMOKE_SRC:-$HERE}"
REPO_ROOT="$(cd "$HERE/../.." && pwd)"

WORK="$(mktemp -d)"
# The status is saved and re-raised, and a run that stops before its last
# case is refused (#1421, the shape #1419 gave selftest-oidc-bootstrap.sh).
# Measured on bash 3.2.57: an unbound-variable death under `set -e` hands
# the EXIT trap $?=0 and the script exits 0, which is how that one read as
# a pass when it died part way. This script runs without -e and such a
# death already exits 1; the flag is for the other way a run can stop early
# with status 0 - an exit reached inside code run in this shell, or an -e
# added later - and it turns that into a FAIL line and exit 1 as well.
selftest_finished=0
# shellcheck disable=SC2154 # selftest_rc is assigned on the trap's first line
trap 'selftest_rc=$?
      rm -rf "$WORK"
      if [ "$selftest_finished" != 1 ]; then
        echo "FAIL: this selftest stopped before its last case, so most of it never ran. Read the output above for where." >&2
        exit 1
      fi
      exit $selftest_rc' EXIT
PASS=1

log() { printf '%s\n' "$*"; }
ok() { log "  ok: $*"; }
bad() { log "  FAIL: $*"; PASS=0; }

# The scratch tree: smoke.sh and lib.sh as shipped, the scenarios below, a
# choudoufu that answers nothing and a docker that does nothing.
SB="$WORK/sb"
mkdir -p "$SB/bin" "$SB/tree/live/smoke/scenarios"
cp "$SMOKE_SRC/smoke.sh" "$SMOKE_SRC/lib.sh" "$SMOKE_SRC/VERSION" "$SMOKE_SRC/claims.json" \
   "$SMOKE_SRC/docker-compose.yml" "$SB/tree/live/smoke/"
[ ! -f "$SMOKE_SRC/ci-run.sh" ] || cp "$SMOKE_SRC/ci-run.sh" "$SB/tree/live/smoke/"
cp "$REPO_ROOT/live/floci-image" "$REPO_ROOT/live/oracle-versions.json" "$SB/tree/live/"
printf '#!/bin/sh\nexit 0\n' > "$SB/bin/choudoufu"
printf '#!/bin/sh\nexit 0\n' > "$SB/bin/docker"
chmod +x "$SB/bin/choudoufu" "$SB/bin/docker"

SC="$SB/tree/live/smoke/scenarios"
cat > "$SC/selftest-dies.sh" <<'EOF'
# selftest-dies
# selftest-verdict.sh: a command that exits 1 mid-step, under set -e
step "1. a step whose command exits 1"
false
proof "never reached"
EOF
cat > "$SC/selftest-grep.sh" <<'EOF'
# selftest-grep
# selftest-verdict.sh: a grep into evidence that matches nothing, under pipefail
step "1. a grep into evidence that matches nothing"
echo hello | grep nothing | evidence
[ -n "" ] || fail "selftest-grep" "never reached: the assertion below the grep"
EOF
cat > "$SC/selftest-exits.sh" <<'EOF'
# selftest-exits
# selftest-verdict.sh: exit 0 on the scenario's own initiative, outside any control arm
step "1. a step that exits 0 on its own"
exit 0
EOF
cat > "$SC/selftest-control.sh" <<'EOF'
# selftest-control
# selftest-verdict.sh: a control arm that prints its caught line and exits 0
step "1. the only step"
if [ "${BREAK:-0}" = "1" ]; then
  proof "caught - the break was seen"
  exit 0
fi
proof "the claim held"
EOF
cat > "$SC/selftest-runson.sh" <<'EOF'
# selftest-runson
# selftest-verdict.sh: a control arm that runs on to the end after its caught line
step "1. the only step"
if [ "${BREAK:-0}" = "1" ]; then proof "caught - the break was seen"; fi
proof "the claim held"
EOF
cat > "$SC/selftest-fails.sh" <<'EOF'
# selftest-fails
# selftest-verdict.sh: fail() by name
step "1. the only step"
fail "selftest-fails" "this is a named failure"
EOF
cat > "$SC/selftest-subfail.sh" <<'EOF'
# selftest-subfail
# selftest-verdict.sh: fail() inside a captured subshell, as a-wrong-bucket-is-refused's control does on purpose
arm() { false || fail "selftest-subfail" "[the arm] the apply SUCCEEDED where it must refuse"; }
step "1. an arm whose fail is the thing being caught"
if [ "${BREAK:-0}" = "1" ]; then
  ARM_OUT="$( (arm) 2>&1 )" && ARM_RC=0 || ARM_RC=$?
  [ "$ARM_RC" != "0" ] || fail "selftest-subfail" "the arm passed"
  proof "caught - the arm's own fail is what stopped it"
  exit 0
fi
ARM_OUT="$(arm)"
proof "never reached: the capture above propagates the arm's exit 1"
EOF
cat > "$SC/selftest-retrap.sh" <<'EOF'
# selftest-retrap
# selftest-verdict.sh: a scenario that re-traps EXIT with a teardown whose last command fails
teardown() { echo "  teardown ran"; false; }
trap 'set +e; teardown; cleanup' EXIT
step "1. a step whose command exits 3"
if [ "${BREAK:-0}" = "1" ]; then proof "caught - seen"; exit 0; fi
sh -c 'exit 3'
EOF

# run_smoke <scenario> [VAR=value ...] sets RC and OUT (a file).
run_smoke() {
  local scenario="$1"; shift
  OUT="$SB/out.txt"
  env "$@" PATH="$SB/bin:$PATH" CHOUDOUFU_BIN="$SB/bin/choudoufu" \
    bash "$SB/tree/live/smoke/smoke.sh" "$scenario" > "$OUT" 2>&1
  RC=$?
}
show_out() { sed "s/^/    ${1:-} | /" "$OUT"; }

# expect <name> <want rc> <verdict regex> [VAR=value ...]: one case. Exactly
# one verdict line, matching the regex, as the LAST line of the output, and
# the exit status the line implies.
expect() {
  local name="$1" want="$2" re="$3"; shift 3
  local n last
  log ""; log "=== $name ==="
  run_smoke "$name" "$@"
  n="$(grep -cE '^(PASS:|FAIL \[)' "$OUT")"
  last="$(tail -1 "$OUT")"
  if [ "$n" = "1" ]; then ok "exactly one verdict line"; else bad "$n verdict lines, wanted exactly 1"; fi
  if printf '%s\n' "$last" | grep -qE -- "$re"; then ok "it is the last line and reads: $last"
  else bad "the last line does not match /$re/: $last"; fi
  if [ "$RC" = "$want" ]; then ok "exit $RC"; else bad "exit $RC, wanted $want"; fi
  [ "$PASS" = "1" ] || show_out
}
# expect_nonzero is expect for a status the case can only bound.
expect_nonzero() {
  local name="$1" re="$2"; shift 2
  local n last
  log ""; log "=== $name ==="
  run_smoke "$name" "$@"
  n="$(grep -cE '^(PASS:|FAIL \[)' "$OUT")"
  last="$(tail -1 "$OUT")"
  if [ "$n" = "1" ]; then ok "exactly one verdict line"; else bad "$n verdict lines, wanted exactly 1"; fi
  if printf '%s\n' "$last" | grep -qE -- "$re"; then ok "it is the last line and reads: $last"
  else bad "the last line does not match /$re/: $last"; fi
  if [ "$RC" != "0" ]; then ok "exit $RC, non-zero"; else bad "exit 0"; fi
  [ "$PASS" = "1" ] || show_out
}

expect_nonzero selftest-dies '^FAIL \[selftest-dies\]: no verdict line - the run ended in step "1\. a step whose command exits 1" on a command that failed under set -e'
expect_nonzero selftest-grep '^FAIL \[selftest-grep\]: no verdict line - the run ended in step "1\. a grep into evidence that matches nothing" on a command that failed under set -e'
expect selftest-exits 1 '^FAIL \[selftest-exits\]: no PASS line - the run exited 0 in step "1\. a step that exits 0 on its own"'
expect selftest-exits 1 "^FAIL \[selftest-exits\]: no '-> caught' line - the control run \(BREAK\) exited 0 in step" BREAK=1
expect selftest-control 0 "^PASS: smoke scenario 'selftest-control' - every claim held"
expect selftest-control 0 "^PASS: smoke scenario 'selftest-control' - the control \(BREAK\) caught what it broke, 1 proof line" BREAK=1
expect selftest-control 1 "^FAIL \[selftest-control\]: no '-> caught' line - the control run \(BREAK_X\) exited 0" BREAK_X=1
expect selftest-runson 0 "^PASS: smoke scenario 'selftest-runson' - the control \(BREAK\) caught what it broke, 1 proof line" BREAK=1
expect selftest-runson 0 "^PASS: smoke scenario 'selftest-runson' - every claim held"
expect selftest-fails 1 '^FAIL \[selftest-fails\]: this is a named failure$'
expect selftest-subfail 0 "^PASS: smoke scenario 'selftest-subfail' - the control \(BREAK\) caught what it broke, 1 proof line" BREAK=1
# The same fail, propagating: its own FAIL line came out of the subshell and
# the run died under set -e on the capture, so the harness closes with its
# line under the arm's, naming the step. Two FAIL lines are right here.
log ""; log "=== selftest-subfail, the arm's fail propagating ==="
run_smoke selftest-subfail
n="$(grep -cE '^(PASS:|FAIL \[)' "$OUT")"
if [ "$n" = "2" ]; then ok "two FAIL lines: the arm's own and the harness's under it"; else bad "$n verdict lines, wanted 2"; fi
if grep -q '^FAIL \[selftest-subfail\]: \[the arm\] the apply SUCCEEDED' "$OUT"; then ok "the arm's own line is there"; else bad "the arm's own FAIL line is missing"; fi
if tail -1 "$OUT" | grep -qE '^FAIL \[selftest-subfail\]: no verdict line - the run ended in step "1\. an arm whose fail is the thing being caught" on a command that failed under set -e\. A FAIL line above this one, if any, came from inside that command'; then ok "the last line is the harness's, naming the step: $(tail -1 "$OUT" | cut -c1-100)..."
else bad "the last line is not the harness's verdict: $(tail -1 "$OUT")"; fi
if [ "$RC" != "0" ]; then ok "exit $RC, non-zero"; else bad "exit 0"; fi
[ "$PASS" = "1" ] || show_out
expect selftest-retrap 3 '^FAIL \[selftest-retrap\]: no verdict line - the run ended in step "1\. a step whose command exits 3" on a command that failed under set -e'
expect selftest-retrap 0 "^PASS: smoke scenario 'selftest-retrap' - the control \(BREAK\) caught what it broke" BREAK=1

# --- the CI reader ---
# ci-run.sh --check reads a log the way the workflows' steps do. A log with
# no PASS line fails by name whatever exit code the run had; a control's log
# with no caught line fails by name; --caught holds a control to one line.
log ""; log "=== ci-run.sh reads the verdict line ==="
CI="$SB/tree/live/smoke/ci-run.sh"
if [ ! -f "$CI" ]; then
  bad "no ci-run.sh in $SMOKE_SRC; the workflows have nothing to read a verdict with"
else
  L="$SB/logs"; mkdir -p "$L"
  ci_check() { # <want rc> <verdict regex or -> <args...>
    local want="$1" re="$2"; shift 2
    local out rc
    out="$(bash "$CI" --check "$@" 2>&1)"; rc=$?
    if [ "$rc" = "$want" ]; then ok "--check $* exits $rc"; else bad "--check $* exits $rc, wanted $want: $out"; fi
    if [ "$re" != "-" ]; then
      if printf '%s\n' "$out" | grep -qE -- "$re"; then ok "and says: $(printf '%s\n' "$out" | grep -E -- "$re" | head -1 | cut -c1-120)"
      else bad "and does not say /$re/: $out"; fi
    fi
  }
  printf 'a run that ended in the middle\n' > "$L/none.log"
  ci_check 1 "^FAIL \[foo\]: no PASS line - the log .*none.log has no \"PASS: smoke scenario 'foo' - \" line" foo "$L/none.log"
  printf "PASS: smoke scenario 'foo' - every claim held (smoke v0)\n" > "$L/pass.log"
  ci_check 0 - foo "$L/pass.log"
  ci_check 1 "^FAIL \[bar\]: no PASS line" bar "$L/pass.log"
  BREAK=1 ci_check 1 "^FAIL \[foo\]: no '-> caught' line - no control printed its proof line" foo "$L/pass.log"
  printf "  -> caught - the break was seen\nPASS: smoke scenario 'foo' - the control (BREAK) caught what it broke, 1 proof line(s) (smoke v0)\n" > "$L/caught.log"
  BREAK=1 ci_check 0 - foo "$L/caught.log"
  BREAK_CROSSCHECK=1 ci_check 0 - foo "$L/caught.log"
  ci_check 0 - foo "$L/caught.log" --caught 'caught - the break was seen'
  ci_check 1 "^FAIL \[foo\]: no '-> caught - some other line' line - the control never printed the proof line it exists to print" foo "$L/caught.log" --caught 'caught - some other line'
  # The run form, end to end: the throwaway that dies fails the reader too,
  # and the ordinary scenario passes it.
  out="$(PATH="$SB/bin:$PATH" CHOUDOUFU_BIN="$SB/bin/choudoufu" bash "$CI" selftest-dies "$L/run-dies.log" 2>&1)"; rc=$?
  if [ "$rc" != "0" ] && printf '%s\n' "$out" | grep -q "^FAIL \[selftest-dies\]: no PASS line"; then ok "the run form fails a scenario that died mid-step by name (exit $rc)"
  else bad "the run form let a scenario that died mid-step through (exit $rc): $out"; fi
  if grep -q '^FAIL \[selftest-dies\]: no verdict line' "$L/run-dies.log"; then ok "and the log it names carries smoke.sh's own FAIL line"
  else bad "the log $L/run-dies.log does not carry smoke.sh's FAIL line"; fi
  out="$(PATH="$SB/bin:$PATH" CHOUDOUFU_BIN="$SB/bin/choudoufu" bash "$CI" selftest-control "$L/run-ok.log" 2>&1)"; rc=$?
  if [ "$rc" = "0" ] && grep -q "^PASS: smoke scenario 'selftest-control' - every claim held" "$L/run-ok.log"; then ok "the run form passes an ordinary run on its PASS line"
  else bad "the run form failed an ordinary run (exit $rc): $out"; fi
fi

# --- the mutant: smoke.sh with its verdict removed ---
# The exit-0 case again, against a copy of smoke.sh whose cleanup no longer
# calls smoke_verdict. It must print no verdict line and exit 0 there, or the
# cases above are not measuring the verdict.
log ""; log "=== the same exit-0 case against a smoke.sh with no verdict ==="
if grep -q 'smoke_verdict$' "$SB/tree/live/smoke/smoke.sh"; then
  sed 's/^  \[ -d "\$SMOKE_WORKROOT\/stalled" \] || smoke_verdict$/  :/' "$SB/tree/live/smoke/smoke.sh" > "$SB/smoke.mutant" \
    && mv "$SB/smoke.mutant" "$SB/tree/live/smoke/smoke.sh"
  if grep -q 'smoke_verdict$' "$SB/tree/live/smoke/smoke.sh"; then
    bad "the mutant still calls smoke_verdict from cleanup; the sed did not match, so nothing here shows the checks can go red"
  else
    run_smoke selftest-exits
    n="$(grep -cE '^(PASS:|FAIL \[)' "$OUT")"
    if [ "$RC" = "0" ] && [ "$n" = "0" ]; then
      ok "the same case passes against a smoke.sh with no verdict: exit 0 and no verdict line, which is the defect"
    else
      bad "with the verdict removed the case still ended on $n verdict line(s) and exit $RC; the cases above are not measuring the verdict"
      show_out mutant
    fi
  fi
else
  bad "smoke.sh has no 'smoke_verdict' call in cleanup to remove; the mutant cannot be built, so nothing shows these checks can go red"
fi

log ""
selftest_finished=1
if [ "$PASS" = "1" ]; then
  log "PASS: selftest-verdict - every way a run can end ends on one verdict line, and the CI reader holds a job to it (#1439)"
  exit 0
fi
log "FAIL: selftest-verdict - see the FAIL lines above (#1439)"
exit 1
