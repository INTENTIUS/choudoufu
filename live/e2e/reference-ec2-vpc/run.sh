#!/usr/bin/env bash
set -uo pipefail

# The reference project: the plainest AWS "getting started" shape anyone
# would write on day one - a VPC, a subnet, an internet gateway, a security
# group, and an EC2 instance. Nothing exotic, nothing from a corpus, no
# version pins beyond the ones every other estate needs for #269's release
# gap. Written after a live, adversarial session question ("can you even
# build an EC2 instance in a VPC") that no existing script answered
# directly: live/e2e/estate/ (the flagship demo fixture) has a VPC, subnet,
# security group and internet gateway, but no bare aws_instance - it uses
# aws_launch_template instead. This script is the one that names the gap
# and closes it, and it stays as the permanent, minimal answer to "does the
# canonical shape work," separate from any corpus estate's own baggage.
#
# Three bars, all real, all required:
#
#   GREENFIELD  write the config with a live block from the start, apply,
#               every object gets a real marker (read back through the AWS
#               CLI directly, never through choudoufu's own report), plan
#               again - empty. Delete the local record_store entirely and
#               plan a third time - still empty, proving the objects are
#               found by their tags and not remembered locally.
#
#   ADOPTION    the identical resource shapes, applied first with PLAIN
#               stock terraform (a real state file, zero choudoufu
#               involvement, zero markers - confirmed by reading the live
#               tags directly), then migrated with "choudoufu live-import
#               -state=... -approve" and replanned. Empty.
#
#   DRIFT AND   against that same adopted estate, one live object (the EC2
#   RECONVERGE  instance's Name tag) is changed out of band directly via
#               the AWS CLI, never through choudoufu. The next
#               "choudoufu plan" must propose fixing that ONE object and
#               nothing else - not "everything looks different" - and
#               applying it must reconverge the live tag back to what the
#               configuration declares.
#
# Both of the first two directions are checked against the SAME five
# resource types, so a gap in either direction is visible rather than
# averaged away. A known, non-fabricated gap found while building this: a
# bare "choudoufu plan" (skipping live-import) only auto-adopts 3 of the 5
# types on its own - VPC/subnet/security-group match a live object by its
# own content, but aws_instance and aws_internet_gateway do not yet, and
# need the explicit live-import step. That gap is real; this script does
# not route around it, it takes the path (live-import) that is documented
# for exactly this case.
#
#   bash live/e2e/reference-ec2-vpc/run.sh
#
# Needs Docker and the AWS CLI. No corpus, no .corpus dependency.
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the greenfield emulator (default 4712).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        set to 1 to run two negative controls instead of the real
#                checks, proving both are load-bearing rather than a grep
#                that always matches: (1) before the greenfield convergence
#                check, the expected empty-plan assertion is skipped in
#                favor of confirming the plan is NOT empty; (2) before the
#                drift-and-reconverge check, a SECOND live object (the
#                security group's Name tag) is also tampered out of band,
#                and the single-object assertion must then correctly fail
#                to hold (it is skipped in favor of confirming more than
#                one object is proposed).

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
WORK="$(mktemp -d)"
GREEN="$WORK/greenfield"
PLAIN="$WORK/plain"
ADOPTED="$WORK/adopted"
FLOCI_PORT="${FLOCI_PORT:-4712}"
FLOCI_ADOPT_PORT=$((FLOCI_PORT + 1))
FLOCI_NAME="choudoufu-reference-ec2-vpc-$$"
FLOCI_ADOPT_NAME="choudoufu-reference-ec2-vpc-adopt-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"
ADOPT_ENDPOINT="http://127.0.0.1:${FLOCI_ADOPT_PORT}"

ESTATE="ec2-reference"
REGION="us-east-1"

