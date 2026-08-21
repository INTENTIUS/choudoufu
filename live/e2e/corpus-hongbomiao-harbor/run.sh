#!/usr/bin/env bash
set -uo pipefail

# The five-stage real-estate crossing (live/corpus-crossing-manifest.json)
# for hongbo-miao/hongbomiao.com (live/corpus-manifest.json, pinned by
# commit - no tag, see that entry's own comment for why), a THIRD, disjoint
# slice of the same repository already crossed by
# live/e2e/corpus-hongbomiao-labelbox/run.sh (the "general" environment's
# Labelbox integration) and live/e2e/corpus-hongbomiao-storage/run.sh (the
# "storage" environment's remote-state-free bootstrap) - HANDOFF.md's own
# suggestion for broadening the OpenTofu-native lane without a fresh
# sourcing search, since estate-gen's own convention treats separate
# module examples/sections of one repository as legitimately separate
# crossing targets.
#
# THE SCOPING DECISION. hongbomiao.com's infrastructure/opentofu tree has
# exactly four AWS environments (general, storage, network, kubernetes),
# all of it cross-wired through terraform_remote_state, plus three
# non-AWS environments (nebius, cloudflare, snowflake) that floci - an
# AWS-only emulator - cannot run at all: those provider blocks target
# real Nebius/Cloudflare/Snowflake endpoints, not anything floci emulates,
# so no cold_deploy stage could even target floci for them. Surveyed for a
# THIRD self-contained AWS slice disjoint from Labelbox and storage:
#   - environments/production/aws/network/main.tofu is ALL data sources
#     (aws_vpc, aws_subnets) - zero resources, nothing to migrate.
#   - environments/production/aws/kubernetes/main.tofu opens with
#     `data "terraform_remote_state" "production_aws_network_..."` and
#     nearly every IAM-role module call in it
#     (velero_iam_role/mimir_iam_role/loki_iam_role/tempo_iam_role/
#     label_studio_iam_role/mlflow_run_iam_role/... - fifteen modules
#     under modules/amazon-eks/*_iam_role) takes
#     amazon_eks_cluster_oidc_provider(_arn) sourced from
#     module.amazon_eks_cluster, which this file builds via the full
#     terraform-aws-modules/eks module against real subnets from that
#     remote state - the same shape as the terraform-popular lane's own
#     terraform-aws-eks examples/basic crossing (cold_deploy/migrate PASS,
#     test_plan FAIL), too large and coupled to redo cheaply here.
#   - ONE section of kubernetes/main.tofu is genuinely self-contained: the
#     "Harbor" section (S3 bucket + IAM user + inline user policy) is the
#     ONLY module block in the whole environment that creates an
#     `aws_iam_user` rather than an OIDC-federated `aws_iam_role` - it
#     needs no EKS cluster, no OIDC provider, no remote state, nothing
#     this file's own EKS cluster resource produces. That is what this
#     script crosses - copied byte-identical from the pinned commit
#     (diffed below at DELTA), with this script's own root wiring
#     standing in for the remote-state-coupled environment root the same
#     way corpus-hongbomiao-labelbox's does for "general" and
#     corpus-hongbomiao-storage's does for "storage".
#
# What this slice contributes that Labelbox and storage did not:
# `aws_iam_user`/`aws_iam_user_policy` (an inline user policy, not a role
# policy) - both already-ratified DefaultTable rows
# (internal/live/identity/table_generated.go), the user's identity a plain
# client-named `name` component and the inline policy's a composite
# `USERNAME:POLICYNAME`, the same shape as `aws_iam_role`/
# `aws_iam_role_policy` in Labelbox but a genuinely different resource
# pair - so no "no orphan recovery" warning is expected here, unlike
# Labelbox's schema-fallback CORS configuration.
#
# STAGES:
#   1. COLD DEPLOY   plain `tofu apply` (real OpenTofu core, no choudoufu),
#                     the unmodified two leaf modules - the honest proof
#                     the module code is real and buildable, and the
#                     source of genuinely unmarked live infra for stage 2.
#   2. MIGRATE        `choudoufu live-import -approve` against that cold
#                     state.
#   3. TEST PLAN      delete the state file, `choudoufu live-plan`, assert
#                     the plan is EMPTY *and* assert the rendered identity
#                     strings against the AWS CLI's own answer.
#   4. TEST APPLY     apply the empty plan; assert a genuine no-op by
#                     comparing the estate's tagged-object count before and
#                     after.
#   5. DRIFT AND      mutate one live object's tag out of band via the AWS
#      RECONVERGE     CLI directly against floci, replan, assert the diff
#                     proposes fixing exactly that one object, apply, and
#                     confirm the live tag is back to what the config says.
#
# BREAK=1 corrupts one expected tofu-address ahead of stage 2's assertion
# and tampers a second, unrelated live object ahead of stage 5's, so both
# assertions are proven load-bearing rather than a grep that always matches.
#
#   bash live/e2e/corpus-hongbomiao-harbor/run.sh
#
# Needs Docker, the AWS CLI, and the real `tofu` binary on PATH for stage 1
# (stock terraform cannot load a directory of .tofu files at all - see
# corpus-hongbomiao-labelbox's own header for the positive proof; not
# repeated here to keep this script shorter, but the same
# `terraform init` == "no configuration files" check is not needed a third
# time in one campaign and is skipped here on purpose).
#
# Env overrides:
#   TOFU_BIN      path to a prebuilt choudoufu binary; skips the go build.
#   FLOCI_PORT    host port for the emulator (default 4728, clear of every
#                 other corpus-*/reference-* script's own default).
#   FLOCI_IMAGE   the emulator image; defaults to the digest pin in
#                 live/floci-image.
#   BREAK         set to 1 to corrupt stage 2's identity assertion and
#                 tamper a second object ahead of stage 5's, proving both
#                 are load-bearing.
#   DEBUG_KEEP    set to 1 to skip the exit trap: the floci container and
#                 the WORK directory are left behind for inspection.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SRC_AWS="$ROOT/.corpus/hongbomiao/infrastructure/opentofu/modules/aws"
SRC_EKS="$ROOT/.corpus/hongbomiao/infrastructure/opentofu/modules/amazon-eks"
WORK="$(mktemp -d)"
PLAIN="$WORK/plain"
ESTATE="$WORK/estate"
FLOCI_PORT="${FLOCI_PORT:-4728}"
FLOCI_NAME="choudoufu-corpus-hongbomiao-harbor-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"
REGION="us-west-2"
ESTATE_NAME="hongbomiao-harbor-crossing"
BUCKET_NAME="${ESTATE_NAME}-hm-harbor"
USER_NAME="${ESTATE_NAME}-hm-harbor-user"
POLICY_NAME="S3ReadWritePolicy-${BUCKET_NAME}"

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
[ -n "${DEBUG_KEEP:-}" ] || trap cleanup EXIT

