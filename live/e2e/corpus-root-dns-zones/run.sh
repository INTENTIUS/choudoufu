#!/usr/bin/env bash
# (moved from the justfile's retired demo-corpus-root-dns-zones recipe; run with: just demo-run corpus-root-dns-zones)
# Issue #274's crossing: .corpus/govuk-aws/terraform/projects/infra-root-dns-zones,
# two aws_route53_zone instances (internal + external) from GDS's Terraform
# 0.12-era module. Its provider pin - `version = "2.46.0"` as a bare
# provider-block argument, no required_providers at all - has no darwin_arm64
# package for this machine, so it is replaced with a real required_providers
# block pinned to 6.59.0, the same #269-shape fix as demo-corpus-cloudwatch-splunk.
# The estate's own `data "terraform_remote_state"` read (an S3-backed state
# file from another team's VPC module) is kept as written; a real S3 object
# holding a minimal, hand-written state file is seeded to answer it. Applied,
# state file deleted, replanned with no resource change proposed twice (the
# estate declares root outputs, so - like demo-corpus-iam-read-only-policy -
# a permanent "Changes to Outputs" section is expected and the assertion
# checks for the absence of a resource action header rather than for
# "Plan:"). Both rendered identities (the real zone IDs, since
# aws_route53_zone is ServerAssigned) checked against Route 53's own answer.
# BREAK=1 corrupts the expected identity and the run must catch it in step 5
# and nowhere else. Needs Docker, the AWS CLI and a populated .corpus; runs
# on its own port (4702) so it can run beside `just demo`.
set -uo pipefail

# A real third-party estate crossed against a real emulator: issue #274's
# step 6, for .corpus/govuk-aws/terraform/projects/infra-root-dns-zones.
#
# Two resources - aws_route53_zone.internal_zone and .external_zone, GDS's
# own module that creates the internal and external root DNS zones for a
# govuk environment. It passes live-check with zero refused sites and, until
# this script existed, had never touched a cloud. Picked as one of #274's
# smallest untouched real corpus estates, smallest-first.
#
# THE ESTATE PREDATES `required_providers`. This is Terraform 0.12-era code:
# `terraform { backend "s3" {} required_version = "~> 0.12.31" }` and a
# provider block carrying `version = "2.46.0"` as an ARGUMENT rather than a
# required_providers constraint - syntax modern OpenTofu still parses, but
# 2.46.0 has no darwin_arm64 package at all (refusal-probe's -schemas run
# records `own_error: ... does not have a package available for your current
# platform`), so it cannot even be installed here, let alone checked for
# #269-shape list-resource skew. Both are replaced: the provider pin becomes
# `= 6.59.0` in a real required_providers block, and the backend becomes a
# live block (#268).
#
# THE OTHER DELTA. `data "terraform_remote_state" "infra_vpc"` reads a real
# S3-backed state file for another team's VPC. Seeded here by writing a
# minimal state JSON (one output, vpc_id) to a bucket this run creates -
# never through choudoufu - and pointing the data source's own `config` at
# floci through the same endpoint/credential arguments the provider block
# uses. The data block itself, and the reference to
# `data.terraform_remote_state.infra_vpc.outputs.vpc_id` inside
# aws_route53_zone.internal_zone's vpc {} block, are the estate's own,
# untouched.
#
# THE TYPE. aws_route53_zone is ServerAssigned (Route 53 mints the zone ID at
# create time; the domain name is not the import identity, and two zones may
# share a name - internal_zone and external_zone here do not, but the type's
# identity never assumes they won't). Its identity resolves through
# internal/live/discovery's type-wide scan (list every hosted zone, read the
# tofu-address marker off each), the same mechanism corpus-crossing and
# repeated-module already proved for this type, not the per-instance
# "materialized ... from import identity" path a client-named type uses -
# though the rendered identity still appears in the trace as
# `from import identity "<ZONEID>"`, and step 5 reads it the same way.
#
#   bash live/e2e/corpus-root-dns-zones/run.sh
#
# Needs Docker and the AWS CLI. .corpus is read, never written: the estate is
# copied out to a temp directory first, same as every other corpus crossing.
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4702, clear of every
#                other live/e2e fixture's port).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        set to 1 to corrupt one expected zone ID before step 5,
#                proving the identity assertion is load-bearing.
#
# Exit codes: 0 on a real pass, non-zero on a real failure. Every assertion
# reads command output, an exit code, or the emulator's own answer through
# the AWS CLI.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CORPUS_DIR="${CORPUS_DIR:-$ROOT/.corpus}"
SRC="$CORPUS_DIR/govuk-aws/terraform/projects/infra-root-dns-zones"
WORK="$(mktemp -d)"
EST="$WORK/estate"
FLOCI_PORT="${FLOCI_PORT:-4702}"
FLOCI_NAME="choudoufu-corpus-root-dns-zones-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="govuk-aws-root-dns-zones-crossing"
REGION="eu-west-1"
REMOTE_STATE_BUCKET="rdz-crossing-remote-state-$$"
STACKNAME="crossing"
INTERNAL_NAME="mydomain.internal"
EXTERNAL_NAME="mydomain.external"

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
cp "$SRC/main.tf" "$EST/"
[ -f "$EST/main.tf" ] || fail "the estate copy is missing main.tf"
log "  estate copied out of .corpus into $EST"

