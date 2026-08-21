#!/usr/bin/env bash
set -uo pipefail

# terraform-aws-modules/terraform-aws-ecs's flagship "fargate" example
# (.corpus/ecs/examples/fargate, pinned in live/corpus-manifest.json at tag
# v7.6.0, commit c83279b39). Fargate is the most common modern way people
# run ECS (the module's other example, ec2-autoscaling, is the older
# launch type and is not this crossing's target), and this module is the
# de facto standard way people provision it. It had never been crossed
# against a cloud before this script existed.
#
# 62 real resources: an ECS cluster (Container Insights + a FARGATE/
# FARGATE_SPOT default capacity-provider split), an ECS service behind an
# ALB with a BLUE_GREEN deployment_configuration, ECS Exec
# (enable_execute_command), ECS Service Connect, a two-container task
# definition (a "fluent-bit" firelens sidecar reading its image from a
# public AWS-managed SSM parameter, plus the app container), a second,
# standalone task definition (no service) with a volume and an ARM64
# runtime platform, a CloudMap HTTP namespace, an ALB module (2 target
# groups, blue/green listener rules), and a VPC module underneath it all.
#
# ONE EMULATOR-GAP WORKAROUND, not a config edit (DELTA 2 below): floci does
# not mirror AWS's own published SSM parameter catalog (the same class of
# gap floci-io/floci's AMI-catalog fixes address for EC2/EKS/sumaform, just
# not extended to SSM). `data.aws_ssm_parameter.fluentbit` reads
# `/aws/service/aws-for-fluent-bit/stable`, a real parameter AWS itself
# publishes with a real value in production - it is seeded via the AWS CLI
# before stage 1's apply, not edited out of the configuration or worked
# around with a variable.
#
# STAGE-BY-STAGE SHAPE (issue #274's five-stage pipeline):
#
#   1. COLD DEPLOY   plain `terraform apply` (real HashiCorp terraform, not
#                     choudoufu), no live block. PASS: a single, clean
#                     apply, 62 added, 0 changed, 0 destroyed.
#   2. MIGRATE       `choudoufu live-import -state=<plain's state>
#                     -estate=... -approve`. PASS: 46 of 62 instances
#                     eligible (28 VERIFIED + 18 DRIFTED), the other 16
#                     UNTAGGABLE by provider schema in the dry run - of
#                     which -approve records 1 (module.ecs_cluster's
#                     time_sleep.this[0], record-backed since #340, seeded
#                     into the record store rather than skipped) and
#                     correctly skips 15; #305's default_* trio is admitted
#                     now and stamps cleanly - 2 VERIFIED + 1 DRIFTED, see
#                     below - and 0 failed. Two
#                     markers - the ECS cluster's and the ECS service's own
#                     - confirmed independently through the AWS CLI.
#   3. TEST PLAN     delete the state file, `choudoufu live-plan`. #305 and
#                     #308 stay FIXED (0 sites each). #313's data-source
#                     root cause (A, 48 sites) and #315's each.value gap
#                     (C, 4 sites) are BOTH now fixed too - re-verified
#                     below by direct assertion, not by omission. BLOCKED
#                     for real by one remaining site: #313's own
#                     acknowledged resource-attribute-via-module-output
#                     scope (root cause B) and its 7-site cascade (8
#                     diagnostics total, was 236). See below.
#   4. TEST APPLY    NOT RUN - depends on stage 3.
#   5. DRIFT/RECONVERGE  NOT RUN - depends on stages 3-4.
#
# #305 (admission: aws_default_network_acl/aws_default_route_table/
# aws_default_security_group were unadmitted) is FIXED. This module's VPC
# submodule adopts the account's default objects
# (manage_default_network_acl/route_table/security_group, all true by
# default), exactly as it does for most of its users. Re-verified against
# this estate below: all three now resolve through their own tofu-address
# marker and stamp in stage 2 (2 VERIFIED, 1 DRIFTED on genuine default-ACL
# drift - subnet_ids/egress/ingress, the same shape found in corpus-
# security-group-complete), and stage 3's live-plan contributes zero
# unadmitted-type refusals from them, asserted directly below rather than
# merely dropped from the old assertion.
#
# #308 IS FIXED (internal/live/identity/foreach_keyset.go's *hclsyntax.ForExpr
# case, a9ac6d06e7/b2bb59585d) - confirmed by direct assertion below (0
# "This module call cannot be expanded under live resource markers" sites,
# was 1). Fixing it did NOT unblock this estate; it let live-plan walk
# further into the configuration and reach #313's own wall (below) instead
# of stopping at #308's site first.
#
# #313 DOES REACH THIS ESTATE, but two of its three original root causes
# here are now FIXED. Re-verified against a real live-plan run below, not
# by omission:
#
#   A. FIXED (c636ab20f7, merged 0284d8c408). `data.aws_availability_zones.
#      available` feeding `local.azs` (main.tf:18, `slice(data.aws_
#      availability_zones.available.names, 0, 3)`), fed to `module "vpc"`
#      as `azs = local.azs` - the same shape filed as #313 crossing
#      corpus-security-group-complete. Was 48 root sites ("Dynamic value in
#      static context", "Unable to use data.aws_availability_zones.
#      available in static context, which is required by local.azs"), now
#      0: `resolver.frozenClosureIsStale` rebuilds a module instance's
#      `var.*` closure when a strictly-ancestral module instance carries
#      read-phase coverage, not only when a call on the path repeats. No
#      type or data-source names; the fix reaches every module-call
#      `data`-source chase in the shared resolver, not just this estate's
#      shape - see #313's own generalization measurement (57 of 204
#      `.corpus` entries with an eligible demanded source improved, this
#      estate's own offline diagnostic count among them, 0 worse).
#   B. STILL OPEN, and correctly so - this is #313's own acknowledged
#      remaining scope, not a fresh gap. `module.ecs_cluster.arn` passed
#      into `module "ecs_service"` as `cluster_arn = module.ecs_cluster.arn`
#      (main.tf:68) - a module output that is itself another resource's
#      attribute (the ECS cluster's ARN, Computed in the AWS provider's own
#      schema even though this example's cluster name is client-named and
#      the ARN is in principle a deterministic function of account, region
#      and name). The maintainer's ruling on #313 scoped the read phase to
#      `data` blocks deliberately - a data source is safe to read
#      unconditionally, a managed resource's attribute may not exist yet
#      within the same plan - and #313's own thread found the RDS crossing's
#      parallel case (`aws_vpc.cidr_block` reached through a module output)
#      lands on the identity resolver's own deliberate Computed boundary
#      (`siblingLiteralExpr`, resolve.go) even for a value a human reader
#      can see is knowable; nobody has made the ruling this would need.
#      Still exactly 1 site ("Module output not supported in static
#      context").
#   C. FIXED (#315, 772bde04d8, merged 1a3d46b767). `each.value.
#      enable_cloudwatch_logging` and `each.value.create_cloudwatch_log_group`
#      (modules/service/main.tf:923-924, inside `module
#      "container_definition"`, the same module call #308 fixed the keyset
#      resolution for) used to refuse because the walker treated the whole
#      `each.value` as one opaque dynamic blob. Was 4 sites (2 vars x 2
#      container_definitions keys); now 0 - `each.value` is projected down
#      to the one field each reference actually reads, the same way #308's
#      fix already did for the for_each expression itself.
#
# With A and C both gone, B's cascade collapsed from 177+6=183 sites to 7:
# `aws_appautoscaling_target.this[0]`'s own `resource_id` argument
# (modules/service/main.tf:1565) interpolates `local.cluster_name`, itself
# derived from `var.cluster_arn` (B) via `element(split("/", var.
# cluster_arn), 1)` - 1 "Unable to compute static value" site. That failure
# then blocks the target resource's own identity, which
# `aws_appautoscaling_policy.this["cpu"]` and `["memory"]` each reference
# through three arguments (`resource_id`, `scalable_dimension`,
# `service_namespace`, main.tf:1589-1591) - 6 "Unresolvable identity"
# cascade sites (2 policy instances x 3 arguments each). 8 diagnostics
# total (was 236), all under already-registered refusal classes
# (internal/live/passthrough/refusals.go,
# internal/live/identity/refusals.go), not new choudoufu behavior. Not
# fixed here; stage 3's assertions below hold the exact counts so a change
# to any of them is visible.
#
# WHY THE DRIFTED (18) BUCKET IS LARGE, AND WHY IT DOES NOT BLOCK STAGE 2:
# live-import tolerates drift by design (it stamps DRIFTED resources same
# as VERIFIED ones - drift is reported, not refused). Several of these 18
# are real floci round-trip gaps, found by this crossing and filed against
# the emulator, not against choudoufu: floci-io/floci (lex00 fork) #59
# (ECS CreateCluster silently drops `settings` - Container Insights never
# comes back on describe/list, though it was genuinely applied - and
# `aws_ecs_cluster_capacity_providers`' `default_capacity_provider_strategy`
# is stored but never serialized in any cluster response) and #60 (ECS
# CreateService/DescribeServices drop `scheduling_strategy`,
# `enable_ecs_managed_tags`, `enable_execute_command`,
# `health_check_grace_period_seconds`, `deployment_controller`, the
# blue-green `load_balancer.advanced_configuration`, and
# `service_connect_configuration` entirely - `scheduling_strategy` in
# particular forces the AWS provider to propose destroying and recreating
# the service on every plan after creation, independent of choudoufu).
# Neither blocks this script: DRIFTED still stamps, and stage 3 refuses
# before ever reaching a diff that would show them.
#
# STAGE 3'S REAL BLOCKER is #313's own remaining scope alone (root cause B
# above) plus its 7-site cascade - see the section above for the full
# breakdown by exact rule and exact site. #305, #308, #313's root cause A
# and #315 are all fixed and confirmed absent below, not merely omitted.
#
# BREAK=1 corrupts every stage-3 expected count (one unadmitted-type site
# and one child-module for_each site that should not exist - #305 and #308
# are both fixed - plus one Dynamic-value-in-static-context site that
# should not exist - #313's root cause A and #315's root cause C are both
# fixed - plus one extra site each for the Module-output, static-value-
# cascade and Unresolvable-identity counts), proving those assertions are
# load-bearing rather than a grep that always matches - same discipline as
# the RDS and security-group crossings before this one. Stages 1 and 2 are
# unaffected.
#
#   bash live/e2e/corpus-ecs-fargate/run.sh
#
# Needs Docker, the AWS CLI, and the real `terraform` binary on PATH for
# stage 1, and .corpus populated (`just corpus-fetch`).
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4790, clear of every
#                other live/e2e fixture's port).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        set to 1 to corrupt the expected stage-3 site counts and
#                one expected unadmitted-type name.
#
# The corpus checkout is shared across worktrees and is NEVER written to:
# the estate is copied out first (twice - once for the cold, unmarked
# deploy, once for the migration attempt) and every delta lands on a copy.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CORPUS_DIR="${CORPUS_DIR:-$ROOT/.corpus}"
SRC="$CORPUS_DIR/ecs"
WORK="$(mktemp -d)"
FLOCI_PORT="${FLOCI_PORT:-4790}"
FLOCI_NAME="choudoufu-corpus-ecs-fargate-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="ecs-fargate-crossing"
REGION="eu-west-1"
INSTANCES=62
ELIGIBLE=46
SKIPPED=16
# SKIPPED is the DRY RUN's untaggable bucket, which -approve then splits in
# two (issue #340): module.ecs_cluster's time_sleep.this[0] is record-backed,
# so -approve seeds the record store for it and reports it RECORDED rather
# than SKIPPED. The dry run's own UNTAGGABLE count does not move -
# ratifyRecordBacked still answers StatusUntaggable - so only the -approve
# summary line splits.
RECORDED=1
APPROVE_SKIPPED=$((SKIPPED - RECORDED))
VERIFIED_WANT=28
DRIFTED_WANT=18
UNTAGGABLE_WANT=16
UNADMITTED_WANT=0
FLUENTBIT_PARAM="/aws/service/aws-for-fluent-bit/stable"

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

