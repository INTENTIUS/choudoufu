#!/usr/bin/env bash
set -uo pipefail

# A module call expanded with count, crossed against a real emulator.
#
# A module call with a statically evaluable count and no count.index in its
# own arguments is admitted (issue #195, live/LIMITATIONS.md "child-module").
# internal/live/identity addresses the resources inside it per instance -
# module.counted[0].aws_vpc.main - because resolve.go's walkModule recurses
# once per instance key.
#
# internal/live/stamp did not. It read only a module call's for_each, so a
# resource under a count'd call was qualified with the UNKEYED module path:
#
#   count = 1   ->  module.counted.aws_vpc.main, an address identity
#                   resolution never computes and discovery never looks for.
#   count = 3   ->  one literal address written onto three real cloud
#                   objects, which is issue #280's defect by a third route.
#
# Both are wrong markers rather than missing ones, which is why this needs a
# crossing at all. internal/live/stamp/modulecontext_test.go evaluates the
# rewritten body and reads the tags out of cty - the value choudoufu INTENDED
# to write, and it asserts agreement through discovery.AddressMatches, which
# is choudoufu's own code on both sides. This script reads the tags off the
# VPCs themselves with the AWS CLI, and then makes choudoufu recognise them
# with no state file at all, which is the only test of whether the two halves
# actually meet.
#
#   bash live/e2e/counted-module/run.sh
#
# Needs Docker and the AWS CLI. Needs no corpus: the estate is written out
# below, because the shape being crossed is a module meta-argument rather
# than anything a particular real configuration does.
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#                Point it at a binary built BEFORE childExpansion existed and
#                this script fails at step 4 with module.one.aws_vpc.main -
#                the unkeyed spelling - which is how it was checked to be
#                measuring anything.
#   FLOCI_PORT   host port for the emulator (default 4607, clear of run.sh's
#                4566, dataread-projection's 4599, tagging-sweep's 4601,
#                create-over's 4602, per-element's 4604, record-located's
#                4608 and repeated-module's 4609 (#520), so every harness can
#                run at once).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
WORK="$(mktemp -d)"
EST="$WORK/estate"
FLOCI_PORT="${FLOCI_PORT:-4607}"
FLOCI_NAME="choudoufu-counted-module-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="counted-module"

# What each VPC must carry, keyed by its own cidr_block so the expectation is
# matched to the object rather than to whatever order the emulator lists in.
# Written out rather than derived from the run: an expectation computed from
# the same walk that produced the answer would agree with a wrong answer too.
#
# The ":0" is the escaping, not a typo. live/MARKERS.md writes an instance
# key as ":" plus the key, and a module call's key escapes exactly as a
# resource's own does, so module.one[0].aws_vpc.main is stamped
# module.one:0.aws_vpc.main.
WANT="10.10.0.0/16	module.one:0.aws_vpc.main
10.20.0.0/16	module.plain.aws_vpc.main
10.30.0.0/16	module.wrapped.module.inner:0.aws_vpc.main"

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

# ── 1. the estate ───────────────────────────────────────────────────────────
# Three module calls, three shapes, one VPC each:
#
#   one       count = 1, the shape that was stamped with the unkeyed path.
#   plain     a static call, the control - its marker must NOT gain a key.
#   wrapped   a static call around a count = 1 call, so the key has to
#             survive a nesting level rather than only appear at the top.
#
# aws_vpc is a marker-discovered type: its id is server-assigned, so the
# tofu-address tag is the only thing that says which live VPC belongs to
# which module instance. That is what makes step 5 a real test rather than a
# formality.
log "=== 1. estate ==="
mkdir -p "$EST/impl" "$EST/wrap"

cat > "$EST/main.tf" <<'EOF'
terraform {
  required_version = "~> 1"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }

  live {
    estate = "counted-module"
  }
}

provider "aws" {
  region = "us-west-1"

  skip_credentials_validation = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true
}

module "one" {
  source = "./impl"
  count  = 1
  cidr   = "10.10.0.0/16"
}

module "plain" {
  source = "./impl"
  cidr   = "10.20.0.0/16"
}

module "wrapped" {
  source = "./wrap"
  cidr   = "10.30.0.0/16"
}
EOF

cat > "$EST/impl/main.tf" <<'EOF'
variable "cidr" {
  type = string
}

resource "aws_vpc" "main" {
  cidr_block = var.cidr
}
EOF

cat > "$EST/wrap/main.tf" <<'EOF'
variable "cidr" {
  type = string
}

module "inner" {
  source = "../impl"
  count  = 1
  cidr   = var.cidr
}
EOF
log "  three module calls: count = 1, static, and a static call around a count = 1 call"

# ── 2. floci ────────────────────────────────────────────────────────────────
log "=== 2. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"ec2"' <<< "$HEALTH" && break
  sleep 2
done
grep -q '"ec2"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (ec2) at $ENDPOINT"
log "  healthy"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-west-1

# ── 3. stand the estate up ──────────────────────────────────────────────────
log "=== 3. apply ==="
( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "init failed"
APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY_OUT" | grep -E '^Error|^│' | head -20
  fail "the first apply failed"; }
grep -qE 'Apply complete!' <<< "$APPLY_OUT" || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "the apply did not complete"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY_OUT" | head -1)"

