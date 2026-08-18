#!/usr/bin/env bash
set -uo pipefail

# A real third-party estate crossed against a real emulator: issue #274's
# step 6, for .corpus/govuk-aws/terraform/projects/infra-cyber-cloudwatch-to-splunk.
#
# One resource - aws_cloudwatch_log_subscription_filter, GDS's own auth-log
# forwarder into Cyber Security's centralised Splunk pipeline. It passes
# live-check with zero refused sites and, until this script existed, had
# never touched a cloud. It was picked as one of the three smallest untouched
# real corpus estates (issue #274's campaign), smallest-first, to establish
# the method rather than to maximise instance count in one slot.
#
# THE DELTAS, and why both matter even at one instance. The estate declares
# `backend "s3" {}` (#268: a module may declare remote state or a live block,
# never both) AND `version = "~> 3.25"` - an AWS provider constraint old
# enough (mid-2021) to resolve to a release with no list resources at all
# (#269's shape). A generated fixture would never carry either: estate-gen
# emits `required_providers` unconstrained and declares no backend. Real
# estates carry real history, and a config this small still cost two of the
# four onboarding-delta classes #274 catalogued.
#
# THE TYPE. aws_cloudwatch_log_subscription_filter has no tags argument at
# all (live/survey-full.json: path "client-named", taggable false). Its
# identity re-derives from the declaration on every run - log_group_name and
# filter name, joined the way the provider's own import syntax joins them -
# and needs no carrier. The estate also assumes auth-log already exists,
# which a real onboarding run would find already true; here it is seeded
# through the AWS CLI once, never through choudoufu.
#
#   bash live/e2e/corpus-cloudwatch-splunk/run.sh
#
# Needs Docker and the AWS CLI. .corpus is read, never written: the estate is
# copied out to a temp directory first, same as every other corpus crossing.
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4698, clear of every
#                other live/e2e fixture's port).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        set to 1 to corrupt the expected identity string before
#                step 5, proving the identity assertion is load-bearing.
#
# Exit codes: 0 on a real pass, non-zero on a real failure. Every assertion
# reads command output, an exit code, or the emulator's own answer through
# the AWS CLI.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CORPUS_DIR="${CORPUS_DIR:-$ROOT/.corpus}"
SRC="$CORPUS_DIR/govuk-aws/terraform/projects/infra-cyber-cloudwatch-to-splunk"
WORK="$(mktemp -d)"
EST="$WORK/estate"
FLOCI_PORT="${FLOCI_PORT:-4698}"
FLOCI_NAME="choudoufu-corpus-cw-splunk-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="cyber-cloudwatch-splunk-crossing"
REGION="eu-west-1"
LOG_GROUP="auth-log"
FILTER_NAME="log_subscription_python"

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }

# ── 0. tools and corpus ─────────────────────────────────────────────────────
log "=== 0. tools and corpus ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
[ -d "$SRC" ] || fail "$SRC is missing - run 'just corpus-fetch' first"

if [ -n "${TOFU_BIN:-}" ]; then
  TOFU="$TOFU_BIN"
  [ -x "$TOFU" ] || fail "TOFU_BIN=$TOFU_BIN is not an executable file"
  log "  using TOFU_BIN=$TOFU"
else
  mkdir -p "$WORK/bin"
  TOFU="$WORK/bin/choudoufu"
  ( cd "$ROOT" && env -u PWD go build -o "$TOFU" ./cmd/choudoufu ) || fail "go build ./cmd/choudoufu failed"
  log "  built $TOFU"
fi

