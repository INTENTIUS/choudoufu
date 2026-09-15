#!/usr/bin/env bash
# corpus-quickpizza: the kubernetes lane's first PUBLISHED estate (#1067,
# under #1016's ruling), crossed on the kind substrate. grafana/quickpizza's
# own deployments/terraform root at tag v0.15.28 (commit
# d5608ca0870d5cf58e911c64d227b9cfcfc94179, live/corpus-manifest.json),
# Grafana Labs' QuickPizza demo application as its README says to run it on
# a cluster: a namespace, seven Deployments and their Services, a Postgres
# StatefulSet, two ConfigMaps, three Secrets, a ServiceAccount and a
# ClusterRoleBinding - 26 hashicorp/kubernetes resources over eight kinds,
# no cloud provider - plus one helm_release behind
# `count = var.enable_k8s_monitoring ? 1 : 0`, off by default. Real images
# (ghcr.io/grafana/quickpizza-local, grafana/alloy, postgres), pulled by
# kind's nodes; every pod runs except Alloy, which errors on the placeholder
# Grafana Cloud token below and blocks nothing.
#
# Four deltas, the same kind every AWS crossing applies for the emulator,
# each asserted so a moved pin fails loudly rather than silently:
#   1. the kubernetes and helm provider blocks lose their config_path /
#      config_context lines (the root names a minikube kubeconfig), so
#      KUBE_CONFIG_PATH names the run's kind cluster;
#   2. terraform.tfvars supplies placeholder values for the two Grafana
#      Cloud variables that have no default (they land in a Secret);
#   3. Alloy's Deployment gets wait_for_rollout = false, a consequence of
#      delta 2: with a placeholder token Alloy's pod can never become
#      ready, and the provider waits its full ten-minute rollout timeout
#      on ANY update to that Deployment - the labels-only write a
#      migration makes included (measured: the first run's live-import
#      -approve sat on exactly that write). Every other Deployment keeps
#      the root's own default and rolls out for real;
#   4. choudoufu's roots get a live block appended to terraform.tf.
# And one addition for one stage: day2_count declares a two-instance count
# ConfigMap in a file of its own, because the estate's own shape has no
# count block; it is added at stage 9 and destroyed with the rest.
#
# .corpus is read, never written: the root is copied out per run. Two kind
# clusters, both created for the run: A holds the estate (stock cold-deploys
# it, choudoufu adopts it and runs every day-2 stage, tears it down, then
# applies the same shape fresh with a live block); B is the oracle, where
# stock applies the identical root and every day-2 change itself.
#
# What this estate found on the way in (#1067): every one of its twenty
# namespaced objects reads `namespace = kubernetes_namespace_v1.quickpizza.id`,
# and identity resolution refused all twenty as "Not an identity attribute"
# until the object-metadata rule claimed the provider's id - the object's own
# import id - as an identity attribute (internal/live/identity/metadata.go).
#
#   go run ./tools/gauntlet run corpus-quickpizza   # one estate, local kind clusters: no allow file
#   bash live/e2e/corpus-quickpizza/run.sh
#
# Needs `just corpus-fetch` first, then kind, kubectl, terraform (the stock
# binary) and Docker on PATH, and network for the images.
#
# Env overrides:
#   TOFU_BIN       path to a prebuilt choudoufu binary; skips the `go build`.
#   BREAK          set to 1 to run drift_reconverge's, day2_rename's and
#                  greenfield's negative controls: a second object tampered
#                  (the single-object assertion must fail); the ServiceAccount's
#                  own metadata.name changed (a bare block rename is zero churn
#                  on Kubernetes; the zero-churn assertion must fail); the
#                  Postgres StatefulSet dropped from greenfield's expected
#                  inventory (the match must fail).
#   BREAK_REMOVE   keep the ClusterRoleBinding block; no destroy may be proposed.
#   BREAK_COUNT    assert the wrong instance was destroyed on the scale-down.
#   BREAK_APPROVAL apply the saved plan after the world moved and expect success.
#   BREAK_STRICT   turn secrets back to "store"; the refusal must vanish.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
source "$ROOT/live/e2e/lib/gauntlet.sh"
ESTATE="corpus-quickpizza"
NS="quickpizza"
KINDS="namespaces configmaps secrets serviceaccounts services deployments statefulsets clusterrolebindings"
SRC="$ROOT/.corpus/quickpizza"
WORK="$(mktemp -d)"
STOCK="$WORK/stock"; ADOPTED="$WORK/adopted"; ORACLE="$WORK/oracle"; GREEN="$WORK/green"
KCA="$WORK/a.kubeconfig"; KCB="$WORK/b.kubeconfig"
CLUSTER_A="chdf-qp-a-$$"; CLUSTER_B="chdf-qp-b-$$"
export TF_PLUGIN_CACHE_DIR="$WORK/plugin-cache"; mkdir -p "$TF_PLUGIN_CACHE_DIR"
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
[ -d "$SRC/deployments/terraform" ] || fail "$SRC/deployments/terraform is missing - run \`just corpus-fetch\` first"
PIN="$(git -C "$SRC" rev-parse HEAD 2>/dev/null)"
[ "$PIN" = "d5608ca0870d5cf58e911c64d227b9cfcfc94179" ] || fail "the fetched quickpizza is at $PIN, not the pinned d5608ca0870d5cf58e911c64d227b9cfcfc94179 - run \`just corpus-fetch\`"

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

