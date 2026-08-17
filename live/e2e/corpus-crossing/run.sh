#!/usr/bin/env bash
set -uo pipefail

# A real third-party estate crossed against a real emulator: issue #274's
# step 6, for .corpus/mastino/global/dns.
#
# 63 instances of aws_route53_zone and aws_route53_record, written by DataCite
# for their own production DNS and not for us. It passes live-check with zero
# refused sites and, before this script existed, had never been run against
# anything. "live-check says clean" and "applies, loses its state file, and
# replans empty" are different claims, and only the second one is the product.
#
# What the estate contributes that a generated fixture cannot:
#
#   1. TWO HOSTED ZONES WITH THE SAME NAME. aws_route53_zone.production and
#      aws_route53_zone.internal are both "datacite.org". The zone ID is
#      server-assigned, so nothing in the configuration tells them apart -
#      only the tofu-address marker does. Fifty-nine records then inherit
#      whichever zone ID their own zone_id reference resolved to, and
#      production-ns and internal-ns have the same name AND the same type,
#      differing in nothing but that inherited ID.
#
#   2. Fifty-nine untaggable instances. aws_route53_record has no tags
#      argument at all, so 59 of the 63 carry no marker and cannot. Their
#      identity re-derives from the declaration plus their zone's ID on
#      every run - the identity-needs-no-carrier path, at a scale a
#      hand-written fixture does not reach.
#
#   3. Names a fixture author would not think to write: a wildcard label
#      (*.blog.datacite.org, which Route 53 stores escaped as \052), a name
#      RELATIVE to its zone (_lovable.strategy), a label that repeats the
#      zone name inside itself (datacite.org._domainkey.datacite.org), and
#      two names spelled with the trailing dot Route 53 itself uses.
#
# Steps 4 and 5 are not decoration; each pins a defect this estate found on
# its first contact with a cloud, so that the day it stops being true the
# script says so instead of going quiet:
#
#   step 4  allow_overwrite is a create-time argument Route 53 never returns.
#           Under markers there is no state file to remember it, so the ten
#           wp-prod-staging records show an in-place update on EVERY run, and
#           applying that update does not settle it.
#
#   step 7  a record whose name is spelled with Route 53's own trailing dot
#           renders an import identity carrying that dot. The import
#           succeeds - Route 53 accepts either spelling - and the resulting
#           prior state keeps the dot while the provider normalises the
#           configuration's copy without it, so the plan proposes destroying
#           and recreating a record the estate already owns, once per run,
#           forever. Step 7 puts the dot back deliberately and asserts both
#           that the plan objects AND that the identity assertion names the
#           exact wrong string, which is the half a verdict cannot say.
#
#   bash live/e2e/corpus-crossing/run.sh
#
# Needs Docker, the AWS CLI, and .corpus populated (`just corpus-fetch`).
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4605, clear of run.sh's
#                4566, dataread-projection's 4599, tagging-sweep's 4601,
#                create-over's 4602 and per-element's 4604).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#
# The corpus checkout is shared across worktrees and is NEVER written to: the
# estate is copied out first and every delta below lands on the copy.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SRC="$ROOT/.corpus/mastino/global/dns"
WORK="$(mktemp -d)"
EST="$WORK/estate"
FLOCI_PORT="${FLOCI_PORT:-4605}"
FLOCI_NAME="choudoufu-corpus-crossing-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="mastino-global-dns"
INSTANCES=63
ZONES=4

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
awsl() { aws --endpoint-url "$ENDPOINT" --region eu-west-1 "$@"; }

# Every delta below asserts it landed. A corpus pin that moved out from under
# this script has to say so at the edit rather than three steps later as an
# unexplained plan.

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
[ -f "$EST/main.tf" ] && [ -f "$EST/tld.tf" ] || fail "the estate copy is missing main.tf or tld.tf"
log "  estate copied out of .corpus into $EST"

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
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=eu-west-1

# ── 2. the deltas ───────────────────────────────────────────────────────────
# Every edit this estate needs before it can run, and what kind each is.
log "=== 2. onboarding deltas ==="

# DELTA 1 + 2, ordinary onboarding. The estate declares `cloud { organization
# = "datacite-ng" ... }`; a module may declare remote state or a live block,
# never both, so the cloud block goes and the live block replaces it. The
# provider constraint "~> 5" resolves to a release with no list resources at
# all, which live-plan cannot discover a marker through (#269), so it is
# pinned to the version the rest of live/e2e pins.
cat > "$EST/terraform.tf" <<'EOF'
# DELTA 1 + 2. Was: `version = "~> 5"` and a `cloud { ... }` block.
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }

  required_version = ">= 1.6"

  live {
    estate = "mastino-global-dns"
  }
}
EOF
log "  DELTA 1  cloud block removed, live block added   (onboarding)"
log "  DELTA 2  provider pinned = 6.58.0                (onboarding, #269)"

