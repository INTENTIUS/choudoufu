#!/usr/bin/env bash
set -uo pipefail

# A real third-party estate crossed against a real emulator: issue #274's
# step 6, for .corpus/iam/examples/iam-policy.
#
# This is a terraform-aws-modules EXAMPLE, not one org's private estate. That
# distinction matters: the terraform-aws-iam examples are the configuration
# an average user copies when they first reach for the aws provider, and
# before this script existed nobody had crossed a module example at all -
# every prior real-estate crossing was somebody's hand-written root module.
# "does the module-example path work end to end" was an open question.
#
# The estate: two aws_iam_policy instances behind the iam-policy module - one
# from a literal policy document, one from a rendered aws_iam_policy_document
# data source - plus a third module instantiation with create = false that
# contributes nothing. It passes live-check with zero refused sites and, until
# this script existed, had never touched a cloud.
#
# THE ONE DELTA. Nothing here needed a provider pin, a backend edit, or an
# emulator override beyond the standard three flags: the example's own
# `version = ">= 6.28"` resolved straight to 6.60.0 with list resources
# intact, and it declares no cloud/backend block to remove. The only real
# edit is the provider block gaining floci's flags. That absence is itself a
# finding: not every real estate costs #268 or #269, and this one didn't.
#
# WHAT IS ASSERTED, and why it is not the verdict. An empty plan says the two
# policies bound. It does not say WHAT they bound BY. Step 5 below reads both
# rendered identity strings out of the run's own trace and checks each one
# against a live aws_iam_policy read through the AWS CLI, never through
# choudoufu. BREAK=1 corrupts one expected string the way a real defect
# would - not a string nothing could produce, but the CORRECT policy ARN with
# the wrong module's suffix - and step 5 must be the only step that goes red.
#
#   bash live/e2e/corpus-iam-policy/run.sh
#
# Needs Docker and the AWS CLI. .corpus is read, never written: the estate is
# copied out to a temp directory first, same as every other corpus crossing.
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4680, clear of run.sh's
#                4566 and every other live/e2e fixture's port).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        set to 1 to corrupt one expected identity string before
#                step 5, proving the identity assertion is load-bearing.
#
# Exit codes: 0 on a real pass, non-zero on a real failure. Every assertion
# reads command output, an exit code, or the emulator's own answer through
# the AWS CLI.

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
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }

# ── 0. tools and corpus ─────────────────────────────────────────────────────
log "=== 0. tools and corpus ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
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

# ── 1. the one delta ─────────────────────────────────────────────────────────
log "=== 1. the one onboarding delta ==="
perl -0pi -e 's/(provider "aws" \{\n  region = "eu-west-1"\n)\}/$1\n  access_key                   = "test"\n  secret_key                   = "test"\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  s3_use_path_style            = true\n}/' "$EST/main.tf"
grep -q 's3_use_path_style' "$EST/main.tf" || fail "the emulator delta did not match main.tf - the corpus pin has moved"
perl -0pi -e 's/(required_providers \{\n    aws = \{\n      source  = "hashicorp\/aws"\n      version = ">= 6.28"\n    \}\n  \}\n)\}/$1\n  live {\n    estate = "'"$ESTATE"'"\n  }\n}/' "$EST/versions.tf"
grep -q "estate = \"$ESTATE\"" "$EST/versions.tf" || fail "the live block delta did not match versions.tf - the corpus pin has moved"
log "  DELTA  emulator flags + live block added; no backend, no version pin needed"

# ── 2. floci ────────────────────────────────────────────────────────────────
log "=== 2. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"iam"' <<< "$HEALTH" && break
  sleep 2
done
grep -q '"iam"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (iam) at $ENDPOINT"
log "  healthy"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION"

# ── 3. stand the estate up ──────────────────────────────────────────────────
log "=== 3. init and apply: 2 policies (a third module instance is create=false) ==="
( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null ) || fail "init failed"
APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY_OUT" | tail -40
  fail "the apply failed"
}
grep -qE 'Apply complete! Resources: 2 added' <<< "$APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY_OUT"; fail "the apply did not create exactly 2 resources"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY_OUT")"

