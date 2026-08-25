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
#                matches.
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

ESTATE="dynamodb-table-basic-crossing"
REGION="eu-west-1"
ACCOUNT="000000000000"

cleanup() {
  docker rm -f "$FLOCI_NAME" "$FLOCI_GREEN_NAME" "$FLOCI_GREEN_ORACLE_NAME" >/dev/null 2>&1 || true
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
CURRENT_STAGE=cold_deploy
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
CURRENT_STAGE=greenfield
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
mkdir -p "$WORK/green" "$WORK/green-oracle"
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
GREEN_APPLY_OUT="$(cd "$GF_GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$GREEN_APPLY_OUT" | tail -40; fail "the greenfield apply failed"; }
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
CURRENT_STAGE=""

docker rm -f "$FLOCI_GREEN_NAME" "$FLOCI_GREEN_ORACLE_NAME" >/dev/null 2>&1 || true

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
CURRENT_STAGE=day2_rename
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
CURRENT_STAGE=day2_remove
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
CURRENT_STAGE=migrate

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE - choudoufu live-import against the cold state, then one
# ordinary apply to converge tofu-slot
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=migrate
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
CURRENT_STAGE=test_plan
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
CURRENT_STAGE=test_apply
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
CURRENT_STAGE=drift_reconverge
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
  CURRENT_STAGE=day2_rename
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
    grep -qE '^  # .+ will be (destroyed|created)' <<< "$MOVED_PLAN_OUT" \
      && { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block rename proposes a destroy or a create - not zero churn"; }
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
    CURRENT_STAGE=day2_remove
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
      grep -qE '^  # module\.dynamodb_table_final\.aws_dynamodb_resource_policy\.this\[0\] will be destroyed' <<< "$REMOVE_PLAN_OUT" \
        || { printf '%s\n' "$REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu does not propose destroying module.dynamodb_table_final's resource policy when its block is deleted"; }
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
    fi
    CURRENT_STAGE=""
  fi
  CURRENT_STAGE=""

  CURRENT_STAGE=""
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
