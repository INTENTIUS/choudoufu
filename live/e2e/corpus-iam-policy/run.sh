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
#                Stage 3's corruption calls fail() and exits nonzero on
#                purpose, so a single BREAK=1 run never reaches stage 5 or
#                day2_rename's own break below - each is a separate,
#                deliberate corruption, checked one at a time.
#   BREAK_RENAME set to 1 to run day2_rename's own break control instead of
#                the real rename checks: rename module.iam_policy_from_data_source
#                WITHOUT a moved block, and assert the plan proposes creating
#                the new address rather than the zero-churn update the moved
#                block and live-mv paths produce. Independent of BREAK, for
#                the reason above.
#   BREAK_REMOVE set to 1 to run day2_remove's own break control instead of
#                the real remove checks: keep module.iam_policy_renamed's
#                block in the config and assert no destroy is proposed for
#                it (the Break text in tools/gauntlet/stages.go for
#                day2_remove is literally "keep the block; no destroy may
#                be proposed"). Independent of BREAK and only reachable
#                when BREAK_RENAME is not 1, because the day2_remove checks
#                start from day2_rename's own real, completed rename.
#   BREAK        also accepts "replace" to run day2_replace's own break
#                control instead of the real replace checks: manufacture
#                the coexistence a skipped destroy would leave behind (see
#                PART F). Independent of BREAK=1 and BREAK_RENAME/
#                BREAK_REMOVE - a separate value on the same variable,
#                checked one at a time the same way BREAK_RENAME already
#                is.
#   BREAK_GREEN  set to 1 to run the greenfield stage's own break control
#                instead of the real object-by-object comparison: drop one
#                policy from the expected inventory before the count check
#                (the Break text in tools/gauntlet/stages.go for greenfield
#                is literally "Drop one resource from the expected
#                inventory; the comparison must fail"). Independent of
#                BREAK, BREAK_RENAME and BREAK_REMOVE - greenfield runs
#                before all three, right after STAGE 1's cold deploy.
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
  docker rm -f "$FLOCI_NAME" "${FLOCI_GREEN_NAME:-}" >/dev/null 2>&1 || true
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

# day2_rename's stock oracle (live/GAUNTLET.md #6, tracked as issue #357):
# "Stock with the same moved block plans zero churn." Run against a COPY of
# the state cold_deploy (STAGE 1) just left, before choudoufu or live-import
# ever touches these objects - the real terraform.tfstate stays untouched
# here so STAGE 2's live-import below still sees the original, unmarked
# resource names. Using the post-adoption estate instead would confound the
# comparison: every resource would also show its ownership-marker tags
# being stripped back out (stock's tags map only ever names what this
# module declares), which is real but has nothing to do with the rename
# this oracle exists to check.
#
# The oracle copy's directory is named "iam-policy", not "iam-policy-
# oracle" or similar: this estate's own local.name computes
# "ex-${basename(path.cwd)}", stamped onto every resource's Example tag, so
# a differently-named copy would show a REAL diff in that tag that has
# nothing to do with the rename - found empirically renaming the first
# draft of this check, where "iam-policy-oracle" turned a true no-op oracle
# plan into "2 to change".
CURRENT_STAGE=day2_rename
log "=== STAGE 1.5: day2_rename stock oracle: renaming both module calls, through moved blocks, on cold_deploy's own state ==="
mkdir -p "$WORK/oracle-tree/iam/examples" "$WORK/oracle-tree/iam/modules"
EST_ORACLE="$WORK/oracle-tree/iam/examples/iam-policy"
cp -r "$EST" "$EST_ORACLE"
cp -R "$SRC_MODULE" "$WORK/oracle-tree/iam/modules/iam-policy"
sed -i.bak 's/module "iam_policy_from_data_source" {/module "iam_policy_renamed" {/' "$EST_ORACLE/main.tf"
sed -i.bak 's/module "iam_policy" {/module "iam_policy_renamed2" {/' "$EST_ORACLE/main.tf"
sed -i.bak 's/module\.iam_policy\./module.iam_policy_renamed2./g' "$EST_ORACLE/outputs.tf"
rm -f "$EST_ORACLE/main.tf.bak" "$EST_ORACLE/outputs.tf.bak"
cat >> "$EST_ORACLE/main.tf" <<'EOF'

moved {
  from = module.iam_policy_from_data_source
  to   = module.iam_policy_renamed
}

