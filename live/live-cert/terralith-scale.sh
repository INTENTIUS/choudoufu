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
#   WALLCLOCK_TRACE  0 (default) or 1. 1 adds sections 2e, 4g and 4h: three
#                    DEBUG-instrumented stock plans before migrate and three
#                    after the steady state is reached on the choudoufu side,
#                    then live/live-cert/wallclock-gaps.py over all six. This
#                    is issue #867's measurement - the idle-gap trace of the
#                    read and sweep passes that #683 took by hand on a branch
#                    that no longer exists. Six extra plans, no extra objects
#                    created, and nothing gates on it.

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

# Which record_store backend the adopted estate declares. Until now this was
# hardcoded to "local", a directory on disk beside the module - which means
# every real-AWS run this harness has ever produced exercised the VALUES half
# of the state model against local disk, not against the cloud. Of the three
# pieces (identity as tags, values in a record store, effects as receipts),
# only identity was genuinely under test.
#
# "ssm" is the default for TARGET=aws because it needs nothing created first:
# it writes under a prefix derived from the estate name, and teardown is a
# prefix delete. "s3" needs a bucket the run would have to make and destroy.
# floci keeps "local", because the point there is speed and the emulator's
# Parameter Store is not what is under test.
if [ "$TARGET" = "aws" ]; then
  RECORD_STORE_BACKEND="${RECORD_STORE_BACKEND:-ssm}"
else
  RECORD_STORE_BACKEND="${RECORD_STORE_BACKEND:-local}"
fi
case "$RECORD_STORE_BACKEND" in
  local) RECORD_STORE_ARGS='      path = ".tofu-records"' ;;
  # key_prefix is a record KEY prefix, not an SSM parameter path, so it is
  # store-relative and must not start with "/" (issue #688 refuses that
  # shape loudly). The ssm backend renders it into the parameter name
  # "/choudoufu/livecert/$PREFIX/...", which is what SSM_PREFIX below
  # counts and tears down. Issue #916.
  ssm)   RECORD_STORE_ARGS="      key_prefix = \"choudoufu/livecert/$PREFIX\"
      region     = \"$REGION\"" ;;
  s3)    : "${RECORD_STORE_BUCKET:?RECORD_STORE_BACKEND=s3 needs RECORD_STORE_BUCKET}"
         RECORD_STORE_ARGS="      bucket     = \"$RECORD_STORE_BUCKET\"
      key_prefix = \"choudoufu/livecert/$PREFIX\"
      region     = \"$REGION\"" ;;
  *)     echo "unknown RECORD_STORE_BACKEND: $RECORD_STORE_BACKEND" >&2; exit 2 ;;
esac
SSM_PREFIX="/choudoufu/livecert/$PREFIX"
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

