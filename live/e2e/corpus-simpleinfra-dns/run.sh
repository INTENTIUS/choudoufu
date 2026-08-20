#!/usr/bin/env bash
set -uo pipefail

# The five-stage real-estate crossing (live/corpus-crossing-manifest.json) for
# rust-lang/simpleinfra's terraform/dns estate - the Rust project's own
# production DNS configuration for seven domains it owns, including crates.io.
#
# WHY THIS IS NOT A DUPLICATE OF live/e2e/repeated-module. That script targets
# the same .corpus directory and was written for issue #280, and it is worth
# saying exactly what it does and does not cover before reading this one:
#   - It never cold-deploys. Its step 3 applies the estate with the `live`
#     block ALREADY declared, so every object is stamped at create time and
#     there is never a moment when genuinely unmarked infrastructure exists.
#   - It therefore never runs `live-import` at all. The migration path - the
#     thing an actual user of this fork performs exactly once, against an
#     estate somebody else built - is not exercised anywhere in it.
#   - It has no drift stage.
# What it does cover, and covers well, is the middle: an empty replan after
# the state file is deleted, the 35 rendered identities asserted against Route
# 53's own answer, and a no-op second apply. Those are stages 3 and 4, and
# this script re-derives them independently rather than trusting them.
#
# So the three stages this crossing adds are 1, 2 and 5, and stage 5 is the
# one worth reading. Every existing crossing's drift stage mutates a TAGGED
# object and watches its own marker's neighbours get restored. This one
# mutates an UNTAGGABLE one: a Route 53 resource record set carries no tags at
# all, so before choudoufu can even notice the drift it has to re-derive that
# record's identity from its parent zone's marker plus the record's own name
# and type, with no state file anywhere. That is the invariant's
# "derived-from-tagged" bucket put under load by an actual out-of-band change
# rather than only by an empty plan.
#
# THE ESTATE. rust-lang/simpleinfra is the Rust project's real infrastructure
# repository (pinned in live/corpus-manifest.json). terraform/dns holds one
# .tf file per domain, each a call of the local ./impl module, which creates
# one aws_route53_zone and four for_each'd aws_route53_record blocks (A,
# CNAME, TXT, MX). 35 instances:
#
#     7 aws_route53_zone       one per module call, TAGGABLE -> 7 markers
#    28 aws_route53_record     UNTAGGABLE -> identity re-derived from the
#                              zone's marker + the record's own name and type
#                              (4 A, 13 CNAME, 3 MX, 8 TXT)
#
# The 7/28 split is the whole reason this estate is interesting: 80% of it
# carries no marker and has to be found some other way, which is a far higher
# derived-from-tagged fraction than any other crossing in the manifest.
#
# THE DELTAS, and how they compare to .corpus/simpleinfra/terraform/
# team-members-access (#274's one previously-crossed estate from this SAME
# repository, which needed four). Three of that estate's four recur here and
# one does not:
#   DELTA 1  backend "s3" removed          RECURS (#268). Same shape: `init`
#            dies on "Failed to get existing workspaces" against a bucket
#            this run cannot reach, and separately a module may declare
#            remote state or a live block, never both.
#   DELTA 2  provider pin ~> 5.64 -> 6.59  RECURS (#269). Identical
#            constraint string, identical cause: ~> 5.64 resolves to a
#            release with no list resources at all, and live-plan cannot
#            discover a marker through one.
#   DELTA 3  emulator flags on provider    RECURS. floci's endpoint plus
#            skip_credentials_validation / skip_metadata_api_check /
#            skip_requesting_account_id.
#   DELTA 4  seeding data-source reads     DOES NOT RECUR. team-members-access
#            needed five reads seeded into the emulator; this estate declares
#            no `data` block at all, in any file, in the root or in ./impl.
#            Asserted below rather than assumed.
# Each delta is asserted at the point it is applied, so a corpus pin that
# moves says so at the edit rather than three stages later as a puzzling plan.
#
# AND THE DELTA THAT IS DELIBERATELY ABSENT. Every record in ./impl spells its
# name with Route 53's own trailing dot - `"${var.domain}."`. Those four dots
# are left exactly as the Rust project wrote them, because #281 is fixed:
# projection adopts the provider's own normalisation of an identity component.
# If they ever have to come off again, #281 has regressed, and the assertion
# below is where to notice.
#
# STAGES:
#   1. COLD DEPLOY   plain `terraform apply` - the real HashiCorp Terraform
#                    binary, no live block, no choudoufu anywhere - over the
#                    estate as rust-lang wrote it. The honest proof it is real
#                    and buildable, and the source of genuinely unmarked live
#                    infrastructure for stage 2.
#   2. MIGRATE       `choudoufu live-import -approve` against that cold state;
#                    the seven markers re-read through the AWS CLI directly.
#   3. TEST PLAN     delete the state file, `choudoufu live-plan`, assert the
#                    plan is EMPTY, and assert all 35 rendered identity
#                    strings against Route 53's own answer.
#   4. TEST APPLY    apply the empty plan; assert a genuine no-op against the
#                    live zone and record counts.
#   5. DRIFT AND     change one UNTAGGABLE record set's TTL out of band, replan,
#      RECONVERGE    assert exactly that one instance is proposed, apply, and
#                    read the restored TTL back off Route 53.
#
# Two independent negative controls, on separate switches so BOTH are
# reachable in a real run (a single BREAK that fails fast at stage 2 leaves
# the stage-5 control never exercised):
#   BREAK=1        corrupts stage 2's expected tofu-address. Must fail at
#                  stage 2.
#   BREAK_STAGE5=1 drifts a SECOND record before stage 5's plan. Must fail
#                  stage 5's exactly-one-instance assertion.
#
#   bash live/e2e/corpus-simpleinfra-dns/run.sh
#
# All five stages pass for real against floci at the digest pinned in
# live/floci-image. Measured 2026-08-19: 127s end to end with a prebuilt
# TOFU_BIN and a warm provider mirror, on a machine also running two other
# crossings.
#
# Needs Docker, the AWS CLI, and the real `terraform` binary on PATH for
# stage 1.
#
# Env overrides:
#   TOFU_BIN      path to a prebuilt choudoufu binary; skips the go build.
#   FLOCI_PORT    host port for the emulator (default 4741, clear of every
#                 other corpus-*/reference-* script's own default).
#   FLOCI_IMAGE   the emulator image; defaults to the digest pin in
#                 live/floci-image.
#   BREAK         set to 1 to corrupt stage 2's identity assertion.
#   BREAK_STAGE5  set to 1 to drift a second record before stage 5.
#   DEBUG_KEEP    set to 1 to skip the exit trap: the floci container and the
#                 WORK directory are left behind for inspection.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SRC="$ROOT/.corpus/simpleinfra/terraform/dns"
WORK="$(mktemp -d)"
PLAIN="$WORK/plain"
ESTATE="$WORK/estate"
FLOCI_PORT="${FLOCI_PORT:-4741}"
FLOCI_NAME="choudoufu-corpus-simpleinfra-dns-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"
REGION="us-west-1"
ESTATE_NAME="simpleinfra-dns-crossing"
PROVIDER_VERSION="6.59.0"

