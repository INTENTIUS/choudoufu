#!/usr/bin/env bash
set -uo pipefail

# live/live-cert/reference-eks.sh: the live-AWS certification for
# reference-eks (#1113; the maintainer's ruling of 2026-10-03 named the
# estate and a $5/run ceiling). It runs every active stage against a real
# account, on the SAME configuration the emulator crossing runs
# (live/e2e/reference-eks/estate.sh) and through the SAME stage bodies
# (live/e2e/reference-eks/stages.sh), and reports them with the GAUNTLET
# protocol so tools/gauntlet's `live-cert` records them in the artifact's
# LiveCert slice and never in a headline bar. Every stage's verdict lands in
# the row; its `clear` still reads only the four LiveCertScopeStages names
# (cold_deploy, migrate, test_plan, test_apply), which is the maintainer's
# to widen.
#
# What only this run measures, and the reason it exists: IRSA's successor
# EKS Pod Identity (the association is created here and nowhere on the
# emulator), access-entry authorization (an access entry with the
# AmazonEKSViewPolicy association), the VPC CNI and a real managed node
# group joining the cluster.
#
# And #1524's records check on a managed control plane. The migrated estate
# keeps its records in its own cluster - record_store "kubernetes" with a
# control_plane "eks" block naming the cluster - so encryption_at_rest is
# read from DescribeCluster rather than reported NOT CHECKED. Before
# migrate writes the first record, `choudoufu live-cluster -json` asks the
# cluster the contract's assertions (namespace_access and read_isolation,
# the two RBAC answers, both SelfSubjectAccessReviews; encryption_at_rest
# from the provider; estate_boundary), records each verdict in migrate's
# detail and in records-check.tsv in the log, requires encryption_at_rest
# to have been ANSWERED (ok or fail, never not_checked - a not_checked there
# means the provider read did not happen), and waives exactly the
# assertions it did not find ok with allow_insecure, which every later run
# prints, the way live/managed-k8s/harness.sh does. On TARGET=floci the same
# block runs against floci's DescribeCluster and the k3s API server.
# greenfield keeps a local store: its plan starts before the cluster that
# would hold the records exists.
#
# THE PAID RUN IS THE MAINTAINER'S (CLAUDE.md, "Heavy and paid runs are the
# maintainer's, by hand"). No agent starts TARGET=aws. The command is:
#
#   go build -o /tmp/gauntlet ./tools/gauntlet
#   /tmp/gauntlet live-cert -target aws -region us-east-1 \
#     -ceiling-usd 5 -timeout-seconds 14400 reference-eks
#
# or, dispatched (waits for the maintainer's approval click on `real-aws`):
#
#   gh workflow run live-cert.yml -R INTENTIUS/choudoufu --ref main \
#     -f estate=reference-eks -f scale=1 -f ceiling_usd=5 -f timeout_minutes=250
#
# What it costs, from AWS's list prices in us-east-1 (not measured; the
# account's Budgets alarm is what binds spend): the EKS control plane at
# $0.10/hour, one t3.small node at about $0.021/hour and its 20 GB gp2
# volume, no NAT gateway and no load balancer - twice over, since greenfield
# stands a second cluster up after day2_teardown has removed the first.
# Each cluster plus node group is fifteen to twenty minutes to create and
# about the same to delete, and the day-2 stages between are minutes, so a
# whole cycle is roughly two and a half hours and still under $1.50 of the
# $5 ceiling. The commands above carry a timeout that fits it.
#
# Prove it on the emulator first, as #1324 asks of every live-cert harness:
#
#   TARGET=floci bash live/live-cert/run.sh reference-eks -timeout 7200
#
# TARGET=floci creates neither the access entry nor the pod identity
# association (estate.sh's header: floci serves their list routes only), so
# the floci proof covers the harness, every stage, the records check and
# the teardown, not those two resources. Written under the maintainer's
# no-testing ruling for #1113 and not run by the changes that added it, on
# either target.
#
# TEARDOWN, on every exit path: the cluster leg first, then the AWS leg.
#   0. choudoufu's own `apply -destroy` on the greenfield estate, when
#      greenfield got as far as applying (best effort).
#   1. choudoufu's own `apply -destroy` on the migrated estate, the path
#      day2_teardown measures (best effort; never trusted alone, and a
#      no-op once day2_teardown has run).
#   2. Stock `terraform destroy` on the cold-deploy state, in two steps:
#      `-target` the four cluster-leg objects first, then everything, so no
#      object in the cluster - and no load balancer or network interface a
#      controller could have made for one - outlives the cluster.
#   3. An independent listing (eks_verify_empty plus livecert_verify_empty)
#      that never trusts either destroy's exit code, and a raw AWS CLI sweep
#      (eks_sweep plus livecert_sweep) if anything survives.
#
# Env: as live/live-cert/reference-ec2-vpc.sh (TARGET, REGION, RUN_ID,
# TOFU_BIN, TF_COLD_BIN, FLOCI_PORT, FLOCI_IMAGE, LIVECERT_WORK_DIR,
# LIVECERT_KEEP_FLOCI). FLOCI_PORT defaults to 4817.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
LIB="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib"
# shellcheck source=live/e2e/lib/gauntlet.sh
source "$ROOT/live/e2e/lib/gauntlet.sh"
# shellcheck source=live/live-cert/lib/live-cert.sh
source "$LIB/live-cert.sh"
# shellcheck source=live/e2e/reference-eks/estate.sh
source "$ROOT/live/e2e/reference-eks/estate.sh"
# shellcheck source=live/e2e/reference-eks/stages.sh
source "$ROOT/live/e2e/reference-eks/stages.sh"

