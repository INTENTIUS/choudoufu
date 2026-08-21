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
#   3. FIXED (INTENTIUS/choudoufu#345), and newly reached at the time it was
#      filed - the provider would not validate a marker-derived identity ARN
#      under this estate's own provider block.
#      Once the three Batch resources carried markers, projection tried to
#      import one by its ARN identity, and hashicorp/aws refused:
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
#      - the ordinary way to point the AWS provider at a local emulator -
#      left it knowing none. live-plan exited 1 and produced no plan at all.
#      The ARN was never wrong: stage 3 re-reads the job queue's real ARN
#      through the AWS CLI and asserts the SAME identity below.
#
#      THE FIX IS A TEST-HARNESS ONE, NOT A floci CODE CHANGE - measured,
#      not argued, 2026-08-20. The obvious fix, `skip_requesting_account_id
#      = false` on the estate copy alone, clears the Batch identity error
#      and was first found to break stage 2 instead: once the provider
#      knows its account, it routes S3 bucket tag reads through S3
#      Control's account-prefixed virtual host -
#      `http://000000000000.127.0.0.1:4726/...` - and
#      `dial tcp: lookup 000000000000.127.0.0.1: no such host`. That is a
#      DNS failure, not an HTTP one: Go's resolver refuses to look up an
#      account-ID label prepended to a bare IPv4 literal, so the request
#      never reaches floci's HTTP server at all - no amount of Host-header-
#      agnostic routing inside floci can answer a request that was never
#      sent. Confirmed directly: `curl`ing
#      `http://<12-digit-account>.127.0.0.1:PORT/...` fails identically at
#      the DNS stage, before any TCP connection is attempted.
#
#      The actual fix is ENDPOINT itself. floci already publishes
#      `localhost.floci.io` as a real, public wildcard DNS domain -
#      `EmbeddedDnsServer.DEFAULT_SUFFIX`, resolving `*.localhost.floci.io`
#      to 127.0.0.1 exactly like LocalStack's own `localhost.localstack.
#      cloud` - and it resolves an account-ID-prefixed label the same as
#      any other: `dig +short 000000000000.localhost.floci.io` returns
#      `127.0.0.1` with no floci container running at all, since it is
#      ordinary public DNS, not floci's embedded per-container resolver.
#      Pointing ENDPOINT (and so AWS_ENDPOINT_URL) at `localhost.floci.io`
#      instead of a bare IP was verified end to end against the CURRENT,
#      unmodified floci image (be3f7ffd, sha256:8a882bcc - no re-pin): the
#      account-prefixed S3 Control request lands on floci's own
#      S3ControlController exactly as a bare-host request does, because
#      that controller dispatches on path (`/v20180820/...`), never on
#      Host, and S3VirtualHostFilter already excludes that path prefix from
#      its own virtual-host bucket rewrite. No floci Java code needed
#      changing.
#
# CONSEQUENCE FOR THE FIVE STAGES: stage 1 (cold deploy) is clean, and its
# own provider block is UNCHANGED (`skip_requesting_account_id = true`
# still, since plain OpenTofu never imports by identity ARN and there is no
# reason to touch it). The estate copy alone now sets
# `skip_requesting_account_id = false`; both copies share the new
# `localhost.floci.io` ENDPOINT, since it is transparent to every other
# call this script makes (health check, AWS CLI re-reads, plain S3 calls
# under s3_use_path_style). Stage 2 (migrate) still passes outright - see
# below for how its VERIFIED/DRIFTED split moved by exactly one resource as
# a direct, expected consequence of the account now being known. Stage 3
# (test plan) no longer crashes: live-plan exits 0 and produces a plan
# rather than two diagnostics. The plan is not EMPTY on the first try - see
# STAGE 3 below for why every line in it is already-known, by-design, or
# already-tracked - and applying it once (verified informationally, not a
# scored stage below) converges the estate to a genuinely empty replan, the
# same shape #345's own header records as the target. Stages 4 and 5 are
# left not_run in the formal sense: this repository's own convention (see
# corpus-mastino-dns, corpus-giantswarm-crossplane) scores test_apply only
# once test_plan itself is the genuinely empty first plan, and this one is
# not - it is deterministic and fully explained instead.
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
#                     state - PASS: 16 stamped, 0 failed, 10 skipped (moved
#                     from 11 VERIFIED/5 DRIFTED to 10/6 - see STAGE 2).
#   3. TEST PLAN     delete the state file, `choudoufu live-plan` - exits 0,
#                     a real plan, asserted deterministically: 1 to add
#                     (the already-tracked #249 OAC gap), 7 to change (six
#                     are the documented tofu-slot migration-visibility tag
#                     - see internal/live/discovery/count.go's
#                     bindCountByAddress doc comment - the seventh is the
#                     S3 bucket policy's own dependency on that same OAC).
#                     FAIL by this repository's own convention (a first
#                     plan must be empty to PASS), but the #345 wall itself
#                     - no plan at all - is gone.
#   4/5.             NOT_RUN in the scored sense - convention here scores
#                     test_apply only once test_plan is itself empty.
#                     Verified informationally instead: applying the stage
#                     3 plan succeeds (1 added, 6 changed, matching the
#                     plan), and a second live-plan afterward is genuinely
#                     empty ("No changes. Your infrastructure matches the
#                     configuration.") - the estate converges in exactly
#                     one apply, as #345's own header always expected.
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
ENDPOINT="http://localhost.floci.io:${FLOCI_PORT}"
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

