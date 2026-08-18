#!/usr/bin/env bash
set -uo pipefail

# A real third-party estate crossed against a real emulator: issue #274's
# step 6, for .corpus/iam/examples/iam-read-only-policy.
#
# A terraform-aws-modules EXAMPLE, and a different module than
# demo-corpus-iam-policy already crosses: the iam-read-only-policy module
# builds its policy document from a generated `allowed_services` matrix
# rather than a literal document or a data source, and this example
# instantiates it three times - once creating a policy, once with
# `create_policy = false` (renders the document, creates nothing), once with
# `create = false` (does nothing at all). Only the first module call
# contributes a resource. It passes live-check with zero refused sites and,
# until this script existed, had never touched a cloud. Picked as one of the
# three smallest untouched real corpus estates (issue #274's campaign),
# smallest-first.
#
# THE ONE DELTA. Same shape as demo-corpus-iam-policy: the example's own
# `version = ">= 6.28"` resolves straight to 6.60.0 with list resources
# intact, and it declares no cloud/backend block to remove. The only real
# edit is the provider block gaining floci's flags.
#
# THE NAME. The module's own `use_name_prefix` defaults to true, so
# `"ex-${basename(path.cwd)}"` - `path.cwd`, one of the four sources
# (var/local/path/terraform) the config-language subset allows to be
# statically evaluable - is used as a PREFIX, and IAM appends its own random
# suffix. The identity is therefore server-assigned, not statically
# derivable from configuration alone (the NAME_PREFIX discovery shape, the
# same one demo-corpus-oidc-provider's role exercises). Step 3 reads the ARN
# IAM actually minted rather than predicting one.
#
# THE OUTPUTS QUIRK, same as demo-corpus-iam-policy. This estate declares
# root `output` blocks, and live-plan holds no state between runs, so there
# is never a prior output baseline to diff against - every run therefore
# shows a permanent "Changes to Outputs" section, and OpenTofu's renderer
# never prints a "Plan: N to add, N to change, N to destroy" line while that
# is true, empty plan or not. Step 5 asserts the absence of a resource
# action header instead.
#
#   bash live/e2e/corpus-iam-read-only-policy/run.sh
#
# Needs Docker and the AWS CLI. .corpus is read, never written: the example
# AND the module it references (preserving the relative path
# `../../modules/iam-read-only-policy`) are copied out to a temp directory
# first, same as every other corpus crossing.
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4699, clear of every
#                other live/e2e fixture's port).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        set to 1 to corrupt the expected identity string before
#                step 5, proving the identity assertion is load-bearing.
#
# Exit codes: 0 on a real pass, non-zero on a real failure. Every assertion
# reads command output, an exit code, or the emulator's own answer through
# the AWS CLI.

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

mkdir -p "$WORK/iam/examples" "$WORK/iam/modules"
cp -R "$SRC_EXAMPLE" "$WORK/iam/examples/iam-read-only-policy"
cp -R "$SRC_MODULE" "$WORK/iam/modules/iam-read-only-policy"
rm -rf "$EST/.terraform" "$EST/.terraform.lock.hcl"
[ -f "$EST/main.tf" ] || fail "the estate copy is missing main.tf"
log "  estate + module copied out of .corpus into $WORK"

# ── 1. the one delta ─────────────────────────────────────────────────────────
log "=== 1. the one onboarding delta ==="
perl -0pi -e 's/(provider "aws" \{\n  region = "eu-west-1"\n)\}/$1\n  access_key                   = "test"\n  secret_key                   = "test"\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  s3_use_path_style            = true\n}/' "$EST/main.tf"
grep -q 's3_use_path_style' "$EST/main.tf" || fail "the emulator delta did not match main.tf - the corpus pin has moved"
perl -0pi -e 's/(required_providers \{\n    aws = \{\n      source  = "hashicorp\/aws"\n      version = ">= 6\.28"\n    \}\n  \}\n)\}/$1\n  live {\n    estate = "'"$ESTATE"'"\n  }\n}/' "$EST/versions.tf"
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
log "=== 3. init and apply: 1 policy (the other two module instances contribute nothing) ==="
( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null ) || fail "init failed"
APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY_OUT" | tail -40
  fail "the apply failed"
}
grep -qE 'Apply complete! Resources: 1 added' <<< "$APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY_OUT"; fail "the apply did not create exactly 1 resource"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY_OUT")"

