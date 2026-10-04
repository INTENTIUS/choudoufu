#!/usr/bin/env bash
# reference-k8s-platform-app: the kubernetes lane's two-estate reference
# (#1883, epic #1885). A hand-written shape kept in this repository, under
# #1107's fallback rule: no published Kubernetes-only root splits a platform
# team's objects from an application team's and has the second read the
# first's outputs, which is the shape this estate exists to cross.
#
# Two estates on one cluster, both with record_store "kubernetes":
#
#   network   the platform team's: a Namespace (platform), a NetworkPolicy, a
#             Service and two ConfigMaps, and two root outputs (the namespace
#             and the Service's name). Records in tofu-records-network.
#   app       the application team's: a Namespace (shop), a ConfigMap, a
#             ServiceAccount, a Service, a Deployment, a
#             HorizontalPodAutoscaler, a two-instance count ConfigMap, and a
#             kubernetes_manifest ConfigMap (handoff) that lives in network's
#             namespace. It declares reads_outputs_of "network" {} and reads
#             network's outputs through data "terraform_estate_outputs", in an
#             identity-bearing argument (the manifest's namespace, #1575,
#             #1862) and in two that are not. Records in tofu-records-app.
#
# Stock's equivalent of the read is data "terraform_remote_state" on
# network's local state file, which is what the stock roots use; that is the
# only line the stock and live shapes differ by beyond the live block.
#
# What this estate measures that the other four kind estates do not, and the
# stage each lands in:
#
#   records in the cluster   migrate and greenfield, both estates: no
#                            .tofu-records directory and no terraform.tfstate
#                            anywhere, the records read back as Secrets.
#   a declared read          test_plan: app's replan, under an identity that
#                            may read its own records and nothing of
#                            network's, refuses with the denial naming
#                            network; with a Role granting get on network's
#                            output Secrets by name the same plan is empty.
#   live-mv -from-estate     day2_crash: the handoff manifest moves from app
#                            to network, the move is killed between its two
#                            requests (the e2e build's
#                            TOFU_E2E_LIVE_MV_INTERRUPT, #1858's window) and
#                            re-run, and the hand-off must be finished.
#   the cache vouch          no_local_state: app's unchanged plan with
#                            -refresh=false is served from the cache (#1864)
#                            and costs fewer requests than reads = "full",
#                            counted on the wire by live/smoke/k8sproxy.py;
#                            with the cache deleted (the only local state an
#                            estate with in-cluster records has) the plan
#                            still finds every object.
#   teardown order           day2_teardown: app first, network second, and
#                            each estate's records gone with it.
#
# Two kind clusters, both created for this run and deleted after it: A holds
# both estates, B is stock's oracle and holds both again, as in
# reference-k8s.
#
#   go run ./tools/gauntlet run reference-k8s-platform-app
#   bash live/e2e/reference-k8s-platform-app/run.sh
#
# Needs kind, kubectl, terraform (the stock binary), python3, openssl and
# Docker on PATH.
#
# Env overrides:
#   TOFU_BIN       a prebuilt choudoufu binary; skips the `go build`.
#   BREAK          set to 1 for drift_reconverge's, day2_rename's and
#                  greenfield's negative controls (as reference-k8s).
#   BREAK_READ     set to 1 to grant app's identity the read BEFORE the
#                  withdrawn-grant plan; the refusal assertion must fail.
#   BREAK_REMOVE, BREAK_COUNT, BREAK_APPROVAL, BREAK_REPLACE, BREAK_CRASH,
#   BREAK_STRICT   each stage's own Break line, as in reference-k8s.
#   BREAK_MV       set to 1 to assert, straight after the killed live-mv and
#                  without its rerun, that the hand-off is finished; must fail.
#   BREAK_NO_LOCAL_STATE
#                  set to 1 to strip tofu-estate off the web Service before
#                  the plan with no cache; the plan must then propose a create.
# SC2015: every `A && B || fail` here means "fail unless both hold", and
# fail exits, so the if-then-else reading shellcheck warns about is the
# intended one.
# shellcheck disable=SC2015
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
source "$ROOT/live/e2e/lib/gauntlet.sh"

gauntlet_plugin_cache
GAUNTLET_ESTATE="reference-k8s-platform-app"
NET="network"; APP="app"                      # the two tofu estates
NS_NET="platform"; NS_APP="shop"              # their workload namespaces
REC_NET="tofu-records-$NET"; REC_APP="tofu-records-$APP"
# ESTATE is the name live/e2e/lib/gauntlet.sh's kind helpers label with and
# count by. Every day-2 stage runs on app.
ESTATE="$APP"
KINDS_NET="namespaces networkpolicies services configmaps"
# secrets is here for day2_crash's crash pair (a kubernetes_secret, as in
# reference-k8s). Record Secrets carry tofu-estate too, so count_a leaves
# out every tofu-records-* namespace.
KINDS_APP="namespaces configmaps serviceaccounts services deployments horizontalpodautoscalers secrets"
WORK="$(mktemp -d)"
STOCK_NET="$WORK/stock-net"; STOCK_APP="$WORK/stock-app"
ORACLE_NET="$WORK/oracle-net"; ORACLE_APP="$WORK/oracle-app"
NET_LIVE="$WORK/net"; APP_LIVE="$WORK/app"
GREEN_NET="$WORK/green-net"; GREEN_APP="$WORK/green-app"
KCA="$WORK/a.kubeconfig"; KCB="$WORK/b.kubeconfig"; KC_PLANNER="$WORK/app-planner.kubeconfig"
CLUSTER_A="${GAUNTLET_KIND_PREFIX:-chdf}-k8spa-a-$$"; CLUSTER_B="${GAUNTLET_KIND_PREFIX:-chdf}-k8spa-b-$$"
PROXY_PID=""
export TF_IN_AUTOMATION=1
log() { printf '%s\n' "$*"; }

CURRENT_STAGE=""
fail() {
  printf 'FAIL: %s\n' "$*" >&2
  if [ -n "$CURRENT_STAGE" ]; then gauntlet_stage "$CURRENT_STAGE" fail "$*"; fi
  exit 1
}
cleanup() {
  if [ -n "$PROXY_PID" ]; then kill "$PROXY_PID" 2>/dev/null || true; fi
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
command -v openssl >/dev/null 2>&1 || fail "openssl is not on PATH - no_local_state's request counter needs a certificate"

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
# day2_crash's two engine-delivered kills need the e2eTestingFeatures build:
# TOFU_E2E_APPLY_RESOURCE_INTERRUPT for the applies (as reference-k8s), and
# TOFU_E2E_LIVE_MV_INTERRUPT for the move (internal/command/
# live_mv_e2etesting.go, #1883). Built from this tree whatever $TOFU is.
mkdir -p "$WORK/bin"
TOFU_CRASH="$WORK/bin/choudoufu-e2e"
( cd "$ROOT" && env -u PWD go build -ldflags="-X 'main.e2eTestingFeatures=yes'" -o "$TOFU_CRASH" ./cmd/choudoufu ) \
  || fail "go build -ldflags e2eTestingFeatures ./cmd/choudoufu failed"
log "  built $TOFU_CRASH (e2eTestingFeatures=yes)"

# ── the shape ────────────────────────────────────────────────────────────
K8S_REQUIRED_PROVIDER="$(gauntlet_kubernetes_required_provider)" \
  || fail "could not read the hashicorp/kubernetes pin from live/oracle-versions.json"

# versions_tf <dir> <stock|live> <estate>: the provider and, for live, the
# live block with the records in the cluster. The waiver is kind's, as in
# live/smoke/scenarios/k8s-records-in-the-cluster.sh: every run here is the
# cluster admin's except test_plan's scoped plan, kind's API server has no
# encryption at rest, and the estate boundary policy is not installed.
versions_tf() {
  local dir="$1" mode="$2" estate="$3" reads=""
  [ "$estate" = "$APP" ] && reads='      reads_outputs_of "network" {}'
  {
    cat <<EOF
terraform {
  required_providers {
$K8S_REQUIRED_PROVIDER
  }
EOF
    if [ "$mode" = "live" ]; then cat <<EOF
  live {
    estate = "$estate"
    record_store "kubernetes" {
      allow_insecure = ["read_isolation", "encryption_at_rest", "estate_boundary"]
$reads
    }
  }
EOF
    fi
    cat <<'EOF'
}

provider "kubernetes" {}
EOF
  } > "$dir/versions.tf"
}

# handoff_block <namespace-expression>: the kubernetes_manifest ConfigMap
# day2_crash moves from app to network. The same manifest in either root.
handoff_block() {
  cat <<EOF
resource "kubernetes_manifest" "handoff" {
  manifest = {
    apiVersion = "v1"
    kind       = "ConfigMap"
    metadata = {
      name      = "handoff"
      namespace = $1
    }
    data = {
      owner = "platform"
    }
  }
}
EOF
}

# write_net <dir> [with-handoff]: network's resources and outputs.
write_net() {
  local dir="$1" handoff="${2:-0}"
  {
    cat <<EOF
resource "kubernetes_namespace" "platform" {
  metadata {
    name = "$NS_NET"
  }
}

resource "kubernetes_network_policy" "default_deny" {
  metadata {
    name      = "default-deny-ingress"
    namespace = kubernetes_namespace.platform.metadata[0].name
  }
  spec {
    pod_selector {}
    policy_types = ["Ingress"]
  }
}

resource "kubernetes_service" "gateway" {
  metadata {
    name      = "gateway"
    namespace = kubernetes_namespace.platform.metadata[0].name
  }
  spec {
    selector = {
      app = "gateway"
    }
    port {
      port        = 80
      target_port = 8080
    }
  }
}

resource "kubernetes_config_map" "settings" {
  metadata {
    name      = "platform-settings"
    namespace = kubernetes_namespace.platform.metadata[0].name
  }
  data = {
    cluster_domain = "cluster.local"
    tier           = "platform"
  }
}

resource "kubernetes_config_map" "routes" {
  metadata {
    name      = "routes"
    namespace = kubernetes_namespace.platform.metadata[0].name
  }
  data = {
    "shop" = "web.$NS_APP.svc"
  }
}

output "namespace" {
  value = kubernetes_namespace.platform.metadata[0].name
}

output "gateway_service" {
  value = kubernetes_service.gateway.metadata[0].name
}
EOF
    if [ "$handoff" = "1" ]; then echo; handoff_block "kubernetes_namespace.platform.metadata[0].name"; fi
  } > "$dir/main.tf"
}

# write_app <dir> <stock|live> [key=value...]: app's resources. The keys:
#   sa=<block>     the ServiceAccount's block name (app; team after the
#                  rename; none to leave the block out)
#   moved=1        the moved block app -> team
#   shards=<n>     the count ConfigMap's count
#   reviewed=1     app-config gains reviewed = "yes" (plan_approval)
#   handoff=0      leave the kubernetes_manifest out (after the move)
write_app() {
  local dir="$1" mode="$2" sa=app moved=0 shards=2 reviewed=0 handoff=1 kv out source extra=""
  shift 2
  for kv in "$@"; do
    case "$kv" in
      sa=*) sa="${kv#sa=}" ;; moved=*) moved="${kv#moved=}" ;; shards=*) shards="${kv#shards=}" ;;
      reviewed=*) reviewed="${kv#reviewed=}" ;; handoff=*) handoff="${kv#handoff=}" ;;
      *) fail "write_app: unknown setting $kv" ;;
    esac
  done
  if [ "$mode" = "live" ]; then
    out="data.terraform_estate_outputs.network.values"
    source='data "terraform_estate_outputs" "network" {
  estate = "network"
  names  = ["namespace", "gateway_service"]
}'
  else
    out="data.terraform_remote_state.network.outputs"
    # Stock's root on cluster A reads stock's network state on A, and the
    # oracle's on B reads the oracle's.
    local net_state="$STOCK_NET/terraform.tfstate"
    [ "$dir" = "$ORACLE_APP" ] && net_state="$ORACLE_NET/terraform.tfstate"
    source="data \"terraform_remote_state\" \"network\" {
  backend = \"local\"
  config = {
    path = \"$net_state\"
  }
}"
  fi
  [ "$reviewed" = "1" ] && extra='    reviewed = "yes"'
  {
    cat <<EOF
$source

locals {
  gateway_host = "\${$out.gateway_service}.\${$out.namespace}.svc.cluster.local"
}

resource "kubernetes_namespace" "app" {
  metadata {
    name = "$NS_APP"
  }
}

resource "kubernetes_config_map" "app" {
  metadata {
    name      = "app-config"
    namespace = kubernetes_namespace.app.metadata[0].name
  }
  data = {
    gateway = local.gateway_host
$extra
  }
}

resource "kubernetes_service" "web" {
  metadata {
    name      = "web"
    namespace = kubernetes_namespace.app.metadata[0].name
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
}

resource "kubernetes_deployment" "web" {
  metadata {
    name      = "web"
    namespace = kubernetes_namespace.app.metadata[0].name
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
          env {
            name  = "GATEWAY_HOST"
            value = local.gateway_host
          }
        }
      }
    }
  }
  wait_for_rollout = false
}

