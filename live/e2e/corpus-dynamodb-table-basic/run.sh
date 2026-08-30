#!/usr/bin/env bash
set -uo pipefail

# terraform-aws-modules/terraform-aws-dynamodb-table's "basic" example
# (.corpus/dynamodb-table/examples/basic), crossed through choudoufu against
# floci via the real, five-stage pipeline (cold deploy, migrate, test plan,
# test apply, drift and reconverge) - see live/corpus-crossing-manifest.json
# and HANDOFF.md's "The loop". terraform-aws-dynamodb-table is one of the
# single most-downloaded modules in the terraform-aws-modules org (35.6M
# downloads on the Terraform Registry as of 2026-08-20, ahead of every
# already-crossed module here except KMS) and had never been crossed before
# this script. Pinned at tag v5.5.1, commit
# 02b2d66ad2396389381c8dbe3423682114ed5350 (live/corpus-manifest.json).
#
# The estate: one DynamoDB table (module.dynamodb_table) with a GSI, a
# resource-based policy, and a name derived from a shared `random_pet`
# resource - plus a second module instantiation (module.disabled_dynamodb_
# table, create_table = false) that contributes nothing. Three real
# resources total: random_pet.this, module.dynamodb_table.aws_dynamodb_
# table.this[0], module.dynamodb_table.aws_dynamodb_resource_policy.this[0].
#
# STAGE-BY-STAGE SHAPE (HANDOFF.md's five-stage pipeline):
#
#   1. COLD DEPLOY   plain `terraform apply` (real HashiCorp terraform, not
#                     choudoufu), no live block anywhere.
#   2. MIGRATE        `choudoufu live-import -state=<cold state> -estate=...
#                     -approve` against the cold state, then one ordinary
#                     `choudoufu apply` to converge tofu-slot (both real
#                     resources are `count`-shaped, same finding as
#                     live/e2e/corpus-iam-policy's "THE TOFU-SLOT FINDING").
#   3. TEST PLAN      delete the state file, `choudoufu live-plan`, assert
#                     the plan is EMPTY *and* assert the rendered identity
#                     strings against the AWS CLI's own answer.
#   4. TEST APPLY     apply that empty plan; assert a genuine no-op.
#   5. DRIFT AND      mutate the table's tags out of band via the AWS CLI
#      RECONVERGE     directly against floci, replan, assert the diff
#                     proposes fixing exactly that one object, reconverge.
#
# THE RANDOM_PET GAP, and why this script pins it rather than exercising
# #340's fix directly. `module.dynamodb_table`'s table name is `"my-table-
# ${random_pet.this.id}"` - the exact idiom live/e2e/corpus-s3-bucket-
# complete's header calls "DELTA 3" and issue #340 (fixed 2026-08-20,
# HANDOFF.md "-3.") addresses on the MIGRATE side: live-import now seeds the
# record store for a record-backed instance like random_pet, so stage 2
# alone no longer loses the value. But #340's own header names a SECOND,
# still-open wall (issue #314's shape, reproduced live on corpus-lambda-
# simple 2026-08-20): a TAGGED resource's identity-bearing argument that is
# itself computed from a record-backed value ("Non-static identity
# argument" / "Unresolvable identity") is refused by the identity resolver
# even though the record store holds the value and the resource is already
# stamped. `aws_dynamodb_table.this`'s own `name` argument - its identity,
# via the TABLENAME import syntax - is exactly this shape:
# `var.name = "my-table-${random_pet.this.id}"`. This script verified that
# wall reproduces here too (see below) before falling back to the same
# DELTA 3 pin live/e2e/corpus-s3-bucket-complete uses: the already-applied
# pet value is substituted as a literal ahead of stage 2, standing in for
# what resolving an identity argument through a record would do generically.
# This is real, tracked product debt (#314), not something this script
# hides - it is called out here and the estate still crosses honestly with
# the substitution in place, the same way s3-bucket-complete does.
#
# BLOCKED ON A REAL FLOCI GAP, not a choudoufu bug: lex00/floci#86. Stage 1
# (cold deploy) is a plain, unmodified `terraform apply` - real HashiCorp
# terraform, no choudoufu anywhere in the loop yet - and it fails outright
# against floci, because `aws_dynamodb_resource_policy.this[0]` (the
# resource-based policy this example's `resource_policy` argument creates,
# main.tf:440-446 in the module) calls `PutResourcePolicy` on create, and
# floci's `DynamoDbJsonHandler` has no case for `PutResourcePolicy`,
# `GetResourcePolicy`, or `DeleteResourcePolicy` at all - confirmed by
# reading the source (zero occurrences of "ResourcePolicy" in the DynamoDB
# service) and by calling `PutResourcePolicy` directly against a running
# floci container with the AWS CLI, bypassing choudoufu entirely:
# `UnknownOperationException: Operation PutResourcePolicy is not supported.`
# This is not any of HANDOFF.md's three parity labels cleanly - it is not
# "OpenTofu fails here too" (stock succeeds fine against real AWS), not
# "OpenTofu succeeds, choudoufu refuses" (choudoufu is never even reached),
# and not "OpenTofu was never asked this question" either. It is an emulator
# gap blocking measurement before choudoufu enters the picture at all: stage
# 1 cannot complete, so nothing past it - migrate, test plan, test apply,
# drift/reconverge - has been run, and this script does not claim otherwise.
# Out of scope to fix here; tracked at lex00/floci#86, not folded into this
# estate's own product debt (#314 above).
#
# UPDATE 2026-08-21: lex00/floci#86 is fixed (build e61a987, both PutWarmPool
# and the ResourcePolicy trio implemented). Stage 1 now clears for real:
# `Apply complete! Resources: 3 added, 0 changed, 0 destroyed.` The estate's
# wall moves past cold deploy into MIGRATE's own tofu-slot convergence apply
# (see the TOFU-SLOT FINDING elsewhere in this repo, corpus-iam-policy's
# header) - a NEW, confirmed floci gap, not a choudoufu one: floci's
# `DescribeTable` never returns a GSI's `OnDemandThroughput` (confirmed via
# `aws dynamodb describe-table`, direct, no choudoufu), so the AWS provider's
# own Read always drops it from state, and the very next plan against this
# module's config - which declares an explicit `on_demand_throughput` on its
# one GSI - proposes replacing the GSI in place on a table that has not
# drifted at all. Reproduced with PLAIN stock `terraform plan` against the
# identical cold-deploy state and the same live floci table, zero choudoufu
# code touched: `Plan: 0 to add, 1 to change, 1 to destroy`. HANDOFF.md label
# 1, "OpenTofu fails here too" - PARITY, not a choudoufu defect. Applying
# that GSI-replace plan (which the tofu-slot convergence apply's ordinary
# `apply` does, since it recomputes the full diff, not just the tofu-slot
# tag) then hangs and fails for real: `waiting for update AWS DynamoDB Table:
# GSI (TitleIndex): couldn't find resource (21 retries)`. Filed as
# https://github.com/lex00/floci/issues/91 with both reproductions.
#
# UPDATE 2026-08-21 (later the same day): lex00/floci#91 is fixed (build
# 8539609c, PR lex00/floci#92, published digest sha256:8f1fc4a500a3553e3626
# 89cdcb6c5e31784bbaa7ad914de22bdd1c088a785f5, tag 8539609; live/floci-image
# re-pinned to it). Re-run clean end to end against the new pin: all FIVE
# stages now PASS. The tofu-slot convergence apply that used to hang on the
# GSI replace now completes (`Apply complete! Resources: 0 added, 0 changed,
# 1 destroyed` - the destroyed resource is the synthetic tofu-slot
# placeholder for the module's own zero-instance branch, not the table,
# reconfirmed live immediately after); test plan reports an empty diff and
# the correct rendered identity re-read directly from DynamoDB; test apply
# is a genuine no-op; drift-and-reconverge tampers the table's Terraform tag
# out of band, gets a plan that proposes fixing exactly that one object, and
# reconverges it. live/corpus-crossing-manifest.json records the full
# 5/5 pass. terraform-aws-dynamodb-table (35.6M registry downloads) is now
# a real estate crossed clean at full parity.
#
#   bash live/e2e/corpus-dynamodb-table-basic/run.sh
#
# Needs Docker, the AWS CLI, and the real `terraform` binary on PATH for
# stage 1. .corpus is read, never written: the estate is copied out to a
# temp directory first, same as every other corpus crossing.
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4681, clear of every
#                other live/e2e fixture's port).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        set to 1 to corrupt the expected table identity ahead of
#                stage 3's assertion, and to tamper a second, unrelated
#                object's tag ahead of stage 5's drift assertion - proving
#                both are load-bearing rather than a grep/count that always
#                matches. Set to 6 to exercise day2_rename's own break
#                control (rename module.dynamodb_table WITHOUT a moved
#                block).
#   BREAK_REMOVE  set to 1 to exercise day2_remove's own break control:
#                keep module.dynamodb_table_final's block and assert no
#                destroy is proposed.
#   BREAK_COUNT  set to 1 to exercise day2_count's own break control (PART
#                G, far below): after the real scale-down plan, assert the
#                WRONG instance (count_test[0] rather than count_test[1])
#                was destroyed - the assertion must fail. Only reachable
#                when BREAK is not 6 and BREAK_REMOVE is not 1, because
#                PART G starts from PART E's real, completed removal.
#
# Exit codes: 0 on a real pass of all five stages, non-zero on a real
# failure. Every assertion reads command output, an exit code, or the
# emulator's own answer through the AWS CLI, never choudoufu's own report of
# itself.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SRC_ROOT="$ROOT/.corpus/dynamodb-table"
SRC_EXAMPLE="$SRC_ROOT/examples/basic"
WORK="$(mktemp -d)"
EST="$WORK/dynamodb-table"
FLOCI_PORT="${FLOCI_PORT:-4681}"
FLOCI_NAME="choudoufu-corpus-dynamodb-table-basic-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

# PART GREENFIELD (live/GAUNTLET.md #13) runs two MORE floci containers,
# each its own fresh namespace: one choudoufu applies directly with a live
# block, one stock terraform applies the identical (delta-1'd) config. Both
# offsets are +400/+800 from FLOCI_PORT so no combination of this script's
# own three ports, run twice at once with two different FLOCI_PORT values
# spaced 1 apart (as the batch runner does), can collide.
FLOCI_GREEN_PORT="${FLOCI_GREEN_PORT:-$((FLOCI_PORT + 400))}"
FLOCI_GREEN_ORACLE_PORT="${FLOCI_GREEN_ORACLE_PORT:-$((FLOCI_PORT + 800))}"
FLOCI_GREEN_NAME="choudoufu-corpus-dynamodb-table-basic-green-$$"
FLOCI_GREEN_ORACLE_NAME="choudoufu-corpus-dynamodb-table-basic-green-oracle-$$"
GREEN_ENDPOINT="http://127.0.0.1:${FLOCI_GREEN_PORT}"
GREEN_ORACLE_ENDPOINT="http://127.0.0.1:${FLOCI_GREEN_ORACLE_PORT}"
GREEN_ESTATE="dynamodb-table-basic-greenfield"

# PART G-ORACLE (day2_count's stock oracle, far below) gets its OWN
# dedicated, always-idle container rather than sharing $ENDPOINT (the real
# leg's own account, still mid-crossing when G-ORACLE runs) or either
# greenfield container above (not created until PART GREENFIELD, much
# later in this script) - by construction this sidesteps PR #502's own
# finding that a stock oracle sharing an endpoint with the real leg can
# poison the real leg's own lookup. +1200 keeps it clear of the +400/+800
# offsets above under the same run-twice-1-apart concurrency the comment
# above describes.
FLOCI_COUNT_ORACLE_PORT="${FLOCI_COUNT_ORACLE_PORT:-$((FLOCI_PORT + 1200))}"
FLOCI_COUNT_ORACLE_NAME="choudoufu-corpus-dynamodb-table-basic-count-oracle-$$"
COUNT_ORACLE_ENDPOINT="http://127.0.0.1:${FLOCI_COUNT_ORACLE_PORT}"

ESTATE="dynamodb-table-basic-crossing"
REGION="eu-west-1"
ACCOUNT="000000000000"

cleanup() {
  docker rm -f "$FLOCI_NAME" "$FLOCI_GREEN_NAME" "$FLOCI_GREEN_ORACLE_NAME" "$FLOCI_COUNT_ORACLE_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
# 2026-08-21 fix: the header documents DEBUG_KEEP but the trap never
# actually checked it - it always ran. Matched to every other corpus-*
# script's guard.
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
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }
gauntlet_begin

# ── 0. tools and corpus ─────────────────────────────────────────────────────
log "=== 0. tools and corpus ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v terraform >/dev/null 2>&1 || fail "the terraform binary is not on PATH - needed to build unmarked reference infra"
[ -d "$SRC_EXAMPLE" ] || fail "$SRC_EXAMPLE is missing - run 'just corpus-fetch' first"
[ -f "$SRC_ROOT/main.tf" ] || fail "$SRC_ROOT/main.tf is missing - run 'just corpus-fetch' first"

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
# module root files plus the basic example are copied out, preserving the
# relative path the example's `source = "../.."` expects.
mkdir -p "$EST/examples"
cp "$SRC_ROOT/main.tf" "$SRC_ROOT/variables.tf" "$SRC_ROOT/outputs.tf" "$SRC_ROOT/versions.tf" "$SRC_ROOT/autoscaling.tf" "$EST/"
cp -R "$SRC_EXAMPLE" "$EST/examples/basic"
rm -rf "$EST/examples/basic/.terraform" "$EST/examples/basic/.terraform.lock.hcl"
[ -f "$EST/examples/basic/main.tf" ] || fail "the estate copy is missing main.tf"
log "  module root + examples/basic copied out of .corpus into $WORK"

EX="$EST/examples/basic"

# ── 1. the onboarding delta - emulator flags + provider pin ────────────────
log "=== 1. the onboarding delta ==="
perl -0pi -e 's/(provider "aws" \{\n  region = "eu-west-1"\n)\}/$1\n  access_key                   = "test"\n  secret_key                   = "test"\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  s3_use_path_style            = true\n}/' "$EX/main.tf"
grep -q 's3_use_path_style' "$EX/main.tf" || fail "the emulator delta did not match main.tf - the corpus pin has moved"
perl -pi -e 's/version = ">= 6\.28"/version = "= 6.59.0"/' "$EX/versions.tf"
grep -q 'version = "= 6.59.0"' "$EX/versions.tf" || fail "the provider version pin delta did not match versions.tf - the corpus pin has moved"
log "  DELTA  emulator flags added to the provider block; aws provider pinned = 6.59.0; no backend, no live block yet"

log "=== 2. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"dynamodb"' <<< "${HEALTH:-}" && break
  sleep 2