# The estate's own shape, restated as numbers so a moved pin fails at the
# copy rather than as an unexplained plan five stages later.
ZONES=7
RECORDS=28
INSTANCES=35

# The seven markers, one per module call. Written out rather than derived from
# the run: an expectation computed by the same walk that produced the answer
# would agree with a wrong answer too. Every one of these is already inside
# the AWS tag-value charset [A-Za-z0-9 _.:/=+@-] with no escaping, which is
# the honest difference from corpus-evoteum-modules - this estate's module
# calls are static, so no for_each key reaches internal/live/markers'
# EscapeAddress at all.
WANT_MARKERS=(
  'module.areweasyncyet_rs.aws_route53_zone.zone'
  'module.arewewebyet_org.aws_route53_zone.zone'
  'module.crates_io.aws_route53_zone.zone'
  'module.cratesio_com.aws_route53_zone.zone'
  'module.docsrs_com.aws_route53_zone.zone'
  'module.rustaceans_org.aws_route53_zone.zone'
  'module.rustconf_com.aws_route53_zone.zone'
)
# The domain each of those calls owns, in the same order - the independent
# fact each marker is checked against, read off the live hosted zone.
WANT_DOMAINS=(
  'areweasyncyet.rs.'
  'arewewebyet.org.'
  'crates.io.'
  'cratesio.com.'
  'docsrs.com.'
  'rustaceans.org.'
  'rustconf.com.'
)

# Stage 5's drift target: one CNAME in rustconf.com, chosen because it is an
# UNTAGGABLE record whose identity has to be re-derived from its zone's marker
# plus its own name and type. Its TTL is var.ttl = 300 in the configuration.
DRIFT_ZONE_MARKER='module.rustconf_com.aws_route53_zone.zone'
DRIFT_RECORD_NAME='2016.rustconf.com.'
DRIFT_RECORD_TYPE='CNAME'
DRIFT_RECORD_VALUE='tildeio.github.io'
DRIFT_ADDR='module.rustconf_com.aws_route53_record.cname["2016"]'
WANT_TTL=300
DRIFT_TTL=60
# BREAK_STAGE5's second victim, in the same zone.
BREAK_RECORD_NAME='2017.rustconf.com.'

