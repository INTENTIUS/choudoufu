#!/usr/bin/env bash
# live/live-cert/selftest-maintainer-guard.sh: proves the maintainer-run-
# guard's shell half - lib/live-cert.sh's livecert_require_maintainer_allow -
# actually blocks a TARGET=aws run with no allow file, rather than merely
# existing (HANDOFF.md: "a check that cannot fail is not a check").
#
# RED on main (before this guard landed): lib/live-cert.sh defines no such
# function, so calling it prints "command not found" on stderr and returns
# 127 - and because neither reference-ec2-vpc.sh nor terralith-scale.sh sets
# `set -e`, that does not stop the script. Execution falls through to the
# NEXT check (LIVECERT_I_UNDERSTAND_THIS_SPENDS_REAL_MONEY), which this
# selftest sets to "yes" on purpose (see below), so the harness reaches
# ALLOWED with no allow file ever having existed.
#
# GREEN after: livecert_require_maintainer_allow exists, finds no allow
# file under the temp HOME this selftest points at, prints the refusal
# text naming that file and the `just allow-heavy-runs` recipe, and calls
# `exit 2` - which ends the command substitution below on the spot, so
# neither the env-var check nor "ALLOWED" ever runs.
#
# HOME is pointed at a fresh, empty temp dir for the whole run, so this
# never reads or depends on a real maintainer allow file.
# LIVECERT_I_UNDERSTAND_THIS_SPENDS_REAL_MONEY=yes is set on purpose: the
# point of this selftest is the NEW guard, isolated from the pre-existing
# env-var check it sits beside (that check already had its own three
# self-authorizations the night of 2026-09-11 - see CLAUDE.md's
# maintainer-run-guard rule - which is exactly why it is not trusted alone).
#
# Deliberately does not invoke reference-ec2-vpc.sh or terralith-scale.sh
# themselves for case A below: on an unmodified (RED) checkout that would
# let the harness reach real AMI resolution and other real AWS API calls,
# which is exactly what this guard exists to prevent. Case A instead
# reproduces the TARGET=aws case-block body inline, in the same order both
# scripts carry it (see either script's own `case "$TARGET" in ... aws)
# ... esac` block) - now guarded by TEARDOWN_ONLY_DIR the same way the real
# scripts are (issue #1032's teardown-only dispatch landed after this guard
# was written, and the two conflicted: selftest-hold-resume.sh's case 3
# caught the merged result refusing a teardown-only run for want of an
# allow file it never needed).
#
# Case B proves the other half of the same fix: with TEARDOWN_ONLY_DIR set
# (mirroring terralith-scale.sh's own `if [ -z "$TEARDOWN_ONLY_DIR" ]`
# guard around the livecert_require_maintainer_allow call), the harness
# reaches ALLOWED with no allow file at all - teardown-only only destroys
# what an earlier run already created and verifies the account empty, so
# refusing it would strand a held, billing estate live instead of tearing
# it down.

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LIB="$ROOT/live-cert/lib"

TMPHOME="$(mktemp -d)"
trap 'rm -rf "$TMPHOME"' EXIT

export HOME="$TMPHOME"
unset GITHUB_ACTIONS
unset CI
export LIVECERT_I_UNDERSTAND_THIS_SPENDS_REAL_MONEY=yes

# shellcheck source=live/live-cert/lib/live-cert.sh
source "$LIB/live-cert.sh"

allow_file="$TMPHOME/.config/choudoufu/allow-heavy-runs"
fail=0

# run_case mirrors terralith-scale.sh's/reference-ec2-vpc.sh's TARGET=aws
# case-block body, verbatim and in the same order, including the
# TEARDOWN_ONLY_DIR guard around livecert_require_maintainer_allow. Prints
# the body's own stdout+stderr and returns its exit code.
run_case() {
  local TEARDOWN_ONLY_DIR="$1"
  (
    if [ -z "$TEARDOWN_ONLY_DIR" ]; then
      livecert_require_maintainer_allow
    fi
    if [ "${LIVECERT_I_UNDERSTAND_THIS_SPENDS_REAL_MONEY:-}" != "yes" ]; then
      echo "refusing: TARGET=aws needs LIVECERT_I_UNDERSTAND_THIS_SPENDS_REAL_MONEY=yes - nothing has been created" >&2
      exit 2
    fi
    echo "ALLOWED"
  ) 2>&1
}

########################################################################
# Case A: a full run (TEARDOWN_ONLY_DIR empty) still refuses with no
# allow file - the guard this selftest has always proven.
########################################################################
echo "=== case A: full run (no TEARDOWN_ONLY_DIR) refuses with no allow file ==="
outA="$(run_case "")"
rcA=$?
echo "$outA"

if printf '%s\n' "$outA" | grep -qx "ALLOWED"; then
  echo "SELFTEST maintainer-guard: RED - case A reached ALLOWED with no allow file at $allow_file (rc=$rcA)" >&2
  fail=1
fi
if [ "$rcA" -eq 0 ]; then
  echo "SELFTEST maintainer-guard: RED - case A exit code was 0 with no allow file at $allow_file" >&2
  fail=1
fi
if ! printf '%s\n' "$outA" | grep -qF "refusing: $allow_file - the allow file does not exist"; then
  echo "SELFTEST maintainer-guard: RED - case A: expected refusal text naming $allow_file was not printed (got: $outA)" >&2
  fail=1
fi
if ! printf '%s\n' "$outA" | grep -qF "just allow-heavy-runs"; then
  echo "SELFTEST maintainer-guard: RED - case A: refusal did not name the \`just allow-heavy-runs\` recipe (got: $outA)" >&2
  fail=1
fi

########################################################################
# Case B: teardown-only (TEARDOWN_ONLY_DIR set) proceeds to ALLOWED with
# the SAME empty HOME/no-allow-file setup - it must never call
# livecert_require_maintainer_allow at all. This is the case that was RED
# on merged main (before this fix): terralith-scale.sh called the guard
# unconditionally, so a teardown-only run with no allow file refused
# exactly like case A, leaving a held estate stranded live instead of torn
# down.
########################################################################
echo ""
echo "=== case B: teardown-only (TEARDOWN_ONLY_DIR set) proceeds without an allow file ==="
outB="$(run_case "/tmp/selftest-maintainer-guard-fake-workdir")"
rcB=$?
echo "$outB"

if [ "$rcB" -ne 0 ]; then
  echo "SELFTEST maintainer-guard: FAIL - case B (teardown-only) exited $rcB, want 0 - it must not require the allow file at $allow_file" >&2
  fail=1
fi
if ! printf '%s\n' "$outB" | grep -qx "ALLOWED"; then
  echo "SELFTEST maintainer-guard: FAIL - case B (teardown-only) did not reach ALLOWED (got: $outB)" >&2
  fail=1
fi
if printf '%s\n' "$outB" | grep -qF "refusing: $allow_file"; then
  echo "SELFTEST maintainer-guard: FAIL - case B (teardown-only) refused citing the allow file - teardown-only never needs it (got: $outB)" >&2
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  exit 1
fi
echo ""
echo "SELFTEST maintainer-guard: GREEN - case A refused with exit $rcA (naming $allow_file and the allow-heavy-runs recipe) before reaching ALLOWED; case B (teardown-only) reached ALLOWED with exit $rcB and no allow file at all"
