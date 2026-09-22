#!/usr/bin/env bash
set -uo pipefail

# live/e2e/selftest-floci-sweep.sh: proof for issue #1312.
#
# A crossing script SIGKILLed mid-run leaves its floci container running,
# and from outside that container is identical to a concurrent run's. So
# live/e2e/lib/gauntlet.sh's gauntlet_floci_start labels every container
# with its owner (pid and that pid's start time) and gauntlet_sweep_leaked_floci
# removes only what the labels prove is dead. This runs the SHIPPED sweeper,
# sourced from the tree under test, against real throwaway containers in
# real docker, because the property is about what docker does to a
# container and a stub would measure the stub:
#
#   dead-owner-is-removed      labelled with a pid that has exited: removed
#   reused-pid-is-removed      labelled with this shell's pid and a start
#                              time that is not this shell's: removed
#   live-owner-is-kept         labelled with this shell's pid and start: kept
#   unlabelled-is-untouched    no ownership labels at all: kept, with the
#                              by-hand command in its line
#   stopped-is-removed         a container that exited: removed, after its
#                              postmortem was printed
#   other-estate-is-out-of-scope  a dead-owner container of ANOTHER estate
#                              is untouched by a sweep scoped to this one
#   entry-point                scripts/floci-sweep.sh <estate> does the same
#   the-check-can-fail         the first case is re-run against a copy of
#                              the library whose liveness check is gone, and
#                              must fail there
#
# Every container it starts is named choudoufu-leaktest-<pid>-<case> and
# carries a choudoufu.selftest=<pid> label; the EXIT trap removes exactly
# those, by label, and nothing else on the machine. Every sweep it runs is
# scoped to its own estate (leaktest-<pid>), never the whole machine, so
# other people's containers are never in its population: a concurrent
# estate run on the same host is safe from this test.
#
# The verdict is the per-case lines below, not the exit code. Every case
# prints "ok:" per property it confirmed and "FAIL:" per property it did
# not, and the last line is PASS or FAIL. A run that stops part way prints
# neither PASS nor its final FAIL line; the EXIT trap says so and exits 1
# (bash 3.2 hands the trap a status of 0 for a `set -u` abort, so the
# status is saved on the trap's first line and a finished flag is checked
# - the #1419 shape).
#
# Usage:
#   bash live/e2e/selftest-floci-sweep.sh
#   GAUNTLET_LIB=<path to a gauntlet.sh> bash live/e2e/selftest-floci-sweep.sh
#     runs the same cases against another library, which is how the red is
#     shown.
#   bash live/e2e/selftest-floci-sweep.sh --only <case>   one case, by name.
#
# Needs docker running and the pinned image (live/floci-image) present;
# live/flocisweep_test.go runs it and is the wiring into CI's floci tier.

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$HERE/../.." && pwd)"
GAUNTLET_LIB="${GAUNTLET_LIB:-$HERE/lib/gauntlet.sh}"

ONLY=""
while [ $# -gt 0 ]; do
  case "$1" in
    --only) ONLY="${2:-}"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

command -v docker >/dev/null 2>&1 || { echo "FAIL: docker is not on PATH"; exit 1; }
docker info >/dev/null 2>&1 || { echo "FAIL: docker is not running"; exit 1; }
IMAGE="${FLOCI_IMAGE:-$(cat "$REPO_ROOT/live/floci-image")}"
docker image inspect "$IMAGE" >/dev/null 2>&1 || { echo "FAIL: image $IMAGE is not present locally and this selftest does not pull"; exit 1; }

# shellcheck source=lib/gauntlet.sh
source "$GAUNTLET_LIB"

ESTATE="leaktest-$$"
SELFTEST_LABEL="choudoufu.selftest=$$"
WORK="$(mktemp -d)"
selftest_finished=0
# shellcheck disable=SC2154 # selftest_rc is assigned on the trap's first line
trap 'selftest_rc=$?
      docker ps -aq --filter "label=$SELFTEST_LABEL" | xargs docker rm -f >/dev/null 2>&1
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
wanted() { [ -z "$ONLY" ] || [ "$ONLY" = "$1" ]; }
has() { # <file> <needle> <what>
  if grep -qF -- "$2" "$1"; then ok "$3"; else bad "$3 (no line contains: $2)"; fi
}
hasnt() {
  if grep -qF -- "$2" "$1"; then bad "$3 (a line contains: $2)"; else ok "$3"; fi
}
present() { # <name> -> exit 0 if docker knows the container
  [ -n "$(docker ps -aq --filter "name=^${1}\$" 2>/dev/null)" ]
}
running() {
  [ "$(docker inspect --format '{{.State.Status}}' "$1" 2>/dev/null)" = "running" ]
}

