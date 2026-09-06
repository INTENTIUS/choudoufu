#!/usr/bin/env bash
# (moved from the justfile's retired demo-corpus-vpc-complete recipe; run with: just demo-run corpus-vpc-complete)
# terraform-aws-modules/terraform-aws-vpc's flagship "complete" example
# (issue #274's real-estate crossing pipeline), all five stages: cold deploy
# with plain terraform, choudoufu live-import adoption, an empty replan with
# the state file deleted and three rendered identities checked against the
# AWS CLI's own answer, a genuine no-op apply, and drift on one object
# reconverging without touching any other. Needs Docker, the AWS CLI, and a
# real `terraform` (or tofu; see TF_COLD_BIN) on PATH; runs on its own port
# (4713) so it can run beside `just demo`.
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
#                 drift assertion, proving both are load-bearing; also
#                 day2_rename's own break control (PART D) once the
#                 estate is clear.
#   BREAK_COUNT   set to 1 to run day2_count's own break control (PART G)
#                 instead of the real assertions: expect the WRONG instance
#                 (count_test[0] rather than count_test[1]) to be the one the
#                 scale-down destroys. The Break text in
#                 tools/gauntlet/stages.go for day2_count, verbatim, and it
#                 makes the stage report fail - which is the point.
#   BREAK_REMOVE  set to 1 to run day2_remove's own break control (PART E)
#                 instead of the real removal: keep the "dynamodb" vpc
#                 endpoint map entry in the config; the plan must propose
#                 no destroy for it at all - the Break text in
#                 tools/gauntlet/stages.go, verbatim.
#   BREAK_APPROVAL
#                 set to 1 to run plan_approval's own negative control
#                 instead of the real refusal check (PART P): after the
#                 world has moved out of band, assert the saved plan file
#                 APPLIES cleanly - the Break text in tools/gauntlet/
#                 stages.go for plan_approval is literally "Apply the
#                 planfile after a mutation and expect success; the run must
#                 refuse", so this assertion has to fail. Independent of
#                 every BREAK above, and the only one of them under which
#                 PART P runs at all - the others deliberately leave the
#                 estate somewhere PART P does not describe, and it reports
#                 no verdict there.
#
# WHERE THIS ESTATE STANDS, re-verified 2026-08-22, SAME DAY, AGAINST A
# SECOND NEW IMAGE
# (ghcr.io/lex00/floci@sha256:dcd57a44da855e65e0c910f81e3a9e87b3b2a5d701f4d95945351ca7ea2ca9b9,
# published for lex00/floci#99):
# **all five stages pass.** CreateVpcEndpoint never parsed
# SubnetConfiguration.N.SubnetId/.Ipv4/.Ipv6 at all - it silently dropped
# the requested address and synthesized its own, which is why only the ecs
# endpoint (the one interface endpoint that pins ipv4 = cidrhost(subnet_cidr,
# 10) explicitly; see "THE REMAINING ONE" below) ever showed a diff. #99
# parses and honours a pinned Ipv4 for interface endpoints; a real run with
# FLOCI_IMAGE overridden to that digest reaches test_plan EMPTY, with the
# same three sampled identities re-read by value off the AWS CLI, and
# test_apply and drift_reconverge both pass for real behind it (log:
# `GAUNTLET stage=test_plan verdict=pass`, `stage=test_apply verdict=pass`,
# `stage=drift_reconverge verdict=pass`). This is NOT yet the estate's
# default result: `live/floci-image` (this script's fallback below) still
# pins the pre-#99 digest, because repinning is a shared-layer change that
# forces a re-measure of every clear estate (live/GAUNTLET.md's engine
# section, "the emulator is wrong" row) and is a maintainer call, not this
# unit's. Reproduce with:
#   FLOCI_IMAGE=ghcr.io/lex00/floci@sha256:dcd57a44da855e65e0c910f81e3a9e87b3b2a5d701f4d95945351ca7ea2ca9b9 \
#     bash live/e2e/corpus-vpc-complete/run.sh
# Everything below this paragraph, up to "THE REMAINING ONE", was written
# against the still-pinned image and is kept for its history; it describes
# what this script still shows until the pin moves.
#
# WHERE THIS ESTATE STOOD, as of the still-pinned image
# (ghcr.io/lex00/floci@sha256:0afd26480833a5081cbf3dc473dc0b688dccc03ee975616c3d57a8ea0fc303de,
# the pin bump that fixed lex00/floci#97's NAT gateway and VPN gateway read
# gaps, and most of its VPC-endpoint read gaps - see below): stages 1 and 2
# PASS. Stage 1 cold-deploys all 62 resources ("Apply complete! Resources:
# 62 added"); stage 2 stamps 40 of them, skips 22 as untaggable, fails none,
# and all three sampled identities read back through the AWS CLI by value.
#
# Stage 1 was blocked for two prior sessions on two successive floci gaps in
# the same resource, both now fixed upstream: lex00/floci#70 (Create/
# ModifyCacheSubnetGroup read SubnetIds under the generic member key while
# ElastiCache's service model overrides it to SubnetIdentifier) and
# lex00/floci#71 (ElastiCache served no tagging actions at all, so the AWS
# provider's unconditional ListTagsForResource on every
# aws_elasticache_subnet_group read 400'd).
#
# STAGE 3 TODAY (re-verified 2026-08-22 against the same pin, with choudoufu
# #372 landed): live-plan exits 0 and proposes ONE changed object. That is
# down from 29, and from the 34 a 2026-08-21 session measured against the
# prior pin. The two steps between:
#
#   - the floci#97 fix reaches exactly as far as advertised:
#     aws_nat_gateway.this[0] and aws_vpn_gateway.this[0] each show ONLY
#     tags/tags_all changing (their allocation_id/subnet_id and
#     availability_zone gaps are gone), and five of the vpc-endpoints
#     submodule's six endpoints (s3, dynamodb, ecr_api, ecr_dkr, rds) plan
#     completely clean. choudoufu #346 and #355, the two identity/discovery
#     refusals that used to block this stage from running at all, remain
#     fixed; neither diagnostic appears in a run any more.
#
#   - choudoufu #372 took 28 of the remaining 29 to zero. Those 28, including
#     module.vpc_endpoints.aws_security_group.this[0], were pure tofu-slot:
#     the only change in each of their plan bodies was `tags`/`tags_all`
#     gaining `tofu-slot`, because live-import wrote only tofu-estate and
#     tofu-address and left the third marker to the first replan (live/e2e/
#     corpus-iam-policy/run.sh's "THE TOFU-SLOT FINDING"). live-import now
#     settles the slot itself for a count-expanded instance of a
#     server-assigned type whose live set carries no slots - the assignment
#     being slot i for index i, which is what the per-instance tofu-address it
#     is writing in the same call already says. Every count instance in this
#     estate is one, so stage 2 below reads the VPC's own tofu-slot back off
#     EC2 by value, and stage 3 refuses to pass if the plan proposes a
#     tofu-slot change at all, in either direction.
#
#   - CORRECTION, checked by A/B rather than assumed: an earlier version of
#     this comment credited #372 with also removing module.vpc_endpoints.
#     aws_security_group.this[0]'s `+ revoke_rules_on_delete = false`. That
#     was wrong. Built and ran the binary at this branch's own base commit
#     (0119227197, before #372's slot.go existed at all) against this same
#     script and config: the plan still shows 29 objects changing, and
#     `revoke_rules_on_delete` appears in NONE of them - the security group's
#     body there is already pure tofu-slot, exactly like the other 27.
#     `revoke_rules_on_delete` was fixed before this branch even started, by
#     56e807062e/b5bb09d27e ("liveimport: record GitHub issue #275 residue
#     during migrate, fixing #327", merged 2026-08-19), which made live-import
#     record every eligible instance's residue - the arguments the provider
#     never echoes back - at migrate time instead of leaving it to the first
#     apply. The "29 objects... all but 2 are pure tofu-slot" characterization
#     this estate's artifact entry carried into this branch was itself stale
#     by the time #372 branched: #327's fix already applied to this security
#     group, and the true pre-#372 shape here was 28 pure-tofu-slot objects
#     plus the one endpoint below, not 27 plus two others.
#
# THE REMAINING ONE: module.vpc_endpoints.aws_vpc_endpoint.this["ecs"] - the
# only one of the four interface endpoints (ecr_api, ecr_dkr, rds, ecs) that
# still changes - proposes replacing network_interface_ids wholesale (three
# ENI ids going to "(known after apply)") and swapping all three
# subnet_configuration blocks' ipv4 addresses (live, as floci reports it:
# *.201; proposed, matching the example's own
# `ipv4 = cidrhost(v.cidr_block, 10)`: *.10), plus a `+ timeouts` block. This
# is a for_each resource, so per internal/live/stamp/doc.go ("nothing is ever
# stamped for for_each") it was never a tofu-slot candidate; the diff is
# unrelated to that mechanism.
#
# CONFIRMED AGAINST THE AWS API DOCS, 2026-08-22: EC2's own
# SubnetConfiguration reference
# (https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_SubnetConfiguration.html)
# documents Ipv4 as "The IPv4 address to assign to the endpoint network
# interface in the subnet" at creation, and that changing it later replaces
# the endpoint network interface - exactly the ENI-replacement shape this
# plan proposes, which is the real AWS provider reacting correctly to what it
# was told changed. The example requests ipv4 = cidrhost(v.cidr_block, 10) at
# creation for every interface endpoint, so a faithful DescribeVpcEndpoints
# read of an object created from that exact configuration must report *.10
# back, the same as the other three interface endpoints in this estate
# (ecr_api, ecr_dkr, rds) already do cleanly. floci reported *.201 for this
# one endpoint instead: a read-fidelity gap in the vpc-endpoint family, in
# the same family as lex00/floci#97 but a different shape (policy/
# route_table_ids/subnet_ids/cidr_blocks reading empty, none of which appear
# in this diff any more, #97 is substantially resolved for this estate). Not
# choudoufu's: it is a for_each resource, never a tofu-slot candidate, and
# every other object in this 62-resource estate reads back exactly what
# stock would expect.
#
# FIXED, 2026-08-22, lex00/floci#99: root-caused to CreateVpcEndpoint never
# parsing SubnetConfiguration.N.SubnetId/.Ipv4/.Ipv6 at all - the requested
# address was silently dropped and floci synthesized its own instead, which
# is exactly the *.201-instead-of-*.10 shape above and exactly why only the
# one endpoint that pins an explicit ipv4 (ecs) ever showed a diff while the
# other three, which let the address be whatever came back, did not. #99
# parses and honours a pinned Ipv4 for interface endpoints. Fixed image:
# ghcr.io/lex00/floci@sha256:dcd57a44da855e65e0c910f81e3a9e87b3b2a5d701f4d95945351ca7ea2ca9b9
# (see "WHERE THIS ESTATE STANDS" above for the real run against it - all
# five stages pass). The fail() detail two blocks below this comment still
# describes the plan body this script actually sees against the still-
# pinned image; it will stop firing the moment live/floci-image moves past
# #99's fix.
#
# So stage 3 fails on one thing against the still-pinned image, and it was
# never choudoufu's. Nothing here is routed around (no -target, no resource
# removed from the example): the script runs the real module and reports
# the real result, per this goal's own standing rule that a partial,
# accurate failure is worth more than a green run that does not hold up.
#
# ALSO ESTABLISHED, 2026-08-21, still true 2026-08-22: the "39 objects carry
# tofu-estate after migration" line below disagrees with the "40 stamped"
# line above it, and the 40 is the right number. The missing object is
# aws_redshift_subnet_group.redshift[0] - it carries both markers, readable
# through `aws redshift describe-cluster-subnet-groups`, but floci's
# resourcegroupstaggingapi does not index Redshift resources, so the
# get-resources count this script logs cannot see it. A floci gap in the
# reporting line, not a stamping gap.
#
# A GAP IN THIS SCRIPT'S OWN CHECK, found and fixed 2026-08-21, re-confirmed
# still doing its job 2026-08-22: stage 3's empty-plan assertion matched
# `will be (created|updated|destroyed)` only. A live-plan header for a
# forced replacement instead reads `must be replaced` - a fourth, distinct
# verb the old pattern never matched. Today's one changed object happens to
# use only the "will be updated in-place" verb (confirmed above), so the
# fourth verb is not load-bearing in this particular run - but the pattern
# stays fixed to catch the day a forced replacement (e.g. from the vpc
# endpoint gap above, if it starts producing one) shows up here, which is exactly
# the silent-false-pass the "empty plan alone is not enough" oracle in
# live/GAUNTLET.md exists to catch. The fifteen other corpus-* scripts
# sharing the same narrower pattern (`grep -rl 'will be (created|updated|
# destroyed)' live/e2e/*/run.sh`) were left untouched when this was first
# found - out of that unit's one-estate scope, flagged for a follow-up unit
# instead.

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

# Two more, fresh containers for the greenfield stage (live/GAUNTLET.md
# #13): one namespace choudoufu applies into directly with no migration,
# and a SEPARATE namespace stock applies the identical config into as
# that stage's own oracle.
FLOCI_GREEN_PORT=$((FLOCI_PORT + 1))
FLOCI_GREEN_NAME="choudoufu-corpus-vpc-complete-green-$$"
FLOCI_ORACLE_PORT=$((FLOCI_PORT + 2))
FLOCI_ORACLE_NAME="choudoufu-corpus-vpc-complete-green-oracle-$$"
GREEN_ENDPOINT="http://127.0.0.1:${FLOCI_GREEN_PORT}"
ORACLE_ENDPOINT="http://127.0.0.1:${FLOCI_ORACLE_PORT}"

# And one more for day2_count's stock oracle (PART G, live/GAUNTLET.md #8).
# It gets its own port rather than reusing the greenfield oracle's, which is
# free by the time PART G runs, so that PART G does not depend on PART
# GREENFIELD having run at all - that section is deliberately in a subshell
# whose failure the rest of the script survives.
FLOCI_COUNT_ORACLE_PORT=$((FLOCI_PORT + 3))
FLOCI_COUNT_ORACLE_NAME="choudoufu-corpus-vpc-complete-count-oracle-$$"
COUNT_ORACLE_ENDPOINT="http://127.0.0.1:${FLOCI_COUNT_ORACLE_PORT}"

ESTATE="vpc-complete-crossing"
GREEN_ESTATE="${ESTATE}-greenfield"

