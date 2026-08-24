#!/usr/bin/env bash
set -uo pipefail

# The five-stage real-estate crossing (live/corpus-crossing-manifest.json)
# for XanCloud/xancloud-iac (live/corpus-manifest.json, pinned by TAG v0.2.0
# AND commit) - the fourth OpenTofu-native crossing, and a genuinely
# different repository from the three already crossed (sumaform, hongbomiao
# x2, overture-tiles).
#
# THE EVIDENCE. A young (first commit 2026-03-08), single-maintainer,
# actively-developed AWS landing-zone accelerator with a real PR-based
# history running through #17, the latest six days before this pin. Its
# README badges and prose describe it as "OpenTofu-first" throughout -
# "MPL 2.0 license, native state encryption, S3 locking without DynamoDB.
# No vendor lock-in" - required_version is pinned to ">= 1.11.0", and its
# own docs/TROUBLESHOOTING.md (written in Spanish, as the README says its
# project docs are) carries a dedicated "OpenTofu general" section
# documenting real `tofu init -upgrade`/`tofu` CLI usage - operational
# evidence of actually running it, not just a badge. Its own HCL is plain
# .tf, not .tofu-suffixed, and it ships no CI workflow exercising a
# tofu-only binary the way overture-tiles's does, so - like overture-tiles -
# it does not itself demonstrate an OpenTofu-exclusive language construct;
# the "native state encryption" claim resolves to ordinary S3 SSE-KMS plus
# `use_lockfile` on the backend, both of which Terraform 1.10+ also
# supports. Weaker self-description evidence than hongbomiao's genuine
# .tofu files, comparable to sumaform/overture-tiles.
#
# THE ESTATE: blueprints/landing-zone-basic, the module's own real root
# (not a leaf module needing a wrapper), run with its own
# examples/dev.tfvars values, patched only via the module's own published
# variables (never a code edit) for the reason below: one VPC (10.10.0.0/16)
# across 2 AZs, a single NAT gateway, 6 VPC endpoints (1 gateway + 5
# interface), CloudWatch-destination flow logs, plus the account-level IAM
# baseline's account-alias resource, enabled via is_account_owner = true,
# the module's own documented "exactly one environment per account" toggle.
# 28 resource instances total - VPC (27) + aws_iam_account_alias (1).
#
# FIVE REAL FLOCI GAPS FOUND, ALL FILED, NONE ROUTED AROUND SILENTLY: the
# module's own defaults also wire up CloudTrail (a dedicated KMS key, an
# Object-Lock-enabled S3 bucket, the trail itself) and three more IAM
# baseline toggles (S3 account public-access block, IAM password policy,
# IAM Access Analyzer, EC2 IMDSv2 regional defaults - four toggles, three
# blocked). Running the FULL unmodified blueprint first (confirmed for
# real, not assumed) hit five distinct, independent floci gaps, each
# confirmed against floci's own source before filing:
#   - lex00/floci#73: S3Control PutPublicAccessBlock (account-level Block
#     Public Access) has no route in S3ControlController.java at all - 404.
#   - lex00/floci#74: IAM UpdateAccountPasswordPolicy has no case in
#     IamQueryHandler.java's action switch - 400 UnsupportedOperation.
#   - lex00/floci#75: IAM Access Analyzer is a WHOLE missing service - no
#     AccessAnalyzer package anywhere under floci's source - 404.
#   - lex00/floci#76: EC2 ModifyInstanceMetadataDefaults appears nowhere in
#     floci's source - 400 UnsupportedOperation.
#   - lex00/floci#77: CloudTrail's tagging trio (ListTags/AddTags/
#     UntagTags) has no case in CloudTrailJsonHandler.java's action switch,
#     so ANY tagged aws_cloudtrail resource fails immediately after a
#     successful CreateTrail, because the AWS provider always reads tags
#     back as part of its post-create Read - a real blocker for every
#     tagged CloudTrail trail against this floci image, not just this
#     estate's.
# None of these is a choudoufu defect and none is this script's to fix
# (HANDOFF.md, "Traps": floci gaps are a floci work item, not a reason to
# skip the estate) - each is filed with the exact source evidence above.
# One MORE real, independent gap used to remain past this point, at stage 3
# - lex00/floci#87, not account-governance shaped and not scoped around.
# RESOLVED 2026-08-21 (lex00/floci#96, squash-merged as 17c7f7ef, published
# sha256:cdd50ec0...): CreateFlowLogs/DescribeFlowLogs now carry
# DeliverLogsPermissionArn, so aws_flow_log.iam_role_arn round-trips and the
# force-replace this stage used to assert is gone. What remained after that
# was the pre-existing #327 NAT-gateway residual - a single in-place update
# to aws_nat_gateway.this["main-0"], "+ regional_nat_gateway_address =
# (known after apply)". RESOLVED 2026-08-21 (choudoufu, not floci):
# regional_nat_gateway_address is Computed only (this NAT gateway is not a
# "regional" one - connectivity_type is plain "public", confirmed directly
# against floci's own DescribeNatGateways response for this object, which
# carries no such field at all), and internal/live/projection's residue
# mechanism (issue #275/#327) used to refuse to consider a purely Computed
# attribute a residue candidate at all ("it cannot be set in configuration,
# so there is nothing to remember"). That reasoning undercounted a real
# case: the provider's Read does not re-derive this attribute from a bare
# identity-only prior, it leaves whatever the prior held (null, from
# [identityOnly]), and OpenTofu's plan marks a null Computed attribute
# "known after apply" forever - exactly the shape residue.go's own
# classifyResidue exists to catch, just for a Computed-only attribute
# rather than an Optional+Computed one. Fixed generically in
# internal/live/projection/residue.go: residueCandidates and fillResidue no
# longer ask whether an attribute is Required or Optional, only whether it
# is the identity, sensitive, write-only, or NestedType-shaped - safety
# comes from classifyResidue's own two-read discriminator (a candidate is
# only ever recorded when the provider provably does not source it from
# the remote), not from that schema-shape filter. Re-crossed for real: the
# NAT gateway residue (regional_nat_gateway_address = an empty set, the
# genuine live value) is now recorded during migrate's live-import
# -approve, filled back into the stateless prior on the first live-plan,
# and stage 3's plan is empty. Every `aws_*` type whose schema carries a
# purely Computed, non-identity, non-sensitive, non-write-only, non-nested
# attribute the provider's Read does not re-derive from a bare prior is
# reached by this same fix - see the PR for the corpus-wide candidate
# count. Stage 4 is attempted next, in this same later unit: applying the
# empty plan is a genuine no-op and the tofu-estate-tagged object count is
# unchanged before and after. Stage 5 (drift and reconverge) is not yet
# written.
#
# GitHub issue #347's own history is worth keeping here rather than only in
# the tracker: lex00/floci#78 (CreateFlowLogs ignoring TagSpecifications)
# used to keep aws_flow_log entirely OUTSIDE this crossing's diagnostic
# surface - the tag write silently no-oped, so live-import never had a
# marker to find and stage 3 never proposed anything for it at all. #78 is
# now fixed, the flow log migrates and stamps cleanly, and that is what
# revealed lex00/floci#87 underneath it: floci's DescribeFlowLogs response
# never carried the IAM role ARN a CloudWatch-Logs-destination flow log
# needs, so the AWS provider's aws_flow_log Read always wrote back an empty
# iam_role_arn - confirmed NOT a choudoufu residue-mechanism gap (issue
# #327's residueCandidates/ResidueStore mechanism, re-derived per resource
# type from its own schema, reaches aws_flow_log exactly as it reaches every
# other type and correctly DECLINED to record iam_role_arn as residue,
# because the second of its two classification reads did not reproduce the
# applied value either - the provider was not preserving a stateless prior,
# it was genuinely never being told the value by floci) but a floci gap that
# also corrupted a real, stateful stock `tofu apply`'s own terraform.tfstate:
# stage 1's plain cold-deploy state used to already carry iam_role_arn=""
# immediately after a real, non-choudoufu apply, which is what proved this
# was not specific to choudoufu's stateless replan design. RESOLVED as
# lex00/floci#96 above - re-crossed for real against the fixed image
# 2026-08-21: stage 1's cold-deploy state now carries the real IAM role ARN,
# and stage 3 no longer proposes any change to aws_flow_log at all.
# Separately, and not tracked by any of the above: #328
# (aws_default_security_group.revoke_rules_on_delete) and the two
# aws_route_table in-place updates it used to cause no longer appear in
# stage 3's plan at all as of this re-cross - not investigated further
# here, since stage 3's assertions below only need to match what the plan
# actually proposes today.
# What this script actually runs is scoped down via the module's own
# published toggles (cloudtrail_enabled=false;
# iam_baseline_enable_s3_block/password_policy/access_analyzer/
# imdsv2_default=false, keeping only account_alias) so the crossing tests
# a genuinely reduced-but-real estate throughout, not choudoufu silently
# abandoning management of something the cold deploy still created - the
# same discipline corpus-sumaform-aws and corpus-s3-bucket-complete use
# for their own real, routed-around gaps.
#
# WHAT THIS SCRIPT PATCHES, AND WHY (both applied identically to the PLAIN
# cold-deploy copy and the ESTATE copy, so the same configuration is
# exercised both times - never a choudoufu-only shortcut):
#   - blueprints/landing-zone-basic/providers.tf: the module's own
#     `provider "aws" { region = var.region }` has no emulator wiring (it
#     is meant to run against real AWS) - patched to add
#     access_key/secret_key/skip_credentials_validation/
#     skip_metadata_api_check/s3_use_path_style, the same floci-pointing
#     block every other crossing in this campaign uses. NOT included:
#     skip_requesting_account_id - tried first, and it turned the S3
#     Account Public Access Block failure from a real server-side 404 into
#     a client-side "AccountId must only contain a-z, A-Z, 0-9 and `-`"
#     endpoint-resolution error, because that flag stops the AWS provider
#     from resolving its own account ID internally, which S3 Control's
#     account-scoped endpoint template needs. Every prior crossing sets
#     this flag safely because none of them calls an account-scoped API
#     that needs the provider's own cached account ID rather than an
#     explicit `data.aws_caller_identity` call in HCL (which always works
#     regardless of this flag) - confirmed by testing both ways before
#     settling on this one.
#   - blueprints/landing-zone-basic/versions.tf: `backend "s3" {}` (a
#     partial backend needing -backend-config at init time, pointing at a
#     real state bucket this crossing has no reason to bootstrap) is
#     removed entirely, falling back to the implicit local backend - the
#     same choice corpus-hongbomiao-labelbox's own root wiring makes.
#     aws provider version is pinned to the exact release this fork's own
#     e2e suite standardizes on (= 6.59.0) instead of the module's own
#     unpinned "~> 6.0", for reproducibility across runs.
#   - examples/dev.tfvars: `region = "<region>"` is a literal placeholder
#     the module's own comment says must be filled in before use ("Debe
#     coincidir con la región del perfil AWS y del state backend") - not a
#     real value the repository ships, so this crossing fills it with
#     "us-west-2", the same substitution class as corpus-hongbomiao-
#     labelbox's own external_id placeholder.
# Every other file - all of blueprints/landing-zone-basic's own .tf logic
# and every file under modules/networking/vpc, modules/security/cloudtrail,
# modules/identity/iam-baseline - is copied byte-identical, confirmed by
# DELTA below. The five floci-gapped resources are disabled purely via
# TF_VAR_* environment variables the same as every other input this script
# supplies (see "TF_VAR_*" below the floci-health-check for why), never a
# code edit to the copied files - modules/security/cloudtrail and 3 of
# modules/identity/iam-baseline's 4 feature toggles are real, unmodified
# code that simply never runs in this crossing.
#
# STAGES:
#   1. COLD DEPLOY   plain `tofu apply` (real OpenTofu core, no choudoufu),
#                     the unmodified blueprint + its three real modules -
#                     PASS.
#   2. MIGRATE       `choudoufu live-import -approve` against that cold
#                     state - PASS.
#   3. TEST PLAN     delete the state file, `choudoufu live-plan` - PASS as of
#                     the residue-mechanism fix above: both prior gaps
#                     (lex00/floci#87's aws_flow_log.iam_role_arn, and the
#                     #327 aws_nat_gateway.regional_nat_gateway_address
#                     residual) are resolved, the plan is genuinely empty,
#                     and the VPC's own identity is re-asserted against the
#                     AWS CLI after the state file is deleted.
#   4. TEST APPLY    apply the empty plan from stage 3 - PASS: a genuine
#                     no-op (0 added, 0 changed, 0 destroyed), and the
#                     tofu-estate-tagged object count (resourcegroupstagging
#                     api) is identical before and after.
#   5. DRIFT + RECONVERGE  mutate the VPC's own Name tag out of band via the
#                     AWS CLI; live-plan proposes fixing exactly that one
#                     object, the stock oracle (the still-plain $PLAIN
#                     working directory) proposes the identical change, and
#                     apply reconverges it - see the stage's own block below
#                     for the full account.
#
#   bash live/e2e/corpus-xancloud-iac/run.sh
#
# Needs Docker, the AWS CLI, and the real `tofu` binary on PATH for stage 1.
# .corpus is read, never written: blueprints/landing-zone-basic and the
# three modules it calls are copied out to a scratch directory first, same
# as every other corpus crossing.
#
# Env overrides:
#   TOFU_BIN      path to a prebuilt choudoufu binary; skips the go build.
#   FLOCI_PORT    host port for the emulator (default 4727, clear of every
#                 other corpus-*/reference-* script's own default).
#   FLOCI_IMAGE   the emulator image; defaults to the digest pin in
#                 live/floci-image.
#   BREAK         set to 1 to corrupt stage 3's VPC identity assertion,
#                 proving it is load-bearing - this exits before stage 5 is
#                 ever reached, so it does not exercise stage 5's own break.
#   BREAK_STAGE5  set to 1 to drift a SECOND object in stage 5 (the internet
#                 gateway's Name tag, alongside the VPC's), proving the
#                 single-object assertion is load-bearing - same convention
#                 as live/e2e/corpus-mastino-dns/run.sh's own BREAK_STAGE5.
#   DEBUG_KEEP    set to 1 to skip the exit trap: the floci container and
#                 the WORK directory are left behind for inspection.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SRC="$ROOT/.corpus/xancloud-iac"
WORK="$(mktemp -d)"
PLAIN="$WORK/plain"
ESTATE="$WORK/estate"
FLOCI_PORT="${FLOCI_PORT:-4727}"
FLOCI_NAME="choudoufu-corpus-xancloud-iac-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"
REGION="us-west-2"
PROJECT="xancloud"
ENVIRONMENT="dev"
ESTATE_NAME="xancloud-iac-crossing"
NAME_PREFIX="${PROJECT}-${ENVIRONMENT}"
VPC_NAME="${NAME_PREFIX}-main-vpc"

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
command -v tofu >/dev/null 2>&1 || fail "the real tofu binary is not on PATH - required for stage 1"
for f in blueprints/landing-zone-basic/main.tf blueprints/landing-zone-basic/providers.tf \
         blueprints/landing-zone-basic/versions.tf blueprints/landing-zone-basic/variables.tf \
         blueprints/landing-zone-basic/locals.tf blueprints/landing-zone-basic/outputs.tf \
         blueprints/landing-zone-basic/examples/dev.tfvars \
         modules/networking/vpc/main.tf modules/security/cloudtrail/main.tf \
         modules/identity/iam-baseline/main.tf; do
  [ -f "$SRC/$f" ] || fail "$SRC/$f is missing - fetch XanCloud/xancloud-iac at the pin in live/corpus-manifest.json first"
