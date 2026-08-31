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
#   6. RENAME         (day2_rename, live/GAUNTLET.md #6) module.cloudfront_
#                     log_bucket renamed to module.cloudfront_log_bucket_
#                     renamed through a `moved` block; module.simple_bucket
#                     renamed to module.simple_bucket_renamed through
#                     `choudoufu live-mv` with no moved block at all. Both
#                     zero churn: the bucket's own tofu-address marker is
#                     rewritten in place, nothing is created or destroyed,
#                     and a further plan is empty. module.log_bucket and
#                     module.s3_bucket are left untouched as negative-control
#                     anchors (module.log_bucket, specifically, because
#                     renaming it exposes a distinct real defect - see the
#                     ANOTHER WALL note above stage 6's own code, issue #404).
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
#                Set to "replace" to exercise day2_replace's own negative
#                control instead (PART F, after STAGE 5, before STAGE 6):
#                manufacture the exact coexistence "skip the destroy half"
#                describes directly - a second live bucket is created via
#                the AWS CLI, carrying the SAME tofu-address/tofu-slot as
#                the bucket a genuine replace would have destroyed - and
#                the next plan must report the collision loudly (for this
#                name-derived-identity type: a "Live resource displaced
#                from the address it is marked for" warning naming the
#                manufactured bucket, proposing nothing for it - not the
#                fungible-set "Two live resources claiming one slot"
#                EC2/SQS's own marker-sweep-only identity produces), not
#                silently propose nothing.
#   BREAK_COUNT  set to 1 to run day2_count's own Break control (PART G,
#                after STAGE 7) instead of that stage's real checks: after
#                the real scale-down plan, assert the WRONG instance
#                (count_test[0] rather than count_test[1]) is the one
#                destroyed, so the stage reports verdict=fail. Independent
#                of BREAK, which this stage never reads.
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
gauntlet_begin_stage cold_deploy
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
# GREENFIELD (greenfield, live/GAUNTLET.md #13, active)
# ══════════════════════════════════════════════════════════════════════════
#
# Two more, fresh containers, entirely independent of everything above and
# below: choudoufu applies the same reduced estate (SCOPE REDUCTION's
# acl/website removal and DELTA 5's expected_bucket_owner removal are
# genuine, permanent properties of this estate under this fork, not
# migration-only deltas, so both apply here too) directly from a live
# block - no live-import, no migration, no state file ever existing. The
# random_pet uniquifier is pinned to the same fixed literal on BOTH this
# copy and its stock oracle: unlike DELTA 3 (which stands in for an
# untaggable effect live-import cannot yet migrate), there is nothing to
# migrate here - greenfield originates the estate - so this is just giving
# both sides the same deterministic bucket names, the same reason
# corpus-sqs-basic's greenfield needs no such pin at all (its resources are
# already statically named). DELTA 4's two count-gating data source reads
# are pinned on both sides too, to the SAME literal (read fresh off the
# greenfield container itself), for the same reason DELTA 4 exists on the
# migrate path: a count expression must be statically evaluable, a
# genuinely fixed parity property of this fork's config-language subset,
# not a defect either side's oracle comparison should be sensitive to.
gauntlet_begin_stage greenfield
FLOCI_GREEN_NAME="choudoufu-corpus-s3-bucket-complete-green-$$"
FLOCI_ORACLE_NAME="choudoufu-corpus-s3-bucket-complete-green-oracle-$$"
GREEN_ESTATE_NAME="s3-bucket-complete-greenfield"
PET_G="greenfield"

# floci_launch_retry <name> <portvar> - several gauntlet scripts run
# concurrently on a shared host, each with its own FLOCI_PORT reservation,
# but a fixed +1/+2 offset from that reservation is not itself reserved and
# collides with siblings picking the same offset. Pick a port at random
# from a wide, rarely-used range and retry on "already allocated" instead.
floci_launch_retry() {
  local name="$1" portvar="$2" tries=0 port out
  while :; do
    port=$((20000 + RANDOM % 20000))
    out="$(docker run -d --rm -p "${port}:4566" --name "$name" "$FLOCI_IMAGE" 2>&1)" && { eval "$portvar=$port"; return 0; }
    tries=$((tries + 1))
    grep -qF 'port is already allocated' <<< "$out" || { printf '%s\n' "$out"; return 1; }
    [ "$tries" -ge 10 ] && { printf '%s\n' "$out"; return 1; }
  done
}

log "=== GREENFIELD: 0. two more floci containers ==="
floci_launch_retry "$FLOCI_GREEN_NAME" FLOCI_GREEN_PORT || fail "docker run for $FLOCI_GREEN_NAME failed"
floci_launch_retry "$FLOCI_ORACLE_NAME" FLOCI_ORACLE_PORT || fail "docker run for $FLOCI_ORACLE_NAME failed"
GREEN_ENDPOINT="http://localhost.localstack.cloud:${FLOCI_GREEN_PORT}"
ORACLE_ENDPOINT="http://localhost.localstack.cloud:${FLOCI_ORACLE_PORT}"
for gep in "$GREEN_ENDPOINT" "$ORACLE_ENDPOINT"; do
  GH=""
  for _ in $(seq 1 45); do
    GH="$(curl -fs "${gep}/_localstack/health" 2>/dev/null)" || true
    grep -q '"s3"' <<< "${GH:-}" && break
    sleep 2
  done
  grep -q '"s3"' <<< "${GH:-}" || fail "floci did not come up healthy (s3) at $gep"
done
log "  healthy: greenfield=$GREEN_ENDPOINT oracle=$ORACLE_ENDPOINT"

GREEN="$WORK/green"
ORACLE_DIR="$WORK/green-oracle"
copy_estate "$GREEN"
copy_estate "$ORACLE_DIR"
scope_reduce "$GREEN"
scope_reduce "$ORACLE_DIR"
provider_patch "$GREEN"
provider_patch "$ORACLE_DIR"
# strict { no_source_create = "create" }: found necessary re-verifying this
# stage after main's CHOUDOUFU_NODE_RESOLVE default flip (845e7a0d9d,
# 2026-08-25) - a genuinely cold apply now refuses config-identified
# instances whose identity value belongs to a sibling that does not exist
# yet either (#365 ruling 4's default refusal of that ambiguity), and a
# greenfield apply is the one case an operator KNOWS it is a real create.
# Same fix, same precedent as corpus-alb-complete's own 898091b8f2.
version_pin "$GREEN" '
  live {
    estate = "'"$GREEN_ESTATE_NAME"'"
    record_store "local" {
      path = ".tofu-records"
    }
    strict {
      no_source_create = "create"
    }
  }'
version_pin "$ORACLE_DIR" ""

pin_pet() { # pin_pet <dir> <literal> - DELTA 3's own transform, applied to
            # a config that never had a random_pet apply to begin with
  perl -0pi -e 's/resource "random_pet" "this" \{\n  length = 2\n\}\n/locals {\n  pinned_pet = "'"$2"'"\n}\n/' "$1/examples/complete/main.tf"
  perl -pi -e 's/random_pet\.this\.id/local.pinned_pet/g' "$1/examples/complete/main.tf"
  grep -q 'pinned_pet = "'"$2"'"' "$1/examples/complete/main.tf" || fail "pin_pet did not match in $1 - the corpus pin has moved"
}
pin_pet "$GREEN" "$PET_G"
pin_pet "$ORACLE_DIR" "$PET_G"

# DELTA 5 (see header): expected_bucket_owner has no live representation,
# which forces a spurious replace on the first replan after any apply that
# touches it - greenfield's own next-plan check below included. Removed
# from both copies so the comparison is between the SAME reduced estate.
for d in "$GREEN" "$ORACLE_DIR"; do
  perl -pi -e 's/^(  expected_bucket_owner                  = data\.aws_caller_identity\.current\.account_id)$/  # DELTA 5: expected_bucket_owner removed - $1/' "$d/examples/complete/main.tf"
  grep -q 'DELTA 5' "$d/examples/complete/main.tf" || fail "DELTA 5 did not match in $d - the corpus pin has moved"
done

GREEN_CANONICAL_USER_ID="$(aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" s3api list-buckets --query 'Owner.ID' --output text)"
[ -n "$GREEN_CANONICAL_USER_ID" ] && [ "$GREEN_CANONICAL_USER_ID" != "None" ] || fail "could not read the greenfield account's own canonical user ID off floci"
CLOUDFRONT_CANONICAL_USER_ID="c4c1ede66af53448b93c283ce9448c4ba468c9432aa01d700d3878632f77d2d0" # AWS-documented constant, every account (see STAGE 2's own DELTA 4 below)
for d in "$GREEN" "$ORACLE_DIR"; do
  perl -0pi -e 's/(grant = \[\{\n    type       = "CanonicalUser"\n    permission = "FULL_CONTROL"\n    id         = )data\.aws_canonical_user_id\.current\.id(\n    \}, \{\n    type       = "CanonicalUser"\n    permission = "FULL_CONTROL"\n    id         = )data\.aws_cloudfront_log_delivery_canonical_user_id\.cloudfront\.id( # Ref\.[^\n]*\n    \}\n  \])/$1"'"$GREEN_CANONICAL_USER_ID"'" # DELTA 4$2"'"$CLOUDFRONT_CANONICAL_USER_ID"'" # DELTA 4$3/' "$d/examples/complete/main.tf"
  [ "$(grep -c 'DELTA 4' "$d/examples/complete/main.tf")" = "2" ] || fail "DELTA 4 did not match in $d - the corpus pin has moved"
done
log "  DELTA 3/4/5 applied to both the greenfield estate and its stock oracle (see comment above)"

GREEN_BUCKETS=(s3-bucket-$PET_G logs-$PET_G cloudfront-logs-$PET_G simple-$PET_G)

log "=== GREENFIELD: 1. choudoufu apply from nothing, no migration ==="
( cd "$GREEN/examples/complete" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -upgrade -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$GREEN/examples/complete" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -upgrade -input=false -no-color 2>&1 | tail -30 ); fail "the greenfield init failed"; }
GREEN_APPLY_OUT="$(cd "$GREEN/examples/complete" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$GREEN_APPLY_OUT" | grep -E '^Error' -A5 | head -60; fail "the greenfield apply failed"; }
grep -qE 'Apply complete! Resources: 29 added' <<< "$GREEN_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT"; fail "the greenfield apply did not create exactly 29 resources (30 upstream reduced by SCOPE REDUCTION and its own random_pet pinned away, see header)"; }
log "  $(grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT" | head -1)"