# ── the root, copied out of .corpus with its deltas ──────────────────────
# write_root copies deployments/terraform (and the init script it reads
# one level up) into $1/terraform, applies delta 1 and delta 2, and delta 3
# when $2 is "live". The root directory is $1/terraform.
write_root() {
  local dst="$1" mode="$2"
  rm -rf "$dst"; mkdir -p "$dst"
  cp -R "$SRC/deployments/terraform" "$dst/terraform" || fail "could not copy the root out of .corpus"
  cp "$SRC/deployments/init-db-observability.sh" "$dst/" || fail "could not copy init-db-observability.sh, which database.tf reads with file()"
  local tf="$dst/terraform/terraform.tf"
  grep -q 'config_context *= *"minikube"' "$tf" || fail "delta 1 did not match terraform.tf (no minikube config_context) - the corpus pin has moved"
  perl -pi -e 's/^\s*config_path\s*=.*\n//; s/^\s*config_context\s*=.*\n//' "$tf"
  grep -q 'config_' "$tf" && fail "delta 1 left a config_ line in terraform.tf"
  printf 'grafana_cloud_stack = "gauntlet"\ngrafana_cloud_token = "glc_gauntlet_placeholder"\n' > "$dst/terraform/terraform.tfvars"
  local alloy="$dst/terraform/alloy.tf"
  perl -0777 -pi -e 's/(resource "kubernetes_deployment_v1" "alloy" \{\n)/$1  wait_for_rollout = false # delta 3: no Grafana Cloud token, so this pod never becomes ready\n/' "$alloy"
  grep -q 'wait_for_rollout = false' "$alloy" || fail "delta 3 did not match alloy.tf (no kubernetes_deployment_v1 alloy block) - the corpus pin has moved"
  if [ "$mode" = "live" ]; then
    cat >> "$tf" <<EOF

terraform {
  live {
    estate = "$ESTATE"
    record_store "local" {
      path = ".tofu-records"
    }
  }
}
EOF
    grep -q "estate = \"$ESTATE\"" "$tf" || fail "delta 4 did not land in terraform.tf"
  fi
}