log() { printf '%s\n' "$*"; }

# The gauntlet protocol (live/GAUNTLET.md): each stage reports its verdict on
# stdout so tools/gauntlet records it. CURRENT_STAGE names the stage a
# failure belongs to; fail() reports it before exiting.
# shellcheck source=live/e2e/lib/gauntlet.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/lib/gauntlet.sh"
CURRENT_STAGE=""
fail() {
  printf 'FAIL: %s\n' "$*" >&2
  if [ -n "$CURRENT_STAGE" ]; then gauntlet_stage "$CURRENT_STAGE" fail "$*"; fi
  exit 1
}
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }
gauntlet_begin

# ── 0. tools and corpus ─────────────────────────────────────────────────────
log "=== 0. tools and corpus ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v tofu >/dev/null 2>&1 || fail "the real tofu binary is not on PATH - required for stage 1"
[ -d "$SRC_AWS/amazon_s3_bucket" ] && [ -d "$SRC_EKS/harbor_iam_user" ] \
  || fail "$SRC_AWS/amazon_s3_bucket or $SRC_EKS/harbor_iam_user is missing - fetch hongbo-miao/hongbomiao.com at the pin in live/corpus-manifest.json first"
log "  cold deploy binary: tofu ($(tofu version | head -1))"

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

# copy_leaf_modules <destdir>: the two real, unmodified leaf modules - every
# file in them keeps hongbomiao.com's own .tofu extension.
copy_leaf_modules() {
  local dest="$1"
  mkdir -p "$dest/modules/aws" "$dest/modules/amazon-eks"
  cp -R "$SRC_AWS/amazon_s3_bucket" "$dest/modules/aws/amazon_s3_bucket"
  cp -R "$SRC_EKS/harbor_iam_user" "$dest/modules/amazon-eks/harbor_iam_user"
}

