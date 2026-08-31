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
#   BREAK_COUNT   set to 1 to run PART G's own Break control instead of
#                 its real checks: expect the WRONG count instance
#                 (count_test[0] rather than count_test[1]) to be the one
#                 destroyed on the way down, which must make day2_count
#                 report fail. Independent of BREAK and BREAK=replace.
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

# PART GREENFIELD (live/GAUNTLET.md #13) needs one MORE floci container, a
# fresh namespace choudoufu applies into directly. Its own oracle reuses
# $ENDPOINT/$PLAIN: STAGE 1's plain terraform apply already IS "stock's
# cold deploy" against this exact estate, still genuinely unmarked at the
# point PART GREENFIELD runs (right after STAGE 1, before STAGE 2's
# live-import ever tags anything in $ENDPOINT) - so no third container is
# needed the way corpus-sqs-basic's own greenfield uses one; that estate's
# greenfield runs interleaved with an already-migrated STAGE 1 object.
FLOCI_GREEN_PORT="${FLOCI_GREEN_PORT:-$((FLOCI_PORT + 400))}"
FLOCI_GREEN_NAME="choudoufu-corpus-autoscaling-complete-green-$$"
GREEN_ENDPOINT="http://127.0.0.1:${FLOCI_GREEN_PORT}"
GREEN_ESTATE="autoscaling-complete-greenfield"
GREEN="$WORK/green/autoscaling/examples/complete"

ESTATE="autoscaling-complete-crossing"
REGION="eu-west-1"
TF_COLD_BIN="${TF_COLD_BIN:-terraform}"

cleanup() {
  docker rm -f "$FLOCI_NAME" "$FLOCI_GREEN_NAME" >/dev/null 2>&1 || true
  if [ -z "${GAUNTLET_KEEP_WORK:-}" ]; then
    rm -rf "$WORK"
  else
    echo "GAUNTLET_KEEP_WORK set: leaving $WORK in place" >&2
  fi
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
gauntlet_begin_stage cold_deploy
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

gauntlet_begin_stage day2_rename
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

# day2_remove's stock oracle (live/GAUNTLET.md #7, active): "Stock with the
# same block removed plans the same destroys." A SEPARATE copy of
# cold_deploy's own state, so this destroy has nothing to do with either
# rename this script also exercises. module.default is the plainest of the
# twelve module calls (the header's "default ASG shape") and self-contained:
# it consumes module.vpc's private_subnets and data.aws_ami, but nothing
# else in main.tf reads module.default's own outputs - only outputs.tf does
# (25 blocks, stripped by live/e2e/lib/strip-output-blocks.py rather than
# truncating the whole file, since the other eleven modules' outputs are
# still worth keeping in this oracle's plan). The block is not renamed by
# either mechanism Part D exercises, so its name is "default" on both the
# stock copy here and, later, on the real $ADOPTED tree.
gauntlet_begin_stage day2_remove
log "=== D-REMOVE-ORACLE. stock: delete module.default's block on cold_deploy's own state ==="
REMOVE_ORACLE_ROOT="$WORK/plain-remove-oracle"
cp -r "$WORK/plain" "$REMOVE_ORACLE_ROOT"
REMOVE_ORACLE="$REMOVE_ORACLE_ROOT/autoscaling/examples/complete"
perl -0777pi -e 's/module "default" \{.*?\n\}\n\n/\n/s' "$REMOVE_ORACLE/main.tf"
grep -q 'module "default" {' "$REMOVE_ORACLE/main.tf" && fail "removing module.default's block from the day2_remove oracle copy did not match - the corpus example has moved"
python3 "$ROOT/live/e2e/lib/strip-output-blocks.py" "$REMOVE_ORACLE/outputs.tf" "module.default." || fail "stripping module.default's outputs from the oracle copy failed"
( cd "$REMOVE_ORACLE" && "$TF_COLD_BIN" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$REMOVE_ORACLE" && "$TF_COLD_BIN" init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_remove stock oracle's reinit failed"; }
REMOVE_ORACLE_PLAN_OUT="$(cd "$REMOVE_ORACLE" && "$TF_COLD_BIN" plan -input=false -no-color 2>&1)"; REMOVE_ORACLE_PLAN_RC=$?
[ "$REMOVE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_remove stock oracle plan exited $REMOVE_ORACLE_PLAN_RC"; }
ORACLE_REMOVE_N="$(grep -cE '^  # module\.default\..+ will be destroyed' <<< "$REMOVE_ORACLE_PLAN_OUT" || true)"
[ "$ORACLE_REMOVE_N" -ge 1 ] || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "stock proposes no destroy at all for module.default when its block is removed"; }
grep -qF "Plan: 0 to add, 0 to change, $ORACLE_REMOVE_N to destroy." <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -10; fail "stock's remove plan touches something other than module.default's own $ORACLE_REMOVE_N resources"; }
log "  stock: exactly $ORACLE_REMOVE_N destroys, all under module.default, nothing else, on the state cold_deploy produced"
gauntlet_end_stage

# day2_replace's stock oracle (live/GAUNTLET.md #9, active): "Stock's
# replace of the same resource leaves the same single object." A THIRD
# separate copy of cold_deploy's own state, so this destroy has nothing to
# do with either rename or the remove Part D/E also exercise. Changes
# module.asg_sg's own `name` CALL argument (not vendored module-internal
# source - always in scope to edit) to a different literal, which the
# terraform-aws-modules/security-group/aws module passes straight through
# to its own aws_security_group.this_name_prefix's `name_prefix` argument
# - a real, upstream-declared ForceNew argument on aws_security_group (the
# EC2 API has no rename call for a security group, only Create/Delete) -
# forcing stock to replace the SAME declared address. module.asg_sg's own
# security_group_id feeds two launch templates' `security_groups`
# argument (main.tf's module.default/module.complete blocks); aws_launch_
# template supports updating that argument in place (a new template
# version, not ForceNew on the resource itself), so this is expected to
# cascade into in-place updates there, not further replaces - read
# dynamically below rather than asserted by fixed count.
#
# NOTE ON THE TARGET CHOICE: this section originally targeted aws_sqs_
# queue.this_renamed (Part D's live-mv leg), which reproduced a genuine,
# separate finding first - recorded here rather than routed around
# silently: after "choudoufu live-mv" renames a BARE, non-module-nested
# resource (no module boundary crossed) with no ordinary apply run
# afterward, the live MARKER is correctly rewritten (day2_rename's own
# Proves text, unaffected) but the LOCAL RECORD is left stale at the OLD
# key. Root cause, read directly off mv.go with no tofu in the loop:
# internal/live/mv/mv.go's propagateModuleRename (called from Move after
# the marker rewrite) opens with `oldPrefix, newPrefix, ok :=
# moduleRenameBoundary(...); if !ok { return diags }` - for a same-module,
# bare-resource rename this check is never satisfied, so the function
# returns immediately and NEVER reaches the MoveRecord call the same
# function's own doc comment says covers "the resource live-mv was asked
# to rename itself". Confirmed empirically: `cat`-ing the record store
# directly (no tofu in the loop) after Part D's real live-mv found the
# record still filed under aws_sqs_queue/<base64 of "aws_sqs_queue.this">,
# never re-keyed to .this_renamed. dynamodb-table-basic's own Part F
# (module.dynamodb_table_final) and alb-complete's own Part F (aws_
# instance.this_renamed) both dodge this by construction: the former's
# LAST rename hop is itself a MODULE-boundary live-mv (propagateModule
# Rename's guard passes), and the latter's rename is a moved-block
# followed by a real converging apply (MOVED_APPLY_OUT), which writes a
# fresh record under the current address as ordinary apply WriteBack -
# neither depends on this function at all. Row 2 of HANDOFF's five-row
# table (choudoufu's own record store and the live marker disagree after
# a plain live-mv rename with no module boundary) - a real gap, FIXED on
# this branch, GitHub issue #412 (gauntlet/mv-rekey): mv.go's
# propagateModuleRename now calls store.MoveRecord(ctx, m.req.Old, m.req.
# New) unconditionally, before the moduleRenameBoundary guard, so a
# same-module bare-resource rename re-keys its own record instead of
# leaving the store pointing at a dead address. eks-basic's and ecs-
# fargate's own day2_replace sections in this same unit independently hit
# the identical shape on aws_security_group.all_worker_mgmt_renamed and
# aws_service_discovery_http_namespace.this_renamed respectively; their
# scripts were not re-run for #412 (out of scope for this unit), so their
# own header comments and detail strings still read pre-fix until their
# next real run. This section still targets module.asg_sg_renamed's own
# security group rather than aws_sqs_queue.this_renamed, unchanged by
# #412 - which Part D1 renames through a moved block
# FOLLOWED BY a real converging apply (MOVED_APPLY_OUT, this script's own
# D1 above) - the same apply-refreshes-the-record shape alb-complete's
# Part F already relies on - so this section exercises the stage
# honestly without depending on the now-fixed gap above.
gauntlet_begin_stage day2_replace
log "=== F-ORACLE. stock: force-replace module.asg_sg's security group via its ForceNew name_prefix argument, on cold_deploy's own state ==="
REPLACE_ORACLE_ROOT="$WORK/plain-replace-oracle"
cp -r "$WORK/plain" "$REPLACE_ORACLE_ROOT"
REPLACE_ORACLE="$REPLACE_ORACLE_ROOT/autoscaling/examples/complete"
perl -0pi -e 's/module "asg_sg" \{\n  source  = "terraform-aws-modules\/security-group\/aws"\n  version = "~> 5\.0"\n\n  name        = local\.name\n/module "asg_sg" {\n  source  = "terraform-aws-modules\/security-group\/aws"\n  version = "~> 5.0"\n\n  name        = "\${local.name}-v2"\n/' "$REPLACE_ORACLE/main.tf"
grep -q '${local.name}-v2' "$REPLACE_ORACLE/main.tf" \
  || fail "changing module.asg_sg's name argument in the replace-oracle copy did not match - the corpus pin has moved"
( cd "$REPLACE_ORACLE" && "$TF_COLD_BIN" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$REPLACE_ORACLE" && "$TF_COLD_BIN" init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_replace stock oracle's reinit failed"; }
REPLACE_ORACLE_PLAN_OUT="$(cd "$REPLACE_ORACLE" && "$TF_COLD_BIN" plan -input=false -no-color 2>&1)"; REPLACE_ORACLE_PLAN_RC=$?
[ "$REPLACE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_replace stock oracle plan exited $REPLACE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.asg_sg\.aws_security_group\.this_name_prefix\[0\] must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "stock does not propose replacing module.asg_sg's security group when its name_prefix argument changes"; }
# The plan summary line is read dynamically rather than asserted as a
# fixed count: this twelve-module example carries several ASG
# enabled_metrics list attributes whose values floci can report with real
# API-timing variance across separate plans against the same live account
# (a computed, server-side list, unrelated to this section's own change),
# and the security group's id cascades into two launch templates'
# security_groups argument (in-place, new template version) - so more
# than one line can legitimately appear alongside the group's own replace.
REPLACE_ORACLE_PLAN_LINE="$(grep -oE 'Plan: [0-9]+ to add, [0-9]+ to change, [0-9]+ to destroy\.' <<< "$REPLACE_ORACLE_PLAN_OUT")"
[ -n "$REPLACE_ORACLE_PLAN_LINE" ] || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -15; fail "the day2_replace stock oracle plan has no summary line"; }
log "  stock: $REPLACE_ORACLE_PLAN_LINE - replaces module.asg_sg's security group at the same declared address, on the state cold_deploy produced - plan only, not applied (this copy shares floci's account with \$ADOPTED, and actually applying here would destroy the real security group the estate's later stages still depend on)"
gauntlet_end_stage

