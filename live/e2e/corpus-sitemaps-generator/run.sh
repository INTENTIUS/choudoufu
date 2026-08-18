#!/usr/bin/env bash
set -uo pipefail

# A real third-party estate attempted against a real emulator: issue #274's
# step 6, for .corpus/mastino/prod-eu-west/services/sitemaps-generator.
#
# Three resources - aws_s3_bucket.akita, aws_cloudwatch_log_group and
# aws_ecs_task_definition, all named "sitemaps-generator" - DataCite's own
# ECS-driven sitemap generator. It passes live-check with zero refused
# sites. Picked as one of #274's smallest untouched real corpus estates,
# smallest-first.
#
# THIS DOES NOT CROSS YET. Filed as #298 with a minimal repro rather than
# forced into a workaround; internal/live/discovery is off limits this
# session. Steps 0-4 below are real and pass: the estate applies cleanly
# under its own declared `version = "~> 5"` (which resolves to 5.100.0, the
# release #269 documented as carrying no list resources at all), and all
# three resources are confirmed live through the AWS CLI. Step 5 is where it
# stops.
#
# THE BLOCKER (#298). Deleting the state file and running live-plan fails:
#
#   Error: Expected ID in format of
#   arn:PARTITION:ecs:REGION:ACCOUNTID:task-definition/FAMILY:REVISION and
#   provided: sitemaps-generator:1
#
#   Error: Cannot import for projection
#
# TF_LOG=trace shows discovery finding the task definition correctly -
# "listing aws_ecs_task_definition via Cloud Control (AWS::ECS::TaskDefinition),
# 1 resources" - and the live object carries the right tofu-address tag,
# confirmed independently through the AWS CLI. But the identity actually
# handed to ImportResourceState is the literal "family:revision" join from
# the row's IdentityAttrs, not the ARN Cloud Control's own listing carries -
# and this provider's ImportResourceState for this type specifically demands
# the ARN form.
#
# WHY THIS TYPE AND NOT ANALYTICS-WORKER. live/e2e/corpus-ecs-taskdef
# crosses this exact resource type successfully, but only after pinning
# `= 6.58.0`. Under that pin, discovery uses the provider's native
# ListResource RPC, whose identity object carries family/revision AND
# resolves to a real ARN for import. 5.100.0 has no ListResource support at
# all (#269), so discovery falls back to Cloud Control listing here instead
# - which finds the object fine but, on this evidence, does not carry an
# ARN through to the import call the same way the native ListResource path
# does.
#
# PINNING TO 6.58.0 (THE #269 WORKAROUND) DOES NOT FIX THIS ESTATE EITHER.
# It clears the aws_ecs_task_definition step, but then aws_s3_bucket.akita's
# tag read under that provider version calls S3 Control's
# ListTagsForResource, which the AWS SDK addresses via an account-ID-prefixed
# virtual-hosted-style hostname (000000000000.127.0.0.1) that does not
# resolve locally - confirmed not fixable by adding
# `endpoints { s3control = "..." }` to the provider block; the SDK
# constructs the account-prefixed host regardless of the endpoint override.
# This is a floci capability gap (S3 Control virtual-hosted addressing), not
# a choudoufu marker-path bug, and is out of scope for a fork change here -
# see #298 for the full detail on both findings.
#
#   bash live/e2e/corpus-sitemaps-generator/run.sh
#
# Needs Docker and the AWS CLI. .corpus is read, never written: the estate
# is copied out to a temp directory first, same as every other corpus
# crossing.
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4705, clear of every
#                other live/e2e fixture's port).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#
# Exit codes: 0 only once #298 is fixed and this crosses for real - this
# script does not fake a pass. Today it exits non-zero at step 5, and
# distinguishes the known, filed #298 signature from anything else so a
# regression to a DIFFERENT failure is still visible as a difference.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CORPUS_DIR="${CORPUS_DIR:-$ROOT/.corpus}"
SRC="$CORPUS_DIR/mastino/prod-eu-west/services/sitemaps-generator"
WORK="$(mktemp -d)"
EST="$WORK/estate"
FLOCI_PORT="${FLOCI_PORT:-4705}"
FLOCI_NAME="choudoufu-corpus-sitemaps-generator-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="mastino-sitemaps-generator-crossing"
REGION="eu-west-1"
BUCKET="commons.datacite.org"
FAMILY="sitemaps-generator"
LOG_GROUP="/ecs/sitemaps-generator"

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
cp "$SRC"/*.tf "$SRC/s3_public_read.json" "$SRC/sitemaps-generator.json" "$EST/"
[ -f "$EST/main.tf" ] || fail "the estate copy is missing main.tf"
log "  estate copied out of .corpus into $EST"

# ── 1. the one delta ─────────────────────────────────────────────────────────
log "=== 1. the one onboarding delta ==="
perl -0pi -e 's/terraform \{\n  required_providers \{\n    aws = \{\n      source = "hashicorp\/aws"\n      version = "~> 5"\n    \}\n  \}\n\n  required_version = ">= 1\.6"\n\n  cloud \{\n    organization = "datacite-ng"\n\n    workspaces \{\n      name = "prod-eu-west-services-sitemaps-generator"\n    \}\n  \}\n\}/terraform {\n  required_providers {\n    aws = {\n      source  = "hashicorp\/aws"\n      version = "~> 5"\n    }\n  }\n\n  required_version = ">= 1.6"\n\n  # DELTA: was a Terraform Cloud block (#268). No provider-version override\n  # here - see the header on why re-pinning to 6.58.0 does not help either.\n  live {\n    estate = "'"$ESTATE"'"\n  }\n}/' "$EST/terraform.tf"
grep -q "estate = \"$ESTATE\"" "$EST/terraform.tf" || fail "the delta did not match terraform.tf - the corpus pin has moved"
log "  DELTA  cloud block removed, live block added                (#268)"

perl -0pi -e 's/(provider "aws" \{\n  access_key = var\.access_key\n  secret_key = var\.secret_key\n  region     = var\.region\n)\}/$1\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  s3_use_path_style            = true\n}/' "$EST/input.tf"
grep -q 's3_use_path_style' "$EST/input.tf" || fail "the emulator delta did not match input.tf - the corpus pin has moved"
log "  DELTA  emulator flags on the provider                        (emulator)"

# ── 2. floci ────────────────────────────────────────────────────────────────
log "=== 2. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"ecs"' <<< "$HEALTH" && break
  sleep 2
done
grep -q '"ecs"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (ecs) at $ENDPOINT"
log "  healthy"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION"

# DELTA (seeded reads): six data sources OpenTofu evaluates unconditionally,
# feeding the estate's VPC S3 endpoint policy and its ECS task definition.
VPC_ID="$(awsl ec2 create-vpc --cidr-block 10.96.0.0/16 --query 'Vpc.VpcId' --output text)"
[ -n "$VPC_ID" ] || fail "could not seed the VPC"
VPCE_ID="$(awsl ec2 create-vpc-endpoint --vpc-id "$VPC_ID" --service-name com.amazonaws.eu-west-1.s3 \
  --query 'VpcEndpoint.VpcEndpointId' --output text)"
[ -n "$VPCE_ID" ] || fail "could not seed the VPC S3 endpoint"
awsl ecs create-cluster --cluster-name default >/dev/null || fail "could not seed the ECS cluster"
awsl iam create-role --role-name ecsTaskExecutionRole \
  --assume-role-policy-document '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ecs-tasks.amazonaws.com"},"Action":"sts:AssumeRole"}]}' \
  >/dev/null || fail "could not seed the ecsTaskExecutionRole"
awsl iam create-role --role-name ecs_events \
  --assume-role-policy-document '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"events.amazonaws.com"},"Action":"sts:AssumeRole"}]}' \
  >/dev/null || fail "could not seed the ecs_events role"
SG_ID="$(awsl ec2 create-security-group --vpc-id "$VPC_ID" --group-name datacite-private \
  --description "datacite-private" --query 'GroupId' --output text)"
[ -n "$SG_ID" ] || fail "could not seed the security group"
SUBNET_PRIVATE_ID="$(awsl ec2 create-subnet --vpc-id "$VPC_ID" --cidr-block 10.96.1.0/24 \
  --availability-zone "${REGION}a" --query 'Subnet.SubnetId' --output text)"
[ -n "$SUBNET_PRIVATE_ID" ] || fail "could not seed the private subnet"
SUBNET_ALT_ID="$(awsl ec2 create-subnet --vpc-id "$VPC_ID" --cidr-block 10.96.2.0/24 \
  --availability-zone "${REGION}b" --query 'Subnet.SubnetId' --output text)"
[ -n "$SUBNET_ALT_ID" ] || fail "could not seed the alt subnet"
log "  DELTA  VPC + S3 endpoint + ECS cluster + 2 IAM roles + SG + 2 subnets seeded"

cat > "$EST/crossing.auto.tfvars" <<EOF
access_key = "test"
secret_key = "test"
vpc_id = "$VPC_ID"
security_group_id = "$SG_ID"
subnet_datacite-private_id = "$SUBNET_PRIVATE_ID"
subnet_datacite-alt_id = "$SUBNET_ALT_ID"
slack_webhook_url = "https://example.org/webhook"
EOF
log "  DELTA  var values for the seeded objects + slack_webhook_url (onboarding)"

# ── 3. stand the estate up ──────────────────────────────────────────────────
log "=== 3. init and apply: 3 instances ==="
( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null ) || fail "init failed"
APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY_OUT" | tail -40
  fail "the apply failed - this used to be clean, so this is a NEW, different regression, not #298"
}
grep -qE 'Apply complete! Resources: 3 added' <<< "$APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY_OUT"; fail "the apply did not create exactly 3 resources"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY_OUT")"

# Read all three back through the AWS CLI, never through choudoufu.
if awsl s3api head-bucket --bucket "$BUCKET" >/dev/null 2>&1; then LIVE_BUCKET="$BUCKET"; else LIVE_BUCKET=""; fi
[ "$LIVE_BUCKET" = "$BUCKET" ] || fail "could not find bucket $BUCKET through the AWS CLI"
log "  the bucket lives: $BUCKET"
LG_LIVE="$(awsl logs describe-log-groups --log-group-name-prefix "$LOG_GROUP" --query 'logGroups[0].logGroupName' --output text 2>/dev/null || true)"
[ "$LG_LIVE" = "$LOG_GROUP" ] || fail "could not find log group $LOG_GROUP through the AWS CLI"
log "  the log group lives: $LOG_GROUP"
TD_ARN="$(awsl ecs list-task-definitions --family-prefix "$FAMILY" --query 'taskDefinitionArns[0]' --output text 2>/dev/null || true)"
[[ "$TD_ARN" == arn:aws:ecs:*task-definition/$FAMILY:* ]] || fail "could not find task definition family $FAMILY through the AWS CLI"
log "  the task definition lives: $TD_ARN"
TD_TAG="$(awsl ecs list-tags-for-resource --resource-arn "$TD_ARN" --query "tags[?key=='tofu-address'].value | [0]" --output text 2>/dev/null || true)"
[ "$TD_TAG" = "aws_ecs_task_definition.sitemaps-generator" ] \
  || fail "the task definition carries tofu-address=$TD_TAG, expected aws_ecs_task_definition.sitemaps-generator"
log "  and carries its marker: tofu-address=$TD_TAG"

# ── 4. no state file, ever ──────────────────────────────────────────────────
rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"
[ ! -f "$EST/terraform.tfstate" ] || fail "the state file is still there"
log "=== 4. state file deleted ==="

# ── 5. THE BLOCKER (#298) ───────────────────────────────────────────────────
log "=== 5. live-plan - this is where #298 stops it ==="
PLAN_OUT="$(cd "$EST" && TF_LOG=trace "$TOFU" live-plan -input=false -no-color 2>&1)"; PLAN_RC=$?
if [ "$PLAN_RC" -eq 0 ]; then
  fail "live-plan EXITED 0. That is a better world than the one this script was written in: #298 looks fixed. Re-read it and turn this into a real passing crossing (drop this step, assert the plan and identities the way live/e2e/corpus-datafiles-generator does)."
fi
if grep -qF 'Expected ID in format of arn:PARTITION:ecs:REGION:ACCOUNTID:task-definition/FAMILY:REVISION' <<< "$PLAN_OUT" \
  && grep -qF 'Cannot import for projection' <<< "$PLAN_OUT"; then
  log "  reproduced the known, filed blocker: #298"
  log "  (Cloud-Control discovery's fallback identity for aws_ecs_task_definition"
  log "   is \"$FAMILY:1\", which this provider's ImportResourceState rejects -"
  log "   see the header for the full trace-level diagnosis)"
else
  printf '%s\n' "$PLAN_OUT" | grep -E '^Error' | head -10
  fail "live-plan failed, but NOT with #298's signature. This is a DIFFERENT problem than the one this script documents - read the error above and file it separately rather than assuming it is #298."
fi

log ""
log "=== BLOCKED (honest, not a false pass): #298 ==="
log ""
log "DataCite's own sitemaps-generator estate applies cleanly - all 3"
log "resources live, correctly tagged, confirmed through the AWS CLI - and"
log "then fails at live-plan on exactly the signature #298 documents. Exit"
log "code is non-zero on purpose: this estate does not cross yet."
exit 1