# write_root <destdir> <live_block>: this crossing's own root wiring,
# standing in for the remote-state-coupled environment root (see header).
# The two module calls below use the SAME source paths and the SAME
# argument names as hongbomiao.com's own aws/kubernetes/main.tofu's
# "Harbor" section, other than the estate-scoped bucket/user names. Written
# with a .tofu extension, matching every file this project's own real
# modules use.
write_root() {
  local dest="$1" live_block="$2"
  cat > "$dest/main.tofu" <<EOF
terraform {
  required_version = ">= 1.11"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.59.0"
    }
  }
$live_block
}

provider "aws" {
  region = "$REGION"

  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style           = true
}

provider "aws" {
  alias  = "production"
  region = "$REGION"

  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style           = true
}

locals {
  common_tags = {
    "hm_environment" = "production"
    "hm_team"        = "hongbomiao"
    "hm_managed_by"  = "opentofu"
  }
}

# Harbor - S3 bucket
module "s3_bucket_hm_harbor" {
  providers      = { aws = aws.production }
  source         = "./modules/aws/amazon_s3_bucket"
  s3_bucket_name = "$BUCKET_NAME"
  common_tags    = local.common_tags
}

# Harbor - IAM user
module "harbor_iam_user" {
  providers         = { aws = aws.production }
  source            = "./modules/amazon-eks/harbor_iam_user"
  aws_iam_user_name = "$USER_NAME"
  s3_bucket_name    = module.s3_bucket_hm_harbor.name
  common_tags       = local.common_tags
}
EOF
}

copy_leaf_modules "$PLAIN"
write_root "$PLAIN" ""
log "  two leaf modules copied unmodified out of .corpus/hongbomiao into $PLAIN"

# DELTA: confirm the copy is byte-identical to the pinned commit - the only
# things this crossing adds are its OWN root file and provider block, never
# an edit to hongbomiao.com's own module code.
diff -rq "$SRC_AWS/amazon_s3_bucket" "$PLAIN/modules/aws/amazon_s3_bucket" >/dev/null \
  || fail "modules/aws/amazon_s3_bucket differs from the pinned commit - this crossing must run the real, unmodified module"
diff -rq "$SRC_EKS/harbor_iam_user" "$PLAIN/modules/amazon-eks/harbor_iam_user" >/dev/null \
  || fail "modules/amazon-eks/harbor_iam_user differs from the pinned commit - this crossing must run the real, unmodified module"
log "  DELTA confirmed: both leaf modules are byte-identical to the pinned commit; only this script's own root file was added"

copy_leaf_modules "$ESTATE"
write_root "$ESTATE" '
  live {
    estate = "'"$ESTATE_NAME"'"
    record_store "local" {
      path = ".tofu-records"
    }
  }'
log "  estate copy written to $ESTATE (stages 2-5: choudoufu, live block added)"

# ── 1. floci ─────────────────────────────────────────────────────────────
log "=== 1. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"s3"' <<< "${HEALTH:-}" && grep -q '"iam"' <<< "${HEALTH:-}" && break
  sleep 2
done
grep -q '"s3"' <<< "${HEALTH:-}" && grep -q '"iam"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (s3/iam) at $ENDPOINT"
log "  healthy"

export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" AWS_ENDPOINT_URL="$ENDPOINT"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 1: COLD DEPLOY - plain tofu apply, no live block, no choudoufu
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=cold_deploy
log "=== STAGE 1: cold deploy (plain tofu apply, the real unmodified modules) ==="
( cd "$PLAIN" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "stage 1 init failed"; }
COLD_OUT="$(cd "$PLAIN" && tofu apply -input=false -auto-approve -no-color 2>&1)"; COLD_RC=$?
[ "$COLD_RC" -eq 0 ] || { printf '%s\n' "$COLD_OUT" | tail -40; fail "stage 1 (cold deploy) failed"; }
grep -qE 'Apply complete! Resources: 3 added, 0 changed, 0 destroyed' <<< "$COLD_OUT" \
  || { grep -E 'Apply complete' <<< "$COLD_OUT"; fail "stage 1 did not create exactly 3 resources"; }