resource "kubernetes_horizontal_pod_autoscaler_v2" "web" {
  metadata {
    name      = "web"
    namespace = kubernetes_namespace.app.metadata[0].name
  }
  spec {
    min_replicas = 1
    max_replicas = 3
    scale_target_ref {
      api_version = "apps/v1"
      kind        = "Deployment"
      name        = kubernetes_deployment.web.metadata[0].name
    }
    metric {
      type = "Resource"
      resource {
        name = "cpu"
        target {
          type                = "Utilization"
          average_utilization = 80
        }
      }
    }
  }
}

resource "kubernetes_config_map" "shard" {
  count = $shards
  metadata {
    name      = "shard-\${count.index}"
    namespace = kubernetes_namespace.app.metadata[0].name
  }
  data = { shard = tostring(count.index) }
}
EOF
    if [ "$sa" != "none" ]; then cat <<EOF

resource "kubernetes_service_account" "$sa" {
  metadata {
    name      = "app"
    namespace = kubernetes_namespace.app.metadata[0].name
  }
}
EOF
    fi
    if [ "$moved" = "1" ]; then cat <<'EOF'

moved {
  from = kubernetes_service_account.app
  to   = kubernetes_service_account.team
}
EOF
    fi
    if [ "$handoff" = "1" ]; then echo; handoff_block "$out.namespace"; fi
  } > "$dir/main.tf"
}

# day2_crash's multi-object pair, as reference-k8s: crash_second reads
# crash_first's name, so the walker cannot reach it before crash_first's
# create commits, and crash_first is a Secret so its record carries residue.
crash_pair_tf() {
  cat > "$1/crash.tf" <<EOF
resource "kubernetes_secret" "crash_first" {
  metadata {
    name      = "crash-first"
    namespace = kubernetes_namespace.app.metadata[0].name
  }
  data = {
    step = "one"
  }
}
EOF
  if [ "${2:-}" = "both" ]; then cat >> "$1/crash.tf" <<'EOF'

resource "kubernetes_config_map" "crash_second" {
  metadata {
    name      = "crash-second"
    namespace = kubernetes_namespace.app.metadata[0].name
  }
  data = {
    after = kubernetes_secret.crash_first.metadata[0].name
  }
}
EOF
  fi
}

# ── cluster helpers ──────────────────────────────────────────────────────
# Every kubectl against a cluster carries a bound, so a cluster that stops
# answering fails a stage instead of hanging it.
kca() { kubectl --kubeconfig "$KCA" --request-timeout=60s "$@"; }
kcb() { kubectl --kubeconfig "$KCB" --request-timeout=60s "$@"; }
kcp() { kubectl --kubeconfig "$KC_PLANNER" --request-timeout=60s "$@"; }
# chdf <dir> <args...>: choudoufu in a root against cluster A, as its admin.
chdf() { local d="$1"; shift; ( cd "$d" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" "$TOFU" "$@" ); }
# stock_b and stock_bn: the stock binary in the oracle roots on cluster B.
# stock_b is app's; live/e2e/lib/gauntlet.sh's kind helpers call it by name.
stock_b() { ( cd "$ORACLE_APP" && KUBECONFIG="$KCB" KUBE_CONFIG_PATH="$KCB" terraform "$@" ); }
stock_bn() { ( cd "$ORACLE_NET" && KUBECONFIG="$KCB" KUBE_CONFIG_PATH="$KCB" terraform "$@" ); }
stock_a() { local d="$1"; shift; ( cd "$d" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" terraform "$@" ); }
in_dir() { local d="$1"; shift; ( cd "$d" && "$@" ); }

# ran <cmd...>: runs a stock or choudoufu call with its output captured in
# RAN_OUT and its exit status in RAN_RC, and returns that status. Every call
# whose output a FAIL path used to drop goes through here, so `shown` can
# print it before the FAIL. Never `terraform ... | grep -q`: grep exits at
# its match, terraform takes SIGPIPE writing whatever follows it (network's
# Outputs block follows "Apply complete!"), and pipefail turns a good apply
# into a FAIL with nothing in the log - cold_deploy's first measured failure.
ran() { RAN_OUT="$("$@" 2>&1)"; RAN_RC=$?; return "$RAN_RC"; }
# ran_has <text> <cmd...>: ran, and the command exited 0 with <text> in its output.
ran_has() { local want="$1"; shift; ran "$@" && grep -qF -- "$want" <<< "$RAN_OUT"; }
# shown: the last ran's exit status and the tail of its output.
shown() { printf -- '--- exit %s; last 40 lines of output ---\n%s\n---\n' "$RAN_RC" "$(tail -40 <<< "$RAN_OUT")"; }
RAN_OUT=""; RAN_RC=0

# count_a <estate> <kinds...>: objects of those kinds carrying
# tofu-estate=<estate> on cluster A, every namespace but the records ones.
count_a() {
  local estate="$1" n=0 k c; shift
  for k in "$@"; do
    c="$(kca get "$k" -A -l "tofu-estate=$estate" -o jsonpath='{range .items[*]}{.metadata.namespace}{"\n"}{end}' 2>/dev/null | grep -vc '^tofu-records-' || true)"
    n=$((n + ${c:-0}))
  done
  printf '%s\n' "$n"
}
# shellcheck disable=SC2086  # the kind lists split on purpose
count_net() { count_a "$NET" $KINDS_NET; }
# shellcheck disable=SC2086
count_app() { count_a "$APP" $KINDS_APP; }

# record_secrets <records-namespace> <estate> <record-namespace-label>: how
# many Secrets the record store holds for the estate under one of its key
# namespaces (tofu-records, tofu-outputs, tofu-hints).
record_secrets() {
  kca get secrets -n "$1" -l "tofu-estate=$2,choudoufu.intentius.io/record-namespace=$3" -o name 2>/dev/null | grep -c . || true
}

# mirror_records <records-namespace> <dir>: rewrites <dir> as a local-store
# image of the estate's record Secrets - each payload gunzipped and written
# at its own record key - so live/e2e/lib/gauntlet.sh's record readers,
# which walk a directory, read the in-cluster store unchanged. <dir>.index
# maps each written file to its Secret's name.
mirror_records() {
  local ns="$1" dir="$2"
  rm -rf "$dir" "$dir.index"; mkdir -p "$dir"
  kca get secrets -n "$ns" -o json > "$dir.json" 2>/dev/null || { rm -f "$dir.json"; return 1; }
  python3 - "$dir.json" "$dir" <<'PY'
import base64, gzip, json, os, sys
src, root = sys.argv[1], sys.argv[2]
items = json.load(open(src)).get("items", [])
index = []
for s in items:
    meta = s.get("metadata") or {}
    ann = meta.get("annotations") or {}
    key = ann.get("choudoufu.intentius.io/record-key")
    if not key or not meta.get("name", "").startswith("tofu-record-"):
        continue
    raw = base64.b64decode((s.get("data") or {}).get("tfstate", ""))
    if ann.get("encoding") == "gzip":
        raw = gzip.decompress(raw)
    segs = [p for p in key.split("/") if p not in ("", ".", "..")]
    if not segs:
        continue
    path = os.path.join(root, *segs)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "wb") as fh:
        fh.write(raw)
    index.append(path + "\t" + meta["name"])
with open(root + ".index", "w") as fh:
    fh.write("\n".join(index) + ("\n" if index else ""))
PY
  local rc=$?
  rm -f "$dir.json"
  return $rc
}
refresh_app_records() { mirror_records "$REC_APP" "$1"; }

# inventory <kubeconfig>: both estates' objects, normalised to what the
# configuration declares, labels and annotations never compared.
inventory() {
  KUBECONFIG="$1" NS_NET="$NS_NET" NS_APP="$NS_APP" DROP="${2:-}" python3 - <<'PY'
import json, os, subprocess
nsn, nsa, drop = os.environ["NS_NET"], os.environ["NS_APP"], os.environ.get("DROP", "")
def get(kind, name, ns=None):
    cmd = ["kubectl", "--request-timeout=60s", "get", kind, name, "-o", "json"]
    if ns:
        cmd += ["-n", ns]
    out = subprocess.run(cmd, capture_output=True, text=True)
    return json.loads(out.stdout) if out.returncode == 0 else None
inv = {}
for ns in (nsn, nsa):
    inv["namespace/" + ns] = get("namespace", ns) is not None
o = get("networkpolicy", "default-deny-ingress", nsn)
inv["networkpolicy/default-deny-ingress"] = None if o is None else {"podSelector": o["spec"].get("podSelector"), "policyTypes": o["spec"].get("policyTypes")}
for ns, name in ((nsn, "gateway"), (nsa, "web")):
    o = get("service", name, ns)
    inv["service/%s/%s" % (ns, name)] = None if o is None else {
        "type": o["spec"].get("type"), "selector": o["spec"].get("selector"),
        "ports": [{"port": p.get("port"), "targetPort": p.get("targetPort"), "protocol": p.get("protocol")} for p in o["spec"].get("ports", [])]}
for ns, name in ((nsn, "platform-settings"), (nsn, "routes"), (nsn, "handoff"), (nsa, "app-config"), (nsa, "shard-0"), (nsa, "shard-1")):
    o = get("configmap", name, ns)
    inv["configmap/%s/%s" % (ns, name)] = None if o is None else o.get("data", {})
inv["serviceaccount/%s/app" % nsa] = get("serviceaccount", "app", nsa) is not None
o = get("deployment", "web", nsa)
inv["deployment/web"] = None if o is None else {
    "replicas": o["spec"].get("replicas"), "selector": o["spec"].get("selector", {}).get("matchLabels"),
    "containers": [{"name": c["name"], "image": c["image"], "env": c.get("env")} for c in o["spec"]["template"]["spec"]["containers"]]}
o = get("horizontalpodautoscaler.v2.autoscaling", "web", nsa)
inv["hpa/web"] = None if o is None else {
    "min": o["spec"].get("minReplicas"), "max": o["spec"].get("maxReplicas"),
    "target": o["spec"].get("scaleTargetRef"), "metrics": o["spec"].get("metrics")}
if drop:
    inv.pop(drop, None)
print(json.dumps(inv, sort_keys=True, indent=1))
PY
}
exists_a() { kca get "$1" "$2" -n "${3:-$NS_APP}" >/dev/null 2>&1; }
plan_line() { grep -E '^Plan:|^No changes' | head -1 | sed 's/\.$//'; }
# flat undoes the CLI's word wrap before a sentence is matched.
flat() { tr '\n' ' ' | sed 's/│/ /g' | tr -s ' '; }

# markers_held_by_update <namespace> <name>: prints "held" when any Update
# entry of the ConfigMap's managedFields owns the tofu-estate label or the
# address annotation - what a move killed between its two requests leaves
# (#1858) - and "free" otherwise.
markers_held_by_update() {
  kca get configmap "$2" -n "$1" -o json --show-managed-fields > "$WORK/mf.json" 2>/dev/null || { echo "unreadable"; return 1; }
  python3 - "$WORK/mf.json" <<'PY'
import json, sys
o = json.load(open(sys.argv[1]))
held = False
for e in (o.get("metadata") or {}).get("managedFields") or []:
    if e.get("operation") != "Update":
        continue
    meta = ((e.get("fieldsV1") or {}).get("f:metadata") or {})
    if "f:tofu-estate" in (meta.get("f:labels") or {}) or "f:choudoufu.intentius.io/tofu-address" in (meta.get("f:annotations") or {}):
        held = True
print("held" if held else "free")
PY
}

# ── 1. cold_deploy: stock stands both estates up on A, and on B ──────────
gauntlet_begin_stage cold_deploy
log "=== 1. cold_deploy: two kind clusters, stock terraform applies network then app on each ==="
gauntlet_kind_up "$CLUSTER_A" "$KCA" || fail "kind cluster A ($CLUSTER_A) did not come up"
gauntlet_kind_up "$CLUSTER_B" "$KCB" || fail "kind cluster B ($CLUSTER_B) did not come up"
export KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA"
mkdir -p "$STOCK_NET" "$STOCK_APP" "$ORACLE_NET" "$ORACLE_APP"
for d in "$STOCK_NET" "$ORACLE_NET"; do versions_tf "$d" stock "$NET"; write_net "$d"; done
for d in "$STOCK_APP" "$ORACLE_APP"; do versions_tf "$d" stock "$APP"; write_app "$d" stock; done
ran in_dir "$STOCK_NET" gauntlet_locked_init terraform init -input=false -no-color || { shown; fail "stock init failed in network's root on A"; }
ran in_dir "$STOCK_APP" gauntlet_locked_init terraform init -input=false -no-color || { shown; fail "stock init failed in app's root on A"; }
ran in_dir "$ORACLE_NET" gauntlet_locked_init terraform init -input=false -no-color || { shown; fail "stock init failed in network's root on B"; }
ran in_dir "$ORACLE_APP" gauntlet_locked_init terraform init -input=false -no-color || { shown; fail "stock init failed in app's root on B"; }
C_NET="$(stock_a "$STOCK_NET" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$C_NET" | tail -20; fail "stock's cold deploy of network failed on A"; }
grep -qF "Apply complete! Resources: 5 added, 0 changed, 0 destroyed" <<< "$C_NET" || { printf '%s\n' "$C_NET" | tail -5; fail "stock's cold deploy of network did not add exactly 5 objects on A"; }
C_APP="$(stock_a "$STOCK_APP" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$C_APP" | tail -20; fail "stock's cold deploy of app failed on A"; }
grep -qF "Apply complete! Resources: 9 added, 0 changed, 0 destroyed" <<< "$C_APP" || { printf '%s\n' "$C_APP" | tail -5; fail "stock's cold deploy of app did not add exactly 9 objects on A"; }
[ -f "$STOCK_NET/terraform.tfstate" ] && [ -f "$STOCK_APP/terraform.tfstate" ] || fail "stock left no terraform.tfstate in one of the two roots on A"
[ "$(count_net)" = "0" ] && [ "$(count_app)" = "0" ] || fail "objects already carry tofu-estate after a plain stock apply (network $(count_net), app $(count_app)) - this proves nothing"
inventory "$KCA" > "$WORK/inventory.stock.json" || fail "could not read the cold-deployed inventory on A"
ran_has "Apply complete! Resources: 5 added" stock_bn apply -auto-approve -input=false -no-color || { shown; fail "stock's cold deploy of network failed on B"; }
ran_has "Apply complete! Resources: 9 added" stock_b apply -auto-approve -input=false -no-color || { shown; fail "stock's cold deploy of app failed on B"; }
gauntlet_stage cold_deploy pass "plain terraform against kind $(kca version 2>/dev/null | gauntlet_k8s_server_version): network's 5 objects (Namespace, NetworkPolicy, Service, 2 ConfigMaps) and two root outputs, then app's 9 (Namespace, 3 ConfigMaps, Service, Deployment, HorizontalPodAutoscaler v2, ServiceAccount, and a kubernetes_manifest ConfigMap in network's namespace) reading network's outputs through data terraform_remote_state; two terraform.tfstate files, zero tofu-estate labels read back with kubectl; the same two estates cold-deployed by stock on a second cluster as every later stage's oracle"

# ── 2. migrate: live-import both estates, records in the cluster ─────────
gauntlet_begin_stage migrate
log "=== 2. migrate: live-import network, record its outputs, then live-import app ==="
for ns in "$REC_NET" "$REC_APP"; do kca create namespace "$ns" >/dev/null || fail "could not create the records namespace $ns"; done
mkdir -p "$NET_LIVE" "$APP_LIVE"
versions_tf "$NET_LIVE" live "$NET"; write_net "$NET_LIVE"
versions_tf "$APP_LIVE" live "$APP"; write_app "$APP_LIVE" live
ran chdf "$NET_LIVE" init -input=false -no-color || { shown; fail "choudoufu init failed in network's live root"; }
ran chdf "$APP_LIVE" init -input=false -no-color || { shown; fail "choudoufu init failed in app's live root"; }
M_NET="$(chdf "$NET_LIVE" live-import -state="$STOCK_NET/terraform.tfstate" -estate="$NET" -approve -no-color 2>&1)" || { printf '%s\n' "$M_NET" | tail -20; fail "network's live-import -approve failed"; }
M_NET_LINE="$(grep -E 'resource\(s\) newly stamped' <<< "$M_NET" | head -1 | sed 's/\.$//')"
# app reads network's outputs at plan time, and an output is recorded by an
# apply, so network applies once before app is imported.
N_ADOPT="$(chdf "$NET_LIVE" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$N_ADOPT" | tail -20; fail "network's first choudoufu apply, which records its outputs, failed"; }
N_ADOPT_LINE="$(grep -E '^Apply complete' <<< "$N_ADOPT" | head -1)"
N_OUTS="$(record_secrets "$REC_NET" "$NET" tofu-outputs)"
[ "$N_OUTS" = "2" ] || fail "network recorded $N_OUTS output Secret(s) in $REC_NET after its apply, want 2 (namespace, gateway_service)"
M_APP="$(chdf "$APP_LIVE" live-import -state="$STOCK_APP/terraform.tfstate" -estate="$APP" -approve -no-color 2>&1)" || { printf '%s\n' "$M_APP" | tail -20; fail "app's live-import -approve failed"; }
M_APP_LINE="$(grep -E 'resource\(s\) newly stamped' <<< "$M_APP" | head -1 | sed 's/\.$//')"
log "  network: ${M_NET_LINE:-no summary line}; then ${N_ADOPT_LINE:-no apply line}"
log "  app: ${M_APP_LINE:-no summary line}"
L_NET="$(count_net)"; L_APP="$(count_app)"
LOCAL_LEFT="$(find "$NET_LIVE" "$APP_LIVE" -name '.tofu-records' -o -name 'terraform.tfstate' | head -3)"
if grep -qE ' 0 failed, 0 skipped' <<< "$M_NET_LINE" && grep -qE ' 0 failed, 0 skipped' <<< "$M_APP_LINE" \
   && [ "$L_NET" = "5" ] && [ "$L_APP" = "9" ] && [ -z "$LOCAL_LEFT" ]; then

  gauntlet_stage migrate pass "network: $M_NET_LINE; its first apply recorded both root outputs as Secrets in $REC_NET (${N_ADOPT_LINE:-no apply line}). app: $M_APP_LINE, its live-import reading network's outputs through data terraform_estate_outputs (#1862). Every object carries its estate's label, read back with kubectl (network 5 of 5, app 9 of 9, the handoff manifest in network's namespace carrying tofu-estate=$APP); records in the cluster, record_store \"kubernetes\": no .tofu-records directory and no terraform.tfstate in either live root"