done
log "  cold deploy binary: tofu ($(tofu version | head -1))"

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

# copy_estate <destdir>: blueprints/landing-zone-basic plus the three real
# modules it calls via "../../modules/...", unmodified, preserving the
# relative path layout the module's own source= arguments need.
copy_estate() {
  local dest="$1"
  mkdir -p "$dest/blueprints/landing-zone-basic/examples"
  for f in main.tf providers.tf versions.tf variables.tf locals.tf outputs.tf; do
    cp "$SRC/blueprints/landing-zone-basic/$f" "$dest/blueprints/landing-zone-basic/$f"
  done
  cp "$SRC/blueprints/landing-zone-basic/examples/dev.tfvars" "$dest/blueprints/landing-zone-basic/examples/dev.tfvars"

  mkdir -p "$dest/modules/networking/vpc" "$dest/modules/security/cloudtrail" "$dest/modules/identity/iam-baseline"
  for f in main.tf locals.tf outputs.tf variables.tf versions.tf; do
    cp "$SRC/modules/networking/vpc/$f" "$dest/modules/networking/vpc/$f"
    cp "$SRC/modules/security/cloudtrail/$f" "$dest/modules/security/cloudtrail/$f"
    cp "$SRC/modules/identity/iam-baseline/$f" "$dest/modules/identity/iam-baseline/$f"
  done
}