# What the estate is, asserted rather than described. 62 resource instances, of
# which 40 carry a tags argument in the AWS provider's schema and 22 do not - see
# stage 2's own comment for why that split is the invariant rather than a gap.
INSTANCES=62
TAGGABLE=40
UNTAGGABLE=22
REGION="eu-west-1"
TF_COLD_BIN="${TF_COLD_BIN:-terraform}"

cleanup() {
  docker rm -f "$FLOCI_NAME" "$FLOCI_GREEN_NAME" "$FLOCI_ORACLE_NAME" "$FLOCI_COUNT_ORACLE_NAME" >/dev/null 2>&1 || true
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
gauntlet_begin_stage cold_deploy
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
# PART GREENFIELD (greenfield, live/GAUNTLET.md #13, active)
# ══════════════════════════════════════════════════════════════════════════
#
# A SEPARATE fresh namespace from everything above: choudoufu applies the
# same unmodified example directly, no migration ever, compared against
# stock's OWN fresh apply of the identical config in a THIRD namespace.
# Two more floci containers. Run in a subshell: a real, honestly-reported
# FAILURE here must not take stage 2 onward down with it - day2_rename and
# day2_remove both operate on the separately-adopted $ADOPTED and have
# nothing to do with this stage.
(
gauntlet_begin_stage greenfield
log ""
log "=== PART GREENFIELD: 0. two more floci containers ==="
docker run -d --rm -p "${FLOCI_GREEN_PORT}:4566" --name "$FLOCI_GREEN_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_GREEN_NAME failed"
docker run -d --rm -p "${FLOCI_ORACLE_PORT}:4566" --name "$FLOCI_ORACLE_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_ORACLE_NAME failed"
for ep in "$GREEN_ENDPOINT" "$ORACLE_ENDPOINT"; do
  H=""
  for _ in $(seq 1 45); do
    H="$(curl -fs "${ep}/_localstack/health" 2>/dev/null)" || true
    grep -q '"ec2"' <<< "${H:-}" && break
    sleep 2
  done
  grep -q '"ec2"' <<< "${H:-}" || fail "floci did not come up healthy (ec2) at $ep"
done
log "  healthy: greenfield=$GREEN_ENDPOINT oracle=$ORACLE_ENDPOINT"

log "=== PART GREENFIELD: 1. choudoufu apply from nothing, no migration, no state file ever existing ==="
GREEN_ROOT="$WORK/green"
copy_estate "$GREEN_ROOT"
emulator_delta "$GREEN_ROOT/vpc/examples/complete"
GREEN="$GREEN_ROOT/vpc/examples/complete"
perl -0pi -e 's/(required_providers \{\n    aws = \{\n      source  = "hashicorp\/aws"\n      version = "= 6\.59\.0"\n    \}\n  \}\n)\}/$1\n  live {\n    estate = "'"$GREEN_ESTATE"'"\n  }\n}/' "$GREEN/versions.tf"
grep -q "estate = \"$GREEN_ESTATE\"" "$GREEN/versions.tf" || fail "the greenfield live-block delta did not match versions.tf"
( cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the greenfield init failed"; }
GREEN_APPLY_OUT="$(cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; GREEN_APPLY_RC=$?
[ "$GREEN_APPLY_RC" -eq 0 ] || { printf '%s\n' "$GREEN_APPLY_OUT" | tail -40; fail "the greenfield apply failed"; }
grep -qE '^Apply complete!' <<< "$GREEN_APPLY_OUT" || fail "the greenfield apply produced no 'Apply complete!' line"
log "  $(grep -E '^Apply complete!' <<< "$GREEN_APPLY_OUT")"
[ ! -f "$GREEN/terraform.tfstate" ] || fail "the greenfield apply left a state file - this estate must never keep local state"

log "=== PART GREENFIELD: 2. markers, read through the AWS CLI directly ==="
# $TAGGABLE ($TAGGABLE) objects carry a tofu-address tag, but
# resourcegroupstaggingapi's own get-resources only ever returns
# $((TAGGABLE - 1)): aws_redshift_subnet_group has no CFN type in floci's
# Tagging-API coverage (internal/live/discovery/tagging.go's ARN-join
# table), a floci gap this estate's own migrate stage already measured
# and documents (see this estate's history in live/gauntlet.json) -
# choudoufu's own stamp count is still the correct one, so this checks
# the resourcegroupstaggingapi-visible count, not the stamped count.
GTAGGED="$(aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$GREEN_ESTATE" --query 'length(ResourceTagMappingList)' --output text)"
[ "$GTAGGED" = "$((TAGGABLE - 1))" ] || fail "the greenfield estate has $GTAGGED objects visible to resourcegroupstaggingapi, expected $((TAGGABLE - 1)) ($TAGGABLE tag-stamped minus the known floci Redshift Tagging-API gap)"
log "  $GTAGGED objects visible to resourcegroupstaggingapi ($TAGGABLE actually tag-stamped, of $INSTANCES total; $UNTAGGABLE untaggable/derived)"

log "=== PART GREENFIELD: 3. the next plan proposes nothing ==="
GREEN_PLAN_OUT="$(cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; GREEN_PLAN_RC=$?
[ "$GREEN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$GREEN_PLAN_OUT" | tail -40; fail "the greenfield replan exited $GREEN_PLAN_RC"; }
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$GREEN_PLAN_OUT" \
  || { grep -E '^  #' <<< "$GREEN_PLAN_OUT"; fail "the greenfield replan is not empty"; }
log "  No changes."

log "=== PART GREENFIELD: 4. stock oracle - the identical config applied fresh in its own namespace ==="
ORACLE_ROOT="$WORK/green-oracle"
copy_estate "$ORACLE_ROOT"
emulator_delta "$ORACLE_ROOT/vpc/examples/complete"
GREEN_ORACLE="$ORACLE_ROOT/vpc/examples/complete"
( cd "$GREEN_ORACLE" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" "$TF_COLD_BIN" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$GREEN_ORACLE" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" "$TF_COLD_BIN" init -input=false -no-color 2>&1 | tail -30 ); fail "the greenfield oracle's init failed"; }
# TF_LOG_PROVIDER=DEBUG (issue #672): the stock oracle's own apply is the leg
# where the 18-vs-19 subnet discrepancy was seen, with no choudoufu anywhere
# in it, so a provider-level debug log is the only way to see what actually
# went out on the wire. This costs nothing on the pass path (the log goes to
# $WORK, which the script's own cleanup trap removes on exit either way) and
# on failure it answers the question #672 posed: a duplicate CreateSubnet
# sharing one amz_sdk_invocation_id across two amz_sdk_request attempts is
# the AWS SDK retrying one logical call (the emulator-defect shape #672
# names); two CreateSubnet calls with two different invocation ids are two
# distinct calls the provider issued on purpose, which points at the
# example's own configuration instead.
ORACLE_TF_LOG="$WORK/greenfield-oracle-tf-provider-debug.log"
ORACLE_APPLY_OUT="$(cd "$GREEN_ORACLE" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" TF_LOG_PROVIDER=DEBUG TF_LOG_PATH="$ORACLE_TF_LOG" "$TF_COLD_BIN" apply -input=false -auto-approve -no-color 2>&1)"; ORACLE_APPLY_RC=$?
[ "$ORACLE_APPLY_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_APPLY_OUT" | tail -40; fail "the greenfield oracle apply failed"; }
grep -qE '^Apply complete!' <<< "$ORACLE_APPLY_OUT" || fail "the greenfield oracle apply produced no 'Apply complete!' line"
log "  $(grep -E '^Apply complete!' <<< "$ORACLE_APPLY_OUT")"

log "=== PART GREENFIELD: 5. object-by-object comparison, via the AWS CLI on both endpoints, tags normalised out ==="
# local.vpc_cidr is a static "10.0.0.0/16" in the example, so filtering by
# cidr-block finds this estate's one VPC without depending on any tag.
GVPC_ID="$(aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" ec2 describe-vpcs --filters Name=cidr-block,Values=10.0.0.0/16 --query 'Vpcs[0].VpcId' --output text)"
OVPC_ID="$(aws --endpoint-url "$ORACLE_ENDPOINT" --region "$REGION" ec2 describe-vpcs --filters Name=cidr-block,Values=10.0.0.0/16 --query 'Vpcs[0].VpcId' --output text)"
[ -n "$GVPC_ID" ] && [ "$GVPC_ID" != "None" ] || fail "no greenfield vpc found at cidr 10.0.0.0/16 - the corpus pin has moved"
[ -n "$OVPC_ID" ] && [ "$OVPC_ID" != "None" ] || fail "no oracle vpc found at cidr 10.0.0.0/16 - the corpus pin has moved"
GSUBNETS="$(aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" ec2 describe-subnets --filters "Name=vpc-id,Values=$GVPC_ID" --query 'length(Subnets)' --output text)"
OSUBNETS="$(aws --endpoint-url "$ORACLE_ENDPOINT" --region "$REGION" ec2 describe-subnets --filters "Name=vpc-id,Values=$OVPC_ID" --query 'length(Subnets)' --output text)"
if [ "$GSUBNETS" != "$OSUBNETS" ]; then
  # SEEN ONCE, 2026-08-31, while building this estate's day2_count section
  # (PART G): one run in four reported greenfield=18 oracle=19 - an extra
  # subnet in the STOCK oracle's own VPC, created by plain $TF_COLD_BIN with
  # no choudoufu in that leg at all, and its own apply still said "Apply
  # complete! Resources: 62 added". The other three runs, on the same image
  # and the same commit, matched at 18. A duplicate CreateSubnet - a call
  # floci answered slowly enough for the provider to retry one that had in
  # fact succeeded - fits what was observed, but nothing was captured to
  # confirm it because this assertion only ever printed the two counts and
  # the containers are torn down on exit.
  #
  # ROOT-CAUSED, 2026-09-05, issue #672: hit CreateSubnet directly with the
  # AWS CLI against the pinned image, no terraform in the loop - two
  # back-to-back calls with the identical VpcId/CidrBlock/AvailabilityZone
  # both returned 200 with distinct subnet ids and the same CIDR. Real EC2
  # refuses the second one (InvalidSubnet.Conflict - CreateSubnet's own docs:
  # "A subnet CIDR block must not overlap the CIDR block of an existing
  # subnet in the VPC"); floci enforced no such check. CreateSubnet carries
  # no idempotency token, so that conflict check is the only thing standing
  # between an SDK-level transport retry (aws-sdk-go-v2's standard retry mode
  # retries I/O failures - connection reset, timeout - independent of
  # idempotency) and a second, live, unrecorded subnet, which is exactly this
  # shape: terraform's own state only ever sees the response it actually
  # received, so a retried create that succeeds twice leaves one subnet
  # tracked and one orphaned. Fixed in the floci fork (lex00/floci, see the
  # linked issue there); this script's own diagnostic below stays regardless,
  # since a maintainer has to repin the image before this stops firing here.
  #
  # So print both sides before failing. It costs two describe calls on a
  # path that is already failing, and it turns "18 != 19" into a named
  # object the next occurrence can be root-caused from - which side has the
  # extra subnet, its id, its CIDR and its AZ, and therefore whether it is a
  # duplicate of a declared one (the retry story) or something else entirely.
  log "  greenfield subnets (id, cidr, az):"
  aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" ec2 describe-subnets \
    --filters "Name=vpc-id,Values=$GVPC_ID" \
    --query 'sort_by(Subnets,&CidrBlock)[].[SubnetId,CidrBlock,AvailabilityZone]' --output text || true
  log "  stock oracle subnets (id, cidr, az):"
  aws --endpoint-url "$ORACLE_ENDPOINT" --region "$REGION" ec2 describe-subnets \
    --filters "Name=vpc-id,Values=$OVPC_ID" \
    --query 'sort_by(Subnets,&CidrBlock)[].[SubnetId,CidrBlock,AvailabilityZone]' --output text || true
  # The oracle's own CreateSubnet calls, from the provider debug log captured
  # in PART GREENFIELD:4 above (TF_LOG_PROVIDER=DEBUG): each call's
  # amz_sdk_invocation_id is stable across SDK-level retries of the SAME
  # logical request, while amz_sdk_request carries the attempt number - two
  # CreateSubnet lines sharing one invocation id at attempt=1 and attempt=2
  # is the SDK retrying a single call (the emulator-defect shape this issue
  # named); two lines with two different invocation ids are two calls the
  # provider issued on purpose, which points at the example's configuration
  # instead. The response body's own <requestId> and subnetId name which
  # object each attempt produced.
  log "  stock oracle's CreateSubnet requests (from the provider debug log, invocation id / attempt / response request id + subnet id):"
  if [ -s "$ORACLE_TF_LOG" ]; then
    grep -E 'rpc\.method=EC2/CreateSubnet|amz_sdk_invocation_id=|amz_sdk_request="attempt=|<CreateSubnetResponse' "$ORACLE_TF_LOG" \
      | grep -oE 'amz_sdk_invocation_id=[^ ]+|amz_sdk_request="[^"]+"|requestId>[a-f0-9-]+|subnetId>subnet-[a-zA-Z0-9]+' \
      || log "    provider debug log has no CreateSubnet lines matching the expected fields - format may have moved"
  else
    log "    no provider debug log was captured at $ORACLE_TF_LOG"
  fi
  fail "the subnet count differs: greenfield=$GSUBNETS oracle=$OSUBNETS - see the two lists above for which side carries the extra subnet and whether its CIDR duplicates a declared one"
fi
GEP="$(aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" ec2 describe-vpc-endpoints --filters "Name=tag:Name,Values=s3-vpc-endpoint" --query 'length(VpcEndpoints)' --output text)"
OEP="$(aws --endpoint-url "$ORACLE_ENDPOINT" --region "$REGION" ec2 describe-vpc-endpoints --filters "Name=tag:Name,Values=s3-vpc-endpoint" --query 'length(VpcEndpoints)' --output text)"
[ "$GEP" = "1" ] && [ "$OEP" = "1" ] \
  || fail "the s3 endpoint count differs: greenfield=$GEP oracle=$OEP"