else
  gauntlet_stage migrate fail "network: ${M_NET_LINE:-no summary line}; app: ${M_APP_LINE:-no summary line}; labelled after import: network $L_NET of 5, app $L_APP of 9; local record or state files: ${LOCAL_LEFT:-none}. First Error: line: $(gauntlet_first_error_line <<< "$M_NET$M_APP")"
fi

# ── 3. test_plan: replan from nothing; app's read of network, declared ───
gauntlet_begin_stage test_plan
log "=== 3. test_plan: both plans with no state file; app's under an identity that may read only its own records ==="
P_NET="$(chdf "$NET_LIVE" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$P_NET" | tail -20; fail "network's post-migration plan failed"; }
P_ADMIN="$(chdf "$APP_LIVE" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$P_ADMIN" | tail -20; fail "app's post-migration plan as the cluster admin failed"; }
if ! grep -q "No changes." <<< "$P_NET" || ! grep -q "No changes." <<< "$P_ADMIN"; then
  gauntlet_stage test_plan fail "the plan with no state file is not empty: network $(plan_line <<< "$P_NET"), app $(plan_line <<< "$P_ADMIN")"
  log "  adopting through choudoufu's own applies so the stages below run on labelled estates"
  ran chdf "$NET_LIVE" apply -auto-approve -input=false -no-color || { shown; fail "network's adopting apply failed"; }
  ran chdf "$APP_LIVE" apply -auto-approve -input=false -no-color || { shown; fail "app's adopting apply failed"; }
  ran_has "No changes." chdf "$APP_LIVE" plan -input=false -no-color || { shown; fail "app's replan after the adopting apply is not empty; nothing below would measure day-2 behaviour"; }
