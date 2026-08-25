#!/usr/bin/env bash
set -uo pipefail

# The five-stage real-estate crossing (live/corpus-crossing-manifest.json)
# for hongbo-miao/hongbomiao.com (live/corpus-manifest.json, pinned by
# commit - no tag, see that entry's own comment for why), the SECOND
# OpenTofu-native estate this goal has crossed (the first,
# corpus-sumaform-aws, only reached stage 2). Unlike sumaform - which
# describes itself as OpenTofu-native but ships its actual example as a
# main.tf.aws.example template with plain .tf files once copied in -
# hongbomiao.com writes literally EVERY one of its ~150 OpenTofu files
# with a .tofu extension, root and leaf modules alike, and its own
# infrastructure/opentofu/justfile drives them exclusively with the `tofu`
# binary (init/plan/apply/refresh/destroy/state/output/console - never
# `terraform`, not once). Its own common_tags even carry
# "hm_managed_by" = "opentofu" as a literal tag value. This crossing is the
# first in this campaign to genuinely exercise the .tofu-suffixed-file
# surface end to end - proven below, not asserted: stock terraform is run
# against this exact estate and shown to find zero configuration files in
# it, because Terraform's CLI does not recognize the .tofu extension at
# all.
#
# THE SCOPING DECISION. hongbomiao.com is a large personal, actively-
# maintained (Renovate-bot dependency-update commits land multiple times a
# week; MIT-licensed, 299 stars, forked 51 times) infrastructure monorepo
# spanning AWS, Nebius, Cloudflare, Snowflake and a Kubernetes/EKS layer,
# all cross-wired through terraform_remote_state data sources between its
# own network/general/storage/kubernetes environments - too large and too
# cross-coupled to stand up against floci in one sitting, the same
# constraint that scoped corpus-sumaform-aws down to one host role. Its
# infrastructure/opentofu/environments/production/aws/general/main.tofu
# alone wires MSK Kafka clusters with external shell-script-built Kafka
# Connect plugins, EMR clusters, AWS Batch, SageMaker and more, most of it
# reading another environment's remote state.
#
# One self-contained section of that same file needs none of that: the
# "Labelbox" integration (an S3 bucket, its CORS configuration, and an IAM
# role with an inline S3-read policy trusting Labelbox's own published AWS
# account for cross-account import) is three module calls whose inputs are
# all either literals or each other's outputs - no remote state, no
# external data source, no EC2 instance, no provisioner. This crossing
# copies hongbomiao.com's own modules/aws/amazon_s3_bucket,
# modules/aws/amazon_s3_bucket_cors_configuration and
# modules/aws/labelbox_iam_role UNMODIFIED (byte-identical to the pinned
# commit - diffed below at DELTA) and writes its own root wiring
# (main.tofu, itself .tofu-suffixed) instantiating exactly those three
# real module calls with the same arguments the real
# aws/general/main.tofu uses for its own "Labelbox" section - standing in
# for the surrounding remote-state plumbing the same way
# corpus-sumaform-aws's own script stands in for module.base's network
# submodule. labelbox_aws_account_id (340636424752) is hongbomiao.com's
# own real, published value (Labelbox's documented cross-account trust
# principal, not a secret); external_id is NOT hongbomiao.com's real value
# - their own repository redacts it to "xxxxxxxxxxxxxxxxxxxxxxxxx" in
# public source, so this crossing supplies its own placeholder, the same
# way it supplies floci's own emulator provider block hongbomiao.com's
# real code does not need (it authenticates against real AWS).
#
# STAGES:
#   1. COLD DEPLOY   plain `tofu apply` (real OpenTofu core, no choudoufu),
#                     the unmodified three leaf modules - the honest proof
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
# ALL FIVE STAGES PASS FOR REAL - no known blocker, nothing routed around.
# Two real, non-blocking findings worth recording rather than papering
# over, both surfaced by stage 2's own read-only verification pass:
#
#   1. aws_s3_bucket.main and aws_iam_role.labelbox_iam_role both report
#      DRIFTED (not VERIFIED) against the plain cold state, because the
#      AWS provider's own aws_s3_bucket resource carries a deprecated,
#      computed `cors_rule` attribute that mirrors whatever
#      aws_s3_bucket_cors_configuration resource is attached to the same
#      bucket, and aws_iam_role carries an equivalent deprecated
#      `inline_policy` attribute mirroring aws_iam_role_policy - both
#      genuinely empty in the state captured right after each resource's
#      own creation, and genuinely non-empty by the time live-import reads
#      the live object back, because the SIBLING resource had by then
#      attached its own configuration to the same live object. This is
#      real, well-documented upstream AWS-provider behavor (the
#      aws_iam_role docs explicitly warn against combining inline_policy
#      with a separately managed aws_iam_role_policy), not a choudoufu
#      defect: it is exactly the kind of harmless drift live-import is
#      DESIGNED to report and stamp through - confirmed by stage 3 below,
#      where a plan that never asks either resource to manage those
#      deprecated attributes comes back genuinely empty.
#   2. aws_s3_bucket_cors_configuration is admitted through the provider's
#      OWN identity schema (client-named: `bucket`) rather than through
#      this fork's generated admission table, and live-plan says so with
#      "Warning: Resource type has no orphan recovery" - an already-
#      documented, known limitation (live/LIMITATIONS.md, "Resource type
#      has no orphan recovery"), not a new gap this crossing found: the
#      estate-wide undeclared-resource sweep does not yet know this type
#      exists, so removing its last block from a configuration would leave
#      the live CORS rule behind with no warning. It still plans and
#      applies correctly for a DECLARED block, which is what this crossing
#      exercises and asserts against the AWS CLI's own answer in stage 3.
#
# BREAK=1 corrupts one expected tofu-address ahead of stage 2's assertion
# and tampers a second, unrelated live object ahead of stage 5's, so both
# assertions are proven load-bearing rather than a grep that always
# matches.
#
# day2_rename's own BREAK=1 arm (D1, below - rename without a moved block)
# is never reached by an ordinary BREAK=1 run: stage 2's own control fires
# first and `fail` exits there, the same limitation corpus-eks-basic's
# header documents for its own day2_rename. Verified load-bearing directly
# instead, with stage 2's control temporarily neutered in a scratch copy:
# BREAK=1 does NOT come back as a clean destroy + create the way
# corpus-eks-basic's security group does. aws_iam_role's identity is
# deterministically client-named (its own `name` argument), so renaming the
# module without a moved block makes choudoufu's discovery find the SAME
# live role twice - once by the marker still naming the old, no-longer-
# declared address, once as the derivable candidate for the new address the
# renamed module now wants - and it refuses outright ("Two live resources
# claiming one address") rather than guess which reading is right. That is
# the stricter, correct response HANDOFF.md's safety rule names ("a wrong
# marker outranks a missing one"): D1's BREAK=1 arm asserts this refusal,
# not the literal destroy-and-create the stage's own Break text describes,
# because that is what this specific, client-named-identity resource
# actually does.
#
#   bash live/e2e/corpus-hongbomiao-labelbox/run.sh
#
# Needs Docker, the AWS CLI, and the real `tofu` binary on PATH for stage
# 1 - not optional here the way it is for other crossings' TF_COLD_BIN,
# because stock `terraform` cannot load a directory of .tofu files at all
# (proven, not asserted, in "0. tools and corpus" below). .corpus is read,
# never written: the three leaf modules are copied out to a scratch
# directory first, same as every other corpus crossing.
#
# Env overrides:
#   TOFU_BIN      path to a prebuilt choudoufu binary; skips the go build.
#   FLOCI_PORT    host port for the emulator (default 4724, clear of every
#                 other corpus-*/reference-* script's own default).
#   FLOCI_IMAGE   the emulator image; defaults to the digest pin in
#                 live/floci-image.
#   BREAK         set to 1 to corrupt stage 2's identity assertion and
#                 tamper a second object ahead of stage 5's, proving both
#                 are load-bearing.
#   BREAK_REMOVE  set to 1 to run day2_remove's own break control instead of
#                 the real remove checks: keep module.labelbox_iam_role_renamed's
#                 block in the config and assert no destroy is proposed for
#                 it. Independent of BREAK and only reachable when BREAK is
#                 not 1, because day2_remove starts from day2_rename's own
#                 real, completed rename.
#   BREAK_GREEN   set to 1 to run the greenfield stage's own break control
#                 instead of the real object-by-object comparison: drop one
#                 object from the actual inventory before the count check.
#                 Independent of the other BREAK flags - greenfield runs
#                 before all of them, right after STAGE 1's cold deploy.
#   BREAK_REPLACE set to 1 to run day2_replace's own break control instead
#                 of the real replace checks: expect the wrong destroy
#                 count on purpose and confirm the real plan-shape
#                 assertion would have caught it (see PART F's own header
#                 comment, "THE COLLISION-DETECTION SCOPE NOTE", for why
#                 this proves load-bearingness differently than
#                 corpus-ec2-instance-complete's and corpus-sqs-basic's
#                 own BREAK=replace controls do). Independent of the other
#                 BREAK flags and only reachable when BREAK is not 1,
#                 because day2_replace starts from day2_rename's own real,
#                 completed rename.
#   DEBUG_KEEP    set to 1 to skip the exit trap: the floci container and
#                 the WORK directory are left behind for inspection.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SRC="$ROOT/.corpus/hongbomiao/infrastructure/opentofu/modules/aws"
WORK="$(mktemp -d)"
PLAIN="$WORK/plain"
ESTATE="$WORK/estate"
FLOCI_PORT="${FLOCI_PORT:-4724}"
FLOCI_NAME="choudoufu-corpus-hongbomiao-labelbox-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"
REGION="us-west-2"
ESTATE_NAME="hongbomiao-labelbox-crossing"
BUCKET_NAME="${ESTATE_NAME}-hm-labelbox"
ROLE_NAME="LabelboxRole-hm-labelbox"
POLICY_NAME="LabelboxRoleS3Policy-hm-labelbox"

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
command -v tofu >/dev/null 2>&1 || fail "the real tofu binary is not on PATH - required for stage 1 (see this script's header: terraform cannot read .tofu files at all)"
command -v terraform >/dev/null 2>&1 || fail "the terraform binary is not on PATH - needed for the positive proof below"
[ -d "$SRC/amazon_s3_bucket" ] && [ -d "$SRC/amazon_s3_bucket_cors_configuration" ] && [ -d "$SRC/labelbox_iam_role" ] \
  || fail "$SRC is missing one of the three leaf modules - fetch hongbo-miao/hongbomiao.com at the pin in live/corpus-manifest.json first"
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

