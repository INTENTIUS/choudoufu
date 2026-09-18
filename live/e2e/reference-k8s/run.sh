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
# the six kinds KINDS names; the out-of-band mutation is a kubectl patch or
# label; the rename is the moved-block half only, since live-mv has no
# Kubernetes leg (#1066). day2_replace does not apply (a name is unique
# within its namespace, so nothing can be created before the object it
# replaces is gone) and is recorded n/a by the runner, not by this script.
# day2_crash's own create-before-destroy window does not exist here for the
# same reason, so what this script interrupts instead is an apply that
# creates several objects (#1110, part 4): the kill lands between one
# object's create committing and the next object's, and the next plan has
# to propose exactly the remainder. The other window #1110 names for this
# substrate, "between the label patch and the object write in a move", is
# not one: a cross-estate live-mv makes exactly one governed write, the
# label patch, and returns before propagateModuleRename because the record
# it would move lives in the estate being left (internal/live/mv/mv.go),
# and a same-estate rename writes nothing on the cluster at all.
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
#   BREAK_CRASH    set to 1 to assert, after the same real interrupt, that
#                  nothing is proposed (day2_crash's own Break line); must
#                  fail, because a recovered run proposes the remainder.
#   BREAK_CRASH_UNBOUND
#                  set to 1 to strip the tofu-estate label off the object
#                  the interrupted apply did create before replanning - the
#                  unrecovered run this stage exists to catch. The real
#                  check ("exactly the remainder, the created object bound")
#                  must then fail to hold.
#   BREAK_STRICT   set to 1 to turn secrets back to "store" and require the
#                  refusal to vanish (strict's Break line).
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
source "$ROOT/live/e2e/lib/gauntlet.sh"
ESTATE="reference-k8s"
NS="refk8s"
# secrets is in the list for day2_crash's crash pair, whose first
# object is a kubernetes_secret (#1235): a type that records residue,
# where the ConfigMap the pair used to start with records nothing. The
# estate's own shape declares no Secret, so every count below is unchanged
# by its presence.
KINDS="namespaces configmaps secrets serviceaccounts services deployments"
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

# day2_crash needs a build with e2eTestingFeatures set, the same ldflags
# gate TOFU_E2E_APPLY_RESOURCE_PANIC sits behind, so that the engine's own
# TOFU_E2E_APPLY_RESOURCE_INTERRUPT hook (internal/command/
# apply_e2etesting_crash.go) is reachable at all. Built from THIS tree's
# source whatever $TOFU came from, since the point is to interrupt this
# tree's engine, and unconditionally, since the real check and both Break
# controls need it. Identical to $TOFU everywhere else: the hook is a no-op
# unless the variable names an address the run actually applies.
mkdir -p "$WORK/bin"
TOFU_CRASH="$WORK/bin/choudoufu-e2e"
( cd "$ROOT" && env -u PWD go build -ldflags="-X 'main.e2eTestingFeatures=yes'" -o "$TOFU_CRASH" ./cmd/choudoufu ) \
  || fail "go build -ldflags e2eTestingFeatures ./cmd/choudoufu failed"
log "  built $TOFU_CRASH (e2eTestingFeatures=yes, for day2_crash's interrupt)"

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