# Both inits below run against the conventional shared plugin cache used as a
# FILESYSTEM MIRROR (-plugin-dir), not as a cache. That distinction is the
# whole point: a plugin cache records no checksums, so `init` in a directory
# with no .terraform.lock.hcl re-downloads the entire ~600MB AWS provider
# purely to compute them (measured for corpus-giantswarm-crossplane at 320s
# per init). Seeding the second directory's lock file from the first's is that
# crossing's fix and it CANNOT work here: stage 1 is real Terraform, which
# resolves hashicorp/aws from registry.terraform.io, while choudoufu resolves
# it from registry.opentofu.org, so the two lock files name different
# providers and neither will satisfy the other. -plugin-dir sidesteps both -
# measured at 0.35s (terraform) and 0.48s (choudoufu) against a warm mirror.
MIRROR="${TF_PLUGIN_CACHE_DIR:-$HOME/.terraform.d/plugin-cache}"

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
[ -n "${DEBUG_KEEP:-}" ] || trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }

# ── 0. tools and corpus ─────────────────────────────────────────────────────
log "=== 0. tools and corpus ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v terraform >/dev/null 2>&1 \
  || fail "the real terraform binary is not on PATH - required for stage 1 (this is a Terraform-authored estate, so the cold deploy uses terraform, not tofu)"
[ -f "$SRC/impl/main.tf" ] \
  || fail "$SRC/impl/main.tf is missing - run 'just corpus-fetch' first"
log "  cold deploy binary: $(terraform version | head -1)"

for reg in registry.terraform.io registry.opentofu.org; do
  [ -d "$MIRROR/$reg/hashicorp/aws/$PROVIDER_VERSION" ] \
    || fail "$MIRROR/$reg/hashicorp/aws/$PROVIDER_VERSION is missing - this script uses the shared plugin cache as a -plugin-dir filesystem mirror, and both registries' copies of $PROVIDER_VERSION have to be in it. Populate it with a one-off 'terraform init' / 'tofu init' in a scratch directory pinning = $PROVIDER_VERSION."
done
log "  provider mirror: $MIRROR has aws $PROVIDER_VERSION for both registries"

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

