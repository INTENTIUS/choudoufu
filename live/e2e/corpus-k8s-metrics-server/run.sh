#!/usr/bin/env bash
# corpus-k8s-metrics-server: kubernetes/k8s.io's own metrics-server
# installation (#1880, epic #1885), crossed on the kind substrate. The file
# infra/aws/terraform/kops-infra-ci/metrics-server.tf at commit
# 4046258e7d54fdc156e2f39ad8da51b57be88f55 (live/corpus-manifest.json's
# k8s-io pin, Apache-2.0), cut out of its root on its own: nine
# hashicorp/kubernetes objects, all on the provider's old non-_v1 type names
# (two ClusterRoles, two ClusterRoleBindings, a RoleBinding, a
# ServiceAccount, a Deployment, a Service and an APIService), every
# namespaced one in kube-system. Real image
# (registry.k8s.io/metrics-server/metrics-server), pulled by kind's node.
#
# Three surfaces the lane had not touched before this estate:
#   - kubernetes_api_service: v1beta1.metrics.k8s.io, a cluster-scoped
#     aggregated API backed by the Service. Once its backend answers, the
#     API server itself serves metrics.k8s.io; when it does not, API
#     discovery reports that group failed and every other group answers.
#     The sweep could not list it at all before this unit: KindOfType read
#     the type as "ApiService" where the server lists "APIService", under a
#     group client-go's scheme does not register (internal/live/kubesweep).
#   - writes into kube-system, a namespace the estate does not own and must
#     never delete. day2_teardown and the stock destroy on B both have to
#     leave every object kube-system held before cold_deploy in place.
#   - the deprecated non-_v1 RBAC type names, beside reference-k8s's
#     non-_v1 ConfigMaps.
#
# Deltas, each asserted so a moved pin fails loudly rather than silently:
#   1. only metrics-server.tf is taken. The rest of kops-infra-ci (VPC, EKS,
#      IAM, ECR, S3 and the aws providers) stays out, and with it the
#      provider "kubernetes" block that reads an EKS cluster's endpoint;
#      providers.tf here is written by the script, with the lane's
#      hashicorp/kubernetes pin (live/oracle-versions.json) in place of the
#      root's own 2.38 constraint and an empty provider block, so
#      KUBE_CONFIG_PATH names the run's kind cluster;
#   2. metrics-server's container gains --kubelet-insecure-tls, because a
#      kind node's kubelet serves a self-signed certificate;
#   3. choudoufu's roots get a live block in providers.tf.
# And the additions every kind estate makes, each in a file of its own and
# each destroyed with the rest: day2_count's two-instance count ConfigMap,
# day2_crash's Secret/ConfigMap pair, and the library's day2_replace and
# crash-rename blocks. They live in kube-system too, since that is the only
# namespace the estate writes into.
#
# .corpus is read, never written: the file is copied out per run. Two kind
# clusters, both created for the run: A holds the estate (stock cold-deploys
# it, choudoufu adopts it and runs every day-2 stage, tears it down, then
# applies the same shape fresh with a live block); B is the oracle, where
# stock applies the identical root and every day-2 change itself.
#
#   go run ./tools/gauntlet run corpus-k8s-metrics-server   # one estate, local kind clusters
#   bash live/e2e/corpus-k8s-metrics-server/run.sh
#
# Needs `just corpus-fetch` first, then kind, kubectl, terraform (the stock
# binary), python3 and Docker on PATH, and network for the image.
#
# Env overrides:
#   TOFU_BIN       path to a prebuilt choudoufu binary; skips the `go build`.
#   BREAK          set to 1 to run drift_reconverge's, day2_rename's and
#                  greenfield's negative controls: a second object tampered
#                  (the single-object assertion must fail); the
#                  ServiceAccount's own metadata.name changed (the zero-churn
#                  assertion must fail); the APIService dropped from
#                  greenfield's expected inventory (the match must fail).
#   BREAK_REMOVE   keep the APIService block; no destroy may be proposed.
#   BREAK_COUNT    assert the wrong instance was destroyed on the scale-down.
#   BREAK_APPROVAL apply the saved plan after the world moved and expect success.
#   BREAK_REPLACE  recreate the renamed ConfigMap's old object after
#                  day2_replace's apply; the next plan must propose
#                  destroying it.
#   BREAK_CRASH    after the same real interrupt, assert nothing is proposed;
#                  must fail.
#   BREAK_CRASH_UNBOUND
#                  strip the tofu-estate label off the object the interrupted
#                  apply did create; the recovery check must fail.
#   BREAK_TEARDOWN delete one of kube-system's own ConfigMaps before the
#                  teardown; the "kube-system left as it was" check must fail.
#   BREAK_STRICT   turn secrets back to "store"; the refusal must vanish.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
source "$ROOT/live/e2e/lib/gauntlet.sh"

# The shared provider plugin cache, and the cross-process lock real terraform
# needs in order to use it safely (#1300). live/e2e/lib/gauntlet.sh carries the
# measured reasons for both; this is the only place a script chooses either.
gauntlet_plugin_cache
ESTATE="corpus-k8s-metrics-server"
NS="kube-system"
APISVC="v1beta1.metrics.k8s.io"
# Every kind the estate's objects (and the stage additions) can be, for the
# labelled-object count. configmaps and secrets are the additions only.
KINDS="clusterroles clusterrolebindings rolebindings serviceaccounts apiservices deployments services configmaps secrets"
# What "kube-system left as it was" compares: every name of these kinds in
# kube-system, plus every cluster-scoped RBAC object and APIService, read
# before cold_deploy and again after each destroy.
BASELINE_NS_KINDS="configmaps serviceaccounts services deployments daemonsets roles rolebindings"
BASELINE_CLUSTER_KINDS="clusterroles clusterrolebindings apiservices"
PIN_COMMIT="4046258e7d54fdc156e2f39ad8da51b57be88f55"
SRC="$ROOT/.corpus/k8s-io/infra/aws/terraform/kops-infra-ci"
WORK="$(mktemp -d)"
STOCK="$WORK/stock"; ADOPTED="$WORK/adopted"; ORACLE="$WORK/oracle"; GREEN="$WORK/green"
KCA="$WORK/a.kubeconfig"; KCB="$WORK/b.kubeconfig"
CLUSTER_A="${GAUNTLET_KIND_PREFIX:-chdf}-ms-a-$$"; CLUSTER_B="${GAUNTLET_KIND_PREFIX:-chdf}-ms-b-$$"  # GAUNTLET_KIND_PREFIX: lets concurrent workers name their own clusters
export TF_IN_AUTOMATION=1
log() { printf '%s\n' "$*"; }

CURRENT_STAGE=""
fail() {
  printf 'FAIL: %s\n' "$*" >&2
  if [ -n "$CURRENT_STAGE" ]; then gauntlet_stage "$CURRENT_STAGE" fail "$*"; fi
  exit 1
}
cleanup() {
  gauntlet_kind_down "$CLUSTER_A"
  gauntlet_kind_down "$CLUSTER_B"
  rm -rf "$WORK"
}
trap cleanup EXIT
gauntlet_begin

# ── 0. tools and the pinned source ───────────────────────────────────────
log "=== 0. tools ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v terraform >/dev/null 2>&1 || fail "the terraform binary is not on PATH - needed as the stock oracle"
command -v kind >/dev/null 2>&1 || fail "kind is not on PATH (brew install kind)"
command -v kubectl >/dev/null 2>&1 || fail "kubectl is not on PATH"
command -v python3 >/dev/null 2>&1 || fail "python3 is not on PATH"
[ -f "$SRC/metrics-server.tf" ] || fail "$SRC/metrics-server.tf is missing - run \`just corpus-fetch\` first"
PIN="$(git -C "$ROOT/.corpus/k8s-io" rev-parse HEAD 2>/dev/null)"
[ "$PIN" = "$PIN_COMMIT" ] || fail "the fetched k8s.io is at $PIN, not the pinned $PIN_COMMIT - run \`just corpus-fetch\`"

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

# day2_crash needs a build with e2eTestingFeatures set, the same ldflags
# gate TOFU_E2E_APPLY_RESOURCE_PANIC sits behind, so the engine's own
# TOFU_E2E_APPLY_RESOURCE_INTERRUPT hook (internal/command/
# apply_e2etesting_crash.go) is reachable. Built from THIS tree's source
# whatever $TOFU came from; identical to $TOFU everywhere else, because the
# hook is a no-op unless the variable names an applied address.
mkdir -p "$WORK/bin"
TOFU_CRASH="$WORK/bin/choudoufu-e2e"
( cd "$ROOT" && env -u PWD go build -ldflags="-X 'main.e2eTestingFeatures=yes'" -o "$TOFU_CRASH" ./cmd/choudoufu ) \
  || fail "go build -ldflags e2eTestingFeatures ./cmd/choudoufu failed"
log "  built $TOFU_CRASH (e2eTestingFeatures=yes, for day2_crash's interrupt)"

# The hashicorp/kubernetes requirement is live/oracle-versions.json's
# kubernetes_provider_version, read once behind one fail (#1252).
K8S_REQUIRED_PROVIDER="$(gauntlet_kubernetes_required_provider)" \
  || fail "could not read the hashicorp/kubernetes pin from live/oracle-versions.json"

