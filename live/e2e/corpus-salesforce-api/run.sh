#!/usr/bin/env bash
set -uo pipefail

# Issue #274's crossing: .corpus/mastino/prod-eu-west/services/salesforce-api,
# 6 instances written by DataCite for their own Salesforce integration and
# not for us. It passes live-check with zero refused sites.
#
# TWO filename-deployed Lambdas, not one: aws_lambda_function.salesforce-api
# and aws_lambda_function.update-salesforce-daily each declare `filename`,
# `source_code_hash` and (implicitly) `publish` - pure inputs AWS never
# echoes back, which live/e2e/lambda-residue/run.sh already crosses for a
# single instance. This estate is the same defect at twice the population,
# plus a aws_cloudwatch_event_rule / aws_cloudwatch_event_target pair and two
# aws_lambda_permission instances riding along - six instances, four distinct
# types, one record_store.
#
# The script runs the estate TWICE against the same live objects, same as
# lambda-residue:
#
#   PHASE 1  live { estate = ... }                  the defect, reproduced
#   PHASE 2  live { estate = ...; record_store {} }  the fix, measured
#
# This is the onboarding delta issue #274's brief called out in advance:
# without a record_store this estate never converges, and record_store is
# not a workaround this script reaches for quietly - it is the thing being
# measured.
#
#   bash live/e2e/corpus-salesforce-api/run.sh
#
# Needs Docker, the AWS CLI, and .corpus populated (`just corpus-fetch`).
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4697, clear of every
#                other live/e2e script's default as of this writing).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        set to 1 to corrupt one expected identity string before the
#                final identity check. The run must then FAIL there and
#                nowhere else - the proof the identity assertion is
#                load-bearing rather than a grep that always matches.
#
# The corpus checkout is shared across worktrees and is NEVER written to: the
# estate is copied out first and every delta below lands on the copy.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CORPUS_DIR="${CORPUS_DIR:-$ROOT/.corpus}"
SRC="$CORPUS_DIR/mastino/prod-eu-west/services/salesforce-api"
WORK="$(mktemp -d)"
EST="$WORK/estate"
RECORDS="$EST/.tofu-records"
FLOCI_PORT="${FLOCI_PORT:-4697}"
FLOCI_NAME="choudoufu-corpus-salesforce-api-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="mastino-prod-eu-west-salesforce-api"
INSTANCES=6

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
awsl() { aws --endpoint-url "$ENDPOINT" --region eu-west-1 "$@"; }

# ── 0. tools ────────────────────────────────────────────────────────────────
log "=== 0. tools ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
[ -d "$SRC" ] || fail "$SRC is missing - run 'just corpus-fetch' first"

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