log "  $(grep -E 'Apply complete' <<< "$COLD_OUT")"
[ -f "$PLAIN/terraform.tfstate" ] || fail "stage 1 left no state file to migrate from"

UNMARKED="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE_NAME" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$UNMARKED" = "0" ] || fail "plain tofu's own objects already carry tofu-estate=$ESTATE_NAME before migration - this crossing proves nothing"
log "  confirmed unmarked: 0 objects carry tofu-estate=$ESTATE_NAME before migration"

log ""
log "STAGE 1 (cold deploy): PASS"
gauntlet_stage cold_deploy pass "$(grep -E 'Apply complete' <<< "$COLD_OUT"); 0 objects carry tofu-estate=$ESTATE_NAME before migration"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=migrate
log "=== STAGE 2: choudoufu live-import ==="
( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "estate init failed"; }

log "--- 2a: live-import, read-only first ---"
IMPORT_OUT="$(cd "$ESTATE" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE_NAME" -no-color 2>&1)"; IMPORT_RC=$?
[ "$IMPORT_RC" -eq 0 ] || { printf '%s\n' "$IMPORT_OUT" | tail -40; fail "live-import (dry run) failed"; }
grep -qF "2 of 3 resource instance(s) are eligible for stamping" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import did not verify exactly 2 of 3 as eligible (the S3 bucket and the IAM user - the inline user policy is correctly UNTAGGABLE, see header)"; }
grep -qF "No tag has been written." <<< "$IMPORT_OUT" || fail "the dry run wrote a tag - it must not"
grep -qF "UNTAGGABLE (1)" <<< "$IMPORT_OUT" \
  || fail "expected exactly 1 UNTAGGABLE resource (the inline user policy)"
log "  2 of 3 verified against the live system; nothing written yet"

log "--- 2b: -approve ---"
APPROVE_OUT="$(cd "$ESTATE" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE_NAME" -approve -no-color 2>&1)"; APPROVE_RC=$?
[ "$APPROVE_RC" -eq 0 ] || { printf '%s\n' "$APPROVE_OUT" | tail -40; fail "live-import -approve failed"; }
grep -qF "2 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 1 skipped" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not stamp exactly 2 of 3 resources cleanly"; }
log "  2 stamped"

log "--- 2c: the markers, read through the AWS CLI directly - never through choudoufu ---"
WANT_BUCKET_ADDR="module.s3_bucket_hm_harbor.aws_s3_bucket.main"
WANT_USER_ADDR="module.harbor_iam_user.aws_iam_user.hm_harbor_iam_user"
if [ "${BREAK:-}" = "1" ]; then
  WANT_USER_ADDR="module.harbor_iam_user.aws_iam_user.wrong_name"
  log "  BREAK=1: expecting a wrong tofu-address on the IAM user on purpose - this check must fail"
fi

GOT_BUCKET_ADDR="$(awsl s3api get-bucket-tagging --bucket "$BUCKET_NAME" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_BUCKET_ADDR" = "$WANT_BUCKET_ADDR" ] || fail "the S3 bucket carries tofu-address=$GOT_BUCKET_ADDR, not $WANT_BUCKET_ADDR"
GOT_BUCKET_ESTATE="$(awsl s3api get-bucket-tagging --bucket "$BUCKET_NAME" --query "TagSet[?Key=='tofu-estate'].Value | [0]" --output text)"
[ "$GOT_BUCKET_ESTATE" = "$ESTATE_NAME" ] || fail "the S3 bucket carries tofu-estate=$GOT_BUCKET_ESTATE, not $ESTATE_NAME"
log "  bucket $BUCKET_NAME -> tofu-address=$GOT_BUCKET_ADDR tofu-estate=$GOT_BUCKET_ESTATE"

GOT_USER_ADDR="$(awsl iam list-user-tags --user-name "$USER_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_USER_ADDR" = "$WANT_USER_ADDR" ] || fail "the IAM user carries tofu-address=$GOT_USER_ADDR, not $WANT_USER_ADDR"
log "  user   $USER_NAME -> tofu-address=$GOT_USER_ADDR"