# ssm_prefix_count counts parameters under a path. NOT `--query
# 'length(Parameters)'`: the CLI applies that per RESULT PAGE, so a prefix
# holding 66 parameters printed "10\n10\n10\n10\n10\n10\n6" - a string every
# numeric comparison chokes on. On the 2026-09-01 proof run that made a
# working record store report "holds no parameters" (a FALSE failure), and
# the same bug in teardown skipped the delete loop and left 75 parameters
# behind. Counting names line-by-line aggregates across pages correctly.
ssm_prefix_count() {
  aws ssm get-parameters-by-path --path "$1" --recursive \
    --query 'Parameters[].Name' --output text 2>/dev/null \
    | tr '\t' '\n' | grep -c . || true
}

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
    record_store "$RECORD_STORE_BACKEND" {
$RECORD_STORE_ARGS
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

  # The record store is not tagged and no destroy reaches it, so it needs its
  # own teardown. Doing it here rather than in sweep() because it must run on
  # every exit path, including a run that never reached test_plan.
  if [ "$RECORD_STORE_BACKEND" = "ssm" ] && [ "$TARGET" = "aws" ]; then
    rs_left="$(ssm_prefix_count "$SSM_PREFIX")"
    log "  record store (ssm $SSM_PREFIX): $rs_left parameter(s) to delete"
    if [ "${rs_left:-0}" -gt 0 ]; then
      aws ssm get-parameters-by-path --path "$SSM_PREFIX" --recursive \
        --query 'Parameters[].Name' --output text 2>/dev/null | tr '\t' '\n' \
        | while read -r n; do [ -n "$n" ] && aws ssm delete-parameter --name "$n" >/dev/null 2>&1; done
      log "    remaining after delete: $(ssm_prefix_count "$SSM_PREFIX")"
    fi
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
  # Known bound, deliberately not "fixed": this counts LINES, and a raw XML
  # error body that puts <Code>Throttling</Code> and <Message>Rate
  # exceeded</Message> on separate lines is caught once, by the message. A
  # body carrying the code and no recognised message counts zero. Adding
  # '<Code>Throttling</Code>' to the alternation was tried and reverted: it
  # makes the common two-line body count 2 for one throttle, which corrupts
  # the number far more than the gap it closes. The count is a lower bound,
  # and it is applied identically to both binaries, so a comparison between
  # them stays sound even where the absolute is short.
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
# Plan wall-clock instrumentation (issue #578).
#
# Nothing anywhere has ever timed stock `plan` against choudoufu `plan` on
# the same estate. The only stock number on record for this terralith is
# `terraform apply`, so there has been no basis for a comparative planning
# claim in either direction. live/e2e/terralith-scale/MIGRATION.md's 36x
# and 262x figures are floci, on another machine, and one side of them was
# taken to establish an adoption ratio rather than to measure planning
# cost - they are not a baseline and are deliberately not reused here.
#
# What makes the two numbers below comparable, and what would void them:
#
#   - Same machine, same session, same region, same scale, same estate,
#     minutes apart. Stock plans its own state after cold_deploy has
#     converged; choudoufu plans the migrated estate at test_plan.
#   - Both run with TF_LOG unset. The stage-gating plan at test_plan keeps
#     its TF_LOG=DEBUG instrumentation for the throttling measurement, and
#     is reported separately: writing megabytes of debug log inside a timed
#     region measures the log, not the plan.
#   - Both are warm - the provider is already installed in each directory
#     by that directory's own init, well before either timed region opens.
#   - Three runs each, all three values reported, no mean and no selection.
#     Whole-second resolution, from date(1), which is what this script
#     already uses.
#
# Emptiness is recorded rather than enforced. Stock's plan is expected to
# propose nothing; choudoufu's is NOT necessarily expected to, because #566
# found the ECS identity defect #572 leaving 3/55 unresolved at scale 1 and
# 9/205 at scale 4. If either plan proposes anything, the comparison is not
# between two equivalent operations, and the report has to say so rather
# than quietly present the seconds as like-for-like.
#
# This block reports measurements. It never fails the run: a real-money
# certification must not be lost to an instrumentation error.
# ══════════════════════════════════════════════════════════════════════
PLAN_TIMING_REPORT=""

timed_plans() {
  local label="$1" dir="$2" bin="$3"
  local i start end secs out rc verdict
  local secs_list="" verdicts=""

  for i in 1 2 3; do
    start=$(date +%s)
    out="$(cd "$dir" && "$bin" plan -input=false -no-color 2>&1)"
    rc=$?
    end=$(date +%s)
    secs=$((end - start))

    if [ "$rc" -ne 0 ]; then
      verdict="exit${rc}"
    elif grep -qF "No changes. Your infrastructure matches the configuration." <<< "$out"; then
      verdict="empty"
    else
      verdict="$(grep -oE 'Plan: [0-9]+ to add, [0-9]+ to change, [0-9]+ to destroy' <<< "$out" | head -1 | tr ' ' '_')"
      [ -n "$verdict" ] || verdict="non-empty"
      printf '%s\n' "$out" > "$WORK/plantiming_${label}_${i}.out"
      log "    run ${i} was NOT a no-change plan (${verdict}); full output kept at plantiming_${label}_${i}.out"
      grep -E '^  # ' <<< "$out" | head -10 | sed 's/^/      /'
    fi

    log "    ${label} plan run ${i}: ${secs}s (${verdict})"
    secs_list="${secs_list}${secs_list:+ }${secs}"
    verdicts="${verdicts}${verdicts:+,}${verdict}"
  done

  PLAN_TIMING_REPORT="${PLAN_TIMING_REPORT}${PLAN_TIMING_REPORT:+
}  ${label}: ${secs_list} seconds (3 runs, TF_LOG unset, warm provider); verdicts ${verdicts}"
}

