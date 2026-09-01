#!/usr/bin/env bash
# (moved from the justfile's retired demo-corpus-simpleinfra-dns recipe; run with: just demo-run corpus-simpleinfra-dns)
# rust-lang/simpleinfra's terraform/dns estate - the Rust project's real
# production DNS configuration for seven domains it owns, crates.io included.
# 35 instances, and the split is the point: 7 aws_route53_zone are TAGGABLE
# and carry markers, 28 aws_route53_record carry no tags at all and must
# re-derive their identity from their tagged parent zone: 28 of 35 instances,
# 80%, carry no marker at all. (No claim about the manifest's maximum - that
# would need every entry counted, and it has not been.)
#
# It is not a duplicate of demo-repeated-module, which targets the same
# .corpus directory for issue #280: that script applies the estate with the
# live block already declared, so it never cold-deploys, never runs
# live-import, and has no drift stage. This runs all five. Stage 5 is the one
# worth reading - it drifts an UNTAGGABLE record set out of band, which no
# other crossing's drift stage does, so the derived-from-tagged identity has
# to be right before the drift is even visible.
#
# All five stages PASS for real as of 2026-08-19, with three deltas from the
# published form (backend removed #268, provider pin #269, emulator flags) -
# the same three .corpus/simpleinfra/terraform/team-members-access needed,
# minus its fourth: this estate declares no data block anywhere, asserted
# rather than assumed. Needs Docker, the AWS CLI, and the real `terraform`
# binary (it is a Terraform-authored estate); runs on its own port (4741).
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
# The 7/28 split is the whole reason this estate is interesting: 28 of 35
# instances, 80%, carry no marker and have to be found some other way. For
# scale, the one other crossing whose untaggable fraction was counted here is
# corpus-evoteum-modules at 3 of 10. No claim is made about the manifest's
# maximum - that would need every entry counted, and it has not been.
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
#   BREAK_REMOVE=1 day2_remove's own break control (PART E, after the real
#                  rename): keep module.cratesio_com_final's block in the
#                  config; the plan below must propose no destroy for it
#                  at all - the Break text in tools/gauntlet/stages.go,
#                  verbatim.
#   BREAK_COUNT=1  day2_count's own break control (PART G, after the real
#                  removal): after the real scale-down plan, assert the
#                  WRONG sibling (module.rustconf_com.aws_route53_record.
#                  cname["2022"] rather than cname["2024"]) was destroyed -
#                  the assertion must fail. Only reachable when BREAK is
#                  not 2 and BREAK_REMOVE is not 1, because PART G starts
#                  from PART E's real, completed removal.
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
#   BREAK_REMOVE  set to 1 to run day2_remove's own break control instead of
#                 the real removal (see above).
#   BREAK_COUNT   set to 1 to run day2_count's own break control instead of
#                 the real scale (see above).
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

# Two more, fresh containers for the greenfield stage (live/GAUNTLET.md #13):
# one namespace choudoufu applies into directly with no migration, and a
# SEPARATE namespace stock applies the identical config into as that
# stage's own oracle. Neither reuses the main container's objects above -
# greenfield means from nothing, and the oracle needs its own independent
# apply.
FLOCI_GREEN_PORT=$((FLOCI_PORT + 1))
FLOCI_GREEN_NAME="choudoufu-corpus-simpleinfra-dns-green-$$"
FLOCI_ORACLE_PORT=$((FLOCI_PORT + 2))
FLOCI_ORACLE_NAME="choudoufu-corpus-simpleinfra-dns-green-oracle-$$"
GREEN_ENDPOINT="http://127.0.0.1:${FLOCI_GREEN_PORT}"
ORACLE_ENDPOINT="http://127.0.0.1:${FLOCI_ORACLE_PORT}"
REGION="us-west-1"
ESTATE_NAME="simpleinfra-dns-crossing"
GREEN_ESTATE_NAME="${ESTATE_NAME}-greenfield"
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
  docker rm -f "$FLOCI_NAME" "$FLOCI_GREEN_NAME" "$FLOCI_ORACLE_NAME" >/dev/null 2>&1 || true
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
gauntlet_begin_stage cold_deploy
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
gauntlet_stage cold_deploy pass "$INSTANCES instances ($Z zones, $R records) from plain terraform, 0 of $ZONES zones carry tofu-estate"

# ══════════════════════════════════════════════════════════════════════════
# PART GREENFIELD (greenfield, live/GAUNTLET.md #13, active)
# ══════════════════════════════════════════════════════════════════════════
#
# A SEPARATE fresh namespace from everything above: applying the same
# estate directly with choudoufu, no migration ever, compared object by
# object against stock's OWN fresh apply of the identical config in a
# THIRD namespace. Two more floci containers, cleaned up the same way the
# main one is. This reuses copy_estate/zone_ids/record_count, all of which
# read through the awsl() helper's global $ENDPOINT, so this section
# points $ENDPOINT at each fresh container in turn and restores it before
# falling back into stage 2's own use of the main container.
gauntlet_begin_stage greenfield
log ""
log "=== PART GREENFIELD: 0. two more floci containers ==="
docker run -d --rm -p "${FLOCI_GREEN_PORT}:4566" --name "$FLOCI_GREEN_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_GREEN_NAME failed"
docker run -d --rm -p "${FLOCI_ORACLE_PORT}:4566" --name "$FLOCI_ORACLE_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_ORACLE_NAME failed"
for ep in "$GREEN_ENDPOINT" "$ORACLE_ENDPOINT"; do
  H=""
  for _ in $(seq 1 45); do
    H="$(curl -fs "${ep}/_localstack/health" 2>/dev/null)" || true
    grep -q '"route53"' <<< "${H:-}" && break
    sleep 2
  done
  grep -q '"route53"' <<< "${H:-}" || fail "floci did not come up healthy (route53) at $ep"
done
log "  healthy: greenfield=$GREEN_ENDPOINT oracle=$ORACLE_ENDPOINT"

log "=== PART GREENFIELD: 1. choudoufu apply from nothing, no migration, no state file ever existing ==="
GREEN_LIVE_BLOCK='
  live {
    estate = "'"$GREEN_ESTATE_NAME"'"
  }'
GREEN="$WORK/green"
copy_estate "$GREEN" "$GREEN_LIVE_BLOCK"
MAIN_ENDPOINT="$ENDPOINT"
( cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color -plugin-dir="$MIRROR" >/dev/null 2>&1 ) || {
  ( cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color -plugin-dir="$MIRROR" 2>&1 | tail -30 ); fail "the greenfield init failed"; }
GREEN_APPLY_OUT="$(cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; GREEN_APPLY_RC=$?
[ "$GREEN_APPLY_RC" -eq 0 ] || { printf '%s\n' "$GREEN_APPLY_OUT" | tail -40; fail "the greenfield apply failed"; }
grep -qE "Apply complete! Resources: $INSTANCES added, 0 changed, 0 destroyed" <<< "$GREEN_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT"; fail "the greenfield apply did not create exactly $INSTANCES instances"; }
[ ! -f "$GREEN/terraform.tfstate" ] || fail "the greenfield apply left a state file - this estate must never keep local state"
log "  $(grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT")"

log "=== PART GREENFIELD: 2. markers, read through the AWS CLI directly ==="
ENDPOINT="$GREEN_ENDPOINT"
GZ="$(zone_ids | grep -c . | tr -d ' ')"
[ "$GZ" = "$ZONES" ] || fail "the greenfield estate has $GZ hosted zones, expected $ZONES"
GR="$(record_count)"
[ "$GR" = "$RECORDS" ] || fail "the greenfield estate has $GR record sets, expected $RECORDS"
for i in $(seq 0 $(( ZONES - 1 ))); do
  want="${WANT_MARKERS[$i]}"
  domain="${WANT_DOMAINS[$i]}"
  z="$(zone_by_marker "$want")" || fail "no greenfield hosted zone carries tofu-address=$want"
  live_name="$(awsl route53 get-hosted-zone --id "$z" --query 'HostedZone.Name' --output text)"
  [ "$live_name" = "$domain" ] \
    || fail "the greenfield zone marked $want is $live_name, not $domain"
  e="$(awsl route53 list-tags-for-resource --resource-type hostedzone --resource-id "$z" \
        --query "ResourceTagSet.Tags[?Key=='tofu-estate'].Value | [0]" --output text)"
  [ "$e" = "$GREEN_ESTATE_NAME" ] || fail "greenfield hosted zone $z carries tofu-estate=$e, not $GREEN_ESTATE_NAME"
done
log "  $GZ hosted zones, $GR record sets, all $ZONES markers verified via the AWS CLI"

log "=== PART GREENFIELD: 3. the next plan proposes nothing ==="
GREEN_PLAN_OUT="$(cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" live-plan -input=false -no-color 2>&1)"; GREEN_PLAN_RC=$?
[ "$GREEN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$GREEN_PLAN_OUT" | tail -40; fail "the greenfield replan exited $GREEN_PLAN_RC"; }
grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$GREEN_PLAN_OUT" \
  && { grep -E '^  # .+ will be' <<< "$GREEN_PLAN_OUT"; fail "the greenfield replan proposes a resource change"; }
log "  no resource action proposed"
ENDPOINT="$MAIN_ENDPOINT"

log "=== PART GREENFIELD: 4. stock oracle - the identical config applied fresh in its own namespace ==="
GREEN_ORACLE="$WORK/green-oracle"
copy_estate "$GREEN_ORACLE" ""
( cd "$GREEN_ORACLE" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform init -input=false -no-color -plugin-dir="$MIRROR" >/dev/null 2>&1 ) || {
  ( cd "$GREEN_ORACLE" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform init -input=false -no-color -plugin-dir="$MIRROR" 2>&1 | tail -30 ); fail "the greenfield oracle's init failed"; }
ORACLE_APPLY_OUT="$(cd "$GREEN_ORACLE" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)"; ORACLE_APPLY_RC=$?
[ "$ORACLE_APPLY_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_APPLY_OUT" | tail -40; fail "the greenfield oracle apply failed"; }
grep -qE "Apply complete! Resources: $INSTANCES added, 0 changed, 0 destroyed" <<< "$ORACLE_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$ORACLE_APPLY_OUT"; fail "the greenfield oracle apply did not create exactly $INSTANCES instances"; }
log "  $(grep -E 'Apply complete' <<< "$ORACLE_APPLY_OUT")"

