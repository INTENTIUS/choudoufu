#!/usr/bin/env bash
set -uo pipefail

# terraform-aws-modules/terraform-aws-ec2-instance examples/complete (tag
# v6.4.0), the most-downloaded module on the registry, crossed through
# choudoufu against floci via the real, five-stage pipeline (cold deploy,
# migrate, test plan, test apply, drift and reconverge). This is the first
# time a bare `aws_instance` compute estate is crossed through the corpus
# path (reference-ec2-vpc already proved the type works at all, hand-written
# and minimal; this is the real, most-downloaded module's own flagship
# example). Added to the core set 2026-08-23 (live/gauntlet/estates.json).
#
# THE REDUCTION. The upstream "complete" example wires up TWELVE module
# instances exercising spot instances, open/targeted capacity reservations, a
# placement group, hibernation, a standalone network interface, per-instance
# metadata-options variants, ignore_ami_changes, and a for_each over three
# instances - each a real, distinct EC2 surface, but many depend on AWS
# features floci does not model at all (capacity reservations are entirely
# unimplemented; a placement group's "cluster" strategy has real inter-AZ
# placement semantics no emulator fakes). This script keeps the two module
# instances that need no such surface: `ec2_complete` (the flagship instance -
# EIP, an IAM instance profile with an attached policy, an encrypted gp3 root
# volume, and a separately attached encrypted EBS data volume) and
# `ec2_disabled` (`create = false`, contributes no resources - the same
# always-off shape corpus-iam-policy's and corpus-sqs-basic's disabled
# instances exercise). Dropped from `ec2_complete`: `placement_group`
# (needs `aws_placement_group`, dropped from Supporting Resources),
# `hibernation` (mutually exclusive with nothing we kept, dropped for
# simplicity), `cpu_options` (was only there to pair with the dropped
# `c5.xlarge` sizing, see below), `user_data_base64` (not needed to prove
# this estate's shape), and `kms_key_id` on the one retained `ebs_volumes`
# entry (drops the standalone `aws_kms_key` resource; the encrypted-volume
# shape itself is kept). Dropped entirely: `ec2_network_interface`,
# `ec2_metadata_options`, `ec2_t2_unlimited`, `ec2_t3_unlimited`,
# `ec2_computed_name`, `ec2_ignore_ami_changes`, the `ec2_multiple` for_each
# block (count/for_each over aws_instance is already exercised elsewhere in
# this corpus), `ec2_spot_instance`, `ec2_open_capacity_reservation`,
# `ec2_targeted_capacity_reservation`, and the standalone
# `aws_ec2_capacity_reservation`/`aws_placement_group`/`aws_kms_key`/
# `aws_network_interface`/`random_string` resources they needed. outputs.tf
# is trimmed to the "EC2 Complete" section only, by exact text match
# (grep-verified below, so a moved corpus pin fails loudly). Nothing here was
# hand-authored: main.tf, outputs.tf and versions.tf are the real upstream
# files with a real subset of their content removed.
#
# THE ONBOARDING DELTAS, beyond the emulator connection flags every corpus
# crossing needs:
#   INSTANCE TYPE   ec2_complete's `instance_type` is changed from the
#                   upstream "c5.xlarge" (chosen upstream to size
#                   `cpu_options.core_count`, which this reduction drops) to
#                   "t3.micro". Not an emulator gap: floci models a fixed,
#                   deliberately small instance-type catalog
#                   (instance-type-catalog.yaml), the same kind of finite
#                   catalog the AMI data source resolves against, and c5.xlarge
#                   is simply not in it ("reading EC2 Instance Type
#                   (c5.xlarge): empty result"). t3.micro is the same type
#                   reference-ec2-vpc's own hand-written estate already uses.
#   METADATA OPTIONS  ec2_complete pins `metadata_options.http_tokens` to
#                   "optional" - a real floci defect, not a reduction. See
#                   "THE METADATA-OPTIONS DEFECT" below.
#
# THE SSM-PARAMETER DEFECT (floci issue lex00/floci#114, FIXED, not yet
# repinned). The module's root main.tf reads
# `data "aws_ssm_parameter" "this" { name = var.ami_ssm_parameter }`
# UNCONDITIONALLY - no `count`, evaluated even for `ec2_disabled` and even
# though `ec2_complete` passes an explicit `ami`. The default
# ami_ssm_parameter, "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-
# default-x86_64", is one of AWS's own documented public parameters
# (https://docs.aws.amazon.com/systems-manager/latest/userguide/parameter-store-public-parameters-ami.html),
# seeded in every real account with no setup. Confirmed directly against the
# AWS CLI with no terraform in the loop before touching any code:
# `aws ssm get-parameter --name /aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64`
# returned ParameterNotFound on the pinned image, and
# `aws ssm get-parameters-by-path --path /aws/service/ami-amazon-linux-latest --recursive`
# returned nothing at all - not an upstream module bug, floci was missing
# AWS's own guaranteed public data. Fixed on branch
# fix/ssm-public-ami-parameters (pushed to floci's `origin`, not `upstream`):
# SsmService now resolves a GetParameter miss under "/aws/service/" against
# the EC2 image catalog's own `publicParameterAliases`, seeding it
# write-through on first access. Not yet published/repinned (a shared-layer
# change the maintainer batches). Until then, this script seeds the exact
# parameter itself via `aws ssm put-parameter` (STAGE 0 below) - the same
# value a real account already has, not a fabricated one.
#
# THE METADATA-OPTIONS DEFECT (floci issue lex00/floci#115, FIXED, not yet
# repinned). The module's `metadata_options` variable DEFAULTS to
# `{ http_endpoint = "enabled", http_put_response_hop_limit = 1, http_tokens
# = "required" }` - the AWS-recommended IMDSv2-enforcing default, a real
# shape most modules that set this at all use, not something this crossing
# invented. floci's RunInstances/DescribeInstances hardcoded every
# metadataOptions field ("optional"/"1"/"enabled"/"disabled"/"disabled")
# regardless of what was requested, so every launch of this shape produced a
# PERMANENT non-empty second plan
# (`~ metadata_options { ~ http_tokens = "optional" -> "required" }`).
# Confirmed this is not a choudoufu-vs-stock difference before touching any
# code: plain, unmodified stock terraform's own second `plan` (no migration,
# no choudoufu at all) on the identical reduced config independently
# reproduces the same diff, because real hashicorp/terraform-provider-aws
# computes "required" as the field's default when the block is present but
# the field unset - the emulator just never stored what was actually
# requested. Fixed on branch fix/ec2-metadata-options-fidelity (pushed to
# `origin`): RunInstances/ModifyInstanceMetadataOptions/DescribeInstances now
# honour MetadataOptions.* end to end. Not yet published/repinned. Until
# then, ec2_complete pins `metadata_options = { http_tokens = "optional" }`
# to match what the currently-pinned image actually returns, so this
# script's plans are stable against the image pinned TODAY; re-running after
# the repin should still pass with this delta left in place (it only pins
# ONE field of the module's default object, and stock's own second plan
# above proves "optional" is a legitimate, config-driven value - it isn't a
# workaround pretending config that was never really honoured).
#
# THE RESOURCE SHAPE (35 resources - measured off `terraform state list` on
# a real run, not derived from reading the module source):
#   aws_instance                      x1  ec2_complete's own instance
#   aws_eip                           x1  ec2_complete's create_eip = true
#   aws_iam_role                      x1  ec2_complete's instance role
#   aws_iam_instance_profile          x1  ec2_complete's instance profile
#   aws_iam_role_policy_attachment    x1  AdministratorAccess on that role
#   aws_ebs_volume                    x1  the /dev/sdf data volume
#   aws_volume_attachment             x1  attaching it to the instance
#   aws_security_group                x2  ec2_complete's own SG (module
#                                         default create_security_group=true)
#                                         + the security_group module's SG
#   aws_vpc_security_group_egress_rule x2 ec2_complete's own SG's default
#                                         ipv4/ipv6 egress rules
#   aws_security_group_rule           x2  security_group module's two
#                                         ingress rules (v5.x module: the
#                                         classic single-rule-resource shape,
#                                         NOT the v6 split
#                                         ingress_rule/egress_rule types
#                                         corpus-security-group-complete
#                                         crosses - a different module major
#                                         version, a genuinely different type)
#   aws_vpc                           x1
#   aws_subnet                        x6  (3 public + 3 private)
#   aws_internet_gateway              x1
#   aws_route_table                   x4  (1 public + 3 private)
#   aws_route_table_association       x6
#   aws_route                         x1  (public -> igw)
#   aws_default_network_acl           x1
#   aws_default_route_table           x1
#   aws_default_security_group        x1
#
# TAGGABLE VS UNTAGGABLE (24 of 35 - measured off a real live-import dry run,
# cross-checked against `terraform providers schema -json`, not guessed):
# aws_iam_role_policy_attachment, aws_volume_attachment, aws_security_group_rule
# (x2), aws_route, and aws_route_table_association (x6) - 11 resources across
# 5 types - have no `tags` argument in the provider's schema. All eleven are
# genuinely derived-from-tagged: each resolves a real live id from the
# provider's identity schema over its own required arguments (an IAM
# role+policy pair, a device name, a security-group-rule hash, a route's
# destination CIDR, an association's subnet+route-table pair), never from a
# marker of its own. "A migrated estate is tagged, plus
# derived-from-tagged. There is no third bucket" (corpus-sqs-basic's own
# finding) holds again here on five NEW untaggable types this corpus had not
# crossed live before.
#
# THE ROOT VOLUME IS GENUINELY FOREIGN, not a bug. `root_block_device` is an
# inline attribute of `aws_instance`, not a separate Terraform resource -
# stock's own state never tracks that volume as its own object either, so a
# stateless replan correctly reports it as a live object with no declared
# resource behind it. Combined with floci's own default-VPC bootstrap
# (`ensureDefaultResources`: one default VPC's IGW, route table, security
# group and three subnets, real AWS's own out-of-the-box account shape),
# every plan this estate runs sees exactly 9 foreign objects: the root
# volume plus those 8 default-account objects. STAGE 3 asserts this COUNT by
# value rather than requiring "none", because "none" would be false for any
# real account this module was ever pointed at.
#
# STAGE-BY-STAGE SHAPE (see live/GAUNTLET.md):
#   1. COLD DEPLOY   plain `terraform apply` (real HashiCorp terraform), no
#                     live block anywhere; asserts 35 resources added and 0
#                     objects carry tofu-estate before migration.
#   2. MIGRATE       `choudoufu live-import -approve`: 24 of 35 eligible (11
#                     untaggable, all derived-from-tagged); one untaggable
#                     type's resolved live id (the IAM role policy
#                     attachment's role+policy pair) is asserted by value
#                     against the AWS CLI's own answer, not merely "did not
#                     error"; the follow-up apply is a genuine no-op.
#   3. TEST PLAN     state file deleted, `choudoufu live-plan` proposes no
#                     resource change and reports exactly 9 foreign objects;
#                     the instance's tofu-address is re-checked against EC2
#                     directly.
#   4. TEST APPLY    apply the empty plan; tofu-estate-tagged object count
#                     (24) is asserted unchanged before and after.
#   5. DRIFT AND     the instance's Example tag is changed out of band via
#      RECONVERGE    the AWS CLI; the next plan proposes fixing exactly that
#                     one object; apply reconverges it.
#
#   bash live/e2e/corpus-ec2-instance-complete/run.sh
#
# Needs Docker, the AWS CLI, and the real `terraform` binary on PATH for
# stage 1. .corpus is read, never written: the module (repo root, which
# examples/complete's `source = "../../"` needs) and the example are copied
# out to a temp directory first, same as every other corpus crossing.
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4740, clear of every
#                other live/e2e fixture's port).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        corrupt one assertion on purpose, to prove it is
#                load-bearing rather than a grep that always matches. Each
#                value corrupts a DIFFERENT assertion and each must make this
#                script exit non-zero at that assertion:
#                  schema    expect the IAM role policy attachment's resolved
#                            live id to name the wrong policy ARN
#                            (ReadOnlyAccess instead of the real
#                            AdministratorAccess) at stage 2. Same role, same
#                            attachment, wrong pairing - the one thing a "did
#                            it error?" check cannot catch.
#                  identity  expect the instance's tofu-address to name a
#                            different, never-created module at stage 3.
#                  drift     tamper a second, unrelated object (the EIP's
#                            tag) before stage 5's mutation, so the plan must
#                            propose fixing two objects where the assertion
#                            demands exactly one.
#                  1         alias for `schema`.
#
# Exit codes: 0 on a real pass of all five stages, non-zero on a real
# failure. Every assertion reads command output, an exit code, or the
# emulator's own answer through the AWS CLI, never choudoufu's own report of
# itself.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SRC_MODULE="$ROOT/.corpus/ec2-instance"
WORK="$(mktemp -d)"
EST="$WORK/ec2-instance/examples/complete"
FLOCI_PORT="${FLOCI_PORT:-4740}"
FLOCI_NAME="choudoufu-corpus-ec2-instance-complete-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="ec2-instance-crossing"
REGION="eu-west-1"

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
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
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }
gauntlet_begin