# ── 1. the deltas ─────────────────────────────────────────────────────────
# Rather than a single fragile regex over the whole terraform+provider+data
# preamble (Terraform's own `${...}` interpolation syntax collides with
# shell and perl interpolation once floci's endpoint has to be spliced in),
# the file is split at the first resource block. Everything from there down
# (the two zones, the third count=0 zone, and all five outputs) is kept
# byte for byte; only the preamble above it is replaced.
log "=== 1. onboarding deltas ==="
grep -q '^resource "aws_route53_zone" "internal_zone" {$' "$EST/main.tf" \
  || fail "the resource-block boundary line is not there - the corpus pin has moved"
awk '/^resource "aws_route53_zone" "internal_zone" \{$/{f=1} f{print}' "$EST/main.tf" > "$EST/main.tf.tail"
[ -s "$EST/main.tf.tail" ] || fail "splitting main.tf produced no tail - the corpus pin has moved"

HEAD_MARKER_COUNT="$(grep -c '^variable "aws_region" {$' "$EST/main.tf")"
[ "$HEAD_MARKER_COUNT" = "1" ] || fail "the variable-block start is not there - the corpus pin has moved"
grep -q '^# Resources$' "$EST/main.tf" || fail "the '# Resources' split marker is not there - the corpus pin has moved"
awk '/^# Resources$/{exit} {print}' "$EST/main.tf" > "$EST/main.tf.vars"
grep -q 'variable "stackname"' "$EST/main.tf.vars" || fail "the split lost the variable declarations - the corpus pin has moved"
grep -q 'backend "s3"' "$EST/main.tf.vars" && fail "the split kept the old backend block - the corpus pin has moved"

cat "$EST/main.tf.vars" > "$EST/main.tf"
cat >> "$EST/main.tf" <<EOF
# Resources
# --------------------------------------------------------------
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      # DELTA 1: was \`version = "2.46.0"\` on the provider block - no
      # darwin_arm64 package exists for that release at all.
      version = "= 6.59.0"
    }
  }

  # DELTA 2: was a root S3 backend block (#268).
  live {
    estate = "$ESTATE"
  }
}

provider "aws" {
  region = var.aws_region

  access_key                   = "test"
  secret_key                   = "test"
  skip_credentials_validation  = true
  skip_metadata_api_check      = true
  s3_use_path_style            = true
}

data "terraform_remote_state" "infra_vpc" {
  backend = "s3"

  # DELTA 3: the estate's own remote-state read, pointed at floci.
  config = {
    bucket                       = "\${var.remote_state_bucket}"
    key                          = "\${coalesce(var.remote_state_infra_vpc_key_stack, var.stackname)}/infra-vpc.tfstate"
    region                       = "\${var.aws_region}"
    access_key                   = "test"
    secret_key                   = "test"
    skip_credentials_validation  = true
    skip_metadata_api_check      = true
    use_path_style                = true
    endpoints = {
      s3 = "$ENDPOINT"
    }
  }
}

