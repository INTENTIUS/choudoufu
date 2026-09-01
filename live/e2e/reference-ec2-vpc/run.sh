#!/usr/bin/env bash
# (moved from the justfile's retired demo-reference-ec2-vpc recipe; run with: just demo-run reference-ec2-vpc)
# The reference project: VPC, subnet, internet gateway, security group, EC2
# instance - the plainest "getting started" AWS shape, checked both
# directions. GREENFIELD: write it with a live block from the start, apply,
# every object's markers read back through the AWS CLI directly (not
# choudoufu's own report), plan empty, plan empty again with the local
# record_store deleted entirely. ADOPTION: the identical shapes applied
# first with plain stock terraform (real state, zero markers, confirmed via
# the AWS CLI), then migrated with "choudoufu live-import -approve" and
# replanned empty. Not from a corpus - hand-written, no version pins beyond
# the ordinary #269 gap every other estate needs. Needs Docker and the AWS
# CLI; runs on two ports (4712, 4713) so it can run beside `just demo`.
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
#   BREAK        set to 1 to run three negative controls instead of the real
#                checks, proving each is load-bearing rather than a grep
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
#                This same BREAK also switches Part D (day2_rename) to its
#                own negative control - see that part's header.
#   BREAK_REMOVE set to 1 to run day2_remove's own negative control instead
#                of the real checks: keep the internet-gateway block in the
#                config and assert no destroy is proposed for it (the
#                Break text in tools/gauntlet/stages.go for day2_remove is
#                literally "keep the block; no destroy may be proposed").
#                Independent of BREAK and only reachable when BREAK is not
#                1, because Part E starts from Part D's real, completed
#                rename - see Part E's header.
#   BREAK_COUNT  set to 1 to run day2_count's own negative control instead
#                of the real checks: after the real scale-down plan (2 -> 1),
#                assert the WRONG instance (count_test[0] rather than
#                count_test[1]) was the one destroyed (the Break text in
#                tools/gauntlet/stages.go for day2_count is literally
#                "Expect a different instance to be destroyed; the
#                assertion must fail"). Independent of BREAK and
#                BREAK_REMOVE and only reachable when neither is 1, because
#                Part F starts from Part E's real, completed removal - see
#                Part F's header.
#   BREAK_CRASH  set to 1 to run day2_crash's own negative control instead of
#                the real checks: after a real create_before_destroy replace
#                is genuinely interrupted between the create committing and
#                the destroy dispatching, assert nothing is proposed on the
#                next plan (the Break text in tools/gauntlet/stages.go for
#                day2_crash is literally "Interrupt and then assert nothing
#                is proposed; the assertion must fail"). Independent of
#                BREAK, BREAK_REMOVE and BREAK_COUNT and only reachable when
#                none of them is 1, because Part H starts from Part F's real,
#                completed count cycle - see Part H's header.
#   BREAK_STRICT set to 1 to run Part G's (strict) own negative control
#                instead of the real check: turn the secrets toggle back to
#                "store" and assert its refusal is gone and no other
#                appeared (the Break text in tools/gauntlet/stages.go for
#                "strict" is literally "Turn a toggle off; its refusal must
#                disappear and no other may appear"). Independent of every
#                other BREAK* var - Part G carries its own scratch estate
#                and does not touch the adopted infra parts B-F left
#                behind - see Part G's own header.

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

# day2_crash (Part H below) needs a build with e2eTestingFeatures set - the
# same ldflags-gated feature TOFU_E2E_APPLY_RESOURCE_PANIC already sits
# behind (internal/command/apply_e2etesting.go) - so its own
# TOFU_E2E_APPLY_RESOURCE_INTERRUPT hook (apply_e2etesting_crash.go) is
# reachable at all. Built fresh from THIS worktree's own source regardless
# of whether $TOFU came from TOFU_BIN, since the point is to exercise this
# tree's own engine, and unconditionally (not only when day2_crash's own
# BREAK_CRASH branch runs) since both the real check and its Break control
# need it. Behaviourally identical to $TOFU for every other stage: the
# hook is a no-op unless TOFU_E2E_APPLY_RESOURCE_INTERRUPT names a matching
# address.
mkdir -p "$WORK/bin"
TOFU_CRASH="$WORK/bin/choudoufu-e2e"
( cd "$ROOT" && env -u PWD go build -ldflags="-X 'main.e2eTestingFeatures=yes'" -o "$TOFU_CRASH" ./cmd/choudoufu ) \
  || fail "go build -ldflags e2eTestingFeatures ./cmd/choudoufu failed"
log "  built $TOFU_CRASH (e2eTestingFeatures=yes, for day2_crash's deterministic interrupt)"

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

