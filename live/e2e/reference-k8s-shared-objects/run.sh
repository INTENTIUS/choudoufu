#!/usr/bin/env bash
# reference-k8s-shared-objects: two estates on one cluster, meeting on the
# same objects through the field-granular types and field-manager ownership
# (#1882, under epic #1885; the ownership model is #1191's ruling of
# 2026-10-03, the removal and hand-over rows are #1869's). Hand-written under
# #1107's fallback rule: nothing published puts two estates on one object.
#
# The two estates, each its own root with its own live block and local
# record store:
#
#   platform  (live estate "shared-platform") owns whole objects, marked by
#             the tofu-estate label: a Namespace, a ConfigMap, a Secret, a
#             Service, a Deployment on the node image's pause container and
#             a two-instance count ConfigMap. It also owns one field of an
#             object neither estate owns - a label on the cluster's own
#             "default" Namespace, through kubernetes_labels - which is the
#             field app's force = true is refused over.
#   app       (live estate "shared-app") owns no object at all, only fields
#             of platform's objects and of the kind node, each written under
#             the field manager choudoufu:shared-app: kubernetes_labels on the
#             Namespace, kubernetes_annotations on the Service,
#             kubernetes_config_map_v1_data on the ConfigMap,
#             kubernetes_secret_v1_data on the Secret, kubernetes_env on the
#             Deployment's container and kubernetes_node_taint on the node.
#
# Why app's blocks are spread over five objects and not stacked on the
# Deployment as #1882's first sketch had them: two field-granular blocks of
# one estate on one object are refused ("Two field-granular blocks patch one
# object") - both would write under the one manager, and server-side apply
# drops the fields a manager's next apply leaves out. The sketch's own
# Deployment stack is that refusal's arm in plan_approval below.
#
# Why platform's blocks carry lifecycle ignore_changes on the keys app
# writes: a whole-object type reads the object back as it is, so without
# them platform's plan proposes removing app's label, annotation, data keys
# and env on every run, under stock and under choudoufu alike. Nothing in
# this fork filters another estate's fields out of a whole-object read
# today; the ignore_changes entries are what keeps each estate's plan off
# the other's fields, and BREAK_BOUNDARY=1 takes them away to show it.
# Each names one key, never the whole labels or annotations map, so the
# ignore_changes lint (#1645, #1740) has nothing to refuse.
#
# Two kind clusters, both created for this run and deleted after it:
#
#   A  the estates. Stock terraform cold-deploys platform and then app
#      (cold_deploy); choudoufu adopts both from stock's two state files
#      (migrate, which for app's six blocks is #1869's hand-over of the
#      fields from stock's "Terraform" manager to choudoufu:shared-app) and
#      runs every day-2 stage on them, tears both down, then applies both
#      fresh with live blocks (greenfield, no_local_state).
#   B  the oracle. Stock terraform cold-deploys the same two roots and
#      applies every day-2 change itself, so "what stock does" is always
#      stock's own plan on its own cluster.
#
# Where each of #1882's measurements is taken:
#
#   each estate's plan leaves the other's fields alone   both plans empty at
#                                                        the end of every
#                                                        day-2 stage
#                                                        (both_plans_empty)
#   force over platform's field refused, over kubectl's   plan_approval, the
#   accepted; two blocks of one estate on one object      boundary half
#   refused
#   day2_remove of an app block releases its fields       day2_remove
#   and leaves platform's object intact
#   teardown of app leaves platform converged; teardown   day2_teardown
#   of platform with app's fields present is stock's
#
# This subsumes live/kubernetes/proof-ssa-conflict.sh, which has not been
# run either; once this estate runs green the proof script is retired.
#
# Written under epic #1885's "build first, test last" ruling and NOT run by
# the change that added it: its first `gauntlet run` on kind is its first
# measurement, and reds there are fixed forward.
#
#   go run ./tools/gauntlet run reference-k8s-shared-objects
#   bash live/e2e/reference-k8s-shared-objects/run.sh
#
# Needs kind, kubectl, terraform (the stock binary), python3 and Docker on
# PATH. No ports: kind picks its own API server ports.
#
# Env overrides:
#   TOFU_BIN        path to a prebuilt choudoufu binary; skips the `go build`.
#   BREAK           set to 1 for drift_reconverge's, day2_rename's and
#                   greenfield's negative controls: a second object tampered
#                   (the single-object assertion must fail); app's
#                   kubernetes_config_map_v1_data pointed at another
#                   ConfigMap instead of moved (the zero-churn assertion must
#                   fail); a field dropped from greenfield's expected
#                   inventory (the match must fail).
#   BREAK_BOUNDARY  set to 1 to write platform without its ignore_changes
#                   entries in test_plan; platform's plan must then propose
#                   taking app's fields away, so "both plans empty" fails.
#   BREAK_FORCE     set to 1 to make plan_approval's forced write with the
#                   stock binary under app's field manager: it must take
#                   platform's label, showing the refusal is this fork's and
#                   not the API server's.
#   BREAK_APPROVAL  set to 1 to expect the saved plan to apply after the
#                   world moved (plan_approval's Break line); must fail.
#   BREAK_REMOVE    set to 1 to keep app's data block and assert no destroy
#                   is proposed (day2_remove's Break line).
#   BREAK_COUNT     set to 1 to assert the wrong shard was destroyed
#                   (day2_count's Break line); must fail.
#   BREAK_REPLACE   day2_replace's Break line, in live/e2e/lib/gauntlet.sh.
#   BREAK_CRASH     set to 1 to assert nothing is proposed after the real
#                   interrupts (day2_crash's Break line); must fail.
#   BREAK_CRASH_UNBOUND
#                   set to 1 to take the label the interrupted apply did
#                   write off the object before replanning; the recovery
#                   check must then fail to hold.
#   BREAK_NO_LOCAL_STATE
#                   set to 1 to strip platform's ConfigMap of its tofu-estate
#                   label before the no-local-state plan, which must then
#                   propose creating it.
#   BREAK_STRICT    set to 1 to turn secrets back to "store" and require the
#                   refusal to vanish (strict's Break line).
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# shellcheck source=live/e2e/lib/gauntlet.sh
source "$ROOT/live/e2e/lib/gauntlet.sh"

# The shared provider plugin cache and the lock real terraform needs to use
# it safely (#1300); live/e2e/lib/gauntlet.sh carries the reasons.
gauntlet_plugin_cache

# ESTATE is platform's live estate: the lane's label count, day2_replace's
# and day2_crash's shared bodies in the library, and every "objects carrying
# tofu-estate" line read it. APP_ESTATE owns fields only, so no label ever
# names it; its count is of objects its field manager owns fields on.
ESTATE="shared-platform"
APP_ESTATE="shared-app"
P_MANAGER="choudoufu:$ESTATE"
A_MANAGER="choudoufu:$APP_ESTATE"
NS="shared-objs"
KINDS="namespaces configmaps secrets services deployments"
WORK="$(mktemp -d)"
STOCK_P="$WORK/stock-platform"; STOCK_A="$WORK/stock-app"
ADOPTED="$WORK/platform"; APP="$WORK/app"
ORACLE="$WORK/oracle-platform"; ORACLE_A="$WORK/oracle-app"
GREEN="$WORK/green-platform"; GREEN_A="$WORK/green-app"
KCA="$WORK/a.kubeconfig"; KCB="$WORK/b.kubeconfig"
CLUSTER_A="${GAUNTLET_KIND_PREFIX:-chdf}-shobj-a-$$"; CLUSTER_B="${GAUNTLET_KIND_PREFIX:-chdf}-shobj-b-$$"  # GAUNTLET_KIND_PREFIX: lets concurrent workers name their own clusters
NODE_A="${CLUSTER_A}-control-plane"; NODE_B="${CLUSTER_B}-control-plane"
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

# day2_crash's interrupt needs the e2eTestingFeatures build, from this
# tree's source whatever $TOFU came from (reference-k8s/run.sh says why).
mkdir -p "$WORK/bin"
TOFU_CRASH="$WORK/bin/choudoufu-e2e"
( cd "$ROOT" && env -u PWD go build -ldflags="-X 'main.e2eTestingFeatures=yes'" -o "$TOFU_CRASH" ./cmd/choudoufu ) \
  || fail "go build -ldflags e2eTestingFeatures ./cmd/choudoufu failed"
log "  built $TOFU_CRASH (e2eTestingFeatures=yes, for day2_crash's interrupt)"

# ── the shape ────────────────────────────────────────────────────────────
K8S_REQUIRED_PROVIDER="$(gauntlet_kubernetes_required_provider)" \
  || fail "could not read the hashicorp/kubernetes pin from live/oracle-versions.json"

versions_block() { # $1 = the live estate's name, or "stock" for no live block
  cat <<EOF
terraform {
  required_providers {
$K8S_REQUIRED_PROVIDER
  }
EOF
  if [ "$1" != "stock" ]; then cat <<EOF
  live {
    estate = "$1"
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

# platform_block writes platform's resources. The knobs are globals so each
# stage changes only the one it is about:
#   P_SHARDS    the shard count (day2_count)
#   P_REVIEWED  1 adds reviewed = "yes" to the ConfigMap (plan_approval)
#   P_IGNORE    0 drops the ignore_changes entries (BREAK_BOUNDARY)
P_SHARDS=2; P_REVIEWED=0; P_IGNORE=1
platform_block() {
  local reviewed="" ns_ign="" svc_ign="" cm_ign="" sec_ign="" dep_ign="" shard_ign=""
  [ "$P_REVIEWED" = "1" ] && reviewed='    reviewed = "yes"'
  if [ "$P_IGNORE" = "1" ]; then
    ns_ign='  lifecycle {
    ignore_changes = [metadata[0].labels["app.shared/team"]]
  }'
    svc_ign='  lifecycle {
    ignore_changes = [metadata[0].annotations["app.shared/owner"]]
  }'
    cm_ign='  lifecycle {
    ignore_changes = [data["app_mode"]]
  }'
    sec_ign='  lifecycle {
    ignore_changes = [data["app_token"]]
  }'
    dep_ign='  lifecycle {
    ignore_changes = [spec[0].template[0].spec[0].container[0].env]
  }'
    shard_ign='  lifecycle {
    ignore_changes = [metadata[0].labels["app.shared/crash"]]
  }'
  fi
  cat <<EOF
resource "kubernetes_namespace_v1" "shared" {
  metadata {
    name = "$NS"
  }
$ns_ign
}

resource "kubernetes_config_map_v1" "settings" {
  metadata {
    name      = "settings"
    namespace = "$NS"
  }
  data = {
    region = "eu"
    tier   = "base"
$reviewed
  }
  depends_on = [kubernetes_namespace_v1.shared]
$cm_ign
}

resource "kubernetes_secret_v1" "creds" {
  metadata {
    name      = "creds"
    namespace = "$NS"
  }
  data = {
    db_user = "platform"
  }
  depends_on = [kubernetes_namespace_v1.shared]
$sec_ign
}

resource "kubernetes_service_v1" "web" {
  metadata {
    name      = "web"
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
  depends_on = [kubernetes_namespace_v1.shared]
$svc_ign
}

resource "kubernetes_deployment_v1" "web" {
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
          name  = "web"
          image = "registry.k8s.io/pause:3.10"
        }
      }
    }
  }
  wait_for_rollout = false
  depends_on       = [kubernetes_namespace_v1.shared]
$dep_ign
}

resource "kubernetes_config_map_v1" "shard" {
  count = $P_SHARDS
  metadata {
    name      = "shard-\${count.index}"
    namespace = "$NS"
  }
  data       = { shard = tostring(count.index) }
  depends_on = [kubernetes_namespace_v1.shared]
$shard_ign
}

