#!/usr/bin/env bash
# reference-k8s: the kubernetes lane's first estate (#1067, under #1016's
# ruling), crossed on the kind substrate. A hand-written reference shape
# kept in this repository, built on live/e2e/estate-k8s - a namespace, a
# ConfigMap, a ServiceAccount, a Service - plus a Deployment on the node
# image's own pause container (no image pull) and a two-instance count
# ConfigMap for the count stage. Seven objects, five kinds, no AWS provider
# anywhere. A published Kubernetes-only root is the lane's next estate; the
# provider's own examples were too thin or on a retired API to be it.
#
# Two kind clusters, both created for this run and deleted after it:
#
#   A  the estate. Stock terraform cold-deploys it (cold_deploy), choudoufu
#      adopts it from stock's state (migrate) and runs every day-2 stage on
#      it, tears it down, then applies the same shape fresh with a live
#      block (greenfield) and compares the cluster with what stock's cold
#      deploy left.
#   B  the oracle. Stock terraform cold-deploys the identical shape and
#      applies every day-2 change itself, so each stage's "what stock does
#      for the same change" is stock's own plan on its own cluster, never
#      a state file choudoufu has moved on from.
#
# What reads differently on this substrate, per tools/gauntlet/stages.go's
# Substrates notes: identities are NAMESPACE/NAME read with kubectl; the
# marker count is `kubectl get <kind> -A -l tofu-estate=<estate>` summed over
# the estate's five kinds; the out-of-band mutation is a kubectl patch or
# label; the rename is the moved-block half only, since live-mv has no
# Kubernetes leg (#1066). day2_replace and day2_crash do not apply (a name
# is unique within its namespace, so nothing can be created before the
# object it replaces is gone) and are recorded n/a by the runner, not by
# this script.
#
# A stage this script cannot pass records fail with the reason and the run
# continues to the next; the runner, not the script, decides what "clear"
# means. The one exception is a fault in the substrate itself (a cluster
# that never came up), which is a fail on the stage being set up and an
# exit.
#
#   go run ./tools/gauntlet run reference-k8s     # one estate, a local kind cluster: no allow file
#   bash live/e2e/reference-k8s/run.sh
#
# Needs kind, kubectl, terraform (the stock binary) and Docker on PATH.
#
# Env overrides:
#   TOFU_BIN       path to a prebuilt choudoufu binary; skips the `go build`.
#   BREAK          set to 1 to run drift_reconverge's, day2_rename's and
#                  greenfield's negative controls instead of the real checks:
#                  a second object is tampered and the single-object
#                  assertion must fail; the ServiceAccount's own
#                  metadata.name is changed (a block rename without a moved
#                  block is zero churn on Kubernetes, because the block name
#                  is not part of the object's identity) and the zero-churn
#                  assertion must fail; the
#                  Deployment is dropped from greenfield's expected inventory
#                  and the object-by-object match must fail.
#   BREAK_REMOVE   set to 1 to keep the ServiceAccount block and assert no
#                  destroy is proposed (day2_remove's own Break line).
#   BREAK_COUNT    set to 1 to assert the wrong instance was destroyed on the
#                  scale-down (day2_count's Break line); must fail.
#   BREAK_APPROVAL set to 1 to apply the saved plan after the world moved and
#                  expect success (plan_approval's Break line); must fail.
#   BREAK_STRICT   set to 1 to turn secrets back to "store" and require the
#                  refusal to vanish (strict's Break line).
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
source "$ROOT/live/e2e/lib/gauntlet.sh"
ESTATE="reference-k8s"
NS="refk8s"
KINDS="namespaces configmaps serviceaccounts services deployments"
WORK="$(mktemp -d)"
STOCK="$WORK/stock"; ADOPTED="$WORK/adopted"; ORACLE="$WORK/oracle"; GREEN="$WORK/green"
KCA="$WORK/a.kubeconfig"; KCB="$WORK/b.kubeconfig"
CLUSTER_A="chdf-refk8s-a-$$"; CLUSTER_B="chdf-refk8s-b-$$"
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

# ── 0. tools ─────────────────────────────────────────────────────────────
log "=== 0. tools ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v terraform >/dev/null 2>&1 || fail "the terraform binary is not on PATH - needed as the stock oracle"
command -v kind >/dev/null 2>&1 || fail "kind is not on PATH (brew install kind)"
command -v kubectl >/dev/null 2>&1 || fail "kubectl is not on PATH"
command -v python3 >/dev/null 2>&1 || fail "python3 is not on PATH"

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

# ── the shape ────────────────────────────────────────────────────────────
versions_block() { # $1 = "live" to include the live block, anything else for stock
  cat <<EOF
terraform {
  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "= 3.2.1"
    }
  }
EOF
  if [ "$1" = "live" ]; then cat <<EOF
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
}
# resource_block writes the seven objects. $1 is the ServiceAccount's block
# name (app, then team after the rename); $2 is the shard count; $3 is an
# extra data line for app-config (plan_approval's reviewed = "yes").
resource_block() {
  local sa="${1:-app}" shards="${2:-2}" extra="${3:-}"
  cat <<EOF
resource "kubernetes_namespace" "app" {
  metadata {
    name = "$NS"
  }
}

resource "kubernetes_config_map" "app" {
  metadata {
    name      = "app-config"
    namespace = "$NS"
  }
  data = {
    greeting = "hello"
$extra
  }
  depends_on = [kubernetes_namespace.app]
}

resource "kubernetes_service_account" "$sa" {
  metadata {
    name      = "app"
    namespace = "$NS"
  }
  depends_on = [kubernetes_namespace.app]
}

resource "kubernetes_service" "app" {
  metadata {
    name      = "app"
    namespace = "$NS"
  }
  spec {
    selector = {
      app = "web"
    }
    port {
      port        = 80
      target_port = 8080
    }
  }
  depends_on = [kubernetes_namespace.app]
}

resource "kubernetes_deployment" "web" {
  metadata {
    name      = "web"
    namespace = "$NS"
  }
  spec {
    replicas = 1
    selector {
      match_labels = { app = "web" }
    }
    template {
      metadata {
        labels = { app = "web" }
      }
      spec {
        container {
          name  = "pause"
          image = "registry.k8s.io/pause:3.10"
        }
      }
    }
  }
  wait_for_rollout = false
  depends_on       = [kubernetes_namespace.app]
}

resource "kubernetes_config_map" "shard" {
  count = $shards
  metadata {
    name      = "shard-\${count.index}"
    namespace = "$NS"
  }
  data = { shard = tostring(count.index) }
  depends_on = [kubernetes_namespace.app]
}
EOF
}
write_config() { # $1 dir, $2 live|stock, then resource_block's args
  local dir="$1" mode="$2"; shift 2
  { versions_block "$mode"; echo; resource_block "$@"; } > "$dir/main.tf"
}