awsg() { aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" "$@"; }

log "=== GREENFIELD: 2. markers, read through the AWS CLI directly ==="
for pair in "s3-bucket-$PET_G:module.s3_bucket.aws_s3_bucket.this:0" "cloudfront-logs-$PET_G:module.cloudfront_log_bucket.aws_s3_bucket.this:0" "simple-$PET_G:module.simple_bucket.aws_s3_bucket.this:0"; do
  b="${pair%%:*}"; want="${pair#*:}"
  got="$(awsg s3api get-bucket-tagging --bucket "$b" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text 2>/dev/null)"
  [ "$got" = "$want" ] || fail "greenfield bucket $b carries tofu-address=$got, not $want"
  est="$(awsg s3api get-bucket-tagging --bucket "$b" --query "TagSet[?Key=='tofu-estate'].Value | [0]" --output text 2>/dev/null)"
  [ "$est" = "$GREEN_ESTATE_NAME" ] || fail "greenfield bucket $b carries tofu-estate=$est, not $GREEN_ESTATE_NAME"
done
log "  3 of 4 buckets spot-checked, correct tofu-address and tofu-estate markers - read via the AWS CLI, not choudoufu's own report"

log "=== GREENFIELD: 3. the local record store holds one record per instance (#364 A2: apply writes a record too, not just live-import) ==="
GREEN_RECORD_FILES="$(find "$GREEN/examples/complete/.tofu-records/tofu-records" -type f ! -name '*.lock' ! -name '*.tmp-*' 2>/dev/null | wc -l | tr -d ' ')"
[ "$GREEN_RECORD_FILES" -gt 0 ] || fail "expected at least one record under the local record store after the greenfield apply, found none"
log "  $GREEN_RECORD_FILES records persisted under the local record store"

log "=== GREENFIELD: 4. the next plan proposes nothing ==="
GREEN_PLAN_OUT="$(cd "$GREEN/examples/complete" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; GREEN_PLAN_RC=$?
[ "$GREEN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$GREEN_PLAN_OUT" | tail -30; fail "the greenfield replan exited $GREEN_PLAN_RC"; }
grep -vE '^[0-9]{4}-' <<< "$GREEN_PLAN_OUT" > "$WORK/green-plan-notrace.log"
if grep -qE '^  # .+ will be (created|updated|destroyed)' "$WORK/green-plan-notrace.log"; then
  grep -E '^  # .+ will be' "$WORK/green-plan-notrace.log"
  fail "the greenfield replan proposes a resource change"
fi
log "  no resource action proposed (outputs quirk aside, see STAGE 3 above)"

log "=== GREENFIELD: 5. stock oracle - the identical reduced estate applied fresh in its own namespace ==="
( cd "$ORACLE_DIR/examples/complete" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform init -upgrade -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ORACLE_DIR/examples/complete" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform init -upgrade -input=false -no-color 2>&1 | tail -30 ); fail "the greenfield oracle's init failed"; }
ORACLE_APPLY_OUT="$(cd "$ORACLE_DIR/examples/complete" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$ORACLE_APPLY_OUT" | tail -40; fail "the greenfield oracle apply failed"; }
grep -qE 'Apply complete! Resources: 29 added' <<< "$ORACLE_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$ORACLE_APPLY_OUT"; fail "the greenfield oracle apply did not create exactly 29 resources"; }
log "  $(grep -E 'Apply complete' <<< "$ORACLE_APPLY_OUT" | head -1)"

bucket_shape() { # $1=endpoint $2=bucket - a normalised structural fact
                  # sheet, read via the AWS CLI, never through tofu state
  local ep="$1" b="$2"
  local ver enc pol
  ver="$(aws --endpoint-url "$ep" --region "$REGION" s3api get-bucket-versioning --bucket "$b" --query 'Status' --output text 2>/dev/null)"
  enc="$(aws --endpoint-url "$ep" --region "$REGION" s3api get-bucket-encryption --bucket "$b" --query 'ServerSideEncryptionConfiguration.Rules[0].ApplyServerSideEncryptionByDefault.SSEAlgorithm' --output text 2>/dev/null)"
  aws --endpoint-url "$ep" --region "$REGION" s3api get-bucket-policy --bucket "$b" >/dev/null 2>&1 && pol=yes || pol=no
  printf 'versioning=%s encryption=%s policy=%s\n' "${ver:-None}" "${enc:-None}" "$pol"
}

log "=== GREENFIELD: 6. object-by-object comparison, via the AWS CLI on both endpoints, tags normalised out ==="
BUCKET_COUNT_GREEN="$(awsg s3api list-buckets --query 'length(Buckets)' --output text)"
[ "$BUCKET_COUNT_GREEN" = "4" ] || fail "the greenfield estate has $BUCKET_COUNT_GREEN buckets, expected 4"
for b in "${GREEN_BUCKETS[@]}"; do
  G="$(bucket_shape "$GREEN_ENDPOINT" "$b")"
  O="$(bucket_shape "$ORACLE_ENDPOINT" "$b")"
  [ "$G" = "$O" ] || { printf 'greenfield %s: %s\noracle    %s: %s\n' "$b" "$G" "$b" "$O"; fail "bucket $b differs structurally between the greenfield estate and the stock oracle"; }
done
log "  all 4 buckets match structurally (versioning, default encryption, policy presence) between choudoufu's greenfield apply and stock's cold deploy in its own namespace"
gauntlet_stage greenfield pass "29 resources from nothing (SCOPE REDUCTION's own reduced count, random_pet pinned to a literal on both sides), 3 of 4 bucket markers verified via the AWS CLI, $GREEN_RECORD_FILES records in the local record store (#364 A2), replan empty, stock oracle in its own namespace matches structurally on all 4 buckets (versioning, default encryption, policy presence)"
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# G-ORACLE: CHANGE COUNT, stock oracle (day2_count, live/GAUNTLET.md #8,
# issue #643)
# ══════════════════════════════════════════════════════════════════════════
#
# WHY A SYNTHETIC BLOCK. This estate's own root configuration
# (examples/complete/main.tf) declares no `count` and no `for_each` at all -
# zero matches - and every count inside the module itself is the boolean
# `count = local.create_bucket ? 1 : 0` create toggle, which is not a knob
# that scales. Its one genuinely multi-instance knob is the module input
# `intelligent_tiering`, a two-entry map ("general" and "documents") the
# module fans out with for_each - but onto
# aws_s3_bucket_intelligent_tiering_configuration, which declares no `tags`
# argument at all (checked against the module's own resource block in
# .corpus/s3-bucket/main.tf, line 1196), i.e. exactly the untaggable,
# parent-derived child STAGE 7's own header below records as issue #410,
# where a count-based removal of an untaggable instance was already
# confirmed invisible on this very estate. So this stage takes the
# sanctioned synthetic fallback: a NEW, self-contained resource,
# aws_s3_bucket.count_test (count_test_block() below), of the type this
# estate exercises most, referenced by nothing else here, at an address no
# other stage in this script ever uses - the same discipline
# live/e2e/reference-ec2-vpc/run.sh's PART F (aws_security_group.count_test)
# and live/e2e/corpus-hongbomiao-storage/run.sh's PART G use.
#
# WHAT COUNTS AS "GENUINELY A NEW OBJECT" HERE, established directly
# against floci with no tofu in the loop before any assertion below was
# written (HANDOFF's identity-semantics rule), on the currently pinned image
# (sha256:c55d74e1): an S3 bucket's id IS its own `bucket` name,
# deterministic from configuration, so a destroy+recreate under the same
# name comes back under the SAME id - a create-bucket -> delete-bucket ->
# create-bucket probe on one name succeeded both times under that one name.
# AWS mints no other server-side identifier for a bucket (unlike a security
# group id or an IAM PolicyId), so "this is a new object" cannot be read off
# an id here. The two discriminators used below instead are (a) head-bucket
# failing outright while the instance is gone - a deleted bucket is
# genuinely absent from floci, not left behind in a pending state the way a
# KMS key is - and (b) list-buckets' own CreationDate, confirmed to change
# across that same delete+recreate cycle (2026-08-31T06:56:33+00:00 ->
# 2026-08-31T06:56:35+00:00). The probe also confirmed tags do NOT survive
# the cycle (the recreated bucket came back with an empty TagSet), so a
# marker found on the recreated instance below is one choudoufu's own apply
# had to write again, never a leftover.
#
# The stock leg runs HERE rather than at the end, in the otherwise-idle
# account the greenfield stage's own stock oracle ($ORACLE_ENDPOINT, plain
# `terraform`, the same binary STAGE 1 cold-deploys with) just finished with
# and never touches again - that account's four buckets are all named
# *-greenfield, disjoint from ${ESTATE_NAME}-count-test-*. It must stay
# ABOVE the `docker rm -f` line below, or the account it needs is gone.
# Stock never had this count block in its own state, so unlike day2_remove's
# and day2_replace's oracles this one cannot be computed off cold_deploy's
# state: it stands the same 2-instance block up for real, scales it down and
# back up, and applies every step.
gauntlet_begin_stage day2_count
count_test_block() { # $1 = count
  cat <<COUNTEOF
resource "aws_s3_bucket" "count_test" {
  count  = $1
  bucket = "${ESTATE_NAME}-count-test-\${count.index}"

  tags = {
    Name = "count-test"
  }
}
COUNTEOF
}
oracle_count_provider() {
  cat <<EOF
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }
}

provider "aws" {
  region = "$REGION"

  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_region_validation      = true
  s3_use_path_style           = true
}

EOF
}
CT0_NAME="${ESTATE_NAME}-count-test-0"
CT1_NAME="${ESTATE_NAME}-count-test-1"
awsco() { aws --endpoint-url "$ORACLE_ENDPOINT" --region "$REGION" "$@"; }

log "=== G-ORACLE: stock, stand up a 2-instance count block, scale it to 1 and back, in the (idle) greenfield-oracle account ==="
ORACLE_COUNT="$WORK/oracle-count"
mkdir -p "$ORACLE_COUNT"
{ oracle_count_provider; count_test_block 2; } > "$ORACLE_COUNT/main.tf"
( cd "$ORACLE_COUNT" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ORACLE_COUNT" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_count stock oracle's init failed"; }
OC_UP_OUT="$(cd "$ORACLE_COUNT" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$OC_UP_OUT" | tail -30; fail "the day2_count stock oracle's baseline apply failed"; }
grep -qE 'Apply complete! Resources: 2 added' <<< "$OC_UP_OUT" \
  || { grep -E 'Apply complete' <<< "$OC_UP_OUT"; fail "stock did not create exactly 2 count-test buckets for the day2_count oracle"; }
OC_CT0_CREATED="$(awsco s3api list-buckets --query "Buckets[?Name=='$CT0_NAME'].CreationDate | [0]" --output text)"
OC_CT1_CREATED="$(awsco s3api list-buckets --query "Buckets[?Name=='$CT1_NAME'].CreationDate | [0]" --output text)"
[ -n "$OC_CT0_CREATED" ] && [ "$OC_CT0_CREATED" != "None" ] || fail "stock's oracle count_test[0] bucket ($CT0_NAME) is not live after the baseline apply"
[ -n "$OC_CT1_CREATED" ] && [ "$OC_CT1_CREATED" != "None" ] || fail "stock's oracle count_test[1] bucket ($CT1_NAME) is not live after the baseline apply"
log "  stock: 2 instances, count_test[0]=$CT0_NAME (created=$OC_CT0_CREATED), count_test[1]=$CT1_NAME (created=$OC_CT1_CREATED)"

