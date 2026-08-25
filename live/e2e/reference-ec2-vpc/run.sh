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
#               CLI directly, never through choudoufu's own report), the
#               local record store holds one record per instance (#364 A2 -
#               apply writes a record too, not just live-import), plan
#               again - empty. Delete the local record_store entirely and
#               plan a third time - still empty, proving the objects are
#               found by their tags and not remembered locally. Then the
#               gauntlet's own greenfield-stage oracle (live/GAUNTLET.md
#               #13): stock applies the IDENTICAL resource_block() fresh in
#               its own namespace (part B's second floci container, before
#               it is touched by anything else), and the two clouds'
#               structural inventories - cidr blocks, the subnet's AZ and
#               public-IP flag, the igw's existence, the security group's
#               rules, the instance's AMI and type - are compared object by
#               object via the AWS CLI on both endpoints, marker tags never
#               part of the comparison.
#
#   ADOPTION    the identical resource shapes, applied first with PLAIN
#               stock terraform (a real state file, zero choudoufu
#               involvement, zero markers - confirmed by reading the live
#               tags directly), then migrated with "choudoufu live-import
#               -state=... -approve" and replanned. Empty. That empty plan is
#               then applied - a genuine no-op, "0 added, 0 changed, 0
#               destroyed" - and the tofu-estate-tagged object count read via
#               the AWS CLI's resourcegroupstaggingapi is asserted unchanged
#               before and after (the gauntlet's test_apply stage).
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
#                that always matches: (1) before the greenfield "no local
#                record store" replan, the instance's Name tag is tampered
#                out of band via the AWS CLI, and the expected empty-plan
#                assertion must then correctly fail to hold (it is skipped
#                in favor of confirming the plan is NOT empty); (2) before
#                the drift-and-reconverge check, a SECOND live object (the
#                security group's Name tag) is also tampered out of band,
#                and the single-object assertion must then correctly fail
#                to hold (it is skipped in favor of confirming more than
#                one object is proposed); (3) before the greenfield-stage
#                oracle comparison, the internet gateway is dropped from the
#                expected inventory on the greenfield side, and the
#                object-by-object match must then correctly fail to hold.

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
gauntlet_begin

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

# resource_block_sg_renamed() is resource_block() with aws_security_group.main
# renamed to aws_security_group.renamed (and the instance's reference to it
# updated to match) - the day2_rename stage's first half, exercised through a
# `moved` block or through choudoufu live-mv depending on which caller adds
# one. The internet gateway is left as "main" so the second half (D3, below)
# has its own untouched resource to rename separately.
resource_block_sg_renamed() {
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

resource "aws_security_group" "renamed" {
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
  vpc_security_group_ids = [aws_security_group.renamed.id]

  tags = {
    Name = "ec2-reference-instance"
  }
}
EOF
}

