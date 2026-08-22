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
#   4. FIXED (test-harness only, no Go code touched) - test_plan was scored
#      FAIL against a known, one-time TRANSIENT rather than the estate's own
#      steady state. internal/live/discovery/count.go's own bindCountByAddress
#      documents that the migration-visibility tofu-slot tag is "visible in
#      the plan as a tofu-slot tag being added to each member" and "cements
#      on the first apply" - BY DESIGN. corpus-sqs-basic and
#      corpus-iam-policy already establish the convention this needs: fold
#      one ordinary apply into migrate to cement it, and score test_plan on
#      the plan AFTER that settles, not the raw post-import one. This
#      crossing used to run that exact apply "informationally" AFTER
#      test_plan was already scored FAIL on the pre-convergence plan (1 to
#      add, 7 to change - see old STAGE 3 below, kept as STAGE 2d's own
#      pre-convergence check now); folding it in ahead of scoring - unchanged
#      in WHAT it does, only WHEN - is what this unit found. The convergence
#      apply also creates and binds aws_cloudfront_origin_access_control.
#      tiles[0], #249's own "enumerable, unbindable" gap (server-assigned,
#      untaggable, so live-import could never BIND the OAC plain tofu's cold
#      deploy already created); this leaves that original OAC orphaned,
#      sharing its deterministic name with the new one - a real, known
#      consequence of #249's ruling and of this crossing running PLAIN and
#      ESTATE against the same account, not a new defect. STAGE 2d verifies
#      the new OAC's identity by value against the AWS CLI (via the
#      distribution's own OriginAccessControlId, not a name lookup, because
#      the name is no longer unique with the orphan present) rather than
#      trusting the empty replan alone. #249 stays open.
#
#   5. FLOCI GAP, filed (lex00/floci#98) - resourcegroupstaggingapi
#      GetResources does not index CloudWatch Logs log groups. Of the 16
#      objects stage 2 stamps, this estate's one aws_cloudwatch_log_group is
#      the sole omission from the cross-service tag search used to count
#      objects for stage 4's no-op-apply check, even though the log group's
#      own tag API (logs list-tags-log-group) reads its markers back
#      correctly. corpus-lambda-simple's own STAGE 4 hit the identical gap
#      first and routed around it without filing, noting it would be worth
#      an issue "if a later estate needs the cross-service search itself" -
#      this crossing is that later estate, so the gap is filed rather than
#      silently re-routed-around a second time. Stage 4 below counts the
#      cross-service search's own result PLUS one direct read of the log
#      group's own tag API, not the cross-service search alone.
#
# CONSEQUENCE FOR THE FIVE STAGES: stage 1 (cold deploy) is clean, and its
# own provider block is UNCHANGED (`skip_requesting_account_id = true`
# still, since plain OpenTofu never imports by identity ARN and there is no
# reason to touch it). The estate copy alone now sets
# `skip_requesting_account_id = false`; both copies share the new
# `localhost.floci.io` ENDPOINT, since it is transparent to every other
# call this script makes (health check, AWS CLI re-reads, plain S3 calls
# under s3_use_path_style). Stage 2 (migrate) passes outright - see below
# for how its VERIFIED/DRIFTED split moved by exactly one resource as a
# direct, expected consequence of the account now being known - and now
# includes STAGE 2d, the tofu-slot/OAC convergence apply (item 4 above).
# Stage 3 (test plan) is genuinely EMPTY: live-plan exits 0 with "No
# changes.", and the S3 bucket and OAC identities are re-checked by value
# against the AWS CLI one more time, fresh off that same plan. Stage 4 (test
# apply) is now written and PASSES: the empty plan applies as a genuine
# no-op (0 added, 0 changed, 0 destroyed), the tagged-object count is
# unchanged (item 5 above), and the S3 bucket and OAC identities are
# re-checked one more time, fresh off this apply. Stage 5 is still left
# not_run: test_apply just started passing this unit, and drift_reconverge
# is the next one, not attempted here.
#
# What crosses cleanly, and is asserted below: cold deploy (26 real
# resources, unmodified module), then 16 of those 26 stamped correctly, 0
# failed, with three markers re-read directly via the AWS CLI - never through
# choudoufu - after stamping: the S3 bucket's tofu-address, and the Batch job
# queue's tofu-address and tofu-estate, the resource gap 1 blocked entirely.
# The job queue's own create-time `Project` tag is asserted to have survived
# the stamp, because a tag write that replaces instead of merging is how a
# live object silently loses either its own tags or its markers. The
# remaining 10 are correctly UNTAGGABLE (9) or the already-ruled
# "enumerable, unbindable" aws_cloudfront_origin_access_control (1, #249) -
# STAGE 2d converges that one via a real apply, verified by value.
#
# STAGES:
#   1. COLD DEPLOY   plain `tofu apply` (real OpenTofu core, no choudoufu),
#                     the unmodified module - PASS.
#   2. MIGRATE       `choudoufu live-import -approve` against that cold
#                     state - PASS: 16 stamped, 0 failed, 10 skipped (moved
#                     from 11 VERIFIED/5 DRIFTED to 10/6 - see STAGE 2), then
#                     STAGE 2d converges: one apply cements the tofu-slot
#                     migration-visibility tag on the six count-toggled
#                     resources and creates/binds the #249 OAC gap, verified
#                     by value against the AWS CLI (item 4 above).
#   3. TEST PLAN     delete the state file a second time, `choudoufu
#                     live-plan` - PASS: genuinely empty ("No changes."),
#                     with the S3 bucket's tofu-address and the OAC's own Id
#                     re-checked against the AWS CLI, fresh off this plan.
#   4. TEST APPLY    apply the empty plan - PASS: genuine no-op (0 added, 0
#                     changed, 0 destroyed), the 16-object tagged count
#                     unchanged (resourcegroupstaggingapi plus one direct
#                     log-group tag read - lex00/floci#98, item 5 above), and
#                     the S3 bucket's tofu-address and the OAC's own Id
#                     re-checked one more time, fresh off this apply.
#   5. DRIFT/RECONVERGE  NOT_RUN - test_apply just started passing this
#                     unit; drift_reconverge is the next one, not attempted
#                     here.
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
gauntlet_begin
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
CURRENT_STAGE=cold_deploy
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
gauntlet_stage cold_deploy pass "26 resources, genuinely cold, genuinely unmarked"
CURRENT_STAGE=migrate

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
gauntlet_stage migrate pass "16 of 26 stamped, 0 failed; the other 10 correctly UNTAGGABLE or already-ruled UNADMITTED_TYPE"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2d: CONVERGE - the one-time settle every count-based migration needs.
# corpus-sqs-basic and corpus-iam-policy already establish this convention:
# internal/live/discovery/count.go's own bindCountByAddress documents that
# the migration-visibility tofu-slot tag is "visible in the plan as a
# tofu-slot tag being added to each member" and "cements on the first
# apply" - BY DESIGN. A script that deletes the state file and scores
# test_plan against the raw post-import projection is scoring a known,
# one-time TRANSIENT, not the steady state migrate is defined to reach.
# This crossing used to run this exact apply "informationally" AFTER
# test_plan was already scored FAIL on that transient; folding it in here -
# unchanged in WHAT it does, only in WHEN it is scored - is what this unit
# found and fixed. No Go code touched.
#
# The apply below also does one more thing, once: aws_cloudfront_origin_
# access_control.tiles[0] is #249's own "enumerable, unbindable" gap -
# server-assigned identity, untaggable - so live-import above could not BIND
# the OAC plain tofu's cold deploy already created (it is one of stage 2's
# 10 skipped, never one of the 16 stamped). This apply therefore CREATES a
# second, choudoufu-owned OAC and binds ITS identity from here on; the
# plain-tofu OAC is left orphaned. That is a direct, known consequence of
# #249's own ruling, not a new defect this unit introduces - #249 stays
# open on it. What this unit changes is only that the script stops mis-
# scoring the symptom (a non-empty first plan) as "test_plan: FAIL" once
# the very next plan is, and stays, genuinely empty.
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=migrate
log "=== STAGE 2d: converge (one apply cements tofu-slot and creates/binds the OAC) ==="