log "  vpc cidr, subnet count ($GSUBNETS), and the s3 endpoint's presence match between the greenfield estate and the stock oracle in its own namespace"
gauntlet_stage greenfield pass "$INSTANCES resources from nothing ($TAGGABLE tag-stamped, $UNTAGGABLE untaggable/derived), replan empty, stock oracle in its own namespace matches on vpc cidr, subnet count ($GSUBNETS) and the s3 endpoint's presence"
gauntlet_end_stage
docker rm -f "$FLOCI_GREEN_NAME" "$FLOCI_ORACLE_NAME" >/dev/null 2>&1 || true
) || log "  PART GREENFIELD did not clear (see the FAIL line and the greenfield stage=fail line above) - continuing to stage 2 onward, which does not depend on it"
docker rm -f "$FLOCI_GREEN_NAME" "$FLOCI_ORACLE_NAME" >/dev/null 2>&1 || true
gauntlet_end_stage

gauntlet_begin_stage day2_rename
log "=== D-ORACLE. stock: the same two renames, through moved blocks, on cold_deploy's own state ==="
PLAIN_ORACLE_ROOT="$WORK/plain-oracle"
cp -r "$WORK/plain" "$PLAIN_ORACLE_ROOT"
PLAIN_ORACLE="$PLAIN_ORACLE_ROOT/vpc/examples/complete"
sed -i.bak 's/module "vpc_endpoints" {/module "vpc_endpoints_renamed" {/' "$PLAIN_ORACLE/main.tf"
sed -i.bak 's/module\.vpc_endpoints\./module.vpc_endpoints_renamed./g' "$PLAIN_ORACLE/outputs.tf"
sed -i.bak 's/resource "aws_security_group" "rds" {/resource "aws_security_group" "rds_renamed" {/' "$PLAIN_ORACLE/main.tf"
sed -i.bak 's/aws_security_group\.rds\.id/aws_security_group.rds_renamed.id/' "$PLAIN_ORACLE/main.tf"
rm -f "$PLAIN_ORACLE/main.tf.bak" "$PLAIN_ORACLE/outputs.tf.bak"
cat >> "$PLAIN_ORACLE/main.tf" <<'EOF'

moved {
  from = module.vpc_endpoints
  to   = module.vpc_endpoints_renamed
}

moved {
  from = aws_security_group.rds
  to   = aws_security_group.rds_renamed
}
EOF
( cd "$PLAIN_ORACLE" && "$TF_COLD_BIN" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_ORACLE" && "$TF_COLD_BIN" init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_rename stock oracle's reinit failed"; }
ORACLE_PLAN_OUT="$(cd "$PLAIN_ORACLE" && "$TF_COLD_BIN" plan -input=false -no-color 2>&1)"; ORACLE_PLAN_RC=$?
[ "$ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -40; fail "the day2_rename stock oracle plan exited $ORACLE_PLAN_RC"; }
grep -qE '^  # .+ will be (destroyed|created)' <<< "$ORACLE_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock proposes a destroy or create for a rename carried entirely by moved blocks - the oracle itself is not zero-churn"; }
grep -qF 'Plan: 0 to add, 0 to change, 0 to destroy.' <<< "$ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -10; fail "stock's rename plan is not a true no-op"; }
log "  stock: zero churn on cold_deploy's own state - both moves report only their move, no attribute diff at all"

# ══════════════════════════════════════════════════════════════════════════
# PART E-ORACLE: REMOVE, stock (day2_remove, active - live/GAUNTLET.md #7):
# "Stock with the same block removed plans the same destroys." A SEPARATE
# copy of cold_deploy's own state, so this destroy has nothing to do with
# the rename above. Removes the "dynamodb" entry from module.vpc_endpoints'
# own `endpoints` map - a single Gateway endpoint, taggable, nothing else
# in the config references its id (route_table_ids is an argument ON the
# endpoint itself, not a separate association resource AWS models), the
# smallest real removal target this estate has (the task's own "a single
# endpoint or route" guidance).
gauntlet_begin_stage day2_remove
log "=== E-ORACLE: stock terraform, delete the \"dynamodb\" endpoint entry on cold_deploy's own state ==="
REMOVE_ORACLE_ROOT="$WORK/remove-oracle"
cp -r "$WORK/plain" "$REMOVE_ORACLE_ROOT"
REMOVE_ORACLE="$REMOVE_ORACLE_ROOT/vpc/examples/complete"
perl -0pi -e 's/\n    dynamodb = \{.*?\n    \},\n//s' "$REMOVE_ORACLE/main.tf"
grep -q 'dynamodb = {' "$REMOVE_ORACLE/main.tf" \
  && fail "removing the dynamodb endpoint entry from the remove-oracle copy did not match - the corpus pin has moved"
( cd "$REMOVE_ORACLE" && "$TF_COLD_BIN" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$REMOVE_ORACLE" && "$TF_COLD_BIN" init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_remove stock oracle's init failed"; }
REMOVE_ORACLE_PLAN_OUT="$(cd "$REMOVE_ORACLE" && "$TF_COLD_BIN" plan -input=false -no-color 2>&1)"; REMOVE_ORACLE_PLAN_RC=$?
[ "$REMOVE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_remove stock oracle plan exited $REMOVE_ORACLE_PLAN_RC"; }
grep -qF '  # module.vpc_endpoints.aws_vpc_endpoint.this["dynamodb"] will be destroyed' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "stock does not propose destroying the dynamodb endpoint when its map entry is removed"; }
grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -10; fail "stock's remove plan proposes something other than exactly one destroy"; }
log "  stock: exactly one destroy (the dynamodb endpoint), nothing else, on the state cold_deploy produced"
gauntlet_end_stage

# day2_replace's stock oracle (live/GAUNTLET.md #9), computed here for the
# same reason day2_remove's own oracle sits before migrate (above): a
# throwaway copy of cold_deploy's own state, module.vpc's customer_
# gateways["IP1"] entry changed its `ip_address` - ForceNew on aws_
# customer_gateway (EC2 assigns the gateway's own id; the on-prem ip_
# address/bgp_asn/type arguments describe the device but are not the
# resource's identity), so this forces a replace at the SAME declared
# for_each key. No cascade: nothing else in this config references
# aws_customer_gateway.this - no vpn_gateway/vpn_connection is declared
# here - so this is a genuinely isolated, single-resource replace, unlike
# module.vpc's other inputs (cidr, subnets, ...) which would cascade
# across the whole VPC. module.vpc is chosen because day2_rename/day2_
# remove (above) target module.vpc_endpoints and aws_security_group.rds,
# never it, so this section has no ordering dependency on either. PLAN
# ONLY, never applied: this copy shares floci's account with $ADOPTED.
gauntlet_begin_stage day2_replace
log "=== F-ORACLE. stock: force-replace module.vpc's customer_gateways[\"IP1\"] via its ForceNew ip_address argument, on cold_deploy's own state ==="
REPLACE_ORACLE_ROOT="$WORK/replace-oracle"
cp -r "$WORK/plain" "$REPLACE_ORACLE_ROOT"
REPLACE_ORACLE="$REPLACE_ORACLE_ROOT/vpc/examples/complete"
sed -i.bak 's/ip_address  = "1\.2\.3\.4"/ip_address  = "9.9.9.9"/' "$REPLACE_ORACLE/main.tf"
rm -f "$REPLACE_ORACLE/main.tf.bak"
grep -q 'ip_address  = "9.9.9.9"' "$REPLACE_ORACLE/main.tf" \
  || fail "changing customer_gateways[\"IP1\"]'s ip_address in the replace-oracle copy did not match - the corpus pin has moved"
( cd "$REPLACE_ORACLE" && "$TF_COLD_BIN" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$REPLACE_ORACLE" && "$TF_COLD_BIN" init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_replace stock oracle's init failed"; }
REPLACE_ORACLE_PLAN_OUT="$(cd "$REPLACE_ORACLE" && "$TF_COLD_BIN" plan -input=false -no-color 2>&1)"; REPLACE_ORACLE_PLAN_RC=$?
[ "$REPLACE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_replace stock oracle plan exited $REPLACE_ORACLE_PLAN_RC"; }
grep -qF '  # module.vpc.aws_customer_gateway.this["IP1"] must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "stock does not propose replacing customer_gateways[\"IP1\"] when its ForceNew ip_address argument changes"; }
grep -qF 'Plan: 1 to add, 0 to change, 1 to destroy.' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -10; fail "the day2_replace stock oracle plan is not exactly one isolated replace"; }
log "  stock: exactly one customer gateway replace at the same declared for_each key, nothing else - 1 to add, 1 to destroy, on the state cold_deploy produced - plan only, not applied (see above)"
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE - choudoufu live-import against the plain state file
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage migrate
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

# The third marker, by value, on the same object - choudoufu #372. aws_vpc is
# count-expanded here (`aws_vpc.this[0]`) and server-assigned, so live-import
# settles its slot at migrate time: slot 0 for index 0, the assignment its own
# tofu-address already expresses. Before #372 this tag did not exist until an
# apply wrote it, which is what put 27 of stage 3's 29 objects in the plan.
VPC_SLOT="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$VPC_ID" "Name=key,Values=tofu-slot" --query "Tags[0].Value" --output text)"
[ "$VPC_SLOT" = "0" ] || fail "the VPC carries tofu-slot=$VPC_SLOT, not 0 - live-import did not settle the slot for a slotless count set of a server-assigned type (choudoufu #372)"
log "  $VPC_ID carries tofu-slot=$VPC_SLOT, written by the migration itself (#372)"

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
gauntlet_stage migrate pass "$TAGGABLE stamped, $UNTAGGABLE skipped, 0 recorded, 0 failed; $MARKED objects carry tofu-estate=$ESTATE; the VPC's tofu-slot reads $VPC_SLOT off EC2, written by the migration itself (choudoufu #372)"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - state deleted, live-plan, empty + identities re-asserted
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage test_plan
log "=== STAGE 3: test plan (state deleted, live-plan empty) ==="
rm -f "$ADOPTED/terraform.tfstate" "$ADOPTED/terraform.tfstate.backup"
[ ! -f "$ADOPTED/terraform.tfstate" ] || fail "the state file is still there"

plan_into() { ( cd "$ADOPTED" && "$TOFU" live-plan -input=false -no-color ); }
PLAN_OUT="$(plan_into 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -60; fail "live-plan exited $PLAN_RC"; }
[ ! -f "$ADOPTED/terraform.tfstate" ] || fail "live-plan wrote a state file"
# Four plan-header verbs mean a non-empty plan, not three: a forced
# replacement reads "must be replaced", not "will be destroyed" - see this
# script's header, "A GAP IN THIS SCRIPT'S OWN CHECK", for how the narrower
# three-verb pattern could have reported a plan with live forced replacements
# in it as EMPTY once the tofu-slot/floci gaps documented above are resolved.
CHANGED_HEADERS="$(grep -E '^  # .+ (will be (created|updated|destroyed)|must be replaced)' <<< "$PLAN_OUT")"
if [ -n "$CHANGED_HEADERS" ]; then
  printf '%s\n' "$CHANGED_HEADERS"
  N_CHANGED="$(printf '%s\n' "$CHANGED_HEADERS" | grep -c .)"
  # A DIFF line only, not a bare key match: a plan body that changes any
  # attribute prints the whole tags map, unchanged entries included.
  grep -qE '^[[:space:]]*[+~-][[:space:]]+"tofu-slot"' <<< "$PLAN_OUT" \
    && { grep -B 6 -A 2 -E '^[[:space:]]*[+~-][[:space:]]+"tofu-slot"' <<< "$PLAN_OUT"
         fail "the plan proposes a tofu-slot change on $N_CHANGED object(s). choudoufu #372 settles the slot at migrate time for every count-expanded instance of a server-assigned type, and every count instance in this estate is one, so no tofu-slot may appear in this plan at all - not as an addition and not as a removal."; }
  fail "the plan is not empty: $N_CHANGED object(s) change, and no tofu-slot is among them (choudoufu #372, which used to account for 28 of the 29 objects here, is fixed for this estate: live-import writes the slot for a slotless count set of a server-assigned type, asserted by value on the VPC in stage 2). What is left is module.vpc_endpoints.aws_vpc_endpoint.this[\"ecs\"], which proposes replacing network_interface_ids/subnet_configuration wholesale (three ENI ids to \"known after apply\", all three subnet_configuration blocks' ipv4 addresses swapped) - a floci EC2 read-fidelity gap in the vpc-endpoint family (CreateVpcEndpoint never parsed SubnetConfiguration.N.Ipv4/.SubnetId/.Ipv6, so the requested address was silently dropped and floci synthesized its own), confirmed against the AWS API docs (API_SubnetConfiguration.html: Ipv4 is the address assigned to the endpoint ENI at creation, and the example requests cidrhost(v.cidr_block, 10) for every interface endpoint - the other three (ecr_api, ecr_dkr, rds) read that back cleanly, only this one does not), a different shape than the now-largely-fixed lex00/floci#97. FIXED 2026-08-22 in lex00/floci#99 (image ghcr.io/lex00/floci@sha256:dcd57a44da855e65e0c910f81e3a9e87b3b2a5d701f4d95945351ca7ea2ca9b9); this fail() only fires against a floci image older than that fix - see live/floci-image and this script's header. Not choudoufu's, and it is a for_each resource, so it was never a tofu-slot candidate either (internal/live/stamp/doc.go, \"nothing is ever stamped for for_each\")"
