#!/usr/bin/env bash
# live/managed-k8s/harness.sh: record_store "kubernetes" against a REAL
# managed control plane (EKS, GKE or AKS). GitHub issue #1524.
#
# THIS IS A PAID RUN AND IT IS THE MAINTAINER'S. It talks to a managed
# cluster the maintainer has stood up and pays for, and to that cloud's API
# with the maintainer's credentials. No agent runs it, and no workflow runs
# it: CLAUDE.md, "Heavy and paid runs are the maintainer's, by hand". It
# creates no cluster and deletes none; it creates two namespaces in the one
# it is pointed at, and removes them on the way out.
#
# ---------------------------------------------------------------------------
# The command
# ---------------------------------------------------------------------------
#
#   EKS:
#     PROVIDER=eks CLUSTER=<name> REGION=<region> KUBE_CONTEXT=<context> \
#       bash live/managed-k8s/harness.sh
#
#   GKE:
#     PROVIDER=gke CLUSTER=<name> PROJECT=<project> LOCATION=<region-or-zone> \
#       KUBE_CONTEXT=<context> bash live/managed-k8s/harness.sh
#
#   AKS:
#     PROVIDER=aks CLUSTER=<name> RESOURCE_GROUP=<rg> SUBSCRIPTION_ID=<sub> \
#       KUBE_CONTEXT=<context> bash live/managed-k8s/harness.sh
#
# Run it from the repository root, with the cloud's ordinary credentials in
# the environment (AWS_PROFILE / `gcloud auth application-default login` /
# `az login`) and a kubeconfig whose KUBE_CONTEXT reaches that cluster
# through its usual exec plugin (`aws eks update-kubeconfig`, `gcloud
# container clusters get-credentials`, `az aks get-credentials` all write
# one). The identity needs cluster-admin on the cluster for the namespaces,
# and the one read-only cloud permission the provider read asks for:
# eks:DescribeCluster, container.clusters.get, or
# Microsoft.ContainerService/managedClusters/read.
#
# Optional:
#   KUBECONFIG=<path>   which kubeconfig KUBE_CONTEXT is in (kubectl's default
#                       otherwise)
#   ESTATE=<name>       the estate name; default mk8s-<provider>-<random>
#   OUT=<dir>           where results and logs go; default
#                       ./managed-k8s-<provider>-<timestamp>
#   TOFU_BIN=<path>     a choudoufu binary to use instead of building one
#   KEEP=1              leave the namespaces behind for inspection
#
# There is deliberately no default for KUBE_CONTEXT, PROVIDER or CLUSTER: a
# harness that fell back to the current context would report on whichever
# cluster happened to be selected.
#
# ---------------------------------------------------------------------------
# What it measures, which is what #1524 asked for
# ---------------------------------------------------------------------------
#
#   1. The Store contract suite (internal/live/staterecord's
#      TestKubernetesStore*, the one kind runs in claim 29's step 1) against
#      the managed API server: conditional writes under resourceVersion,
#      authenticated through the cloud's exec plugin. The suite's output is
#      also searched for 429 / throttling, the managed API server's rate
#      limits being one of the things the issue says nobody has measured.
#   2. `choudoufu live-cluster -json` with NO control_plane block, which is
#      what an estate written before #1524 gets. On EKS the exec plugin is
#      recognised and the provider is asked anyway; on GKE and AKS this is the
#      old NOT CHECKED, recorded as the baseline.
#   3. `choudoufu live-cluster -json` WITH the control_plane block. Every
#      assertion's verdict is recorded. encryption_at_rest must have been
#      ANSWERED by the provider (ok or fail): a NOT CHECKED here means the
#      provider read did not happen or was not believed, and the finding's
#      text says which, so the harness fails and prints it.
#   4. An estate applies through the store on this cluster: a namespace, a
#      ConfigMap and a terraform_data (a record-backed type, so the run
#      cannot finish without the store), with allow_insecure naming exactly
#      the assertions step 3 did not find OK - so the run measures the store,
#      and the waiver is printed on every run as it should be. Its record
#      Secrets are listed, and a second plan must say No changes.
#   5. Destroy, then remove the namespaces.
#
# The results land in $OUT/results.tsv (one line per step: step, verdict,
# detail) and $OUT/assertions.tsv (one line per contract assertion per
# live-cluster run: run, setting, verdict, found). Paste both into #1524.
#
# Exit 0 when every step passed; the assertions' own verdicts are recorded,
# not required, because a cluster that fails one is a finding and not a
# broken harness.

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