# Read the policy back through the AWS CLI, never through choudoufu. The
# module's own use_name_prefix defaults to true, so the name is
# "ex-${basename(path.cwd)}-" used as a PREFIX - basename of the temp dir
# this script copied the estate into - and IAM appends its own random
# suffix. The policy's identity is therefore server-assigned, not
# statically derivable from configuration alone; the assertion below reads
# the ARN AWS actually minted rather than predicting one.
NAME_PREFIX="ex-$(basename "$EST")-"
POLICY_ARN="$(awsl iam list-policies --path-prefix /example/ \
  --query "Policies[?starts_with(PolicyName, '$NAME_PREFIX') == \`true\`].Arn | [0]" --output text)"
[ -n "$POLICY_ARN" ] && [ "$POLICY_ARN" != "None" ] \
  || fail "could not find a policy named with prefix $NAME_PREFIX through the AWS CLI"
log "  the policy lives: $POLICY_ARN"

# ── 4. no state file, ever ──────────────────────────────────────────────────
rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"
[ ! -f "$EST/terraform.tfstate" ] || fail "the state file is still there"
log "=== 4. state file deleted ==="

# ── 5. THE VALUE, not the verdict ───────────────────────────────────────────
log "=== 5. live-plan, and the rendered identity read out of the run ==="
plan_into() {
  ( cd "$EST" && TF_LOG=trace "$TOFU" live-plan -input=false -no-color )
}
PLAN_OUT="$(plan_into 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -40; fail "live-plan exited $PLAN_RC"; }
[ ! -f "$EST/terraform.tfstate" ] || fail "live-plan wrote a state file"
# Not a "Plan:" grep - see the header comment about root outputs.
grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$PLAN_OUT" \
  && { grep -E '^  # .+ will be' <<< "$PLAN_OUT"; fail "the plan proposes a resource change"; }
grep -qE '^Foreign resources: (none|nothing was swept)' <<< "$PLAN_OUT" \
  || { grep -E '^Foreign resources:' <<< "$PLAN_OUT"; fail "the plan reports foreign resources"; }
log "  no resource change proposed; nothing foreign"

WANT="$POLICY_ARN"
if [ "${BREAK:-}" = "1" ]; then
  WANT="arn:aws:iam::${ACCOUNT}:policy/example/ex-wrong-directory-name"
  log "  BREAK=1: expecting $WANT, the SAME account and the SAME shape of ARN"
  log "           as the real one, just the wrong policy name. The plan above"
  log "           stayed empty. This step must fail."
fi
grep -qF "from import identity \"$WANT\"" <<< "$PLAN_OUT" || {
  grep -oE 'from import identity "[^"]*"' <<< "$PLAN_OUT" | sort -u
  fail "no instance materialized from import identity \"$WANT\". The identities the run actually rendered are listed above."
}
GOT_N="$(grep -oE 'from import identity "[^"]*"' <<< "$PLAN_OUT" | sort -u | wc -l | tr -d ' ')"
[ "$GOT_N" = "1" ] || fail "the run materialized $GOT_N distinct identities, expected 1"
log "  the rendered identity asserted, and no other"

# ── 6. and it converges ─────────────────────────────────────────────────────
log "=== 6. the next run proposes nothing, and applying it adds nothing ==="
PLAN2_OUT="$(plan_into 2>&1)"; PLAN2_RC=$?
[ "$PLAN2_RC" -eq 0 ] || { printf '%s\n' "$PLAN2_OUT" | tail -30; fail "the second live-plan exited $PLAN2_RC"; }
grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$PLAN2_OUT" \
  && { grep -E '^  # .+ will be' <<< "$PLAN2_OUT"; fail "the second plan proposes a resource change, so the run does not converge"; }

APPLY2_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY2_OUT" | tail -30; fail "the second apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY2_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY2_OUT"; fail "the second apply was not a no-op"; }
STILL="$(awsl iam list-policies --path-prefix /example/ \
  --query "length(Policies[?starts_with(PolicyName, '$NAME_PREFIX') == \`true\`])" --output text)"
[ "$STILL" = "1" ] || fail "expected exactly 1 policy prefixed $NAME_PREFIX afterward, got $STILL"
[ ! -f "$EST/terraform.tfstate" ] || fail "a state file exists after the second run"
log "  converged: nothing proposed, nothing added, still 1 policy, still no state file"

log ""
log "=== PASS ==="
log ""
log "A terraform-aws-modules EXAMPLE using a different iam module than"
log "demo-corpus-iam-policy - one that builds its policy from a generated"
log "allowed_services matrix, instantiated three times with only one call"
log "contributing a resource. Applied 1 policy against an emulator, lost its"
log "state file, and replanned empty twice. The rendered identity was"
log "checked against IAM's own answer. Run again with BREAK=1: everything"
log "above step 5 still passes and step 5 goes red."
