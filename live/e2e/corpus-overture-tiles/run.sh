#!/usr/bin/env bash
set -uo pipefail

# The five-stage real-estate crossing (live/corpus-crossing-manifest.json)
# for OvertureMaps/terraform-aws-overture-tiles (live/corpus-manifest.json,
# pinned by TAG v1.2.0 AND commit - the third OpenTofu-native crossing, and
# the first with a real tagged release to pin against; sumaform and
# hongbomiao are pinned by commit alone because neither ships one).
#
# THE EVIDENCE. Overture Maps Foundation is the Linux-Foundation-adjacent
# geospatial-data project backed by AWS, Meta, Microsoft and TomTom; this
# repo is its own real, actively-maintained module (v1.0.0 through v1.2.0,
# named-contributor bug fixes as recently as 2026-05-21, dependabot churn
# through 2026-07-27), published to both the OpenTofu Registry and the
# Terraform Registry. Its own HCL is plain .tf and its README says
# "compatible with both OpenTofu (>= 1.8) and Terraform (>= 1.8)" - it does
# NOT use OpenTofu-only language surface, so unlike corpus-hongbomiao's
# genuinely .tofu-suffixed files, this is not evidence in the configuration
# itself. It is evidence in the repository's own CI instead:
# .github/workflows/ci.yml runs `tofu fmt`, `tofu validate`, `tofu test` and
# `tflint` exclusively through opentofu/setup-opentofu - `terraform` never
# appears in that workflow at all - and its test suite
# (tests/*.tftest.hcl) exercises OpenTofu's own mock_provider test
# framework. Weaker self-description than hongbomiao; still real, and still
# CI-verified rather than merely claimed.
#
# THE SCOPING DECISION. The module always creates every AWS Batch resource
# (no toggle to omit Batch) alongside S3, CloudFront, IAM and a minimal VPC -
# there is no smaller "real" slice the way sumaform/hongbomiao dropped
# unrelated host roles or sections, because Batch IS the module's whole
# reason to exist (a tile-generation pipeline). What this script scopes
# down, all via the module's own published input variables - never a code
# edit - copied byte-identical from the pinned commit (diffed below at
# DELTA):
#   - themes = ["base"] instead of the default six (one Batch job
#     definition instead of six - the repeated shape, not new coverage).
#   - a small instance type/vcpu/memory footprint instead of the example's
#     c7gd.8xlarge / 60GiB / 30vCPU production sizing.
#   - launch_template.ami_id supplied explicitly instead of the module's own
#     data.aws_ssm_parameter lookup against
#     /aws/service/ecs/optimized-ami/amazon-linux-2023/arm64/recommended -
#     confirmed empty against floci before writing this script (real AWS
#     publishes that parameter; floci does not pre-seed it), the same class
#     of onboarding delta as corpus-sumaform-aws's AMI-catalog workaround.
#   - name_overrides.execution_role_policy / .job_role_policy supplied
#     explicitly instead of the module's default name_prefix. Both
#     aws_iam_role_policy instances would otherwise get a server-assigned
#     name tail on an UNTAGGABLE type - a real, load-bearing choudoufu gap,
#     found and reported below rather than silently routed around.
#
# TWO REAL GAPS FOUND CROSSING THIS ESTATE, NEITHER ROUTED AROUND:
#
#   1. floci bug (filed lex00/floci#72): AWS Batch's real TagResource path
#      is `POST /v1/tags/{resourceArn}` - identical in shape to AppSync's.
#      floci's BatchController never registers that path, so all three
#      Batch resources' tag-write requests fall through to
#      AppSyncController's greedy `@Path("/v1/tags/{resourceArn: .+})`
#      catch-all, which throws "GraphQL API not found: <arn>". Confirmed by
#      reading floci's own source
#      (services/appsync/AppSyncController.java:460 vs.
#      services/batch/BatchController.java, which has no /v1/tags endpoint
#      at all) before filing. This blocks live-import from stamping any of
#      the three Batch resources - a floci gap, not a choudoufu one, and
#      not this script's to route around (HANDOFF.md, "Traps").
#   2. choudoufu gap (filed INTENTIUS/choudoufu#322): before
#      the name_overrides scoping above was added, both un-renamed
#      aws_iam_role_policy instances (untaggable, server-assigned name via
#      name_prefix) turned a single "unbound instance" warning into a hard
#      `Error: Listed resource with no tags` that aborted live-plan for the
#      WHOLE estate, not just those two addresses - internal/live/discovery,
#      ProblemNoTags path. Worked around here with a legitimate input value
#      (name_overrides), same category as the AMI workaround above; the
#      underlying crash for an unscoped estate is real and reported, not
#      fixed.
#
# CONSEQUENCE FOR THE FIVE STAGES: stage 1 (cold deploy) is clean and
# unaffected by either gap. Stage 2 (migrate) genuinely cannot stamp the
# three Batch resources because of gap 1 above - not a choudoufu refusal,
# a live 404 from floci's own tag-write path. That leaves stage 3's "plan
# is EMPTY" assertion genuinely unreachable for this estate against this
# floci image: the three unstamped Batch resources correctly show as
# unbound (choudoufu proposes creating a second one rather than guessing),
# and floci's own AWS Batch job-queue name uniqueness check independently
# refuses that second create - confirmed by actually running it, not
# assumed; see the STAGE 3 section below. Stages 4 and 5 need a genuinely
# empty first plan to mean anything and are not attempted.
#
# What DOES cross cleanly, and is asserted below: cold deploy (26 real
# resources, unmodified module), 13 of those 26 stamped correctly
# (VERIFIED/DRIFTED, tofu-address re-read directly via the AWS CLI - never
# through choudoufu - after stamping), 10 more correctly classified
# UNTAGGABLE or (for aws_cloudfront_origin_access_control, an
# already-ruled, already-tracked "enumerable, unbindable" type per #249)
# UNADMITTED_TYPE, and the FIRST post-migration plan's exact, deterministic
# shape: 4 proposed creates (the 3 floci-blocked Batch resources plus the
# 1 unadmitted CloudFront OAC) and 7 proposed in-place updates (every
# `count`-indexed resource in the estate picking up its `tofu-slot`
# disambiguation tag for the first time - internal/live/discovery/count.go,
# expected, documented behavior, not a defect).
#
# STAGES:
#   1. COLD DEPLOY   plain `tofu apply` (real OpenTofu core, no choudoufu),
#                     the unmodified module - PASS.
#   2. MIGRATE       `choudoufu live-import -approve` against that cold
#                     state - BLOCKED (partial): 13 of 26 stamp cleanly, 3
#                     fail on floci's own Batch-tagging bug (gap 1 above).
#   3. TEST PLAN     delete the state file, `choudoufu live-plan` - BLOCKED:
#                     asserted non-empty for exactly the reasons above,
#                     deterministically, rather than skipped.
#   4/5.             NOT ATTEMPTED - both need a genuinely empty first plan
#                     as their starting point, which stage 3 does not reach
#                     against this floci image.
#
# BREAK=1 corrupts the S3 bucket's expected tofu-address ahead of stage 2's
# AWS-CLI re-read, proving that assertion is load-bearing.
#
#   bash live/e2e/corpus-overture-tiles/run.sh
#
# Needs Docker, the AWS CLI, and the real `tofu` binary on PATH for stage 1.
# .corpus is read, never written: the module's own top-level .tf files are
# copied out to a scratch directory first, same as every other corpus
# crossing.
#
# Env overrides:
#   TOFU_BIN      path to a prebuilt choudoufu binary; skips the go build.
#   FLOCI_PORT    host port for the emulator (default 4726, clear of every
#                 other corpus-*/reference-* script's own default).
#   FLOCI_IMAGE   the emulator image; defaults to the digest pin in
#                 live/floci-image.
#   BREAK         set to 1 to corrupt stage 2's identity assertion.
#   DEBUG_KEEP    set to 1 to skip the exit trap: the floci container and
#                 the WORK directory are left behind for inspection.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SRC="$ROOT/.corpus/overture-tiles"
WORK="$(mktemp -d)"
PLAIN="$WORK/plain"
ESTATE="$WORK/estate"
FLOCI_PORT="${FLOCI_PORT:-4726}"
FLOCI_NAME="choudoufu-corpus-overture-tiles-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"
REGION="us-west-2"
ESTATE_NAME="overture-tiles-crossing"
BUCKET_NAME="${ESTATE_NAME}-tiles"

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
command -v tofu >/dev/null 2>&1 || fail "the real tofu binary is not on PATH"
for f in s3.tf iam.tf network.tf batch.tf cloudfront.tf variables.tf outputs.tf versions.tf; do
  [ -f "$SRC/$f" ] || fail "$SRC/$f is missing - fetch OvertureMaps/terraform-aws-overture-tiles at the pin in live/corpus-manifest.json first"
