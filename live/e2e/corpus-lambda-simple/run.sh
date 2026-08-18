#!/usr/bin/env bash
set -uo pipefail

# The five-stage real-estate crossing (see live/corpus-crossing-manifest.json)
# for .corpus/lambda/examples/simple, from terraform-aws-modules/terraform-
# aws-lambda pinned at v8.8.1 (live/corpus-manifest.json). Lambda is one of
# the most commonly deployed AWS services via Terraform, and this module is
# the de facto standard way people provision it; "simple" is its minimal
# entry point: one module call (module.lambda_function), publishing a
# Python function whose name is derived from a random_pet.
#
# Stages:
#   1. COLD DEPLOY   plain `terraform apply`, no live block, no choudoufu
#                     anywhere - the honest proof the estate is real and
#                     buildable, and genuinely unmarked live infra to adopt.
#   2. MIGRATE        `choudoufu live-import -state=... -estate=... -approve`
#                     against that cold state.
#   3. TEST PLAN      delete the state file, `choudoufu live-plan`, assert
#                     empty AND assert the rendered identity strings.
#   4. TEST APPLY     apply the empty plan, assert a genuine no-op.
#   5. DRIFT + RECONVERGE  mutate one live object out of band, replan,
#                     assert the diff proposes fixing exactly that object.
#
# WHAT THIS RUN ACTUALLY FOUND, first pass (before any fix in this branch):
# stage 2 reported "0 of 1 resource instance(s) are eligible for stamping"
# and "7 resource instance(s) in a non-root module were not considered
# (root module only, v1; see issue #59)". Every real resource this module
# creates - the IAM role, its inline log policy, the Lambda function, the
# CloudWatch log group - lives under module.lambda_function.*, because
# calling a published module is how essentially every real Terraform root
# module uses one. `internal/live/liveimport/ratify.go` skipped every
# non-root module wholesale, a restriction its own comments attributed to
# issue #59 - which is CLOSED, and whose closing scope explicitly gave the
# other four root-only walkers (identity, discovery, stamp, projection, mv)
# real module traversal. `live-import` was never updated to match; this was
# a live regression against a shipped capability, not a documented gap. See
# the fix in internal/live/liveimport/ratify.go (this branch) - three real
# AWS resources now stamp correctly with module-qualified tofu-address tags,
# verified below by reading the tags directly with the AWS CLI.
#
# Fixing that uncovers a SECOND, separate, real blocker at stage 3:
# `choudoufu live-plan` refuses the estate outright with "Resource type is
# outside the live-markers subset" for aws_lambda_function_url.this and
# aws_lambda_function_recursion_config.this - even though both have
# `count = ... ? 1 : 0` with the `? 1` condition statically false in this
# example (var.create_lambda_function_url defaults to false;
# var.recursive_loop defaults to null, and the config never overrides
# either), so stock OpenTofu creates zero instances of either type and
# would never even need their schema. Type admission here runs once per
# declared resource block, not once per resolved instance, so a provably-
# zero-instance block still has to pass admission before ANYTHING in the
# estate plans - a parity gap against the standing bar in HANDOFF.md
# ("if upstream accepts a configuration we refuse, that is a defect").
# This is NOT this branch's fix to make (see this run's own report for why:
# it needs a general static-count-is-zero evaluator feeding the identity
# walker, which is a materially different, larger piece of work than the
# module-scope fix above, and nothing in this repository already has it).
# So this script currently proves stages 1 and 2 for real and stops at
# stage 3 with the actual product error - stages 4 and 5 are unwritten
# because there is nothing running yet for them to exercise. Once the
# stage-3 blocker clears, complete stage 3's identity assertions and add 4
# and 5 following live/e2e/reference-ec2-vpc/run.sh's shape.
#
#   bash live/e2e/corpus-lambda-simple/run.sh
#
# Needs Docker, the AWS CLI, and python3 (the module's package.py builds the
# deployment zip locally through a `data "external"` block - no network).
# .corpus is read, never written: the estate is copied out to a temp
# directory first, same as every other corpus crossing.
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4714).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        set to 1 to corrupt one expected identity string before the
#                stage-2 tag assertions, proving they are load-bearing
#                rather than a grep that always matches. It does not affect
#                stage 3, which fails for the same real reason either way.
#
# Exit codes: 0 on a real pass of every stage this script currently
# exercises, non-zero on a real failure (including the current, documented
# stage-3 block). Every assertion reads command output, an exit code, or the
# emulator's own answer through the AWS CLI, never choudoufu's own report of
# itself.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SRC_MODULE="$ROOT/.corpus/lambda"
SRC_EXAMPLE="$ROOT/.corpus/lambda/examples/simple"
SRC_FIXTURES="$ROOT/.corpus/lambda/examples/fixtures"
WORK="$(mktemp -d)"
EST="$WORK/lambda/examples/simple"
FLOCI_PORT="${FLOCI_PORT:-4714}"
FLOCI_NAME="choudoufu-corpus-lambda-simple-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="lambda-simple-crossing"
REGION="eu-west-1"
ACCOUNT="000000000000"

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
command -v python3 >/dev/null 2>&1 || fail "python3 is not on PATH - package.py needs it to build the deployment zip"
[ -d "$SRC_MODULE" ] || fail "$SRC_MODULE is missing - run 'just corpus-fetch' first"
[ -d "$SRC_EXAMPLE" ] || fail "$SRC_EXAMPLE is missing - run 'just corpus-fetch' first"
[ -d "$SRC_FIXTURES" ] || fail "$SRC_FIXTURES is missing - run 'just corpus-fetch' first"

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
# module, the example, and the fixtures the example's source_path reaches
# are copied out, preserving the relative paths main.tf's
# `source = "../../"` and `source_path = ["${path.module}/../fixtures/..."]`
# both expect.
mkdir -p "$WORK/lambda"
rsync -a --exclude 'examples' --exclude 'tests' --exclude '.git' "$SRC_MODULE/" "$WORK/lambda/"
mkdir -p "$WORK/lambda/examples/simple" "$WORK/lambda/examples/fixtures"
cp -R "$SRC_EXAMPLE/." "$EST/"
cp -R "$SRC_FIXTURES/." "$WORK/lambda/examples/fixtures/"
rm -rf "$EST/.terraform" "$EST/.terraform.lock.hcl"
[ -f "$EST/main.tf" ] || fail "the estate copy is missing main.tf"
log "  module + example + fixtures copied out of .corpus into $WORK"

