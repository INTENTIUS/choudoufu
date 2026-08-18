#!/usr/bin/env bash
set -uo pipefail

# A real third-party estate crossed against a real emulator:
# .corpus/rds/examples/complete-postgres, from terraform-aws-modules/
# terraform-aws-rds (pinned in live/corpus-manifest.json at tag v7.2.1,
# commit 9920097a4). RDS is one of the most commonly deployed AWS services
# via Terraform, Postgres is the most common engine choice, and this module
# is the de facto standard way people provision it - the configuration an
# average user copies when they first reach for RDS. It had never been
# crossed against a cloud before this script existed; #102 only ever used
# it for a static, offline refusal-ranking measurement.
#
# THREE REAL DEFECTS THIS ESTATE FOUND, none of them narrow to RDS:
#
#   DEFECT A (choudoufu, blocks stage 2 outright). `choudoufu live-import`
#   considers root-module managed resource instances ONLY
#   (internal/live/liveimport/ratify.go: "if !mod.Addr.IsRoot() { ...
#   continue }", cited in its own doc comment as "see issue #59"). Every
#   single resource in this estate - all 39 of them - lives inside a child
#   module (module.vpc, module.security_group, module.db, module.db_default),
#   because that is how virtually every reusable, realistic Terraform
#   configuration is written. Issue #59 ("Epic: admit child modules") is
#   CLOSED, and identity/discovery/stamp/lint/mv all walk module trees now
#   (59b static, 59c keyed for_each) - but liveimport's own restriction was
#   never lifted to match, and its doc comment's citation of #59 is stale.
#   The practical result: the dedicated "migrate an existing state-backed
#   estate to markers" tool cannot migrate ANY module-structured estate,
#   which is most of them. This is not a niche gap; it is close to the
#   whole point of the tool. Root-causing this needs liveimport's Ratify to
#   read the estate's config tree (which it currently never opens at all -
#   see ratify.go's imports) to resolve a module-nested resource's provider
#   configuration the same way stamp/discovery already do, which is a real
#   design item, not a one-line fix. Reported here rather than attempted -
#   see the HANDOFF entry this script's landing commit points to.
#
#   DEFECT B (choudoufu, would ALSO block stage 3 once A is fixed).
#   The lint phase (no cloud call) refuses 35 sites under rule
#   count-index-in-tag ("count.index is not available in resource
#   arguments"). 28 of those 35 are module.vpc's own per-AZ resources
#   (aws_route_table_association.database, aws_network_acl_rule,
#   aws_vpn_gateway_route_propagation, aws_route.*) indexing count.index
#   into a SIBLING MANAGED RESOURCE's own attributes, e.g.
#   `element(aws_subnet.database[*].id, count.index)` - genuinely not
#   knowable before those subnets exist, so those 28 are CORRECT, expected
#   refusals, not a defect.
#
#   The remaining 7 are the real defect, and they are all
#   aws_security_group_rule.ingress_with_cidr_blocks (this estate's own
#   ingress rule, and a near-universal terraform-aws-modules/security-group
#   pattern): the identity-bearing arguments are built through
#   `lookup(var.ingress_with_cidr_blocks[count.index], "from_port",
#   var.rules[lookup(var.ingress_with_cidr_blocks[count.index], "rule",
#   "_")][0])` - a lookup() whose DEFAULT branch is itself a nested
#   lookup()-keyed index into another variable, both fully static (a
#   user-supplied literal plus the module's own bundled rules table, no
#   managed resource involved anywhere in the chain). HCL evaluates both
#   branches of lookup()'s arguments eagerly, so the static identity walker
#   has to prove the whole expression safe even though, for every element
#   this estate actually supplies, the explicit branch is what wins at
#   runtime - and it cannot, so it refuses a statically-resolvable value.
#   Demonstrated in stage 3 below.
#
#   DEFECT C (choudoufu, would ALSO block stage 3 once A is fixed).
#   aws_default_network_acl, aws_default_route_table and
#   aws_default_security_group - the VPC module's "adopt the account's
#   default objects" resources, created by this estate exactly as
#   terraform-aws-modules/vpc creates them for most of its users - are
#   unadmitted types: "not in the live-markers admission table, and neither
#   the provider's identity schema nor this configuration's own arguments
#   settled its identity either" (rule unadmitted-type). The refusal's own
#   text names why: these default_* adopters have no CFN resource and no
#   identity schema of their own, so nothing but a hand-ratified table entry
#   can say what identifies one. The SAME lint pass reports two more sites
#   of the identical rule - aws_default_vpc and aws_vpn_gateway_attachment -
#   for module.vpc blocks this estate's own variables leave at count = 0;
#   the identity walker evaluates every declared block regardless, so the
#   refusal is a lint-time SITE count (5), not this estate's own applied
#   INSTANCE count (3 - the three default_* adopters actually created; see
#   stage 1's resource list). Also demonstrated in stage 3, no cloud.
#
# TWO REAL FLOCI GAPS (genuine emulator gaps, not choudoufu bugs, filed and
# worked around with documented deltas so stage 1 can still stand the
# estate up for real):
#
#   floci-io/floci#51 (via the lex00/floci fork). aws_db_instance_
#   automated_backups_replication has no matching RDS action
#   (Start/StopDBInstanceAutomatedBackupsReplication). DELTA 2 removes that
#   module and the KMS key that only fed it.
#
#   lex00/floci#52. SecretsManager RotateSecret unconditionally requires a
#   Lambda ARN, but manage_master_user_password_rotation = true - the
#   module's own default posture - creates an RDS-managed secret that uses
#   AWS's Lambda-less "hosted rotation" and never has one, so the apply
#   hangs retrying an InvalidRequestException. DELTA 3 disables it.
#
# WHAT THIS SCRIPT ACTUALLY PROVES, GIVEN ALL OF THE ABOVE:
#
#   stage 1  cold deploy   PASS - real, verified, unmarked infrastructure.
#   stage 2  migrate       BLOCKED by DEFECT A, asserted precisely (a
#                          control, same spirit as other corpus-crossing
#                          scripts' deliberately-broken steps: if this ever
#                          reports something OTHER than the current known
#                          bad behavior, the assertion fails loudly).
#   stage 3  test plan     Cannot be run for real (nothing was migrated to
#                          test against). DEFECTS B and C are demonstrated
#                          instead, independently of A, with no cloud call
#                          at all.
#   stage 4  test apply    NOT RUN - depends on stage 3.
#   stage 5  drift/reconverge  NOT RUN - depends on stage 2-4.
#
# A partial, honestly-reported pass is the point: this is the real, current
# behavior of choudoufu against a real, popular module, not a green claim
# routed around the truth.
#
#   bash live/e2e/corpus-rds-complete-postgres/run.sh
#
# Needs Docker, the AWS CLI, terraform (real, stock terraform - stage 1 is
# deliberately NOT choudoufu, to prove the estate is real and buildable on
# its own), and .corpus populated (`just corpus-fetch`).
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4720, clear of every
#                other live/e2e fixture's port).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        set to 1 to corrupt the expected stage-2 defect assertion,
#                proving it is load-bearing rather than a grep that always
#                matches.
#
# The corpus checkout is shared across worktrees and is NEVER written to:
# the estate is copied out first (twice - once for the cold, unmarked
# deploy, once for the migration attempt) and every delta below lands on a
# copy.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CORPUS_DIR="${CORPUS_DIR:-$ROOT/.corpus}"
SRC="$CORPUS_DIR/rds"
WORK="$(mktemp -d)"
FLOCI_PORT="${FLOCI_PORT:-4720}"
FLOCI_NAME="choudoufu-corpus-rds-complete-postgres-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="rds-complete-postgres"
REGION="eu-west-1"
INSTANCES=39

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }

# copy_tree DEST - the rds module root plus examples/complete-postgres,
# preserving the relative layout the example's `source = "../../"` needs.
copy_tree() {
  local dest="$1"
  mkdir -p "$dest/rds/examples"
  cp -R "$SRC/main.tf" "$SRC/variables.tf" "$SRC/outputs.tf" "$SRC/versions.tf" "$SRC/modules" "$dest/rds/"
  cp -R "$SRC/examples/complete-postgres" "$dest/rds/examples/complete-postgres"
  rm -rf "$dest/rds/examples/complete-postgres/.terraform" \
         "$dest/rds/examples/complete-postgres/.terraform.lock.hcl" \
         "$dest/rds/examples/complete-postgres/terraform.tfstate" \
         "$dest/rds/examples/complete-postgres/terraform.tfstate.backup"
}

# ── 0. tools and corpus ─────────────────────────────────────────────────────
log "=== 0. tools and corpus ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v terraform >/dev/null 2>&1 || fail "the terraform binary is not on PATH - stage 1 is deliberately plain terraform, not choudoufu"
[ -d "$SRC/examples/complete-postgres" ] || fail "$SRC/examples/complete-postgres is missing - run 'just corpus-fetch' first"

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
PLAIN_EST="$PLAIN/rds/examples/complete-postgres"
log "  estate copied out of .corpus into $PLAIN_EST"

