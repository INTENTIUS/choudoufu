#!/usr/bin/env bash
# (moved from the justfile's retired demo-corpus-hongbomiao-storage recipe; run with: just demo-run corpus-hongbomiao-storage)
# hongbo-miao/hongbomiao.com's own "storage" environment bootstrap section
# (live/corpus-manifest.json, pinned by commit - same repo and pin as
# corpus-hongbomiao-labelbox, a SECOND disjoint self-contained slice of it):
# the three module calls at the top of environments/production/aws/storage/
# main.tofu that read no terraform_remote_state at all, before that file's
# next section starts reading network's - the shared production S3 bucket,
# a second independent IoT-data S3 bucket, and the Kafka KMS key (an
# aws_kms_key/aws_kms_alias pair). Unlike Labelbox, this exercises the same
# leaf module (amazon_s3_bucket) called twice under different module names,
# plus a server-assigned-ID taggable type paired with a client-named
# untaggable one that is already a ratified DefaultTable row rather than a
# schema fallback. All five stages pass for real: 4 resources cold-deployed,
# 3 stamped (the KMS alias is correctly UNTAGGABLE), an empty replan with
# the state file deleted and identities re-asserted against the AWS CLI's
# own answer, a genuine no-op apply, and drift on the IoT-data bucket's tags
# reconverging without touching the production bucket or the KMS key. See
# the script's own header for why Amazon SageMaker (the other candidate
# section) was ruled out: floci itself returns UnknownOperationException for
# CreateNotebookInstance, confirmed directly before writing this script.
# Needs Docker, the AWS CLI, and the real `tofu` binary; runs on its own
# port (4725).
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
#   6-9. DAY-2 OPS    rename (moved block and live-mv), replace under the
#                     default destroy-then-create ordering, remove a block,
#                     and change count (a synthetic aws_s3_bucket.count_test
#                     block - see PART G's own header for why none of this
#                     estate's own module calls has a real count/for_each
#                     knob), each checked against a real stock oracle.
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
#                 zero-churn result. day2_replace (PART F) has no BREAK
#                 control of its own in this script - it targets the
#                 untaggable, client-named KMS alias (see PART F's own
#                 header for why neither taggable object here can be
#                 force-replaced), which has no marker to manufacture a
#                 collision on; that control's load-bearing-ness is proven
#                 by corpus-evoteum-modules and corpus-giantswarm-
#                 crossplane's own BREAK=replace instead.
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
#   BREAK_COUNT   set to 1 to run day2_count's own break control instead of
#                 the real scale-down/scale-up checks: after the real
#                 scale-down plan, assert the WRONG instance (count_test[0]
#                 rather than count_test[1]) was destroyed. Independent of
#                 the other BREAK flags and only reachable when BREAK is not
#                 "rename" and BREAK_REMOVE is not 1, because PART G starts
#                 from PART E's real, completed removal.
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
gauntlet_begin_stage cold_deploy
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
gauntlet_begin_stage day2_rename
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
gauntlet_begin_stage day2_remove
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
# PART F-ORACLE: REPLACE, stock oracle (day2_replace, live/GAUNTLET.md #9):
# "Stock's replace of the same resource leaves the same single object." A
# THIRD separate copy of cold_deploy's own state ($PLAIN), unrenamed and
# unremoved, so this oracle has nothing to do with the rename/remove
# oracles above. Same wall as corpus-hongbomiao-harbor's own F-ORACLE, one
# level over: both bucket modules carry `lifecycle { prevent_destroy =
# true }` (E-ORACLE's own header above), and aws_kms_key has no client-set
# identity argument at all - its `id`/`key_id` is server-assigned, only
# tags and description are settable, so there is nothing to change that
# would force it to replace. What DOES force-replace: aws_kms_alias, the
# untaggable, client-named sibling - its `name` argument
# ("alias/${var.aws_kms_key_name}") is its whole identity (ImportSyntax
# "alias/ALIASNAME"), and AWS's KMS API has no rename-alias operation
# (UpdateAlias only repoints an existing alias at a different key, it does
# not rename one), so changing aws_kms_key_name forces the alias to
# replace at the SAME declared address while the key itself - same
# server-assigned id, same tofu-address - only sees its own non-identity
# `hm_resource_name` tag value update in place. PLAN ONLY, never applied -
# same convention as the rename/remove oracles above.
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage day2_replace
log "=== F-ORACLE: stock tofu, force-replace module.kafka_kms_key's alias via its ForceNew name (driven by aws_kms_key_name), on cold_deploy's own state ==="
PLAIN_ORACLE_REPLACE="$WORK/plain-oracle-replace"
cp -r "$PLAIN" "$PLAIN_ORACLE_REPLACE"
rm -rf "$PLAIN_ORACLE_REPLACE/.terraform"
sed -i.bak "s/aws_kms_key_name = \"$KMS_KEY_NAME\"/aws_kms_key_name = \"${KMS_KEY_NAME}-v2\"/" "$PLAIN_ORACLE_REPLACE/main.tofu"
rm -f "$PLAIN_ORACLE_REPLACE/main.tofu.bak"
grep -q "${KMS_KEY_NAME}-v2" "$PLAIN_ORACLE_REPLACE/main.tofu" \
  || fail "changing module.kafka_kms_key's aws_kms_key_name argument in the replace-oracle copy did not match - the corpus pin has moved"
