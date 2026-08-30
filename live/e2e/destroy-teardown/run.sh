#!/usr/bin/env bash
set -uo pipefail

# GitHub issue #320's mechanism, ruled in #425: "choudoufu apply -destroy"
# under a live block is a generalization of the existing orphan sweep, not a
# separate mechanism, so internal/command/live_mode.go no longer refuses
# plans.DestroyMode. This is the tier-1 (#522) crossing for it - the first
# time that lifted refusal is exercised against a real emulator rather than
# only the mock-cloud command tests (TestStatelessMode_applyDestroy).
#
# The claim under test, in one sentence: one "apply -destroy" removes every
# object THIS estate owns - across all three #522-mandatory shapes (a real
# count block, a real for_each map, a module-nested resource) and across a
# genuine dependency (a subnet inside a VPC, so the destroy graph has real
# ordering work to do) - and leaves nothing marked, with no ordering logic
# of this fork's own: the plan and apply that follow the lifted refusal are
# stock's, unmodified, exactly as day2_remove and day2_replace already lean
# on the same destroy-graph walker for one instance at a time.
#
# The oracle (live/GAUNTLET.md #11's own wording): "Stock apply -destroy on
# the same estate leaves the same empty account." So this script builds the
# IDENTICAL resource shapes twice on the same emulator - once with plain,
# unmodified `terraform` and a real state file, once with `choudoufu` and a
# live block - destroys each with its own "apply -destroy", and checks both
# empty-account claims the same way: by enumerating the live objects that
# carry each run's own marker tag, never by trusting a reported count alone
# (this repo has been burned by counts matching for the wrong reasons - see
# CLAUDE.md). The BREAK control (step 9) proves that enumeration actually has
# teeth: a resource deliberately left behind is not equivalent to an empty
# account, and the check must say so.
#
#   bash live/e2e/destroy-teardown/run.sh
#
# Needs Docker, the AWS CLI, and a real `terraform` binary on PATH (the
# oracle half - `choudoufu`'s own `-destroy` is not its own oracle). Needs no
# corpus: the estate is written out below, the same choice counted-module and
# deterministic-recreate make, since the shape being crossed is the teardown
# mechanism rather than anything a particular real configuration does.
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4743, clear of every
#                other shape fixture's own default: 4599, 4601, 4602, 4604,
#                4605, 4606, 4607, 4608, 4609, 4712, 4742).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#
# Exit codes: 0 on a real pass, non-zero on a real failure. Every assertion
# reads actual command output, an exit code, or the emulator's own answer
# through the AWS CLI - never a timeout.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
WORK="$(mktemp -d)"
FLOCI_PORT="${FLOCI_PORT:-4743}"
FLOCI_NAME="choudoufu-destroy-teardown-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="destroy-teardown-e2e"
STOCK_MARK="destroy-teardown-stock-mark"
LIVE_MARK="destroy-teardown-live-mark"

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
awsl() { aws --endpoint-url "$ENDPOINT" --region us-east-1 "$@"; }

# inventory_by_mark prints one line per live VPC or subnet carrying
# tag:Name=$1, sorted - the object-inventory check both empty-account
# assertions below are built on. Never a bare count: a count that happens to
# read 0 for the wrong reason (a filter typo, a query against the wrong
# endpoint) is indistinguishable from a real empty account, and this repo has
# shipped that mistake before. Enumerating the actual ids is what a BREAK
# control (step 9) can catch and a count cannot.
inventory_by_mark() {
  {
    awsl ec2 describe-vpcs --filters "Name=tag:Name,Values=$1" --query 'Vpcs[].VpcId' --output text | tr '\t' '\n'
    awsl ec2 describe-subnets --filters "Name=tag:Name,Values=$1" --query 'Subnets[].SubnetId' --output text | tr '\t' '\n'
  } | grep -v '^$' | sort
}