log "=== PART GREENFIELD: 5. object-by-object comparison, via the AWS CLI on both endpoints, tags normalised out ==="
zone_id_by_domain() {
  local ep="$1" domain="$2"
  aws --endpoint-url "$ep" --region "$REGION" route53 list-hosted-zones-by-name \
    --dns-name "$domain" --query "HostedZones[?Name=='$domain'].Id | [0]" --output text 2>/dev/null | sed 's|/hostedzone/||'
}
zone_record_dump() {
  local ep="$1" zid="$2"
  aws --endpoint-url "$ep" --region "$REGION" route53 list-resource-record-sets --hosted-zone-id "$zid" \
    --query "ResourceRecordSets[?Type!='NS' && Type!='SOA'].[Name,Type,TTL,join(',',ResourceRecords[].Value)]" \
    --output text 2>/dev/null | LC_ALL=C sort
}
for domain in "${WANT_DOMAINS[@]}"; do
  gzid="$(zone_id_by_domain "$GREEN_ENDPOINT" "$domain")"
  ozid="$(zone_id_by_domain "$ORACLE_ENDPOINT" "$domain")"
  [ -n "$gzid" ] || fail "no greenfield hosted zone found for $domain"
  [ -n "$ozid" ] || fail "no stock-oracle hosted zone found for $domain"
  gcomment="$(aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" route53 get-hosted-zone --id "$gzid" --query 'HostedZone.Config.Comment' --output text)"
  ocomment="$(aws --endpoint-url "$ORACLE_ENDPOINT" --region "$REGION" route53 get-hosted-zone --id "$ozid" --query 'HostedZone.Config.Comment' --output text)"
  [ "$gcomment" = "$ocomment" ] \
    || fail "$domain's zone comment differs between the greenfield estate ($gcomment) and the stock oracle ($ocomment)"
  gdump="$(zone_record_dump "$GREEN_ENDPOINT" "$gzid")"
  odump="$(zone_record_dump "$ORACLE_ENDPOINT" "$ozid")"
  [ "$gdump" = "$odump" ] \
    || { printf 'greenfield:\n%s\noracle:\n%s\n' "$gdump" "$odump"; fail "$domain's record sets differ structurally between the greenfield estate and the stock oracle"; }
done
log "  all $ZONES zones match structurally (comment, and every non-NS/SOA record's name/type/ttl/value) between choudoufu's greenfield apply and stock's fresh apply in its own namespace"
gauntlet_stage greenfield pass "$INSTANCES instances from nothing ($ZONES zones, $RECORDS records), all $ZONES markers verified via the AWS CLI, replan empty, stock oracle in its own namespace matches structurally on all $ZONES zones ($RECORDS records)"
gauntlet_end_stage
docker rm -f "$FLOCI_GREEN_NAME" "$FLOCI_ORACLE_NAME" >/dev/null 2>&1 || true

# ══════════════════════════════════════════════════════════════════════════
# PART D-ORACLE: RENAME, stock (day2_rename, active - live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
#
# The closer template here is corpus-mastino-dns's day2_rename (zone
# renames, untaggable record children not moved), not the module-rename
# scripts' per-resource moved-block sweep - but this estate's zones are
# themselves module calls (module "X" { source = "./impl" }, not a bare
# `resource "aws_route53_zone"`), so renaming the module changes the STATE
# ADDRESS of every child, records included, and the two mechanisms differ
# in what each actually needs:
#   - the STOCK oracle below (real terraform, real state) needs a moved
#     block for every stateful child under a renamed module, or an
#     unlisted record's old-address instance genuinely destroys and its
#     new-address instance genuinely creates - ordinary Terraform move
#     semantics, demonstrated on module.rustaceans_org (1 zone + 2
#     records = 3 moved blocks).
#   - choudoufu's OWN legs below (D1/D2, at the end of this script) need
#     only ONE moved block, for the zone: it never uses local state at
#     all (every stage above deletes/asserts-absent terraform.tfstate),
#     so its untaggable records are re-derived every plan from the zone's
#     marker plus each record's own name and type - unaffected by the
#     zone's OWN address, exactly the mastino-dns finding, just reached
#     through a module rename instead of a bare-resource one.
#
# Two zones, chosen for what each demonstrates: module.rustaceans_org (1 A
# + 1 CNAME - the smallest real record set, so the "records don't move"
# claim is actually exercised) gets the moved-block leg; module.
# cratesio_com (0 records - "parked and reserved") gets the live-mv leg,
# kept maximally simple since live-mv moves one resource instance at a
# time. The stock oracle plans the NET rename of BOTH on a copy of
# cold_deploy's own state, before choudoufu or live-import touch either.
gauntlet_begin_stage day2_rename
log "=== D-ORACLE. stock: the same two module renames, through moved blocks, on cold_deploy's own state ==="
ORACLE="$WORK/oracle"
copy_estate "$ORACLE" ""
cp "$PLAIN/terraform.tfstate" "$ORACLE/terraform.tfstate"
( cd "$ORACLE" && terraform init -input=false -no-color -plugin-dir="$MIRROR" >/dev/null 2>&1 ) || {
  ( cd "$ORACLE" && terraform init -input=false -no-color -plugin-dir="$MIRROR" 2>&1 | tail -30 ); fail "the day2_rename stock oracle's init failed"; }
BASELINE_PLAN_OUT="$(cd "$ORACLE" && terraform plan -input=false -no-color 2>&1)"; BASELINE_PLAN_RC=$?
[ "$BASELINE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BASELINE_PLAN_OUT" | tail -40; fail "the day2_rename stock oracle's baseline (no-rename) plan exited $BASELINE_PLAN_RC"; }
grep -qF 'No changes. Your infrastructure matches the configuration.' <<< "$BASELINE_PLAN_OUT" \
  || { printf '%s\n' "$BASELINE_PLAN_OUT" | tail -20; fail "the baseline (no-rename) oracle plan is not clean - this estate has drifted since the baseline was last measured"; }
log "  baseline (no rename): clean, confirmed BEFORE the rename below"

sed -i.bak 's/module "cratesio_com" {/module "cratesio_com_final" {/' "$ORACLE/cratesio.com.tf"
rm -f "$ORACLE/cratesio.com.tf.bak"
sed -i.bak 's/module "rustaceans_org" {/module "rustaceans_org_final" {/' "$ORACLE/rustaceans.org.tf"
rm -f "$ORACLE/rustaceans.org.tf.bak"
cat > "$ORACLE/_moved.tf" <<'EOF'
moved {
  from = module.cratesio_com.aws_route53_zone.zone
  to   = module.cratesio_com_final.aws_route53_zone.zone
}

moved {
  from = module.rustaceans_org.aws_route53_zone.zone
  to   = module.rustaceans_org_final.aws_route53_zone.zone
}

moved {
  from = module.rustaceans_org.aws_route53_record.a["@"]
  to   = module.rustaceans_org_final.aws_route53_record.a["@"]
}

moved {
  from = module.rustaceans_org.aws_route53_record.cname["www"]
  to   = module.rustaceans_org_final.aws_route53_record.cname["www"]
}
EOF
( cd "$ORACLE" && terraform init -input=false -no-color -plugin-dir="$MIRROR" >/dev/null 2>&1 ) || {
  ( cd "$ORACLE" && terraform init -input=false -no-color -plugin-dir="$MIRROR" 2>&1 | tail -30 ); fail "the day2_rename stock oracle's reinit failed"; }
ORACLE_PLAN_OUT="$(cd "$ORACLE" && terraform plan -input=false -no-color 2>&1)"; ORACLE_PLAN_RC=$?
[ "$ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -40; fail "the day2_rename stock oracle plan exited $ORACLE_PLAN_RC"; }
grep -qE '^  # .+ will be (destroyed|created)' <<< "$ORACLE_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock proposes a destroy or create for a rename carried entirely by moved blocks - the oracle itself is not zero-churn"; }
# Not "No changes." literally (unlike the baseline above): four moved
# blocks print their own "Terraform will perform the following actions
# because you requested..."-style move notices even with zero attribute
# churn, so the Plan: line and the destroy/create grep above are what rule
# out real churn - confirmed by running, not assumed (this is the same
# finding corpus-iam-read-only-policy's own day2_rename oracle made, there
# from a data-source re-read instead of a move notice).
grep -qF 'Plan: 0 to add, 0 to change, 0 to destroy.' <<< "$ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -20; fail "stock's rename plan is not a true no-op"; }
log "  stock: zero churn on cold_deploy's own state - both module moves (cratesio.com, zone only; rustaceans.org, zone + 2 records, each needing its own moved block for a genuine state-address rename) report only their move, no attribute diff at all"

# ══════════════════════════════════════════════════════════════════════════
# PART E-ORACLE: REMOVE, stock (day2_remove, active - live/GAUNTLET.md #7):
# "Stock with the same block removed plans the same destroys." A SEPARATE
# copy_estate copy of cold_deploy's own state, so this destroy has nothing
# to do with the rename above. Removes module.cratesio_com's WHOLE block -
# a TAGGABLE zone with 0 records ("parked and reserved for future use"),
# the smallest real removal target this estate has, so the destroy is
# exactly one object and nothing else has to be reasoned about.
#
# An untaggable child's own for_each entry (a single CNAME/TXT/MX/A record,
# its parent zone staying) was tried first and reverted: aws_route53_record
# carries no tags argument at all, so [markerCapable] correctly refuses it
# a marker-discoverable sweep the same way [scanTypeLocatedFallback]
# documents for every other untaggable type - CollectUnclaimed's
# account-wide listing silently skips every result of that type rather
# than erroring, which means nothing in this estate's current discovery
# path can ever notice such a record's block disappeared at all (the
# derivation this estate's stage 5 depends on - parent marker + name + type
# - has nothing left to derive FROM once the declaring block is gone). That
# is a real, structural gap in untaggable-child orphan detection, not a
# fixable-here defect in this estate's own script, so it is left as a
# finding rather than forced; a whole TAGGABLE zone is what this crossing's
# day2_remove actually proves.
gauntlet_begin_stage day2_remove
log "=== E-ORACLE: stock terraform, delete module.cratesio_com's block on cold_deploy's own state ==="
REMOVE_ORACLE="$WORK/remove-oracle"
copy_estate "$REMOVE_ORACLE" ""
cp "$PLAIN/terraform.tfstate" "$REMOVE_ORACLE/terraform.tfstate"
perl -0pi -e 's/\nmodule "cratesio_com" \{.*?\n\}\n//s' "$REMOVE_ORACLE/cratesio.com.tf"
grep -q 'module "cratesio_com"' "$REMOVE_ORACLE/cratesio.com.tf" \
  && fail "removing module.cratesio_com's block from the remove-oracle copy did not match - the corpus pin has moved"
