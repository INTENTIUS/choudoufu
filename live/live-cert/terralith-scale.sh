#!/usr/bin/env bash
set -uo pipefail

# live/live-cert/terralith-scale.sh: the live-AWS certification harness for
# issue #567 (child E of epic #546) - the one question floci cannot answer:
# does the O(types) discovery advantage survive contact with a service that
# throttles? floci never returns TooManyRequests; this script runs the SAME
# terralith #564's tools/terralith-gen builds (proven apply/destroy against
# floci at -scale 1 and -scale 4 by #564 and #566) against real AWS instead,
# and instruments for pagination depth, streamed volume, and observed
# throttling/backoff on top of the same four gauntlet stages
# live/live-cert/reference-ec2-vpc.sh already proves (cold_deploy, migrate,
# test_plan, test_apply).
#
# It is NOT reference-ec2-vpc.sh with a bigger config: the composition is
# categorically different - IAM (account-global, not region-scoped) and
# Route 53 (a single hosted zone with a real, documented account-wide
# ChangeResourceRecordSets rate limit) dominate this estate instead of a
# handful of EC2 objects, so this script:
#
#   1. Names every resource with a per-run PREFIX (embeds RUN_ID) rather
#      than relying on tags alone for scoping - about half this estate's
#      resource types (aws_iam_role_policy, aws_iam_role_policy_attachment,
#      aws_route53_record) have NO tags argument in the provider schema at
#      all (confirmed: live/e2e/terralith-scale/MIGRATION.md's ratification
#      table, 29/55 UNTAGGABLE at scale 1), so a tag-only scope would miss
#      them entirely. Every enumeration and delete below is scoped by name
#      prefix, by the tofu-cert-run tag (for the ~half that support one, via
#      a provider-level default_tags block - see provider_block), or by
#      cascading from an already-scoped parent (a role's own inline
#      policies/attachments, a zone's own records, a cluster's own
#      services) - never an unscoped list-everything-and-delete.
#   2. Fixes two things terralith-gen's output gets right for floci but
#      wrong for a real, non-us-east-1 region: skip_requesting_account_id
#      (root cause of #572, the ECS identity-resolution defect
#      live/e2e/terralith-scale/MIGRATION.md found - the fix IS to not set
#      it for a real account) and a hardcoded "us-east-1a" availability
#      zone (network.tf) that plain does not exist in us-east-2. Both are
#      corrected in generate_estate below, not in tools/terralith-gen itself
#      - this script's provider/AZ handling is self-owned the same way
#      reference-ec2-vpc.sh's resource_block/provider_block are.
#   3. Verifies teardown the same double-path way reference-ec2-vpc.sh does
#      - choudoufu's own destroy path best-effort, the untouched stock state
#      in COLD_DIR as the trusted primary path, then an independent listing
#      (verify_empty below), then a raw-CLI sweep (sweep below) if anything
#      survives - extended for every resource kind this estate creates
#      (IAM role/policy/instance-profile, ECS cluster/service/task-def,
#      Route 53 zone/record, plus the VPC/subnet/SG reference-ec2-vpc.sh
#      already covers).
#
# Env (beyond what reference-ec2-vpc.sh reads - see that file's own doc
# comment for TARGET/REGION/RUN_ID/TOFU_BIN/TF_COLD_BIN/FLOCI_PORT/
# FLOCI_IMAGE/LIVECERT_WORK_DIR/LIVECERT_I_UNDERSTAND_THIS_SPENDS_REAL_MONEY):
#   SCALE           terralith-gen's own -scale (default 1, the smallest
#                    tier - #546's own rule: prove teardown at each tier
#                    before growing).
#   THROTTLE_LOG     1 (default) captures TF_LOG=DEBUG for cold_deploy's
#                    apply and migrate's -approve (both bounded, single-pass
#                    operations) to a file under WORK, so this run can grep
#                    it afterward for retry/throttle/pagination evidence
#                    without holding the log in memory. 0 disables it.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
LIB="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib"
# shellcheck source=live/e2e/lib/gauntlet.sh
source "$ROOT/live/e2e/lib/gauntlet.sh"
# shellcheck source=live/live-cert/lib/live-cert.sh
source "$LIB/live-cert.sh"

TARGET="${TARGET:-floci}"
REGION="${REGION:-us-east-1}"
SCALE="${SCALE:-1}"
RUN_ID="${RUN_ID:-lc$(date +%s)-$$}"
PREFIX="${PREFIX:-lc$(date +%s)$$}"
ESTATE="tl-livecert-$PREFIX"
WORK="${LIVECERT_WORK_DIR:-$(mktemp -d)}"
mkdir -p "$WORK"
FLOCI_PORT="${FLOCI_PORT:-4817}"
FLOCI_NAME="choudoufu-livecert-terralith-scale-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
THROTTLE_LOG="${THROTTLE_LOG:-1}"
COLD_DIR="$WORK/cold"
ADOPTED_DIR="$WORK/adopted"

# Both formulas track tools/terralith-gen's composition and MUST be updated
# with it. They were 50*SCALE+5 / 21*SCALE+5 when this script was written
# against the pre-#574 generator; issue #574 then added the count-expanded
# (6 resources * countTeamsPerScale=2 per scale = 12*SCALE, of which 3 per
# team-equivalent are taggable = 6*SCALE) and module-nested (6 resources *
# len(modulePodKeys)=2 * podSizePerScale=1 per scale = 12*SCALE, likewise
# 6*SCALE taggable) identity buckets, so both went up by 24 and 12 per
# scale respectively. EXPECTED matches live/e2e/terralith-scale/run.sh's
# own post-#574 formula (74*SCALE+5) by construction; keep them equal.
EXPECTED=$((74 * SCALE + 5))    # total resources
VERIFIED=$((33 * SCALE + 5))    # taggable (VERIFIED/DRIFTED-eligible) resources - 18 named-team + 1 service-exec-role + 6 count-expanded + 6 module-nested + 2 container per scale, plus a fixed 5 (zone, VPC, subnet, SG, cluster)