mkdir -p "$EST"
cp "$SRC"/*.tf "$EST/"
[ -f "$EST/main.tf" ] || fail "the estate copy is missing main.tf"
log "  estate copied out of .corpus into $EST"

# ── 1. the deltas ────────────────────────────────────────────────────────────
log "=== 1. onboarding deltas ==="
perl -0pi -e 's/terraform \{\n  backend "s3" \{\}\n  required_version = "~> 1\.1"\n\n  required_providers \{\n    aws = \{\n      source  = "hashicorp\/aws"\n      version = "~> 3\.25"\n    \}\n  \}\n\}/terraform {\n  # DELTA 1: was `backend "s3" {}` (#268).\n  required_version = "~> 1.1"\n\n  required_providers {\n    aws = {\n      source  = "hashicorp\/aws"\n      # DELTA 2: was `version = "~> 3.25"`, which resolves to a release with\n      # no list resources (#269-shape).\n      version = "= 6.58.0"\n    }\n  }\n\n  live {\n    estate = "'"$ESTATE"'"\n  }\n}/' "$EST/main.tf"
grep -q "estate = \"$ESTATE\"" "$EST/main.tf" || fail "DELTA 1+2 did not match main.tf - the corpus pin has moved"
log "  DELTA 1  backend block removed, live block added   (#268)"
log "  DELTA 2  provider pinned = 6.58.0                  (#269-shape)"

perl -0pi -e 's/(provider "aws" \{\n  region = var\.aws_region\n)\}/$1\n  access_key                   = "test"\n  secret_key                   = "test"\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  s3_use_path_style            = true\n}/' "$EST/main.tf"
grep -q 's3_use_path_style' "$EST/main.tf" || fail "the emulator delta did not match main.tf - the corpus pin has moved"
log "  DELTA 3  emulator flags on the provider             (emulator)"

cat > "$EST/crossing.auto.tfvars" <<EOF
splunk_destination_v2_arn = "arn:aws:logs:${REGION}:000000000000:destination:crossing-splunk"
EOF
log "  DELTA 4  a value for splunk_destination_v2_arn      (onboarding)"

# ── 2. floci ────────────────────────────────────────────────────────────────
log "=== 2. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"logs"' <<< "$HEALTH" && break
  sleep 2
done
grep -q '"logs"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (logs) at $ENDPOINT"
log "  healthy"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION"

# DELTA 5, stand-up only: the estate assumes auth-log already exists, the way
# a real onboarding run would find it. Seeded once, never through choudoufu.
awsl logs create-log-group --log-group-name "$LOG_GROUP" >/dev/null || fail "could not seed the log group"
log "  DELTA 5  log group '$LOG_GROUP' seeded               (stand-up only)"

# ── 3. stand the estate up ──────────────────────────────────────────────────
log "=== 3. init and apply: 1 instance ==="
( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null ) || fail "init failed"
APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY_OUT" | tail -40
  fail "the apply failed"
}
grep -qE 'Apply complete! Resources: 1 added' <<< "$APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY_OUT"; fail "the apply did not create exactly 1 resource"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY_OUT")"

# Read the filter back through the AWS CLI, never through choudoufu.
LIVE_ARN="$(awsl logs describe-subscription-filters --log-group-name "$LOG_GROUP" \
  --query "subscriptionFilters[?filterName=='$FILTER_NAME'].destinationArn | [0]" --output text)"
[ -n "$LIVE_ARN" ] && [ "$LIVE_ARN" != "None" ] || fail "could not find the subscription filter through the AWS CLI"
log "  the filter lives: $LOG_GROUP|$FILTER_NAME -> $LIVE_ARN"

# ── 4. no state file, ever ──────────────────────────────────────────────────
rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"
[ ! -f "$EST/terraform.tfstate" ] || fail "the state file is still there"
log "=== 4. state file deleted ==="

# ── 5. THE VALUE, not the verdict ───────────────────────────────────────────
log "=== 5. live-plan, and the rendered identity read out of the run ==="
plan_into() {
  ( cd "$EST" && TF_LOG=trace "$TOFU" live-plan -input=false -no-color )
}
PLAN_OUT="$(plan_into 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -40; fail "live-plan exited $PLAN_RC"; }
[ ! -f "$EST/terraform.tfstate" ] || fail "live-plan wrote a state file"
grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$PLAN_OUT" \
  && { grep -E '^  # .+ will be' <<< "$PLAN_OUT"; fail "the plan proposes a resource change"; }
grep -qE '^Foreign resources: (none|nothing was swept)' <<< "$PLAN_OUT" \
  || { grep -E '^Foreign resources:' <<< "$PLAN_OUT"; fail "the plan reports foreign resources"; }
log "  no resource change proposed; nothing foreign"

WANT="${LOG_GROUP}|${FILTER_NAME}"
if [ "${BREAK:-}" = "1" ]; then
  WANT="wrong-log-group|${FILTER_NAME}"
  log "  BREAK=1: expecting \"$WANT\", the same filter name, wrong log group."
  log "           The plan above stayed empty. This step must fail."
fi
grep -qF "from import identity \"$WANT\"" <<< "$PLAN_OUT" || {
  grep -oE 'from import identity "[^"]*"' <<< "$PLAN_OUT" | sort -u
  fail "no instance materialized from import identity \"$WANT\". The identities the run actually rendered are listed above."
}
GOT_N="$(grep -oE 'from import identity "[^"]*"' <<< "$PLAN_OUT" | sort -u | wc -l | tr -d ' ')"
[ "$GOT_N" = "1" ] || fail "the run materialized $GOT_N distinct identities, expected 1"
log "  the rendered identity asserted, and no other"

# ── 6. and it converges ─────────────────────────────────────────────────────
log "=== 6. the next run proposes nothing, and applying it adds nothing ==="
PLAN2_OUT="$(plan_into 2>&1)"; PLAN2_RC=$?
[ "$PLAN2_RC" -eq 0 ] || { printf '%s\n' "$PLAN2_OUT" | tail -30; fail "the second live-plan exited $PLAN2_RC"; }
grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$PLAN2_OUT" \
  && { grep -E '^  # .+ will be' <<< "$PLAN2_OUT"; fail "the second plan proposes a resource change, so the run does not converge"; }

APPLY2_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY2_OUT" | tail -30; fail "the second apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY2_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY2_OUT"; fail "the second apply was not a no-op"; }
STILL="$(awsl logs describe-subscription-filters --log-group-name "$LOG_GROUP" \
  --query "length(subscriptionFilters[?filterName=='$FILTER_NAME'])" --output text)"
[ "$STILL" = "1" ] || fail "expected exactly 1 filter named $FILTER_NAME afterward, got $STILL"
[ ! -f "$EST/terraform.tfstate" ] || fail "a state file exists after the second run"
log "  converged: nothing proposed, nothing added, still 1 filter, still no state file"

log ""
log "=== PASS ==="
log ""
log "GDS's own auth-log Splunk forwarder, applied against an emulator, lost"
log "its state file, and replanned empty twice. The rendered identity"
log "(log_group_name|filter_name, the provider's own import syntax) was"
log "checked against CloudWatch Logs' own answer. Run again with BREAK=1:"
log "everything above step 5 still passes and step 5 goes red."
