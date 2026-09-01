#!/usr/bin/env bash
# (moved from the justfile's retired demo-corpus-sitemaps-generator recipe; run with: just demo-run corpus-sitemaps-generator)
# Issue #274's attempted crossing, and #298's repro:
# .corpus/mastino/prod-eu-west/services/sitemaps-generator, three resources
# (aws_s3_bucket.akita, aws_cloudwatch_log_group and aws_ecs_task_definition)
# apply cleanly and are confirmed live through the AWS CLI, then live-plan
# fails: discovery's Cloud Control fallback (needed because `version = "~> 5"`
# resolves to 5.100.0, the release #269 documented as carrying no list
# resources at all) finds the task definition correctly but hands
# ImportResourceState the literal "family:revision" string instead of the
# ARN, which this provider's importer for that type rejects. Re-pinning to
# 6.58.0 (the #269 workaround demo-corpus-ecs-taskdef uses) clears that step
# but trades it for a floci gap: aws_s3_bucket's tag read under that
# provider version calls S3 Control's ListTagsForResource, addressed via an
# account-ID-prefixed hostname floci cannot resolve. This script does not
# fake a pass - it exits non-zero at step 5, distinguishing #298's exact
# signature from any other failure. Needs Docker, the AWS CLI and a
# populated .corpus; runs on its own port (4705) so it can run beside
# `just demo`.
set -uo pipefail

# A real third-party estate crossed against a real emulator: issue #274's
# step 6, for .corpus/mastino/prod-eu-west/services/sitemaps-generator.
#
# Three resources - aws_s3_bucket.akita, aws_cloudwatch_log_group and
# aws_ecs_task_definition, all named "sitemaps-generator" - DataCite's own
# ECS-driven sitemap generator. It passes live-check with zero refused
# sites. Picked as one of #274's smallest untouched real corpus estates,
# smallest-first.
#
# #298, FIXED. Steps 0-4 below apply cleanly under the estate's own declared
# `version = "~> 5"` (which resolves to 5.100.0, the release #269 documented
# as carrying no list resources at all), and all three resources are
# confirmed live through the AWS CLI. Step 5 used to stop here:
#
#   Error: Expected ID in format of
#   arn:PARTITION:ecs:REGION:ACCOUNTID:task-definition/FAMILY:REVISION and
#   provided: sitemaps-generator:1
#
#   Error: Cannot import for projection
#
# TF_LOG=trace showed discovery finding the task definition correctly -
# "listing aws_ecs_task_definition via Cloud Control (AWS::ECS::TaskDefinition),
# 1 resources" - and the live object carried the right tofu-address tag,
# confirmed independently through the AWS CLI. But the identity handed to
# ImportResourceState was the literal "family:revision" join from the row's
# IdentityAttrs, not the ARN Cloud Control's own listing carries - and this
# provider's ImportResourceState for this type specifically demands the ARN
# form, even though its newer identity-object import (used by the native
# ListResource RPC path, see live/e2e/corpus-ecs-taskdef) wants
# family+revision instead.
#
# THE FIX. internal/live/discovery/cloudcontrol.go's
# resolveCloudControlImportID and importIDFromARN shared one inline check
# ("does this type import by ARN?") that only ever looked at
# IdentityAttrs[0]=="arn" - the newer identity-object convention - and had
# no way to learn that a type's LEGACY id-string import wants the ARN when
# its identity object does not. aws_ecs_task_definition's ImportSyntax
# (TASKDEFINITIONARN, row-gen-derived straight from the provider's own "##
# Import" section, which documents `terraform import
# aws_ecs_task_definition.example arn:aws:ecs:...:task-definition/FAMILY:REVISION`
# verbatim) already carried that answer; importsWholeARNString now reads it
# as a second signal, and any other ServerAssigned type whose ImportSyntax
# is a single ARN-suffixed token gets the same fix - not just this one type.
# See internal/live/discovery/cloudcontrol_test.go's TestImportsWholeARNString
# for the general shape and its regression guards.
#
# WHY THIS TYPE AND NOT ANALYTICS-WORKER. live/e2e/corpus-ecs-taskdef
# crosses this exact resource type successfully under `= 6.58.0`, where
# discovery uses the provider's native ListResource RPC and never reaches
# the Cloud Control fallback this fix touches at all. 5.100.0 has no
# ListResource support (#269), so discovery falls back to Cloud Control
# listing here instead - which finds the object fine, and (after the fix)
# now carries its ARN through to the import call the same way the native
# ListResource path's identity object always did.
#
# WHAT STILL DOES NOT CONVERGE, AND WHY - NEITHER IS #298. Fixing the
# identity gets live-plan to run clean (exit 0, no state file, the task
# definition materializing from its ARN), but the plan used not to be
# empty: it proposed changes to BOTH declared resources, and both were
# pre-existing, already-documented floci emulator gaps, not marker bugs:
#
#   - aws_ecs_task_definition.sitemaps-generator "must be replaced": floci's
#     DescribeTaskDefinition response dropped
#     container_definitions[0].logConfiguration entirely, even though
#     RegisterTaskDefinition was called WITH it - the exact gap
#     live/e2e/corpus-ecs-taskdef's header documented and asserted by name
#     for a different family (analytics-worker). Confirmed here on a
#     SECOND, independent family (sitemaps-generator): not a fixture quirk.
#     FIXED fork-side 2026-08-18 (issue #287 item 5, lex00/floci commit
#     5d435915): the pinned image now echoes logConfiguration back, so this
#     instance materializes with an empty diff and drops out of the plan
#     entirely - step 6 now asserts its ABSENCE from the rendered plan.
#   - aws_s3_bucket.akita "will be updated in-place" (+ acl, + force_destroy):
#     the same acl/force_destroy drift live/e2e/corpus-datafiles-generator's
#     header documents and asserts by name for a different bucket. Confirmed
#     here on a SECOND, independent bucket: not a fixture quirk either. Not
#     fixed here - still asserted by name below.
#
# The remaining gap is asserted BY NAME below, the same "value, not verdict"
# departure corpus-datafiles-generator takes: a floci fix that stops needing
# the acl/force_destroy backfill turns this script red rather than silently
# green, and so does any resource starting to drift beyond what step 6
# names.
#
# A THIRD, SEPARATE FINDING (also not #298, not fixed here): the plan also
# warns "Tagged resource's ARN could not be joined to a resource type ...
# no CFN type is known for ARN service \"ecs\" and resource segment
# \"task-definition\"" - internal/live/discovery/tagging.go's arnJoinTable
# (a curated, hand-maintained ARN-service+resource-type -> CFN-type ledger
# used only by the estate-wide UNDECLARED-resource tag sweep, a different
# code path than the one #298's fix touches) has no "ecs"/"task-definition"
# row, only "ecs"/"cluster". This narrows sweep coverage for undeclared ECS
# task definitions; it does not affect resolving this estate's OWN declared
# instance, which is what #298 was about and what step 5 below asserts
# fixed. Left as a follow-up rather than folded in here.
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