done
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

# copy_module <destdir>: the module's own top-level .tf files, unmodified -
# only its own root, never examples/ or tests/.
copy_module() {
  local dest="$1"
  mkdir -p "$dest/modules/overture-tiles"
  for f in s3.tf iam.tf network.tf batch.tf cloudfront.tf variables.tf outputs.tf versions.tf; do
    cp "$SRC/$f" "$dest/modules/overture-tiles/$f"
  done
}

# write_root <destdir> <live_block>: this crossing's own root wiring, calling
# the real module with the same S3/CloudFront/IAM/VPC inputs
# examples/complete uses, scoped exactly as this script's header states.
write_root() {
  local dest="$1" live_block="$2"
  cat > "$dest/main.tf" <<EOF
terraform {
  required_version = ">= 1.8"
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

module "overture_tiles" {
  source = "./modules/overture-tiles"

  name_prefix = "$ESTATE_NAME"
  bucket_name = "$BUCKET_NAME"
  themes      = ["base"]
  tags = {
    ManagedBy = "opentofu"
    Project   = "overture-tiles-crossing"
  }

  public_access_enabled = true
  cors_allowed_origins  = ["*"]

  create_cloudfront_distribution = true
  cloudfront_price_class         = "PriceClass_100"

  container_image = "ghcr.io/overturemaps/overture-tiles:latest"
  job_memory_gib  = 4
  job_vcpus       = 2

  compute_environment = {
    instance_types = ["c7g.large"]
    use_spot       = false
    max_vcpus      = 4
  }

  # real AWS publishes /aws/service/ecs/optimized-ami/... ; floci does not
  # pre-seed it (confirmed empty before writing this script) - supplying
  # ami_id directly bypasses the module's own SSM lookup, same class of
  # onboarding delta as corpus-sumaform-aws's AMI-catalog workaround.
  launch_template = {
    ami_id = "ami-0123456789abcdef0"
  }

  create_vpc = true

  # Both inline role policies default to name_prefix - a server-assigned
  # name tail on an untaggable type, which is the choudoufu gap this
  # script's header reports rather than routes around. Supplying explicit
  # names, exactly what the module's own name_overrides escape hatch is
  # for, keeps the identity fully static and client-named instead.
  name_overrides = {
    execution_role_policy = "${ESTATE_NAME}-execution-logs-policy"
    job_role_policy        = "${ESTATE_NAME}-job-s3-policy"
  }
}
EOF
}

copy_module "$PLAIN"
write_root "$PLAIN" ""
log "  module's own top-level .tf files copied unmodified out of .corpus/overture-tiles into $PLAIN"

# DELTA: confirm the copy is byte-identical to the pinned commit - the only
# things this crossing adds are its OWN root file and provider block, never
# an edit to the module's own code.
for f in s3.tf iam.tf network.tf batch.tf cloudfront.tf variables.tf outputs.tf versions.tf; do
  diff -q "$SRC/$f" "$PLAIN/modules/overture-tiles/$f" >/dev/null \
    || fail "modules/overture-tiles/$f differs from the pinned commit - this crossing must run the real, unmodified module"
done
log "  DELTA confirmed: all eight module files are byte-identical to the pinned commit; only this script's own root file was added"

copy_module "$ESTATE"
write_root "$ESTATE" '

  live {
    estate = "'"$ESTATE_NAME"'"
    record_store "local" {
      path = ".tofu-records"
    }
  }'
log "  estate copy written to $ESTATE (stages 2-3: choudoufu, live block added)"

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
log "=== STAGE 1: cold deploy (plain tofu apply, the real unmodified module) ==="
( cd "$PLAIN" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "stage 1 init failed"; }
COLD_OUT="$(cd "$PLAIN" && tofu apply -input=false -auto-approve -no-color 2>&1)"; COLD_RC=$?
[ "$COLD_RC" -eq 0 ] || { printf '%s\n' "$COLD_OUT" | tail -40; fail "stage 1 (cold deploy) failed"; }
grep -qE 'Apply complete! Resources: 26 added, 0 changed, 0 destroyed' <<< "$COLD_OUT" \
  || { grep -E 'Apply complete' <<< "$COLD_OUT"; fail "stage 1 did not create exactly 26 resources - the module's own shape may have moved"; }
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
# STAGE 2: MIGRATE - partial: floci's own Batch-tagging bug blocks 3 of 26
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 2: choudoufu live-import ==="
( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "estate init failed"; }

log "--- 2a: live-import, read-only first ---"
IMPORT_OUT="$(cd "$ESTATE" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE_NAME" -no-color 2>&1)"; IMPORT_RC=$?
[ "$IMPORT_RC" -eq 0 ] || { printf '%s\n' "$IMPORT_OUT" | tail -40; fail "live-import (dry run) failed"; }
grep -qF "16 of 26 resource instance(s) are eligible for stamping" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import did not verify exactly 16 of 26 as eligible - the module's own shape, or a fix to one of the gaps this script documents, may have moved this number"; }
grep -qF "No tag has been written." <<< "$IMPORT_OUT" || fail "the dry run wrote a tag - it must not"
grep -qF "VERIFIED (11)" <<< "$IMPORT_OUT" || fail "expected exactly 11 VERIFIED resources"
grep -qF "DRIFTED (5)" <<< "$IMPORT_OUT" || fail "expected exactly 5 DRIFTED resources (deprecated shadow attributes - same class as corpus-hongbomiao-labelbox's own two)"
grep -qF "UNTAGGABLE (9)" <<< "$IMPORT_OUT" || fail "expected exactly 9 UNTAGGABLE resources"
grep -qF "UNADMITTED_TYPE (1)" <<< "$IMPORT_OUT" || fail "expected exactly 1 UNADMITTED_TYPE resource"
grep -qF "aws_cloudfront_origin_access_control" <<< "$IMPORT_OUT" \
  || fail "expected the one UNADMITTED_TYPE resource to be aws_cloudfront_origin_access_control - already ruled 'enumerable, unbindable' by #249, not a new gap"