die() { printf 'harness: %s\n' "$*" >&2; exit 2; }
need() { [ -n "${!1:-}" ] || die "$1 is required; see the header of $0 for the command"; }

need PROVIDER
need CLUSTER
need KUBE_CONTEXT
case "$PROVIDER" in
  eks) CP_ARGS="name = \"$CLUSTER\""
       [ -n "${REGION:-}" ] && CP_ARGS="$CP_ARGS
        region = \"$REGION\"" ;;
  gke) need PROJECT; need LOCATION
       CP_ARGS="name     = \"$CLUSTER\"
        project  = \"$PROJECT\"
        location = \"$LOCATION\"" ;;
  aks) need RESOURCE_GROUP; need SUBSCRIPTION_ID
       CP_ARGS="name            = \"$CLUSTER\"
        resource_group  = \"$RESOURCE_GROUP\"
        subscription_id = \"$SUBSCRIPTION_ID\"" ;;
  *) die "PROVIDER must be eks, gke or aks, not \"$PROVIDER\"" ;;
esac

for tool in kubectl jq go; do
  command -v "$tool" >/dev/null 2>&1 || die "$tool is not on PATH"
done

ESTATE="${ESTATE:-mk8s-$PROVIDER-$(LC_ALL=C tr -dc 'a-z0-9' </dev/urandom | head -c 6)}"
RECORDS_NS="tofu-records-$ESTATE"
APP_NS="$ESTATE-app"
OUT="${OUT:-$PWD/managed-k8s-$PROVIDER-$(date -u +%Y%m%dT%H%M%SZ)}"
mkdir -p "$OUT"
RESULTS="$OUT/results.tsv"
ASSERTIONS="$OUT/assertions.tsv"
: > "$RESULTS"
printf 'run\tsetting\tverdict\tfound\n' > "$ASSERTIONS"

KC="$OUT/kubeconfig"
kc() { kubectl --kubeconfig "$KC" "$@"; }

failed=0
record() { # record <step> <PASS|FAIL> <detail>
  printf '%s\t%s\t%s\n' "$1" "$2" "$3" >> "$RESULTS"
  printf '[%s] %s: %s\n' "$2" "$1" "$3"
  [ "$2" = PASS ] || failed=1
}

finished=0
# shellcheck disable=SC2329 # invoked by the EXIT trap
cleanup() {
  rc=$?
  if [ "${KEEP:-0}" != 1 ] && [ -s "$KC" ]; then
    if [ -d "$OUT/app/.terraform" ]; then
      ( cd "$OUT/app" && "$TOFU" destroy -auto-approve -input=false -no-color >"$OUT/destroy-on-exit.log" 2>&1 ) || true
    fi
    kc delete namespace "$APP_NS" "$RECORDS_NS" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  fi
  if [ "$finished" != 1 ]; then
    echo "harness: stopped before its last step; results so far are in $RESULTS" >&2
    exit 1
  fi
  exit "$rc"
}
trap cleanup EXIT

echo "=== record_store \"kubernetes\" on $PROVIDER cluster $CLUSTER (context $KUBE_CONTEXT), estate $ESTATE ==="
echo "    results: $OUT"

# One self-contained kubeconfig for the named context, so every client below
# - kubectl, the Go suite, choudoufu, the kubernetes provider - reaches the
# same cluster whatever the current context is.
kubectl config view --minify --flatten --context "$KUBE_CONTEXT" > "$KC" 2>"$OUT/kubeconfig.err" \
  || die "could not read context $KUBE_CONTEXT: $(cat "$OUT/kubeconfig.err")"
chmod 600 "$KC"
SERVER="$(kubectl --kubeconfig "$KC" config view -o jsonpath='{.clusters[0].cluster.server}')"
echo "    API server: $SERVER"

# --- 0. preflight --------------------------------------------------------
if V="$(kc version -o json 2>&1)"; then
  record preflight PASS "reached $SERVER, server $(jq -r '.serverVersion.gitVersion' <<<"$V" 2>/dev/null)"
else
  record preflight FAIL "kubectl could not reach $SERVER: $V"
  finished=1; exit 1
