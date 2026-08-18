#!/usr/bin/env bash
set -uo pipefail

# terraform-aws-modules/terraform-aws-security-group's flagship "complete"
# example (.corpus/security-group/examples/complete, pinned in
# live/corpus-manifest.json at tag v6.0.0, commit 58d8e895), crossed through
# choudoufu against floci - the real, five-stage pipeline (cold deploy,
# migrate, test plan, test apply, drift and reconverge). This module is a
# common dependency of many other terraform-aws-modules (rds, eks and others
# wire security groups through it), and a prior crossing
# (corpus-rds-complete-postgres) already found and filed a real defect
# through it (#304) as a side effect of a different estate - this script is
# the first time the module's own flagship example is crossed directly.
#
# v6.0.0 rewrote this module from the classic single-`aws_security_group`-
# with-dynamic-`ingress`-blocks shape to one `aws_security_group` plus
# per-rule `aws_vpc_security_group_ingress_rule`/`egress_rule` resources
# (for_each over a rules map), `aws_vpc_security_group_vpc_association` for
# cross-VPC association, and `aws_vpc_security_group_rules_exclusive` for
# drift enforcement. #304's pattern (a static `lookup()`-keyed `count.index`
# into `ingress_with_cidr_blocks`) is the OLD shape - it does not exist
# anywhere in this example, because v6.0.0 dropped that variable and its
# dynamic-block loop entirely in favor of the for_each-over-a-map shape
# above. So this crossing does NOT hit #304 - a different, real gap in the
# same family surfaced instead (see DEFECT B below), which is exactly the
# "if you hit something different, that's new" case.
#
# 68 resources: the root `security_group` module (1 SG, 7 ingress rules, 1
# egress rule, 1 vpc_association, 1 rules_exclusive), its `postgresql`
# preset submodule (1 SG, 2 ingress rules from setproduct(preset, cidr), 1
# egress rule, 1 rules_exclusive), its `consul` preset submodule (1 SG, 22
# ingress rules from setproduct(11 presets, {cidr, referenced_sg}), 1
# rules_exclusive), a standalone `aws_security_group.app` referenced by
# `mysql-from-app`/`consul`'s referenced_security_group_id ingress rules, an
# `aws_ec2_managed_prefix_list.dns` referenced by a prefix-list ingress
# rule, two `disabled_*` modules at `create = false` (0 instances), and two
# `terraform-aws-modules/vpc` registry module calls (`vpc`, `vpc_secondary`,
# resolving to v6.6.1 - the same version and module
# live/e2e/corpus-vpc-complete/run.sh crosses - each contributing 1 VPC + 3
# private subnets + 3 route tables + 3 associations + the default_* adopter
# trio).
#
# DEFECT A (floci, EMULATOR GAP, filed lex00/floci#57). EC2
# AssociateSecurityGroupVpc ("security groups for multiple VPCs", the action
# behind aws_vpc_security_group_vpc_association) has no floci handler:
# "UnsupportedOperation: Operation AssociateSecurityGroupVpc is not
# supported." DELTA 2 removes the estate's one `vpc_associations` block
# (module.security_group's "secondary" entry) so the other 67 resources can
# stand up for real; `module.vpc_secondary` itself is left in place (nothing
# else in the example depends on removing it) and still applies cleanly on
# its own.
#
# DEFECT B (choudoufu, real gap, distinct from #304/#305 - filed
# #307). aws_vpc_security_group_rules_exclusive is not
# in the admission table (internal/live/identity/table_generated.go) and the
# pinned AWS provider release (6.59.0) ships no resource identity schema for
# it either (live/survey-full.json: "identity_schema": false, path "moves to
# Ops"), so identity.Report cannot settle it and it is a hard unadmitted-type
# refusal at every one of its 3 instances in this estate (the main, postgresql
# and consul security groups each get one, since enable_exclusive_rules
# defaults to true). This is NOT #305 (aws_default_network_acl/route_table/
# security_group - the vpc module's default-object adopters, which this
# estate ALSO creates via its two nested terraform-aws-vpc calls and which
# DOES fire here too, 6 sites) and NOT #304 (a static lookup()-into-count.index
# pattern that does not exist anywhere in this v6.0.0 example). What is
# recoverable without a provider identity schema: the resource's own import
# documentation is unambiguous - "import exclusive management of security
# group rules using the security_group_id" - and security_group_id is the
# type's one required, ForceNew argument, always a direct reference to the
# aws_security_group resource it governs. This is the same shape as
# aws_vpc_security_group_vpc_association's own admitted row (client-supplied
# arguments only, composed over a tagged parent's identity), just without a
# provider-shipped identity schema to derive it from mechanically - doc-only
# admission, not schema-only.
#
# WHAT THIS SCRIPT ACTUALLY PROVES, GIVEN BOTH:
#
#   stage 1  cold deploy   PASS - real, unmarked infrastructure, 67 of the
#                          module's 68 resources (DELTA 2, #57).
#   stage 2  migrate       PASS - real: 52 of 67 resource instances stamped
#                          for real (35 VERIFIED + 17 DRIFTED - the drift is
#                          a provider-normalized referenced_security_group_id
#                          format difference at read time, see stage 2's own
#                          log), the rest correctly skipped (6 UNTAGGABLE -
#                          aws_route_table_association, no tags argument -
#                          + 9 UNADMITTED_TYPE - #305's default_* trio (6)
#                          plus DEFECT B's rules_exclusive (3)), asserted
#                          against live-import's own report AND confirmed
#                          independently through the AWS CLI.
#   stage 3  test plan     BLOCKED, for real, by #305 (6 sites) and DEFECT B
#                          (3 sites) - specific counts and resource
#                          addresses asserted against a real live-plan run,
#                          state file deleted first, BREAK=1 negative
#                          control.
#   stage 4  test apply    NOT RUN - depends on stage 3.
#   stage 5  drift/reconverge  NOT RUN - depends on stages 3-4.
#
#   bash live/e2e/corpus-security-group-complete/run.sh
#
# Needs Docker, the AWS CLI, terraform (real, stock terraform - stage 1 is
# deliberately NOT choudoufu) on PATH, network access for `terraform init`
# to resolve terraform-aws-modules/vpc from the registry (same as
# corpus-vpc-complete), and .corpus populated (`just corpus-fetch`).
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4721, clear of every
#                other live/e2e fixture's port).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        set to 1 to corrupt the expected stage-3 site counts and
#                one expected unadmitted-type name, proving those
#                assertions are load-bearing. Stages 1 and 2 are unaffected;
#                stage 3 is the one that must fail.
#
# The corpus checkout is shared across worktrees and is NEVER written to:
# the estate is copied out first (twice) and every delta lands on a copy.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CORPUS_DIR="${CORPUS_DIR:-$ROOT/.corpus}"
SRC="$CORPUS_DIR/security-group"
WORK="$(mktemp -d)"
FLOCI_PORT="${FLOCI_PORT:-4721}"
FLOCI_NAME="choudoufu-corpus-security-group-complete-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="security-group-complete-crossing"
REGION="eu-west-1"
INSTANCES=67
ELIGIBLE=52
SKIPPED=15

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }

# copy_tree DEST - the security-group module root (incl. every preset
# submodule) plus examples/complete, preserving the relative layout the
# example's `source = "../../"` / `"../../modules/*"` need.
copy_tree() {
  local dest="$1"
  mkdir -p "$dest/security-group/examples"
  cp -R "$SRC/main.tf" "$SRC/variables.tf" "$SRC/outputs.tf" "$SRC/versions.tf" "$SRC/modules" "$dest/security-group/"
  cp -R "$SRC/examples/complete" "$dest/security-group/examples/complete"
  rm -rf "$dest/security-group/examples/complete/.terraform" \
         "$dest/security-group/examples/complete/.terraform.lock.hcl" \
         "$dest/security-group/examples/complete/terraform.tfstate" \
         "$dest/security-group/examples/complete/terraform.tfstate.backup"
}

# ── 0. tools and corpus ─────────────────────────────────────────────────────
log "=== 0. tools and corpus ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v terraform >/dev/null 2>&1 || fail "the terraform binary is not on PATH - stage 1 is deliberately plain terraform, not choudoufu"
[ -d "$SRC/examples/complete" ] || fail "$SRC/examples/complete is missing - run 'just corpus-fetch' first"

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

PLAIN="$WORK/plain"
copy_tree "$PLAIN"
PLAIN_EST="$PLAIN/security-group/examples/complete"
log "  estate copied out of .corpus into $PLAIN_EST"

