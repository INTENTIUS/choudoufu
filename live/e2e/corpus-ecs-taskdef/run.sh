#!/usr/bin/env bash
set -uo pipefail

# aws_ecs_task_definition crossed against a real emulator, using DataCite's
# own task definition out of
# .corpus/mastino/prod-eu-west/services/analytics-worker.
#
# THIS SCRIPT PINS A FIX. Exit 0 means the cold replan resolves the live task
# definition and converges. Both mastino service estates - analytics-worker
# and datafile-generator - used to die here on "Listed resource with no
# identity"; step 5 is where that used to stop, and is now where it is
# checked to keep working.
#
# WHAT THIS IS, AND WHAT IT IS NOT. It is not that estate crossed. That
# estate reads twelve data sources, two of which this floci build cannot
# answer at all - see step 0 - so it cannot stand up here, and none of that
# is about markers. This is the ONE resource in it that failed for a marker
# reason: "Listed resource with no identity" on aws_ecs_task_definition.
# The estate's own resource block and its own container_definitions file are
# used verbatim, so the shape under test is DataCite's and not a fixture
# author's.
#
# aws_ecs_task_definition is ServerAssigned: ECS mints the revision ARN at
# create time and no argument reconstructs it, so a cold run can only find
# the task definition it already owns by enumerating ECS and reading a
# marker off what comes back. Steps 3 and 4 show that half working - it
# applies, it carries its marker, and the enumeration finds it. Step 5 used
# to be where it stopped: the row for this type looked its identity up by
# "id", which the list result never carries. tools/row-gen now carries the
# provider's own survey-full.json required_for_import into IdentityAttrs for
# a server-assigned row that has neither Components nor a ratified
# IdentityAttrs of its own - family and revision here - so
# internal/live/discovery's importIdentity finds "family" in the identity
# object the provider's ListResource RPC returns instead of falling back to
# an "id" that was never there.
#
#   bash live/e2e/corpus-ecs-taskdef/run.sh
#
# Needs Docker, the AWS CLI, and a populated .corpus (`just corpus-fetch`).
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4694).
#   FLOCI_IMAGE  the emulator image; defaults to live/floci-image.
#
# .corpus is shared across every worktree and is NEVER written to.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CORPUS_DIR="${CORPUS_DIR:-$ROOT/.corpus}"
SRC="$CORPUS_DIR/mastino/prod-eu-west/services/analytics-worker"
WORK="$(mktemp -d)"
EST="$WORK/estate"
FLOCI_PORT="${FLOCI_PORT:-4694}"
FLOCI_NAME="choudoufu-corpus-ecs-taskdef-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="mastino-analytics-worker-taskdef"
REGION="eu-west-1"
FAMILY="analytics-worker"

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }

# ── 0. tools and corpus ─────────────────────────────────────────────────────
# The two data reads floci cannot answer, recorded here because they are the
# reason this script is one resource and not the estate:
#
#   data "aws_route53_zone" "internal" { name = "datacite.org", private_zone = true }
#     floci reports Config.PrivateZone = false for a hosted zone created WITH
#     a VPC, so no zone ever matches private_zone = true. The same gap is
#     documented at live/e2e/corpus-crossing/run.sh's DELTA 4.
#
#   data "aws_lb" "default" / data "aws_lb_listener" "default"
#     elbv2 is not in this build's /_localstack/health service list at all.
#
# A third floci gap, on the ONE resource this script does stand up, USED TO
# force step 5 into a replace: DescribeTaskDefinition dropped
# container_definitions[0].logConfiguration entirely, even though
# RegisterTaskDefinition was called WITH it (confirmed by reading the raw
# HTTP bodies under TF_LOG=trace - the request carried logConfiguration, the
# DescribeTaskDefinition response did not). It was not a marker bug: the
# same drift would have reproduced under plain OpenTofu with a real state
# file and `plan` against that floci build.
#
# Fixed fork-side 2026-08-18 (issue #287 item 5, lex00/floci commit
# 5d435915): ContainerDefinition had no logConfiguration field on the model
# at all - EcsJsonHandler.parseContainerDefinitions never read the JSON key
# off a RegisterTaskDefinition request body, so the value never reached the
# model to begin with, and the response builder had nothing to render back
# out on Describe/List either. live/floci-image now pins a build with this
# fixed, so step 5 converges clean by default; its assertion still tolerates
# the old forced-replace shape defensively, for a FLOCI_IMAGE override
# pointed at an older pin.
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
# The estate's own container_definitions file, byte for byte.
cp "$SRC/analytics-worker.json" "$EST/"
# And the estate's own resource block, extracted rather than retyped: from
# `resource "aws_ecs_task_definition"` to the first line that is a bare "}".
awk '/^resource "aws_ecs_task_definition" "analytics-worker" \{/{f=1} f{print} f&&/^\}$/{exit}' \
  "$SRC/main.tf" > "$EST/taskdef.tf"