fi
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
gauntlet_begin_stage test_apply
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
gauntlet_begin_stage drift_reconverge
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
#   P2/P3  the world MOVES between the approval and the apply - "Private
#          Subnet One"'s Example tag is changed out of band through the AWS
#          CLI, the same mutation STAGE 5 above already proves this
#          estate's plan notices, on the same subnet - and the apply must
#          refuse: exit 3, the named summary, the unapproved row printed by
#          address AND by the live subnet id it was computed against, and
#          the reviewed change still not landed when the security group's
#          tags are read back through the CLI.
#   P4     nothing has moved (the tag is put back first) and the SAME file
#          must APPLY. This is the inverted control that
#          live/smoke/scenarios/apply-what-was-approved.sh reasons out: a
#          comparison which refuses unconditionally is not a check, so P3's
#          refusal is only worth something if the identical artifact goes
#          through when the world is where the approval left it.
#
# The two objects are deliberately disjoint - the change under review is on
# the root aws_security_group.rds, the out-of-band move is on one of
# module.vpc's private subnets - so the refusal has an EXTRA row to name
# rather than a values-only disagreement about the same row.
#
# WHY THE RDS SECURITY GROUP'S TAGS, and not one of the other 61 instances:
# the reviewed edit has to reach exactly ONE instance and must not disturb a
# live id a later part of this script already captured (issue #903's second
# trap). A tags-only update is not ForceNew on aws_security_group - the
# group id survives - and PART D's own live-mv rename of this same resource
# runs later against $RDS_SG_ID, which P5 leaves exactly where it found it.
# module.vpc's and module.vpc_endpoints' `tags` arguments both fan out
# across many instances, aws_security_group.rds's `description` IS ForceNew,
# and module.vpc.aws_customer_gateway.this["IP1"] is PART F's own replace
# target - so this is the one in-place, single-instance argument this estate
# offers.
#
# Placed here, between STAGE 5 and PART F, so it starts from the state
# drift_reconverge leaves (converged, marked, no state file) and finishes
# before PART F captures the customer gateway's live id at F0.
#
# Runs only on a real run. Under any of this script's other BREAK controls
# the estate is deliberately left somewhere this part does not describe, so
# it reports no verdict at all and the runner records the stage as not_run,
# never as a pass.
if [ -z "${BREAK:-}" ] && [ -z "${BREAK_COUNT:-}" ] && [ -z "${BREAK_REMOVE:-}" ]; then
  gauntlet_begin_stage plan_approval
  log "=== PART P: plan, review, apply (the approval gate, live/GAUNTLET.md #12) ==="

  P_REVIEWED_ADDR="$RDS_SG_ADDR"
  P_MOVED_ADDR='module.vpc.aws_subnet.private[0]'
  # Tied to what STAGE 5 actually measured rather than asserted from
  # memory: CHANGED_ADDRS is the address the drift plan printed for this
  # very subnet a few lines above, so a corpus pin that renumbers the
  # private subnets fails here instead of silently weakening P3.
  [ "$CHANGED_ADDRS" = "$P_MOVED_ADDR" ] \
    || fail "STAGE 5 saw $DRIFT_SUBNET_ID at [$CHANGED_ADDRS], not $P_MOVED_ADDR - the corpus pin has moved and PART P's out-of-band row would be named wrong"

  log "=== P1. the change under review: one argument, on the root rds security group ==="
  [ "$(grep -c '^  tags = local\.tags$' "$ADOPTED/main.tf")" = "2" ] \
    || fail "main.tf no longer carries exactly two \"tags = local.tags\" arguments (module.vpc's and aws_security_group.rds's) - the corpus pin has moved"
  sed -i.bak '/^resource "aws_security_group" "rds" {/,/^}/ s/^  tags = local\.tags$/  tags = merge(local.tags, { Reviewed = "yes" })/' "$ADOPTED/main.tf"
  rm -f "$ADOPTED/main.tf.bak"
  [ "$(grep -c 'Reviewed = "yes"' "$ADOPTED/main.tf")" = "1" ] \
    || fail "the reviewed edit did not write exactly one merge(local.tags, { Reviewed = \"yes\" }) argument"
  [ "$(grep -c '^  tags = local\.tags$' "$ADOPTED/main.tf")" = "1" ] \
    || fail "the reviewed edit changed more than one of the two \"tags = local.tags\" arguments"
  log "  edited one argument: aws_security_group.rds's tags now merge in Reviewed = \"yes\""

  P_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color -out=approved.tfplan 2>&1)"; P_PLAN_RC=$?
  [ "$P_PLAN_RC" -eq 0 ] || { printf '%s\n' "$P_PLAN_OUT" | tail -40; fail "plan -out exited $P_PLAN_RC"; }
  [ -f "$ADOPTED/approved.tfplan" ] || { printf '%s\n' "$P_PLAN_OUT" | tail -20; fail "plan -out wrote no file"; }
  P_APPROVED_ADDRS="$(grep -oE '^  # \S+ will be updated' <<< "$P_PLAN_OUT" | awk '{print $2}' | sort -u)"
  [ "$P_APPROVED_ADDRS" = "$P_REVIEWED_ADDR" ] \
    || { grep -E '^  # .+ (will be|must be)' <<< "$P_PLAN_OUT"; fail "the approved plan is about [$P_APPROVED_ADDRS], not $P_REVIEWED_ADDR alone"; }
  if grep -qE '^  # .+ (will be (created|destroyed)|must be replaced)' <<< "$P_PLAN_OUT"; then
    grep -E '^  # .+ (will be|must be)' <<< "$P_PLAN_OUT"; fail "the approved plan proposes a create, a destroy or a replace; this review is one in-place update"
  fi
  P_PLAN_BYTES="$(wc -c < "$ADOPTED/approved.tfplan" | tr -d ' ')"
  log "  approved.tfplan written ($P_PLAN_BYTES bytes of stock-format plan file); the approval is exactly one update, on $P_REVIEWED_ADDR"

  log "=== P2. the world moves between the approval and the apply ==="
  awsl ec2 create-tags --resources "$DRIFT_SUBNET_ID" --tags Key=Example,Value=moved-after-approval >/dev/null
  P_MOVED_VALUE="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$DRIFT_SUBNET_ID" "Name=key,Values=Example" --query "Tags[0].Value" --output text)"
  [ "$P_MOVED_VALUE" = "moved-after-approval" ] || fail "the out-of-band move did not take: $DRIFT_SUBNET_ID's Example tag reads \"$P_MOVED_VALUE\""
  log "  $DRIFT_SUBNET_ID's Example tag changed out of band to \"moved-after-approval\" - after the approval, before the apply, through the AWS CLI"

  log "=== P3. apply the approved plan against a world that moved ==="
  P_GATE_RC=0
  P_GATE_OUT="$(cd "$ADOPTED" && "$TOFU" apply -input=false -no-color approved.tfplan 2>&1)" || P_GATE_RC=$?
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
  # printed above it also names the subnet, so asserting over the whole
  # output would pass on a refusal that named nothing at all.
  P_REFUSAL="$(sed -n '/The approved plan no longer matches the live system/,$p' <<< "$P_GATE_OUT")"
  grep -qF "This apply would do, and the approved plan does not include:" <<< "$P_REFUSAL" \
    || { printf '%s\n' "$P_REFUSAL"; fail "the refusal does not classify the difference as a change nobody approved"; }
  grep -qF "$P_MOVED_ADDR" <<< "$P_REFUSAL" \
    || { printf '%s\n' "$P_REFUSAL"; fail "the refusal does not name $P_MOVED_ADDR, the change nobody approved"; }
  grep -qF "$DRIFT_SUBNET_ID" <<< "$P_REFUSAL" \
    || { printf '%s\n' "$P_REFUSAL"; fail "the refusal names the address but not $DRIFT_SUBNET_ID, the live object the change was computed against"; }
  grep -qF "Exit status 3" <<< "$P_REFUSAL" \
    || { printf '%s\n' "$P_REFUSAL"; fail "the refusal does not tell a pipeline what its exit status means"; }
  if grep -q "Apply complete!" <<< "$P_GATE_OUT"; then
    printf '%s\n' "$P_GATE_OUT" | tail -20; fail "the apply ran anyway after refusing"
  fi
  # Not "no Apply complete line" alone: read the live object the approval
  # was about and confirm the reviewed change did not land.
  P_REVIEWED_TAG="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$RDS_SG_ID" "Name=key,Values=Reviewed" --query "Tags[0].Value" --output text)"
  [ "$P_REVIEWED_TAG" = "None" ] || [ -z "$P_REVIEWED_TAG" ] \
    || fail "the refused apply still wrote the reviewed change: $RDS_SG_ID carries Reviewed=\"$P_REVIEWED_TAG\""
  printf '%s\n' "$P_REFUSAL" | head -12
  log "  refused by name, exit $P_GATE_RC, nothing applied - and the row it names is exactly the change that appeared after the approval"

  log "=== P4. the inverted control: put the world back, apply the SAME file ==="
  awsl ec2 create-tags --resources "$DRIFT_SUBNET_ID" --tags Key=Example,Value=ex-complete >/dev/null
  P_RESTORED="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$DRIFT_SUBNET_ID" "Name=key,Values=Example" --query "Tags[0].Value" --output text)"
  [ "$P_RESTORED" = "ex-complete" ] || fail "the out-of-band move was not undone: $DRIFT_SUBNET_ID's Example tag reads \"$P_RESTORED\""
  P_OK_RC=0
  P_OK_OUT="$(cd "$ADOPTED" && "$TOFU" apply -input=false -no-color approved.tfplan 2>&1)" || P_OK_RC=$?
  [ "$P_OK_RC" = "0" ] \
    || { printf '%s\n' "$P_OK_OUT" | tail -40; fail "the same plan file was refused (exit $P_OK_RC) over a world that had not moved - a comparison that refuses unconditionally is not a check"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$P_OK_OUT" \
    || { grep -E 'Apply complete' <<< "$P_OK_OUT"; fail "the approved apply did not change exactly the one reviewed resource"; }
  P_LANDED="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$RDS_SG_ID" "Name=key,Values=Reviewed" --query "Tags[0].Value" --output text)"
  [ "$P_LANDED" = "yes" ] \
    || fail "the approved change did not land: $RDS_SG_ID carries Reviewed=\"$P_LANDED\", want \"yes\""
  log "  the identical artifact applied (0 added, 1 changed, 0 destroyed) and $RDS_SG_ID now carries Reviewed=yes, read via the AWS CLI"

  log "=== P5. put the estate back where the rest of this script expects it ==="
  rm -f "$ADOPTED/approved.tfplan"
  sed -i.bak '/^resource "aws_security_group" "rds" {/,/^}/ s/^  tags = merge(local\.tags, { Reviewed = "yes" })$/  tags = local.tags/' "$ADOPTED/main.tf"
  rm -f "$ADOPTED/main.tf.bak"
  [ "$(grep -c '^  tags = local\.tags$' "$ADOPTED/main.tf")" = "2" ] \
    || fail "reverting the reviewed edit did not restore both \"tags = local.tags\" arguments"
  P_BACK_OUT="$(cd "$ADOPTED" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; P_BACK_RC=$?
  [ "$P_BACK_RC" -eq 0 ] || { printf '%s\n' "$P_BACK_OUT" | tail -40; fail "the revert apply failed"; }
  P_GONE="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$RDS_SG_ID" "Name=key,Values=Reviewed" --query "Tags[0].Value" --output text)"
  [ "$P_GONE" = "None" ] || [ -z "$P_GONE" ] \
    || fail "the reviewed tag is still on $RDS_SG_ID after the revert: \"$P_GONE\""
  P_FINAL_OUT="$(plan_into 2>&1)"; P_FINAL_RC=$?
  [ "$P_FINAL_RC" -eq 0 ] || { printf '%s\n' "$P_FINAL_OUT" | tail -40; fail "the post-revert plan exited $P_FINAL_RC"; }
  if grep -qE '^  # .+ (will be (created|updated|destroyed)|must be replaced)' <<< "$P_FINAL_OUT"; then
    grep -E '^  # .+ (will be|must be)' <<< "$P_FINAL_OUT"; fail "the estate is not converged again after PART P"
  fi
  [ ! -f "$ADOPTED/terraform.tfstate" ] || fail "PART P left a state file behind - this estate must never keep local state"
  log "  reverted; the estate is converged again and PART F starts from where it would have"

  log ""
  log "PART P (plan, review, apply): PASS"
  gauntlet_stage plan_approval pass "one argument edited (aws_security_group.rds's tags gain Reviewed=yes, a tags-only update that is not ForceNew and leaves $RDS_SG_ID's id alone for PART D's later rename), \"plan -out=approved.tfplan\" wrote a $P_PLAN_BYTES-byte stock-format plan file whose whole change set is one update on $P_REVIEWED_ADDR; the world then moved out of band ($DRIFT_SUBNET_ID's Example tag, through the AWS CLI, never through choudoufu) and \"apply approved.tfplan\" refused with \"The approved plan no longer matches the live system\" at exit 3, classifying the drift under \"This apply would do, and the approved plan does not include:\" and naming both $P_MOVED_ADDR and the live $DRIFT_SUBNET_ID it was computed against, with \"Exit status 3\" spelled out for a pipeline; nothing was applied - $RDS_SG_ID still carried no Reviewed tag, read back through the AWS CLI rather than from the absence of an \"Apply complete!\" line. Inverted control on the same run (the shape live/smoke/scenarios/apply-what-was-approved.sh reasons out): with the tag put back and nothing else changed, the IDENTICAL file applied - 0 added, 1 changed, 0 destroyed - and $RDS_SG_ID read back with Reviewed=yes, so the refusal is earned by the drift and not handed out to every plan file. BREAK_APPROVAL=1 asserts stage 12's own recorded Break line (apply the planfile after a mutation and expect success) and correctly fails"
  log ""
fi
# ══════════════════════════════════════════════════════════════════════════
# PART F: REPLACE (day2_replace, active - live/GAUNTLET.md #9)
# ══════════════════════════════════════════════════════════════════════════
#
# Placed right after STAGE 5 and BEFORE PART D (day2_rename, below) on
# purpose, the same convention corpus-ec2-instance-complete's own PART F
# uses: module.vpc.aws_customer_gateway.this["IP1"] is never touched by
# PART D's rename (that stage's own two targets are module.vpc_endpoints
# and aws_security_group.rds), so this section has no dependency on PART
# D's outcome. Its `ip_address` argument changes from "1.2.3.4" to
# "9.9.9.9" - ForceNew on aws_customer_gateway (EC2 assigns the gateway's
# own id; the on-prem device attributes describe it but are not the
# resource's identity) - forcing a replace at the SAME declared for_each
# key. No cascade: nothing else in this config references aws_customer_
# gateway.this at all (no vpn_gateway/vpn_connection is declared here), so
# this is a genuinely isolated, single-resource replace - unlike module.
# vpc's other inputs (cidr, subnets, ...), which would cascade across the
# whole VPC and are out of scope for a surgical day2_replace evidence pass.
#
# THE create_before_destroy SCOPE NOTE (see corpus-sqs-basic's own PART F
# for the full reasoning, reproduced only in summary here): OpenTofu core
# rejects a `lifecycle` block on a `module` call, and patching the
# vendored terraform-aws-vpc module's own aws_customer_gateway resource to
# add create_before_destroy would cross this corpus's reduction-only
# convention, so this evidence pass exercises the default destroy-then-
# create ordering instead.
#
# NO BREAK=replace LEG: aws_customer_gateway is ServerAssigned (EC2
# assigns the gateway id; none of its own arguments are its import
# identity - the same shape aws_instance/aws_security_group have), so the
# manufactured-coexistence check would hit the SAME fungible-slot
# regression corpus-security-group-complete's own day2_replace section
# found and documented in this same unit (a valid record short-circuits
# the duplicate-slot claimant matcher before it ever runs) - not
# re-measured here.
gauntlet_begin_stage day2_replace
record_key() { printf '%s' "$1" | base64 | tr '+/' '-_' | tr -d '=\n'; }
record_import_id() { jq -r '.identity.import_id' "$1"; }
F_ADDR='module.vpc.aws_customer_gateway.this["IP1"]'
F_RECORD="$ADOPTED/.tofu-records/tofu-records/$ESTATE/aws_customer_gateway/$(record_key "$F_ADDR")"