# write_root <destdir> <live_block> <skip_account_id>: this crossing's own
# root wiring, calling the real module with the same S3/CloudFront/IAM/VPC
# inputs examples/complete uses, scoped exactly as this script's header
# states.
#
# skip_account_id is parameterized per copy (#345 FIXED, see header): the
# cold-deploy copy keeps skip_requesting_account_id = true (plain OpenTofu,
# no identity-ARN import ever happens there, no reason to change it), while
# the estate copy sets it false so the provider learns its own account and
# validates the Batch job queue's identity ARN. That alone would still break
# S3 Control's account-prefixed virtual host, per this file's header - the
# fix that makes it safe is ENDPOINT itself (localhost.floci.io instead of a
# bare IP), not this flag.
write_root() {
  local dest="$1" live_block="$2" skip_account_id="$3"
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
  skip_requesting_account_id  = $skip_account_id
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
write_root "$PLAIN" "" true
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
  }' false
log "  estate copy written to $ESTATE (stages 2-5: choudoufu, live block added, skip_requesting_account_id = false - see header, #345 FIXED)"

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
grep -qF "VERIFIED (10)" <<< "$IMPORT_OUT" || fail "expected exactly 10 VERIFIED resources (moved from 11 by #345's fix - see below)"
grep -qF "DRIFTED (6)" <<< "$IMPORT_OUT" \
  || fail "expected exactly 6 DRIFTED resources (deprecated shadow attributes, same class as corpus-hongbomiao-labelbox's own two, PLUS aws_launch_template.batch[0]'s arn - see below)"
# aws_launch_template.batch[0] moved from VERIFIED to DRIFTED as a direct,
# expected consequence of #345's fix: the state PLAIN's cold deploy wrote
# (skip_requesting_account_id = true there, untouched) recorded this
# launch template's computed arn with no account segment; the ESTATE
# copy's provider (skip_requesting_account_id = false, #345 FIXED) now
# knows its account and reads the SAME live object's arn WITH one. That is
# a real difference between two readings taken under two different
# provider configurations - an artifact of this crossing needing both
# configurations at once, not of the live object or the marker being
# wrong - and "DRIFTED, not VERIFIED" is the correct, honest classification
# for it.
grep -qF "arn (cty.StringVal(\"arn:aws:ec2:$REGION::launch-template/" <<< "$IMPORT_OUT" \
  || fail "expected aws_launch_template.batch[0]'s DRIFTED detail to name its own account-less prior arn"