# ── the root, cut out of .corpus with its deltas ─────────────────────────
# write_root copies metrics-server.tf into $1 (the root directory), applies
# delta 2, and writes providers.tf (delta 1, plus delta 3 when $2 is "live").
write_root() {
  local dst="$1" mode="$2"
  rm -rf "$dst"; mkdir -p "$dst"
  cp "$SRC/metrics-server.tf" "$dst/metrics-server.tf" || fail "could not copy metrics-server.tf out of .corpus"
  local tf="$dst/metrics-server.tf"
  [ "$(grep -c '^resource "kubernetes_' "$tf")" = "9" ] || fail "metrics-server.tf does not declare exactly nine kubernetes_ resources - the corpus pin has moved"
  grep -q '^provider\|^terraform' "$tf" && fail "metrics-server.tf carries a provider or terraform block of its own - the corpus pin has moved"
  perl -0777 -pi -e 's/("--metric-resolution=15s")\n/$1,\n            "--kubelet-insecure-tls" # delta 2: a kind node'"'"'s kubelet serves a self-signed certificate\n/' "$tf"
  grep -q '"--kubelet-insecure-tls"' "$tf" || fail "delta 2 did not match metrics-server.tf (no \"--metric-resolution=15s\" line ending the args) - the corpus pin has moved"
  {
    cat <<EOF
# Written by live/e2e/corpus-k8s-metrics-server/run.sh (delta 1): the
# published root's providers.tf points the kubernetes provider at an EKS
# cluster through two aws data sources; this one names no cluster, so
# KUBE_CONFIG_PATH selects the run's kind cluster.
terraform {
  required_providers {
$K8S_REQUIRED_PROVIDER
  }
EOF
    if [ "$mode" = "live" ]; then cat <<EOF
  live {
    estate = "$ESTATE"
    record_store "local" {
      path = ".tofu-records"
    }
  }
EOF
    fi
    cat <<'EOF'
}

provider "kubernetes" {}
EOF
  } > "$dst/providers.tf"
  if [ "$mode" = "live" ]; then
    grep -q "estate = \"$ESTATE\"" "$dst/providers.tf" || fail "delta 3 did not land in providers.tf"
  fi
}

# ── cluster helpers ──────────────────────────────────────────────────────
# Every kubectl against a cluster goes through kca/kcb, which carry a
# request timeout so an API server that stops answering fails the stage
# instead of hanging the run.
KC_TIMEOUT="--request-timeout=60s"
kca() { kubectl --kubeconfig "$KCA" "$KC_TIMEOUT" "$@"; }
kcb() { kubectl --kubeconfig "$KCB" "$KC_TIMEOUT" "$@"; }
stock_a() { ( cd "$STOCK" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" terraform "$@" ); }
stock_b() { ( cd "$ORACLE" && KUBECONFIG="$KCB" KUBE_CONFIG_PATH="$KCB" terraform "$@" ); }
chdf_a() { local dir="$1"; shift; ( cd "$dir" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" "$TOFU" "$@" ); }
# shellcheck disable=SC2086 # KINDS is a word list on purpose
count_a() { KUBECONFIG="$KCA" gauntlet_kind_count "$ESTATE" $KINDS; }
exists_a() { kca get "$1" "$2" -n "$NS" >/dev/null 2>&1; }
# apisvc_available prints the APIService's Available condition on cluster
# $1's kubeconfig: True, False, or nothing when it does not exist.
apisvc_available() {
  kubectl --kubeconfig "$1" "$KC_TIMEOUT" get apiservice "$APISVC" -o jsonpath='{.status.conditions[?(@.type=="Available")].status}' 2>/dev/null
}
apisvc_is() { [ "$(apisvc_available "$1")" = "$2" ]; }
# metrics_served: the aggregated API answers through the API server.
metrics_served() { kubectl --kubeconfig "$1" "$KC_TIMEOUT" get --raw /apis/metrics.k8s.io/v1beta1 >/dev/null 2>&1; }
# kube_system_snapshot prints every name of BASELINE_NS_KINDS in
# kube-system and every name of BASELINE_CLUSTER_KINDS, one "kind/name" per
# line, sorted - what "left kube-system and the cluster's own objects as
# they were" compares, before cold_deploy and after each destroy. $2 is a
# list of "kind/name" lines to drop (the estate's own objects, which a
# snapshot taken while the estate is up would otherwise carry).
kube_system_snapshot() {
  local cfg="$1" k
  {
    for k in $BASELINE_NS_KINDS; do
      kubectl --kubeconfig "$cfg" "$KC_TIMEOUT" get "$k" -n "$NS" -o name 2>/dev/null
    done
    for k in $BASELINE_CLUSTER_KINDS; do
      kubectl --kubeconfig "$cfg" "$KC_TIMEOUT" get "$k" -o name 2>/dev/null
    done
  } | sort
}
# inventory prints the estate's nine objects on cluster $1, normalised to
# what the configuration declares - never labels, annotations or anything
# the server set - so stock's cold deploy and choudoufu's greenfield apply
# compare object by object. $2 is a "kind/name" to drop (BREAK's control).
inventory() {
  local cfg="$1" drop="${2:-}"
  KUBECONFIG="$cfg" DROP="$drop" NS="$NS" APISVC="$APISVC" python3 - <<'PY'
import json, os, subprocess
ns = os.environ["NS"]; drop = os.environ.get("DROP", ""); apisvc = os.environ["APISVC"]
def get(kind, name, namespaced=True):
    args = ["kubectl", "--request-timeout=60s", "get", kind, name, "-o", "json"] + (["-n", ns] if namespaced else [])
    out = subprocess.run(args, capture_output=True, text=True)
    return json.loads(out.stdout) if out.returncode == 0 else None
inv = {}
for name in ["system:aggregated-metrics-reader", "system:metrics-server"]:
    o = get("clusterrole", name, namespaced=False)
    inv["clusterrole/" + name] = None if o is None else {
        "rules": o.get("rules"),
        "aggregate": sorted(k for k in o["metadata"].get("labels", {}) if k.startswith("rbac.authorization.k8s.io/aggregate-to-"))}
for name in ["metrics-server:system:auth-delegator", "system:metrics-server"]:
    o = get("clusterrolebinding", name, namespaced=False)
    inv["clusterrolebinding/" + name] = None if o is None else {"roleRef": o.get("roleRef"), "subjects": o.get("subjects")}
o = get("rolebinding", "metrics-server-auth-reader")
inv["rolebinding/metrics-server-auth-reader"] = None if o is None else {"roleRef": o.get("roleRef"), "subjects": o.get("subjects")}
o = get("serviceaccount", "metrics-server")
inv["serviceaccount/metrics-server"] = None if o is None else {"exists": True}
o = get("apiservice", apisvc, namespaced=False)
inv["apiservice/" + apisvc] = None if o is None else {k: o["spec"].get(k) for k in
    ["group", "version", "groupPriorityMinimum", "versionPriority", "insecureSkipTLSVerify", "service"]}
o = get("deployment", "metrics-server")
if o is None:
    inv["deployment/metrics-server"] = None
else:
    t = o["spec"]["template"]["spec"]
    inv["deployment/metrics-server"] = {"selector": o["spec"].get("selector", {}).get("matchLabels"),
        "serviceAccount": t.get("serviceAccountName"), "priorityClass": t.get("priorityClassName"),
        "containers": [{"name": c["name"], "image": c["image"], "args": c.get("args"),
                        "ports": [{"name": p.get("name"), "containerPort": p.get("containerPort")} for p in c.get("ports", [])]}
                       for c in t["containers"]]}
o = get("service", "metrics-server")
inv["service/metrics-server"] = None if o is None else {"selector": o["spec"].get("selector"),
    "ports": [{"name": p.get("name"), "port": p.get("port"), "targetPort": p.get("targetPort"), "protocol": p.get("protocol")} for p in o["spec"].get("ports", [])]}
if drop:
    inv.pop(drop, None)
print(json.dumps(inv, sort_keys=True, indent=1))
PY
}

# ── 1. cold_deploy: stock stands the root up on A (and B, the oracle) ────
gauntlet_begin_stage cold_deploy
log "=== 1. cold_deploy: two kind clusters, stock terraform applies the cut-out root on each ==="
gauntlet_kind_up "$CLUSTER_A" "$KCA" || fail "kind cluster A ($CLUSTER_A) did not come up"
gauntlet_kind_up "$CLUSTER_B" "$KCB" || fail "kind cluster B ($CLUSTER_B) did not come up"
export KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA"
K8S_SERVER="$(kca version 2>/dev/null | gauntlet_k8s_server_version)"
log "  cluster A: $K8S_SERVER; cluster B: $CLUSTER_B"
kube_system_snapshot "$KCA" > "$WORK/baseline.a" || fail "could not read kube-system's own objects on A"
kube_system_snapshot "$KCB" > "$WORK/baseline.b" || fail "could not read kube-system's own objects on B"
[ -s "$WORK/baseline.a" ] || fail "kube-system's baseline on A is empty - the snapshot read nothing"
grep -qx "apiservice.apiregistration.k8s.io/$APISVC" "$WORK/baseline.a" && fail "$APISVC already exists on a fresh kind cluster - the node image now ships metrics-server, and this estate would be adopting the cluster's own object"
log "  kube-system baseline on A: $(wc -l < "$WORK/baseline.a" | tr -d ' ') object(s) of the kinds compared"
write_root "$STOCK" stock
write_root "$ORACLE" stock
( cd "$STOCK" && gauntlet_locked_init terraform init -input=false -no-color >/dev/null 2>&1 ) || fail "stock init failed on A"
COLD_OUT="$(stock_a apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$COLD_OUT" | tail -20; fail "stock cold deploy failed on A"; }
grep -qF "Apply complete! Resources: 9 added, 0 changed, 0 destroyed" <<< "$COLD_OUT" || { printf '%s\n' "$COLD_OUT" | tail -5; fail "stock cold deploy did not add exactly 9 objects on A"; }
STOCK_N="$(stock_a state list | wc -l | tr -d ' ')"
[ "$STOCK_N" = "9" ] || fail "stock's state holds $STOCK_N instances, want 9"
UNMARKED="$(count_a)"
[ "$UNMARKED" = "0" ] || fail "$UNMARKED object(s) already carry tofu-estate=$ESTATE after a plain stock apply"
gauntlet_wait_until 240 "$APISVC Available=True on A" -- apisvc_is "$KCA" True \
  || fail "the aggregated API $APISVC never reported Available on A after stock's apply"
metrics_served "$KCA" || fail "$APISVC reports Available but /apis/metrics.k8s.io/v1beta1 does not answer on A"
inventory "$KCA" > "$WORK/inventory.stock.json" || fail "could not read the cold-deployed inventory on A"
( cd "$ORACLE" && export KUBECONFIG="$KCB" KUBE_CONFIG_PATH="$KCB" && gauntlet_locked_init terraform init -input=false -no-color >/dev/null 2>&1 ) || fail "stock init failed on B"
B_COLD="$(stock_b apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$B_COLD" | tail -20; fail "stock cold deploy failed on B"; }
grep -qF "Apply complete! Resources: 9 added, 0 changed, 0 destroyed" <<< "$B_COLD" || fail "stock cold deploy did not add exactly 9 objects on B"
gauntlet_wait_until 240 "$APISVC Available=True on B" -- apisvc_is "$KCB" True \
  || fail "the aggregated API $APISVC never reported Available on B after stock's apply"
gauntlet_stage cold_deploy pass "9 objects (two ClusterRoles, two ClusterRoleBindings, a RoleBinding, a ServiceAccount, a Deployment and a Service in kube-system, and the APIService $APISVC) from plain terraform on k8s.io's own metrics-server.tf at $PIN_COMMIT plus its deltas (cut out of kops-infra-ci with a kind-pointed provider block, --kubelet-insecure-tls), against kind $K8S_SERVER; a real terraform.tfstate with 9 instances, zero tofu-estate labels; the APIService reports Available=True and /apis/metrics.k8s.io/v1beta1 answers through the API server; kube-system's own objects read beforehand as the teardown's baseline; the identical root cold-deployed by stock on a second cluster as every later stage's oracle"

# ── 2. migrate ────────────────────────────────────────────────────────────
gauntlet_begin_stage migrate
log "=== 2. migrate: live-import against the stock state file, read-only then -approve ==="
write_root "$ADOPTED" live
( chdf_a "$ADOPTED" init -input=false -no-color >/dev/null 2>&1 ) || fail "adopted init failed"
IMPORT_OUT="$(chdf_a "$ADOPTED" live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -no-color 2>&1)" || { printf '%s\n' "$IMPORT_OUT" | tail -20; fail "live-import (dry run) failed"; }
log "  dry run: $(grep -E 'resource instance\(s\) are eligible for stamping' <<< "$IMPORT_OUT" | head -1)"
APPROVE_OUT="$(chdf_a "$ADOPTED" live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -approve -no-color 2>&1)" || { printf '%s\n' "$APPROVE_OUT" | tail -20; fail "live-import -approve failed"; }
SUMMARY_LINE="$(grep -E 'resource\(s\) newly stamped' <<< "$APPROVE_OUT" | head -1 | sed 's/\.$//')"
log "  approve: ${SUMMARY_LINE:-no summary line}"
if grep -qF "9 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 0 skipped." <<< "$APPROVE_OUT"; then
  LABELLED="$(count_a)"
  [ "$LABELLED" = "9" ] || fail "live-import reported 9 stamped but $LABELLED object(s) carry tofu-estate=$ESTATE"
  APISVC_LABEL="$(kca get apiservice "$APISVC" -o jsonpath='{.metadata.labels.tofu-estate}')"
  [ "$APISVC_LABEL" = "$ESTATE" ] || fail "the APIService does not carry tofu-estate=$ESTATE after the stamp (reads '${APISVC_LABEL}')"
  gauntlet_stage migrate pass "9 of 9 stamped, 0 skipped, from the stock state file; every object carries tofu-estate=$ESTATE, read back with kubectl across the estate's kinds, the cluster-scoped APIService $APISVC among them"
else
  gauntlet_stage migrate fail "live-import -approve did not stamp all 9 cleanly: ${SUMMARY_LINE:-no summary line}"
fi

# ── 3. test_plan ──────────────────────────────────────────────────────────
gauntlet_begin_stage test_plan
log "=== 3. test_plan: choudoufu plan with no state file, identities read with kubectl ==="
PLAN_OUT="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$PLAN_OUT" | tail -20; fail "the post-migration plan failed"; }
IDS_OK=1
for spec in "deployment metrics-server" "service metrics-server" "serviceaccount metrics-server" "rolebinding metrics-server-auth-reader"; do
  read -r kind name <<< "$spec"; exists_a "$kind" "$name" || IDS_OK=0
done
for spec in "apiservice $APISVC" "clusterrole system:aggregated-metrics-reader" "clusterrole system:metrics-server" "clusterrolebinding metrics-server:system:auth-delegator" "clusterrolebinding system:metrics-server"; do
  read -r kind name <<< "$spec"; kca get "$kind" "$name" >/dev/null 2>&1 || IDS_OK=0
done
if grep -q "No changes." <<< "$PLAN_OUT" && [ "$IDS_OK" = "1" ]; then
  gauntlet_stage test_plan pass "the plan with no state file is empty; all nine identities confirmed present by name with kubectl - the five cluster-scoped ones (the APIService, both ClusterRoles, both ClusterRoleBindings) by NAME, the four in kube-system by NAMESPACE/NAME"
else
  # The plan itself, so the log names what it proposed (#1885).
  printf '%s\n' "$PLAN_OUT" | grep -vE '^[[:space:]]*$' | tail -120
  gauntlet_stage test_plan fail "the plan with no state file is not empty or an identity is missing (ids ok: $IDS_OK): $(grep -E '^Plan:|No changes' <<< "$PLAN_OUT" | head -1)"
  ADOPT_OUT="$(chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$ADOPT_OUT" | tail -20; fail "the converging apply failed"; }
  REPLAN_OUT="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)"
  grep -q "No changes." <<< "$REPLAN_OUT" || { printf '%s\n' "$REPLAN_OUT" | grep -vE '^[[:space:]]*$' | tail -120; fail "the replan after the converging apply is not empty: $(grep -E '^Plan:|No changes' <<< "$REPLAN_OUT" | head -1)"; }
fi

# ── 4. test_apply ─────────────────────────────────────────────────────────
gauntlet_begin_stage test_apply
log "=== 4. test_apply: apply the empty plan; the labelled-object count must not move ==="
BEFORE_N="$(count_a)"
NOOP_OUT="$(chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$NOOP_OUT" | tail -20; fail "the no-op apply failed"; }
grep -qF "Apply complete! Resources: 0 added, 0 changed, 0 destroyed" <<< "$NOOP_OUT" || { printf '%s\n' "$NOOP_OUT" | tail -5; fail "the no-op apply changed something"; }
[ "$BEFORE_N" = "$(count_a)" ] || fail "labelled-object count moved across a no-op apply: $BEFORE_N -> $(count_a)"
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed); objects carrying tofu-estate=$ESTATE unchanged at $BEFORE_N across the estate's kinds, counted with kubectl"