else
  # app's planning identity is an estate principal's ordinary RBAC, as
  # live/kubernetes/estate-grant.yaml's comment names it, and nothing of
  # network's records:
  #   - the estate fence itself, that file with ESTATE=app bound to app-planner;
  #   - get, list and watch on every listable kind but Secrets, cluster-wide.
  #     That is the sweep's read, and it includes customresourcedefinitions,
  #     which the provider lists to resolve kubernetes_manifest's GVK. Without
  #     it the plan failed on that lookup, not on the read under test;
  #   - every verb on the kinds app declares, in shop, the handoff ConfigMap's
  #     kind in platform, and the shop Namespace by name;
  #   - every verb on its own records in tofu-records-app.
  # Secrets are left out of the cluster-wide read on purpose: a list on
  # Secrets in tofu-records-network would read network's outputs, and the
  # refusal below is about exactly that read. So the sweep still warns that
  # cluster-wide Secrets were denied, and that warning is expected.
  kca create serviceaccount app-planner -n default >/dev/null || fail "could not create the app-planner ServiceAccount"
  FENCE="$(sed -e 's/PRINCIPAL_NAMESPACE/default/g' -e 's/PRINCIPAL/app-planner/g' -e "s/ESTATE/$APP/g" "$ROOT/live/kubernetes/estate-grant.yaml")"
  FENCE_OUT="$(kca apply -f - <<< "$FENCE" 2>&1)" || { printf '%s\n' "$FENCE_OUT"; fail "could not apply live/kubernetes/estate-grant.yaml for $APP and app-planner"; }
  READ_KINDS="$(kca api-resources --verbs=list -o name 2>&1)" || { printf '%s\n' "$READ_KINDS"; fail "could not list the cluster's listable kinds"; }
  READ_KINDS="$(grep -vx 'secrets' <<< "$READ_KINDS" | paste -sd, -)"
  grep -q 'customresourcedefinitions' <<< "$READ_KINDS" || fail "the listable kinds do not include customresourcedefinitions: $READ_KINDS"
  kca create clusterrole app-planner-read --verb=get,list,watch --resource="$READ_KINDS" >/dev/null || fail "could not create app-planner's read ClusterRole"
  kca create clusterrolebinding app-planner-read --clusterrole=app-planner-read --serviceaccount=default:app-planner >/dev/null || fail "could not bind app-planner's read ClusterRole"
  W_VERBS="get,list,watch,create,update,patch,delete"
  kca create role app-kinds -n "$NS_APP" --verb="$W_VERBS" \
    --resource=configmaps,serviceaccounts,services,secrets,deployments.apps,horizontalpodautoscalers.autoscaling >/dev/null \
    || fail "could not create app's kinds Role in $NS_APP"
  kca create rolebinding app-kinds -n "$NS_APP" --role=app-kinds --serviceaccount=default:app-planner >/dev/null || fail "could not bind app's kinds Role in $NS_APP"
  kca create role app-handoff -n "$NS_NET" --verb="$W_VERBS" --resource=configmaps >/dev/null || fail "could not create app's handoff Role in $NS_NET"
  kca create rolebinding app-handoff -n "$NS_NET" --role=app-handoff --serviceaccount=default:app-planner >/dev/null || fail "could not bind app's handoff Role in $NS_NET"
  kca create clusterrole app-namespace --verb=get,update,patch,delete --resource=namespaces --resource-name="$NS_APP" >/dev/null || fail "could not create app's Namespace ClusterRole"
  kca create clusterrolebinding app-namespace --clusterrole=app-namespace --serviceaccount=default:app-planner >/dev/null || fail "could not bind app's Namespace ClusterRole"
  kca create role app-records -n "$REC_APP" --verb=get,list,create,update,delete --resource=secrets >/dev/null || fail "could not create app's records Role"
  kca create rolebinding app-records -n "$REC_APP" --role=app-records --serviceaccount=default:app-planner >/dev/null || fail "could not bind app's records Role"
  TOK="$(kca create token app-planner -n default --duration=2h)" || fail "could not mint a token for app-planner"
  cp "$KCA" "$KC_PLANNER"
  kubectl --kubeconfig "$KC_PLANNER" config set-credentials app-planner --token="$TOK" >/dev/null || fail "could not write app-planner's credentials"
  kubectl --kubeconfig "$KC_PLANNER" config set-context --current --user=app-planner >/dev/null || fail "could not switch the planner kubeconfig to app-planner"
  OUT_SECRETS="$(kca get secrets -n "$REC_NET" -l "tofu-estate=$NET,choudoufu.intentius.io/record-namespace=tofu-outputs" -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}')" \
    || fail "could not list network's output Secrets"
  grant_read() {
    local args=() n
    while read -r n; do [ -n "$n" ] && args+=("--resource-name=$n"); done <<< "$OUT_SECRETS"
    kca create role "tofu-reads-outputs-of-$NET" -n "$REC_NET" --verb=get --resource=secrets "${args[@]}" >/dev/null || fail "could not create the read Role in $REC_NET"
    kca create rolebinding "tofu-reads-outputs-of-$NET-$APP" -n "$REC_NET" --role="tofu-reads-outputs-of-$NET" --serviceaccount=default:app-planner >/dev/null || fail "could not bind the read Role"
  }
  as_planner() { local d="$1"; shift; ( cd "$d" && KUBECONFIG="$KC_PLANNER" KUBE_CONFIG_PATH="$KC_PLANNER" "$TOFU" "$@" ); }
  kcp auth can-i list secrets -n "$REC_APP" >/dev/null 2>&1 || fail "app-planner cannot list its own records; the refusal below would not be about network"
  kcp auth can-i list customresourcedefinitions.apiextensions.k8s.io >/dev/null 2>&1 || fail "app-planner cannot list CRDs; the plan would fail on kubernetes_manifest's GVK lookup, not on the read"
  for v in get list; do
    CAN="$(kcp auth can-i "$v" secrets -n "$REC_NET" 2>&1 || true)"
    [ "$CAN" = "no" ] || fail "app-planner may $v secrets in $REC_NET before any grant ($CAN); the refusal below would not be measured"
  done
  [ "${BREAK_READ:-}" = "1" ] && grant_read
  NET_BEFORE="$(kca get secrets -n "$REC_NET" -o jsonpath='{range .items[*]}{.metadata.name}={.metadata.resourceVersion}{"\n"}{end}' | sort)"
  D_OUT="$(as_planner "$APP_LIVE" plan -input=false -no-color 2>&1)"; D_RC=$?
  D_FLAT="$(flat <<< "$D_OUT")"
  # denied: the plan refused with the refusal's own summary, and every
  # error it printed is that refusal. Any other error (the GVK lookup that
  # failed when app-planner could not list CRDs, the projection import that
  # followed it) means the plan stopped on app-planner's ordinary RBAC, not
  # on the read under test, and the refusal half must not pass on it.
  denied() {
    local errs other
    [ "$D_RC" -ne 0 ] || return 1
    errs="$(grep '^Error: ' <<< "$D_OUT")"
    grep -q "^Error: This estate may not read another estate's outputs" <<< "$errs" || return 1
    other="$(grep -v "^Error: This estate may not read another estate's outputs" <<< "$errs")"
    [ -z "$other" ] || return 1
    grep -q "Failed to determine resource type from GVK" <<< "$D_OUT" && return 1
    grep -q "This estate may not read another estate's outputs" <<< "$D_FLAT" || return 1
    grep -q "estate \"$NET\"" <<< "$D_FLAT" || return 1
    grep -q "$REC_NET" <<< "$D_FLAT" || return 1
  }
  if [ "${BREAK_READ:-}" = "1" ]; then
    denied && fail "BREAK_READ=1: with the read granted the plan still refused - the refusal is not the grant's doing"
    gauntlet_stage test_plan pass "BREAK_READ=1 control: with app-planner granted get on network's output Secrets before the plan, the withdrawn-grant refusal correctly fails to appear (exit $D_RC, $(plan_line <<< "$D_OUT")); the real check is skipped"
  else
    denied || { printf '%s\n' "$D_OUT" | tail -40; fail "app's plan under an identity with no grant on network's outputs did not refuse with \"This estate may not read another estate's outputs\" naming estate $NET and $REC_NET as its only error (exit $D_RC)"; }
    grant_read
    G_OUT="$(as_planner "$APP_LIVE" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$G_OUT" | tail -40; fail "app's plan with the read granted failed"; }
    G_FLAT="$(flat <<< "$G_OUT")"
    NET_AFTER="$(kca get secrets -n "$REC_NET" -o jsonpath='{range .items[*]}{.metadata.name}={.metadata.resourceVersion}{"\n"}{end}' | sort)"
    grep -q "No changes." <<< "$G_OUT" || { printf '%s\n' "$G_OUT" | tail -20; fail "app's plan with the read granted is not empty: $(plan_line <<< "$G_OUT")"; }
    grep -q "Values from another estate are as of its last apply" <<< "$G_FLAT" || fail "app's plan does not say network's values are as of its last apply"
    [ "$NET_BEFORE" = "$NET_AFTER" ] || fail "app's two plans changed something in $REC_NET: before [$NET_BEFORE] after [$NET_AFTER]"
    ONE="$(kcp auth can-i list secrets -n "$REC_NET" 2>&1 || true)"
    [ "$ONE" = "no" ] || fail "app-planner may list secrets in $REC_NET ($ONE); the grant is wider than get by name"
    gauntlet_stage test_plan pass "both plans with no state file are empty as the cluster admin (network 5 objects, app 9). app's plan under app-planner - the estate fence for $APP, get/list/watch cluster-wide on every listable kind but Secrets, every verb on app's declared kinds and its own records in $REC_APP, nothing in $REC_NET - refused with \"This estate may not read another estate's outputs\" naming estate $NET and $REC_NET (exit $D_RC); with one Role granting get on network's $(grep -c . <<< "$OUT_SECRETS") output Secret(s) by name, and still no list there, the same plan is empty and says the values are as of network's last apply. Nothing in $REC_NET changed across either plan (names and resourceVersions). BREAK_READ=1 grants the read first and the refusal correctly does not appear"
  fi
fi
[ "$(count_net)" = "5" ] && [ "$(count_app)" = "9" ] || fail "after adoption network carries $(count_net) labels (want 5), app $(count_app) (want 9)"

# ── 4. test_apply: no-op applies, marker counts unchanged ────────────────
gauntlet_begin_stage test_apply
log "=== 4. test_apply: apply both empty plans; the labelled counts must not move ==="
B_NET="$(count_net)"; B_APP="$(count_app)"
for d in "$NET_LIVE" "$APP_LIVE"; do
  NOOP="$(chdf "$d" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$NOOP" | tail -20; fail "the no-op apply in $(basename "$d") failed"; }
  grep -qF "Apply complete! Resources: 0 added, 0 changed, 0 destroyed" <<< "$NOOP" || { printf '%s\n' "$NOOP" | tail -5; fail "the no-op apply in $(basename "$d") changed something"; }
done
[ "$B_NET" = "$(count_net)" ] && [ "$B_APP" = "$(count_app)" ] || fail "labelled counts moved across no-op applies: network $B_NET -> $(count_net), app $B_APP -> $(count_app)"
gauntlet_stage test_apply pass "no-op apply in both estates (0 added, 0 changed, 0 destroyed each); objects carrying tofu-estate unchanged at network $B_NET and app $B_APP, counted with kubectl outside the records namespaces"

# ── 5. drift_reconverge: one object tampered out of band ─────────────────
gauntlet_begin_stage drift_reconverge
log "=== 5. drift_reconverge: kubectl patch app-config on A and B; stock's plan on B is the oracle ==="
kca patch configmap app-config -n "$NS_APP" --type merge -p '{"data":{"gateway":"tampered"}}' >/dev/null || fail "could not tamper app-config on A"
kcb patch configmap app-config -n "$NS_APP" --type merge -p '{"data":{"gateway":"tampered"}}' >/dev/null || fail "could not tamper app-config on B"
[ "${BREAK:-}" = "1" ] && { kca patch configmap shard-0 -n "$NS_APP" --type merge -p '{"data":{"shard":"tampered"}}' >/dev/null || fail "BREAK: could not tamper shard-0"; }
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's plan on B after the tamper failed"; }
grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's plan on B does not propose exactly one change"; }
ran stock_b apply -auto-approve -input=false -no-color || { shown; fail "stock could not reconverge B"; }
DR_PLAN="$(chdf "$APP_LIVE" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$DR_PLAN" | tail -20; fail "the plan after the tamper failed"; }
if [ "${BREAK:-}" = "1" ]; then
  grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$DR_PLAN" && fail "BREAK=1: two objects were tampered and the plan still proposes exactly one change"
  ran chdf "$APP_LIVE" apply -auto-approve -input=false -no-color || { shown; fail "BREAK: could not reconverge A"; }
  gauntlet_stage drift_reconverge pass "BREAK=1 control: with two objects tampered the single-object assertion correctly fails ($(plan_line <<< "$DR_PLAN")); reconverged afterwards"
else
  grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$DR_PLAN" || { printf '%s\n' "$DR_PLAN" | tail -20; fail "the plan after one tamper does not propose exactly one change"; }
  grep -q "kubernetes_config_map.app " <<< "$DR_PLAN" || fail "the plan does not name kubernetes_config_map.app"
  RC_OUT="$(chdf "$APP_LIVE" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$RC_OUT" | tail -20; fail "the reconverging apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$RC_OUT" || { printf '%s\n' "$RC_OUT" | tail -20; fail "the reconverging apply did not change exactly one object"; }
  GW="$(kca get configmap app-config -n "$NS_APP" -o jsonpath='{.data.gateway}')"
  [ "$GW" = "gateway.$NS_NET.svc.cluster.local" ] || fail "app-config's gateway reads $GW after reconverging, want gateway.$NS_NET.svc.cluster.local"
  gauntlet_stage drift_reconverge pass "app-config tampered with kubectl patch; choudoufu proposed exactly kubernetes_config_map.app (0 add, 1 change, 0 destroy), matching stock's own plan on the oracle cluster; apply changed 1 and the gateway value - built from network's recorded outputs - reads back as gateway.$NS_NET.svc.cluster.local. BREAK=1 tampers a second object and the single-object assertion correctly fails"
fi

