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
# THE ONBOARDING DELTA, beyond the emulator connection flags every corpus
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
#
# TWO FLOCI DEFECTS FOUND, FIXED, AND REPINNED (no workaround left in this
# script - both onboarding deltas that used to route around them are gone):
#
#   THE SSM-PARAMETER DEFECT (lex00/floci#114). The module's root main.tf
#   reads `data "aws_ssm_parameter" "this" { name = var.ami_ssm_parameter }`
#   UNCONDITIONALLY - no `count`, evaluated even for `ec2_disabled` and even
#   though `ec2_complete` passes an explicit `ami`. The default
#   ami_ssm_parameter, "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-
#   default-x86_64", is one of AWS's own documented public parameters
#   (https://docs.aws.amazon.com/systems-manager/latest/userguide/parameter-store-public-parameters-ami.html),
#   seeded in every real account with no setup. Confirmed directly against
#   the AWS CLI with no terraform in the loop before touching any code:
#   `aws ssm get-parameter --name /aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64`
#   returned ParameterNotFound, and `aws ssm get-parameters-by-path --path
#   /aws/service/ami-amazon-linux-latest --recursive` returned nothing at
#   all - not an upstream module bug, floci was missing AWS's own guaranteed
#   public data. Fixed on branch fix/ssm-public-ami-parameters (merged to
#   floci's `origin` main, lex00/floci#116): SsmService now resolves a
#   GetParameter miss under "/aws/service/" against the EC2 image catalog's
#   own `publicParameterAliases`, seeding it write-through on first access.
#   This script used to seed the parameter itself via `aws ssm put-parameter`
#   before STAGE 1 as a stand-in; that workaround is removed now that the
#   pinned image resolves it on its own - a run against a pin that regresses
#   this would fail loudly (ParameterNotFound propagating into the AMI data
#   source) rather than silently keep passing behind a workaround nobody
#   would notice needed to come out.
#
#   THE METADATA-OPTIONS DEFECT (lex00/floci#115). The module's
#   `metadata_options` variable DEFAULTS to `{ http_endpoint = "enabled",
#   http_put_response_hop_limit = 1, http_tokens = "required" }` - the
#   AWS-recommended IMDSv2-enforcing default, a real shape most modules that
#   set this at all use, not something this crossing invented. floci's
#   RunInstances/DescribeInstances hardcoded every metadataOptions field
#   ("optional"/"1"/"enabled"/"disabled"/"disabled") regardless of what was
#   requested, so every launch of this shape produced a PERMANENT non-empty
#   second plan (`~ metadata_options { ~ http_tokens = "optional" ->
#   "required" }`). Confirmed this is not a choudoufu-vs-stock difference
#   before touching any code: plain, unmodified stock terraform's own second
#   `plan` (no migration, no choudoufu at all) on the identical reduced
#   config independently reproduces the same diff, because real
#   hashicorp/terraform-provider-aws computes "required" as the field's
#   default when the block is present but the field unset - the emulator
#   just never stored what was actually requested. Fixed on branch
#   fix/ec2-metadata-options-fidelity (merged to floci's `origin` main,
#   lex00/floci#116): RunInstances/ModifyInstanceMetadataOptions/
#   DescribeInstances now honour MetadataOptions.* end to end. This script
#   used to pin `ec2_complete`'s own `metadata_options = { http_tokens =
#   "optional" }`, overriding the module's real "required" default, as a
#   stand-in for the fix; that onboarding delta is removed now that the
#   pinned image honours the module's own default correctly, so this
#   crossing exercises the real upstream shape (http_tokens = "required")
#   rather than a value chosen to dodge the defect.
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
#                  replace   day2_replace's own break control (PART F, after
#                            the real rename, before the real remove):
#                            manufacture the exact coexistence "skip the
#                            destroy half" describes directly - a second
#                            live instance is launched via the AWS CLI,
#                            carrying the SAME tofu-address/tofu-slot as the
#                            instance a genuine replace would have
#                            destroyed - and the next plan must report the
#                            collision loudly, not propose nothing. The
#                            Break text in tools/gauntlet/stages.go,
#                            verbatim.
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

# PART GREENFIELD (live/GAUNTLET.md #13) needs one MORE floci container, a
# fresh namespace choudoufu applies into directly. Its own oracle reuses
# $ENDPOINT: STAGE 1's plain terraform cold-deploy is still genuinely
# unmarked at the point PART GREENFIELD runs (right after STAGE 1, before
# STAGE 2's live-import ever tags anything - this script keeps every stage
# on the SAME $EST tree/namespace rather than a separate PLAIN copy, so
# $ENDPOINT already IS "the cloud after stock's cold deploy" until migrate).
FLOCI_GREEN_PORT="${FLOCI_GREEN_PORT:-$((FLOCI_PORT + 400))}"
FLOCI_GREEN_NAME="choudoufu-corpus-ec2-instance-complete-green-$$"
GREEN_ENDPOINT="http://127.0.0.1:${FLOCI_GREEN_PORT}"
GREEN_ESTATE="ec2-instance-greenfield"
GREEN="$WORK/green/examples/complete"

ESTATE="ec2-instance-crossing"
REGION="eu-west-1"

cleanup() {
  docker rm -f "$FLOCI_NAME" "$FLOCI_GREEN_NAME" >/dev/null 2>&1 || true
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
  ""|schema|identity|drift|replace) ;;
  *) fail "BREAK must be one of: schema, identity, drift, replace (1 is an alias for schema)" ;;
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
grep -q 'placement_group' "$EST/main.tf" && fail "the placement_group removal did not apply - the corpus pin has moved"

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

# ══════════════════════════════════════════════════════════════════════════
# PART GREENFIELD (greenfield, live/GAUNTLET.md #13, active)
# ══════════════════════════════════════════════════════════════════════════
#
# choudoufu applies the identical reduced+delta'd example directly with a
# live block, no migration, into a SEPARATE namespace ($GREEN_ENDPOINT).
# $GREEN is a straight copy of $EST as STAGE 1 left it - main.tf/outputs.tf/
# versions.tf are untouched by a `terraform apply` (only terraform.tfstate
# is written), so the reduction and every onboarding delta above are
# already baked in; only the live block (STAGE 2's own delta) still needs
# adding. The oracle is $ENDPOINT, STAGE 1's own plain terraform cold-
# deploy, still genuinely unmarked at this point (STAGE 2's live-import
# has not run yet) - no third container needed.
CURRENT_STAGE=greenfield
log "=== PART GREENFIELD: 0. one more floci container, a fresh namespace ==="
docker run -d --rm -p "${FLOCI_GREEN_PORT}:4566" --name "$FLOCI_GREEN_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_GREEN_NAME failed"
GH=""
for _ in $(seq 1 45); do
  GH="$(curl -fs "${GREEN_ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"ec2"' <<< "${GH:-}" && break
  sleep 2
done
grep -q '"ec2"' <<< "${GH:-}" || fail "floci did not come up healthy (ec2) at $GREEN_ENDPOINT"
log "  healthy: greenfield=$GREEN_ENDPOINT oracle=$ENDPOINT (STAGE 1's own plain apply)"

# The WHOLE module tree, not just the example leaf directory, preserving
# the same nesting depth - a shallow copy silently breaks
# module.ec2_complete's own "../../" relative source path (the failure mode
# corpus-sqs-basic's own greenfield comment names: confirmed here live, the
# first attempt at this copy resolved "../../" to an empty directory and
# apply failed with five stale "Unsupported argument" diagnostics before
# this fix).
cp -R "$WORK/ec2-instance" "$WORK/green"
rm -rf "$GREEN/.terraform" "$GREEN/.terraform.lock.hcl" "$GREEN/terraform.tfstate" "$GREEN/terraform.tfstate.backup"
perl -0777 -pi -e 's/(\n  provider_meta "aws" \{\n    user_agent = \[\n      "github\.com\/terraform-aws-modules\/terraform-aws-ec2-instance"\n    \]\n  \}\n)\}/$1\n  live {\n    estate = "'"$GREEN_ESTATE"'"\n\n    record_store "local" {\n      path = ".tofu-records"\n    }\n  }\n}/s' "$GREEN/versions.tf"
grep -q "estate = \"$GREEN_ESTATE\"" "$GREEN/versions.tf" || fail "the greenfield live-block delta did not match versions.tf - the corpus pin has moved"
log "  DELTA  live block (record_store, evidence for #364 A2) added on top of \$EST's own reduction + onboarding deltas"

log "=== PART GREENFIELD: 1. choudoufu apply from nothing, no migration, no state file ever existing ==="
( cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the greenfield init failed"; }
GREEN_APPLY_OUT="$(cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"
if [ $? -ne 0 ]; then
  printf '%s\n' "$GREEN_APPLY_OUT" | grep -E '^Error' -A 6 | head -200
  gauntlet_stage greenfield fail "the greenfield apply failed - see live/gauntlet/logs/corpus-ec2-instance-complete.log for the full diagnostic; cold_deploy/migrate/test_plan/test_apply/drift_reconverge/day2_rename/day2_remove for this estate are unaffected (checked earlier/later in the same run)"
  CURRENT_STAGE=""
  docker rm -f "$FLOCI_GREEN_NAME" >/dev/null 2>&1 || true
  SKIP_GREENFIELD_REST=1
fi
if [ -z "${SKIP_GREENFIELD_REST:-}" ]; then
grep -qE 'Apply complete! Resources: 35 added' <<< "$GREEN_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT"; fail "the greenfield apply did not create exactly 35 resources"; }
log "  $(grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT")"

