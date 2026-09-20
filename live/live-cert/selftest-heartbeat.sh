#!/usr/bin/env bash
set -uo pipefail

# live/live-cert/selftest-heartbeat.sh: issue #1324's third proposal, proven
# without a live-cert run.
#
# The defect is that the run log is written at stage boundaries only. On the
# scale-128 run #1324 was filed from, cold_deploy's apply took 5,633s and the
# file did not grow once in 1h34m, so a healthy stage and a wedged one were
# byte-identical from outside. The fix is one line per interval naming the
# stage and its elapsed seconds.
#
# This drives the heartbeat block EXTRACTED from live/live-cert/terralith-
# scale.sh rather than running that script, which no selftest may do (#1380:
# it deploys real, paid infrastructure, and a guard inside it that fails to
# fire does not print a red line, it lets the run continue into a cold
# deploy). The extracted text ends where the block ends, so no mutation of
# this file can reach a deploy.
#
# What it proves:
#   1. the log GROWS during a stage that emits no stage boundary
#   2. every line names the stage and a rising elapsed_s
#   3. heartbeat_stop ends it, and nothing arrives after a stage
#   4. it does not outlive the script, even when the script is SIGKILLed
#      past its own traps - a background child holding the stdout pipe open
#      makes the Go side sit out its whole WaitDelay after the script exits
#   5. LIVECERT_HEARTBEAT_S=0 turns it off
#   6. a heartbeat line is not a protocol line, so ParseProtocol ignores it
#
# Usage: bash live/live-cert/selftest-heartbeat.sh
# Needs nothing but bash: no docker, no AWS CLI, no terraform, no go build.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SRC_ARG="${TERRALITH_SCALE_SH:-$ROOT/live/live-cert/terralith-scale.sh}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

log() { printf '%s\n' "$*"; }
pass=1

# The interval every case below runs at. One second keeps this selftest in
# the seconds the roster's bound allows; the production default is 60.
IVL=1

# ── extract the block ───────────────────────────────────────────────────
BLOCK="$WORK/heartbeat.sh"
sed -n '/^# >>> heartbeat block$/,/^# <<< heartbeat block$/p' "$SRC_ARG" > "$BLOCK"
if [ ! -s "$BLOCK" ]; then
  log "FAIL: no heartbeat block found in $SRC_ARG between '# >>> heartbeat block' and '# <<< heartbeat block'."
  log "      If the markers were renamed, rename them here too; if the heartbeat was removed, #1324's third"
  log "      proposal is gone and a long stage is silent again."
  log "=== selftest-heartbeat: FAIL - see above ==="
  exit 1
fi
for want in heartbeat_start heartbeat_stop LIVECERT_HEARTBEAT_S; do
  grep -q "$want" "$BLOCK" || { log "FAIL: the extracted block does not mention $want"; pass=0; }
done
log "=== 0. extracted $(wc -l < "$BLOCK" | tr -d ' ') line(s) of heartbeat block from $SRC_ARG ==="

# ── 1 and 2. the log grows during a stage with no stage boundary ────────
log ""
log "=== 1. a stage that emits no boundary: does the log grow? ==="
cat > "$WORK/case1.sh" <<EOF
set -uo pipefail
source "$BLOCK"
LIVECERT_HEARTBEAT_S=$IVL
heartbeat_start cold_deploy
# The fake long stage. It prints nothing at all - that is the whole point:
# on the real run this is a 5,633-second terraform apply whose output goes
# to a file, not to this log.
sleep $((IVL * 3 + 1))
heartbeat_stop
EOF
bash "$WORK/case1.sh" > "$WORK/case1.out" 2>&1
N1="$(grep -c '^HEARTBEAT ' "$WORK/case1.out" || true)"
if [ "${N1:-0}" -ge 2 ]; then
  log "  $N1 heartbeat line(s) during a stage that printed nothing of its own:"
  sed 's/^/    /' "$WORK/case1.out"