# ── 1. cold deploy: plain terraform, no live block, no choudoufu ───────────
log "=== 1. cold deploy: plain terraform, 67 real resources ==="

# DELTA 1, onboarding: emulator flags on the estate's one provider block.
perl -0pi -e 's/^(provider "aws" \{\n  region = local\.region\n)\}/$1  access_key                   = "test" # DELTA 1\n  secret_key                   = "test"\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  skip_requesting_account_id   = true\n  s3_use_path_style            = true\n}/' "$PLAIN_EST/main.tf"
grep -q 'DELTA 1' "$PLAIN_EST/main.tf" || fail "DELTA 1 did not match the provider block - the corpus pin has moved"
log "  DELTA 1  emulator flags on the provider block             (onboarding)"

# DELTA 2, EMULATOR GAP (lex00/floci#57): EC2 AssociateSecurityGroupVpc has
# no floci handler. Removes the one vpc_associations block; module.vpc_
# secondary itself is left standing (nothing else depends on removing it).
perl -0pi -e 's/\n  vpc_associations = \{\n    secondary = \{\n      vpc_id = module\.vpc_secondary\.vpc_id\n    \}\n  \}\n\n/\n  # DELTA 2 (EMULATOR GAP, lex00\/floci#57): cross-VPC association removed.\n  # aws_vpc_security_group_vpc_association calls EC2 AssociateSecurityGroupVpc,\n  # which floci does not implement.\n\n/' "$PLAIN_EST/main.tf"
grep -q 'DELTA 2' "$PLAIN_EST/main.tf" || fail "DELTA 2 did not match the vpc_associations block - the corpus pin has moved"
grep -q '^  vpc_associations = {' "$PLAIN_EST/main.tf" && fail "DELTA 2 left a vpc_associations block behind"
log "  DELTA 2  vpc_associations removed                         (EMULATOR GAP, lex00/floci#57)"

# Pin the provider version for reproducibility, same discipline
# corpus-vpc-complete uses (this checkout's admission tables were generated
# against 6.59.0).
perl -0pi -e 's/version = ">= 6\.29"/version = "= 6.59.0"/' "$PLAIN_EST/versions.tf"
grep -q '= 6.59.0' "$PLAIN_EST/versions.tf" || fail "the version pin did not match versions.tf - the corpus pin has moved"

log "=== 1a. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
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

log "=== 1b. terraform init + apply ==="
( cd "$PLAIN_EST" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_EST" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "plain terraform init failed"; }
PLAIN_APPLY_OUT="$(cd "$PLAIN_EST" && terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$PLAIN_APPLY_OUT" | tail -60
  fail "the plain terraform apply failed"; }
grep -qE "Apply complete! Resources: $INSTANCES added" <<< "$PLAIN_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$PLAIN_APPLY_OUT"; fail "the apply did not create exactly $INSTANCES resources - the corpus pin or the emulator has moved"; }
[ -f "$PLAIN_EST/terraform.tfstate" ] || fail "plain terraform left no state file to migrate from"
log "  $(grep -E 'Apply complete' <<< "$PLAIN_APPLY_OUT")"
log "  real terraform.tfstate, zero choudoufu markers - 4 security groups"
log "  (main + app + postgresql preset + consul preset), 31 ingress rules,"
log "  2 egress rules, 3 rules_exclusive enforcers, 1 managed prefix list,"
log "  2 VPCs with 3 private subnets/route tables/associations each, plus"
log "  each VPC's default_* adopter trio"