# resource_block_both_renamed() carries resource_block_sg_renamed()'s
# security-group rename forward and also renames aws_internet_gateway.main to
# .renamed - the shape D3 (live-mv) and the stock oracle (D0) both plan
# against, once the security-group rename has already landed.
resource_block_both_renamed() {
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

resource "aws_internet_gateway" "renamed" {
  vpc_id = aws_vpc.main.id
  tags = {
    Name = "ec2-reference-igw"
  }
}

resource "aws_security_group" "renamed" {
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
  vpc_security_group_ids = [aws_security_group.renamed.id]

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

CURRENT_STAGE=greenfield
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

log "=== A2b. the record store holds every instance (#364 A2: apply writes a record too, not just live-import) ==="
GREEN_RECORD_FILES="$(find "$GREEN/.tofu-records/tofu-records" -type f ! -name '*.lock' ! -name '*.tmp-*' 2>/dev/null | wc -l | tr -d ' ')"
[ "$GREEN_RECORD_FILES" = "5" ] || fail "expected 5 records under the local record store after the greenfield apply (one per instance: vpc, subnet, igw, sg, instance), found $GREEN_RECORD_FILES"
log "  5 records persisted, one per managed instance, read directly off the local record store"

log "=== A3. the next plan proposes nothing ==="
PLAN_OUT="$(cd "$GREEN" && "$TOFU" plan -input=false -no-color 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -30; fail "the second plan exited $PLAN_RC"; }
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$PLAN_OUT" \
  || { grep -E '^  #' <<< "$PLAN_OUT"; fail "the second plan is not empty"; }
log "  No changes."

log "=== A4. delete the local record store; plan a third time ==="
rm -rf "$GREEN/.tofu-records"
if [ "${BREAK:-}" = "1" ]; then
  aws --endpoint-url "$ENDPOINT" --region "$REGION" ec2 create-tags \
    --resources "$INSTANCE_ID" --tags Key=Name,Value=tampered-by-BREAK >/dev/null
  log "  BREAK=1: tampered $INSTANCE_ID's Name tag out of band - the plan below must NOT come back empty"
fi
PLAN2_OUT="$(cd "$GREEN" && "$TOFU" plan -input=false -no-color 2>&1)"; PLAN2_RC=$?
[ "$PLAN2_RC" -eq 0 ] || { printf '%s\n' "$PLAN2_OUT" | tail -30; fail "the third plan (no local record store) exited $PLAN2_RC"; }
if [ "${BREAK:-}" = "1" ]; then
  grep -qF "No changes. Your infrastructure matches the configuration." <<< "$PLAN2_OUT" \
    && fail "BREAK=1 set, but the plan still came back empty - this step is not load-bearing"
  log "  BREAK=1: the plan correctly proposes fixing the tampered tag - the empty-plan assertion below is skipped"
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

CURRENT_STAGE=cold_deploy
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
gauntlet_stage cold_deploy pass "5 resources from plain terraform, a real terraform.tfstate, zero markers"

# ══════════════════════════════════════════════════════════════════════════
# PART B1.5: GREENFIELD ORACLE (greenfield, live/GAUNTLET.md #13, planned
# stage - this is the crossing that wires the evidence for it)
# ══════════════════════════════════════════════════════════════════════════
#
# Both floci containers are still up at this point: $ENDPOINT still holds
# the greenfield estate part A applied directly from a live block (5
# objects, markers written on create, already replanned empty twice), and
# $ADOPT_ENDPOINT just got stock's cold deploy of the IDENTICAL
# resource_block() (B1, above) in its own separate namespace - zero
# choudoufu involvement, confirmed unmarked. That is exactly the oracle the
# stage asks for: "the cloud after stock's cold deploy, compared object by
# object with marker tags normalised out." resource_shape() reads structural
# facts straight off the AWS CLI on each endpoint - cidr blocks, the
# subnet's AZ and public-IP flag, the igw's existence, the security group's
# ingress/egress rules, the instance's AMI and type - never through tofu
# state on either side, so the comparison cannot be fooled by choudoufu's
# own bookkeeping agreeing with itself.
CURRENT_STAGE=greenfield
log "=== B1.5. greenfield oracle: the greenfield estate (part A) against stock's cold deploy (B1), object by object ==="
resource_shape() { # $1 = endpoint
  local ep="$1"
  aws --endpoint-url "$ep" --region "$REGION" ec2 describe-vpcs \
    --filters "Name=tag:Name,Values=ec2-reference-vpc" \
    --query "Vpcs[0].CidrBlock" --output text 2>/dev/null | sed 's/^/vpc cidr=/'
  aws --endpoint-url "$ep" --region "$REGION" ec2 describe-subnets \
    --filters "Name=tag:Name,Values=ec2-reference-subnet" \
    --query "Subnets[0].[CidrBlock,AvailabilityZone,MapPublicIpOnLaunch]" --output text 2>/dev/null \
    | awk '{print "subnet cidr="$1" az="$2" pub="$3}'
  aws --endpoint-url "$ep" --region "$REGION" ec2 describe-internet-gateways \
    --filters "Name=tag:Name,Values=ec2-reference-igw" \
    --query "length(InternetGateways)" --output text 2>/dev/null | sed 's/^/igw n=/'
  aws --endpoint-url "$ep" --region "$REGION" ec2 describe-security-groups \
    --filters "Name=group-name,Values=ec2-reference-sg" \
    --query "SecurityGroups[0].IpPermissions[].[IpProtocol,FromPort,ToPort]" --output text 2>/dev/null \
    | sort | awk '{print "sg-in proto="$1" from="$2" to="$3}'
  aws --endpoint-url "$ep" --region "$REGION" ec2 describe-security-groups \
    --filters "Name=group-name,Values=ec2-reference-sg" \
    --query "SecurityGroups[0].IpPermissionsEgress[].[IpProtocol]" --output text 2>/dev/null \
    | sort | awk '{print "sg-eg proto="$1}'
  aws --endpoint-url "$ep" --region "$REGION" ec2 describe-instances \
    --filters "Name=tag:Name,Values=ec2-reference-instance" "Name=instance-state-name,Values=running,pending" \
    --query "Reservations[0].Instances[0].[ImageId,InstanceType]" --output text 2>/dev/null \
    | awk '{print "instance ami="$1" type="$2}'
}
GREEN_SHAPE="$(resource_shape "$ENDPOINT" | sort)"
if [ "${BREAK:-}" = "1" ]; then
  GREEN_SHAPE="$(resource_shape "$ENDPOINT" | grep -v '^igw ' | sort)"
  log "  BREAK=1: dropped the internet gateway from the expected inventory - the comparison below must fail"
fi
ORACLE_SHAPE="$(resource_shape "$ADOPT_ENDPOINT" | sort)"
if [ "${BREAK:-}" = "1" ]; then
  if [ "$GREEN_SHAPE" = "$ORACLE_SHAPE" ]; then
    fail "BREAK=1: dropping the internet gateway from the expected inventory should have made the comparison fail, but it still matched - this stage's check is not load-bearing"
  fi
  log "  BREAK=1: correctly mismatched with one resource dropped from the expected inventory - the real comparison below is skipped"
else
  if [ "$GREEN_SHAPE" != "$ORACLE_SHAPE" ]; then
    diff <(printf '%s\n' "$GREEN_SHAPE") <(printf '%s\n' "$ORACLE_SHAPE") || true
    fail "the greenfield estate's object inventory does not match stock's cold deploy, object by object"
  fi
  log "  object-by-object match: vpc cidr, subnet cidr/az/public-ip, igw count, security-group ingress+egress rules, instance ami+type - identical between the greenfield estate and stock's cold deploy, marker tags normalised out (never compared)"
  gauntlet_stage greenfield pass "5-object structural comparison (vpc/subnet/igw/sg/instance) between the greenfield estate and stock's cold deploy matches, via the AWS CLI on both endpoints, marker tags never compared; local record store held 5 records, one per instance (#364 A2); replanned empty both with and without the local record store"
fi
CURRENT_STAGE=""

# day2_rename's stock oracle (live/GAUNTLET.md #6, tracked as issue #357):
# "Stock with the same moved block plans zero churn." Run against a COPY of
# the state cold_deploy (B1) just left, before choudoufu or live-import ever
# touches these objects - the real terraform.tfstate stays untouched here so
# B2's live-import below still sees the original, unmarked resource names.
# Using $ADOPTED's post-adoption state instead would confound the comparison:
# every resource would also show its ownership-marker tags being stripped
# back out (stock's tags map only ever names "Name"), which is real but has
# nothing to do with the rename this oracle exists to check.
CURRENT_STAGE=day2_rename
log "=== B1.5. day2_rename stock oracle: the same two-resource rename, through moved blocks, on cold_deploy's own state ==="
PLAIN_ORACLE="$WORK/plain-oracle"
cp -r "$PLAIN" "$PLAIN_ORACLE"
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
  resource_block_both_renamed
  cat <<'EOF'

moved {
  from = aws_security_group.main
  to   = aws_security_group.renamed
}

moved {
  from = aws_internet_gateway.main
  to   = aws_internet_gateway.renamed
}
EOF
} > "$PLAIN_ORACLE/main.tf"
ORACLE_PLAN_OUT="$(cd "$PLAIN_ORACLE" && terraform plan -input=false -no-color 2>&1)"; ORACLE_PLAN_RC=$?
[ "$ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -30; fail "the day2_rename stock oracle plan exited $ORACLE_PLAN_RC"; }
grep -qE '^  # .+ will be destroyed' <<< "$ORACLE_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock proposes a destroy for a rename carried entirely by moved blocks - the oracle itself is not zero-churn"; }
grep -qE '^  # .+ will be created' <<< "$ORACLE_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock proposes a create for a rename carried entirely by moved blocks - the oracle itself is not zero-churn"; }
grep -qE '^  # aws_security_group\.main has moved to aws_security_group\.renamed' <<< "$ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_PLAN_OUT"; fail "stock's plan does not report the security-group move"; }
grep -qE '^  # aws_internet_gateway\.main has moved to aws_internet_gateway\.renamed' <<< "$ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_PLAN_OUT"; fail "stock's plan does not report the internet-gateway move"; }
grep -qF 'Plan: 0 to add, 0 to change, 0 to destroy.' <<< "$ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -10; fail "stock's rename plan is not a true no-op"; }
log "  stock: zero churn, no attribute diff at all - both resources report only their move, on the state cold_deploy produced"
CURRENT_STAGE=migrate

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
grep -qF "5 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 0 skipped" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not stamp exactly 5 resources cleanly"; }
log "  5 stamped"
gauntlet_stage migrate pass "5 of 5 verified, 5 stamped, 0 skipped"
CURRENT_STAGE=test_plan

