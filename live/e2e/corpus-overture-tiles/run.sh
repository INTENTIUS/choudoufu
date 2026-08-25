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

# ══════════════════════════════════════════════════════════════════════════
# GREENFIELD (greenfield, live/GAUNTLET.md #13, active)
# ══════════════════════════════════════════════════════════════════════════
#
# Two more, fresh containers, entirely independent of everything above and
# below: choudoufu applies the SAME real, unmodified module directly from a
# live block - no live-import, no migration, no state file ever existing -
# and its stock oracle applies the identical module fresh in its own
# namespace. Reuses copy_module/write_root exactly as PLAIN/ESTATE do
# (skip_requesting_account_id=false on the greenfield side, for the same
# #345 reason the header documents), so both copies are, byte for byte,
# the same real module this whole script already diffs against DELTA.
CURRENT_STAGE=greenfield
FLOCI_GREEN_NAME="choudoufu-corpus-overture-tiles-green-$$"
FLOCI_ORACLE_NAME="choudoufu-corpus-overture-tiles-green-oracle-$$"
GREEN_ESTATE_NAME="overture-tiles-green" # kept <= ESTATE_NAME's own length: name_prefix's own 38-char cap (aws_iam_role.ecs_instance's "-ecs-instance-" suffix is the longest) already fits ESTATE_NAME exactly
GREEN_BUCKET_NAME="${GREEN_ESTATE_NAME}-tiles"

# floci_launch_retry <name> <portvar> - several gauntlet scripts run
# concurrently on a shared host, each with its own FLOCI_PORT reservation,
# but a fixed offset from that reservation is not itself reserved and
# collides with a sibling picking the same offset. Pick a port at random
# from a wide, rarely-used range and retry on "already allocated" instead.
floci_launch_retry() {
  local name="$1" portvar="$2" tries=0 port out
  while :; do
    port=$((20000 + RANDOM % 20000))
    out="$(docker run -d --rm -p "${port}:4566" --name "$name" "$FLOCI_IMAGE" 2>&1)" && { eval "$portvar=$port"; return 0; }
    tries=$((tries + 1))
    grep -qF 'port is already allocated' <<< "$out" || { printf '%s\n' "$out"; return 1; }
    [ "$tries" -ge 10 ] && { printf '%s\n' "$out"; return 1; }
  done
}

log "=== GREENFIELD: 0. two more floci containers ==="
floci_launch_retry "$FLOCI_GREEN_NAME" FLOCI_GREEN_PORT || fail "docker run for $FLOCI_GREEN_NAME failed"
floci_launch_retry "$FLOCI_ORACLE_NAME" FLOCI_ORACLE_PORT || fail "docker run for $FLOCI_ORACLE_NAME failed"
GREEN_ENDPOINT="http://localhost.floci.io:${FLOCI_GREEN_PORT}"
ORACLE_ENDPOINT="http://localhost.floci.io:${FLOCI_ORACLE_PORT}"
for gep in "$GREEN_ENDPOINT" "$ORACLE_ENDPOINT"; do
  GH=""
  for _ in $(seq 1 45); do
    GH="$(curl -fs "${gep}/_localstack/health" 2>/dev/null)" || true
    grep -q '"s3"' <<< "${GH:-}" && break
    sleep 2
  done
  grep -q '"s3"' <<< "${GH:-}" || fail "floci did not come up healthy (s3) at $gep"
done
log "  healthy: greenfield=$GREEN_ENDPOINT oracle=$ORACLE_ENDPOINT"

GREEN="$WORK/green"
ORACLE_G="$WORK/green-oracle"
copy_module "$GREEN"
copy_module "$ORACLE_G"

# write_root's own module block hardcodes ESTATE_NAME/BUCKET_NAME via
# closure over the SAME shell variables the rest of this script uses;
# temporarily point them at the greenfield names for these two calls only.
_SAVED_ESTATE_NAME="$ESTATE_NAME"; _SAVED_BUCKET_NAME="$BUCKET_NAME"
ESTATE_NAME="$GREEN_ESTATE_NAME"; BUCKET_NAME="$GREEN_BUCKET_NAME"
write_root "$GREEN" '

  live {
    estate = "'"$GREEN_ESTATE_NAME"'"
    record_store "local" {
      path = ".tofu-records"
    }
  }' false
write_root "$ORACLE_G" "" true
ESTATE_NAME="$_SAVED_ESTATE_NAME"; BUCKET_NAME="$_SAVED_BUCKET_NAME"

[ -f "$PLAIN/.terraform.lock.hcl" ] && cp "$PLAIN/.terraform.lock.hcl" "$GREEN/.terraform.lock.hcl"
[ -f "$PLAIN/.terraform.lock.hcl" ] && cp "$PLAIN/.terraform.lock.hcl" "$ORACLE_G/.terraform.lock.hcl"

log "=== GREENFIELD: 1. choudoufu apply from nothing, no migration ==="
( cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the greenfield init failed"; }
GREEN_APPLY_OUT="$(cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$GREEN_APPLY_OUT" | grep -E '^Error' -A5 | head -60; fail "the greenfield apply failed"; }
grep -qE 'Apply complete! Resources: 26 added' <<< "$GREEN_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT"; fail "the greenfield apply did not create exactly 26 resources"; }
log "  $(grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT" | head -1)"

awsg() { aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" "$@"; }

log "=== GREENFIELD: 2. markers, read through the AWS CLI directly ==="
GOT_G_BUCKET_ADDR="$(awsg s3api get-bucket-tagging --bucket "$GREEN_BUCKET_NAME" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_G_BUCKET_ADDR" = "module.overture_tiles.aws_s3_bucket.tiles:0" ] || fail "the greenfield S3 bucket carries tofu-address=$GOT_G_BUCKET_ADDR, not module.overture_tiles.aws_s3_bucket.tiles:0"
GOT_G_BUCKET_ESTATE="$(awsg s3api get-bucket-tagging --bucket "$GREEN_BUCKET_NAME" --query "TagSet[?Key=='tofu-estate'].Value | [0]" --output text)"
[ "$GOT_G_BUCKET_ESTATE" = "$GREEN_ESTATE_NAME" ] || fail "the greenfield S3 bucket carries tofu-estate=$GOT_G_BUCKET_ESTATE, not $GREEN_ESTATE_NAME"
GREEN_QUEUE_ARN="$(awsg batch describe-job-queues --query "jobQueues[?jobQueueName=='${GREEN_ESTATE_NAME}-queue'].jobQueueArn | [0]" --output text)"
[ -n "$GREEN_QUEUE_ARN" ] && [ "$GREEN_QUEUE_ARN" != "None" ] || fail "no job queue named ${GREEN_ESTATE_NAME}-queue came back from the greenfield floci"
GOT_G_QUEUE_ADDR="$(awsg batch list-tags-for-resource --resource-arn "$GREEN_QUEUE_ARN" --query 'tags."tofu-address"' --output text)"
[ "$GOT_G_QUEUE_ADDR" = "module.overture_tiles.aws_batch_job_queue.tiles" ] \
  || fail "the greenfield Batch job queue carries tofu-address=$GOT_G_QUEUE_ADDR, not module.overture_tiles.aws_batch_job_queue.tiles"
