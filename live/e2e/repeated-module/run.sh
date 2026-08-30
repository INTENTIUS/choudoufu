#!/usr/bin/env bash
set -uo pipefail

# One local module called seven times, crossed against a real emulator: issue
# #280, for .corpus/simpleinfra/terraform/dns.
#
# The Rust project's DNS estate calls ./impl once per domain - seven static
# module calls of one source, which is the ordinary way a Terraform
# configuration gets factored. Each call creates one aws_route53_zone, and
# the zone's ID is server-assigned, so the tofu-address marker is the only
# thing that says which live zone belongs to which call.
#
# Before the fix, all seven zones came back carrying
#   address=module.rustconf_com.aws_route53_zone.zone
# The apply reported success, and the run after it was a hard error -
# "Two live resources claiming one address" - with seven real cloud objects
# already stamped with one identity.
#
# The mechanism: hclparse.Parser caches a parsed file by its name, so seven
# calls of ./impl reach one syntax tree. internal/live/stamp writes a literal
# address into the resource's tags argument, and seven writes into one shared
# node leave the last one standing.
#
# What this script can prove that a unit test cannot. The unit tests in
# internal/live/stamp/sharedbody_test.go evaluate the rewritten body and read
# the tags out of cty. That is the value choudoufu INTENDED to write. This
# reads the tags off the hosted zones themselves, through the AWS CLI, which
# is the value that reached the cloud - and step 5 is the consequence the
# unit test cannot stage at all, because "the next run is unplannable" needs
# a previous run's objects to be there.
#
#   bash live/e2e/repeated-module/run.sh
#
# Needs Docker, the AWS CLI, and .corpus populated (`just corpus-fetch`).
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#                Point it at a binary built BEFORE the fix and this script
#                fails at step 4 with the seven collapsed addresses printed,
#                which is how it was checked to be measuring anything.
#   FLOCI_PORT   host port for the emulator (default 4609, clear of run.sh's
#                4566, dataread-projection's 4599, tagging-sweep's 4601,
#                create-over's 4602, per-element's 4604 and
#                record-located's 4608 - moved off 4606 itself in #520,
#                which had been shared with lambda-residue and so could
#                never run alongside it - so every harness can run at
#                once). Note this default only matters for a hand-invoked
#                run: `go run ./tools/gauntlet run` always assigns
#                FLOCI_PORT itself (#520) and never falls back to this
#                value.
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#
# The corpus checkout is shared across worktrees and is NEVER written to: the
# estate is copied out first and every delta below lands on the copy.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SRC="$ROOT/.corpus/simpleinfra/terraform/dns"
WORK="$(mktemp -d)"
EST="$WORK/estate"
FLOCI_PORT="${FLOCI_PORT:-4609}"
FLOCI_NAME="choudoufu-repeated-module-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="simpleinfra-dns"
ZONES=7
INSTANCES=35