# Which single assertion BREAK corrupts (see the header's env-override notes).
BREAK_AT="${BREAK:-}"
[ "$BREAK_AT" = "1" ] && BREAK_AT="schema"
case "$BREAK_AT" in
  ""|schema|identity|drift) ;;
  *) fail "BREAK must be one of: schema, identity, drift (1 is an alias for schema)" ;;
esac

# ── 0. tools and corpus ─────────────────────────────────────────────────────
log "=== 0. tools and corpus ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v terraform >/dev/null 2>&1 || fail "the terraform binary is not on PATH - needed to build unmarked reference infra"
[ -d "$SRC_MODULE" ] || fail "$SRC_MODULE is missing - run 'go run ./tools/corpus-fetch' first"
[ -d "$SRC_MODULE/examples/complete" ] || fail "$SRC_MODULE/examples/complete is missing - run 'go run ./tools/corpus-fetch' first"

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

# .corpus is shared across every worktree and is NEVER written to: the whole
# module (repo root, which examples/complete's `source = "../../"` needs) is
# copied out first.
mkdir -p "$WORK/ec2-instance"
cp -R "$SRC_MODULE"/. "$WORK/ec2-instance"
rm -rf "$EST/.terraform" "$EST/.terraform.lock.hcl"
[ -f "$EST/main.tf" ] || fail "the estate copy is missing main.tf"
log "  module + example copied out of .corpus into $WORK"