{ oracle_count_provider; count_test_block 1; } > "$ORACLE_COUNT/main.tf"
OC_DOWN_PLAN="$(cd "$ORACLE_COUNT" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform plan -input=false -no-color 2>&1)"; OC_DOWN_PLAN_RC=$?
[ "$OC_DOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$OC_DOWN_PLAN" | tail -40; fail "the day2_count stock oracle's scale-down plan exited $OC_DOWN_PLAN_RC"; }
grep -qE '^  # aws_s3_bucket\.count_test\[1\] will be destroyed' <<< "$OC_DOWN_PLAN" \
  || { grep -E '^  # .+ will be' <<< "$OC_DOWN_PLAN"; fail "stock's scale-down plan does not destroy count_test[1]"; }
grep -qE '^  # aws_s3_bucket\.count_test\[0\] will be' <<< "$OC_DOWN_PLAN" \
  && { grep -E '^  # .+ will be' <<< "$OC_DOWN_PLAN"; fail "stock's scale-down plan touches count_test[0], which must be left alone"; }
grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$OC_DOWN_PLAN" \
  || { printf '%s\n' "$OC_DOWN_PLAN" | tail -10; fail "stock's scale-down plan proposes something other than exactly one destroy"; }
OC_DOWN_APPLY="$(cd "$ORACLE_COUNT" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$OC_DOWN_APPLY" | tail -30; fail "the day2_count stock oracle's scale-down apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$OC_DOWN_APPLY" \
  || { grep -E 'Apply complete' <<< "$OC_DOWN_APPLY"; fail "the day2_count stock oracle's scale-down apply was not exactly one destroy"; }
if OC_CT1_STILL="$(awsco s3api head-bucket --bucket "$CT1_NAME" 2>&1)"; then
  printf '%s\n' "$OC_CT1_STILL"; fail "stock's count_test[1] bucket ($CT1_NAME) is still live after its scale-down destroy"
fi
OC_CT0_AFTER_DOWN="$(awsco s3api list-buckets --query "Buckets[?Name=='$CT0_NAME'].CreationDate | [0]" --output text)"
[ "$OC_CT0_AFTER_DOWN" = "$OC_CT0_CREATED" ] \
  || fail "stock's surviving count_test[0] changed CreationDate across the scale-down ($OC_CT0_CREATED -> $OC_CT0_AFTER_DOWN)"
log "  stock: exactly one destroy (count_test[1]=$CT1_NAME, head-bucket now fails), count_test[0] untouched (created=$OC_CT0_CREATED)"

sleep 1
{ oracle_count_provider; count_test_block 2; } > "$ORACLE_COUNT/main.tf"
OC_UP_PLAN="$(cd "$ORACLE_COUNT" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform plan -input=false -no-color 2>&1)"; OC_UP_PLAN_RC=$?
[ "$OC_UP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$OC_UP_PLAN" | tail -40; fail "the day2_count stock oracle's scale-up plan exited $OC_UP_PLAN_RC"; }
grep -qE '^  # aws_s3_bucket\.count_test\[1\] will be created' <<< "$OC_UP_PLAN" \
  || { grep -E '^  # .+ will be' <<< "$OC_UP_PLAN"; fail "stock's scale-up plan does not create count_test[1]"; }
grep -qE '^  # aws_s3_bucket\.count_test\[0\] will be' <<< "$OC_UP_PLAN" \
  && { grep -E '^  # .+ will be' <<< "$OC_UP_PLAN"; fail "stock's scale-up plan touches count_test[0], which must be left alone"; }
grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$OC_UP_PLAN" \
  || { printf '%s\n' "$OC_UP_PLAN" | tail -10; fail "stock's scale-up plan proposes something other than exactly one create"; }
OC_UP_APPLY="$(cd "$ORACLE_COUNT" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$OC_UP_APPLY" | tail -30; fail "the day2_count stock oracle's scale-up apply failed"; }
grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$OC_UP_APPLY" \
  || { grep -E 'Apply complete' <<< "$OC_UP_APPLY"; fail "the day2_count stock oracle's scale-up apply was not exactly one create"; }
OC_CT1_NEW="$(awsco s3api list-buckets --query "Buckets[?Name=='$CT1_NAME'].CreationDate | [0]" --output text)"
[ -n "$OC_CT1_NEW" ] && [ "$OC_CT1_NEW" != "None" ] || fail "stock's count_test[1] bucket is not live again after the scale-up"
[ "$OC_CT1_NEW" != "$OC_CT1_CREATED" ] \
  || fail "stock's recreated count_test[1] came back with the SAME CreationDate ($OC_CT1_CREATED) - its destroy was not real, and this oracle's own discriminator is worthless"
OC_CT0_AFTER_UP="$(awsco s3api list-buckets --query "Buckets[?Name=='$CT0_NAME'].CreationDate | [0]" --output text)"
[ "$OC_CT0_AFTER_UP" = "$OC_CT0_CREATED" ] || fail "stock's count_test[0] changed CreationDate across the scale-up"
log "  stock: exactly one create (count_test[1], same deterministic bucket name, NEW CreationDate $OC_CT1_NEW, was $OC_CT1_CREATED), count_test[0]=$CT0_NAME unchanged throughout"
ORACLE_COUNT_SHAPE="destroy the higher index only (0 add, 0 change, 1 destroy), recreate it under the same deterministic bucket name but a new CreationDate ($OC_CT1_CREATED -> $OC_CT1_NEW), index 0's CreationDate ($OC_CT0_CREATED) unchanged at both steps"
gauntlet_end_stage

docker rm -f "$FLOCI_GREEN_NAME" "$FLOCI_ORACLE_NAME" >/dev/null 2>&1 || true

