#!/usr/bin/env bash
set -uo pipefail

# live/smoke/selftest-bounds.sh: proof for issue #1457.
#
# On 2026-09-21 one k8s smoke sat in its scenario step for 35 minutes, was
# cancelled by the job timeout and left no log. Nothing between `just smoke
# <name>` and the API server had a time limit: no bound on the scenario, none
# on a kubectl request, none on a choudoufu call made against a fail-closed
# webhook, and a wait loop that counted tries while one try could block for
# ever.
#
# This self-test runs the SHIPPED smoke.sh and lib.sh, copied into a scratch
# tree, against a stub `kubectl` that never returns, a stub `choudoufu` that
# never returns, and stub `kind` and `docker`, and requires each bound to
# fail the scenario BY NAME, naming the step, inside its limit, with the
# cluster still deleted and nothing left running. No cluster is made and no
# container is started; it needs bash, python3, ps and pgrep.
#
# The verdict is the per-case lines below, not the exit code. Every case
# prints "ok:" per property it confirmed and "FAIL:" per property it did
# not, and the script exits non-zero if any FAIL was printed.
#
# Usage:
#   bash live/smoke/selftest-bounds.sh
#   SMOKE_SRC=<a copy of live/smoke> bash live/smoke/selftest-bounds.sh
#     runs the same cases against another tree, which is how the red was
#     shown: against live/smoke as of main before this issue every case
#     below fails, the first three by hanging until this script kills them.
#   bash live/smoke/selftest-bounds.sh --only <case>   one case, by name.

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SMOKE_SRC="${SMOKE_SRC:-$HERE}"
REPO_ROOT="$(cd "$HERE/../.." && pwd)"

ONLY=""
while [ $# -gt 0 ]; do
  case "$1" in
    --only) ONLY="${2:-}"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

WORK="$(mktemp -d)"
PASS=1
SB=""

log() { printf '%s\n' "$*"; }
ok() { log "  ok: $*"; }
bad() { log "  FAIL: $*"; PASS=0; }
wanted() { [ -z "$ONLY" ] || [ "$ONLY" = "$1" ]; }

# This script's own process killer, written out here and not borrowed from
# lib.sh: a harness that kills a hung run with the code under test cannot
# tell a broken bound from a broken harness.
kill_tree() {
  local p
  for p in $(pgrep -P "$1" 2>/dev/null); do kill_tree "$p"; done
  kill -KILL "$1" 2>/dev/null
}
reap_stubs() {
  local p
  [ -f "$SB/stub.pids" ] || return 0
  while read -r p; do kill -KILL "$p" 2>/dev/null; done < "$SB/stub.pids"
}
trap 'reap_stubs 2>/dev/null; rm -rf "$WORK"' EXIT

# --- the stubs ---
# `kubectl` logs every invocation and its own pid, then does one of three
# things with a call matching KUBECTL_STALL_GLOB: never return (the fault
# #1457 is about), never return and ignore TERM as well, or, with
# KUBECTL_HONOUR_TIMEOUT=1, give up after its --request-timeout the way the
# real one does. Every other call answers the little the scenarios ask.
write_stubs() { # <bin dir>
  mkdir -p "$1"
  cat > "$1/kubectl" <<'KCEOF'
#!/usr/bin/env bash
args="$*"
printf '%s\n' "$args" >> "$STUB_DIR/kubectl.log"
echo "$$" >> "$STUB_DIR/stub.pids"
# shellcheck disable=SC2254  # the glob is the point
case "$args" in
  ${KUBECTL_STALL_GLOB:-__none__})
    if [ "${KUBECTL_HONOUR_TIMEOUT:-0}" = "1" ]; then
      secs="$(printf '%s\n' "$args" | sed -n 's/.*--request-timeout=\([0-9][0-9]*\)s.*/\1/p')"
      if [ -n "$secs" ]; then
        sleep "$secs"
        echo "Unable to connect to the server: context deadline exceeded (stub kubectl, after ${secs}s)" >&2
        exit 1
      fi
    fi
    [ "${KUBECTL_IGNORE_TERM:-0}" = "1" ] && trap '' TERM
    sleep 600 &
    echo "$!" >> "$STUB_DIR/stub.pids"
    wait
    exit 1
    ;;
