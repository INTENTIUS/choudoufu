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
# themselves: on an unmodified (RED) checkout that would let the harness
# reach real AMI resolution and other real AWS API calls, which is exactly
# what this guard exists to prevent. This harness instead reproduces their
# TARGET=aws case-block body inline, in the same order both scripts carry
# it (see either script's own `case "$TARGET" in ... aws) ... esac` block).

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

out="$({
  livecert_require_maintainer_allow
  # Mirrors reference-ec2-vpc.sh's and terralith-scale.sh's TARGET=aws
  # case-block body, verbatim and in the same order.
  if [ "${LIVECERT_I_UNDERSTAND_THIS_SPENDS_REAL_MONEY:-}" != "yes" ]; then
    echo "refusing: TARGET=aws needs LIVECERT_I_UNDERSTAND_THIS_SPENDS_REAL_MONEY=yes - nothing has been created" >&2
    exit 2
  fi
  echo "ALLOWED"
} 2>&1)"
rc=$?

echo "$out"

fail=0
if printf '%s\n' "$out" | grep -qx "ALLOWED"; then
  echo "SELFTEST maintainer-guard: RED - the harness reached ALLOWED with no allow file at $allow_file (rc=$rc)" >&2
  fail=1
fi
if [ "$rc" -eq 0 ]; then
  echo "SELFTEST maintainer-guard: RED - exit code was 0 with no allow file at $allow_file" >&2
  fail=1
fi
if ! printf '%s\n' "$out" | grep -qF "refusing: $allow_file - the allow file does not exist"; then
  echo "SELFTEST maintainer-guard: RED - expected refusal text naming $allow_file was not printed (got: $out)" >&2
  fail=1
fi
if ! printf '%s\n' "$out" | grep -qF "just allow-heavy-runs"; then
  echo "SELFTEST maintainer-guard: RED - refusal did not name the \`just allow-heavy-runs\` recipe (got: $out)" >&2
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  exit 1
fi
echo "SELFTEST maintainer-guard: GREEN - refused with exit $rc, naming $allow_file and the allow-heavy-runs recipe, before reaching ALLOWED"
