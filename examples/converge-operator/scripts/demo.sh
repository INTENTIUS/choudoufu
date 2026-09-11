#!/usr/bin/env bash
# The replan-and-converge loop, actually run (issue #1033): a choudoufu root,
# `dev-converge`'s ConvergeOp rule table, and `chant operator` ticking it on
# an interval against floci — ending with the gate `dev-converge` dispatches
# still pending after the operator that discovered it has been killed and
# restarted, because the gate is a fact on the `chant/lifecycle` git ledger
# and not a wait on a process.
#
# One verdict line per stage, greppable ("VERDICT stage=... "), plus the raw
# `chant operator log`/`chant operator status` output at each checkpoint —
# read those, not the exit codes, per this repository's own rule that a
# check which cannot fail is not a check.
#
# ## Why a scratch repository, not this worktree
#
# Same reason as `examples/ci-pipelines/scripts/smoke.sh`: chant's gate and
# converge ledgers live on the orphan `chant/lifecycle` branch, and
# `pushLifecycle` pushes it to the first configured remote after every
# ledger write, force-with-lease, swallowing the failure. This script builds
# a throwaway repository with no remote — `git remote` comes back empty, the
# push is a local no-op, and the ledger stays on disk where every check below
# reads it straight back.
#
# ## Two upstream findings, both fixed in chant 0.68.1 (this project's pin)
#
# Both discovered by running this example against a real floci while it
# pinned chant 0.63.0; both closed upstream now — chant#2395 and chant#2396,
# quoted in full in README.md's "Two upstream findings" section. The
# `convergeTick` JSON parse (chant#2395) and the `classifyDispatchFailure`
# gate misclassification (chant#2396) are both fixed as of this pin, so this
# script needs no workaround for either: the tick's own summary line reads
# `gated=1` for a genuinely gated dispatch, matching the real pending gate
# this script also reads independently from `chant operator status` / `chant
# run log dev-apply`.
#
# Env:
#   CHOUDOUFU_BIN   an existing choudoufu binary; default builds ./cmd/choudoufu
#   FLOCI_PORT      host port for the emulator (default 4671, issue #1033's own)
#   FLOCI_IMAGE     override the pin in live/floci-image
#   CONTAINER       emulator container name (default wt-1033-floci)
#   OPERATOR_INTERVAL  chant operator's --interval (default 10s)
#   KEEP            1 leaves the scratch repository and the container up
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
EXAMPLE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
ROOT="$(cd "$EXAMPLE_DIR/../.." && pwd)"

FLOCI_PORT="${FLOCI_PORT:-4671}"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
CONTAINER="${CONTAINER:-wt-1033-floci}"
OPERATOR_INTERVAL="${OPERATOR_INTERVAL:-10s}"
# Short on purpose: chant's own lease default (5m, `DEFAULT_LEASE_TTL_MS`) is
# sized for a real environment, where nothing else is about to grab the
# lease. This script kills and restarts the operator deliberately, and with
# the 5m default the restarted process would find the dead holder's lease
# still unexpired and sit out every round with `skipped=1(lease-held:...)`
# for minutes — measured, not assumed, while building this demo. A short TTL
# here is what makes the restart proof land inside this script's own poll
# windows rather than chant's real-world default.
LEASE_TTL="${LEASE_TTL:-15s}"
WORK=""
OPERATOR_PID=""
FAILURES=0
PASSES=0

log()  { echo "  $*"; }
step() { echo; echo "=== $* ==="; echo; }
die()  { echo "DEMO fatal: $*" >&2; cleanup; exit 1; }

stop_operator() {
  if [ -n "$OPERATOR_PID" ] && kill -0 "$OPERATOR_PID" 2>/dev/null; then
    kill "$OPERATOR_PID" 2>/dev/null
    for _ in $(seq 1 20); do kill -0 "$OPERATOR_PID" 2>/dev/null || break; sleep 0.5; done
    kill -9 "$OPERATOR_PID" 2>/dev/null || true
  fi
  OPERATOR_PID=""
}