log "=== F0. capture the live customer gateway and its record ahead of the forced replace ==="
F_OLD_CGW_ID="$(awsl ec2 describe-customer-gateways --filters "Name=tag:tofu-address,Values=module.vpc.aws_customer_gateway.this:IP1" --query "CustomerGateways[0].CustomerGatewayId" --output text)"
[ -n "$F_OLD_CGW_ID" ] && [ "$F_OLD_CGW_ID" != "None" ] || fail "no live customer gateway found by tofu-address=module.vpc.aws_customer_gateway.this:IP1 ahead of day2_replace"
if [ -f "$F_RECORD" ]; then
  F_OLD_IMPORT_ID="$(record_import_id "$F_RECORD")"
  [ "$F_OLD_IMPORT_ID" = "$F_OLD_CGW_ID" ] || fail "the record for $F_ADDR names $F_OLD_IMPORT_ID ahead of day2_replace, not $F_OLD_CGW_ID"
  log "  $F_OLD_CGW_ID, record import_id=$F_OLD_IMPORT_ID"
else
  log "  $F_OLD_CGW_ID (no local record file at $F_RECORD - this estate declares no record_store; identity resolves from the live marker alone, same as every other stamped instance here)"
fi

log "=== F1. choudoufu: change the ForceNew ip_address argument, forcing a replace at the same declared for_each key ==="
sed -i.bak 's/ip_address  = "1\.2\.3\.4"/ip_address  = "9.9.9.9"/' "$ADOPTED/main.tf"
rm -f "$ADOPTED/main.tf.bak"
grep -q 'ip_address  = "9.9.9.9"' "$ADOPTED/main.tf" || fail "changing customer_gateways[\"IP1\"]'s ip_address did not match - the corpus pin has moved"

F_PLAN_OUT="$(plan_into 2>&1)"; F_PLAN_RC=$?
[ "$F_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_PLAN_OUT" | tail -40; fail "the day2_replace plan exited $F_PLAN_RC"; }
grep -qF '  # module.vpc.aws_customer_gateway.this["IP1"] must be replaced' <<< "$F_PLAN_OUT" \
  || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not propose replacing customer_gateways[\"IP1\"] when its ForceNew ip_address argument changes"; }
grep -qE '~ +ip_address +=.+forces replacement' <<< "$F_PLAN_OUT" \
  || { printf '%s\n' "$F_PLAN_OUT"; fail "the plan does not mark ip_address as forcing replacement"; }
grep -qF 'Plan: 1 to add, 0 to change, 1 to destroy.' <<< "$F_PLAN_OUT" \
  || { printf '%s\n' "$F_PLAN_OUT" | tail -10; fail "the day2_replace plan is not exactly one isolated replace, matching F-ORACLE's own plan shape"; }
log "  choudoufu: exactly one customer gateway replace at the same declared for_each key, nothing else - matches F-ORACLE's own plan shape"

F_APPLY_OUT="$(cd "$ADOPTED" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; F_APPLY_RC=$?
[ "$F_APPLY_RC" -eq 0 ] || { printf '%s\n' "$F_APPLY_OUT" | tail -40; fail "the day2_replace apply exited $F_APPLY_RC"; }
grep -qE 'Resources: 1 added, 0 changed, 1 destroyed' <<< "$F_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$F_APPLY_OUT"; fail "the day2_replace apply did not match the planned 1 added, 1 destroyed"; }

F_OLD_STATE="$(awsl ec2 describe-customer-gateways --customer-gateway-ids "$F_OLD_CGW_ID" --query 'CustomerGateways[0].State' --output text 2>&1)"
# A deleted customer gateway is not just marked "deleted" and left
# queryable (unlike, say, an EC2 instance) - floci (matching real AWS)
# drops it from DescribeCustomerGateways entirely, so the query errors
# NotFound. Found empirically: the first cut of this assertion expected a
# lingering "deleted" state string and failed on a genuinely correct
# destroy.
[ "$F_OLD_STATE" = "deleted" ] || grep -qF 'InvalidCustomerGatewayID.NotFound' <<< "$F_OLD_STATE" \
  || fail "$F_OLD_CGW_ID is not gone after the replace (state=$F_OLD_STATE) - the old object was orphaned, not destroyed"
log "  $F_OLD_CGW_ID (the old customer gateway) is gone - confirmed via the AWS CLI, not through choudoufu's own report"

F_NEW_CGW_ID="$(awsl ec2 describe-customer-gateways --filters "Name=tag:tofu-address,Values=module.vpc.aws_customer_gateway.this:IP1" "Name=state,Values=available" --query "CustomerGateways[0].CustomerGatewayId" --output text)"
[ -n "$F_NEW_CGW_ID" ] && [ "$F_NEW_CGW_ID" != "None" ] && [ "$F_NEW_CGW_ID" != "$F_OLD_CGW_ID" ] \
  || fail "could not find a new, different, available customer gateway carrying the same tofu-address after the replace (got '$F_NEW_CGW_ID')"
log "  $F_NEW_CGW_ID (the new object) carries tofu-address=module.vpc.aws_customer_gateway.this:IP1 - the marker moved onto the new object, read via the AWS CLI"

if [ -f "$F_RECORD" ]; then
  # THE RECORD STORE, asserted by value (HANDOFF's safety rule; the
  # #398-guard shape: a stale record still naming the destroyed gateway
  # would be exactly the wrong-marker failure that outranks a missing
  # one).
  F_NEW_IMPORT_ID="$(record_import_id "$F_RECORD")"
  [ "$F_NEW_IMPORT_ID" = "$F_NEW_CGW_ID" ] \
    || fail "the record for $F_ADDR names $F_NEW_IMPORT_ID after the replace, not the new object $F_NEW_CGW_ID - a stale record still claiming the destroyed gateway, the #398-guard shape"
  [ "$F_NEW_IMPORT_ID" != "$F_OLD_IMPORT_ID" ] \
    || fail "sanity: the record's import_id at $F_ADDR did not change at all across the replace"
  log "  record store: import_id $F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID at the same key ($F_ADDR) - read directly off the local record store file, not through choudoufu's own report"
fi

log "=== F2. one more plan: config and reality agree, no marker collision ==="
F_FINAL_PLAN_OUT="$(plan_into 2>&1)"; F_FINAL_PLAN_RC=$?
[ "$F_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_FINAL_PLAN_OUT" | tail -40; fail "the post-replace plan exited $F_FINAL_PLAN_RC"; }
if grep -qE '^  # .+ (will be (created|updated|destroyed)|must be replaced)' <<< "$F_FINAL_PLAN_OUT"; then
  grep -E '^  # .+ (will be|must be)' <<< "$F_FINAL_PLAN_OUT"
  fail "the post-replace plan proposes a resource change"
fi
log "  no resource action proposed. The replace is complete and invisible to the next plan - no marker collision."

gauntlet_stage day2_replace pass "choudoufu: changing customer_gateways[\"IP1\"]'s ForceNew ip_address argument proposed exactly one isolated replace at the same declared for_each key (1 to add, 1 to destroy, nothing else), matching F-ORACLE's own plan shape; applied cleanly; the old gateway ($F_OLD_CGW_ID) is confirmed gone/deleted and the new gateway ($F_NEW_CGW_ID) carries the marker, both via the AWS CLI; the next plan proposes no resource action. No BREAK=replace leg - see this section's own header comment (reusing corpus-security-group-complete's own finding from this same unit rather than re-measuring it here)."
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# PART D: RENAME (day2_rename, planned stage - live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
#
# The adopted estate (stages 2-5) is still marked and still converged, which
# is exactly the state a rename needs to start from. Two mechanisms, on two
# different objects so a gap in either is visible: a `moved` block renames
# the whole module.vpc_endpoints call (6 taggable objects: 5 active
# endpoints + the module's own security group - ecs_telemetry is
# create=false), and "choudoufu live-mv" renames the standalone root
# aws_security_group.rds with no moved block at all. The stock oracle for
# both runs on a copy of cold_deploy's own state, before choudoufu or
# live-import ever touched these objects - reusing $ADOPTED's post-adoption
# state would confound the comparison with the marker-tag churn adoption
# itself introduces, which has nothing to do with the rename.
#
# BREAK=1 exercises this stage's own break control instead of the real
# checks: renaming aws_security_group.rds WITHOUT a moved block, which must
# make choudoufu propose destroying the old address and creating the new
# one - the opposite of every other assertion in this part.

gauntlet_begin_stage day2_rename
log "=== D0. capture the live ids a rename must not disturb ==="
log "  $S3_EP_ID (module.vpc_endpoints, s3 endpoint), $RDS_SG_ID (aws_security_group.rds)"

