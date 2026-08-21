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
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }

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

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE - choudoufu live-import against the plain state file
# ══════════════════════════════════════════════════════════════════════════
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
grep -qE '[0-9]+ resource\(s\) newly stamped, 0 already stamped, 0 newly recorded, 0 already recorded, 0 failed, [0-9]+ skipped' <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT" | tail -40; fail "live-import -approve did not stamp cleanly"; }
log "  $(grep -oE '[0-9]+ resource\(s\) newly stamped, 0 already stamped, 0 newly recorded, 0 already recorded, 0 failed, [0-9]+ skipped' <<< "$APPROVE_OUT")"

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

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - state deleted, live-plan, empty + identities re-asserted
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 3: test plan (state deleted, live-plan empty) ==="
rm -f "$ADOPTED/terraform.tfstate" "$ADOPTED/terraform.tfstate.backup"
[ ! -f "$ADOPTED/terraform.tfstate" ] || fail "the state file is still there"

plan_into() { ( cd "$ADOPTED" && "$TOFU" live-plan -input=false -no-color ); }
PLAN_OUT="$(plan_into 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -80; fail "live-plan exited $PLAN_RC"; }
[ ! -f "$ADOPTED/terraform.tfstate" ] || fail "live-plan wrote a state file"
grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$PLAN_OUT" \
  && { grep -E '^  # .+ will be' <<< "$PLAN_OUT"; fail "the plan proposes a resource change with no local record store and no drift"; }
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

# ══════════════════════════════════════════════════════════════════════════
# STAGE 4: TEST APPLY - apply the empty plan, assert a genuine no-op
# ══════════════════════════════════════════════════════════════════════════
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

# ══════════════════════════════════════════════════════════════════════════
# STAGE 5: DRIFT AND RECONVERGE - mutate one object, replan, assert one fix
# ══════════════════════════════════════════════════════════════════════════
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
fi

log ""
log "=== PASS ==="
log ""
log "terraform-aws-modules/terraform-aws-autoscaling's flagship \"complete\""
log "example, crossed through all five stages: cold deploy with plain"
log "terraform, choudoufu live-import adoption, an empty replan with the"
log "state file deleted and three rendered identities checked against the"
log "AWS CLI's own answer, a genuine no-op apply, and drift on one object"
log "reconverging without touching any other."