# The one field platform owns on an object neither estate owns: a label on
# the cluster's own default Namespace, written under platform's manager.
resource "kubernetes_labels" "default_ns" {
  api_version = "v1"
  kind        = "Namespace"
  metadata {
    name = "default"
  }
  labels = {
    "shared-objects/platform" = "owned"
  }
}
EOF
}
write_platform() { # $1 dir, $2 estate name or "stock"
  { versions_block "$2"; echo; platform_block; } > "$1/main.tf"
}

# app_block writes app's six field-granular blocks. $1 is the node name (it
# differs per cluster). Knobs:
#   A_CM       the data block's name: settings, app_settings after the
#              rename, or empty once day2_remove has taken it out
#   A_CM_OBJ   the ConfigMap it patches (BREAK=1's rename control changes it)
A_CM="settings"; A_CM_OBJ="settings"
app_block() {
  local node="$1"
  cat <<EOF
resource "kubernetes_labels" "ns" {
  api_version = "v1"
  kind        = "Namespace"
  metadata {
    name = "$NS"
  }
  labels = {
    "app.shared/team" = "app"
  }
}

resource "kubernetes_annotations" "svc" {
  api_version = "v1"
  kind        = "Service"
  metadata {
    name      = "web"
    namespace = "$NS"
  }
  annotations = {
    "app.shared/owner" = "app"
  }
}

resource "kubernetes_secret_v1_data" "creds" {
  metadata {
    name      = "creds"
    namespace = "$NS"
  }
  data = {
    app_token = "app-token-value"
  }
}

resource "kubernetes_env" "web" {
  api_version = "apps/v1"
  kind        = "Deployment"
  container   = "web"
  metadata {
    name      = "web"
    namespace = "$NS"
  }
  env {
    name  = "APP_MODE"
    value = "shared"
  }
}

resource "kubernetes_node_taint" "node" {
  metadata {
    name = "$node"
  }
  taint {
    key    = "shared-objects/app"
    value  = "true"
    effect = "PreferNoSchedule"
  }
}
EOF
  if [ -n "$A_CM" ]; then cat <<EOF

resource "kubernetes_config_map_v1_data" "$A_CM" {
  metadata {
    name      = "$A_CM_OBJ"
    namespace = "$NS"
  }
  data = {
    app_mode = "shared"
  }
}
EOF
  fi
}
write_app() { # $1 dir, $2 estate name or "stock", $3 node name
  { versions_block "$2"; echo; app_block "$3"; } > "$1/main.tf"
}

# ── cluster helpers ──────────────────────────────────────────────────────
kca() { kubectl --kubeconfig "$KCA" "$@"; }
kcb() { kubectl --kubeconfig "$KCB" "$@"; }
# stock_b and stock_ab run the stock binary in the two oracle roots on B.
# stock_b is platform's; live/e2e/lib/gauntlet.sh's day2_replace body calls
# it by that name.
stock_b() { ( cd "$ORACLE" && KUBECONFIG="$KCB" KUBE_CONFIG_PATH="$KCB" terraform "$@" ); }
stock_ab() { ( cd "$ORACLE_A" && KUBECONFIG="$KCB" KUBE_CONFIG_PATH="$KCB" terraform "$@" ); }
# chdf <dir> <args...>: choudoufu in a root, against cluster A.
chdf() { local dir="$1"; shift; ( cd "$dir" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" "$TOFU" "$@" ); }
count_a() { KUBECONFIG="$KCA" gauntlet_kind_count "$ESTATE" $KINDS; }
exists_a() { kca get "$1" "$2" -n "$NS" >/dev/null 2>&1; }
plan_line() { grep -E '^Plan:|^No changes' <<< "$1" | head -1 | sed 's/\.$//'; }

# owners_of <kubeconfig> <kind> <name> <namespace|-> <FieldsV1 step>...:
# the managers whose metadata.managedFields hold the field the steps name,
# comma-joined and sorted; "UNREADABLE" when the object cannot be read.
# This is the ownership marker of a field-granular block, read by value
# from the API server with no provider in the loop.
owners_of() {
  python3 - "$@" <<'PY'
import json, subprocess, sys
_, cfg, kind, name, ns, *path = sys.argv
args = ["kubectl", "--kubeconfig", cfg, "get", kind, name, "-o", "json", "--show-managed-fields"]
if ns != "-":
    args += ["-n", ns]
out = subprocess.run(args, capture_output=True, text=True)
if out.returncode != 0:
    print("UNREADABLE")
    sys.exit(0)
owners = set()
for e in json.loads(out.stdout)["metadata"].get("managedFields") or []:
    if e.get("subresource"):
        continue
    cur = e.get("fieldsV1") or {}
    for step in path:
        if not isinstance(cur, dict) or step not in cur:
            break
        cur = cur[step]
    else:
        owners.add(e["manager"])
print(",".join(sorted(owners)))
PY
}

# owned_objects <kubeconfig> <manager>: how many objects the manager owns
# a field-granular field of, over every Namespace and Node and the
# ConfigMaps, Secrets, Services and Deployments in $NS - the field-granular
# estate's answer to the label count, since no label names it.
#
# A field is a KEY of one of the surfaces the six types write: a label or
# annotation key, a data key, an env item, or - taints being an atomic list
# - the taints of a node that has any. A released block leaves its
# manager's entry behind owning only the EMPTY map or env list
# (hashicorp/kubernetes deletes by applying one, #1885); that entry owns no
# field and is not counted. It is the rule choudoufu's own field-manager
# sweep uses (internal/live/discovery's matchFieldGranular).
owned_objects() {
  NS="$NS" python3 - "$@" <<'PY'
import json, os, subprocess, sys
_, cfg, manager = sys.argv
ns = os.environ["NS"]
n = 0
def at(d, *path):
    for p in path:
        if not isinstance(d, dict) or p not in d:
            return None
        d = d[p]
    return d if isinstance(d, dict) else None
def keys(d):
    return [k for k in (d or {}) if k != "."]
def owns_field(fields, obj):
    for path in [("f:metadata", "f:labels"), ("f:metadata", "f:annotations"), ("f:data",),
                 ("f:spec", "f:template", "f:metadata", "f:annotations")]:
        if keys(at(fields, *path)):
            return True
    for spec in [("f:spec",), ("f:spec", "f:template", "f:spec"), ("f:spec", "f:jobTemplate", "f:spec", "f:template", "f:spec")]:
        for lst in ("f:containers", "f:initContainers"):
            for item in (at(fields, *spec, lst) or {}).values():
                if keys(at(item, "f:env")):
                    return True
    if at(fields, "f:spec", "f:taints") is not None and (obj.get("spec") or {}).get("taints"):
        return True
    return False
for kind, scoped in [("namespaces", False), ("nodes", False), ("configmaps", True), ("secrets", True), ("services", True), ("deployments", True)]:
    args = ["kubectl", "--kubeconfig", cfg, "get", kind, "-o", "json", "--show-managed-fields"]
    if scoped:
        args += ["-n", ns]
    out = subprocess.run(args, capture_output=True, text=True)
    if out.returncode != 0:
        continue
    for o in json.loads(out.stdout).get("items", []):
        if any(e.get("manager") == manager and not e.get("subresource") and owns_field(e.get("fieldsV1") or {}, o)
               for e in o["metadata"].get("managedFields") or []):
            n += 1
print(n)
PY
}

# app_fields_owned <kubeconfig> <node> <manager> [held]: checks every field
# app's configuration writes against <manager> and prints, space-separated,
# the ones it does NOT own (empty when it owns them all) - or, with "held",
# the ones it still DOES own (empty when it owns none). $A_CM decides
# whether the ConfigMap's key is still app's.
app_fields_owned() {
  local cfg="$1" node="$2" want="$3" mode="${4:-}" out="" got
  _app_owns() { # <what> <kind> <name> <ns> <steps...>
    local what="$1" owns=0; shift
    got="$(owners_of "$cfg" "$@")"
    case ",$got," in *",$want,"*) owns=1 ;; esac
    if [ "$mode" = "held" ]; then
      [ "$owns" = "1" ] && out="$out ${what}[owners:${got}]"
    else
      [ "$owns" = "1" ] || out="$out ${what}[owners:${got:-none}]"
    fi
    return 0
  }
  _app_owns namespace-label namespace "$NS" - f:metadata f:labels "f:app.shared/team"
  _app_owns service-annotation service web "$NS" f:metadata f:annotations "f:app.shared/owner"
  _app_owns secret-data secret creds "$NS" f:data f:app_token
  _app_owns deployment-env deployment web "$NS" f:spec f:template f:spec f:containers 'k:{"name":"web"}' f:env 'k:{"name":"APP_MODE"}'
  _app_owns node-taint node "$node" - f:spec f:taints
  if [ -n "$A_CM" ]; then _app_owns configmap-data configmap "$A_CM_OBJ" "$NS" f:data f:app_mode; fi
  printf '%s' "$out"
}
# platform_field_owner <kubeconfig>: who owns platform's default-Namespace label.
platform_field_owner() { owners_of "$1" namespace default - f:metadata f:labels "f:shared-objects/platform"; }

# both_plans_empty <where>: #1882's first measurement, taken at the end of
# every day-2 stage - each estate's plan proposes nothing, so neither is
# about to write over the other's fields. Prints the reason and returns 1.
both_plans_empty() {
  local p a
  p="$(chdf "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$p" | tail -20 >&2; echo "platform's plan $1 failed: $(gauntlet_first_error_line <<< "$p")"; return 1; }
  a="$(chdf "$APP" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$a" | tail -20 >&2; echo "app's plan $1 failed: $(gauntlet_first_error_line <<< "$a")"; return 1; }
  grep -q "No changes." <<< "$p" || { printf '%s\n' "$p" | grep -E '^Plan:|will be|must be|^ +[+~-] ' | head -20 >&2; echo "platform's plan $1 is not empty ($(plan_line "$p"))"; return 1; }
  grep -q "No changes." <<< "$a" || { printf '%s\n' "$a" | grep -E '^Plan:|will be|must be|^ +[+~-] ' | head -20 >&2; echo "app's plan $1 is not empty ($(plan_line "$a"))"; return 1; }
}
must_both_be_empty() { local why; why="$(both_plans_empty "$1")" || fail "$why"; }

# inventory <kubeconfig> <node> [drop]: both estates' footprint on the
# cluster, normalised to what the two configurations declare: platform's
# objects (labels and annotations with the tofu-estate label, the address
# annotation and the server's own keys taken out) and every field app
# writes into them, plus platform's default-Namespace label and app's node
# taint. Field managers are not compared (stock writes under "Terraform").
inventory() {
  KUBECONFIG="$1" NODE="$2" DROP="${3:-}" NS="$NS" python3 - <<'PY'
import base64, json, os, subprocess
ns, node, drop = os.environ["NS"], os.environ["NODE"], os.environ.get("DROP", "")
def get(*args):
    out = subprocess.run(["kubectl", "get", *args, "-o", "json"], capture_output=True, text=True)
    return None if out.returncode != 0 else json.loads(out.stdout)
def keep(d, prefixes):
    return {k: v for k, v in (d or {}).items() if any(k.startswith(p) for p in prefixes)}
inv = {}
o = get("namespace", ns)
inv["namespace/" + ns] = None if o is None else {"labels": keep(o["metadata"].get("labels"), ["app.shared/"])}
o = get("namespace", "default")
inv["namespace/default"] = None if o is None else {"labels": keep(o["metadata"].get("labels"), ["shared-objects/"])}
for name in ["settings", "shard-0", "shard-1"]:
    o = get("configmap", name, "-n", ns)
    inv["configmap/" + name] = None if o is None else {"data": o.get("data", {}), "labels": keep(o["metadata"].get("labels"), ["app.shared/"])}
o = get("secret", "creds", "-n", ns)
inv["secret/creds"] = None if o is None else {"data": {k: base64.b64decode(v).decode() for k, v in (o.get("data") or {}).items()}}
o = get("service", "web", "-n", ns)
inv["service/web"] = None if o is None else {
    "annotations": keep(o["metadata"].get("annotations"), ["app.shared/"]),
    "selector": o["spec"].get("selector"),
    "ports": [{"port": p.get("port"), "targetPort": p.get("targetPort")} for p in o["spec"].get("ports", [])]}
o = get("deployment", "web", "-n", ns)
inv["deployment/web"] = None if o is None else {
    "replicas": o["spec"].get("replicas"),
    "containers": [{"name": c["name"], "image": c["image"], "env": [{"name": e["name"], "value": e.get("value")} for e in c.get("env", [])]}
                   for c in o["spec"]["template"]["spec"]["containers"]]}
o = get("node", node)
inv["node/taints"] = None if o is None else [t for t in (o["spec"].get("taints") or []) if t.get("key", "").startswith("shared-objects/")]
if drop:
    path = drop.split(".")
    cur = inv
    for step in path[:-1]:
        cur = cur.get(step) or {}
    cur.pop(path[-1], None)
print(json.dumps(inv, sort_keys=True, indent=1))
PY
}