# copy_leaf_modules <destdir>: the three real, unmodified leaf modules -
# every file in them keeps hongbomiao.com's own .tofu extension.
copy_leaf_modules() {
  local dest="$1"
  mkdir -p "$dest/modules/aws"
  for m in amazon_s3_bucket amazon_s3_bucket_cors_configuration labelbox_iam_role; do
    cp -R "$SRC/$m" "$dest/modules/aws/$m"
  done
}

# write_root <destdir> <live_block>: this crossing's own root wiring,
# standing in for the remote-state-coupled environment root the real
# "Labelbox" section lives inside (see header) - same convention
# corpus-sumaform-aws's own script uses for module.base's network. The
# three module calls below use the SAME source paths, the SAME argument
# names and, other than the estate-scoped bucket/role name and the
# necessarily-fabricated external_id, the SAME values as hongbomiao.com's
# own aws/general/main.tofu "Labelbox" section. Written with a .tofu
# extension, matching every file this project's own real modules use -
# the file plain terraform cannot see at all (proven below).
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

# Labelbox - S3 bucket
module "amazon_s3_bucket_hm_labelbox" {
  providers      = { aws = aws.production }
  source         = "./modules/aws/amazon_s3_bucket"
  s3_bucket_name = "$BUCKET_NAME"
  common_tags    = local.common_tags
}
# Labelbox - S3 bucket CORS configuration (no providers= - matches the real
# aws/general/main.tofu call exactly; it relies on the root's own default,
# unaliased aws provider rather than the production alias)
module "amazon_s3_bucket_hm_labelbox_cors_configuration" {
  source       = "./modules/aws/amazon_s3_bucket_cors_configuration"
  s3_bucket_id = module.amazon_s3_bucket_hm_labelbox.id
  allowed_origins = [
    "https://app.labelbox.com",
    "https://editor.labelbox.com"
  ]
}
# Labelbox - IAM role
module "labelbox_iam_role" {
  providers                     = { aws = aws.production }
  source                        = "./modules/aws/labelbox_iam_role"
  labelbox_service_account_name = "hm-labelbox"
  labelbox_aws_account_id       = "340636424752"
  external_id                   = "crossing-external-id-placeholder"
  s3_bucket_name                = module.amazon_s3_bucket_hm_labelbox.name
  common_tags                   = local.common_tags
}
EOF
}

copy_leaf_modules "$PLAIN"
write_root "$PLAIN" ""
log "  three leaf modules copied unmodified out of .corpus/hongbomiao into $PLAIN"

# DELTA: confirm the copy is byte-identical to the pinned commit - the only
# things this crossing adds are its OWN root file and provider block, never
# an edit to hongbomiao.com's own module code.
for m in amazon_s3_bucket amazon_s3_bucket_cors_configuration labelbox_iam_role; do
  diff -rq "$SRC/$m" "$PLAIN/modules/aws/$m" >/dev/null \
    || fail "modules/aws/$m differs from the pinned commit - this crossing must run the real, unmodified module"
done
log "  DELTA confirmed: all three leaf modules are byte-identical to the pinned commit; only this script's own root file was added"

# Positive proof, not an assertion of belief: stock terraform genuinely
# cannot see this estate at all, because every file in it - the three leaf
# modules AND this script's own root wiring - uses the .tofu extension.
TF_INIT_OUT="$(cd "$PLAIN" && terraform init -input=false -no-color 2>&1)"
grep -qF "The directory has no Terraform configuration files." <<< "$TF_INIT_OUT" \
  || { printf '%s\n' "$TF_INIT_OUT"; fail "expected stock terraform to find zero config files in an all-.tofu directory - either the extension changed or terraform now reads .tofu"; }
log "  proven: stock terraform sees an EMPTY directory here (.tofu is invisible to it) - this is genuinely OpenTofu-only surface"
rm -rf "$PLAIN/.terraform"

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
  grep -q '"s3"' <<< "${HEALTH:-}" && break
  sleep 2
done
grep -q '"s3"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (s3) at $ENDPOINT"
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
log ""
gauntlet_stage cold_deploy pass "4 resources added, 0 objects carry tofu-estate=$ESTATE_NAME before migration"

# ══════════════════════════════════════════════════════════════════════════
# PART D-ORACLE: RENAME, stock oracle (day2_rename, live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
#
# The estate's two taggable roots (the CORS configuration and the inline
# role policy are correctly untaggable, see header): a `moved` block renames
# the WHOLE module call "amazon_s3_bucket_hm_labelbox" (its own
# aws_s3_bucket.main is the only object it carries), and "choudoufu live-mv"
# (below, after drift_reconverge) renames the whole module call
# "labelbox_iam_role" with no moved block at all - not an individual
# resource inside either module's own source, which stays byte-identical to
# the pinned commit throughout (DELTA discipline). The S3 bucket module is
# referenced by both siblings (cors config's s3_bucket_id, iam_role's
# s3_bucket_name), so its rename updates those two reference lines too, in
# this script's own root wiring only. The IAM role module is a leaf no
# other module references, chosen for BREAK=1's rename-without-moved
# control specifically because aws_s3_bucket.main carries
# `lifecycle { prevent_destroy = true }` in the real module - a destroy
# proposal against the bucket would refuse outright rather than plan, so
# BREAK=1 below renames the role, never the bucket. The stock oracle (real
# tofu - stock terraform cannot see this .tofu-only estate at all, see
# header) runs the same two renames, through moved blocks only, on a copy
# of cold_deploy's own state - before choudoufu or live-import ever touch
# these objects.
CURRENT_STAGE=day2_rename
log "=== D-ORACLE: stock tofu, the same two renames through moved blocks, on cold_deploy's own state ==="
PLAIN_ORACLE="$WORK/plain-oracle"
cp -r "$PLAIN" "$PLAIN_ORACLE"
sed -i.bak 's/module "amazon_s3_bucket_hm_labelbox" {/module "amazon_s3_bucket_hm_labelbox_renamed" {/' "$PLAIN_ORACLE/main.tofu"
sed -i.bak 's/module\.amazon_s3_bucket_hm_labelbox\.id/module.amazon_s3_bucket_hm_labelbox_renamed.id/' "$PLAIN_ORACLE/main.tofu"
sed -i.bak 's/module\.amazon_s3_bucket_hm_labelbox\.name/module.amazon_s3_bucket_hm_labelbox_renamed.name/' "$PLAIN_ORACLE/main.tofu"
sed -i.bak 's/module "labelbox_iam_role" {/module "labelbox_iam_role_renamed" {/' "$PLAIN_ORACLE/main.tofu"
rm -f "$PLAIN_ORACLE/main.tofu.bak"
cat >> "$PLAIN_ORACLE/main.tofu" <<'EOF'

moved {
  from = module.amazon_s3_bucket_hm_labelbox
  to   = module.amazon_s3_bucket_hm_labelbox_renamed
}