cleanup() {
  stop_operator
  if [ "${KEEP:-0}" = "1" ]; then
    echo "KEEP=1: container $CONTAINER and scratch repo $WORK left up." >&2
    return
  fi
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  [ -n "$WORK" ] && rm -rf "$WORK"
}
trap cleanup EXIT

verdict() {
  local stage="$1" want="$2" got="$3" extra="${4:-}"
  if [ "$want" = "$got" ]; then
    PASSES=$((PASSES + 1))
    echo "VERDICT stage=$stage verdict=pass status=$got${extra:+ $extra}"
  else
    FAILURES=$((FAILURES + 1))
    echo "VERDICT stage=$stage verdict=fail status=$got want=$want${extra:+ $extra}"
  fi
}

# run_status <file>: the run record chant writes as the last JSON *line* of
# a `chant run <op> --json` invocation's stdout — the same convention
# ci-pipelines/scripts/smoke.sh reads by, and for the same reason: the
# human-readable render `report()` echoes ahead of it, and `chant run
# <op> --json`'s own final record is already one minified line, so scanning
# backward for the last line starting with `{` that parses as a run record
# is sufficient here, same as smoke.sh.
run_status() {
  node -e '
    const fs = require("fs");
    const lines = fs.readFileSync(process.argv[1], "utf8").split("\n");
    for (let i = lines.length - 1; i >= 0; i--) {
      const line = lines[i].trim();
      if (!line.startsWith("{")) continue;
      try {
        const r = JSON.parse(line);
        if (r && r.op && r.status) { console.log(r.status); process.exit(0); }
      } catch { /* not the record */ }
    }
    console.log("no-run-record");
  ' "$1"
}

# last_top_level_json <file>: takes the last top-level JSON object on
# stdout, the same defense ci-pipelines' smoke.sh applies at the line level
# for a multi-line `--json` stream, just at the object level. Unused now
# that chant 0.68.1 (chant#2395) no longer echoes a subprocess blob ahead of
# its own document on a live root's `--json` read; kept for any subcommand
# that still concatenates documents on stdout.
last_top_level_json() {
  node -e '
    const fs = require("fs");
    const lines = fs.readFileSync(process.argv[1], "utf8").split("\n");
    let start = 0;
    for (let i = lines.length - 1; i >= 0; i--) { if (lines[i] === "{") { start = i; break; } }
    process.stdout.write(lines.slice(start).join("\n"));
  ' "$1"
}

# newest_run_status <op>: the STATUS column of the newest row `chant run log
# <op>` prints — that command's own human table, header row first, newest
# run first (verified against a live run while building this example).
newest_run_status() {
  npx chant run log "$1" 2>/dev/null | sed -n '2p' | awk '{print $2}'
}

# wait_for_tick_after <log-file> <since-line> <timeout-s>: block, polling
# every 3s, until a `ticked=1` round line appears after line `since-line` of
# `log-file` — chant operator's own per-round line (`formatRoundLine`,
# printed to stderr, redirected into this file). Returns nonzero on timeout.
# A condition that cannot match itself, per this repository's own rule on
# polling loops: it names an exact line number to look past, not a marker
# the loop's own prior iteration could have already satisfied.
wait_for_tick_after() {
  local file="$1" since_line="$2" timeout_s="$3"
  local waited=0
  while [ "$waited" -lt "$timeout_s" ]; do
    local n; n=$(wc -l < "$file" | tr -d ' ')
    if [ "$n" -gt "$since_line" ] && sed -n "$((since_line + 1)),\$p" "$file" | grep -q "ticked=1"; then
      return 0
    fi
    sleep 3
    waited=$((waited + 3))
  done
  return 1
}

# ---------------------------------------------------------------- preflight