# ── 1. the reduction (see header) ───────────────────────────────────────────
log "=== 1. the reduction and the onboarding deltas ==="

perl -0777 -pi -e 's/\nmodule "ec2_network_interface" \{.*?\nmodule "ec2_disabled" \{/\nmodule "ec2_disabled" \{/s' "$EST/main.tf"
grep -q 'module "ec2_network_interface"' "$EST/main.tf" && fail "the module-block reduction did not remove ec2_network_interface..ec2_computed_name - the corpus pin has moved"
grep -q 'module "ec2_disabled"' "$EST/main.tf" || fail "ec2_disabled did not survive the reduction - the corpus pin has moved"

perl -0777 -pi -e 's/\n################################################################################\n# EC2 Module - with ignore AMI changes\n################################################################################.*?\n################################################################################\n# Supporting Resources/\n################################################################################\n# Supporting Resources/s' "$EST/main.tf"
grep -q 'ec2_ignore_ami_changes\|ec2_spot_instance\|ec2_multiple\|capacity_reservation' "$EST/main.tf" && fail "the ignore-ami-changes..capacity-reservation section reduction did not remove it - the corpus pin has moved"
grep -q '# Supporting Resources' "$EST/main.tf" || fail "the Supporting Resources header did not survive - the corpus pin has moved"