# ── 1. the one delta - emulator flags, no live block yet ───────────────────
log "=== 1. cold deploy: plain terraform, no live block, no choudoufu ==="
command -v terraform >/dev/null 2>&1 || fail "the terraform binary is not on PATH - needed to build unmarked reference infra"
perl -0pi -e 's/(provider "aws" \{\n  region = "eu-west-1"\n)(.*?\n)(\}\n)/$1  access_key                  = "test"\n  secret_key                  = "test"\n  skip_requesting_account_id  = true\n  s3_use_path_style           = true\n$2$3/s' "$EST/main.tf"
grep -q 's3_use_path_style' "$EST/main.tf" || fail "the emulator delta did not match main.tf - the corpus pin has moved"
log "  DELTA  emulator flags added to the provider block; no backend, no version pin needed"

log "=== 2. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"lambda"' <<< "${HEALTH:-}" && break
  sleep 2
done
grep -q '"lambda"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (lambda) at $ENDPOINT"
log "  healthy"

export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" AWS_ENDPOINT_URL="$ENDPOINT"

log "=== 3. cold init and apply: plain terraform, 8 resources from nothing ==="
( cd "$EST" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$EST" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "cold terraform init failed"; }
COLD_APPLY_OUT="$(cd "$EST" && terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$COLD_APPLY_OUT" | tail -40
  fail "the cold apply failed"; }
grep -qE 'Apply complete! Resources: 8 added' <<< "$COLD_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$COLD_APPLY_OUT"; fail "the cold apply did not create exactly 8 resources"; }
log "  $(grep -E 'Apply complete' <<< "$COLD_APPLY_OUT" | head -1)"
[ -f "$EST/terraform.tfstate" ] || fail "plain terraform left no state file to migrate from"

PET="$(python3 -c "
import json
s = json.load(open('$EST/terraform.tfstate'))
for r in s['resources']:
    if r['type'] == 'random_pet' and r['name'] == 'this':
        print(r['instances'][0]['attributes']['id'])
")"
[ -n "$PET" ] || fail "could not read random_pet.this's id back out of the cold state"
FN_NAME="${PET}-lambda-simple"
log "  function_name resolved to $FN_NAME (random_pet.this = $PET)"