log() { printf '%s\n' "$*"; }

case "$TARGET" in
  floci) ENDPOINT="http://127.0.0.1:${FLOCI_PORT}" ;;
  aws)
    ENDPOINT=""
    if [ "${LIVECERT_I_UNDERSTAND_THIS_SPENDS_REAL_MONEY:-}" != "yes" ]; then
      echo "refusing: TARGET=aws needs LIVECERT_I_UNDERSTAND_THIS_SPENDS_REAL_MONEY=yes - nothing has been created" >&2
      exit 2
    fi
    ;;
  *) echo "TARGET must be floci or aws, got $TARGET" >&2; exit 2 ;;
esac

# ── teardown ────────────────────────────────────────────────────────────
TEARDOWN_DONE=0
MIGRATE_DONE=0
teardown() {
  [ "$TEARDOWN_DONE" = "1" ] && return 0
  TEARDOWN_DONE=1
  log "=== TEARDOWN (target=$TARGET run=$RUN_ID prefix=$PREFIX scale=$SCALE) ==="

  if [ "$MIGRATE_DONE" = "1" ] && [ -d "$ADOPTED_DIR" ]; then
    log "  attempting choudoufu's own destroy path ($ADOPTED_DIR): best effort, NOT the trusted path - see below"
    {
      cat <<EOF
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.59.0"
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
    } > "$ADOPTED_DIR/versions.tf"
    ( cd "$ADOPTED_DIR" && "${TOFU:-}" apply -input=false -auto-approve -no-color ) \
      > "$WORK/teardown_choudoufu_destroy.out" 2>&1
    cd_rc=$?
    log "    exit=$cd_rc (see $WORK/teardown_choudoufu_destroy.out) - not trusted alone"
    [ "$cd_rc" -ne 0 ] && tail -15 "$WORK/teardown_choudoufu_destroy.out" | sed 's/^/    | /'
  fi

  if [ -d "$COLD_DIR" ] && [ -f "$COLD_DIR/terraform.tfstate" ]; then
    log "  destroying the plain stock state ($COLD_DIR) - the primary, trusted path: valid the instant cold_deploy finishes, untouched by anything migrate/test_plan/test_apply do afterward"
    ( cd "$COLD_DIR" && AWS_ENDPOINT_URL="$ENDPOINT" "${TF_COLD:-terraform}" destroy -input=false -auto-approve -no-color -parallelism=5 ) \
      > "$WORK/teardown_stock_destroy.out" 2>&1
    sd_rc=$?
    log "    exit=$sd_rc (see $WORK/teardown_stock_destroy.out) - not trusted alone, verifying by listing next"
    [ "$sd_rc" -ne 0 ] && tail -30 "$WORK/teardown_stock_destroy.out" | sed 's/^/    | /'
  fi

  if verify_empty; then
    log "  VERIFIED EMPTY by listing: nothing matching prefix=$PREFIX or tag tofu-cert-run=$RUN_ID remains"
  else
    log "  destroy path(s) left resources behind - running the raw-CLI sweep as the belt-and-suspenders fallback"
    sweep
    if verify_empty; then
      log "  VERIFIED EMPTY by listing after the sweep"
    else
      log "  STILL NOT EMPTY after destroy and sweep - see the listing above; PREFIX=$PREFIX RUN_ID=$RUN_ID, retry teardown with the SAME values"
    fi
  fi

  if [ "$TARGET" = "floci" ]; then
    docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  fi

  # Every number this run needs (stage verdicts, timings, throttle/retry/
  # pagination counts) is already on stdout above, via gauntlet_stage and
  # the THROTTLE SUMMARY block - WORK (including the TF_LOG=DEBUG captures)
  # is scratch, not evidence, and LIVECERT_KEEP_WORK=1 opts out for a
  # human who wants to inspect a debug log by hand after a run.
  if [ "${LIVECERT_KEEP_WORK:-0}" != "1" ]; then
    rm -rf "$WORK"
  else
    log "  LIVECERT_KEEP_WORK=1: leaving $WORK in place"
  fi
}

CURRENT_STAGE=""
fail() {
  printf 'FAIL: %s\n' "$*" >&2
  [ -n "$CURRENT_STAGE" ] && gauntlet_stage "$CURRENT_STAGE" fail "$*"
  exit 1
}

APPLY_PID=""
on_signal() {
  local sig="$1"
  log "=== caught $sig - forwarding to in-flight child (pid ${APPLY_PID:-none}) and tearing down ==="
  if [ -n "$APPLY_PID" ] && kill -0 "$APPLY_PID" 2>/dev/null; then
    kill -TERM "$APPLY_PID" 2>/dev/null || true
    wait "$APPLY_PID" 2>/dev/null || true
  fi
  teardown
  trap - EXIT INT TERM
  exit 130
}
trap 'on_signal INT' INT
trap 'on_signal TERM' TERM
trap teardown EXIT
gauntlet_begin

# ══════════════════════════════════════════════════════════════════════
# helpers: provider_block, generate_estate, verify_empty, sweep
# ══════════════════════════════════════════════════════════════════════

provider_block() {
  if [ "$TARGET" = "floci" ]; then
    cat <<EOF
provider "aws" {
  region                      = "$REGION"
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true
  default_tags {
    tags = {
      tofu-cert-run = "$RUN_ID"
    }
  }
}
EOF
  else
    cat <<EOF
provider "aws" {
  region = "$REGION"
  default_tags {
    tags = {
      tofu-cert-run = "$RUN_ID"
    }
  }
}
EOF
  fi
}