# ── 6. plan_approval: plan -out, the world moves, apply refuses ──────────
gauntlet_begin_stage plan_approval
log "=== 6. plan_approval: a saved plan, an out-of-band label, a refusal, then the same file ==="
write_app "$APP_LIVE" live reviewed=1; write_app "$ORACLE_APP" stock reviewed=1
PA_PLAN="$(chdf "$APP_LIVE" plan -out=approved.tfplan -input=false -no-color 2>&1)" || { printf '%s\n' "$PA_PLAN" | tail -20; fail "plan -out failed"; }
grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$PA_PLAN" || { printf '%s\n' "$PA_PLAN" | tail -10; fail "the saved plan is not exactly one change"; }
kca label configmap shard-0 -n "$NS_APP" stray=yes >/dev/null || fail "could not move the world (label shard-0)"
PA_APPLY="$(chdf "$APP_LIVE" apply -input=false -no-color approved.tfplan 2>&1)"; PA_RC=$?
if [ "${BREAK_APPROVAL:-}" = "1" ]; then
  [ "$PA_RC" -eq 0 ] && fail "BREAK_APPROVAL=1: applying the saved plan after the world moved succeeded"
  kca label configmap shard-0 -n "$NS_APP" stray- >/dev/null
  ran chdf "$APP_LIVE" apply -input=false -no-color approved.tfplan || { shown; fail "BREAK_APPROVAL: the saved plan did not apply once the world was put back"; }
  ran stock_b apply -auto-approve -input=false -no-color || { shown; fail "BREAK_APPROVAL: stock could not apply the reviewed change on B"; }
  gauntlet_stage plan_approval pass "BREAK_APPROVAL=1 control: applying the saved plan after the world moved exited $PA_RC (refused), so 'expect success' correctly fails; applied once the world was put back"
else
  [ "$PA_RC" -eq 3 ] || { printf '%s\n' "$PA_APPLY" | tail -20; fail "apply of the saved plan after the world moved exited $PA_RC, want 3"; }
  grep -qF "The approved plan no longer matches the live system" <<< "$PA_APPLY" || { printf '%s\n' "$PA_APPLY" | tail -20; fail "the refusal does not carry its documented sentence"; }
  [ -z "$(kca get configmap app-config -n "$NS_APP" -o jsonpath='{.data.reviewed}')" ] || fail "app-config gained reviewed despite the refusal"
  kca label configmap shard-0 -n "$NS_APP" stray- >/dev/null || fail "could not put the world back"
  PA_APPLY2="$(chdf "$APP_LIVE" apply -input=false -no-color approved.tfplan 2>&1)" || { printf '%s\n' "$PA_APPLY2" | tail -20; fail "the saved plan did not apply once the world was put back"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$PA_APPLY2" || { printf '%s\n' "$PA_APPLY2" | tail -20; fail "the saved plan's apply did not change exactly one object"; }
  [ "$(kca get configmap app-config -n "$NS_APP" -o jsonpath='{.data.reviewed}')" = "yes" ] || fail "app-config does not read reviewed=yes after the saved plan applied"
  { ran stock_b plan -out=approved.tfplan -input=false -no-color && ran stock_b apply -input=false -no-color approved.tfplan; } || { shown; fail "stock's own planfile did not apply on B"; }
  gauntlet_stage plan_approval pass "plan -out wrote one update (app-config gains reviewed=yes); a stray label on shard-0 (kubectl) moved the world and apply of the saved plan refused with \"The approved plan no longer matches the live system\" at exit 3, nothing applied; with the label removed the identical file applied, 0 added, 1 changed, 0 destroyed, and reviewed=yes reads back; stock's own planfile applied on the oracle cluster. BREAK_APPROVAL=1 expects success after the move and correctly fails"
fi

# ── 7. day2_rename: a moved block ────────────────────────────────────────
gauntlet_begin_stage day2_rename
log "=== 7. day2_rename: kubernetes_service_account.app becomes .team through a moved block ==="
write_app "$ORACLE_APP" stock reviewed=1 sa=team moved=1
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's moved-block plan failed on B"; }
grep -qE "^No changes|Plan: 0 to add, 0 to change, 0 to destroy" <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's moved-block plan on B is not zero churn"; }
ran stock_b apply -auto-approve -input=false -no-color || { shown; fail "stock's moved-block apply failed on B"; }
if [ "${BREAK:-}" = "1" ]; then
  write_app "$APP_LIVE" live reviewed=1
  sed -i.bak 's/^    name      = "app"$/    name      = "app-renamed"/' "$APP_LIVE/main.tf" && rm -f "$APP_LIVE/main.tf.bak"
  grep -q '"app-renamed"' "$APP_LIVE/main.tf" || fail "BREAK: could not rename the ServiceAccount's metadata.name"
  RN_PLAN="$(chdf "$APP_LIVE" plan -input=false -no-color 2>&1)" || fail "BREAK: the plan after renaming the object failed"
  grep -q "1 to add" <<< "$RN_PLAN" && grep -q "1 to destroy" <<< "$RN_PLAN" || fail "BREAK=1: renaming the object's own name did not plan a destroy and a create: $(plan_line <<< "$RN_PLAN")"
  write_app "$APP_LIVE" live reviewed=1 sa=team moved=1
  ran chdf "$APP_LIVE" apply -auto-approve -input=false -no-color || { shown; fail "BREAK: the moved-block apply failed"; }
  gauntlet_stage day2_rename pass "BREAK=1 control: renaming the ServiceAccount's own metadata.name plans a replace ($(plan_line <<< "$RN_PLAN")), so the marker-rewritten-in-place assertion correctly fails; the moved block then applied"
else
  write_app "$APP_LIVE" live reviewed=1 sa=team moved=1
  RN_PLAN="$(chdf "$APP_LIVE" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$RN_PLAN" | tail -20; fail "the moved-block plan failed"; }
  grep -qE 'will be (created|destroyed)' <<< "$RN_PLAN" && { printf '%s\n' "$RN_PLAN" | grep -E '^  # .+ will be'; fail "the moved-block rename proposes a create or a destroy"; }
  grep -qF 'Plan: 0 to add, 1 to change, 0 to destroy.' <<< "$RN_PLAN" || { printf '%s\n' "$RN_PLAN" | tail -20; fail "the moved-block plan is not exactly one in-place change"; }
  grep -qE '~ +"choudoufu\.intentius\.io/tofu-address" = ".*" -> ".*"' <<< "$RN_PLAN" || { printf '%s\n' "$RN_PLAN"; fail "the moved-block plan does not rewrite the tofu-address annotation"; }
  RN_APPLY="$(chdf "$APP_LIVE" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$RN_APPLY" | tail -20; fail "the moved-block apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$RN_APPLY" || { printf '%s\n' "$RN_APPLY" | tail -20; fail "the moved-block apply was not exactly one in-place change"; }
  exists_a serviceaccount app || fail "the ServiceAccount is gone after the rename"
  [ "$(count_app)" = "9" ] || fail "$(count_app) labelled app objects after the rename, want 9"
  gauntlet_stage day2_rename pass "moved block kubernetes_service_account.app -> .team: no add and no destroy, one in-place change confined to the address annotation rewrite (0 add, 1 change, 0 destroy); the ServiceAccount untouched and still labelled, read with kubectl; stock's plan for the same moved block on the oracle cluster is zero churn, since stock never writes the annotation. BREAK=1 renames metadata.name instead, a real identity change, and the in-place assertion correctly fails"
fi

# ── 8. day2_remove: the ServiceAccount's block leaves ────────────────────
gauntlet_begin_stage day2_remove
log "=== 8. day2_remove: the ServiceAccount block leaves the configuration ==="
if [ "${BREAK_REMOVE:-}" = "1" ]; then
  K_PLAN="$(chdf "$APP_LIVE" plan -input=false -no-color 2>&1)" || fail "BREAK_REMOVE: the plan with the block kept failed"
  grep -qE "will be destroyed|[1-9][0-9]* to destroy" <<< "$K_PLAN" && fail "BREAK_REMOVE=1: with the block kept a destroy was still proposed"
  gauntlet_stage day2_remove pass "BREAK_REMOVE=1 control: with the ServiceAccount block kept, no destroy is proposed; the real check is skipped"
  write_app "$APP_LIVE" live reviewed=1 sa=none; write_app "$ORACLE_APP" stock reviewed=1 sa=none
  ran chdf "$APP_LIVE" apply -auto-approve -input=false -no-color || { shown; fail "BREAK_REMOVE: the removal apply failed afterwards"; }
  ran stock_b apply -auto-approve -input=false -no-color || { shown; fail "BREAK_REMOVE: stock's removal apply failed on B"; }
else
  write_app "$APP_LIVE" live reviewed=1 sa=none; write_app "$ORACLE_APP" stock reviewed=1 sa=none
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's remove plan failed on B"; }
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's remove plan on B is not exactly one destroy"; }
  ran stock_b apply -auto-approve -input=false -no-color || { shown; fail "stock's remove apply failed on B"; }
  RM_PLAN="$(chdf "$APP_LIVE" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$RM_PLAN" | tail -20; fail "the remove plan failed"; }
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$RM_PLAN" || { printf '%s\n' "$RM_PLAN" | tail -20; fail "the remove plan is not exactly one destroy"; }
  RM_LINE="$(grep -E '^[[:space:]]*# .* will be destroyed' <<< "$RM_PLAN" | head -1)"
  RM_ADDR="$(sed -E 's/^[[:space:]#]*//; s/ will be destroyed.*$//' <<< "$RM_LINE")"
  [ "$RM_ADDR" = "kubernetes_service_account_v1.orphan_${NS_APP}_app" ] || fail "the one destroy is ${RM_ADDR:-unnamed}, not the orphan address kubernetes_service_account_v1.orphan_${NS_APP}_app"
  RM_APPLY="$(chdf "$APP_LIVE" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$RM_APPLY" | tail -20; fail "the remove apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 0 changed, 1 destroyed" <<< "$RM_APPLY" || { printf '%s\n' "$RM_APPLY" | tail -20; fail "the remove apply did not destroy exactly one object"; }
  exists_a serviceaccount app && fail "the ServiceAccount still exists after the remove apply"
  ran_has "No changes." chdf "$APP_LIVE" plan -input=false -no-color || { shown; fail "the replan after the remove is not empty"; }
  [ "$(count_app)" = "8" ] || fail "$(count_app) labelled app objects after the remove, want 8"
  gauntlet_stage day2_remove pass "deleting kubernetes_service_account.team's block proposed exactly one destroy at the sweep's orphan address $RM_ADDR, applied cleanly, the ServiceAccount gone (kubectl) and the next plan empty; stock's plan for the same removal on the oracle cluster is also exactly one destroy. BREAK_REMOVE=1 keeps the block and no destroy is proposed"
fi

# ── 9. day2_count: shards 2 -> 1 -> 2 ────────────────────────────────────
gauntlet_begin_stage day2_count
log "=== 9. day2_count: kubernetes_config_map.shard scales 2 -> 1 -> 2 ==="
write_app "$APP_LIVE" live reviewed=1 sa=none shards=1; write_app "$ORACLE_APP" stock reviewed=1 sa=none shards=1
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's scale-down plan failed on B"
grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$O_PLAN" && grep -q 'kubernetes_config_map.shard\[1\]' <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's scale-down on B is not exactly shard[1]'s destroy"; }
ran stock_b apply -auto-approve -input=false -no-color || { shown; fail "stock's scale-down apply failed on B"; }
CD_PLAN="$(chdf "$APP_LIVE" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$CD_PLAN" | tail -20; fail "the scale-down plan failed"; }
grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$CD_PLAN" || { printf '%s\n' "$CD_PLAN" | tail -20; fail "the scale-down plan is not exactly one destroy"; }
CD_ADDR="$(grep -E '^[[:space:]]*# .* will be destroyed' <<< "$CD_PLAN" | head -1 | sed -E 's/^[[:space:]#]*//; s/ will be destroyed.*$//')"
grep -qE "^kubernetes_config_map(_v1)?\.orphan_${NS_APP}_shard-1$" <<< "$CD_ADDR" || fail "the scale-down destroys ${CD_ADDR:-nothing named}, not shard-1 at its orphan address"
ran_has "0 added, 0 changed, 1 destroyed" chdf "$APP_LIVE" apply -auto-approve -input=false -no-color || { shown; fail "the scale-down apply did not destroy exactly one object"; }
if [ "${BREAK_COUNT:-}" = "1" ]; then
  exists_a configmap shard-0 || fail "BREAK_COUNT=1: shard-0 was destroyed - the 'wrong instance' assertion would hold"
  gauntlet_stage day2_count pass "BREAK_COUNT=1 control: asserting shard-0 was the one destroyed correctly fails to hold; the real check is skipped"
  write_app "$APP_LIVE" live reviewed=1 sa=none; write_app "$ORACLE_APP" stock reviewed=1 sa=none
  ran chdf "$APP_LIVE" apply -auto-approve -input=false -no-color || { shown; fail "BREAK_COUNT: the scale-up failed afterwards"; }
  ran stock_b apply -auto-approve -input=false -no-color || { shown; fail "BREAK_COUNT: stock's scale-up failed on B afterwards"; }
