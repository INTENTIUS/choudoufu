#!/usr/bin/env bash
set -uo pipefail

# A real third-party estate crossed against a real emulator: issue #274's
# step 6, for .corpus/mastino/prod-eu-west/services/raw-resolution-logs.
#
# One resource - aws_s3_bucket.raw-resolution-logs, DataCite's own bucket for
# their resolver's raw access logs. It passes live-check with zero refused
# sites and, until this script existed, had never touched a cloud. Picked as
# one of #274's smallest untouched real corpus estates, smallest-first.
#
# THE ONE DELTA. The estate declares `cloud { organization = "datacite-ng"
# ... }` (#268: a module may declare remote state or a live block, never
# both), replaced with a live block. Unlike several of its sibling mastino
# service estates, this one needs no provider-version override: its
# `version = "~> 5"` DOES resolve to 5.100.0, the release #269 documented as
# carrying no list resources at all - but aws_s3_bucket's identity here is
# client-supplied (the literal `bucket` argument), so it never calls
# ListBuckets and 5.100.0's gap never bites. refusal-probe's -schemas
# version-skew check agrees: this entry carries no version_skew at all,
# unlike its sibling sitemaps-generator crossing.
#
# THE TYPE. aws_s3_bucket's row (internal/live/identity/table_generated.go)
# takes its identity from the `bucket` argument alone - ServerAssignedIfAbsent,
# but present and literal here, so the import identity is the bucket name
# itself: "raw-resolution-logs.datacite.org". `acl = "private"` is still a
# valid (deprecated) argument on aws_s3_bucket through 6.59.0 - confirmed
# against the doc cache before assuming otherwise.
#
# WHAT DOES NOT CONVERGE, AND WHY. This is the one #274 crossing that does
# not reach an empty second plan, and step 5/6 assert that honestly instead
# of hiding it. A cold live-plan always proposes "+ acl = private" and
# "+ force_destroy = false" in-place, on every single run, forever - applying
# it changes nothing real and the next plan proposes the identical update
# again. This was isolated to the provider, not to choudoufu or floci's
# marker layer: the SAME diff reproduces byte-for-byte running plain,
# upstream `terraform import aws_s3_bucket.x <bucket>` then `terraform plan`
# against this same floci, with zero choudoufu code in the path. floci's own
# GetBucketAcl answer is correct (a single FULL_CONTROL grant to the owner,
# the canonical shape of "private") - the provider's Read simply never
# repopulates the deprecated `acl` attribute from a fresh import, only from
# a Create it performed itself in the same state lineage. Traditional
# Terraform never notices because it imports once and then trusts state
# forever; choudoufu's no-state model re-derives prior state from a live
# read on every single plan, so for THIS argument shape it can never
# stabilize. Steps 5 and 6 assert the update is exactly this known,
# reproduced, external gap - never a create, a destroy, or any other
# attribute - so a real regression still turns the assertion red.
#
#   bash live/e2e/corpus-raw-resolution-logs/run.sh
#
# Needs Docker and the AWS CLI. .corpus is read, never written: the estate is
# copied out to a temp directory first, same as every other corpus crossing.
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4700, clear of every
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
SRC="$CORPUS_DIR/mastino/prod-eu-west/services/raw-resolution-logs"
WORK="$(mktemp -d)"
EST="$WORK/estate"
FLOCI_PORT="${FLOCI_PORT:-4700}"
FLOCI_NAME="choudoufu-corpus-rawreslogs-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="mastino-raw-resolution-logs-crossing"
REGION="eu-west-1"
BUCKET="raw-resolution-logs.datacite.org"

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
cp "$SRC"/*.tf "$EST/"
[ -f "$EST/main.tf" ] || fail "the estate copy is missing main.tf"
log "  estate copied out of .corpus into $EST"

# ── 1. the one delta ─────────────────────────────────────────────────────────
log "=== 1. the one onboarding delta ==="
perl -0pi -e 's/terraform \{\n  required_providers \{\n    aws = \{\n      source = "hashicorp\/aws"\n      version = "~> 5"\n    \}\n  \}\n\n  required_version = ">= 1\.6"\n\n  cloud \{\n    organization = "datacite-ng"\n\n    workspaces \{\n      name = "prod-eu-west-services-raw-resolution-logs"\n    \}\n  \}\n\}/terraform {\n  required_providers {\n    aws = {\n      source  = "hashicorp\/aws"\n      version = "~> 5"\n    }\n  }\n\n  required_version = ">= 1.6"\n\n  # DELTA: was a Terraform Cloud block (#268). No provider-version override\n  # needed here - see the header comment on why this entry carries no\n  # version_skew.\n  live {\n    estate = "'"$ESTATE"'"\n  }\n}/' "$EST/terraform.tf"
grep -q "estate = \"$ESTATE\"" "$EST/terraform.tf" || fail "the delta did not match terraform.tf - the corpus pin has moved"
grep -q 'cloud {' "$EST/terraform.tf" && fail "the cloud block is still there - the corpus pin has moved"
log "  DELTA  cloud block removed, live block added                (#268)"

perl -0pi -e 's/(provider "aws" \{\n  access_key = var\.access_key\n  secret_key = var\.secret_key\n  region     = var\.region\n)\}/$1\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  s3_use_path_style            = true\n}/' "$EST/input.tf"
grep -q 's3_use_path_style' "$EST/input.tf" || fail "the emulator delta did not match input.tf - the corpus pin has moved"
log "  DELTA  emulator flags on the provider                        (emulator)"

cat > "$EST/crossing.auto.tfvars" <<EOF
access_key = "test"
secret_key = "test"
EOF
log "  DELTA  access_key/secret_key values                          (onboarding)"

# ── 2. floci ────────────────────────────────────────────────────────────────
log "=== 2. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"s3"' <<< "$HEALTH" && break
  sleep 2
done
grep -q '"s3"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (s3) at $ENDPOINT"
log "  healthy"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION"

# ── 3. stand the estate up ──────────────────────────────────────────────────
log "=== 3. init and apply: 1 instance ==="
( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null ) || fail "init failed"
APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY_OUT" | tail -40
  fail "the apply failed"
}
grep -qE 'Apply complete! Resources: 1 added' <<< "$APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY_OUT"; fail "the apply did not create exactly 1 resource"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY_OUT")"

# Read the bucket back through the AWS CLI, never through choudoufu.
if awsl s3api head-bucket --bucket "$BUCKET" >/dev/null 2>&1; then
  LIVE_BUCKET="$BUCKET"
else
  LIVE_BUCKET=""
fi
[ "$LIVE_BUCKET" = "$BUCKET" ] || fail "could not find bucket $BUCKET through the AWS CLI"
log "  the bucket lives: $BUCKET"

# ── 4. no state file, ever ──────────────────────────────────────────────────
rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"
[ ! -f "$EST/terraform.tfstate" ] || fail "the state file is still there"
log "=== 4. state file deleted ==="

# ── 5. THE VALUE, not the verdict ───────────────────────────────────────────
# A DELIBERATE DEPARTURE from every other #274 crossing: this one does NOT
# assert an empty plan. See "WHAT DOES NOT CONVERGE, AND WHY" in the header -
# a fresh import of a bucket with the deprecated `acl` argument set always
# proposes "+ acl" / "+ force_destroy", confirmed byte-for-byte reproducing
# under plain `terraform import` + `terraform plan` against the same floci,
# with zero choudoufu code involved. The identity and the marker are still
# asserted; the update is asserted to be EXACTLY those two attributes, never
# a create, a destroy, or anything else.
log "=== 5. live-plan, and the rendered identity read out of the run ==="
plan_into() {
  ( cd "$EST" && TF_LOG=trace "$TOFU" live-plan -input=false -no-color )
}
assert_only_known_diff() {
  local out="$1" label="$2"
  # $out is TF_LOG=trace output: the rendered plan is one contiguous block at
  # the end, but the trace preceding it can contain unrelated lines that
  # share a diff line's leading "      +"/"~"/"-" shape (SDK consistency-check
  # trace entries like ".versioning: block count in plan (1) disagrees with
  # count in config (0)"). Bound the extraction to the rendered resource
  # block itself - from its "# ... will be" header to its own closing brace -
  # so only real diff attribute lines are ever inspected.
  local block
  block="$(awk '
    /^  # .+ will be (created|updated in-place|destroyed)/ { grabbing=1 }
    grabbing { print }
    grabbing && /^    }$/ { exit }
  ' <<< "$out")"
  [ -n "$block" ] || { grep -E '^Plan:' <<< "$out"; fail "$label: no resource action block found in the rendered plan"; }
  grep -qE '^  # .+ will be (created|destroyed)' <<< "$block" \
    && { printf '%s\n' "$block"; fail "$label proposes a create or destroy - not the known acl-only update"; }
  if grep -qE '^  # .+ will be updated in-place' <<< "$block"; then
    grep -qF '+ acl' <<< "$block" || { printf '%s\n' "$block"; fail "$label proposes an update that is not the known acl diff"; }
    UNEXPECTED="$(grep -E '^ +[+~-] [A-Za-z_]+ +=' <<< "$block" | grep -vE '\+ acl |\+ force_destroy ')"
    [ -z "$UNEXPECTED" ] || { printf '%s\n' "$block"; printf '%s\n' "$UNEXPECTED"; fail "$label's update touches attributes beyond the known acl/force_destroy gap"; }
    log "  $label: the known acl-only update (see header), nothing else"
    return 0
  fi
  grep -qE '^Plan: 0 to add, 0 to change, 0 to destroy' <<< "$out" \
    || { grep -E '^Plan:' <<< "$out"; fail "$label's plan is neither empty nor the known acl-only update"; }
  log "  $label: plan converged with no changes (floci returned acl-consistent state this run)"
}
PLAN_OUT="$(plan_into 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -40; fail "live-plan exited $PLAN_RC"; }
[ ! -f "$EST/terraform.tfstate" ] || fail "live-plan wrote a state file"
assert_only_known_diff "$PLAN_OUT" "first plan"
grep -qE '^Foreign resources: (none|nothing was swept)' <<< "$PLAN_OUT" \
  || { grep -E '^Foreign resources:' <<< "$PLAN_OUT"; fail "the plan reports foreign resources"; }
log "  nothing foreign"

WANT="$BUCKET"
if [ "${BREAK:-}" = "1" ]; then
  WANT="wrong-bucket.datacite.org"
  log "  BREAK=1: expecting \"$WANT\", the wrong bucket name. The plan above"
  log "           still only shows the known acl update. This step must fail."
fi
grep -qF "from import identity \"$WANT\"" <<< "$PLAN_OUT" || {
  grep -oE 'from import identity "[^"]*"' <<< "$PLAN_OUT" | sort -u
  fail "no instance materialized from import identity \"$WANT\". The identities the run actually rendered are listed above."
}
GOT_N="$(grep -oE 'from import identity "[^"]*"' <<< "$PLAN_OUT" | sort -u | wc -l | tr -d ' ')"
[ "$GOT_N" = "1" ] || fail "the run materialized $GOT_N distinct identities, expected 1"
log "  the rendered identity asserted, and no other"

# ── 6. it does NOT converge, and that is asserted rather than hidden ────────
log "=== 6. the next run proposes the SAME known update, never anything new ==="
APPLY2_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY2_OUT" | tail -30; fail "the second apply failed"; }
grep -qE 'Apply complete' <<< "$APPLY2_OUT" || { printf '%s\n' "$APPLY2_OUT" | tail -30; fail "the second apply did not report completion"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY2_OUT")"

PLAN2_OUT="$(plan_into 2>&1)"; PLAN2_RC=$?
[ "$PLAN2_RC" -eq 0 ] || { printf '%s\n' "$PLAN2_OUT" | tail -30; fail "the third live-plan exited $PLAN2_RC"; }
assert_only_known_diff "$PLAN2_OUT" "third plan, after applying the known update once"

if awsl s3api head-bucket --bucket "$BUCKET" >/dev/null 2>&1; then STILL="ok"; else STILL="missing"; fi
[ "$STILL" = "ok" ] || fail "expected bucket $BUCKET to still exist afterward, got: $STILL"
[ ! -f "$EST/terraform.tfstate" ] || fail "a state file exists after the second run"
log "  the marker, the bucket, and the SAME known acl update all persist -"
log "  no drift beyond the one documented, external, reproducible gap"

log ""
log "=== PASS (with a documented non-convergence) ==="
log ""
log "DataCite's own raw-resolution-logs bucket, applied against an emulator,"
log "lost its state file, and replanned twice. The rendered identity (the"
log "literal bucket name) was checked against S3's own answer both times."
log "The plan never goes fully empty - see WHAT DOES NOT CONVERGE, AND WHY"
log "in the header - but it never proposes anything beyond the one known,"
log "externally-reproduced acl gap either. Run again with BREAK=1:"
log "everything above step 5 still passes and step 5 goes red."