# ── cluster helpers ──────────────────────────────────────────────────────
kca() { kubectl --kubeconfig "$KCA" "$@"; }
kcb() { kubectl --kubeconfig "$KCB" "$@"; }
stock_b() { ( cd "$ORACLE/terraform" && KUBECONFIG="$KCB" KUBE_CONFIG_PATH="$KCB" terraform "$@" ); }
chdf_a() { local dir="$1"; shift; ( cd "$dir/terraform" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" "$TOFU" "$@" ); }
count_a() { KUBECONFIG="$KCA" gauntlet_kind_count "$ESTATE" $KINDS; }
exists_a() { kca get "$1" "$2" -n "$NS" >/dev/null 2>&1; }
# inventory prints the estate's objects on cluster $1, normalised to what
# the configuration declares - never labels, annotations or anything the
# server set - so stock's cold deploy and choudoufu's greenfield apply
# compare object by object. $2 is a "kind/name" to drop (BREAK's control).
inventory() {
  local cfg="$1" drop="${2:-}"
  KUBECONFIG="$cfg" DROP="$drop" NS="$NS" python3 - <<'PY'
import json, os, subprocess
ns = os.environ["NS"]; drop = os.environ.get("DROP", "")
def items(kind, namespaced=True):
    args = ["kubectl", "get", kind, "-o", "json"] + (["-n", ns] if namespaced else [])
    out = subprocess.run(args, capture_output=True, text=True)
    return json.loads(out.stdout).get("items", []) if out.returncode == 0 else []
inv = {}
inv["namespace/" + ns] = {"exists": subprocess.run(["kubectl", "get", "namespace", ns], capture_output=True).returncode == 0}
for o in items("configmaps"):
    if o["metadata"]["name"] == "kube-root-ca.crt": continue
    inv["configmap/" + o["metadata"]["name"]] = {"keys": sorted(o.get("data", {}).keys())}
for o in items("secrets"):
    if o.get("type", "") != "Opaque": continue
    inv["secret/" + o["metadata"]["name"]] = {"keys": sorted(o.get("data", {}).keys()), "type": o.get("type")}
for o in items("serviceaccounts"):
    if o["metadata"]["name"] == "default": continue
    inv["serviceaccount/" + o["metadata"]["name"]] = {"exists": True}
for o in items("services"):
    s = o["spec"]
    inv["service/" + o["metadata"]["name"]] = {"type": s.get("type"), "selector": s.get("selector"),
        "ports": [{"port": p.get("port"), "targetPort": p.get("targetPort"), "protocol": p.get("protocol"), "name": p.get("name")} for p in s.get("ports", [])]}
for o in items("deployments"):
    s = o["spec"]
    inv["deployment/" + o["metadata"]["name"]] = {"replicas": s.get("replicas"), "selector": s.get("selector", {}).get("matchLabels"),
        "containers": [{"name": c["name"], "image": c["image"]} for c in s["template"]["spec"]["containers"]],
        "serviceAccount": s["template"]["spec"].get("serviceAccountName")}
for o in items("statefulsets"):
    s = o["spec"]
    inv["statefulset/" + o["metadata"]["name"]] = {"replicas": s.get("replicas"), "serviceName": s.get("serviceName"),
        "containers": [{"name": c["name"], "image": c["image"]} for c in s["template"]["spec"]["containers"]],
        "claims": [c["metadata"]["name"] for c in s.get("volumeClaimTemplates", [])]}
for o in items("clusterrolebindings", namespaced=False):
    if o["metadata"]["name"] != "alloy": continue
    inv["clusterrolebinding/alloy"] = {"roleRef": o.get("roleRef"), "subjects": o.get("subjects")}
if drop:
    inv.pop(drop, None)
print(json.dumps(inv, sort_keys=True, indent=1))
PY
}

# ── 1. cold_deploy: stock stands the root up on A (and B, the oracle) ────
gauntlet_begin_stage cold_deploy
log "=== 1. cold_deploy: two kind clusters, stock terraform applies the published root on each ==="
gauntlet_kind_up "$CLUSTER_A" "$KCA" || fail "kind cluster A ($CLUSTER_A) did not come up"
gauntlet_kind_up "$CLUSTER_B" "$KCB" || fail "kind cluster B ($CLUSTER_B) did not come up"
export KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA"
log "  cluster A: $(kca version 2>/dev/null | grep -i server | head -1); cluster B: $CLUSTER_B"
write_root "$STOCK" stock
write_root "$ORACLE" stock
( cd "$STOCK/terraform" && terraform init -input=false -no-color >/dev/null 2>&1 ) || fail "stock init failed on A"
COLD_OUT="$(cd "$STOCK/terraform" && terraform apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$COLD_OUT" | tail -20; fail "stock cold deploy failed on A"; }
grep -qF "Apply complete! Resources: 26 added, 0 changed, 0 destroyed" <<< "$COLD_OUT" || { printf '%s\n' "$COLD_OUT" | tail -5; fail "stock cold deploy did not add exactly 26 objects on A"; }
STOCK_N="$(cd "$STOCK/terraform" && terraform state list | wc -l | tr -d ' ')"
[ "$STOCK_N" = "26" ] || fail "stock's state holds $STOCK_N instances, want 26"
UNMARKED="$(count_a)"
[ "$UNMARKED" = "0" ] || fail "$UNMARKED object(s) already carry tofu-estate=$ESTATE after a plain stock apply"
inventory "$KCA" > "$WORK/inventory.stock.json" || fail "could not read the cold-deployed inventory on A"
RUNNING="$(kca get pods -n "$NS" --no-headers 2>/dev/null | grep -c Running)"
( stock_b init -input=false -no-color >/dev/null 2>&1 ) || fail "stock init failed on B"
( stock_b apply -auto-approve -input=false -no-color 2>&1 | grep -qF "Apply complete! Resources: 26 added" ) || fail "stock cold deploy failed on B"
gauntlet_stage cold_deploy pass "26 objects (namespace, 7 Deployments, 8 Services, a StatefulSet, 2 ConfigMaps, 3 Secrets, a ServiceAccount, a ClusterRoleBinding) from plain terraform on the unmodified published root plus its deltas (kubeconfig lines dropped, placeholder Grafana Cloud values, Alloy's rollout not awaited), against kind $(kca version 2>/dev/null | grep -io 'v1\.[0-9.]*' | head -1); a real terraform.tfstate with 26 instances, zero tofu-estate labels, $RUNNING pod(s) Running (Alloy errors on the placeholder Grafana Cloud token, by design); the identical root cold-deployed by stock on a second cluster as every later stage's oracle"

# ── 2. migrate ────────────────────────────────────────────────────────────
gauntlet_begin_stage migrate
log "=== 2. migrate: live-import against the stock state file, read-only then -approve ==="
write_root "$ADOPTED" live
( chdf_a "$ADOPTED" init -input=false -no-color >/dev/null 2>&1 ) || fail "adopted init failed"
IMPORT_OUT="$(chdf_a "$ADOPTED" live-import -state="$STOCK/terraform/terraform.tfstate" -estate="$ESTATE" -no-color 2>&1)" || { printf '%s\n' "$IMPORT_OUT" | tail -20; fail "live-import (dry run) failed"; }
log "  dry run: $(grep -E 'resource instance\(s\) are eligible for stamping' <<< "$IMPORT_OUT" | head -1)"
APPROVE_OUT="$(chdf_a "$ADOPTED" live-import -state="$STOCK/terraform/terraform.tfstate" -estate="$ESTATE" -approve -no-color 2>&1)" || { printf '%s\n' "$APPROVE_OUT" | tail -20; fail "live-import -approve failed"; }
SUMMARY_LINE="$(grep -E 'resource\(s\) newly stamped' <<< "$APPROVE_OUT" | head -1 | sed 's/\.$//')"
log "  approve: ${SUMMARY_LINE:-no summary line}"
if grep -qF "26 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 0 skipped." <<< "$APPROVE_OUT"; then
  LABELLED="$(count_a)"
  [ "$LABELLED" = "26" ] || fail "live-import reported 26 stamped but $LABELLED object(s) carry tofu-estate=$ESTATE"
  gauntlet_stage migrate pass "26 of 26 stamped, 0 skipped, from the stock state file; every object carries tofu-estate=$ESTATE, read back with kubectl across the estate's eight kinds"
else
  gauntlet_stage migrate fail "live-import -approve did not stamp all 26 cleanly: ${SUMMARY_LINE:-no summary line}"
fi

# ── 3. test_plan ──────────────────────────────────────────────────────────
gauntlet_begin_stage test_plan
log "=== 3. test_plan: choudoufu plan with no state file, identities read with kubectl ==="
PLAN_OUT="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$PLAN_OUT" | tail -20; fail "the post-migration plan failed"; }
IDS_OK=1
kca get namespace "$NS" >/dev/null 2>&1 || IDS_OK=0
for spec in "deployment catalog" "deployment public-api" "statefulset quickpizza-db" "service alloy" "configmap alloy-config" "secret alloy-credentials" "serviceaccount alloy"; do
  read -r kind name <<< "$spec"; exists_a "$kind" "$name" || IDS_OK=0
