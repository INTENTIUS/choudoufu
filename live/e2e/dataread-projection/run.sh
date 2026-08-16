#!/usr/bin/env bash
set -euo pipefail

# GitHub issue #193's read side, end to end against a real emulator.
#
# The claim under test, in one sentence: a data source whose argument reads a
# managed resource attribute that the resource's OWN BLOCK SETS is readable
# before the plan, and the value it reads is the live one.
#
# Why this needs a cloud at all. The classification half (Analyze) is offline
# by design - that is what keeps tools/corpus-gen generable over 250
# third-party configurations with no AWS account behind them - so it can be
# tested offline and is, in internal/live/dataread. The read half cannot be:
# it exists precisely to turn a projected argument into a provider call, and
# nothing offline can show that the call went out with the right argument and
# came back with the live answer. That gap is why this script exists rather
# than another unit test.
#
# The fixture is built so a static shortcut cannot pass it. Phase 1 creates
# an SSM parameter whose value the configuration states. Between the phases
# this script OVERWRITES that value out of band through the AWS CLI. Phase 2
# then names a log group after the parameter's value as read back from the
# cloud, so the only way to get the expected name is to have really read it.
# A run that resolved the value from configuration would name the log group
# after the phase-1 string and fail here.
#
#   bash live/e2e/dataread-projection/run.sh
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4599, off run.sh's 4566
#                so both harnesses can run at once).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image, the same single source of truth
#                live/e2e/run.sh uses.
#
# Exit codes: 0 on a real pass, non-zero on a real failure. Every assertion
# reads actual command output, an exit code, or the emulator's own answer
# through the AWS CLI - never a timeout.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
FIXTURE="$ROOT/live/e2e/dataread-projection"
WORK="$(mktemp -d)"
FLOCI_PORT="${FLOCI_PORT:-4599}"
FLOCI_NAME="choudoufu-dataread-projection-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="dataread-projection-e2e"
PARAM="/dataread-projection/seed"
CONFIG_VALUE="config-only-value"
LIVE_VALUE="live-only-8842"
WANT_LOG_GROUP="/dataread-projection/${LIVE_VALUE}"

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
  grep -q '"ssm"' <<< "$HEALTH" && break
  sleep 2
done
HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
grep -q '"ssm"' <<< "$HEALTH" || fail "floci did not come up healthy (ssm) at $ENDPOINT"
log "  healthy"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

MAIN="$WORK/estate"
mkdir -p "$MAIN"

# ── 2. phase 1: stand the seed up ───────────────────────────────────────────
log "=== 2. phase 1 — apply the seed parameter ==="
cp "$FIXTURE/phase1/main.tf" "$MAIN/main.tf"
( cd "$MAIN" && "$TOFU" init -input=false -no-color >/dev/null ) || fail "init failed"
APPLY_OUT="$(cd "$MAIN" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY_OUT"
  fail "phase 1 apply failed"
}
grep -q "1 added" <<< "$APPLY_OUT" || {
  printf '%s\n' "$APPLY_OUT"
  fail "phase 1 apply did not report 1 added"
}
log "  applied: $(grep -E 'Apply complete' <<< "$APPLY_OUT")"

# ── 3. move the live value away from the configured one ─────────────────────
# This is what makes the phase-2 assertion mean something: from here on, the
# configuration and the cloud disagree about the parameter's value, and only
# a real read can produce the live one.
log "=== 3. overwrite the parameter's value out of band ==="
awsl ssm put-parameter --name "$PARAM" --type String --value "$LIVE_VALUE" --overwrite >/dev/null \
  || fail "could not overwrite $PARAM through the AWS CLI"
READ_BACK="$(awsl ssm get-parameter --name "$PARAM" --query 'Parameter.Value' --output text)"
[ "$READ_BACK" = "$LIVE_VALUE" ] || fail "the parameter reads back as '$READ_BACK', want '$LIVE_VALUE'"
log "  $PARAM is now '$LIVE_VALUE' live, while the configuration still says '$CONFIG_VALUE'"

# Floci's ssm:PutParameter drops a parameter's tag set (chant/test/
# floci-gaps.md #10, the same gap live/e2e/run.sh's receipt-adoption step
# works around) - both the inline set an apply creates it with and,
# empirically, the set an --overwrite writes over. So the ownership markers
# go on by hand here, AFTER the overwrite rather than before it: adoption
# exactly as the docs describe it, a tag written with your own cloud tools.
TAGS_NOW="$(awsl ssm list-tags-for-resource --resource-type Parameter --resource-id "$PARAM" \
  --query 'TagList[?Key==`tofu-estate`]|[0].Value' --output text 2>/dev/null || echo None)"
if [ "$TAGS_NOW" != "$ESTATE" ]; then
  awsl ssm add-tags-to-resource --resource-type Parameter --resource-id "$PARAM" \
    --tags "Key=tofu-estate,Value=$ESTATE" "Key=tofu-address,Value=aws_ssm_parameter.seed" >/dev/null \
    || fail "could not write the ownership markers onto $PARAM"
  log "  wrote tofu-estate/tofu-address onto $PARAM (floci-gaps #10)"