# ── 4. THE VALUE, read off the objects ──────────────────────────────────────
# Deliberately not a verdict: before the fix the apply above reported success
# while writing module.one.aws_vpc.main - an address nothing resolves to -
# onto a real VPC. Read through the AWS CLI, never through choudoufu.
log "=== 4. the markers, read back with the AWS CLI ==="
: > "$WORK/got"
for v in $(awsl ec2 describe-vpcs --query 'Vpcs[].VpcId' --output text | tr '\t' '\n'); do
  CIDR="$(awsl ec2 describe-vpcs --vpc-ids "$v" --query 'Vpcs[0].CidrBlock' --output text)"
  A="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$v" "Name=key,Values=tofu-address" \
        --query 'Tags[0].Value' --output text)"
  E="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$v" "Name=key,Values=tofu-estate" \
        --query 'Tags[0].Value' --output text)"
  # floci creates a default VPC of its own, which this estate does not own
  # and must not have marked.
  if [ "$E" = "None" ] || [ -z "$E" ]; then
    log "  (unmarked: $v $CIDR - floci's own default VPC)"
    continue
  fi
  [ "$E" = "$ESTATE" ] || fail "VPC $v ($CIDR) carries tofu-estate=$E, expected $ESTATE"
  printf '%s\t%s\n' "$CIDR" "$A" >> "$WORK/got"
done
sort -o "$WORK/got" "$WORK/got"
printf '%s\n' "$WANT" | sort > "$WORK/want"
paste -d'\n' /dev/null /dev/null < /dev/null
column -t "$WORK/got" 2>/dev/null || cat "$WORK/got"

diff "$WORK/got" "$WORK/want" > "$WORK/addr.diff" \
  || { printf 'got (left) vs want (right):\n'; cat "$WORK/addr.diff"
       fail "the markers on the live VPCs are not the addresses the configuration declares. A count'd module call whose resource carries the UNKEYED path (module.one.aws_vpc.main rather than module.one:0.aws_vpc.main) is the defect internal/live/stamp's childExpansion fixes."; }

DISTINCT="$(cut -f2 "$WORK/got" | sort -u | grep -c .)"
[ "$DISTINCT" = 3 ] || fail "the three VPCs carry $DISTINCT distinct tofu-address markers, not 3"
log "  3 VPCs, 3 distinct addresses, and they are the 3 the configuration declares"

# ── 5. the consequence: choudoufu recognises its own markers ────────────────
# The assertion step 4 cannot make. A marker that is merely DIFFERENT from
# the other two would satisfy step 4's distinctness; only a marker discovery
# actually looks for survives deleting the state file and replanning, because
# with no state there is nothing but the tag to bind a live VPC by.
log "=== 5. delete the state file and replan ==="
rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"
( cd "$EST" && "$TOFU" live-plan -input=false -no-color ) > "$WORK/plan1.log" 2>&1
PLAN_RC=$?
if grep -q 'Two live resources claiming one address' "$WORK/plan1.log"; then
  grep -A 6 'Two live resources claiming one address' "$WORK/plan1.log" | head -12
  fail "two module instances are claiming one address"
fi
[ "$PLAN_RC" -eq 0 ] || { grep -E '^Error|^│' "$WORK/plan1.log" | head -20; fail "live-plan exited $PLAN_RC"; }
[ ! -f "$EST/terraform.tfstate" ] || fail "live-plan wrote a state file"
grep -qE 'No changes|Plan: 0 to add, 0 to change, 0 to destroy' "$WORK/plan1.log" \
  || { grep -E '^  # ' "$WORK/plan1.log" | head -20
       fail "the plan is not empty: at least one module instance's live VPC was not recognised from its marker, which is what an address nothing resolves to looks like from here"; }
log "  no state file, nothing to create"

# ── 6. and it converges ─────────────────────────────────────────────────────
log "=== 6. the next apply adds nothing, and the markers do not move ==="
APPLY2="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY2" | tail -20; fail "the second apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY2" \
  || { grep -E 'Apply complete' <<< "$APPLY2"; fail "the second apply was not a no-op"; }

: > "$WORK/got2"
for v in $(awsl ec2 describe-vpcs --query 'Vpcs[].VpcId' --output text | tr '\t' '\n'); do
  CIDR="$(awsl ec2 describe-vpcs --vpc-ids "$v" --query 'Vpcs[0].CidrBlock' --output text)"
  A="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$v" "Name=key,Values=tofu-address" \
        --query 'Tags[0].Value' --output text)"
  [ "$A" = "None" ] && continue
  printf '%s\t%s\n' "$CIDR" "$A" >> "$WORK/got2"
done
sort -o "$WORK/got2" "$WORK/got2"
diff "$WORK/got" "$WORK/got2" > /dev/null \
  || fail "the markers on the live VPCs changed between the two runs"
log "  $(grep -E 'Apply complete' <<< "$APPLY2" | head -1), the same 3 markers, unmoved"

log ""
log "=== PASS ==="
log ""
log "A count = 1 module call, a static one, and a count = 1 call one level"
log "down, applied against an emulator. Each VPC carries its own module"
log "INSTANCE's address - read off the object, not off the plan - and the"
log "estate rebinds to all three from the tags alone with no state file."
log ""
log "Run this with TOFU_BIN pointing at a binary built before"
log "internal/live/stamp's childExpansion existed and step 4 fails with"
log "module.one.aws_vpc.main and module.wrapped.module.inner.aws_vpc.main."