# DELTA check helper - every file this crossing copies must be
# byte-identical to the pinned commit BEFORE any patch below touches it.
delta_check() {
  local dest="$1"
  for f in main.tf variables.tf locals.tf outputs.tf; do
    diff -q "$SRC/blueprints/landing-zone-basic/$f" "$dest/blueprints/landing-zone-basic/$f" >/dev/null \
      || fail "blueprints/landing-zone-basic/$f differs from the pinned commit before patching - the corpus pin has moved"
  done
  for m in networking/vpc security/cloudtrail identity/iam-baseline; do
    for f in main.tf locals.tf outputs.tf variables.tf versions.tf; do
      diff -q "$SRC/modules/$m/$f" "$dest/modules/$m/$f" >/dev/null \
        || fail "modules/$m/$f differs from the pinned commit - this crossing must run the real, unmodified module"
    done
  done
}

# provider_and_backend_patch <destdir> - the two DELTAs this script's
# header documents: emulator wiring on the provider block, and the S3
# partial backend removed (falls back to local state). Applied to
# providers.tf/versions.tf ONLY, after delta_check has already confirmed
# every other file is untouched.
provider_and_backend_patch() {
  local dest="$1"
  cat > "$dest/blueprints/landing-zone-basic/providers.tf" <<EOF
provider "aws" {
  region = var.region

  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true
}
EOF
  cat > "$dest/blueprints/landing-zone-basic/versions.tf" <<EOF
terraform {
  required_version = ">= 1.11.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.59.0"
    }
  }
}
EOF
}

# region_patch <destdir> - examples/dev.tfvars ships region = "<region>", a
# literal placeholder the module's own comment says must be filled in
# before use, not a real value the repository ships.
region_patch() {
  local dest="$1"
  python3 - "$dest/blueprints/landing-zone-basic/examples/dev.tfvars" <<PYEOF
import sys
p = sys.argv[1]
s = open(p).read()
old = 'region      = "<region>"'
assert old in s, "region_patch did not find the <region> placeholder - the corpus pin has moved"
open(p, "w").write(s.replace(old, 'region      = "$REGION"', 1))
PYEOF
}

copy_estate "$PLAIN"
delta_check "$PLAIN"
log "  DELTA confirmed: blueprints/landing-zone-basic and all three modules are byte-identical to the pinned commit"
provider_and_backend_patch "$PLAIN"
region_patch "$PLAIN"
log "  cold-deploy copy patched: emulator provider wiring, S3 backend removed (local state), examples/dev.tfvars region filled in"

copy_estate "$ESTATE"
delta_check "$ESTATE"
provider_and_backend_patch "$ESTATE"
region_patch "$ESTATE"
cat >> "$ESTATE/blueprints/landing-zone-basic/versions.tf" <<EOF

# Appended by this crossing, not part of xancloud-iac's own versions.tf -
# same convention as every other corpus-*/run.sh script's ESTATE copy.
EOF
# The live block is added to main.tf's own terraform{} block is not an
# option here (versions.tf owns terraform{}, main.tf has none) - append it
# to versions.tf's terraform{} block directly instead.
python3 - "$ESTATE/blueprints/landing-zone-basic/versions.tf" <<'PYEOF'
import sys
p = sys.argv[1]
s = open(p).read()
marker = "  }\n}\n"
i = s.rindex(marker)
live_block = '''
  live {
    estate = "xancloud-iac-crossing"
    record_store "local" {
      path = ".tofu-records"
    }
  }
}
'''
s = s[:i] + "  }\n" + live_block
open(p, "w").write(s)
PYEOF
log "  estate copy written to $ESTATE (stages 2-5: choudoufu, live block added to versions.tf's terraform{} block)"

# ── 1. floci ─────────────────────────────────────────────────────────────
log "=== 1. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"s3"' <<< "${HEALTH:-}" && break
  sleep 2
done
grep -q '"s3"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (s3) at $ENDPOINT"
log "  healthy"

export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" AWS_ENDPOINT_URL="$ENDPOINT"

# TF_VAR_* rather than -var-file=examples/dev.tfvars on each invocation:
# `choudoufu live-import` (stage 2) has no -var/-var-file flag at all - it
# only takes -state/-estate/-approve (its own -help confirms this) - so
# every variable this blueprint needs (var.region has no default; neither
# do environment/vpcs) has to reach it through the environment instead, the
# one input channel every subcommand below shares. Values below are
# dev.tfvars's own, verbatim, plus this script's five SCOPE overrides
# (lex00/floci#73-#77, see header) - the on-disk examples/dev.tfvars copy
# stays purely for DELTA/provenance, no command below reads it.
export TF_VAR_region="$REGION"
export TF_VAR_environment="$ENVIRONMENT"
export TF_VAR_project="$PROJECT"
export TF_VAR_is_account_owner=true
export TF_VAR_account_alias="xancloud-dev"
export TF_VAR_vpcs='{"main":{"cidr":"10.10.0.0/16","azs":2,"single_nat":true,"vpc_endpoints":["s3","ssm","ssmmessages","ecr.api","ecr.dkr","logs"],"flow_logs_destination":"cloudwatch"}}'
export TF_VAR_cloudtrail_enabled=false
export TF_VAR_iam_baseline_enable_s3_block=false
export TF_VAR_iam_baseline_enable_password_policy=false
export TF_VAR_iam_baseline_enable_access_analyzer=false
export TF_VAR_iam_baseline_enable_imdsv2_default=false