else
  log "FAIL: $N1 heartbeat line(s) in ${IVL}s intervals over a $((IVL * 3 + 1))s stage - the log did not grow, which is the defect"
  sed 's/^/    /' "$WORK/case1.out"
  pass=0
fi

# Every line names the stage, and elapsed_s rises. A heartbeat that printed
# the same number every time would satisfy a line count and still tell a
# reader nothing about whether the process is moving.
BADSTAGE="$(grep '^HEARTBEAT ' "$WORK/case1.out" | grep -vc 'stage=cold_deploy' || true)"
if [ "${BADSTAGE:-0}" = "0" ]; then
  log "  every line names stage=cold_deploy"
else
  log "FAIL: $BADSTAGE heartbeat line(s) do not name the stage"
  pass=0
fi
ELAPSED="$(grep -o 'elapsed_s=[0-9]*' "$WORK/case1.out" | cut -d= -f2 | tr '\n' ' ')"
PREV=-1; RISING=1
for e in $ELAPSED; do
  [ "$e" -gt "$PREV" ] || RISING=0
  PREV="$e"
done
if [ "$RISING" = "1" ] && [ -n "$ELAPSED" ]; then
  log "  elapsed_s rises: $ELAPSED"
else
  log "FAIL: elapsed_s did not rise monotonically: $ELAPSED"
  pass=0
fi

# ── 3. heartbeat_stop ends it ───────────────────────────────────────────
log ""
log "=== 2. heartbeat_stop: nothing arrives after the stage ends ==="
cat > "$WORK/case2.sh" <<EOF
set -uo pipefail
source "$BLOCK"
LIVECERT_HEARTBEAT_S=$IVL
heartbeat_start migrate
sleep $((IVL * 2 + 1))
heartbeat_stop
echo "STAGE_ENDED"
# Two more intervals of quiet. Any HEARTBEAT line after STAGE_ENDED is a
# heartbeat that outlived its stage.
sleep $((IVL * 2 + 1))
echo "DONE"
EOF
bash "$WORK/case2.sh" > "$WORK/case2.out" 2>&1
AFTER="$(sed -n '/^STAGE_ENDED$/,$p' "$WORK/case2.out" | grep -c '^HEARTBEAT ' || true)"
if [ "${AFTER:-0}" = "0" ]; then
  log "  no heartbeat lines after STAGE_ENDED ($(grep -c '^HEARTBEAT ' "$WORK/case2.out" || true) before it)"
else
  log "FAIL: $AFTER heartbeat line(s) arrived after heartbeat_stop:"
  sed 's/^/    /' "$WORK/case2.out"
  pass=0
fi

# ── 4. it does not outlive the script ───────────────────────────────────
#
# The SIGKILL case, which is the one a trap cannot cover. The script is
# killed past every trap it has, so heartbeat_stop never runs and the only
# thing that can end the subshell is its own `kill -0 "$parent"` check.
log ""
log "=== 3. SIGKILL the script: the heartbeat subshell exits on its own ==="
# The script SIGKILLs itself rather than being killed from here. Same
# signal, same unrunnable traps, and the selftest's own shell never has a
# killed background job to report - a "Killed: 9" line in the middle of a
# passing selftest reads like a failure to whoever opens the CI log.
cat > "$WORK/case3-inner.sh" <<EOF
set -uo pipefail
source "$BLOCK"
LIVECERT_HEARTBEAT_S=$IVL
heartbeat_start test_plan
echo "\$HEARTBEAT_PID" > "$WORK/case3.hbpid"
sleep 60
EOF
# The process that gets SIGKILLed is a GRANDchild of this selftest, killed
# and reaped by its own parent. This shell never waits on it, so bash's
# "Killed: 9" job notice goes into case3.out instead of landing in the
# middle of a passing selftest's output, where it reads like a failure.
cat > "$WORK/case3.sh" <<EOF
set -uo pipefail
bash "$WORK/case3-inner.sh" &
INNER=\$!
sleep 1
kill -KILL "\$INNER" 2>/dev/null
wait "\$INNER" 2>/dev/null
exit 0
EOF
bash "$WORK/case3.sh" > "$WORK/case3.out" 2>&1
WAITED=0
while [ ! -s "$WORK/case3.hbpid" ] && [ "$WAITED" -lt 100 ]; do sleep 0.1; WAITED=$((WAITED + 1)); done
if [ ! -s "$WORK/case3.hbpid" ]; then
  log "FAIL: the heartbeat subshell never started within 10s"
  pass=0
