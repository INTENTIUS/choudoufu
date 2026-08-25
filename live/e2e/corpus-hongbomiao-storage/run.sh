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
#                 are load-bearing. Set to "rename" to exercise day2_rename's
#                 own break control instead - renaming module kafka_kms_key
#                 WITHOUT a moved block, which must refuse with a marker-
#                 ambiguity error rather than reproduce the real legs'
#                 zero-churn result.
#   BREAK_REMOVE  set to 1 to run day2_remove's own break control instead of
#                 the real remove checks: keep module.kafka_kms_key_renamed's
#                 block in the config and assert no destroy is proposed for
#                 it. Independent of BREAK and only reachable when BREAK is
#                 not 1, because day2_remove starts from day2_rename's own
#                 real, completed rename.
#   BREAK_GREEN   set to 1 to run the greenfield stage's own break control
#                 instead of the real object-by-object comparison: drop one
#                 object from the actual inventory before the count check.
#                 Independent of the other BREAK flags - greenfield runs
#                 before all of them, right after STAGE 1's cold deploy.
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
  docker rm -f "$FLOCI_NAME" "${FLOCI_GREEN_NAME:-}" >/dev/null 2>&1 || true
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
CURRENT_STAGE=cold_deploy
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
gauntlet_stage cold_deploy pass "$(grep -E 'Apply complete' <<< "$COLD_OUT"); 0 objects carry tofu-estate=$ESTATE_NAME before migration"
log ""

# ══════════════════════════════════════════════════════════════════════════
# PART D-ORACLE: RENAME, stock oracle (day2_rename, live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
#
# Two of the three module calls: a `moved` block renames the WHOLE module
# call "hm_production_bucket" (its own aws_s3_bucket.main is the only
# taggable object it carries), and "choudoufu live-mv" (below, after
# drift_reconverge) renames the whole module call "kafka_kms_key" with no
# moved block at all - its aws_kms_key.main is the only taggable object;
# its sibling aws_kms_alias.main carries no `tags` argument in the provider
# schema and is untaggable by design, the same as harbor's inline policy
# and labelbox's CORS configuration. "s3_bucket_iot_data" is left untouched
# as a negative control (it is also stage 5's own drifted object). Neither
# leaf module's own source is touched (DELTA discipline). Both S3 bucket
# modules carry `lifecycle { prevent_destroy = true }` on aws_s3_bucket.main
# in the real module, so BREAK=rename's rename-without-moved control below
# renames the KMS key, never a bucket. The stock oracle (real tofu - stock
# terraform cannot see this .tofu-only estate at all, see header) runs the
# same two renames, through moved blocks only, on a copy of cold_deploy's
# own state - before choudoufu or live-import ever touch these objects.
CURRENT_STAGE=day2_rename
log "=== D-ORACLE: stock tofu, the same two renames through moved blocks, on cold_deploy's own state ==="
PLAIN_ORACLE="$WORK/plain-oracle"
cp -r "$PLAIN" "$PLAIN_ORACLE"
sed -i.bak 's/module "hm_production_bucket" {/module "hm_production_bucket_renamed" {/' "$PLAIN_ORACLE/main.tofu"
sed -i.bak 's/module "kafka_kms_key" {/module "kafka_kms_key_renamed" {/' "$PLAIN_ORACLE/main.tofu"
rm -f "$PLAIN_ORACLE/main.tofu.bak"
cat >> "$PLAIN_ORACLE/main.tofu" <<'EOF'

moved {
  from = module.hm_production_bucket
  to   = module.hm_production_bucket_renamed
}

moved {
  from = module.kafka_kms_key
  to   = module.kafka_kms_key_renamed
}
EOF
( cd "$PLAIN_ORACLE" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_ORACLE" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_rename stock oracle's reinit failed"; }
ORACLE_PLAN_OUT="$(cd "$PLAIN_ORACLE" && tofu plan -input=false -no-color 2>&1)"; ORACLE_PLAN_RC=$?
[ "$ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -40; fail "the day2_rename stock oracle plan exited $ORACLE_PLAN_RC"; }
grep -qE '^  # .+ will be (destroyed|created)' <<< "$ORACLE_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock proposes a destroy or create for a rename carried entirely by moved blocks - the oracle itself is not zero-churn"; }
grep -qF 'Plan: 0 to add, 0 to change, 0 to destroy.' <<< "$ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -10; fail "stock's rename plan is not a true no-op"; }
log "  stock: zero churn on cold_deploy's own state - both moves report only their move, no attribute diff at all"