log "  bucket and batch job queue carry their expected tofu-address/tofu-estate markers - read via the AWS CLI, not choudoufu's own report"

log "=== GREENFIELD: 3. the local record store holds at least one record per taggable instance (#364 A2) ==="
GREEN_RECORD_FILES="$(find "$GREEN/.tofu-records/tofu-records" -type f ! -name '*.lock' ! -name '*.tmp-*' 2>/dev/null | wc -l | tr -d ' ')"
[ "$GREEN_RECORD_FILES" -gt 0 ] || fail "expected at least one record under the local record store after the greenfield apply, found none"
log "  $GREEN_RECORD_FILES records persisted under the local record store"

log "=== GREENFIELD: 4. the next plan proposes nothing ==="
GREEN_PLAN_OUT="$(cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; GREEN_PLAN_RC=$?
[ "$GREEN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$GREEN_PLAN_OUT" | tail -30; fail "the greenfield replan exited $GREEN_PLAN_RC"; }
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$GREEN_PLAN_OUT" \
  || { grep -E '^  #' <<< "$GREEN_PLAN_OUT"; fail "the greenfield replan is not empty"; }
log "  No changes."

log "=== GREENFIELD: 5. stock oracle - the identical module applied fresh in its own namespace ==="
( cd "$ORACLE_G" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ORACLE_G" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the greenfield oracle's init failed"; }
ORACLE_G_APPLY_OUT="$(cd "$ORACLE_G" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" tofu apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$ORACLE_G_APPLY_OUT" | tail -40; fail "the greenfield oracle apply failed"; }
grep -qE 'Apply complete! Resources: 26 added' <<< "$ORACLE_G_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$ORACLE_G_APPLY_OUT"; fail "the greenfield oracle apply did not create exactly 26 resources"; }
log "  $(grep -E 'Apply complete' <<< "$ORACLE_G_APPLY_OUT" | head -1)"

awso() { aws --endpoint-url "$ORACLE_ENDPOINT" --region "$REGION" "$@"; }

log "=== GREENFIELD: 6. object-by-object comparison, via the AWS CLI on both endpoints, tags normalised out ==="
GREEN_QUEUE_STATE="$(awsg batch describe-job-queues --job-queues "$GREEN_QUEUE_ARN" --query 'jobQueues[0].state' --output text)"
ORACLE_QUEUE_ARN="$(awso batch describe-job-queues --query "jobQueues[?jobQueueName=='${GREEN_ESTATE_NAME}-queue'].jobQueueArn | [0]" --output text)"
[ -n "$ORACLE_QUEUE_ARN" ] && [ "$ORACLE_QUEUE_ARN" != "None" ] || fail "no job queue named ${GREEN_ESTATE_NAME}-queue came back from the oracle floci"
ORACLE_QUEUE_STATE="$(awso batch describe-job-queues --job-queues "$ORACLE_QUEUE_ARN" --query 'jobQueues[0].state' --output text)"
[ "$GREEN_QUEUE_STATE" = "$ORACLE_QUEUE_STATE" ] || fail "the Batch job queue's state differs: greenfield=$GREEN_QUEUE_STATE oracle=$ORACLE_QUEUE_STATE"

GREEN_DIST_COMMENT="$(awsg cloudfront list-distributions --query "DistributionList.Items[0].Comment" --output text)"
ORACLE_DIST_COMMENT="$(awso cloudfront list-distributions --query "DistributionList.Items[0].Comment" --output text)"
[ -n "$GREEN_DIST_COMMENT" ] && [ "$GREEN_DIST_COMMENT" != "None" ] || fail "no CloudFront distribution came back from the greenfield floci"
[ "$GREEN_DIST_COMMENT" = "$ORACLE_DIST_COMMENT" ] || fail "the CloudFront distribution's comment differs: greenfield=$GREEN_DIST_COMMENT oracle=$ORACLE_DIST_COMMENT"

GREEN_BUCKET_COUNT="$(awsg s3api list-buckets --query 'length(Buckets)' --output text)"
ORACLE_BUCKET_COUNT="$(awso s3api list-buckets --query 'length(Buckets)' --output text)"
[ "$GREEN_BUCKET_COUNT" = "$ORACLE_BUCKET_COUNT" ] || fail "bucket count differs: greenfield=$GREEN_BUCKET_COUNT oracle=$ORACLE_BUCKET_COUNT"

log "  Batch job queue state, CloudFront distribution comment and bucket count all match between choudoufu's greenfield apply and stock's cold deploy in its own namespace"
gauntlet_stage greenfield pass "26 resources from nothing, bucket and batch job queue markers verified via the AWS CLI, $GREEN_RECORD_FILES records in the local record store (#364 A2), replan empty, stock oracle in its own namespace matches structurally (batch job queue state, CloudFront distribution comment, bucket count)"
CURRENT_STAGE=""

docker rm -f "$FLOCI_GREEN_NAME" "$FLOCI_ORACLE_NAME" >/dev/null 2>&1 || true


# ══════════════════════════════════════════════════════════════════════════
# PART D-ORACLE: RENAME, stock (day2_rename, active - live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
#
# module.overture_tiles is this estate's only module call and carries every
# one of its 26 resources, so - like corpus-lambda-simple's single-module
# estate - both day2_rename mechanisms run on the SAME module, one after the
# other, rather than on two different objects. Unlike lambda-simple's seven
# per-child moved blocks, this rename uses ONE module-level moved block:
# there are no record-located children here needing their own blocks (this
# estate records nothing - "0 newly recorded" at stage 2), so the cycle
# lambda-simple's header describes (a module-level block colliding with
# explicit per-resource blocks in the same plan) does not arise, and a bare
# `moved { from = module.overture_tiles to = module.overture_tiles_X }`
# covers every one of its 26 children - the 16 taggable ones AND the 9
# untaggable/config-derived ones - in a real Terraform plan. Confirmed
# empirically below, not assumed.
#
# The oracle below is a THIRD copy, built the same way $PLAIN was
# (copy_module + write_root, skip_account_id=true, no live block) with
# $PLAIN's own state copied in - not $PLAIN itself, which stage 5 later
# reuses for its own drift oracle. #249's known OAC-orphan gap is a
# consequence of comparing $PLAIN against $ESTATE (two separate live
# objects); it does not arise here, where the oracle only ever compares
# $PLAIN's own state against itself.
#
# BREAK=2 (not 1: this crossing already uses BREAK=1 for stage 2's own
# marker assertion) exercises this stage's own break control instead of the
# real checks: renaming module.overture_tiles WITHOUT a moved block.
CURRENT_STAGE=day2_rename
log "=== D-ORACLE. stock: the net module rename, through one moved block, on cold_deploy's own state ==="
ORACLE="$WORK/oracle"
copy_module "$ORACLE"
write_root "$ORACLE" "" true
[ -f "$PLAIN/.terraform.lock.hcl" ] && cp "$PLAIN/.terraform.lock.hcl" "$ORACLE/.terraform.lock.hcl"
cp "$PLAIN/terraform.tfstate" "$ORACLE/terraform.tfstate"
( cd "$ORACLE" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ORACLE" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_rename stock oracle's init failed"; }
BASELINE_PLAN_OUT="$(cd "$ORACLE" && tofu plan -input=false -no-color 2>&1)"; BASELINE_PLAN_RC=$?
[ "$BASELINE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BASELINE_PLAN_OUT" | tail -40; fail "the day2_rename stock oracle's baseline (no-rename) plan exited $BASELINE_PLAN_RC"; }
grep -qE '^  # .+ will be' <<< "$BASELINE_PLAN_OUT" \
  && { printf '%s\n' "$BASELINE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the baseline (no-rename) oracle plan is not clean - this estate has drifted since the baseline was last measured"; }