( cd "$REMOVE_ORACLE" && terraform init -input=false -no-color -plugin-dir="$MIRROR" >/dev/null 2>&1 ) || {
  ( cd "$REMOVE_ORACLE" && terraform init -input=false -no-color -plugin-dir="$MIRROR" 2>&1 | tail -30 ); fail "the day2_remove stock oracle's init failed"; }
REMOVE_ORACLE_PLAN_OUT="$(cd "$REMOVE_ORACLE" && terraform plan -input=false -no-color 2>&1)"; REMOVE_ORACLE_PLAN_RC=$?
[ "$REMOVE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_remove stock oracle plan exited $REMOVE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.cratesio_com\.aws_route53_zone\.zone will be destroyed' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "stock does not propose destroying module.cratesio_com's zone when its block is removed"; }
grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -10; fail "stock's remove plan proposes something other than exactly one destroy"; }
log "  stock: exactly one destroy (module.cratesio_com's zone), nothing else, on the state cold_deploy produced"
gauntlet_end_stage

# day2_replace's stock oracle (live/GAUNTLET.md #9), computed here for the
# same reason day2_remove's own oracle sits before migrate (above): a
# throwaway copy of cold_deploy's own state, module.areweasyncyet_rs's
# `domain` argument changed to a different literal - `domain` sets aws_
# route53_zone.zone's own `name`, which is ForceNew (Route 53 has no
# rename-hosted-zone API), so this forces a replace at the SAME declared
# address, cascading into the zone's own one A record (its `zone_id`
# argument is ForceNew too, and its own `name` also interpolates var.
# domain). module.areweasyncyet_rs is the smallest real zone in this
# estate - one A record, no CNAME/MX/TXT - and day2_rename/day2_remove
# (above) target module.rustaceans_org and module.cratesio_com, never it,
# so this section has no ordering dependency on either. PLAN ONLY, never
# applied: this copy shares floci's account with $ESTATE.
gauntlet_begin_stage day2_replace
log "=== F-ORACLE. stock: force-replace module.areweasyncyet_rs's zone via its ForceNew domain argument, on cold_deploy's own state ==="
REPLACE_ORACLE="$WORK/replace-oracle"
copy_estate "$REPLACE_ORACLE" ""
cp "$PLAIN/terraform.tfstate" "$REPLACE_ORACLE/terraform.tfstate"
sed -i.bak 's/domain  = "areweasyncyet\.rs"/domain  = "areweasyncyet-replaced.rs"/' "$REPLACE_ORACLE/areweasyncyet.rs.tf"
rm -f "$REPLACE_ORACLE/areweasyncyet.rs.tf.bak"
grep -q 'domain  = "areweasyncyet-replaced.rs"' "$REPLACE_ORACLE/areweasyncyet.rs.tf" \
  || fail "changing module.areweasyncyet_rs's domain argument in the replace-oracle copy did not match - the corpus pin has moved"
( cd "$REPLACE_ORACLE" && terraform init -input=false -no-color -plugin-dir="$MIRROR" >/dev/null 2>&1 ) || {
  ( cd "$REPLACE_ORACLE" && terraform init -input=false -no-color -plugin-dir="$MIRROR" 2>&1 | tail -30 ); fail "the day2_replace stock oracle's init failed"; }
REPLACE_ORACLE_PLAN_OUT="$(cd "$REPLACE_ORACLE" && terraform plan -input=false -no-color 2>&1)"; REPLACE_ORACLE_PLAN_RC=$?
[ "$REPLACE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_replace stock oracle plan exited $REPLACE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.areweasyncyet_rs\.aws_route53_zone\.zone must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "stock does not propose replacing module.areweasyncyet_rs's zone when its ForceNew domain argument changes"; }
grep -qE '^  # module\.areweasyncyet_rs\.aws_route53_record\.a\["@"\] must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "stock does not cascade the zone replace into its A record"; }
grep -qF 'Plan: 2 to add, 0 to change, 2 to destroy.' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -10; fail "the day2_replace stock oracle plan does not match the header's own two-resource cascade (zone + its one A record, both replaced)"; }
log "  stock: exactly one zone replace at the same declared address, cascading into its one A record - 2 to add, 2 to destroy, on the state cold_deploy produced - plan only, not applied (see above)"
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# PART G-ORACLE: CHANGE COUNT, stock (day2_count, active - live/GAUNTLET.md
# #8, issue #359/#488)
# ══════════════════════════════════════════════════════════════════════════
#
# The real for_each set this estate offers day2_count: module.rustconf_com's
# own CNAME map (impl/variables.tf's `variable "CNAME"`, live since STAGE
# 1/cold deploy) - the "harder, preferred shape" issue #488 asks for over a
# synthetic block, since this estate already has one. "2024" is the key
# scaled here; PART G below (after STAGE 5/day2_remove) is the choudoufu
# leg and explains why that specific key.
#
# Same timing discipline as D-ORACLE/E-ORACLE/F-ORACLE, above: a copy of
# $PLAIN's own state, before choudoufu or live-import ever touch these
# objects. And, like F-ORACLE, PLAN ONLY, never applied - this copy shares
# the SAME $ENDPOINT account $PLAIN's own state, read by STAGE 2/migrate
# right after this section, depends on finding undisturbed; applying here
# would for-real destroy/recreate the live cname["2024"] record out from
# under $PLAIN's own terraform.tfstate. The down-plan reads directly off
# cold_deploy's untouched state (matches the real cloud); the up-plan's
# "not there yet" starting point is simulated with `terraform state rm` on
# a SEPARATE copy - a pure local state edit, no provider API call, so it
# can never touch a live object (the same technique corpus-xancloud-iac's
# own day2_count oracle uses).
gauntlet_begin_stage day2_count
COUNT_ZONE_MARKER='module.rustconf_com.aws_route53_zone.zone'
COUNT_RECORD_NAME='2024.rustconf.com.'
COUNT_RECORD_TYPE='CNAME'
COUNT_RECORD_VALUE='wp.wpenginepowered.com'
COUNT_ADDR='module.rustconf_com.aws_route53_record.cname["2024"]'
COUNT_LINE='    "2024" = ["wp.wpenginepowered.com"],'
COUNT_SIBLING_NAME='2022.rustconf.com.'
COUNT_SIBLING_TYPE='CNAME'
COUNT_SIBLING_ADDR='module.rustconf_com.aws_route53_record.cname["2022"]'
COUNT_SIBLING_LINE='    "2022" = ["rustconf-2022.netlify.app"],'

# drop_count_record/add_count_record <dir>: mutate <dir>/rustconf.com.tf's
# CNAME map by removing/restoring exactly the "2024" entry - the module's
# own documented for_each input, the real knob day2_count scales here
# rather than a synthetic block, per #488's preference for a real knob.
# "2024" is the only CNAME key in this estate no other stage ever names
# (STAGE 5/BREAK_STAGE5 use "2016"/"2017"; day2_rename/day2_remove/
# day2_replace never touch module.rustconf_com at all).
drop_count_record() {
  local dir="$1"
  grep -qF "$COUNT_LINE" "$dir/rustconf.com.tf" \
    || fail "rustconf.com.tf's \"2024\" CNAME line is not what this script expects - the corpus pin has moved"
  COUNT_LINE="$COUNT_LINE" perl -0777 -i -pe 's/\Q$ENV{COUNT_LINE}\E\n//' "$dir/rustconf.com.tf"
  grep -qF "$COUNT_LINE" "$dir/rustconf.com.tf" \
    && fail "removing the \"2024\" CNAME line from $dir/rustconf.com.tf did not take"
}
add_count_record() {
  local dir="$1"
  grep -qF "$COUNT_LINE" "$dir/rustconf.com.tf" \
    && fail "the \"2024\" CNAME line is unexpectedly already present in $dir/rustconf.com.tf"
  grep -qF "$COUNT_SIBLING_LINE" "$dir/rustconf.com.tf" \
    || fail "rustconf.com.tf's \"2022\" CNAME line is not what this script expects - the corpus pin has moved"
  SIBLING_LINE="$COUNT_SIBLING_LINE" COUNT_LINE="$COUNT_LINE" perl -0777 -i -pe 's/\Q$ENV{SIBLING_LINE}\E\n/$ENV{SIBLING_LINE}\n$ENV{COUNT_LINE}\n/' "$dir/rustconf.com.tf"
  grep -qF "$COUNT_LINE" "$dir/rustconf.com.tf" \
    || fail "adding the \"2024\" CNAME line back to $dir/rustconf.com.tf did not take"
}

log "=== G-ORACLE: stock, dropping then restoring \"2024\" from module.rustconf_com's CNAME map, on cold_deploy's own state (plan-only - see header) ==="
PLAIN_COUNT_ORACLE="$WORK/plain-count-oracle"
cp -r "$PLAIN" "$PLAIN_COUNT_ORACLE"
drop_count_record "$PLAIN_COUNT_ORACLE"
( cd "$PLAIN_COUNT_ORACLE" && terraform init -input=false -no-color -plugin-dir="$MIRROR" >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_COUNT_ORACLE" && terraform init -input=false -no-color -plugin-dir="$MIRROR" 2>&1 | tail -30 ); fail "the day2_count stock oracle's reinit failed"; }
ORACLE_COUNT_DOWN_PLAN_OUT="$(cd "$PLAIN_COUNT_ORACLE" && terraform plan -input=false -no-color 2>&1)"; ORACLE_COUNT_DOWN_PLAN_RC=$?
[ "$ORACLE_COUNT_DOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_COUNT_DOWN_PLAN_OUT" | tail -40; fail "the day2_count stock oracle's scale-down plan exited $ORACLE_COUNT_DOWN_PLAN_RC"; }
grep -qF "  # $COUNT_ADDR will be destroyed" <<< "$ORACLE_COUNT_DOWN_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_COUNT_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-down plan does not destroy $COUNT_ADDR"; }
ORACLE_OTHER_TOUCHED_DOWN="$(grep -E '^  # module\.rustconf_com\.aws_route53_record\.' <<< "$ORACLE_COUNT_DOWN_PLAN_OUT" | grep -vF "$COUNT_ADDR" || true)"
[ -z "$ORACLE_OTHER_TOUCHED_DOWN" ] || { printf '%s\n' "$ORACLE_OTHER_TOUCHED_DOWN"; fail "stock's scale-down plan touches a rustconf_com record other than $COUNT_ADDR"; }
grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$ORACLE_COUNT_DOWN_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_COUNT_DOWN_PLAN_OUT" | tail -10; fail "stock's scale-down plan proposes something other than exactly one destroy"; }
log "  stock (plan-only): exactly one destroy proposed ($COUNT_ADDR), every sibling rustconf_com record untouched"