# ══════════════════════════════════════════════════════════════════════════
# PART E-ORACLE: REMOVE A BLOCK, stock oracle (day2_remove, live/GAUNTLET.md
# #7). Another separate copy of cold_deploy's own state. Removes
# module.kafka_kms_key's block entirely - it references nothing else and
# nothing references it (s3_bucket_iot_data and hm_production_bucket are
# both fully independent, see header), and it is the one taggable root in
# this estate WITHOUT `lifecycle { prevent_destroy = true }` - both buckets
# carry it (the real amazon_s3_bucket module, shared with the harbor and
# labelbox crossings), so kafka_kms_key is the only real destroy day2_remove
# can exercise here. The module holds TWO resources - aws_kms_key (taggable)
# and aws_kms_alias (untaggable, client-named from its own `name` argument,
# not parent-derived - see header) - so removing its block also has to
# destroy the alias, in an order the cloud accepts.
CURRENT_STAGE=day2_remove
log "=== E-ORACLE: stock tofu, delete module.kafka_kms_key's block on cold_deploy's own state ==="
PLAIN_REMOVE_ORACLE="$WORK/plain-remove-oracle"
cp -r "$PLAIN" "$PLAIN_REMOVE_ORACLE"
perl -0pi -e 's/\n# Kafka KMS key\nmodule "kafka_kms_key" \{.*?\n\}\n//s' "$PLAIN_REMOVE_ORACLE/main.tofu"
grep -q 'module "kafka_kms_key"' "$PLAIN_REMOVE_ORACLE/main.tofu" \
  && fail "removing module.kafka_kms_key's block from the oracle copy did not match - this script's own root wiring has moved"
( cd "$PLAIN_REMOVE_ORACLE" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_REMOVE_ORACLE" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_remove stock oracle's reinit failed"; }
REMOVE_ORACLE_PLAN_OUT="$(cd "$PLAIN_REMOVE_ORACLE" && tofu plan -input=false -no-color 2>&1)"; REMOVE_ORACLE_PLAN_RC=$?
[ "$REMOVE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_remove stock oracle plan exited $REMOVE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.kafka_kms_key\.aws_kms_key\.main will be destroyed' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "stock does not propose destroying the KMS key when module.kafka_kms_key's block is removed"; }
grep -qE '^  # module\.kafka_kms_key\.aws_kms_alias\.main will be destroyed' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "stock does not propose destroying the KMS alias when module.kafka_kms_key's block is removed"; }
grep -qF 'Plan: 0 to add, 0 to change, 2 to destroy.' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -10; fail "stock's remove plan proposes something other than exactly two destroys (the key and its alias)"; }
log "  stock: exactly two destroys (the KMS key and its alias), nothing else, on the state cold_deploy produced"

# ══════════════════════════════════════════════════════════════════════════
# PART GREENFIELD (greenfield, live/GAUNTLET.md #13, active stage)
# ══════════════════════════════════════════════════════════════════════════
#
# One more, fresh floci container. STAGE 1's own container ($FLOCI_NAME on
# $FLOCI_PORT) is reused as THIS stage's oracle: nothing between cold_deploy
# and here has applied, changed or destroyed anything in it - the
# day2_rename and day2_remove oracle blocks above only run `tofu plan`
# against COPIES of cold_deploy's state (never `apply`), so it still holds
# exactly stock's unmodified, unmarked cold-deploy inventory. Greenfield
# applies the identical, unmodified leaf modules (a live block added,
# nothing else) into a namespace of its own with choudoufu directly - no
# migration at all. This estate is multi-provider (both buckets under
# aws.production, the KMS key/alias under the default, unaliased aws), so
# the object-by-object comparison below reads BOTH provider namespaces on
# both endpoints - the same $ENDPOINT/$GREEN_ENDPOINT floci container
# either way, since the alias distinguishes provider CONFIGURATION, not
# provider ENDPOINT, and both configurations point at the same floci here.
# The SAME bucket/key names are reused (a fresh, isolated floci container
# is a separate account, so there is no collision).
FLOCI_GREEN_PORT=$((FLOCI_PORT + 1))
FLOCI_GREEN_NAME="choudoufu-corpus-hongbomiao-storage-green-$$"
GREEN_ENDPOINT="http://127.0.0.1:${FLOCI_GREEN_PORT}"
GREEN_ESTATE_NAME="hongbomiao-storage-greenfield"