command -v docker >/dev/null 2>&1 || die "docker is not installed"
docker info >/dev/null 2>&1 || die "the docker daemon is not running (start Docker Desktop)"
command -v node >/dev/null 2>&1 || die "node is not installed"
command -v git  >/dev/null 2>&1 || die "git is not installed"
command -v aws  >/dev/null 2>&1 || die "the aws CLI is not installed"

step "the binary under test"
if [ -n "${CHOUDOUFU_BIN:-}" ]; then
  [ -x "$CHOUDOUFU_BIN" ] || die "CHOUDOUFU_BIN=$CHOUDOUFU_BIN is not executable"
  CHOUDOUFU_BIN="$(cd "$(dirname "$CHOUDOUFU_BIN")" && pwd)/$(basename "$CHOUDOUFU_BIN")"
else
  command -v go >/dev/null 2>&1 || die "no CHOUDOUFU_BIN and no go toolchain to build one"
  BIN_DIR="$(mktemp -d)"
  log "building ./cmd/choudoufu from this worktree"
  ( cd "$ROOT" && env -u PWD go build -o "$BIN_DIR/choudoufu" ./cmd/choudoufu ) \
    || die "go build ./cmd/choudoufu failed"
  CHOUDOUFU_BIN="$BIN_DIR/choudoufu"
fi
log "$("$CHOUDOUFU_BIN" version | head -1)"

# ------------------------------------------------------- the scratch project

step "the project, at the root of a repository with no remote"
WORK="$(mktemp -d)"
mkdir -p "$WORK/repo"
tar -C "$EXAMPLE_DIR" -cf - \
  --exclude=node_modules --exclude=.terraform --exclude='*.tfstate*' --exclude='*.tfplan' --exclude=dist \
  . | tar -C "$WORK/repo" -xf -
(
  cd "$WORK/repo"
  npm ci --no-audit --no-fund >/dev/null
) || die "npm ci failed"

(
  cd "$WORK/repo"
  git init -q .
  git config user.email demo@example.invalid
  git config user.name "converge-operator demo"
  printf 'node_modules/\n.terraform/\n*.tfstate*\n*.tfplan\ndist/\n' > .gitignore
  git add -A >/dev/null && git commit -qm "the converge-operator example, for the demo"
) || die "could not stand up the scratch repository"
git -C "$WORK/repo" remote | grep -q . && die "the scratch repository has a remote; the ledger would be pushed"
log "$WORK/repo (no remote: chant's pushLifecycle cannot reach anything)"

# ------------------------------------------------------------- the emulator

step "the emulator"
docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
docker run -d --name "$CONTAINER" -p "127.0.0.1:$FLOCI_PORT:4566" "$FLOCI_IMAGE" >/dev/null \
  || die "could not start $FLOCI_IMAGE"
ENDPOINT="http://localhost:$FLOCI_PORT"
ready=0
for _ in $(seq 1 60); do
  if curl -fsS "$ENDPOINT/_localstack/health" >/dev/null 2>&1; then ready=1; break; fi
  sleep 2
done
[ "$ready" = "1" ] || die "floci never answered on $ENDPOINT/_localstack/health"
log "$CONTAINER ready at $ENDPOINT"

export PATH="$(dirname "$CHOUDOUFU_BIN"):$PATH"
export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test
export AWS_REGION=us-east-1 AWS_DEFAULT_REGION=us-east-1
export TF_VAR_aws_region=us-east-1
export CHECKPOINT_DISABLE=1

OUT="$WORK/out"; mkdir -p "$OUT"
cd "$WORK/repo" || die "cannot enter the scratch repository"
LOG_GROUP=/choudoufu-converge-operator-example/app

# ---------------------------------------------------------- the baseline apply
#
# `chant run dev-apply`, gated then approved then applied, run directly
# (not through the operator) — issue #1033 asks for a root that is already
# inited and applied by the time the operator starts converging it.