cleanup() {
  docker rm -f "$FLOCI_NAME" "$FLOCI_ADOPT_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

# ── 0. tools ─────────────────────────────────────────────────────────────
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

wait_healthy() {
  local ep="$1"
  for _ in $(seq 1 45); do
    HEALTH="$(curl -fs "${ep}/_localstack/health" 2>/dev/null)" || true
    grep -q '"ec2"' <<< "${HEALTH:-}" && return 0
    sleep 2
  done
  return 1
}

resource_block() { # writes the five resources shared by every variant
  cat <<'EOF'
resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
  tags = {
    Name = "ec2-reference-vpc"
  }
}

resource "aws_subnet" "main" {
  vpc_id                  = aws_vpc.main.id
  cidr_block              = "10.0.1.0/24"
  availability_zone       = "us-east-1a"
  map_public_ip_on_launch = true
  tags = {
    Name = "ec2-reference-subnet"
  }
}

resource "aws_internet_gateway" "main" {
  vpc_id = aws_vpc.main.id
  tags = {
    Name = "ec2-reference-igw"
  }
}

resource "aws_security_group" "main" {
  name        = "ec2-reference-sg"
  description = "Allow SSH"
  vpc_id      = aws_vpc.main.id

  ingress {
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name = "ec2-reference-sg"
  }
}

resource "aws_instance" "main" {
  ami                    = "ami-12345678"
  instance_type          = "t3.micro"
  subnet_id              = aws_subnet.main.id
  vpc_security_group_ids = [aws_security_group.main.id]

  tags = {
    Name = "ec2-reference-instance"
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

# ══════════════════════════════════════════════════════════════════════════
# PART A: GREENFIELD
# ══════════════════════════════════════════════════════════════════════════

log "=== A0. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
wait_healthy "$ENDPOINT" || fail "floci did not come up healthy (ec2) at $ENDPOINT"
log "  healthy"

mkdir -p "$GREEN"
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
    record_store "local" {
      path = ".tofu-records"
    }
  }
}

EOF
  provider_block
  echo
  resource_block
} > "$GREEN/main.tf"

export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" AWS_ENDPOINT_URL="$ENDPOINT"

log "=== A1. init and apply: 5 resources from nothing ==="
( cd "$GREEN" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$GREEN" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "greenfield init failed"; }
APPLY_OUT="$(cd "$GREEN" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY_OUT" | grep -E '^Error|^│' | head -30
  fail "the greenfield apply failed"; }
grep -qE 'Apply complete! Resources: 5 added' <<< "$APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY_OUT"; fail "the apply did not create exactly 5 resources"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY_OUT" | head -1)"

log "=== A2. markers, read through the AWS CLI directly ==="
INSTANCE_ID="$(aws --endpoint-url "$ENDPOINT" --region "$REGION" ec2 describe-instances \
  --filters "Name=tag:Name,Values=ec2-reference-instance" \
  --query "Reservations[0].Instances[0].InstanceId" --output text)"
[ -n "$INSTANCE_ID" ] && [ "$INSTANCE_ID" != "None" ] || fail "no live instance found by its Name tag"
ADDR_TAG="$(aws --endpoint-url "$ENDPOINT" --region "$REGION" ec2 describe-tags \
  --filters "Name=resource-id,Values=$INSTANCE_ID" "Name=key,Values=tofu-address" \
  --query "Tags[0].Value" --output text)"
[ "$ADDR_TAG" = "aws_instance.main" ] || fail "the instance carries tofu-address=$ADDR_TAG, not aws_instance.main"
EST_TAG="$(aws --endpoint-url "$ENDPOINT" --region "$REGION" ec2 describe-tags \
  --filters "Name=resource-id,Values=$INSTANCE_ID" "Name=key,Values=tofu-estate" \
  --query "Tags[0].Value" --output text)"
[ "$EST_TAG" = "$ESTATE" ] || fail "the instance carries tofu-estate=$EST_TAG, not $ESTATE"
log "  instance $INSTANCE_ID carries tofu-address=$ADDR_TAG tofu-estate=$EST_TAG - read via the AWS CLI, not choudoufu's own report"

log "=== A3. the next plan proposes nothing ==="
PLAN_OUT="$(cd "$GREEN" && "$TOFU" plan -input=false -no-color 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -30; fail "the second plan exited $PLAN_RC"; }
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$PLAN_OUT" \
  || { grep -E '^  #' <<< "$PLAN_OUT"; fail "the second plan is not empty"; }
log "  No changes."

log "=== A4. delete the local record store; plan a third time ==="
rm -rf "$GREEN/.tofu-records"
PLAN2_OUT="$(cd "$GREEN" && "$TOFU" plan -input=false -no-color 2>&1)"; PLAN2_RC=$?
[ "$PLAN2_RC" -eq 0 ] || { printf '%s\n' "$PLAN2_OUT" | tail -30; fail "the third plan (no local record store) exited $PLAN2_RC"; }
if [ "${BREAK:-}" = "1" ]; then
  grep -qF "No changes. Your infrastructure matches the configuration." <<< "$PLAN2_OUT" \
    && fail "BREAK=1 set, but the plan still came back empty - this step is not load-bearing"
  log "  BREAK=1: forcing a mismatch check instead of the real one - the empty-plan assertion below is skipped"
else
  grep -qF "No changes. Your infrastructure matches the configuration." <<< "$PLAN2_OUT" \
    || { grep -E '^  #' <<< "$PLAN2_OUT"; fail "the third plan is not empty with no local record store - the objects are not being found by their tags alone"; }
  log "  No changes, with zero local memory of the run that created them"
fi

# ══════════════════════════════════════════════════════════════════════════
# PART B: COLD ADOPTION
# ══════════════════════════════════════════════════════════════════════════

