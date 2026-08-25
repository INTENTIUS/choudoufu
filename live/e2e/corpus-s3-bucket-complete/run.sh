#!/usr/bin/env bash
set -uo pipefail

# The five-stage real-estate crossing pipeline (cold deploy -> migrate ->
# test plan -> test apply -> drift and reconverge), run against
# .corpus/s3-bucket/examples/complete: terraform-aws-modules/terraform-aws-
# s3-bucket's flagship example, pinned in live/corpus-manifest.json at tag
# v5.9.1. S3 buckets are among the single most common AWS resources anyone
# provisions with Terraform, and this module is the standard way to do it
# with all the trimmings - versioning, lifecycle rules, an ACL, a bucket
# policy, KMS-backed SSE, CORS, intelligent tiering, a metric filter,
# object lock, transfer acceleration, request-payer, ownership controls.
# Five module calls, 30 managed instances across 14 distinct
# aws_s3_bucket_* types plus the bucket itself, applied for the first time
# against choudoufu/floci by this script. (The upstream example also
# configures a canned ACL and a website on module.s3_bucket specifically;
# both are scoped out here - see SCOPE REDUCTION below.)
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
#   6. RENAME         (day2_rename, live/GAUNTLET.md #6) module.log_bucket
#                     renamed to module.log_bucket_renamed through a `moved`
#                     block; module.simple_bucket renamed to
#                     module.simple_bucket_renamed through `choudoufu live-mv`
#                     with no moved block at all. Both zero churn: the
#                     bucket's own tofu-address marker is rewritten in place,
#                     nothing is created or destroyed, and a further plan is
#                     empty. module.cloudfront_log_bucket and module.s3_bucket
#                     are left untouched as negative-control anchors.
#
# Six real defects this estate found on first contact with a cloud. Four
# are fixed (three landed ahead of this script on this same branch; the
# fourth, #306, fixed and re-verified 2026-08-18 - see below). One, #340,
# is fixed for the record-store half (random_pet's value now migrates
# generically) but DELTA 3 stays, pending the separate, unverified
# question of whether the config can read that value back for an
# identity-bearing argument on a stateless replan without it - see below.
# The sixth -
# the acl/website_configuration gap, which the original investigation
# attributed to #306 too but is actually a separate mechanism (see below) -
# is genuinely structural rather than quick-fixable, so this script scopes
# those two arguments OUT of module.s3_bucket (SCOPE REDUCTION, below)
# rather than asserting-and-stopping on them:
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
#   CHOUDOUFU DEFECT (HALF FIXED 2026-08-20 - issue #340, closed; DELTA 3
#     still kept, see below). The module names all four of its real buckets
#     from one shared `resource "random_pet" "this"`, an extremely common
#     idiom for uniquifying a bucket name. This originally read: "choudoufu
#     live-import only ever verifies and stamps TAGGABLE resources, so
#     random_pet is neither stamped nor migrated into the estate's
#     record_store" - #340 fixed exactly that generically
#     (internal/live/liveimport/stamp.go's recordOne, for every
#     RecordBacked type, not just this one): `-approve` now seeds the
#     estate's record store from the state's own random_pet.this object as
#     part of an ordinary migrate (STAMPED/RECORDED/SKIPPED counts below
#     reflect this - 1 newly recorded, 23 skipped, not 0/24 as this script
#     asserted before this pass). Re-verified live this pass: the
#     migrate-stage log below shows "RECORDED (1) ... random_pet.this ...
#     Wrote the state's own object into this estate's record store."
#     DELTA 3 is still kept, not because that half is unfixed, but because
#     the OTHER half - whether the estate's own config can then read that
#     recorded value back to compute module.s3_bucket's bucket-name
#     argument (`"s3-bucket-${random_pet.this.id}"`) on a stateless replan,
#     with no DELTA-3 literal standing in for it - was tried in this pass
#     and hit a distinct failure at the stage 2c residue-classification
#     plan ("no plan summary line") not root-caused here; see issue #336
#     (closed, but for a narrower coalesce()-shaped cause than this
#     estate's plain interpolation) for the closest prior art. Removing
#     DELTA 3 is a separate, unverified unit, not this one.
#
#   CHOUDOUFU DEFECT (FIXED, 2026-08-18 - issue #306, closed). Marker loss:
#   a stamped resource's tofu-address/tofu-estate tags could be silently
#   dropped from the live object by `choudoufu apply` even though the plan
#   the operator read showed them correctly. Root-caused as a floci bug,
#   not choudoufu's: real AWS S3 Control TagResource is a merge/upsert,
#   floci's implementation did a full replace, and terraform-provider-aws
#   (v6.58+) sends only the changed tag on an incremental update, trusting
#   merge semantics - so any tag not part of that one delta, including both
#   markers, was silently deleted. Fixed in lex00/floci
#   (S3ControlController.tagResource now reads-merges-writes, mirroring
#   untagResource), reconciled into the currently-pinned floci image.
#   internal/live/lifecycle/marker_tag_merge_live_test.go's
#   TestMarkerSurvivesIncrementalTagUpdate pins the regression and passes
#   live against it. DELTA 6, which worked around this by removing
#   module.s3_bucket's own explicit tags argument, is RETIRED below -
#   re-verified against the fixed image (2026-08-18) with tags restored to
#   its real upstream value: the residue-classification apply in stage 2c
#   leaves all four buckets' markers intact. DELTA 5 (expected_bucket_owner)
#   is NOT retired - it stays, unrelated to #306. expected_bucket_owner is
#   ForceNew on most of these sub-resource types and has no live
#   representation at all (it is a request-header assertion, not a stored
#   property GetBucketAcl/GetBucketWebsite/etc. can ever answer), so a
#   discovery-rebuilt prior with nothing for it would force a real replace
#   of module.s3_bucket's children on the very first onboarding apply -
#   the same shape as DELTA 3's random_pet, independent of marker survival.
#   Reverting DELTA 5 was out of this pass's scope (only DELTA 6 was asked
#   for) and re-litigating it needs its own investigation of whether #275's
#   residue mechanism can be taught to run BEFORE a ForceNew argument's
#   first apply rather than only after, not assumed safe from #306 alone.
#
#   CHOUDOUFU GAP (not fixed - genuinely structural, scoped out below
#   rather than worked around). Two arguments on module.s3_bucket - the
#   canned `acl` and `website.routing_rule` - never converge under stateless
#   discovery even after #306's fix and even after the stage 2c residue-
#   classification apply that DOES settle force_destroy, deletion_window_
#   in_days and five others on this same estate. Traced past the point the
#   original investigation reached (which stopped at "the provider's Read()
#   needs a genuinely-remembered prior"): issue #275's residue mechanism
#   (internal/live/projection/residue.go) DOES run for this instance and
#   DOES try to classify `acl` - TF_LOG=trace on the stage 2c apply shows
#   `residue candidate "acl": readA=cty.StringVal("private")
#   readB=cty.StringVal("private") applied=cty.StringVal("private")` - and
#   its own documented rule (`if !av.IsNull() && av.RawEquals(want) {
#   continue }`, residue.go's classifyResidue) correctly reads that as "the
#   provider answers this from the remote", so nothing is recorded as
#   residue. Yet the SAME attribute, read moments later by
#   internal/live/projection/build.go's importAndRead (the function stage
#   3's plan actually uses), comes back empty and proposes `+ acl =
#   "private"`. The two reads disagree because their PRIOR is built two
#   different ways for the exact same attribute: residue.go's identityOnly
#   nulls every non-identity attribute outright (cty.NullVal), while
#   importAndRead's prior is whatever provider.ImportResourceState()
#   returned - which for this SDKv2 resource is a zero-value stub (`acl =
#   ""`, not null). The provider's own Read() apparently treats an
#   explicit null differently from an SDKv2 zero-value string for this
#   attribute, which is provider (SDKv2 shim) behavior neither prior
#   construction is wrong to assume on its own. Reconciling the two prior
#   constructions is a real fix, but importAndRead is the read path EVERY
#   projected resource in this fork goes through, so a change there needs
#   validation across the whole corpus, not just this estate - out of scope
#   for this crossing. Scoped out below (SCOPE REDUCTION) rather than
#   asserted-and-stopped, the same discipline
#   live/e2e/corpus-sumaform-aws/run.sh uses for its own two structural
#   refusals.
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
#                than a grep that always matches. Set to 6 to exercise the
#                stage-6 (day2_rename) negative control instead: renames
#                module.simple_bucket with no moved block and no live-mv,
#                proving choudoufu proposes a plain CREATE for the new
#                address rather than the zero-churn rename the real checks
#                assert (a distinct value from 1 because stage 3's own
#                BREAK=1 control fails and exits before stage 6 ever runs).
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