PLAIN_COUNT_ORACLE_UP="$WORK/plain-count-oracle-up"
cp -r "$PLAIN" "$PLAIN_COUNT_ORACLE_UP"
( cd "$PLAIN_COUNT_ORACLE_UP" && terraform init -input=false -no-color -plugin-dir="$MIRROR" >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_COUNT_ORACLE_UP" && terraform init -input=false -no-color -plugin-dir="$MIRROR" 2>&1 | tail -30 ); fail "the day2_count stock up-oracle's reinit failed"; }
STATE_RM_OUT="$(cd "$PLAIN_COUNT_ORACLE_UP" && terraform state rm "$COUNT_ADDR" 2>&1)"; STATE_RM_RC=$?
[ "$STATE_RM_RC" -eq 0 ] || { printf '%s\n' "$STATE_RM_OUT" | tail -30; fail "the day2_count stock up-oracle's state rm failed"; }
ORACLE_COUNT_UP_PLAN_OUT="$(cd "$PLAIN_COUNT_ORACLE_UP" && terraform plan -input=false -no-color 2>&1)"; ORACLE_COUNT_UP_PLAN_RC=$?
[ "$ORACLE_COUNT_UP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_COUNT_UP_PLAN_OUT" | tail -40; fail "the day2_count stock oracle's scale-up plan exited $ORACLE_COUNT_UP_PLAN_RC"; }
grep -qF "  # $COUNT_ADDR will be created" <<< "$ORACLE_COUNT_UP_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_COUNT_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-up plan does not create $COUNT_ADDR"; }
ORACLE_OTHER_TOUCHED_UP="$(grep -E '^  # module\.rustconf_com\.aws_route53_record\.' <<< "$ORACLE_COUNT_UP_PLAN_OUT" | grep -vF "$COUNT_ADDR" || true)"
[ -z "$ORACLE_OTHER_TOUCHED_UP" ] || { printf '%s\n' "$ORACLE_OTHER_TOUCHED_UP"; fail "stock's scale-up plan touches a rustconf_com record other than $COUNT_ADDR"; }
grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$ORACLE_COUNT_UP_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_COUNT_UP_PLAN_OUT" | tail -10; fail "stock's scale-up plan proposes something other than exactly one create"; }
log "  stock (plan-only): exactly one create proposed ($COUNT_ADDR, state simulated with 'terraform state rm' - no live object ever touched), every sibling rustconf_com record untouched"
gauntlet_end_stage

gauntlet_begin_stage migrate

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
grep -qF "$ZONES resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, $RECORDS skipped" <<< "$APPROVE_OUT" \
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
gauntlet_stage migrate pass "$ZONES stamped, $ZONES distinct hosted zones, one per module call"
gauntlet_begin_stage test_plan

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - state deleted, live-plan, empty + identities by value
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 3: no state file, live-plan ==="
rm -f "$ESTATE/terraform.tfstate" "$ESTATE/terraform.tfstate.backup"
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "the state file is still there"

plan_into() { ( cd "$ESTATE" && TF_LOG=trace "$TOFU" live-plan -input=false -no-color ); }

# Route53's own "list resource" RPC (a terraform-plugin-framework feature
# the provider uses only for CollectUnclaimed's account-wide sweep -
# internal/live/discovery/discovery.go's `case req.CollectUnclaimed &&
# !sweep`, exercised by PART E below - distinct from the CloudControl-based
# path every other discovery scan in this estate goes through) was observed
# TWICE, in two independent full runs of this script, to route to the real
# https://route53.amazonaws.com instead of the configured floci endpoint
# (confirmed by a full TF_LOG=trace capture: "override_region=us-east-1"
# followed by an actual HTTP request to route53.amazonaws.com, a 403
# InvalidClientTokenId, and the resulting empty/errored stream result
# misread as ProblemNoTags - "a resource with no tags" - because
# discovery.go's scanType never inspects a streamed ListResource result's
# own per-result diagnostics before falling into tag classification). A
# manual replay of the byte-identical plan against the byte-identical
# container, seconds later, with nothing else changed, succeeded cleanly
# every time (4 of 4) - the signature of a transient RPC-routing race in
# this concurrent, multi-worker environment rather than a configuration or
# identity defect, so this retries only that one specific signature rather
# than masking a real failure.
plan_into_retrying_route53() {
  local out rc n
  for n in 1 2 3; do
    out="$(plan_into 2>&1)"; rc=$?
    if [ "$rc" -ne 0 ] && grep -qF 'Listed resource with no tags' <<< "$out" && grep -qF 'aws_route53_zone' <<< "$out"; then
      log "  transient: the aws_route53_zone list RPC misrouted to real AWS (attempt $n/3) - retrying"
      sleep 3
      continue
    fi
    break
  done
  printf '%s' "$out"
  return "$rc"
}

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
gauntlet_stage test_plan pass "no resource change proposed, nothing foreign; all $INSTANCES rendered identities name a live hosted zone or record set"
gauntlet_begin_stage test_apply

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
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed); $BEFORE_Z zones / $BEFORE_R records unchanged, all $ZONES markers unmoved"
gauntlet_begin_stage drift_reconverge

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

# The address list above counts UPDATES only, so a destroy or a create
# alongside the one expected update would leave it reading 1 and this stage
# would pass while the estate was being rebuilt underneath it. The plan's own
# totals line is the independent check on that, and it is asserted before the
# address is, so a co-occurring change fails here rather than two steps later.
PLAN_TOTALS="$(grep -oE '^Plan: [0-9]+ to add, [0-9]+ to change, [0-9]+ to destroy' "$WORK/plan-drift.log" | tail -1)"
[ -n "$PLAN_TOTALS" ] \
  || { grep -E '^Plan:|^No changes' "$WORK/plan-drift.log"; fail "the drift plan printed no 'Plan: N to add, M to change, K to destroy' line at all - this stage's totals assertion is reading nothing"; }
if [ "${BREAK_STAGE5:-}" = "1" ]; then
  [ "$N_CHANGED" = "1" ] \
    && fail "BREAK_STAGE5=1 set (two records drifted), but the plan proposes fixing only 1 - this assertion is not load-bearing"
  log "  BREAK_STAGE5=1: the plan proposes fixing $N_CHANGED instances ($PLAN_TOTALS),"
  log "                  correctly more than one - the single-instance assertion and"
  log "                  reconverge apply below are skipped"