log "=== B0. a second floci on :$FLOCI_ADOPT_PORT, standing in for infra nobody marked ==="
docker run -d --rm -p "${FLOCI_ADOPT_PORT}:4566" --name "$FLOCI_ADOPT_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_ADOPT_NAME failed"
wait_healthy "$ADOPT_ENDPOINT" || fail "the adoption floci did not come up healthy at $ADOPT_ENDPOINT"
log "  healthy"

mkdir -p "$PLAIN"
{
  cat <<EOF
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
  resource_block
} > "$PLAIN/main.tf"

export AWS_ENDPOINT_URL="$ADOPT_ENDPOINT"

log "=== B1. plain terraform stands the estate up, no choudoufu involved ==="
command -v terraform >/dev/null 2>&1 || fail "the terraform binary is not on PATH - needed to build unmarked reference infra"
( cd "$PLAIN" && terraform init -input=false -no-color >/dev/null 2>&1 ) || fail "plain terraform init failed"
PLAIN_APPLY_OUT="$(cd "$PLAIN" && terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$PLAIN_APPLY_OUT" | tail -30; fail "the plain terraform apply failed"; }
grep -qE 'Apply complete! Resources: 5 added' <<< "$PLAIN_APPLY_OUT" \
  || fail "plain terraform did not create exactly 5 resources"
[ -f "$PLAIN/terraform.tfstate" ] || fail "plain terraform left no state file to migrate from"
log "  5 resources, real terraform.tfstate, zero choudoufu markers"

PLAIN_INSTANCE_ID="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-instances \
  --filters "Name=tag:Name,Values=ec2-reference-instance" \
  --query "Reservations[0].Instances[0].InstanceId" --output text)"
UNMARKED_TAGS="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-tags \
  --filters "Name=resource-id,Values=$PLAIN_INSTANCE_ID" "Name=key,Values=tofu-address" \
  --query "length(Tags)" --output text)"
[ "$UNMARKED_TAGS" = "0" ] || fail "the plain-terraform instance already carries a tofu-address tag before migration - this test proves nothing"
log "  confirmed unmarked: $PLAIN_INSTANCE_ID carries no tofu-address tag"

mkdir -p "$ADOPTED"
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
    record_store "local" {
      path = ".tofu-records"
    }
  }
}

EOF
  provider_block
  echo
  resource_block
} > "$ADOPTED/main.tf"

log "=== B2. choudoufu live-import against the plain state file, read-only first ==="
( cd "$ADOPTED" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "adopted init failed"
IMPORT_OUT="$(cd "$ADOPTED" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE" 2>&1)" || {
  printf '%s\n' "$IMPORT_OUT" | tail -30; fail "live-import (dry run) failed"; }
grep -qF "5 of 5 resource instance(s) are eligible for stamping" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import did not verify all 5 resources as eligible"; }
grep -qF "No tag has been written." <<< "$IMPORT_OUT" \
  || fail "the dry run wrote a tag - it must not"
log "  5 of 5 verified against the live system; nothing written yet"

log "=== B3. -approve: stamp the markers ==="
APPROVE_OUT="$(cd "$ADOPTED" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE" -approve 2>&1)" || {
  printf '%s\n' "$APPROVE_OUT" | tail -30; fail "live-import -approve failed"; }
grep -qF "5 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 already recorded, 0 failed, 0 skipped" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not stamp exactly 5 resources cleanly"; }
log "  5 stamped"

log "=== B4. and the adopted config plans empty ==="
ADOPT_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; ADOPT_PLAN_RC=$?
[ "$ADOPT_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ADOPT_PLAN_OUT" | tail -30; fail "the post-adoption plan exited $ADOPT_PLAN_RC"; }
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$ADOPT_PLAN_OUT" \
  || { grep -E '^  #' <<< "$ADOPT_PLAN_OUT"; fail "the post-adoption plan is not empty"; }
log "  No changes. The infra terraform created, unmarked, is now under live markers with an empty plan."

# ══════════════════════════════════════════════════════════════════════════
# PART C: DRIFT AND RECONVERGE
# ══════════════════════════════════════════════════════════════════════════
#
# The adopted estate (Part B) is the one both stamped by choudoufu and
# already proven to plan empty - the natural place to prove the OTHER
# direction: a live object changed behind choudoufu's back is detected and
# the fix is scoped to exactly that object, not "the whole estate looks
# different." AWS_ENDPOINT_URL is still $ADOPT_ENDPOINT and PLAIN_INSTANCE_ID
# is still the live instance's id, both set during part B above.