else
  exists_a configmap shard-0 || fail "shard-0 was destroyed on the scale-down"
  exists_a configmap shard-1 && fail "shard-1 still exists after the scale-down"
  write_app "$APP_LIVE" live reviewed=1 sa=none; write_app "$ORACLE_APP" stock reviewed=1 sa=none
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's scale-up plan failed on B"
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's scale-up plan on B is not exactly one add"; }
  ran stock_b apply -auto-approve -input=false -no-color || { shown; fail "stock's scale-up apply failed on B"; }
  CU_PLAN="$(chdf "$APP_LIVE" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$CU_PLAN" | tail -20; fail "the scale-up plan failed"; }
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$CU_PLAN" && grep -q 'kubernetes_config_map.shard\[1\]' <<< "$CU_PLAN" || { printf '%s\n' "$CU_PLAN" | tail -20; fail "the scale-up plan is not exactly shard[1]'s create"; }
  ran_has "1 added, 0 changed, 0 destroyed" chdf "$APP_LIVE" apply -auto-approve -input=false -no-color || { shown; fail "the scale-up apply did not create exactly one object"; }
  ran_has "No changes." chdf "$APP_LIVE" plan -input=false -no-color || { shown; fail "the replan after the scale-up is not empty"; }
  [ "$(count_app)" = "8" ] || fail "$(count_app) labelled app objects after the count cycle, want 8"
  gauntlet_stage day2_count pass "scaling kubernetes_config_map.shard 2 -> 1 destroyed exactly shard-1 at the sweep's orphan address $CD_ADDR (shard-0 untouched, kubectl); back to 2 created exactly shard[1]; the next plan is empty; stock's plans for the same two changes on the oracle cluster have the identical shape. BREAK_COUNT=1 asserts the lower index was destroyed and correctly fails"
fi

# ── 9b. day2_replace: a create_before_destroy rename ─────────────────────
gauntlet_begin_stage day2_replace
log "=== 9b. day2_replace: a content-hashed ConfigMap renamed under create_before_destroy ==="
gauntlet_kind_day2_replace "$APP_LIVE" "$ORACLE_APP" "$NS_APP"

# ── 10. day2_crash: three interrupted operations, each re-run ────────────
gauntlet_begin_stage day2_crash
log "=== 10. day2_crash: the create_before_destroy rename window, a two-object apply, and a cross-estate live-mv ==="

# 10a. The create_before_destroy rename window (#1768), read off the
# in-cluster store through mirror_records.
GAUNTLET_RECORDS_REFRESH=refresh_app_records
gauntlet_kind_day2_crash_rename "$APP_LIVE" "$NS_APP" "$WORK/app-records"
GAUNTLET_RECORDS_REFRESH=""

# 10b. An apply creating two objects, killed after the first (#1110 part 4),
# as reference-k8s, with the record read back from the cluster.
crash_pair_tf "$ORACLE_APP"
ran_has "Apply complete! Resources: 1 added" stock_b apply -auto-approve -input=false -no-color || { shown; fail "stock's crash-first apply failed on B"; }
crash_pair_tf "$ORACLE_APP" both
O_REM="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_REM" | tail -10; fail "stock's remainder plan failed on B"; }
grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_REM" && grep -q 'kubernetes_config_map.crash_second' <<< "$O_REM" \
  || { printf '%s\n' "$O_REM" | tail -10; fail "stock's remainder plan on B is not exactly crash_second's add"; }
ran stock_b apply -auto-approve -input=false -no-color || { shown; fail "stock's remainder apply failed on B"; }
crash_pair_tf "$APP_LIVE" both
X_PLAN="$(chdf "$APP_LIVE" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$X_PLAN" | tail -20; fail "the pre-crash plan failed"; }
grep -qF "Plan: 2 to add, 0 to change, 0 to destroy." <<< "$X_PLAN" || { printf '%s\n' "$X_PLAN" | tail -20; fail "the pre-crash plan is not exactly two adds"; }
mirror_records "$REC_APP" "$WORK/recs-before" || fail "could not read app's records before the interrupt"
X_RECORDS_BEFORE="$(gauntlet_record_envelope_count "$WORK/recs-before")"
X_OUT="$(cd "$APP_LIVE" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" TOFU_E2E_APPLY_RESOURCE_INTERRUPT="kubernetes_secret.crash_first" "$TOFU_CRASH" apply -input=false -auto-approve -no-color -parallelism=1 2>&1)"; X_RC=$?
[ "$X_RC" -ne 0 ] || { printf '%s\n' "$X_OUT" | tail -20; fail "the interrupted apply exited 0 - the engine's self-signal never landed"; }
exists_a secret crash-first || fail "crash-first does not exist after the interrupted apply - the kill landed before its create committed"
exists_a configmap crash-second && fail "crash-second exists after the interrupted apply - the kill landed after both creates"
X_LABELLED="$(kca get secret -n "$NS_APP" -l "tofu-estate=$APP" -o name 2>&1)"
grep -qx "secret/crash-first" <<< "$X_LABELLED" || { printf '%s\n' "$X_LABELLED" | tail -20; fail "crash-first does not come back under tofu-estate=$APP"; }
mirror_records "$REC_APP" "$WORK/recs-after" || fail "could not read app's records after the interrupt"
X_RECORDS_AFTER="$(gauntlet_record_envelope_count "$WORK/recs-after")"
X_REC="$(gauntlet_record_file "$WORK/recs-after" "kubernetes_secret.crash_first")" || fail "the interrupted apply wrote no record for kubernetes_secret.crash_first in $REC_APP (records $X_RECORDS_BEFORE -> $X_RECORDS_AFTER)"
X_RESIDUE="$(gauntlet_record_residue "$X_REC" | tr '\n' ' ' | sed 's/ $//')"
[ "$X_RESIDUE" = "wait_for_service_account_token" ] || fail "the record for kubernetes_secret.crash_first carries residue [${X_RESIDUE:-none}], want wait_for_service_account_token"
X_SECRET="$(awk -F'\t' -v p="$X_REC" '$1 == p { print $2 }' "$WORK/recs-after.index")"
[ -n "$X_SECRET" ] || fail "could not tell which record Secret holds kubernetes_secret.crash_first"
R_PLAN="$(chdf "$APP_LIVE" plan -input=false -no-color 2>&1)"; R_RC=$?
R_LINE="$(plan_line <<< "$R_PLAN")"
recovered() {
  [ "$R_RC" -eq 0 ] || return 1
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$R_PLAN" || return 1
  grep -qE '^[[:space:]]*# kubernetes_config_map\.crash_second will be created' <<< "$R_PLAN" || return 1
  local will
  will="$(grep -E '^[[:space:]]*# .* will be' <<< "$R_PLAN")"
  grep -q 'crash_first\|crash-first' <<< "$will" && return 1
  return 0
}
if [ "${BREAK_CRASH:-}" = "1" ]; then
  grep -qF "No changes." <<< "$R_PLAN" && fail "BREAK_CRASH=1: the plan after a real interrupted two-object apply came back empty"
  ran chdf "$APP_LIVE" apply -auto-approve -input=false -no-color || { shown; fail "BREAK_CRASH: the recovery apply failed afterwards"; }
  CRASH_APPLY_DETAIL="BREAK_CRASH=1: after the two-object interrupt the plan proposes work ($R_LINE), so 'nothing is proposed' correctly fails."
else
  recovered || { printf '%s\n' "$R_PLAN" | grep -E '^Plan:|^No changes|will be' | head -20; fail "the plan after the interrupt between crash_first's and crash_second's creates is not exactly the remainder: ${R_LINE:-no plan line} (exit $R_RC)"; }
  # The record's contribution, measured by taking its Secret away and
  # putting it back (#1235), as reference-k8s does with the file.
  kca get secret "$X_SECRET" -n "$REC_APP" -o json > "$WORK/crash-record.json" || fail "could not save the crash record Secret"
  kca delete secret "$X_SECRET" -n "$REC_APP" >/dev/null || fail "could not take the crash record out of the store"
  N_PLAN="$(chdf "$APP_LIVE" plan -input=false -no-color 2>&1)"; N_RC=$?
  N_LINE="$(plan_line <<< "$N_PLAN")"
  python3 - "$WORK/crash-record.json" <<'PY' || fail "could not prepare the crash record for restore"
import json, sys
p = sys.argv[1]; o = json.load(open(p)); m = o["metadata"]
for k in ("resourceVersion", "uid", "creationTimestamp", "managedFields"):
    m.pop(k, None)
json.dump(o, open(p, "w"))
PY
  kca create -f "$WORK/crash-record.json" >/dev/null || fail "could not put the crash record back"
  [ "$N_RC" -eq 0 ] && grep -qF "Plan: 1 to add, 1 to change, 0 to destroy." <<< "$N_PLAN" && grep -qE '^[[:space:]]+\+ wait_for_service_account_token +=' <<< "$N_PLAN" \
    || { printf '%s\n' "$N_PLAN" | grep -E '^Plan:|will be|^ +[+~-] ' | head -20; fail "with the crash record's Secret taken out of $REC_APP the plan is $N_LINE (exit $N_RC), not the remainder plus wait_for_service_account_token put back"; }
  B_PLAN="$(chdf "$APP_LIVE" plan -input=false -no-color 2>&1)"
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$B_PLAN" || { printf '%s\n' "$B_PLAN" | tail -20; fail "putting the record Secret back does not restore the exact-remainder plan"; }
  R_APPLY="$(chdf "$APP_LIVE" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$R_APPLY" | tail -20; fail "the recovery apply failed"; }
  grep -qF "Apply complete! Resources: 1 added, 0 changed, 0 destroyed" <<< "$R_APPLY" || { printf '%s\n' "$R_APPLY" | tail -20; fail "the recovery apply did not add exactly the one remaining object"; }
  exists_a configmap crash-second && exists_a secret crash-first || fail "after the recovery crash-first and crash-second do not both exist"
  ran_has "No changes." chdf "$APP_LIVE" plan -input=false -no-color || { shown; fail "the replan after the recovery is not empty"; }
  CRASH_APPLY_DETAIL="An apply creating two objects was killed by the engine's own SIGTERM the instant kubernetes_secret.crash_first's create committed (exit $X_RC, -parallelism=1, crash_second reads crash_first's name); the next plan proposed exactly the remainder ($R_LINE) and nothing for crash-first, matching stock's plan from the same position on the oracle cluster, and one apply finished it. The record the interrupted apply wrote is a Secret in $REC_APP (envelopes $X_RECORDS_BEFORE -> $X_RECORDS_AFTER) carrying residue $X_RESIDUE: taking that Secret out turns the plan into $N_LINE, putting it back restores the remainder."
fi
[ "$(count_app)" = "10" ] || fail "$(count_app) labelled app objects after the two-object crash, want 10"