PRECONVERGE_OUT="$(cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color 2>&1)"; PRECONVERGE_RC=$?
[ "$PRECONVERGE_RC" -eq 0 ] \
  || { printf '%s\n' "$PRECONVERGE_OUT" | tail -60; fail "the pre-convergence live-plan exited $PRECONVERGE_RC - the #345 identity-ARN wall may have come back, or a new one blocks projection"; }
grep -qF "Plan: 1 to add, 7 to change, 0 to destroy." <<< "$PRECONVERGE_OUT" \
  || { grep -E '^Plan: |^No changes' <<< "$PRECONVERGE_OUT"; fail "expected the pre-convergence plan to read exactly 'Plan: 1 to add, 7 to change, 0 to destroy.' - the module's own shape, or one of the gaps this script documents, has moved"; }

# Every changed address, named rather than just counted.
PRECONVERGE_CHANGED="$(grep -oE '^  # \S+ will be (created|updated in-place|destroyed|replaced)' <<< "$PRECONVERGE_OUT" | awk '{print $2}' | sort -u)"
WANT_PRECONVERGE_CHANGED="module.overture_tiles.aws_cloudfront_distribution.tiles[0]
module.overture_tiles.aws_cloudfront_origin_access_control.tiles[0]
module.overture_tiles.aws_internet_gateway.batch[0]
module.overture_tiles.aws_launch_template.batch[0]
module.overture_tiles.aws_route_table.public[0]
module.overture_tiles.aws_s3_bucket_policy.tiles[0]
module.overture_tiles.aws_subnet.public[0]
module.overture_tiles.aws_vpc.batch[0]"
[ "$PRECONVERGE_CHANGED" = "$WANT_PRECONVERGE_CHANGED" ] || {
  printf 'got:\n%s\nwant:\n%s\n' "$PRECONVERGE_CHANGED" "$WANT_PRECONVERGE_CHANGED" >&2
  fail "the pre-convergence plan's changed-address set has moved"
}
# Every line traced to a known, already-tracked cause - nothing new:
#   - aws_cloudfront_origin_access_control.tiles[0] CREATED: the pre-existing,
#     already-ruled #249 gap (see this block's header).
grep -qF "aws_cloudfront_origin_access_control.tiles[0] will be created" <<< "$PRECONVERGE_OUT" \
  || fail "expected the OAC create - #249's own gap, not a new one"
