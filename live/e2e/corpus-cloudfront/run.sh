#!/usr/bin/env bash
set -uo pipefail

# GitHub issue #274's cloudfront leg: the first real-cloud contact for the
# unique-name discovery mechanism (aws_cloudfront_cache_policy and
# aws_cloudfront_origin_request_policy, "discovery binds a live object by
# that name rather than by an ownership tag" - live/survey-full.json's
# "unique-name" path), against .corpus/govuk-infrastructure's own cloudfront
# deployment: 16 instances, written by GOV.UK and not for us. It used to
# pass live-check with zero refused sites; since 2026-08-20 it carries
# exactly one, aws_iam_policy_attachment, and step 3 pins it by count and by
# name - see that step's own note.
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
#           aws.global.
#
#           That USED to be where this estate died, before any resource was
#           touched: "Marker discovery across several provider
#           configurations", live/LIMITATIONS.md's v0 bound "Marker
#           discovery goes through one provider configuration per run".
#           Issue #283 lifted it - statelessDiscover runs one discovery pass
#           per provider configuration with discovery.Request.ScopeProvider
#           narrowing each to the resolutions whose own resource block names
#           it - and this step now asserts that refusal is GONE.
#
#           The estate still does not cross. RE-VERIFIED 2026-08-19 (issue
#           #300): the aws_wafv2_web_acl listability gap this comment used to
#           pin is GONE - no "cannot list" error anywhere, and the aliased
#           aws.global pass's own "Incomplete sweep for undeclared resources"
#           warnings no longer name it, exactly as they would if it were
#           still unlistable.
#
#           RE-MEASURED 2026-08-21 (issue #331), and the estate now stops
#           SOONER than every earlier version of this comment describes: at
#           PLAN time, with a single refusal, before floci is asked for
#           anything at all. What this step now pins is that one refusal, by
#           count and by name:
#
#             aws_iam_policy_attachment (1 instance, basic_lambda_attach) -
#             refused as unadmitted-type, a REAL PROVIDER BOUNDARY reported
#             at the right moment rather than a choudoufu gap. The provider
#             has no Importer for this type at all: helper/schema's
#             ImportState answers "resource ... doesn't support import" for a
#             nil Importer, which is the same hard stop stock OpenTofu's
#             `terraform import` hits, and v6.59.0's
#             iam_policy_attachment.html.markdown carries no Import section.
#
#             This script used to assert the string "aws_iam_policy_attachment
#             doesn't support import" in the APPLY output, because choudoufu
#             admitted the type on the strength of its wire identity schema
#             (policy_arn) and only discovered the missing Importer when
#             internal/live/projection actually called ImportResourceState -
#             a plan refusal traded for an apply refusal, which this fork is
#             not allowed to do. Issue #331 closed that: tools/survey-gen
#             probes ImportResourceState for every type, tools/row-gen emits
#             identity.NotImportableTypes from the result, and every
#             admission route now consults identity.NotImportable. So that
#             string can no longer appear anywhere - the refusal arrives
#             before the projection exists to raise it.
#
#             The two floci gaps this comment used to pin alongside it -
#             aws_cloudwatch_log_delivery_destination and
#             aws_cloudwatch_log_delivery_source (2 instances each), whose
#             reads 400 with "UnsupportedOperation: Operation
#             Get{DeliveryDestination,DeliverySource} is not supported",
#             lex00/floci#79 - are NOT closed and NOT asserted here any more.
#             They are apply-time failures and this estate no longer reaches
#             apply. Step 3 asserts their absence for that reason, so that a
#             regression putting the estate back past the plan gate is caught
#             here rather than read as progress.
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
#   STEP 6  a FLOCI GAP, not a choudoufu defect - but RE-VERIFIED 2026-08-19
#           (issue #300) to be a DIFFERENT floci gap than the one this
#           comment used to pin. The original shape gap - Cloud Control's
#           List/GetResource for these two CloudFront types answering with a
#           FLAT Properties object ({"Id":...,"Name":...}) instead of AWS's
#           documented shape nested under a *Config object
#           ({"Id":...,"CachePolicyConfig":{"Name":...}}) - is CLOSED: a cold
#           replan now finds both objects by name instead of refusing with
#           "Listed resource with no readable name".
#
#           It still does not cross empty, for a new reason: floci's
#           GetCachePolicy/GetOriginRequestPolicy responses carry almost
#           nothing besides Name/Comment. Read straight back with the AWS
#           CLI (bypassing choudoufu/OpenTofu entirely), the CachePolicyConfig
#           this estate created with default_ttl=300, max_ttl=31536000,
#           min_ttl=1 and a full parameters_in_cache_key_and_forwarded_to_origin
#           block comes back as {"Comment":"","Name":"no-cookies"} - every
#           other field silently dropped, and the same for
#           OriginRequestPolicyConfig's cookies/headers/query-strings config.
#           choudoufu's replan sees the live object's config reset to zero
#           values and proposes an in-place update to "restore" configuration
#           that was never actually lost against real AWS - a false-drift
#           plan (Plan: 0 to add, 2 to change, 0 to destroy), not the empty
#           plan a real crossing needs. Filed as lex00/floci#80; the earlier
#           shape gap this comment used to describe was lex00/floci's own
#           fix, not tracked under a kept-open number here.
#
#           This step is NOT rewritten into a real crossing: "the cold
#           replan succeeded" (RC 0) is necessary but not sufficient, and
#           this script now distinguishes RC 0 with an empty plan (a real
#           crossing) from RC 0 with a nonempty one (still blocked, just by
#           a different floci gap than the one first documented here).
#
#   bash live/e2e/corpus-cloudfront/run.sh
#
# Needs Docker, the AWS CLI, and .corpus populated (`just corpus-fetch`).
#
# Env overrides:
#   BREAK=1      corrupts STEP 3's expected unadmitted-type count (1 -> 2).
#                That step must then fail. It is scoped to step 3 because
#                that is the only step here asserting a count; the rest
#                assert the presence or absence of a named diagnostic.
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

