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
# every plan this estate runs sees exactly 8 foreign objects: the root
# volume plus those 7 default-account objects (the default security group's
# single default egress rule - real AWS's default security group carries
# exactly one default outbound rule, allow-all IPv4, and no default inbound
# rule at all - not the two floci previously, wrongly, reported before
# lex00/floci#136's SecurityGroupRuleId-revoke fix landed; re-measured after
# that repin, this count moved 9 -> 8 and the assertion below moved with
# it). STAGE 3 asserts this COUNT by value rather than requiring "none",
# because "none" would be false for any real account this module was ever
# pointed at.
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
#                     resource change; the default plan reports that it left
#                     the account-inventory question unasked and a second
#                     plan under TOFU_LIVE_COLLECT_UNCLAIMED=1 reports
#                     exactly 8 foreign objects (see STAGE 3's own comment
#                     for why that is now two plans); the instance's
#                     tofu-address is re-checked against EC2 directly.
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
#   BREAK_COUNT  day2_count's own break control (PART C), independent of
#                BREAK: after the real scale-down plan, assert the WRONG
#                instance (count_test[0] rather than count_test[1]) was the
#                one destroyed - the Break text in tools/gauntlet/stages.go
#                for day2_count, verbatim ("Expect a different instance to
#                be destroyed; the assertion must fail"). A BREAK_COUNT=1
#                run must print `GAUNTLET stage=day2_count verdict=fail`
#                and exit non-zero.
#   BREAK_APPROVAL
#                plan_approval's own negative control (PART P), independent
#                of BREAK and BREAK_COUNT: after the module.ec2_complete
#                instance's Example tag has moved out of band, assert the
#                saved plan file APPLIES cleanly - the Break text in
#                tools/gauntlet/stages.go for plan_approval, verbatim
#                ("Apply the planfile after a mutation and expect success;
#                the run must refuse") - so this assertion has to fail. It
#                is the only break control under which PART P runs at all;
#                the others deliberately leave the estate somewhere PART P
#                does not describe, and it reports no verdict there.
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
gauntlet_begin_stage cold_deploy
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
# THIS SECTION USED TO select the EIP with a server-side
# `Name=instance-id,Values=$INSTANCE_ID` filter and take
# `Addresses[0]`, on the unstated assumption that this account has
# exactly one EIP so the pick is safe regardless. THAT ASSUMPTION WAS
# NEVER TESTED, and it is unsafe to make: floci's DescribeAddresses
# ignores its entire `--filters` parameter - not one missing filter
# name, the whole parameter - and returns EVERY address in the account
# no matter what is passed, confirmed directly against the API (two
# allocated EIPs, one associated to a running instance and one not;
# `instance-id`, `tag:tofu-address` and `public-ip` filters ALL returned
# both addresses unchanged regardless of value, including a value
# guaranteed not to match anything) - lex00/floci#150, and this repo's
# own live/e2e/run.sh already works around the identical gap for this
# same API call ("floci-gaps #8").
#
# Hardened the same way: list every address, unfiltered, and match
# InstanceId EXACTLY in bash. Zero matches or more than one is a hard,
# loud fail here, never an `Addresses[0]` pick over an unfiltered list.
EIP_ROWS="$(awsl ec2 describe-addresses --query 'Addresses[].[AllocationId,InstanceId]' --output text)"
EIP_MATCHES=()
while IFS=$'\t' read -r alloc_id assoc_instance; do
  [ "$assoc_instance" = "$INSTANCE_ID" ] && EIP_MATCHES+=("$alloc_id")
done <<< "$EIP_ROWS"
if [ "${#EIP_MATCHES[@]}" -ne 1 ]; then
  printf '%s\n' "$EIP_ROWS" | sed 's/^/    /' >&2
  fail "expected exactly one EIP associated with instance $INSTANCE_ID (client-side match over an unfiltered describe-addresses - --filters is a floci no-op for this call, lex00/floci#150), found ${#EIP_MATCHES[@]}; full candidate list on stderr above"
fi
EIP_ALLOC_ID="${EIP_MATCHES[0]}"
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
gauntlet_begin_stage greenfield
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
# strict { no_source_create = "create" }: found necessary re-verifying this
# stage after main's CHOUDOUFU_NODE_RESOLVE default flip (845e7a0d9d,
# 2026-08-25) - a genuinely cold apply now refuses config-identified
# instances whose identity value belongs to a sibling that does not exist
# yet either (#365 ruling 4's default refusal of that ambiguity), and a
# greenfield apply is the one case an operator KNOWS it is a real create.
# Same fix, same precedent as corpus-alb-complete's own 898091b8f2.
perl -0777 -pi -e 's/(\n  provider_meta "aws" \{\n    user_agent = \[\n      "github\.com\/terraform-aws-modules\/terraform-aws-ec2-instance"\n    \]\n  \}\n)\}/$1\n  live {\n    estate = "'"$GREEN_ESTATE"'"\n\n    record_store "local" {\n      path = ".tofu-records"\n    }\n\n    strict {\n      no_source_create = "create"\n    }\n  }\n}/s' "$GREEN/versions.tf"
grep -q "estate = \"$GREEN_ESTATE\"" "$GREEN/versions.tf" || fail "the greenfield live-block delta did not match versions.tf - the corpus pin has moved"
log "  DELTA  live block (record_store, evidence for #364 A2) added on top of \$EST's own reduction + onboarding deltas"

