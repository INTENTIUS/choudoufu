#!/usr/bin/env bash
set -uo pipefail

# live/live-cert/run.sh: the wrapper a human or CI actually invokes to run a
# live-AWS certification (issue #440), never the estate script directly. It
# adds the two guardrails #440's brief says must be independent of the
# estate script's own logic:
#
#   1. A PROCESS-LEVEL wall-clock timeout via `timeout`(1), wrapping the
#      whole estate script - not an in-script check, which a hung API call
#      inside the script could never reach. `timeout` sends TERM first (the
#      estate script's own trap tears down and verifies empty, per
#      live/live-cert/lib/live-cert.sh), then KILL after a grace period if
#      the script does not exit - the account-level AWS Budgets alarm named
#      in #440's brief is the independent backstop for exactly that last
#      case (a KILL that gives the trap no chance to run at all).
#   2. The TARGET=aws confirmation gate, enforced here too (belt and
#      suspenders with the estate script's own check) so a bare
#      `bash live/live-cert/run.sh reference-ec2-vpc -target aws` without the
#      env var refuses before `timeout` even starts a process.
#
# Usage:
#   bash live/live-cert/run.sh <estate> [-target floci|aws] [-timeout SECONDS] [-region REGION]
#
# living estates today: reference-ec2-vpc only (2026-08-29 ruling, #440).

usage() { echo "usage: $0 <estate> [-target floci|aws] [-timeout SECONDS] [-region REGION]" >&2; }

[ $# -ge 1 ] || { usage; exit 2; }
ESTATE="$1"; shift
TARGET="floci"
TIMEOUT_S="900"
REGION="us-east-1"
while [ $# -gt 0 ]; do
  case "$1" in
    -target) TARGET="$2"; shift 2 ;;
    -timeout) TIMEOUT_S="$2"; shift 2 ;;
    -region) REGION="$2"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; usage; exit 2 ;;
  esac
done

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SCRIPT="$ROOT/live/live-cert/$ESTATE.sh"
[ -x "$SCRIPT" ] || { echo "no live-cert script for estate $ESTATE ($SCRIPT)" >&2; exit 2; }

if [ "$TARGET" = "aws" ] && [ "${LIVECERT_I_UNDERSTAND_THIS_SPENDS_REAL_MONEY:-}" != "yes" ]; then
  echo "refusing: -target aws needs LIVECERT_I_UNDERSTAND_THIS_SPENDS_REAL_MONEY=yes in the environment - nothing has been started" >&2
  echo "this run's ceiling ($TIMEOUT_S s process timeout) is one of TWO independent enforcements #440's brief requires; confirm the account also carries an AWS Budgets alarm before running for real" >&2
  exit 2
fi

command -v timeout >/dev/null 2>&1 || { echo "timeout(1) is not on PATH - the process-level ceiling cannot be enforced, refusing to run" >&2; exit 2; }

echo "=== live-cert: $ESTATE target=$TARGET region=$REGION, process ceiling ${TIMEOUT_S}s (TERM, then KILL after 30s grace) ==="
TARGET="$TARGET" REGION="$REGION" timeout --signal=TERM --kill-after=30 "$TIMEOUT_S" bash "$SCRIPT"
rc=$?
if [ "$rc" -eq 124 ]; then
  echo "=== live-cert: $ESTATE hit the ${TIMEOUT_S}s process ceiling (timeout's own exit 124) - the estate script's own trap should have torn down on the TERM timeout sent it; check its output above for the TEARDOWN section's verdict ===" >&2
elif [ "$rc" -eq 137 ]; then
  echo "=== live-cert: $ESTATE was KILLed after the grace period - its own trap did NOT get to run. This is exactly the case #440's brief's account-level AWS Budgets alarm exists for; verify the account by hand (or the sweep script) before trusting anything is torn down ===" >&2
fi
exit "$rc"
