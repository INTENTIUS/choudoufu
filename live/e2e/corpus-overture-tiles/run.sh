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
#      documented that the migration-visibility tofu-slot tag was "visible in
#      the plan as a tofu-slot tag being added to each member" and "cements
#      on the first apply" - BY DESIGN at the time, and since choudoufu #372
#      not a transient at all: live-import writes the slot itself. What is
#      left in 2d is the #249 OAC create. corpus-sqs-basic and
#      corpus-iam-policy established the convention this needs: fold
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
#   5. FLOCI GAP, filed (lex00/floci#98), NOW FIXED. resourcegroupstaggingapi
#      GetResources used not to index CloudWatch Logs log groups. Of the 16
#      objects stage 2 stamps, this estate's one aws_cloudwatch_log_group
#      was the sole omission from the cross-service tag search used to count
#      objects for stage 4's no-op-apply check, even though the log group's
#      own tag API (logs list-tags-log-group) read its markers back
#      correctly. corpus-lambda-simple's own STAGE 4 hit the identical gap
#      first and routed around it without filing, noting it would be worth
#      an issue "if a later estate needs the cross-service search itself" -
#      this crossing is that later estate, so the gap was filed rather than
#      silently re-routed-around a second time. Fixed in floci commit
#      c212d9e84 ("fix(resourcegroupstagging): index CloudWatch Logs log
#      groups in GetResources"), on the path to the pin
#      ghcr.io/lex00/floci@sha256:0afd2648...: re-measured against this pin,
#      the cross-service search alone now returns all 16 objects, including
#      the log group. The stage-4 workaround (count the search's own result
#      PLUS one direct read of the log group's own tag API) is gone -
#      keeping it after the fix would have double-counted the log group
#      (17, not 16), which is exactly the failure this pin bump surfaced.
#      Stage 4 below asserts the log group's ARN is present in the
#      cross-service search's own result directly, so a future regression
#      of #98 fails on that assertion rather than silently returning to the
#      workaround's old count by coincidence.
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
# includes STAGE 2d, the OAC convergence apply (item 4 above; its tofu-slot
# half is gone since choudoufu #372).
# Stage 3 (test plan) is genuinely EMPTY: live-plan exits 0 with "No
# changes.", and the S3 bucket and OAC identities are re-checked by value
# against the AWS CLI one more time, fresh off that same plan. Stage 4 (test
# apply) is now written and PASSES: the empty plan applies as a genuine
# no-op (0 added, 0 changed, 0 destroyed), the tagged-object count is
# unchanged (item 5 above), and the S3 bucket and OAC identities are
# re-checked one more time, fresh off this apply. Stage 5 (drift and
# reconverge) now PASSES too: the VPC's own Name tag is mutated out of band
# via the AWS CLI, choudoufu's plan proposes fixing exactly that one object,
# the stock oracle (plain tofu, the same live VPC via $PLAIN's own untouched
# state) proposes the identical change, and the reconverging apply restores
# the configured value.
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
#                     STAGE 2d converges: one apply creates and binds the
#                     #249 OAC gap, verified by value against the AWS CLI
#                     (item 4 above). The tofu-slot half of that apply is
#                     gone since choudoufu #372 - live-import writes the
#                     slot itself now, and 2d asserts the plan proposes no
#                     tofu-slot change in either direction.
#   3. TEST PLAN     delete the state file a second time, `choudoufu
#                     live-plan` - PASS: genuinely empty ("No changes."),
#                     with the S3 bucket's tofu-address and the OAC's own Id
#                     re-checked against the AWS CLI, fresh off this plan.
#   4. TEST APPLY    apply the empty plan - PASS: genuine no-op (0 added, 0
#                     changed, 0 destroyed), the 16-object tagged count
#                     unchanged (resourcegroupstaggingapi's cross-service
#                     search alone, now that lex00/floci#98 is fixed - item
#                     5 above), and the S3 bucket's tofu-address and the
#                     OAC's own Id re-checked one more time, fresh off this
#                     apply.
#   5. DRIFT/RECONVERGE  PASS - the VPC's Name tag mutated out of band via
#                     the AWS CLI; choudoufu's plan proposes fixing exactly
#                     module.overture_tiles.aws_vpc.batch[0] and nothing
#                     else; the stock oracle ($PLAIN, the same live VPC)
#                     proposes the identical change with marker tags
#                     (tofu-address/tofu-estate/tofu-slot) normalised out of
#                     both plans; the reconverging apply restores the
#                     configured value.
#
# BREAK=1 corrupts the S3 bucket's expected tofu-address ahead of stage 2's
# AWS-CLI re-read, proving that assertion is load-bearing. BREAK_STAGE5=1
# tampers a second live object (the internet gateway's Name tag) ahead of
# stage 5's own mutation, proving its single-object assertion is load-bearing.
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
#   BREAK_STAGE5  set to 1 to tamper a second live object ahead of stage 5,
#                 proving its single-object assertion is load-bearing.
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
# STAGE 2d: CONVERGE - what is left of the one-time settle a count-based
# migration used to need.
#
# It used to be two things. The tofu-slot half is GONE as of choudoufu #372:
# internal/live/discovery/count.go's bindCountByAddress used to be the only
# thing that could work out the slot for a set carrying none, so a migration
# left it to the first replan and the tag was "visible in the plan as a
# tofu-slot tag being added to each member", cementing on the first apply.
# But bindCountByAddress's assignment for a slotless set is not a discovery -
# it is slot i for index i (internal/live/slots.Sequential), frozen from the
# per-instance tofu-address values live-import is already writing. live-import
# now writes it in the same call, gated on the type being server-assigned
# (the one case where the instance's ClassNeedsDiscovery class is certain
# without a resolution pass a migrate does not run). Six of this plan's
# changes were that tag; all six are gone, asserted by absence below, in BOTH
# directions - an ungated version of #372 wrote a slot onto this estate's
# CLIENT-NAMED aws_s3_bucket.tiles[0] too, and the plan proposed REMOVING it,
# which is the failure this assertion catches.
#
# What is left is the other half, and this apply still exists for it:
# aws_cloudfront_origin_
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
log "=== STAGE 2d: converge (one apply creates and binds the OAC; tofu-slot is already cemented, choudoufu #372) ==="