# 10c. live-mv -from-estate on a kubernetes_manifest object, killed between
# its two requests (#1858's window) and re-run. The object is the handoff
# ConfigMap in network's namespace; the move makes network its owner.
#
# The oracle is stock's own move on cluster B: `terraform state rm` in
# app's root and `terraform import` in network's, after which both stock
# plans are empty. choudoufu's answer has to end in the same place: the
# object network's, network's plan and app's plan both empty.
write_net "$ORACLE_NET" 1
write_app "$ORACLE_APP" stock reviewed=1 sa=none handoff=0
ran stock_b state rm kubernetes_manifest.handoff || { shown; fail "stock's state rm of the handoff manifest failed on B"; }
ran stock_bn import -input=false -no-color kubernetes_manifest.handoff "apiVersion=v1,kind=ConfigMap,namespace=$NS_NET,name=handoff" || { shown; fail "stock's import of the handoff manifest into network failed on B"; }
ran_has "No changes." stock_bn plan -input=false -no-color || { shown; fail "stock's network plan on B after the move is not empty"; }
ran_has "No changes." stock_b plan -input=false -no-color || { shown; fail "stock's app plan on B after the move is not empty"; }

write_net "$NET_LIVE" 1
write_app "$APP_LIVE" live reviewed=1 sa=none handoff=0
MV_KILL="$(cd "$NET_LIVE" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" TOFU_E2E_LIVE_MV_INTERRUPT="$NS_NET/handoff" "$TOFU_CRASH" live-mv -no-color -from-estate="$APP" kubernetes_manifest.handoff kubernetes_manifest.handoff 2>&1)"; MV_KILL_RC=$?
[ "$MV_KILL_RC" -ne 0 ] || { printf '%s\n' "$MV_KILL" | tail -20; fail "the interrupted live-mv exited 0 - the kill never landed"; }
grep -qF "TOFU_E2E_LIVE_MV_INTERRUPT: killing live-mv after the marker patch on ConfigMap $NS_NET/handoff" <<< "$MV_KILL" \
  || { printf '%s\n' "$MV_KILL" | tail -20; fail "the live-mv exited $MV_KILL_RC without reaching the window between its two requests"; }
MV_LABEL="$(kca get configmap handoff -n "$NS_NET" -o jsonpath='{.metadata.labels.tofu-estate}')"
MV_ANN="$(kca get configmap handoff -n "$NS_NET" -o jsonpath='{.metadata.annotations.choudoufu\.intentius\.io/tofu-address}')"
MV_HELD="$(markers_held_by_update "$NS_NET" handoff)"
[ "$MV_LABEL" = "$NET" ] && [ "$MV_ANN" = "kubernetes_manifest.handoff" ] \
  || fail "after the kill handoff carries tofu-estate=$MV_LABEL and address $MV_ANN; the merge patch was supposed to have landed"
if [ "${BREAK_MV:-}" = "1" ]; then
  [ "$MV_HELD" = "free" ] && fail "BREAK_MV=1: straight after the kill, with no rerun, the markers are already out of every Update entry - the hand-off check below would pass without the rerun doing anything"
  MV_DETAIL="BREAK_MV=1: straight after the killed live-mv the markers are still $MV_HELD by an Update entry, so asserting the hand-off finished without the rerun correctly fails."
fi
[ "$MV_HELD" = "held" ] || fail "after the kill no Update entry holds the markers ($MV_HELD); the kill did not land between the merge patch and the hand-off"
MV_RERUN="$(chdf "$NET_LIVE" live-mv -no-color -from-estate="$APP" kubernetes_manifest.handoff kubernetes_manifest.handoff 2>&1)" \
  || { printf '%s\n' "$MV_RERUN" | tail -20; fail "the rerun of the killed live-mv failed: $(gauntlet_first_error_line <<< "$MV_RERUN")"; }
MV_AFTER="$(markers_held_by_update "$NS_NET" handoff)"
[ "$MV_AFTER" = "free" ] || fail "after the rerun the markers are $MV_AFTER by an Update entry; the hand-off was not finished (#1858)"
MV_NET_PLAN="$(chdf "$NET_LIVE" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$MV_NET_PLAN" | tail -20; fail "network's plan after the move failed"; }
grep -q "No changes." <<< "$MV_NET_PLAN" || { printf '%s\n' "$MV_NET_PLAN" | tail -20; fail "network's plan after the move is not empty: $(plan_line <<< "$MV_NET_PLAN")"; }
MV_APP_PLAN="$(chdf "$APP_LIVE" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$MV_APP_PLAN" | tail -20; fail "app's plan after the move failed"; }
grep -q "No changes." <<< "$MV_APP_PLAN" || { printf '%s\n' "$MV_APP_PLAN" | tail -20; fail "app's plan after the move is not empty: $(plan_line <<< "$MV_APP_PLAN")"; }
# A provider apply that writes the markers now has to go through: network's
# no-op apply reasserts them under its Apply entry with nothing to conflict.
MV_NET_APPLY="$(chdf "$NET_LIVE" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$MV_NET_APPLY" | tail -20; fail "network's apply after the move failed (a field manager conflict here is #1858's symptom)"; }
[ "$(count_net)" = "6" ] && [ "$(count_app)" = "9" ] || fail "after the move network carries $(count_net) labels (want 6) and app $(count_app) (want 9)"
MV_DETAIL="${MV_DETAIL:-} live-mv -from-estate=$APP of kubernetes_manifest.handoff (a ConfigMap in $NS_NET) into network was killed by SIGKILL between its two requests (TOFU_E2E_LIVE_MV_INTERRUPT on the e2e build, exit $MV_KILL_RC): kubectl read tofu-estate=$NET and the new address on the object with the markers still held by an Update entry; the plain rerun finished the hand-off (no Update entry holds a marker), network's and app's plans are empty and network's apply goes through, the same end state as stock's state rm and import on the oracle cluster."

if [ "${BREAK_CRASH:-}" = "1" ] || [ "${BREAK_MV:-}" = "1" ]; then
  gauntlet_stage day2_crash pass "Break controls: $CRASH_RENAME_DETAIL $CRASH_APPLY_DETAIL $MV_DETAIL"
else
  gauntlet_stage day2_crash pass "$CRASH_APPLY_DETAIL $MV_DETAIL $CRASH_RENAME_DETAIL BREAK_CRASH=1 asserts nothing is proposed after the apply interrupts and correctly fails; BREAK_MV=1 asserts the hand-off finished without the rerun and correctly fails"
fi

# ── 11. day2_teardown: app first, network second ─────────────────────────
gauntlet_begin_stage day2_teardown
log "=== 11. day2_teardown: app's apply -destroy, then network's; each estate's records go with it ==="
T_APP="$(count_app)"; T_NET="$(count_net)"
T_OUT="$(chdf "$APP_LIVE" apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$T_OUT" | tail -20; fail "app's apply -destroy failed"; }
grep -qF "Resources: 0 added, 0 changed, $T_APP destroyed" <<< "$T_OUT" || { printf '%s\n' "$T_OUT" | tail -5; fail "app's destroy did not remove exactly its $T_APP objects"; }
T_APP_LEFT="$(count_app)"; T_APP_REC="$(record_secrets "$REC_APP" "$APP" tofu-records)"
[ "$T_APP_LEFT" = "0" ] || fail "$T_APP_LEFT object(s) still carry tofu-estate=$APP after app's destroy"
[ "$T_APP_REC" = "0" ] || fail "$T_APP_REC record Secret(s) of app's remain in $REC_APP after its destroy"
[ "$(count_net)" = "$T_NET" ] || fail "app's destroy moved network's count ($T_NET -> $(count_net))"
T_OUT="$(chdf "$NET_LIVE" apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$T_OUT" | tail -20; fail "network's apply -destroy failed"; }
grep -qF "Resources: 0 added, 0 changed, $T_NET destroyed" <<< "$T_OUT" || { printf '%s\n' "$T_OUT" | tail -5; fail "network's destroy did not remove exactly its $T_NET objects"; }
for _ in $(seq 1 30); do kca get namespace "$NS_NET" >/dev/null 2>&1 || kca get namespace "$NS_APP" >/dev/null 2>&1 || break; sleep 2; done
kca get namespace "$NS_NET" >/dev/null 2>&1 && fail "the $NS_NET namespace still exists after the destroys"
kca get namespace "$NS_APP" >/dev/null 2>&1 && fail "the $NS_APP namespace still exists after the destroys"
[ "$(count_net)" = "0" ] || fail "$(count_net) object(s) still carry tofu-estate=$NET"
T_NET_REC="$(record_secrets "$REC_NET" "$NET" tofu-records)"; T_NET_OUT="$(record_secrets "$REC_NET" "$NET" tofu-outputs)"
[ "$T_NET_REC" = "0" ] && [ "$T_NET_OUT" = "0" ] || fail "network's destroy left $T_NET_REC record and $T_NET_OUT output Secret(s) in $REC_NET"
T_HINTS="$(( $(record_secrets "$REC_NET" "$NET" tofu-hints) + $(record_secrets "$REC_APP" "$APP" tofu-hints) ))"
ran stock_b apply -destroy -auto-approve -input=false -no-color || { shown; fail "stock's destroy of app failed on B"; }
ran stock_bn apply -destroy -auto-approve -input=false -no-color || { shown; fail "stock's destroy of network failed on B"; }
gauntlet_stage day2_teardown pass "app's apply -destroy removed exactly its $T_APP objects while network's $T_NET stood untouched and app's records left $REC_APP with them (0 record Secrets); then network's removed its $T_NET (the handoff manifest it took over by live-mv included), both namespaces are gone, no object of either estate carries tofu-estate (kubectl, every namespace but the records ones), and $REC_NET holds 0 record and 0 output Secrets - so app's read of network's outputs would now be told they are not recorded. Guided-discovery hints left in the records namespaces: $T_HINTS. Stock's destroys of the same two estates on the oracle cluster, app then network, both completed"

# ── 12. greenfield: both estates fresh, records in the cluster ───────────
gauntlet_begin_stage greenfield
log "=== 12. greenfield: network then app applied fresh on the now-empty cluster A ==="
mkdir -p "$GREEN_NET" "$GREEN_APP"
versions_tf "$GREEN_NET" live "$NET"; write_net "$GREEN_NET"
versions_tf "$GREEN_APP" live "$APP"; write_app "$GREEN_APP" live
ran chdf "$GREEN_NET" init -input=false -no-color || { shown; fail "greenfield init failed in network's root"; }
ran chdf "$GREEN_APP" init -input=false -no-color || { shown; fail "greenfield init failed in app's root"; }
G_NET="$(chdf "$GREEN_NET" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$G_NET" | tail -20; fail "network's greenfield apply failed"; }
grep -qF "Apply complete! Resources: 5 added, 0 changed, 0 destroyed" <<< "$G_NET" || { printf '%s\n' "$G_NET" | tail -20; fail "network's greenfield apply did not add exactly 5 objects"; }
G_APP="$(chdf "$GREEN_APP" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$G_APP" | tail -20; fail "app's greenfield apply failed"; }
grep -qF "Apply complete! Resources: 9 added, 0 changed, 0 destroyed" <<< "$G_APP" || { printf '%s\n' "$G_APP" | tail -20; fail "app's greenfield apply did not add exactly 9 objects"; }
G_LOCAL="$(find "$GREEN_NET" "$GREEN_APP" -name '.tofu-records' -o -name 'terraform.tfstate' | head -3)"
[ -z "$G_LOCAL" ] || fail "a live-block apply with records in the cluster left local files: $G_LOCAL"
[ "$(count_net)" = "5" ] && [ "$(count_app)" = "9" ] || fail "after greenfield network carries $(count_net) labels (want 5), app $(count_app) (want 9)"
G_OUTS="$(record_secrets "$REC_NET" "$NET" tofu-outputs)"
[ "$G_OUTS" = "2" ] || fail "network's greenfield apply recorded $G_OUTS output Secret(s), want 2"
mirror_records "$REC_APP" "$WORK/green-records" || fail "could not read app's greenfield records"
G_RECORDS="$(gauntlet_record_envelope_count "$WORK/green-records")"
for d in "$GREEN_NET" "$GREEN_APP"; do
  ran_has "No changes." chdf "$d" plan -input=false -no-color || { shown; fail "the greenfield replan in $(basename "$d") is not empty"; }
  rm -f "$d/.terraform/choudoufu-cache.tfstate"
  ran_has "No changes." chdf "$d" plan -input=false -no-color || { shown; fail "the greenfield replan in $(basename "$d") without the cache is not empty"; }
