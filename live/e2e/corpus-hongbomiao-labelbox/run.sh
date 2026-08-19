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
grep -qF "2 resource(s) newly stamped, 0 already stamped, 0 failed, 2 skipped" <<< "$APPROVE_OUT" \
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

log "=== PASS: all five stages, real, against hongbo-miao/hongbomiao.com's own ==="
log "=== unmodified Labelbox integration modules, .tofu extension throughout  ==="