# ── 1. cold_deploy: stock stands both estates up on A and on B ───────────
gauntlet_begin_stage cold_deploy
log "=== 1. cold_deploy: two kind clusters, stock terraform applies platform then app on each ==="
gauntlet_kind_up "$CLUSTER_A" "$KCA" || fail "kind cluster A ($CLUSTER_A) did not come up"
gauntlet_kind_up "$CLUSTER_B" "$KCB" || fail "kind cluster B ($CLUSTER_B) did not come up"
export KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA"
kca get node "$NODE_A" >/dev/null 2>&1 || fail "cluster A has no node named $NODE_A: $(kca get nodes -o name 2>&1 | tr '\n' ' ')"
kcb get node "$NODE_B" >/dev/null 2>&1 || fail "cluster B has no node named $NODE_B: $(kcb get nodes -o name 2>&1 | tr '\n' ' ')"
K8S_VERSION="$(kca version 2>/dev/null | gauntlet_k8s_server_version)"
mkdir -p "$STOCK_P" "$STOCK_A" "$ORACLE" "$ORACLE_A"
write_platform "$STOCK_P" stock; write_platform "$ORACLE" stock
write_app "$STOCK_A" stock "$NODE_A"; write_app "$ORACLE_A" stock "$NODE_B"
( cd "$STOCK_P" && gauntlet_locked_init terraform init -input=false -no-color >/dev/null 2>&1 ) || fail "stock init failed in platform's root on A"
( cd "$STOCK_A" && gauntlet_locked_init terraform init -input=false -no-color >/dev/null 2>&1 ) || fail "stock init failed in app's root on A"
COLD_P="$(cd "$STOCK_P" && terraform apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$COLD_P" | tail -20; fail "stock cold deploy of platform failed on A"; }
grep -qF "Apply complete! Resources: 8 added, 0 changed, 0 destroyed" <<< "$COLD_P" || { printf '%s\n' "$COLD_P" | tail -5; fail "stock cold deploy of platform did not add exactly 8 instances on A"; }
COLD_A="$(cd "$STOCK_A" && terraform apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$COLD_A" | tail -20; fail "stock cold deploy of app failed on A"; }
grep -qF "Apply complete! Resources: 6 added, 0 changed, 0 destroyed" <<< "$COLD_A" || { printf '%s\n' "$COLD_A" | tail -5; fail "stock cold deploy of app did not add exactly 6 instances on A"; }
[ -f "$STOCK_P/terraform.tfstate" ] && [ -f "$STOCK_A/terraform.tfstate" ] || fail "stock left no terraform.tfstate in one of the two roots on A"
[ "$(count_a)" = "0" ] || fail "$(count_a) object(s) already carry tofu-estate=$ESTATE after a plain stock apply - this proves nothing"
STOCK_OWNED="$(app_fields_owned "$KCA" "$NODE_A" Terraform)"
[ -z "$STOCK_OWNED" ] || fail "after stock's app apply, not every field app writes is owned by stock's default manager Terraform:$STOCK_OWNED"
[ "$(platform_field_owner "$KCA")" = "Terraform" ] || fail "platform's default-Namespace label is owned by '$(platform_field_owner "$KCA")' after stock's apply, want Terraform"
[ "$(owned_objects "$KCA" "$A_MANAGER")" = "0" ] || fail "choudoufu's app manager owns fields before choudoufu ran"
inventory "$KCA" "$NODE_A" > "$WORK/inventory.stock.json" || fail "could not read the cold-deployed inventory on A"
( cd "$ORACLE" && gauntlet_locked_init terraform init -input=false -no-color >/dev/null 2>&1 ) || fail "stock init failed in platform's oracle root"
( cd "$ORACLE_A" && gauntlet_locked_init terraform init -input=false -no-color >/dev/null 2>&1 ) || fail "stock init failed in app's oracle root"
{ APPLY_OUT="$(stock_b apply -auto-approve -input=false -no-color 2>&1)" && grep -qF "Apply complete! Resources: 8 added" <<< "$APPLY_OUT"; } || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "stock cold deploy of platform failed on B"; }
{ APPLY_OUT="$(stock_ab apply -auto-approve -input=false -no-color 2>&1)" && grep -qF "Apply complete! Resources: 6 added" <<< "$APPLY_OUT"; } || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "stock cold deploy of app failed on B"; }
log "  platform (8 instances) then app (6 field-granular instances) from plain terraform on A and on B; zero labels, app's fields owned by Terraform"
gauntlet_stage cold_deploy pass "two roots from plain terraform against kind $K8S_VERSION: platform's 8 instances (Namespace, 2 ConfigMaps plus a 2-instance count ConfigMap, Secret, Service, Deployment, and a kubernetes_labels on the cluster's default Namespace) and then app's 6 field-granular instances writing into them and into the node (kubernetes_labels, kubernetes_annotations, kubernetes_config_map_v1_data, kubernetes_secret_v1_data, kubernetes_env, kubernetes_node_taint), each root with a real terraform.tfstate; zero tofu-estate labels, and every field app writes owned by stock's default manager Terraform, read from metadata.managedFields with kubectl; the same two roots cold-deployed by stock on a second cluster as every later stage's oracle"