# ── 3. the full estate clears the two-configuration bound ───────────────────
log "=== 3. the full 16-instance estate, as GOV.UK wrote it ==="
( cd "$EST" && "$TOFU" init -upgrade -input=false -no-color >/dev/null 2>&1 ) || fail "init -upgrade failed"
APPLY_FULL="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"
RC=$?

# What this step exists to assert, and the reason it is worth a whole corpus
# estate: the refusal that used to end this run before any resource was
# touched must not fire. It is the ONLY assertion here that is about issue
# #283; everything below it is bookkeeping about where the estate stops now.
grep -q 'Marker discovery across several provider configurations' <<< "$APPLY_FULL" \
  && fail "the multi-provider-configuration refusal fired again (issue #283 regression). This estate's discovery-needing resources sit on both the default (eu-west-1) and the aliased aws.global (us-east-1) configuration, which is the shape AWS's own CloudFront-plus-WAF guidance produces; statelessDiscover is supposed to run one scoped discovery pass per configuration and discovery.Merge combine them."
log "  the two-configuration refusal is GONE: discovery ran per provider"
log "  configuration (default eu-west-1 and aliased global us-east-1)"
log "  rather than refusing the estate outright.                (#283)"

if [ "$RC" -eq 0 ]; then
  fail "the full estate applied cleanly, which this script does not yet expect. That is GOOD NEWS: every remaining blocker below has been closed too, and this step should be rewritten into a real crossing (apply, delete the state, replan empty, replan empty again - per every other script in live/e2e)."
fi

# The aws_wafv2_web_acl listability gap this comment used to pin is GONE -
# verified 2026-08-19 (#300). If it ever comes back, say so rather than let
# the generic fallback below swallow it as "something moved".
grep -q 'cannot list aws_wafv2_web_acl' <<< "$APPLY_FULL" \
  && fail "the aws_wafv2_web_acl listability gap fired again. It was verified CLOSED on 2026-08-19 (issue #300) - no 'cannot list' error, and the aliased aws.global pass's own undeclared-resource sweep no longer named the type. Something has regressed; read the errors above and re-stale this script's header comment to match."

# Where it stops instead: ONE plan-time refusal, before any resource is
# touched. See this script's header for the full account (issue #331).
#
# The count is asserted, not just the presence: an estate that grows a second
# refused type has moved, and a script that only greps for one name would
# report the same "as expected" either way.
WANT_UNADMITTED_N=1
WANT_UNADMITTED_TYPE="aws_iam_policy_attachment"
if [ "${BREAK:-}" = "1" ]; then
  WANT_UNADMITTED_N=2
  log "  BREAK=1: expecting 2 unadmitted-type sites where the estate has 1."
  log "           Wrong. This step must fail."
fi

log "  all distinct Error: lines from this apply:"
grep -E '^Error:' <<< "$APPLY_FULL" | sort | uniq -c | sed 's/^/    /'
UNADMITTED_N="$(grep -c '^Error: Resource type is outside the live-markers subset$' <<< "$APPLY_FULL")"
[ "$UNADMITTED_N" = "$WANT_UNADMITTED_N" ] \
  || { printf '%s\n' "$APPLY_FULL" | head -40
       fail "expected $WANT_UNADMITTED_N unadmitted-type site(s) in this estate, got $UNADMITTED_N - read the output above"; }
grep -qE "resource \"$WANT_UNADMITTED_TYPE\"" <<< "$APPLY_FULL" \
  || { printf '%s\n' "$APPLY_FULL" | head -40
       fail "the one unadmitted-type refusal is not about $WANT_UNADMITTED_TYPE. That type has no Importer in the pinned provider release and identity.NotImportableTypes is derived from a probe of exactly that; if it is no longer refused here, either the probe has gone stale or a route stopped consulting the veto (issue #331)."; }