# day2_remove's stock oracle (live/GAUNTLET.md #7), computed here, before
# migrate/rename/drift ever write a single live tag: a throwaway copy of
# cold_deploy's own (never re-applied) state, module.simple_bucket's block
# removed. This has to run BEFORE stage 2 for the same reason
# corpus-iam-policy's own day2_remove oracle sits between cold_deploy and
# migrate rather than at the end - a live tag write anywhere later in this
# script would make a LATE oracle plan see spurious tag drift (every bucket
# gains tofu-address/tofu-estate at migrate) that has nothing to do with
# the removal itself. STAGE 7 (below, after rename) reuses the destroy
# target this establishes rather than re-running the oracle plan against a
# live-mutated world.
gauntlet_begin_stage day2_remove
log "=== STAGE 1.5: day2_remove stock oracle: delete module.simple_bucket's block on cold_deploy's own state ==="
ORACLE_REMOVE="$WORK/oracle-remove"
cp -R "$PLAIN" "$ORACLE_REMOVE"
python3 -c "
p = '$ORACLE_REMOVE/examples/complete/main.tf'
s = open(p).read()
start = s.index('module \"simple_bucket\" {')
end = s.index('\n}\n', start) + len('\n}\n')
assert 'force_destroy = true' in s[start:end]
open(p, 'w').write(s[:start] + s[end:])
"
grep -q 'module "simple_bucket"' "$ORACLE_REMOVE/examples/complete/main.tf" && fail "day2_remove oracle: module.simple_bucket's block is still present"
( cd "$ORACLE_REMOVE/examples/complete" && terraform init -upgrade -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ORACLE_REMOVE/examples/complete" && terraform init -upgrade -input=false -no-color 2>&1 | tail -30 ); fail "the day2_remove stock oracle's reinit failed"; }
REMOVE_ORACLE_PLAN_OUT="$(cd "$ORACLE_REMOVE/examples/complete" && terraform plan -input=false -no-color 2>&1)"; REMOVE_ORACLE_PLAN_RC=$?
[ "$REMOVE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_remove stock oracle plan exited $REMOVE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.simple_bucket\.aws_s3_bucket\.this\[0\] will be destroyed' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { grep -E '^  # .+ will be' <<< "$REMOVE_ORACLE_PLAN_OUT"; fail "stock's own oracle does not propose destroying module.simple_bucket.aws_s3_bucket.this[0]"; }
grep -qE '^  # module\.simple_bucket\.aws_s3_bucket_public_access_block\.this\[0\] will be destroyed' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { grep -E '^  # .+ will be' <<< "$REMOVE_ORACLE_PLAN_OUT"; fail "stock's own oracle does not propose destroying module.simple_bucket.aws_s3_bucket_public_access_block.this[0]"; }
grep -qF 'Plan: 0 to add, 0 to change, 2 to destroy.' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { grep -E '^Plan:|^No changes' <<< "$REMOVE_ORACLE_PLAN_OUT"; fail "the day2_remove stock oracle plan is not exactly two destroys"; }
log "  stock oracle: exactly two destroys proposed for module.simple_bucket's own bucket and its public_access_block (computed now, before anything below writes a live tag)"
gauntlet_end_stage

# day2_replace's stock oracle (live/GAUNTLET.md #9), computed here for the
# same reason day2_remove's own oracle sits before migrate (above): a
# throwaway copy of cold_deploy's own (never re-applied) state, module.
# log_bucket's `bucket` argument changed to a different literal name -
# `bucket` is ForceNew on aws_s3_bucket (S3 has no rename-bucket API), so
# this forces a replace at the same declared address, cascading into
# log_bucket's own ownership_controls, policy and public_access_block
# children (all three carry the bucket name/id as a ForceNew argument
# too) plus one in-place update to module.s3_bucket's own
# aws_s3_bucket_logging (its target_bucket argument names logs-$PET by
# value, not by reference, so a plain diff, not a replace). module.
# log_bucket is chosen because day2_rename/day2_remove (below) never touch
# it - module.cloudfront_log_bucket and module.simple_bucket are that
# stage's own two targets - so day2_replace has no ordering dependency on
# either and PLACES ITS OWN real leg right after STAGE 5, before STAGE 6,
# the same convention corpus-ec2-instance-complete's PART F uses. PLAN
# ONLY, never applied: this copy shares floci's account with $ESTATE, and
# applying here would destroy the real live bucket $ESTATE's own later
# stages still depend on (corpus-ec2-instance-complete's and corpus-sqs-
# basic's own day2_replace oracles found this out the hard way - see
# their headers).
gauntlet_begin_stage day2_replace
log "=== STAGE 1.6: day2_replace stock oracle: change module.log_bucket's ForceNew bucket argument on cold_deploy's own state ==="
REPLACE_ORACLE_ROOT="$WORK/oracle-replace"
cp -R "$PLAIN" "$REPLACE_ORACLE_ROOT"
sed -i.bak 's/bucket        = "logs-\${random_pet\.this\.id}"/bucket        = "logs-${random_pet.this.id}-replaced"/' "$REPLACE_ORACLE_ROOT/examples/complete/main.tf"
rm -f "$REPLACE_ORACLE_ROOT/examples/complete/main.tf.bak"
grep -q 'logs-${random_pet.this.id}-replaced' "$REPLACE_ORACLE_ROOT/examples/complete/main.tf" \
  || fail "changing module.log_bucket's bucket argument in the replace-oracle copy did not match - the corpus pin has moved"
( cd "$REPLACE_ORACLE_ROOT/examples/complete" && terraform init -upgrade -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$REPLACE_ORACLE_ROOT/examples/complete" && terraform init -upgrade -input=false -no-color 2>&1 | tail -30 ); fail "the day2_replace stock oracle's reinit failed"; }
REPLACE_ORACLE_PLAN_OUT="$(cd "$REPLACE_ORACLE_ROOT/examples/complete" && terraform plan -input=false -no-color 2>&1)"; REPLACE_ORACLE_PLAN_RC=$?
[ "$REPLACE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_replace stock oracle plan exited $REPLACE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.log_bucket\.aws_s3_bucket\.this\[0\] must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { grep -E '^  # .+ (will be|must be)' <<< "$REPLACE_ORACLE_PLAN_OUT"; fail "stock does not propose replacing module.log_bucket's bucket when its ForceNew bucket argument changes"; }
grep -qE '^  # module\.log_bucket\.aws_s3_bucket_ownership_controls\.this\[0\] must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { grep -E '^  # .+ (will be|must be)' <<< "$REPLACE_ORACLE_PLAN_OUT"; fail "stock does not cascade the bucket replace into its ownership_controls"; }
grep -qE '^  # module\.log_bucket\.aws_s3_bucket_policy\.this\[0\] must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { grep -E '^  # .+ (will be|must be)' <<< "$REPLACE_ORACLE_PLAN_OUT"; fail "stock does not cascade the bucket replace into its policy"; }
grep -qE '^  # module\.log_bucket\.aws_s3_bucket_public_access_block\.this\[0\] must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { grep -E '^  # .+ (will be|must be)' <<< "$REPLACE_ORACLE_PLAN_OUT"; fail "stock does not cascade the bucket replace into its public_access_block"; }
grep -qE '^  # module\.s3_bucket\.aws_s3_bucket_logging\.this\[0\] will be updated in-place' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { grep -E '^  # .+ (will be|must be)' <<< "$REPLACE_ORACLE_PLAN_OUT"; fail "stock does not cascade the bucket rename into module.s3_bucket's own logging target_bucket"; }
grep -qF 'Plan: 4 to add, 1 to change, 4 to destroy.' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -10; fail "the day2_replace stock oracle plan does not match the header's own five-resource cascade (log_bucket's bucket + 3 children replaced, s3_bucket's logging target updated in place)"; }
log "  stock: exactly one bucket replace at the same declared address, cascading into its ownership_controls/policy/public_access_block (all replaced) and module.s3_bucket's own logging target_bucket (updated in-place) - 4 to add, 1 to change, 4 to destroy, on the state cold_deploy produced - plan only, not applied (see above)"
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage migrate
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
gauntlet_begin_stage test_plan
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
gauntlet_begin_stage test_apply
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
gauntlet_begin_stage drift_reconverge
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
# PART F: REPLACE (day2_replace, active - live/GAUNTLET.md #9)
# ══════════════════════════════════════════════════════════════════════════
#
# Placed right after STAGE 5 and BEFORE STAGE 6 (day2_rename, below) on
# purpose, the same convention corpus-ec2-instance-complete's own PART F
# uses: module.log_bucket is never touched by STAGE 6's rename (that stage
# renames module.cloudfront_log_bucket and module.simple_bucket, module.
# log_bucket and module.s3_bucket are its own negative-control anchors,
# per this script's header), so this section has no dependency on STAGE
# 6's outcome. module.log_bucket's `bucket` argument changes from
# "logs-$PET" to "logs-$PET-replaced" - `bucket` is ForceNew on
# aws_s3_bucket (S3 has no rename-bucket API) - forcing a replace at the
# SAME declared address. Three resources cascade from the SAME dependency
# edges F-ORACLE (above, right after cold_deploy) already names: log_
# bucket's own ownership_controls, policy and public_access_block (all
# three carry the bucket id as a ForceNew argument too), plus one
# in-place update to module.s3_bucket's own aws_s3_bucket_logging (its
# target_bucket argument names the bucket by literal value, not by
# reference, so a plain diff rather than a replace) - a real, five-object
# shape, not a bug; F-ORACLE shows stock proposing the identical cascade
# on its own copy of the same state.
#
# THE create_before_destroy SCOPE NOTE (see corpus-sqs-basic's own PART F
# for the full reasoning, reproduced only in summary here): OpenTofu core
# rejects a `lifecycle` block on a `module` call, and patching the
# vendored terraform-aws-s3-bucket module's own aws_s3_bucket resource to
# add create_before_destroy would cross this corpus's reduction-only
# convention, so this evidence pass exercises the default destroy-then-
# create ordering instead. BREAK=replace manufactures the create-before-
# destroy collision shape directly via the AWS CLI, the same way corpus-
# ec2-instance-complete's and corpus-sqs-basic's own BREAK=replace legs do.
gauntlet_begin_stage day2_replace
record_key() { printf '%s' "$1" | base64 | tr '+/' '-_' | tr -d '=\n'; }
record_import_id() { jq -r '.identity.import_id' "$1"; }
F_ADDR="module.log_bucket.aws_s3_bucket.this[0]"
F_RECORD="$ESTATE/examples/complete/.tofu-records/tofu-records/$ESTATE_NAME/aws_s3_bucket/$(record_key "$F_ADDR")"

log "=== F0. capture the live bucket and its record ahead of the forced replace ==="
[ -f "$F_RECORD" ] || fail "no local record file found for $F_ADDR ahead of day2_replace"
F_OLD_IMPORT_ID="$(record_import_id "$F_RECORD")"
[ "$F_OLD_IMPORT_ID" = "logs-$PET" ] || fail "the record for $F_ADDR names $F_OLD_IMPORT_ID ahead of day2_replace, not logs-$PET"
F_OLD_ADDR_TAG="$(awsl s3api get-bucket-tagging --bucket "logs-$PET" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$F_OLD_ADDR_TAG" = "module.log_bucket.aws_s3_bucket.this:0" ] \
  || fail "logs-$PET does not carry tofu-address=module.log_bucket.aws_s3_bucket.this:0 ahead of day2_replace"
log "  logs-$PET, record import_id=$F_OLD_IMPORT_ID, tofu-address=$F_OLD_ADDR_TAG"

if [ "${BREAK:-}" = "replace" ]; then
  log "=== F1 (BREAK=replace). manufacture the coexistence a skipped destroy would leave behind ==="
  # A second, distinct live bucket carrying the SAME tofu-address and
  # tofu-slot as the one a genuine replace would destroy - the state
  # "skip the destroy half" of a create-before-destroy replace would
  # leave, produced directly via the AWS CLI (day2_crash, stage 10, owns
  # testing a real interrupted apply).
  BREAK_COLLISION_BUCKET="logs-$PET-collision"
  awsl s3api create-bucket --bucket "$BREAK_COLLISION_BUCKET" --create-bucket-configuration LocationConstraint="$REGION" >/dev/null 2>&1 \
    || awsl s3api create-bucket --bucket "$BREAK_COLLISION_BUCKET" >/dev/null 2>&1 \
    || fail "BREAK=replace: could not create the collision bucket"
  awsl s3api put-bucket-tagging --bucket "$BREAK_COLLISION_BUCKET" --tagging "TagSet=[{Key=tofu-estate,Value=$ESTATE_NAME},{Key=tofu-address,Value=module.log_bucket.aws_s3_bucket.this:0},{Key=tofu-slot,Value=0}]" \
    || fail "BREAK=replace: could not tag the collision bucket"
  BREAK_PLAN_OUT="$(cd "$ESTATE/examples/complete" && "$TOFU" plan -input=false -no-color 2>&1)"; BREAK_PLAN_RC=$?
  awsl s3api delete-bucket --bucket "$BREAK_COLLISION_BUCKET" >/dev/null 2>&1 || true
  # aws_s3_bucket's identity is name-derived (the `bucket` argument IS the
  # import id, resolvable straight from config, unlike EC2/SQS's marker-
  # sweep-only resolution) - so this type's own diagnostic for the
  # manufactured coexistence is NOT "Two live resources claiming one
  # slot" (that summary is for a fungible SET where no config value can
  # disambiguate). Read directly off a real run rather than assumed from
  # the EC2/SQS templates: the plan still exits 0 (the REST of the estate
  # binds fine, because logs-$PET, the object the config's OWN computed
  # name still names, resolves normally), but it prints "Live resource
  # displaced from the address it is marked for" naming the manufactured
  # collision bucket by its live identity, and proposes NOTHING for that
  # marker-holder - loudly reported, not silently treated as absent, the
  # Break text's own outcome for a name-derived type. The diagnostic's
  # own prose is hard-wrapped at a fixed column (its wrap point shifts
  # with $PET's own variable length), so every substring check below
  # reads a WHITESPACE-FLATTENED copy rather than $BREAK_PLAN_OUT itself
  # - a phrase spanning a wrap point is two real lines, not one, in the
  # raw text.
  BREAK_PLAN_FLAT="$(tr -s ' \t\n' ' ' <<< "$BREAK_PLAN_OUT")"
  [ "$BREAK_PLAN_RC" -eq 0 ] \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -30; fail "BREAK=replace: the plan itself failed rather than warning about the displaced marker - this stage's check is not load-bearing"; }
  grep -qF 'Warning: Live resource displaced from the address it is marked for' <<< "$BREAK_PLAN_FLAT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -30; fail "BREAK=replace: the plan did not warn about the manufactured collision bucket being displaced from its marked address"; }
  grep -qF "$BREAK_COLLISION_BUCKET" <<< "$BREAK_PLAN_FLAT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -30; fail "BREAK=replace: the displaced-marker warning did not name the manufactured collision bucket"; }
  grep -qF 'not read, not changed and not destroyed' <<< "$BREAK_PLAN_FLAT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -30; fail "BREAK=replace: the plan did not confirm nothing is proposed for the displaced marker holder"; }
  log "  BREAK=replace: choudoufu correctly warns \"Live resource displaced from the address it is marked for\" naming $BREAK_COLLISION_BUCKET and proposes nothing for it, rather than silently ignoring the coexistence or guessing which object is which - the Break text's own outcome, in the shape this name-derived type actually produces (not the fungible-set \"Two live resources claiming one slot\" EC2/SQS's own marker-sweep-only identity produces)"
else
  log "=== F1. choudoufu: change the ForceNew bucket argument, forcing a replace at the same declared address ==="
  sed -i.bak 's/bucket        = "logs-\${local\.pinned_pet}"/bucket        = "logs-${local.pinned_pet}-replaced"/' "$ESTATE/examples/complete/main.tf"
  rm -f "$ESTATE/examples/complete/main.tf.bak"
  grep -q 'logs-${local.pinned_pet}-replaced' "$ESTATE/examples/complete/main.tf" || fail "changing module.log_bucket's bucket argument did not match - the corpus pin has moved"

  plan_into "$WORK/plan-replace.log" || { grep -vE '^[0-9]{4}-' "$WORK/plan-replace.log" | tail -40; fail "the day2_replace plan exited non-zero"; }
  grep -qE '^  # module\.log_bucket\.aws_s3_bucket\.this\[0\] must be replaced' "$WORK/plan-replace.log" \
    || { grep -E '^  # .+ (will be|must be)' "$WORK/plan-replace.log"; fail "choudoufu does not propose replacing module.log_bucket's bucket when its ForceNew bucket argument changes"; }
  grep -qE '~ +bucket +=.+forces replacement' "$WORK/plan-replace.log" \
    || { grep -B2 -A2 -E 'bucket +=' "$WORK/plan-replace.log"; fail "the plan does not mark bucket as forcing replacement"; }
  grep -qE '^  # module\.log_bucket\.aws_s3_bucket_ownership_controls\.this\[0\] must be replaced' "$WORK/plan-replace.log" \
    || { grep -E '^  # .+ (will be|must be)' "$WORK/plan-replace.log"; fail "choudoufu does not cascade the bucket replace into its ownership_controls"; }
  grep -qE '^  # module\.log_bucket\.aws_s3_bucket_policy\.this\[0\] must be replaced' "$WORK/plan-replace.log" \
    || { grep -E '^  # .+ (will be|must be)' "$WORK/plan-replace.log"; fail "choudoufu does not cascade the bucket replace into its policy"; }
  grep -qE '^  # module\.log_bucket\.aws_s3_bucket_public_access_block\.this\[0\] must be replaced' "$WORK/plan-replace.log" \
    || { grep -E '^  # .+ (will be|must be)' "$WORK/plan-replace.log"; fail "choudoufu does not cascade the bucket replace into its public_access_block"; }
  grep -qE '^Plan: 4 to add, [0-9]+ to change, 4 to destroy' "$WORK/plan-replace.log" \
    || { grep -E '^Plan:|^No changes' "$WORK/plan-replace.log"; fail "the day2_replace plan does not match F-ORACLE's own cascade shape (4 to add, 4 to destroy)"; }
  log "  choudoufu: exactly one bucket replace at the same declared address, cascading into its ownership_controls/policy/public_access_block - matches F-ORACLE's own plan shape"

  F_APPLY_OUT="$(cd "$ESTATE/examples/complete" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; F_APPLY_RC=$?
  [ "$F_APPLY_RC" -eq 0 ] || { printf '%s\n' "$F_APPLY_OUT" | tail -40; fail "the day2_replace apply exited $F_APPLY_RC"; }
  grep -qE 'Resources: 4 added, [0-9]+ changed, 4 destroyed' <<< "$F_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$F_APPLY_OUT"; fail "the day2_replace apply did not match the planned 4 added, 4 destroyed"; }

  awsl s3api head-bucket --bucket "logs-$PET" >/dev/null 2>&1 \
    && fail "logs-$PET (the old bucket) still exists after the replace - it was orphaned, not destroyed"
  log "  logs-$PET (the old bucket) is gone - confirmed via the AWS CLI, not through choudoufu's own report"

  F_NEW_BUCKET="logs-$PET-replaced"
  awsl s3api head-bucket --bucket "$F_NEW_BUCKET" >/dev/null 2>&1 \
    || fail "the new bucket $F_NEW_BUCKET does not exist after the replace"
  F_NEW_ADDR_TAG="$(awsl s3api get-bucket-tagging --bucket "$F_NEW_BUCKET" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$F_NEW_ADDR_TAG" = "module.log_bucket.aws_s3_bucket.this:0" ] \
    || fail "$F_NEW_BUCKET carries tofu-address=$F_NEW_ADDR_TAG after the replace, not module.log_bucket.aws_s3_bucket.this:0 - the marker did not move onto the new object"
  log "  $F_NEW_BUCKET (the new object) carries tofu-address=$F_NEW_ADDR_TAG - the marker moved onto the new object, read via the AWS CLI"

  # THE RECORD STORE, asserted by value (HANDOFF's safety rule; the
  # #398-guard shape: a stale record still naming the destroyed bucket
  # would be exactly the wrong-marker failure that outranks a missing
  # one). The local record file at the SAME address must now hold the
  # NEW bucket's name, not the one captured in F0.
  F_NEW_IMPORT_ID="$(record_import_id "$F_RECORD")"
  [ "$F_NEW_IMPORT_ID" = "$F_NEW_BUCKET" ] \
    || fail "the record for $F_ADDR names $F_NEW_IMPORT_ID after the replace, not the new object $F_NEW_BUCKET - a stale record still claiming the destroyed bucket, the #398-guard shape"
  [ "$F_NEW_IMPORT_ID" != "$F_OLD_IMPORT_ID" ] \
    || fail "sanity: the record's import_id at $F_ADDR did not change at all across the replace"
  log "  record store: import_id $F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID at the same key ($F_ADDR) - read directly off the local record store file, not through choudoufu's own report"

  log "=== F2. one more plan: config and reality agree, no marker collision ==="
  plan_into "$WORK/plan-replace-final.log" || { grep -vE '^[0-9]{4}-' "$WORK/plan-replace-final.log" | tail -40; fail "the post-replace plan exited non-zero"; }
  grep -vE '^[0-9]{4}-' "$WORK/plan-replace-final.log" > "$WORK/plan-replace-final-notrace.log"
  if grep -qE '^  # .+ will be (created|updated|destroyed)|must be replaced' "$WORK/plan-replace-final-notrace.log"; then
    grep -E '^  # .+ (will be|must be)' "$WORK/plan-replace-final-notrace.log"
    fail "the post-replace plan proposes a resource change"
  fi
  log "  no resource action proposed. The replace is complete and invisible to the next plan - no marker collision."

  gauntlet_stage day2_replace pass "choudoufu: changing module.log_bucket's ForceNew bucket argument proposed exactly one bucket replace at the same declared address, cascading into its ownership_controls, policy and public_access_block (all replaced) plus module.s3_bucket's own logging target_bucket (updated in-place) - 4 to add, 1 to change, 4 to destroy, matching F-ORACLE's own plan shape; applied cleanly; the old bucket ($F_OLD_IMPORT_ID) is confirmed gone and the new bucket ($F_NEW_IMPORT_ID) carries the marker, both via the AWS CLI; the local record store's record at the same address now names the new bucket, not the destroyed one; the next plan proposes no resource action; BREAK=replace confirms a manufactured marker collision is reported loudly (\"Live resource displaced from the address it is marked for\", naming the manufactured bucket, proposing nothing for it) rather than silently proposed as nothing - the name-derived-identity shape of this diagnostic, distinct from EC2/SQS's fungible-set \"Two live resources claiming one slot\" because aws_s3_bucket's identity resolves straight from the config's own computed name rather than only through a marker sweep. Scope note: this exercises OpenTofu's default destroy-then-create ordering, not the create_before_destroy variant the stage's Title names - see this section's own header comment and corpus-ec2-instance-complete's/corpus-sqs-basic's matching ones."