# copy_tree DEST - the ecs module root plus examples/fargate, preserving the
# relative layout the example's `source = "../../modules/..."` needs.
copy_tree() {
  local dest="$1"
  mkdir -p "$dest/ecs/examples"
  cp -R "$SRC/main.tf" "$SRC/variables.tf" "$SRC/outputs.tf" "$SRC/versions.tf" "$SRC/modules" "$dest/ecs/"
  cp -R "$SRC/examples/fargate" "$dest/ecs/examples/fargate"
  rm -rf "$dest/ecs/examples/fargate/.terraform" \
         "$dest/ecs/examples/fargate/.terraform.lock.hcl" \
         "$dest/ecs/examples/fargate/terraform.tfstate" \
         "$dest/ecs/examples/fargate/terraform.tfstate.backup"
}

gauntlet_begin

# ── 0. tools and corpus ─────────────────────────────────────────────────────
log "=== 0. tools and corpus ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v terraform >/dev/null 2>&1 || fail "the terraform binary is not on PATH - stage 1 is deliberately plain terraform, not choudoufu"
[ -d "$SRC/examples/fargate" ] || fail "$SRC/examples/fargate is missing - run 'just corpus-fetch' first"

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
PLAIN_EST="$PLAIN/ecs/examples/fargate"
log "  estate copied out of .corpus into $PLAIN_EST"