grep -q 'requires_compatibilities' "$EST/taskdef.tf" \
  || { cat "$EST/taskdef.tf"; fail "the extracted resource block is not the whole block - the corpus pin has moved"; }
log "  DataCite's own resource block and container_definitions copied out of .corpus"

# ── 1. floci ────────────────────────────────────────────────────────────────
log "=== 1. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
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

# ── 2. the deltas ───────────────────────────────────────────────────────────
log "=== 2. deltas ==="

# DELTA 1, the seeded read. The estate takes execution_role_arn from
# data "aws_iam_role" "ecs_task_execution_role". The role is seeded through
# the AWS CLI and the data source is declared over it, so the reference in
# DataCite's block is untouched.
ROLE_ARN="$(awsl iam create-role --role-name ecsTaskExecutionRole \
  --assume-role-policy-document '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ecs-tasks.amazonaws.com"},"Action":"sts:AssumeRole"}]}' \
  --query 'Role.Arn' --output text)"
[ -n "$ROLE_ARN" ] || fail "could not seed the ecsTaskExecutionRole"
cat > "$EST/input.tf" <<'EOF'
# DELTA 1. The estate's own data block, over a role seeded through the AWS
# CLI. The reference in DataCite's resource block is unchanged.
data "aws_iam_role" "ecs_task_execution_role" {
  name = "ecsTaskExecutionRole"
}
EOF
log "  DELTA 1  ecsTaskExecutionRole seeded, its data read kept  (seeded read)"

# DELTA 2, the seven variables the templatefile call interpolates. The
# estate keeps these in keeshond.auto.tfvars, which is not in the corpus
# checkout - the .tmpl beside it is. Values are placeholders; none of them
# is identity-bearing.
cat > "$EST/var.tf" <<'EOF'
# DELTA 2. Variable declarations + values for the templatefile call.
variable "keeshond_tags" {
  type    = map(string)
  default = { version = "1.0.0" }
}
variable "datacite_api_url" { default = "https://api.example.org" }
variable "analytics_database_dbname" { default = "analytics" }
variable "analytics_database_host" { default = "db.example.org" }
variable "analytics_database_user" { default = "analytics" }
variable "analytics_database_password" { default = "placeholder" }
variable "datacite_jwt" { default = "placeholder" }
EOF
log "  DELTA 2  7 variable defaults for the templatefile call     (onboarding)"

# DELTA 3 + 4, the terraform block. The estate's own terraform.tf declares
# a cloud block; it is replaced rather than edited, because its provider
# block also needs floci's flags.
cat > "$EST/terraform.tf" <<EOF
# DELTA 3 + 4. Was: a cloud {} block and a bare provider block.
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }

  live {
    estate = "$ESTATE"
  }
}

provider "aws" {
  region = "$REGION"

  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true
}
EOF
log "  DELTA 3  cloud block removed, live block added             (onboarding, #268)"
log "  DELTA 4  provider pinned = 6.58.0 + emulator flags         (onboarding, #269)"