# resource_block writes the three #522-mandatory shapes, all real dependency
# included: a count block (aws_vpc.pool), a for_each map whose members
# depend on one count instance (aws_subnet.edge - deleting pool[0] before
# edge is a real DependencyViolation on real AWS, so this is not a toy
# ordering), and a module-nested resource (module.extra's aws_vpc.inner).
# Shared between the stock and live copies so a divergence between what
# choudoufu tears down and what stock tears down cannot be explained by the
# two configurations actually being different shapes.
resource_block() {
  local mark="$1"
  cat <<EOF
resource "aws_vpc" "pool" {
  count      = 2
  cidr_block = "10.\${count.index}.0.0/16"

  tags = {
    Name = "$mark"
  }
}

resource "aws_subnet" "edge" {
  for_each          = { a = "10.0.1.0/24", b = "10.0.2.0/24" }
  vpc_id            = aws_vpc.pool[0].id
  cidr_block        = each.value
  availability_zone = "us-east-1a"

  tags = {
    Name = "$mark"
  }
}

module "extra" {
  source = "./extra"
  mark   = "$mark"
}
EOF
}

extra_module_block() {
  cat <<'EOF'
variable "mark" {
  type = string
}

resource "aws_vpc" "inner" {
  cidr_block = "10.9.0.0/16"

  tags = {
    Name = var.mark
  }
}
EOF
}

provider_block() {
  cat <<'EOF'
provider "aws" {
  region                      = "us-east-1"
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style           = true
}
EOF
}

# ── 0. tools ────────────────────────────────────────────────────────────────
log "=== 0. tools ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v terraform >/dev/null 2>&1 || fail "a real terraform binary is not on PATH (it is the oracle for this crossing, not a checked-out tool)"

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

STOCK="$WORK/stock"
LIVEDIR="$WORK/live"
mkdir -p "$STOCK/extra" "$LIVEDIR/extra"

{
  cat <<'EOF'
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }
}

EOF
  provider_block
  echo
  resource_block "$STOCK_MARK"
} > "$STOCK/main.tf"
extra_module_block > "$STOCK/extra/main.tf"

{
  cat <<EOF
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }

  live {
    estate = "$ESTATE"
  }
}

EOF
  provider_block
  echo
  resource_block "$LIVE_MARK"
} > "$LIVEDIR/main.tf"
extra_module_block > "$LIVEDIR/extra/main.tf"
log "  two estate copies written: stock (plain state) and live (a live block, estate=$ESTATE)"

# ── 1. floci ────────────────────────────────────────────────────────────────
log "=== 1. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
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
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

# ── 2. the oracle half: stock stands the estate up ──────────────────────────
log "=== 2. stock: terraform apply (real state, no markers) ==="
( cd "$STOCK" && terraform init -input=false -no-color >/dev/null 2>&1 ) || fail "stock init failed"
STOCK_APPLY1="$(cd "$STOCK" && terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$STOCK_APPLY1" | grep -E '^Error|^│' | head -20
  fail "the stock apply failed"; }
grep -qE 'Apply complete! Resources: 5 added' <<< "$STOCK_APPLY1" \
  || { grep -E 'Apply complete' <<< "$STOCK_APPLY1"; fail "the stock apply did not create exactly 5 resources"; }
log "  $(grep -E 'Apply complete' <<< "$STOCK_APPLY1")"

STOCK_BEFORE="$(inventory_by_mark "$STOCK_MARK")"
STOCK_BEFORE_N="$(grep -c . <<< "$STOCK_BEFORE" || true)"
[ "$STOCK_BEFORE_N" = "5" ] || { printf '%s\n' "$STOCK_BEFORE"; fail "stock's own inventory holds $STOCK_BEFORE_N objects before destroy, want 5 (2 pool VPCs, 2 edge subnets, 1 module VPC)"; }
log "  stock inventory before destroy: 5 objects (2 VPCs from count, 2 subnets from for_each - one destroy-order dependency - 1 VPC from the module)"

# ── 3. stock's own "apply -destroy": the oracle ─────────────────────────────
log "=== 3. stock: terraform apply -destroy ==="
STOCK_APPLY2="$(cd "$STOCK" && terraform apply -destroy -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$STOCK_APPLY2" | grep -E '^Error|^│' | head -20
  fail "the stock destroy failed"; }