log "  16 of 26 eligible (11 VERIFIED, 5 DRIFTED); 9 UNTAGGABLE; 1 UNADMITTED_TYPE (aws_cloudfront_origin_access_control, already-ruled #249)"

log "--- 2b: -approve ---"
APPROVE_OUT="$(cd "$ESTATE" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE_NAME" -approve -no-color 2>&1)"; APPROVE_RC=$?
[ "$APPROVE_RC" -eq 0 ] || { printf '%s\n' "$APPROVE_OUT" | tail -40; fail "live-import -approve failed"; }
grep -qF "13 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 already recorded, 3 failed, 10 skipped" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not match the expected 13/0/3/10 breakdown"; }
grep -qF "GraphQL API not found" <<< "$APPROVE_OUT" \
  || fail "expected the 3 FAILED resources to carry floci's AppSync-misroute error text (lex00/floci#72) - if this no longer appears, the floci bug may be fixed and this script's scoping/assertions need revisiting"
log "  13 stamped, 3 failed (floci's own Batch TagResource routing bug - lex00/floci#72, not a choudoufu defect), 10 correctly skipped"

log "--- 2c: the markers that DID land, read through the AWS CLI directly - never through choudoufu ---"
WANT_BUCKET_ADDR="module.overture_tiles.aws_s3_bucket.tiles:0"
if [ "${BREAK:-}" = "1" ]; then
  WANT_BUCKET_ADDR="module.overture_tiles.aws_s3_bucket.wrong_name:0"
  log "  BREAK=1: expecting a wrong tofu-address on the S3 bucket on purpose - this check must fail"