# analyze_debug_log reads a TF_LOG=DEBUG capture and prints four
# space-separated numbers: byte size, throttling-error line count, GENUINE
# retry line count, and pagination-continuation line count. Prints to
# stdout only - the caller assigns via `read -r a b c d <<< "$(...)"`.
#
# The retry pattern is deliberately narrower than a first draft
# (`retryable error`) that this script shipped and then caught against its
# own floci proving run (2026-08-29/30): every "retry" hit at floci was
# actually the substring "retry" inside "unretryable error" - the AWS
# provider's own log line for a permanently-failed call (a 404 for a type
# floci does not emulate, or floci's own dummy-credential STS/IAM 404s),
# the OPPOSITE of a retried call. `grep -v unretryable` removes exactly
# that false-positive class while still counting whatever a genuine retry
# line says (this codebase's own retry logging was never observed to fire
# against floci - floci does not throttle, which is this issue's whole
# premise - so this pattern is written from AWS's/the SDK's own vocabulary
# for a retried call, not reverse-engineered from a sample that doesn't
# exist yet).
analyze_debug_log() {
  local f="$1"
  local bytes throttle retry pagination
  bytes="$(wc -c < "$f" | tr -d ' ')"
  throttle="$(grep -cE 'ThrottlingException|Throttling:|TooManyRequestsException|Rate exceeded|PriorRequestNotComplete|RequestLimitExceeded' "$f" 2>/dev/null || true)"
  retry="$(grep -iE 'retry|retrying|backoff|backing off' "$f" 2>/dev/null | grep -vi 'unretryable' | wc -l | tr -d ' ')"
  pagination="$(grep -icE 'NextToken|Marker=|IsTruncated=true|nextMarker' "$f" 2>/dev/null || true)"
  printf '%s %s %s %s\n' "${bytes:-0}" "${throttle:-0}" "${retry:-0}" "${pagination:-0}"
}

# generate_estate writes a fresh terralith-gen output into $1, then corrects
# the two things it gets right for floci/us-east-1 but wrong for a real,
# non-us-east-1 account (see this file's own doc comment, point 2): the
# provider block (skip_requesting_account_id must be false against real AWS
# - #572) and network.tf's hardcoded "us-east-1a" availability zone (must be
# a real AZ in $REGION).
generate_estate() {
  local dir="$1"
  ( cd "$ROOT" && env -u PWD go run ./tools/terralith-gen -scale "$SCALE" -prefix "$PREFIX" -out "$dir" -fmt-bin "" ) \
    > "$WORK/terralith_gen.out" 2>&1 || { cat "$WORK/terralith_gen.out"; fail "terralith-gen failed"; }
  cat "$WORK/terralith_gen.out"

  # Replace the generated provider block wholesale with provider_block's
  # target-appropriate one; keep the generated required_providers block
  # (the version pin belongs to terralith-gen, not this script).
  sed -n '1,/^provider "aws" {$/p' "$dir/versions.tf" | sed '$d' > "$dir/versions.tf.new"
  provider_block >> "$dir/versions.tf.new"
  mv "$dir/versions.tf.new" "$dir/versions.tf"

  # network.tf: "us-east-1a" -> "${REGION}a" - only a real issue for
  # TARGET=aws (floci does not validate AZs), but corrected unconditionally
  # so the SAME generated config is what both targets run, per #546's own
  # rule that stock and choudoufu numbers (and, here, floci and aws runs)
  # come from the same estate or are not reported as a comparison.
  sed -i.bak "s/us-east-1a/${REGION}a/" "$dir/network.tf" && rm -f "$dir/network.tf.bak"

  # iam.tf: terralith-gen's "scoped team" roles trust a SYNTHETIC 12-digit
  # account id (tools/terralith-gen/templates.go: crossAccountID,
  # 100000000000+i) as a cross-account Principal. That id is
  # syntactically valid but does not belong to any real AWS account, and
  # real IAM's trust-policy validation rejects a Principal naming an
  # account it cannot confirm exists - "MalformedPolicyDocument: Invalid
  # principal in policy" (issue #567's live-AWS run, 2026-08-30; floci
  # never validates this and accepted every synthetic id). This is a
  # real-AWS-specific validation rule, not a malformed-output bug the way
  # the TXT record double-quoting fix (tools/terralith-gen/gen.go,
  # writeRecords) was, so it is corrected HERE - the same "self-owned,
  # live-cert-specific" line this file already draws for the AZ and
  # skip_requesting_account_id fixes above - rather than in terralith-gen
  # itself: every synthetic id is replaced with the CALLER's own real
  # account id (definitely exists), which trades away the per-team
  # uniqueness terralith-gen's own duplication counters credit this trust
  # block with (GENERATED.md's duplication percentage, computed before
  # this patch runs, still describes the pre-patch generated content
  # accurately - it is just no longer what actually gets applied here).
  sed -i.bak -E "s/arn:aws:iam::1[0-9]{11}:root/arn:aws:iam::${CALLER_ACCOUNT_ID}:root/g" "$dir/iam.tf" && rm -f "$dir/iam.tf.bak"
}