# ── 3. stand it up ──────────────────────────────────────────────────────────
log "=== 3. init and apply ==="
( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "init failed"; }
APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY_OUT" | grep -E '^Error|^│' | head -30; fail "the apply failed"; }
grep -qE 'Apply complete! Resources: 1 added' <<< "$APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY_OUT"; fail "the apply did not create exactly 1 instance"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY_OUT" | head -1)"

# ── 4. what is live, and its marker ─────────────────────────────────────────
log "=== 4. the live task definition and its marker ==="
TD_ARN="$(awsl ecs describe-task-definition --task-definition "$FAMILY" \
  --query 'taskDefinition.taskDefinitionArn' --output text)"
[ -n "$TD_ARN" ] && [ "$TD_ARN" != "None" ] || fail "ECS holds no task definition in family $FAMILY"
REVS="$(awsl ecs list-task-definitions --family-prefix "$FAMILY" --query 'length(taskDefinitionArns)' --output text)"
[ "$REVS" = "1" ] || fail "ECS holds $REVS revisions of $FAMILY, expected 1"
ADDR="$(awsl ecs list-tags-for-resource --resource-arn "$TD_ARN" \
  --query "tags[?key=='tofu-address'].value | [0]" --output text 2>/dev/null || echo None)"
[ "$ADDR" = "aws_ecs_task_definition.analytics-worker" ] \
  || { awsl ecs list-tags-for-resource --resource-arn "$TD_ARN" --output text; fail "the task definition carries tofu-address=$ADDR"; }
EST_TAG="$(awsl ecs list-tags-for-resource --resource-arn "$TD_ARN" \
  --query "tags[?key=='tofu-estate'].value | [0]" --output text 2>/dev/null || echo None)"
[ "$EST_TAG" = "$ESTATE" ] || fail "the task definition carries tofu-estate=$EST_TAG, expected $ESTATE"
log "  $TD_ARN"
log "  carries tofu-address=$ADDR"

# ── 5. the identity refusal, gone ───────────────────────────────────────────
# This is where both mastino service estates used to die. It no longer does.
#
# WHAT USED TO HAPPEN, read off the trace of a run before the fix:
#
#   1. ListTaskDefinitions returns 200 and the provider finds the object.
#   2. The provider resolves the revision ARN from it and serves the
#      ListResource RPC with zero diagnostics.
#   3. The fork MATCHES the marker on the listed object - the refusal names
#      aws_ecs_task_definition.analytics-worker, so discovery worked.
#   4. And then refuses, because the attribute it looks the identity up by,
#      `id`, is not among the attributes the list call returned.
#
# live/survey-full.json had the answer the provider itself gives all along:
#
#   "identity": { "required_for_import": ["family", "revision"],
#                 "optional_for_import": ["account_id", "region"] }
#
# tools/row-gen's mergeIdentityAttrs (serverassignedattrs.go) now carries a
# ServerAssigned row's provider identity schema into IdentityAttrs in full
# rather than only when it names exactly one attribute, so
# aws_ecs_task_definition's row now offers ["family", "revision"].
# internal/live/discovery's importIdentity tries each in order and stops at
# the first the list result actually carries - "family" here, since the
# provider's ListResource identity object does not carry "id" either. That
# is enough: the "has a usable identity" check this refusal guards does not
# need family and revision combined, only ONE attribute the list result
# echoes back, and the read that follows uses the resource's full identity
# object (family AND revision together), not this single string - the trace
# below shows the provider's ReadResource call resolving the exact revision
# ARN, not just the family.
log "=== 5. cold replan, and what discovery does with it ==="
plan_into() {
  rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"
  ( cd "$EST" && TF_LOG=trace "$TOFU" live-plan -input=false -no-color )
}
PLAN_OUT="$(plan_into 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] \
  || { grep -E '^Error|^│' <<< "$PLAN_OUT" | head -30; fail "live-plan failed"; }