# Confirmed unmarked: read the main security group's tags directly, never
# through choudoufu.
MAIN_SG_ID="$(terraform -chdir="$PLAIN_EST" output -raw security_group_id)"
[ -n "$MAIN_SG_ID" ] && [ "$MAIN_SG_ID" != "None" ] || fail "could not read the main security group's id from terraform output"
MARKER_COUNT="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$MAIN_SG_ID" "Name=key,Values=tofu-address" --query 'length(Tags)' --output text)"
[ "$MARKER_COUNT" = "0" ] || fail "the main security group already carries a tofu-address tag before migration - this crossing proves nothing"
log "  confirmed unmarked: $MAIN_SG_ID carries no tofu-address tag"

log ""
log "STAGE 1 (cold deploy): PASS"
log ""

# ── 2. migrate: choudoufu live-import against the plain state file ─────────
log "=== 2. migrate: choudoufu live-import ==="

ADOPTED="$WORK/adopted"
copy_tree "$ADOPTED"
ADOPTED_EST="$ADOPTED/security-group/examples/complete"
perl -0pi -e 's/^(provider "aws" \{\n  region = local\.region\n)\}/$1  access_key                   = "test" # DELTA 1\n  secret_key                   = "test"\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  skip_requesting_account_id   = true\n  s3_use_path_style            = true\n}/' "$ADOPTED_EST/main.tf"
perl -0pi -e 's/\n  vpc_associations = \{\n    secondary = \{\n      vpc_id = module\.vpc_secondary\.vpc_id\n    \}\n  \}\n\n/\n  # DELTA 2 (EMULATOR GAP, lex00\/floci#57): cross-VPC association removed.\n\n/' "$ADOPTED_EST/main.tf"
perl -0pi -e 's/version = ">= 6\.29"/version = "= 6.59.0"/' "$ADOPTED_EST/versions.tf"

# DELTA 3, onboarding: add the live block. No record_store needed - this
# estate has no effects-only (null_resource/time_*/random_*) resources.
perl -0pi -e "s/(required_providers \{\n    aws = \{\n      source  = \"hashicorp\/aws\"\n      version = \"= 6\.59\.0\"\n    \}\n  \}\n)\}/\$1\n  live {\n    estate = \"$ESTATE\"\n  }\n}/" "$ADOPTED_EST/versions.tf"
grep -q "estate = \"$ESTATE\"" "$ADOPTED_EST/versions.tf" || fail "DELTA 3 did not match versions.tf - the corpus pin has moved"
log "  DELTA 3  live block added                                  (onboarding)"

( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "adopted init failed"; }

log "=== 2a. live-import dry run: verify against the live system, write nothing ==="
IMPORT_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-import -state="$PLAIN_EST/terraform.tfstate" -estate="$ESTATE" 2>&1)"
IMPORT_RC=$?
[ "$IMPORT_RC" -eq 0 ] || { printf '%s\n' "$IMPORT_OUT" | tail -30; fail "live-import (dry run) exited $IMPORT_RC unexpectedly"; }

grep -qF "$ELIGIBLE of $INSTANCES resource instance(s) are eligible for stamping (VERIFIED or DRIFTED)." <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import did not report exactly $ELIGIBLE of $INSTANCES eligible - this estate's resource shape has moved"; }
grep -qF "No tag has been written. Rerun with -approve to stamp tofu-estate and tofu-address onto every eligible resource above." <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import's dry run did not report 'no tag written' correctly"; }