EOF
cat "$EST/main.tf.tail" >> "$EST/main.tf"
rm -f "$EST/main.tf.tail" "$EST/main.tf.vars"

grep -q "estate = \"$ESTATE\"" "$EST/main.tf" || fail "the delta did not land in main.tf"
grep -q 'backend "s3" {}' "$EST/main.tf" && fail "the root backend block is still there"
grep -q 'resource "aws_route53_zone" "external_zone"' "$EST/main.tf" || fail "the resource blocks were lost in the split"
log "  DELTA 1  provider pinned = 6.59.0                            (#269-shape)"
log "  DELTA 2  backend block removed, live block added             (#268)"
log "  DELTA 3  remote-state data source pointed at floci            (emulator)"

# ── 2. floci ────────────────────────────────────────────────────────────────
log "=== 2. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"route53"' <<< "$HEALTH" && break
  sleep 2
done
grep -q '"route53"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (route53) at $ENDPOINT"
log "  healthy"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION"

# DELTA 4, the seeded remote-state object: a real S3 bucket holding a
# minimal, hand-written state file with one output (vpc_id). Never written
# through choudoufu.
awsl s3api create-bucket --bucket "$REMOTE_STATE_BUCKET" \
  --create-bucket-configuration LocationConstraint="$REGION" >/dev/null \
  || fail "could not seed the remote-state bucket"
VPC_ID="vpc-rdzcrossing00"
cat > "$WORK/infra-vpc.tfstate" <<EOF
{"version":4,"terraform_version":"1.5.0","serial":1,"lineage":"crossing","outputs":{"vpc_id":{"value":"$VPC_ID","type":"string"}},"resources":[]}
EOF
awsl s3api put-object --bucket "$REMOTE_STATE_BUCKET" --key "$STACKNAME/infra-vpc.tfstate" \
  --body "$WORK/infra-vpc.tfstate" >/dev/null \
  || fail "could not seed the remote-state object"
log "  DELTA 4  a real S3 object holding the VPC's remote state seeded  (seeded read)"

cat > "$EST/crossing.auto.tfvars" <<EOF
remote_state_bucket = "$REMOTE_STATE_BUCKET"
stackname = "$STACKNAME"
EOF
log "  DELTA 5  var values naming the seeded bucket + stack             (onboarding)"

# ── 3. stand the estate up ──────────────────────────────────────────────────
log "=== 3. init and apply: 2 instances ==="
( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null ) || {
  ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "init failed"; }
APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY_OUT" | grep -E '^Error|^│' | head -30
  fail "the apply failed"
}
grep -qE 'Apply complete! Resources: 2 added' <<< "$APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY_OUT"; fail "the apply did not create exactly 2 resources"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY_OUT")"

# Read the zone IDs back through the AWS CLI, never through choudoufu.
INTERNAL_ZONE_ID="$(awsl route53 list-hosted-zones-by-name --dns-name "${INTERNAL_NAME}." \
  --query "HostedZones[?Name=='${INTERNAL_NAME}.'].Id | [0]" --output text | sed 's|/hostedzone/||')"
EXTERNAL_ZONE_ID="$(awsl route53 list-hosted-zones-by-name --dns-name "${EXTERNAL_NAME}." \
  --query "HostedZones[?Name=='${EXTERNAL_NAME}.'].Id | [0]" --output text | sed 's|/hostedzone/||')"
[ -n "$INTERNAL_ZONE_ID" ] && [ "$INTERNAL_ZONE_ID" != "None" ] || fail "could not find the internal zone through the AWS CLI"
[ -n "$EXTERNAL_ZONE_ID" ] && [ "$EXTERNAL_ZONE_ID" != "None" ] || fail "could not find the external zone through the AWS CLI"
log "  internal zone lives: $INTERNAL_ZONE_ID ($INTERNAL_NAME)"
log "  external zone lives: $EXTERNAL_ZONE_ID ($EXTERNAL_NAME)"