# ── 1. cold deploy: plain terraform, no live block, no choudoufu ───────────
log "=== 1. cold deploy: plain terraform, 39 real resources ==="

# DELTA 1, onboarding: emulator flags on the estate's one provider block.
perl -0pi -e 's/^(provider "aws" \{\n  region = local\.region\n)\}/$1  access_key                   = "test" # DELTA 1\n  secret_key                   = "test"\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  skip_requesting_account_id   = true\n  s3_use_path_style            = true\n}/' "$PLAIN_EST/main.tf"
grep -q 'DELTA 1' "$PLAIN_EST/main.tf" || fail "DELTA 1 did not match the provider block - the corpus pin has moved"
log "  DELTA 1  emulator flags on the provider block             (onboarding)"

# DELTA 2, EMULATOR GAP (floci-io/floci#51): aws_db_instance_automated_
# backups_replication has no matching RDS action in floci. The KMS key
# existed only to feed it, so it goes too.
perl -0pi -e 's/provider "aws" \{\n  alias  = "region2"\n  region = local\.region2\n\}\n\nmodule "kms" \{.*?\n\}\n\nmodule "db_automated_backups_replication" \{.*?\n\}\n\n/# DELTA 2 (EMULATOR GAP, floci-io\/floci#51): region2 provider, the kms\n# module, and db_automated_backups_replication removed.\n# aws_db_instance_automated_backups_replication calls RDS\n# StartDBInstanceAutomatedBackupsReplication, which floci does not implement.\n\n/s' "$PLAIN_EST/main.tf"
grep -q 'DELTA 2' "$PLAIN_EST/main.tf" || fail "DELTA 2 did not match the automated-backups-replication block - the corpus pin has moved"
grep -q 'module "kms"' "$PLAIN_EST/main.tf" && fail "DELTA 2 left the kms module behind"
log "  DELTA 2  automated-backups-replication + kms removed      (EMULATOR GAP, floci-io/floci#51)"

# DELTA 3, EMULATOR GAP (lex00/floci#52): floci's SecretsManager RotateSecret
# unconditionally requires a Lambda ARN. RDS-managed secret rotation
# (manage_master_user_password_rotation = true, the module's own default
# posture alongside manage_master_user_password) is Lambda-less on real AWS,
# so the apply hangs retrying an InvalidRequestException against floci.
perl -pi -e 's/^(  manage_master_user_password_rotation)(\s*)= true$/$1$2= false # DELTA 3 (EMULATOR GAP, lex00\/floci#52)/' "$PLAIN_EST/main.tf"
grep -q 'DELTA 3' "$PLAIN_EST/main.tf" || fail "DELTA 3 did not match manage_master_user_password_rotation - the corpus pin has moved"
log "  DELTA 3  manage_master_user_password_rotation disabled    (EMULATOR GAP, lex00/floci#52)"