done
DROP=""; [ "${BREAK:-}" = "1" ] && DROP="hpa/web"
inventory "$KCA" "$DROP" > "$WORK/inventory.green.json" || fail "could not read the greenfield inventory"
if [ "${BREAK:-}" = "1" ]; then
  diff -q "$WORK/inventory.stock.json" "$WORK/inventory.green.json" >/dev/null && fail "BREAK=1: with the HPA dropped from the greenfield inventory the two still match"
  gauntlet_stage greenfield pass "BREAK=1 control: dropping the HorizontalPodAutoscaler from the greenfield inventory makes the object-by-object comparison correctly fail; both estates applied (5 and 9 added) with no local record store and no terraform.tfstate"
else
  diff -u "$WORK/inventory.stock.json" "$WORK/inventory.green.json" || fail "the greenfield inventory differs from stock's cold-deploy inventory (diff above)"
  gauntlet_stage greenfield pass "network then app applied fresh with live blocks and record_store \"kubernetes\" (5 and 9 added), no terraform.tfstate and no .tofu-records directory in either root; every object labelled with its estate (kubectl); network's two outputs recorded as Secrets in $REC_NET and app's $G_RECORDS record envelope(s) read back from $REC_APP; both replanned empty with and without the cache. The two estates' inventory - the NetworkPolicy, both Services, the five ConfigMaps (the handoff manifest's included), the Deployment and its GATEWAY_HOST built from network's outputs, the HPA and the ServiceAccount - matches stock's cold deploy on the same cluster object by object, labels never compared. BREAK=1 drops the HPA from the expected inventory and the match correctly fails"
fi

# ── 13. no_local_state: the cache-served plan, then no cache at all ──────
#
# With the records in the cluster the only local state an estate has is
# the state cache, so "a fresh clone" is the cache deleted. The requests are
# counted on the wire: live/smoke/k8sproxy.py relays to cluster A's API
# server and writes one line per request (its own header says why the API
# server's counter cannot answer this). It forwards as the admin, which is
# what these plans run as anyway.
gauntlet_begin_stage no_local_state
log "=== 13. no_local_state: app's unchanged plan from the cache, reads = \"full\", and no cache at all ==="
PROXY_DIR="$WORK/proxy"; mkdir -p "$PROXY_DIR"
SERVER="$(kubectl --kubeconfig "$KCA" config view --raw --minify -o jsonpath='{.clusters[0].cluster.server}')"
[ -n "$SERVER" ] || fail "cluster A's kubeconfig names no server"
kubectl --kubeconfig "$KCA" config view --raw --minify -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' | base64 -d > "$PROXY_DIR/upstream-ca.crt"
kubectl --kubeconfig "$KCA" config view --raw --minify -o jsonpath='{.users[0].user.client-certificate-data}' | base64 -d > "$PROXY_DIR/upstream-client.crt"
kubectl --kubeconfig "$KCA" config view --raw --minify -o jsonpath='{.users[0].user.client-key-data}' | base64 -d > "$PROXY_DIR/upstream-client.key"
for f in upstream-ca.crt upstream-client.crt upstream-client.key; do [ -s "$PROXY_DIR/$f" ] || fail "cluster A's kubeconfig yielded no $f for the request counter"; done
openssl req -x509 -newkey rsa:2048 -nodes -keyout "$PROXY_DIR/proxy.key" -out "$PROXY_DIR/proxy.crt" -days 1 \
  -subj /CN=gauntlet-proxy -addext subjectAltName=IP:127.0.0.1 >/dev/null 2>&1 || fail "openssl could not write the counter's certificate"
python3 "$ROOT/live/smoke/k8sproxy.py" "$SERVER" "$PROXY_DIR" >"$PROXY_DIR/proxy.out" 2>"$PROXY_DIR/proxy.err" &
PROXY_PID=$!
for _ in $(seq 1 50); do [ -s "$PROXY_DIR/proxy.port" ] && break; sleep 0.1; done
[ -s "$PROXY_DIR/proxy.port" ] || fail "the request counter never started: $(cat "$PROXY_DIR/proxy.err")"
PROXY_KC="$PROXY_DIR/proxy.kubeconfig"; cp "$KCA" "$PROXY_KC"
kubectl --kubeconfig "$PROXY_KC" config set-cluster "kind-$CLUSTER_A" --server="https://127.0.0.1:$(cat "$PROXY_DIR/proxy.port")" --insecure-skip-tls-verify=true >/dev/null \
  || fail "could not point a kubeconfig at the request counter"
kubectl --kubeconfig "$PROXY_KC" --request-timeout=30s get namespace kube-system >/dev/null 2>&1 || fail "a read through the request counter failed: $(cat "$PROXY_DIR/proxy.err")"
# counted_plan <label> [env...]: plan -refresh=false in app's greenfield root
# through the counter, with a debug log; prints "<cache hits> <requests> <rc>".
counted_plan() {
  local label="$1" rc; shift
  : > "$PROXY_DIR/proxy.log"
  ( cd "$GREEN_APP" && env "$@" KUBECONFIG="$PROXY_KC" KUBE_CONFIG_PATH="$PROXY_KC" TF_LOG=debug TF_LOG_PATH="$WORK/$label.log" \
      "$TOFU" plan -refresh=false -input=false -no-color > "$WORK/$label.plan" 2>&1 ); rc=$?
  printf '%s %s %s\n' "$(grep -c 'state cache hit' "$WORK/$label.log" 2>/dev/null || true)" "$(grep -c . "$PROXY_DIR/proxy.log" || true)" "$rc"
}
CACHE="$GREEN_APP/.terraform/choudoufu-cache.tfstate"
ran chdf "$GREEN_APP" plan -input=false -no-color || { shown; fail "the plan that writes app's cache failed"; }
[ -s "$CACHE" ] || fail "no state cache at $CACHE after a plan, so there is nothing to serve from or delete"
cp "$CACHE" "$WORK/cache.keep"
read -r HITS_SEL REQ_SEL RC_SEL <<< "$(counted_plan selective)"
cp "$WORK/cache.keep" "$CACHE"
read -r HITS_FULL REQ_FULL RC_FULL <<< "$(counted_plan full CHOUDOUFU_READS=full)"
[ "$RC_SEL" = "0" ] && grep -q "No changes." "$WORK/selective.plan" || fail "the cache-serving plan (exit $RC_SEL) is not empty: $(tail -5 "$WORK/selective.plan")"
[ "$RC_FULL" = "0" ] && grep -q "No changes." "$WORK/full.plan" || fail "the reads = \"full\" plan (exit $RC_FULL) is not empty: $(tail -5 "$WORK/full.plan")"
[ "$REQ_FULL" -gt 0 ] || fail "the reads = \"full\" plan sent no request through the counter, so it is not on the plan's path"
[ "$HITS_FULL" = "0" ] || fail "reads = \"full\" still served $HITS_FULL instance(s) from the cache"
[ "$HITS_SEL" -gt 0 ] || fail "the unchanged plan served nothing from a fresh cache (#1864's vouch did not happen)"
[ "$REQ_SEL" -lt "$REQ_FULL" ] || fail "the cache-serving plan cost $REQ_SEL requests against reads = \"full\"'s $REQ_FULL"
rm -f "$CACHE"
[ ! -e "$CACHE" ] || fail "the state cache survived deletion"
if [ "${BREAK_NO_LOCAL_STATE:-}" = "1" ]; then
  kca label service web -n "$NS_APP" tofu-estate- >/dev/null || fail "BREAK_NO_LOCAL_STATE: could not strip the web Service's label"
fi
read -r _ REQ_NONE RC_NONE <<< "$(counted_plan nocache)"
NLS_BAD=""
[ "$RC_NONE" = "0" ] || NLS_BAD="the plan with no cache exited $RC_NONE: $(grep -m1 '^Error' "$WORK/nocache.plan")"
[ -z "$NLS_BAD" ] && grep -qE '# .+ (will be (created|destroyed)|must be replaced)' "$WORK/nocache.plan" \
  && NLS_BAD="with no cache the plan proposes: $(grep -E '# .+ (will be (created|destroyed)|must be replaced)' "$WORK/nocache.plan" | sed 's/^ *//' | tr '\n' ' ')"
if [ "${BREAK_NO_LOCAL_STATE:-}" = "1" ]; then
  [ -n "$NLS_BAD" ] || fail "BREAK_NO_LOCAL_STATE=1: with the web Service's label stripped and no cache, the plan still found everything"
  kca label service web -n "$NS_APP" "tofu-estate=$APP" >/dev/null || fail "BREAK_NO_LOCAL_STATE: could not put the label back"
  gauntlet_stage no_local_state pass "BREAK_NO_LOCAL_STATE=1 control: with the web Service's tofu-estate label stripped and the cache deleted, the check correctly failed ($NLS_BAD); the label was put back"
else
  [ -z "$NLS_BAD" ] || fail "$NLS_BAD"
  NLS_UPD="$(grep -cE '# .+ will be updated in-place' "$WORK/nocache.plan" || true)"
  RATIO="$(awk -v a="$REQ_NONE" -v b="$REQ_SEL" 'BEGIN { printf "%.2f", a / b }')"
  gauntlet_stage no_local_state pass "app's records live in the cluster, so the only local state is the state cache; with it deleted - a fresh clone - the plan found every declared object by its label and namespace and name: nothing created, destroyed or replaced, $NLS_UPD in-place update(s). The unchanged plan before the deletion was served from the cache: $HITS_SEL instance(s) answered by a cache hit (#1864's vouch), $REQ_SEL requests against reads = \"full\"'s $REQ_FULL with 0 hits, both plans empty. plan_calls_no_local_state=$REQ_NONE plan_calls_cache_serving=$REQ_SEL ratio=${RATIO}x (all plan -refresh=false, Kubernetes API requests counted on the wire through live/smoke/k8sproxy.py); stock in this position has no plan at all, only one import block per object. BREAK_NO_LOCAL_STATE=1 strips the web Service's label and the check correctly fails"
fi
kill "$PROXY_PID" 2>/dev/null || true; PROXY_PID=""
ran chdf "$GREEN_APP" apply -destroy -auto-approve -input=false -no-color || { shown; fail "app's greenfield teardown failed"; }
ran chdf "$GREEN_NET" apply -destroy -auto-approve -input=false -no-color || { shown; fail "network's greenfield teardown failed"; }

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
    estate = "platform-app-strict"
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
log "=== 14. strict: every strict toggle on ==="
strict_block "refuse" > "$STRICT/main.tf"
ran in_dir "$STRICT" "$TOFU" init -input=false -no-color || { shown; fail "choudoufu init for the strict-stage scratch estate failed"; }
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
  grep -qF 'Error: Logical resource is not admitted' <<< "$STRICT_ON" || fail "the one refusal is not \"Logical resource is not admitted\""
  grep -qF 'strict { secrets = "refuse" }' <<< "$STRICT_ON" || fail "the refusal does not cite strict { secrets = \"refuse\" }"
  gauntlet_stage strict pass "every strict toggle on (secrets = refuse, no_source_create = refuse, marker_repair = never with a markers \"record\" selection naming kubernetes_config_map) against a scratch estate carrying random_password.db: exactly one refusal, Logical resource is not admitted under strict { secrets = \"refuse\" }. BREAK_STRICT=1 turns secrets back to \"store\" and the refusal disappears"
fi
gauntlet_end_stage

gauntlet_end
log "$GAUNTLET_ESTATE: done"
