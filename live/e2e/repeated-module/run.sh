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
#   FLOCI_PORT   host port for the emulator (default 4606, clear of run.sh's
#                4566, dataread-projection's 4599, tagging-sweep's 4601,
#                create-over's 4602, per-element's 4604 and
#                record-located's 4605, so every harness can run at once).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#
# The corpus checkout is shared across worktrees and is NEVER written to: the
# estate is copied out first and every delta below lands on the copy.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SRC="$ROOT/.corpus/simpleinfra/terraform/dns"
WORK="$(mktemp -d)"
EST="$WORK/estate"
FLOCI_PORT="${FLOCI_PORT:-4606}"
FLOCI_NAME="choudoufu-repeated-module-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="simpleinfra-dns"
ZONES=7

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

# DELTA 4, GitHub issue #281, which is NOT this script's subject. Every record
# in ./impl spells its name with Route 53's own trailing dot. The import
# succeeds and the prior state keeps the dot while the provider normalises
# the configuration's copy without it, so those records propose a
# destroy-and-recreate on every run, forever. It is filed separately and
# worked around here the same way live/e2e/corpus-crossing/run.sh works
# around it - by taking the dot off - so that step 5 is measuring #280 and
# not #281.
perl -pi -e 'if (s/\$\{var\.domain\}\."/\${var.domain}"/g) { s/$/ # DELTA 4/ }' "$EST/impl/main.tf"
[ "$(grep -c 'DELTA 4' "$EST/impl/main.tf")" = "4" ] \
  || fail "DELTA 4 reached $(grep -c 'DELTA 4' "$EST/impl/main.tf") record names, expected 4 - the corpus pin has moved"
grep -qF '${var.domain}."' "$EST/impl/main.tf" \
  && fail "DELTA 4 left a trailing dot behind"
log "  DELTA 4  trailing dot off 4 record names         (#281, NOT #280)"

# ── 3. stand the estate up ──────────────────────────────────────────────────
log "=== 3. apply ==="
( cd "$EST" && "$TOFU" init -upgrade -input=false -no-color >/dev/null 2>&1 ) || fail "init -upgrade failed"
APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY_OUT" | grep -E '^Error|^│' | head -20
  fail "the first apply failed"; }
grep -qE 'Apply complete!' <<< "$APPLY_OUT" || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "the apply did not complete"; }
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
( cd "$EST" && "$TOFU" live-plan -input=false -no-color ) > "$WORK/plan1.log" 2>&1
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

# ── 6. and it converges ─────────────────────────────────────────────────────
# One empty plan is a proposal. This is what applying it does.
log "=== 6. the next run proposes nothing either, and applying it adds nothing ==="
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