# ── 5. drift_reconverge: the aggregated API goes unavailable ─────────────
#
# The tamper is a rollout: metrics-server's --secure-port moves from 10250
# to 10251 on both clusters with a JSON patch whose test op fails loudly if
# the argument has moved. The pod template declares no readiness probe, so
# the new pod counts as ready the moment it runs, the old one is
# terminated, and the Service (target port "https", 10250) is left with a
# backend that refuses the connection: the APIService reports
# Available=False and API discovery reports metrics.k8s.io as a failed
# group. That is the condition the stage plans in. The sweep's discovery
# tolerates one failed group (internal/live/kubesweep/client.go), and the
# plan has to propose exactly the Deployment, as stock's does on B, with no
# "Kubernetes sweep unavailable" warning; the reconverging apply rolls the
# Deployment back and the APIService has to report Available again.
gauntlet_begin_stage drift_reconverge
log "=== 5. drift_reconverge: kubectl patch metrics-server's port on A and B; the aggregated API goes unavailable; stock's plan on B is the oracle ==="
PORT_PATCH='[{"op":"test","path":"/spec/template/spec/containers/0/args/1","value":"--secure-port=10250"},{"op":"replace","path":"/spec/template/spec/containers/0/args/1","value":"--secure-port=10251"}]'
kca patch deployment metrics-server -n "$NS" --type json -p "$PORT_PATCH" >/dev/null || fail "could not tamper metrics-server's --secure-port on A (did args[1] move?)"
kcb patch deployment metrics-server -n "$NS" --type json -p "$PORT_PATCH" >/dev/null || fail "could not tamper metrics-server's --secure-port on B"
gauntlet_wait_until 240 "$APISVC Available=False on A after the rollout" -- apisvc_is "$KCA" False \
  || fail "the aggregated API never reported unavailable on A after the port moved - this stage would plan against a healthy aggregated API"
gauntlet_wait_until 240 "$APISVC Available=False on B after the rollout" -- apisvc_is "$KCB" False \
  || fail "the aggregated API never reported unavailable on B after the port moved"
metrics_served "$KCA" && fail "/apis/metrics.k8s.io/v1beta1 still answers on A although $APISVC reports Available=False"
UNAVAIL_REASON="$(kca get apiservice "$APISVC" -o jsonpath='{.status.conditions[?(@.type=="Available")].reason}' 2>/dev/null)"
log "  $APISVC on A: Available=False (${UNAVAIL_REASON:-no reason})"
if [ "${BREAK:-}" = "1" ]; then
  kca label service metrics-server -n "$NS" kubernetes.io/cluster-service=tampered --overwrite >/dev/null || fail "BREAK: could not tamper the Service's declared label on A"
