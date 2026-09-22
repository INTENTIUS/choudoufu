#!/usr/bin/env bash
# The smoke entrypoint (issue #713): one scenario per invocation, verdict
# lines over exit codes, exit 0 only when every claim held.
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

cleanup() {
  # errexit off: this is a trap body, and the first command that fails in one
  # ends it with every later step skipped and nothing printed (#1378).
  set +e
  [ -z "$WATCHDOG_PID" ] || smoke_timer_stop "$WATCHDOG_PID"
  stack_down; cluster_down
  if [ -d "$SMOKE_WORKROOT/stalled" ]; then rm -rf "$SMOKE_WORKROOT"; exit 124; fi
  rm -rf "$SMOKE_WORKROOT"
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
echo
echo "PASS: smoke scenario '$SCENARIO' - every claim held (smoke v$SMOKE_VERSION)"