perl -0777 -pi -e 's/\nresource "aws_placement_group" "web" \{.*\z//s' "$EST/main.tf"
grep -qE 'resource "aws_placement_group"|resource "aws_kms_key"|resource "aws_network_interface"|resource "random_string"' "$EST/main.tf" && fail "the standalone supporting-resource reduction did not remove them - the corpus pin has moved"

perl -0pi -e 's/instance_type          = "c5\.xlarge" # used to set core count below/instance_type          = "t3.micro"/' "$EST/main.tf"
grep -q 'c5.xlarge' "$EST/main.tf" && fail "the instance-type onboarding delta did not apply - the corpus pin has moved"

perl -0pi -e 's/  placement_group        = aws_placement_group\.web\.id\n  # conflicts with placement_group\n  # placement_group_id = aws_placement_group\.web\.placement_group_id\n//' "$EST/main.tf"
perl -0pi -e 's/  create_eip       = true\n  disable_api_stop = false\n/  create_eip = true\n\n  # floci issue lex00\/floci#115 (fixed, not yet repinned): DescribeInstances\n  # hardcoded every metadataOptions field regardless of what a launch\n  # requested. Pinning http_tokens to what the currently-pinned image\n  # actually returns keeps this plan stable until the repin; see the\n  # header'"'"'s "THE METADATA-OPTIONS DEFECT".\n  metadata_options = {\n    http_tokens = "optional"\n  }\n/' "$EST/main.tf"
grep -q 'placement_group' "$EST/main.tf" && fail "the placement_group removal did not apply - the corpus pin has moved"
grep -q 'metadata_options = {' "$EST/main.tf" || fail "the metadata_options onboarding delta did not apply - the corpus pin has moved"

perl -0pi -e 's/  # only one of these can be enabled at a time\n  hibernation = true\n  # enclave_options_enabled = true\n\n//' "$EST/main.tf"
grep -q 'hibernation' "$EST/main.tf" && fail "the hibernation removal did not apply - the corpus pin has moved"

perl -0pi -e 's/  user_data_base64            = base64encode\(local\.user_data\)\n  user_data_replace_on_change = false\n\n//' "$EST/main.tf"
grep -q 'user_data_base64' "$EST/main.tf" && fail "the user_data removal did not apply - the corpus pin has moved"

perl -0pi -e 's/  cpu_options = \{\n    core_count       = 2\n    threads_per_core = 1\n  \}\n//' "$EST/main.tf"
grep -q 'cpu_options' "$EST/main.tf" && fail "the cpu_options removal did not apply - the corpus pin has moved"

perl -0pi -e 's/      kms_key_id = aws_kms_key\.this\.arn\n//' "$EST/main.tf"
grep -q 'kms_key_id' "$EST/main.tf" && fail "the ebs_volumes kms_key_id removal did not apply - the corpus pin has moved"

perl -0pi -e 's/  user_data = <<-EOT\n    #!\/bin\/bash\n    echo "Hello Terraform!"\n  EOT\n\n//' "$EST/main.tf"
grep -q 'Hello Terraform' "$EST/main.tf" && fail "the unused user_data local removal did not apply - the corpus pin has moved"

grep -q 'module "ec2_complete"' "$EST/main.tf" || fail "ec2_complete did not survive the reduction - the corpus pin has moved"
grep -q 'module "vpc"' "$EST/main.tf" || fail "the vpc supporting module did not survive - the corpus pin has moved"
grep -q 'module "security_group"' "$EST/main.tf" || fail "the security_group supporting module did not survive - the corpus pin has moved"
grep -q 'data "aws_ami" "amazon_linux"' "$EST/main.tf" || fail "the AMI data source did not survive - the corpus pin has moved"
log "  DELTA  main.tf reduced to ec2_complete/ec2_disabled + vpc/security_group/aws_ami supporting resources (see header)"

perl -0777 -pi -e 's/\n# EC2 T2 Unlimited\n.*\z//s' "$EST/outputs.tf"
grep -q 'ec2_t2_unlimited\|ec2_t3_unlimited\|ec2_spot_instance\|ec2_multiple' "$EST/outputs.tf" && fail "the outputs reduction did not remove the dropped modules' outputs - the corpus pin has moved"
grep -q 'output "ec2_complete_id"' "$EST/outputs.tf" || fail "ec2_complete's own outputs did not survive - the corpus pin has moved"
log "  DELTA  outputs.tf reduced to the EC2 Complete section"

perl -0777 -pi -e 's/    random = \{\n      source  = "hashicorp\/random"\n      version = ">= 3\.0"\n    \}\n//' "$EST/versions.tf"
grep -q 'hashicorp/random' "$EST/versions.tf" && fail "the unused random-provider requirement removal did not apply - the corpus pin has moved"
log "  DELTA  versions.tf: unused random provider requirement dropped (random_string was removed above)"