# ══════════════════════════════════════════════════════════════════════════
# STAGE 1: COLD DEPLOY - plain tofu apply, no live block, no choudoufu
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=cold_deploy
log "=== STAGE 1: cold deploy (plain tofu apply, the real unmodified blueprint) ==="
( cd "$PLAIN/blueprints/landing-zone-basic" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN/blueprints/landing-zone-basic" && tofu init -input=false -no-color 2>&1 | tail -40 ); fail "stage 1 init failed"; }
COLD_OUT="$(cd "$PLAIN/blueprints/landing-zone-basic" && tofu apply -input=false -auto-approve -no-color 2>&1)"; COLD_RC=$?
[ "$COLD_RC" -eq 0 ] || { printf '%s\n' "$COLD_OUT" | tail -80; fail "stage 1 (cold deploy) failed"; }
grep -qE 'Apply complete! Resources: 28 added, 0 changed, 0 destroyed' <<< "$COLD_OUT" \
  || { grep -E 'Apply complete' <<< "$COLD_OUT"; fail "stage 1 did not create exactly 28 resources - the module's own shape may have moved"; }
log "  $(grep -E 'Apply complete' <<< "$COLD_OUT")"
[ -f "$PLAIN/blueprints/landing-zone-basic/terraform.tfstate" ] || fail "stage 1 left no state file to migrate from"

UNMARKED="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE_NAME" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$UNMARKED" = "0" ] || fail "plain tofu's own objects already carry tofu-estate=$ESTATE_NAME before migration - this crossing proves nothing"
log "  confirmed unmarked: 0 objects carry tofu-estate=$ESTATE_NAME before migration"

log ""
log "STAGE 1 (cold deploy): PASS"
log ""
gauntlet_stage cold_deploy pass "28 resources, genuinely cold, genuinely unmarked"

# ══════════════════════════════════════════════════════════════════════════
# PART D-ORACLE: RENAME, stock oracle (day2_rename, live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
#
# Two taggable, single-key for_each resources inside module "vpc"'s own
# source (never the module CALL itself): a `moved` block renames
# aws_iam_role.flow_logs, and "choudoufu live-mv" (below, after
# drift_reconverge) renames aws_eip.nat with no moved block at all. Both
# rename and their moved block are written into the COPIED module source
# (modules/networking/vpc/main.tf - never $SRC, never $PLAIN), since a
# for_each resource's own moved block lives alongside the resource, using
# local (unqualified) addresses - the same convention
# corpus-leynos-monitoring's own day2_rename uses for a resource inside a
# module. BREAK=1's rename-without-moved control (D1 below) always targets
# aws_eip.nat, never aws_iam_role.flow_logs: the IAM role's identity is
# deterministically client-named (its own `name` argument), which makes an
# unmoved rename ambiguous rather than a clean destroy+create (see
# corpus-hongbomiao-labelbox's header for the same finding, verified
# there); an EIP's identity is its server-assigned allocation id, which
# genuinely cannot be re-derived from config alone. The stock oracle (real
# tofu, plain .tf) runs the same two renames, through moved blocks only, on
# a copy of cold_deploy's own state - before choudoufu or live-import ever
# touch these objects.
CURRENT_STAGE=day2_rename
log "=== D-ORACLE: stock tofu, the same two renames through moved blocks, on cold_deploy's own state ==="
PLAIN_ORACLE="$WORK/plain-oracle"
cp -r "$PLAIN" "$PLAIN_ORACLE"
VPC_MAIN="$PLAIN_ORACLE/modules/networking/vpc/main.tf"
sed -i.bak 's/resource "aws_iam_role" "flow_logs" {/resource "aws_iam_role" "flow_logs_renamed" {/' "$VPC_MAIN"
sed -i.bak 's/aws_iam_role\.flow_logs\[each\.key\]/aws_iam_role.flow_logs_renamed[each.key]/g' "$VPC_MAIN"
sed -i.bak 's/resource "aws_eip" "nat" {/resource "aws_eip" "nat_renamed" {/' "$VPC_MAIN"
sed -i.bak 's/aws_eip\.nat\[each\.key\]/aws_eip.nat_renamed[each.key]/g' "$VPC_MAIN"
rm -f "$VPC_MAIN.bak"
cat >> "$VPC_MAIN" <<'EOF'

moved {
  from = aws_iam_role.flow_logs
  to   = aws_iam_role.flow_logs_renamed
}

moved {
  from = aws_eip.nat
  to   = aws_eip.nat_renamed
}
EOF
( cd "$PLAIN_ORACLE/blueprints/landing-zone-basic" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_ORACLE/blueprints/landing-zone-basic" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_rename stock oracle's reinit failed"; }
ORACLE_PLAN_OUT="$(cd "$PLAIN_ORACLE/blueprints/landing-zone-basic" && tofu plan -input=false -no-color 2>&1)"; ORACLE_PLAN_RC=$?
[ "$ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -40; fail "the day2_rename stock oracle plan exited $ORACLE_PLAN_RC"; }
grep -qE '^  # .+ will be (destroyed|created)' <<< "$ORACLE_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock proposes a destroy or create for a rename carried entirely by moved blocks - the oracle itself is not zero-churn"; }
grep -qF 'Plan: 0 to add, 0 to change, 0 to destroy.' <<< "$ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -10; fail "stock's rename plan is not a true no-op"; }
log "  stock: zero churn on cold_deploy's own state - both moves report only their move, no attribute diff at all"

CURRENT_STAGE=migrate

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 2: choudoufu live-import ==="
( cd "$ESTATE/blueprints/landing-zone-basic" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ESTATE/blueprints/landing-zone-basic" && "$TOFU" init -input=false -no-color 2>&1 | tail -40 ); fail "estate init failed"; }

log "--- 2a: live-import, read-only first ---"
IMPORT_OUT="$(cd "$ESTATE/blueprints/landing-zone-basic" && "$TOFU" live-import -state="$PLAIN/blueprints/landing-zone-basic/terraform.tfstate" -estate="$ESTATE_NAME" -no-color 2>&1)"; IMPORT_RC=$?
[ "$IMPORT_RC" -eq 0 ] || { printf '%s\n' "$IMPORT_OUT" | tail -60; fail "live-import (dry run) failed"; }
grep -qF "No tag has been written." <<< "$IMPORT_OUT" || fail "the dry run wrote a tag - it must not"
log "  live-import dry run:"
printf '%s\n' "$IMPORT_OUT" | grep -E 'resource instance\(s\) are eligible|VERIFIED|DRIFTED|UNTAGGABLE|UNADMITTED|UNBOUND|PROBLEM' | sed 's/^/    /'

log "--- 2b: -approve ---"
APPROVE_OUT="$(cd "$ESTATE/blueprints/landing-zone-basic" && "$TOFU" live-import -state="$PLAIN/blueprints/landing-zone-basic/terraform.tfstate" -estate="$ESTATE_NAME" -approve -no-color 2>&1)"; APPROVE_RC=$?
[ "$APPROVE_RC" -eq 0 ] || { printf '%s\n' "$APPROVE_OUT" | tail -60; fail "live-import -approve failed"; }
log "  live-import -approve:"
grep -E 'newly stamped' <<< "$APPROVE_OUT" | sed 's/^/    /'

log ""
log "STAGE 2 (migrate): PASS"
log ""
gauntlet_stage migrate pass "live-import -approve completed cleanly against the cold state"
CURRENT_STAGE=test_plan

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - now genuinely empty. Both real gaps that used to
# reach this point are resolved (see header): lex00/floci#87 (fixed by
# lex00/floci#96) and the #327 NAT-gateway residual (fixed in this repo -
# residueCandidates/fillResidue no longer exclude a purely Computed
# attribute by schema shape alone).
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 3: no state file, live-plan (expected empty) ==="
rm -f "$ESTATE/blueprints/landing-zone-basic/terraform.tfstate" "$ESTATE/blueprints/landing-zone-basic/terraform.tfstate.backup"
[ ! -f "$ESTATE/blueprints/landing-zone-basic/terraform.tfstate" ] || fail "the state file is still there"

plan_into() { ( cd "$ESTATE/blueprints/landing-zone-basic" && "$TOFU" live-plan -input=false -no-color ); }
PLAN_OUT="$(plan_into 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -80; fail "live-plan exited $PLAN_RC"; }
[ ! -f "$ESTATE/blueprints/landing-zone-basic/terraform.tfstate" ] || fail "live-plan wrote a state file"

grep -qF "No changes. Your infrastructure matches the configuration." <<< "$PLAN_OUT" \
  || { grep -E '^  #' <<< "$PLAN_OUT"; fail "expected an empty plan ('No changes. Your infrastructure matches the configuration.') - if this moved, the documented cause above may have changed shape"; }
grep -qE '^  # .*aws_nat_gateway' <<< "$PLAN_OUT" \
  && fail "aws_nat_gateway has a proposed change again - the regional_nat_gateway_address residue fix may have regressed"
grep -qE '^  # .*aws_flow_log' <<< "$PLAN_OUT" \
  && fail "aws_flow_log has a proposed change again - lex00/floci#87 may have regressed"
log "  empty plan: no resource change proposed."

# Re-assert the VPC's identity directly against the AWS CLI, after the
# local state file was deleted - the answer below can only have come from
# the marker on the live object itself. The VPC itself is untouched by the
# gap above (it is not the changed address), so its own marker is exactly
# what a clean stage 3 would have shown.
VPC_ID="$(awsl ec2 describe-vpcs --filters "Name=tag:Name,Values=$VPC_NAME" --query 'Vpcs[0].VpcId' --output text)"
[ -n "$VPC_ID" ] && [ "$VPC_ID" != "None" ] || fail "could not find the VPC via the AWS CLI"
WANT_VPC_ADDR='module.vpc.aws_vpc.this:main'
if [ "${BREAK:-}" = "1" ]; then
  WANT_VPC_ADDR='module.vpc.aws_vpc.wrong_name:main'
  log "  BREAK=1: expecting a wrong tofu-address on the VPC on purpose - this check must fail"
fi
GOT_VPC_ADDR="$(awsl ec2 describe-vpcs --vpc-ids "$VPC_ID" --query "Vpcs[0].Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_VPC_ADDR" = "$WANT_VPC_ADDR" ] || fail "the VPC carries tofu-address=$GOT_VPC_ADDR, not $WANT_VPC_ADDR"
GOT_VPC_ESTATE="$(awsl ec2 describe-vpcs --vpc-ids "$VPC_ID" --query "Vpcs[0].Tags[?Key=='tofu-estate'].Value | [0]" --output text)"
[ "$GOT_VPC_ESTATE" = "$ESTATE_NAME" ] || fail "the VPC carries tofu-estate=$GOT_VPC_ESTATE, not $ESTATE_NAME"
log "  VPC $VPC_ID ($VPC_NAME) -> tofu-address=$GOT_VPC_ADDR tofu-estate=$GOT_VPC_ESTATE"

if [ "${BREAK:-}" = "1" ]; then
  fail "BREAK=1: the VPC's real tofu-address matched the WRONG expected value above without this script noticing - stage 3's assertion is not load-bearing"
fi

log ""
log "STAGE 3 (test plan): PASS"
log ""
gauntlet_stage test_plan pass "no resource change proposed"
CURRENT_STAGE=test_apply

# ══════════════════════════════════════════════════════════════════════════
# STAGE 4: TEST APPLY - apply the empty plan, assert a genuine no-op. The
# tagged-object count (resourcegroupstaggingapi, tofu-estate=$ESTATE_NAME) is
# read before and after; a no-op that left the count unchanged is the whole
# proof (live/GAUNTLET.md, stage 4's oracle).
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 4: test apply (apply the empty plan; object count unchanged) ==="
BEFORE_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE_NAME" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$BEFORE_N" != "0" ] || fail "0 objects carry tofu-estate=$ESTATE_NAME before the no-op apply - the tag query itself is broken"

APPLY2_OUT="$(cd "$ESTATE/blueprints/landing-zone-basic" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; APPLY2_RC=$?
[ "$APPLY2_RC" -eq 0 ] || { printf '%s\n' "$APPLY2_OUT" | tail -60; fail "the no-op apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY2_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY2_OUT"; fail "the apply of the empty plan was not a genuine no-op"; }

AFTER_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE_NAME" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$AFTER_N" = "$BEFORE_N" ] || fail "object count changed across a no-op apply: $BEFORE_N -> $AFTER_N"
[ ! -f "$ESTATE/blueprints/landing-zone-basic/terraform.tfstate" ] || fail "the no-op apply left a state file behind"
log "  genuine no-op: $BEFORE_N objects before, $AFTER_N after, no state file either time"