# day2_crash's two extra objects, appended to a root that already holds the
# shape above. crash_second reads crash_first's own name, so the two are a
# real edge in the graph rather than two independent nodes the walker may
# reach in either order: nothing can create crash-second until crash-first
# has committed. That is what makes the interrupt below deterministic by
# construction (the discipline #490 imposed on the AWS crash stage after an
# external tail/grep/kill race produced a retry lottery), so this script
# interrupts once and reports what it saw instead of retrying.
#
# crash_first is a kubernetes_secret and not a ConfigMap, which is #1235:
# the stage is measuring what the record store contributes to recovering a
# half-applied graph, and a kubernetes_config_map(_v1) is the one type in
# this lane's surface with neither a ratified identity row nor a
# config-only argument, so it records nothing at all and the stage's own
# evidence line could only ever report a count that did not move (#1188
# measured 12 -> 12 and 25 -> 25 for exactly that reason). A Secret
# records residue.wait_for_service_account_token - the applied value of a
# config-only argument the API server never returns, the irrecoverable
# member - so the record the interrupted apply writes has something in it
# to read, and section 10 below reads it by taking it away again.
crash_first_block() {
  cat <<EOF
resource "kubernetes_secret" "crash_first" {
  metadata {
    name      = "crash-first"
    namespace = "$NS"
  }
  data = {
    step = "one"
  }
  depends_on = [kubernetes_namespace.app]
}
EOF
}
crash_second_block() {
  cat <<EOF
resource "kubernetes_config_map" "crash_second" {
  metadata {
    name      = "crash-second"
    namespace = "$NS"
  }
  data = {
    after = kubernetes_secret.crash_first.metadata[0].name
  }
}
EOF
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
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed); objects carrying tofu-estate=$ESTATE unchanged at $BEFORE_N across the six kinds the count covers, counted with kubectl"

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

# ── 10. day2_crash: an apply of several objects, killed after the first ──
#
# day2_crash on the kind substrate (#1110, part 4). The stage's own window
# on AWS - after a create_before_destroy create, before the paired destroy -
# cannot exist here, because a name is unique in its namespace and nothing
# is created before the object it replaces is gone (day2_replace's n/a, and
# this file's header). The Kubernetes window with the same question in it is
# an apply that creates several objects: kill it after one object exists and
# before the next does, and ask the next plan to propose exactly the
# remainder, with the object already created bound rather than created a
# second time (which the API server would refuse with AlreadyExists) or
# swept away as an orphan.
#
# The kill is the engine's own: internal/command/apply_e2etesting_crash.go
# self-delivers SIGTERM - the same signal an operator's Ctrl-C forwards -
# synchronously inside the graph walker the instant
# TOFU_E2E_APPLY_RESOURCE_INTERRUPT's named address finishes a real create.
# Under -parallelism=1 nothing else can be dispatched until that hook
# returns, and crash_second's own data reads crash_first's name, so the
# second object cannot have been reached: the window is closed by the graph,
# not by timing.
#
# The oracle is stock on cluster B, walked into the same position: it
# applies crash-first alone, and its plan for the configuration that then
# adds crash-second is what "exactly the remainder" means for this estate.
gauntlet_begin_stage day2_crash
log "=== 10. day2_crash: SIGTERM between the create of one object and the create of the next ==="

# The oracle first, so the comparison exists before choudoufu is asked
# anything: stock at crash-first only, then stock's plan for crash-second.
crash_first_block >> "$ORACLE/main.tf" || fail "could not append crash-first to the oracle root"
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's crash-first plan failed on B"; }
grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's crash-first plan on B is not exactly one add"; }
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's crash-first apply failed on B"
{ echo; crash_second_block; } >> "$ORACLE/main.tf" || fail "could not append crash-second to the oracle root"
O_REMAINDER="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_REMAINDER" | tail -10; fail "stock's remainder plan failed on B"; }
grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_REMAINDER" \
  || { printf '%s\n' "$O_REMAINDER" | tail -10; fail "stock's remainder plan on B is not exactly one add - the oracle for this stage is not what it should be"; }
grep -q 'kubernetes_config_map.crash_second' <<< "$O_REMAINDER" || fail "stock's remainder plan on B does not name crash_second"
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's remainder apply failed on B"
log "  oracle: stock at crash-first alone plans exactly one add (crash_second) for the remainder, and applies it"

{ echo; crash_first_block; echo; crash_second_block; } >> "$ADOPTED/main.tf" || fail "could not append the crash pair to the adopted root"
X_PLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$X_PLAN" | tail -20; fail "the pre-crash plan failed"; }
grep -qF "Plan: 2 to add, 0 to change, 0 to destroy." <<< "$X_PLAN" \
  || { printf '%s\n' "$X_PLAN" | tail -20; fail "the pre-crash plan is not exactly two adds - there is no two-object apply to interrupt"; }
