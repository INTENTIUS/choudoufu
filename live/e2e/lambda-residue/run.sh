#!/usr/bin/env bash
set -uo pipefail

# GitHub issue #275's crossing: a filename-deployed Lambda applied against a
# real emulator, its state file deleted, and replanned cold - twice.
#
# .corpus/mastino/prod-eu-west/services/check-links is four instances, one of
# them an aws_lambda_function deployed from a local zip. It passes live-check
# with zero refused sites, applies cleanly, and then - before this ran -
# proposed the identical update on every cold replan, forever:
#
#     ~ resource "aws_lambda_function" "check-links" {
#         + filename          = "check_links.py.zip"
#         + publish           = false
#         + source_code_hash  = "82e750d3..."
#       }
#     Plan: 0 to add, 1 to change
#
# Applying it does not settle it. Those three arguments are pure inputs: AWS
# never knew the local path a zip came from, so no read recovers them, and
# issue #73 deletes the state file that stock OpenTofu remembers them in.
#
# The script runs the estate TWICE against the same live objects, and the
# difference between the two runs is one block:
#
#   PHASE 1  live { estate = ... }                  the defect, reproduced
#   PHASE 2  live { estate = ...; record_store {} } the fix, measured
#
# Phase 2's apply classifies each argument by putting the object to the
# provider twice, with priors that differ in exactly the arguments under
# test, and records only what the provider proves it does not manage. Both
# priors are legitimate - the applied object, and the applied object with
# some optional attributes null - so no bogus value is ever handed to a
# provider. See internal/live/projection/residue.go.
#
#   bash live/e2e/lambda-residue/run.sh
#
# Needs Docker, the AWS CLI, and .corpus populated (`just corpus-fetch`).
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4606, clear of run.sh's
#                4566, dataread-projection's 4599, tagging-sweep's 4601,
#                create-over's 4602, per-element's 4604 and
#                corpus-crossing's 4605).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#
# The corpus checkout is shared across worktrees and is NEVER written to: the
# estate is copied out first and every delta below lands on the copy.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# CORPUS_DIR exists because .corpus is populated in the main checkout and a
# git worktree does not have one. It is READ from and never written to,
# which is why pointing it at a shared checkout is safe.
CORPUS_DIR="${CORPUS_DIR:-$ROOT/.corpus}"
SRC="$CORPUS_DIR/mastino/prod-eu-west/services/check-links"
WORK="$(mktemp -d)"
EST="$WORK/estate"
RECORDS="$EST/.tofu-records"
FLOCI_PORT="${FLOCI_PORT:-4606}"
FLOCI_NAME="choudoufu-lambda-residue-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="mastino-prod-eu-west-check-links"
INSTANCES=4

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
cp "$SRC"/*.tf "$SRC"/check_links.py.zip "$EST/"
[ -f "$EST/main.tf" ] && [ -f "$EST/check_links.py.zip" ] || fail "the estate copy is missing main.tf or the zip"
log "  estate copied out of .corpus into $EST"

# ── 1. floci ────────────────────────────────────────────────────────────────
log "=== 1. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"lambda"' <<< "$HEALTH" && break
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
# state or a live block, never both. The provider constraint "~> 5" resolves
# to a release with no list resources at all (#269), so it is pinned to the
# version the rest of live/e2e pins. PHASE 1 has no record_store: that is
# the estate as issue #275 found it.
write_terraform_tf() {
  cat > "$EST/terraform.tf" <<EOF
# DELTA 1 + 2. Was: \`version = "~> 5"\` and a \`cloud { ... }\` block.
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
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
log "  DELTA 1  cloud block removed, live block added   (onboarding)"
log "  DELTA 2  provider pinned = 6.58.0                (onboarding, #269)"

# DELTA 3, emulator wiring: the flags with no environment-variable form.
perl -0pi -e 's/^(  region\s*= var\.region\n)/$1  skip_credentials_validation = true # DELTA 3\n  skip_metadata_api_check     = true\n  s3_use_path_style           = true\n/mg' "$EST/input.tf"
grep -q 'DELTA 3' "$EST/input.tf" || fail "DELTA 3 did not reach the provider block - the corpus pin has moved"
log "  DELTA 3  emulator flags on the provider          (emulator)"

# DELTA 4, an EMULATOR GAP, and the reason this crossing measures the three
# top-level arguments and not the fourth thing the issue's plan showed.
#
# `aws lambda get-function-configuration` on floci never returns a VpcConfig
# key, even for a function created with one - verified directly with the AWS
# CLI against a function this script's own apply made. So the provider's read
# reports no VPC configuration at all and the plan proposes adding it back on
# every run, whatever choudoufu does. That is floci's gap, not the defect
# issue #275 is about: against real AWS the block reads back.
#
# Removing the block is how it is worked around, and it takes the three data
# sources that fed it with it - they exist only to supply subnet and security
# group IDs, and floci would need them seeded for a read that no longer
# happens.
perl -0pi -e 's/\n  vpc_config \{\n[^}]*\n  \}\n/\n  # DELTA 4: floci never returns VpcConfig from get-function-configuration.\n/' "$EST/main.tf"
grep -q 'DELTA 4' "$EST/main.tf" || fail "DELTA 4 did not match the vpc_config block - the corpus pin has moved"
grep -q 'vpc_config' "$EST/main.tf" && fail "DELTA 4 left a vpc_config block behind"
perl -0pi -e 's/data "aws_security_group" "datacite-private" \{\n[^}]*\}\n//; s/data "aws_subnet" "datacite-private" \{\n[^}]*\}\n//; s/data "aws_subnet" "datacite-alt" \{\n[^}]*\}\n//' "$EST/input.tf"
grep -q 'aws_subnet' "$EST/input.tf" && fail "the subnet data sources survived DELTA 4"
log "  DELTA 4  vpc_config + its 3 data sources removed (EMULATOR GAP)"

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
api_endpoint   = "https://api.example.org"
redis_host     = "redis.example.org"
start_urls_key = "start-urls"
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

# The marker, read back through the AWS CLI and never through choudoufu.
TAG="$(awsl lambda list-tags --resource "$(awsl lambda get-function --function-name check-links --query 'Configuration.FunctionArn' --output text)" --query 'Tags."tofu-address"' --output text)"
[ "$TAG" = "aws_lambda_function.check-links" ] \
  || fail "the Lambda carries tofu-address=$TAG, expected aws_lambda_function.check-links"
log "  the Lambda carries its marker: tofu-address=$TAG"

plan_into() {
  rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"
  ( cd "$EST" && "$TOFU" plan -input=false -no-color ) > "$1" 2>&1
  local rc=$?
  if [ -n "${KEEP_LOGS:-}" ]; then
    mkdir -p "$KEEP_LOGS"
    cp "$1" "$KEEP_LOGS/$(basename "$1")"
  fi
  return $rc
}

plan_into "$WORK/plan-1a.log" || { tail -25 "$WORK/plan-1a.log"; fail "the phase 1 cold replan exited non-zero"; }
grep -qE '^Plan: 0 to add, 1 to change, 0 to destroy' "$WORK/plan-1a.log" \
  || { grep -E '^Plan:|^No changes' "$WORK/plan-1a.log"
       fail "expected exactly the one Lambda to show an in-place update on a cold replan. If this is now 0, the defect issue #275 describes has been fixed somewhere else and this phase should be re-read."; }
for arg in filename publish source_code_hash; do
  grep -qE "^ +\+ +$arg" "$WORK/plan-1a.log" \
    || { grep -E '^ +[+~-] ' "$WORK/plan-1a.log" | head -20; fail "the cold replan does not propose $arg; the shape has changed"; }
done
log "  cold replan: 0 to add, 1 to change - filename, publish, source_code_hash"

# And applying it does not settle it, which is what makes it never converge
# rather than merely cost one extra apply.
APPLY1B="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY1B" | grep -E '^Error|^│' | head -20; fail "the settling apply failed"; }
plan_into "$WORK/plan-1b.log" || { tail -25 "$WORK/plan-1b.log"; fail "the second phase 1 cold replan exited non-zero"; }
grep -qE '^Plan: 0 to add, 1 to change, 0 to destroy' "$WORK/plan-1b.log" \
  || { grep -E '^Plan:|^No changes' "$WORK/plan-1b.log"
       fail "the phase 1 diff SETTLED after an apply. That is a better world than the one this script was written in - re-read it."; }
log "  applying it does not settle it: the identical plan comes back"

# ── 4. PHASE 2: the record_store, and one apply ─────────────────────────────
# The ONE edit between the two phases. The store is a directory beside the
# state file, not the state file: step 5 deletes terraform.tfstate by name
# and step 4's own assertion reads the record back off disk afterwards, so
# there is no question of the two being the same thing. A record_store path
# has to stay inside the module directory - internal/configs refuses an
# absolute one - which is why it is not somewhere more obviously separate.
log "=== 4. PHASE 2 (record_store declared): one apply, then replan cold twice ==="
write_terraform_tf "
    record_store \"local\" {
      path = \".tofu-records\"
    }"
grep -q 'record_store' "$EST/terraform.tf" || fail "the record_store block was not written"
log "  DELTA 6  record_store \"local\" added              (issue #275)"

rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"
# KEEP_LOGS, when set to a directory, copies the classifier's own trace out
# for reading. The classifier logs one line per candidate at TRACE naming
# what each of the two reads answered, which is the only view of WHY an
# argument was or was not recorded.
APPLY2="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY2" | grep -E '^Error|^│' | head -20; fail "the phase 2 apply failed"; }
if [ -n "${KEEP_LOGS:-}" ]; then
  mkdir -p "$KEEP_LOGS"
  printf '%s\n' "$APPLY2" > "$KEEP_LOGS/apply2.log"
fi
log "  $(grep -E 'Apply complete' <<< "$APPLY2" | head -1)"

# What landed in the store, read as a file and not through choudoufu.
# GitHub issue #364 unit A1 collapsed the once-separate "tofu-residue" root
# into the single per-instance envelope every record now lives in
# (internal/live/projection/record.go's RecordKeyPrefix); it is the
# envelope's own "kind" field, not which literal a key starts with, that
# now keeps a residue-carrying key out of the listing which proposes
# destroying undeclared records - see record.go's recordKindIdentity.
RESKEY="$(find "$RECORDS" -type f -path '*tofu-records*' -exec grep -l '"residue":{' {} \; | head -1)"
[ -n "$RESKEY" ] || { find "$RECORDS" -type f | head -20; fail "no record carrying a residue member was written under tofu-records/"; }
log "  residue recorded at ${RESKEY#$RECORDS/}"
for arg in filename source_code_hash publish; do
  grep -q "\"$arg\"" "$RESKEY" || { cat "$RESKEY"; fail "the residue record does not carry $arg"; }
done
grep -q '"description"' "$RESKEY" \
  && { cat "$RESKEY"; fail "the residue record carries description, which the provider DOES return. A record that duplicates a value the cloud answers is a second opinion, and the plan would go empty over real drift."; }
grep -qE 'sentinel|tofu-live-probe' "$RESKEY" \
  && { cat "$RESKEY"; fail "a probe value reached the record"; }
log "  it carries filename, source_code_hash, publish - and NOT description"

# ── 5. the crossing ─────────────────────────────────────────────────────────
log "=== 5. delete the state, replan cold ==="
plan_into "$WORK/plan-2a.log" || { tail -25 "$WORK/plan-2a.log"; fail "the phase 2 cold replan exited non-zero"; }
grep -qE '^No changes\.' "$WORK/plan-2a.log" \
  || { grep -E '^Plan:|^No changes' "$WORK/plan-2a.log"
       grep -E '^ +[+~-] ' "$WORK/plan-2a.log" | head -20
       fail "the cold replan is not empty"; }
grep -qE 'sentinel|tofu-live-probe' "$WORK/plan-2a.log" \
  && fail "a probe value reached the plan output"
log "  No changes. The estate applied, lost its state file, and replanned empty."

log "=== 6. delete the state AGAIN, replan cold again ==="
plan_into "$WORK/plan-2b.log" || { tail -25 "$WORK/plan-2b.log"; fail "the second phase 2 cold replan exited non-zero"; }
grep -qE '^No changes\.' "$WORK/plan-2b.log" \
  || { grep -E '^Plan:|^No changes' "$WORK/plan-2b.log"
       fail "the SECOND cold replan is not empty. One empty plan can come from a read that happened to be fresh; two says the record is doing the work."; }
log "  No changes. Still."

log ""
log "PASS: $INSTANCES instances, one filename-deployed Lambda."
log "  phase 1, no record_store:  1 to change, and applying it does not settle it"
log "  phase 2, record_store:     one apply, then empty and empty again"