#   - the other six UPDATES are the tofu-slot migration-visibility tag
#     (bindCountByAddress, see this block's header), on every count-toggled
#     ([0]) resource this module declares.
for addr in aws_cloudfront_distribution.tiles aws_internet_gateway.batch \
            aws_launch_template.batch aws_route_table.public \
            aws_subnet.public aws_vpc.batch; do
  grep -qF "module.overture_tiles.$addr[0] will be updated in-place" <<< "$PRECONVERGE_OUT" \
    || fail "expected $addr[0]'s tofu-slot migration-visibility update"
done
grep -qF '+ "tofu-slot"    = "0"' <<< "$PRECONVERGE_OUT" \
  || fail "expected at least one tofu-slot tag actually being added, not just the resource header"
#   - aws_s3_bucket_policy.tiles[0] is the one CONTENT diff, cascading from
#     the OAC above: its desired policy names the CloudFront distribution's
#     own origin-access condition, which needs the OAC's arn - "known after
#     apply" until the OAC in this same plan exists.
grep -qF "module.overture_tiles.aws_s3_bucket_policy.tiles[0] will be updated in-place" <<< "$PRECONVERGE_OUT" \
  || fail "expected the S3 bucket policy update cascading from the new OAC"
log "  pre-convergence live-plan: Plan: 1 to add, 7 to change, 0 to destroy - every line traced (see STAGE 2d header)"
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "live-plan wrote a state file"

