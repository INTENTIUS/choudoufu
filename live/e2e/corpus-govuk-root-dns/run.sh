#!/usr/bin/env bash
set -uo pipefail

# A real third-party estate run against a real emulator: issue #274's step
# 6, for .corpus/govuk-infrastructure/terraform/deployments/root-dns.
#
# NOT to be confused with live/e2e/corpus-root-dns-zones, which crosses a
# DIFFERENT estate (.corpus/govuk-aws/terraform/projects/infra-root-dns-zones,
# an older govuk-aws repository) that is already crossed. This script is for
# the govuk-infrastructure repository's root-dns deployment, named by #274's
# comment thread as one of its "six previously unnamed" candidates and, until
# this script existed, genuinely unattempted.
#
# Three resources, GDS's own root DNS zones for a govuk environment:
#
#   aws_route53_zone.internal_zone         server-assigned zone ID
#   aws_route53_zone.external_zone         server-assigned zone ID
#   aws_route53_zone.publishing_subdomain  server-assigned zone ID
#
# (root_dns_zones.tf also declares aws_route53_record.publishing_fastly_acme_challenge,
# but it is `count = var.govuk_environment == "production" ? 1 : 0` - zero
# instances at any non-production environment, which is what every crossing
# in this campaign uses.)
#
# IT DOES NOT CROSS. It is BLOCKED before choudoufu's live-marker mechanism
# is ever reached, and pinned here rather than hidden, per this campaign's
# own discipline against fabricating a clean convergence.
#
# THE BLOCKER. remote.tf declares:
#
#   data "tfe_outputs" "vpc" {
#     organization = "govuk"
#     workspace    = "vpc-${var.govuk_environment}"
#   }
#
# read into aws_route53_zone.internal_zone's `vpc { vpc_id = ... }` block.
# `tfe_outputs` is NOT an AWS data source - it comes from the `hashicorp/tfe`
# provider and reads a workspace's current state outputs over HCP Terraform's
# own REST API (default host app.terraform.io), a completely different
# system from anything AWS or floci ever touch. Reading it for real would
# mean authenticating against "govuk" - GDS's own real, live, production HCP
# Terraform organization - which this campaign has neither credentials for
# nor any business accessing. Init succeeds (the tfe provider installs from
# the registry with no trouble); the very first plan or apply fails
# immediately, before any AWS call is made and before choudoufu's admission
# or identity-resolution stages ever run:
#
#   Error: Invalid provider configuration
#   Provider "registry.opentofu.org/hashicorp/tfe" requires explicit
#   configuration. Add a provider block to the root module and configure the
#   provider's required arguments as described in the provider documentation.
#
#   Error: required token could not be found. Please set the token using an
#   input variable in the provider configuration block or by using the
#   TFE_TOKEN environment variable
#
# THIS IS NEITHER A CHOUDOUFU DEFECT NOR A FLOCI GAP. choudoufu's live block,
# admission, and identity resolution are never reached - the failure is
# stock OpenTofu core resolving a provider configuration for a provider that
# has nothing to do with AWS. floci only emulates AWS services; there is no
# floci endpoint a `tfe` provider block could even point at, unlike
# corpus-root-dns-zones's sibling `data "terraform_remote_state"` (backend =
# "s3"), which floci CAN answer because S3 is the actual read - or unlike
# corpus-mobile-backend's `data "fastly_ip_ranges"`, a genuine external read
# against a PUBLIC, unauthenticated API this sandbox can actually reach. HCP
# Terraform's workspace-outputs API requires an authenticated, org-scoped
# token; short of standing up a real (and unauthorized) session against
# GDS's production "govuk" organization, no floci-only, no-real-AWS
# constraint this campaign runs under can ever satisfy this read. The one
# way this specific estate could ever fully cross is a from-scratch local
# stub of HCP Terraform's own REST API (workspace lookup + current state
# version + outputs, in JSON:API form) with `dev_overrides` pointing the
# `tfe` provider at it - a standalone project, not a config delta.
#
# THE MEASUREMENT GAP THIS PROVES. live/corpus-refusals.json (refusal-probe)
# reports this exact entry "blocked": false, "sites": 0, 3 instances - clean,
# by the only offline instrument this repo has. It has never touched a
# cloud, and it cannot, without credentials this campaign will never have.
# This is issue #274's own opening claim, demonstrated on one more estate:
# "live-check says clean" and "applies... and replans empty" are different
# claims, and only the second one is the product.
#
# THE DELTAS THAT DID WORK, for the record - steps 0-1 below, never
# exercised past the blocker:
#
#   1. `cloud { organization = "govuk" workspaces { tags = [...] } }` (#268),
#      replaced with a live block.
#   2. Emulator flags on the provider block (unused - the failure is in the
#      tfe provider, never the aws one).
#   3. A tfvars file for variables-common.tf's seven undefaulted variables -
#      the same real symlink demo-corpus-mobile-backend's own comment
#      documents, shared across every govuk-infrastructure deployment - plus
#      this estate's own publishing_service_domain.
#   4. govuk_environment = "test" (not "production"), so
#      aws_route53_record.publishing_fastly_acme_challenge and its own
#      SECOND tfe_outputs read (data.tfe_outputs.fastly_www, also gated on
#      `count = var.govuk_environment == "production" ? 1 : 0`) both stay at
#      zero instances and are never evaluated. Only the unconditional
#      data.tfe_outputs.vpc blocks.
#
#   bash live/e2e/corpus-govuk-root-dns/run.sh
#
# Needs Docker, the AWS CLI, and a populated .corpus (`just corpus-fetch`).
# floci is started and torn down even though the estate never reaches an
# AWS call, so this script's own shape matches every other crossing in
# live/e2e and a future re-run costs nothing extra to verify.
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4710, clear of every
#                other live/e2e fixture's port).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#
# Exit codes: 0 when the run reaches exactly the pinned tfe-provider
# blocker and nothing else has moved; non-zero if anything earlier fails,
# if the apply unexpectedly SUCCEEDS (which would mean this environment now
# has TFE_TOKEN set and reachable to "govuk" - almost certainly a leaked
# credential, not a fix, and this script should be re-examined rather than
# treated as a green crossing), or if the apply fails for a different
# reason than the one pinned above.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CORPUS_DIR="${CORPUS_DIR:-$ROOT/.corpus}"
SRC="$CORPUS_DIR/govuk-infrastructure/terraform/deployments/root-dns"
WORK="$(mktemp -d)"
EST="$WORK/estate"
FLOCI_PORT="${FLOCI_PORT:-4710}"
FLOCI_NAME="choudoufu-corpus-govuk-root-dns-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="govuk-root-dns-crossing"
REGION="eu-west-1"

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

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
[ -f "$EST/main.tf" ] && [ -f "$EST/remote.tf" ] \
  || fail "the estate copy is missing main.tf or remote.tf"