esac
case "$args" in
  *version*) echo "Server Version: v0.0.0-stub" ;;
  *labels.tofu-estate*) printf 'smoke-k8s' ;;
esac
exit 0
KCEOF
  cat > "$1/kind" <<'KINDEOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$STUB_DIR/kind.log"
exit 0
KINDEOF
  cat > "$1/docker" <<'DKEOF'
#!/usr/bin/env bash
exit 0
DKEOF
  # The binary under test, as CHOUDOUFU_BIN. It answers the first two steps
  # of k8s-the-server-gets-the-last-word, and a call matching
  # CHDF_STALL_GLOB never returns.
  cat > "$1/choudoufu" <<'CHEOF'
#!/usr/bin/env bash
args="$*"
printf '%s\n' "$args" >> "$STUB_DIR/choudoufu.log"
echo "$$" >> "$STUB_DIR/stub.pids"
# shellcheck disable=SC2254
case "$args" in
  ${CHDF_STALL_GLOB:-__none__})
    sleep 600 &
    echo "$!" >> "$STUB_DIR/stub.pids"
    wait
    exit 1
    ;;
esac
case "$args" in
  plan*-out=*) : > saved.tfplan; echo "Plan: 0 to add, 1 to change, 0 to destroy." ;;
  apply*) echo "Apply complete! Resources: 1 added, 0 changed, 0 destroyed." ;;
esac
exit 0
CHEOF
  chmod +x "$1/kubectl" "$1/kind" "$1/docker" "$1/choudoufu"
}

# The scenario the first cases drive. It is written here and dropped into the
# scratch copy, so what is under test is smoke.sh and lib.sh as shipped and
# the stall is in a step whose name this script knows. The `|| fail` after
# each stalled call is on purpose: a killed call fails, and that failure must
# not print a second verdict under the bound's own.
write_stall_scenario() { # <scenarios dir>
  cat > "$1/k8s-selftest-stall.sh" <<'SCEOF'
# k8s-selftest-stall
# selftest-bounds.sh's own scenario (#1457): step 2 never returns.
SMOKE_WORK="$SMOKE_WORKROOT/work"; mkdir -p "$SMOKE_WORK"
cluster_up
step "1. a step that returns"
kc get namespaces >/dev/null || fail "k8s-selftest-stall" "step 1 failed"
kc_as "$KUBECONFIG" auth can-i list secrets >/dev/null || fail "k8s-selftest-stall" "step 1 failed"
step "2. the step that stalls"
case "$SELFTEST_STALL" in
  kubectl)
    OUT="$(kc create namespace stall 2>&1)" || fail "k8s-selftest-stall" "create namespace failed: $OUT" ;;
  choudoufu)
    OUT="$(cd "$SMOKE_WORK" && chdf_bounded apply -auto-approve 2>&1)" || fail "k8s-selftest-stall" "apply failed: $OUT" ;;
esac
step "3. a step that must never start"
SCEOF
}

# sandbox <name>: a scratch tree shaped like the repository as far as lib.sh
# reads it (live/smoke, live/floci-image, live/oracle-versions.json), the
# stubs on a bin dir of its own, and the logs the stubs write.
sandbox() {
  [ -z "${SB:-}" ] || reap_stubs
  SB="$WORK/$1"
  mkdir -p "$SB/bin" "$SB/tree/live/smoke/scenarios"
  cp "$SMOKE_SRC/smoke.sh" "$SMOKE_SRC/lib.sh" "$SMOKE_SRC/VERSION" "$SMOKE_SRC/claims.json" \
     "$SMOKE_SRC/docker-compose.yml" "$SB/tree/live/smoke/"
  cp "$SMOKE_SRC/scenarios/k8s-the-server-gets-the-last-word.sh" "$SB/tree/live/smoke/scenarios/"
  cp "$REPO_ROOT/live/floci-image" "$REPO_ROOT/live/oracle-versions.json" "$SB/tree/live/"
  write_stubs "$SB/bin"
  write_stall_scenario "$SB/tree/live/smoke/scenarios"
  : > "$SB/kubectl.log"; : > "$SB/kind.log"; : > "$SB/choudoufu.log"; : > "$SB/stub.pids"
  log ""
  log "=== $1 ==="
}