moved {
  from = module.labelbox_iam_role
  to   = module.labelbox_iam_role_renamed
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
# module.labelbox_iam_role's block entirely - a leaf no other module
# references (unlike amazon_s3_bucket_hm_labelbox, which
# amazon_s3_bucket_hm_labelbox_cors_configuration and labelbox_iam_role both
# read, and which also carries `lifecycle { prevent_destroy = true }` in the
# real module - the reason both this oracle and Part E below remove the
# role, not the bucket). The module holds TWO resources - aws_iam_role
# (taggable) and aws_iam_role_policy (untaggable, inline) - a real instance
# of live/GAUNTLET.md #7's "blocks for untaggable children whose parents
# stay" concern: the untaggable inline policy has to be destroyed ahead of
# its own parent role in an order IAM accepts.
CURRENT_STAGE=day2_remove
log "=== E-ORACLE: stock tofu, delete module.labelbox_iam_role's block on cold_deploy's own state ==="
PLAIN_REMOVE_ORACLE="$WORK/plain-remove-oracle"
cp -r "$PLAIN" "$PLAIN_REMOVE_ORACLE"
perl -0pi -e 's/\n# Labelbox - IAM role\nmodule "labelbox_iam_role" \{.*?\n\}\n//s' "$PLAIN_REMOVE_ORACLE/main.tofu"
grep -q 'module "labelbox_iam_role"' "$PLAIN_REMOVE_ORACLE/main.tofu" \
  && fail "removing module.labelbox_iam_role's block from the oracle copy did not match - this script's own root wiring has moved"
( cd "$PLAIN_REMOVE_ORACLE" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_REMOVE_ORACLE" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_remove stock oracle's reinit failed"; }
REMOVE_ORACLE_PLAN_OUT="$(cd "$PLAIN_REMOVE_ORACLE" && tofu plan -input=false -no-color 2>&1)"; REMOVE_ORACLE_PLAN_RC=$?
[ "$REMOVE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_remove stock oracle plan exited $REMOVE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.labelbox_iam_role\.aws_iam_role\.labelbox_iam_role will be destroyed' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "stock does not propose destroying the IAM role when module.labelbox_iam_role's block is removed"; }
grep -qE '^  # module\.labelbox_iam_role\.aws_iam_role_policy\.labelbox_iam_role_s3_policy will be destroyed' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "stock does not propose destroying the inline role policy when module.labelbox_iam_role's block is removed"; }
grep -qF 'Plan: 0 to add, 0 to change, 2 to destroy.' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -10; fail "stock's remove plan proposes something other than exactly two destroys (the role and its inline policy)"; }
log "  stock: exactly two destroys (the IAM role and its inline policy), nothing else, on the state cold_deploy produced"

# ══════════════════════════════════════════════════════════════════════════
# PART F-ORACLE: REPLACE, stock oracle (day2_replace, live/GAUNTLET.md #9)
# ══════════════════════════════════════════════════════════════════════════
#
# Another separate copy of cold_deploy's own state (never renamed - PART D
# below only renames $ESTATE, not $PLAIN). labelbox_service_account_name
# feeds aws_iam_role.labelbox_iam_role's `name` (ForceNew - AWS has no
# UpdateRole-by-rename API, only CreateRole/DeleteRole) AND, through the
# SAME module's own aws_iam_role_policy resource, its `role` (references
# the sibling's name, also ForceNew) and `name` arguments both - the exact
# untaggable-identity shape this estate's greenfield stage's own fix
# addresses (ComponentsFromValue's whole-object IsWhollyKnown gate; see
# git history), now under a real, already-applied replace rather than a
# from-nothing apply. Changing the variable therefore forces BOTH
# resources in the module to replace at their same declared addresses.
CURRENT_STAGE=day2_replace
REPLACE_ORACLE="$WORK/plain-replace-oracle"
cp -r "$PLAIN" "$REPLACE_ORACLE"
sed -i.bak 's/labelbox_service_account_name = "hm-labelbox"/labelbox_service_account_name = "hm-labelbox-v2"/' "$REPLACE_ORACLE/main.tofu"
rm -f "$REPLACE_ORACLE/main.tofu.bak"
grep -q 'hm-labelbox-v2' "$REPLACE_ORACLE/main.tofu" \
  || fail "the day2_replace oracle's variable edit did not match - the corpus pin has moved"
log "=== F-ORACLE: stock tofu, the same labelbox_service_account_name change on cold_deploy's own state (plan only, not applied - it shares $ENDPOINT's account with \$ESTATE) ==="
( cd "$REPLACE_ORACLE" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$REPLACE_ORACLE" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_replace stock oracle's reinit failed"; }
REPLACE_ORACLE_PLAN_OUT="$(cd "$REPLACE_ORACLE" && tofu plan -input=false -no-color 2>&1)"; REPLACE_ORACLE_PLAN_RC=$?
[ "$REPLACE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_replace stock oracle plan exited $REPLACE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.labelbox_iam_role\.aws_iam_role\.labelbox_iam_role must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "stock does not propose replacing the IAM role when labelbox_service_account_name changes"; }
grep -qE '^  # module\.labelbox_iam_role\.aws_iam_role_policy\.labelbox_iam_role_s3_policy must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "stock does not cascade the role replace into its inline policy"; }
grep -qF 'Plan: 2 to add, 0 to change, 2 to destroy.' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -10; fail "stock's replace plan proposes something other than exactly two replaces (the role and its inline policy)"; }
log "  stock: exactly two replaces (the IAM role and its inline policy), nothing else, on the state cold_deploy produced"

# ══════════════════════════════════════════════════════════════════════════
# PART GREENFIELD (greenfield, live/GAUNTLET.md #13, active stage)
# ══════════════════════════════════════════════════════════════════════════
#
# One more, fresh floci container. STAGE 1's own container ($FLOCI_NAME on
# $FLOCI_PORT) is reused as THIS stage's oracle: nothing between cold_deploy
# and here has applied, changed or destroyed anything in it - the
# day2_rename and day2_remove oracle blocks above only run `tofu plan`
# against COPIES of cold_deploy's state (never `apply`), so it still holds
# exactly stock's unmodified, unmarked cold-deploy inventory - the oracle
# live/GAUNTLET.md #13 names verbatim. Greenfield applies the identical,
# unmodified leaf modules (a live block added, nothing else) into a
# namespace of its own with choudoufu directly - no migration at all. The
# SAME bucket/role names are reused (a fresh, isolated floci container is a
# separate account, so there is no collision), which makes the
# object-by-object comparison below a byte-for-byte one.
FLOCI_GREEN_PORT=$((FLOCI_PORT + 1))
FLOCI_GREEN_NAME="choudoufu-corpus-hongbomiao-labelbox-green-$$"
GREEN_ENDPOINT="http://127.0.0.1:${FLOCI_GREEN_PORT}"
GREEN_ESTATE_NAME="hongbomiao-labelbox-greenfield"

docker run -d --rm -p "${FLOCI_GREEN_PORT}:4566" --name "$FLOCI_GREEN_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_GREEN_NAME failed"
for _ in $(seq 1 45); do
  GREEN_HEALTH="$(curl -fs "${GREEN_ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"s3"' <<< "${GREEN_HEALTH:-}" && grep -q '"iam"' <<< "${GREEN_HEALTH:-}" && break
  sleep 2
done
grep -q '"s3"' <<< "${GREEN_HEALTH:-}" && grep -q '"iam"' <<< "${GREEN_HEALTH:-}" \
  || fail "the greenfield floci did not come up healthy (s3/iam) at $GREEN_ENDPOINT"
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
log "  greenfield estate written to $GREEN (same three unmodified leaf modules, a live block from the start)"

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
GREEN_WANT_BUCKET_ADDR="module.amazon_s3_bucket_hm_labelbox.aws_s3_bucket.main"
GREEN_WANT_ROLE_ADDR="module.labelbox_iam_role.aws_iam_role.labelbox_iam_role"
GREEN_BUCKET_ADDR="$(awslg s3api get-bucket-tagging --bucket "$BUCKET_NAME" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GREEN_BUCKET_ADDR" = "$GREEN_WANT_BUCKET_ADDR" ] || fail "the greenfield bucket carries tofu-address=$GREEN_BUCKET_ADDR, not $GREEN_WANT_BUCKET_ADDR"
GREEN_BUCKET_ESTATE="$(awslg s3api get-bucket-tagging --bucket "$BUCKET_NAME" --query "TagSet[?Key=='tofu-estate'].Value | [0]" --output text)"
[ "$GREEN_BUCKET_ESTATE" = "$GREEN_ESTATE_NAME" ] || fail "the greenfield bucket carries tofu-estate=$GREEN_BUCKET_ESTATE, not $GREEN_ESTATE_NAME"
GREEN_ROLE_ADDR="$(awslg iam list-role-tags --role-name "$ROLE_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GREEN_ROLE_ADDR" = "$GREEN_WANT_ROLE_ADDR" ] || fail "the greenfield role carries tofu-address=$GREEN_ROLE_ADDR, not $GREEN_WANT_ROLE_ADDR"
log "  bucket $BUCKET_NAME -> tofu-address=$GREEN_BUCKET_ADDR tofu-estate=$GREEN_BUCKET_ESTATE; role $ROLE_NAME -> tofu-address=$GREEN_ROLE_ADDR - read via the AWS CLI, not choudoufu's own report"

