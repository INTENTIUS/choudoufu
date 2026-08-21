#!/usr/bin/env bash
set -uo pipefail

# The five-stage real-estate crossing (live/corpus-crossing-manifest.json)
# for uyuni-project/sumaform (live/corpus-manifest.json, pinned by commit -
# see that entry's own comment for why no tag), the first OpenTofu-NATIVE
# estate this goal has crossed: every other crossing tonight came from
# terraform-aws-modules/*, which is Terraform-authored and merely OpenTofu-
# compatible. sumaform describes itself as "OpenTofu configuration to
# quickly set up SUSE Multi-Linux Manager/Uyuni environments" - not
# "Terraform configuration" - and its own README leads with the OpenTofu
# binary, not terraform.
#
# THE SCOPING DECISION, and why it is not the whole example.
#
# sumaform is a libvirt/Uyuni-focused framework with AWS as one of several
# backend options (main.tf.aws.example, alongside azure/libvirt/ssh/null).
# The full example composes FOUR host roles - module.base (network + a
# bastion instance), module.mirror, module.server, module.minion - all
# built from the SAME leaf module, backend_modules/aws/host. That leaf
# module has NO root-facing toggle to disable its own Salt/SSH
# provisioning (`terraform_data.host_salt_configuration`, real
# remote-exec/file provisioners over SSH against the guest) for THREE of
# those four roles: base's bastion, mirror, and minion all launch it
# unconditionally. Only modules/server exposes `provision` as a real
# input, defaulting true, overridable to false. Floci launches a real
# Docker container per instance and CAN do real SSH (see its own
# docs/services/ec2.md - key injection, a mapped host port), but
# sumaform's own connection blocks hardcode `host = aws_instance.instance
# [...].public_dns` with no port override, which is not reachable from
# the host machine's own SSH client without the module itself routing
# through floci's mapped port - not ours to add without forking the
# module. This is exactly the "EC2 instances with real boot behavior,
# out of scope" case the task that produced this script named up front.
#
# So this crossing uses module.server ALONE (provision=false, the one
# role with a real off-switch) as the real, unmodified-sumaform-code
# centerpiece, plus this script's own plain aws_vpc/subnet/internet
# gateway/route table/NAT gateway/security group resources standing in
# for what module.base's own network submodule would otherwise create
# (create_network=true) - because create_network=true ALSO turns out to
# be unusable for a different, unrelated reason: floci does not implement
# CreateDhcpOptions (already a documented, out-of-scope-for-tonight gap -
# see live/e2e/corpus-vpc-complete/run.sh's own header) or
# ReplaceRouteTableAssociation (aws_main_route_table_association), and
# module.base's network submodule creates both unconditionally whenever
# create_network=true, with no toggle to skip just those two. Both are
# real floci API gaps, neither is this crossing's to implement (a genuine
# EC2 feature each, not a data-catalog fix). create_network=false skips
# both, and also zeroes module.base's own bastion quantity for free
# (`quantity = local.create_network ? 1 : 0`), which is what routes
# around the unreachable-provisioner problem above without touching a
# single line of sumaform's own code.
#
# create_private_network and create_additional_network are separate
# toggles from create_network and are turned off too (server attaches to
# the public subnet instead, via provider_settings.public_instance =
# true): backend_modules/aws/network's `data "aws_nat_gateway" "default"`
# (looked up by subnet_id, needed when create_network=false) is ALSO
# unconditional whenever create_network=false, and races the NAT gateway
# this script creates in the SAME apply unless module.base is ordered
# after it - which needs a real depends_on, which in turn forces
# module.base's OWN `data.aws_region.current` (feeding
# `data.aws_ami.tumbleweedo`'s count) to be unknown until apply, an
# "Invalid count argument" PLAN-time error regardless of which region is
# in use. Both problems are the identical unconditional-data-source shape
# 6a33837d (the EKS worker AMI fix, below) already names for a different
# module; sumaform's own ami.tf and network/main.tf both lean on it
# throughout. Rather than layer a second depends_on workaround on top of
# the first, stage 1 below applies in the two ordered phases Terraform's
# OWN "Invalid count argument" error suggests (-target, then a plain
# apply) - a real, common bootstrapping pattern, not a routed-around
# failure.
#
# A REAL FLOCI GAP, FIXED: sumaform's ami.tf declares one
# data "aws_ami" block PER SUPPORTED GUEST OS (~23 of them) feeding a
# single `ami_info` local every host module reads through lookup() - so
# EVERY ONE of those data sources evaluates unconditionally on every
# plan, regardless of which single image an estate's own instances
# actually pick (this crossing picks ubuntu2204 throughout). Floci's
# image catalog had zero AWS-owned SUSE, Red Hat or Rocky Linux entries,
# so ~20 of the ~23 returned "Your query returned no results" and
# module.base could not even compute its own outputs. Fixed on the fork:
# lex00/floci branch fix/sumaform-suse-ami-catalog, commit d4479d36,
# published as ghcr.io/lex00/floci:sumaform-suse-ami (digest below) -
# seeds 20 catalog entries under SUSE's, AWS Marketplace's, Rocky
# Linux's and Red Hat's real, documented AWS publisher accounts, named to
# match each data source's own name_regex. None of the 20 are ever
# actually launched (this crossing's own instances use ubuntu2204,
# already cataloged); they exist purely so the unconditional lookup
# resolves - the same shape 6a33837d ("seed AWS-owned EKS worker AMIs")
# already fixed for terraform-aws-eks, not a new pattern.
#
# THE REAL BLOCK THIS CROSSING FOUND, NOT PAPERED OVER: with the AMI
# catalog gap fixed, stage 1 (cold deploy) and stage 2 (migrate) both
# genuinely PASS - 11 real resources, 9 of them stamped cleanly. Stage 3
# (test plan) is refused outright, for real, structural reasons baked
# into backend_modules/aws/host - the ONE leaf module EVERY AWS host role
# in this estate (bastion, mirror, server, minion) shares:
#
#   1. aws_instance.instance carries an unconditional, provisioner-less
#      `connection { private_ip = self.private_ip }` block - dead code in
#      sumaform's own module (no provisioner anywhere in the same
#      resource references it), but internal/live/lint's own
#      checkProvisioners rule flags a connection block on its own terms,
#      deliberately, per that function's own comment ("reported in its
#      own right rather than silently tolerated when the provisioners it
#      feeds have been removed").
#   2. Both aws_instance.instance and aws_ebs_volume.data_disk carry
#      `lifecycle { ignore_changes = [tags] }` - sumaform's own
#      WORKAROUND comment names why: "SUSE internal openbare AWS accounts
#      add special tags... After the first apply, terraform removes those
#      tags." A real, legitimate need at authoring time, but it ignores
#      the WHOLE tags argument, which is exactly where this mode's own
#      tofu-estate/tofu-address markers live - internal/live/lint's
#      ignore-changes rule refuses it for the reason its own error text
#      gives: the update that would write the markers is planned and then
#      silently discarded, so an adopted resource can never actually be
#      re-tagged. The message even names the fix - `ignore_changes =
#      [tags["Owner"]]`, not the whole argument - but making it is an
#      edit to sumaform's own module, which this crossing does not make.
#
# Because both live in the ONE leaf module every role shares, this is not
# an artifact of this crossing's own reduced slice: module.mirror,
# module.minion, and module.base's own bastion would hit the identical
# two refusals the moment any of them is actually instantiated. A real,
# generalizable finding about this real, popular project's compatibility
# with choudoufu today, not a corner this script backed itself into.
#
# Stages 4 and 5 are unwritten, the same discipline
# live/e2e/corpus-lambda-simple/run.sh's own header uses for its own
# stage-3 block: there is nothing running yet for them to exercise.
#
#   bash live/e2e/corpus-sumaform-aws/run.sh
#
# Needs Docker and the AWS CLI. Prefers the real `tofu` binary for stage
# 1's cold deploy, since this is specifically an OpenTofu-native estate;
# falls back to `terraform` if `tofu` is not on PATH (TF_COLD_BIN
# overrides either way). .corpus is read, never written: modules/ and
# backend_modules/ - the only two directories this reduced slice uses -
# are copied out to a scratch directory first, same as every other corpus
# crossing.
#
# Env overrides:
#   TOFU_BIN      path to a prebuilt choudoufu binary; skips the go build.
#   TF_COLD_BIN   the plain binary for stage 1 (default: tofu if present
#                 on PATH, else terraform).
#   FLOCI_PORT    host port for the emulator (default 4716, clear of
#                 every other corpus-*/reference-* script's own default).
#   FLOCI_IMAGE   the emulator image; defaults to
#                 ghcr.io/lex00/floci:sumaform-suse-ami (this crossing's
#                 own AMI-catalog fix on top of tonight's other floci
#                 fixes) rather than live/floci-image, because that fix
#                 has not been folded into the shared pin yet - see this
#                 run's own report for the digest and why.
#   BREAK         set to 1 to corrupt one expected tag string before
#                 stage 2's assertion, proving it is load-bearing rather
#                 than a grep that always matches.
#   DEBUG_KEEP    set to 1 to skip the exit trap: the floci container and
#                 the WORK directory are left behind for inspection.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SRC="$ROOT/.corpus/sumaform"
WORK="$(mktemp -d)"
PLAIN="$WORK/plain"
ESTATE="$WORK/estate"
FLOCI_PORT="${FLOCI_PORT:-4716}"
FLOCI_NAME="choudoufu-corpus-sumaform-aws-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-ghcr.io/lex00/floci:sumaform-suse-ami}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"
REGION="eu-west-1"
AZ="eu-west-1a"
ESTATE_NAME="sumaform-aws-crossing"
KEY_NAME="sumaform-crossing-key"