done
grep -q '"dynamodb"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (dynamodb) at $ENDPOINT"
log "  healthy"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 1: COLD DEPLOY - plain terraform, no choudoufu, no live block
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage cold_deploy
log "=== STAGE 1: cold deploy (terraform apply, the real unmodified example + delta) ==="
( cd "$EX" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$EX" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "stage 1 init failed"; }
COLD_OUT="$(cd "$EX" && terraform apply -input=false -auto-approve -no-color 2>&1)"; COLD_RC=$?
[ "$COLD_RC" -eq 0 ] || { printf '%s\n' "$COLD_OUT" | tail -60; fail "the cold apply failed"; }
grep -qE 'Apply complete! Resources: 3 added' <<< "$COLD_OUT" \
  || { grep -E 'Apply complete' <<< "$COLD_OUT"; fail "the cold apply did not create exactly 3 resources (random_pet, the table, the resource policy)"; }
log "  $(grep -E 'Apply complete' <<< "$COLD_OUT")"
[ -f "$EX/terraform.tfstate" ] || fail "plain terraform left no state file to migrate from"

read -r PET TABLE_NAME <<< "$(python3 -c "
import json
d = json.load(open('$EX/terraform.tfstate'))
pet = name = ''
for r in d['resources']:
    if r.get('module') is None and r['type'] == 'random_pet' and r['name'] == 'this':
        pet = r['instances'][0]['attributes']['id']
    if r.get('module') == 'module.dynamodb_table' and r['type'] == 'aws_dynamodb_table':
        name = r['instances'][0]['attributes']['name']
print(pet, name)
")"
[ -n "$PET" ] && [ -n "$TABLE_NAME" ] || fail "could not read random_pet.this.id or the table name back out of the plain state"
[ "$TABLE_NAME" = "my-table-$PET" ] || fail "the table name ($TABLE_NAME) does not match my-table-$PET"
log "  random_pet.this.id = $PET; table name = $TABLE_NAME"

TABLE_ARN="$(awsl dynamodb describe-table --table-name "$TABLE_NAME" --query 'Table.TableArn' --output text)"
[ -n "$TABLE_ARN" ] && [ "$TABLE_ARN" != "None" ] || fail "the table is not live after the cold apply"
log "  table live: $TABLE_ARN"

UNMARKED="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$UNMARKED" = "0" ] || fail "plain terraform's own objects already carry tofu-estate=$ESTATE before migration - this crossing proves nothing"
log "  confirmed unmarked: 0 objects carry tofu-estate=$ESTATE before migration"

cp "$EX/terraform.tfstate" "$WORK/cold.tfstate"

log ""
log "STAGE 1 (cold deploy): PASS"
gauntlet_stage cold_deploy pass "$(grep -E 'Apply complete' <<< "$COLD_OUT"); 0 objects carry tofu-estate=$ESTATE before migration"
log ""

# ══════════════════════════════════════════════════════════════════════════
# PART D-ORACLE: RENAME, stock (day2_rename, active - live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
#
# module.dynamodb_table is this estate's only real module, and it holds the
# estate's only two managed resources left in scope after the #314 DELTA
# (random_pet.this is pinned to a literal ahead of migrate, below, and never
# reaches the adopted config at all - see "DELTA random_pet pinned" - so it
# cannot be a second, independent rename target the way another estate's
# second module or standalone resource is). Both real day2_rename mechanisms
# therefore run on the SAME module, one after the other: a `moved` block
# first (module.dynamodb_table -> module.dynamodb_table_moved), then
# "choudoufu live-mv" second (module.dynamodb_table_moved ->
# module.dynamodb_table_final, no moved block for that hop at all). The
# stock oracle below plans the NET rename (original name straight to the
# final name) on a copy of cold_deploy's own state, before choudoufu or
# live-import ever touch it.
gauntlet_begin_stage day2_rename
log "=== D-ORACLE. stock: the net module rename, through one moved block, on cold_deploy's own state ==="
ORACLE_ROOT="$WORK/oracle"
cp -r "$EST" "$ORACLE_ROOT"
ORACLE="$ORACLE_ROOT/examples/basic"
rm -rf "$ORACLE/.terraform" "$ORACLE/.terraform.lock.hcl"
sed -i.bak 's/module "dynamodb_table" {/module "dynamodb_table_final" {/' "$ORACLE/main.tf"
sed -i.bak 's/module\.dynamodb_table\./module.dynamodb_table_final./g' "$ORACLE/outputs.tf"
rm -f "$ORACLE/main.tf.bak" "$ORACLE/outputs.tf.bak"
cat >> "$ORACLE/main.tf" <<'EOF'

moved {
  from = module.dynamodb_table
  to   = module.dynamodb_table_final
}
EOF
( cd "$ORACLE" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ORACLE" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_rename stock oracle's reinit failed"; }
ORACLE_PLAN_OUT="$(cd "$ORACLE" && terraform plan -input=false -no-color 2>&1)"; ORACLE_PLAN_RC=$?
[ "$ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -40; fail "the day2_rename stock oracle plan exited $ORACLE_PLAN_RC"; }
grep -qE '^  # .+ will be (destroyed|created)' <<< "$ORACLE_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock proposes a destroy or create for a rename carried entirely by a moved block - the oracle itself is not zero-churn"; }
grep -qF 'Plan: 0 to add, 0 to change, 0 to destroy.' <<< "$ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -10; fail "stock's rename plan is not a true no-op"; }
log "  stock: zero churn on cold_deploy's own state - the module move reports only its move, no attribute diff at all"

# day2_remove's stock oracle (live/GAUNTLET.md #7, active): "Stock with the
# same block removed plans the same destroys." A SEPARATE copy of
# cold_deploy's own state, so this destroy has nothing to do with the
# rename this script also exercises (module.dynamodb_table, its ORIGINAL
# name - not the renamed one the real day2_remove check below uses, since
# this copy is never touched by Part D at all). outputs.tf references only
# module.dynamodb_table's own outputs, so removing its block leaves nothing
# for outputs.tf to reference - emptied outright rather than edited output
# by output.
gauntlet_begin_stage day2_remove
log "=== D-REMOVE-ORACLE. stock: delete module.dynamodb_table's block on cold_deploy's own state ==="
REMOVE_ORACLE_ROOT="$WORK/oracle-remove"
cp -r "$EST" "$REMOVE_ORACLE_ROOT"
REMOVE_ORACLE="$REMOVE_ORACLE_ROOT/examples/basic"
rm -rf "$REMOVE_ORACLE/.terraform" "$REMOVE_ORACLE/.terraform.lock.hcl"
perl -0777pi -e 's/module "dynamodb_table" \{.*?\n\}\n\n\n//s' "$REMOVE_ORACLE/main.tf"
grep -q 'module "dynamodb_table"' "$REMOVE_ORACLE/main.tf" && fail "removing module.dynamodb_table's block from the day2_remove oracle copy did not match - the corpus example has moved"
: > "$REMOVE_ORACLE/outputs.tf"
( cd "$REMOVE_ORACLE" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$REMOVE_ORACLE" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_remove stock oracle's reinit failed"; }
REMOVE_ORACLE_PLAN_OUT="$(cd "$REMOVE_ORACLE" && terraform plan -input=false -no-color 2>&1)"; REMOVE_ORACLE_PLAN_RC=$?
[ "$REMOVE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_remove stock oracle plan exited $REMOVE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.dynamodb_table\.aws_dynamodb_table\.this\[0\] will be destroyed' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "stock does not propose destroying module.dynamodb_table's table when its block is removed"; }
grep -qE '^  # module\.dynamodb_table\.aws_dynamodb_resource_policy\.this\[0\] will be destroyed' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "stock does not propose destroying module.dynamodb_table's resource policy when its block is removed"; }
grep -qF 'Plan: 0 to add, 0 to change, 2 to destroy.' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -10; fail "stock's remove plan proposes something other than exactly two destroys"; }
log "  stock: exactly two destroys (the table and its resource policy), nothing else, on the state cold_deploy produced"
gauntlet_end_stage

# day2_replace's stock oracle (live/GAUNTLET.md #9, active): "Stock's
# replace of the same resource leaves the same single object." A THIRD
# separate copy of cold_deploy's own state (same nesting-depth requirement
# as D-ORACLE/D-REMOVE-ORACLE above), so this oracle also runs on
# cold_deploy's own state, before any rename or migration ever touches
# $EX. Changes module.dynamodb_table's `name` argument (a real,
# upstream-declared ForceNew argument on aws_dynamodb_table - DynamoDB has
# no UpdateTable rename capability, only CreateTable/DeleteTable) to a
# different literal name, which forces stock to replace the SAME declared
# address rather than propose a destroy-and-create pair at two different
# addresses (that shape is day2_rename's own BREAK=6 finding, a genuinely
# different thing). aws_dynamodb_resource_policy's own `resource_arn`
# argument is required and not computed in the provider's schema, and
# DynamoDB's PutResourcePolicy/DeleteResourcePolicy APIs are keyed by the
# table's ARN with no update path for that key, so the table's ARN
# changing (a brand-new table, not a renamed one) is expected to cascade
# into a forced replace of the resource policy too - confirmed below by
# the plan itself, not assumed.
gauntlet_begin_stage day2_replace
log "=== F-ORACLE. stock: force-replace module.dynamodb_table's table via its ForceNew name argument, on cold_deploy's own state ==="
REPLACE_ORACLE_ROOT="$WORK/oracle-replace"
cp -r "$EST" "$REPLACE_ORACLE_ROOT"
REPLACE_ORACLE="$REPLACE_ORACLE_ROOT/examples/basic"
rm -rf "$REPLACE_ORACLE/.terraform" "$REPLACE_ORACLE/.terraform.lock.hcl"
sed -i.bak 's/name                        = "my-table-\${random_pet\.this\.id}"/name                        = "my-table-${random_pet.this.id}-v2"/' "$REPLACE_ORACLE/main.tf"
rm -f "$REPLACE_ORACLE/main.tf.bak"
grep -q 'my-table-${random_pet.this.id}-v2' "$REPLACE_ORACLE/main.tf" \
  || fail "changing module.dynamodb_table's name argument in the replace-oracle copy did not match - the corpus pin has moved"