# resource_block_ami_replaced() is resource_block() with aws_instance.main's
# `ami` argument changed - the day2_replace stage's own target. `ami` is
# ForceNew on aws_instance (AWS has no in-place image swap for a running
# instance), and nothing else in this estate's five resources references
# the instance's own attributes, so this is a genuinely isolated,
# single-resource replace: no EIP, no volume attachment, no sibling to
# cascade into (unlike corpus-ec2-instance-complete's own module.ec2_
# complete, which has both). Both AMI strings are the estate's own kind of
# literal (cold_deploy already applies "ami-12345678" with no real AMI
# lookup - floci accepts any aws_instance.ami value without validating it
# exists, confirmed by that stage passing today), so "ami-87654321" needs
# no fixed-catalog id the way a real-AMI-lookup estate would.
resource_block_ami_replaced() {
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
  ami                    = "ami-87654321"
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

# resource_block_no_igw() is resource_block() (original "main" names,
# unrenamed) with the aws_internet_gateway.main block deleted outright -
# day2_rename's stock oracle removes a block on the SAME cold_deploy state
# copy every other stock oracle here uses, before any rename has ever
# happened, so this is the "main"-named counterpart to
# resource_block_igw_removed() below rather than that function itself. The
# internet gateway has no other resource depending on it in this estate (the
# subnet references only the VPC, the instance references only the subnet
# and the security group), so deleting its block needs no other edit.
resource_block_no_igw() {
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

# resource_block_igw_removed() is resource_block_both_renamed() (the shape
# Part D leaves live: security group AND internet gateway both renamed to
# .renamed) with the aws_internet_gateway.renamed block deleted outright -
# Part E's real day2_remove check plans this against the adopted estate.
resource_block_igw_removed() {
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

# count_test_block($1 = count, $2 = vpc_id HCL expression) is the
# day2_count stage's own resource: a security group nothing else in this
# estate names, added and removed entirely within Part F (and the B1.7
# stock oracle), so day2_count's own history never touches the five
# resources every other part depends on. $2 lets the same helper serve
# both Part F (inside the adopted estate, where aws_vpc.main already
# exists) and the B1.7 oracle (its own separate working directory and
# state, with its own small VPC - see oracle_vpc_block below). Unquoted
# heredoc so $1/$2 interpolate; ${count.index} is escaped so bash never
# tries to expand it as a parameter.
count_test_block() {
  local n="$1" vpc_ref="$2"
  cat <<COUNTEOF
resource "aws_security_group" "count_test" {
  count       = $n
  name        = "ec2-reference-count-test-\${count.index}"
  description = "day2_count evidence (issue #359)"
  vpc_id      = $vpc_ref

  tags = {
    Name = "ec2-reference-count-test-\${count.index}"
  }
}
COUNTEOF
}

# oracle_vpc_block() is the B1.7 stock oracle's own tiny VPC, standing in
# for aws_vpc.main so count_test_block's security groups have a vpc_id in
# a working directory that never declares the adopted estate's real VPC.
oracle_vpc_block() {
  cat <<'EOF'
resource "aws_vpc" "count_oracle" {
  cidr_block = "10.99.0.0/16"
  tags = {
    Name = "ec2-reference-count-oracle-vpc"
  }
}
EOF
}

# resource_block_crash($1 = ami) is resource_block_igw_removed() (the
# internet gateway removed by day2_remove, the security group renamed by
# day2_rename) with a lifecycle { create_before_destroy = true } block added
# to aws_instance.main - day2_crash's own replace target. A crash strictly
# "between the create and the destroy" is only reachable through the
# create-then-destroy ordering that lifecycle block requests; the default
# ordering destroys first, so there would be no window between the two at
# all. $1 lets the caller drive the ForceNew ami argument through successive
# crash attempts without a second heredoc.
resource_block_crash() {
  local ami="$1"
  cat <<EOF
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
  ami                    = "$ami"
  instance_type          = "t3.micro"
  subnet_id              = aws_subnet.main.id
  vpc_security_group_ids = [aws_security_group.renamed.id]

  tags = {
    Name = "ec2-reference-instance"
  }

  lifecycle {
    create_before_destroy = true
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

gauntlet_begin_stage greenfield
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
gauntlet_end_stage

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

gauntlet_begin_stage cold_deploy
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
gauntlet_begin_stage greenfield
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
gauntlet_end_stage

# day2_rename's stock oracle (live/GAUNTLET.md #6, tracked as issue #357):
# "Stock with the same moved block plans zero churn." Run against a COPY of
# the state cold_deploy (B1) just left, before choudoufu or live-import ever
# touches these objects - the real terraform.tfstate stays untouched here so
# B2's live-import below still sees the original, unmarked resource names.
# Using $ADOPTED's post-adoption state instead would confound the comparison:
# every resource would also show its ownership-marker tags being stripped
# back out (stock's tags map only ever names "Name"), which is real but has
# nothing to do with the rename this oracle exists to check.
gauntlet_begin_stage day2_rename
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

# day2_remove's stock oracle (live/GAUNTLET.md #7, issue #358 - gauntlet
# evidence unit, planned stage, does not count toward clear): "Stock with
# the same block removed plans the same destroys in a working order." Same
# principle as B1.5 above: a SEPARATE copy of cold_deploy's own state, "main"
# names throughout, so this destroy has nothing to do with the rename this
# script also exercises.
gauntlet_begin_stage day2_remove
log "=== B1.6. day2_remove stock oracle: delete the internet-gateway block on cold_deploy's own state ==="
PLAIN_ORACLE_REMOVE="$WORK/plain-oracle-remove"
cp -r "$PLAIN" "$PLAIN_ORACLE_REMOVE"
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
  resource_block_no_igw
} > "$PLAIN_ORACLE_REMOVE/main.tf"
REMOVE_ORACLE_PLAN_OUT="$(cd "$PLAIN_ORACLE_REMOVE" && terraform plan -input=false -no-color 2>&1)"; REMOVE_ORACLE_PLAN_RC=$?
[ "$REMOVE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -30; fail "the day2_remove stock oracle plan exited $REMOVE_ORACLE_PLAN_RC"; }
grep -qE '^  # aws_internet_gateway\.main will be destroyed' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock does not propose destroying aws_internet_gateway.main when its block is removed"; }
grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -10; fail "stock's remove plan proposes something other than exactly one destroy"; }
log "  stock: exactly one destroy (aws_internet_gateway.main), nothing else, on the state cold_deploy produced"

# day2_count's stock oracle (live/GAUNTLET.md #8, issue #359 - gauntlet
# evidence unit, planned stage, does not count toward clear): "Stock's plan
# for the same count change, normalised." Unlike B1.5/B1.6, there is no
# pre-existing count block to reuse, so the oracle applies a genuinely new
# one for real, with plain terraform, in $ENDPOINT - the greenfield
# account Part A already finished with, holding only choudoufu's own
# five-resource estate under a completely different name and never touched
# again after A4. aws_security_group.count_test collides with nothing
# there, and $ENDPOINT is left exactly as this leaves it (no further part
# of this script writes to it), so contaminating it with a real, non-choudoufu
# apply is safe. AWS_ENDPOINT_URL stays $ADOPT_ENDPOINT for the rest of the
# script; only this block's own terraform invocations are pointed at
# $ENDPOINT, via a per-command environment override.
gauntlet_begin_stage day2_count
log "=== B1.7. day2_count stock oracle: create a 2-instance count block, scale it to 1 and back, in the (idle) greenfield account ==="
PLAIN_ORACLE_COUNT="$WORK/plain-oracle-count"
mkdir -p "$PLAIN_ORACLE_COUNT"
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
  oracle_vpc_block
  echo
  count_test_block 2 "aws_vpc.count_oracle.id"
} > "$PLAIN_ORACLE_COUNT/main.tf"
( cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$ENDPOINT" terraform init -input=false -no-color >/dev/null 2>&1 ) \
  || fail "the day2_count oracle's terraform init failed"
ORACLE_COUNT_APPLY_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$ORACLE_COUNT_APPLY_OUT" | tail -30; fail "the day2_count oracle's baseline apply failed"; }
grep -qE 'Apply complete! Resources: 3 added' <<< "$ORACLE_COUNT_APPLY_OUT" \
  || { printf '%s\n' "$ORACLE_COUNT_APPLY_OUT" | tail -30; fail "stock did not create exactly 3 resources (the oracle's own VPC plus 2 count-test security groups) for the day2_count oracle"; }
ORACLE_SG0_ID="$(aws --endpoint-url "$ENDPOINT" --region "$REGION" ec2 describe-security-groups \
  --filters "Name=tag:Name,Values=ec2-reference-count-test-0" --query "SecurityGroups[0].GroupId" --output text)"
ORACLE_SG1_ID="$(aws --endpoint-url "$ENDPOINT" --region "$REGION" ec2 describe-security-groups \
  --filters "Name=tag:Name,Values=ec2-reference-count-test-1" --query "SecurityGroups[0].GroupId" --output text)"
[ -n "$ORACLE_SG0_ID" ] && [ "$ORACLE_SG0_ID" != "None" ] || fail "no oracle count_test[0] security group found by its Name tag"
[ -n "$ORACLE_SG1_ID" ] && [ "$ORACLE_SG1_ID" != "None" ] || fail "no oracle count_test[1] security group found by its Name tag"
log "  stock: 2 instances created, count_test[0]=$ORACLE_SG0_ID count_test[1]=$ORACLE_SG1_ID"

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
  oracle_vpc_block
  echo
  count_test_block 1 "aws_vpc.count_oracle.id"
} > "$PLAIN_ORACLE_COUNT/main.tf"
ORACLE_DOWN_PLAN_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$ENDPOINT" terraform plan -input=false -no-color 2>&1)"; ORACLE_DOWN_PLAN_RC=$?
[ "$ORACLE_DOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | tail -30; fail "the day2_count oracle's scale-down plan exited $ORACLE_DOWN_PLAN_RC"; }
grep -qE '^  # aws_security_group\.count_test\[1\] will be destroyed' <<< "$ORACLE_DOWN_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-down plan does not destroy count_test[1]"; }
grep -qE '^  # aws_security_group\.count_test\[0\] will be' <<< "$ORACLE_DOWN_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-down plan touches count_test[0], which should be untouched"; }
grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$ORACLE_DOWN_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | tail -10; fail "stock's scale-down plan proposes something other than exactly one destroy"; }
ORACLE_DOWN_APPLY_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$ORACLE_DOWN_APPLY_OUT" | tail -30; fail "the day2_count oracle's scale-down apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$ORACLE_DOWN_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$ORACLE_DOWN_APPLY_OUT"; fail "the day2_count oracle's scale-down apply was not exactly one destroy"; }
ORACLE_SG0_AFTER_DOWN="$(aws --endpoint-url "$ENDPOINT" --region "$REGION" ec2 describe-security-groups \
  --group-ids "$ORACLE_SG0_ID" --query "SecurityGroups[0].GroupId" --output text 2>/dev/null || true)"
[ "$ORACLE_SG0_AFTER_DOWN" = "$ORACLE_SG0_ID" ] || fail "stock's surviving count_test[0] changed id across the scale-down"
ORACLE_SG1_N_AFTER_DOWN="$(aws --endpoint-url "$ENDPOINT" --region "$REGION" ec2 describe-security-groups \
  --group-ids "$ORACLE_SG1_ID" --query "length(SecurityGroups)" --output text 2>/dev/null || echo 0)"
[ "$ORACLE_SG1_N_AFTER_DOWN" = "0" ] || fail "stock's count_test[1] ($ORACLE_SG1_ID) still exists after the scale-down destroy"
log "  stock: exactly one destroy (count_test[1]=$ORACLE_SG1_ID), count_test[0]=$ORACLE_SG0_ID unchanged"

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
  oracle_vpc_block
  echo
  count_test_block 2 "aws_vpc.count_oracle.id"
} > "$PLAIN_ORACLE_COUNT/main.tf"
ORACLE_UP_PLAN_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$ENDPOINT" terraform plan -input=false -no-color 2>&1)"; ORACLE_UP_PLAN_RC=$?
[ "$ORACLE_UP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | tail -30; fail "the day2_count oracle's scale-up plan exited $ORACLE_UP_PLAN_RC"; }
grep -qE '^  # aws_security_group\.count_test\[1\] will be created' <<< "$ORACLE_UP_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-up plan does not create count_test[1]"; }
grep -qE '^  # aws_security_group\.count_test\[0\] will be' <<< "$ORACLE_UP_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-up plan touches count_test[0], which should be untouched"; }
grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$ORACLE_UP_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | tail -10; fail "stock's scale-up plan proposes something other than exactly one create"; }
ORACLE_UP_APPLY_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$ORACLE_UP_APPLY_OUT" | tail -30; fail "the day2_count oracle's scale-up apply failed"; }
grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$ORACLE_UP_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$ORACLE_UP_APPLY_OUT"; fail "the day2_count oracle's scale-up apply was not exactly one create"; }
ORACLE_SG1_NEW_ID="$(aws --endpoint-url "$ENDPOINT" --region "$REGION" ec2 describe-security-groups \
  --filters "Name=tag:Name,Values=ec2-reference-count-test-1" --query "SecurityGroups[0].GroupId" --output text)"
[ -n "$ORACLE_SG1_NEW_ID" ] && [ "$ORACLE_SG1_NEW_ID" != "None" ] || fail "no oracle count_test[1] security group found after the scale-up"
[ "$ORACLE_SG1_NEW_ID" != "$ORACLE_SG1_ID" ] || fail "stock's recreated count_test[1] came back with the SAME id it had before being destroyed"
ORACLE_SG0_AFTER_UP="$(aws --endpoint-url "$ENDPOINT" --region "$REGION" ec2 describe-security-groups \
  --group-ids "$ORACLE_SG0_ID" --query "SecurityGroups[0].GroupId" --output text 2>/dev/null || true)"
[ "$ORACLE_SG0_AFTER_UP" = "$ORACLE_SG0_ID" ] || fail "stock's count_test[0] changed id across the scale-up"
log "  stock: exactly one create (count_test[1], new id $ORACLE_SG1_NEW_ID, was $ORACLE_SG1_ID), count_test[0]=$ORACLE_SG0_ID unchanged throughout"
gauntlet_end_stage

# day2_replace's stock oracle (live/GAUNTLET.md #9), computed here for the
# same reason B1.5/B1.6's own oracles sit before migrate (above): a
# SEPARATE copy of cold_deploy's own state, aws_instance.main's `ami`
# argument changed - ForceNew on aws_instance, forcing a replace at the
# SAME declared address. No cascade: this estate's aws_instance.main has
# no EIP, no volume attachment, nothing else referencing its own
# attributes - see resource_block_ami_replaced()'s own header comment.
#
# THE DOWNSTREAM AMI NOTE: this section's own real leg (below, after PART
# C) runs a genuine apply on $ADOPTED that leaves the live instance
# carrying ami-87654321, permanently, before PART D/PART E ever run. Every
# call to resource_block_sg_renamed/resource_block_both_renamed/resource_
# block_igw_removed AFTER that point is piped through `sed
# 's/ami-12345678/ami-87654321/'` so the config those stages generate
# matches what is actually live - found the hard way (day2_rename's own
# moved-block check failed with an extra, unexplained instance replace
# until this was added). B1.5/B1.6's own oracle calls to the SAME
# functions (above, before this section) are deliberately left
# unpatched: they run against $PLAIN_ORACLE/$PLAIN_ORACLE_REMOVE, copies
# of cold_deploy's state from BEFORE this section's replace ever touches
# anything, so ami-12345678 is still their own live truth.
gauntlet_begin_stage day2_replace
log "=== B1.8. day2_replace stock oracle: change aws_instance.main's ForceNew ami argument, on cold_deploy's own state ==="
PLAIN_ORACLE_REPLACE="$WORK/plain-oracle-replace"
cp -r "$PLAIN" "$PLAIN_ORACLE_REPLACE"
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
  resource_block_ami_replaced
} > "$PLAIN_ORACLE_REPLACE/main.tf"
REPLACE_ORACLE_PLAN_OUT="$(cd "$PLAIN_ORACLE_REPLACE" && terraform plan -input=false -no-color 2>&1)"; REPLACE_ORACLE_PLAN_RC=$?
[ "$REPLACE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_replace stock oracle plan exited $REPLACE_ORACLE_PLAN_RC"; }
grep -qE '^  # aws_instance\.main must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "stock does not propose replacing aws_instance.main when its ForceNew ami argument changes"; }
grep -qF 'Plan: 1 to add, 0 to change, 1 to destroy.' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -10; fail "the day2_replace stock oracle plan is not exactly one isolated replace"; }
log "  stock: exactly one instance replace at the same declared address, nothing else - 1 to add, 1 to destroy, on the state cold_deploy produced - plan only, not applied (see above)"
gauntlet_end_stage