if [ -n "${TF_COLD_BIN:-}" ]; then
  TF_COLD="$TF_COLD_BIN"
elif command -v tofu >/dev/null 2>&1; then
  TF_COLD="tofu"
else
  TF_COLD="terraform"
fi

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
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
gauntlet_begin
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }

# ── 0. tools and corpus ─────────────────────────────────────────────────────
log "=== 0. tools and corpus ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v "$TF_COLD" >/dev/null 2>&1 || fail "TF_COLD_BIN=$TF_COLD is not on PATH - needed for stage 1's plain cold deploy"
command -v ssh-keygen >/dev/null 2>&1 || fail "ssh-keygen is not on PATH"
[ -d "$SRC/modules" ] && [ -d "$SRC/backend_modules" ] || fail "$SRC is missing modules/ or backend_modules/ - run 'go run ./tools/corpus-fetch' first"
log "  cold deploy binary: $TF_COLD"

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

# copy_estate <destdir>: modules/ and backend_modules/ only - the two
# directories this reduced slice reaches - plus sumaform's own documented
# "select a backend" setup step (README.md: `ln -s ../backend_modules/
# <BACKEND>/ modules/backend`, not this script's invention) and a fresh
# SSH key pair for aws_instance's required key_name/key_file inputs
# (never actually used for a real SSH session - module.server runs with
# provision=false, so the connection block inside its own
# terraform_data.host_salt_configuration, gated on that variable, is
# never evaluated; the key only has to exist for RunInstances to accept
# key_name and for Terraform's own `file(key_file)` local-side read).
copy_estate() {
  local dest="$1"
  mkdir -p "$dest"
  rsync -a --exclude '.git' "$SRC/modules" "$SRC/backend_modules" "$dest/"
  ln -sf ../backend_modules/aws/ "$dest/modules/backend"
  ssh-keygen -t ed25519 -f "$dest/crossing-key" -N '' -q -C sumaform-crossing
}