fi
ORACLE_PLAN="$(stock_b plan -detailed-exitcode -input=false -no-color 2>&1)"; ORACLE_RC=$?
[ "$ORACLE_RC" -eq 2 ] || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B after the tamper exited $ORACLE_RC, want 2"; }
grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$ORACLE_PLAN" || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B does not propose exactly one change"; }
grep -q "kubernetes_deployment.metrics" <<< "$ORACLE_PLAN" || fail "stock's plan on B does not name kubernetes_deployment.metrics"
DRIFT_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$DRIFT_PLAN" | tail -20; fail "the plan with the aggregated API unavailable failed"; }
grep -qF "Kubernetes sweep unavailable" <<< "$DRIFT_PLAN" && { printf '%s\n' "$DRIFT_PLAN" | grep -A3 'Kubernetes sweep unavailable'; fail "with one aggregated API group down the sweep reported itself unavailable - one failed discovery group cost the whole sweep"; }
if [ "${BREAK:-}" = "1" ]; then
  grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$DRIFT_PLAN" && fail "BREAK=1: two objects were tampered but the plan still proposes exactly one change"
  log "  BREAK=1: caught - with a second object tampered the plan is $(grep -E '^Plan:' <<< "$DRIFT_PLAN" | head -1)"
  ( chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: could not reconverge A"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: stock could not reconverge B"
  gauntlet_stage drift_reconverge pass "BREAK=1 control: with the Service's declared label tampered too the single-object assertion correctly fails to hold ($(grep -E '^Plan:' <<< "$DRIFT_PLAN" | head -1)); reconverged afterwards"
else
  grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$DRIFT_PLAN" || { printf '%s\n' "$DRIFT_PLAN" | tail -20; fail "the plan with the aggregated API unavailable does not propose exactly one change"; }
  grep -q "kubernetes_deployment.metrics" <<< "$DRIFT_PLAN" || fail "the plan does not name kubernetes_deployment.metrics"
  RECONV="$(chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$RECONV" | tail -20; fail "the reconverging apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$RECONV" || fail "the reconverging apply did not change exactly one object"
  [ "$(kca get deployment metrics-server -n "$NS" -o jsonpath='{.spec.template.spec.containers[0].args[1]}')" = "--secure-port=10250" ] || fail "metrics-server's --secure-port does not read 10250 after reconverging"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock could not reconverge B"
  gauntlet_wait_until 240 "$APISVC Available=True on A after the reconverging rollout" -- apisvc_is "$KCA" True \
    || fail "the aggregated API did not report Available again on A after the reconverging apply"
  metrics_served "$KCA" || fail "/apis/metrics.k8s.io/v1beta1 does not answer on A after the reconverging apply"
  grep -q "No changes." <<< "$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || fail "the replan after the reconverging apply is not empty"
  gauntlet_stage drift_reconverge pass "metrics-server's --secure-port moved 10250 -> 10251 with kubectl patch, a rollout after which the APIService $APISVC reported Available=False (${UNAVAIL_REASON:-no reason}) and /apis/metrics.k8s.io/v1beta1 stopped answering; planned in that state, choudoufu proposed exactly kubernetes_deployment.metrics (0 add, 1 change, 0 destroy) with no sweep-unavailable warning, the failed discovery group costing nothing, matching stock's own plan on the oracle cluster for the same tamper; apply changed 1, the argument reads back 10250, the APIService reported Available again and the aggregated API answers, the next plan empty. BREAK=1 tampers the Service's declared label too and the single-object assertion correctly fails"
fi

# ── 6. plan_approval ──────────────────────────────────────────────────────
gauntlet_begin_stage plan_approval
log "=== 6. plan_approval: the system:metrics-server ClusterRole gains a label in configuration; a saved plan, an out-of-band label elsewhere, a refusal ==="
add_reviewed() {
  perl -0777 -pi -e 's/(resource "kubernetes_cluster_role" "resource" \{\n  metadata \{\n    name = "system:metrics-server"\n)/$1    labels = { reviewed = "yes" }\n/' "$1/metrics-server.tf"
  grep -q 'reviewed = "yes"' "$1/metrics-server.tf" || fail "the reviewed-label edit did not match metrics-server.tf - the corpus pin has moved"
}
add_reviewed "$ADOPTED"; add_reviewed "$ORACLE"
P_PLAN="$(chdf_a "$ADOPTED" plan -out=approved.tfplan -input=false -no-color 2>&1)" || { printf '%s\n' "$P_PLAN" | tail -20; fail "plan -out failed"; }
grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$P_PLAN" || { printf '%s\n' "$P_PLAN" | tail -10; fail "the saved plan is not exactly one change"; }
kca label service metrics-server -n "$NS" stray=yes >/dev/null || fail "could not move the world (label service/metrics-server) on A"
P_APPLY="$(chdf_a "$ADOPTED" apply -input=false -no-color approved.tfplan 2>&1)"; P_RC=$?
if [ "${BREAK_APPROVAL:-}" = "1" ]; then
  [ "$P_RC" -eq 0 ] && fail "BREAK_APPROVAL=1: applying the saved plan after the world moved succeeded"
  log "  BREAK_APPROVAL=1: caught - the apply after the world moved exited $P_RC"
  kca label service metrics-server -n "$NS" stray- >/dev/null
  ( chdf_a "$ADOPTED" apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: the saved plan did not apply once the world was put back"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: stock could not apply the reviewed label on B"
  gauntlet_stage plan_approval pass "BREAK_APPROVAL=1 control: applying the saved plan after the world moved exited $P_RC (refused), so the stage's own Break line correctly fails; applied once the world was put back"
else
  [ "$P_RC" -eq 3 ] || { printf '%s\n' "$P_APPLY" | tail -20; fail "apply of the saved plan after the world moved exited $P_RC, want 3"; }
  grep -qF "The approved plan no longer matches the live system" <<< "$P_APPLY" || { printf '%s\n' "$P_APPLY" | tail -20; fail "the refusal does not carry its documented sentence"; }
  [ -z "$(kca get clusterrole system:metrics-server -o jsonpath='{.metadata.labels.reviewed}')" ] || fail "the ClusterRole gained reviewed despite the refusal"
  kca label service metrics-server -n "$NS" stray- >/dev/null || fail "could not put the world back"
  P_APPLY2="$(chdf_a "$ADOPTED" apply -input=false -no-color approved.tfplan 2>&1)" || { printf '%s\n' "$P_APPLY2" | tail -20; fail "the saved plan did not apply once the world was put back"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$P_APPLY2" || fail "the saved plan's apply did not change exactly one object"
  [ "$(kca get clusterrole system:metrics-server -o jsonpath='{.metadata.labels.reviewed}')" = "yes" ] || fail "the ClusterRole does not read reviewed=yes after the saved plan applied"
  ( stock_b plan -out=approved.tfplan -input=false -no-color >/dev/null 2>&1 && stock_b apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "stock's own planfile did not apply on B"
  gauntlet_stage plan_approval pass "plan -out wrote one update (the cluster-scoped ClusterRole system:metrics-server gains reviewed=yes); the world then moved out of band (a stray label on the metrics-server Service in kube-system, kubectl, never choudoufu) and apply of the saved plan refused with \"The approved plan no longer matches the live system\" at exit 3, nothing applied; with the label removed the identical file applied, 0 added, 1 changed, 0 destroyed, and reviewed=yes reads back; stock's own planfile applied on the oracle cluster in the unchanged case. BREAK_APPROVAL=1 expects success after the move and correctly fails"
fi

# ── 7. day2_rename ────────────────────────────────────────────────────────
gauntlet_begin_stage day2_rename
log "=== 7. day2_rename: kubernetes_service_account.metrics becomes .collector through a moved block; its seven references follow ==="
rename_sa() { # $1 root: the block and every reference, plus the moved block
  perl -pi -e 's/kubernetes_service_account" "metrics"/kubernetes_service_account" "collector"/; s/kubernetes_service_account\.metrics\./kubernetes_service_account.collector./g' "$1/metrics-server.tf"
  grep -q 'kubernetes_service_account" "collector"' "$1/metrics-server.tf" || fail "the rename did not match metrics-server.tf - the corpus pin has moved"
  grep -q 'kubernetes_service_account\.metrics\b' "$1/metrics-server.tf" && fail "a reference to kubernetes_service_account.metrics survived the rename"
  printf '\nmoved {\n  from = kubernetes_service_account.metrics\n  to   = kubernetes_service_account.collector\n}\n' >> "$1/metrics-server.tf"
}
if [ "${BREAK:-}" = "1" ]; then
  perl -0777 -pi -e 's/(resource "kubernetes_service_account" "metrics" \{\n  metadata \{\n    namespace = "kube-system"\n    name      = )"metrics-server"/$1"metrics-server-renamed"/' "$ADOPTED/metrics-server.tf"
  grep -q '"metrics-server-renamed"' "$ADOPTED/metrics-server.tf" || fail "BREAK: could not rename the ServiceAccount's metadata.name"
  R_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK: the plan after renaming the object failed"; }
  grep -qE 'will be (created|destroyed)|must be replaced' <<< "$R_PLAN" || fail "BREAK=1: renaming the object's own name did not plan a destroy and a create - the marker-rewritten-in-place assertion is not load-bearing: $(grep -E '^Plan:' <<< "$R_PLAN" | head -1)"
  log "  BREAK=1: caught - renaming metadata.name plans $(grep -E '^Plan:' <<< "$R_PLAN" | head -1)"
  perl -pi -e 's/"metrics-server-renamed"/"metrics-server"/' "$ADOPTED/metrics-server.tf"
  rename_sa "$ADOPTED"; rename_sa "$ORACLE"
  ( chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: the moved-block apply failed"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: stock's moved-block apply failed on B"
  gauntlet_stage day2_rename pass "BREAK=1 control: renaming the object's own metadata.name plans a replace ($(grep -E '^Plan:' <<< "$R_PLAN" | head -1)), so the marker-rewritten-in-place assertion correctly fails to hold; the moved block then applied"
else
  rename_sa "$ADOPTED"; rename_sa "$ORACLE"
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's moved-block plan failed on B"; }
  grep -qE "^No changes|Plan: 0 to add, 0 to change, 0 to destroy" <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's moved-block plan on B is not zero churn"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's moved-block apply failed on B"
  R_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_PLAN" | tail -20; fail "the moved-block plan failed"; }
  grep -qE 'will be (created|destroyed)' <<< "$R_PLAN" \
    && { printf '%s\n' "$R_PLAN" | grep -E '^  # .+ will be'; fail "the moved-block rename proposes a create or a destroy - not the marker rewritten in place"; }
  grep -qF 'Plan: 0 to add, 1 to change, 0 to destroy.' <<< "$R_PLAN" \
    || { printf '%s\n' "$R_PLAN" | tail -20; fail "the moved-block plan is not exactly one in-place change (the address annotation rewrite)"; }
  grep -qE '~ +"choudoufu\.intentius\.io/tofu-address" = ".*" -> ".*"' <<< "$R_PLAN" \
    || { printf '%s\n' "$R_PLAN"; fail "the moved-block plan does not show the tofu-address annotation being rewritten"; }
  R_APPLY_OUT="$(chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$R_APPLY_OUT" | tail -20; fail "the moved-block apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$R_APPLY_OUT" || { printf '%s\n' "$R_APPLY_OUT" | tail -10; fail "the moved-block apply was not exactly one in-place change"; }
  exists_a serviceaccount metrics-server || fail "the ServiceAccount is gone after the rename"
  [ "$(count_a)" = "9" ] || fail "$(count_a) labelled objects after the rename, want 9"
  gauntlet_stage day2_rename pass "moved block: kubernetes_service_account.metrics -> .collector, with the three bindings' subjects and the Deployment's service_account_name following, no add and no destroy, one in-place change confined to the address annotation rewrite (0 add, 1 change, 0 destroy) - the marker rewritten in place, the shape every lane asserts for a rename; the live ServiceAccount in kube-system untouched and still labelled; stock's plan for the same moved block on the oracle cluster is zero churn, since stock never writes this annotation. The moved-block half only. BREAK=1 renames the object's own metadata.name and the marker-rewritten-in-place assertion correctly fails"
fi

# ── 8. day2_remove: the APIService leaves the configuration ──────────────
#
# The cluster-scoped kind this estate exists for. Its orphan is found only
# by the sweep listing apiservices by label, which it could not do before
# this unit (the kind join read "ApiService").
gauntlet_begin_stage day2_remove
log "=== 8. day2_remove: the APIService block leaves the configuration ==="
remove_apisvc() { python3 - "$1/metrics-server.tf" <<'PY'
import re, sys
p = sys.argv[1]; s = open(p).read()
s2 = re.sub(r'resource "kubernetes_api_service" "metrics" \{.*?\n\}\n', '', s, count=1, flags=re.S)
assert s2 != s and 'kubernetes_api_service' not in s2, "the APIService block did not match metrics-server.tf - the corpus pin has moved"
open(p, 'w').write(s2)
PY
}
if [ "${BREAK_REMOVE:-}" = "1" ]; then
  K_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || fail "BREAK_REMOVE: the plan with the block kept failed"
  grep -qE "will be destroyed|[1-9][0-9]* to destroy" <<< "$K_PLAN" && fail "BREAK_REMOVE=1: with the block kept a destroy was still proposed"
  log "  BREAK_REMOVE=1: caught - with the block kept the plan proposes no destroy"
  gauntlet_stage day2_remove pass "BREAK_REMOVE=1 control: with the APIService block kept, no destroy is proposed; the real check is skipped"
  remove_apisvc "$ADOPTED" || fail "BREAK_REMOVE: could not remove the block afterwards"; remove_apisvc "$ORACLE" || fail "BREAK_REMOVE: could not remove the block from the oracle root afterwards"
  ( chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_REMOVE: the removal apply failed afterwards"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_REMOVE: stock's removal apply failed on B afterwards"
else
  remove_apisvc "$ADOPTED" || fail "could not remove the APIService block from the adopted root"
  remove_apisvc "$ORACLE" || fail "could not remove the APIService block from the oracle root"
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's remove plan failed on B"; }
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's remove plan on B is not exactly one destroy"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's remove apply failed on B"
  D_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$D_PLAN" | tail -20; fail "the remove plan failed"; }
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$D_PLAN" || { printf '%s\n' "$D_PLAN" | tail -20; fail "the remove plan is not exactly one destroy - the sweep did not find the APIService by its label"; }
  D_LINE="$(grep -E '^[[:space:]]*# .* will be destroyed' <<< "$D_PLAN" | head -1)"
  D_ADDR="$(sed -E 's/^[[:space:]#]*//; s/ will be destroyed.*$//' <<< "$D_LINE")"
  grep -qE '^kubernetes_api_service(_v1)?\.orphan_v1beta1_metrics_k8s_io$' <<< "$D_ADDR" || { printf '%s\n' "$D_PLAN" | grep -E 'destroyed|^Plan:'; fail "the one destroy is ${D_ADDR:-unnamed}, not the APIService at its orphan address"; }
  D_APPLY="$(chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$D_APPLY" | tail -20; fail "the remove apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 0 changed, 1 destroyed" <<< "$D_APPLY" || fail "the remove apply did not destroy exactly one object"
  kca get apiservice "$APISVC" >/dev/null 2>&1 && fail "the APIService still exists after the remove apply"
  exists_a service metrics-server || fail "the Service that backed the APIService went with it"
  grep -q "No changes." <<< "$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || fail "the replan after the remove is not empty"
  [ "$(count_a)" = "8" ] || fail "$(count_a) labelled objects after the remove, want 8"
  gauntlet_stage day2_remove pass "deleting kubernetes_api_service.metrics's block proposed exactly one destroy (0 add, 0 change, 1 destroy) at the sweep's orphan address $D_ADDR - the cluster-scoped aggregated APIService found by its label alone under apiregistration.k8s.io, a group client-go's scheme does not register - applied cleanly, the object gone (kubectl get apiservice: NotFound), the Service behind it untouched, the next plan empty; stock's plan for the same removal on the oracle cluster is also exactly one destroy; the Deployment's ReplicaSet and Pod, which carry no estate label, were never proposed. BREAK_REMOVE=1 keeps the block and no destroy is proposed"
fi

# ── 9. day2_count ─────────────────────────────────────────────────────────
gauntlet_begin_stage day2_count
log "=== 9. day2_count: a two-instance count ConfigMap the script declares in kube-system scales 2 -> 1 -> 2 ==="
write_shards() { # $1 root, $2 count
  cat > "$1/count_test.tf" <<EOF
# Added by live/e2e/corpus-k8s-metrics-server/run.sh for the gauntlet's
# day2_count stage: the published file declares no count block of its own.
resource "kubernetes_config_map_v1" "shard" {
  count = $2
  metadata {
    name      = "ms-shard-\${count.index}"
    namespace = "$NS"
  }
  data = { shard = tostring(count.index) }
}
EOF
}
write_shards "$ADOPTED" 2; write_shards "$ORACLE" 2
( stock_b apply -auto-approve -input=false -no-color 2>&1 | grep -qF "2 added, 0 changed, 0 destroyed" ) || fail "stock could not add the two shards on B"
( chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1 | grep -qF "2 added, 0 changed, 0 destroyed" ) || fail "choudoufu could not add the two shards on A"
write_shards "$ADOPTED" 1; write_shards "$ORACLE" 1
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's scale-down plan failed on B"
grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's scale-down plan on B is not exactly one destroy"; }
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's scale-down apply failed on B"
C_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$C_PLAN" | tail -20; fail "the scale-down plan failed"; }
grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$C_PLAN" || { printf '%s\n' "$C_PLAN" | tail -20; fail "the scale-down plan is not exactly one destroy"; }
C_LINE="$(grep -E '^[[:space:]]*# .* will be destroyed' <<< "$C_PLAN" | head -1)"
C_ADDR="$(sed -E 's/^[[:space:]#]*//; s/ will be destroyed.*$//' <<< "$C_LINE")"
grep -qE "^kubernetes_config_map(_v1)?\.orphan_${NS}_ms-shard-1$" <<< "$C_ADDR" || { printf '%s\n' "$C_PLAN" | grep -E 'destroyed|^Plan:'; fail "the scale-down destroys ${C_ADDR:-nothing named}, not ms-shard-1 at its orphan address"; }
( chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1 | grep -qF "0 added, 0 changed, 1 destroyed" ) || fail "the scale-down apply did not destroy exactly one object"
if [ "${BREAK_COUNT:-}" = "1" ]; then
  exists_a configmap ms-shard-0 || fail "BREAK_COUNT=1: ms-shard-0 was destroyed - the 'wrong instance' assertion would hold"
  log "  BREAK_COUNT=1: caught - ms-shard-0 still exists, so asserting it was the one destroyed correctly fails"
  gauntlet_stage day2_count pass "BREAK_COUNT=1 control: asserting the lower index (ms-shard-0) was destroyed correctly fails to hold; the real check is skipped"
else
  exists_a configmap ms-shard-0 || fail "ms-shard-0 was destroyed on the scale-down"
  exists_a configmap ms-shard-1 && fail "ms-shard-1 still exists after the scale-down"
  write_shards "$ADOPTED" 2; write_shards "$ORACLE" 2
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's scale-up plan failed on B"
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's scale-up plan on B is not exactly one add"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's scale-up apply failed on B"
  U_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$U_PLAN" | tail -20; fail "the scale-up plan failed"; }
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$U_PLAN" || { printf '%s\n' "$U_PLAN" | tail -20; fail "the scale-up plan is not exactly one add"; }
  grep -q 'kubernetes_config_map_v1.shard\[1\]' <<< "$U_PLAN" || fail "the scale-up does not create shard[1]"
  ( chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1 | grep -qF "1 added, 0 changed, 0 destroyed" ) || fail "the scale-up apply did not create exactly one object"
  exists_a configmap ms-shard-0 || fail "ms-shard-0 does not exist after the scale-up"
  exists_a configmap ms-shard-1 || fail "ms-shard-1 does not exist after the scale-up"
  grep -q "No changes." <<< "$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || fail "the replan after the scale-up is not empty"
  gauntlet_stage day2_count pass "a two-instance count ConfigMap added in kube-system beside the published file (its own shape has no count block): scaling 2 to 1 destroyed exactly ms-shard-1, planned at the sweep's orphan address $C_ADDR since the label carries no index (ms-shard-0 untouched, both read with kubectl); back to 2 created exactly kubernetes_config_map_v1.shard[1] under the same name; the next plan is empty; stock's plans for the same two changes on the oracle cluster have the identical shape. BREAK_COUNT=1 asserts the lower index was destroyed and correctly fails"
fi

# ── 9b. day2_replace: a create_before_destroy rename ───────────────────
# The stage body is shared by the kind estates: live/e2e/lib/gauntlet.sh's
# gauntlet_kind_day2_replace, which adds its own block and removes it again.
gauntlet_begin_stage day2_replace
log "=== 9b. day2_replace: a content-hashed ConfigMap renamed under create_before_destroy ==="
gauntlet_kind_day2_replace "$ADOPTED" "$ORACLE" "$NS"

# ── 10. day2_crash ────────────────────────────────────────────────────────
#
# The same two windows every kind estate interrupts (see corpus-quickpizza's
# section 10 for the full reasoning): the create_before_destroy rename
# window, through the library's gauntlet_kind_day2_crash_rename, and an
# apply creating two objects with a real edge between them, killed by the
# engine itself inside the -parallelism=1 walker after the first create
# commits. The pair's first object is a Secret because a ConfigMap records
# nothing (#1235). Both live in kube-system.
gauntlet_begin_stage day2_crash
log "=== 10. day2_crash: SIGTERM between the create of one object and the create of the next ==="
gauntlet_kind_day2_crash_rename "$ADOPTED" "$NS"

write_crash_pair() { # $1 root, $2 = "first" for crash-first alone
  cat > "$1/crash_test.tf" <<EOF
# Added by live/e2e/corpus-k8s-metrics-server/run.sh for the gauntlet's
# day2_crash stage: the published file declares no pair of objects with an
# edge between them that an interrupted apply could be caught halfway
# through. The first is a Secret and not a ConfigMap because a
# kubernetes_config_map(_v1) records nothing (#1188, #1235).
resource "kubernetes_secret_v1" "crash_first" {
  metadata {
    name      = "ms-crash-first"
    namespace = "$NS"
  }
  data = { step = "one" }
}
EOF
  [ "${2:-both}" = "first" ] && return 0
  cat >> "$1/crash_test.tf" <<EOF

resource "kubernetes_config_map_v1" "crash_second" {
  metadata {
    name      = "ms-crash-second"
    namespace = "$NS"
  }
  data = { after = kubernetes_secret_v1.crash_first.metadata[0].name }
}
EOF
}

write_crash_pair "$ORACLE" first
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's crash-first plan failed on B"; }
grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's crash-first plan on B is not exactly one add"; }
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's crash-first apply failed on B"
write_crash_pair "$ORACLE"
O_REMAINDER="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_REMAINDER" | tail -10; fail "stock's remainder plan failed on B"; }
grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_REMAINDER" \
  || { printf '%s\n' "$O_REMAINDER" | tail -10; fail "stock's remainder plan on B is not exactly one add - the oracle for this stage is not what it should be"; }
grep -q 'kubernetes_config_map_v1.crash_second' <<< "$O_REMAINDER" || fail "stock's remainder plan on B does not name crash_second"
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's remainder apply failed on B"
log "  oracle: stock at crash-first alone plans exactly one add (crash_second) for the remainder, and applies it"

write_crash_pair "$ADOPTED"
X_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$X_PLAN" | tail -20; fail "the pre-crash plan failed"; }
grep -qF "Plan: 2 to add, 0 to change, 0 to destroy." <<< "$X_PLAN" \
  || { printf '%s\n' "$X_PLAN" | tail -20; fail "the pre-crash plan is not exactly two adds - there is no two-object apply to interrupt"; }
X_RECORDS_BEFORE="$(gauntlet_record_envelope_count "$ADOPTED/.tofu-records")"

# The interrupt is delivered by the engine itself, so this runs in the
# plain foreground: no background process, no output tailing, no poll loop.
# A non-zero exit is the NORMAL outcome - the process was killed.
X_OUT="$( cd "$ADOPTED" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" \
  TOFU_E2E_APPLY_RESOURCE_INTERRUPT="kubernetes_secret_v1.crash_first" \
  "$TOFU_CRASH" apply -input=false -auto-approve -no-color -parallelism=1 2>&1 )"; X_RC=$?
printf '%s\n' "$X_OUT" > "$WORK/day2_crash.log"
log "  interrupted apply exited $X_RC (a genuine crash is not expected to exit 0)"
[ "$X_RC" -ne 0 ] || { printf '%s\n' "$X_OUT" | tail -20; fail "the interrupted apply exited 0 - the engine's self-signal never landed, so nothing was interrupted and this stage would measure a clean apply"; }
exists_a secret ms-crash-first || { printf '%s\n' "$X_OUT" | tail -20; fail "ms-crash-first does not exist after the interrupted apply - the kill landed before the create committed"; }
exists_a configmap ms-crash-second && { printf '%s\n' "$X_OUT" | tail -20; fail "ms-crash-second exists after the interrupted apply - the kill landed after both creates"; }
# The selector alone, never a selector next to a resource name: kubectl
# refuses that combination outright, which would read as an unlabelled
# object.
kca get secret -n "$NS" -l "tofu-estate=$ESTATE" -o name 2>/dev/null | grep -qx "secret/ms-crash-first" \
  || fail "ms-crash-first was created by the interrupted apply but does not come back under tofu-estate=$ESTATE - the marker the rerun is supposed to find is not there"
X_RECORDS_AFTER="$(gauntlet_record_envelope_count "$ADOPTED/.tofu-records")"
log "  ms-crash-first exists and is labelled; ms-crash-second does not exist; records $X_RECORDS_BEFORE -> $X_RECORDS_AFTER"

X_REC="$(gauntlet_record_file "$ADOPTED/.tofu-records" "kubernetes_secret_v1.crash_first")"
[ -n "$X_REC" ] || fail "the interrupted apply created ms-crash-first but wrote no record for kubernetes_secret_v1.crash_first (records $X_RECORDS_BEFORE -> $X_RECORDS_AFTER)"
[ "$X_RECORDS_AFTER" = "$((X_RECORDS_BEFORE + 1))" ] || fail "records went $X_RECORDS_BEFORE -> $X_RECORDS_AFTER across the interrupted apply, want exactly one more"
X_RESIDUE="$(gauntlet_record_residue "$X_REC" | tr '\n' ' ' | sed 's/ $//')"
[ "$X_RESIDUE" = "wait_for_service_account_token" ] || fail "the record the interrupted apply wrote for kubernetes_secret_v1.crash_first carries residue [${X_RESIDUE:-none}], want wait_for_service_account_token (#1188, #1235)"
log "  the interrupted apply wrote one record for kubernetes_secret_v1.crash_first, carrying residue $X_RESIDUE"

if [ "${BREAK_CRASH_UNBOUND:-}" = "1" ]; then
  kca label secret ms-crash-first -n "$NS" tofu-estate- >/dev/null 2>&1 || fail "BREAK_CRASH_UNBOUND: could not strip the label off ms-crash-first"
  log "  BREAK_CRASH_UNBOUND=1: stripped tofu-estate off ms-crash-first with kubectl"
fi

R_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)"; R_RC=$?
R_LINE="$(grep -E '^Plan:|^No changes' <<< "$R_PLAN" | head -1 | sed 's/\.$//')"
# What the real check asserts, as one predicate, so the Break controls can
# require the SAME predicate to fail rather than approximating it.
recovered() {
  [ "$R_RC" -eq 0 ] || return 1
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$R_PLAN" || return 1
  grep -qE '^[[:space:]]*# kubernetes_config_map(_v1)?\.crash_second will be created' <<< "$R_PLAN" || return 1
  grep -E '^[[:space:]]*# .* will be' <<< "$R_PLAN" | grep -q 'crash_first\|crash-first' && return 1
  return 0
}

if [ "${BREAK_CRASH:-}" = "1" ]; then
  log "=== 10b (BREAK_CRASH=1). assert nothing is proposed after the interrupt - this must fail ==="
  [ "$R_RC" -eq 0 ] || { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK_CRASH=1: the plan after the interrupt exited $R_RC"; }
  grep -qF "No changes." <<< "$R_PLAN" \
    && { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK_CRASH=1: the plan after a real interrupted two-object apply came back empty, so this stage's own check is not load-bearing"; }
  log "  BREAK_CRASH=1: caught - the plan proposes work ($R_LINE), so 'nothing is proposed' correctly fails to hold"
  gauntlet_stage day2_crash pass "BREAK_CRASH=1 control: after the same real interrupt the plan proposes work ($R_LINE), so the stage's own Break line - interrupt and then assert nothing is proposed - correctly fails to hold; the real check is skipped $CRASH_RENAME_DETAIL"
  ( chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_CRASH: the recovery apply failed afterwards"
elif [ "${BREAK_CRASH_UNBOUND:-}" = "1" ]; then
  log "=== 10b (BREAK_CRASH_UNBOUND=1). the same check against an unbound object - this must fail ==="
  if recovered; then
    printf '%s\n' "$R_PLAN" | grep -E '^Plan:|will be'
    fail "BREAK_CRASH_UNBOUND=1: the recovery check still holds with ms-crash-first carrying no tofu-estate label - it is not measuring whether the crashed-out object was bound at all"
  fi
  log "  BREAK_CRASH_UNBOUND=1: caught - with the label stripped the recovery check fails ($R_LINE)"
  gauntlet_stage day2_crash pass "BREAK_CRASH_UNBOUND=1 control: with the tofu-estate label stripped off the object the interrupted apply created - the unrecovered run this stage exists to catch - the recovery check correctly fails to hold ($R_LINE); the real check is skipped $CRASH_RENAME_DETAIL"
  kca delete secret ms-crash-first -n "$NS" >/dev/null 2>&1 || fail "BREAK_CRASH_UNBOUND: could not delete the unlabelled ms-crash-first afterwards"
  ( chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_CRASH_UNBOUND: the apply after the cleanup failed"
else
  if ! recovered; then
    printf '%s\n' "$R_PLAN" | grep -E '^Plan:|^No changes|will be' | head -20
    gauntlet_stage day2_crash fail "the plan after a real interrupt between the create of kubernetes_secret_v1.crash_first and the create of kubernetes_config_map_v1.crash_second is not exactly the remainder: ${R_LINE:-no plan line} (exit $R_RC). ms-crash-first exists in kube-system carrying tofu-estate=$ESTATE and ms-crash-second does not, both read with kubectl; stock, walked into the same position on the oracle cluster, plans exactly one add (crash_second). The interrupted apply wrote one record for kubernetes_secret_v1.crash_first carrying residue ${X_RESIDUE:-none} (records $X_RECORDS_BEFORE -> $X_RECORDS_AFTER) $CRASH_RENAME_DETAIL"
  else
    # The record's contribution, measured rather than counted (#1235): take
    # the one record out, replan from the identical position, put it back.
    mv "$X_REC" "$WORK/crash_first.record" || fail "could not move the crash record aside"
    N_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)"; N_RC=$?
    N_LINE="$(grep -E '^Plan:|^No changes' <<< "$N_PLAN" | head -1 | sed 's/\.$//')"
    [ "$N_RC" -eq 0 ] || { printf '%s\n' "$N_PLAN" | tail -20; fail "the replan with the crash record taken out of the store exited $N_RC"; }
    grep -qF "Plan: 1 to add, 1 to change, 0 to destroy." <<< "$N_PLAN" \
      || { printf '%s\n' "$N_PLAN" | grep -E '^Plan:|will be|^ +[+~-] ' | head -20
           fail "with the one record the interrupted apply wrote taken out of the store, the recovery plan is $N_LINE, not the remainder plus one in-place update - so the record contributed nothing this stage can read"; }
    grep -qE '^[[:space:]]+\+ wait_for_service_account_token +=' <<< "$N_PLAN" \
      || { printf '%s\n' "$N_PLAN" | grep -E 'will be|^ +[+~-] ' | head -20
           fail "the extra in-place update the missing record produces does not propose wait_for_service_account_token back"; }
    mv "$WORK/crash_first.record" "$X_REC" || fail "could not put the crash record back"
    B_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)"
    grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$B_PLAN" \
      || { printf '%s\n' "$B_PLAN" | grep -E '^Plan:|will be' | head -10
           fail "putting the record back does not restore the exact-remainder plan, so the difference measured above is not the record's"; }
    log "  the record's contribution: with it, $R_LINE; without it, $N_LINE (+ wait_for_service_account_token on crash_first); with it again, the remainder"

    R_APPLY="$(chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)"; R_APPLY_RC=$?
    [ "$R_APPLY_RC" -eq 0 ] || { printf '%s\n' "$R_APPLY" | tail -20; fail "the recovery apply exited $R_APPLY_RC"; }
    grep -qF "Apply complete! Resources: 1 added, 0 changed, 0 destroyed" <<< "$R_APPLY" \
      || { printf '%s\n' "$R_APPLY" | tail -5; fail "the recovery apply did not add exactly the one remaining object"; }
    exists_a configmap ms-crash-second || fail "ms-crash-second does not exist after the recovery apply"
    exists_a secret ms-crash-first || fail "ms-crash-first is gone after the recovery apply - the recovery replaced the object the crash created instead of binding it"
    R_REPLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)"; R_REPLAN_RC=$?
    [ "$R_REPLAN_RC" -eq 0 ] || { printf '%s\n' "$R_REPLAN" | tail -30
      R_ERR="$(grep -E '^Error' <<< "$R_REPLAN" | head -1)"
      fail "the replan after the recovery exited $R_REPLAN_RC: ${R_ERR:-no Error: line; the last 30 lines of the plan are above this verdict in the log}"; }
    grep -q "No changes." <<< "$R_REPLAN" || { printf '%s\n' "$R_REPLAN" | tail -20; fail "the replan after the recovery is not empty"; }
    gauntlet_stage day2_crash pass "a Secret/ConfigMap pair added in kube-system beside the published file: the apply creating both was interrupted by a real SIGTERM (exit $X_RC), delivered by the engine itself inside the -parallelism=1 graph walker the instant kubernetes_secret_v1.crash_first's create committed; crash_second reads crash_first's name, so the walker cannot have reached it - kubectl confirms ms-crash-first exists carrying tofu-estate=$ESTATE and ms-crash-second does not. The next plan proposed exactly the remainder ($R_LINE) and nothing for crash_first, which it bound by its label and its namespace and name, matching stock's own plan from the same position on the oracle cluster; the recovery apply added exactly one object and the plan after it is empty. The record store's contribution is read, not counted: the interrupted apply wrote exactly one record (files $X_RECORDS_BEFORE -> $X_RECORDS_AFTER) carrying residue $X_RESIDUE, and taking it out and replanning turns $R_LINE into $N_LINE, proposing wait_for_service_account_token back; putting it back restores the exact-remainder plan. BREAK_CRASH=1 and BREAK_CRASH_UNBOUND=1 correctly fail $CRASH_RENAME_DETAIL"
  fi
fi
gauntlet_end_stage

# ── 11. day2_teardown: kube-system stays ─────────────────────────────────
#
# What this estate adds to the stage: the namespace every namespaced object
# lives in is kube-system, which the estate never declared and must never
# delete. After apply -destroy, kube-system has to exist, and every object
# of the compared kinds it (and the cluster scope) held before cold_deploy
# has to be there still - nothing more, nothing less. The same comparison
# runs on B after stock's own destroy, so the two agree on what "only the
# declared objects" means.
gauntlet_begin_stage day2_teardown
log "=== 11. day2_teardown: apply -destroy on the adopted estate, and stock's own destroy on B ==="
if [ "${BREAK_TEARDOWN:-}" = "1" ]; then
  # A ConfigMap kube-system held before cold_deploy, deleted out of band: the
  # snapshot comparison below must notice that the namespace is not as it was.
  # kubeadm's own, which no controller recreates (kube-root-ca.crt and
  # extension-apiserver-authentication are rewritten within seconds, and
  # would let the comparison match again by the time it runs).
  VICTIM="$(grep -m1 -E '^configmap/(kubeadm-config|kubelet-config)$' "$WORK/baseline.a")"
  [ -n "$VICTIM" ] || fail "BREAK_TEARDOWN: kube-system's baseline holds no ConfigMap to delete"
  kca delete "$VICTIM" -n "$NS" >/dev/null || fail "BREAK_TEARDOWN: could not delete $VICTIM"
  log "  BREAK_TEARDOWN=1: deleted kube-system's own $VICTIM with kubectl"
fi
T_EXPECT="$(count_a)"
T_OUT="$(chdf_a "$ADOPTED" apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$T_OUT" | tail -20; fail "apply -destroy failed"; }
grep -qF "Resources: 0 added, 0 changed, $T_EXPECT destroyed" <<< "$T_OUT" || { printf '%s\n' "$T_OUT" | tail -5; fail "apply -destroy did not remove exactly the $T_EXPECT remaining objects"; }
[ "$(count_a)" = "0" ] || fail "$(count_a) object(s) still carry tofu-estate=$ESTATE after the destroy"
[ "$(kca get namespace "$NS" -o jsonpath='{.status.phase}' 2>/dev/null)" = "Active" ] || fail "kube-system is not Active after the destroy - the estate deleted a namespace it never declared"
kube_system_snapshot "$KCA" > "$WORK/after.a"
TEARDOWN_DIFF="$(diff "$WORK/baseline.a" "$WORK/after.a")"
O_EXPECT="$(stock_b state list 2>/dev/null | wc -l | tr -d ' ')"
O_T="$(stock_b apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$O_T" | tail -10; fail "stock's destroy failed on B"; }
grep -qF "Resources: 0 added, 0 changed, $O_EXPECT destroyed" <<< "$O_T" || fail "stock's destroy on B did not remove exactly the $O_EXPECT objects its state held"
kube_system_snapshot "$KCB" > "$WORK/after.b"
diff -q "$WORK/baseline.b" "$WORK/after.b" >/dev/null || { diff "$WORK/baseline.b" "$WORK/after.b"; fail "stock's destroy on B did not leave kube-system and the cluster scope as they were before its cold deploy - the oracle for this stage does not hold"; }
if [ "${BREAK_TEARDOWN:-}" = "1" ]; then
  [ -z "$TEARDOWN_DIFF" ] && fail "BREAK_TEARDOWN=1: with $VICTIM deleted out of band the kube-system comparison still matches - it is not load-bearing"
  log "  BREAK_TEARDOWN=1: caught - the comparison names the difference: $(tr '\n' ' ' <<< "$TEARDOWN_DIFF")"
  gauntlet_stage day2_teardown pass "BREAK_TEARDOWN=1 control: with one of kube-system's own ConfigMaps ($VICTIM) deleted out of band, the before/after comparison of kube-system and the cluster scope correctly fails to match; the real check is skipped"
else
  [ -z "$TEARDOWN_DIFF" ] || { printf '%s\n' "$TEARDOWN_DIFF"; fail "after apply -destroy, kube-system and the cluster scope are not what they were before cold_deploy (diff above)"; }
  gauntlet_stage day2_teardown pass "apply -destroy removed exactly the $T_EXPECT remaining objects in one apply (the four cluster-scoped RBAC objects among them), no object of any of the estate's kinds carrying tofu-estate=$ESTATE afterwards (kubectl, every namespace); kube-system, which the estate writes into and never declared, is still Active, and every one of its $(wc -l < "$WORK/baseline.a" | tr -d ' ') objects of the compared kinds (ConfigMaps, ServiceAccounts, Services, Deployments, DaemonSets, Roles, RoleBindings, plus every ClusterRole, ClusterRoleBinding and APIService) read before cold_deploy is there and nothing else is; stock's destroy on the oracle cluster removed exactly the $O_EXPECT its state held and passes the same comparison. BREAK_TEARDOWN=1 deletes one of kube-system's own ConfigMaps and the comparison correctly fails"
fi

# ── 12. greenfield ────────────────────────────────────────────────────────
gauntlet_begin_stage greenfield
log "=== 12. greenfield: choudoufu applies the cut-out root fresh, with a live block, on the now-empty cluster A ==="
write_root "$GREEN" live
( chdf_a "$GREEN" init -input=false -no-color >/dev/null 2>&1 ) || fail "greenfield init failed"
G_OUT="$(chdf_a "$GREEN" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$G_OUT" | tail -20; fail "greenfield apply failed"; }
grep -qF "Apply complete! Resources: 9 added, 0 changed, 0 destroyed" <<< "$G_OUT" || fail "greenfield apply did not add exactly 9 objects"
[ ! -f "$GREEN/terraform.tfstate" ] || fail "a terraform.tfstate appeared after a live-block apply"
[ "$(count_a)" = "9" ] || fail "$(count_a) object(s) carry tofu-estate=$ESTATE after the greenfield apply, want 9"
gauntlet_wait_until 240 "$APISVC Available=True on A after greenfield" -- apisvc_is "$KCA" True \
  || fail "the aggregated API never reported Available on A after the greenfield apply"