log "=== B4. and the adopted config plans empty ==="
ADOPT_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; ADOPT_PLAN_RC=$?
[ "$ADOPT_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ADOPT_PLAN_OUT" | tail -30; fail "the post-adoption plan exited $ADOPT_PLAN_RC"; }
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$ADOPT_PLAN_OUT" \
  || { grep -E '^  #' <<< "$ADOPT_PLAN_OUT"; fail "the post-adoption plan is not empty"; }
log "  No changes. The infra terraform created, unmarked, is now under live markers with an empty plan."
gauntlet_stage test_plan pass "post-adoption plan is empty; markers read back through the AWS CLI in part A"
CURRENT_STAGE=test_apply

log "=== B5. apply the empty plan: a genuine no-op ==="
BEFORE_N="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$BEFORE_N" = "5" ] || fail "expected 5 objects carrying tofu-estate=$ESTATE before the no-op apply (vpc, subnet, igw, sg, instance), got $BEFORE_N"
NOOP_APPLY_OUT="$(cd "$ADOPTED" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; NOOP_APPLY_RC=$?
[ "$NOOP_APPLY_RC" -eq 0 ] || { printf '%s\n' "$NOOP_APPLY_OUT" | tail -30; fail "the no-op apply exited $NOOP_APPLY_RC"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$NOOP_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$NOOP_APPLY_OUT"; fail "the no-op apply was not a genuine no-op"; }
AFTER_N="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$AFTER_N" = "$BEFORE_N" ] || fail "object count changed across a no-op apply: $BEFORE_N -> $AFTER_N"
[ ! -f "$ADOPTED/terraform.tfstate" ] || fail "the no-op apply left a state file behind"
log "  genuine no-op: $BEFORE_N objects before, $AFTER_N after, no state file either time"
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed); tofu-estate-tagged object count unchanged at $BEFORE_N"
CURRENT_STAGE=drift_reconverge

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
  gauntlet_stage drift_reconverge pass "one object tampered, exactly aws_instance.main proposed, apply changed 1 and the tag reads back as configured"