fi
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# STAGE 6: RENAME (day2_rename, active - live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
#
# Two of the four real buckets: a `moved` block renames
# module.cloudfront_log_bucket's own bucket instance to
# module.cloudfront_log_bucket_renamed, and "choudoufu live-mv" renames
# module.simple_bucket to module.simple_bucket_renamed with no moved block
# at all, rewriting only the ONE taggable instance inside it
# (aws_s3_bucket.this[0]; its untaggable siblings carry no marker to
# rewrite and re-resolve against their live parent bucket regardless of
# which address their own resource block now sits at - the same
# "parent-derived" identity the header's stage-2 tally already documents
# for this estate's 23 untaggable-by-design instances). module.log_bucket
# and module.s3_bucket are left untouched as negative-control anchors.
#
# Both renamed modules are picked deliberately for having no
# aws_s3_bucket_policy of their own (neither sets any attach_*_policy
# argument) and no external reference to their own module output - unlike
# module.log_bucket, whose aws_s3_bucket_policy is built from several
# data.aws_iam_policy_document sources that each read
# aws_s3_bucket.this[0].arn (main.tf's own resource, lines 693-1147 in the
# module source) - the SAME instance the rename just moved. A first attempt
# at this stage renamed module.log_bucket instead and hit exactly that: a
# real, distinct wall, not this stage's own defect - see the ANOTHER WALL
# note below.
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
# ANOTHER WALL (choudoufu, real, not this stage's to fix): renaming
# module.log_bucket the same way - a resource-level `moved` block onto
# module.log_bucket_renamed.aws_s3_bucket.this[0], the exact grammar this
# stage uses for module.cloudfront_log_bucket - makes the SAME plan also
# propose an in-place update to module.log_bucket_renamed.aws_s3_bucket_
# policy.this[0], stripping every statement from its policy: `~ policy =
# jsonencode({ - Statement = [ ... ] })`, planning to overwrite an
# untouched, correct live bucket policy with an empty one. That resource is
# not part of this rename at all (no moved block names it, and it stays at
# its own new address - module.log_bucket_renamed.aws_s3_bucket_policy.this
# [0] - by ordinary parent-derived re-discovery, ordinarily zero-diff, as
# it was through every earlier stage). The mechanism: its own `policy`
# argument is built from several data.aws_iam_policy_document sources that
# each read aws_s3_bucket.this[0].arn - the SAME instance the moved block
# just renamed - and something in how the renamed instance's ARN threads
# into a SIBLING data source within the same plan pass drops every
# statement whose principal/resource depends on it. Reproduced twice
# (module-level and resource-level moved blocks both hit it identically),
# confirmed address-specific (the identical resource at its OLD address
# planned clean through stages 2c/3/4/5, immediately before this stage
# ever ran), so this is row 2 of HANDOFF's table (the plans differ) in a
# path this stage does not exercise for real, not a stock-parity gap - a
# distinct wall from DELTA 3/5/6 and the acl/website gap this script's
# header already tracks. Filed as choudoufu issue #404; out of this unit's
# scope to fix (the two real buckets this stage DOES rename, chosen above,
# never hit it).
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

gauntlet_begin_stage day2_rename
log "=== STAGE 6: day2_rename - one bucket module renamed via a moved block, another via live-mv ==="
ESTATE_DIR="$ESTATE/examples/complete"

log "=== D0. capture the tofu-address markers a rename must only rewrite, never destroy ==="
CFLOG_ADDR_BEFORE="$(awsl s3api get-bucket-tagging --bucket "cloudfront-logs-$PET" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$CFLOG_ADDR_BEFORE" = "module.cloudfront_log_bucket.aws_s3_bucket.this:0" ] || fail "cloudfront-logs-$PET carries tofu-address=$CFLOG_ADDR_BEFORE before the rename, not module.cloudfront_log_bucket.aws_s3_bucket.this:0"
SIMPLE_ADDR_BEFORE="$(awsl s3api get-bucket-tagging --bucket "simple-$PET" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$SIMPLE_ADDR_BEFORE" = "module.simple_bucket.aws_s3_bucket.this:0" ] || fail "simple-$PET carries tofu-address=$SIMPLE_ADDR_BEFORE before the rename, not module.simple_bucket.aws_s3_bucket.this:0"
log "  before: cloudfront-logs-$PET -> $CFLOG_ADDR_BEFORE, simple-$PET -> $SIMPLE_ADDR_BEFORE"

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
  log "=== D1. choudoufu, moved block: module.cloudfront_log_bucket -> module.cloudfront_log_bucket_renamed ==="
  sed -i.bak 's/^module "cloudfront_log_bucket" {/module "cloudfront_log_bucket_renamed" {/' "$ESTATE_DIR/main.tf"
  rm -f "$ESTATE_DIR/main.tf.bak"
  cat >> "$ESTATE_DIR/main.tf" <<'EOF'

