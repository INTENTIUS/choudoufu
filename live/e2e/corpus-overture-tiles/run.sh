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
# THE GAPS THIS CROSSING HAS FOUND, IN THE ORDER IT REACHED THEM. The first
# two are fixed; the third is where it stands today, and it was only
# reachable once the first was.
#
#   1. FIXED - floci bug (lex00/floci#72). AWS Batch's real TagResource path
#      is `POST /v1/tags/{resourceArn}`, identical in shape to AppSync's.
#      floci's BatchController never registered it, so all three Batch
#      resources' tag writes fell through to AppSyncController's greedy
#      `@Path("/v1/tags/{resourceArn: .+}")` catch-all and came back 404
#      "GraphQL API not found". Stage 2 stamped 13 of 26 with 3 FAILED.
#      Fixed at floci 1d469fff: a shared /v1/tags dispatcher over the
#      TagHandler registry floci already had for /tags, keyed on the ARN's
#      own service segment and on the path prefix, plus a BatchTagHandler.
#      Stage 2 now stamps 16 with 0 failed, asserted below.
#
#   2. FIXED - choudoufu gap (INTENTIUS/choudoufu#322, item 1), at
#      576990a599/b75e46c24e. `aws_iam_role_policy` - untaggable, with a
#      server-assigned `name` when `name_prefix` is used - escalated a
#      single-address unbound warning into a hard "Error: Listed resource
#      with no tags" that aborted the WHOLE live-plan. ProblemNoTags is now
#      gated on the same markerCapable signal other paths use, so the blast
#      radius is the one resource. The `name_overrides` scoping below stays
#      anyway: it is what keeps those two identities static and client-named,
#      which is a different question from the abort.
#
#   3. OPEN (INTENTIUS/choudoufu#345), and newly reached - the provider will
#      not validate a marker-derived identity ARN under this estate's own
#      provider block.
#      Now that the three Batch resources carry markers, projection tries to
#      import one by its ARN identity, and hashicorp/aws refuses:
#
#        Error: Invalid Identity Attribute Value
#        Identity attribute "arn" contains an Account ID "000000000000"
#        which does not match the provider's ""
#        Value: "arn:aws:batch:us-west-2:000000000000:job-queue/..."
#
#        Error: Cannot import for projection
#
#      The provider compares an identity ARN's account segment against the
#      account it knows about itself, and `skip_requesting_account_id = true`
#      - which every crossing script here sets, and which is the ordinary
#      way to point the AWS provider at a local emulator - leaves it knowing
#      none. live-plan exits 1 and produces no plan at all, so stages 4 and 5
#      have nothing to start from. The ARN is not wrong: stage 3 re-reads the
#      job queue's real ARN through the AWS CLI and asserts the refusal names
#      that exact string.
#
# MEASURED, NOT ASSUMED: the obvious fix for gap 3 is worse. Setting
# `skip_requesting_account_id = false` on the estate copy only (cold deploy
# untouched) was run for real against floci 2026-08-20. It does clear the
# Batch identity error - and it breaks stage 2 instead, because once the
# provider knows its account it routes S3 bucket tag reads through S3
# Control's account-prefixed virtual host:
#
#   Get "http://000000000000.127.0.0.1:4726/v20180820/tags/arn%3Aaws%3As3..."
#   dial tcp: lookup 000000000000.127.0.0.1: no such host
#
# `aws_s3_bucket.tiles[0]` goes from VERIFIED to MISSING and the estate drops
# to 15 of 26 eligible. An account-prefixed host cannot be made to resolve to
# a local port without a wildcard DNS domain, which floci does not serve. So
# the flag is left true and gap 3 is reported rather than routed around; do
# not re-try that flag without reading this paragraph.
#
# CONSEQUENCE FOR THE FIVE STAGES: stage 1 (cold deploy) is clean. Stage 2
# (migrate) now passes outright. Stage 3 is BLOCKED on gap 3 above - not a
# non-empty plan, no plan at all - and stages 4 and 5 need one to mean
# anything, so they are not attempted.
#
# What DOES cross cleanly, and is asserted below: cold deploy (26 real
# resources, unmodified module), then 16 of those 26 stamped correctly, 0
# failed, with three markers re-read directly via the AWS CLI - never through
# choudoufu - after stamping: the S3 bucket's tofu-address, and the Batch job
# queue's tofu-address and tofu-estate, the resource gap 1 blocked entirely.
# The job queue's own create-time `Project` tag is asserted to have survived
# the stamp, because a tag write that replaces instead of merging is how a
# live object silently loses either its own tags or its markers. The
# remaining 10 are correctly UNTAGGABLE (9) or the already-ruled
# "enumerable, unbindable" aws_cloudfront_origin_access_control (1, #249).
#
# STAGES:
#   1. COLD DEPLOY   plain `tofu apply` (real OpenTofu core, no choudoufu),
#                     the unmodified module - PASS.
#   2. MIGRATE       `choudoufu live-import -approve` against that cold
#                     state - PASS: 16 stamped, 0 failed, 10 skipped.
#   3. TEST PLAN     delete the state file, `choudoufu live-plan` - BLOCKED:
#                     refuses at exactly 2 diagnostics (gap 3), asserted
#                     deterministically rather than skipped.
#   4/5.             NOT ATTEMPTED - stage 3 produces no plan at all.
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
#
# Both copies keep the provider's skip_requesting_account_id = true, and that
# is a measured decision rather than boilerplate - see MEASURED, NOT ASSUMED
# in this file's header for what setting it false costs.
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
# Seed the estate copy's lock file from this init. The shared plugin cache
# records no checksums, so a directory with no .terraform.lock.hcl re-downloads
# the whole ~600MB AWS provider purely to compute them - ~320s, per init.
# Copying the lock file the first init just produced takes stage 2's init from
# that to about a second, and pins the identical provider build.
[ -f "$PLAIN/.terraform.lock.hcl" ] && cp "$PLAIN/.terraform.lock.hcl" "$ESTATE/.terraform.lock.hcl"
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
grep -qF "16 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 already recorded, 0 failed, 10 skipped" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not match the expected 16/0/0/0/0/10 breakdown"; }
# lex00/floci#72's own negative control. The three AWS Batch resources used to
# fail here with floci's AppSync misroute text; if it ever comes back, the
# emulator has regressed and stage 2 is partial again rather than clean.
grep -qF "GraphQL API not found" <<< "$APPROVE_OUT" \
  && { printf '%s\n' "$APPROVE_OUT"; fail "floci's AppSync tag-path misroute is back (lex00/floci#72) - the three Batch resources cannot be stamped"; }