# ── 5. live-plan, #298 fixed ────────────────────────────────────────────────
log "=== 5. live-plan and the rendered identities ==="
PLAN_OUT="$(cd "$EST" && TF_LOG=trace "$TOFU" live-plan -input=false -no-color 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error|^│' | head -30; fail "live-plan exited $PLAN_RC"; }
[ ! -f "$EST/terraform.tfstate" ] || fail "live-plan wrote a state file"
log "  live-plan exited 0"

WANT_TD_ID="arn:aws:ecs:${REGION}:000000000000:task-definition/${FAMILY}:1"
grep -qF "materialized aws_ecs_task_definition.sitemaps-generator from import identity \"$WANT_TD_ID\"" <<< "$PLAN_OUT" \
  || { grep -oE 'materialized aws_ecs_task_definition\.sitemaps-generator from import identity "[^"]*"' <<< "$PLAN_OUT"; \
       fail "the task definition was not materialized from its ARN ($WANT_TD_ID) - #298 regressed"; }
log "  aws_ecs_task_definition.sitemaps-generator materialized from its ARN, not \"$FAMILY:1\" - #298 fixed"

grep -qF 'materialized aws_s3_bucket.akita from import identity "'"$BUCKET"'"' <<< "$PLAN_OUT" \
  || fail "aws_s3_bucket.akita was not materialized from \"$BUCKET\""