gauntlet_begin_stage migrate

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
gauntlet_begin_stage test_plan

log "=== B4. and the adopted config plans empty ==="
ADOPT_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; ADOPT_PLAN_RC=$?
[ "$ADOPT_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ADOPT_PLAN_OUT" | tail -30; fail "the post-adoption plan exited $ADOPT_PLAN_RC"; }
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$ADOPT_PLAN_OUT" \
  || { grep -E '^  #' <<< "$ADOPT_PLAN_OUT"; fail "the post-adoption plan is not empty"; }
log "  No changes. The infra terraform created, unmarked, is now under live markers with an empty plan."
gauntlet_stage test_plan pass "post-adoption plan is empty; markers read back through the AWS CLI in part A"
gauntlet_begin_stage test_apply

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
gauntlet_begin_stage drift_reconverge

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
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# PART C2: REPLACE (day2_replace, active - live/GAUNTLET.md #9)
# ══════════════════════════════════════════════════════════════════════════
#
# Placed right after PART C and BEFORE PART D (day2_rename, below) on
# purpose, the same convention corpus-ec2-instance-complete's own PART F
# uses: aws_instance.main is never renamed by PART D (that stage's own
# two targets are aws_security_group.main and aws_internet_gateway.main),
# so this section has no dependency on PART D's outcome. Its `ami`
# argument changes from "ami-12345678" to "ami-87654321" - ForceNew on
# aws_instance (AWS has no in-place image swap for a running instance) -
# forcing a replace at the SAME declared address. No cascade: unlike
# corpus-ec2-instance-complete's own module.ec2_complete (which has both
# an EIP and a volume attachment referencing the instance), this
# hand-written reference estate's aws_instance.main has neither, so this
# is a genuinely isolated, single-resource replace.
#
# THE create_before_destroy SCOPE NOTE (see corpus-sqs-basic's own PART F
# for the full reasoning, reproduced only in summary here): a lifecycle
# block on this bare resource is technically legal (unlike a module call),
# but adding one here would be a THIRD estate-wide reduction convention
# with no precedent in this hand-written reference script, so this
# evidence pass exercises the default destroy-then-create ordering
# instead, matching every other corpus-* day2_replace section in this
# same unit.
#
# NO BREAK=replace LEG: aws_instance is ServerAssigned (EC2 assigns the
# instance id; none of its own arguments are its import identity), so the
# manufactured-coexistence check would hit the SAME fungible-slot
# regression corpus-security-group-complete's own day2_replace section
# found and documented in this same unit (a valid record short-circuits
# the duplicate-slot claimant matcher before it ever runs) - not
# re-measured here.
gauntlet_begin_stage day2_replace
record_key() { printf '%s' "$1" | base64 | tr '+/' '-_' | tr -d '=\n'; }
record_import_id() { jq -r '.identity.import_id' "$1"; }
F_ADDR="aws_instance.main"
F_RECORD="$ADOPTED/.tofu-records/tofu-records/$ESTATE/aws_instance/$(record_key "$F_ADDR")"

log "=== F0. capture the live instance and its record ahead of the forced replace ==="
[ -f "$F_RECORD" ] || fail "no local record file found for $F_ADDR ahead of day2_replace"
F_OLD_IMPORT_ID="$(record_import_id "$F_RECORD")"
[ "$F_OLD_IMPORT_ID" = "$PLAIN_INSTANCE_ID" ] || fail "the record for $F_ADDR names $F_OLD_IMPORT_ID ahead of day2_replace, not $PLAIN_INSTANCE_ID"
F_OLD_ADDR_TAG="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-tags --filters "Name=resource-id,Values=$PLAIN_INSTANCE_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
[ "$F_OLD_ADDR_TAG" = "aws_instance.main" ] || fail "$PLAIN_INSTANCE_ID does not carry tofu-address=aws_instance.main ahead of day2_replace"
log "  $PLAIN_INSTANCE_ID, record import_id=$F_OLD_IMPORT_ID, tofu-address=$F_OLD_ADDR_TAG"

log "=== F1. choudoufu: change the ForceNew ami argument, forcing a replace at the same declared address ==="
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
  resource_block_ami_replaced
} > "$ADOPTED/main.tf"

F_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; F_PLAN_RC=$?
[ "$F_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_PLAN_OUT" | tail -40; fail "the day2_replace plan exited $F_PLAN_RC"; }
grep -qE '^  # aws_instance\.main must be replaced' <<< "$F_PLAN_OUT" \
  || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not propose replacing aws_instance.main when its ForceNew ami argument changes"; }
grep -qE '~ +ami +=.+forces replacement' <<< "$F_PLAN_OUT" \
  || { printf '%s\n' "$F_PLAN_OUT"; fail "the plan does not mark ami as forcing replacement"; }
grep -qF 'Plan: 1 to add, 0 to change, 1 to destroy.' <<< "$F_PLAN_OUT" \
  || { printf '%s\n' "$F_PLAN_OUT" | tail -10; fail "the day2_replace plan is not exactly one isolated replace, matching B1.8's own plan shape"; }
log "  choudoufu: exactly one instance replace at the same declared address, nothing else - matches B1.8's own plan shape"

F_APPLY_OUT="$(cd "$ADOPTED" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; F_APPLY_RC=$?
[ "$F_APPLY_RC" -eq 0 ] || { printf '%s\n' "$F_APPLY_OUT" | tail -40; fail "the day2_replace apply exited $F_APPLY_RC"; }
grep -qE 'Resources: 1 added, 0 changed, 1 destroyed' <<< "$F_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$F_APPLY_OUT"; fail "the day2_replace apply did not match the planned 1 added, 1 destroyed"; }

F_OLD_STATE="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-instances --instance-ids "$PLAIN_INSTANCE_ID" --query "Reservations[0].Instances[0].State.Name" --output text 2>&1)"
[ "$F_OLD_STATE" = "terminated" ] || fail "$PLAIN_INSTANCE_ID is not terminated after the replace (state=$F_OLD_STATE) - the old object was orphaned, not destroyed"
log "  $PLAIN_INSTANCE_ID terminated - confirmed via the AWS CLI, not through choudoufu's own report"

F_NEW_ID="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-instances \
  --filters "Name=tag:tofu-address,Values=aws_instance.main" "Name=instance-state-name,Values=running,pending" \
  --query "Reservations[0].Instances[0].InstanceId" --output text)"
[ -n "$F_NEW_ID" ] && [ "$F_NEW_ID" != "None" ] && [ "$F_NEW_ID" != "$PLAIN_INSTANCE_ID" ] \
  || fail "could not find a new, different, running instance carrying aws_instance.main's tofu-address after the replace (got '$F_NEW_ID')"
F_NEW_ADDR_TAG="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-tags --filters "Name=resource-id,Values=$F_NEW_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
[ "$F_NEW_ADDR_TAG" = "aws_instance.main" ] \
  || fail "$F_NEW_ID carries tofu-address=$F_NEW_ADDR_TAG after the replace, not aws_instance.main - the marker did not move onto the new object"
log "  $F_NEW_ID (the new object) carries tofu-address=$F_NEW_ADDR_TAG - the marker moved onto the new object, read via the AWS CLI"

# THE RECORD STORE, asserted by value (HANDOFF's safety rule; the
# #398-guard shape: a stale record still naming the destroyed instance
# would be exactly the wrong-marker failure that outranks a missing one).
F_NEW_IMPORT_ID="$(record_import_id "$F_RECORD")"
[ "$F_NEW_IMPORT_ID" = "$F_NEW_ID" ] \
  || fail "the record for $F_ADDR names $F_NEW_IMPORT_ID after the replace, not the new object $F_NEW_ID - a stale record still claiming the destroyed instance, the #398-guard shape"
[ "$F_NEW_IMPORT_ID" != "$F_OLD_IMPORT_ID" ] \
  || fail "sanity: the record's import_id at $F_ADDR did not change at all across the replace"
log "  record store: import_id $F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID at the same key ($F_ADDR) - read directly off the local record store file, not through choudoufu's own report"

log "=== F2. one more plan: config and reality agree, no marker collision ==="
F_FINAL_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; F_FINAL_PLAN_RC=$?
[ "$F_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_FINAL_PLAN_OUT" | tail -40; fail "the post-replace plan exited $F_FINAL_PLAN_RC"; }
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$F_FINAL_PLAN_OUT" \
  || { grep -E '^  #' <<< "$F_FINAL_PLAN_OUT"; fail "the post-replace plan proposes a resource change"; }
log "  No changes. The replace is complete and invisible to the next plan - no marker collision."

PLAIN_INSTANCE_ID="$F_NEW_ID"
gauntlet_stage day2_replace pass "choudoufu: changing aws_instance.main's ForceNew ami argument proposed exactly one isolated instance replace at the same declared address (1 to add, 1 to destroy, nothing else), matching B1.8's own plan shape; applied cleanly; the old instance ($F_OLD_IMPORT_ID) is confirmed terminated and the new instance ($F_NEW_ID) carries the marker, both via the AWS CLI; the local record store's record at the same address now names the new instance, not the terminated one; the next plan proposes no resource action. No BREAK=replace leg - see this section's own header comment (reusing corpus-security-group-complete's own finding from this same unit rather than re-measuring it here)."
gauntlet_end_stage

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