done
kca get clusterrolebinding alloy >/dev/null 2>&1 || IDS_OK=0
if grep -q "No changes." <<< "$PLAN_OUT" && [ "$IDS_OK" = "1" ]; then
  gauntlet_stage test_plan pass "the plan with no state file is empty; a representative set of identities (the namespace, two Deployments, the StatefulSet, a Service, a ConfigMap, a Secret, the ServiceAccount, the ClusterRoleBinding) confirmed present by NAMESPACE/NAME with kubectl"
else
  gauntlet_stage test_plan fail "the plan with no state file is not empty or an identity is missing (ids ok: $IDS_OK): $(grep -E '^Plan:|No changes' <<< "$PLAN_OUT" | head -1)"
  ADOPT_OUT="$(chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$ADOPT_OUT" | tail -20; fail "the converging apply failed"; }
  grep -q "No changes." <<< "$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || fail "the replan after the converging apply is not empty"
fi

# ── 4. test_apply ─────────────────────────────────────────────────────────
gauntlet_begin_stage test_apply
log "=== 4. test_apply: apply the empty plan; the labelled-object count must not move ==="
BEFORE_N="$(count_a)"
NOOP_OUT="$(chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$NOOP_OUT" | tail -20; fail "the no-op apply failed"; }
grep -qF "Apply complete! Resources: 0 added, 0 changed, 0 destroyed" <<< "$NOOP_OUT" || { printf '%s\n' "$NOOP_OUT" | tail -5; fail "the no-op apply changed something"; }
[ "$BEFORE_N" = "$(count_a)" ] || fail "labelled-object count moved across a no-op apply: $BEFORE_N -> $(count_a)"
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed); objects carrying tofu-estate=$ESTATE unchanged at $BEFORE_N across eight kinds, counted with kubectl"

# ── 5. drift_reconverge ───────────────────────────────────────────────────
gauntlet_begin_stage drift_reconverge
log "=== 5. drift_reconverge: kubectl patch the alloy-config ConfigMap on A and B; stock's plan on B is the oracle ==="
kca patch configmap alloy-config -n "$NS" --type merge -p '{"data":{"config.alloy":"tampered"}}' >/dev/null || fail "could not tamper alloy-config on A"
kcb patch configmap alloy-config -n "$NS" --type merge -p '{"data":{"config.alloy":"tampered"}}' >/dev/null || fail "could not tamper alloy-config on B"
if [ "${BREAK:-}" = "1" ]; then
  kca patch configmap postgres-init-script -n "$NS" --type merge -p '{"data":{"init-db-observability.sh":"tampered"}}' >/dev/null || fail "BREAK: could not tamper postgres-init-script on A"
