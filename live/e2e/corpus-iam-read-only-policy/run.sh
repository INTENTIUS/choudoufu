#!/usr/bin/env bash
set -uo pipefail

# terraform-aws-modules/terraform-aws-iam's iam-read-only-policy example
# (.corpus/iam/examples/iam-read-only-policy), crossed through choudoufu
# against floci via the real, five-stage pipeline (cold deploy, migrate, test
# plan, test apply, drift and reconverge) issue #274 tracks in
# live/corpus-crossing-manifest.json. A different module than
# corpus-iam-policy already crosses: the iam-read-only-policy module builds
# its policy document from a generated `allowed_services` matrix rather than
# a literal document or a data source, and this example instantiates it
# three times - once creating a policy, once with `create_policy = false`
# (renders the document, creates nothing), once with `create = false` (does
# nothing at all). Only the first module call contributes a resource, and it
# is the ONLY real object this estate creates - a genuine constraint on
# stage 5 below (there is no second live object to independently drift).
#
# This script's predecessor ran a 2-3 stage shape (choudoufu apply from a
# live block present from the start, delete state, replan empty twice); this
# version adds a genuine plain-terraform cold deploy ahead of it and a real
# drift-and-reconverge behind it, following live/e2e/corpus-vpc-complete and
# live/e2e/corpus-lambda-simple's shape - see corpus-iam-policy/run.sh's own
# header for the fuller design discussion this script shares (the tofu-slot
# finding and the outputs quirk both apply here identically).
#
# STAGE-BY-STAGE SHAPE (issue #274's five-stage pipeline; see
# live/corpus-crossing-manifest.json):
#
#   1. COLD DEPLOY   plain `terraform apply` (real HashiCorp terraform, not
#                     choudoufu), no live block anywhere.
#   2. MIGRATE        `choudoufu live-import -state=<cold state> -estate=...
#                     -approve`, the policy's tofu-slot read back off IAM by
#                     value, then ONE ordinary `choudoufu apply` with no
#                     state file present which must be a NO-OP. That apply
#                     used to be the tofu-slot convergence step (the module's
#                     aws_iam_policy declares `count = var.create &&
#                     var.create_policy ? 1 : 0`, the shape that needs a
#                     slot); choudoufu #372 moved the slot write into
#                     live-import itself, so the apply now proves there is
#                     nothing left to converge. See corpus-iam-policy's
#                     header for the whole finding.
#   3. TEST PLAN      delete the state file (already gone), `choudoufu
#                     live-plan`, assert no resource action is proposed *and*
#                     re-assert the rendered identity against a live
#                     aws_iam_policy read through the AWS CLI.
#   4. TEST APPLY     apply that empty plan; assert a genuine no-op and that
#                     the tofu-estate-tagged object count is unchanged.
#   5. DRIFT AND      mutate the one policy's Example tag out of band via the
#      RECONVERGE     AWS CLI, replan, assert the diff proposes fixing
#                     exactly that object (by address, not merely by count -
#                     see BREAK below) and nothing else, then apply and
#                     confirm it reconverged.
#
# THE ONE ONBOARDING DELTA. Same shape as corpus-iam-policy: the example's
# own `version = ">= 6.28"` resolves straight to a current provider with list
# resources intact, and it declares no cloud/backend block to remove. The
# only real edit is the provider block gaining floci's flags.
#
# THE NAME. The module's own `use_name_prefix` defaults to true, so
# `"ex-${basename(path.cwd)}"` - `path.cwd`, one of the four sources
# (var/local/path/terraform) the config-language subset allows to be
# statically evaluable - is used as a PREFIX, and IAM appends its own random
# suffix. The identity is therefore server-assigned, not statically
# derivable from configuration alone. Every stage below reads the ARN IAM
# actually minted rather than predicting one.
#
# WHY STAGE 5's BREAK CONTROL DIFFERS FROM corpus-iam-policy's. That script
# has two real objects, so BREAK=1 there tampers a second one and proves the
# "exactly one object" COUNT assertion is load-bearing. This estate creates
# exactly one real object - there is no second one to tamper. So BREAK=1
# here instead corrupts the ADDRESS the single-object assertion expects (the
# same shape and the same resource type, a module that in fact creates
# nothing), proving that assertion catches a wrong answer rather than merely
# a wrong count. This is the same technique corpus-iam-policy's own stage 3
# identity check and every corpus-* script's BREAK convention already use;
# it is simply the only one available to a single-resource estate's stage 5.
#
#   bash live/e2e/corpus-iam-read-only-policy/run.sh
#
# Needs Docker, the AWS CLI, and the real `terraform` binary on PATH for
# stage 1. .corpus is read, never written: the example AND the module it
# references (preserving the relative path
# `../../modules/iam-read-only-policy`) are copied out to a temp directory
# first, same as every other corpus crossing.
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4699, clear of every
#                other live/e2e fixture's port).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        set to 1 to corrupt the address stage 5's drift assertion
#                expects (see above), proving it is load-bearing.
#
# Exit codes: 0 on a real pass of all five stages, non-zero on a real
# failure. Every assertion reads command output, an exit code, or the
# emulator's own answer through the AWS CLI, never choudoufu's own report of
# itself.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CORPUS_DIR="${CORPUS_DIR:-$ROOT/.corpus}"
SRC_EXAMPLE="$CORPUS_DIR/iam/examples/iam-read-only-policy"
SRC_MODULE="$CORPUS_DIR/iam/modules/iam-read-only-policy"
WORK="$(mktemp -d)"
EST="$WORK/iam/examples/iam-read-only-policy"
FLOCI_PORT="${FLOCI_PORT:-4699}"
FLOCI_NAME="choudoufu-corpus-roiam-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="iam-read-only-policy-crossing"
REGION="eu-west-1"

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