# ── cluster helpers ──────────────────────────────────────────────────────
kca() { kubectl --kubeconfig "$KCA" "$@"; }
kcb() { kubectl --kubeconfig "$KCB" "$@"; }
# stock_b runs the stock binary in the oracle root against cluster B.
stock_b() { ( cd "$ORACLE" && KUBECONFIG="$KCB" KUBE_CONFIG_PATH="$KCB" terraform "$@" ); }
# count_a is the marker count on cluster A, the tagging index's equivalent.
count_a() { KUBECONFIG="$KCA" gauntlet_kind_count "$ESTATE" $KINDS; }
# inventory prints the estate's objects on the cluster $1 names, normalised:
# what the configuration declares and nothing the server set, labels
# included, so stock's cold deploy and choudoufu's greenfield apply compare
# object by object. $2 is a name to drop (BREAK's control).
inventory() {
  local cfg="$1" drop="${2:-}"
  KUBECONFIG="$cfg" DROP="$drop" NS="$NS" python3 - <<'PY'
import json, os, subprocess, sys
ns = os.environ["NS"]; drop = os.environ.get("DROP", "")
def get(kind, name):
    out = subprocess.run(["kubectl", "get", kind, name, "-n", ns, "-o", "json"], capture_output=True, text=True)
    if out.returncode != 0:
        return None
    return json.loads(out.stdout)
inv = {}
nsobj = subprocess.run(["kubectl", "get", "namespace", ns, "-o", "json"], capture_output=True, text=True)
inv["namespace/" + ns] = {"exists": nsobj.returncode == 0}
for name in ["app-config", "shard-0", "shard-1"]:
    o = get("configmap", name)
    inv["configmap/" + name] = None if o is None else {"data": o.get("data", {})}
o = get("serviceaccount", "app")
inv["serviceaccount/app"] = None if o is None else {"exists": True}
o = get("service", "app")
inv["service/app"] = None if o is None else {
    "type": o["spec"].get("type"), "selector": o["spec"].get("selector"),
    "ports": [{"port": p.get("port"), "targetPort": p.get("targetPort"), "protocol": p.get("protocol")} for p in o["spec"].get("ports", [])]}
o = get("deployment", "web")
inv["deployment/web"] = None if o is None else {
    "replicas": o["spec"].get("replicas"), "selector": o["spec"].get("selector", {}).get("matchLabels"),
    "containers": [{"name": c["name"], "image": c["image"]} for c in o["spec"]["template"]["spec"]["containers"]]}
if drop:
    inv.pop(drop, None)
print(json.dumps(inv, sort_keys=True, indent=1))
PY
}
exists_a() { kca get "$1" "$2" -n "$NS" >/dev/null 2>&1; }

# ── 1. cold_deploy: stock stands the estate up on A (and B, the oracle) ──
gauntlet_begin_stage cold_deploy
log "=== 1. cold_deploy: two kind clusters, stock terraform applies the shape on each ==="
gauntlet_kind_up "$CLUSTER_A" "$KCA" || fail "kind cluster A ($CLUSTER_A) did not come up"
gauntlet_kind_up "$CLUSTER_B" "$KCB" || fail "kind cluster B ($CLUSTER_B) did not come up"
export KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA"
log "  cluster A: $(kca version 2>/dev/null | grep -i server | head -1); cluster B: $CLUSTER_B"
mkdir -p "$STOCK" "$ORACLE"
write_config "$STOCK" stock
write_config "$ORACLE" stock
( cd "$STOCK" && terraform init -input=false -no-color >/dev/null 2>&1 ) || fail "stock init failed on A"
COLD_OUT="$(cd "$STOCK" && terraform apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$COLD_OUT" | tail -20; fail "stock cold deploy failed on A"; }
grep -qF "Apply complete! Resources: 7 added, 0 changed, 0 destroyed" <<< "$COLD_OUT" || { printf '%s\n' "$COLD_OUT" | tail -5; fail "stock cold deploy did not add exactly 7 objects on A"; }
[ -f "$STOCK/terraform.tfstate" ] || fail "stock left no terraform.tfstate on A"
STOCK_N="$(cd "$STOCK" && terraform state list | wc -l | tr -d ' ')"
[ "$STOCK_N" = "7" ] || fail "stock's state holds $STOCK_N instances, want 7"
UNMARKED="$(count_a)"
[ "$UNMARKED" = "0" ] || fail "$UNMARKED object(s) already carry tofu-estate=$ESTATE after a plain stock apply - this proves nothing"
inventory "$KCA" > "$WORK/inventory.stock.json" || fail "could not read the cold-deployed inventory on A"
( stock_b init -input=false -no-color >/dev/null 2>&1 ) || fail "stock init failed on B"
( stock_b apply -auto-approve -input=false -no-color 2>&1 | grep -qF "Apply complete! Resources: 7 added" ) || fail "stock cold deploy failed on B"
log "  7 objects from plain terraform on A, a real terraform.tfstate, zero labels; the same 7 on B for the oracle"
gauntlet_stage cold_deploy pass "7 objects (namespace, 3 ConfigMaps, ServiceAccount, Service, Deployment) from plain terraform against kind $(kca version 2>/dev/null | grep -io 'v1\.[0-9.]*' | head -1), a real terraform.tfstate with 7 instances, zero tofu-estate labels read back with kubectl; the identical shape cold-deployed by stock on a second cluster as every later stage's oracle"