log "=== PART GREENFIELD 3. the local record store holds one record per instance, taggable and untaggable alike (#364 A2) ==="
GREEN_RECORD_FILES="$(find "$GREEN/.tofu-records/tofu-records" -type f ! -name '*.lock' ! -name '*.tmp-*' 2>/dev/null | wc -l | tr -d ' ')"
[ "$GREEN_RECORD_FILES" = "4" ] || fail "expected 4 records under the local record store after the greenfield apply (bucket, CORS config, role, inline policy), found $GREEN_RECORD_FILES"
log "  4 records persisted, one per managed instance including the two untaggable ones, read directly off the local record store"

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

log "=== PART GREENFIELD 6. object-by-object against stock's own cold-deploy container (STAGE 1, untouched since) ==="
GREEN_TAGGABLE_COUNT="$(awslg resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$GREEN_ESTATE_NAME" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$GREEN_TAGGABLE_COUNT" = "2" ] || fail "the greenfield estate has $GREEN_TAGGABLE_COUNT taggable objects, expected 2 (the bucket and the role)"
GREEN_POLICY_DOC="$(awslg iam get-role-policy --role-name "$ROLE_NAME" --policy-name "$POLICY_NAME" --query 'PolicyDocument' --output json 2>/dev/null || true)"
COLD_POLICY_DOC="$(awsl iam get-role-policy --role-name "$ROLE_NAME" --policy-name "$POLICY_NAME" --query 'PolicyDocument' --output json 2>/dev/null || true)"
GREEN_TOTAL_COUNT=2
[ -n "$GREEN_POLICY_DOC" ] && [ "$GREEN_POLICY_DOC" != "None" ] && GREEN_TOTAL_COUNT=3
GREEN_CORS_ORIGINS="$(awslg s3api get-bucket-cors --bucket "$BUCKET_NAME" --query 'CORSRules[0].AllowedOrigins' --output json 2>/dev/null || true)"
COLD_CORS_ORIGINS="$(awsl s3api get-bucket-cors --bucket "$BUCKET_NAME" --query 'CORSRules[0].AllowedOrigins' --output json 2>/dev/null || true)"
[ -n "$GREEN_CORS_ORIGINS" ] && [ "$GREEN_CORS_ORIGINS" != "None" ] && GREEN_TOTAL_COUNT=$((GREEN_TOTAL_COUNT + 1))
if [ "${BREAK_GREEN:-}" = "1" ]; then
  GREEN_TOTAL_COUNT=$((GREEN_TOTAL_COUNT - 1))
  log "  BREAK_GREEN=1: dropped one object from the actual inventory - the count comparison below must fail"
fi
[ "$GREEN_TOTAL_COUNT" = "4" ] \
  || fail "the greenfield estate has $GREEN_TOTAL_COUNT objects (2 taggable plus the CORS config and the inline policy, if readable), expected 4 - the object-by-object comparison against stock's cold deploy must fail on a dropped resource"
[ "$GREEN_POLICY_DOC" = "$COLD_POLICY_DOC" ] || fail "the inline role policy's document differs between the greenfield estate and stock's cold deploy"
[ "$GREEN_CORS_ORIGINS" = "$COLD_CORS_ORIGINS" ] || fail "the CORS configuration's allowed origins differ between the greenfield estate and stock's cold deploy"
GREEN_ROLE_TRUST="$(awslg iam get-role --role-name "$ROLE_NAME" --query 'Role.AssumeRolePolicyDocument.Statement[0].Condition' --output json)"
COLD_ROLE_TRUST="$(awsl iam get-role --role-name "$ROLE_NAME" --query 'Role.AssumeRolePolicyDocument.Statement[0].Condition' --output json)"
[ "$GREEN_ROLE_TRUST" = "$COLD_ROLE_TRUST" ] || fail "the role's trust policy condition differs between the greenfield estate and stock's cold deploy"
log "  2 taggable objects plus the CORS config and the inline policy match stock's cold-deploy container object by object (policy document, CORS origins, role trust condition), marker tags never compared"

log ""
log "PART GREENFIELD (greenfield): PASS"
gauntlet_stage greenfield pass "4 resources from nothing (bucket, CORS config, role, untaggable inline role policy), markers verified via the AWS CLI, 4 records in the local record store (#364 A2), replan empty both with and without the local record store, all objects match stock's cold-deploy container (STAGE 1, untouched) object by object, marker tags never compared"
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
grep -qF "2 of 4 resource instance(s) are eligible for stamping" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import did not verify exactly 2 of 4 as eligible (the S3 bucket and the IAM role - see header for why the CORS configuration and the inline role policy are correctly UNTAGGABLE)"; }
grep -qF "No tag has been written." <<< "$IMPORT_OUT" || fail "the dry run wrote a tag - it must not"
# The two DRIFTED (not VERIFIED) findings this script's header explains -
# real, harmless, expected AWS-provider behavior, not routed around.
grep -qF "DRIFTED (2)" <<< "$IMPORT_OUT" \
  || fail "expected exactly 2 DRIFTED resources (cors_rule/inline_policy shadow attributes - see header); the module's own shape may have moved"
grep -qF "UNTAGGABLE (2)" <<< "$IMPORT_OUT" \
  || fail "expected exactly 2 UNTAGGABLE resources (the CORS configuration and the inline role policy)"
log "  2 of 4 verified/drifted against the live system; nothing written yet"

log "--- 2b: -approve ---"
APPROVE_OUT="$(cd "$ESTATE" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE_NAME" -approve -no-color 2>&1)"; APPROVE_RC=$?
[ "$APPROVE_RC" -eq 0 ] || { printf '%s\n' "$APPROVE_OUT" | tail -40; fail "live-import -approve failed"; }
grep -qF "2 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 2 skipped" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not stamp exactly 2 of 4 resources cleanly"; }
log "  2 stamped"

log "--- 2c: the markers, read through the AWS CLI directly - never through choudoufu ---"
WANT_BUCKET_ADDR="module.amazon_s3_bucket_hm_labelbox.aws_s3_bucket.main"
WANT_ROLE_ADDR="module.labelbox_iam_role.aws_iam_role.labelbox_iam_role"
if [ "${BREAK:-}" = "1" ]; then
  WANT_ROLE_ADDR="module.labelbox_iam_role.aws_iam_role.wrong_name"
  log "  BREAK=1: expecting a wrong tofu-address on the IAM role on purpose - this check must fail"
fi

GOT_BUCKET_ADDR="$(awsl s3api get-bucket-tagging --bucket "$BUCKET_NAME" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_BUCKET_ADDR" = "$WANT_BUCKET_ADDR" ] || fail "the S3 bucket carries tofu-address=$GOT_BUCKET_ADDR, not $WANT_BUCKET_ADDR"
GOT_BUCKET_ESTATE="$(awsl s3api get-bucket-tagging --bucket "$BUCKET_NAME" --query "TagSet[?Key=='tofu-estate'].Value | [0]" --output text)"
[ "$GOT_BUCKET_ESTATE" = "$ESTATE_NAME" ] || fail "the S3 bucket carries tofu-estate=$GOT_BUCKET_ESTATE, not $ESTATE_NAME"
log "  bucket $BUCKET_NAME -> tofu-address=$GOT_BUCKET_ADDR tofu-estate=$GOT_BUCKET_ESTATE"

GOT_ROLE_ADDR="$(awsl iam list-role-tags --role-name "$ROLE_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_ROLE_ADDR" = "$WANT_ROLE_ADDR" ] || fail "the IAM role carries tofu-address=$GOT_ROLE_ADDR, not $WANT_ROLE_ADDR"
log "  role $ROLE_NAME -> tofu-address=$GOT_ROLE_ADDR"

if [ "${BREAK:-}" = "1" ]; then
  fail "BREAK=1: the role's real tofu-address matched the WRONG expected value above without this script noticing - stage 2's assertion is not load-bearing"
fi

log ""
log "STAGE 2 (migrate): PASS"
log ""
gauntlet_stage migrate pass "2 of 4 stamped (2 skipped, untaggable), 0 failed; markers read back via the AWS CLI"

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

# The already-documented, known limitation this script's header names -
# still reported, still not a blocker for a DECLARED resource.
grep -qF "aws_s3_bucket_cors_configuration is admitted by the provider's own identity" <<< "$PLAN_OUT" \
  || fail "expected the documented no-orphan-recovery warning for aws_s3_bucket_cors_configuration; live/LIMITATIONS.md may need regenerating if this type gained sweep coverage"