# "Apply complete!", not "Destroy complete!" - that wording is reserved for
# the "terraform destroy" alias, which sets a different view flag than
# "apply -destroy" does (and choudoufu's ApplyCommand mirrors the same
# split - see step 5 below). Both invoke DestroyMode identically underneath.
grep -qE 'Apply complete! Resources: 0 added, 0 changed, 5 destroyed' <<< "$STOCK_APPLY2" \
  || { grep -E 'Apply complete' <<< "$STOCK_APPLY2"; fail "stock's destroy did not remove exactly 5 resources"; }
log "  $(grep -E 'Apply complete' <<< "$STOCK_APPLY2")"

STOCK_AFTER="$(inventory_by_mark "$STOCK_MARK")"
[ -z "$STOCK_AFTER" ] || { printf '%s\n' "$STOCK_AFTER"; fail "stock's account is not empty after apply -destroy: $STOCK_AFTER"; }
log "  the oracle: stock's apply -destroy leaves the account empty, enumerated, not just counted"

# ── 4. choudoufu stands the SAME shapes up, under a live block ─────────────
log "=== 4. choudoufu: apply (live block, no state) ==="
( cd "$LIVEDIR" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "choudoufu init failed"
LIVE_APPLY1="$(cd "$LIVEDIR" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$LIVE_APPLY1" | grep -E '^Error|^│' | head -20
  fail "the choudoufu apply failed"; }
grep -qE 'Apply complete! Resources: 5 added, 0 changed, 0 destroyed' <<< "$LIVE_APPLY1" \
  || { grep -E 'Apply complete' <<< "$LIVE_APPLY1"; fail "the choudoufu apply did not create exactly 5 resources"; }
log "  $(grep -E 'Apply complete' <<< "$LIVE_APPLY1")"
[ ! -f "$LIVEDIR/terraform.tfstate" ] || fail "the live-block apply wrote a state file"

LIVE_BEFORE="$(inventory_by_mark "$LIVE_MARK")"
LIVE_BEFORE_N="$(grep -c . <<< "$LIVE_BEFORE" || true)"
[ "$LIVE_BEFORE_N" = "5" ] || { printf '%s\n' "$LIVE_BEFORE"; fail "choudoufu's own inventory holds $LIVE_BEFORE_N objects before destroy, want 5"; }
# Every one of the 5 must also carry this estate's own ownership marker -
# the thing a destroy plans against, not merely the Name tag used to filter
# it apart from stock's copy and floci's own default VPC.
for id in $LIVE_BEFORE; do
  if [[ "$id" == subnet-* ]]; then
    MARKER="$(awsl ec2 describe-subnets --subnet-ids "$id" \
      --query "Subnets[0].Tags[?Key=='tofu-estate'].Value | [0]" --output text)"
  else
    MARKER="$(awsl ec2 describe-vpcs --vpc-ids "$id" \
      --query "Vpcs[0].Tags[?Key=='tofu-estate'].Value | [0]" --output text)"
  fi
  [ "$MARKER" = "$ESTATE" ] || fail "$id carries tofu-estate=$MARKER, want $ESTATE - the sweep this destroy generalizes has nothing to plan against otherwise"
done
log "  choudoufu inventory before destroy: 5 objects, every one carrying tofu-estate=$ESTATE"

# ── 5. THE MECHANISM: choudoufu apply -destroy, previously a hard refusal ──
log "=== 5. choudoufu: apply -destroy (issue #320's lifted refusal) ==="
LIVE_APPLY2="$(cd "$LIVEDIR" && "$TOFU" apply -destroy -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$LIVE_APPLY2" | grep -E '^Error|^│' | head -20
  fail "choudoufu apply -destroy failed - if this names \"Only the normal planning mode is available\", the refusal in internal/command/live_mode.go was not actually lifted"; }
grep -qE 'Apply complete! Resources: 0 added, 0 changed, 5 destroyed' <<< "$LIVE_APPLY2" \
  || { grep -E 'Apply complete' <<< "$LIVE_APPLY2"; fail "choudoufu's apply -destroy did not destroy exactly 5 resources"; }
log "  $(grep -E 'Apply complete' <<< "$LIVE_APPLY2")"
[ ! -f "$LIVEDIR/terraform.tfstate" ] || fail "apply -destroy under a live block wrote a state file"