log "  16 stamped, 0 failed, 10 correctly skipped (9 UNTAGGABLE + 1 already-ruled UNADMITTED_TYPE)"

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

# The AWS Batch job queue is the resource lex00/floci#72 blocked outright, so
# its marker is read back the same way - through the AWS CLI's own
# ListTagsForResource, which is the very call that used to be answered by
# AppSync. A marker here proves the fix reached the right object, not just
# that live-import stopped reporting a failure.
BATCH_QUEUE_ARN="$(awsl batch describe-job-queues --query "jobQueues[?jobQueueName=='${ESTATE_NAME}-queue'].jobQueueArn | [0]" --output text)"
[ -n "$BATCH_QUEUE_ARN" ] && [ "$BATCH_QUEUE_ARN" != "None" ] || fail "no job queue named ${ESTATE_NAME}-queue came back from floci"
GOT_QUEUE_ADDR="$(awsl batch list-tags-for-resource --resource-arn "$BATCH_QUEUE_ARN" --query 'tags."tofu-address"' --output text)"
[ "$GOT_QUEUE_ADDR" = "module.overture_tiles.aws_batch_job_queue.tiles" ] \
  || fail "the Batch job queue carries tofu-address=$GOT_QUEUE_ADDR, not module.overture_tiles.aws_batch_job_queue.tiles"
GOT_QUEUE_ESTATE="$(awsl batch list-tags-for-resource --resource-arn "$BATCH_QUEUE_ARN" --query 'tags."tofu-estate"' --output text)"
[ "$GOT_QUEUE_ESTATE" = "$ESTATE_NAME" ] || fail "the Batch job queue carries tofu-estate=$GOT_QUEUE_ESTATE, not $ESTATE_NAME"
# The module's own create-time tags must have survived the marker write: a
# TagResource that replaces instead of merging is how a live object silently
# loses either its own tags or its markers.
GOT_QUEUE_PROJECT="$(awsl batch list-tags-for-resource --resource-arn "$BATCH_QUEUE_ARN" --query 'tags.Project' --output text)"
[ "$GOT_QUEUE_PROJECT" = "overture-tiles-crossing" ] \
  || fail "the Batch job queue lost its own create-time Project tag during the stamp (got '$GOT_QUEUE_PROJECT')"
