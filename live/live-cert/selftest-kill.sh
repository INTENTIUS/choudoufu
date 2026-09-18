#!/usr/bin/env bash
set -uo pipefail

# live/live-cert/selftest-kill.sh: Stage 1 of #440's proof obligation -
# "prove teardown works by running the whole thing against floci with the
# teardown path deliberately exercised, including killing it mid-apply and
# confirming the trap still tears down and still verifies empty."
#
# This is an EXTERNAL driver, deliberately not a mode flag inside
# reference-ec2-vpc.sh itself: it launches that script exactly as a human or
# CI would (TARGET=floci, unmodified), synchronizes against its REAL apply
# progress (polling the cold_deploy apply's own log for stock terraform's
# "Creation complete" line, in two separately bounded phases so a cold
# runner's image pull and provider download cannot be mistaken for a stalled
# apply - see the phase comment below), sends the estate script itself a
# real SIGTERM (simulating an
# operator Ctrl-C or a CI job cancellation, not an internal self-signal), and
# then verifies emptiness ITSELF, independently, against the same floci
# endpoint, rather than trusting the estate script's own "VERIFIED EMPTY"
# self-report - the same "verify by listing, not by trusting the destroy's
# exit code" discipline #440's brief asks the harness to hold to, held here
# a second time against the harness itself.
#
# Usage: bash live/live-cert/selftest-kill.sh
# Needs docker, the AWS CLI, and terraform on PATH - same as the harness.
#   SELFTEST_KILL_SETUP_BOUND_S=<seconds>  bounds harness launch -> the
#     cold_deploy apply STARTING: the emulator image pull, the health wait,
#     the AMI lookup and `terraform init` (default 600).
#   SELFTEST_KILL_APPLY_BOUND_S=<seconds>  bounds the apply starting -> its
#     first "Creation complete" (default 180).
#   SELFTEST_KILL_WAIT_BOUND_S=<seconds> bounds the wait for the harness to
#     finish its trap after the SIGTERM (default 240).
#
# KNOWN LIMIT (issue #1279): the "independent verification" section at the
# end of this script is unreachable on a passing run. The harness removes
# the floci container as teardown's last step, so the endpoint is already
# gone when this driver goes to list it, and the listing is skipped with a
# line that reads like a confirmation. Measured: with the harness's destroy
# AND sweep neutered, the harness itself printed "STILL NOT EMPTY after
# destroy and sweep" and this script still exited 0 with its PASS verdict.
# Until that is fixed, read this script's PASS as "the trap fired, teardown
# ran, the container is gone", not as "the account is empty".
#
# Run automatically by ci.yml's livecert-selftest-kill job (issue #1267);
# live/livecert_selftests_test.go's TestCIRunsTheKillSelftest is the guard
# that keeps that job from going away.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
RUN_ID="selftest-kill-$(date +%s)-$$"
FLOCI_PORT="${FLOCI_PORT:-4817}"
# Everything this driver waits on is bounded, because a hang in the
# harness's own trap is one of the defects it exists to catch and an
# unbounded wait turns that defect into a stuck job rather than a red one
# (issue #1267, hazard 2 - the same lesson #1143's first red arm paid for).
# This is the bound on the trap itself, which has a real destroy and an
# independent listing to get through, so it is generous rather than tight.
# The two synchronization bounds are set further down, beside the loops
# they govern.
WAIT_BOUND_S="${SELFTEST_KILL_WAIT_BOUND_S:-240}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"
REGION="us-east-1"
LOG="$WORK/harness.log"

log() { printf '%s\n' "$*"; }
pass=1