# run_smoke <scenario> <limit secs> [VAR=value ...] runs the scratch copy's
# smoke.sh with the stubs first on PATH and sets RC, ELAPSED, OUT (a file)
# and HUNG. A run still going at <limit> is killed by this script and HUNG
# is 1: that is what an unbounded stall looks like from here, and it is the
# only reason this selftest cannot itself hang. <limit> is also what "late"
# means: the bound under test, the kill grace, and about eight seconds for
# python3, the stubs and a slow runner. A verdict after that is a FAIL here.
run_smoke() {
  local scenario="$1" limit="$2"; shift 2
  local pid start
  OUT="$SB/out.txt"; HUNG=0; RC=0
  start="$(date +%s)"
  env "$@" STUB_DIR="$SB" PATH="$SB/bin:$PATH" CHOUDOUFU_BIN="$SB/bin/choudoufu" \
    bash "$SB/tree/live/smoke/smoke.sh" "$scenario" > "$OUT" 2>&1 &
  pid=$!
  while kill -0 "$pid" 2>/dev/null; do
    if [ $(( $(date +%s) - start )) -ge "$limit" ]; then
      HUNG=1; kill_tree "$pid"; break
    fi
    sleep 1
  done
  wait "$pid" 2>/dev/null; RC=$?
  ELAPSED=$(( $(date +%s) - start ))
}

show_out() { sed "s/^/    ${1:-} | /" "$OUT"; }

stubs_all_gone() {
  local p left=""
  while read -r p; do kill -0 "$p" 2>/dev/null && left="$left $p"; done < "$SB/stub.pids"
  [ -z "$left" ] && return 0
  echo "$left"; return 1
}

# check_stall_verdict <scenario> <what the FAIL line must say> <step>: the
# properties every bound owes, read off one finished run.
check_stall_verdict() {
  local scenario="$1" what="$2" stepname="$3" n left
  if [ "$HUNG" = "1" ]; then
    bad "the run was still going after ${ELAPSED}s and this script killed it: nothing bounded the stall"
    return 0
  fi
  ok "the run ended by itself after ${ELAPSED}s, inside this case's limit"
  if grep -qE "^FAIL \[$scenario\]: .*$what.*in step \"$stepname\"" "$OUT"; then
    ok "the verdict names the scenario, what ran out and the step: $(grep -E '^FAIL \[' "$OUT" | head -1)"
  else
    bad "no 'FAIL [$scenario]: ...$what... in step \"$stepname\"' line in the output"
  fi
  n="$(grep -cE '^FAIL \[' "$OUT")"
  if [ "$n" = "1" ]; then ok "exactly one FAIL line: the killed call's own failure did not print a second verdict"
  else bad "$n FAIL lines, wanted exactly 1"; fi
  if [ "$RC" = "124" ]; then ok "exit 124"; else bad "exit $RC, wanted 124"; fi
  if grep -q '=== 1. a step that returns ===' "$OUT"; then ok "the steps before the stall are in the output, so the log shows where it stopped"
  else bad "the output does not show the step before the stall"; fi
  if grep -q 'a step that must never start' "$OUT"; then bad "the scenario carried on past the stalled step"
  else ok "nothing after the stalled step ran"; fi
  if grep -q '^PASS:' "$OUT"; then bad "the run printed a PASS line"; else ok "no PASS line"; fi
  if grep -q '^delete cluster' "$SB/kind.log"; then ok "the EXIT trap still ran: kind delete cluster was called"
  else bad "kind delete cluster was never called, so a stalled run leaves its cluster behind"; fi
  if left="$(stubs_all_gone)"; then ok "no stub process outlived the run (the stalled call and its sleep were grandchildren)"
  else bad "still running after the run ended:$left"; fi
}

# --- 1. the scenario bound, against a kubectl that never returns ---
# The scenario bound counts from the scenario's start, so the step it names
# depends on the run reaching step 2 before it fires. Ten seconds is the
# margin for that: a four-second bound named an earlier step once, on a
# loaded laptop, in some forty runs. The other bounds count from the call
# they guard and have no such race.
if wanted watchdog-kubectl; then
  sandbox watchdog-kubectl
  run_smoke k8s-selftest-stall 20 SELFTEST_STALL=kubectl KUBECTL_STALL_GLOB='*create namespace*' \
    SMOKE_TIMEOUT_SECS=10 SMOKE_KILL_GRACE_SECS=2
  check_stall_verdict k8s-selftest-stall "no verdict after 10s" "2. the step that stalls"
  if grep -q 'still running: .*kubectl.*create namespace stall' "$OUT"; then ok "the stalled command is named above the verdict"
  else bad "the output does not name the command that was still running"; fi
  [ "$PASS" = "1" ] || show_out