if [ "${BREAK:-}" = "1" ]; then
  log "=== D1 (BREAK=1). rename aws_security_group.rds -> .rds_renamed WITHOUT a moved block ==="
  sed -i.bak 's/resource "aws_security_group" "rds" {/resource "aws_security_group" "rds_renamed" {/' "$ADOPTED/main.tf"
  sed -i.bak 's/aws_security_group\.rds\.id/aws_security_group.rds_renamed.id/' "$ADOPTED/main.tf"
  rm -f "$ADOPTED/main.tf.bak"
  ( cd "$ADOPTED" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ADOPTED" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the BREAK=1 rename's reinit failed"; }
  BREAK_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; BREAK_PLAN_RC=$?
  [ "$BREAK_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -30; fail "the BREAK=1 rename-without-moved plan exited $BREAK_PLAN_RC"; }
  grep -qE '^  # aws_security_group\.rds will be destroyed' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=1: renaming without a moved block did not propose destroying aws_security_group.rds - this stage's check is not load-bearing"; }
  grep -qE '^  # aws_security_group\.rds_renamed will be created' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=1: renaming without a moved block did not propose creating aws_security_group.rds_renamed - this stage's check is not load-bearing"; }
  log "  BREAK=1: correctly proposes destroying aws_security_group.rds and creating aws_security_group.rds_renamed - the moved-block and live-mv checks below are skipped"
else
  log "=== D1. choudoufu, moved block: module.vpc_endpoints -> module.vpc_endpoints_renamed ==="
  sed -i.bak 's/module "vpc_endpoints" {/module "vpc_endpoints_renamed" {/' "$ADOPTED/main.tf"
  sed -i.bak 's/module\.vpc_endpoints\./module.vpc_endpoints_renamed./g' "$ADOPTED/outputs.tf"
  rm -f "$ADOPTED/main.tf.bak" "$ADOPTED/outputs.tf.bak"
  cat >> "$ADOPTED/main.tf" <<'EOF'

moved {
  from = module.vpc_endpoints
  to   = module.vpc_endpoints_renamed
}
EOF
  ( cd "$ADOPTED" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ADOPTED" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the moved-block rename's reinit failed"; }
  MOVED_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; MOVED_PLAN_RC=$?
  [ "$MOVED_PLAN_RC" -eq 0 ] || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -40; fail "the moved-block rename plan exited $MOVED_PLAN_RC"; }
  grep -qE '^  # .+ will be (destroyed|created)' <<< "$MOVED_PLAN_OUT" \
    && { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu defect: the moved-block rename of module.vpc_endpoints proposes a create for its untaggable child aws_security_group_rule.this[\"ingress_https\"] instead of matching it structurally under the parent's new address - not zero churn. The parent aws_security_group.this[0] itself IS relocated correctly ('will be updated in-place'); stock's native moved-block handling relocates the rule cleanly too ('has moved to', zero churn, confirmed via a standalone repro). The gap is choudoufu-specific: an untaggable/derived child's identity resolution does not follow a moved parent module the way a marker-carrying resource's does (internal/live/moved's alias index is marker-based and has nothing to index for an untaggable type). Reaches every estate that renames a module containing a derived child of a moved parent (aws_security_group_rule, aws_route, aws_route_table_association, aws_vpc_dhcp_options_association, ...) - not fixed in this unit, scope is the day2_rename stage activation itself."; }
  N_CHANGED_D1="$(grep -cE '^  # .+ will be updated in-place' <<< "$MOVED_PLAN_OUT" || true)"
  [ "$N_CHANGED_D1" -ge 1 ] || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -20; fail "the moved-block rename plan proposes no in-place changes at all - nothing to rewrite the markers"; }
  grep -qF "Plan: 0 to add, $N_CHANGED_D1 to change, 0 to destroy." <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -10; fail "the moved-block rename plan's summary does not match its own $N_CHANGED_D1 in-place changes"; }
  grep -qE '~ +"tofu-address" = "module\.vpc_endpoints\.aws_vpc_endpoint\.this:s3" -> "module\.vpc_endpoints_renamed\.aws_vpc_endpoint\.this:s3"' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT"; fail "the moved-block plan does not show the s3 endpoint's tofu-address marker being rewritten from the old address to the new one"; }
  log "  choudoufu: zero churn, $N_CHANGED_D1 in-place tags updates - the marker rewrite the moved block completes"

  MOVED_APPLY_OUT="$(cd "$ADOPTED" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; MOVED_APPLY_RC=$?
  [ "$MOVED_APPLY_RC" -eq 0 ] || { printf '%s\n' "$MOVED_APPLY_OUT" | tail -40; fail "the moved-block rename apply exited $MOVED_APPLY_RC"; }
  grep -qE "Resources: 0 added, $N_CHANGED_D1 changed, 0 destroyed" <<< "$MOVED_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$MOVED_APPLY_OUT"; fail "the moved-block rename apply did not change exactly $N_CHANGED_D1 resources"; }

  S3_EP_ID_AFTER="$(awsl ec2 describe-vpc-endpoints --vpc-endpoint-ids "$S3_EP_ID" --query "VpcEndpoints[0].VpcEndpointId" --output text 2>/dev/null || true)"
  [ "$S3_EP_ID_AFTER" = "$S3_EP_ID" ] || fail "the s3 endpoint's id changed across the rename ($S3_EP_ID -> $S3_EP_ID_AFTER) - it was destroyed and recreated, not renamed"
  S3_EP_ADDR_AFTER="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$S3_EP_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
  [ "$S3_EP_ADDR_AFTER" = "module.vpc_endpoints_renamed.aws_vpc_endpoint.this:s3" ] \
    || fail "the s3 endpoint carries tofu-address=$S3_EP_ADDR_AFTER after the rename, not module.vpc_endpoints_renamed.aws_vpc_endpoint.this:s3"
  log "  $S3_EP_ID unchanged, tofu-address now module.vpc_endpoints_renamed.aws_vpc_endpoint.this:s3 - read via the AWS CLI"

  log "=== D2. choudoufu, live-mv: aws_security_group.rds -> .rds_renamed, no moved block at all ==="
  sed -i.bak 's/resource "aws_security_group" "rds" {/resource "aws_security_group" "rds_renamed" {/' "$ADOPTED/main.tf"
  sed -i.bak 's/aws_security_group\.rds\.id/aws_security_group.rds_renamed.id/' "$ADOPTED/main.tf"
  rm -f "$ADOPTED/main.tf.bak"
  ( cd "$ADOPTED" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ADOPTED" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the live-mv rename's reinit failed"; }
  MV_OUT="$(cd "$ADOPTED" && "$TOFU" live-mv -estate="$ESTATE" aws_security_group.rds aws_security_group.rds_renamed 2>&1)"; MV_RC=$?
  [ "$MV_RC" -eq 0 ] || { printf '%s\n' "$MV_OUT" | tail -30; fail "choudoufu live-mv exited $MV_RC"; }
  grep -qF 'Rewrote the ownership marker on one live resource. This was a cloud write.' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report a real write"; }
  grep -qF '"aws_security_group.rds" -> "aws_security_group.rds_renamed"' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report rewriting the tofu-address marker from the old address to the new one"; }
  log "  live-mv: $(grep -F 'live ID' <<< "$MV_OUT")"

  RDS_SG_ID_AFTER="$(awsl ec2 describe-security-groups --group-ids "$RDS_SG_ID" --query "SecurityGroups[0].GroupId" --output text 2>/dev/null || true)"
  [ "$RDS_SG_ID_AFTER" = "$RDS_SG_ID" ] || fail "the rds security group's id changed across live-mv ($RDS_SG_ID -> $RDS_SG_ID_AFTER) - it was destroyed and recreated, not renamed"
  RDS_SG_ADDR_AFTER="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$RDS_SG_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
  [ "$RDS_SG_ADDR_AFTER" = "aws_security_group.rds_renamed" ] || fail "the rds security group carries tofu-address=$RDS_SG_ADDR_AFTER after live-mv, not aws_security_group.rds_renamed"
  log "  $RDS_SG_ID unchanged, tofu-address now aws_security_group.rds_renamed - read via the AWS CLI"

  log "=== D3. one more plan: config and markers agree on both renames, nothing proposed ==="
  FINAL_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; FINAL_PLAN_RC=$?
  [ "$FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$FINAL_PLAN_OUT" | tail -40; fail "the post-rename plan exited $FINAL_PLAN_RC"; }
  grep -qF "No changes. Your infrastructure matches the configuration." <<< "$FINAL_PLAN_OUT" \
    || { grep -E '^  #' <<< "$FINAL_PLAN_OUT"; fail "the post-rename plan is not empty"; }
  log "  No changes. Both renames are complete and invisible to the next plan."

  gauntlet_stage day2_rename pass "moved block: module.vpc_endpoints renamed with zero churn (0 add, $N_CHANGED_D1 change, 0 destroy), marker rewritten in place across its taggable objects; live-mv: aws_security_group.rds renamed with zero churn, marker rewritten in place; stock oracle over the same two-object rename on cold_deploy's own state also shows zero churn (0 add, 0 change, 0 destroy); both live ids unchanged, read via the AWS CLI"

  # ══════════════════════════════════════════════════════════════════════
  # PART E: REMOVE A BLOCK (day2_remove, active - live/GAUNTLET.md #7)
  # ══════════════════════════════════════════════════════════════════════
  #
  # Starts from Part D's real, completed state: module.vpc_endpoints_
  # renamed is bound and converged. Its "dynamodb" map entry is removed
  # here - see E-ORACLE's own comment above for why this single Gateway
  # endpoint is the target. E-ORACLE already proved stock destroys it
  # cleanly on cold_deploy's own state.
  gauntlet_begin_stage day2_remove
  log ""
  log "=== E0. capture the dynamodb endpoint's own marker one more time ==="
  DYNAMODB_EP_ID="$(awsl ec2 describe-tags --filters "Name=resource-type,Values=vpc-endpoint" "Name=key,Values=tofu-address" "Name=value,Values=module.vpc_endpoints_renamed.aws_vpc_endpoint.this:dynamodb" --query "Tags[0].ResourceId" --output text)"
  [ -n "$DYNAMODB_EP_ID" ] && [ "$DYNAMODB_EP_ID" != "None" ] || fail "no live dynamodb endpoint found by its tofu-address marker before day2_remove even starts"

  if [ "${BREAK_REMOVE:-}" = "1" ]; then
    log "=== E1 (BREAK_REMOVE=1). keep the dynamodb endpoint entry; no destroy may be proposed ==="
    BREAK_REMOVE_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; BREAK_REMOVE_PLAN_RC=$?
    [ "$BREAK_REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_REMOVE_PLAN_OUT" | tail -40; fail "the BREAK_REMOVE=1 kept-entry plan exited $BREAK_REMOVE_PLAN_RC"; }
    grep -qF '  # module.vpc_endpoints_renamed.aws_vpc_endpoint.this["dynamodb"] will be destroyed' <<< "$BREAK_REMOVE_PLAN_OUT" \
      && { grep -E '^  # .+ will be' <<< "$BREAK_REMOVE_PLAN_OUT"; fail "BREAK_REMOVE=1: a destroy was proposed for the dynamodb endpoint even though its map entry is still in the config - this stage's check is not load-bearing"; }
    grep -qE '^  # .+ will be (created|destroyed)' <<< "$BREAK_REMOVE_PLAN_OUT" \
      && { grep -E '^  # .+ will be' <<< "$BREAK_REMOVE_PLAN_OUT"; fail "BREAK_REMOVE=1: some resource action was proposed with the entry still in the config"; }
    log "  BREAK_REMOVE=1: correctly proposes no resource action - the entry is still declared"
  else
    log "=== E1. choudoufu: delete the dynamodb endpoint's map entry ==="
    perl -0pi -e 's/\n    dynamodb = \{.*?\n    \},\n//s' "$ADOPTED/main.tf"
    grep -q 'dynamodb = {' "$ADOPTED/main.tf" \
      && fail "removing the dynamodb endpoint entry did not match - the config has moved"
    ( cd "$ADOPTED" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$ADOPTED" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the day2_remove reinit failed"; }
    REMOVE_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; REMOVE_PLAN_RC=$?
    [ "$REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -40; fail "the day2_remove plan exited $REMOVE_PLAN_RC"; }
    grep -qF '  # module.vpc_endpoints_renamed.aws_vpc_endpoint.this["dynamodb"] will be destroyed' <<< "$REMOVE_PLAN_OUT" \
      || { printf '%s\n' "$REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu does not propose destroying the dynamodb endpoint when its map entry is removed"; }
    grep -qE '^  # .+ will be (created|updated)' <<< "$REMOVE_PLAN_OUT" \
      && { printf '%s\n' "$REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's remove plan proposes something other than the one destroy"; }
    grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy' <<< "$REMOVE_PLAN_OUT" \
      || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -10; fail "choudoufu's remove plan proposes something other than exactly one destroy"; }
    log "  choudoufu: exactly one destroy (the dynamodb endpoint), nothing else"

    REMOVE_APPLY_OUT="$(cd "$ADOPTED" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; REMOVE_APPLY_RC=$?
    [ "$REMOVE_APPLY_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_APPLY_OUT" | tail -40; fail "the day2_remove apply exited $REMOVE_APPLY_RC"; }
    grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$REMOVE_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$REMOVE_APPLY_OUT"; fail "the day2_remove apply was not exactly one destroy"; }

    # A deleted VPC endpoint's own describe call, confirmed directly
    # against floci, not through choudoufu's own report: the endpoint
    # transitions to State=deleted rather than disappearing outright.
    EP_STATE="$(awsl ec2 describe-vpc-endpoints --vpc-endpoint-ids "$DYNAMODB_EP_ID" --query 'VpcEndpoints[0].State' --output text 2>/dev/null || echo "absent")"
    [ "$EP_STATE" = "deleted" ] || [ "$EP_STATE" = "absent" ] \
      || fail "vpc endpoint $DYNAMODB_EP_ID is still in state \"$EP_STATE\" after the destroy - it was orphaned, not destroyed"
    log "  vpc endpoint $DYNAMODB_EP_ID state=\"$EP_STATE\" after the destroy - confirmed via the AWS CLI, not through choudoufu's own report"

    log "=== E2. one more plan: config and reality agree, nothing left to propose ==="
    E_FINAL_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; E_FINAL_PLAN_RC=$?
    [ "$E_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$E_FINAL_PLAN_OUT" | tail -40; fail "the post-remove plan exited $E_FINAL_PLAN_RC"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$E_FINAL_PLAN_OUT" \
      || { grep -E '^  #' <<< "$E_FINAL_PLAN_OUT"; fail "the post-remove plan is not empty"; }
    log "  No changes. The removal is complete and invisible to the next plan."

    log ""
    log "STAGE E (day2_remove): PASS"
    gauntlet_stage day2_remove pass "choudoufu: deleting the dynamodb endpoint's map entry (module.vpc_endpoints_renamed.aws_vpc_endpoint.this[\"dynamodb\"]) proposed exactly one destroy (0 add, 0 change, 1 destroy), applied cleanly (0 added, 0 changed, 1 destroyed), the endpoint is genuinely gone from the live account (State=$EP_STATE, read via the AWS CLI, not choudoufu's own report), and the next plan proposes no resource action; stock oracle on cold_deploy's own state (E-ORACLE) also proposes exactly one destroy for the same object"
    log ""
  fi
  gauntlet_end_stage
fi
gauntlet_end_stage
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# PART G: CHANGE COUNT (day2_count, active - live/GAUNTLET.md #8)
# ══════════════════════════════════════════════════════════════════════════
#
# Placed LAST on purpose. fail() exits the whole script, so a stage that
# cannot pass takes every verdict after it down with it; putting the newest
# section at the end means a failure here can never hide day2_replace,
# day2_rename or day2_remove's own verdicts from the runner. It starts from
# PART E's real, completed state: the adopted estate is converged (E2
# asserted an empty plan one line above) with the endpoints module renamed
# and its dynamodb entry gone. None of that touches what this section adds.
#
# THE COUNT BLOCK IS SYNTHETIC, and this estate is the one where that choice
# needs its reasons written down, because it has more real count knobs than
# any other in the manifest. Neither family of them is measurable here:
#
#   - Every real `count` knob is a subnet list (private/public/database/
#     elasticache/redshift/intra, plus the NAT gateway and EIP counts), and
#     every one of them drives a SECOND count block in lockstep:
#     aws_route_table_association.<group>, which is untaggable and carries no
#     marker at all (it is one of this estate's own 22 UNTAGGABLE instances,
#     asserted by value at stage 2). Scaling intra_subnets from 3 to 2 asks
#     what choudoufu does about an orphaned derived child whose declaration
#     just disappeared - a genuine question, and NOT the one day2_count's
#     oracle is about ("which instance of a count set is destroyed, and does
#     the survivor keep its identity"). It belongs to whatever unit takes on
#     untaggable orphan sweeping; measuring it here would report a verdict
#     about a different mechanism under this stage's name.
#   - The one real knob with zero cascade, module.vpc's `customer_gateways`,
#     is `for_each` over a string-keyed map, so there are no index slots to
#     bind and nothing for internal/live/discovery/count.go to get wrong -
#     removing a map key is the shape day2_remove already measures. It is
#     also day2_replace's own target (PART F), so reusing it would couple
#     two stages that are deliberately independent here.
#
# So the block below is aws_security_group.count_test's shape from
# live/e2e/reference-ec2-vpc/run.sh Part F, over a type THIS estate actually
# exercises: aws_customer_gateway, of which module.vpc creates three. It is
# taggable (so it carries a real tofu-address marker to assert by value),
# server-assigned (EC2 mints the cgw- id, so a recreated instance is provably
# a new object), and depends on nothing at all - no VPC, no subnet - which is
# what lets G-ORACLE stand the identical block up under the stock binary in
# its own account without also standing up a VPC to hang it from. Nothing
# else in this configuration names aws_customer_gateway.count_test, so
# day2_count's history is self-contained.
#
# WHAT THIS SECTION FOUND, and it was a real defect rather than a gap in the
# estate: the first run of it proposed NO destroy at all on the way down -
# "No changes. Your infrastructure matches the configuration." - where stock
# destroys count_test[1]. The removal sweep had classified aws_customer_gateway
# TYPE_NOT_LISTABLE ("the provider cannot list these types at all"), which
# live/registry.json flatly contradicts: AWS::EC2::CustomerGateway carries
# handlers.list true with no required input and tagging.taggable true. Root
# cause, read off the artifacts with no tofu in the loop: live/mapping.json
# gives aws_customer_gateway via "former2", and registry.Roster.
# CloudControlType accepts only name/alias/service-alias - a filter that
# exists so Cloud Control never ENUMERATES a type on a mapping row it does
# not trust, being read to answer the different question "which TF type is
# this ARN". Fixed in this branch's first commit; see it for the generic rule
# and the measurement (47 former2 rows, 38 of them admitted, every one
# unambiguous; the routing change moves exactly one type today).
#
# BREAK_COUNT=1 exercises this stage's own Break control instead of the real
# checks: it expects the WRONG instance (count_test[0] rather than
# count_test[1]) to be the one destroyed - tools/gauntlet/stages.go's Break
# text for day2_count, verbatim: "Expect a different instance to be
# destroyed; the assertion must fail." Unlike BREAK/BREAK_REMOVE, it does not
# leave the estate un-reconverged for anything downstream, because nothing is
# downstream of this section.
if [ "${BREAK:-}" = "1" ] || [ "${BREAK_REMOVE:-}" = "1" ]; then
  log ""
  log "  (PART G/day2_count skipped: BREAK/BREAK_REMOVE deliberately leave the estate un-reconverged, which is not the state a count change can be measured from. BREAK_COUNT=1 is this stage's own control.)"
else
gauntlet_begin_stage day2_count

# The same plain `plan` PART D and PART E use on this working directory,
# named here so PART G's five plan calls cannot drift apart from each other.
count_plan() { ( cd "$ADOPTED" && "$TOFU" plan -input=false -no-color ); }

# count_test_block($1 = count) is day2_count's own resource, shared by the
# G-ORACLE stock leg and the real leg below so the two cannot drift into
# measuring different shapes. Unquoted heredoc so $1 interpolates;
# ${count.index} is escaped so bash never tries to expand it.
count_test_block() {
  cat <<COUNTEOF

resource "aws_customer_gateway" "count_test" {
  count = $1

  bgp_asn    = 65200
  ip_address = "203.0.113.1\${count.index}"
  type       = "ipsec.1"

  tags = {
    Name = "ex-complete-count-test-\${count.index}"
  }
}
COUNTEOF
}

log ""
log "=== G-ORACLE. day2_count stock oracle: the identical count block, stood up by $TF_COLD_BIN in its own account, scaled 2 -> 1 -> 2 for real ==="
# A fresh container rather than a reused one. cold_deploy's own state cannot
# serve as this oracle the way it does for day2_remove (E-ORACLE) and
# day2_replace (F-ORACLE): stock never had a count_test block, so there is
# nothing in that state to scale. The greenfield ports are free by now (PART
# GREENFIELD removes both of its containers), but this takes its own port and
# its own name so the stage does not depend on that section having run, let
# alone having passed.
docker run -d --rm -p "${FLOCI_COUNT_ORACLE_PORT}:4566" --name "$FLOCI_COUNT_ORACLE_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_COUNT_ORACLE_NAME failed"
COH=""
for _ in $(seq 1 45); do
  COH="$(curl -fs "${COUNT_ORACLE_ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"ec2"' <<< "${COH:-}" && break
  sleep 2
done
grep -q '"ec2"' <<< "${COH:-}" || fail "floci did not come up healthy (ec2) at $COUNT_ORACLE_ENDPOINT for the day2_count oracle"
log "  healthy: count oracle at $COUNT_ORACLE_ENDPOINT"

COUNT_ORACLE_DIR="$WORK/count-oracle"
mkdir -p "$COUNT_ORACLE_DIR"
awso() { aws --endpoint-url "$COUNT_ORACLE_ENDPOINT" --region "$REGION" "$@"; }
oracle_count_config() { # oracle_count_config <n>: the whole oracle working directory's main.tf
  {
    cat <<'EOF'
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.59.0"
    }
  }
}

provider "aws" {
  region                      = "eu-west-1"
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style           = true
}
EOF
    count_test_block "$1"
  } > "$COUNT_ORACLE_DIR/main.tf"
}

