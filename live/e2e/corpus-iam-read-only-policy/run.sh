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
#                     -approve`, then ONE ordinary `choudoufu apply` with no
#                     state file present, to converge tofu-slot (see
#                     corpus-iam-policy/run.sh's header - the module's
#                     aws_iam_policy declares `count = var.create &&
#                     var.create_policy ? 1 : 0`, the same shape that needs
#                     it).
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
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }

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
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE - choudoufu live-import against the cold state, then one
# ordinary apply to converge tofu-slot
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 2: migrate (choudoufu live-import -approve, then converge) ==="
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
grep -qF "1 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 already recorded, 0 failed, 0 skipped" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not stamp the 1 resource cleanly"; }
log "  1 stamped"

WANT_ADDR="module.read_only_iam_policy.aws_iam_policy.policy:0"
GOT_ADDR="$(awsl iam list-policy-tags --policy-arn "$POLICY_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_ADDR" = "$WANT_ADDR" ] || fail "$POLICY_ARN carries tofu-address=$GOT_ADDR, not $WANT_ADDR"
log "  marker verified directly against IAM, not through choudoufu's own report: $POLICY_ARN -> tofu-address=$GOT_ADDR"

# The tofu-slot convergence apply - see corpus-iam-policy/run.sh's header.
# The module's aws_iam_policy declares count = var.create && var.create_policy
# ? 1 : 0, the same shape that needs it.
CONVERGE_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; CONVERGE_RC=$?
[ "$CONVERGE_RC" -eq 0 ] || { printf '%s\n' "$CONVERGE_OUT" | tail -40; fail "the tofu-slot convergence apply failed"; }
grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$CONVERGE_OUT" \
  || { grep -E 'Apply complete' <<< "$CONVERGE_OUT"; fail "the convergence apply did not change exactly 1 resource (expected: the policy gaining tofu-slot)"; }
log "  $(grep -E 'Apply complete' <<< "$CONVERGE_OUT") (tofu-slot written)"
[ ! -f "$EST/terraform.tfstate" ] || fail "the convergence apply wrote a state file"

log ""
log "STAGE 2 (migrate): PASS"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - state deleted (already true), live-plan empty,
# identity re-asserted
# ══════════════════════════════════════════════════════════════════════════
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
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 4: TEST APPLY - apply the empty plan, assert a genuine no-op
# ══════════════════════════════════════════════════════════════════════════
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
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 5: DRIFT AND RECONVERGE - mutate the one object, replan, assert the
# fix is proposed against the right address (see the header comment on why
# this differs from corpus-iam-policy's two-object BREAK control)
# ══════════════════════════════════════════════════════════════════════════
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
log ""

log "=== PASS ==="
log ""
log "A terraform-aws-modules EXAMPLE using a different iam module than"
log "corpus-iam-policy - one that builds its policy from a generated"
log "allowed_services matrix, instantiated three times with only one call"
log "contributing a resource - crossed through all five stages: cold deploy"
log "with plain terraform, choudoufu live-import adoption plus the"
log "tofu-slot convergence apply it requires, an empty replan with the"
log "state file deleted and the rendered identity checked against IAM's own"
log "answer, a genuine no-op apply, and drift on the one policy reconverging"
log "and proposing a fix against the right address."