awsg() { aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" "$@"; }

log "=== PART GREENFIELD: 2. markers, read through the AWS CLI directly ==="
# Read the live id via the AWS CLI, not "choudoufu output": this estate
# writes no terraform.tfstate at all under the live block (record-based),
# and "output -raw" against that came back "No outputs found" when this
# was first tried live - a real, separate finding about the output command
# under a stateless record-backed run, not this stage's own subject, so
# it is routed around here rather than chased. $GREEN_ENDPOINT is a brand
# new namespace with only this one apply's objects in it, so the single
# running/pending instance is unambiguous.
GREEN_INSTANCE_ID="$(awsg ec2 describe-instances --filters "Name=instance-state-name,Values=running,pending" --query "Reservations[0].Instances[0].InstanceId" --output text)"
[ -n "$GREEN_INSTANCE_ID" ] && [ "$GREEN_INSTANCE_ID" != "None" ] || fail "no live instance found in the greenfield namespace"
GREEN_INSTANCE_ADDR="$(awsg ec2 describe-tags --filters "Name=resource-id,Values=$GREEN_INSTANCE_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
[ "$GREEN_INSTANCE_ADDR" = "module.ec2_complete.aws_instance.this:0" ] \
  || fail "the greenfield instance carries tofu-address=$GREEN_INSTANCE_ADDR, not module.ec2_complete.aws_instance.this:0"
GREEN_INSTANCE_EST="$(awsg ec2 describe-tags --filters "Name=resource-id,Values=$GREEN_INSTANCE_ID" "Name=key,Values=tofu-estate" --query "Tags[0].Value" --output text)"
[ "$GREEN_INSTANCE_EST" = "$GREEN_ESTATE" ] || fail "the greenfield instance carries tofu-estate=$GREEN_INSTANCE_EST, not $GREEN_ESTATE"
log "  instance $GREEN_INSTANCE_ID carries tofu-address=$GREEN_INSTANCE_ADDR tofu-estate=$GREEN_INSTANCE_EST - read via the AWS CLI, not choudoufu's own report"

log "=== PART GREENFIELD: 3. the record store holds instances, including the untaggable types (#364 A2) ==="
GREEN_RECORD_FILES="$(find "$GREEN/.tofu-records/tofu-records" -type f ! -name '*.lock' ! -name '*.tmp-*' 2>/dev/null | wc -l | tr -d ' ')"
[ "$GREEN_RECORD_FILES" -gt 0 ] || fail "expected at least one record under the local record store after the greenfield apply, found none"
log "  $GREEN_RECORD_FILES records persisted, read directly off the local record store"

log "=== PART GREENFIELD: 4. the next plan proposes nothing (besides the same 9 foreign default-account objects STAGE 3 already names) ==="
GREEN_PLAN_OUT="$(cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; GREEN_PLAN_RC=$?
[ "$GREEN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$GREEN_PLAN_OUT" | tail -30; fail "the greenfield replan exited $GREEN_PLAN_RC"; }
if ! grep -qF "No changes. Your infrastructure matches the configuration." <<< "$GREEN_PLAN_OUT"; then
  NONEMPTY_ITEMS="$(grep -E '^  # .+ will be' <<< "$GREEN_PLAN_OUT" | sed 's/^  # //' | tr '\n' '; ')"
  log "  the replan is NOT empty: $NONEMPTY_ITEMS"
  gauntlet_stage greenfield fail "the greenfield replan proposes real resource action on objects the SAME apply just created (no other run touched this namespace in between): $NONEMPTY_ITEMS. A create proposed for something that already exists is the wrong-marker-shaped failure HANDOFF ranks above a missing one, not a safe fallback; not fixed in this script-only pass. 35 objects were created and the instance's own marker verified fine (see the earlier PART GREENFIELD steps in the same run), so this is narrower than a total apply failure - the specific objects named above are the gap."
  CURRENT_STAGE=""
  docker rm -f "$FLOCI_GREEN_NAME" >/dev/null 2>&1 || true
  SKIP_GREENFIELD_REST=1
fi
if [ -z "${SKIP_GREENFIELD_REST:-}" ]; then
log "  No changes."

log "=== PART GREENFIELD: 5. structural comparison against stock's cold deploy (STAGE 1), via the AWS CLI on both endpoints ==="
instance_shape() { # $1=endpoint $2=instance-id
  aws --endpoint-url "$1" --region "$REGION" ec2 describe-instances --instance-ids "$2" \
    --query "Reservations[0].Instances[0].[InstanceType,ImageId,length(BlockDeviceMappings)]" --output text 2>/dev/null
}
STOCK_INSTANCE_ID="$INSTANCE_ID"
[ -n "$STOCK_INSTANCE_ID" ] || fail "no instance id captured from stock's own cold-deploy output (STAGE 1)"
GREEN_SHAPE="$(instance_shape "$GREEN_ENDPOINT" "$GREEN_INSTANCE_ID")"
STOCK_SHAPE="$(instance_shape "$ENDPOINT" "$STOCK_INSTANCE_ID")"
[ "$GREEN_SHAPE" = "$STOCK_SHAPE" ] || fail "the instance's shape differs: greenfield=$GREEN_SHAPE stock=$STOCK_SHAPE"
log "  instance shape matches (type/ami/block-device-count: $GREEN_SHAPE)"

GREEN_TAGGED="$(awsg resourcegroupstaggingapi get-resources --tag-filters "Key=tofu-estate,Values=$GREEN_ESTATE" --query 'length(ResourceTagMappingList)' --output text)"
[ "$GREEN_TAGGED" -gt 0 ] || fail "no live objects carry tofu-estate=$GREEN_ESTATE after the greenfield apply"
log "  $GREEN_TAGGED objects carry tofu-estate=$GREEN_ESTATE - read via the AWS CLI"

gauntlet_stage greenfield pass "35 resources from nothing, matching stock's own cold-deploy count; the instance's markers verified via the AWS CLI; $GREEN_RECORD_FILES records in the local record store including untaggable types; replan empty; the instance's own shape (type/ami/block-device-count) matches stock's cold deploy, via the AWS CLI on both endpoints, marker tags never compared; $GREEN_TAGGED objects carry the estate tag"
fi
fi
CURRENT_STAGE=""
docker rm -f "$FLOCI_GREEN_NAME" >/dev/null 2>&1 || true

# ══════════════════════════════════════════════════════════════════════════
# PART D: RENAME (day2_rename, planned stage - live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
#
# The reduction (see header) removed every standalone root resource
# (aws_placement_group, aws_kms_key, aws_network_interface, random_string),
# so both legs of this rename are module calls. A `moved` block renames the
# whole module.vpc call; "choudoufu live-mv" renames the single taggable
# resource inside module.security_group (its two untaggable
# aws_security_group_rule siblings derive their identity from the security
# group's id and are expected to follow with no explicit action - the same
# way vpc-complete's own untaggable route-table associations do). The stock
# oracle for both runs on a copy of cold_deploy's own state, taken and
# PLANNED right after stage 1 - before choudoufu ever touches these shared
# objects, because migrate's marker writes land on the SAME live objects
# $EST manages throughout (there is no separate PLAIN copy in this script;
# -state=$WORK/cold.tfstate is a source for live-import, not a
# parallel apply), and re-planning against them after migrate would compare
# a plan that legitimately wants the marker tags gone, which has nothing to
# do with the rename.
#
# BREAK=1 exercises this stage's own break control instead of the real
# checks: renaming module.security_group's security group WITHOUT a moved
# block, which must make choudoufu propose destroying the old address and
# creating the new one - the opposite of every other assertion in this part.

CURRENT_STAGE=day2_rename
log "=== D-ORACLE. stock: the same two renames, through moved blocks, on cold_deploy's own state ==="
ORACLE_ROOT="$WORK/oracle"
mkdir -p "$ORACLE_ROOT"
cp -R "$SRC_MODULE"/. "$ORACLE_ROOT"
perl -0777 -pi -e 's/\nmodule "ec2_network_interface" \{.*?\nmodule "ec2_disabled" \{/\nmodule "ec2_disabled" \{/s' "$ORACLE_ROOT/examples/complete/main.tf"
perl -0777 -pi -e 's/\n################################################################################\n# EC2 Module - with ignore AMI changes\n################################################################################.*?\n################################################################################\n# Supporting Resources/\n################################################################################\n# Supporting Resources/s' "$ORACLE_ROOT/examples/complete/main.tf"
perl -0777 -pi -e 's/\nresource "aws_placement_group" "web" \{.*\z//s' "$ORACLE_ROOT/examples/complete/main.tf"
perl -0pi -e 's/instance_type          = "c5\.xlarge" # used to set core count below/instance_type          = "t3.micro"/' "$ORACLE_ROOT/examples/complete/main.tf"
perl -0pi -e 's/  placement_group        = aws_placement_group\.web\.id\n  # conflicts with placement_group\n  # placement_group_id = aws_placement_group\.web\.placement_group_id\n//' "$ORACLE_ROOT/examples/complete/main.tf"
perl -0pi -e 's/  # only one of these can be enabled at a time\n  hibernation = true\n  # enclave_options_enabled = true\n\n//' "$ORACLE_ROOT/examples/complete/main.tf"
perl -0pi -e 's/  user_data_base64            = base64encode\(local\.user_data\)\n  user_data_replace_on_change = false\n\n//' "$ORACLE_ROOT/examples/complete/main.tf"
perl -0pi -e 's/  cpu_options = \{\n    core_count       = 2\n    threads_per_core = 1\n  \}\n//' "$ORACLE_ROOT/examples/complete/main.tf"
perl -0pi -e 's/      kms_key_id = aws_kms_key\.this\.arn\n//' "$ORACLE_ROOT/examples/complete/main.tf"
perl -0pi -e 's/  user_data = <<-EOT\n    #!\/bin\/bash\n    echo "Hello Terraform!"\n  EOT\n\n//' "$ORACLE_ROOT/examples/complete/main.tf"
perl -0777 -pi -e 's/\n# EC2 T2 Unlimited\n.*\z//s' "$ORACLE_ROOT/examples/complete/outputs.tf"
perl -0777 -pi -e 's/    random = \{\n      source  = "hashicorp\/random"\n      version = ">= 3\.0"\n    \}\n//' "$ORACLE_ROOT/examples/complete/versions.tf"
perl -0pi -e 's/(provider "aws" \{\n  region = local\.region\n)\}/$1\n  access_key                   = "test"\n  secret_key                   = "test"\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  s3_use_path_style            = true\n}/' "$ORACLE_ROOT/examples/complete/main.tf"
grep -q 's3_use_path_style' "$ORACLE_ROOT/examples/complete/main.tf" || fail "the oracle's reconstruction of the reduction deltas did not match - the corpus pin has moved"
ORACLE_EST="$ORACLE_ROOT/examples/complete"
cp "$WORK/cold.tfstate" "$ORACLE_EST/terraform.tfstate"
sed -i.bak 's/module "vpc" {/module "vpc_renamed" {/' "$ORACLE_EST/main.tf"
sed -i.bak 's/module\.vpc\./module.vpc_renamed./g' "$ORACLE_EST/main.tf"
sed -i.bak 's/module "security_group" {/module "security_group_renamed" {/' "$ORACLE_EST/main.tf"
sed -i.bak 's/module\.security_group\./module.security_group_renamed./g' "$ORACLE_EST/main.tf"
rm -f "$ORACLE_EST/main.tf.bak"
cat >> "$ORACLE_EST/main.tf" <<'EOF'

moved {
  from = module.vpc
  to   = module.vpc_renamed
}

moved {
  from = module.security_group
  to   = module.security_group_renamed
}
EOF
( cd "$ORACLE_EST" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ORACLE_EST" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_rename stock oracle's reinit failed"; }
ORACLE_PLAN_OUT="$(cd "$ORACLE_EST" && terraform plan -input=false -no-color 2>&1)"; ORACLE_PLAN_RC=$?
[ "$ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -40; fail "the day2_rename stock oracle plan exited $ORACLE_PLAN_RC"; }
grep -qE '^  # .+ will be (destroyed|created)' <<< "$ORACLE_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock proposes a destroy or create for a rename carried entirely by moved blocks - the oracle itself is not zero-churn"; }
grep -qF 'Plan: 0 to add, 0 to change, 0 to destroy.' <<< "$ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -10; fail "stock's rename plan is not a true no-op"; }
log "  stock: zero churn on cold_deploy's own state - both moves report only their move, no attribute diff at all"
log "STAGE 1 (cold deploy): PASS"
log ""

# day2_remove's stock oracle (live/GAUNTLET.md #7, active): "Stock with the
# same block removed plans the same destroys." module.ec2_complete is the
# estate's only real module (module.ec2_disabled contributes zero declared
# instances of any block key, so it can never be a classifyOrphans
# ambiguity) and self-contained: it consumes module.vpc's and
# module.security_group's outputs but feeds nothing else in main.tf - only
# outputs.tf's 19 blocks reference it, all of them, so outputs.tf is
# truncated outright rather than edited output by output (the whole file's
# own header comment already narrows it to "EC2 Complete" outputs alone).
# A SEPARATE copy of cold_deploy's own state, reconstructed with the exact
# same reduction deltas D-ORACLE above uses, minus the two renames.
CURRENT_STAGE=day2_remove
log "=== D-REMOVE-ORACLE. stock: delete module.ec2_complete's block on cold_deploy's own state ==="
REMOVE_ORACLE_ROOT="$WORK/remove-oracle"
mkdir -p "$REMOVE_ORACLE_ROOT"
cp -R "$SRC_MODULE"/. "$REMOVE_ORACLE_ROOT"
perl -0777 -pi -e 's/\nmodule "ec2_network_interface" \{.*?\nmodule "ec2_disabled" \{/\nmodule "ec2_disabled" \{/s' "$REMOVE_ORACLE_ROOT/examples/complete/main.tf"
perl -0777 -pi -e 's/\n################################################################################\n# EC2 Module - with ignore AMI changes\n################################################################################.*?\n################################################################################\n# Supporting Resources/\n################################################################################\n# Supporting Resources/s' "$REMOVE_ORACLE_ROOT/examples/complete/main.tf"
perl -0777 -pi -e 's/\nresource "aws_placement_group" "web" \{.*\z//s' "$REMOVE_ORACLE_ROOT/examples/complete/main.tf"
perl -0pi -e 's/instance_type          = "c5\.xlarge" # used to set core count below/instance_type          = "t3.micro"/' "$REMOVE_ORACLE_ROOT/examples/complete/main.tf"
perl -0pi -e 's/  placement_group        = aws_placement_group\.web\.id\n  # conflicts with placement_group\n  # placement_group_id = aws_placement_group\.web\.placement_group_id\n//' "$REMOVE_ORACLE_ROOT/examples/complete/main.tf"
perl -0pi -e 's/  # only one of these can be enabled at a time\n  hibernation = true\n  # enclave_options_enabled = true\n\n//' "$REMOVE_ORACLE_ROOT/examples/complete/main.tf"
perl -0pi -e 's/  user_data_base64            = base64encode\(local\.user_data\)\n  user_data_replace_on_change = false\n\n//' "$REMOVE_ORACLE_ROOT/examples/complete/main.tf"
perl -0pi -e 's/  cpu_options = \{\n    core_count       = 2\n    threads_per_core = 1\n  \}\n//' "$REMOVE_ORACLE_ROOT/examples/complete/main.tf"
perl -0pi -e 's/      kms_key_id = aws_kms_key\.this\.arn\n//' "$REMOVE_ORACLE_ROOT/examples/complete/main.tf"
perl -0pi -e 's/  user_data = <<-EOT\n    #!\/bin\/bash\n    echo "Hello Terraform!"\n  EOT\n\n//' "$REMOVE_ORACLE_ROOT/examples/complete/main.tf"
perl -0777 -pi -e 's/\n# EC2 T2 Unlimited\n.*\z//s' "$REMOVE_ORACLE_ROOT/examples/complete/outputs.tf"
perl -0777 -pi -e 's/    random = \{\n      source  = "hashicorp\/random"\n      version = ">= 3\.0"\n    \}\n//' "$REMOVE_ORACLE_ROOT/examples/complete/versions.tf"
perl -0pi -e 's/(provider "aws" \{\n  region = local\.region\n)\}/$1\n  access_key                   = "test"\n  secret_key                   = "test"\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  s3_use_path_style            = true\n}/' "$REMOVE_ORACLE_ROOT/examples/complete/main.tf"
grep -q 's3_use_path_style' "$REMOVE_ORACLE_ROOT/examples/complete/main.tf" || fail "the day2_remove oracle's reconstruction of the reduction deltas did not match - the corpus pin has moved"
REMOVE_ORACLE_EST="$REMOVE_ORACLE_ROOT/examples/complete"
cp "$WORK/cold.tfstate" "$REMOVE_ORACLE_EST/terraform.tfstate"
perl -0777 -pi -e 's/module "ec2_complete" \{.*?\n\}\n\nmodule "ec2_disabled"/module "ec2_disabled"/s' "$REMOVE_ORACLE_EST/main.tf"
grep -q 'module "ec2_complete" {' "$REMOVE_ORACLE_EST/main.tf" && fail "removing module.ec2_complete's block from the day2_remove oracle copy did not match - the corpus example has moved"
: > "$REMOVE_ORACLE_EST/outputs.tf"
( cd "$REMOVE_ORACLE_EST" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$REMOVE_ORACLE_EST" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_remove stock oracle's reinit failed"; }
REMOVE_ORACLE_PLAN_OUT="$(cd "$REMOVE_ORACLE_EST" && terraform plan -input=false -no-color 2>&1)"; REMOVE_ORACLE_PLAN_RC=$?
[ "$REMOVE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_remove stock oracle plan exited $REMOVE_ORACLE_PLAN_RC"; }
grep -qF 'Plan: 0 to add, 0 to change, 10 to destroy.' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -15; fail "stock's remove plan does not propose exactly the ten module.ec2_complete destroys the header's own resource-shape table names"; }
log "  stock: exactly 10 destroys, all under module.ec2_complete (the header's own resource-shape table), nothing else, on the state cold_deploy produced"