step "init and apply the root (baseline, before the operator ever runs)"
npx chant run dev-apply --gated-exit 0 --json > "$OUT/apply1.log" 2>&1
verdict baseline-gate gated "$(run_status "$OUT/apply1.log")"
npx chant approve dev-apply approve-dev-apply --approver demo > "$OUT/approve1.log" 2>&1
verdict baseline-approve resolved "$(grep -q "resolved by demo" "$OUT/approve1.log" && echo resolved || echo not-approved)"
npx chant run dev-apply --gated-exit 0 --json > "$OUT/apply2.log" 2>&1
verdict baseline-apply ok "$(run_status "$OUT/apply2.log")"

# ---------------------------------------------------------------- the operator
#
# nohup'd inside this same call, which then polls its log synchronously —
# never backgrounded across turns.

step "start chant operator, ticking dev-converge every $OPERATOR_INTERVAL"
nohup npx chant operator --env dev --interval "$OPERATOR_INTERVAL" --lease-ttl "$LEASE_TTL" > "$OUT/operator.log" 2>&1 &
OPERATOR_PID=$!
log "operator pid $OPERATOR_PID, log at $OUT/operator.log"

LINE0=0
wait_for_tick_after "$OUT/operator.log" "$LINE0" 120
verdict operator-started running "$([ $? -eq 0 ] && echo running || echo silent)"
LINE1=$(wc -l < "$OUT/operator.log" | tr -d ' ')

step "observe: the first tick, clean estate"
npx chant operator log --op dev-converge --json 2>/dev/null > "$OUT/opstat1.json"
node -e '
  const d = JSON.parse(require("fs").readFileSync(process.argv[1], "utf8"));
  const ticks = d.entries.filter(e => e.kind === "tick");
  const last = ticks[ticks.length - 1];
  console.log(last ? last.record.log : "no tick recorded");
' "$OUT/opstat1.json" | tee "$OUT/clean-tick.line"
grep -q "drifted=0" "$OUT/clean-tick.line" && grep -q "gated=0" "$OUT/clean-tick.line"
verdict observe-clean drifted=0 "$(grep -o 'drifted=[0-9]*' "$OUT/clean-tick.line" | head -1)"

# --------------------------------------------------------------- the drift

step "introduce drift from the AWS CLI: delete the declared log group"
aws logs delete-log-group --log-group-name "$LOG_GROUP" > "$OUT/delete.log" 2>&1
verdict drift-deleted deleted "$([ $? -eq 0 ] && echo deleted || echo delete-failed)"
aws logs describe-log-groups --log-group-name-prefix "$LOG_GROUP" > "$OUT/describe-after-delete.json" 2>&1
grep -q '"logGroups": \[\]' "$OUT/describe-after-delete.json"
verdict drift-confirmed-absent absent "$([ $? -eq 0 ] && echo absent || echo still-present)"

wait_for_tick_after "$OUT/operator.log" "$LINE1" 120
verdict operator-ticked-after-drift running "$([ $? -eq 0 ] && echo running || echo silent)"

step "classify + dispatch: the next tick sees createCount>0 and dispatches dev-apply"
npx chant operator log --op dev-converge --json 2>/dev/null > "$OUT/opstat2.json"
node -e '
  const d = JSON.parse(require("fs").readFileSync(process.argv[1], "utf8"));
  const ticks = d.entries.filter(e => e.kind === "tick");
  const last = ticks[ticks.length - 1];
  console.log(last ? last.record.log : "no tick recorded");
  console.log(last ? JSON.stringify(last.record.outcomes) : "[]");
' "$OUT/opstat2.json" > "$OUT/drift-tick.lines"
cat "$OUT/drift-tick.lines"
grep -q "recreate-deleted" "$OUT/opstat2.json"
verdict classify-fired fired "$(grep -q 'recreate-deleted' "$OUT/opstat2.json" && echo fired || echo not-fired)"