fi
case "$PROVIDER" in
  eks) command -v aws >/dev/null && aws sts get-caller-identity --output json > "$OUT/cloud-identity.json" 2>&1 || true ;;
  gke) command -v gcloud >/dev/null && gcloud auth list --format=json > "$OUT/cloud-identity.json" 2>&1 || true ;;
  aks) command -v az >/dev/null && az account show -o json > "$OUT/cloud-identity.json" 2>&1 || true ;;
esac

if [ -n "${TOFU_BIN:-}" ]; then
  TOFU="$TOFU_BIN"
else
  TOFU="$OUT/choudoufu"
  ( cd "$ROOT" && env -u PWD go build -o "$TOFU" . ) > "$OUT/build.log" 2>&1 \
    || { record build FAIL "go build failed, see $OUT/build.log"; finished=1; exit 1; }
fi

kc create namespace "$RECORDS_NS" >/dev/null 2>&1 \
  || { record namespaces FAIL "could not create $RECORDS_NS"; finished=1; exit 1; }
kc create namespace "$APP_NS" >/dev/null 2>&1 \
  || { record namespaces FAIL "could not create $APP_NS"; finished=1; exit 1; }
record namespaces PASS "created $RECORDS_NS and $APP_NS"

# --- 1. the Store contract suite ------------------------------------------
SUITE_LOG="$OUT/conformance.log"
( cd "$ROOT" && CHOUDOUFU_K8S_TEST=1 \
    CHOUDOUFU_K8S_RECORD_KUBECONFIG="$KC" CHOUDOUFU_K8S_RECORD_NAMESPACE="$RECORDS_NS" \
    env -u PWD go test ./internal/live/staterecord -run 'TestKubernetesStore' -count=1 -v ) > "$SUITE_LOG" 2>&1
SUITE_RC=$?
PASSES="$(grep -cE '^( {4})*--- PASS' "$SUITE_LOG" || true)"
FAILS="$(grep -c -- '--- FAIL' "$SUITE_LOG" || true)"
SKIPS="$(grep -c -- '--- SKIP' "$SUITE_LOG" || true)"
THROTTLED="$(grep -ciE '429|too many requests|throttl|client-side throttling' "$SUITE_LOG" || true)"
if [ "$SUITE_RC" = 0 ] && [ "$FAILS" = 0 ] && [ "$PASSES" -gt 0 ] && [ "$SKIPS" = 0 ]; then
  record conformance PASS "$PASSES PASS lines, 0 FAIL, 0 SKIP; $THROTTLED throttling line(s)"
else
  record conformance FAIL "rc=$SUITE_RC, $PASSES PASS, $FAILS FAIL, $SKIPS SKIP, $THROTTLED throttling line(s); see $SUITE_LOG"
fi

# --- 2 and 3. live-cluster, without and with the control_plane block -------
write_cluster_dir() { # write_cluster_dir <dir> <with-control-plane:0|1> <allow_insecure HCL or "">
  mkdir -p "$1"
  local cp="" waive=""
  if [ "$2" = 1 ]; then
    cp="      control_plane \"$PROVIDER\" {
        $CP_ARGS
      }"
  fi
  [ -n "$3" ] && waive="      allow_insecure = $3"
  cat > "$1/versions.tf" <<TF
terraform {
  required_version = ">= 1.5.0"

  live {
    estate = "$ESTATE"

    record_store "kubernetes" {
      namespace   = "$RECORDS_NS"
      config_path = "$KC"
$waive
$cp
    }
  }

  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "= 3.2.1"
    }
  }
}

provider "kubernetes" {
  config_path = "$KC"
}
TF
}

live_cluster() { # live_cluster <run name> <dir>
  local json="$OUT/live-cluster-$1.json"
  ( cd "$2" && "$TOFU" live-cluster -json -no-color ) > "$json" 2>"$OUT/live-cluster-$1.err"
  local rc=$?
  if ! jq -e '.settings' "$json" >/dev/null 2>&1; then
    record "live-cluster-$1" FAIL "rc=$rc and no JSON report: $(head -c 2000 "$OUT/live-cluster-$1.err")"
    return 1
  fi
  jq -r --arg run "$1" '.settings[] | [$run, .setting, .verdict, (.found | gsub("[\t\n]"; " "))] | @tsv' "$json" >> "$ASSERTIONS"
  jq -r '.settings[] | "      \(.setting): \(.verdict)"' "$json"
  record "live-cluster-$1" PASS "rc=$rc, correct=$(jq -r .correct "$json"), server $(jq -r .server "$json")"
}