gauntlet_begin_stage day2_rename
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
    resource_block_sg_renamed | sed 's/ami-12345678/ami-87654321/' # post-day2_replace: the live instance's real ami is now ami-87654321
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
    resource_block_sg_renamed | sed 's/ami-12345678/ami-87654321/' # post-day2_replace: the live instance's real ami is now ami-87654321
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
    resource_block_both_renamed | sed 's/ami-12345678/ami-87654321/' # post-day2_replace: the live instance's real ami is now ami-87654321
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

  # ════════════════════════════════════════════════════════════════════════
  # PART E: REMOVE A BLOCK (day2_remove, planned stage - live/GAUNTLET.md #7,
  # issue #358 - a gauntlet evidence unit; the runner records this verdict
  # but a planned stage does not count toward "clear" until its status is
  # flipped to active in tools/gauntlet/stages.go, a maintainer decision)
  # ════════════════════════════════════════════════════════════════════════
  #
  # Starts from Part D's real, completed state: both the security group and
  # the internet gateway carry their .renamed markers, and the config plans
  # empty (D3). The internet gateway is the object removed here - it has no
  # other resource depending on it in this estate (the subnet references
  # only the VPC, the instance references only the subnet and the security
  # group), so deleting its block needs no other config edit.
  #
  # Its removal is also unambiguous in the one way issue #357's own comment
  # names as day2_remove's territory: internal/live/discovery/discovery.go's
  # classifyOrphans withholds a destroy whenever a declared instance of the
  # SAME block (type and name, module and instance key stripped) is still
  # unclaimed elsewhere in the estate - the rename-vs-delete ambiguity a
  # rename-without-a-moved-block produces, because the new address IS an
  # unclaimed declared instance of that block. A genuine remove, deleting
  # the block outright with no replacement declared anywhere, produces no
  # such unclaimed instance: no other aws_internet_gateway block exists in
  # this config, so nothing is ever added to classifyOrphans's "pending"
  # set for it, and the destroy is never a candidate for withholding in the
  # first place. If that reasoning is wrong, the guard right below the plan
  # turns it into an honest, named wall instead of a silently skipped check.
  #
  # BREAK_REMOVE=1 exercises this stage's own Break control instead: keep
  # the block, and assert the plan proposes no destroy for it at all - the
  # Break text in tools/gauntlet/stages.go, verbatim.

  gauntlet_begin_stage day2_remove
  log "=== E0. capture the live internet-gateway id one more time ==="
  IGW_ID_BEFORE_REMOVE="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-internet-gateways \
    --internet-gateway-ids "$IGW_ID" --query "InternetGateways[0].InternetGatewayId" --output text 2>/dev/null || true)"
  [ "$IGW_ID_BEFORE_REMOVE" = "$IGW_ID" ] || fail "the internet gateway is not live under $IGW_ID before day2_remove even starts"

  if [ "${BREAK_REMOVE:-}" = "1" ]; then
    log "=== E1 (BREAK_REMOVE=1). keep the internet-gateway block; no destroy may be proposed ==="
    BREAK_REMOVE_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; BREAK_REMOVE_PLAN_RC=$?
    [ "$BREAK_REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_REMOVE_PLAN_OUT" | tail -30; fail "the BREAK_REMOVE=1 kept-block plan exited $BREAK_REMOVE_PLAN_RC"; }
    grep -qE '^  # aws_internet_gateway\.renamed will be destroyed' <<< "$BREAK_REMOVE_PLAN_OUT" \
      && { printf '%s\n' "$BREAK_REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK_REMOVE=1: a destroy was proposed for aws_internet_gateway.renamed even though its block is still in the config - this stage's check is not load-bearing"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$BREAK_REMOVE_PLAN_OUT" \
      || { grep -E '^  #' <<< "$BREAK_REMOVE_PLAN_OUT"; fail "BREAK_REMOVE=1: the kept-block plan is not empty"; }
    log "  BREAK_REMOVE=1: correctly proposes nothing - the block is still declared"
  else
    log "=== E1. choudoufu: delete the aws_internet_gateway.renamed block ==="
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
      resource_block_igw_removed | sed 's/ami-12345678/ami-87654321/' # post-day2_replace: the live instance's real ami is now ami-87654321
    } > "$ADOPTED/main.tf"
    REMOVE_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; REMOVE_PLAN_RC=$?
    [ "$REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -30; fail "the day2_remove plan exited $REMOVE_PLAN_RC"; }
    if grep -q 'is unclaimed, so this may be the same resource under a new instance key' <<< "$REMOVE_PLAN_OUT"; then
      printf '%s\n' "$REMOVE_PLAN_OUT" | tail -30
      fail "choudoufu withheld the destroy of aws_internet_gateway.renamed as a possible rename (discovery.go's classifyOrphans) even though no other aws_internet_gateway block exists anywhere in this config - this is the honest wall issue #358 names, not a pass"
    fi
    grep -qE '^  # aws_internet_gateway\.renamed will be destroyed' <<< "$REMOVE_PLAN_OUT" \
      || { printf '%s\n' "$REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu does not propose destroying aws_internet_gateway.renamed when its block is deleted"; }
    grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$REMOVE_PLAN_OUT" \
      || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -10; fail "choudoufu's remove plan proposes something other than exactly one destroy"; }
    log "  choudoufu: exactly one destroy (aws_internet_gateway.renamed), nothing else"

    REMOVE_APPLY_OUT="$(cd "$ADOPTED" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; REMOVE_APPLY_RC=$?
    [ "$REMOVE_APPLY_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_APPLY_OUT" | tail -30; fail "the day2_remove apply exited $REMOVE_APPLY_RC"; }
    grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$REMOVE_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$REMOVE_APPLY_OUT"; fail "the day2_remove apply was not exactly one destroy"; }

    # A destroyed internet gateway is confirmed by COUNT, not by exit code:
    # floci (checked directly against a real, no-tofu-involved
    # create/attach/detach/delete sequence while building this check) answers
    # describe-internet-gateways for an already-deleted id with a plain
    # HTTP 200 and an EMPTY list, not the InvalidInternetGatewayID.NotFound
    # error real AWS documents for the same request - so treating "the AWS
    # CLI call succeeded" as "still exists" is wrong regardless of which of
    # the two answers the emulator gives, and length(...) is right either
    # way (0 on both an empty list and, were the emulator ever to start
    # erroring instead, this query would fail loudly rather than lie).
    IGW_COUNT="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-internet-gateways \
      --internet-gateway-ids "$IGW_ID" --query "length(InternetGateways)" --output text 2>/dev/null || echo 0)"
    [ "$IGW_COUNT" = "0" ] || fail "the internet gateway $IGW_ID still exists in the live account after the destroy ($IGW_COUNT found) - it was orphaned, not destroyed"
    log "  $IGW_ID no longer exists (0 found) - confirmed via the AWS CLI, not through choudoufu's own report"

    log "=== E2. one more plan: config and reality agree, nothing left to propose ==="
    E_FINAL_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; E_FINAL_PLAN_RC=$?
    [ "$E_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$E_FINAL_PLAN_OUT" | tail -30; fail "the post-remove plan exited $E_FINAL_PLAN_RC"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$E_FINAL_PLAN_OUT" \
      || { grep -E '^  #' <<< "$E_FINAL_PLAN_OUT"; fail "the post-remove plan is not empty"; }
    log "  No changes. The removal is complete and invisible to the next plan."

    gauntlet_stage day2_remove pass "choudoufu: deleting aws_internet_gateway.renamed's block proposed exactly one destroy (0 add, 0 change, 1 destroy), applied cleanly (0 added, 0 changed, 1 destroyed), the object is genuinely gone from the live account (describe-internet-gateways on the old id no longer returns it, read via the AWS CLI, not choudoufu's own report), and the next plan is empty; stock oracle on cold_deploy's own state (B1.6) also proposes exactly one destroy for the same object; classifyOrphans did not withhold the destroy because no other aws_internet_gateway block is declared anywhere in this config"

    # ══════════════════════════════════════════════════════════════════════
    # PART F: CHANGE COUNT (day2_count, planned stage - live/GAUNTLET.md #8,
    # issue #359 - a gauntlet evidence unit; the runner records this verdict
    # but a planned stage does not count toward "clear" until its status is
    # flipped to active in tools/gauntlet/stages.go, a maintainer decision)
    # ══════════════════════════════════════════════════════════════════════
    #
    # Starts from Part E's real, completed state: the adopted estate plans
    # empty with the internet gateway gone. A NEW count block
    # (aws_security_group.count_test, count_test_block() above) is added
    # here rather than reusing anything from Parts A-E, so day2_count's own
    # history is self-contained: F0 creates the baseline two instances
    # through choudoufu directly (the same "day 2, add a declaration"
    # operation every other part here already proves works), then F1
    # scales down to one and F2 scales back up to two, exercising
    # internal/live/discovery/count.go's slot binding both directions:
    # which instance is destroyed on the way down, and that the surviving
    # instance's identity (its live GroupId and its tofu-address marker) is
    # untouched by either move. B1.7 above is the stock oracle for the same
    # shape, applied for real against a separate account since - unlike
    # B1.5/B1.6 - there is no pre-existing count block in cold_deploy's own
    # state to reuse.
    #
    # BREAK_COUNT=1 exercises this stage's own Break control instead of the
    # real checks: after the real scale-down plan, assert the WRONG
    # instance (count_test[0] rather than count_test[1]) was the one
    # destroyed - the Break text in tools/gauntlet/stages.go for
    # day2_count, verbatim: "Expect a different instance to be destroyed;
    # the assertion must fail."

    gauntlet_begin_stage day2_count
    log "=== F0. choudoufu: add aws_security_group.count_test, count = 2 ==="
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
      resource_block_igw_removed | sed 's/ami-12345678/ami-87654321/' # post-day2_replace: the live instance's real ami is now ami-87654321
      echo
      count_test_block 2 "aws_vpc.main.id"
    } > "$ADOPTED/main.tf"
    COUNT_ADD_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; COUNT_ADD_PLAN_RC=$?
    [ "$COUNT_ADD_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_ADD_PLAN_OUT" | tail -30; fail "the count-block-add plan exited $COUNT_ADD_PLAN_RC"; }
    grep -qF 'Plan: 2 to add, 0 to change, 0 to destroy.' <<< "$COUNT_ADD_PLAN_OUT" \
      || { printf '%s\n' "$COUNT_ADD_PLAN_OUT" | tail -10; fail "adding the count block did not plan exactly 2 creates"; }
    COUNT_ADD_APPLY_OUT="$(cd "$ADOPTED" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_ADD_APPLY_RC=$?
    [ "$COUNT_ADD_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_ADD_APPLY_OUT" | tail -30; fail "the count-block-add apply exited $COUNT_ADD_APPLY_RC"; }
    grep -qE 'Resources: 2 added, 0 changed, 0 destroyed' <<< "$COUNT_ADD_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$COUNT_ADD_APPLY_OUT"; fail "the count-block-add apply did not create exactly 2 resources"; }

    SG0_ID="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-security-groups \
      --filters "Name=tag:Name,Values=ec2-reference-count-test-0" --query "SecurityGroups[0].GroupId" --output text)"
    SG1_ID="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-security-groups \
      --filters "Name=tag:Name,Values=ec2-reference-count-test-1" --query "SecurityGroups[0].GroupId" --output text)"
    [ -n "$SG0_ID" ] && [ "$SG0_ID" != "None" ] || fail "no live count_test[0] security group found by its Name tag"
    [ -n "$SG1_ID" ] && [ "$SG1_ID" != "None" ] || fail "no live count_test[1] security group found by its Name tag"
    SG0_ADDR_TAG="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-tags \
      --filters "Name=resource-id,Values=$SG0_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
    SG1_ADDR_TAG="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-tags \
      --filters "Name=resource-id,Values=$SG1_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
    [ "$SG0_ADDR_TAG" = 'aws_security_group.count_test:0' ] || fail "count_test[0]'s live tofu-address tag is $SG0_ADDR_TAG, not aws_security_group.count_test:0 (live/MARKERS.md: a count instance's tag value is colon-escaped, e.g. aws_eip.this[2] -> aws_eip.this:2)"
    [ "$SG1_ADDR_TAG" = 'aws_security_group.count_test:1' ] || fail "count_test[1]'s live tofu-address tag is $SG1_ADDR_TAG, not aws_security_group.count_test:1"
    log "  2 instances created: index 0 = $SG0_ID (tofu-address=$SG0_ADDR_TAG), index 1 = $SG1_ID (tofu-address=$SG1_ADDR_TAG) - read via the AWS CLI"

    COUNT_NOOP_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; COUNT_NOOP_PLAN_RC=$?
    [ "$COUNT_NOOP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_NOOP_PLAN_OUT" | tail -30; fail "the post-add plan exited $COUNT_NOOP_PLAN_RC"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$COUNT_NOOP_PLAN_OUT" \
      || { grep -E '^  #' <<< "$COUNT_NOOP_PLAN_OUT"; fail "the plan right after adding the count block is not empty - the new instances did not bind their own markers cleanly"; }
    log "  No changes - both new instances plan empty immediately after creation"

    log "=== F1. scale count down: 2 -> 1 ==="
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
      resource_block_igw_removed | sed 's/ami-12345678/ami-87654321/' # post-day2_replace: the live instance's real ami is now ami-87654321
      echo
      count_test_block 1 "aws_vpc.main.id"
    } > "$ADOPTED/main.tf"
    COUNT_DOWN_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; COUNT_DOWN_PLAN_RC=$?
    [ "$COUNT_DOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | tail -30; fail "the scale-down plan exited $COUNT_DOWN_PLAN_RC"; }

    if [ "${BREAK_COUNT:-}" = "1" ]; then
      log "  BREAK_COUNT=1: asserting the WRONG instance (count_test[0]) was destroyed instead of count_test[1]"
      if grep -qE '^  # aws_security_group\.count_test\[0\] will be destroyed' <<< "$COUNT_DOWN_PLAN_OUT"; then
        fail "BREAK_COUNT=1: the plan actually destroys count_test[0] - this assertion is not load-bearing"
      fi
      log "  BREAK_COUNT=1: correctly does NOT destroy count_test[0] - the wrong-instance assertion above fails to hold, as it must"
    else
      grep -qE '^  # aws_security_group\.count_test\[1\] will be destroyed' <<< "$COUNT_DOWN_PLAN_OUT" \
        || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-down plan does not destroy count_test[1]"; }
      grep -qE '^  # aws_security_group\.count_test\[0\] will be' <<< "$COUNT_DOWN_PLAN_OUT" \
        && { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-down plan touches count_test[0], which should be untouched"; }
      grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$COUNT_DOWN_PLAN_OUT" \
        || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | tail -10; fail "choudoufu's scale-down plan proposes something other than exactly one destroy"; }
      log "  choudoufu: exactly one destroy (count_test[1]), count_test[0] untouched"

      COUNT_DOWN_APPLY_OUT="$(cd "$ADOPTED" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_DOWN_APPLY_RC=$?
      [ "$COUNT_DOWN_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_DOWN_APPLY_OUT" | tail -30; fail "the scale-down apply exited $COUNT_DOWN_APPLY_RC"; }
      grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$COUNT_DOWN_APPLY_OUT" \
        || { grep -E 'Apply complete' <<< "$COUNT_DOWN_APPLY_OUT"; fail "the scale-down apply was not exactly one destroy"; }

      SG0_AFTER_DOWN="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-security-groups \
        --group-ids "$SG0_ID" --query "SecurityGroups[0].GroupId" --output text 2>/dev/null || true)"
      [ "$SG0_AFTER_DOWN" = "$SG0_ID" ] || fail "count_test[0]'s live id changed across the scale-down ($SG0_ID -> $SG0_AFTER_DOWN) - it was destroyed and recreated, not left alone"
      SG1_COUNT_AFTER_DOWN="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-security-groups \
        --group-ids "$SG1_ID" --query "length(SecurityGroups)" --output text 2>/dev/null || echo 0)"
      [ "$SG1_COUNT_AFTER_DOWN" = "0" ] || fail "count_test[1] ($SG1_ID) still exists in the live account after the scale-down destroy"
      SG0_ADDR_AFTER_DOWN="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-tags \
        --filters "Name=resource-id,Values=$SG0_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
      [ "$SG0_ADDR_AFTER_DOWN" = 'aws_security_group.count_test:0' ] || fail "count_test[0]'s tofu-address tag changed across the scale-down: $SG0_ADDR_AFTER_DOWN"
      log "  $SG1_ID (count_test[1]) no longer exists (0 found); $SG0_ID (count_test[0]) unchanged id and marker - all read via the AWS CLI"

      log "=== F2. scale count back up: 1 -> 2 ==="
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
        resource_block_igw_removed | sed 's/ami-12345678/ami-87654321/' # post-day2_replace: the live instance's real ami is now ami-87654321
        echo
        count_test_block 2 "aws_vpc.main.id"
      } > "$ADOPTED/main.tf"
      COUNT_UP_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; COUNT_UP_PLAN_RC=$?
      [ "$COUNT_UP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | tail -30; fail "the scale-up plan exited $COUNT_UP_PLAN_RC"; }
      grep -qE '^  # aws_security_group\.count_test\[1\] will be created' <<< "$COUNT_UP_PLAN_OUT" \
        || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-up plan does not create count_test[1]"; }
      grep -qE '^  # aws_security_group\.count_test\[0\] will be' <<< "$COUNT_UP_PLAN_OUT" \
        && { printf '%s\n' "$COUNT_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-up plan touches count_test[0], which should be untouched"; }
      grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$COUNT_UP_PLAN_OUT" \
        || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | tail -10; fail "choudoufu's scale-up plan proposes something other than exactly one create"; }
      log "  choudoufu: exactly one create (count_test[1]), count_test[0] untouched"

      COUNT_UP_APPLY_OUT="$(cd "$ADOPTED" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_UP_APPLY_RC=$?
      [ "$COUNT_UP_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_UP_APPLY_OUT" | tail -30; fail "the scale-up apply exited $COUNT_UP_APPLY_RC"; }
      grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$COUNT_UP_APPLY_OUT" \
        || { grep -E 'Apply complete' <<< "$COUNT_UP_APPLY_OUT"; fail "the scale-up apply was not exactly one create"; }

      SG1_NEW_ID="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-security-groups \
        --filters "Name=tag:Name,Values=ec2-reference-count-test-1" --query "SecurityGroups[0].GroupId" --output text)"
      [ -n "$SG1_NEW_ID" ] && [ "$SG1_NEW_ID" != "None" ] || fail "no live count_test[1] security group found by its Name tag after the scale-up"
      [ "$SG1_NEW_ID" != "$SG1_ID" ] || fail "count_test[1] came back with the SAME id ($SG1_ID) it had before being destroyed - the destroy in F1 was not real"
      SG1_NEW_ADDR_TAG="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-tags \
        --filters "Name=resource-id,Values=$SG1_NEW_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
      [ "$SG1_NEW_ADDR_TAG" = 'aws_security_group.count_test:1' ] || fail "the recreated count_test[1] ($SG1_NEW_ID) carries tofu-address=$SG1_NEW_ADDR_TAG, not aws_security_group.count_test:1"
      SG0_AFTER_UP="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-security-groups \
        --group-ids "$SG0_ID" --query "SecurityGroups[0].GroupId" --output text 2>/dev/null || true)"
      [ "$SG0_AFTER_UP" = "$SG0_ID" ] || fail "count_test[0]'s live id changed across the scale-up ($SG0_ID -> $SG0_AFTER_UP)"
      log "  count_test[1] recreated under a new id ($SG1_NEW_ID, was $SG1_ID), tofu-address=$SG1_NEW_ADDR_TAG; count_test[0] ($SG0_ID) untouched throughout the down-then-up cycle - all read via the AWS CLI"

      log "=== F3. one more plan: config and reality agree, nothing left to propose ==="
      COUNT_FINAL_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; COUNT_FINAL_PLAN_RC=$?
      [ "$COUNT_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_FINAL_PLAN_OUT" | tail -30; fail "the post-scale-up plan exited $COUNT_FINAL_PLAN_RC"; }
      grep -qF "No changes. Your infrastructure matches the configuration." <<< "$COUNT_FINAL_PLAN_OUT" \
        || { grep -E '^  #' <<< "$COUNT_FINAL_PLAN_OUT"; fail "the post-scale-up plan is not empty"; }
      log "  No changes. The scale-down-then-up cycle is complete and invisible to the next plan."

      gauntlet_stage day2_count pass "choudoufu: scaling aws_security_group.count_test from 2 to 1 destroyed exactly count_test[1] (0 add, 0 change, 1 destroy), leaving count_test[0]'s live id and tofu-address marker unchanged; scaling back from 1 to 2 created exactly count_test[1] under a NEW live id (0 add, 0 change -> 1 add, 0 change, 0 destroy) while count_test[0] stayed untouched throughout; the next plan is empty; the B1.7 stock oracle on the same 2-instance count block, applied fresh in the idle greenfield account, shows the identical shape: destroy the higher index only, create the higher index back under a new id, the lower index's id unchanged both times"

      # ══════════════════════════════════════════════════════════════════
      # PART H: CRASH BETWEEN CREATE AND DESTROY (day2_crash, planned stage
      # - live/GAUNTLET.md #10, issue #361; the interrupt mechanism below
      # replaces #483's own retry-lottery one - issue #490)
      # ══════════════════════════════════════════════════════════════════
      #
      # Starts from Part F's real, completed state: the adopted estate plans
      # empty with count_test back at 2. aws_instance.main gets a
      # lifecycle { create_before_destroy = true } block (a no-op diff on
      # its own, confirmed at H0) and its ami changes again, forcing a real
      # replace under create-then-destroy ordering - the only ordering that
      # has a window "between the create and the destroy" at all.
      #
      # THE INTERRUPT IS STILL A REAL SIGTERM against a real running
      # choudoufu process, but it is no longer detected from OUTSIDE that
      # process. #483's own version ran `apply -parallelism=1` in the
      # background, tailed its streamed output for the literal hook line
      # "aws_instance.main: Creation complete", and sent SIGTERM the
      # instant that line appeared - racing an external tail/grep/kill
      # chain (file I/O, a 50ms poll loop, a process fork+exec) against the
      # internal gap between the graph walker finishing that node and
      # considering the next one. That race could not be won reliably -
      # issue #490 is the retry-lottery it produced ("landed on attempt N
      # of 20") - and no amount of external tuning closes it, because the
      # external detect-and-signal chain is both slower and far more
      # variable than what it is racing.
      #
      # So the interrupt is now delivered by the ENGINE ITSELF.
      # internal/command/apply_e2etesting_crash.go adds a PostApply hook,
      # reachable only in an e2eTestingFeatures build ($TOFU_CRASH above),
      # that self-delivers SIGTERM - the same signal cmd/choudoufu's own
      # makeShutdownCh forwards identically to an ordinary Ctrl-C - the
      # instant TOFU_E2E_APPLY_RESOURCE_INTERRUPT's named address completes
      # a real, non-null apply (a create, never a destroy). Two facts make
      # this deterministic BY CONSTRUCTION rather than by a wider margin:
      #
      #   1. node_resource_apply_instance.go's own managedResourceExecute
      #      deposes the prior current object
      #      (state.DeposeResourceInstanceObject) BEFORE calling the
      #      provider's Create, and writes the new object as current
      #      (writeResourceInstanceState) BEFORE this hook's own PostApply
      #      ever fires - both confirmed by reading that function, not
      #      assumed - so by the moment the signal is sent, the in-memory
      #      state this stage's whole assertion set cares about
      #      (current=new, deposed=old) already holds. The record file
      #      itself is written LATER still, once - backend_apply.go's
      #      opApply calls Stateless.WriteBack exactly once, after
      #      lr.Core.Apply has returned for the whole graph, from whatever
      #      applyState the (possibly interrupted) walk left behind - so
      #      there is no window in which the create half is live but the
      #      record does not yet know about it; #483's "unrecorded orphan"
      #      case this script used to defend against cannot arise from
      #      this mechanism.
      #   2. -parallelism=1 serializes the graph walker to a single
      #      worker, and this hook runs synchronously inside that worker's
      #      own call stack for the create node. Nothing else in the graph
      #      CAN be dispatched until PostApply returns, so
      #      interruptSettleTime's fixed pause before it does
      #      (apply_e2etesting_crash.go) is a guaranteed lower bound on
      #      the window's width, not a hopeful one - it only has to
      #      outlast in-process signal delivery (the runtime's signal
      #      goroutine waking cmd/choudoufu's shutdown listener, which
      #      calls tofu.Context.Stop()), which on any real system is
      #      microseconds.
      #
      # This also means the filler work #483's version scaled count_test
      # by (to give the walker something else to dispatch while the
      # external race played out) is no longer needed: nothing else has
      # to be queued up, because nothing else can run at all until the
      # signal has already been delivered. count_test stays at Part F's
      # own steady state (2) throughout - no more widening, no more H4
      # teardown.
      #
      # H1 below still retries up to a small, DOCUMENTED bound
      # (H1_ATTEMPT_MAX) rather than assuming attempt 1 always lands - a
      # genuinely unrelated failure (an emulator hiccup on the create
      # itself, say) still deserves a retry - but that bound is
      # belt-and-braces only, never load-bearing: the pass line below
      # records which attempt landed, so a run that ever needed more than
      # one can never again be mistaken for a clean pass. Issue #490's own
      # acceptance evidence is five consecutive real runs, each showing
      # attempt 1 of H1_ATTEMPT_MAX.
      #
      # THE ENGINE FIX #361 ITSELF NEEDED (unchanged by #490, carried
      # forward from #483): investigating why a first hand-built
      # reproduction of this exact crash window (a scratch estate, not this
      # script) still refused with "Two live resources claiming one
      # address" surfaced a genuine, generalizable gap in the crash-window
      # recovery design merged for this same issue
      # (internal/live/discovery/deposed_test.go's own money test,
      # TestCrashWindowBWriteBackThenBuildRecoversTheDeposedObject, already
      # proved the write-back and build seams in isolation and named "the
      # actual plan" - this script - as the next unit to prove the last
      # step). A create_before_destroy replace's own write-back commits the
      # NEW object as the record's CURRENT identity in the same pass it
      # records the OLD one as deposed, so the very next plan finds
      # aws_instance.main record-backed rather than needs-discovery -
      # issue #415's own population (decl.recordBacked), never issue #361's
      # own decl.types scalar loop the merged design wired
      # matchDeposedClaimant into. decl.recordBacked's 2-claimant branch
      # called collisionProblem unconditionally, with no deposed-record
      # lookup at all, so a record-backed crash window could never recover
      # on its own - confirmed with a debug build and a stack trace before
      # writing the fix, not guessed. The fix (internal/live/discovery/
      # discovery.go) mirrors the scalar path's own disambiguation exactly:
      # try matchDeposedClaimant first, and only fall through to
      # collisionProblem when it does not resolve the pair. Generic by
      # construction - no resource type name in the fix, the same property
      # matchDeposedClaimant/deposedClaimantMatches already have - and
      # covered by two new unit tests in internal/live/discovery/
      # deposed_test.go mirroring the existing needs-discovery pair
      # (TestDiscoverDeposedDisambiguationRecordBacked and its own
      # no-match-still-collides sibling).
      #
      # BREAK_CRASH=1 exercises this stage's own Break control instead of
      # the real checks: after the SAME real interrupt lands, assert
      # nothing is proposed - the Break text in tools/gauntlet/stages.go
      # for day2_crash, verbatim: "Interrupt and then assert nothing is
      # proposed; the assertion must fail." A real crash-window recovery
      # DOES propose a destroy, so this assertion is wrong and must fail to
      # hold - proving the real check above is load-bearing rather than a
      # grep that always matches.

      gauntlet_begin_stage day2_crash
      log "=== H0. capture the pre-crash instance; confirm create_before_destroy is a no-op on its own ==="
      G_PRE_ID="$PLAIN_INSTANCE_ID"
      G_PRE_LIVE="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-instances --instance-ids "$G_PRE_ID" --query "Reservations[0].Instances[0].State.Name" --output text 2>/dev/null || true)"
      [ "$G_PRE_LIVE" = "running" ] || fail "the pre-crash instance $G_PRE_ID is not running ahead of day2_crash (state=$G_PRE_LIVE)"
      G_PRE_AMI="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-instances --instance-ids "$G_PRE_ID" --query "Reservations[0].Instances[0].ImageId" --output text)"

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
        resource_block_crash "$G_PRE_AMI"
        echo
        count_test_block 2 "aws_vpc.main.id"
      } > "$ADOPTED/main.tf"
      G_BASELINE_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; G_BASELINE_RC=$?
      [ "$G_BASELINE_RC" -eq 0 ] || { printf '%s\n' "$G_BASELINE_OUT" | tail -30; fail "the create_before_destroy no-op plan exited $G_BASELINE_RC"; }
      grep -qF "No changes. Your infrastructure matches the configuration." <<< "$G_BASELINE_OUT" \
        || { grep -E '^  #' <<< "$G_BASELINE_OUT"; fail "adding the lifecycle block alone is not a no-op diff"; }
      log "  create_before_destroy added to aws_instance.main with no diff of its own"
      G_ADDR_KEY="$(record_key aws_instance.main)"
      G_RECORD="$ADOPTED/.tofu-records/tofu-records/$ESTATE/aws_instance/$G_ADDR_KEY"

      log "=== H1. interrupt a real create_before_destroy replace between the create committing and the destroy dispatching ==="
      # H1_ATTEMPT_MAX bounds a belt-and-braces retry, not the interrupt
      # itself - see this part's own header comment. The interrupt has
      # landed on attempt 1 every time this script has been run while
      # writing and proving #490 (its own accept evidence: 5 consecutive
      # real runs, each attempt 1 of H1_ATTEMPT_MAX, quoted in the PR).
      H1_ATTEMPT_MAX=3
      G_LANDED=0
      G_NEW_ID=""
      G_ATTEMPT=0
      while [ "$G_ATTEMPT" -lt "$H1_ATTEMPT_MAX" ] && [ "$G_LANDED" -eq 0 ]; do
        G_ATTEMPT=$((G_ATTEMPT + 1))

        # Read the CURRENTLY live instance and its ami fresh every attempt -
        # never $G_PRE_ID/$G_PRE_AMI, which name only the ORIGINAL
        # pre-crash object. A retry here means the interrupt mechanism
        # itself did not fail (that would be a bug in
        # apply_e2etesting_crash.go worth stopping and investigating, not
        # retrying past) - it means some genuinely unrelated apply error
        # occurred, which could have completed the replace in full, left
        # it fully applied with no crash window at all, or left the prior
        # object untouched; re-reading what is actually live is what keeps
        # every attempt correct regardless of which of those happened.
        G_STEP_OLD_ID="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-instances \
          --filters "Name=tag:tofu-address,Values=aws_instance.main" "Name=instance-state-name,Values=running,pending" \
          --query "Reservations[0].Instances[0].InstanceId" --output text)"
        [ -n "$G_STEP_OLD_ID" ] && [ "$G_STEP_OLD_ID" != "None" ] || fail "no live aws_instance.main found by its marker ahead of day2_crash attempt $G_ATTEMPT"
        G_STEP_OLD_AMI="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-instances --instance-ids "$G_STEP_OLD_ID" --query "Reservations[0].Instances[0].ImageId" --output text)"
        G_AMI=""
        for G_CANDIDATE in ami-91000001 ami-92000002 ami-93000003; do
          if [ "$G_CANDIDATE" != "$G_STEP_OLD_AMI" ]; then G_AMI="$G_CANDIDATE"; break; fi
        done
        [ -n "$G_AMI" ] || fail "every candidate crash-replace ami matched the live instance's own ami ($G_STEP_OLD_AMI) - the candidate pool needs a fourth literal"
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
          resource_block_crash "$G_AMI"
          echo
          count_test_block 2 "aws_vpc.main.id"
        } > "$ADOPTED/main.tf"

        # The interrupt is delivered by the engine itself (this part's own
        # header comment), so this runs in the plain foreground: no
        # background process, no output-tailing, no poll loop. The apply
        # is expected to self-terminate mid-flight, so a non-zero exit
        # here is the NORMAL outcome, not a failure - see this stage's own
        # H1 header comment for what makes that deterministic.
        G_LOG="$WORK/day2_crash_attempt_${G_ATTEMPT}.log"
        G_CRASH_OUT="$(cd "$ADOPTED" && TOFU_E2E_APPLY_RESOURCE_INTERRUPT="aws_instance.main" "$TOFU_CRASH" apply -input=false -auto-approve -no-color -parallelism=1 2>&1)"; G_CRASH_RC=$?
        printf '%s\n' "$G_CRASH_OUT" > "$G_LOG"
        log "  attempt $G_ATTEMPT: interrupted apply exited $G_CRASH_RC (a genuine crash is not expected to exit 0)"

        G_OLD_STATE="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-instances --instance-ids "$G_STEP_OLD_ID" --query "Reservations[0].Instances[0].State.Name" --output text 2>/dev/null || echo gone)"
        # A landed crash leaves TWO live instances carrying aws_instance.main's
        # marker at once (that is the whole shape this stage exercises) -
        # Reservations[0].Instances[0] alone is ambiguous between them, so
        # every matching id is read and the one that is NOT $G_STEP_OLD_ID
        # is what "new" means here.
        G_NEW_ID=""
        for G_CANDIDATE_ID in $(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-instances \
          --filters "Name=tag:tofu-address,Values=aws_instance.main" "Name=instance-state-name,Values=running,pending" \
          --query "Reservations[].Instances[].InstanceId" --output text 2>/dev/null); do
          if [ "$G_CANDIDATE_ID" != "$G_STEP_OLD_ID" ]; then G_NEW_ID="$G_CANDIDATE_ID"; break; fi
        done
        if [ "$G_OLD_STATE" = "running" ] && [ -n "$G_NEW_ID" ]; then
          # Two live claimants is necessary but not sufficient: the ONE
          # write-back this whole stage is about (current=new, deposed=old,
          # committed together) has to have actually happened for this to
          # be the recoverable window day2_crash proves. This part's own
          # header comment (point 1) is why this mechanism should never
          # actually land here with an unrecorded orphan - the deposing and
          # the current-object write both happen, in memory, strictly
          # before the signal is even sent, and the record write-back reads
          # whatever that in-memory state shows once, after the walk
          # returns - but the check stays as a defensive backstop rather
          # than an assumption, and cleans up directly if it ever fires.
          G_REC_CURRENT="$(jq -r '.identity.import_id // empty' "$G_RECORD" 2>/dev/null)"
          G_REC_DEPOSED_N="$(jq '.deposed | length' "$G_RECORD" 2>/dev/null || echo 0)"
          if [ "$G_REC_CURRENT" = "$G_NEW_ID" ] && [ "$G_REC_DEPOSED_N" = "1" ]; then
            G_LANDED=1
            G_PRE_ID="$G_STEP_OLD_ID"
            log "  attempt $G_ATTEMPT: landed - old $G_STEP_OLD_ID still running (untouched), new $G_NEW_ID running, ami $G_STEP_OLD_AMI -> $G_AMI; record confirms current=$G_REC_CURRENT deposed_count=$G_REC_DEPOSED_N"
          else
            log "  attempt $G_ATTEMPT: two live claimants but the record does not reflect the crash (current=$G_REC_CURRENT, want $G_NEW_ID; deposed_count=$G_REC_DEPOSED_N) - cleaning up the unrecorded orphan directly and retrying"
            aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 terminate-instances --instance-ids "$G_NEW_ID" >/dev/null
            for G_CLEAN_ITER in $(seq 1 30); do
              G_CLEAN_STATE="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-instances --instance-ids "$G_NEW_ID" --query "Reservations[0].Instances[0].State.Name" --output text 2>/dev/null || echo gone)"
              [ "$G_CLEAN_STATE" = "terminated" ] && break
              sleep 1
            done
            [ "$G_CLEAN_STATE" = "terminated" ] || fail "cleaning up the unrecorded orphan $G_NEW_ID after attempt $G_ATTEMPT did not converge (state=$G_CLEAN_STATE) - day2_crash cannot proceed safely"
          fi
        else
          log "  attempt $G_ATTEMPT: did not land (old $G_STEP_OLD_ID state=$G_OLD_STATE, new=$G_NEW_ID, ami $G_STEP_OLD_AMI -> $G_AMI) - retrying with a fresh replace"
        fi
      done
      [ "$G_LANDED" -eq 1 ] || fail "could not reproduce a real crash window (old object still alive, new object bound, record confirming it) after $H1_ATTEMPT_MAX real interrupted-apply attempts - the interrupt mechanism itself (internal/command/apply_e2etesting_crash.go) is meant to land on attempt 1 every time and may need investigating"

      [ -f "$G_RECORD" ] || fail "no local record file found for aws_instance.main after the crash"
      G_DEPOSED_COUNT="$(jq '.deposed | length' "$G_RECORD")"
      [ "$G_DEPOSED_COUNT" = "1" ] || fail "expected exactly one deposed entry recorded after the crash, found $G_DEPOSED_COUNT"
      G_DEPOSED_ID="$(jq -r '.deposed | to_entries[0].value.identity.import_id' "$G_RECORD")"
      [ "$G_DEPOSED_ID" = "$G_PRE_ID" ] || fail "the recorded deposed object is $G_DEPOSED_ID, not the interrupted replace's old object $G_PRE_ID"
      G_CURRENT_ID="$(record_import_id "$G_RECORD")"
      [ "$G_CURRENT_ID" = "$G_NEW_ID" ] || fail "the record's current identity is $G_CURRENT_ID, not the new object $G_NEW_ID"
      log "  record: current=$G_CURRENT_ID deposed=$G_DEPOSED_ID, written in the one crashed apply's own write-back - a real create_before_destroy replace was genuinely interrupted between the create committing and the destroy dispatching"

      if [ "${BREAK_CRASH:-}" = "1" ]; then
        log "=== H2 (BREAK_CRASH=1). assert nothing is proposed after the interrupt - this must fail ==="
        G_BREAK_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; G_BREAK_PLAN_RC=$?
        [ "$G_BREAK_PLAN_RC" -eq 0 ] || { printf '%s\n' "$G_BREAK_PLAN_OUT" | tail -30; fail "the BREAK_CRASH=1 plan exited $G_BREAK_PLAN_RC"; }
        if grep -qF "No changes. Your infrastructure matches the configuration." <<< "$G_BREAK_PLAN_OUT"; then
          fail "BREAK_CRASH=1: the plan after a real interrupted create_before_destroy replace came back empty - this stage's own check is not load-bearing"
        fi
        grep -qE 'aws_instance\.main \(deposed object [0-9a-f]+\) will be destroyed' <<< "$G_BREAK_PLAN_OUT" \
          || { printf '%s\n' "$G_BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK_CRASH=1: the plan after the crash does not even propose the expected destroy - the fixture is not what this control expects"; }
        log "  BREAK_CRASH=1: correctly proposes destroying the deposed object ($G_DEPOSED_ID) - the empty-plan assertion above correctly fails to hold"
      else
        log "=== H2. the next plan recovers on its own: destroy the deposed object, nothing else ==="
        G_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; G_PLAN_RC=$?
        [ "$G_PLAN_RC" -eq 0 ] || { printf '%s\n' "$G_PLAN_OUT" | tail -40; fail "the day2_crash recovery plan exited $G_PLAN_RC"; }
        grep -qE 'aws_instance\.main \(deposed object [0-9a-f]+\) will be destroyed' <<< "$G_PLAN_OUT" \
          || { printf '%s\n' "$G_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the recovery plan does not propose destroying the deposed object"; }
        grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$G_PLAN_OUT" \
          || { printf '%s\n' "$G_PLAN_OUT" | tail -10; fail "the recovery plan proposes something other than exactly one destroy"; }
        log "  choudoufu: exactly one destroy - the deposed object ($G_DEPOSED_ID), nothing else"

        G_APPLY_OUT="$(cd "$ADOPTED" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; G_APPLY_RC=$?
        [ "$G_APPLY_RC" -eq 0 ] || { printf '%s\n' "$G_APPLY_OUT" | tail -40; fail "the day2_crash recovery apply exited $G_APPLY_RC"; }
        grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$G_APPLY_OUT" \
          || { grep -E 'Apply complete' <<< "$G_APPLY_OUT"; fail "the recovery apply did not match the planned 0 add / 1 destroy"; }

        G_OLD_FINAL_STATE="$(aws --endpoint-url "$ADOPT_ENDPOINT" --region "$REGION" ec2 describe-instances --instance-ids "$G_PRE_ID" --query "Reservations[0].Instances[0].State.Name" --output text 2>&1)"
        [ "$G_OLD_FINAL_STATE" = "terminated" ] || fail "$G_PRE_ID (the crashed-out old object) is not terminated after the recovery apply (state=$G_OLD_FINAL_STATE)"
        log "  $G_PRE_ID terminated - confirmed via the AWS CLI, not through choudoufu's own report"

        G_DEPOSED_AFTER="$(jq '.deposed | length' "$G_RECORD")"
        [ "$G_DEPOSED_AFTER" = "0" ] || fail "the record still carries $G_DEPOSED_AFTER deposed entries after the recovery apply"
        G_CURRENT_AFTER="$(record_import_id "$G_RECORD")"
        [ "$G_CURRENT_AFTER" = "$G_NEW_ID" ] || fail "the record's current identity changed unexpectedly across the recovery apply: $G_CURRENT_AFTER"
        log "  record: the deposed entry is cleared, current identity is unchanged ($G_NEW_ID)"

        log "=== H3. one more plan: fully converged, nothing left to propose ==="
        G_FINAL_PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; G_FINAL_PLAN_RC=$?
        [ "$G_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$G_FINAL_PLAN_OUT" | tail -30; fail "the post-recovery plan exited $G_FINAL_PLAN_RC"; }
        grep -qF "No changes. Your infrastructure matches the configuration." <<< "$G_FINAL_PLAN_OUT" \
          || { grep -E '^  #' <<< "$G_FINAL_PLAN_OUT"; fail "the post-recovery plan is not empty"; }
        log "  No changes. The crash window is closed, recovered without a human."
        PLAIN_INSTANCE_ID="$G_NEW_ID"

        gauntlet_stage day2_crash pass "choudoufu: a real create_before_destroy replace of aws_instance.main was interrupted with SIGTERM (landed on attempt $G_ATTEMPT of $H1_ATTEMPT_MAX; deterministic by construction, not by timing luck - internal/command/apply_e2etesting_crash.go self-signals synchronously inside the single -parallelism=1 graph-walker goroutine the instant the create half's own write-back commits in memory, replacing #483's external tail/grep/kill race that produced issue #490's own retry-lottery evidence) strictly between the create committing (new object $G_NEW_ID, confirmed running via the AWS CLI) and the destroy of the deposed old object ($G_PRE_ID, confirmed still running and untouched via the AWS CLI) ever dispatching; the local record's one write-back correctly carried both facts at once (current=$G_NEW_ID, deposed=$G_DEPOSED_ID). Real investigation before writing this check found a genuine engine gap: issue #415's record-backed collision branch (internal/live/discovery/discovery.go, decl.recordBacked's 2-claimant path) called collisionProblem unconditionally with no deposed-record lookup at all, so a record-backed address's own crash window - exactly what a real crash's write-back leaves, since it answers the address's CURRENT identity in the same commit - could never recover on its own; fixed generically (mirrors the scalar path's own matchDeposedClaimant call, no resource type name in the fix), covered by two new unit tests (internal/live/discovery/deposed_test.go). The next plan proposed exactly one destroy (the deposed object, 0 add, 0 change, 1 destroy) and nothing else, matching stock's own documented deposed-object semantics (Stock records the old object as deposed and destroys it on the next apply); applying it destroyed exactly that object (confirmed terminated via the AWS CLI), cleared the deposed record entry, and left the current identity untouched; the plan after that is empty. BREAK_CRASH=1 confirms the empty-plan assertion this stage's Break text names correctly fails to hold against the same real crash window."
      fi
      gauntlet_end_stage
    fi
    gauntlet_end_stage
  fi
  gauntlet_end_stage