# ── 2. migrate: live-import against both of stock's state files ──────────
gauntlet_begin_stage migrate
log "=== 2. migrate: live-import -approve in platform's root, then app's ==="
mkdir -p "$ADOPTED" "$APP"
write_platform "$ADOPTED" "$ESTATE"; write_app "$APP" "$APP_ESTATE" "$NODE_A"
( cd "$ADOPTED" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "platform's adopted init failed"
( cd "$APP" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "app's adopted init failed"
MP_OUT="$(chdf "$ADOPTED" live-import -state="$STOCK_P/terraform.tfstate" -estate="$ESTATE" -approve -no-color 2>&1)" || { printf '%s\n' "$MP_OUT" | tail -20; fail "platform's live-import -approve failed"; }
MA_OUT="$(chdf "$APP" live-import -state="$STOCK_A/terraform.tfstate" -estate="$APP_ESTATE" -approve -no-color 2>&1)" || { printf '%s\n' "$MA_OUT" | tail -20; fail "app's live-import -approve failed"; }
MP_LINE="$(grep -E 'resource\(s\) newly stamped' <<< "$MP_OUT" | head -1 | sed 's/\.$//')"
MA_LINE="$(grep -E 'resource\(s\) newly stamped' <<< "$MA_OUT" | head -1 | sed 's/\.$//')"
log "  platform: ${MP_LINE:-no summary line}"
log "  app:      ${MA_LINE:-no summary line}"
M_WHY=""
grep -qF "8 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 0 skipped" <<< "$MP_OUT" \
  || M_WHY="$M_WHY platform's live-import did not report 8 newly stamped with 0 failed and 0 skipped (${MP_LINE:-no summary line}; first Error: $(gauntlet_first_error_line <<< "$MP_OUT"));"
grep -qF "6 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 0 skipped" <<< "$MA_OUT" \
  || M_WHY="$M_WHY app's live-import did not report 6 newly stamped with 0 failed and 0 skipped (${MA_LINE:-no summary line}; first Error: $(gauntlet_first_error_line <<< "$MA_OUT"));"
M_LABELLED="$(count_a)"
[ "$M_LABELLED" = "7" ] || M_WHY="$M_WHY $M_LABELLED of platform's 7 objects carry tofu-estate=$ESTATE;"
M_MISSING="$(app_fields_owned "$KCA" "$NODE_A" "$A_MANAGER")"
[ -z "$M_MISSING" ] || M_WHY="$M_WHY app's fields not handed to $A_MANAGER:$M_MISSING;"
M_LEFT="$(app_fields_owned "$KCA" "$NODE_A" Terraform held)"
[ -z "$M_LEFT" ] || M_WHY="$M_WHY Terraform still owns some of app's fields:$M_LEFT;"
[ "$(platform_field_owner "$KCA")" = "$P_MANAGER" ] || M_WHY="$M_WHY platform's default-Namespace label is owned by '$(platform_field_owner "$KCA")', want $P_MANAGER alone;"
if [ -z "$M_WHY" ]; then
  gauntlet_stage migrate pass "platform: 8 of 8 newly stamped, 0 failed, 0 skipped - its 7 objects carry tofu-estate=$ESTATE and the default-Namespace label moved from Terraform to $P_MANAGER; app: 6 of 6 newly stamped, 0 failed, 0 skipped - #1869's hand-over moved exactly the fields each block writes (the Namespace label, the Service annotation, the ConfigMap and Secret keys, the container's APP_MODE env, the node taint) from Terraform to $A_MANAGER with no value changed, and Terraform owns none of them now; all read from metadata.managedFields with kubectl"
else
  gauntlet_stage migrate fail "the migration of two estates sharing objects did not hold:$M_WHY"
fi

# ── 3. test_plan: replan from nothing, both estates ──────────────────────
gauntlet_begin_stage test_plan
log "=== 3. test_plan: both estates plan with no state file; identities and owners read with kubectl ==="
if [ "${BREAK_BOUNDARY:-}" = "1" ]; then
  P_IGNORE=0; write_platform "$ADOPTED" "$ESTATE"
  B_PLAN="$(chdf "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$B_PLAN" | tail -20; fail "BREAK_BOUNDARY: platform's plan without ignore_changes failed"; }
  P_IGNORE=1; write_platform "$ADOPTED" "$ESTATE"
  if grep -q "No changes." <<< "$B_PLAN"; then
    fail "BREAK_BOUNDARY=1: with its ignore_changes entries removed platform's plan is still empty, so they are not what keeps platform off app's fields and the boundary check below is not load-bearing"
  fi
  grep -qE 'app_mode|app_token|APP_MODE|app\.shared/' <<< "$B_PLAN" \
    || { printf '%s\n' "$B_PLAN" | tail -30; fail "BREAK_BOUNDARY=1: platform's plan without ignore_changes changes something, but nothing of app's"; }
  log "  BREAK_BOUNDARY=1: caught - without ignore_changes platform plans $(plan_line "$B_PLAN"), taking app's fields away"
  gauntlet_stage test_plan pass "BREAK_BOUNDARY=1 control: with platform's ignore_changes entries removed its plan proposes $(plan_line "$B_PLAN"), taking app's label, annotation, data keys and env away, so 'both plans empty' correctly fails; the real check is skipped"
else
  TP_P="$(chdf "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$TP_P" | tail -20; fail "platform's post-migration plan failed"; }
  TP_A="$(chdf "$APP" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$TP_A" | tail -20; fail "app's post-migration plan failed"; }
  IDS_MISSING=""
  kca get namespace "$NS" >/dev/null 2>&1 || IDS_MISSING="$IDS_MISSING namespace/$NS"
  for spec in "configmap settings" "configmap shard-0" "configmap shard-1" "secret creds" "service web" "deployment web"; do
    read -r kind name <<< "$spec"
    exists_a "$kind" "$name" || IDS_MISSING="$IDS_MISSING $NS/$kind/$name"
  done
  OWN_MISSING="$(app_fields_owned "$KCA" "$NODE_A" "$A_MANAGER")"
  if grep -q "No changes." <<< "$TP_P" && grep -q "No changes." <<< "$TP_A" && [ -z "$IDS_MISSING" ] && [ -z "$OWN_MISSING" ] && [ "$(platform_field_owner "$KCA")" = "$P_MANAGER" ]; then
    gauntlet_stage test_plan pass "with no state file both plans are empty - platform's and app's, each over objects the other writes into, so neither proposes touching the other's fields; platform's 7 identities (NAMESPACE/NAME) confirmed with kubectl, and app's 6 by value as the fields $A_MANAGER owns in metadata.managedFields (the patched object is the identity, the manager the marker), platform's default-Namespace label by $P_MANAGER. BREAK_BOUNDARY=1 drops platform's ignore_changes entries and its plan correctly proposes taking app's fields"
  else
    gauntlet_stage test_plan fail "with no state file: platform's plan is $(plan_line "$TP_P"), app's is $(plan_line "$TP_A"); identities missing:${IDS_MISSING:- none}; app fields not owned by $A_MANAGER:${OWN_MISSING:- none}; default-Namespace label owner: $(platform_field_owner "$KCA")"
    log "  converging both estates through choudoufu's own apply so the day-2 stages below run on adopted estates"
    ( chdf "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "platform's adopting apply failed"
    ( chdf "$APP" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "app's adopting apply failed"
    must_both_be_empty "after the adopting applies"
  fi
fi
[ "$(count_a)" = "7" ] || fail "$(count_a) object(s) carry tofu-estate=$ESTATE after adoption, want 7"

# ── 4. test_apply: no-op applies, counts unchanged ───────────────────────
gauntlet_begin_stage test_apply
log "=== 4. test_apply: apply both empty plans; neither count may move ==="
BEFORE_P="$(count_a)"; BEFORE_A="$(owned_objects "$KCA" "$A_MANAGER")"
[ "$BEFORE_A" = "6" ] || fail "$A_MANAGER owns fields on $BEFORE_A object(s) before the no-op apply, want 6"
NOOP_P="$(chdf "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$NOOP_P" | tail -20; fail "platform's no-op apply failed"; }
NOOP_A="$(chdf "$APP" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$NOOP_A" | tail -20; fail "app's no-op apply failed"; }
grep -qF "Apply complete! Resources: 0 added, 0 changed, 0 destroyed" <<< "$NOOP_P" || fail "platform's no-op apply changed something"
grep -qF "Apply complete! Resources: 0 added, 0 changed, 0 destroyed" <<< "$NOOP_A" || fail "app's no-op apply changed something"
AFTER_P="$(count_a)"; AFTER_A="$(owned_objects "$KCA" "$A_MANAGER")"
[ "$BEFORE_P" = "$AFTER_P" ] || fail "platform's labelled-object count moved across a no-op apply: $BEFORE_P -> $AFTER_P"
[ "$BEFORE_A" = "$AFTER_A" ] || fail "the objects $A_MANAGER owns fields on moved across a no-op apply: $BEFORE_A -> $AFTER_A"
gauntlet_stage test_apply pass "both no-op applies were 0 added, 0 changed, 0 destroyed; platform's objects carrying tofu-estate=$ESTATE unchanged at $BEFORE_P over the five kinds the count covers, and the objects $A_MANAGER owns fields on unchanged at $BEFORE_A (the field-granular estate's count, read from metadata.managedFields since no label names it)"

# ── 5. drift_reconverge: one of platform's fields tampered ───────────────
gauntlet_begin_stage drift_reconverge
log "=== 5. drift_reconverge: kubectl patch platform's ConfigMap on A and on B; stock's plan on B is the oracle ==="
kca patch configmap settings -n "$NS" --type merge -p '{"data":{"region":"tampered"}}' >/dev/null || fail "could not tamper settings on A"
kcb patch configmap settings -n "$NS" --type merge -p '{"data":{"region":"tampered"}}' >/dev/null || fail "could not tamper settings on B"
if [ "${BREAK:-}" = "1" ]; then
  kca patch configmap shard-0 -n "$NS" --type merge -p '{"data":{"shard":"tampered"}}' >/dev/null || fail "BREAK: could not tamper shard-0 on A"
fi
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's plan on B after the tamper failed"; }
grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's plan on B does not propose exactly one change"; }
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock could not reconverge B"
D_PLAN="$(chdf "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$D_PLAN" | tail -20; fail "platform's plan after the tamper failed"; }
if [ "${BREAK:-}" = "1" ]; then
  grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$D_PLAN" \
    && fail "BREAK=1: two objects were tampered but platform's plan still proposes exactly one change - the single-object assertion is not load-bearing"
  ( chdf "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: could not reconverge A"
  gauntlet_stage drift_reconverge pass "BREAK=1 control: with a second object tampered the single-object assertion correctly fails to hold ($(plan_line "$D_PLAN")); reconverged afterwards"
else
  grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$D_PLAN" || { printf '%s\n' "$D_PLAN" | tail -20; fail "platform's plan after one tamper does not propose exactly one change"; }
  grep -q "kubernetes_config_map_v1.settings will be updated in-place" <<< "$D_PLAN" || fail "platform's plan does not name kubernetes_config_map_v1.settings"
  grep -q "app_mode" <<< "$D_PLAN" && fail "platform's plan for the tamper touches app_mode, the key app writes into the same ConfigMap"
  DA_PLAN="$(chdf "$APP" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$DA_PLAN" | tail -20; fail "app's plan after the tamper failed"; }
  grep -q "No changes." <<< "$DA_PLAN" || { printf '%s\n' "$DA_PLAN" | tail -20; fail "app's plan is not empty after a tamper of platform's key in the ConfigMap they share"; }
  RECONV="$(chdf "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$RECONV" | tail -20; fail "platform's reconverging apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$RECONV" || fail "platform's reconverging apply did not change exactly one object"
  [ "$(kca get configmap settings -n "$NS" -o jsonpath='{.data.region}')" = "eu" ] || fail "settings' region does not read eu after reconverging"
  [ "$(kca get configmap settings -n "$NS" -o jsonpath='{.data.app_mode}')" = "shared" ] || fail "app's app_mode is gone from settings after platform's reconverging apply"
  [ "$(owners_of "$KCA" configmap settings "$NS" f:data f:app_mode)" = "$A_MANAGER" ] || fail "app_mode is owned by '$(owners_of "$KCA" configmap settings "$NS" f:data f:app_mode)' after platform's reconverging apply, want $A_MANAGER alone"
  must_both_be_empty "after the reconverging apply"
  gauntlet_stage drift_reconverge pass "one of platform's keys (settings.region) tampered with kubectl patch, in the ConfigMap app also writes app_mode into: platform proposed exactly kubernetes_config_map_v1.settings (0 add, 1 change, 0 destroy) and nothing about app_mode, matching stock's own plan on the oracle cluster; app's plan stayed empty; the apply changed 1, region reads back eu, and app_mode still reads shared, owned by $A_MANAGER alone in metadata.managedFields; both plans empty afterwards. BREAK=1 tampers a second object and the single-object assertion correctly fails"
fi

# ── 6. plan_approval: a saved plan, then the estate boundary's refusals ──
gauntlet_begin_stage plan_approval
log "=== 6. plan_approval: plan -out, the world moves, apply refuses; then force and same-object refusals ==="
P_REVIEWED=1; write_platform "$ADOPTED" "$ESTATE"; write_platform "$ORACLE" stock
PA_PLAN="$(chdf "$ADOPTED" plan -out=approved.tfplan -input=false -no-color 2>&1)" || { printf '%s\n' "$PA_PLAN" | tail -20; fail "platform's plan -out failed"; }
grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$PA_PLAN" || { printf '%s\n' "$PA_PLAN" | tail -10; fail "the saved plan is not exactly one change"; }
kca label configmap shard-0 -n "$NS" stray=yes >/dev/null || fail "could not move the world (label shard-0) on A"
PA_APPLY="$(chdf "$ADOPTED" apply -input=false -no-color approved.tfplan 2>&1)"; PA_RC=$?
if [ "${BREAK_APPROVAL:-}" = "1" ]; then
  [ "$PA_RC" -eq 0 ] && fail "BREAK_APPROVAL=1: applying the saved plan after the world moved succeeded - the refusal is not load-bearing"
  log "  BREAK_APPROVAL=1: caught - the apply after the world moved exited $PA_RC"
  kca label configmap shard-0 -n "$NS" stray- >/dev/null
  ( chdf "$ADOPTED" apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: the saved plan did not apply once the world was put back"
  PA_DETAIL="BREAK_APPROVAL=1 control: applying the saved plan after the world moved exited $PA_RC (refused), so the stage's Break line correctly fails; applied once the world was put back."
else
  [ "$PA_RC" -eq 3 ] || { printf '%s\n' "$PA_APPLY" | tail -20; fail "apply of the saved plan after the world moved exited $PA_RC, want 3 (the refusal)"; }
  grep -qF "The approved plan no longer matches the live system" <<< "$PA_APPLY" || { printf '%s\n' "$PA_APPLY" | tail -20; fail "the refusal does not carry its documented sentence"; }
  [ -z "$(kca get configmap settings -n "$NS" -o jsonpath='{.data.reviewed}')" ] || fail "settings gained reviewed despite the refusal"
  kca label configmap shard-0 -n "$NS" stray- >/dev/null || fail "could not put the world back"
  PA_APPLY2="$(chdf "$ADOPTED" apply -input=false -no-color approved.tfplan 2>&1)" || { printf '%s\n' "$PA_APPLY2" | tail -20; fail "the saved plan did not apply once the world was put back"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$PA_APPLY2" || fail "the saved plan's apply did not change exactly one object"
  [ "$(kca get configmap settings -n "$NS" -o jsonpath='{.data.reviewed}')" = "yes" ] || fail "settings does not read reviewed=yes after the saved plan applied"
  [ "$(kca get configmap settings -n "$NS" -o jsonpath='{.data.app_mode}')" = "shared" ] || fail "app's app_mode is gone from settings after platform's saved plan applied"
  PA_DETAIL="plan -out wrote one update (settings gains reviewed=yes, in the ConfigMap app writes app_mode into); the world then moved out of band (a stray label on shard-0, kubectl) and apply of the saved plan refused with \"The approved plan no longer matches the live system\" at exit 3, nothing applied; with the label removed the identical file applied, 0 added, 1 changed, 0 destroyed, reviewed=yes reads back and app_mode is untouched."
fi
( stock_b plan -out=approved.tfplan -input=false -no-color >/dev/null 2>&1 && stock_b apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "stock's own planfile did not apply on B"
must_both_be_empty "after the saved plan applied"

# The boundary half (#1191, #1106 section 3). Each arm is one extra block of
# app's in a file of its own, on the cluster's default Namespace, where
# platform owns one label under its own manager; app has no other block on
# that object, so none of these is the same-object refusal by accident.
boundary_labels() { # $1 block name, $2 label key, $3 "force" or ""
  local force=""
  [ "$3" = "force" ] && force="  force = true"
  cat > "$APP/boundary.tf" <<EOF
resource "kubernetes_labels" "$1" {
  api_version = "v1"
  kind        = "Namespace"
  metadata {
    name = "default"
  }
  labels = {
    "$2" = "app"
  }
$force
}
EOF
}
flat() { tr '\n' ' ' | tr -s ' '; }

# a. a write over platform's field, no force: warned by name, refused by the server by name.
boundary_labels over_platform "shared-objects/platform" ""
BW_PLAN="$(chdf "$APP" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$BW_PLAN" | tail -20; fail "app's plan over platform's label (no force) failed"; }
grep -q "Field owned by another estate" <<< "$BW_PLAN" || { printf '%s\n' "$BW_PLAN" | tail -30; fail "app's plan over platform's label does not warn \"Field owned by another estate\""; }
grep -qF "the estate \"$ESTATE\" owns" <<< "$(flat <<< "$BW_PLAN")" || fail "the warning does not name the estate $ESTATE"
BW_APPLY="$(chdf "$APP" apply -auto-approve -input=false -no-color 2>&1)"; BW_RC=$?
[ "$BW_RC" -ne 0 ] || fail "app's apply over platform's label went through without force"
grep -qF "conflict with \"$P_MANAGER\"" <<< "$(flat <<< "$BW_APPLY")" || { printf '%s\n' "$BW_APPLY" | tail -20; fail "the API server's refusal does not name $P_MANAGER"; }
[ "$(kca get namespace default -o jsonpath='{.metadata.labels.shared-objects/platform}')" = "owned" ] || fail "platform's label changed under a refused apply"

# b. the same write with force = true: refused by the plan, by name, nothing applied.
boundary_labels over_platform "shared-objects/platform" force
if [ "${BREAK_FORCE:-}" = "1" ]; then
  rm -f "$APP/boundary.tf"
  FS="$WORK/force-stock"; mkdir -p "$FS"
  { versions_block stock; echo; cat <<EOF
resource "kubernetes_labels" "over_platform" {
  api_version = "v1"
  kind        = "Namespace"
  metadata {
    name = "default"
  }
  labels = {
    "shared-objects/platform" = "app"
  }
  force         = true
  field_manager = "$A_MANAGER"
}
EOF
  } > "$FS/main.tf"
  ( cd "$FS" && gauntlet_locked_init terraform init -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_FORCE: stock init failed"
  ( cd "$FS" && terraform apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_FORCE: the stock forced apply failed, so the refusal cannot be told apart from the server's"
  [ "$(kca get namespace default -o jsonpath='{.metadata.labels.shared-objects/platform}')" = "app" ] \
    || fail "BREAK_FORCE=1: the stock forced write under $A_MANAGER did not take platform's label - without this fork's refusal the label should have moved"
  ( cd "$FS" && terraform apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_FORCE: the stock forced block could not be destroyed afterwards"
  ( chdf "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_FORCE: platform could not write its label back"
  gauntlet_stage plan_approval pass "$PA_DETAIL BREAK_FORCE=1 control: the stock binary made the same forced write under $A_MANAGER and took platform's label, so the refusal measured by the real check is this fork's and not the API server's; platform's label written back afterwards"
else
  BF_PLAN="$(chdf "$APP" plan -input=false -no-color 2>&1)"; BF_RC=$?
  [ "$BF_RC" = "1" ] || { printf '%s\n' "$BF_PLAN" | tail -20; fail "app's forced plan over platform's label exited $BF_RC, want 1"; }
  grep -q "Force refused over another estate's field" <<< "$BF_PLAN" || { printf '%s\n' "$BF_PLAN" | tail -30; fail "the forced plan is not refused by name"; }
  grep -qF "the estate \"$ESTATE\" owns" <<< "$(flat <<< "$BF_PLAN")" || fail "the force refusal does not name the estate $ESTATE"
  BF_APPLY="$(chdf "$APP" apply -auto-approve -input=false -no-color 2>&1)"; BF_ARC=$?
  [ "$BF_ARC" -ne 0 ] || { printf '%s\n' "$BF_APPLY" | tail -10; fail "app's forced apply over platform's label went through"; }
  [ "$(kca get namespace default -o jsonpath='{.metadata.labels.shared-objects/platform}')" = "owned" ] || fail "platform's label changed under a refused forced apply"
  [ "$(platform_field_owner "$KCA")" = "$P_MANAGER" ] || fail "platform's label is owned by '$(platform_field_owner "$KCA")' after the refused force, want $P_MANAGER alone"

  # c. the control: force over a label kubectl wrote keeps its ordinary meaning.
  kca label namespace default shared-objects/ops=kubectl >/dev/null || fail "could not write the kubectl-owned label on the default Namespace"
  boundary_labels over_kubectl "shared-objects/ops" force
  BK_PLAN="$(chdf "$APP" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$BK_PLAN" | tail -20; fail "app's forced plan over kubectl's label failed"; }
  grep -q "Force refused" <<< "$BK_PLAN" && fail "force over kubectl's label (a manager that is not an estate's) was refused"
  BK_APPLY="$(chdf "$APP" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$BK_APPLY" | tail -20; fail "app's forced apply over kubectl's label failed"; }
  grep -qF "Apply complete! Resources: 1 added, 0 changed, 0 destroyed" <<< "$BK_APPLY" || fail "app's forced apply over kubectl's label did not add exactly one instance"
  [ "$(kca get namespace default -o jsonpath='{.metadata.labels.shared-objects/ops}')" = "app" ] || fail "the forced apply did not take kubectl's label"
  [ "$(owners_of "$KCA" namespace default - f:metadata f:labels f:shared-objects/ops)" = "$A_MANAGER" ] || fail "the forced label is owned by '$(owners_of "$KCA" namespace default - f:metadata f:labels f:shared-objects/ops)', want $A_MANAGER alone"
  rm -f "$APP/boundary.tf"
  BK_RM="$(chdf "$APP" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$BK_RM" | tail -20; fail "removing the forced block failed"; }
  grep -qF "Apply complete! Resources: 0 added, 0 changed, 1 destroyed" <<< "$BK_RM" || { printf '%s\n' "$BK_RM" | tail -10; fail "removing the forced block did not release exactly one instance"; }
  [ -z "$(owners_of "$KCA" namespace default - f:metadata f:labels f:shared-objects/ops)" ] || fail "the released label is still owned by '$(owners_of "$KCA" namespace default - f:metadata f:labels f:shared-objects/ops)'"

  # d. two blocks of one estate on one object: #1882's own first sketch,
  #    labels and annotations stacked on the Deployment kubernetes_env
  #    already writes into.
  cat > "$APP/boundary.tf" <<EOF
resource "kubernetes_annotations" "web_extra" {
  api_version = "apps/v1"
  kind        = "Deployment"
  metadata {
    name      = "web"
    namespace = "$NS"
  }
  annotations = {
    "app.shared/extra" = "app"
  }
}
EOF
  BS_PLAN="$(chdf "$APP" plan -input=false -no-color 2>&1)"; BS_RC=$?
  rm -f "$APP/boundary.tf"
  [ "$BS_RC" = "1" ] || { printf '%s\n' "$BS_PLAN" | tail -20; fail "two of app's blocks on one Deployment planned with exit $BS_RC, want 1"; }
  grep -q "Two field-granular blocks patch one object" <<< "$BS_PLAN" || { printf '%s\n' "$BS_PLAN" | tail -30; fail "two of app's blocks on one Deployment are not refused by name"; }
  grep -q "kubernetes_annotations.web_extra" <<< "$BS_PLAN" && grep -q "kubernetes_env.web" <<< "$BS_PLAN" || fail "the same-object refusal does not name both blocks"
  must_both_be_empty "after the boundary arms"
  gauntlet_stage plan_approval pass "$PA_DETAIL Stock's own planfile applied on the oracle cluster in the unchanged case. The estate boundary's refusals, each an extra block of app's on the cluster's default Namespace, where platform owns one label under $P_MANAGER: without force the plan warned \"Field owned by another estate\" naming $ESTATE and the API server refused the apply with a conflict naming $P_MANAGER; with force = true the plan refused at exit 1, \"Force refused over another estate's field\", naming $ESTATE, nothing applied and the label still $P_MANAGER's; force over a label kubectl wrote planned and applied as stock's force does, the label then $A_MANAGER's alone, and removing the block released it. Two blocks of app's on one Deployment (kubernetes_env.web and a kubernetes_annotations) were refused, \"Two field-granular blocks patch one object\". Both plans empty afterwards. BREAK_APPROVAL=1 expects success after the move and correctly fails; BREAK_FORCE=1 makes the forced write with the stock binary under $A_MANAGER and it takes the label"
fi

# ── 7. day2_rename: app's data block, through a moved block ──────────────
gauntlet_begin_stage day2_rename
log "=== 7. day2_rename: kubernetes_config_map_v1_data.settings becomes .app_settings through a moved block ==="
moved_block() { cat <<'EOF'
moved {
  from = kubernetes_config_map_v1_data.settings
  to   = kubernetes_config_map_v1_data.app_settings
}
EOF
}
if [ "${BREAK:-}" = "1" ]; then
  # The control that can fire: the block points at another ConfigMap, which
  # is a change of the patched object - the field-granular identity - and
  # not a rename. It must plan a create and a destroy.
  A_CM="settings"; A_CM_OBJ="settings-elsewhere"; write_app "$APP" "$APP_ESTATE" "$NODE_A"; A_CM_OBJ="settings"
  R_PLAN="$(chdf "$APP" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK: the plan after pointing the block elsewhere failed"; }
  grep -qE 'will be (created|destroyed)' <<< "$R_PLAN" \
    || fail "BREAK=1: pointing the data block at another ConfigMap planned no create and no destroy - the zero-churn assertion is not load-bearing: $(plan_line "$R_PLAN")"
  log "  BREAK=1: caught - changing the patched object plans $(plan_line "$R_PLAN")"
fi
A_CM="app_settings"; write_app "$APP" "$APP_ESTATE" "$NODE_A"; moved_block > "$APP/moved.tf"
write_app "$ORACLE_A" stock "$NODE_B"; moved_block > "$ORACLE_A/moved.tf"
O_PLAN="$(stock_ab plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's moved-block plan failed on B"; }
grep -qE "^No changes|Plan: 0 to add, 0 to change, 0 to destroy" <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's moved-block plan on B is not zero churn"; }
( stock_ab apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's moved-block apply failed on B"
R_PLAN="$(chdf "$APP" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_PLAN" | tail -20; fail "app's moved-block plan failed"; }
grep -qE 'will be (created|destroyed)|must be replaced' <<< "$R_PLAN" \
  && { printf '%s\n' "$R_PLAN" | grep -E '^  # .+ (will|must) be'; fail "the moved-block rename of a field-granular block proposes a create, a destroy or a replace - the fields would be released and written again"; }
R_LINE="$(plan_line "$R_PLAN")"
R_APPLY="$(chdf "$APP" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$R_APPLY" | tail -20; fail "app's moved-block apply failed"; }
grep -qE "Apply complete! Resources: 0 added, [0-9]+ changed, 0 destroyed" <<< "$R_APPLY" || { printf '%s\n' "$R_APPLY" | tail -10; fail "app's moved-block apply added or destroyed something"; }
[ "$(owners_of "$KCA" configmap settings "$NS" f:data f:app_mode)" = "$A_MANAGER" ] || fail "app_mode is owned by '$(owners_of "$KCA" configmap settings "$NS" f:data f:app_mode)' after the rename, want $A_MANAGER alone"
must_both_be_empty "after the rename"
if [ "${BREAK:-}" = "1" ]; then
  gauntlet_stage day2_rename pass "BREAK=1 control: pointing app's kubernetes_config_map_v1_data at another ConfigMap - a change of the patched object, which is the field-granular identity - planned a create or a destroy, so the zero-churn assertion correctly fails to hold; the moved block then applied"
else
  gauntlet_stage day2_rename pass "moved block on a field-granular block: app's kubernetes_config_map_v1_data.settings -> .app_settings planned $R_LINE, no create, no destroy, no replace - the marker is the field manager and carries no address, so nothing on the object is rewritten - and applied without releasing the field: app_mode still owned by $A_MANAGER alone in metadata.managedFields; stock's plan for the same moved block on the oracle cluster is zero churn; both plans empty afterwards. BREAK=1 points the block at another ConfigMap instead, which changes its identity and plans a create or a destroy"
fi

# ── 8. day2_remove: app's data block leaves; its field is released ───────
gauntlet_begin_stage day2_remove
log "=== 8. day2_remove: app's kubernetes_config_map_v1_data block leaves the configuration ==="
if [ "${BREAK_REMOVE:-}" = "1" ]; then
  K_PLAN="$(chdf "$APP" plan -input=false -no-color 2>&1)" || fail "BREAK_REMOVE: app's plan with the block kept failed"
  grep -qE "will be destroyed|[1-9][0-9]* to destroy" <<< "$K_PLAN" && fail "BREAK_REMOVE=1: with the block kept a destroy was still proposed"
  log "  BREAK_REMOVE=1: caught - with the block kept no destroy is proposed ($(plan_line "$K_PLAN"))"
fi
A_CM=""; write_app "$APP" "$APP_ESTATE" "$NODE_A"; rm -f "$APP/moved.tf"
write_app "$ORACLE_A" stock "$NODE_B"; rm -f "$ORACLE_A/moved.tf"
O_PLAN="$(stock_ab plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's remove plan failed on B"; }
grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's remove plan on B is not exactly one destroy"; }
( stock_ab apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's remove apply failed on B"
X_PLAN="$(chdf "$APP" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$X_PLAN" | tail -20; fail "app's remove plan failed"; }
grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$X_PLAN" || { printf '%s\n' "$X_PLAN" | tail -20; fail "app's remove plan is not exactly one destroy"; }
# The fields have no address on the object, so the sweep files them at the
# synthetic orphan address of the one type whose fields they are
# (live/kubernetes/COMPATIBILITY.md, "A field-granular block removed").
X_ADDR="$(grep -E '^[[:space:]]*# .* will be destroyed' <<< "$X_PLAN" | head -1 | sed -E 's/^[[:space:]#]*//; s/ will be destroyed.*$//')"
[ "$X_ADDR" = "kubernetes_config_map_v1_data.orphan_${NS}_settings" ] || { printf '%s\n' "$X_PLAN" | grep -E 'destroyed|^Plan:'; fail "the one destroy is ${X_ADDR:-unnamed}, not kubernetes_config_map_v1_data.orphan_${NS}_settings"; }
X_APPLY="$(chdf "$APP" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$X_APPLY" | tail -20; fail "app's remove apply failed"; }
grep -qF "Apply complete! Resources: 0 added, 0 changed, 1 destroyed" <<< "$X_APPLY" || fail "app's remove apply did not destroy exactly one instance"
exists_a configmap settings || fail "platform's ConfigMap settings is gone after app released its field - the destroy took the object, not the field"
[ -z "$(kca get configmap settings -n "$NS" -o jsonpath='{.data.app_mode}')" ] || fail "app_mode is still in settings after app released it"
[ "$(kca get configmap settings -n "$NS" -o jsonpath='{.data.region}')" = "eu" ] || fail "platform's region key did not survive app's release"
[ -z "$(owners_of "$KCA" configmap settings "$NS" f:data f:app_mode)" ] || fail "app_mode's ownership survived the release: $(owners_of "$KCA" configmap settings "$NS" f:data f:app_mode)"
{ GET_OUT="$(kca get configmap -n "$NS" -l "tofu-estate=$ESTATE" -o name 2>/dev/null)" && grep -qx "configmap/settings" <<< "$GET_OUT"; } || { printf '%s\n' "$GET_OUT"; fail "settings no longer carries tofu-estate=$ESTATE after app's release"; }
DATA_A="$(kca get configmap settings -n "$NS" -o jsonpath='{.data}')"; DATA_B="$(kcb get configmap settings -n "$NS" -o jsonpath='{.data}')"
[ "$DATA_A" = "$DATA_B" ] || fail "settings' data differs from stock's end state on the oracle cluster: A=$DATA_A B=$DATA_B"
must_both_be_empty "after the release"
if [ "${BREAK_REMOVE:-}" = "1" ]; then
  gauntlet_stage day2_remove pass "BREAK_REMOVE=1 control: with app's data block kept, no destroy is proposed; the removal then applied as the real check does"