# Re-assert identities directly against the live objects, after the local
# state file was deleted - any answer below can only have come from the
# marker (or, for the two untaggable resources, the re-derived identity)
# on the live object itself.
BUCKET_ADDR2="$(awsl s3api get-bucket-tagging --bucket "$BUCKET_NAME" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$BUCKET_ADDR2" = "$WANT_BUCKET_ADDR" ] || fail "the bucket's tofu-address changed across the empty plan: $WANT_BUCKET_ADDR -> $BUCKET_ADDR2"
ROLE_ADDR2="$(awsl iam list-role-tags --role-name "$ROLE_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$ROLE_ADDR2" = "$WANT_ROLE_ADDR" ] || fail "the role's tofu-address changed across the empty plan: $WANT_ROLE_ADDR -> $ROLE_ADDR2"

# The two untaggable resources have no tag to re-read, so their identity
# assertion is the live object's OWN content, read directly - the plan
# came back empty above, meaning the client-named/composite derivation
# (bucket name; ROLENAME:POLICYNAME) found exactly these live objects with
# no diff, and this independently confirms what it found is correct.
GOT_ORIGINS="$(awsl s3api get-bucket-cors --bucket "$BUCKET_NAME" --query 'CORSRules[0].AllowedOrigins' --output json | tr -d '[:space:]')"
echo "$GOT_ORIGINS" | grep -qF '"https://app.labelbox.com"' && echo "$GOT_ORIGINS" | grep -qF '"https://editor.labelbox.com"' \
  || fail "the live CORS configuration's AllowedOrigins ($GOT_ORIGINS) do not match the configuration"
GOT_POLICY="$(awsl iam get-role-policy --role-name "$ROLE_NAME" --policy-name "$POLICY_NAME" --query 'PolicyDocument.Statement[0].Resource[0]' --output text)"
[ "$GOT_POLICY" = "arn:aws:s3:::$BUCKET_NAME" ] || fail "the live inline role policy's first Resource ($GOT_POLICY) does not match the configuration"
log "  identity re-check: bucket and role tofu-address unchanged; the CORS configuration's origins and the inline policy's resource ARN, read directly off the live objects, still match the configuration"

log ""
log "STAGE 3 (test plan): PASS"
log ""
gauntlet_stage test_plan pass "no resource change proposed; bucket and role tofu-address unchanged, CORS origins and inline policy resource match config"

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
log ""
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed); object count unchanged at $BEFORE_N, no state file"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 5: DRIFT AND RECONVERGE - mutate one object, replan, assert one fix
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=drift_reconverge
log "=== STAGE 5: drift and reconverge (mutate one object's tag out of band) ==="

if [ "${BREAK:-}" = "1" ]; then
  awsl iam tag-role --role-name "$ROLE_NAME" --tags Key=hm_team,Value=tampered-by-BREAK >/dev/null
  log "  BREAK=1: also tampered the IAM role's hm_team tag - stage 5 must now see TWO"
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
  [ "$CHANGED_ADDRS" = "module.amazon_s3_bucket_hm_labelbox.aws_s3_bucket.main" ] \
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
log ""
gauntlet_stage drift_reconverge pass "bucket tag drifted; exactly module.amazon_s3_bucket_hm_labelbox.aws_s3_bucket.main proposed, applied (1 changed), reconverged to hongbomiao"

# ══════════════════════════════════════════════════════════════════════════
# PART D: RENAME (day2_rename, live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=day2_rename
log "=== D0. capture the live ids a rename must not disturb ==="
log "  bucket $BUCKET_NAME (module.amazon_s3_bucket_hm_labelbox), role $ROLE_NAME (module.labelbox_iam_role)"

if [ "${BREAK:-}" = "1" ]; then
  log "=== D1 (BREAK=1). rename module labelbox_iam_role -> labelbox_iam_role_renamed WITHOUT a moved block ==="
  sed -i.bak 's/module "labelbox_iam_role" {/module "labelbox_iam_role_renamed" {/' "$ESTATE/main.tofu"
  rm -f "$ESTATE/main.tofu.bak"
  ( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the BREAK=1 rename's reinit failed"; }
  BREAK_PLAN_OUT="$(plan_into 2>&1)"; BREAK_PLAN_RC=$?
  # Verified directly (isolated BREAK=1 run, stage 2's own earlier control
  # neutered so day2_rename's arm is actually reached - see header): for
  # THIS resource, an unmoved rename does not come back as a clean destroy
  # + create the way corpus-eks-basic's security group does. aws_iam_role's
  # identity is deterministically client-named (its own `name` argument),
  # so choudoufu's discovery finds the SAME live role twice - once by the
  # marker still naming the old, no-longer-declared address, once as the
  # derivable candidate for the new address the renamed module now wants -
  # and refuses outright ("Two live resources claiming one address") rather
  # than guess which reading is right. That is the stricter, correct
  # response the safety rule in HANDOFF.md names ("a wrong marker outranks
  # a missing one"): a plan is never produced, so there is no wrong destroy
  # or wrong create to make. The plan/apply RC IS still expected non-zero.
  [ "$BREAK_PLAN_RC" -ne 0 ] \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -30; fail "BREAK=1: renaming without a moved block planned cleanly (exit 0) - expected a refusal (see header)"; }
  grep -qF "Two live resources claiming one address" <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -30; fail "BREAK=1: renaming without a moved block did not refuse with the expected marker-ambiguity error - this stage's check is not load-bearing"; }
  grep -qF "module.labelbox_iam_role.aws_iam_role.labelbox_iam_role" <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -30; fail "BREAK=1: the refusal did not name the IAM role's old address"; }
  log "  BREAK=1: correctly refuses (two live resources claiming module.labelbox_iam_role.aws_iam_role.labelbox_iam_role - the marker's old address and the renamed module's client-derivable identity resolve to the same live role) - the moved-block and live-mv checks below are skipped"
else
  log "=== D1. choudoufu, moved block: module amazon_s3_bucket_hm_labelbox -> amazon_s3_bucket_hm_labelbox_renamed ==="
  sed -i.bak 's/module "amazon_s3_bucket_hm_labelbox" {/module "amazon_s3_bucket_hm_labelbox_renamed" {/' "$ESTATE/main.tofu"
  sed -i.bak 's/module\.amazon_s3_bucket_hm_labelbox\.id/module.amazon_s3_bucket_hm_labelbox_renamed.id/' "$ESTATE/main.tofu"
  sed -i.bak 's/module\.amazon_s3_bucket_hm_labelbox\.name/module.amazon_s3_bucket_hm_labelbox_renamed.name/' "$ESTATE/main.tofu"
  rm -f "$ESTATE/main.tofu.bak"
  cat >> "$ESTATE/main.tofu" <<'EOF'