# write_main_tf <destdir> <live_block>: the estate itself. $2 is either
# empty (stage 1, cold deploy) or a `live { ... }` block (stage 2+) -
# every other line is identical between the two copies, ordinary
# onboarding (#269-style version pin, emulator provider flags), never a
# per-stage behavior change.
write_main_tf() {
  local dest="$1" live_block="$2"
  cat > "$dest/main.tf" <<EOF
terraform {
  required_version = ">= 1.5.7"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.59.0"
    }
  }
$live_block
}

locals {
  region            = "$REGION"
  availability_zone = "$AZ"
  key_file          = "\${path.module}/crossing-key"
  key_name          = "$KEY_NAME"
}

provider "aws" {
  region = local.region

  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style            = true
}


# Bring-your-own network: plain resources standing in for what
# modules/base's own network submodule creates when create_network=true -
# this script's header explains why create_network=true itself is not
# usable against floci today (CreateDhcpOptions,
# ReplaceRouteTableAssociation).
resource "aws_vpc" "crossing" {
  cidr_block           = "172.16.0.0/16"
  enable_dns_support   = true
  enable_dns_hostnames = true
  tags                 = { Name = "sumaform-crossing-vpc" }
}

resource "aws_internet_gateway" "crossing" {
  vpc_id = aws_vpc.crossing.id
  tags   = { Name = "sumaform-crossing-igw" }
}