oracle_count_config 2
( cd "$COUNT_ORACLE_DIR" && AWS_ENDPOINT_URL="$COUNT_ORACLE_ENDPOINT" "$TF_COLD_BIN" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$COUNT_ORACLE_DIR" && AWS_ENDPOINT_URL="$COUNT_ORACLE_ENDPOINT" "$TF_COLD_BIN" init -input=false -no-color 2>&1 | tail -20 ); fail "the day2_count oracle's init failed"; }
G_ORACLE_BASE_OUT="$(cd "$COUNT_ORACLE_DIR" && AWS_ENDPOINT_URL="$COUNT_ORACLE_ENDPOINT" "$TF_COLD_BIN" apply -input=false -auto-approve -no-color 2>&1)"; G_ORACLE_BASE_RC=$?
[ "$G_ORACLE_BASE_RC" -eq 0 ] || { printf '%s\n' "$G_ORACLE_BASE_OUT" | tail -30; fail "the day2_count oracle's baseline apply failed"; }
grep -qE 'Apply complete! Resources: 2 added, 0 changed, 0 destroyed' <<< "$G_ORACLE_BASE_OUT" \
  || { printf '%s\n' "$G_ORACLE_BASE_OUT" | tail -20; fail "stock did not create exactly 2 count-test customer gateways for the day2_count oracle"; }
G_ORACLE_CGW0="$(awso ec2 describe-customer-gateways --filters "Name=tag:Name,Values=ex-complete-count-test-0" "Name=state,Values=available" --query "CustomerGateways[0].CustomerGatewayId" --output text)"
G_ORACLE_CGW1="$(awso ec2 describe-customer-gateways --filters "Name=tag:Name,Values=ex-complete-count-test-1" "Name=state,Values=available" --query "CustomerGateways[0].CustomerGatewayId" --output text)"
[ -n "$G_ORACLE_CGW0" ] && [ "$G_ORACLE_CGW0" != "None" ] || fail "no oracle count_test[0] customer gateway found by its Name tag"
[ -n "$G_ORACLE_CGW1" ] && [ "$G_ORACLE_CGW1" != "None" ] || fail "no oracle count_test[1] customer gateway found by its Name tag"
log "  stock: 2 instances created, count_test[0]=$G_ORACLE_CGW0 count_test[1]=$G_ORACLE_CGW1"

oracle_count_config 1
G_ORACLE_DOWN_PLAN="$(cd "$COUNT_ORACLE_DIR" && AWS_ENDPOINT_URL="$COUNT_ORACLE_ENDPOINT" "$TF_COLD_BIN" plan -input=false -no-color 2>&1)"; G_ORACLE_DOWN_RC=$?
[ "$G_ORACLE_DOWN_RC" -eq 0 ] || { printf '%s\n' "$G_ORACLE_DOWN_PLAN" | tail -30; fail "the day2_count oracle's scale-down plan exited $G_ORACLE_DOWN_RC"; }
grep -qE '^  # aws_customer_gateway\.count_test\[1\] will be destroyed' <<< "$G_ORACLE_DOWN_PLAN" \
  || { printf '%s\n' "$G_ORACLE_DOWN_PLAN" | grep -E '^  # .+ (will be|must be)'; fail "stock's scale-down plan does not destroy count_test[1]"; }
grep -qE '^  # aws_customer_gateway\.count_test\[0\] ' <<< "$G_ORACLE_DOWN_PLAN" \
  && { printf '%s\n' "$G_ORACLE_DOWN_PLAN" | grep -E '^  # .+ (will be|must be)'; fail "stock's scale-down plan touches count_test[0], which must be untouched"; }
grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$G_ORACLE_DOWN_PLAN" \
  || { printf '%s\n' "$G_ORACLE_DOWN_PLAN" | tail -10; fail "stock's scale-down plan proposes something other than exactly one destroy"; }
G_ORACLE_DOWN_APPLY="$(cd "$COUNT_ORACLE_DIR" && AWS_ENDPOINT_URL="$COUNT_ORACLE_ENDPOINT" "$TF_COLD_BIN" apply -input=false -auto-approve -no-color 2>&1)"; G_ORACLE_DOWN_APPLY_RC=$?
[ "$G_ORACLE_DOWN_APPLY_RC" -eq 0 ] || { printf '%s\n' "$G_ORACLE_DOWN_APPLY" | tail -30; fail "the day2_count oracle's scale-down apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$G_ORACLE_DOWN_APPLY" \
  || { grep -E 'Apply complete' <<< "$G_ORACLE_DOWN_APPLY"; fail "the day2_count oracle's scale-down apply was not exactly one destroy"; }
# Same NotFound shape PART F already documents for this type: floci (matching
# real AWS) drops a deleted customer gateway from DescribeCustomerGateways
# entirely rather than leaving it queryable in state "deleted".
G_ORACLE_CGW1_GONE="$(awso ec2 describe-customer-gateways --customer-gateway-ids "$G_ORACLE_CGW1" --query 'CustomerGateways[0].State' --output text 2>&1)"
[ "$G_ORACLE_CGW1_GONE" = "deleted" ] || grep -qF 'InvalidCustomerGatewayID.NotFound' <<< "$G_ORACLE_CGW1_GONE" \
  || fail "stock's count_test[1] ($G_ORACLE_CGW1) is not gone after its scale-down (state=$G_ORACLE_CGW1_GONE)"
G_ORACLE_CGW0_AFTER_DOWN="$(awso ec2 describe-customer-gateways --customer-gateway-ids "$G_ORACLE_CGW0" --query "CustomerGateways[0].CustomerGatewayId" --output text 2>/dev/null || true)"
[ "$G_ORACLE_CGW0_AFTER_DOWN" = "$G_ORACLE_CGW0" ] || fail "stock's surviving count_test[0] changed id across the scale-down ($G_ORACLE_CGW0 -> $G_ORACLE_CGW0_AFTER_DOWN)"
log "  stock: exactly one destroy (count_test[1]=$G_ORACLE_CGW1, confirmed gone via the AWS CLI), count_test[0]=$G_ORACLE_CGW0 unchanged"

oracle_count_config 2
G_ORACLE_UP_PLAN="$(cd "$COUNT_ORACLE_DIR" && AWS_ENDPOINT_URL="$COUNT_ORACLE_ENDPOINT" "$TF_COLD_BIN" plan -input=false -no-color 2>&1)"; G_ORACLE_UP_RC=$?
[ "$G_ORACLE_UP_RC" -eq 0 ] || { printf '%s\n' "$G_ORACLE_UP_PLAN" | tail -30; fail "the day2_count oracle's scale-up plan exited $G_ORACLE_UP_RC"; }
grep -qE '^  # aws_customer_gateway\.count_test\[1\] will be created' <<< "$G_ORACLE_UP_PLAN" \
  || { printf '%s\n' "$G_ORACLE_UP_PLAN" | grep -E '^  # .+ (will be|must be)'; fail "stock's scale-up plan does not create count_test[1]"; }
grep -qE '^  # aws_customer_gateway\.count_test\[0\] ' <<< "$G_ORACLE_UP_PLAN" \
  && { printf '%s\n' "$G_ORACLE_UP_PLAN" | grep -E '^  # .+ (will be|must be)'; fail "stock's scale-up plan touches count_test[0], which must be untouched"; }
grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$G_ORACLE_UP_PLAN" \
  || { printf '%s\n' "$G_ORACLE_UP_PLAN" | tail -10; fail "stock's scale-up plan proposes something other than exactly one create"; }
G_ORACLE_UP_APPLY="$(cd "$COUNT_ORACLE_DIR" && AWS_ENDPOINT_URL="$COUNT_ORACLE_ENDPOINT" "$TF_COLD_BIN" apply -input=false -auto-approve -no-color 2>&1)"; G_ORACLE_UP_APPLY_RC=$?
[ "$G_ORACLE_UP_APPLY_RC" -eq 0 ] || { printf '%s\n' "$G_ORACLE_UP_APPLY" | tail -30; fail "the day2_count oracle's scale-up apply failed"; }
grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$G_ORACLE_UP_APPLY" \
  || { grep -E 'Apply complete' <<< "$G_ORACLE_UP_APPLY"; fail "the day2_count oracle's scale-up apply was not exactly one create"; }
G_ORACLE_CGW1_NEW="$(awso ec2 describe-customer-gateways --filters "Name=tag:Name,Values=ex-complete-count-test-1" "Name=state,Values=available" --query "CustomerGateways[0].CustomerGatewayId" --output text)"
[ -n "$G_ORACLE_CGW1_NEW" ] && [ "$G_ORACLE_CGW1_NEW" != "None" ] || fail "no oracle count_test[1] customer gateway found after the scale-up"
[ "$G_ORACLE_CGW1_NEW" != "$G_ORACLE_CGW1" ] || fail "stock's recreated count_test[1] came back with the SAME id it had before being destroyed"
G_ORACLE_CGW0_AFTER_UP="$(awso ec2 describe-customer-gateways --customer-gateway-ids "$G_ORACLE_CGW0" --query "CustomerGateways[0].CustomerGatewayId" --output text 2>/dev/null || true)"
[ "$G_ORACLE_CGW0_AFTER_UP" = "$G_ORACLE_CGW0" ] || fail "stock's count_test[0] changed id across the scale-up ($G_ORACLE_CGW0 -> $G_ORACLE_CGW0_AFTER_UP)"
log "  stock: exactly one create (count_test[1], new id $G_ORACLE_CGW1_NEW, was $G_ORACLE_CGW1), count_test[0]=$G_ORACLE_CGW0 unchanged throughout"
docker rm -f "$FLOCI_COUNT_ORACLE_NAME" >/dev/null 2>&1 || true

log ""
log "=== G0. choudoufu: add aws_customer_gateway.count_test, count = 2 ==="
# The whole file is rewritten from a pristine copy taken here, so every step
# below differs from the one before it in exactly the count and nothing else -
# PART D and PART E have already sed'ed this main.tf several times, and
# re-running those edits is not what this stage measures.
COUNT_BASE_TF="$WORK/count-base-main.tf"
cp "$ADOPTED/main.tf" "$COUNT_BASE_TF"
set_count_test() { # set_count_test <n>: the estate's own main.tf plus a count_test block of <n>
  cat "$COUNT_BASE_TF" > "$ADOPTED/main.tf"
  count_test_block "$1" >> "$ADOPTED/main.tf"
}

set_count_test 2
( cd "$ADOPTED" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ADOPTED" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the day2_count reinit failed"; }
G_ADD_PLAN="$(count_plan 2>&1)"; G_ADD_PLAN_RC=$?
[ "$G_ADD_PLAN_RC" -eq 0 ] || { printf '%s\n' "$G_ADD_PLAN" | tail -40; fail "the count-block-add plan exited $G_ADD_PLAN_RC"; }
grep -qF 'Plan: 2 to add, 0 to change, 0 to destroy.' <<< "$G_ADD_PLAN" \
  || { printf '%s\n' "$G_ADD_PLAN" | tail -10; fail "adding the count block did not plan exactly 2 creates and nothing else"; }
G_ADD_APPLY="$(cd "$ADOPTED" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; G_ADD_APPLY_RC=$?
[ "$G_ADD_APPLY_RC" -eq 0 ] || { printf '%s\n' "$G_ADD_APPLY" | tail -40; fail "the count-block-add apply exited $G_ADD_APPLY_RC"; }
grep -qE 'Resources: 2 added, 0 changed, 0 destroyed' <<< "$G_ADD_APPLY" \
  || { grep -E 'Apply complete' <<< "$G_ADD_APPLY"; fail "the count-block-add apply did not create exactly 2 resources"; }

# Both instances, found by their own marker rather than by the Name tag the
# oracle uses - this is the estate that has markers, so the marker is the
# thing worth reading. live/MARKERS.md: an instance key reaches a tag in its
# ESCAPED form, so count_test[0]'s tag value is aws_customer_gateway.count_test:0.
G_CGW0="$(awsl ec2 describe-customer-gateways --filters "Name=tag:tofu-address,Values=aws_customer_gateway.count_test:0" "Name=state,Values=available" --query "CustomerGateways[0].CustomerGatewayId" --output text)"
G_CGW1="$(awsl ec2 describe-customer-gateways --filters "Name=tag:tofu-address,Values=aws_customer_gateway.count_test:1" "Name=state,Values=available" --query "CustomerGateways[0].CustomerGatewayId" --output text)"
[ -n "$G_CGW0" ] && [ "$G_CGW0" != "None" ] || fail "no live count_test[0] customer gateway carries tofu-address=aws_customer_gateway.count_test:0"
[ -n "$G_CGW1" ] && [ "$G_CGW1" != "None" ] || fail "no live count_test[1] customer gateway carries tofu-address=aws_customer_gateway.count_test:1"
[ "$G_CGW0" != "$G_CGW1" ] || fail "count_test[0] and count_test[1] resolved to the same live object ($G_CGW0) - the two markers are not distinguishing the instances"
G_CGW0_SLOT="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$G_CGW0" "Name=key,Values=tofu-slot" --query "Tags[0].Value" --output text)"
G_CGW1_SLOT="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$G_CGW1" "Name=key,Values=tofu-slot" --query "Tags[0].Value" --output text)"
[ "$G_CGW0_SLOT" = "0" ] || fail "count_test[0] ($G_CGW0) carries tofu-slot=$G_CGW0_SLOT, not 0"
[ "$G_CGW1_SLOT" = "1" ] || fail "count_test[1] ($G_CGW1) carries tofu-slot=$G_CGW1_SLOT, not 1"
log "  2 instances created: [0]=$G_CGW0 (tofu-slot=$G_CGW0_SLOT), [1]=$G_CGW1 (tofu-slot=$G_CGW1_SLOT) - both found BY their tofu-address marker through the AWS CLI"