perl -0pi -e 's/(provider "aws" \{\n  region = local\.region\n)\}/$1\n  access_key                   = "test"\n  secret_key                   = "test"\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  s3_use_path_style            = true\n}/' "$EST/main.tf"
grep -q 's3_use_path_style' "$EST/main.tf" || fail "the emulator connection delta did not match main.tf - the corpus pin has moved"
log "  DELTA  emulator connection flags added to the provider block; no backend, no version pin, no live block yet"

log "=== 2. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"ec2"' <<< "${HEALTH:-}" && break
  sleep 2
done
grep -q '"ec2"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (ec2) at $ENDPOINT"
log "  healthy"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION"

# floci issue lex00/floci#114 (fixed, not yet repinned): the module reads
# this AWS-documented public parameter unconditionally, for every module
# instance including the disabled one. Seed it directly so this run behaves
# the way a real account already does, rather than routing around the
# module's real behaviour.
awsl ssm put-parameter --name /aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64 \
  --type String --value ami-0abcdef1234567891 --overwrite >/dev/null \
  || fail "seeding the public AMI SSM parameter failed"
log "  seeded /aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64 (floci issue #114 workaround)"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 1: COLD DEPLOY - plain terraform, no choudoufu, no live block
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=cold_deploy
log "=== STAGE 1: cold deploy (terraform apply, the real reduced example + deltas) ==="
# The shared plugin cache - see live/e2e/README.md, "The shared plugin
# cache" - and #339's dependency-lock-file escape hatch, the same as every
# other terraform-aws-modules crossing.
export TF_PLUGIN_CACHE_DIR="${TF_PLUGIN_CACHE_DIR:-$HOME/.terraform.d/plugin-cache}"
export TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE=1
mkdir -p "$TF_PLUGIN_CACHE_DIR"
( cd "$EST" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$EST" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "stage 1 init failed"; }
COLD_OUT="$(cd "$EST" && terraform apply -input=false -auto-approve -no-color 2>&1)"; COLD_RC=$?
[ "$COLD_RC" -eq 0 ] || { printf '%s\n' "$COLD_OUT" | tail -40; fail "the cold apply failed"; }
grep -qE 'Apply complete! Resources: 35 added' <<< "$COLD_OUT" \
  || { grep -E 'Apply complete' <<< "$COLD_OUT"; fail "the cold apply did not create exactly 35 resources"; }
log "  $(grep -E 'Apply complete' <<< "$COLD_OUT")"
[ -f "$EST/terraform.tfstate" ] || fail "plain terraform left no state file to migrate from"

# The exact managed shape, read off terraform's own state rather than off
# this script's reading of the module (see the header's resource-shape
# table for the by-type tally this list reproduces).
WANT_SHAPE="$(LC_ALL=C sort <<'EOF'
module.ec2_complete.aws_ebs_volume.this["/dev/sdf"]
module.ec2_complete.aws_eip.this[0]
module.ec2_complete.aws_iam_instance_profile.this[0]
module.ec2_complete.aws_iam_role.this[0]
module.ec2_complete.aws_iam_role_policy_attachment.this["AdministratorAccess"]
module.ec2_complete.aws_instance.this[0]
module.ec2_complete.aws_security_group.this[0]
module.ec2_complete.aws_volume_attachment.this["/dev/sdf"]
module.ec2_complete.aws_vpc_security_group_egress_rule.this["ipv4_default"]
module.ec2_complete.aws_vpc_security_group_egress_rule.this["ipv6_default"]
module.security_group.aws_security_group.this_name_prefix[0]
module.security_group.aws_security_group_rule.ingress_rules[0]
module.security_group.aws_security_group_rule.ingress_rules[1]
module.vpc.aws_default_network_acl.this[0]
module.vpc.aws_default_route_table.default[0]
module.vpc.aws_default_security_group.this[0]
module.vpc.aws_internet_gateway.this[0]
module.vpc.aws_route.public_internet_gateway[0]
module.vpc.aws_route_table.private[0]
module.vpc.aws_route_table.private[1]
module.vpc.aws_route_table.private[2]
module.vpc.aws_route_table.public[0]
module.vpc.aws_route_table_association.private[0]
module.vpc.aws_route_table_association.private[1]
module.vpc.aws_route_table_association.private[2]
module.vpc.aws_route_table_association.public[0]
module.vpc.aws_route_table_association.public[1]
module.vpc.aws_route_table_association.public[2]
module.vpc.aws_subnet.private[0]
module.vpc.aws_subnet.private[1]
module.vpc.aws_subnet.private[2]
module.vpc.aws_subnet.public[0]
module.vpc.aws_subnet.public[1]
module.vpc.aws_subnet.public[2]
module.vpc.aws_vpc.this[0]
EOF
)"
GOT_SHAPE="$(cd "$EST" && terraform state list 2>/dev/null | grep -v '\.data\.\|^data\.' | LC_ALL=C sort)"
[ "$GOT_SHAPE" = "$WANT_SHAPE" ] || {
  printf 'want:\n%s\ngot:\n%s\n' "$WANT_SHAPE" "$GOT_SHAPE"
  fail "the cold estate's managed resource shape is not the 35 resources this script documents - the corpus pin has moved"; }