# The gauntlet protocol (live/GAUNTLET.md): each stage reports its verdict on
# stdout so tools/gauntlet records it. CURRENT_STAGE names the stage a
# failure belongs to; fail() reports it before exiting.
# shellcheck source=live/e2e/lib/gauntlet.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/lib/gauntlet.sh"
CURRENT_STAGE=""
fail() {
  printf 'FAIL: %s\n' "$*" >&2
  if [ -n "$CURRENT_STAGE" ]; then gauntlet_stage "$CURRENT_STAGE" fail "$*"; fi
  exit 1
}
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }
gauntlet_begin

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

# SCOPE REDUCTION (2026-08-18, see header): module.s3_bucket's own `acl`
# and `website` inputs are removed, applied to BOTH copies so the crossing
# tests one genuinely-reduced estate throughout rather than having
# choudoufu silently stop managing something the cold deploy still created.
# This is not a CHOUDOUFU GAP delta (nothing here works around a defect in
# a resource that stays in the estate) - it drops two resources from the
# estate entirely, the same kind of deliberate reduction
# live/e2e/corpus-sumaform-aws/run.sh applies to its own estate before any
# stage runs.
scope_reduce() { # scope_reduce <dir>
  python3 - "$1/examples/complete/main.tf" <<'PYEOF'
import sys
p = sys.argv[1]
s = open(p).read()

old_acl = '  acl = "private" # "acl" conflicts with "grant" and "owner"\n'
assert old_acl in s, "SCOPE REDUCTION did not match module.s3_bucket's acl line - the corpus pin has moved"
s = s.replace(old_acl, '  # SCOPE REDUCTION (CHOUDOUFU GAP, see header): acl removed - ' + old_acl.strip() + '\n', 1)

start_marker = '  website = {\n'
i = s.index(start_marker)
# Find the matching close: the next line that is exactly "  }\n" at the
# same two-space indent as "  website = {" - the block's own brace nesting
# is all indented deeper than that, so the first two-space "  }\n" after
# the start is the block's own close.
end_marker = '\n  }\n'
j = s.index(end_marker, i)
end = j + len(end_marker)
block = s[i:end]
assert block.count('routing_rules') == 1, "SCOPE REDUCTION did not isolate exactly one website block - the corpus pin has moved"
s = s[:i] + '  # SCOPE REDUCTION (CHOUDOUFU GAP, see header): website removed (index_document/error_document/2 routing_rules)\n' + s[end:]

open(p, "w").write(s)
PYEOF
}
scope_reduce "$PLAIN"
scope_reduce "$ESTATE"
log "  SCOPE      acl and website removed from module s3_bucket in BOTH copies (CHOUDOUFU GAP, see header)"

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
CURRENT_STAGE=cold_deploy
log "=== STAGE 1: cold deploy (plain terraform, unmodified estate) ==="
provider_patch "$PLAIN"
version_pin "$PLAIN" ""
( cd "$PLAIN/examples/complete" && terraform init -upgrade -input=false -no-color >/dev/null 2>&1 ) \
  || { ( cd "$PLAIN/examples/complete" && terraform init -upgrade -input=false -no-color 2>&1 | tail -30 ); fail "plain terraform init failed"; }