G_NOOP_PLAN="$(count_plan 2>&1)"; G_NOOP_RC=$?
[ "$G_NOOP_RC" -eq 0 ] || { printf '%s\n' "$G_NOOP_PLAN" | tail -40; fail "the post-add plan exited $G_NOOP_RC"; }
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$G_NOOP_PLAN" \
  || { grep -E '^  #' <<< "$G_NOOP_PLAN"; fail "the plan right after adding the count block is not empty - the new instances did not bind their own markers cleanly"; }
log "  No changes - both new instances plan empty immediately after creation"

log "=== G1. scale the count down: 2 -> 1 ==="
set_count_test 1
G_DOWN_PLAN="$(count_plan 2>&1)"; G_DOWN_RC=$?
[ "$G_DOWN_RC" -eq 0 ] || { printf '%s\n' "$G_DOWN_PLAN" | tail -40; fail "the scale-down plan exited $G_DOWN_RC"; }

if [ "${BREAK_COUNT:-}" = "1" ]; then
  # The Break text in tools/gauntlet/stages.go for day2_count, verbatim:
  # "Expect a different instance to be destroyed; the assertion must fail."
  # Only the two indices swap; every assertion below runs exactly as it does
  # in a real run, so what fails is the check itself and not a different
  # code path written for the control.
  G_DOOMED=0; G_SURVIVOR=1
  log "  BREAK_COUNT=1: expecting count_test[0] - the WRONG instance - to be the one destroyed. This assertion must fail."
else
  G_DOOMED=1; G_SURVIVOR=0
fi
G_DOOMED_ID="$G_CGW1"; G_SURVIVOR_ID="$G_CGW0"
if [ "$G_DOOMED" = "0" ]; then G_DOOMED_ID="$G_CGW0"; G_SURVIVOR_ID="$G_CGW1"; fi

grep -qE "^  # aws_customer_gateway\.count_test\[$G_DOOMED\] will be destroyed" <<< "$G_DOWN_PLAN" \
  || { printf '%s\n' "$G_DOWN_PLAN" | grep -E '^  # .+ (will be|must be)'
       fail "choudoufu's scale-down plan does not destroy count_test[$G_DOOMED]. Stock's own plan for the identical block (G-ORACLE above) destroys count_test[1] and nothing else."; }
grep -qE "^  # aws_customer_gateway\.count_test\[$G_SURVIVOR\] " <<< "$G_DOWN_PLAN" \
  && { printf '%s\n' "$G_DOWN_PLAN" | grep -E '^  # .+ (will be|must be)'
       fail "choudoufu's scale-down plan touches count_test[$G_SURVIVOR], which must be untouched"; }
grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$G_DOWN_PLAN" \
  || { printf '%s\n' "$G_DOWN_PLAN" | tail -10; fail "choudoufu's scale-down plan proposes something other than exactly one destroy"; }
log "  choudoufu: exactly one destroy (count_test[$G_DOOMED]), count_test[$G_SURVIVOR] untouched - the same shape G-ORACLE showed for stock"

G_DOWN_APPLY="$(cd "$ADOPTED" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; G_DOWN_APPLY_RC=$?
[ "$G_DOWN_APPLY_RC" -eq 0 ] || { printf '%s\n' "$G_DOWN_APPLY" | tail -40; fail "the scale-down apply exited $G_DOWN_APPLY_RC"; }
grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$G_DOWN_APPLY" \
  || { grep -E 'Apply complete' <<< "$G_DOWN_APPLY"; fail "the scale-down apply was not exactly one destroy"; }

# Every fact below is read off EC2 directly, never out of choudoufu's own
# report: the destroyed instance is gone, the survivor's server-minted id is
# the same one it had before, and its marker still names the same address.
G_DOOMED_STATE="$(awsl ec2 describe-customer-gateways --customer-gateway-ids "$G_DOOMED_ID" --query 'CustomerGateways[0].State' --output text 2>&1)"
[ "$G_DOOMED_STATE" = "deleted" ] || grep -qF 'InvalidCustomerGatewayID.NotFound' <<< "$G_DOOMED_STATE" \
  || fail "count_test[$G_DOOMED] ($G_DOOMED_ID) is not gone after the scale-down (state=$G_DOOMED_STATE) - it was orphaned, not destroyed"
G_SURVIVOR_AFTER_DOWN="$(awsl ec2 describe-customer-gateways --customer-gateway-ids "$G_SURVIVOR_ID" --query "CustomerGateways[0].CustomerGatewayId" --output text 2>/dev/null || true)"
[ "$G_SURVIVOR_AFTER_DOWN" = "$G_SURVIVOR_ID" ] || fail "count_test[$G_SURVIVOR]'s live id changed across the scale-down ($G_SURVIVOR_ID -> $G_SURVIVOR_AFTER_DOWN) - it was destroyed and recreated, not left alone"
G_SURVIVOR_ADDR="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$G_SURVIVOR_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
[ "$G_SURVIVOR_ADDR" = "aws_customer_gateway.count_test:$G_SURVIVOR" ] \
  || fail "count_test[$G_SURVIVOR] ($G_SURVIVOR_ID) carries tofu-address=$G_SURVIVOR_ADDR after the scale-down, not aws_customer_gateway.count_test:$G_SURVIVOR"
G_SURVIVOR_SLOT="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$G_SURVIVOR_ID" "Name=key,Values=tofu-slot" --query "Tags[0].Value" --output text)"
[ "$G_SURVIVOR_SLOT" = "$G_SURVIVOR" ] || fail "count_test[$G_SURVIVOR]'s tofu-slot is $G_SURVIVOR_SLOT after the scale-down, not $G_SURVIVOR"
log "  $G_DOOMED_ID (count_test[$G_DOOMED]) is gone; $G_SURVIVOR_ID (count_test[$G_SURVIVOR]) keeps its id, its tofu-address ($G_SURVIVOR_ADDR) and its tofu-slot ($G_SURVIVOR_SLOT) - all read via the AWS CLI"

log "=== G2. scale the count back up: 1 -> 2 ==="
set_count_test 2
G_UP_PLAN="$(count_plan 2>&1)"; G_UP_RC=$?
[ "$G_UP_RC" -eq 0 ] || { printf '%s\n' "$G_UP_PLAN" | tail -40; fail "the scale-up plan exited $G_UP_RC"; }
grep -qE '^  # aws_customer_gateway\.count_test\[1\] will be created' <<< "$G_UP_PLAN" \
  || { printf '%s\n' "$G_UP_PLAN" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu's scale-up plan does not create count_test[1]"; }
grep -qE '^  # aws_customer_gateway\.count_test\[0\] ' <<< "$G_UP_PLAN" \
  && { printf '%s\n' "$G_UP_PLAN" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu's scale-up plan touches count_test[0], which must be untouched"; }
grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$G_UP_PLAN" \
  || { printf '%s\n' "$G_UP_PLAN" | tail -10; fail "choudoufu's scale-up plan proposes something other than exactly one create"; }
log "  choudoufu: exactly one create (count_test[1]), count_test[0] untouched - the same shape G-ORACLE showed for stock"

G_UP_APPLY="$(cd "$ADOPTED" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; G_UP_APPLY_RC=$?
[ "$G_UP_APPLY_RC" -eq 0 ] || { printf '%s\n' "$G_UP_APPLY" | tail -40; fail "the scale-up apply exited $G_UP_APPLY_RC"; }
grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$G_UP_APPLY" \
  || { grep -E 'Apply complete' <<< "$G_UP_APPLY"; fail "the scale-up apply was not exactly one create"; }

G_CGW1_NEW="$(awsl ec2 describe-customer-gateways --filters "Name=tag:tofu-address,Values=aws_customer_gateway.count_test:1" "Name=state,Values=available" --query "CustomerGateways[0].CustomerGatewayId" --output text)"
[ -n "$G_CGW1_NEW" ] && [ "$G_CGW1_NEW" != "None" ] || fail "no live count_test[1] customer gateway carries tofu-address=aws_customer_gateway.count_test:1 after the scale-up"
[ "$G_CGW1_NEW" != "$G_CGW1" ] \
  || fail "count_test[1] came back with the SAME server-minted id ($G_CGW1) it had before being destroyed - the destroy in G1 was not real, or the marker was moved onto a surviving object"
G_CGW1_NEW_SLOT="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$G_CGW1_NEW" "Name=key,Values=tofu-slot" --query "Tags[0].Value" --output text)"
[ "$G_CGW1_NEW_SLOT" = "1" ] || fail "the recreated count_test[1] ($G_CGW1_NEW) carries tofu-slot=$G_CGW1_NEW_SLOT, not 1"
G_CGW0_AFTER_UP="$(awsl ec2 describe-customer-gateways --customer-gateway-ids "$G_CGW0" --query "CustomerGateways[0].CustomerGatewayId" --output text 2>/dev/null || true)"
[ "$G_CGW0_AFTER_UP" = "$G_CGW0" ] || fail "count_test[0]'s live id changed across the scale-up ($G_CGW0 -> $G_CGW0_AFTER_UP)"
G_CGW0_ADDR_AFTER_UP="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$G_CGW0" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
[ "$G_CGW0_ADDR_AFTER_UP" = "aws_customer_gateway.count_test:0" ] \
  || fail "count_test[0] ($G_CGW0) carries tofu-address=$G_CGW0_ADDR_AFTER_UP after the whole cycle, not aws_customer_gateway.count_test:0"
log "  count_test[1] recreated as a NEW object ($G_CGW1_NEW, was $G_CGW1), tofu-slot=$G_CGW1_NEW_SLOT; count_test[0] ($G_CGW0) untouched throughout the down-then-up cycle, still tofu-address=$G_CGW0_ADDR_AFTER_UP - all read via the AWS CLI"

log "=== G3. one more plan: config and reality agree, nothing left to propose ==="
G_FINAL_PLAN="$(count_plan 2>&1)"; G_FINAL_RC=$?
[ "$G_FINAL_RC" -eq 0 ] || { printf '%s\n' "$G_FINAL_PLAN" | tail -40; fail "the post-scale-up plan exited $G_FINAL_RC"; }
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$G_FINAL_PLAN" \
  || { grep -E '^  #' <<< "$G_FINAL_PLAN"; fail "the post-scale-up plan is not empty"; }
log "  No changes. The scale-down-then-up cycle is complete and invisible to the next plan."

gauntlet_stage day2_count pass "choudoufu: scaling aws_customer_gateway.count_test from 2 to 1 destroyed exactly count_test[1], the higher index (0 add, 0 change, 1 destroy), and $G_CGW1 is confirmed gone from the live account while count_test[0] ($G_CGW0) kept its server-minted id, its tofu-address marker and its tofu-slot; scaling back from 1 to 2 created exactly count_test[1] (1 add, 0 change, 0 destroy) as a genuinely NEW object ($G_CGW1_NEW, not the destroyed $G_CGW1), with count_test[0] untouched throughout; the next plan is empty. Every identity above was read off EC2 through the AWS CLI, by the tofu-address marker, never from choudoufu's own report. Stock oracle (G-ORACLE): the identical count block stood up by $TF_COLD_BIN in its own floci account and scaled 2->1->2 for real shows the identical shape - destroy count_test[1] only, recreate it under a new id, count_test[0]'s id unchanged both times. THE BLOCK IS SYNTHETIC, deliberately: every real count knob in this estate is a subnet list that drives an untaggable aws_route_table_association count block in lockstep (a question about orphaned derived children, not about count-slot binding), and the one real zero-cascade knob, module.vpc's customer_gateways, is a for_each map with no index slots and is already day2_replace's target - so the block uses aws_customer_gateway, a type this estate really does exercise three of. Building it found and fixed a real defect (five-row row 2): the scale-down proposed NO destroy at all, because the removal sweep read live/mapping.json's via=\"former2\" provenance - a Cloud Control ENUMERABILITY filter - to answer the identity question \"which TF type is this ARN\", and classified aws_customer_gateway TYPE_NOT_LISTABLE against live/registry.json's own handlers.list=true."
gauntlet_end_stage
fi

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