CURRENT_STAGE=cold_deploy
# ── 1. cold deploy: plain terraform, no live block, no choudoufu ───────────
log "=== 1. cold deploy: plain terraform, 62 real resources ==="

# DELTA 1, onboarding: emulator flags on the estate's one provider block.
perl -0pi -e 's/^(provider "aws" \{\n  region = local\.region\n)\}/$1  access_key                  = "test" # DELTA 1\n  secret_key                  = "test"\n  skip_credentials_validation = true\n  skip_metadata_api_check     = true\n  skip_requesting_account_id  = true\n  s3_use_path_style           = true\n}/' "$PLAIN_EST/main.tf"
grep -q 'DELTA 1' "$PLAIN_EST/main.tf" || fail "DELTA 1 did not match the provider block - the corpus pin has moved"
log "  DELTA 1  emulator flags on the provider block             (onboarding)"

log "=== 1a. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"ecs"' <<< "${HEALTH:-}" && break
  sleep 2
done
grep -q '"ecs"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (ecs) at $ENDPOINT"
log "  healthy"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION"

# EMULATOR-GAP WORKAROUND (not a config edit): floci does not mirror AWS's
# own published SSM parameter catalog. Seed the one real, AWS-published
# parameter this estate reads, with the real value AWS itself serves there,
# so data.aws_ssm_parameter.fluentbit resolves exactly as it would on real
# AWS - see this script's header.
awsl ssm put-parameter --name "$FLUENTBIT_PARAM" --type String \
  --value "public.ecr.aws/aws-observability/aws-for-fluent-bit:stable" >/dev/null \
  || fail "could not seed $FLUENTBIT_PARAM through the AWS CLI"