# DELTA 3, emulator wiring: the flags with no environment-variable form.
# Both provider blocks, including the aliased us-east-1 one.
perl -0pi -e 's/^(  region\s*= (?:var\.region|"us-east-1")\n)/$1  skip_credentials_validation = true # DELTA 3\n  skip_metadata_api_check     = true\n  s3_use_path_style           = true\n/mg' "$EST/input.tf"
[ "$(grep -c 'DELTA 3' "$EST/input.tf")" = "2" ] \
  || fail "DELTA 3 reached $(grep -c 'DELTA 3' "$EST/input.tf") provider blocks, expected 2 - the corpus pin has moved"
log "  DELTA 3  emulator flags on both providers        (emulator)"

# DELTA 4, an emulator gap. aws_route53_zone.internal is a PRIVATE zone;
# floci answers AssociateVPCWithHostedZone with a bare 404 UnknownError, and
# create-hosted-zone --vpc returns PrivateZone: false for a zone it was
# handed a VPC for. The zone stays, its VPC association does not - which
# costs the run nothing it is measuring, since what makes this zone
# interesting is that it shares a NAME with aws_route53_zone.production.
perl -0pi -e 's/  vpc \{\n    vpc_id = var\.vpc_id\n  \}\n/  # DELTA 4: floci cannot associate a VPC with a hosted zone.\n/' "$EST/main.tf"
grep -q 'DELTA 4' "$EST/main.tf" || fail "DELTA 4 did not match the private zone's vpc block - the corpus pin has moved"
grep -q 'vpc_id = var.vpc_id' "$EST/main.tf" && fail "DELTA 4 left a vpc block behind"
log "  DELTA 4  private-zone VPC association removed    (EMULATOR GAP)"

# DELTA 5, stand-up only. Creating a hosted zone auto-creates its apex NS
# record, so a plain aws_route53_record for that same name collides with
# InvalidChangeBatch on a FIRST create. The real estate's zones have existed
# for years; this run has to make them. The flag comes straight back off in
# step 3, and step 4 is about the estate's OWN use of it.
perl -0pi -e 's/(resource "aws_route53_record" "(?:production-ns|internal-ns|com-ns|eu-ns)" \{\n)/$1  allow_overwrite = true # DELTA 5\n/g' "$EST/main.tf" "$EST/tld.tf"
[ "$(grep -c 'DELTA 5' "$EST/main.tf" "$EST/tld.tf" | awk -F: '{s+=$2} END {print s}')" = "4" ] \
  || fail "DELTA 5 did not reach all four apex NS records"
log "  DELTA 5  allow_overwrite on 4 apex NS records    (stand-up only)"

# DELTA 6, values for the estate's eighteen unset variables plus a VPC for
# its data "aws_vpc" read. Seeded through the AWS CLI, never real AWS.
VPC="$(awsl ec2 create-vpc --cidr-block 10.30.0.0/16 --query 'Vpc.VpcId' --output text)"
VPCUS="$(aws --endpoint-url "$ENDPOINT" --region us-east-1 ec2 create-vpc --cidr-block 10.31.0.0/16 --query 'Vpc.VpcId' --output text)"
[ -n "$VPC" ] || fail "could not seed a VPC for the data aws_vpc read"
cat > "$EST/crossing.auto.tfvars" <<EOF
access_key = "test"
secret_key = "test"
region     = "eu-west-1"
vpc_id     = "$VPC"
vpc_id_us  = "$VPCUS"
changelog_dns_name = "changelog.example.net"
support_dns_name   = "support.example.net"
design_dns_name    = "design.example.net"
status_dns_name    = "status.example.net"
dkim_record                     = "v=DKIM1; k=rsa; p=crossing"
dmarc_record                    = "v=DMARC1; p=none; rua=mailto:dmarc@example.org"
google_site_verification_record = "google-site-verification=crossing"
ms_record                       = "MS=ms00000000"
verification_record             = "crossing-verification=abc123"
dkim_salesforce                 = "sf1.example.net"
dkim_alt_salesforce             = "sf2.example.net"
dkim_cm                         = "cm.example.net"
siteground_ip_stage         = "203.0.113.10"
siteground_ip_prod          = "203.0.113.11"
siteground_ip_homepage_prod = "203.0.113.12"
strategy_lovable_ip_prod    = "203.0.113.13"
EOF
log "  DELTA 6  tfvars + a seeded VPC ($VPC)  (onboarding)"