[ ! -f "$EST/terraform.tfstate" ] || fail "live-plan wrote a state file"
grep -qF 'Listed resource with no identity' <<< "$PLAN_OUT" \
  && fail "the refusal this script used to pin is BACK. Read the trace and re-diagnose before touching this assertion."
log "  live-plan succeeded: no \"Listed resource with no identity\" refusal"

# Discovery reached the object and resolved a real identity for it, not an
# empty one silently accepted.
grep -qE 'ListTaskDefinitions' <<< "$PLAN_OUT" \
  || fail "the provider never issued ListTaskDefinitions, so the enumeration itself regressed"
grep -qF 'tf_aws.resource_attribute.id=' <<< "$PLAN_OUT" \
  || fail "the provider never resolved a task-definition id, so the list result is emptier than this script claims"
MATERIALIZED="$(grep -oE 'materialized aws_ecs_task_definition\.analytics-worker from import identity "[^"]*"' <<< "$PLAN_OUT" | tail -1)"
[ -n "$MATERIALIZED" ] \
  || fail "no \"materialized aws_ecs_task_definition.analytics-worker from import identity\" trace line - discovery did not bind an identity to the declared resource"
grep -qF "\"$FAMILY\"" <<< "$MATERIALIZED" \
  || fail "the materialized identity is $MATERIALIZED, expected the family name $FAMILY - IdentityAttrs stopped naming an attribute the list result carries"
log "  $MATERIALIZED"
# And the read this identity drove actually reached the live object: the
# provider's own ReadResource call resolved the exact revision ARN, not just
# the family the trace above names.
grep -qF "DescribeTaskDefinition" <<< "$PLAN_OUT" \
  || fail "no DescribeTaskDefinition call in the trace - the materialized identity was never read"
grep -qF "\"taskDefinition\":\"$TD_ARN\"" <<< "$PLAN_OUT" \
  || fail "DescribeTaskDefinition was never called with the live revision ARN $TD_ARN - the resolved identity did not drive a read of the right object"
log "  and that identity drove a DescribeTaskDefinition read of $TD_ARN itself"

# A separate floci gap, fixed 2026-08-18 (see step 0): DescribeTaskDefinition
# used to drop container_definitions[0].logConfiguration on the way back
# even though it was sent at create time. The pinned image now echoes it
# back, so the expected outcome is a clean converge; the elif branch stays
# as a named, non-silent fallback for a FLOCI_IMAGE pointed at an older pin,
# rather than this script failing opaquely against one.
if grep -qE 'No changes|Plan: 0 to add, 0 to change, 0 to destroy' <<< "$PLAN_OUT"; then
  log "  plan converged with no changes (floci echoed logConfiguration back this run)"
elif grep -qF 'must be replaced' <<< "$PLAN_OUT" && grep -qF 'logConfiguration' <<< "$PLAN_OUT"; then
  log "  plan forces a replace over container_definitions.logConfiguration - the"
  log "  pre-#287-5 floci round-trip gap, not a marker defect (is FLOCI_IMAGE"
  log "  pointed at an older pin?)"
else
  grep -E '^  #|^Plan:' <<< "$PLAN_OUT" | head -10
  fail "live-plan's diff is neither empty nor the known logConfiguration replace - something else changed; re-diagnose before touching this assertion"
fi

log ""
log "=== PASS ==="
log ""
log "DataCite's own aws_ecs_task_definition block applies against an emulator,"
log "carries its marker, and a cold replan now resolves its live identity by"
log "family (the attribute the list result actually carries) instead of"
log "refusing on the missing 'id'. Both mastino service estates -"
log "analytics-worker and datafile-generator - used to die here; neither does"
log "now."
log ""
log "This is one resource, not that estate. The estate also reads two data"
log "sources this floci build cannot answer at all; see step 0. A third,"
log "unrelated floci gap - logConfiguration dropped on read - forced a replace"
log "on this one resource until #287 item 5 fixed it fork-side 2026-08-18;"
log "step 5 still checks for it by name in case FLOCI_IMAGE points elsewhere."