log "=== PART GREENFIELD: 1. choudoufu apply from nothing, no migration, no state file ever existing ==="
( cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the greenfield init failed"; }
GREEN_APPLY_OUT="$(cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"
if [ $? -ne 0 ]; then
  printf '%s\n' "$GREEN_APPLY_OUT" | grep -E '^Error' -A 6 | head -200
  gauntlet_stage greenfield fail "the greenfield apply failed - see live/gauntlet/logs/corpus-ec2-instance-complete.log for the full diagnostic; cold_deploy/migrate/test_plan/test_apply/drift_reconverge/day2_rename/day2_remove for this estate are unaffected (checked earlier/later in the same run)"
  gauntlet_end_stage
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
GREEN_RECORD_FILES="$(gauntlet_record_count "$GREEN/.tofu-records/tofu-records")"
[ "$GREEN_RECORD_FILES" -gt 0 ] || fail "expected at least one record under the local record store after the greenfield apply, found none"
log "  $GREEN_RECORD_FILES records persisted, read directly off the local record store"

log "=== PART GREENFIELD: 4. the next plan proposes nothing (besides the same 8 foreign default-account objects STAGE 3 already names) ==="
GREEN_PLAN_OUT="$(cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; GREEN_PLAN_RC=$?
[ "$GREEN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$GREEN_PLAN_OUT" | tail -30; fail "the greenfield replan exited $GREEN_PLAN_RC"; }
if ! grep -qF "No changes. Your infrastructure matches the configuration." <<< "$GREEN_PLAN_OUT"; then
  NONEMPTY_ITEMS="$(grep -E '^  # .+ will be' <<< "$GREEN_PLAN_OUT" | sed 's/^  # //' | tr '\n' '; ')"
  log "  the replan is NOT empty: $NONEMPTY_ITEMS"
  gauntlet_stage greenfield fail "the greenfield replan proposes real resource action on objects the SAME apply just created (no other run touched this namespace in between): $NONEMPTY_ITEMS. A create proposed for something that already exists is the wrong-marker-shaped failure HANDOFF ranks above a missing one, not a safe fallback; not fixed in this script-only pass. 35 objects were created and the instance's own marker verified fine (see the earlier PART GREENFIELD steps in the same run), so this is narrower than a total apply failure - the specific objects named above are the gap."
  gauntlet_end_stage
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
gauntlet_end_stage
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

gauntlet_begin_stage day2_rename
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
gauntlet_begin_stage day2_remove
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
gauntlet_begin_stage day2_replace
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
gauntlet_begin_stage migrate

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE - choudoufu live-import against the cold state
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage migrate
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
gauntlet_begin_stage test_plan
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
# three subnets, that security group's single default egress rule - real
# AWS's default security group carries exactly one default outbound rule
# and no default inbound rule) - 8 real, expected foreign objects, not
# "none". Asserting this by value is what distinguishes "this estate's
# known foreign shape" from "something this estate owns was missed by the
# sweep".
#
# WHICH PLAN IS ASKED, AND WHY THIS IS NOW TWO PLANS. Until 2026-08-30 an
# ordinary stateless plan always asked the account-inventory question
# ("what is in my account this estate does not know about"), so the count
# above fell out of the plan this stage already ran.
# the CollectUnclaimed ruling (#604)
# (09d180f921, "a steady-state plan stops enumerating the whole admission
# table") makes that question opt-in: it costs a per-type enumeration of
# every admitted type the ARN join cannot place, and the default is now off
# for anything but "choudoufu plan -adoption-only". An ordinary plan
# therefore reports "Foreign resources: nothing was swept" - which the view
# deliberately keeps distinct from "none", precisely so a narrowed run
# cannot be read as an answer (internal/command/views/live_plan.go's
# Foreign, "swept and found none and nothing was swept are different
# answers").
#
# That is a ruled behaviour change, not a regression, so the ORACLE is what
# moves here, not the product: this stage asserts BOTH answers rather than
# dropping the count it was written for. The default plan must say it did
# not ask, and a second plan that DOES ask - through
# TOFU_LIVE_COLLECT_UNCLAIMED=1, the switch
# internal/command/live_collect_unclaimed.go exists to provide for exactly
# this - must still find all 8. Weakening this to the
# "(none|nothing was swept)" pattern the estates with no foreign shape use
# would have made the assertion unfailable on the one estate that has a
# foreign shape worth counting.
grep -qE '^Foreign resources: nothing was swept' <<< "$PLAN_OUT" \
  || { grep -E '^Foreign resources:' <<< "$PLAN_OUT"; fail "the default plan does not report that it left the account-inventory question unasked - the CollectUnclaimed ruling (#604) says a run that did not ask must say so rather than imply there is nothing"; }
log "  default plan: $(grep -E '^Foreign resources:' <<< "$PLAN_OUT")"
SWEEP_PLAN_OUT="$(cd "$EST" && TOFU_LIVE_COLLECT_UNCLAIMED=1 "$TOFU" live-plan -input=false -no-color 2>&1)"; SWEEP_PLAN_RC=$?
[ "$SWEEP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$SWEEP_PLAN_OUT" | tail -60; fail "the account-inventory plan (TOFU_LIVE_COLLECT_UNCLAIMED=1) exited $SWEEP_PLAN_RC"; }
[ ! -f "$EST/terraform.tfstate" ] || fail "the account-inventory plan wrote a state file"
grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$SWEEP_PLAN_OUT" \
  && { grep -E '^  # .+ will be' <<< "$SWEEP_PLAN_OUT"; fail "the account-inventory plan proposes a resource change the default plan did not"; }
grep -qE "^Foreign resources: 8 live resources not owned by estate $ESTATE" <<< "$SWEEP_PLAN_OUT" \
  || { grep -E '^Foreign resources:' <<< "$SWEEP_PLAN_OUT"; fail "expected exactly 8 foreign objects (the instance's own root volume + floci's default-VPC bootstrap) from the plan that asked; the corpus pin, floci's default-account shape, or a real gap has moved"; }
log "  no resource change proposed by either plan; the plan that asked found exactly 8 foreign objects (root volume + default-VPC bootstrap, both expected)"

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
gauntlet_stage test_plan pass "no resource change proposed by either plan; the default plan reports \"nothing was swept\" (the CollectUnclaimed ruling (#604) made the account-inventory question opt-in, and a run that did not ask must say so), and a second plan run with TOFU_LIVE_COLLECT_UNCLAIMED=1 finds exactly 8 foreign objects - the instance's own root volume plus floci's default-VPC bootstrap; instance tofu-address re-checked against EC2"
log "STAGE 3 (test plan): PASS"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 4: TEST APPLY - apply the empty plan, assert a genuine no-op
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage test_apply
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
gauntlet_begin_stage drift_reconverge
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

# ══════════════════════════════════════════════════════════════════════════
# PART P: PLAN, REVIEW, APPLY (plan_approval, live/GAUNTLET.md #12, #903)
# ══════════════════════════════════════════════════════════════════════════
#
# The pipeline shape CI has always run: plan on the pull request, a human
# approves, apply exactly what was approved. The artifact that crosses that
# gate is the plan file, and under live markers it is an APPROVAL rather
# than an instruction - "apply <planfile>" re-reads the live system, plans
# against what it finds now, and compares that fresh plan with the file's,
# refusing by name and with exit 3 when the two disagree (issue #878,
# internal/command/live_approval.go).
#
# Both arms run on every real run, because only the pair is evidence:
#
#   P2/P3  the world MOVES between the approval and the apply - the
#          module.ec2_complete instance's Example tag is changed out of
#          band through the AWS CLI, the same mutation STAGE 5 above
#          already proves this estate's plan notices - and the apply must
#          refuse: exit 3, the named summary, the unapproved row printed by
#          address AND by the live instance id it was computed against, and
#          the reviewed change still not landed when the "/dev/sdf" EBS
#          volume is read back through the CLI.
#   P4     nothing has moved (the tag is put back first) and the SAME file
#          must APPLY. This is the inverted control that
#          live/smoke/scenarios/apply-what-was-approved.sh reasons out: a
#          comparison which refuses unconditionally is not a check, so P3's
#          refusal is only worth something if the identical artifact goes
#          through when the world is where the approval left it.
#
# The two objects are deliberately disjoint. The change under review is the
# "/dev/sdf" entry's own `tags` map inside module "ec2_complete"'s
# `ebs_volumes` argument. terraform-aws-ec2-instance merges each entry's
# tags into that entry's aws_ebs_volume alone (its `each.value.tags` is the
# last term of aws_ebs_volume.this's tag merge and appears nowhere else), so
# the edit reaches exactly module.ec2_complete.aws_ebs_volume.this
# ["/dev/sdf"]. The out-of-band move is on
# module.ec2_complete.aws_instance.this[0] - a different object - so the
# refusal has an EXTRA row to name rather than a values-only disagreement
# about the same row.
#
# Measured, not assumed. The first candidate was module
# "ec2_metadata_options"'s tags, and this script's own reduction (step 1
# above) deletes that module call along with every other ec2_* variant: the
# estate that reaches the emulator has exactly ONE EC2 instance, and PART P
# found that out by listing every live instance rather than by guessing.
# The reduced estate leaves few single-instance targets, and the ones that
# remain are all objects a later part touches - module.security_group and
# module.vpc are renamed by PART D, module.ec2_complete's own instance is
# the moved object and is replaced by PART F. The "/dev/sdf" volume is the
# exception: PART C scales a SYNTHETIC aws_ebs_volume.count_test, a
# different resource, and nothing else reads this one by id.
#
# Runs only on a real run. Under this script's other BREAK controls the
# estate is deliberately left somewhere this part does not describe, so it
# reports no verdict at all and the runner records the stage as not_run,
# never as a pass.
if [ -z "${BREAK:-}" ] && [ -z "${BREAK_COUNT:-}" ]; then
  gauntlet_begin_stage plan_approval
  log "=== PART P: plan, review, apply (the approval gate, live/GAUNTLET.md #12) ==="

  P_REVIEWED_ADDR='module.ec2_complete.aws_ebs_volume.this["/dev/sdf"]'
  P_MOVED_ADDR="module.ec2_complete.aws_instance.this[0]"
  # Found through the AWS CLI by the tag the example's own config puts on
  # it, by enumerating volumes rather than by a server-side tag filter: this
  # part's first draft used "--filters Name=tag:...", and on the pinned
  # emulator that returned nothing for a tag the object demonstrably carries
  # (measured, 2026-09-06). Enumerating and matching in the shell is the
  # same answer without depending on that filter.
  P_VOL_LIST="$(awsl ec2 describe-volumes --query "Volumes[].[VolumeId, Tags[?Key=='MountPoint'] | [0].Value]" --output text)"
  P_VOL_ID="$(awk '$2 == "/mnt/data" { print $1 }' <<< "$P_VOL_LIST")"
  P_VOL_N="$(printf '%s\n' "$P_VOL_ID" | grep -c . || true)"
  [ "$P_VOL_N" = "1" ] || {
    printf '%s\n' "$P_VOL_LIST"
    fail "expected exactly one live EBS volume tagged MountPoint=/mnt/data, found $P_VOL_N (every live volume is listed above as id/MountPoint)"
  }
  P_VOL_REVIEWED="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$P_VOL_ID" "Name=key,Values=MountPoint" --query 'Tags[0].Value' --output text)"
  [ "$P_VOL_REVIEWED" = "/mnt/data" ] || fail "$P_VOL_ID carries MountPoint=\"$P_VOL_REVIEWED\", not the configured \"/mnt/data\", going into PART P"
  P_INSTANCE_EXAMPLE="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$INSTANCE_ID" "Name=key,Values=Example" --query 'Tags[0].Value' --output text)"
  [ -n "$P_INSTANCE_EXAMPLE" ] && [ "$P_INSTANCE_EXAMPLE" != "None" ] || fail "$INSTANCE_ID carries no Example tag going into PART P"
  log "  the change under review lands on volume $P_VOL_ID ($P_REVIEWED_ADDR, MountPoint=\"$P_VOL_REVIEWED\"); the out-of-band move lands on $INSTANCE_ID ($P_MOVED_ADDR, Example=\"$P_INSTANCE_EXAMPLE\")"

  log "=== P1. the change under review: one argument, on one EBS volume ==="
  [ "$(grep -c 'MountPoint = "/mnt/data"' "$EST/main.tf")" = "1" ] \
    || fail "main.tf no longer carries exactly one MountPoint = \"/mnt/data\" volume tag - the corpus pin has moved"
  perl -0pi -e 's{MountPoint = "/mnt/data"}{MountPoint = "/mnt/data-reviewed"}' "$EST/main.tf"
  [ "$(grep -c 'MountPoint = "/mnt/data-reviewed"' "$EST/main.tf")" = "1" ] \
    || fail "the reviewed edit did not write exactly one MountPoint = \"/mnt/data-reviewed\" volume tag"
  log "  edited one argument: the \"/dev/sdf\" volume's MountPoint tag is now \"/mnt/data-reviewed\""

  P_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color -out=approved.tfplan 2>&1)"; P_PLAN_RC=$?
  [ "$P_PLAN_RC" -eq 0 ] || { printf '%s\n' "$P_PLAN_OUT" | tail -60; fail "plan -out exited $P_PLAN_RC"; }
  [ -f "$EST/approved.tfplan" ] || { printf '%s\n' "$P_PLAN_OUT" | tail -20; fail "plan -out wrote no file"; }
  P_APPROVED_ADDRS="$(grep -oE '^  # \S+ will be updated' <<< "$P_PLAN_OUT" | awk '{print $2}' | sort -u)"
  [ "$P_APPROVED_ADDRS" = "$P_REVIEWED_ADDR" ] \
    || { grep -E '^  # .+ will be' <<< "$P_PLAN_OUT"; fail "the approved plan is about [$P_APPROVED_ADDRS], not $P_REVIEWED_ADDR alone"; }
  if grep -qE '^  # .+ will be (created|destroyed)' <<< "$P_PLAN_OUT"; then
    grep -E '^  # .+ will be' <<< "$P_PLAN_OUT"; fail "the approved plan proposes a create or a destroy; this review is one in-place update"
  fi
  P_PLAN_BYTES="$(wc -c < "$EST/approved.tfplan" | tr -d ' ')"
  log "  approved.tfplan written ($P_PLAN_BYTES bytes of stock-format plan file); the approval is exactly one update, on $P_REVIEWED_ADDR"

  log "=== P2. the world moves between the approval and the apply ==="
  awsl ec2 create-tags --resources "$INSTANCE_ID" --tags Key=Example,Value=moved-after-approval
  P_MOVED_VALUE="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$INSTANCE_ID" "Name=key,Values=Example" --query 'Tags[0].Value' --output text)"
  [ "$P_MOVED_VALUE" = "moved-after-approval" ] || fail "the out-of-band move did not take: $INSTANCE_ID's Example tag reads \"$P_MOVED_VALUE\""
  log "  $INSTANCE_ID's Example tag changed out of band to \"moved-after-approval\" - after the approval, before the apply, through the AWS CLI"

  log "=== P3. apply the approved plan against a world that moved ==="
  P_GATE_RC=0
  P_GATE_OUT="$(cd "$EST" && "$TOFU" apply -input=false -no-color approved.tfplan 2>&1)" || P_GATE_RC=$?
  if [ "${BREAK_APPROVAL:-}" = "1" ]; then
    # stages.go's own Break line for plan_approval, executed literally:
    # "Apply the planfile after a mutation and expect success; the run must
    # refuse." Expecting success here is the defect this stage exists to
    # catch, so this assertion has to fail.
    [ "$P_GATE_RC" = "0" ] \
      || fail "BREAK_APPROVAL=1: the apply of a plan file approved before the world moved exited $P_GATE_RC, not 0 - the refusal is load-bearing and this expectation is the defect stage 12 catches"
    log "  BREAK_APPROVAL=1: the apply exited 0 with the world moved - stage 12 is NOT load-bearing"
  fi
  [ "$P_GATE_RC" = "3" ] \
    || { printf '%s\n' "$P_GATE_OUT" | tail -60; fail "the apply exited $P_GATE_RC, want 3 - a plan file whose approval no longer covers the run must refuse with its own status"; }
  grep -q "The approved plan no longer matches the live system" <<< "$P_GATE_OUT" \
    || { printf '%s\n' "$P_GATE_OUT" | tail -60; fail "the apply stopped, but not with the named refusal"; }
  # Everything from the refusal's own summary line onward. The fresh plan
  # printed above it also names the moved instance, so asserting over the
  # whole output would pass on a refusal that named nothing at all.
  P_REFUSAL="$(sed -n '/The approved plan no longer matches the live system/,$p' <<< "$P_GATE_OUT")"
  grep -qF "This apply would do, and the approved plan does not include:" <<< "$P_REFUSAL" \
    || { printf '%s\n' "$P_REFUSAL"; fail "the refusal does not classify the difference as a change nobody approved"; }
  grep -qF "$P_MOVED_ADDR" <<< "$P_REFUSAL" \
    || { printf '%s\n' "$P_REFUSAL"; fail "the refusal does not name $P_MOVED_ADDR, the change nobody approved"; }
  grep -qF "$INSTANCE_ID" <<< "$P_REFUSAL" \
    || { printf '%s\n' "$P_REFUSAL"; fail "the refusal names the address but not $INSTANCE_ID, the live object the change was computed against"; }
  grep -qF "Exit status 3" <<< "$P_REFUSAL" \
    || { printf '%s\n' "$P_REFUSAL"; fail "the refusal does not tell a pipeline what its exit status means"; }
  if grep -q "Apply complete!" <<< "$P_GATE_OUT"; then
    printf '%s\n' "$P_GATE_OUT" | tail -20; fail "the apply ran anyway after refusing"
  fi
  # Not "no Apply complete line" alone: read the live object the approval
  # was about and confirm the reviewed change did not land.
  P_REVIEWED_TAG="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$P_VOL_ID" "Name=key,Values=MountPoint" --query 'Tags[0].Value' --output text)"
  [ "$P_REVIEWED_TAG" = "/mnt/data" ] \
    || fail "the refused apply still wrote the reviewed change: $P_VOL_ID carries MountPoint=\"$P_REVIEWED_TAG\", not the pre-approval \"/mnt/data\""
  printf '%s\n' "$P_REFUSAL" | head -12
  log "  refused by name, exit $P_GATE_RC, nothing applied - and the row it names is exactly the change that appeared after the approval"

  log "=== P4. the inverted control: put the world back, apply the SAME file ==="
  awsl ec2 create-tags --resources "$INSTANCE_ID" --tags "Key=Example,Value=$P_INSTANCE_EXAMPLE"
  P_RESTORED="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$INSTANCE_ID" "Name=key,Values=Example" --query 'Tags[0].Value' --output text)"
  [ "$P_RESTORED" = "$P_INSTANCE_EXAMPLE" ] || fail "the out-of-band move was not undone: $INSTANCE_ID's Example tag reads \"$P_RESTORED\""
  P_OK_RC=0
  P_OK_OUT="$(cd "$EST" && "$TOFU" apply -input=false -no-color approved.tfplan 2>&1)" || P_OK_RC=$?
  [ "$P_OK_RC" = "0" ] \
    || { printf '%s\n' "$P_OK_OUT" | tail -60; fail "the same plan file was refused (exit $P_OK_RC) over a world that had not moved - a comparison that refuses unconditionally is not a check"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$P_OK_OUT" \
    || { grep -E 'Apply complete' <<< "$P_OK_OUT"; fail "the approved apply did not change exactly the one reviewed resource"; }
  P_LANDED="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$P_VOL_ID" "Name=key,Values=MountPoint" --query 'Tags[0].Value' --output text)"
  [ "$P_LANDED" = "/mnt/data-reviewed" ] \
    || fail "the approved change did not land: $P_VOL_ID carries MountPoint=\"$P_LANDED\", want \"/mnt/data-reviewed\""
  log "  the identical artifact applied (0 added, 1 changed, 0 destroyed) and $P_VOL_ID now carries MountPoint=/mnt/data-reviewed, read via the AWS CLI"

  log "=== P5. put the estate back where the rest of this script expects it ==="
  rm -f "$EST/approved.tfplan"
  perl -0pi -e 's{MountPoint = "/mnt/data-reviewed"}{MountPoint = "/mnt/data"}' "$EST/main.tf"
  [ "$(grep -c 'MountPoint = "/mnt/data"' "$EST/main.tf")" = "1" ] \
    || fail "reverting the reviewed edit did not restore the MountPoint = \"/mnt/data\" volume tag"
  P_BACK_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; P_BACK_RC=$?
  [ "$P_BACK_RC" -eq 0 ] || { printf '%s\n' "$P_BACK_OUT" | tail -60; fail "the revert apply failed"; }
  P_GONE="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$P_VOL_ID" "Name=key,Values=MountPoint" --query 'Tags[0].Value' --output text)"
  [ "$P_GONE" = "/mnt/data" ] \
    || fail "the reviewed MountPoint value is still on $P_VOL_ID after the revert: \"$P_GONE\""
  P_FINAL_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; P_FINAL_RC=$?
  [ "$P_FINAL_RC" -eq 0 ] || { printf '%s\n' "$P_FINAL_OUT" | tail -60; fail "the post-revert plan exited $P_FINAL_RC"; }
  if grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$P_FINAL_OUT"; then
    grep -E '^  # .+ will be' <<< "$P_FINAL_OUT"; fail "the estate is not converged again after PART P"
  fi
  log "  reverted; the estate is converged again and PART C starts from where it would have"

  log ""
  log "PART P (plan, review, apply): PASS"
  gauntlet_stage plan_approval pass "one argument edited (the \"/dev/sdf\" entry's MountPoint volume tag inside module \"ec2_complete\"'s ebs_volumes argument, /mnt/data -> /mnt/data-reviewed - the module merges each entry's tags into that entry's aws_ebs_volume alone, so it reaches $P_REVIEWED_ADDR and nothing else), \"plan -out=approved.tfplan\" wrote a $P_PLAN_BYTES-byte stock-format plan file whose whole change set is that one update; the world then moved out of band ($INSTANCE_ID's Example tag, through the AWS CLI, never through choudoufu - the same mutation STAGE 5 uses) and \"apply approved.tfplan\" refused with \"The approved plan no longer matches the live system\" at exit 3, classifying the drift under \"This apply would do, and the approved plan does not include:\" and naming both $P_MOVED_ADDR and the live $INSTANCE_ID it was computed against, with \"Exit status 3\" spelled out for a pipeline; nothing was applied - volume $P_VOL_ID still read MountPoint=/mnt/data through ec2 describe-tags, not from the absence of an \"Apply complete!\" line. Inverted control on the same run (the shape live/smoke/scenarios/apply-what-was-approved.sh reasons out): with $INSTANCE_ID's tag put back and nothing else changed, the IDENTICAL file applied - 0 added, 1 changed, 0 destroyed - and $P_VOL_ID read back with MountPoint=/mnt/data-reviewed, so the refusal is earned by the drift and not handed out to every plan file. The edit was then reverted, re-applied and the estate replanned empty, so PART C starts where it would have. BREAK_APPROVAL=1 asserts stage 12's own recorded Break line (apply the planfile after a mutation and expect success) and correctly fails"
  log ""