PRECONVERGE_OUT="$(cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color 2>&1)"; PRECONVERGE_RC=$?
[ "$PRECONVERGE_RC" -eq 0 ] \
  || { printf '%s\n' "$PRECONVERGE_OUT" | tail -60; fail "the pre-convergence live-plan exited $PRECONVERGE_RC - the #345 identity-ARN wall may have come back, or a new one blocks projection"; }
grep -qF "Plan: 1 to add, 2 to change, 0 to destroy." <<< "$PRECONVERGE_OUT" \
  || { grep -E '^Plan: |^No changes' <<< "$PRECONVERGE_OUT"; grep -E '^  # ' <<< "$PRECONVERGE_OUT"; fail "expected the pre-convergence plan to read exactly 'Plan: 1 to add, 2 to change, 0 to destroy.' - the module's own shape, or one of the gaps this script documents, has moved"; }

# Every changed address, named rather than just counted.
PRECONVERGE_CHANGED="$(grep -oE '^  # \S+ will be (created|updated in-place|destroyed|replaced)' <<< "$PRECONVERGE_OUT" | awk '{print $2}' | sort -u)"
WANT_PRECONVERGE_CHANGED="module.overture_tiles.aws_cloudfront_distribution.tiles[0]
module.overture_tiles.aws_cloudfront_origin_access_control.tiles[0]
module.overture_tiles.aws_s3_bucket_policy.tiles[0]"
[ "$PRECONVERGE_CHANGED" = "$WANT_PRECONVERGE_CHANGED" ] || {
  printf 'got:\n%s\nwant:\n%s\n' "$PRECONVERGE_CHANGED" "$WANT_PRECONVERGE_CHANGED" >&2
  fail "the pre-convergence plan's changed-address set has moved"
}
# Every line traced to a known, already-tracked cause - nothing new:
#   - aws_cloudfront_origin_access_control.tiles[0] CREATED: the pre-existing,
#     already-ruled #249 gap (see this block's header).
grep -qF "aws_cloudfront_origin_access_control.tiles[0] will be created" <<< "$PRECONVERGE_OUT" \
  || fail "expected the OAC create - #249's own gap, not a new one"
