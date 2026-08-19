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
# Three MORE real, independent gaps surfaced past this point, at stage 3 -
# see that stage's own header comment below for #327, #328 and
# lex00/floci#78, none of them account-governance shaped and none
# scoped around (this script asserts stage 3's exact blocked shape
# instead).
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
#   3. TEST PLAN     delete the state file, `choudoufu live-plan` - BLOCKED:
#                     asserted non-empty for exactly three documented,
#                     independent, already-filed reasons (#327, #328,
#                     lex00/floci#78), deterministically rather than
#                     skipped; the VPC's own identity, untouched by any of
#                     the three, is still re-asserted against the AWS CLI.
#   4/5.             NOT ATTEMPTED - both need a genuinely empty first plan
#                     as their starting point, which stage 3 does not reach
#                     against this floci image/main.
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
#                 proving it is load-bearing (stages 4-5 are not attempted -
#                 see STAGES above - so there is no second, drift-based use
#                 of this flag here the way other crossings have).
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
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
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

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - genuinely BLOCKED, asserted deterministically rather
# than skipped. Three real, independent, filed gaps - none of them a
# choudoufu-vs-floci ambiguity, each traced to its own root cause before
# filing (see header):
#   - #327: aws_nat_gateway's ForceNew args (allocation_id, subnet_id) come
#     back null from this fork's stateless prior, forcing a replace of an
#     object that is not actually different - confirmed floci's own
#     DescribeNatGateways XML shape is correct, so this is choudoufu's own
#     prior-construction gap, not floci's.
#   - #328: aws_default_security_group's revoke_rules_on_delete is a
#     client-only argument with no live AWS representation at all, so it
#     always diffs against whatever import found (null/false).
#   - lex00/floci#78: aws_flow_log's CreateFlowLogs ignores
#     TagSpecifications entirely - the marker write reports success but
#     never persists, so the freshly-discovered live flow log carries no
#     ownership marker and a stateless replan proposes creating a second
#     one alongside it.
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 3: no state file, live-plan (expected non-empty - see header) ==="
rm -f "$ESTATE/blueprints/landing-zone-basic/terraform.tfstate" "$ESTATE/blueprints/landing-zone-basic/terraform.tfstate.backup"
[ ! -f "$ESTATE/blueprints/landing-zone-basic/terraform.tfstate" ] || fail "the state file is still there"

plan_into() { ( cd "$ESTATE/blueprints/landing-zone-basic" && "$TOFU" live-plan -input=false -no-color ); }
PLAN_OUT="$(plan_into 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -80; fail "live-plan exited $PLAN_RC (expected 0 - a non-empty plan is not the same as a plan error)"; }
[ ! -f "$ESTATE/blueprints/landing-zone-basic/terraform.tfstate" ] || fail "live-plan wrote a state file"

grep -qF "Plan: 2 to add, 3 to change, 1 to destroy." <<< "$PLAN_OUT" \
  || { grep -E '^  #' <<< "$PLAN_OUT"; fail "expected exactly 'Plan: 2 to add, 3 to change, 1 to destroy.' - if this moved, one of the three documented causes above may have changed shape"; }
for addr in \
  'module.vpc.aws_default_security_group.this\["main"\] will be updated in-place' \
  'module.vpc.aws_flow_log.cloudwatch\["main"\] will be created' \
  'module.vpc.aws_nat_gateway.this\["main-0"\] must be replaced' \
  'module.vpc.aws_route_table.private\["main-0"\] will be updated in-place' \
  'module.vpc.aws_route_table.private\["main-1"\] will be updated in-place'
do
  grep -qE "$addr" <<< "$PLAN_OUT" || fail "expected '$addr' among the 5 proposed changes"
done
log "  non-empty plan, all 5 proposed changes traced: #327 (NAT gateway forced replace), #328"
log "  (default_security_group's revoke_rules_on_delete), lex00/floci#78 (flow log tag-write silently no-ops)"

# Re-assert the VPC's identity directly against the AWS CLI, after the
# local state file was deleted - the answer below can only have come from
# the marker on the live object itself. The VPC itself is untouched by any
# of the three gaps above (it is not among the 5 changed addresses), so its
# own marker is exactly what a clean stage 3 would have shown.
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
log "STAGE 3 (test plan): BLOCKED (deterministic) - 5 proposed changes, all traced to #327/#328/lex00-floci#78, none a choudoufu-vs-floci ambiguity"
log ""

log "=== STAGES 4-5: NOT ATTEMPTED - both need a genuinely empty first plan as their ==="
log "=== starting point, which stage 3 does not reach against this floci image/main  ==="