# ══════════════════════════════════════════════════════════════════════
# API-CALL INSTRUMENTATION (issue #622, and the call-count half of the
# stock-vs-choudoufu plan comparison #578/#588 measured only in seconds).
#
# What is counted, and what each count is worth:
#
#   * PROVIDER-MEDIATED CALLS - counted EXACTLY. terraform-provider-aws
#     logs one "HTTP Request Sent" entry per AWS SDK request when the
#     provider's log level is DEBUG, carrying rpc.method=<Service>/<Op>.
#     The entry is MULTI-LINE (a body block sits between the header line
#     and the attributes), so the counter below reassembles entries on the
#     leading timestamp before matching - a plain `grep -c` on the header
#     line gets the total right but loses every operation name to the
#     continuation lines. Stock's plan makes no other kind of call, so for
#     stock this IS the plan's API-call count.
#
#   * CHOUDOUFU'S OWN CLIENT CALLS - NOT counted, and not countable from
#     this log. internal/live/cloudcontrol's Client talks to Cloud Control
#     and to the Tagging API over its own net/http client, inside the tofu
#     process, and logs no line per HTTP request. What internal/live/
#     discovery logs is one [DEBUG] line per TYPE listed and per TYPE swept,
#     which is a different quantity in each direction: a Cloud Control
#     listing is one ListResources call per type (plus pagination pages,
#     which are not logged at all), while the WHOLE tagging sweep is one
#     estate-filtered GetResources call that logs one line for every type it
#     covers (tagging.go, sweepViaTagging). So both are reported as type
#     counts, explicitly, rather than dressed up as call counts.
#
#   * TypeScan.Refined - issue #622's question - IS exact: cloudcontrol.go
#     prints one line per GetResource refinement at the same place it
#     increments scan.Refined. Two structural facts decide what a number
#     here means. It can only be produced by scanTypeCloudControl, the
#     per-type Cloud Control path; the tagging sweep never refines at all
#     ("tags always arrive with the candidate", sweepViaTagging's own doc
#     comment), so a run whose sweep is served entirely by the tagging path
#     reads zero however populated the account is. And the refinement
#     scales with the ACCOUNT's object count for the types that DO take the
#     Cloud Control path, not with the estate's, which is why only a real
#     account can answer whether it still fires materially. Reported for
#     BOTH the first post-migration plan (cold hint store, widest sweep) and
#     a steady-state plan taken after the three timed runs; those are
#     different questions and one number answers neither.
#
# Like timed_plans, this block only reports. It never fails the run.
# ══════════════════════════════════════════════════════════════════════
API_CALL_REPORT=""

# apicalls_awk writes the entry-reassembling counter to a file and echoes
# its path. Kept as a file rather than inlined so the same program can be
# re-run by hand over a kept WORK dir (LIVECERT_KEEP_WORK=1).
APICALLS_AWK=""
apicalls_awk() {
  if [ -z "$APICALLS_AWK" ]; then
    APICALLS_AWK="$WORK/apicalls.awk"
    cat > "$APICALLS_AWK" <<'AWKEOF'
function flush() {
  if (entry ~ /HTTP Request Sent/) {
    total++
    op = "unknown"
    # hclog quotes an attribute value containing a space, and several AWS
    # service names DO contain one - rpc.method="Route 53/GetHostedZone",
    # rpc.method="Resource Groups Tagging API/GetResources". The unquoted
    # alternative must come second: matching it first would stop at the
    # opening quote and bucket every Route 53 call as "unknown", which is
    # exactly what the first version of this program did (22 of 156 calls
    # on the floci proving run, all of them Route 53).
    if (match(entry, /rpc\.method="[^"]+"/)) {
      op = substr(entry, RSTART + 12, RLENGTH - 13)
    } else if (match(entry, /rpc\.method=[A-Za-z0-9]+\/[A-Za-z0-9]+/)) {
      op = substr(entry, RSTART + 11, RLENGTH - 11)
    }
    cnt[op]++
  }
  entry = ""
}
/^20[0-9][0-9]-[0-9][0-9]-[0-9][0-9]T/ { flush(); entry = $0; next }
{ entry = entry " " $0 }
END {
  flush()
  # Tab-separated, count BEFORE the operation, because an operation name can
  # contain a space ("Route 53/GetHostedZone"). The first version emitted
  # "<op> <count>" and the reader split on whitespace, so every Route 53 row
  # printed the count as "Route" and the operation as "53" - three identical
  # "53 Route" lines on the scale-1 real-AWS run, with the TOTAL still right.
  printf "TOTAL\t%d\n", total + 0
  for (o in cnt) printf "OP\t%d\t%s\n", cnt[o], o
}
AWKEOF
  fi
  printf '%s\n' "$APICALLS_AWK"
}