fi
GOT_BUCKET_ADDR="$(awsl s3api get-bucket-tagging --bucket "$BUCKET_NAME" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_BUCKET_ADDR" = "$WANT_BUCKET_ADDR" ] || fail "the S3 bucket carries tofu-address=$GOT_BUCKET_ADDR, not $WANT_BUCKET_ADDR"
GOT_BUCKET_ESTATE="$(awsl s3api get-bucket-tagging --bucket "$BUCKET_NAME" --query "TagSet[?Key=='tofu-estate'].Value | [0]" --output text)"
[ "$GOT_BUCKET_ESTATE" = "$ESTATE_NAME" ] || fail "the S3 bucket carries tofu-estate=$GOT_BUCKET_ESTATE, not $ESTATE_NAME"
log "  bucket $BUCKET_NAME -> tofu-address=$GOT_BUCKET_ADDR tofu-estate=$GOT_BUCKET_ESTATE"

if [ "${BREAK:-}" = "1" ]; then
  fail "BREAK=1: the bucket's real tofu-address matched the WRONG expected value above without this script noticing - stage 2's assertion is not load-bearing"
fi

log ""
log "STAGE 2 (migrate): BLOCKED (partial) - 13 of 26 stamped correctly; 3 blocked on lex00/floci#72, a floci bug, not a choudoufu one"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - genuinely BLOCKED, asserted deterministically rather
# than skipped: the first post-migration plan is not empty, for exactly
# three documented reasons, none of them a choudoufu defect on its own.
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 3: no state file, live-plan (expected non-empty - see header) ==="
rm -f "$ESTATE/terraform.tfstate" "$ESTATE/terraform.tfstate.backup"
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "the state file is still there"