log "  baseline (no rename): clean, confirmed BEFORE the rename below"

sed -i.bak 's/module "overture_tiles" {/module "overture_tiles_final" {/' "$ORACLE/main.tf"
rm -f "$ORACLE/main.tf.bak"
cat >> "$ORACLE/main.tf" <<'EOF'

moved {
  from = module.overture_tiles
  to   = module.overture_tiles_final
}
EOF
( cd "$ORACLE" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ORACLE" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_rename stock oracle's reinit failed"; }
ORACLE_PLAN_OUT="$(cd "$ORACLE" && tofu plan -input=false -no-color 2>&1)"; ORACLE_PLAN_RC=$?
[ "$ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -40; fail "the day2_rename stock oracle plan exited $ORACLE_PLAN_RC"; }
grep -qE '^  # .+ will be (destroyed|created)' <<< "$ORACLE_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock proposes a destroy or create for a rename carried entirely by ONE module-level moved block - the oracle itself is not zero-churn"; }
grep -qE '^  # .+ will be updated' <<< "$ORACLE_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock proposes an in-place update for a rename that should be pure address bookkeeping under stock (stock writes no marker tags of its own)"; }
log "  stock: zero churn on cold_deploy's own state - one module-level moved block covers every one of this module's 26 children, taggable and untaggable alike, no attribute diff at all"