# verify_empty lists, independently of any destroy command's exit code,
# whether anything this run created still exists - scoped by name PREFIX
# (every resource this estate creates, taggable or not) and cross-checked
# by the tofu-cert-run tag (the taggable half, via resourcegroupstaggingapi,
# informational the same way live-cert.sh's own livecert_verify_empty
# treats it - never the sole gate).
# checked_list runs a listing command and reports through the SAME channel
# whether it failed (dirty: fail-SAFE, never fail-open) or came back
# non-empty (dirty) or came back empty and clean (not dirty). This exists
# because the first draft of this function used `... 2>/dev/null || true`
# on every call, which cannot tell "confirmed empty" apart from "the AWS
# CLI call itself errored" (a dropped endpoint, a throttled list call) -
# discovered empirically building this script (2026-08-29/30): killing the
# floci container mid-teardown made every per-service check fail with
# "connection refused", `|| true` swallowed every one of them, and the
# function reported VERIFIED EMPTY on an account it had not actually been
# able to check at all. Never report empty on an error - HANDOFF's mirror
# rule for the spend side: leaving nothing live is checked, not assumed,
# and a check that cannot run is not a check that passed.
#
# Sets the caller's DIRTY=1 if the command failed (prints its stderr) or
# its filtered output was non-empty (prints the output); leaves DIRTY
# untouched otherwise. Args: <label> <command...>
checked_list() {
  local label="$1"; shift
  local out err rc
  err="$(mktemp)"
  out="$("$@" 2>"$err")"; rc=$?
  if [ "$rc" -ne 0 ]; then
    printf '  verify_empty: %s CHECK FAILED (exit %s) - treating as NOT verified empty: %s\n' "$label" "$rc" "$(tr '\n' ' ' < "$err")"
    DIRTY=1
  elif [ -n "$out" ]; then
    printf '  verify_empty: live %s: %s\n' "$label" "$out"
    DIRTY=1
  fi
  rm -f "$err"
}

verify_empty() {
  DIRTY=0

  local rgta_n
  rgta_n="$(livecert_aws resourcegroupstaggingapi get-resources \
    --tag-filters "Key=tofu-cert-run,Values=$RUN_ID" \
    --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo unknown)"
  if [ "$rgta_n" != "0" ]; then
    printf '  verify_empty: resourcegroupstaggingapi reports %s resource(s) tagged tofu-cert-run=%s (informational - per-service checks below gate the verdict)\n' "$rgta_n" "$RUN_ID"
  fi

  checked_list "IAM role(s)" livecert_aws iam list-roles --query "Roles[?starts_with(RoleName, '${PREFIX}-')].RoleName" --output text
  checked_list "IAM customer-managed policy(ies)" livecert_aws iam list-policies --scope Local --query "Policies[?starts_with(PolicyName, '${PREFIX}-')].PolicyName" --output text
  checked_list "IAM instance profile(s)" livecert_aws iam list-instance-profiles --query "InstanceProfiles[?starts_with(InstanceProfileName, '${PREFIX}-')].InstanceProfileName" --output text
  checked_list "Route53 zone(s)" livecert_aws route53 list-hosted-zones --query "HostedZones[?Name=='${PREFIX}.terralith.test.'].Id" --output text
  checked_list "subnet(s)" livecert_aws ec2 describe-subnets --filters "Name=tag:Name,Values=${PREFIX}-subnet" --query 'Subnets[].SubnetId' --output text
  checked_list "security group(s)" livecert_aws ec2 describe-security-groups --filters "Name=group-name,Values=${PREFIX}-ecs-sg" --query 'SecurityGroups[].GroupId' --output text
  checked_list "vpc(s)" livecert_aws ec2 describe-vpcs --filters "Name=tag:Name,Values=${PREFIX}-vpc" --query 'Vpcs[].VpcId' --output text

  # ECS cluster/task-definition listing needs client-side filtering
  # (list-clusters has no name filter; list-task-definitions' own
  # --family-prefix IS server-side, used directly) - wrapped in a function
  # so checked_list's rc/output contract still applies to the pipeline as a
  # whole.
  ecs_clusters_for_prefix() {
    local all rc
    all="$(livecert_aws ecs list-clusters --query 'clusterArns' --output text)"; rc=$?
    [ "$rc" -eq 0 ] || return "$rc"
    # grep's own "no match" exit (1) is a valid empty result, not a
    # failure - explicit `return 0` after it means THIS function's exit
    # status only ever reflects list-clusters' own rc, never grep's.
    printf '%s\n' "$all" | tr '\t' '\n' | grep -F "/${PREFIX}-cluster"
    return 0
  }
  checked_list "ECS cluster(s)" ecs_clusters_for_prefix
  checked_list "ACTIVE ECS task definition(s) (deregistering these is not required for emptiness - they are free and AWS retains INACTIVE families - but ACTIVE ones would mean the estate config was never removed)" \
    livecert_aws ecs list-task-definitions --family-prefix "${PREFIX}-svc-" --status ACTIVE --query 'taskDefinitionArns' --output text

  [ "$DIRTY" = "0" ]
}