# ── 2. migrate: choudoufu live-import against stock's state ──────────────
gauntlet_begin_stage migrate
log "=== 2. migrate: live-import against the stock state file, read-only then -approve ==="
mkdir -p "$ADOPTED"
write_config "$ADOPTED" live
( cd "$ADOPTED" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "adopted init failed"
IMPORT_OUT="$(cd "$ADOPTED" && "$TOFU" live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -no-color 2>&1)" || { printf '%s\n' "$IMPORT_OUT" | tail -20; fail "live-import (dry run) failed"; }
ELIGIBLE_LINE="$(grep -E 'resource instance\(s\) are eligible for stamping' <<< "$IMPORT_OUT" | head -1)"
log "  dry run: ${ELIGIBLE_LINE:-no eligibility line}"
APPROVE_OUT="$(cd "$ADOPTED" && "$TOFU" live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -approve -no-color 2>&1)" || { printf '%s\n' "$APPROVE_OUT" | tail -20; fail "live-import -approve failed"; }
SUMMARY_LINE="$(grep -E 'resource\(s\) newly stamped' <<< "$APPROVE_OUT" | head -1 | sed 's/\.$//')"
log "  approve: ${SUMMARY_LINE:-no summary line}"
MIGRATED=0
if grep -qF "7 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 0 skipped" <<< "$APPROVE_OUT"; then
  LABELLED="$(count_a)"
  if [ "$LABELLED" = "7" ]; then
    MIGRATED=1
    gauntlet_stage migrate pass "7 of 7 stamped, 0 skipped; every object carries tofu-estate=$ESTATE, read back with kubectl"
  else
    gauntlet_stage migrate fail "live-import reported 7 stamped but only $LABELLED object(s) carry tofu-estate=$ESTATE on the cluster"
  fi
else
  gauntlet_stage migrate fail "live-import -approve wrote no label: ${SUMMARY_LINE:-no summary line}. It classes every kubernetes_* type as UNTAGGABLE (\"has no tags argument in the provider's schema\") because ratify.go's carrier is the AWS tags surface and does not know the label surface markers.LabelSurface added in #1061; a Kubernetes object binds by namespace and name, and the next plan proposes writing the label itself (marker repair), so adoption here is one apply away rather than one live-import away (#1073)"
fi

# ── 3. test_plan: replan from nothing ────────────────────────────────────
gauntlet_begin_stage test_plan
log "=== 3. test_plan: choudoufu plan with no state file, identities read with kubectl ==="
PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$PLAN_OUT" | tail -20; fail "the post-migration plan failed"; }
IDS_OK=1
for spec in "namespace $NS" "configmap app-config" "serviceaccount app" "service app" "deployment web" "configmap shard-0" "configmap shard-1"; do
  read -r kind name <<< "$spec"
  if [ "$kind" = "namespace" ]; then kca get namespace "$name" >/dev/null 2>&1 || IDS_OK=0
  else exists_a "$kind" "$name" || IDS_OK=0; fi
done
if grep -q "No changes." <<< "$PLAN_OUT" && [ "$IDS_OK" = "1" ]; then
  gauntlet_stage test_plan pass "the plan with no state file is empty; all 7 identities (NAMESPACE/NAME) confirmed present with kubectl"
else
  PLAN_LINE="$(grep -E '^Plan:|No changes' <<< "$PLAN_OUT" | head -1 | sed 's/\.$//')"
  gauntlet_stage test_plan fail "the plan with no state file is not empty: ${PLAN_LINE:-no plan line}. Every object bound by namespace and name (identities confirmed with kubectl: $IDS_OK), and the changes are the tofu-estate label live-import never wrote (see migrate)"
  log "  adopting through choudoufu's own apply so the day-2 stages below run on a labelled estate"
  ADOPT_OUT="$(cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$ADOPT_OUT" | tail -20; fail "the adopting apply failed"; }
  REPLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)" || fail "the replan after the adopting apply failed"
  grep -q "No changes." <<< "$REPLAN" || { printf '%s\n' "$REPLAN" | tail -20; fail "the replan after the adopting apply is not empty; nothing below would measure day-2 behaviour"; }
fi
[ "$(count_a)" = "7" ] || fail "$(count_a) object(s) carry tofu-estate=$ESTATE after adoption, want 7"

# ── 4. test_apply: no-op apply, marker count unchanged ───────────────────
gauntlet_begin_stage test_apply
log "=== 4. test_apply: apply the empty plan; the labelled-object count must not move ==="
BEFORE_N="$(count_a)"
NOOP_OUT="$(cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$NOOP_OUT" | tail -20; fail "the no-op apply failed"; }
grep -qF "Apply complete! Resources: 0 added, 0 changed, 0 destroyed" <<< "$NOOP_OUT" || { printf '%s\n' "$NOOP_OUT" | tail -5; fail "the no-op apply changed something"; }
AFTER_N="$(count_a)"
[ "$BEFORE_N" = "$AFTER_N" ] || fail "labelled-object count moved across a no-op apply: $BEFORE_N -> $AFTER_N"
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed); objects carrying tofu-estate=$ESTATE unchanged at $BEFORE_N across the estate's five kinds, counted with kubectl"