G_RECORDS="$(gauntlet_record_envelope_count "$GREEN/.tofu-records")"
grep -q "No changes." <<< "$(chdf_a "$GREEN" plan -input=false -no-color 2>&1)" || fail "the greenfield replan is not empty"
rm -f "$GREEN/.terraform/choudoufu-cache.tfstate"
grep -q "No changes." <<< "$(chdf_a "$GREEN" plan -input=false -no-color 2>&1)" || fail "the greenfield replan without the cache is not empty"

# ── the lost-store control (#1235), as the other kind estates take it ────
rm -rf "$GREEN/.tofu-records" "$GREEN/.terraform/choudoufu-cache.tfstate"
L_PLAN="$(chdf_a "$GREEN" plan -input=false -no-color 2>&1)"; L_RC=$?
L_LINE="$(grep -E '^Plan:|^No changes' <<< "$L_PLAN" | head -1 | sed 's/\.$//')"
[ "$L_RC" -eq 0 ] || { printf '%s\n' "$L_PLAN" | tail -20; fail "the plan with no record store at all exited $L_RC"; }
L_GONE="$(grep -cE '^[[:space:]]*# .* will be (created|destroyed|replaced)' <<< "$L_PLAN")"
[ "$L_GONE" = "0" ] || { printf '%s\n' "$L_PLAN" | grep -E 'will be' | head -20
  fail "with the whole record store deleted the plan proposes $L_GONE create/destroy/replace(s) ($L_LINE) - the objects are not being found by their label and their name alone"; }
