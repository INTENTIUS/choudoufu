#!/usr/bin/env bash
set -uo pipefail

# The five-stage real-estate crossing pipeline (cold deploy -> migrate ->
# test plan -> test apply -> drift and reconverge), run against
# .corpus/s3-bucket/examples/complete: terraform-aws-modules/terraform-aws-
# s3-bucket's flagship example, pinned in live/corpus-manifest.json at tag
# v5.9.1. S3 buckets are among the single most common AWS resources anyone
# provisions with Terraform, and this module is the standard way to do it
# with all the trimmings - versioning, lifecycle rules, an ACL, a bucket
# policy, KMS-backed SSE, CORS, a website, intelligent tiering, a metric
# filter, object lock, transfer acceleration, request-payer, ownership
# controls. Five module calls, 32 managed instances across 15 distinct
# aws_s3_bucket_* types plus the bucket itself, applied for the first time
# against choudoufu/floci by this script.
#
# Stage shape, stricter than the older corpus-* scripts (neither
# live/e2e/corpus-iam-policy nor live/e2e/reference-ec2-vpc implements all
# five; both are boilerplate prior art, not templates to copy wholesale):
#
#   1. COLD DEPLOY   plain `terraform apply`, no live block, no choudoufu
#                     awareness at all - the honest test that the estate is
#                     real and buildable, and genuinely unmarked live infra
#                     to adopt.
#   2. MIGRATE       `choudoufu live-import -state=... -estate=... -approve`
#                     against that cold state.
#   3. TEST PLAN      state file deleted, `choudoufu live-plan`: empty, and
#                     every rendered import identity asserted as a STRING
#                     (grepped from a TF_LOG=trace run), not just the
#                     0/0/0 verdict - HANDOFF.md's own standing bar.
#   4. TEST APPLY     applying the empty plan is a genuine no-op: the live
#                     bucket count is read via the AWS CLI before and after.
#   5. DRIFT          one live object mutated out of band (the accelerate
#                     configuration this script's own floci fix, below,
#                     makes possible to create in the first place),
#                     replanned, asserted to propose fixing exactly that
#                     one instance and nothing else, then reconverged.
#
# Five real defects this estate found on first contact with a cloud. Three
# are fixed (landed ahead of this script, on this same branch); two are
# worked around with a labeled delta below, not fixed, because each is a
# separate investigation larger than this crossing:
#
#   ADMISSION GAP (fixed). aws_s3_bucket_accelerate_configuration and
#     aws_s3_bucket_request_payment_configuration had no identity row and
#     no runtime schema (the AWS provider ships no Identity Schema for
#     either, unlike their six siblings), so every instance hard-refused as
#     unadmitted-type. row-gen's own -v report already proposed both as
#     [client-named] off the doc-scraped import grammar; ratified into
#     tools/row-gen/ratified.json.
#
#   LIVE-IMPORT ADMISSION GAP (fixed). internal/live/liveimport's own
#     admission check only ever consulted identity.LookupType (the static
#     table), never the schema-based fallback internal/live/lint's admitted()
#     already applies at plan time - so live-import refused to even READ six
#     types (aws_s3_bucket_acl among them) a plain live-plan over the
#     identical configuration admits fine. Fixed in
#     internal/live/liveimport/ratify.go (admittedByProviderSchema).
#
#   FLOCI DEFECT (fixed). PUT /{bucket}?accelerate had no dispatch branch in
#     floci's S3Controller, so it fell through to the bucket-creation
#     handler and returned 409 BucketAlreadyOwnedByYou on every apply
#     against a bucket that already exists - which is every apply, since
#     aws_s3_bucket_accelerate_configuration is always created after its own
#     bucket. The identical bug the sibling ?requestPayment action had,
#     fixed earlier but never ported to this action. Fixed in lex00/floci
#     (fix/s3-accelerate-configuration, PR #53) and re-pinned in
#     live/floci-image.
#
#   CHOUDOUFU DEFECT (worked around with DELTA 3, not fixed). The module
#     names all four of its real buckets from one shared
#     `resource "random_pet" "this"`, an extremely common idiom for
#     uniquifying a bucket name. `choudoufu live-import` only ever verifies
#     and stamps TAGGABLE resources (internal/live/liveimport/ratify.go's
#     `ratifyOne`); random_pet has no tags argument at all, so it is neither
#     stamped nor - live-import never writes any state, tagged or not -
#     migrated into the estate's record_store. A later `live-plan` finding
#     no record would mint a FRESH pet name, and every bucket name the
#     config computes from it would stop matching the buckets actually
#     tagged live. DELTA 3 pins the already-applied pet value as a literal,
#     standing in for what live-import migrating RECORD_ADMITTED
#     (untaggable, effects-only) resource values into the record store
#     would do generically.
#
#   CHOUDOUFU DEFECT (worked around with DELTA 5 and DELTA 6, not fixed -
#   issue #306). Marker loss: a stamped resource's tofu-address/tofu-estate
#   tags can be silently
#   dropped from the live object by `choudoufu apply` even though the plan
#   the operator reads shows them correctly. Minimally reproduced OUTSIDE
#   this estate - two tiny .tf files, one aws_s3_bucket, one explicit
#   `tags = { Owner = "Anton" }`, no default_tags anywhere, no ForceNew
#   argument, no module: `live-import -approve` stamps it; `live-plan`
#   correctly shows Owner being added with both markers unchanged; `apply`
#   returns clean, "Resources: 0 added, 1 changed, 0 destroyed" - and the
#   live tag set afterward is `{Owner: Anton}` alone. The markers are gone.
#   HANDOFF.md's own ranking puts a lost or wrong marker above every other
#   defect class; this is that class, found live rather than argued from
#   principle. In this estate it strikes module.s3_bucket's own bucket,
#   the one call of four that declares its own tags, and cascades further
#   because expected_bucket_owner (DELTA 5's own subject: a deprecated,
#   ForceNew-on-most-of-these-types argument with no live representation,
#   a legitimate one-time residue need on its own) forces a same-apply
#   replace on that bucket's children - which the next plan, unable to
#   find their now-markerless parent, reports as needing full recreation.
#   DELTA 6 removes the explicit tags argument that triggers the defect
#   directly; DELTA 5 removes what turns it into a wider cascade.
#
#   bash live/e2e/corpus-s3-bucket-complete/run.sh
#
# Needs Docker, the AWS CLI, `terraform` on PATH (for the honest cold
# deploy - `tofu`/`choudoufu` never touches this stage), and .corpus
# populated (`just corpus-fetch`).
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4715, clear of every
#                other corpus-* and reference-* script's own default).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        set to 1 to corrupt the expected identity string before
#                the stage-3 assertion, proving it is load-bearing rather
#                than a grep that always matches.
#   DEBUG_KEEP   set to 1 to skip the exit trap: the floci container and the
#                WORK directory (both estate copies, every plan log) are
#                left behind for inspection instead of being torn down.
#
# The corpus checkout is shared across worktrees and is NEVER written to:
# the estate is copied out first (root .tf files plus examples/complete/,
# preserving the module's own `source = "../../"` relative path) and every
# delta below lands on the copy.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CORPUS_DIR="${CORPUS_DIR:-$ROOT/.corpus}"
SRC="$CORPUS_DIR/s3-bucket"
WORK="$(mktemp -d)"
PLAIN="$WORK/plain"
ESTATE="$WORK/estate"
FLOCI_PORT="${FLOCI_PORT:-4715}"
FLOCI_NAME="choudoufu-corpus-s3-bucket-complete-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
# localhost.localstack.cloud is a public wildcard-DNS name resolving every
# subdomain to 127.0.0.1. The AWS provider's S3 Control calls (which the
# provider issues even for a plain aws_s3_bucket's tag read, in v6.58+)
# address themselves at "<account-id>.<host>" - a real DNS label, not a
# path - so pointing AWS_ENDPOINT_URL at a bare 127.0.0.1 host makes every
# such call fail DNS resolution on "000000000000.127.0.0.1". This is not a
# choudoufu- or floci-specific workaround; it is the standard fix documented
# for LocalStack itself for the same reason.
ENDPOINT="http://localhost.localstack.cloud:${FLOCI_PORT}"
REGION="eu-west-1" # matches the example's own locals.region, unmodified
ESTATE_NAME="s3-bucket-complete"

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
[ -n "${DEBUG_KEEP:-}" ] || trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }

# ── 0. tools ─────────────────────────────────────────────────────────────
log "=== 0. tools ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v terraform >/dev/null 2>&1 || fail "the terraform binary is not on PATH - needed for the honest cold deploy"
[ -d "$SRC/examples/complete" ] || fail "$SRC/examples/complete is missing - run 'just corpus-fetch' first"

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

copy_estate() { # copy_estate <destdir> - root .tf files plus examples/complete/, preserving "../.." module resolution
  mkdir -p "$1/examples/complete"
  cp "$SRC"/*.tf "$1/"
  cp "$SRC/examples/complete"/*.tf "$1/examples/complete/"
}
copy_estate "$PLAIN"
copy_estate "$ESTATE"
log "  estate copied out of .corpus into $PLAIN and $ESTATE"

provider_patch() { # provider_patch <dir> - emulator wiring, the same for every phase
  python3 - "$1/examples/complete/main.tf" <<'PYEOF'
import sys
p = sys.argv[1]
s = open(p).read()
old = '''provider "aws" {
  region = local.region

  # Make it faster by skipping something
  skip_metadata_api_check     = true
  skip_region_validation      = true
  skip_credentials_validation = true
}'''
new = '''provider "aws" {
  region = local.region

  access_key                  = "test"
  secret_key                  = "test"
  skip_metadata_api_check     = true
  skip_region_validation      = true
  skip_credentials_validation = true
  s3_use_path_style           = true
}'''
assert old in s, "provider block not found - the corpus pin has moved"
open(p, "w").write(s.replace(old, new))
PYEOF
}
version_pin() { # version_pin <dir> <extra> - pin aws to the release every other e2e script pins (#269), optionally appending a live block
  cat > "$1/examples/complete/versions.tf" <<EOF
terraform {
  required_version = ">= 1.5.7"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
    random = {
      source  = "hashicorp/random"
      version = ">= 2.0"
    }
  }
$2
}
EOF
}

export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" AWS_ENDPOINT_URL="$ENDPOINT"

# ── 1. floci ─────────────────────────────────────────────────────────────
log "=== 1. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "http://127.0.0.1:${FLOCI_PORT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"s3"' <<< "${HEALTH:-}" && break
  sleep 2
done
grep -q '"s3"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (s3) at $ENDPOINT"
log "  healthy"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 1: COLD DEPLOY - plain terraform, no live block, no choudoufu at all
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 1: cold deploy (plain terraform, unmodified estate) ==="
provider_patch "$PLAIN"
version_pin "$PLAIN" ""
( cd "$PLAIN/examples/complete" && terraform init -upgrade -input=false -no-color >/dev/null 2>&1 ) \
  || { ( cd "$PLAIN/examples/complete" && terraform init -upgrade -input=false -no-color 2>&1 | tail -30 ); fail "plain terraform init failed"; }
PLAIN_APPLY="$(cd "$PLAIN/examples/complete" && terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$PLAIN_APPLY" | grep -E '^Error' -A5 | head -60
  fail "the cold-deploy apply failed"; }
grep -qE 'Apply complete! Resources: 32 added' <<< "$PLAIN_APPLY" \
  || { grep -E 'Apply complete' <<< "$PLAIN_APPLY"; fail "the cold-deploy apply did not create exactly 32 resources - the corpus pin or the module's own conditionals have moved"; }
log "  $(grep -E 'Apply complete' <<< "$PLAIN_APPLY" | head -1)"

[ -f "$PLAIN/examples/complete/terraform.tfstate" ] || fail "plain terraform left no state file to migrate from"
read -r PET ROLE_NAME KMS_KEY_ID <<< "$(python3 -c "
import json
d = json.load(open('$PLAIN/examples/complete/terraform.tfstate'))
pet = role = key = ''
for r in d['resources']:
    if r.get('module') is not None:
        continue
    if r['type'] == 'random_pet' and r['name'] == 'this':
        pet = r['instances'][0]['attributes']['id']
    elif r['type'] == 'aws_iam_role' and r['name'] == 'this':
        role = r['instances'][0]['attributes']['name']
    elif r['type'] == 'aws_kms_key' and r['name'] == 'objects':
        key = r['instances'][0]['attributes']['key_id']
print(pet, role, key)
")"
[ -n "$PET" ] && [ -n "$ROLE_NAME" ] && [ -n "$KMS_KEY_ID" ] \
  || fail "could not read random_pet.this.id, aws_iam_role.this.name or aws_kms_key.objects.key_id back out of the plain state"
log "  random_pet.this.id = $PET (the four bucket names all derive from this)"
log "  aws_iam_role.this = $ROLE_NAME, aws_kms_key.objects = $KMS_KEY_ID"

BUCKETS=(s3-bucket-$PET logs-$PET cloudfront-logs-$PET simple-$PET)
for b in "${BUCKETS[@]}"; do
  awsl s3api head-bucket --bucket "$b" >/dev/null 2>&1 || fail "bucket $b does not exist live after the cold apply"
done
log "  4 buckets confirmed live: ${BUCKETS[*]}"
# The taggable roster this estate actually has: the four buckets plus the
# IAM role and the KMS key (aws_iam_role.this and aws_kms_key.objects, both
# root-level, outside every module). Every OTHER instance in this estate -
# 26 of the 32 - is an untaggable S3 sub-resource whose identity is
# parent-derived from one of the four buckets.
KNOWN_ROOTS=("${BUCKETS[@]}" "$ROLE_NAME" "$KMS_KEY_ID")

UNMARKED="$(awsl s3api get-bucket-tagging --bucket "s3-bucket-$PET" 2>&1)"
if grep -qF 'tofu-address' <<< "$UNMARKED"; then
  fail "the plain-terraform bucket already carries a tofu-address tag before migration - this test proves nothing"
fi
log "  confirmed unmarked: s3-bucket-$PET carries no tofu-address tag (${UNMARKED:0:40}...)"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 2: choudoufu live-import ==="
provider_patch "$ESTATE"

# DELTA 1+2, ordinary onboarding: pin the provider and add the live block.
version_pin "$ESTATE" '
  live {
    estate = "'"$ESTATE_NAME"'"
    record_store "local" {
      path = ".tofu-records"
    }
  }'
log "  DELTA 1+2  provider pinned = 6.58.0, live block added   (onboarding, #269)"

# DELTA 3, the untaggable-effects gap this script's header names: pin the
# already-applied random_pet value as a literal, standing in for what
# migrating RECORD_ADMITTED resource values into the record store would do.
perl -0pi -e 's/resource "random_pet" "this" \{\n  length = 2\n\}\n/locals {\n  pinned_pet = "'"$PET"'" # DELTA 3: live-import does not migrate untaggable effects resources\n}\n/' "$ESTATE/examples/complete/main.tf"
perl -pi -e 's/random_pet\.this\.id/local.pinned_pet/g' "$ESTATE/examples/complete/main.tf"
grep -q 'pinned_pet = "'"$PET"'"' "$ESTATE/examples/complete/main.tf" || fail "DELTA 3 did not pin the random_pet value - the corpus pin has moved"
[ "$(grep -c 'local.pinned_pet' "$ESTATE/examples/complete/main.tf")" = "4" ] \
  || fail "DELTA 3 reached $(grep -c 'local.pinned_pet' "$ESTATE/examples/complete/main.tf") references, expected 4"
grep -q 'resource "random_pet"' "$ESTATE/examples/complete/main.tf" && fail "DELTA 3 left the random_pet resource behind"
log "  DELTA 3    random_pet pinned to $PET               (CHOUDOUFU GAP, see header)"

# DELTA 4, PARITY: two data source reads feed module.cloudfront_log_bucket's
# "grant" argument, which count-gates aws_s3_bucket_acl.this - and every
# count expression has to be statically evaluable from var/local/path/
# terraform alone (HANDOFF.md's own standing bar); a data source read is
# not in that set, on purpose, because its value is only known once a live
# call has actually been made. That is real, current parity with the
# config-language subset this fork accepts - not a workaround for a
# choudoufu defect - so it stays a delta and does not get its own "GAP" in
# the header the way DELTA 3 does. One of the two reads is
# account-canonical (aws_canonical_user_id.current.id: this account's own
# S3 canonical user ID) and the other is the fixed, AWS-documented
# CloudFront log-delivery constant every account shares - both pinned here
# to the exact values floci itself returned for this run.
CANONICAL_USER_ID="$(awsl s3api list-buckets --query 'Owner.ID' --output text)"
[ -n "$CANONICAL_USER_ID" ] && [ "$CANONICAL_USER_ID" != "None" ] || fail "could not read the account's own canonical user ID off floci"
CLOUDFRONT_CANONICAL_USER_ID="c4c1ede66af53448b93c283ce9448c4ba468c9432aa01d700d3878632f77d2d0" # AWS-documented constant, every account
perl -0pi -e 's/(grant = \[\{\n    type       = "CanonicalUser"\n    permission = "FULL_CONTROL"\n    id         = )data\.aws_canonical_user_id\.current\.id(\n    \}, \{\n    type       = "CanonicalUser"\n    permission = "FULL_CONTROL"\n    id         = )data\.aws_cloudfront_log_delivery_canonical_user_id\.cloudfront\.id( # Ref\.[^\n]*\n    \}\n  \])/$1"'"$CANONICAL_USER_ID"'" # DELTA 4$2"'"$CLOUDFRONT_CANONICAL_USER_ID"'" # DELTA 4$3/' "$ESTATE/examples/complete/main.tf"
[ "$(grep -c 'DELTA 4' "$ESTATE/examples/complete/main.tf")" = "2" ] \
  || fail "DELTA 4 reached $(grep -c 'DELTA 4' "$ESTATE/examples/complete/main.tf") sites, expected 2 - the corpus pin has moved"
grep -qF "data.aws_canonical_user_id.current.id" "$ESTATE/examples/complete/main.tf" \
  || fail "DELTA 4 removed every reference to aws_canonical_user_id - the owner block's own use of it should be untouched"
log "  DELTA 4    2 data-source reads in a count-gating grant pinned to literals (PARITY, not a defect)"

# DELTA 5 and DELTA 6 both work around the SAME choudoufu defect - marker
# loss - this script's header names, minimally reproduced (two .tf files,
# one resource, one explicit tag, no default_tags, no ForceNew argument
# anywhere) OUTSIDE this estate entirely before either delta was written:
# choudoufu apply's own internal re-plan of a stamped resource that
# declares its own explicit "tags" argument writes only the config's
# AS-WRITTEN tags to the live object, dropping the markers stamp.Stamp
# injected for the plan everyone actually READS. The plan shown to a human
# is correct; what apply sends to the provider is not. See this run's own
# tracker issue for the full minimal repro.
#
# DELTA 5 removes expected_bucket_owner (a deprecated, ForceNew-on-most-of-
# these-types argument the provider is dropping, and itself a legitimate
# create-time-only-value residue need with no live representation - same
# shape as DELTA 3's random_pet). Its own replace is not the defect by
# itself; what makes it destructive here is that the SAME apply that
# forces it also updates the parent bucket's tags, loses the parent's
# marker to the defect above, and the next plan - unable to find the
# bucket it just updated - cascades into "must be replaced" across every
# sub-resource that depends on it.
perl -pi -e 's/^(  expected_bucket_owner                  = data\.aws_caller_identity\.current\.account_id)$/  # DELTA 5 (CHOUDOUFU GAP, see header): expected_bucket_owner removed - $1/' "$ESTATE/examples/complete/main.tf"
grep -q 'DELTA 5' "$ESTATE/examples/complete/main.tf" || fail "DELTA 5 did not match the s3_bucket module's expected_bucket_owner line - the corpus pin has moved"
log "  DELTA 5    expected_bucket_owner removed from module s3_bucket   (CHOUDOUFU GAP, see header)"

# DELTA 6: module.s3_bucket is the one call among the four that sets its
# own explicit tags = { Owner = "Anton" }, which is exactly the shape the
# minimal repro above isolates - and losing THIS bucket's markers is what
# triggers DELTA 5's cascade, independent of expected_bucket_owner. Removed
# here for the same reason DELTA 5 is: real, current, not yet fixed.
perl -0pi -e 's/(\n  tags = \{\n    Owner = "Anton"\n  \}\n)/\n  # DELTA 6 (CHOUDOUFU GAP, see header): tags removed - explicit tags = { Owner = "Anton" }\n/' "$ESTATE/examples/complete/main.tf"
grep -q 'DELTA 6' "$ESTATE/examples/complete/main.tf" || fail "DELTA 6 did not match module.s3_bucket's tags argument - the corpus pin has moved"
log "  DELTA 6    tags removed from module s3_bucket                    (CHOUDOUFU GAP, see header)"

( cd "$ESTATE/examples/complete" && "$TOFU" init -upgrade -input=false -no-color >/dev/null 2>&1 ) \
  || { ( cd "$ESTATE/examples/complete" && "$TOFU" init -upgrade -input=false -no-color 2>&1 | tail -30 ); fail "estate init failed"; }

log "=== STAGE 2a: live-import, read-only first ==="
IMPORT_OUT="$(cd "$ESTATE/examples/complete" && "$TOFU" live-import -state="$PLAIN/examples/complete/terraform.tfstate" -estate="$ESTATE_NAME" 2>&1)" || {
  printf '%s\n' "$IMPORT_OUT" | tail -40; fail "live-import (dry run) failed"; }
grep -qF "6 of 32 resource instance(s) are eligible for stamping" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT" | head -60; fail "live-import did not verify exactly 6 of 32 resource instances as eligible - only the four aws_s3_bucket instances plus aws_iam_role.this and aws_kms_key.objects carry a tags argument, everything else in this estate is untaggable"; }
grep -qF "No tag has been written." <<< "$IMPORT_OUT" \
  || fail "the dry run wrote a tag - it must not"
log "  6 of 32 verified against the live system (the four buckets, the IAM role, the KMS key - everything else here is untaggable and admits parent-derived); nothing written yet"

log "=== STAGE 2b: -approve ==="
APPROVE_OUT="$(cd "$ESTATE/examples/complete" && "$TOFU" live-import -state="$PLAIN/examples/complete/terraform.tfstate" -estate="$ESTATE_NAME" -approve 2>&1)" || {
  printf '%s\n' "$APPROVE_OUT" | tail -40; fail "live-import -approve failed"; }
grep -qF "6 resource(s) newly stamped, 0 already stamped, 0 failed, 26 skipped" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not stamp exactly 6 resources cleanly (26 skipped: the untaggable, parent-derived S3 sub-resources plus random_pet)"; }
log "  6 stamped"

for b in "${BUCKETS[@]}"; do
  ADDR="$(awsl s3api get-bucket-tagging --bucket "$b" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text 2>/dev/null)"
  EST="$(awsl s3api get-bucket-tagging --bucket "$b" --query "TagSet[?Key=='tofu-estate'].Value | [0]" --output text 2>/dev/null)"
  [ "$EST" = "$ESTATE_NAME" ] || fail "bucket $b carries tofu-estate=$EST, not $ESTATE_NAME"
  [ -n "$ADDR" ] && [ "$ADDR" != "None" ] || fail "bucket $b carries no tofu-address"
  log "  $b -> $ADDR"
done

log "=== STAGE 2c: classify residue (one settling apply) ==="
# Not part of the five-stage shape and not a defect: a handful of this
# estate's arguments (force_destroy, skip_destroy,
# bypass_policy_lockout_safety_check among them) are client-supplied and
# create-time-only, with nothing in the provider's schema a live Read call
# recovers. Ordinary Terraform state remembers them for free; this fork's
# discovery-rebuilt prior does not, until an apply through the estate's
# own record_store classifies each into residue - the exact shape
# live/e2e/corpus-crossing/run.sh's own "allow_overwrite" step names and
# pins for a different estate. The plan below is legitimately non-empty
# for that reason alone; Stage 3 is the plan that has to be.
plan_into() {
  rm -f "$ESTATE/examples/complete/terraform.tfstate" "$ESTATE/examples/complete/terraform.tfstate.backup"
  ( cd "$ESTATE/examples/complete" && TF_LOG=trace "$TOFU" live-plan -input=false -no-color ) > "$1" 2>&1
  return $?
}
plan_into "$WORK/plan-residue.log" || { grep -vE '^[0-9]{4}-' "$WORK/plan-residue.log" | tail -40; fail "the residue-classification live-plan exited non-zero"; }
grep -qE '^Plan:' "$WORK/plan-residue.log" || { cat "$WORK/plan-residue.log" | tail -20; fail "no plan summary line"; }
log "  $(grep -E '^Plan:' "$WORK/plan-residue.log")"
APPLY_RESIDUE="$(cd "$ESTATE/examples/complete" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY_RESIDUE" | grep -E '^Error' -A5 | head -60; fail "the residue-classification apply failed"; }
grep -qE '^Apply complete! Resources: 0 added' <<< "$APPLY_RESIDUE" \
  || { grep -E 'Apply complete' <<< "$APPLY_RESIDUE"; fail "the residue-classification apply added or destroyed a resource - it should only ever change"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY_RESIDUE" | head -1)"
for b in "${BUCKETS[@]}"; do
  EST="$(awsl s3api get-bucket-tagging --bucket "$b" --query "TagSet[?Key=='tofu-estate'].Value | [0]" --output text 2>/dev/null)"
  [ "$EST" = "$ESTATE_NAME" ] || fail "bucket $b lost its tofu-estate marker during the residue-classification apply (got \"$EST\") - this is the CHOUDOUFU DEFECT the header names, and DELTA 6 exists precisely to keep it from firing here"
done
log "  all four buckets' markers survived the classification apply"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - no state file, live-plan, empty, and the identities
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 3: no state file, live-plan ==="
plan_into "$WORK/plan1.log" || { grep -vE '^[0-9]{4}-' "$WORK/plan1.log" | tail -40; fail "live-plan exited non-zero"; }
[ ! -f "$ESTATE/examples/complete/terraform.tfstate" ] || fail "live-plan wrote a state file"
if grep -qE 'No changes|Plan: 0 to add, 0 to change, 0 to destroy' "$WORK/plan1.log"; then
  log "  0 to add, 0 to change, 0 to destroy"
else
  # KNOWN, NOT YET FIXED (issue #306): two arguments -
  # the canned "acl" string and website.routing_rules - do not settle even
  # after the Stage 2c residue-classification apply, unlike force_destroy/
  # skip_destroy/bypass_policy_lockout_safety_check, which that same apply
  # DOES classify cleanly. Both are ordinary Optional, non-WriteOnly,
  # non-Sensitive arguments (internal/live/lint's CheckResidueAttributes
  # never warns on either), so the provider's own Read() implementation for
  # each apparently needs a genuinely-remembered prior to round-trip them,
  # which a pure-discovery stateless run cannot supply. This is asserted
  # PRECISELY - the exact two sites, nothing more - rather than hidden,
  # because a script that fails silently by exit code is worse than one
  # that names what remains.
  grep -qE '^Plan: 0 to add, 2 to change, 0 to destroy' "$WORK/plan1.log" \
    || { grep -vE '^[0-9]{4}-' "$WORK/plan1.log" | grep -E '^Plan:|^  #' | head -20
         fail "the plan is not empty, and not the exact known-remaining shape (0 add/2 change/0 destroy) either - something new has moved"; }
  KNOWN_SITES="$(grep -vE '^[0-9]{4}-' "$WORK/plan1.log" | grep -E '^  # .* will be updated in-place')"
  echo "$KNOWN_SITES" | grep -qF 'module.s3_bucket.aws_s3_bucket_acl.this[0]' \
    && echo "$KNOWN_SITES" | grep -qF 'module.s3_bucket.aws_s3_bucket_website_configuration.this[0]' \
    || { echo "$KNOWN_SITES"; fail "the 2 remaining changes are not the known acl/website_configuration pair - something new has moved"; }
  log "  BLOCKED (CHOUDOUFU GAP, see header and issue #306): exactly the known"
  log "  2 sites remain non-empty - module.s3_bucket.aws_s3_bucket_acl.this[0] and"
  log "  module.s3_bucket.aws_s3_bucket_website_configuration.this[0] - and nothing else has moved."
  log "  Stopping here rather than forcing stages 4-5 past a plan this script cannot honestly call empty."
  log ""
  log "=== PARTIAL: stages 1, 2, 2c verified; stage 3 blocked as described above ==="
  exit 1
fi

# The value, not the verdict: every rendered import identity, read off the
# TF_LOG=trace projection log, must NAME one of the six taggable roots
# (KNOWN_ROOTS: the four buckets, the IAM role, the KMS key) - as the whole
# identity (the taggable types themselves) or as the leading component
# before a ":" or "," (every parent-derived S3 sub-resource type).
grep -oE 'from import identity "[^"]*"' "$WORK/plan1.log" | sed 's/.*identity "//; s/"$//' | sort -u > "$WORK/ids"
N_IDS="$(grep -c . "$WORK/ids")"
[ "$N_IDS" -ge 30 ] || { cat "$WORK/ids"; fail "only $N_IDS rendered identities, expected close to 32 (one per managed AWS instance)"; }
log "  $N_IDS rendered import identities"

# ids_all_name_known_roots <idfile> -> 0 every line's leading component is
# one of $KNOWN_ROOTS, 1 otherwise (prints the offending line[s]).
ids_all_name_known_roots() {
  local idfile="$1" rc=0 id root_part match r
  while read -r id; do
    [ -n "$id" ] || continue
    root_part="${id%%[:,]*}"
    match=0
    for r in "${KNOWN_ROOTS[@]}"; do [ "$root_part" = "$r" ] && match=1; done
    [ "$match" = "1" ] || { echo "  \"$id\" names no known live object ($root_part)"; rc=1; }
  done < "$idfile"
  return "$rc"
}

ids_all_name_known_roots "$WORK/ids" \
  || fail "at least one rendered identity names no known live object (see above) - the plan was EMPTY when this fired, which is the whole reason this stage reads the strings rather than trusting the verdict"
log "  every rendered identity names one of the six taggable roots"

# Negative control: the SAME function, over the SAME captured identities,
# with exactly one real root swapped for one that does not exist - proving
# ids_all_name_known_roots actually discriminates rather than matching
# anything it is handed. Runs every time (not just under BREAK=1) because
# it costs nothing extra: no new tofu or AWS CLI call, just a second pass
# over data already on disk.
sed "s/^${BUCKETS[0]}/not-a-real-bucket-name/" "$WORK/ids" > "$WORK/ids-corrupted"
if ids_all_name_known_roots "$WORK/ids-corrupted" > "$WORK/ids-corrupted.out" 2>&1; then
  fail "the identity check PASSED on a corrupted identity set (one real bucket name replaced with a fake one) - this stage's assertion is not load-bearing"
fi
grep -qF 'not-a-real-bucket-name' "$WORK/ids-corrupted.out" \
  || { cat "$WORK/ids-corrupted.out"; fail "the corrupted-identity check failed, but not on the string this control corrupted"; }
log "  negative control: the same check rejects a corrupted identity set, naming the bad string"

if [ "${BREAK:-}" = "1" ]; then
  # BREAK=1 flips the control above from a self-check into the reported
  # failure, proving this script's own exit code responds to it rather than
  # the control being dead code nothing ever runs.
  fail "BREAK=1: treating the negative control above as the run's own result, to prove this script's exit code is not vacuously 0"
fi

# ══════════════════════════════════════════════════════════════════════════
# STAGE 4: TEST APPLY - the empty plan applies as a genuine no-op
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 4: apply the empty plan ==="
BEFORE="$(awsl s3api list-buckets --query 'length(Buckets)' --output text)"
rm -f "$ESTATE/examples/complete/terraform.tfstate" "$ESTATE/examples/complete/terraform.tfstate.backup"
APPLY4="$(cd "$ESTATE/examples/complete" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY4" | tail -40; fail "the stage-4 apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY4" \
  || { grep -E 'Apply complete|No changes' <<< "$APPLY4"; fail "the stage-4 apply was not a no-op"; }
AFTER="$(awsl s3api list-buckets --query 'length(Buckets)' --output text)"
[ "$BEFORE" = "$AFTER" ] || fail "bucket count moved from $BEFORE to $AFTER across a no-op apply"
log "  $(grep -E 'Apply complete|No changes' <<< "$APPLY4" | head -1); bucket count unchanged at $BEFORE"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 5: DRIFT AND RECONVERGE
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 5: drift one object out of band, replan, reconverge ==="
# The configuration declares acceleration_status = "Suspended" for
# s3-bucket-$PET (aws_s3_bucket_accelerate_configuration, admitted by this
# script's own row-gen ratification and only creatable at all because of
# this script's own floci fix). Flip it to Enabled directly against floci -
# never through choudoufu - and expect exactly that one instance to show
# an in-place update.
awsl s3api put-bucket-accelerate-configuration --bucket "s3-bucket-$PET" --accelerate-configuration Status=Enabled \
  || fail "seeding drift via the AWS CLI failed"
DRIFTED="$(awsl s3api get-bucket-accelerate-configuration --bucket "s3-bucket-$PET" --query 'Status' --output text)"
[ "$DRIFTED" = "Enabled" ] || fail "the out-of-band mutation did not take: accelerate status reads $DRIFTED, not Enabled"
log "  drifted s3-bucket-$PET's accelerate status to Enabled, out of band"

plan_into "$WORK/plan-drift.log" || { grep -vE '^[0-9]{4}-' "$WORK/plan-drift.log" | tail -40; fail "the drift live-plan exited non-zero"; }
grep -qE '^Plan: 0 to add, 1 to change, 0 to destroy' "$WORK/plan-drift.log" \
  || { grep -E '^Plan:|^No changes' "$WORK/plan-drift.log"; fail "expected exactly 1 in-place update, the drifted accelerate configuration"; }
CHANGED="$(grep -oE '^  # [^ ]+ will be updated in-place' "$WORK/plan-drift.log" | awk '{print $2}')"
[ "$CHANGED" = "module.s3_bucket.aws_s3_bucket_accelerate_configuration.this[0]" ] \
  || fail "the plan proposes fixing $CHANGED, not the drifted accelerate configuration - the diff is not surgical"
log "  the diff proposes fixing exactly $CHANGED and nothing else"

APPLY5="$(cd "$ESTATE/examples/complete" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY5" | tail -40; fail "the reconverging apply failed"; }
grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$APPLY5" \
  || { grep -E 'Apply complete' <<< "$APPLY5"; fail "the reconverging apply did not show exactly 1 changed"; }
RECONVERGED="$(awsl s3api get-bucket-accelerate-configuration --bucket "s3-bucket-$PET" --query 'Status' --output text)"
[ "$RECONVERGED" = "Suspended" ] || fail "after reconverging, accelerate status reads $RECONVERGED, not Suspended - the config's own value"
log "  reconverged: accelerate status is back to Suspended, the estate's own declared value"

plan_into "$WORK/plan-final.log" || fail "the final live-plan exited non-zero"
grep -qE 'No changes|Plan: 0 to add, 0 to change, 0 to destroy' "$WORK/plan-final.log" \
  || { grep -vE '^[0-9]{4}-' "$WORK/plan-final.log" | grep -E '^  #' | head -20; fail "the final plan is not empty"; }
log "  final plan: 0 to add, 0 to change, 0 to destroy"

log ""
log "=== PASS ==="
log ""
log "terraform-aws-modules/terraform-aws-s3-bucket's complete example: 32"
log "instances across 5 module calls and 15 aws_s3_bucket_* types, cold-"
log "deployed with plain terraform, migrated with live-import, stripped of"
log "its state file and replanned empty with every identity checked against"
log "a live AWS CLI read, applied as a genuine no-op, drifted on one"
log "instance and reconverged."
log ""
log "Five real defects found on first contact with a cloud, three fixed"
log "(two admission gaps, ratified and merged into the schema-based"
log "fallback; a floci routing bug, PR #53) and two worked around, not"
log "fixed (random_pet migration via DELTA 3; marker loss on apply via"
log "DELTA 5 and DELTA 6) - see the header comment above and this run's"
log "issue #306."