# day2_replace's stock oracle (live/GAUNTLET.md #9, planned): "Stock's
# replace of the same resource leaves the same single object." A THIRD
# reconstruction of cold_deploy's own state, same reduction deltas as
# D-ORACLE/D-REMOVE-ORACLE above. Changes module.ec2_complete's `ami`
# argument from the data-source reference to a different literal AMI id
# also present in floci's fixed image catalog (ami-0abcdef1234567890, the
# amzn2 image; the al2023 image the data source resolves to is
# ami-0abcdef1234567891 - both real, both in the catalog, genuinely
# different images) - `ami` is ForceNew on aws_instance (AWS has no
# ModifyInstanceAttribute call that swaps an image under a running
# instance), so this forces a replace at the SAME declared address. PLAN
# ONLY, never applied - same convention as D-ORACLE and D-REMOVE-ORACLE:
# this copy shares floci's ACCOUNT with $EST, and corpus-sqs-basic's own
# day2_replace section found out the hard way (an early version of that
# section did apply here and a real run collaterally destroyed the object
# $EST's own later stages still depended on) that applying an oracle here
# would do the same to module.ec2_complete's real instance.
CURRENT_STAGE=day2_replace
log "=== F-ORACLE. stock: force-replace module.ec2_complete's instance via its ForceNew ami argument, on cold_deploy's own state ==="
REPLACE_ORACLE_ROOT="$WORK/replace-oracle"
mkdir -p "$REPLACE_ORACLE_ROOT"
cp -R "$SRC_MODULE"/. "$REPLACE_ORACLE_ROOT"
perl -0777 -pi -e 's/\nmodule "ec2_network_interface" \{.*?\nmodule "ec2_disabled" \{/\nmodule "ec2_disabled" \{/s' "$REPLACE_ORACLE_ROOT/examples/complete/main.tf"
perl -0777 -pi -e 's/\n################################################################################\n# EC2 Module - with ignore AMI changes\n################################################################################.*?\n################################################################################\n# Supporting Resources/\n################################################################################\n# Supporting Resources/s' "$REPLACE_ORACLE_ROOT/examples/complete/main.tf"
perl -0777 -pi -e 's/\nresource "aws_placement_group" "web" \{.*\z//s' "$REPLACE_ORACLE_ROOT/examples/complete/main.tf"
perl -0pi -e 's/instance_type          = "c5\.xlarge" # used to set core count below/instance_type          = "t3.micro"/' "$REPLACE_ORACLE_ROOT/examples/complete/main.tf"
perl -0pi -e 's/  placement_group        = aws_placement_group\.web\.id\n  # conflicts with placement_group\n  # placement_group_id = aws_placement_group\.web\.placement_group_id\n//' "$REPLACE_ORACLE_ROOT/examples/complete/main.tf"
perl -0pi -e 's/  # only one of these can be enabled at a time\n  hibernation = true\n  # enclave_options_enabled = true\n\n//' "$REPLACE_ORACLE_ROOT/examples/complete/main.tf"
perl -0pi -e 's/  user_data_base64            = base64encode\(local\.user_data\)\n  user_data_replace_on_change = false\n\n//' "$REPLACE_ORACLE_ROOT/examples/complete/main.tf"
perl -0pi -e 's/  cpu_options = \{\n    core_count       = 2\n    threads_per_core = 1\n  \}\n//' "$REPLACE_ORACLE_ROOT/examples/complete/main.tf"
perl -0pi -e 's/      kms_key_id = aws_kms_key\.this\.arn\n//' "$REPLACE_ORACLE_ROOT/examples/complete/main.tf"
perl -0pi -e 's/  user_data = <<-EOT\n    #!\/bin\/bash\n    echo "Hello Terraform!"\n  EOT\n\n//' "$REPLACE_ORACLE_ROOT/examples/complete/main.tf"
perl -0777 -pi -e 's/\n# EC2 T2 Unlimited\n.*\z//s' "$REPLACE_ORACLE_ROOT/examples/complete/outputs.tf"
perl -0777 -pi -e 's/    random = \{\n      source  = "hashicorp\/random"\n      version = ">= 3\.0"\n    \}\n//' "$REPLACE_ORACLE_ROOT/examples/complete/versions.tf"
perl -0pi -e 's/(provider "aws" \{\n  region = local\.region\n)\}/$1\n  access_key                   = "test"\n  secret_key                   = "test"\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  s3_use_path_style            = true\n}/' "$REPLACE_ORACLE_ROOT/examples/complete/main.tf"
grep -q 's3_use_path_style' "$REPLACE_ORACLE_ROOT/examples/complete/main.tf" || fail "the day2_replace oracle's reconstruction of the reduction deltas did not match - the corpus pin has moved"
REPLACE_ORACLE_EST="$REPLACE_ORACLE_ROOT/examples/complete"
cp "$WORK/cold.tfstate" "$REPLACE_ORACLE_EST/terraform.tfstate"
sed -i.bak 's/ami                    = data\.aws_ami\.amazon_linux\.id/ami                    = "ami-0abcdef1234567890"/' "$REPLACE_ORACLE_EST/main.tf"
rm -f "$REPLACE_ORACLE_EST/main.tf.bak"
grep -q 'ami-0abcdef1234567890' "$REPLACE_ORACLE_EST/main.tf" || fail "changing module.ec2_complete's ami argument in the replace-oracle copy did not match - the corpus pin has moved"
( cd "$REPLACE_ORACLE_EST" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$REPLACE_ORACLE_EST" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_replace stock oracle's reinit failed"; }
REPLACE_ORACLE_PLAN_OUT="$(cd "$REPLACE_ORACLE_EST" && terraform plan -input=false -no-color 2>&1)"; REPLACE_ORACLE_PLAN_RC=$?
[ "$REPLACE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_replace stock oracle plan exited $REPLACE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.ec2_complete\.aws_instance\.this\[0\] must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "stock does not propose replacing module.ec2_complete's instance when its ami argument changes"; }
grep -qE '^  # module\.ec2_complete\.aws_volume_attachment\.this\["/dev/sdf"\] must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "stock does not cascade the instance replace into the volume attachment (instance_id is ForceNew there too)"; }
grep -qE '^  # module\.ec2_complete\.aws_eip\.this\[0\] will be updated in-place' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "stock does not cascade the instance replace into the eip's instance association"; }
grep -qF 'Plan: 2 to add, 1 to change, 2 to destroy.' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -10; fail "stock's replace plan does not match the header's own three-resource cascade (instance + volume attachment replaced, eip updated in place)"; }
log "  stock: exactly one instance replace at the same declared address, cascading into the eip (updated in place) and the volume attachment (replaced, instance_id is ForceNew there too) - 2 to add, 1 to change, 2 to destroy, on the state cold_deploy produced - plan only, not applied (see above)"
CURRENT_STAGE=migrate

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

  # ══════════════════════════════════════════════════════════════════════
  # PART F: REPLACE (day2_replace, planned - live/GAUNTLET.md #9)
  # ══════════════════════════════════════════════════════════════════════
  #
  # Placed right after STAGE 5 and BEFORE PART D (day2_rename, below) on
  # purpose. module.ec2_complete is never touched by either rename PART D
  # performs (D0's own note there), so this section has no dependency on
  # PART D's outcome - and PART D's own moved-block rename of module.vpc
  # carries a real, already-documented, pre-existing choudoufu defect
  # (an untaggable derived child, aws_route/aws_route_table_association,
  # not always following its moved parent module - see PART D's own
  # BREAK-independent fail text below) that reproduced on this branch
  # during real runs, unrelated to this section's own changes. Running
  # PART F before PART D means day2_replace's own evidence is not held
  # hostage to that separate, already-tracked wall. $INSTANCE_ID,
  # captured back at STAGE 1, still names the live instance here. Its
  # `ami` argument changes from the data-source reference to a different
  # literal AMI id also present in floci's fixed image catalog (see the
  # header's THE ONBOARDING DELTA for how that catalog was already
  # discovered) - `ami` is ForceNew on aws_instance (AWS has no in-place
  # image swap for a running instance), so this forces a replace at the
  # SAME declared address. Two resources cascade from the SAME dependency
  # edges STAGE 1's resource-shape table already names: the eip's
  # `instance` attribute (a plain update) and the volume attachment's
  # `instance_id` (ForceNew there too, so it replaces alongside the
  # instance) - a real, three-resource shape, not a bug; F-ORACLE above
  # (right after cold_deploy) shows stock proposing the identical cascade
  # on its own copy of the same state.
  #
  # THE create_before_destroy SCOPE NOTE (see corpus-sqs-basic's own PART
  # F for the full reasoning, reproduced only in summary here):
  # OpenTofu core rejects a `lifecycle` block on a `module` call, and
  # patching the vendored terraform-aws-ec2-instance module's own resource
  # to add create_before_destroy would cross this corpus's
  # reduction-only convention, so this evidence pass exercises the
  # default destroy-then-create ordering instead. BREAK=replace
  # manufactures the create-before-destroy collision shape directly via
  # the AWS CLI, the same way corpus-sqs-basic's does.
  CURRENT_STAGE=day2_replace
  record_key() { printf '%s' "$1" | base64 | tr '+/' '-_' | tr -d '=\n'; }
  record_import_id() { jq -r '.identity.import_id' "$1"; }
  F_ADDR="module.ec2_complete.aws_instance.this[0]"
  F_RECORD="$EST/.tofu-records/tofu-records/$ESTATE/aws_instance/$(record_key "$F_ADDR")"

  log "=== F0. capture the live instance and its record ahead of the forced replace ==="
  [ -f "$F_RECORD" ] || fail "no local record file found for $F_ADDR ahead of day2_replace"
  F_OLD_IMPORT_ID="$(record_import_id "$F_RECORD")"
  [ "$F_OLD_IMPORT_ID" = "$INSTANCE_ID" ] || fail "the record for $F_ADDR names $F_OLD_IMPORT_ID ahead of day2_replace, not $INSTANCE_ID"
  F_OLD_ADDR_TAG="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$INSTANCE_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
  [ "$F_OLD_ADDR_TAG" = "module.ec2_complete.aws_instance.this:0" ] \
    || fail "$INSTANCE_ID does not carry tofu-address=module.ec2_complete.aws_instance.this:0 ahead of day2_replace"
  log "  $INSTANCE_ID, record import_id=$F_OLD_IMPORT_ID, tofu-address=$F_OLD_ADDR_TAG"

  if [ "${BREAK:-}" = "replace" ]; then
    log "=== F1 (BREAK=replace). manufacture the coexistence a skipped destroy would leave behind ==="
    # A second, distinct live instance carrying the SAME tofu-address and
    # tofu-slot as the one a genuine replace would destroy - the state
    # "skip the destroy half" of a create-before-destroy replace would
    # leave, produced directly via the AWS CLI (day2_crash, stage 10,
    # owns testing a real interrupted apply).
    BREAK_COLLISION_ID="$(awsl ec2 run-instances --image-id ami-0abcdef1234567891 --instance-type t3.micro --count 1 \
      --tag-specifications "ResourceType=instance,Tags=[{Key=tofu-estate,Value=$ESTATE},{Key=tofu-address,Value=module.ec2_complete.aws_instance.this:0},{Key=tofu-slot,Value=0}]" \
      --query 'Instances[0].InstanceId' --output text)"
    [ -n "$BREAK_COLLISION_ID" ] && [ "$BREAK_COLLISION_ID" != "None" ] || fail "BREAK=replace: could not launch the collision instance"
    BREAK_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; BREAK_PLAN_RC=$?
    awsl ec2 terminate-instances --instance-ids "$BREAK_COLLISION_ID" >/dev/null 2>&1 || true
    [ "$BREAK_PLAN_RC" -ne 0 ] \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -20; fail "BREAK=replace: the plan succeeded with two live instances claiming the same tofu-address/tofu-slot - it must report the collision, not propose nothing"; }
    grep -qF 'Two live resources claiming one slot' <<< "$BREAK_PLAN_OUT" \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -20; fail "BREAK=replace: the plan failed for a reason other than the slot collision - this stage's check is not load-bearing"; }
    log "  BREAK=replace: choudoufu correctly refused with a named collision (two live resources claiming one slot) rather than silently proposing nothing - the Break text's own outcome"
  else
    log "=== F1. choudoufu: change the ForceNew ami argument, forcing a replace at the same declared address ==="
    sed -i.bak 's/ami                    = data\.aws_ami\.amazon_linux\.id/ami                    = "ami-0abcdef1234567890"/' "$EST/main.tf"
    rm -f "$EST/main.tf.bak"
    grep -q 'ami-0abcdef1234567890' "$EST/main.tf" || fail "changing module.ec2_complete's ami argument did not match - the corpus pin has moved"

    F_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; F_PLAN_RC=$?
    [ "$F_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_PLAN_OUT" | tail -40; fail "the day2_replace plan exited $F_PLAN_RC"; }
    grep -qE '^  # module\.ec2_complete\.aws_instance\.this\[0\] must be replaced' <<< "$F_PLAN_OUT" \
      || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not propose replacing module.ec2_complete's instance when its ForceNew ami argument changes"; }
    grep -qE '~ +ami +=.+forces replacement' <<< "$F_PLAN_OUT" \
      || { printf '%s\n' "$F_PLAN_OUT"; fail "the plan does not mark ami as forcing replacement"; }
    grep -qE '^  # module\.ec2_complete\.aws_volume_attachment\.this\["/dev/sdf"\] must be replaced' <<< "$F_PLAN_OUT" \
      || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not cascade the instance replace into the volume attachment"; }
    grep -qE '^  # module\.ec2_complete\.aws_eip\.this\[0\] will be updated in-place' <<< "$F_PLAN_OUT" \
      || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not cascade the instance replace into the eip's instance association"; }
    grep -qF 'Plan: 2 to add, 1 to change, 2 to destroy.' <<< "$F_PLAN_OUT" \
      || { printf '%s\n' "$F_PLAN_OUT" | tail -10; fail "the day2_replace plan does not match F-ORACLE's own three-resource cascade"; }
    log "  choudoufu: exactly one instance replace at the same declared address, cascading into the eip (in-place) and volume attachment (replaced) - matches F-ORACLE's own plan shape"

    F_APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; F_APPLY_RC=$?
    [ "$F_APPLY_RC" -eq 0 ] || { printf '%s\n' "$F_APPLY_OUT" | tail -40; fail "the day2_replace apply exited $F_APPLY_RC"; }
    grep -qE 'Resources: 2 added, 1 changed, 2 destroyed' <<< "$F_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$F_APPLY_OUT"; fail "the day2_replace apply did not match the planned 2 added, 1 changed, 2 destroyed"; }

    F_OLD_STATE="$(awsl ec2 describe-instances --instance-ids "$INSTANCE_ID" --query "Reservations[0].Instances[0].State.Name" --output text 2>&1)"
    [ "$F_OLD_STATE" = "terminated" ] || fail "$INSTANCE_ID is not terminated after the replace (state=$F_OLD_STATE) - the old object was orphaned, not destroyed"
    log "  $INSTANCE_ID terminated - confirmed via the AWS CLI, not through choudoufu's own report"

    F_NEW_ID="$(cd "$EST" && "$TOFU" output -raw ec2_complete_id 2>/dev/null || true)"
    [ -n "$F_NEW_ID" ] && [ "$F_NEW_ID" != "$INSTANCE_ID" ] || fail "could not read a new, different instance id from choudoufu output after the replace"
    F_NEW_ADDR_TAG="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$F_NEW_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
    [ "$F_NEW_ADDR_TAG" = "module.ec2_complete.aws_instance.this:0" ] \
      || fail "$F_NEW_ID carries tofu-address=$F_NEW_ADDR_TAG after the replace, not module.ec2_complete.aws_instance.this:0 - the marker did not move onto the new object"
    log "  $F_NEW_ID (the new object) carries tofu-address=$F_NEW_ADDR_TAG - the marker moved onto the new object, read via the AWS CLI"

    # THE RECORD STORE, asserted by value (HANDOFF's safety rule; the
    # #398-guard shape: a stale record still naming the destroyed instance
    # would be exactly the wrong-marker failure that outranks a missing
    # one). The local record file at the SAME address must now hold the
    # NEW instance's id, not the one captured in F0.
    F_NEW_IMPORT_ID="$(record_import_id "$F_RECORD")"
    [ "$F_NEW_IMPORT_ID" = "$F_NEW_ID" ] \
      || fail "the record for $F_ADDR names $F_NEW_IMPORT_ID after the replace, not the new object $F_NEW_ID - a stale record still claiming the destroyed instance, the #398-guard shape"
    [ "$F_NEW_IMPORT_ID" != "$F_OLD_IMPORT_ID" ] \
      || fail "sanity: the record's import_id at $F_ADDR did not change at all across the replace"
    log "  record store: import_id $F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID at the same key ($F_ADDR) - read directly off the local record store file, not through choudoufu's own report"

    log "=== F2. one more plan: config and reality agree, no marker collision ==="
    F_FINAL_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; F_FINAL_PLAN_RC=$?
    [ "$F_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_FINAL_PLAN_OUT" | tail -40; fail "the post-replace plan exited $F_FINAL_PLAN_RC"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$F_FINAL_PLAN_OUT" \
      || { grep -E '^  #' <<< "$F_FINAL_PLAN_OUT"; fail "the post-replace plan proposes a resource change"; }
    log "  No changes. The replace is complete and invisible to the next plan - no marker collision."

    INSTANCE_ID="$F_NEW_ID"
    gauntlet_stage day2_replace pass "choudoufu: changing module.ec2_complete's ForceNew ami argument proposed exactly one instance replace at the same declared address, cascading into the eip (updated in-place) and the volume attachment (also replaced, instance_id is ForceNew there too) - 2 to add, 1 to change, 2 to destroy, matching F-ORACLE's own plan shape; applied cleanly; the old instance is confirmed terminated and the new instance carries the marker, both via the AWS CLI; the local record store's record at the same address now names the new instance's id, not the terminated one ($F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID); the next plan proposes no resource action; BREAK=replace confirms a manufactured marker collision is reported loudly (\"Two live resources claiming one slot\") rather than silently proposed as nothing. Scope note: this exercises OpenTofu's default destroy-then-create ordering, not the create_before_destroy variant the stage's Title names - see this section's own header comment and corpus-sqs-basic's matching one."
  fi
  CURRENT_STAGE=""

CURRENT_STAGE=day2_rename
log "=== D0. capture the live ids a rename must not disturb ==="
VPC_ID_D="$(awsl ec2 describe-vpcs --filters '[{"Name":"tag:tofu-address","Values":["module.vpc.aws_vpc.this:0"]}]' --query "Vpcs[0].VpcId" --output text)"
[ -n "$VPC_ID_D" ] && [ "$VPC_ID_D" != "None" ] || fail "no live vpc found by its tofu-address marker"
SG_ID_D="$(awsl ec2 describe-security-groups --filters '[{"Name":"tag:tofu-address","Values":["module.security_group.aws_security_group.this_name_prefix:0"]}]' --query "SecurityGroups[0].GroupId" --output text)"
[ -n "$SG_ID_D" ] && [ "$SG_ID_D" != "None" ] || fail "no live security group found by its tofu-address marker"
log "  $VPC_ID_D (module.vpc), $SG_ID_D (module.security_group)"

if [ "${BREAK:-}" = "1" ]; then
  log "=== D1 (BREAK=1). rename module.security_group -> module.security_group_renamed WITHOUT a moved block ==="
  sed -i.bak 's/module "security_group" {/module "security_group_renamed" {/' "$EST/main.tf"
  sed -i.bak 's/module\.security_group\./module.security_group_renamed./g' "$EST/main.tf"
  rm -f "$EST/main.tf.bak"
  ( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the BREAK=1 rename's reinit failed"; }
  BREAK_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; BREAK_PLAN_RC=$?
  [ "$BREAK_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -30; fail "the BREAK=1 rename-without-moved plan exited $BREAK_PLAN_RC"; }
  grep -qE '^  # module\.security_group\.aws_security_group\.this_name_prefix\[0\] will be destroyed' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=1: renaming without a moved block did not propose destroying module.security_group.aws_security_group.this_name_prefix[0] - this stage's check is not load-bearing"; }
  grep -qE '^  # module\.security_group_renamed\.aws_security_group\.this_name_prefix\[0\] will be created' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=1: renaming without a moved block did not propose creating module.security_group_renamed.aws_security_group.this_name_prefix[0] - this stage's check is not load-bearing"; }
  log "  BREAK=1: correctly proposes destroying the old security group address and creating the new one - the moved-block and live-mv checks below are skipped"
else
  log "=== D1. choudoufu, moved block: module.vpc -> module.vpc_renamed ==="
  sed -i.bak 's/module "vpc" {/module "vpc_renamed" {/' "$EST/main.tf"
  sed -i.bak 's/module\.vpc\./module.vpc_renamed./g' "$EST/main.tf"
  rm -f "$EST/main.tf.bak"
  cat >> "$EST/main.tf" <<'EOF'

moved {
  from = module.vpc
  to   = module.vpc_renamed
}
EOF
  ( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the moved-block rename's reinit failed"; }
  MOVED_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; MOVED_PLAN_RC=$?
  [ "$MOVED_PLAN_RC" -eq 0 ] || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -40; fail "the moved-block rename plan exited $MOVED_PLAN_RC"; }
  grep -qE '^  # .+ will be (destroyed|created)' <<< "$MOVED_PLAN_OUT" \
    && { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu defect: the moved-block rename of module.vpc proposes a create/destroy for one of its untaggable derived children (aws_route/aws_route_table_association) instead of matching them structurally under the parent's new address - not zero churn. The renamed taggable resources ARE relocated correctly ('will be updated in-place'); stock's native moved-block handling relocates every child cleanly. The gap is choudoufu-specific: an untaggable/derived child's identity resolution does not follow a moved parent module the way a marker-carrying resource's does. Not fixed in this unit, scope is the day2_rename stage activation itself (see corpus-vpc-complete's own day2_rename detail for the first occurrence of this wall)."; }
  N_CHANGED_D1="$(grep -cE '^  # .+ will be updated in-place' <<< "$MOVED_PLAN_OUT" || true)"
  [ "$N_CHANGED_D1" -ge 1 ] || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -20; fail "the moved-block rename plan proposes no in-place changes at all - nothing to rewrite the markers"; }
  grep -qF "Plan: 0 to add, $N_CHANGED_D1 to change, 0 to destroy." <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -10; fail "the moved-block rename plan's summary does not match its own $N_CHANGED_D1 in-place changes"; }
  grep -qE '~ +"tofu-address" = "module\.vpc\.aws_vpc\.this:0" -> "module\.vpc_renamed\.aws_vpc\.this:0"' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT"; fail "the moved-block plan does not show the vpc's tofu-address marker being rewritten from the old address to the new one"; }
  log "  choudoufu: zero churn, $N_CHANGED_D1 in-place tags update(s) - the marker rewrite the moved block completes"

  MOVED_APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; MOVED_APPLY_RC=$?
  [ "$MOVED_APPLY_RC" -eq 0 ] || { printf '%s\n' "$MOVED_APPLY_OUT" | tail -40; fail "the moved-block rename apply exited $MOVED_APPLY_RC"; }
  grep -qE "Resources: 0 added, $N_CHANGED_D1 changed, 0 destroyed" <<< "$MOVED_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$MOVED_APPLY_OUT"; fail "the moved-block rename apply did not change exactly $N_CHANGED_D1 resources"; }

  VPC_ID_D_AFTER="$(awsl ec2 describe-vpcs --vpc-ids "$VPC_ID_D" --query "Vpcs[0].VpcId" --output text 2>/dev/null || true)"
  [ "$VPC_ID_D_AFTER" = "$VPC_ID_D" ] || fail "the vpc's id changed across the rename ($VPC_ID_D -> $VPC_ID_D_AFTER) - it was destroyed and recreated, not renamed"
  VPC_ADDR_D_AFTER="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$VPC_ID_D" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
  [ "$VPC_ADDR_D_AFTER" = "module.vpc_renamed.aws_vpc.this:0" ] \
    || fail "the vpc carries tofu-address=$VPC_ADDR_D_AFTER after the rename, not module.vpc_renamed.aws_vpc.this:0"
  log "  $VPC_ID_D unchanged, tofu-address now module.vpc_renamed.aws_vpc.this:0 - read via the AWS CLI"

  log "=== D2. choudoufu, live-mv: module.security_group -> module.security_group_renamed, no moved block at all ==="
  sed -i.bak 's/module "security_group" {/module "security_group_renamed" {/' "$EST/main.tf"
  sed -i.bak 's/module\.security_group\./module.security_group_renamed./g' "$EST/main.tf"
  rm -f "$EST/main.tf.bak"
  ( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the live-mv rename's reinit failed"; }
  MV_OUT="$(cd "$EST" && "$TOFU" live-mv -estate="$ESTATE" 'module.security_group.aws_security_group.this_name_prefix[0]' 'module.security_group_renamed.aws_security_group.this_name_prefix[0]' 2>&1)"; MV_RC=$?
  [ "$MV_RC" -eq 0 ] || { printf '%s\n' "$MV_OUT" | tail -30; fail "choudoufu live-mv exited $MV_RC"; }
  grep -qF 'Rewrote the ownership marker on one live resource. This was a cloud write.' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report a real write"; }
  grep -qF '"module.security_group.aws_security_group.this_name_prefix:0" -> "module.security_group_renamed.aws_security_group.this_name_prefix:0"' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report rewriting the tofu-address marker from the old address to the new one"; }
  log "  live-mv: $(grep -F 'live ID' <<< "$MV_OUT")"

  SG_ID_D_AFTER="$(awsl ec2 describe-security-groups --group-ids "$SG_ID_D" --query "SecurityGroups[0].GroupId" --output text 2>/dev/null || true)"
  [ "$SG_ID_D_AFTER" = "$SG_ID_D" ] || fail "the security group's id changed across live-mv ($SG_ID_D -> $SG_ID_D_AFTER) - it was destroyed and recreated, not renamed"
  SG_ADDR_D_AFTER="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$SG_ID_D" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
  [ "$SG_ADDR_D_AFTER" = "module.security_group_renamed.aws_security_group.this_name_prefix:0" ] \
    || fail "the security group carries tofu-address=$SG_ADDR_D_AFTER after live-mv, not module.security_group_renamed.aws_security_group.this_name_prefix:0"
  log "  $SG_ID_D unchanged, tofu-address now module.security_group_renamed.aws_security_group.this_name_prefix:0 - read via the AWS CLI"

  log "=== D3. one more plan: config and markers agree on both renames, nothing proposed ==="
  FINAL_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; FINAL_PLAN_RC=$?
  [ "$FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$FINAL_PLAN_OUT" | tail -40; fail "the post-rename plan exited $FINAL_PLAN_RC"; }
  grep -qF "No changes. Your infrastructure matches the configuration." <<< "$FINAL_PLAN_OUT" \
    || { grep -E '^  #' <<< "$FINAL_PLAN_OUT"; fail "the post-rename plan is not empty - the two untaggable aws_security_group_rule siblings under module.security_group_renamed may not have followed their live-mv'd parent (see this stage's own moved-block finding for the related engine gap)"; }
  log "  No changes. Both renames are complete and invisible to the next plan - including the two untaggable security-group rules, which followed their live-mv'd parent with no explicit action."

  gauntlet_stage day2_rename pass "moved block: module.vpc renamed with zero churn (0 add, $N_CHANGED_D1 change, 0 destroy), marker rewritten in place; live-mv: module.security_group's security group renamed with zero churn, its two untaggable rules followed for free; stock oracle over the same two-object rename on cold_deploy's own state also shows zero churn (0 add, 0 change, 0 destroy); both live ids unchanged, read via the AWS CLI"
  # ══════════════════════════════════════════════════════════════════════
  # PART E: REMOVE A BLOCK (day2_remove, active - live/GAUNTLET.md #7)
  # ══════════════════════════════════════════════════════════════════════
  #
  # Starts from Part D's real, completed state (module.vpc_renamed and
  # module.security_group_renamed are both bound and converged;
  # module.ec2_complete was never touched by either rename). It is removed
  # here in full: self-contained (consumes module.vpc/module.security_group
  # outputs, feeds nothing else in main.tf), and module.ec2_disabled
  # declares zero instances of any of its block keys, so it can never be a
  # classifyOrphans ambiguity. outputs.tf's 19 blocks reference
  # module.ec2_complete exclusively (the header's own reduction already
  # narrowed the file to "EC2 Complete" outputs alone), so it is truncated
  # outright rather than edited output by output.
  CURRENT_STAGE=day2_remove
  log "=== E0. delete module.ec2_complete's block ==="
  perl -0777 -pi -e 's/module "ec2_complete" \{.*?\n\}\n\nmodule "ec2_disabled"/module "ec2_disabled"/s' "$EST/main.tf"
  grep -q 'module "ec2_complete" {' "$EST/main.tf" && fail "removing module.ec2_complete's block did not match - the config has moved"
  : > "$EST/outputs.tf"
  ( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the day2_remove reinit failed"; }
  REMOVE_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; REMOVE_PLAN_RC=$?
  [ "$REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -40; fail "the day2_remove plan exited $REMOVE_PLAN_RC"; }
  if grep -q 'is unclaimed, so this may be the same resource under a new instance key' <<< "$REMOVE_PLAN_OUT"; then
    printf '%s\n' "$REMOVE_PLAN_OUT" | tail -40
    fail "choudoufu withheld a destroy of module.ec2_complete's resources as a possible rename (discovery.go's classifyOrphans) - this is the honest wall issue #358 names, not a pass"
  fi
  CHOUDOUFU_REMOVE_N="$(grep -cE '^  # module\.ec2_complete\..+ will be destroyed' <<< "$REMOVE_PLAN_OUT" || true)"
  if [ "$CHOUDOUFU_REMOVE_N" -lt 10 ]; then
    # A REAL, DOCUMENTED gap, not a surprise (see the day2_remove finding
    # already recorded for [gauntlet:corpus-dynamodb-table-basic/day2_remove]
    # and [gauntlet:corpus-autoscaling-complete/day2_remove]): a type
    # admitted by the provider's own identity schema rather than by the
    # generated admission table is invisible to the estate-wide destroy
    # sweep (live/LIMITATIONS.md, "Resource type has no orphan recovery").
    printf '%s\n' "$REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'
    log "  choudoufu proposes $CHOUDOUFU_REMOVE_N of the oracle's 10 destroys under module.ec2_complete - a real gap, not this stage's own load-bearing check failing"
    gauntlet_stage day2_remove fail "choudoufu's remove plan destroys only $CHOUDOUFU_REMOVE_N of module.ec2_complete's 10 resources; stock oracle on cold_deploy's own state (D-REMOVE-ORACLE) proposes all 10 for the same module (0 add, 0 change, 10 destroy). choudoufu has strictly less destroy coverage than stock here - the missing address(es) are left live and orphaned, most likely a type admitted by the provider's identity schema rather than the generated admission table (live/LIMITATIONS.md, \"Resource type has no orphan recovery\"), the same class corpus-dynamodb-table-basic (aws_dynamodb_resource_policy) and corpus-autoscaling-complete (most likely aws_autoscaling_group) already hit. Not fixed in this script-only pass; see live/gauntlet/logs/corpus-ec2-instance-complete.log for the exact plan diff"
  else
    grep -qF 'Plan: 0 to add, 0 to change, 10 to destroy.' <<< "$REMOVE_PLAN_OUT" \
      || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -10; fail "choudoufu's remove plan touches something other than module.ec2_complete's own 10 resources"; }
    log "  choudoufu: exactly 10 destroys under module.ec2_complete, matching the stock oracle, nothing else"

    TAGGED_BEFORE="$(awsl resourcegroupstaggingapi get-resources --tag-filters "Key=tofu-estate,Values=$ESTATE" --query 'length(ResourceTagMappingList)' --output text)"

    REMOVE_APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; REMOVE_APPLY_RC=$?
    [ "$REMOVE_APPLY_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_APPLY_OUT" | tail -40; fail "the day2_remove apply exited $REMOVE_APPLY_RC"; }
    grep -qE 'Resources: 0 added, 0 changed, 10 destroyed' <<< "$REMOVE_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$REMOVE_APPLY_OUT"; fail "the day2_remove apply was not exactly 10 destroys"; }

    if E_STILL="$(awsl ec2 describe-instances --instance-ids "$INSTANCE_ID" --query "Reservations[0].Instances[0].State.Name" --output text 2>&1)"; then
      [ "$E_STILL" = "terminated" ] || { echo "$E_STILL"; fail "$INSTANCE_ID is not terminated after the destroy (state=$E_STILL) - it was orphaned, not destroyed"; }
    fi
    log "  $INSTANCE_ID terminated - confirmed via the AWS CLI, not through choudoufu's own report"

    TAGGED_AFTER="$(awsl resourcegroupstaggingapi get-resources --tag-filters "Key=tofu-estate,Values=$ESTATE" --query 'length(ResourceTagMappingList)' --output text)"
    [ "$TAGGED_AFTER" -lt "$TAGGED_BEFORE" ] \
      || fail "the tagged object count did not drop at all across the destroy ($TAGGED_BEFORE -> $TAGGED_AFTER)"
    log "  tagged object count $TAGGED_BEFORE -> $TAGGED_AFTER - confirmed via the AWS CLI"

    log "=== E2. one more plan: config and reality agree, nothing left to propose ==="
    E_FINAL_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; E_FINAL_PLAN_RC=$?
    [ "$E_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$E_FINAL_PLAN_OUT" | tail -40; fail "the post-remove plan exited $E_FINAL_PLAN_RC"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$E_FINAL_PLAN_OUT" \
      || { grep -E '^  #' <<< "$E_FINAL_PLAN_OUT"; fail "the post-remove plan is not empty"; }
    log "  No changes. The removal is complete and invisible to the next plan."

    gauntlet_stage day2_remove pass "choudoufu: deleting module.ec2_complete's block proposed exactly 10 destroys (0 add, 0 change, 10 destroy), matching the stock oracle's own count and applied cleanly; the instance is confirmed terminated and the tagged object count dropped, both via the AWS CLI, not through choudoufu's own report; the next plan proposes no resource action; stock oracle on cold_deploy's own state (D-REMOVE-ORACLE) also proposes exactly 10 destroys for the same module"
  fi
  CURRENT_STAGE=""
fi
CURRENT_STAGE=""

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
log "floci defects found, fixed, merged, and repinned (lex00/floci#114, #115), onboarding deltas removed."