# ── 6. THE VALUE: the same empty-account claim, enumerated ─────────────────
log "=== 6. the empty-account assertion: same shape as step 3's oracle check ==="
LIVE_AFTER="$(inventory_by_mark "$LIVE_MARK")"
[ -z "$LIVE_AFTER" ] || { printf '%s\n' "$LIVE_AFTER"; fail "choudoufu's account is not empty after apply -destroy: $LIVE_AFTER"; }
# Stronger than re-filtering by the same tag: every one of the 5 specific ids
# read back in step 4 must individually be gone, not merely absent from a
# filtered list that a tag change could also explain.
#
# Content, not exit code: checked directly against this same emulator with
# no tofu in the loop (the same discipline deterministic-recreate's step 2
# premise check uses) - describe-vpcs errors (nonzero exit) for a deleted
# id, matching real AWS, but describe-subnets on this emulator answers a
# deleted id with an empty list and exit 0 rather than
# InvalidSubnetID.NotFound. An exit-code check would silently pass here
# whether or not the subnet was actually destroyed; only the response body
# tells the two apart.
for id in $LIVE_BEFORE; do
  if [[ "$id" == subnet-* ]]; then
    STILL="$(awsl ec2 describe-subnets --subnet-ids "$id" --query 'Subnets[0].SubnetId' --output text 2>/dev/null)"
    [ -z "$STILL" ] || [ "$STILL" = "None" ] \
      || fail "$id (a subnet from step 4's inventory) still exists after apply -destroy"
  else
    awsl ec2 describe-vpcs --vpc-ids "$id" >/dev/null 2>&1 \
      && fail "$id (a VPC from step 4's inventory) still describes successfully after apply -destroy"
  fi
done
log "  choudoufu's apply -destroy leaves the same empty account stock's oracle does - enumerated, and every specific id from before is individually gone"
log "  no ordering logic of this fork's own was involved: the plan and apply above are stock's own destroy-graph walker, unmodified, given every owned instance instead of one orphan at a time"

# ── 7. control: one resource left behind must NOT read as empty ────────────
# Run every time, not only under a BREAK flag - the same rule
# deterministic-recreate's step 9 and provisioner-taint's own control hold:
# a check that has never actually been made to fail is not evidence it can.
# This simulates an incomplete teardown directly against the emulator, with
# no choudoufu involved, so the failure mode under test is the ASSERTION
# above, not choudoufu's mechanism.
log "=== 7. control: leave one resource behind and confirm the empty-account check goes red ==="
LEFTOVER="$(awsl ec2 create-vpc --cidr-block 10.99.0.0/16 --tag-specifications \
  "ResourceType=vpc,Tags=[{Key=Name,Value=$LIVE_MARK},{Key=tofu-estate,Value=$ESTATE},{Key=tofu-address,Value=aws_vpc.leftover}]" \
  --query 'Vpc.VpcId' --output text)" || fail "control: could not create the deliberately-left-behind VPC"
CONTROL_AFTER="$(inventory_by_mark "$LIVE_MARK")"
if [ -z "$CONTROL_AFTER" ]; then
  fail "control failed: a VPC carrying this estate's own marker exists ($LEFTOVER) and the empty-account check still reported nothing - step 6's assertion is not measuring anything"
fi
[ "$CONTROL_AFTER" = "$LEFTOVER" ] \
  || fail "control: expected exactly the leftover VPC ($LEFTOVER) in the inventory, got:\n$CONTROL_AFTER"
log "  BREAK proved red: with $LEFTOVER left behind, the empty-account check correctly reports it rather than reading empty"
awsl ec2 delete-vpc --vpc-id "$LEFTOVER" >/dev/null 2>&1 || true

log ""
log "=== PASS ==="
log ""
log "Five objects across all three #522-mandatory shapes (count, for_each,"
log "module-nested), with a genuine destroy-order dependency (a subnet"
log "inside a VPC), torn down in one \"apply -destroy\" under live markers -"
log "previously a hard refusal (internal/command/live_mode.go, issue #320)."
log "Stock's own apply -destroy on the identical shapes is the oracle, both"
log "empty-account claims were checked by enumerating live objects rather"
log "than trusting a count, and the check was proven to go red when a"
log "resource is deliberately left behind."