# sweep force-deletes everything this run's PREFIX or tofu-cert-run tag
# names, with NO tofu/terraform involved. Order matches AWS's own
# dependency requirements: detach/delete a role's policies before the role,
# detach a policy from every entity before deleting the policy, empty a
# zone of its own (non-NS/SOA) records before deleting the zone, remove ECS
# services before the cluster, subnet/vpc last (mirrors live-cert.sh's own
# livecert_sweep for the network layer this estate shares with
# reference-ec2-vpc). Every step is best-effort (`|| true`).
sweep() {
  printf '  sweep: force-deleting everything named %s-* or tagged tofu-cert-run=%s\n' "$PREFIX" "$RUN_ID"

  local roles
  roles="$(livecert_aws iam list-roles --query "Roles[?starts_with(RoleName, '${PREFIX}-')].RoleName" --output text 2>/dev/null || true)"
  for role in $roles; do
    printf '    role %s: detaching managed policies\n' "$role"
    for arn in $(livecert_aws iam list-attached-role-policies --role-name "$role" --query 'AttachedPolicies[].PolicyArn' --output text 2>/dev/null || true); do
      livecert_aws iam detach-role-policy --role-name "$role" --policy-arn "$arn" >/dev/null 2>&1 || true
    done
    printf '    role %s: deleting inline policies\n' "$role"
    for pname in $(livecert_aws iam list-role-policies --role-name "$role" --query 'PolicyNames' --output text 2>/dev/null || true); do
      livecert_aws iam delete-role-policy --role-name "$role" --policy-name "$pname" >/dev/null 2>&1 || true
    done
    printf '    role %s: removing from instance profiles\n' "$role"
    for prof in $(livecert_aws iam list-instance-profiles-for-role --role-name "$role" --query 'InstanceProfiles[].InstanceProfileName' --output text 2>/dev/null || true); do
      livecert_aws iam remove-role-from-instance-profile --instance-profile-name "$prof" --role-name "$role" >/dev/null 2>&1 || true
    done
    printf '    deleting role %s\n' "$role"
    livecert_aws iam delete-role --role-name "$role" >/dev/null 2>&1 || true
  done

  local profiles
  profiles="$(livecert_aws iam list-instance-profiles --query "InstanceProfiles[?starts_with(InstanceProfileName, '${PREFIX}-')].InstanceProfileName" --output text 2>/dev/null || true)"
  for prof in $profiles; do
    printf '    deleting instance profile %s\n' "$prof"
    livecert_aws iam delete-instance-profile --instance-profile-name "$prof" >/dev/null 2>&1 || true
  done

  local policies
  policies="$(livecert_aws iam list-policies --scope Local --query "Policies[?starts_with(PolicyName, '${PREFIX}-')].Arn" --output text 2>/dev/null || true)"
  for parn in $policies; do
    for role in $(livecert_aws iam list-entities-for-policy --policy-arn "$parn" --entity-filter Role --query 'PolicyRoles[].RoleName' --output text 2>/dev/null || true); do
      livecert_aws iam detach-role-policy --role-name "$role" --policy-arn "$parn" >/dev/null 2>&1 || true
    done
    printf '    deleting policy %s\n' "$parn"
    livecert_aws iam delete-policy --policy-arn "$parn" >/dev/null 2>&1 || true
  done

  local clusterarn
  clusterarn="$(livecert_aws ecs list-clusters --query 'clusterArns' --output text 2>/dev/null | tr '\t' '\n' | grep -F "/${PREFIX}-cluster" || true)"
  if [ -n "$clusterarn" ]; then
    for svcarn in $(livecert_aws ecs list-services --cluster "$clusterarn" --query 'serviceArns' --output text 2>/dev/null | tr '\t' '\n' || true); do
      printf '    deleting ECS service %s\n' "$svcarn"
      livecert_aws ecs update-service --cluster "$clusterarn" --service "$svcarn" --desired-count 0 >/dev/null 2>&1 || true
      livecert_aws ecs delete-service --cluster "$clusterarn" --service "$svcarn" --force >/dev/null 2>&1 || true
    done
    printf '    deleting ECS cluster %s\n' "$clusterarn"
    livecert_aws ecs delete-cluster --cluster "$clusterarn" >/dev/null 2>&1 || true
  fi

  local zoneid
  zoneid="$(livecert_aws route53 list-hosted-zones --query "HostedZones[?Name=='${PREFIX}.terralith.test.'].Id" --output text 2>/dev/null || true)"
  if [ -n "$zoneid" ]; then
    printf '    emptying and deleting Route53 zone %s\n' "$zoneid"
    local recs
    recs="$(livecert_aws route53 list-resource-record-sets --hosted-zone-id "$zoneid" \
      --query "ResourceRecordSets[?Type!='NS' && Type!='SOA']" --output json 2>/dev/null || echo '[]')"
    if [ "$recs" != "[]" ] && [ -n "$recs" ] && command -v jq >/dev/null 2>&1; then
      jq '{Changes: [.[] | {Action: "DELETE", ResourceRecordSet: .}]}' <<< "$recs" \
        > "$WORK/sweep_record_delete_batch.json" 2>/dev/null || true
      if [ -s "$WORK/sweep_record_delete_batch.json" ]; then
        livecert_aws route53 change-resource-record-sets --hosted-zone-id "$zoneid" \
          --change-batch "file://$WORK/sweep_record_delete_batch.json" >/dev/null 2>&1 || true
      fi
    fi
    livecert_aws route53 delete-hosted-zone --id "$zoneid" >/dev/null 2>&1 || true
  fi

  local subnets
  subnets="$(livecert_aws ec2 describe-subnets --filters "Name=tag:Name,Values=${PREFIX}-subnet" --query 'Subnets[].SubnetId' --output text 2>/dev/null || true)"
  for sn in $subnets; do
    printf '    deleting subnet %s\n' "$sn"
    livecert_aws ec2 delete-subnet --subnet-id "$sn" >/dev/null 2>&1 || true
  done

  local sgs
  sgs="$(livecert_aws ec2 describe-security-groups --filters "Name=group-name,Values=${PREFIX}-ecs-sg" --query 'SecurityGroups[].GroupId' --output text 2>/dev/null || true)"
  for sg in $sgs; do
    printf '    deleting security group %s\n' "$sg"
    livecert_aws ec2 delete-security-group --group-id "$sg" >/dev/null 2>&1 || true
  done

  local vpcs
  vpcs="$(livecert_aws ec2 describe-vpcs --filters "Name=tag:Name,Values=${PREFIX}-vpc" --query 'Vpcs[].VpcId' --output text 2>/dev/null || true)"
  for vpc in $vpcs; do
    printf '    deleting vpc %s\n' "$vpc"
    livecert_aws ec2 delete-vpc --vpc-id "$vpc" >/dev/null 2>&1 || true
  done
}