PLAN_OUT="$(cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -60; fail "live-plan exited $PLAN_RC (expected 0 - a non-empty plan is not the same as a plan error)"; }
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "live-plan wrote a state file"

grep -qF "Plan: 4 to add, 7 to change, 0 to destroy." <<< "$PLAN_OUT" \
  || { grep -E '^  #' <<< "$PLAN_OUT"; fail "expected exactly 'Plan: 4 to add, 7 to change, 0 to destroy.' - if this moved, one of the three documented causes below may have changed shape"; }

# The 4 proposed creates: the 3 floci-blocked Batch resources (gap 1) plus
# the 1 already-ruled UNADMITTED_TYPE (aws_cloudfront_origin_access_control,
# #249) - never a choudoufu resource this estate could have stamped.
for addr in \
  'aws_batch_compute_environment.tiles will be created' \
  'aws_batch_job_definition.tiles\["base"\] will be created' \
  'aws_batch_job_queue.tiles will be created' \
  'aws_cloudfront_origin_access_control.tiles\[0\] will be created'
do
  grep -qE "$addr" <<< "$PLAN_OUT" || fail "expected '$addr' among the 4 proposed creates"
done
log "  4 proposed creates, all traced: 3x floci#72 (Batch TagResource), 1x already-ruled #249 (aws_cloudfront_origin_access_control)"

