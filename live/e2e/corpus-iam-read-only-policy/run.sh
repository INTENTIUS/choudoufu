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
#   BREAK        set to 1 to corrupt the address stage 3/5's identity and
#                drift assertions expect (see above), proving they are
#                load-bearing; set to 2 to exercise day2_rename's own break
#                control (rename module.read_only_iam_policy WITHOUT a moved
#                block); set to 3 to exercise the greenfield stage's own
#                break control (tamper the expected greenfield policy path
#                before the structural comparison against the stock oracle).
#   BREAK_REMOVE set to 1 to exercise day2_remove's own break control: keep
#                module.read_only_iam_policy_final's block and assert no
#                destroy is proposed. Only reachable when BREAK is not 2,
#                because Part E starts from Part D's real, completed rename.
#   BREAK_COUNT  set to 1 to exercise day2_count's own break control (PART
#                G, far below): after the real scale-down plan, assert the
#                WRONG instance (count_test[0] rather than count_test[1])
#                was destroyed - the assertion must fail. Only reachable
#                when BREAK is not 2 and BREAK_REMOVE is not 1, because
#                PART G starts from PART E's real, completed removal.
#
# Exit codes: 0 on a real pass of all stages, non-zero on a real failure.
# Every assertion reads command output, an exit code, or the emulator's own
# answer through the AWS CLI, never choudoufu's own report of itself.

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

# Two more, fresh containers for the greenfield stage (live/GAUNTLET.md #13):
# one namespace choudoufu applies into directly with no migration, and a
# SEPARATE namespace stock applies the identical config into as that stage's
# own oracle. Neither reuses the crossing container above.
FLOCI_GREEN_PORT=$((FLOCI_PORT + 1))
FLOCI_GREEN_NAME="choudoufu-corpus-roiam-green-$$"
FLOCI_ORACLE_PORT=$((FLOCI_PORT + 2))
FLOCI_ORACLE_NAME="choudoufu-corpus-roiam-green-oracle-$$"
GREEN_ENDPOINT="http://127.0.0.1:${FLOCI_GREEN_PORT}"
ORACLE_ENDPOINT="http://127.0.0.1:${FLOCI_ORACLE_PORT}"

ESTATE="iam-read-only-policy-crossing"
GREEN_ESTATE="iam-read-only-policy-greenfield"
REGION="eu-west-1"

cleanup() {
  docker rm -f "$FLOCI_NAME" "$FLOCI_GREEN_NAME" "$FLOCI_ORACLE_NAME" >/dev/null 2>&1 || true
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
gauntlet_begin_stage cold_deploy
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
# PART GREENFIELD (greenfield, active - live/GAUNTLET.md #13)
# ══════════════════════════════════════════════════════════════════════════
#
# A SEPARATE fresh namespace from everything above: greenfield means from
# nothing, so this never touches the objects STAGE 1's plain terraform apply
# created (those get migrated in STAGE 2, below). choudoufu applies the
# identical example (module.read_only_iam_policy is this estate's only
# module call that contributes a real resource - see this script's header)
# directly, with a live block from the start, no migration, no state file
# ever existing; the record store must hold one record; and the estate's own
# oracle is stock applying the SAME config fresh in a THIRD, independent
# namespace, compared structurally via the AWS CLI on both endpoints, never
# through tofu state.
gauntlet_begin_stage greenfield
log "=== PART GREENFIELD: 0. two more floci containers, one per fresh namespace ==="
docker run -d --rm -p "${FLOCI_GREEN_PORT}:4566" --name "$FLOCI_GREEN_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_GREEN_NAME failed"
docker run -d --rm -p "${FLOCI_ORACLE_PORT}:4566" --name "$FLOCI_ORACLE_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_ORACLE_NAME failed"
for gep in "$GREEN_ENDPOINT" "$ORACLE_ENDPOINT"; do
  GH=""
  for _ in $(seq 1 45); do
    GH="$(curl -fs "${gep}/_localstack/health" 2>/dev/null)" || true
    grep -q '"iam"' <<< "${GH:-}" && break
    sleep 2
  done
  grep -q '"iam"' <<< "${GH:-}" || fail "floci did not come up healthy (iam) at $gep"
done
log "  healthy: greenfield=$GREEN_ENDPOINT oracle=$ORACLE_ENDPOINT"

mkdir -p "$WORK/iam-greenfield/examples" "$WORK/iam-greenfield/modules"
cp -R "$SRC_EXAMPLE" "$WORK/iam-greenfield/examples/iam-read-only-policy"
cp -R "$SRC_MODULE" "$WORK/iam-greenfield/modules/iam-read-only-policy"
GREEN_EST="$WORK/iam-greenfield/examples/iam-read-only-policy"
rm -rf "$GREEN_EST/.terraform" "$GREEN_EST/.terraform.lock.hcl"
perl -0pi -e 's/(provider "aws" \{\n  region = "eu-west-1"\n)\}/$1\n  access_key                   = "test"\n  secret_key                   = "test"\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  s3_use_path_style            = true\n}/' "$GREEN_EST/main.tf"
grep -q 's3_use_path_style' "$GREEN_EST/main.tf" || fail "the greenfield emulator delta did not match main.tf - the corpus pin has moved"
perl -0pi -e 's/(required_providers \{\n    aws = \{\n      source  = "hashicorp\/aws"\n      version = ">= 6\.28"\n    \}\n  \}\n)\}/$1\n\n  live {\n    estate = "'"$GREEN_ESTATE"'"\n    record_store "local" {\n      path = ".tofu-records"\n    }\n  }\n}/' "$GREEN_EST/versions.tf"
grep -q "estate = \"$GREEN_ESTATE\"" "$GREEN_EST/versions.tf" || fail "the greenfield live-block delta did not match versions.tf - the corpus pin has moved"

log "=== PART GREENFIELD: 1. choudoufu apply from nothing, no migration, no state file ever existing ==="
( cd "$GREEN_EST" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$GREEN_EST" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the greenfield init failed"; }
GREEN_APPLY_OUT="$(cd "$GREEN_EST" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$GREEN_APPLY_OUT" | tail -40; fail "the greenfield apply failed"; }
grep -qE 'Apply complete! Resources: 1 added' <<< "$GREEN_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT"; fail "the greenfield apply did not create exactly 1 resource"; }
log "  $(grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT")"