# ── 0. tools ────────────────────────────────────────────────────────────
log "=== 0. tools (target=$TARGET run_id=$RUN_ID prefix=$PREFIX scale=$SCALE) ==="
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v "${TF_COLD_BIN:-terraform}" >/dev/null 2>&1 || fail "${TF_COLD_BIN:-terraform} is not on PATH (needed for cold_deploy's stock apply)"
TF_COLD="${TF_COLD_BIN:-terraform}"

if [ -n "${TOFU_BIN:-}" ]; then
  TOFU="$TOFU_BIN"
  [ -x "$TOFU" ] || fail "TOFU_BIN=$TOFU_BIN is not an executable file"
  log "  using TOFU_BIN=$TOFU"
else
  command -v docker >/dev/null 2>&1 || fail "docker is not on PATH (needed to build choudoufu)"
  mkdir -p "$WORK/bin"
  TOFU="$WORK/bin/choudoufu"
  ( cd "$ROOT" && env -u PWD go build -o "$TOFU" ./cmd/choudoufu ) || fail "go build ./cmd/choudoufu failed"
  log "  built $TOFU"
fi

if [ "$TARGET" = "floci" ]; then
  command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
  docker info >/dev/null 2>&1 || fail "docker is not running"
fi

# ── 0b. the endpoint ────────────────────────────────────────────────────
if [ "$TARGET" = "floci" ]; then
  log "=== 0b. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
  docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
    || fail "docker run for $FLOCI_NAME failed"
  healthy=0
  for _ in $(seq 1 45); do
    H="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
    case "$H" in *'"ec2":"running"'*) healthy=1; break ;; esac
    sleep 2
  done
  [ "$healthy" = "1" ] || fail "floci did not come up healthy (ec2) at $ENDPOINT"
  log "  healthy"
  export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" AWS_ENDPOINT_URL="$ENDPOINT"
  CALLER_ACCOUNT_ID="$(livecert_aws sts get-caller-identity --query Account --output text 2>/dev/null || echo 000000000000)"
else
  log "=== 0b. target=aws, region=$REGION - using the ambient AWS credential chain, no endpoint override ==="
  unset AWS_ENDPOINT_URL || true
  export AWS_REGION="$REGION"
  IDENTITY="$(aws sts get-caller-identity --query Account --output text 2>&1)" \
    || fail "aws sts get-caller-identity failed - no usable credentials for a real run: $IDENTITY"
  log "  caller account ...${IDENTITY: -4} (only the last 4 digits are ever logged or recorded)"
  CALLER_ACCOUNT_ID="$IDENTITY"
fi

# ══════════════════════════════════════════════════════════════════════
# cold_deploy: stock applies the unmodified (AZ/provider-corrected)
# generator output for real.
# ══════════════════════════════════════════════════════════════════════
CURRENT_STAGE=cold_deploy
log "=== 1. terralith-gen -scale $SCALE -prefix $PREFIX -> $COLD_DIR ==="
generate_estate "$COLD_DIR"
log "  expect ${EXPECTED} resources (${VERIFIED} taggable/eligible)"

log "=== 2. cold_deploy: $TF_COLD init ==="
( cd "$COLD_DIR" && "$TF_COLD" init -input=false -no-color ) > "$WORK/cold_deploy_init.out" 2>&1 \
  || { tail -20 "$WORK/cold_deploy_init.out"; fail "stock init failed"; }

log "=== 2b. cold_deploy: $TF_COLD apply (backgrounded so a signal can interrupt it) ==="
(
  cd "$COLD_DIR" || exit 1
  if [ "$THROTTLE_LOG" = "1" ]; then
    export TF_LOG=DEBUG
    export TF_LOG_PATH="$WORK/cold_deploy_apply.debug.log"
  fi
  exec "$TF_COLD" apply -input=false -auto-approve -no-color -parallelism=10
) > "$WORK/cold_deploy_apply.out" 2>&1 &
APPLY_PID=$!
COLD_APPLY_START=$(date +%s)
wait "$APPLY_PID"
APPLY_RC=$?
COLD_APPLY_END=$(date +%s)
APPLY_PID=""
[ "$APPLY_RC" -eq 0 ] || { tail -40 "$WORK/cold_deploy_apply.out"; fail "stock apply exited $APPLY_RC"; }
grep -qE "Apply complete! Resources: ${EXPECTED} added" "$WORK/cold_deploy_apply.out" \
  || { grep -E 'Apply complete' "$WORK/cold_deploy_apply.out"; fail "stock apply did not create exactly ${EXPECTED} resources"; }
[ -f "$COLD_DIR/terraform.tfstate" ] || fail "stock apply left no state file to migrate from"
COLD_APPLY_S=$((COLD_APPLY_END - COLD_APPLY_START))
log "  $(grep -E 'Apply complete' "$WORK/cold_deploy_apply.out") in ${COLD_APPLY_S}s"
COLD_LOG_BYTES=0 COLD_THROTTLE_HITS=0 COLD_RETRY_LINES=0 COLD_PAGINATION_HITS=0
if [ "$THROTTLE_LOG" = "1" ] && [ -f "$WORK/cold_deploy_apply.debug.log" ]; then
  read -r COLD_LOG_BYTES COLD_THROTTLE_HITS COLD_RETRY_LINES COLD_PAGINATION_HITS <<< "$(analyze_debug_log "$WORK/cold_deploy_apply.debug.log")"
  log "  cold_deploy debug log: ${COLD_LOG_BYTES} bytes, ${COLD_THROTTLE_HITS} throttling-error line(s), ${COLD_RETRY_LINES} genuine-retry line(s) - this is the parallelism=10, single-zone Route53 record fan-out, the most plausible place in this pipeline to see ChangeResourceRecordSets pushed back on"
