#!/usr/bin/env bash
set -uo pipefail

# A real third-party estate crossed against a real emulator: issue #274's
# step 6, for .corpus/mastino/prod-eu-west/services/crossref-agent.
#
# Four resources - aws_cloudwatch_event_rule, aws_cloudwatch_event_target,
# aws_lambda_function and aws_lambda_permission - DataCite's own daily cron
# job that pushes metadata updates to Crossref. It passes live-check with
# zero refused sites and, until this script existed, had never touched a
# cloud. Picked as one of #274's smallest untouched real corpus estates,
# smallest-first, and the first Lambda-based estate this campaign crosses.
#
# THE DELTAS.
#
#   1. `cloud { organization = "datacite-ng" ... }` (#268), replaced with a
#      live block.
#   2. Emulator flags on the provider block.
#   3. FOUR seeded reads. The estate takes its execution role, its subnets
#      and its security group from data sources over a Lambda IAM role and a
#      VPC that already exist in DataCite's real account. All four are
#      seeded through the AWS CLI (ec2 + iam), never through choudoufu, and
#      the data blocks in the estate's own input.tf are kept exactly as
#      written - only the var values naming which VPC objects to read point
#      at what this run seeded.
#   4. A value for var.token (no default in the estate; not identity-bearing).
#
# NO PROVIDER-VERSION OVERRIDE. `version = "~> 5"` resolves to 5.100.0, the
# release #269 documented as carrying no list resources at all - but every
# type this estate declares is client-named (see THE TYPES below), so no
# instance ever calls a list operation and 5.100.0's gap never bites.
# refusal-probe's -schemas version-skew check agrees: this entry carries no
# version_skew.
#
# THE TYPES, all four literal and client-named
# (internal/live/identity/table_generated.go), so every rendered identity is
# reconstructible from the estate's own configuration with no discovery:
#
#   aws_cloudwatch_event_rule    default/crossref-agent
#   aws_cloudwatch_event_target  default/crossref-agent/crossref-agent
#   aws_lambda_function          crossref-agent
#   aws_lambda_permission        crossref-agent/AllowExecutionFromCloudWatch
#
# THE RUNTIME. The estate's own aws_lambda_function block sets
# `runtime = "nodejs14.x"`, a runtime AWS has since deprecated for new
# function creates. This script does not edit it - DataCite's own estate is
# applied byte for byte - and floci's CreateFunction does not enforce the
# deprecation, so it applies here. If floci starts enforcing it, this script
# will fail at step 3 with AWS's own rejection, not silently.
#
#   bash live/e2e/corpus-crossref-agent/run.sh
#
# Needs Docker and the AWS CLI. .corpus is read, never written: the estate
# (and its runner zip) is copied out to a temp directory first, same as
# every other corpus crossing.
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4701, clear of every
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
SRC="$CORPUS_DIR/mastino/prod-eu-west/services/crossref-agent"
WORK="$(mktemp -d)"
EST="$WORK/estate"
FLOCI_PORT="${FLOCI_PORT:-4701}"
FLOCI_NAME="choudoufu-corpus-crossref-agent-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="mastino-crossref-agent-crossing"
REGION="eu-west-1"
FUNCTION_NAME="crossref-agent"
RULE_NAME="crossref-agent"

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
cp "$SRC/crossref-agent_runner.js.zip" "$EST/"
[ -f "$EST/main.tf" ] || fail "the estate copy is missing main.tf"
[ -f "$EST/crossref-agent_runner.js.zip" ] || fail "the estate copy is missing the runner zip"
log "  estate + runner zip copied out of .corpus into $EST"

# ── 1. the deltas ─────────────────────────────────────────────────────────
log "=== 1. onboarding deltas ==="
perl -0pi -e 's/terraform \{\n  required_providers \{\n    aws = \{\n      source = "hashicorp\/aws"\n      version = "~> 5"\n    \}\n  \}\n\n  required_version = ">= 1\.6"\n\n  cloud \{\n    organization = "datacite-ng"\n\n    workspaces \{\n      name = "prod-eu-west-services-crossref-agent"\n    \}\n  \}\n\}/terraform {\n  required_providers {\n    aws = {\n      source  = "hashicorp\/aws"\n      version = "~> 5"\n    }\n  }\n\n  required_version = ">= 1.6"\n\n  # DELTA 1: was a Terraform Cloud block (#268).\n  live {\n    estate = "'"$ESTATE"'"\n  }\n}/' "$EST/terraform.tf"
grep -q "estate = \"$ESTATE\"" "$EST/terraform.tf" || fail "DELTA 1 did not match terraform.tf - the corpus pin has moved"
log "  DELTA 1  cloud block removed, live block added              (#268)"

perl -0pi -e 's/(provider "aws" \{\n  access_key = var\.access_key\n  secret_key = var\.secret_key\n  region     = var\.region\n)\}/$1\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  s3_use_path_style            = true\n}/' "$EST/input.tf"
grep -q 's3_use_path_style' "$EST/input.tf" || fail "DELTA 2 did not match input.tf - the corpus pin has moved"
log "  DELTA 2  emulator flags on the provider                     (emulator)"

# ── 2. floci ────────────────────────────────────────────────────────────────
log "=== 2. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"lambda"' <<< "$HEALTH" && break
  sleep 2
done
grep -q '"lambda"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (lambda) at $ENDPOINT"
log "  healthy"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION"

# DELTA 3, four seeded reads: a Lambda execution role, a VPC, two subnets in
# it and a security group. The estate's own data blocks are kept as written;
# only the var values naming which objects to read are seeded here.
ROLE_ARN="$(awsl iam create-role --role-name lambda \
  --assume-role-policy-document '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}' \
  --query 'Role.Arn' --output text)"