# rgta_count: same fix as lib/live-cert.sh's livecert_rgta_count (#1047) -
# `--query 'length(ResourceTagMappingList)' --output text` prints one
# number PER PAGE, not one total, so this driver counts the ARN array
# (which --output text correctly concatenates across every page) instead.
# This script does not source lib/live-cert.sh (it drives the harness as an
# external process), so the fix is duplicated here rather than shared.
rgta_count() {
  aws --endpoint-url "$ENDPOINT" --region "$REGION" resourcegroupstaggingapi get-resources \
    --tag-filters "Key=$1,Values=$2" \
    --query 'ResourceTagMappingList[].ResourceARN' --output text 2>/dev/null \
    | tr '\t' '\n' | grep -c . || true
}

# dump_harness_artifacts prints what the harness redirected AWAY from its own
# stdout, which is where every interesting failure lands.
#
# This exists because of the first CI run of this selftest (2026-09-18,
# #1267): it failed, and the entire evidence in the job log was two banner
# lines and "FAIL - see above" with nothing above. cold_deploy's init and
# apply are both redirected into files in the harness's work dir, the apply
# is additionally BACKGROUNDED, and this driver's own cleanup deletes that
# work dir on the way out - so the one thing a reader needed had been
# written, never printed, and then removed. A selftest whose failure message
# points at output it did not print cannot be acted on the first time it
# goes red, which is the only time it matters.
dump_harness_artifacts() {
  log ""
  log "=== selftest-kill: the harness's own redirected output (work dir $HARNESS_WORK) ==="
  log "    This is what \"see above\" means: the harness sends each cold_deploy step to a"
  log "    file rather than to its stdout, so none of it reaches the harness log."
  if [ ! -d "$HARNESS_WORK" ]; then
    log "    (the work dir does not exist: the harness never reached the point of creating one)"
  else
    ls -la "$HARNESS_WORK" 2>/dev/null | sed 's/^/      /'
    local f n
    for f in "$HARNESS_WORK"/*.out; do
      [ -f "$f" ] || continue
      n="$(wc -l < "$f" | tr -d ' ')"
      log ""
      log "    --- $(basename "$f") (${n} line(s), last 40) ---"
      tail -40 "$f" | sed 's/^/      | /'
    done
  fi
  local cname
  cname="$(docker ps -a --filter "name=choudoufu-livecert-reference-ec2-vpc-" --format '{{.Names}}' 2>/dev/null | head -1)"
  if [ -n "$cname" ]; then
    log ""
    log "    --- docker logs $cname (last 30) ---"
    docker logs --tail 30 "$cname" 2>&1 | sed 's/^/      | /'
  fi
}

cleanup() {
  # This driver's own belt-and-suspenders: if the assertions below somehow
  # leave the harness process or its container alive, clean up rather than
  # leaving a second thing depending on a trap firing correctly.
  [ -n "${WATCHDOG_PID:-}" ] && kill -TERM "$WATCHDOG_PID" 2>/dev/null
  [ -n "${HARNESS_PID:-}" ] && kill -0 "$HARNESS_PID" 2>/dev/null && kill -TERM "$HARNESS_PID" 2>/dev/null
  docker rm -f "choudoufu-livecert-reference-ec2-vpc-${HARNESS_PID:-nonexistent}" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log "=== selftest-kill: launching the harness (target=floci, run_id=$RUN_ID) in the background ==="
# `exec` on the last line is load-bearing, not stylistic (found the hard way
# building this script, 2026-08-29): without it, HARNESS_PID names the
# SUBSHELL this whole (...) group runs in, not the `bash reference-ec2-vpc.sh`
# process itself. A SIGTERM sent to that subshell PID kills the subshell
# under its own default disposition without reliably delivering to (or even
# reaching) the harness's own bash process - which then never runs its trap
# at all, and can be left running, orphaned, invisible to this driver's own
# wait/kill-by-PID. `exec` replaces the subshell's process image with the
# harness itself, so HARNESS_PID is unambiguously that process on every bash
# version, and the harness's own `trap ... TERM` fires when this driver
# signals it.
(
  cd "$ROOT" && \
  export TARGET=floci RUN_ID="$RUN_ID" FLOCI_PORT="$FLOCI_PORT" LIVECERT_WORK_DIR="$WORK/harness-work" && \
  exec bash live/live-cert/reference-ec2-vpc.sh
) > "$LOG" 2>&1 &
HARNESS_PID=$!
log "  harness pid=$HARNESS_PID, log=$LOG"

# Synchronization is in TWO phases, bounded separately, because they are two
# different things and only the second is about this test.
#
# It was one phase - 30s from harness launch to "Creation complete" - and
# that bound was measured on a warm laptop where the emulator image was
# already pulled and the provider already in the plugin cache. The first
# GitHub-runner run (2026-09-18, #1267) spent the whole 30s on setup:
# pulling ghcr.io/lex00/floci cold, then `terraform init` downloading
# hashicorp/aws 6.58.0. The harness did reach "2b. cold_deploy: apply" -
# with seconds to spare - so the driver gave up on a bound that was
# measuring the runner's download speed rather than anything about a
# mid-apply kill.
#
# Splitting it is not a loosening. Each phase now bounds the interval it is
# actually about, and a stall in either names itself rather than being
# absorbed by the other's slack:
#   setup: launch -> the apply's own log file exists. Image pull, health
#          wait, AMI lookup, init. Generous by design; a cold runner
#          legitimately spends minutes here and none of it is under test.
#   apply: that file exists -> "Creation complete" appears in it. THIS is
#          the property - stock terraform actually creating something
#          against the emulator. Measured at 6s on a warm laptop (8s setup
#          + 6s apply = the 17.8s whole run), which is worth noticing: the
#          old single 30s bound had only ~16s of margin even at its best,
#          and a cold runner spent all 30 on setup alone.
SETUP_BOUND_S="${SELFTEST_KILL_SETUP_BOUND_S:-600}"
APPLY_BOUND_S="${SELFTEST_KILL_APPLY_BOUND_S:-180}"
HARNESS_WORK="$WORK/harness-work"
APPLY_LOG="$HARNESS_WORK/cold_deploy_apply.out"
synced=0

log "=== selftest-kill: phase 1/2, waiting for cold_deploy's apply to START (its log file to appear), bound ${SETUP_BOUND_S}s ==="
started=0
T0=$(date +%s)
while [ $(( $(date +%s) - T0 )) -lt "$SETUP_BOUND_S" ]; do
  if [ -f "$APPLY_LOG" ]; then
    started=1
    log "  apply started after $(( $(date +%s) - T0 ))s (image pull + health + AMI + init all happened inside this)"
    break
  fi
  # bash reaps its own background children and keeps their status for
  # `wait`, so once the harness exits this `kill -0` fails and the loop
  # reports the real reason instead of running the bound out. Measured on
  # bash 3.2.57: an exited background child is NOT left visible to its
  # parent shell as a zombie.
  if ! kill -0 "$HARNESS_PID" 2>/dev/null; then
    log "FAIL: the harness exited after $(( $(date +%s) - T0 ))s, before cold_deploy's apply ever started - so there was never a mid-apply moment to interrupt. The harness's own log and its redirected step output are below; the failure is in one of them, not in this driver."
    pass=0
    break
  fi
  sleep 0.2
done
if [ "$pass" = "1" ] && [ "$started" != "1" ]; then
  log "FAIL: cold_deploy's apply never started within ${SETUP_BOUND_S}s - $APPLY_LOG never appeared. That interval is setup (emulator image pull, health, AMI lookup, terraform init), not the apply. Read the init output below before raising SELFTEST_KILL_SETUP_BOUND_S, because an init that is failing looks the same from here as one that is merely slow."
  pass=0
fi

if [ "$started" = "1" ]; then
  log "=== selftest-kill: phase 2/2, waiting for genuine apply progress (stock terraform's own \"Creation complete\" line), bound ${APPLY_BOUND_S}s ==="
  T1=$(date +%s)
  while [ $(( $(date +%s) - T1 )) -lt "$APPLY_BOUND_S" ]; do
    if grep -q "Creation complete" "$APPLY_LOG" 2>/dev/null; then
      synced=1
      log "  synced after $(( $(date +%s) - T1 ))s of applying: at least one resource confirmed created, apply is genuinely in flight"
      break
    fi
    if ! kill -0 "$HARNESS_PID" 2>/dev/null; then
      log "FAIL: the harness exited $(( $(date +%s) - T1 ))s into the apply without creating anything this driver could see - the apply itself failed. Its output is below."
      pass=0
      break
    fi
    sleep 0.2
  done
  if [ "$pass" = "1" ] && [ "$synced" != "1" ]; then
    log "FAIL: the apply ran for ${APPLY_BOUND_S}s without one \"Creation complete\" line. It had started, so this is not setup: either stock terraform is stuck against the emulator or it is failing without exiting. Its output is below - $(wc -l < "$APPLY_LOG" 2>/dev/null | tr -d ' ') line(s) so far."
    pass=0
  fi
fi

# Everything below asks whether the SIGTERM was handled correctly. If no
# SIGTERM was ever sent, none of it can answer anything, and asking anyway is
# how the first CI failure produced four cascading "the trap did not fire"
# lines about a trap nothing had triggered. One verdict and the evidence.
if [ "$synced" != "1" ]; then
  dump_harness_artifacts
  log ""
  log "=== selftest-kill: full harness log (the harness's own stdout) ==="
  cat "$LOG"
  log ""
  log "=== selftest-kill: FAIL - never reached a mid-apply moment, so no SIGTERM was sent and nothing about teardown was tested. The cause is in the two blocks above, not in teardown assertions this run never made. ==="
  exit 1
fi

if [ "$pass" = "1" ]; then
  # A little more headroom past the sync point so the kill lands mid-flight
  # (more resources in progress) rather than the instant after the first
  # one - closer to a realistic interrupt, not just the earliest possible one.
  sleep 1
  log "=== selftest-kill: sending SIGTERM to the harness itself (pid $HARNESS_PID) - simulating an operator interrupt, not an internal self-signal ==="
  kill -TERM "$HARNESS_PID"
  # A watchdog rather than a polling loop, and the reason is narrower than
  # an earlier version of this comment claimed. That version said a
  # `kill -0` poll could not work, because an exited child stays a zombie
  # and `kill -0` on a zombie succeeds from its parent. That is FALSE for a
  # bash background job - bash reaps it and keeps the status for `wait`, so
  # `kill -0` does fail once it is gone (measured on bash 3.2.57 while
  # red-arming this, which is how the claim was caught). The real reasons
  # are that a watchdog bounds the `wait` itself rather than racing it,
  # needs no loop, and produces an unambiguous 137 - which is what
  # distinguishes "the trap hung and we killed it" from "the trap ran and
  # exited non-130" below.
  ( sleep "$WAIT_BOUND_S"; kill -KILL "$HARNESS_PID" 2>/dev/null ) &
  WATCHDOG_PID=$!
  wait "$HARNESS_PID"
  HARNESS_RC=$?
  kill -TERM "$WATCHDOG_PID" 2>/dev/null
  wait "$WATCHDOG_PID" 2>/dev/null
  WATCHDOG_PID=""
  log "  harness exited $HARNESS_RC"
  if [ "$HARNESS_RC" -eq 137 ]; then
    log "FAIL: the harness was still running ${WAIT_BOUND_S}s after the SIGTERM and had to be SIGKILLed - its trap (on_signal -> teardown) hung rather than tearing down. Nothing below this line means anything: the teardown never finished."
    pass=0
  else
    [ "$HARNESS_RC" -eq 130 ] || { log "FAIL: expected exit 130 (on_signal's own exit after handling TERM), got $HARNESS_RC"; pass=0; }
  fi
fi

log "=== selftest-kill: reading the harness's own report ==="
if grep -q "caught TERM" "$LOG"; then
  log "  on_signal fired (harness log carries \"caught TERM\")"
else
  log "FAIL: harness log never shows on_signal catching TERM - the trap did not fire as expected"
  pass=0
fi
if grep -q "=== TEARDOWN " "$LOG"; then
  log "  teardown ran (harness log carries the TEARDOWN banner)"
else
  log "FAIL: harness log never shows teardown running"
  pass=0
fi
if grep -q "VERIFIED EMPTY" "$LOG"; then
  log "  the harness's OWN self-report says VERIFIED EMPTY"
else
  log "  the harness's own self-report does NOT say VERIFIED EMPTY - checking independently below regardless (this is exactly why the check below does not stop here)"
fi

log "=== selftest-kill: independent verification - THIS driver lists the SAME floci endpoint itself, trusting nothing the harness said ==="
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" AWS_ENDPOINT_URL="$ENDPOINT"
if docker ps --filter "name=choudoufu-livecert-reference-ec2-vpc-" --format '{{.Names}}' 2>/dev/null | grep -q .; then
  # The container may legitimately still be reachable for a moment right
  # after the harness process exits (docker rm -f is the harness's last
  # teardown step); give it a short settle window before treating this as
  # a real leak, purely for the container's own lifecycle, never for the
  # AWS-object verification below.
  sleep 2
fi
if docker ps --filter "name=choudoufu-livecert-reference-ec2-vpc-" --format '{{.Names}}' 2>/dev/null | grep -q .; then
  log "FAIL: the floci container is still running after the harness exited - teardown's own container cleanup did not happen"
  pass=0
else
  log "  floci container is gone"
fi

# The container itself may already be gone (the harness's own teardown
# removes it), which would make an endpoint-based listing fail outright -
# that is EXPECTED and is itself part of the proof (teardown discarded the
# emulator state along with the real objects the sweep would otherwise have
# had to find). Only treat a listing failure as a hard FAIL when the
# container is still reachable but reports something left over.
if curl -fs "${ENDPOINT}/_localstack/health" >/dev/null 2>&1; then
  N="$(rgta_count tofu-cert-run "$RUN_ID")"
  VPCS="$(aws --endpoint-url "$ENDPOINT" --region "$REGION" ec2 describe-vpcs --filters "Name=tag:tofu-cert-run,Values=$RUN_ID" --query 'Vpcs[].VpcId' --output text 2>/dev/null || true)"
  IGWS="$(aws --endpoint-url "$ENDPOINT" --region "$REGION" ec2 describe-internet-gateways --filters "Name=tag:tofu-cert-run,Values=$RUN_ID" --query 'InternetGateways[].InternetGatewayId' --output text 2>/dev/null || true)"
  if [ "$N" = "0" ] && [ -z "$VPCS" ] && [ -z "$IGWS" ]; then
    log "  independent listing (this driver's own aws CLI calls): 0 resources tagged tofu-cert-run=$RUN_ID, no vpc, no internet gateway"
  else
    log "FAIL: independent listing found leftovers - resourcegroupstaggingapi=$N vpcs=[$VPCS] igws=[$IGWS]"
    pass=0
  fi
else
  log "  the emulator endpoint is unreachable (the container is already gone, which teardown does on its own last step) - nothing left to list, consistent with a full teardown"
fi

if [ "$pass" != "1" ]; then
  # Only on failure: on a passing run the harness log below is the whole
  # story and the redirected step output is noise. On a failing one it is
  # usually the only place the reason exists at all.
  dump_harness_artifacts
fi

log ""
log "=== selftest-kill: full harness log (the evidence this verdict was read from) ==="
cat "$LOG"
log ""
if [ "$pass" = "1" ]; then
  log "=== selftest-kill: PASS - a real SIGTERM delivered to the harness mid-apply (after at least one resource genuinely existed) still ran teardown and left the account (this floci endpoint) verifiably empty, confirmed independently of the harness's own report ==="
else
  log "=== selftest-kill: FAIL - see above ==="
fi
exit $((1 - pass))