fi
gauntlet_end_stage

# ════════════════════════════════════════════════════════════════════════
# G. strict (GitHub issue #363; tools/gauntlet/stages.go's "strict" stage,
# Order 14, Status still StatusPlanned - see this section's tail comment
# for why flipping it active is not part of this unit). "With every strict
# toggle on, the estate is refused for exactly the things the toggles name
# ... and for nothing else." No stock oracle: live/LIMITATIONS.md's
# "strict-secrets" / "strict-no-source-create" / "strict-marker-repair"
# sections are what a refusal is compared against.
#
# BREAK_STRICT=1 exercises this stage's own Break control instead of the
# real check: turn ONE toggle (secrets) back off and assert its refusal is
# gone and no other appeared - the Break text in tools/gauntlet/stages.go
# for "strict", verbatim: "Turn a toggle off; its refusal must disappear
# and no other may appear." Independent of BREAK, BREAK_REMOVE and
# BREAK_COUNT: this stage carries its own scratch estate below, so it
# neither depends on nor disturbs the adopted infra parts B-F left behind.
#
# The scratch estate is deliberately not reference-ec2-vpc's own five
# resources. The refusal this stage checks is a config-time one
# (internal/live/lint's checkLiveStrict, which never reads live state), so
# the `random` provider alone carries it - no cloud call, no Docker, no AWS
# CLI - and it runs whether or not the parts above it did.
# random_password.db is the one resource declared here, purpose-built to
# be the one thing every toggle but "secrets" leaves alone:
#   - secrets = "refuse" refuses it outright: hashicorp/random 3.9.0 marks
#     bcrypt_hash and result sensitive, and strict.Refuse's own text is
#     what the assertion below matches, word for word.
#   - no_source_create = "refuse" (the schema default, named explicitly so
#     "every toggle" is not just secrets by omission) has nothing here to
#     refuse: internal/live/projection/noderesolver.go only ever fires it
#     on a CONFIG-IDENTIFIED type with no record, no marker and no
#     derivable identity, and random_password is a logical (non-cloud)
#     resource, outside that check entirely - the aws_* types this same
#     script's parts A-F carry are all ServerAssigned, exempted the same
#     way, and every "No changes" assertion since part A has already
#     exercised that exemption for real, against the live emulator.
#   - marker_repair = "never", paired with a markers "record" selection
#     naming aws_ebs_volume (a real, recordable AWS type this scratch
#     config never declares an instance of), is accepted rather than
#     refused at the config level (checkLiveStrict: a non-empty selection
#     gives "never" a mechanism), and reaches nothing: its own per-resource
#     limit, checkIgnoreChanges, fires only on a resource that declares
#     lifecycle { ignore_changes }, and none does here.
# Both are "on" in the block below and both are silent in the plan - not
# left out, but exercised and confirmed to change nothing for a config
# they do not reach, which is the other half of "for nothing else".