docker run -d --rm -p "${FLOCI_GREEN_PORT}:4566" --name "$FLOCI_GREEN_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_GREEN_NAME failed"
for _ in $(seq 1 45); do
  GREEN_HEALTH="$(curl -fs "${GREEN_ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"s3"' <<< "${GREEN_HEALTH:-}" && grep -q '"kms"' <<< "${GREEN_HEALTH:-}" && break
  sleep 2
done
grep -q '"s3"' <<< "${GREEN_HEALTH:-}" && grep -q '"kms"' <<< "${GREEN_HEALTH:-}" \
  || fail "the greenfield floci did not come up healthy (s3/kms) at $GREEN_ENDPOINT"
log "  healthy: greenfield=$GREEN_ENDPOINT"

GREEN="$WORK/greenfield"
copy_leaf_modules "$GREEN"
write_root "$GREEN" '
  live {
    estate = "'"$GREEN_ESTATE_NAME"'"
    record_store "local" {
      path = ".tofu-records"
    }
  }'
log "  greenfield estate written to $GREEN (same two unmodified leaf modules, a live block from the start)"

CURRENT_STAGE=greenfield
log "=== PART GREENFIELD 1. choudoufu apply directly, no migration ==="
( cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the greenfield init failed"; }
GREEN_APPLY_OUT="$(cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; GREEN_APPLY_RC=$?
[ "$GREEN_APPLY_RC" -eq 0 ] || { printf '%s\n' "$GREEN_APPLY_OUT" | tail -40; fail "the greenfield apply failed"; }
grep -qE 'Apply complete! Resources: 4 added' <<< "$GREEN_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT"; fail "the greenfield apply did not create exactly 4 resources"; }
log "  $(grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT")"

log "=== PART GREENFIELD 2. markers, read through the AWS CLI directly ==="
awslg() { aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" "$@"; }
GREEN_WANT_PROD_ADDR="module.hm_production_bucket.aws_s3_bucket.main"
GREEN_WANT_IOT_ADDR="module.s3_bucket_iot_data.aws_s3_bucket.main"
GREEN_WANT_KMS_ADDR="module.kafka_kms_key.aws_kms_key.main"
GREEN_PROD_ADDR="$(awslg s3api get-bucket-tagging --bucket "$PROD_BUCKET_NAME" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GREEN_PROD_ADDR" = "$GREEN_WANT_PROD_ADDR" ] || fail "the greenfield production bucket carries tofu-address=$GREEN_PROD_ADDR, not $GREEN_WANT_PROD_ADDR"
GREEN_IOT_ADDR="$(awslg s3api get-bucket-tagging --bucket "$IOT_BUCKET_NAME" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GREEN_IOT_ADDR" = "$GREEN_WANT_IOT_ADDR" ] || fail "the greenfield IoT bucket carries tofu-address=$GREEN_IOT_ADDR, not $GREEN_WANT_IOT_ADDR"
GREEN_KMS_KEY_ID="$(awslg kms list-aliases --query "Aliases[?AliasName=='alias/$KMS_KEY_NAME'].TargetKeyId | [0]" --output text)"
[ -n "$GREEN_KMS_KEY_ID" ] && [ "$GREEN_KMS_KEY_ID" != "None" ] || fail "could not find the greenfield KMS key through its alias via the AWS CLI"
GREEN_KMS_ADDR="$(awslg kms list-resource-tags --key-id "$GREEN_KMS_KEY_ID" --query "Tags[?TagKey=='tofu-address'].TagValue | [0]" --output text)"
[ "$GREEN_KMS_ADDR" = "$GREEN_WANT_KMS_ADDR" ] || fail "the greenfield KMS key carries tofu-address=$GREEN_KMS_ADDR, not $GREEN_WANT_KMS_ADDR"
log "  both buckets and the KMS key carry their expected tofu-address, read via the AWS CLI, not choudoufu's own report"

log "=== PART GREENFIELD 3. the local record store holds one record per instance, taggable and untaggable alike (#364 A2) ==="
GREEN_RECORD_FILES="$(find "$GREEN/.tofu-records/tofu-records" -type f ! -name '*.lock' ! -name '*.tmp-*' 2>/dev/null | wc -l | tr -d ' ')"
[ "$GREEN_RECORD_FILES" = "4" ] || fail "expected 4 records under the local record store after the greenfield apply (2 buckets, the KMS key, the untaggable alias), found $GREEN_RECORD_FILES"
log "  4 records persisted, one per managed instance including the untaggable alias, read directly off the local record store"

log "=== PART GREENFIELD 4. the next plan proposes nothing ==="
GREEN_PLAN_OUT="$(cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; GREEN_PLAN_RC=$?
[ "$GREEN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$GREEN_PLAN_OUT" | tail -30; fail "the greenfield replan exited $GREEN_PLAN_RC"; }
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$GREEN_PLAN_OUT" \
  || { grep -E '^  #' <<< "$GREEN_PLAN_OUT"; fail "the greenfield replan is not empty"; }
log "  No changes."

log "=== PART GREENFIELD 5. delete the local record store; plan a third time ==="
rm -rf "$GREEN/.tofu-records"
GREEN_PLAN2_OUT="$(cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; GREEN_PLAN2_RC=$?
[ "$GREEN_PLAN2_RC" -eq 0 ] || { printf '%s\n' "$GREEN_PLAN2_OUT" | tail -30; fail "the third greenfield plan (no local record store) exited $GREEN_PLAN2_RC"; }
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$GREEN_PLAN2_OUT" \
  || { grep -E '^  #' <<< "$GREEN_PLAN2_OUT"; fail "the third greenfield plan is not empty with no local record store - the objects are not being found by their tags alone"; }