#   - and no tofu-slot tag change at all, in either direction. Six of this
#     plan's changes used to be exactly that - the migration-visibility tag
#     on every count-toggled ([0]) resource this module declares - and
#     choudoufu #372 moved that write into live-import itself, where the
#     assignment (slot i for index i) is the same one bindCountByAddress
#     would have reached and is frozen from the same tofu-address the same
#     call is writing. The absence is asserted in BOTH directions on
#     purpose: a superfluous slot would show up here as a REMOVAL, which is
#     what an ungated version of #372 produced on this estate's
#     aws_s3_bucket.tiles[0] (client-named, so the plan never wanted one).
# A DIFF line only: a plan body that changes any attribute prints the whole
# tags map, unchanged entries included, so a bare grep for the key matches
# every block that changes anything at all and asserts nothing.
grep -qE '^[[:space:]]*[+~-][[:space:]]+"tofu-slot"' <<< "$PRECONVERGE_OUT" \
  && { grep -B 6 -A 2 -E '^[[:space:]]*[+~-][[:space:]]+"tofu-slot"' <<< "$PRECONVERGE_OUT"
       fail "the pre-convergence plan proposes a tofu-slot change; since choudoufu #372 live-import settles the slot for every count-expanded instance of a server-assigned type here, neither an addition nor a removal may appear"; }
for addr in aws_internet_gateway.batch aws_launch_template.batch \
            aws_route_table.public aws_subnet.public aws_vpc.batch; do
  grep -qF "module.overture_tiles.$addr[0] will be updated in-place" <<< "$PRECONVERGE_OUT" \
    && fail "$addr[0] is still in the pre-convergence plan - choudoufu #372 should have settled its tofu-slot at migrate time"
done
#   - aws_s3_bucket_policy.tiles[0] is the one CONTENT diff, cascading from
#     the OAC above: its desired policy names the CloudFront distribution's
#     own origin-access condition, which needs the OAC's arn - "known after
#     apply" until the OAC in this same plan exists.
grep -qF "module.overture_tiles.aws_s3_bucket_policy.tiles[0] will be updated in-place" <<< "$PRECONVERGE_OUT" \
  || fail "expected the S3 bucket policy update cascading from the new OAC"
log "  pre-convergence live-plan: Plan: 1 to add, 2 to change, 0 to destroy - every line traced, and no tofu-slot in it at all (see STAGE 2d header)"
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "live-plan wrote a state file"

CONVERGE_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; CONVERGE_RC=$?
[ "$CONVERGE_RC" -eq 0 ] || { printf '%s\n' "$CONVERGE_OUT" | tail -40; fail "the convergence apply failed"; }
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "the convergence apply wrote a state file"
grep -qE '^Apply complete! Resources: 1 added, 1 changed, 0 destroyed\.' <<< "$CONVERGE_OUT" \
  || { grep -E '^Apply complete' <<< "$CONVERGE_OUT"; fail "the convergence apply did not read exactly 'Apply complete! Resources: 1 added, 1 changed, 0 destroyed.'"; }
log "  $(grep -E '^Apply complete' <<< "$CONVERGE_OUT") (OAC created and bound; the tofu-slot half is gone since choudoufu #372)"

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
# floci's resourcegroupstaggingapi GetResources used not to index CloudWatch
# Logs log groups (lex00/floci#98, filed by this unit) - the cross-service
# search returned 15 of the 16 objects stage 2 stamped, the log group the
# sole omission, even though the log group's OWN tag API (logs
# list-tags-log-group / list-tags-for-resource) read tofu-estate/tofu-address
# back correctly. corpus-lambda-simple's own STAGE 4 first found and routed
# around this exact gap without filing it, noting it would be "worth one if
# a later estate needs the cross-service search itself" - this was that
# later estate, so the issue was filed rather than re-routed-around silently
# a second time.
#
# FIXED by floci commit c212d9e84 ("fix(resourcegroupstagging): index
# CloudWatch Logs log groups in GetResources"), on the path to the pin
# ghcr.io/lex00/floci@sha256:0afd2648...: re-measured against this pin, the
# cross-service search alone now returns all 16 objects. The count below is
# the cross-service search alone, with the log group's presence in it
# asserted directly (not inferred from a count matching by coincidence) -
# keeping the old "+1 direct read" workaround after this fix would silently
# double-count the log group instead of under-reporting it, which is
# exactly the failure this pin bump surfaced (BEFORE_N=17, not 16).
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=test_apply
log "=== STAGE 4: test apply (apply the empty plan; object count and identities unchanged) ==="

