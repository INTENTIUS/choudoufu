#!/usr/bin/env bash
# The smoke entrypoint (issue #713): one scenario per invocation, verdict
# lines over exit codes, exit 0 only when every claim held.
#
# Every run ends on exactly one verdict line, printed here and never by a
# scenario (#1439): `PASS: smoke scenario '<name>' - ...` on exit 0, or a
# `FAIL [<name>]: ...` naming what broke. A run that exits 0 without one is
# itself a FAIL, and a control run (BREAK=1 or any BREAK_<NAME>=1) is a FAIL
# unless at least one control printed its `-> caught` proof line. The
# convention every scenario follows: a control arm ends with
# `proof "caught ..."`, and then either exits 0 or runs on into the steps it
# shares with the main arm; both reach the same closing line.
#
#   bash live/smoke/smoke.sh greenfield
#   bash live/smoke/smoke.sh import
#   bash live/smoke/smoke.sh full
#   bash live/smoke/smoke.sh k8s-greenfield   (a kind cluster, no emulator)
#
# Knobs (all optional):
#   CHOUDOUFU_VERSION=v0.8.0   run a pinned release instead of source
#   CHOUDOUFU_BIN=/path        run an explicit binary
#   FLOCI_IMAGE=...            override the pinned emulator image
#   FLOCI_PORT=4650            host port for the emulator
#   SMOKE_INSTRUMENT=1         capture TF_LOG=debug per call and summarize
#                              requests/retries (the terralith counters)
#   BREAK=1                    corrupt one expected fact mid-scenario and
#                              require the scenario to CATCH it - proof the
#                              assertions are load-bearing, never scenery
#
# Bounds, for the k8s-* scenarios (issue #1457). Each fails the scenario by
# name, with the step it was in, and the cluster is still deleted:
#   SMOKE_TIMEOUT_SECS=600     seconds before a k8s-* scenario with no verdict
#                              is killed (default max(600, 2 x claims.json
#                              minutes))
#   CHDF_TIMEOUT_SECS=300      one choudoufu call made behind a failing or
#                              rewriting admission chain
#   KC_REQUEST_TIMEOUT=30s     one kubectl request
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"