CONVERGE_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; CONVERGE_RC=$?
[ "$CONVERGE_RC" -eq 0 ] || { printf '%s\n' "$CONVERGE_OUT" | tail -40; fail "the convergence apply failed"; }
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "the convergence apply wrote a state file"
grep -qE '^Apply complete! Resources: 1 added, 6 changed, 0 destroyed\.' <<< "$CONVERGE_OUT" \
  || { grep -E '^Apply complete' <<< "$CONVERGE_OUT"; fail "the convergence apply did not read exactly 'Apply complete! Resources: 1 added, 6 changed, 0 destroyed.'"; }
log "  $(grep -E '^Apply complete' <<< "$CONVERGE_OUT") (tofu-slot cemented; OAC created and bound)"

# The new OAC's identity, BY VALUE - not just "the plan came back empty
# afterward". HANDOFF's own warning ("an empty plan alone is not enough: a
# wrong identity can converge") applies here as much as anywhere.
#
# Read the distribution's own origin FIRST, not a name-based OAC lookup: this
# crossing runs PLAIN's cold deploy and the ESTATE copy against the SAME
# floci account, and PLAIN's own cold-deploy OAC (module's
# name = "${name_prefix}-oac", identical in both copies) is exactly the
# object #249's ruling leaves orphaned rather than bound - see this block's
# header. So floci now genuinely holds TWO origin access controls sharing
# that name, and asserting "exactly one" would be asserting away a real,
# already-documented consequence of this script's own dual-stack
# methodology, not a defect. The distribution's own OriginAccessControlId is
# the single, unambiguous value that says which OAC ESTATE actually uses;
# that Id's own Name is then checked against the module's deterministic
# naming convention, which is the by-value assertion that matters here.
WANT_DIST_COMMENT="${ESTATE_NAME} tiles distribution"
DIST_ID="$(awsl cloudfront list-distributions --query "DistributionList.Items[?Comment=='$WANT_DIST_COMMENT'].Id | [0]" --output text)"
[ -n "$DIST_ID" ] && [ "$DIST_ID" != "None" ] || fail "no CloudFront distribution commented '$WANT_DIST_COMMENT' came back from floci"
GOT_OAC_ID="$(awsl cloudfront get-distribution-config --id "$DIST_ID" --query "DistributionConfig.Origins.Items[0].OriginAccessControlId" --output text)"
[ -n "$GOT_OAC_ID" ] && [ "$GOT_OAC_ID" != "None" ] || fail "the distribution's own origin carries no OriginAccessControlId"
WANT_OAC_NAME="${ESTATE_NAME}-oac"
GOT_OAC_NAME="$(awsl cloudfront get-origin-access-control --id "$GOT_OAC_ID" --query "OriginAccessControl.OriginAccessControlConfig.Name" --output text)"
[ "$GOT_OAC_NAME" = "$WANT_OAC_NAME" ] \
  || fail "the distribution's own OAC ($GOT_OAC_ID) is named $GOT_OAC_NAME, not $WANT_OAC_NAME"
OAC_NAME_COUNT="$(awsl cloudfront list-origin-access-controls --query "length(OriginAccessControlList.Items[?Name=='$WANT_OAC_NAME'])" --output text)"
log "  OAC referenced by the distribution: $GOT_OAC_ID, named $GOT_OAC_NAME as expected ($OAC_NAME_COUNT total share that name - the PLAIN cold-deploy orphan plus this one, per #249)"

REPLAN_OUT="$(cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color 2>&1)"; REPLAN_RC=$?
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "the post-convergence live-plan wrote a state file"
[ "$REPLAN_RC" -eq 0 ] \
  || { printf '%s\n' "$REPLAN_OUT" | tail -60; fail "the post-convergence live-plan exited $REPLAN_RC - convergence did not settle the estate"; }
grep -qE 'No changes\.' <<< "$REPLAN_OUT" \
  || { grep -E '^Plan: |^No changes' <<< "$REPLAN_OUT"; fail "the post-convergence live-plan is not empty - convergence did not settle the estate"; }