PLAIN_APPLY="$(cd "$PLAIN/examples/complete" && terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$PLAIN_APPLY" | grep -E '^Error' -A5 | head -60
  fail "the cold-deploy apply failed"; }
grep -qE 'Apply complete! Resources: 30 added' <<< "$PLAIN_APPLY" \
  || { grep -E 'Apply complete' <<< "$PLAIN_APPLY"; fail "the cold-deploy apply did not create exactly 30 resources - the corpus pin or the module's own conditionals have moved"; }
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
# 24 of the 30 - is an untaggable S3 sub-resource whose identity is
# parent-derived from one of the four buckets.
KNOWN_ROOTS=("${BUCKETS[@]}" "$ROLE_NAME" "$KMS_KEY_ID")

UNMARKED="$(awsl s3api get-bucket-tagging --bucket "s3-bucket-$PET" 2>&1)"
if grep -qF 'tofu-address' <<< "$UNMARKED"; then
  fail "the plain-terraform bucket already carries a tofu-address tag before migration - this test proves nothing"
fi
log "  confirmed unmarked: s3-bucket-$PET carries no tofu-address tag (${UNMARKED:0:40}...)"
gauntlet_stage cold_deploy pass "30 resources added by plain terraform, 4 buckets confirmed live, no tofu-address tag"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=migrate
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

# DELTA 6: REVERTED (2026-08-18). #306 is fixed (lex00/floci
# S3ControlController.tagResource now reads-merges-writes rather than
# replacing the whole tag set) and reconciled into the currently-pinned
# floci image; internal/live/lifecycle/marker_tag_merge_live_test.go's
# TestMarkerSurvivesIncrementalTagUpdate passes live against it. The
# estate's own tags = { Owner = "Anton" } argument on module.s3_bucket is
# left exactly as terraform-aws-modules/terraform-aws-s3-bucket wrote it -
# no delta applied here at all.

( cd "$ESTATE/examples/complete" && "$TOFU" init -upgrade -input=false -no-color >/dev/null 2>&1 ) \
  || { ( cd "$ESTATE/examples/complete" && "$TOFU" init -upgrade -input=false -no-color 2>&1 | tail -30 ); fail "estate init failed"; }

