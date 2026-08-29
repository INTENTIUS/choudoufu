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
# "Creation complete" line - proven empirically while building this script,
# 2026-08-29, to land reliably inside the ~29s a 5-resource apply takes
# against floci, with the instance's own ~10s creation window giving a wide
# margin), sends the estate script itself a real SIGTERM (simulating an
# operator Ctrl-C or a CI job cancellation, not an internal self-signal), and
# then verifies emptiness ITSELF, independently, against the same floci
# endpoint, rather than trusting the estate script's own "VERIFIED EMPTY"
# self-report - the same "verify by listing, not by trusting the destroy's
# exit code" discipline #440's brief asks the harness to hold to, held here
# a second time against the harness itself.
#
# Usage: bash live/live-cert/selftest-kill.sh
# Needs docker, the AWS CLI, and terraform on PATH - same as the harness.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
RUN_ID="selftest-kill-$(date +%s)-$$"
FLOCI_PORT="${FLOCI_PORT:-4817}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"
REGION="us-east-1"
LOG="$WORK/harness.log"

log() { printf '%s\n' "$*"; }
pass=1

cleanup() {
  # This driver's own belt-and-suspenders: if the assertions below somehow
  # leave the harness process or its container alive, clean up rather than
  # leaving a second thing depending on a trap firing correctly.
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

log "=== selftest-kill: waiting for genuine apply progress (stock terraform's own \"Creation complete\" line) ==="
APPLY_LOG="$WORK/harness-work/cold_deploy_apply.out"
synced=0
for i in $(seq 1 300); do
  if [ -f "$APPLY_LOG" ] && grep -q "Creation complete" "$APPLY_LOG" 2>/dev/null; then
    synced=1
    log "  synced after ${i}00ms: at least one resource confirmed created, apply is genuinely in flight"
    break
  fi
  if ! kill -0 "$HARNESS_PID" 2>/dev/null; then
    log "FAIL: the harness process exited before apply made any progress we could detect - cannot prove a mid-apply kill this way"
    pass=0
    break
  fi
  sleep 0.1
done
if [ "$synced" != "1" ]; then
  log "FAIL: never observed apply progress within 30s"
  pass=0
fi

if [ "$pass" = "1" ]; then
  # A little more headroom past the sync point so the kill lands mid-flight
  # (more resources in progress) rather than the instant after the first
  # one - closer to a realistic interrupt, not just the earliest possible one.
  sleep 1
  log "=== selftest-kill: sending SIGTERM to the harness itself (pid $HARNESS_PID) - simulating an operator interrupt, not an internal self-signal ==="
  kill -TERM "$HARNESS_PID"
  wait "$HARNESS_PID"
  HARNESS_RC=$?
  log "  harness exited $HARNESS_RC"
  [ "$HARNESS_RC" -eq 130 ] || { log "FAIL: expected exit 130 (on_signal's own exit after handling TERM), got $HARNESS_RC"; pass=0; }
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
  N="$(aws --endpoint-url "$ENDPOINT" --region "$REGION" resourcegroupstaggingapi get-resources \
    --tag-filters "Key=tofu-cert-run,Values=$RUN_ID" --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo unknown)"
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
