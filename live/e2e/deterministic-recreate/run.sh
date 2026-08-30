#!/usr/bin/env bash
set -uo pipefail

# The deterministic-identity fixture, issue #541: an aws_iam_policy applied,
# destroyed, and recreated with the identical name and path, end to end
# against a real emulator.
#
# The claim under test, in one sentence: an IAM policy's identity (its ARN)
# is assembled from the account, name and path - all known before the
# create call goes out - so it comes back IDENTICAL after a genuine
# destroy-and-recreate, while the policy's own PolicyId (server-minted,
# fresh on every real create) does not. That is the "deterministic"
# identity kind #522's ruling asks tier 1 to cover, and until this fixture
# nothing did.
#
# Why this exists. PR #500's worker copied the reference estate's
# day2_replace assertion - "a forced replacement creates the new object,
# destroys the old one, and the new identity differs from the old" - onto
# an instance whose identity does NOT change on replace, and the run
# failed. That assertion is only true for a SERVER-MINTED identity kind
# (a VPC id, a hosted zone id, anything the provider hands back as an
# opaque string with no relationship to what was asked for). An IAM
# policy's ARN is not that: it is a deterministic function of inputs this
# configuration and this account both already know, the same as
# aws_iam_user's name-built identity (internal/live/identity/table_generated.go).
# PolicyId is the discriminator that actually changes; this script proves
# both halves rather than asserting one and assuming the other.
#
# The premise is checked against the emulator directly, with no tofu in the
# loop, before choudoufu ever runs (step 2): create the same policy name and
# path three times over, deleting between each, and confirm the ARN never
# moves while PolicyId does every time. A fixture whose own premise was
# never verified against the emulator it runs on is not evidence.
#
#   bash live/e2e/deterministic-recreate/run.sh
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4608, clear of every
#                other shape fixture's own default: 4599, 4601, 4602, 4604,
#                4605, 4606, 4607, 4742).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#
# Exit codes: 0 on a real pass, non-zero on a real failure. Every assertion
# reads actual command output, an exit code, or the emulator's own answer
# through the AWS CLI - never a timeout.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
FIXTURE="$ROOT/live/e2e/deterministic-recreate"
WORK="$(mktemp -d)"
FLOCI_PORT="${FLOCI_PORT:-4608}"
FLOCI_NAME="choudoufu-deterministic-recreate-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="deterministic-recreate-e2e"
POLICY_NAME="det-recreate-e2e-subject"
POLICY_ARN="arn:aws:iam::000000000000:policy/${POLICY_NAME}"

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
awsl() { aws --endpoint-url "$ENDPOINT" --region us-east-1 "$@"; }

# ── 0. tools ────────────────────────────────────────────────────────────────
log "=== 0. tools ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"

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

MAIN="$WORK/estate"
mkdir -p "$MAIN"

# ── 1. floci ────────────────────────────────────────────────────────────────
log "=== 1. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
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
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

# ── 2. the premise, checked with no tofu in the loop ────────────────────────
# Create and delete the same policy name+path three times over, straight
# through the AWS CLI. If the ARN ever moved, or PolicyId ever repeated,
# this fixture would be pinning something the emulator does not actually
# do - see HANDOFF.md, "before calling anything upstream, read the API
# directly, with no tofu in the loop".
log "=== 2. the premise, against the emulator, with no tofu involved ==="
PREMISE_POLICY_DOC='{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*"}]}'
declare -a PREMISE_IDS=()
for i in 1 2 3; do
  OUT="$(awsl iam create-policy --policy-name "premise-check-$$" --policy-document "$PREMISE_POLICY_DOC" \
    --query 'Policy.[Arn,PolicyId]' --output text)" \
    || fail "premise check $i: create-policy failed"
  ARN_I="$(cut -f1 <<< "$OUT")"
  ID_I="$(cut -f2 <<< "$OUT")"
  [ "$ARN_I" = "arn:aws:iam::000000000000:policy/premise-check-$$" ] \
    || fail "premise check $i: unexpected ARN $ARN_I"
  PREMISE_IDS+=("$ID_I")
  awsl iam delete-policy --policy-arn "$ARN_I" >/dev/null \
    || fail "premise check $i: delete-policy failed"