fi
CURRENT_STAGE=""

# ══════════════════════════════════════════════════════════════════════════
# PART D: RENAME (day2_rename, planned stage - live/GAUNTLET.md #6, issue #357)
# ══════════════════════════════════════════════════════════════════════════
#
# The adopted estate (Part B/C) is still marked and still converged, which is
# exactly the state a rename needs to start from. Two mechanisms, on two
# different resources so a gap in either is visible: a `moved` block renames
# the security group, and "choudoufu live-mv" renames the internet gateway
# with no moved block at all. The stock oracle for both already ran in B1.5,
# against the state cold_deploy left before choudoufu ever touched these
# objects - see the comment there for why reusing $ADOPTED's post-adoption
# state would confound the comparison instead.
#
# BREAK=1 exercises this stage's own Break control instead of the real
# checks: renaming the security group WITHOUT a moved block, which must make
# choudoufu propose destroying the old address and creating the new one - the
# opposite of every other assertion in this part.

CURRENT_STAGE=day2_rename
log "=== D0. capture the two live ids a rename must not disturb ==="
SG_ID="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-security-groups \
  --filters "Name=group-name,Values=ec2-reference-sg" \
  --query "SecurityGroups[0].GroupId" --output text)"
[ -n "$SG_ID" ] && [ "$SG_ID" != "None" ] || fail "no live security group found by its name before the rename"
IGW_ID="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-internet-gateways \
  --filters "Name=tag:Name,Values=ec2-reference-igw" \
  --query "InternetGateways[0].InternetGatewayId" --output text)"