fi
ORACLE_PLAN="$(stock_b plan -detailed-exitcode -input=false -no-color 2>&1)"; ORACLE_RC=$?
[ "$ORACLE_RC" -eq 2 ] || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B after the tamper exited $ORACLE_RC, want 2"; }
grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$ORACLE_PLAN" || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B does not propose exactly one change"; }
grep -q "kubernetes_config_map_v1.alloy_config" <<< "$ORACLE_PLAN" || fail "stock's plan on B does not name kubernetes_config_map_v1.alloy_config"
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock could not reconverge B"
DRIFT_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$DRIFT_PLAN" | tail -20; fail "the plan after the tamper failed"; }
if [ "${BREAK:-}" = "1" ]; then
  grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$DRIFT_PLAN" && fail "BREAK=1: two objects were tampered but the plan still proposes exactly one change"
  log "  BREAK=1: caught - with a second object tampered the plan is $(grep -E '^Plan:' <<< "$DRIFT_PLAN" | head -1)"
  ( chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: could not reconverge A"
  gauntlet_stage drift_reconverge pass "BREAK=1 control: with two objects tampered the single-object assertion correctly fails to hold ($(grep -E '^Plan:' <<< "$DRIFT_PLAN" | head -1)); reconverged afterwards"
else
  grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$DRIFT_PLAN" || { printf '%s\n' "$DRIFT_PLAN" | tail -20; fail "the plan after one tamper does not propose exactly one change"; }
  grep -q "kubernetes_config_map_v1.alloy_config" <<< "$DRIFT_PLAN" || fail "the plan does not name kubernetes_config_map_v1.alloy_config"
  RECONV="$(chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$RECONV" | tail -20; fail "the reconverging apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$RECONV" || fail "the reconverging apply did not change exactly one object"
  [ "$(kca get configmap alloy-config -n "$NS" -o jsonpath='{.data.config\.alloy}' | head -c 8)" != "tampered" ] || fail "alloy-config still reads tampered after reconverging"
  gauntlet_stage drift_reconverge pass "the alloy-config ConfigMap tampered with kubectl patch; choudoufu proposed exactly kubernetes_config_map_v1.alloy_config (0 add, 1 change, 0 destroy), matching stock's own plan on the oracle cluster for the same tamper; apply changed 1 and the config reads back as the file the root ships. BREAK=1 tampers postgres-init-script too and the single-object assertion correctly fails"
fi

# ── 6. plan_approval ──────────────────────────────────────────────────────
gauntlet_begin_stage plan_approval
log "=== 6. plan_approval: the namespace gains a label in configuration; a saved plan, an out-of-band label elsewhere, a refusal ==="
add_reviewed() { perl -0777 -pi -e 's/(resource "kubernetes_namespace_v1" "quickpizza" \{\n  metadata \{\n    name = var\.quickpizza_kubernetes_namespace\n)/$1    labels = { reviewed = "yes" }\n/' "$1/terraform/main.tf"; grep -q 'reviewed = "yes"' "$1/terraform/main.tf" || fail "the reviewed-label edit did not match main.tf - the corpus pin has moved"; }
add_reviewed "$ADOPTED"; add_reviewed "$ORACLE"
P_PLAN="$(chdf_a "$ADOPTED" plan -out=approved.tfplan -input=false -no-color 2>&1)" || { printf '%s\n' "$P_PLAN" | tail -20; fail "plan -out failed"; }
grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$P_PLAN" || { printf '%s\n' "$P_PLAN" | tail -10; fail "the saved plan is not exactly one change"; }
kca label service catalog -n "$NS" stray=yes >/dev/null || fail "could not move the world (label service/catalog) on A"
P_APPLY="$(chdf_a "$ADOPTED" apply -input=false -no-color approved.tfplan 2>&1)"; P_RC=$?
if [ "${BREAK_APPROVAL:-}" = "1" ]; then
  [ "$P_RC" -eq 0 ] && fail "BREAK_APPROVAL=1: applying the saved plan after the world moved succeeded"
  log "  BREAK_APPROVAL=1: caught - the apply after the world moved exited $P_RC"
  kca label service catalog -n "$NS" stray- >/dev/null
  ( chdf_a "$ADOPTED" apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: the saved plan did not apply once the world was put back"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: stock could not apply the reviewed label on B"
  gauntlet_stage plan_approval pass "BREAK_APPROVAL=1 control: applying the saved plan after the world moved exited $P_RC (refused), so the stage's own Break line correctly fails; applied once the world was put back"
else
  [ "$P_RC" -eq 3 ] || { printf '%s\n' "$P_APPLY" | tail -20; fail "apply of the saved plan after the world moved exited $P_RC, want 3"; }
  grep -qF "The approved plan no longer matches the live system" <<< "$P_APPLY" || { printf '%s\n' "$P_APPLY" | tail -20; fail "the refusal does not carry its documented sentence"; }
  [ -z "$(kca get namespace "$NS" -o jsonpath='{.metadata.labels.reviewed}')" ] || fail "the namespace gained reviewed despite the refusal"
  kca label service catalog -n "$NS" stray- >/dev/null || fail "could not put the world back"
  P_APPLY2="$(chdf_a "$ADOPTED" apply -input=false -no-color approved.tfplan 2>&1)" || { printf '%s\n' "$P_APPLY2" | tail -20; fail "the saved plan did not apply once the world was put back"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$P_APPLY2" || fail "the saved plan's apply did not change exactly one object"
  [ "$(kca get namespace "$NS" -o jsonpath='{.metadata.labels.reviewed}')" = "yes" ] || fail "the namespace does not read reviewed=yes after the saved plan applied"
  ( stock_b plan -out=approved.tfplan -input=false -no-color >/dev/null 2>&1 && stock_b apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "stock's own planfile did not apply on B"
  gauntlet_stage plan_approval pass "plan -out wrote one update (the namespace gains reviewed=yes); the world then moved out of band (a stray label on the catalog Service, kubectl, never choudoufu) and apply of the saved plan refused with \"The approved plan no longer matches the live system\" at exit 3, nothing applied; with the label removed the identical file applied, 0 added, 1 changed, 0 destroyed, and reviewed=yes reads back; stock's own planfile applied on the oracle cluster in the unchanged case. BREAK_APPROVAL=1 expects success after the move and correctly fails"
fi

# ── 7. day2_rename ────────────────────────────────────────────────────────
gauntlet_begin_stage day2_rename
log "=== 7. day2_rename: kubernetes_service_account_v1.alloy becomes .collector through a moved block; its two references follow ==="
rename_sa() { # $1 root: the block and both references, plus the moved block
  perl -pi -e 's/kubernetes_service_account_v1" "alloy"/kubernetes_service_account_v1" "collector"/; s/kubernetes_service_account_v1\.alloy\./kubernetes_service_account_v1.collector./g' "$1/terraform/alloy.tf"
  grep -q '"collector"' "$1/terraform/alloy.tf" || fail "the rename did not match alloy.tf - the corpus pin has moved"
  grep -q 'kubernetes_service_account_v1.alloy\b' "$1/terraform/alloy.tf" && fail "a reference to kubernetes_service_account_v1.alloy survived the rename"
  printf '\nmoved {\n  from = kubernetes_service_account_v1.alloy\n  to   = kubernetes_service_account_v1.collector\n}\n' >> "$1/terraform/alloy.tf"
}
if [ "${BREAK:-}" = "1" ]; then
  perl -0777 -pi -e 's/(resource "kubernetes_service_account_v1" "alloy" \{\n  metadata \{\n    name      = )"alloy"/$1"alloy-renamed"/' "$ADOPTED/terraform/alloy.tf"
  grep -q '"alloy-renamed"' "$ADOPTED/terraform/alloy.tf" || fail "BREAK: could not rename the ServiceAccount's metadata.name"
  R_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK: the plan after renaming the object failed"; }
  grep -qE "^No changes|Plan: 0 to add, 0 to change, 0 to destroy" <<< "$R_PLAN" && fail "BREAK=1: renaming the object's own name still planned zero churn"
  grep -q "1 to add" <<< "$R_PLAN" && grep -q "1 to destroy" <<< "$R_PLAN" || { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK=1: renaming the object's own name did not plan a destroy and a create: $(grep -E '^Plan:' <<< "$R_PLAN" | head -1)"; }
  log "  BREAK=1: caught - renaming metadata.name plans $(grep -E '^Plan:' <<< "$R_PLAN" | head -1)"
  perl -pi -e 's/"alloy-renamed"/"alloy"/' "$ADOPTED/terraform/alloy.tf"
  rename_sa "$ADOPTED"; rename_sa "$ORACLE"
  ( chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: the moved-block apply failed"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: stock's moved-block apply failed on B"
  gauntlet_stage day2_rename pass "BREAK=1 control: renaming the object's own metadata.name plans a replace ($(grep -E '^Plan:' <<< "$R_PLAN" | head -1)), so the zero-churn assertion correctly fails to hold; a bare block rename is zero churn on this substrate because the block name is not part of the object's identity; the moved block then applied"
else
  rename_sa "$ADOPTED"; rename_sa "$ORACLE"
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's moved-block plan failed on B"; }
  grep -qE "^No changes|Plan: 0 to add, 0 to change, 0 to destroy" <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's moved-block plan on B is not zero churn"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's moved-block apply failed on B"
  R_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_PLAN" | tail -20; fail "the moved-block plan failed"; }
  grep -qE "^No changes|Plan: 0 to add, 0 to change, 0 to destroy" <<< "$R_PLAN" || { printf '%s\n' "$R_PLAN" | tail -20; fail "the moved-block plan is not zero churn"; }
  ( chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "the moved-block apply failed"
  exists_a serviceaccount alloy || fail "the ServiceAccount is gone after the rename"
  [ "$(count_a)" = "26" ] || fail "$(count_a) labelled objects after the rename, want 26"
  gauntlet_stage day2_rename pass "moved block: kubernetes_service_account_v1.alloy -> .collector, with the ClusterRoleBinding's subject and the Deployment's service_account_name references following, zero churn (no add, no change, no destroy); the live object untouched and still labelled; stock's plan for the same moved block on the oracle cluster is also zero churn. The moved-block half only: live-mv has no Kubernetes leg (#1066). BREAK=1 renames the object's own metadata.name and the zero-churn assertion correctly fails"
fi

# ── 8. day2_remove ────────────────────────────────────────────────────────
gauntlet_begin_stage day2_remove
log "=== 8. day2_remove: the ClusterRoleBinding block leaves the configuration ==="
remove_crb() { python3 - "$1/terraform/alloy.tf" <<'PY'
import re, sys
p = sys.argv[1]; s = open(p).read()
s2 = re.sub(r'// Grant the "view" ClusterRole.*?\nresource "kubernetes_cluster_role_binding_v1" "alloy" \{.*?\n\}\n', '', s, count=1, flags=re.S)
assert s2 != s and 'kubernetes_cluster_role_binding_v1' not in s2, "the ClusterRoleBinding block did not match alloy.tf - the corpus pin has moved"
open(p, 'w').write(s2)
PY
}
if [ "${BREAK_REMOVE:-}" = "1" ]; then
  K_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || fail "BREAK_REMOVE: the plan with the block kept failed"
  grep -qE "will be destroyed|[1-9][0-9]* to destroy" <<< "$K_PLAN" && fail "BREAK_REMOVE=1: with the block kept a destroy was still proposed"
  log "  BREAK_REMOVE=1: caught - with the block kept the plan proposes no destroy"
  gauntlet_stage day2_remove pass "BREAK_REMOVE=1 control: with the ClusterRoleBinding block kept, no destroy is proposed; the real check is skipped"
  remove_crb "$ADOPTED" || fail "BREAK_REMOVE: could not remove the block afterwards"; remove_crb "$ORACLE" || fail "BREAK_REMOVE: could not remove the block from the oracle root afterwards"
  ( chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_REMOVE: the removal apply failed afterwards"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_REMOVE: stock's removal apply failed on B afterwards"
else
  remove_crb "$ADOPTED" || fail "could not remove the ClusterRoleBinding block from the adopted root"
  remove_crb "$ORACLE" || fail "could not remove the ClusterRoleBinding block from the oracle root"
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's remove plan failed on B"; }
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's remove plan on B is not exactly one destroy"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's remove apply failed on B"
  D_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$D_PLAN" | tail -20; fail "the remove plan failed"; }
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$D_PLAN" || { printf '%s\n' "$D_PLAN" | tail -20; fail "the remove plan is not exactly one destroy"; }
  D_LINE="$(grep -E '^[[:space:]]*# .* will be destroyed' <<< "$D_PLAN" | head -1)"
  D_ADDR="$(sed -E 's/^[[:space:]#]*//; s/ will be destroyed.*$//' <<< "$D_LINE")"
  grep -qE '^kubernetes_cluster_role_binding(_v1)?\.orphan_.*alloy$' <<< "$D_ADDR" || { printf '%s\n' "$D_PLAN" | grep -E 'destroyed|^Plan:'; fail "the one destroy is ${D_ADDR:-unnamed}, not the ClusterRoleBinding at its orphan address"; }
  D_APPLY="$(chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$D_APPLY" | tail -20; fail "the remove apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 0 changed, 1 destroyed" <<< "$D_APPLY" || fail "the remove apply did not destroy exactly one object"
  kca get clusterrolebinding alloy >/dev/null 2>&1 && fail "the ClusterRoleBinding still exists after the remove apply"
  grep -q "No changes." <<< "$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || fail "the replan after the remove is not empty"
  [ "$(count_a)" = "25" ] || fail "$(count_a) labelled objects after the remove, want 25"
  gauntlet_stage day2_remove pass "deleting kubernetes_cluster_role_binding_v1.alloy's block proposed exactly one destroy (0 add, 0 change, 1 destroy) at the sweep's orphan address $D_ADDR - a cluster-scoped object found by its label, which carries no address - applied cleanly, the object gone (kubectl get clusterrolebinding: NotFound), the next plan empty; stock's plan for the same removal on the oracle cluster is also exactly one destroy; the Deployments' ReplicaSets and Pods and the StatefulSet's Pod and PVC, which carry no estate label, were never proposed. BREAK_REMOVE=1 keeps the block and no destroy is proposed"
fi

# ── 9. day2_count ─────────────────────────────────────────────────────────
gauntlet_begin_stage day2_count
log "=== 9. day2_count: a two-instance count ConfigMap the script declares scales 2 -> 1 -> 2 ==="
write_shards() { # $1 root, $2 count
  cat > "$1/terraform/count_test.tf" <<EOF
# Added by live/e2e/corpus-quickpizza/run.sh for the gauntlet's day2_count
# stage: the published root declares no count block of its own.
resource "kubernetes_config_map_v1" "shard" {
  count = $2
  metadata {
    name      = "shard-\${count.index}"
    namespace = kubernetes_namespace_v1.quickpizza.id
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
grep -qE "^kubernetes_config_map(_v1)?\.orphan_${NS}_shard-1$" <<< "$C_ADDR" || { printf '%s\n' "$C_PLAN" | grep -E 'destroyed|^Plan:'; fail "the scale-down destroys ${C_ADDR:-nothing named}, not shard-1 at its orphan address"; }
( chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1 | grep -qF "0 added, 0 changed, 1 destroyed" ) || fail "the scale-down apply did not destroy exactly one object"
if [ "${BREAK_COUNT:-}" = "1" ]; then
  exists_a configmap shard-0 || fail "BREAK_COUNT=1: shard-0 was destroyed - the 'wrong instance' assertion would hold"
  log "  BREAK_COUNT=1: caught - shard-0 still exists, so asserting it was the one destroyed correctly fails"
  gauntlet_stage day2_count pass "BREAK_COUNT=1 control: asserting the lower index (shard-0) was destroyed correctly fails to hold; the real check is skipped"
else
  exists_a configmap shard-0 || fail "shard-0 was destroyed on the scale-down"
  exists_a configmap shard-1 && fail "shard-1 still exists after the scale-down"
  write_shards "$ADOPTED" 2; write_shards "$ORACLE" 2
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's scale-up plan failed on B"
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's scale-up plan on B is not exactly one add"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's scale-up apply failed on B"
  U_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$U_PLAN" | tail -20; fail "the scale-up plan failed"; }
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$U_PLAN" || { printf '%s\n' "$U_PLAN" | tail -20; fail "the scale-up plan is not exactly one add"; }
  grep -q 'kubernetes_config_map_v1.shard\[1\]' <<< "$U_PLAN" || fail "the scale-up does not create shard[1]"
  ( chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1 | grep -qF "1 added, 0 changed, 0 destroyed" ) || fail "the scale-up apply did not create exactly one object"
  exists_a configmap shard-0 && exists_a configmap shard-1 || fail "both shards do not exist after the scale-up"
  grep -q "No changes." <<< "$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || fail "the replan after the scale-up is not empty"
  gauntlet_stage day2_count pass "a two-instance count ConfigMap added beside the published root (the estate's own shape has no count block): scaling 2 to 1 destroyed exactly shard-1, planned at the sweep's orphan address $C_ADDR since the label carries no index (shard-0 untouched, both read with kubectl); back to 2 created exactly kubernetes_config_map_v1.shard[1] under the same name; the next plan is empty; stock's plans for the same two changes on the oracle cluster have the identical shape. BREAK_COUNT=1 asserts the lower index was destroyed and correctly fails"
fi

# ── 10. day2_teardown ─────────────────────────────────────────────────────
gauntlet_begin_stage day2_teardown
log "=== 10. day2_teardown: apply -destroy on the adopted estate, and stock's own destroy on B ==="
T_EXPECT="$(count_a)"
T_OUT="$(chdf_a "$ADOPTED" apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$T_OUT" | tail -20; fail "apply -destroy failed"; }
grep -qF "Resources: 0 added, 0 changed, $T_EXPECT destroyed" <<< "$T_OUT" || { printf '%s\n' "$T_OUT" | tail -5; fail "apply -destroy did not remove exactly the $T_EXPECT remaining objects"; }
for _ in $(seq 1 60); do kca get namespace "$NS" >/dev/null 2>&1 || break; sleep 2; done
kca get namespace "$NS" >/dev/null 2>&1 && fail "the $NS namespace still exists after the destroy"
[ "$(count_a)" = "0" ] || fail "$(count_a) object(s) still carry tofu-estate=$ESTATE after the destroy"
O_EXPECT="$(stock_b state list 2>/dev/null | wc -l | tr -d ' ')"
O_T="$(stock_b apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$O_T" | tail -10; fail "stock's destroy failed on B"; }
grep -qF "Resources: 0 added, 0 changed, $O_EXPECT destroyed" <<< "$O_T" || fail "stock's destroy on B did not remove exactly the $O_EXPECT objects its state held"
gauntlet_stage day2_teardown pass "apply -destroy removed exactly the $T_EXPECT remaining objects in one apply, in an order the API server accepted (the namespace last), the namespace gone and no object of any of the estate's eight kinds carrying tofu-estate=$ESTATE (kubectl, every namespace); stock's destroy of the same estate on the oracle cluster also removed exactly the $O_EXPECT its state held"

# ── 11. greenfield ────────────────────────────────────────────────────────
gauntlet_begin_stage greenfield
log "=== 11. greenfield: choudoufu applies the published root fresh, with a live block, on the now-empty cluster A ==="
write_root "$GREEN" live
( chdf_a "$GREEN" init -input=false -no-color >/dev/null 2>&1 ) || fail "greenfield init failed"
G_OUT="$(chdf_a "$GREEN" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$G_OUT" | tail -20; fail "greenfield apply failed"; }
grep -qF "Apply complete! Resources: 26 added, 0 changed, 0 destroyed" <<< "$G_OUT" || fail "greenfield apply did not add exactly 26 objects"
[ ! -f "$GREEN/terraform/terraform.tfstate" ] || fail "a terraform.tfstate appeared after a live-block apply"
[ "$(count_a)" = "26" ] || fail "$(count_a) object(s) carry tofu-estate=$ESTATE after the greenfield apply, want 26"
G_RECORDS="$(gauntlet_record_count "$GREEN/terraform/.tofu-records")"
grep -q "No changes." <<< "$(chdf_a "$GREEN" plan -input=false -no-color 2>&1)" || fail "the greenfield replan is not empty"
rm -f "$GREEN/terraform/.terraform/choudoufu-cache.tfstate"
grep -q "No changes." <<< "$(chdf_a "$GREEN" plan -input=false -no-color 2>&1)" || fail "the greenfield replan without the cache is not empty"
DROP=""; [ "${BREAK:-}" = "1" ] && DROP="statefulset/quickpizza-db"
inventory "$KCA" "$DROP" > "$WORK/inventory.green.json" || fail "could not read the greenfield inventory"
if [ "${BREAK:-}" = "1" ]; then
  diff -q "$WORK/inventory.stock.json" "$WORK/inventory.green.json" >/dev/null && fail "BREAK=1: with the StatefulSet dropped the two inventories still match"
  log "  BREAK=1: caught - the inventories differ once the StatefulSet is dropped"
  gauntlet_stage greenfield pass "BREAK=1 control: dropping the Postgres StatefulSet from the greenfield inventory makes the object-by-object comparison correctly fail; the estate applied (26 added, no terraform.tfstate) and replanned empty with and without the cache"
else
  if ! diff -u "$WORK/inventory.stock.json" "$WORK/inventory.green.json"; then
    fail "the greenfield inventory differs from stock's cold-deploy inventory (diff above)"
  fi
  gauntlet_stage greenfield pass "the published root applied fresh with a live block and no terraform.tfstate: 26 objects, every one labelled tofu-estate=$ESTATE (kubectl, eight kinds); the record store held $G_RECORDS file(s); replanned empty with and without the cache; the cluster's inventory (ConfigMap and Secret keys, every Service's ports and selector, every Deployment's replicas, containers and service account, the StatefulSet's containers and claim, the ClusterRoleBinding's role and subjects, the namespace) matches stock's cold deploy on the same cluster object by object, labels never compared. BREAK=1 drops the StatefulSet from the expected inventory and the match correctly fails"
fi
( chdf_a "$GREEN" apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "greenfield teardown failed"

# ── 12. strict ────────────────────────────────────────────────────────────
gauntlet_begin_stage strict
STRICT="$WORK/strict/terraform"; mkdir -p "$STRICT"
strict_block() { cat <<EOF
terraform {
  required_providers {
    random = {
      source  = "hashicorp/random"
      version = ">= 3.0"
    }
  }
  live {
    estate = "corpus-quickpizza-strict"
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
log "=== 12. strict: every strict toggle on ==="
strict_block "refuse" > "$STRICT/main.tf"
( chdf_a "$WORK/strict" init -input=false -no-color >/dev/null 2>&1 ) || fail "choudoufu init for the strict-stage scratch estate failed"
STRICT_ON="$(chdf_a "$WORK/strict" plan -input=false -no-color 2>&1)"; STRICT_ON_RC=$?
if [ "${BREAK_STRICT:-}" = "1" ]; then
  strict_block "store" > "$STRICT/main.tf"
  STRICT_OFF="$(chdf_a "$WORK/strict" plan -input=false -no-color 2>&1)"; STRICT_OFF_RC=$?
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
log "corpus-quickpizza: done"