LOGGROUP_NAME="/aws/batch/${ESTATE_NAME}"

# tagged_object_count: resourcegroupstaggingapi's own cross-service count,
# now that lex00/floci#98 is fixed and the log group is part of it (see
# header above). Prints the raw ARN list on the first line and the count on
# the second, so the caller can assert the log group's presence directly
# (not called from inside a command substitution itself, since fail() must
# be able to report and exit the whole script, not just a subshell).
tagged_object_count_raw() {
  awsl resourcegroupstaggingapi get-resources \
    --tag-filters "Key=tofu-estate,Values=$ESTATE_NAME" \
    --query 'ResourceTagMappingList[].ResourceARN' --output text 2>/dev/null || true
}
tagged_object_count() {
  local resources
  resources="$(tagged_object_count_raw)"
  if [ -z "$resources" ]; then
    printf '0\n'
  else
    wc -w <<< "$resources" | tr -d ' '
  fi
}
assert_log_group_indexed() {
  local resources
  resources="$(tagged_object_count_raw)"
  grep -q "log-group:${LOGGROUP_NAME}\b" <<< "$resources" \
    || fail "the log group $LOGGROUP_NAME is missing from resourcegroupstaggingapi's cross-service search - lex00/floci#98 may have regressed"
}

assert_log_group_indexed
BEFORE_N="$(tagged_object_count)"
[ "$BEFORE_N" = "16" ] || fail "expected 16 tagged objects before the no-op apply (the 16 stamped in stage 2, all via resourcegroupstaggingapi now that floci#98 is fixed), got $BEFORE_N"

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

log "  genuine no-op: $BEFORE_N tagged objects before, $AFTER_N after (resourcegroupstaggingapi's cross-service search alone, floci#98 fixed), no state file"
log "  identities re-checked: bucket $GOT_BUCKET_ADDR3, OAC $GOT_OAC_ID3; record store intact"