mkdir -p "$WORK/iam/examples" "$WORK/iam/modules"
cp -R "$SRC_EXAMPLE" "$WORK/iam/examples/iam-read-only-policy"
cp -R "$SRC_MODULE" "$WORK/iam/modules/iam-read-only-policy"
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
grep -qE 'Apply complete! Resources: 1 added' <<< "$COLD_OUT" \
  || { grep -E 'Apply complete' <<< "$COLD_OUT"; fail "the cold apply did not create exactly 1 resource"; }
log "  $(grep -E 'Apply complete' <<< "$COLD_OUT")"
[ -f "$EST/terraform.tfstate" ] || fail "plain terraform left no state file to migrate from"

# Read the policy back through the AWS CLI, never through choudoufu. The
# module's own use_name_prefix defaults to true, so the name is
# "ex-${basename(path.cwd)}-" used as a PREFIX - basename of the temp dir
# this script copied the estate into - and IAM appends its own random
# suffix. The policy's identity is therefore server-assigned; the assertion
# below reads the ARN AWS actually minted rather than predicting one.
NAME_PREFIX="ex-$(basename "$EST")-"
POLICY_ARN="$(awsl iam list-policies --path-prefix /example/ \
  --query "Policies[?starts_with(PolicyName, '$NAME_PREFIX') == \`true\`].Arn | [0]" --output text)"
[ -n "$POLICY_ARN" ] && [ "$POLICY_ARN" != "None" ] \
  || fail "could not find a policy named with prefix $NAME_PREFIX through the AWS CLI"
log "  the policy lives: $POLICY_ARN"

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
# PART D-ORACLE: RENAME, stock (day2_rename, active - live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
#
# module.read_only_iam_policy is this estate's only module call that
# contributes a real resource (module.read_only_iam_policy_doc renders a
# policy document and creates nothing; module.read_only_iam_policy_disabled
# does nothing at all - create = false - see this script's header), and
# that one resource, aws_iam_policy.policy[0], is this estate's ONLY live
# object. So both day2_rename mechanisms run on the SAME module, one after
# the other, rather than on two different objects: a `moved` block first
# (module.read_only_iam_policy -> .read_only_iam_policy_moved), then
# "choudoufu live-mv" second (.read_only_iam_policy_moved ->
# .read_only_iam_policy_final, no moved block for that hop at all). The
# stock oracle below plans the NET rename (original name straight to the
# final name) on a copy of cold_deploy's own state, before choudoufu or
# live-import ever touch it. Both main.tf (the module block itself) and
# outputs.tf (five root outputs that all read module.read_only_iam_policy.*)
# need the rename's sed pass, or the estate fails to even validate.
#
# BREAK=2 (not 1: this script's own stage 3 identity check and stage 5
# drift check already corrupt their assertions and exit through fail()
# under BREAK=1 before this point) exercises this stage's own break control
# instead of the real checks: renaming module.read_only_iam_policy WITHOUT
# a moved block, which must make choudoufu propose destroying the old
# address's policy and creating the new one - the opposite of every other
# assertion in this part.
CURRENT_STAGE=day2_rename
log "=== D-ORACLE. stock: the net module rename, through one moved block, on cold_deploy's own state ==="
ORACLE_ROOT="$WORK/oracle"
mkdir -p "$ORACLE_ROOT/iam/examples" "$ORACLE_ROOT/iam/modules"
cp -R "$SRC_EXAMPLE" "$ORACLE_ROOT/iam/examples/iam-read-only-policy"
cp -R "$SRC_MODULE" "$ORACLE_ROOT/iam/modules/iam-read-only-policy"
ORACLE="$ORACLE_ROOT/iam/examples/iam-read-only-policy"
rm -rf "$ORACLE/.terraform" "$ORACLE/.terraform.lock.hcl"
perl -0pi -e 's/(provider "aws" \{\n  region = "eu-west-1"\n)\}/$1\n  access_key                   = "test"\n  secret_key                   = "test"\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  s3_use_path_style            = true\n}/' "$ORACLE/main.tf"
grep -q 's3_use_path_style' "$ORACLE/main.tf" || fail "the day2_rename oracle's emulator delta did not match main.tf"
cp "$WORK/cold.tfstate" "$ORACLE/terraform.tfstate"
( cd "$ORACLE" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ORACLE" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_rename stock oracle's init failed"; }
BASELINE_PLAN_OUT="$(cd "$ORACLE" && terraform plan -input=false -no-color 2>&1)"; BASELINE_PLAN_RC=$?
[ "$BASELINE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BASELINE_PLAN_OUT" | tail -40; fail "the day2_rename stock oracle's baseline (no-rename) plan exited $BASELINE_PLAN_RC"; }
grep -qF 'No changes. Your infrastructure matches the configuration.' <<< "$BASELINE_PLAN_OUT" \
  || { printf '%s\n' "$BASELINE_PLAN_OUT" | tail -20; fail "the baseline (no-rename) oracle plan is not clean - this estate has drifted since the baseline was last measured"; }
log "  baseline (no rename): clean, confirmed BEFORE the rename below"

sed -i.bak 's/module "read_only_iam_policy" {/module "read_only_iam_policy_final" {/' "$ORACLE/main.tf"
sed -i.bak 's/module\.read_only_iam_policy\./module.read_only_iam_policy_final./g' "$ORACLE/outputs.tf"
rm -f "$ORACLE/main.tf.bak" "$ORACLE/outputs.tf.bak"
cat >> "$ORACLE/main.tf" <<'EOF'

moved {
  from = module.read_only_iam_policy.aws_iam_policy.policy[0]
  to   = module.read_only_iam_policy_final.aws_iam_policy.policy[0]
}
EOF
( cd "$ORACLE" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ORACLE" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_rename stock oracle's reinit failed"; }
ORACLE_PLAN_OUT="$(cd "$ORACLE" && terraform plan -input=false -no-color 2>&1)"; ORACLE_PLAN_RC=$?
[ "$ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -40; fail "the day2_rename stock oracle plan exited $ORACLE_PLAN_RC"; }
grep -qE '^  # .+ will be (destroyed|created)' <<< "$ORACLE_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock proposes a destroy or create for a rename carried entirely by a moved block - the oracle itself is not zero-churn"; }
grep -qF 'No changes. Your infrastructure matches the configuration.' <<< "$ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -10; fail "stock's rename plan is not a true no-op"; }
log "  stock: zero churn on cold_deploy's own state - the move reports only its move, no attribute diff at all, outputs unchanged in value"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE - choudoufu live-import against the cold state, the slot
# it now writes read back by value, then one ordinary apply that must be a
# no-op (choudoufu #372)
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=migrate
log "=== STAGE 2: migrate (choudoufu live-import -approve; the following apply must be a no-op) ==="
perl -0pi -e 's/(required_providers \{\n    aws = \{\n      source  = "hashicorp\/aws"\n      version = ">= 6\.28"\n    \}\n  \}\n)\}/$1\n  live {\n    estate = "'"$ESTATE"'"\n  }\n}/' "$EST/versions.tf"
grep -q "estate = \"$ESTATE\"" "$EST/versions.tf" || fail "the live block delta did not match versions.tf - the corpus pin has moved"

( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "choudoufu init failed"; }

rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"

IMPORT_OUT="$(cd "$EST" && "$TOFU" live-import -state="$WORK/cold.tfstate" -estate="$ESTATE" -no-color 2>&1)"; IMPORT_RC=$?
[ "$IMPORT_RC" -eq 0 ] || { printf '%s\n' "$IMPORT_OUT" | tail -40; fail "live-import (dry run) failed"; }
grep -qF "1 of 1 resource instance(s) are eligible for stamping" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import did not verify exactly 1 of 1 resource as eligible - the corpus pin has moved"; }
grep -qF "No tag has been written." <<< "$IMPORT_OUT" || fail "the dry run wrote a tag - it must not"
log "  dry run: 1 of 1 eligible; nothing written yet"

APPROVE_OUT="$(cd "$EST" && "$TOFU" live-import -state="$WORK/cold.tfstate" -estate="$ESTATE" -approve -no-color 2>&1)"; APPROVE_RC=$?
[ "$APPROVE_RC" -eq 0 ] || { printf '%s\n' "$APPROVE_OUT" | tail -40; fail "live-import -approve failed"; }
grep -qF "1 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 0 skipped" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not stamp the 1 resource cleanly"; }
log "  1 stamped"

WANT_ADDR="module.read_only_iam_policy.aws_iam_policy.policy:0"
GOT_ADDR="$(awsl iam list-policy-tags --policy-arn "$POLICY_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_ADDR" = "$WANT_ADDR" ] || fail "$POLICY_ARN carries tofu-address=$GOT_ADDR, not $WANT_ADDR"
log "  marker verified directly against IAM, not through choudoufu's own report: $POLICY_ARN -> tofu-address=$GOT_ADDR"

# The third marker, by value, off the live object - choudoufu #372. The
# module's aws_iam_policy declares count = var.create && var.create_policy
# ? 1 : 0, so it is a count set of one and must carry tofu-slot = "0" the
# moment live-import returns. This used to be what the apply below wrote;
# see corpus-iam-policy/run.sh's header, "THE TOFU-SLOT FINDING", for the
# whole of what changed and why the assignment is not a guess.
WANT_SLOT="0"
GOT_SLOT="$(awsl iam list-policy-tags --policy-arn "$POLICY_ARN" --query "Tags[?Key=='tofu-slot'].Value | [0]" --output text)"
[ "$GOT_SLOT" = "$WANT_SLOT" ] || fail "$POLICY_ARN carries tofu-slot=$GOT_SLOT, not $WANT_SLOT - live-import did not settle the slot for a slotless count set (choudoufu #372)"
log "  slot verified the same way: $POLICY_ARN -> tofu-slot=$GOT_SLOT"

# What used to be the tofu-slot convergence apply, kept as #372's regression
# guard: with the slot written at migrate time there is nothing left to
# converge, so this apply has to be a genuine no-op. If the slot write
# regresses it reads "0 added, 1 changed, 0 destroyed" again and fails here.
CONVERGE_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; CONVERGE_RC=$?
[ "$CONVERGE_RC" -eq 0 ] || { printf '%s\n' "$CONVERGE_OUT" | tail -40; fail "the post-migration apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$CONVERGE_OUT" \
  || { grep -E 'Apply complete' <<< "$CONVERGE_OUT"; grep -E '^  # .+ will be' <<< "$CONVERGE_OUT"; fail "the apply straight after live-import was not a no-op - the migration left something for a plan to finish (choudoufu #372 is about exactly this)"; }
log "  $(grep -E 'Apply complete' <<< "$CONVERGE_OUT") (nothing left to converge)"
[ ! -f "$EST/terraform.tfstate" ] || fail "the post-migration apply wrote a state file"

log ""
log "STAGE 2 (migrate): PASS"
gauntlet_stage migrate pass "1 of 1 stamped, carrying tofu-slot=$GOT_SLOT read back through IAM (choudoufu #372); $(grep -E 'Apply complete' <<< "$CONVERGE_OUT") - nothing left to converge"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - state deleted (already true), live-plan empty,
# identity re-asserted
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=test_plan
log "=== STAGE 3: test plan (live-plan empty, identity re-checked) ==="
[ ! -f "$EST/terraform.tfstate" ] || fail "a state file exists ahead of stage 3"

plan_into() { ( cd "$EST" && "$TOFU" live-plan -input=false -no-color ); }
PLAN_OUT="$(plan_into 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -60; fail "live-plan exited $PLAN_RC"; }
[ ! -f "$EST/terraform.tfstate" ] || fail "live-plan wrote a state file"
# Not a "No changes."/"Plan:" grep - see the header comment about root
# outputs, same quirk corpus-iam-policy documents.
grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$PLAN_OUT" \
  && { grep -E '^  # .+ will be' <<< "$PLAN_OUT"; fail "the plan proposes a resource change"; }
grep -qE '^Foreign resources: (none|nothing was swept)' <<< "$PLAN_OUT" \
  || { grep -E '^Foreign resources:' <<< "$PLAN_OUT"; fail "the plan reports foreign resources"; }
log "  no resource change proposed; nothing foreign"

WANT_ADDR2="$WANT_ADDR"
if [ "${BREAK:-}" = "1" ]; then
  WANT_ADDR2="module.read_only_iam_policy_doc.aws_iam_policy.policy:0"
  log "  BREAK=1: expecting tofu-address=$WANT_ADDR2 - the SAME shape and the"
  log "           SAME resource type, but a module call (create_policy=false)"
  log "           that in fact creates nothing. This step must fail."
fi
GOT_ADDR2="$(awsl iam list-policy-tags --policy-arn "$POLICY_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_ADDR2" = "$WANT_ADDR2" ] || fail "$POLICY_ARN's tofu-address is $GOT_ADDR2, not $WANT_ADDR2"
log "  identity re-check (read via the AWS CLI, after the state file has never existed this run): unchanged"

log ""
log "STAGE 3 (test plan): PASS"
gauntlet_stage test_plan pass "no resource change proposed, nothing foreign; identity re-check (via the AWS CLI) unchanged"
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
# STAGE 5: DRIFT AND RECONVERGE - mutate the one object, replan, assert the
# fix is proposed against the right address (see the header comment on why
# this differs from corpus-iam-policy's two-object BREAK control)
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=drift_reconverge
log "=== STAGE 5: drift and reconverge (mutate the one object out of band) ==="
awsl iam tag-policy --policy-arn "$POLICY_ARN" --tags Key=Example,Value=tampered-out-of-band
DRIFTED_VALUE="$(awsl iam list-policy-tags --policy-arn "$POLICY_ARN" --query "Tags[?Key=='Example'].Value | [0]" --output text)"
[ "$DRIFTED_VALUE" = "tampered-out-of-band" ] || fail "the out-of-band tag mutation did not take"
log "  mutated $POLICY_ARN's Example tag to \"tampered-out-of-band\" directly via the AWS CLI"

DRIFT_PLAN_OUT="$(plan_into 2>&1)"; DRIFT_PLAN_RC=$?
[ "$DRIFT_PLAN_RC" -eq 0 ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | tail -60; fail "the drift-detection plan exited $DRIFT_PLAN_RC"; }

CHANGED_ADDRS="$(grep -oE '^  # \S+ will be updated' <<< "$DRIFT_PLAN_OUT" | awk '{print $2}' | sort -u)"
N_CHANGED="$(printf '%s\n' "$CHANGED_ADDRS" | grep -c . || true)"
[ "$N_CHANGED" = "1" ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | grep -E '^  # .+ will be'; fail "expected exactly 1 object proposed for a fix, got $N_CHANGED"; }

# The literal tag VALUE stamped on a count/for_each instance escapes "[0]"
# to ":0" (live/MARKERS.md's escaping rule) - that is WANT_ADDR above, read
# via the AWS CLI. A live-plan diff's own "# addr will be updated in-place"
# header renders the ordinary, unescaped Terraform resource address instead
# ("[0]", not ":0") - CHANGED_ADDRS above came from exactly that header, so
# the comparison here needs the same bracket form, not WANT_ADDR's colon form.
WANT_DRIFT_ADDR="module.read_only_iam_policy.aws_iam_policy.policy[0]"
if [ "${BREAK:-}" = "1" ]; then
  WANT_DRIFT_ADDR="module.read_only_iam_policy_doc.aws_iam_policy.policy[0]"
  log "  BREAK=1: expecting the plan to propose fixing $WANT_DRIFT_ADDR - the"
  log "           SAME shape and the SAME resource type, but a module call"
  log "           that in fact creates nothing. This step must fail."
fi
[ "$CHANGED_ADDRS" = "$WANT_DRIFT_ADDR" ] || fail "the plan proposes fixing $CHANGED_ADDRS, not $WANT_DRIFT_ADDR"
log "  the plan proposes fixing exactly the right object: $CHANGED_ADDRS"

RECONVERGE_APPLY="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; RECONVERGE_RC=$?
[ "$RECONVERGE_RC" -eq 0 ] || { printf '%s\n' "$RECONVERGE_APPLY" | tail -40; fail "the reconverge apply failed"; }
grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$RECONVERGE_APPLY" \
  || { grep -E 'Apply complete' <<< "$RECONVERGE_APPLY"; fail "the reconverge apply did not change exactly 1 resource"; }
FIXED_VALUE="$(awsl iam list-policy-tags --policy-arn "$POLICY_ARN" --query "Tags[?Key=='Example'].Value | [0]" --output text)"
[ "$FIXED_VALUE" = "ex-iam-read-only-policy" ] || fail "$POLICY_ARN's Example tag is \"$FIXED_VALUE\" after reconverging, not \"ex-iam-read-only-policy\""
log "  reconverged: $POLICY_ARN's Example tag is back to \"ex-iam-read-only-policy\""

log ""
log "STAGE 5 (drift and reconverge): PASS"
gauntlet_stage drift_reconverge pass "one object tampered ($POLICY_ARN's Example tag), plan proposed fixing exactly $CHANGED_ADDRS, apply changed 1 and reconverged the tag"
log ""

# ══════════════════════════════════════════════════════════════════════════
# PART D: RENAME (day2_rename, active - live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
#
# See the D-ORACLE comment above stage 2 for why both mechanisms run on the
# SAME module. The adopted estate (stages 2-5) is still marked and still
# converged, which is exactly the state a rename needs to start from.
CURRENT_STAGE=day2_rename
log "=== D0. capture the live object this rename must not disturb ==="
log "  $POLICY_ARN (module.read_only_iam_policy.aws_iam_policy.policy[0])"

if [ "${BREAK:-}" = "2" ]; then
  log "=== D1 (BREAK=2). rename module.read_only_iam_policy -> module.read_only_iam_policy_final WITHOUT a moved block ==="
  sed -i.bak 's/module "read_only_iam_policy" {/module "read_only_iam_policy_final" {/' "$EST/main.tf"
  sed -i.bak 's/module\.read_only_iam_policy\./module.read_only_iam_policy_final./g' "$EST/outputs.tf"
  rm -f "$EST/main.tf.bak" "$EST/outputs.tf.bak"
  ( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the BREAK=2 rename's reinit failed"; }
  BREAK_PLAN_OUT="$(plan_into 2>&1)"; BREAK_PLAN_RC=$?
  [ "$BREAK_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -30; fail "the BREAK=2 rename-without-moved plan exited $BREAK_PLAN_RC"; }
  grep -qE '^  # module\.read_only_iam_policy\.aws_iam_policy\.policy\[0\] will be destroyed' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=2: renaming without a moved block did not propose destroying module.read_only_iam_policy.aws_iam_policy.policy[0] - this stage's check is not load-bearing"; }
  grep -qE '^  # module\.read_only_iam_policy_final\.aws_iam_policy\.policy\[0\] will be created' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=2: renaming without a moved block did not propose creating module.read_only_iam_policy_final.aws_iam_policy.policy[0] - this stage's check is not load-bearing"; }
  log "  BREAK=2: correctly proposes destroying module.read_only_iam_policy.aws_iam_policy.policy[0] and creating module.read_only_iam_policy_final.aws_iam_policy.policy[0] - the moved-block and live-mv checks below are skipped"
else
  log "=== D1. choudoufu, moved block: module.read_only_iam_policy -> module.read_only_iam_policy_moved ==="
  sed -i.bak 's/module "read_only_iam_policy" {/module "read_only_iam_policy_moved" {/' "$EST/main.tf"
  sed -i.bak 's/module\.read_only_iam_policy\./module.read_only_iam_policy_moved./g' "$EST/outputs.tf"
  rm -f "$EST/main.tf.bak" "$EST/outputs.tf.bak"
  cat >> "$EST/main.tf" <<'EOF'

moved {
  from = module.read_only_iam_policy.aws_iam_policy.policy[0]
  to   = module.read_only_iam_policy_moved.aws_iam_policy.policy[0]
}
EOF
  ( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the moved-block rename's reinit failed"; }
  MOVED_PLAN_OUT="$(plan_into 2>&1)"; MOVED_PLAN_RC=$?
  [ "$MOVED_PLAN_RC" -eq 0 ] || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -40; fail "the moved-block rename plan exited $MOVED_PLAN_RC"; }
  grep -qE '^  # .+ will be (destroyed|created)' <<< "$MOVED_PLAN_OUT" \
    && { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block rename proposes a destroy or a create - not zero churn"; }
  grep -qE '^  # module\.read_only_iam_policy_moved\.aws_iam_policy\.policy\[0\] will be updated in-place' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block plan does not propose an in-place update to module.read_only_iam_policy_moved.aws_iam_policy.policy[0]"; }
  grep -qF 'Plan: 0 to add, 1 to change, 0 to destroy.' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -10; fail "the moved-block rename plan is not exactly one in-place change"; }
  grep -qE '~ +"tofu-address" += +"module\.read_only_iam_policy\.aws_iam_policy\.policy:0" +-> +"module\.read_only_iam_policy_moved\.aws_iam_policy\.policy:0"' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT"; fail "the moved-block plan does not show the policy's tofu-address marker being rewritten from the old address to the new one"; }
  log "  choudoufu: zero churn, one in-place tags update - the marker rewrite the moved block completes"

  MOVED_APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; MOVED_APPLY_RC=$?
  [ "$MOVED_APPLY_RC" -eq 0 ] || { printf '%s\n' "$MOVED_APPLY_OUT" | tail -40; fail "the moved-block rename apply exited $MOVED_APPLY_RC"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$MOVED_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$MOVED_APPLY_OUT"; fail "the moved-block rename apply was not exactly one in-place change"; }

  POLICY_ARN_D1_AFTER="$(awsl iam list-policies --path-prefix /example/ --query "Policies[?starts_with(PolicyName, '$NAME_PREFIX') == \`true\`].Arn | [0]" --output text)"
  [ "$POLICY_ARN_D1_AFTER" = "$POLICY_ARN" ] || fail "the policy's ARN changed across the rename ($POLICY_ARN -> $POLICY_ARN_D1_AFTER) - it was destroyed and recreated, not renamed"
  ADDR_D1="$(awsl iam list-policy-tags --policy-arn "$POLICY_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$ADDR_D1" = "module.read_only_iam_policy_moved.aws_iam_policy.policy:0" ] \
    || fail "the policy carries tofu-address=$ADDR_D1 after the rename, not module.read_only_iam_policy_moved.aws_iam_policy.policy:0"
  log "  $POLICY_ARN unchanged, tofu-address now module.read_only_iam_policy_moved.aws_iam_policy.policy:0 - read via the AWS CLI"

  log "=== D2. choudoufu, live-mv: module.read_only_iam_policy_moved -> module.read_only_iam_policy_final, no moved block at all ==="
  sed -i.bak 's/module "read_only_iam_policy_moved" {/module "read_only_iam_policy_final" {/' "$EST/main.tf"
  sed -i.bak 's/module\.read_only_iam_policy_moved\./module.read_only_iam_policy_final./g' "$EST/outputs.tf"
  rm -f "$EST/main.tf.bak" "$EST/outputs.tf.bak"
  ( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the live-mv rename's reinit failed"; }
  MV_OUT="$(cd "$EST" && "$TOFU" live-mv -estate="$ESTATE" 'module.read_only_iam_policy_moved.aws_iam_policy.policy[0]' 'module.read_only_iam_policy_final.aws_iam_policy.policy[0]' 2>&1)"; MV_RC=$?
  [ "$MV_RC" -eq 0 ] || { printf '%s\n' "$MV_OUT" | tail -30; fail "choudoufu live-mv exited $MV_RC"; }
  grep -qF 'Rewrote the ownership marker on one live resource. This was a cloud write.' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report a real write"; }
  grep -qF '"module.read_only_iam_policy_moved.aws_iam_policy.policy:0" -> "module.read_only_iam_policy_final.aws_iam_policy.policy:0"' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report rewriting the tofu-address marker from the old address to the new one"; }
  log "  live-mv: $(grep -F 'live ID' <<< "$MV_OUT")"

  POLICY_ARN_D2_AFTER="$(awsl iam list-policies --path-prefix /example/ --query "Policies[?starts_with(PolicyName, '$NAME_PREFIX') == \`true\`].Arn | [0]" --output text)"
  [ "$POLICY_ARN_D2_AFTER" = "$POLICY_ARN" ] || fail "the policy's ARN changed across live-mv ($POLICY_ARN -> $POLICY_ARN_D2_AFTER) - it was destroyed and recreated, not renamed"
  ADDR_D2="$(awsl iam list-policy-tags --policy-arn "$POLICY_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$ADDR_D2" = "module.read_only_iam_policy_final.aws_iam_policy.policy:0" ] \
    || fail "the policy carries tofu-address=$ADDR_D2 after live-mv, not module.read_only_iam_policy_final.aws_iam_policy.policy:0"
  log "  $POLICY_ARN unchanged, tofu-address now module.read_only_iam_policy_final.aws_iam_policy.policy:0 - read via the AWS CLI"

  log "=== D3. one more plan: config and marker agree on both renames, nothing proposed ==="
  FINAL_PLAN_OUT="$(plan_into 2>&1)"; FINAL_PLAN_RC=$?
  [ "$FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$FINAL_PLAN_OUT" | tail -40; fail "the post-rename plan exited $FINAL_PLAN_RC"; }
  grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$FINAL_PLAN_OUT" \
    && { grep -E '^  # .+ will be' <<< "$FINAL_PLAN_OUT"; fail "the post-rename plan proposes a resource change"; }
  log "  no resource change proposed (this estate's outputs quirk means a permanent Changes-to-Outputs section is expected here too - see the header - so the check is the absence of a resource-action header, not a summary line). Both renames are complete and invisible to the next plan."

  gauntlet_stage day2_rename pass "moved block: module.read_only_iam_policy renamed to module.read_only_iam_policy_moved with zero churn (0 add, 1 change, 0 destroy), tofu-address marker rewritten in place; live-mv: module.read_only_iam_policy_moved renamed to module.read_only_iam_policy_final with zero churn, marker rewritten in place; stock oracle over the identical net rename on cold_deploy's own state also shows a true no-op (0 add, 0 change, 0 destroy, outputs unchanged in value); the live policy ARN unchanged throughout, read via the AWS CLI"
fi

CURRENT_STAGE=""
gauntlet_end

log "=== PASS ==="
log ""
log "A terraform-aws-modules EXAMPLE using a different iam module than"
log "corpus-iam-policy - one that builds its policy from a generated"
log "allowed_services matrix, instantiated three times with only one call"
log "contributing a resource - crossed through all five stages: cold deploy"
log "with plain terraform, choudoufu live-import adoption - which now writes"
log "the tofu-slot too, so the apply behind it is a no-op - an empty replan"
log "with the"
log "state file deleted and the rendered identity checked against IAM's own"
log "answer, a genuine no-op apply, and drift on the one policy reconverging"
log "and proposing a fix against the right address."