( cd "$PLAIN_ORACLE_REPLACE" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_ORACLE_REPLACE" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_replace stock oracle's reinit failed"; }
REPLACE_ORACLE_PLAN_OUT="$(cd "$PLAIN_ORACLE_REPLACE" && tofu plan -input=false -no-color 2>&1)"; REPLACE_ORACLE_PLAN_RC=$?
[ "$REPLACE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_replace stock oracle plan exited $REPLACE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.kafka_kms_key\.aws_kms_alias\.main must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "stock does not propose replacing module.kafka_kms_key's alias when aws_kms_key_name changes"; }
grep -qE '^  # module\.kafka_kms_key\.aws_kms_key\.main will be updated in-place' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "stock does not propose an in-place tag update for the key itself alongside the alias replace"; }
grep -qF 'Plan: 1 to add, 1 to change, 1 to destroy.' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -10; fail "stock's replace plan proposes something other than exactly one add, one in-place change and one destroy"; }
log "  stock: exactly one replace proposed (the alias only) at the same declared address, plus one in-place tag update on the key itself (same server-assigned id, untouched identity), on the state cold_deploy produced - plan only, not applied"
gauntlet_end_stage

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

gauntlet_begin_stage greenfield
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
GREEN_RECORD_FILES="$(gauntlet_record_count "$GREEN/.tofu-records/tofu-records")"
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
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# PART G-ORACLE: CHANGE COUNT, stock oracle (day2_count, live/GAUNTLET.md #8,
# issue #359/#488)
# ══════════════════════════════════════════════════════════════════════════
#
# None of this estate's own three module calls exposes a numeric count or
# for_each knob of its own - hongbomiao.com's amazon_s3_bucket and
# aws_kms_key leaf modules (see header, and copy_leaf_modules above) each
# declare exactly one resource with no count/for_each argument anywhere, and
# neither module's own variables.tofu offers one either; this is issue
# #488's synthetic-count fallback clause, following
# live/e2e/corpus-iam-read-only-policy/run.sh's own PART G rather than
# corpus-xancloud-iac's real for_each shape. A NEW, entirely synthetic
# resource - aws_s3_bucket.count_test, count_test_block() below - reuses one
# of this estate's own three exercised types rather than introducing a
# foreign one.
#
# Established directly against floci first, no tofu in the loop, before
# writing any assertion (HANDOFF's identity-semantics rule): an S3 bucket's
# id (its `bucket` name) is deterministic from its own configuration, like
# an IAM policy's ARN, so a destroy+recreate under the same name returns the
# SAME id - confirmed with s3api create-bucket -> delete-bucket ->
# create-bucket under one identical name, same bucket name both times, no
# tags carried over. Unlike aws_kms_key (whose schedule-key-deletion leaves
# the object present as KeyState=PendingDeletion - the discriminator
# day2_remove above already uses, since a KMS key is never truly gone the
# instant it is destroyed), a deleted S3 bucket is genuinely gone from floci
# - head-bucket on it returns a 404 - so absence-then-recreate is asserted
# directly below, and AWS mints no other server-side identifier for a
# bucket the way it does a security-group id or an IAM PolicyId: the
# "genuinely a new object" discriminator used below is list-buckets' own
# CreationDate, confirmed to change across a real delete+recreate under the
# same name (two probe buckets created three seconds apart showed
# CreationDate 17:15:04Z and 17:15:07Z respectively).
#
# Applied for real, twice (2 -> 1 -> 2), in the SAME otherwise-idle account
# PART GREENFIELD's own real leg ($GREEN_ENDPOINT) just finished with above
# and never touches again before this script tears it down a few lines
# below - "count-test-0"/"count-test-1" collides with nothing that account
# already holds (its own four objects are named from
# $PROD_BUCKET_NAME/$IOT_BUCKET_NAME/$KMS_KEY_NAME, all disjoint prefixes) -
# the same reasoning corpus-iam-read-only-policy's own G-ORACLE gives for
# reusing its own idle greenfield-oracle account rather than spinning up a
# third container. This oracle section MUST run before the
# `docker rm -f "$FLOCI_GREEN_NAME"` line a few lines below, or the account
# it needs is already gone.
gauntlet_begin_stage day2_count
count_test_block() { # $1 = count
  local n="$1"
  cat <<COUNTEOF
resource "aws_s3_bucket" "count_test" {
  count  = $n
  bucket = "${ESTATE_NAME}-count-test-\${count.index}"
  tags = {
    "hm_environment" = "production"
  }
}
COUNTEOF
}
oracle_count_provider() {
  cat <<EOF
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.59.0"
    }
  }
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

EOF
}

log "=== G-ORACLE: stock, create a 2-instance count block, scale it to 1 and back, in the (idle) greenfield real-leg account ==="
PLAIN_ORACLE_COUNT="$WORK/plain-oracle-count"
mkdir -p "$PLAIN_ORACLE_COUNT"
{ oracle_count_provider; count_test_block 2; } > "$PLAIN_ORACLE_COUNT/main.tf"
( cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_count stock oracle's init failed"; }
ORACLE_COUNT_APPLY_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" tofu apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$ORACLE_COUNT_APPLY_OUT" | tail -30; fail "the day2_count stock oracle's baseline apply failed"; }
grep -qE 'Apply complete! Resources: 2 added' <<< "$ORACLE_COUNT_APPLY_OUT" \
  || { printf '%s\n' "$ORACLE_COUNT_APPLY_OUT" | tail -30; fail "stock did not create exactly 2 count-test buckets for the day2_count oracle"; }