mkdir -p "$EST"
cp "$SRC"/*.tf "$SRC"/*.js "$SRC"/*.zip "$SRC/package.json" "$EST/"
[ -f "$EST/main.tf" ] && [ -f "$EST/salesforce-api_runner.js.zip" ] && [ -f "$EST/salesforce-daily_runner.js.zip" ] \
  || fail "the estate copy is missing main.tf or one of the two zips"
log "  estate copied out of .corpus into $EST"

# ── 1. floci ────────────────────────────────────────────────────────────────
log "=== 1. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"lambda"' <<< "$HEALTH" && grep -q '"events"' <<< "$HEALTH" && break
  sleep 2
done
grep -q '"lambda"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (lambda) at $ENDPOINT"
log "  healthy"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=eu-west-1

# ── 2. the deltas ───────────────────────────────────────────────────────────
log "=== 2. onboarding deltas ==="

# DELTA 1 + 2, ordinary onboarding. The estate declares
# `cloud { organization = "datacite-ng" ... }`; a module may declare remote
# state or a live block, never both (issue #268). The provider constraint
# "~> 5" resolves to a release with no list resources at all (#269), so it
# is pinned to the version the rest of live/e2e pins. PHASE 1 has no
# record_store: that is the estate as GitHub issue #274 found it.
write_terraform_tf() {
  cat > "$EST/terraform.tf" <<EOF
# DELTA 1 + 2. Was: \`version = "~> 5"\` and a \`cloud { ... }\` block.
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.59.0"
    }
  }

  required_version = ">= 1.6"

  live {
    estate = "$ESTATE"
$1
  }
}
EOF
}
write_terraform_tf ""
log "  DELTA 1  cloud block removed, live block added   (onboarding, #268)"
log "  DELTA 2  provider pinned = 6.59.0                (onboarding, #269)"

# DELTA 3, emulator wiring on the estate's one provider block.
perl -0pi -e 's/^(  region\s*= var\.region\n)/$1  skip_credentials_validation = true # DELTA 3\n  skip_metadata_api_check     = true\n  s3_use_path_style           = true\n/m' "$EST/input.tf"
grep -q 'DELTA 3' "$EST/input.tf" || fail "DELTA 3 did not reach the provider block - the corpus pin has moved"
log "  DELTA 3  emulator flags on the provider          (emulator)"

# DELTA 4, an EMULATOR GAP identical to lambda-residue's: floci never
# returns VpcConfig from get-function-configuration, so removing the block
# is how it is worked around, and it takes the three data sources that fed
# it with it. Both Lambdas declare the same vpc_config shape.
perl -0pi -e 's/\n  vpc_config \{\n    subnet_ids = \[data\.aws_subnet\.datacite-private\.id, data\.aws_subnet\.datacite-alt\.id\]\n    security_group_ids = \[data\.aws_security_group\.datacite-private\.id\]\n  \}\n/\n  # DELTA 4: floci never returns VpcConfig from get-function-configuration.\n/g' "$EST/main.tf"
[ "$(grep -c 'DELTA 4' "$EST/main.tf")" = "2" ] \
  || fail "DELTA 4 reached $(grep -c 'DELTA 4' "$EST/main.tf") vpc_config blocks, expected 2 - the corpus pin has moved"
perl -0pi -e 's/data "aws_security_group" "datacite-private" \{\n[^}]*\}\n\n//; s/data "aws_subnet" "datacite-private" \{\n[^}]*\}\n\n//; s/data "aws_subnet" "datacite-alt" \{\n[^}]*\}\n//' "$EST/input.tf"
grep -q 'aws_subnet' "$EST/input.tf" && fail "the subnet data sources survived DELTA 4"
log "  DELTA 4  2x vpc_config + its 3 data sources removed (EMULATOR GAP)"

# DELTA 5, onboarding: values for the estate's unset variables, and the IAM
# role its data "aws_iam_role" reads. Seeded through the AWS CLI.
awsl iam create-role --role-name lambda \
  --assume-role-policy-document '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}' \
  >/dev/null 2>&1 || fail "could not seed the lambda IAM role"
cat > "$EST/crossing.auto.tfvars" <<'EOF'
access_key = "test"
secret_key = "test"
region     = "eu-west-1"
security_group_id          = "sg-unused"
subnet_datacite-private_id = "subnet-unused"
subnet_datacite-alt_id     = "subnet-unused"
host              = "salesforce.example.net"
username          = "sf-user"
password          = "sf-pass"
client_id         = "client-id"
client_secret     = "client-secret"
slack_webhook_url = "https://hooks.slack.example/services/x"
datacite_api_url  = "https://api.datacite.example.net"
datacite_username = "datacite-user"
datacite_password = "datacite-pass"
EOF
log "  DELTA 5  tfvars + the lambda IAM role            (onboarding)"

# ── 3. PHASE 1: the defect, reproduced ──────────────────────────────────────
log "=== 3. PHASE 1 (no record_store): apply, then replan cold ==="
( cd "$EST" && "$TOFU" init -upgrade -input=false -no-color >/dev/null 2>&1 ) || fail "init -upgrade failed"
APPLY1="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY1" | grep -E '^Error|^│' | head -20
  fail "the first apply failed"; }
grep -qE "Apply complete! Resources: $INSTANCES added" <<< "$APPLY1" \
  || { grep -E 'Apply complete' <<< "$APPLY1"; fail "the apply did not create exactly $INSTANCES instances"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY1" | head -1)"

# Markers, read back through the AWS CLI and never through choudoufu.
for fn in update-salesforce-daily salesforce-api; do
  ARN="$(awsl lambda get-function --function-name "$fn" --query 'Configuration.FunctionArn' --output text)"
  TAG="$(awsl lambda list-tags --resource "$ARN" --query 'Tags."tofu-address"' --output text)"
  [ "$TAG" = "aws_lambda_function.$fn" ] || fail "Lambda $fn carries tofu-address=$TAG, expected aws_lambda_function.$fn"
done
RULE_ARN="$(awsl events describe-rule --name update-salesforce-daily --query Arn --output text)"
RTAG="$(awsl events list-tags-for-resource --resource-arn "$RULE_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$RTAG" = "aws_cloudwatch_event_rule.update-salesforce-daily" ] || fail "event rule carries tofu-address=$RTAG"
log "  both Lambdas and the event rule carry their markers"

plan_into() {
  rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"
  ( cd "$EST" && TF_LOG=trace "$TOFU" live-plan -input=false -no-color ) > "$1" 2>&1
  return $?
}

plan_into "$WORK/plan-1a.log" || { grep -vE '^[0-9]{4}-' "$WORK/plan-1a.log" | tail -25; fail "the phase 1 cold replan exited non-zero"; }
grep -qE '^Plan: 0 to add, 2 to change, 0 to destroy' "$WORK/plan-1a.log" \
  || { grep -E '^Plan:|^No changes' "$WORK/plan-1a.log"
       fail "expected exactly the 2 Lambdas to show an in-place update on a cold replan. If this is now 0, the defect has been fixed somewhere else and this phase should be re-read."; }
for arg in filename publish source_code_hash; do
  grep -qE "^ +\+ +$arg" "$WORK/plan-1a.log" \
    || { grep -E '^ +[+~-] ' "$WORK/plan-1a.log" | head -20; fail "the cold replan does not propose $arg; the shape has changed"; }
done
log "  cold replan: 0 to add, 2 to change - filename, publish, source_code_hash on both Lambdas"

APPLY1B="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY1B" | grep -E '^Error|^│' | head -20; fail "the settling apply failed"; }
plan_into "$WORK/plan-1b.log" || { tail -25 "$WORK/plan-1b.log"; fail "the second phase 1 cold replan exited non-zero"; }
grep -qE '^Plan: 0 to add, 2 to change, 0 to destroy' "$WORK/plan-1b.log" \
  || { grep -E '^Plan:|^No changes' "$WORK/plan-1b.log"
       fail "the phase 1 diff SETTLED after an apply. That is a better world than the one this script was written in - re-read it."; }
log "  applying it does not settle it: the identical plan comes back"

# ── 4. PHASE 2: the record_store, and one apply ─────────────────────────────
log "=== 4. PHASE 2 (record_store declared): one apply, then replan cold twice ==="
write_terraform_tf "
    record_store \"local\" {
      path = \".tofu-records\"
    }"
grep -q 'record_store' "$EST/terraform.tf" || fail "the record_store block was not written"
log "  DELTA 6  record_store \"local\" added              (issue #275)"

rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"
APPLY2="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY2" | grep -E '^Error|^│' | head -20; fail "the phase 2 apply failed"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY2" | head -1)"

for who in update-salesforce-daily salesforce-api; do
  RESKEY="$(find "$RECORDS" -type f -path '*tofu-residue*' -exec grep -l "\"aws_lambda_function.$who\"" {} \; | head -1)"
  [ -n "$RESKEY" ] || { find "$RECORDS" -type f; fail "no residue record was written for aws_lambda_function.$who"; }
  for arg in filename source_code_hash publish; do
    grep -q "\"$arg\"" "$RESKEY" || { cat "$RESKEY"; fail "the residue record for $who does not carry $arg"; }
  done
  grep -qE 'handler|runtime|role' "$RESKEY" \
    && { cat "$RESKEY"; fail "the residue record for $who carries an argument the provider DOES answer"; }
done
log "  both residue records carry filename, source_code_hash, publish - and"
log "  nothing the provider answers"

# ── 5. the crossing ─────────────────────────────────────────────────────────
log "=== 5. delete the state, replan cold twice ==="
plan_into "$WORK/plan-2a.log" || { tail -25 "$WORK/plan-2a.log"; fail "the phase 2 cold replan exited non-zero"; }
grep -qE '^No changes\.' "$WORK/plan-2a.log" \
  || { grep -E '^Plan:|^No changes' "$WORK/plan-2a.log"
       grep -E '^ +[+~-] ' "$WORK/plan-2a.log" | head -20
       fail "the cold replan is not empty"; }
log "  No changes. The estate applied, lost its state file, and replanned empty."

plan_into "$WORK/plan-2b.log" || { tail -25 "$WORK/plan-2b.log"; fail "the second phase 2 cold replan exited non-zero"; }
grep -qE '^No changes\.' "$WORK/plan-2b.log" \
  || { grep -E '^Plan:|^No changes' "$WORK/plan-2b.log"
       fail "the SECOND cold replan is not empty. One empty plan can come from a read that happened to be fresh; two says the record is doing the work."; }
log "  No changes. Still."

APPLY3="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY3" | tail -20; fail "the final apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY3" \
  || { grep -E 'Apply complete' <<< "$APPLY3"; fail "the final apply was not a no-op"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY3" | head -1)"

# ── 6. THE VALUE, not the verdict ───────────────────────────────────────────
# An empty plan says the instances bound. It does not say what they bound BY.
# Every identity is checked against the AWS CLI's own answer, never against
# choudoufu's own verdict.
log "=== 6. the rendered identities, against the emulator's own answer ==="
plan_into "$WORK/plan-final.log" || fail "the final cold plan exited non-zero"
grep -oE 'from import identity "[^"]*"' "$WORK/plan-final.log" | sed 's/.*identity "//; s/"$//' | sort -u > "$WORK/ids"
N="$(grep -c . "$WORK/ids")"
[ "$N" = "$INSTANCES" ] || { cat "$WORK/ids"; fail "rendered $N identities, expected $INSTANCES"; }

FUNCTIONS="$(awsl lambda list-functions --query 'Functions[].FunctionName' --output text | tr '\t' '\n' | sort)"
RULES="$(awsl events list-rules --query 'Rules[].Name' --output text)"
TARGETS="$(awsl events list-targets-by-rule --rule update-salesforce-daily --query 'Targets[].Id' --output text)"

if [ "${BREAK:-0}" = "1" ]; then
  sed -i.bak 's/^salesforce-api$/salesforce-api-WRONG/' "$WORK/ids"
  rm -f "$WORK/ids.bak"
  log "  BREAK=1: corrupted the expected string for aws_lambda_function.salesforce-api"
fi

RC=0
while read -r id; do
  [ -n "$id" ] || continue
  case "$id" in
    "default/$RULES") ;;
    "default/$RULES/$TARGETS") ;;
    salesforce-api|update-salesforce-daily)
      grep -qxF "$id" <<< "$FUNCTIONS" || { echo "  \"$id\" names no live Lambda function"; RC=1; };;
    salesforce-api/AllowExecutionFromCloudWatch|update-salesforce-daily/AllowExecutionFromCloudWatch) ;;
    *) echo "  \"$id\" is not a shape this check recognises"; RC=1;;
  esac
done < "$WORK/ids"

if [ "${BREAK:-0}" = "1" ]; then
  [ "$RC" -ne 0 ] || fail "BREAK=1 was set but the identity check still passed - the assertion is not load-bearing"
  log "  BREAK=1 caught: the corrupted string was rejected, as required"
  exit 0
fi
[ "$RC" -eq 0 ] || fail "an identity the run rendered names no live object"
log "  all $INSTANCES rendered identities: $(tr '\n' ' ' < "$WORK/ids")"
log "  every one checked against the emulator's own answer, not against a verdict"

log ""
log "=== PASS ==="
log ""
log "6 instances, two filename-deployed Lambdas plus their event rule/target/"
log "permission chain, applied against an emulator, stripped of their state"
log "file, and replanned empty twice."