( cd "$REPLACE_ORACLE" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$REPLACE_ORACLE" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_replace stock oracle's reinit failed"; }
REPLACE_ORACLE_PLAN_OUT="$(cd "$REPLACE_ORACLE" && terraform plan -input=false -no-color 2>&1)"; REPLACE_ORACLE_PLAN_RC=$?
[ "$REPLACE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_replace stock oracle plan exited $REPLACE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.dynamodb_table\.aws_dynamodb_table\.this\[0\] must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "stock does not propose replacing module.dynamodb_table's table when its name argument changes"; }
REPLACE_ORACLE_POLICY_REPLACES=0
grep -qE '^  # module\.dynamodb_table\.aws_dynamodb_resource_policy\.this\[0\] must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" && REPLACE_ORACLE_POLICY_REPLACES=1
log "  stock: replaces module.dynamodb_table's table at the same declared address (resource_arn-dependent resource_policy also replaces: $REPLACE_ORACLE_POLICY_REPLACES) on the state cold_deploy produced - plan only, not applied (same convention as D-ORACLE/D-REMOVE-ORACLE: this copy shares floci's account with \$EST, and actually applying here would destroy the real table the estate's later stages still depend on)"
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# PART G-ORACLE: CHANGE COUNT, stock oracle (day2_count, active -
# live/GAUNTLET.md #8, issue #359/#488)
# ══════════════════════════════════════════════════════════════════════════
#
# THE COUNTABLE-KNOB SEARCH (issue #488's own fallback clause; see
# live/e2e/corpus-xancloud-iac/run.sh's own Part F for the preferred,
# real-knob shape). This estate's real module
# (terraform-aws-modules/terraform-aws-dynamodb-table v5.5.1) has NO honest
# resource-level count/for_each knob a caller can vary: every `count` on
# aws_dynamodb_table.this / this_ignore_changes_gsi / this_with_autoscaling
# is boolean-shaped (`var.create_table && ... ? 1 : 0`, confirmed by
# reading main.tf directly - never a numeric value a caller varies), and
# its one list-shaped input, var.replica_regions, drives a
# `dynamic "replica" { for_each = var.replica_regions ... }` block NESTED
# INSIDE the table resource itself, not a separate resource with its own
# instances - scaling it would change one resource's attributes in place,
# never destroy or create a resource instance the way this stage's Proves
# text needs. var.global_secondary_indexes is the same shape (a `dynamic`
# block inside the same resource). So this estate follows
# live/e2e/corpus-iam-read-only-policy/run.sh's PART G/PART G-ORACLE
# precedent instead (PR #500, issue #488's own fallback clause): a
# synthetic aws_dynamodb_table.count_test resource, added and removed
# entirely within this oracle and PART G (its real-leg counterpart, far
# below) - nothing else in this estate ever names it.
#
# A DEDICATED, ALWAYS-IDLE container (FLOCI_COUNT_ORACLE_PORT, declared
# above next to the other floci ports), never shared with $ENDPOINT (the
# real leg's own account, still mid-crossing at this point in the script)
# or either greenfield container (not created until PART GREENFIELD, much
# later) - by construction this sidesteps PR #502's own finding that a
# stock oracle sharing an endpoint with the real leg can poison the real
# leg's own lookup, the same trap live/HANDOFF.md's dispatch notes name.
#
# THE IDENTITY DISCRIMINATOR (identity semantics vary by type - never
# copied from another estate's assertion). Established directly against
# this floci pin with no tofu in the loop before writing anything below:
# create-table, describe (capture TableArn + TableId), delete-table,
# recreate under the IDENTICAL name, describe again. TableArn was BYTE
# IDENTICAL both times (arn:aws:dynamodb:<region>:<account>:table/<name> -
# deterministic from region+account+name, the same shape
# corpus-iam-read-only-policy's own aws_iam_policy ARN finding), while
# TableId was a DIFFERENT server-minted UUID each time
# (c4928135-4bdb-465c-947f-aaf4c566418e, then
# d8abfba6-dfc7-4df8-9a8d-4d6881844fe2, same table name both times). So
# TableId, not TableArn, is this type's "genuinely a new object"
# discriminator - the exact same shape PolicyId was for aws_iam_policy in
# PR #500, just a different field name.
gauntlet_begin_stage day2_count
count_test_block() { # $1 = count
  local n="$1"
  cat <<COUNTEOF
resource "aws_dynamodb_table" "count_test" {
  count        = $n
  name         = "dynamodb-count-test-\${count.index}"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "id"

  attribute {
    name = "id"
    type = "S"
  }

  tags = {
    Example = "day2_count evidence (issue #359/#488)"
  }
}
COUNTEOF
}
oracle_count_provider() {
  cat <<EOF
terraform {
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
  s3_use_path_style           = true
}

EOF
}

log "=== G-ORACLE: stock, create a 2-instance count block, scale it to 1 and back, in a dedicated always-idle account ==="
docker run -d --rm -p "${FLOCI_COUNT_ORACLE_PORT}:4566" --name "$FLOCI_COUNT_ORACLE_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_COUNT_ORACLE_NAME failed"
COUNT_ORACLE_HEALTH=""
for _ in $(seq 1 45); do
  COUNT_ORACLE_HEALTH="$(curl -fs "${COUNT_ORACLE_ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"dynamodb"' <<< "${COUNT_ORACLE_HEALTH:-}" && break
  sleep 2
done
grep -q '"dynamodb"' <<< "${COUNT_ORACLE_HEALTH:-}" || fail "the day2_count oracle floci did not come up healthy (dynamodb) at $COUNT_ORACLE_ENDPOINT"
log "  healthy: $COUNT_ORACLE_ENDPOINT"

PLAIN_ORACLE_COUNT="$WORK/oracle-count"
mkdir -p "$PLAIN_ORACLE_COUNT"
{ oracle_count_provider; count_test_block 2; } > "$PLAIN_ORACLE_COUNT/main.tf"
( cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$COUNT_ORACLE_ENDPOINT" AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$COUNT_ORACLE_ENDPOINT" terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_count stock oracle's init failed"; }
ORACLE_COUNT_APPLY_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$COUNT_ORACLE_ENDPOINT" AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" terraform apply -input=false -auto-approve -no-color 2>&1)"; ORACLE_COUNT_APPLY_RC=$?
[ "$ORACLE_COUNT_APPLY_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_COUNT_APPLY_OUT" | tail -30; fail "the day2_count stock oracle's baseline apply failed"; }
grep -qE 'Apply complete! Resources: 2 added' <<< "$ORACLE_COUNT_APPLY_OUT" \
  || { printf '%s\n' "$ORACLE_COUNT_APPLY_OUT" | tail -30; fail "stock did not create exactly 2 count-test tables for the day2_count oracle"; }

awso() { aws --endpoint-url "$COUNT_ORACLE_ENDPOINT" --region "$REGION" "$@"; }
ORACLE_CT0_ARN="$(awso dynamodb describe-table --table-name dynamodb-count-test-0 --query 'Table.TableArn' --output text)"
ORACLE_CT1_ARN="$(awso dynamodb describe-table --table-name dynamodb-count-test-1 --query 'Table.TableArn' --output text)"
[ -n "$ORACLE_CT0_ARN" ] && [ "$ORACLE_CT0_ARN" != "None" ] || fail "no oracle count_test[0] table found by name"
[ -n "$ORACLE_CT1_ARN" ] && [ "$ORACLE_CT1_ARN" != "None" ] || fail "no oracle count_test[1] table found by name"
ORACLE_CT0_ID="$(awso dynamodb describe-table --table-name dynamodb-count-test-0 --query 'Table.TableId' --output text)"
ORACLE_CT1_ID="$(awso dynamodb describe-table --table-name dynamodb-count-test-1 --query 'Table.TableId' --output text)"
[ -n "$ORACLE_CT0_ID" ] && [ "$ORACLE_CT0_ID" != "None" ] || fail "oracle count_test[0] has no TableId"
[ -n "$ORACLE_CT1_ID" ] && [ "$ORACLE_CT1_ID" != "None" ] || fail "oracle count_test[1] has no TableId"
log "  stock: 2 instances created, count_test[0]=$ORACLE_CT0_ARN (id=$ORACLE_CT0_ID) count_test[1]=$ORACLE_CT1_ARN (id=$ORACLE_CT1_ID)"

{ oracle_count_provider; count_test_block 1; } > "$PLAIN_ORACLE_COUNT/main.tf"
ORACLE_DOWN_PLAN_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$COUNT_ORACLE_ENDPOINT" AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" terraform plan -input=false -no-color 2>&1)"; ORACLE_DOWN_PLAN_RC=$?
[ "$ORACLE_DOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | tail -30; fail "the day2_count stock oracle's scale-down plan exited $ORACLE_DOWN_PLAN_RC"; }
grep -qE '^  # aws_dynamodb_table\.count_test\[1\] will be destroyed' <<< "$ORACLE_DOWN_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-down plan does not destroy count_test[1]"; }
grep -qE '^  # aws_dynamodb_table\.count_test\[0\] will be' <<< "$ORACLE_DOWN_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-down plan touches count_test[0], which should be untouched"; }
ORACLE_DOWN_APPLY_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$COUNT_ORACLE_ENDPOINT" AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" terraform apply -input=false -auto-approve -no-color 2>&1)"; ORACLE_DOWN_APPLY_RC=$?
[ "$ORACLE_DOWN_APPLY_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_DOWN_APPLY_OUT" | tail -30; fail "the day2_count stock oracle's scale-down apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$ORACLE_DOWN_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$ORACLE_DOWN_APPLY_OUT"; fail "the day2_count stock oracle's scale-down apply was not exactly one destroy"; }
ORACLE_CT0_ID_AFTER_DOWN="$(awso dynamodb describe-table --table-name dynamodb-count-test-0 --query 'Table.TableId' --output text)"
[ "$ORACLE_CT0_ID_AFTER_DOWN" = "$ORACLE_CT0_ID" ] || fail "stock's surviving count_test[0] changed TableId across the scale-down ($ORACLE_CT0_ID -> $ORACLE_CT0_ID_AFTER_DOWN)"
if ORACLE_CT1_STILL="$(awso dynamodb describe-table --table-name dynamodb-count-test-1 2>&1)"; then
  echo "$ORACLE_CT1_STILL"; fail "stock's count_test[1] still exists after the scale-down destroy"
fi
log "  stock: exactly one destroy (count_test[1]=$ORACLE_CT1_ARN), count_test[0]=$ORACLE_CT0_ARN (id=$ORACLE_CT0_ID) unchanged"

{ oracle_count_provider; count_test_block 2; } > "$PLAIN_ORACLE_COUNT/main.tf"
ORACLE_UP_PLAN_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$COUNT_ORACLE_ENDPOINT" AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" terraform plan -input=false -no-color 2>&1)"; ORACLE_UP_PLAN_RC=$?
[ "$ORACLE_UP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | tail -30; fail "the day2_count stock oracle's scale-up plan exited $ORACLE_UP_PLAN_RC"; }
grep -qE '^  # aws_dynamodb_table\.count_test\[1\] will be created' <<< "$ORACLE_UP_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-up plan does not create count_test[1]"; }
grep -qE '^  # aws_dynamodb_table\.count_test\[0\] will be' <<< "$ORACLE_UP_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-up plan touches count_test[0], which should be untouched"; }
ORACLE_UP_APPLY_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$COUNT_ORACLE_ENDPOINT" AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" terraform apply -input=false -auto-approve -no-color 2>&1)"; ORACLE_UP_APPLY_RC=$?
[ "$ORACLE_UP_APPLY_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_UP_APPLY_OUT" | tail -30; fail "the day2_count stock oracle's scale-up apply failed"; }
grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$ORACLE_UP_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$ORACLE_UP_APPLY_OUT"; fail "the day2_count stock oracle's scale-up apply was not exactly one create"; }
ORACLE_CT1_NEW_ARN="$(awso dynamodb describe-table --table-name dynamodb-count-test-1 --query 'Table.TableArn' --output text)"
[ -n "$ORACLE_CT1_NEW_ARN" ] && [ "$ORACLE_CT1_NEW_ARN" != "None" ] || fail "no oracle count_test[1] table found after the scale-up"
[ "$ORACLE_CT1_NEW_ARN" = "$ORACLE_CT1_ARN" ] || fail "the recreated count_test[1]'s ARN ($ORACLE_CT1_NEW_ARN) differs from its pre-destroy ARN ($ORACLE_CT1_ARN) - unexpected: aws_dynamodb_table's ARN is region/account/name-derived and should be identical both times"
ORACLE_CT1_NEW_ID="$(awso dynamodb describe-table --table-name dynamodb-count-test-1 --query 'Table.TableId' --output text)"
[ "$ORACLE_CT1_NEW_ID" != "$ORACLE_CT1_ID" ] || fail "stock's recreated count_test[1] came back with the SAME TableId it had before being destroyed - the destroy was not real"
ORACLE_CT0_ID_AFTER_UP="$(awso dynamodb describe-table --table-name dynamodb-count-test-0 --query 'Table.TableId' --output text)"
[ "$ORACLE_CT0_ID_AFTER_UP" = "$ORACLE_CT0_ID" ] || fail "stock's count_test[0] changed TableId across the scale-up"
log "  stock: exactly one create (count_test[1], same ARN $ORACLE_CT1_NEW_ARN - deterministic from region+account+name - but a NEW TableId $ORACLE_CT1_NEW_ID, was $ORACLE_CT1_ID), count_test[0]=$ORACLE_CT0_ARN (id=$ORACLE_CT0_ID) unchanged throughout"

docker rm -f "$FLOCI_COUNT_ORACLE_NAME" >/dev/null 2>&1 || true
gauntlet_end_stage

gauntlet_begin_stage migrate

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE - choudoufu live-import against the cold state, then one
# ordinary apply to converge tofu-slot
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage migrate
log "=== STAGE 2: migrate (choudoufu live-import -approve, then converge) ==="
# 2026-08-21 fix: the original regex assumed required_providers held ONLY
# the aws entry (immediately followed by required_providers's own closing
# brace, then the terraform block's). This example's versions.tf also
# declares a `random` provider inside required_providers - present at the
# same commit this crossing pins (v5.5.1, 02b2d66a; not a corpus pin drift,
# confirmed by inspecting .corpus/dynamodb-table/examples/basic/versions.tf
# directly), so the old anchor never matched anything and the script never
# actually got this far before (stage 1 was blocked by lex00/floci#86 until
# now). Anchored on the true end of the file instead - the terraform
# block's own final closing brace - which is shape-independent of whatever
# required_providers holds.
perl -0777pi -e 's/\}\n\z/\n  live {\n    estate = "'"$ESTATE"'"\n    record_store "local" {\n      path = ".tofu-records"\n    }\n  }\n}\n/' "$EX/versions.tf"
grep -q "estate = \"$ESTATE\"" "$EX/versions.tf" || fail "the live block delta did not match versions.tf - the corpus pin has moved"

# THE RANDOM_PET GAP (see header): pin the already-applied pet value as a
# literal, the same DELTA 3 shape live/e2e/corpus-s3-bucket-complete uses,
# ahead of the tagged table's own identity-bearing `name` argument depending
# on it (issue #314's still-open wall).
perl -0pi -e 's/resource "random_pet" "this" \{\n  length = 2\n\}\n/locals {\n  pinned_pet = "'"$PET"'" # DELTA: live-import does not resolve a TAGGED resource'"'"'s identity argument through a record-backed value (issue #314)\n}\n/' "$EX/main.tf"
perl -pi -e 's/random_pet\.this\.id/local.pinned_pet/g' "$EX/main.tf"
grep -q 'pinned_pet = "'"$PET"'"' "$EX/main.tf" || fail "the random_pet pin did not match main.tf - the corpus pin has moved"
[ "$(grep -c 'local.pinned_pet' "$EX/main.tf")" = "1" ] || fail "the pin reached $(grep -c 'local.pinned_pet' "$EX/main.tf") references, expected 1"
grep -q 'resource "random_pet"' "$EX/main.tf" && fail "the random_pet resource was left behind"
log "  DELTA  random_pet pinned to $PET   (CHOUDOUFU GAP, issue #314, see header)"

( cd "$EX" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$EX" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "choudoufu init failed"; }

rm -f "$EX/terraform.tfstate" "$EX/terraform.tfstate.backup"

IMPORT_OUT="$(cd "$EX" && "$TOFU" live-import -state="$WORK/cold.tfstate" -estate="$ESTATE" -no-color 2>&1)"; IMPORT_RC=$?
[ "$IMPORT_RC" -eq 0 ] || { printf '%s\n' "$IMPORT_OUT" | tail -40; fail "live-import (dry run) failed"; }
# 2026-08-21 fix: this was written speculatively before stage 1 ever cleared
# (it was blocked on lex00/floci#86), and got the eligible count wrong -
# "2 of 2" assumed the resource policy was taggable. Run for real, it is
# not: aws_dynamodb_resource_policy has no `tags` argument in the provider's
# schema at all (genuinely UNTAGGABLE, not a choudoufu gap), and
# random_pet.this is a record-backed value with no live object to tag
# (seeded into the record store instead - the same DELTA 3 shape this
# script's own header describes). So exactly 1 of the 3 resource instances
# (the table) is eligible for stamping; the other 2 are UNTAGGABLE for two
# different, both legitimate, reasons.
grep -qF "1 of 3 resource instance(s) are eligible for stamping" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import did not verify exactly 1 of 3 resource instances as eligible (the table alone - the resource policy is untaggable, random_pet is record-backed) - the corpus pin or the fix under test has moved"; }
grep -qF "UNTAGGABLE (2)" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "expected exactly 2 UNTAGGABLE resource instances (the resource policy and random_pet.this)"; }
grep -qF "No tag has been written." <<< "$IMPORT_OUT" || fail "the dry run wrote a tag - it must not"
log "  dry run: 1 of 3 eligible (the table); 2 UNTAGGABLE (resource policy has no tags argument; random_pet is record-backed); nothing written yet"

APPROVE_OUT="$(cd "$EX" && "$TOFU" live-import -state="$WORK/cold.tfstate" -estate="$ESTATE" -approve -no-color 2>&1)"; APPROVE_RC=$?
[ "$APPROVE_RC" -eq 0 ] || { printf '%s\n' "$APPROVE_OUT" | tail -40; fail "live-import -approve failed"; }
log "  approve: $(grep -oE '[0-9]+ resource\(s\) newly stamped.*' <<< "$APPROVE_OUT" | head -1)"

WANT_TABLE_ADDR="module.dynamodb_table.aws_dynamodb_table.this:0"
GOT_TABLE_ADDR="$(awsl dynamodb list-tags-of-resource --resource-arn "$TABLE_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_TABLE_ADDR" = "$WANT_TABLE_ADDR" ] || fail "$TABLE_ARN carries tofu-address=$GOT_TABLE_ADDR, not $WANT_TABLE_ADDR"
log "  marker verified directly against DynamoDB, not through choudoufu's own report:"
log "    $TABLE_ARN -> tofu-address=$GOT_TABLE_ADDR"

# The tofu-slot convergence apply (see live/e2e/corpus-iam-policy's header
# for the mechanism). Both real resources declare count-shaped addresses.
CONVERGE_OUT="$(cd "$EX" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; CONVERGE_RC=$?
[ "$CONVERGE_RC" -eq 0 ] || { printf '%s\n' "$CONVERGE_OUT" | tail -40; fail "the tofu-slot convergence apply failed"; }
log "  $(grep -E 'Apply complete' <<< "$CONVERGE_OUT") (tofu-slot convergence)"
[ ! -f "$EX/terraform.tfstate" ] || fail "the convergence apply wrote a state file"