for pair in "$INTERNAL_ZONE_ID:aws_route53_zone.internal_zone:0" "$EXTERNAL_ZONE_ID:aws_route53_zone.external_zone:0"; do
  ZID="${pair%%:*}"; REST="${pair#*:}"
  ADDR="$(awsl route53 list-tags-for-resource --resource-type hostedzone --resource-id "$ZID" \
    --query "ResourceTagSet.Tags[?Key=='tofu-address'].Value | [0]" --output text 2>/dev/null || echo None)"
  [ "$ADDR" = "$REST" ] || fail "zone $ZID carries tofu-address=$ADDR, expected $REST"
done
log "  both zones carry their own tofu-address marker"

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
# Not a "Plan:" grep: the estate declares root outputs, and live-plan holds
# no state between runs, so every run shows a permanent "Changes to Outputs"
# section (same as demo-corpus-iam-policy and demo-corpus-iam-read-only-policy) -
# check for the absence of a resource action header instead.
grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$PLAN_OUT" \
  && { grep -E '^  # .+ will be' <<< "$PLAN_OUT"; fail "the plan proposes a resource change"; }
grep -qE '^Foreign resources: (none|nothing was swept)' <<< "$PLAN_OUT" \
  || { grep -E '^Foreign resources:' <<< "$PLAN_OUT"; fail "the plan reports foreign resources"; }
log "  no resource change proposed; nothing foreign"

WANT_INTERNAL="$INTERNAL_ZONE_ID"
WANT_EXTERNAL="$EXTERNAL_ZONE_ID"
if [ "${BREAK:-}" = "1" ]; then
  WANT_INTERNAL="ZWRONGZONEIDXX"
  log "  BREAK=1: expecting \"$WANT_INTERNAL\" for the internal zone, a zone ID"
  log "           that does not exist. The plan above stayed empty. This step"
  log "           must fail."
fi
for WANT in "$WANT_INTERNAL" "$WANT_EXTERNAL"; do
  grep -qF "from import identity \"$WANT\"" <<< "$PLAN_OUT" || {
    grep -oE 'from import identity "[^"]*"' <<< "$PLAN_OUT" | sort -u
    fail "no instance materialized from import identity \"$WANT\". The identities the run actually rendered are listed above."
  }
done
GOT_N="$(grep -oE 'from import identity "[^"]*"' <<< "$PLAN_OUT" | sort -u | wc -l | tr -d ' ')"
[ "$GOT_N" = "2" ] || fail "the run materialized $GOT_N distinct identities, expected 2"
log "  both rendered identities (the real zone IDs) asserted, and no others"

# ── 6. and it converges ─────────────────────────────────────────────────────
log "=== 6. the next run proposes no resource change, and applying it creates nothing ==="
PLAN2_OUT="$(plan_into 2>&1)"; PLAN2_RC=$?
[ "$PLAN2_RC" -eq 0 ] || { printf '%s\n' "$PLAN2_OUT" | tail -30; fail "the second live-plan exited $PLAN2_RC"; }
grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$PLAN2_OUT" \
  && { grep -E '^  # .+ will be' <<< "$PLAN2_OUT"; fail "the second plan proposes a resource change, so the run does not converge"; }

APPLY2_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY2_OUT" | tail -30; fail "the second apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY2_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY2_OUT"; fail "the second apply added, changed or destroyed a resource"; }
STILL="$(awsl route53 list-hosted-zones --query 'length(HostedZones)' --output text)"
[ "$STILL" = "2" ] || fail "expected exactly 2 hosted zones afterward, got $STILL"
[ ! -f "$EST/terraform.tfstate" ] || fail "a state file exists after the second run"
log "  converged: no resource change proposed, nothing added, both zones still there, still no state file"

log ""
log "=== PASS ==="
log ""
log "GDS's own infra-root-dns-zones module - the internal and external root"
log "DNS zones for a govuk environment, on Terraform 0.12-era syntax with a"
log "provider pin that cannot even be installed on this machine - applied"
log "against an emulator, lost its state file, and replanned with no"
log "resource change proposed twice. Both rendered identities (the real"
log "zone IDs) were checked against Route 53's own answer. Run again with"
log "BREAK=1: everything above step 5 still passes and step 5 goes red."
