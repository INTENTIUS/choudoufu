#!/usr/bin/env bash
set -uo pipefail

# terraform-aws-modules/terraform-aws-vpc's flagship "complete" example
# (.corpus/vpc/examples/complete, pinned in live/corpus-manifest.json at tag
# v6.6.1, commit 3ffbd46f), crossed through choudoufu against floci - the
# real, five-stage pipeline this goal's crossings follow (cold deploy,
# migrate, test plan, test apply, drift and reconverge), not the offline
# lint/identity check every earlier instrument here already runs. This
# module is one of the most-forked AWS Terraform modules that exists, and
# "complete" is its own stress test: 62 resources across the module proper
# (VPC, ~18 subnets across 6 subnet groups, NAT gateway, internet gateway,
# VPN gateway + 3 customer gateways, DHCP options, default-VPC-management
# blocks left at their default of unused) plus its vpc-endpoints submodule
# (6 endpoints: s3, dynamodb, ecs, ecr.api, ecr.dkr, rds) plus one root
# security group.
#
# THE ONE DELTA, same discipline live/e2e/corpus-iam-policy/run.sh
# documents: the example's own provider block gains the standard emulator
# connection flags, and its version constraint is pinned to the exact
# provider version this checkout's admission tables were generated against
# (6.59.0) for reproducibility. No resource in the example is edited,
# removed, or parameterized away - stage 1 runs the module exactly as
# terraform-aws-modules publishes it.
#
# STAGE-BY-STAGE SHAPE (issue #274's five-stage pipeline; see
# live/corpus-crossing-manifest.json):
#
#   1. COLD DEPLOY   plain `terraform apply` (real HashiCorp terraform, not
#                     choudoufu) against the unmodified example. No live
#                     block, no choudoufu awareness at all - the honest
#                     test that the estate is real and buildable, and the
#                     source of genuinely unmarked live infrastructure for
#                     stage 2 to adopt.
#   2. MIGRATE        `choudoufu live-import -state=<plain's state>
#                     -estate=... -approve` against that cold-deployed
#                     state - the standard adoption path, since
#                     terraform-aws-vpc's own default output attributes
#                     mean a bare `choudoufu plan` cannot auto-adopt every
#                     type here either (reference-ec2-vpc's own script
#                     found the same gap for aws_instance/
#                     aws_internet_gateway; the same reasoning applies to
#                     several types this module creates).
#   3. TEST PLAN      delete the state file, `choudoufu live-plan`, assert
#                     the plan is EMPTY *and* assert a representative set of
#                     rendered identity strings against the AWS CLI's own
#                     answer - HANDOFF.md's own standing bar: "convergence
#                     is not evidence an identity is right, assert the
#                     rendered identity itself."
#   4. TEST APPLY     apply that empty plan; assert a genuine no-op by
#                     comparing the estate's tagged-object count in floci
#                     before and after.
#   5. DRIFT AND      mutate one live object out of band via the AWS CLI
#      RECONVERGE     directly against floci (a tag value on one subnet),
#                     replan, and assert the diff proposes fixing exactly
#                     that one object and nothing else.
#
# BREAK=1 corrupts one expected identity string ahead of stage 3's
# assertion, and separately corrupts the drift assertion in stage 5, so
# both assertions are proven non-vacuous rather than a grep that always
# matches - grep a couple of the older corpus-* scripts for this pattern.
#
#   bash live/e2e/corpus-vpc-complete/run.sh
#
# Needs Docker and the AWS CLI, and the real `terraform` binary on PATH for
# stage 1 (`tofu` also works - see TF_COLD_BIN below). .corpus is read,
# never written: the module and its example are copied out to a temp
# directory first, same as every other corpus crossing.
#
# Env overrides:
#   TOFU_BIN      path to a prebuilt choudoufu binary; skips the `go build`.
#   TF_COLD_BIN   the plain binary for stage 1 (default: `terraform` on
#                 PATH). Set to a `tofu` binary to use stock OpenTofu
#                 instead - the header comment's "or tofu apply" choice.
#   FLOCI_PORT    host port for the emulator (default 4713, clear of
#                 run.sh's 4566 and every other live/e2e fixture's port).
#   FLOCI_IMAGE   the emulator image; defaults to the digest pin in
#                 live/floci-image.
#   BREAK         set to 1 to corrupt an expected identity string and a
#                 drift assertion, proving both are load-bearing.
#
# WHERE THIS ESTATE STANDS, re-verified 2026-08-21 by a real run against
# ghcr.io/lex00/floci@sha256:cdd50ec0: stages 1 and 2 PASS. Stage 1
# cold-deploys all 62 resources ("Apply complete! Resources: 62 added");
# stage 2 stamps 40 of them, skips 22 as untaggable, fails none, and all
# three sampled identities read back through the AWS CLI by value.
#
# Stage 1 was blocked for two prior sessions on two successive floci gaps in
# the same resource, both now fixed upstream: lex00/floci#70 (Create/
# ModifyCacheSubnetGroup read SubnetIds under the generic member key while
# ElastiCache's service model overrides it to SubnetIdentifier) and
# lex00/floci#71 (ElastiCache served no tagging actions at all, so the AWS
# provider's unconditional ListTagsForResource on every
# aws_elasticache_subnet_group read 400'd).
#
# KNOWN, NOT PAPERED OVER: stage 3 still fails, and the reason has moved
# twice. choudoufu #346 (an identity-resolution refusal) and choudoufu #355
# (discovery aborting on the account's untagged default DHCP options set) are
# both fixed and neither diagnostic appears in a run any more. As of
# 2026-08-21, against ghcr.io/lex00/floci@sha256:cdd50ec0, live-plan exits 0
# and renders a full plan - and what stage 3's empty-plan assertion catches is
# three things behind those two, none of them a choudoufu defect:
#
#   1. The first live-plan after live-import proposes adding tofu-slot to 31
#      objects. That is deliberate product behavior, not drift:
#      live/e2e/corpus-iam-policy/run.sh's "THE TOFU-SLOT FINDING" has the
#      whole mechanism (a slot is minted from a counter over the live set,
#      which live-import's one-state-file view cannot compute), and three
#      other crossing scripts fold a convergence apply into their stage 2.
#      This one does not, because it had never reached stage 3 to notice.
#   2. That convergence apply cannot run here yet: floci answers
#      "UnsupportedOperation: Operation ModifyVpcEndpoint is not supported"
#      for each of the four interface endpoints. 26 of the 31 slots do land.
#      Filed with the two below as lex00/floci#97.
#   3. The replan after that reads "Plan: 3 to add, 5 to change, 3 to
#      destroy", and all three replacements are floci read fidelity rather
#      than drift: DescribeNatGateways returns neither allocation_id nor
#      subnet_id, DescribeVpnGateways returns an availability_zone the
#      configuration never set, and DescribeVpcEndpoints reads policy,
#      route_table_ids, subnet_ids and cidr_blocks back empty.
#
# So the convergence apply is NOT folded into stage 2 here the way
# corpus-iam-policy and corpus-dynamodb-table-basic fold theirs: doing that
# today would turn a genuinely passing stage 2 into a failing one over
# ModifyVpcEndpoint, which would report this estate as worse than it is. It
# goes in once floci serves that call.
#
# Nothing here is routed around (no -target, no resource removed from the
# example): the script runs the real module and reports the real result, per
# this goal's own standing rule that a partial, accurate failure is worth
# more than a green run that does not hold up.
#
# ALSO ESTABLISHED, 2026-08-21: the "39 objects carry tofu-estate after
# migration" line below disagrees with the "40 stamped" line above it, and
# the 40 is the right number. The missing object is
# aws_redshift_subnet_group.redshift[0] - it carries both markers, readable
# through `aws redshift describe-cluster-subnet-groups`, but floci's
# resourcegroupstaggingapi does not index Redshift resources, so the
# get-resources count this script logs cannot see it. A floci gap in the
# reporting line, not a stamping gap - lex00/floci#97 carries it too.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SRC_MODULE="$ROOT/.corpus/vpc"
SRC_EXAMPLE="$ROOT/.corpus/vpc/examples/complete"
WORK="$(mktemp -d)"
PLAIN="$WORK/plain/vpc/examples/complete"
ADOPTED="$WORK/adopted/vpc/examples/complete"
FLOCI_PORT="${FLOCI_PORT:-4713}"
FLOCI_NAME="choudoufu-corpus-vpc-complete-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="vpc-complete-crossing"