grep -qF "UNTAGGABLE (9)" <<< "$IMPORT_OUT" || fail "expected exactly 9 UNTAGGABLE resources"
grep -qF "UNADMITTED_TYPE (1)" <<< "$IMPORT_OUT" || fail "expected exactly 1 UNADMITTED_TYPE resource"
grep -qF "aws_cloudfront_origin_access_control" <<< "$IMPORT_OUT" \
  || fail "expected the one UNADMITTED_TYPE resource to be aws_cloudfront_origin_access_control - already ruled 'enumerable, unbindable' by #249, not a new gap"
log "  16 of 26 eligible (10 VERIFIED, 6 DRIFTED); 9 UNTAGGABLE; 1 UNADMITTED_TYPE (aws_cloudfront_origin_access_control, already-ruled #249)"

log "--- 2b: -approve ---"
APPROVE_OUT="$(cd "$ESTATE" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE_NAME" -approve -no-color 2>&1)"; APPROVE_RC=$?
[ "$APPROVE_RC" -eq 0 ] || { printf '%s\n' "$APPROVE_OUT" | tail -40; fail "live-import -approve failed"; }
grep -qF "16 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 10 skipped" <<< "$APPROVE_OUT" \
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
# STAGE 3: TEST PLAN - #345 FIXED: live-plan no longer crashes. It produces
# a real, deterministic, non-empty plan instead - asserted exactly, address
# by address, rather than just "it didn't crash".
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 3: no state file, live-plan (expect a real plan - see header, #345 FIXED) ==="
rm -f "$ESTATE/terraform.tfstate" "$ESTATE/terraform.tfstate.backup"
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "the state file is still there"

PLAN_OUT="$(cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color 2>&1)"; PLAN_RC=$?
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "live-plan wrote a state file"

# Asserted, not tolerated: a nonzero exit here would mean the identity-ARN
# wall #345 fixed has come back, or a new one blocks projection outright.
[ "$PLAN_RC" -eq 0 ] \
  || { printf '%s\n' "$PLAN_OUT" | tail -60; fail "live-plan exited $PLAN_RC - the #345 identity-ARN wall may have come back, or a new one blocks projection"; }

ERRORS="$(grep -cE '^Error: ' <<< "$PLAN_OUT")"
[ "$ERRORS" = "0" ] \
  || { grep -E '^Error: ' <<< "$PLAN_OUT"; fail "expected 0 errors from live-plan, got $ERRORS"; }

grep -qF "Plan: 1 to add, 7 to change, 0 to destroy." <<< "$PLAN_OUT" \
  || { grep -E '^Plan: |^No changes' <<< "$PLAN_OUT"; fail "expected exactly 'Plan: 1 to add, 7 to change, 0 to destroy.' - the plan's shape has moved"; }

# Every changed address, named rather than just counted - the whole
# diagnostic surface of a plan that is deterministic but not yet empty.
CHANGED="$(grep -oE '^  # \S+ will be (created|updated in-place|destroyed|replaced)' <<< "$PLAN_OUT" | awk '{print $2}' | sort -u)"
WANT_CHANGED="module.overture_tiles.aws_cloudfront_distribution.tiles[0]
module.overture_tiles.aws_cloudfront_origin_access_control.tiles[0]
module.overture_tiles.aws_internet_gateway.batch[0]
module.overture_tiles.aws_launch_template.batch[0]
module.overture_tiles.aws_route_table.public[0]
module.overture_tiles.aws_s3_bucket_policy.tiles[0]
module.overture_tiles.aws_subnet.public[0]
module.overture_tiles.aws_vpc.batch[0]"
[ "$CHANGED" = "$WANT_CHANGED" ] || {
  printf 'got:\n%s\nwant:\n%s\n' "$CHANGED" "$WANT_CHANGED" >&2
  fail "the plan's changed-address set has moved"
}

# Every line traced to a known, already-tracked cause - nothing new:
#   - aws_cloudfront_origin_access_control.tiles[0] CREATED: the pre-existing,
#     already-ruled #249 UNADMITTED_TYPE gap (see stage 2), now visible as a
#     create because the plan reaches this far at all.
grep -qF "aws_cloudfront_origin_access_control.tiles[0] will be created" <<< "$PLAN_OUT" \
  || fail "expected the OAC create - #249's own gap, not a new one"
#   - the other six UPDATES are internal/live/discovery/count.go's own
#     documented, one-time tofu-slot migration-visibility tag
#     (bindCountByAddress: "visible in the plan as a tofu-slot tag being
#     added to each member" - by design, cements on the first apply), on
#     every count-toggled ([0]) resource this module declares.
for addr in aws_cloudfront_distribution.tiles aws_internet_gateway.batch \
            aws_launch_template.batch aws_route_table.public \
            aws_subnet.public aws_vpc.batch; do
  grep -qF "module.overture_tiles.$addr[0] will be updated in-place" <<< "$PLAN_OUT" \
    || fail "expected $addr[0]'s tofu-slot migration-visibility update"
done
grep -qF '+ "tofu-slot"    = "0"' <<< "$PLAN_OUT" \
  || fail "expected at least one tofu-slot tag actually being added, not just the resource header"
#   - aws_s3_bucket_policy.tiles[0] is the one CONTENT diff, cascading from
#     the OAC above: its desired policy names the CloudFront distribution's
#     own origin-access condition, which needs the OAC's arn - "known after
#     apply" until the OAC in this same plan exists.
grep -qF "module.overture_tiles.aws_s3_bucket_policy.tiles[0] will be updated in-place" <<< "$PLAN_OUT" \
  || fail "expected the S3 bucket policy update cascading from the new OAC"

log "  live-plan produced a real plan: Plan: 1 to add, 7 to change, 0 to destroy."
log "    1 add:  aws_cloudfront_origin_access_control.tiles[0] - the already-ruled #249 gap"
log "    6 tag-only changes: the documented tofu-slot migration-visibility tag"
log "       (internal/live/discovery/count.go, bindCountByAddress), on every"
log "       count-toggled ([0]) resource - by design, cements on first apply"
log "    1 content change: aws_s3_bucket_policy.tiles[0], cascading from the new OAC"

log ""
log "STAGE 3 (test plan): FAIL by this repo's own convention (first plan must be"
log "  empty) - but the #345 wall (no plan at all) is GONE, and every line above"
log "  traces to an already-tracked or by-design cause, none of them new."
log ""

# ── informational only, NOT a scored stage: does the estate actually
# converge? Verifies #345's own header claim - applying once should cement
# the tofu-slot tags and create the OAC, after which a second plan should
# be genuinely empty. Not stages 4/5 in the scored sense: this repository's
# own convention (corpus-mastino-dns, corpus-giantswarm-crossplane) scores
# test_apply only once test_plan is ITSELF the empty first plan, which this
# is not. Run for the evidence, not for the grade.
log "--- informational: does one apply converge the estate? (not scored) ---"
APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; APPLY_RC=$?
[ "$APPLY_RC" -eq 0 ] || { printf '%s\n' "$APPLY_OUT" | tail -40; log "  informational apply FAILED - not fatal to this script, but #345's convergence claim is unverified"; }
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "apply wrote a state file"
if [ "$APPLY_RC" -eq 0 ]; then
  grep -qE '^Apply complete!' <<< "$APPLY_OUT" \
    || log "  informational apply produced no 'Apply complete!' line - unexpected, not fatal"
  rm -f "$ESTATE/terraform.tfstate" "$ESTATE/terraform.tfstate.backup"
  REPLAN_OUT="$(cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color 2>&1)"; REPLAN_RC=$?
  if [ "$REPLAN_RC" -eq 0 ] && grep -qE 'No changes\.' <<< "$REPLAN_OUT"; then
    log "  CONFIRMED: one apply converges the estate - the second live-plan is genuinely empty"
  else
    log "  the second live-plan did NOT come back empty (rc=$REPLAN_RC) - #345's convergence claim needs revisiting; see $WORK if DEBUG_KEEP=1"
  fi
fi

log ""
log "=== SUMMARY: stage 1 PASS; stage 2 PASS; stage 3 FAIL (deterministic, non-empty," 
log "=== every line already-tracked or by-design); stages 4-5 NOT_RUN in the scored"
log "=== sense (convergence verified informationally above) ==="