else
  gauntlet_stage day2_remove pass "deleting app's kubernetes_config_map_v1_data block proposed exactly one destroy, at the sweep's synthetic orphan address $X_ADDR - found by the fields $A_MANAGER owns, with no selector and no label - and the apply released the field and nothing else (#1869): app_mode is gone from platform's ConfigMap and owned by nobody, the ConfigMap is still there carrying tofu-estate=$ESTATE with region and every other platform key intact, its data identical to stock's end state for the same removal on the oracle cluster (stock: 0 add, 0 change, 1 destroy); both plans empty afterwards. BREAK_REMOVE=1 keeps the block and no destroy is proposed"
fi

# ── 9. day2_count: platform's shards 2 -> 1 -> 2 ─────────────────────────
gauntlet_begin_stage day2_count
log "=== 9. day2_count: kubernetes_config_map_v1.shard scales 2 -> 1 -> 2 ==="
scale_to() { P_SHARDS="$1"; write_platform "$ADOPTED" "$ESTATE"; write_platform "$ORACLE" stock; }
scale_to 1
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's scale-down plan failed on B"
grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's scale-down plan on B is not exactly one destroy"; }
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's scale-down apply failed on B"
C_PLAN="$(chdf "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$C_PLAN" | tail -20; fail "the scale-down plan failed"; }
grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$C_PLAN" || { printf '%s\n' "$C_PLAN" | tail -20; fail "the scale-down plan is not exactly one destroy"; }
C_ADDR="$(grep -E '^[[:space:]]*# .* will be destroyed' <<< "$C_PLAN" | head -1 | sed -E 's/^[[:space:]#]*//; s/ will be destroyed.*$//')"
grep -qE "^kubernetes_config_map_v1\.orphan_${NS}_shard-1$" <<< "$C_ADDR" || { printf '%s\n' "$C_PLAN" | grep -E 'destroyed|^Plan:'; fail "the scale-down destroys ${C_ADDR:-nothing named}, not shard-1 at its orphan address"; }
{ APPLY_OUT="$(chdf "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" && grep -qF "0 added, 0 changed, 1 destroyed" <<< "$APPLY_OUT"; } || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "the scale-down apply did not destroy exactly one object"; }
if [ "${BREAK_COUNT:-}" = "1" ]; then
  exists_a configmap shard-0 || fail "BREAK_COUNT=1: shard-0 was destroyed - the 'wrong instance' assertion would hold, so the check is not load-bearing"
  log "  BREAK_COUNT=1: caught - shard-0 still exists"
  scale_to 2
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_COUNT: stock's scale-up failed on B"
  ( chdf "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_COUNT: the scale-up failed"
  gauntlet_stage day2_count pass "BREAK_COUNT=1 control: asserting the lower index (shard-0) was destroyed correctly fails to hold; scaled back up afterwards"
else
  exists_a configmap shard-0 || fail "shard-0 was destroyed on the scale-down"
  exists_a configmap shard-1 && fail "shard-1 still exists after the scale-down"
  scale_to 2
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's scale-up plan failed on B"
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's scale-up plan on B is not exactly one add"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's scale-up apply failed on B"
  U_PLAN="$(chdf "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$U_PLAN" | tail -20; fail "the scale-up plan failed"; }
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$U_PLAN" || { printf '%s\n' "$U_PLAN" | tail -20; fail "the scale-up plan is not exactly one add"; }
  grep -q 'kubernetes_config_map_v1.shard\[1\]' <<< "$U_PLAN" || fail "the scale-up does not create shard[1]"
  { APPLY_OUT="$(chdf "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" && grep -qF "1 added, 0 changed, 0 destroyed" <<< "$APPLY_OUT"; } || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "the scale-up apply did not create exactly one object"; }
  exists_a configmap shard-0 && exists_a configmap shard-1 || fail "both shards do not exist after the scale-up"
  must_both_be_empty "after the count cycle"
  [ "$(count_a)" = "7" ] || fail "$(count_a) labelled objects after the count cycle, want 7"
  gauntlet_stage day2_count pass "scaling platform's kubernetes_config_map_v1.shard from 2 to 1 destroyed exactly shard-1, planned at the sweep's orphan address $C_ADDR (shard-0 untouched, both read with kubectl); back to 2 created exactly kubernetes_config_map_v1.shard[1]; both estates' plans empty afterwards; stock's plans for the same two changes on the oracle cluster have the identical shape. BREAK_COUNT=1 asserts the lower index was destroyed and correctly fails"
fi

# ── 9b. day2_replace: a create_before_destroy rename, in platform ────────
gauntlet_begin_stage day2_replace
log "=== 9b. day2_replace: a content-hashed ConfigMap renamed under create_before_destroy, in platform's root ==="
gauntlet_kind_day2_replace "$ADOPTED" "$ORACLE" "$NS"

# ── 10. day2_crash: interrupted applies ──────────────────────────────────
#
# The create_before_destroy rename window first, in platform's root (#1768,
# the library body every kind estate shares). Then app's own window: an
# apply of two field-granular blocks, each writing a label into one of
# platform's shards, killed the instant the first one's write commits.
# crash_second's label reads crash_first's own attribute, so the walker at
# -parallelism=1 cannot have reached it. The next plan must propose exactly
# the remainder and bind the first block by the field its manager now owns,
# not write it again and not sweep it as an orphan.
gauntlet_begin_stage day2_crash
log "=== 10. day2_crash: the rename window in platform, then SIGTERM between two field-granular writes in app ==="
# day2_replace's library body reports its own verdict, so the boundary
# check after it is this stage's starting condition.
must_both_be_empty "after day2_replace"
gauntlet_kind_day2_crash_rename "$ADOPTED" "$NS"

crash_pair() { # $1: first or both
  cat <<EOF
resource "kubernetes_labels" "crash_first" {
  api_version = "v1"
  kind        = "ConfigMap"
  metadata {
    name      = "shard-0"
    namespace = "$NS"
  }
  labels = {
    "app.shared/crash" = "one"
  }
}
EOF
  [ "$1" = "first" ] && return 0
  cat <<EOF

resource "kubernetes_labels" "crash_second" {
  api_version = "v1"
  kind        = "ConfigMap"
  metadata {
    name      = "shard-1"
    namespace = "$NS"
  }
  labels = {
    "app.shared/crash" = "after-\${kubernetes_labels.crash_first.metadata[0].name}"
  }
}
EOF
}
crash_pair first > "$ORACLE_A/crash.tf"
O_PLAN="$(stock_ab plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's crash-first plan failed on B"; }
grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's crash-first plan on B is not exactly one add"; }
( stock_ab apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's crash-first apply failed on B"
crash_pair both > "$ORACLE_A/crash.tf"
O_REMAINDER="$(stock_ab plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_REMAINDER" | tail -10; fail "stock's remainder plan failed on B"; }
grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_REMAINDER" || { printf '%s\n' "$O_REMAINDER" | tail -10; fail "stock's remainder plan on B is not exactly one add"; }
grep -q 'kubernetes_labels.crash_second' <<< "$O_REMAINDER" || fail "stock's remainder plan on B does not name crash_second"
( stock_ab apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's remainder apply failed on B"
log "  oracle: stock at crash_first alone plans exactly one add (crash_second) for the remainder"

crash_pair both > "$APP/crash.tf"
X_PLAN="$(chdf "$APP" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$X_PLAN" | tail -20; fail "app's pre-crash plan failed"; }
grep -qF "Plan: 2 to add, 0 to change, 0 to destroy." <<< "$X_PLAN" || { printf '%s\n' "$X_PLAN" | tail -20; fail "app's pre-crash plan is not exactly two adds - there is no two-block apply to interrupt"; }
X_RECORDS_BEFORE="$(gauntlet_record_envelope_count "$APP/.tofu-records")"
# A non-zero exit is the normal outcome: the engine killed itself.
X_OUT="$(cd "$APP" && TOFU_E2E_APPLY_RESOURCE_INTERRUPT="kubernetes_labels.crash_first" "$TOFU_CRASH" apply -input=false -auto-approve -no-color -parallelism=1 2>&1)"; X_RC=$?
log "  interrupted apply exited $X_RC"
[ "$X_RC" -ne 0 ] || { printf '%s\n' "$X_OUT" | tail -20; fail "the interrupted apply exited 0 - the engine's self-signal never landed"; }
[ "$(owners_of "$KCA" configmap shard-0 "$NS" f:metadata f:labels f:app.shared/crash)" = "$A_MANAGER" ] \
  || { printf '%s\n' "$X_OUT" | tail -20; fail "shard-0's crash label is owned by '$(owners_of "$KCA" configmap shard-0 "$NS" f:metadata f:labels f:app.shared/crash)' after the interrupted apply, want $A_MANAGER - the kill landed before the first write committed"; }
[ -z "$(kca get configmap shard-1 -n "$NS" -o jsonpath='{.metadata.labels.app\.shared/crash}')" ] \
  || { printf '%s\n' "$X_OUT" | tail -20; fail "shard-1 carries the crash label after the interrupted apply - the kill landed after both writes"; }
X_RECORDS_AFTER="$(gauntlet_record_envelope_count "$APP/.tofu-records")"
X_REC="$(gauntlet_record_file "$APP/.tofu-records" "kubernetes_labels.crash_first")"
[ -n "$X_REC" ] || fail "the interrupted apply wrote crash_first's label but no record for kubernetes_labels.crash_first (records $X_RECORDS_BEFORE -> $X_RECORDS_AFTER)"
if [ "${BREAK_CRASH_UNBOUND:-}" = "1" ]; then
  kca label configmap shard-0 -n "$NS" app.shared/crash- >/dev/null 2>&1 || fail "BREAK_CRASH_UNBOUND: could not take the crash label off shard-0"
  log "  BREAK_CRASH_UNBOUND=1: took the label crash_first wrote off shard-0 with kubectl"
fi
R_PLAN="$(chdf "$APP" plan -input=false -no-color 2>&1)"; R_RC=$?
R_LINE="$(plan_line "$R_PLAN")"
recovered() {
  [ "$R_RC" -eq 0 ] || return 1
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$R_PLAN" || return 1
  grep -qE '^[[:space:]]*# kubernetes_labels\.crash_second will be created' <<< "$R_PLAN" || return 1
  local r_proposed
  r_proposed="$(grep -E '^[[:space:]]*# .* will be' <<< "$R_PLAN")"
  grep -q 'crash_first\|orphan_' <<< "$r_proposed" && return 1
  return 0
}
if [ "${BREAK_CRASH:-}" = "1" ]; then
  grep -q "No changes." <<< "$R_PLAN" && fail "BREAK_CRASH=1: the plan after a real interrupted two-block apply is empty, so this stage's check is not load-bearing"
  gauntlet_stage day2_crash pass "BREAK_CRASH=1 control: after the real interrupt between app's two field-granular writes the plan proposes work ($R_LINE), so 'nothing is proposed' correctly fails to hold; the real check is skipped. $CRASH_RENAME_DETAIL"
  ( chdf "$APP" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_CRASH: the recovery apply failed afterwards"
elif [ "${BREAK_CRASH_UNBOUND:-}" = "1" ]; then
  recovered && fail "BREAK_CRASH_UNBOUND=1: the recovery check still holds with crash_first's label taken off shard-0 - it is not measuring whether the written field was bound"
  gauntlet_stage day2_crash pass "BREAK_CRASH_UNBOUND=1 control: with the label crash_first wrote taken off shard-0 out of band, the recovery check correctly fails to hold ($R_LINE); the real check is skipped. $CRASH_RENAME_DETAIL"
  ( chdf "$APP" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_CRASH_UNBOUND: the apply after the control failed"
else
  if ! recovered; then
    printf '%s\n' "$R_PLAN" | grep -E '^Plan:|^No changes|will be' | head -20
    gauntlet_stage day2_crash fail "after a real interrupt between app's kubernetes_labels.crash_first write and crash_second's, the plan is not exactly the remainder: ${R_LINE:-no plan line} (exit $R_RC). shard-0's label is owned by $A_MANAGER and shard-1 carries none, both read with kubectl; stock, walked into the same position on the oracle cluster, plans exactly one add (crash_second). Records $X_RECORDS_BEFORE -> $X_RECORDS_AFTER. $CRASH_RENAME_DETAIL"
  else
    R_APPLY="$(chdf "$APP" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$R_APPLY" | tail -20; fail "app's recovery apply failed"; }
    grep -qF "Apply complete! Resources: 1 added, 0 changed, 0 destroyed" <<< "$R_APPLY" || fail "app's recovery apply did not add exactly the one remaining block"
    [ "$(kca get configmap shard-1 -n "$NS" -o jsonpath='{.metadata.labels.app\.shared/crash}')" = "after-shard-0" ] || fail "shard-1 does not carry crash_second's label after the recovery"
    [ "$(owners_of "$KCA" configmap shard-0 "$NS" f:metadata f:labels f:app.shared/crash)" = "$A_MANAGER" ] || fail "shard-0's crash label lost its owner in the recovery"
    must_both_be_empty "after the crash recovery"
    gauntlet_stage day2_crash pass "app's apply of two field-granular blocks, each labelling one of platform's shards, was interrupted by a real SIGTERM (exit $X_RC) the engine delivered itself at -parallelism=1 the instant kubernetes_labels.crash_first's write committed; crash_second's label reads crash_first's name, so the walker cannot have reached it - kubectl confirms shard-0's label owned by $A_MANAGER and shard-1 unlabelled, and the interrupted apply wrote crash_first's record (records $X_RECORDS_BEFORE -> $X_RECORDS_AFTER). The next plan proposed exactly the remainder ($R_LINE, kubernetes_labels.crash_second created) and nothing for crash_first, which it bound by the field its manager owns - not a second write, not an orphan release - matching stock's own plan from the same position on the oracle cluster; the recovery added exactly one, shard-1 reads after-shard-0, and both estates' plans are empty afterwards. BREAK_CRASH=1 asserts nothing is proposed and correctly fails; BREAK_CRASH_UNBOUND=1 takes the label off shard-0 and the recovery check correctly fails. $CRASH_RENAME_DETAIL"
  fi
fi
gauntlet_end_stage

# ── 11. day2_teardown: app first, then platform with app's fields present ─
gauntlet_begin_stage day2_teardown
log "=== 11. day2_teardown: app's apply -destroy leaves platform converged; platform's with app's fields present is stock's ==="
P_COUNT="$(count_a)"
A_OWNED="$(owned_objects "$KCA" "$A_MANAGER")"
[ "$A_OWNED" = "7" ] || fail "$A_MANAGER owns fields on $A_OWNED object(s) before the teardown, want 7 (five of the cold shape after day2_remove, plus the two shards day2_crash labelled)"
TA_OUT="$(chdf "$APP" apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$TA_OUT" | tail -20; fail "app's apply -destroy failed"; }
grep -qF "Resources: 0 added, 0 changed, $A_OWNED destroyed" <<< "$TA_OUT" || { printf '%s\n' "$TA_OUT" | tail -5; fail "app's apply -destroy did not release exactly its $A_OWNED instances"; }
[ "$(owned_objects "$KCA" "$A_MANAGER")" = "0" ] || fail "$A_MANAGER still owns fields on $(owned_objects "$KCA" "$A_MANAGER") object(s) after app's teardown"
[ "$(count_a)" = "$P_COUNT" ] || fail "platform's labelled objects went $P_COUNT -> $(count_a) across app's teardown"
TP_PLAN="$(chdf "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$TP_PLAN" | tail -20; fail "platform's plan after app's teardown failed"; }
grep -q "No changes." <<< "$TP_PLAN" || { printf '%s\n' "$TP_PLAN" | tail -20; fail "platform's plan after app's teardown is not empty - releasing app's fields disturbed platform's objects"; }
( stock_ab apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's teardown of app failed on B"
log "  app torn down: $A_OWNED released, platform's $P_COUNT objects untouched and its plan empty"

# Put app's fields back on both clusters, then tear platform down under them.
RA_OUT="$(chdf "$APP" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$RA_OUT" | tail -20; fail "app's re-apply before platform's teardown failed"; }
grep -qF "Apply complete! Resources: $A_OWNED added, 0 changed, 0 destroyed" <<< "$RA_OUT" || fail "app's re-apply did not add back its $A_OWNED instances"
( stock_ab apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's re-apply of app failed on B"
P_EXPECT=$((P_COUNT + 1))   # the labelled objects plus the default-Namespace label
TT_OUT="$(chdf "$ADOPTED" apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$TT_OUT" | tail -20; fail "platform's apply -destroy with app's fields present failed"; }
grep -qF "Resources: 0 added, 0 changed, $P_EXPECT destroyed" <<< "$TT_OUT" || { printf '%s\n' "$TT_OUT" | tail -5; fail "platform's apply -destroy did not remove exactly its $P_EXPECT instances"; }
O_EXPECT="$(stock_b state list 2>/dev/null | wc -l | tr -d ' ')"
O_TT="$(stock_b apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$O_TT" | tail -10; fail "stock's teardown of platform with app's fields present failed on B"; }
grep -qF "Resources: 0 added, 0 changed, $O_EXPECT destroyed" <<< "$O_TT" || fail "stock's teardown of platform on B did not remove exactly the $O_EXPECT its state held"
[ "$O_EXPECT" = "$P_EXPECT" ] || fail "platform destroyed $P_EXPECT instances and stock $O_EXPECT for the same estate"
for _ in $(seq 1 30); do kca get namespace "$NS" >/dev/null 2>&1 || break; sleep 2; done
kca get namespace "$NS" >/dev/null 2>&1 && fail "the $NS namespace still exists after platform's destroy"
[ "$(count_a)" = "0" ] || fail "$(count_a) object(s) still carry tofu-estate=$ESTATE after platform's destroy"
[ -z "$(platform_field_owner "$KCA")" ] || fail "platform's default-Namespace label is still owned by '$(platform_field_owner "$KCA")'"
# What is left of app, on each cluster: the node taint (the node outlives
# platform) and blocks whose objects are gone. app's next plan on A must be
# stock's next plan on B.
for _ in $(seq 1 30); do kcb get namespace "$NS" >/dev/null 2>&1 || break; sleep 2; done
AP_PLAN="$(chdf "$APP" plan -input=false -no-color 2>&1)"; AP_RC=$?
AO_PLAN="$(stock_ab plan -input=false -no-color 2>&1)"; AO_RC=$?
AP_LINE="$(plan_line "$AP_PLAN")"; AO_LINE="$(plan_line "$AO_PLAN")"
[ "$AP_RC" = "$AO_RC" ] && [ "$AP_LINE" = "$AO_LINE" ] \
  || { printf '%s\n' "$AP_PLAN" | grep -E '^Plan:|will be|^Error' | head -10; printf '%s\n' "$AO_PLAN" | grep -E '^Plan:|will be|^Error' | head -10
       fail "with platform gone and app's fields left behind, app's plan is '${AP_LINE:-none}' (exit $AP_RC) and stock's on the oracle cluster '${AO_LINE:-none}' (exit $AO_RC)"; }
[ "$(owners_of "$KCA" node "$NODE_A" - f:spec f:taints)" = "$A_MANAGER" ] || fail "app's node taint is not $A_MANAGER's after platform's teardown"
TF_OUT="$(chdf "$APP" apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$TF_OUT" | tail -20; fail "app's final apply -destroy failed"; }
( stock_ab apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's final teardown of app failed on B"
[ "$(owned_objects "$KCA" "$A_MANAGER")" = "0" ] || fail "$A_MANAGER still owns fields after app's final teardown"
[ -z "$(kca get node "$NODE_A" -o jsonpath='{.spec.taints[?(@.key=="shared-objects/app")].key}')" ] || fail "app's node taint survived app's final teardown"
gauntlet_stage day2_teardown pass "app first: apply -destroy released exactly its $A_OWNED field-granular instances in one apply, $A_MANAGER owns no label, annotation or data key, env item or taint anywhere afterwards (a released block's manager entry is left owning only an empty map or env list, and is not counted), and platform's $P_COUNT objects still carry tofu-estate=$ESTATE with its plan empty - app's teardown left platform converged. Then, with app's fields written back, platform's apply -destroy removed exactly its $P_EXPECT instances (its labelled objects and the default-Namespace label) in one apply, the same count stock's destroy of the same estate removed on the oracle cluster with app's fields present there too; the namespace is gone, no object carries tofu-estate=$ESTATE and platform's label is released. What app had left behind planned the same on both: '${AP_LINE:-none}' here (exit $AP_RC), '${AO_LINE:-none}' from stock (exit $AO_RC); app's final destroy released the node taint"

# ── 12. greenfield: both estates fresh, with live blocks ─────────────────
gauntlet_begin_stage greenfield
log "=== 12. greenfield: choudoufu applies platform then app fresh on the now-empty cluster A ==="
mkdir -p "$GREEN" "$GREEN_A"
P_SHARDS=2; P_REVIEWED=0; P_IGNORE=1; A_CM="settings"; A_CM_OBJ="settings"
write_platform "$GREEN" "$ESTATE"; write_app "$GREEN_A" "$APP_ESTATE" "$NODE_A"
( cd "$GREEN" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "platform's greenfield init failed"
( cd "$GREEN_A" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "app's greenfield init failed"
GP_OUT="$(chdf "$GREEN" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$GP_OUT" | tail -20; fail "platform's greenfield apply failed"; }
grep -qF "Apply complete! Resources: 8 added, 0 changed, 0 destroyed" <<< "$GP_OUT" || fail "platform's greenfield apply did not add exactly 8"
GA_OUT="$(chdf "$GREEN_A" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$GA_OUT" | tail -20; fail "app's greenfield apply failed"; }
grep -qF "Apply complete! Resources: 6 added, 0 changed, 0 destroyed" <<< "$GA_OUT" || fail "app's greenfield apply did not add exactly 6"
[ ! -f "$GREEN/terraform.tfstate" ] && [ ! -f "$GREEN_A/terraform.tfstate" ] || fail "a terraform.tfstate appeared after a live-block apply"
[ "$(count_a)" = "7" ] || fail "$(count_a) object(s) carry tofu-estate=$ESTATE after the greenfield apply, want 7"
G_MISSING="$(app_fields_owned "$KCA" "$NODE_A" "$A_MANAGER")"
[ -z "$G_MISSING" ] || fail "after app's greenfield apply these fields are not $A_MANAGER's:$G_MISSING"
[ "$(platform_field_owner "$KCA")" = "$P_MANAGER" ] || fail "platform's default-Namespace label is owned by '$(platform_field_owner "$KCA")' after greenfield, want $P_MANAGER"
for d in "$GREEN" "$GREEN_A"; do
  G_PLAN="$(chdf "$d" plan -input=false -no-color 2>&1)" || fail "the greenfield replan in $(basename "$d") failed"
  grep -q "No changes." <<< "$G_PLAN" || { printf '%s\n' "$G_PLAN" | tail -20; fail "the greenfield replan in $(basename "$d") is not empty"; }
done
DROP=""; [ "${BREAK:-}" = "1" ] && DROP="configmap/settings.data"
inventory "$KCA" "$NODE_A" "$DROP" > "$WORK/inventory.green.json" || fail "could not read the greenfield inventory"
if [ "${BREAK:-}" = "1" ]; then
  diff -q "$WORK/inventory.stock.json" "$WORK/inventory.green.json" >/dev/null \
    && fail "BREAK=1: with settings' data dropped from the greenfield inventory the two inventories still match - the comparison is not load-bearing"
  gauntlet_stage greenfield pass "BREAK=1 control: dropping settings' data (where app's key lives beside platform's) from the greenfield inventory makes the comparison correctly fail; both estates applied fresh (8 and 6 added, no terraform.tfstate) and replanned empty"
else
  diff -u "$WORK/inventory.stock.json" "$WORK/inventory.green.json" || fail "the greenfield inventory differs from stock's cold-deploy inventory (diff above)"
  gauntlet_stage greenfield pass "both estates applied fresh with live blocks and no terraform.tfstate: platform 8 added, its 7 objects labelled tofu-estate=$ESTATE and its default-Namespace label owned by $P_MANAGER; app 6 added, every field it writes owned by $A_MANAGER in metadata.managedFields; both replanned empty. The cluster's inventory - platform's objects and every field app wrote into them, the default-Namespace label and the node taint - matches stock's cold deploy of the same two roots on the same cluster field by field, labels and managers normalised out. BREAK=1 drops settings' data from the expected inventory and the match correctly fails"
fi

# ── 13. no_local_state: both record stores and caches gone ───────────────
gauntlet_begin_stage no_local_state
log "=== 13. no_local_state: delete both estates' record stores and state caches, then plan ==="
# api_calls <log>: the provider's HTTP requests in a TF_LOG=DEBUG log.
api_calls() { [ -f "$1" ] || { echo 0; return; }; grep -cE 'Sending HTTP Request|HTTP Request Sent' "$1" || true; }
NLS_BASE=0; NLS_CALLS=0; NLS_BAD=""
for d in "$GREEN" "$GREEN_A"; do
  n="$(basename "$d")"
  base="$(cd "$d" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" TF_LOG=DEBUG TF_LOG_PATH="$WORK/nls.base.$n.log" "$TOFU" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$base" | tail -20; fail "the cache-serving plan in $n failed"; }
  grep -q "No changes." <<< "$base" || fail "the cache-serving plan in $n is not empty, so it is no baseline"
  NLS_BASE=$((NLS_BASE + $(api_calls "$WORK/nls.base.$n.log")))
  rm -rf "$d/.tofu-records" "$d/.terraform/choudoufu-cache.tfstate"
  [ ! -e "$d/.tofu-records" ] && [ ! -e "$d/.terraform/choudoufu-cache.tfstate" ] || fail "the local state in $n survived deletion"
done
if [ "${BREAK_NO_LOCAL_STATE:-}" = "1" ]; then
  kca label configmap settings -n "$NS" tofu-estate- >/dev/null || fail "BREAK_NO_LOCAL_STATE: could not strip settings' label"
fi
for d in "$GREEN" "$GREEN_A"; do
  n="$(basename "$d")"
  plan="$(cd "$d" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" TF_LOG=DEBUG TF_LOG_PATH="$WORK/nls.$n.log" "$TOFU" plan -input=false -no-color 2>&1)"; rc=$?
  NLS_CALLS=$((NLS_CALLS + $(api_calls "$WORK/nls.$n.log")))
  if [ "$rc" -ne 0 ]; then NLS_BAD="$NLS_BAD $n's plan exited $rc ($(gauntlet_first_error_line <<< "$plan"));"
  elif grep -qE '# .+ (will be (created|destroyed)|must be replaced)' <<< "$plan"; then
    NLS_BAD="$NLS_BAD $n's plan proposes $(grep -E '# .+ (will be (created|destroyed)|must be replaced)' <<< "$plan" | sed -E 's/^[[:space:]#]*//' | tr '\n' ';')"
  fi