log ""
log "STAGE 4 (test apply): PASS"
log ""
gauntlet_stage test_apply pass "genuine no-op (0 added, 0 changed, 0 destroyed); tofu-estate-tagged object count unchanged at $BEFORE_N"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 5: DRIFT AND RECONVERGE
# ══════════════════════════════════════════════════════════════════════════
#
# The same $ESTATE estate, already stamped and already proven to plan and
# apply empty (stages 2-4), is the natural place to prove the OTHER
# direction: one live object changed out of band, directly through the AWS
# CLI, is detected and the fix is scoped to exactly that object - not "the
# whole estate looks different." The mutated attribute is the VPC's own
# Name tag (module.vpc.aws_vpc.this["main"]): "$VPC_NAME" in the config,
# changed live to "tampered-out-of-band" via `aws ec2 create-tags` - never
# through choudoufu. $VPC_ID is still the id read via the AWS CLI at stage
# 3. $PLAIN still holds a plain tofu working directory pointed at the same
# floci endpoint, with its own state file from stage 1's cold apply,
# untouched since - zero choudoufu involvement, same live objects - which is
# this stage's stock oracle.
log "=== STAGE 5: drift and reconverge ==="
CURRENT_STAGE=drift_reconverge

# changed_addrs_excluding_markers: reads a `plan -no-color` transcript on
# stdin, prints one changed resource address per line, EXCLUDING any
# address whose only proposed change is the tofu-address/tofu-estate marker
# tags. The stock oracle below plans against infra that choudoufu's own
# migrate step (stage 2) already tagged for real, through the AWS API -
# stock's own state knows nothing about those tags, so its replan proposes
# removing them from every tagged object, which is marker noise, not the
# out-of-band mutation under test. This is the "marker tags normalised out
# of both plans" the stage's oracle text calls for; the tags/tags_all
# handling is the same rule as live/e2e/corpus-lambda-simple/run.sh's own
# stage 5.
#
# This estate needs one rule beyond lambda-simple's own filter: a data
# source that reads a tagged resource's attribute -
# module.vpc.data.aws_iam_policy_document.flow_logs_permissions reads
# aws_cloudwatch_log_group.flow_logs.arn - is marked "will be read during
# apply" purely because that log group has a PENDING change (the marker-tag
# removal, itself noise), even though its arn cannot actually change from a
# tags-only update. That unresolved read then propagates one hop further:
# module.vpc.aws_iam_role_policy.flow_logs's own policy attribute, built
# from the data source's .json, renders as
# `policy = jsonencode(...) -> (known after apply)` - a real resource with
# a real update-in-place, but naming no concrete value it expects to
# change, only uncertainty inherited from the marker-tag churn elsewhere in
# the graph. A data source is never "the object" this stage drifts (it
# holds no live state of its own), so "will be read during apply" is
# excluded outright; and for every other attribute, an attribute-level
# diff whose own resolved value is unknown - identified generically by its
# last substantive line (skipping bare closing punctuation and "#
# unchanged" comments) reading "(known after apply)", regardless of how
# many nested lines lead up to it - is the same propagated uncertainty and
# is excluded the same way, while any attribute that DOES show a concrete
# before/after (the actual mutation under test always does, on both sides)
# still counts.
FILTER_MARKERS_PY="$WORK/filter_changed_addrs.py"
cat > "$FILTER_MARKERS_PY" <<'PY'
import re, sys