log "=== 1a. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"rds"' <<< "${HEALTH:-}" && break
  sleep 2
done
grep -q '"rds"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (rds) at $ENDPOINT"
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
log "  real terraform.tfstate, zero choudoufu markers - VPC, security group,"
log "  2 RDS Postgres instances (module.db + module.db_default), parameter"
log "  group, 2 CloudWatch log groups, enhanced-monitoring IAM role"

# Confirmed unmarked: read the primary DB instance's tags directly, never
# through choudoufu.
DB_ARN="$(awsl rds describe-db-instances --db-instance-identifier complete-postgresql \
  --query 'DBInstances[0].DBInstanceArn' --output text)"
[ -n "$DB_ARN" ] && [ "$DB_ARN" != "None" ] || fail "could not find the complete-postgresql DB instance through the AWS CLI"
MARKER_COUNT="$(awsl rds list-tags-for-resource --resource-name "$DB_ARN" \
  --query "length(TagList[?Key=='tofu-address'])" --output text)"
[ "$MARKER_COUNT" = "0" ] || fail "the DB instance already carries a tofu-address tag before migration - this crossing proves nothing"
log "  confirmed unmarked: $DB_ARN carries no tofu-address tag"

# ── 2. migrate: choudoufu live-import against the plain state file ─────────
log "=== 2. migrate: choudoufu live-import ==="

ADOPTED="$WORK/adopted"
copy_tree "$ADOPTED"
ADOPTED_EST="$ADOPTED/rds/examples/complete-postgres"
# Carry the same three deltas so the adopted config is otherwise identical
# to what is actually standing (module structure has to match for live-plan
# to resolve the same addresses).
perl -0pi -e 's/^(provider "aws" \{\n  region = local\.region\n)\}/$1  access_key                   = "test" # DELTA 1\n  secret_key                   = "test"\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  skip_requesting_account_id   = true\n  s3_use_path_style            = true\n}/' "$ADOPTED_EST/main.tf"
perl -0pi -e 's/provider "aws" \{\n  alias  = "region2"\n  region = local\.region2\n\}\n\nmodule "kms" \{.*?\n\}\n\nmodule "db_automated_backups_replication" \{.*?\n\}\n\n/# DELTA 2 (EMULATOR GAP, floci-io\/floci#51)\n\n/s' "$ADOPTED_EST/main.tf"
perl -pi -e 's/^(  manage_master_user_password_rotation)(\s*)= true$/$1$2= false # DELTA 3 (EMULATOR GAP, lex00\/floci#52)/' "$ADOPTED_EST/main.tf"

# DELTA 4, onboarding: add the live block. record_store is needed for
# module.db_default's random_id.snapshot_identifier (an effects-only
# resource - see the record-store fixture; skip_final_snapshot defaults to
# false and module.db_default does not override it).
perl -0pi -e "s/(required_providers \{\n    aws = \{\n      source  = \"hashicorp\/aws\"\n      version = \">= 6.28\"\n    \}\n  \}\n)\}/\$1\n  live {\n    estate = \"$ESTATE\"\n\n    record_store \"local\" {\n      path = \".tofu-records\"\n    }\n  }\n}/" "$ADOPTED_EST/versions.tf"
grep -q "estate = \"$ESTATE\"" "$ADOPTED_EST/versions.tf" || fail "DELTA 4 did not match versions.tf - the corpus pin has moved"
log "  DELTA 4  live block + local record_store added             (onboarding)"

( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "adopted init failed"; }