done
if [ "${BREAK_NO_LOCAL_STATE:-}" = "1" ]; then
  [ -n "$NLS_BAD" ] || fail "BREAK_NO_LOCAL_STATE=1: with settings' label stripped and no local state, both plans still found everything - the check is not load-bearing"
  kca label configmap settings -n "$NS" "tofu-estate=$ESTATE" >/dev/null || fail "BREAK_NO_LOCAL_STATE: could not put settings' label back"
  gauntlet_stage no_local_state pass "BREAK_NO_LOCAL_STATE=1 control: with settings' tofu-estate label stripped and both estates' record stores and caches deleted, the check correctly failed ($NLS_BAD); the label was put back"
elif [ -n "$NLS_BAD" ]; then
  gauntlet_stage no_local_state fail "with both estates' record stores and state caches deleted:$NLS_BAD"
else
  [ "$NLS_CALLS" -gt 0 ] && [ "$NLS_BASE" -gt 0 ] || fail "the plans left no countable provider requests in their debug logs (no local state: $NLS_CALLS, cache-serving: $NLS_BASE)"
  ratio="$(awk -v a="$NLS_CALLS" -v b="$NLS_BASE" 'BEGIN { printf "%.2f", a / b }')"
  gauntlet_stage no_local_state pass "with both estates' local record stores and state caches deleted - a fresh clone with only the cluster left - neither plan created, destroyed or replaced anything: platform's objects found by their tofu-estate label, app's six field-granular instances by the fields $A_MANAGER owns on objects it does not own. plan_calls_no_local_state=$NLS_CALLS plan_calls_cache_serving=$NLS_BASE ratio=${ratio}x (both estates' plans summed, the provider's HTTP requests counted from TF_LOG=DEBUG); stock in this position has no plan at all. BREAK_NO_LOCAL_STATE=1 strips settings' label and the check correctly fails"
