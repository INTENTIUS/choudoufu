#!/usr/bin/env bash
set -uo pipefail

# terraform-aws-modules/terraform-aws-iam's iam-policy example
# (.corpus/iam/examples/iam-policy), crossed through choudoufu against
# floci via the real, five-stage pipeline (cold deploy, migrate, test plan,
# test apply, drift and reconverge) issue #274 tracks in
# live/corpus-crossing-manifest.json. This is a terraform-aws-modules
# EXAMPLE, not one org's private estate - the configuration an average user
# copies when they first reach for the aws provider - and it was the first
# module example ever crossed against a real emulator at all (this script's
# predecessor). That predecessor ran a 2-3 stage shape (choudoufu apply from
# a live block present from the start, delete state, replan empty twice);
# this version adds a genuine plain-terraform cold deploy ahead of it and a
# real drift-and-reconverge behind it, following live/e2e/corpus-vpc-complete
# and live/e2e/corpus-lambda-simple's shape.
#
# The estate: two aws_iam_policy instances behind the iam-policy module - one
# from a literal policy document with a server-assigned name_prefix, one
# statically named from a rendered aws_iam_policy_document data source - plus
# a third module instantiation with create = false that contributes nothing.
#
# STAGE-BY-STAGE SHAPE (issue #274's five-stage pipeline; see
# live/corpus-crossing-manifest.json):
#
#   1. COLD DEPLOY   plain `terraform apply` (real HashiCorp terraform, not
#                     choudoufu), no live block anywhere - the honest proof
#                     the estate is real and buildable, and genuinely
#                     unmarked live infra for stage 2 to adopt.
#   2. MIGRATE        `choudoufu live-import -state=<cold state> -estate=...
#                     -approve`, then ONE ordinary `choudoufu apply` against
#                     the now-live-blocked estate with no state file present.
#                     That second command used to be the tofu-slot
#                     convergence apply stage 3 could not be empty without;
#                     since choudoufu #372 it is a no-op, and it stays here
#                     as the assertion that it IS one - see "THE TOFU-SLOT
#                     FINDING" below.
#   3. TEST PLAN      delete the state file (already gone by this point),
#                     `choudoufu live-plan`, assert the plan proposes no
#                     resource action *and* re-assert both rendered
#                     identities against a live aws_iam_policy read through
#                     the AWS CLI, never through choudoufu.
#   4. TEST APPLY     apply that empty plan; assert a genuine no-op
#                     (0 added, 0 changed, 0 destroyed) and that the
#                     tofu-estate-tagged object count is unchanged.
#   5. DRIFT AND      mutate one policy's Example tag out of band via the AWS
#      RECONVERGE     CLI directly against floci, replan, assert the diff
#                     proposes fixing exactly that one object and nothing
#                     else, then apply and confirm it reconverged.
#
# THE ONE ONBOARDING DELTA (stage 1). Nothing here needed a provider pin, a
# backend edit, or an emulator override beyond the standard flags on the
# provider block: the example's own `version = ">= 6.28"` resolves straight
# to a current provider with list resources intact, and it declares no
# cloud/backend block to remove.
#
# THE TOFU-SLOT FINDING, discovered building this script, and CLOSED by
# choudoufu #372 on 2026-08-22. What it found: live-import -approve wrote only
# tofu-estate and tofu-address, so both of this estate's aws_iam_policy
# resources - each `count = var.create ? 1 : 0`, exactly the shape that needs
# a slot - came out of a migration with none, and the FIRST live-plan after it
# proposed adding `tofu-slot = "0"` to both, nothing else. One ordinary
# `choudoufu apply` applied exactly that ("0 added, 2 changed, 0 destroyed")
# and every replan after was empty. Deliberate product behavior at the time,
# and folded into stage 2 rather than stage 3 because it was not a no-op.
#
# What #372 changed: for a count set whose live members carry NO slot at all,
# there is nothing for a discovery pass to discover - the assignment is
# slot i for index i (internal/live/slots.Sequential), frozen from the same
# per-instance tofu-address values the migration is already writing. So
# live-import now writes the slot in the same tags write, and this estate's
# migration is complete when live-import returns. The apply below stays
# exactly where it was and now asserts the OPPOSITE - "0 added, 0 changed, 0
# destroyed" - which is what makes it a regression guard for #372 rather than
# a step that could be deleted: if the slot write ever stops happening, this
# apply changes 2 resources again and stage 2 fails here.
#
# It applies HERE because aws_iam_policy is server-assigned
# (identity.TypeIdentity.ServerAssigned - the module's own use_name_prefix
# default is why: IAM mints the suffix). That is #372's gate, and it is not
# decoration. Discovery assigns a slot only to an instance it classifies
# ClassNeedsDiscovery, and a server-assigned type is the one case where that
# class is certain without resolving the configuration - which a migration,
# reading a state file, has not got. A count instance of a client-named type
# gets no slot here and still gets one from the first replan, exactly as
# before; corpus-sqs-basic is that case and still runs the convergence apply
# this script no longer needs.
#
# The slot values themselves are read back off both live policies through the
# AWS CLI below, by value. Both read "0" and both are right: each policy is
# the sole member of its own module's count set, so "0" is the whole of each
# set. That is also why this estate cannot, on its own, prove the assignment
# is per-set rather than a constant - the multi-member case is pinned with no
# cloud at all in internal/live/liveimport/slot_test.go
# (TestApprove_WritesSequentialSlotsOnASlotlessCountSet: three instances, 0,
# 1 and 2, asserted by value on the tag map that reached the provider).
#
# THE OUTPUTS QUIRK, carried over from the predecessor script. This estate
# declares root `output` blocks, and live-plan holds no state between runs,
# so there is never a prior output baseline to diff against. Every run
# therefore shows a permanent "Changes to Outputs" section, and OpenTofu's
# renderer omits the "Plan: N to add, N to change, N to destroy" line
# entirely whenever 0 resources change but the outputs section is non-empty -
# it does not print "Plan: 0 to add, 0 to change, 0 to destroy" either. Every
# empty-plan assertion below checks for the absence of a resource action
# header ("will be created" / "will be updated" / "will be destroyed")
# instead of a summary line, for exactly this reason.
#
#   bash live/e2e/corpus-iam-policy/run.sh
#
# Needs Docker, the AWS CLI, and the real `terraform` binary on PATH for
# stage 1. .corpus is read, never written: the estate is copied out to a
# temp directory first, same as every other corpus crossing.
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4680, clear of run.sh's
#                4566 and every other live/e2e fixture's port).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        set to 1 to corrupt one expected identity string ahead of
#                stage 3's assertion (the same shape and resource type, the
#                wrong module) AND to tamper a second, unrelated policy's tag
#                ahead of stage 5's drift assertion - proving both are
#                load-bearing rather than a grep/count that always matches.
#
# Exit codes: 0 on a real pass of all five stages, non-zero on a real
# failure. Every assertion reads command output, an exit code, or the
# emulator's own answer through the AWS CLI, never choudoufu's own report of
# itself.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SRC_EXAMPLE="$ROOT/.corpus/iam/examples/iam-policy"
SRC_MODULE="$ROOT/.corpus/iam/modules/iam-policy"
WORK="$(mktemp -d)"
EST="$WORK/iam/examples/iam-policy"
FLOCI_PORT="${FLOCI_PORT:-4680}"
FLOCI_NAME="choudoufu-corpus-iam-policy-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="iam-policy-crossing"
REGION="eu-west-1"
ACCOUNT="000000000000"

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
command -v terraform >/dev/null 2>&1 || fail "the terraform binary is not on PATH - needed to build unmarked reference infra"
[ -d "$SRC_EXAMPLE" ] || fail "$SRC_EXAMPLE is missing - run 'just corpus-fetch' first"
[ -d "$SRC_MODULE" ] || fail "$SRC_MODULE is missing - run 'just corpus-fetch' first"

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