# What the estate is, asserted rather than described. 62 resource instances, of
# which 40 carry a tags argument in the AWS provider's schema and 22 do not - see
# stage 2's own comment for why that split is the invariant rather than a gap.
INSTANCES=62
TAGGABLE=40
UNTAGGABLE=22
REGION="eu-west-1"
TF_COLD_BIN="${TF_COLD_BIN:-terraform}"

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

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
command -v "$TF_COLD_BIN" >/dev/null 2>&1 || fail "TF_COLD_BIN=$TF_COLD_BIN is not on PATH - needed for stage 1's plain apply"
[ -d "$SRC_MODULE" ] || fail "$SRC_MODULE is missing - run 'just corpus-fetch' first"
[ -d "$SRC_EXAMPLE" ] || fail "$SRC_EXAMPLE is missing - run 'just corpus-fetch' first"

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

copy_estate() { # copy_estate <dest-root>: preserves the module's own relative source paths
  local dest="$1"
  mkdir -p "$dest"
  cp -R "$SRC_MODULE" "$dest/vpc"
  rm -rf "$dest/vpc/examples"
  mkdir -p "$dest/vpc/examples/complete"
  cp -R "$SRC_EXAMPLE"/*.tf "$dest/vpc/examples/complete/"
  rm -rf "$dest/vpc/examples/complete/.terraform" "$dest/vpc/examples/complete/.terraform.lock.hcl"
}

# emulator_delta <example-dir>: the one onboarding delta - provider
# connection flags and a pinned provider version, nothing else.
emulator_delta() {
  local ex="$1"
  perl -0pi -e 's/(provider "aws" \{\n  region = local\.region\n)\}/$1\n  access_key                  = "test"\n  secret_key                  = "test"\n  skip_credentials_validation = true\n  skip_metadata_api_check     = true\n  skip_requesting_account_id  = true\n  s3_use_path_style           = true\n}/' "$ex/main.tf"
  grep -q 's3_use_path_style' "$ex/main.tf" || fail "the emulator delta did not match main.tf's provider block - the corpus pin has moved"
  perl -0pi -e 's/version = ">= 6\.28"/version = "= 6.59.0"/' "$ex/versions.tf"
  grep -q '= 6.59.0' "$ex/versions.tf" || fail "the version pin delta did not match versions.tf - the corpus pin has moved"
}

copy_estate "$WORK/plain"
emulator_delta "$PLAIN"
log "  module + example copied out of .corpus into $WORK/plain (stage 1: plain terraform)"

copy_estate "$WORK/adopted"
emulator_delta "$ADOPTED"
perl -0pi -e 's/(required_providers \{\n    aws = \{\n      source  = "hashicorp\/aws"\n      version = "= 6\.59\.0"\n    \}\n  \}\n)\}/$1\n  live {\n    estate = "'"$ESTATE"'"\n  }\n}/' "$ADOPTED/versions.tf"
grep -q "estate = \"$ESTATE\"" "$ADOPTED/versions.tf" || fail "the live-block delta did not match versions.tf"
log "  module + example copied out of .corpus into $WORK/adopted (stages 2-5: choudoufu, live block added)"

# ── 1. floci ────────────────────────────────────────────────────────────────
log "=== 1. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"ec2"' <<< "${HEALTH:-}" && break
  sleep 2
done
grep -q '"ec2"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (ec2) at $ENDPOINT"
log "  healthy"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 1: COLD DEPLOY - plain terraform, no choudoufu, no live block
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=cold_deploy
log "=== STAGE 1: cold deploy ($TF_COLD_BIN apply, the real unmodified example) ==="
( cd "$PLAIN" && "$TF_COLD_BIN" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN" && "$TF_COLD_BIN" init -input=false -no-color 2>&1 | tail -30 ); fail "stage 1 init failed"; }
COLD_OUT="$(cd "$PLAIN" && "$TF_COLD_BIN" apply -input=false -auto-approve -no-color 2>&1)"; COLD_RC=$?
if [ "$COLD_RC" -ne 0 ]; then
  printf '%s\n' "$COLD_OUT" | grep -E '^Error' -A 6 | head -120
  fail "stage 1 (cold deploy) did not complete - see the real terraform errors above. This crossing does not route around them: see this script's header comment and the crossing's own report for whether each is a genuine floci gap and its size."
fi
grep -qE '^Apply complete!' <<< "$COLD_OUT" || fail "stage 1 apply produced no 'Apply complete!' line"
log "  $(grep -E '^Apply complete!' <<< "$COLD_OUT")"
[ -f "$PLAIN/terraform.tfstate" ] || fail "stage 1 left no state file to migrate from"

UNMARKED="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$UNMARKED" = "0" ] || fail "plain terraform's own objects already carry tofu-estate=$ESTATE before migration - this crossing proves nothing"
log "  confirmed unmarked: 0 objects carry tofu-estate=$ESTATE before migration"
gauntlet_stage cold_deploy pass "$(grep -E '^Apply complete!' <<< "$COLD_OUT"); 0 objects carry tofu-estate=$ESTATE before migration"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE - choudoufu live-import against the plain state file
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=migrate
log "=== STAGE 2: migrate (choudoufu live-import -approve) ==="
( cd "$ADOPTED" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ADOPTED" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "adopted-copy init failed"; }

IMPORT_OUT="$(cd "$ADOPTED" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE" 2>&1)"; IMPORT_RC=$?
if [ "$IMPORT_RC" -ne 0 ]; then
  printf '%s\n' "$IMPORT_OUT" | tail -60
  fail "live-import (dry run) failed"
fi
grep -qF "No tag has been written." <<< "$IMPORT_OUT" || fail "the dry run wrote a tag - it must not"
grep -qF "$TAGGABLE of $INSTANCES resource instance(s) are eligible for stamping" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT" | tail -40
       fail "live-import did not verify exactly $TAGGABLE of $INSTANCES as eligible"; }
# The 22 that are not eligible must be untaggable, not unadmitted. Those are two
# entirely different verdicts: UNTAGGABLE is the invariant working as designed
# (aws_route, aws_route_table_association, aws_vpc_dhcp_options_association and
# aws_security_group_rule carry no tags argument in the provider's schema at all,
# so there is nowhere to hang a marker and their identity derives from tagged
# parents instead), while UNADMITTED_TYPE would mean a type this fork cannot
# resolve. Asserting the first by value and the second by ABSENCE is what makes
# this check say something: a future admission regression that turned one of the
# 40 into an unadmitted type would keep the eligible count wrong AND put an
# UNADMITTED_TYPE section in this report, and either one fails here.
grep -qF "UNTAGGABLE ($UNTAGGABLE)" <<< "$IMPORT_OUT" \
  || { grep -E 'UNTAGGABLE|UNADMITTED_TYPE' <<< "$IMPORT_OUT"
       fail "expected exactly $UNTAGGABLE UNTAGGABLE instances in the ratification report"; }
grep -qE '^UNADMITTED_TYPE \(' <<< "$IMPORT_OUT" \
  && { grep -A 40 '^UNADMITTED_TYPE (' <<< "$IMPORT_OUT"
       fail "the ratification report names an UNADMITTED_TYPE - every type in this estate is admitted"; }
log "  dry run: $TAGGABLE of $INSTANCES eligible, $UNTAGGABLE untaggable, 0 unadmitted"

APPROVE_OUT="$(cd "$ADOPTED" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE" -approve 2>&1)"; APPROVE_RC=$?
if [ "$APPROVE_RC" -ne 0 ]; then
  printf '%s\n' "$APPROVE_OUT" | tail -60
  fail "live-import -approve failed"
fi
# The counts, by exact string. The two record-backed columns #340 added both read
# 0 here because this estate declares no record_store and reaches no record-backed
# type; asserting them anyway is how a change that starts routing an ordinary AWS
# type through that path fails here rather than passing quietly.
grep -qF "$TAGGABLE resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, $UNTAGGABLE skipped" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT" | tail -30
       fail "live-import -approve did not stamp exactly $TAGGABLE of $INSTANCES cleanly"; }
log "  $TAGGABLE stamped, $UNTAGGABLE skipped, 0 recorded, 0 failed"

# ── identity assertions, read via the AWS CLI directly, never through choudoufu ──
VPC_ID="$(awsl ec2 describe-vpcs --filters "Name=tag:Name,Values=ex-complete" --query "Vpcs[0].VpcId" --output text)"
[ -n "$VPC_ID" ] && [ "$VPC_ID" != "None" ] || fail "no live VPC found by its Name tag"
VPC_ADDR="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$VPC_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
# An instance key reaches a tag in its ESCAPED form, never OpenTofu's bracket
# spelling: an AWS tag value cannot carry "[", "]" or a quote, so
# internal/live/stamp writes "module.vpc.aws_vpc.this:0" and
# internal/live/discovery normalizes an observed marker back the same way
# (internal/live/markers.EscapeKey; TestStamp_countInstancesGetEscapedAddresses
# and TestStamp_forEachInstancesGetEscapedAddresses pin both directions). The
# two forms are kept as separate variables wherever both are needed, because
# comparing one against the other is a check that can never match - a vacuous
# assertion of exactly that shape shipped in corpus-iam-policy and
# corpus-iam-read-only-policy before it was caught.
VPC_ADDR_BRACKET='module.vpc.aws_vpc.this[0]'
VPC_ADDR_TAG='module.vpc.aws_vpc.this:0'
[ "$VPC_ADDR" = "$VPC_ADDR_TAG" ] || fail "the VPC carries tofu-address=$VPC_ADDR, not $VPC_ADDR_TAG"
log "  $VPC_ID carries tofu-address=$VPC_ADDR"

RDS_SG_ID="$(awsl ec2 describe-security-groups --filters "Name=vpc-id,Values=$VPC_ID" "Name=group-name,Values=ex-complete-rds*" --query "SecurityGroups[0].GroupId" --output text)"
[ -n "$RDS_SG_ID" ] && [ "$RDS_SG_ID" != "None" ] || fail "no live rds security group found by its name prefix"
RDS_SG_ADDR="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$RDS_SG_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
[ "$RDS_SG_ADDR" = "aws_security_group.rds" ] || fail "the rds security group carries tofu-address=$RDS_SG_ADDR, not aws_security_group.rds"
log "  $RDS_SG_ID carries tofu-address=$RDS_SG_ADDR"

S3_EP_ID="$(awsl ec2 describe-vpc-endpoints --filters "Name=vpc-id,Values=$VPC_ID" "Name=tag:Name,Values=s3-vpc-endpoint" --query "VpcEndpoints[0].VpcEndpointId" --output text)"
[ -n "$S3_EP_ID" ] && [ "$S3_EP_ID" != "None" ] || fail "no live s3 vpc endpoint found by its Name tag"
S3_EP_ADDR="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$S3_EP_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
if [ "${BREAK:-}" = "1" ]; then
  log "  BREAK=1: expecting the wrong address for $S3_EP_ID's endpoint on purpose - this check must fail"
  # The escaped form of the WRONG key: still a well-formed marker value, so
  # this fails on the address rather than on the escaping.
  WANT_S3_ADDR='module.vpc_endpoints.aws_vpc_endpoint.this:dynamodb'
else
  WANT_S3_ADDR='module.vpc_endpoints.aws_vpc_endpoint.this:s3'
fi
S3_EP_ADDR_BRACKET='module.vpc_endpoints.aws_vpc_endpoint.this["s3"]' 
[ "$S3_EP_ADDR" = "$WANT_S3_ADDR" ] || fail "the s3 vpc endpoint carries tofu-address=$S3_EP_ADDR, not $WANT_S3_ADDR"
log "  $S3_EP_ID carries tofu-address=$S3_EP_ADDR"

MARKED="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
log "  $MARKED objects carry tofu-estate=$ESTATE after migration"
gauntlet_stage migrate pass "$TAGGABLE stamped, $UNTAGGABLE skipped, 0 recorded, 0 failed; $MARKED objects carry tofu-estate=$ESTATE"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - state deleted, live-plan, empty + identities re-asserted
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=test_plan
log "=== STAGE 3: test plan (state deleted, live-plan empty) ==="
rm -f "$ADOPTED/terraform.tfstate" "$ADOPTED/terraform.tfstate.backup"
[ ! -f "$ADOPTED/terraform.tfstate" ] || fail "the state file is still there"

plan_into() { ( cd "$ADOPTED" && "$TOFU" live-plan -input=false -no-color ); }
PLAN_OUT="$(plan_into 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -60; fail "live-plan exited $PLAN_RC"; }
[ ! -f "$ADOPTED/terraform.tfstate" ] || fail "live-plan wrote a state file"
grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$PLAN_OUT" \
  && { grep -E '^  # .+ will be' <<< "$PLAN_OUT"; fail "the plan proposes a resource change with no local record store and no drift"; }
log "  no resource change proposed, with zero local memory of the migration that stamped it"

# Re-assert the same three identities, not merely against the tags stage 2
# already checked - the "and" in the brief's "assert the plan is EMPTY *and*
# assert the rendered identity strings themselves" is two separate
# assertions, not one satisfying both. live-plan's own trace for a converged
# plan carries no per-instance line to grep when nothing changed (it only
# names a resource it is about to CHANGE), so this stage's identity evidence
# is the same AWS-CLI tag read as stage 2, re-run here against the state
# this plan actually saw - after the local state file was deleted, so any
# answer below can only have come from the marker on the live object itself.
VPC_ADDR2="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$VPC_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
[ "$VPC_ADDR2" = "$VPC_ADDR" ] || fail "the VPC's tofu-address changed across the empty plan: $VPC_ADDR -> $VPC_ADDR2"
RDS_SG_ADDR2="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$RDS_SG_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
[ "$RDS_SG_ADDR2" = "$RDS_SG_ADDR" ] || fail "the rds security group's tofu-address changed across the empty plan: $RDS_SG_ADDR -> $RDS_SG_ADDR2"
S3_EP_ADDR2="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$S3_EP_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
[ "$S3_EP_ADDR2" = "$S3_EP_ADDR" ] || fail "the s3 vpc endpoint's tofu-address changed across the empty plan: $S3_EP_ADDR -> $S3_EP_ADDR2"
log "  identity re-check: all three objects still carry the same tofu-address after the state file was deleted (re-read via the AWS CLI): $VPC_ADDR2, $RDS_SG_ADDR2, $S3_EP_ADDR2"
gauntlet_stage test_plan pass "empty plan; identity re-check unchanged: $VPC_ADDR2, $RDS_SG_ADDR2, $S3_EP_ADDR2"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 4: TEST APPLY - apply the empty plan, assert a genuine no-op
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=test_apply
log "=== STAGE 4: test apply (apply the empty plan; object count unchanged) ==="
BEFORE_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"

APPLY2_OUT="$(cd "$ADOPTED" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; APPLY2_RC=$?
[ "$APPLY2_RC" -eq 0 ] || { printf '%s\n' "$APPLY2_OUT" | tail -40; fail "the post-migration apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY2_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY2_OUT"; fail "the post-migration apply was not a no-op"; }

AFTER_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$AFTER_N" = "$BEFORE_N" ] || fail "object count changed across a no-op apply: $BEFORE_N -> $AFTER_N"
[ ! -f "$ADOPTED/terraform.tfstate" ] || fail "a state file exists after the apply"
log "  genuine no-op: $BEFORE_N objects before, $AFTER_N after, no state file either time"
gauntlet_stage test_apply pass "genuine no-op: $BEFORE_N objects before, $AFTER_N after, no state file either time"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 5: DRIFT AND RECONVERGE - mutate one object, replan, assert one fix
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=drift_reconverge
log "=== STAGE 5: drift and reconverge (mutate one object out of band) ==="
DRIFT_SUBNET_ID="$(awsl ec2 describe-subnets --filters "Name=vpc-id,Values=$VPC_ID" "Name=tag:Name,Values=Private Subnet One" --query "Subnets[0].SubnetId" --output text)"
[ -n "$DRIFT_SUBNET_ID" ] && [ "$DRIFT_SUBNET_ID" != "None" ] || fail "no live subnet found by its Name tag (Private Subnet One)"

if [ "${BREAK:-}" = "1" ]; then
  # A second, unrelated object is mutated too - the assertion below must
  # catch this as MORE than one object proposed, not silently pass.
  OTHER_SUBNET_ID="$(awsl ec2 describe-subnets --filters "Name=vpc-id,Values=$VPC_ID" "Name=tag:Name,Values=Private Subnet Two" --query "Subnets[0].SubnetId" --output text)"
  awsl ec2 create-tags --resources "$OTHER_SUBNET_ID" --tags Key=Example,Value=tampered-by-BREAK >/dev/null
  log "  BREAK=1: also tampered $OTHER_SUBNET_ID's Example tag - stage 5 must now see TWO drifted objects and fail the single-object assertion"
fi

awsl ec2 create-tags --resources "$DRIFT_SUBNET_ID" --tags Key=Example,Value=tampered-out-of-band >/dev/null
DRIFTED_VALUE="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$DRIFT_SUBNET_ID" "Name=key,Values=Example" --query "Tags[0].Value" --output text)"
[ "$DRIFTED_VALUE" = "tampered-out-of-band" ] || fail "the out-of-band tag mutation did not take"
log "  mutated $DRIFT_SUBNET_ID's Example tag to \"tampered-out-of-band\" directly via the AWS CLI"

DRIFT_PLAN_OUT="$(plan_into 2>&1)"; DRIFT_PLAN_RC=$?
[ "$DRIFT_PLAN_RC" -eq 0 ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | tail -60; fail "the drift-detection plan exited $DRIFT_PLAN_RC"; }

CHANGED_ADDRS="$(grep -oE '^  # \S+ will be updated' <<< "$DRIFT_PLAN_OUT" | awk '{print $2}' | sort -u)"
N_CHANGED="$(printf '%s\n' "$CHANGED_ADDRS" | grep -c . || true)"
if [ "${BREAK:-}" = "1" ]; then
  [ "$N_CHANGED" = "1" ] && fail "BREAK=1 set (two objects tampered), but the plan proposes fixing only 1 - this assertion is not load-bearing"
  log "  BREAK=1: the plan proposes fixing $N_CHANGED objects, correctly more than one - the single-object assertion below is skipped"
else
  [ "$N_CHANGED" = "1" ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | grep -E '^  # .+ will be' ; fail "expected exactly 1 object proposed for a fix, got $N_CHANGED"; }
  # CHANGED_ADDRS comes from the plan's own diff headers, which print OpenTofu
  # addresses in BRACKET form - so the untouched objects are named here in that
  # form, not in the tag form stage 2 asserted. aws_security_group.rds carries no
  # instance key at all, so its two spellings coincide; the other two do not, and
  # naming them in tag form here would be a check that could never fire.
  for UNTOUCHED in "$RDS_SG_ADDR" "$VPC_ADDR_BRACKET" "$S3_EP_ADDR_BRACKET"; do
    printf '%s\n' "$CHANGED_ADDRS" | grep -qF "$UNTOUCHED" \
      && fail "the plan proposes changing $UNTOUCHED, which was never touched"
  done
  log "  the plan proposes fixing exactly one object: $(printf '%s' "$CHANGED_ADDRS")"

  RECONVERGE_APPLY="$(cd "$ADOPTED" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; RECONVERGE_RC=$?
  [ "$RECONVERGE_RC" -eq 0 ] || { printf '%s\n' "$RECONVERGE_APPLY" | tail -40; fail "the reconverge apply failed"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$RECONVERGE_APPLY" \
    || { grep -E 'Apply complete' <<< "$RECONVERGE_APPLY"; fail "the reconverge apply did not change exactly 1 resource"; }
  FIXED_VALUE="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$DRIFT_SUBNET_ID" "Name=key,Values=Example" --query "Tags[0].Value" --output text)"
  [ "$FIXED_VALUE" = "ex-complete" ] || fail "the subnet's Example tag is \"$FIXED_VALUE\" after reconverging, not \"ex-complete\""
  log "  reconverged: $DRIFT_SUBNET_ID's Example tag is back to \"ex-complete\""
  gauntlet_stage drift_reconverge pass "one subnet tampered (Example tag), plan proposed fixing exactly one object, apply changed 1 and reconverged the tag to ex-complete"
fi

CURRENT_STAGE=""
gauntlet_end

log ""
log "=== PASS ==="
log ""
log "terraform-aws-modules/terraform-aws-vpc's flagship \"complete\" example,"
log "62 resources, crossed through all five stages: cold deploy with plain"
log "terraform, choudoufu live-import adoption, an empty replan with the"
log "state file deleted and three rendered identities checked against the"
log "AWS CLI's own answer, a genuine no-op apply, and drift on one object"
log "reconverging without touching any other."