gauntlet_begin_stage migrate

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE - choudoufu live-import against the plain state file
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage migrate
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
gauntlet_begin_stage test_plan
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
gauntlet_begin_stage test_apply
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
gauntlet_begin_stage drift_reconverge
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

gauntlet_begin_stage day2_rename
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
  # RE-VERIFIED against current main (re-verify-day2_remove unit, 2026-08):
  # root cause is now precisely named, not just "already characterized":
  # 610511fb73 (internal/live/discovery/recordorphan_read.go, #405's
  # day2_remove fix) added recordOrphanReadSweep, which reads the record
  # store for any UNTAGGABLE type's undeclared old-address record and
  # proposes destroying/recreating it - generically, since its filter is
  # "untaggable + has a persisted identity record", not tied to any
  # specific type. Its own rename-safety check (the `pending` map, built
  # from res.Unbound) only recognizes "a declared instance of the SAME
  # address is unclaimed" - it never consults
  # moved.Aliases/moved.Honoured(req.Config) the way the marker path
  # already does. SAME root cause, independently confirmed on
  # corpus-giantswarm-crossplane, corpus-ec2-instance-complete,
  # corpus-rds-complete-postgres, corpus-security-group-complete and
  # corpus-dynamodb-table-basic in this same unit - a generic gap now
  # reaching at least six estates. live-mv does not hit this
  # (RecordStore.MoveRecord re-keys the store directly, 8bd0d47e4e); only
  # a bare HCL `moved` block does. Not fixed here - a Go change, out of
  # scope for this script-only re-verification unit. Because fail() exits
  # immediately, day2_remove's own post-fix status for this estate could
  # not be independently re-measured this run.
  grep -qE '^  # .+ will be (destroyed|created)' <<< "$MOVED_PLAN_OUT" \
    && { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu defect: the moved-block rename of module.asg_sg proposes a create for its untaggable child aws_security_group_rule.egress_rules[0] instead of matching it structurally under the parent's new address - not zero churn. Root cause: 610511fb73's recordOrphanReadSweep has no moved-block awareness (see the comment immediately above this assertion) - the SAME generic gap corpus-giantswarm-crossplane, corpus-ec2-instance-complete, corpus-rds-complete-postgres, corpus-security-group-complete and corpus-dynamodb-table-basic independently hit in this same unit. day2_remove's own post-fix status for this estate could not be re-measured this run because of it."; }
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

  # ══════════════════════════════════════════════════════════════════════
  # PART F: REPLACE (day2_replace, active - live/GAUNTLET.md #9)
  # ══════════════════════════════════════════════════════════════════════
  #
  # Starts from Part D's real, completed state: module.asg_sg_renamed's
  # own security group (module.asg_sg_renamed.aws_security_group.this_
  # name_prefix[0]) is bound and converged. Its module CALL's `name`
  # argument (main.tf's own module.asg_sg_renamed block, not vendored
  # module-internal source) changes to a new literal, which the
  # terraform-aws-modules/security-group/aws module passes through to its
  # own aws_security_group.this_name_prefix's `name_prefix` argument - a
  # real, upstream-declared ForceNew argument on aws_security_group (the
  # EC2 API has no rename call, only Create/Delete) - forcing a
  # replacement at the SAME declared address. The security group's id
  # feeds two launch templates' `security_groups` argument, which aws_
  # launch_template updates in place (a new template version, F-ORACLE's
  # own header comment) - so this is expected to cascade into in-place
  # updates there, not a second replace, read dynamically below.
  #
  # THE TARGET CHOICE, and the finding that produced it: F-ORACLE's own
  # header comment above records a genuine, separate defect this section
  # originally reproduced on aws_sqs_queue.this_renamed (Part D's live-mv
  # leg) - a bare, non-module-nested live-mv rename with no apply
  # afterward left the LOCAL RECORD stale at the old key even though the
  # live MARKER moved correctly (internal/live/mv/mv.go's
  # propagateModuleRename never reached its own MoveRecord call for a
  # same-module rename). FIXED on this branch, GitHub issue #412
  # (gauntlet/mv-rekey) - see F-ORACLE's own header comment above for the
  # fix and for eks-basic's/ecs-fargate's own day2_replace sections in
  # this same unit, which independently hit the identical shape and were
  # not re-run for #412. This section still targets module.asg_sg_
  # renamed's security group rather than aws_sqs_queue.this_renamed,
  # unchanged by #412 - Part D1 renames it through a moved block FOLLOWED BY
  # a real converging apply (MOVED_APPLY_OUT, above) - the apply-
  # refreshes-the-record shape alb-complete's own Part F already relies
  # on - so the stage is exercised honestly without depending on the
  # now-fixed gap.
  #
  # THE create_before_destroy SCOPE NOTE (same shape as corpus-ec2-
  # instance-complete's and corpus-sqs-basic's own Part F): the security
  # group lives inside a vendored registry module
  # (terraform-aws-modules/security-group/aws), whose own source this
  # corpus's established convention never patches to add a library-
  # internal lifecycle block. This evidence pass exercises OpenTofu's
  # DEFAULT replace ordering instead. BREAK=replace manufactures the
  # coexistence a skipped destroy would leave behind directly via the AWS
  # CLI.
  gauntlet_begin_stage day2_replace
  record_key() { printf '%s' "$1" | base64 | tr '+/' '-_' | tr -d '=\n'; }
  record_import_id() { jq -r '.identity.import_id' "$1"; }
  F_ADDR='module.asg_sg_renamed.aws_security_group.this_name_prefix[0]'
  F_RECORD="$ADOPTED/.tofu-records/tofu-records/$ESTATE/aws_security_group/$(record_key "$F_ADDR")"

  log "=== F0. capture the live security group and its record ahead of the forced replace ==="
  [ -f "$F_RECORD" ] || fail "no local record file found for $F_ADDR ahead of day2_replace"
  F_OLD_IMPORT_ID="$(record_import_id "$F_RECORD")"
  [ "$F_OLD_IMPORT_ID" = "$ASG_SG_ID" ] || fail "the record for $F_ADDR names $F_OLD_IMPORT_ID ahead of day2_replace, not $ASG_SG_ID"
  F_OLD_ADDR_TAG="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$ASG_SG_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
  [ "$F_OLD_ADDR_TAG" = "module.asg_sg_renamed.aws_security_group.this_name_prefix:0" ] \
    || fail "$ASG_SG_ID does not carry tofu-address=module.asg_sg_renamed.aws_security_group.this_name_prefix:0 ahead of day2_replace"
  log "  $ASG_SG_ID, record import_id=$F_OLD_IMPORT_ID, tofu-address=$F_OLD_ADDR_TAG"

  if [ "${BREAK:-}" = "replace" ]; then
    log "=== F1 (BREAK=replace). manufacture the coexistence a skipped destroy would leave behind ==="
    # A second, distinct live security group carrying the SAME tofu-
    # address and tofu-slot as the one a genuine replace would destroy -
    # the state "skip the destroy half" of a create-before-destroy
    # replace would leave, produced directly via the AWS CLI rather than
    # by actually interrupting an apply (day2_crash's own job).
    SG_VPC_ID="$(awsl ec2 describe-security-groups --group-ids "$ASG_SG_ID" --query "SecurityGroups[0].VpcId" --output text)"
    BREAK_COLLISION_ID="$(awsl ec2 create-security-group --group-name "${ESTATE}-sg-collision" --description "collision" --vpc-id "$SG_VPC_ID" --query "GroupId" --output text)"
    awsl ec2 create-tags --resources "$BREAK_COLLISION_ID" --tags "Key=tofu-estate,Value=$ESTATE" "Key=tofu-address,Value=module.asg_sg_renamed.aws_security_group.this_name_prefix:0" "Key=tofu-slot,Value=0" \
      >/dev/null || fail "BREAK=replace: could not tag the collision security group"
    BREAK_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; BREAK_PLAN_RC=$?
    awsl ec2 delete-security-group --group-id "$BREAK_COLLISION_ID" >/dev/null 2>&1 || true
    [ "$BREAK_PLAN_RC" -ne 0 ] \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -20; fail "BREAK=replace: the plan succeeded with two live objects claiming the same tofu-address/tofu-slot - it must report the collision, not propose nothing"; }
    grep -qF 'Two live resources claiming one slot' <<< "$BREAK_PLAN_OUT" \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -20; fail "BREAK=replace: the plan failed for a reason other than the slot collision - this stage's check is not load-bearing"; }
    log "  BREAK=replace: choudoufu correctly refused with a named collision (two live resources claiming one slot) rather than silently proposing nothing - the Break text's own outcome"
  else
    log "=== F1. choudoufu: change the ForceNew name argument, forcing a replace at the same declared address ==="
    perl -0pi -e 's/module "asg_sg_renamed" \{\n  source  = "terraform-aws-modules\/security-group\/aws"\n  version = "~> 5\.0"\n\n  name        = local\.name\n/module "asg_sg_renamed" {\n  source  = "terraform-aws-modules\/security-group\/aws"\n  version = "~> 5.0"\n\n  name        = "\${local.name}-v2"\n/' "$ADOPTED/main.tf"
    grep -q '${local.name}-v2' "$ADOPTED/main.tf" || fail "changing module.asg_sg_renamed's name argument did not match - the corpus pin has moved"

    F_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; F_PLAN_RC=$?
    [ "$F_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_PLAN_OUT" | tail -40; fail "the day2_replace plan exited $F_PLAN_RC"; }
    grep -qE '^  # module\.asg_sg_renamed\.aws_security_group\.this_name_prefix\[0\] must be replaced' <<< "$F_PLAN_OUT" \
      || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not propose replacing module.asg_sg_renamed's security group when its ForceNew name_prefix argument changes"; }
    grep -qE '~ +name_prefix +=.+forces replacement' <<< "$F_PLAN_OUT" \
      || { printf '%s\n' "$F_PLAN_OUT"; fail "the plan does not mark name_prefix as forcing replacement"; }
    F_PLAN_LINE="$(grep -oE 'Plan: [0-9]+ to add, [0-9]+ to change, [0-9]+ to destroy\.' <<< "$F_PLAN_OUT")"
    [ -n "$F_PLAN_LINE" ] || { printf '%s\n' "$F_PLAN_OUT" | tail -15; fail "the day2_replace plan has no summary line"; }
    log "  choudoufu: $F_PLAN_LINE - the security group forced to replace at the same declared address, name_prefix forces replacement"

    F_APPLY_OUT="$(cd "$ADOPTED" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; F_APPLY_RC=$?
    [ "$F_APPLY_RC" -eq 0 ] || { printf '%s\n' "$F_APPLY_OUT" | tail -40; fail "the day2_replace apply exited $F_APPLY_RC"; }
    grep -qE 'Apply complete! Resources: [0-9]+ added, [0-9]+ changed, [0-9]+ destroyed' <<< "$F_APPLY_OUT" \
      || { printf '%s\n' "$F_APPLY_OUT" | tail -20; fail "the day2_replace apply did not report a clean apply"; }
    log "  $(grep -E 'Apply complete' <<< "$F_APPLY_OUT")"

    # floci's own describe-security-groups on an unknown group id returns
    # a 200 with an empty SecurityGroups list rather than a real AWS
    # InvalidGroup.NotFound error (confirmed directly against floci here,
    # no tofu in the loop - a floci gap, not a choudoufu one), so
    # existence is read from the query result's own emptiness rather than
    # the CLI's exit code.
    F_OLD_STILL="$(awsl ec2 describe-security-groups --group-ids "$ASG_SG_ID" --query 'SecurityGroups[0].GroupId' --output text 2>/dev/null || true)"
    [ -z "$F_OLD_STILL" ] || [ "$F_OLD_STILL" = "None" ] \
      || fail "$ASG_SG_ID still exists after the replace - the old object was orphaned, not destroyed"
    log "  $ASG_SG_ID no longer exists - confirmed via the AWS CLI (empty describe-security-groups result), not through choudoufu's own report"

    F_NEW_ID="$(awsl ec2 describe-security-groups --filters "Name=tag:tofu-address,Values=module.asg_sg_renamed.aws_security_group.this_name_prefix:0" --query "SecurityGroups[0].GroupId" --output text)"
    [ -n "$F_NEW_ID" ] && [ "$F_NEW_ID" != "None" ] || fail "no live security group found carrying tofu-address=module.asg_sg_renamed.aws_security_group.this_name_prefix:0 after the replace"
    F_NEW_ADDR_TAG="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$F_NEW_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
    [ "$F_NEW_ADDR_TAG" = "module.asg_sg_renamed.aws_security_group.this_name_prefix:0" ] \
      || fail "$F_NEW_ID carries tofu-address=$F_NEW_ADDR_TAG after the replace, not module.asg_sg_renamed.aws_security_group.this_name_prefix:0 - the marker did not move onto the new object"
    log "  $F_NEW_ID (the new object) carries tofu-address=$F_NEW_ADDR_TAG - the marker moved onto the new object, read via the AWS CLI"

    # THE RECORD STORE, asserted by value (HANDOFF's safety rule; the
    # #398-guard shape: a stale record still naming the destroyed
    # security group would be exactly the wrong-marker failure that
    # outranks a missing one). The local record file at the SAME address
    # must now hold the NEW group's id, not the one captured in F0.
    F_NEW_IMPORT_ID="$(record_import_id "$F_RECORD")"
    [ "$F_NEW_IMPORT_ID" = "$F_NEW_ID" ] \
      || fail "the record for $F_ADDR names $F_NEW_IMPORT_ID after the replace, not the new object $F_NEW_ID - a stale record still claiming the destroyed object, the #398-guard shape"
    [ "$F_NEW_IMPORT_ID" != "$F_OLD_IMPORT_ID" ] \
      || fail "sanity: the record's import_id at $F_ADDR did not change at all across the replace"
    log "  record store: import_id $F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID at the same key ($F_ADDR) - read directly off the local record store file, not through choudoufu's own report"

    log "=== F2. one more plan: config and reality agree, no marker collision ==="
    F_FINAL_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; F_FINAL_PLAN_RC=$?
    [ "$F_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_FINAL_PLAN_OUT" | tail -40; fail "the post-replace plan exited $F_FINAL_PLAN_RC"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$F_FINAL_PLAN_OUT" \
      || { grep -E '^  #' <<< "$F_FINAL_PLAN_OUT"; fail "the post-replace plan is not empty"; }
    log "  no resource action proposed, no marker collision. The replace is complete and invisible to the next plan."

    ASG_SG_ID="$F_NEW_ID"
    gauntlet_stage day2_replace pass "choudoufu: changing module.asg_sg_renamed's ForceNew name argument (module CALL, passed through to its own aws_security_group.this_name_prefix's name_prefix) proposed a forced replace at the same declared address ($F_PLAN_LINE), applied cleanly; the old security group is confirmed gone via the AWS CLI (InvalidGroup.NotFound) and the new group ($F_NEW_ID) carries the marker; the local record store's record at the same address now names the new object's id, not the destroyed one ($F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID); the next plan proposes no resource action; stock oracle on cold_deploy's own state (F-ORACLE) also proposes replacing the security group at the same address ($REPLACE_ORACLE_PLAN_LINE, plan only, not applied - it shares floci's account with \$ADOPTED); BREAK=replace confirms a manufactured marker collision is reported loudly (\"Two live resources claiming one slot\") rather than silently proposed as nothing. Scope note: this exercises OpenTofu's default destroy-then-create ordering, not the create_before_destroy variant the stage's Title names; also scope note: the section originally targeted aws_sqs_queue.this_renamed and found a genuine, separate defect (mv.go's propagateModuleRename skipped MoveRecord for a same-module live-mv rename, leaving the local record stale even though the marker moved correctly) - FIXED on this branch, GitHub issue #412: propagateModuleRename now calls MoveRecord unconditionally for the renamed resource's own key before the moduleRenameBoundary guard; see this section's own header comment for the fix and eks-basic's/ecs-fargate's matching ones in this same unit, which independently hit the identical shape and were not re-run for #412."
  fi
  gauntlet_end_stage

  # ══════════════════════════════════════════════════════════════════════
  # PART G: CHANGE COUNT (day2_count, active - live/GAUNTLET.md #8;
  # written for issue #643's board repair sweep)
  # ══════════════════════════════════════════════════════════════════════
  #
  # THE VOCABULARY TRAP FIRST, because this is the one estate where it
  # actually bites: an aws_autoscaling_group's own desired_capacity /
  # min_size / max_size is NOT what this stage means. day2_count is about
  # OpenTofu's `count` META-ARGUMENT on a resource block - how many
  # resource INSTANCES the configuration declares - and the thing under
  # test is internal/live/discovery/count.go's slot binding: which
  # declared instance a live object stays bound to when the block's
  # instance count changes. Scaling an ASG's capacity would exercise none
  # of that; it is one instance's argument changing value, which is
  # drift_reconverge's shape, not this one's.
  #
  # THIS ESTATE HAS NO REAL SCALABLE count KNOB. Checked block by block
  # against .corpus/autoscaling at v9.3.0 before falling back:
  #   - the module's own aws_autoscaling_group.this / .idc pair is a
  #     count-gated 0-or-1 boolean toggle keyed off
  #     ignore_desired_capacity_changes, never N>1 - this script's own
  #     STAGE 2 already asserts exactly that pair resolving to .idc for
  #     module.complete and .this not existing at all;
  #   - aws_autoscaling_schedule, aws_autoscaling_policy and
  #     aws_autoscaling_traffic_source_attachment are `for_each` over the
  #     module call's variable MAPS, whose keys are names, not indices -
  #     "destroy the higher index" has no meaning there - and all three
  #     are untaggable and sit behind the emulator gaps STAGE 3's own
  #     header already lists (lex00/floci#112, the aws_autoscaling_policy
  #     field drops);
  #   - none of the twelve module CALLS in the example carries count or
  #     for_each at all.
  # So this section uses the sanctioned self-contained synthetic block
  # (live/GAUNTLET.md #8; precedent: reference-ec2-vpc's Part F and
  # corpus-iam-policy's Part G): aws_security_group.count_test, count = 2,
  # written to its OWN file ($ADOPTED/count_test.tf) so that nothing else
  # in the estate references it and main.tf - which PART E rewrites with
  # perl immediately after - is never touched. aws_security_group is a
  # type this estate exercises for real: module.asg_sg's own group is what
  # PART D renames and PART F force-replaces.
  #
  # WHY THAT TYPE AND NOT ANOTHER, read directly off this floci pin with
  # the AWS CLI and NO terraform in the loop before this section was
  # written: creating two groups, tagging them, deleting the higher one
  # and recreating it under the SAME group name returned a genuinely NEW
  # GroupId (sg-736e0505f1b204aa8 -> sg-6c45a8857a10a30a9) while the lower
  # one's id and its tofu-address tag were untouched. So "the recreated
  # instance is a new object" is provable by server-minted id here, and
  # neither corpus-hongbomiao-storage's changed-CreationDate variant nor
  # corpus-simpleinfra-dns's verified-absence variant is needed. The same
  # probe confirmed `--filters Name=group-name` genuinely narrows on this
  # pin (lex00/floci#150 is about `description`, which does not narrow -
  # PART GREENFIELD's own comment below records that defect), so each
  # instance is located by its CONFIGURED group name and never by the
  # marker this section then asserts by value; and it confirmed that
  # describe-security-groups on a deleted id returns an empty list rather
  # than InvalidGroup.NotFound, which is why absence is read as a length
  # of 0 (the same floci behaviour PART F's own F1 documents).
  #
  # WHERE THIS SECTION SITS, and why not after PART E the way
  # reference-ec2-vpc and corpus-iam-policy place theirs: PART E's apply
  # has a live hard-fail path (the `fail` on REMOVE_APPLY_RC, whose header
  # comment records a real sibling-orphan destroy-ordering gap that has
  # fired on this estate before), and fail() exits the script outright.
  # Sitting between PART F and PART E means day2_count is measured from
  # PART F's own converged state - F2 asserts an empty plan immediately
  # above - and is not hostage to a stage that has failed here before. It
  # costs PART E nothing: count_test is left declared and converged at
  # count = 2, so it contributes 0 to PART E's own "0 to add, 0 to change,
  # N to destroy" assertion; it is not an ASG, so PART E's live
  # ASG-count-drops-by-one check is unaffected; and it adds the same 2 to
  # both sides of PART E's tagged-object-count drop.
  #
  # BREAK_COUNT=1 exercises this stage's own Break control instead of the
  # real checks, and is independent of BREAK and BREAK=replace: after the
  # real scale-down plan it asserts the WRONG instance (count_test[0]
  # rather than count_test[1]) was the one destroyed - the Break text in
  # tools/gauntlet/stages.go for day2_count, verbatim: "Expect a different
  # instance to be destroyed; the assertion must fail." Both of its
  # branches end in fail(), so a BREAK_COUNT=1 run reports
  # `GAUNTLET stage=day2_count verdict=fail` and stops; a Break control
  # that could still report pass would prove nothing.

  gauntlet_begin_stage day2_count

  # count_test_block <count> <vpc_id HCL expression> <group-name prefix>:
  # this stage's own resource, added and removed entirely within PART G
  # (and its G0 stock oracle). Unquoted heredoc so $1/$2/$3 interpolate;
  # ${count.index} is escaped so bash never expands it as a parameter.
  count_test_block() {
    local n="$1" vpc_ref="$2" prefix="$3"
    cat <<COUNTEOF
resource "aws_security_group" "count_test" {
  count       = $n
  name        = "$prefix-\${count.index}"
  description = "day2_count evidence (gauntlet stage 8)"
  vpc_id      = $vpc_ref

  tags = {
    Name = "$prefix-\${count.index}"
  }
}
COUNTEOF
  }

  # sg_ids_by_group_name <endpoint> <group name>: every GroupId carrying
  # that exact group name, whitespace-separated. group-name is one of the
  # two filters lex00/floci#150's own repro confirms floci really applies.
  sg_ids_by_group_name() {
    aws --endpoint-url "$1" --region "$REGION" ec2 describe-security-groups \
      --filters "Name=group-name,Values=$2" --query "SecurityGroups[].GroupId" --output text 2>/dev/null
  }
  # require_one_sg <endpoint> <group name> <what>: sets SG_ONE to the one
  # matching GroupId, or fails loudly. It assigns a global rather than
  # printing, deliberately: fail() ends in `exit 1`, and inside a $(...)
  # command substitution that exits only the SUBSHELL - the verdict line
  # would be printed and the script would then carry on with an empty
  # value, which is the "a check that cannot fail" shape CLAUDE.md names.
  # grep -c over a newline split, not wc -w: BSD wc pads its count with
  # leading spaces so a string compare against "1" never matches on macOS
  # (the same trap STAGE 2's own LT_ID check documents).
  require_one_sg() {
    local ep="$1" name="$2" what="$3" ids n
    ids="$(sg_ids_by_group_name "$ep" "$name")"
    n="$(printf '%s\n' $ids | grep -c . || true)"
    [ "$n" = "1" ] || { printf 'group-name=%s matched %s group(s): %s\n' "$name" "$n" "${ids:-<none>}" >&2; fail "$what: expected exactly one live security group named $name, found $n"; }
    SG_ONE="$ids"
  }
  sg_addr_tag() { # <endpoint> <group id>
    aws --endpoint-url "$1" --region "$REGION" ec2 describe-tags \
      --filters "Name=resource-id,Values=$2" "Name=key,Values=tofu-address" \
      --query "Tags[0].Value" --output text 2>/dev/null
  }
  sg_exists_n() { # <endpoint> <group id> -> 1 or 0
    aws --endpoint-url "$1" --region "$REGION" ec2 describe-security-groups \
      --group-ids "$2" --query "length(SecurityGroups)" --output text 2>/dev/null || echo 0
  }

  COUNT_NAME="autoscaling-complete-count-test"
  COUNT_ORACLE_NAME="autoscaling-complete-count-oracle"

  # ── G0. the stock oracle (live/GAUNTLET.md #8: "Stock's plan for the
  # same count change, normalised") ─────────────────────────────────────
  # Unlike day2_remove's and day2_replace's oracles, this one cannot reuse
  # cold_deploy's own state: stock never had a count block at all, so the
  # oracle stands the identical 2-instance block up for real with the
  # plain binary, in its own working directory with its own tiny VPC and
  # its own state, and scales it down and back up. It runs against
  # $ENDPOINT - the account $PLAIN cold-deployed into and $ADOPTED took
  # over - which is safe because the oracle's own VPC and group names
  # collide with nothing there, and because it is torn down completely
  # before the real choudoufu leg runs. The teardown is not optional:
  # corpus-iam-policy's Part G records finding empirically that leaving
  # an oracle's own unmarked objects behind makes the real leg's marker
  # lookup pick whichever object the API returns first. Belt and braces
  # here, since the two legs also use DIFFERENT group names.
  log "=== G0. day2_count stock oracle: stand a 2-instance count block up with $TF_COLD_BIN, scale it 2 -> 1 -> 2 ==="
  ORACLE_COUNT_DIR="$WORK/plain-count-oracle"
  mkdir -p "$ORACLE_COUNT_DIR"
  write_count_oracle() { # <count>
    {
      cat <<'HCL'
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

resource "aws_vpc" "count_oracle" {
  cidr_block = "10.99.0.0/16"

  tags = {
    Name = "autoscaling-complete-count-oracle-vpc"
  }
}

HCL
      count_test_block "$1" "aws_vpc.count_oracle.id" "$COUNT_ORACLE_NAME"
    } > "$ORACLE_COUNT_DIR/main.tf"
  }

  write_count_oracle 2
  ( cd "$ORACLE_COUNT_DIR" && AWS_ENDPOINT_URL="$ENDPOINT" "$TF_COLD_BIN" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ORACLE_COUNT_DIR" && AWS_ENDPOINT_URL="$ENDPOINT" "$TF_COLD_BIN" init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_count stock oracle's init failed"; }
  ORACLE_COUNT_APPLY_OUT="$(cd "$ORACLE_COUNT_DIR" && AWS_ENDPOINT_URL="$ENDPOINT" "$TF_COLD_BIN" apply -input=false -auto-approve -no-color 2>&1)"; ORACLE_COUNT_APPLY_RC=$?
  [ "$ORACLE_COUNT_APPLY_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_COUNT_APPLY_OUT" | tail -30; fail "the day2_count stock oracle's baseline apply exited $ORACLE_COUNT_APPLY_RC"; }
  grep -qE 'Apply complete! Resources: 3 added, 0 changed, 0 destroyed' <<< "$ORACLE_COUNT_APPLY_OUT" \
    || { printf '%s\n' "$ORACLE_COUNT_APPLY_OUT" | tail -20; fail "stock did not create exactly 3 objects (its own VPC plus 2 count-test security groups) for the day2_count oracle"; }
  require_one_sg "$ENDPOINT" "$COUNT_ORACLE_NAME-0" "the day2_count stock oracle's baseline"; ORACLE_SG0_ID="$SG_ONE"
  require_one_sg "$ENDPOINT" "$COUNT_ORACLE_NAME-1" "the day2_count stock oracle's baseline"; ORACLE_SG1_ID="$SG_ONE"
  log "  stock: 2 instances created, count_test[0]=$ORACLE_SG0_ID count_test[1]=$ORACLE_SG1_ID"

  write_count_oracle 1
  ORACLE_CDOWN_PLAN_OUT="$(cd "$ORACLE_COUNT_DIR" && AWS_ENDPOINT_URL="$ENDPOINT" "$TF_COLD_BIN" plan -input=false -no-color 2>&1)"; ORACLE_CDOWN_PLAN_RC=$?
  [ "$ORACLE_CDOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_CDOWN_PLAN_OUT" | tail -30; fail "the day2_count stock oracle's scale-down plan exited $ORACLE_CDOWN_PLAN_RC"; }
  grep -qE '^  # aws_security_group\.count_test\[1\] will be destroyed' <<< "$ORACLE_CDOWN_PLAN_OUT" \
    || { printf '%s\n' "$ORACLE_CDOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-down plan does not destroy count_test[1]"; }
  grep -qE '^  # aws_security_group\.count_test\[0\] will be' <<< "$ORACLE_CDOWN_PLAN_OUT" \
    && { printf '%s\n' "$ORACLE_CDOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-down plan touches count_test[0], which must be untouched"; }
  grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$ORACLE_CDOWN_PLAN_OUT" \
    || { printf '%s\n' "$ORACLE_CDOWN_PLAN_OUT" | tail -10; fail "stock's scale-down plan proposes something other than exactly one destroy"; }
  ORACLE_CDOWN_APPLY_OUT="$(cd "$ORACLE_COUNT_DIR" && AWS_ENDPOINT_URL="$ENDPOINT" "$TF_COLD_BIN" apply -input=false -auto-approve -no-color 2>&1)"; ORACLE_CDOWN_APPLY_RC=$?
  [ "$ORACLE_CDOWN_APPLY_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_CDOWN_APPLY_OUT" | tail -30; fail "the day2_count stock oracle's scale-down apply exited $ORACLE_CDOWN_APPLY_RC"; }
  grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$ORACLE_CDOWN_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$ORACLE_CDOWN_APPLY_OUT"; fail "the day2_count stock oracle's scale-down apply was not exactly one destroy"; }
  [ "$(sg_exists_n "$ENDPOINT" "$ORACLE_SG0_ID")" = "1" ] || fail "stock's surviving count_test[0] ($ORACLE_SG0_ID) is gone after the scale-down"
  [ "$(sg_exists_n "$ENDPOINT" "$ORACLE_SG1_ID")" = "0" ] || fail "stock's count_test[1] ($ORACLE_SG1_ID) still exists after the scale-down destroy"
  log "  stock: exactly one destroy (count_test[1]=$ORACLE_SG1_ID), count_test[0]=$ORACLE_SG0_ID untouched"

  write_count_oracle 2
  ORACLE_CUP_PLAN_OUT="$(cd "$ORACLE_COUNT_DIR" && AWS_ENDPOINT_URL="$ENDPOINT" "$TF_COLD_BIN" plan -input=false -no-color 2>&1)"; ORACLE_CUP_PLAN_RC=$?
  [ "$ORACLE_CUP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_CUP_PLAN_OUT" | tail -30; fail "the day2_count stock oracle's scale-up plan exited $ORACLE_CUP_PLAN_RC"; }
  grep -qE '^  # aws_security_group\.count_test\[1\] will be created' <<< "$ORACLE_CUP_PLAN_OUT" \
    || { printf '%s\n' "$ORACLE_CUP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-up plan does not create count_test[1]"; }
  grep -qE '^  # aws_security_group\.count_test\[0\] will be' <<< "$ORACLE_CUP_PLAN_OUT" \
    && { printf '%s\n' "$ORACLE_CUP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-up plan touches count_test[0], which must be untouched"; }
  grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$ORACLE_CUP_PLAN_OUT" \
    || { printf '%s\n' "$ORACLE_CUP_PLAN_OUT" | tail -10; fail "stock's scale-up plan proposes something other than exactly one create"; }
  ORACLE_CUP_APPLY_OUT="$(cd "$ORACLE_COUNT_DIR" && AWS_ENDPOINT_URL="$ENDPOINT" "$TF_COLD_BIN" apply -input=false -auto-approve -no-color 2>&1)"; ORACLE_CUP_APPLY_RC=$?
  [ "$ORACLE_CUP_APPLY_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_CUP_APPLY_OUT" | tail -30; fail "the day2_count stock oracle's scale-up apply exited $ORACLE_CUP_APPLY_RC"; }
  grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$ORACLE_CUP_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$ORACLE_CUP_APPLY_OUT"; fail "the day2_count stock oracle's scale-up apply was not exactly one create"; }
  require_one_sg "$ENDPOINT" "$COUNT_ORACLE_NAME-1" "the day2_count stock oracle's scale-up"; ORACLE_SG1_NEW_ID="$SG_ONE"
  [ "$ORACLE_SG1_NEW_ID" != "$ORACLE_SG1_ID" ] \
    || fail "stock's recreated count_test[1] came back with the SAME id ($ORACLE_SG1_ID) it had before being destroyed - the oracle's own destroy was not real"
  [ "$(sg_exists_n "$ENDPOINT" "$ORACLE_SG0_ID")" = "1" ] || fail "stock's count_test[0] ($ORACLE_SG0_ID) is gone after the scale-up"
  ORACLE_COUNT_SHAPE="destroy count_test[1] ($ORACLE_SG1_ID) only on the way down, create count_test[1] back under a new id ($ORACLE_SG1_NEW_ID) on the way up, count_test[0] ($ORACLE_SG0_ID) untouched both times"
  log "  stock: exactly one create (count_test[1], new id $ORACLE_SG1_NEW_ID, was $ORACLE_SG1_ID), count_test[0]=$ORACLE_SG0_ID unchanged throughout"

  ORACLE_COUNT_DESTROY_OUT="$(cd "$ORACLE_COUNT_DIR" && AWS_ENDPOINT_URL="$ENDPOINT" "$TF_COLD_BIN" destroy -input=false -auto-approve -no-color 2>&1)"; ORACLE_COUNT_DESTROY_RC=$?
  [ "$ORACLE_COUNT_DESTROY_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_COUNT_DESTROY_OUT" | tail -30; fail "the day2_count stock oracle's teardown exited $ORACLE_COUNT_DESTROY_RC"; }
  grep -qE 'Destroy complete! Resources: 3 destroyed' <<< "$ORACLE_COUNT_DESTROY_OUT" \
    || { grep -E 'Destroy complete' <<< "$ORACLE_COUNT_DESTROY_OUT"; fail "the day2_count stock oracle's teardown was not exactly 3 destroys"; }
  [ -z "$(sg_ids_by_group_name "$ENDPOINT" "$COUNT_ORACLE_NAME-0")" ] \
    || fail "the day2_count stock oracle's own count_test[0] survived its teardown - the shared endpoint is not clean before the real leg runs"
  log "  stock oracle torn down (3 destroyed, its own VPC included): the shared endpoint is clean before the choudoufu leg runs"

  # ── G1. the real leg: add the count block through choudoufu ──────────
  log "=== G1. choudoufu: add aws_security_group.count_test, count = 2 ==="
  count_test_block 2 "module.vpc.vpc_id" "$COUNT_NAME" > "$ADOPTED/count_test.tf"
  COUNT_ADD_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; COUNT_ADD_PLAN_RC=$?
  [ "$COUNT_ADD_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_ADD_PLAN_OUT" | tail -40; fail "the count-block-add plan exited $COUNT_ADD_PLAN_RC"; }
  grep -qF 'Plan: 2 to add, 0 to change, 0 to destroy.' <<< "$COUNT_ADD_PLAN_OUT" \
    || { printf '%s\n' "$COUNT_ADD_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; printf '%s\n' "$COUNT_ADD_PLAN_OUT" | tail -10; fail "adding the count block did not plan exactly 2 creates and nothing else"; }
  COUNT_ADD_APPLY_OUT="$(cd "$ADOPTED" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_ADD_APPLY_RC=$?
  [ "$COUNT_ADD_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_ADD_APPLY_OUT" | tail -40; fail "the count-block-add apply exited $COUNT_ADD_APPLY_RC"; }
  grep -qE 'Resources: 2 added, 0 changed, 0 destroyed' <<< "$COUNT_ADD_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$COUNT_ADD_APPLY_OUT"; fail "the count-block-add apply did not create exactly 2 resources"; }

  # Located by the CONFIGURED group name, never by the marker - the marker
  # is what is then asserted BY VALUE below, and looking an object up by
  # the very tag under test is the vacuous comparison this script's own
  # STAGE 2 header warns about. live/MARKERS.md: an indexed instance's
  # tag value is colon-escaped (aws_x.count_test[1] -> aws_x.count_test:1);
  # a tag VALUE can never contain '['.
  require_one_sg "$ENDPOINT" "$COUNT_NAME-0" "the count-block-add"; SG0_ID="$SG_ONE"
  require_one_sg "$ENDPOINT" "$COUNT_NAME-1" "the count-block-add"; SG1_ID="$SG_ONE"
  SG0_ADDR_TAG="$(sg_addr_tag "$ENDPOINT" "$SG0_ID")"
  SG1_ADDR_TAG="$(sg_addr_tag "$ENDPOINT" "$SG1_ID")"
  [ "$SG0_ADDR_TAG" = 'aws_security_group.count_test:0' ] \
    || fail "count_test[0] ($SG0_ID) carries tofu-address=$SG0_ADDR_TAG, not aws_security_group.count_test:0"
  [ "$SG1_ADDR_TAG" = 'aws_security_group.count_test:1' ] \
    || fail "count_test[1] ($SG1_ID) carries tofu-address=$SG1_ADDR_TAG, not aws_security_group.count_test:1"
  [ "$SG0_ID" != "$SG1_ID" ] || fail "sanity: both count_test indices resolved to the same live security group $SG0_ID"
  log "  2 instances created: index 0 = $SG0_ID (tofu-address=$SG0_ADDR_TAG), index 1 = $SG1_ID (tofu-address=$SG1_ADDR_TAG) - read via the AWS CLI"

  # The record store, by value, at the bracket-spelled address PART F's
  # own helpers key on. A read half without its write half is exactly how
  # a plan proposing to CREATE something that already exists appears.
  COUNT0_RECORD="$ADOPTED/.tofu-records/tofu-records/$ESTATE/aws_security_group/$(record_key 'aws_security_group.count_test[0]')"
  COUNT1_RECORD="$ADOPTED/.tofu-records/tofu-records/$ESTATE/aws_security_group/$(record_key 'aws_security_group.count_test[1]')"
  [ -f "$COUNT0_RECORD" ] || fail "no local record file for aws_security_group.count_test[0] after the count-block-add apply"
  [ -f "$COUNT1_RECORD" ] || fail "no local record file for aws_security_group.count_test[1] after the count-block-add apply"
  [ "$(record_import_id "$COUNT0_RECORD")" = "$SG0_ID" ] \
    || fail "the record for aws_security_group.count_test[0] names $(record_import_id "$COUNT0_RECORD"), not the live $SG0_ID"
  [ "$(record_import_id "$COUNT1_RECORD")" = "$SG1_ID" ] \
    || fail "the record for aws_security_group.count_test[1] names $(record_import_id "$COUNT1_RECORD"), not the live $SG1_ID"
  log "  record store: count_test[0]=$SG0_ID count_test[1]=$SG1_ID at their own bracket-spelled keys - read directly off the local record store, not through choudoufu's own report"

  COUNT_NOOP_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; COUNT_NOOP_PLAN_RC=$?
  [ "$COUNT_NOOP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_NOOP_PLAN_OUT" | tail -40; fail "the post-add plan exited $COUNT_NOOP_PLAN_RC"; }
  grep -qF "No changes. Your infrastructure matches the configuration." <<< "$COUNT_NOOP_PLAN_OUT" \
    || { grep -E '^  # .+ (will be|must be)' <<< "$COUNT_NOOP_PLAN_OUT"; fail "the plan right after adding the count block is not empty - the two new instances did not bind their own markers cleanly"; }
  log "  No changes - both new instances plan empty immediately after creation"

  # ── G2. scale down 2 -> 1 ────────────────────────────────────────────
  log "=== G2. scale count down: 2 -> 1 ==="
  count_test_block 1 "module.vpc.vpc_id" "$COUNT_NAME" > "$ADOPTED/count_test.tf"
  COUNT_DOWN_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; COUNT_DOWN_PLAN_RC=$?
  [ "$COUNT_DOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | tail -40; fail "the scale-down plan exited $COUNT_DOWN_PLAN_RC"; }

  if [ "${BREAK_COUNT:-}" = "1" ]; then
    log "  BREAK_COUNT=1: this stage's Break control - expecting a DIFFERENT instance (count_test[0], not count_test[1]) to be the one destroyed. Both outcomes below are a fail, by design."
    printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'
    grep -qE '^  # aws_security_group\.count_test\[0\] will be destroyed' <<< "$COUNT_DOWN_PLAN_OUT" \
      || fail "BREAK_COUNT=1: the scale-down plan does not destroy count_test[0] - the Break control's own expectation, and it correctly does not hold, because choudoufu destroys the HIGHER index (count_test[1]) exactly as stock does. The real assertion this replaces is therefore load-bearing: it is not a grep that always matches."
    fail "BREAK_COUNT=1: the scale-down plan really does destroy count_test[0] - the WRONG instance. choudoufu's slot binding disagrees with stock's."
  fi

  grep -qE '^  # aws_security_group\.count_test\[1\] will be destroyed' <<< "$COUNT_DOWN_PLAN_OUT" \
    || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-down plan does not destroy count_test[1]"; }
  grep -qE '^  # aws_security_group\.count_test\[0\] will be' <<< "$COUNT_DOWN_PLAN_OUT" \
    && { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-down plan touches count_test[0], which must be untouched"; }
  grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$COUNT_DOWN_PLAN_OUT" \
    || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | tail -10; fail "choudoufu's scale-down plan proposes something other than exactly one destroy"; }
  log "  choudoufu: exactly one destroy (count_test[1]), count_test[0] untouched - the same shape the G0 stock oracle showed"

  COUNT_DOWN_APPLY_OUT="$(cd "$ADOPTED" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_DOWN_APPLY_RC=$?
  [ "$COUNT_DOWN_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_DOWN_APPLY_OUT" | tail -40; fail "the scale-down apply exited $COUNT_DOWN_APPLY_RC"; }
  grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$COUNT_DOWN_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$COUNT_DOWN_APPLY_OUT"; fail "the scale-down apply was not exactly one destroy"; }

  # The survivor's identity, re-read through the AWS CLI rather than out
  # of choudoufu's own report: the same live GroupId AND the same
  # tofu-address marker. floci answers describe-security-groups on a
  # deleted id with an empty list rather than InvalidGroup.NotFound
  # (confirmed directly, no tofu in the loop - PART F's F1 records the
  # same), so absence is read as a length of 0.
  [ "$(sg_exists_n "$ENDPOINT" "$SG0_ID")" = "1" ] \
    || fail "count_test[0] ($SG0_ID) no longer exists after the scale-down - the wrong instance was destroyed"
  [ "$(sg_exists_n "$ENDPOINT" "$SG1_ID")" = "0" ] \
    || fail "count_test[1] ($SG1_ID) still exists in the live account after the scale-down destroy - it was orphaned, not destroyed"
  require_one_sg "$ENDPOINT" "$COUNT_NAME-0" "the scale-down"; SG0_ID_AFTER_DOWN="$SG_ONE"
  [ "$SG0_ID_AFTER_DOWN" = "$SG0_ID" ] \
    || fail "count_test[0]'s live id changed across the scale-down ($SG0_ID -> $SG0_ID_AFTER_DOWN) - it was destroyed and recreated, not left alone"
  SG0_ADDR_AFTER_DOWN="$(sg_addr_tag "$ENDPOINT" "$SG0_ID")"
  [ "$SG0_ADDR_AFTER_DOWN" = 'aws_security_group.count_test:0' ] \
    || fail "count_test[0]'s tofu-address marker changed across the scale-down: $SG0_ADDR_TAG -> $SG0_ADDR_AFTER_DOWN"
  [ "$(record_import_id "$COUNT0_RECORD")" = "$SG0_ID" ] \
    || fail "the record for aws_security_group.count_test[0] names $(record_import_id "$COUNT0_RECORD") after the scale-down, not the untouched live $SG0_ID"
  log "  $SG1_ID (count_test[1]) is gone (0 found); $SG0_ID (count_test[0]) unchanged id, unchanged tofu-address, unchanged record - all read via the AWS CLI and the record store on disk"

  # ── G3. scale back up 1 -> 2 ─────────────────────────────────────────
  log "=== G3. scale count back up: 1 -> 2 ==="
  count_test_block 2 "module.vpc.vpc_id" "$COUNT_NAME" > "$ADOPTED/count_test.tf"
  COUNT_UP_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; COUNT_UP_PLAN_RC=$?
  [ "$COUNT_UP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | tail -40; fail "the scale-up plan exited $COUNT_UP_PLAN_RC"; }
  grep -qE '^  # aws_security_group\.count_test\[1\] will be created' <<< "$COUNT_UP_PLAN_OUT" \
    || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-up plan does not create count_test[1]"; }
  grep -qE '^  # aws_security_group\.count_test\[0\] will be' <<< "$COUNT_UP_PLAN_OUT" \
    && { printf '%s\n' "$COUNT_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-up plan touches count_test[0], which must be untouched"; }
  grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$COUNT_UP_PLAN_OUT" \
    || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | tail -10; fail "choudoufu's scale-up plan proposes something other than exactly one create"; }
  log "  choudoufu: exactly one create (count_test[1]), count_test[0] untouched - the same shape the G0 stock oracle showed"

  COUNT_UP_APPLY_OUT="$(cd "$ADOPTED" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_UP_APPLY_RC=$?
  [ "$COUNT_UP_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_UP_APPLY_OUT" | tail -40; fail "the scale-up apply exited $COUNT_UP_APPLY_RC"; }
  grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$COUNT_UP_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$COUNT_UP_APPLY_OUT"; fail "the scale-up apply was not exactly one create"; }

  require_one_sg "$ENDPOINT" "$COUNT_NAME-1" "the scale-up"; SG1_NEW_ID="$SG_ONE"
  [ "$SG1_NEW_ID" != "$SG1_ID" ] \
    || fail "count_test[1] came back with the SAME live id ($SG1_ID) it had before being destroyed - the destroy in G2 was not real"
  SG1_NEW_ADDR_TAG="$(sg_addr_tag "$ENDPOINT" "$SG1_NEW_ID")"
  [ "$SG1_NEW_ADDR_TAG" = 'aws_security_group.count_test:1' ] \
    || fail "the recreated count_test[1] ($SG1_NEW_ID) carries tofu-address=$SG1_NEW_ADDR_TAG, not aws_security_group.count_test:1"
  require_one_sg "$ENDPOINT" "$COUNT_NAME-0" "the scale-up"; SG0_ID_AFTER_UP="$SG_ONE"
  [ "$SG0_ID_AFTER_UP" = "$SG0_ID" ] \
    || fail "count_test[0]'s live id changed across the scale-up ($SG0_ID -> $SG0_ID_AFTER_UP)"
  [ "$(sg_addr_tag "$ENDPOINT" "$SG0_ID")" = 'aws_security_group.count_test:0' ] \
    || fail "count_test[0]'s tofu-address marker changed across the scale-up"
  [ "$(record_import_id "$COUNT1_RECORD")" = "$SG1_NEW_ID" ] \
    || fail "the record for aws_security_group.count_test[1] names $(record_import_id "$COUNT1_RECORD") after the scale-up, not the NEW object $SG1_NEW_ID - a stale record still claiming the destroyed object, the wrong-marker shape HANDOFF ranks above a missing one"
  [ "$(record_import_id "$COUNT0_RECORD")" = "$SG0_ID" ] \
    || fail "the record for aws_security_group.count_test[0] moved across the scale-up"
  log "  count_test[1] recreated under a NEW id ($SG1_NEW_ID, was $SG1_ID), tofu-address=$SG1_NEW_ADDR_TAG, record re-keyed to the new object; count_test[0] ($SG0_ID) untouched throughout the down-then-up cycle - all read via the AWS CLI and the record store on disk"

  # ── G4. the next plan is empty ───────────────────────────────────────
  log "=== G4. one more plan: config and reality agree, nothing left to propose ==="
  COUNT_FINAL_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; COUNT_FINAL_PLAN_RC=$?
  [ "$COUNT_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_FINAL_PLAN_OUT" | tail -40; fail "the post-scale-up plan exited $COUNT_FINAL_PLAN_RC"; }
  grep -qF "No changes. Your infrastructure matches the configuration." <<< "$COUNT_FINAL_PLAN_OUT" \
    || { grep -E '^  # .+ (will be|must be)' <<< "$COUNT_FINAL_PLAN_OUT"; fail "the post-scale-up plan is not empty"; }
  log "  No changes. The scale-down-then-up cycle is complete and invisible to the next plan."

  gauntlet_stage day2_count pass "choudoufu: scaling aws_security_group.count_test from 2 to 1 proposed and applied exactly one destroy (0 add, 0 change, 1 destroy) and it was the HIGHER index, count_test[1] ($SG1_ID); count_test[0] ($SG0_ID) kept its live GroupId, its tofu-address=aws_security_group.count_test:0 marker and its local record across the move, all re-read through the AWS CLI and off the record-store file rather than out of choudoufu's own report. Scaling back from 1 to 2 proposed and applied exactly one create (1 add, 0 change, 0 destroy) and count_test[1] came back as a genuinely NEW object ($SG1_ID -> $SG1_NEW_ID) carrying tofu-address=aws_security_group.count_test:1, with its record re-keyed to the new id rather than left stale on the destroyed one; count_test[0] was untouched throughout and the next plan is empty. Stock oracle (G0): a plain $TF_COLD_BIN working directory standing the identical 2-instance block up for real in its own VPC and scaling it the same way shows the identical shape - $ORACLE_COUNT_SHAPE - and was torn down before the choudoufu leg ran. SYNTHETIC BLOCK, and why: this estate declares no scalable count anywhere - the module's own aws_autoscaling_group.this/.idc pair is a 0-or-1 boolean toggle, the schedules/policies/traffic-source attachments are for_each over name-keyed maps, and none of the twelve module calls carries count or for_each - so this is live/GAUNTLET.md #8's sanctioned self-contained fallback, the same one reference-ec2-vpc's Part F and corpus-iam-policy's Part G use, on aws_security_group, a type this estate already exercises through module.asg_sg. An ASG's own desired_capacity is deliberately NOT what was scaled: this stage is about the count meta-argument's slot binding (internal/live/discovery/count.go), not about a live group's capacity. BREAK_COUNT=1 confirms the check has teeth: expecting count_test[0] to be the destroyed instance makes this stage report fail."
  gauntlet_end_stage

  # ══════════════════════════════════════════════════════════════════════
  # PART E: REMOVE A BLOCK (day2_remove, active - live/GAUNTLET.md #7)
  # ══════════════════════════════════════════════════════════════════════
  #
  # Starts from Part D's real, completed state (module.asg_sg_renamed and
  # aws_sqs_queue.this_renamed are both bound and converged; module.default
  # was never touched by either rename). module.default is removed here -
  # it is self-contained (consumes module.vpc's private_subnets and
  # data.aws_ami, nothing else in main.tf reads its own outputs), so
  # deleting its block needs only outputs.tf's own 25 references stripped,
  # the same live/e2e/lib/strip-output-blocks.py used for the D-REMOVE-
  # ORACLE copy above. Its own aws_autoscaling_group carries no ownership
  # marker at all (untaggable - the `tag` nested-block shape markers.go's
  # own Taggable doc comment names, see this script's header), so the
  # destroy proposal for it, if any, has to come from the record the
  # migrate-adoption apply above should have written for it (#364 A2), not
  # from a tag; this is the reason this stage's own oracle comparison below
  # is by COUNT, not by asserting a specific untaggable child's own address.
  gauntlet_begin_stage day2_remove
  log "=== E0. delete module.default's block ==="
  perl -0777pi -e 's/module "default" \{.*?\n\}\n\n/\n/s' "$ADOPTED/main.tf"
  grep -q 'module "default" {' "$ADOPTED/main.tf" && fail "removing module.default's block did not match - the config has moved"
  python3 "$ROOT/live/e2e/lib/strip-output-blocks.py" "$ADOPTED/outputs.tf" "module.default." || fail "stripping module.default's outputs failed"
  ( cd "$ADOPTED" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ADOPTED" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the day2_remove reinit failed"; }
  REMOVE_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; REMOVE_PLAN_RC=$?
  [ "$REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -40; fail "the day2_remove plan exited $REMOVE_PLAN_RC"; }
  if grep -q 'is unclaimed, so this may be the same resource under a new instance key' <<< "$REMOVE_PLAN_OUT"; then
    printf '%s\n' "$REMOVE_PLAN_OUT" | tail -40
    fail "choudoufu withheld a destroy of module.default's resources as a possible rename (discovery.go's classifyOrphans) - this is the honest wall issue #358 names, not a pass"
  fi
  CHOUDOUFU_REMOVE_N="$(grep -cE '^  # module\.default\..+ will be destroyed' <<< "$REMOVE_PLAN_OUT" || true)"
  if [ "$CHOUDOUFU_REMOVE_N" -lt "$ORACLE_REMOVE_N" ]; then
    # A REAL, DOCUMENTED gap, not a surprise (see day2_remove's own detail
    # for corpus-dynamodb-table-basic, [gauntlet:corpus-dynamodb-table-
    # basic/day2_remove]): a type admitted by the provider's own identity
    # schema rather than by the generated admission table is invisible to
    # the estate-wide destroy sweep (live/LIMITATIONS.md, "Resource type
    # has no orphan recovery"). aws_autoscaling_group is the prime
    # candidate here, given it is already untaggable by a different,
    # independently-confirmed mechanism (the `tag` nested-block shape).
    printf '%s\n' "$REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'
    log "  choudoufu proposes $CHOUDOUFU_REMOVE_N of the oracle's $ORACLE_REMOVE_N destroys under module.default - a real gap, not this stage's own load-bearing check failing"
    gauntlet_stage day2_remove fail "choudoufu's remove plan destroys only $CHOUDOUFU_REMOVE_N of module.default's resources; stock oracle on cold_deploy's own state (D-REMOVE-ORACLE) proposes $ORACLE_REMOVE_N destroys for the same module (0 add, 0 change, $ORACLE_REMOVE_N destroy). choudoufu has strictly less destroy coverage than stock here - the missing address(es) are left live and orphaned, most likely module.default's own aws_autoscaling_group: it carries no ownership marker at all (the \`tag\` nested-block shape, not the top-level tags map internal/live/markers.TagSurface requires) and may also be admitted by the provider's identity schema rather than the generated admission table (live/LIMITATIONS.md, \"Resource type has no orphan recovery\", the same class corpus-dynamodb-table-basic's day2_remove hits on aws_dynamodb_resource_policy). Not fixed in this script-only pass; see live/gauntlet/logs/corpus-autoscaling-complete.log for the exact plan diff"
  else
    grep -qF "Plan: 0 to add, 0 to change, $ORACLE_REMOVE_N to destroy." <<< "$REMOVE_PLAN_OUT" \
      || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -10; fail "choudoufu's remove plan touches something other than module.default's own $ORACLE_REMOVE_N resources"; }
    log "  choudoufu: exactly $ORACLE_REMOVE_N destroys under module.default, matching the stock oracle, nothing else"

    ASG_COUNT_BEFORE="$(awsl autoscaling describe-auto-scaling-groups --query 'length(AutoScalingGroups)' --output text)"
    TAGGED_BEFORE="$(awsl resourcegroupstaggingapi get-resources --tag-filters "Key=tofu-estate,Values=$ESTATE" --query 'length(ResourceTagMappingList)' --output text)"

    REMOVE_APPLY_OUT="$(cd "$ADOPTED" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; REMOVE_APPLY_RC=$?
    if [ "$REMOVE_APPLY_RC" -ne 0 ]; then
      printf '%s\n' "$REMOVE_APPLY_OUT" | tail -40
      # RE-VERIFIED against current main (alias-wave-b unit, 2026-08): the plan
      # matches the stock oracle's own count (2 destroys, module.default's LT
      # and ASG, both undeclared/record-orphan now that the block is gone),
      # but the APPLY itself fails mid-destroy: real AWS's own ASG-delete
      # path (scale to 0 before deleting instances/the group) rejects the
      # UpdateAutoScalingGroup call because the launch template the ASG still
      # references has already been deleted - i.e. the sibling launch
      # template was destroyed before the ASG that depends on it, the
      # opposite of the order a normal dependency-graph destroy enforces.
      # module.default's ASG and LT have no HCL left to derive that edge from
      # once the block is removed (both are undeclared orphans discovered by
      # address, not by a live dependency graph), so this looks like the same
      # family 91281978b9 named for corpus-mastino-dns ("give an undeclared
      # record-orphan a destroy-before-parent ordering hint") one hop further
      # out: a same-level (sibling) reference between two undeclared orphans,
      # not a parent/child one - NOT fixed in this re-verification pass; the
      # actual AWS diagnostic is captured below for whoever picks this up.
      REMOVE_APPLY_ERR="$(grep -E '^Error: ' <<< "$REMOVE_APPLY_OUT" | head -1)"
      fail "the day2_remove apply exited $REMOVE_APPLY_RC - ${REMOVE_APPLY_ERR:-no 'Error:' line found}; plan matched the stock oracle's destroy count ($ORACLE_REMOVE_N) but the apply itself failed destroying module.default's undeclared orphan pair (its launch template and its ASG), most likely a destroy-order gap between two sibling undeclared orphans with no HCL left to derive the ASG-references-LT edge from - see live/gauntlet/logs/corpus-autoscaling-complete.log"
    fi
    grep -qE "Resources: 0 added, 0 changed, $ORACLE_REMOVE_N destroyed" <<< "$REMOVE_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$REMOVE_APPLY_OUT"; fail "the day2_remove apply was not exactly $ORACLE_REMOVE_N destroys"; }

    ASG_COUNT_AFTER="$(awsl autoscaling describe-auto-scaling-groups --query 'length(AutoScalingGroups)' --output text)"
    [ "$ASG_COUNT_AFTER" -eq "$((ASG_COUNT_BEFORE - 1))" ] \
      || fail "the live auto-scaling-group count went from $ASG_COUNT_BEFORE to $ASG_COUNT_AFTER across the destroy, expected exactly one fewer - module.default's untaggable ASG was not genuinely destroyed (confirmed via the AWS CLI, not through choudoufu's own report)"
    TAGGED_AFTER="$(awsl resourcegroupstaggingapi get-resources --tag-filters "Key=tofu-estate,Values=$ESTATE" --query 'length(ResourceTagMappingList)' --output text)"
    [ "$TAGGED_AFTER" -lt "$TAGGED_BEFORE" ] \
      || fail "the tagged object count did not drop at all across the destroy ($TAGGED_BEFORE -> $TAGGED_AFTER)"
    log "  live ASG count $ASG_COUNT_BEFORE -> $ASG_COUNT_AFTER, tagged object count $TAGGED_BEFORE -> $TAGGED_AFTER - confirmed via the AWS CLI, not through choudoufu's own report"

    log "=== E2. one more plan: config and reality agree, nothing left to propose ==="
    E_FINAL_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; E_FINAL_PLAN_RC=$?
    [ "$E_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$E_FINAL_PLAN_OUT" | tail -40; fail "the post-remove plan exited $E_FINAL_PLAN_RC"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$E_FINAL_PLAN_OUT" \
      || { grep -E '^  #' <<< "$E_FINAL_PLAN_OUT"; fail "the post-remove plan is not empty"; }
    log "  No changes. The removal is complete and invisible to the next plan."

    gauntlet_stage day2_remove pass "choudoufu: deleting module.default's block proposed exactly $ORACLE_REMOVE_N destroys (0 add, 0 change, $ORACLE_REMOVE_N destroy), matching the stock oracle's own count and applied cleanly; the live ASG count dropped by exactly one and the tagged object count dropped too, both confirmed via the AWS CLI, not through choudoufu's own report; the next plan proposes no resource action; stock oracle on cold_deploy's own state (D-REMOVE-ORACLE) also proposes exactly $ORACLE_REMOVE_N destroys for the same module"
  fi
  gauntlet_end_stage
fi
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# PART GREENFIELD (greenfield, live/GAUNTLET.md #13, active)
# ══════════════════════════════════════════════════════════════════════════
#
# choudoufu applies the identical, unmodified example directly with a live
# block, no migration, into a SEPARATE namespace ($GREEN_ENDPOINT). The
# oracle is $ENDPOINT/$PLAIN, STAGE 1's own plain terraform cold-deploy -
# still genuinely unmarked at this point in the script (STAGE 2's
# live-import has not run yet), so it is exactly "the cloud after stock's
# cold deploy" live/GAUNTLET.md #13 asks for, with no third container
# needed. Given this estate's size (twelve module calls, ~90 resources),
# the object-by-object comparison other, smaller crossings run is not
# repeated exhaustively here: this checks the total resource count on both
# sides, the total live tagged-object count on both sides, and a
# representative structural comparison of the two objects Part D also
# targets (the standalone SQS queue and the asg_sg security group's own
# rules) - the same "representative set, not exhaustive" standard
# live/GAUNTLET.md's own test_plan stage already uses for identity strings.
gauntlet_begin_stage greenfield
log "=== PART GREENFIELD: 0. one more floci container, a fresh namespace ==="
docker run -d --rm -p "${FLOCI_GREEN_PORT}:4566" --name "$FLOCI_GREEN_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_GREEN_NAME failed"
GH=""
for _ in $(seq 1 45); do
  GH="$(curl -fs "${GREEN_ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"ec2"' <<< "${GH:-}" && break
  sleep 2
done
grep -q '"ec2"' <<< "${GH:-}" || fail "floci did not come up healthy (ec2) at $GREEN_ENDPOINT"
log "  healthy: greenfield=$GREEN_ENDPOINT oracle=$ENDPOINT (STAGE 1's own plain apply)"

copy_estate "$WORK/green"
emulator_delta "$GREEN"
# strict { no_source_create = "create" } on the greenfield delta: found
# necessary while re-verifying this stage under CHOUDOUFU_NODE_RESOLVE's
# default flip (2026-08-25) - a genuinely cold apply refuses config-
# identified instances whose identity value depends on a Cloud property
# (aws_sqs_queue's account-id segment; account ID is not discoverable from
# an empty account's own listings, see identity.CloudContext's own doc
# comment) the node seam is not wired to answer (#365 ruling 4's default
# refusal of that same "cannot yet tell genuinely new from real" ambiguity,
# reached here through a different component shape than the aws_route
# unknown-value case #388's node-seam fix already exempts unconditionally).
# A greenfield apply is the one case an operator KNOWS every such instance
# is a real create. Same fix, same precedent as corpus-alb-complete's own
# 898091b8f2 and corpus-ec2-instance-complete's own equivalent.
perl -0pi -e 's/(required_providers \{\n    aws = \{\n      source  = "hashicorp\/aws"\n      version = "= 6\.59\.0"\n    \}\n  \}\n)\}/$1\n  live {\n    estate = "'"$GREEN_ESTATE"'"\n\n    record_store "local" {\n      path = ".tofu-records"\n    }\n\n    strict {\n      no_source_create = "create"\n    }\n  }\n}/' "$GREEN/versions.tf"
grep -q "estate = \"$GREEN_ESTATE\"" "$GREEN/versions.tf" || fail "the greenfield live-block delta did not match versions.tf"
log "  DELTA  emulator flags + provider pin + live block (record_store, same reason as \$ADOPTED - main.tf:889's provisioner; strict.no_source_create=create for #388's default-flip greenfield ambiguity)"

log "=== PART GREENFIELD: 1. choudoufu apply from nothing, no migration, no state file ever existing ==="
( cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the greenfield init failed"; }
GREEN_APPLY_OUT="$(cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"
if [ $? -ne 0 ]; then
  printf '%s\n' "$GREEN_APPLY_OUT" | grep -E '^Error' -A 6 | head -200
  gauntlet_stage greenfield fail "the greenfield apply failed - see live/gauntlet/logs/corpus-autoscaling-complete.log for the full diagnostic; cold_deploy/migrate/test_plan/test_apply/drift_reconverge/day2_rename/day2_remove for this estate are unaffected (checked earlier/later in the same run)"
  gauntlet_end_stage
  docker rm -f "$FLOCI_GREEN_NAME" >/dev/null 2>&1 || true
  SKIP_GREENFIELD_REST=1
fi
if [ -z "${SKIP_GREENFIELD_REST:-}" ]; then
STOCK_N="$(grep -oE '[0-9]+ added' <<< "$COLD_OUT" | head -1 | awk '{print $1}')"
GREEN_N="$(grep -oE '[0-9]+ added' <<< "$GREEN_APPLY_OUT" | head -1 | awk '{print $1}')"
[ -n "$STOCK_N" ] && [ -n "$GREEN_N" ] || fail "could not read a resource count out of one of the two applies"
[ "$GREEN_N" = "$STOCK_N" ] || fail "the greenfield apply created $GREEN_N resources, stock's cold deploy created $STOCK_N - not the same estate"
log "  $(grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT") ($GREEN_N, matching stock's own $STOCK_N)"

awsg() { aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" "$@"; }

log "=== PART GREENFIELD: 2. markers, read through the AWS CLI directly ==="
GREEN_SQS_URL="$(awsg sqs list-queues --query 'QueueUrls[0]' --output text)"
[ -n "$GREEN_SQS_URL" ] && [ "$GREEN_SQS_URL" != "None" ] || fail "no live sqs queue found in the greenfield namespace"
GREEN_SQS_ADDR="$(awsg sqs list-queue-tags --queue-url "$GREEN_SQS_URL" --query "Tags.\"tofu-address\"" --output text)"
[ "$GREEN_SQS_ADDR" = "aws_sqs_queue.this" ] || fail "the greenfield sqs queue carries tofu-address=$GREEN_SQS_ADDR, not aws_sqs_queue.this"
GREEN_SQS_EST="$(awsg sqs list-queue-tags --queue-url "$GREEN_SQS_URL" --query "Tags.\"tofu-estate\"" --output text)"
[ "$GREEN_SQS_EST" = "$GREEN_ESTATE" ] || fail "the greenfield sqs queue carries tofu-estate=$GREEN_SQS_EST, not $GREEN_ESTATE"
log "  sqs queue carries tofu-address=$GREEN_SQS_ADDR tofu-estate=$GREEN_SQS_EST - read via the AWS CLI, not choudoufu's own report"

log "=== PART GREENFIELD: 3. the record store holds instances, including the untaggable ASGs (#364 A2) ==="
GREEN_RECORD_FILES="$(find "$GREEN/.tofu-records/tofu-records" -type f ! -name '*.lock' ! -name '*.tmp-*' 2>/dev/null | wc -l | tr -d ' ')"
[ "$GREEN_RECORD_FILES" -gt 0 ] || fail "expected at least one record under the local record store after the greenfield apply, found none"
log "  $GREEN_RECORD_FILES records persisted, read directly off the local record store"

log "=== PART GREENFIELD: 4. the next plan proposes nothing ==="
GREEN_PLAN_OUT="$(cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; GREEN_PLAN_RC=$?
[ "$GREEN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$GREEN_PLAN_OUT" | tail -30; fail "the greenfield replan exited $GREEN_PLAN_RC"; }
if ! grep -qF "No changes. Your infrastructure matches the configuration." <<< "$GREEN_PLAN_OUT"; then
  # A REAL finding, not a surprise to paper over: a plan proposing to
  # CREATE something that already exists is the failure HANDOFF ranks
  # above a missing marker (a refusal stops a human; a create is
  # something a human approves without knowing it is wrong). Recorded
  # honestly rather than retried into passing.
  NONEMPTY_ITEMS="$(grep -E '^  # .+ will be' <<< "$GREEN_PLAN_OUT" | sed 's/^  # //' | tr '\n' '; ')"
  log "  the replan is NOT empty: $NONEMPTY_ITEMS"
  EMULATOR_NOTE=""
  if grep -q 'aws_ec2_capacity_reservation' <<< "$NONEMPTY_ITEMS"; then
    # Confirmed by reading the AWS CLI directly against this same
    # floci image with no tofu in the loop: a genuine choudoufu CREATE
    # of aws_ec2_capacity_reservation (tags declared or not) never
    # writes any tags at all, while a subsequent update or a plain
    # `aws ec2 create-tags` on the same object round-trips fine - the
    # AWS provider sends this one action's inline tags under
    # TagSpecifications.N.* (plural), every other action's handler in
    # floci only ever reads TagSpecification.N.* (singular), so this
    # object's create-time marker is silently dropped. Row 4 of
    # HANDOFF's five-row table: fixed at floci's own level
    # (lex00/floci#137, PR lex00/floci#138, branch
    # fix/car-tag-specifications-plural, pushed to origin), not yet
    # repinned here - that is the orchestrator's shared-layer batch,
    # not this script's to do.
    EMULATOR_NOTE=" Confirmed floci emulator gap, fixed and pushed to origin, not yet repinned: lex00/floci#137 / PR lex00/floci#138 (CreateCapacityReservation drops inline tags sent as the plural TagSpecifications.N.*, which is what a real terraform-aws-provider apply sends for this one action)."
  fi
  gauntlet_stage greenfield fail "the greenfield replan proposes real resource action on objects the SAME apply just created (no other run touched this namespace in between): $NONEMPTY_ITEMS. A create proposed for something that already exists is the wrong-marker-shaped failure HANDOFF ranks above a missing one, not a safe fallback; not fixed in this script-only pass. $GREEN_N/$STOCK_N objects match by count and the sqs queue's own marker verified fine (see the earlier PART GREENFIELD steps in the same run), so this is narrower than a total apply failure - the specific objects named above are the gap.$EMULATOR_NOTE"
  gauntlet_end_stage
  docker rm -f "$FLOCI_GREEN_NAME" >/dev/null 2>&1 || true
  SKIP_GREENFIELD_REST=1
fi
if [ -z "${SKIP_GREENFIELD_REST:-}" ]; then
log "  No changes."

log "=== PART GREENFIELD: 5. structural comparison against stock's cold deploy (STAGE 1), via the AWS CLI on both endpoints ==="
sg_shape() { # $1=endpoint $2=security-group-id
  aws --endpoint-url "$1" --region "$REGION" ec2 describe-security-groups --group-ids "$2" \
    --query "SecurityGroups[0].[length(IpPermissions),length(IpPermissionsEgress)]" --output text 2>/dev/null
}
vpc_ids_by_name_tag() { # $1=endpoint $2=Name tag value -> zero or more vpc ids, tab-separated on one line
  aws --endpoint-url "$1" --region "$REGION" ec2 describe-vpcs \
    --filters "Name=tag:Name,Values=$2" --query "Vpcs[].VpcId" --output text
}
sg_list_in_vpc() { # $1=endpoint $2=vpc-id -> "GroupId<TAB>Description", one line per group, EVERY group in that vpc
  aws --endpoint-url "$1" --region "$REGION" ec2 describe-security-groups \
    --filters "Name=vpc-id,Values=$2" --query "SecurityGroups[].[GroupId,Description]" --output text
}
# module.asg_sg's own `name` argument is local.name (basename(path.cwd)),
# not a literal "asg_sg" - the group's real AWS name is the example
# directory's own basename ("complete") in every copy of this estate
# (plain, adopted, green all share the same trailing
# "autoscaling/examples/complete" path, so basename(path.cwd) is
# identical in all three), so a group-name filter for "*asg_sg*" can
# never match anything and this whole comparison was dead code until the
# greenfield stage started clearing PART GREENFIELD: 4's replan check
# (this floci repin).
#
# THIS SECTION USED TO select the group with a server-side
# `Name=description,Values=A security group` filter and take `[0]`, on
# the claim that the description is unique across the example and so
# `[0]` is safe. THAT CLAIM IS FALSE, and not because the configuration
# changed: floci's DescribeSecurityGroups ignores the `description`
# filter name entirely and returns EVERY security group in the account
# regardless of the value passed - confirmed directly against the API,
# no tofu in the loop, by repeating the identical query with a value
# guaranteed not to match anything and getting back the SAME unfiltered
# list (lex00/floci#150, filed against this exact defect; `vpc-id` and
# `group-name`, tested the same way against the same data, correctly
# narrow the result). So `[0]` was an order-unspecified pick over the
# WHOLE account - both VPCs' own auto-created default security groups
# included - and intermittently landed on one of those instead of
# module.asg_sg's, which is what an earlier run of this script wrongly
# attributed to real-API timing variance. Ground truth via
# `describe-security-group-rules --filters Name=group-id,Values=<id>`
# (exact-id filtering, unaffected by this bug) on the actually-intended
# group showed 1 ingress/1 egress on BOTH sides throughout every probe:
# this estate's behaviour matched stock the whole time, and only the
# SELECTION was ever wrong.
#
# Hardened to depend on no filter that `description`'s own bug shows
# floci might silently ignore: scope server-side on `vpc-id` (confirmed
# correctly narrowing, unlike `description` - lex00/floci#150's own
# repro) to this estate's one non-default VPC - module.vpc's own
# `name = local.name` writes that same basename as the VPC's Name tag -
# then match `Description` EXACTLY in bash against every group the
# vpc-id filter returned, and insist on exactly one match. Zero matches
# or more than one is a hard, loud fail here, never a `[0]`.
ESTATE_DIR_NAME="$(basename "$GREEN")"
[ "$ESTATE_DIR_NAME" = "$(basename "$PLAIN")" ] || fail "internal: greenfield/plain work dirs have different basenames ($ESTATE_DIR_NAME vs $(basename "$PLAIN")) - the vpc Name-tag lookup below assumes they match"
GREEN_VPC_IDS="$(vpc_ids_by_name_tag "$GREEN_ENDPOINT" "$ESTATE_DIR_NAME")"
STOCK_VPC_IDS="$(vpc_ids_by_name_tag "$ENDPOINT" "$ESTATE_DIR_NAME")"
read -ra GREEN_VPC_ARR <<< "$GREEN_VPC_IDS"
read -ra STOCK_VPC_ARR <<< "$STOCK_VPC_IDS"
[ "${#GREEN_VPC_ARR[@]}" -eq 1 ] || fail "expected exactly one VPC tagged Name=$ESTATE_DIR_NAME in the greenfield namespace, found ${#GREEN_VPC_ARR[@]} ($GREEN_VPC_IDS)"
[ "${#STOCK_VPC_ARR[@]}" -eq 1 ] || fail "expected exactly one VPC tagged Name=$ESTATE_DIR_NAME in stock's own cold-deploy namespace, found ${#STOCK_VPC_ARR[@]} ($STOCK_VPC_IDS)"
GREEN_VPC_ID="${GREEN_VPC_ARR[0]}"
STOCK_VPC_ID="${STOCK_VPC_ARR[0]}"

GREEN_SG_ROWS="$(sg_list_in_vpc "$GREEN_ENDPOINT" "$GREEN_VPC_ID")"
STOCK_SG_ROWS="$(sg_list_in_vpc "$ENDPOINT" "$STOCK_VPC_ID")"
GREEN_SG_MATCHES=()
while IFS=$'\t' read -r sg_id sg_desc; do
  [ "$sg_desc" = "A security group" ] && GREEN_SG_MATCHES+=("$sg_id")
done <<< "$GREEN_SG_ROWS"
STOCK_SG_MATCHES=()
while IFS=$'\t' read -r sg_id sg_desc; do
  [ "$sg_desc" = "A security group" ] && STOCK_SG_MATCHES+=("$sg_id")
done <<< "$STOCK_SG_ROWS"
if [ "${#GREEN_SG_MATCHES[@]}" -ne 1 ]; then
  printf '%s\n' "$GREEN_SG_ROWS" | sed 's/^/    /' >&2
  fail "expected exactly one security group in the greenfield vpc $GREEN_VPC_ID with Description exactly \"A security group\" (client-side match over a server-side vpc-id-only filter - description filtering is a floci no-op, lex00/floci#150), found ${#GREEN_SG_MATCHES[@]}; full per-vpc group list on stderr above"
fi
if [ "${#STOCK_SG_MATCHES[@]}" -ne 1 ]; then
  printf '%s\n' "$STOCK_SG_ROWS" | sed 's/^/    /' >&2
  fail "expected exactly one security group in stock's own cold-deploy vpc $STOCK_VPC_ID with Description exactly \"A security group\" (client-side match over a server-side vpc-id-only filter - description filtering is a floci no-op, lex00/floci#150), found ${#STOCK_SG_MATCHES[@]}; full per-vpc group list on stderr above"
fi
GREEN_SG_ID="${GREEN_SG_MATCHES[0]}"
STOCK_SG_ID="${STOCK_SG_MATCHES[0]}"
GREEN_SG_SHAPE="$(sg_shape "$GREEN_ENDPOINT" "$GREEN_SG_ID")"
STOCK_SG_SHAPE="$(sg_shape "$ENDPOINT" "$STOCK_SG_ID")"
[ "$GREEN_SG_SHAPE" = "$STOCK_SG_SHAPE" ] || fail "the asg_sg security group's rule counts differ: greenfield=$GREEN_SG_SHAPE stock=$STOCK_SG_SHAPE"
log "  asg_sg security group rule counts match (ingress/egress: $GREEN_SG_SHAPE), identified by vpc-id scoping + an exact client-side Description match, not floci's broken description filter (lex00/floci#150)"

GREEN_TAGGED="$(awsg resourcegroupstaggingapi get-resources --tag-filters "Key=tofu-estate,Values=$GREEN_ESTATE" --query 'length(ResourceTagMappingList)' --output text)"
[ "$GREEN_TAGGED" -gt 0 ] || fail "no live objects carry tofu-estate=$GREEN_ESTATE after the greenfield apply"
log "  $GREEN_TAGGED objects carry tofu-estate=$GREEN_ESTATE - read via the AWS CLI"

gauntlet_stage greenfield pass "$GREEN_N resources from nothing, matching stock's own cold-deploy count ($STOCK_N); the sqs queue's markers verified via the AWS CLI; $GREEN_RECORD_FILES records in the local record store including the untaggable ASGs (#364 A2); replan empty; the asg_sg security group's rule counts match stock's cold deploy structurally, via the AWS CLI on both endpoints, marker tags never compared; $GREEN_TAGGED objects carry the estate tag"
fi
gauntlet_end_stage
docker rm -f "$FLOCI_GREEN_NAME" >/dev/null 2>&1 || true
fi

gauntlet_end_stage
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