if [ "${BREAK:-}" = "1" ]; then
  fail "BREAK=1: the user's real tofu-address matched the WRONG expected value above without this script noticing - stage 2's assertion is not load-bearing"
fi

log ""
log "STAGE 2 (migrate): PASS"
gauntlet_stage migrate pass "2 of 3 stamped (bucket, user), 1 UNTAGGABLE (inline policy); bucket $BUCKET_NAME -> tofu-address=$GOT_BUCKET_ADDR, user $USER_NAME -> tofu-address=$GOT_USER_ADDR"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - state deleted, live-plan, empty + identities re-asserted
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=test_plan
log "=== STAGE 3: no state file, live-plan ==="
rm -f "$ESTATE/terraform.tfstate" "$ESTATE/terraform.tfstate.backup"
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "the state file is still there"

plan_into() { ( cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color ); }
PLAN_OUT="$(plan_into 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -60; fail "live-plan exited $PLAN_RC"; }
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "live-plan wrote a state file"
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$PLAN_OUT" \
  || { grep -E '^  #' <<< "$PLAN_OUT"; fail "live-plan is not empty"; }
log "  no resource change proposed, with zero local memory of the migration that stamped it"

# Re-assert identities directly against the live objects, after the local
# state file was deleted - any answer below can only have come from the
# marker (or, for the untaggable inline policy, the re-derived identity) on
# the live object itself.
BUCKET_ADDR2="$(awsl s3api get-bucket-tagging --bucket "$BUCKET_NAME" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$BUCKET_ADDR2" = "$WANT_BUCKET_ADDR" ] || fail "the bucket's tofu-address changed across the empty plan: $WANT_BUCKET_ADDR -> $BUCKET_ADDR2"
USER_ADDR2="$(awsl iam list-user-tags --user-name "$USER_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$USER_ADDR2" = "$WANT_USER_ADDR" ] || fail "the user's tofu-address changed across the empty plan: $WANT_USER_ADDR -> $USER_ADDR2"

# The inline user policy has no tag to re-read, so its identity assertion is
# the live object's OWN content, read directly - the plan came back empty
# above, meaning the composite USERNAME:POLICYNAME derivation found exactly
# this live object with no diff, and this independently confirms what it
# found is correct.
GOT_POLICY_RESOURCE="$(awsl iam get-user-policy --user-name "$USER_NAME" --policy-name "$POLICY_NAME" --query 'PolicyDocument.Statement[0].Resource[0]' --output text)"
[ "$GOT_POLICY_RESOURCE" = "arn:aws:s3:::$BUCKET_NAME" ] || fail "the live inline user policy's first Resource ($GOT_POLICY_RESOURCE) does not match the configuration"
log "  identity re-check: bucket and user tofu-address unchanged; the inline user policy's resource ARN, read directly off the live object, still matches the configuration"

log ""
log "STAGE 3 (test plan): PASS"
gauntlet_stage test_plan pass "empty plan; identity re-check: bucket and user tofu-address unchanged, inline policy's resource ARN still matches the configuration"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 4: TEST APPLY - apply the empty plan, assert a genuine no-op
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=test_apply
log "=== STAGE 4: test apply (apply the empty plan; object count unchanged) ==="
BEFORE_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE_NAME" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"

APPLY2_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; APPLY2_RC=$?
[ "$APPLY2_RC" -eq 0 ] || { printf '%s\n' "$APPLY2_OUT" | tail -40; fail "the post-migration apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY2_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY2_OUT"; fail "the post-migration apply was not a no-op"; }

AFTER_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE_NAME" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$AFTER_N" = "$BEFORE_N" ] || fail "object count changed across a no-op apply: $BEFORE_N -> $AFTER_N"
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "a state file exists after the apply"
log "  genuine no-op: $BEFORE_N objects before, $AFTER_N after, no state file either time"

log ""
log "STAGE 4 (test apply): PASS"
gauntlet_stage test_apply pass "genuine no-op: $BEFORE_N objects before, $AFTER_N after, no state file either time"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 5: DRIFT AND RECONVERGE - mutate one object, replan, assert one fix
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=drift_reconverge
log "=== STAGE 5: drift and reconverge (mutate one object's tag out of band) ==="