done
UNIQUE_IDS="$(printf '%s\n' "${PREMISE_IDS[@]}" | sort -u | wc -l | tr -d ' ')"
[ "$UNIQUE_IDS" = "3" ] \
  || fail "premise check: PolicyId repeated across 3 create/delete cycles (${PREMISE_IDS[*]}) - this emulator does not mint a fresh one, so this fixture would prove nothing on it"
log "  3 create/delete cycles: the ARN never moved, PolicyId was fresh all 3 times"

# ── 3. stand the estate up ──────────────────────────────────────────────────
log "=== 3. apply: the policy, once ==="
cp "$FIXTURE/main.tf" "$MAIN/main.tf"
( cd "$MAIN" && "$TOFU" init -input=false -no-color >/dev/null ) || fail "init failed"
APPLY1="$(cd "$MAIN" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY1" | grep -E '^Error|^│' | head -20
  fail "the first apply failed"; }
grep -qE 'Apply complete! Resources: 1 added' <<< "$APPLY1" \
  || { grep -E 'Apply complete' <<< "$APPLY1"; fail "the apply did not create exactly 1 resource"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY1")"

# Read the live object back through the AWS CLI, never through choudoufu.
OUT1="$(awsl iam get-policy --policy-arn "$POLICY_ARN" --query 'Policy.[Arn,PolicyId]' --output text)" \
  || fail "the policy does not exist at $POLICY_ARN after the apply"
ARN1="$(cut -f1 <<< "$OUT1")"
POLICY_ID1="$(cut -f2 <<< "$OUT1")"
[ "$ARN1" = "$POLICY_ARN" ] || fail "the live ARN is $ARN1, expected $POLICY_ARN"
log "  live: arn=$ARN1 policy_id=$POLICY_ID1"

# ── 4. choudoufu's own rendered identity is the ARN, not the PolicyId ──────
# aws_iam_policy.subject is a STABLE resource with nothing to change, so a
# plan against it prints no "from import identity" trace line at all - that
# trace only fires for an instance being destroyed or bound fresh under a
# replace (checked directly: repeated-module and create-over both rely on
# it for exactly that reason, and this fixture does not have either shape
# to offer it). What choudoufu actually WROTE as this instance's identity is
# the record apply itself produced, read here as a file and never through
# choudoufu - the same reason every other fixture on this branch reads its
# record by content rather than trusting a command's own report about it.
log "=== 4. the record: choudoufu's own rendered identity is the ARN, not the PolicyId ==="
find_record() {
  # Content-based lookup, not a recomputed RecordKey encoding - see
  # repeated-module's and create-over's run.sh for the same pattern and the
  # reason (issue #541): the encoding is an implementation detail this
  # script should not have to keep in sync with.
  grep -rlF "\"address\":\"$1\"" "$MAIN/.tofu-records" 2>/dev/null | head -1
}
APP_REC="$(find_record 'aws_iam_policy.subject')"
[ -n "$APP_REC" ] && [ -f "$APP_REC" ] \
  || fail "no record found for aws_iam_policy.subject under $MAIN/.tofu-records"
python3 -c '
import json,sys
p = json.load(open(sys.argv[1]))
ident = p.get("identity") or {}
assert ident.get("import_id") == sys.argv[2], "the record identity is %r, not the ARN %r" % (ident, sys.argv[2])
' "$APP_REC" "$ARN1" || fail "the record does not identify this instance by its ARN"
grep -qF "$POLICY_ID1" "$APP_REC" \
  && { cat "$APP_REC"; fail "the PolicyId leaked into the record as if it were part of the identity - it is not; the ARN alone is"; }
log "  the record identifies this instance by the ARN alone; PolicyId appears nowhere in it"

rm -f "$MAIN/terraform.tfstate" "$MAIN/terraform.tfstate.backup"
PLAN1="$(cd "$MAIN" && "$TOFU" live-plan -input=false -no-color 2>&1)" \
  || { printf '%s\n' "$PLAN1" | grep -E '^Error|^│' | head -20; fail "the cold replan exited non-zero"; }
grep -qE 'No changes|Plan: 0 to add, 0 to change, 0 to destroy' <<< "$PLAN1" \
  || { grep -E '^  # |^Plan:' <<< "$PLAN1" | head -20; fail "the cold replan is not empty"; }
log "  the cold replan is empty too"

# ── 5. destroy it, out from under nothing but this configuration ───────────
log "=== 5. destroy: remove the block, apply ==="
cat > "$MAIN/main.tf" <<'EOF'
terraform {
  required_version = ">= 1.5.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }

  live {
    estate = "deterministic-recreate-e2e"
  }
}

provider "aws" {
  region                      = "us-east-1"
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style           = true
}
EOF
rm -f "$MAIN/terraform.tfstate" "$MAIN/terraform.tfstate.backup"
APPLY2="$(cd "$MAIN" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY2" | grep -E '^Error|^│' | head -20; fail "the destroy apply failed"; }
grep -qE 'Apply complete! Resources: 0 added, 0 changed, 1 destroyed' <<< "$APPLY2" \
  || { grep -E 'Apply complete' <<< "$APPLY2"; fail "the destroy apply did not destroy exactly 1 resource"; }