# ── 5. drift_reconverge: one object tampered out of band ─────────────────
gauntlet_begin_stage drift_reconverge
log "=== 5. drift_reconverge: kubectl patch one ConfigMap on A and on B; stock's plan on B is the oracle ==="
kca patch configmap app-config -n "$NS" --type merge -p '{"data":{"greeting":"tampered"}}' >/dev/null || fail "could not tamper app-config on A"
kcb patch configmap app-config -n "$NS" --type merge -p '{"data":{"greeting":"tampered"}}' >/dev/null || fail "could not tamper app-config on B"
if [ "${BREAK:-}" = "1" ]; then
  kca patch configmap shard-0 -n "$NS" --type merge -p '{"data":{"shard":"tampered"}}' >/dev/null || fail "BREAK: could not tamper shard-0 on A"
fi
ORACLE_PLAN="$(stock_b plan -detailed-exitcode -input=false -no-color 2>&1)"; ORACLE_RC=$?
[ "$ORACLE_RC" -eq 2 ] || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B after the tamper exited $ORACLE_RC, want 2 (changes)"; }
grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$ORACLE_PLAN" || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B does not propose exactly one change"; }
grep -q "kubernetes_config_map.app" <<< "$ORACLE_PLAN" || fail "stock's plan on B does not name kubernetes_config_map.app"
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock could not reconverge B"
DRIFT_PLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$DRIFT_PLAN" | tail -20; fail "the plan after the tamper failed"; }
if [ "${BREAK:-}" = "1" ]; then
  if grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$DRIFT_PLAN"; then
    fail "BREAK=1: two objects were tampered but the plan still proposes exactly one change - the single-object assertion is not load-bearing"
  fi
  log "  BREAK=1: caught - with a second object tampered the plan is $(grep -E '^Plan:' <<< "$DRIFT_PLAN" | head -1), not one change; the real check below is skipped"
  ( cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: could not reconverge A"
  gauntlet_stage drift_reconverge pass "BREAK=1 control: with two objects tampered the single-object assertion correctly fails to hold ($(grep -E '^Plan:' <<< "$DRIFT_PLAN" | head -1)); reconverged afterwards"
else
  grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$DRIFT_PLAN" || { printf '%s\n' "$DRIFT_PLAN" | tail -20; fail "the plan after one tamper does not propose exactly one change"; }
  grep -q "kubernetes_config_map.app" <<< "$DRIFT_PLAN" || fail "the plan does not name kubernetes_config_map.app"
  grep -q "shard" <<< "$DRIFT_PLAN" && fail "the plan touches a shard ConfigMap nobody tampered"
  RECONV="$(cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$RECONV" | tail -20; fail "the reconverging apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$RECONV" || fail "the reconverging apply did not change exactly one object"
  GREETING="$(kca get configmap app-config -n "$NS" -o jsonpath='{.data.greeting}')"
  [ "$GREETING" = "hello" ] || fail "app-config's greeting reads $GREETING after reconverging, want hello"
  gauntlet_stage drift_reconverge pass "one ConfigMap tampered with kubectl patch; choudoufu proposed exactly kubernetes_config_map.app (0 add, 1 change, 0 destroy), matching stock's own plan on the oracle cluster for the same tamper; apply changed 1 and the value reads back as configured. BREAK=1 tampers a second object and the single-object assertion correctly fails"
fi

# ── 6. plan_approval: plan -out, the world moves, apply refuses ──────────
gauntlet_begin_stage plan_approval
log "=== 6. plan_approval: a saved plan, an out-of-band label, a refusal; then the same file applies once the world is back ==="
write_config "$ADOPTED" live app 2 '    reviewed = "yes"'
write_config "$ORACLE" stock app 2 '    reviewed = "yes"'
P_PLAN="$(cd "$ADOPTED" && "$TOFU" plan -out=approved.tfplan -input=false -no-color 2>&1)" || { printf '%s\n' "$P_PLAN" | tail -20; fail "plan -out failed"; }
grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$P_PLAN" || { printf '%s\n' "$P_PLAN" | tail -10; fail "the saved plan is not exactly one change"; }
kca label configmap shard-0 -n "$NS" stray=yes >/dev/null || fail "could not move the world (label shard-0) on A"
P_APPLY="$(cd "$ADOPTED" && "$TOFU" apply -input=false -no-color approved.tfplan 2>&1)"; P_RC=$?
if [ "${BREAK_APPROVAL:-}" = "1" ]; then
  [ "$P_RC" -eq 0 ] && fail "BREAK_APPROVAL=1: applying the saved plan after the world moved succeeded - the refusal is not load-bearing"
  log "  BREAK_APPROVAL=1: caught - the apply after the world moved exited $P_RC, so 'expect success' correctly fails"
  kca label configmap shard-0 -n "$NS" stray- >/dev/null
  ( cd "$ADOPTED" && "$TOFU" apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: the saved plan did not apply once the world was put back"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: stock could not apply the reviewed change on B"
  gauntlet_stage plan_approval pass "BREAK_APPROVAL=1 control: applying the saved plan after the world moved exited $P_RC (refused), so the stage's own Break line correctly fails; applied once the world was put back"
else
  [ "$P_RC" -eq 3 ] || { printf '%s\n' "$P_APPLY" | tail -20; fail "apply of the saved plan after the world moved exited $P_RC, want 3 (the refusal)"; }
  grep -qF "The approved plan no longer matches the live system" <<< "$P_APPLY" || { printf '%s\n' "$P_APPLY" | tail -20; fail "the refusal does not carry its documented sentence"; }
  REVIEWED="$(kca get configmap app-config -n "$NS" -o jsonpath='{.data.reviewed}')"
  [ -z "$REVIEWED" ] || fail "app-config gained reviewed=$REVIEWED despite the refusal"
  kca label configmap shard-0 -n "$NS" stray- >/dev/null || fail "could not put the world back"
  P_APPLY2="$(cd "$ADOPTED" && "$TOFU" apply -input=false -no-color approved.tfplan 2>&1)" || { printf '%s\n' "$P_APPLY2" | tail -20; fail "the saved plan did not apply once the world was put back"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$P_APPLY2" || fail "the saved plan's apply did not change exactly one object"
  [ "$(kca get configmap app-config -n "$NS" -o jsonpath='{.data.reviewed}')" = "yes" ] || fail "app-config does not read reviewed=yes after the saved plan applied"
  ( stock_b plan -out=approved.tfplan -input=false -no-color >/dev/null 2>&1 && stock_b apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "stock's own planfile did not apply on B"
  gauntlet_stage plan_approval pass "plan -out wrote one update (app-config gains reviewed=yes); the world then moved out of band (a stray label on shard-0, kubectl, never choudoufu) and apply of the saved plan refused with \"The approved plan no longer matches the live system\" at exit 3, nothing applied (kubectl reads no reviewed key); with the label removed the identical file applied, 0 added, 1 changed, 0 destroyed, and reviewed=yes reads back; stock's own planfile applied on the oracle cluster in the unchanged case. BREAK_APPROVAL=1 expects success after the move and correctly fails"
fi

# ── 7. day2_rename: a moved block, zero churn ────────────────────────────
gauntlet_begin_stage day2_rename
log "=== 7. day2_rename: kubernetes_service_account.app becomes .team through a moved block ==="
moved_block() { cat <<'EOF'

moved {
  from = kubernetes_service_account.app
  to   = kubernetes_service_account.team
}
EOF
}
if [ "${BREAK:-}" = "1" ]; then
  # The AWS Break line (rename without a moved block, expect churn) cannot
  # fire here: with no address on the object the block name is not part
  # of its identity, and the plan after a bare block rename is empty too
  # (measured 2026-09-12: Plan: 0/0/0). The control that can fire is a
  # rename of the object's own metadata.name, which is a replace.
  write_config "$ADOPTED" live app 2 '    reviewed = "yes"'
  python3 - "$ADOPTED/main.tf" <<'PYIN' || fail "BREAK: could not rename the ServiceAccount's metadata.name"
import sys
p = sys.argv[1]; s = open(p).read()
old = 'resource "kubernetes_service_account" "app" {\n  metadata {\n    name      = "app"'
assert old in s
open(p, 'w').write(s.replace(old, old.replace('name      = "app"', 'name      = "app-renamed"'), 1))
PYIN
  R_PLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK: the plan after renaming the object failed"; }
  if grep -qE "^No changes|Plan: 0 to add, 0 to change, 0 to destroy" <<< "$R_PLAN"; then
    fail "BREAK=1: renaming the object's own name still planned zero churn - the zero-churn assertion is not load-bearing"
  fi
  grep -q "1 to add" <<< "$R_PLAN" && grep -q "1 to destroy" <<< "$R_PLAN" || { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK=1: renaming the object's own name did not plan a destroy and a create: $(grep -E '^Plan:' <<< "$R_PLAN" | head -1)"; }
  log "  BREAK=1: caught - renaming metadata.name plans $(grep -E '^Plan:' <<< "$R_PLAN" | head -1); the real moved-block check below is skipped"
  { write_config "$ADOPTED" live team 2 '    reviewed = "yes"'; moved_block >> "$ADOPTED/main.tf"; }
  ( cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: the moved-block apply failed"
  { write_config "$ORACLE" stock team 2 '    reviewed = "yes"'; moved_block >> "$ORACLE/main.tf"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: stock's moved-block apply failed on B"
  gauntlet_stage day2_rename pass "BREAK=1 control: renaming the object's own metadata.name plans a replace ($(grep -E '^Plan:' <<< "$R_PLAN" | head -1)), so the zero-churn assertion correctly fails to hold; a bare block rename without a moved block is zero churn on this substrate because the block name is not part of the object's identity; the moved block then applied"
else
  { write_config "$ADOPTED" live team 2 '    reviewed = "yes"'; moved_block >> "$ADOPTED/main.tf"; }
  { write_config "$ORACLE" stock team 2 '    reviewed = "yes"'; moved_block >> "$ORACLE/main.tf"; }
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's moved-block plan failed on B"; }
  grep -qE "^No changes|Plan: 0 to add, 0 to change, 0 to destroy" <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's moved-block plan on B is not zero churn"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's moved-block apply failed on B"
  R_PLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_PLAN" | tail -20; fail "the moved-block plan failed"; }
  grep -qE "^No changes|Plan: 0 to add, 0 to change, 0 to destroy" <<< "$R_PLAN" || { printf '%s\n' "$R_PLAN" | tail -20; fail "the moved-block plan is not zero churn"; }
  ( cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "the moved-block apply failed"
  exists_a serviceaccount app || fail "the ServiceAccount is gone after the rename"
  [ "$(count_a)" = "7" ] || fail "$(count_a) labelled objects after the rename, want 7"
  gauntlet_stage day2_rename pass "moved block: kubernetes_service_account.app -> .team with zero churn (no add, no change, no destroy), the live object untouched and still labelled, read with kubectl; stock's plan for the same moved block on the oracle cluster is also zero churn. The moved-block half only: live-mv has no Kubernetes leg, because the object carries no address to rewrite (#1066). A bare block rename without a moved block is zero churn here too, since the block name is not part of the object's identity; BREAK=1 renames the object's own metadata.name instead, which plans a replace, and the zero-churn assertion correctly fails"
fi

# ── 8. day2_remove: delete the ServiceAccount's block ────────────────────
gauntlet_begin_stage day2_remove
log "=== 8. day2_remove: the ServiceAccount block leaves the configuration ==="
# remove_block strips the kubernetes_service_account block (and the moved
# block) from a written config.
remove_sa() { python3 - "$1" <<'PY'
import re, sys
p = sys.argv[1]; s = open(p).read()
s = re.sub(r'resource "kubernetes_service_account" "team" \{.*?\n\}\n\n', '', s, flags=re.S)
s = re.sub(r'\nmoved \{.*?\n\}\n', '', s, flags=re.S)
assert 'kubernetes_service_account' not in s
open(p, 'w').write(s)
PY
}
if [ "${BREAK_REMOVE:-}" = "1" ]; then
  K_PLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)" || fail "BREAK_REMOVE: the plan with the block kept failed"
  # "0 to destroy" in a zero-churn summary line is not a destroy.
  grep -qE "will be destroyed|[1-9][0-9]* to destroy" <<< "$K_PLAN" && fail "BREAK_REMOVE=1: with the block kept a destroy was still proposed - the destroy below would not be the block removal's doing"
  log "  BREAK_REMOVE=1: caught - with the block kept the plan proposes no destroy ($(grep -E '^Plan:|^No changes' <<< "$K_PLAN" | head -1)); the real check is skipped"
  gauntlet_stage day2_remove pass "BREAK_REMOVE=1 control: with the ServiceAccount block kept, no destroy is proposed; the real check is skipped"
  remove_sa "$ADOPTED/main.tf" || fail "BREAK_REMOVE: could not remove the block afterwards"
  remove_sa "$ORACLE/main.tf" || fail "BREAK_REMOVE: could not remove the block from the oracle root afterwards"
  ( cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_REMOVE: the removal apply failed afterwards"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_REMOVE: stock's removal apply failed on B afterwards"
else
  remove_sa "$ADOPTED/main.tf" || fail "could not remove the ServiceAccount block from the adopted root"
  remove_sa "$ORACLE/main.tf" || fail "could not remove the ServiceAccount block from the oracle root"
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's remove plan failed on B"; }
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's remove plan on B is not exactly one destroy"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's remove apply failed on B"
  D_PLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$D_PLAN" | tail -20; fail "the remove plan failed"; }
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$D_PLAN" || { printf '%s\n' "$D_PLAN" | tail -20; fail "the remove plan is not exactly one destroy"; }
  # With no address on the object, the sweep plans the orphan at the
  # synthetic address <type>.orphan_<namespace>_<name> (live/MARKERS.md,
  # "Kubernetes: one label"), never at the block address that no longer
  # exists.
  # ... and, with no block left declaring the kind, files it under the
  # provider type that targets the API the cluster serves today, the
  # versioned name (internal/live/kubesweep's TypeFor).
  D_LINE="$(grep -E '^[[:space:]]*# .* will be destroyed' <<< "$D_PLAN" | head -1)"
  D_ADDR="$(sed -E 's/^[[:space:]#]*//; s/ will be destroyed.*$//' <<< "$D_LINE")"
  [ "$D_ADDR" = "kubernetes_service_account_v1.orphan_${NS}_app" ] || { printf '%s\n' "$D_PLAN" | grep -E 'destroyed|^Plan:' ; fail "the one destroy is ${D_ADDR:-unnamed} (line: ${D_LINE:-none}), not the orphan address kubernetes_service_account_v1.orphan_${NS}_app the sweep plans a label-found object at"; }
  grep -q "Owned and undeclared: 1 live resource will be destroyed" <<< "$D_PLAN" || fail "the plan does not say the destroy is an owned, undeclared object"
  D_APPLY="$(cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$D_APPLY" | tail -20; fail "the remove apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 0 changed, 1 destroyed" <<< "$D_APPLY" || fail "the remove apply did not destroy exactly one object"
  exists_a serviceaccount app && fail "the ServiceAccount still exists after the remove apply"
  D_REPLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)" || fail "the replan after the remove failed"
  grep -q "No changes." <<< "$D_REPLAN" || { printf '%s\n' "$D_REPLAN" | tail -20; fail "the replan after the remove is not empty"; }
  [ "$(count_a)" = "6" ] || fail "$(count_a) labelled objects after the remove, want 6"
  gauntlet_stage day2_remove pass "deleting kubernetes_service_account.team's block proposed exactly one destroy (0 add, 0 change, 1 destroy) at the sweep's synthetic orphan address $D_ADDR (\"Owned and undeclared: 1 live resource will be destroyed\") - the object is found by its label, which carries no address, and filed under the versioned type since no block declares the kind any more - applied cleanly, the object gone from the cluster (kubectl get serviceaccount: NotFound) and the next plan empty; stock's plan for the same removal on the oracle cluster is also exactly one destroy; the Deployment's ReplicaSet and Pod, which carry no estate label, were never proposed. BREAK_REMOVE=1 keeps the block and no destroy is proposed"
fi

# ── 9. day2_count: shards 2 -> 1 -> 2 ────────────────────────────────────
gauntlet_begin_stage day2_count
log "=== 9. day2_count: kubernetes_config_map.shard scales 2 -> 1 -> 2 ==="
scale_to() { # $1 count, for both roots (the ServiceAccount block is gone)
  write_config "$ADOPTED" live team "$1" '    reviewed = "yes"'; remove_sa "$ADOPTED/main.tf" || fail "could not rewrite the adopted root at count $1"
  write_config "$ORACLE" stock team "$1" '    reviewed = "yes"'; remove_sa "$ORACLE/main.tf" || fail "could not rewrite the oracle root at count $1"
}
scale_to 1
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's scale-down plan failed on B"
grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's scale-down plan on B is not exactly one destroy"; }
grep -q 'kubernetes_config_map.shard\[1\]' <<< "$O_PLAN" || fail "stock's scale-down on B does not destroy shard[1]"
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's scale-down apply failed on B"
C_PLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$C_PLAN" | tail -20; fail "the scale-down plan failed"; }
grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$C_PLAN" || { printf '%s\n' "$C_PLAN" | tail -20; fail "the scale-down plan is not exactly one destroy"; }
# The instance leaving the count is found by its label, which carries no
# index, so the sweep plans it at its orphan address; kubectl below confirms
# it is shard-1, the object stock destroys.
C_LINE="$(grep -E '^[[:space:]]*# .* will be destroyed' <<< "$C_PLAN" | head -1)"
C_ADDR="$(sed -E 's/^[[:space:]#]*//; s/ will be destroyed.*$//' <<< "$C_LINE")"
grep -qE "^kubernetes_config_map(_v1)?\.orphan_${NS}_shard-1$" <<< "$C_ADDR" || { printf '%s\n' "$C_PLAN" | grep -E 'destroyed|^Plan:'; fail "the scale-down destroys ${C_ADDR:-nothing named}, not shard-1 at its orphan address"; }
( cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color 2>&1 | grep -qF "0 added, 0 changed, 1 destroyed" ) || fail "the scale-down apply did not destroy exactly one object"
if [ "${BREAK_COUNT:-}" = "1" ]; then
  if ! exists_a configmap shard-0; then
    fail "BREAK_COUNT=1: shard-0 was destroyed - the 'wrong instance' assertion would hold, so the check is not load-bearing"
  fi
  log "  BREAK_COUNT=1: caught - shard-0 still exists, so asserting it was the one destroyed correctly fails"
  gauntlet_stage day2_count pass "BREAK_COUNT=1 control: asserting the lower index (shard-0) was destroyed correctly fails to hold; the real check is skipped"
else
  exists_a configmap shard-0 || fail "shard-0 was destroyed on the scale-down"
  exists_a configmap shard-1 && fail "shard-1 still exists after the scale-down"
  scale_to 2
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's scale-up plan failed on B"
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's scale-up plan on B is not exactly one add"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's scale-up apply failed on B"
  U_PLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$U_PLAN" | tail -20; fail "the scale-up plan failed"; }
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$U_PLAN" || { printf '%s\n' "$U_PLAN" | tail -20; fail "the scale-up plan is not exactly one add"; }
  grep -q 'kubernetes_config_map.shard\[1\]' <<< "$U_PLAN" || fail "the scale-up does not create shard[1]"
  ( cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color 2>&1 | grep -qF "1 added, 0 changed, 0 destroyed" ) || fail "the scale-up apply did not create exactly one object"
  exists_a configmap shard-0 && exists_a configmap shard-1 || fail "both shards do not exist after the scale-up"
  U_REPLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)" || fail "the replan after the scale-up failed"
  grep -q "No changes." <<< "$U_REPLAN" || { printf '%s\n' "$U_REPLAN" | tail -20; fail "the replan after the scale-up is not empty"; }
  [ "$(count_a)" = "6" ] || fail "$(count_a) labelled objects after the count cycle, want 6"
  gauntlet_stage day2_count pass "scaling kubernetes_config_map.shard from 2 to 1 destroyed exactly shard-1, planned at the sweep's orphan address $C_ADDR since the label carries no index (shard-0 untouched, both read with kubectl); back to 2 created exactly kubernetes_config_map.shard[1] under the same name; the next plan is empty; stock's plans for the same two changes on the oracle cluster have the identical shape. BREAK_COUNT=1 asserts the lower index was destroyed and correctly fails"
fi

# ── 10. day2_teardown: destroy the adopted estate ────────────────────────
gauntlet_begin_stage day2_teardown
log "=== 10. day2_teardown: apply -destroy on the adopted estate, and stock's own destroy on B ==="
T_EXPECT="$(count_a)"
T_OUT="$(cd "$ADOPTED" && "$TOFU" apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$T_OUT" | tail -20; fail "apply -destroy failed"; }
grep -qF "Resources: 0 added, 0 changed, $T_EXPECT destroyed" <<< "$T_OUT" || { printf '%s\n' "$T_OUT" | tail -5; fail "apply -destroy did not remove exactly the $T_EXPECT remaining objects"; }
for _ in $(seq 1 30); do kca get namespace "$NS" >/dev/null 2>&1 || break; sleep 2; done
kca get namespace "$NS" >/dev/null 2>&1 && fail "the $NS namespace still exists after the destroy"
[ "$(count_a)" = "0" ] || fail "$(count_a) object(s) still carry tofu-estate=$ESTATE after the destroy"
O_EXPECT="$(stock_b state list 2>/dev/null | wc -l | tr -d ' ')"
O_T="$(stock_b apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$O_T" | tail -10; fail "stock's destroy failed on B"; }
grep -qF "Resources: 0 added, 0 changed, $O_EXPECT destroyed" <<< "$O_T" || fail "stock's destroy on B did not remove exactly the $O_EXPECT objects its state held"
gauntlet_stage day2_teardown pass "apply -destroy removed exactly the $T_EXPECT remaining objects in one apply, the namespace is gone and no object of any of the estate's five kinds carries tofu-estate=$ESTATE (kubectl, every namespace); stock's destroy of the same estate on the oracle cluster also removed exactly the $O_EXPECT its state held"

# ── 11. greenfield: the same shape, fresh, with a live block ─────────────
gauntlet_begin_stage greenfield
log "=== 11. greenfield: choudoufu applies the shape fresh on the now-empty cluster A ==="
mkdir -p "$GREEN"
write_config "$GREEN" live
( cd "$GREEN" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "greenfield init failed"
G_OUT="$(cd "$GREEN" && "$TOFU" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$G_OUT" | tail -20; fail "greenfield apply failed"; }
grep -qF "Apply complete! Resources: 7 added, 0 changed, 0 destroyed" <<< "$G_OUT" || fail "greenfield apply did not add exactly 7 objects"
[ ! -f "$GREEN/terraform.tfstate" ] || fail "a terraform.tfstate appeared after a live-block apply"
[ "$(count_a)" = "7" ] || fail "$(count_a) object(s) carry tofu-estate=$ESTATE after the greenfield apply, want 7"
G_RECORDS="$(gauntlet_record_count "$GREEN/.tofu-records")"
G_PLAN="$(cd "$GREEN" && "$TOFU" plan -input=false -no-color 2>&1)" || fail "the greenfield replan failed"
grep -q "No changes." <<< "$G_PLAN" || { printf '%s\n' "$G_PLAN" | tail -20; fail "the greenfield replan is not empty"; }
rm -f "$GREEN/.terraform/choudoufu-cache.tfstate"
G_PLAN2="$(cd "$GREEN" && "$TOFU" plan -input=false -no-color 2>&1)" || fail "the greenfield replan without the cache failed"
grep -q "No changes." <<< "$G_PLAN2" || { printf '%s\n' "$G_PLAN2" | tail -20; fail "the greenfield replan without the cache is not empty"; }
DROP=""; [ "${BREAK:-}" = "1" ] && DROP="deployment/web"
inventory "$KCA" "$DROP" > "$WORK/inventory.green.json" || fail "could not read the greenfield inventory"
if [ "${BREAK:-}" = "1" ]; then
  if diff -q "$WORK/inventory.stock.json" "$WORK/inventory.green.json" >/dev/null; then
    fail "BREAK=1: with the Deployment dropped from the greenfield inventory the two inventories still match - the comparison is not load-bearing"
  fi
  log "  BREAK=1: caught - the inventories differ once the Deployment is dropped; the real comparison is skipped"
  gauntlet_stage greenfield pass "BREAK=1 control: dropping the Deployment from the greenfield inventory makes the object-by-object comparison correctly fail; the estate applied (7 added, no terraform.tfstate) and replanned empty with and without the cache"
else
  if ! diff -u "$WORK/inventory.stock.json" "$WORK/inventory.green.json"; then
    fail "the greenfield inventory differs from stock's cold-deploy inventory (diff above)"
  fi
  gauntlet_stage greenfield pass "7 objects applied fresh with a live block and no terraform.tfstate, every one labelled tofu-estate=$ESTATE (kubectl, five kinds); the record store held $G_RECORDS file(s); replanned empty with and without the cache; the cluster's inventory (ConfigMap data, the Service's ports and selector, the Deployment's replicas and container, the ServiceAccount and namespace) matches stock's cold deploy on the same cluster object by object, labels never compared. BREAK=1 drops the Deployment from the expected inventory and the match correctly fails"
fi
( cd "$GREEN" && "$TOFU" apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "greenfield teardown failed"

# ── 12. strict: every toggle on, one refusal ─────────────────────────────
gauntlet_begin_stage strict
STRICT="$WORK/strict"
mkdir -p "$STRICT"
strict_block() { # $1 = the secrets setting under test ("refuse" or "store")
  cat <<EOF
terraform {
  required_providers {
    random = {
      source  = "hashicorp/random"
      version = ">= 3.0"
    }
  }
  live {
    estate = "reference-k8s-strict"
    record_store "local" {
      path = ".tofu-records"
    }
    strict {
      secrets          = "$1"
      no_source_create = "refuse"
      marker_repair    = "never"
      markers "record" {
        types = ["kubernetes_config_map"]
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
( cd "$STRICT" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "choudoufu init for the strict-stage scratch estate failed"
STRICT_ON="$(cd "$STRICT" && "$TOFU" plan -input=false -no-color 2>&1)"; STRICT_ON_RC=$?
if [ "${BREAK_STRICT:-}" = "1" ]; then
  strict_block "store" > "$STRICT/main.tf"
  STRICT_OFF="$(cd "$STRICT" && "$TOFU" plan -input=false -no-color 2>&1)"; STRICT_OFF_RC=$?
  [ "$STRICT_OFF_RC" -eq 0 ] || { printf '%s\n' "$STRICT_OFF" | tail -20; fail "BREAK_STRICT=1: the plan with secrets = \"store\" exited $STRICT_OFF_RC - a refusal appeared where none should"; }
  grep -q "^Error:" <<< "$STRICT_OFF" && fail "BREAK_STRICT=1: turning secrets off did not clear every refusal"
  grep -qF 'random_password.db will be created' <<< "$STRICT_OFF" || fail "BREAK_STRICT=1: the plan with secrets = \"store\" does not propose creating random_password.db"
  gauntlet_stage strict pass "BREAK_STRICT=1 control: with secrets back to \"store\" the refusal is gone and the plan is an ordinary create; the real check is skipped"
else
  [ "$STRICT_ON_RC" -eq 1 ] || { printf '%s\n' "$STRICT_ON" | tail -20; fail "the every-toggle-on plan exited $STRICT_ON_RC, not the refusal's usual 1"; }
  [ "$(grep -c '^Error:' <<< "$STRICT_ON")" -eq 1 ] || { printf '%s\n' "$STRICT_ON"; fail "every strict toggle on refused more than one thing"; }
  grep -qF 'Error: Logical resource is not admitted' <<< "$STRICT_ON" || { printf '%s\n' "$STRICT_ON"; fail "the one refusal is not \"Logical resource is not admitted\""; }
  grep -qF 'strict { secrets = "refuse" }' <<< "$STRICT_ON" || { printf '%s\n' "$STRICT_ON"; fail "the refusal does not cite strict { secrets = \"refuse\" }"; }
  gauntlet_stage strict pass "every strict toggle on (secrets = refuse, no_source_create = refuse, marker_repair = never with a markers \"record\" selection naming kubernetes_config_map) against a scratch estate carrying random_password.db: exactly one refusal, Logical resource is not admitted under strict { secrets = \"refuse\" }; the other two toggles are on and silent. BREAK_STRICT=1 turns secrets back to \"store\" and the refusal disappears"
fi
gauntlet_end_stage

gauntlet_end
log "reference-k8s: done"