fi
( chdf "$GREEN_A" apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "app's greenfield teardown failed"
( chdf "$GREEN" apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "platform's greenfield teardown failed"

# ── 14. strict: every toggle on, one refusal ─────────────────────────────
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
    estate = "reference-k8s-shared-objects-strict"
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
log "=== 14. strict: every strict toggle on ==="
strict_block "refuse" > "$STRICT/main.tf"
( cd "$STRICT" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "choudoufu init for the strict-stage scratch estate failed"
STRICT_ON="$(cd "$STRICT" && "$TOFU" plan -input=false -no-color 2>&1)"; STRICT_ON_RC=$?
if [ "${BREAK_STRICT:-}" = "1" ]; then
  strict_block "store" > "$STRICT/main.tf"
  STRICT_OFF="$(cd "$STRICT" && "$TOFU" plan -input=false -no-color 2>&1)"; STRICT_OFF_RC=$?
  [ "$STRICT_OFF_RC" -eq 0 ] || { printf '%s\n' "$STRICT_OFF" | tail -20; fail "BREAK_STRICT=1: the plan with secrets = \"store\" exited $STRICT_OFF_RC"; }
  grep -q "^Error:" <<< "$STRICT_OFF" && fail "BREAK_STRICT=1: turning secrets off did not clear every refusal"
  gauntlet_stage strict pass "BREAK_STRICT=1 control: with secrets back to \"store\" the refusal is gone and the plan is an ordinary create; the real check is skipped"
else
  [ "$STRICT_ON_RC" -eq 1 ] || { printf '%s\n' "$STRICT_ON" | tail -20; fail "the every-toggle-on plan exited $STRICT_ON_RC, not the refusal's usual 1"; }
  [ "$(grep -c '^Error:' <<< "$STRICT_ON")" -eq 1 ] || { printf '%s\n' "$STRICT_ON"; fail "every strict toggle on refused more than one thing"; }
  grep -qF 'Error: Logical resource is not admitted' <<< "$STRICT_ON" || { printf '%s\n' "$STRICT_ON"; fail "the one refusal is not \"Logical resource is not admitted\""; }
  grep -qF 'strict { secrets = "refuse" }' <<< "$(tr -s ' \n' '  ' <<< "$STRICT_ON")" || { printf '%s\n' "$STRICT_ON"; fail "the refusal does not cite strict { secrets = \"refuse\" }"; }
  gauntlet_stage strict pass "every strict toggle on (secrets = refuse, no_source_create = refuse, marker_repair = never with a markers \"record\" selection naming kubernetes_config_map_v1) against a scratch estate carrying random_password.db: exactly one refusal, Logical resource is not admitted under strict { secrets = \"refuse\" }; the other two toggles are on and silent. BREAK_STRICT=1 turns secrets back to \"store\" and the refusal disappears"
fi
gauntlet_end_stage

gauntlet_end
log "reference-k8s-shared-objects: done"