gauntlet_begin_stage strict
STRICT="$WORK/strict"
mkdir -p "$STRICT"
strict_block() { # $1 = the secrets setting under test ("refuse" or "store")
  cat <<EOF
terraform {
  required_providers {
    random = {
      source  = "hashicorp/random"
      version = ">= 3.0"
    }
  }
  live {
    estate = "ec2-reference-strict"
    record_store "local" {
      path = ".tofu-records"
    }
    strict {
      secrets          = "$1"
      no_source_create = "refuse"
      marker_repair    = "never"
      markers "record" {
        types = ["aws_ebs_volume"]
      }
    }
  }
}

resource "random_password" "db" {
  length = 16
}
EOF
}

log "=== G0. every strict toggle on ==="
strict_block "refuse" > "$STRICT/main.tf"
STRICT_INIT_OUT="$(cd "$STRICT" && "$TOFU" init -input=false -no-color 2>&1)"; STRICT_INIT_RC=$?
[ "$STRICT_INIT_RC" -eq 0 ] || { printf '%s\n' "$STRICT_INIT_OUT" | tail -30; fail "choudoufu init for the strict-stage scratch estate exited $STRICT_INIT_RC"; }
STRICT_PLAN_ON_OUT="$(cd "$STRICT" && "$TOFU" plan -input=false -no-color 2>&1)"; STRICT_PLAN_ON_RC=$?