fi

# --- 2. the same, against a kubectl that ignores TERM ---
if wanted watchdog-kubectl-ignores-term; then
  sandbox watchdog-kubectl-ignores-term
  run_smoke k8s-selftest-stall 20 SELFTEST_STALL=kubectl KUBECTL_STALL_GLOB='*create namespace*' \
    KUBECTL_IGNORE_TERM=1 SMOKE_TIMEOUT_SECS=10 SMOKE_KILL_GRACE_SECS=2
  check_stall_verdict k8s-selftest-stall "no verdict after 10s" "2. the step that stalls"
  [ "$PASS" = "1" ] || show_out
fi

# --- 3. the choudoufu bound, inside a scenario bound that is far away ---
if wanted chdf-bound; then
  sandbox chdf-bound
  run_smoke k8s-selftest-stall 15 SELFTEST_STALL=choudoufu CHDF_STALL_GLOB='apply*' \
    CHDF_TIMEOUT_SECS=3 SMOKE_TIMEOUT_SECS=300 SMOKE_KILL_GRACE_SECS=2
  check_stall_verdict k8s-selftest-stall "choudoufu apply did not return within 3s" "2. the step that stalls"
  [ "$PASS" = "1" ] || show_out
fi

# --- 4. the shipped scenario: wait_admission against requests that time out ---
# k8s-the-server-gets-the-last-word, as shipped, up to the wait that hung in
# CI. Every admission-probe request gives up after its --request-timeout,
# which only happens if kc passes one. The wait then has to fail by name
# inside its own deadline, and it must NOT take a request that timed out for
# the webhook refusing a write, which is what step 1 is waiting to see.
if wanted last-word-wait; then
  sandbox last-word-wait
  run_smoke k8s-the-server-gets-the-last-word 20 KUBECTL_STALL_GLOB='*admission-probe*' \
    KUBECTL_HONOUR_TIMEOUT=1 PROBE_REQUEST_TIMEOUT=1s ADMISSION_WAIT_SECS=4 SMOKE_TIMEOUT_SECS=300
  if [ "$HUNG" = "1" ]; then
    bad "the run was still going after ${ELAPSED}s and this script killed it: a probe request never came back, so kc carries no --request-timeout"
  else
    ok "the run ended by itself after ${ELAPSED}s"
    if grep -qE '^FAIL \[k8s-the-server-gets-the-last-word\]: admission did not reach the state this step needs within 4s' "$OUT"; then
      ok "wait_admission failed by name inside its deadline: $(grep -E '^FAIL \[' "$OUT" | head -1 | cut -c1-150)..."
    else
      bad "no 'admission did not reach the state this step needs within 4s' verdict"
    fi
    if grep -q '=== 2\. ' "$OUT"; then bad "step 2 started: a request that timed out was read as the webhook's refusal"
    else ok "a request that timed out was not read as the webhook's refusal; step 2 never started"; fi
    if grep -q '^delete cluster' "$SB/kind.log"; then ok "kind delete cluster was called"
    else bad "kind delete cluster was never called"; fi
  fi
  [ "$PASS" = "1" ] || show_out
fi