# Read the policies back through the AWS CLI, never through choudoufu.
POLICY1_ARN="arn:aws:iam::${ACCOUNT}:policy/example_from_data_source"
POLICY2_ARN="$(awsl iam list-policies --path-prefix / \
  --query "Policies[?starts_with(PolicyName, 'example-') == \`true\`].Arn | [0]" --output text)"
[ -n "$POLICY2_ARN" ] && [ "$POLICY2_ARN" != "None" ] || fail "could not find the name_prefix policy through the AWS CLI"
log "  both policies live: $POLICY1_ARN and $POLICY2_ARN"

MARKED="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$MARKED" = "2" ] || fail "expected 2 objects carrying tofu-estate=$ESTATE, got $MARKED"
log "  2 of 2 objects carry markers; aws_iam_policy is fully taggable"

# ── 4. no state file, ever ──────────────────────────────────────────────────
rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"
[ ! -f "$EST/terraform.tfstate" ] || fail "the state file is still there"
log "=== 4. state file deleted ==="

# ── 5. THE VALUE, not the verdict ───────────────────────────────────────────
log "=== 5. live-plan, and the rendered identities read out of the run ==="
plan_into() {
  ( cd "$EST" && TF_LOG=trace "$TOFU" live-plan -input=false -no-color )
}
PLAN_OUT="$(plan_into 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -40; fail "live-plan exited $PLAN_RC"; }
[ ! -f "$EST/terraform.tfstate" ] || fail "live-plan wrote a state file"
grep -qE '^Plan: 0 to add, 0 to change, 0 to destroy' <<< "$PLAN_OUT" \
  || { grep -E '^Plan:' <<< "$PLAN_OUT"; fail "the plan is not empty of resource changes"; }
grep -qE '^Foreign resources: (none|nothing was swept)' <<< "$PLAN_OUT" \
  || { grep -E '^Foreign resources:' <<< "$PLAN_OUT"; fail "the plan reports foreign resources"; }
log "  0 to add, 0 to change, 0 to destroy; nothing foreign"

WANT=("$POLICY1_ARN" "$POLICY2_ARN")
if [ "${BREAK:-}" = "1" ]; then
  WANT[0]="arn:aws:iam::${ACCOUNT}:policy/example_from_wrong_module"
  log "  BREAK=1: expecting ${WANT[0]}, the SAME account and the SAME shape of"
  log "           ARN as the real one, just the wrong policy name. The plan"
  log "           above stayed empty. This step must fail."
fi
for id in "${WANT[@]}"; do
  grep -qF "from import identity \"$id\"" <<< "$PLAN_OUT" || {
    grep -oE 'from import identity "[^"]*"' <<< "$PLAN_OUT" | sort -u
    fail "no instance materialized from import identity \"$id\". The identities the run actually rendered are listed above."
  }
done
GOT_N="$(grep -oE 'from import identity "[^"]*"' <<< "$PLAN_OUT" | sort -u | wc -l | tr -d ' ')"
[ "$GOT_N" = "2" ] || fail "the run materialized $GOT_N distinct identities, expected 2"
log "  both rendered identities asserted, and no third"

# ── 6. and it converges ─────────────────────────────────────────────────────
log "=== 6. the next run proposes nothing, and applying it adds nothing ==="
PLAN2_OUT="$(plan_into 2>&1)"; PLAN2_RC=$?
[ "$PLAN2_RC" -eq 0 ] || { printf '%s\n' "$PLAN2_OUT" | tail -30; fail "the second live-plan exited $PLAN2_RC"; }
grep -qE '^Plan: 0 to add, 0 to change, 0 to destroy' <<< "$PLAN2_OUT" \
  || { grep -E '^Plan:' <<< "$PLAN2_OUT"; fail "the second plan is not empty, so the run does not converge"; }

APPLY2_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY2_OUT" | tail -30; fail "the second apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY2_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY2_OUT"; fail "the second apply was not a no-op"; }
AFTER_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$AFTER_N" = "2" ] || fail "there are now $AFTER_N objects carrying tofu-estate=$ESTATE, not 2"
[ ! -f "$EST/terraform.tfstate" ] || fail "a state file exists after the second run"
log "  converged: nothing proposed, nothing added, still 2 policies, still no state file"

log ""
log "=== PASS ==="
log ""
log "A terraform-aws-modules EXAMPLE - the configuration a new user copies"
log "first - applied 2 policies against an emulator, lost its state file, and"
log "replanned empty twice. Both rendered identities were checked against"
log "IAM's own answer. Run again with BREAK=1: everything above step 5 still"
log "passes and step 5 goes red."