# start <case> [labels...]: a throwaway container for one case, named after
# it, with whatever ownership labels the case wants. No port is published:
# the sweeper never looks at ports, and a port is the one thing this could
# collide with another run over.
start() {
  local name="choudoufu-${ESTATE}-$1"; shift
  local args=()
  while [ $# -gt 0 ]; do args+=(--label "$1"); shift; done
  docker run -d --name "$name" --label "$SELFTEST_LABEL" ${args[@]+"${args[@]}"} "$IMAGE" >/dev/null \
    || { bad "could not start $name"; return 1; }
  printf '%s\n' "$name"
}

# A pid that has already exited: the subshell prints its own pid and is gone
# by the time the substitution returns. If the kernel hands that pid out
# again before the sweep runs, the start time will not match the label and
# the verdict is "reused" - removed either way, which is what the case asserts.
DEAD_PID="$(sh -c 'echo $$')"
MY_STARTED="$(gauntlet_pid_started $$)"
[ -n "$MY_STARTED" ] || { bad "gauntlet_pid_started could not read this shell's own start time, so nothing below can be labelled"; }
STALE_STARTED="Thu Jan 1 00:00:00 1970"

L_ESTATE="$GAUNTLET_FLOCI_LABEL_ESTATE=$ESTATE"

# ── one sweep over six containers ───────────────────────────────────────
# The cases share a single sweep because that is the real shape: a run's
# pre-start sweep meets everything its estate left behind at once, and a
# sweeper that decides each container correctly on its own but not in
# company would pass six separate sweeps and fail the real one.
if [ -z "$ONLY" ] || wanted dead-owner-is-removed || wanted reused-pid-is-removed || wanted live-owner-is-kept || wanted unlabelled-is-untouched || wanted stopped-is-removed || wanted other-estate-is-out-of-scope; then
  log ""
  log "=== sweep of estate $ESTATE ==="
  # The numeric suffix is what makes the unlabelled one match the name
  # filter for unlabelled containers, choudoufu-<estate>-<digits>.
  DEAD="$(start dead "$L_ESTATE" "$GAUNTLET_FLOCI_LABEL_PID=$DEAD_PID" "$GAUNTLET_FLOCI_LABEL_STARTED=$STALE_STARTED")"
  REUSED="$(start reused "$L_ESTATE" "$GAUNTLET_FLOCI_LABEL_PID=$$" "$GAUNTLET_FLOCI_LABEL_STARTED=$STALE_STARTED")"
  LIVE="$(start live "$L_ESTATE" "$GAUNTLET_FLOCI_LABEL_PID=$$" "$GAUNTLET_FLOCI_LABEL_STARTED=$MY_STARTED")"
  UNLABELLED="$(start 99999)"
  STOPPED="$(start stopped "$L_ESTATE" "$GAUNTLET_FLOCI_LABEL_PID=$DEAD_PID" "$GAUNTLET_FLOCI_LABEL_STARTED=$STALE_STARTED")"
  OTHER="$(start other "$GAUNTLET_FLOCI_LABEL_ESTATE=other-$ESTATE" "$GAUNTLET_FLOCI_LABEL_PID=$DEAD_PID" "$GAUNTLET_FLOCI_LABEL_STARTED=$STALE_STARTED")"
  docker kill "$STOPPED" >/dev/null 2>&1
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    running "$STOPPED" || break
    sleep 1
  done
  running "$STOPPED" && bad "$STOPPED is still running after docker kill, so the stopped case measures nothing"
  for c in "$DEAD" "$REUSED" "$LIVE" "$UNLABELLED" "$OTHER"; do
    running "$c" || bad "$c is not running before the sweep, so its case measures nothing"
  done

  gauntlet_sweep_leaked_floci "$ESTATE" > "$WORK/sweep.out" 2>&1
  sed 's/^/        | /' "$WORK/sweep.out"

  if wanted dead-owner-is-removed; then
    log "  dead-owner-is-removed"
    if present "$DEAD"; then bad "$DEAD (owner pid $DEAD_PID is dead) is still there"; else ok "$DEAD (owner pid $DEAD_PID is dead) is gone"; fi
    has "$WORK/sweep.out" "FLOCI-SWEEP $DEAD: removed - owner pid $DEAD_PID is" "the sweeper said it removed it and why"
  fi
  if wanted reused-pid-is-removed; then
    log "  reused-pid-is-removed"
    if present "$REUSED"; then bad "$REUSED (owner pid $$ alive, start time wrong) is still there"; else ok "$REUSED (owner pid $$ alive, start time wrong) is gone"; fi
    has "$WORK/sweep.out" "FLOCI-SWEEP $REUSED: removed - owner pid $$ is alive but started $MY_STARTED, not $STALE_STARTED" "the sweeper said the pid was reused"
  fi
  if wanted live-owner-is-kept; then
    log "  live-owner-is-kept"
    if running "$LIVE"; then ok "$LIVE (owner pid $$ alive with the labelled start) is still running"; else bad "$LIVE (owner pid $$ alive with the labelled start) was removed or stopped"; fi
    has "$WORK/sweep.out" "FLOCI-SWEEP $LIVE: kept - owner pid $$ is alive (started $MY_STARTED)" "the sweeper said it kept it and why"
  fi
  if wanted unlabelled-is-untouched; then
    log "  unlabelled-is-untouched"
    if running "$UNLABELLED"; then ok "$UNLABELLED (no ownership labels) is still running"; else bad "$UNLABELLED (no ownership labels) was removed or stopped"; fi
    has "$WORK/sweep.out" "FLOCI-SWEEP $UNLABELLED: kept - no ownership labels" "the sweeper listed it as unowned"
    has "$WORK/sweep.out" "docker rm -f $UNLABELLED" "its line carries the by-hand command"
  fi
  if wanted stopped-is-removed; then
    log "  stopped-is-removed"
    if present "$STOPPED"; then bad "$STOPPED (exited) is still there"; else ok "$STOPPED (exited) is gone"; fi
    has "$WORK/sweep.out" "FLOCI-SWEEP $STOPPED: removing - status=exited" "the sweeper said it was removing a stopped container"
    has "$WORK/sweep.out" "FLOCI-POSTMORTEM $STOPPED: DIED BEFORE TEARDOWN" "its postmortem was printed before it went (#1299's evidence, read rather than buried)"
  fi
  if wanted other-estate-is-out-of-scope; then
    log "  other-estate-is-out-of-scope"
    if running "$OTHER"; then ok "$OTHER (estate other-$ESTATE, owner dead) is untouched by a sweep of $ESTATE"; else bad "$OTHER (estate other-$ESTATE) was removed by a sweep of $ESTATE"; fi
    hasnt "$WORK/sweep.out" "$OTHER" "the sweep of $ESTATE did not even mention it"
    # And it IS a leak by its own estate's sweep, or the case proves only
    # that the filter is narrow, not that it is a filter.
    gauntlet_sweep_leaked_floci "other-$ESTATE" > "$WORK/other.out" 2>&1
    if present "$OTHER"; then bad "$OTHER is still there after a sweep of its own estate"; else ok "$OTHER is gone after a sweep of its own estate"; fi
  fi
fi

# ── the entry point ─────────────────────────────────────────────────────
if wanted entry-point; then
  log ""
  log "=== entry-point ==="
  EP="$(start entrypoint "$L_ESTATE" "$GAUNTLET_FLOCI_LABEL_PID=$DEAD_PID" "$GAUNTLET_FLOCI_LABEL_STARTED=$STALE_STARTED")"
  KEPT="$(start entrykept "$L_ESTATE" "$GAUNTLET_FLOCI_LABEL_PID=$$" "$GAUNTLET_FLOCI_LABEL_STARTED=$MY_STARTED")"
  bash "$REPO_ROOT/scripts/floci-sweep.sh" "$ESTATE" > "$WORK/ep.out" 2>&1
  rc=$?
  sed 's/^/        | /' "$WORK/ep.out"
  [ "$rc" = 0 ] && ok "scripts/floci-sweep.sh $ESTATE exited 0" || bad "scripts/floci-sweep.sh $ESTATE exited $rc"
  if present "$EP"; then bad "$EP (owner dead) survived the entry point"; else ok "$EP (owner dead) is gone"; fi
  if running "$KEPT"; then ok "$KEPT (owner alive) survived the entry point"; else bad "$KEPT (owner alive) was removed by the entry point"; fi
  has "$WORK/ep.out" "FLOCI-SWEEP $EP: removed - owner pid $DEAD_PID is" "the entry point printed the sweeper's decision"
fi

# ── this check can fail ─────────────────────────────────────────────────
# The cases above pass against the shipped library. That is worth nothing
# unless they fail against a library without the ownership check, so the
# first case is re-run against a copy whose liveness check has been taken
# out: gauntlet_pid_started answers with the label's own start time, which
# is what "trust the label, never ask the machine" looks like. If the copy
# passes, the check is scenery.
if wanted "" && [ -z "$ONLY" ]; then
  log ""
  log "=== the-check-can-fail ==="
  MUT="$WORK/mutant-gauntlet.sh"
  python3 - "$GAUNTLET_LIB" "$MUT" <<'PYEOF'
import sys
src = open(sys.argv[1], encoding="utf-8", errors="surrogateescape").read()
old = 'now="$(gauntlet_pid_started "$pid")"'
assert src.count(old) == 1, "gauntlet_floci_ownership has no liveness lookup to remove; this mutation proves nothing"
open(sys.argv[2], "w", encoding="utf-8", errors="surrogateescape").write(src.replace(old, 'now="$started"'))
PYEOF
  if [ $? -ne 0 ]; then
    bad "could not build the mutant library, so nothing here shows the check can fail"
  else
    if GAUNTLET_LIB="$MUT" bash "$0" --only dead-owner-is-removed > "$WORK/mutant.out" 2>&1; then
      bad "dead-owner-is-removed PASSED against a library that never asks whether the owner is alive, so it does not measure what it claims to"
      sed 's/^/        | /' "$WORK/mutant.out" | head -30
    else
      ok "dead-owner-is-removed fails against a library that never asks whether the owner is alive"
      grep -m3 "FAIL:" "$WORK/mutant.out" | sed 's/^/        mutant | /'
    fi
  fi
fi

log ""
selftest_finished=1
if [ "$PASS" = "1" ]; then
  log "PASS: selftest-floci-sweep - a leaked floci container is one whose owner is dead, and only those were removed (#1312)"
  exit 0
fi
log "FAIL: selftest-floci-sweep - see the FAIL lines above (#1312)"
exit 1