X_RECORDS_BEFORE="$(gauntlet_record_count "$ADOPTED/.tofu-records")"

# The interrupt is delivered by the engine itself, so this runs in the
# plain foreground: no background process, no output tailing, no poll loop.
# A non-zero exit is the NORMAL outcome here - the process was killed.
X_OUT="$(cd "$ADOPTED" && TOFU_E2E_APPLY_RESOURCE_INTERRUPT="kubernetes_secret.crash_first" "$TOFU_CRASH" apply -input=false -auto-approve -no-color -parallelism=1 2>&1)"; X_RC=$?
printf '%s\n' "$X_OUT" > "$WORK/day2_crash.log"
log "  interrupted apply exited $X_RC (a genuine crash is not expected to exit 0)"
[ "$X_RC" -ne 0 ] || { printf '%s\n' "$X_OUT" | tail -20; fail "the interrupted apply exited 0 - the engine's self-signal never landed, so nothing was interrupted and this stage would measure a clean apply"; }
exists_a secret crash-first || { printf '%s\n' "$X_OUT" | tail -20; fail "crash-first does not exist after the interrupted apply - the kill landed before the create committed, so there is no crash window to recover from"; }
exists_a configmap crash-second && { printf '%s\n' "$X_OUT" | tail -20; fail "crash-second exists after the interrupted apply - the kill landed after both creates, so there is no remainder to propose"; }
# The selector alone, never a selector next to a resource name: kubectl
# refuses that combination outright ("name cannot be provided when a
# selector is specified"), which would read as an unlabelled object.
kca get secret -n "$NS" -l "tofu-estate=$ESTATE" -o name 2>/dev/null | grep -qx "secret/crash-first" \
  || fail "crash-first was created by the interrupted apply but does not come back under tofu-estate=$ESTATE - the marker the rerun is supposed to find is not there (labels: $(kca get secret crash-first -n "$NS" --show-labels --no-headers 2>&1 | tr -s ' ' | cut -d' ' -f4))"
X_RECORDS_AFTER="$(gauntlet_record_count "$ADOPTED/.tofu-records")"
log "  crash-first exists and is labelled; crash-second does not exist; record files $X_RECORDS_BEFORE -> $X_RECORDS_AFTER"

# What the interrupted apply actually wrote, read off the store by the
# envelope's own address rather than counted (#1235). A count that does
# not move is not a reading: it is what this stage reported for two years
# on the two _v1 estates, because their crash pair was the one type that
# records nothing.
X_REC="$(gauntlet_record_file "$ADOPTED/.tofu-records" "kubernetes_secret.crash_first")"
[ -n "$X_REC" ] || fail "the interrupted apply created crash-first but wrote no record for kubernetes_secret.crash_first (record files $X_RECORDS_BEFORE -> $X_RECORDS_AFTER); there is nothing for the recovery to read back and this stage cannot measure what the record contributes"
[ "$X_RECORDS_AFTER" = "$((X_RECORDS_BEFORE + 1))" ] || fail "record files went $X_RECORDS_BEFORE -> $X_RECORDS_AFTER across the interrupted apply, want exactly one more - the apply is supposed to have written the record for the one object it did create, and nothing else"
X_RESIDUE="$(gauntlet_record_residue "$X_REC" | tr '\n' ' ' | sed 's/ $//')"
[ "$X_RESIDUE" = "wait_for_service_account_token" ] || fail "the record the interrupted apply wrote for kubernetes_secret.crash_first carries residue [${X_RESIDUE:-none}], want wait_for_service_account_token - the crash pair's first object has to be a type that records something irrecoverable, or this stage measures the record's contribution with an object that has none (#1188, #1235)"
log "  the interrupted apply wrote one record for kubernetes_secret.crash_first, carrying residue $X_RESIDUE"