log "  No changes, with zero local memory of the run that created them"

log "=== PART GREENFIELD 6. object-by-object against stock's own cold-deploy container (STAGE 1, untouched since), per provider namespace ==="
GREEN_TAGGABLE_COUNT="$(awslg resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$GREEN_ESTATE_NAME" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$GREEN_TAGGABLE_COUNT" = "3" ] || fail "the greenfield estate has $GREEN_TAGGABLE_COUNT taggable objects, expected 3 (both buckets and the KMS key)"
GREEN_ALIAS_TARGET="$(awslg kms list-aliases --query "Aliases[?AliasName=='alias/$KMS_KEY_NAME'].TargetKeyId | [0]" --output text 2>/dev/null || true)"
GREEN_TOTAL_COUNT=3
[ -n "$GREEN_ALIAS_TARGET" ] && [ "$GREEN_ALIAS_TARGET" != "None" ] && GREEN_TOTAL_COUNT=4
if [ "${BREAK_GREEN:-}" = "1" ]; then
  GREEN_TOTAL_COUNT=$((GREEN_TOTAL_COUNT - 1))
  log "  BREAK_GREEN=1: dropped one object from the actual inventory - the count comparison below must fail"
fi
[ "$GREEN_TOTAL_COUNT" = "4" ] \
  || fail "the greenfield estate has $GREEN_TOTAL_COUNT objects (3 taggable plus the KMS alias, if readable), expected 4 - the object-by-object comparison against stock's cold deploy must fail on a dropped resource"
# aws.production namespace: both buckets, object by object against the same
# provider config on the untouched cold-deploy endpoint.
GREEN_PROD_LOCATION="$(awslg s3api get-bucket-location --bucket "$PROD_BUCKET_NAME" --query 'LocationConstraint' --output text 2>&1 || true)"
COLD_PROD_LOCATION="$(awsl s3api get-bucket-location --bucket "$PROD_BUCKET_NAME" --query 'LocationConstraint' --output text 2>&1 || true)"
[ "$GREEN_PROD_LOCATION" = "$COLD_PROD_LOCATION" ] || fail "the production bucket's location differs between the greenfield estate and stock's cold deploy"
GREEN_IOT_LOCATION="$(awslg s3api get-bucket-location --bucket "$IOT_BUCKET_NAME" --query 'LocationConstraint' --output text 2>&1 || true)"
COLD_IOT_LOCATION="$(awsl s3api get-bucket-location --bucket "$IOT_BUCKET_NAME" --query 'LocationConstraint' --output text 2>&1 || true)"
[ "$GREEN_IOT_LOCATION" = "$COLD_IOT_LOCATION" ] || fail "the IoT bucket's location differs between the greenfield estate and stock's cold deploy"
# default (unaliased) aws namespace: the KMS key and its alias.
COLD_KMS_KEY_ID="$(awsl kms list-aliases --query "Aliases[?AliasName=='alias/$KMS_KEY_NAME'].TargetKeyId | [0]" --output text)"
GREEN_KMS_KEY_DESC="$(awslg kms describe-key --key-id "$GREEN_KMS_KEY_ID" --query 'KeyMetadata.KeyUsage' --output text)"
COLD_KMS_KEY_DESC="$(awsl kms describe-key --key-id "$COLD_KMS_KEY_ID" --query 'KeyMetadata.KeyUsage' --output text)"
[ "$GREEN_KMS_KEY_DESC" = "$COLD_KMS_KEY_DESC" ] || fail "the KMS key's usage differs between the greenfield estate and stock's cold deploy"
[ "$GREEN_ALIAS_TARGET" = "$GREEN_KMS_KEY_ID" ] || fail "the greenfield alias does not target the greenfield key"
log "  3 taggable objects plus the KMS alias match stock's cold-deploy container object by object (bucket locations under aws.production, KMS key usage and alias target under the default aws provider), marker tags never compared"

log ""
log "PART GREENFIELD (greenfield): PASS"
gauntlet_stage greenfield pass "4 resources from nothing (2 buckets under aws.production, KMS key and untaggable alias under the default aws provider), markers verified via the AWS CLI, 4 records in the local record store (#364 A2), replan empty both with and without the local record store, all objects match stock's cold-deploy container (STAGE 1, untouched) object by object per provider namespace, marker tags never compared"
log ""
CURRENT_STAGE=""
docker rm -f "$FLOCI_GREEN_NAME" >/dev/null 2>&1 || true

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
gauntlet_stage migrate pass "3 of 4 stamped (2 buckets, KMS key), 1 UNTAGGABLE (KMS alias); bucket $PROD_BUCKET_NAME -> tofu-address=$GOT_PROD_ADDR, bucket $IOT_BUCKET_NAME -> tofu-address=$GOT_IOT_ADDR, key $KMS_KEY_ID -> tofu-address=$GOT_KMS_ADDR"
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
gauntlet_stage test_plan pass "empty plan; identity re-check: both buckets' and the key's tofu-address unchanged, KMS alias still points at the same key"
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
gauntlet_stage drift_reconverge pass "the plan proposed fixing $N_CHANGED object(s) after the out-of-band tag mutation: $CHANGED_ADDRS"
log ""

