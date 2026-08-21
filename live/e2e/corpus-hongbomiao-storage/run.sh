#!/usr/bin/env bash
set -uo pipefail

# The five-stage real-estate crossing (live/corpus-crossing-manifest.json)
# for hongbo-miao/hongbomiao.com (live/corpus-manifest.json, pinned by
# commit - no tag, see that entry's own comment for why), a SECOND, disjoint
# slice of the same repository already crossed by
# live/e2e/corpus-hongbomiao-labelbox/run.sh - HANDOFF.md's own suggestion
# for broadening the OpenTofu-native lane without a fresh sourcing search,
# since estate-gen's own convention treats separate module examples/sections
# of one repository as legitimately separate crossing targets.
#
# THE SCOPING DECISION. hongbomiao.com's infrastructure/opentofu/environments
# tree is cross-wired through terraform_remote_state between its own
# network/general/storage/kubernetes environments (see corpus-hongbomiao-
# labelbox's own header) - too large and too coupled to stand up against
# floci in one sitting. Surveyed for a SECOND self-contained slice disjoint
# from the "Labelbox" section already crossed:
#
#   - environments/production/aws/general/main.tofu's other sections (Kafka
#     Manager, Amazon EMR, AWS Glue DataBrew, AWS Batch, Amazon SageMaker)
#     all either read another environment's terraform_remote_state output or
#     (Amazon SageMaker) call an AWS API floci genuinely does not implement -
#     confirmed directly against floci before writing this script: `aws
#     sagemaker create-notebook-instance` against floci returns
#     "UnknownOperationException: Operation CreateNotebookInstance is not
#     supported by floci", and the type has no entry anywhere in
#     live/floci-capabilities.json's Cloud Control sweep either. That is a
#     floci gap, not a choudoufu one, and not this script's to route around.
#   - environments/production/aws/storage/main.tofu opens with THREE module
#     calls that read no remote state at all, before the file's very next
#     section (`IoT Kafka`) starts reading network's remote state: the
#     shared production S3 bucket (`hm_production_bucket`), the Kafka KMS
#     key (`kafka_kms_key`, an aws_kms_key + aws_kms_alias pair), and a
#     second, independent S3 bucket for IoT data (`s3_bucket_iot_data`).
#     These three are what this script crosses - copied byte-identical from
#     the pinned commit (diffed below at DELTA), with this script's own root
#     wiring standing in for the remote-state-coupled environment root the
#     same way corpus-hongbomiao-labelbox's does for "general".
#
# What this slice contributes that Labelbox did not: TWO separate instances
# of the same leaf module (amazon_s3_bucket, called twice under different
# module names) rather than one, exercising address disambiguation between
# same-named child resources under different module calls; and aws_kms_key/
# aws_kms_alias, a server-assigned-ID taggable resource paired with a
# client-named untaggable one that re-derives its identity from its own
# `name` argument every run (ImportSyntax "alias/ALIASNAME",
# internal/live/identity/table_generated.go) - both already-ratified
# DefaultTable rows (unlike Labelbox's CORS configuration, which fell back
# to the provider's own identity schema), so no "no orphan recovery"
# warning is expected here.
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
#   bash live/e2e/corpus-hongbomiao-storage/run.sh
#
# Needs Docker, the AWS CLI, and the real `tofu` binary on PATH for stage 1
# (stock terraform cannot load a directory of .tofu files at all - see
# corpus-hongbomiao-labelbox's own header for the positive proof; not
# repeated here to keep this script shorter, but the same
# `terraform init` == "no configuration files" check is not needed twice in
# one campaign and is skipped here on purpose).
#
# Env overrides:
#   TOFU_BIN      path to a prebuilt choudoufu binary; skips the go build.
#   FLOCI_PORT    host port for the emulator (default 4725, clear of every
#                 other corpus-*/reference-* script's own default).
#   FLOCI_IMAGE   the emulator image; defaults to the digest pin in
#                 live/floci-image.
#   BREAK         set to 1 to corrupt stage 2's identity assertion and
#                 tamper a second object ahead of stage 5's, proving both
#                 are load-bearing.
#   DEBUG_KEEP    set to 1 to skip the exit trap: the floci container and
#                 the WORK directory are left behind for inspection.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SRC="$ROOT/.corpus/hongbomiao/infrastructure/opentofu/modules/aws"
WORK="$(mktemp -d)"
PLAIN="$WORK/plain"
ESTATE="$WORK/estate"
FLOCI_PORT="${FLOCI_PORT:-4725}"
FLOCI_NAME="choudoufu-corpus-hongbomiao-storage-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"
REGION="us-west-2"
ESTATE_NAME="hongbomiao-storage-crossing"
PROD_BUCKET_NAME="${ESTATE_NAME}-hm-production"
IOT_BUCKET_NAME="${ESTATE_NAME}-hm-iot-data"
KMS_KEY_NAME="${ESTATE_NAME}-hm-kafka-kms-key"
KMS_ALIAS_NAME="alias/${KMS_KEY_NAME}"

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
[ -n "${DEBUG_KEEP:-}" ] || trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }

