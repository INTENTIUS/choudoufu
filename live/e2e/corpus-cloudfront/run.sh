#!/usr/bin/env bash
set -uo pipefail

# GitHub issue #274's cloudfront leg: the first real-cloud contact for the
# unique-name discovery mechanism (aws_cloudfront_cache_policy and
# aws_cloudfront_origin_request_policy, "discovery binds a live object by
# that name rather than by an ownership tag" - live/survey-full.json's
# "unique-name" path), against .corpus/govuk-infrastructure's own cloudfront
# deployment: 16 instances, written by GOV.UK and not for us. It passes
# live-check with zero refused sites.
#
# It does NOT cross, in two separate ways this script pins rather than
# hides:
#
#   STEP 3  the full 16-instance estate, applied as authored. Its resources
#           span two provider configurations - the default one (eu-west-1)
#           and an aliased "aws.global" (us-east-1, for CloudFront/WAF,
#           which is how GOV.UK actually deploys this) - and discovery-
#           needing types sit on BOTH sides: aws_cloudfront_cache_policy and
#           aws_cloudfront_distribution on the default provider,
#           aws_wafv2_web_acl and the aws_cloudwatch_log_* chain on
#           aws.global. live/LIMITATIONS.md documents this as a v0 bound:
#           "Marker discovery goes through one provider configuration per
#           run." This is not an onboarding delta this script works around;
#           it is the estate as GOV.UK wrote it, refused before any
#           resource is touched.
#
#   STEP 5  a REAL fix, found by this script's first run today. Before it,
#           EVERY unique-name type failed its very first apply,
#           unconditionally, with "Unstamped marker-only resource" - before
#           discovery ever got a chance to run. internal/command/live_plan.go's
#           statelessStampGaps re-derived stamping severity from
#           stamp.Result.Skipped without ever consulting
#           identity.DiscoveryCause.BindsByName(), the exact method
#           internal/live/stamp's OWN mustStamp() uses to make the same
#           decision correctly. The unique-name leg that landed today had
#           never actually reached a live apply. Fixed by adding the same
#           check to the second reader (`git log -1 --grep BindsByName` for
#           the commit, if this file's history survives a rebase).
#
#   STEP 6  a FLOCI GAP, not a choudoufu defect: once state is deleted and
#           the estate is replanned, Cloud Control's List/GetResource for
#           these two CloudFront types answers with a FLAT Properties object
#           ({"Id":...,"Name":...}) instead of AWS's own documented shape,
#           nested under a *Config object ({"Id":...,"CachePolicyConfig":
#           {"Name":...}}) - live/registry.json's unique_name_property field
#           for both types, scraped from AWS's real CloudFormation registry
#           schema, says CachePolicyConfig/Name and OriginRequestPolicyConfig/
#           Name. choudoufu reads the documented path, finds nothing there,
#           and REFUSES - "Listed resource with no readable name" - rather
#           than silently binding wrong or creating a duplicate. That is the
#           mechanism failing safe, not failing; the crossing itself (delete
#           state, replan empty) cannot complete against floci today because
#           floci's own emulation of these two types does not match the
#           schema this fork verified against.
#
#   bash live/e2e/corpus-cloudfront/run.sh
#
# Needs Docker, the AWS CLI, and .corpus populated (`just corpus-fetch`).
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4694, clear of every
#                other live/e2e script's default as of this writing).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#
# The corpus checkout is shared across worktrees and is NEVER written to: the
# estate is copied out first and every delta below lands on the copy.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CORPUS_DIR="${CORPUS_DIR:-$ROOT/.corpus}"
SRC="$CORPUS_DIR/govuk-infrastructure/terraform/deployments/cloudfront"
WORK="$(mktemp -d)"
EST="$WORK/estate"
UNIQ="$WORK/uniquename"
FLOCI_PORT="${FLOCI_PORT:-4694}"
FLOCI_NAME="choudoufu-corpus-cloudfront-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

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
cp "$SRC"/*.tf "$SRC"/index.js "$EST/"
[ -f "$EST/main.tf" ] && [ -f "$EST/logging.tf" ] || fail "the estate copy is missing main.tf or logging.tf"
log "  estate copied out of .corpus into $EST"

# ── 1. floci ────────────────────────────────────────────────────────────────
log "=== 1. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"cloudfront"' <<< "$HEALTH" && break
  sleep 2
done
grep -q '"cloudfront"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (cloudfront) at $ENDPOINT"
log "  healthy"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=eu-west-1

# ── 2. onboarding deltas, full estate ───────────────────────────────────────
log "=== 2. onboarding deltas (full 16-instance estate) ==="

# DELTA 1, ordinary onboarding: `cloud { organization = "govuk" ... }` out,
# `live { estate = ... }` in (issue #268).
perl -0pi -e 's/^  cloud \{\n    organization = "govuk"\n    workspaces \{\n      tags = \["cloudfront", "eks", "aws"\]\n    \}\n  \}\n/  # DELTA 1: was a cloud block naming the govuk TFC organization.\n\n  live {\n    estate = "govuk-cloudfront"\n  }\n/m' "$EST/main.tf"
grep -q 'DELTA 1' "$EST/main.tf" || fail "DELTA 1 did not match the cloud block - the corpus pin has moved"
grep -q 'live {' "$EST/main.tf" || fail "DELTA 1 did not add a live block"
log "  DELTA 1  cloud block removed, live block added   (onboarding, #268)"

# DELTA 2: the estate asks aws ~> 6.28, which is not the ~> 5.x shape #269
# is about, but is pinned anyway to the exact release live/survey.json was
# verified against, so a provider-version-skew warning does not obscure
# what this script is actually measuring.
sed -i.bak 's/version = "~> 6.28"/version = "= 6.59.0" # DELTA 2, pinned to survey.json'"'"'s verified release/' "$EST/main.tf"
rm -f "$EST"/*.bak
grep -q 'DELTA 2' "$EST/main.tf" || fail "DELTA 2 did not match the aws provider constraint - the corpus pin has moved"
log "  DELTA 2  aws pinned = 6.59.0                      (onboarding, matches survey.json)"

# DELTA 3, emulator wiring on BOTH provider blocks: the default one and the
# aliased "global" (us-east-1) one.
perl -0pi -e 's/^(provider "aws" \{\n  region = var\.aws_region\n)/$1  skip_credentials_validation = true # DELTA 3\n  skip_metadata_api_check     = true\n  s3_use_path_style           = true\n/m; s/^(provider "aws" \{\n  alias  = "global"\n  region = "us-east-1"\n)/$1  skip_credentials_validation = true # DELTA 3\n  skip_metadata_api_check     = true\n  s3_use_path_style           = true\n/m' "$EST/main.tf"
[ "$(grep -c 'DELTA 3' "$EST/main.tf")" = "2" ] \
  || fail "DELTA 3 reached $(grep -c 'DELTA 3' "$EST/main.tf") provider blocks, expected 2 - the corpus pin has moved"
log "  DELTA 3  emulator flags on both providers          (emulator)"

# DELTA 4, onboarding: values for the estate's variables. variables-common.tf
# is symlinked into every govuk-infrastructure deployment root and declares
# networking/cluster variables this deployment never references but OpenTofu
# still requires a value for.
cat > "$EST/crossing.auto.tfvars" <<'EOF'
govuk_environment          = "test"
publishing_service_domain  = "publishing.example.net"
vpc_cidr                   = "10.40.0.0/16"
eks_control_plane_subnets  = { a = { az = "eu-west-1a", cidr = "10.40.1.0/24" } }
eks_public_subnets         = { a = { az = "eu-west-1a", cidr = "10.40.2.0/24" } }
eks_private_subnets        = { a = { az = "eu-west-1a", cidr = "10.40.3.0/24" } }
legacy_private_subnets     = { a = { az = "eu-west-1a", cidr = "10.40.4.0/24", nat = false } }

cloudfront_enable                = true
origin_www_domain                = "origin-www.example.net"
origin_www_id                    = "origin-www"
origin_assets_domain             = "origin-assets.example.net"
origin_assets_id                 = "origin-assets"
origin_notify_domain             = "origin-notify.example.net"
origin_notify_id                 = "origin-notify"
cloudfront_web_acl_default_allow = true
cloudfront_web_acl_allow_gds_ips = false
www_certificate_arn              = "arn:aws:acm:us-east-1:000000000000:certificate/00000000-0000-0000-0000-000000000000"
assets_certificate_arn           = "arn:aws:acm:us-east-1:000000000000:certificate/00000000-0000-0000-0000-000000000000"
EOF
log "  DELTA 4  tfvars for the estate's declared variables (onboarding)"

# ── 3. the full estate does not cross ───────────────────────────────────────
log "=== 3. the full 16-instance estate, as GOV.UK wrote it ==="
( cd "$EST" && "$TOFU" init -upgrade -input=false -no-color >/dev/null 2>&1 ) || fail "init -upgrade failed"
APPLY_FULL="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"
RC=$?
if [ "$RC" -eq 0 ]; then
  fail "the full estate applied cleanly. Either the multi-provider-configuration bound (live/LIMITATIONS.md, 'Marker discovery goes through one provider configuration per run') has been lifted, or this corpus pin no longer spans two provider configurations - re-read this script's header before assuming the estate crossed."
fi
grep -q 'Marker discovery across several provider configurations' <<< "$APPLY_FULL" \
  || { grep -E '^Error|^│' <<< "$APPLY_FULL" | head -20
       fail "the full estate failed, but not with the multi-provider-configuration refusal this script pins. Something else about the corpus pin or this fork has moved."; }
log "  refused, exactly as documented: this estate's discovery-needing"
log "  resources span the default and aliased(global) aws provider"
log "  configurations, and live-plan discovers through one config per run."
log "  This is NOT crossed. It is GOV.UK's estate as authored, and"
log "  live/LIMITATIONS.md calls this a v0 bound rather than a permanent one."

# ── 4. the unique-name mechanism, isolated ──────────────────────────────────
# The two resources in this estate that exercise the leg #274 asked this
# script to test both sit on the DEFAULT provider and have no dependency on
# anything the full estate above could not get past. Extracted verbatim -
# not re-typed - so what applies below is GOV.UK's own configuration for
# these two resources, not a fixture standing in for it.
log "=== 4. isolating the unique-name resources (verbatim from main.tf) ==="
mkdir -p "$UNIQ"
awk '/^resource "aws_cloudfront_cache_policy" "no-cookies" \{/,/^\}/' "$EST/main.tf" > "$UNIQ/policies.tf"
awk '/^resource "aws_cloudfront_origin_request_policy" "all-viewer-headers" \{/,/^\}/' "$EST/main.tf" >> "$UNIQ/policies.tf"
[ "$(grep -c '^resource' "$UNIQ/policies.tf")" = "2" ] \
  || fail "expected to extract exactly 2 resource blocks, got $(grep -c '^resource' "$UNIQ/policies.tf") - the corpus pin has moved"
cat > "$UNIQ/terraform.tf" <<'EOF'
terraform {
  required_version = "~> 1.15"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.59.0"
    }
  }

  live {
    estate = "govuk-cloudfront-uniquename"
  }
}

provider "aws" {
  region                       = "eu-west-1"
  skip_credentials_validation  = true
  skip_metadata_api_check      = true
  s3_use_path_style            = true
}
EOF
( cd "$UNIQ" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "the isolated init failed"
log "  extracted aws_cloudfront_cache_policy.no-cookies and"
log "  aws_cloudfront_origin_request_policy.all-viewer-headers"

# ── 5. the fix: a first apply, where none ever landed before today ─────────
log "=== 5. first apply: the unique-name leg's first real cloud contact ==="
APPLY1="$(cd "$UNIQ" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY1" | grep -E '^Error|^│' | head -20
  fail "the isolated apply failed. If this says 'Unstamped marker-only resource', the fix internal/command/live_plan.go's statelessStampGaps needs (consulting identity.DiscoveryCause.BindsByName(), the same check stamp.mustStamp already makes) is missing or has regressed - see this script's header."
}
grep -qE 'Apply complete! Resources: 2 added' <<< "$APPLY1" \
  || { grep -E 'Apply complete' <<< "$APPLY1"; fail "the apply did not create exactly 2 instances"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY1" | head -1)"

# Read the objects back through the AWS CLI, never through choudoufu. Both
# types are untaggable - the whole reason this leg exists - so there is no
# marker to check; the only proof of ownership is that the object exists
# under the name the configuration declared.
CP="$(awsl cloudfront list-cache-policies --type custom --query "CachePolicyList.Items[?CachePolicy.CachePolicyConfig.Name=='no-cookies'].CachePolicy.Id | [0]" --output text)"
[ -n "$CP" ] && [ "$CP" != "None" ] || fail "no cache policy named no-cookies exists"
ORP="$(awsl cloudfront list-origin-request-policies --type custom --query "OriginRequestPolicyList.Items[?OriginRequestPolicy.OriginRequestPolicyConfig.Name=='all-headers-cookies'].OriginRequestPolicy.Id | [0]" --output text)"
[ -n "$ORP" ] && [ "$ORP" != "None" ] || fail "no origin request policy named all-headers-cookies exists"
log "  both objects exist: cache policy $CP, origin request policy $ORP"

# ── 6. the crossing itself: blocked by floci, not by choudoufu ─────────────
log "=== 6. delete the state, replan cold: floci's own gap ==="
rm -f "$UNIQ/terraform.tfstate" "$UNIQ/terraform.tfstate.backup"
PLAN1="$(cd "$UNIQ" && "$TOFU" live-plan -input=false -no-color 2>&1)"
RC=$?
if [ "$RC" -eq 0 ]; then
  fail "the cold replan succeeded. floci's Cloud Control emulation for AWS::CloudFront::CachePolicy / AWS::CloudFront::OriginRequestPolicy now nests Name under *Config the way AWS's own schema documents - re-read this script's header, this is GOOD NEWS, and step 6 should be rewritten into a real crossing (assert No changes, twice, per every other script in live/e2e)."
fi
grep -q 'Listed resource with no readable name' <<< "$PLAN1" \
  || { grep -E '^Error|^│' <<< "$PLAN1" | head -20
       fail "the cold replan failed, but not with the unreadable-name refusal this script pins. Something has moved."; }
log "  refused, exactly as documented: Cloud Control listed both objects"
log "  back with a FLAT Properties shape (Id, Name, Etag, LastModifiedTime),"
log "  not AWS's own documented shape (Name nested under CachePolicyConfig /"
log "  OriginRequestPolicyConfig - live/registry.json's unique_name_property"
log "  for both types). choudoufu reads the documented path, finds nothing"
log "  there, and REFUSES rather than binding wrong or proposing a duplicate"
log "  create. That is the mechanism failing SAFE. The crossing itself -"
log "  delete state, replan empty, replan empty again - cannot complete"
log "  against floci today for these two types; it is not something this"
log "  fork's code gets to decide."

log ""
log "=== PASS (partial) ==="
log ""
log "The unique-name discovery leg had never touched a live cloud before"
log "today. Its own bug did: EVERY unique-name type failed its first apply"
log "unconditionally, before discovery ran at all (step 5's fix). What"
log "remains untestable against floci is the leg's actual binding behavior -"
log "step 6's floci gap blocks it before that question is reachable."
log ""
log "The full 16-instance estate does not cross (step 3): it spans two"
log "provider configurations and live-plan discovers through one per run,"
log "a documented v0 bound. This script measures the estate as GOV.UK wrote"
log "it, not a version restructured to fit around that bound."
