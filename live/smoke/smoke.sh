#!/usr/bin/env bash
# The smoke entrypoint (issue #713): one scenario per invocation, verdict
# lines over exit codes, exit 0 only when every claim held.
#
#   bash live/smoke/smoke.sh greenfield
#   bash live/smoke/smoke.sh import
#   bash live/smoke/smoke.sh full
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

cleanup() { stack_down; rm -rf "$SMOKE_WORKROOT"; }
trap cleanup EXIT

resolve_choudoufu
banner "$SCENARIO"

# shellcheck source=/dev/null
. "$HERE/scenarios/$SCENARIO.sh"

instrument_summary
echo
echo "PASS: smoke scenario '$SCENARIO' - every claim held (smoke v$SMOKE_VERSION)"