TARGET="${TARGET:-floci}"
REGION="${REGION:-us-east-1}"
RUN_ID="${RUN_ID:-livecert-$(date +%s)-$$}"
ESTATE="livecert-eks-reference"
WORK="${LIVECERT_WORK_DIR:-$(mktemp -d)}"
mkdir -p "$WORK"
FLOCI_PORT="${FLOCI_PORT:-4817}"
FLOCI_NAME="choudoufu-livecert-reference-eks-$$"
FLOCI_NS="lceks$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
COLD_DIR="$WORK/cold"
ADOPTED_DIR="$WORK/adopted"
# Every name this run creates starts with PREFIX, so a retry with the same
# RUN_ID, and the sweep, find exactly this run's objects. IAM role names
# cap at 64 characters; "<prefix>-g-cluster" stays well under. greenfield's
# estate is GREEN_PREFIX, which starts with PREFIX too.
PREFIX="$(printf '%s' "$RUN_ID" | tr -c 'A-Za-z0-9-' '-' | cut -c1-40)"
CLUSTER="${PREFIX}-eks"
GREEN_PREFIX="${PREFIX}-g"
GREEN_CLUSTER="${GREEN_PREFIX}-eks"
GREEN_ESTATE="livecert-eks-greenfield"
GREEN_DIR="$WORK/green"
# The migrated estate's records live in its own cluster (#1524).
RECORDS_NS="tofu-records-${ESTATE}"
KC="$WORK/kubeconfig"
GREEN_KC="$WORK/kubeconfig-green"

case "$TARGET" in
  floci) ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"; ACCESS_API=false; AWS_N=15 ;;
  aws) ENDPOINT=""; ACCESS_API=true; AWS_N=20 ;;
  *) echo "TARGET must be floci or aws, got $TARGET" >&2; exit 2 ;;
esac
CLUSTER_N=6
TOTAL_N=$((AWS_N + CLUSTER_N))

log() { printf '%s\n' "$*"; }