BASE_DIR="$OUT/baseline"
write_cluster_dir "$BASE_DIR" 0 ""
live_cluster baseline "$BASE_DIR" || true

CP_DIR="$OUT/with-control-plane"
write_cluster_dir "$CP_DIR" 1 ""
if live_cluster control-plane "$CP_DIR"; then
  ENC_VERDICT="$(jq -r '.settings[] | select(.setting=="encryption_at_rest") | .verdict' "$OUT/live-cluster-control-plane.json")"
  ENC_FOUND="$(jq -r '.settings[] | select(.setting=="encryption_at_rest") | .found' "$OUT/live-cluster-control-plane.json")"
  case "$ENC_VERDICT" in
    ok|fail) record encryption-from-provider PASS "$ENC_VERDICT: $ENC_FOUND" ;;
    *)       record encryption-from-provider FAIL "$ENC_VERDICT, so the provider did not answer or was not believed: $ENC_FOUND" ;;
  esac
  # What a run would refuse or warn on, as an allow_insecure list, so the
  # apply below measures the store and says out loud what it waived.
  WAIVE="$(jq -c '[.settings[] | select(.verdict != "ok") | .setting]' "$OUT/live-cluster-control-plane.json")"
else
  WAIVE='["namespace_access","read_isolation","encryption_at_rest","estate_boundary"]'
fi
[ "$WAIVE" = "[]" ] && WAIVE=""

# --- 4. an estate applies through the store -------------------------------
APP="$OUT/app"
write_cluster_dir "$APP" 1 "$WAIVE"
cat > "$APP/main.tf" <<TF
resource "kubernetes_config_map" "app" {
  metadata {
    name      = "managed-k8s-harness"
    namespace = "$APP_NS"
  }
  data = { greeting = "hello" }
}

resource "terraform_data" "effect" {
  input = "v1"
}
TF
if ( cd "$APP" && "$TOFU" init -input=false -no-color ) > "$OUT/init.log" 2>&1; then
  if ( cd "$APP" && "$TOFU" apply -auto-approve -input=false -no-color ) > "$OUT/apply.log" 2>&1 \
     && grep -q 'Apply complete! Resources: 2 added' "$OUT/apply.log"; then
    record apply PASS "2 added; waiver ${WAIVE:-none}; $(grep -ciE 'allow_insecure|waiv' "$OUT/apply.log" || true) waiver line(s) printed"
  else
    record apply FAIL "see $OUT/apply.log: $(tail -n 20 "$OUT/apply.log" | tr '\n' ' ')"
  fi
  RECS="$(kc get secrets -n "$RECORDS_NS" -l "tofu-estate=$ESTATE" -o name 2>&1)"
  if grep -q '^secret/tofu-record-' <<<"$RECS"; then
    record records-are-secrets PASS "$(grep -c '^secret/tofu-record-' <<<"$RECS") record Secret(s) in $RECORDS_NS"
  else
    record records-are-secrets FAIL "no tofu-record- Secret carries tofu-estate=$ESTATE: $RECS"
  fi
  ( cd "$APP" && "$TOFU" plan -input=false -no-color ) > "$OUT/replan.log" 2>&1
  if grep -q 'No changes' "$OUT/replan.log"; then
    record replan PASS "No changes"
  else
    record replan FAIL "the second plan proposed changes or failed; see $OUT/replan.log"
  fi
  # --- 5. destroy --------------------------------------------------------
  if ( cd "$APP" && "$TOFU" destroy -auto-approve -input=false -no-color ) > "$OUT/destroy.log" 2>&1; then
    record destroy PASS "$(grep -E 'Destroy complete' "$OUT/destroy.log" | head -n 1)"
  else
    record destroy FAIL "see $OUT/destroy.log"
  fi
else
  record init FAIL "see $OUT/init.log"
fi

TOTAL_THROTTLE="$(cat "$OUT"/*.log 2>/dev/null | grep -ciE '429|too many requests|throttl' || true)"
record throttling-observed PASS "$TOTAL_THROTTLE line(s) mentioning 429 or throttling across every log (a count, not a gate)"

echo
echo "=== results ($RESULTS) ==="
column -t -s $'\t' "$RESULTS" 2>/dev/null || cat "$RESULTS"
echo
echo "=== assertions ($ASSERTIONS) ==="
cut -f1-3 "$ASSERTIONS" | column -t -s $'\t' 2>/dev/null || cut -f1-3 "$ASSERTIONS"

finished=1
exit "$failed"