step "the gate: dev-apply's own run, read from chant operator status"
npx chant operator status > "$OUT/status1.txt" 2>&1
cat "$OUT/status1.txt"
verdict gate-pending pending "$(grep -q 'dev-apply gate "approve-dev-apply"' "$OUT/status1.txt" && echo pending || echo absent)"
verdict gate-run-status gated "$(newest_run_status dev-apply)"

# --------------------------------------------------- restart while gated

step "kill the operator while the gate is pending"
stop_operator
verdict operator-stopped stopped "stopped"

step "the gate still holds with no daemon running (a ledger fact, not a wait)"
npx chant operator status > "$OUT/status2.txt" 2>&1
diff <(grep '^    expires:' "$OUT/status1.txt") <(grep '^    expires:' "$OUT/status2.txt") >/dev/null
verdict gate-held-no-daemon unchanged "$([ $? -eq 0 ] && echo unchanged || echo changed)"

step "restart the operator"
nohup npx chant operator --env dev --interval "$OPERATOR_INTERVAL" --lease-ttl "$LEASE_TTL" > "$OUT/operator2.log" 2>&1 &
OPERATOR_PID=$!
log "operator restarted, pid $OPERATOR_PID"
LINE2=0
wait_for_tick_after "$OUT/operator2.log" "$LINE2" 120
verdict operator-restarted running "$([ $? -eq 0 ] && echo running || echo silent)"
# A second round, so the restarted process has ticked against the still-
# pending gate at least once on its own schedule (not just at startup)
# before we read it back — polled the same way, never a blind sleep.
LINE2b=$(wc -l < "$OUT/operator2.log" | tr -d ' ')
wait_for_tick_after "$OUT/operator2.log" "$LINE2b" 120

npx chant operator status > "$OUT/status3.txt" 2>&1
cat "$OUT/status3.txt"
diff <(grep '^    expires:' "$OUT/status1.txt") <(grep '^    expires:' "$OUT/status3.txt") >/dev/null
verdict gate-held-after-restart same-fact "$([ $? -eq 0 ] && echo same-fact || echo changed)"
verdict gate-still-pending-after-restart pending "$(grep -q 'dev-apply gate "approve-dev-apply"' "$OUT/status3.txt" && echo pending || echo absent)"

# ------------------------------------------------------------------ approve

step "chant approve, then let the next tick converge"
npx chant approve dev-apply approve-dev-apply --approver demo > "$OUT/approve2.log" 2>&1
cat "$OUT/approve2.log"
verdict approve resolved "$(grep -q 'resolved by demo' "$OUT/approve2.log" && echo resolved || echo not-approved)"

LINE3=$(wc -l < "$OUT/operator2.log" | tr -d ' ')
wait_for_tick_after "$OUT/operator2.log" "$LINE3" 120
verdict operator-ticked-after-approve running "$([ $? -eq 0 ] && echo running || echo silent)"
# A second round in case the tick right after approval landed before the
# approval committed (a genuine race an interval-driven demo can hit) — the
# next one re-reads the resolution and applies. Polled, not a blind sleep.
LINE3b=$(wc -l < "$OUT/operator2.log" | tr -d ' ')
wait_for_tick_after "$OUT/operator2.log" "$LINE3b" 120

step "converge: the resource is back"
aws logs describe-log-groups --log-group-name-prefix "$LOG_GROUP" > "$OUT/describe-after-converge.json" 2>&1
cat "$OUT/describe-after-converge.json"
verdict converged recreated "$(grep -q "$LOG_GROUP" "$OUT/describe-after-converge.json" && echo recreated || echo still-absent)"
verdict apply-run-status ok "$(newest_run_status dev-apply)"

# --------------------------------------------------------------- teardown

step "stop the operator and tear down"
stop_operator

step "tally"
echo "DEMO total pass=$PASSES fail=$FAILURES"
[ "$FAILURES" = "0" ] || exit 1