else
  [ "$PLAN_TOTALS" = "Plan: 0 to add, 1 to change, 0 to destroy" ] \
    || { grep -E '^  # .+ will be' "$WORK/plan-drift.log"; fail "the drift plan's own totals are \"$PLAN_TOTALS\", not \"Plan: 0 to add, 1 to change, 0 to destroy\" - something other than the one drifted record is in the diff"; }
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
  gauntlet_stage drift_reconverge pass "one untaggable record drifted, exactly $DRIFT_ADDR proposed and applied, TTL reconverged to $WANT_TTL, $RECORDS records and the parent marker intact"

  # ════════════════════════════════════════════════════════════════════════
  # PART F: REPLACE (day2_replace, active - live/GAUNTLET.md #9)
  # ════════════════════════════════════════════════════════════════════════
  #
  # Placed right after STAGE 5 and BEFORE PART D (day2_rename, below) on
  # purpose, the same convention corpus-ec2-instance-complete's own PART F
  # uses: module.areweasyncyet_rs is never touched by PART D's rename
  # (that stage's own two targets are module.rustaceans_org and module.
  # cratesio_com), so this section has no dependency on PART D's outcome.
  # module.areweasyncyet_rs's `domain` argument changes from
  # "areweasyncyet.rs" to "areweasyncyet-replaced.rs" - `domain` sets aws_
  # route53_zone.zone's own `name`, which is ForceNew (Route 53 has no
  # rename-hosted-zone API) - forcing a replace at the SAME declared
  # address. One resource cascades from the SAME dependency edge F-ORACLE
  # (above, right after cold_deploy) already names: the zone's own one A
  # record (its zone_id argument is ForceNew, and its own name also
  # interpolates var.domain) - a real, two-object shape, not a bug.
  #
  # THE create_before_destroy SCOPE NOTE (see corpus-sqs-basic's own PART F
  # for the full reasoning, reproduced only in summary here): OpenTofu core
  # rejects a `lifecycle` block on a `module` call, and patching this
  # estate's own vendored ./impl module to add create_before_destroy would
  # cross this corpus's reduction-only convention, so this evidence pass
  # exercises the default destroy-then-create ordering instead.
  #
  # NO BREAK=replace LEG: aws_route53_zone is ServerAssigned (Route 53
  # assigns the zone id; the domain name is not its import identity - the
  # same shape aws_instance/aws_security_group have, unlike aws_s3_bucket's
  # or aws_db_instance's own config-derived identity), so the manufactured-
  # coexistence check would hit the SAME fungible-slot regression
  # corpus-security-group-complete's own day2_replace section found and
  # documented in this same unit (a valid record short-circuits the
  # duplicate-slot claimant matcher before it ever runs) - not
  # re-measured here.
  gauntlet_begin_stage day2_replace
  record_key() { printf '%s' "$1" | base64 | tr '+/' '-_' | tr -d '=\n'; }
  record_import_id() { jq -r '.identity.import_id' "$1"; }
  F_ADDR="module.areweasyncyet_rs.aws_route53_zone.zone"
  F_RECORD="$ESTATE/.tofu-records/tofu-records/$ESTATE_NAME/aws_route53_zone/$(record_key "$F_ADDR")"

  log "=== F0. capture the live zone and its record ahead of the forced replace ==="
  F_OLD_ZONE_ID="$(zone_by_marker 'module.areweasyncyet_rs.aws_route53_zone.zone')"
  [ -n "$F_OLD_ZONE_ID" ] || fail "no live zone found by tofu-address=module.areweasyncyet_rs.aws_route53_zone.zone ahead of day2_replace"
  [ -f "$F_RECORD" ] || fail "no local record file found for $F_ADDR ahead of day2_replace"
  F_OLD_IMPORT_ID="$(record_import_id "$F_RECORD")"
  [ "$F_OLD_IMPORT_ID" = "$F_OLD_ZONE_ID" ] || fail "the record for $F_ADDR names $F_OLD_IMPORT_ID ahead of day2_replace, not $F_OLD_ZONE_ID"
  log "  $F_OLD_ZONE_ID, record import_id=$F_OLD_IMPORT_ID"

  log "=== F1. choudoufu: change the ForceNew domain argument, forcing a replace at the same declared address ==="
  sed -i.bak 's/domain  = "areweasyncyet\.rs"/domain  = "areweasyncyet-replaced.rs"/' "$ESTATE/areweasyncyet.rs.tf"
  rm -f "$ESTATE/areweasyncyet.rs.tf.bak"
  grep -q 'domain  = "areweasyncyet-replaced.rs"' "$ESTATE/areweasyncyet.rs.tf" || fail "changing module.areweasyncyet_rs's domain argument did not match - the corpus pin has moved"

  F_PLAN_OUT="$(plan_into 2>&1)"; F_PLAN_RC=$?
  [ "$F_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_PLAN_OUT" | tail -40; fail "the day2_replace plan exited $F_PLAN_RC"; }
  grep -qE '^  # module\.areweasyncyet_rs\.aws_route53_zone\.zone must be replaced' <<< "$F_PLAN_OUT" \
    || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not propose replacing module.areweasyncyet_rs's zone when its ForceNew domain argument changes"; }
  grep -qE '~ +name +=.+forces replacement' <<< "$F_PLAN_OUT" \
    || { printf '%s\n' "$F_PLAN_OUT"; fail "the plan does not mark the zone's name as forcing replacement"; }
  grep -qE '^  # module\.areweasyncyet_rs\.aws_route53_record\.a\["@"\] must be replaced' <<< "$F_PLAN_OUT" \
    || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not cascade the zone replace into its A record"; }
  grep -qF 'Plan: 2 to add, 0 to change, 2 to destroy.' <<< "$F_PLAN_OUT" \
    || { printf '%s\n' "$F_PLAN_OUT" | tail -10; fail "the day2_replace plan does not match F-ORACLE's own two-resource cascade"; }
  log "  choudoufu: exactly one zone replace at the same declared address, cascading into its one A record - matches F-ORACLE's own plan shape"

  F_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; F_APPLY_RC=$?
  [ "$F_APPLY_RC" -eq 0 ] || { printf '%s\n' "$F_APPLY_OUT" | tail -40; fail "the day2_replace apply exited $F_APPLY_RC"; }
  grep -qE 'Resources: 2 added, 0 changed, 2 destroyed' <<< "$F_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$F_APPLY_OUT"; fail "the day2_replace apply did not match the planned 2 added, 2 destroyed"; }

  awsl route53 get-hosted-zone --id "$F_OLD_ZONE_ID" >/dev/null 2>&1 \
    && fail "$F_OLD_ZONE_ID (the old zone) still exists after the replace - it was orphaned, not destroyed"
  log "  $F_OLD_ZONE_ID (the old zone) is gone - confirmed via the AWS CLI, not through choudoufu's own report"

  F_NEW_ZONE_ID="$(zone_by_marker 'module.areweasyncyet_rs.aws_route53_zone.zone')"
  [ -n "$F_NEW_ZONE_ID" ] && [ "$F_NEW_ZONE_ID" != "$F_OLD_ZONE_ID" ] \
    || fail "could not find a new, different zone carrying module.areweasyncyet_rs's tofu-address after the replace (got '$F_NEW_ZONE_ID')"
  log "  $F_NEW_ZONE_ID (the new object) carries tofu-address=module.areweasyncyet_rs.aws_route53_zone.zone - the marker moved onto the new object, read via the AWS CLI"

  # THE RECORD STORE, asserted by value (HANDOFF's safety rule; the
  # #398-guard shape: a stale record still naming the destroyed zone would
  # be exactly the wrong-marker failure that outranks a missing one). The
  # local record file at the SAME address must now hold the NEW zone's id,
  # not the one captured in F0.
  F_NEW_IMPORT_ID="$(record_import_id "$F_RECORD")"
  [ "$F_NEW_IMPORT_ID" = "$F_NEW_ZONE_ID" ] \
    || fail "the record for $F_ADDR names $F_NEW_IMPORT_ID after the replace, not the new object $F_NEW_ZONE_ID - a stale record still claiming the destroyed zone, the #398-guard shape"
  [ "$F_NEW_IMPORT_ID" != "$F_OLD_IMPORT_ID" ] \
    || fail "sanity: the record's import_id at $F_ADDR did not change at all across the replace"
  log "  record store: import_id $F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID at the same key ($F_ADDR) - read directly off the local record store file, not through choudoufu's own report"

  log "=== F2. one more plan: config and reality agree, no marker collision ==="
  F_FINAL_PLAN_OUT="$(plan_into 2>&1)"; F_FINAL_PLAN_RC=$?
  [ "$F_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_FINAL_PLAN_OUT" | tail -40; fail "the post-replace plan exited $F_FINAL_PLAN_RC"; }
  if grep -qE '^  # .+ (will be (created|updated|destroyed)|must be replaced)' <<< "$F_FINAL_PLAN_OUT"; then
    grep -E '^  # .+ (will be|must be)' <<< "$F_FINAL_PLAN_OUT"
    fail "the post-replace plan proposes a resource change"
  fi
  log "  no resource action proposed. The replace is complete and invisible to the next plan - no marker collision."

  gauntlet_stage day2_replace pass "choudoufu: changing module.areweasyncyet_rs's ForceNew domain argument proposed exactly one zone replace at the same declared address, cascading into its one A record - 2 to add, 2 to destroy, matching F-ORACLE's own plan shape; applied cleanly; the old zone ($F_OLD_ZONE_ID) is confirmed gone and the new zone ($F_NEW_ZONE_ID) carries the marker, both via the AWS CLI; the local record store's record at the same address now names the new zone, not the destroyed one; the next plan proposes no resource action. No BREAK=replace leg - see this section's own header comment (reusing corpus-security-group-complete's own finding from this same unit rather than re-measuring it here)."
  gauntlet_end_stage

  # ════════════════════════════════════════════════════════════════════════
  # PART D: RENAME (day2_rename, active - live/GAUNTLET.md #6)
  # ════════════════════════════════════════════════════════════════════════
  #
  # See the D-ORACLE comment above stage 2 for why choudoufu's own legs
  # below need only ONE moved block each (the zone), unlike the oracle's
  # per-child sweep. The adopted estate (stages 2-5) is still marked and
  # still converged, which is exactly the state a rename needs to start
  # from.
  gauntlet_begin_stage day2_rename
  log ""
  log "=== D0. capture the live zones this rename must not disturb ==="
  RUSTACEANS_ZONE="$(zone_by_marker 'module.rustaceans_org.aws_route53_zone.zone')" \
    || fail "no hosted zone carries tofu-address=module.rustaceans_org.aws_route53_zone.zone"
  CRATESIO_ZONE="$(zone_by_marker 'module.cratesio_com.aws_route53_zone.zone')" \
    || fail "no hosted zone carries tofu-address=module.cratesio_com.aws_route53_zone.zone"
  log "  $RUSTACEANS_ZONE (module.rustaceans_org, 1 A + 1 CNAME record), $CRATESIO_ZONE (module.cratesio_com, 0 records)"

  if [ "${BREAK:-}" = "2" ]; then
    log "=== D1 (BREAK=2). rename module.rustaceans_org -> module.rustaceans_org_final WITHOUT a moved block ==="
    sed -i.bak 's/module "rustaceans_org" {/module "rustaceans_org_final" {/' "$ESTATE/rustaceans.org.tf"
    rm -f "$ESTATE/rustaceans.org.tf.bak"
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color -plugin-dir="$MIRROR" >/dev/null 2>&1 ) || {
      ( cd "$ESTATE" && "$TOFU" init -input=false -no-color -plugin-dir="$MIRROR" 2>&1 | tail -20 ); fail "the BREAK=2 rename's reinit failed"; }
    BREAK_PLAN_OUT="$(plan_into 2>&1)"; BREAK_PLAN_RC=$?
    [ "$BREAK_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -30; fail "the BREAK=2 rename-without-moved plan exited $BREAK_PLAN_RC"; }
    # Verified directly (measured, not guessed): this is a genuinely
    # stateless live-plan (no local state file, ever), so - like
    # corpus-iam-read-only-policy's own BREAK=2 in this same batch - it
    # walks only the addresses the CURRENT config declares. The old,
    # no-longer-declared module.rustaceans_org is never visited at all, so
    # nothing is ever proposed for destroying it. And because the two
    # records' own untaggable identity is derived from THEIR PARENT ZONE'S
    # marker, which under the new module path has no marker at all yet (the
    # zone itself was never bound there), all three children under the new
    # path - the zone AND both records - come back as creates, not just the
    # zone alone.
    grep -qE '^  # module\.rustaceans_org\.' <<< "$BREAK_PLAN_OUT" \
      && { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=2: the old, no-longer-declared module unexpectedly still appears in the plan - this stage's check is not load-bearing"; }
    for addr in 'aws_route53_zone.zone' 'aws_route53_record.a["@"]' 'aws_route53_record.cname["www"]'; do
      grep -qF "  # module.rustaceans_org_final.$addr will be created" <<< "$BREAK_PLAN_OUT" \
        || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=2: renaming without a moved block did not propose creating module.rustaceans_org_final.$addr - this stage's check is not load-bearing"; }
    done
    log "  BREAK=2: correctly proposes creating all three of module.rustaceans_org_final's children (the zone, whose marker never existed there, AND its two records, whose identity derives from that same missing marker) - no destroy anywhere, the old module.rustaceans_org is simply never visited; the moved-block and live-mv checks below are skipped"
  else
    log "=== D1. choudoufu, moved block: module.rustaceans_org -> module.rustaceans_org_moved ==="
    sed -i.bak 's/module "rustaceans_org" {/module "rustaceans_org_moved" {/' "$ESTATE/rustaceans.org.tf"
    rm -f "$ESTATE/rustaceans.org.tf.bak"
    cat >> "$ESTATE/rustaceans.org.tf" <<'EOF'

moved {
  from = module.rustaceans_org.aws_route53_zone.zone
  to   = module.rustaceans_org_moved.aws_route53_zone.zone
}
EOF
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color -plugin-dir="$MIRROR" >/dev/null 2>&1 ) || {
      ( cd "$ESTATE" && "$TOFU" init -input=false -no-color -plugin-dir="$MIRROR" 2>&1 | tail -20 ); fail "the moved-block rename's reinit failed"; }
    MOVED_PLAN_OUT="$(plan_into 2>&1)"; MOVED_PLAN_RC=$?
    [ "$MOVED_PLAN_RC" -eq 0 ] || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -40; fail "the moved-block rename plan exited $MOVED_PLAN_RC"; }
    grep -qE '^  # .+ will be (destroyed|created)' <<< "$MOVED_PLAN_OUT" \
      && { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block rename proposes a destroy or a create - not zero churn (the two records deriving their identity from the zone's marker must not move)"; }
    grep -qE '^  # module\.rustaceans_org_moved\.aws_route53_zone\.zone will be updated in-place' <<< "$MOVED_PLAN_OUT" \
      || { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block plan does not propose an in-place update to module.rustaceans_org_moved.aws_route53_zone.zone"; }
    grep -qF 'Plan: 0 to add, 1 to change, 0 to destroy' <<< "$MOVED_PLAN_OUT" \
      || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -10; fail "the moved-block rename plan is not exactly one in-place change - only the zone itself carries a marker to rewrite"; }
    grep -qE '~ +"tofu-address" += +"module\.rustaceans_org\.aws_route53_zone\.zone" +-> +"module\.rustaceans_org_moved\.aws_route53_zone\.zone"' <<< "$MOVED_PLAN_OUT" \
      || { printf '%s\n' "$MOVED_PLAN_OUT"; fail "the moved-block plan does not show the zone's tofu-address marker being rewritten from the old address to the new one"; }
    log "  choudoufu: zero churn, one in-place tags update on the zone itself - its 2 record children (A, CNAME) do not move at all"

    MOVED_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; MOVED_APPLY_RC=$?
    [ "$MOVED_APPLY_RC" -eq 0 ] || { printf '%s\n' "$MOVED_APPLY_OUT" | tail -40; fail "the moved-block rename apply exited $MOVED_APPLY_RC"; }
    grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$MOVED_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$MOVED_APPLY_OUT"; fail "the moved-block rename apply was not exactly one in-place change"; }

    RUSTACEANS_ZONE_AFTER="$(zone_by_marker 'module.rustaceans_org_moved.aws_route53_zone.zone')" \
      || fail "no hosted zone carries tofu-address=module.rustaceans_org_moved.aws_route53_zone.zone after the rename"
    [ "$RUSTACEANS_ZONE_AFTER" = "$RUSTACEANS_ZONE" ] \
      || fail "the rustaceans.org zone's id changed across the rename ($RUSTACEANS_ZONE -> $RUSTACEANS_ZONE_AFTER) - it was destroyed and recreated, not renamed"
    [ "$(record_count)" = "$RECORDS" ] \
      || fail "the record set count is no longer $RECORDS after the moved-block rename - a record child moved when it should not have"
    log "  $RUSTACEANS_ZONE unchanged, tofu-address now module.rustaceans_org_moved.aws_route53_zone.zone, and all $RECORDS record sets across the estate are still there - read via the AWS CLI"

    log "=== D2. choudoufu, live-mv: module.cratesio_com -> module.cratesio_com_final, no moved block at all ==="
    sed -i.bak 's/module "cratesio_com" {/module "cratesio_com_final" {/' "$ESTATE/cratesio.com.tf"
    rm -f "$ESTATE/cratesio.com.tf.bak"
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color -plugin-dir="$MIRROR" >/dev/null 2>&1 ) || {
      ( cd "$ESTATE" && "$TOFU" init -input=false -no-color -plugin-dir="$MIRROR" 2>&1 | tail -20 ); fail "the live-mv rename's reinit failed"; }
    MV_OUT="$(cd "$ESTATE" && "$TOFU" live-mv -estate="$ESTATE_NAME" 'module.cratesio_com.aws_route53_zone.zone' 'module.cratesio_com_final.aws_route53_zone.zone' 2>&1)"; MV_RC=$?
    [ "$MV_RC" -eq 0 ] || { printf '%s\n' "$MV_OUT" | tail -30; fail "choudoufu live-mv exited $MV_RC"; }
    grep -qF 'Rewrote the ownership marker on one live resource. This was a cloud write.' <<< "$MV_OUT" \
      || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report a real write"; }
    grep -qF '"module.cratesio_com.aws_route53_zone.zone" -> "module.cratesio_com_final.aws_route53_zone.zone"' <<< "$MV_OUT" \
      || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report rewriting the tofu-address marker from the old address to the new one"; }
    log "  live-mv: $(grep -F 'live ID' <<< "$MV_OUT")"

    CRATESIO_ZONE_AFTER="$(zone_by_marker 'module.cratesio_com_final.aws_route53_zone.zone')" \
      || fail "no hosted zone carries tofu-address=module.cratesio_com_final.aws_route53_zone.zone after live-mv"
    [ "$CRATESIO_ZONE_AFTER" = "$CRATESIO_ZONE" ] \
      || fail "the cratesio.com zone's id changed across live-mv ($CRATESIO_ZONE -> $CRATESIO_ZONE_AFTER) - it was destroyed and recreated, not renamed"
    log "  $CRATESIO_ZONE unchanged, tofu-address now module.cratesio_com_final.aws_route53_zone.zone - read via the AWS CLI"

    log "=== D3. one more plan: config and markers agree on both renames, nothing proposed ==="
    FINAL_PLAN_OUT="$(plan_into 2>&1)"; FINAL_PLAN_RC=$?
    [ "$FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$FINAL_PLAN_OUT" | tail -40; fail "the post-rename plan exited $FINAL_PLAN_RC"; }
    grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$FINAL_PLAN_OUT" \
      && { grep -E '^  # .+ will be' <<< "$FINAL_PLAN_OUT"; fail "the post-rename plan proposes a resource change"; }
    log "  no resource change proposed. Both renames are complete and invisible to the next plan."

    gauntlet_stage day2_rename pass "moved block: module.rustaceans_org renamed to module.rustaceans_org_moved with zero churn (0 add, 1 change, 0 destroy) - only the zone's own marker rewritten, its 2 record children (A, CNAME) did not move; live-mv: module.cratesio_com (0 records) renamed to module.cratesio_com_final with zero churn, marker rewritten in place; stock oracle over the identical two-module rename on cold_deploy's own state also shows zero churn (0 add, 0 change, 0 destroy), using per-child moved blocks stock's own state-address tracking requires and choudoufu's stateless untaggable-record derivation does not; both live zone ids unchanged, read via the AWS CLI"

    # ══════════════════════════════════════════════════════════════════
    # PART E: REMOVE A BLOCK (day2_remove, active - live/GAUNTLET.md #7)
    # ══════════════════════════════════════════════════════════════════
    #
    # Starts from Part D's real, completed state: module.cratesio_com_
    # final (originally module.cratesio_com, renamed by live-mv with no
    # moved block) is bound and converged, 0 records. Its whole block is
    # removed here - E-ORACLE above already proved stock destroys cleanly
    # on cold_deploy's own state. See E-ORACLE's own comment for why the
    # removal target is a whole TAGGABLE zone rather than an untaggable
    # child's own for_each entry.
    gauntlet_begin_stage day2_remove
    log ""
    log "=== E0. capture the zone's own marker one more time ==="
    E_ZONE_BEFORE="$(zone_by_marker 'module.cratesio_com_final.aws_route53_zone.zone')" \
      || fail "no hosted zone carries tofu-address=module.cratesio_com_final.aws_route53_zone.zone before day2_remove even starts"
    [ "$E_ZONE_BEFORE" = "$CRATESIO_ZONE" ] \
      || fail "the cratesio.com zone id changed before day2_remove even started ($CRATESIO_ZONE -> $E_ZONE_BEFORE)"

    if [ "${BREAK_REMOVE:-}" = "1" ]; then
      log "=== E1 (BREAK_REMOVE=1). keep module.cratesio_com_final's block; no destroy may be proposed ==="
      BREAK_REMOVE_PLAN_OUT="$(plan_into_retrying_route53 2>&1)"; BREAK_REMOVE_PLAN_RC=$?
      [ "$BREAK_REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_REMOVE_PLAN_OUT" | tail -60; fail "the BREAK_REMOVE=1 kept-block plan exited $BREAK_REMOVE_PLAN_RC"; }
      grep -qE '^  # module\.cratesio_com_final\.aws_route53_zone\.zone will be destroyed' <<< "$BREAK_REMOVE_PLAN_OUT" \
        && { grep -E '^  # .+ will be' <<< "$BREAK_REMOVE_PLAN_OUT"; fail "BREAK_REMOVE=1: a destroy was proposed for module.cratesio_com_final's zone even though its block is still in the config - this stage's check is not load-bearing"; }
      grep -qE '^  # .+ will be (created|destroyed)' <<< "$BREAK_REMOVE_PLAN_OUT" \
        && { grep -E '^  # .+ will be' <<< "$BREAK_REMOVE_PLAN_OUT"; fail "BREAK_REMOVE=1: some resource action was proposed with the block still in the config"; }
      log "  BREAK_REMOVE=1: correctly proposes no resource action - the block is still declared"
    else
      log "=== E1. choudoufu: delete module.cratesio_com_final's block ==="
      perl -0pi -e 's/\nmodule "cratesio_com_final" \{.*?\n\}\n//s' "$ESTATE/cratesio.com.tf"
      grep -q 'module "cratesio_com_final"' "$ESTATE/cratesio.com.tf" \
        && fail "removing module.cratesio_com_final's block did not match - the config has moved"
      ( cd "$ESTATE" && "$TOFU" init -input=false -no-color -plugin-dir="$MIRROR" >/dev/null 2>&1 ) || {
        ( cd "$ESTATE" && "$TOFU" init -input=false -no-color -plugin-dir="$MIRROR" 2>&1 | tail -30 ); fail "the day2_remove reinit failed"; }
      REMOVE_PLAN_OUT="$(plan_into_retrying_route53 2>&1)"; REMOVE_PLAN_RC=$?
      [ "$REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -60; fail "the day2_remove plan exited $REMOVE_PLAN_RC"; }
      grep -qE '^  # module\.cratesio_com_final\.aws_route53_zone\.zone will be destroyed' <<< "$REMOVE_PLAN_OUT" \
        || { printf '%s\n' "$REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu does not propose destroying module.cratesio_com_final's zone when its block is deleted"; }
      grep -qE '^  # .+ will be (created|updated)' <<< "$REMOVE_PLAN_OUT" \
        && { printf '%s\n' "$REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's remove plan proposes something other than the one destroy"; }
      grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy' <<< "$REMOVE_PLAN_OUT" \
        || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -10; fail "choudoufu's remove plan proposes something other than exactly one destroy"; }
      log "  choudoufu: exactly one destroy (module.cratesio_com_final's zone), nothing else"

      REMOVE_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; REMOVE_APPLY_RC=$?
      [ "$REMOVE_APPLY_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_APPLY_OUT" | tail -40; fail "the day2_remove apply exited $REMOVE_APPLY_RC"; }
      grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$REMOVE_APPLY_OUT" \
        || { grep -E 'Apply complete' <<< "$REMOVE_APPLY_OUT"; fail "the day2_remove apply was not exactly one destroy"; }

      # Route 53 zone deletion, confirmed directly against the emulator,
      # not through choudoufu's own report: a deleted zone's id is simply
      # absent from list-hosted-zones.
      if E_STILL="$(awsl route53 get-hosted-zone --id "$E_ZONE_BEFORE" 2>&1)"; then
        echo "$E_STILL"; fail "hosted zone $E_ZONE_BEFORE still exists in the live account after the destroy - it was orphaned, not destroyed"
      fi
      log "  hosted zone $E_ZONE_BEFORE no longer exists - confirmed via the AWS CLI, not through choudoufu's own report"
      Z_AFTER="$(zone_ids | grep -c . | tr -d ' ')"
      [ "$Z_AFTER" = "$(( ZONES - 1 ))" ] \
        || fail "there are $Z_AFTER hosted zones after day2_remove, expected $(( ZONES - 1 ))"
      log "  $Z_AFTER hosted zones remain, one fewer than before"

      log "=== E2. one more plan: config and reality agree, nothing left to propose ==="
      E_FINAL_PLAN_OUT="$(plan_into_retrying_route53 2>&1)"; E_FINAL_PLAN_RC=$?
      [ "$E_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$E_FINAL_PLAN_OUT" | tail -60; fail "the post-remove plan exited $E_FINAL_PLAN_RC"; }
      grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$E_FINAL_PLAN_OUT" \
        && { grep -E '^  # .+ will be' <<< "$E_FINAL_PLAN_OUT"; fail "the post-remove plan proposes a resource change"; }
      log "  no resource action proposed. The removal is complete and invisible to the next plan."

      log ""
      log "STAGE E (day2_remove): PASS"
      gauntlet_stage day2_remove pass "choudoufu: deleting module.cratesio_com_final's block proposed exactly one destroy (0 add, 0 change, 1 destroy), applied cleanly (0 added, 0 changed, 1 destroyed), the hosted zone is genuinely gone from the live account (route53 get-hosted-zone on the old id now errors, read via the AWS CLI, not choudoufu's own report; $ZONES zones down to $(( ZONES - 1 ))), and the next plan proposes no resource action; stock oracle on cold_deploy's own state (E-ORACLE) also proposes exactly one destroy for the same zone (before any rename ever touched it)"
      log ""

      # ══════════════════════════════════════════════════════════════════
      # PART G: CHANGE COUNT (day2_count, active - live/GAUNTLET.md #8,
      # issue #359/#488)
      # ══════════════════════════════════════════════════════════════════
      #
      # Starts from Part E's real, completed removal: module.cratesio_com_
      # final is gone, the plan is empty (E2). The real knob this estate
      # offers day2_count: module.rustconf_com's own CNAME for_each set
      # (impl/variables.tf's `variable "CNAME"`, live since STAGE 1/cold
      # deploy and untouched by every stage above). "2024" is the key
      # scaled - the only CNAME key in this estate no other stage ever
      # names (STAGE 5/BREAK_STAGE5 use "2016"/"2017", day2_rename/
      # day2_remove/day2_replace never touch module.rustconf_com at all).
      # G-ORACLE, right after F-ORACLE far above, is the stock oracle for
      # the identical change, plan-only on a copy of cold_deploy's own
      # state (never applied - see G-ORACLE's own comment for why: it
      # shares the same $ENDPOINT account $PLAIN's own state, read by
      # STAGE 2/migrate right after it, depends on finding undisturbed).
      #
      # THE IDENTITY TRAP THIS SECTION FOUND (verified directly against
      # floci, no tofu in the loop, before writing any assertion below): a
      # Route 53 record set carries NO server-minted identifier of its own
      # at all. Unlike aws_iam_policy's PolicyId or an EC2 VPC endpoint's
      # VpcEndpointId - this same stage's own precedents in
      # corpus-iam-read-only-policy and corpus-xancloud-iac, both of which
      # assert a NEW id after a genuine destroy+recreate - a genuine
      # delete-then-recreate of the identical name/type/value returns a
      # byte-identical ListResourceRecordSets entry (Name, Type, TTL,
      # ResourceRecords) both times, confirmed by direct probe: create,
      # delete, recreate the same CNAME against floci with the AWS CLI, no
      # tofu in the loop - the only value that ever differed was
      # ChangeResourceRecordSets' own response ChangeInfo.Id, which is a
      # per-API-call change-batch tracking token, never a queryable
      # attribute of the record set itself afterward (list-resource-
      # record-sets does not return it). So "genuinely destroyed and
      # recreated" cannot be proven by an id-diff here - it is proven by
      # provable ABSENCE in between (the record missing entirely from
      # list-resource-record-sets during the scaled-down window), which is
      # what G1/G2 below check instead.
      #
      # BREAK_COUNT=1 exercises this stage's own Break control instead of
      # the real checks: after the real scale-down plan, assert the WRONG
      # sibling ($COUNT_SIBLING_ADDR rather than $COUNT_ADDR) was the one
      # destroyed - the Break text in tools/gauntlet/stages.go for
      # day2_count, verbatim: "Expect a different instance to be
      # destroyed; the assertion must fail."
      gauntlet_begin_stage day2_count
      COUNT_ZONE_ID="$(zone_by_marker "$COUNT_ZONE_MARKER")" \
        || fail "no hosted zone carries tofu-address=$COUNT_ZONE_MARKER ahead of day2_count"
      log "=== G0. capture the live sibling record a for_each scale must not disturb ==="
      COUNT_SIBLING_TTL_BEFORE="$(live_record_ttl "$COUNT_ZONE_ID" "$COUNT_SIBLING_NAME" "$COUNT_SIBLING_TYPE")"
      [ "$COUNT_SIBLING_TTL_BEFORE" = "$WANT_TTL" ] \
        || fail "the sibling $COUNT_SIBLING_NAME's live TTL is $COUNT_SIBLING_TTL_BEFORE ahead of day2_count, expected $WANT_TTL"
      COUNT_TTL_BEFORE="$(live_record_ttl "$COUNT_ZONE_ID" "$COUNT_RECORD_NAME" "$COUNT_RECORD_TYPE")"
      [ "$COUNT_TTL_BEFORE" = "$WANT_TTL" ] \
        || fail "$COUNT_RECORD_NAME's live TTL is $COUNT_TTL_BEFORE ahead of day2_count, expected $WANT_TTL - it is not there to scale down"
      log "  $COUNT_ADDR present (TTL=$COUNT_TTL_BEFORE), sibling $COUNT_SIBLING_ADDR present (TTL=$COUNT_SIBLING_TTL_BEFORE) - the sibling must stay untouched throughout"

      log "=== G1. scale the for_each down: drop \"2024\" from module.rustconf_com's CNAME map ==="
      drop_count_record "$ESTATE"
      ( cd "$ESTATE" && "$TOFU" init -input=false -no-color -plugin-dir="$MIRROR" >/dev/null 2>&1 ) || {
        ( cd "$ESTATE" && "$TOFU" init -input=false -no-color -plugin-dir="$MIRROR" 2>&1 | tail -20 ); fail "the count-scale-down reinit failed"; }
      COUNT_DOWN_PLAN_OUT="$(plan_into_retrying_route53 2>&1)"; COUNT_DOWN_PLAN_RC=$?
      [ "$COUNT_DOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | tail -40; fail "the day2_count scale-down plan exited $COUNT_DOWN_PLAN_RC"; }

      if [ "${BREAK_COUNT:-}" = "1" ]; then
        log "  BREAK_COUNT=1: asserting the WRONG instance ($COUNT_SIBLING_ADDR) was destroyed instead of $COUNT_ADDR"
        if grep -qF "  # $COUNT_SIBLING_ADDR will be destroyed" <<< "$COUNT_DOWN_PLAN_OUT"; then
          fail "BREAK_COUNT=1: the plan actually destroys $COUNT_SIBLING_ADDR - this assertion is not load-bearing"
        fi
        log "  BREAK_COUNT=1: correctly does NOT destroy $COUNT_SIBLING_ADDR - the wrong-instance assertion above fails to hold, as it must"
      else
        grep -qF "  # $COUNT_ADDR will be destroyed" <<< "$COUNT_DOWN_PLAN_OUT" \
          || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-down plan does not destroy $COUNT_ADDR"; }
        OTHER_TOUCHED_DOWN="$(grep -E '^  # module\.rustconf_com\.aws_route53_record\.' <<< "$COUNT_DOWN_PLAN_OUT" | grep -vF "$COUNT_ADDR" || true)"
        [ -z "$OTHER_TOUCHED_DOWN" ] || { printf '%s\n' "$OTHER_TOUCHED_DOWN"; fail "choudoufu's scale-down plan touches a rustconf_com record other than $COUNT_ADDR"; }
        grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$COUNT_DOWN_PLAN_OUT" \
          || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | tail -10; fail "choudoufu's scale-down plan proposes something other than exactly one destroy"; }
        log "  choudoufu: exactly one destroy ($COUNT_ADDR), every sibling untouched"

        COUNT_DOWN_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_DOWN_APPLY_RC=$?
        [ "$COUNT_DOWN_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_DOWN_APPLY_OUT" | tail -40; fail "the scale-down apply exited $COUNT_DOWN_APPLY_RC"; }
        grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$COUNT_DOWN_APPLY_OUT" \
          || { grep -E 'Apply complete' <<< "$COUNT_DOWN_APPLY_OUT"; fail "the scale-down apply was not exactly one destroy"; }

        # No PolicyId/VpcEndpointId equivalent exists for a Route 53 record
        # (see this section's own header comment) - the destroy is proven
        # by provable ABSENCE, not an id-diff.
        COUNT_ABSENT="$(live_record_ttl "$COUNT_ZONE_ID" "$COUNT_RECORD_NAME" "$COUNT_RECORD_TYPE")"
        [ "$COUNT_ABSENT" = "None" ] \
          || fail "$COUNT_RECORD_NAME is still present (TTL=$COUNT_ABSENT) after the scale-down destroy - it was not genuinely removed"
        COUNT_SIBLING_TTL_AFTER_DOWN="$(live_record_ttl "$COUNT_ZONE_ID" "$COUNT_SIBLING_NAME" "$COUNT_SIBLING_TYPE")"
        [ "$COUNT_SIBLING_TTL_AFTER_DOWN" = "$COUNT_SIBLING_TTL_BEFORE" ] \
          || fail "the sibling $COUNT_SIBLING_NAME's TTL changed across the scale-down: $COUNT_SIBLING_TTL_BEFORE -> $COUNT_SIBLING_TTL_AFTER_DOWN"
        [ "$(record_count)" = "$(( RECORDS - 1 ))" ] \
          || fail "the record set count is not $(( RECORDS - 1 )) after the scale-down destroy"
        log "  $COUNT_RECORD_NAME is genuinely absent from Route 53 (list-resource-record-sets returns no TTL, not merely a report from choudoufu); sibling $COUNT_SIBLING_NAME unchanged (TTL=$COUNT_SIBLING_TTL_AFTER_DOWN); $(( RECORDS - 1 )) record sets remain - all read via the AWS CLI"

        log "=== G2. scale back up: restore \"2024\" to module.rustconf_com's CNAME map ==="
        add_count_record "$ESTATE"
        ( cd "$ESTATE" && "$TOFU" init -input=false -no-color -plugin-dir="$MIRROR" >/dev/null 2>&1 ) || {
          ( cd "$ESTATE" && "$TOFU" init -input=false -no-color -plugin-dir="$MIRROR" 2>&1 | tail -20 ); fail "the count-scale-up reinit failed"; }
        COUNT_UP_PLAN_OUT="$(plan_into_retrying_route53 2>&1)"; COUNT_UP_PLAN_RC=$?
        [ "$COUNT_UP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | tail -40; fail "the day2_count scale-up plan exited $COUNT_UP_PLAN_RC"; }
        grep -qF "  # $COUNT_ADDR will be created" <<< "$COUNT_UP_PLAN_OUT" \
          || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-up plan does not create $COUNT_ADDR"; }
        OTHER_TOUCHED_UP="$(grep -E '^  # module\.rustconf_com\.aws_route53_record\.' <<< "$COUNT_UP_PLAN_OUT" | grep -vF "$COUNT_ADDR" || true)"
        [ -z "$OTHER_TOUCHED_UP" ] || { printf '%s\n' "$OTHER_TOUCHED_UP"; fail "choudoufu's scale-up plan touches a rustconf_com record other than $COUNT_ADDR"; }
        grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$COUNT_UP_PLAN_OUT" \
          || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | tail -10; fail "choudoufu's scale-up plan proposes something other than exactly one create"; }
        log "  choudoufu: exactly one create ($COUNT_ADDR), every sibling untouched"

        COUNT_UP_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_UP_APPLY_RC=$?
        [ "$COUNT_UP_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_UP_APPLY_OUT" | tail -40; fail "the scale-up apply exited $COUNT_UP_APPLY_RC"; }
        grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$COUNT_UP_APPLY_OUT" \
          || { grep -E 'Apply complete' <<< "$COUNT_UP_APPLY_OUT"; fail "the scale-up apply was not exactly one create"; }

        COUNT_TTL_AFTER_UP="$(live_record_ttl "$COUNT_ZONE_ID" "$COUNT_RECORD_NAME" "$COUNT_RECORD_TYPE")"
        [ "$COUNT_TTL_AFTER_UP" = "$WANT_TTL" ] \
          || fail "$COUNT_RECORD_NAME's TTL after the scale-up is $COUNT_TTL_AFTER_UP, not the configuration's $WANT_TTL - it was not genuinely recreated"
        COUNT_VALUE_AFTER_UP="$(awsl route53 list-resource-record-sets --hosted-zone-id "$COUNT_ZONE_ID" \
          --query "ResourceRecordSets[?Name=='$COUNT_RECORD_NAME' && Type=='$COUNT_RECORD_TYPE'].ResourceRecords[0].Value | [0]" --output text)"
        [ "$COUNT_VALUE_AFTER_UP" = "$COUNT_RECORD_VALUE" ] \
          || fail "$COUNT_RECORD_NAME's value after the scale-up is $COUNT_VALUE_AFTER_UP, not $COUNT_RECORD_VALUE"
        COUNT_SIBLING_TTL_AFTER_UP="$(live_record_ttl "$COUNT_ZONE_ID" "$COUNT_SIBLING_NAME" "$COUNT_SIBLING_TYPE")"
        [ "$COUNT_SIBLING_TTL_AFTER_UP" = "$COUNT_SIBLING_TTL_BEFORE" ] \
          || fail "the sibling $COUNT_SIBLING_NAME's TTL changed across the scale-up: $COUNT_SIBLING_TTL_BEFORE -> $COUNT_SIBLING_TTL_AFTER_UP"
        [ "$(record_count)" = "$RECORDS" ] \
          || fail "the record set count is not back to $RECORDS after the scale-up create"
        # The zone's own marker, unaffected throughout - only its
        # untaggable record child ever moved.
        COUNT_ZONE_AFTER="$(awsl route53 list-tags-for-resource --resource-type hostedzone --resource-id "$COUNT_ZONE_ID" \
          --query "ResourceTagSet.Tags[?Key=='tofu-address'].Value | [0]" --output text)"
        [ "$COUNT_ZONE_AFTER" = "$COUNT_ZONE_MARKER" ] \
          || fail "module.rustconf_com's zone tofu-address changed across day2_count: $COUNT_ZONE_MARKER -> $COUNT_ZONE_AFTER"
        log "  $COUNT_RECORD_NAME recreated (TTL=$COUNT_TTL_AFTER_UP, value=$COUNT_VALUE_AFTER_UP - Route 53 gives it no id of its own to diff, see this section's header); sibling $COUNT_SIBLING_NAME unchanged (TTL=$COUNT_SIBLING_TTL_AFTER_UP); $RECORDS record sets restored; the zone's own marker untouched - all read via the AWS CLI"

        log "=== G3. one more plan: config and reality agree, nothing left to propose ==="
        COUNT_FINAL_PLAN_OUT="$(plan_into_retrying_route53 2>&1)"; COUNT_FINAL_PLAN_RC=$?
        [ "$COUNT_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_FINAL_PLAN_OUT" | tail -40; fail "the post-scale-up plan exited $COUNT_FINAL_PLAN_RC"; }
        grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$COUNT_FINAL_PLAN_OUT" \
          && { grep -E '^  # .+ will be' <<< "$COUNT_FINAL_PLAN_OUT"; fail "the post-scale-up plan proposes a resource change"; }
        log "  no resource action proposed. The scale-down-then-up cycle is complete and invisible to the next plan."

        gauntlet_stage day2_count pass "choudoufu: dropping \"2024\" from module.rustconf_com's CNAME for_each map destroyed exactly $COUNT_ADDR (0 add, 0 change, 1 destroy), leaving sibling $COUNT_SIBLING_ADDR's TTL and $(( RECORDS - 1 )) remaining record sets untouched; adding it back created exactly the same key (0 add, 0 change -> 1 add, 0 change, 0 destroy), restoring its TTL/value and the $RECORDS record-set count, while the sibling and the parent zone's own marker stayed untouched throughout; the next plan is empty; a Route 53 record set carries no server-minted identifier of its own (verified directly against floci, no tofu in the loop: ListResourceRecordSets returns a byte-identical entry across a genuine delete/recreate, only ChangeResourceRecordSets' own per-call ChangeInfo.Id differs), so the destroy is proven by verified ABSENCE rather than an id-diff, unlike this stage's aws_iam_policy/PolicyId and EC2/VpcEndpointId precedents; the G-ORACLE stock oracle on the identical for_each change, plan-only on cold_deploy's own state, shows the identical shape: destroy the dropped key only, propose creating it back, every sibling key untouched both times"
      fi
      gauntlet_end_stage
    fi
    gauntlet_end_stage
  fi
fi
gauntlet_end_stage
gauntlet_end

log ""
log "STAGE 5 (drift and reconverge): PASS"
log ""

log "=== PASS: all five stages, real, against rust-lang/simpleinfra's own  ==="
log "=== terraform/dns estate - $ZONES tagged hosted zones and $RECORDS untaggable  ==="
log "=== record sets, cold-deployed by real Terraform, migrated by         ==="
log "=== live-import, and drift-corrected on an object that carries no tag ==="