log "  managed shape confirmed: 35 resources across 13 types"

UNMARKED="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$UNMARKED" = "0" ] || fail "plain terraform's own objects already carry tofu-estate=$ESTATE before migration - this crossing proves nothing"
log "  confirmed unmarked: 0 objects carry tofu-estate=$ESTATE before migration"

INSTANCE_ID="$(cd "$EST" && terraform output -raw ec2_complete_id)"
ROLE_NAME="$(cd "$EST" && terraform output -raw ec2_complete_iam_role_name)"
[ -n "$INSTANCE_ID" ] || fail "could not read ec2_complete_id from terraform output"
[ -n "$ROLE_NAME" ] || fail "could not read ec2_complete_iam_role_name from terraform output"
EIP_ALLOC_ID="$(awsl ec2 describe-addresses --filters "Name=instance-id,Values=$INSTANCE_ID" \
  --query 'Addresses[0].AllocationId' --output text)"
[ -n "$EIP_ALLOC_ID" ] && [ "$EIP_ALLOC_ID" != "None" ] || fail "could not find the EIP allocated to $INSTANCE_ID"
log "  instance $INSTANCE_ID, IAM role $ROLE_NAME (name_prefix-generated, read from output rather than assumed), EIP $EIP_ALLOC_ID"

cp "$EST/terraform.tfstate" "$WORK/cold.tfstate"

log ""
gauntlet_stage cold_deploy pass "35 resources added across 13 types (aws_instance, aws_eip, aws_iam_role/instance_profile/role_policy_attachment, aws_ebs_volume, aws_volume_attachment, aws_security_group x2, aws_vpc_security_group_egress_rule x2, aws_security_group_rule x2, vpc/subnet/route*/igw/default_* from the vpc module), 0 objects carry tofu-estate before migration"
log "STAGE 1 (cold deploy): PASS"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE - choudoufu live-import against the cold state
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=migrate
log "=== STAGE 2: migrate (choudoufu live-import -approve) ==="
perl -0777 -pi -e 's/(\n  provider_meta "aws" \{\n    user_agent = \[\n      "github\.com\/terraform-aws-modules\/terraform-aws-ec2-instance"\n    \]\n  \}\n)\}/$1\n  live {\n    estate = "'"$ESTATE"'"\n  }\n}/s' "$EST/versions.tf"
grep -q "estate = \"$ESTATE\"" "$EST/versions.tf" || fail "the live block delta did not match versions.tf - the corpus pin has moved"

( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "choudoufu init failed"; }

rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"

IMPORT_OUT="$(cd "$EST" && "$TOFU" live-import -state="$WORK/cold.tfstate" -estate="$ESTATE" -no-color 2>&1)"; IMPORT_RC=$?
[ "$IMPORT_RC" -eq 0 ] || { printf '%s\n' "$IMPORT_OUT" | tail -40; fail "live-import (dry run) failed"; }
grep -qF "24 of 35 resource instance(s) are eligible for stamping" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import did not report exactly 24 of 35 resources as eligible - the corpus pin or the fix under test has moved"; }
grep -qF "No tag has been written." <<< "$IMPORT_OUT" || fail "the dry run wrote a tag - it must not"

# THE SCHEMA-FALLBACK ASSERTION: aws_iam_role_policy_attachment has no row in
# the generated identity table and no tags argument. Its live id is the
# provider's own composite key (role name + policy ARN, both required
# arguments), and this is a real, live test of that schema-fallback path on
# a type this corpus had not crossed live before - asserted by value, not by
# "did the run error".
WANT_POLICY_ARN="arn:aws:iam::aws:policy/AdministratorAccess"
if [ "$BREAK_AT" = "schema" ]; then
  WANT_POLICY_ARN="arn:aws:iam::aws:policy/ReadOnlyAccess"
  log "  BREAK=schema: expecting the wrong policy ARN (ReadOnlyAccess, never"
  log "                attached) paired with the real role name - the one"
  log "                thing a \"did it error?\" check cannot catch. This"
  log "                step must fail."
fi
WANT_ATTACHMENT_ID="${ROLE_NAME}/${WANT_POLICY_ARN}"
ATTACHMENT_LINE="$(grep -F 'aws_iam_role_policy_attachment' <<< "$IMPORT_OUT" | grep -F 'live id:' | head -1)"
grep -qF "live id: $WANT_ATTACHMENT_ID" <<< "$ATTACHMENT_LINE" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "aws_iam_role_policy_attachment did not resolve to $WANT_ATTACHMENT_ID via the provider identity schema; got: ${ATTACHMENT_LINE:-<no live id line at all>}"; }
log "  schema fallback resolved aws_iam_role_policy_attachment by value: $ROLE_NAME/$WANT_POLICY_ARN"
log "  dry run: 24 of 35 eligible (11 untaggable across 5 types, all derived-from-tagged); nothing written yet"