log "  seeded $FLUENTBIT_PARAM                       (EMULATOR GAP: floci has no public SSM parameter catalog)"

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
log "  real terraform.tfstate, zero choudoufu markers - ECS cluster + service"
log "  behind an ALB with blue/green + Service Connect, a standalone task"
log "  definition, a CloudMap namespace, an ALB module, a VPC module"

# Confirmed unmarked: read the cluster's tags directly through the AWS CLI,
# never through choudoufu.
CLUSTER_ARN="$(awsl ecs describe-clusters --clusters ex-fargate --query 'clusters[0].clusterArn' --output text)"
[ -n "$CLUSTER_ARN" ] && [ "$CLUSTER_ARN" != "None" ] || fail "could not find the ex-fargate ECS cluster through the AWS CLI"
MARKER_COUNT="$(awsl ecs list-tags-for-resource --resource-arn "$CLUSTER_ARN" --query "length(tags[?key=='tofu-address'])" --output text)"
[ "$MARKER_COUNT" = "0" ] || fail "the ECS cluster already carries a tofu-address tag before migration - this crossing proves nothing"
log "  confirmed unmarked: $CLUSTER_ARN carries no tofu-address tag"

log ""
log "STAGE 1 (cold deploy): PASS"
log ""
gauntlet_stage cold_deploy pass "$INSTANCES resources, once for real"
CURRENT_STAGE=migrate

# ── 2. migrate: choudoufu live-import against the plain state file ─────────
log "=== 2. migrate: choudoufu live-import ==="

ADOPTED="$WORK/adopted"
copy_tree "$ADOPTED"
ADOPTED_EST="$ADOPTED/ecs/examples/fargate"
# Carry the same emulator delta so the adopted config is otherwise identical
# to what is actually standing.
perl -0pi -e 's/^(provider "aws" \{\n  region = local\.region\n)\}/$1  access_key                  = "test" # DELTA 1\n  secret_key                  = "test"\n  skip_credentials_validation = true\n  skip_metadata_api_check     = true\n  skip_requesting_account_id  = true\n  s3_use_path_style           = true\n}/' "$ADOPTED_EST/main.tf"