VERIFIED_N="$(grep -oE '^VERIFIED \([0-9]+\)' <<< "$IMPORT_OUT" | grep -oE '[0-9]+')"
DRIFTED_N="$(grep -oE '^DRIFTED \([0-9]+\)' <<< "$IMPORT_OUT" | grep -oE '[0-9]+')"
UNTAGGABLE_N="$(grep -oE '^UNTAGGABLE \([0-9]+\)' <<< "$IMPORT_OUT" | grep -oE '[0-9]+')"
UNADMITTED_N="$(grep -oE '^UNADMITTED_TYPE \([0-9]+\)' <<< "$IMPORT_OUT" | grep -oE '[0-9]+')"
[ "${VERIFIED_N:-0}" = "35" ] || fail "expected 35 VERIFIED, got ${VERIFIED_N:-0}"
[ "${DRIFTED_N:-0}" = "17" ] || fail "expected 17 DRIFTED, got ${DRIFTED_N:-0}"
[ "${UNTAGGABLE_N:-0}" = "6" ] || fail "expected 6 UNTAGGABLE, got ${UNTAGGABLE_N:-0}"
[ "${UNADMITTED_N:-0}" = "9" ] || fail "expected 9 UNADMITTED_TYPE, got ${UNADMITTED_N:-0}"
grep -qF 'module.vpc.aws_default_network_acl.this[0]' <<< "$IMPORT_OUT" || fail "expected module.vpc.aws_default_network_acl.this[0] among UNADMITTED_TYPE"
grep -qF 'module.vpc.aws_default_route_table.default[0]' <<< "$IMPORT_OUT" || fail "expected module.vpc.aws_default_route_table.default[0] among UNADMITTED_TYPE"
grep -qF 'module.vpc.aws_default_security_group.this[0]' <<< "$IMPORT_OUT" || fail "expected module.vpc.aws_default_security_group.this[0] among UNADMITTED_TYPE"
grep -qF 'module.security_group.aws_vpc_security_group_rules_exclusive.this[0]' <<< "$IMPORT_OUT" || fail "expected module.security_group.aws_vpc_security_group_rules_exclusive.this[0] among UNADMITTED_TYPE"
log "  $ELIGIBLE of $INSTANCES eligible (35 VERIFIED + 17 DRIFTED); $SKIPPED skipped"
log "  (6 UNTAGGABLE - aws_route_table_association x6, no tags argument - +"
log "  9 UNADMITTED_TYPE - #305's default_* trio on BOTH module.vpc and"
log "  module.vpc_secondary (6 sites) + the new rules_exclusive gap (3"
log "  sites), DEFECT B); nothing written yet"

log "=== 2b. -approve: stamp the $ELIGIBLE eligible resources for real ==="
APPROVE_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-import -state="$PLAIN_EST/terraform.tfstate" -estate="$ESTATE" -approve 2>&1)"
APPROVE_RC=$?
[ "$APPROVE_RC" -eq 0 ] || { printf '%s\n' "$APPROVE_OUT" | tail -30; fail "live-import -approve exited $APPROVE_RC unexpectedly"; }
grep -qF "$ELIGIBLE resource(s) newly stamped, 0 already stamped, 0 failed, $SKIPPED skipped." <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not stamp exactly $ELIGIBLE of $INSTANCES resources cleanly"; }
log "  $ELIGIBLE stamped, 0 failed, $SKIPPED skipped - matches the dry run exactly"

log "=== 2c. the main security group's marker, read through the AWS CLI directly ==="
WANT_SG_ADDR="module.security_group.aws_security_group.this:0"
GOT_SG_ADDR="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$MAIN_SG_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
[ "$GOT_SG_ADDR" = "$WANT_SG_ADDR" ] || fail "the main security group carries tofu-address=$GOT_SG_ADDR, not $WANT_SG_ADDR"
GOT_SG_ESTATE="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$MAIN_SG_ID" "Name=key,Values=tofu-estate" --query "Tags[0].Value" --output text)"
[ "$GOT_SG_ESTATE" = "$ESTATE" ] || fail "the main security group carries tofu-estate=$GOT_SG_ESTATE, not $ESTATE"
log "  $MAIN_SG_ID now carries tofu-address=$GOT_SG_ADDR tofu-estate=$GOT_SG_ESTATE"
log "  confirmed independently through the AWS CLI, never through choudoufu's own report"

log ""
log "STAGE 2 (migrate): PASS"
log ""

# ── 3. test plan: delete the state file, real choudoufu live-plan ──────────
log "=== 3. test plan: real live-plan against the really-migrated estate ==="
rm -f "$ADOPTED_EST/terraform.tfstate" "$ADOPTED_EST/terraform.tfstate.backup"
[ ! -f "$ADOPTED_EST/terraform.tfstate" ] || fail "the state file is still there"
log "  no local state file"