if [ "${BREAK_STRICT:-}" = "1" ]; then
  log "=== G1 (BREAK_STRICT=1). turn secrets off; its refusal must disappear and no other may appear ==="
  strict_block "store" > "$STRICT/main.tf"
  STRICT_PLAN_OFF_OUT="$(cd "$STRICT" && "$TOFU" plan -input=false -no-color 2>&1)"; STRICT_PLAN_OFF_RC=$?
  [ "$STRICT_PLAN_OFF_RC" -eq 0 ] \
    || { printf '%s\n' "$STRICT_PLAN_OFF_OUT" | tail -30; fail "BREAK_STRICT=1: the plan with secrets = \"store\" exited $STRICT_PLAN_OFF_RC - a refusal appeared where none should"; }
  grep -q "^Error:" <<< "$STRICT_PLAN_OFF_OUT" \
    && { printf '%s\n' "$STRICT_PLAN_OFF_OUT"; fail "BREAK_STRICT=1: turning secrets off did not clear every refusal - this stage's check is not load-bearing"; }
  grep -qF 'random_password.db will be created' <<< "$STRICT_PLAN_OFF_OUT" \
    || { printf '%s\n' "$STRICT_PLAN_OFF_OUT"; fail "BREAK_STRICT=1: the plan with secrets = \"store\" does not propose creating random_password.db"; }
  log "  BREAK_STRICT=1: with secrets back to \"store\", the refusal is gone and the plan is an ordinary create - the real check below is skipped"