# Confirmed unmarked: plain terraform never wrote a tofu-address tag.
LAMBDA_ARN="arn:aws:lambda:${REGION}:${ACCOUNT}:function:${FN_NAME}"
COLD_TAGS="$(awsl lambda list-tags --resource "$LAMBDA_ARN" --query 'length(Tags)' --output text 2>/dev/null || echo 0)"
[ "$COLD_TAGS" = "0" ] || fail "the cold-deployed function already carries $COLD_TAGS tag(s) before migration - this test proves nothing"
log "  confirmed unmarked: $LAMBDA_ARN carries no tags"

cp "$EST/terraform.tfstate" "$WORK/cold.tfstate"

log ""
log "STAGE 1 (cold deploy): PASS"
log ""

# ── STAGE 2: MIGRATE ─────────────────────────────────────────────────────
log "=== 4. add the live block (record_store, for the estate's random_pet/"
log "        null_resource/terraform_data residue) ==="
perl -0pi -e 's/(random = \{\n      source  = "hashicorp\/random"\n      version = ">= 2.0"\n    \}\n  \}\n)\}/$1\n  live {\n    estate = "'"$ESTATE"'"\n    record_store "local" {\n      path = ".tofu-records"\n    }\n  }\n}/' "$EST/versions.tf"
grep -q "estate = \"$ESTATE\"" "$EST/versions.tf" || fail "the live block delta did not match versions.tf - the corpus pin has moved"

( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "choudoufu init failed"; }

log "=== 5. choudoufu live-import against the cold state, read-only first ==="
IMPORT_OUT="$(cd "$EST" && "$TOFU" live-import -state="$WORK/cold.tfstate" -estate="$ESTATE" -no-color 2>&1)" || {
  printf '%s\n' "$IMPORT_OUT" | tail -40; fail "live-import (dry run) failed"; }
grep -qF "3 of 8 resource instance(s) are eligible for stamping" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import did not verify exactly 3 of 8 resources as eligible - the module-scope fix or the module's own resource shape has moved"; }
grep -qF "No tag has been written." <<< "$IMPORT_OUT" \
  || fail "the dry run wrote a tag - it must not"
# The three real AWS resources live under module.lambda_function - this is
# the module-scope fix under test. random_pet, local_file, null_resource and
# terraform_data all correctly report UNTAGGABLE or UNADMITTED_TYPE instead:
# none of them has an AWS tags argument, and this run never claims to stamp
# what it cannot.
grep -qF "module.lambda_function.aws_lambda_function.this[0]" <<< "$IMPORT_OUT" \
  || fail "live-import's report does not name the module-nested Lambda function at all - the module-scope fix regressed"
log "  3 of 8 verified against the live system (the module-nested IAM role, log group and function); nothing written yet"

log "=== 6. -approve: stamp the three module-nested AWS resources ==="
APPROVE_OUT="$(cd "$EST" && "$TOFU" live-import -state="$WORK/cold.tfstate" -estate="$ESTATE" -approve -no-color 2>&1)" || {
  printf '%s\n' "$APPROVE_OUT" | tail -40; fail "live-import -approve failed"; }
grep -qF "3 resource(s) newly stamped, 0 already stamped, 0 failed, 5 skipped" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not stamp exactly 3 of 8 resources cleanly"; }
log "  3 stamped"

log "=== 7. the markers, read through the AWS CLI directly - never through choudoufu ==="
WANT_LAMBDA_ADDR="module.lambda_function.aws_lambda_function.this:0"
WANT_ROLE_ADDR="module.lambda_function.aws_iam_role.lambda:0"
WANT_LOGGROUP_ADDR="module.lambda_function.aws_cloudwatch_log_group.lambda:0"
if [ "${BREAK:-}" = "1" ]; then
  WANT_LAMBDA_ADDR="module.lambda_alias.aws_lambda_function.this:0"
  log "  BREAK=1: expecting tofu-address=$WANT_LAMBDA_ADDR on the function - the"
  log "           SAME shape and the SAME resource type, just the wrong module"
  log "           name. This step must fail."
fi

GOT_LAMBDA_ADDR="$(awsl lambda list-tags --resource "$LAMBDA_ARN" --query 'Tags."tofu-address"' --output text)"
[ "$GOT_LAMBDA_ADDR" = "$WANT_LAMBDA_ADDR" ] || fail "aws_lambda_function.this carries tofu-address=$GOT_LAMBDA_ADDR, not $WANT_LAMBDA_ADDR"
GOT_LAMBDA_ESTATE="$(awsl lambda list-tags --resource "$LAMBDA_ARN" --query 'Tags."tofu-estate"' --output text)"
[ "$GOT_LAMBDA_ESTATE" = "$ESTATE" ] || fail "aws_lambda_function.this carries tofu-estate=$GOT_LAMBDA_ESTATE, not $ESTATE"

