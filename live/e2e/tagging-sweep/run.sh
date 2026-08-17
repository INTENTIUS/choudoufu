#!/usr/bin/env bash
set -euo pipefail

# GitHub issue #255, end to end against a real emulator.
#
# The claim under test, in one sentence: a full-estate live-plan gathers its
# removal candidates from ONE Resource Groups Tagging API call, against the
# emulator this repository pins, through the command wiring a user actually
# runs - and finds an undeclared resource that the per-type fallback cannot
# see at all.
#
# Why this needs a cloud at all, and why it did not exist before. Discovery's
# tagging leg (internal/live/discovery/discovery.go, the TaggingSweep branch)
# had unit coverage and one live test that constructs a discovery.Request by
# hand. What nothing exercised was the wiring in
# internal/command/live_plan.go, because that wiring turned TaggingSweep OFF
# for any loopback endpoint - and loopback is what every emulator harness in
# this repository uses. So the one configuration that could have covered the
# branch was the one configuration excluded from it. The gate's premise (the
# emulator's tagging index was blind) was fixed in lex00/floci#229 and the
# pin moved; the gate stayed. This script is the coverage the gate's removal
# is worth.
#
# Two runs, deliberately:
#
#   A. the default. The debug log must show the sweep going through the
#      Tagging API, and the plan must propose destroying exactly the block
#      that was deleted.
#   B. TOFU_LIVE_CLOUDCONTROL=off, which skips the whole Cloud Control /
#      tagging block in live_plan.go and returns the run to the per-type
#      sweep. The Tagging API line must be absent.
#
# B is what keeps A honest. An assertion that a log line is present proves
# nothing on its own if the line is printed unconditionally; B is the control
# that shows the line tracks the branch.
#
# B also measures something this fixture did not expect to find, so it is
# recorded here rather than in a commit message. The per-type sweep does NOT
# propose the removal at all against this emulator, and the reason is not the
# emulator: the AWS provider's aws_iam_role list resource (6.58.0,
# internal/service/iam/role_list.go) builds its objects from iam:ListRoles
# and issues no GetRole per member - checked in the debug log, zero GetRole
# requests reach the wire during the ListResource call - and iam:ListRoles
# returns no tags, on floci and on real AWS alike ("this operation does not
# return tags ... to view all of the information for a role, see GetRole").
# So a listed role carries an empty tag map, no ownership marker can be read
# off it, and the estate-wide tagging sweep is the ONLY path that detects an
# undeclared aws_iam_role. The gate this script exists to retire was
# therefore not costing the emulator tier its speed. It was costing it the
# removal.
#
# That is asserted, not merely noted, because an unchecked claim is what
# issue #255 was about. If a provider release makes the list path carry tags,
# run B will start proposing the destroy and this script will fail with a
# message saying so - which is the right way to find out.
#
#   bash live/e2e/tagging-sweep/run.sh
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4601, off run.sh's 4566
#                and dataread-projection's 4599 so all three can run at once).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image, the same single source of truth
#                live/e2e/run.sh uses.
#
# Exit codes: 0 on a real pass, non-zero on a real failure. Every assertion
# reads actual command output, an exit code, or the emulator's own answer
# through the AWS CLI - never a timeout.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
FIXTURE="$ROOT/live/e2e/tagging-sweep"
WORK="$(mktemp -d)"
FLOCI_PORT="${FLOCI_PORT:-4601}"
FLOCI_NAME="choudoufu-tagging-sweep-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="tagging-sweep-e2e"
DEMO_ROLE="tagging-sweep-e2e-demo"
KEEPER_ROLE="tagging-sweep-e2e-keeper"

# The exact line internal/live/discovery/tagging.go logs once per swept type.
# Matched as a substring, so a change to the count or the CFN type in it does
# not break this; a change to the branch does.
TAGGING_LOG="sweeping aws_iam_role via the Tagging API"

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

awsl() { aws --endpoint-url "$ENDPOINT" --region us-east-1 "$@"; }

# ── 0. tools ────────────────────────────────────────────────────────────────
log "=== 0. tools ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"

if [ -n "${TOFU_BIN:-}" ]; then
  TOFU="$TOFU_BIN"
  [ -x "$TOFU" ] || fail "TOFU_BIN=$TOFU_BIN is not an executable file"
  log "  using TOFU_BIN=$TOFU"