resource "aws_subnet" "crossing_public" {
  vpc_id                  = aws_vpc.crossing.id
  cidr_block              = "172.16.0.0/24"
  availability_zone       = local.availability_zone
  map_public_ip_on_launch = true
  tags                    = { Name = "sumaform-crossing-public-subnet" }
}

resource "aws_route_table" "crossing_public" {
  vpc_id = aws_vpc.crossing.id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.crossing.id
  }
  tags = { Name = "sumaform-crossing-public-rt" }
}

resource "aws_route_table_association" "crossing_public" {
  subnet_id      = aws_subnet.crossing_public.id
  route_table_id = aws_route_table.crossing_public.id
}

resource "aws_eip" "crossing_nat" {
  domain = "vpc"
  tags   = { Name = "sumaform-crossing-nat-eip" }
}

resource "aws_nat_gateway" "crossing" {
  allocation_id = aws_eip.crossing_nat.id
  subnet_id     = aws_subnet.crossing_public.id
  depends_on    = [aws_internet_gateway.crossing]
  tags          = { Name = "sumaform-crossing-nat" }
}

resource "aws_security_group" "crossing_public" {
  name        = "sumaform-crossing-public-sg"
  description = "sumaform crossing public sg"
  vpc_id      = aws_vpc.crossing.id
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
  tags = { Name = "sumaform-crossing-public-sg" }
}

module "base" {
  source = "./modules/base"

  cc_username = "admin"
  cc_password = "admin12345"

  name_prefix     = "sumaform-crossing-"
  product_version = "uyuni-released"

  provider_settings = {
    availability_zone = local.availability_zone
    region             = local.region
    ssh_allowed_ips    = ["0.0.0.0/0"]
    key_name           = local.key_name
    key_file           = local.key_file
    bastion_image      = "ubuntu2204"

    create_network            = false
    create_private_network    = false
    create_additional_network = false
    vpc_id                    = aws_vpc.crossing.id
    public_subnet_id          = aws_subnet.crossing_public.id
    public_security_group_id  = aws_security_group.crossing_public.id
  }
}

module "server" {
  source             = "./modules/server"
  base_configuration = module.base.configuration

  name  = "server"
  image = "ubuntu2204"

  provision            = false
  repository_disk_size = 10
  provider_settings    = { public_instance = true }
}

output "server_id" {
  value = module.server.configuration.id
}
EOF
}

# ── 1. floci ─────────────────────────────────────────────────────────────
log "=== 1. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"ec2"' <<< "${HEALTH:-}" && break
  sleep 2
done
grep -q '"ec2"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (ec2) at $ENDPOINT"
log "  healthy"

export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" AWS_ENDPOINT_URL="$ENDPOINT"

copy_estate "$PLAIN"
copy_estate "$ESTATE"
write_main_tf "$PLAIN" ""
write_main_tf "$ESTATE" '
  live {
    estate = "'"$ESTATE_NAME"'"
    record_store "local" {
      path = ".tofu-records"
    }
  }'
log "  estate copied out of .corpus/sumaform (modules/, backend_modules/ only) into $PLAIN and $ESTATE"

awsl ec2 import-key-pair --key-name "$KEY_NAME" --public-key-material "fileb://$PLAIN/crossing-key.pub" >/dev/null \
  || fail "importing the crossing key pair into floci failed"
log "  key pair $KEY_NAME imported (never actually used for SSH - provision=false)"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 1: COLD DEPLOY - plain tofu/terraform, no live block, no choudoufu
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=cold_deploy
log "=== STAGE 1: cold deploy (plain $TF_COLD, two-phase - see header) ==="
( cd "$PLAIN" && "$TF_COLD" init -input=false -no-color >/dev/null 2>&1 ) \
  || { ( cd "$PLAIN" && "$TF_COLD" init -input=false -no-color 2>&1 | tail -30 ); fail "plain init failed"; }

# Phase 1: the NAT gateway and its dependents only. modules/base's own
# `data "aws_nat_gateway" "default"` (create_network=false) looks the NAT
# gateway up by subnet_id with no resource-level edge tying it to
# module.base, so it can race the NAT gateway's own creation inside a
# single apply - this is Terraform's own suggested workaround for exactly
# this shape ("use the -target argument to first apply only the resources
# that the count depends on"), not a routed-around failure.
PHASE1="$(cd "$PLAIN" && "$TF_COLD" apply -input=false -auto-approve -no-color \
  -target=aws_nat_gateway.crossing -target=aws_route_table_association.crossing_public -target=aws_security_group.crossing_public 2>&1)" || {
  printf '%s\n' "$PHASE1" | tail -40; fail "stage 1 phase 1 (network bootstrap) failed"; }