if [ "${BREAK_CRASH_UNBOUND:-}" = "1" ]; then
  # The unrecovered run this stage exists to catch: the object is there,
  # but nothing marks it as the estate's, which is what a crash between
  # the create and the marker write would leave. The real check below must
  # fail to hold against it.
  kca label configmap crash-first -n "$NS" tofu-estate- >/dev/null 2>&1 || fail "BREAK_CRASH_UNBOUND: could not strip the label off crash-first"
  log "  BREAK_CRASH_UNBOUND=1: stripped tofu-estate off crash-first with kubectl"
fi

R_PLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; R_RC=$?
R_LINE="$(grep -E '^Plan:|^No changes' <<< "$R_PLAN" | head -1 | sed 's/\.$//')"
# What the real check asserts, as one predicate, so the two Break controls
# can require the SAME predicate to fail rather than approximating it.
recovered() {
  [ "$R_RC" -eq 0 ] || return 1
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$R_PLAN" || return 1
  grep -qE '^[[:space:]]*# kubernetes_config_map(_v1)?\.crash_second will be created' <<< "$R_PLAN" || return 1
  # Nothing may be proposed for the object the crash did create - not a
  # second create, not a sweep of it as an orphan.
  grep -E '^[[:space:]]*# .* will be' <<< "$R_PLAN" | grep -q 'crash_first\|crash-first' && return 1
  return 0
}