[ -n "$IGW_ID" ] && [ "$IGW_ID" != "None" ] || fail "no live internet gateway found by its name before the rename"
log "  security group $SG_ID, internet gateway $IGW_ID"

if [ "${BREAK:-}" = "1" ]; then
  log "=== D1 (BREAK=1). rename aws_security_group.main -> .renamed WITHOUT a moved block ==="
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
    resource_block_sg_renamed
  } > "$ADOPTED/main.tf"
  BREAK_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; BREAK_PLAN_RC=$?
  [ "$BREAK_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -30; fail "the BREAK=1 rename-without-moved plan exited $BREAK_PLAN_RC"; }
  grep -qE '^  # aws_security_group\.main will be destroyed' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=1: renaming without a moved block did not propose destroying aws_security_group.main - this stage's check is not load-bearing"; }
  grep -qE '^  # aws_security_group\.renamed will be created' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=1: renaming without a moved block did not propose creating aws_security_group.renamed - this stage's check is not load-bearing"; }
  log "  BREAK=1: correctly proposes destroying aws_security_group.main and creating aws_security_group.renamed - the moved-block and live-mv checks below are skipped"
else
  log "=== D1. choudoufu, moved block: aws_security_group.main -> .renamed ==="
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
    resource_block_sg_renamed
    cat <<'EOF'

moved {
  from = aws_security_group.main
  to   = aws_security_group.renamed
}
EOF
  } > "$ADOPTED/main.tf"
  MOVED_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; MOVED_PLAN_RC=$?
  [ "$MOVED_PLAN_RC" -eq 0 ] || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -30; fail "the moved-block rename plan exited $MOVED_PLAN_RC"; }
  grep -qE '^  # aws_security_group\.renamed will be updated in-place' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block plan does not propose an in-place update to aws_security_group.renamed"; }
  grep -qE 'will be destroyed' <<< "$MOVED_PLAN_OUT" \
    && { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block rename proposes a destroy - not zero churn"; }
  grep -qE 'will be created' <<< "$MOVED_PLAN_OUT" \
    && { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block rename proposes a create - not zero churn"; }
  grep -qF 'Plan: 0 to add, 1 to change, 0 to destroy.' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -10; fail "the moved-block rename plan is not exactly one in-place change"; }
  grep -qE '~ +"tofu-address" = "aws_security_group\.main" -> "aws_security_group\.renamed"' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT"; fail "the moved-block plan does not show the tofu-address marker being rewritten from the old address to the new one"; }
  log "  choudoufu: zero churn, one in-place tags update - the marker rewrite the moved block completes"

  MOVED_APPLY_OUT="$(cd "$ADOPTED" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; MOVED_APPLY_RC=$?
  [ "$MOVED_APPLY_RC" -eq 0 ] || { printf '%s\n' "$MOVED_APPLY_OUT" | tail -30; fail "the moved-block rename apply exited $MOVED_APPLY_RC"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$MOVED_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$MOVED_APPLY_OUT"; fail "the moved-block rename apply was not exactly one in-place change"; }

  SG_ID_AFTER="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-security-groups \
    --group-ids "$SG_ID" --query "SecurityGroups[0].GroupId" --output text 2>/dev/null || true)"
  [ "$SG_ID_AFTER" = "$SG_ID" ] || fail "the security group's id changed across the rename ($SG_ID -> $SG_ID_AFTER) - it was destroyed and recreated, not renamed"
  SG_ADDR_TAG="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-tags \
    --filters "Name=resource-id,Values=$SG_ID" "Name=key,Values=tofu-address" \
    --query "Tags[0].Value" --output text)"
  [ "$SG_ADDR_TAG" = "aws_security_group.renamed" ] || fail "the security group carries tofu-address=$SG_ADDR_TAG after the rename, not aws_security_group.renamed"
  log "  $SG_ID unchanged, tofu-address now aws_security_group.renamed - read via the AWS CLI"

  log "=== D2. choudoufu, live-mv: aws_internet_gateway.main -> .renamed, no moved block at all ==="
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
    resource_block_both_renamed
    cat <<'EOF'

moved {
  from = aws_security_group.main
  to   = aws_security_group.renamed
}
EOF
  } > "$ADOPTED/main.tf"
  MV_OUT="$(cd "$ADOPTED" && "$TOFU" live-mv -estate="$ESTATE" aws_internet_gateway.main aws_internet_gateway.renamed 2>&1)"; MV_RC=$?
  [ "$MV_RC" -eq 0 ] || { printf '%s\n' "$MV_OUT" | tail -30; fail "choudoufu live-mv exited $MV_RC"; }
  grep -qF 'Rewrote the ownership marker on one live resource. This was a cloud write.' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report a real write"; }
  grep -qF '"aws_internet_gateway.main" -> "aws_internet_gateway.renamed"' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report rewriting the tofu-address marker from the old address to the new one"; }
  log "  live-mv: $(grep -F 'live ID' <<< "$MV_OUT")"

  IGW_ID_AFTER="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-internet-gateways \
    --internet-gateway-ids "$IGW_ID" --query "InternetGateways[0].InternetGatewayId" --output text 2>/dev/null || true)"
  [ "$IGW_ID_AFTER" = "$IGW_ID" ] || fail "the internet gateway's id changed across live-mv ($IGW_ID -> $IGW_ID_AFTER) - it was destroyed and recreated, not renamed"
  IGW_ADDR_TAG="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-tags \
    --filters "Name=resource-id,Values=$IGW_ID" "Name=key,Values=tofu-address" \
    --query "Tags[0].Value" --output text)"
  [ "$IGW_ADDR_TAG" = "aws_internet_gateway.renamed" ] || fail "the internet gateway carries tofu-address=$IGW_ADDR_TAG after live-mv, not aws_internet_gateway.renamed"
  log "  $IGW_ID unchanged, tofu-address now aws_internet_gateway.renamed - read via the AWS CLI"

  log "=== D3. one more plan: config and markers agree on both renames, nothing proposed ==="
  FINAL_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; FINAL_PLAN_RC=$?
  [ "$FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$FINAL_PLAN_OUT" | tail -30; fail "the post-rename plan exited $FINAL_PLAN_RC"; }
  grep -qF "No changes. Your infrastructure matches the configuration." <<< "$FINAL_PLAN_OUT" \
    || { grep -E '^  #' <<< "$FINAL_PLAN_OUT"; fail "the post-rename plan is not empty"; }
  log "  No changes. Both renames are complete and invisible to the next plan."

  gauntlet_stage day2_rename pass "moved block: aws_security_group renamed with zero churn (0 add, 1 change, 0 destroy), marker rewritten in place; live-mv: aws_internet_gateway renamed with zero churn, marker rewritten in place; stock oracle over the same two-resource rename on cold_deploy's own state also shows zero churn (0 add, 0 change, 0 destroy); both live ids unchanged, read via the AWS CLI"
fi
CURRENT_STAGE=""
gauntlet_end

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
log "  NO-OP APPLY applying that empty plan is a genuine no-op (0 added, 0"
log "              changed, 0 destroyed) and the tofu-estate-tagged object"
log "              count is unchanged, read via the AWS CLI."
log "  DRIFT       one live object tampered out of band -> choudoufu plan"
log "              proposes fixing that object and nothing else -> apply"
log "              reconverges it -> the live tag reads back as configured."
log ""
log "Known, non-fabricated gap this script does not paper over: a bare"
log "'choudoufu plan' against unmarked infra (skipping live-import) only"
log "auto-adopts the VPC, subnet and security group on its own - the"
log "instance and the internet gateway need the explicit live-import step."
log "That is why part B goes through live-import rather than a bare plan."