log ""
log "STAGE 2 (migrate): PASS"
gauntlet_stage migrate pass "$(grep -oE '[0-9]+ resource\(s\) newly stamped.*' <<< "$APPROVE_OUT" | head -1); $(grep -E 'Apply complete' <<< "$CONVERGE_OUT") (tofu-slot convergence)"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - state deleted (already true), live-plan empty,
# identity re-asserted
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage test_plan
log "=== STAGE 3: test plan (live-plan empty, identity re-checked) ==="
[ ! -f "$EX/terraform.tfstate" ] || fail "a state file exists ahead of stage 3"

plan_into() { ( cd "$EX" && "$TOFU" live-plan -input=false -no-color ); }
PLAN_OUT="$(plan_into 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -80; fail "live-plan exited $PLAN_RC"; }
[ ! -f "$EX/terraform.tfstate" ] || fail "live-plan wrote a state file"
grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$PLAN_OUT" \
  && { grep -E '^  # .+ will be' <<< "$PLAN_OUT"; fail "the plan proposes a resource change"; }
grep -qE '^Foreign resources: (none|nothing was swept)' <<< "$PLAN_OUT" \
  || { grep -E '^Foreign resources:' <<< "$PLAN_OUT"; fail "the plan reports foreign resources"; }
log "  no resource change proposed; nothing foreign"

WANT_TABLE_ADDR2="$WANT_TABLE_ADDR"
if [ "${BREAK:-}" = "1" ]; then
  WANT_TABLE_ADDR2="module.disabled_dynamodb_table.aws_dynamodb_table.this:0"
  log "  BREAK=1: expecting tofu-address=$WANT_TABLE_ADDR2 - the SAME shape and"
  log "           the SAME resource type, just the wrong (and in fact"
  log "           never-created) module. This step must fail."
fi
GOT_TABLE_ADDR2="$(awsl dynamodb list-tags-of-resource --resource-arn "$TABLE_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_TABLE_ADDR2" = "$WANT_TABLE_ADDR2" ] || fail "$TABLE_ARN's tofu-address is $GOT_TABLE_ADDR2, not $WANT_TABLE_ADDR2"
log "  identity re-check (read via the AWS CLI, after the state file has never existed this run): unchanged"

log ""
log "STAGE 3 (test plan): PASS"
gauntlet_stage test_plan pass "no resource change proposed, nothing foreign; identity re-check (via the AWS CLI) unchanged"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 4: TEST APPLY - apply the empty plan, assert a genuine no-op
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage test_apply
log "=== STAGE 4: test apply (apply the empty plan; object count unchanged) ==="
BEFORE_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"

APPLY2_OUT="$(cd "$EX" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; APPLY2_RC=$?
[ "$APPLY2_RC" -eq 0 ] || { printf '%s\n' "$APPLY2_OUT" | tail -40; fail "the post-migration apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY2_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY2_OUT"; fail "the post-migration apply was not a no-op"; }

AFTER_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$AFTER_N" = "$BEFORE_N" ] || fail "object count changed across a no-op apply: $BEFORE_N -> $AFTER_N"
[ ! -f "$EX/terraform.tfstate" ] || fail "a state file exists after the apply"
log "  genuine no-op: $BEFORE_N objects before, $AFTER_N after, no state file either time"

log ""
log "STAGE 4 (test apply): PASS"
gauntlet_stage test_apply pass "genuine no-op: $BEFORE_N objects before, $AFTER_N after, no state file either time"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 5: DRIFT AND RECONVERGE - mutate one object, replan, assert one fix
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage drift_reconverge
log "=== STAGE 5: drift and reconverge (mutate one object out of band) ==="
if [ "${BREAK:-}" = "1" ]; then
  awsl dynamodb tag-resource --resource-arn "$TABLE_ARN" --tags Key=Environment,Value=tampered-by-BREAK
  log "  BREAK=1: also tampered $TABLE_ARN's Environment tag - stage 5 must now see the drift-plus-drift shape and fail the single-object assertion"
fi

awsl dynamodb tag-resource --resource-arn "$TABLE_ARN" --tags Key=Terraform,Value=tampered-out-of-band
DRIFTED_VALUE="$(awsl dynamodb list-tags-of-resource --resource-arn "$TABLE_ARN" --query "Tags[?Key=='Terraform'].Value | [0]" --output text)"
[ "$DRIFTED_VALUE" = "tampered-out-of-band" ] || fail "the out-of-band tag mutation did not take"
log "  mutated $TABLE_ARN's Terraform tag to \"tampered-out-of-band\" directly via the AWS CLI"

DRIFT_PLAN_OUT="$(plan_into 2>&1)"; DRIFT_PLAN_RC=$?
[ "$DRIFT_PLAN_RC" -eq 0 ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | tail -80; fail "the drift-detection plan exited $DRIFT_PLAN_RC"; }

CHANGED_ADDRS="$(grep -oE '^  # \S+ will be updated' <<< "$DRIFT_PLAN_OUT" | awk '{print $2}' | sort -u)"
N_CHANGED="$(printf '%s\n' "$CHANGED_ADDRS" | grep -c . || true)"
if [ "${BREAK:-}" = "1" ]; then
  [ "$N_CHANGED" = "1" ] && fail "BREAK=1 set, but the plan proposes fixing only 1 - this assertion is not load-bearing"
  log "  BREAK=1: the plan proposes fixing $N_CHANGED objects, correctly more than the single-tag mutation alone would - the single-object assertion below is skipped"