# analyze_api_calls reads a TF_LOG=DEBUG capture and logs, for one labelled
# plan: the exact provider-mediated request count with its per-operation
# breakdown, choudoufu's own discovery-client floor, and the per-type
# GetResource refinement counts (#622). Appends one summary line to
# API_CALL_REPORT.
analyze_api_calls() {
  local label="$1" f="$2"
  local prog total refined listings tagsweeps joins
  if [ ! -f "$f" ]; then
    log "  ${label}: no debug log at $f - not instrumented"
    return 0
  fi
  prog="$(apicalls_awk)"

  awk -f "$prog" "$f" > "$WORK/apicalls_${label}.counts" 2>/dev/null
  total="$(awk -F'\t' '$1=="TOTAL"{print $2}' "$WORK/apicalls_${label}.counts")"
  [ -n "$total" ] || total=0

  # One line per GetResource refinement, printed beside scan.Refined++.
  refined="$(grep -cF 'refined with GetResource' "$f" 2>/dev/null || true)"
  listings="$(grep -cE 'stateless/discovery: listing .* via Cloud Control' "$f" 2>/dev/null || true)"
  tagsweeps="$(grep -cE 'stateless/discovery: sweeping .* via the Tagging API' "$f" 2>/dev/null || true)"
  joins="$(grep -cF 'joined one from the estate' "$f" 2>/dev/null || true)"

  log "  ${label}: ${total:-0} provider-mediated AWS API request(s) (exact, from rpc.method entries)"
  log "    top operations:"
  awk -F'\t' '$1=="OP"{printf "      %8d %s\n", $2, $3}' "$WORK/apicalls_${label}.counts" | sort -rn | head -25
  # These two counts are types, not calls, and they scale differently:
  # a Cloud Control listing is one ListResources call per TYPE (plus
  # pagination), while the whole Tagging sweep is ONE estate-filtered
  # GetResources call (plus pagination) that logs one line per type it
  # covers (internal/live/discovery/tagging.go, sweepViaTagging). Reporting
  # both as "calls" would overstate the tagging path by a factor of the
  # type count and understate the Cloud Control path by its page depth.
  log "    choudoufu's own Cloud Control / Tagging client (types, not calls - see below):"
  log "      ${listings:-0} type(s) listed via Cloud Control ListResources (>= 1 call each, more with pagination)"
  log "      ${tagsweeps:-0} type(s) covered by the estate-filtered Tagging sweep (ONE GetResources call for all of them, plus pagination)"
  log "      ${joins:-0} tag-index join(s)"
  log "    TypeScan.Refined (#622): ${refined:-0} per-object GetResource refinement(s) total"
  if [ "${refined:-0}" -gt 0 ]; then
    log "    refinements by type:"
    grep -F 'refined with GetResource' "$f" \
      | sed -E 's/.*stateless\/discovery: ([a-z0-9_]+) identifier .*/\1/' \
      | sort | uniq -c | sort -rn | head -25 | sed 's/^/      /'
  fi
  API_CALL_REPORT="${API_CALL_REPORT}${API_CALL_REPORT:+
}  ${label}: ${total:-0} provider-mediated request(s); ${listings:-0} type(s) via Cloud Control, ${tagsweeps:-0} type(s) via the one-call tagging sweep; TypeScan.Refined=${refined:-0}"
}

# instrumented_plan runs ONE extra plan with TF_LOG=DEBUG purely to count
# calls. It is deliberately NOT one of timed_plans' three: writing a debug
# log inside a timed region measures the log, not the plan, which is the
# same rule 2c/4d already state. Its own wall clock is reported anyway, so
# a reader can see what the instrumentation cost.
instrumented_plan() {
  local label="$1" dir="$2" bin="$3"
  local f="$WORK/apicalls_${label}.debug.log"
  local start end secs out rc verdict
  start=$(date +%s)
  out="$(cd "$dir" && TF_LOG=DEBUG TF_LOG_PATH="$f" "$bin" plan -input=false -no-color 2>&1)"
  rc=$?
  end=$(date +%s)
  secs=$((end - start))
  if [ "$rc" -ne 0 ]; then
    verdict="exit${rc}"
  elif grep -qF "No changes. Your infrastructure matches the configuration." <<< "$out"; then
    verdict="empty"
  else
    verdict="$(grep -oE 'Plan: [0-9]+ to add, [0-9]+ to change, [0-9]+ to destroy' <<< "$out" | head -1 | tr ' ' '_')"
    [ -n "$verdict" ] || verdict="non-empty"
  fi
  printf '%s\n' "$out" > "$WORK/apicalls_${label}.out"
  log "  ${label}: instrumented plan took ${secs}s (${verdict}); NOT a timing measurement - TF_LOG=DEBUG is on"
  analyze_api_calls "$label" "$f"

  # The same capture also answers "was this side throttled", and until now
  # nothing asked it. analyze_debug_log ran only on cold_deploy's apply,
  # migrate's tag writes and test_plan - so every throttle count this harness
  # has ever printed for a PLAN belongs to choudoufu, and stock's plan-side
  # count did not exist. That made the two sides incomparable exactly where
  # the comparison matters: stock's apply (writes) against choudoufu's plan
  # (reads) is not a like-for-like pairing. One call, both labels, no extra
  # AWS request - the log is already on disk.
  read -r _b _t _r _p <<< "$(analyze_debug_log "$f")"
  log "  ${label}: plan-side throttling: ${_t} throttling-error line(s), ${_r} retry line(s), ${_p} pagination-continuation line(s) in ${_b}B"
  printf '%s %s %s %s %s\n' "$label" "$_b" "$_t" "$_r" "$_p" >> "$WORK/plan_throttle_by_label.txt"
}

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

