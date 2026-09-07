#!/usr/bin/env bash
# (moved from the justfile's retired demo-corpus-overture-tiles recipe; run with: just demo-run corpus-overture-tiles)
# OvertureMaps/terraform-aws-overture-tiles (live/corpus-manifest.json,
# pinned by TAG v1.2.0 AND commit - the third OpenTofu-native crossing, and
# the first with a real tagged release): a real, actively-maintained AWS
# Batch/S3/CloudFront tile-generation module from the Overture Maps
# Foundation, CI-verified against OpenTofu exclusively (tofu fmt/validate/
# test/tflint, mock_provider tests - terraform never appears in its own
# CI) though its own HCL is plain .tf and Terraform-compatible too, so the
# evidence here is in tooling rather than syntax. Stage 1 (cold deploy, 26
# real resources) passes clean. Stages 2-3 are genuinely BLOCKED, and
# asserted as such rather than skipped or routed around: a real floci bug
# (lex00/floci#72 - AWS Batch's TagResource path misroutes to AppSync's
# catch-all handler) blocks 3 of 26 resources from stamping, and a real
# choudoufu gap (INTENTIUS/choudoufu#322 - an untaggable admitted type with
# a server-assigned name component hard-aborts the whole live-plan) was
# found and worked around via the module's own name_overrides input rather
# than fixed. See the script's own header for the full evidence, the exact
# scoping, and both issues. Needs Docker, the AWS CLI, and the real `tofu`
# binary; runs on its own port (4726).
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
#   8. DAY2_COUNT    PASS - PART G, after day2_remove: a self-contained
#                     synthetic count block (aws_security_group.count_test,
#                     count = 2, at the estate root, nothing references it)
#                     scaled 2 -> 1 -> 2. Every count this module declares
#                     is a boolean create toggle, and its one real for_each
#                     is scoped to a single theme by this crossing's own
#                     root config, so neither can carry this stage - the
#                     sanctioned fallback, see PART G's own header for the
#                     measured reasoning. G0 is the stock oracle: the identical
#                     block stood up with plain tofu in its own working
#                     directory against the same (idle) endpoint, then torn
#                     down before the choudoufu side runs.
#
# BREAK=1 corrupts the S3 bucket's expected tofu-address ahead of stage 2's
# AWS-CLI re-read, proving that assertion is load-bearing. BREAK_STAGE5=1
# tampers a second live object (the internet gateway's Name tag) ahead of
# stage 5's own mutation, proving its single-object assertion is load-bearing.
# BREAK_COUNT=1 asserts day2_count's WRONG instance (count_test[0] rather
# than count_test[1]) was the one destroyed, proving that assertion is
# load-bearing too.
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
#   BREAK_COUNT   set to 1 to run day2_count's own negative control instead
#                 of its real checks: after the real scale-down plan, assert
#                 the WRONG instance (count_test[0] rather than
#                 count_test[1]) was destroyed. The Break text in
#                 tools/gauntlet/stages.go for day2_count, verbatim.
#   BREAK_APPROVAL
#                 set to 1 to run plan_approval's own negative control
#                 instead of the real refusal check (PART P): after the
#                 world has moved out of band, assert the saved plan file
#                 APPLIES cleanly - the Break text in
#                 tools/gauntlet/stages.go for plan_approval is literally
#                 "Apply the planfile after a mutation and expect success;
#                 the run must refuse", so this assertion has to fail.
#                 Independent of every BREAK above, and the only one of them
#                 under which PART P runs at all - the others deliberately
#                 leave the estate somewhere PART P does not describe, and
#                 it reports no verdict there.
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
gauntlet_begin_stage cold_deploy
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
gauntlet_begin_stage greenfield
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
# strict { no_source_create = "create" }: found necessary re-verifying this
# stage after main's CHOUDOUFU_NODE_RESOLVE default flip (845e7a0d9d,
# 2026-08-25) - a genuinely cold apply now refuses config-identified
# instances whose identity value belongs to a sibling that does not exist
# yet either (#365 ruling 4's default refusal of that ambiguity), and a
# greenfield apply is the one case an operator KNOWS it is a real create.
# Same fix, same precedent as corpus-alb-complete's own 898091b8f2.
write_root "$GREEN" '

  live {
    estate = "'"$GREEN_ESTATE_NAME"'"
    record_store "local" {
      path = ".tofu-records"
    }
    strict {
      no_source_create = "create"
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
GREEN_RECORD_FILES="$(gauntlet_record_count "$GREEN/.tofu-records/tofu-records")"
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
gauntlet_end_stage

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
gauntlet_begin_stage day2_rename
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
gauntlet_begin_stage day2_remove
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
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# PART F-ORACLE: REPLACE, stock (day2_replace, active - live/GAUNTLET.md #9)
# ══════════════════════════════════════════════════════════════════════════
#
# "Stock's replace of the same resource leaves the same single object."
# aws_cloudwatch_log_group.batch (this estate's one CloudWatch log group,
# a plain resource with no count/for_each - untouched by REMOVE-ORACLE's
# own create_cloudfront_distribution=false toggle above) is forced to
# replace via the module's own name_overrides.cloudwatch_log_group input
# (variables.tf; batch.tf's local.resolved_log_group_name coalesces it
# ahead of the name_prefix-derived default), the SAME published escape
# hatch this crossing's own root already uses for the two inline role
# policies (header point 4) - a variable edit, not a code edit, matching
# this corpus's own DELTA discipline. CloudWatch Logs has no RenameLogGroup
# API - only CreateLogGroup/DeleteLogGroup - so name is ForceNew in the
# provider's own schema, confirmed empirically below by the plan's own
# "must be replaced" annotation, not assumed. The SAME literal reaches two
# real siblings whose own content embeds the log group's name/arn: aws_
# iam_role_policy.execution_logs's policy document (iam.tf line 75:
# ${aws_cloudwatch_log_group.batch.arn}:*) and aws_batch_job_definition.
# tiles["base"]'s container_properties JSON (batch.tf: "awslogs-group" =
# aws_cloudwatch_log_group.batch.name) - both real, expected changes
# cascading from the one ForceNew change, computed here (not assumed) so
# PART F below knows exactly what shape to expect. A FOURTH copy of
# cold_deploy's own state (same convention as D-ORACLE/REMOVE-ORACLE
# above), before the real script's own rename ever touches $ESTATE.
gauntlet_begin_stage day2_replace
log "=== F-ORACLE. stock: force-replace module.overture_tiles's log group via its ForceNew name, on cold_deploy's own state ==="
REPLACE_ORACLE="$WORK/replace-oracle"
copy_module "$REPLACE_ORACLE"
write_root "$REPLACE_ORACLE" "" true
perl -pi -e "s{(execution_role_policy = \"${ESTATE_NAME}-execution-logs-policy\"\n)}{\$1    cloudwatch_log_group   = \"/aws/batch/${ESTATE_NAME}-v2\"\n}" "$REPLACE_ORACLE/main.tf"
grep -q 'cloudwatch_log_group   = "/aws/batch/' "$REPLACE_ORACLE/main.tf" \
  || fail "adding name_overrides.cloudwatch_log_group to the replace-oracle copy did not match - the corpus pin has moved"
[ -f "$PLAIN/.terraform.lock.hcl" ] && cp "$PLAIN/.terraform.lock.hcl" "$REPLACE_ORACLE/.terraform.lock.hcl"
cp "$PLAIN/terraform.tfstate" "$REPLACE_ORACLE/terraform.tfstate"
( cd "$REPLACE_ORACLE" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$REPLACE_ORACLE" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_replace stock oracle's init failed"; }
REPLACE_ORACLE_PLAN_OUT="$(cd "$REPLACE_ORACLE" && tofu plan -input=false -no-color 2>&1)"; REPLACE_ORACLE_PLAN_RC=$?
[ "$REPLACE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_replace stock oracle plan exited $REPLACE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.overture_tiles\.aws_cloudwatch_log_group\.batch must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "stock does not propose replacing the log group when its name changes"; }
grep -qE '~ +name +=.+forces replacement' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "stock's plan does not mark the log group's name as forcing replacement - it may not be ForceNew after all"; }
printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" > "$WORK/replace-oracle-plan.log"
log "  stock: exactly one forced replace (the log group); the cascade into its two siblings (the execution role's inline log policy, the job definition) is recorded to $WORK/replace-oracle-plan.log for PART F below to compare against, not asserted rigidly here - plan only, never applied"
gauntlet_end_stage

gauntlet_begin_stage migrate

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
gauntlet_begin_stage migrate
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
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - #345 FIXED, and now genuinely EMPTY. STAGE 2d above
# folds in the one-time convergence this estate's count blocks and its #249
# OAC gap require; this stage deletes the state file a SECOND time and reruns
# live-plan completely fresh, never trusting the plan already seen in 2d, so
# it is its own genuine, from-nothing check.
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage test_plan
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
gauntlet_end_stage

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
gauntlet_begin_stage test_apply
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
gauntlet_end_stage

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
gauntlet_begin_stage drift_reconverge
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
  # PART P: PLAN, REVIEW, APPLY (plan_approval, live/GAUNTLET.md #12, issue #903)
  # ══════════════════════════════════════════════════════════════════════════
  #
  # The pipeline shape CI has always run: plan on the pull request, a human
  # approves, apply exactly what was approved. The artifact that crosses that
  # gate is the plan file, and under live markers it is an APPROVAL rather
  # than an instruction - "apply <planfile>" re-reads the live system, plans
  # against what it finds now, and compares that fresh plan with the file's,
  # refusing by name and with exit 3 when the two disagree (issue #878,
  # internal/command/live_approval.go).
  #
  # Both arms run on every real run, because only the pair is evidence:
  #
  #   P2/P3  the world MOVES between the approval and the apply - the VPC's
  #          own Name tag is changed out of band through the AWS CLI, the
  #          SAME mutation STAGE 5 above already proves this estate's plan
  #          notices and scopes to one object - and the apply must refuse:
  #          exit 3, the named summary, the unapproved row printed by address
  #          AND by the live VPC id it was computed against, and the reviewed
  #          change still not landed when the bucket's CORS rules are read
  #          back through the CLI.
  #   P4     nothing has moved (the Name tag is put back first) and the SAME
  #          file must APPLY. This is the inverted control that
  #          live/smoke/scenarios/apply-what-was-approved.sh reasons out: a
  #          comparison which refuses unconditionally is not a check, so P3's
  #          refusal is only worth something if the identical artifact goes
  #          through when the world is where the approval left it.
  #
  # The two objects are deliberately disjoint - the change under review is
  # one in-place cors_rule update on the bucket's CORS configuration, the
  # out-of-band move is on the VPC - so the refusal has an EXTRA row to name
  # rather than a values-only disagreement about the same row
  # (approvalMismatchDetail's Drifted branch). cors_allowed_origins is the
  # reviewed argument because it is the only root knob that lands on exactly
  # ONE of this estate's 26 instances and lands in place: `tags` reaches all
  # 16 taggable children at once, name_overrides and the launch template are
  # ForceNew, and the create_* toggles are creates and destroys. Nothing
  # later in this script reads aws_s3_bucket_cors_configuration.tiles[0] by
  # id, and an in-place update moves no id anyway, so PART D's own 16-address
  # capture and PART F's log-group ids are untouched.
  #
  # Runs only on a real run. Under any of this script's other BREAK controls
  # the estate is deliberately left somewhere this part does not describe, so
  # it reports no verdict at all and the runner records the stage as not_run,
  # never as a pass. (BREAK_STAGE5 is already excluded: this whole block sits
  # inside stage 5's own real-run branch.)
  if [ -z "${BREAK:-}" ] && [ -z "${BREAK_COUNT:-}" ]; then
    gauntlet_begin_stage plan_approval
    log ""
    log "=== PART P: plan, review, apply (the approval gate, live/GAUNTLET.md #12) ==="

    P_REVIEWED_ADDR="module.overture_tiles.aws_s3_bucket_cors_configuration.tiles[0]"
    P_MOVED_ADDR="module.overture_tiles.aws_vpc.batch[0]"
    P_NEW_ORIGIN="https://tiles.example.invalid"

    log "=== P1. the change under review: one argument, reaching one instance ==="
    [ "$(grep -c '^  cors_allowed_origins  = \["\*"\]$' "$ESTATE/main.tf")" = "1" ] \
      || fail "main.tf no longer carries exactly one cors_allowed_origins argument - this script's own write_root has moved"
    perl -0pi -e "s{^  cors_allowed_origins  = \\[\"\\*\"\\]\$}{  cors_allowed_origins  = [\"$P_NEW_ORIGIN\"]}m" "$ESTATE/main.tf"
    [ "$(grep -c "^  cors_allowed_origins  = \[\"$P_NEW_ORIGIN\"\]\$" "$ESTATE/main.tf")" = "1" ] \
      || fail "the reviewed edit did not write exactly one cors_allowed_origins argument"
    log "  edited one argument: module.overture_tiles's cors_allowed_origins is now [\"$P_NEW_ORIGIN\"] (was [\"*\"])"

    P_PLAN_OUT="$(cd "$ESTATE" && "$TOFU" plan -input=false -no-color -out=approved.tfplan 2>&1)"; P_PLAN_RC=$?
    [ "$P_PLAN_RC" -eq 0 ] || { printf '%s\n' "$P_PLAN_OUT" | tail -40; fail "plan -out exited $P_PLAN_RC"; }
    [ -f "$ESTATE/approved.tfplan" ] || { printf '%s\n' "$P_PLAN_OUT" | tail -20; fail "plan -out wrote no file"; }
    P_APPROVED_ADDRS="$(grep -oE '^  # \S+ will be updated' <<< "$P_PLAN_OUT" | awk '{print $2}' | sort -u)"
    [ "$P_APPROVED_ADDRS" = "$P_REVIEWED_ADDR" ] \
      || { grep -E '^  # .+ (will be|must be)' <<< "$P_PLAN_OUT"; fail "the approved plan is about [$P_APPROVED_ADDRS], not $P_REVIEWED_ADDR alone"; }
    if grep -qE '^  # .+ (will be (created|destroyed)|must be replaced)' <<< "$P_PLAN_OUT"; then
      grep -E '^  # .+ (will be|must be)' <<< "$P_PLAN_OUT"; fail "the approved plan proposes a create, a destroy or a replace; this review is one in-place update"
    fi
    P_PLAN_BYTES="$(wc -c < "$ESTATE/approved.tfplan" | tr -d ' ')"
    log "  approved.tfplan written ($P_PLAN_BYTES bytes of stock-format plan file); the approval is exactly one update, on $P_REVIEWED_ADDR"

    log "=== P2. the world moves between the approval and the apply ==="
    awsl ec2 create-tags --resources "$VPC_ID" --tags Key=Name,Value=moved-after-approval >/dev/null \
      || fail "the out-of-band Name-tag move could not be made through the AWS CLI"
    P_MOVED_VALUE="$(awsl ec2 describe-tags \
      --filters "Name=resource-id,Values=$VPC_ID" "Name=key,Values=Name" \
      --query "Tags[0].Value" --output text)"
    [ "$P_MOVED_VALUE" = "moved-after-approval" ] || fail "the out-of-band move did not take: $VPC_ID's Name tag reads \"$P_MOVED_VALUE\""
    log "  $VPC_ID's Name tag changed out of band to \"moved-after-approval\" (config says $VPC_NAME_TAG) - after the approval, before the apply, through the AWS CLI"

    log "=== P3. apply the approved plan against a world that moved ==="
    P_GATE_RC=0
    P_GATE_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -no-color approved.tfplan 2>&1)" || P_GATE_RC=$?
    if [ "${BREAK_APPROVAL:-}" = "1" ]; then
      # stages.go's own Break line for plan_approval, executed literally:
      # "Apply the planfile after a mutation and expect success; the run must
      # refuse." Expecting success here is the defect this stage exists to
      # catch, so this assertion has to fail.
      [ "$P_GATE_RC" = "0" ] \
        || fail "BREAK_APPROVAL=1: the apply of a plan file approved before the world moved exited $P_GATE_RC, not 0 - the refusal is load-bearing and this expectation is the defect stage 12 catches"
      log "  BREAK_APPROVAL=1: the apply exited 0 with the world moved - stage 12 is NOT load-bearing"
    fi
    [ "$P_GATE_RC" = "3" ] \
      || { printf '%s\n' "$P_GATE_OUT" | tail -40; fail "the apply exited $P_GATE_RC, want 3 - a plan file whose approval no longer covers the run must refuse with its own status"; }
    grep -q "The approved plan no longer matches the live system" <<< "$P_GATE_OUT" \
      || { printf '%s\n' "$P_GATE_OUT" | tail -40; fail "the apply stopped, but not with the named refusal"; }
    # Everything from the refusal's own summary line onward. The fresh plan
    # printed above it also names the moved VPC, so asserting over the whole
    # output would pass on a refusal that named nothing at all.
    P_REFUSAL="$(sed -n '/The approved plan no longer matches the live system/,$p' <<< "$P_GATE_OUT")"
    grep -qF "This apply would do, and the approved plan does not include:" <<< "$P_REFUSAL" \
      || { printf '%s\n' "$P_REFUSAL"; fail "the refusal does not classify the difference as a change nobody approved"; }
    P_EXTRA_ROW="$(grep -F "$P_MOVED_ADDR" <<< "$P_REFUSAL" | head -1)"
    [ -n "$P_EXTRA_ROW" ] \
      || { printf '%s\n' "$P_REFUSAL"; fail "the refusal does not name $P_MOVED_ADDR, the change nobody approved"; }
    grep -qF "$VPC_ID" <<< "$P_EXTRA_ROW" \
      || { printf '%s\n' "$P_REFUSAL"; fail "the refusal names the address but not $VPC_ID, the live object the change was computed against: the row reads \"$P_EXTRA_ROW\""; }
    grep -qF "Exit status 3" <<< "$P_REFUSAL" \
      || { printf '%s\n' "$P_REFUSAL"; fail "the refusal does not tell a pipeline what its exit status means"; }
    if grep -q "Apply complete!" <<< "$P_GATE_OUT"; then
      printf '%s\n' "$P_GATE_OUT" | tail -20; fail "the apply ran anyway after refusing"
    fi
    # Not "no Apply complete line" alone: read the live object the approval
    # was about and confirm the reviewed change did not land.
    P_REVIEWED_ORIGIN="$(awsl s3api get-bucket-cors --bucket "$BUCKET_NAME" --query 'CORSRules[0].AllowedOrigins[0]' --output text)"
    [ "$P_REVIEWED_ORIGIN" = "*" ] \
      || fail "the refused apply still wrote the reviewed change: $BUCKET_NAME's first CORS allowed origin reads \"$P_REVIEWED_ORIGIN\", want the pre-approval \"*\""
    printf '%s\n' "$P_REFUSAL" | head -12
    log "  refused by name, exit $P_GATE_RC, nothing applied - the row it names is \"$P_EXTRA_ROW\", exactly the change that appeared after the approval"

    log "=== P4. the inverted control: put the world back, apply the SAME file ==="
    awsl ec2 create-tags --resources "$VPC_ID" --tags "Key=Name,Value=$VPC_NAME_TAG" >/dev/null \
      || fail "the out-of-band move could not be undone through the AWS CLI"
    P_RESTORED="$(awsl ec2 describe-tags \
      --filters "Name=resource-id,Values=$VPC_ID" "Name=key,Values=Name" \
      --query "Tags[0].Value" --output text)"
    [ "$P_RESTORED" = "$VPC_NAME_TAG" ] || fail "the out-of-band move was not undone: $VPC_ID's Name tag reads \"$P_RESTORED\""
    P_OK_RC=0
    P_OK_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -no-color approved.tfplan 2>&1)" || P_OK_RC=$?
    [ "$P_OK_RC" = "0" ] \
      || { printf '%s\n' "$P_OK_OUT" | tail -40; fail "the same plan file was refused (exit $P_OK_RC) over a world that had not moved - a comparison that refuses unconditionally is not a check"; }
    grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$P_OK_OUT" \
      || { grep -E 'Apply complete' <<< "$P_OK_OUT"; fail "the approved apply did not change exactly the one reviewed resource"; }
    P_LANDED="$(awsl s3api get-bucket-cors --bucket "$BUCKET_NAME" --query 'CORSRules[0].AllowedOrigins[0]' --output text)"
    [ "$P_LANDED" = "$P_NEW_ORIGIN" ] \
      || fail "the approved change did not land: $BUCKET_NAME's first CORS allowed origin reads \"$P_LANDED\", want \"$P_NEW_ORIGIN\""
    log "  the identical artifact applied (0 added, 1 changed, 0 destroyed) and $BUCKET_NAME's CORS rule now allows $P_NEW_ORIGIN, read via the AWS CLI"

    log "=== P5. put the estate back where the rest of this script expects it ==="
    rm -f "$ESTATE/approved.tfplan"
    perl -0pi -e "s{^  cors_allowed_origins  = \\[\"$P_NEW_ORIGIN\"\\]\$}{  cors_allowed_origins  = [\"*\"]}m" "$ESTATE/main.tf"
    [ "$(grep -c '^  cors_allowed_origins  = \["\*"\]$' "$ESTATE/main.tf")" = "1" ] \
      || fail "reverting the reviewed edit did not restore cors_allowed_origins = [\"*\"]"
    P_REVERT_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; P_REVERT_RC=$?
    [ "$P_REVERT_RC" -eq 0 ] || { printf '%s\n' "$P_REVERT_OUT" | tail -40; fail "the revert apply failed"; }
    grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$P_REVERT_OUT" \
      || { grep -E 'Apply complete' <<< "$P_REVERT_OUT"; fail "the revert apply did not change exactly the one reviewed resource back"; }
    P_GONE="$(awsl s3api get-bucket-cors --bucket "$BUCKET_NAME" --query 'CORSRules[0].AllowedOrigins[0]' --output text)"
    [ "$P_GONE" = "*" ] || fail "$BUCKET_NAME's CORS rule still reads \"$P_GONE\" after the revert, not \"*\""
    P_KEPT_NAME="$(awsl ec2 describe-tags \
      --filters "Name=resource-id,Values=$VPC_ID" "Name=key,Values=Name" \
      --query "Tags[0].Value" --output text)"
    [ "$P_KEPT_NAME" = "$VPC_NAME_TAG" ] || fail "$VPC_ID's Name tag is \"$P_KEPT_NAME\" after PART P, not the configured $VPC_NAME_TAG"
    P_FINAL_OUT="$(cd "$ESTATE" && "$TOFU" plan -input=false -no-color 2>&1)"; P_FINAL_RC=$?
    [ "$P_FINAL_RC" -eq 0 ] || { printf '%s\n' "$P_FINAL_OUT" | tail -40; fail "the post-revert plan exited $P_FINAL_RC"; }
    if grep -qE '^  # .+ (will be (created|updated|destroyed)|must be replaced)' <<< "$P_FINAL_OUT"; then
      grep -E '^  # .+ (will be|must be)' <<< "$P_FINAL_OUT"; fail "the estate is not converged again after PART P"
    fi
    [ ! -f "$ESTATE/terraform.tfstate" ] || fail "PART P left a state file behind"
    log "  reverted; the estate is converged again and PART D starts from where it would have"

    log ""
    log "PART P (plan, review, apply): PASS"
    gauntlet_stage plan_approval pass "one argument edited (module.overture_tiles's cors_allowed_origins, [\"*\"] -> [\"$P_NEW_ORIGIN\"], which reaches $P_REVIEWED_ADDR and nothing else - it is the only root knob of this 26-instance estate that lands on exactly one instance IN PLACE, since \`tags\` reaches all 16 taggable children at once, name_overrides and the launch template are ForceNew, and the create_* toggles are creates and destroys), \"plan -out=approved.tfplan\" wrote a $P_PLAN_BYTES-byte stock-format plan file whose whole change set is that one in-place update; the world then moved out of band ($VPC_ID's Name tag -> moved-after-approval, this estate's own STAGE 5 mutation lifted, through the AWS CLI and never through choudoufu) and \"apply approved.tfplan\" refused with \"The approved plan no longer matches the live system\" at exit 3, classifying the drift under \"This apply would do, and the approved plan does not include:\" and naming the extra row as \"$P_EXTRA_ROW\" - both $P_MOVED_ADDR and the live $VPC_ID it was computed against - with \"Exit status 3\" spelled out for a pipeline; nothing was applied - $BUCKET_NAME's CORS rule still read \"*\" through s3api get-bucket-cors rather than from the absence of an \"Apply complete!\" line. Inverted control on the same run (the shape live/smoke/scenarios/apply-what-was-approved.sh reasons out): with the Name tag put back and nothing else changed, the IDENTICAL file applied - 0 added, 1 changed, 0 destroyed - and the CORS rule read back as $P_NEW_ORIGIN, so the refusal is earned by the drift and not handed out to every plan file. Reverted and reconverged in P5 (CORS back to \"*\", VPC Name tag still $VPC_NAME_TAG, next plan proposes no resource action, no state file left behind) so PART D starts where it would have. BREAK_APPROVAL=1 asserts stage 12's own recorded Break line (apply the planfile after a mutation and expect success) and correctly fails"
    log ""
  fi

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
  gauntlet_begin_stage day2_rename
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
    # FIXED by gauntlet:sweep-moved-alias (internal/live/discovery/
    # recordorphan_read.go, merged 43556706d6): this leg regressed after
    # 610511fb73 (the record-orphan-read sweep, #405's day2_remove fix)
    # added recordOrphanReadSweep, which reads the record store for any
    # UNTAGGABLE type's undeclared old-address record and proposes
    # destroying it - generically, since its filter is "untaggable + has a
    # persisted identity record", not tied to any specific type. Its own
    # rename-safety check (the `pending` map, built from res.Unbound) only
    # recognized "a declared instance of the SAME address is unclaimed" -
    # it never consulted moved.Aliases/moved.Honoured(req.Config) the way
    # the marker path already did. So this moved block, relocating
    # module.overture_tiles, started destroying EIGHT untaggable children
    # under the OLD address instead of matching them under the new one:
    # aws_iam_role_policy (x2), aws_iam_role_policy_attachment (x2),
    # aws_route, aws_route_table_association, aws_s3_bucket_policy and
    # aws_s3_bucket_public_access_block - the widest blast radius of this
    # generic gap seen in that unit, spanning IAM, VPC/routing and S3
    # types in one estate. SAME root cause, independently confirmed on
    # corpus-giantswarm-crossplane, corpus-ec2-instance-complete,
    # corpus-rds-complete-postgres, corpus-security-group-complete,
    # corpus-dynamodb-table-basic, corpus-autoscaling-complete and
    # corpus-s3-bucket-complete in that same unit.
    #
    # RE-VERIFIED against current main (alias-wave-b unit, 2026-08): this
    # leg is zero churn again (0 add, 0 destroy, sixteen in-place tag
    # updates - the assertion below already went from failing to passing;
    # the stale comment above is what changed here). See D2 below for a
    # SEPARATE, still-open gap this fix does not reach.
    grep -qE '^  # .+ will be (destroyed|created)' <<< "$MOVED_PLAN_OUT" \
      && { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block rename destroys untaggable children under the OLD module.overture_tiles address instead of zero churn - see the comment immediately above this assertion for the (fixed) root cause"; }
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
    if ! grep -qE "Resources: 0 added, ${#NO_LIVE_MV[@]} changed, 0 destroyed" <<< "$MOVE_APPLY_OUT"; then
      # RE-VERIFIED against current main (alias-wave-b unit, 2026-08): D1's
      # module-level moved block (module.overture_tiles -> _moved) is
      # structural HCL and covers every one of the 26 children, taggable
      # and untaggable alike, automatically. D2 has no equivalent: the
      # module is renamed a second time (_moved -> _final) via a mix of
      # live-mv calls (14 taggable addresses) and two per-resource moved
      # blocks (NO_LIVE_MV, above) - neither of which names the OTHER ten
      # untaggable/config-derived children at all. Those never got a moved
      # statement or a live-mv call carrying them from _moved to _final,
      # so this apply proposes real action on them - the SAME generic gap
      # 63c0a18858 named for corpus-giantswarm-crossplane's D2 ("a second,
      # distinct record-located-rename gap the moved.Aliases consult fix
      # does not reach, since no moved statement names this hop at all"),
      # here reaching a wider spread across this estate's own untaggable
      # set. Not fixed in this re-verification pass.
      MOVE_APPLY_ACTIONS="$(grep -E ': (Destroying|Destruction complete|Creating|Creation complete)' <<< "$MOVE_APPLY_OUT")"
      printf '%s\n' "$MOVE_APPLY_ACTIONS"
      grep -E 'Apply complete' <<< "$MOVE_APPLY_OUT"
      MOVE_APPLY_SUMMARY="$(grep -E 'Apply complete' <<< "$MOVE_APPLY_OUT")"
      fail "the D2 moved-block-only apply was not exactly ${#NO_LIVE_MV[@]} in-place change(s): $MOVE_APPLY_SUMMARY; per-resource actions: $(tr '\n' ';' <<< "$MOVE_APPLY_ACTIONS"); the ten untaggable/config-derived children (nine plus the OAC) have no moved statement or live-mv call carrying their record from module.overture_tiles_moved to module.overture_tiles_final - the SAME generic gap 63c0a18858 named for corpus-giantswarm-crossplane's D2, not fixed here"
    fi

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
    # ══════════════════════════════════════════════════════════════════
    # PART F: REPLACE (day2_replace, active - live/GAUNTLET.md #9)
    # ══════════════════════════════════════════════════════════════════
    #
    # module.overture_tiles_final's log group is forced to replace via the
    # module's own published name_overrides.cloudwatch_log_group input -
    # the same escape hatch this crossing's root already uses for the two
    # inline role policies (header point 4), now supplied for the first
    # time. F-ORACLE above already confirmed, empirically, that stock
    # marks the log group's name as forcing replacement on cold_deploy's
    # own state, cascading into two real in-place updates: aws_iam_role_
    # policy.execution_logs's policy document (embeds the log group's arn)
    # and aws_batch_job_definition.tiles["base"]'s container_properties
    # (embeds the log group's name in its awslogs-group log option).
    # Neither the execution role's policy nor the job definition is
    # destroyed or created - only the log group is.
    #
    # THE create_before_destroy SCOPE NOTE (see corpus-sqs-basic's own
    # PART F header for the fuller discussion). The log group is a plain
    # child of module.overture_tiles_final, not itself a module call, so a
    # `lifecycle` block could in principle sit directly on it without
    # crossing the module-call-boundary line the sqs/ec2/lambda estates in
    # this same unit hit - but this corpus's own DELTA discipline (header:
    # every edit here is a published input variable, never a code edit)
    # applies just as much, so this exercises OpenTofu's DEFAULT replace
    # ordering (destroy-then-create) rather than create_before_destroy,
    # the same choice every other estate in this unit makes. BREAK=replace
    # below manufactures the coexistence a skipped destroy half would
    # leave.
    gauntlet_begin_stage day2_replace
    record_key() { printf '%s' "$1" | base64 | tr '+/' '-_' | tr -d '=\n'; }
    record_import_id() { jq -r '.identity.import_id' "$1"; }
    F_ADDR="module.overture_tiles_final.aws_cloudwatch_log_group.batch"
    F_RECORD="$ESTATE/.tofu-records/tofu-records/$ESTATE_NAME/aws_cloudwatch_log_group/$(record_key "$F_ADDR")"
    F_OLD_LOGGROUP_NAME="/aws/batch/${ESTATE_NAME}"

    log ""
    log "=== F0. capture the live log group and its record ahead of the forced replace ==="
    [ -f "$F_RECORD" ] || fail "no local record file found for $F_ADDR ahead of day2_replace"
    F_OLD_IMPORT_ID="$(record_import_id "$F_RECORD")"
    # aws_cloudwatch_log_group's import ID is the log group NAME, not its
    # ARN (the same shape corpus-lambda-simple's own log group needed in
    # this unit) - checked against the record file directly.
    [ "$F_OLD_IMPORT_ID" = "$F_OLD_LOGGROUP_NAME" ] || fail "the record for $F_ADDR names $F_OLD_IMPORT_ID ahead of day2_replace, not $F_OLD_LOGGROUP_NAME"
    F_OLD_ADDR_TAG="$(awsl logs list-tags-log-group --log-group-name "$F_OLD_LOGGROUP_NAME" --query 'tags."tofu-address"' --output text)"
    [ "$F_OLD_ADDR_TAG" = "module.overture_tiles_final.aws_cloudwatch_log_group.batch" ] \
      || fail "$F_OLD_LOGGROUP_NAME does not carry tofu-address=module.overture_tiles_final.aws_cloudwatch_log_group.batch ahead of day2_replace"
    log "  $F_OLD_LOGGROUP_NAME, record import_id=$F_OLD_IMPORT_ID (the log group's name, not its ARN), tofu-address=$F_OLD_ADDR_TAG"

    if [ "${BREAK:-}" = "replace" ]; then
      log "=== F1 (BREAK=replace). manufacture the coexistence a skipped destroy half would leave behind ==="
      # A second, distinct live log group carrying the SAME tofu-address
      # as the one a genuine replace would destroy - the state "skip the
      # destroy half" of a create-before-destroy replace would leave,
      # produced directly via the AWS CLI rather than by actually
      # interrupting an apply (day2_crash, stage 10, owns testing a real
      # interruption).
      BREAK_COLLISION_NAME="/aws/batch/${ESTATE_NAME}-collision"
      awsl logs create-log-group --log-group-name "$BREAK_COLLISION_NAME" \
        --tags "tofu-estate=$ESTATE_NAME,tofu-address=module.overture_tiles_final.aws_cloudwatch_log_group.batch" \
        >/dev/null || fail "BREAK=replace: could not create the collision log group"
      BREAK_PLAN_OUT="$(cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color 2>&1)"; BREAK_PLAN_RC=$?
      awsl logs delete-log-group --log-group-name "$BREAK_COLLISION_NAME" >/dev/null 2>&1 || true
      [ "$BREAK_PLAN_RC" -ne 0 ] \
        || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -20; fail "BREAK=replace: the plan succeeded with two live objects claiming the same tofu-address - it must report the collision, not propose nothing"; }
      grep -qF 'Two live resources claiming one address' <<< "$BREAK_PLAN_OUT" \
        || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -20; fail "BREAK=replace: the plan failed for a reason other than the address collision - this stage's check is not load-bearing"; }
      log "  BREAK=replace: choudoufu correctly refused with a named collision (two live resources claiming one address) rather than silently proposing nothing - the Break text's own outcome"
    else
      log "=== F1. choudoufu: supply name_overrides.cloudwatch_log_group, forcing a replace at the same declared address ==="
      perl -pi -e "s{(execution_role_policy = \"${ESTATE_NAME}-execution-logs-policy\"\n)}{\$1    cloudwatch_log_group   = \"/aws/batch/${ESTATE_NAME}-v2\"\n}" "$ESTATE/main.tf"
      grep -q 'cloudwatch_log_group   = "/aws/batch/' "$ESTATE/main.tf" || fail "adding name_overrides.cloudwatch_log_group did not match - the corpus pin has moved"
      F_NEW_NAME="/aws/batch/${ESTATE_NAME}-v2"

      F_PLAN_OUT="$(cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color 2>&1)"; F_PLAN_RC=$?
      [ "$F_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_PLAN_OUT" | tail -40; fail "the day2_replace plan exited $F_PLAN_RC"; }
      grep -qE '^  # module\.overture_tiles_final\.aws_cloudwatch_log_group\.batch must be replaced' <<< "$F_PLAN_OUT" \
        || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not propose replacing the log group when its name changes"; }
      grep -qE '~ +name +=.+forces replacement' <<< "$F_PLAN_OUT" \
        || { printf '%s\n' "$F_PLAN_OUT"; fail "the plan does not mark the log group's name as forcing replacement"; }
      grep -qE '^  # module\.overture_tiles_final\.aws_iam_role_policy\.execution_logs will be updated in-place' <<< "$F_PLAN_OUT" \
        || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not propose updating the execution role's inline log policy in-place when the log group's arn changes"; }
      grep -qE '^  # module\.overture_tiles_final\.aws_batch_job_definition\.tiles\["base"\] will be updated in-place' <<< "$F_PLAN_OUT" \
        || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not propose updating the job definition in-place when the log group's name changes - it may register a new revision as a replace instead, matching F-ORACLE's own recorded shape at \$WORK/replace-oracle-plan.log"; }
      F_OTHER="$(grep -E '^  # .+ (will be (destroyed|created)|must be replaced)' <<< "$F_PLAN_OUT" | grep -v 'aws_cloudwatch_log_group\.batch' || true)"
      [ -z "$F_OTHER" ] \
        || { printf '%s\n' "$F_OTHER"; fail "choudoufu proposes a destroy, create or replace beyond the log group's own forced replace"; }
      log "  choudoufu: exactly one forced replace at the same declared address (module.overture_tiles_final.aws_cloudwatch_log_group.batch), cascading into the expected in-place updates (execution role's inline log policy, job definition)"

      F_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; F_APPLY_RC=$?
      [ "$F_APPLY_RC" -eq 0 ] || { printf '%s\n' "$F_APPLY_OUT" | tail -40; fail "the day2_replace apply exited $F_APPLY_RC"; }
      grep -qE 'Apply complete' <<< "$F_APPLY_OUT" || { printf '%s\n' "$F_APPLY_OUT" | tail -20; fail "the day2_replace apply did not complete"; }

      F_OLD_STILL="$(awsl logs describe-log-groups --log-group-name-prefix "$F_OLD_LOGGROUP_NAME" --query "logGroups[?logGroupName=='$F_OLD_LOGGROUP_NAME']" --output text 2>&1)"
      [ -z "$F_OLD_STILL" ] || { echo "$F_OLD_STILL"; fail "$F_OLD_LOGGROUP_NAME still exists after the replace - the old object was orphaned, not destroyed"; }
      log "  $F_OLD_LOGGROUP_NAME no longer exists (confirmed via the AWS CLI, not through choudoufu's own report)"

      F_NEW_ADDR_TAG="$(awsl logs list-tags-log-group --log-group-name "$F_NEW_NAME" --query 'tags."tofu-address"' --output text)"
      [ "$F_NEW_ADDR_TAG" = "module.overture_tiles_final.aws_cloudwatch_log_group.batch" ] \
        || fail "$F_NEW_NAME carries tofu-address=$F_NEW_ADDR_TAG after the replace, not module.overture_tiles_final.aws_cloudwatch_log_group.batch - the marker did not move onto the new object"
      log "  $F_NEW_NAME (the new object) carries tofu-address=$F_NEW_ADDR_TAG - the marker moved onto the new object, read via the AWS CLI"

      # THE RECORD STORE, asserted by value (HANDOFF's safety rule; the
      # #398-guard shape: a stale record still naming the destroyed object
      # would be exactly the wrong-marker failure that outranks a missing
      # one). The local record file at the SAME address must now hold the
      # NEW object's import_id, not the one captured in F0.
      F_NEW_IMPORT_ID="$(record_import_id "$F_RECORD")"
      [ "$F_NEW_IMPORT_ID" = "$F_NEW_NAME" ] \
        || fail "the record for $F_ADDR names $F_NEW_IMPORT_ID after the replace, not the new object's name $F_NEW_NAME - a stale record still claiming the destroyed object, the #398-guard shape"
      [ "$F_NEW_IMPORT_ID" != "$F_OLD_IMPORT_ID" ] \
        || fail "sanity: the record's import_id at $F_ADDR did not change at all across the replace"
      log "  record store: import_id $F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID at the same key ($F_ADDR) - read directly off the local record store file, not through choudoufu's own report"

      log "=== F2. one more plan: config and reality agree, no marker collision ==="
      F_FINAL_PLAN_OUT="$(cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color 2>&1)"; F_FINAL_PLAN_RC=$?
      [ "$F_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_FINAL_PLAN_OUT" | tail -40; fail "the post-replace plan exited $F_FINAL_PLAN_RC"; }
      grep -qE '^  # .+ will be' <<< "$F_FINAL_PLAN_OUT" \
        && { printf '%s\n' "$F_FINAL_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the post-replace plan proposes a resource change"; }
      log "  no resource action proposed, no marker collision. The replace is complete and invisible to the next plan."

      gauntlet_stage day2_replace pass "choudoufu: supplying module.overture_tiles_final's name_overrides.cloudwatch_log_group proposed exactly one replace at the same declared address (the log group; -/+ destroy and then create) cascading into two expected in-place updates (the execution role's inline log policy, the job definition) and nothing else; applied cleanly; the old object ($F_OLD_LOGGROUP_NAME) is confirmed gone and the new object ($F_NEW_NAME) carries the marker, both via the AWS CLI; the local record store's record at the same address now names the new object's import_id, not the destroyed one ($F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID); the next plan proposes no resource action; stock oracle on cold_deploy's own state (F-ORACLE) also proposes exactly one replace at the same address plus the same cascade (plan only, not applied); BREAK=replace confirms a manufactured marker collision is reported loudly rather than silently proposed as nothing. Scope note: this exercises OpenTofu's default destroy-then-create ordering, not the create_before_destroy variant the stage's Title names - see this section's own header comment."
    fi
    gauntlet_end_stage

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
    # untaggable OAC NOR the TAGGED, MARKED CloudFront distribution was
    # destroyed - only the still-declared bucket policy's in-place update
    # was proposed, with no diagnostic for either missing destroy.
    #
    # FIXED (gauntlet:overture-remove, 2026-08-25). The mechanism is NOT
    # classifyOrphans's "pending" block-key guard - instrumenting that
    # function directly showed it never even ran for this stage
    # (res.Orphans was empty on entry). It is two separate discovery-demand
    # gaps, both triggered the same way:
    # [declared.indexCountBlocks]'s own doc comment says a count block that
    # shrinks to zero still owns whatever is live, but the config-driven
    # per-type scan only runs for types something still declares an
    # instance of - count=0 leaves decl.types["aws_cloudfront_distribution"]
    # empty, so the type's own List call never happens and both children
    # fall back to whatever OTHER route can still find them:
    #   - the distribution (taggable) fell back to the estate-wide tag
    #     sweep, which needs internal/live/discovery/tagging.go's
    #     arnJoinTable to turn its ARN's "distribution" segment into a CFN
    #     type - no entry existed, so the sweep could not join it and
    #     reported it under "Not swept for removal ... NO_ARN_JOIN" rather
    #     than finding it.
    #   - the OAC (untaggable, server-assigned id, admitted through
    #     identity.LocatedType's schema-first "record rung" rather than a
    #     ratified table row) has no tags to sweep and no parent-derivable
    #     argument for parentReadSweep, so the ONLY place its identity
    #     survived was the estate's own record store (written by the #249
    #     convergence apply); internal/live/discovery/recordorphan_read.go's
    #     type gate required a RATIFIED row to admit a type, which
    #     structurally excludes every LocatedType-admitted one, even though
    #     reading exactly that store is the whole point of the file.
    # Fixed both: an arnJoinTable entry for cloudfront/distribution, and a
    # recordorphan_read.go gate that also accepts a LocatedType-admitted
    # type. Neither fix names a concrete type in control flow - the table
    # entry reaches every type whose ARN carries that service/segment pair,
    # and the gate reaches every LocatedType-admitted type with a
    # persisted record.
    gauntlet_begin_stage day2_remove
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
      || { grep -E '^  # .+ will be' <<< "$REMOVE_PLAN_OUT"; fail "choudoufu does not propose destroying module.overture_tiles_final.aws_cloudfront_distribution.tiles[0] - see the comment above this stage: a count-shrunk-to-zero type falls out of the config-driven scan and needs internal/live/discovery/tagging.go's arnJoinTable to be found by the tag sweep instead"; }
    grep -qE '^  # module\.overture_tiles_final\.aws_cloudfront_origin_access_control\.tiles\[0\] will be destroyed' <<< "$REMOVE_PLAN_OUT" \
      || { grep -E '^  # .+ will be' <<< "$REMOVE_PLAN_OUT"; fail "choudoufu does not propose destroying module.overture_tiles_final.aws_cloudfront_origin_access_control.tiles[0] - see the comment above this stage: an untaggable, record-located type's identity has to survive in the estate's record store, and internal/live/discovery/recordorphan_read.go's type gate has to accept a LocatedType-admitted type, not only a ratified one"; }
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
    gauntlet_end_stage

    # ══════════════════════════════════════════════════════════════════
    # PART G: CHANGE COUNT (day2_count, live/GAUNTLET.md #8, active -
    # issue #643 board repair sweep)
    # ══════════════════════════════════════════════════════════════════
    #
    # WHY A SYNTHETIC BLOCK. Every `count` this module declares is a
    # boolean create toggle, never a scalable set - checked against the
    # pinned corpus rather than assumed: `count = var.create_vpc ? 1 : 0`
    # (network.tf, seven resources), `count = var.create_s3_bucket ? 1 : 0`
    # (s3.tf, four), `count = var.create_cloudfront_distribution ? 1 : 0`
    # (cloudfront.tf, two), and three launch-template variants in batch.tf
    # gated on `var.launch_template.existing_id == null`. All of them
    # evaluate to 0 or 1 and nothing else, so scaling one is the day2_remove
    # shape (this crossing's own PART day2_remove above is literally that:
    # create_cloudfront_distribution flipped to false), not the day2_count
    # shape the stage's Proves text names ("every surviving instance keeps
    # its identity" needs a survivor).
    #
    # The module's one genuine for_each - `aws_batch_job_definition.tiles`
    # over `toset(var.themes)` - cannot carry it either, for a reason that
    # is about THIS crossing rather than about the type: this script's own
    # root config scopes `themes` to a single theme ("base") from stage 1
    # onward (see the SCOPING DECISION in the file header), so the set has
    # one member and there is nothing to scale down to. Widening it to two
    # would move every earlier stage's counted assertions - cold_deploy's
    # and greenfield's "26 resources", migrate's "16 of 26 stamped",
    # test_apply's 16-tagged-object count, and day2_rename's own
    # TAGGABLE_ADDRS list, which names aws_batch_job_definition.tiles["base"]
    # by value - and the stage that comes last is not allowed to reach back
    # and rewrite the nine that already reported. What shrinking that set
    # would actually plan is NOT claimed here: it was never measured,
    # because it was never a usable option.
    #
    # So this section adds the sanctioned fallback (live/GAUNTLET.md #8;
    # precedent reference-ec2-vpc's own Part F and corpus-iam-policy's Part
    # G): a NEW, self-contained synthetic count block -
    # aws_security_group.count_test, count = 2 - scaled 2 -> 1 -> 2,
    # entirely AFTER day2_remove's real, completed removal, at a root
    # address nothing else in this estate names. It reuses a type the
    # estate already exercises (aws_security_group.batch, network.tf) and
    # nothing references it, so no earlier stage's assertions move.
    #
    # WHY aws_security_group WITNESSES A DESTROY HERE. Probed directly
    # against this pin's floci with NO tofu in the loop before this section
    # was written (ghcr.io/lex00/floci@sha256:c55d74e1):
    #   - two SGs created in one VPC got distinct GroupIds; deleting index 1
    #     and recreating it under the SAME name minted a THIRD, different
    #     GroupId (sg-4a867487... -> sg-7aca7b44...), while index 0's
    #     GroupId was untouched throughout;
    #   - `describe-security-groups --group-ids <deleted-id>` returns an
    #     EMPTY LIST with EXIT 0 on this image, not an error. Every absence
    #     check below therefore reads a LENGTH through
    #     `--filters Name=group-id,Values=...` and `--query
    #     length(SecurityGroups)`, never an exit code (CLAUDE.md: "read
    #     verdict lines, never exit codes");
    #   - the tofu-address tag reads back by value off
    #     describe-security-groups, which is how the marker half is checked.
    # That is the same "surviving instance keeps its identity, the
    # destroyed one comes back under a new one" shape reference-ec2-vpc's
    # own Part F proves for this type.
    #
    # G0 is this stage's stock oracle (live/GAUNTLET.md #8: "Stock's plan
    # for the same count change, normalised"). Stock never had this count
    # block, so unlike day2_rename/day2_replace/day2_remove it cannot be
    # computed off cold_deploy's own state: it is a genuinely new plain-
    # OpenTofu working directory standing the SAME 2-instance block up
    # against $ENDPOINT (idle since day2_remove finished), with its own
    # tiny VPC standing in for the estate's. Its resources are named
    # -count-oracle- rather than -count-test- so the two can never be
    # confused by a tag lookup, AND it is torn down before G1 runs - the
    # trap corpus-iam-policy's own Part G recorded empirically (an oracle
    # left behind is an unmarked object a name lookup can pick instead).
    #
    # BREAK_COUNT=1 exercises this stage's own Break control instead of the
    # real checks: after the real scale-down plan, assert the WRONG
    # instance (count_test[0] rather than count_test[1]) was destroyed -
    # tools/gauntlet/stages.go's Break text for day2_count, verbatim:
    # "Expect a different instance to be destroyed; the assertion must
    # fail." Independent of BREAK, BREAK=2, BREAK=replace and
    # BREAK_STAGE5; only reachable on the real path, since day2_count
    # starts from day2_remove's real, completed removal.

    gauntlet_begin_stage day2_count

    # count_test_block <count> <vpc_id HCL expression> <name prefix>: the
    # one resource this stage scales, emitted identically for the estate
    # (G1-G4) and for the stock oracle (G0). Unquoted heredoc so the three
    # arguments interpolate; ${count.index} is escaped so bash never tries
    # to expand it as a parameter.
    count_test_block() {
      local n="$1" vpc_ref="$2" prefix="$3"
      cat <<COUNTEOF

resource "aws_security_group" "count_test" {
  count       = $n
  name        = "${prefix}-\${count.index}"
  description = "day2_count evidence (issue #643)"
  vpc_id      = $vpc_ref

  tags = {
    Name    = "${prefix}-\${count.index}"
    Project = "overture-tiles-crossing"
  }
}
COUNTEOF
    }

    # sg_id_by_name <Name tag value>: the GroupId, or the empty string.
    sg_id_by_name() {
      awsl ec2 describe-security-groups --filters "Name=tag:Name,Values=$1" \
        --query 'SecurityGroups[0].GroupId' --output text 2>/dev/null || true
    }
    # sg_count_by_id <GroupId>: how many live security groups carry that id.
    # A filter, never --group-ids, and a LENGTH, never an exit code - see
    # this section's header: a deleted id comes back as [] with exit 0.
    sg_count_by_id() {
      awsl ec2 describe-security-groups --filters "Name=group-id,Values=$1" \
        --query 'length(SecurityGroups)' --output text 2>/dev/null || echo 0
    }
    # sg_tag <GroupId> <tag key>: one tag's value, read straight off EC2.
    sg_tag() {
      awsl ec2 describe-security-groups --group-ids "$1" \
        --query "SecurityGroups[0].Tags[?Key=='$2'].Value | [0]" --output text 2>/dev/null || true
    }

    ORACLE_COUNT_DIR="$WORK/oracle-count"
    ORACLE_CT_PREFIX="${ESTATE_NAME}-count-oracle"
    CT_PREFIX="${ESTATE_NAME}-count-test"

    # write_count_oracle <count>: the oracle working directory's whole
    # main.tf, regenerated per scale step. skip_requesting_account_id stays
    # true here exactly as it does for $PLAIN: this is plain OpenTofu, no
    # identity-ARN import ever happens, and #345 does not arise.
    write_count_oracle() {
      cat > "$ORACLE_COUNT_DIR/main.tf" <<EOF
terraform {
  required_version = ">= 1.8"
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

resource "aws_vpc" "count_oracle" {
  cidr_block = "10.99.0.0/16"

  tags = {
    Name = "${ORACLE_CT_PREFIX}-vpc"
  }
}
EOF
      count_test_block "$1" "aws_vpc.count_oracle.id" "$ORACLE_CT_PREFIX" >> "$ORACLE_COUNT_DIR/main.tf"
    }

    log ""
    log "=== G0. day2_count stock oracle: stand the SAME 2-instance count block up with plain tofu, scale it to 1 and back, against the idle estate endpoint ==="
    mkdir -p "$ORACLE_COUNT_DIR"
    write_count_oracle 2
    [ -f "$PLAIN/.terraform.lock.hcl" ] && cp "$PLAIN/.terraform.lock.hcl" "$ORACLE_COUNT_DIR/.terraform.lock.hcl"
    ( cd "$ORACLE_COUNT_DIR" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$ORACLE_COUNT_DIR" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_count stock oracle's init failed"; }
    ORACLE_COUNT_APPLY_OUT="$(cd "$ORACLE_COUNT_DIR" && tofu apply -input=false -auto-approve -no-color 2>&1)"; ORACLE_COUNT_APPLY_RC=$?
    [ "$ORACLE_COUNT_APPLY_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_COUNT_APPLY_OUT" | tail -30; fail "the day2_count stock oracle's baseline apply failed"; }
    grep -qE 'Apply complete! Resources: 3 added' <<< "$ORACLE_COUNT_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$ORACLE_COUNT_APPLY_OUT"; fail "stock did not create exactly 3 resources (the oracle's own VPC plus 2 count-test security groups) for the day2_count oracle"; }
    ORACLE_SG0_ID="$(sg_id_by_name "${ORACLE_CT_PREFIX}-0")"
    ORACLE_SG1_ID="$(sg_id_by_name "${ORACLE_CT_PREFIX}-1")"
    [ -n "$ORACLE_SG0_ID" ] && [ "$ORACLE_SG0_ID" != "None" ] || fail "no oracle count_test[0] security group found by its Name tag"
    [ -n "$ORACLE_SG1_ID" ] && [ "$ORACLE_SG1_ID" != "None" ] || fail "no oracle count_test[1] security group found by its Name tag"
    [ "$ORACLE_SG0_ID" != "$ORACLE_SG1_ID" ] || fail "the oracle's two count_test instances came back with the same GroupId"
    log "  stock: 2 instances created, count_test[0]=$ORACLE_SG0_ID count_test[1]=$ORACLE_SG1_ID"

    write_count_oracle 1
    ORACLE_DOWN_PLAN_OUT="$(cd "$ORACLE_COUNT_DIR" && tofu plan -input=false -no-color 2>&1)"; ORACLE_DOWN_PLAN_RC=$?
    [ "$ORACLE_DOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | tail -30; fail "the day2_count stock oracle's scale-down plan exited $ORACLE_DOWN_PLAN_RC"; }
    grep -qE '^  # aws_security_group\.count_test\[1\] will be destroyed' <<< "$ORACLE_DOWN_PLAN_OUT" \
      || { grep -E '^  # .+ will be' <<< "$ORACLE_DOWN_PLAN_OUT"; fail "stock's scale-down plan does not destroy count_test[1]"; }
    grep -qE '^  # aws_security_group\.count_test\[0\] will be' <<< "$ORACLE_DOWN_PLAN_OUT" \
      && { grep -E '^  # .+ will be' <<< "$ORACLE_DOWN_PLAN_OUT"; fail "stock's scale-down plan touches count_test[0], which should be untouched"; }
    grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$ORACLE_DOWN_PLAN_OUT" \
      || { grep -E '^Plan: |^No changes' <<< "$ORACLE_DOWN_PLAN_OUT"; fail "stock's scale-down plan proposes something other than exactly one destroy"; }
    ORACLE_DOWN_APPLY_OUT="$(cd "$ORACLE_COUNT_DIR" && tofu apply -input=false -auto-approve -no-color 2>&1)"; ORACLE_DOWN_APPLY_RC=$?
    [ "$ORACLE_DOWN_APPLY_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_DOWN_APPLY_OUT" | tail -30; fail "the day2_count stock oracle's scale-down apply failed"; }
    grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$ORACLE_DOWN_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$ORACLE_DOWN_APPLY_OUT"; fail "the day2_count stock oracle's scale-down apply was not exactly one destroy"; }
    [ "$(sg_count_by_id "$ORACLE_SG0_ID")" = "1" ] || fail "stock's surviving count_test[0] ($ORACLE_SG0_ID) is not live after the scale-down"
    [ "$(sg_count_by_id "$ORACLE_SG1_ID")" = "0" ] || fail "stock's count_test[1] ($ORACLE_SG1_ID) still exists after the scale-down destroy"
    log "  stock: exactly one destroy (count_test[1]=$ORACLE_SG1_ID gone), count_test[0]=$ORACLE_SG0_ID still live"

    write_count_oracle 2
    ORACLE_UP_PLAN_OUT="$(cd "$ORACLE_COUNT_DIR" && tofu plan -input=false -no-color 2>&1)"; ORACLE_UP_PLAN_RC=$?
    [ "$ORACLE_UP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | tail -30; fail "the day2_count stock oracle's scale-up plan exited $ORACLE_UP_PLAN_RC"; }
    grep -qE '^  # aws_security_group\.count_test\[1\] will be created' <<< "$ORACLE_UP_PLAN_OUT" \
      || { grep -E '^  # .+ will be' <<< "$ORACLE_UP_PLAN_OUT"; fail "stock's scale-up plan does not create count_test[1]"; }
    grep -qE '^  # aws_security_group\.count_test\[0\] will be' <<< "$ORACLE_UP_PLAN_OUT" \
      && { grep -E '^  # .+ will be' <<< "$ORACLE_UP_PLAN_OUT"; fail "stock's scale-up plan touches count_test[0], which should be untouched"; }
    grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$ORACLE_UP_PLAN_OUT" \
      || { grep -E '^Plan: |^No changes' <<< "$ORACLE_UP_PLAN_OUT"; fail "stock's scale-up plan proposes something other than exactly one create"; }
    ORACLE_UP_APPLY_OUT="$(cd "$ORACLE_COUNT_DIR" && tofu apply -input=false -auto-approve -no-color 2>&1)"; ORACLE_UP_APPLY_RC=$?
    [ "$ORACLE_UP_APPLY_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_UP_APPLY_OUT" | tail -30; fail "the day2_count stock oracle's scale-up apply failed"; }
    grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$ORACLE_UP_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$ORACLE_UP_APPLY_OUT"; fail "the day2_count stock oracle's scale-up apply was not exactly one create"; }
    ORACLE_SG1_NEW_ID="$(sg_id_by_name "${ORACLE_CT_PREFIX}-1")"
    [ -n "$ORACLE_SG1_NEW_ID" ] && [ "$ORACLE_SG1_NEW_ID" != "None" ] || fail "no oracle count_test[1] security group found after the scale-up"
    [ "$ORACLE_SG1_NEW_ID" != "$ORACLE_SG1_ID" ] \
      || fail "stock's recreated count_test[1] came back with the SAME GroupId ($ORACLE_SG1_ID) it had before being destroyed - the oracle's own destroy was not real"
    [ "$(sg_id_by_name "${ORACLE_CT_PREFIX}-0")" = "$ORACLE_SG0_ID" ] || fail "stock's count_test[0] changed GroupId across the scale-up"
    log "  stock: exactly one create (count_test[1] recreated as $ORACLE_SG1_NEW_ID, was $ORACLE_SG1_ID), count_test[0]=$ORACLE_SG0_ID unchanged across the down-then-up cycle"

    # The oracle and the real choudoufu side share $ENDPOINT (it is idle,
    # not a second account), so the oracle's own objects are torn down here
    # before G1 creates anything: corpus-iam-policy's own Part G recorded
    # empirically that an oracle left behind is an UNMARKED object a name
    # lookup can pick up instead of the marked one. The -count-oracle-
    # naming above already makes that impossible; this makes it doubly so,
    # and leaves the account exactly as day2_remove left it.
    ORACLE_COUNT_DESTROY_OUT="$(cd "$ORACLE_COUNT_DIR" && tofu destroy -input=false -auto-approve -no-color 2>&1)"; ORACLE_COUNT_DESTROY_RC=$?
    [ "$ORACLE_COUNT_DESTROY_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_COUNT_DESTROY_OUT" | tail -30; fail "the day2_count stock oracle's teardown failed"; }
    grep -qE 'Destroy complete! Resources: 3 destroyed' <<< "$ORACLE_COUNT_DESTROY_OUT" \
      || { grep -E 'Destroy complete' <<< "$ORACLE_COUNT_DESTROY_OUT"; fail "the day2_count stock oracle's teardown was not exactly 3 destroys"; }
    [ "$(sg_count_by_id "$ORACLE_SG0_ID")" = "0" ] || fail "the oracle's count_test[0] ($ORACLE_SG0_ID) survived the oracle teardown"
    [ "$(sg_count_by_id "$ORACLE_SG1_NEW_ID")" = "0" ] || fail "the oracle's count_test[1] ($ORACLE_SG1_NEW_ID) survived the oracle teardown"
    log "  stock oracle torn down (3 destroyed): the shared endpoint is back to exactly what day2_remove left"

    log "=== G1. choudoufu: add aws_security_group.count_test, count = 2, at the estate root ==="
    grep -q 'module "overture_tiles_final" {' "$ESTATE/main.tf" \
      || fail "the estate's module block is not named overture_tiles_final at day2_count - day2_rename's own chain has moved"
    count_test_block 2 "module.overture_tiles_final.vpc_id" "$CT_PREFIX" >> "$ESTATE/main.tf"
    COUNT_ADD_PLAN_OUT="$(cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color 2>&1)"; COUNT_ADD_PLAN_RC=$?
    [ "$COUNT_ADD_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_ADD_PLAN_OUT" | tail -40; fail "the count-block-add plan exited $COUNT_ADD_PLAN_RC"; }
    grep -qF 'Plan: 2 to add, 0 to change, 0 to destroy.' <<< "$COUNT_ADD_PLAN_OUT" \
      || { grep -E '^Plan: |^No changes|^  # .+ will be' <<< "$COUNT_ADD_PLAN_OUT"; fail "adding the count block did not plan exactly 2 creates and nothing else"; }
    COUNT_ADD_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_ADD_APPLY_RC=$?
    [ "$COUNT_ADD_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_ADD_APPLY_OUT" | tail -40; fail "the count-block-add apply exited $COUNT_ADD_APPLY_RC"; }
    grep -qE 'Resources: 2 added, 0 changed, 0 destroyed' <<< "$COUNT_ADD_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$COUNT_ADD_APPLY_OUT"; fail "the count-block-add apply did not create exactly 2 resources"; }

    SG0_ID="$(sg_id_by_name "${CT_PREFIX}-0")"
    SG1_ID="$(sg_id_by_name "${CT_PREFIX}-1")"
    [ -n "$SG0_ID" ] && [ "$SG0_ID" != "None" ] || fail "no live count_test[0] security group found by its Name tag"
    [ -n "$SG1_ID" ] && [ "$SG1_ID" != "None" ] || fail "no live count_test[1] security group found by its Name tag"
    [ "$SG0_ID" != "$SG1_ID" ] || fail "count_test[0] and count_test[1] came back with the same GroupId"
    # The markers, by value, off EC2 - never through choudoufu's own report.
    # live/MARKERS.md: a count instance's tag value is colon-escaped
    # (aws_eip.this[2] -> aws_eip.this:2), and count_test is a ROOT address,
    # so there is no module prefix on it.
    SG0_ADDR_TAG="$(sg_tag "$SG0_ID" tofu-address)"
    SG1_ADDR_TAG="$(sg_tag "$SG1_ID" tofu-address)"
    [ "$SG0_ADDR_TAG" = 'aws_security_group.count_test:0' ] \
      || fail "count_test[0]'s live tofu-address tag is $SG0_ADDR_TAG, not aws_security_group.count_test:0 (live/MARKERS.md: a count instance's tag value is colon-escaped)"
    [ "$SG1_ADDR_TAG" = 'aws_security_group.count_test:1' ] \
      || fail "count_test[1]'s live tofu-address tag is $SG1_ADDR_TAG, not aws_security_group.count_test:1"
    SG0_ESTATE_TAG="$(sg_tag "$SG0_ID" tofu-estate)"
    [ "$SG0_ESTATE_TAG" = "$ESTATE_NAME" ] || fail "count_test[0] carries tofu-estate=$SG0_ESTATE_TAG, not $ESTATE_NAME"
    # The block's OWN create-time tag has to have survived the marker
    # stamp: a tag write that replaces instead of merging is how a live
    # object silently loses either its own tags or its markers (the same
    # assertion stage 2 makes about the job queue's Project tag).
    [ "$(sg_tag "$SG0_ID" Project)" = "overture-tiles-crossing" ] \
      || fail "count_test[0] lost its own create-time Project tag when the markers were stamped"
    log "  2 instances created: index 0 = $SG0_ID (tofu-address=$SG0_ADDR_TAG, tofu-estate=$SG0_ESTATE_TAG), index 1 = $SG1_ID (tofu-address=$SG1_ADDR_TAG) - all read via the AWS CLI"

    COUNT_NOOP_PLAN_OUT="$(cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color 2>&1)"; COUNT_NOOP_PLAN_RC=$?
    [ "$COUNT_NOOP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_NOOP_PLAN_OUT" | tail -40; fail "the post-add plan exited $COUNT_NOOP_PLAN_RC"; }
    grep -qE 'No changes\.' <<< "$COUNT_NOOP_PLAN_OUT" \
      || { grep -E '^Plan: |^  # .+ will be' <<< "$COUNT_NOOP_PLAN_OUT"; fail "the plan right after adding the count block is not empty - the new instances did not bind their own markers cleanly"; }
    log "  No changes - both new instances bind straight off their own markers on the next stateless live-plan"

    log "=== G2. scale count down: 2 -> 1 ==="
    perl -pi -e 's/^  count       = 2$/  count       = 1/' "$ESTATE/main.tf"
    grep -qE '^  count       = 1$' "$ESTATE/main.tf" || fail "the scale-down edit did not match aws_security_group.count_test's own count line"
    COUNT_DOWN_PLAN_OUT="$(cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color 2>&1)"; COUNT_DOWN_PLAN_RC=$?
    [ "$COUNT_DOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | tail -40; fail "the scale-down plan exited $COUNT_DOWN_PLAN_RC"; }

    if [ "${BREAK_COUNT:-}" = "1" ]; then
      # The control, written the way the Break text words it: the SAME
      # assertion shape the real path uses below, pointed at the wrong
      # index. "The assertion must fail" means this stage must report
      # verdict=fail, not merely log that it noticed - so both arms below
      # call fail(), which reports `GAUNTLET stage=day2_count verdict=fail`
      # through CURRENT_STAGE. The first arm is the expected outcome (the
      # plan does NOT destroy index 0, so the wrong-instance assertion does
      # not hold); the second arm fires only if index 0 really was the one
      # destroyed, which would mean the real assertion below is not
      # load-bearing at all.
      log "  BREAK_COUNT=1: asserting the WRONG instance (count_test[0]) was destroyed instead of count_test[1]"
      grep -qE '^  # aws_security_group\.count_test\[0\] will be destroyed' <<< "$COUNT_DOWN_PLAN_OUT" \
        || { grep -E '^  # .+ will be' <<< "$COUNT_DOWN_PLAN_OUT"; fail "BREAK_COUNT=1 (expected): the scale-down plan does NOT destroy aws_security_group.count_test[0], so the wrong-instance assertion does not hold - exactly what day2_count's Break text requires (\"Expect a different instance to be destroyed; the assertion must fail\"). The real assertion on the non-BREAK path is load-bearing."; }
      fail "BREAK_COUNT=1: the scale-down plan really does destroy count_test[0] - the WRONG instance was destroyed, and the real assertion on the non-BREAK path is not load-bearing"
    else
      grep -qE '^  # aws_security_group\.count_test\[1\] will be destroyed' <<< "$COUNT_DOWN_PLAN_OUT" \
        || { grep -E '^  # .+ will be' <<< "$COUNT_DOWN_PLAN_OUT"; fail "choudoufu's scale-down plan does not destroy count_test[1]"; }
      grep -qE '^  # aws_security_group\.count_test\[0\] will be' <<< "$COUNT_DOWN_PLAN_OUT" \
        && { grep -E '^  # .+ will be' <<< "$COUNT_DOWN_PLAN_OUT"; fail "choudoufu's scale-down plan touches count_test[0], which should be untouched"; }
      grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$COUNT_DOWN_PLAN_OUT" \
        || { grep -E '^Plan: |^No changes' <<< "$COUNT_DOWN_PLAN_OUT"; fail "choudoufu's scale-down plan proposes something other than exactly one destroy"; }
      log "  choudoufu: exactly one destroy (count_test[1]), count_test[0] untouched - the same shape the G0 stock oracle showed"

      COUNT_DOWN_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_DOWN_APPLY_RC=$?
      [ "$COUNT_DOWN_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_DOWN_APPLY_OUT" | tail -40; fail "the scale-down apply exited $COUNT_DOWN_APPLY_RC"; }
      grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$COUNT_DOWN_APPLY_OUT" \
        || { grep -E 'Apply complete' <<< "$COUNT_DOWN_APPLY_OUT"; fail "the scale-down apply was not exactly one destroy"; }

      # The survivor and the casualty, both read back through the AWS CLI,
      # never through choudoufu's own report.
      [ "$(sg_count_by_id "$SG0_ID")" = "1" ] \
        || fail "count_test[0] ($SG0_ID) is no longer live after the scale-down - the WRONG instance was destroyed"
      [ "$(sg_id_by_name "${CT_PREFIX}-0")" = "$SG0_ID" ] \
        || fail "count_test[0]'s live GroupId changed across the scale-down (was $SG0_ID) - it was destroyed and recreated, not left alone"
      [ "$(sg_count_by_id "$SG1_ID")" = "0" ] \
        || fail "count_test[1] ($SG1_ID) still exists in the live account after the scale-down destroy"
      SG0_ADDR_AFTER_DOWN="$(sg_tag "$SG0_ID" tofu-address)"
      [ "$SG0_ADDR_AFTER_DOWN" = 'aws_security_group.count_test:0' ] \
        || fail "count_test[0]'s tofu-address tag changed across the scale-down: $SG0_ADDR_AFTER_DOWN"
      log "  $SG1_ID (count_test[1]) no longer exists (0 security groups carry that id); $SG0_ID (count_test[0]) unchanged GroupId and marker - all read via the AWS CLI"

      log "=== G3. scale count back up: 1 -> 2 ==="
      perl -pi -e 's/^  count       = 1$/  count       = 2/' "$ESTATE/main.tf"
      grep -qE '^  count       = 2$' "$ESTATE/main.tf" || fail "the scale-up edit did not match aws_security_group.count_test's own count line"
      COUNT_UP_PLAN_OUT="$(cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color 2>&1)"; COUNT_UP_PLAN_RC=$?
      [ "$COUNT_UP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | tail -40; fail "the scale-up plan exited $COUNT_UP_PLAN_RC"; }
      grep -qE '^  # aws_security_group\.count_test\[1\] will be created' <<< "$COUNT_UP_PLAN_OUT" \
        || { grep -E '^  # .+ will be' <<< "$COUNT_UP_PLAN_OUT"; fail "choudoufu's scale-up plan does not create count_test[1]"; }
      grep -qE '^  # aws_security_group\.count_test\[0\] will be' <<< "$COUNT_UP_PLAN_OUT" \
        && { grep -E '^  # .+ will be' <<< "$COUNT_UP_PLAN_OUT"; fail "choudoufu's scale-up plan touches count_test[0], which should be untouched"; }
      grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$COUNT_UP_PLAN_OUT" \
        || { grep -E '^Plan: |^No changes' <<< "$COUNT_UP_PLAN_OUT"; fail "choudoufu's scale-up plan proposes something other than exactly one create"; }
      log "  choudoufu: exactly one create (count_test[1]), count_test[0] untouched - the same shape the G0 stock oracle showed"

      COUNT_UP_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_UP_APPLY_RC=$?
      [ "$COUNT_UP_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_UP_APPLY_OUT" | tail -40; fail "the scale-up apply exited $COUNT_UP_APPLY_RC"; }
      grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$COUNT_UP_APPLY_OUT" \
        || { grep -E 'Apply complete' <<< "$COUNT_UP_APPLY_OUT"; fail "the scale-up apply was not exactly one create"; }

      SG1_NEW_ID="$(sg_id_by_name "${CT_PREFIX}-1")"
      [ -n "$SG1_NEW_ID" ] && [ "$SG1_NEW_ID" != "None" ] || fail "no live count_test[1] security group found by its Name tag after the scale-up"
      [ "$SG1_NEW_ID" != "$SG1_ID" ] \
        || fail "count_test[1] came back with the SAME GroupId ($SG1_ID) it had before being destroyed - the destroy in G2 was not real"
      SG1_NEW_ADDR_TAG="$(sg_tag "$SG1_NEW_ID" tofu-address)"
      [ "$SG1_NEW_ADDR_TAG" = 'aws_security_group.count_test:1' ] \
        || fail "the recreated count_test[1] ($SG1_NEW_ID) carries tofu-address=$SG1_NEW_ADDR_TAG, not aws_security_group.count_test:1"
      [ "$(sg_id_by_name "${CT_PREFIX}-0")" = "$SG0_ID" ] \
        || fail "count_test[0]'s live GroupId changed across the scale-up (was $SG0_ID)"
      [ "$(sg_tag "$SG0_ID" tofu-address)" = 'aws_security_group.count_test:0' ] \
        || fail "count_test[0]'s tofu-address tag changed across the scale-up"
      log "  count_test[1] recreated under a NEW GroupId ($SG1_NEW_ID, was $SG1_ID), tofu-address=$SG1_NEW_ADDR_TAG; count_test[0] ($SG0_ID) untouched throughout the down-then-up cycle - all read via the AWS CLI"

      log "=== G4. one more plan: config and reality agree, nothing left to propose ==="
      COUNT_FINAL_PLAN_OUT="$(cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color 2>&1)"; COUNT_FINAL_PLAN_RC=$?
      [ "$COUNT_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_FINAL_PLAN_OUT" | tail -40; fail "the post-scale-up plan exited $COUNT_FINAL_PLAN_RC"; }
      grep -qE 'No changes\.' <<< "$COUNT_FINAL_PLAN_OUT" \
        || { grep -E '^Plan: |^  # .+ will be' <<< "$COUNT_FINAL_PLAN_OUT"; fail "the post-scale-up plan is not empty"; }
      log "  No changes. The scale-down-then-up cycle is complete and invisible to the next plan."

      log ""
      log "PART G (day2_count): PASS"
      gauntlet_stage day2_count pass "choudoufu: scaling aws_security_group.count_test from 2 to 1 destroyed exactly count_test[1] (0 add, 0 change, 1 destroy), leaving count_test[0]'s live GroupId ($SG0_ID) and its tofu-address marker (aws_security_group.count_test:0, colon-escaped per live/MARKERS.md) unchanged, both read back through the AWS CLI; scaling back from 1 to 2 created exactly count_test[1] (1 add, 0 change, 0 destroy) under a NEW GroupId ($SG1_ID -> $SG1_NEW_ID) carrying tofu-address=aws_security_group.count_test:1, while count_test[0] stayed untouched throughout; every absence check reads length(SecurityGroups) through a group-id FILTER, because describe-security-groups --group-ids on a deleted id returns an empty list with exit 0 on this emulator pin; the next stateless live-plan is empty. Stock oracle (G0): the identical 2-instance block stood up with plain tofu in its own working directory against the same idle endpoint shows the identical shape - destroy the higher index only (0 add, 0 change, 1 destroy), create the higher index back under a new GroupId (1 add, 0 change, 0 destroy), the lower index's GroupId unchanged both times - then torn down (3 destroyed) before the choudoufu side ran. SYNTHETIC BLOCK, and why: every count this module declares is a boolean create toggle (create_vpc x7, create_s3_bucket x4, create_cloudfront_distribution x2, three launch_template variants gated on existing_id == null), never a scalable set, so scaling one is the day2_remove shape this script already runs, not a shape with a survivor; and its one real for_each (aws_batch_job_definition.tiles over toset(var.themes)) is scoped by this crossing's own root config to a single theme from stage 1 onward, so it has nothing to scale down to and widening it would move every earlier stage's counted assertions (26 resources, 16 stamped, 16 tagged objects, day2_rename's own 16-address list). What shrinking that set would plan is not claimed: it was never measured, because it was never a usable option. Sanctioned fallback per live/GAUNTLET.md #8, precedent reference-ec2-vpc Part F and corpus-iam-policy Part G. It reuses a type this estate already exercises (aws_security_group.batch), sits at a root address nothing else names, and runs entirely after day2_remove, so no earlier stage's assertions move. BREAK_COUNT=1 asserts the WRONG instance (count_test[0]) was destroyed and correctly reports fail."
      log ""
    fi
    gauntlet_end_stage
  fi
fi
gauntlet_end_stage

gauntlet_end

log ""
log "=== SUMMARY: stage 1 PASS; stage 2 PASS; stage 3 PASS (empty plan, S3 bucket"
log "=== and OAC identities verified by value); stage 4 PASS (no-op apply, object"
log "=== count and identities unchanged); stage 5 PASS (VPC Name-tag drift"
log "=== detected and reconverged, verified against the stock oracle); day2_count"
log "=== PASS (synthetic count block scaled 2 -> 1 -> 2, survivor's GroupId and"
log "=== marker unchanged, casualty back under a new GroupId, stock oracle agrees) ==="