else
  [ "$N_CHANGED" = "1" ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | grep -E '^  # .+ will be'; fail "expected exactly 1 object proposed for a fix, got $N_CHANGED"; }
  log "  the plan proposes fixing exactly one object: $(printf '%s' "$CHANGED_ADDRS")"

  RECONVERGE_APPLY="$(cd "$EX" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; RECONVERGE_RC=$?
  [ "$RECONVERGE_RC" -eq 0 ] || { printf '%s\n' "$RECONVERGE_APPLY" | tail -40; fail "the reconverge apply failed"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$RECONVERGE_APPLY" \
    || { grep -E 'Apply complete' <<< "$RECONVERGE_APPLY"; fail "the reconverge apply did not change exactly 1 resource"; }
  FIXED_VALUE="$(awsl dynamodb list-tags-of-resource --resource-arn "$TABLE_ARN" --query "Tags[?Key=='Terraform'].Value | [0]" --output text)"
  [ "$FIXED_VALUE" = "true" ] || fail "$TABLE_ARN's Terraform tag is \"$FIXED_VALUE\" after reconverging, not \"true\""
  log "  reconverged: $TABLE_ARN's Terraform tag is back to \"true\""

  log ""
  log "STAGE 5 (drift and reconverge): PASS"
  gauntlet_stage drift_reconverge pass "one object tampered ($TABLE_ARN's Terraform tag), plan proposed fixing exactly one object, apply changed 1 and reconverged the tag"
  log ""

  # ════════════════════════════════════════════════════════════════════════
  # PART D: RENAME (day2_rename, active - live/GAUNTLET.md #6)
  # ════════════════════════════════════════════════════════════════════════
  #
  # module.dynamodb_table is this estate's only real module and the table is
  # its only marker-bearing object (see the D-ORACLE comment above stage 2 -
  # random_pet.this never reaches the adopted config at all, pinned to a
  # literal ahead of migrate by the #314 DELTA). Both mechanisms therefore
  # run on the SAME module, one right after the other: a `moved` block first
  # (module.dynamodb_table -> module.dynamodb_table_moved), then "choudoufu
  # live-mv" second (module.dynamodb_table_moved -> module.dynamodb_
  # table_final, no moved block for that hop at all). The resource policy
  # (module.dynamodb_table.aws_dynamodb_resource_policy.this[0]) carries no
  # tags and no marker of its own - its identity is derived entirely from
  # the table's own name argument, which neither rename touches - so it is
  # expected to show no diff of any kind across either hop.
  #
  # BREAK=6 (not 1: this script's own stage 3 and stage 5 already corrupt
  # their assertions under BREAK=1 and exit through fail() long before this
  # point, the same collision issue corpus-eks-basic's own header documents
  # for its stage 2 vs stage 3 - see "the stage-3-only value exists" there)
  # exercises this stage's own break control instead of the real checks:
  # renaming module.dynamodb_table WITHOUT a moved block, which must make
  # choudoufu propose destroying the old address's table and creating the
  # new one - the opposite of every other assertion in this part.
  gauntlet_begin_stage day2_rename
  log "=== D0. capture the live table this rename must not disturb ==="
  log "  $TABLE_ARN (module.dynamodb_table.aws_dynamodb_table.this[0])"

  if [ "${BREAK:-}" = "6" ]; then
    log "=== D1 (BREAK=6). rename module.dynamodb_table -> module.dynamodb_table_final WITHOUT a moved block ==="
    sed -i.bak 's/module "dynamodb_table" {/module "dynamodb_table_final" {/' "$EX/main.tf"
    sed -i.bak 's/module\.dynamodb_table\./module.dynamodb_table_final./g' "$EX/outputs.tf"
    rm -f "$EX/main.tf.bak" "$EX/outputs.tf.bak"
    ( cd "$EX" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$EX" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the BREAK=6 rename's reinit failed"; }
    BREAK_PLAN_OUT="$(plan_into 2>&1)"; BREAK_PLAN_RC=$?
    [ "$BREAK_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -30; fail "the BREAK=6 rename-without-moved plan exited $BREAK_PLAN_RC"; }
    grep -qE '^  # module\.dynamodb_table\.aws_dynamodb_table\.this\[0\] will be destroyed' <<< "$BREAK_PLAN_OUT" \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=6: renaming without a moved block did not propose destroying module.dynamodb_table.aws_dynamodb_table.this[0] - this stage's check is not load-bearing"; }
    grep -qE '^  # module\.dynamodb_table_final\.aws_dynamodb_table\.this\[0\] will be created' <<< "$BREAK_PLAN_OUT" \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=6: renaming without a moved block did not propose creating module.dynamodb_table_final.aws_dynamodb_table.this[0] - this stage's check is not load-bearing"; }
    log "  BREAK=6: correctly proposes destroying module.dynamodb_table.aws_dynamodb_table.this[0] and creating module.dynamodb_table_final.aws_dynamodb_table.this[0] - the moved-block and live-mv checks below are skipped"
  else
    log "=== D1. choudoufu, moved block: module.dynamodb_table -> module.dynamodb_table_moved ==="
    sed -i.bak 's/module "dynamodb_table" {/module "dynamodb_table_moved" {/' "$EX/main.tf"
    sed -i.bak 's/module\.dynamodb_table\./module.dynamodb_table_moved./g' "$EX/outputs.tf"
    rm -f "$EX/main.tf.bak" "$EX/outputs.tf.bak"
    cat >> "$EX/main.tf" <<'EOF'

moved {
  from = module.dynamodb_table
  to   = module.dynamodb_table_moved
}
EOF
    ( cd "$EX" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$EX" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the moved-block rename's reinit failed"; }
    MOVED_PLAN_OUT="$(plan_into 2>&1)"; MOVED_PLAN_RC=$?
    [ "$MOVED_PLAN_RC" -eq 0 ] || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -40; fail "the moved-block rename plan exited $MOVED_PLAN_RC"; }
    # RE-VERIFIED against current main (re-verify-day2_remove unit,
    # 2026-08): this used to be zero churn. Root cause is now precisely
    # named: 610511fb73 (internal/live/discovery/recordorphan_read.go,
    # #405's day2_remove fix) added recordOrphanReadSweep, which reads the
    # record store for any UNTAGGABLE type's undeclared old-address record
    # and proposes destroying it - generically, since its filter is
    # "untaggable + has a persisted identity record", not tied to any
    # specific type. Its own rename-safety check (the `pending` map, built
    # from res.Unbound) only recognizes "a declared instance of the SAME
    # address is unclaimed" - it never consults
    # moved.Aliases/moved.Honoured(req.Config) the way the marker path
    # already does. So this moved block, relocating module.dynamodb_table,
    # now destroys module.dynamodb_table.aws_dynamodb_resource_policy.this[0]
    # under the OLD address instead of matching it under the new one; the
    # tagged table itself still moves correctly via the marker path, which
    # DOES follow moved blocks. SAME root cause, independently confirmed on
    # corpus-giantswarm-crossplane, corpus-ec2-instance-complete,
    # corpus-rds-complete-postgres and corpus-security-group-complete in
    # this same unit - a generic gap now reaching at least five estates.
    # live-mv does not hit this (RecordStore.MoveRecord re-keys the store
    # directly, 8bd0d47e4e); only a bare HCL `moved` block does. Not fixed
    # here - a Go change, out of scope for this script-only
    # re-verification unit. Because fail() exits immediately, day2_remove's
    # own post-fix status for this estate could not be independently
    # re-measured this run.
    grep -qE '^  # .+ will be (destroyed|created)' <<< "$MOVED_PLAN_OUT" \
      && { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block rename now destroys module.dynamodb_table.aws_dynamodb_resource_policy.this[0] under the OLD address instead of zero churn - a regression traced to 610511fb73's recordOrphanReadSweep, which has no moved-block awareness (see the comment immediately above this assertion); the SAME generic gap corpus-giantswarm-crossplane, corpus-ec2-instance-complete, corpus-rds-complete-postgres and corpus-security-group-complete independently hit in this same unit. day2_remove's own post-fix status for this estate could not be re-measured this run because of it."; }
    grep -qE '^  # module\.dynamodb_table_moved\.aws_dynamodb_table\.this\[0\] will be updated in-place' <<< "$MOVED_PLAN_OUT" \
      || { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block plan does not propose an in-place update to module.dynamodb_table_moved.aws_dynamodb_table.this[0]"; }
    grep -qF 'Plan: 0 to add, 1 to change, 0 to destroy.' <<< "$MOVED_PLAN_OUT" \
      || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -10; fail "the moved-block rename plan is not exactly one in-place change - the resource policy's identity does not depend on the table's own address and must show no diff at all"; }
    grep -qE '~ +"tofu-address" = "module\.dynamodb_table\.aws_dynamodb_table\.this:0" -> "module\.dynamodb_table_moved\.aws_dynamodb_table\.this:0"' <<< "$MOVED_PLAN_OUT" \
      || { printf '%s\n' "$MOVED_PLAN_OUT"; fail "the moved-block plan does not show the table's tofu-address marker being rewritten from the old address to the new one"; }
    log "  choudoufu: zero churn, one in-place tags update - the resource policy's own identity is unaffected by the table's address"

    MOVED_APPLY_OUT="$(cd "$EX" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; MOVED_APPLY_RC=$?
    [ "$MOVED_APPLY_RC" -eq 0 ] || { printf '%s\n' "$MOVED_APPLY_OUT" | tail -40; fail "the moved-block rename apply exited $MOVED_APPLY_RC"; }
    grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$MOVED_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$MOVED_APPLY_OUT"; fail "the moved-block rename apply was not exactly one in-place change"; }

    TABLE_ARN_D1_AFTER="$(awsl dynamodb describe-table --table-name "$TABLE_NAME" --query 'Table.TableArn' --output text)"
    [ "$TABLE_ARN_D1_AFTER" = "$TABLE_ARN" ] || fail "the table's ARN changed across the rename ($TABLE_ARN -> $TABLE_ARN_D1_AFTER) - it was destroyed and recreated, not renamed"
    ADDR_D1_AFTER="$(awsl dynamodb list-tags-of-resource --resource-arn "$TABLE_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
    [ "$ADDR_D1_AFTER" = "module.dynamodb_table_moved.aws_dynamodb_table.this:0" ] \
      || fail "the table carries tofu-address=$ADDR_D1_AFTER after the rename, not module.dynamodb_table_moved.aws_dynamodb_table.this:0"
    log "  $TABLE_ARN unchanged, tofu-address now module.dynamodb_table_moved.aws_dynamodb_table.this:0 - read via the AWS CLI"

    log "=== D2. choudoufu, live-mv: module.dynamodb_table_moved -> module.dynamodb_table_final, no moved block at all ==="
    sed -i.bak 's/module "dynamodb_table_moved" {/module "dynamodb_table_final" {/' "$EX/main.tf"
    sed -i.bak 's/module\.dynamodb_table_moved\./module.dynamodb_table_final./g' "$EX/outputs.tf"
    rm -f "$EX/main.tf.bak" "$EX/outputs.tf.bak"
    ( cd "$EX" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$EX" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the live-mv rename's reinit failed"; }
    MV_OUT="$(cd "$EX" && "$TOFU" live-mv -estate="$ESTATE" 'module.dynamodb_table_moved.aws_dynamodb_table.this[0]' 'module.dynamodb_table_final.aws_dynamodb_table.this[0]' 2>&1)"; MV_RC=$?
    [ "$MV_RC" -eq 0 ] || { printf '%s\n' "$MV_OUT" | tail -30; fail "choudoufu live-mv exited $MV_RC"; }
    grep -qF 'Rewrote the ownership marker on one live resource. This was a cloud write.' <<< "$MV_OUT" \
      || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report a real write"; }
    grep -qF '"module.dynamodb_table_moved.aws_dynamodb_table.this:0" -> "module.dynamodb_table_final.aws_dynamodb_table.this:0"' <<< "$MV_OUT" \
      || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report rewriting the tofu-address marker from the old address to the new one"; }
    log "  live-mv: $(grep -F 'live ID' <<< "$MV_OUT")"

    TABLE_ARN_D2_AFTER="$(awsl dynamodb describe-table --table-name "$TABLE_NAME" --query 'Table.TableArn' --output text)"
    [ "$TABLE_ARN_D2_AFTER" = "$TABLE_ARN" ] || fail "the table's ARN changed across live-mv ($TABLE_ARN -> $TABLE_ARN_D2_AFTER) - it was destroyed and recreated, not renamed"
    ADDR_D2_AFTER="$(awsl dynamodb list-tags-of-resource --resource-arn "$TABLE_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
    [ "$ADDR_D2_AFTER" = "module.dynamodb_table_final.aws_dynamodb_table.this:0" ] \
      || fail "the table carries tofu-address=$ADDR_D2_AFTER after live-mv, not module.dynamodb_table_final.aws_dynamodb_table.this:0"
    log "  $TABLE_ARN unchanged, tofu-address now module.dynamodb_table_final.aws_dynamodb_table.this:0 - read via the AWS CLI"

    log "=== D3. one more plan: config and marker agree on both renames, nothing proposed ==="
    FINAL_PLAN_OUT="$(plan_into 2>&1)"; FINAL_PLAN_RC=$?
    [ "$FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$FINAL_PLAN_OUT" | tail -40; fail "the post-rename plan exited $FINAL_PLAN_RC"; }
    grep -qE '^  # .+ will be' <<< "$FINAL_PLAN_OUT" \
      && { grep -E '^  # .+ will be' <<< "$FINAL_PLAN_OUT"; fail "the post-rename plan is not empty"; }
    log "  no resource change proposed. Both renames are complete and invisible to the next plan."

    gauntlet_stage day2_rename pass "moved block: module.dynamodb_table renamed to module.dynamodb_table_moved with zero churn (0 add, 1 change, 0 destroy) - the table's own marker rewritten in place, the untaggable resource policy unaffected; live-mv: module.dynamodb_table_moved renamed to module.dynamodb_table_final with zero churn, marker rewritten in place; stock oracle over the same net module rename on cold_deploy's own state also shows zero churn (0 add, 0 change, 0 destroy); the table's ARN unchanged throughout, read via the AWS CLI"

    # ══════════════════════════════════════════════════════════════════════
    # PART F: REPLACE (day2_replace, active - live/GAUNTLET.md #9)
    # ══════════════════════════════════════════════════════════════════════
    #
    # Starts from Part D's real, completed state: module.dynamodb_table_final
    # (originally module.dynamodb_table) is bound and converged. Its `name`
    # argument - not the module CALL's own label, which this stage never
    # touches - changes to a new literal table name. aws_dynamodb_table's
    # `name` is ForceNew in the provider's real schema (confirmed by the
    # plan output itself below, not assumed: DynamoDB has no rename API,
    # only CreateTable/DeleteTable), so this forces a replacement at the
    # SAME declared address (module.dynamodb_table_final.aws_dynamodb_
    # table.this[0] never changes) while the physical live table behind it
    # is destroyed and a new one created - the marker moving onto the new
    # object is this stage's own Proves text. The resource policy's
    # `resource_arn` argument is required and not computed, and DynamoDB's
    # PutResourcePolicy/DeleteResourcePolicy APIs have no update path for
    # that key, so the new table's new ARN cascades into a forced replace
    # of the resource policy too (F-ORACLE's own finding, confirmed there).
    #
    # THE create_before_destroy SCOPE NOTE (same shape as corpus-ec2-
    # instance-complete's and corpus-sqs-basic's own Part F): this estate's
    # only real module wraps the terraform-aws-dynamodb-table registry
    # module, whose own source this corpus's established convention only
    # ever REMOVES real upstream content from, never adds library-internal
    # lifecycle blocks to, and OpenTofu core rejects a `lifecycle` block
    # written directly on a `module` call. So this evidence pass exercises
    # OpenTofu's DEFAULT replace ordering instead (destroy-then-create -
    # confirmed below by the plan's own "-/+ destroy and then create
    # replacement" legend). BREAK=replace manufactures the coexistence a
    # skipped destroy would leave behind directly via the AWS CLI.
    gauntlet_begin_stage day2_replace
    record_key() { printf '%s' "$1" | base64 | tr '+/' '-_' | tr -d '=\n'; }
    record_import_id() { jq -r '.identity.import_id' "$1"; }
    F_ADDR="module.dynamodb_table_final.aws_dynamodb_table.this[0]"
    F_RECORD="$EX/.tofu-records/tofu-records/$ESTATE/aws_dynamodb_table/$(record_key "$F_ADDR")"

    log "=== F0. capture the live table and its record ahead of the forced replace ==="
    [ -f "$F_RECORD" ] || fail "no local record file found for $F_ADDR ahead of day2_replace"
    F_OLD_IMPORT_ID="$(record_import_id "$F_RECORD")"
    [ "$F_OLD_IMPORT_ID" = "$TABLE_NAME" ] || fail "the record for $F_ADDR names $F_OLD_IMPORT_ID ahead of day2_replace, not $TABLE_NAME"
    F_OLD_ADDR_TAG="$(awsl dynamodb list-tags-of-resource --resource-arn "$TABLE_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
    [ "$F_OLD_ADDR_TAG" = "module.dynamodb_table_final.aws_dynamodb_table.this:0" ] \
      || fail "$TABLE_ARN does not carry tofu-address=module.dynamodb_table_final.aws_dynamodb_table.this:0 ahead of day2_replace"
    log "  $TABLE_ARN, record import_id=$F_OLD_IMPORT_ID, tofu-address=$F_OLD_ADDR_TAG"

    if [ "${BREAK:-}" = "replace" ]; then
      log "=== F1 (BREAK=replace). manufacture the coexistence a skipped destroy would leave behind ==="
      # A second, distinct live table carrying the SAME tofu-address and
      # tofu-slot as the one a genuine replace would destroy - the state
      # "skip the destroy half" of a create-before-destroy replace would
      # leave, produced directly via the AWS CLI rather than by actually
      # interrupting an apply (day2_crash's own job).
      BREAK_COLLISION_NAME="${TABLE_NAME}-collision"
      awsl dynamodb create-table --table-name "$BREAK_COLLISION_NAME" \
        --attribute-definitions AttributeName=id,AttributeType=N \
        --key-schema AttributeName=id,KeyType=HASH \
        --billing-mode PAY_PER_REQUEST \
        --tags "Key=tofu-estate,Value=$ESTATE" "Key=tofu-address,Value=module.dynamodb_table_final.aws_dynamodb_table.this:0" "Key=tofu-slot,Value=0" \
        >/dev/null || fail "BREAK=replace: could not create the collision table"
      for _ in $(seq 1 20); do
        [ "$(awsl dynamodb describe-table --table-name "$BREAK_COLLISION_NAME" --query 'Table.TableStatus' --output text 2>/dev/null)" = "ACTIVE" ] && break
        sleep 1
      done
      BREAK_PLAN_OUT="$(plan_into 2>&1)"; BREAK_PLAN_RC=$?
      awsl dynamodb delete-table --table-name "$BREAK_COLLISION_NAME" >/dev/null 2>&1 || true
      [ "$BREAK_PLAN_RC" -ne 0 ] \
        || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -20; fail "BREAK=replace: the plan succeeded with two live objects claiming the same tofu-address/tofu-slot - it must report the collision, not propose nothing"; }
      grep -qF 'Two live resources claiming one slot' <<< "$BREAK_PLAN_OUT" \
        || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -20; fail "BREAK=replace: the plan failed for a reason other than the slot collision - this stage's check is not load-bearing"; }
      log "  BREAK=replace: choudoufu correctly refused with a named collision (two live resources claiming one slot) rather than silently proposing nothing - the Break text's own outcome"
    else
      log "=== F1. choudoufu: change the ForceNew name argument, forcing a replace at the same declared address ==="
      sed -i.bak 's/name                        = "my-table-\${local\.pinned_pet}"/name                        = "my-table-${local.pinned_pet}-v2"/' "$EX/main.tf"
      rm -f "$EX/main.tf.bak"
      grep -q 'my-table-${local.pinned_pet}-v2' "$EX/main.tf" || fail "changing module.dynamodb_table_final's name argument did not match - the corpus pin has moved"
      F_NEW_NAME="my-table-${PET}-v2"

      F_PLAN_OUT="$(plan_into 2>&1)"; F_PLAN_RC=$?
      [ "$F_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_PLAN_OUT" | tail -40; fail "the day2_replace plan exited $F_PLAN_RC"; }
      grep -qE '^  # module\.dynamodb_table_final\.aws_dynamodb_table\.this\[0\] must be replaced' <<< "$F_PLAN_OUT" \
        || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not propose replacing module.dynamodb_table_final's table when its ForceNew name argument changes"; }
      grep -qE '~ +name +=.+forces replacement' <<< "$F_PLAN_OUT" \
        || { printf '%s\n' "$F_PLAN_OUT"; fail "the plan does not mark name as forcing replacement"; }
      log "  choudoufu: exactly one forced replace at the same declared address (module.dynamodb_table_final.aws_dynamodb_table.this[0]), name forces replacement"

      F_APPLY_OUT="$(cd "$EX" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; F_APPLY_RC=$?
      [ "$F_APPLY_RC" -eq 0 ] || { printf '%s\n' "$F_APPLY_OUT" | tail -40; fail "the day2_replace apply exited $F_APPLY_RC"; }
      grep -qE 'Apply complete! Resources: [0-9]+ added, [0-9]+ changed, [0-9]+ destroyed' <<< "$F_APPLY_OUT" \
        || { printf '%s\n' "$F_APPLY_OUT" | tail -20; fail "the day2_replace apply did not report a clean apply"; }
      log "  $(grep -E 'Apply complete' <<< "$F_APPLY_OUT")"

      if F_OLD_STILL="$(awsl dynamodb describe-table --table-name "$TABLE_NAME" 2>&1)"; then
        echo "$F_OLD_STILL"; fail "$TABLE_NAME still exists after the replace - the old object was orphaned, not destroyed"
      fi
      log "  $TABLE_NAME no longer exists (ResourceNotFoundException) - confirmed via the AWS CLI, not through choudoufu's own report"

      F_NEW_ARN="$(awsl dynamodb describe-table --table-name "$F_NEW_NAME" --query 'Table.TableArn' --output text)"
      [ -n "$F_NEW_ARN" ] && [ "$F_NEW_ARN" != "None" ] || fail "$F_NEW_NAME is not live after the replace"
      F_NEW_ADDR_TAG="$(awsl dynamodb list-tags-of-resource --resource-arn "$F_NEW_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
      [ "$F_NEW_ADDR_TAG" = "module.dynamodb_table_final.aws_dynamodb_table.this:0" ] \
        || fail "$F_NEW_ARN carries tofu-address=$F_NEW_ADDR_TAG after the replace, not module.dynamodb_table_final.aws_dynamodb_table.this:0 - the marker did not move onto the new object"
      log "  $F_NEW_ARN (the new object) carries tofu-address=$F_NEW_ADDR_TAG - the marker moved onto the new object, read via the AWS CLI"

      # THE RECORD STORE, asserted by value (HANDOFF's safety rule; the
      # #398-guard shape: a stale record still naming the destroyed table
      # would be exactly the wrong-marker failure that outranks a missing
      # one). The local record file at the SAME address must now hold the
      # NEW table's import_id, not the one captured in F0.
      F_NEW_IMPORT_ID="$(record_import_id "$F_RECORD")"
      [ "$F_NEW_IMPORT_ID" = "$F_NEW_NAME" ] \
        || fail "the record for $F_ADDR names $F_NEW_IMPORT_ID after the replace, not the new object $F_NEW_NAME - a stale record still claiming the destroyed object, the #398-guard shape"
      [ "$F_NEW_IMPORT_ID" != "$F_OLD_IMPORT_ID" ] \
        || fail "sanity: the record's import_id at $F_ADDR did not change at all across the replace"
      log "  record store: import_id $F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID at the same key ($F_ADDR) - read directly off the local record store file, not through choudoufu's own report"

      log "=== F2. one more plan: config and reality agree, no marker collision ==="
      F_FINAL_PLAN_OUT="$(plan_into 2>&1)"; F_FINAL_PLAN_RC=$?
      [ "$F_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_FINAL_PLAN_OUT" | tail -40; fail "the post-replace plan exited $F_FINAL_PLAN_RC"; }
      grep -qE '^  # .+ will be' <<< "$F_FINAL_PLAN_OUT" \
        && { printf '%s\n' "$F_FINAL_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the post-replace plan proposes a resource change"; }
      log "  no resource action proposed, no marker collision. The replace is complete and invisible to the next plan."

      TABLE_NAME="$F_NEW_NAME"
      TABLE_ARN="$F_NEW_ARN"
      gauntlet_stage day2_replace pass "choudoufu: changing module.dynamodb_table_final's ForceNew name argument proposed exactly one table replace at the same declared address, cascading into the untaggable resource policy (its resource_arn argument follows the table's ARN and is not independently updatable, so it also replaces - F-ORACLE's own finding); applied cleanly; the old table is confirmed gone via the AWS CLI (ResourceNotFoundException) and the new table carries the marker; the local record store's record at the same address now names the new table's name, not the destroyed one ($F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID); the next plan proposes no resource action; stock oracle on cold_deploy's own state (F-ORACLE) also proposes replacing the table at the same address (plan only, not applied - it shares floci's account with \$EST); BREAK=replace confirms a manufactured marker collision is reported loudly (\"Two live resources claiming one slot\") rather than silently proposed as nothing. Scope note: this exercises OpenTofu's default destroy-then-create ordering, not the create_before_destroy variant the stage's Title names - see this section's own header comment and corpus-ec2-instance-complete's/corpus-sqs-basic's matching ones."
    fi
    gauntlet_end_stage

    # ══════════════════════════════════════════════════════════════════════
    # PART E: REMOVE A BLOCK (day2_remove, active - live/GAUNTLET.md #7)
    # ══════════════════════════════════════════════════════════════════════
    #
    # Starts from Part D's real, completed state: module.dynamodb_table_final
    # (originally module.dynamodb_table) is bound and converged - the only
    # real module in this estate other than module.disabled_dynamodb_table,
    # whose create_table=false leaves it with ZERO declared instances of
    # aws_dynamodb_table.this, so it can never be a same-blockKey ambiguity
    # for discovery.go's classifyOrphans (there is no unclaimed declared
    # instance of that key anywhere in the estate to withhold against, unlike
    # corpus-iam-policy's stronger case). Removing module.dynamodb_table_
    # final's block destroys BOTH of its resources - the table AND its
    # resource policy, an untaggable child of the table - together, in
    # whatever order the dependency graph requires (the policy depends on
    # the table's ARN, so it is destroyed first); outputs.tf references only
    # this module's own outputs, so it is emptied rather than edited output
    # by output, the same as the D-REMOVE-ORACLE copy above.
    gauntlet_begin_stage day2_remove
    log "=== E0. capture the live table this removal destroys ==="
    E_ARN_BEFORE="$(awsl dynamodb list-tags-of-resource --resource-arn "$TABLE_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text 2>/dev/null || true)"
    [ "$E_ARN_BEFORE" = "module.dynamodb_table_final.aws_dynamodb_table.this:0" ] \
      || fail "$TABLE_ARN does not carry tofu-address=module.dynamodb_table_final.aws_dynamodb_table.this:0 before day2_remove even starts (got $E_ARN_BEFORE)"

    if [ "${BREAK_REMOVE:-}" = "1" ]; then
      log "=== E1 (BREAK_REMOVE=1). keep module.dynamodb_table_final's block; no destroy may be proposed ==="
      BREAK_REMOVE_PLAN_OUT="$(plan_into 2>&1)"; BREAK_REMOVE_PLAN_RC=$?
      [ "$BREAK_REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_REMOVE_PLAN_OUT" | tail -40; fail "the BREAK_REMOVE=1 kept-block plan exited $BREAK_REMOVE_PLAN_RC"; }
      grep -qE '^  # .+ will be (created|destroyed)' <<< "$BREAK_REMOVE_PLAN_OUT" \
        && { grep -E '^  # .+ will be' <<< "$BREAK_REMOVE_PLAN_OUT"; fail "BREAK_REMOVE=1: a resource action was proposed with the block still in the config - this stage's check is not load-bearing"; }
      log "  BREAK_REMOVE=1: correctly proposes no resource action - the block is still declared"
    else
      log "=== E1. choudoufu: delete module.dynamodb_table_final's block ==="
      perl -0777pi -e 's/module "dynamodb_table_final" \{.*?\n\}\n\n\n//s' "$EX/main.tf"
      grep -q 'module "dynamodb_table_final"' "$EX/main.tf" \
        && fail "removing module.dynamodb_table_final's block did not match - the config has moved"
      : > "$EX/outputs.tf"
      ( cd "$EX" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
        ( cd "$EX" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the day2_remove reinit failed"; }
      REMOVE_PLAN_OUT="$(plan_into 2>&1)"; REMOVE_PLAN_RC=$?
      [ "$REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -40; fail "the day2_remove plan exited $REMOVE_PLAN_RC"; }
      if grep -q 'is unclaimed, so this may be the same resource under a new instance key' <<< "$REMOVE_PLAN_OUT"; then
        printf '%s\n' "$REMOVE_PLAN_OUT" | tail -40
        fail "choudoufu withheld a destroy of module.dynamodb_table_final's resources as a possible rename (discovery.go's classifyOrphans) even though module.disabled_dynamodb_table declares zero instances of the same block key - this is the honest wall issue #358 names, not a pass"
      fi
      grep -qE '^  # module\.dynamodb_table_final\.aws_dynamodb_table\.this\[0\] will be destroyed' <<< "$REMOVE_PLAN_OUT" \
        || { printf '%s\n' "$REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu does not propose destroying module.dynamodb_table_final's table when its block is deleted"; }
      if ! grep -qE '^  # module\.dynamodb_table_final\.aws_dynamodb_resource_policy\.this\[0\] will be destroyed' <<< "$REMOVE_PLAN_OUT"; then
        # A REAL, DOCUMENTED gap, not a surprise: aws_dynamodb_resource_policy
        # is admitted by the provider's own identity schema rather than by
        # the generated admission table (live/LIMITATIONS.md, "Resource type
        # has no orphan recovery" - "the estate-wide sweep draws its type
        # universe from that table and will not list it... deleting its
        # last block leaves the live resource with no run proposing to
        # remove it"). Confirmed live here: the table alone is proposed for
        # destroy, the resource policy is not, and it is never even
        # mentioned - not withheld as a possible rename, simply invisible to
        # the destroy sweep. This is choudoufu having LESS destroy coverage
        # than stock (row 2 of HANDOFF's five-row table: the plans differ),
        # not a stricter refusal - recorded as a genuine fail rather than
        # chased here (script-only pass; the fix belongs with #387's
        # schema-first admission work).
        printf '%s\n' "$REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'
        log "  choudoufu proposes destroying the table but not its resource policy - a real gap, not this stage's own load-bearing check failing"
        gauntlet_stage day2_remove fail "choudoufu's remove plan destroys module.dynamodb_table_final's table but never mentions its resource policy at all when the block is deleted: aws_dynamodb_resource_policy is admitted by the provider's own identity schema rather than by the generated admission table (live/LIMITATIONS.md, \"Resource type has no orphan recovery\"), so the estate-wide destroy sweep does not know the type exists and proposes nothing for it - the object is left live and orphaned. Stock oracle on cold_deploy's own state (D-REMOVE-ORACLE) proposes exactly two destroys for the same two objects (0 add, 0 change, 2 destroy); choudoufu here has strictly less destroy coverage than stock, a real gap tracked at live/LIMITATIONS.md, not fixed in this script-only pass"
      else
        grep -qF 'Plan: 0 to add, 0 to change, 2 to destroy.' <<< "$REMOVE_PLAN_OUT" \
          || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -10; fail "choudoufu's remove plan proposes something other than exactly two destroys"; }
        log "  choudoufu: exactly two destroys (the table and its resource policy), nothing else"

        REMOVE_APPLY_OUT="$(cd "$EX" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; REMOVE_APPLY_RC=$?
        [ "$REMOVE_APPLY_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_APPLY_OUT" | tail -40; fail "the day2_remove apply exited $REMOVE_APPLY_RC"; }
        grep -qE 'Resources: 0 added, 0 changed, 2 destroyed' <<< "$REMOVE_APPLY_OUT" \
          || { grep -E 'Apply complete' <<< "$REMOVE_APPLY_OUT"; fail "the day2_remove apply was not exactly two destroys"; }

        # DynamoDB, like IAM (checked directly while building corpus-iam-
        # policy's own Part E) and unlike EC2's describe-internet-gateways,
        # answers describe-table for a deleted table with a real
        # ResourceNotFoundException and a non-zero exit - confirmed the same
        # way, a standalone create/delete/describe-table sequence against
        # floci with no tofu in the loop at all - so "the AWS CLI call
        # succeeded" is the right test here.
        if E_STILL="$(awsl dynamodb describe-table --table-name "$TABLE_NAME" 2>&1)"; then
          echo "$E_STILL"; fail "$TABLE_NAME still exists in the live account after the destroy - it was orphaned, not destroyed"
        fi
        log "  $TABLE_NAME no longer exists (ResourceNotFoundException) - confirmed via the AWS CLI, not through choudoufu's own report"

        log "=== E2. one more plan: config and reality agree, nothing left to propose ==="
        E_FINAL_PLAN_OUT="$(plan_into 2>&1)"; E_FINAL_PLAN_RC=$?
        [ "$E_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$E_FINAL_PLAN_OUT" | tail -40; fail "the post-remove plan exited $E_FINAL_PLAN_RC"; }
        grep -qE '^  # .+ will be' <<< "$E_FINAL_PLAN_OUT" \
          && { grep -E '^  # .+ will be' <<< "$E_FINAL_PLAN_OUT"; fail "the post-remove plan is not empty"; }
        log "  No changes. The removal is complete and invisible to the next plan."

        gauntlet_stage day2_remove pass "choudoufu: deleting module.dynamodb_table_final's block proposed exactly two destroys (0 add, 0 change, 2 destroy: the table and its untaggable resource policy), applied cleanly (0 added, 0 changed, 2 destroyed), the table is genuinely gone from the live account (dynamodb describe-table on the old name now returns ResourceNotFoundException, read via the AWS CLI, not choudoufu's own report), and the next plan proposes no resource action; stock oracle on cold_deploy's own state (D-REMOVE-ORACLE) also proposes exactly two destroys for the same two objects; classifyOrphans did not withhold either destroy because module.disabled_dynamodb_table declares zero instances of the same block key (create_table=false), so nothing is ever pending against it"

        # ════════════════════════════════════════════════════════════════════
        # PART G: CHANGE COUNT (day2_count, active - live/GAUNTLET.md #8,
        # issue #359/#488)
        # ════════════════════════════════════════════════════════════════════
        #
        # Starts from Part E's real, completed state: module.dynamodb_table_
        # final and its resource policy are gone (Part E just destroyed this
        # estate's only real objects). A NEW, entirely synthetic resource
        # (aws_dynamodb_table.count_test, count_test_block() defined above
        # G-ORACLE, far above STAGE 2) is added here, in its own file, so
        # day2_count's own history is self-contained and never revisits an
        # address any other stage already used - the same discipline
        # live/e2e/reference-ec2-vpc/run.sh's own Part F and
        # live/e2e/corpus-iam-read-only-policy/run.sh's own PART G use.
        # G-ORACLE above is the stock oracle for the identical shape,
        # applied for real in a dedicated, always-idle account never shared
        # with this one (the trap PR #502 found: a stock oracle sharing an
        # endpoint with the real leg can poison the real leg's own lookup).
        #
        # tofu-address's TAG VALUE is colon-escaped for a count instance
        # (live/MARKERS.md): aws_dynamodb_table.count_test:0, never the
        # bracket form the plan's own CLI text uses.
        #
        # THE RECORD FILE ON A DESTROY: TOMBSTONED, NOT REMOVED (the
        # #398-guard shape; corpus-mastino-dns's own day2_count unit
        # measured this same trap first, commit 0ad667f847). A destroyed
        # count instance's local record is not erased - its top-level
        # "identity" block is replaced with a "tombstone" entry (the
        # destroyed identity's own attrs plus a timestamp), so nothing
        # later can misread a leftover file as still naming a live object.
        # record_tombstoned() below asserts that shape directly
        # (has("tombstone") and not has("identity")) rather than assuming
        # the file for the destroyed higher index is simply gone.
        #
        # BREAK_COUNT=1 exercises this stage's own Break control instead of
        # the real checks: after the real scale-down plan, assert the WRONG
        # instance (count_test[0] rather than count_test[1]) was the one
        # destroyed - the Break text in tools/gauntlet/stages.go for
        # day2_count, verbatim: "Expect a different instance to be
        # destroyed; the assertion must fail." Only reachable when BREAK is
        # not 6 and BREAK_REMOVE is not 1, because PART G starts from
        # PART E's real, completed removal.
        gauntlet_begin_stage day2_count
        record_tombstoned() { jq -e 'has("tombstone") and (has("identity") | not)' "$1" >/dev/null 2>&1; }

        log "=== G0. choudoufu: add aws_dynamodb_table.count_test, count = 2 ==="
        count_test_block 2 > "$EX/day2_count.tf"
        ( cd "$EX" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
          ( cd "$EX" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the count-block-add reinit failed"; }
        COUNT_ADD_PLAN_OUT="$(plan_into 2>&1)"; COUNT_ADD_PLAN_RC=$?
        [ "$COUNT_ADD_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_ADD_PLAN_OUT" | tail -30; fail "the count-block-add plan exited $COUNT_ADD_PLAN_RC"; }
        grep -qF 'Plan: 2 to add, 0 to change, 0 to destroy.' <<< "$COUNT_ADD_PLAN_OUT" \
          || { printf '%s\n' "$COUNT_ADD_PLAN_OUT" | tail -10; fail "adding the count block did not plan exactly 2 creates"; }
        COUNT_ADD_APPLY_OUT="$(cd "$EX" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_ADD_APPLY_RC=$?
        [ "$COUNT_ADD_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_ADD_APPLY_OUT" | tail -30; fail "the count-block-add apply exited $COUNT_ADD_APPLY_RC"; }
        grep -qE 'Resources: 2 added, 0 changed, 0 destroyed' <<< "$COUNT_ADD_APPLY_OUT" \
          || { grep -E 'Apply complete' <<< "$COUNT_ADD_APPLY_OUT"; fail "the count-block-add apply did not create exactly 2 resources"; }

        G_CT0_ARN="$(awsl dynamodb describe-table --table-name dynamodb-count-test-0 --query 'Table.TableArn' --output text)"
        G_CT1_ARN="$(awsl dynamodb describe-table --table-name dynamodb-count-test-1 --query 'Table.TableArn' --output text)"
        [ -n "$G_CT0_ARN" ] && [ "$G_CT0_ARN" != "None" ] || fail "no live count_test[0] table found by name"
        [ -n "$G_CT1_ARN" ] && [ "$G_CT1_ARN" != "None" ] || fail "no live count_test[1] table found by name"
        G_CT0_ADDR_TAG="$(awsl dynamodb list-tags-of-resource --resource-arn "$G_CT0_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
        G_CT1_ADDR_TAG="$(awsl dynamodb list-tags-of-resource --resource-arn "$G_CT1_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
        [ "$G_CT0_ADDR_TAG" = 'aws_dynamodb_table.count_test:0' ] || fail "count_test[0]'s live tofu-address tag is $G_CT0_ADDR_TAG, not aws_dynamodb_table.count_test:0 (live/MARKERS.md: a count instance's tag value is colon-escaped, e.g. aws_eip.this[2] -> aws_eip.this:2)"
        [ "$G_CT1_ADDR_TAG" = 'aws_dynamodb_table.count_test:1' ] || fail "count_test[1]'s live tofu-address tag is $G_CT1_ADDR_TAG, not aws_dynamodb_table.count_test:1"
        G_CT0_ID="$(awsl dynamodb describe-table --table-name dynamodb-count-test-0 --query 'Table.TableId' --output text)"
        G_CT1_ID="$(awsl dynamodb describe-table --table-name dynamodb-count-test-1 --query 'Table.TableId' --output text)"
        [ -n "$G_CT0_ID" ] && [ "$G_CT0_ID" != "None" ] || fail "live count_test[0] has no TableId"
        [ -n "$G_CT1_ID" ] && [ "$G_CT1_ID" != "None" ] || fail "live count_test[1] has no TableId"
        log "  2 instances created: index 0 = $G_CT0_ARN (tofu-address=$G_CT0_ADDR_TAG, id=$G_CT0_ID), index 1 = $G_CT1_ARN (tofu-address=$G_CT1_ADDR_TAG, id=$G_CT1_ID) - read via the AWS CLI"

        COUNT_NOOP_PLAN_OUT="$(plan_into 2>&1)"; COUNT_NOOP_PLAN_RC=$?
        [ "$COUNT_NOOP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_NOOP_PLAN_OUT" | tail -30; fail "the post-add plan exited $COUNT_NOOP_PLAN_RC"; }
        grep -qE '^  # .+ will be' <<< "$COUNT_NOOP_PLAN_OUT" \
          && { grep -E '^  # .+ will be' <<< "$COUNT_NOOP_PLAN_OUT"; fail "the plan right after adding the count block is not empty - the new instances did not bind their own markers cleanly"; }
        log "  no resource change proposed - both new instances plan empty immediately after creation"

        G_ADDR1="aws_dynamodb_table.count_test[1]"
        G_RECORD1="$EX/.tofu-records/tofu-records/$ESTATE/aws_dynamodb_table/$(record_key "$G_ADDR1")"
        [ -f "$G_RECORD1" ] || fail "no local record file found for $G_ADDR1 ahead of the scale-down"

        log "=== G1. scale count down: 2 -> 1 ==="
        count_test_block 1 > "$EX/day2_count.tf"
        COUNT_DOWN_PLAN_OUT="$(plan_into 2>&1)"; COUNT_DOWN_PLAN_RC=$?
        [ "$COUNT_DOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | tail -30; fail "the scale-down plan exited $COUNT_DOWN_PLAN_RC"; }

        if [ "${BREAK_COUNT:-}" = "1" ]; then
          log "  BREAK_COUNT=1: asserting the WRONG instance (count_test[0]) was destroyed instead of count_test[1]"
          if grep -qE '^  # aws_dynamodb_table\.count_test\[0\] will be destroyed' <<< "$COUNT_DOWN_PLAN_OUT"; then
            fail "BREAK_COUNT=1: the plan actually destroys count_test[0] - this assertion is not load-bearing"
          fi
          log "  BREAK_COUNT=1: correctly does NOT destroy count_test[0] - the wrong-instance assertion above fails to hold, as it must"
        else
          grep -qE '^  # aws_dynamodb_table\.count_test\[1\] will be destroyed' <<< "$COUNT_DOWN_PLAN_OUT" \
            || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-down plan does not destroy count_test[1]"; }
          grep -qE '^  # aws_dynamodb_table\.count_test\[0\] will be' <<< "$COUNT_DOWN_PLAN_OUT" \
            && { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-down plan touches count_test[0], which should be untouched"; }
          grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$COUNT_DOWN_PLAN_OUT" \
            || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | tail -10; fail "choudoufu's scale-down plan proposes something other than exactly one destroy"; }
          log "  choudoufu: exactly one destroy (count_test[1]), count_test[0] untouched"

          COUNT_DOWN_APPLY_OUT="$(cd "$EX" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_DOWN_APPLY_RC=$?
          [ "$COUNT_DOWN_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_DOWN_APPLY_OUT" | tail -30; fail "the scale-down apply exited $COUNT_DOWN_APPLY_RC"; }
          grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$COUNT_DOWN_APPLY_OUT" \
            || { grep -E 'Apply complete' <<< "$COUNT_DOWN_APPLY_OUT"; fail "the scale-down apply was not exactly one destroy"; }

          G_CT0_ID_AFTER_DOWN="$(awsl dynamodb describe-table --table-name dynamodb-count-test-0 --query 'Table.TableId' --output text 2>/dev/null || true)"
          [ "$G_CT0_ID_AFTER_DOWN" = "$G_CT0_ID" ] || fail "count_test[0]'s TableId changed across the scale-down ($G_CT0_ID -> $G_CT0_ID_AFTER_DOWN) - it was destroyed and recreated, not left alone"
          if G_CT1_STILL="$(awsl dynamodb describe-table --table-name dynamodb-count-test-1 2>&1)"; then
            echo "$G_CT1_STILL"; fail "count_test[1] still exists in the live account after the scale-down destroy - it was orphaned, not destroyed"
          fi
          G_CT0_ADDR_AFTER_DOWN="$(awsl dynamodb list-tags-of-resource --resource-arn "$G_CT0_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
          [ "$G_CT0_ADDR_AFTER_DOWN" = 'aws_dynamodb_table.count_test:0' ] || fail "count_test[0]'s tofu-address tag changed across the scale-down: $G_CT0_ADDR_AFTER_DOWN"
          log "  $G_CT1_ARN (count_test[1]) no longer exists (ResourceNotFoundException); $G_CT0_ARN (count_test[0]) unchanged TableId ($G_CT0_ID) and marker - all read via the AWS CLI"

          record_tombstoned "$G_RECORD1" \
            || { cat "$G_RECORD1" >&2; fail "the record for $G_ADDR1 after the scale-down destroy is not a proper tombstone (#398-guard shape: still carries a live identity block, or carries neither)"; }
          log "  the local record for $G_ADDR1 is correctly tombstoned (has(\"tombstone\") and not has(\"identity\")), not simply removed - read directly off the local record store, not through choudoufu's own report"

          log "=== G2. scale count back up: 1 -> 2 ==="
          count_test_block 2 > "$EX/day2_count.tf"
          COUNT_UP_PLAN_OUT="$(plan_into 2>&1)"; COUNT_UP_PLAN_RC=$?
          [ "$COUNT_UP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | tail -30; fail "the scale-up plan exited $COUNT_UP_PLAN_RC"; }
          grep -qE '^  # aws_dynamodb_table\.count_test\[1\] will be created' <<< "$COUNT_UP_PLAN_OUT" \
            || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-up plan does not create count_test[1]"; }
          grep -qE '^  # aws_dynamodb_table\.count_test\[0\] will be' <<< "$COUNT_UP_PLAN_OUT" \
            && { printf '%s\n' "$COUNT_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-up plan touches count_test[0], which should be untouched"; }
          grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$COUNT_UP_PLAN_OUT" \
            || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | tail -10; fail "choudoufu's scale-up plan proposes something other than exactly one create"; }
          log "  choudoufu: exactly one create (count_test[1]), count_test[0] untouched"

          COUNT_UP_APPLY_OUT="$(cd "$EX" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_UP_APPLY_RC=$?
          [ "$COUNT_UP_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_UP_APPLY_OUT" | tail -30; fail "the scale-up apply exited $COUNT_UP_APPLY_RC"; }
          grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$COUNT_UP_APPLY_OUT" \
            || { grep -E 'Apply complete' <<< "$COUNT_UP_APPLY_OUT"; fail "the scale-up apply was not exactly one create"; }

          G_CT1_NEW_ARN="$(awsl dynamodb describe-table --table-name dynamodb-count-test-1 --query 'Table.TableArn' --output text)"
          [ -n "$G_CT1_NEW_ARN" ] && [ "$G_CT1_NEW_ARN" != "None" ] || fail "no live count_test[1] table found after the scale-up"
          [ "$G_CT1_NEW_ARN" = "$G_CT1_ARN" ] || fail "the recreated count_test[1]'s ARN ($G_CT1_NEW_ARN) differs from its pre-destroy ARN ($G_CT1_ARN) - unexpected: aws_dynamodb_table's ARN is region/account/name-derived and should be identical both times"
          G_CT1_NEW_ID="$(awsl dynamodb describe-table --table-name dynamodb-count-test-1 --query 'Table.TableId' --output text)"
          [ "$G_CT1_NEW_ID" != "$G_CT1_ID" ] || fail "count_test[1] came back with the SAME TableId ($G_CT1_ID) it had before being destroyed - the destroy in G1 was not real"
          G_CT1_NEW_ADDR_TAG="$(awsl dynamodb list-tags-of-resource --resource-arn "$G_CT1_NEW_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
          [ "$G_CT1_NEW_ADDR_TAG" = 'aws_dynamodb_table.count_test:1' ] || fail "the recreated count_test[1] ($G_CT1_NEW_ARN) carries tofu-address=$G_CT1_NEW_ADDR_TAG, not aws_dynamodb_table.count_test:1"
          G_CT0_ID_AFTER_UP="$(awsl dynamodb describe-table --table-name dynamodb-count-test-0 --query 'Table.TableId' --output text)"
          [ "$G_CT0_ID_AFTER_UP" = "$G_CT0_ID" ] || fail "count_test[0]'s TableId changed across the scale-up"
          log "  count_test[1] recreated under the same ARN ($G_CT1_NEW_ARN, deterministic from region+account+name) but a NEW TableId ($G_CT1_NEW_ID, was $G_CT1_ID), tofu-address=$G_CT1_NEW_ADDR_TAG; count_test[0] ($G_CT0_ARN, id=$G_CT0_ID) untouched throughout the down-then-up cycle - all read via the AWS CLI"

          G_RECORD1_ID="$(record_import_id "$G_RECORD1")"
          [ "$G_RECORD1_ID" = "dynamodb-count-test-1" ] || fail "the record for $G_ADDR1 after the scale-up names import_id=$G_RECORD1_ID, not dynamodb-count-test-1 - the tombstone was not cleared back to a live identity"
          log "  the local record for $G_ADDR1 is a live identity again (import_id=$G_RECORD1_ID), not still a tombstone - read directly off the local record store"

          log "=== G3. one more plan: config and reality agree, nothing left to propose ==="
          COUNT_FINAL_PLAN_OUT="$(plan_into 2>&1)"; COUNT_FINAL_PLAN_RC=$?
          [ "$COUNT_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_FINAL_PLAN_OUT" | tail -30; fail "the post-scale-up plan exited $COUNT_FINAL_PLAN_RC"; }
          grep -qE '^  # .+ will be' <<< "$COUNT_FINAL_PLAN_OUT" \
            && { grep -E '^  # .+ will be' <<< "$COUNT_FINAL_PLAN_OUT"; fail "the post-scale-up plan is not empty"; }
          log "  no resource change proposed. The scale-down-then-up cycle is complete and invisible to the next plan."

          gauntlet_stage day2_count pass "choudoufu: scaling the synthetic aws_dynamodb_table.count_test from 2 to 1 (issue #359/#488's own fallback clause - this estate's real module has no honest resource-level count/for_each knob: create_table is boolean-shaped and replica_regions/global_secondary_indexes drive dynamic blocks nested inside the SAME table resource, not a separate resource instance, confirmed by reading main.tf directly) destroyed exactly count_test[1] (0 add, 0 change, 1 destroy), confirmed gone via the AWS CLI, its local record correctly tombstoned rather than left claiming a live identity (#398-guard shape, has(tombstone) and not has(identity)), and left count_test[0]'s live TableId and tofu-address marker unchanged; scaling back from 1 to 2 created exactly count_test[1] again under the SAME ARN (deterministic from region+account+name - established directly against floci with no tofu in the loop before writing this assertion) but a NEW TableId (0 add -> 1 add, 0 change, 0 destroy), and its local record returned to a live identity, while count_test[0] stayed untouched throughout; the next plan is empty; the G-ORACLE stock oracle on the identical 2-instance count block, applied for real in a dedicated always-idle account never shared with this one, shows the identical shape: destroy the higher index only, create it back under the same ARN but a new TableId, the lower index's TableId unchanged both times. BREAK_COUNT=1 confirms the wrong-instance assertion correctly fails to hold."
        fi
        rm -f "$EX/day2_count.tf"
        gauntlet_end_stage
      fi
    fi
    gauntlet_end_stage
  fi
  gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# PART GREENFIELD (greenfield, live/GAUNTLET.md #13, active)
# ══════════════════════════════════════════════════════════════════════════
#
# A SEPARATE fresh namespace from everything above: greenfield means from
# nothing, so this never touches the objects STAGE 1's plain terraform apply
# created (those get migrated in STAGE 2, below). choudoufu applies the
# reduced example directly, with a live block from the start, no migration,
# no state file ever existing; the record store must hold one record per
# instance (random_pet, the table, the resource policy - #364 A2, apply
# writes a record too, not just live-import); and the estate's own oracle is
# stock applying the SAME config fresh in a THIRD, independent namespace,
# compared structurally via the AWS CLI on both endpoints, never through
# tofu state. Unlike STAGE 2's migrate path below, this never needs the
# #314 random_pet DELTA: random_pet.this is applied by the SAME run, before
# the table, so its id is a real, already-known value by the time the
# table's `name` argument is evaluated - the wall #314 names is specific to
# live-import resolving an identity argument through a state-derived record,
# a path a from-nothing apply never takes.
gauntlet_begin_stage greenfield
log "=== PART GREENFIELD: 0. two more floci containers, one per fresh namespace ==="
docker run -d --rm -p "${FLOCI_GREEN_PORT}:4566" --name "$FLOCI_GREEN_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_GREEN_NAME failed"
docker run -d --rm -p "${FLOCI_GREEN_ORACLE_PORT}:4566" --name "$FLOCI_GREEN_ORACLE_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_GREEN_ORACLE_NAME failed"
for gep in "$GREEN_ENDPOINT" "$GREEN_ORACLE_ENDPOINT"; do
  GH=""
  for _ in $(seq 1 45); do
    GH="$(curl -fs "${gep}/_localstack/health" 2>/dev/null)" || true
    grep -q '"dynamodb"' <<< "${GH:-}" && break
    sleep 2
  done
  grep -q '"dynamodb"' <<< "${GH:-}" || fail "floci did not come up healthy (dynamodb) at $gep"
done
log "  healthy: greenfield=$GREEN_ENDPOINT oracle=$GREEN_ORACLE_ENDPOINT"

# Fresh copies of the WHOLE module tree, preserving the "../.." relative
# source path examples/basic expects, one per namespace.
mkdir -p "$WORK/green/examples" "$WORK/green-oracle"
cp "$SRC_ROOT/main.tf" "$SRC_ROOT/variables.tf" "$SRC_ROOT/outputs.tf" "$SRC_ROOT/versions.tf" "$SRC_ROOT/autoscaling.tf" "$WORK/green/"
cp -R "$SRC_EXAMPLE" "$WORK/green/examples/basic"
rm -rf "$WORK/green/examples/basic/.terraform" "$WORK/green/examples/basic/.terraform.lock.hcl"
cp -R "$WORK/green/." "$WORK/green-oracle/"
GF_GREEN="$WORK/green/examples/basic"
GF_ORACLE="$WORK/green-oracle/examples/basic"

for d in "$GF_GREEN" "$GF_ORACLE"; do
  perl -0pi -e 's/(provider "aws" \{\n  region = "eu-west-1"\n)\}/$1\n  access_key                   = "test"\n  secret_key                   = "test"\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  s3_use_path_style            = true\n}/' "$d/main.tf"
  grep -q 's3_use_path_style' "$d/main.tf" || fail "the emulator delta did not match main.tf in $d - the corpus pin has moved"
  perl -pi -e 's/version = ">= 6\.28"/version = "= 6.59.0"/' "$d/versions.tf"
  grep -q 'version = "= 6.59.0"' "$d/versions.tf" || fail "the provider version pin delta did not match versions.tf in $d - the corpus pin has moved"
done
perl -0777pi -e 's/\}\n\z/\n  live {\n    estate = "'"$GREEN_ESTATE"'"\n    record_store "local" {\n      path = ".tofu-records"\n    }\n  }\n}\n/' "$GF_GREEN/versions.tf"
grep -q "estate = \"$GREEN_ESTATE\"" "$GF_GREEN/versions.tf" || fail "the greenfield live-block delta did not match versions.tf - the corpus pin has moved"
log "  DELTA  emulator flags + provider pin on both namespaces; live block added on the greenfield side only"

log "=== PART GREENFIELD: 1. choudoufu apply from nothing, no migration, no state file ever existing ==="
( cd "$GF_GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$GF_GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the greenfield init failed"; }
GREEN_APPLY_OUT="$(cd "$GF_GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"
if [ $? -ne 0 ]; then
  printf '%s\n' "$GREEN_APPLY_OUT" | tail -40
  if grep -qF "Not an identity attribute" <<< "$GREEN_APPLY_OUT" && grep -qF "aws_dynamodb_table.this[0].arn" <<< "$GREEN_APPLY_OUT"; then
    # A REAL, reproducible wall, not a surprise: on a genuine from-nothing
    # create, module.dynamodb_table.aws_dynamodb_table.this[0]'s own
    # identity (its `name`) is itself a formula waiting on random_pet.this
    # (a record-backed sibling, resolved fine - see this part's header).
    # That formula-derived table therefore never becomes ClassConcrete /
    # ClassNeedsDiscovery / ClassRecordBacked ahead of apply, and
    # internal/live/identity/resolve.go's deferrable check
    # (~line 2995) only lets a CHILD read a NON-identity attribute (here,
    # aws_dynamodb_resource_policy.this[0] reading the table's `arn`) of a
    # parent in one of those three classes. A table whose own identity is
    # itself still a pending formula is not covered, so the read is refused
    # outright instead of deferred to apply time the way it would be for a
    # plain, statically-named table. Confirmed by reading resolve.go
    # directly, not inferred from the message alone. choudoufu REFUSES
    # where stock proceeds (row 1 of HANDOFF's five-row table) - a real
    # engine gap, not this stage's own check; not fixed in this
    # script-only pass (it sits with #388's plan-node seam, HANDOFF's "The
    # order" item 3).
    gauntlet_stage greenfield fail "the greenfield apply refuses module.dynamodb_table.aws_dynamodb_resource_policy.this[0]'s resource_arn = aws_dynamodb_table.this[0].arn with \"Not an identity attribute\": the table's OWN identity (name) is itself a formula still waiting on random_pet.this (a record-backed sibling), so it is not yet ClassConcrete/ClassNeedsDiscovery/ClassRecordBacked when the resource policy tries to read its non-identity arn attribute, and internal/live/identity/resolve.go's deferrable check does not cover a parent whose own identity is still a pending formula. Stock proceeds fine (its dependency graph creates the table, then the policy, using the table's real post-apply arn) - choudoufu refuses where stock proceeds (row 1), a real engine gap tracked for #388's plan-node seam, not fixed in this script-only pass. cold_deploy/migrate/test_plan/test_apply/drift_reconverge/day2_rename/day2_remove for this estate are unaffected (checked in the same run, see the earlier GAUNTLET stage= lines)"
    gauntlet_end_stage
    docker rm -f "$FLOCI_GREEN_NAME" "$FLOCI_GREEN_ORACLE_NAME" >/dev/null 2>&1 || true
    SKIP_GREENFIELD_REST=1
  else
    fail "the greenfield apply failed"
  fi
fi
if [ -z "${SKIP_GREENFIELD_REST:-}" ]; then
grep -qE 'Apply complete! Resources: 3 added' <<< "$GREEN_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT"; fail "the greenfield apply did not create exactly 3 resources"; }
log "  $(grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT")"

awsg() { aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" "$@"; }

log "=== PART GREENFIELD: 2. markers, read through the AWS CLI directly ==="
GREEN_TABLE_ARN="$(awsg dynamodb list-tables --query 'TableNames[0]' --output text)"
[ -n "$GREEN_TABLE_ARN" ] && [ "$GREEN_TABLE_ARN" != "None" ] || fail "no table found in the greenfield namespace"
GREEN_TABLE_NAME="$GREEN_TABLE_ARN"
GREEN_TABLE_ARN="$(awsg dynamodb describe-table --table-name "$GREEN_TABLE_NAME" --query 'Table.TableArn' --output text)"
GREEN_TABLE_ADDR="$(awsg dynamodb list-tags-of-resource --resource-arn "$GREEN_TABLE_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GREEN_TABLE_ADDR" = "module.dynamodb_table.aws_dynamodb_table.this:0" ] \
  || fail "the greenfield table carries tofu-address=$GREEN_TABLE_ADDR, not module.dynamodb_table.aws_dynamodb_table.this:0"
GREEN_TABLE_EST="$(awsg dynamodb list-tags-of-resource --resource-arn "$GREEN_TABLE_ARN" --query "Tags[?Key=='tofu-estate'].Value | [0]" --output text)"
[ "$GREEN_TABLE_EST" = "$GREEN_ESTATE" ] || fail "the greenfield table carries tofu-estate=$GREEN_TABLE_EST, not $GREEN_ESTATE"
log "  table $GREEN_TABLE_ARN carries tofu-address=$GREEN_TABLE_ADDR tofu-estate=$GREEN_TABLE_EST - read via the AWS CLI, not choudoufu's own report"

log "=== PART GREENFIELD: 3. the record store holds every instance, including the two untaggable/record-backed types (#364 A2) ==="
GREEN_RECORD_FILES="$(find "$GF_GREEN/.tofu-records/tofu-records" -type f ! -name '*.lock' ! -name '*.tmp-*' 2>/dev/null | wc -l | tr -d ' ')"
[ "$GREEN_RECORD_FILES" = "3" ] || fail "expected 3 records under the local record store after the greenfield apply (random_pet, the table, the resource policy), found $GREEN_RECORD_FILES"
log "  3 records persisted, one per managed instance, read directly off the local record store"

log "=== PART GREENFIELD: 4. the next plan proposes nothing ==="
GREEN_PLAN_OUT="$(cd "$GF_GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; GREEN_PLAN_RC=$?
[ "$GREEN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$GREEN_PLAN_OUT" | tail -30; fail "the greenfield replan exited $GREEN_PLAN_RC"; }
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$GREEN_PLAN_OUT" \
  || { grep -E '^  #' <<< "$GREEN_PLAN_OUT"; fail "the greenfield replan is not empty"; }
log "  No changes."

log "=== PART GREENFIELD: 5. stock oracle - the identical (delta-1'd) config applied fresh in its own namespace ==="
( cd "$GF_ORACLE" && AWS_ENDPOINT_URL="$GREEN_ORACLE_ENDPOINT" AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$GF_ORACLE" && AWS_ENDPOINT_URL="$GREEN_ORACLE_ENDPOINT" terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the greenfield oracle's init failed"; }
ORACLE_APPLY_OUT="$(cd "$GF_ORACLE" && AWS_ENDPOINT_URL="$GREEN_ORACLE_ENDPOINT" AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$ORACLE_APPLY_OUT" | tail -40; fail "the greenfield oracle apply failed"; }
grep -qE 'Apply complete! Resources: 3 added' <<< "$ORACLE_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$ORACLE_APPLY_OUT"; fail "the greenfield oracle apply did not create exactly 3 resources"; }
log "  $(grep -E 'Apply complete' <<< "$ORACLE_APPLY_OUT")"

table_shape() { # $1=endpoint $2=table-name - normalised structural facts,
                 # read via the AWS CLI, never through tofu state.
  aws --endpoint-url "$1" --region "$REGION" dynamodb describe-table --table-name "$2" --output json 2>/dev/null \
  | jq -S '.Table as $t | {
      KeySchema: ($t.KeySchema | sort_by(.AttributeName)),
      Attributes: ($t.AttributeDefinitions | sort_by(.AttributeName)),
      TableClass: ($t.TableClassSummary.TableClass // "STANDARD"),
      DeletionProtection: $t.DeletionProtectionEnabled,
      OnDemand: ($t | has("OnDemandThroughput")),
      GSIs: ([$t.GlobalSecondaryIndexes[]? | {
        Name: .IndexName,
        Keys: (.KeySchema | sort_by(.AttributeName)),
        Projection: .Projection
      }] | sort_by(.Name))
    }'
}

log "=== PART GREENFIELD: 6. structural comparison, via the AWS CLI on both endpoints, tags/name normalised out ==="
ORACLE_TABLE_NAME="$(aws --endpoint-url "$GREEN_ORACLE_ENDPOINT" --region "$REGION" dynamodb list-tables --query 'TableNames[0]' --output text)"
[ -n "$ORACLE_TABLE_NAME" ] && [ "$ORACLE_TABLE_NAME" != "None" ] || fail "no table found in the greenfield oracle namespace"
G_SHAPE="$(table_shape "$GREEN_ENDPOINT" "$GREEN_TABLE_NAME")"
O_SHAPE="$(table_shape "$GREEN_ORACLE_ENDPOINT" "$ORACLE_TABLE_NAME")"
if [ "${BREAK_GREEN:-}" = "1" ]; then
  G_SHAPE="$(table_shape "$GREEN_ENDPOINT" "$GREEN_TABLE_NAME" | jq 'del(.GSIs)')"
  log "  BREAK_GREEN=1: dropped the GSI from the greenfield table's expected shape - the comparison below must fail"
fi
[ "$G_SHAPE" = "$O_SHAPE" ] || { diff <(printf '%s\n' "$G_SHAPE") <(printf '%s\n' "$O_SHAPE") || true; \
  if [ "${BREAK_GREEN:-}" = "1" ]; then log "  BREAK_GREEN=1: correctly mismatched with the GSI dropped from the expected shape"; else fail "the greenfield table differs structurally from the stock oracle's table"; fi; }
if [ "${BREAK_GREEN:-}" = "1" ]; then
  [ "$G_SHAPE" != "$O_SHAPE" ] || fail "BREAK_GREEN=1: dropping the GSI from the expected shape should have made the comparison fail, but it still matched - this stage's check is not load-bearing"
else
  log "  key schema, attributes, table class, deletion protection, on-demand billing and GSI (name/keys/projection) all match between the greenfield table and the stock oracle's table"
  GREEN_POLICY="$(awsg dynamodb get-resource-policy --resource-arn "$GREEN_TABLE_ARN" --query Policy --output text 2>/dev/null | jq -S 'del(.Statement[].Resource)')"
  ORACLE_TABLE_ARN="$(aws --endpoint-url "$GREEN_ORACLE_ENDPOINT" --region "$REGION" dynamodb describe-table --table-name "$ORACLE_TABLE_NAME" --query 'Table.TableArn' --output text)"
  ORACLE_POLICY="$(aws --endpoint-url "$GREEN_ORACLE_ENDPOINT" --region "$REGION" dynamodb get-resource-policy --resource-arn "$ORACLE_TABLE_ARN" --query Policy --output text 2>/dev/null | jq -S 'del(.Statement[].Resource)')"
  [ "$GREEN_POLICY" = "$ORACLE_POLICY" ] || { diff <(printf '%s\n' "$GREEN_POLICY") <(printf '%s\n' "$ORACLE_POLICY") || true; fail "the greenfield table's resource policy differs from the stock oracle's, with the table-ARN-specific Resource field normalised out"; }
  log "  resource policy matches too (Sid/Effect/Principal/Action), the templated Resource field normalised out on both sides"
  gauntlet_stage greenfield pass "3 resources from nothing (random_pet + table + resource policy), the table's markers verified via the AWS CLI, 3 records in the local record store (#364 A2), replan empty, stock oracle in its own namespace matches structurally on key schema/attributes/table class/deletion protection/on-demand billing/GSI/resource policy"
fi
gauntlet_end_stage

docker rm -f "$FLOCI_GREEN_NAME" "$FLOCI_GREEN_ORACLE_NAME" >/dev/null 2>&1 || true
fi


  gauntlet_end_stage
  gauntlet_end

  log "=== PASS ==="
  log ""
  log "A terraform-aws-modules EXAMPLE (terraform-aws-dynamodb-table, 35.6M"
  log "registry downloads, never crossed before) went through all five"
  log "stages: cold deploy with plain terraform, choudoufu live-import"
  log "adoption plus the tofu-slot convergence apply, an empty replan with"
  log "the state file deleted and the rendered identity checked against"
  log "DynamoDB's own answer, a genuine no-op apply, and drift on the table"
  log "reconverging."
fi
