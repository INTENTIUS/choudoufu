#!/usr/bin/env bash
set -uo pipefail

# terraform-aws-modules/terraform-aws-autoscaling's flagship "complete"
# example (.corpus/autoscaling/examples/complete, pinned in
# live/corpus-manifest.json at tag v9.3.0, commit 75cce874), crossed
# through choudoufu against floci - the real, five-stage pipeline this
# goal's crossings follow (cold deploy, migrate, test plan, test apply,
# drift and reconverge), not the offline lint/identity check every earlier
# instrument here already runs. AutoScalingGroups are one of the most
# common resource types in real Terraform/OpenTofu estates, and this
# module's own example is deliberately exhaustive: twelve separate module
# calls exercising the default ASG shape, ignore_desired_capacity_changes
# (a SECOND, count-gated aws_autoscaling_group resource - "this" vs "idc",
# only one of which ever has count=1 in a given call - real exercise of
# the count-is-zero-per-instance admission fix), mixed instances policies,
# warm pools, EFA network interfaces, attribute-based instance
# requirements, external launch templates, lifecycle hooks, scheduled
# actions, target tracking / predictive / step scaling policies, plus
# supporting VPC, ALB, IAM role/instance profile/service-linked-role, SQS
# queue and CloudWatch alarm resources. floci's own AutoScalingGroup
# SuspendProcesses/ResumeProcesses support (the AWS provider calls
# SuspendProcesses unconditionally around every ASG create) was added the
# same night this crossing was written, specifically so a real popular
# module like this one could be tested against it rather than only the
# narrow repro that found the gap.
#
# THE ONE DELTA, same discipline live/e2e/corpus-iam-policy/run.sh
# documents: the example's own provider block gains the standard emulator
# connection flags, and its version constraint is pinned to the exact
# provider version this checkout's admission tables were generated against
# (6.59.0) for reproducibility. No resource in the example is edited,
# removed, or parameterized away - stage 1 runs the module exactly as
# terraform-aws-modules publishes it.
#
# THE MIGRATION EDIT, which is a different thing and belongs to stages 2-5
# only: the adopted copy's versions.tf gains a live block, and that block
# declares `record_store "local"`. Every other crossing here declares only
# the estate name. This one needs the store, and the reason is worth stating
# because it is the whole of what GitHub issue #353 changed: main.tf:889's
# aws_iam_service_linked_role.autoscaling carries a `provisioner
# "local-exec"` (the example's own `sleep 10`, commented "Sometimes good
# sleep is required to have some IAM resources created before they can be
# used"). Stock OpenTofu remembers one thing about a create-time
# provisioner - the tainted flag it sets when one fails - and a live
# resource marker cannot carry it, because internal/live/stamp writes the
# marker BEFORE the create request goes out. So a provisioner is refused
# for an estate with nowhere to keep that bit and admitted for one that has
# somewhere, and declaring the store is what an operator migrating this
# estate would actually do. It is not routing around the wall: it is the
# supported way past it, and live/e2e/provisioner-taint/run.sh is where the
# mechanism itself is proven end to end.
#
# Measured on this estate, at commit 7aea0eef95, both ways in the same
# session against the same floci pin: WITHOUT the record_store, stage 3
# fails on exactly one diagnostic, "Provisioners are not available under
# live resource markers". WITH it, that diagnostic is gone. The estate does
# NOT reach five of five as a result - see the stage-3 comment below and
# live/corpus-crossing-manifest.json for the two walls that stand behind it.
#
# STAGE-BY-STAGE SHAPE (issue #274's five-stage pipeline; see
# live/corpus-crossing-manifest.json):
#
#   1. COLD DEPLOY   plain `terraform apply` (real HashiCorp terraform, not
#                     choudoufu) against the unmodified example.
#   2. MIGRATE        `choudoufu live-import -state=<plain's state>
#                     -estate=... -approve` against that cold-deployed
#                     state.
#   3. TEST PLAN      delete the state file, `choudoufu live-plan`, assert
#                     the plan is EMPTY *and* assert a representative set
#                     of rendered identity strings against the AWS CLI's
#                     own answer.
#   4. TEST APPLY     apply that empty plan; assert a genuine no-op by
#                     comparing the estate's tagged-object count in floci
#                     before and after.
#   5. DRIFT AND      mutate one live object out of band via the AWS CLI
#      RECONVERGE     directly against floci (a tag value on the root SQS
#                     queue), replan, and assert the diff proposes fixing
#                     exactly that one object and nothing else.
#
# ON THE ASG'S OWN IDENTITY: an aws_autoscaling_group carries no ownership
# marker, and that is the marker vocabulary working rather than a gap. Its
# tags are `tag` NESTED BLOCKS, not the top-level tags map
# internal/live/markers.TagSurface requires, so Taggable() refuses it from
# the schema and live-import skips all eight of this example's ASGs as
# UNTAGGABLE. markers.go's Taggable doc comment names this exact type as the
# worked example of the shape it will not stamp. Stage 2 therefore asserts
# the ASG's resolved identity out of live-import's own UNTAGGABLE row, which
# prints the address beside the live id it bound to, and separately asserts
# that the live ASG carries ZERO tofu-* tags - a marker written into a tag
# block would be the wrong-marker failure this repository ranks above a
# missing one. An earlier version of this script asserted a tofu-address tag
# on the ASG instead. It had never run (stage 1 had never passed), it could
# never have passed, and floci does not implement autoscaling:DescribeTags
# either, so the CLI error was being swallowed by a 2>/dev/null and read
# back as an empty tag.
#
# BREAK=1 corrupts the expected identity string for the IAM role in stage 2
# (the header said "stage 3" until 2026-08-20; the assertion has always been
# in stage 2), and separately corrupts the drift assertion in stage 5, so
# both assertions are proven non-vacuous rather than a grep that always
# matches.
#
#   bash live/e2e/corpus-autoscaling-complete/run.sh
#
# Needs Docker and the AWS CLI, and the real `terraform` binary on PATH for
# stage 1 (`tofu` also works - see TF_COLD_BIN below), plus network access
# for `terraform init` to resolve terraform-aws-modules/vpc,
# terraform-aws-modules/security-group, terraform-aws-modules/alb and
# terraform-aws-modules/cloudwatch from the registry (same as
# demo-corpus-vpc-complete and demo-corpus-security-group-complete).
# .corpus is read, never written: the module and its example are copied
# out to a temp directory first, same as every other corpus crossing.
#
# Env overrides:
#   TOFU_BIN      path to a prebuilt choudoufu binary; skips the `go build`.
#   TF_COLD_BIN   the plain binary for stage 1 (default: `terraform` on
#                 PATH). Set to a `tofu` binary to use stock OpenTofu
#                 instead.
#   FLOCI_PORT    host port for the emulator (default 4722, clear of every
#                 other live/e2e fixture's port).
#   FLOCI_IMAGE   the emulator image; defaults to the digest pin in
#                 live/floci-image.
#   BREAK         set to 1 to corrupt an expected identity string and a
#                 drift assertion, proving both are load-bearing.
#
# This script is not routed around any real floci gap it hits (no
# -target, no resource removed from the example): it runs the real module
# and reports the real result, per this goal's own standing rule that a
# partial, accurate failure is worth more than a green run that does not
# hold up. See this crossing's own report for exactly what happened at
# each stage.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SRC_MODULE="$ROOT/.corpus/autoscaling"
SRC_EXAMPLE="$ROOT/.corpus/autoscaling/examples/complete"
WORK="$(mktemp -d)"
PLAIN="$WORK/plain/autoscaling/examples/complete"
ADOPTED="$WORK/adopted/autoscaling/examples/complete"
FLOCI_PORT="${FLOCI_PORT:-4722}"
FLOCI_NAME="choudoufu-corpus-autoscaling-complete-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="autoscaling-complete-crossing"
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
  cp -R "$SRC_MODULE" "$dest/autoscaling"
  rm -rf "$dest/autoscaling/examples"
  mkdir -p "$dest/autoscaling/examples/complete"
  cp -R "$SRC_EXAMPLE"/*.tf "$dest/autoscaling/examples/complete/"
  rm -rf "$dest/autoscaling/examples/complete/.terraform" "$dest/autoscaling/examples/complete/.terraform.lock.hcl"
}

# emulator_delta <example-dir>: the one onboarding delta - provider
# connection flags and a pinned provider version, nothing else.
emulator_delta() {
  local ex="$1"
  perl -0pi -e 's/(provider "aws" \{\n  region = local\.region\n)\}/$1\n  access_key                  = "test"\n  secret_key                  = "test"\n  skip_credentials_validation = true\n  skip_metadata_api_check     = true\n  skip_requesting_account_id  = true\n  s3_use_path_style           = true\n}/' "$ex/main.tf"
  grep -q 's3_use_path_style' "$ex/main.tf" || fail "the emulator delta did not match main.tf's provider block - the corpus pin has moved"
  perl -0pi -e 's/version = ">= 6\.56"/version = "= 6.59.0"/' "$ex/versions.tf"
  grep -q '= 6.59.0' "$ex/versions.tf" || fail "the version pin delta did not match versions.tf - the corpus pin has moved"
}

copy_estate "$WORK/plain"
emulator_delta "$PLAIN"
log "  module + example copied out of .corpus into $WORK/plain (stage 1: plain terraform)"

copy_estate "$WORK/adopted"
emulator_delta "$ADOPTED"
# The live block, plus a record_store. The store is not decoration and is
# not there for any record-backed type - this estate has none. It is what
# admits main.tf:889's `provisioner "local-exec"` (GitHub issue #353): a
# create-time provisioner that fails leaves a live, already-marked,
# half-built object, and the tainted bit stock keeps in its state file needs
# somewhere to live. Without this line the estate is refused at stage 3 with
# exactly that diagnostic, which is what it did until 2026-08-21.
perl -0pi -e 's/(required_providers \{\n    aws = \{\n      source  = "hashicorp\/aws"\n      version = "= 6\.59\.0"\n    \}\n  \}\n)\}/$1\n  live {\n    estate = "'"$ESTATE"'"\n\n    record_store "local" {\n      path = ".tofu-records"\n    }\n  }\n}/' "$ADOPTED/versions.tf"
grep -q "estate = \"$ESTATE\"" "$ADOPTED/versions.tf" || fail "the live-block delta did not match versions.tf"
grep -q 'record_store "local"' "$ADOPTED/versions.tf" || fail "the record_store delta did not match versions.tf"
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
  printf '%s\n' "$COLD_OUT" | grep -E '^Error' -A 6 | head -200
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
# PART D: RENAME (day2_rename, planned stage - live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
#
# The adopted estate (stages 2-5) is still marked and still converged, which
# is exactly the state a rename needs to start from. Two mechanisms, on two
# different objects so a gap in either is visible: a `moved` block renames
# the whole module.asg_sg call (a security group plus its computed ingress
# rule), and "choudoufu live-mv" renames the standalone root
# aws_sqs_queue.this with no moved block at all. The stock oracle for both
# runs on a copy of cold_deploy's own state, before choudoufu or live-import
# ever touched these objects.
#
# BREAK=1 exercises this stage's own break control instead of the real
# checks: renaming aws_sqs_queue.this WITHOUT a moved block, which must make
# choudoufu propose destroying the old address and creating the new one -
# the opposite of every other assertion in this part.

CURRENT_STAGE=day2_rename
log "=== D-ORACLE. stock: the same two renames, through moved blocks, on cold_deploy's own state ==="
PLAIN_ORACLE_ROOT="$WORK/plain-oracle"
cp -r "$WORK/plain" "$PLAIN_ORACLE_ROOT"
PLAIN_ORACLE="$PLAIN_ORACLE_ROOT/autoscaling/examples/complete"
sed -i.bak 's/module "asg_sg" {/module "asg_sg_renamed" {/' "$PLAIN_ORACLE/main.tf"
sed -i.bak 's/module\.asg_sg\./module.asg_sg_renamed./g' "$PLAIN_ORACLE/main.tf"
sed -i.bak 's/resource "aws_sqs_queue" "this" {/resource "aws_sqs_queue" "this_renamed" {/' "$PLAIN_ORACLE/main.tf"
sed -i.bak 's/aws_sqs_queue\.this\.name/aws_sqs_queue.this_renamed.name/' "$PLAIN_ORACLE/main.tf"
rm -f "$PLAIN_ORACLE/main.tf.bak"
cat >> "$PLAIN_ORACLE/main.tf" <<'EOF'

moved {
  from = module.asg_sg
  to   = module.asg_sg_renamed
}

moved {
  from = aws_sqs_queue.this
  to   = aws_sqs_queue.this_renamed
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
# STAGE 2: MIGRATE - choudoufu live-import against the plain state file
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=migrate
log "=== STAGE 2: migrate (choudoufu live-import -approve) ==="
( cd "$ADOPTED" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ADOPTED" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "adopted-copy init failed"; }

IMPORT_OUT="$(cd "$ADOPTED" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE" 2>&1)"; IMPORT_RC=$?
if [ "$IMPORT_RC" -ne 0 ]; then
  printf '%s\n' "$IMPORT_OUT" | tail -80
  fail "live-import (dry run) failed"
fi
grep -qF "No tag has been written." <<< "$IMPORT_OUT" || fail "the dry run wrote a tag - it must not"
log "  dry run: $(grep -oE '[0-9]+ of [0-9]+ resource instance\(s\) are eligible for stamping' <<< "$IMPORT_OUT")"

APPROVE_OUT="$(cd "$ADOPTED" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE" -approve 2>&1)"; APPROVE_RC=$?
if [ "$APPROVE_RC" -ne 0 ]; then
  printf '%s\n' "$APPROVE_OUT" | tail -80
  fail "live-import -approve failed"
fi
grep -qE '[0-9]+ resource\(s\) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, [0-9]+ skipped' <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT" | tail -40; fail "live-import -approve did not stamp cleanly"; }
log "  $(grep -oE '[0-9]+ resource\(s\) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, [0-9]+ skipped' <<< "$APPROVE_OUT")"

# ── the ASG's own identity, read out of live-import's UNTAGGABLE listing ──
# An aws_autoscaling_group carries NO ownership marker, and that is the
# product working rather than a gap: its tags are `tag` NESTED BLOCKS, not
# the top-level tags map internal/live/markers.TagSurface requires, so
# Taggable() refuses it by schema and live-import skips it. All eight ASGs
# in this example are skipped for exactly that reason. See
# internal/live/markers/markers.go's Taggable doc comment, which names this
# type as the worked example of the shape it will not stamp.
#
# So the ASG's identity cannot be read off a tag; it is asserted from
# live-import's own UNTAGGABLE row instead, which prints the resolved
# address BESIDE the live id it bound to - a cross-check between the two
# that a tag read-back would not give either. This is the assertion that
# exercises the count-is-zero-per-instance admission fix for real:
# module.complete sets ignore_desired_capacity_changes=true, so of the
# module's OWN two count-gated ASG resources (aws_autoscaling_group.this vs
# .idc, only one of which ever has count=1 in a given call) it must be .idc
# that resolves and .this that does not exist at all.
ASG_ADDR="module.complete.aws_autoscaling_group.idc[0]"
ASG_ROW="$(grep -F "$ASG_ADDR " <<< "$IMPORT_OUT" | head -1)"
[ -n "$ASG_ROW" ] || { grep -F 'aws_autoscaling_group' <<< "$IMPORT_OUT" | head -20; fail "live-import's listing never mentions $ASG_ADDR"; }
grep -qF 'aws_autoscaling_group' <<< "$ASG_ROW" || fail "$ASG_ADDR did not resolve as an aws_autoscaling_group: $ASG_ROW"
grep -qE 'live id: complete$' <<< "$ASG_ROW" || fail "$ASG_ADDR did not bind to live ASG 'complete': $ASG_ROW"
grep -qF 'module.complete.aws_autoscaling_group.this[' <<< "$IMPORT_OUT" \
  && fail "module.complete.aws_autoscaling_group.this has count=0 here (ignore_desired_capacity_changes=true) and must not resolve to any instance"
log "  $ASG_ADDR resolved to live ASG 'complete', untaggable by schema - the count-gated .this/.idc pair resolved to .idc"
ASG_TAGS_ON_LIVE="$(awsl autoscaling describe-auto-scaling-groups --auto-scaling-group-names complete \
  --query "AutoScalingGroups[0].Tags[?starts_with(Key, 'tofu-')] | length(@)" --output text)"
[ "$ASG_TAGS_ON_LIVE" = "0" ] || fail "ASG 'complete' carries $ASG_TAGS_ON_LIVE tofu-* tag(s); an ASG's tag blocks are not a marker surface and must never be stamped"
log "  ASG 'complete' carries 0 tofu-* tags, as the marker vocabulary requires"

# ── identity assertions, read via the AWS CLI directly, never through choudoufu ──
# A tag VALUE can never carry '[': internal/live/markers.EscapeKey renders
# an index as ':0'. The bracket spelling is what a plan diff header uses, so
# the two forms are separate variables here - comparing a tag against the
# bracket form is the vacuous comparison corpus-vpc-complete, corpus-iam-policy
# and corpus-iam-read-only-policy each shipped once.
LT_ADDR="module.complete.aws_launch_template.this:0"
LT_ADDR_PLAN="module.complete.aws_launch_template.this[0]"
# Found BY ITS MARKER, not by name: the module builds this launch template
# from name_prefix, so its live name carries a provider-minted random suffix
# ("complete-5c94d67aed...") that no assertion can hardcode. Reading the
# marker back as a lookup key is the product's own claim under test.
LT_ID="$(awsl ec2 describe-tags --filters "Name=key,Values=tofu-address" "Name=value,Values=$LT_ADDR" --query 'Tags[].ResourceId' --output text)"
[ -n "$LT_ID" ] && [ "$LT_ID" != "None" ] || fail "no live object carries tofu-address=$LT_ADDR"
# grep -c, not wc -w: BSD wc pads its count with leading spaces, so a
# string comparison against "1" never matches on macOS.
[ "$(printf '%s\n' $LT_ID | grep -c .)" = "1" ] || fail "tofu-address=$LT_ADDR is on more than one live object: $LT_ID"
LT_NAME="$(awsl ec2 describe-launch-templates --launch-template-ids "$LT_ID" --query "LaunchTemplates[0].LaunchTemplateName" --output text)"
case "$LT_NAME" in complete-*) ;; *) fail "tofu-address=$LT_ADDR resolved to launch template '$LT_NAME', which is not one of module.complete's (name_prefix 'complete-')" ;; esac
log "  $LT_ID ('$LT_NAME') carries tofu-address=$LT_ADDR"

# The IAM role named exactly "complete" is the example's ROOT-LEVEL
# aws_iam_role.ssm (name = local.name), not module.complete's own role -
# that one is built from a name_prefix and lands as "complete-5c82d83c...".
# The root role is the one with a stable name, so it is the one asserted by
# value here.
ROLE_TAG_VALUE="$(awsl iam list-role-tags --role-name complete --query "Tags[?Key=='tofu-address'].Value | [0]" --output text 2>/dev/null)"
[ -n "$ROLE_TAG_VALUE" ] && [ "$ROLE_TAG_VALUE" != "None" ] || fail "no tofu-address tag found on IAM role 'complete'"
if [ "${BREAK:-}" = "1" ]; then
  log "  BREAK=1: expecting the wrong address for role 'complete' on purpose - this check must fail"
  WANT_ROLE_ADDR="module.complete.aws_iam_role.this:0"
else
  WANT_ROLE_ADDR="aws_iam_role.ssm"
fi
[ "$ROLE_TAG_VALUE" = "$WANT_ROLE_ADDR" ] || fail "the IAM role carries tofu-address=$ROLE_TAG_VALUE, not $WANT_ROLE_ADDR"
log "  IAM role 'complete' carries tofu-address=$ROLE_TAG_VALUE"

MARKED="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
log "  $MARKED objects carry tofu-estate=$ESTATE after migration"
gauntlet_stage migrate pass "$(grep -oE '[0-9]+ resource\(s\) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, [0-9]+ skipped' <<< "$APPROVE_OUT"); $MARKED objects carry tofu-estate=$ESTATE"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - state deleted, live-plan, empty + identities re-asserted
# ══════════════════════════════════════════════════════════════════════════
# WHERE THIS STAGE ACTUALLY STOPS, re-measured 2026-08-22/23 against real
# Docker/floci/terraform/AWS CLI, hashicorp/aws 6.59.0, read off this
# script's own output and not inferred. History of fixed walls, oldest
# first:
#
#   1. FIXED (GitHub issue #354). "Non-static identity argument" on
#      module.complete.aws_autoscaling_traffic_source_attachment.this["ex-alb"].identifier.
#   2. FIXED (GitHub issue #369). "Ambiguous list-valued identity argument"
#      on module.asg_sg.aws_security_group_rule.computed_ingress_with_source_security_group_id[0].prefix_list_ids.
#   3. FIXED. "The identity table names something the provider does not
#      have" on aws_autoscaling_traffic_source_attachment
#      (componentSchemaBlock in internal/live/identity/schema_verify.go).
#   4. FIXED. SEVEN aws_autoscaling_group instances (one per module call
#      using name_prefix instead of name) fell to the record rung via
#      identity.RecordFallbackType rather than refusing outright.
#   5. FIXED 2026-08-23. The IAM role/instance-profile trio under
#      module.complete (name_prefix'd, so NEEDS_DISCOVERY at plan time)
#      showed "will be created" despite migrate having stamped both
#      correctly: internal/live/discovery/bindtags.go's tag-index join
#      (issue #266) keys a listed object's import ID against the estate's
#      tag index by the tagged resource's ARN resource-id segment - correct
#      for a root-path IAM entity, but an IAM entity under a non-default
#      Path (both of these are, name_prefix'd under "/ec2/") has an ARN
#      resource-id of "PATH/NAME" while its import ID (and iam:ListRoles'/
#      iam:ListInstanceProfiles' own listing) is the bare NAME alone - so
#      the join silently missed (joinNone, no diagnostic) for any such
#      entity. markerJoinKeys now adds the ARN's trailing path segment as a
#      third join key for IAM ARNs specifically (TestMarkerJoinKeysAddsIAMBareNameForAPathedEntity),
#      safe because IAM enforces name uniqueness per entity kind account-
#      wide independent of Path.
#
# What stands now:
#
#   - Several floci emulator gaps, confirmed at the API level with the AWS
#     CLI (no Terraform), each already fixed on a branch
#     (lex00/floci fix/launch-template-metadata-monitoring) but not yet
#     reflected here because publishing an image is a shared-layer change
#     this unit does not make: aws_launch_template drops metadata_options/
#     monitoring entirely (module.default/.efa/.instance_requirements/
#     .instance_requirements_accelerators/.launch_template_only/
#     .mixed_instance/.target_tracking_customized_metrics/.warm_pool/
#     .complete, and every aws_autoscaling_group whose launch_template.version
#     is therefore unknown); aws_autoscaling_policy drops Enabled/
#     StepAdjustments/PredictiveScalingConfiguration/ResourceLabel and
#     defaults Cooldown to 300 for every policy type instead of only
#     SimpleScaling (module.complete, module.target_tracking_customized_metrics);
#     aws_cloudwatch_metric_alarm drops TreatMissingData and invents a
#     DatapointsToAlarm default real AWS does not (lex00/floci#93,
#     reopened - confirmed directly against real AWS the earlier closure's
#     premise was backwards).
#   - lex00/floci#111 (already filed, open): module.vpc's default network
#     ACL loses its IPv6 egress/ingress rule's CIDR type on read.
#   - lex00/floci#112 (filed 2026-08-23): aws_autoscaling_group's
#     DescribeAutoScalingGroups drops most of its own optional fields
#     (default_instance_warmup, capacity_rebalance, service_linked_role_arn,
#     instance_maintenance_policy, availability_zone_distribution,
#     capacity_reservation_specification, mixed_instances_policy override
#     fields, traffic sources) - module.complete and module.mixed_instance.
#   - choudoufu#384 (filed 2026-08-23, NOT an emulator gap): a
#     Component.SoleElement identity alternation binds the wrong live
#     object - not merely a missing feature - when TWO of its alternative
#     attributes are genuinely non-empty at once, which is exactly
#     module.asg_sg's default shape (egress_cidr_blocks and
#     egress_ipv6_cidr_blocks both default non-empty in
#     terraform-aws-modules/security-group). module.asg_sg.aws_security_group_rule.egress_rules[0]
#     binds to the IPv4 rule while the config's real identity is the IPv6
#     one.
#   - choudoufu#385 (filed 2026-08-23): aws_autoscaling_group's
#     initial_lifecycle_hook is a NestingSet, ForceNew block the provider
#     never sources from the remote at all - internal/live/projection/residue.go
#     (issue #275) is the mechanism for exactly this shape but its own doc
#     comment explicitly excludes every collection-nested block, pending a
#     real design pass on whether the two-read discriminator generalizes
#     safely to a Set value.
#
# Stages 4 and 5 are therefore still not reached and stay not_run in
# live/corpus-crossing-manifest.json: running them against a non-empty plan
# would prove nothing.
CURRENT_STAGE=test_plan
log "=== STAGE 3: test plan (state deleted, live-plan empty) ==="
rm -f "$ADOPTED/terraform.tfstate" "$ADOPTED/terraform.tfstate.backup"
[ ! -f "$ADOPTED/terraform.tfstate" ] || fail "the state file is still there"

plan_into() { ( cd "$ADOPTED" && "$TOFU" live-plan -input=false -no-color ); }
PLAN_OUT="$(plan_into 2>&1)"; PLAN_RC=$?
# GitHub issue #354, asserted here rather than only in a unit test: the
# traffic-source attachment's identity argument must not raise "Non-static
# identity argument" again. Checked before the exit-code assertion below so
# that a regression names itself rather than arriving as "live-plan exited
# 1" among whatever else the stage still expects.
#
# Deliberately narrower than "no diagnostic at all naming the type": this
# type also raises a benign "Incomplete sweep for undeclared resources"
# WARNING (it has no CFN type the ARN-join table recognizes, internal/live/discovery/tagging.go),
# which is not #354 and used to make this very check false-fail on its own
# unrelated text - the type's name appearing anywhere is not evidence of
# #354's regression, only its specific summary is.
! { grep -qF 'Non-static identity argument' <<< "$PLAN_OUT" && grep -qF 'aws_autoscaling_traffic_source_attachment' <<< "$PLAN_OUT"; } \
  || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:|Non-static identity argument|traffic_source_attachment'; fail "#354's root cause is back on aws_autoscaling_traffic_source_attachment - it must not raise Non-static identity argument again"; }
if [ "$PLAN_RC" -ne 0 ]; then
  # A blind tail is not enough here: this plan's own output runs into the
  # thousands of lines (the diff for all 68 resources, printed before the
  # diagnostics), so "tail -80" can miss every "Error:" block entirely -
  # measured 2026-08-22, it missed all 7 "Unstamped marker-only resource"
  # errors and several of the 7 "Unmarked apply of a marker-only resource"
  # ones that stood behind #354 and #369 once both were fixed. Every error
  # block, summary plus detail, in order, is what a reader needs instead.
  ERROR_SUMMARIES="$(grep -oE '^Error: .+' <<< "$PLAN_OUT" | sort | uniq -c)"
  printf '%s\n' "$ERROR_SUMMARIES"
  printf '%s\n' "$PLAN_OUT" | grep -A6 -E '^Error:'
  fail "live-plan exited $PLAN_RC: $(printf '%s' "$ERROR_SUMMARIES" | tr '\n' '; ')"
fi
[ ! -f "$ADOPTED/terraform.tfstate" ] || fail "live-plan wrote a state file"
# "must be replaced" is Terraform's own summary line for a forced
# create_before_destroy=false replacement, and it carries no "will be" at
# all - a plan-emptiness check that only matched "will be
# (created|updated|destroyed)" let a replacement through undetected for as
# long as something else in the plan already failed this same check, which
# is exactly what happened here until 2026-08-22 (a real replacement was
# masked by unrelated "will be created" noise the whole time). Both verbs
# now covered so the loudest hole in this check cannot silently reopen.
grep -qE '^  # .+ (will be (created|updated|destroyed)|must be replaced)' <<< "$PLAN_OUT" \
  && { grep -E '^  # .+ (will be|must be replaced)' <<< "$PLAN_OUT"; fail "the plan proposes a resource change with no local record store and no drift"; }
log "  no resource change proposed, with zero local memory of the migration that stamped it"

# Re-assert the two marker-carrying identities, after the local state file
# was deleted - so any answer below can only have come from the marker on
# the live object itself, same discipline corpus-vpc-complete's stage 3
# uses. The ASG is not re-read here because it carries no marker to re-read
# (see stage 2); the empty plan just above is what proves choudoufu still
# knows which ASG is which without one.
LT_ADDR2="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$LT_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
[ "$LT_ADDR2" = "$LT_ADDR" ] || fail "the launch template's tofu-address changed across the empty plan: $LT_ADDR -> $LT_ADDR2"
ROLE_TAG_VALUE2="$(awsl iam list-role-tags --role-name complete --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$ROLE_TAG_VALUE2" = "$ROLE_TAG_VALUE" ] || fail "the IAM role's tofu-address changed across the empty plan: $ROLE_TAG_VALUE -> $ROLE_TAG_VALUE2"
log "  identity re-check: both marked objects still carry the same tofu-address after the state file was deleted (re-read via the AWS CLI): $LT_ADDR2, $ROLE_TAG_VALUE2"
gauntlet_stage test_plan pass "empty plan; identity re-check unchanged: $LT_ADDR2, $ROLE_TAG_VALUE2"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 4: TEST APPLY - apply the empty plan, assert a genuine no-op
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=test_apply
log "=== STAGE 4: test apply (apply the empty plan; object count unchanged) ==="
BEFORE_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"

APPLY2_OUT="$(cd "$ADOPTED" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; APPLY2_RC=$?
[ "$APPLY2_RC" -eq 0 ] || { printf '%s\n' "$APPLY2_OUT" | tail -50; fail "the post-migration apply failed"; }
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
QUEUE_URL="$(awsl sqs get-queue-url --queue-name complete --query QueueUrl --output text 2>/dev/null)"
[ -n "$QUEUE_URL" ] && [ "$QUEUE_URL" != "None" ] || fail "no live SQS queue found named 'complete'"

if [ "${BREAK:-}" = "1" ]; then
  # A second, unrelated object is mutated too - the assertion below must
  # catch this as MORE than one object proposed, not silently pass.
  awsl ec2 create-tags --resources "$LT_ID" --tags Key=Example,Value=tampered-by-BREAK >/dev/null
  log "  BREAK=1: also tampered launch template $LT_ID's Example tag - stage 5 must now see TWO drifted objects and fail the single-object assertion"
fi

awsl sqs tag-queue --queue-url "$QUEUE_URL" --tags Example=tampered-out-of-band >/dev/null
DRIFTED_VALUE="$(awsl sqs list-queue-tags --queue-url "$QUEUE_URL" --query "Tags.Example" --output text)"
[ "$DRIFTED_VALUE" = "tampered-out-of-band" ] || fail "the out-of-band tag mutation did not take"
log "  mutated SQS queue 'complete's Example tag to \"tampered-out-of-band\" directly via the AWS CLI"

DRIFT_PLAN_OUT="$(plan_into 2>&1)"; DRIFT_PLAN_RC=$?
[ "$DRIFT_PLAN_RC" -eq 0 ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | tail -80; fail "the drift-detection plan exited $DRIFT_PLAN_RC"; }

CHANGED_ADDRS="$(grep -oE '^  # \S+ will be updated' <<< "$DRIFT_PLAN_OUT" | awk '{print $2}' | sort -u)"
N_CHANGED="$(printf '%s\n' "$CHANGED_ADDRS" | grep -c . || true)"
if [ "${BREAK:-}" = "1" ]; then
  [ "$N_CHANGED" = "1" ] && fail "BREAK=1 set (two objects tampered), but the plan proposes fixing only 1 - this assertion is not load-bearing"
  log "  BREAK=1: the plan proposes fixing $N_CHANGED objects, correctly more than one - the single-object assertion below is skipped"
else
  [ "$N_CHANGED" = "1" ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | grep -E '^  # .+ will be' ; fail "expected exactly 1 object proposed for a fix, got $N_CHANGED"; }
  # The plan's own diff header spells an index in BRACKETS, so the bracket
  # form is the one compared here - not the colon form the tag carries.
  printf '%s\n' "$CHANGED_ADDRS" | grep -qF "$LT_ADDR_PLAN" && fail "the plan proposes changing $LT_ADDR_PLAN, which was never touched"
  log "  the plan proposes fixing exactly one object: $(printf '%s' "$CHANGED_ADDRS")"

  RECONVERGE_APPLY="$(cd "$ADOPTED" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; RECONVERGE_RC=$?
  [ "$RECONVERGE_RC" -eq 0 ] || { printf '%s\n' "$RECONVERGE_APPLY" | tail -50; fail "the reconverge apply failed"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$RECONVERGE_APPLY" \
    || { grep -E 'Apply complete' <<< "$RECONVERGE_APPLY"; fail "the reconverge apply did not change exactly 1 resource"; }
  FIXED_VALUE="$(awsl sqs list-queue-tags --queue-url "$QUEUE_URL" --query "Tags.Example" --output text)"
  [ "$FIXED_VALUE" = "complete" ] || fail "the SQS queue's Example tag is \"$FIXED_VALUE\" after reconverging, not \"complete\""
  log "  reconverged: SQS queue 'complete's Example tag is back to \"complete\""
  gauntlet_stage drift_reconverge pass "one object tampered (SQS queue 'complete's Example tag), plan proposed fixing exactly one object, apply changed 1 and reconverged the tag"
fi

CURRENT_STAGE=day2_rename
log "=== D0. capture the live ids a rename must not disturb ==="
SQS_URL="$(awsl sqs get-queue-url --queue-name "$(cd "$ADOPTED" && "$TOFU" output -raw 2>/dev/null || true)" 2>/dev/null || true)"
# The exact escaped form of the marker (":0" vs no index at all) depends on
# how the external security-group module's own count/for_each resolves, so
# this scans every security group this estate marked and matches by prefix
# in bash rather than guessing the address's exact suffix in a server-side
# filter.
ASG_SG_ALL="$(awsl ec2 describe-security-groups \
  --filters "Name=tag:tofu-estate,Values=$ESTATE" \
  --query "SecurityGroups[].[GroupId,Tags[?Key=='tofu-address']|[0].Value]" --output text)"
ASG_SG_LINE="$(grep -E '	module\.asg_sg\.' <<< "$ASG_SG_ALL" | head -1)"
[ -n "$ASG_SG_LINE" ] || { printf '%s\n' "$ASG_SG_ALL"; fail "no live asg_sg security group found by its tofu-address marker"; }
ASG_SG_ID="$(awk -F'\t' '{print $1}' <<< "$ASG_SG_LINE")"
ASG_SG_ADDR_BEFORE="$(awk -F'\t' '{print $2}' <<< "$ASG_SG_LINE")"
SQS_ARN="$(awsl sqs list-queues --query "QueueUrls[0]" --output text)"
[ -n "$SQS_ARN" ] && [ "$SQS_ARN" != "None" ] || fail "no live sqs queue found"
log "  $ASG_SG_ID (module.asg_sg), $SQS_ARN (aws_sqs_queue.this)"

if [ "${BREAK:-}" = "1" ]; then
  log "=== D1 (BREAK=1). rename aws_sqs_queue.this -> .this_renamed WITHOUT a moved block ==="
  sed -i.bak 's/resource "aws_sqs_queue" "this" {/resource "aws_sqs_queue" "this_renamed" {/' "$ADOPTED/main.tf"
  sed -i.bak 's/aws_sqs_queue\.this\.name/aws_sqs_queue.this_renamed.name/' "$ADOPTED/main.tf"
  rm -f "$ADOPTED/main.tf.bak"
  ( cd "$ADOPTED" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ADOPTED" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the BREAK=1 rename's reinit failed"; }
  BREAK_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; BREAK_PLAN_RC=$?
  [ "$BREAK_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -30; fail "the BREAK=1 rename-without-moved plan exited $BREAK_PLAN_RC"; }
  grep -qE '^  # aws_sqs_queue\.this will be destroyed' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=1: renaming without a moved block did not propose destroying aws_sqs_queue.this - this stage's check is not load-bearing"; }
  grep -qE '^  # aws_sqs_queue\.this_renamed will be created' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=1: renaming without a moved block did not propose creating aws_sqs_queue.this_renamed - this stage's check is not load-bearing"; }
  log "  BREAK=1: correctly proposes destroying aws_sqs_queue.this and creating aws_sqs_queue.this_renamed - the moved-block and live-mv checks below are skipped"
else
  log "=== D1. choudoufu, moved block: module.asg_sg -> module.asg_sg_renamed ==="
  sed -i.bak 's/module "asg_sg" {/module "asg_sg_renamed" {/' "$ADOPTED/main.tf"
  sed -i.bak 's/module\.asg_sg\./module.asg_sg_renamed./g' "$ADOPTED/main.tf"
  rm -f "$ADOPTED/main.tf.bak"
  cat >> "$ADOPTED/main.tf" <<'EOF'

moved {
  from = module.asg_sg
  to   = module.asg_sg_renamed
}
EOF
  ( cd "$ADOPTED" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ADOPTED" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the moved-block rename's reinit failed"; }
  MOVED_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; MOVED_PLAN_RC=$?
  [ "$MOVED_PLAN_RC" -eq 0 ] || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -40; fail "the moved-block rename plan exited $MOVED_PLAN_RC"; }
  grep -qE '^  # .+ will be (destroyed|created)' <<< "$MOVED_PLAN_OUT" \
    && { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu defect: the moved-block rename of module.asg_sg proposes a create for its untaggable child aws_security_group_rule.egress_rules[0] instead of matching it structurally under the parent's new address - not zero churn. The parent aws_security_group.this_name_prefix[0] itself IS relocated correctly ('will be updated in-place'); stock's native moved-block handling relocates the rule cleanly too (confirmed by the oracle above, zero churn). The gap is choudoufu-specific and already characterized: an untaggable/derived child's identity resolution does not follow a moved parent module the way a marker-carrying resource's does. Not fixed in this unit, scope is the day2_rename stage activation itself (see corpus-vpc-complete's own day2_rename detail for the first occurrence of this class of wall)."; }
  N_CHANGED_D1="$(grep -cE '^  # .+ will be updated in-place' <<< "$MOVED_PLAN_OUT" || true)"
  [ "$N_CHANGED_D1" -ge 1 ] || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -20; fail "the moved-block rename plan proposes no in-place changes at all - nothing to rewrite the markers"; }
  grep -qF "Plan: 0 to add, $N_CHANGED_D1 to change, 0 to destroy." <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -10; fail "the moved-block rename plan's summary does not match its own $N_CHANGED_D1 in-place changes"; }
  ASG_SG_ADDR_AFTER_RENAME="${ASG_SG_ADDR_BEFORE/module.asg_sg./module.asg_sg_renamed.}"
  grep -qE "~ +\"tofu-address\" = \"${ASG_SG_ADDR_BEFORE//./\\.}\" -> \"${ASG_SG_ADDR_AFTER_RENAME//./\\.}\"" <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT"; fail "the moved-block plan does not show the security group's tofu-address marker being rewritten from the old address to the new one"; }
  log "  choudoufu: zero churn, $N_CHANGED_D1 in-place tags update(s) - the marker rewrite the moved block completes"

  MOVED_APPLY_OUT="$(cd "$ADOPTED" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; MOVED_APPLY_RC=$?
  [ "$MOVED_APPLY_RC" -eq 0 ] || { printf '%s\n' "$MOVED_APPLY_OUT" | tail -40; fail "the moved-block rename apply exited $MOVED_APPLY_RC"; }
  grep -qE "Resources: 0 added, $N_CHANGED_D1 changed, 0 destroyed" <<< "$MOVED_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$MOVED_APPLY_OUT"; fail "the moved-block rename apply did not change exactly $N_CHANGED_D1 resources"; }

  ASG_SG_ID_AFTER="$(awsl ec2 describe-security-groups --group-ids "$ASG_SG_ID" --query "SecurityGroups[0].GroupId" --output text 2>/dev/null || true)"
  [ "$ASG_SG_ID_AFTER" = "$ASG_SG_ID" ] || fail "the asg_sg security group's id changed across the rename ($ASG_SG_ID -> $ASG_SG_ID_AFTER) - it was destroyed and recreated, not renamed"
  ASG_SG_ADDR_AFTER="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$ASG_SG_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
  [ "$ASG_SG_ADDR_AFTER" = "$ASG_SG_ADDR_AFTER_RENAME" ] \
    || fail "the asg_sg security group carries tofu-address=$ASG_SG_ADDR_AFTER after the rename, not $ASG_SG_ADDR_AFTER_RENAME"
  log "  $ASG_SG_ID unchanged, tofu-address now $ASG_SG_ADDR_AFTER_RENAME - read via the AWS CLI"

  log "=== D2. choudoufu, live-mv: aws_sqs_queue.this -> .this_renamed, no moved block at all ==="
  sed -i.bak 's/resource "aws_sqs_queue" "this" {/resource "aws_sqs_queue" "this_renamed" {/' "$ADOPTED/main.tf"
  sed -i.bak 's/aws_sqs_queue\.this\.name/aws_sqs_queue.this_renamed.name/' "$ADOPTED/main.tf"
  rm -f "$ADOPTED/main.tf.bak"
  ( cd "$ADOPTED" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ADOPTED" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the live-mv rename's reinit failed"; }
  MV_OUT="$(cd "$ADOPTED" && "$TOFU" live-mv -estate="$ESTATE" aws_sqs_queue.this aws_sqs_queue.this_renamed 2>&1)"; MV_RC=$?
  [ "$MV_RC" -eq 0 ] || { printf '%s\n' "$MV_OUT" | tail -30; fail "choudoufu live-mv exited $MV_RC"; }
  grep -qF 'Rewrote the ownership marker on one live resource. This was a cloud write.' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report a real write"; }
  grep -qF '"aws_sqs_queue.this" -> "aws_sqs_queue.this_renamed"' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report rewriting the tofu-address marker from the old address to the new one"; }
  log "  live-mv: $(grep -F 'live ID' <<< "$MV_OUT")"

  SQS_URL_AFTER="$(awsl sqs list-queues --query "QueueUrls[0]" --output text)"
  [ "$SQS_URL_AFTER" = "$SQS_ARN" ] || fail "the sqs queue's url changed across live-mv ($SQS_ARN -> $SQS_URL_AFTER) - it was destroyed and recreated, not renamed"
  SQS_ADDR_AFTER="$(awsl sqs list-queue-tags --queue-url "$SQS_ARN" --query "Tags.\"tofu-address\"" --output text)"
  [ "$SQS_ADDR_AFTER" = "aws_sqs_queue.this_renamed" ] || fail "the sqs queue carries tofu-address=$SQS_ADDR_AFTER after live-mv, not aws_sqs_queue.this_renamed"
  log "  $SQS_ARN unchanged, tofu-address now aws_sqs_queue.this_renamed - read via the AWS CLI"

  log "=== D3. one more plan: config and markers agree on both renames, nothing proposed ==="
  FINAL_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; FINAL_PLAN_RC=$?
  [ "$FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$FINAL_PLAN_OUT" | tail -40; fail "the post-rename plan exited $FINAL_PLAN_RC"; }
  grep -qF "No changes. Your infrastructure matches the configuration." <<< "$FINAL_PLAN_OUT" \
    || { grep -E '^  #' <<< "$FINAL_PLAN_OUT"; fail "the post-rename plan is not empty"; }
  log "  No changes. Both renames are complete and invisible to the next plan."

  gauntlet_stage day2_rename pass "moved block: module.asg_sg renamed with zero churn (0 add, $N_CHANGED_D1 change, 0 destroy), marker rewritten in place on its security group; live-mv: aws_sqs_queue.this renamed with zero churn, marker rewritten in place; stock oracle over the same two-object rename on cold_deploy's own state also shows zero churn (0 add, 0 change, 0 destroy); both live ids unchanged, read via the AWS CLI"
fi
CURRENT_STAGE=""

CURRENT_STAGE=""
gauntlet_end

log ""
log "=== PASS ==="
log ""
log "terraform-aws-modules/terraform-aws-autoscaling's flagship \"complete\""
log "example, crossed through all five stages: cold deploy with plain"
log "terraform, choudoufu live-import adoption, an empty replan with the"
log "state file deleted and three rendered identities checked against the"
log "AWS CLI's own answer, a genuine no-op apply, and drift on one object"
log "reconverging without touching any other."
