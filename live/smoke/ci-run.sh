#!/usr/bin/env bash
# live/smoke/ci-run.sh: one smoke run in CI, passed on its verdict line and
# not on its exit code (issue #1439).
#
#   bash live/smoke/ci-run.sh <scenario> <log> [--caught '<text>']
#   bash live/smoke/ci-run.sh --check <scenario> <log> [--caught '<text>']
#
# The first form runs `smoke.sh <scenario>` with its output appended to <log>
# as it goes, copied into the job log by a `tail -f` rather than a `| tee`
# (#1457: a process the scenario orphans on purpose would hold a pipe open
# and keep the step alive after smoke.sh had exited; it holds a file open
# harmlessly). Then it reads the log back, which is the --check form on its
# own, for the Go test and for a log in hand:
#
#   - `PASS: smoke scenario '<scenario>' - ` must be there, whatever
#     smoke.sh exited. CLAUDE.md's rule is to read verdict lines and never
#     exit codes, and the case this file was written for was a run that
#     ended with no verdict line at all.
#   - when BREAK=1 or any BREAK_<NAME>=1 is in the environment, or --caught
#     is given, a `-> caught` proof line must be there too: a control that
#     exits 0 has only proved something if it got as far as the corruption.
#     With --caught it must carry that text, which is how claim 31's
#     BREAK_CROSSCHECK control is held to the one line it exists to print.
#
# Every failure is a FAIL line naming what was missing and the log to read.
# The exit code is non-zero if smoke.sh's was or a line was missing.
set -uo pipefail

usage() { echo "usage: $0 [--check] <scenario> <log> [--caught '<text>']" >&2; exit 2; }

CHECK_ONLY=0
if [ "${1:-}" = "--check" ]; then CHECK_ONLY=1; shift; fi
SCENARIO="${1:-}"; LOG="${2:-}"
[ -n "$SCENARIO" ] && [ -n "$LOG" ] || usage
shift 2
CAUGHT=""
while [ $# -gt 0 ]; do
  case "$1" in
    --caught) CAUGHT="${2:-}"; [ -n "$CAUGHT" ] || usage; shift 2 ;;
    *) usage ;;
  esac
done

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# is_control: the same reading of the environment smoke.sh makes.
is_control() {
  [ "${BREAK:-0}" = "1" ] && return 0
  local v
  for v in $(compgen -v BREAK_ 2>/dev/null); do [ "${!v}" = "1" ] && return 0; done
  return 1
}

rc=0
if [ "$CHECK_ONLY" = "0" ]; then
  mkdir -p "$(dirname "$LOG")"; : > "$LOG"
  tail -f "$LOG" & tail_pid=$!
  bash "$HERE/smoke.sh" "$SCENARIO" >> "$LOG" 2>&1 || rc=$?
  sleep 2; kill "$tail_pid" 2>/dev/null || true
fi

[ -f "$LOG" ] || { echo "FAIL [$SCENARIO]: no log at $LOG to read a verdict from" >&2; exit 1; }
exited="exited $rc"; [ "$CHECK_ONLY" = "0" ] || exited="exit code not read in --check mode"

missing=0
if ! grep -q -- "^PASS: smoke scenario '$SCENARIO' - " "$LOG"; then
  echo "FAIL [$SCENARIO]: no PASS line - the log $LOG has no \"PASS: smoke scenario '$SCENARIO' - \" line (smoke.sh $exited). Its last FAIL line, if any, names the step; read the log from there." >&2
  grep -E '^FAIL \[' "$LOG" | tail -1 | sed 's/^/    /' >&2
  missing=1
fi
if [ -n "$CAUGHT" ]; then
  if ! grep -qF -- "-> $CAUGHT" "$LOG"; then
    echo "FAIL [$SCENARIO]: no '-> $CAUGHT' line - the control never printed the proof line it exists to print, so it never reached the corruption (smoke.sh $exited). Read the log at $LOG." >&2
    missing=1
  fi
elif is_control; then
  if ! grep -q -- '^  -> caught' "$LOG"; then
    echo "FAIL [$SCENARIO]: no '-> caught' line - no control printed its proof line, so nothing shows the break was caught (smoke.sh $exited). Read the log at $LOG." >&2
    missing=1
  fi
fi

if [ "$missing" = "1" ]; then exit 1; fi
[ "$rc" = "0" ] || { echo "FAIL [$SCENARIO]: smoke.sh exited $rc while its log $LOG carries a PASS line; the two must agree" >&2; exit "$rc"; }
exit 0