list_scenarios() {
  echo "scenarios (run one with: just smoke <name>):"
  for s in "$HERE"/scenarios/*.sh; do
    b="$(basename "$s" .sh)"
    printf "  %-12s %s\n" "$b" "$(sed -n '2s/^# //p' "$s")"
  done
}

SCENARIO="${1:-}"
if [ -z "$SCENARIO" ]; then
  # No argument is a request for the list, not a mistake.
  list_scenarios
  exit 0
fi
if [ ! -f "$HERE/scenarios/$SCENARIO.sh" ]; then
  echo "no scenario named '$SCENARIO'" >&2
  list_scenarios >&2
  exit 2
fi

SMOKE_WORKROOT="$(mktemp -d)"
mkdir -p "$SMOKE_WORKROOT/logs"
export SMOKE_WORKROOT

# shellcheck source=lib.sh
. "$HERE/lib.sh"

# The scenario bound (#1457). Nothing between `just smoke <name>` and the API
# server had a time limit, so one stalled call took the whole CI job timeout
# and printed nothing. smoke_stall in lib.sh does the reporting and the
# killing. What is here is the part only the entrypoint can do: keep the
# stderr this run started with open as fd 9, so a bound that fires inside a
# `$(... 2>&1)` still prints where an operator reads; exit on the TERM the
# stall sends; and delete the cluster on the way out as on any other exit.
SMOKE_SCENARIO="$SCENARIO"
exec 9>&2
SMOKE_ERR_FD=9
WATCHDOG_PID=""

# scenario_bound_secs prints the scenario's bound: SMOKE_TIMEOUT_SECS, or
# twice the claim's measured minutes with a floor of ten. CI runs these at up
# to 1.8 times the claims.json figure (kind on a shared runner), so twice is
# the smallest multiple that does not fail a healthy run, and the floor
# covers the two-minute claims, where cluster_up alone is most of the time.
scenario_bound_secs() {
  if [ -n "${SMOKE_TIMEOUT_SECS:-}" ]; then echo "$SMOKE_TIMEOUT_SECS"; return 0; fi
  python3 - "$HERE/claims.json" "$SCENARIO" <<'PY'
import json, sys
minutes = [c.get("minutes", 0) for c in json.load(open(sys.argv[1]))["claims"]
           if c.get("scenario", "").endswith("/" + sys.argv[2] + ".sh")]
print(max(600, 2 * 60 * max(minutes + [0])))
PY
}

# The verdict (#1439). Four files under SMOKE_WORKROOT say how the run ended,
# and cleanup reads them because it is the one place every exit passes:
#   verdict  fail() printed its FAIL line; the exit status is its own
#   caught   one line per `proof "caught ..."` a control printed
#   done     the scenario returned and the main path reached its end
#   exit     the status the main shell's own `exit` was called with
# Exit status alone cannot be read there: seven scenarios re-trap EXIT to
# run a teardown before cleanup, and by the time cleanup runs `$?` is the
# teardown's. So `exit` is shadowed below, in this shell only, to record its
# argument first. A death under `set -e` calls no exit and leaves no record,
# which is exactly how cleanup tells it apart from a scenario's own `exit 0`.
exit() {
  local rc="${1:-$?}"
  # Only the scenario shell's exit is the run's; the timer subshell exits 0
  # every time it is stopped. The portable spelling of "am I that shell".
  if [ "$(exec sh -c 'echo $PPID')" = "$$" ]; then printf '%s\n' "$rc" > "$SMOKE_WORKROOT/exit"; fi
  builtin exit "$rc"
}

# smoke_controls prints the control variables this run was started with:
# BREAK, and any BREAK_<NAME>, that are set to 1. Empty for an ordinary run.
smoke_controls() {
  local v out=""
  [ "${BREAK:-0}" = "1" ] && out="BREAK"
  for v in $(compgen -v BREAK_ 2>/dev/null); do
    [ "${!v}" = "1" ] && out="$out${out:+ }$v"
  done
  echo "$out"
}
CONTROLS="$(smoke_controls)"

# smoke_verdict prints the closing line from the files above and sets
# VERDICT_RC to the status the run must exit with, or leaves it empty when
# the status the shell is already exiting with is the right one: fail's own
# exit 1, or a death under set -e. An EXIT trap that calls no exit keeps
# that status.
VERDICT_RC=""
smoke_verdict() {
  local step ncaught rc
  step="$(cat "$SMOKE_WORKROOT/step" 2>/dev/null || echo '?')"
  ncaught="$(grep -c '' "$SMOKE_WORKROOT/caught" 2>/dev/null || echo 0)"
  rc="$(cat "$SMOKE_WORKROOT/exit" 2>/dev/null || echo '')"
  if [ -f "$SMOKE_WORKROOT/verdict" ]; then return 0; fi
  if [ -f "$SMOKE_WORKROOT/done" ] || [ "$rc" = "0" ]; then
    if [ -n "$CONTROLS" ] && [ "$ncaught" = "0" ]; then
      echo "FAIL [$SCENARIO]: no '-> caught' line - the control run ($CONTROLS) exited 0 in step \"$step\" without any control printing its proof line, so nothing shows the break was caught. Read that step's output above." >&2
      VERDICT_RC=1; return 0
    fi
    if [ -z "$CONTROLS" ] && [ ! -f "$SMOKE_WORKROOT/done" ]; then
      echo "FAIL [$SCENARIO]: no PASS line - the run exited 0 in step \"$step\" before reaching its end. Outside a control arm a scenario ends on the harness's PASS line or a FAIL naming what broke, never an exit of its own. Read that step's output above." >&2
      VERDICT_RC=1; return 0
    fi
    echo
    if [ -n "$CONTROLS" ]; then
      echo "PASS: smoke scenario '$SCENARIO' - the control ($CONTROLS) caught what it broke, $ncaught proof line(s) (smoke v$SMOKE_VERSION)"
    else
      echo "PASS: smoke scenario '$SCENARIO' - every claim held (smoke v$SMOKE_VERSION)"
    fi
    VERDICT_RC=0; return 0
  fi
  if [ -z "$rc" ]; then
    echo "FAIL [$SCENARIO]: no verdict line - the run ended in step \"$step\" on a command that failed under set -e. A FAIL line above this one, if any, came from inside that command; otherwise nothing named what broke. Read that step's output above." >&2
  else
    echo "FAIL [$SCENARIO]: no verdict line - the run ended with exit $rc in step \"$step\". Read that step's output above." >&2
  fi
  return 0
}

cleanup() {
  # errexit off: this is a trap body, and the first command that fails in one
  # ends it with every later step skipped and nothing printed (#1378).
  set +e
  [ -z "$WATCHDOG_PID" ] || smoke_timer_stop "$WATCHDOG_PID"
  # The verdict goes out before the teardown, so it sits under the step it
  # is about and a slow cluster delete does not hold it back. A stall has
  # already printed its FAIL line from the timer, prints nothing here and
  # exits 124 as before (#1457). `builtin exit` below: the shadowing exit
  # above would write to a work directory this trap has just removed.
  [ -d "$SMOKE_WORKROOT/stalled" ] || smoke_verdict
  stack_down; cluster_down
  if [ -d "$SMOKE_WORKROOT/stalled" ]; then rm -rf "$SMOKE_WORKROOT"; builtin exit 124; fi
  rm -rf "$SMOKE_WORKROOT"
  [ -z "$VERDICT_RC" ] || builtin exit "$VERDICT_RC"
}
trap cleanup EXIT
# 124 is what timeout(1) exits with. A TERM from anywhere else keeps its 143.
trap 'if [ -d "$SMOKE_WORKROOT/stalled" ]; then exit 124; else exit 143; fi' TERM

resolve_choudoufu
banner "$SCENARIO"

# Only the k8s-* scenarios are bounded. The real-AWS scenarios tear down in
# EXIT traps of their own that run before cleanup stops the watchdog, and a
# bound firing in the middle of one would kill the calls that delete what
# the run created.
case "$SCENARIO" in
  k8s-*)
    BOUND="$(scenario_bound_secs)"
    # The verdict's two halves, in variables so the call below stays on one
    # line: selftest-bounds.sh builds its mutant by replacing that line.
    STALL_BEFORE="stalled"
    STALL_AFTER="and killed after ${BOUND}s. Raise the bound with SMOKE_TIMEOUT_SECS=<seconds>."
    smoke_timer "$BOUND" "$STALL_BEFORE" "$STALL_AFTER"
    WATCHDOG_PID="$SMOKE_TIMER_PID"
    ;;
esac

# shellcheck source=/dev/null
. "$HERE/scenarios/$SCENARIO.sh"

instrument_summary
# The scenario returned. The closing line is cleanup's, from this mark: an
# ordinary run's PASS, or a control run's PASS if a control printed its
# proof line and its FAIL if none did.
: > "$SMOKE_WORKROOT/done"
exit 0