# DELTA 2, onboarding: add the live block. record_store is needed for
# module.ecs_cluster's time_sleep.this[0] (an effects-only logical
# resource - see the record-store fixture).
perl -0pi -e "s/(required_providers \{\n    aws = \{\n      source  = \"hashicorp\/aws\"\n      version = \">= 6\.41\"\n    \}\n  \}\n)\}/\$1\n  live {\n    estate = \"$ESTATE\"\n\n    record_store \"local\" {\n      path = \".tofu-records\"\n    }\n  }\n}/" "$ADOPTED_EST/versions.tf"
grep -q "estate = \"$ESTATE\"" "$ADOPTED_EST/versions.tf" || fail "DELTA 2 did not match versions.tf - the corpus pin has moved"
log "  DELTA 2  live block + local record_store added             (onboarding)"

( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "adopted init failed"; }

log "=== 2a. live-import dry run: verify against the live system, write nothing ==="
IMPORT_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-import -state="$PLAIN_EST/terraform.tfstate" -estate="$ESTATE" 2>&1)"
IMPORT_RC=$?
[ "$IMPORT_RC" -eq 0 ] || { printf '%s\n' "$IMPORT_OUT" | tail -30; fail "live-import (dry run) exited $IMPORT_RC unexpectedly"; }

grep -qF "$ELIGIBLE of $INSTANCES resource instance(s) are eligible for stamping (VERIFIED or DRIFTED)." <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import did not report exactly $ELIGIBLE of $INSTANCES eligible - this estate's resource shape or floci's own round-trip behavior may have moved"; }
grep -qF "No tag has been written. Rerun with -approve to stamp tofu-estate and tofu-address onto every eligible resource above." <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import's dry run did not report 'no tag written' correctly"; }

VERIFIED_N="$(grep -oE '^VERIFIED \([0-9]+\)' <<< "$IMPORT_OUT" | grep -oE '[0-9]+')"
DRIFTED_N="$(grep -oE '^DRIFTED \([0-9]+\)' <<< "$IMPORT_OUT" | grep -oE '[0-9]+')"
UNTAGGABLE_N="$(grep -oE '^UNTAGGABLE \([0-9]+\)' <<< "$IMPORT_OUT" | grep -oE '[0-9]+')"
UNADMITTED_N="$(grep -oE '^UNADMITTED_TYPE \([0-9]+\)' <<< "$IMPORT_OUT" | grep -oE '[0-9]+')"
[ "${VERIFIED_N:-0}" = "$VERIFIED_WANT" ] || fail "expected $VERIFIED_WANT VERIFIED, got ${VERIFIED_N:-0}"
[ "${DRIFTED_N:-0}" = "$DRIFTED_WANT" ] || fail "expected $DRIFTED_WANT DRIFTED, got ${DRIFTED_N:-0}"
[ "${UNTAGGABLE_N:-0}" = "$UNTAGGABLE_WANT" ] || fail "expected $UNTAGGABLE_WANT UNTAGGABLE, got ${UNTAGGABLE_N:-0}"
[ "${UNADMITTED_N:-0}" = "$UNADMITTED_WANT" ] || fail "expected $UNADMITTED_WANT UNADMITTED_TYPE (#305 is fixed - none expected), got ${UNADMITTED_N:-0}"
# #305 fixed: the vpc submodule's three default-object adopters now resolve
# and stamp like any other server-assigned type (2 VERIFIED, 1 DRIFTED on
# genuine default-ACL drift), not skipped as UNADMITTED_TYPE.
grep -qF 'module.vpc.aws_default_route_table.default[0]' <<< "$IMPORT_OUT" || fail "expected module.vpc.aws_default_route_table.default[0] among VERIFIED (#305 fixed)"
grep -qF 'module.vpc.aws_default_security_group.this[0]' <<< "$IMPORT_OUT" || fail "expected module.vpc.aws_default_security_group.this[0] among VERIFIED (#305 fixed)"
grep -qF 'module.vpc.aws_default_network_acl.this[0]' <<< "$IMPORT_OUT" || fail "expected module.vpc.aws_default_network_acl.this[0] among DRIFTED (#305 fixed)"
log "  $ELIGIBLE of $INSTANCES eligible ($VERIFIED_WANT VERIFIED + $DRIFTED_WANT DRIFTED); $SKIPPED skipped"
log "  ($UNTAGGABLE_WANT UNTAGGABLE by provider schema; #305's default_* trio is"
log "  admitted now and stamped above); nothing written yet"