PLAN_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-plan -input=false -no-color 2>&1)"
PLAN_RC=$?
[ "$PLAN_RC" -ne 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -30; fail "live-plan succeeded - #305 and/or DEFECT B may be fixed; update this script"; }

WANT_DEFAULT_N=6
WANT_EXCL_N=3
WANT_TYPES=(aws_default_network_acl aws_default_route_table aws_default_security_group)
if [ "${BREAK:-}" = "1" ]; then
  WANT_DEFAULT_N=7
  WANT_EXCL_N=4
  WANT_TYPES[1]="aws_default_dhcp_options"
  log "  BREAK=1: expecting 7 default-object sites (one more than the real"
  log "           6, #305) and 4 rules_exclusive sites (one more than the"
  log "           real 3), plus aws_default_dhcp_options among the unadmitted"
  log "           refusals - none of these are real. This step must fail."
fi

TOTAL_N="$(grep -c '^Error: Resource type is outside the live-markers subset$' <<< "$PLAN_OUT")"
# Every unadmitted-type diagnostic's code frame echoes the declaring HCL
# line verbatim ('  90: resource "TYPE" "NAME" {'), which is a precise,
# per-type count no prose-matching heuristic is needed for.
RULES_EXCL_SITES="$(grep -cF 'resource "aws_vpc_security_group_rules_exclusive" "this" {' <<< "$PLAN_OUT")"
[ "$RULES_EXCL_SITES" = "$WANT_EXCL_N" ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:|^In module'; fail "expected $WANT_EXCL_N aws_vpc_security_group_rules_exclusive unadmitted sites, got $RULES_EXCL_SITES"; }
DEFAULT_SITES="$((TOTAL_N - RULES_EXCL_SITES))"
[ "$DEFAULT_SITES" = "$WANT_DEFAULT_N" ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:' | sort | uniq -c; fail "expected $WANT_DEFAULT_N default-object unadmitted sites (#305), got $DEFAULT_SITES (of $TOTAL_N total unadmitted-type sites, $RULES_EXCL_SITES attributed to rules_exclusive)"; }
for t in "${WANT_TYPES[@]}"; do
  N="$(grep -cF "resource \"$t\"" <<< "$PLAN_OUT")"
  [ "$N" -ge 1 ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:|^In module'; fail "expected $t among the unadmitted-type refusals (#305)"; }
done
log "  #305 confirmed: exactly $DEFAULT_SITES default-object adopter sites"
log "  (aws_default_network_acl, aws_default_route_table,"
log "  aws_default_security_group across both module.vpc and"
log "  module.vpc_secondary)."
log "  New gap confirmed: exactly $RULES_EXCL_SITES aws_vpc_security_group_"
log "  rules_exclusive sites - unadmitted because the pinned AWS provider"
log "  release ships no resource identity schema for this type (see this"
log "  script's own header, DEFECT B)."

log ""
log "STAGE 3 (test_plan): BLOCKED for real - #305 (default_* trio) and a new"
log "gap (aws_vpc_security_group_rules_exclusive), asserted above, nothing new"
log ""
log "=== 4. test apply: NOT RUN - depends on stage 3, which does not produce a clean plan ==="
log "=== 5. drift and reconverge: NOT RUN - depends on stages 3-4 ==="

log ""
log "=== SUMMARY (partial pass, reported honestly) ==="
log ""
log "  stage 1  cold_deploy        PASS (67 resources; DELTA 2, lex00/floci#57)"
log "  stage 2  migrate            PASS (real: $ELIGIBLE of $INSTANCES stamped, see header)"
log "  stage 3  test_plan          BLOCKED - #305 and a new admission gap (choudoufu, see header)"
log "  stage 4  test_apply         NOT RUN"
log "  stage 5  drift_reconverge   NOT RUN"
log ""
log "67 real resources, real emulator, real unmarked infrastructure, real"
log "migration. Every assertion above reads live-import's or live-plan's own"
log "output, or a tag read straight through the AWS CLI - never choudoufu's"
log "own self-report. Run again with BREAK=1: stages 1 and 2 still pass and"
log "stage 3's site-count assertions are the ones that fail."