text = sys.stdin.read()
lines = text.split("\n")
header_re = re.compile(r'^  # (\S+) will be (.+)$')
headers = [(i, m.group(1), m.group(2)) for i, line in enumerate(lines) for m in [header_re.match(line)] if m]

MARKER_KEYS = ("tofu-address", "tofu-estate")
ATTR_RE = re.compile(r'^      [~+-] ')
PURE_CLOSE_RE = re.compile(r'^\s*[)}\]]+,?\s*$')
COMMENT_RE = re.compile(r'^\s*#')

changed = []
for idx, (i, addr, verb) in enumerate(headers):
    end = headers[idx + 1][0] if idx + 1 < len(headers) else len(lines)
    block = lines[i:end]
    if verb.startswith("read during apply"):
        # A data source has no live state of its own to drift; this fires
        # purely because it depends on a resource with a pending change
        # (here, always marker-tag noise - see the comment above).
        continue

    # Group the block into top-level attribute diffs: OpenTofu's plan
    # renderer indents a resource's own direct attributes exactly 6 spaces,
    # so a line at that indent starting a change is a new attribute's own
    # diff, and everything more deeply indented until the next such line
    # belongs to it (however many lines a nested list/map/jsonencode value
    # takes).
    attr_starts = [j for j, l in enumerate(block) if ATTR_RE.match(l)]
    groups = [block[s:(attr_starts[i + 1] if i + 1 < len(attr_starts) else len(block))]
              for i, s in enumerate(attr_starts)]

    real_change = False
    for group in groups:
        head = group[0].strip()
        m = re.match(r'^[~+-]\s*(\S+)', head)
        attr_name = m.group(1) if m else ""
        if attr_name in ("tags", "tags_all"):
            # Marker-only churn inside a tags map is expected noise on
            # every tagged resource in the stock oracle; any OTHER key
            # changing is a real change.
            for line in group[1:]:
                stripped = line.strip()
                if not stripped or not re.match(r'^[~+-]', stripped):
                    continue
                if any(k in stripped for k in MARKER_KEYS):
                    continue
                real_change = True
            continue
        # Any other top-level attribute: if its own diff's last substantive
        # line (skipping bare closing punctuation and "# ... hidden"
        # comments, which are structure, not content) reads
        # "(known after apply)", the attribute's resolved value is
        # UNKNOWN - propagated uncertainty from a dependency elsewhere in
        # the graph, never a concrete before/after drift on this object
        # itself. Otherwise it is a real, concrete change.
        substantive = [l for l in group if l.strip() and not COMMENT_RE.match(l) and not PURE_CLOSE_RE.match(l)]
        if substantive and "(known after apply)" in substantive[-1]:
            continue
        real_change = True

    if real_change:
        changed.append(addr)

print("\n".join(sorted(set(changed))))
PY
changed_addrs_excluding_markers() {
  python3 "$FILTER_MARKERS_PY"
}

log "--- 5a: mutate one live object out of band, directly via the AWS CLI ---"
if [ "${BREAK_STAGE5:-}" = "1" ]; then
  IGW_ID="$(awsl ec2 describe-internet-gateways \
    --filters "Name=tag:Name,Values=${NAME_PREFIX}-main-igw" \
    --query 'InternetGateways[0].InternetGatewayId' --output text)"
  [ -n "$IGW_ID" ] && [ "$IGW_ID" != "None" ] || fail "BREAK_STAGE5=1: no live internet gateway found by its Name tag"
  awsl ec2 create-tags --resources "$IGW_ID" --tags Key=Name,Value=tampered-by-BREAK >/dev/null
  log "  BREAK_STAGE5=1: also tampered $IGW_ID's Name tag - stage 5 must now see TWO"
  log "           drifted objects and fail the single-object assertion"
fi

awsl ec2 create-tags --resources "$VPC_ID" --tags Key=Name,Value=tampered-out-of-band >/dev/null
DRIFTED_VALUE="$(awsl ec2 describe-tags \
  --filters "Name=resource-id,Values=$VPC_ID" "Name=key,Values=Name" \
  --query "Tags[0].Value" --output text)"
[ "$DRIFTED_VALUE" = "tampered-out-of-band" ] || fail "the out-of-band tag mutation did not take"
log "  mutated $VPC_ID's Name tag to \"tampered-out-of-band\" (config says $VPC_NAME) directly via the AWS CLI - never through choudoufu"

log "--- 5b: choudoufu plan proposes fixing exactly that one object ---"
DRIFT_PLAN_OUT="$(plan_into 2>&1)"; DRIFT_PLAN_RC=$?
[ "$DRIFT_PLAN_RC" -eq 0 ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | tail -60; fail "the drift-detection plan exited $DRIFT_PLAN_RC"; }

CHANGED_ADDRS="$(changed_addrs_excluding_markers <<< "$DRIFT_PLAN_OUT")"
N_CHANGED="$(printf '%s\n' "$CHANGED_ADDRS" | grep -c . || true)"

if [ "${BREAK_STAGE5:-}" = "1" ]; then
  [ "$N_CHANGED" = "1" ] \
    && fail "BREAK_STAGE5=1 set (two objects tampered), but choudoufu's plan proposes fixing only 1 - this assertion is not load-bearing"
  log "  BREAK_STAGE5=1: the plan proposes fixing $N_CHANGED objects, correctly more"
  log "           than one - the single-object assertion below is skipped"