APPROVE_OUT="$(cd "$EST" && "$TOFU" live-import -state="$WORK/cold.tfstate" -estate="$ESTATE" -approve -no-color 2>&1)"; APPROVE_RC=$?
[ "$APPROVE_RC" -eq 0 ] || { printf '%s\n' "$APPROVE_OUT" | tail -40; fail "live-import -approve failed"; }
grep -qF "24 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 11 skipped" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not stamp exactly 24 of 35 resources cleanly with 11 skipped"; }
log "  24 stamped, 11 skipped as untaggable"

GOT_ADDR="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$INSTANCE_ID" "Name=key,Values=tofu-address" --query 'Tags[0].Value' --output text)"
[ "$GOT_ADDR" = "module.ec2_complete.aws_instance.this:0" ] || fail "$INSTANCE_ID carries tofu-address=$GOT_ADDR, not module.ec2_complete.aws_instance.this:0"
log "  marker verified directly against EC2, not through choudoufu's own report: $INSTANCE_ID -> tofu-address=$GOT_ADDR"

CONVERGE_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; CONVERGE_RC=$?
[ "$CONVERGE_RC" -eq 0 ] || { printf '%s\n' "$CONVERGE_OUT" | tail -40; fail "the post-migrate apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$CONVERGE_OUT" \
  || { grep -E 'Apply complete' <<< "$CONVERGE_OUT"; fail "the post-migrate apply was not a genuine no-op"; }
log "  $(grep -E 'Apply complete' <<< "$CONVERGE_OUT") (genuine no-op)"
[ ! -f "$EST/terraform.tfstate" ] || fail "the post-migrate apply wrote a state file"

log ""
gauntlet_stage migrate pass "24 of 35 eligible (11 untaggable across 5 types - aws_iam_role_policy_attachment, aws_volume_attachment, aws_security_group_rule x2, aws_route, aws_route_table_association x6 - all resolved by provider identity schema), 24 stamped, 0 failed, 11 skipped; the IAM role policy attachment's composite live id asserted by value; genuine no-op on the follow-up apply"
log "STAGE 2 (migrate): PASS"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - state deleted (already true), live-plan empty
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=test_plan
log "=== STAGE 3: test plan (live-plan empty, identity re-checked) ==="
[ ! -f "$EST/terraform.tfstate" ] || fail "a state file exists ahead of stage 3"

plan_into() { ( cd "$EST" && "$TOFU" live-plan -input=false -no-color ); }
PLAN_OUT="$(plan_into 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -60; fail "live-plan exited $PLAN_RC"; }
[ ! -f "$EST/terraform.tfstate" ] || fail "live-plan wrote a state file"
grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$PLAN_OUT" \
  && { grep -E '^  # .+ will be' <<< "$PLAN_OUT"; fail "the plan proposes a resource change"; }

# THE FOREIGN-OBJECT COUNT (see the header's "THE ROOT VOLUME IS GENUINELY
# FOREIGN" note): the instance's own root EBS volume (an inline attribute of
# aws_instance, never a Terraform resource of its own) plus floci's
# out-of-the-box default-VPC bootstrap (IGW, route table, security group,
# three subnets, that security group's two default egress rules) - 9 real,
# expected foreign objects, not "none". Asserting this by value is what
# distinguishes "this estate's known foreign shape" from "something this
# estate owns was missed by the sweep".
grep -qE "^Foreign resources: 9 live resources not owned by estate $ESTATE" <<< "$PLAN_OUT" \
  || { grep -E '^Foreign resources:' <<< "$PLAN_OUT"; fail "expected exactly 9 foreign objects (the instance's own root volume + floci's default-VPC bootstrap); the corpus pin, floci's default-account shape, or a real gap has moved"; }
log "  no resource change proposed; exactly 9 foreign objects (root volume + default-VPC bootstrap, both expected)"

WANT_ADDR2="module.ec2_complete.aws_instance.this:0"
if [ "$BREAK_AT" = "identity" ]; then
  WANT_ADDR2="module.ec2_disabled.aws_instance.this:0"
  log "  BREAK=identity: expecting tofu-address=$WANT_ADDR2 on the instance -"
  log "           the SAME shape and the SAME resource type, just the wrong"
  log "           (and in fact never-created) module. This step must fail."
fi
GOT_ADDR2="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$INSTANCE_ID" "Name=key,Values=tofu-address" --query 'Tags[0].Value' --output text)"
[ "$GOT_ADDR2" = "$WANT_ADDR2" ] || fail "$INSTANCE_ID's tofu-address is $GOT_ADDR2, not $WANT_ADDR2"
log "  identity re-check (via EC2, after the state file has never existed this run): unchanged"

log ""
gauntlet_stage test_plan pass "no resource change proposed, exactly 9 foreign objects (the instance's own root volume + floci's default-VPC bootstrap); instance tofu-address re-checked against EC2"
log "STAGE 3 (test plan): PASS"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 4: TEST APPLY - apply the empty plan, assert a genuine no-op
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=test_apply
log "=== STAGE 4: test apply (apply the empty plan; object count unchanged) ==="
BEFORE_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$BEFORE_N" = "24" ] || fail "expected 24 tofu-estate-tagged objects before stage 4, got $BEFORE_N"