log "  phase 1: $(grep -E 'Apply complete' <<< "$PHASE1" | tail -1)"

PHASE2="$(cd "$PLAIN" && "$TF_COLD" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$PHASE2" | tail -40; fail "stage 1 phase 2 (module.base + module.server) failed"; }
grep -qE 'Apply complete! Resources: 3 added' <<< "$PHASE2" \
  || { grep -E 'Apply complete' <<< "$PHASE2"; fail "stage 1 phase 2 did not add exactly 3 resources (module.server's instance, EBS volume, attachment) - the AMI catalog fix or the corpus pin may have moved"; }
log "  phase 2: $(grep -E 'Apply complete' <<< "$PHASE2" | tail -1)"

[ -f "$PLAIN/terraform.tfstate" ] || fail "plain $TF_COLD left no state file to migrate from"
# state list also lists every data source (module.base alone reads ~23
# data.aws_ami/data.aws_region/data.aws_vpc/data.aws_nat_gateway) - filter
# those out to count only actual managed resource instances.
TOTAL_RESOURCES="$(cd "$PLAIN" && "$TF_COLD" state list | grep -vE '(^|\.)data\.' | wc -l | tr -d ' ')"
[ "$TOTAL_RESOURCES" = "11" ] || fail "expected exactly 11 managed resource instances in state, got $TOTAL_RESOURCES"
log "  11 managed resource instances total (8 network/VPC pieces + 3 from module.server)"

INSTANCE_ID="$(cd "$PLAIN" && "$TF_COLD" output -raw server_id)"
[ -n "$INSTANCE_ID" ] || fail "could not read module.server's instance id back out of the cold state"
log "  module.server's instance: $INSTANCE_ID"

COLD_TAGS="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$INSTANCE_ID" "Name=key,Values=tofu-address" --query 'length(Tags)' --output text)"
[ "$COLD_TAGS" = "0" ] || fail "the cold-deployed instance already carries a tofu-address tag before migration - this test proves nothing"
log "  confirmed unmarked: $INSTANCE_ID carries no tofu-address tag"

log ""
log "STAGE 1 (cold deploy): PASS"
log ""
gauntlet_stage cold_deploy pass "11 managed resource instances, genuinely cold, genuinely unmarked"
CURRENT_STAGE=migrate

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 2: choudoufu live-import ==="
( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) \
  || { ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "estate init failed"; }

log "--- 2a: live-import, read-only first ---"
IMPORT_OUT="$(cd "$ESTATE" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE_NAME" 2>&1)" || {
  printf '%s\n' "$IMPORT_OUT" | tail -40; fail "live-import (dry run) failed"; }
grep -qF "9 of 11 resource instance(s) are eligible for stamping" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT" | head -60; fail "live-import did not verify exactly 9 of 11 as eligible (the 7 network resources plus the instance and EBS volume - the route table association and volume attachment are untaggable)"; }
grep -qF "No tag has been written." <<< "$IMPORT_OUT" || fail "the dry run wrote a tag - it must not"
log "  9 of 11 verified/drifted against the live system; nothing written yet"

log "--- 2b: -approve ---"
APPROVE_OUT="$(cd "$ESTATE" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE_NAME" -approve 2>&1)" || {
  printf '%s\n' "$APPROVE_OUT" | tail -40; fail "live-import -approve failed"; }
grep -qF "9 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 2 skipped" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not stamp exactly 9 resources cleanly"; }
log "  9 stamped"

VPC_ID="$(cd "$PLAIN" && "$TF_COLD" state show aws_vpc.crossing | grep -E '^\s+id\s+=' | head -1 | awk -F'"' '{print $2}')"
[ -n "$VPC_ID" ] || fail "could not read the crossing VPC's id back out of the cold state"