moved {
  from = module.iam_policy
  to   = module.iam_policy_renamed2
}
EOF
( cd "$EST_ORACLE" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$EST_ORACLE" && terraform init -input=false -no-color 2>&1 | tail -20 ); fail "the day2_rename stock oracle's reinit (after renaming both module calls) failed"; }
ORACLE_PLAN_OUT="$(cd "$EST_ORACLE" && terraform plan -input=false -no-color 2>&1)"; ORACLE_PLAN_RC=$?
[ "$ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -40; fail "the day2_rename stock oracle plan exited $ORACLE_PLAN_RC"; }
grep -qE '^  # .+ will be destroyed' <<< "$ORACLE_PLAN_OUT" \
  && { grep -E '^  # .+ will be' <<< "$ORACLE_PLAN_OUT"; fail "stock proposes a destroy for a rename carried entirely by moved blocks - the oracle itself is not zero-churn"; }
grep -qE '^  # .+ will be created' <<< "$ORACLE_PLAN_OUT" \
  && { grep -E '^  # .+ will be' <<< "$ORACLE_PLAN_OUT"; fail "stock proposes a create for a rename carried entirely by moved blocks - the oracle itself is not zero-churn"; }
grep -qE '^  # module\.iam_policy_from_data_source\.aws_iam_policy\.policy\[0\] has moved to module\.iam_policy_renamed\.aws_iam_policy\.policy\[0\]' <<< "$ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -40; fail "stock's plan does not report the data-source policy's move"; }
grep -qE '^  # module\.iam_policy\.aws_iam_policy\.policy\[0\] has moved to module\.iam_policy_renamed2\.aws_iam_policy\.policy\[0\]' <<< "$ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -40; fail "stock's plan does not report the name_prefix policy's move"; }
grep -qF 'Plan: 0 to add, 0 to change, 0 to destroy.' <<< "$ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -10; fail "stock's rename plan is not a true no-op"; }
log "  stock: zero churn, no attribute diff at all - both policies report only their move, on the state cold_deploy produced"

# day2_remove's stock oracle (live/GAUNTLET.md #7, issue #358 - a gauntlet
# evidence unit, planned stage, does not count toward "clear"): "Stock with
# the same block removed plans the same destroys." Another SEPARATE copy of
# cold_deploy's own state, so this destroy has nothing to do with the rename
# this script also exercises. Removes module.iam_policy_from_data_source's
# block entirely - outputs.tf references only module.iam_policy, so no
# other edit is needed. Same directory-naming note as STAGE 1.5 above: the
# oracle tree's leaf directory is named "iam-policy" so local.name's
# "ex-${basename(path.cwd)}" tag matches the real run and this plan shows
# only the removal, nothing else.
CURRENT_STAGE=day2_remove
log "=== STAGE 1.5.5: day2_remove stock oracle: delete module.iam_policy_from_data_source's block on cold_deploy's own state ==="
mkdir -p "$WORK/oracle-remove-tree/iam/examples" "$WORK/oracle-remove-tree/iam/modules"
EST_ORACLE_REMOVE="$WORK/oracle-remove-tree/iam/examples/iam-policy"
cp -r "$EST" "$EST_ORACLE_REMOVE"
cp -R "$SRC_MODULE" "$WORK/oracle-remove-tree/iam/modules/iam-policy"
perl -0pi -e 's/module "iam_policy_from_data_source" \{.*?\n\}\n\n//s' "$EST_ORACLE_REMOVE/main.tf"
grep -q 'module "iam_policy_from_data_source"' "$EST_ORACLE_REMOVE/main.tf" \
  && fail "removing module.iam_policy_from_data_source's block from the oracle copy did not match - the corpus example has moved"
( cd "$EST_ORACLE_REMOVE" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$EST_ORACLE_REMOVE" && terraform init -input=false -no-color 2>&1 | tail -20 ); fail "the day2_remove stock oracle's reinit (after removing the block) failed"; }
REMOVE_ORACLE_PLAN_OUT="$(cd "$EST_ORACLE_REMOVE" && terraform plan -input=false -no-color 2>&1)"; REMOVE_ORACLE_PLAN_RC=$?
[ "$REMOVE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_remove stock oracle plan exited $REMOVE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.iam_policy_from_data_source\.aws_iam_policy\.policy\[0\] will be destroyed' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "stock does not propose destroying module.iam_policy_from_data_source's policy when its block is removed"; }
grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -10; fail "stock's remove plan proposes something other than exactly one destroy"; }
log "  stock: exactly one destroy (module.iam_policy_from_data_source's policy), nothing else, on the state cold_deploy produced"
CURRENT_STAGE=""
log ""

# day2_replace's stock oracle (live/GAUNTLET.md #9): "Stock's replace of the
# same resource leaves the same single object." Another SEPARATE copy of
# cold_deploy's own state, so this replace has nothing to do with the
# rename/remove ones above. Targets module.iam_policy (the name_prefix
# policy) - changing `name_prefix` is a real, upstream-declared ForceNew
# argument on aws_iam_policy (IAM has no rename-policy API; confirmed
# already by corpus-giantswarm-crossplane's own F-ORACLE for the plain
# `name` argument on the same type - `name_prefix` reaches the identical
# provider field once the random suffix is appended). No dependent
# resources reference this policy (unlike Labelbox/harbor's IAM
# role+policy pairs), so this is a clean, single-resource replace with no
# cascade. Same directory-naming note as STAGE 1.5/1.5.5 above.
CURRENT_STAGE=day2_replace
log "=== STAGE 1.5.6: day2_replace stock oracle: force-replace module.iam_policy's policy via its ForceNew name_prefix argument, on cold_deploy's own state ==="
mkdir -p "$WORK/oracle-replace-tree/iam/examples" "$WORK/oracle-replace-tree/iam/modules"
EST_ORACLE_REPLACE="$WORK/oracle-replace-tree/iam/examples/iam-policy"
cp -r "$EST" "$EST_ORACLE_REPLACE"
cp -R "$SRC_MODULE" "$WORK/oracle-replace-tree/iam/modules/iam-policy"
sed -i.bak 's/name_prefix = "example-"/name_prefix = "example-v2-"/' "$EST_ORACLE_REPLACE/main.tf"
rm -f "$EST_ORACLE_REPLACE/main.tf.bak"
grep -q 'name_prefix = "example-v2-"' "$EST_ORACLE_REPLACE/main.tf" \
  || fail "changing module.iam_policy's name_prefix argument in the replace-oracle copy did not match - the corpus pin has moved"
( cd "$EST_ORACLE_REPLACE" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$EST_ORACLE_REPLACE" && terraform init -input=false -no-color 2>&1 | tail -20 ); fail "the day2_replace stock oracle's reinit failed"; }
REPLACE_ORACLE_PLAN_OUT="$(cd "$EST_ORACLE_REPLACE" && terraform plan -input=false -no-color 2>&1)"; REPLACE_ORACLE_PLAN_RC=$?
[ "$REPLACE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_replace stock oracle plan exited $REPLACE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.iam_policy\.aws_iam_policy\.policy\[0\] must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "stock does not propose replacing module.iam_policy's policy when name_prefix changes"; }
grep -qF 'Plan: 1 to add, 0 to change, 1 to destroy.' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -10; fail "stock's replace plan proposes something other than exactly one add and one destroy at the same address"; }
log "  stock: exactly one replace proposed (destroy the old example-* policy, create a new example-v2-* one) at the same declared address, on the state cold_deploy produced - plan only, not applied"
CURRENT_STAGE=""
log ""


# ══════════════════════════════════════════════════════════════════════════
# PART GREENFIELD (greenfield, live/GAUNTLET.md #13, active stage)
# ══════════════════════════════════════════════════════════════════════════
#
# One more, fresh floci container. STAGE 1's own container ($FLOCI_NAME on
# $FLOCI_PORT) is reused as THIS stage's oracle rather than standing up a
# third one: nothing between cold_deploy and here has applied, changed or
# destroyed anything in it - the day2_rename and day2_remove oracle blocks
# just above only run `terraform plan` against COPIES of cold_deploy's
# state (never `apply`, never against $ENDPOINT directly with a write) - so
# it still holds exactly stock's unmodified, unmarked cold-deploy
# inventory, which is the oracle live/GAUNTLET.md #13 names verbatim ("the
# cloud after stock's cold deploy"). Greenfield applies the identical,
# unmodified example (a live block added, nothing else) into a namespace
# of its own with choudoufu directly - no migration at all.
FLOCI_GREEN_PORT=$((FLOCI_PORT + 1))
FLOCI_GREEN_NAME="choudoufu-corpus-iam-policy-green-$$"
GREEN_ENDPOINT="http://127.0.0.1:${FLOCI_GREEN_PORT}"
GREEN_ESTATE="iam-policy-greenfield"

docker run -d --rm -p "${FLOCI_GREEN_PORT}:4566" --name "$FLOCI_GREEN_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_GREEN_NAME failed"
for _ in $(seq 1 45); do
  GREEN_HEALTH="$(curl -fs "${GREEN_ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"iam"' <<< "${GREEN_HEALTH:-}" && break
  sleep 2
done
grep -q '"iam"' <<< "${GREEN_HEALTH:-}" || fail "the greenfield floci did not come up healthy (iam) at $GREEN_ENDPOINT"
log "  healthy: greenfield=$GREEN_ENDPOINT"

# .corpus is never touched: this copies the SAME pre-migrate tree ($WORK/iam,
# still exactly as step 1's onboarding delta left it - no live block yet)
# out a second time, so the greenfield estate carries the identical
# emulator-flags delta and nothing of STAGE 2's migration.
cp -R "$WORK/iam" "$WORK/iam-greenfield"
rm -rf "$WORK/iam-greenfield/examples/iam-policy/.terraform" \
       "$WORK/iam-greenfield/examples/iam-policy/terraform.tfstate" \
       "$WORK/iam-greenfield/examples/iam-policy/terraform.tfstate.backup" \
       "$WORK/iam-greenfield/examples/iam-policy/.terraform.lock.hcl"
GREEN_EST="$WORK/iam-greenfield/examples/iam-policy"
perl -0pi -e 's/(required_providers \{\n    aws = \{\n      source  = "hashicorp\/aws"\n      version = ">= 6\.28"\n    \}\n  \}\n)\}/$1\n  live {\n    estate = "'"$GREEN_ESTATE"'"\n  }\n}/' "$GREEN_EST/versions.tf"
grep -q "estate = \"$GREEN_ESTATE\"" "$GREEN_EST/versions.tf" || fail "the greenfield live-block delta did not match versions.tf - the corpus pin has moved"

CURRENT_STAGE=greenfield
log "=== PART GREENFIELD 1. choudoufu apply directly, no migration ==="
( cd "$GREEN_EST" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$GREEN_EST" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the greenfield init failed"; }
GREEN_APPLY_OUT="$(cd "$GREEN_EST" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; GREEN_APPLY_RC=$?
[ "$GREEN_APPLY_RC" -eq 0 ] || { printf '%s\n' "$GREEN_APPLY_OUT" | tail -40; fail "the greenfield apply failed"; }
grep -qE 'Apply complete! Resources: 2 added' <<< "$GREEN_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT"; fail "the greenfield apply did not create exactly 2 resources"; }
log "  $(grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT")"

log "=== PART GREENFIELD 2. markers, read through the AWS CLI directly ==="
awslg() { aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" "$@"; }
GREEN_POLICY1_ARN="arn:aws:iam::${ACCOUNT}:policy/example_from_data_source"
GREEN_POLICY2_ARN="$(awslg iam list-policies --path-prefix / \
  --query "Policies[?starts_with(PolicyName, 'example-') == \`true\`].Arn | [0]" --output text)"
[ -n "$GREEN_POLICY2_ARN" ] && [ "$GREEN_POLICY2_ARN" != "None" ] || fail "could not find the greenfield name_prefix policy through the AWS CLI"
GREEN_WANT_ADDR1="module.iam_policy_from_data_source.aws_iam_policy.policy:0"
GREEN_WANT_ADDR2="module.iam_policy.aws_iam_policy.policy:0"
GREEN_ADDR1="$(awslg iam list-policy-tags --policy-arn "$GREEN_POLICY1_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GREEN_ADDR1" = "$GREEN_WANT_ADDR1" ] || fail "$GREEN_POLICY1_ARN carries tofu-address=$GREEN_ADDR1, not $GREEN_WANT_ADDR1"
GREEN_ADDR2="$(awslg iam list-policy-tags --policy-arn "$GREEN_POLICY2_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GREEN_ADDR2" = "$GREEN_WANT_ADDR2" ] || fail "$GREEN_POLICY2_ARN carries tofu-address=$GREEN_ADDR2, not $GREEN_WANT_ADDR2"
GREEN_TAG_EST1="$(awslg iam list-policy-tags --policy-arn "$GREEN_POLICY1_ARN" --query "Tags[?Key=='tofu-estate'].Value | [0]" --output text)"
[ "$GREEN_TAG_EST1" = "$GREEN_ESTATE" ] || fail "$GREEN_POLICY1_ARN carries tofu-estate=$GREEN_TAG_EST1, not $GREEN_ESTATE"
log "  both policies' tofu-address and tofu-estate=$GREEN_ESTATE verified via the AWS CLI, not through choudoufu's own report"

log "=== PART GREENFIELD 3. the local record store holds one record per instance (#364 A2) ==="
GREEN_RECORD_FILES="$(find "$GREEN_EST/.tofu-records/tofu-records" -type f ! -name '*.lock' ! -name '*.tmp-*' 2>/dev/null | wc -l | tr -d ' ')"
[ "$GREEN_RECORD_FILES" = "2" ] || fail "expected 2 records under the local record store after the greenfield apply (one per policy), found $GREEN_RECORD_FILES"
log "  2 records persisted, one per managed instance, read directly off the local record store"

log "=== PART GREENFIELD 4. the next plan proposes nothing ==="
GREEN_PLAN_OUT="$(cd "$GREEN_EST" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; GREEN_PLAN_RC=$?
[ "$GREEN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$GREEN_PLAN_OUT" | tail -30; fail "the greenfield replan exited $GREEN_PLAN_RC"; }
grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$GREEN_PLAN_OUT" \
  && { grep -E '^  # .+ will be' <<< "$GREEN_PLAN_OUT"; fail "the greenfield replan proposes a resource change"; }
log "  no resource change proposed"

log "=== PART GREENFIELD 5. delete the local record store; plan a third time ==="
rm -rf "$GREEN_EST/.tofu-records"
GREEN_PLAN2_OUT="$(cd "$GREEN_EST" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; GREEN_PLAN2_RC=$?
[ "$GREEN_PLAN2_RC" -eq 0 ] || { printf '%s\n' "$GREEN_PLAN2_OUT" | tail -30; fail "the third greenfield plan (no local record store) exited $GREEN_PLAN2_RC"; }
grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$GREEN_PLAN2_OUT" \
  && { grep -E '^  # .+ will be' <<< "$GREEN_PLAN2_OUT"; fail "the third greenfield plan proposes a resource change with no local record store - the objects are not being found by their tags alone"; }
log "  no resource change proposed, with zero local memory of the run that created them"

log "=== PART GREENFIELD 6. object-by-object against stock's own cold-deploy container (STAGE 1, untouched since) ==="
GREEN_POLICY_COUNT_EXPECTED=2
if [ "${BREAK_GREEN:-}" = "1" ]; then
  GREEN_POLICY_COUNT_EXPECTED=1
  log "  BREAK_GREEN=1: dropped one policy from the expected inventory - the count comparison below must fail"
fi
GREEN_POLICY_COUNT_ACTUAL="$(awslg resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$GREEN_ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$GREEN_POLICY_COUNT_ACTUAL" = "$GREEN_POLICY_COUNT_EXPECTED" ] \
  || fail "the greenfield estate has $GREEN_POLICY_COUNT_ACTUAL objects, expected $GREEN_POLICY_COUNT_EXPECTED - the object-by-object comparison against stock's cold deploy must fail on a dropped resource"
GREEN_DOC1="$(awslg iam get-policy-version --policy-arn "$GREEN_POLICY1_ARN" --version-id v1 --query 'PolicyVersion.Document' --output text)"
COLD_DOC1="$(awsl iam get-policy-version --policy-arn "$POLICY1_ARN" --version-id v1 --query 'PolicyVersion.Document' --output text)"
[ -n "$GREEN_DOC1" ] && [ "$GREEN_DOC1" = "$COLD_DOC1" ] || fail "the data-source policy's document differs between the greenfield estate and stock's cold deploy"
GREEN_PATH1="$(awslg iam get-policy --policy-arn "$GREEN_POLICY1_ARN" --query 'Policy.Path' --output text)"
COLD_PATH1="$(awsl iam get-policy --policy-arn "$POLICY1_ARN" --query 'Policy.Path' --output text)"
[ "$GREEN_PATH1" = "$COLD_PATH1" ] || fail "the data-source policy's path differs between the greenfield estate and stock's cold deploy"
GREEN_DOC2="$(awslg iam get-policy-version --policy-arn "$GREEN_POLICY2_ARN" --version-id v1 --query 'PolicyVersion.Document' --output text)"
COLD_DOC2="$(awsl iam get-policy-version --policy-arn "$POLICY2_ARN" --version-id v1 --query 'PolicyVersion.Document' --output text)"
[ -n "$GREEN_DOC2" ] && [ "$GREEN_DOC2" = "$COLD_DOC2" ] || fail "the name_prefix policy's document differs between the greenfield estate and stock's cold deploy"
GREEN_PATH2="$(awslg iam get-policy --policy-arn "$GREEN_POLICY2_ARN" --query 'Policy.Path' --output text)"
COLD_PATH2="$(awsl iam get-policy --policy-arn "$POLICY2_ARN" --query 'Policy.Path' --output text)"
[ "$GREEN_PATH2" = "$COLD_PATH2" ] || fail "the name_prefix policy's path differs between the greenfield estate and stock's cold deploy"
log "  both policies' documents and paths match stock's cold-deploy inventory, object by object, marker tags never compared"

log ""
log "PART GREENFIELD (greenfield): PASS"
gauntlet_stage greenfield pass "2 resources from nothing (both aws_iam_policy), markers verified via the AWS CLI, 2 records in the local record store (#364 A2), replan empty both with and without the local record store, both policies' documents and paths match stock's cold-deploy container (STAGE 1, untouched) object by object, marker tags never compared"
log ""
CURRENT_STAGE=""
docker rm -f "$FLOCI_GREEN_NAME" >/dev/null 2>&1 || true

CURRENT_STAGE=migrate

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE - choudoufu live-import against the cold state, then one
# ordinary apply to converge tofu-slot (see the TOFU-SLOT FINDING above)
# ══════════════════════════════════════════════════════════════════════════
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
fi

# ══════════════════════════════════════════════════════════════════════════
# PART D: RENAME (day2_rename, planned stage - live/GAUNTLET.md #6, issue #357)
# ══════════════════════════════════════════════════════════════════════════
#
# The adopted estate (STAGE 2-5) is still marked and still converged, which
# is exactly the state a rename needs to start from. Two mechanisms, on two
# different module calls so a gap in either is visible: a `moved` block
# renames module.iam_policy_from_data_source (the statically-named policy),
# and "choudoufu live-mv" renames module.iam_policy (the name_prefix one)
# with no moved block at all. The stock oracle for both already ran at
# STAGE 1.5, against the state cold_deploy left before choudoufu ever
# touched these objects.
#
# What this estate found, empirically, on the way to making live-mv work at
# all: iam:ListPolicies drops tags from every listed object the same way
# iam:ListRoles does (internal/live/discovery/bindtags.go's issue #266
# comment names the second, not the first) - live-mv's own sweep
# (internal/live/mv/mv.go) had no fallback to the estate's tag index the
# way an ordinary discovery pass already does, so it could never find
# either policy by its marker. Fixed generically: mv.Request now carries a
# Tagging client the same way live-plan already builds one
# (internal/command/live_mv.go), and sweep() asks
# discovery.JoinMarkerFromTagging - the SAME estate-filtered GetResources
# join discovery.go's own scan already falls back to - before concluding a
# listed object carries no marker at all. Reaches every ClassNeedsDiscovery
# type whose provider List() omits tags; the doc comment on
# internal/live/discovery/bindtags.go already named one (aws_iam_role) and
# this estate is the second (aws_iam_policy), found live rather than
# inferred.
#
# A second, narrower gap surfaced proving this: the estate-wide tag sweep's
# ARN-to-CFN-type join (internal/live/discovery/tagging.go's arnJoinTable)
# had no entry for iam's "policy" ARN segment at all, so a tagged
# aws_iam_policy could never be told apart from an untypeable ARN - "Tagged
# resource's ARN could not be joined to a resource type" on every plan.
# Fixed the same way every other entry in that table is: one curated row,
# `single("AWS::IAM::Policy")` (live/mapping.json's own row for
# aws_iam_policy, not the AWS::IAM::ManagedPolicy alias also recorded
# there).
#
# What that second fix did NOT turn up is a destroy for the BREAK case
# below. Read to the end: internal/live/discovery/discovery.go's
# classifyOrphans withholds a destroy for exactly this shape on purpose,
# and the reason is the same safety principle moved.go documents for the
# alias it will not build - see the BREAK branch's own comment.

CURRENT_STAGE=day2_rename
log "=== D0. capture the two live ARNs a rename must not disturb ==="
D_POLICY1_ARN="$POLICY1_ARN"
D_POLICY2_ARN="$POLICY2_ARN"
log "  $D_POLICY1_ARN (module.iam_policy_from_data_source), $D_POLICY2_ARN (module.iam_policy)"

if [ "${BREAK_RENAME:-}" = "1" ]; then
  log "=== D1 (BREAK_RENAME=1). rename module.iam_policy_from_data_source -> module.iam_policy_renamed WITHOUT a moved block ==="
  sed -i.bak 's/module "iam_policy_from_data_source" {/module "iam_policy_renamed" {/' "$EST/main.tf"
  rm -f "$EST/main.tf.bak"
  ( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the BREAK_RENAME=1 rename's reinit failed"; }
  BREAK_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; BREAK_PLAN_RC=$?
  [ "$BREAK_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -40; fail "the BREAK_RENAME=1 rename-without-moved plan exited $BREAK_PLAN_RC"; }
  grep -qE '^  # module\.iam_policy_renamed\.aws_iam_policy\.policy\[0\] will be created' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK_RENAME=1: renaming without a moved block did not propose creating module.iam_policy_renamed.aws_iam_policy.policy[0] - this stage's check is not load-bearing"; }
  log "  BREAK_RENAME=1: correctly proposes creating module.iam_policy_renamed.aws_iam_policy.policy[0] - zero churn breaks the moment the moved block is missing"
  # No destroy of module.iam_policy_from_data_source's old address is
  # proposed alongside it, and that is not this check failing to be
  # load-bearing: internal/live/discovery/discovery.go's classifyOrphans
  # WITHHOLDS a destroy whenever a declared instance of the SAME type is
  # unbound elsewhere in the estate (here, the create above) - exactly the
  # "possible rename, not a deletion" ambiguity moved.go's own doc comment
  # names for the alias it refuses to build the other way: an alias built
  # wrong destroys a live object stock would have kept. Confirmed by
  # reading discovery.go directly (the "pending[block]" branch,
  # classifyOrphans) rather than assumed; not fixed here, and not this
  # estate's day2_rename claim - the moved-block and live-mv paths below
  # are what the stage proves, and this control's job is only to show that
  # skipping them is not free.
  if grep -qE '^  # module\.iam_policy_from_data_source\.aws_iam_policy\.policy\[0\] will be destroyed' <<< "$BREAK_PLAN_OUT"; then
    log "  BREAK_RENAME=1: choudoufu also proposed the destroy stock would - stronger than expected, not a failure"
  else
    log "  BREAK_RENAME=1: no destroy of the old address is proposed (discovery withholds it as a possible rename - see this part's header comment); the create above is what proves the check load-bearing"
  fi
else
  log "=== D1. choudoufu, moved block: module.iam_policy_from_data_source -> module.iam_policy_renamed ==="
  sed -i.bak 's/module "iam_policy_from_data_source" {/module "iam_policy_renamed" {/' "$EST/main.tf"
  rm -f "$EST/main.tf.bak"
  cat >> "$EST/main.tf" <<'EOF'

moved {
  from = module.iam_policy_from_data_source
  to   = module.iam_policy_renamed
}
EOF
  ( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the moved-block rename's reinit failed"; }
  MOVED_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; MOVED_PLAN_RC=$?
  [ "$MOVED_PLAN_RC" -eq 0 ] || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -40; fail "the moved-block rename plan exited $MOVED_PLAN_RC"; }
  grep -qE '^  # module\.iam_policy_renamed\.aws_iam_policy\.policy\[0\] will be updated in-place' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block plan does not propose an in-place update to module.iam_policy_renamed.aws_iam_policy.policy[0]"; }
  grep -qE '^  # .+ will be (destroyed|created)' <<< "$MOVED_PLAN_OUT" \
    && { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block rename proposes a destroy or a create - not zero churn"; }
  grep -qF 'Plan: 0 to add, 1 to change, 0 to destroy.' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -10; fail "the moved-block rename plan is not exactly one in-place change"; }
  grep -qE '~ +"tofu-address" = "module\.iam_policy_from_data_source\.aws_iam_policy\.policy:0" -> "module\.iam_policy_renamed\.aws_iam_policy\.policy:0"' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT"; fail "the moved-block plan does not show the tofu-address marker being rewritten from the old address to the new one"; }
  log "  choudoufu: zero churn, one in-place tags update - the marker rewrite the moved block completes"

  MOVED_APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; MOVED_APPLY_RC=$?
  [ "$MOVED_APPLY_RC" -eq 0 ] || { printf '%s\n' "$MOVED_APPLY_OUT" | tail -40; fail "the moved-block rename apply exited $MOVED_APPLY_RC"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$MOVED_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$MOVED_APPLY_OUT"; fail "the moved-block rename apply was not exactly one in-place change"; }

  D_POLICY1_ADDR="$(awsl iam list-policy-tags --policy-arn "$D_POLICY1_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$D_POLICY1_ADDR" = "module.iam_policy_renamed.aws_iam_policy.policy:0" ] \
    || fail "$D_POLICY1_ARN carries tofu-address=$D_POLICY1_ADDR after the rename, not module.iam_policy_renamed.aws_iam_policy.policy:0"
  log "  $D_POLICY1_ARN unchanged (same ARN, so the same live policy - not destroyed and recreated), tofu-address now module.iam_policy_renamed.aws_iam_policy.policy:0 - read via the AWS CLI"

  log "=== D2. choudoufu, live-mv: module.iam_policy -> module.iam_policy_renamed2, no moved block at all ==="
  sed -i.bak 's/module "iam_policy" {/module "iam_policy_renamed2" {/' "$EST/main.tf"
  sed -i.bak 's/module\.iam_policy\./module.iam_policy_renamed2./g' "$EST/outputs.tf"
  rm -f "$EST/main.tf.bak" "$EST/outputs.tf.bak"
  ( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the live-mv rename's reinit failed"; }
  MV_OUT="$(cd "$EST" && "$TOFU" live-mv -estate="$ESTATE" 'module.iam_policy.aws_iam_policy.policy[0]' 'module.iam_policy_renamed2.aws_iam_policy.policy[0]' 2>&1)"; MV_RC=$?
  [ "$MV_RC" -eq 0 ] || { printf '%s\n' "$MV_OUT" | tail -40; fail "choudoufu live-mv exited $MV_RC"; }
  grep -qF 'Rewrote the ownership marker on one live resource. This was a cloud write.' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report a real write"; }
  grep -qF '"module.iam_policy.aws_iam_policy.policy:0" -> "module.iam_policy_renamed2.aws_iam_policy.policy:0"' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report rewriting the tofu-address marker from the old address to the new one"; }
  log "  live-mv: $(grep -F 'live ID' <<< "$MV_OUT")"

  D_POLICY2_ADDR="$(awsl iam list-policy-tags --policy-arn "$D_POLICY2_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$D_POLICY2_ADDR" = "module.iam_policy_renamed2.aws_iam_policy.policy:0" ] \
    || fail "$D_POLICY2_ARN carries tofu-address=$D_POLICY2_ADDR after live-mv, not module.iam_policy_renamed2.aws_iam_policy.policy:0"
  log "  $D_POLICY2_ARN unchanged (same ARN - not destroyed and recreated), tofu-address now module.iam_policy_renamed2.aws_iam_policy.policy:0 - read via the AWS CLI"

  log "=== D3. one more plan: config and markers agree on both renames, nothing proposed ==="
  FINAL_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; FINAL_PLAN_RC=$?
  [ "$FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$FINAL_PLAN_OUT" | tail -40; fail "the post-rename plan exited $FINAL_PLAN_RC"; }
  grep -qF "No changes. Your infrastructure matches the configuration." <<< "$FINAL_PLAN_OUT" \
    || { grep -E '^  #' <<< "$FINAL_PLAN_OUT"; fail "the post-rename plan is not empty"; }
  log "  No changes. Both renames are complete and invisible to the next plan."

  log ""
  log "STAGE D (day2_rename): PASS"
  gauntlet_stage day2_rename pass "moved block: module.iam_policy_from_data_source renamed with zero churn (0 add, 1 change, 0 destroy), marker rewritten in place; live-mv: module.iam_policy renamed with zero churn, marker rewritten in place (found and fixed live-mv's own missing issue #266 tag-index fallback and the arnJoinTable's missing iam:policy entry to get here); stock oracle over the same two-module rename on cold_deploy's own state also shows zero churn (0 add, 0 change, 0 destroy); both ARNs unchanged, read via the AWS CLI"

  # ══════════════════════════════════════════════════════════════════════
  # PART F: REPLACE (day2_replace, active stage - live/GAUNTLET.md #9)
  # ══════════════════════════════════════════════════════════════════════
  #
  # Targets module.iam_policy_renamed2 (originally module.iam_policy, the
  # name_prefix policy), never module.iam_policy_renamed - PART E above
  # already removes that one, and outputs.tf references only
  # iam_policy_renamed2, so this section starts from Part D's real,
  # completed state and is otherwise untouched by anything else in this
  # script. `name_prefix` is a real, upstream-declared ForceNew argument on
  # aws_iam_policy (IAM has no rename-policy API - the same fact
  # corpus-giantswarm-crossplane's own PART F establishes for the plain
  # `name` argument on the same type), so changing it forces a replace at
  # the SAME declared address. No other resource references this policy,
  # so this is a clean, single-resource replace with no cascade.
  #
  # THE create_before_destroy SCOPE NOTE (full reasoning in corpus-sqs-
  # basic's own PART F). OpenTofu core rejects a `lifecycle` block on a
  # `module` call, and this corpus's own module source
  # (.corpus/iam/modules/iam-policy) is copied byte-identical rather than
  # patched, so this evidence pass exercises the default
  # destroy-then-create ordering instead. BREAK=replace manufactures the
  # create-before-destroy collision shape directly via the AWS CLI, the
  # same way corpus-sqs-basic's does.
  #
  # aws_iam_policy.policy is declared with `count = var.create ? 1 : 0`
  # (this instance is policy[0]) - a fungible, count-indexed set, the SAME
  # shape corpus-sqs-basic's aws_sqs_queue.this[0] is, not the scalar
  # shape corpus-evoteum-modules/corpus-giantswarm-crossplane/corpus-
  # hongbomiao-harbor's own PART F sections document. A manufactured
  # collision here is a named, hard refusal (see the BREAK=replace branch
  # below) - GitHub issue #411 fixed: entryFor
  # (internal/live/discovery/discovery.go) used to exclude a record-backed
  # count entry from its own index, so a live claimant naming its address
  # fell through to declares()+displacedFrom, which is a no-op for a
  # ClassNeedsDiscovery address - a second live object carrying a duplicate
  # marker was silently dropped: never bound, never an orphan, never a
  # Problem. Root cause and fix reached every ServerAssigned/ARN-identity
  # type under a fungible (count/for_each) set, not aws_iam_policy alone.
  #
  # The refusal's own MESSAGE is not corpus-sqs-basic's "Two live resources
  # claiming one slot" (ProblemDuplicateSlot), even though this is the same
  # count-indexed, record-backed shape: GitHub issue #409, landed on main
  # after #411's own fix (and after this comment first asserted the
  # ProblemDuplicateSlot shape - see git history for that version if this
  # ever needs re-deriving), made bindCountBlock route every count block
  # carrying any record-backed entry through the address path
  # unconditionally, before ever asking whether the live set carries slot
  # tags - so a record-backed block's collision is always "Indistinguishable
  # instances without per-instance markers" (ProblemNeedsSlotMarkers) now,
  # regardless of whether the colliding objects carry matching tofu-slot
  # tags (they still do here; #409 simply never looks). That is #409's own
  # fix working as intended - trusting slot data for a block containing a
  # record-backed entry is exactly the hazard #409 closed - not a gap in
  # #411's. Confirmed against a fresh emulator with both fixes merged, no
  # BREAK-branch code beyond what this file's own BREAK=replace toggles.
  CURRENT_STAGE=day2_replace
  record_key() { printf '%s' "$1" | base64 | tr '+/' '-_' | tr -d '=\n'; }
  record_import_id() { jq -r '.identity.import_id' "$1"; }
  F_ADDR="module.iam_policy_renamed2.aws_iam_policy.policy[0]"
  F_RECORD="$EST/.tofu-records/tofu-records/$ESTATE/aws_iam_policy/$(record_key "$F_ADDR")"

  log "=== F0. capture the live policy and its record ahead of the forced replace ==="
  [ -f "$F_RECORD" ] || fail "no local record file found for $F_ADDR ahead of day2_replace"
  F_OLD_IMPORT_ID="$(record_import_id "$F_RECORD")"
  [ "$F_OLD_IMPORT_ID" = "$D_POLICY2_ARN" ] || fail "the record for $F_ADDR names $F_OLD_IMPORT_ID ahead of day2_replace, not $D_POLICY2_ARN"
  F_OLD_ADDR_TAG="$(awsl iam list-policy-tags --policy-arn "$D_POLICY2_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$F_OLD_ADDR_TAG" = "module.iam_policy_renamed2.aws_iam_policy.policy:0" ] \
    || fail "$D_POLICY2_ARN does not carry tofu-address=module.iam_policy_renamed2.aws_iam_policy.policy:0 ahead of day2_replace"
  log "  $D_POLICY2_ARN, record import_id=$F_OLD_IMPORT_ID, tofu-address=$F_OLD_ADDR_TAG"

  if [ "${BREAK:-}" = "replace" ]; then
    log "=== F1 (BREAK=replace). manufacture the coexistence a skipped destroy would leave behind ==="
    # A second, distinct live policy carrying the SAME tofu-address and
    # tofu-slot as the one a genuine replace would destroy - the state
    # "skip the destroy half" of a create-before-destroy replace would
    # leave, produced directly via the AWS CLI rather than by actually
    # interrupting an apply (day2_crash's own job). Same shape as
    # corpus-sqs-basic's own BREAK=replace; a DIFFERENT message than
    # corpus-sqs-basic's, though, per this section's own header comment
    # (GitHub issue #409's routing, layered on #411's own fix) - "2 live
    # aws_iam_policy resources claim the count instance ...
    # Indistinguishable instances without per-instance markers", not "Two
    # live resources claiming one slot". Before #411's fix this section
    # asserted a THIRD shape (a verified, silent rc=0 "No changes." with the
    # decoy object never mentioned); see git history for either prior
    # version if this ever needs re-deriving.
    BREAK_COLLISION_DOC='{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"ec2:DescribeInstances","Resource":"*"}]}'
    BREAK_COLLISION_ARN="$(awsl iam create-policy --policy-name "example-collision" \
      --policy-document "$BREAK_COLLISION_DOC" \
      --tags "Key=tofu-estate,Value=$ESTATE" "Key=tofu-address,Value=module.iam_policy_renamed2.aws_iam_policy.policy:0" "Key=tofu-slot,Value=0" \
      --query 'Policy.Arn' --output text)"
    [ -n "$BREAK_COLLISION_ARN" ] && [ "$BREAK_COLLISION_ARN" != "None" ] || fail "BREAK=replace: could not create the collision policy"
    BREAK_PLAN_OUT="$(plan_into 2>&1)"; BREAK_PLAN_RC=$?
    awsl iam delete-policy --policy-arn "$BREAK_COLLISION_ARN" >/dev/null 2>&1 || true
    [ "$BREAK_PLAN_RC" -ne 0 ] \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -40; fail "BREAK=replace: the plan succeeded with two live objects claiming the same tofu-address/tofu-slot - it must report the collision, not propose nothing"; }
    grep -qF 'Indistinguishable instances without per-instance markers' <<< "$BREAK_PLAN_OUT" \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -40; fail "BREAK=replace: the plan failed for a reason other than the manufactured collision - this stage's check is not load-bearing"; }
    log "  BREAK=replace: choudoufu correctly refused with a named collision (indistinguishable instances without per-instance markers) rather than silently proposing nothing - GitHub issues #411 and #409 together"
  else
    log "=== F1. choudoufu: change the ForceNew name_prefix argument, forcing a replace at the same declared address ==="
    sed -i.bak 's/name_prefix = "example-"/name_prefix = "example-v2-"/' "$EST/main.tf"
    rm -f "$EST/main.tf.bak"
    grep -q 'name_prefix = "example-v2-"' "$EST/main.tf" || fail "changing module.iam_policy_renamed2's name_prefix argument did not match - the corpus pin has moved"

    F_PLAN_OUT="$(plan_into 2>&1)"; F_PLAN_RC=$?
    [ "$F_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_PLAN_OUT" | tail -40; fail "the day2_replace plan exited $F_PLAN_RC"; }
    grep -qE '^  # module\.iam_policy_renamed2\.aws_iam_policy\.policy\[0\] must be replaced' <<< "$F_PLAN_OUT" \
      || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not propose replacing module.iam_policy_renamed2's policy when its ForceNew name_prefix argument changes"; }
    grep -qF 'Plan: 1 to add, 0 to change, 1 to destroy.' <<< "$F_PLAN_OUT" \
      || { printf '%s\n' "$F_PLAN_OUT" | tail -10; fail "the day2_replace plan is not exactly one add and one destroy at the same address"; }
    log "  choudoufu: exactly one forced replace at the same declared address (module.iam_policy_renamed2.aws_iam_policy.policy[0]), name_prefix forces replacement"

    F_APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; F_APPLY_RC=$?
    [ "$F_APPLY_RC" -eq 0 ] || { printf '%s\n' "$F_APPLY_OUT" | tail -40; fail "the day2_replace apply exited $F_APPLY_RC"; }
    grep -qE 'Resources: 1 added, 0 changed, 1 destroyed' <<< "$F_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$F_APPLY_OUT"; fail "the day2_replace apply was not exactly one add and one destroy"; }

    if F_OLD_STILL="$(awsl iam get-policy --policy-arn "$D_POLICY2_ARN" 2>&1)"; then
      echo "$F_OLD_STILL"; fail "$D_POLICY2_ARN still exists after the replace - the old object was orphaned, not destroyed"
    fi
    grep -qi 'NoSuchEntity' <<< "$F_OLD_STILL" \
      || { echo "$F_OLD_STILL"; fail "get-policy for $D_POLICY2_ARN failed with an unexpected error, not NoSuchEntity - it may still exist"; }
    log "  $D_POLICY2_ARN no longer exists (NoSuchEntity) - confirmed via the AWS CLI, not through choudoufu's own report"

    F_NEW_ARN="$(awsl iam list-policies --path-prefix / \
      --query "Policies[?starts_with(PolicyName, 'example-v2-') == \`true\`].Arn | [0]" --output text)"
    [ -n "$F_NEW_ARN" ] && [ "$F_NEW_ARN" != "None" ] || fail "could not find the replaced name_prefix policy (example-v2-*) through the AWS CLI"
    F_NEW_ADDR_TAG="$(awsl iam list-policy-tags --policy-arn "$F_NEW_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
    [ "$F_NEW_ADDR_TAG" = "module.iam_policy_renamed2.aws_iam_policy.policy:0" ] \
      || fail "$F_NEW_ARN carries tofu-address=$F_NEW_ADDR_TAG after the replace, not module.iam_policy_renamed2.aws_iam_policy.policy:0 - the marker did not move onto the new object"
    log "  $F_NEW_ARN (the new object) carries tofu-address=$F_NEW_ADDR_TAG - the marker moved onto the new object, read via the AWS CLI"

    # THE RECORD STORE, asserted by value (HANDOFF's safety rule; the
    # #398-guard shape: a stale record still naming the destroyed object
    # would be exactly the wrong-marker failure that outranks a missing
    # one). The local record file at the SAME address must now hold the
    # NEW object's ARN, not the one captured in F0.
    F_NEW_IMPORT_ID="$(record_import_id "$F_RECORD")"
    [ "$F_NEW_IMPORT_ID" = "$F_NEW_ARN" ] \
      || fail "the record for $F_ADDR names $F_NEW_IMPORT_ID after the replace, not the new object $F_NEW_ARN - a stale record still claiming the destroyed object, the #398-guard shape"
    [ "$F_NEW_IMPORT_ID" != "$F_OLD_IMPORT_ID" ] \
      || fail "sanity: the record's import_id at $F_ADDR did not change at all across the replace"
    log "  record store: import_id $F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID at the same key ($F_ADDR) - read directly off the local record store file, not through choudoufu's own report"

    log "=== F2. one more plan: config and reality agree, no marker collision ==="
    F_FINAL_PLAN_OUT="$(plan_into 2>&1)"; F_FINAL_PLAN_RC=$?
    [ "$F_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_FINAL_PLAN_OUT" | tail -40; fail "the post-replace plan exited $F_FINAL_PLAN_RC"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$F_FINAL_PLAN_OUT" \
      || { grep -E '^  #' <<< "$F_FINAL_PLAN_OUT"; fail "the post-replace plan is not empty"; }
    log "  No changes. The replace is complete and invisible to the next plan."

    # Nothing downstream in this script reads $D_POLICY2_ARN again - PART E
    # above already ran, and the only remaining stage this affects is this
    # section's own gauntlet_stage report below.
    D_POLICY2_ARN="$F_NEW_ARN"

    gauntlet_stage day2_replace pass "choudoufu: changing module.iam_policy_renamed2's ForceNew name_prefix argument proposed exactly one replace at the same declared address (1 add, 0 change, 1 destroy; -/+ destroy and then create), applied cleanly; the old policy ($F_OLD_IMPORT_ID) is confirmed gone and the new policy ($F_NEW_ARN) carries the marker, both via the AWS CLI; the local record store's record at the same address now names the new object's ARN, not the destroyed one ($F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID); the next plan proposes no resource action; stock oracle on cold_deploy's own state (STAGE 1.5.6) also proposes exactly one replace at the same address (plan only, not applied). Scope note: this exercises OpenTofu's default destroy-then-create ordering, not the create_before_destroy variant the stage's Title names - see this section's own header comment and corpus-sqs-basic's matching one. BREAK=replace's manufactured marker collision IS now reported for this type (GitHub issue #411, fixed): 'Indistinguishable instances without per-instance markers' - GitHub issue #409, layered on top of #411's own fix, is why this is not corpus-sqs-basic's 'Two live resources claiming one slot' text despite the same shape - see PART F's own header and its BREAK=replace branch."
  fi
  CURRENT_STAGE=""
  log ""

  log ""

  # ══════════════════════════════════════════════════════════════════════
  # PART E: REMOVE A BLOCK (day2_remove, planned stage - live/GAUNTLET.md #7,
  # issue #358 - a gauntlet evidence unit; the runner records this verdict
  # but a planned stage does not count toward "clear" until its status is
  # flipped to active in tools/gauntlet/stages.go, a maintainer decision)
  # ══════════════════════════════════════════════════════════════════════
  #
  # Starts from Part D's real, completed state: module.iam_policy_renamed
  # (originally module.iam_policy_from_data_source) is bound and converged,
  # and module.iam_policy_renamed2 (originally module.iam_policy) is bound
  # and converged too. module.iam_policy_renamed is the one removed here -
  # nothing else in the config references it (outputs.tf reads only
  # module.iam_policy_renamed2), so deleting its block, and the now-orphaned
  # "moved" block that pointed at it, needs no other edit.
  #
  # This is also a stronger test of the exact ambiguity issue #357's own
  # comment names as day2_remove's territory than reference-ec2-vpc's Part E
  # is: internal/live/discovery/discovery.go's classifyOrphans keys its
  # "pending" (possible-rename) set by blockKey, which is the resource's
  # type and name with BOTH the instance key AND THE MODULE PATH stripped
  # (blockKey's own doc comment says so). module.iam_policy_renamed's
  # policy and module.iam_policy_renamed2's policy are two DIFFERENT
  # modules but the SAME blockKey ("aws_iam_policy.policy") - exactly the
  # shape that looks, by block key alone, like the same ambiguity a
  # rename-without-a-moved-block produces. The difference that must still
  # tell them apart is whether the surviving same-block-key instance is
  # BOUND already (this case) or UNCLAIMED (the BREAK_RENAME case above):
  # classifyOrphans's "pending" set is built only from res.Unbound, and
  # module.iam_policy_renamed2's policy is bound from Part D, so it is
  # never added to it - the orphaned module.iam_policy_renamed policy
  # should never be withheld. If that reasoning is wrong, the guard right
  # below the plan turns it into an honest, named wall instead of a
  # silently skipped check.
  #
  # BREAK_REMOVE=1 exercises this stage's own Break control instead: keep
  # the block, and assert the plan proposes no destroy for it at all - the
  # Break text in tools/gauntlet/stages.go, verbatim.

  CURRENT_STAGE=day2_remove
  log "=== E0. capture the live ARN one more time ==="
  E_ARN_BEFORE="$(awsl iam list-policy-tags --policy-arn "$D_POLICY1_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text 2>/dev/null || true)"
  [ "$E_ARN_BEFORE" = "module.iam_policy_renamed.aws_iam_policy.policy:0" ] \
    || fail "$D_POLICY1_ARN does not carry tofu-address=module.iam_policy_renamed.aws_iam_policy.policy:0 before day2_remove even starts (got $E_ARN_BEFORE)"

  if [ "${BREAK_REMOVE:-}" = "1" ]; then
    log "=== E1 (BREAK_REMOVE=1). keep module.iam_policy_renamed's block; no destroy may be proposed ==="
    BREAK_REMOVE_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; BREAK_REMOVE_PLAN_RC=$?
    [ "$BREAK_REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_REMOVE_PLAN_OUT" | tail -40; fail "the BREAK_REMOVE=1 kept-block plan exited $BREAK_REMOVE_PLAN_RC"; }
    grep -qE '^  # module\.iam_policy_renamed\.aws_iam_policy\.policy\[0\] will be destroyed' <<< "$BREAK_REMOVE_PLAN_OUT" \
      && { grep -E '^  # .+ will be' <<< "$BREAK_REMOVE_PLAN_OUT"; fail "BREAK_REMOVE=1: a destroy was proposed for module.iam_policy_renamed's policy even though its block is still in the config - this stage's check is not load-bearing"; }
    grep -qE '^  # .+ will be (created|destroyed)' <<< "$BREAK_REMOVE_PLAN_OUT" \
      && { grep -E '^  # .+ will be' <<< "$BREAK_REMOVE_PLAN_OUT"; fail "BREAK_REMOVE=1: some resource action was proposed with the block still in the config"; }
    log "  BREAK_REMOVE=1: correctly proposes no resource action - the block is still declared"
  else
    log "=== E1. choudoufu: delete module.iam_policy_renamed's block ==="
    perl -0pi -e 's/module "iam_policy_renamed" \{.*?\n\}\n\n//s' "$EST/main.tf"
    perl -0pi -e 's/\nmoved \{\n  from = module\.iam_policy_from_data_source\n  to   = module\.iam_policy_renamed\n\}\n//s' "$EST/main.tf"
    grep -q 'module "iam_policy_renamed"' "$EST/main.tf" \
      && fail "removing module.iam_policy_renamed's block did not match - the config has moved"
    ( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the day2_remove reinit failed"; }
    REMOVE_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; REMOVE_PLAN_RC=$?
    [ "$REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -40; fail "the day2_remove plan exited $REMOVE_PLAN_RC"; }
    if grep -q 'is unclaimed, so this may be the same resource under a new instance key' <<< "$REMOVE_PLAN_OUT"; then
      printf '%s\n' "$REMOVE_PLAN_OUT" | tail -40
      fail "choudoufu withheld the destroy of module.iam_policy_renamed's policy as a possible rename (discovery.go's classifyOrphans) even though module.iam_policy_renamed2's same-block-key policy is already bound, not unclaimed - this is the honest wall issue #358 names, not a pass"
    fi
    grep -qE '^  # module\.iam_policy_renamed\.aws_iam_policy\.policy\[0\] will be destroyed' <<< "$REMOVE_PLAN_OUT" \
      || { printf '%s\n' "$REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu does not propose destroying module.iam_policy_renamed's policy when its block is deleted"; }
    grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$REMOVE_PLAN_OUT" \
      || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -10; fail "choudoufu's remove plan proposes something other than exactly one destroy"; }
    log "  choudoufu: exactly one destroy (module.iam_policy_renamed's policy), nothing else"

    REMOVE_APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; REMOVE_APPLY_RC=$?
    [ "$REMOVE_APPLY_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_APPLY_OUT" | tail -40; fail "the day2_remove apply exited $REMOVE_APPLY_RC"; }
    grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$REMOVE_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$REMOVE_APPLY_OUT"; fail "the day2_remove apply was not exactly one destroy"; }

    # IAM, unlike EC2's describe-internet-gateways (checked directly while
    # building reference-ec2-vpc's own Part E), DOES answer get-policy for a
    # deleted ARN with a real NoSuchEntity error and a non-zero exit -
    # confirmed the same way, a standalone create/delete/get-policy sequence
    # against floci with no tofu in the loop at all - so "the AWS CLI call
    # succeeded" is the right test here, not the count-based one EC2 needed.
    if E_STILL="$(awsl iam get-policy --policy-arn "$D_POLICY1_ARN" 2>&1)"; then
      echo "$E_STILL"; fail "$D_POLICY1_ARN still exists in the live account after the destroy - it was orphaned, not destroyed"
    fi
    log "  $D_POLICY1_ARN no longer exists (NoSuchEntity) - confirmed via the AWS CLI, not through choudoufu's own report"

    log "=== E2. one more plan: config and reality agree, nothing left to propose ==="
    E_FINAL_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; E_FINAL_PLAN_RC=$?
    [ "$E_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$E_FINAL_PLAN_OUT" | tail -40; fail "the post-remove plan exited $E_FINAL_PLAN_RC"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$E_FINAL_PLAN_OUT" \
      || { grep -E '^  #' <<< "$E_FINAL_PLAN_OUT"; fail "the post-remove plan is not empty"; }
    log "  No changes. The removal is complete and invisible to the next plan."

    log ""
    log "STAGE E (day2_remove): PASS"
    gauntlet_stage day2_remove pass "choudoufu: deleting module.iam_policy_renamed's block proposed exactly one destroy (0 add, 0 change, 1 destroy), applied cleanly (0 added, 0 changed, 1 destroyed), the object is genuinely gone from the live account (iam get-policy on the old ARN now returns NoSuchEntity, read via the AWS CLI, not choudoufu's own report), and the next plan proposes no resource action; stock oracle on cold_deploy's own state (STAGE 1.5.5) also proposes exactly one destroy for the same object; classifyOrphans did not withhold the destroy even though module.iam_policy_renamed2's policy shares the same block key, because that surviving instance is bound, not unclaimed"
    log ""
  fi
  CURRENT_STAGE=""
fi
CURRENT_STAGE=""
gauntlet_end

log ""
log "=== PASS ==="
log ""
log "A terraform-aws-modules EXAMPLE - the configuration a new user copies"
log "first - crossed through all five stages: cold deploy with plain"
log "terraform, choudoufu live-import adoption plus the tofu-slot"
log "convergence apply it requires, an empty replan with the state file"
log "deleted and both rendered identities checked against IAM's own answer,"
log "a genuine no-op apply, and drift on one policy reconverging without"
log "touching the other. Plus day2_rename (planned): the two module calls"
log "renamed through a moved block and through choudoufu live-mv, zero"
log "churn either way, matched against a stock oracle on cold_deploy's own"
log "state."