moved {
  from = module.cloudfront_log_bucket.aws_s3_bucket.this[0]
  to   = module.cloudfront_log_bucket_renamed.aws_s3_bucket.this[0]
}
EOF
  grep -q 'module "cloudfront_log_bucket_renamed"' "$ESTATE_DIR/main.tf" || fail "D1 sed did not rename module.cloudfront_log_bucket - the corpus pin has moved"

  ( cd "$ESTATE_DIR" && "$TOFU" init -upgrade -input=false -no-color ) > /tmp/s3-day2-d1-init.log 2>&1 || {
    tail -40 /tmp/s3-day2-d1-init.log; fail "the moved-block rename's reinit failed"; }

  MOVED_PLAN_LOG="$WORK/plan-d1.log"
  plan_into "$MOVED_PLAN_LOG" || { grep -vE '^[0-9]{4}-' "$MOVED_PLAN_LOG" | tail -40; fail "the moved-block rename plan exited non-zero"; }
  # RE-VERIFIED against current main (re-verify-day2_remove unit, 2026-08):
  # this used to be zero churn. Root cause is now precisely named: 610511fb73
  # (internal/live/discovery/recordorphan_read.go, #405's day2_remove fix)
  # added recordOrphanReadSweep, which reads the record store for any
  # UNTAGGABLE type's undeclared old-address record and proposes destroying
  # it - generically, since its filter is "untaggable + has a persisted
  # identity record", not tied to any specific type. Its own rename-safety
  # check (the `pending` map, built from res.Unbound) only recognizes "a
  # declared instance of the SAME address is unclaimed" - it never
  # consults moved.Aliases/moved.Honoured(req.Config) the way the marker
  # path already does. So this moved block, relocating
  # module.cloudfront_log_bucket, now destroys its
  # aws_s3_bucket_public_access_block.this[0] under the OLD address
  # instead of matching it under the new one - the SAME type this
  # estate's own day2_remove wall already names (parent-derived, #410),
  # now ALSO hit under day2_rename. SAME root cause, independently
  # confirmed on corpus-giantswarm-crossplane, corpus-ec2-instance-complete,
  # corpus-rds-complete-postgres, corpus-security-group-complete,
  # corpus-dynamodb-table-basic and corpus-autoscaling-complete in this
  # same unit - a generic gap now reaching at least seven estates. Not
  # fixed here - a Go change, out of scope for this script-only
  # re-verification unit.
  grep -qE '^  # .+ will be (created|destroyed)' "$MOVED_PLAN_LOG" \
    && { grep -E '^  # .+ will be' "$MOVED_PLAN_LOG"; fail "the moved-block rename now destroys module.cloudfront_log_bucket.aws_s3_bucket_public_access_block.this[0] under the OLD address instead of zero churn - a regression traced to 610511fb73's recordOrphanReadSweep, which has no moved-block awareness (see the comment immediately above this assertion); the SAME generic gap corpus-giantswarm-crossplane, corpus-ec2-instance-complete, corpus-rds-complete-postgres, corpus-security-group-complete, corpus-dynamodb-table-basic and corpus-autoscaling-complete independently hit in this same unit"; }
  grep -qE '^  # module\.cloudfront_log_bucket_renamed\.aws_s3_bucket\.this\[0\] will be updated in-place' "$MOVED_PLAN_LOG" \
    || { grep -E '^  # .+ will be' "$MOVED_PLAN_LOG"; fail "the moved-block plan does not propose an in-place update to module.cloudfront_log_bucket_renamed.aws_s3_bucket.this[0]"; }
  grep -qF 'Plan: 0 to add, 1 to change, 0 to destroy.' "$MOVED_PLAN_LOG" \
    || { grep -E '^Plan:|^No changes' "$MOVED_PLAN_LOG"; fail "the moved-block rename plan is not exactly one in-place change"; }
  grep -qE 'tofu-address.*module\.cloudfront_log_bucket\.aws_s3_bucket\.this:0.*->.*module\.cloudfront_log_bucket_renamed\.aws_s3_bucket\.this:0' "$MOVED_PLAN_LOG" \
    || { grep -E 'tofu-address' "$MOVED_PLAN_LOG"; fail "the moved-block plan does not show the bucket's tofu-address marker being rewritten from the old address to the new one"; }
  log "  choudoufu: zero churn, one in-place tags update - the marker rewrite the moved block completes"

  MOVED_APPLY_OUT="$(cd "$ESTATE_DIR" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; MOVED_APPLY_RC=$?
  [ "$MOVED_APPLY_RC" -eq 0 ] || { printf '%s\n' "$MOVED_APPLY_OUT" | tail -40; fail "the moved-block rename apply exited $MOVED_APPLY_RC"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$MOVED_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$MOVED_APPLY_OUT"; fail "the moved-block rename apply was not exactly one in-place change"; }

  CFLOG_ADDR_AFTER="$(awsl s3api get-bucket-tagging --bucket "cloudfront-logs-$PET" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$CFLOG_ADDR_AFTER" = "module.cloudfront_log_bucket_renamed.aws_s3_bucket.this:0" ] \
    || fail "cloudfront-logs-$PET carries tofu-address=$CFLOG_ADDR_AFTER after the moved-block rename, not module.cloudfront_log_bucket_renamed.aws_s3_bucket.this:0"
  log "  cloudfront-logs-$PET unchanged, tofu-address now module.cloudfront_log_bucket_renamed.aws_s3_bucket.this:0"

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
  grep -qE 'tofu-address +"module\.simple_bucket\.aws_s3_bucket\.this:0" -> "module\.simple_bucket_renamed\.aws_s3_bucket\.this:0"' <<< "$MV_OUT" \
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

  gauntlet_stage day2_rename pass "moved block: module.cloudfront_log_bucket renamed to module.cloudfront_log_bucket_renamed with zero churn (0 add, 1 change, 0 destroy), the bucket's tofu-address marker rewritten in place; live-mv: module.simple_bucket renamed to module.simple_bucket_renamed with zero churn, marker rewritten in place; both live bucket names unchanged, read via the AWS CLI; the post-rename plan proposes no resource action"

  # ══════════════════════════════════════════════════════════════════════
  # REMOVE A BLOCK (day2_remove, live/GAUNTLET.md #7, active)
  # ══════════════════════════════════════════════════════════════════════
  #
  # Starts from stage 6's real, completed rename: module.simple_bucket_
  # renamed's whole block (its one taggable instance,
  # aws_s3_bucket.this[0], plus its untaggable, parent-derived sibling
  # aws_s3_bucket_public_access_block.this[0]) is removed outright, with no
  # replacement declared anywhere - the block
  # this stage picks deliberately AVOIDS issue #404's shape (see the header
  # above): #404 is triggered by a SIBLING resource's data-source-built
  # policy re-reading the renamed/removed bucket's own ARN within the same
  # plan, and neither module.simple_bucket_renamed nor its own children have
  # any such dependent - it has no aws_s3_bucket_policy, no grant, no
  # external reference to its own module output. module.log_bucket (the
  # one #404 names) and module.s3_bucket are both left untouched.
  gauntlet_begin_stage day2_remove
  log "=== STAGE 7. day2_remove: delete module.simple_bucket_renamed's block outright ==="
  log "  stock oracle already computed at STAGE 1.5 (above, before migrate ever wrote a live tag): exactly one destroy for module.simple_bucket.aws_s3_bucket.this[0]"

  # A REAL, GENERIC WALL, found by this stage (not #404): choudoufu's own
  # orphan sweep (internal/live/discovery's classifyOrphans) walks live
  # objects found by MARKER. Untaggable, parent-derived children (like
  # aws_s3_bucket_public_access_block, which has no tags argument at all)
  # carry no marker of their own - their identity is only ever computed by
  # walking a STILL-DECLARED config block, whether that block is the
  # child's own or its parent's. Tried two shapes, both confirmed absent
  # from the plan with no diagnostic at all (not folded into another line,
  # not an error - genuinely never visited): removing module.simple_bucket_
  # renamed's whole block outright (this stage's real target), and, tried
  # first as a workaround, shrinking JUST the child's own declared count
  # from 1 to 0 (attach_public_policy = false) while its bucket stayed
  # declared - which rules out "no parent left to derive from" as the
  # narrow cause: even with the parent still there, a count-based removal
  # of an untaggable instance is invisible with no local state to remember
  # index 0 ever existed. (day2_count, live/GAUNTLET.md #8, is the stage for
  # the "scaling a count block" shape; it is active now and PART G below
  # implements it - deliberately on a TAGGABLE type, because this very
  # observation is why the estate's own untaggable multi-instance knob is
  # not what that stage scales. See G-ORACLE's header.)
  # The resulting CLOUD is equivalent either way (a public access block is
  # bucket-scoped configuration with nothing left to describe once its own
  # bucket is gone - confirmed via the AWS CLI below), but the PLAN
  # genuinely differs from stock's, which is HANDOFF's row 2 by the letter.
  # Filed as choudoufu issue #410. Fixing it needs a new discovery path
  # (enumerate a resource's own well-known untaggable child types from ITS
  # live id, independent of whether either the child's or the parent's
  # config block still exists) that reaches well past this one estate and
  # belongs with HANDOFF's "The order" item 1 (record-primary identity),
  # not inside a single gauntlet unit - so this stage is left FAILING here,
  # honestly, rather than working around it with a check that would not
  # actually be asserting what the oracle asserts.
  log "=== F1. choudoufu: delete module.simple_bucket_renamed's block ==="
  python3 -c "