else
  [ "$STRICT_PLAN_ON_RC" -eq 1 ] \
    || { printf '%s\n' "$STRICT_PLAN_ON_OUT" | tail -30; fail "the every-toggle-on plan exited $STRICT_PLAN_ON_RC, not the refusal's usual 1"; }
  STRICT_ERR_COUNT="$(grep -c "^Error:" <<< "$STRICT_PLAN_ON_OUT")"
  [ "$STRICT_ERR_COUNT" -eq 1 ] \
    || { printf '%s\n' "$STRICT_PLAN_ON_OUT"; fail "every strict toggle on refused $STRICT_ERR_COUNT things, not exactly 1"; }
  grep -qF 'Error: Logical resource is not admitted' <<< "$STRICT_PLAN_ON_OUT" \
    || { printf '%s\n' "$STRICT_PLAN_ON_OUT"; fail "the one refusal is not \"Logical resource is not admitted\""; }
  grep -qF 'random_password.db: "random_password" is a logical resource, classified' <<< "$STRICT_PLAN_ON_OUT" \
    || { printf '%s\n' "$STRICT_PLAN_ON_OUT"; fail "the refusal does not name random_password.db as the refused instance"; }
  grep -qF 'strict { secrets = "refuse" }' <<< "$STRICT_PLAN_ON_OUT" \
    || { printf '%s\n' "$STRICT_PLAN_ON_OUT"; fail "the refusal's detail does not cite strict { secrets = \"refuse\" }, live/LIMITATIONS.md's own \"strict-secrets\" wording"; }
  grep -qi "no_source" <<< "$STRICT_PLAN_ON_OUT" \
    && { printf '%s\n' "$STRICT_PLAN_ON_OUT"; fail "no_source_create = \"refuse\" (also on) unexpectedly surfaced its own refusal text"; }
  grep -qi "marker" <<< "$STRICT_PLAN_ON_OUT" \
    && { printf '%s\n' "$STRICT_PLAN_ON_OUT"; fail "marker_repair = \"never\" (also on, with its markers \"record\" selection) unexpectedly surfaced its own refusal text"; }
  log "  every strict toggle on (secrets = \"refuse\", no_source_create = \"refuse\", marker_repair = \"never\" with a markers \"record\" selection): exactly one refusal, random_password.db under strict { secrets = \"refuse\" }, matching live/LIMITATIONS.md's \"strict-secrets\" wording word for word; the other two toggles are on and refuse nothing, because neither reaches anything this scratch estate declares"

  gauntlet_stage strict pass "every strict toggle on (secrets = refuse, no_source_create = refuse, marker_repair = never with a markers \"record\" selection naming aws_ebs_volume) against a scratch estate carrying one resource, random_password.db: exactly one refusal, matching live/LIMITATIONS.md's \"strict-secrets\" text word for word (Logical resource is not admitted / SECRET_REFUSED / strict { secrets = \"refuse\" }); no_source_create and marker_repair are on and silent, reaching nothing this config declares. BREAK_STRICT=1 turns secrets back to \"store\" alone: the refusal disappears, the plan becomes an ordinary create, and no other refusal appears. Not part of the headline bars: tools/gauntlet/stages.go keeps Status planned here, because isClear (tools/gauntlet/artifact.go) and NextUnits (tools/gauntlet/next.go) both key strictly off ActiveStages today, with no exemption for a stage the docs already call non-headline - flipping Status without first adding that exemption would silently start gating the two headline bars on this stage, which #363 did not ask for and this unit did not build."
fi
gauntlet_end_stage

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