# --- 5. every kubectl request carries a timeout ---
# Two halves. What the stub saw: each request the cases above made has
# --request-timeout on it. And the roster: no k8s scenario, and nothing in
# lib.sh but kc_as, calls kubectl bare, so a scenario written tomorrow cannot
# go round the helper. `kubectl config` only edits a local file and is
# allowed.
if wanted kc-request-timeout; then
  sandbox kc-request-timeout
  run_smoke k8s-selftest-stall 25 SELFTEST_STALL=none SMOKE_TIMEOUT_SECS=60
  n="$(grep -c . "$SB/kubectl.log")"
  if [ "$n" -ge 3 ]; then ok "the stub kubectl saw $n requests (cluster_up's, kc's and kc_as's)"
  else bad "the stub kubectl saw $n requests, wanted at least 3; this case measured nothing"; fi
  missing="$(grep -v -- '--request-timeout=' "$SB/kubectl.log" || true)"
  if [ -z "$missing" ]; then ok "every one of them carries --request-timeout"
  else bad "kubectl requests without --request-timeout: $(echo "$missing" | tr '\n' ';')"; fi

  # bare_kubectl <file>: lines where kubectl is a command word. `kubectl
  # config` is allowed, and so are the lines that only print a command for
  # the reader (cmd, note, proof, step, echo, and explain's quoted lines).
  bare_kubectl() {
    grep -nE '(^|[|;&(]|\$\(|as_role [A-Za-z_]+ )[[:space:]]*kubectl[[:space:]]' "$1" \
      | grep -vE '^[0-9]+:[[:space:]]*(#|"|(cmd|note|proof|step|echo|explain)[[:space:]])' \
      | grep -vE 'kubectl[[:space:]]+(--kubeconfig[[:space:]]+"[^"]*"[[:space:]]+)?config[[:space:]]'
  }
  checked=0
  for f in "$SMOKE_SRC"/scenarios/k8s-*.sh; do
    checked=$((checked+1))
    hits="$(bare_kubectl "$f" || true)"
    [ -z "$hits" ] || bad "$(basename "$f") calls kubectl bare; use kc or kc_as, which carry --request-timeout: $(echo "$hits" | head -3 | tr '\n' ';')"
  done
  if [ "$checked" -ge 8 ]; then ok "$checked k8s scenarios read for a bare kubectl"
  else bad "only $checked k8s scenarios found under $SMOKE_SRC/scenarios; the roster read nothing"; fi
  libhits="$(bare_kubectl "$SMOKE_SRC/lib.sh" | grep -vc -- '--request-timeout=' || true)"
  if [ "$libhits" = "0" ]; then ok "lib.sh calls kubectl in kc_as and nowhere else"
  else bad "lib.sh has $libhits kubectl call(s) with no --request-timeout"; fi
  # The roster's own red: a bare call planted in a copy must be found.
  cp "$SMOKE_SRC/scenarios/k8s-greenfield.sh" "$SB/planted.sh"
  echo 'LEFT="$(kubectl get configmaps -A -o name)"' >> "$SB/planted.sh"
  if [ -n "$(bare_kubectl "$SB/planted.sh" || true)" ]; then ok "a bare kubectl planted in a copy of k8s-greenfield.sh is found"
  else bad "the roster did not find a planted bare kubectl, so it cannot go red"; fi
  [ "$PASS" = "1" ] || show_out
fi

# --- 6. the mutant: the same stall with the scenario bound taken out ---
# Case 1 again, against a copy of smoke.sh with the line that starts the
# watchdog removed. It has to hang until this script kills it. Without this
# the cases above could be passing over a stub that returns by itself.
if wanted mutant-no-watchdog; then
  sandbox mutant-no-watchdog
  if grep -q '^    smoke_timer "\$BOUND".*' "$SB/tree/live/smoke/smoke.sh"; then
    sed 's/^    smoke_timer "\$BOUND".*/    SMOKE_TIMER_PID=""/' "$SB/tree/live/smoke/smoke.sh" > "$SB/smoke.mutant" \
      && mv "$SB/smoke.mutant" "$SB/tree/live/smoke/smoke.sh"
    run_smoke k8s-selftest-stall 20 SELFTEST_STALL=kubectl KUBECTL_STALL_GLOB='*create namespace*' \
      SMOKE_TIMEOUT_SECS=10 SMOKE_KILL_GRACE_SECS=2
    show_out mutant
    if [ "$HUNG" = "1" ] && ! grep -qE '^FAIL \[' "$OUT"; then
      ok "the same case fails against a smoke.sh with no watchdog: still going after ${ELAPSED}s, no FAIL line, killed by this script"
    else
      bad "with the watchdog removed the run still ended by itself (HUNG=$HUNG, exit $RC); case 1 is not measuring the watchdog"
    fi
  else
    bad "smoke.sh has no 'smoke_timer \"\$BOUND\"' line to remove; the mutant cannot be built, so nothing shows these checks can go red"
  fi
  reap_stubs
fi

log ""
if [ "$PASS" = "1" ]; then
  log "PASS: selftest-bounds - every stalled call failed by name inside its bound, and the cluster was still deleted (#1457)"
  exit 0
fi
log "FAIL: selftest-bounds - see the FAIL lines above (#1457)"
exit 1
