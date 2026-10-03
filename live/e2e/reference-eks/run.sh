#!/usr/bin/env bash
# The reference EKS estate (#1113): one estate across AWS and the cluster
# its own aws_eks_cluster creates, run on the floci-eks substrate. Hand-written
# (live/e2e/reference-eks/estate.sh), so its two provider pins come from
# live/oracle-versions.json. Needs Docker with a socket floci can reach
# (floci's EKS real mode starts a k3s container per cluster), the AWS CLI
# (the provider block's exec plugin runs `aws eks get-token`), jq and
# terraform. Runs on ports 4750 and 5750.
set -uo pipefail

# What this estate is for, in the order the design comment gives it
# (https://github.com/INTENTIUS/choudoufu/issues/1113#issuecomment-5973813530):
#
#   - The provider block reads the cluster's endpoint and CA straight off
#     aws_eks_cluster.this - no data source - and authenticates with an exec
#     plugin. corpus-eks-basic goes through data.aws_eks_cluster and a token
#     data source, so this is the only estate whose every stage measures
#     the provider-configuration fixpoint's managed-value leg (#1113 step 2)
#     and the exec credential (the design's open point 4).
#   - The block runs exactly as written. The tools run on the host and
#     floci's EKS host endpoint mode publishes k3s on https://localhost:<port>
#     with a localhost SAN (floci's EksClusterManager starts k3s with
#     --tls-san=localhost), so the CA verifies with no `insecure` delta -
#     corpus-eks-basic's delta 7 exists only because that estate runs its
#     tools inside a container on a Docker network.
#   - The cluster leg is read with kubectl INSIDE the k3s container, never
#     through choudoufu's own report, the way corpus-eks-basic reads
#     aws-auth.
#
# What it does not measure on this substrate, said once here and once on
# cold_deploy's floci-eks note: IRSA, EKS Pod Identity at run time, the VPC
# CNI, EBS CSI, access-entry authorization and managed add-ons. The access
# entry and the pod identity association are not even created here
# (eks_access_api=false; estate.sh's header gives floci's list-only routes
# as the reason). live/live-cert/reference-eks.sh creates them on real AWS.
#
# Stages run: cold_deploy, migrate, test_plan, test_apply, greenfield. Every
# other active stage is reported not_run with the reason, so the row reads
# honestly incomplete rather than silently short; the day-2 stages are the
# next unit of work on this estate. Written under the maintainer's
# no-testing ruling for #1113 and NOT run by the change that added it - the
# first emulator run is its first measurement.
#
#   bash live/e2e/reference-eks/run.sh
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the main emulator (default 4750; the
#                greenfield emulator is FLOCI_PORT+1000).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        set to 1 to run the greenfield stage's negative control:
#                one object (the config map) is dropped from the expected
#                inventory, and the object-by-object comparison must then
#                fail to hold.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# shellcheck source=live/e2e/lib/gauntlet.sh
source "$ROOT/live/e2e/lib/gauntlet.sh"
# shellcheck source=live/e2e/reference-eks/estate.sh
source "$ROOT/live/e2e/reference-eks/estate.sh"

gauntlet_plugin_cache

WORK="$(mktemp -d)"
REGION="us-east-1"
PREFIX="refeks"
ESTATE="reference-eks-crossing"
GREEN_ESTATE="reference-eks-greenfield"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
FLOCI_PORT="${FLOCI_PORT:-4750}"
FLOCI_GREEN_PORT=$((FLOCI_PORT + 1000))
FLOCI_NAME="choudoufu-reference-eks-$$"
FLOCI_GREEN_NAME="choudoufu-reference-eks-green-$$"
# floci's per-process child namespace (FLOCI_DOCKER_RESOURCE_NAMESPACE):
# every k3s container floci starts is named floci-<ns>-eks-<cluster>, which
# is how cleanup() removes exactly this run's and how kc() finds the one to
# exec into. Short and [A-Za-z0-9] for the reason corpus-eks-basic gives.
FLOCI_NS="refeks$$"
FLOCI_GREEN_NS="refeksg$$"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"
GREEN_ENDPOINT="http://127.0.0.1:${FLOCI_GREEN_PORT}"
STOCK="$WORK/stock"
ADOPTED="$WORK/adopted"
GREEN="$WORK/green"
CLUSTER="${PREFIX}-eks"

# The access entry and pod identity association are off on floci; see
# estate.sh. The count every assertion below uses follows from it.
ACCESS_API=false
AWS_N=15
CLUSTER_N=4
TOTAL_N=$((AWS_N + CLUSTER_N))

cleanup() {
  local ns children
  for ns in "$FLOCI_NS" "$FLOCI_GREEN_NS"; do
    children="$(docker ps -a --filter "name=floci-${ns}-" --format '{{.Names}}' 2>/dev/null)"
    if [ -n "$children" ]; then
      # shellcheck disable=SC2086  # docker container names, never globs
      docker rm -f $children >/dev/null 2>&1 || true
    fi
  done
  gauntlet_floci_teardown "$FLOCI_NAME" "$FLOCI_GREEN_NAME"
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
CURRENT_STAGE=""
fail() {
  printf 'FAIL: %s\n' "$*" >&2
  if [ -n "$CURRENT_STAGE" ]; then gauntlet_stage "$CURRENT_STAGE" fail "$*"; fi
  exit 1
}
gauntlet_begin

awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }
awsg() { aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" "$@"; }

# with_endpoint <endpoint> <cmd...>: runs a tool against one emulator. The
# exec plugin inherits this environment, so `aws eks get-token` signs
# against the same emulator the cluster lives in.
with_endpoint() {
  local ep="$1"; shift
  AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" AWS_ENDPOINT_URL="$ep" "$@"
}

# k3s_of <namespace>: the k3s container floci started for this run's cluster.
k3s_of() {
  docker ps --filter "name=floci-${1}-eks-" --format '{{.Names}}' | head -n1
}

# kc <namespace> <kubectl args...>: kubectl inside that k3s container.
# `kubectl` and `k3s kubectl` are both tried: which is on PATH is a property
# of the k3s image, and a check that cannot run must fail, not pass.
kc() {
  local ns="$1" c; shift
  c="$(k3s_of "$ns")"
  [ -n "$c" ] || { printf 'kc: no k3s container for namespace %s\n' "$ns" >&2; return 1; }
  docker exec "$c" kubectl "$@" 2>/dev/null || docker exec "$c" k3s kubectl "$@"
}

# labelled_count <namespace> <estate>: cluster-leg objects carrying
# tofu-estate=<estate>, over the four kinds the estate declares.
labelled_count() {
  local ns="$1" estate="$2" n=0 k got
  for k in namespace serviceaccount configmap deployment; do
    got="$(kc "$ns" get "$k" -A -l "tofu-estate=$estate" -o name)" || return 1
    n=$((n + $(printf '%s\n' "$got" | awk 'NF' | wc -l | tr -d ' ')))
  done
  printf '%s\n' "$n"
}

# inventory <aws-fn> <namespace>: the structural inventory greenfield
# compares, one line per object, markers never part of it. The AWS leg by
# the AWS CLI, the cluster leg by kubectl in the k3s container. The
# backquoted words are JMESPath literals labelling each line, not shell.
# shellcheck disable=SC2016
inventory() {
  local awsf="$1" ns="$2" vpc
  vpc="$("$awsf" ec2 describe-vpcs --filters "Name=tag:Name,Values=${PREFIX}-vpc" --query 'Vpcs[0].VpcId' --output text)" || return 1
  "$awsf" ec2 describe-vpcs --vpc-ids "$vpc" --query 'Vpcs[].[`vpc`,CidrBlock]' --output text
  "$awsf" ec2 describe-subnets --filters "Name=vpc-id,Values=$vpc" --query 'Subnets[].[`subnet`,CidrBlock,AvailabilityZone,MapPublicIpOnLaunch]' --output text
  "$awsf" ec2 describe-internet-gateways --filters "Name=attachment.vpc-id,Values=$vpc" --query 'InternetGateways[].[`igw`,Attachments[0].State]' --output text
  "$awsf" ec2 describe-route-tables --filters "Name=vpc-id,Values=$vpc" "Name=tag:Name,Values=${PREFIX}-public" --query 'RouteTables[].[`rtb`,length(Associations)]' --output text
  "$awsf" iam get-role --role-name "${PREFIX}-cluster" --query 'Role.[`role`,RoleName]' --output text
  "$awsf" iam get-role --role-name "${PREFIX}-node" --query 'Role.[`role`,RoleName]' --output text
  "$awsf" eks describe-cluster --name "$CLUSTER" --query 'cluster.[`cluster`,name,length(resourcesVpcConfig.subnetIds)]' --output text
  "$awsf" eks describe-nodegroup --cluster-name "$CLUSTER" --nodegroup-name "${PREFIX}-default" \
    --query 'nodegroup.[`nodegroup`,nodegroupName,scalingConfig.desiredSize,scalingConfig.minSize,scalingConfig.maxSize]' --output text
  kc "$ns" get namespace app -o jsonpath='{"namespace\t"}{.metadata.name}{"\n"}'
  kc "$ns" get serviceaccount app -n app -o jsonpath='{"serviceaccount\t"}{.metadata.name}{"\n"}'
  kc "$ns" get configmap app-config -n app -o jsonpath='{"configmap\t"}{.metadata.name}{"\t"}{.data.greeting}{"\n"}'
  kc "$ns" get deployment app -n app -o jsonpath='{"deployment\t"}{.metadata.name}{"\t"}{.spec.template.spec.serviceAccountName}{"\t"}{.spec.template.spec.containers[0].image}{"\n"}'
}

aws_provider_block() {
  cat <<EOF
provider "aws" {
  region                      = "$REGION"
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style           = true
}
EOF
}

# start_floci <name> <port> <namespace>: floci in EKS real mode, host
# endpoint mode, with its children named under <namespace>.
start_floci() {
  local name="$1" port="$2" ns="$3" health=""
  gauntlet_floci_start "$name" -p "${port}:4566" \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -e FLOCI_SERVICES_EKS_ENDPOINT_MODE=host \
    -e "FLOCI_DOCKER_RESOURCE_NAMESPACE=$ns" \
    "$FLOCI_IMAGE" || return 1
  for _ in $(seq 1 45); do
    health="$(curl -fs "http://127.0.0.1:${port}/_localstack/health" 2>/dev/null)" || true
    grep -q '"eks"' <<< "$health" && return 0
    sleep 2
  done
  return 1
}

# ── 0. tools ────────────────────────────────────────────────────────────────
log "=== 0. tools ==="
for t in docker aws jq terraform curl; do
  command -v "$t" >/dev/null 2>&1 || fail "$t is not on PATH"
done
docker info >/dev/null 2>&1 || fail "docker is not running"
[ -S /var/run/docker.sock ] || fail "no /var/run/docker.sock to mount - floci's EKS real mode needs it to start k3s"
if [ -n "${TOFU_BIN:-}" ]; then
  TOFU="$TOFU_BIN"
  [ -x "$TOFU" ] || fail "TOFU_BIN=$TOFU_BIN is not an executable file"
else
  mkdir -p "$WORK/bin"
  TOFU="$WORK/bin/choudoufu"
  ( cd "$ROOT" && env -u PWD go build -o "$TOFU" ./cmd/choudoufu ) || fail "go build ./cmd/choudoufu failed"
fi
log "  choudoufu: $TOFU"

log "=== 0b. floci on :$FLOCI_PORT ($FLOCI_IMAGE), EKS real mode, host endpoints ==="
start_floci "$FLOCI_NAME" "$FLOCI_PORT" "$FLOCI_NS" || fail "floci did not come up healthy (eks) at $ENDPOINT"

# ══════════════════════════════════════════════════════════════════════
# cold_deploy: stock applies the unmodified configuration.
# ══════════════════════════════════════════════════════════════════════
gauntlet_begin_stage cold_deploy
mkdir -p "$STOCK"
reference_eks_main_tf "" "$(aws_provider_block)" "$PREFIX" "$ACCESS_API" "$REGION" > "$STOCK/main.tf" \
  || fail "could not write the stock root (provider pins unreadable?)"
gauntlet_locked_init with_endpoint "$ENDPOINT" terraform -chdir="$STOCK" init -input=false -no-color > "$WORK/stock_init.out" 2>&1 \
  || { tail -30 "$WORK/stock_init.out"; fail "stock terraform init failed"; }
with_endpoint "$ENDPOINT" terraform -chdir="$STOCK" apply -input=false -auto-approve -no-color > "$WORK/stock_apply.out" 2>&1 \
  || { tail -40 "$WORK/stock_apply.out"; fail "stock terraform apply failed against floci-eks"; }
grep -qE "Apply complete! Resources: ${TOTAL_N} added" "$WORK/stock_apply.out" \
  || { grep -E 'Apply complete' "$WORK/stock_apply.out"; fail "stock apply did not create exactly ${TOTAL_N} resources (${AWS_N} AWS, ${CLUSTER_N} cluster)"; }
STATUS="$(awsl eks describe-cluster --name "$CLUSTER" --query 'cluster.status' --output text)" \
  || fail "the AWS CLI cannot describe cluster $CLUSTER after the cold apply"
[ -n "$(k3s_of "$FLOCI_NS")" ] || fail "no k3s container for namespace $FLOCI_NS: floci did not start the cluster this estate's provider block configures against"
STOCK_INVENTORY="$(inventory awsl "$FLOCI_NS")" || fail "could not read the stock inventory"
UNLABELLED="$(labelled_count "$FLOCI_NS" "$ESTATE")" || fail "could not count tofu-estate labels with kubectl in the k3s container"
[ "$UNLABELLED" = "0" ] || fail "the stock cluster leg already carries $UNLABELLED tofu-estate=$ESTATE label(s) before any migration"
gauntlet_stage cold_deploy pass "${TOTAL_N} resources from stock terraform (${AWS_N} AWS, ${CLUSTER_N} cluster objects) against floci-eks; cluster $CLUSTER is $STATUS and its k3s container $(k3s_of "$FLOCI_NS") holds the cluster leg, read with kubectl; zero tofu-estate labels; the kubernetes provider was configured from aws_eks_cluster.this with an exec token as written; access entry and pod identity association not created (eks_access_api=false, floci serves list routes only)"

# ══════════════════════════════════════════════════════════════════════
# migrate: live-import against stock's state; both legs bound.
# ══════════════════════════════════════════════════════════════════════
gauntlet_begin_stage migrate
mkdir -p "$ADOPTED"
reference_eks_main_tf "$ESTATE" "$(aws_provider_block)" "$PREFIX" "$ACCESS_API" "$REGION" > "$ADOPTED/main.tf" \
  || fail "could not write the adopted root"
with_endpoint "$ENDPOINT" "$TOFU" -chdir="$ADOPTED" init -input=false -no-color > "$WORK/adopted_init.out" 2>&1 \
  || { tail -30 "$WORK/adopted_init.out"; fail "choudoufu init failed"; }
IMPORT_OUT="$(cd "$ADOPTED" && with_endpoint "$ENDPOINT" "$TOFU" live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -no-color 2>&1)" \
  || { printf '%s\n' "$IMPORT_OUT" | tail -30; fail "live-import (dry run) failed"; }
APPROVE_OUT="$(cd "$ADOPTED" && with_endpoint "$ENDPOINT" "$TOFU" live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -approve -no-color 2>&1)" \
  || { printf '%s\n' "$APPROVE_OUT" | tail -30; fail "live-import -approve failed"; }
SUMMARY_LINE="$(grep -E 'resource\(s\) newly stamped' <<< "$APPROVE_OUT" | head -1 | sed 's/\.$//')"
grep -qE '[0-9]+ resource\(s\) newly stamped, 0 already stamped, [0-9]+ newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 0 skipped' <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT" | tail -40; fail "live-import -approve did not finish with 0 failed and 0 skipped: ${SUMMARY_LINE:-no summary line}"; }
if grep -q "MISSING" <<< "$APPROVE_OUT"; then
  fail "live-import reported a MISSING instance; the kubernetes provider, configured from aws_eks_cluster.this, did not reach the cluster leg: $(grep -m1 MISSING <<< "$APPROVE_OUT")"
fi
LABELLED="$(labelled_count "$FLOCI_NS" "$ESTATE")" || fail "could not count tofu-estate labels with kubectl"
[ "$LABELLED" = "$CLUSTER_N" ] \
  || fail "live-import's summary was clean but only $LABELLED of the ${CLUSTER_N} cluster-leg objects carry tofu-estate=$ESTATE, read with kubectl in $(k3s_of "$FLOCI_NS")"
CLUSTER_TAGS="$(awsl eks describe-cluster --name "$CLUSTER" --query 'cluster.tags' --output json)" || fail "could not read the cluster's tags"
[ "$(jq -r '."tofu-estate" // empty' <<< "$CLUSTER_TAGS")" = "$ESTATE" ] || fail "cluster $CLUSTER carries no tofu-estate=$ESTATE tag after migrate: $CLUSTER_TAGS"
[ "$(jq -r '."tofu-address" // empty' <<< "$CLUSTER_TAGS")" = "aws_eks_cluster.this" ] || fail "cluster $CLUSTER carries tofu-address=$(jq -r '."tofu-address" // "none"' <<< "$CLUSTER_TAGS"), not aws_eks_cluster.this"
gauntlet_estate_objects "$ESTATE" awsl || fail "could not count the AWS leg's marked objects"
gauntlet_stage migrate pass "$SUMMARY_LINE; the cluster leg's ${CLUSTER_N} objects carry tofu-estate=$ESTATE, read with kubectl in the k3s container; cluster $CLUSTER carries tofu-estate and tofu-address=aws_eks_cluster.this, read with the AWS CLI; $GAUNTLET_ESTATE_N AWS objects marked (GetResources $GAUNTLET_ESTATE_RGTA_N, IAM $GAUNTLET_ESTATE_IAM_N); the kubernetes provider was configured from the cluster read through the provider-configuration fixpoint (#1113)"

# ══════════════════════════════════════════════════════════════════════
# test_plan: replan from nothing.
# ══════════════════════════════════════════════════════════════════════
gauntlet_begin_stage test_plan
PLAN_OUT="$(cd "$ADOPTED" && with_endpoint "$ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -40; fail "the post-migrate plan exited $PLAN_RC"; }
grep -qF "No changes." <<< "$PLAN_OUT" \
  || { grep -E '^  #|^Plan:' <<< "$PLAN_OUT"; fail "the post-migrate plan is not empty: $(grep -m1 -E '^Plan:' <<< "$PLAN_OUT")"; }
if grep -qE "Provider (configuration not evaluable|unavailable)" <<< "$PLAN_OUT"; then
  fail "the plan was empty but reported the kubernetes provider unavailable on a cluster that exists - unreachable reported as empty: $(grep -m1 -E 'Provider (configuration not evaluable|unavailable)' <<< "$PLAN_OUT")"
fi
for obj in "namespace app" "serviceaccount app -n app" "configmap app-config -n app" "deployment app -n app"; do
  # shellcheck disable=SC2086  # obj is a kind, a name and an optional namespace flag
  kc "$FLOCI_NS" get $obj -o name >/dev/null || fail "kubectl does not find $obj in the k3s container"
done
gauntlet_stage test_plan pass "the plan with no state file is empty, with the kubernetes provider configured from aws_eks_cluster.this read live and no provider-unavailable warning; all ${CLUSTER_N} cluster-leg identities (NAMESPACE/NAME) confirmed with kubectl"

# ══════════════════════════════════════════════════════════════════════
# test_apply: the empty plan applies as a genuine no-op, both legs.
# ══════════════════════════════════════════════════════════════════════
gauntlet_begin_stage test_apply
gauntlet_estate_objects "$ESTATE" awsl || fail "could not count the AWS leg before the no-op apply"
BEFORE_AWS="$GAUNTLET_ESTATE_N"
BEFORE_K8S="$(labelled_count "$FLOCI_NS" "$ESTATE")" || fail "could not count the cluster leg before the no-op apply"
NOOP_OUT="$(cd "$ADOPTED" && with_endpoint "$ENDPOINT" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; NOOP_RC=$?
[ "$NOOP_RC" -eq 0 ] || { printf '%s\n' "$NOOP_OUT" | tail -40; fail "the no-op apply exited $NOOP_RC"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$NOOP_OUT" \
  || { grep -E 'Apply complete' <<< "$NOOP_OUT"; fail "the no-op apply was not a genuine no-op"; }
gauntlet_estate_objects "$ESTATE" awsl || fail "could not count the AWS leg after the no-op apply"
AFTER_K8S="$(labelled_count "$FLOCI_NS" "$ESTATE")" || fail "could not count the cluster leg after the no-op apply"
[ "$GAUNTLET_ESTATE_N" = "$BEFORE_AWS" ] || fail "the AWS leg's marked object count changed across a no-op apply: $BEFORE_AWS -> $GAUNTLET_ESTATE_N"
[ "$AFTER_K8S" = "$BEFORE_K8S" ] || fail "the cluster leg's labelled object count changed across a no-op apply: $BEFORE_K8S -> $AFTER_K8S"
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed); AWS leg unchanged at $BEFORE_AWS marked objects (AWS CLI), cluster leg unchanged at $BEFORE_K8S labelled objects (kubectl in the k3s container)"

# ══════════════════════════════════════════════════════════════════════
# greenfield: choudoufu applies the same configuration from an empty
# account; the cluster does not exist when the plan starts.
# ══════════════════════════════════════════════════════════════════════
gauntlet_begin_stage greenfield
start_floci "$FLOCI_GREEN_NAME" "$FLOCI_GREEN_PORT" "$FLOCI_GREEN_NS" || fail "the greenfield floci did not come up healthy (eks) at $GREEN_ENDPOINT"
mkdir -p "$GREEN"
reference_eks_main_tf "$GREEN_ESTATE" "$(aws_provider_block)" "$PREFIX" "$ACCESS_API" "$REGION" > "$GREEN/main.tf" \
  || fail "could not write the greenfield root"
with_endpoint "$GREEN_ENDPOINT" "$TOFU" -chdir="$GREEN" init -input=false -no-color > "$WORK/green_init.out" 2>&1 \
  || { tail -30 "$WORK/green_init.out"; fail "greenfield choudoufu init failed"; }
GREEN_APPLY="$(cd "$GREEN" && with_endpoint "$GREEN_ENDPOINT" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; GREEN_RC=$?
[ "$GREEN_RC" -eq 0 ] || { printf '%s\n' "$GREEN_APPLY" | tail -40; fail "the greenfield apply exited $GREEN_RC - a cluster that does not exist yet must read as empty, stock's order, not refuse"; }
grep -qE "Apply complete! Resources: ${TOTAL_N} added" <<< "$GREEN_APPLY" \
  || { grep -E 'Apply complete' <<< "$GREEN_APPLY"; fail "the greenfield apply did not create exactly ${TOTAL_N} resources"; }
GREEN_INVENTORY="$(inventory awsg "$FLOCI_GREEN_NS")" || fail "could not read the greenfield inventory"
EXPECTED="$STOCK_INVENTORY"
if [ "${BREAK:-}" = "1" ]; then
  EXPECTED="$(grep -v '^configmap' <<< "$STOCK_INVENTORY")"
fi
if [ "$(sort <<< "$EXPECTED")" = "$(sort <<< "$GREEN_INVENTORY")" ]; then
  [ "${BREAK:-}" = "1" ] && fail "BREAK=1: the config map was dropped from the expected inventory and the comparison still held"
  GREEN_LABELLED="$(labelled_count "$FLOCI_GREEN_NS" "$GREEN_ESTATE")" || fail "could not count greenfield labels"
  [ "$GREEN_LABELLED" = "$CLUSTER_N" ] || fail "the greenfield cluster leg carries $GREEN_LABELLED tofu-estate=$GREEN_ESTATE label(s), want ${CLUSTER_N}"
  REPLAN="$(cd "$GREEN" && with_endpoint "$GREEN_ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$REPLAN" | tail -30; fail "the greenfield replan failed"; }
  grep -qF "No changes." <<< "$REPLAN" || fail "the greenfield replan is not empty: $(grep -m1 -E '^Plan:' <<< "$REPLAN")"
  gauntlet_stage greenfield pass "choudoufu applied ${TOTAL_N} resources into an empty floci-eks account, the kubernetes provider configured only once aws_eks_cluster.this existed; $(wc -l <<< "$GREEN_INVENTORY" | tr -d ' ') inventory lines (AWS CLI and kubectl) match stock's cold deploy object by object; ${CLUSTER_N} cluster-leg objects labelled; the replan is empty"
else
  if [ "${BREAK:-}" = "1" ]; then
    gauntlet_stage greenfield pass "BREAK=1 control: with the config map dropped from the expected inventory the object-by-object comparison correctly fails to hold"
  else
    diff <(sort <<< "$EXPECTED") <(sort <<< "$GREEN_INVENTORY") | sed 's/^/  /'
    fail "the greenfield inventory differs from stock's cold deploy (diff above)"
  fi
fi

# ══════════════════════════════════════════════════════════════════════
# Not built yet for this estate.
# ══════════════════════════════════════════════════════════════════════
for s in drift_reconverge day2_rename day2_remove day2_count day2_replace day2_crash day2_teardown plan_approval strict; do
  gauntlet_stage "$s" not_run "not built for reference-eks yet: #1113 shipped cold_deploy, migrate, test_plan, test_apply and greenfield, the stages that measure the provider block against the cluster it creates; the day-2 stages are the estate's next unit"
done

gauntlet_end
log "=== reference-eks: five stages run on floci-eks; the rest reported not_run ==="