fi

  # ══════════════════════════════════════════════════════════════════════
  # PART C: CHANGE COUNT (day2_count, active - live/GAUNTLET.md #8; the
  # section this estate never had, written for issue #643's board repair)
  # ══════════════════════════════════════════════════════════════════════
  #
  # Runs here - straight off STAGE 5's converged estate, BEFORE PART F and
  # PART D - on purpose, and this is load-bearing rather than tidy. Both of
  # those sections carry documented walls whose assertions call fail(), and
  # fail() exits the script, so day2_count placed after either of them
  # reports nothing at all on a run where one of them reproduces. Both did,
  # on real runs of this branch: PART D's moved-block rename hits
  # recordOrphanReadSweep's missing moved-block awareness (that section's own
  # comment names the commit), and PART F's post-replace plan hits the
  # terminated-instance claimant wall this script's PART F now documents.
  # day2_count's own subject depends on neither: it needs only an adopted,
  # converged estate, which is exactly what STAGE 5 leaves.
  #
  # THE SYNTHETIC BLOCK, AND WHY. terraform-aws-ec2-instance v6.4.0 has no
  # scalable count or for_each knob anywhere this estate reaches. Every
  # `count` in the module's own source (read directly rather than inferred -
  # .corpus/ec2-instance/main.tf lines 42, 252, 468, 715, 729, 751, 778,
  # 786 and 859) has the shape `count = local.create && ... ? 1 : 0`: a
  # boolean create toggle that can hold zero or one instance and never two,
  # so there is nothing there for day2_count to scale. The upstream
  # complete example's one genuine fan-out, `module "ec2_multiple" {
  # for_each = local.multiple_instances }`, is dropped by this script's own
  # reduction (see the header) along with the spot/capacity-reservation
  # surfaces floci does not model. So this section adds a NEW,
  # self-contained synthetic count block - the sanctioned fallback
  # live/GAUNTLET.md #8 names, with reference-ec2-vpc's Part F and
  # corpus-iam-policy's Part G as precedent - reusing a type this estate
  # ALREADY exercises (aws_ebs_volume, module.ec2_complete's own /dev/sdf
  # data volume) rather than introducing a new one.
  #
  # WHY aws_ebs_volume RATHER THAN aws_security_group (reference-ec2-vpc's
  # own choice for the same fallback): a volume needs nothing from the rest
  # of this estate but an availability zone - no vpc_id - so count_test
  # never references module.vpc, the module PART D renames out from under
  # any such reference two sections later. It lives in its own file
  # ($EST/count_test.tf) for the same reason: main.tf is rewritten by PART
  # D's sed and PART E's perl, and a block appended there would have to
  # survive both. C4 scales the block to zero and deletes the file, so the
  # estate PART D inherits is byte-for-byte the estate PART F left.
  #
  # CONFIRMED DIRECTLY AGAINST THE EMULATOR, with no terraform in the loop,
  # before this section was written: two volumes created through `aws ec2
  # create-volume --tag-specifications` came back under distinct,
  # server-minted, random ids (vol-333c22db694d10957 and
  # vol-2a6aff95d0baaa0dc); `describe-volumes --filters Name=tag:Name,...`
  # really does filter, checked with a negative control - a value
  # guaranteed to match nothing returned 0 volumes, so the lookups below
  # read a genuinely filtered list, unlike this script's own EIP lookup
  # which has to match client-side because DescribeAddresses ignores
  # --filters entirely (lex00/floci#150); and deleting one made
  # `describe-volumes --volume-ids` on it fail loudly with
  # InvalidVolume.NotFound rather than return a stale record. Every lookup
  # below still asserts it matched EXACTLY one volume, so an emulator that
  # regressed that filter fails here rather than silently picking a
  # neighbour.
  #
  # C-ORACLE is this stage's stock oracle (live/GAUNTLET.md #8: "Stock's
  # plan for the same count change, normalised"). Stock never had this
  # count block, so - unlike D-ORACLE, D-REMOVE-ORACLE and F-ORACLE above -
  # there is nothing in cold_deploy's own state to replan against: it
  # stands the identical 2-instance block up for real with the plain
  # terraform binary, in its own working directory against $ENDPOINT,
  # scales it down and back up, and is torn down again before the choudoufu
  # half starts. $GREEN_ENDPOINT is not available here - PART GREENFIELD
  # removes its own container the moment it finishes - and $ENDPOINT is
  # safe to borrow: STAGE 3's foreign-object count, the one assertion in
  # this script that would notice two extra unowned objects, ran long
  # before this point. The oracle's volumes are named apart from the
  # choudoufu half's AND destroyed before it runs, which is the collision
  # corpus-iam-policy's Part G hit empirically and documents. The oracle
  # pins the SAME provider version the adopted estate itself resolved, read
  # out of $EST/.terraform.lock.hcl rather than hardcoded, so a plan-shape
  # difference between the two halves can never be a provider-version
  # difference.
  #
  # BREAK_COUNT=1 exercises this stage's own Break control instead of the
  # real checks: after the real scale-down plan, assert the WRONG instance
  # (count_test[0] rather than count_test[1]) was the one destroyed - the
  # Break text in tools/gauntlet/stages.go for day2_count, verbatim:
  # "Expect a different instance to be destroyed; the assertion must fail."
  # Unlike reference-ec2-vpc's own variant, which logs and falls through
  # leaving the stage with no verdict at all, this one routes the inverted
  # assertion through fail(), so a BREAK_COUNT=1 run prints a real
  # `GAUNTLET stage=day2_count verdict=fail` line. Independent of BREAK.
  gauntlet_begin_stage day2_count

  COUNT_AZ="${REGION}a"
  awsl ec2 describe-availability-zones --query "AvailabilityZones[?ZoneName=='$COUNT_AZ'].ZoneName" --output text 2>/dev/null | grep -qx "$COUNT_AZ" \
    || fail "$COUNT_AZ is not an availability zone this account offers - day2_count's count block has nowhere to put its volumes"

  # count_test_block($1 = count): day2_count's own synthetic resource, in
  # its own file so nothing else in this script's config edits can touch
  # it. Unquoted heredoc so $1 interpolates; ${count.index} is escaped so
  # bash never tries to expand it.
  count_test_block() {
    cat > "$EST/count_test.tf" <<COUNTEOF
resource "aws_ebs_volume" "count_test" {
  count             = $1
  availability_zone = "$COUNT_AZ"
  size              = 1
  type              = "gp3"

  tags = {
    Name = "ec2-instance-count-test-\${count.index}"
  }
}
COUNTEOF
  }

  # oracle_count_block($1 = count): the identical block for the stock
  # oracle's own working directory, under its own Name tags so the two
  # halves' lookups can never see each other's volumes.
  oracle_count_block() {
    cat > "$ORACLE_COUNT_DIR/main.tf" <<COUNTEOF
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= $EST_AWS_VER"
    }
  }
}