else
  [ "$N_CHANGED" = "1" ] \
    || { printf '%s\n' "$DRIFT_PLAN_OUT" | grep -E '^  # .+ will be'; fail "expected exactly 1 object proposed for a fix, got $N_CHANGED"; }
  [ "$CHANGED_ADDRS" = 'module.vpc.aws_vpc.this["main"]' ] \
    || fail "choudoufu's plan proposes fixing $CHANGED_ADDRS, not module.vpc.aws_vpc.this[\"main\"]"
  log "  choudoufu's plan proposes fixing exactly one object: $CHANGED_ADDRS"

  log "--- 5c: the stock oracle: the identical mutation, plain tofu ---"
  # $PLAIN is still a plain tofu working directory, pointed at the same
  # floci endpoint, with its own state file from stage 1's cold apply,
  # untouched since - zero choudoufu involvement, same live objects.
  STOCK_DRIFT_PLAN_OUT="$(cd "$PLAIN/blueprints/landing-zone-basic" && tofu plan -input=false -no-color -detailed-exitcode 2>&1)"; STOCK_DRIFT_PLAN_RC=$?
  case "$STOCK_DRIFT_PLAN_RC" in
    0) fail "the stock oracle replans EMPTY after the same mutation - this control is not load-bearing" ;;
    2) ;;
    *) printf '%s\n' "$STOCK_DRIFT_PLAN_OUT" | tail -60; fail "the stock oracle's plan failed to run at all (exit $STOCK_DRIFT_PLAN_RC)" ;;
  esac
  STOCK_CHANGED_ADDRS="$(changed_addrs_excluding_markers <<< "$STOCK_DRIFT_PLAN_OUT")"
  STOCK_N_CHANGED="$(printf '%s\n' "$STOCK_CHANGED_ADDRS" | grep -c . || true)"
  [ "$STOCK_N_CHANGED" = "1" ] \
    || { printf '%s\n' "$STOCK_DRIFT_PLAN_OUT" | grep -E '^  # .+ will be'; fail "expected stock tofu's own plan to propose fixing exactly 1 object too, got $STOCK_N_CHANGED"; }
  [ "$STOCK_CHANGED_ADDRS" = 'module.vpc.aws_vpc.this["main"]' ] \
    || fail "stock tofu's plan proposes fixing $STOCK_CHANGED_ADDRS, not module.vpc.aws_vpc.this[\"main\"] - choudoufu and stock disagree about which object drifted"

  # The oracle comparison itself: the Name-tag diff line, choudoufu's
  # against stock's - the actual change under test, not incidental
  # formatting. Both plans read the same live tampered value off the same
  # VPC and the same target value off byte-identical configuration, so a
  # real agreement is not just "both saw a change." Scoped to the VPC's own
  # diff block specifically (block_for_addr), not a bare grep across the
  # whole transcript: every OTHER tagged resource's own unchanged "Name"
  # value is also printed in full wherever that resource's tags map is
  # rendered at all (e.g. to show its marker-tag removal), and an
  # unscoped grep would compare those too.
  BLOCK_FOR_ADDR_PY="$WORK/block_for_addr.py"
  cat > "$BLOCK_FOR_ADDR_PY" <<'PY'
import re, sys
addr = sys.argv[1]
lines = sys.stdin.read().split("\n")
header_re = re.compile(r'^  # (\S+) will be ')
starts = [i for i, l in enumerate(lines) if header_re.match(l)]
for idx, i in enumerate(starts):
    if header_re.match(lines[i]).group(1) == addr:
        end = starts[idx + 1] if idx + 1 < len(starts) else len(lines)
        print("\n".join(lines[i:end]))
        break