RES_N="$(grep -hc '^resource "' "$EST"/*.tf | awk '{s+=$1} END {print s}')"
[ "$RES_N" = "4" ] \
  || fail "the estate declares $RES_N resource blocks (3 unconditional + 1 count-gated), expected 4 - the corpus pin has moved"
grep -q 'data "tfe_outputs" "vpc"' "$EST/remote.tf" \
  || fail "remote.tf no longer declares data.tfe_outputs.vpc - the corpus pin has moved, and this script's whole premise needs re-checking"
log "  estate copied out of .corpus into $EST (3 unconditional resources, 1 count-gated to 0)"

# ── 1. the deltas that DID work ──────────────────────────────────────────────
log "=== 1. onboarding deltas ==="
perl -0pi -e 's/  cloud \{\n    organization = "govuk"\n    workspaces \{\n      tags = \["root-dns", "aws"\]\n    \}\n  \}\n/  live {\n    estate = "'"$ESTATE"'" # DELTA 1\n  }\n/m' "$EST/main.tf"
grep -q 'DELTA 1' "$EST/main.tf" || { sed -n '1,15p' "$EST/main.tf"; fail "DELTA 1 did not match the cloud block - the corpus pin has moved"; }
grep -q 'cloud {' "$EST/main.tf" && fail "DELTA 1 left a cloud block behind"
log "  DELTA 1  cloud block removed, live block added    (onboarding, #268)"