ORACLE_CT0_NAME="${ESTATE_NAME}-count-test-0"
ORACLE_CT1_NAME="${ESTATE_NAME}-count-test-1"
ORACLE_CT0_CREATED="$(awslg s3api list-buckets --query "Buckets[?Name=='$ORACLE_CT0_NAME'].CreationDate | [0]" --output text)"
ORACLE_CT1_CREATED="$(awslg s3api list-buckets --query "Buckets[?Name=='$ORACLE_CT1_NAME'].CreationDate | [0]" --output text)"
[ -n "$ORACLE_CT0_CREATED" ] && [ "$ORACLE_CT0_CREATED" != "None" ] || fail "no oracle count_test[0] bucket found by name"
[ -n "$ORACLE_CT1_CREATED" ] && [ "$ORACLE_CT1_CREATED" != "None" ] || fail "no oracle count_test[1] bucket found by name"
log "  stock: 2 instances created, count_test[0]=$ORACLE_CT0_NAME (created=$ORACLE_CT0_CREATED) count_test[1]=$ORACLE_CT1_NAME (created=$ORACLE_CT1_CREATED)"

{ oracle_count_provider; count_test_block 1; } > "$PLAIN_ORACLE_COUNT/main.tf"
ORACLE_DOWN_PLAN_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" tofu plan -input=false -no-color 2>&1)"; ORACLE_DOWN_PLAN_RC=$?
[ "$ORACLE_DOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | tail -40; fail "the day2_count stock oracle's scale-down plan exited $ORACLE_DOWN_PLAN_RC"; }
grep -qE '^  # aws_s3_bucket\.count_test\[1\] will be destroyed' <<< "$ORACLE_DOWN_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-down plan does not destroy count_test[1]"; }
grep -qE '^  # aws_s3_bucket\.count_test\[0\] will be' <<< "$ORACLE_DOWN_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-down plan touches count_test[0], which should be untouched"; }
grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$ORACLE_DOWN_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | tail -10; fail "stock's scale-down plan proposes something other than exactly one destroy"; }
ORACLE_DOWN_APPLY_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" tofu apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$ORACLE_DOWN_APPLY_OUT" | tail -30; fail "the day2_count stock oracle's scale-down apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$ORACLE_DOWN_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$ORACLE_DOWN_APPLY_OUT"; fail "the day2_count stock oracle's scale-down apply was not exactly one destroy"; }
if ORACLE_CT1_STILL="$(awslg s3api head-bucket --bucket "$ORACLE_CT1_NAME" 2>&1)"; then
  echo "$ORACLE_CT1_STILL"; fail "stock's count_test[1] bucket ($ORACLE_CT1_NAME) still exists after the scale-down destroy"
fi
ORACLE_CT0_CREATED_AFTER_DOWN="$(awslg s3api list-buckets --query "Buckets[?Name=='$ORACLE_CT0_NAME'].CreationDate | [0]" --output text)"
[ "$ORACLE_CT0_CREATED_AFTER_DOWN" = "$ORACLE_CT0_CREATED" ] || fail "stock's surviving count_test[0] bucket changed CreationDate across the scale-down ($ORACLE_CT0_CREATED -> $ORACLE_CT0_CREATED_AFTER_DOWN)"
log "  stock: exactly one destroy (count_test[1]=$ORACLE_CT1_NAME, now gone), count_test[0]=$ORACLE_CT0_NAME (created=$ORACLE_CT0_CREATED) unchanged"

sleep 1
{ oracle_count_provider; count_test_block 2; } > "$PLAIN_ORACLE_COUNT/main.tf"
ORACLE_UP_PLAN_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" tofu plan -input=false -no-color 2>&1)"; ORACLE_UP_PLAN_RC=$?
[ "$ORACLE_UP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | tail -40; fail "the day2_count stock oracle's scale-up plan exited $ORACLE_UP_PLAN_RC"; }
grep -qE '^  # aws_s3_bucket\.count_test\[1\] will be created' <<< "$ORACLE_UP_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-up plan does not create count_test[1]"; }
grep -qE '^  # aws_s3_bucket\.count_test\[0\] will be' <<< "$ORACLE_UP_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-up plan touches count_test[0], which should be untouched"; }
grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$ORACLE_UP_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | tail -10; fail "stock's scale-up plan proposes something other than exactly one create"; }
ORACLE_UP_APPLY_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" tofu apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$ORACLE_UP_APPLY_OUT" | tail -30; fail "the day2_count stock oracle's scale-up apply failed"; }
grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$ORACLE_UP_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$ORACLE_UP_APPLY_OUT"; fail "the day2_count stock oracle's scale-up apply was not exactly one create"; }
ORACLE_CT1_NEW_CREATED="$(awslg s3api list-buckets --query "Buckets[?Name=='$ORACLE_CT1_NAME'].CreationDate | [0]" --output text)"
[ -n "$ORACLE_CT1_NEW_CREATED" ] && [ "$ORACLE_CT1_NEW_CREATED" != "None" ] || fail "no oracle count_test[1] bucket found after the scale-up"
[ "$ORACLE_CT1_NEW_CREATED" != "$ORACLE_CT1_CREATED" ] || fail "stock's recreated count_test[1] came back with the SAME CreationDate it had before being destroyed - the destroy was not real"
ORACLE_CT0_CREATED_AFTER_UP="$(awslg s3api list-buckets --query "Buckets[?Name=='$ORACLE_CT0_NAME'].CreationDate | [0]" --output text)"
[ "$ORACLE_CT0_CREATED_AFTER_UP" = "$ORACLE_CT0_CREATED" ] || fail "stock's count_test[0] bucket changed CreationDate across the scale-up"
log "  stock: exactly one create (count_test[1], same bucket name - deterministic - but a NEW CreationDate $ORACLE_CT1_NEW_CREATED, was $ORACLE_CT1_CREATED), count_test[0]=$ORACLE_CT0_NAME unchanged throughout"
gauntlet_end_stage