log "=== STAGE 2a: live-import, read-only first ==="
IMPORT_OUT="$(cd "$ESTATE/examples/complete" && "$TOFU" live-import -state="$PLAIN/examples/complete/terraform.tfstate" -estate="$ESTATE_NAME" 2>&1)" || {
  printf '%s\n' "$IMPORT_OUT" | tail -40; fail "live-import (dry run) failed"; }
grep -qF "6 of 30 resource instance(s) are eligible for stamping" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT" | head -60; fail "live-import did not verify exactly 6 of 30 resource instances as eligible - only the four aws_s3_bucket instances plus aws_iam_role.this and aws_kms_key.objects carry a tags argument, everything else in this estate is untaggable"; }
grep -qF "No tag has been written." <<< "$IMPORT_OUT" \
  || fail "the dry run wrote a tag - it must not"
log "  6 of 30 verified against the live system (the four buckets, the IAM role, the KMS key - everything else here is untaggable and admits parent-derived); nothing written yet"

log "=== STAGE 2b: -approve ==="
APPROVE_OUT="$(cd "$ESTATE/examples/complete" && "$TOFU" live-import -state="$PLAIN/examples/complete/terraform.tfstate" -estate="$ESTATE_NAME" -approve 2>&1)" || {
  printf '%s\n' "$APPROVE_OUT" | tail -40; fail "live-import -approve failed"; }