# The seven addresses, one per module call. They are written out rather than
# derived from the run: an expectation computed from the same walk that
# produced the answer would agree with a wrong answer too.
WANT_ADDRESSES="module.areweasyncyet_rs.aws_route53_zone.zone
module.arewewebyet_org.aws_route53_zone.zone
module.crates_io.aws_route53_zone.zone
module.cratesio_com.aws_route53_zone.zone
module.docsrs_com.aws_route53_zone.zone
module.rustaceans_org.aws_route53_zone.zone
module.rustconf_com.aws_route53_zone.zone"

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
awsl() { aws --endpoint-url "$ENDPOINT" --region us-west-1 "$@"; }

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
cp "$SRC"/*.tf "$EST/"
cp -R "$SRC/impl" "$EST/impl"
[ -f "$EST/impl/main.tf" ] || fail "the estate copy is missing impl/main.tf"
CALLS="$(grep -h '^module "' "$EST"/*.tf | grep -c .)"
[ "$CALLS" = "$ZONES" ] \
  || fail "the estate has $CALLS module calls, expected $ZONES - the corpus pin has moved and every count below is wrong"
log "  estate copied out of .corpus into $EST ($CALLS calls of ./impl)"

# ── 1. floci ────────────────────────────────────────────────────────────────
log "=== 1. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
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
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-west-1

# ── 2. the deltas ───────────────────────────────────────────────────────────
# Every edit this estate needs before it can run, and what kind each is. Each
# asserts it landed: a corpus pin that moved out from under this script has to
# say so at the edit, not three steps later as an unexplained plan.
log "=== 2. onboarding deltas ==="

# DELTA 1 + 2 + 3, ordinary onboarding plus emulator wiring. The estate
# declares backend "s3"; a module may declare remote state or a live block,
# never both. The provider constraint "~> 5.64" resolves to a release with no
# list resources at all, which live-plan cannot discover a marker through
# (#269), so it is pinned to the version the rest of live/e2e pins.
cat > "$EST/_terraform.tf" <<'EOF'
# DELTA 1 + 2 + 3. Was: `version = "~> 5.64"`, a backend "s3" block, and a
# bare provider block.
terraform {
  required_version = "~> 1"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }

  live {
    estate = "simpleinfra-dns"
  }
}

provider "aws" {
  region = "us-west-1"

  skip_credentials_validation = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true
}
EOF
log "  DELTA 1  backend s3 removed, live block added    (onboarding)"
log "  DELTA 2  provider pinned = 6.58.0                (onboarding, #269)"
log "  DELTA 3  emulator flags on the provider          (emulator)"

# There is no DELTA 4, and its absence is asserted. Every record in ./impl
# spells its name with Route 53's own trailing dot - `"${var.domain}."` - and
# this script used to take that dot off before applying, because the prior
# state kept the dot while the provider normalised the configuration's copy
# without it and those records then proposed a destroy-and-recreate on every
# run, forever. That is issue #281, and it is fixed: projection adopts the
# provider's own normalisation of an identity component, so the dot the
# estate wrote is the dot the run has to cope with.
#
# The estate is now applied EXACTLY as the Rust project wrote it, four
# trailing dots and all. If those four dots ever have to come off again,
# #281 has regressed and this is where to notice.
DOTS="$(grep -cF '${var.domain}."' "$EST/impl/main.tf")"
[ "$DOTS" = "4" ] \
  || fail "the estate has $DOTS record names spelled with a trailing dot, expected 4 - the corpus pin has moved and the #281 half of this run is measuring nothing"
log "  no DELTA 4: the 4 trailing dots are left ON      (#281 is fixed)"

# ── 3. stand the estate up ──────────────────────────────────────────────────
log "=== 3. apply ==="
( cd "$EST" && "$TOFU" init -upgrade -input=false -no-color >/dev/null 2>&1 ) || fail "init -upgrade failed"
APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY_OUT" | grep -E '^Error|^│' | head -20
  fail "the first apply failed"; }
grep -qE "Apply complete! Resources: $INSTANCES added" <<< "$APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY_OUT"; fail "the apply did not create exactly $INSTANCES instances"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY_OUT" | head -1)"

Z="$(awsl route53 list-hosted-zones --query 'length(HostedZones)' --output text)"
[ "$Z" = "$ZONES" ] || fail "there are $Z hosted zones, expected $ZONES"

# ── 4. THE VALUE, read off the objects ──────────────────────────────────────
# This is the assertion issue #280 is about, and it is deliberately not a
# verdict: the apply above reported success while writing one address onto
# all seven zones. Read through the AWS CLI, never through choudoufu.
log "=== 4. the seven markers, read back with the AWS CLI ==="
: > "$WORK/addresses"
: > "$WORK/report"
for z in $(awsl route53 list-hosted-zones --query 'HostedZones[].Id' --output text | tr '\t' '\n' | sed 's|/hostedzone/||'); do
  NAME="$(awsl route53 get-hosted-zone --id "$z" --query 'HostedZone.Name' --output text)"
  A="$(awsl route53 list-tags-for-resource --resource-type hostedzone --resource-id "$z" \
        --query "ResourceTagSet.Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  E="$(awsl route53 list-tags-for-resource --resource-type hostedzone --resource-id "$z" \
        --query "ResourceTagSet.Tags[?Key=='tofu-estate'].Value | [0]" --output text)"
  [ "$E" = "$ESTATE" ] || fail "hosted zone $z ($NAME) carries tofu-estate=$E, expected $ESTATE"
  printf '%-18s %-22s %s\n' "$z" "$NAME" "$A" >> "$WORK/report"
  printf '%s\n' "$A" >> "$WORK/addresses"
done
cat "$WORK/report"

DISTINCT="$(sort -u "$WORK/addresses" | grep -c .)"
[ "$DISTINCT" = "$ZONES" ] \
  || fail "the $ZONES hosted zones carry $DISTINCT DISTINCT tofu-address markers, not $ZONES. This is issue #280: seven calls of ./impl share one parsed configuration body, so the last call's address is the one all seven zones got. See internal/live/stamp/sharedbody.go."

# And the addresses are the right ones, not merely seven different strings.
# A distinct-but-wrong set would pass the count.
diff <(sort "$WORK/addresses") <(printf '%s\n' "$WANT_ADDRESSES" | sort) > "$WORK/addr.diff" \
  || { cat "$WORK/addr.diff"
       fail "the seven markers are distinct but are not the seven addresses the configuration declares"; }
log "  $ZONES zones, $ZONES distinct addresses, and they are the $ZONES the configuration declares"

# ── 5. the consequence: the next run is plannable ───────────────────────────
# With the defect, this step is where the estate died: seven live zones
# claiming one address is a hard resolve error, so the run that follows a
# successful apply cannot plan at all. The assertion is on the error's
# absence BY NAME, not merely on the exit code - a run that failed for some
# other reason would otherwise read as this one passing.
log "=== 5. delete the state file and replan ==="
rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"
( cd "$EST" && TF_LOG=trace "$TOFU" live-plan -input=false -no-color ) > "$WORK/plan1.log" 2>&1
PLAN_RC=$?
if grep -q 'Two live resources claiming one address' "$WORK/plan1.log"; then
  grep -A 6 'Two live resources claiming one address' "$WORK/plan1.log" | head -12
  fail "the estate is unplannable after its own apply: issue #280's end state"
fi
[ "$PLAN_RC" -eq 0 ] || { grep -E '^Error|^│' "$WORK/plan1.log" | head -20; fail "live-plan exited $PLAN_RC"; }
[ ! -f "$EST/terraform.tfstate" ] || fail "live-plan wrote a state file"
grep -qE 'No changes|Plan: 0 to add, 0 to change, 0 to destroy' "$WORK/plan1.log" \
  || { grep -E '^  # ' "$WORK/plan1.log" | head -20; fail "the plan is not empty"; }
grep -qE '^Foreign resources: (none|nothing was swept)' "$WORK/plan1.log" \
  || { grep -E '^Foreign resources:' "$WORK/plan1.log"; fail "the plan reports foreign resources"; }
log "  no state file, nothing to create, nothing foreign"

# ── 5b. THE VALUE, not the verdict ──────────────────────────────────────────
# An empty plan says the 35 instances bound. It does not say what they bound
# BY, and the whole subject of this script is a run that reported success
# while binding the wrong things. So: every identity the run rendered must
# name a hosted zone that exists, or a record set that exists IN THE ZONE THE
# IDENTITY NAMES - checked against Route 53's own answer, never against
# choudoufu's.
#
# This is also the only place the four trailing dots are checked. The plan
# verdict cannot see them: a record whose identity carries the dot still
# IMPORTS, because Route 53 accepts either spelling. It binds a real record
# by a name Route 53 does not use, and only the string says so.
log "=== 5b. the 35 rendered identities, against Route 53's own answer ==="
live_keys() {
  awsl route53 list-hosted-zones --query 'HostedZones[].Id' --output text \
    | tr '\t' '\n' | sed 's|/hostedzone/||' > "$WORK/zones"
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

# assert_identities <trace> <expected count> -> 0 clean, 1 something is wrong.
# Prints every identity that names nothing live.
assert_identities() {
  local log="$1" want="$2" rc=0 n
  grep -oE 'from import identity "[^"]*"' "$log" | sed 's/.*identity "//; s/"$//' | sort -u > "$WORK/ids"
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

# And the seven zone identities are seven DISTINCT zone IDs. #280's end state
# is seven addresses collapsed to one; the mirror of it is seven identities
# collapsed to one zone, which the count above would not catch on its own
# because the records would still be 28 distinct strings.
ZIDS="$(grep -E '^Z' "$WORK/ids" | grep -vc '_')"
[ "$ZIDS" = "$ZONES" ] \
  || { grep -E '^Z' "$WORK/ids" | grep -v '_'
       fail "the run rendered $ZIDS distinct hosted-zone identities, expected $ZONES"; }
log "  $ZONES of them are distinct hosted-zone IDs, one per module call"

# ── 5c. the control ─────────────────────────────────────────────────────────
# One instance, module.rustconf_com.aws_route53_record.cname["2016"], loses
# its persisted record and has its `name` argument rewritten to the bare,
# un-normalised form ("2016" instead of "2016.rustconf.com."). Every other
# instance - every other CNAME included - is untouched. With no record to
# fall back on, the identity for this one instance has to be derived fresh
# from the (now wrong) configuration, and that derivation does not append
# the zone's domain: it renders literally "2016", which names nothing in
# Route 53's own answer. The plan itself stays quiet regardless - nothing
# about a locally-derived identity string changes what the provider proposes
# for an object it already believes is unmodified - so this is exactly the
# case stage 3's own Proves line warns about: "An empty plan alone is not
# enough: a wrong identity can converge." A break the COUNT catches proves
# nothing about the strings: it just means an instance did not resolve at
# all. This one resolves, to the wrong thing. It has to be caught by the
# identity, or step 5b is decoration.
#
# (An earlier version of this control put a second trailing dot on the name.
# It was caught by the count - 22 identities instead of 35 - because the
# record then resolves to nothing at all. That is a weaker control and it is
# recorded here so it is not tried again.)
#
# (A second earlier version rewrote the WHOLE for_each's `name` expression to
# the relative form, for all 13 CNAMEs at once, on the theory that Route 53
# treats "2016" in zone rustconf.com as the same live object as
# "2016.rustconf.com." - so every CNAME would still materialise and the plan
# would stay empty, with only the rendered STRING changing. Checked against
# stock terraform against the same emulator (issue #541), that theory is
# false: `name` is ForceNew, and Route 53's own diff logic does not treat a
# relative and an absolute spelling of the same name as equal - stock
# proposes "-/+ ... must be replaced" for exactly this edit, and so did
# choudoufu. Once every CNAME becomes a destroy-then-create pair,
# "from import identity" only ever appears for the side being DESTROYED -
# the surviving, still-correctly-bound OLD object, read from its own record
# - so the count and membership checks below saw all 35 legitimate
# identities and passed, regardless of what the never-rendered NEW side
# would have been. That is a control that cannot fail, and it is why this
# one instead removes a record rather than editing every CNAME's name: with
# every neighbour's record and configuration untouched, nothing forces a
# replace, and the one instance under test is exactly where a genuinely
# lost record leaves it. A pure case change ("2016.RUSTCONF.COM." for
# "2016.rustconf.com.") was also tried and also rejected: Route 53 names are
# case-insensitive, both stock and choudoufu treat it as no change at all,
# and choudoufu's own no-op path keeps the prior, correctly-cased identity -
# so it cannot be used to force a wrong render either.)
log "=== 5c. control: one record lost, one identity deliberately wrong ==="
CONTROL_ADDR='module.rustconf_com.aws_route53_record.cname["2016"]'
CONTROL_REC="$(grep -rlF "\"address\":\"module.rustconf_com.aws_route53_record.cname[\\\"2016\\\"]\"" "$EST/.tofu-records" 2>/dev/null | head -1)"
[ -n "$CONTROL_REC" ] && [ -f "$CONTROL_REC" ] \
  || fail "no record found for $CONTROL_ADDR under $EST/.tofu-records - the record layout or the corpus pin has moved"
cp "$CONTROL_REC" "$WORK/control-record.bak"

cp "$EST/impl/main.tf" "$WORK/impl.main.tf.orig"
perl -0pi -e 's/(resource "aws_route53_record" "cname" \{.*?name    = each\.key == "\@" \? "\$\{var\.domain\}\." : )"\$\{each\.key\}\.\$\{var\.domain\}\."/$1(each.key == "2016" ? each.key : "\${each.key}.\${var.domain}.") # CONTROL/s' "$EST/impl/main.tf"
grep -q '# CONTROL' "$EST/impl/main.tf" \
  || { grep -n 'name    =' "$EST/impl/main.tf"; fail "the control edit did not match"; }

rm -f "$CONTROL_REC"
rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"
( cd "$EST" && TF_LOG=trace "$TOFU" live-plan -input=false -no-color ) > "$WORK/plan-break.log" 2>&1
if assert_identities "$WORK/plan-break.log" "$INSTANCES" > "$WORK/break.out" 2>&1; then
  fail "the identity assertion PASSED on a run whose identities are wrong. Step 5b is not measuring anything."
fi
grep -q 'names no live record set' "$WORK/break.out" \
  || { cat "$WORK/break.out"; fail "the assertion fired, but not on a record-set string"; }
log "  the assertion fires, and names the strings:"
grep 'names no live record set' "$WORK/break.out" | head -3 | sed 's/^/  /'

# The whole point of this control is that the failure above is invisible to
# anything that only reads the plan's own add/destroy counts - so prove the
# plan really did stay quiet, rather than assume it.
grep -qE 'No changes|Plan: 0 to add, 0 to change, 0 to destroy' "$WORK/plan-break.log" \
  || { grep -E '^  # ' "$WORK/plan-break.log" | head -10; fail "the control's plan is not empty - it is no longer isolated to the one broken identity, and the assertion above may be firing on a replace instead of a wrong string"; }
log "  and the plan itself proposed nothing: the wrong identity was invisible to it"

cp "$WORK/impl.main.tf.orig" "$EST/impl/main.tf"
grep -q '# CONTROL' "$EST/impl/main.tf" && fail "the control was not reverted"
cp "$WORK/control-record.bak" "$CONTROL_REC"
rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"

# ── 6. and it converges ─────────────────────────────────────────────────────
# One empty plan is a proposal. This is what applying it does.
log "=== 6. the next run proposes nothing either, and applying it adds nothing ==="
( cd "$EST" && TF_LOG=trace "$TOFU" live-plan -input=false -no-color ) > "$WORK/plan2.log" 2>&1 \
  || { grep -E '^Error|^│' "$WORK/plan2.log" | head -20; fail "the second live-plan exited non-zero"; }
grep -qE 'No changes|Plan: 0 to add, 0 to change, 0 to destroy' "$WORK/plan2.log" \
  || { grep -E '^  # ' "$WORK/plan2.log" | head -20; fail "the second plan is not empty, so the run does not converge"; }
assert_identities "$WORK/plan2.log" "$INSTANCES" \
  || fail "the second run's identities do not all name live objects"
log "  the second cold plan is empty too, with all $INSTANCES identities still binding"

rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"
APPLY2="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY2" | tail -20; fail "the second apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY2" \
  || { grep -E 'Apply complete' <<< "$APPLY2"; fail "the second apply was not a no-op"; }
Z2="$(awsl route53 list-hosted-zones --query 'length(HostedZones)' --output text)"
[ "$Z2" = "$ZONES" ] || fail "there are now $Z2 hosted zones, not $ZONES - something was created over what the estate already owned"
log "  $(grep -E 'Apply complete' <<< "$APPLY2" | head -1)"

# The markers did not move either. A second stamping pass writing a different
# address onto a zone the first one marked would leave the counts above
# untouched and the estate broken.
: > "$WORK/addresses2"
for z in $(awsl route53 list-hosted-zones --query 'HostedZones[].Id' --output text | tr '\t' '\n' | sed 's|/hostedzone/||'); do
  awsl route53 list-tags-for-resource --resource-type hostedzone --resource-id "$z" \
    --query "ResourceTagSet.Tags[?Key=='tofu-address'].Value | [0]" --output text >> "$WORK/addresses2"
done
diff <(sort "$WORK/addresses") <(sort "$WORK/addresses2") > /dev/null \
  || fail "the markers on the live zones changed between the two runs"
log "  the same $ZONES markers, unmoved"

log ""
log "=== PASS ==="
log ""
log "Seven calls of one local module, applied against an emulator, each"
log "hosted zone carrying its own call's address - read off the objects, not"
log "off the plan - and the estate still plannable afterwards."
log ""
log "Run this with TOFU_BIN pointing at a binary built before"
log "internal/live/stamp/sharedbody.go existed and step 4 fails with all"
log "seven zones carrying module.rustconf_com.aws_route53_zone.zone."