# ── 0. tools and corpus ─────────────────────────────────────────────────────
log "=== 0. tools and corpus ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v tofu >/dev/null 2>&1 || fail "the real tofu binary is not on PATH - required for stage 1"
[ -d "$SRC/amazon_s3_bucket" ] && [ -d "$SRC/aws_kms_key" ] \
  || fail "$SRC is missing one of the two leaf modules - fetch hongbo-miao/hongbomiao.com at the pin in live/corpus-manifest.json first"
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
  mkdir -p "$dest/modules/aws"
  for m in amazon_s3_bucket aws_kms_key; do
    cp -R "$SRC/$m" "$dest/modules/aws/$m"
  done
}

# write_root <destdir> <live_block>: this crossing's own root wiring,
# standing in for the remote-state-coupled environment root (see header).
# The three module calls below use the SAME source paths and the SAME
# argument names as hongbomiao.com's own aws/storage/main.tofu's opening
# section, other than the estate-scoped bucket/key names. Written with a
# .tofu extension, matching every file this project's own real modules use.
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

# Amazon S3 bucket - hm-production-bucket
module "hm_production_bucket" {
  providers      = { aws = aws.production }
  source         = "./modules/aws/amazon_s3_bucket"
  s3_bucket_name = "$PROD_BUCKET_NAME"
  common_tags    = local.common_tags
}

# Kafka KMS key
module "kafka_kms_key" {
  source           = "./modules/aws/aws_kms_key"
  aws_kms_key_name = "$KMS_KEY_NAME"
  common_tags      = local.common_tags
}

# IoT data - S3 bucket
module "s3_bucket_iot_data" {
  providers      = { aws = aws.production }
  source         = "./modules/aws/amazon_s3_bucket"
  s3_bucket_name = "$IOT_BUCKET_NAME"
  common_tags    = local.common_tags
}
EOF
}

copy_leaf_modules "$PLAIN"
write_root "$PLAIN" ""
log "  two leaf modules copied unmodified out of .corpus/hongbomiao into $PLAIN"

# DELTA: confirm the copy is byte-identical to the pinned commit - the only
# things this crossing adds are its OWN root file and provider block, never
# an edit to hongbomiao.com's own module code.
for m in amazon_s3_bucket aws_kms_key; do
  diff -rq "$SRC/$m" "$PLAIN/modules/aws/$m" >/dev/null \
    || fail "modules/aws/$m differs from the pinned commit - this crossing must run the real, unmodified module"
done
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
  grep -q '"s3"' <<< "${HEALTH:-}" && grep -q '"kms"' <<< "${HEALTH:-}" && break
  sleep 2
done
grep -q '"s3"' <<< "${HEALTH:-}" && grep -q '"kms"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (s3/kms) at $ENDPOINT"
log "  healthy"

export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" AWS_ENDPOINT_URL="$ENDPOINT"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 1: COLD DEPLOY - plain tofu apply, no live block, no choudoufu
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 1: cold deploy (plain tofu apply, the real unmodified modules) ==="
( cd "$PLAIN" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "stage 1 init failed"; }
COLD_OUT="$(cd "$PLAIN" && tofu apply -input=false -auto-approve -no-color 2>&1)"; COLD_RC=$?
[ "$COLD_RC" -eq 0 ] || { printf '%s\n' "$COLD_OUT" | tail -40; fail "stage 1 (cold deploy) failed"; }
grep -qE 'Apply complete! Resources: 4 added, 0 changed, 0 destroyed' <<< "$COLD_OUT" \
  || { grep -E 'Apply complete' <<< "$COLD_OUT"; fail "stage 1 did not create exactly 4 resources"; }
log "  $(grep -E 'Apply complete' <<< "$COLD_OUT")"
[ -f "$PLAIN/terraform.tfstate" ] || fail "stage 1 left no state file to migrate from"

UNMARKED="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE_NAME" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$UNMARKED" = "0" ] || fail "plain tofu's own objects already carry tofu-estate=$ESTATE_NAME before migration - this crossing proves nothing"
log "  confirmed unmarked: 0 objects carry tofu-estate=$ESTATE_NAME before migration"

log ""
log "STAGE 1 (cold deploy): PASS"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 2: choudoufu live-import ==="
( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "estate init failed"; }