L_CHANGES="$(grep -cE '^[[:space:]]*# .* will be updated in-place' <<< "$L_PLAN")"
L_RESIDUE="$(grep -oE '^[[:space:]]+\+ [a-z_]+ +=' <<< "$L_PLAN" | sed -E 's/^[[:space:]]*\+ //; s/ *=$//' | sort -u | tr '\n' ' ' | sed 's/ $//')"
L_APPLY="$(chdf_a "$GREEN" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$L_APPLY" | tail -20; fail "the apply that should reconverge after a lost record store failed"; }
grep -qF "Apply complete! Resources: 0 added, $L_CHANGES changed, 0 destroyed" <<< "$L_APPLY" \
  || { printf '%s\n' "$L_APPLY" | tail -5; fail "the reconverging apply after a lost record store is not exactly the $L_CHANGES in-place update(s) the plan proposed"; }
grep -q "No changes." <<< "$(chdf_a "$GREEN" plan -input=false -no-color 2>&1)" || fail "the plan after the reconverging apply is not empty"
[ "$(count_a)" = "9" ] || fail "$(count_a) object(s) carry tofu-estate=$ESTATE after the lost-store reconvergence, want 9"
log "  lost store: $L_LINE, every object still bound; one apply reconverged and the plan after it is empty"
DROP=""; [ "${BREAK:-}" = "1" ] && DROP="apiservice/$APISVC"
inventory "$KCA" "$DROP" > "$WORK/inventory.green.json" || fail "could not read the greenfield inventory"
if [ "${BREAK:-}" = "1" ]; then
  diff -q "$WORK/inventory.stock.json" "$WORK/inventory.green.json" >/dev/null && fail "BREAK=1: with the APIService dropped the two inventories still match"
  log "  BREAK=1: caught - the inventories differ once the APIService is dropped"
  gauntlet_stage greenfield pass "BREAK=1 control: dropping the APIService from the greenfield inventory makes the object-by-object comparison correctly fail; the estate applied (9 added, no terraform.tfstate) and replanned empty with and without the cache"