log "=== 2b. -approve: stamp the $ELIGIBLE eligible resources for real ==="
APPROVE_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-import -state="$PLAIN_EST/terraform.tfstate" -estate="$ESTATE" -approve 2>&1)"
APPROVE_RC=$?
[ "$APPROVE_RC" -eq 0 ] || { printf '%s\n' "$APPROVE_OUT" | tail -30; fail "live-import -approve exited $APPROVE_RC unexpectedly"; }
grep -qF "$ELIGIBLE resource(s) newly stamped, 0 already stamped, $RECORDED newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, $APPROVE_SKIPPED skipped." <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not stamp exactly $ELIGIBLE of $INSTANCES resources cleanly ($RECORDED recorded, $APPROVE_SKIPPED skipped)"; }
log "  $ELIGIBLE stamped, $RECORDED recorded (time_sleep.this[0]), 0 failed,"
log "  $APPROVE_SKIPPED skipped - $SKIPPED untaggable in the dry run, one of them record-backed"

log "=== 2c. the cluster's and the service's own markers, read through the AWS CLI directly ==="
WANT_CLUSTER_ADDR="module.ecs_cluster.aws_ecs_cluster.this:0"
GOT_CLUSTER_ADDR="$(awsl ecs list-tags-for-resource --resource-arn "$CLUSTER_ARN" --query "tags[?key=='tofu-address'].value | [0]" --output text)"
[ "$GOT_CLUSTER_ADDR" = "$WANT_CLUSTER_ADDR" ] || fail "the ECS cluster carries tofu-address=$GOT_CLUSTER_ADDR, not $WANT_CLUSTER_ADDR"
log "  $CLUSTER_ARN now carries tofu-address=$GOT_CLUSTER_ADDR"

SVC_ARN="$(awsl ecs describe-services --cluster ex-fargate --services ex-fargate --query 'services[0].serviceArn' --output text)"
[ -n "$SVC_ARN" ] && [ "$SVC_ARN" != "None" ] || fail "could not find the ex-fargate ECS service through the AWS CLI"
WANT_SVC_ADDR="module.ecs_service.aws_ecs_service.this:0"
GOT_SVC_ADDR="$(awsl ecs list-tags-for-resource --resource-arn "$SVC_ARN" --query "tags[?key=='tofu-address'].value | [0]" --output text)"
[ "$GOT_SVC_ADDR" = "$WANT_SVC_ADDR" ] || fail "the ECS service carries tofu-address=$GOT_SVC_ADDR, not $WANT_SVC_ADDR"
log "  $SVC_ARN now carries tofu-address=$GOT_SVC_ADDR"
log "  confirmed independently through the AWS CLI, never through choudoufu's own report"

log ""
log "STAGE 2 (migrate): PASS"
log ""
gauntlet_stage migrate pass "$ELIGIBLE of $INSTANCES stamped"
CURRENT_STAGE=test_plan

# ── 3. test plan: delete the state file, real choudoufu live-plan ──────────
log "=== 3. test plan: real live-plan against the really-migrated estate ==="
rm -f "$ADOPTED_EST/terraform.tfstate" "$ADOPTED_EST/terraform.tfstate.backup"
[ ! -f "$ADOPTED_EST/terraform.tfstate" ] || fail "the state file is still there"
log "  no local state file - live-import above never wrote one (cloud tags"
log "  only)"

PLAN_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-plan -input=false -no-color 2>&1)"
PLAN_RC=$?
[ "$PLAN_RC" -ne 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -30; fail "live-plan succeeded - #313 may be fixed; update this script"; }
# Flatten choudoufu's wrapped "In module.X, ... RESOURCE.NAME:" context
# lines to one line per diagnostic clause, same discipline as the RDS and
# security-group crossings, so a substring match is not at the mercy of
# where the wrap happened to land.
PLAN_FLAT="$(awk 'BEGIN{RS=""} {gsub(/\n/," "); print; print "@@CLAUSE@@"}' <<< "$PLAN_OUT")"