# assert_tag <resource-id> <label> <expected-estate>: reads both markers
# straight off floci through the AWS CLI (never off choudoufu's own
# report of itself) and asserts BOTH the value and the estate name -
# HANDOFF.md's own standing bar, assert the rendered identity, not just a
# verdict. <expected-estate> is parameterized so BREAK=1 can hand this
# exact function a wrong value below and prove it actually discriminates,
# rather than asserting only the real value ever runs through it.
assert_tag() {
  local rid="$1" label="$2" want_estate="$3" addr est
  addr="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$rid" "Name=key,Values=tofu-address" --query 'Tags[0].Value' --output text)"
  est="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$rid" "Name=key,Values=tofu-estate" --query 'Tags[0].Value' --output text)"
  [ -n "$addr" ] && [ "$addr" != "None" ] || { echo "  $label ($rid) carries no tofu-address"; return 1; }
  [ "$est" = "$want_estate" ] || { echo "  $label ($rid) carries tofu-estate=$est, not $want_estate"; return 1; }
  echo "  $label ($rid) -> tofu-address=$addr tofu-estate=$est"
  return 0
}

assert_tag "$VPC_ID" "the crossing VPC" "$ESTATE_NAME" || fail "the crossing VPC's markers are wrong"
assert_tag "$INSTANCE_ID" "module.server's instance" "$ESTATE_NAME" || fail "module.server's instance's markers are wrong"

# Negative control: the SAME function, over the SAME live resource, with a
# deliberately wrong expected estate name - proving assert_tag actually
# discriminates rather than passing anything it is handed.
if assert_tag "$VPC_ID" "the crossing VPC" "not-the-real-estate-name" >/dev/null 2>&1; then
  fail "assert_tag PASSED with a deliberately wrong expected estate name - this stage's assertion is not load-bearing"
fi
log "  negative control: assert_tag rejects a wrong expected estate name"

if [ "${BREAK:-}" = "1" ]; then
  # BREAK=1 flips the control above from a self-check into the reported
  # failure, proving this script's own exit code responds to it.
  fail "BREAK=1: treating the negative control above as the run's own result, to prove this script's exit code is not vacuously 0"
fi

log ""
log "STAGE 2 (migrate): PASS"
log ""
gauntlet_stage migrate pass "9 stamped, 0 failed, 2 skipped"
CURRENT_STAGE=test_plan

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - real, structural refusal (see this script's header)
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 3: no state file, live-plan ==="
rm -f "$ESTATE/terraform.tfstate" "$ESTATE/terraform.tfstate.backup"
PLAN_OUT="$(cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color 2>&1)"
PLAN_RC=$?
[ "$PLAN_RC" -ne 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -40; fail "live-plan exited 0 - the known refusal has apparently been fixed; update this script's header and stages 3-5"; }

grep -qF 'Error: Provisioners are not available under live resource markers' <<< "$PLAN_OUT" \
  || { printf '%s\n' "$PLAN_OUT" | tail -60; fail "expected the connection-block refusal, did not find it - something else has moved"; }
grep -qF 'connection block on' <<< "$PLAN_OUT" || fail "expected the connection-block refusal to name its construct"
[ "$(grep -cF 'Error: Ownership markers would be ignored' <<< "$PLAN_OUT")" = "2" ] \
  || { printf '%s\n' "$PLAN_OUT" | tail -60; fail "expected exactly 2 ignore-changes refusals (aws_instance.instance and aws_ebs_volume.data_disk), got $(grep -cF 'Error: Ownership markers would be ignored' <<< "$PLAN_OUT")"; }
grep -qF 'lifecycle { ignore_changes =' <<< "$PLAN_OUT" || fail "expected the ignore-changes refusal detail"
log "  confirmed: exactly the 3 known refusals fired (1 connection block, 2 ignore_changes[tags]) -"
log "  all three live in backend_modules/aws/host, the ONE leaf module every AWS host role"
log "  in this estate shares. See this script's header for why this is not routed around."

log ""
log "STAGE 3 (test plan): BLOCKED (real, structural - see header). Stages 4-5 unwritten:"
log "nothing runs yet for them to exercise. Stopping here rather than forcing a plan"
log "this script cannot honestly call empty."
log ""
gauntlet_stage test_plan fail "3 known refusals: 1 connection block + 2 ignore_changes[tags]"
gauntlet_stage test_apply not_run "stage 3 blocks structurally; stages 4-5 unwritten"
gauntlet_stage drift_reconverge not_run "stage 3 blocks structurally; stages 4-5 unwritten"
CURRENT_STAGE=""
log "=== PARTIAL: stages 1 and 2 verified for real; stage 3 blocked as described above ==="
exit 1
gauntlet_end