log "--- 2a: live-import, read-only first ---"
IMPORT_OUT="$(cd "$ESTATE" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE_NAME" -no-color 2>&1)"; IMPORT_RC=$?
[ "$IMPORT_RC" -eq 0 ] || { printf '%s\n' "$IMPORT_OUT" | tail -40; fail "live-import (dry run) failed"; }
grep -qF "3 of 4 resource instance(s) are eligible for stamping" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import did not verify exactly 3 of 4 as eligible (the two S3 buckets and the KMS key - the KMS alias is correctly UNTAGGABLE, see header)"; }
grep -qF "No tag has been written." <<< "$IMPORT_OUT" || fail "the dry run wrote a tag - it must not"
grep -qF "UNTAGGABLE (1)" <<< "$IMPORT_OUT" \
  || fail "expected exactly 1 UNTAGGABLE resource (the KMS alias)"
log "  3 of 4 verified against the live system; nothing written yet"

log "--- 2b: -approve ---"
APPROVE_OUT="$(cd "$ESTATE" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE_NAME" -approve -no-color 2>&1)"; APPROVE_RC=$?
[ "$APPROVE_RC" -eq 0 ] || { printf '%s\n' "$APPROVE_OUT" | tail -40; fail "live-import -approve failed"; }
grep -qF "3 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 1 skipped" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not stamp exactly 3 of 4 resources cleanly"; }
log "  3 stamped"

log "--- 2c: the markers, read through the AWS CLI directly - never through choudoufu ---"
WANT_PROD_ADDR="module.hm_production_bucket.aws_s3_bucket.main"
WANT_IOT_ADDR="module.s3_bucket_iot_data.aws_s3_bucket.main"
WANT_KMS_ADDR="module.kafka_kms_key.aws_kms_key.main"
if [ "${BREAK:-}" = "1" ]; then
  WANT_KMS_ADDR="module.kafka_kms_key.aws_kms_key.wrong_name"
  log "  BREAK=1: expecting a wrong tofu-address on the KMS key on purpose - this check must fail"
fi

GOT_PROD_ADDR="$(awsl s3api get-bucket-tagging --bucket "$PROD_BUCKET_NAME" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_PROD_ADDR" = "$WANT_PROD_ADDR" ] || fail "the production bucket carries tofu-address=$GOT_PROD_ADDR, not $WANT_PROD_ADDR"
GOT_IOT_ADDR="$(awsl s3api get-bucket-tagging --bucket "$IOT_BUCKET_NAME" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_IOT_ADDR" = "$WANT_IOT_ADDR" ] || fail "the IoT-data bucket carries tofu-address=$GOT_IOT_ADDR, not $WANT_IOT_ADDR"
log "  bucket $PROD_BUCKET_NAME -> tofu-address=$GOT_PROD_ADDR"
log "  bucket $IOT_BUCKET_NAME -> tofu-address=$GOT_IOT_ADDR"

KMS_KEY_ID="$(awsl kms list-keys --query 'Keys[0].KeyId' --output text)"
[ -n "$KMS_KEY_ID" ] && [ "$KMS_KEY_ID" != "None" ] || fail "no KMS key was created"
# KMS's own list-resource-tags answers TagKey/TagValue, not the Key/Value
# shape every other service here uses.
GOT_KMS_ADDR="$(awsl kms list-resource-tags --key-id "$KMS_KEY_ID" --query "Tags[?TagKey=='tofu-address'].TagValue | [0]" --output text)"
[ "$GOT_KMS_ADDR" = "$WANT_KMS_ADDR" ] || fail "the KMS key carries tofu-address=$GOT_KMS_ADDR, not $WANT_KMS_ADDR"
log "  key    $KMS_KEY_ID -> tofu-address=$GOT_KMS_ADDR"

if [ "${BREAK:-}" = "1" ]; then
  fail "BREAK=1: the KMS key's real tofu-address matched the WRONG expected value above without this script noticing - stage 2's assertion is not load-bearing"
fi

log ""
log "STAGE 2 (migrate): PASS"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - state deleted, live-plan, empty + identities re-asserted
# ══════════════════════════════════════════════════════════════════════════
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
# marker (or, for the KMS alias, the re-derived identity) on the live
# object itself.
PROD_ADDR2="$(awsl s3api get-bucket-tagging --bucket "$PROD_BUCKET_NAME" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$PROD_ADDR2" = "$WANT_PROD_ADDR" ] || fail "the production bucket's tofu-address changed across the empty plan: $WANT_PROD_ADDR -> $PROD_ADDR2"
IOT_ADDR2="$(awsl s3api get-bucket-tagging --bucket "$IOT_BUCKET_NAME" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$IOT_ADDR2" = "$WANT_IOT_ADDR" ] || fail "the IoT-data bucket's tofu-address changed across the empty plan: $WANT_IOT_ADDR -> $IOT_ADDR2"
KMS_ADDR2="$(awsl kms list-resource-tags --key-id "$KMS_KEY_ID" --query "Tags[?TagKey=='tofu-address'].TagValue | [0]" --output text)"
[ "$KMS_ADDR2" = "$WANT_KMS_ADDR" ] || fail "the KMS key's tofu-address changed across the empty plan: $WANT_KMS_ADDR -> $KMS_ADDR2"