# Issue #578: stock's own plan on its own state, AFTER the apply has
# converged and BEFORE anything migrates it - a refresh-and-diff of an
# already-applied estate, which is the operation choudoufu's post-migration
# plan at test_plan is compared against. Unattributed on purpose: it reports
# no verdict, so no stage should be blamed if it goes wrong.
gauntlet_end_stage
log "=== 2c. plan timing: stock $TF_COLD plan x3, converged estate, TF_LOG unset (#578) ==="
timed_plans "stock-terraform" "$COLD_DIR" "$TF_COLD"

log "=== 2d. API calls: one EXTRA stock plan with TF_LOG=DEBUG, outside every timed region ==="
instrumented_plan "stock-terraform" "$COLD_DIR" "$TF_COLD"

# ══════════════════════════════════════════════════════════════════════
# WALLCLOCK_TRACE (issue #867): the stock half of the idle-gap comparison.
#
# #683 measured the read pass's head-of-line stall from ONE debug capture per
# side, and #867 asks for three, on the same estate in the same session, so
# that "the sweep showed no straggler" can be said about a sample rather than
# about a single run. The stock side has to be taken HERE, before migrate, for
# the reason 2c states: once live-import has stamped tags on 335 objects the
# stock plan is no longer empty, and a plan that renders 335 changes is not
# comparable to an empty one on wall clock or on request count.
#
# These are not timing measurements and nothing gates on them - TF_LOG=DEBUG
# is on, which is the same rule 2c/2d/4d already state. What they are for is
# live/live-cert/wallclock-gaps.py, which reads in-flight concurrency out of
# the capture.
# ══════════════════════════════════════════════════════════════════════
if [ "${WALLCLOCK_TRACE:-0}" = "1" ]; then
  log "=== 2e. wall-clock trace (#867): stock plan x3 with TF_LOG=DEBUG, pre-migrate, empty ==="
  for i in 1 2 3; do
    instrumented_plan "trace-stock-$i" "$COLD_DIR" "$TF_COLD"
  done