provider_block() {
  if [ "$TARGET" = "floci" ]; then
    cat <<EOF
provider "aws" {
  region                      = "$REGION"
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
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

# STORE_BLOCK is empty until migrate's records check has measured the
# cluster; from then on it is the record_store "kubernetes" block every
# root of the migrated estate is written with.
STORE_BLOCK=""
write_root() { # $1 = dir, $2 = estate or empty, then estate.sh's variants
  local d="$1" e="$2"; shift 2
  mkdir -p "$d"
  REFERENCE_EKS_STORE="${STORE_BLOCK:-.tofu-records}" reference_eks_main_tf "$e" "$(provider_block)" "$PREFIX" "$ACCESS_API" "$REGION" "$@" > "$d/main.tf"
}

# ── EKS-specific listing and sweep, beside live-cert.sh's network ones ──
# The cluster, its node groups and the four IAM roles are found by NAME
# (every one carries PREFIX), not only by tag: an EKS cluster mid-delete
# and an IAM role are both things the tagging API answers late or not at
# all, and a listing that cannot see a resource reports it gone.
eks_verify_empty() {
  local dirty=0 c ng r
  c="$(livecert_aws eks list-clusters --query "clusters[?starts_with(@, '${PREFIX}-')]" --output text 2>/dev/null || true)"
  if [ -n "$c" ]; then
    printf '  eks_verify_empty: live cluster(s): %s\n' "$c"; dirty=1
    for x in $c; do
      ng="$(livecert_aws eks list-nodegroups --cluster-name "$x" --query 'nodegroups' --output text 2>/dev/null || true)"
      [ -n "$ng" ] && printf '  eks_verify_empty: %s still has node group(s): %s\n' "$x" "$ng"
    done
  fi
  local p
  for p in "$PREFIX" "$GREEN_PREFIX"; do
    for r in cluster node ops app; do
      if livecert_aws iam get-role --role-name "${p}-$r" >/dev/null 2>&1; then
        printf '  eks_verify_empty: live IAM role %s\n' "${p}-$r"; dirty=1
      fi
    done
  done
  local rtbs
  rtbs="$(livecert_aws ec2 describe-route-tables --filters "Name=tag:tofu-cert-run,Values=$RUN_ID" \
    --query 'RouteTables[].RouteTableId' --output text 2>/dev/null || true)"
  [ -n "$rtbs" ] && { printf '  eks_verify_empty: live route table(s): %s\n' "$rtbs"; dirty=1; }
  [ "$dirty" = "0" ]
}

eks_sweep() {
  local c ng r p rtb assoc
  printf '  eks_sweep: force-deleting everything named %s-* (EKS, IAM) and the route tables tagged tofu-cert-run=%s\n' "$PREFIX" "$RUN_ID"
  for c in $(livecert_aws eks list-clusters --query "clusters[?starts_with(@, '${PREFIX}-')]" --output text 2>/dev/null || true); do
    for ng in $(livecert_aws eks list-nodegroups --cluster-name "$c" --query 'nodegroups' --output text 2>/dev/null || true); do
      printf '    deleting node group %s/%s\n' "$c" "$ng"
      livecert_aws eks delete-nodegroup --cluster-name "$c" --nodegroup-name "$ng" >/dev/null 2>&1 || true
      livecert_aws eks wait nodegroup-deleted --cluster-name "$c" --nodegroup-name "$ng" 2>/dev/null || true
    done
    printf '    deleting cluster %s (its access entries and pod identity associations go with it)\n' "$c"
    livecert_aws eks delete-cluster --name "$c" >/dev/null 2>&1 || true
    livecert_aws eks wait cluster-deleted --name "$c" 2>/dev/null || true
  done
  local role
  for role in "${PREFIX}"-{cluster,node,ops,app} "${GREEN_PREFIX}"-{cluster,node,ops,app}; do
    livecert_aws iam get-role --role-name "$role" >/dev/null 2>&1 || continue
    for p in $(livecert_aws iam list-attached-role-policies --role-name "$role" --query 'AttachedPolicies[].PolicyArn' --output text 2>/dev/null || true); do
      livecert_aws iam detach-role-policy --role-name "$role" --policy-arn "$p" >/dev/null 2>&1 || true
    done
    printf '    deleting IAM role %s\n' "$role"
    livecert_aws iam delete-role --role-name "$role" >/dev/null 2>&1 || true
  done
  for rtb in $(livecert_aws ec2 describe-route-tables --filters "Name=tag:tofu-cert-run,Values=$RUN_ID" \
      --query 'RouteTables[].RouteTableId' --output text 2>/dev/null || true); do
    # shellcheck disable=SC2016  # a JMESPath literal, not shell
    for assoc in $(livecert_aws ec2 describe-route-tables --route-table-ids "$rtb" \
        --query 'RouteTables[].Associations[?Main!=`true`].RouteTableAssociationId' --output text 2>/dev/null || true); do
      livecert_aws ec2 disassociate-route-table --association-id "$assoc" >/dev/null 2>&1 || true
    done
    printf '    deleting route table %s\n' "$rtb"
    livecert_aws ec2 delete-route-table --route-table-id "$rtb" >/dev/null 2>&1 || true
  done
}

all_empty() { eks_verify_empty && livecert_verify_empty; }

TEARDOWN_DONE=0
MIGRATE_DONE=0
GREEN_STARTED=0
teardown() {
  [ "$TEARDOWN_DONE" = "1" ] && return 0
  TEARDOWN_DONE=1
  log "=== TEARDOWN (target=$TARGET run=$RUN_ID prefix=$PREFIX) - cluster leg first ==="

  if [ "$GREEN_STARTED" = "1" ] && [ -d "$GREEN_DIR" ]; then
    log "  choudoufu apply -destroy on the greenfield estate ($GREEN_DIR) - best effort, verified by listing below"
    ( cd "$GREEN_DIR" && AWS_ENDPOINT_URL="$ENDPOINT" "${TOFU:-false}" apply -destroy -input=false -auto-approve -no-color ) \
      > "$WORK/teardown_greenfield_destroy.out" 2>&1
    log "    exit=$?"
  fi

  if [ "$MIGRATE_DONE" = "1" ] && [ -d "$ADOPTED_DIR" ]; then
    log "  choudoufu apply -destroy on the migrated estate ($ADOPTED_DIR) - best effort, verified by listing below"
    ( cd "$ADOPTED_DIR" && AWS_ENDPOINT_URL="$ENDPOINT" "${TOFU:-false}" apply -destroy -input=false -auto-approve -no-color ) \
      > "$WORK/teardown_choudoufu_destroy.out" 2>&1
    log "    exit=$? (see $WORK/teardown_choudoufu_destroy.out)"
  fi

  if [ -d "$COLD_DIR" ] && [ -f "$COLD_DIR/terraform.tfstate" ]; then
    local targets=() a
    while read -r a; do targets+=("-target=$a"); done < <(reference_eks_cluster_leg)
    log "  stock destroy, step 1 of 2: the cluster leg (${targets[*]})"
    ( cd "$COLD_DIR" && AWS_ENDPOINT_URL="$ENDPOINT" "${TF_COLD:-terraform}" destroy -input=false -auto-approve -no-color "${targets[@]}" ) \
      > "$WORK/teardown_stock_destroy_cluster_leg.out" 2>&1
    log "    exit=$?"
    log "  stock destroy, step 2 of 2: everything else"
    ( cd "$COLD_DIR" && AWS_ENDPOINT_URL="$ENDPOINT" "${TF_COLD:-terraform}" destroy -input=false -auto-approve -no-color ) \
      > "$WORK/teardown_stock_destroy.out" 2>&1
    log "    exit=$? (see $WORK/teardown_stock_destroy.out) - not trusted alone, verifying by listing next"
  fi

  if all_empty; then
    log "  VERIFIED EMPTY by listing: no cluster, node group or role named ${PREFIX}-*, nothing tagged tofu-cert-run=$RUN_ID"
  else
    log "  destroy left resources behind - running the EKS sweep, then the network sweep"
    eks_sweep
    livecert_sweep
    if all_empty; then
      log "  VERIFIED EMPTY by listing after the sweep"
    else
      log "  STILL NOT EMPTY after destroy and sweep - see the listing above; retry teardown with RUN_ID=$RUN_ID"
    fi
  fi

  if [ "$TARGET" = "floci" ]; then
    if [ "${LIVECERT_KEEP_FLOCI:-0}" = "1" ]; then
      log "  LIVECERT_KEEP_FLOCI=1: leaving $FLOCI_NAME up for the driver that asked for it"
    else
      local children
      children="$(docker ps -a --filter "name=floci-${FLOCI_NS}-" --format '{{.Names}}' 2>/dev/null)"
      # shellcheck disable=SC2086  # docker container names, never globs
      [ -n "$children" ] && docker rm -f $children >/dev/null 2>&1
      gauntlet_floci_teardown "$FLOCI_NAME"
    fi
  fi
  rm -rf "$WORK"
}

CURRENT_STAGE=""
fail() {
  printf 'FAIL: %s\n' "$*" >&2
  [ -n "$CURRENT_STAGE" ] && gauntlet_stage "$CURRENT_STAGE" fail "$*"
  exit 1
}

# See reference-ec2-vpc.sh's on_signal for why the apply is backgrounded and
# `exec`ed: a signal must reach terraform, and teardown must run after it.
APPLY_PID=""
on_signal() {
  log "=== caught $1 - forwarding to in-flight child (pid ${APPLY_PID:-none}) and tearing down ==="
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

# ── 0. tools and endpoint ───────────────────────────────────────────────
log "=== 0. tools (target=$TARGET run_id=$RUN_ID prefix=$PREFIX) ==="
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH (the provider block's exec plugin runs it)"
command -v jq >/dev/null 2>&1 || fail "jq is not on PATH"
TF_COLD="${TF_COLD_BIN:-terraform}"
command -v "$TF_COLD" >/dev/null 2>&1 || fail "$TF_COLD is not on PATH (needed for cold_deploy's stock apply)"
if [ -n "${TOFU_BIN:-}" ]; then
  TOFU="$TOFU_BIN"
  [ -x "$TOFU" ] || fail "TOFU_BIN=$TOFU_BIN is not an executable file"
else
  mkdir -p "$WORK/bin"
  TOFU="$WORK/bin/choudoufu"
  ( cd "$ROOT" && env -u PWD go build -o "$TOFU" ./cmd/choudoufu ) || fail "go build ./cmd/choudoufu failed"
fi

if [ "$TARGET" = "floci" ]; then
  command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
  [ -S /var/run/docker.sock ] || fail "no /var/run/docker.sock to mount - floci's EKS real mode needs it to start k3s"
  log "=== 0b. floci on :$FLOCI_PORT ($FLOCI_IMAGE), EKS real mode, host endpoints ==="
  gauntlet_floci_start "$FLOCI_NAME" -p "${FLOCI_PORT}:4566" \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -e FLOCI_SERVICES_EKS_ENDPOINT_MODE=host \
    -e "FLOCI_DOCKER_RESOURCE_NAMESPACE=$FLOCI_NS" \
    "$FLOCI_IMAGE" || fail "docker run for $FLOCI_NAME failed"
  healthy=0
  for _ in $(seq 1 45); do
    H="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
    case "$H" in *'"eks"'*) healthy=1; break ;; esac
    sleep 2
  done
  [ "$healthy" = "1" ] || fail "floci did not come up healthy (eks) at $ENDPOINT"
  export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" AWS_ENDPOINT_URL="$ENDPOINT"
else
  log "=== 0b. target=aws, region=$REGION - the ambient AWS credential chain, no endpoint override ==="
  unset AWS_ENDPOINT_URL || true
  export AWS_REGION="$REGION"
  IDENTITY="$(aws sts get-caller-identity --query Account --output text 2>&1)" \
    || fail "aws sts get-caller-identity failed - no usable credentials for a real run: $IDENTITY"
  log "  caller account ...${IDENTITY: -4} (only the last 4 digits are ever logged or recorded)"
  # A name collision with a previous run's leftovers would make cold_deploy
  # adopt or fail on someone else's cluster; refuse instead.
  eks_verify_empty >/dev/null || fail "objects named ${PREFIX}-* already exist in this account; pass a fresh RUN_ID or tear the old run down with the same RUN_ID first"
fi

# day2_crash needs a build with e2eTestingFeatures set: the engine's own
# TOFU_E2E_APPLY_RESOURCE_INTERRUPT hook is compiled in only then.
mkdir -p "$WORK/bin"
TOFU_CRASH="$WORK/bin/choudoufu-e2e"
( cd "$ROOT" && env -u PWD go build -ldflags="-X 'main.e2eTestingFeatures=yes'" -o "$TOFU_CRASH" ./cmd/choudoufu ) \
  || fail "go build -ldflags e2eTestingFeatures ./cmd/choudoufu failed"
command -v kubectl >/dev/null 2>&1 || fail "kubectl is not on PATH (the cluster leg is read with it, never through choudoufu's report)"

# kubectl against a cluster, the way the provider block reaches it: the
# endpoint and CA from the AWS API, a token from `aws eks get-token`. Read
# directly, never through choudoufu's own report. Written once per cluster;
# the migrated estate's record_store "kubernetes" block names the same file.
write_kubeconfig() { # $1 = cluster, $2 = path
  livecert_aws eks update-kubeconfig --name "$1" --kubeconfig "$2" >/dev/null 2>&1 || return 1
  chmod 600 "$2"
}
labelled_count() { # $1 = kubeconfig, $2 = estate
  local n=0 k got
  for k in namespace serviceaccount configmap deployment secret; do
    got="$(kubectl --kubeconfig "$1" get "$k" -A -l "tofu-estate=$2" -o name)" || return 1
    n=$((n + $(printf '%s\n' "$got" | awk 'NF' | wc -l | tr -d ' ')))
  done
  printf '%s\n' "$n"
}
aws_marked() { # $1 = estate: the AWS leg's marked objects, GetResources and IAM's own tag reads
  gauntlet_estate_objects "$1" livecert_aws || return 1
  printf '%s\n' "$GAUNTLET_ESTATE_N"
}
kc_main() { kubectl --kubeconfig "$KC" "$@"; }
kc_green() { kubectl --kubeconfig "$GREEN_KC" "$@"; }

# ── the hooks live/e2e/reference-eks/stages.sh runs every day-2 stage through
# The records are in the cluster, so there is no local store to read
# (REF_RECORDS empty): day2_crash reads the outcome and not the record, and
# no_local_state's "local state" is the state cache alone.
REF_RECORDS=""
ADOPTED="$ADOPTED_DIR"
STOCK="$COLD_DIR"
ref_kubectl() { kubectl --kubeconfig "$KC" "$@"; }
ref_aws() { livecert_aws "$@"; }
ref_tofu() { local d="$1"; shift; ( cd "$d" && "$TOFU" "$@" ); }
ref_tofu_crash() { local d="$1"; shift; ( cd "$d" && "$TOFU_CRASH" "$@" ); }
ref_stock() { local d="$1"; shift; ( cd "$d" && "$TF_COLD" "$@" ); }
ref_root() { write_root "$@"; }
ref_aws_provider() { provider_block; }
ref_labelled() { labelled_count "$KC" "$ESTATE"; }
ref_aws_marked() { aws_marked "$ESTATE"; }
ref_crash_rename() {
  CRASH_RENAME_DETAIL="The create_before_destroy rename window (#1768) is not interrupted on this cycle: its body (live/e2e/lib/gauntlet.sh's gauntlet_kind_day2_crash_rename) asserts on the deposed record the interrupted apply writes, read from a local store, and this cycle's records are in the cluster (record_store \"kubernetes\"); the emulator crossing interrupts it."
  return 3
}

# ══════════════════════════════════════════════════════════════════════
# cold_deploy
# ══════════════════════════════════════════════════════════════════════
gauntlet_begin_stage cold_deploy
write_root "$COLD_DIR" "" || fail "could not write the stock root"
( cd "$COLD_DIR" && "$TF_COLD" init -input=false -no-color ) > "$WORK/cold_deploy_init.out" 2>&1 \
  || { tail -20 "$WORK/cold_deploy_init.out"; fail "stock init failed"; }
( cd "$COLD_DIR" && exec "$TF_COLD" apply -input=false -auto-approve -no-color ) \
  > "$WORK/cold_deploy_apply.out" 2>&1 &
APPLY_PID=$!
wait "$APPLY_PID"
APPLY_RC=$?
APPLY_PID=""
[ "$APPLY_RC" -eq 0 ] || { tail -40 "$WORK/cold_deploy_apply.out"; fail "stock apply exited $APPLY_RC"; }
grep -qE "Apply complete! Resources: ${TOTAL_N} added" "$WORK/cold_deploy_apply.out" \
  || { grep -E 'Apply complete' "$WORK/cold_deploy_apply.out"; fail "stock apply did not create exactly ${TOTAL_N} resources"; }
STATUS="$(livecert_aws eks describe-cluster --name "$CLUSTER" --output json | jq -r '.cluster.status')" \
  || fail "cannot describe cluster $CLUSTER after the cold apply"
write_kubeconfig "$CLUSTER" "$KC" || fail "aws eks update-kubeconfig could not write a kubeconfig for $CLUSTER"
UNLABELLED="$(labelled_count "$KC" "$ESTATE")" || fail "could not read the cluster leg with kubectl"
[ "$UNLABELLED" = "0" ] || fail "the cluster leg already carries $UNLABELLED tofu-estate label(s)"
STOCK_INVENTORY="$(reference_eks_inventory livecert_aws kc_main "$PREFIX")" || fail "could not read the stock inventory greenfield compares against"
gauntlet_stage cold_deploy pass "${TOTAL_N} resources (${AWS_N} AWS, ${CLUSTER_N} cluster) from stock $TF_COLD against $TARGET; cluster $CLUSTER is $STATUS; eks_access_api=$ACCESS_API; tofu-cert-run=$RUN_ID"

log "=== stock oracles: day-2 plans on copies of cold_deploy's own state ==="
reference_eks_stock_oracles

# ══════════════════════════════════════════════════════════════════════
# migrate, with #1524's records check first
# ══════════════════════════════════════════════════════════════════════
gauntlet_begin_stage migrate
log "=== migrate: the records check (#1524) against $CLUSTER, then live-import into record_store \"kubernetes\" ==="
kubectl --kubeconfig "$KC" create namespace "$RECORDS_NS" >/dev/null 2>&1 \
  || kubectl --kubeconfig "$KC" get namespace "$RECORDS_NS" >/dev/null 2>&1 \
  || fail "could not create the records namespace $RECORDS_NS"
RC_DIR="$WORK/records-check"
mkdir -p "$RC_DIR"
reference_eks_terraform_block "$ESTATE" "$(reference_eks_kubernetes_store "$KC" "$RECORDS_NS" "$CLUSTER" "$REGION")" > "$RC_DIR/versions.tf" \
  || fail "could not write the records check's root"
RC_JSON="$WORK/live-cluster.json"
( cd "$RC_DIR" && "$TOFU" live-cluster -json -no-color ) > "$RC_JSON" 2> "$WORK/live-cluster.err"
RC_RC=$?
jq -e '.settings | length > 0' "$RC_JSON" >/dev/null 2>&1 \
  || { head -c 2000 "$WORK/live-cluster.err"; fail "choudoufu live-cluster -json (exit $RC_RC) produced no report of the cluster contract for $CLUSTER"; }
printf 'setting\tverdict\tfound\n' > "$WORK/records-check.tsv"
jq -r '.settings[] | [.setting, .verdict, (.found | gsub("[\t\n]"; " "))] | @tsv' "$RC_JSON" >> "$WORK/records-check.tsv"
log "  records check (#1524), live-cluster exit $RC_RC, server $(jq -r .server "$RC_JSON"):"
sed 's/^/    /' "$WORK/records-check.tsv"
rc_verdict() { jq -r --arg s "$1" '[.settings[] | select(.setting == $s) | .verdict][0] // "absent"' "$RC_JSON"; }
rc_found() { jq -r --arg s "$1" '[.settings[] | select(.setting == $s) | .found][0] // ""' "$RC_JSON"; }
ENC_VERDICT="$(rc_verdict encryption_at_rest)"
ENC_FOUND="$(rc_found encryption_at_rest)"
NSA_VERDICT="$(rc_verdict namespace_access)"
ISO_VERDICT="$(rc_verdict read_isolation)"
BND_VERDICT="$(rc_verdict estate_boundary)"
for v in "$NSA_VERDICT" "$ISO_VERDICT" "$ENC_VERDICT" "$BND_VERDICT"; do
  [ "$v" != "absent" ] || fail "the records check's report is missing one of namespace_access, read_isolation, encryption_at_rest and estate_boundary: $(jq -c '[.settings[].setting]' "$RC_JSON")"
done
case "$ENC_VERDICT" in
  ok|fail) ;;
  *) fail "encryption_at_rest reads $ENC_VERDICT with control_plane \"eks\" naming $CLUSTER, so the provider did not answer or was not believed (#1524): $ENC_FOUND" ;;
esac
WAIVE="$(jq -c '[.settings[] | select(.verdict != "ok") | .setting]' "$RC_JSON")"
[ "$WAIVE" = "[]" ] && WAIVE=""
STORE_BLOCK="$(reference_eks_kubernetes_store "$KC" "$RECORDS_NS" "$CLUSTER" "$REGION" "$WAIVE")"
RECORDS_DETAIL="records check (#1524, record_store \"kubernetes\" with control_plane \"eks\" on $CLUSTER): encryption_at_rest=$ENC_VERDICT ($ENC_FOUND); RBAC namespace_access=$NSA_VERDICT, read_isolation=$ISO_VERDICT; estate_boundary=$BND_VERDICT; allow_insecure ${WAIVE:-none}"

write_root "$ADOPTED_DIR" "$ESTATE" || fail "could not write the adopted root"
( cd "$ADOPTED_DIR" && "$TOFU" init -input=false -no-color ) > "$WORK/migrate_init.out" 2>&1 \
  || { tail -20 "$WORK/migrate_init.out"; fail "adopted init failed"; }
IMPORT_OUT="$(cd "$ADOPTED_DIR" && "$TOFU" live-import -state="$COLD_DIR/terraform.tfstate" -estate="$ESTATE" -no-color 2>&1)" \
  || { printf '%s\n' "$IMPORT_OUT" | tail -30; fail "live-import (dry run) failed"; }
APPROVE_OUT="$(cd "$ADOPTED_DIR" && "$TOFU" live-import -state="$COLD_DIR/terraform.tfstate" -estate="$ESTATE" -approve -no-color 2>&1)" \
  || { printf '%s\n' "$APPROVE_OUT" | tail -30; fail "live-import -approve failed"; }
MIGRATE_DONE=1
SUMMARY_LINE="$(grep -E 'resource\(s\) newly stamped' <<< "$APPROVE_OUT" | head -1 | sed 's/\.$//')"
grep -qE '[0-9]+ resource\(s\) newly stamped, 0 already stamped, [0-9]+ newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 0 skipped' <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT" | tail -40; fail "live-import -approve did not finish with 0 failed and 0 skipped: ${SUMMARY_LINE:-no summary line}"; }
LABELLED="$(labelled_count "$KC" "$ESTATE")" || fail "could not read the cluster leg with kubectl"
[ "$LABELLED" = "$CLUSTER_N" ] || fail "only $LABELLED of ${CLUSTER_N} cluster-leg objects carry tofu-estate=$ESTATE, read with kubectl"
CLUSTER_TAGS="$(livecert_aws eks describe-cluster --name "$CLUSTER" --output json | jq '.cluster.tags')" || fail "could not read the cluster's tags"
[ "$(jq -r '."tofu-address" // empty' <<< "$CLUSTER_TAGS")" = "aws_eks_cluster.this" ] \
  || fail "cluster $CLUSTER carries tofu-address=$(jq -r '."tofu-address" // "none"' <<< "$CLUSTER_TAGS"), not aws_eks_cluster.this"
RECORD_SECRETS="$(kubectl --kubeconfig "$KC" get secrets -n "$RECORDS_NS" -l "tofu-estate=$ESTATE" -o name 2>/dev/null | grep -c '^secret/tofu-record-' || true)"
[ "$RECORD_SECRETS" -gt 0 ] || fail "live-import reported records, but no tofu-record- Secret in $RECORDS_NS carries tofu-estate=$ESTATE"
gauntlet_stage migrate pass "$SUMMARY_LINE; ${CLUSTER_N} cluster-leg objects labelled tofu-estate=$ESTATE (kubectl); cluster tofu-address=aws_eks_cluster.this (AWS CLI); $RECORD_SECRETS record Secret(s) in $RECORDS_NS on the estate's own cluster. $RECORDS_DETAIL"

# ══════════════════════════════════════════════════════════════════════
# test_plan
# ══════════════════════════════════════════════════════════════════════
gauntlet_begin_stage test_plan
PLAN_OUT="$(cd "$ADOPTED_DIR" && "$TOFU" plan -input=false -no-color 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -30; fail "the post-migrate plan exited $PLAN_RC"; }
grep -qF "No changes." <<< "$PLAN_OUT" || { grep -E '^  #|^Plan:' <<< "$PLAN_OUT"; fail "the post-migrate plan is not empty"; }
if grep -qE "Provider (configuration not evaluable|unavailable)" <<< "$PLAN_OUT"; then
  fail "the plan reported the kubernetes provider unavailable on a cluster that exists: $(grep -m1 -E 'Provider (configuration not evaluable|unavailable)' <<< "$PLAN_OUT")"
fi
gauntlet_stage test_plan pass "post-migrate plan is empty, the kubernetes provider configured from aws_eks_cluster.this with an exec token and no provider-unavailable warning, the records read from the cluster's own record_store \"kubernetes\""

# ══════════════════════════════════════════════════════════════════════
# test_apply
# ══════════════════════════════════════════════════════════════════════
gauntlet_begin_stage test_apply
BEFORE_AWS="$(aws_marked "$ESTATE")" || fail "could not count the AWS leg"
BEFORE_K8S="$(labelled_count "$KC" "$ESTATE")" || fail "could not count the cluster leg"
NOOP_OUT="$(cd "$ADOPTED_DIR" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; NOOP_RC=$?
[ "$NOOP_RC" -eq 0 ] || { printf '%s\n' "$NOOP_OUT" | tail -30; fail "the no-op apply exited $NOOP_RC"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$NOOP_OUT" || { grep -E 'Apply complete' <<< "$NOOP_OUT"; fail "the no-op apply was not a genuine no-op"; }
AFTER_AWS="$(aws_marked "$ESTATE")" || fail "could not count the AWS leg"
AFTER_K8S="$(labelled_count "$KC" "$ESTATE")" || fail "could not count the cluster leg"
[ "$AFTER_AWS" = "$BEFORE_AWS" ] || fail "AWS-leg marked object count changed across a no-op apply: $BEFORE_AWS -> $AFTER_AWS"
[ "$AFTER_K8S" = "$BEFORE_K8S" ] || fail "cluster-leg labelled object count changed across a no-op apply: $BEFORE_K8S -> $AFTER_K8S"
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed); AWS leg unchanged at $BEFORE_AWS marked objects (GetResources and IAM's own tag reads), cluster leg unchanged at $BEFORE_K8S labelled objects (kubectl)"

# ══════════════════════════════════════════════════════════════════════
# The day-2 stages, strict and no_local_state: stages.sh's bodies, the same
# the emulator crossing runs. day2_teardown destroys the cluster, so it is
# the migrated estate's last stage.
# ══════════════════════════════════════════════════════════════════════
reference_eks_stage_drift_reconverge
reference_eks_stage_plan_approval
reference_eks_stage_day2_rename
reference_eks_stage_day2_remove
reference_eks_stage_day2_count
reference_eks_stage_day2_replace
reference_eks_stage_day2_crash
reference_eks_stage_strict
reference_eks_stage_no_local_state
reference_eks_stage_day2_teardown

# ══════════════════════════════════════════════════════════════════════
# greenfield: choudoufu applies the same configuration where nothing of it
# exists, under GREEN_PREFIX, after day2_teardown has removed the first
# cluster, and the account's objects are compared with stock's cold deploy
# by shape (every name with its prefix taken off). A local record store:
# the cluster that would hold the records does not exist when the plan
# starts.
# ══════════════════════════════════════════════════════════════════════
gauntlet_begin_stage greenfield
mkdir -p "$GREEN_DIR"
REFERENCE_EKS_STORE=".tofu-records" reference_eks_main_tf "$GREEN_ESTATE" "$(provider_block)" "$GREEN_PREFIX" "$ACCESS_API" "$REGION" > "$GREEN_DIR/main.tf" \
  || fail "could not write the greenfield root"
( cd "$GREEN_DIR" && "$TOFU" init -input=false -no-color ) > "$WORK/green_init.out" 2>&1 \
  || { tail -20 "$WORK/green_init.out"; fail "greenfield choudoufu init failed"; }
GREEN_STARTED=1
( cd "$GREEN_DIR" && exec "$TOFU" apply -input=false -auto-approve -no-color ) > "$WORK/green_apply.out" 2>&1 &
APPLY_PID=$!
wait "$APPLY_PID"
GREEN_RC=$?
APPLY_PID=""
[ "$GREEN_RC" -eq 0 ] || { tail -40 "$WORK/green_apply.out"; fail "the greenfield apply exited $GREEN_RC - a cluster that does not exist yet must read as empty, stock's order, not refuse"; }
grep -qE "Apply complete! Resources: ${TOTAL_N} added" "$WORK/green_apply.out" \
  || { grep -E 'Apply complete' "$WORK/green_apply.out"; fail "the greenfield apply did not create exactly ${TOTAL_N} resources"; }
write_kubeconfig "$GREEN_CLUSTER" "$GREEN_KC" || fail "aws eks update-kubeconfig could not write a kubeconfig for $GREEN_CLUSTER"
GREEN_INVENTORY="$(reference_eks_inventory livecert_aws kc_green "$GREEN_PREFIX")" || fail "could not read the greenfield inventory"
EXPECTED="$STOCK_INVENTORY"
if [ "${BREAK:-}" = "1" ]; then
  EXPECTED="$(grep -v '^configmap' <<< "$STOCK_INVENTORY")"
fi
if [ "$(sort <<< "$EXPECTED")" = "$(sort <<< "$GREEN_INVENTORY")" ]; then
  [ "${BREAK:-}" = "1" ] && fail "BREAK=1: the config maps were dropped from the expected inventory and the comparison still held"
  GREEN_LABELLED="$(labelled_count "$GREEN_KC" "$GREEN_ESTATE")" || fail "could not count greenfield labels"
  [ "$GREEN_LABELLED" = "$CLUSTER_N" ] || fail "the greenfield cluster leg carries $GREEN_LABELLED tofu-estate=$GREEN_ESTATE label(s), want ${CLUSTER_N}"
  REPLAN="$(cd "$GREEN_DIR" && "$TOFU" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$REPLAN" | tail -30; fail "the greenfield replan failed"; }
  grep -qF "No changes." <<< "$REPLAN" || fail "the greenfield replan is not empty: $(grep -m1 -E '^Plan:' <<< "$REPLAN")"
  gauntlet_stage greenfield pass "choudoufu applied ${TOTAL_N} resources where none of them existed (prefix $GREEN_PREFIX), the kubernetes provider configured only once aws_eks_cluster.this existed; $(wc -l <<< "$GREEN_INVENTORY" | tr -d ' ') inventory lines (AWS CLI and kubectl, names without their prefix) match stock's cold deploy object by object; ${CLUSTER_N} cluster-leg objects labelled; the replan is empty"
else
  if [ "${BREAK:-}" = "1" ]; then
    gauntlet_stage greenfield pass "BREAK=1 control: with the config maps dropped from the expected inventory the object-by-object comparison correctly fails to hold"
  else
    diff <(sort <<< "$EXPECTED") <(sort <<< "$GREEN_INVENTORY") | sed 's/^/  /'
    fail "the greenfield inventory differs from stock's cold deploy (diff above)"
  fi
fi

gauntlet_end
log "=== every stage ran against target=$TARGET; teardown runs next via the EXIT trap, greenfield's estate first, then the cluster leg before the AWS leg ==="