log ""
log "STAGE 4 (test apply): PASS - genuine no-op; object count and identities unchanged"
log ""
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed); $BEFORE_N tagged objects before and after (resourcegroupstaggingapi's cross-service search alone, floci#98 fixed); S3 bucket and OAC identities unchanged; record store intact"
CURRENT_STAGE=""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 5: DRIFT AND RECONVERGE
# ══════════════════════════════════════════════════════════════════════════
#
# The same $ESTATE estate, already stamped and already proven to plan and
# apply empty (stages 2-4), is the natural place to prove the OTHER
# direction: one live object changed out of band, directly through the AWS
# CLI, is detected and the fix is scoped to exactly that object - not "the
# whole estate looks different." The mutated attribute is the VPC's own Name
# tag (module.overture_tiles.aws_vpc.batch[0]): "${ESTATE_NAME}-vpc" in the
# config, changed live to "tampered-out-of-band" via `aws ec2 create-tags` -
# never through choudoufu. $PLAIN is still a plain-tofu working directory
# with its own state file from stage 1's cold apply, untouched since; because
# STAGE 2's live-import BOUND (not recreated) this exact VPC - it is one of
# the 10 VERIFIED resources - $PLAIN's state still names the very same live
# object, which is what makes it a legitimate stock oracle for this
# mutation, same as reference-ec2-vpc's PART C and corpus-xancloud-iac's own
# STAGE 5 (also a VPC Name-tag mutation, same module shape).
#
# This choice is deliberately clear of this estate's known, tracked gap:
# #249's orphaned CloudFront OAC is untouched by a VPC tag mutation, so it
# needs no special-casing in the object-count or identity logic below -
# there is no object count in this stage at all, only a single-address plan
# diff. (floci#98's log-group tagging gap, the other gap this estate used
# to route around, is fixed as of the pin this script now runs against -
# see item 5 above.)
#
# The VPC is also one of the six count-toggled ([0]) resources STAGE 2d
# cemented with a tofu-slot marker (internal/live/discovery/count.go), on top
# of the tofu-address/tofu-estate every stamped resource carries. $PLAIN's
# own state knows none of the three, so its replan proposes removing them
# from the VPC's tags in addition to reverting the Name-tag mutation - marker
# noise, not the out-of-band mutation under test, and exactly the "marker
# tags normalised out of both plans" the stage's oracle text calls for.
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=drift_reconverge
log "=== STAGE 5: drift and reconverge ==="

VPC_NAME_TAG="${ESTATE_NAME}-vpc"
IGW_NAME_TAG="${ESTATE_NAME}-igw"

# changed_addrs_excluding_markers: reads a `plan -no-color` transcript on
# stdin, prints one changed resource address per line, EXCLUDING any address
# whose only proposed change is the tofu-address/tofu-estate/tofu-slot marker
# tags. The stock oracle below plans against infra that choudoufu's own
# migrate and converge steps (stages 2 and 2d) already tagged for real,
# through the AWS API - stock's own state knows nothing about those tags, so
# its replan proposes removing them from every one of the 16 resources
# choudoufu stamped, which is marker noise, not the out-of-band mutation
# under test. Same rule as live/e2e/corpus-lambda-simple/run.sh's and
# live/e2e/corpus-xancloud-iac/run.sh's own stage 5, with tofu-slot added to
# the marker-key set (this estate's drift target is one of the count-toggled
# resources STAGE 2d cemented a tofu-slot onto), plus two rules
# corpus-xancloud-iac's own stage 5 already established for this exact
# shape: a data source with a pending-change dependency is excluded outright
# ("will be read during apply" - it holds no live state of its own to
# drift), and any OTHER top-level attribute whose own diff resolves to
# "(known after apply)" is propagated uncertainty from elsewhere in the
# graph, not a concrete drift on the object itself.
#
# This estate needs one rule beyond either precedent: EXCLUDE_ADDRS, a fixed
# skip list for the ONE address this crossing already knows is permanently
# out of step between $PLAIN and $ESTATE for a reason that has nothing to do
# with drift. module.overture_tiles.aws_cloudfront_distribution.tiles[0] is
# the single live distribution both copies share (STAGE 2's live-import
# bound it), but its own origin.origin_access_control_id has pointed at
# TWO DIFFERENT live OACs since STAGE 2d: $PLAIN's config still names its
# own cold-deploy OAC (the orphan #249 leaves behind), while $ESTATE's own
# apply already re-pointed the live distribution at the OAC IT created and
# bound. That is a real, concrete, CONCRETE-valued difference (both OAC ids
# are known, not "known after apply"), so the propagation rule above cannot
# safely exclude it - and it is not the mutation stage 5 is testing, so this
# unit does not want it counted either way. It is #249's own already-ruled
# gap surfacing in a new comparison, not a new one: excluding it here is a
# test-harness scoping decision (which address this comparison is ABOUT),
# not a change to how choudoufu resolves or admits any type.
FILTER_MARKERS_PY="$WORK/filter_changed_addrs.py"
cat > "$FILTER_MARKERS_PY" <<'PY'
# Reads a `plan -no-color` transcript on stdin, prints one changed resource
# address per line, EXCLUDING any address whose only proposed change is the
# tofu-address/tofu-estate/tofu-slot marker tags, propagated
# "(known after apply)" uncertainty, or the one already-known #249 OAC
# divergence named in EXCLUDE_ADDRS. A file, not a `python3 - <<PY` heredoc:
# the latter feeds the script itself to python3's stdin, leaving nothing
# left on stdin for sys.stdin.read() below to read.
import re, sys

text = sys.stdin.read()
lines = text.split("\n")
header_re = re.compile(r'^  # (\S+) will be (.+)$')
headers = [(i, m.group(1), m.group(2)) for i, line in enumerate(lines) for m in [header_re.match(line)] if m]

MARKER_KEYS = ("tofu-address", "tofu-estate", "tofu-slot")
EXCLUDE_ADDRS = {"module.overture_tiles.aws_cloudfront_distribution.tiles[0]"}
ATTR_RE = re.compile(r'^      [~+-] ')
PURE_CLOSE_RE = re.compile(r'^\s*[)}\]]+,?\s*$')
COMMENT_RE = re.compile(r'^\s*#')

changed = []
for idx, (i, addr, verb) in enumerate(headers):
    end = headers[idx + 1][0] if idx + 1 < len(headers) else len(lines)
    if addr in EXCLUDE_ADDRS:
        continue
    if verb.startswith("read during apply"):
        # A data source has no live state of its own to drift; this fires
        # purely because it depends on a resource with a pending change
        # (here, always marker-tag noise on the resource it reads).
        continue
    block = lines[i:end]

    # Group into top-level attribute diffs: OpenTofu's plan renderer indents
    # a resource's own direct attributes exactly 6 spaces, so a line at that
    # indent starting a change is a new attribute's own diff. Its OWN extent
    # is found by bracket balance, not "until the next 6-space line": a
    # scalar attribute (no bracket characters) is exactly its one line, while
    # a list/map/jsonencode value's own lines keep the group open until its
    # own opening brackets close back to zero. Stopping at the next 6-space
    # line instead would occasionally pull in an UNRELATED unchanged sibling
    # attribute's own context line - OpenTofu prints one of those with no
    # symbol whenever it sits between two attributes that ARE shown, rather
    # than folding it into "N unchanged attributes hidden" - which would
    # then wrongly become this group's own "last substantive line" and hide
    # a real, concrete change behind it (caught on aws_launch_template.
    # batch[0]'s latest_version, whose one line was followed by exactly such
    # a "name = ..." context line here).
    attr_starts = [j for j, l in enumerate(block) if ATTR_RE.match(l)]
    def bracket_delta(s):
        return sum(s.count(c) for c in "({[") - sum(s.count(c) for c in ")}]")
    groups = []
    for s in attr_starts:
        depth = 0
        j = s
        while True:
            depth += bracket_delta(block[j])
            j += 1
            if depth <= 0 or j >= len(block):
                break
        groups.append(block[s:j])

    real_change = False
    for group in groups:
        head = group[0].strip()
        m = re.match(r'^[~+-]\s*(\S+)', head)
        attr_name = m.group(1) if m else ""
        if attr_name in ("tags", "tags_all"):
            # Marker-only churn inside a tags map is expected noise on every
            # tagged resource in the stock oracle; any OTHER key changing is
            # a real change.
            for line in group[1:]:
                stripped = line.strip()
                if not stripped or not re.match(r'^[~+-]', stripped):
                    continue
                if any(k in stripped for k in MARKER_KEYS):
                    continue
                real_change = True
            continue
        # Any other top-level attribute: if its own diff's last substantive
        # line (skipping bare closing punctuation and comments, which are
        # structure, not content) reads "(known after apply)", the
        # attribute's resolved value is UNKNOWN - propagated uncertainty,
        # never a concrete before/after drift on this object itself.
        # Otherwise it is a real, concrete change.
        substantive = [l for l in group if l.strip() and not COMMENT_RE.match(l) and not PURE_CLOSE_RE.match(l)]
        if substantive and "(known after apply)" in substantive[-1]:
            continue
        real_change = True

    if real_change:
        changed.append(addr)

print("\n".join(sorted(set(changed))))
PY
changed_addrs_excluding_markers() {
  python3 "$FILTER_MARKERS_PY"
}

# block_for_addr <addr>: reads a `plan -no-color` transcript on stdin, prints
# just the one resource's own diff block. Every taggable resource in this
# module carries a "Name" tag, so a flat grep for '"Name"' across the whole
# transcript would also match every OTHER changed resource's own unchanged
# Name value wherever its tags map is rendered at all - scoping to the one
# address under test is what keeps the comparison below about the actual
# mutation, not incidental formatting.
BLOCK_FOR_ADDR_PY="$WORK/block_for_addr.py"
cat > "$BLOCK_FOR_ADDR_PY" <<'PY'
import re, sys
addr = sys.argv[1]
lines = sys.stdin.read().split("\n")
header_re = re.compile(r'^  # (\S+) will be ')
starts = [i for i, l in enumerate(lines) if header_re.match(l)]
for idx, i in enumerate(starts):
    if header_re.match(lines[i]).group(1) == addr:
        end = starts[idx + 1] if idx + 1 < len(starts) else len(lines)
        print("\n".join(lines[i:end]))
        break
PY
block_for_addr() {
  python3 "$BLOCK_FOR_ADDR_PY" "$1"
}

log "--- 5a: mutate one live object out of band, directly via the AWS CLI ---"
if [ "${BREAK_STAGE5:-}" = "1" ]; then
  IGW_ID="$(awsl ec2 describe-internet-gateways \
    --filters "Name=tag:Name,Values=$IGW_NAME_TAG" \
    --query 'InternetGateways[0].InternetGatewayId' --output text)"
  [ -n "$IGW_ID" ] && [ "$IGW_ID" != "None" ] || fail "BREAK_STAGE5=1: no live internet gateway found by its Name tag"
  awsl ec2 create-tags --resources "$IGW_ID" --tags Key=Name,Value=tampered-by-BREAK >/dev/null
  log "  BREAK_STAGE5=1: also tampered $IGW_ID's Name tag - stage 5 must now see TWO"
  log "           drifted objects and fail the single-object assertion"
fi

VPC_ID="$(awsl ec2 describe-vpcs \
  --filters "Name=tag:Name,Values=$VPC_NAME_TAG" \
  --query 'Vpcs[0].VpcId' --output text)"
[ -n "$VPC_ID" ] && [ "$VPC_ID" != "None" ] || fail "no live VPC found by its Name tag ($VPC_NAME_TAG)"
awsl ec2 create-tags --resources "$VPC_ID" --tags Key=Name,Value=tampered-out-of-band >/dev/null
DRIFTED_VALUE="$(awsl ec2 describe-tags \
  --filters "Name=resource-id,Values=$VPC_ID" "Name=key,Values=Name" \
  --query "Tags[0].Value" --output text)"
[ "$DRIFTED_VALUE" = "tampered-out-of-band" ] || fail "the out-of-band tag mutation did not take"
log "  mutated $VPC_ID's Name tag to \"tampered-out-of-band\" (config says $VPC_NAME_TAG) directly via the AWS CLI - never through choudoufu"

log "--- 5b: choudoufu plan proposes fixing exactly that one object ---"
DRIFT_PLAN_OUT="$(cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color 2>&1)"; DRIFT_PLAN_RC=$?
[ "$DRIFT_PLAN_RC" -eq 0 ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | tail -60; fail "the drift-detection plan exited $DRIFT_PLAN_RC"; }
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "live-plan wrote a state file"

CHANGED_ADDRS="$(changed_addrs_excluding_markers <<< "$DRIFT_PLAN_OUT")"
N_CHANGED="$(printf '%s\n' "$CHANGED_ADDRS" | grep -c . || true)"

if [ "${BREAK_STAGE5:-}" = "1" ]; then
  [ "$N_CHANGED" = "1" ] \
    && fail "BREAK_STAGE5=1 set (two objects tampered), but choudoufu's plan proposes fixing only 1 - this assertion is not load-bearing"
  log "  BREAK_STAGE5=1: the plan proposes fixing $N_CHANGED objects, correctly more"
  log "           than one - the single-object assertion below is skipped"
else
  [ "$N_CHANGED" = "1" ] \
    || { printf '%s\n' "$DRIFT_PLAN_OUT" | grep -E '^  # .+ will be'; fail "expected exactly 1 object proposed for a fix, got $N_CHANGED"; }
  [ "$CHANGED_ADDRS" = "module.overture_tiles.aws_vpc.batch[0]" ] \
    || fail "choudoufu's plan proposes fixing $CHANGED_ADDRS, not module.overture_tiles.aws_vpc.batch[0]"
  log "  choudoufu's plan proposes fixing exactly one object: $CHANGED_ADDRS"

  log "--- 5c: the stock oracle: the identical mutation, plain tofu ---"
  # $PLAIN is still a plain-tofu working directory, pointed at the same floci
  # endpoint, with its own state file from stage 1's cold apply - zero
  # choudoufu involvement, and (per this block's header) the SAME live VPC,
  # since STAGE 2's live-import bound rather than recreated it.
  STOCK_DRIFT_PLAN_OUT="$(cd "$PLAIN" && tofu plan -input=false -no-color -detailed-exitcode 2>&1)"; STOCK_DRIFT_PLAN_RC=$?
  case "$STOCK_DRIFT_PLAN_RC" in
    0) fail "the stock oracle replans EMPTY after the same mutation - this control is not load-bearing" ;;
    2) ;;
    *) printf '%s\n' "$STOCK_DRIFT_PLAN_OUT" | tail -60; fail "the stock oracle's plan failed to run at all (exit $STOCK_DRIFT_PLAN_RC)" ;;
  esac
  STOCK_CHANGED_ADDRS="$(changed_addrs_excluding_markers <<< "$STOCK_DRIFT_PLAN_OUT")"
  STOCK_N_CHANGED="$(printf '%s\n' "$STOCK_CHANGED_ADDRS" | grep -c . || true)"
  [ "$STOCK_N_CHANGED" = "1" ] \
    || { printf '%s\n' "$STOCK_DRIFT_PLAN_OUT" | grep -E '^  # .+ will be'; fail "expected stock tofu's own plan to propose fixing exactly 1 object too, got $STOCK_N_CHANGED"; }
  [ "$STOCK_CHANGED_ADDRS" = "module.overture_tiles.aws_vpc.batch[0]" ] \
    || fail "stock tofu's plan proposes fixing $STOCK_CHANGED_ADDRS, not module.overture_tiles.aws_vpc.batch[0] - choudoufu and stock disagree about which object drifted"

  # The oracle comparison itself: the Name-tag diff line, choudoufu's against
  # stock's - the actual change under test, not incidental formatting. Both
  # plans read the same live tampered value off the same VPC and the same
  # target value off byte-identical configuration (module.overture_tiles's
  # own name_prefix), so a real agreement is not just "both saw a change."
  CHOUDOUFU_NAME_DIFF="$(block_for_addr 'module.overture_tiles.aws_vpc.batch[0]' <<< "$DRIFT_PLAN_OUT" | grep -E '"Name"' | sed -E 's/^[[:space:]]*[~+-]?[[:space:]]*//; s/[[:space:]]+/ /g' | sort -u)"
  STOCK_NAME_DIFF="$(block_for_addr 'module.overture_tiles.aws_vpc.batch[0]' <<< "$STOCK_DRIFT_PLAN_OUT" | grep -E '"Name"' | sed -E 's/^[[:space:]]*[~+-]?[[:space:]]*//; s/[[:space:]]+/ /g' | sort -u)"
  [ -n "$CHOUDOUFU_NAME_DIFF" ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | grep -B2 -A10 'will be updated'; fail "choudoufu's plan proposes fixing the object but names no Name-tag diff line"; }
  [ "$CHOUDOUFU_NAME_DIFF" = "$STOCK_NAME_DIFF" ] \
    || fail "choudoufu says \"$CHOUDOUFU_NAME_DIFF\", stock says \"$STOCK_NAME_DIFF\" - same object, different proposed change"
  log "  the stock oracle proposes fixing the identical object with the identical change: $CHOUDOUFU_NAME_DIFF"

  log "--- 5d: apply the reconverging plan; the drift is gone ---"
  RECONVERGE_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; RECONVERGE_RC=$?
  [ "$RECONVERGE_RC" -eq 0 ] || { printf '%s\n' "$RECONVERGE_OUT" | tail -60; fail "the reconverge apply failed"; }
  [ ! -f "$ESTATE/terraform.tfstate" ] || fail "the reconverge apply left a state file behind"
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$RECONVERGE_OUT" \
    || { grep -E 'Apply complete' <<< "$RECONVERGE_OUT"; fail "the reconverge apply did not change exactly 1 resource"; }
  FIXED_VALUE="$(awsl ec2 describe-tags \
    --filters "Name=resource-id,Values=$VPC_ID" "Name=key,Values=Name" \
    --query "Tags[0].Value" --output text)"
  [ "$FIXED_VALUE" = "$VPC_NAME_TAG" ] \
    || fail "the VPC's Name tag is \"$FIXED_VALUE\" after reconverging, not $VPC_NAME_TAG"
  log "  reconverged: $VPC_ID's Name tag is back to \"$VPC_NAME_TAG\", read via the AWS CLI"

  log ""
  log "STAGE 5 (drift and reconverge): PASS"
  log ""
  gauntlet_stage drift_reconverge pass "one object tampered (VPC Name tag), exactly module.overture_tiles.aws_vpc.batch[0] proposed by both choudoufu and stock with the identical change, apply changed 1 and the Name tag reads back as configured"
fi
CURRENT_STAGE=""

gauntlet_end

log ""
log "=== SUMMARY: stage 1 PASS; stage 2 PASS; stage 3 PASS (empty plan, S3 bucket"
log "=== and OAC identities verified by value); stage 4 PASS (no-op apply, object"
log "=== count and identities unchanged); stage 5 PASS (VPC Name-tag drift"
log "=== detected and reconverged, verified against the stock oracle) ==="