if [ "${BREAK_CRASH:-}" = "1" ]; then
  log "=== 10b (BREAK_CRASH=1). assert nothing is proposed after the interrupt - this must fail ==="
  [ "$R_RC" -eq 0 ] || { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK_CRASH=1: the plan after the interrupt exited $R_RC"; }
  grep -qF "No changes." <<< "$R_PLAN" \
    && { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK_CRASH=1: the plan after a real interrupted two-object apply came back empty, so this stage's own check is not load-bearing"; }
  log "  BREAK_CRASH=1: caught - the plan proposes work ($R_LINE), so 'nothing is proposed' correctly fails to hold"
  gauntlet_stage day2_crash pass "BREAK_CRASH=1 control: after the same real interrupt the plan proposes work ($R_LINE), so the stage's own Break line - interrupt and then assert nothing is proposed - correctly fails to hold; the real check is skipped"
  ( cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_CRASH: the recovery apply failed afterwards"
elif [ "${BREAK_CRASH_UNBOUND:-}" = "1" ]; then
  log "=== 10b (BREAK_CRASH_UNBOUND=1). the same check against an unbound object - this must fail ==="
  if recovered; then
    printf '%s\n' "$R_PLAN" | grep -E '^Plan:|will be'
    fail "BREAK_CRASH_UNBOUND=1: the recovery check still holds with crash-first carrying no tofu-estate label - it is not measuring whether the crashed-out object was bound at all"
  fi
  log "  BREAK_CRASH_UNBOUND=1: caught - with the label stripped the recovery check fails ($R_LINE)"
  gauntlet_stage day2_crash pass "BREAK_CRASH_UNBOUND=1 control: with the tofu-estate label stripped off the object the interrupted apply created - the unrecovered run this stage exists to catch - the recovery check correctly fails to hold ($R_LINE); the real check is skipped"
  kca delete secret crash-first -n "$NS" >/dev/null 2>&1 || fail "BREAK_CRASH_UNBOUND: could not delete the unlabelled crash-first afterwards"
  ( cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_CRASH_UNBOUND: the apply after the cleanup failed"
else
  if ! recovered; then
    printf '%s\n' "$R_PLAN" | grep -E '^Plan:|^No changes|will be' | head -20
    gauntlet_stage day2_crash fail "the plan after a real interrupt between the create of kubernetes_config_map.crash_first and the create of kubernetes_config_map.crash_second is not exactly the remainder: ${R_LINE:-no plan line} (exit $R_RC). crash-first exists on the cluster carrying tofu-estate=$ESTATE and crash-second does not, both read with kubectl; stock, walked into the same position on the oracle cluster, plans exactly one add (crash_second). The interrupted apply wrote one record for kubernetes_secret.crash_first carrying residue ${X_RESIDUE:-none} (record files $X_RECORDS_BEFORE -> $X_RECORDS_AFTER)"
  else
    # ── the record's contribution, measured rather than counted (#1235) ──
    #
    # Take the one record the interrupted apply wrote out of the store,
    # replan from the identical position, and the difference between the
    # two plans IS what the record carried. Put it back and the difference
    # has to go away. Neither plan can pass vacuously: what is asserted is
    # that they differ, and in which direction. This is the reading the
    # stage's "Record files went N -> N" line never had.
    mv "$X_REC" "$WORK/crash_first.record" || fail "could not move the crash record aside"
    N_PLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; N_RC=$?
    N_LINE="$(grep -E '^Plan:|^No changes' <<< "$N_PLAN" | head -1 | sed 's/\.$//')"
    [ "$N_RC" -eq 0 ] || { printf '%s\n' "$N_PLAN" | tail -20; fail "the replan with the crash record taken out of the store exited $N_RC"; }
    grep -qF "Plan: 1 to add, 1 to change, 0 to destroy." <<< "$N_PLAN" \
      || { printf '%s\n' "$N_PLAN" | grep -E '^Plan:|will be|^ +[+~-] ' | head -20
           fail "with the one record the interrupted apply wrote taken out of the store, the recovery plan is $N_LINE, not the remainder plus one in-place update - so the record contributed nothing this stage can read, and its file count is not evidence of anything"; }
    grep -qE '^[[:space:]]+\+ wait_for_service_account_token +=' <<< "$N_PLAN" \
      || { printf '%s\n' "$N_PLAN" | grep -E 'will be|^ +[+~-] ' | head -20
           fail "the extra in-place update the missing record produces does not propose wait_for_service_account_token back - that config-only argument's applied value is the residue the record is supposed to be carrying"; }
    mv "$WORK/crash_first.record" "$X_REC" || fail "could not put the crash record back"
    B_PLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"
    grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$B_PLAN" \
      || { printf '%s\n' "$B_PLAN" | grep -E '^Plan:|will be' | head -10
           fail "putting the record back does not restore the exact-remainder plan, so the difference measured above is not the record's"; }
    log "  the record's contribution: with it, $R_LINE; without it, $N_LINE (+ wait_for_service_account_token on crash_first); with it again, the remainder"

    R_APPLY="$(cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color 2>&1)"; R_APPLY_RC=$?
    [ "$R_APPLY_RC" -eq 0 ] || { printf '%s\n' "$R_APPLY" | tail -20; fail "the recovery apply exited $R_APPLY_RC"; }
    grep -qF "Apply complete! Resources: 1 added, 0 changed, 0 destroyed" <<< "$R_APPLY" \
      || { printf '%s\n' "$R_APPLY" | tail -5; fail "the recovery apply did not add exactly the one remaining object"; }
    exists_a configmap crash-second || fail "crash-second does not exist after the recovery apply"
    exists_a secret crash-first || fail "crash-first is gone after the recovery apply - the recovery replaced the object the crash created instead of binding it"
    R_REPLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)" || fail "the replan after the recovery failed"
    grep -q "No changes." <<< "$R_REPLAN" || { printf '%s\n' "$R_REPLAN" | tail -20; fail "the replan after the recovery is not empty"; }
    [ "$(count_a)" = "8" ] || fail "$(count_a) labelled objects after the recovery, want 8"
    gauntlet_stage day2_crash pass "an apply creating two objects was interrupted by a real SIGTERM (exit $X_RC), delivered by the engine itself inside the -parallelism=1 graph walker the instant kubernetes_config_map.crash_first's create committed (internal/command/apply_e2etesting_crash.go); crash_second reads crash_first's name, so the walker cannot have reached it - kubectl confirms crash-first exists carrying tofu-estate=$ESTATE and crash-second does not. The create-before-destroy window this stage interrupts on the emulator does not exist here (see day2_replace), and neither does a move's: a cross-estate live-mv makes one governed write, the label patch itself, and re-keys no record. The next plan proposed exactly the remainder ($R_LINE, kubernetes_config_map.crash_second created) and proposed nothing at all for crash-first, which it bound by its label and its namespace and name - not a second create the API server would refuse, not an orphan sweep - matching stock's own plan from the same position on the oracle cluster; the recovery apply added exactly one object, both objects read back with kubectl, the plan after it is empty and 8 objects carry the estate's label. The record store's contribution is read, not counted: the interrupted apply wrote exactly one record (files $X_RECORDS_BEFORE -> $X_RECORDS_AFTER) for kubernetes_secret.crash_first carrying residue $X_RESIDUE, and taking that one file out of the store and replanning from the identical position turns the recovery plan from $R_LINE into $N_LINE, proposing wait_for_service_account_token back on the object the crash left behind; putting it back restores the exact-remainder plan. The crash pair's first object is a Secret and not a ConfigMap for that reason (#1235): a kubernetes_config_map(_v1) has neither a ratified identity row nor a config-only argument, records nothing, and made this line a count that could not move (#1188). BREAK_CRASH=1 asserts nothing is proposed and correctly fails; BREAK_CRASH_UNBOUND=1 strips the label off crash-first and the same recovery check correctly fails"
  fi
fi
gauntlet_end_stage

# ── 11. day2_teardown: destroy the adopted estate ────────────────────────
gauntlet_begin_stage day2_teardown
log "=== 11. day2_teardown: apply -destroy on the adopted estate, and stock's own destroy on B ==="
T_EXPECT="$(count_a)"
T_OUT="$(cd "$ADOPTED" && "$TOFU" apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$T_OUT" | tail -20; fail "apply -destroy failed"; }
grep -qF "Resources: 0 added, 0 changed, $T_EXPECT destroyed" <<< "$T_OUT" || { printf '%s\n' "$T_OUT" | tail -5; fail "apply -destroy did not remove exactly the $T_EXPECT remaining objects"; }
for _ in $(seq 1 30); do kca get namespace "$NS" >/dev/null 2>&1 || break; sleep 2; done
kca get namespace "$NS" >/dev/null 2>&1 && fail "the $NS namespace still exists after the destroy"
[ "$(count_a)" = "0" ] || fail "$(count_a) object(s) still carry tofu-estate=$ESTATE after the destroy"
O_EXPECT="$(stock_b state list 2>/dev/null | wc -l | tr -d ' ')"
O_T="$(stock_b apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$O_T" | tail -10; fail "stock's destroy failed on B"; }
grep -qF "Resources: 0 added, 0 changed, $O_EXPECT destroyed" <<< "$O_T" || fail "stock's destroy on B did not remove exactly the $O_EXPECT objects its state held"
gauntlet_stage day2_teardown pass "apply -destroy removed exactly the $T_EXPECT remaining objects in one apply, the namespace is gone and no object of any of the six kinds the count covers carries tofu-estate=$ESTATE (kubectl, every namespace); stock's destroy of the same estate on the oracle cluster also removed exactly the $O_EXPECT its state held"