GOT_ROLE_ADDR="$(awsl iam list-role-tags --role-name "$FN_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_ROLE_ADDR" = "$WANT_ROLE_ADDR" ] || fail "aws_iam_role.lambda carries tofu-address=$GOT_ROLE_ADDR, not $WANT_ROLE_ADDR"

LOGGROUP_ARN="arn:aws:logs:${REGION}:${ACCOUNT}:log-group:/aws/lambda/${FN_NAME}"
GOT_LOGGROUP_ADDR="$(awsl logs list-tags-for-resource --resource-arn "$LOGGROUP_ARN" --query 'tags."tofu-address"' --output text 2>/dev/null \
  || awsl logs list-tags-log-group --log-group-name "/aws/lambda/${FN_NAME}" --query 'tags."tofu-address"' --output text)"
[ "$GOT_LOGGROUP_ADDR" = "$WANT_LOGGROUP_ADDR" ] || fail "aws_cloudwatch_log_group.lambda carries tofu-address=$GOT_LOGGROUP_ADDR, not $WANT_LOGGROUP_ADDR"

log "  function:   tofu-address=$GOT_LAMBDA_ADDR tofu-estate=$GOT_LAMBDA_ESTATE"
log "  iam role:   tofu-address=$GOT_ROLE_ADDR"
log "  log group:  tofu-address=$GOT_LOGGROUP_ADDR"
log "  all three module-nested markers verified directly against IAM/Lambda/Logs, not through choudoufu's own report"

log ""
log "STAGE 2 (migrate): PASS"
log ""

# ── STAGE 3: TEST PLAN ──────────────────────────────────────────────────────
log "=== 8. delete the state file, choudoufu live-plan ==="
rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"
[ ! -f "$EST/terraform.tfstate" ] || fail "the state file is still there"

PLAN_OUT="$(cd "$EST" && "$TOFU" live-plan -input=false -no-color 2>&1)"; PLAN_RC=$?
if [ "$PLAN_RC" -ne 0 ]; then
  log ""
  log "STAGE 3 (test plan): BLOCKED - a real, separate product limitation, not a"
  log "  script or floci problem. choudoufu live-plan refuses this estate before"
  log "  proposing anything, on two resource blocks whose count is statically"
  log "  zero in this example (aws_lambda_function_url.this and"
  log "  aws_lambda_function_recursion_config.this both use"
  log "  \`count = ... ? 1 : 0\` where the condition is a var with a false/null"
  log "  default this example never overrides), because type admission here"
  log "  runs per declared resource BLOCK, not per resolved instance. The"
  log "  actual error:"
  log ""
  printf '%s\n' "$PLAN_OUT" | grep -B1 -A2 "^Error:" | head -60
  log ""
  log "STAGE 4 (test apply): NOT REACHED"
  log "STAGE 5 (drift and reconverge): NOT REACHED"
  log ""
  log "Stages 1 and 2 are real, verified passes - see above. Stage 3's block is"
  log "reported, not routed around; see this run's final report for the fix"
  log "this needs (a static-count-is-zero evaluator ahead of type admission)"
  log "and why it is out of scope for the module-scope fix this branch makes."
  exit 1
fi

# The code below runs once the stage-3 blocker above clears. It is real,
# untested-in-this-run code, not a stub: once live-plan stops erroring, this
# is what completing the crossing looks like.
[ ! -f "$EST/terraform.tfstate" ] || fail "live-plan wrote a state file"
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$PLAN_OUT" \
  || { grep -E '^  #' <<< "$PLAN_OUT"; fail "live-plan is not empty"; }

for id in "$FN_NAME" "${FN_NAME}-logs"; do
  grep -qF "$id" <<< "$PLAN_OUT" || true
done
log "  no resource change proposed"

log ""
log "STAGE 3 (test plan): PASS"
log ""
log "STAGE 4 (test apply): NOT YET WRITTEN - stage 3 only just started passing in this run"
log "STAGE 5 (drift and reconverge): NOT YET WRITTEN - same reason"