# ══════════════════════════════════════════════════════════════════════════
# PART D: RENAME (day2_rename, live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=day2_rename
log "=== D0. capture the live ids a rename must not disturb ==="
log "  bucket $PROD_BUCKET_NAME (module.hm_production_bucket), key $KMS_KEY_ID (module.kafka_kms_key)"

if [ "${BREAK:-}" = "rename" ]; then
  log "=== D1 (BREAK=rename). rename module kafka_kms_key -> kafka_kms_key_renamed WITHOUT a moved block ==="
  sed -i.bak 's/module "kafka_kms_key" {/module "kafka_kms_key_renamed" {/' "$ESTATE/main.tofu"
  rm -f "$ESTATE/main.tofu.bak"
  ( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the BREAK=rename reinit failed"; }
  BREAK_PLAN_OUT="$(plan_into 2>&1)"; BREAK_PLAN_RC=$?
  # GitHub issue #403 part 2. This estate declares its KMS key module under
  # the DEFAULT "aws" provider while its sibling S3 bucket modules use the
  # "aws.production" alias (see the provider blocks above) - a genuine
  # multi-provider estate in internal/live/discovery's own terms (issue
  # #69), even though both configurations point at the same region in this
  # test. Before the #403 fix, renaming the KMS key's module without a
  # moved block made TWO discovery passes independently find the SAME live
  # key as an orphan under its old address - the pass that declares
  # aws_kms_key, via its own native scan, and the pass that does not, via
  # its own sweep - and Merge's crossProviderOrphanCollisions treated any
  # address two distinct passes agreed on as a cross-region ownership
  # collision without checking whether they were the SAME live object. The
  # refusal that produced ("Two live resources claiming one address",
  # printing this one key's id twice) was itself the safe outcome HANDOFF's
  # rule wants, but the diagnostic was wrong: an import ID identifies one
  # physical object, so agreement on one ID from two passes can never be
  # the two-different-objects ambiguity that message describes.
  #
  # Fixed, discovery now dedupes by live import ID before deciding whether
  # a cross-provider collision exists at all: with only one live object in
  # play, this reduces to exactly corpus-hongbomiao-harbor's own
  # aws_iam_user rename shape (a still-marked live object under an address
  # nothing declares any more, with a declared-but-unclaimed sibling
  # instance at the new address) - a clean plan, RC 0, that proposes
  # CREATING the renamed instance and never proposes destroying the old
  # marked object. Verified directly, stable across repeated runs, and
  # covered independently at the unit level by
  # TestMergeSameLiveObjectAcrossPassesIsNotACollision and
  # TestMergeSameLiveObjectAcrossPassesPrefersAPendingRename
  # (internal/live/discovery/multiprovider_test.go).
  [ "$BREAK_PLAN_RC" -eq 0 ] \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -40; fail "BREAK=rename: the plan exited $BREAK_PLAN_RC - expected a clean exit now that the same live object is no longer misreported as two colliding claimants (see the PR, GitHub issue #403)"; }
  grep -qE '^  # module\.kafka_kms_key\.aws_kms_key\.main will be destroyed' <<< "$BREAK_PLAN_OUT" \
    && { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=rename: the plan proposes destroying the live KMS key under its old address - a wrong marker could have been written"; }
  grep -qF "Two live resources claiming one address" <<< "$BREAK_PLAN_OUT" \
    && { printf '%s\n' "$BREAK_PLAN_OUT" | tail -40; fail "BREAK=rename: the plan still reports the fixed one-live-object-two-passes collision - the #403 dedup regressed"; }
  grep -qE '^  # module\.kafka_kms_key_renamed\.aws_kms_key\.main will be created' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=rename: renaming without a moved block neither refused nor proposed creating the renamed instance - this stage's check is not load-bearing"; }
  grep -qF ', 0 to destroy.' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -10; fail "BREAK=rename: the plan proposes a destroy"; }
  log "  BREAK=rename: never destroys the live KMS key's old marker; proposes creating module.kafka_kms_key_renamed.aws_kms_key.main, same shape as corpus-hongbomiao-harbor's aws_iam_user rename - proves the moved-block/live-mv checks below are load-bearing (see the PR, GitHub issue #403, for the dedup fix)"