docker rm -f "$FLOCI_GREEN_NAME" >/dev/null 2>&1 || true

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage migrate
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
gauntlet_begin_stage test_plan
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
gauntlet_begin_stage test_apply
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
gauntlet_begin_stage drift_reconverge
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
gauntlet_begin_stage day2_rename
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
  # PART F: REPLACE (day2_replace, active stage - live/GAUNTLET.md #9)
  # ══════════════════════════════════════════════════════════════════════
  #
  # Starts from Part D's real, completed state: module.kafka_kms_key_
  # renamed (originally module.kafka_kms_key) is bound and converged, and
  # is otherwise untouched by anything else in this script until PART E
  # removes it below - the two day-2 stages compose on the SAME addresses
  # rather than needing a second standalone object. Neither of this
  # estate's other two module calls (both S3 buckets) can be
  # force-replaced at all - both carry `lifecycle { prevent_destroy = true
  # }` in the real amazon_s3_bucket module (see header) - and within
  # kafka_kms_key itself, aws_kms_key has no client-set identity argument
  # to change (server-assigned id; see F-ORACLE's own header for the full
  # reasoning, discovered there first). What DOES force-replace is the
  # untaggable, client-named aws_kms_alias sibling: its `name` argument is
  # its whole identity, and AWS's KMS API has no rename-alias operation
  # (UpdateAlias only repoints an alias at a different key, never renames
  # it), so changing aws_kms_key_name forces the alias to replace at the
  # SAME declared address while the key itself only sees its own
  # non-identity `hm_resource_name` tag value update in place - same
  # server-assigned id, same tofu-address, never replaced.
  #
  # THE MARKER-COLLISION SCOPE NOTE (full reasoning in corpus-hongbomiao-
  # harbor's own PART F). aws_kms_alias has no `tags` argument at all - it
  # is untaggable, resolved by its own name every run - so there is no
  # marker to plant a manufactured collision on; corpus-evoteum-modules and
  # corpus-giantswarm-crossplane's own BREAK=replace runs already prove
  # that control load-bearing for the taggable, marker-based shape.
  #
  # THE create_before_destroy SCOPE NOTE (full reasoning in corpus-sqs-
  # basic's own PART F). OpenTofu core rejects a `lifecycle` block on a
  # `module` call, and patching the vendored aws_kms_key module's own
  # resources to add create_before_destroy would cross this corpus's own
  # DELTA discipline (see header), so this evidence pass exercises the
  # default destroy-then-create ordering instead.
  gauntlet_begin_stage day2_replace
  record_key() { printf '%s' "$1" | base64 | tr '+/' '-_' | tr -d '=\n'; }
  record_import_id() { jq -r '.identity.import_id' "$1"; }
  F_ADDR="module.kafka_kms_key_renamed.aws_kms_alias.main"
  F_RECORD="$ESTATE/.tofu-records/tofu-records/$ESTATE_NAME/aws_kms_alias/$(record_key "$F_ADDR")"
  F_KEY_ADDR="module.kafka_kms_key_renamed.aws_kms_key.main"

  log "=== F0. capture the live alias and its record ahead of the forced replace ==="
  [ -f "$F_RECORD" ] || fail "no local record file found for $F_ADDR ahead of day2_replace"
  F_OLD_IMPORT_ID="$(record_import_id "$F_RECORD")"
  [ "$F_OLD_IMPORT_ID" = "$KMS_ALIAS_NAME" ] || fail "the record for $F_ADDR names $F_OLD_IMPORT_ID ahead of day2_replace, not $KMS_ALIAS_NAME"
  awsl kms list-aliases --query "Aliases[?AliasName=='$KMS_ALIAS_NAME'].TargetKeyId | [0]" --output text | grep -qF "$KMS_KEY_ID" \
    || fail "$KMS_ALIAS_NAME does not point at $KMS_KEY_ID ahead of day2_replace"
  log "  $KMS_ALIAS_NAME -> $KMS_KEY_ID, record import_id=$F_OLD_IMPORT_ID"

  log "=== F1. choudoufu: change the aws_kms_key_name argument, forcing the alias to replace at the same declared address while the key stays put ==="
  sed -i.bak "s/aws_kms_key_name = \"$KMS_KEY_NAME\"/aws_kms_key_name = \"${KMS_KEY_NAME}-v2\"/" "$ESTATE/main.tofu"
  rm -f "$ESTATE/main.tofu.bak"
  grep -q "${KMS_KEY_NAME}-v2" "$ESTATE/main.tofu" || fail "changing module.kafka_kms_key_renamed's aws_kms_key_name argument did not match - the corpus pin has moved"
  F_NEW_ALIAS_NAME="alias/${KMS_KEY_NAME}-v2"

  F_PLAN_OUT="$(plan_into 2>&1)"; F_PLAN_RC=$?
  [ "$F_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_PLAN_OUT" | tail -40; fail "the day2_replace plan exited $F_PLAN_RC"; }
  grep -qE '^  # module\.kafka_kms_key_renamed\.aws_kms_alias\.main must be replaced' <<< "$F_PLAN_OUT" \
    || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not propose replacing module.kafka_kms_key_renamed's alias when its ForceNew name argument changes"; }
  grep -qE '^  # module\.kafka_kms_key_renamed\.aws_kms_key\.main will be updated in-place' <<< "$F_PLAN_OUT" \
    || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not propose an in-place tag update for the key itself alongside the alias replace"; }
  grep -qF 'Plan: 1 to add, 1 to change, 1 to destroy.' <<< "$F_PLAN_OUT" \
    || { printf '%s\n' "$F_PLAN_OUT" | tail -10; fail "the day2_replace plan is not exactly one add, one in-place change and one destroy"; }
  log "  choudoufu: exactly one forced replace at the same declared address (the alias), plus one in-place tag update on the key (same id, untouched identity)"

  F_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; F_APPLY_RC=$?
  [ "$F_APPLY_RC" -eq 0 ] || { printf '%s\n' "$F_APPLY_OUT" | tail -40; fail "the day2_replace apply exited $F_APPLY_RC"; }
  grep -qE 'Resources: 1 added, 1 changed, 1 destroyed' <<< "$F_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$F_APPLY_OUT"; fail "the day2_replace apply was not exactly one add, one change and one destroy"; }

  if F_OLD_ALIAS_STILL="$(awsl kms list-aliases --query "Aliases[?AliasName=='$KMS_ALIAS_NAME']" --output text)" && [ -n "$F_OLD_ALIAS_STILL" ]; then
    echo "$F_OLD_ALIAS_STILL"; fail "$KMS_ALIAS_NAME still exists after the replace - the old object was orphaned, not destroyed"
  fi
  log "  $KMS_ALIAS_NAME no longer exists - confirmed via the AWS CLI, not through choudoufu's own report"

  F_NEW_ALIAS_TARGET="$(awsl kms list-aliases --query "Aliases[?AliasName=='$F_NEW_ALIAS_NAME'].TargetKeyId | [0]" --output text)"
  [ "$F_NEW_ALIAS_TARGET" = "$KMS_KEY_ID" ] \
    || fail "$F_NEW_ALIAS_NAME points at $F_NEW_ALIAS_TARGET after the replace, not the SAME key $KMS_KEY_ID - the key itself should never have moved"
  log "  $F_NEW_ALIAS_NAME (the new alias) points at the SAME key $KMS_KEY_ID, read via the AWS CLI - the key was never replaced, only the alias was"

  # THE RECORD STORE, asserted by value (HANDOFF's safety rule; the
  # #398-guard shape: a stale record still naming the destroyed object
  # would be exactly the wrong-marker failure that outranks a missing
  # one). The local record file at the SAME address must now hold the
  # NEW alias's import_id, not the one captured in F0.
  F_NEW_IMPORT_ID="$(record_import_id "$F_RECORD")"
  [ "$F_NEW_IMPORT_ID" = "$F_NEW_ALIAS_NAME" ] \
    || fail "the record for $F_ADDR names $F_NEW_IMPORT_ID after the replace, not the new alias $F_NEW_ALIAS_NAME - a stale record still claiming the destroyed object, the #398-guard shape"
  [ "$F_NEW_IMPORT_ID" != "$F_OLD_IMPORT_ID" ] \
    || fail "sanity: the record's import_id at $F_ADDR did not change at all across the replace"
  log "  record store: import_id $F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID at the same key ($F_ADDR) - read directly off the local record store file, not through choudoufu's own report"

  # Sanity: the KEY's own record, at its OWN unrelated address, must still
  # name the exact same key id - the tag-value update above must never
  # have been misread as an identity change on the key.
  F_KEY_RECORD="$ESTATE/.tofu-records/tofu-records/$ESTATE_NAME/aws_kms_key/$(record_key "$F_KEY_ADDR")"
  [ -f "$F_KEY_RECORD" ] || fail "no local record file found for $F_KEY_ADDR after day2_replace"
  F_KEY_IMPORT_ID="$(record_import_id "$F_KEY_RECORD")"
  [ "$F_KEY_IMPORT_ID" = "$KMS_KEY_ID" ] \
    || fail "the record for $F_KEY_ADDR names $F_KEY_IMPORT_ID after the replace, not the unchanged key $KMS_KEY_ID - the key's own identity moved when it should not have"
  log "  the key's own record at $F_KEY_ADDR still names $KMS_KEY_ID, unchanged - the replace stayed scoped to the alias"

  log "=== F2. one more plan: config and reality agree, no marker collision ==="
  F_FINAL_PLAN_OUT="$(plan_into 2>&1)"; F_FINAL_PLAN_RC=$?
  [ "$F_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_FINAL_PLAN_OUT" | tail -40; fail "the post-replace plan exited $F_FINAL_PLAN_RC"; }
  grep -qF "No changes. Your infrastructure matches the configuration." <<< "$F_FINAL_PLAN_OUT" \
    || { grep -E '^  #' <<< "$F_FINAL_PLAN_OUT"; fail "the post-replace plan is not empty"; }
  log "  No changes. The replace is complete and invisible to the next plan."

  # PART E below reads $KMS_KEY_NAME/$KMS_ALIAS_NAME for its own AWS CLI
  # checks; the live alias it must find is now the one this replace just
  # created (the key itself, and $KMS_KEY_ID, are unaffected).
  KMS_KEY_NAME="${KMS_KEY_NAME}-v2"
  KMS_ALIAS_NAME="$F_NEW_ALIAS_NAME"

  gauntlet_stage day2_replace pass "choudoufu: changing module.kafka_kms_key_renamed's aws_kms_key_name argument proposed exactly one forced replace at the same declared address (the untaggable, client-named alias - 1 add, 1 change, 1 destroy overall) plus one in-place tag update on the taggable key itself, applied cleanly; the old alias ($F_OLD_IMPORT_ID) is confirmed gone and the new alias ($F_NEW_ALIAS_NAME) points at the SAME key ($KMS_KEY_ID, read via the AWS CLI) - the key was never replaced; the local record store's record at the alias's address now names the new alias, not the destroyed one ($F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID), while the key's own record at its own address is unchanged; the next plan proposes no resource action; stock oracle on cold_deploy's own state (F-ORACLE) also proposes exactly one replace (the alias) plus one in-place key update. Scope notes: (1) this exercises OpenTofu's default destroy-then-create ordering, not the create_before_destroy variant the stage's Title names - see corpus-sqs-basic's own PART F; (2) BREAK=replace's marker-collision control is not exercised here - aws_kms_alias is untaggable and resolved by its own name, with no marker to plant a collision on, so that control's load-bearing-ness is proven instead by corpus-evoteum-modules and corpus-giantswarm-crossplane's own PART F sections against the taggable shape."
  gauntlet_end_stage
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
  gauntlet_begin_stage day2_remove
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

    # ══════════════════════════════════════════════════════════════════════
    # PART G: CHANGE COUNT (day2_count, active - live/GAUNTLET.md #8, issue
    # #359/#488)
    # ══════════════════════════════════════════════════════════════════════
    #
    # Starts from Part E's real, completed state: the estate plans empty
    # with module.kafka_kms_key_renamed's key and alias gone - Part E just
    # destroyed the only object in this estate that is not guarded by
    # `lifecycle { prevent_destroy = true }` (see header). A NEW, entirely
    # synthetic resource (aws_s3_bucket.count_test, count_test_block()
    # defined above G-ORACLE) is added here, in its own file, so
    # day2_count's own history is self-contained and never revisits an
    # address any other stage already used - the same discipline
    # live/e2e/reference-ec2-vpc/run.sh's own Part F uses for its
    # aws_security_group.count_test. G-ORACLE above is the stock oracle for
    # the identical shape, applied for real in the otherwise-idle greenfield
    # real-leg account before this script tore that container down.
    #
    # BREAK_COUNT=1 exercises this stage's own Break control instead of the
    # real checks: after the real scale-down plan, assert the WRONG
    # instance (count_test[0] rather than count_test[1]) was the one
    # destroyed - the Break text in tools/gauntlet/stages.go for day2_count,
    # verbatim: "Expect a different instance to be destroyed; the assertion
    # must fail." Only reachable when BREAK is not "rename" and BREAK_REMOVE
    # is not 1, because PART G starts from PART E's real, completed removal.
    gauntlet_begin_stage day2_count
    log "=== G0. choudoufu: add aws_s3_bucket.count_test, count = 2 ==="
    count_test_block 2 > "$ESTATE/day2_count.tf"
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the count-block-add reinit failed"; }
    COUNT_ADD_PLAN_OUT="$(plan_into 2>&1)"; COUNT_ADD_PLAN_RC=$?
    [ "$COUNT_ADD_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_ADD_PLAN_OUT" | tail -30; fail "the count-block-add plan exited $COUNT_ADD_PLAN_RC"; }
    grep -qF 'Plan: 2 to add, 0 to change, 0 to destroy.' <<< "$COUNT_ADD_PLAN_OUT" \
      || { printf '%s\n' "$COUNT_ADD_PLAN_OUT" | tail -10; fail "adding the count block did not plan exactly 2 creates"; }
    COUNT_ADD_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_ADD_APPLY_RC=$?
    [ "$COUNT_ADD_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_ADD_APPLY_OUT" | tail -30; fail "the count-block-add apply exited $COUNT_ADD_APPLY_RC"; }
    grep -qE 'Resources: 2 added, 0 changed, 0 destroyed' <<< "$COUNT_ADD_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$COUNT_ADD_APPLY_OUT"; fail "the count-block-add apply did not create exactly 2 resources"; }

    CT0_NAME="${ESTATE_NAME}-count-test-0"
    CT1_NAME="${ESTATE_NAME}-count-test-1"
    CT0_ADDR_TAG="$(awsl s3api get-bucket-tagging --bucket "$CT0_NAME" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
    CT1_ADDR_TAG="$(awsl s3api get-bucket-tagging --bucket "$CT1_NAME" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
    [ "$CT0_ADDR_TAG" = 'aws_s3_bucket.count_test:0' ] || fail "count_test[0]'s live tofu-address tag is $CT0_ADDR_TAG, not aws_s3_bucket.count_test:0 (live/MARKERS.md: a count instance's tag value is colon-escaped, e.g. aws_eip.this[2] -> aws_eip.this:2)"
    [ "$CT1_ADDR_TAG" = 'aws_s3_bucket.count_test:1' ] || fail "count_test[1]'s live tofu-address tag is $CT1_ADDR_TAG, not aws_s3_bucket.count_test:1"
    # aws_s3_bucket's id (its own name) is deterministic from the `bucket`
    # argument, not server-random (verified directly against floci ahead of
    # writing this stage, no tofu in the loop - see G-ORACLE's own comment
    # above for the same finding), so a destroy+recreate under the same name
    # yields the SAME id. list-buckets' own CreationDate, not the name, is
    # what the "genuinely a new object" checks below compare - AWS mints no
    # other server-side identifier for a bucket.
    CT0_CREATED="$(awsl s3api list-buckets --query "Buckets[?Name=='$CT0_NAME'].CreationDate | [0]" --output text)"
    CT1_CREATED="$(awsl s3api list-buckets --query "Buckets[?Name=='$CT1_NAME'].CreationDate | [0]" --output text)"
    [ -n "$CT0_CREATED" ] && [ "$CT0_CREATED" != "None" ] || fail "live count_test[0] bucket has no CreationDate"
    [ -n "$CT1_CREATED" ] && [ "$CT1_CREATED" != "None" ] || fail "live count_test[1] bucket has no CreationDate"
    log "  2 instances created: index 0 = $CT0_NAME (tofu-address=$CT0_ADDR_TAG, created=$CT0_CREATED), index 1 = $CT1_NAME (tofu-address=$CT1_ADDR_TAG, created=$CT1_CREATED) - read via the AWS CLI"

    COUNT_NOOP_PLAN_OUT="$(plan_into 2>&1)"; COUNT_NOOP_PLAN_RC=$?
    [ "$COUNT_NOOP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_NOOP_PLAN_OUT" | tail -30; fail "the post-add plan exited $COUNT_NOOP_PLAN_RC"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$COUNT_NOOP_PLAN_OUT" \
      || { grep -E '^  #' <<< "$COUNT_NOOP_PLAN_OUT"; fail "the plan right after adding the count block is not empty - the new instances did not bind their own markers cleanly"; }
    log "  No changes - both new instances plan empty immediately after creation"

    log "=== G1. scale count down: 2 -> 1 ==="
    count_test_block 1 > "$ESTATE/day2_count.tf"
    COUNT_DOWN_PLAN_OUT="$(plan_into 2>&1)"; COUNT_DOWN_PLAN_RC=$?
    [ "$COUNT_DOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | tail -30; fail "the scale-down plan exited $COUNT_DOWN_PLAN_RC"; }

    if [ "${BREAK_COUNT:-}" = "1" ]; then
      log "  BREAK_COUNT=1: asserting the WRONG instance (count_test[0]) was destroyed instead of count_test[1]"
      if grep -qE '^  # aws_s3_bucket\.count_test\[0\] will be destroyed' <<< "$COUNT_DOWN_PLAN_OUT"; then
        fail "BREAK_COUNT=1: the plan actually destroys count_test[0] - this assertion is not load-bearing"
      fi
      log "  BREAK_COUNT=1: correctly does NOT destroy count_test[0] - the wrong-instance assertion above fails to hold, as it must"
      # BREAK_COUNT is a control-only invocation that never applies the
      # scale-down: revert the count file to its 2-instance shape so the
      # config this script leaves behind matches what it already applied,
      # the same discipline every other BREAK path in this script follows.
      count_test_block 2 > "$ESTATE/day2_count.tf"
    else
      grep -qE '^  # aws_s3_bucket\.count_test\[1\] will be destroyed' <<< "$COUNT_DOWN_PLAN_OUT" \
        || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-down plan does not destroy count_test[1]"; }
      grep -qE '^  # aws_s3_bucket\.count_test\[0\] will be' <<< "$COUNT_DOWN_PLAN_OUT" \
        && { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-down plan touches count_test[0], which should be untouched"; }
      grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$COUNT_DOWN_PLAN_OUT" \
        || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | tail -10; fail "choudoufu's scale-down plan proposes something other than exactly one destroy"; }
      log "  choudoufu: exactly one destroy (count_test[1]), count_test[0] untouched"

      COUNT_DOWN_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_DOWN_APPLY_RC=$?
      [ "$COUNT_DOWN_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_DOWN_APPLY_OUT" | tail -30; fail "the scale-down apply exited $COUNT_DOWN_APPLY_RC"; }
      grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$COUNT_DOWN_APPLY_OUT" \
        || { grep -E 'Apply complete' <<< "$COUNT_DOWN_APPLY_OUT"; fail "the scale-down apply was not exactly one destroy"; }

      CT0_CREATED_AFTER_DOWN="$(awsl s3api list-buckets --query "Buckets[?Name=='$CT0_NAME'].CreationDate | [0]" --output text)"
      [ "$CT0_CREATED_AFTER_DOWN" = "$CT0_CREATED" ] || fail "count_test[0]'s CreationDate changed across the scale-down ($CT0_CREATED -> $CT0_CREATED_AFTER_DOWN) - it was destroyed and recreated, not left alone"
      if CT1_STILL="$(awsl s3api head-bucket --bucket "$CT1_NAME" 2>&1)"; then
        echo "$CT1_STILL"; fail "count_test[1] bucket ($CT1_NAME) still exists in the live account after the scale-down destroy"
      fi

      # The local record store, asserted by value (HANDOFF's safety rule;
      # the #398-guard shape - the same discipline PART F's own comment
      # above uses). A destroyed count instance's record is TOMBSTONED, not
      # deleted outright ([projection.RecordStore.tombstone]): the
      # envelope's top-level "identity" is cleared and a "tombstone" entry
      # is added, so the honest check is has(tombstone) and not
      # has(identity), never file absence.
      CT1_RECORD="$ESTATE/.tofu-records/tofu-records/$ESTATE_NAME/aws_s3_bucket/$(record_key 'aws_s3_bucket.count_test[1]')"
      [ -f "$CT1_RECORD" ] || fail "no local record file found for aws_s3_bucket.count_test[1] after the scale-down - expected a tombstoned record, not none at all"
      jq -e 'has("tombstone") and (has("identity") | not)' "$CT1_RECORD" >/dev/null \
        || fail "the record at aws_s3_bucket.count_test[1] after the scale-down is not tombstoned: $(cat "$CT1_RECORD")"
      log "  $CT1_NAME (count_test[1]) no longer exists (confirmed via head-bucket); $CT0_NAME (count_test[0]) unchanged CreationDate and marker; count_test[1]'s local record is tombstoned, not deleted - all read directly, not through choudoufu's own report"

      log "=== G2. scale count back up: 1 -> 2 ==="
      sleep 1
      count_test_block 2 > "$ESTATE/day2_count.tf"
      COUNT_UP_PLAN_OUT="$(plan_into 2>&1)"; COUNT_UP_PLAN_RC=$?
      [ "$COUNT_UP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | tail -30; fail "the scale-up plan exited $COUNT_UP_PLAN_RC"; }
      grep -qE '^  # aws_s3_bucket\.count_test\[1\] will be created' <<< "$COUNT_UP_PLAN_OUT" \
        || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-up plan does not create count_test[1]"; }
      grep -qE '^  # aws_s3_bucket\.count_test\[0\] will be' <<< "$COUNT_UP_PLAN_OUT" \
        && { printf '%s\n' "$COUNT_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-up plan touches count_test[0], which should be untouched"; }
      grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$COUNT_UP_PLAN_OUT" \
        || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | tail -10; fail "choudoufu's scale-up plan proposes something other than exactly one create"; }
      log "  choudoufu: exactly one create (count_test[1]), count_test[0] untouched"

      COUNT_UP_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_UP_APPLY_RC=$?
      [ "$COUNT_UP_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_UP_APPLY_OUT" | tail -30; fail "the scale-up apply exited $COUNT_UP_APPLY_RC"; }
      grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$COUNT_UP_APPLY_OUT" \
        || { grep -E 'Apply complete' <<< "$COUNT_UP_APPLY_OUT"; fail "the scale-up apply was not exactly one create"; }

      CT1_NEW_CREATED="$(awsl s3api list-buckets --query "Buckets[?Name=='$CT1_NAME'].CreationDate | [0]" --output text)"
      [ -n "$CT1_NEW_CREATED" ] && [ "$CT1_NEW_CREATED" != "None" ] || fail "no live count_test[1] bucket found after the scale-up"
      [ "$CT1_NEW_CREATED" != "$CT1_CREATED" ] || fail "count_test[1] came back with the SAME CreationDate ($CT1_CREATED) it had before being destroyed - the destroy in G1 was not real"
      CT1_NEW_ADDR_TAG="$(awsl s3api get-bucket-tagging --bucket "$CT1_NAME" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
      [ "$CT1_NEW_ADDR_TAG" = 'aws_s3_bucket.count_test:1' ] || fail "the recreated count_test[1] ($CT1_NAME) carries tofu-address=$CT1_NEW_ADDR_TAG, not aws_s3_bucket.count_test:1"
      CT0_CREATED_AFTER_UP="$(awsl s3api list-buckets --query "Buckets[?Name=='$CT0_NAME'].CreationDate | [0]" --output text)"
      [ "$CT0_CREATED_AFTER_UP" = "$CT0_CREATED" ] || fail "count_test[0]'s CreationDate changed across the scale-up"
      CT1_NEW_RECORD_ID="$(record_import_id "$CT1_RECORD" 2>/dev/null || true)"
      [ "$CT1_NEW_RECORD_ID" = "$CT1_NAME" ] \
        || fail "the record at aws_s3_bucket.count_test[1] after the scale-up names $CT1_NEW_RECORD_ID, not the recreated bucket $CT1_NAME"
      log "  count_test[1] recreated under the same bucket name ($CT1_NAME, deterministic) but a NEW CreationDate ($CT1_NEW_CREATED, was $CT1_CREATED), tofu-address=$CT1_NEW_ADDR_TAG; count_test[0] ($CT0_NAME) untouched throughout the down-then-up cycle - all read via the AWS CLI and the local record store"

      log "=== G3. one more plan: config and reality agree, nothing left to propose ==="
      COUNT_FINAL_PLAN_OUT="$(plan_into 2>&1)"; COUNT_FINAL_PLAN_RC=$?
      [ "$COUNT_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_FINAL_PLAN_OUT" | tail -30; fail "the post-scale-up plan exited $COUNT_FINAL_PLAN_RC"; }
      grep -qF "No changes. Your infrastructure matches the configuration." <<< "$COUNT_FINAL_PLAN_OUT" \
        || { grep -E '^  #' <<< "$COUNT_FINAL_PLAN_OUT"; fail "the post-scale-up plan is not empty"; }
      log "  No changes. The scale-down-then-up cycle is complete and invisible to the next plan."

      gauntlet_stage day2_count pass "choudoufu: scaling aws_s3_bucket.count_test from 2 to 1 destroyed exactly count_test[1] (0 add, 0 change, 1 destroy), leaving count_test[0]'s live CreationDate and tofu-address marker unchanged and tombstoning count_test[1]'s local record (has tombstone, no identity - the #398-guard shape); scaling back from 1 to 2 created exactly count_test[1] under the SAME bucket name (deterministic) but a NEW CreationDate (0 add, 0 change -> 1 add, 0 change, 0 destroy) while count_test[0] stayed untouched throughout; the next plan is empty; the G-ORACLE stock oracle on the same 2-instance count block, applied for real in the idle greenfield real-leg account, shows the identical shape: destroy the higher index only, create the higher index back under the same bucket name but a new CreationDate, the lower index's CreationDate unchanged both times"
    fi
    gauntlet_end_stage
  fi
  gauntlet_end_stage
fi
gauntlet_end_stage
gauntlet_end

log "=== PASS: all five stages, real, against hongbo-miao/hongbomiao.com's own ==="
log "=== unmodified storage-environment leaf modules, .tofu extension throughout ==="