[ -n "$ROLE_ARN" ] || fail "could not seed the lambda execution role"

VPC_ID="$(awsl ec2 create-vpc --cidr-block 10.90.0.0/16 --query 'Vpc.VpcId' --output text)"
[ -n "$VPC_ID" ] || fail "could not seed the VPC"
SUBNET_PRIVATE_ID="$(awsl ec2 create-subnet --vpc-id "$VPC_ID" --cidr-block 10.90.1.0/24 \
  --availability-zone "${REGION}a" --query 'Subnet.SubnetId' --output text)"
[ -n "$SUBNET_PRIVATE_ID" ] || fail "could not seed the private subnet"
SUBNET_ALT_ID="$(awsl ec2 create-subnet --vpc-id "$VPC_ID" --cidr-block 10.90.2.0/24 \
  --availability-zone "${REGION}b" --query 'Subnet.SubnetId' --output text)"
[ -n "$SUBNET_ALT_ID" ] || fail "could not seed the alt subnet"
SG_ID="$(awsl ec2 create-security-group --vpc-id "$VPC_ID" --group-name datacite-private \
  --description "datacite-private" --query 'GroupId' --output text)"
[ -n "$SG_ID" ] || fail "could not seed the security group"
log "  DELTA 3  lambda role + VPC + 2 subnets + 1 SG seeded         (seeded reads)"

cat > "$EST/crossing.auto.tfvars" <<EOF
access_key = "test"
secret_key = "test"
security_group_id = "$SG_ID"
subnet_datacite-private_id = "$SUBNET_PRIVATE_ID"
subnet_datacite-alt_id = "$SUBNET_ALT_ID"
token = "test-token"
EOF
log "  DELTA 4  var values for the seeded objects + var.token       (onboarding)"

# ── 3. stand the estate up ──────────────────────────────────────────────────
log "=== 3. init and apply: 4 instances ==="
( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null ) || {
  ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "init failed"; }
APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY_OUT" | grep -E '^Error|^│' | head -30
  fail "the apply failed"
}
grep -qE 'Apply complete! Resources: 4 added' <<< "$APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY_OUT"; fail "the apply did not create exactly 4 resources"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY_OUT")"

# Read the function back through the AWS CLI, never through choudoufu.
LIVE_ARN="$(awsl lambda get-function --function-name "$FUNCTION_NAME" --query 'Configuration.FunctionArn' --output text 2>/dev/null || true)"
[ -n "$LIVE_ARN" ] && [ "$LIVE_ARN" != "None" ] || fail "could not find the Lambda function through the AWS CLI"
log "  the function lives: $LIVE_ARN"
RULE_LIVE="$(awsl events describe-rule --name "$RULE_NAME" --query 'Name' --output text 2>/dev/null || true)"
[ "$RULE_LIVE" = "$RULE_NAME" ] || fail "could not find the CloudWatch event rule through the AWS CLI"
log "  the rule lives: $RULE_LIVE"

# ── 4. no state file, ever ──────────────────────────────────────────────────
rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"
[ ! -f "$EST/terraform.tfstate" ] || fail "the state file is still there"
log "=== 4. state file deleted ==="

# ── 5. THE VALUE, not the verdict ───────────────────────────────────────────
log "=== 5. live-plan, and the rendered identities read out of the run ==="
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

WANT_RULE='default/crossref-agent'
WANT_TARGET='default/crossref-agent/crossref-agent'
WANT_FUNC='crossref-agent'
WANT_PERM='crossref-agent/AllowExecutionFromCloudWatch'
if [ "${BREAK:-}" = "1" ]; then
  WANT_FUNC="wrong-function-name"
  log "  BREAK=1: expecting \"$WANT_FUNC\" for the function, the wrong name."
  log "           The plan above stayed empty. This step must fail."
fi
for WANT in "$WANT_RULE" "$WANT_TARGET" "$WANT_FUNC" "$WANT_PERM"; do
  grep -qF "from import identity \"$WANT\"" <<< "$PLAN_OUT" || {
    grep -oE 'from import identity "[^"]*"' <<< "$PLAN_OUT" | sort -u
    fail "no instance materialized from import identity \"$WANT\". The identities the run actually rendered are listed above."
  }
done
GOT_N="$(grep -oE 'from import identity "[^"]*"' <<< "$PLAN_OUT" | sort -u | wc -l | tr -d ' ')"
[ "$GOT_N" = "4" ] || fail "the run materialized $GOT_N distinct identities, expected 4"
log "  all 4 rendered identities asserted, and no others"

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
STILL="$(awsl lambda get-function --function-name "$FUNCTION_NAME" --query 'Configuration.FunctionArn' --output text 2>/dev/null || echo None)"
[ "$STILL" = "$LIVE_ARN" ] || fail "expected the function to still be $LIVE_ARN afterward, got $STILL"
[ ! -f "$EST/terraform.tfstate" ] || fail "a state file exists after the second run"
log "  converged: nothing proposed, nothing added, all 4 resources still there, still no state file"

log ""
log "=== PASS ==="
log ""
log "DataCite's own crossref-agent cron job - a CloudWatch event rule and"
log "target driving a Lambda function, with its EventBridge invoke"
log "permission - applied against an emulator, lost its state file, and"
log "replanned empty twice. All 4 rendered identities were checked against"
log "the emulator's own answer. Run again with BREAK=1: everything above"
log "step 5 still passes and step 5 goes red."