fi

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
    record_store "$RECORD_STORE_BACKEND" {
$RECORD_STORE_ARGS
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

# TP_FAIL defers this stage's failure instead of taking it immediately, so
# that the plan-timing measurement at 4d still runs (issue #588). It is NOT
# a softening of the verdict: TP_FAIL is non-empty iff the old code would
# have called fail(), the same fail() is called with the same message a few
# steps further down, and the stage still reports `fail`. What changes is
# only that a run which is going to fail this stage anyway now yields the
# one number it was dispatched to produce before it exits.
#
# Why that matters here specifically: 4d is the ONLY measurement of
# choudoufu's plan wall-clock, #588's whole blocked cell, and it sat behind
# an early `exit 1`. #578 got stock's side at scale 4 and lost choudoufu's
# entirely (to #580's refusal), so the pair could not be formed and the
# claim's slope stayed unknown - a second run losing it to a DIFFERENT
# scale-4 failure would repeat that at full price. timed_plans already
# records a per-run verdict (`empty` / `Plan:_N_to_add...` / `exitN`) beside
# every duration, so a number taken on a non-empty plan is self-labelling in
# the output and cannot be mistaken for a no-change plan by a later reader.
#
# The success path below is deliberately left in its original order
# (gating plan -> 4b -> 4c -> stage pass -> 4d), so a passing run's numbers
# stay directly comparable to #578's scale-1 run; the fallback only fires on
# the path that previously produced nothing at all.
TP_FAIL=""
if [ "$PLAN_RC" -ne 0 ]; then
  printf '%s\n' "$PLAN_OUT" | tail -40
  # Carry the plan's OWN diagnosis into the stage detail, not the exit code.
  # "the post-migrate plan exited 1" is the exact shape this repository
  # refuses everywhere else - an exit code standing in for a verdict - and
  # it is what the recorded live_cert row is stuck with until the run that
  # produced it is repeated. A row that names the rule and the first error
  # is readable without the log; a row that names a number is not.
  PLAN_ERR="$(grep -m1 -E '^Error: ' <<< "$PLAN_OUT" | tr -d '\r')"
  PLAN_RULE="$(grep -m1 -oE 'Rule: [a-z0-9-]+' <<< "$PLAN_OUT")"
  PLAN_ERR_N="$(grep -c -E '^Error: ' <<< "$PLAN_OUT" | tr -d ' ')"
  TP_FAIL="the post-migrate plan exited ${PLAN_RC} with ${PLAN_ERR_N} error(s)${PLAN_RULE:+, ${PLAN_RULE}}${PLAN_ERR:+ - first: ${PLAN_ERR}}"
elif ! grep -qF "No changes. Your infrastructure matches the configuration." <<< "$PLAN_OUT"; then
  grep -E '^  #' <<< "$PLAN_OUT" | head -20
  TP_FAIL="the post-migrate plan is not empty - see $WORK/test_plan.out"
else
  log "  plan empty in ${PLAN_S}s"
fi

log "=== 4a2. state model: prove each piece was actually exercised, not just configured ==="
# A run that DECLARES a cloud record store and then never writes to it looks
# identical, in every other line of this log, to one that used local disk. So
# each piece is checked against the cloud, by listing, and the check fails the
# stage rather than warning - "configured" is not "used".
#
# Identity is proved by the tag index, values by the record store. Effects are
# not asserted here: this fixture declares none, so an assertion would be
# vacuously green and worse than no assertion at all.
if [ "$TARGET" = "aws" ]; then
  ident_n="$(aws resourcegroupstaggingapi get-resources --region "$REGION" \
    --tag-filters "Key=tofu-estate,Values=$ESTATE" \
    --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
  log "  identity (tofu-estate=$ESTATE tags in the cloud): $ident_n resource(s)"
  [ "${ident_n:-0}" -gt 0 ] || fail "identity piece unused: no resource in the account carries tofu-estate=$ESTATE"

  if [ "$RECORD_STORE_BACKEND" = "ssm" ]; then
    rec_n="$(ssm_prefix_count "$SSM_PREFIX")"
    log "  values (record_store ssm at $SSM_PREFIX): $rec_n parameter(s) in Parameter Store"
    [ "${rec_n:-0}" -gt 0 ] || fail "values piece unused: record_store is \"ssm\" but $SSM_PREFIX holds no parameters - the store was declared and never written"
    # A read-side check was tried here and REMOVED as vacuous rather than
    # kept looking rigorous: it grepped the plan log for "ssm", which matches
    # the provider's own aws_ssm_parameter type sweep 600+ times on any run,
    # so it could not fail for the right reason. choudoufu's staterecord SSM
    # client logs nothing per request (the #682 logging covers the
    # cloudcontrol/tagging client, a different seam), so there is nothing
    # honest to grep for until that client logs too. Write-side proof stands;
    # the read side is proved at the cache stage (5b), whose "state cache
    # supplied N" line comes from the projection itself.
  else
    log "  values: record_store is \"$RECORD_STORE_BACKEND\" (local disk), so the cloud values piece is NOT under test in this run"
  fi
fi

log "=== 4b. test_plan: throttling/pagination read from the debug log ==="
if [ "$THROTTLE_LOG" = "1" ] && [ -f "$PLAN_LOG" ]; then
  read -r PLAN_LOG_BYTES THROTTLE_HITS RETRY_LINES PAGINATION_HITS <<< "$(analyze_debug_log "$PLAN_LOG")"
  log "  test_plan debug log: ${PLAN_LOG_BYTES} bytes, ${THROTTLE_HITS} throttling-error line(s), ${RETRY_LINES} genuine-retry line(s), ${PAGINATION_HITS} pagination-continuation line(s)"
else
  PLAN_LOG_BYTES=0 THROTTLE_HITS=0 RETRY_LINES=0 PAGINATION_HITS=0
  log "  THROTTLE_LOG=$THROTTLE_LOG - not instrumented for this stage"
fi

# The deferred failure from the gating plan (see TP_FAIL above) is taken
# HERE, after the plan-timing measurement has had its chance to run. This is
# the stage's real failure: same message, same fail(), verdict still `fail`.
if [ -n "$TP_FAIL" ]; then
  log "=== 4d (fallback path): the gating plan did NOT pass, so this stage will fail - taking the plan-timing measurement first, because it is the one number this run was dispatched for (#588) ==="
  gauntlet_end_stage
  timed_plans "choudoufu" "$ADOPTED_DIR" "$TOFU"
  log "=== PLAN TIMING SUMMARY (scale=$SCALE, ${EXPECTED} resources, target=$TARGET) - PARTIAL ==="
  log "  WARNING: choudoufu's gating plan was NOT a no-change plan, so the two sides below are NOT like-for-like. Read each run's own verdict, not the seconds alone."
  printf '%s\n' "$PLAN_TIMING_REPORT"
  log "  stage-gating choudoufu plan, measured separately WITH TF_LOG=DEBUG: ${PLAN_S}s (${PLAN_LOG_BYTES} bytes of debug log written inside that region)"
  # Same reasoning as the timing measurement above: #622's refinement count
  # is a property of the sweep, not of whether the plan came back empty, so
  # a run that is about to fail this stage still yields it.
  analyze_api_calls "choudoufu-first" "$PLAN_LOG"
  instrumented_plan "choudoufu-steady" "$ADOPTED_DIR" "$TOFU"
  log "=== API CALL SUMMARY (scale=$SCALE, ${EXPECTED} resources, target=$TARGET) - PARTIAL ==="
  printf '%s\n' "$API_CALL_REPORT"
  CURRENT_STAGE=test_plan
  fail "$TP_FAIL"
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

# Issue #578: the same three-run, TF_LOG-unset measurement stock got at
# 2c, on the migrated estate, so the two sides differ in the binary and
# the state model and in nothing else this script controls. The gating
# plan above keeps its debug instrumentation and stays out of this number.
gauntlet_end_stage
log "=== 4d. plan timing: choudoufu plan x3, migrated estate, TF_LOG unset (#578) ==="
timed_plans "choudoufu" "$ADOPTED_DIR" "$TOFU"
log "=== PLAN TIMING SUMMARY (scale=$SCALE, ${EXPECTED} resources, target=$TARGET) ==="
printf '%s\n' "$PLAN_TIMING_REPORT"
log "  stage-gating choudoufu plan, measured separately WITH TF_LOG=DEBUG: ${PLAN_S}s (${PLAN_LOG_BYTES} bytes of debug log written inside that region)"

# The stage-gating plan at step 4 is the FIRST plan after migration: cold
# hint store, widest sweep. The one below is the FIFTH, taken after
# timed_plans' three, so it is the steady state the sweep narrowing (#627)
# is supposed to have narrowed. #622 asks about the steady state, but the
# first plan is the only thing the difference can be read against, so both
# are counted and reported separately.
log "=== 4e. API calls: the FIRST post-migration plan (stage-gating, already TF_LOG=DEBUG) ==="
analyze_api_calls "choudoufu-first" "$PLAN_LOG"
log "=== 4f. API calls: one EXTRA steady-state choudoufu plan with TF_LOG=DEBUG, outside every timed region ==="
instrumented_plan "choudoufu-steady" "$ADOPTED_DIR" "$TOFU"
log "=== API CALL SUMMARY (scale=$SCALE, ${EXPECTED} resources, target=$TARGET) ==="
printf '%s\n' "$API_CALL_REPORT"

# ══════════════════════════════════════════════════════════════════════
# WALLCLOCK_TRACE (issue #867): the choudoufu half, and the analysis.
#
# Three steady-state plans, taken after 4d's three timed runs and 4f's, so the
# hint store is warm and the sweep is the narrowed one (#627) rather than the
# first plan's widest. Then wallclock-gaps.py over all six captures at once,
# so the run log itself carries the comparison instead of it living only in a
# directory someone has to still have.
#
# The gap threshold is 0.8s because that is the one #683 reported at, and a
# re-measurement that moves its own threshold is not a re-measurement.
# ══════════════════════════════════════════════════════════════════════
if [ "${WALLCLOCK_TRACE:-0}" = "1" ]; then
  log "=== 4g. wall-clock trace (#867): choudoufu plan x3 with TF_LOG=DEBUG, steady state ==="
  for i in 1 2 3; do
    instrumented_plan "trace-choudoufu-$i" "$ADOPTED_DIR" "$TOFU"
  done

  log "=== 4h. wall-clock trace (#867): idle gaps >= 0.8s, both sides, one instrument ==="
  # The label=path list is built in the positional parameters rather than in
  # an array: this script runs under `set -u`, and in bash 3.2 - the bash
  # macOS ships, and the one a maintainer running this by hand is most likely
  # to hit - expanding an EMPTY array under `set -u` is an unbound-variable
  # error, so the "nothing to analyze" branch would abort the run instead of
  # reporting.
  set --
  for i in 1 2 3; do
    [ -f "$WORK/apicalls_trace-stock-$i.debug.log" ] \
      && set -- "$@" "stock-$i=$WORK/apicalls_trace-stock-$i.debug.log"
  done
  for i in 1 2 3; do
    [ -f "$WORK/apicalls_trace-choudoufu-$i.debug.log" ] \
      && set -- "$@" "choudoufu-$i=$WORK/apicalls_trace-choudoufu-$i.debug.log"
  done
  if [ "$#" -eq 0 ]; then
    log "  no trace captures on disk - nothing to analyze"
  elif ! command -v python3 >/dev/null 2>&1; then
    log "  python3 is not on PATH; the captures are under $WORK, run live/live-cert/wallclock-gaps.py over them by hand"
  else
    python3 "$ROOT/live/live-cert/wallclock-gaps.py" --min 0.8 "$@" \
      | tee "$WORK/wallclock-gaps.txt" | sed 's/^/  /'
  fi
fi

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

log "=== 5b. state cache: written by the apply, and USED by the plan after it (#685) ==="
# Placement matters and the first attempt got it wrong. test_apply (stage 5)
# is the first choudoufu APPLY, so it is the first thing that can write a
# cache - a check before it would have asserted against a file that cannot
# exist yet. This runs one more plan, after the apply, which is the first plan
# in the whole harness that has a cache to read.
#
# A cache that is written and never consulted is indistinguishable from a
# working one in every other line of this log, which is the state this fork
# shipped for months while its documentation described a cache. So all three
# halves are asserted and zero hits FAILS rather than warns.
if [ -n "${CHOUDOUFU_STATE_CACHE:-}" ]; then
  [ -s "$CHOUDOUFU_STATE_CACHE" ] \
    || fail "state cache enabled at $CHOUDOUFU_STATE_CACHE and the apply wrote nothing there"
  log "  written: $(wc -c < "$CHOUDOUFU_STATE_CACHE" | tr -d " ")B at $CHOUDOUFU_STATE_CACHE"

  CACHE_LOG="$WORK/cache_plan.debug.log"
  CACHE_OUT="$(cd "$ADOPTED_DIR" && TF_LOG=DEBUG TF_LOG_PATH="$CACHE_LOG" "$TOFU" plan -input=false -no-color 2>&1)"; CACHE_RC=$?
  [ "$CACHE_RC" -eq 0 ] || { printf '%s\n' "$CACHE_OUT" | tail -20; fail "the post-apply plan exited $CACHE_RC"; }
  grep -qF "No changes. Your infrastructure matches the configuration." <<< "$CACHE_OUT" \
    || fail "the post-apply plan was not empty, so a cache hit count from it would not be comparable"

  CACHE_HITS="$(grep -oE "state cache supplied [0-9]+ instance" "$CACHE_LOG" 2>/dev/null | grep -oE "[0-9]+" | tail -1)"
  [ -n "$CACHE_HITS" ] \
    || fail "the plan never reported a state-cache result; the cache was written but the plan did not load it"
  log "  USED: the post-apply plan answered $CACHE_HITS instance(s) from the cache instead of reading them"
  [ "$CACHE_HITS" -gt 0 ] \
    || fail "the state cache was written and loaded and supplied 0 instances - written, not used"
else
  log "  CHOUDOUFU_STATE_CACHE unset, so the cache half is NOT under test in this run"
fi

CURRENT_STAGE=""
gauntlet_end
log "=== all four stages passed against target=$TARGET scale=$SCALE; teardown runs next via the EXIT trap ==="
log "=== THROTTLE SUMMARY (target=$TARGET scale=$SCALE) ==="
log "  cold_deploy apply (${EXPECTED} resources, parallelism=10, single Route53 zone): ${COLD_APPLY_S}s, debug log ${COLD_LOG_BYTES}B, ${COLD_THROTTLE_HITS} throttling-error line(s), ${COLD_RETRY_LINES} genuine-retry line(s)"
log "  migrate -approve (${VERIFIED} sequential tag-write API calls): ${MIGRATE_S}s, debug log ${MIGRATE_LOG_BYTES}B, ${MIGRATE_THROTTLE_HITS} throttling-error line(s), ${MIGRATE_RETRY_LINES} genuine-retry line(s)"
log "  test_plan (choudoufu's own O(types) full-account sweep): ${PLAN_S}s, debug log ${PLAN_LOG_BYTES}B, ${THROTTLE_HITS} throttling-error line(s), ${RETRY_LINES} genuine-retry line(s), ${PAGINATION_HITS} pagination-continuation line(s)"