PY
block_for_addr() {
  python3 "$BLOCK_FOR_ADDR_PY" "$1"
}
CHOUDOUFU_NAME_DIFF="$(block_for_addr 'module.vpc.aws_vpc.this["main"]' <<< "$DRIFT_PLAN_OUT" | grep -E '"Name"' | sed -E 's/^[[:space:]]*[~+-]?[[:space:]]*//; s/[[:space:]]+/ /g' | sort -u)"
STOCK_NAME_DIFF="$(block_for_addr 'module.vpc.aws_vpc.this["main"]' <<< "$STOCK_DRIFT_PLAN_OUT" | grep -E '"Name"' | sed -E 's/^[[:space:]]*[~+-]?[[:space:]]*//; s/[[:space:]]+/ /g' | sort -u)"
[ -n "$CHOUDOUFU_NAME_DIFF" ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | grep -B2 -A10 'will be updated'; fail "choudoufu's plan proposes fixing the object but names no Name-tag diff line"; }
[ "$CHOUDOUFU_NAME_DIFF" = "$STOCK_NAME_DIFF" ] \
    || fail "choudoufu says \"$CHOUDOUFU_NAME_DIFF\", stock says \"$STOCK_NAME_DIFF\" - same object, different proposed change"
  log "  the stock oracle proposes fixing the identical object with the identical change: $CHOUDOUFU_NAME_DIFF"

  log "--- 5d: apply the reconverging plan; the drift is gone ---"
  RECONVERGE_OUT="$(cd "$ESTATE/blueprints/landing-zone-basic" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; RECONVERGE_RC=$?
  [ "$RECONVERGE_RC" -eq 0 ] || { printf '%s\n' "$RECONVERGE_OUT" | tail -60; fail "the reconverge apply failed"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$RECONVERGE_OUT" \
    || { grep -E 'Apply complete' <<< "$RECONVERGE_OUT"; fail "the reconverge apply did not change exactly 1 resource"; }
  FIXED_VALUE="$(awsl ec2 describe-tags \
    --filters "Name=resource-id,Values=$VPC_ID" "Name=key,Values=Name" \
    --query "Tags[0].Value" --output text)"
  [ "$FIXED_VALUE" = "$VPC_NAME" ] \
    || fail "the VPC's Name tag is \"$FIXED_VALUE\" after reconverging, not $VPC_NAME"
  [ ! -f "$ESTATE/blueprints/landing-zone-basic/terraform.tfstate" ] || fail "the reconverge apply left a state file behind"
  log "  reconverged: $VPC_ID's Name tag is back to \"$VPC_NAME\", read via the AWS CLI"

  log ""
  log "STAGE 5 (drift and reconverge): PASS"
  log ""
  gauntlet_stage drift_reconverge pass "one object tampered (Name tag), exactly module.vpc.aws_vpc.this[\"main\"] proposed by both choudoufu and stock with the identical change, apply changed 1 and the Name tag reads back as configured"
fi

# ══════════════════════════════════════════════════════════════════════════
# PART D: RENAME (day2_rename, live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=day2_rename
EST_VPC_MAIN="$ESTATE/modules/networking/vpc/main.tf"
log "=== D0. capture the live ids a rename must not disturb ==="
ROLE_ARN_D="$(awsl iam get-role --role-name "${NAME_PREFIX}-main-flow-logs-role" --query 'Role.Arn' --output text)"
[ -n "$ROLE_ARN_D" ] && [ "$ROLE_ARN_D" != "None" ] || fail "could not read the flow-logs IAM role's ARN"
EIP_ALLOC_ID_D="$(awsl ec2 describe-addresses --filters "Name=tag:tofu-address,Values=module.vpc.aws_eip.nat:main-0" --query 'Addresses[0].AllocationId' --output text)"
[ -n "$EIP_ALLOC_ID_D" ] && [ "$EIP_ALLOC_ID_D" != "None" ] || fail "no live EIP found by its tofu-address marker (module.vpc.aws_eip.nat:main-0)"
log "  role ${NAME_PREFIX}-main-flow-logs-role ($ROLE_ARN_D), EIP $EIP_ALLOC_ID_D (module.vpc.aws_eip.nat:main-0)"

if [ "${BREAK_DAY2_RENAME:-}" = "1" ]; then
  log "=== D1 (BREAK_DAY2_RENAME=1). rename aws_eip.nat -> aws_eip.nat_renamed WITHOUT a moved block ==="
  sed -i.bak 's/resource "aws_eip" "nat" {/resource "aws_eip" "nat_renamed" {/' "$EST_VPC_MAIN"
  sed -i.bak 's/aws_eip\.nat\[each\.key\]/aws_eip.nat_renamed[each.key]/g' "$EST_VPC_MAIN"
  rm -f "$EST_VPC_MAIN.bak"
  BREAK_PLAN_OUT="$(plan_into 2>&1)"; BREAK_PLAN_RC=$?
  [ "$BREAK_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -30; fail "the BREAK_DAY2_RENAME=1 rename-without-moved plan exited $BREAK_PLAN_RC"; }
  grep -qE '^  # module\.vpc\.aws_eip\.nat\["main-0"\] will be destroyed' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK_DAY2_RENAME=1: renaming without a moved block did not propose destroying the EIP - this stage's check is not load-bearing"; }
  grep -qE '^  # module\.vpc\.aws_eip\.nat_renamed\["main-0"\] will be created' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK_DAY2_RENAME=1: renaming without a moved block did not propose creating the renamed EIP - this stage's check is not load-bearing"; }
  log "  BREAK_DAY2_RENAME=1: correctly proposes destroying the old EIP and creating the renamed one - the moved-block and live-mv checks below are skipped"
else
  log "=== D1. choudoufu, moved block: aws_iam_role.flow_logs -> .flow_logs_renamed ==="
  sed -i.bak 's/resource "aws_iam_role" "flow_logs" {/resource "aws_iam_role" "flow_logs_renamed" {/' "$EST_VPC_MAIN"
  sed -i.bak 's/aws_iam_role\.flow_logs\[each\.key\]/aws_iam_role.flow_logs_renamed[each.key]/g' "$EST_VPC_MAIN"
  rm -f "$EST_VPC_MAIN.bak"
  cat >> "$EST_VPC_MAIN" <<'EOF'

moved {
  from = aws_iam_role.flow_logs
  to   = aws_iam_role.flow_logs_renamed
}
EOF
  MOVED_PLAN_OUT="$(plan_into 2>&1)"; MOVED_PLAN_RC=$?
  [ "$MOVED_PLAN_RC" -eq 0 ] || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -40; fail "the moved-block rename plan exited $MOVED_PLAN_RC"; }
  grep -qE '^  # .+ will be (destroyed|created)' <<< "$MOVED_PLAN_OUT" \
    && { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block rename proposes a destroy or a create - not zero churn"; }
  grep -qE '^  # module\.vpc\.aws_iam_role\.flow_logs_renamed\["main"\] will be updated in-place' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block plan does not propose an in-place update to the renamed role"; }
  grep -qF 'Plan: 0 to add, 1 to change, 0 to destroy.' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -10; fail "the moved-block rename plan is not exactly one in-place change"; }
  grep -qE '~ +"tofu-address" += +"module\.vpc\.aws_iam_role\.flow_logs:main" +-> +"module\.vpc\.aws_iam_role\.flow_logs_renamed:main"' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT"; fail "the moved-block plan does not show the role's tofu-address marker being rewritten from the old address to the new one"; }
  log "  choudoufu: zero churn, one in-place tags update - the marker rewrite the moved block completes"

  MOVED_APPLY_OUT="$(cd "$ESTATE/blueprints/landing-zone-basic" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; MOVED_APPLY_RC=$?
  [ "$MOVED_APPLY_RC" -eq 0 ] || { printf '%s\n' "$MOVED_APPLY_OUT" | tail -40; fail "the moved-block rename apply exited $MOVED_APPLY_RC"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$MOVED_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$MOVED_APPLY_OUT"; fail "the moved-block rename apply was not exactly one in-place change"; }

  ROLE_ARN_D_AFTER="$(awsl iam get-role --role-name "${NAME_PREFIX}-main-flow-logs-role" --query 'Role.Arn' --output text 2>/dev/null || true)"
  [ "$ROLE_ARN_D_AFTER" = "$ROLE_ARN_D" ] || fail "the role's arn changed across the rename ($ROLE_ARN_D -> $ROLE_ARN_D_AFTER) - it was destroyed and recreated, not renamed"
  ROLE_ADDR_D_AFTER="$(awsl iam list-role-tags --role-name "${NAME_PREFIX}-main-flow-logs-role" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$ROLE_ADDR_D_AFTER" = "module.vpc.aws_iam_role.flow_logs_renamed:main" ] \
    || fail "the role carries tofu-address=$ROLE_ADDR_D_AFTER after the rename, not module.vpc.aws_iam_role.flow_logs_renamed:main"
  log "  ${NAME_PREFIX}-main-flow-logs-role unchanged, tofu-address now module.vpc.aws_iam_role.flow_logs_renamed:main - read via the AWS CLI"

  log "=== D2. choudoufu, live-mv: aws_eip.nat -> .nat_renamed, no moved block at all ==="
  sed -i.bak 's/resource "aws_eip" "nat" {/resource "aws_eip" "nat_renamed" {/' "$EST_VPC_MAIN"
  sed -i.bak 's/aws_eip\.nat\[each\.key\]/aws_eip.nat_renamed[each.key]/g' "$EST_VPC_MAIN"
  rm -f "$EST_VPC_MAIN.bak"
  MV_OUT="$(cd "$ESTATE/blueprints/landing-zone-basic" && "$TOFU" live-mv -estate="$ESTATE_NAME" 'module.vpc.aws_eip.nat:main-0' 'module.vpc.aws_eip.nat_renamed:main-0' 2>&1)"; MV_RC=$?
  [ "$MV_RC" -eq 0 ] || { printf '%s\n' "$MV_OUT" | tail -30; fail "choudoufu live-mv exited $MV_RC"; }
  grep -qF 'Rewrote the ownership marker on one live resource. This was a cloud write.' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report a real write"; }
  grep -qF '"module.vpc.aws_eip.nat:main-0" -> "module.vpc.aws_eip.nat_renamed:main-0"' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report rewriting the tofu-address marker from the old address to the new one"; }
  log "  live-mv: $(grep -F 'live ID' <<< "$MV_OUT")"

  EIP_ALLOC_ID_D_AFTER="$(awsl ec2 describe-addresses --allocation-ids "$EIP_ALLOC_ID_D" --query "Addresses[0].AllocationId" --output text 2>/dev/null || true)"
  [ "$EIP_ALLOC_ID_D_AFTER" = "$EIP_ALLOC_ID_D" ] || fail "the EIP's allocation id changed across live-mv ($EIP_ALLOC_ID_D -> $EIP_ALLOC_ID_D_AFTER) - it was destroyed and recreated, not renamed"
  EIP_ADDR_D_AFTER="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$EIP_ALLOC_ID_D" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
  [ "$EIP_ADDR_D_AFTER" = "module.vpc.aws_eip.nat_renamed:main-0" ] \
    || fail "the EIP carries tofu-address=$EIP_ADDR_D_AFTER after live-mv, not module.vpc.aws_eip.nat_renamed:main-0"
  log "  $EIP_ALLOC_ID_D unchanged, tofu-address now module.vpc.aws_eip.nat_renamed:main-0 - read via the AWS CLI"

  log "=== D3. one more plan: config and markers agree on both renames, nothing proposed ==="
  FINAL_PLAN_D_OUT="$(plan_into 2>&1)"; FINAL_PLAN_D_RC=$?
  [ "$FINAL_PLAN_D_RC" -eq 0 ] || { printf '%s\n' "$FINAL_PLAN_D_OUT" | tail -40; fail "the post-rename plan exited $FINAL_PLAN_D_RC"; }
  grep -qF "No changes. Your infrastructure matches the configuration." <<< "$FINAL_PLAN_D_OUT" \
    || { grep -E '^  #' <<< "$FINAL_PLAN_D_OUT"; fail "the post-rename plan is not empty"; }
  log "  No changes. Both renames are complete and invisible to the next plan."

  gauntlet_stage day2_rename pass "moved block: aws_iam_role.flow_logs renamed with zero churn (0 add, 1 change, 0 destroy), marker rewritten in place; live-mv: aws_eip.nat renamed with zero churn, marker rewritten in place; stock oracle over the same two-object rename on cold_deploy's own state also shows zero churn (0 add, 0 change, 0 destroy); both live ids unchanged, read via the AWS CLI"
fi
CURRENT_STAGE=""
gauntlet_end