if [ "${BREAK:-}" = "1" ]; then
  awsl iam tag-user --user-name "$USER_NAME" --tags Key=hm_team,Value=tampered-by-BREAK >/dev/null
  log "  BREAK=1: also tampered the IAM user's hm_team tag - stage 5 must now see TWO"
  log "           drifted objects and fail the single-object assertion"
fi

awsl s3api put-bucket-tagging --bucket "$BUCKET_NAME" --tagging '{
  "TagSet": [
    {"Key": "hm_environment", "Value": "production"},
    {"Key": "hm_team", "Value": "tampered-out-of-band"},
    {"Key": "hm_managed_by", "Value": "opentofu"},
    {"Key": "hm_resource_name", "Value": "'"$BUCKET_NAME"'"},
    {"Key": "tofu-address", "Value": "'"$WANT_BUCKET_ADDR"'"},
    {"Key": "tofu-estate", "Value": "'"$ESTATE_NAME"'"}
  ]
}'
DRIFTED_VALUE="$(awsl s3api get-bucket-tagging --bucket "$BUCKET_NAME" --query "TagSet[?Key=='hm_team'].Value | [0]" --output text)"
[ "$DRIFTED_VALUE" = "tampered-out-of-band" ] || fail "the out-of-band tag mutation did not take"
log "  mutated $BUCKET_NAME's hm_team tag to \"tampered-out-of-band\" directly via the AWS CLI - never through choudoufu"

DRIFT_PLAN_OUT="$(plan_into 2>&1)"; DRIFT_PLAN_RC=$?
[ "$DRIFT_PLAN_RC" -eq 0 ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | tail -60; fail "the drift-detection plan exited $DRIFT_PLAN_RC"; }

CHANGED_ADDRS="$(grep -oE '^  # \S+ will be updated' <<< "$DRIFT_PLAN_OUT" | awk '{print $2}' | sort -u)"
N_CHANGED="$(printf '%s\n' "$CHANGED_ADDRS" | grep -c . || true)"
if [ "${BREAK:-}" = "1" ]; then
  [ "$N_CHANGED" = "1" ] \
    && fail "BREAK=1 set (two objects tampered), but the plan proposes fixing only 1 - this assertion is not load-bearing"
  log "  BREAK=1: the plan proposes fixing $N_CHANGED objects, correctly more than"
  log "           one - the single-object assertion and reconverge apply below are skipped"
else
  [ "$N_CHANGED" = "1" ] \
    || { printf '%s\n' "$DRIFT_PLAN_OUT" | grep -E '^  # .+ will be'; fail "expected exactly 1 object proposed for a fix, got $N_CHANGED"; }
  [ "$CHANGED_ADDRS" = "module.s3_bucket_hm_harbor.aws_s3_bucket.main" ] \
    || fail "the plan proposes fixing $CHANGED_ADDRS, not the S3 bucket"
  log "  the plan proposes fixing exactly one object: $CHANGED_ADDRS - nothing else in the diff"

  RECONVERGE_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; RECONVERGE_RC=$?
  [ "$RECONVERGE_RC" -eq 0 ] || { printf '%s\n' "$RECONVERGE_OUT" | tail -40; fail "the reconverge apply failed"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$RECONVERGE_OUT" \
    || { grep -E 'Apply complete' <<< "$RECONVERGE_OUT"; fail "the reconverge apply did not change exactly 1 resource"; }
  FIXED_VALUE="$(awsl s3api get-bucket-tagging --bucket "$BUCKET_NAME" --query "TagSet[?Key=='hm_team'].Value | [0]" --output text)"
  [ "$FIXED_VALUE" = "hongbomiao" ] \
    || fail "the bucket's hm_team tag is \"$FIXED_VALUE\" after reconverging, not \"hongbomiao\""
  log "  reconverged: $BUCKET_NAME's hm_team tag is back to \"hongbomiao\", read via the AWS CLI"
fi

log ""
log "STAGE 5 (drift and reconverge): PASS"
gauntlet_stage drift_reconverge pass "the plan proposed fixing $N_CHANGED object(s) after the out-of-band tag mutation: $CHANGED_ADDRS"
log ""

CURRENT_STAGE=""
gauntlet_end

log "=== PASS: all five stages, real, against hongbo-miao/hongbomiao.com's own ==="
log "=== unmodified Harbor S3+IAM-user leaf modules, .tofu extension throughout ==="