log "  batch job queue $BATCH_QUEUE_ARN -> tofu-address=$GOT_QUEUE_ADDR tofu-estate=$GOT_QUEUE_ESTATE, own Project tag intact"

if [ "${BREAK:-}" = "1" ]; then
  fail "BREAK=1: the bucket's real tofu-address matched the WRONG expected value above without this script noticing - stage 2's assertion is not load-bearing"
fi

log ""
log "STAGE 2 (migrate): PASS - 16 of 26 stamped, 0 failed; the other 10 correctly UNTAGGABLE or already-ruled UNADMITTED_TYPE"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - genuinely BLOCKED, asserted deterministically rather
# than skipped. The wall moved when stage 2 started succeeding: it is no
# longer a non-empty plan, it is live-plan REFUSING TO PLAN AT ALL, at
# exactly two diagnostics with one cause between them.
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 3: no state file, live-plan (expected to refuse - see header) ==="
rm -f "$ESTATE/terraform.tfstate" "$ESTATE/terraform.tfstate.backup"
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "the state file is still there"

PLAN_OUT="$(cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color 2>&1)"; PLAN_RC=$?
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "live-plan wrote a state file"

# Asserted, not tolerated: a nonzero exit is the recorded behaviour, and a
# ZERO exit here would mean the wall below has been fixed and this whole
# section needs rewriting rather than silently passing.
[ "$PLAN_RC" -ne 0 ] \
  || { printf '%s\n' "$PLAN_OUT" | tail -40; fail "live-plan exited 0 - the identity-ARN wall this stage records may be fixed; re-scope stages 3-5 rather than leaving this assertion inverted"; }

# The whole diagnostic surface, counted rather than sampled. Two errors, and
# the second is the first one's consequence.
PLAN_ERRORS="$(grep -cE '^Error: ' <<< "$PLAN_OUT")"
[ "$PLAN_ERRORS" = "2" ] \
  || { grep -E '^Error: ' <<< "$PLAN_OUT"; fail "expected exactly 2 errors from live-plan, got $PLAN_ERRORS - the wall has changed shape"; }

grep -qF "Invalid Identity Attribute Value" <<< "$PLAN_OUT" \
  || { printf '%s\n' "$PLAN_OUT" | tail -40; fail "expected the provider's identity-ARN account check to be the first error"; }
grep -qF 'Identity attribute "arn" contains an Account ID "000000000000" which does not' <<< "$PLAN_OUT" \
  || fail "expected the account-mismatch text naming 000000000000 against the provider's own empty account"
grep -qF "Cannot import for projection" <<< "$PLAN_OUT" \
  || fail "expected the projection import to be the second, consequent error"
grep -qF "arn:aws:batch:$REGION:000000000000:job-queue/${ESTATE_NAME}-queue" <<< "$PLAN_OUT" \
  || fail "expected both errors to name the Batch job queue's own marker-derived ARN"

# The marker itself is NOT what is wrong here, and this is the assertion that
# says so: the value the provider rejected is the same ARN the live object
# actually carries, read back independently through the AWS CLI at stage 2c.
# choudoufu resolved the right identity; the provider declined to validate it
# under this estate's own provider configuration.
[ "$(awsl batch describe-job-queues --query "jobQueues[?jobQueueName=='${ESTATE_NAME}-queue'].jobQueueArn | [0]" --output text)" \
    = "arn:aws:batch:$REGION:000000000000:job-queue/${ESTATE_NAME}-queue" ] \
  || fail "the ARN in the refusal is not the live object's own ARN - that would make this an identity defect rather than a provider-validation one"

log "  live-plan refused at exactly 2 diagnostics, both on the Batch job queue's own real ARN:"
log "    1. Invalid Identity Attribute Value - the provider compares an identity ARN's account"
log "       segment against the account it knows, and skip_requesting_account_id = true leaves"
log "       it knowing none, so a correct \"000000000000\" is rejected against \"\""
log "    2. Cannot import for projection - the consequence: projection will not build a plan"
log "       while a provider is erroring, because the result would propose creating objects"
log "       that already exist"

log ""
log "STAGE 3 (test plan): BLOCKED - live-plan refuses, deterministically, at exactly 2 traced diagnostics"
log ""

log ""
log "=== SUMMARY: stage 1 PASS; stage 2 PASS; stage 3 BLOCKED (deterministic, 2 traced diagnostics) ==="
log "=== stages 4-5 not attempted - both need a plan, and stage 3 does not produce one ==="