grep -qF "6 resource(s) newly stamped, 0 already stamped, 1 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 23 skipped" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not stamp exactly 6 resources and record random_pet cleanly (23 skipped: the untaggable, parent-derived S3 sub-resources; 1 recorded: random_pet, issue #340)"; }
# GitHub issue #364 unit A2: every stamped instance, plus every untaggable
# instance whose identity is a plain, non-composite, non-sensitive
# server-minted id, now also gets a kind=identity record - not only its
# marker (stamped) or nothing at all (untaggable, before this unit).
# Measured for real against this estate (26), not derived: the 6 stamped
# instances all qualify, and so do 20 of the 23 skipped (untaggable) ones -
# the parent-derived S3 sub-resources whose identity is their own plain,
# server-minted id. The remaining 3 skipped instances' identity is not
# fully recordable by this mechanism (a composite identity, or no usable
# id attribute at all) and get none. random_pet (RECORDED above) is
# kind=object, not kind=identity, and is correctly excluded from this
# count either way.
grep -qF "26 identities recorded." <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not report exactly 26 identities recorded (GitHub issue #364 unit A2)"; }
log "  6 stamped, 1 recorded (random_pet, issue #340), 26 identities recorded (#364 unit A2)"

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
# GitHub issue #402's own scouting: this used to anchor only the "0 added"
# prefix and never inspected the destroyed count, so "0 added, 0 changed, 1
# destroyed" - random_pet.this, DELTA 3's own known gap (live-import does
# not migrate untaggable effects resources, see header) - passed silently
# on every run, undetected, alongside whatever else might have been
# destroyed beside it. Pinned to the exact count instead: this apply may
# destroy random_pet.this and NOTHING else, so a regression that destroys
# an unrelated resource (or 2+, or 0 when random_pet's own gap is finally
# closed) now fails loudly rather than passing this same permissive check.
grep -qE '^Apply complete! Resources: 0 added, 0 changed, 1 destroyed\.$' <<< "$APPLY_RESIDUE" \
  || { grep -E 'Apply complete' <<< "$APPLY_RESIDUE"; fail "the residue-classification apply did not destroy exactly random_pet.this and nothing else (0 added, 0 changed, 1 destroyed expected - issue #340's own known gap, see header) - it should only ever change, plus that one pre-existing destroy"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY_RESIDUE" | head -1)"
for b in "${BUCKETS[@]}"; do
  EST="$(awsl s3api get-bucket-tagging --bucket "$b" --query "TagSet[?Key=='tofu-estate'].Value | [0]" --output text 2>/dev/null)"
  [ "$EST" = "$ESTATE_NAME" ] || fail "bucket $b lost its tofu-estate marker during the residue-classification apply (got \"$EST\") - this is issue #306, which the header says is fixed and re-verified; if this fires, the fix has regressed or the pinned floci image has moved backward"
done
log "  all four buckets' markers survived the classification apply"
gauntlet_stage migrate pass "6 of 30 stamped, 1 recorded (random_pet, issue #340), 23 skipped (untaggable), 0 failed, 26 identities recorded (#364 unit A2); markers survived the residue-classification apply"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - no state file, live-plan, empty, and the identities
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=test_plan
log "=== STAGE 3: no state file, live-plan ==="
plan_into "$WORK/plan1.log" || { grep -vE '^[0-9]{4}-' "$WORK/plan1.log" | tail -40; fail "live-plan exited non-zero"; }
[ ! -f "$ESTATE/examples/complete/terraform.tfstate" ] || fail "live-plan wrote a state file"
# The acl/website_configuration gap this script's header documents at
# length (CHOUDOUFU GAP, not fixed - genuinely structural) is scoped OUT of
# the estate entirely by the SCOPE REDUCTION step above, on both copies, so
# this plan is expected to propose no RESOURCE changes, like every other
# stage-3 assertion in this fork's other corpus-* scripts.
#
# THE OUTPUTS QUIRK (same one live/e2e/corpus-iam-read-only-policy and
# live/e2e/corpus-root-dns-zones document and work around): this estate's
# root main.tf re-exports module.s3_bucket's outputs, live-plan holds no
# state between runs, so there is never a prior output baseline to diff
# against - every run shows a permanent "Changes to Outputs" section, and
# OpenTofu's renderer never prints a "Plan: N to add, N to change, N to
# destroy" line while that is true, empty plan or not. Asserting the
# absence of a resource action header is the real check, not "No changes."
grep -vE '^[0-9]{4}-' "$WORK/plan1.log" > "$WORK/plan1-notrace.log"
if grep -qE '^  # .+ will be (created|updated|destroyed)' "$WORK/plan1-notrace.log"; then
  grep -E '^  # .+ will be' "$WORK/plan1-notrace.log"
  fail "the plan proposes a resource change"
fi
log "  no resource action proposed (outputs quirk aside, see header)"

# The value, not the verdict: every rendered import identity, read off the
# TF_LOG=trace projection log, must NAME one of the six taggable roots
# (KNOWN_ROOTS: the four buckets, the IAM role, the KMS key) - as the whole
# identity (the taggable types themselves) or as the leading component
# before a ":" or "," (every parent-derived S3 sub-resource type).
#
# Two counts, not one, and deliberately not the same file: RAW_N_IDS is
# "close to one occurrence per managed instance" (many of module.s3_bucket's
# own sub-resource TYPES - ownership_controls, versioning, policy, logging,
# cors_configuration, lifecycle_configuration, object_lock_configuration,
# public_access_block, accelerate_configuration, request_payment_
# configuration - import by the bucket's own id alone, no distinguishing
# suffix, so several real instances render the IDENTICAL string). ids.
# (deduplicated) is what ids_all_name_known_roots actually checks, and
# de-duplication is correct there: the claim is "every DISTINCT identity
# string names a known root", not "every instance produced a distinct one".
grep -oE 'from import identity "[^"]*"' "$WORK/plan1.log" | sed 's/.*identity "//; s/"$//' > "$WORK/ids-raw"
sort -u "$WORK/ids-raw" > "$WORK/ids"
RAW_N_IDS="$(grep -c . "$WORK/ids-raw")"
N_IDS="$(grep -c . "$WORK/ids")"
[ "$RAW_N_IDS" -ge 28 ] || { cat "$WORK/ids-raw"; fail "only $RAW_N_IDS rendered identity occurrences, expected close to 30 (one per managed AWS instance)"; }
log "  $RAW_N_IDS rendered import identity occurrences ($N_IDS distinct strings)"

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
gauntlet_stage test_plan pass "no resource action proposed; $RAW_N_IDS rendered identity occurrences ($N_IDS distinct), all naming known roots"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 4: TEST APPLY - the empty plan applies as a genuine no-op
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=test_apply
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
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed); bucket count unchanged at $BEFORE"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 5: DRIFT AND RECONVERGE
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=drift_reconverge
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
# THE OUTPUTS QUIRK again (see STAGE 3): no "Plan:"/"No changes." line to
# grep for, so the check is the same absence-of-a-resource-action-header
# test.
grep -vE '^[0-9]{4}-' "$WORK/plan-final.log" > "$WORK/plan-final-notrace.log"
if grep -qE '^  # .+ will be (created|updated|destroyed)' "$WORK/plan-final-notrace.log"; then
  grep -E '^  # .+ will be' "$WORK/plan-final-notrace.log"
  fail "the final plan proposes a resource change"
fi
log "  final plan: no resource action proposed"
gauntlet_stage drift_reconverge pass "accelerate config drifted to Enabled, exactly 1 change proposed and applied, reconverged to Suspended, final plan empty"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 6: RENAME (day2_rename, active - live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
#
# Two of the four real buckets: a `moved` block renames the whole
# module.log_bucket call to module.log_bucket_renamed - moving every one of
# its instances, the bucket itself plus its untaggable, parent-derived
# siblings (ownership controls, the attached policies), in one statement,
# the same way OpenTofu's own moved-block mechanism moves a whole module -
# and "choudoufu live-mv" renames module.simple_bucket to
# module.simple_bucket_renamed with no moved block at all, rewriting only
# the ONE taggable instance inside it (aws_s3_bucket.this[0]; its untaggable
# siblings carry no marker to rewrite and re-resolve against their live
# parent bucket regardless of which address their own resource block now
# sits at - the same "parent-derived" identity the header's stage-2 tally
# already documents for this estate's 23 untaggable-by-design instances).
# module.cloudfront_log_bucket and module.s3_bucket are left untouched as
# negative-control anchors. module.simple_bucket is chosen for the live-mv
# leg because nothing else in the estate references its module output;
# module.log_bucket IS referenced once, by module.s3_bucket's own
# logging.target_bucket argument, which the same sed pass below updates for
# the moved-block leg.
#
# Two address grammars appear below and must not be confused: the `moved`
# block and live-mv's own command-line arguments use plain OpenTofu
# resource-instance syntax (module.simple_bucket.aws_s3_bucket.this[0],
# bracket-indexed, because the module source's aws_s3_bucket.this carries
# `count = local.create_bucket && !var.is_directory_bucket ? 1 : 0`); the
# tofu-address MARKER value itself uses choudoufu's own serialization
# (module.simple_bucket.aws_s3_bucket.this:0, colon-indexed - confirmed
# against the live tag read back in stage 2b's own log above, not assumed).
#
# BREAK=6 exercises this stage's own break control instead of the real
# checks (a distinct value from BREAK=1, which is already claimed by stage
# 3's own control and fails before this stage is ever reached): renaming
# module.simple_bucket with NEITHER a moved block NOR live-mv. Unlike
# live/e2e/corpus-eks-basic/run.sh's day2_rename BREAK=1 leg (a destroy AND
# a create - that estate's marker sweep finds the vacated security-group
# marker and proposes destroying it), this estate's stateless replan
# proposes a CREATE ONLY for the new address: nothing here still declares
# module.simple_bucket, so there is no config-driven candidate left to sweep
# a leftover marker against, and the old bucket - never itself destroyed -
# stays live, invisibly orphaned under an address the configuration no
# longer names.

CURRENT_STAGE=day2_rename
log "=== STAGE 6: day2_rename - one bucket module renamed via a moved block, another via live-mv ==="
ESTATE_DIR="$ESTATE/examples/complete"

log "=== D0. capture the tofu-address markers a rename must only rewrite, never destroy ==="
LOG_ADDR_BEFORE="$(awsl s3api get-bucket-tagging --bucket "logs-$PET" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$LOG_ADDR_BEFORE" = "module.log_bucket.aws_s3_bucket.this:0" ] || fail "logs-$PET carries tofu-address=$LOG_ADDR_BEFORE before the rename, not module.log_bucket.aws_s3_bucket.this:0"
SIMPLE_ADDR_BEFORE="$(awsl s3api get-bucket-tagging --bucket "simple-$PET" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$SIMPLE_ADDR_BEFORE" = "module.simple_bucket.aws_s3_bucket.this:0" ] || fail "simple-$PET carries tofu-address=$SIMPLE_ADDR_BEFORE before the rename, not module.simple_bucket.aws_s3_bucket.this:0"
log "  before: logs-$PET -> $LOG_ADDR_BEFORE, simple-$PET -> $SIMPLE_ADDR_BEFORE"

if [ "${BREAK:-}" = "6" ]; then
  log "=== D1 (BREAK=6). rename module.simple_bucket -> module.simple_bucket_renamed WITHOUT a moved block or live-mv ==="
  sed -i.bak 's/^module "simple_bucket" {/module "simple_bucket_renamed" {/' "$ESTATE_DIR/main.tf"
  rm -f "$ESTATE_DIR/main.tf.bak"
  grep -q 'module "simple_bucket_renamed"' "$ESTATE_DIR/main.tf" || fail "BREAK=6 sed did not rename module.simple_bucket - the corpus pin has moved"
  ( cd "$ESTATE_DIR" && "$TOFU" init -upgrade -input=false -no-color ) > /tmp/s3-day2-break-init.log 2>&1 || {
    tail -40 /tmp/s3-day2-break-init.log; fail "the BREAK=6 rename's reinit failed"; }
  BREAK_PLAN_LOG="$WORK/plan-break.log"
  plan_into "$BREAK_PLAN_LOG" || { grep -vE '^[0-9]{4}-' "$BREAK_PLAN_LOG" | tail -40; fail "the BREAK=6 rename's plan exited non-zero"; }
  grep -qE '^  # module\.simple_bucket_renamed\.aws_s3_bucket\.this\[0\] will be created' "$BREAK_PLAN_LOG" \
    || { grep -E '^  # .+ will be' "$BREAK_PLAN_LOG"; fail "BREAK=6: renaming module.simple_bucket without a moved block or live-mv did not propose creating module.simple_bucket_renamed.aws_s3_bucket.this[0] - this stage's check is not load-bearing"; }
  log "  BREAK=6: correctly proposes creating module.simple_bucket_renamed.aws_s3_bucket.this[0] with no marker rewrite - the moved-block and live-mv checks below are skipped"
else
  log "=== D1. choudoufu, moved block: module.log_bucket -> module.log_bucket_renamed ==="
  sed -i.bak 's/^module "log_bucket" {/module "log_bucket_renamed" {/' "$ESTATE_DIR/main.tf"
  sed -i.bak 's/target_bucket = module\.log_bucket\.s3_bucket_id/target_bucket = module.log_bucket_renamed.s3_bucket_id/' "$ESTATE_DIR/main.tf"
  rm -f "$ESTATE_DIR/main.tf.bak"
  cat >> "$ESTATE_DIR/main.tf" <<'EOF'

moved {
  from = module.log_bucket
  to   = module.log_bucket_renamed
}
EOF
  grep -q 'module "log_bucket_renamed"' "$ESTATE_DIR/main.tf" || fail "D1 sed did not rename module.log_bucket - the corpus pin has moved"
  grep -q 'module.log_bucket_renamed.s3_bucket_id' "$ESTATE_DIR/main.tf" || fail "D1 sed did not update the logging.target_bucket reference to module.log_bucket - the corpus pin has moved"

  ( cd "$ESTATE_DIR" && "$TOFU" init -upgrade -input=false -no-color ) > /tmp/s3-day2-d1-init.log 2>&1 || {
    tail -40 /tmp/s3-day2-d1-init.log; fail "the moved-block rename's reinit failed"; }

  MOVED_PLAN_LOG="$WORK/plan-d1.log"
  plan_into "$MOVED_PLAN_LOG" || { grep -vE '^[0-9]{4}-' "$MOVED_PLAN_LOG" | tail -40; fail "the moved-block rename plan exited non-zero"; }
  grep -qE '^  # .+ will be (created|destroyed)' "$MOVED_PLAN_LOG" \
    && { grep -E '^  # .+ will be' "$MOVED_PLAN_LOG"; fail "the moved-block rename proposes a create or destroy - not zero churn"; }
  grep -qE '^  # module\.log_bucket_renamed\.aws_s3_bucket\.this\[0\] will be updated in-place' "$MOVED_PLAN_LOG" \
    || { grep -E '^  # .+ will be' "$MOVED_PLAN_LOG"; fail "the moved-block plan does not propose an in-place update to module.log_bucket_renamed.aws_s3_bucket.this[0]"; }
  grep -qF 'Plan: 0 to add, 1 to change, 0 to destroy.' "$MOVED_PLAN_LOG" \
    || { grep -E '^Plan:|^No changes' "$MOVED_PLAN_LOG"; fail "the moved-block rename plan is not exactly one in-place change"; }
  grep -qE 'tofu-address.*module\.log_bucket\.aws_s3_bucket\.this:0.*->.*module\.log_bucket_renamed\.aws_s3_bucket\.this:0' "$MOVED_PLAN_LOG" \
    || { grep -E 'tofu-address' "$MOVED_PLAN_LOG"; fail "the moved-block plan does not show the bucket's tofu-address marker being rewritten from the old address to the new one"; }
  log "  choudoufu: zero churn, one in-place tags update - the marker rewrite the moved block completes"

  MOVED_APPLY_OUT="$(cd "$ESTATE_DIR" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; MOVED_APPLY_RC=$?
  [ "$MOVED_APPLY_RC" -eq 0 ] || { printf '%s\n' "$MOVED_APPLY_OUT" | tail -40; fail "the moved-block rename apply exited $MOVED_APPLY_RC"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$MOVED_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$MOVED_APPLY_OUT"; fail "the moved-block rename apply was not exactly one in-place change"; }

  LOG_ADDR_AFTER="$(awsl s3api get-bucket-tagging --bucket "logs-$PET" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$LOG_ADDR_AFTER" = "module.log_bucket_renamed.aws_s3_bucket.this:0" ] \
    || fail "logs-$PET carries tofu-address=$LOG_ADDR_AFTER after the moved-block rename, not module.log_bucket_renamed.aws_s3_bucket.this:0"
  log "  logs-$PET unchanged, tofu-address now module.log_bucket_renamed.aws_s3_bucket.this:0"

  log "=== D2. choudoufu, live-mv: module.simple_bucket -> module.simple_bucket_renamed, no moved block at all ==="
  sed -i.bak 's/^module "simple_bucket" {/module "simple_bucket_renamed" {/' "$ESTATE_DIR/main.tf"
  rm -f "$ESTATE_DIR/main.tf.bak"
  grep -q 'module "simple_bucket_renamed"' "$ESTATE_DIR/main.tf" || fail "D2 sed did not rename module.simple_bucket - the corpus pin has moved"

  ( cd "$ESTATE_DIR" && "$TOFU" init -upgrade -input=false -no-color ) > /tmp/s3-day2-d2-init.log 2>&1 || {
    tail -40 /tmp/s3-day2-d2-init.log; fail "the live-mv rename's reinit failed"; }

  MV_OUT="$(cd "$ESTATE_DIR" && "$TOFU" live-mv -estate="$ESTATE_NAME" 'module.simple_bucket.aws_s3_bucket.this[0]' 'module.simple_bucket_renamed.aws_s3_bucket.this[0]' 2>&1)"; MV_RC=$?
  [ "$MV_RC" -eq 0 ] || { printf '%s\n' "$MV_OUT" | tail -30; fail "choudoufu live-mv exited $MV_RC"; }
  grep -qF 'Rewrote the ownership marker on one live resource. This was a cloud write.' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report a real write"; }
  grep -qF '"module.simple_bucket.aws_s3_bucket.this[0]" -> "module.simple_bucket_renamed.aws_s3_bucket.this[0]"' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report rewriting the tofu-address marker from the old address to the new one"; }
  log "  live-mv: $(grep -F 'live ID' <<< "$MV_OUT")"

  SIMPLE_ADDR_AFTER="$(awsl s3api get-bucket-tagging --bucket "simple-$PET" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$SIMPLE_ADDR_AFTER" = "module.simple_bucket_renamed.aws_s3_bucket.this:0" ] \
    || fail "simple-$PET carries tofu-address=$SIMPLE_ADDR_AFTER after live-mv, not module.simple_bucket_renamed.aws_s3_bucket.this:0"
  log "  simple-$PET unchanged, tofu-address now module.simple_bucket_renamed.aws_s3_bucket.this:0"

  log "=== D3. one more plan: config and markers agree on both renames, nothing proposed ==="
  FINAL_PLAN_LOG="$WORK/plan-d3.log"
  plan_into "$FINAL_PLAN_LOG" || { grep -vE '^[0-9]{4}-' "$FINAL_PLAN_LOG" | tail -40; fail "the post-rename plan exited non-zero"; }
  grep -vE '^[0-9]{4}-' "$FINAL_PLAN_LOG" > "$WORK/plan-d3-notrace.log"
  if grep -qE '^  # .+ will be (created|updated|destroyed)' "$WORK/plan-d3-notrace.log"; then
    grep -E '^  # .+ will be' "$WORK/plan-d3-notrace.log"
    fail "the post-rename plan proposes a resource change"
  fi
  log "  no resource action proposed. Both renames are complete and invisible to the next plan."

  gauntlet_stage day2_rename pass "moved block: module.log_bucket renamed to module.log_bucket_renamed with zero churn (0 add, 1 change, 0 destroy), the bucket's tofu-address marker rewritten in place; live-mv: module.simple_bucket renamed to module.simple_bucket_renamed with zero churn, marker rewritten in place; both live bucket names unchanged, read via the AWS CLI; the post-rename plan proposes no resource action"
fi
CURRENT_STAGE=""
gauntlet_end

log ""
log "=== PASS ==="
log ""
log "terraform-aws-modules/terraform-aws-s3-bucket's complete example: 30"
log "instances across 5 module calls and 14 aws_s3_bucket_* types (of 32"
log "instances/15 types upstream - module.s3_bucket's own canned acl and"
log "website scoped out, see header), cold-deployed with plain terraform,"
log "migrated with live-import, stripped of its state file and replanned"
log "with no resource action proposed and every identity checked against a"
log "live AWS CLI read, applied as a genuine no-op, drifted on one instance"
log "and reconverged."
log ""
log "Six real defects found on first contact with a cloud. Four fixed (two"
log "admission gaps, ratified and merged into the schema-based fallback; a"
log "floci routing bug, PR #53; marker loss on apply, issue #306, fixed in"
log "lex00/floci and re-verified here with DELTA 6 reverted). One, #340,"
log "fixed for the record-store half (random_pet now migrates generically"
log "into the record store); DELTA 3 stays, pending its own separate"
log "verification - see the header comment above. One genuinely"
log "structural, not fixed, scoped out rather than worked around: the"
log "canned acl/website_configuration gap - see the header comment above."