else
  HBPID="$(cat "$WORK/case3.hbpid")"
  # Bounded, for #1267 hazard 2: an unbounded wait here would turn a
  # heartbeat that never exits into a stuck job rather than a red one.
  DEADLINE=$((IVL * 2 + 3)); GONE=0
  for _ in $(seq 1 $((DEADLINE * 10))); do
    if ! kill -0 "$HBPID" 2>/dev/null; then GONE=1; break; fi
    sleep 0.1
  done
  if [ "$GONE" = "1" ]; then
    log "  the heartbeat subshell (pid $HBPID) exited within ${DEADLINE}s of the script being SIGKILLed past its traps"
  else
    log "FAIL: the heartbeat subshell (pid $HBPID) outlived the SIGKILLed script."
    log "      A background child that outlives the script holds the stdout pipe open, and the Go side's"
    log "      cmd.Wait() then sits out its whole WaitDelay after the script has already exited."
    kill -KILL "$HBPID" 2>/dev/null
    pass=0
  fi
fi

# ── 5. LIVECERT_HEARTBEAT_S=0 turns it off ──────────────────────────────
log ""
log "=== 4. LIVECERT_HEARTBEAT_S=0: no heartbeat, no background child ==="
cat > "$WORK/case4.sh" <<EOF
set -uo pipefail
source "$BLOCK"
LIVECERT_HEARTBEAT_S=0
heartbeat_start cold_deploy
sleep $((IVL * 2 + 1))
heartbeat_stop
echo "HBPID=[\${HEARTBEAT_PID}]"
EOF
bash "$WORK/case4.sh" > "$WORK/case4.out" 2>&1
N4="$(grep -c '^HEARTBEAT ' "$WORK/case4.out" || true)"
if [ "${N4:-0}" = "0" ] && grep -q 'HBPID=\[\]' "$WORK/case4.out"; then
  log "  disabled: no lines, and no background child was started"
else
  log "FAIL: LIVECERT_HEARTBEAT_S=0 still produced output:"
  sed 's/^/    /' "$WORK/case4.out"
  pass=0
fi

# ── 6. a heartbeat line is not a protocol line ──────────────────────────
#
# tools/gauntlet/protocol.go reads lines beginning "GAUNTLET " and computes
# each stage's duration_s as the delta since the PREVIOUS such line. A
# heartbeat that spoke the protocol prefix would land between two stage
# lines and silently rewrite a stage's measured wall time.
log ""
log "=== 5. heartbeat lines are not GAUNTLET lines ==="
if ! grep -q '^HEARTBEAT ' "$WORK/case1.out"; then
  # Distinguished from the GAUNTLET case on purpose. Reporting "a
  # heartbeat line carries the protocol prefix" when there were no
  # heartbeat lines at all sends the reader after the wrong defect, and a
  # failure message is only worth anything the first time it appears.
  log "FAIL: case 1 produced no heartbeat lines, so this case had nothing to inspect (see case 1's own failure above)"
  pass=0
elif grep -q '^GAUNTLET ' "$WORK/case1.out"; then
  log "FAIL: a heartbeat line carries the GAUNTLET protocol prefix, which would rewrite a stage's duration_s:"
  grep '^GAUNTLET ' "$WORK/case1.out" | sed 's/^/    /'
  pass=0
else
  log "  the heartbeat speaks its own prefix; ParseProtocol ignores it and no stage's duration_s moves"
fi

log ""
if [ "$pass" = "1" ]; then
  log "=== selftest-heartbeat: PASS ==="
else
  log "=== selftest-heartbeat: FAIL - see above ==="
fi
exit $((1 - pass))