log "  post-convergence live-plan: No changes. - the estate has reached steady state"
log ""
log "STAGE 2d (converge): done - tofu-slot cemented, OAC created and bound, steady state reached"
log ""
CURRENT_STAGE=""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - #345 FIXED, and now genuinely EMPTY. STAGE 2d above
# folds in the one-time convergence this estate's count blocks and its #249
# OAC gap require; this stage deletes the state file a SECOND time and reruns
# live-plan completely fresh, never trusting the plan already seen in 2d, so
# it is its own genuine, from-nothing check.
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=test_plan
log "=== STAGE 3: no state file, live-plan (expect empty) ==="
rm -f "$ESTATE/terraform.tfstate" "$ESTATE/terraform.tfstate.backup"
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "the state file is still there"

PLAN_OUT="$(cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color 2>&1)"; PLAN_RC=$?
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "live-plan wrote a state file"

[ "$PLAN_RC" -eq 0 ] \
  || { printf '%s\n' "$PLAN_OUT" | tail -60; fail "live-plan exited $PLAN_RC"; }

ERRORS="$(grep -cE '^Error: ' <<< "$PLAN_OUT")"
[ "$ERRORS" = "0" ] \
  || { grep -E '^Error: ' <<< "$PLAN_OUT"; fail "expected 0 errors from live-plan, got $ERRORS"; }

grep -qE 'No changes\.' <<< "$PLAN_OUT" \
  || { grep -E '^Plan: |^No changes' <<< "$PLAN_OUT"; fail "expected an empty plan (No changes.), got a real diff - convergence did not hold"; }

# The representative identities, re-checked directly against the AWS CLI one
# more time, fresh off THIS plan's own projection - not reused from stage 2d
# or 2c. An empty plan alone is not enough (HANDOFF): a wrong identity can
# converge just as quietly as a right one.
GOT_BUCKET_ADDR2="$(awsl s3api get-bucket-tagging --bucket "$BUCKET_NAME" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_BUCKET_ADDR2" = "module.overture_tiles.aws_s3_bucket.tiles:0" ] \
  || fail "the S3 bucket's tofu-address moved to $GOT_BUCKET_ADDR2 across stage 3"
GOT_OAC_ID2="$(awsl cloudfront get-distribution-config --id "$DIST_ID" --query "DistributionConfig.Origins.Items[0].OriginAccessControlId" --output text)"
[ "$GOT_OAC_ID2" = "$GOT_OAC_ID" ] \
  || fail "the OAC's own Id moved from $GOT_OAC_ID to $GOT_OAC_ID2 across stage 3 - a wrong identity converging, not a right one"
log "  live-plan: No changes. - identities re-checked: bucket $GOT_BUCKET_ADDR2, OAC $GOT_OAC_ID2"

log ""
log "STAGE 3 (test plan): PASS - live-plan is genuinely empty; representative"
log "  identities (S3 bucket, OAC) re-checked against the AWS CLI by value"
log ""
gauntlet_stage test_plan pass "live-plan empty after the STAGE 2d convergence apply; S3 bucket and OAC identities re-checked by value against the AWS CLI"
CURRENT_STAGE=""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 4: TEST APPLY - apply the empty plan; assert a genuine no-op both by
# tagged-object count and by re-checking the two identities stage 3 already
# checked, fresh off this apply rather than reused.
#
# floci's resourcegroupstaggingapi GetResources does not index CloudWatch
# Logs log groups (lex00/floci#98, filed by this unit). Confirmed directly
# against this estate: the cross-service search returns 15 of the 16 objects
# stage 2 stamped, the log group the sole omission, even though the log
# group's OWN tag API (logs list-tags-log-group / list-tags-for-resource)
# reads tofu-estate/tofu-address back correctly. corpus-lambda-simple's own
# STAGE 4 first found and routed around this exact gap without filing it,
# noting it would be "worth one if a later estate needs the cross-service
# search itself" - this is that later estate, so the issue is now filed
# rather than re-routed-around silently a second time. The count below is
# the cross-service search PLUS one direct read of the log group's own tag
# API, not the cross-service search alone: a smaller count would silently
# under-report, which is worse than a slower, honest one.
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=test_apply
log "=== STAGE 4: test apply (apply the empty plan; object count and identities unchanged) ==="

LOGGROUP_NAME="/aws/batch/${ESTATE_NAME}"