else
  log "=== D1. choudoufu, moved block: module hm_production_bucket -> hm_production_bucket_renamed ==="
  sed -i.bak 's/module "hm_production_bucket" {/module "hm_production_bucket_renamed" {/' "$ESTATE/main.tofu"
  rm -f "$ESTATE/main.tofu.bak"
  cat >> "$ESTATE/main.tofu" <<'EOF'

moved {
  from = module.hm_production_bucket
  to   = module.hm_production_bucket_renamed
}
EOF
  # Renaming a MODULE CALL (not a resource label) changes the module
  # instance registry .terraform tracks, unlike a plain resource rename -
  # a re-init is required even though the source path itself is unchanged.
  ( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the moved-block rename's reinit failed"; }
  MOVED_PLAN_OUT="$(plan_into 2>&1)"; MOVED_PLAN_RC=$?
  [ "$MOVED_PLAN_RC" -eq 0 ] || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -40; fail "the moved-block rename plan exited $MOVED_PLAN_RC"; }
  grep -qE '^  # .+ will be (destroyed|created)' <<< "$MOVED_PLAN_OUT" \
    && { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block rename proposes a destroy or a create - not zero churn"; }
  grep -qE '^  # module\.hm_production_bucket_renamed\.aws_s3_bucket\.main will be updated in-place' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block plan does not propose an in-place update to the renamed bucket"; }
  grep -qF 'Plan: 0 to add, 1 to change, 0 to destroy.' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -10; fail "the moved-block rename plan is not exactly one in-place change"; }
  grep -qE '~ +"tofu-address" += +"module\.hm_production_bucket\.aws_s3_bucket\.main" +-> +"module\.hm_production_bucket_renamed\.aws_s3_bucket\.main"' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT"; fail "the moved-block plan does not show the bucket's tofu-address marker being rewritten from the old address to the new one"; }
  log "  choudoufu: zero churn, one in-place tags update - the marker rewrite the moved block completes"

  MOVED_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; MOVED_APPLY_RC=$?
  [ "$MOVED_APPLY_RC" -eq 0 ] || { printf '%s\n' "$MOVED_APPLY_OUT" | tail -40; fail "the moved-block rename apply exited $MOVED_APPLY_RC"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$MOVED_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$MOVED_APPLY_OUT"; fail "the moved-block rename apply was not exactly one in-place change"; }

  PROD_ADDR_D_AFTER="$(awsl s3api get-bucket-tagging --bucket "$PROD_BUCKET_NAME" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$PROD_ADDR_D_AFTER" = "module.hm_production_bucket_renamed.aws_s3_bucket.main" ] \
    || fail "the bucket carries tofu-address=$PROD_ADDR_D_AFTER after the rename, not module.hm_production_bucket_renamed.aws_s3_bucket.main"
  log "  $PROD_BUCKET_NAME unchanged, tofu-address now module.hm_production_bucket_renamed.aws_s3_bucket.main - read via the AWS CLI"

  log "=== D2. choudoufu, live-mv: module kafka_kms_key -> kafka_kms_key_renamed, no moved block at all ==="
  sed -i.bak 's/module "kafka_kms_key" {/module "kafka_kms_key_renamed" {/' "$ESTATE/main.tofu"
  rm -f "$ESTATE/main.tofu.bak"
  ( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the live-mv rename's reinit failed"; }
  MV_OUT="$(cd "$ESTATE" && "$TOFU" live-mv -estate="$ESTATE_NAME" module.kafka_kms_key.aws_kms_key.main module.kafka_kms_key_renamed.aws_kms_key.main 2>&1)"; MV_RC=$?
  [ "$MV_RC" -eq 0 ] || { printf '%s\n' "$MV_OUT" | tail -30; fail "choudoufu live-mv exited $MV_RC"; }
  grep -qF 'Rewrote the ownership marker on one live resource. This was a cloud write.' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report a real write"; }
  grep -qF '"module.kafka_kms_key.aws_kms_key.main" -> "module.kafka_kms_key_renamed.aws_kms_key.main"' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report rewriting the tofu-address marker from the old address to the new one"; }
  log "  live-mv: $(grep -F 'live ID' <<< "$MV_OUT")"

  KMS_ADDR_D_AFTER="$(awsl kms list-resource-tags --key-id "$KMS_KEY_ID" --query "Tags[?TagKey=='tofu-address'].TagValue | [0]" --output text)"
  [ "$KMS_ADDR_D_AFTER" = "module.kafka_kms_key_renamed.aws_kms_key.main" ] \
    || fail "the key carries tofu-address=$KMS_ADDR_D_AFTER after live-mv, not module.kafka_kms_key_renamed.aws_kms_key.main"
  log "  $KMS_KEY_ID unchanged, tofu-address now module.kafka_kms_key_renamed.aws_kms_key.main - read via the AWS CLI"

  log "=== D3. one more plan: config and markers agree on both renames, nothing proposed ==="
  FINAL_PLAN_D_OUT="$(plan_into 2>&1)"; FINAL_PLAN_D_RC=$?
  [ "$FINAL_PLAN_D_RC" -eq 0 ] || { printf '%s\n' "$FINAL_PLAN_D_OUT" | tail -40; fail "the post-rename plan exited $FINAL_PLAN_D_RC"; }
  grep -qF "No changes. Your infrastructure matches the configuration." <<< "$FINAL_PLAN_D_OUT" \
    || { grep -E '^  #' <<< "$FINAL_PLAN_D_OUT"; fail "the post-rename plan is not empty"; }
  log "  No changes. Both renames are complete and invisible to the next plan."

  gauntlet_stage day2_rename pass "moved block: module.hm_production_bucket renamed with zero churn (0 add, 1 change, 0 destroy), marker rewritten in place; live-mv: module.kafka_kms_key renamed with zero churn, marker rewritten in place; stock oracle over the same two-object rename on cold_deploy's own state also shows zero churn (0 add, 0 change, 0 destroy); both live ids unchanged, read via the AWS CLI"
  log ""

  # ══════════════════════════════════════════════════════════════════════
  # PART E: REMOVE A BLOCK (day2_remove, live/GAUNTLET.md #7)
  # ══════════════════════════════════════════════════════════════════════
  #
  # Starts from Part D's real, completed state: module.kafka_kms_key_renamed
  # (originally module.kafka_kms_key) is bound and converged. It is the one
  # removed here, not either bucket module - both buckets carry
  # `lifecycle { prevent_destroy = true }` in the real amazon_s3_bucket
  # module (see header). kafka_kms_key's block holds TWO resources - the
  # taggable aws_kms_key and the untaggable, client-named aws_kms_alias -
  # so removing it also has to destroy the alias, in an order the cloud
  # accepts (the alias references the key by id, so it has to go first).
  # This estate is the same multi-provider shape #403 fixed for day2_rename
  # (the key module under the default aws provider, both bucket modules
  # under aws.production) - the removal below runs entirely on the
  # DEFAULT-provider module and touches neither bucket module nor its
  # provider alias, so it does not exercise #403's own cross-provider
  # dedup path at all; left as-is rather than disturbed.
  CURRENT_STAGE=day2_remove
  log "=== E0. capture the live ids one more time ==="
  E_KMS_ADDR_BEFORE="$(awsl kms list-resource-tags --key-id "$KMS_KEY_ID" --query "Tags[?TagKey=='tofu-address'].TagValue | [0]" --output text 2>/dev/null || true)"
  [ "$E_KMS_ADDR_BEFORE" = "module.kafka_kms_key_renamed.aws_kms_key.main" ] \
    || fail "$KMS_KEY_ID does not carry tofu-address=module.kafka_kms_key_renamed.aws_kms_key.main before day2_remove even starts (got $E_KMS_ADDR_BEFORE)"

  if [ "${BREAK_REMOVE:-}" = "1" ]; then
    log "=== E1 (BREAK_REMOVE=1). keep module.kafka_kms_key_renamed's block; no destroy may be proposed ==="
    BREAK_REMOVE_PLAN_OUT="$(plan_into 2>&1)"; BREAK_REMOVE_PLAN_RC=$?
    [ "$BREAK_REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_REMOVE_PLAN_OUT" | tail -40; fail "the BREAK_REMOVE=1 kept-block plan exited $BREAK_REMOVE_PLAN_RC"; }
    grep -qE '^  # module\.kafka_kms_key_renamed\..+ will be destroyed' <<< "$BREAK_REMOVE_PLAN_OUT" \
      && { grep -E '^  # .+ will be' <<< "$BREAK_REMOVE_PLAN_OUT"; fail "BREAK_REMOVE=1: a destroy was proposed for module.kafka_kms_key_renamed even though its block is still in the config - this stage's check is not load-bearing"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$BREAK_REMOVE_PLAN_OUT" \
      || { grep -E '^  #' <<< "$BREAK_REMOVE_PLAN_OUT"; fail "BREAK_REMOVE=1: some resource action was proposed with the block still in the config"; }
    log "  BREAK_REMOVE=1: correctly proposes no resource action - the block is still declared"
  else
    log "=== E1. choudoufu: delete module.kafka_kms_key_renamed's block ==="
    perl -0pi -e 's/\n# Kafka KMS key\nmodule "kafka_kms_key_renamed" \{.*?\n\}\n//s' "$ESTATE/main.tofu"
    grep -q 'module "kafka_kms_key_renamed"' "$ESTATE/main.tofu" \
      && fail "removing module.kafka_kms_key_renamed's block did not match - the config has moved"
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the day2_remove reinit failed"; }
    REMOVE_PLAN_OUT="$(plan_into 2>&1)"; REMOVE_PLAN_RC=$?
    [ "$REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -40; fail "the day2_remove plan exited $REMOVE_PLAN_RC"; }
    if grep -q 'is unclaimed, so this may be the same resource under a new instance key' <<< "$REMOVE_PLAN_OUT"; then
      printf '%s\n' "$REMOVE_PLAN_OUT" | tail -40
      fail "choudoufu withheld the destroy of module.kafka_kms_key_renamed as a possible rename - this is the honest wall, not a pass"
    fi
    grep -qE '^  # module\.kafka_kms_key_renamed\.aws_kms_key\.main will be destroyed' <<< "$REMOVE_PLAN_OUT" \
      || { printf '%s\n' "$REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu does not propose destroying the KMS key when module.kafka_kms_key_renamed's block is deleted"; }
    grep -qE '^  # module\.kafka_kms_key_renamed\.aws_kms_alias\.main will be destroyed' <<< "$REMOVE_PLAN_OUT" \
      || { printf '%s\n' "$REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu does not propose destroying the KMS alias when module.kafka_kms_key_renamed's block is deleted"; }
    grep -qF 'Plan: 0 to add, 0 to change, 2 to destroy.' <<< "$REMOVE_PLAN_OUT" \
      || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -10; fail "choudoufu's remove plan proposes something other than exactly two destroys"; }
    log "  choudoufu: exactly two destroys (the KMS key and its alias), nothing else"

    REMOVE_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; REMOVE_APPLY_RC=$?
    [ "$REMOVE_APPLY_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_APPLY_OUT" | tail -40; fail "the day2_remove apply exited $REMOVE_APPLY_RC"; }
    grep -qE 'Resources: 0 added, 0 changed, 2 destroyed' <<< "$REMOVE_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$REMOVE_APPLY_OUT"; fail "the day2_remove apply was not exactly two destroys"; }

    # A KMS key is never truly gone the instant it is destroyed - AWS
    # schedules deletion (a pending-deletion window) rather than removing it
    # immediately, confirmed directly against floci with no tofu in the loop
    # while building this check: describe-key on a just-destroyed key
    # returns 200 with KeyState=PendingDeletion, not an error. The
    # equivalent of EC2's or IAM's "no longer exists" check here is that
    # state, not a failed call.
    E_KEY_STATE="$(awsl kms describe-key --key-id "$KMS_KEY_ID" --query 'KeyMetadata.KeyState' --output text 2>&1)"
    [ "$E_KEY_STATE" = "PendingDeletion" ] \
      || fail "$KMS_KEY_ID has KeyState=$E_KEY_STATE after the destroy, not PendingDeletion - it was orphaned, not destroyed"
    log "  $KMS_KEY_ID is PendingDeletion - confirmed via the AWS CLI, not through choudoufu's own report"
    if E_ALIAS_STILL="$(awsl kms list-aliases --query "Aliases[?AliasName=='alias/$KMS_KEY_NAME']" --output text)" && [ -n "$E_ALIAS_STILL" ]; then
      echo "$E_ALIAS_STILL"; fail "alias/$KMS_KEY_NAME still exists in the live account after the destroy - it was orphaned, not destroyed"
    fi
    log "  alias/$KMS_KEY_NAME no longer exists - confirmed via the AWS CLI, not through choudoufu's own report"

    log "=== E2. one more plan: config and reality agree, nothing left to propose ==="
    E_FINAL_PLAN_OUT="$(plan_into 2>&1)"; E_FINAL_PLAN_RC=$?
    [ "$E_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$E_FINAL_PLAN_OUT" | tail -40; fail "the post-remove plan exited $E_FINAL_PLAN_RC"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$E_FINAL_PLAN_OUT" \
      || { grep -E '^  #' <<< "$E_FINAL_PLAN_OUT"; fail "the post-remove plan is not empty"; }
    log "  No changes. The removal is complete and invisible to the next plan."

    log ""
    log "STAGE E (day2_remove): PASS"
    gauntlet_stage day2_remove pass "choudoufu: deleting module.kafka_kms_key_renamed's block proposed exactly two destroys (0 add, 0 change, 2 destroy - the untaggable alias and its taggable parent key), applied cleanly (0 added, 0 changed, 2 destroyed) in an order the cloud accepted, the key is genuinely PendingDeletion and the alias is gone (read via the AWS CLI, not choudoufu's own report), and the next plan proposes no resource action; stock oracle on cold_deploy's own state (E-ORACLE) also proposes exactly two destroys for the same objects"
    log ""
  fi
  CURRENT_STAGE=""
fi
CURRENT_STAGE=""
gauntlet_end

log "=== PASS: all five stages, real, against hongbo-miao/hongbomiao.com's own ==="
log "=== unmodified storage-environment leaf modules, .tofu extension throughout ==="