grep -qF 'materialized aws_cloudwatch_log_group.sitemaps-generator from import identity "'"$LOG_GROUP"'"' <<< "$PLAN_OUT" \
  || fail "aws_cloudwatch_log_group.sitemaps-generator was not materialized from \"$LOG_GROUP\""
log "  and the other two instances materialized from their own identities"

# ── 6. one known, pre-existing floci gap remains (the other, #287-5, fixed) ─
# NOT #298: block_for and assert_known_diff below isolate the block for one
# resource address out of the rendered plan (same technique
# corpus-datafiles-generator's assert_only_known_diff uses) and check it is
# EXACTLY the one remaining known, already-documented emulator gap this
# script's header explains - never a create, a destroy, or an unexplained
# attribute change. The task definition's own logConfiguration gap (#287
# item 5) is fixed as of the pinned image, so it is asserted ABSENT from
# the plan entirely, not present-but-known.
log "=== 6. the remaining known, pre-existing floci gap - asserted by name, not hidden ==="
block_for() {
  local addr="$1" out="$2"
  awk -v addr="$addr" '
    $0 ~ ("^  # " addr " (will be|must be)") { grabbing=1 }
    grabbing { print }
    grabbing && /^    }$/ { exit }
  ' <<< "$out"
}

TD_BLOCK="$(block_for 'aws_ecs_task_definition\.sitemaps-generator' "$PLAN_OUT")"
[ -z "$TD_BLOCK" ] || { printf '%s\n' "$TD_BLOCK"; fail "aws_ecs_task_definition.sitemaps-generator has a rendered plan block - the #287-5 logConfiguration fix regressed, or a DIFFERENT drift appeared (is FLOCI_IMAGE pointed at an older pin?)"; }
log "  aws_ecs_task_definition.sitemaps-generator: no plan block at all - #287-5's logConfiguration fix holds"

S3_BLOCK="$(block_for 'aws_s3_bucket\.akita' "$PLAN_OUT")"
[ -n "$S3_BLOCK" ] || { grep -E '^Plan:' <<< "$PLAN_OUT"; fail "no resource action block found for aws_s3_bucket.akita"; }
grep -qE '^  # aws_s3_bucket\.akita will be updated in-place' <<< "$S3_BLOCK" \
  || { printf '%s\n' "$S3_BLOCK"; fail "aws_s3_bucket.akita's action is not the known in-place update"; }
S3_ATTRS="$(grep -E '^ +[+~-] [A-Za-z_]+ +=' <<< "$S3_BLOCK")"
S3_UNEXPECTED="$(grep -vE '\+ acl +=|\+ force_destroy +=' <<< "$S3_ATTRS")"
[ -z "$S3_UNEXPECTED" ] || { printf '%s\n' "$S3_ATTRS"; fail "aws_s3_bucket.akita's update touches attributes beyond the known acl/force_destroy gap (live/e2e/corpus-datafiles-generator)"; }
log "  aws_s3_bucket.akita: the known acl/force_destroy update, nothing else"

grep -qE '^Plan: 0 to add, 1 to change, 0 to destroy\.$' <<< "$PLAN_OUT" \
  || { grep -E '^Plan:' <<< "$PLAN_OUT"; fail "the plan summary is not exactly the one remaining known-gap resource - a THIRD resource changed, or #287-5 regressed"; }
log "  Plan: 0 to add, 1 to change, 0 to destroy - exactly the in-place update, nothing more"

log ""
log "=== CROSSED: #298 fixed, #287 item 5 fixed ==="
log ""
log "DataCite's own sitemaps-generator estate applies cleanly, all 3"
log "resources correctly tagged and confirmed through the AWS CLI, and a"
log "cold live-plan against a deleted state file resolves every instance -"
log "including the task definition, now from its ARN rather than the bare"
log "\"family:revision\" join #298 filed. The plan is not EMPTY - one"
log "pre-existing, already-documented floci emulator gap remains (the S3"
log "acl/force_destroy backfill), asserted by name above rather than"
log "hidden - it is not a marker bug. The task definition's own gap"
log "(logConfiguration dropped on read) is fixed as of the pinned image and"
log "now materializes with an empty diff, confirmed on a second, independent"
log "family beyond live/e2e/corpus-ecs-taskdef's analytics-worker."