# The refusal has to arrive at PLAN time, which is the whole of issue #331's
# fix: before it, this same type was admitted, applied, and only then failed
# on ImportResourceState - a plan refusal traded for an apply refusal, with
# a real object already created. Two absences say the gate held, and both are
# asserted rather than assumed.
# internal/live/projection's own summary for a failed ImportResourceState,
# and NOT the provider's "doesn't support import" sentence: the refusal
# asserted above quotes that sentence in its own explanation, so grepping
# for it would match the very message proving the gate held.
grep -q 'Cannot import for projection' <<< "$APPLY_FULL" \
  && { printf '%s\n' "$APPLY_FULL" | head -40
       fail "the projection still reached ImportResourceState for a type with no Importer. That is the exact trade issue #331 closed: the refusal must arrive at plan time, before anything is created."; }
grep -q 'reading CloudWatch Logs Delivery' <<< "$APPLY_FULL" \
  && { printf '%s\n' "$APPLY_FULL" | head -40
       fail "the estate reached apply and hit the CloudWatch Logs Delivery floci gap (lex00/floci#79). That gap is real and still open, but it is unreachable while the plan gate refuses this estate - so reaching it means the refusal above did not stop the run, and this script's account of where the estate stops is wrong."; }

log "  it stops at PLAN time now, on one refusal: $WANT_UNADMITTED_TYPE has"
log "  no Importer in the pinned provider release, so no admission route"
log "  will take it (issue #331). Nothing was created, and the two floci"
log "  CloudWatch Logs Delivery gaps (lex00/floci#79) are real, still open,"
log "  and no longer reachable from here."
log "  This estate is still NOT crossed.                        (#331)"

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

# The shape gap this step used to pin - Cloud Control answering with a FLAT
# Properties object instead of Name nested under *Config, refused as "Listed
# resource with no readable name" - is CLOSED, verified 2026-08-19 (#300). A
# nonzero exit now means something else has moved, not the gap this script
# knows about.
if [ "$RC" -ne 0 ]; then
  grep -E '^Error|^│' <<< "$PLAN1" | head -20
  fail "the cold replan failed. The 'Listed resource with no readable name' shape gap this script used to pin was verified CLOSED on 2026-08-19 (issue #300) - Cloud Control now nests Name under *Config correctly. Something else about the corpus pin or this fork has moved; read the errors above."
fi

if grep -q 'No changes' <<< "$PLAN1"; then
  fail "the cold replan came back with no changes. That is GREAT NEWS: floci's CachePolicyConfig/OriginRequestPolicyConfig completeness gap (lex00/floci#80) has closed too, and this step should be rewritten into a real crossing (replan empty a second time, per every other script in live/e2e)."
fi

# Where it stops instead: not a refusal at all now, but a nonempty plan.
# floci's GetCachePolicy/GetOriginRequestPolicy drop nearly every field but
# Name/Comment (lex00/floci#80), so the replan sees the live object's config
# reset to zero values and proposes restoring configuration that was never
# actually lost - a false-drift update, not the empty plan a real crossing
# needs.
grep -q 'to change' <<< "$PLAN1" \
  || { printf '%s\n' "$PLAN1" | tail -40
       fail "the cold replan succeeded with an unexpected shape - not the 'N to change' false-drift plan this script now pins for lex00/floci#80, and not 'No changes' either. Something has moved; read the plan output above."; }
log "  the old shape gap is GONE: Cloud Control now nests Name under *Config"
log "  the way AWS's own schema documents, and the cold replan finds both"
log "  objects by name instead of refusing.                     (#300)"
log "  It still does not cross empty, for a DIFFERENT floci gap:"
log "  GetCachePolicy/GetOriginRequestPolicy drop nearly every field but"
log "  Name/Comment (lex00/floci#80), so the replan proposes restoring"
log "  configuration that was never actually lost against real AWS."

log ""
log "=== PASS (partial) ==="
log ""
log "The unique-name discovery leg had never touched a live cloud before"
log "today. Its own bug did: EVERY unique-name type failed its first apply"
log "unconditionally, before discovery ran at all (step 5's fix). What"
log "remains untestable against floci is the leg's actual binding behavior -"
log "step 6's floci gap (lex00/floci#80, a false-drift plan from an"
log "incomplete read) blocks it before that question is reachable."
log ""
log "The full 16-instance estate still does not cross (step 3), but no"
log "longer for the reason it used to. It spans two provider configurations -"
log "the shape AWS's own CloudFront-plus-WAF guidance produces - and that"
log "was a hard refusal before any resource was touched. Issue #283 lifted"
log "it: the estate now runs one scoped discovery pass per configuration."
log "aws_wafv2_web_acl's old listability gap is gone too (re-verified"
log "2026-08-19, #300). Where it stops now is EARLIER, not further in: one"
log "plan-time refusal of aws_iam_policy_attachment, a type the pinned"
log "provider has no Importer for at all (#331). That used to be an"
log "apply-time failure after the object was created; it is a plan refusal"
log "now, which is the direction this fork is required to move in. The"
log "CloudWatch Logs Delivery floci gap (lex00/floci#79) is still open and"
log "simply not reachable from here any more. This script measures the"
log "estate as GOV.UK wrote it, not a version restructured to fit around"
log "any of these bounds."