# ── 12. greenfield: the same shape, fresh, with a live block ─────────────
gauntlet_begin_stage greenfield
log "=== 12. greenfield: choudoufu applies the shape fresh on the now-empty cluster A ==="
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

# ── the lost-store control (#1235) ───────────────────────────────────────
#
# Six AWS estates delete the local record store here and require the next
# plan to come back empty (reference-ec2-vpc/run.sh's A4); no Kubernetes
# estate took the reading at all. It is not the same answer on this
# substrate, and that is the point of taking it: identity re-derives from
# the tofu-estate label plus the configuration's own namespace and name,
# so nothing is created and nothing is swept - but the residue is gone,
# and the plan proposes it back as in-place updates. A lost record store
# costs one converging apply here, not an object (#1188). The "nothing is
# created" half is the same predicate BREAK_CRASH_UNBOUND proves red in
# section 10 of this script: strip the label and the plan proposes a
# create.
rm -rf "$GREEN/.tofu-records" "$GREEN/.terraform/choudoufu-cache.tfstate"
L_PLAN="$(cd "$GREEN" && "$TOFU" plan -input=false -no-color 2>&1)"; L_RC=$?
L_LINE="$(grep -E '^Plan:|^No changes' <<< "$L_PLAN" | head -1 | sed 's/\.$//')"
[ "$L_RC" -eq 0 ] || { printf '%s\n' "$L_PLAN" | tail -20; fail "the plan with no record store at all exited $L_RC"; }
L_GONE="$(grep -cE '^[[:space:]]*# .* will be (created|destroyed|replaced)' <<< "$L_PLAN")"
[ "$L_GONE" = "0" ] || { printf '%s\n' "$L_PLAN" | grep -E 'will be' | head -20
  fail "with the whole record store deleted the plan proposes $L_GONE create/destroy/replace(s) ($L_LINE) - the objects are not being found by their label and their namespace and name alone, so a lost store costs objects and not just an apply"; }