IMPORT_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-import -state="$PLAIN_EST/terraform.tfstate" -estate="$ESTATE" 2>&1)"
IMPORT_RC=$?
[ "$IMPORT_RC" -eq 0 ] || { printf '%s\n' "$IMPORT_OUT" | tail -30; fail "live-import (dry run) exited $IMPORT_RC unexpectedly"; }

WANT_CHILD="$INSTANCES"
if [ "${BREAK:-}" = "1" ]; then
  WANT_CHILD="$((INSTANCES - 1))"
  log "  BREAK=1: expecting $WANT_CHILD non-root instances, one short of the"
  log "           real $INSTANCES - this step must fail"
fi
grep -qF "0 of 0 resource instance(s) are eligible for stamping (VERIFIED or DRIFTED)." <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import reported something OTHER than 0 eligible - DEFECT A may be fixed; read live/e2e/corpus-rds-complete-postgres/run.sh's header and update this script"; }
grep -qF "$WANT_CHILD resource instance(s) in a non-root module were not considered (root module only, v1; see issue #59)." <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import did not report exactly $WANT_CHILD non-root instances excluded"; }
log "  DEFECT A confirmed: live-import excludes all $INSTANCES resources -"
log "  every one of them lives inside module.vpc, module.security_group,"
log "  module.db or module.db_default. Root module only, v1; see issue #59"
log "  (closed - its own doc comment's citation is stale, see this script's"
log "  header)."

APPROVE_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-import -state="$PLAIN_EST/terraform.tfstate" -estate="$ESTATE" -approve 2>&1)"
APPROVE_RC=$?
[ "$APPROVE_RC" -eq 0 ] || { printf '%s\n' "$APPROVE_OUT" | tail -30; fail "live-import -approve exited $APPROVE_RC unexpectedly"; }
grep -qF "0 resource(s) newly stamped, 0 already stamped, 0 failed, 0 skipped." <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve stamped something - DEFECT A may be fixed; update this script"; }
log "  -approve confirms it: 0 stamped. No marker exists to plan, apply, or"
log "  drift-reconverge against - stages 3-5 cannot run against real"
log "  migrated markers."

# Read the DB instance's tags again: still unmarked, since nothing above
# could have written one.
STILL_UNMARKED="$(awsl rds list-tags-for-resource --resource-name "$DB_ARN" \
  --query "length(TagList[?Key=='tofu-address'])" --output text)"
[ "$STILL_UNMARKED" = "0" ] || fail "the DB instance carries a tofu-address tag after a run that reported 0 stamped - contradicts live-import's own report"
log "  confirmed: $DB_ARN still carries no tofu-address tag"

# ── 3. what stage 3 (test plan) would ALSO hit - no cloud needed ───────────
log "=== 3. test plan: cannot run for real; DEFECTS B and C instead ==="
log "  Nothing was migrated in stage 2, so there is no real empty-plan"
log "  claim to make. What follows is not stage 3 - it is proof that fixing"
log "  DEFECT A alone would not be enough: choudoufu live-plan's identity"
log "  resolution (lint phase, no cloud call) refuses this SAME estate on"
log "  two more grounds, independently of migration."

PLAN_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-plan -input=false -no-color 2>&1)"
PLAN_RC=$?
[ "$PLAN_RC" -ne 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -30; fail "live-plan succeeded - DEFECTS B and/or C may be fixed; update this script"; }
# choudoufu wraps its "In module.X, ... RESOURCE.NAME:" context lines at a
# fixed column when captured non-interactively, sometimes splitting the
# resource name onto its own line. Flattened to one line per diagnostic
# clause (blank-line-separated) so a substring match is not at the mercy of
# where the wrap happened to land.
PLAN_FLAT="$(awk 'BEGIN{RS=""} {gsub(/\n/," "); print; print "@@CLAUSE@@"}' <<< "$PLAN_OUT")"