perl -0pi -e 's/^(provider "aws" \{\n  region = var\.aws_region\n)/$1  access_key                  = "test" # DELTA 2\n  secret_key                  = "test"\n  skip_credentials_validation = true\n  skip_requesting_account_id  = true\n  skip_metadata_api_check     = true\n  s3_use_path_style           = true\n/m' "$EST/main.tf"
grep -q 'DELTA 2' "$EST/main.tf" || { sed -n '/provider "aws"/,/^}/p' "$EST/main.tf"; fail "DELTA 2 did not match the provider block"; }
log "  DELTA 2  emulator flags on the provider           (emulator, never exercised - see header)"

cat > "$EST/crossing.auto.tfvars" <<'EOF'
govuk_environment          = "test"
publishing_service_domain  = "www.test.gov.uk"
vpc_cidr                   = "10.40.0.0/16"

eks_control_plane_subnets = {
  a = { az = "eu-west-1a", cidr = "10.40.0.0/24" }
}
eks_public_subnets = {
  a = { az = "eu-west-1a", cidr = "10.40.1.0/24" }
}
eks_private_subnets = {
  a = { az = "eu-west-1a", cidr = "10.40.2.0/24" }
}
legacy_private_subnets = {
  a = { az = "eu-west-1a", cidr = "10.40.3.0/24", nat = false }
}
EOF
log "  DELTA 3  tfvars for variables-common.tf's seven unused-but-required vars"
log "  DELTA 4  govuk_environment = \"test\": the count-gated record and its"
log "           OWN tfe_outputs read both stay at 0 instances"

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
log "  healthy (unused - the blocker below is reached before any AWS call)"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION"

# ── 3. init, then the apply pinned to the tfe-provider blocker ─────────────
log "=== 3. init and apply: pinned to the tfe provider's auth requirement ==="
INIT_OUT="$(cd "$EST" && "$TOFU" init -input=false -no-color 2>&1)" || {
  printf '%s\n' "$INIT_OUT" | tail -30; fail "init failed - the whole point of this script is that init SUCCEEDS and the plan/apply is what blocks"; }
grep -q 'hashicorp/tfe' <<< "$INIT_OUT" \
  || { printf '%s\n' "$INIT_OUT"; fail "init did not install the tfe provider - the corpus pin has moved"; }
log "  init succeeded, including installing hashicorp/tfe (no credentials needed to install)"

APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"
RC=$?

if [ "$RC" -eq 0 ]; then
  fail "the apply SUCCEEDED, which this script does not expect. If this environment has a real TFE_TOKEN reachable to the \"govuk\" organization, that is almost certainly a leaked credential, not a fix - stop and check AWS_* and TFE_* env vars before treating this as a green crossing. If GDS's own remote.tf has changed to no longer depend on tfe_outputs, rewrite this script into a real crossing (delete the state file, live-plan empty twice, BREAK=1 negative control) per every other script in live/e2e."
fi

grep -qF 'required token could not be found' <<< "$APPLY_OUT" || {
  printf '%s\n' "$APPLY_OUT" | grep -E '^Error|^│' | head -30
  fail "the apply failed, but not with the pinned tfe-provider-token error. Something else about the corpus pin or this fork has moved - read the errors above."
}
grep -qF 'registry.opentofu.org/hashicorp/tfe' <<< "$APPLY_OUT" || {
  printf '%s\n' "$APPLY_OUT" | grep -E '^Error|^│' | head -30
  fail "the apply failed with a token error, but not attributed to the tfe provider - read the errors above."
}
log "  apply failed exactly as pinned:"
log "    Error: required token could not be found. Please set the token"
log "    using an input variable in the provider configuration block or by"
log "    using the TFE_TOKEN environment variable"
log ""
log "  data.tfe_outputs.vpc reads GDS's own real, live \"govuk\" HCP"
log "  Terraform organization - a system floci has no bearing on and this"
log "  campaign has no business authenticating against. choudoufu's live"
log "  block, admission, and identity resolution are never reached."

log ""
log "=== BLOCKED (neither choudoufu nor floci - see header) ==="
log ""
log "live/corpus-refusals.json reports this exact entry clean: blocked"
log "false, 0 sites, 3 instances. It has never touched a cloud, and cannot,"
log "without a real credential to GDS's production HCP Terraform"
log "organization that this campaign will never have. Steps 0-2 above ran"
log "clean: the estate onboards with the same cloud-block-to-live-block"
log "shape as every other govuk-infrastructure crossing in this campaign."
log "Nothing downstream of the tfe provider's own token check - state"
log "deletion, live-plan, rendered identities - was ever reached."