# .corpus is shared across every worktree and is NEVER written to: the
# example and the module it references are copied out, preserving the
# relative path the example's `source = "../../modules/iam-policy"` expects.
mkdir -p "$WORK/iam/examples" "$WORK/iam/modules"
cp -R "$SRC_EXAMPLE" "$WORK/iam/examples/iam-policy"
cp -R "$SRC_MODULE" "$WORK/iam/modules/iam-policy"
rm -rf "$EST/.terraform" "$EST/.terraform.lock.hcl"
[ -f "$EST/main.tf" ] || fail "the estate copy is missing main.tf"
log "  estate + module copied out of .corpus into $WORK"

# ── 1. the onboarding delta - emulator flags only, no live block yet ───────
log "=== 1. the one onboarding delta ==="
perl -0pi -e 's/(provider "aws" \{\n  region = "eu-west-1"\n)\}/$1\n  access_key                   = "test"\n  secret_key                   = "test"\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  s3_use_path_style            = true\n}/' "$EST/main.tf"
grep -q 's3_use_path_style' "$EST/main.tf" || fail "the emulator delta did not match main.tf - the corpus pin has moved"
log "  DELTA  emulator flags added to the provider block; no backend, no version pin, no live block yet"

log "=== 2. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"iam"' <<< "${HEALTH:-}" && break
  sleep 2
