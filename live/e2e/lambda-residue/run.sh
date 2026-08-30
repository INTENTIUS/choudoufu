#!/usr/bin/env bash
set -uo pipefail

# GitHub issue #275's crossing: a filename-deployed Lambda applied against a
# real emulator, its state file deleted, and replanned cold - twice.
#
# .corpus/mastino/prod-eu-west/services/check-links is four instances, one of
# them an aws_lambda_function deployed from a local zip. It passes live-check
# with zero refused sites, applies cleanly, and then - before issue #275's fix
# - proposed the identical update on every cold replan, forever:
#
#     ~ resource "aws_lambda_function" "check-links" {
#         + filename          = "check_links.py.zip"
#         + publish           = false
#         + source_code_hash  = "82e750d3..."
#       }
#     Plan: 0 to add, 1 to change
#
# Applying it did not settle it. Those three arguments are pure inputs: AWS
# never knew the local path a zip came from, so no read recovers them, and
# issue #73 deletes the state file that stock OpenTofu remembers them in.
#
# The fix classifies each argument by putting the object to the provider
# twice, with priors that differ in exactly the arguments under test, and
# records only what the provider proves it does not manage. Both priors are
# legitimate - the applied object, and the applied object with some optional
# attributes null - so no bogus value is ever handed to a provider. See
# internal/live/projection/residue.go.
#
# The script runs the estate TWICE against the same live objects. It used to
# be one live block with no record_store (reproducing the defect) followed by
# one that added `record_store {}` (measuring the fix) - two genuinely
# different configurations. They are not anymore: HANDOFF.md's "compatible
# out of the box" principle, and internal/configs/live.go's
# impliedRecordStore, mean a live block declaring no record_store gets the
# local backend anyway - "the same store, holding the same records under the
# same keys in the same directory" as one spelled out by hand
# (internal/live/lint's implied_record_store_test.go pins that by value).
# Issue #541 found this the hard way: PHASE 1 below used to reproduce the
# perpetual-churn plan above and no longer does - the cold replan comes back
# "No changes" - because the fix it was written to prove absent is now on by
# default. That is the fixture being stale against an improved product, not
# a regression: residue tracking moved from "declare a store or refuse" to
# "on unless you go out of your way", which is a strictly stronger claim than
# the one this script originally pinned.
#
#   PHASE 1  live { estate = ... }                   no block written: the
#                                                     IMPLIED local store
#   PHASE 2  live { estate = ...; record_store {} }  the same store, spelled
#                                                     out - proves the two
#                                                     configurations really
#                                                     are the one store this
#                                                     fixture's own estate
#                                                     depends on, not merely
#                                                     what a unit test says
#                                                     about a smaller case
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

# ── 3. PHASE 1: the implied default, measured ───────────────────────────────
log "=== 3. PHASE 1 (no record_store block: the implied local store): apply, then replan cold twice ==="
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

# residue_at finds the record carrying the residue for the Lambda, by
# content rather than by recomputing internal/live/projection/record.go's
# RecordKey encoding - the same reason repeated-module's and create-over's
# controls look records up this way (issue #541). Fails loudly if the
# implied store did not write one, which is what "the fix is on by default"
# actually has to mean: not merely an empty plan, which a lucky read could
# also produce.
residue_at() {
  local reskey
  reskey="$(find "$RECORDS" -type f -path '*tofu-records*' -exec grep -l '"residue":{' {} \; 2>/dev/null | head -1)"
  [ -n "$reskey" ] || { find "$RECORDS" -type f 2>/dev/null | head -20; fail "no record carrying a residue member was written under $RECORDS"; }
  printf '%s\n' "$reskey"
}

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

RESKEY1="$(residue_at)"
log "  residue recorded at ${RESKEY1#$RECORDS/} - the implied store wrote one with no record_store block in sight"
for arg in filename source_code_hash publish; do
  grep -q "\"$arg\"" "$RESKEY1" || { cat "$RESKEY1"; fail "the residue record does not carry $arg"; }
done
grep -q '"description"' "$RESKEY1" \
  && { cat "$RESKEY1"; fail "the residue record carries description, which the provider DOES return. A record that duplicates a value the cloud answers is a second opinion, and the plan would go empty over real drift."; }

plan_into "$WORK/plan-1a.log" || { tail -25 "$WORK/plan-1a.log"; fail "the phase 1 cold replan exited non-zero"; }
grep -qE '^No changes\.' "$WORK/plan-1a.log" \
  || { grep -E '^Plan:|^No changes' "$WORK/plan-1a.log"
       grep -E '^ +[+~-] ' "$WORK/plan-1a.log" | head -20
       fail "the phase 1 cold replan is not empty. Issue #275's defect (filename/publish/source_code_hash proposed forever) is back, and the implied local record store (internal/configs/live.go's impliedRecordStore) is not classifying them - read internal/live/projection/residue.go."; }
log "  No changes. The estate applied with no record_store block, lost its state file, and replanned empty - the implied store did the work."

plan_into "$WORK/plan-1b.log" || { tail -25 "$WORK/plan-1b.log"; fail "the second phase 1 cold replan exited non-zero"; }
grep -qE '^No changes\.' "$WORK/plan-1b.log" \
  || { grep -E '^Plan:|^No changes' "$WORK/plan-1b.log"
       fail "the SECOND phase 1 cold replan is not empty. One empty plan can come from a read that happened to be fresh; two says the implied store's record is doing the work."; }
log "  No changes. Still - twice is the record, not a lucky read."

# ── 4. PHASE 2: the same store, spelled out ─────────────────────────────────
# The ONE edit between the two phases: a record_store block naming exactly
# the path the implied store already resolves to (impliedRecordStore, "the
# 'local' backend with no path, which
# [internal/live/projection.NewRecordStore] resolves to a '.tofu-records'
# directory beside the module"). If phase 1 and phase 2 ever disagree, either
# that resolution changed or this fixture's premise that they are the same
# store did - the whole reason phase 1 above is not just "delete this phase"
# now that it converges on its own.
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
RESKEY="$(residue_at)"
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
log "  phase 1, no record_store block (implied local store): residue recorded, empty and empty again"
log "  phase 2, record_store spelled out explicitly:         the same store, the same result"