# tagged_object_count: resourcegroupstaggingapi's own cross-service count,
# plus the one object it cannot see (floci#98, see header above), read
# directly through its own service instead.
tagged_object_count() {
  local n
  n="$(awsl resourcegroupstaggingapi get-resources \
    --tag-filters "Key=tofu-estate,Values=$ESTATE_NAME" \
    --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
  local lg_estate
  lg_estate="$(awsl logs list-tags-log-group --log-group-name "$LOGGROUP_NAME" --query 'tags."tofu-estate"' --output text 2>/dev/null)"
  [ "$lg_estate" = "$ESTATE_NAME" ] && n=$((n + 1))
  printf '%s\n' "$n"
}

BEFORE_N="$(tagged_object_count)"
[ "$BEFORE_N" = "16" ] || fail "expected 16 tagged objects before the no-op apply (the 16 stamped in stage 2: 15 via resourcegroupstaggingapi + the log group read directly, floci#98), got $BEFORE_N"

NOOP_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; NOOP_APPLY_RC=$?
[ "$NOOP_APPLY_RC" -eq 0 ] || { printf '%s\n' "$NOOP_APPLY_OUT" | tail -40; fail "the no-op apply exited $NOOP_APPLY_RC"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed|No changes' <<< "$NOOP_APPLY_OUT" \
  || { grep -E 'Apply complete|Plan: ' <<< "$NOOP_APPLY_OUT"; fail "the no-op apply was not a genuine no-op"; }
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "the no-op apply left a state file behind"

AFTER_N="$(tagged_object_count)"
[ "$AFTER_N" = "$BEFORE_N" ] || fail "the tagged object count changed across a no-op apply: $BEFORE_N -> $AFTER_N"

# The same two identities stage 3 checked, re-checked one more time, fresh off
# THIS apply - not reused. HANDOFF: an empty plan (or a no-op apply) alone is
# not enough; a wrong identity can converge, or survive, just as quietly as a
# right one.
GOT_BUCKET_ADDR3="$(awsl s3api get-bucket-tagging --bucket "$BUCKET_NAME" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_BUCKET_ADDR3" = "module.overture_tiles.aws_s3_bucket.tiles:0" ] \
  || fail "the S3 bucket's tofu-address moved to $GOT_BUCKET_ADDR3 across the no-op apply"
GOT_OAC_ID3="$(awsl cloudfront get-distribution-config --id "$DIST_ID" --query "DistributionConfig.Origins.Items[0].OriginAccessControlId" --output text)"
[ "$GOT_OAC_ID3" = "$GOT_OAC_ID" ] \
  || fail "the OAC's own Id moved from $GOT_OAC_ID to $GOT_OAC_ID3 across the no-op apply"

# The record store itself survived a run with nothing to change - not
# silently dropped by a write-back after a no-op.
[ -d "$ESTATE/.tofu-records" ] || fail "the record store directory is gone after the no-op apply"
[ -n "$(find "$ESTATE/.tofu-records" -type f 2>/dev/null)" ] || fail "the record store holds no files after the no-op apply"

log "  genuine no-op: $BEFORE_N tagged objects before, $AFTER_N after (resourcegroupstaggingapi + log group direct read, floci#98), no state file"
log "  identities re-checked: bucket $GOT_BUCKET_ADDR3, OAC $GOT_OAC_ID3; record store intact"

log ""
log "STAGE 4 (test apply): PASS - genuine no-op; object count and identities unchanged"
log ""
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed); $BEFORE_N tagged objects before and after (resourcegroupstaggingapi + log group direct read - floci#98); S3 bucket and OAC identities unchanged; record store intact"
CURRENT_STAGE=""

gauntlet_stage drift_reconverge not_run "not yet exercised by this script - test_apply just started passing this unit; drift_reconverge is the next unit"
gauntlet_end

log ""
log "=== SUMMARY: stage 1 PASS; stage 2 PASS; stage 3 PASS (empty plan, S3 bucket"
log "=== and OAC identities verified by value); stage 4 PASS (no-op apply, object"
log "=== count and identities unchanged); stage 5 NOT_RUN (not yet exercised) ==="