else
  if ! diff -u "$WORK/inventory.stock.json" "$WORK/inventory.green.json"; then
    fail "the greenfield inventory differs from stock's cold-deploy inventory (diff above)"
  fi
  gauntlet_stage greenfield pass "the cut-out root applied fresh with a live block and no terraform.tfstate: 9 objects, every one labelled tofu-estate=$ESTATE (kubectl), the APIService reporting Available=True again; the record store held $G_RECORDS record envelope(s), counted by their own address field; replanned empty with and without the cache. Deleting the whole record store and the cache and replanning proposed $L_LINE: nothing created, destroyed or swept, every object still bound by its label and its name, and $L_CHANGES in-place update(s) putting back the residue the store held (${L_RESIDUE:-none}); one apply reconverged and the plan after it is empty. The cluster's inventory (both ClusterRoles' rules and aggregation labels, the three bindings' roles and subjects, the ServiceAccount, the APIService's group, version, priorities, TLS setting and backing Service, the Deployment's containers, args, ports, service account and priority class, the Service's ports and selector) matches stock's cold deploy on the same cluster object by object, labels never compared. BREAK=1 drops the APIService from the expected inventory and the match correctly fails"
fi
( chdf_a "$GREEN" apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "greenfield teardown failed"

# ── 13. strict ────────────────────────────────────────────────────────────
gauntlet_begin_stage strict
STRICT="$WORK/strict"; mkdir -p "$STRICT"
strict_block() { cat <<EOF
terraform {
  required_providers {
    random = {
      source  = "hashicorp/random"
      version = ">= 3.0"
    }
  }
  live {
    estate = "corpus-k8s-metrics-server-strict"
    record_store "local" {
      path = ".tofu-records"
    }
    strict {
      secrets          = "$1"
      no_source_create = "refuse"
      marker_repair    = "never"
      markers "record" {
        types = ["kubernetes_config_map_v1"]
      }
    }
  }
}

resource "random_password" "db" {
  length = 16
}
EOF
}
log "=== 13. strict: every strict toggle on ==="
strict_block "refuse" > "$STRICT/main.tf"
( chdf_a "$STRICT" init -input=false -no-color >/dev/null 2>&1 ) || fail "choudoufu init for the strict-stage scratch estate failed"
STRICT_ON="$(chdf_a "$STRICT" plan -input=false -no-color 2>&1)"; STRICT_ON_RC=$?
if [ "${BREAK_STRICT:-}" = "1" ]; then
  strict_block "store" > "$STRICT/main.tf"
  STRICT_OFF="$(chdf_a "$STRICT" plan -input=false -no-color 2>&1)"; STRICT_OFF_RC=$?
  [ "$STRICT_OFF_RC" -eq 0 ] || { printf '%s\n' "$STRICT_OFF" | tail -20; fail "BREAK_STRICT=1: the plan with secrets = \"store\" exited $STRICT_OFF_RC"; }
  grep -q "^Error:" <<< "$STRICT_OFF" && fail "BREAK_STRICT=1: turning secrets off did not clear every refusal"
  grep -qF 'random_password.db will be created' <<< "$STRICT_OFF" || fail "BREAK_STRICT=1: the plan with secrets = \"store\" does not propose creating random_password.db"
  gauntlet_stage strict pass "BREAK_STRICT=1 control: with secrets back to \"store\" the refusal is gone and the plan is an ordinary create"
else
  [ "$STRICT_ON_RC" -eq 1 ] || { printf '%s\n' "$STRICT_ON" | tail -20; fail "the every-toggle-on plan exited $STRICT_ON_RC, not the refusal's usual 1"; }
  [ "$(grep -c '^Error:' <<< "$STRICT_ON")" -eq 1 ] || { printf '%s\n' "$STRICT_ON"; fail "every strict toggle on refused more than one thing"; }
  grep -qF 'Error: Logical resource is not admitted' <<< "$STRICT_ON" || { printf '%s\n' "$STRICT_ON"; fail "the one refusal is not \"Logical resource is not admitted\""; }
  grep -qF 'strict { secrets = "refuse" }' <<< "$STRICT_ON" || { printf '%s\n' "$STRICT_ON"; fail "the refusal does not cite strict { secrets = \"refuse\" }"; }
  gauntlet_stage strict pass "every strict toggle on (secrets = refuse, no_source_create = refuse, marker_repair = never with a markers \"record\" selection naming kubernetes_config_map_v1) against a scratch estate carrying random_password.db: exactly one refusal, Logical resource is not admitted under strict { secrets = \"refuse\" }; the other two toggles are on and silent. BREAK_STRICT=1 turns secrets back to \"store\" and the refusal disappears"
fi
gauntlet_end_stage

gauntlet_end
log "corpus-k8s-metrics-server: done"