fi
gauntlet_stage cold_deploy pass "${EXPECTED} resources from stock $TF_COLD against $TARGET at scale=$SCALE in ${COLD_APPLY_S}s, tofu-cert-run=$RUN_ID, debug log ${COLD_LOG_BYTES}B/${COLD_THROTTLE_HITS} throttle/${COLD_RETRY_LINES} retry"

# ══════════════════════════════════════════════════════════════════════
# migrate: choudoufu live-import -approve against the stock state file.
# ══════════════════════════════════════════════════════════════════════
CURRENT_STAGE=migrate
log "=== 3. migrate: generate the SAME estate into $ADOPTED_DIR (live block + record_store) ==="
generate_estate "$ADOPTED_DIR"
{
  cat <<EOF
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.59.0"
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
} > "$ADOPTED_DIR/versions.tf"

log "=== 3b. migrate: choudoufu init + live-import (dry run, then -approve) ==="
( cd "$ADOPTED_DIR" && "$TOFU" init -input=false -no-color ) > "$WORK/migrate_init.out" 2>&1 \
  || { tail -20 "$WORK/migrate_init.out"; fail "adopted init failed"; }

IMPORT_OUT="$(cd "$ADOPTED_DIR" && "$TOFU" live-import -state="$COLD_DIR/terraform.tfstate" -estate="$ESTATE" 2>&1)" || {
  printf '%s\n' "$IMPORT_OUT" | tail -60; fail "live-import (dry run) failed"; }
printf '%s\n' "$IMPORT_OUT" > "$WORK/migrate_dryrun.out"
grep -qF "${VERIFIED} of ${EXPECTED} resource instance(s) are eligible for stamping" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT" | grep -E 'eligible for stamping'; fail "live-import did not verify ${VERIFIED} of ${EXPECTED} resources as eligible - see $WORK/migrate_dryrun.out"; }

MIGRATE_APPROVE_ENV=()
if [ "$THROTTLE_LOG" = "1" ]; then
  MIGRATE_APPROVE_ENV=(TF_LOG=DEBUG "TF_LOG_PATH=$WORK/migrate_approve.debug.log")
fi
MIGRATE_START=$(date +%s)
# ${arr[@]+"${arr[@]}"} rather than a bare "${arr[@]}": under `set -u`,
# bash 3.2 - which is what /bin/bash still is on macOS, where this script
# is developed - treats expanding an EMPTY array as an unbound variable and
# aborts. That is only reachable with THROTTLE_LOG=0, which is why every
# run behind this issue (all at the default THROTTLE_LOG=1, so the array
# always held two entries) passed straight over it, and why it surfaced
# only when PR #577's merge verification ran the harness with the debug log
# turned off.
APPROVE_OUT="$(cd "$ADOPTED_DIR" && env ${MIGRATE_APPROVE_ENV[@]+"${MIGRATE_APPROVE_ENV[@]}"} "$TOFU" live-import -state="$COLD_DIR/terraform.tfstate" -estate="$ESTATE" -approve 2>&1)" || {
  printf '%s\n' "$APPROVE_OUT" | tail -60; fail "live-import -approve failed"; }
MIGRATE_END=$(date +%s)
printf '%s\n' "$APPROVE_OUT" > "$WORK/migrate_approve.out"
SKIPPED=$((EXPECTED - VERIFIED))  # UNTAGGABLE instances (no tags argument in the provider schema): reported as "skipped", not an error - live-import needs no action on these, their identity composes from an already-stamped parent
grep -qF "${VERIFIED} resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, ${SKIPPED} skipped" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT" | tail -30; fail "live-import -approve did not stamp exactly ${VERIFIED} resources cleanly (expected ${SKIPPED} skipped/untaggable) - see $WORK/migrate_approve.out"; }
MIGRATE_DONE=1
MIGRATE_S=$((MIGRATE_END - MIGRATE_START))
log "  ${VERIFIED} of ${EXPECTED} stamped in ${MIGRATE_S}s"
MIGRATE_LOG_BYTES=0 MIGRATE_THROTTLE_HITS=0 MIGRATE_RETRY_LINES=0
if [ "$THROTTLE_LOG" = "1" ] && [ -f "$WORK/migrate_approve.debug.log" ]; then
  read -r MIGRATE_LOG_BYTES MIGRATE_THROTTLE_HITS MIGRATE_RETRY_LINES _ <<< "$(analyze_debug_log "$WORK/migrate_approve.debug.log")"
  log "  migrate debug log: ${MIGRATE_LOG_BYTES} bytes, ${MIGRATE_THROTTLE_HITS} throttling-error line(s), ${MIGRATE_RETRY_LINES} genuine-retry line(s) - this is ${VERIFIED} sequential tag-write API calls (one per resource, not batched), the most plausible place to see a WRITE-side rate limit"
fi
gauntlet_stage migrate pass "${VERIFIED} of ${EXPECTED} verified, ${VERIFIED} stamped, ${SKIPPED} skipped, in ${MIGRATE_S}s, debug log ${MIGRATE_LOG_BYTES}B/${MIGRATE_THROTTLE_HITS} throttle/${MIGRATE_RETRY_LINES} retry"

# ══════════════════════════════════════════════════════════════════════
# test_plan: replan from nothing; identities checked against the AWS CLI;
# this is also where the throttling/pagination measurement runs, since it
# is choudoufu's full estate-wide sweep (#546's O(types) side).
# ══════════════════════════════════════════════════════════════════════
CURRENT_STAGE=test_plan
log "=== 4. test_plan: choudoufu plan must be empty (instrumented) ==="
PLAN_LOG="$WORK/test_plan.debug.log"
PLAN_START=$(date +%s)
if [ "$THROTTLE_LOG" = "1" ]; then
  PLAN_OUT="$(cd "$ADOPTED_DIR" && TF_LOG=DEBUG TF_LOG_PATH="$PLAN_LOG" "$TOFU" plan -input=false -no-color 2>&1)"; PLAN_RC=$?