provider "aws" {
  region                      = "$REGION"
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true
}

resource "aws_ebs_volume" "count_test" {
  count             = $1
  availability_zone = "$COUNT_AZ"
  size              = 1
  type              = "gp3"

  tags = {
    Name = "ec2-instance-count-oracle-\${count.index}"
  }
}
COUNTEOF
  }

  # vol_by_name($1 = exact Name tag value): the single matching VolumeId on
  # stdout, or a loud failure. Never "take the first row of whatever came
  # back" - exactly one match or nothing.
  vol_by_name() {
    local want="$1" ids n
    ids="$(awsl ec2 describe-volumes --filters "Name=tag:Name,Values=$want" --query 'Volumes[].VolumeId' --output text 2>/dev/null | tr '\t' '\n' | sed '/^$/d;/^None$/d')"
    n="$(printf '%s\n' "$ids" | sed '/^$/d' | wc -l | tr -d ' ')"
    if [ "$n" != "1" ]; then
      printf 'describe-volumes for Name=%s matched %s volume(s): %s\n' "$want" "$n" "$(printf '%s' "$ids" | tr '\n' ' ')" >&2
      return 1
    fi
    printf '%s' "$ids"
  }

  # vol_gone($1 = volume id): true when the emulator no longer knows the
  # volume at all. floci answers InvalidVolume.NotFound for a deleted
  # volume (confirmed directly, see this section's header), so a non-zero
  # exit here is genuine absence, not a swallowed error.
  vol_gone() {
    ! awsl ec2 describe-volumes --volume-ids "$1" --query 'Volumes[0].VolumeId' --output text >/dev/null 2>&1
  }

  vol_tag() { # $1 = volume id, $2 = tag key
    awsl ec2 describe-tags --filters "Name=resource-id,Values=$1" "Name=key,Values=$2" --query 'Tags[0].Value' --output text 2>/dev/null
  }

  # The registry host is deliberately NOT pinned in this pattern. STAGE 1's
  # plain `terraform init` writes
  # provider "registry.terraform.io/hashicorp/aws"; STAGE 2's `choudoufu
  # init` rewrites the SAME file as
  # provider "registry.opentofu.org/hashicorp/aws", because this fork is an
  # OpenTofu fork and resolves a bare "hashicorp/aws" source against
  # OpenTofu's own registry. Both resolved 6.62.0 for this estate's
  # ">= 6.37" constraint when this was checked directly; a host-specific
  # pattern here matched neither by the time day2_count runs, which is
  # exactly how the first draft of this section failed.
  EST_AWS_VER="$(sed -n '/^provider "registry\.[^"]*\/hashicorp\/aws" {/,/^}/p' "$EST/.terraform.lock.hcl" 2>/dev/null | sed -n 's/^[[:space:]]*version[[:space:]]*=[[:space:]]*"\(.*\)"$/\1/p' | head -1)"
  [ -n "$EST_AWS_VER" ] \
    || { [ -f "$EST/.terraform.lock.hcl" ] && sed -n '1,20p' "$EST/.terraform.lock.hcl"; fail "could not read the adopted estate's own resolved hashicorp/aws version out of $EST/.terraform.lock.hcl - the day2_count oracle would otherwise silently compare two different providers"; }

  log "=== C-ORACLE. day2_count stock oracle: stand a 2-instance count block up with plain terraform, scale it to 1 and back ==="
  ORACLE_COUNT_DIR="$WORK/oracle-count"
  mkdir -p "$ORACLE_COUNT_DIR"
  oracle_count_block 2
  ( cd "$ORACLE_COUNT_DIR" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ORACLE_COUNT_DIR" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_count stock oracle's terraform init failed (hashicorp/aws = $EST_AWS_VER)"; }
  ORACLE_COUNT_APPLY_OUT="$(cd "$ORACLE_COUNT_DIR" && terraform apply -input=false -auto-approve -no-color 2>&1)"; ORACLE_COUNT_APPLY_RC=$?
  [ "$ORACLE_COUNT_APPLY_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_COUNT_APPLY_OUT" | tail -30; fail "the day2_count stock oracle's baseline apply failed"; }
  grep -qE 'Apply complete! Resources: 2 added' <<< "$ORACLE_COUNT_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$ORACLE_COUNT_APPLY_OUT"; fail "stock did not create exactly 2 count-test volumes for the day2_count oracle"; }
  ORACLE_V0="$(vol_by_name ec2-instance-count-oracle-0)" || fail "no single oracle count_test[0] volume found by its Name tag"
  ORACLE_V1="$(vol_by_name ec2-instance-count-oracle-1)" || fail "no single oracle count_test[1] volume found by its Name tag"
  log "  stock: 2 instances created, count_test[0]=$ORACLE_V0 count_test[1]=$ORACLE_V1"

  oracle_count_block 1
  ORACLE_DOWN_PLAN_OUT="$(cd "$ORACLE_COUNT_DIR" && terraform plan -input=false -no-color 2>&1)"; ORACLE_DOWN_PLAN_RC=$?
  [ "$ORACLE_DOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | tail -30; fail "the day2_count stock oracle's scale-down plan exited $ORACLE_DOWN_PLAN_RC"; }
  grep -qE '^  # aws_ebs_volume\.count_test\[1\] will be destroyed' <<< "$ORACLE_DOWN_PLAN_OUT" \
    || { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-down plan does not destroy count_test[1]"; }
  grep -qE '^  # aws_ebs_volume\.count_test\[0\] will be' <<< "$ORACLE_DOWN_PLAN_OUT" \
    && { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-down plan touches count_test[0], which should be untouched"; }
  grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$ORACLE_DOWN_PLAN_OUT" \
    || { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | tail -10; fail "stock's scale-down plan proposes something other than exactly one destroy"; }
  ORACLE_DOWN_APPLY_OUT="$(cd "$ORACLE_COUNT_DIR" && terraform apply -input=false -auto-approve -no-color 2>&1)"; ORACLE_DOWN_APPLY_RC=$?
  [ "$ORACLE_DOWN_APPLY_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_DOWN_APPLY_OUT" | tail -30; fail "the day2_count stock oracle's scale-down apply failed"; }
  grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$ORACLE_DOWN_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$ORACLE_DOWN_APPLY_OUT"; fail "the day2_count stock oracle's scale-down apply was not exactly one destroy"; }
  ORACLE_V0_AFTER_DOWN="$(awsl ec2 describe-volumes --volume-ids "$ORACLE_V0" --query 'Volumes[0].VolumeId' --output text 2>/dev/null || true)"
  [ "$ORACLE_V0_AFTER_DOWN" = "$ORACLE_V0" ] || fail "stock's surviving count_test[0] changed id across the scale-down ($ORACLE_V0 -> $ORACLE_V0_AFTER_DOWN)"
  vol_gone "$ORACLE_V1" || fail "stock's count_test[1] ($ORACLE_V1) still exists after the scale-down destroy"
  log "  stock: exactly one destroy (count_test[1]=$ORACLE_V1, gone from the account), count_test[0]=$ORACLE_V0 unchanged"

  oracle_count_block 2
  ORACLE_UP_PLAN_OUT="$(cd "$ORACLE_COUNT_DIR" && terraform plan -input=false -no-color 2>&1)"; ORACLE_UP_PLAN_RC=$?
  [ "$ORACLE_UP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | tail -30; fail "the day2_count stock oracle's scale-up plan exited $ORACLE_UP_PLAN_RC"; }
  grep -qE '^  # aws_ebs_volume\.count_test\[1\] will be created' <<< "$ORACLE_UP_PLAN_OUT" \
    || { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-up plan does not create count_test[1]"; }
  grep -qE '^  # aws_ebs_volume\.count_test\[0\] will be' <<< "$ORACLE_UP_PLAN_OUT" \
    && { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-up plan touches count_test[0], which should be untouched"; }
  grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$ORACLE_UP_PLAN_OUT" \
    || { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | tail -10; fail "stock's scale-up plan proposes something other than exactly one create"; }
  ORACLE_UP_APPLY_OUT="$(cd "$ORACLE_COUNT_DIR" && terraform apply -input=false -auto-approve -no-color 2>&1)"; ORACLE_UP_APPLY_RC=$?
  [ "$ORACLE_UP_APPLY_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_UP_APPLY_OUT" | tail -30; fail "the day2_count stock oracle's scale-up apply failed"; }
  grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$ORACLE_UP_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$ORACLE_UP_APPLY_OUT"; fail "the day2_count stock oracle's scale-up apply was not exactly one create"; }
  ORACLE_V1_NEW="$(vol_by_name ec2-instance-count-oracle-1)" || fail "no single oracle count_test[1] volume found after the scale-up"
  [ "$ORACLE_V1_NEW" != "$ORACLE_V1" ] || fail "stock's recreated count_test[1] came back with the SAME id ($ORACLE_V1) it had before being destroyed - the oracle's own destroy was not real"
  ORACLE_V0_AFTER_UP="$(awsl ec2 describe-volumes --volume-ids "$ORACLE_V0" --query 'Volumes[0].VolumeId' --output text 2>/dev/null || true)"
  [ "$ORACLE_V0_AFTER_UP" = "$ORACLE_V0" ] || fail "stock's count_test[0] changed id across the scale-up ($ORACLE_V0 -> $ORACLE_V0_AFTER_UP)"
  log "  stock: exactly one create (count_test[1] came back as $ORACLE_V1_NEW, was $ORACLE_V1), count_test[0]=$ORACLE_V0 unchanged throughout"

  # Torn down before the choudoufu half runs: the two halves share
  # $ENDPOINT (it is idle here, not a second account), and leaving the
  # oracle's own untagged, unmarked volumes behind is what made
  # corpus-iam-policy's first draft of this section read "None" off an
  # oracle object instead of the marked one it meant to check.
  ORACLE_COUNT_DESTROY_OUT="$(cd "$ORACLE_COUNT_DIR" && terraform destroy -input=false -auto-approve -no-color 2>&1)"; ORACLE_COUNT_DESTROY_RC=$?
  [ "$ORACLE_COUNT_DESTROY_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_COUNT_DESTROY_OUT" | tail -30; fail "the day2_count stock oracle's teardown failed"; }
  grep -qE 'Destroy complete! Resources: 2 destroyed' <<< "$ORACLE_COUNT_DESTROY_OUT" \
    || { grep -E 'Destroy complete' <<< "$ORACLE_COUNT_DESTROY_OUT"; fail "the day2_count stock oracle's teardown was not exactly 2 destroys"; }
  log "  stock oracle torn down (2 destroyed) - the shared endpoint is clean before the real choudoufu side starts"

  log "=== C0. choudoufu: add aws_ebs_volume.count_test, count = 2 ==="
  count_test_block 2
  COUNT_ADD_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; COUNT_ADD_PLAN_RC=$?
  [ "$COUNT_ADD_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_ADD_PLAN_OUT" | tail -40; fail "the count-block-add plan exited $COUNT_ADD_PLAN_RC"; }
  grep -qF 'Plan: 2 to add, 0 to change, 0 to destroy.' <<< "$COUNT_ADD_PLAN_OUT" \
    || { printf '%s\n' "$COUNT_ADD_PLAN_OUT" | tail -20; fail "adding the count block did not plan exactly 2 creates"; }
  COUNT_ADD_APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_ADD_APPLY_RC=$?
  [ "$COUNT_ADD_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_ADD_APPLY_OUT" | tail -40; fail "the count-block-add apply exited $COUNT_ADD_APPLY_RC"; }
  grep -qE 'Resources: 2 added, 0 changed, 0 destroyed' <<< "$COUNT_ADD_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$COUNT_ADD_APPLY_OUT"; fail "the count-block-add apply did not create exactly 2 resources"; }

  CV0_ID="$(vol_by_name ec2-instance-count-test-0)" || fail "no single live count_test[0] volume found by its Name tag"
  CV1_ID="$(vol_by_name ec2-instance-count-test-1)" || fail "no single live count_test[1] volume found by its Name tag"
  [ "$CV0_ID" != "$CV1_ID" ] || fail "count_test[0] and count_test[1] resolved to the same volume id ($CV0_ID)"
  CV0_ADDR="$(vol_tag "$CV0_ID" tofu-address)"
  CV1_ADDR="$(vol_tag "$CV1_ID" tofu-address)"
  [ "$CV0_ADDR" = 'aws_ebs_volume.count_test:0' ] || fail "count_test[0]'s live tofu-address tag is $CV0_ADDR, not aws_ebs_volume.count_test:0 (live/MARKERS.md: a count instance's tag value is colon-escaped, e.g. aws_eip.this[2] -> aws_eip.this:2)"
  [ "$CV1_ADDR" = 'aws_ebs_volume.count_test:1' ] || fail "count_test[1]'s live tofu-address tag is $CV1_ADDR, not aws_ebs_volume.count_test:1"
  CV0_EST="$(vol_tag "$CV0_ID" tofu-estate)"
  [ "$CV0_EST" = "$ESTATE" ] || fail "count_test[0] carries tofu-estate=$CV0_EST, not $ESTATE"
  # tofu-slot, asserted by value against live/MARKERS.md's own promise:
  # "The first instance of aws_eip.this gets slot 0, the second gets slot
  # 1." It is the marker that survives a rename and retires on a
  # cardinality change, so a count stage that never reads it is not
  # reading the identity the stage is about.
  CV0_SLOT="$(vol_tag "$CV0_ID" tofu-slot)"
  CV1_SLOT="$(vol_tag "$CV1_ID" tofu-slot)"
  [ "$CV0_SLOT" = "0" ] || fail "count_test[0] carries tofu-slot=$CV0_SLOT, not 0 (live/MARKERS.md: slots are assigned from a monotonic counter per count block, starting at 0)"
  [ "$CV1_SLOT" = "1" ] || fail "count_test[1] carries tofu-slot=$CV1_SLOT, not 1"
  log "  2 instances created: index 0 = $CV0_ID (tofu-address=$CV0_ADDR tofu-slot=$CV0_SLOT), index 1 = $CV1_ID (tofu-address=$CV1_ADDR tofu-slot=$CV1_SLOT) - read via the AWS CLI"

  COUNT_NOOP_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; COUNT_NOOP_PLAN_RC=$?
  [ "$COUNT_NOOP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_NOOP_PLAN_OUT" | tail -40; fail "the post-add plan exited $COUNT_NOOP_PLAN_RC"; }
  grep -qF "No changes. Your infrastructure matches the configuration." <<< "$COUNT_NOOP_PLAN_OUT" \
    || { grep -E '^  #' <<< "$COUNT_NOOP_PLAN_OUT"; fail "the plan right after adding the count block is not empty - the new instances did not bind their own markers cleanly"; }
  log "  No changes - both new instances plan empty immediately after creation"

  log "=== C1. scale count down: 2 -> 1 ==="
  count_test_block 1
  COUNT_DOWN_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; COUNT_DOWN_PLAN_RC=$?
  [ "$COUNT_DOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | tail -40; fail "the scale-down plan exited $COUNT_DOWN_PLAN_RC"; }

  if [ "${BREAK_COUNT:-}" = "1" ]; then
    log "  BREAK_COUNT=1: asserting the WRONG instance (count_test[0]) was destroyed instead of count_test[1] - stages.go's Break text for day2_count, inverted on purpose; this MUST report fail"
    printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'
    grep -qE '^  # aws_ebs_volume\.count_test\[0\] will be destroyed' <<< "$COUNT_DOWN_PLAN_OUT" \
      || fail "BREAK_COUNT=1: the scale-down plan does NOT destroy count_test[0] (it destroys the higher index, as it must) - which is exactly why the real assertion below is load-bearing rather than a grep that always matches"
    fail "BREAK_COUNT=1: the scale-down plan destroys count_test[0], the LOWER index - stock destroys the higher one; a surviving instance was displaced"
  fi

  grep -qE '^  # aws_ebs_volume\.count_test\[1\] will be destroyed' <<< "$COUNT_DOWN_PLAN_OUT" \
    || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-down plan does not destroy count_test[1]"; }
  grep -qE '^  # aws_ebs_volume\.count_test\[0\] will be' <<< "$COUNT_DOWN_PLAN_OUT" \
    && { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-down plan touches count_test[0], which should be untouched"; }
  grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$COUNT_DOWN_PLAN_OUT" \
    || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | tail -10; fail "choudoufu's scale-down plan proposes something other than exactly one destroy"; }
  log "  choudoufu: exactly one destroy (count_test[1]), count_test[0] untouched - the same shape C-ORACLE showed"

  COUNT_DOWN_APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_DOWN_APPLY_RC=$?
  [ "$COUNT_DOWN_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_DOWN_APPLY_OUT" | tail -40; fail "the scale-down apply exited $COUNT_DOWN_APPLY_RC"; }
  grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$COUNT_DOWN_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$COUNT_DOWN_APPLY_OUT"; fail "the scale-down apply was not exactly one destroy"; }

  CV0_AFTER_DOWN="$(awsl ec2 describe-volumes --volume-ids "$CV0_ID" --query 'Volumes[0].VolumeId' --output text 2>/dev/null || true)"
  [ "$CV0_AFTER_DOWN" = "$CV0_ID" ] || fail "count_test[0]'s live id changed across the scale-down ($CV0_ID -> $CV0_AFTER_DOWN) - it was destroyed and recreated, not left alone"
  vol_gone "$CV1_ID" || fail "count_test[1] ($CV1_ID) still exists in the live account after the scale-down destroy"
  CV0_ADDR_AFTER_DOWN="$(vol_tag "$CV0_ID" tofu-address)"
  [ "$CV0_ADDR_AFTER_DOWN" = 'aws_ebs_volume.count_test:0' ] || fail "count_test[0]'s tofu-address tag changed across the scale-down: $CV0_ADDR_AFTER_DOWN"
  CV0_SLOT_AFTER_DOWN="$(vol_tag "$CV0_ID" tofu-slot)"
  [ "$CV0_SLOT_AFTER_DOWN" = "$CV0_SLOT" ] || fail "count_test[0]'s tofu-slot changed across the scale-down ($CV0_SLOT -> $CV0_SLOT_AFTER_DOWN) - a surviving instance's slot is never reassigned"
  log "  $CV1_ID (count_test[1]) is gone from the account (InvalidVolume.NotFound); $CV0_ID (count_test[0]) keeps its id, its tofu-address and its tofu-slot - all read via the AWS CLI"

  log "=== C2. scale count back up: 1 -> 2 ==="
  count_test_block 2
  COUNT_UP_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; COUNT_UP_PLAN_RC=$?
  [ "$COUNT_UP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | tail -40; fail "the scale-up plan exited $COUNT_UP_PLAN_RC"; }
  grep -qE '^  # aws_ebs_volume\.count_test\[1\] will be created' <<< "$COUNT_UP_PLAN_OUT" \
    || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-up plan does not create count_test[1]"; }
  grep -qE '^  # aws_ebs_volume\.count_test\[0\] will be' <<< "$COUNT_UP_PLAN_OUT" \
    && { printf '%s\n' "$COUNT_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-up plan touches count_test[0], which should be untouched"; }
  grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$COUNT_UP_PLAN_OUT" \
    || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | tail -10; fail "choudoufu's scale-up plan proposes something other than exactly one create"; }
  log "  choudoufu: exactly one create (count_test[1]), count_test[0] untouched - the same shape C-ORACLE showed"

  COUNT_UP_APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_UP_APPLY_RC=$?
  [ "$COUNT_UP_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_UP_APPLY_OUT" | tail -40; fail "the scale-up apply exited $COUNT_UP_APPLY_RC"; }
  grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$COUNT_UP_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$COUNT_UP_APPLY_OUT"; fail "the scale-up apply was not exactly one create"; }

  CV1_NEW_ID="$(vol_by_name ec2-instance-count-test-1)" || fail "no single live count_test[1] volume found by its Name tag after the scale-up"
  [ "$CV1_NEW_ID" != "$CV1_ID" ] || fail "count_test[1] came back under the SAME id ($CV1_ID) it had before being destroyed - the destroy in C1 was not real"
  CV1_NEW_ADDR="$(vol_tag "$CV1_NEW_ID" tofu-address)"
  [ "$CV1_NEW_ADDR" = 'aws_ebs_volume.count_test:1' ] || fail "the recreated count_test[1] ($CV1_NEW_ID) carries tofu-address=$CV1_NEW_ADDR, not aws_ebs_volume.count_test:1"
  CV0_AFTER_UP="$(awsl ec2 describe-volumes --volume-ids "$CV0_ID" --query 'Volumes[0].VolumeId' --output text 2>/dev/null || true)"
  [ "$CV0_AFTER_UP" = "$CV0_ID" ] || fail "count_test[0]'s live id changed across the scale-up ($CV0_ID -> $CV0_AFTER_UP)"
  CV0_ADDR_AFTER_UP="$(vol_tag "$CV0_ID" tofu-address)"
  [ "$CV0_ADDR_AFTER_UP" = 'aws_ebs_volume.count_test:0' ] || fail "count_test[0]'s tofu-address tag changed across the scale-up: $CV0_ADDR_AFTER_UP"
  # live/MARKERS.md's own guarantee for a recreated instance, written from
  # the spec rather than from the implementation: "New instances are
  # assigned slots above the live high-water mark", and a slot is "never
  # duplicated within a set". The live high-water mark after C1 is
  # count_test[0]'s own slot, so the new instance's slot must parse as an
  # integer strictly greater than it - not merely "different".
  CV1_NEW_SLOT="$(vol_tag "$CV1_NEW_ID" tofu-slot)"
  CV0_SLOT_AFTER_UP="$(vol_tag "$CV0_ID" tofu-slot)"
  [ "$CV0_SLOT_AFTER_UP" = "$CV0_SLOT" ] || fail "count_test[0]'s tofu-slot changed across the scale-up ($CV0_SLOT -> $CV0_SLOT_AFTER_UP) - a surviving instance's slot is never reassigned"
  case "$CV1_NEW_SLOT" in
    ''|*[!0-9]*) fail "the recreated count_test[1] carries tofu-slot=$CV1_NEW_SLOT, which is not an unsigned base-10 integer (live/MARKERS.md)" ;;
  esac
  [ "$CV1_NEW_SLOT" -gt "$CV0_SLOT_AFTER_UP" ] \
    || fail "the recreated count_test[1] carries tofu-slot=$CV1_NEW_SLOT, not above the live high-water mark ($CV0_SLOT_AFTER_UP) the surviving count_test[0] holds - live/MARKERS.md: a slot is never reused while any resource holds it and never duplicated within a set"
  log "  count_test[1] recreated under a new id ($CV1_NEW_ID, was $CV1_ID), tofu-address=$CV1_NEW_ADDR tofu-slot=$CV1_NEW_SLOT; count_test[0] ($CV0_ID) untouched throughout the down-then-up cycle, tofu-slot still $CV0_SLOT_AFTER_UP - all read via the AWS CLI"

  log "=== C3. one more plan: config and reality agree, nothing left to propose ==="
  COUNT_FINAL_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; COUNT_FINAL_PLAN_RC=$?
  [ "$COUNT_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_FINAL_PLAN_OUT" | tail -40; fail "the post-scale-up plan exited $COUNT_FINAL_PLAN_RC"; }
  grep -qF "No changes. Your infrastructure matches the configuration." <<< "$COUNT_FINAL_PLAN_OUT" \
    || { grep -E '^  #' <<< "$COUNT_FINAL_PLAN_OUT"; fail "the post-scale-up plan is not empty"; }
  log "  No changes. The scale-down-then-up cycle is complete and invisible to the next plan."

  log "=== C4. retire the count block: 2 -> 0, so PART D inherits exactly the estate PART F left ==="
  count_test_block 0
  COUNT_ZERO_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; COUNT_ZERO_PLAN_RC=$?
  [ "$COUNT_ZERO_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_ZERO_PLAN_OUT" | tail -40; fail "the scale-to-zero plan exited $COUNT_ZERO_PLAN_RC"; }
  grep -qF 'Plan: 0 to add, 0 to change, 2 to destroy.' <<< "$COUNT_ZERO_PLAN_OUT" \
    || { printf '%s\n' "$COUNT_ZERO_PLAN_OUT" | tail -10; fail "scaling the count block to zero proposes something other than exactly two destroys"; }
  COUNT_ZERO_APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_ZERO_APPLY_RC=$?
  [ "$COUNT_ZERO_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_ZERO_APPLY_OUT" | tail -40; fail "the scale-to-zero apply exited $COUNT_ZERO_APPLY_RC"; }
  grep -qE 'Resources: 0 added, 0 changed, 2 destroyed' <<< "$COUNT_ZERO_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$COUNT_ZERO_APPLY_OUT"; fail "the scale-to-zero apply was not exactly two destroys"; }
  vol_gone "$CV0_ID" || fail "count_test[0] ($CV0_ID) still exists after the count block was scaled to zero"
  vol_gone "$CV1_NEW_ID" || fail "count_test[1] ($CV1_NEW_ID) still exists after the count block was scaled to zero"
  rm -f "$EST/count_test.tf"
  COUNT_GONE_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; COUNT_GONE_PLAN_RC=$?
  [ "$COUNT_GONE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_GONE_PLAN_OUT" | tail -40; fail "the plan after deleting count_test.tf exited $COUNT_GONE_PLAN_RC"; }
  grep -qF "No changes. Your infrastructure matches the configuration." <<< "$COUNT_GONE_PLAN_OUT" \
    || { grep -E '^  #' <<< "$COUNT_GONE_PLAN_OUT"; fail "the plan after deleting the (already zero-instance) count_test.tf is not empty"; }
  log "  both count_test volumes destroyed and count_test.tf deleted; the estate plans empty again, exactly as PART F left it"

  log ""
  log "PART C (day2_count): PASS"
  gauntlet_stage day2_count pass "choudoufu: scaling aws_ebs_volume.count_test from 2 to 1 destroyed exactly count_test[1] ($CV1_ID, 0 add, 0 change, 1 destroy) and left count_test[0] ($CV0_ID) with the same live VolumeId, the same tofu-address=aws_ebs_volume.count_test:0 and the same tofu-slot=$CV0_SLOT, all read back through the AWS CLI rather than choudoufu's own report; the destroyed volume is genuinely gone (describe-volumes answers InvalidVolume.NotFound for it). Scaling back from 1 to 2 planned exactly 1 to add, 0 to change, 0 to destroy and brought count_test[1] back as a NEW object ($CV1_NEW_ID, not $CV1_ID) carrying tofu-address=aws_ebs_volume.count_test:1 and tofu-slot=$CV1_NEW_SLOT, above the live high-water mark count_test[0] still holds, while count_test[0] stayed untouched throughout; the next plan is empty, and scaling the block to zero destroys both and leaves the estate planning empty again. C-ORACLE, the same 2-instance block stood up for real with plain terraform in its own working directory at the SAME resolved provider version ($EST_AWS_VER), shows the identical shape: destroy the higher index only ($ORACLE_V1), create the higher index back under a new id ($ORACLE_V1_NEW), the lower index's id ($ORACLE_V0) unchanged both times. SYNTHETIC BLOCK, and why: terraform-aws-ec2-instance v6.4.0 declares no scalable count or for_each knob this estate reaches - all nine of its own count usages are boolean create toggles of the form 'count = local.create ? 1 : 0', which can never hold two instances, and the upstream example's one real for_each fan-out (module.ec2_multiple) is dropped by this script's reduction because floci does not model the surfaces around it - so this section adds a new, self-contained count block of a type the estate ALREADY exercises (aws_ebs_volume, module.ec2_complete's own /dev/sdf data volume), the sanctioned fallback live/GAUNTLET.md #8 names, with reference-ec2-vpc Part F and corpus-iam-policy Part G as precedent. BREAK_COUNT=1 asserts the WRONG instance (count_test[0]) was destroyed and reports day2_count fail, proving the assertion is load-bearing."
  log ""
  gauntlet_end_stage

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
  gauntlet_begin_stage day2_replace
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
    # Unlike the security-group/SQS/S3 collision controls, a terminated EC2
    # instance is not gone from the account: AWS documents (and the F2 wall
    # below reproduces empirically) that a terminated instance keeps
    # answering describe-instances/describe-tags/get-resources with its
    # tags for a time after termination. Left alone, this manufactured
    # ghost would still claim module.ec2_complete's address on every LATER
    # stage's own tag sweep in this same script run (day2_rename is next).
    # Strip its markers explicitly so cleanup here is actually complete,
    # not merely "terminated and hoping the sweep does not notice".
    awsl ec2 terminate-instances --instance-ids "$BREAK_COLLISION_ID" >/dev/null 2>&1 || true
    awsl ec2 delete-tags --resources "$BREAK_COLLISION_ID" --tags Key=tofu-estate Key=tofu-address Key=tofu-slot >/dev/null 2>&1 || true
    [ "$BREAK_PLAN_RC" -ne 0 ] \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -20; fail "BREAK=replace: the plan succeeded with two live instances claiming the same tofu-address/tofu-slot - it must report the collision, not propose nothing"; }
    grep -qF 'Indistinguishable instances without per-instance markers' <<< "$BREAK_PLAN_OUT" \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -20; fail "BREAK=replace: the plan failed for a reason other than the fungible-slot collision - this stage's check is not load-bearing"; }
    grep -qF "$INSTANCE_ID" <<< "$BREAK_PLAN_OUT" \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -20; fail "BREAK=replace: the collision refusal does not name the real, still-valid instance ($INSTANCE_ID)"; }
    grep -qF "$BREAK_COLLISION_ID" <<< "$BREAK_PLAN_OUT" \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -20; fail "BREAK=replace: the collision refusal does not name the manufactured duplicate ($BREAK_COLLISION_ID)"; }
    log "  BREAK=replace: caught - choudoufu correctly refused with \"Indistinguishable instances without per-instance markers\", naming both $INSTANCE_ID and $BREAK_COLLISION_ID, rather than silently proposing nothing - the Break text's own outcome"
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

    # NOT "choudoufu output": this estate writes no terraform.tfstate at
    # all under the live block (record-based), and "output -raw" against
    # that is a real, separate, already-documented finding (PART
    # GREENFIELD's own note, above) - "No outputs found" under a
    # stateless record-backed run. Found by its marker instead: the new
    # instance is the one carrying the SAME tofu-address in running/
    # pending state, in an account with only one other (now-terminated)
    # instance under that estate ever having claimed it.
    F_NEW_ID="$(awsl ec2 describe-instances \
      --filters "Name=tag:tofu-address,Values=module.ec2_complete.aws_instance.this:0" "Name=instance-state-name,Values=running,pending" \
      --query "Reservations[0].Instances[0].InstanceId" --output text)"
    [ -n "$F_NEW_ID" ] && [ "$F_NEW_ID" != "None" ] && [ "$F_NEW_ID" != "$INSTANCE_ID" ] \
      || fail "could not find a new, different, running instance carrying module.ec2_complete's tofu-address after the replace (got '$F_NEW_ID')"
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
    if [ "$F_FINAL_PLAN_RC" -ne 0 ]; then
      printf '%s\n' "$F_FINAL_PLAN_OUT" | tail -40
      # THE TERMINATED-CLAIMANT WALL, named rather than reported as "exited
      # 1". A default destroy-then-create replace leaves the old instance in
      # `terminated` state still carrying this estate's tofu-estate,
      # tofu-address and tofu-slot tags. That is real AWS's own documented
      # behaviour, not an emulator artefact - confirmed here directly with
      # no tofu in the loop against the pinned image: run-instances,
      # terminate-instances, then describe-instances (returns the instance,
      # State.Name=terminated), describe-tags on the terminated id (returns
      # the markers) and resourcegroupstaggingapi get-resources (still lists
      # its ARN). So the estate-wide tag sweep legitimately sees TWO live
      # claimants of the same declared count address, and the count binding
      # path refuses ("Indistinguishable instances without per-instance
      # markers ... Count instances are a fungible set").
      #
      # discovery.go's classifyOrphans already solves exactly this shape one
      # function over, for an UNDECLARED address: recordCurrentClaimant
      # disambiguates from the estate's own identity record, which
      # the foundation-order ruling (#388) item 1 makes
      # authoritative for "which live object does this address own right
      # now", and which the replace's own apply already rewrote to the new
      # instance's id. The declared count-instance path has no equivalent.
      if grep -qF 'Indistinguishable instances without per-instance markers' <<< "$F_FINAL_PLAN_OUT"; then
        # Every live claimant of the replaced address, with the state and
        # the two markers the refusal turns on, read through the AWS CLI.
        # Without this the log says only "2 live aws_instance resources
        # claim ..." and a reader cannot tell whether the terminated ghost
        # kept a distinct tofu-slot (so the disambiguation exists and was
        # not used) or shares the survivor's (so it does not).
        F_CLAIMANTS="$(awsl ec2 describe-instances \
          --filters "Name=tag:tofu-address,Values=module.ec2_complete.aws_instance.this:0" \
          --query "Reservations[].Instances[].[InstanceId,State.Name,Tags[?Key=='tofu-slot']|[0].Value]" \
          --output text 2>&1 | awk 'NF{printf "%s(%s,slot=%s) ", $1, $2, $3}')"
        log "  the live claimants of module.ec2_complete.aws_instance.this[0], id(state,slot), via the AWS CLI: $F_CLAIMANTS"
        fail "choudoufu refuses where stock proceeds (HANDOFF's first row): after the replace applied cleanly, the post-replace plan refuses with \"Indistinguishable instances without per-instance markers\" because the TERMINATED old instance still carries this estate's tofu-estate/tofu-address/tofu-slot tags and the estate-wide tag sweep counts it as a second live claimant of module.ec2_complete.aws_instance.this[0]. The claimants this run measured, id(state,slot) via the AWS CLI: $F_CLAIMANTS - so the refusal's own suggested discriminator cannot break the tie either: both carry the SAME tofu-slot, which live/MARKERS.md permits by design (\"a slot whose resource has been deleted may be assigned again later\"), so the replacement legitimately took the retired instance's slot back. Stock's own post-replace plan is empty: its state names one instance id and it never asks the account what else claims the address. The lingering tags are real AWS behaviour, confirmed against the pinned emulator with no tofu in the loop (run-instances, terminate-instances, then describe-instances/describe-tags/resourcegroupstaggingapi all still return the terminated id and its markers), so this is not an emulator gap to fix in floci. The fix belongs in the declared count-instance binding path, which needs the discipline discovery.go's classifyOrphans already applies to an UNDECLARED address: recordCurrentClaimant disambiguates from the estate's own identity record (authoritative per the foundation-order ruling (#388) item 1, and rewritten to the new id by the replace's own apply) and returns a survivor only when EXACTLY one candidate matches. Not fixed in this script-only pass"
      fi
      fail "the post-replace plan exited $F_FINAL_PLAN_RC"
    fi
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$F_FINAL_PLAN_OUT" \
      || { grep -E '^  #' <<< "$F_FINAL_PLAN_OUT"; fail "the post-replace plan proposes a resource change"; }
    log "  No changes. The replace is complete and invisible to the next plan - no marker collision."

    INSTANCE_ID="$F_NEW_ID"
    gauntlet_stage day2_replace pass "choudoufu: changing module.ec2_complete's ForceNew ami argument proposed exactly one instance replace at the same declared address, cascading into the eip (updated in-place) and the volume attachment (also replaced, instance_id is ForceNew there too) - 2 to add, 1 to change, 2 to destroy, matching F-ORACLE's own plan shape; applied cleanly; the old instance is confirmed terminated and the new instance carries the marker, both via the AWS CLI; the local record store's record at the same address now names the new instance's id, not the terminated one ($F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID); the next plan proposes no resource action; BREAK=replace confirms a manufactured marker collision is reported loudly (\"Indistinguishable instances without per-instance markers\", naming both live instances) rather than silently proposed as nothing - internal/live/discovery/supersededclaimant.go (#849) tombstones only what an apply actually destroyed, so a live duplicate with no tombstone is never pruned away. Scope note: this exercises OpenTofu's default destroy-then-create ordering, not the create_before_destroy variant the stage's Title names - see this section's own header comment and corpus-sqs-basic's matching one."
  fi
  gauntlet_end_stage


gauntlet_begin_stage day2_rename
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
  # RE-VERIFIED against current main (re-verify-day2_remove unit, 2026-08):
  # this used to be zero churn (this estate's day2_remove/greenfield both
  # cleared for real on this same shape). Root cause is now precisely
  # named, not just "the day2_rename stage activation itself": 610511fb73
  # (internal/live/discovery/recordorphan_read.go, #405's day2_remove fix)
  # added recordOrphanReadSweep, which reads the record store for any
  # UNTAGGABLE type's undeclared old-address record and proposes destroying
  # it - generically, not just for the three IAM types its own package
  # comment names as "today"'s population, because its filter is only
  # "untaggable + has a persisted identity record", nothing type-specific.
  # Its own rename-safety check (the `pending` map, built from
  # res.Unbound) only recognizes "a declared instance of the SAME address
  # is unclaimed" - it never consults moved.Aliases/moved.Honoured(req.Config)
  # the way the marker path already does. So a moved block relocating
  # module.vpc now destroys its untaggable derived children
  # (aws_route/aws_route_table_association) instead of matching them under
  # the new address; the taggable resources (subnets, route tables, VPC
  # itself) still move correctly via the marker path, which DOES follow
  # moved blocks. SAME root cause, independently confirmed on
  # corpus-giantswarm-crossplane (aws_iam_role_policy family),
  # corpus-rds-complete-postgres (aws_security_group_rule) and
  # corpus-security-group-complete (aws_vpc_security_group_rules_exclusive)
  # in this same unit - a generic gap reaching at least these four estates,
  # not this one's alone. live-mv does not hit this
  # (RecordStore.MoveRecord re-keys the store directly, 8bd0d47e4e); only a
  # bare HCL `moved` block does. Not fixed here - a Go change, out of scope
  # for this script-only re-verification unit. Because fail() exits
  # immediately, day2_remove's own post-fix status for this estate could
  # not be independently re-measured this run either.
  grep -qE '^  # .+ will be (destroyed|created)' <<< "$MOVED_PLAN_OUT" \
    && { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu defect: the moved-block rename of module.vpc proposes a create/destroy for one of its untaggable derived children (aws_route/aws_route_table_association) instead of matching them structurally under the parent's new address - not zero churn. Root cause: 610511fb73's recordOrphanReadSweep has no moved-block awareness (see the comment immediately above this assertion) - the SAME generic gap corpus-giantswarm-crossplane, corpus-rds-complete-postgres and corpus-security-group-complete independently hit in this same unit. day2_remove's own post-fix status for this estate could not be re-measured this run because of it."; }
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
  gauntlet_begin_stage day2_remove
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
  gauntlet_end_stage
fi
gauntlet_end_stage

gauntlet_end_stage
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