awsg() { aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" "$@"; }

log "=== PART GREENFIELD: 2. markers, read through the AWS CLI directly ==="
GREEN_POLICY_ARN="$(awsg iam list-policies --path-prefix /example/ \
  --query "Policies[?starts_with(PolicyName, '$NAME_PREFIX') == \`true\`].Arn | [0]" --output text)"
[ -n "$GREEN_POLICY_ARN" ] && [ "$GREEN_POLICY_ARN" != "None" ] \
  || fail "no live greenfield policy found by its name prefix through the AWS CLI"
GREEN_ADDR="$(awsg iam list-policy-tags --policy-arn "$GREEN_POLICY_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GREEN_ADDR" = "module.read_only_iam_policy.aws_iam_policy.policy:0" ] || fail "the greenfield policy carries tofu-address=$GREEN_ADDR, not module.read_only_iam_policy.aws_iam_policy.policy:0"
GREEN_EST_TAG="$(awsg iam list-policy-tags --policy-arn "$GREEN_POLICY_ARN" --query "Tags[?Key=='tofu-estate'].Value | [0]" --output text)"
[ "$GREEN_EST_TAG" = "$GREEN_ESTATE" ] || fail "the greenfield policy carries tofu-estate=$GREEN_EST_TAG, not $GREEN_ESTATE"
log "  $GREEN_POLICY_ARN carries tofu-address=$GREEN_ADDR tofu-estate=$GREEN_EST_TAG - read via the AWS CLI, not choudoufu's own report"

log "=== PART GREENFIELD: 3. the record store holds one record (#364 A2) ==="
GREEN_RECORD_FILES="$(find "$GREEN_EST/.tofu-records/tofu-records" -type f ! -name '*.lock' ! -name '*.tmp-*' 2>/dev/null | wc -l | tr -d ' ')"
[ "$GREEN_RECORD_FILES" = "1" ] || fail "expected 1 record under the local record store after the greenfield apply, found $GREEN_RECORD_FILES"
log "  1 record persisted, read directly off the local record store"

log "=== PART GREENFIELD: 4. the next plan proposes nothing ==="
GREEN_PLAN_OUT="$(cd "$GREEN_EST" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; GREEN_PLAN_RC=$?
[ "$GREEN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$GREEN_PLAN_OUT" | tail -30; fail "the greenfield replan exited $GREEN_PLAN_RC"; }
grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$GREEN_PLAN_OUT" \
  && { grep -E '^  # .+ will be' <<< "$GREEN_PLAN_OUT"; fail "the greenfield replan proposes a resource change"; }
log "  no resource change proposed (this estate's outputs quirk means a permanent Changes-to-Outputs section is expected - see the header - so the check is the absence of a resource-action header)"

log "=== PART GREENFIELD: 5. stock oracle - the identical config applied fresh in its own namespace ==="
mkdir -p "$WORK/iam-greenfield-oracle/examples" "$WORK/iam-greenfield-oracle/modules"
cp -R "$SRC_EXAMPLE" "$WORK/iam-greenfield-oracle/examples/iam-read-only-policy"
cp -R "$SRC_MODULE" "$WORK/iam-greenfield-oracle/modules/iam-read-only-policy"
ORACLE_EST="$WORK/iam-greenfield-oracle/examples/iam-read-only-policy"
rm -rf "$ORACLE_EST/.terraform"
perl -0pi -e 's/(provider "aws" \{\n  region = "eu-west-1"\n)\}/$1\n  access_key                   = "test"\n  secret_key                   = "test"\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  s3_use_path_style            = true\n}/' "$ORACLE_EST/main.tf"
grep -q 's3_use_path_style' "$ORACLE_EST/main.tf" || fail "the greenfield oracle's emulator delta did not match main.tf"
( cd "$ORACLE_EST" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ORACLE_EST" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the greenfield oracle's init failed"; }
ORACLE_APPLY_OUT="$(cd "$ORACLE_EST" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$ORACLE_APPLY_OUT" | tail -40; fail "the greenfield oracle apply failed"; }
grep -qE 'Apply complete! Resources: 1 added' <<< "$ORACLE_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$ORACLE_APPLY_OUT"; fail "the greenfield oracle apply did not create exactly 1 resource"; }
log "  $(grep -E 'Apply complete' <<< "$ORACLE_APPLY_OUT")"

policy_shape() { # $1=endpoint $2=arn - a normalised structural fact sheet,
                  # read via the AWS CLI, never through tofu state.
  local ep="$1" arn="$2" ver doc
  ver="$(aws --endpoint-url "$ep" --region "$REGION" iam get-policy --policy-arn "$arn" \
    --query 'Policy.DefaultVersionId' --output text 2>/dev/null)"
  doc="$(aws --endpoint-url "$ep" --region "$REGION" iam get-policy-version --policy-arn "$arn" --version-id "$ver" \
    --query 'PolicyVersion.Document' --output json 2>/dev/null)"
  aws --endpoint-url "$ep" --region "$REGION" iam get-policy --policy-arn "$arn" \
    --query 'Policy.[Path,Description]' --output json 2>/dev/null \
  | jq -S --argjson doc "${doc:-null}" '{path: .[0], description: .[1], document: $doc}'
}

log "=== PART GREENFIELD: 6. object-by-object comparison, via the AWS CLI on both endpoints, tags normalised out ==="
ORACLE_POLICY_ARN="$(aws --endpoint-url "$ORACLE_ENDPOINT" --region "$REGION" iam list-policies --path-prefix /example/ \
  --query "Policies[?starts_with(PolicyName, '$NAME_PREFIX') == \`true\`].Arn | [0]" --output text)"
[ -n "$ORACLE_POLICY_ARN" ] && [ "$ORACLE_POLICY_ARN" != "None" ] || fail "no oracle policy found by its name prefix through the AWS CLI"
GREEN_SHAPE="$(policy_shape "$GREEN_ENDPOINT" "$GREEN_POLICY_ARN")"
if [ "${BREAK:-}" = "3" ]; then
  GREEN_SHAPE="$(jq -S '.path = "/tampered-by-BREAK/"' <<< "$GREEN_SHAPE")"
  log "  BREAK=3: tampered the expected greenfield path - the comparison below must fail"
fi
ORACLE_SHAPE="$(policy_shape "$ORACLE_ENDPOINT" "$ORACLE_POLICY_ARN")"
[ "$GREEN_SHAPE" = "$ORACLE_SHAPE" ] || { printf 'greenfield: %s\noracle:     %s\n' "$GREEN_SHAPE" "$ORACLE_SHAPE"; fail "the greenfield policy differs structurally from the stock oracle"; }
log "  path, description and policy document match structurally between choudoufu's greenfield apply and stock's cold deploy in its own namespace"
gauntlet_stage greenfield pass "1 resource from nothing, marker verified via the AWS CLI, 1 record in the local record store (#364 A2), replan empty, stock oracle in its own namespace matches structurally (path, description, policy document)"
gauntlet_end_stage

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
# a moved block. This estate's live-plan is genuinely stateless throughout
# (no local state file, ever), so - like corpus-sqs-basic's own module
# rename, not corpus-eks-basic's - the old, no-longer-declared address is
# never visited and never proposed for destroying; only a create for the
# renamed address is proposed. See D1 below for the verified detail.
gauntlet_begin_stage day2_rename
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
# Not "No changes." literally: the moved block moves the resource but not
# the data source beside it (data.aws_iam_policy_document.this[0], which
# `moved` blocks do not apply to), so it is re-read under the new module
# path on this one plan and terraform reports that as a data read rather
# than a silent match - real churn either way is what the Plan: line and
# the destroy/create grep above already rule out.
grep -qF 'Plan: 0 to add, 0 to change, 0 to destroy.' <<< "$ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -20; fail "stock's rename plan is not a true no-op"; }
log "  stock: zero churn on cold_deploy's own state - the move reports only its move, no attribute diff at all, outputs unchanged in value"

# ══════════════════════════════════════════════════════════════════════════
# PART F-ORACLE: REPLACE, stock (day2_replace, active - live/GAUNTLET.md #9)
# ══════════════════════════════════════════════════════════════════════════
#
# "Stock's replace of the same resource leaves the same single object."
# aws_iam_policy.policy[0] under module.read_only_iam_policy (this estate's
# only live object) is forced to replace by changing its `description`
# argument: IAM's UpdatePolicy has no field for description at all - the
# only mutable thing about a managed policy is its document, versioned
# separately - so description is ForceNew in the provider's own schema,
# confirmed empirically below by the plan's own "forces replacement"
# annotation, not assumed. A FRESH copy of cold_deploy's own state, same as
# D-ORACLE above, so this oracle runs on the ORIGINAL module name before the
# real script's own rename ever touches $EST.
gauntlet_begin_stage day2_replace
log "=== F-ORACLE. stock: force-replace module.read_only_iam_policy's policy via its ForceNew description argument, on cold_deploy's own state ==="
REPLACE_ORACLE_ROOT="$WORK/replace-oracle"
mkdir -p "$REPLACE_ORACLE_ROOT/iam/examples" "$REPLACE_ORACLE_ROOT/iam/modules"
cp -R "$SRC_EXAMPLE" "$REPLACE_ORACLE_ROOT/iam/examples/iam-read-only-policy"
cp -R "$SRC_MODULE" "$REPLACE_ORACLE_ROOT/iam/modules/iam-read-only-policy"
REPLACE_ORACLE="$REPLACE_ORACLE_ROOT/iam/examples/iam-read-only-policy"
rm -rf "$REPLACE_ORACLE/.terraform" "$REPLACE_ORACLE/.terraform.lock.hcl"
perl -0pi -e 's/(provider "aws" \{\n  region = "eu-west-1"\n)\}/$1\n  access_key                   = "test"\n  secret_key                   = "test"\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  s3_use_path_style            = true\n}/' "$REPLACE_ORACLE/main.tf"
grep -q 's3_use_path_style' "$REPLACE_ORACLE/main.tf" || fail "the day2_replace oracle's emulator delta did not match main.tf"
cp "$WORK/cold.tfstate" "$REPLACE_ORACLE/terraform.tfstate"
sed -i.bak 's/description = "My read only example policy"/description = "My read only example policy v2"/' "$REPLACE_ORACLE/main.tf"
rm -f "$REPLACE_ORACLE/main.tf.bak"
grep -q 'example policy v2' "$REPLACE_ORACLE/main.tf" \
  || fail "changing module.read_only_iam_policy's description argument in the replace-oracle copy did not match - the corpus pin has moved"
( cd "$REPLACE_ORACLE" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$REPLACE_ORACLE" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_replace stock oracle's init failed"; }
REPLACE_ORACLE_PLAN_OUT="$(cd "$REPLACE_ORACLE" && terraform plan -input=false -no-color 2>&1)"; REPLACE_ORACLE_PLAN_RC=$?
[ "$REPLACE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_replace stock oracle plan exited $REPLACE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.read_only_iam_policy\.aws_iam_policy\.policy\[0\] must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "stock does not propose replacing module.read_only_iam_policy's policy when its description argument changes"; }
grep -qE '~ +description +=.+forces replacement' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "stock's plan does not mark description as forcing replacement - it may not be ForceNew after all"; }
grep -qF 'Plan: 1 to add, 0 to change, 1 to destroy.' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -10; fail "stock's replace plan proposes something other than exactly one add and one destroy at the same address"; }
# PLAN ONLY, never applied - same convention as D-ORACLE above and every
# other gauntlet ORACLE section: this oracle's copy shares floci's account
# with $EST, so actually applying here would destroy the real policy the
# rest of this script still depends on.
log "  stock: exactly one replace at the same declared address (module.read_only_iam_policy.aws_iam_policy.policy[0]), plan only, never applied"

# ══════════════════════════════════════════════════════════════════════════
# PART G-ORACLE: CHANGE COUNT, stock (day2_count, active - live/GAUNTLET.md
# #8, issue #359/#488)
# ══════════════════════════════════════════════════════════════════════════
#
# This estate's only real object (module.read_only_iam_policy_final's
# policy, destroyed for good by PART E, far below) is instantiated through
# the module's own create/create_policy BOOLEANS - count = var.create &&
# var.create_policy ? 1 : 0 - never a numeric count a caller can vary, so
# there is no honest countable knob of this estate's own (issue #488's
# fallback clause). This follows live/e2e/reference-ec2-vpc/run.sh's own
# Part F/B1.7 convention rather than corpus-xancloud-iac's real for_each
# shape: a synthetic aws_iam_policy resource, count_test_block() below,
# added and removed entirely within this oracle and PART G (its real-leg
# counterpart, far below) - nothing else in this estate ever names it.
# Applied for real, twice (2 -> 1 -> 2), in the SAME otherwise-idle account
# PART GREENFIELD's own oracle ($ORACLE_ENDPOINT) already finished with
# above and never touches again - "iam-ro-count-test-*" collides with
# nothing that account already holds (its one policy is named with the
# "ex-$(basename $EST)-" prefix, a completely different name) - the same
# reasoning reference-ec2-vpc's B1.7 gives for reusing its own idle
# greenfield account rather than spinning up a fourth container.
gauntlet_begin_stage day2_count
count_test_block() { # $1 = count
  local n="$1"
  cat <<COUNTEOF
resource "aws_iam_policy" "count_test" {
  count       = $n
  name        = "iam-ro-count-test-\${count.index}"
  path        = "/example/"
  description = "day2_count evidence (issue #359)"
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = "s3:GetObject"
      Resource = "*"
    }]
  })
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

log "=== G-ORACLE: stock, create a 2-instance count block, scale it to 1 and back, in the (idle) greenfield-oracle account ==="
PLAIN_ORACLE_COUNT="$WORK/plain-oracle-count"
mkdir -p "$PLAIN_ORACLE_COUNT"
{ oracle_count_provider; count_test_block 2; } > "$PLAIN_ORACLE_COUNT/main.tf"
( cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_count stock oracle's init failed"; }
ORACLE_COUNT_APPLY_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$ORACLE_COUNT_APPLY_OUT" | tail -30; fail "the day2_count stock oracle's baseline apply failed"; }
grep -qE 'Apply complete! Resources: 2 added' <<< "$ORACLE_COUNT_APPLY_OUT" \
  || { printf '%s\n' "$ORACLE_COUNT_APPLY_OUT" | tail -30; fail "stock did not create exactly 2 count-test policies for the day2_count oracle"; }
ORACLE_CT0_ARN="$(aws --endpoint-url "$ORACLE_ENDPOINT" --region "$REGION" iam list-policies --path-prefix /example/ \
  --query "Policies[?PolicyName=='iam-ro-count-test-0'].Arn | [0]" --output text)"
ORACLE_CT1_ARN="$(aws --endpoint-url "$ORACLE_ENDPOINT" --region "$REGION" iam list-policies --path-prefix /example/ \
  --query "Policies[?PolicyName=='iam-ro-count-test-1'].Arn | [0]" --output text)"
[ -n "$ORACLE_CT0_ARN" ] && [ "$ORACLE_CT0_ARN" != "None" ] || fail "no oracle count_test[0] policy found by name"
[ -n "$ORACLE_CT1_ARN" ] && [ "$ORACLE_CT1_ARN" != "None" ] || fail "no oracle count_test[1] policy found by name"
# aws_iam_policy's ARN is arn:aws:iam::<account>:policy/<path><name> - fully
# determined by account, path and name, none of which this cycle changes -
# so an ARN alone cannot prove a destroy+recreate happened for real (verified
# directly against the emulator, no tofu in the loop, before writing this:
# deleting and recreating a same-named/same-path policy yields the identical
# ARN both times). What AWS DOES mint fresh on every CreatePolicy call is
# PolicyId (an AWS-assigned identifier, independent of name/path); that is
# the value this stage's "genuinely a new object" checks below compare.
ORACLE_CT0_ID="$(aws --endpoint-url "$ORACLE_ENDPOINT" --region "$REGION" iam get-policy --policy-arn "$ORACLE_CT0_ARN" --query 'Policy.PolicyId' --output text)"
ORACLE_CT1_ID="$(aws --endpoint-url "$ORACLE_ENDPOINT" --region "$REGION" iam get-policy --policy-arn "$ORACLE_CT1_ARN" --query 'Policy.PolicyId' --output text)"
[ -n "$ORACLE_CT0_ID" ] && [ "$ORACLE_CT0_ID" != "None" ] || fail "oracle count_test[0] has no PolicyId"
[ -n "$ORACLE_CT1_ID" ] && [ "$ORACLE_CT1_ID" != "None" ] || fail "oracle count_test[1] has no PolicyId"
log "  stock: 2 instances created, count_test[0]=$ORACLE_CT0_ARN (id=$ORACLE_CT0_ID) count_test[1]=$ORACLE_CT1_ARN (id=$ORACLE_CT1_ID)"

{ oracle_count_provider; count_test_block 1; } > "$PLAIN_ORACLE_COUNT/main.tf"
ORACLE_DOWN_PLAN_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform plan -input=false -no-color 2>&1)"; ORACLE_DOWN_PLAN_RC=$?
[ "$ORACLE_DOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | tail -30; fail "the day2_count stock oracle's scale-down plan exited $ORACLE_DOWN_PLAN_RC"; }
grep -qE '^  # aws_iam_policy\.count_test\[1\] will be destroyed' <<< "$ORACLE_DOWN_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-down plan does not destroy count_test[1]"; }
grep -qE '^  # aws_iam_policy\.count_test\[0\] will be' <<< "$ORACLE_DOWN_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-down plan touches count_test[0], which should be untouched"; }
grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$ORACLE_DOWN_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | tail -10; fail "stock's scale-down plan proposes something other than exactly one destroy"; }
ORACLE_DOWN_APPLY_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$ORACLE_DOWN_APPLY_OUT" | tail -30; fail "the day2_count stock oracle's scale-down apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$ORACLE_DOWN_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$ORACLE_DOWN_APPLY_OUT"; fail "the day2_count stock oracle's scale-down apply was not exactly one destroy"; }
ORACLE_CT0_ID_AFTER_DOWN="$(aws --endpoint-url "$ORACLE_ENDPOINT" --region "$REGION" iam get-policy --policy-arn "$ORACLE_CT0_ARN" --query 'Policy.PolicyId' --output text 2>/dev/null || true)"
[ "$ORACLE_CT0_ID_AFTER_DOWN" = "$ORACLE_CT0_ID" ] || fail "stock's surviving count_test[0] changed PolicyId across the scale-down ($ORACLE_CT0_ID -> $ORACLE_CT0_ID_AFTER_DOWN)"
if ORACLE_CT1_STILL="$(aws --endpoint-url "$ORACLE_ENDPOINT" --region "$REGION" iam get-policy --policy-arn "$ORACLE_CT1_ARN" 2>&1)"; then
  echo "$ORACLE_CT1_STILL"; fail "stock's count_test[1] ($ORACLE_CT1_ARN) still exists after the scale-down destroy"
fi
log "  stock: exactly one destroy (count_test[1]=$ORACLE_CT1_ARN), count_test[0]=$ORACLE_CT0_ARN (id=$ORACLE_CT0_ID) unchanged"

{ oracle_count_provider; count_test_block 2; } > "$PLAIN_ORACLE_COUNT/main.tf"
ORACLE_UP_PLAN_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform plan -input=false -no-color 2>&1)"; ORACLE_UP_PLAN_RC=$?
[ "$ORACLE_UP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | tail -30; fail "the day2_count stock oracle's scale-up plan exited $ORACLE_UP_PLAN_RC"; }
grep -qE '^  # aws_iam_policy\.count_test\[1\] will be created' <<< "$ORACLE_UP_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-up plan does not create count_test[1]"; }
grep -qE '^  # aws_iam_policy\.count_test\[0\] will be' <<< "$ORACLE_UP_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-up plan touches count_test[0], which should be untouched"; }
grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$ORACLE_UP_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | tail -10; fail "stock's scale-up plan proposes something other than exactly one create"; }
ORACLE_UP_APPLY_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$ORACLE_UP_APPLY_OUT" | tail -30; fail "the day2_count stock oracle's scale-up apply failed"; }
grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$ORACLE_UP_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$ORACLE_UP_APPLY_OUT"; fail "the day2_count stock oracle's scale-up apply was not exactly one create"; }
ORACLE_CT1_NEW_ARN="$(aws --endpoint-url "$ORACLE_ENDPOINT" --region "$REGION" iam list-policies --path-prefix /example/ \
  --query "Policies[?PolicyName=='iam-ro-count-test-1'].Arn | [0]" --output text)"
[ -n "$ORACLE_CT1_NEW_ARN" ] && [ "$ORACLE_CT1_NEW_ARN" != "None" ] || fail "no oracle count_test[1] policy found after the scale-up"
[ "$ORACLE_CT1_NEW_ARN" = "$ORACLE_CT1_ARN" ] || fail "the recreated count_test[1]'s ARN ($ORACLE_CT1_NEW_ARN) differs from its pre-destroy ARN ($ORACLE_CT1_ARN) - unexpected: aws_iam_policy's ARN is name/path-derived and should be identical both times"
ORACLE_CT1_NEW_ID="$(aws --endpoint-url "$ORACLE_ENDPOINT" --region "$REGION" iam get-policy --policy-arn "$ORACLE_CT1_NEW_ARN" --query 'Policy.PolicyId' --output text)"
[ "$ORACLE_CT1_NEW_ID" != "$ORACLE_CT1_ID" ] || fail "stock's recreated count_test[1] came back with the SAME PolicyId it had before being destroyed - the destroy was not real"
ORACLE_CT0_ID_AFTER_UP="$(aws --endpoint-url "$ORACLE_ENDPOINT" --region "$REGION" iam get-policy --policy-arn "$ORACLE_CT0_ARN" --query 'Policy.PolicyId' --output text 2>/dev/null || true)"
[ "$ORACLE_CT0_ID_AFTER_UP" = "$ORACLE_CT0_ID" ] || fail "stock's count_test[0] changed PolicyId across the scale-up"
log "  stock: exactly one create (count_test[1], same ARN $ORACLE_CT1_NEW_ARN - deterministic from name+path - but a NEW PolicyId $ORACLE_CT1_NEW_ID, was $ORACLE_CT1_ID), count_test[0]=$ORACLE_CT0_ARN (id=$ORACLE_CT0_ID) unchanged throughout"
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE - choudoufu live-import against the cold state, the slot
# it now writes read back by value, then one ordinary apply that must be a
# no-op (choudoufu #372)
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage migrate
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
gauntlet_begin_stage test_plan
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
gauntlet_begin_stage test_apply
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
gauntlet_begin_stage drift_reconverge
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
gauntlet_begin_stage day2_rename
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
  # Verified directly (measured, not guessed): this is a genuinely stateless
  # live-plan (no local state file, ever - every stage above asserts its
  # absence), so it walks only the addresses the CURRENT config declares.
  # The old, no-longer-declared module.read_only_iam_policy is never
  # visited at all - there is nothing to propose destroying, and the marker
  # it still carries is simply left behind, orphaned - while the new
  # address IS declared and gets a create proposed. This is
  # corpus-sqs-basic's exact stateless-replan shape (its own D1
  # BREAK=rename comment documents the same finding for its module
  # rename), not corpus-eks-basic's clean destroy+create.
  grep -qE '^  # module\.read_only_iam_policy\.aws_iam_policy\.policy\[0\] will be' <<< "$BREAK_PLAN_OUT" \
    && { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=2: the old, no-longer-declared address unexpectedly still appears in the plan - this stage's check is not load-bearing"; }
  grep -qE '^  # module\.read_only_iam_policy_final\.aws_iam_policy\.policy\[0\] will be created' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=2: renaming without a moved block did not propose creating module.read_only_iam_policy_final.aws_iam_policy.policy[0] - this stage's check is not load-bearing"; }
  log "  BREAK=2: correctly proposes ONLY a create for module.read_only_iam_policy_final.aws_iam_policy.policy[0], no destroy of the old (no-longer-declared, now-orphaned) module.read_only_iam_policy.aws_iam_policy.policy[0] - the real outcome for a stateless live-plan with no moved block, not a literal destroy-and-create; the moved-block and live-mv checks below are skipped"
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

  # ══════════════════════════════════════════════════════════════════════
  # PART F: REPLACE (day2_replace, active - live/GAUNTLET.md #9)
  # ══════════════════════════════════════════════════════════════════════
  #
  # Starts from Part D's real, completed rename: module.read_only_iam_policy_final
  # (this estate's only live object) is bound and converged. Its `description`
  # argument changes to a new literal - a real, upstream-immutable argument
  # on aws_iam_policy (IAM's UpdatePolicy has no field for description at
  # all; only the policy document is versioned) - forcing a replace at the
  # SAME declared address. F-ORACLE above already confirmed, empirically,
  # that stock marks description as forcing replacement on cold_deploy's
  # own state.
  #
  # THE create_before_destroy SCOPE NOTE (see corpus-sqs-basic's own PART F
  # header for the fuller discussion). OpenTofu core rejects a `lifecycle`
  # block written directly on a `module` call, and this corpus's established
  # convention only ever removes real upstream module content, never adds
  # library-internal lifecycle blocks to it - so this exercises OpenTofu's
  # DEFAULT replace ordering (destroy-then-create) rather than the
  # create_before_destroy variant the stage's Title names. The
  # marker-on-new-object and clean-old-object outcomes this stage's Proves
  # text cares about are identical either way; BREAK=replace below
  # manufactures the coexistence a skipped destroy half would leave, the
  # same way corpus-sqs-basic's own BREAK=replace does.
  gauntlet_begin_stage day2_replace
  record_key() { printf '%s' "$1" | base64 | tr '+/' '-_' | tr -d '=\n'; }
  record_import_id() { jq -r '.identity.import_id' "$1"; }
  F_ADDR="module.read_only_iam_policy_final.aws_iam_policy.policy[0]"
  F_RECORD="$EST/.tofu-records/tofu-records/$ESTATE/aws_iam_policy/$(record_key "$F_ADDR")"

  log "=== F0. capture the live policy and its record ahead of the forced replace ==="
  [ -f "$F_RECORD" ] || fail "no local record file found for $F_ADDR ahead of day2_replace"
  F_OLD_IMPORT_ID="$(record_import_id "$F_RECORD")"
  [ "$F_OLD_IMPORT_ID" = "$POLICY_ARN" ] || fail "the record for $F_ADDR names $F_OLD_IMPORT_ID ahead of day2_replace, not $POLICY_ARN"
  F_OLD_ADDR_TAG="$(awsl iam list-policy-tags --policy-arn "$POLICY_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$F_OLD_ADDR_TAG" = "module.read_only_iam_policy_final.aws_iam_policy.policy:0" ] \
    || fail "$POLICY_ARN does not carry tofu-address=module.read_only_iam_policy_final.aws_iam_policy.policy:0 ahead of day2_replace"
  log "  $POLICY_ARN, record import_id=$F_OLD_IMPORT_ID, tofu-address=$F_OLD_ADDR_TAG"

  if [ "${BREAK:-}" = "replace" ]; then
    log "=== F1 (BREAK=replace). manufacture the coexistence a skipped destroy half would leave behind ==="
    # A second, distinct live policy carrying the SAME tofu-address and
    # tofu-slot as the one a genuine replace would destroy - the state
    # "skip the destroy half" of a create-before-destroy replace would
    # leave, produced directly via the AWS CLI rather than by actually
    # interrupting an apply (day2_crash, stage 10, owns testing a real
    # interruption).
    BREAK_COLLISION_DOC='{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*"}]}'
    BREAK_COLLISION_ARN="$(awsl iam create-policy --policy-name "${NAME_PREFIX}collision" --path /example/ \
      --policy-document "$BREAK_COLLISION_DOC" \
      --tags "Key=tofu-estate,Value=$ESTATE" "Key=tofu-address,Value=module.read_only_iam_policy_final.aws_iam_policy.policy:0" "Key=tofu-slot,Value=0" \
      --query 'Policy.Arn' --output text)"
    [ -n "$BREAK_COLLISION_ARN" ] && [ "$BREAK_COLLISION_ARN" != "None" ] || fail "BREAK=replace: could not create the collision policy"
    BREAK_PLAN_OUT="$(plan_into 2>&1)"; BREAK_PLAN_RC=$?
    awsl iam delete-policy --policy-arn "$BREAK_COLLISION_ARN" >/dev/null 2>&1 || true
    [ "$BREAK_PLAN_RC" -ne 0 ] \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -20; fail "BREAK=replace: the plan succeeded with two live objects claiming the same tofu-address/tofu-slot - it must report the collision, not propose nothing"; }
    grep -qF 'Two live resources claiming one slot' <<< "$BREAK_PLAN_OUT" \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -20; fail "BREAK=replace: the plan failed for a reason other than the slot collision - this stage's check is not load-bearing"; }
    log "  BREAK=replace: choudoufu correctly refused with a named collision (two live resources claiming one slot) rather than silently proposing nothing - the Break text's own outcome"
  else
    log "=== F1. choudoufu: change the ForceNew description argument, forcing a replace at the same declared address ==="
    sed -i.bak 's/description = "My read only example policy"/description = "My read only example policy v2"/' "$EST/main.tf"
    rm -f "$EST/main.tf.bak"
    grep -q 'example policy v2' "$EST/main.tf" || fail "changing module.read_only_iam_policy_final's description argument did not match - the corpus pin has moved"

    F_PLAN_OUT="$(plan_into 2>&1)"; F_PLAN_RC=$?
    [ "$F_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_PLAN_OUT" | tail -40; fail "the day2_replace plan exited $F_PLAN_RC"; }
    grep -qE '^  # module\.read_only_iam_policy_final\.aws_iam_policy\.policy\[0\] must be replaced' <<< "$F_PLAN_OUT" \
      || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not propose replacing module.read_only_iam_policy_final's policy when its description argument changes"; }
    grep -qE '~ +description +=.+forces replacement' <<< "$F_PLAN_OUT" \
      || { printf '%s\n' "$F_PLAN_OUT"; fail "the plan does not mark description as forcing replacement"; }
    grep -qF 'Plan: 1 to add, 0 to change, 1 to destroy.' <<< "$F_PLAN_OUT" \
      || { printf '%s\n' "$F_PLAN_OUT" | tail -10; fail "the day2_replace plan is not exactly one add and one destroy at the same address"; }
    log "  choudoufu: exactly one forced replace at the same declared address (module.read_only_iam_policy_final.aws_iam_policy.policy[0]), description forces replacement"

    F_APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; F_APPLY_RC=$?
    [ "$F_APPLY_RC" -eq 0 ] || { printf '%s\n' "$F_APPLY_OUT" | tail -40; fail "the day2_replace apply exited $F_APPLY_RC"; }
    grep -qE 'Resources: 1 added, 0 changed, 1 destroyed' <<< "$F_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$F_APPLY_OUT"; fail "the day2_replace apply was not exactly one add and one destroy"; }

    if F_OLD_STILL="$(awsl iam get-policy --policy-arn "$POLICY_ARN" 2>&1)"; then
      echo "$F_OLD_STILL"; fail "$POLICY_ARN still exists after the replace - the old object was orphaned, not destroyed"
    fi
    log "  $POLICY_ARN no longer exists (NoSuchEntity) - confirmed via the AWS CLI, not through choudoufu's own report"

    F_NEW_ARN="$(awsl iam list-policies --path-prefix /example/ --query "Policies[?starts_with(PolicyName, '$NAME_PREFIX') == \`true\`].Arn | [0]" --output text)"
    [ -n "$F_NEW_ARN" ] && [ "$F_NEW_ARN" != "None" ] || fail "no policy found by its name prefix through the AWS CLI after the replace"
    [ "$F_NEW_ARN" != "$POLICY_ARN" ] || fail "sanity: the policy ARN did not change at all across the replace"
    F_NEW_ADDR_TAG="$(awsl iam list-policy-tags --policy-arn "$F_NEW_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
    [ "$F_NEW_ADDR_TAG" = "module.read_only_iam_policy_final.aws_iam_policy.policy:0" ] \
      || fail "$F_NEW_ARN carries tofu-address=$F_NEW_ADDR_TAG after the replace, not module.read_only_iam_policy_final.aws_iam_policy.policy:0 - the marker did not move onto the new object"
    log "  $F_NEW_ARN (the new object) carries tofu-address=$F_NEW_ADDR_TAG - the marker moved onto the new object, read via the AWS CLI"

    # THE RECORD STORE, asserted by value (HANDOFF's safety rule; the
    # #398-guard shape: a stale record still naming the destroyed object
    # would be exactly the wrong-marker failure that outranks a missing
    # one). The local record file at the SAME address must now hold the
    # NEW object's import_id, not the one captured in F0.
    F_NEW_IMPORT_ID="$(record_import_id "$F_RECORD")"
    [ "$F_NEW_IMPORT_ID" = "$F_NEW_ARN" ] \
      || fail "the record for $F_ADDR names $F_NEW_IMPORT_ID after the replace, not the new object $F_NEW_ARN - a stale record still claiming the destroyed object, the #398-guard shape"
    [ "$F_NEW_IMPORT_ID" != "$F_OLD_IMPORT_ID" ] \
      || fail "sanity: the record's import_id at $F_ADDR did not change at all across the replace"
    log "  record store: import_id $F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID at the same key ($F_ADDR) - read directly off the local record store file, not through choudoufu's own report"

    log "=== F2. one more plan: config and reality agree, no marker collision ==="
    F_FINAL_PLAN_OUT="$(plan_into 2>&1)"; F_FINAL_PLAN_RC=$?
    [ "$F_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_FINAL_PLAN_OUT" | tail -40; fail "the post-replace plan exited $F_FINAL_PLAN_RC"; }
    grep -qE '^  # .+ will be' <<< "$F_FINAL_PLAN_OUT" \
      && { printf '%s\n' "$F_FINAL_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the post-replace plan proposes a resource change"; }
    log "  no resource action proposed, no marker collision. The replace is complete and invisible to the next plan."

    # PART E below still reads $POLICY_ARN; the replace destroyed the old
    # object, so it must now name the new one.
    POLICY_ARN="$F_NEW_ARN"

    gauntlet_stage day2_replace pass "choudoufu: changing module.read_only_iam_policy_final's ForceNew description argument proposed exactly one replace at the same declared address (1 add, 0 change, 1 destroy; -/+ destroy and then create), applied cleanly; the old object ($F_OLD_IMPORT_ID) is confirmed gone and the new object ($F_NEW_ARN) carries the marker, both via the AWS CLI; the local record store's record at the same address now names the new object's import_id, not the destroyed one ($F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID); the next plan proposes no resource action; stock oracle on cold_deploy's own state (F-ORACLE) also proposes exactly one replace at the same address (plan only, not applied); BREAK=replace confirms a manufactured marker collision is reported loudly rather than silently proposed as nothing. Scope note: this exercises OpenTofu's default destroy-then-create ordering, not the create_before_destroy variant the stage's Title names - see this section's own header comment."
  fi
  gauntlet_end_stage

  # ══════════════════════════════════════════════════════════════════════
  # PART E: REMOVE A BLOCK (day2_remove, active - live/GAUNTLET.md #7)
  # ══════════════════════════════════════════════════════════════════════
  #
  # Starts from Part D's real, completed state: module.read_only_iam_policy_final
  # (originally module.read_only_iam_policy) is the ONLY module call in this
  # estate that ever contributed a real resource (module.read_only_iam_policy_doc
  # has create_policy=false, module.read_only_iam_policy_disabled has
  # create=false - see this script's header) - so removing its block leaves
  # this estate with zero live objects, and its declared aws_iam_policy.policy
  # block key has no surviving instance anywhere else in the config (the doc
  # and disabled modules each declare the same block key but with count=0, so
  # neither ever contributes an instance to classifyOrphans's "pending" set -
  # there is nothing for a genuine remove here to be mistaken for a rename).
  # outputs.tf's five root outputs all read module.read_only_iam_policy_final.* -
  # the only module that ever produced these values - so they are removed
  # along with the block, the same edit a person deleting this resource would
  # make.
  #
  # BREAK_REMOVE=1 exercises this stage's own Break control instead: keep
  # the block, and assert the plan proposes no destroy for it at all - the
  # Break text in tools/gauntlet/stages.go, verbatim.

  gauntlet_begin_stage day2_remove
  log "=== E0. capture the live ARN one more time ==="
  E_ADDR_BEFORE="$(awsl iam list-policy-tags --policy-arn "$POLICY_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text 2>/dev/null || true)"
  [ "$E_ADDR_BEFORE" = "module.read_only_iam_policy_final.aws_iam_policy.policy:0" ] \
    || fail "$POLICY_ARN does not carry tofu-address=module.read_only_iam_policy_final.aws_iam_policy.policy:0 before day2_remove even starts (got $E_ADDR_BEFORE)"

  if [ "${BREAK_REMOVE:-}" = "1" ]; then
    log "=== E1 (BREAK_REMOVE=1). keep module.read_only_iam_policy_final's block; no destroy may be proposed ==="
    BREAK_REMOVE_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; BREAK_REMOVE_PLAN_RC=$?
    [ "$BREAK_REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_REMOVE_PLAN_OUT" | tail -40; fail "the BREAK_REMOVE=1 kept-block plan exited $BREAK_REMOVE_PLAN_RC"; }
    grep -qE '^  # module\.read_only_iam_policy_final\.aws_iam_policy\.policy\[0\] will be destroyed' <<< "$BREAK_REMOVE_PLAN_OUT" \
      && { grep -E '^  # .+ will be' <<< "$BREAK_REMOVE_PLAN_OUT"; fail "BREAK_REMOVE=1: a destroy was proposed for module.read_only_iam_policy_final's policy even though its block is still in the config - this stage's check is not load-bearing"; }
    grep -qE '^  # .+ will be (created|destroyed)' <<< "$BREAK_REMOVE_PLAN_OUT" \
      && { grep -E '^  # .+ will be' <<< "$BREAK_REMOVE_PLAN_OUT"; fail "BREAK_REMOVE=1: some resource action was proposed with the block still in the config"; }
    log "  BREAK_REMOVE=1: correctly proposes no resource action - the block is still declared"
  else
    log "=== E1. choudoufu: delete module.read_only_iam_policy_final's block ==="
    perl -0pi -e 's/\nmodule "read_only_iam_policy_final" \{.*?\n\}\n//s' "$EST/main.tf"
    perl -0pi -e 's/\nmoved \{\n  from = module\.read_only_iam_policy\.aws_iam_policy\.policy\[0\]\n  to   = module\.read_only_iam_policy_moved\.aws_iam_policy\.policy\[0\]\n\}\n//s' "$EST/main.tf"
    grep -q 'module "read_only_iam_policy_final"' "$EST/main.tf" \
      && fail "removing module.read_only_iam_policy_final's block did not match - the config has moved"
    cat > "$EST/outputs.tf" <<'EOF'
################################################################################
# IAM Policy - outputs removed along with module.read_only_iam_policy_final's
# block (day2_remove)
################################################################################
EOF
    ( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the day2_remove reinit failed"; }
    REMOVE_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; REMOVE_PLAN_RC=$?
    [ "$REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -40; fail "the day2_remove plan exited $REMOVE_PLAN_RC"; }
    printf '%s\n' "$REMOVE_PLAN_OUT" > /tmp/roiam-debug-plan.txt
    if grep -q 'is unclaimed, so this may be the same resource under a new instance key' <<< "$REMOVE_PLAN_OUT"; then
      printf '%s\n' "$REMOVE_PLAN_OUT" | tail -40
      fail "choudoufu withheld the destroy of module.read_only_iam_policy_final's policy as a possible rename (discovery.go's classifyOrphans) even though no other aws_iam_policy.policy block anywhere in this config ever declares a real instance - this is the honest wall issue #358 names, not a pass"
    fi
    grep -qE '^  # module\.read_only_iam_policy_final\.aws_iam_policy\.policy\[0\] will be destroyed' <<< "$REMOVE_PLAN_OUT" \
      || { printf '%s\n' "$REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu does not propose destroying module.read_only_iam_policy_final's policy when its block is deleted"; }
    grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$REMOVE_PLAN_OUT" \
      || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -10; fail "choudoufu's remove plan proposes something other than exactly one destroy"; }
    log "  choudoufu: exactly one destroy (module.read_only_iam_policy_final's policy), nothing else"

    REMOVE_APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; REMOVE_APPLY_RC=$?
    [ "$REMOVE_APPLY_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_APPLY_OUT" | tail -40; fail "the day2_remove apply exited $REMOVE_APPLY_RC"; }
    grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$REMOVE_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$REMOVE_APPLY_OUT"; fail "the day2_remove apply was not exactly one destroy"; }

    if E_STILL="$(awsl iam get-policy --policy-arn "$POLICY_ARN" 2>&1)"; then
      echo "$E_STILL"; fail "$POLICY_ARN still exists in the live account after the destroy - it was orphaned, not destroyed"
    fi
    log "  $POLICY_ARN no longer exists (NoSuchEntity) - confirmed via the AWS CLI, not through choudoufu's own report"

    log "=== E2. one more plan: config and reality agree, nothing left to propose ==="
    E_FINAL_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; E_FINAL_PLAN_RC=$?
    [ "$E_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$E_FINAL_PLAN_OUT" | tail -40; fail "the post-remove plan exited $E_FINAL_PLAN_RC"; }
    grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$E_FINAL_PLAN_OUT" \
      && { grep -E '^  # .+ will be' <<< "$E_FINAL_PLAN_OUT"; fail "the post-remove plan proposes a resource change"; }
    log "  No resource change proposed. The removal is complete and invisible to the next plan."

    gauntlet_stage day2_remove pass "choudoufu: deleting module.read_only_iam_policy_final's block proposed exactly one destroy (0 add, 0 change, 1 destroy), applied cleanly (0 added, 0 changed, 1 destroyed), the object is genuinely gone from the live account (iam get-policy on the old ARN now returns NoSuchEntity, read via the AWS CLI, not choudoufu's own report), and the next plan proposes no resource action; classifyOrphans did not withhold the destroy because no other aws_iam_policy.policy block anywhere in this config ever declares a real instance (count=0 on both remaining module calls)"

    # ══════════════════════════════════════════════════════════════════════
    # PART G: CHANGE COUNT (day2_count, active - live/GAUNTLET.md #8, issue
    # #359/#488)
    # ══════════════════════════════════════════════════════════════════════
    #
    # Starts from Part E's real, completed state: the estate plans empty
    # with module.read_only_iam_policy_final's policy gone (Part E just
    # destroyed this estate's only real object). A NEW, entirely synthetic
    # resource (aws_iam_policy.count_test, count_test_block() defined above
    # PART G-ORACLE) is added here, in its own file, so day2_count's own
    # history is self-contained and never revisits an address any other
    # stage already used - the same discipline
    # live/e2e/reference-ec2-vpc/run.sh's own Part F uses for its
    # aws_security_group.count_test. G-ORACLE above is the stock oracle for
    # the identical shape, applied for real in a separate, otherwise-idle
    # account.
    #
    # BREAK_COUNT=1 exercises this stage's own Break control instead of the
    # real checks: after the real scale-down plan, assert the WRONG
    # instance (count_test[0] rather than count_test[1]) was the one
    # destroyed - the Break text in tools/gauntlet/stages.go for
    # day2_count, verbatim: "Expect a different instance to be destroyed;
    # the assertion must fail." Only reachable when BREAK is not 2 and
    # BREAK_REMOVE is not 1, because PART G starts from PART E's real,
    # completed removal.

    gauntlet_begin_stage day2_count
    log "=== G0. choudoufu: add aws_iam_policy.count_test, count = 2 ==="
    count_test_block 2 > "$EST/day2_count.tf"
    ( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the count-block-add reinit failed"; }
    COUNT_ADD_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; COUNT_ADD_PLAN_RC=$?
    [ "$COUNT_ADD_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_ADD_PLAN_OUT" | tail -30; fail "the count-block-add plan exited $COUNT_ADD_PLAN_RC"; }
    grep -qF 'Plan: 2 to add, 0 to change, 0 to destroy.' <<< "$COUNT_ADD_PLAN_OUT" \
      || { printf '%s\n' "$COUNT_ADD_PLAN_OUT" | tail -10; fail "adding the count block did not plan exactly 2 creates"; }
    COUNT_ADD_APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_ADD_APPLY_RC=$?
    [ "$COUNT_ADD_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_ADD_APPLY_OUT" | tail -30; fail "the count-block-add apply exited $COUNT_ADD_APPLY_RC"; }
    grep -qE 'Resources: 2 added, 0 changed, 0 destroyed' <<< "$COUNT_ADD_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$COUNT_ADD_APPLY_OUT"; fail "the count-block-add apply did not create exactly 2 resources"; }

    CT0_ARN="$(awsl iam list-policies --path-prefix /example/ --query "Policies[?PolicyName=='iam-ro-count-test-0'].Arn | [0]" --output text)"
    CT1_ARN="$(awsl iam list-policies --path-prefix /example/ --query "Policies[?PolicyName=='iam-ro-count-test-1'].Arn | [0]" --output text)"
    [ -n "$CT0_ARN" ] && [ "$CT0_ARN" != "None" ] || fail "no live count_test[0] policy found by name"
    [ -n "$CT1_ARN" ] && [ "$CT1_ARN" != "None" ] || fail "no live count_test[1] policy found by name"
    CT0_ADDR_TAG="$(awsl iam list-policy-tags --policy-arn "$CT0_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
    CT1_ADDR_TAG="$(awsl iam list-policy-tags --policy-arn "$CT1_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
    [ "$CT0_ADDR_TAG" = 'aws_iam_policy.count_test:0' ] || fail "count_test[0]'s live tofu-address tag is $CT0_ADDR_TAG, not aws_iam_policy.count_test:0 (live/MARKERS.md: a count instance's tag value is colon-escaped, e.g. aws_eip.this[2] -> aws_eip.this:2)"
    [ "$CT1_ADDR_TAG" = 'aws_iam_policy.count_test:1' ] || fail "count_test[1]'s live tofu-address tag is $CT1_ADDR_TAG, not aws_iam_policy.count_test:1"
    # aws_iam_policy's ARN is name/path-derived, not server-random (verified
    # directly against the emulator ahead of writing this stage, no tofu in
    # the loop - see G-ORACLE's own comment above for the same finding), so
    # a destroy+recreate under the same name yields the SAME ARN. PolicyId,
    # not ARN, is what the "genuinely a new object" checks below compare.
    CT0_ID="$(awsl iam get-policy --policy-arn "$CT0_ARN" --query 'Policy.PolicyId' --output text)"
    CT1_ID="$(awsl iam get-policy --policy-arn "$CT1_ARN" --query 'Policy.PolicyId' --output text)"
    [ -n "$CT0_ID" ] && [ "$CT0_ID" != "None" ] || fail "live count_test[0] has no PolicyId"
    [ -n "$CT1_ID" ] && [ "$CT1_ID" != "None" ] || fail "live count_test[1] has no PolicyId"
    log "  2 instances created: index 0 = $CT0_ARN (tofu-address=$CT0_ADDR_TAG, id=$CT0_ID), index 1 = $CT1_ARN (tofu-address=$CT1_ADDR_TAG, id=$CT1_ID) - read via the AWS CLI"

    COUNT_NOOP_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; COUNT_NOOP_PLAN_RC=$?
    [ "$COUNT_NOOP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_NOOP_PLAN_OUT" | tail -30; fail "the post-add plan exited $COUNT_NOOP_PLAN_RC"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$COUNT_NOOP_PLAN_OUT" \
      || { grep -E '^  #' <<< "$COUNT_NOOP_PLAN_OUT"; fail "the plan right after adding the count block is not empty - the new instances did not bind their own markers cleanly"; }
    log "  No changes - both new instances plan empty immediately after creation"

    log "=== G1. scale count down: 2 -> 1 ==="
    count_test_block 1 > "$EST/day2_count.tf"
    COUNT_DOWN_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; COUNT_DOWN_PLAN_RC=$?
    [ "$COUNT_DOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | tail -30; fail "the scale-down plan exited $COUNT_DOWN_PLAN_RC"; }

    if [ "${BREAK_COUNT:-}" = "1" ]; then
      log "  BREAK_COUNT=1: asserting the WRONG instance (count_test[0]) was destroyed instead of count_test[1]"
      if grep -qE '^  # aws_iam_policy\.count_test\[0\] will be destroyed' <<< "$COUNT_DOWN_PLAN_OUT"; then
        fail "BREAK_COUNT=1: the plan actually destroys count_test[0] - this assertion is not load-bearing"
      fi
      log "  BREAK_COUNT=1: correctly does NOT destroy count_test[0] - the wrong-instance assertion above fails to hold, as it must"
    else
      grep -qE '^  # aws_iam_policy\.count_test\[1\] will be destroyed' <<< "$COUNT_DOWN_PLAN_OUT" \
        || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-down plan does not destroy count_test[1]"; }
      grep -qE '^  # aws_iam_policy\.count_test\[0\] will be' <<< "$COUNT_DOWN_PLAN_OUT" \
        && { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-down plan touches count_test[0], which should be untouched"; }
      grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$COUNT_DOWN_PLAN_OUT" \
        || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | tail -10; fail "choudoufu's scale-down plan proposes something other than exactly one destroy"; }
      log "  choudoufu: exactly one destroy (count_test[1]), count_test[0] untouched"

      COUNT_DOWN_APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_DOWN_APPLY_RC=$?
      [ "$COUNT_DOWN_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_DOWN_APPLY_OUT" | tail -30; fail "the scale-down apply exited $COUNT_DOWN_APPLY_RC"; }
      grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$COUNT_DOWN_APPLY_OUT" \
        || { grep -E 'Apply complete' <<< "$COUNT_DOWN_APPLY_OUT"; fail "the scale-down apply was not exactly one destroy"; }

      CT0_ID_AFTER_DOWN="$(awsl iam get-policy --policy-arn "$CT0_ARN" --query 'Policy.PolicyId' --output text 2>/dev/null || true)"
      [ "$CT0_ID_AFTER_DOWN" = "$CT0_ID" ] || fail "count_test[0]'s PolicyId changed across the scale-down ($CT0_ID -> $CT0_ID_AFTER_DOWN) - it was destroyed and recreated, not left alone"
      if CT1_STILL="$(awsl iam get-policy --policy-arn "$CT1_ARN" 2>&1)"; then
        echo "$CT1_STILL"; fail "count_test[1] ($CT1_ARN) still exists in the live account after the scale-down destroy"
      fi
      CT0_ADDR_AFTER_DOWN="$(awsl iam list-policy-tags --policy-arn "$CT0_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
      [ "$CT0_ADDR_AFTER_DOWN" = 'aws_iam_policy.count_test:0' ] || fail "count_test[0]'s tofu-address tag changed across the scale-down: $CT0_ADDR_AFTER_DOWN"
      log "  $CT1_ARN (count_test[1]) no longer exists (NoSuchEntity); $CT0_ARN (count_test[0]) unchanged PolicyId ($CT0_ID) and marker - all read via the AWS CLI"

      log "=== G2. scale count back up: 1 -> 2 ==="
      count_test_block 2 > "$EST/day2_count.tf"
      COUNT_UP_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; COUNT_UP_PLAN_RC=$?
      [ "$COUNT_UP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | tail -30; fail "the scale-up plan exited $COUNT_UP_PLAN_RC"; }
      grep -qE '^  # aws_iam_policy\.count_test\[1\] will be created' <<< "$COUNT_UP_PLAN_OUT" \
        || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-up plan does not create count_test[1]"; }
      grep -qE '^  # aws_iam_policy\.count_test\[0\] will be' <<< "$COUNT_UP_PLAN_OUT" \
        && { printf '%s\n' "$COUNT_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-up plan touches count_test[0], which should be untouched"; }
      grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$COUNT_UP_PLAN_OUT" \
        || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | tail -10; fail "choudoufu's scale-up plan proposes something other than exactly one create"; }
      log "  choudoufu: exactly one create (count_test[1]), count_test[0] untouched"

      COUNT_UP_APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_UP_APPLY_RC=$?
      [ "$COUNT_UP_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_UP_APPLY_OUT" | tail -30; fail "the scale-up apply exited $COUNT_UP_APPLY_RC"; }
      grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$COUNT_UP_APPLY_OUT" \
        || { grep -E 'Apply complete' <<< "$COUNT_UP_APPLY_OUT"; fail "the scale-up apply was not exactly one create"; }

      CT1_NEW_ARN="$(awsl iam list-policies --path-prefix /example/ --query "Policies[?PolicyName=='iam-ro-count-test-1'].Arn | [0]" --output text)"
      [ -n "$CT1_NEW_ARN" ] && [ "$CT1_NEW_ARN" != "None" ] || fail "no live count_test[1] policy found by name after the scale-up"
      [ "$CT1_NEW_ARN" = "$CT1_ARN" ] || fail "the recreated count_test[1]'s ARN ($CT1_NEW_ARN) differs from its pre-destroy ARN ($CT1_ARN) - unexpected: aws_iam_policy's ARN is name/path-derived and should be identical both times"
      CT1_NEW_ID="$(awsl iam get-policy --policy-arn "$CT1_NEW_ARN" --query 'Policy.PolicyId' --output text)"
      [ "$CT1_NEW_ID" != "$CT1_ID" ] || fail "count_test[1] came back with the SAME PolicyId ($CT1_ID) it had before being destroyed - the destroy in G1 was not real"
      CT1_NEW_ADDR_TAG="$(awsl iam list-policy-tags --policy-arn "$CT1_NEW_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
      [ "$CT1_NEW_ADDR_TAG" = 'aws_iam_policy.count_test:1' ] || fail "the recreated count_test[1] ($CT1_NEW_ARN) carries tofu-address=$CT1_NEW_ADDR_TAG, not aws_iam_policy.count_test:1"
      CT0_ID_AFTER_UP="$(awsl iam get-policy --policy-arn "$CT0_ARN" --query 'Policy.PolicyId' --output text 2>/dev/null || true)"
      [ "$CT0_ID_AFTER_UP" = "$CT0_ID" ] || fail "count_test[0]'s PolicyId changed across the scale-up"
      log "  count_test[1] recreated under the same ARN ($CT1_NEW_ARN, deterministic from name+path) but a NEW PolicyId ($CT1_NEW_ID, was $CT1_ID), tofu-address=$CT1_NEW_ADDR_TAG; count_test[0] ($CT0_ARN, id=$CT0_ID) untouched throughout the down-then-up cycle - all read via the AWS CLI"

      log "=== G3. one more plan: config and reality agree, nothing left to propose ==="
      COUNT_FINAL_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; COUNT_FINAL_PLAN_RC=$?
      [ "$COUNT_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_FINAL_PLAN_OUT" | tail -30; fail "the post-scale-up plan exited $COUNT_FINAL_PLAN_RC"; }
      grep -qF "No changes. Your infrastructure matches the configuration." <<< "$COUNT_FINAL_PLAN_OUT" \
        || { grep -E '^  #' <<< "$COUNT_FINAL_PLAN_OUT"; fail "the post-scale-up plan is not empty"; }
      log "  No changes. The scale-down-then-up cycle is complete and invisible to the next plan."

      gauntlet_stage day2_count pass "choudoufu: scaling aws_iam_policy.count_test from 2 to 1 destroyed exactly count_test[1] (0 add, 0 change, 1 destroy), leaving count_test[0]'s live PolicyId and tofu-address marker unchanged; scaling back from 1 to 2 created exactly count_test[1] under the SAME ARN (deterministic from name+path) but a NEW PolicyId (0 add, 0 change -> 1 add, 0 change, 0 destroy) while count_test[0] stayed untouched throughout; the next plan is empty; the G-ORACLE stock oracle on the same 2-instance count block, applied fresh in the idle greenfield-oracle account, shows the identical shape: destroy the higher index only, create the higher index back under the same ARN but a new PolicyId, the lower index's PolicyId unchanged both times"
    fi
    gauntlet_end_stage
  fi
  gauntlet_end_stage
fi

gauntlet_end_stage
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