# day2_remove's stock oracle (live/GAUNTLET.md #7), computed here on a
# FOURTH copy of cold_deploy's own state, before migrate/rename/drift ever
# touch a live tag - same reason D-ORACLE above runs early. create_
# cloudfront_distribution=false count-gates BOTH aws_cloudfront_origin_
# access_control.tiles and aws_cloudfront_distribution.tiles to 0; picked
# because it is the module's own toggle, self-contained (nothing else in
# the module reads either resource), and this estate is one module call
# carrying all 26 resources - there is no small, standalone resource
# block to remove the way reference-ec2-vpc and corpus-iam-policy each do.
CURRENT_STAGE=day2_remove
log "=== REMOVE-ORACLE. stock: create_cloudfront_distribution=false, on cold_deploy's own state ==="
REMOVE_ORACLE="$WORK/remove-oracle"
copy_module "$REMOVE_ORACLE"
write_root "$REMOVE_ORACLE" "" true
perl -pi -e 's/create_cloudfront_distribution = true/create_cloudfront_distribution = false/' "$REMOVE_ORACLE/main.tf"
grep -q 'create_cloudfront_distribution = false' "$REMOVE_ORACLE/main.tf" || fail "REMOVE-ORACLE: the create_cloudfront_distribution edit did not match - the corpus pin has moved"
[ -f "$PLAIN/.terraform.lock.hcl" ] && cp "$PLAIN/.terraform.lock.hcl" "$REMOVE_ORACLE/.terraform.lock.hcl"
cp "$PLAIN/terraform.tfstate" "$REMOVE_ORACLE/terraform.tfstate"
( cd "$REMOVE_ORACLE" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$REMOVE_ORACLE" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_remove stock oracle's init failed"; }
REMOVE_ORACLE_PLAN_OUT="$(cd "$REMOVE_ORACLE" && tofu plan -input=false -no-color 2>&1)"; REMOVE_ORACLE_PLAN_RC=$?
[ "$REMOVE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_remove stock oracle plan exited $REMOVE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.overture_tiles\.aws_cloudfront_distribution\.tiles\[0\] will be destroyed' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { grep -E '^  # .+ will be' <<< "$REMOVE_ORACLE_PLAN_OUT"; fail "stock's own oracle does not propose destroying module.overture_tiles.aws_cloudfront_distribution.tiles[0]"; }
grep -qE '^  # module\.overture_tiles\.aws_cloudfront_origin_access_control\.tiles\[0\] will be destroyed' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { grep -E '^  # .+ will be' <<< "$REMOVE_ORACLE_PLAN_OUT"; fail "stock's own oracle does not propose destroying module.overture_tiles.aws_cloudfront_origin_access_control.tiles[0]"; }
# s3.tf's own bucket policy document has a dynamic CloudFrontOAC statement
# keyed on aws_cloudfront_distribution.tiles[0].arn - real ripple, not
# #404's shape (this is the bucket's OWN policy losing a statement whose
# condition names the object actually being removed, not a SIBLING's
# policy corrupted by an unrelated rename), and an ordinary in-place
# update to a still-declared resource, not an orphan-sweep question at
# all - so it does not implicate issue #410 below.
grep -qE '^  # module\.overture_tiles\.aws_s3_bucket_policy\.tiles\[0\] will be updated in-place' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { grep -E '^  # .+ will be' <<< "$REMOVE_ORACLE_PLAN_OUT"; fail "stock's own oracle does not propose updating module.overture_tiles.aws_s3_bucket_policy.tiles[0] (the CloudFrontOAC statement should drop from its policy JSON)"; }
grep -qF 'Plan: 0 to add, 1 to change, 2 to destroy.' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { grep -E '^Plan:|^No changes' <<< "$REMOVE_ORACLE_PLAN_OUT"; fail "the day2_remove stock oracle plan is not exactly two destroys plus the bucket policy update"; }
log "  stock oracle: exactly two destroys proposed (the CloudFront distribution and its OAC) plus one in-place bucket-policy update (the CloudFrontOAC statement drops) - computed now, before anything below writes a live tag"
CURRENT_STAGE=""

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
# #249's own gap has narrowed since this script was last verified (found by
# running, not assumed): aws_cloudfront_origin_access_control now reads
# UNTAGGABLE ("has no tags argument in the provider's schema"), not
# UNADMITTED_TYPE - the type is admitted, it is simply untaggable, the same
# honest bucket every other tagless AWS type in this estate already falls
# into (aws_route_table_association, aws_iam_role_policy, and so on). The
# TOTAL not-eligible count does not move (10 either way: 9+1 before, 10+0
# now), so #601's stamped/skipped line below is unaffected; only the
# UNTAGGABLE/UNADMITTED_TYPE split does. This is the schema-first admission
# work HANDOFF's "The order" item 2 describes reaching one more type, not a
# regression - the assertion here was stale, not the estate.
grep -qF "UNTAGGABLE (10)" <<< "$IMPORT_OUT" || fail "expected exactly 10 UNTAGGABLE resources"
grep -qE '^UNADMITTED_TYPE \(' <<< "$IMPORT_OUT" \
  && { grep -E '^UNADMITTED_TYPE' <<< "$IMPORT_OUT"; fail "expected 0 UNADMITTED_TYPE resources - #249's own gap has widened again"; }
grep -qF "module.overture_tiles.aws_cloudfront_origin_access_control.tiles[0]" <<< "$IMPORT_OUT" \
  || fail "expected aws_cloudfront_origin_access_control.tiles[0] to appear (now UNTAGGABLE) - already ruled 'enumerable, unbindable' by #249, not a new gap"
log "  16 of 26 eligible (10 VERIFIED, 6 DRIFTED); 10 UNTAGGABLE (aws_cloudfront_origin_access_control among them, now correctly UNTAGGABLE rather than UNADMITTED_TYPE - #249 narrowed); 0 UNADMITTED_TYPE"

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

# UPDATE, found by running this script fresh rather than assumed (the same
# #249 narrowing STAGE 2's own assertions above now account for):
# aws_cloudfront_origin_access_control moving from UNADMITTED_TYPE to
# UNTAGGABLE is not merely a classification-label change. UNTAGGABLE still
# means "no tags argument in the schema", but it now also means live-import's
# own dry-run VERIFIES the type against the live system by a DERIVED
# identity (its own deterministic `name`, unaffected by module path) even
# though it cannot stamp a marker on it - visible directly above, in STAGE
# 2's own dry-run output ("module.overture_tiles.aws_cloudfront_origin_
# access_control.tiles[0] ... live id: <the OAC's own id>"). That is the
# SAME live object $PLAIN's cold deploy created; #249's own "orphan a second
# OAC" consequence is gone for this type. So the plan below is now
# genuinely EMPTY before any apply at all - there is nothing left for an
# apply to converge, and the "converge" name is now aspirational for this
# estate rather than literal. Kept as a real, asserted no-op apply anyway
# (an empty plan alone is not enough - HANDOFF - and this is where the
# DIST_ID/GOT_OAC_ID identity used by every later stage is established).
PRECONVERGE_OUT="$(cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color 2>&1)"; PRECONVERGE_RC=$?
[ "$PRECONVERGE_RC" -eq 0 ] \
  || { printf '%s\n' "$PRECONVERGE_OUT" | tail -60; fail "the pre-convergence live-plan exited $PRECONVERGE_RC - the #345 identity-ARN wall may have come back, or a new one blocks projection"; }
grep -qE 'No changes\.' <<< "$PRECONVERGE_OUT" \
  || { grep -E '^Plan: |^No changes' <<< "$PRECONVERGE_OUT"; grep -E '^  # ' <<< "$PRECONVERGE_OUT"; fail "expected the pre-convergence live-plan to already be empty (#249 narrowed - see the UPDATE above) - the module's own shape, or one of the gaps this script documents, has moved"; }
log "  pre-convergence live-plan: No changes. - #249's own orphan-OAC consequence is gone for this type (UNTAGGABLE, not UNADMITTED_TYPE - see the UPDATE above); nothing left to converge"
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "live-plan wrote a state file"

CONVERGE_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; CONVERGE_RC=$?
[ "$CONVERGE_RC" -eq 0 ] || { printf '%s\n' "$CONVERGE_OUT" | tail -40; fail "the convergence apply failed"; }
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "the convergence apply wrote a state file"
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed|No changes' <<< "$CONVERGE_OUT" \
  || { grep -E '^Apply complete|^No changes' <<< "$CONVERGE_OUT"; fail "the convergence apply was not a genuine no-op"; }
log "  $(grep -E '^Apply complete|^No changes' <<< "$CONVERGE_OUT") (genuine no-op - nothing left to converge)"

# The OAC's identity, BY VALUE - not just "the plan came back empty". HANDOFF:
# an empty plan alone is not enough; a wrong identity can converge just as
# quietly as a right one. Read the distribution's own origin FIRST, not a
# name-based OAC lookup, exactly as before #249 narrowed - only the
# commentary below changed, not the read.
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
[ "$OAC_NAME_COUNT" = "1" ] \
  || fail "$OAC_NAME_COUNT origin access controls share the name $WANT_OAC_NAME, expected exactly 1 - #249's own orphan-OAC consequence may have come back for this type"
log "  OAC referenced by the distribution: $GOT_OAC_ID, named $GOT_OAC_NAME as expected (exactly 1 total shares that name - PLAIN's cold-deploy OAC and ESTATE's bound OAC are the SAME live object, #249's own orphan consequence gone for this type)"

REPLAN_OUT="$(cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color 2>&1)"; REPLAN_RC=$?
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "the post-convergence live-plan wrote a state file"
[ "$REPLAN_RC" -eq 0 ] \
  || { printf '%s\n' "$REPLAN_OUT" | tail -60; fail "the post-convergence live-plan exited $REPLAN_RC - convergence did not settle the estate"; }
grep -qE 'No changes\.' <<< "$REPLAN_OUT" \
  || { grep -E '^Plan: |^No changes' <<< "$REPLAN_OUT"; fail "the post-convergence live-plan is not empty - convergence did not settle the estate"; }
log "  post-convergence live-plan: No changes. - the estate has reached steady state"
log ""
log "STAGE 2d (converge): done - tofu-slot cemented at migrate time (#372), OAC already bound at migrate time (#249 narrowed), steady state reached with no apply needed"
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
        if attr_name == "revoke_rules_on_delete":
            # A tofu-side-only behaviour flag EC2 never stores (live/e2e/
            # run.sh's own DRIFT_UNSERVED list, live/LIMITATIONS.md #328,
            # corpus-vpc-complete's own day2 notes on this exact "+
            # revoke_rules_on_delete = false" line): every stateless replan
            # of an aws_security_group shows it, moved or not, because
            # there is no live value to compare against, only the
            # provider's own default. Not a real change on this or any
            # other estate.
            continue
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

  # ══════════════════════════════════════════════════════════════════════════
  # PART D: RENAME (day2_rename, active - live/GAUNTLET.md #6)
  # ══════════════════════════════════════════════════════════════════════════
  #
  # See the D-ORACLE comment above stage 2 for why both mechanisms run on the
  # SAME module. Unlike stock's one module-level block, choudoufu's own legs
  # need one moved block PER TAGGABLE child (16): each carries its own marker,
  # and only an explicit moved block tells choudoufu's stateless live-plan
  # which old tag address maps to which new declared one - the same finding
  # corpus-lambda-simple made for its own 3 taggable children, generalized to
  # 16. The 9 untaggable/config-derived children and the 1 UNADMITTED_TYPE OAC
  # (server-assigned identity, but re-derivable from its own deterministic
  # `name` once choudoufu's own apply has created it - unaffected by the
  # module's OWN address, same shape as an untaggable Route 53 record) need
  # none - confirmed empirically below.
  CURRENT_STAGE=day2_rename
  log ""
  log "=== D0. the estate's 16 taggable addresses this rename must not disturb ==="
  TAGGABLE_ADDRS=(
    'aws_vpc.batch[0]'
    'aws_internet_gateway.batch[0]'
    'aws_subnet.public[0]'
    'aws_route_table.public[0]'
    'aws_security_group.batch'
    'aws_launch_template.batch[0]'
    'aws_cloudwatch_log_group.batch'
    'aws_batch_job_definition.tiles["base"]'
    'aws_batch_compute_environment.tiles'
    'aws_batch_job_queue.tiles'
    'aws_iam_role.job'
    'aws_iam_role.execution'
    'aws_iam_role.ecs_instance'
    'aws_iam_instance_profile.ecs'
    'aws_s3_bucket.tiles[0]'
    'aws_cloudfront_distribution.tiles[0]'
  )
  BEFORE_RENAME_N="$(tagged_object_count)"
  [ "$BEFORE_RENAME_N" = "16" ] || fail "expected 16 tagged objects ahead of the rename, got $BEFORE_RENAME_N"
  log "  $BEFORE_RENAME_N tagged objects, read via resourcegroupstaggingapi"

  if [ "${BREAK:-}" = "2" ]; then
    log "=== D1 (BREAK=2). rename module.overture_tiles -> module.overture_tiles_final WITHOUT a moved block ==="
    sed -i.bak 's/module "overture_tiles" {/module "overture_tiles_final" {/' "$ESTATE/main.tf"
    rm -f "$ESTATE/main.tf.bak"
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the BREAK=2 rename's reinit failed"; }
    BREAK_PLAN_OUT="$(cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color 2>&1)"; BREAK_PLAN_RC=$?
    [ "$BREAK_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -30; fail "the BREAK=2 rename-without-moved plan exited $BREAK_PLAN_RC"; }
    # Verified directly (measured, not guessed - this is NOT the uniform
    # "create only, no destroy" stateless-replan shape iam-read-only-policy
    # and simpleinfra-dns show elsewhere in this batch): different
    # resources in THIS module resolve differently once nothing bridges
    # the rename. The VPC, subnet, security group and a few others show
    # NO churn at all here - their discovery finds the same live object by
    # its own deterministic Name tag regardless of module path, a fallback
    # this batch's other estates' resource types do not have. The client-
    # named ones with no such Name-tag fallback (the S3 bucket, the
    # CloudWatch log group - both named directly from a config value, not
    # from a Name tag) show the literal shape the stage's own Break text
    # names: the old address destroyed, the new address created. Spot-
    # checked on the S3 bucket, GAUNTLET.md's own literal words for this
    # stage's Break control.
    grep -qE '^  # module\.overture_tiles\.aws_s3_bucket\.tiles\[0\] will be destroyed' <<< "$BREAK_PLAN_OUT" \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be' | head -20; fail "BREAK=2: renaming without a moved block did not propose destroying module.overture_tiles.aws_s3_bucket.tiles[0] - this stage's check is not load-bearing"; }
    grep -qE '^  # module\.overture_tiles_final\.aws_s3_bucket\.tiles\[0\] will be created' <<< "$BREAK_PLAN_OUT" \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be' | head -20; fail "BREAK=2: renaming without a moved block did not propose creating module.overture_tiles_final.aws_s3_bucket.tiles[0] - this stage's check is not load-bearing"; }
    log "  BREAK=2: correctly proposes destroying module.overture_tiles.aws_s3_bucket.tiles[0] and creating module.overture_tiles_final.aws_s3_bucket.tiles[0] (spot-checked on the S3 bucket, the shape GAUNTLET.md's own Break text names) - the moved-block and live-mv checks below are skipped"
  else
    log "=== D1. choudoufu, moved block: module.overture_tiles -> module.overture_tiles_moved ==="
    sed -i.bak 's/module "overture_tiles" {/module "overture_tiles_moved" {/' "$ESTATE/main.tf"
    rm -f "$ESTATE/main.tf.bak"
    cat >> "$ESTATE/main.tf" <<'EOF'

moved {
  from = module.overture_tiles
  to   = module.overture_tiles_moved
}
EOF
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the moved-block rename's reinit failed"; }
    MOVED_PLAN_OUT="$(cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color 2>&1)"; MOVED_PLAN_RC=$?
    [ "$MOVED_PLAN_RC" -eq 0 ] || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -40; fail "the moved-block rename plan exited $MOVED_PLAN_RC"; }
    grep -qE '^  # .+ will be (destroyed|created)' <<< "$MOVED_PLAN_OUT" \
      && { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block rename proposes a destroy or a create - not zero churn"; }
    for addr in "${TAGGABLE_ADDRS[@]}"; do
      grep -qF "  # module.overture_tiles_moved.$addr will be updated in-place" <<< "$MOVED_PLAN_OUT" \
        || { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block plan does not propose an in-place update to module.overture_tiles_moved.$addr"; }
    done
    # Found by running, not guessed: TWO untaggable/config-derived children
    # ALSO show "will be updated in-place" here, beyond the sixteen taggable
    # marker rewrites - aws_iam_role_policy.execution_logs and
    # aws_s3_bucket_policy.tiles[0]. Both are inline JSON policies whose OWN
    # `policy` attribute embeds a SIBLING resource's arn (the CloudWatch log
    # group's arn, and the CloudFront distribution's arn respectively) -
    # siblings THIS SAME plan is also renaming. Verified below to be
    # PROPAGATED "(known after apply)" uncertainty, not a real content
    # change: each one's `policy` diff resolves to
    # "jsonencode(...) -> (known after apply)" and nothing else in the block
    # differs (no tags to rewrite either - they are untaggable). Neither
    # needs a moved block of its own: like every other untaggable/
    # config-derived child in this batch, their identity is re-derived
    # every plan from their own live parent (the role, the bucket), not
    # from any module-address state tracking.
    for addr in 'aws_iam_role_policy.execution_logs' 'aws_s3_bucket_policy.tiles[0]'; do
      grep -qF "  # module.overture_tiles_moved.$addr will be updated in-place" <<< "$MOVED_PLAN_OUT" \
        || { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block plan no longer shows the expected propagated-uncertainty update to module.overture_tiles_moved.$addr - re-check whether this is still the right shape"; }
      BLOCK="$(block_for_addr "module.overture_tiles_moved.$addr" <<< "$MOVED_PLAN_OUT")"
      grep -qE '^\s*\)\s*->\s*\(known after apply\)\s*$' <<< "$BLOCK" \
        || { printf '%s\n' "$BLOCK"; fail "module.overture_tiles_moved.$addr's diff is not the expected policy -> (known after apply) propagation - it may be a genuine content change"; }
    done
    REAL_CHANGES="$(changed_addrs_excluding_markers <<< "$MOVED_PLAN_OUT")"
    [ -z "$REAL_CHANGES" ] \
      || { printf '%s\n' "$REAL_CHANGES"; fail "the moved-block rename plan shows real, non-marker, non-propagated changes beyond the known eighteen"; }
    grep -qF 'Plan: 0 to add, 18 to change, 0 to destroy.' <<< "$MOVED_PLAN_OUT" \
      || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -10; fail "the moved-block rename plan is not exactly eighteen in-place changes - the sixteen taggable marker rewrites plus the two propagated-uncertainty updates"; }
    log "  choudoufu: zero churn, eighteen in-place updates - sixteen tag-marker rewrites plus two propagated (known after apply) policy recomputes on untaggable siblings, confirmed via the same stage-5 marker/propagation filter to hold no real content change"

    MOVED_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; MOVED_APPLY_RC=$?
    [ "$MOVED_APPLY_RC" -eq 0 ] || { printf '%s\n' "$MOVED_APPLY_OUT" | tail -40; fail "the moved-block rename apply exited $MOVED_APPLY_RC"; }
    # Sixteen, not eighteen: the two propagated "(known after apply)" policy
    # recomputes resolve, at apply time, to the SAME final value the live
    # object already holds (their sibling's arn does not actually change
    # value across a module rename, only its symbolic reference could not be
    # resolved at plan time) - OpenTofu's own apply-time refinement finds no
    # real difference and does not count them as changed. Confirmed by
    # running, not assumed: the plan legitimately shows 18, the apply
    # legitimately shows 16.
    grep -qE 'Resources: 0 added, 16 changed, 0 destroyed' <<< "$MOVED_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$MOVED_APPLY_OUT"; fail "the moved-block rename apply was not exactly sixteen in-place changes"; }

    AFTER_D1_N="$(tagged_object_count)"
    [ "$AFTER_D1_N" = "16" ] || fail "the tagged object count changed across the moved-block rename: 16 -> $AFTER_D1_N"
    GOT_BUCKET_ADDR_D1="$(awsl s3api get-bucket-tagging --bucket "$BUCKET_NAME" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
    [ "$GOT_BUCKET_ADDR_D1" = "module.overture_tiles_moved.aws_s3_bucket.tiles:0" ] \
      || fail "the S3 bucket carries tofu-address=$GOT_BUCKET_ADDR_D1 after the rename, not module.overture_tiles_moved.aws_s3_bucket.tiles:0"
    GOT_OAC_ID_D1="$(awsl cloudfront get-distribution-config --id "$DIST_ID" --query "DistributionConfig.Origins.Items[0].OriginAccessControlId" --output text)"
    [ "$GOT_OAC_ID_D1" = "$GOT_OAC_ID" ] \
      || fail "the CloudFront distribution's own OAC changed from $GOT_OAC_ID to $GOT_OAC_ID_D1 across the moved-block rename - the untaggable/config-derived OAC moved when it should not have"
    log "  $BUCKET_NAME's tofu-address now module.overture_tiles_moved.aws_s3_bucket.tiles:0; the distribution's OAC ($GOT_OAC_ID_D1) unchanged - read via the AWS CLI; $AFTER_D1_N tagged objects, unchanged"

    log "=== D2. choudoufu, live-mv: module.overture_tiles_moved -> module.overture_tiles_final ==="
    sed -i.bak 's/module "overture_tiles_moved" {/module "overture_tiles_final" {/' "$ESTATE/main.tf"
    rm -f "$ESTATE/main.tf.bak"
    # FOUND BY RUNNING, NOT GUESSED: most of the sixteen taggable children go
    # through live-mv with no moved block at all, exactly this leg's own
    # point. NO_LIVE_MV names the ones that genuinely cannot, each refused
    # by internal/live/mv/mv.go's own correct check (tested at
    # internal/live/mv/recordfallback_test.go): a server-/provider-assigned
    # identity (an ARN AWS mints at create time, or a name_prefix suffix the
    # provider appends at create time) that hashicorp/aws exposes no List
    # operation for, so live-mv has no way to FIND the live object starting
    # only from a new address with no marker on it yet. This is HANDOFF's
    # "no-source instance... refuses by default" row, not "choudoufu refuses
    # where stock proceeds" - stock's own moved block (the D-ORACLE above,
    # and this estate's own D1 moved-block leg) renames every one of these
    # fine, because a moved block is pure address bookkeeping and needs no
    # live discovery at all. Each one here gets its own moved block instead
    # of a live-mv call; the leg still demonstrates live-mv genuinely
    # renaming every child it CAN.
    #   - aws_batch_compute_environment.tiles: AWS Batch mints the compute
    #     environment's own ARN at create time (provider-assigned identity
    #     per the provider's own Identity Schema).
    #   - aws_iam_instance_profile.ecs: this crossing's own name_overrides
    #     (header: "Both inline role policies default to name_prefix") does
    #     NOT cover instance_profile, so it falls back to the module's own
    #     name_prefix default - a server-assigned name tail, same shape as
    #     the two role policies name_overrides exists specifically to avoid,
    #     just not extended to this one.
    NO_LIVE_MV=('aws_batch_compute_environment.tiles' 'aws_iam_instance_profile.ecs')
    {
      for addr in "${NO_LIVE_MV[@]}"; do
        printf 'moved {\n  from = module.overture_tiles_moved.%s\n  to   = module.overture_tiles_final.%s\n}\n\n' "$addr" "$addr"
      done
    } >> "$ESTATE/main.tf"
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the live-mv rename's reinit failed"; }
    LIVE_MV_ADDRS=()
    for addr in "${TAGGABLE_ADDRS[@]}"; do
      skip=""
      for nomv in "${NO_LIVE_MV[@]}"; do [ "$addr" = "$nomv" ] && skip=1; done
      [ -n "$skip" ] && continue
      LIVE_MV_ADDRS+=("$addr")
    done
    [ "${#LIVE_MV_ADDRS[@]}" = "$((16 - ${#NO_LIVE_MV[@]}))" ] \
      || fail "expected $((16 - ${#NO_LIVE_MV[@]})) live-mv addresses (16 taggable minus ${#NO_LIVE_MV[@]} moved-block-only), got ${#LIVE_MV_ADDRS[@]}"
    for addr in "${LIVE_MV_ADDRS[@]}"; do
      # live-mv's CLI arguments take the ordinary HCL address (brackets);
      # its own "Rewrote..." report line names the tofu-address TAG VALUE,
      # which live/MARKERS.md's escaping rule renders with a colon instead
      # ([0] -> :0, ["base"] -> :base) - the same escaping every other
      # script in this batch applies when checking a report line rather
      # than an argument.
      addr_colon="$(sed -E 's/\[/:/g; s/[]"]//g' <<< "$addr")"
      MV_OUT="$(cd "$ESTATE" && "$TOFU" live-mv -estate="$ESTATE_NAME" "module.overture_tiles_moved.$addr" "module.overture_tiles_final.$addr" 2>&1)"; MV_RC=$?
      [ "$MV_RC" -eq 0 ] || { printf '%s\n' "$MV_OUT" | tail -30; fail "choudoufu live-mv on $addr exited $MV_RC"; }
      grep -qF 'Rewrote the ownership marker on one live resource. This was a cloud write.' <<< "$MV_OUT" \
        || { printf '%s\n' "$MV_OUT"; fail "live-mv on $addr did not report a real write"; }
      grep -qF "\"module.overture_tiles_moved.$addr_colon\" -> \"module.overture_tiles_final.$addr_colon\"" <<< "$MV_OUT" \
        || { printf '%s\n' "$MV_OUT"; fail "live-mv on $addr did not report rewriting the tofu-address marker from the old address ($addr_colon) to the new one"; }
    done
    # And the NO_LIVE_MV children: confirm their markers followed the SAME
    # module rename this apply below settles, read via the AWS CLI, not
    # through choudoufu's own report. Both names are server-assigned
    # (name_prefix/ARN), so both lookups are by prefix/filter, not exact
    # name - same shape as the OAC lookup above.
    COMPUTE_ENV_ARN="$(awsl batch describe-compute-environments --query "computeEnvironments[?starts_with(computeEnvironmentName, '${ESTATE_NAME}')].computeEnvironmentArn | [0]" --output text)"
    [ -n "$COMPUTE_ENV_ARN" ] && [ "$COMPUTE_ENV_ARN" != "None" ] || fail "no live Batch compute environment found for $ESTATE_NAME"
    INSTANCE_PROFILE_ARN="$(awsl iam list-instance-profiles --query "InstanceProfiles[?starts_with(InstanceProfileName, '${ESTATE_NAME}')].Arn | [0]" --output text)"
    [ -n "$INSTANCE_PROFILE_ARN" ] && [ "$INSTANCE_PROFILE_ARN" != "None" ] || fail "no live IAM instance profile found for $ESTATE_NAME"

    MOVE_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; MOVE_APPLY_RC=$?
    [ "$MOVE_APPLY_RC" -eq 0 ] || { printf '%s\n' "$MOVE_APPLY_OUT" | tail -40; fail "the D2 moved-block-only apply (for ${NO_LIVE_MV[*]}) exited $MOVE_APPLY_RC"; }
    grep -qE "Resources: 0 added, ${#NO_LIVE_MV[@]} changed, 0 destroyed" <<< "$MOVE_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$MOVE_APPLY_OUT"; fail "the D2 moved-block-only apply was not exactly ${#NO_LIVE_MV[@]} in-place change(s)"; }

    COMPUTE_ENV_ADDR="$(awsl batch list-tags-for-resource --resource-arn "$COMPUTE_ENV_ARN" --query 'tags."tofu-address"' --output text)"
    [ "$COMPUTE_ENV_ADDR" = "module.overture_tiles_final.aws_batch_compute_environment.tiles" ] \
      || fail "the compute environment carries tofu-address=$COMPUTE_ENV_ADDR, not module.overture_tiles_final.aws_batch_compute_environment.tiles"
    INSTANCE_PROFILE_ADDR="$(awsl iam list-instance-profile-tags --instance-profile-name "${INSTANCE_PROFILE_ARN##*/}" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
    [ "$INSTANCE_PROFILE_ADDR" = "module.overture_tiles_final.aws_iam_instance_profile.ecs" ] \
      || fail "the instance profile carries tofu-address=$INSTANCE_PROFILE_ADDR, not module.overture_tiles_final.aws_iam_instance_profile.ecs"
    log "  live-mv: ${#LIVE_MV_ADDRS[@]} of sixteen taggable children renamed, one call each, zero churn; the other ${#NO_LIVE_MV[@]} (${NO_LIVE_MV[*]}, both server-/provider-assigned identities with no List support in the provider - internal/live/mv/mv.go's own correct refusal) renamed via their own moved blocks instead, applied cleanly (0 add, ${#NO_LIVE_MV[@]} change, 0 destroy)"

    AFTER_D2_N="$(tagged_object_count)"
    [ "$AFTER_D2_N" = "16" ] || fail "the tagged object count changed across live-mv: 16 -> $AFTER_D2_N"
    GOT_BUCKET_ADDR_D2="$(awsl s3api get-bucket-tagging --bucket "$BUCKET_NAME" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
    [ "$GOT_BUCKET_ADDR_D2" = "module.overture_tiles_final.aws_s3_bucket.tiles:0" ] \
      || fail "the S3 bucket carries tofu-address=$GOT_BUCKET_ADDR_D2 after live-mv, not module.overture_tiles_final.aws_s3_bucket.tiles:0"
    GOT_OAC_ID_D2="$(awsl cloudfront get-distribution-config --id "$DIST_ID" --query "DistributionConfig.Origins.Items[0].OriginAccessControlId" --output text)"
    [ "$GOT_OAC_ID_D2" = "$GOT_OAC_ID" ] \
      || fail "the CloudFront distribution's own OAC changed from $GOT_OAC_ID to $GOT_OAC_ID_D2 across live-mv - the untaggable/config-derived OAC moved when it should not have"
    log "  $BUCKET_NAME's tofu-address now module.overture_tiles_final.aws_s3_bucket.tiles:0; the distribution's OAC ($GOT_OAC_ID_D2) unchanged - read via the AWS CLI; $AFTER_D2_N tagged objects, unchanged"

    log "=== D3. one more plan: config and markers agree on both renames, nothing proposed ==="
    FINAL_PLAN_OUT="$(cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color 2>&1)"; FINAL_PLAN_RC=$?
    [ "$FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$FINAL_PLAN_OUT" | tail -40; fail "the post-rename plan exited $FINAL_PLAN_RC"; }
    grep -qE 'No changes\.' <<< "$FINAL_PLAN_OUT" \
      || { grep -E '^Plan: |^  # .+ will be' <<< "$FINAL_PLAN_OUT"; fail "the post-rename plan is not empty"; }
    log "  No changes. Both renames are complete and invisible to the next plan."

    gauntlet_stage day2_rename pass "moved block: module.overture_tiles renamed to module.overture_tiles_moved via ONE module-level moved block, 0 add/0 destroy, 16 real tag-marker rewrites (plan showed 18 - two untaggable siblings' policy JSON transiently 'known after apply', resolving to no real change at apply time, confirmed via the stage-5 marker/propagation filter and by value); live-mv: module.overture_tiles_moved renamed to module.overture_tiles_final across 14 of 16 taggable children, one call each, zero churn - the other 2 (aws_batch_compute_environment.tiles and aws_iam_instance_profile.ecs, both server-/provider-assigned identities with no List support in the provider) correctly refused by live-mv and renamed via their own moved blocks instead, applied cleanly; the nine untaggable/config-derived children and the UNTAGGABLE OAC (no longer UNADMITTED_TYPE - #249 narrowed) did not move at all; stock oracle over the identical module rename on cold_deploy's own state also shows zero churn via its own single module-level moved block, covering every one of the 26 children including the two live-mv cannot"

    # ══════════════════════════════════════════════════════════════════
    # REMOVE A BLOCK (day2_remove, live/GAUNTLET.md #7, active)
    # ══════════════════════════════════════════════════════════════════
    #
    # Flips module.overture_tiles_final's own create_cloudfront_
    # distribution input to false - the calling config's module block
    # STAYS declared (a variable edit, not a block deletion) rather than
    # removing module.overture_tiles_final wholesale, because this estate
    # is ONE module call carrying all 26 resources; see REMOVE-ORACLE
    # above for why. The plan-visible outcome is identical to a block
    # deletion: both count-gated children go from declared to gone.
    #
    # THE SAME FAMILY OF WALL corpus-s3-bucket-complete's own day2_remove
    # found and filed as issue #410 - BROADER here, measured, not assumed:
    # this crossing tried the "block entirely gone" shape S3 used first and
    # switched to this count-shrink shape because the estate is one module
    # call with no smaller block to delete (see above); that switch is what
    # surfaces the wider defect. Confirmed empirically: NEITHER the
    # untaggable OAC NOR the TAGGED, MARKED CloudFront distribution is
    # destroyed - only the still-declared bucket policy's in-place update
    # is proposed, with no diagnostic for either missing destroy. That
    # rules out "untaggable, so no marker to sweep" as the sole cause (the
    # distribution IS tagged and WAS marked, confirmed live through every
    # earlier stage): classifyOrphans's own "pending" guard - "a declared
    # instance of this block is unclaimed, so this may be a rename, not a
    # removal" - reads by block key (type + name), not by whether that
    # block's CURRENT count still evaluates to a live instance, so a
    # count-shrunk-to-zero block reads exactly like a genuinely pending one
    # and withholds the destroy for BOTH its taggable and untaggable
    # children alike. That is day2_count's own not-yet-active territory
    # (live/GAUNTLET.md #8) by a different name, and #410 undersold it as
    # untaggable-only; both need the same record-primary discovery path
    # HANDOFF's "The order" item 1 describes. The resulting cloud is
    # equivalent either way (nothing left dangling once the distribution
    # and its OAC are gone, confirmed via the AWS CLI below), but the PLAN
    # differs from stock's, so this is left genuinely failing here rather
    # than asserting less than the oracle asserts.
    CURRENT_STAGE=day2_remove
    log "=== STAGE 7. day2_remove: create_cloudfront_distribution=false on module.overture_tiles_final ==="
    log "  stock oracle already computed above (REMOVE-ORACLE, before migrate ever wrote a live tag): exactly two destroys (the distribution and its OAC)"
    perl -pi -e 's/create_cloudfront_distribution = true/create_cloudfront_distribution = false/' "$ESTATE/main.tf"
    grep -q 'create_cloudfront_distribution = false' "$ESTATE/main.tf" || fail "STAGE 7: the create_cloudfront_distribution edit did not match module.overture_tiles_final's block - the corpus pin has moved"
    ( cd "$ESTATE" && "$TOFU" init -upgrade -input=false -no-color ) > /tmp/overture-day2-remove-init.log 2>&1 || {
      tail -40 /tmp/overture-day2-remove-init.log; fail "the day2_remove reinit failed"; }
    rm -f "$ESTATE/terraform.tfstate" "$ESTATE/terraform.tfstate.backup"
    REMOVE_PLAN_OUT="$(cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color 2>&1)"; REMOVE_PLAN_RC=$?
    [ "$REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -40; fail "the day2_remove plan exited $REMOVE_PLAN_RC"; }
    grep -qE '^  # module\.overture_tiles_final\.aws_cloudfront_distribution\.tiles\[0\] will be destroyed' <<< "$REMOVE_PLAN_OUT" \
      || { grep -E '^  # .+ will be' <<< "$REMOVE_PLAN_OUT"; fail "choudoufu does not propose destroying module.overture_tiles_final.aws_cloudfront_distribution.tiles[0]"; }
    grep -qE '^  # module\.overture_tiles_final\.aws_cloudfront_origin_access_control\.tiles\[0\] will be destroyed' <<< "$REMOVE_PLAN_OUT" \
      || { grep -E '^  # .+ will be' <<< "$REMOVE_PLAN_OUT"; fail "choudoufu does not propose destroying module.overture_tiles_final.aws_cloudfront_origin_access_control.tiles[0] - the untaggable sibling stock also destroys (issue #410, see above)"; }
    grep -qE '^  # module\.overture_tiles_final\.aws_s3_bucket_policy\.tiles\[0\] will be updated in-place' <<< "$REMOVE_PLAN_OUT" \
      || { grep -E '^  # .+ will be' <<< "$REMOVE_PLAN_OUT"; fail "choudoufu does not propose updating module.overture_tiles_final.aws_s3_bucket_policy.tiles[0] (the CloudFrontOAC statement should drop, same as the stock oracle)"; }
    grep -qF 'Plan: 0 to add, 1 to change, 2 to destroy.' <<< "$REMOVE_PLAN_OUT" \
      || { grep -E '^Plan:|^No changes' <<< "$REMOVE_PLAN_OUT"; fail "the day2_remove plan is not exactly two destroys plus the bucket policy update"; }
    log "  choudoufu: exactly two destroys plus one in-place bucket-policy update proposed - the same objects and change the stock oracle proposes"

    REMOVE_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; REMOVE_APPLY_RC=$?
    [ "$REMOVE_APPLY_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_APPLY_OUT" | tail -40; fail "the day2_remove apply exited $REMOVE_APPLY_RC"; }
    grep -qE 'Resources: 0 added, 1 changed, 2 destroyed' <<< "$REMOVE_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$REMOVE_APPLY_OUT"; fail "the day2_remove apply was not exactly two destroys plus one change"; }
    awsl cloudfront get-distribution --id "$DIST_ID" >/dev/null 2>&1 \
      && fail "the CloudFront distribution $DIST_ID is still live after the day2_remove apply"
    log "  the CloudFront distribution is genuinely gone (read via the AWS CLI, not choudoufu's own report)"

    FINAL_REMOVE_PLAN_OUT="$(cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color 2>&1)"; FINAL_REMOVE_PLAN_RC=$?
    [ "$FINAL_REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$FINAL_REMOVE_PLAN_OUT" | tail -40; fail "the post-remove plan exited $FINAL_REMOVE_PLAN_RC"; }
    grep -qE '^  # .+ will be' <<< "$FINAL_REMOVE_PLAN_OUT" \
      && { grep -E '^  # .+ will be' <<< "$FINAL_REMOVE_PLAN_OUT"; fail "the post-remove plan proposes a resource change"; }
    log "  no resource action proposed. The distribution is gone and nothing else moved."

    gauntlet_stage day2_remove pass "choudoufu: create_cloudfront_distribution=false proposed exactly two destroys plus one in-place update (0 add, 1 change, 2 destroy: the distribution, its untaggable OAC, and the bucket policy's own CloudFrontOAC statement dropping), applied cleanly (0 added, 1 changed, 2 destroyed), the distribution is genuinely gone from the live account (read via the AWS CLI, not choudoufu's own report), and the next plan proposes no resource action; stock oracle on cold_deploy's own state also proposes exactly the same two destroys plus the same bucket-policy update"
    CURRENT_STAGE=""
  fi
fi
CURRENT_STAGE=""

gauntlet_end

log ""
log "=== SUMMARY: stage 1 PASS; stage 2 PASS; stage 3 PASS (empty plan, S3 bucket"
log "=== and OAC identities verified by value); stage 4 PASS (no-op apply, object"
log "=== count and identities unchanged); stage 5 PASS (VPC Name-tag drift"
log "=== detected and reconverged, verified against the stock oracle) ==="