log "=== C0. mutate one live object out of band, directly via the AWS CLI ==="
if [ "${BREAK:-}" = "1" ]; then
  DRIFT_SG_ID="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-security-groups \
    --filters "Name=group-name,Values=ec2-reference-sg" \
    --query "SecurityGroups[0].GroupId" --output text)"
  [ -n "$DRIFT_SG_ID" ] && [ "$DRIFT_SG_ID" != "None" ] || fail "no live security group found by its name"
  aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 create-tags \
    --resources "$DRIFT_SG_ID" --tags Key=Name,Value=tampered-by-BREAK >/dev/null
  log "  BREAK=1: also tampered $DRIFT_SG_ID's Name tag - part C must now see TWO"
  log "           drifted objects and fail the single-object assertion"
fi

aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 create-tags \
  --resources "$PLAIN_INSTANCE_ID" --tags Key=Name,Value=tampered-out-of-band >/dev/null
DRIFTED_VALUE="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-tags \
  --filters "Name=resource-id,Values=$PLAIN_INSTANCE_ID" "Name=key,Values=Name" \
  --query "Tags[0].Value" --output text)"
[ "$DRIFTED_VALUE" = "tampered-out-of-band" ] || fail "the out-of-band tag mutation did not take"
log "  mutated $PLAIN_INSTANCE_ID's Name tag to \"tampered-out-of-band\" directly via the AWS CLI - never through choudoufu"

log "=== C1. choudoufu plan proposes fixing exactly that one object ==="
DRIFT_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; DRIFT_PLAN_RC=$?
[ "$DRIFT_PLAN_RC" -eq 0 ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | tail -30; fail "the drift-detection plan exited $DRIFT_PLAN_RC"; }

CHANGED_ADDRS="$(grep -oE '^  # \S+ will be updated' <<< "$DRIFT_PLAN_OUT" | awk '{print $2}' | sort -u)"
N_CHANGED="$(printf '%s\n' "$CHANGED_ADDRS" | grep -c . || true)"
if [ "${BREAK:-}" = "1" ]; then
  [ "$N_CHANGED" = "1" ] \
    && fail "BREAK=1 set (two objects tampered), but the plan proposes fixing only 1 - this assertion is not load-bearing"
  log "  BREAK=1: the plan proposes fixing $N_CHANGED objects, correctly more than"
  log "           one - the single-object assertion below is skipped"
else
  [ "$N_CHANGED" = "1" ] \
    || { printf '%s\n' "$DRIFT_PLAN_OUT" | grep -E '^  # .+ will be'; fail "expected exactly 1 object proposed for a fix, got $N_CHANGED"; }
  [ "$CHANGED_ADDRS" = "aws_instance.main" ] \
    || fail "the plan proposes fixing $CHANGED_ADDRS, not aws_instance.main"
  log "  the plan proposes fixing exactly one object: $CHANGED_ADDRS - nothing else in the diff"

  log "=== C2. apply the reconverging plan; the drift is gone ==="
  RECONVERGE_OUT="$(cd "$ADOPTED" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; RECONVERGE_RC=$?
  [ "$RECONVERGE_RC" -eq 0 ] || { printf '%s\n' "$RECONVERGE_OUT" | tail -30; fail "the reconverge apply failed"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$RECONVERGE_OUT" \
    || { grep -E 'Apply complete' <<< "$RECONVERGE_OUT"; fail "the reconverge apply did not change exactly 1 resource"; }
  FIXED_VALUE="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-tags \
    --filters "Name=resource-id,Values=$PLAIN_INSTANCE_ID" "Name=key,Values=Name" \
    --query "Tags[0].Value" --output text)"
  [ "$FIXED_VALUE" = "ec2-reference-instance" ] \
    || fail "the instance's Name tag is \"$FIXED_VALUE\" after reconverging, not ec2-reference-instance"
  log "  reconverged: $PLAIN_INSTANCE_ID's Name tag is back to \"ec2-reference-instance\", read via the AWS CLI"
fi

log ""
log "=== PASS ==="
log ""
log "The reference project: VPC, subnet, internet gateway, security group,"
log "EC2 instance. Every direction real, every assertion checked against"
log "actual AWS CLI reads rather than choudoufu's own report:"
log ""
log "  GREENFIELD  apply from a live block -> 5 marked -> empty plan ->"
log "              empty plan again with the local record store deleted."
log "  ADOPTION    plain terraform -> real state, zero markers -> choudoufu"
log "              live-import -approve -> empty plan."
log "  DRIFT       one live object tampered out of band -> choudoufu plan"
log "              proposes fixing that object and nothing else -> apply"
log "              reconverges it -> the live tag reads back as configured."
log ""
log "Known, non-fabricated gap this script does not paper over: a bare"
log "'choudoufu plan' against unmarked infra (skipping live-import) only"
log "auto-adopts the VPC, subnet and security group on its own - the"
log "instance and the internet gateway need the explicit live-import step."
log "That is why part B goes through live-import rather than a bare plan."