# 35 count-index-in-tag sites total. 28 belong to module.vpc's own per-AZ
# resources indexing a SIBLING resource's attributes (element(aws_subnet...
# [*].id, count.index)) - genuinely unknowable before those subnets exist,
# so those are correct, expected refusals, not asserted on individually
# here. The other 7 are all aws_security_group_rule.ingress_with_cidr_
# blocks, and THAT is DEFECT B: a fully static expression (a literal plus
# the module's own bundled rules table, no managed resource involved)
# refused anyway.
CIDX_N="$(grep -c '^Error: count.index is not available in resource arguments$' <<< "$PLAN_OUT")"
[ "$CIDX_N" = "35" ] || { grep -E '^Error:' <<< "$PLAN_OUT" | sort | uniq -c; fail "expected 35 count-index-in-tag sites total, got $CIDX_N"; }
SGR_N="$(grep -oF 'count.index in aws_security_group_rule.ingress_with_cidr_blocks:' <<< "$PLAN_FLAT" | wc -l | tr -d ' ')"
[ "$SGR_N" = "7" ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:|^In module'; fail "expected 7 aws_security_group_rule.ingress_with_cidr_blocks count-index sites (DEFECT B), got $SGR_N"; }
VPC_CIDX_N="$(grep -oE 'In module\.vpc, count\.index in [a-zA-Z0-9_.]+:' <<< "$PLAN_FLAT" | sort -u | wc -l | tr -d ' ')"
log "  DEFECT B confirmed: 7 of the 35 count-index-in-tag sites are"
log "  aws_security_group_rule.ingress_with_cidr_blocks, a fully static"
log "  expression (terraform-aws-modules/security-group's lookup()-into-"
log "  its-own-rules-table pattern) refused anyway. The other 28 (module.vpc,"
log "  $VPC_CIDX_N distinct resource names) index a SIBLING resource's"
log "  attributes and are correctly, expectedly refused - not a defect."

DEFAULT_N="$(grep -c '^Error: Resource type is outside the live-markers subset$' <<< "$PLAN_OUT")"
[ "$DEFAULT_N" = "5" ] || { grep -E '^Error:' <<< "$PLAN_OUT" | sort | uniq -c; fail "expected 5 unadmitted-type sites (DEFECT C), got $DEFAULT_N"; }
for t in aws_default_network_acl aws_default_route_table aws_default_security_group aws_default_vpc aws_vpn_gateway_attachment; do
  grep -qE "In module\.[a-z_]+, ${t}\." <<< "$PLAN_FLAT" \
    || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:|^In module'; fail "expected $t among the unadmitted-type refusals"; }
done
log "  DEFECT C confirmed: 5 unadmitted-type sites. aws_default_network_acl,"
log "  aws_default_route_table and aws_default_security_group are the three"
log "  default-object adopters this estate actually creates; aws_default_vpc"
log "  and aws_vpn_gateway_attachment are two more the same lint pass"
log "  refuses in declared-but-count-0 module.vpc blocks."

log ""
log "=== 4. test apply: NOT RUN - depends on stage 3, which did not pass ==="
log "=== 5. drift and reconverge: NOT RUN - depends on stages 2-4 ==="

log ""
log "=== SUMMARY (partial pass, reported honestly) ==="
log ""
log "  stage 1  cold_deploy        PASS"
log "  stage 2  migrate            BLOCKED - DEFECT A (choudoufu, see header)"
log "  stage 3  test_plan          BLOCKED - DEFECTS B and C (choudoufu, see header)"
log "  stage 4  test_apply         NOT RUN"
log "  stage 5  drift_reconverge   NOT RUN"
log ""
log "39 real resources, real emulator, real unmarked infrastructure, real"
log "migration attempt. Every assertion above reads live-import's or"
log "live-plan's own output, or a tag read straight through the AWS CLI -"
log "never choudoufu's own self-report. Run again with BREAK=1: stage 1"
log "still passes and stage 2's defect-A assertion is the one that fails."
