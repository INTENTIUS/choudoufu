#!/usr/bin/env bash
# scripts/floci-sweep.sh [estate]: remove the floci containers crossing
# scripts have leaked, and say what was decided about every one (#1312).
#
# A crossing script SIGKILLed mid-run leaves its floci container running,
# because an EXIT trap cannot fire on SIGKILL and `--rm` removes a container
# when the container exits, not when the script does. The containers are
# named choudoufu-<estate>-<pid> and each holds a published port until an
# unrelated run of the same estate fails its health check on it.
#
# This is the whole-machine entry point to live/e2e/lib/gauntlet.sh's
# gauntlet_sweep_leaked_floci, which every crossing script also runs for its
# own estate before it starts a container. It removes:
#
#   - every stopped choudoufu-* container nobody alive owns, after printing
#     the postmortem #1299 kept it for (FLOCI-POSTMORTEM lines);
#   - every running one whose ownership labels name a pid that is dead, or
#     alive with a different start time (the pid was reused).
#
# It never removes a running container with no ownership labels - a run
# from before the labels, or something started by hand - because from
# outside that container is indistinguishable from a concurrent run's. It
# lists those with the command to run by hand once you have looked.
#
# With an estate name it looks only at that estate's containers, the same
# scope a run uses. Read-only preview: bash scripts/pickup.sh prints the
# same verdicts without acting on them.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=live/e2e/lib/gauntlet.sh
source "$ROOT/live/e2e/lib/gauntlet.sh"

command -v docker >/dev/null 2>&1 || { echo "floci-sweep: docker is not on PATH" >&2; exit 1; }
docker info >/dev/null 2>&1 || { echo "floci-sweep: docker is not running" >&2; exit 1; }

scope="${1:-}"
if [ -n "$scope" ]; then
  echo "floci-sweep: containers of estate ${scope} (choudoufu-${scope}-<pid>)"
else
  echo "floci-sweep: every choudoufu-* container on this machine"
fi
out="$(gauntlet_sweep_leaked_floci "$scope")"
if [ -z "$out" ]; then
  echo "floci-sweep: nothing to decide - no such containers"
  exit 0
fi
printf '%s\n' "$out"