# DELTA 7, the trailing dot. Two records spell their name the way Route 53
# itself does. Step 7 puts one back and shows what it costs; see the header.
perl -pi -e 's/^  name    = "(_[0-9a-f]+\.corpus[^"]*)\."$/  name    = "$1" # DELTA 7/' "$EST/main.tf"
[ "$(grep -c 'DELTA 7' "$EST/main.tf")" = "2" ] \
  || fail "DELTA 7 reached $(grep -c 'DELTA 7' "$EST/main.tf") record names, expected 2 - the corpus pin has moved"
log "  DELTA 7  trailing dot off 2 record names         (CHOUDOUFU DEFECT)"

# ── 3. stand the estate up ──────────────────────────────────────────────────
log "=== 3. apply: $INSTANCES instances ==="
# -upgrade, and the estate's .terraform.lock.hcl deliberately not copied above:
# the lock pins 5.25.0, and against it plain `init` refuses DELTA 2 outright
# with "locked provider ... does not match configured version constraint".
# That is one more edit a real onboarding needs and this script does not
# measure, so it is taken off the table rather than worked around silently.
( cd "$EST" && "$TOFU" init -upgrade -input=false -no-color >/dev/null 2>&1 ) || fail "init -upgrade failed"
APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY_OUT" | grep -E '^Error|^│' | head -20
  fail "the first apply failed"; }
grep -qE "Apply complete! Resources: $INSTANCES added" <<< "$APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY_OUT"; fail "the apply did not create exactly $INSTANCES instances"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY_OUT" | head -1)"