L_CHANGES="$(grep -cE '^[[:space:]]*# .* will be updated in-place' <<< "$L_PLAN")"
L_RESIDUE="$(grep -oE '^[[:space:]]+\+ [a-z_]+ +=' <<< "$L_PLAN" | sed -E 's/^[[:space:]]*\+ //; s/ *=$//' | sort -u | tr '\n' ' ' | sed 's/ $//')"
L_APPLY="$(cd "$GREEN" && "$TOFU" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$L_APPLY" | tail -20; fail "the apply that should reconverge after a lost record store failed"; }
grep -qF "Apply complete! Resources: 0 added, $L_CHANGES changed, 0 destroyed" <<< "$L_APPLY" \
  || { printf '%s\n' "$L_APPLY" | tail -5; fail "the reconverging apply after a lost record store is not exactly the $L_CHANGES in-place update(s) the plan proposed"; }
L_REPLAN="$(cd "$GREEN" && "$TOFU" plan -input=false -no-color 2>&1)" || fail "the replan after the reconverging apply failed"
grep -q "No changes." <<< "$L_REPLAN" || { printf '%s\n' "$L_REPLAN" | tail -20; fail "the plan after the reconverging apply is not empty - a lost record store costs more than the one apply #1188 measured"; }
[ "$(count_a)" = "7" ] || fail "$(count_a) object(s) carry tofu-estate=$ESTATE after the lost-store reconvergence, want 7"
log "  lost store: $L_LINE, every object still bound; one apply reconverged and the plan after it is empty"
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
  gauntlet_stage greenfield pass "7 objects applied fresh with a live block and no terraform.tfstate, every one labelled tofu-estate=$ESTATE (kubectl, six kinds); the record store held $G_RECORDS file(s); replanned empty with and without the cache. Deleting the whole record store and the cache and replanning - the reading six AWS estates take here and no Kubernetes estate took - proposed $L_LINE: nothing created, nothing destroyed, nothing swept as an orphan, every object still bound by its label and its namespace and name, and $L_CHANGES in-place update(s) putting back the residue the store held (${L_RESIDUE:-none}); one apply reconverged and the plan after it is empty, so a lost store costs an apply here and not an object (#1188, #1235). The cluster's inventory (ConfigMap data, the Service's ports and selector, the Deployment's replicas and container, the ServiceAccount and namespace) matches stock's cold deploy on the same cluster object by object, labels never compared. BREAK=1 drops the Deployment from the expected inventory and the match correctly fails"
fi
( cd "$GREEN" && "$TOFU" apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "greenfield teardown failed"

# ── 13. strict: every toggle on, one refusal ─────────────────────────────
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
log "=== 13. strict: every strict toggle on ==="
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