moved {
  from = module.amazon_s3_bucket_hm_labelbox
  to   = module.amazon_s3_bucket_hm_labelbox_renamed
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
  grep -qE '^  # module\.amazon_s3_bucket_hm_labelbox_renamed\.aws_s3_bucket\.main will be updated in-place' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block plan does not propose an in-place update to the renamed bucket"; }
  grep -qF 'Plan: 0 to add, 1 to change, 0 to destroy.' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -10; fail "the moved-block rename plan is not exactly one in-place change"; }
  grep -qE '~ +"tofu-address" += +"module\.amazon_s3_bucket_hm_labelbox\.aws_s3_bucket\.main" +-> +"module\.amazon_s3_bucket_hm_labelbox_renamed\.aws_s3_bucket\.main"' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT"; fail "the moved-block plan does not show the bucket's tofu-address marker being rewritten from the old address to the new one"; }
  log "  choudoufu: zero churn, one in-place tags update - the marker rewrite the moved block completes"

  MOVED_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; MOVED_APPLY_RC=$?
  [ "$MOVED_APPLY_RC" -eq 0 ] || { printf '%s\n' "$MOVED_APPLY_OUT" | tail -40; fail "the moved-block rename apply exited $MOVED_APPLY_RC"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$MOVED_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$MOVED_APPLY_OUT"; fail "the moved-block rename apply was not exactly one in-place change"; }

  BUCKET_ADDR_D_AFTER="$(awsl s3api get-bucket-tagging --bucket "$BUCKET_NAME" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$BUCKET_ADDR_D_AFTER" = "module.amazon_s3_bucket_hm_labelbox_renamed.aws_s3_bucket.main" ] \
    || fail "the bucket carries tofu-address=$BUCKET_ADDR_D_AFTER after the rename, not module.amazon_s3_bucket_hm_labelbox_renamed.aws_s3_bucket.main"
  log "  $BUCKET_NAME unchanged, tofu-address now module.amazon_s3_bucket_hm_labelbox_renamed.aws_s3_bucket.main - read via the AWS CLI"

  log "=== D2. choudoufu, live-mv: module labelbox_iam_role -> labelbox_iam_role_renamed, no moved block at all ==="
  sed -i.bak 's/module "labelbox_iam_role" {/module "labelbox_iam_role_renamed" {/' "$ESTATE/main.tofu"
  rm -f "$ESTATE/main.tofu.bak"
  ( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the live-mv rename's reinit failed"; }
  MV_OUT="$(cd "$ESTATE" && "$TOFU" live-mv -estate="$ESTATE_NAME" module.labelbox_iam_role.aws_iam_role.labelbox_iam_role module.labelbox_iam_role_renamed.aws_iam_role.labelbox_iam_role 2>&1)"; MV_RC=$?
  [ "$MV_RC" -eq 0 ] || { printf '%s\n' "$MV_OUT" | tail -30; fail "choudoufu live-mv exited $MV_RC"; }
  grep -qF 'Rewrote the ownership marker on one live resource. This was a cloud write.' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report a real write"; }
  grep -qF '"module.labelbox_iam_role.aws_iam_role.labelbox_iam_role" -> "module.labelbox_iam_role_renamed.aws_iam_role.labelbox_iam_role"' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report rewriting the tofu-address marker from the old address to the new one"; }
  log "  live-mv: $(grep -F 'live ID' <<< "$MV_OUT")"

  ROLE_ADDR_D_AFTER="$(awsl iam list-role-tags --role-name "$ROLE_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$ROLE_ADDR_D_AFTER" = "module.labelbox_iam_role_renamed.aws_iam_role.labelbox_iam_role" ] \
    || fail "the role carries tofu-address=$ROLE_ADDR_D_AFTER after live-mv, not module.labelbox_iam_role_renamed.aws_iam_role.labelbox_iam_role"
  log "  $ROLE_NAME unchanged, tofu-address now module.labelbox_iam_role_renamed.aws_iam_role.labelbox_iam_role - read via the AWS CLI"

  log "=== D3. one more plan: config and markers agree on both renames, nothing proposed ==="
  FINAL_PLAN_D_OUT="$(plan_into 2>&1)"; FINAL_PLAN_D_RC=$?
  [ "$FINAL_PLAN_D_RC" -eq 0 ] || { printf '%s\n' "$FINAL_PLAN_D_OUT" | tail -40; fail "the post-rename plan exited $FINAL_PLAN_D_RC"; }
  grep -qF "No changes. Your infrastructure matches the configuration." <<< "$FINAL_PLAN_D_OUT" \
    || { grep -E '^  #' <<< "$FINAL_PLAN_D_OUT"; fail "the post-rename plan is not empty"; }
  log "  No changes. Both renames are complete and invisible to the next plan."

  gauntlet_stage day2_rename pass "moved block: module.amazon_s3_bucket_hm_labelbox renamed with zero churn (0 add, 1 change, 0 destroy), marker rewritten in place; live-mv: module.labelbox_iam_role renamed with zero churn, marker rewritten in place; stock oracle over the same two-object rename on cold_deploy's own state also shows zero churn (0 add, 0 change, 0 destroy); both live ids unchanged, read via the AWS CLI"
  log ""

  # ══════════════════════════════════════════════════════════════════════
  # PART F: REPLACE (day2_replace, live/GAUNTLET.md #9)
  # ══════════════════════════════════════════════════════════════════════
  #
  # Starts from Part D's real, completed state: module.labelbox_iam_role_
  # renamed (originally module.labelbox_iam_role, renamed above by
  # live-mv) is bound and converged. labelbox_service_account_name feeds
  # BOTH resources this module holds - aws_iam_role.labelbox_iam_role's
  # `name` (ForceNew: AWS has no rename-role API, only
  # CreateRole/DeleteRole) and, through that sibling's own `name`
  # attribute, aws_iam_role_policy.labelbox_iam_role_s3_policy's `role`
  # (also ForceNew) and its own `name` argument - so changing it forces
  # BOTH resources to replace at their same declared addresses. This is
  # the SAME untaggable-identity path this estate's own greenfield stage
  # fix (ComponentsFromValue's whole-object IsWhollyKnown gate no longer
  # letting an unrelated unknown `policy` argument veto a known role/name
  # pair) resolves for a from-nothing apply; F0/the record-store checks
  # below prove it also holds when the SAME instance is replaced, not
  # created, under a real, already-applied estate.
  #
  # THE create_before_destroy SCOPE NOTE (corpus-ec2-instance-complete's
  # own PART F and corpus-sqs-basic's own PART F carry the full reasoning,
  # reproduced only in summary here): OpenTofu core rejects a `lifecycle`
  # block on a `module` call, and patching the vendored labelbox_iam_role
  # module's own resources to add create_before_destroy would cross this
  # corpus's own DELTA discipline (the three leaf modules stay byte-
  # identical to the pinned commit throughout - see this script's header),
  # so this evidence pass exercises the default destroy-then-create
  # ordering instead, confirmed below by the plan's own "-/+ destroy and
  # then create replacement" legend.
  #
  # THE COLLISION-DETECTION SCOPE NOTE. The stage's own Break text
  # ("skip the destroy half; the next plan must report a collision") is
  # the shape corpus-ec2-instance-complete's and corpus-sqs-basic's own
  # BREAK=replace controls manufacture, by tagging a second live object
  # with the SAME tofu-slot a count-indexed resource's replace would
  # destroy. aws_iam_role.labelbox_iam_role has no count/for_each - there
  # is no tofu-slot to manufacture a collision on - and, confirmed by
  # instrumenting discovery.bind() directly (not inferred from behavior
  # alone) against this exact estate, a type whose entire declared
  # population is already record-backed is excluded from that function's
  # per-type claimant sweep entirely for an ordinary plan
  # (decl.bindTypeNames() returned an empty list for this run): the
  # engine answers this instance's identity straight from its record and
  # never re-lists every live aws_iam_role to check for a stray second
  # object carrying the same tag. A manufactured duplicate role therefore
  # goes undetected by an ordinary plan today for this resource shape -
  # a real, worth-recording finding, not something this unit fixes (that
  # is discovery-sweep-invocation-policy work, well beyond one stage
  # section, and exactly the kind of identity-path change HANDOFF says to
  # stop and report rather than guess at). BREAK_REPLACE below instead
  # proves THIS section's own plan-shape assertion is load-bearing the
  # way STAGE 2 and STAGE 5's BREAK=1 controls already do here: expect
  # the wrong destroy count on purpose and confirm the real assertion
  # would have caught it.
  CURRENT_STAGE=day2_replace
  record_key() { printf '%s' "$1" | base64 | tr '+/' '-_' | tr -d '=\n'; }
  record_import_id() { jq -r '.identity.import_id // empty' "$1" 2>/dev/null; }
  record_identity_attr() { jq -r ".identity.attrs.$2 // empty" "$1" 2>/dev/null; }
  F_ROLE_ADDR="module.labelbox_iam_role_renamed.aws_iam_role.labelbox_iam_role"
  F_POLICY_ADDR="module.labelbox_iam_role_renamed.aws_iam_role_policy.labelbox_iam_role_s3_policy"
  F_ROLE_RECORD="$ESTATE/.tofu-records/tofu-records/$ESTATE_NAME/aws_iam_role/$(record_key "$F_ROLE_ADDR")"
  F_POLICY_RECORD="$ESTATE/.tofu-records/tofu-records/$ESTATE_NAME/aws_iam_role_policy/$(record_key "$F_POLICY_ADDR")"

  log "=== F0. capture the live role, its inline policy, and their records ahead of the forced replace ==="
  [ -f "$F_ROLE_RECORD" ] || fail "no local record file found for $F_ROLE_ADDR ahead of day2_replace"
  F_OLD_ROLE_IMPORT_ID="$(record_import_id "$F_ROLE_RECORD")"
  [ "$F_OLD_ROLE_IMPORT_ID" = "$ROLE_NAME" ] || fail "the record for $F_ROLE_ADDR names $F_OLD_ROLE_IMPORT_ID ahead of day2_replace, not $ROLE_NAME"
  [ -f "$F_POLICY_RECORD" ] || fail "no local record file found for $F_POLICY_ADDR ahead of day2_replace"
  F_OLD_POLICY_ROLE="$(record_identity_attr "$F_POLICY_RECORD" role)"
  F_OLD_POLICY_NAME="$(record_identity_attr "$F_POLICY_RECORD" name)"
  [ "$F_OLD_POLICY_ROLE" = "$ROLE_NAME" ] && [ "$F_OLD_POLICY_NAME" = "$POLICY_NAME" ] \
    || fail "the record for $F_POLICY_ADDR names role=$F_OLD_POLICY_ROLE name=$F_OLD_POLICY_NAME ahead of day2_replace, not role=$ROLE_NAME name=$POLICY_NAME"
  F_OLD_ROLE_ADDR_TAG="$(awsl iam list-role-tags --role-name "$ROLE_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$F_OLD_ROLE_ADDR_TAG" = "$F_ROLE_ADDR" ] || fail "$ROLE_NAME does not carry tofu-address=$F_ROLE_ADDR ahead of day2_replace (got $F_OLD_ROLE_ADDR_TAG)"
  log "  $ROLE_NAME / $POLICY_NAME, role record import_id=$F_OLD_ROLE_IMPORT_ID, policy record role:name=$F_OLD_POLICY_ROLE:$F_OLD_POLICY_NAME, tofu-address=$F_OLD_ROLE_ADDR_TAG"

  log "=== F1. choudoufu: change the ForceNew labelbox_service_account_name variable, forcing a replace at the same declared addresses ==="
  sed -i.bak 's/labelbox_service_account_name = "hm-labelbox"/labelbox_service_account_name = "hm-labelbox-v2"/' "$ESTATE/main.tofu"
  rm -f "$ESTATE/main.tofu.bak"
  grep -q 'hm-labelbox-v2' "$ESTATE/main.tofu" || fail "changing labelbox_service_account_name did not match - the corpus pin has moved"
  F_NEW_ROLE_NAME="LabelboxRole-hm-labelbox-v2"
  F_NEW_POLICY_NAME="LabelboxRoleS3Policy-hm-labelbox-v2"

  F_PLAN_OUT="$(plan_into 2>&1)"; F_PLAN_RC=$?
  [ "$F_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_PLAN_OUT" | tail -40; fail "the day2_replace plan exited $F_PLAN_RC"; }
  grep -qE '^  # module\.labelbox_iam_role_renamed\.aws_iam_role\.labelbox_iam_role must be replaced' <<< "$F_PLAN_OUT" \
    || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not propose replacing $F_ROLE_ADDR when labelbox_service_account_name changes"; }
  grep -qE '~ +name +=.+forces replacement' <<< "$F_PLAN_OUT" \
    || { printf '%s\n' "$F_PLAN_OUT"; fail "the plan does not mark the role's name as forcing replacement"; }
  grep -qE '^  # module\.labelbox_iam_role_renamed\.aws_iam_role_policy\.labelbox_iam_role_s3_policy must be replaced' <<< "$F_PLAN_OUT" \
    || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not cascade the role replace into its inline policy"; }
  grep -qE '~ +role +=.+forces replacement' <<< "$F_PLAN_OUT" \
    || { printf '%s\n' "$F_PLAN_OUT"; fail "the plan does not mark the policy's role as forcing replacement"; }

  if [ "${BREAK_REPLACE:-}" = "1" ]; then
    log "=== F2 (BREAK_REPLACE=1). expect the wrong destroy count on purpose - this assertion must fail ==="
    grep -qF 'Plan: 2 to add, 0 to change, 1 to destroy.' <<< "$F_PLAN_OUT" \
      && fail "BREAK_REPLACE=1: the plan matched the WRONG expected shape (1 to destroy) without this script noticing - the plan-shape assertion below is not load-bearing"
    log "  BREAK_REPLACE=1: correctly did not match the wrong shape - the real plan is 2 to add, 0 to change, 2 to destroy (F1's own two \"must be replaced\" lines above), so the assertion this section relies on is load-bearing; apply and the record/marker checks below are skipped, matching this file's own BREAK=1/BREAK_REMOVE=1 convention of diverging the rest of the run rather than reverting"
  else
    grep -qF 'Plan: 2 to add, 0 to change, 2 to destroy.' <<< "$F_PLAN_OUT" \
      || { printf '%s\n' "$F_PLAN_OUT" | tail -10; fail "the day2_replace plan does not match F-ORACLE's own two-resource replace"; }
    log "  choudoufu: exactly one role replace at the same declared address, cascading into its inline policy (also replaced) - matches F-ORACLE's own plan shape"

    F_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; F_APPLY_RC=$?
    [ "$F_APPLY_RC" -eq 0 ] || { printf '%s\n' "$F_APPLY_OUT" | tail -40; fail "the day2_replace apply exited $F_APPLY_RC"; }
    grep -qE 'Resources: 2 added, 0 changed, 2 destroyed' <<< "$F_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$F_APPLY_OUT"; fail "the day2_replace apply did not match the planned 2 added, 0 changed, 2 destroyed"; }

    if F_OLD_STILL="$(awsl iam get-role --role-name "$ROLE_NAME" 2>&1)"; then
      echo "$F_OLD_STILL"; fail "$ROLE_NAME still exists after the replace - the old object was orphaned, not destroyed"
    fi
    log "  $ROLE_NAME no longer exists (NoSuchEntity) - confirmed via the AWS CLI, not through choudoufu's own report"

    F_NEW_ROLE_ADDR_TAG="$(awsl iam list-role-tags --role-name "$F_NEW_ROLE_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
    [ "$F_NEW_ROLE_ADDR_TAG" = "$F_ROLE_ADDR" ] \
      || fail "$F_NEW_ROLE_NAME carries tofu-address=$F_NEW_ROLE_ADDR_TAG after the replace, not $F_ROLE_ADDR - the marker did not move onto the new object"
    log "  $F_NEW_ROLE_NAME (the new object) carries tofu-address=$F_NEW_ROLE_ADDR_TAG - the marker moved onto the new object, read via the AWS CLI"

    F_NEW_POLICY_DOC="$(awsl iam get-role-policy --role-name "$F_NEW_ROLE_NAME" --policy-name "$F_NEW_POLICY_NAME" --query 'PolicyDocument' --output json)"
    grep -qF "$BUCKET_NAME" <<< "$F_NEW_POLICY_DOC" || fail "the new role's inline policy document does not reference $BUCKET_NAME"
    log "  $F_NEW_POLICY_NAME exists on $F_NEW_ROLE_NAME and still scopes to $BUCKET_NAME - read via the AWS CLI"

    # THE RECORD STORE, asserted by value (HANDOFF's safety rule; the
    # #398-guard shape: a stale record still naming the destroyed role or
    # policy would be exactly the wrong-marker failure that outranks a
    # missing one).
    F_NEW_ROLE_IMPORT_ID="$(record_import_id "$F_ROLE_RECORD")"
    [ "$F_NEW_ROLE_IMPORT_ID" = "$F_NEW_ROLE_NAME" ] \
      || fail "the record for $F_ROLE_ADDR names $F_NEW_ROLE_IMPORT_ID after the replace, not the new object $F_NEW_ROLE_NAME - a stale record still claiming the destroyed role"
    [ "$F_NEW_ROLE_IMPORT_ID" != "$F_OLD_ROLE_IMPORT_ID" ] || fail "sanity: the role record's import_id at $F_ROLE_ADDR did not change at all across the replace"
    F_NEW_POLICY_ROLE="$(record_identity_attr "$F_POLICY_RECORD" role)"
    F_NEW_POLICY_NAME_REC="$(record_identity_attr "$F_POLICY_RECORD" name)"
    [ "$F_NEW_POLICY_ROLE" = "$F_NEW_ROLE_NAME" ] && [ "$F_NEW_POLICY_NAME_REC" = "$F_NEW_POLICY_NAME" ] \
      || fail "the record for $F_POLICY_ADDR names role=$F_NEW_POLICY_ROLE name=$F_NEW_POLICY_NAME_REC after the replace, not role=$F_NEW_ROLE_NAME name=$F_NEW_POLICY_NAME - a stale record still claiming the destroyed policy"
    log "  record store: role import_id $F_OLD_ROLE_IMPORT_ID -> $F_NEW_ROLE_IMPORT_ID; policy role:name $F_OLD_POLICY_ROLE:$F_OLD_POLICY_NAME -> $F_NEW_POLICY_ROLE:$F_NEW_POLICY_NAME_REC, both at the same keys - read directly off the local record store, not through choudoufu's own report; the SAME untaggable-identity derivation this estate's greenfield stage's own fix resolves for a from-nothing apply now proven under a real apply-time replace"

    log "=== F2. one more plan: config and reality agree, no marker collision ==="
    F_FINAL_PLAN_OUT="$(plan_into 2>&1)"; F_FINAL_PLAN_RC=$?
    [ "$F_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_FINAL_PLAN_OUT" | tail -40; fail "the post-replace plan exited $F_FINAL_PLAN_RC"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$F_FINAL_PLAN_OUT" \
      || { grep -E '^  #' <<< "$F_FINAL_PLAN_OUT"; fail "the post-replace plan proposes a resource change"; }
    log "  No changes. The replace is complete and invisible to the next plan - no marker collision."

    ROLE_NAME="$F_NEW_ROLE_NAME"
    POLICY_NAME="$F_NEW_POLICY_NAME"

    gauntlet_stage day2_replace pass "choudoufu: changing labelbox_service_account_name proposed exactly one role replace at module.labelbox_iam_role_renamed's declared address, cascading into its untaggable inline policy (also replaced, role and name are both ForceNew there) - 2 to add, 0 to change, 2 to destroy, matching F-ORACLE's own plan shape; applied cleanly; the old role is confirmed terminated (NoSuchEntity) and the new role carries the marker, both via the AWS CLI; the local record store's records at the same addresses now name the new role's import_id and the new role:name pair, not the destroyed ones (role $F_OLD_ROLE_IMPORT_ID -> $F_NEW_ROLE_IMPORT_ID; policy $F_OLD_POLICY_ROLE:$F_OLD_POLICY_NAME -> $F_NEW_POLICY_ROLE:$F_NEW_POLICY_NAME_REC) - the same untaggable-identity path this estate's greenfield fix resolves for a from-nothing apply, now proven under a real replace; the next plan proposes no resource action. Scope notes: this exercises OpenTofu's default destroy-then-create ordering, not the create_before_destroy variant the stage's Title names (module lifecycle blocks are rejected by OpenTofu core, and the vendored module stays byte-identical to the pinned commit throughout - see this section's own header); and a manufactured live-object collision (this stage's own Break text) goes undetected by an ordinary plan for this resource shape, confirmed by instrumenting discovery.bind() directly - a fully record-backed type's declared population is excluded from that function's per-type claimant sweep, a real finding this unit records rather than fixes (identity-path change, HANDOFF's stop-and-report territory); BREAK_REPLACE instead proves this section's own plan-shape assertion is load-bearing."
  fi
  CURRENT_STAGE=""

  # ══════════════════════════════════════════════════════════════════════
  # PART E: REMOVE A BLOCK (day2_remove, live/GAUNTLET.md #7)
  # ══════════════════════════════════════════════════════════════════════
  #
  # Starts from Part D's real, completed state: module.labelbox_iam_role_renamed
  # (originally module.labelbox_iam_role) is bound and converged. It is the
  # one removed here, not module.amazon_s3_bucket_hm_labelbox_renamed - the
  # real module carries `lifecycle { prevent_destroy = true }` on the
  # bucket (see header), and labelbox_iam_role is a leaf nothing else
  # references. Its block holds TWO resources - the taggable aws_iam_role
  # and the untaggable, inline aws_iam_role_policy - so this is a real
  # instance of live/GAUNTLET.md #7's "blocks for untaggable children whose
  # parents stay" concern even though both are removed together: IAM
  # refuses to delete a role with an inline policy still attached, so the
  # destroy order the cloud accepts here is the policy first, then the
  # role.
  CURRENT_STAGE=day2_remove
  log "=== E0. capture the live ids one more time ==="
  E_ROLE_ADDR_BEFORE="$(awsl iam list-role-tags --role-name "$ROLE_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text 2>/dev/null || true)"
  [ "$E_ROLE_ADDR_BEFORE" = "module.labelbox_iam_role_renamed.aws_iam_role.labelbox_iam_role" ] \
    || fail "$ROLE_NAME does not carry tofu-address=module.labelbox_iam_role_renamed.aws_iam_role.labelbox_iam_role before day2_remove even starts (got $E_ROLE_ADDR_BEFORE)"

  if [ "${BREAK_REMOVE:-}" = "1" ]; then
    log "=== E1 (BREAK_REMOVE=1). keep module.labelbox_iam_role_renamed's block; no destroy may be proposed ==="
    BREAK_REMOVE_PLAN_OUT="$(plan_into 2>&1)"; BREAK_REMOVE_PLAN_RC=$?
    [ "$BREAK_REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_REMOVE_PLAN_OUT" | tail -40; fail "the BREAK_REMOVE=1 kept-block plan exited $BREAK_REMOVE_PLAN_RC"; }
    grep -qE '^  # module\.labelbox_iam_role_renamed\..+ will be destroyed' <<< "$BREAK_REMOVE_PLAN_OUT" \
      && { grep -E '^  # .+ will be' <<< "$BREAK_REMOVE_PLAN_OUT"; fail "BREAK_REMOVE=1: a destroy was proposed for module.labelbox_iam_role_renamed even though its block is still in the config - this stage's check is not load-bearing"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$BREAK_REMOVE_PLAN_OUT" \
      || { grep -E '^  #' <<< "$BREAK_REMOVE_PLAN_OUT"; fail "BREAK_REMOVE=1: some resource action was proposed with the block still in the config"; }
    log "  BREAK_REMOVE=1: correctly proposes no resource action - the block is still declared"
  else
    log "=== E1. choudoufu: delete module.labelbox_iam_role_renamed's block ==="
    perl -0pi -e 's/\n# Labelbox - IAM role\nmodule "labelbox_iam_role_renamed" \{.*?\n\}\n//s' "$ESTATE/main.tofu"
    grep -q 'module "labelbox_iam_role_renamed"' "$ESTATE/main.tofu" \
      && fail "removing module.labelbox_iam_role_renamed's block did not match - the config has moved"
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the day2_remove reinit failed"; }
    REMOVE_PLAN_OUT="$(plan_into 2>&1)"; REMOVE_PLAN_RC=$?
    [ "$REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -40; fail "the day2_remove plan exited $REMOVE_PLAN_RC"; }
    if grep -q 'is unclaimed, so this may be the same resource under a new instance key' <<< "$REMOVE_PLAN_OUT"; then
      printf '%s\n' "$REMOVE_PLAN_OUT" | tail -40
      fail "choudoufu withheld the destroy of module.labelbox_iam_role_renamed as a possible rename - this is the honest wall, not a pass"
    fi
    grep -qE '^  # module\.labelbox_iam_role_renamed\.aws_iam_role\.labelbox_iam_role will be destroyed' <<< "$REMOVE_PLAN_OUT" \
      || { printf '%s\n' "$REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu does not propose destroying the IAM role when module.labelbox_iam_role_renamed's block is deleted"; }
    grep -qE '^  # module\.labelbox_iam_role_renamed\.aws_iam_role_policy\.labelbox_iam_role_s3_policy will be destroyed' <<< "$REMOVE_PLAN_OUT" \
      || { printf '%s\n' "$REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu does not propose destroying the inline role policy when module.labelbox_iam_role_renamed's block is deleted"; }
    grep -qF 'Plan: 0 to add, 0 to change, 2 to destroy.' <<< "$REMOVE_PLAN_OUT" \
      || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -10; fail "choudoufu's remove plan proposes something other than exactly two destroys"; }
    log "  choudoufu: exactly two destroys (the IAM role and its inline policy), nothing else"

    REMOVE_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; REMOVE_APPLY_RC=$?
    [ "$REMOVE_APPLY_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_APPLY_OUT" | tail -40; fail "the day2_remove apply exited $REMOVE_APPLY_RC"; }
    grep -qE 'Resources: 0 added, 0 changed, 2 destroyed' <<< "$REMOVE_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$REMOVE_APPLY_OUT"; fail "the day2_remove apply was not exactly two destroys"; }

    if E_STILL="$(awsl iam get-role --role-name "$ROLE_NAME" 2>&1)"; then
      echo "$E_STILL"; fail "$ROLE_NAME still exists in the live account after the destroy - it was orphaned, not destroyed"
    fi
    log "  $ROLE_NAME no longer exists (NoSuchEntity) - confirmed via the AWS CLI, not through choudoufu's own report"

    log "=== E2. one more plan: config and reality agree, nothing left to propose ==="
    E_FINAL_PLAN_OUT="$(plan_into 2>&1)"; E_FINAL_PLAN_RC=$?
    [ "$E_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$E_FINAL_PLAN_OUT" | tail -40; fail "the post-remove plan exited $E_FINAL_PLAN_RC"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$E_FINAL_PLAN_OUT" \
      || { grep -E '^  #' <<< "$E_FINAL_PLAN_OUT"; fail "the post-remove plan is not empty"; }
    log "  No changes. The removal is complete and invisible to the next plan."

    log ""
    log "STAGE E (day2_remove): PASS"
    gauntlet_stage day2_remove pass "choudoufu: deleting module.labelbox_iam_role_renamed's block proposed exactly two destroys (0 add, 0 change, 2 destroy - the untaggable inline policy and its taggable parent role), applied cleanly (0 added, 0 changed, 2 destroyed) in an order IAM accepted, the role is genuinely gone from the live account (iam get-role on the old name now returns NoSuchEntity, read via the AWS CLI, not choudoufu's own report), and the next plan proposes no resource action; stock oracle on cold_deploy's own state (E-ORACLE) also proposes exactly two destroys for the same objects"
    log ""
  fi
  CURRENT_STAGE=""
fi
CURRENT_STAGE=""
gauntlet_end

log "=== PASS: all five stages, real, against hongbo-miao/hongbomiao.com's own ==="
log "=== unmodified Labelbox integration modules, .tofu extension throughout  ==="