else
  PLAN_OUT="$(cd "$ADOPTED_DIR" && "$TOFU" plan -input=false -no-color 2>&1)"; PLAN_RC=$?
fi
PLAN_END=$(date +%s)
PLAN_S=$((PLAN_END - PLAN_START))
printf '%s\n' "$PLAN_OUT" > "$WORK/test_plan.out"
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -40; fail "the post-migrate plan exited $PLAN_RC"; }
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$PLAN_OUT" \
  || { grep -E '^  #' <<< "$PLAN_OUT" | head -20; fail "the post-migrate plan is not empty - see $WORK/test_plan.out"; }
log "  plan empty in ${PLAN_S}s"

log "=== 4b. test_plan: throttling/pagination read from the debug log ==="
if [ "$THROTTLE_LOG" = "1" ] && [ -f "$PLAN_LOG" ]; then
  read -r PLAN_LOG_BYTES THROTTLE_HITS RETRY_LINES PAGINATION_HITS <<< "$(analyze_debug_log "$PLAN_LOG")"
  log "  test_plan debug log: ${PLAN_LOG_BYTES} bytes, ${THROTTLE_HITS} throttling-error line(s), ${RETRY_LINES} genuine-retry line(s), ${PAGINATION_HITS} pagination-continuation line(s)"
else
  PLAN_LOG_BYTES=0 THROTTLE_HITS=0 RETRY_LINES=0 PAGINATION_HITS=0
  log "  THROTTLE_LOG=$THROTTLE_LOG - not instrumented for this stage"
fi

log "=== 4c. test_plan: rendered identity checked against the AWS CLI directly (spot check: the zone and one team role) ==="
ZONEID="$(livecert_aws route53 list-hosted-zones --query "HostedZones[?Name=='${PREFIX}.terralith.test.'].Id" --output text)"
[ -n "$ZONEID" ] && [ "$ZONEID" != "None" ] || fail "no live zone found for prefix $PREFIX"
ZTAG="$(livecert_aws route53 list-tags-for-resource --resource-type hostedzone --resource-id "${ZONEID#/hostedzone/}" --query "ResourceTagSet.Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$ZTAG" = "aws_route53_zone.main" ] || fail "the zone carries tofu-address=$ZTAG, not aws_route53_zone.main - identity read via the AWS CLI, not choudoufu's own report"
ROLEARN="$(livecert_aws iam get-role --role-name "${PREFIX}-team-0000-role" --query 'Role.Arn' --output text)"
[ -n "$ROLEARN" ] && [ "$ROLEARN" != "None" ] || fail "no live role found for ${PREFIX}-team-0000-role"
RTAG="$(livecert_aws iam list-role-tags --role-name "${PREFIX}-team-0000-role" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$RTAG" = "aws_iam_role.team_0000_role" ] || fail "the role carries tofu-address=$RTAG, not aws_iam_role.team_0000_role"
log "  zone $ZONEID and role $ROLEARN: tofu-address confirmed via the AWS CLI directly"
gauntlet_stage test_plan pass "post-migrate plan is empty in ${PLAN_S}s; zone/role tofu-address confirmed via the AWS CLI; debug log ${PLAN_LOG_BYTES} bytes, ${THROTTLE_HITS} throttling-error line(s), ${RETRY_LINES} retry line(s)"

# ══════════════════════════════════════════════════════════════════════
# test_apply: applying the empty plan is a genuine no-op.
# ══════════════════════════════════════════════════════════════════════
CURRENT_STAGE=test_apply
log "=== 5. test_apply: the empty plan applies as a genuine no-op ==="
BEFORE_N="$(livecert_aws resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-cert-run,Values=$RUN_ID" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
NOOP_OUT="$(cd "$ADOPTED_DIR" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; NOOP_RC=$?
[ "$NOOP_RC" -eq 0 ] || { printf '%s\n' "$NOOP_OUT" | tail -30; fail "the no-op apply exited $NOOP_RC"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$NOOP_OUT" \
  || { grep -E 'Apply complete' <<< "$NOOP_OUT"; fail "the no-op apply was not a genuine no-op"; }
AFTER_N="$(livecert_aws resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-cert-run,Values=$RUN_ID" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$AFTER_N" = "$BEFORE_N" ] || fail "object count changed across a no-op apply: $BEFORE_N -> $AFTER_N"
log "  genuine no-op: $BEFORE_N objects before, $AFTER_N after"
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed); tofu-estate-tagged object count unchanged at $BEFORE_N"

CURRENT_STAGE=""
gauntlet_end
log "=== all four stages passed against target=$TARGET scale=$SCALE; teardown runs next via the EXIT trap ==="
log "=== THROTTLE SUMMARY (target=$TARGET scale=$SCALE) ==="
log "  cold_deploy apply (${EXPECTED} resources, parallelism=10, single Route53 zone): ${COLD_APPLY_S}s, debug log ${COLD_LOG_BYTES}B, ${COLD_THROTTLE_HITS} throttling-error line(s), ${COLD_RETRY_LINES} genuine-retry line(s)"
log "  migrate -approve (${VERIFIED} sequential tag-write API calls): ${MIGRATE_S}s, debug log ${MIGRATE_LOG_BYTES}B, ${MIGRATE_THROTTLE_HITS} throttling-error line(s), ${MIGRATE_RETRY_LINES} genuine-retry line(s)"
log "  test_plan (choudoufu's own O(types) full-account sweep): ${PLAN_S}s, debug log ${PLAN_LOG_BYTES}B, ${THROTTLE_HITS} throttling-error line(s), ${RETRY_LINES} genuine-retry line(s), ${PAGINATION_HITS} pagination-continuation line(s)"