else
  mkdir -p "$WORK/bin"
  TOFU="$WORK/bin/choudoufu"
  ( cd "$ROOT" && go build -o "$TOFU" ./cmd/choudoufu ) || fail "go build ./cmd/choudoufu failed"
  log "  built $TOFU from $ROOT"
fi

# ── 1. floci ────────────────────────────────────────────────────────────────
log "=== 1. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  # Captured, then matched: `curl | grep -q` lets grep close the pipe the
  # instant it matches, which is the SIGPIPE shape live/e2e/run.sh documents.
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"iam"' <<< "$HEALTH" && break
  sleep 2
done
HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
grep -q '"iam"' <<< "$HEALTH" || fail "floci did not come up healthy (iam) at $ENDPOINT"
grep -q '"tagging"' <<< "$HEALTH" \
  || fail "floci does not report a tagging service at all; this whole script is about that service"
log "  healthy, tagging present"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

MAIN="$WORK/estate"
mkdir -p "$MAIN"

# ── 2. stand the estate up ──────────────────────────────────────────────────
log "=== 2. apply both roles ==="
cp "$FIXTURE/declared/main.tf" "$MAIN/main.tf"
( cd "$MAIN" && "$TOFU" init -input=false -no-color >/dev/null ) || fail "init failed"
APPLY_OUT="$(cd "$MAIN" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY_OUT"
  fail "the apply failed"
}
log "  $(grep -E 'Apply complete' <<< "$APPLY_OUT" || echo 'apply finished')"

# Read the roles back through the AWS CLI, never through choudoufu: the
# sweep can only find what is really there, and a missing role would make
# every assertion below fail for the wrong reason.
for role in "$DEMO_ROLE" "$KEEPER_ROLE"; do
  awsl iam get-role --role-name "$role" >/dev/null 2>&1 \
    || fail "$role does not exist on the emulator after the apply"
done
# And the markers have to be visible to the tagging API specifically, which
# is a different question from being visible to iam:ListRoleTags - the
# difference is exactly what lex00/floci#229 fixed.
SWEPT="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'ResourceTagMappingList[].ResourceARN' --output text 2>/dev/null || echo "")"
grep -q "role/$DEMO_ROLE" <<< "$SWEPT" \
  || fail "the emulator's tagging index does not hold $DEMO_ROLE (got: $SWEPT) - see live/floci-capabilities.json's tagging-sweep rows for the pinned digest, and internal/command/tagging_sweep_premise_test.go"
log "  both roles live; the tagging index holds the estate's ARNs"

# ── 3. delete the block, and the state file with it ─────────────────────────
log "=== 3. delete aws_iam_role.demo's block and the state file ==="
cp "$FIXTURE/removed/main.tf" "$MAIN/main.tf"
rm -f "$MAIN/terraform.tfstate" "$MAIN/terraform.tfstate.backup"
[ ! -f "$MAIN/terraform.tfstate" ] || fail "the state file is still there"
grep -q "$DEMO_ROLE" "$MAIN/main.tf" \
  && fail "removed/main.tf still names $DEMO_ROLE; the fixture does not delete what it claims to"
log "  nothing on disk names $DEMO_ROLE any more"

# assert_removal_plan checks the one plan shape both runs must produce:
# exit 0, exactly the demo role destroyed, the keeper untouched.
#   $1 label  $2 exit code  $3 plan output
assert_removal_plan() {
  local label="$1" rc="$2" out="$3"
  [ "$rc" -eq 0 ] || { printf '%s\n' "$out"; fail "$label: live-plan exited $rc"; }
  grep -q "aws_iam_role.demo" <<< "$out" \
    || { printf '%s\n' "$out"; fail "$label: the plan never mentions aws_iam_role.demo, so the sweep did not find it"; }
  grep -qE '^\s*-\s+resource "aws_iam_role" "demo"' <<< "$out" \
    || { printf '%s\n' "$out"; fail "$label: aws_iam_role.demo is mentioned but not proposed for destruction"; }
  grep -q "$DEMO_ROLE" <<< "$out" \
    || { printf '%s\n' "$out"; fail "$label: the plan does not name the live role $DEMO_ROLE"; }
  grep -qE '^\s*-\s+resource "aws_iam_role" "keeper"' <<< "$out" \
    && { printf '%s\n' "$out"; fail "$label: the still-declared keeper role is proposed for destruction"; }
  grep -qE 'Plan: [1-9][0-9]* to add' <<< "$out" \
    && { printf '%s\n' "$out"; fail "$label: the plan proposes creating something"; }
  return 0
}