# #305 and #308 are BOTH fixed: no diagnostic may read "Resource type is
# outside the live-markers subset" (#305) or "This module call cannot be
# expanded under live resource markers" (#308) any more, for any type or
# module call.
WANT_UNADMITTED_N=0
WANT_CHILDMOD_N=0
# #313's root cause A and #315's root cause C are BOTH fixed now too, so
# "Dynamic value in static context" contributes nothing - see header. Only
# #313's own remaining scope (root cause B, a module output wrapping a
# Computed managed-resource attribute) and its cascade remain.
WANT_DYNAMIC_N=0
WANT_MODULE_OUTPUT_N=1
WANT_STATIC_CASCADE_N=1
WANT_UNRESOLVED_IDENTITY_N=6
if [ "${BREAK:-}" = "1" ]; then
  WANT_UNADMITTED_N=1
  WANT_CHILDMOD_N=1
  WANT_DYNAMIC_N=1
  WANT_MODULE_OUTPUT_N=2
  WANT_STATIC_CASCADE_N=2
  WANT_UNRESOLVED_IDENTITY_N=7
  log "  BREAK=1: expecting 1 unadmitted-type site and 1 child-module"
  log "           for_each site (#305 and #308 are both fixed; real is 0 for"
  log "           both), plus 1 Dynamic-value-in-static-context site (#313's"
  log "           root cause A and #315's root cause C are both fixed; real"
  log "           is 0), plus one extra site each for the Module-output (2"
  log "           vs real 1), static-value-cascade (2 vs real 1) and"
  log "           Unresolvable-identity (7 vs real 6) counts. All of these"
  log "           are wrong. This step must fail."
fi

UNADMITTED_N="$(grep -c '^Error: Resource type is outside the live-markers subset$' <<< "$PLAN_OUT")"
[ "$UNADMITTED_N" = "$WANT_UNADMITTED_N" ] || { grep -E '^Error:' <<< "$PLAN_OUT" | sort | uniq -c; fail "expected $WANT_UNADMITTED_N unadmitted-type sites (#305 is fixed), got $UNADMITTED_N"; }
log "  #305 confirmed fixed: 0 unadmitted-type sites - the default-object"
log "  adopters resolved and stamped back in stage 2 contribute no refusal"
log "  here."