done
grep -q '"iam"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (iam) at $ENDPOINT"
log "  healthy"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 1: COLD DEPLOY - plain terraform, no choudoufu, no live block
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=cold_deploy
log "=== STAGE 1: cold deploy (terraform apply, the real unmodified example + delta) ==="
( cd "$EST" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$EST" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "stage 1 init failed"; }
COLD_OUT="$(cd "$EST" && terraform apply -input=false -auto-approve -no-color 2>&1)"; COLD_RC=$?
[ "$COLD_RC" -eq 0 ] || { printf '%s\n' "$COLD_OUT" | tail -40; fail "the cold apply failed"; }
grep -qE 'Apply complete! Resources: 2 added' <<< "$COLD_OUT" \
  || { grep -E 'Apply complete' <<< "$COLD_OUT"; fail "the cold apply did not create exactly 2 resources"; }
log "  $(grep -E 'Apply complete' <<< "$COLD_OUT")"
[ -f "$EST/terraform.tfstate" ] || fail "plain terraform left no state file to migrate from"

POLICY1_ARN="arn:aws:iam::${ACCOUNT}:policy/example_from_data_source"
POLICY2_ARN="$(awsl iam list-policies --path-prefix / \
  --query "Policies[?starts_with(PolicyName, 'example-') == \`true\`].Arn | [0]" --output text)"
[ -n "$POLICY2_ARN" ] && [ "$POLICY2_ARN" != "None" ] || fail "could not find the name_prefix policy through the AWS CLI"
log "  both policies live: $POLICY1_ARN and $POLICY2_ARN"

UNMARKED="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$UNMARKED" = "0" ] || fail "plain terraform's own objects already carry tofu-estate=$ESTATE before migration - this crossing proves nothing"
log "  confirmed unmarked: 0 objects carry tofu-estate=$ESTATE before migration"

cp "$EST/terraform.tfstate" "$WORK/cold.tfstate"

log ""
log "STAGE 1 (cold deploy): PASS"
gauntlet_stage cold_deploy pass "$(grep -E 'Apply complete' <<< "$COLD_OUT"); 0 objects carry tofu-estate=$ESTATE before migration"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE - choudoufu live-import against the cold state, then one
# ordinary apply to converge tofu-slot (see the TOFU-SLOT FINDING above)
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=migrate
log "=== STAGE 2: migrate (choudoufu live-import -approve, then converge) ==="
perl -0pi -e 's/(required_providers \{\n    aws = \{\n      source  = "hashicorp\/aws"\n      version = ">= 6\.28"\n    \}\n  \}\n)\}/$1\n  live {\n    estate = "'"$ESTATE"'"\n  }\n}/' "$EST/versions.tf"
grep -q "estate = \"$ESTATE\"" "$EST/versions.tf" || fail "the live block delta did not match versions.tf - the corpus pin has moved"

( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "choudoufu init failed"; }

# The cold state file is not touched by live-import (it reads the WORK copy
# via -state) but is removed here regardless, ahead of the schedule stage 3
# would otherwise remove it on - the live-markers apply a few lines down
# must run with no state file present, same as every other command from here
# on out (issue #73: no state ops).
rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"

IMPORT_OUT="$(cd "$EST" && "$TOFU" live-import -state="$WORK/cold.tfstate" -estate="$ESTATE" -no-color 2>&1)"; IMPORT_RC=$?
[ "$IMPORT_RC" -eq 0 ] || { printf '%s\n' "$IMPORT_OUT" | tail -40; fail "live-import (dry run) failed"; }
grep -qF "2 of 2 resource instance(s) are eligible for stamping" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import did not verify exactly 2 of 2 resources as eligible - the corpus pin or the fix under test has moved"; }
grep -qF "No tag has been written." <<< "$IMPORT_OUT" || fail "the dry run wrote a tag - it must not"
log "  dry run: 2 of 2 eligible; nothing written yet"

APPROVE_OUT="$(cd "$EST" && "$TOFU" live-import -state="$WORK/cold.tfstate" -estate="$ESTATE" -approve -no-color 2>&1)"; APPROVE_RC=$?
[ "$APPROVE_RC" -eq 0 ] || { printf '%s\n' "$APPROVE_OUT" | tail -40; fail "live-import -approve failed"; }
grep -qF "2 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 0 skipped" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not stamp exactly 2 of 2 resources cleanly"; }
log "  2 stamped"

WANT_POLICY1_ADDR="module.iam_policy_from_data_source.aws_iam_policy.policy:0"
WANT_POLICY2_ADDR="module.iam_policy.aws_iam_policy.policy:0"
# The literal tag VALUE stamped on a count/for_each instance escapes "[0]" to
# ":0" (live/MARKERS.md's escaping rule, internal/live/markers.EscapeAddress)
# - that is WANT_POLICY2_ADDR above, read via the AWS CLI. A live-plan diff's
# own "# addr will be updated in-place" header renders the ordinary,
# unescaped Terraform resource address instead ("[0]", not ":0") - a second,
# bracket-form spelling of the same instance, needed only for the stage 5
# comparison against that header text below.
WANT_POLICY2_ADDR_BRACKET="module.iam_policy.aws_iam_policy.policy[0]"

GOT_POLICY1_ADDR="$(awsl iam list-policy-tags --policy-arn "$POLICY1_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_POLICY1_ADDR" = "$WANT_POLICY1_ADDR" ] || fail "$POLICY1_ARN carries tofu-address=$GOT_POLICY1_ADDR, not $WANT_POLICY1_ADDR"
GOT_POLICY2_ADDR="$(awsl iam list-policy-tags --policy-arn "$POLICY2_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_POLICY2_ADDR" = "$WANT_POLICY2_ADDR" ] || fail "$POLICY2_ARN carries tofu-address=$GOT_POLICY2_ADDR, not $WANT_POLICY2_ADDR"
log "  markers verified directly against IAM, not through choudoufu's own report:"
log "    $POLICY1_ARN -> tofu-address=$GOT_POLICY1_ADDR"
log "    $POLICY2_ARN -> tofu-address=$GOT_POLICY2_ADDR"

# The third marker, by value, off the live objects (choudoufu #372). Both
# policies declare count = var.create ? 1 : 0, so both are count sets of one
# and both must carry tofu-slot = "0" the moment live-import returns - not
# after an apply, which is what this estate's "TOFU-SLOT FINDING" used to
# document. Read through IAM's own list-policy-tags, never through
# choudoufu's report, same as the addresses above.
# Not under BREAK: this script's BREAK corrupts an identity string ahead of
# stage 3 and must go on failing THERE (see the header), so a second BREAK
# site here would move which stage goes red first. What keeps these two lines
# load-bearing instead is the no-op apply below - it changes 2 resources the
# moment the slot write regresses - and, without a cloud at all,
# internal/live/liveimport/slot_test.go's by-value pins.
WANT_SLOT="0"
GOT_POLICY1_SLOT="$(awsl iam list-policy-tags --policy-arn "$POLICY1_ARN" --query "Tags[?Key=='tofu-slot'].Value | [0]" --output text)"
[ "$GOT_POLICY1_SLOT" = "$WANT_SLOT" ] || fail "$POLICY1_ARN carries tofu-slot=$GOT_POLICY1_SLOT, not $WANT_SLOT - live-import did not settle the slot for a slotless count set (choudoufu #372)"
GOT_POLICY2_SLOT="$(awsl iam list-policy-tags --policy-arn "$POLICY2_ARN" --query "Tags[?Key=='tofu-slot'].Value | [0]" --output text)"
[ "$GOT_POLICY2_SLOT" = "$WANT_SLOT" ] || fail "$POLICY2_ARN carries tofu-slot=$GOT_POLICY2_SLOT, not $WANT_SLOT - live-import did not settle the slot for a slotless count set (choudoufu #372)"
log "    $POLICY1_ARN -> tofu-slot=$GOT_POLICY1_SLOT"
log "    $POLICY2_ARN -> tofu-slot=$GOT_POLICY2_SLOT"

# What used to be the tofu-slot convergence apply (see this script's header),
# kept as the regression guard for #372: with the slot written at migrate
# time there is nothing left to converge, so this apply has to be a genuine
# no-op. If the slot write regresses, this line reads "0 added, 2 changed, 0
# destroyed" again and stage 2 fails right here.
CONVERGE_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; CONVERGE_RC=$?
[ "$CONVERGE_RC" -eq 0 ] || { printf '%s\n' "$CONVERGE_OUT" | tail -40; fail "the post-migration apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$CONVERGE_OUT" \
  || { grep -E 'Apply complete' <<< "$CONVERGE_OUT"; grep -E '^  # .+ will be' <<< "$CONVERGE_OUT"; fail "the apply straight after live-import was not a no-op - the migration left something for a plan to finish (choudoufu #372 is about exactly this)"; }
log "  $(grep -E 'Apply complete' <<< "$CONVERGE_OUT") (nothing left to converge)"
[ ! -f "$EST/terraform.tfstate" ] || fail "the post-migration apply wrote a state file"

log ""
log "STAGE 2 (migrate): PASS"
gauntlet_stage migrate pass "2 of 2 stamped, both carrying tofu-slot=$GOT_POLICY1_SLOT/$GOT_POLICY2_SLOT read back through IAM (choudoufu #372); $(grep -E 'Apply complete' <<< "$CONVERGE_OUT") - nothing left to converge"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - state deleted (already true), live-plan empty,
# identities re-asserted
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=test_plan
log "=== STAGE 3: test plan (live-plan empty, identities re-checked) ==="
[ ! -f "$EST/terraform.tfstate" ] || fail "a state file exists ahead of stage 3"

plan_into() { ( cd "$EST" && "$TOFU" live-plan -input=false -no-color ); }
PLAN_OUT="$(plan_into 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -60; fail "live-plan exited $PLAN_RC"; }
[ ! -f "$EST/terraform.tfstate" ] || fail "live-plan wrote a state file"
# Not a "No changes."/"Plan:" grep - see the header comment on the outputs
# quirk. This estate has root outputs and live-plan carries no state to diff
# them against, so a "Plan:" summary line is never printed, empty or not.
grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$PLAN_OUT" \
  && { grep -E '^  # .+ will be' <<< "$PLAN_OUT"; fail "the plan proposes a resource change"; }
grep -qE '^Foreign resources: (none|nothing was swept)' <<< "$PLAN_OUT" \
  || { grep -E '^Foreign resources:' <<< "$PLAN_OUT"; fail "the plan reports foreign resources"; }
log "  no resource change proposed; nothing foreign"

WANT_POLICY1_ADDR2="$WANT_POLICY1_ADDR"
if [ "${BREAK:-}" = "1" ]; then
  WANT_POLICY1_ADDR2="module.iam_policy_disabled.aws_iam_policy.policy:0"
  log "  BREAK=1: expecting tofu-address=$WANT_POLICY1_ADDR2 on the data-source"
  log "           policy - the SAME shape and the SAME resource type, just the"
  log "           wrong (and in fact never-created) module. This step must fail."
fi
GOT_POLICY1_ADDR2="$(awsl iam list-policy-tags --policy-arn "$POLICY1_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_POLICY1_ADDR2" = "$WANT_POLICY1_ADDR2" ] || fail "$POLICY1_ARN's tofu-address is $GOT_POLICY1_ADDR2, not $WANT_POLICY1_ADDR2"
GOT_POLICY2_ADDR2="$(awsl iam list-policy-tags --policy-arn "$POLICY2_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_POLICY2_ADDR2" = "$WANT_POLICY2_ADDR" ] || fail "$POLICY2_ARN's tofu-address changed across the empty plan: $WANT_POLICY2_ADDR -> $GOT_POLICY2_ADDR2"
log "  identity re-check (read via the AWS CLI, after the state file has never existed this run): both unchanged"

log ""
log "STAGE 3 (test plan): PASS"
gauntlet_stage test_plan pass "no resource change proposed, nothing foreign; identity re-check (via the AWS CLI) both unchanged"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 4: TEST APPLY - apply the empty plan, assert a genuine no-op
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=test_apply
log "=== STAGE 4: test apply (apply the empty plan; object count unchanged) ==="
BEFORE_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"

APPLY2_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; APPLY2_RC=$?
[ "$APPLY2_RC" -eq 0 ] || { printf '%s\n' "$APPLY2_OUT" | tail -40; fail "the post-migration apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY2_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY2_OUT"; fail "the post-migration apply was not a no-op"; }

AFTER_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$AFTER_N" = "$BEFORE_N" ] || fail "object count changed across a no-op apply: $BEFORE_N -> $AFTER_N"
[ ! -f "$EST/terraform.tfstate" ] || fail "a state file exists after the apply"
log "  genuine no-op: $BEFORE_N objects before, $AFTER_N after, no state file either time"

log ""
log "STAGE 4 (test apply): PASS"
gauntlet_stage test_apply pass "genuine no-op: $BEFORE_N objects before, $AFTER_N after, no state file either time"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 5: DRIFT AND RECONVERGE - mutate one object, replan, assert one fix
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=drift_reconverge
log "=== STAGE 5: drift and reconverge (mutate one object out of band) ==="
if [ "${BREAK:-}" = "1" ]; then
  # A second, unrelated object is mutated too - the assertion below must
  # catch this as MORE than one object proposed, not silently pass.
  awsl iam tag-policy --policy-arn "$POLICY2_ARN" --tags Key=Example,Value=tampered-by-BREAK
  log "  BREAK=1: also tampered $POLICY2_ARN's Example tag - stage 5 must now see TWO drifted objects and fail the single-object assertion"
fi

awsl iam tag-policy --policy-arn "$POLICY1_ARN" --tags Key=Example,Value=tampered-out-of-band
DRIFTED_VALUE="$(awsl iam list-policy-tags --policy-arn "$POLICY1_ARN" --query "Tags[?Key=='Example'].Value | [0]" --output text)"
[ "$DRIFTED_VALUE" = "tampered-out-of-band" ] || fail "the out-of-band tag mutation did not take"
log "  mutated $POLICY1_ARN's Example tag to \"tampered-out-of-band\" directly via the AWS CLI"

DRIFT_PLAN_OUT="$(plan_into 2>&1)"; DRIFT_PLAN_RC=$?
[ "$DRIFT_PLAN_RC" -eq 0 ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | tail -60; fail "the drift-detection plan exited $DRIFT_PLAN_RC"; }

CHANGED_ADDRS="$(grep -oE '^  # \S+ will be updated' <<< "$DRIFT_PLAN_OUT" | awk '{print $2}' | sort -u)"
N_CHANGED="$(printf '%s\n' "$CHANGED_ADDRS" | grep -c . || true)"
if [ "${BREAK:-}" = "1" ]; then
  [ "$N_CHANGED" = "1" ] && fail "BREAK=1 set (two objects tampered), but the plan proposes fixing only 1 - this assertion is not load-bearing"
  log "  BREAK=1: the plan proposes fixing $N_CHANGED objects, correctly more than one - the single-object assertion below is skipped"
else
  [ "$N_CHANGED" = "1" ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | grep -E '^  # .+ will be'; fail "expected exactly 1 object proposed for a fix, got $N_CHANGED"; }
  printf '%s\n' "$CHANGED_ADDRS" | grep -qF "$WANT_POLICY2_ADDR_BRACKET" && fail "the plan proposes changing $WANT_POLICY2_ADDR_BRACKET, which was never touched"
  log "  the plan proposes fixing exactly one object: $(printf '%s' "$CHANGED_ADDRS")"

  RECONVERGE_APPLY="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; RECONVERGE_RC=$?
  [ "$RECONVERGE_RC" -eq 0 ] || { printf '%s\n' "$RECONVERGE_APPLY" | tail -40; fail "the reconverge apply failed"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$RECONVERGE_APPLY" \
    || { grep -E 'Apply complete' <<< "$RECONVERGE_APPLY"; fail "the reconverge apply did not change exactly 1 resource"; }
  FIXED_VALUE="$(awsl iam list-policy-tags --policy-arn "$POLICY1_ARN" --query "Tags[?Key=='Example'].Value | [0]" --output text)"
  [ "$FIXED_VALUE" = "ex-iam-policy" ] || fail "$POLICY1_ARN's Example tag is \"$FIXED_VALUE\" after reconverging, not \"ex-iam-policy\""
  log "  reconverged: $POLICY1_ARN's Example tag is back to \"ex-iam-policy\""

  log ""
  log "STAGE 5 (drift and reconverge): PASS"
  gauntlet_stage drift_reconverge pass "one object tampered ($POLICY1_ARN's Example tag), plan proposed fixing exactly one object, apply changed 1 and reconverged the tag"
  log ""

  CURRENT_STAGE=""
  gauntlet_end

  log "=== PASS ==="
  log ""
  log "A terraform-aws-modules EXAMPLE - the configuration a new user copies"
  log "first - crossed through all five stages: cold deploy with plain"
  log "terraform, choudoufu live-import adoption plus the tofu-slot"
  log "convergence apply it requires, an empty replan with the state file"
  log "deleted and both rendered identities checked against IAM's own answer,"
  log "a genuine no-op apply, and drift on one policy reconverging without"
  log "touching the other."
fi