# ── 4. run A: the default path ──────────────────────────────────────────────
log "=== 4. run A - the default: one GetResources ==="
set +e
A_T0=$(date +%s)
A_OUT="$(cd "$MAIN" && TF_LOG=debug "$TOFU" live-plan -input=false -no-color 2>&1)"
A_RC=$?
A_T1=$(date +%s)
set -e
assert_removal_plan "run A" "$A_RC" "$A_OUT"
grep -q "$TAGGING_LOG" <<< "$A_OUT" \
  || { printf '%s\n' "$A_OUT" | tail -60
       fail "run A: the debug log never says \"$TAGGING_LOG\". The command wiring did not enable the estate-wide tagging sweep, so internal/live/discovery's sweepViaTagging leg is still uncovered end to end - which is the entire point of this script (issue #255)."; }
log "  destroy proposed for aws_iam_role.demo, keeper untouched, sweep went through the Tagging API; $((A_T1 - A_T0))s"

# ── 5. run B: the control ───────────────────────────────────────────────────
# TOFU_LIVE_CLOUDCONTROL=off skips the Cloud Control / tagging block in
# live_plan.go entirely, which is the documented lever for an emulator whose
# own tagging index cannot be trusted.
log "=== 5. run B - TOFU_LIVE_CLOUDCONTROL=off: the per-type sweep ==="
set +e
B_T0=$(date +%s)
B_OUT="$(cd "$MAIN" && TF_LOG=debug TOFU_LIVE_CLOUDCONTROL=off "$TOFU" live-plan -input=false -no-color 2>&1)"
B_RC=$?
B_T1=$(date +%s)
set -e
[ "$B_RC" -eq 0 ] || { printf '%s\n' "$B_OUT"; fail "run B: live-plan exited $B_RC"; }
grep -q "$TAGGING_LOG" <<< "$B_OUT" \
  && { printf '%s\n' "$B_OUT" | tail -40
       fail "run B: the debug log says \"$TAGGING_LOG\" even with TOFU_LIVE_CLOUDCONTROL=off. Run A's assertion is therefore vacuous - that line is printed regardless of the branch - and this script proves nothing about the wiring."; }
# The per-type sweep must still LIST the type - if it did not, run B would be
# a control for nothing, and the comparison below would be between a sweep
# and no sweep rather than between two candidate sources.
grep -q "listing aws_iam_role unfiltered" <<< "$B_OUT" \
  || { printf '%s\n' "$B_OUT" | tail -40
       fail "run B: the per-type sweep never listed aws_iam_role at all, so it is not a control for run A's candidate source."; }
# And it must come up empty-handed, for the reason in this script's header.
grep -qE '^\s*-\s+resource "aws_iam_role" "demo"' <<< "$B_OUT" \
  && { printf '%s\n' "$B_OUT" | tail -40
       fail "run B proposes destroying aws_iam_role.demo through the per-type sweep. That is GOOD NEWS and a stale assertion: the provider's aws_iam_role list resource used not to carry tags (iam:ListRoles returns none, and 6.58.0 issued no GetRole per member), which is why the tagging sweep was the only path that could see an undeclared role. Re-read this script's header, record the provider version that changed it, and relax this assertion."; }
log "  aws_iam_role listed per-type, no ownership marker readable off it, no destroy proposed, no Tagging API line; $((B_T1 - B_T0))s"

# ── 6. what the two runs cost ───────────────────────────────────────────────
# Reported, not asserted. The per-type sweep's cost is a function of how many
# types the admission table holds and how fast the emulator answers, and
# turning that into a threshold would be a flaky test rather than a finding.
# Reported, not asserted, and reported precisely because the obvious
# expectation is wrong. One GetResources call against a local emulator is not
# measurably faster than the per-type sweep's several hundred List calls: at
# this estate's size both runs land within a second or two of each other, and
# the emulator answers an empty list in about a millisecond. Turning that into
# a threshold would be a flaky test, and quoting a speedup would be a number
# nobody measured.
log "=== 6. cost ==="
log "  run A (tagging sweep):  $((A_T1 - A_T0))s"
log "  run B (per-type sweep): $((B_T1 - B_T0))s"

# ── 7. no state file, ever ──────────────────────────────────────────────────
[ ! -f "$MAIN/terraform.tfstate" ] \
  || fail "a state file exists after live-plan - it must never be read or written"

log "=== PASS ==="