CHILDMOD_N="$(grep -c '^Error: This module call cannot be expanded under live resource markers$' <<< "$PLAN_OUT")"
[ "$CHILDMOD_N" = "$WANT_CHILDMOD_N" ] || { grep -E '^Error:' <<< "$PLAN_OUT" | sort | uniq -c; fail "expected $WANT_CHILDMOD_N child-module for_each sites (#308 is fixed), got $CHILDMOD_N"; }
grep -qF 'In module.ecs_service, module "container_definition": for_each on a module' <<< "$PLAN_FLAT" \
  && { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:|^In module'; fail "#308's own diagnostic (module.ecs_service's container_definition for_each) still appears - it should be fixed"; }
log "  #308 confirmed fixed: 0 child-module for_each sites (was 1) -"
log "  module.ecs_service's container_definition module call now expands."
log "  Fixing it did not unblock this estate: it let live-plan walk further"
log "  in and reach #313's own wall instead (below)."

# #313's root cause A (data.aws_availability_zones) and #315's root cause C
# (each.value) are both fixed - asserted by ABSENCE, the load-bearing half:
# not one diagnostic may name either any more. #313's root cause B (the
# module output wrapping a Computed resource attribute) is #313's own
# acknowledged remaining scope and still correctly refuses.
DYNAMIC_N="$(grep -c '^Error: Dynamic value in static context$' <<< "$PLAN_OUT")"
MODULE_OUTPUT_N="$(grep -c '^Error: Module output not supported in static context$' <<< "$PLAN_OUT")"
STATIC_CASCADE_N="$(grep -c '^Error: Unable to compute static value$' <<< "$PLAN_OUT")"
UNRESOLVED_IDENTITY_N="$(grep -c '^Error: Unresolvable identity$' <<< "$PLAN_OUT")"
[ "$DYNAMIC_N" = "$WANT_DYNAMIC_N" ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:' | sort | uniq -c; fail "expected $WANT_DYNAMIC_N 'Dynamic value in static context' sites (#313's root cause A and #315's root cause C are both fixed), got $DYNAMIC_N"; }
[ "$MODULE_OUTPUT_N" = "$WANT_MODULE_OUTPUT_N" ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:' | sort | uniq -c; fail "expected $WANT_MODULE_OUTPUT_N 'Module output not supported in static context' site (#313's own remaining scope, root cause B), got $MODULE_OUTPUT_N"; }
[ "$STATIC_CASCADE_N" = "$WANT_STATIC_CASCADE_N" ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:' | sort | uniq -c; fail "expected $WANT_STATIC_CASCADE_N 'Unable to compute static value' cascade sites (B's cascade into aws_appautoscaling_target.this[0].resource_id), got $STATIC_CASCADE_N"; }
[ "$UNRESOLVED_IDENTITY_N" = "$WANT_UNRESOLVED_IDENTITY_N" ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:' | sort | uniq -c; fail "expected $WANT_UNRESOLVED_IDENTITY_N 'Unresolvable identity' cascade sites (B's cascade into both aws_appautoscaling_policy instances), got $UNRESOLVED_IDENTITY_N"; }
grep -qF 'Unable to use data.aws_availability_zones.available in static context' <<< "$PLAN_OUT" \
  && { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:|^In module'; fail "#313's root cause A (data.aws_availability_zones) still appears - it should be fixed"; }
grep -qF 'Unable to use each.value in static context, which is required by' <<< "$PLAN_OUT" \
  && { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:|^In module'; fail "#315's root cause C (each.value) still appears - it should be fixed"; }
grep -qF 'Unable to use module.ecs_cluster.arn in static context' <<< "$PLAN_OUT" \
  || fail "expected the module.ecs_cluster.arn root cause (B, #313's own acknowledged remaining scope) among the diagnostics - the corpus pin may have moved, or B may have been fixed too"
grep -qF 'module.ecs_service.aws_appautoscaling_target.this[0].resource_id' <<< "$PLAN_OUT" \
  || fail "expected B's cascade into aws_appautoscaling_target.this[0].resource_id - the corpus pin may have moved"

log "  #313's root cause A (data.aws_availability_zones) and #315's root"
log "  cause C (each.value) both confirmed fixed: 0 Dynamic-value-in-"
log "  static-context sites (was 52)."
log "  #313's own remaining scope (root cause B, module.ecs_cluster.arn -"
log "  a Computed resource attribute reached through a module output)"
log "  still correctly refuses: $MODULE_OUTPUT_N Module-output site,"
log "  cascading to $STATIC_CASCADE_N Unable-to-compute-static-value site"
log "  (aws_appautoscaling_target.this[0]'s own resource_id) and"
log "  $UNRESOLVED_IDENTITY_N Unresolvable-identity sites (both"
log "  aws_appautoscaling_policy instances' resource_id/scalable_dimension/"
log "  service_namespace arguments, which depend on the target's own"
log "  unresolved identity)."

log ""
log "STAGE 3 (test_plan): BLOCKED for real - #313's own remaining scope"
log "(root cause B) plus its 7-site cascade, 8 diagnostics total (was 236)."
log "#305, #308, #313's root cause A and #315 are all fixed and confirmed"
log "absent above by direct assertion, not by omission."
log ""
gauntlet_stage test_plan fail "BLOCKED - #313's own remaining scope alone (root cause B, see header), 8 diagnostics (was 236); #305, #308, #313's root cause A and #315 all fixed"
log "=== 4. test apply: NOT RUN - depends on stage 3, which does not produce a clean plan ==="
gauntlet_stage test_apply not_run "depends on stage 3, which does not produce a clean plan"
log "=== 5. drift and reconverge: NOT RUN - depends on stages 3-4 ==="
gauntlet_stage drift_reconverge not_run "depends on stages 3-4"
CURRENT_STAGE=""
gauntlet_end

log ""
log "=== SUMMARY (partial pass, reported honestly) ==="
log ""
log "  stage 1  cold_deploy        PASS"
log "  stage 2  migrate            PASS (real: $ELIGIBLE of $INSTANCES stamped, see header)"
log "  stage 3  test_plan          BLOCKED - #313's own remaining scope alone (root cause B, see header), 8 diagnostics (was 236); #305, #308, #313's root cause A and #315 all fixed"
log "  stage 4  test_apply         NOT RUN"
log "  stage 5  drift_reconverge   NOT RUN"
log ""
log "62 real resources, real emulator, real unmarked infrastructure, real"
log "migration. Every assertion above reads live-import's or live-plan's own"
log "output, or a tag read straight through the AWS CLI - never choudoufu's"
log "own self-report. Two real floci gaps found and filed along the way"
log "(lex00/floci#59, #60) do not block this script: live-import tolerates"
log "drift by design, and stage 3 refuses on choudoufu-side gaps before ever"
log "reaching a diff that would show them. Run again with BREAK=1: stages 1"
log "and 2 still pass and stage 3's site-count assertions are the ones that"
log "fail."