# The KMS alias has no tag to re-read, so its identity assertion is the live
# object's OWN content, read directly - the plan came back empty above,
# meaning the client-named derivation (its own `name` argument) found
# exactly this live object with no diff, and this independently confirms
# what it found is correct.
GOT_ALIAS_TARGET="$(awsl kms list-aliases --query "Aliases[?AliasName=='$KMS_ALIAS_NAME'].TargetKeyId | [0]" --output text)"
[ "$GOT_ALIAS_TARGET" = "$KMS_KEY_ID" ] || fail "the live KMS alias $KMS_ALIAS_NAME does not point at $KMS_KEY_ID (got $GOT_ALIAS_TARGET)"
log "  identity re-check: both buckets' and the key's tofu-address unchanged; the KMS alias, read directly off the live object, still points at the same key"

log ""
log "STAGE 3 (test plan): PASS"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 4: TEST APPLY - apply the empty plan, assert a genuine no-op
# ══════════════════════════════════════════════════════════════════════════
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
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 5: DRIFT AND RECONVERGE - mutate one object, replan, assert one fix
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 5: drift and reconverge (mutate one object's tag out of band) ==="

if [ "${BREAK:-}" = "1" ]; then
  awsl kms tag-resource --key-id "$KMS_KEY_ID" --tags TagKey=hm_team,TagValue=tampered-by-BREAK >/dev/null
  log "  BREAK=1: also tampered the KMS key's hm_team tag - stage 5 must now see TWO"
  log "           drifted objects and fail the single-object assertion"
fi

awsl s3api put-bucket-tagging --bucket "$IOT_BUCKET_NAME" --tagging '{
  "TagSet": [
    {"Key": "hm_environment", "Value": "production"},
    {"Key": "hm_team", "Value": "tampered-out-of-band"},
    {"Key": "hm_managed_by", "Value": "opentofu"},
    {"Key": "hm_resource_name", "Value": "'"$IOT_BUCKET_NAME"'"},
    {"Key": "tofu-address", "Value": "'"$WANT_IOT_ADDR"'"},
    {"Key": "tofu-estate", "Value": "'"$ESTATE_NAME"'"}
  ]
}'
DRIFTED_VALUE="$(awsl s3api get-bucket-tagging --bucket "$IOT_BUCKET_NAME" --query "TagSet[?Key=='hm_team'].Value | [0]" --output text)"
[ "$DRIFTED_VALUE" = "tampered-out-of-band" ] || fail "the out-of-band tag mutation did not take"
log "  mutated $IOT_BUCKET_NAME's hm_team tag to \"tampered-out-of-band\" directly via the AWS CLI - never through choudoufu"

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
  [ "$CHANGED_ADDRS" = "module.s3_bucket_iot_data.aws_s3_bucket.main" ] \
    || fail "the plan proposes fixing $CHANGED_ADDRS, not the IoT-data bucket"
  log "  the plan proposes fixing exactly one object: $CHANGED_ADDRS - nothing else in the diff"

  RECONVERGE_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; RECONVERGE_RC=$?
  [ "$RECONVERGE_RC" -eq 0 ] || { printf '%s\n' "$RECONVERGE_OUT" | tail -40; fail "the reconverge apply failed"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$RECONVERGE_OUT" \
    || { grep -E 'Apply complete' <<< "$RECONVERGE_OUT"; fail "the reconverge apply did not change exactly 1 resource"; }
  FIXED_VALUE="$(awsl s3api get-bucket-tagging --bucket "$IOT_BUCKET_NAME" --query "TagSet[?Key=='hm_team'].Value | [0]" --output text)"
  [ "$FIXED_VALUE" = "hongbomiao" ] \
    || fail "the bucket's hm_team tag is \"$FIXED_VALUE\" after reconverging, not \"hongbomiao\""
  log "  reconverged: $IOT_BUCKET_NAME's hm_team tag is back to \"hongbomiao\", read via the AWS CLI"
fi

log ""
log "STAGE 5 (drift and reconverge): PASS"
log ""

log "=== PASS: all five stages, real, against hongbo-miao/hongbomiao.com's own ==="
log "=== unmodified storage-environment leaf modules, .tofu extension throughout ==="