# DELTA 5 comes straight back off: it was there to create the apex NS
# records, not to be measured.
sed -i.bak '/allow_overwrite = true # DELTA 5/d' "$EST/main.tf" "$EST/tld.tf"
rm -f "$EST"/*.bak

# Read the markers back through the AWS CLI, never through choudoufu. Four
# zones, four distinct addresses - including the two that share a NAME, which
# is the only thing that can tell those two apart.
MARKED=0
for z in $(awsl route53 list-hosted-zones --query 'HostedZones[].Id' --output text | tr '\t' '\n' | sed 's|/hostedzone/||'); do
  A="$(awsl route53 list-tags-for-resource --resource-type hostedzone --resource-id "$z" \
        --query "ResourceTagSet.Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  E="$(awsl route53 list-tags-for-resource --resource-type hostedzone --resource-id "$z" \
        --query "ResourceTagSet.Tags[?Key=='tofu-estate'].Value | [0]" --output text)"
  [ "$E" = "$ESTATE" ] || fail "hosted zone $z carries tofu-estate=$E, expected $ESTATE"
  printf '%s\n' "$A"
  MARKED=$((MARKED+1))
done > "$WORK/addresses"
[ "$MARKED" = "$ZONES" ] || fail "expected $ZONES marked hosted zones, got $MARKED"
[ "$(sort -u "$WORK/addresses" | grep -c .)" = "$ZONES" ] \
  || { sort "$WORK/addresses"
       fail "the $ZONES hosted zones do not carry $ZONES DISTINCT tofu-address markers. Two of them share the name datacite.org, so a collapsed address here means nothing downstream can tell them apart."; }
grep -qx 'aws_route53_zone.production' "$WORK/addresses" || fail "no zone carries aws_route53_zone.production"
grep -qx 'aws_route53_zone.internal' "$WORK/addresses" || fail "no zone carries aws_route53_zone.internal"
log "  $ZONES hosted zones marked, with $ZONES distinct addresses; the other $((INSTANCES-ZONES)) instances are aws_route53_record, which has no tags argument and carries nothing"

# ── 4. the estate's own allow_overwrite, pinned ─────────────────────────────
# wp-prod-staging declares allow_overwrite = true over count = 10. Route 53
# never returns that argument, and under markers there is no state file that
# remembers it, so it reads as null on every import.
log "=== 4. allow_overwrite: a create-time argument with nowhere to live ==="
plan_into() {
  rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"
  ( cd "$EST" && TF_LOG=trace "$TOFU" live-plan -input=false -no-color ) > "$1" 2>&1
  return $?
}
plan_into "$WORK/plan-aow.log" || { grep -vE '^[0-9]{4}-' "$WORK/plan-aow.log" | tail -20; fail "live-plan exited non-zero"; }
grep -qE '^Plan: 0 to add, 10 to change, 0 to destroy' "$WORK/plan-aow.log" \
  || { grep -E '^Plan:|^No changes' "$WORK/plan-aow.log"
       fail "expected exactly the 10 wp-prod-staging records to show an in-place update. If this is now 0, allow_overwrite has somewhere to live and step 4 should be inverted."; }
log "  10 in-place updates, all of them wp-prod-staging"

APPLY2="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY2" | tail -20; fail "the allow_overwrite apply failed"; }
grep -qE 'Resources: 0 added, 10 changed' <<< "$APPLY2" \
  || { grep -E 'Apply complete' <<< "$APPLY2"; fail "expected 0 added, 10 changed"; }
plan_into "$WORK/plan-aow2.log" || fail "the second allow_overwrite plan exited non-zero"
grep -qE '^Plan: 0 to add, 10 to change, 0 to destroy' "$WORK/plan-aow2.log" \
  || { grep -E '^Plan:|^No changes' "$WORK/plan-aow2.log"
       fail "the allow_overwrite diff SETTLED after an apply. That is a better world than the one this step was written in - re-read it and invert it."; }
log "  applying it does not settle it: the same 10 come back. Nothing on the"
log "  wire changes; the plan is simply never empty while the argument is there."

sed -i.bak 's|^  allow_overwrite = true$|  # DELTA 8 (was allow_overwrite = true)|' "$EST/main.tf"
rm -f "$EST"/*.bak
grep -q 'DELTA 8' "$EST/main.tf" || fail "DELTA 8 did not match the estate's own allow_overwrite"
log "  DELTA 8  allow_overwrite removed                 (CHOUDOUFU DEFECT)"

# ── 5. the crossing ─────────────────────────────────────────────────────────
log "=== 5. no state file, and nothing to do ==="
plan_into "$WORK/plan1.log" || { grep -vE '^[0-9]{4}-' "$WORK/plan1.log" | tail -20; fail "live-plan exited non-zero"; }
[ ! -f "$EST/terraform.tfstate" ] || fail "live-plan wrote a state file"
grep -qE 'No changes|Plan: 0 to add, 0 to change, 0 to destroy' "$WORK/plan1.log" \
  || { grep -vE '^[0-9]{4}-' "$WORK/plan1.log" | grep -E '^  # ' | head -20
       fail "the plan is not empty"; }
grep -qE '^Foreign resources: (none|nothing was swept)' "$WORK/plan1.log" \
  || { grep -E '^Foreign resources:' "$WORK/plan1.log"; fail "the plan reports foreign resources"; }
log "  nothing to create, nothing foreign"

# ── 6. THE VALUE, not the verdict ───────────────────────────────────────────
# An empty plan says the instances bound. It does not say what they bound BY.
# The expectation here comes from Route 53's own answer, never from
# choudoufu: every identity the run rendered must name a hosted zone that
# exists, or a record set that exists IN THE ZONE THE IDENTITY NAMES.
log "=== 6. the rendered identities, against Route 53's own answer ==="
live_keys() {
  awsl route53 list-hosted-zones --query 'HostedZones[].Id' --output text \
    | tr '\t' '\n' | sed 's|/hostedzone/||' > "$WORK/zones"
  : > "$WORK/live"
  while read -r z; do
    [ -n "$z" ] || continue
    ZN="$(awsl route53 get-hosted-zone --id "$z" --query 'HostedZone.Name' --output text)"
    ZN="${ZN%.}"
    awsl route53 list-resource-record-sets --hosted-zone-id "$z" \
      --query 'ResourceRecordSets[].[Name,Type]' --output text \
      | while IFS=$'\t' read -r n t; do
          n="${n%.}"
          # Route 53 stores a wildcard label escaped as \052.
          n="${n//\\052/*}"
          echo "${z}_${n}_${t}"
          # and the same object spelled relative to its own zone
          case "$n" in *".${ZN}") echo "${z}_${n%".${ZN}"}_${t}";; esac
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

# The two same-named zones, told apart. production-ns and internal-ns share a
# name and a type; only the inherited zone ID separates them, so the two
# identities must differ in exactly that.
PNS="$(grep -oE 'Z[A-Z0-9]+_datacite\.org_NS' "$WORK/plan1.log" | sort -u | grep -c .)"
[ "$PNS" = "2" ] \
  || { grep -oE 'Z[A-Z0-9]+_datacite\.org_NS' "$WORK/plan1.log" | sort -u
       fail "expected 2 distinct datacite.org NS identities, one per same-named zone, got $PNS"; }
log "  the two zones named datacite.org rendered 2 distinct apex NS identities"

# ── 7. the control ──────────────────────────────────────────────────────────
# Put one record's name back the way the estate spells it - with Route 53's
# own trailing dot - and show what that costs. The import still succeeds, the
# record still exists, and the identity is still wrong.
log "=== 7. control: the trailing dot, deliberately restored ==="
sed -i.bak 's|^  name    = "\(_a20336933a30c9ce94e198bf89da2260\.corpus\.datacite\.org\)" # DELTA 7$|  name    = "\1." # CONTROL|' "$EST/main.tf"
rm -f "$EST"/*.bak
grep -q '# CONTROL' "$EST/main.tf" || fail "the control edit did not match"

plan_into "$WORK/plan-break.log" || fail "the control live-plan exited non-zero"
grep -qE '^Plan: 1 to add, 0 to change, 1 to destroy' "$WORK/plan-break.log" \
  || { grep -E '^Plan:|^No changes' "$WORK/plan-break.log"
       fail "the control did not produce the replace-once-per-run shape it was written to pin"; }
if assert_identities "$WORK/plan-break.log" "$INSTANCES" > "$WORK/break.out" 2>&1; then
  fail "the identity assertion PASSED on a run whose identity is wrong. Step 6 is not measuring anything."
fi
grep -qF '_a20336933a30c9ce94e198bf89da2260.corpus.datacite.org._CNAME' "$WORK/break.out" \
  || { cat "$WORK/break.out"; fail "the identity assertion fired, but not on the string the control broke"; }
grep -qE 'identity count is' "$WORK/break.out" \
  && fail "the control was caught by the COUNT, not by the string. The instance still materialises here - it binds a real record by a wrong name - so the count must stay at $INSTANCES and the string is what has to catch it."
log "  the assertion fires, and names the string:"
log "   $(grep 'names no live record set' "$WORK/break.out")"

sed -i.bak 's|^  name    = "\(_a20336933a30c9ce94e198bf89da2260\.corpus\.datacite\.org\)\." # CONTROL$|  name    = "\1" # DELTA 7|' "$EST/main.tf"
rm -f "$EST"/*.bak
grep -q 'DELTA 7' "$EST/main.tf" || fail "the control was not reverted"

# ── 8. and it converges ─────────────────────────────────────────────────────
log "=== 8. the next run proposes nothing either, and adding nothing is what applying it does ==="
plan_into "$WORK/plan2.log" || fail "the second live-plan exited non-zero"
grep -qE 'No changes|Plan: 0 to add, 0 to change, 0 to destroy' "$WORK/plan2.log" \
  || { grep -vE '^[0-9]{4}-' "$WORK/plan2.log" | grep -E '^  # ' | head -20
       fail "the second plan is not empty, so the run does not converge"; }
assert_identities "$WORK/plan2.log" "$INSTANCES" || fail "the second run's identities do not all name live objects"

rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"
APPLY3="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY3" | tail -20; fail "the final apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY3" \
  || { grep -E 'Apply complete' <<< "$APPLY3"; fail "the final apply was not a no-op"; }

# And the emulator agrees: still four zones, not eight.
Z="$(awsl route53 list-hosted-zones --query 'length(HostedZones)' --output text)"
[ "$Z" = "$ZONES" ] || fail "there are now $Z hosted zones, not $ZONES - something was created over what the estate already owned"
log "  $(grep -E 'Apply complete' <<< "$APPLY3" | head -1)"
log "  $ZONES hosted zones, the same $ZONES"

# ── 9. no state file, ever ──────────────────────────────────────────────────
rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"
( cd "$EST" && "$TOFU" live-plan -input=false -no-color >/dev/null 2>&1 )
[ ! -f "$EST/terraform.tfstate" ] || fail "a state file exists after live-plan"

log ""
log "=== PASS ==="
log ""
log "63 instances of somebody else's production DNS, applied against an"
log "emulator, stripped of their state file and replanned empty twice, with"
log "every rendered identity checked against Route 53's own answer. Two"
log "hosted zones share a name and were told apart by their markers alone;"
log "59 of the 63 instances carry no marker and could not."
log ""
log "Steps 4 and 7 are the two defects this estate found. If either goes red,"
log "read it before assuming the fixture is wrong - both are pinned as they"
log "are because they were TRUE, and going red means someone fixed one."
