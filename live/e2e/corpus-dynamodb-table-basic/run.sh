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
# https://github.com/lex00/floci/issues/91 with both reproductions. Stage 2
# (migrate) therefore still does not complete, for a different, now-real
# reason than before - test plan/test apply/drift-reconverge remain
# unreached, and this script does not claim otherwise.
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

ESTATE="dynamodb-table-basic-crossing"
REGION="eu-west-1"
ACCOUNT="000000000000"

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
# 2026-08-21 fix: the header documents DEBUG_KEEP but the trap never
# actually checked it - it always ran. Matched to every other corpus-*
# script's guard.
[ -n "${DEBUG_KEEP:-}" ] || trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }

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
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE - choudoufu live-import against the cold state, then one
# ordinary apply to converge tofu-slot
# ══════════════════════════════════════════════════════════════════════════
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
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - state deleted (already true), live-plan empty,
# identity re-asserted
# ══════════════════════════════════════════════════════════════════════════
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
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 4: TEST APPLY - apply the empty plan, assert a genuine no-op
# ══════════════════════════════════════════════════════════════════════════
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
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 5: DRIFT AND RECONVERGE - mutate one object, replan, assert one fix
# ══════════════════════════════════════════════════════════════════════════
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
  log ""

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