fi
MARKER="$(awsl ssm list-tags-for-resource --resource-type Parameter --resource-id "$PARAM" \
  --query 'TagList[?Key==`tofu-estate`]|[0].Value' --output text 2>/dev/null || echo None)"
[ "$MARKER" = "$ESTATE" ] || fail "$PARAM does not carry tofu-estate=$ESTATE after adoption (got '$MARKER')"

# ── 4. phase 2: the run under test ──────────────────────────────────────────
log "=== 4. phase 2 — plan with the projected data read ==="
cp "$FIXTURE/phase2/main.tf" "$MAIN/main.tf"
rm -f "$MAIN/terraform.tfstate" "$MAIN/terraform.tfstate.backup"
set +e
PLAN_OUT="$(cd "$MAIN" && "$TOFU" plan -input=false -no-color 2>&1)"
PLAN_RC=$?
set -e
if [ "$PLAN_RC" != "0" ]; then
  printf '%s\n' "$PLAN_OUT"
  fail "phase 2 plan failed (exit $PLAN_RC) - the projected managed reference did not resolve"
fi
grep -q "aws_cloudwatch_log_group.derived" <<< "$PLAN_OUT" || {
  printf '%s\n' "$PLAN_OUT"
  fail "the plan does not mention aws_cloudwatch_log_group.derived at all"
}
grep -q "$WANT_LOG_GROUP" <<< "$PLAN_OUT" || {
  printf '%s\n' "$PLAN_OUT"
  fail "the plan does not name the log group $WANT_LOG_GROUP - the value did not come from the live read"
}
grep -q "/dataread-projection/${CONFIG_VALUE}" <<< "$PLAN_OUT" && {
  printf '%s\n' "$PLAN_OUT"
  fail "the plan names the log group after the CONFIGURED parameter value, so the read never happened"
}
log "  the plan names $WANT_LOG_GROUP, which exists only in the cloud"

# ── 5. apply it, and read the result back with the AWS CLI ──────────────────
# choudoufu is not trusted to grade itself: the log group is read straight
# off the emulator.
log "=== 5. apply, then read the log group back through the AWS CLI ==="
APPLY2_OUT="$(cd "$MAIN" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY2_OUT"
  fail "phase 2 apply failed"
}
FOUND="$(awsl logs describe-log-groups --log-group-name-prefix "/dataread-projection/" \
  --query 'logGroups[].logGroupName' --output text 2>/dev/null || echo "")"
grep -q "$WANT_LOG_GROUP" <<< "$FOUND" || {
  printf 'log groups present: %s\n' "$FOUND"
  fail "no log group named $WANT_LOG_GROUP exists on the emulator after the apply"
}
log "  $WANT_LOG_GROUP exists on the emulator"

# ── 6. a clean re-plan, from markers alone ──────────────────────────────────
# The read is not cached: every run reads live. So a second plan has to do
# the whole projection and read again and still propose nothing.
log "=== 6. clean re-plan ==="
rm -f "$MAIN/terraform.tfstate" "$MAIN/terraform.tfstate.backup"
set +e
REPLAN_OUT="$(cd "$MAIN" && "$TOFU" plan -input=false -no-color -detailed-exitcode 2>&1)"
REPLAN_RC=$?
set -e
grep -q "aws_cloudwatch_log_group.derived" <<< "$REPLAN_OUT" && {
  printf '%s\n' "$REPLAN_OUT"
  fail "the re-plan proposes a change to aws_cloudwatch_log_group.derived; it was created identically a moment ago"
}
log "  the re-plan proposes nothing for the derived log group (exit $REPLAN_RC; the seed's own out-of-band value drift is expected and not asserted on)"

# ── 7. the other side of the rule, live ─────────────────────────────────────
# aws_ssm_parameter.seed.arn is assigned by the provider and appears nowhere
# in the block, so there is nothing to project. This must refuse, and the
# refusal must name the managed resource rather than the run quietly reading
# something else.
log "=== 7. an unset attribute must still refuse ==="
cp "$FIXTURE/refused/main.tf" "$MAIN/main.tf"
set +e
REFUSED_OUT="$(cd "$MAIN" && "$TOFU" plan -input=false -no-color 2>&1)"
REFUSED_RC=$?
set -e
if [ "$REFUSED_RC" = "0" ]; then
  printf '%s\n' "$REFUSED_OUT"
  fail "the plan succeeded while reading aws_ssm_parameter.seed.arn, which the configuration never sets"
fi
grep -q "aws_ssm_parameter.seed" <<< "$REFUSED_OUT" || {
  printf '%s\n' "$REFUSED_OUT"
  fail "the refusal does not name aws_ssm_parameter.seed"
}
grep -q "does not have an attribute named" <<< "$REFUSED_OUT" && {
  printf '%s\n' "$REFUSED_OUT"
  fail "a raw HCL attribute error leaked out instead of the refusal this path owns"
}
log "  refused, naming aws_ssm_parameter.seed, with no HCL attribute error leaking"

log "=== PASS ==="