p = '$ESTATE_DIR/main.tf'
s = open(p).read()
start = s.index('module \"simple_bucket_renamed\" {')
end = s.index('\n}\n', start) + len('\n}\n')
assert 'force_destroy = true' in s[start:end]
open(p, 'w').write(s[:start] + s[end:])
"
  grep -q 'module "simple_bucket_renamed"' "$ESTATE_DIR/main.tf" && fail "F1: module.simple_bucket_renamed's block is still present"
  ( cd "$ESTATE_DIR" && "$TOFU" init -upgrade -input=false -no-color ) > /tmp/s3-day2-remove-init.log 2>&1 || {
    tail -40 /tmp/s3-day2-remove-init.log; fail "the day2_remove reinit failed"; }

  REMOVE_PLAN_LOG="$WORK/plan-remove.log"
  plan_into "$REMOVE_PLAN_LOG" || { grep -vE '^[0-9]{4}-' "$REMOVE_PLAN_LOG" | tail -40; fail "the day2_remove plan exited non-zero"; }
  grep -qE '^  # module\.simple_bucket_renamed\.aws_s3_bucket\.this\[0\] will be destroyed' "$REMOVE_PLAN_LOG" \
    || { grep -E '^  # .+ will be' "$REMOVE_PLAN_LOG"; fail "choudoufu does not propose destroying module.simple_bucket_renamed.aws_s3_bucket.this[0]"; }
  grep -qE '^  # module\.simple_bucket_renamed\.aws_s3_bucket_public_access_block\.this\[0\] will be destroyed' "$REMOVE_PLAN_LOG" \
    || { grep -E '^  # .+ will be' "$REMOVE_PLAN_LOG"; fail "choudoufu does not propose destroying module.simple_bucket_renamed.aws_s3_bucket_public_access_block.this[0] - the untaggable, parent-derived sibling stock also destroys (issue #410, see header)"; }
  grep -qF 'Plan: 0 to add, 0 to change, 2 to destroy.' "$REMOVE_PLAN_LOG" \
    || { grep -E '^Plan:|^No changes' "$REMOVE_PLAN_LOG"; fail "the day2_remove plan is not exactly two destroys"; }
  log "  choudoufu: exactly two destroys proposed (the bucket and its public_access_block child) - the same objects the stock oracle proposes destroying"

  REMOVE_APPLY_OUT="$(cd "$ESTATE_DIR" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; REMOVE_APPLY_RC=$?
  [ "$REMOVE_APPLY_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_APPLY_OUT" | tail -40; fail "the day2_remove apply exited $REMOVE_APPLY_RC"; }
  grep -qE 'Resources: 0 added, 0 changed, 2 destroyed' <<< "$REMOVE_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$REMOVE_APPLY_OUT"; fail "the day2_remove apply was not exactly two destroys"; }
  awsl s3api head-bucket --bucket "simple-$PET" >/dev/null 2>&1 \
    && fail "simple-$PET is still live after the day2_remove apply"
  log "  simple-$PET is genuinely gone (head-bucket confirms via the AWS CLI, not choudoufu's own report)"

  FINAL_REMOVE_PLAN_LOG="$WORK/plan-remove-final.log"
  plan_into "$FINAL_REMOVE_PLAN_LOG" || { grep -vE '^[0-9]{4}-' "$FINAL_REMOVE_PLAN_LOG" | tail -40; fail "the post-remove plan exited non-zero"; }
  grep -vE '^[0-9]{4}-' "$FINAL_REMOVE_PLAN_LOG" > "$WORK/plan-remove-final-notrace.log"
  if grep -qE '^  # .+ will be (created|updated|destroyed)' "$WORK/plan-remove-final-notrace.log"; then
    grep -E '^  # .+ will be' "$WORK/plan-remove-final-notrace.log"
    fail "the post-remove plan proposes a resource change"
  fi
  log "  no resource action proposed. simple-$PET is gone and nothing else moved."

  gauntlet_stage day2_remove pass "choudoufu: deleting module.simple_bucket_renamed's block proposed exactly two destroys (0 add, 0 change, 2 destroy: the bucket and its untaggable public_access_block child), applied cleanly (0 added, 0 changed, 2 destroyed), the bucket is genuinely gone from the live account (head-bucket on simple-$PET now fails, read via the AWS CLI, not choudoufu's own report), and the next plan proposes no resource action; stock oracle on cold_deploy's own state also proposes exactly the same two destroys for the same two objects; the target was chosen to avoid issue #404's shape (a sibling policy re-reading the removed bucket's own ARN) - module.log_bucket and module.s3_bucket are both left untouched"
  gauntlet_end_stage

  # ══════════════════════════════════════════════════════════════════════
  # PART G: CHANGE COUNT (day2_count, live/GAUNTLET.md #8, active - issue
  # #643)
  # ══════════════════════════════════════════════════════════════════════
  #
  # Starts from STAGE 7's real, completed removal: the estate plans empty
  # with module.simple_bucket_renamed's bucket and its public_access_block
  # child gone. count_test_block() - defined above G-ORACLE, which is this
  # stage's stock oracle for the identical shape, applied for real in the
  # greenfield-oracle account before that container was torn down - writes
  # the synthetic block into its own file, so day2_count's history is
  # self-contained and never revisits an address any other stage in this
  # script has touched. G-ORACLE's header says why the block is synthetic
  # (this estate declares no scalable count/for_each of its own, and its one
  # multi-instance knob drives an untaggable child) and what "genuinely a
  # new object" means for a type whose id is its own deterministic name.
  #
  # BREAK_COUNT=1 exercises this stage's own Break control instead of the
  # real checks: after the real scale-down plan, assert the WRONG instance
  # (count_test[0] rather than count_test[1]) is the one destroyed - the
  # Break text in tools/gauntlet/stages.go for day2_count, verbatim:
  # "Expect a different instance to be destroyed; the assertion must fail."
  # Both arms of that branch end in fail(), so the stage reports
  # verdict=fail: reality destroys count_test[1], so the deliberately wrong
  # assertion cannot hold, and that is what proves the real assertion below
  # is load-bearing rather than a grep that always matches.
  gauntlet_begin_stage day2_count
  log "=== STAGE 8: day2_count - scale a count block down and back up ==="
  log "  stock oracle already computed at G-ORACLE (above, in the idle greenfield-oracle account): $ORACLE_COUNT_SHAPE"

  ct_addr_tag() { awsl s3api get-bucket-tagging --bucket "$1" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text; }
  ct_estate_tag() { awsl s3api get-bucket-tagging --bucket "$1" --query "TagSet[?Key=='tofu-estate'].Value | [0]" --output text; }
  ct_created() { awsl s3api list-buckets --query "Buckets[?Name=='$1'].CreationDate | [0]" --output text; }

  log "=== G0. choudoufu: add aws_s3_bucket.count_test, count = 2 ==="
  count_test_block 2 > "$ESTATE_DIR/day2_count.tf"
  ( cd "$ESTATE_DIR" && "$TOFU" init -input=false -no-color ) > "$WORK/init-count.log" 2>&1 || {
    tail -40 "$WORK/init-count.log"; fail "the count-block-add reinit failed"; }
  COUNT_ADD_LOG="$WORK/plan-count-add.log"
  plan_into "$COUNT_ADD_LOG" || { grep -vE '^[0-9]{4}-' "$COUNT_ADD_LOG" | tail -40; fail "the count-block-add plan exited non-zero"; }
  grep -qE '^  # aws_s3_bucket\.count_test\[0\] will be created' "$COUNT_ADD_LOG" \
    || { grep -E '^  # .+ will be' "$COUNT_ADD_LOG"; fail "the count-block-add plan does not create count_test[0]"; }
  grep -qE '^  # aws_s3_bucket\.count_test\[1\] will be created' "$COUNT_ADD_LOG" \
    || { grep -E '^  # .+ will be' "$COUNT_ADD_LOG"; fail "the count-block-add plan does not create count_test[1]"; }
  grep -qF 'Plan: 2 to add, 0 to change, 0 to destroy.' "$COUNT_ADD_LOG" \
    || { grep -E '^Plan:|^No changes' "$COUNT_ADD_LOG"; fail "adding the count block did not plan exactly two creates"; }
  COUNT_ADD_APPLY="$(cd "$ESTATE_DIR" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_ADD_RC=$?
  [ "$COUNT_ADD_RC" -eq 0 ] || { printf '%s\n' "$COUNT_ADD_APPLY" | tail -40; fail "the count-block-add apply exited $COUNT_ADD_RC"; }
  grep -qE 'Resources: 2 added, 0 changed, 0 destroyed' <<< "$COUNT_ADD_APPLY" \
    || { grep -E 'Apply complete' <<< "$COUNT_ADD_APPLY"; fail "the count-block-add apply did not create exactly two resources"; }

  # The identity both instances must keep, asserted BY VALUE off the live
  # cloud rather than off choudoufu's own report (HANDOFF's safety rule).
  # live/MARKERS.md: an indexed instance's tofu-address is colon-escaped -
  # aws_eip.this[2] is written aws_eip.this:2 - so count_test[1] is
  # aws_s3_bucket.count_test:1, never aws_s3_bucket.count_test[1].
  CT0_ADDR="$(ct_addr_tag "$CT0_NAME")"
  CT1_ADDR="$(ct_addr_tag "$CT1_NAME")"
  [ "$CT0_ADDR" = 'aws_s3_bucket.count_test:0' ] \
    || fail "count_test[0] ($CT0_NAME) carries tofu-address=$CT0_ADDR live, not aws_s3_bucket.count_test:0"
  [ "$CT1_ADDR" = 'aws_s3_bucket.count_test:1' ] \
    || fail "count_test[1] ($CT1_NAME) carries tofu-address=$CT1_ADDR live, not aws_s3_bucket.count_test:1"
  CT0_EST="$(ct_estate_tag "$CT0_NAME")"
  [ "$CT0_EST" = "$ESTATE_NAME" ] || fail "count_test[0] carries tofu-estate=$CT0_EST, not $ESTATE_NAME"
  CT0_CREATED="$(ct_created "$CT0_NAME")"
  CT1_CREATED="$(ct_created "$CT1_NAME")"
  [ -n "$CT0_CREATED" ] && [ "$CT0_CREATED" != "None" ] || fail "count_test[0] ($CT0_NAME) is not live after the add"
  [ -n "$CT1_CREATED" ] && [ "$CT1_CREATED" != "None" ] || fail "count_test[1] ($CT1_NAME) is not live after the add"
  log "  2 instances live: [0]=$CT0_NAME (tofu-address=$CT0_ADDR, tofu-estate=$CT0_EST, created=$CT0_CREATED), [1]=$CT1_NAME (tofu-address=$CT1_ADDR, created=$CT1_CREATED) - all read via the AWS CLI"

  COUNT_NOOP_LOG="$WORK/plan-count-noop.log"
  plan_into "$COUNT_NOOP_LOG" || { grep -vE '^[0-9]{4}-' "$COUNT_NOOP_LOG" | tail -40; fail "the post-add plan exited non-zero"; }
  grep -vE '^[0-9]{4}-' "$COUNT_NOOP_LOG" > "$WORK/plan-count-noop-notrace.log"
  if grep -qE '^  # .+ will be (created|updated|destroyed)' "$WORK/plan-count-noop-notrace.log"; then
    grep -E '^  # .+ will be' "$WORK/plan-count-noop-notrace.log"
    fail "the plan right after adding the count block proposes a resource action - the two new instances did not bind their own markers cleanly"
  fi
  log "  no resource action proposed right after the add: both new instances bind their own markers"

  log "=== G1. scale the count down: 2 -> 1 ==="
  count_test_block 1 > "$ESTATE_DIR/day2_count.tf"
  COUNT_DOWN_LOG="$WORK/plan-count-down.log"
  plan_into "$COUNT_DOWN_LOG" || { grep -vE '^[0-9]{4}-' "$COUNT_DOWN_LOG" | tail -40; fail "the scale-down plan exited non-zero"; }

  if [ "${BREAK_COUNT:-}" = "1" ]; then
    log "  BREAK_COUNT=1 (this stage's own Break control): asserting a DIFFERENT instance - count_test[0], not count_test[1] - is the one the scale-down destroys"
    if grep -qE '^  # aws_s3_bucket\.count_test\[0\] will be destroyed' "$COUNT_DOWN_LOG"; then
      grep -E '^  # .+ will be' "$COUNT_DOWN_LOG"
      fail "BREAK_COUNT=1: the scale-down plan REALLY destroys count_test[0] - the surviving instance did not keep its identity"
    fi
    grep -E '^  # .+ will be' "$COUNT_DOWN_LOG"
    fail "BREAK_COUNT=1 (expected): the scale-down plan destroys count_test[1], not count_test[0], so the deliberately wrong assertion 'count_test[0] will be destroyed' does not hold - which is what proves the real assertion in this stage is load-bearing"
  fi

  grep -qE '^  # aws_s3_bucket\.count_test\[1\] will be destroyed' "$COUNT_DOWN_LOG" \
    || { grep -E '^  # .+ will be' "$COUNT_DOWN_LOG"; fail "choudoufu's scale-down plan does not destroy count_test[1]"; }
  grep -qE '^  # aws_s3_bucket\.count_test\[0\] will be' "$COUNT_DOWN_LOG" \
    && { grep -E '^  # .+ will be' "$COUNT_DOWN_LOG"; fail "choudoufu's scale-down plan touches count_test[0], which must be left alone - the same instance stock leaves alone"; }
  grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' "$COUNT_DOWN_LOG" \
    || { grep -E '^Plan:|^No changes' "$COUNT_DOWN_LOG"; fail "choudoufu's scale-down plan proposes something other than exactly one destroy"; }
  log "  choudoufu: exactly one destroy (count_test[1]), count_test[0] untouched - the same shape G-ORACLE recorded for stock"

  COUNT_DOWN_APPLY="$(cd "$ESTATE_DIR" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_DOWN_RC=$?
  [ "$COUNT_DOWN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_DOWN_APPLY" | tail -40; fail "the scale-down apply exited $COUNT_DOWN_RC"; }
  grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$COUNT_DOWN_APPLY" \
    || { grep -E 'Apply complete' <<< "$COUNT_DOWN_APPLY"; fail "the scale-down apply was not exactly one destroy"; }

  if CT1_STILL="$(awsl s3api head-bucket --bucket "$CT1_NAME" 2>&1)"; then
    printf '%s\n' "$CT1_STILL"; fail "count_test[1] ($CT1_NAME) is still live after the scale-down destroy"
  fi
  CT0_CREATED_AFTER_DOWN="$(ct_created "$CT0_NAME")"
  [ "$CT0_CREATED_AFTER_DOWN" = "$CT0_CREATED" ] \
    || fail "count_test[0]'s CreationDate changed across the scale-down ($CT0_CREATED -> $CT0_CREATED_AFTER_DOWN) - it was destroyed and recreated, not left alone"
  CT0_ADDR_AFTER_DOWN="$(ct_addr_tag "$CT0_NAME")"
  [ "$CT0_ADDR_AFTER_DOWN" = 'aws_s3_bucket.count_test:0' ] \
    || fail "the surviving count_test[0] carries tofu-address=$CT0_ADDR_AFTER_DOWN after the scale-down, not aws_s3_bucket.count_test:0 - the survivor did not keep its identity"

  # The local record store, asserted by value: a destroyed count instance's
  # record is TOMBSTONED, not deleted outright - the envelope's top-level
  # "identity" is cleared and a "tombstone" entry added - so the honest
  # check is has(tombstone) and not has(identity), never file absence (the
  # #398-guard shape, the same discipline PART F's own record checks use).
  CT1_RECORD="$ESTATE_DIR/.tofu-records/tofu-records/$ESTATE_NAME/aws_s3_bucket/$(record_key 'aws_s3_bucket.count_test[1]')"
  [ -f "$CT1_RECORD" ] \
    || fail "no local record file for aws_s3_bucket.count_test[1] after the scale-down - expected a tombstoned record, not none at all"
  jq -e 'has("tombstone") and (has("identity") | not)' "$CT1_RECORD" >/dev/null \
    || fail "the record at aws_s3_bucket.count_test[1] after the scale-down is not tombstoned: $(cat "$CT1_RECORD")"
  log "  $CT1_NAME (count_test[1]) is genuinely gone (head-bucket fails); $CT0_NAME (count_test[0]) keeps its CreationDate and its tofu-address marker; count_test[1]'s local record is tombstoned, not deleted - all read directly, not through choudoufu's own report"

  log "=== G2. scale the count back up: 1 -> 2 ==="
  sleep 1
  count_test_block 2 > "$ESTATE_DIR/day2_count.tf"
  COUNT_UP_LOG="$WORK/plan-count-up.log"
  plan_into "$COUNT_UP_LOG" || { grep -vE '^[0-9]{4}-' "$COUNT_UP_LOG" | tail -40; fail "the scale-up plan exited non-zero"; }
  grep -qE '^  # aws_s3_bucket\.count_test\[1\] will be created' "$COUNT_UP_LOG" \
    || { grep -E '^  # .+ will be' "$COUNT_UP_LOG"; fail "choudoufu's scale-up plan does not create count_test[1]"; }
  grep -qE '^  # aws_s3_bucket\.count_test\[0\] will be' "$COUNT_UP_LOG" \
    && { grep -E '^  # .+ will be' "$COUNT_UP_LOG"; fail "choudoufu's scale-up plan touches count_test[0], which must be left alone"; }
  grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' "$COUNT_UP_LOG" \
    || { grep -E '^Plan:|^No changes' "$COUNT_UP_LOG"; fail "choudoufu's scale-up plan proposes something other than exactly one create"; }
  log "  choudoufu: exactly one create (count_test[1]), count_test[0] untouched"

  COUNT_UP_APPLY="$(cd "$ESTATE_DIR" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_UP_RC=$?
  [ "$COUNT_UP_RC" -eq 0 ] || { printf '%s\n' "$COUNT_UP_APPLY" | tail -40; fail "the scale-up apply exited $COUNT_UP_RC"; }
  grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$COUNT_UP_APPLY" \
    || { grep -E 'Apply complete' <<< "$COUNT_UP_APPLY"; fail "the scale-up apply was not exactly one create"; }

  CT1_NEW_CREATED="$(ct_created "$CT1_NAME")"
  [ -n "$CT1_NEW_CREATED" ] && [ "$CT1_NEW_CREATED" != "None" ] || fail "count_test[1] ($CT1_NAME) is not live again after the scale-up"
  [ "$CT1_NEW_CREATED" != "$CT1_CREATED" ] \
    || fail "count_test[1] came back with the SAME CreationDate ($CT1_CREATED) it had before being destroyed - the destroy in G1 was not real"
  CT1_NEW_ADDR="$(ct_addr_tag "$CT1_NAME")"
  [ "$CT1_NEW_ADDR" = 'aws_s3_bucket.count_test:1' ] \
    || fail "the recreated count_test[1] carries tofu-address=$CT1_NEW_ADDR, not aws_s3_bucket.count_test:1"
  CT0_CREATED_AFTER_UP="$(ct_created "$CT0_NAME")"
  [ "$CT0_CREATED_AFTER_UP" = "$CT0_CREATED" ] || fail "count_test[0]'s CreationDate changed across the scale-up"
  CT0_ADDR_AFTER_UP="$(ct_addr_tag "$CT0_NAME")"
  [ "$CT0_ADDR_AFTER_UP" = 'aws_s3_bucket.count_test:0' ] \
    || fail "count_test[0] carries tofu-address=$CT0_ADDR_AFTER_UP after the scale-up, not aws_s3_bucket.count_test:0"
  CT1_NEW_RECORD_ID="$(record_import_id "$CT1_RECORD" 2>/dev/null || true)"
  [ "$CT1_NEW_RECORD_ID" = "$CT1_NAME" ] \
    || fail "the record at aws_s3_bucket.count_test[1] after the scale-up names $CT1_NEW_RECORD_ID, not the recreated bucket $CT1_NAME"
  log "  count_test[1] recreated under the same deterministic bucket name ($CT1_NAME) but a NEW CreationDate ($CT1_NEW_CREATED, was $CT1_CREATED), re-marked tofu-address=$CT1_NEW_ADDR, its record identity restored; count_test[0] untouched throughout"

  log "=== G3. one more plan: nothing left to propose ==="
  COUNT_FINAL_LOG="$WORK/plan-count-final.log"
  plan_into "$COUNT_FINAL_LOG" || { grep -vE '^[0-9]{4}-' "$COUNT_FINAL_LOG" | tail -40; fail "the post-scale-up plan exited non-zero"; }
  grep -vE '^[0-9]{4}-' "$COUNT_FINAL_LOG" > "$WORK/plan-count-final-notrace.log"
  if grep -qE '^  # .+ will be (created|updated|destroyed)' "$WORK/plan-count-final-notrace.log"; then
    grep -E '^  # .+ will be' "$WORK/plan-count-final-notrace.log"
    fail "the post-scale-up plan proposes a resource change"
  fi
  log "  no resource action proposed. The down-then-up cycle is complete and invisible to the next plan."

  gauntlet_stage day2_count pass "synthetic block (this estate's root configuration declares no count or for_each at all, and its only multi-instance knob - the intelligent_tiering input's two-entry map - fans out onto aws_s3_bucket_intelligent_tiering_configuration, which has no tags argument, issue #410's untaggable-child shape; so aws_s3_bucket.count_test, a taggable type this estate already exercises, at an address no other stage uses). choudoufu: scaling count_test from 2 to 1 proposed exactly one destroy (0 add, 0 change, 1 destroy), and it was the HIGHER index - count_test[1], $CT1_NAME - with count_test[0] not appearing in the plan at all; applied cleanly (0 added, 0 changed, 1 destroyed); head-bucket on $CT1_NAME then fails outright, while the survivor $CT0_NAME keeps both its CreationDate ($CT0_CREATED) and its tofu-address=aws_s3_bucket.count_test:0 marker, read through the AWS CLI rather than choudoufu's own report, and count_test[1]'s local record is tombstoned (has tombstone, no identity - the #398-guard shape) rather than deleted. Scaling back from 1 to 2 proposed exactly one create (1 add, 0 change, 0 destroy) for count_test[1] alone, and the recreated bucket is genuinely a NEW object: same deterministic name (an S3 bucket's id IS its own name - probed against floci with no tofu in the loop) but a new CreationDate ($CT1_CREATED -> $CT1_NEW_CREATED), re-stamped tofu-address=aws_s3_bucket.count_test:1 (tags do not survive a delete+recreate on this image, so the marker is one the apply wrote again), and a record identity naming the recreated bucket; count_test[0]'s CreationDate and marker were unchanged at every step. The next plan proposes no resource action. G-ORACLE, plain terraform standing the identical 2-instance block up for real in the idle greenfield-oracle account, shows the identical shape: $ORACLE_COUNT_SHAPE. BREAK_COUNT=1 asserts the wrong instance (count_test[0]) was destroyed and reports this stage fail, as the stage's Break text requires."
  gauntlet_end_stage
fi
gauntlet_end_stage
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