APPLY2_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; APPLY2_RC=$?
[ "$APPLY2_RC" -eq 0 ] || { printf '%s\n' "$APPLY2_OUT" | tail -40; fail "the post-migration apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY2_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY2_OUT"; fail "the post-migration apply was not a no-op"; }

AFTER_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$AFTER_N" = "$BEFORE_N" ] || fail "object count changed across a no-op apply: $BEFORE_N -> $AFTER_N"
[ ! -f "$EST/terraform.tfstate" ] || fail "a state file exists after the apply"
log "  genuine no-op: $BEFORE_N objects before, $AFTER_N after, no state file either time"

log ""
gauntlet_stage test_apply pass "genuine no-op (0 added, 0 changed, 0 destroyed); 24 objects before, 24 after, no state file"
log "STAGE 4 (test apply): PASS"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 5: DRIFT AND RECONVERGE - mutate one object, replan, assert one fix
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=drift_reconverge
log "=== STAGE 5: drift and reconverge (mutate one object out of band) ==="
if [ "$BREAK_AT" = "drift" ]; then
  awsl ec2 create-tags --resources "$EIP_ALLOC_ID" --tags Key=Example,Value=tampered-by-BREAK
  log "  BREAK=drift: also tampered $EIP_ALLOC_ID's Example tag - stage 5 must now see TWO drifted objects and fail the single-object assertion"
fi

awsl ec2 create-tags --resources "$INSTANCE_ID" --tags Key=Example,Value=tampered-out-of-band
DRIFTED_VALUE="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$INSTANCE_ID" "Name=key,Values=Example" --query 'Tags[0].Value' --output text)"
[ "$DRIFTED_VALUE" = "tampered-out-of-band" ] || fail "the out-of-band tag mutation did not take"
log "  mutated $INSTANCE_ID's Example tag to \"tampered-out-of-band\" directly via the AWS CLI"

DRIFT_PLAN_OUT="$(plan_into 2>&1)"; DRIFT_PLAN_RC=$?
[ "$DRIFT_PLAN_RC" -eq 0 ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | tail -60; fail "the drift-detection plan exited $DRIFT_PLAN_RC"; }

CHANGED_ADDRS="$(grep -oE '^  # \S+ will be updated' <<< "$DRIFT_PLAN_OUT" | awk '{print $2}' | sort -u)"
N_CHANGED="$(printf '%s\n' "$CHANGED_ADDRS" | grep -c . || true)"
# No BREAK special-case here on purpose (see corpus-sqs-basic's precedent):
# under BREAK=drift two objects really have been tampered, so this ordinary
# assertion is the one that must fail - that IS the demonstration that it is
# load-bearing.
[ "$N_CHANGED" = "1" ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | grep -E '^  # .+ will be'; fail "expected exactly 1 object proposed for a fix, got $N_CHANGED"; }
printf '%s\n' "$CHANGED_ADDRS" | grep -qF "module.ec2_complete.aws_instance.this[0]" \
  || fail "the plan does not propose fixing the tampered instance; got: $CHANGED_ADDRS"
log "  the plan proposes fixing exactly one object: $(printf '%s' "$CHANGED_ADDRS")"

RECONVERGE_APPLY="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; RECONVERGE_RC=$?
[ "$RECONVERGE_RC" -eq 0 ] || { printf '%s\n' "$RECONVERGE_APPLY" | tail -40; fail "the reconverge apply failed"; }
grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$RECONVERGE_APPLY" \
  || { grep -E 'Apply complete' <<< "$RECONVERGE_APPLY"; fail "the reconverge apply did not change exactly 1 resource"; }
FIXED_VALUE="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$INSTANCE_ID" "Name=key,Values=Example" --query 'Tags[0].Value' --output text)"
[ "$FIXED_VALUE" = "ex-complete" ] || fail "$INSTANCE_ID's Example tag is \"$FIXED_VALUE\" after reconverging, not \"ex-complete\""
log "  reconverged: $INSTANCE_ID's Example tag is back to \"ex-complete\""

log ""
gauntlet_stage drift_reconverge pass "one object tampered, exactly 1 object proposed and applied (0 added, 1 changed, 0 destroyed), tag reconverged to \"ex-complete\""
log "STAGE 5 (drift and reconverge): PASS"
log ""

CURRENT_STAGE=""
gauntlet_end

log "=== PASS ==="
log ""
log "terraform-aws-modules/terraform-aws-ec2-instance's flagship EXAMPLE"
log "(reduced per this script's header) - the first bare-aws_instance corpus"
log "crossing - through all five stages: cold deploy with plain terraform,"
log "choudoufu live-import adoption, an empty replan with the state file"
log "deleted and identity re-checked against EC2, a genuine no-op apply, and"
log "drift on the instance reconverging without touching anything else."
log ""
log "35 managed resources across 13 types; 24 taggable and stamped, 11"
log "untaggable across 5 NEW types this corpus had not crossed live before"
log "(aws_iam_role_policy_attachment, aws_volume_attachment,"
log "aws_security_group_rule, aws_route, aws_route_table_association), every"
log "one resolved by the provider's own identity schema and asserted by"
log "value. Tagged, plus derived-from-tagged; no third bucket. Two real"
log "floci defects found and fixed (issues #114, #115), pending repin."