awsl iam get-policy --policy-arn "$POLICY_ARN" >/dev/null 2>&1 \
  && fail "the policy still exists at $POLICY_ARN after the destroy"
log "  destroyed: $(grep -E 'Apply complete' <<< "$APPLY2")"

# ── 6. recreate it, identical name and path ─────────────────────────────────
log "=== 6. recreate: the block comes back exactly as it was ==="
cp "$FIXTURE/main.tf" "$MAIN/main.tf"
rm -f "$MAIN/terraform.tfstate" "$MAIN/terraform.tfstate.backup"
APPLY3="$(cd "$MAIN" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY3" | grep -E '^Error|^│' | head -20; fail "the recreate apply failed"; }
grep -qE 'Apply complete! Resources: 1 added' <<< "$APPLY3" \
  || { grep -E 'Apply complete' <<< "$APPLY3"; fail "the recreate apply did not create exactly 1 resource"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY3")"

OUT2="$(awsl iam get-policy --policy-arn "$POLICY_ARN" --query 'Policy.[Arn,PolicyId]' --output text)" \
  || fail "the policy does not exist at $POLICY_ARN after the recreate"
ARN2="$(cut -f1 <<< "$OUT2")"
POLICY_ID2="$(cut -f2 <<< "$OUT2")"
log "  live: arn=$ARN2 policy_id=$POLICY_ID2"

# ── 7. THE VALUE: the deterministic half survived, the server-minted half did not ──
log "=== 7. the distinction PR #500 got backwards ==="
[ "$ARN2" = "$ARN1" ] \
  || fail "the ARN moved across a destroy-and-recreate ($ARN1 -> $ARN2). The whole premise of this fixture - that an IAM policy's identity is deterministic from account+name+path - is false on this build; read internal/live/identity/table_generated.go's aws_iam_policy row."
[ "$POLICY_ID2" != "$POLICY_ID1" ] \
  || fail "PolicyId did NOT change across a destroy-and-recreate ($POLICY_ID1). Either the recreate did not really happen (a no-op apply reporting success it should not), or the emulator is not mimicking real IAM - see step 2's premise check, which passed against this same emulator moments ago."
log "  same ARN ($ARN1), different PolicyId ($POLICY_ID1 -> $POLICY_ID2): deterministic identity, server-minted discriminator"

APP_REC2="$(find_record 'aws_iam_policy.subject')"
[ -n "$APP_REC2" ] && [ -f "$APP_REC2" ] \
  || fail "no record found for aws_iam_policy.subject after the recreate"
python3 -c '
import json,sys
p = json.load(open(sys.argv[1]))
ident = p.get("identity") or {}
assert ident.get("import_id") == sys.argv[2], "the post-recreate record identity is %r, not the ARN %r" % (ident, sys.argv[2])
' "$APP_REC2" "$ARN2" || fail "the post-recreate record does not identify this instance by its (unchanged) ARN"
grep -qF "$POLICY_ID2" "$APP_REC2" \
  && { cat "$APP_REC2"; fail "the new PolicyId leaked into the post-recreate record"; }
log "  the post-recreate record identifies this instance by the same ARN; the new PolicyId appears nowhere in it"

# ── 8. cold replan after the recreate: still empty, still the ARN ──────────
log "=== 8. cold replan after the recreate: empty, and still bound by the ARN ==="
rm -f "$MAIN/terraform.tfstate" "$MAIN/terraform.tfstate.backup"
PLAN2="$(cd "$MAIN" && "$TOFU" live-plan -input=false -no-color 2>&1)" \
  || { printf '%s\n' "$PLAN2" | grep -E '^Error|^│' | head -20; fail "the post-recreate cold replan exited non-zero"; }
grep -qE 'No changes|Plan: 0 to add, 0 to change, 0 to destroy' <<< "$PLAN2" \
  || { grep -E '^  # |^Plan:' <<< "$PLAN2" | head -20; fail "the post-recreate cold replan is not empty - with no state file, choudoufu has to rebind by the ARN alone, and did not"; }
log "  No changes: the recreated policy rebinds from nothing but its own deterministic ARN"

# ── 9. the control: a wrong identity does not converge quietly ─────────────
# Run every time, not only under a BREAK flag, the same rule provisioner-taint
# and repeated-module hold their own controls to: it costs one plan, and a
# fixture whose corruption path is never actually exercised is not evidence
# that step 4's and step 8's checks can fail. Point the record at an ARN
# that names nothing live and confirm the next cold replan notices - the
# same "corrupt the record, not the config" shape create-over's step 7 uses,
# for the same reason: the CONFIG here is already correct (the policy's name
# and path are literals), so only the record can be made to lie.
log "=== 9. control: a record pointed at a nonexistent ARN must not converge ==="
cp "$APP_REC2" "$WORK/app-record.bak"
BOGUS_ARN="arn:aws:iam::000000000000:policy/this-policy-does-not-exist-$$"
python3 -c '
import json,sys
p = json.load(open(sys.argv[1]))
p["identity"]["import_id"] = sys.argv[2]
json.dump(p, open(sys.argv[1], "w"))
' "$APP_REC2" "$BOGUS_ARN" || fail "could not corrupt the record for the control"
rm -f "$MAIN/terraform.tfstate" "$MAIN/terraform.tfstate.backup"
set +e
PLAN3="$(cd "$MAIN" && "$TOFU" live-plan -input=false -no-color 2>&1)"
PLAN3_RC=$?
set -e
cp "$WORK/app-record.bak" "$APP_REC2"
if [ "$PLAN3_RC" = "0" ] && grep -qF 'No changes.' <<< "$PLAN3"; then
  fail "the plan converged even with the record pointed at an ARN that names nothing live. Step 4 and step 8's empty-plan checks are not measuring anything - a wrong identity would converge silently."
fi
log "  a record naming a nonexistent ARN does not converge quietly - the record is restored, and the checks above are load-bearing"

log ""
log "=== PASS ==="
log ""
log "One aws_iam_policy, destroyed and recreated: the ARN this instance is"
log "identified by came back byte-for-byte identical both times, while its"
log "PolicyId - server-minted, read straight off the emulator with no tofu"
log "in the loop - was different every single time, premise check included."