# copy_estate <destdir> <live_block>: the estate, copied OUT of .corpus (which
# is shared across worktrees and is never written to), with deltas 1-3 and
# optionally the live block. Everything except _terraform.tf is byte-identical
# to the pin, and that is asserted rather than described.
copy_estate() {
  local dest="$1" live_block="$2"
  mkdir -p "$dest"
  cp "$SRC"/*.tf "$dest/"
  cp -R "$SRC/impl" "$dest/impl"

  # Byte-identical BEFORE any edit, so the delta below is the only thing that
  # can account for a later difference.
  diff -rq "$SRC/impl" "$dest/impl" >/dev/null \
    || fail "impl/ differs from the pinned commit before any edit"

  # DELTA 1 + 2 + 3, all three inside the one boilerplate file the estate's
  # own README calls "Terraform boilerplate".
  grep -qF 'backend "s3"' "$dest/_terraform.tf" \
    || fail "_terraform.tf no longer declares backend \"s3\" - re-read the pin before applying DELTA 1"
  grep -qF 'version = "~> 5.64"' "$dest/_terraform.tf" \
    || fail "_terraform.tf no longer constrains aws to \"~> 5.64\" - re-read the pin before applying DELTA 2"

  cat > "$dest/_terraform.tf" <<EOF
// Configuration for Terraform itself.
//
// DELTA 1: the backend "s3" block is removed.
// DELTA 2: version = "~> 5.64" becomes = $PROVIDER_VERSION (#269).
// DELTA 3: floci's connection flags on the provider block.

terraform {
  required_version = "~> 1"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= $PROVIDER_VERSION"
    }
  }
$live_block
}

provider "aws" {
  region = "$REGION"

  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style           = true
}
EOF

  # Nothing but _terraform.tf moved. .terraform/ and .terraform.lock.hcl are
  # excluded because the corpus tree carries them for most entries - module
  # installation writes them (`just corpus-modules`), they are not part of the
  # pinned repository, and this script copies neither.
  local other
  other="$(diff -rq -x '.terraform' -x '.terraform.lock.hcl' "$SRC" "$dest" 2>/dev/null \
             | grep -v '_terraform\.tf' | grep -v 'README\.md' || true)"
  [ -z "$other" ] || { printf '%s\n' "$other"; fail "a file other than _terraform.tf differs from the pin"; }

  # The estate's own shape, checked at the copy.
  local calls
  calls="$(grep -h '^module "' "$dest"/*.tf | grep -c . | tr -d ' ')"
  [ "$calls" = "$ZONES" ] \
    || fail "the estate has $calls module calls, expected $ZONES - the corpus pin has moved and every count below is wrong"

  # THE DELTA THAT IS DELIBERATELY ABSENT (#281): four record names spelled
  # with Route 53's own trailing dot, left exactly as rust-lang wrote them.
  local dots
  dots="$(grep -cF '${var.domain}."' "$dest/impl/main.tf" | tr -d ' ')"
  [ "$dots" = "4" ] \
    || fail "impl/main.tf has $dots record names spelled with a trailing dot, expected 4 - the pin has moved and the #281 half of this run is measuring nothing"

  # DELTA 4 DOES NOT RECUR: team-members-access needed five data-source reads
  # seeded into the emulator. This estate declares no data block anywhere.
  local datablocks
  datablocks="$(cat "$dest"/*.tf "$dest"/impl/*.tf | grep -cE '^[[:space:]]*data[[:space:]]+"' | tr -d ' ')"
  [ "$datablocks" = "0" ] \
    || fail "the estate declares $datablocks data block(s) - team-members-access's DELTA 4 (seeded data-source reads) now recurs here and this script's header is wrong about it"
}

LIVE_BLOCK='
  live {
    estate = "'"$ESTATE_NAME"'"
  }'

copy_estate "$PLAIN" ""
log "  estate copied out of .corpus into $PLAIN ($ZONES module calls of ./impl, stage 1: plain terraform, no live block)"
copy_estate "$ESTATE" "$LIVE_BLOCK"
log "  estate copied out of .corpus into $ESTATE (stages 2-5: choudoufu, live block added)"
log "  DELTA 1  backend \"s3\" removed              (#268, recurs from team-members-access)"
log "  DELTA 2  aws ~> 5.64 -> = $PROVIDER_VERSION            (#269, recurs from team-members-access)"
log "  DELTA 3  emulator flags on the provider    (recurs from team-members-access)"
log "  DELTA 4  NOT NEEDED: 0 data blocks         (does NOT recur - asserted, not assumed)"
log "  no delta on the 4 trailing record-name dots (#281 is fixed)"

# ── 1. floci ────────────────────────────────────────────────────────────────
log "=== 1. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
HEALTH=""
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"route53"' <<< "${HEALTH:-}" && break
  sleep 2
done
grep -q '"route53"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (route53) at $ENDPOINT"
log "  healthy"

export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" AWS_ENDPOINT_URL="$ENDPOINT"

# zone_ids: every live hosted zone's bare id, one per line.
zone_ids() {
  awsl route53 list-hosted-zones --query 'HostedZones[].Id' --output text \
    | tr '\t' '\n' | sed 's|/hostedzone/||' | grep .
}

# zone_by_marker <marker>: the hosted zone id carrying that tofu-address, or
# empty. Route 53 has no tag-filter API, so this walks the zones and reads
# each one's tag set - which is still reading the marker off the live object,
# never asking choudoufu.
zone_by_marker() {
  local want="$1" z a
  while read -r z; do
    [ -n "$z" ] || continue
    a="$(awsl route53 list-tags-for-resource --resource-type hostedzone --resource-id "$z" \
          --query "ResourceTagSet.Tags[?Key=='tofu-address'].Value | [0]" --output text 2>/dev/null)"
    [ "$a" = "$want" ] && { printf '%s\n' "$z"; return 0; }
  done < <(zone_ids)
  return 1
}

# live_record_ttl <zone id> <name> <type>
live_record_ttl() {
  awsl route53 list-resource-record-sets --hosted-zone-id "$1" \
    --query "ResourceRecordSets[?Name=='$2' && Type=='$3'].TTL | [0]" --output text
}

# record_count: total resource record sets across every zone, minus the NS and
# SOA pair Route 53 creates for each zone by itself and the estate does not
# manage.
record_count() {
  local z n=0 c
  while read -r z; do
    [ -n "$z" ] || continue
    c="$(awsl route53 list-resource-record-sets --hosted-zone-id "$z" \
          --query "length(ResourceRecordSets[?Type!='NS' && Type!='SOA'])" --output text)"
    n=$(( n + c ))
  done < <(zone_ids)
  printf '%s\n' "$n"
}

# ══════════════════════════════════════════════════════════════════════════
# STAGE 1: COLD DEPLOY - plain terraform apply, no live block, no choudoufu
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 1: cold deploy (plain terraform apply, the estate as rust-lang wrote it) ==="
( cd "$PLAIN" && terraform init -input=false -no-color -plugin-dir="$MIRROR" >/dev/null 2>&1 ) || {
  ( cd "$PLAIN" && terraform init -input=false -no-color -plugin-dir="$MIRROR" 2>&1 | tail -30 ); fail "stage 1 init failed"; }

COLD_OUT="$(cd "$PLAIN" && terraform apply -input=false -auto-approve -no-color 2>&1)"; COLD_RC=$?
[ "$COLD_RC" -eq 0 ] || { printf '%s\n' "$COLD_OUT" | tail -40; fail "stage 1 (cold deploy) failed"; }
grep -qE "Apply complete! Resources: $INSTANCES added, 0 changed, 0 destroyed" <<< "$COLD_OUT" \
  || { grep -E 'Apply complete' <<< "$COLD_OUT"; fail "stage 1 did not create exactly $INSTANCES instances ($ZONES zones + $RECORDS records)"; }
log "  $(grep -E 'Apply complete' <<< "$COLD_OUT")"
[ -f "$PLAIN/terraform.tfstate" ] || fail "stage 1 left no state file to migrate from"

# The 7/28 split is the point of this estate, so assert the two halves
# separately: a run that made 35 objects of the wrong shape would pass the
# total above.
Z="$(zone_ids | grep -c . | tr -d ' ')"
[ "$Z" = "$ZONES" ] || fail "there are $Z hosted zones, expected $ZONES"
R="$(record_count)"
[ "$R" = "$RECORDS" ] \
  || fail "there are $R estate-managed record sets (excluding Route 53's own NS/SOA), expected $RECORDS"
log "  $Z hosted zones and $R record sets, live, made by real Terraform with no choudoufu involved"

# Genuinely unmarked. Route 53 has no tag-filter API, so this reads every
# zone's tag set directly rather than asking a filter to prove an absence.
STAMPED=0
while read -r z; do
  [ -n "$z" ] || continue
  a="$(awsl route53 list-tags-for-resource --resource-type hostedzone --resource-id "$z" \
        --query "ResourceTagSet.Tags[?Key=='tofu-estate'].Value | [0]" --output text 2>/dev/null)"
  [ "$a" = "None" ] || [ -z "$a" ] || STAMPED=$(( STAMPED + 1 ))
done < <(zone_ids)
[ "$STAMPED" = "0" ] \
  || fail "$STAMPED of the $ZONES zones already carry a tofu-estate tag before migration - this crossing proves nothing"
log "  confirmed unmarked: 0 of $ZONES zones carry tofu-estate before migration"

log ""
log "STAGE 1 (cold deploy): PASS"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE - live-import against the cold state
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 2: choudoufu live-import ==="
( cd "$ESTATE" && "$TOFU" init -input=false -no-color -plugin-dir="$MIRROR" >/dev/null 2>&1 ) || {
  ( cd "$ESTATE" && "$TOFU" init -input=false -no-color -plugin-dir="$MIRROR" 2>&1 | tail -30 ); fail "estate init failed"; }

log "--- 2a: live-import, read-only first ---"
IMPORT_OUT="$(cd "$ESTATE" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE_NAME" -no-color 2>&1)"; IMPORT_RC=$?
[ "$IMPORT_RC" -eq 0 ] || { printf '%s\n' "$IMPORT_OUT" | tail -40; fail "live-import (dry run) failed"; }
grep -qF "$ZONES of $INSTANCES resource instance(s) are eligible for stamping" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import did not verify exactly $ZONES of $INSTANCES as eligible (the $ZONES hosted zones)"; }
grep -qF "No tag has been written." <<< "$IMPORT_OUT" || fail "the dry run wrote a tag - it must not"
grep -qF "UNTAGGABLE ($RECORDS)" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "expected exactly $RECORDS UNTAGGABLE resources - the record sets, which carry no tags at all and must derive their identity from their tagged parent zone"; }
grep -qE '^(UNADMITTED_TYPE|FAILED) \(' <<< "$IMPORT_OUT" \
  && { printf '%s\n' "$IMPORT_OUT"; fail "live-import reported an UNADMITTED_TYPE or FAILED bucket this crossing does not expect - re-read the whole output before changing the assertions above"; }
log "  $ZONES of $INSTANCES verified against the live system; $RECORDS correctly UNTAGGABLE; nothing written yet"

log "--- 2b: -approve ---"
APPROVE_OUT="$(cd "$ESTATE" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE_NAME" -approve -no-color 2>&1)"; APPROVE_RC=$?
[ "$APPROVE_RC" -eq 0 ] || { printf '%s\n' "$APPROVE_OUT" | tail -40; fail "live-import -approve failed"; }
grep -qF "$ZONES resource(s) newly stamped, 0 already stamped, 0 failed, $RECORDS skipped" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not stamp exactly $ZONES of $INSTANCES resources cleanly"; }
log "  $ZONES stamped"

log "--- 2c: the markers, read through the AWS CLI directly - never through choudoufu ---"
log "  --- every live hosted zone's own tofu-address tag, read verbatim ---"
while read -r z; do
  [ -n "$z" ] || continue
  n="$(awsl route53 get-hosted-zone --id "$z" --query 'HostedZone.Name' --output text)"
  a="$(awsl route53 list-tags-for-resource --resource-type hostedzone --resource-id "$z" \
        --query "ResourceTagSet.Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  printf '    %-16s %-20s tofu-address=%s\n' "$z" "$n" "$a"
done < <(zone_ids)

ZONE_IDS=()
for i in $(seq 0 $(( ZONES - 1 ))); do
  want="${WANT_MARKERS[$i]}"
  domain="${WANT_DOMAINS[$i]}"
  if [ "${BREAK:-}" = "1" ] && [ "$i" = "2" ]; then
    want='module.crates_io.aws_route53_zone.wrong_name'
    log "  BREAK=1: expecting a wrong tofu-address on the crates.io zone on purpose - this check must fail"
  fi
  # A marker that cannot be written into an AWS tag value would fail on real
  # AWS while passing against a lenient emulator, so check the charset too.
  [[ "$want" =~ ^[A-Za-z0-9\ _.:/=+@-]+$ ]] \
    || fail "the expected marker $want is not inside the AWS tag-value charset"
  z="$(zone_by_marker "$want")" \
    || fail "no hosted zone carries tofu-address=$want"
  # And it is the RIGHT zone, checked against a fact the marker does not
  # supply: the domain that module call declares.
  live_name="$(awsl route53 get-hosted-zone --id "$z" --query 'HostedZone.Name' --output text)"
  [ "$live_name" = "$domain" ] \
    || fail "the zone marked $want is $live_name, not $domain - the marker and the live object disagree"
  e="$(awsl route53 list-tags-for-resource --resource-type hostedzone --resource-id "$z" \
        --query "ResourceTagSet.Tags[?Key=='tofu-estate'].Value | [0]" --output text)"
  [ "$e" = "$ESTATE_NAME" ] || fail "hosted zone $z carries tofu-estate=$e, not $ESTATE_NAME"
  ZONE_IDS+=("$z")
  log "  zone $z -> tofu-address=$want (live name $live_name matches the domain that call declares)"
done

DISTINCT="$(printf '%s\n' "${ZONE_IDS[@]}" | sort -u | grep -c .)"
[ "$DISTINCT" = "$ZONES" ] \
  || fail "the $ZONES markers resolve to $DISTINCT DISTINCT hosted zones, not $ZONES - two module calls are claiming one live object (#280's end state)"
log "  $ZONES markers, $ZONES distinct hosted zones, one per module call"

if [ "${BREAK:-}" = "1" ]; then
  fail "BREAK=1: a zone's real tofu-address matched the WRONG expected value above without this script noticing - stage 2's assertion is not load-bearing"
fi

log ""
log "STAGE 2 (migrate): PASS"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - state deleted, live-plan, empty + identities by value
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 3: no state file, live-plan ==="
rm -f "$ESTATE/terraform.tfstate" "$ESTATE/terraform.tfstate.backup"
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "the state file is still there"

plan_into() { ( cd "$ESTATE" && TF_LOG=trace "$TOFU" live-plan -input=false -no-color ); }

plan_into > "$WORK/plan1.log" 2>&1; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { grep -E '^Error: |^│ Error' "$WORK/plan1.log" | head -20; fail "live-plan exited $PLAN_RC"; }
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "live-plan wrote a state file"
grep -qE 'No changes|Plan: 0 to add, 0 to change, 0 to destroy' "$WORK/plan1.log" \
  || { grep -E '^  # ' "$WORK/plan1.log" | head -20; fail "live-plan is not empty"; }
grep -qE '^Foreign resources: (none|nothing was swept)' "$WORK/plan1.log" \
  || { grep -E '^Foreign resources:' "$WORK/plan1.log"; fail "the plan reports foreign resources"; }
log "  no resource change proposed and nothing foreign, with zero local memory of the migration that stamped it"

# ── 3b. THE VALUE, not the verdict ──────────────────────────────────────────
# An empty plan says the 35 instances bound. It does not say WHAT they bound
# to. Every identity the run rendered must name a hosted zone that exists, or
# a record set that exists IN THE ZONE THE IDENTITY NAMES - checked against
# Route 53's own answer, never against choudoufu's.
#
# This is also the only place the four trailing dots are checked. The plan
# verdict cannot see them: a record whose identity carries the dot still
# imports, because Route 53 accepts either spelling. It would bind a real
# record by a name Route 53 does not use, and only the string says so.
log "=== 3b. the $INSTANCES rendered identities, against Route 53's own answer ==="
live_keys() {
  zone_ids > "$WORK/zones"
  : > "$WORK/live"
  while read -r z; do
    [ -n "$z" ] || continue
    awsl route53 list-resource-record-sets --hosted-zone-id "$z" \
      --query 'ResourceRecordSets[].[Name,Type]' --output text \
      | while IFS=$'\t' read -r n t; do
          echo "${z}_${n%.}_${t}"
        done >> "$WORK/live"
  done < "$WORK/zones"
  sort -u -o "$WORK/live" "$WORK/live"
}
live_keys

# assert_identities <trace log> <expected count> -> 0 clean, 1 something wrong.
# Prints every identity that names nothing live.
assert_identities() {
  local src="$1" want="$2" rc=0 n id
  grep -oE 'from import identity "[^"]*"' "$src" | sed 's/.*identity "//; s/"$//' | sort -u > "$WORK/ids"
  n="$(grep -c . "$WORK/ids")"
  [ "$n" = "$want" ] || { echo "  identity count is $n, expected $want"; rc=1; }
  while read -r id; do
    [ -n "$id" ] || continue
    case "$id" in
      Z*_*) grep -qxF "$id" "$WORK/live" || { echo "  \"$id\" names no live record set"; rc=1; };;
      Z*)   grep -qxF "$id" "$WORK/zones" || { echo "  \"$id\" names no live hosted zone"; rc=1; };;
      *)    echo "  \"$id\" is not a Route 53 identity shape"; rc=1;;
    esac
  done < "$WORK/ids"
  return "$rc"
}
assert_identities "$WORK/plan1.log" "$INSTANCES" \
  || fail "an identity the run rendered names no live object. The plan was EMPTY when this fired, which is the whole reason this step reads the strings."
log "  all $INSTANCES rendered identities name a live hosted zone or record set"

# And the seven zone identities are seven DISTINCT zone ids. Seven identities
# collapsed onto one zone would still leave 28 distinct record strings, so the
# count above cannot catch it on its own.
ZIDS="$(grep -E '^Z' "$WORK/ids" | grep -vc '_')"
[ "$ZIDS" = "$ZONES" ] \
  || { grep -E '^Z' "$WORK/ids" | grep -v '_'
       fail "the run rendered $ZIDS distinct hosted-zone identities, expected $ZONES"; }
log "  $ZONES of them are distinct hosted-zone ids, one per module call"

# The 28 untaggable ones re-derived correctly: the identity of a record set is
# {zone}_{name}_{type}, and the zone component can ONLY have come from the
# parent zone's marker, since a record set carries no tag of its own. Assert
# the drift target's identity by value, against the zone this run bound it to.
DRIFT_ZONE_ID="$(zone_by_marker "$DRIFT_ZONE_MARKER")" \
  || fail "no hosted zone carries tofu-address=$DRIFT_ZONE_MARKER"
WANT_DRIFT_ID="${DRIFT_ZONE_ID}_${DRIFT_RECORD_NAME%.}_${DRIFT_RECORD_TYPE}"
grep -qxF "$WANT_DRIFT_ID" "$WORK/ids" \
  || { grep -F "$DRIFT_ZONE_ID" "$WORK/ids"; fail "the run did not render the identity \"$WANT_DRIFT_ID\" for $DRIFT_ADDR - an untaggable record's identity did not re-derive from its tagged parent zone"; }
log "  the untaggable $DRIFT_ADDR rendered \"$WANT_DRIFT_ID\" - its zone component came from its parent's marker and nowhere else"

log ""
log "STAGE 3 (test plan): PASS"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 4: TEST APPLY - apply the empty plan, assert a genuine no-op
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 4: test apply (apply the empty plan; object counts unchanged) ==="
BEFORE_Z="$(zone_ids | grep -c . | tr -d ' ')"
BEFORE_R="$(record_count)"

APPLY2_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; APPLY2_RC=$?
[ "$APPLY2_RC" -eq 0 ] || { printf '%s\n' "$APPLY2_OUT" | tail -40; fail "the post-migration apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY2_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY2_OUT"; fail "the post-migration apply was not a no-op"; }

AFTER_Z="$(zone_ids | grep -c . | tr -d ' ')"
AFTER_R="$(record_count)"
[ "$AFTER_Z" = "$BEFORE_Z" ] || fail "hosted zone count changed across a no-op apply: $BEFORE_Z -> $AFTER_Z"
[ "$AFTER_R" = "$BEFORE_R" ] || fail "record set count changed across a no-op apply: $BEFORE_R -> $AFTER_R"
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "a state file exists after the apply"

# The markers did not move either. A second stamping pass writing a different
# address onto a zone the first one marked would leave both counts untouched
# and the estate broken.
for i in $(seq 0 $(( ZONES - 1 ))); do
  z="$(zone_by_marker "${WANT_MARKERS[$i]}")" \
    || fail "after the no-op apply, no hosted zone carries tofu-address=${WANT_MARKERS[$i]}"
  [ "$z" = "${ZONE_IDS[$i]}" ] \
    || fail "the zone bound to ${WANT_MARKERS[$i]} moved across the no-op apply: ${ZONE_IDS[$i]} -> $z"
done
log "  genuine no-op: $BEFORE_Z zones / $BEFORE_R records before and after, no state file either time, all $ZONES markers unmoved"

log ""
log "STAGE 4 (test apply): PASS"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 5: DRIFT AND RECONVERGE - mutate one UNTAGGABLE record out of band
# ══════════════════════════════════════════════════════════════════════════
# Every other crossing's stage 5 drifts a TAGGED object. This one drifts an
# untaggable record set on purpose: with no state file and no tag on the
# object, the only way choudoufu can see this change at all is by re-deriving
# the record's identity from its parent zone's marker plus its own name and
# type, and then reading it back. If that derivation is wrong, the drift is
# invisible and the plan comes back empty - which is why the assertion below
# is on the address AND on the restored value, not on an exit code.
log "=== STAGE 5: drift and reconverge (one untaggable record set, out of band) ==="

upsert_ttl() {
  local zone="$1" name="$2" ttl="$3"
  awsl route53 change-resource-record-sets --hosted-zone-id "$zone" \
    --change-batch "$(printf '{"Changes":[{"Action":"UPSERT","ResourceRecordSet":{"Name":"%s","Type":"%s","TTL":%s,"ResourceRecords":[{"Value":"%s"}]}}]}' \
        "$name" "$DRIFT_RECORD_TYPE" "$ttl" "$DRIFT_RECORD_VALUE")" >/dev/null
}

if [ "${BREAK_STAGE5:-}" = "1" ]; then
  upsert_ttl "$DRIFT_ZONE_ID" "$BREAK_RECORD_NAME" "$DRIFT_TTL" \
    || fail "the BREAK_STAGE5 second mutation did not take"
  log "  BREAK_STAGE5=1: also drifted $BREAK_RECORD_NAME's TTL - stage 5 must now see TWO"
  log "                  drifted instances and fail the single-instance assertion"
fi

upsert_ttl "$DRIFT_ZONE_ID" "$DRIFT_RECORD_NAME" "$DRIFT_TTL" \
  || fail "the out-of-band record mutation did not take"
GOT_TTL="$(live_record_ttl "$DRIFT_ZONE_ID" "$DRIFT_RECORD_NAME" "$DRIFT_RECORD_TYPE")"
[ "$GOT_TTL" = "$DRIFT_TTL" ] \
  || fail "the out-of-band mutation did not take: $DRIFT_RECORD_NAME's TTL is $GOT_TTL, expected $DRIFT_TTL"
log "  set $DRIFT_RECORD_NAME ($DRIFT_RECORD_TYPE) TTL to $DRIFT_TTL in zone $DRIFT_ZONE_ID, directly via the AWS CLI - never through choudoufu"

plan_into > "$WORK/plan-drift.log" 2>&1; DRIFT_RC=$?
[ "$DRIFT_RC" -eq 0 ] || { grep -E '^Error: |^│ Error' "$WORK/plan-drift.log" | head -20; fail "the drift-detection plan exited $DRIFT_RC"; }

CHANGED_ADDRS="$(grep -oE '^  # \S+ will be updated' "$WORK/plan-drift.log" | awk '{print $2}' | sort -u)"
N_CHANGED="$(printf '%s\n' "$CHANGED_ADDRS" | grep -c . || true)"
if [ "${BREAK_STAGE5:-}" = "1" ]; then
  [ "$N_CHANGED" = "1" ] \
    && fail "BREAK_STAGE5=1 set (two records drifted), but the plan proposes fixing only 1 - this assertion is not load-bearing"
  log "  BREAK_STAGE5=1: the plan proposes fixing $N_CHANGED instances, correctly more than"
  log "                  one - the single-instance assertion and reconverge apply below are skipped"
else
  [ "$N_CHANGED" = "1" ] \
    || { grep -E '^  # .+ will be' "$WORK/plan-drift.log"; fail "expected exactly 1 instance proposed for a fix, got $N_CHANGED"; }
  [ "$CHANGED_ADDRS" = "$DRIFT_ADDR" ] \
    || fail "the plan proposes fixing $CHANGED_ADDRS, not $DRIFT_ADDR"
  log "  the plan proposes fixing exactly one instance: $CHANGED_ADDRS - nothing else in the diff"

  RECONVERGE_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; RECONVERGE_RC=$?
  [ "$RECONVERGE_RC" -eq 0 ] || { printf '%s\n' "$RECONVERGE_OUT" | tail -40; fail "the reconverge apply failed"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$RECONVERGE_OUT" \
    || { grep -E 'Apply complete' <<< "$RECONVERGE_OUT"; fail "the reconverge apply did not change exactly 1 resource"; }
  FIXED_TTL="$(live_record_ttl "$DRIFT_ZONE_ID" "$DRIFT_RECORD_NAME" "$DRIFT_RECORD_TYPE")"
  [ "$FIXED_TTL" = "$WANT_TTL" ] \
    || fail "$DRIFT_RECORD_NAME's TTL is $FIXED_TTL after reconverging, not the configuration's $WANT_TTL"
  # And nothing else moved: the counts, and the parent zone's own marker.
  [ "$(record_count)" = "$RECORDS" ] \
    || fail "the record set count is no longer $RECORDS after reconverging"
  STILL="$(awsl route53 list-tags-for-resource --resource-type hostedzone --resource-id "$DRIFT_ZONE_ID" \
            --query "ResourceTagSet.Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$STILL" = "$DRIFT_ZONE_MARKER" ] \
    || fail "the parent zone's tofu-address is \"$STILL\" after the reconverge apply - the marker did not survive"
  log "  reconverged: TTL is back to $WANT_TTL, $RECORDS record sets still there, the parent zone's marker intact - all read via the AWS CLI"
fi

log ""
log "STAGE 5 (drift and reconverge): PASS"
log ""

log "=== PASS: all five stages, real, against rust-lang/simpleinfra's own  ==="
log "=== terraform/dns estate - $ZONES tagged hosted zones and $RECORDS untaggable  ==="
log "=== record sets, cold-deployed by real Terraform, migrated by         ==="
log "=== live-import, and drift-corrected on an object that carries no tag ==="