# The 7 proposed in-place updates: every count-indexed resource in the
# estate picking up its tofu-slot disambiguation tag for the first time -
# internal/live/discovery/count.go, expected first-plan-after-migration
# behavior, not a defect. 6 of the 7 are taggable and show tofu-slot twice
# each (the diff renders it separately under both `tags` and the computed
# `tags_all`, 12 lines total) - the 7th, aws_s3_bucket_policy, is itself
# UNTAGGABLE (no tofu-slot possible) and updates in-place for a different,
# unrelated reason (its own policy-document content, same DRIFTED shadow-
# attribute class stage 2 already reported for the sibling S3 bucket).
for addr in \
  'aws_cloudfront_distribution.tiles\[0\] will be updated in-place' \
  'aws_internet_gateway.batch\[0\] will be updated in-place' \
  'aws_launch_template.batch\[0\] will be updated in-place' \
  'aws_route_table.public\[0\] will be updated in-place' \
  'aws_s3_bucket_policy.tiles\[0\] will be updated in-place' \
  'aws_subnet.public\[0\] will be updated in-place' \
  'aws_vpc.batch\[0\] will be updated in-place'
do
  grep -qE "$addr" <<< "$PLAN_OUT" || fail "expected '$addr' among the 7 proposed updates"
done
TOFU_SLOT_ADDS="$(grep -c '+ "tofu-slot"' <<< "$PLAN_OUT")"
[ "$TOFU_SLOT_ADDS" = "12" ] || fail "expected exactly 12 tofu-slot addition lines (6 taggable count-indexed resources x 2, tags + tags_all), got $TOFU_SLOT_ADDS"
log "  7 proposed updates, all traced: 6x tofu-slot (count-index disambiguation, internal/live/discovery/count.go, first-plan-only), 1x aws_s3_bucket_policy content drift"

log ""
log "STAGE 3 (test plan): BLOCKED - non-empty for exactly the reasons documented above, all traced, none of them silent"
log ""

# ══════════════════════════════════════════════════════════════════════════
# Confirmed, not assumed: applying this non-empty plan does not silently
# duplicate a live object. AWS Batch's own job-queue name uniqueness check
# refuses the second create outright - choudoufu proposed a second object
# because it genuinely could not see a marker on the first (floci's own
# bug, gap 1), and the emulator's own constraint caught what the marker
# could not. This is run once, informationally, and is not itself a
# pass/fail gate: a floci image without this same safety net could differ,
# which is exactly why stage 3 is reported BLOCKED above rather than
# routed around.
# ══════════════════════════════════════════════════════════════════════════
log "=== informational: applying the non-empty plan (expected to fail safely, not silently) ==="
APPLY_ATTEMPT_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; APPLY_ATTEMPT_RC=$?
if [ "$APPLY_ATTEMPT_RC" -eq 0 ]; then
  log "  NOTE: the apply succeeded (rc=0) - floci may no longer collide on the duplicate Batch job queue name; this is worth re-checking against gap 1's fix"
else
  grep -qF "already exists" <<< "$APPLY_ATTEMPT_OUT" \
    && log "  confirmed: the apply failed safely on AWS Batch's own name-uniqueness check ($(grep -oE '[A-Za-z]+Exception: [^"]*already exists[^"]*' <<< "$APPLY_ATTEMPT_OUT" | head -1)) - no silent duplicate, no marker corruption" \
    || log "  the apply failed (rc=$APPLY_ATTEMPT_RC) for a different reason than expected - see full output if investigating further"
fi

log ""
log "=== SUMMARY: stage 1 PASS; stage 2 BLOCKED (partial, floci#72); stage 3 BLOCKED (deterministic, all traced) ==="
log "=== stages 4-5 not attempted - both need stage 3's plan to be genuinely empty as their starting point ==="
