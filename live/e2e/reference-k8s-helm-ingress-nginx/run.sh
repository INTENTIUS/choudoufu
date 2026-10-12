#!/usr/bin/env bash
# reference-k8s-helm-ingress-nginx (#1984): the kubernetes lane's Helm estate for an
# ingress controller with no CRDs, crossed on the kind substrate - the shape
# reference-k8s-helm-template (#1963) set, for a different chart.
#
# The configuration is a Helm chart rendered CLIENT-SIDE through
# hashicorp/helm's `data "helm_template"` and applied as kubernetes_manifest
# blocks - the shape live/kubernetes/COMPATIBILITY.md recommends for a chart.
# No helm_release anywhere: Helm never talks to the cluster, the provider is
# declared for the one data source.
#
#   chart    kubernetes/ingress-nginx 4.15.1 (controller v1.15.1), Apache-2.0.
#            The upstream project was retired in March 2026 and its GitHub
#            repository is archived; 4.15.1 is its last chart, and both the
#            release asset and the controller image (pinned by digest in the
#            chart, linux/arm64 published) are still served
#   archive  $CHART_URL below, sha256 $CHART_SHA256 below, checked on every run
#   render   18 documents at the chart's defaults (two certgen hook Jobs among
#            them), 9 objects over 9 kinds with root/values.yaml's deltas (the
#            admission webhook off, a ClusterIP Service with one annotation,
#            one controller config key)
#   root     root/main.tf, ours, hence reference-: one data source, a
#            Namespace (helm template renders none), the render's 8
#            non-workload objects, its Deployment after them, and one Ingress
#            per rendered IngressClass - each for_each over the render split
#            and yamldecoded INSIDE the for_each expression (#1962)
#
# What it drives that nothing else in the lane does:
#
#   - a Helm render with no CRDs at all, so no declared pre-apply: the whole
#     root plans and applies in ONE pass against an empty cluster, on every
#     side, at cold deploy and at greenfield. Every other Helm estate in the
#     lane is a two-apply crossing;
#   - a block of the root's own (kubernetes_manifest.ingresses) whose
#     for_each keys are read off the render - one Ingress per IngressClass
#     the chart renders - so a single values edit scales a chart block and a
#     declared block together (day2_count: ingressClassResource.aliases),
#     and a single toggle takes instances out of both (day2_remove:
#     ingressClassResource.enabled), across a cluster-scoped and a
#     namespaced kind;
#   - a controller that writes the STATUS subresource of objects the estate
#     owns: with --publish-service, ingress-nginx writes the controller
#     Service's cluster IP into status.loadBalancer of every Ingress it
#     admits, read back with kubectl, and no replan may propose touching it.
#     The lane's other Ingress (reference-k8s-workloads) has no controller;
#   - objects the cluster creates beside the estate's and which nothing
#     declares: the controller's leader-election Lease (no owner reference)
#     and the EndpointSlice and Endpoints the Service gets. Endpoints shares
#     its name with three declared objects of other kinds and, like the
#     EndpointSlice, copies the Service's labels - tofu-estate included once
#     migrate has stamped it. day2_remove's sweep must leave them alone;
#   - one name across five kinds, two of them cluster-scoped
#     (rel-ingress-nginx), and one across three (rel-ingress-nginx-controller);
#   - day-2 changes made the way a chart user makes them: through values.
#
# ── one apply ────────────────────────────────────────────────────────────
#
# Two kind clusters, both created for this run and deleted after it:
#
#   A  the estate. Stock cold-deploys it, choudoufu adopts it from stock's
#      state and runs every day-2 stage on it, tears it down, then applies
#      the same shape fresh with a live block (greenfield).
#   B  the oracle. Stock cold-deploys the identical shape and applies every
#      day-2 change itself.
#
# A stage this script cannot pass records fail with the reason and the run
# continues; the runner decides what "clear" means. A fault in the
# substrate itself is a fail on the stage being set up and an exit.
#
#   go run ./tools/gauntlet run reference-k8s-helm-ingress-nginx
#   bash live/e2e/reference-k8s-helm-ingress-nginx/run.sh
#
# Needs kind, kubectl, terraform (the stock oracle), Docker, python3, jq and
# curl. Network: the chart archive once (then cached under
# $CHOUDOUFU_CHART_CACHE, default ~/.cache/choudoufu/charts, and re-checked
# against its sha256 on every run) and one public image pull per cluster
# (registry.k8s.io/ingress-nginx/controller:v1.15.1, by digest).
#
# Env overrides:
#   TOFU_BIN       path to a prebuilt choudoufu binary; skips the go build.
#   CHOUDOUFU_CHART_CACHE
#                  where the verified chart archive is kept between runs.
#   BREAK          run drift_reconverge's and greenfield's negative
#                  controls instead of the real checks.
#   BREAK_REMOVE   keep the IngressClass enabled and assert no destroy is
#                  proposed.
#   BREAK_COUNT    assert the wrong alias was destroyed on the scale-down.
#   BREAK_APPROVAL apply the saved plan after the world moved and expect
#                  success.
#   BREAK_REPLACE  see live/e2e/lib/gauntlet.sh's gauntlet_kind_day2_replace.
#   BREAK_CRASH    assert, after the same real interrupt, that nothing is
#                  proposed; must fail, because a recovered run proposes
#                  the remainder.
#   BREAK_CRASH_UNBOUND
#                  strip the tofu-estate label off the Ingress the
#                  interrupted apply did create before replanning. The real
#                  check must then fail to hold.
#   BREAK_STRICT   turn secrets back to "store" and require the refusal to
#                  vanish.
#   BREAK_NO_LOCAL_STATE
#                  strip one declared object's tofu-estate label before
#                  no_local_state's plan with no local state; that plan must
#                  then propose a create, so the check fails.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$ROOT/live/e2e/lib/gauntlet.sh"

# The shared provider plugin cache, and the cross-process lock real terraform
# needs in order to use it safely (#1300). live/e2e/lib/gauntlet.sh carries the
# measured reasons for both; this is the only place a script chooses either.
gauntlet_plugin_cache
ESTATE="reference-k8s-helm-ingress-nginx"
NS="ingress-nginx"
RELEASE="rel"
FULL="$RELEASE-ingress-nginx"   # the chart's fullname for this release
CTRL="$FULL-controller"         # the controller's ConfigMap, Service and Deployment

# The chart, pinned by digest. root/main.tf reads it from charts/ beside
# itself; write_root copies the verified archive there.
CHART_VERSION="4.15.1"
CHART_FILE="ingress-nginx-$CHART_VERSION.tgz"
CHART_URL="https://github.com/kubernetes/ingress-nginx/releases/download/helm-chart-$CHART_VERSION/$CHART_FILE"
CHART_SHA256="3eff0bd18151d6e6b1c441463410571443dda1ac78292cb189346628de784f0c"
CHART_CACHE="${CHOUDOUFU_CHART_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/choudoufu/charts}"

# The estate's kinds, for the label count. ReplicaSets, Pods,
# EndpointSlices, Endpoints and Leases are deliberately absent: the root
# declares none, and the cluster's own are counted separately (copies_a).
KINDS="namespaces serviceaccounts configmaps clusterroles clusterrolebindings roles rolebindings services deployments ingressclasses ingresses"
REST_N=8      # kubernetes_manifest.rest: the render minus its Deployment
LATE_N=1      # kubernetes_manifest.late: the controller's Deployment
ING_N=1       # kubernetes_manifest.ingresses: one per rendered IngressClass
MANIFEST_N=$((REST_N + LATE_N + ING_N))
TOTAL_N=$((1 + MANIFEST_N))   # 11 managed instances, the Namespace and 10 kubernetes_manifest
CLASS_N=2     # objects ingressClassResource.enabled=false takes out: the IngressClass and its Ingress
REST_BLOCK="rest"   # day2_rename renames it; every address helper reads this
WORK="$(mktemp -d)"
STOCK="$WORK/stock"; ADOPTED="$WORK/adopted"; ORACLE="$WORK/oracle"; GREEN="$WORK/green"
KCA="$WORK/a.kubeconfig"; KCB="$WORK/b.kubeconfig"
CLUSTER_A="${GAUNTLET_KIND_PREFIX:-chdf}-refhin-a-$$"; CLUSTER_B="${GAUNTLET_KIND_PREFIX:-chdf}-refhin-b-$$"  # GAUNTLET_KIND_PREFIX: lets concurrent workers name their own clusters
export TF_IN_AUTOMATION=1
log() { printf '%s\n' "$*"; }

CURRENT_STAGE=""
fail() {
  printf 'FAIL: %s\n' "$*" >&2
  if [ -n "$CURRENT_STAGE" ]; then gauntlet_stage "$CURRENT_STAGE" fail "$*"; fi
  exit 1
}
cleanup() {
  gauntlet_sides_stop
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
command -v jq >/dev/null 2>&1 || fail "jq is not on PATH"
command -v curl >/dev/null 2>&1 || fail "curl is not on PATH"
[ -f "$HERE/root/main.tf" ] || fail "$HERE/root/main.tf is missing"
[ -f "$HERE/root/values.yaml" ] || fail "$HERE/root/values.yaml is missing"

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1
  else shasum -a 256 "$1" | cut -d' ' -f1; fi
}
mkdir -p "$CHART_CACHE" || fail "could not create the chart cache $CHART_CACHE"
CHART="$CHART_CACHE/$CHART_FILE"
if [ ! -f "$CHART" ] || [ "$(sha256_of "$CHART")" != "$CHART_SHA256" ]; then
  rm -f "$CHART"
  curl -fsSL -o "$CHART.part" "$CHART_URL" || fail "could not fetch $CHART_URL"
  mv "$CHART.part" "$CHART" || fail "could not move the fetched chart into $CHART_CACHE"
fi
CHART_GOT="$(sha256_of "$CHART")"
[ "$CHART_GOT" = "$CHART_SHA256" ] || fail "the chart archive at $CHART reads sha256 $CHART_GOT, want $CHART_SHA256 (from $CHART_URL); refusing to render a chart that is not the pinned one"
log "  chart: $CHART_FILE, sha256 $CHART_GOT, from $CHART_URL"

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

# day2_crash needs a build with e2eTestingFeatures set so that the engine's
# own TOFU_E2E_APPLY_RESOURCE_INTERRUPT hook is reachable; see
# reference-k8s-cert-manager/run.sh for why it is built from this tree
# unconditionally.
mkdir -p "$WORK/bin"
TOFU_CRASH="$WORK/bin/choudoufu-e2e"
( cd "$ROOT" && env -u PWD go build -ldflags="-X 'main.e2eTestingFeatures=yes'" -o "$TOFU_CRASH" ./cmd/choudoufu ) \
  || fail "go build -ldflags e2eTestingFeatures ./cmd/choudoufu failed"
log "  built $TOFU_CRASH (e2eTestingFeatures=yes, for day2_crash's interrupt)"

# ── the shape ────────────────────────────────────────────────────────────
# Both provider requirements are live/oracle-versions.json's, read once
# behind one fail each (#1252, #1963).
K8S_REQUIRED_PROVIDER="$(gauntlet_kubernetes_required_provider)" \
  || fail "could not read the hashicorp/kubernetes pin from live/oracle-versions.json"
HELM_REQUIRED_PROVIDER="$(gauntlet_helm_required_provider)" \
  || fail "could not read the hashicorp/helm pin from live/oracle-versions.json"

# The committed root is what runs: every working copy is a plain cp of
# root/main.tf and root/values.yaml plus the verified chart archive, and a
# versions.tf this script writes (only choudoufu's side gets a live block).
versions_block() { # $1 = "live" for the live block, anything else for stock
  cat <<EOF
terraform {
  required_providers {
$K8S_REQUIRED_PROVIDER
$HELM_REQUIRED_PROVIDER
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

# Configured for helm_template only, which renders client-side and never
# reaches the cluster.
provider "helm" {}
EOF
}
write_root() { # $1 dir, $2 live|stock
  mkdir -p "$1/charts" || return 1
  cp "$HERE/root/main.tf" "$HERE/root/values.yaml" "$1/" || return 1
  cp "$CHART" "$1/charts/$CHART_FILE" || return 1
  versions_block "$2" > "$1/versions.tf"
}

# set_value <dir> <exact old line> <exact new line>: a values.yaml edit, the
# way a chart user makes a day-2 change. Exact match, once, or fail.
set_value() {
  python3 - "$1/values.yaml" "$2" "$3" <<'PY'
import sys
p, old, new = sys.argv[1], sys.argv[2], sys.argv[3]
s = open(p).read()
assert s.count(old + "\n") == 1, "values.yaml does not carry %r exactly once" % old
open(p, 'w').write(s.replace(old + "\n", new + "\n", 1))
PY
}

# set_aliases <dir> <n>: day2_count's for_each scaling. The chart renders one
# IngressClass per controller.ingressClassResource.aliases entry, named as
# the entry, and main.tf's ingresses block declares one Ingress per rendered
# IngressClass, so each entry is one more instance of the rest block and one
# more of the ingresses block. The entries are choudoufu-0 .. choudoufu-<n-1>.
set_aliases() {
  python3 - "$1/values.yaml" "$2" <<'PY'
import re, sys
p, n = sys.argv[1], int(sys.argv[2])
s = open(p).read()
pat = re.compile(r"(?m)^    aliases: .*$")
assert len(pat.findall(s)) == 1, "values.yaml does not carry one ingressClassResource aliases line"
names = ", ".join("choudoufu-%d" % k for k in range(n))
open(p, 'w').write(pat.sub("    aliases: [%s]" % names, s, count=1))
PY
}

# rename_rest <dir>: the block rename day2_rename's moved block covers. Every
# reference moves with it (late's and ingresses' depends_on name it).
rename_rest() {
  python3 - "$1/main.tf" <<'PY'
import sys
p = sys.argv[1]; s = open(p).read()
old_block = 'resource "kubernetes_manifest" "rest" {'
assert s.count(old_block) == 1
s = s.replace(old_block, 'resource "kubernetes_manifest" "chart_objects" {', 1)
assert s.count('kubernetes_manifest.rest') == 2, "late's and ingresses' depends_on are not the two other references this edit expects"
s = s.replace('kubernetes_manifest.rest', 'kubernetes_manifest.chart_objects')
s += '''
moved {
  from = kubernetes_manifest.rest
  to   = kubernetes_manifest.chart_objects
}
'''
open(p, 'w').write(s)
PY
}

# crash_block <first|second>: day2_crash's two extra Ingresses, written to
# crash.tf in a root that already holds the estate. Both are of the class
# the render installs, so the controller admits them; crash_second's
# manifest reads crash_first's name, a real edge in the graph, so nothing
# can create crash-second until crash-first has committed (#490's
# discipline; reference-k8s-cert-manager's crash pair, #1262).
crash_block() {
  if [ "$1" = "first" ]; then
    cat <<EOF

resource "kubernetes_manifest" "crash_first" {
  manifest = {
    "apiVersion" = "networking.k8s.io/v1"
    "kind"       = "Ingress"
    "metadata" = {
      "name"      = "crash-first"
      "namespace" = "$NS"
    }
    "spec" = {
      "ingressClassName" = "nginx"
      "rules" = [{
        "host" = "crash-first.choudoufu.test"
        "http" = {
          "paths" = [{
            "path"     = "/"
            "pathType" = "Prefix"
            "backend"  = { "service" = { "name" = "$CTRL", "port" = { "name" = "http" } } }
          }]
        }
      }]
    }
  }

  depends_on = [kubernetes_namespace_v1.ingress_nginx]
}
EOF
  else
    cat <<EOF

resource "kubernetes_manifest" "crash_second" {
  manifest = {
    "apiVersion" = "networking.k8s.io/v1"
    "kind"       = "Ingress"
    "metadata" = {
      "annotations" = {
        "after" = kubernetes_manifest.crash_first.manifest.metadata.name
      }
      "name"      = "crash-second"
      "namespace" = "$NS"
    }
    "spec" = {
      "ingressClassName" = "nginx"
      "rules" = [{
        "host" = "crash-second.choudoufu.test"
        "http" = {
          "paths" = [{
            "path"     = "/"
            "pathType" = "Prefix"
            "backend"  = { "service" = { "name" = "$CTRL", "port" = { "name" = "http" } } }
          }]
        }
      }]
    }
  }
}
EOF
  fi
}

# ── cluster helpers ──────────────────────────────────────────────────────
kca() { kubectl --kubeconfig "$KCA" "$@"; }
kcb() { kubectl --kubeconfig "$KCB" "$@"; }
tofu_a() { ( cd "$ADOPTED" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" "$TOFU" "$@" ); }
stock_a() { ( cd "$STOCK"  && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" terraform "$@" ); }
stock_b() { ( cd "$ORACLE" && KUBECONFIG="$KCB" KUBE_CONFIG_PATH="$KCB" terraform "$@" ); }
exists_a() { kca get "$1" "$2" -n "$NS" >/dev/null 2>&1; }
# quiet <command...>: runs it with its output captured and prints the last
# 30 lines only when it fails, so every failure below is diagnosable from
# the log alone (metallb's stock apply failure printed nothing).
quiet() {
  local out rc
  out="$("$@" 2>&1)"; rc=$?
  [ "$rc" -eq 0 ] || printf '%s\n' "$out" | tail -30
  return "$rc"
}
# managed_n <stock fn>: managed instances in that side's state; the
# helm_template data source sits in state too and is not an object.
managed_n() { "$1" state list 2>/dev/null | grep -vc '^data\.'; }

# count_a: objects of the estate's kinds carrying tofu-estate=$ESTATE that
# NO controller owns. The EndpointSlice controller copies a Service's labels
# onto the slice it writes, owned by the Service; KINDS leaves that kind out
# anyway, and the ownerReference filter keeps the count to declared objects
# should a kind in KINDS ever gain such a copy. copies_a counts the other
# side of the line.
count_a() {
  local n=0 k c
  for k in $KINDS; do
    c="$(kca get "$k" -A -l "tofu-estate=$ESTATE" -o json 2>/dev/null | jq '[.items[]? | select((.metadata.ownerReferences // []) | length == 0)] | length' 2>/dev/null)"
    n=$((n + ${c:-0}))
  done
  printf '%s\n' "$n"
}
# copies_a: what the cluster generated in $NS beside the declared objects,
# as kind/name lines - the objects the root never declared and the sweep must
# never propose. Owned ReplicaSets and EndpointSlices, plus every Endpoints
# object and every Lease (the Endpoints controller and the controller's
# leader election set no ownerReference). Pods are left out: plan_approval
# scales the Deployment to two and back, and the second pod can still be
# terminating, under the chart's 300s grace period, when day2_remove reads
# this list.
copies_a() {
  {
    kca get replicasets,endpointslices -n "$NS" -o json 2>/dev/null \
      | jq -r '.items[]? | select((.metadata.ownerReferences // []) | length > 0) | "\(.kind)/\(.metadata.name)"'
    kca get endpoints,leases -n "$NS" -o json 2>/dev/null | jq -r '.items[]? | "\(.kind)/\(.metadata.name)"'
  } 2>/dev/null | sort
}
# labelled_a: the objects count_a counts, as kind/namespace/name lines, for
# a failure line.
labelled_a() {
  local k
  for k in $KINDS; do
    kca get "$k" -A -l "tofu-estate=$ESTATE" -o json 2>/dev/null | jq -r '.items[]? | select((.metadata.ownerReferences // []) | length == 0) | "\(.kind)/\(.metadata.namespace // "")/\(.metadata.name)"' 2>/dev/null
  done | tr '\n' ' '
}
# copies_labelled_a: how many of those carry the estate's label (the
# EndpointSlice and Endpoints copy the Service's labels).
copies_labelled_a() {
  kca get endpointslices,endpoints -n "$NS" -l "tofu-estate=$ESTATE" -o name 2>/dev/null | grep -c .
}

# status_ip <kubeconfig> <ingress>: the address the controller published
# into the Ingress' status; svc_ip <kubeconfig>: the controller Service's
# cluster IP, which is what --publish-service publishes for a ClusterIP
# Service.
status_ip() { kubectl --kubeconfig "$1" get ingress "$2" -n "$NS" -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null; }
svc_ip() { kubectl --kubeconfig "$1" get service "$CTRL" -n "$NS" -o jsonpath='{.spec.clusterIP}' 2>/dev/null; }
status_published() {
  local ip want
  ip="$(status_ip "$1" "$2")"; want="$(svc_ip "$1")"
  [ -n "$ip" ] && [ "$ip" = "$want" ]
}

# nginx_ready <kubeconfig> <label>: bounded and loud. The controller's
# Deployment Available, then the controller has admitted the estate's
# Ingress via-nginx and written the Service's cluster IP into its status.
nginx_ready() {
  local cfg="$1" label="$2"
  gauntlet_k8s_wait_all "$cfg" "$NS" deployment Available 300 "$label" || return 1
  gauntlet_wait_until 240 "the controller on $label to publish $CTRL's cluster IP into Ingress via-nginx's status" -- status_published "$cfg" via-nginx
}

# block_addr <block> <key>: one instance address; rest_addr <kind> <namespace>
# <name>: one instance of the rest block, under whatever name it has now.
block_addr() { printf 'kubernetes_manifest.%s["%s"]' "$1" "$2"; }
rest_addr() { block_addr "$REST_BLOCK" "$1/$2/$3"; }
# planned <plan>: the addresses a plan proposes something for, one per line.
planned() { grep -E '^[[:space:]]*# .+ (will be|must be)' <<< "$1" | sed -E 's/^[[:space:]]*# //; s/ (will be|must be).*$//'; }
# destroyed <plan>, created <plan>: the addresses a plan destroys or creates,
# sorted, one per line; joined <lines>: the same on one line, "; "-separated.
destroyed() { { grep -E '^[[:space:]]*# .+ will be destroyed' <<< "$1" || true; } | sed -E 's/^[[:space:]]*# //; s/ will be destroyed.*$//' | sort; }
created() { { grep -E '^[[:space:]]*# .+ will be created' <<< "$1" || true; } | sed -E 's/^[[:space:]]*# //; s/ will be created.*$//' | sort; }
joined() { tr '\n' ';' <<< "$1" | sed -E 's/;+$//; s/;/; /g'; }
# plan_lines <plan>: the plan's summary and every action header, indented,
# for the log - what a stage that compares addresses prints whatever it
# decides, so a mismatch is readable from the log alone.
plan_lines() { { grep -E '^Plan:|^No changes|^[[:space:]]*# .+ (will be|must be)' <<< "$1" || true; } | sed -E 's/^[[:space:]]*/    /'; }
# objects_of: the addresses on stdin as the objects they name, one
# "kind|orphan_<ns>_<name>" line each, sorted - independent of the address
# a plan files the object under. A kubernetes_manifest instance keyed
# kind/namespace/name, an instance of the root's ingresses block (keyed by
# its IngressClass, the Ingress via-<key> in $NS), and the sweep's two orphan
# spellings (#1996: <typed resource>.orphan_[<ns>_]<name>, and
# kubernetes_manifest.orphan_<kind>_[<ns>_]<name>, kubesweep's
# OrphanResourceName and ManifestOrphanResourceName) all come out the same
# for the same object. Anything else is printed as "?|<address>", which
# never matches.
objects_of() {
  python3 -c '
import re, sys
ns = sys.argv[1]
def orphan(n, name):
    return re.sub(r"[^A-Za-z0-9_-]", "_", "orphan_" + (n + "_" if n else "") + name)
out = []
for a in sys.stdin.read().splitlines():
    a = a.strip()
    if not a:
        continue
    m = re.match(r"^kubernetes_manifest\.ingresses\[\"([^\"]+)\"\]$", a)
    if m:
        out.append("ingress|" + orphan(ns, "via-" + m.group(1))); continue
    m = re.match(r"^kubernetes_manifest\.[A-Za-z0-9_-]+\[\"([^/\"]+)/([^/\"]*)/([^\"]+)\"\]$", a)
    if m:
        out.append(m.group(1).lower() + "|" + orphan(m.group(2), m.group(3))); continue
    m = re.match(r"^kubernetes_manifest\.orphan_([a-z0-9]+)_(.+)$", a)
    if m:
        out.append(m.group(1) + "|orphan_" + m.group(2)); continue
    m = re.match(r"^kubernetes_([a-z0-9_]+?)(_v[0-9]+)?\.(orphan_.+)$", a)
    if m:
        out.append(m.group(1).replace("_", "") + "|" + m.group(3)); continue
    out.append("?|" + a)
print("\n".join(sorted(out)))
' "$NS"
}

# ── 1. cold_deploy: one apply, no pre-apply ──────────────────────────────
gauntlet_begin_stage cold_deploy
log "=== 1. cold_deploy: two kind clusters; the whole root in one plain apply on each ==="
gauntlet_side_start kind_b gauntlet_kind_up "$CLUSTER_B" "$KCB"  # B comes up while A does (#2037)
gauntlet_kind_up "$CLUSTER_A" "$KCA" || fail "kind cluster A ($CLUSTER_A) did not come up"
gauntlet_side_wait kind_b || fail "kind cluster B ($CLUSTER_B) did not come up"
export KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA"
K8S_VER="$(kca version 2>/dev/null | gauntlet_k8s_server_version)"
log "  cluster A: $CLUSTER_A (kubernetes $K8S_VER); cluster B: $CLUSTER_B"
write_root "$STOCK" stock  || fail "could not write the stock root on A"
write_root "$ORACLE" stock || fail "could not write the oracle root on B"
quiet stock_a init -input=false -no-color || fail "stock init failed on A"
gauntlet_report_lock "$STOCK"  # the versions stage 1 resolved, for the row (#1739)
quiet stock_b init -input=false -no-color || fail "stock init failed on B"

# Stock's apply and rollout on B, the oracle, run while A's do (#2037): its
# own root and kubeconfig, joined where B's wait used to run.
oracle_cold_deploy() {
  export KUBECONFIG="$KCB" KUBE_CONFIG_PATH="$KCB"
  quiet stock_b apply -auto-approve -input=false -no-color || fail "stock's apply failed on B"
  nginx_ready "$KCB" "cluster B" || fail "ingress-nginx never became ready on B"
}
gauntlet_side_start oracle_cold oracle_cold_deploy
COLD_OUT="$(stock_a apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$COLD_OUT" | tail -20; fail "stock's apply failed on A"; }
grep -qF "Apply complete! Resources: $TOTAL_N added, 0 changed, 0 destroyed" <<< "$COLD_OUT" || { printf '%s\n' "$COLD_OUT" | tail -5; fail "stock's apply on A did not add exactly the $TOTAL_N declared instances in one pass"; }

[ -f "$STOCK/terraform.tfstate" ] || fail "stock left no terraform.tfstate on A"
STOCK_N="$(managed_n stock_a)"
[ "$STOCK_N" = "$TOTAL_N" ] || fail "stock's state holds $STOCK_N managed instances, want $TOTAL_N"
nginx_ready "$KCA" "cluster A" || fail "ingress-nginx never became ready on A"
gauntlet_side_wait oracle_cold || fail "$GAUNTLET_SIDE_FAIL"
UNMARKED="$(count_a)"
[ "$UNMARKED" = "0" ] || fail "$UNMARKED object(s) already carry tofu-estate=$ESTATE after a plain stock apply - this proves nothing"
STOCK_REPLAN="$(stock_a plan -detailed-exitcode -input=false -no-color 2>&1)"; STOCK_REPLAN_RC=$?
[ "$STOCK_REPLAN_RC" -eq 0 ] || { printf '%s\n' "$STOCK_REPLAN" | tail -20; fail "stock's own replan after its cold deploy is not empty (exit $STOCK_REPLAN_RC) - a provider/controller fight in the shape, which would be blamed on choudoufu later"; }
STATUS_IP="$(status_ip "$KCA" via-nginx)"
COPIES="$(copies_a | tr '\n' ';' | sed -E 's/;$//; s/;/; /g')"
log "  $TOTAL_N objects on A, via-nginx status $STATUS_IP, cluster-created: $COPIES"
gauntlet_stage cold_deploy pass "$TOTAL_N objects from ONE plain terraform apply against kind $K8S_VER, no pre-apply: ingress-nginx $CHART_VERSION (sha256 $CHART_SHA256) rendered by data.helm_template.ingress_nginx into $((REST_N + LATE_N)) kubernetes_manifest instances over 9 kinds under two for_each blocks keyed kind/namespace/name (the render, then its Deployment after the RBAC, ConfigMap, Service and IngressClass it needs), $ING_N Ingress from the root's own block keyed by the rendered IngressClass, plus the Namespace; a real terraform.tfstate with $TOTAL_N managed instances, zero tofu-estate labels read back with kubectl, the controller's Deployment Available and the controller's own write of the Service's cluster IP ($STATUS_IP) into Ingress via-nginx's status, and stock's own replan empty with that status present. The identical shape cold-deployed by stock on a second cluster as every later stage's oracle. Cluster-created objects the root never declared: $COPIES"

# ── 2. migrate: live-import against stock's state ────────────────────────
gauntlet_begin_stage migrate
log "=== 2. migrate: live-import against the stock state file, read-only then -approve ==="
write_root "$ADOPTED" live || fail "could not write the adopted root"
quiet tofu_a init -input=false -no-color || fail "adopted init failed"
IMPORT_OUT="$(tofu_a live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -no-color 2>&1)" || { printf '%s\n' "$IMPORT_OUT" | tail -20; fail "live-import (dry run) failed"; }
ELIGIBLE_LINE="$(grep -E 'resource instance\(s\) are eligible for stamping' <<< "$IMPORT_OUT" | head -1)"
log "  dry run: ${ELIGIBLE_LINE:-no eligibility line}"
APPROVE_OUT="$(tofu_a live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -approve -no-color 2>&1)" || { printf '%s\n' "$APPROVE_OUT" | tail -20; fail "live-import -approve failed"; }
SUMMARY_LINE="$(grep -E 'resource\(s\) newly stamped' <<< "$APPROVE_OUT" | head -1 | sed 's/\.$//')"
log "  approve: ${SUMMARY_LINE:-no summary line}"
if grep -qF "$TOTAL_N resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 0 skipped" <<< "$APPROVE_OUT"; then
  LABELLED="$(count_a)"
  COPIES_LABELLED="$(copies_labelled_a)"
  if [ "$LABELLED" = "$TOTAL_N" ]; then
    gauntlet_stage migrate pass "$TOTAL_N of $TOTAL_N stamped, 0 skipped (${SUMMARY_LINE:-no summary line}); every declared object carries tofu-estate=$ESTATE, counted back with kubectl across all 11 kinds including the three cluster-scoped objects and the root's own Ingress. EndpointSlice and Endpoints objects carrying the label after the stamp, by the cluster's own copying from the Service it was stamped on: ${COPIES_LABELLED:-0} (not of the estate's kinds, so not in the count). The eligibility line was: ${ELIGIBLE_LINE:-none}"
  else
    gauntlet_stage migrate fail "live-import reported $TOTAL_N stamped but $LABELLED declared object(s) carry tofu-estate=$ESTATE on the cluster (cluster-generated copies carrying it: ${COPIES_LABELLED:-0})"
  fi
else
  gauntlet_stage migrate fail "live-import -approve did not bind every instance: ${SUMMARY_LINE:-no summary line}"
fi

# ── 3. test_plan: replan from nothing ────────────────────────────────────
gauntlet_begin_stage test_plan
log "=== 3. test_plan: choudoufu plan with no state file, identities read with kubectl ==="
PLAN_OUT="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$PLAN_OUT" | tail -20; fail "the post-migration plan failed"; }
# The two name groups: one name across five kinds (two cluster-scoped), one
# across three; every one of them must be there as its own object.
IDS_OK=1; IDS_MISSING=""
for spec in "serviceaccount $NS $FULL" "clusterrole - $FULL" "clusterrolebinding - $FULL" "role $NS $FULL" "rolebinding $NS $FULL" "configmap $NS $CTRL" "service $NS $CTRL" "deployment $NS $CTRL" "ingressclass - nginx" "ingress $NS via-nginx"; do
  read -r kind where name <<< "$spec"
  if [ "$where" = "-" ]; then
    kca get "$kind" "$name" >/dev/null 2>&1 || { IDS_OK=0; IDS_MISSING="$IDS_MISSING $kind/$name"; }
  else
    exists_a "$kind" "$name" || { IDS_OK=0; IDS_MISSING="$IDS_MISSING $NS/$kind/$name"; }
  fi
done
if grep -q "No changes." <<< "$PLAN_OUT" && [ "$IDS_OK" = "1" ]; then
  gauntlet_stage test_plan pass "the plan with no state file is empty over $TOTAL_N instances whose for_each keys and identities come from the helm_template render the data-read phase read - the root's own Ingress block included, keyed by the rendered IngressClass; $FULL, one name across five kinds (ServiceAccount, ClusterRole, ClusterRoleBinding, Role, RoleBinding), and $CTRL, one name across three (ConfigMap, Service, Deployment), confirmed present as eight objects with kubectl, with IngressClass nginx and Ingress via-nginx"
else
  PLAN_LINE="$(grep -E '^Plan:|No changes' <<< "$PLAN_OUT" | head -1 | sed 's/\.$//')"
  gauntlet_stage test_plan fail "the plan with no state file is not empty (${PLAN_LINE:-no plan line}) or an identity is missing (identities confirmed: $IDS_OK;${IDS_MISSING:- none missing}). Proposed: $(planned "$PLAN_OUT" | head -10 | tr '\n' ';')"
  printf '%s\n' "$PLAN_OUT" | tail -30
fi

# ── 4. test_apply: no-op apply ───────────────────────────────────────────
gauntlet_begin_stage test_apply
log "=== 4. test_apply: apply the empty plan; the labelled-object count must not move ==="
BEFORE_N="$(count_a)"
NOOP_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$NOOP_OUT" | tail -20; fail "the no-op apply failed"; }
grep -qF "Apply complete! Resources: 0 added, 0 changed, 0 destroyed" <<< "$NOOP_OUT" || { printf '%s\n' "$NOOP_OUT" | tail -5; fail "the no-op apply changed something"; }
AFTER_N="$(count_a)"
[ "$BEFORE_N" = "$AFTER_N" ] || fail "labelled-object count moved across a no-op apply: $BEFORE_N -> $AFTER_N"
status_published "$KCA" via-nginx || fail "Ingress via-nginx's status no longer reads the Service's cluster IP after the no-op apply"
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed) over $TOTAL_N instances; declared objects carrying tofu-estate=$ESTATE unchanged at $BEFORE_N across the estate's 11 kinds, counted with kubectl, and the controller-written status of Ingress via-nginx still the Service's cluster IP"

# ── 5. drift_reconverge: one object out of a five-kind name group ────────
gauntlet_begin_stage drift_reconverge
DRIFT_ADDR="$(rest_addr RoleBinding "$NS" "$FULL")"
log "=== 5. drift_reconverge: the RoleBinding $FULL is deleted out of band on A and on B ==="
# A delete, not a patch: every object here is server-side applied, and a
# kubectl patch makes kubectl a field manager (reference-k8s-cert-manager
# measured the conflict on both sides). The deleted object shares its name
# with four objects of other kinds, so the plan has to name it by kind.
kca delete rolebinding "$FULL" -n "$NS" >/dev/null || fail "could not delete the RoleBinding out of band on A"
kcb delete rolebinding "$FULL" -n "$NS" >/dev/null || fail "could not delete the RoleBinding out of band on B"
if [ "${BREAK:-}" = "1" ]; then
  kca delete role "$FULL" -n "$NS" >/dev/null || fail "BREAK: could not delete a second object on A"
fi
ORACLE_PLAN="$(stock_b plan -detailed-exitcode -input=false -no-color 2>&1)"; ORACLE_RC=$?
[ "$ORACLE_RC" -eq 2 ] || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B after the out-of-band delete exited $ORACLE_RC, want 2 (changes)"; }
grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$ORACLE_PLAN" || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B does not propose exactly one create"; }
grep -qF "$DRIFT_ADDR" <<< "$ORACLE_PLAN" || fail "stock's plan on B does not name $DRIFT_ADDR"
quiet stock_b apply -auto-approve -input=false -no-color || fail "stock could not reconverge B"
DRIFT_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$DRIFT_PLAN" | tail -20; fail "the plan after the out-of-band delete failed"; }
if [ "${BREAK:-}" = "1" ]; then
  if grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$DRIFT_PLAN"; then
    fail "BREAK=1: two objects were deleted but the plan still proposes exactly one create - the single-object assertion is not load-bearing"
  fi
  log "  BREAK=1: caught - with a second object deleted the plan is $(grep -E '^Plan:' <<< "$DRIFT_PLAN" | head -1)"
  quiet tofu_a apply -auto-approve -input=false -no-color || fail "BREAK: could not reconverge A"
  gauntlet_stage drift_reconverge pass "BREAK=1 control: with the same-named Role deleted too the single-object assertion correctly fails to hold ($(grep -E '^Plan:' <<< "$DRIFT_PLAN" | head -1)); reconverged afterwards"
else
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$DRIFT_PLAN" || { printf '%s\n' "$DRIFT_PLAN" | tail -20; fail "the plan after one out-of-band delete does not propose exactly one create"; }
  D_PLANNED="$(planned "$DRIFT_PLAN")"
  [ "$D_PLANNED" = "$DRIFT_ADDR" ] || { printf '%s\n' "$D_PLANNED"; fail "the plan does not propose exactly $DRIFT_ADDR: $(tr '\n' ';' <<< "$D_PLANNED")"; }
  RECONV="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$RECONV" | tail -20; fail "the reconverging apply failed"; }
  grep -qF "Apply complete! Resources: 1 added, 0 changed, 0 destroyed" <<< "$RECONV" || { printf '%s\n' "$RECONV" | tail -10; fail "the reconverging apply did not create exactly one object"; }
  exists_a rolebinding "$FULL" || fail "the RoleBinding does not exist after reconverging"
  [ "$(count_a)" = "$TOTAL_N" ] || fail "$(count_a) labelled objects after reconverging, want $TOTAL_N - the recreated object did not get its marker"
  gauntlet_stage drift_reconverge pass "a RoleBinding that shares its name ($FULL) with four objects of other kinds, two of them cluster-scoped, was deleted out of band with kubectl; choudoufu proposed putting back exactly $DRIFT_ADDR (1 add, 0 change, 0 destroy) and nothing for the four same-named objects, matching stock's own plan on the oracle cluster; apply created 1 and the recreated object carries the estate label again ($TOTAL_N labelled). A delete rather than a patch because every object is server-side applied. BREAK=1 deletes the same-named Role as well and the single-object assertion correctly fails"
fi

# ── 6. plan_approval: plan -out, the world moves, apply refuses ──────────
gauntlet_begin_stage plan_approval
log "=== 6. plan_approval: a values edit saved as a plan, an out-of-band move, a refusal; then the same file applies ==="
CM_ADDR="$(rest_addr ConfigMap "$NS" "$CTRL")"
body_size_a() { kca get configmap "$CTRL" -n "$NS" -o jsonpath='{.data.proxy-body-size}'; }
set_value "$ADOPTED" "    proxy-body-size: 1m" "    proxy-body-size: 8m" || fail "could not edit proxy-body-size in the adopted root"
set_value "$ORACLE"  "    proxy-body-size: 1m" "    proxy-body-size: 8m" || fail "could not edit proxy-body-size in the oracle root"
P_PLAN="$(tofu_a plan -out=approved.tfplan -input=false -no-color 2>&1)"; P_PLAN_RC=$?
P_PLAN_LINE="$(grep -E '^Plan:|^No changes' <<< "$P_PLAN" | head -1 | sed 's/\.$//')"
if [ "$P_PLAN_RC" -ne 0 ] || ! grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$P_PLAN" || ! grep -qF "$CM_ADDR will be updated in-place" <<< "$P_PLAN"; then
  gauntlet_stage plan_approval fail "controller.config.proxy-body-size 1m -> 8m in values.yaml did not plan as exactly one in-place update of $CM_ADDR: ${P_PLAN_LINE:-no summary line} (exit $P_PLAN_RC). What it proposed: $(planned "$P_PLAN" | head -10 | tr '\n' ';')"
  printf '%s\n' "$P_PLAN" | tail -30
  quiet tofu_a apply -auto-approve -input=false -no-color || fail "could not converge A after plan_approval; nothing below would measure day-2 behaviour"
  quiet stock_b apply -auto-approve -input=false -no-color || fail "could not converge B after plan_approval; the oracle would be stale for every stage below"
  PLAN_APPROVAL_SKIPPED=1
fi
if [ -z "${PLAN_APPROVAL_SKIPPED:-}" ]; then
# The world moves on a spec-side field of an object the saved plan does not
# touch: the controller's Deployment declares replicas: 1, and it is put
# back below.
kca patch deployment "$CTRL" -n "$NS" --type merge -p '{"spec":{"replicas":2}}' >/dev/null || fail "could not move the world (the controller's replicas) on A"
P_APPLY="$(tofu_a apply -input=false -no-color approved.tfplan 2>&1)"; P_RC=$?
if [ "${BREAK_APPROVAL:-}" = "1" ]; then
  [ "$P_RC" -eq 0 ] && fail "BREAK_APPROVAL=1: applying the saved plan after the world moved succeeded - the refusal is not load-bearing"
  log "  BREAK_APPROVAL=1: caught - the apply after the world moved exited $P_RC"
  kca patch deployment "$CTRL" -n "$NS" --type merge -p '{"spec":{"replicas":1}}' >/dev/null
  quiet tofu_a apply -input=false -no-color approved.tfplan || fail "BREAK_APPROVAL: the saved plan did not apply once the world was put back"
  quiet stock_b apply -auto-approve -input=false -no-color || fail "BREAK_APPROVAL: stock could not apply the change on B"
  gauntlet_stage plan_approval pass "BREAK_APPROVAL=1 control: applying the saved plan after the world moved exited $P_RC (refused), so the stage's own Break line correctly fails; applied once the world was put back"
else
  if [ "$P_RC" -ne 3 ] || ! grep -qF "The approved plan no longer matches the live system" <<< "$P_APPLY"; then
    printf '%s\n' "$P_APPLY" | tail -20
    gauntlet_stage plan_approval fail "the saved plan was applied after the world had moved out of band (the controller Deployment's replicas 1 -> 2, kubectl) and choudoufu exited $P_RC rather than refusing at 3 with \"The approved plan no longer matches the live system\". The saved plan's own change was one in-place update of $CM_ADDR from a values edit"
    kca patch deployment "$CTRL" -n "$NS" --type merge -p '{"spec":{"replicas":1}}' >/dev/null
    quiet tofu_a apply -auto-approve -input=false -no-color || fail "could not converge A after plan_approval; nothing below would measure day-2 behaviour"
    quiet stock_b apply -auto-approve -input=false -no-color || fail "could not converge B after plan_approval; the oracle would be stale for every stage below"
    PLAN_APPROVAL_SKIPPED=1
  fi
  if [ -z "${PLAN_APPROVAL_SKIPPED:-}" ]; then
  BODY_NOW="$(body_size_a)"
  [ "$BODY_NOW" = "1m" ] || fail "the controller ConfigMap's proxy-body-size reads $BODY_NOW despite the refusal, want 1m"
  kca patch deployment "$CTRL" -n "$NS" --type merge -p '{"spec":{"replicas":1}}' >/dev/null || fail "could not put the world back"
  P_APPLY2="$(tofu_a apply -input=false -no-color approved.tfplan 2>&1)" || { printf '%s\n' "$P_APPLY2" | tail -20; fail "the saved plan did not apply once the world was put back"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$P_APPLY2" || fail "the saved plan's apply did not change exactly one object"
  [ "$(body_size_a)" = "8m" ] || fail "the controller ConfigMap does not read proxy-body-size 8m after the saved plan applied"
  O_PA="$(stock_b plan -out=approved.tfplan -input=false -no-color 2>&1)" || fail "stock's own planfile failed on B"
  grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$O_PA" || { printf '%s\n' "$O_PA" | tail -10; fail "stock's plan for the same values edit on B is not exactly one in-place update"; }
  quiet stock_b apply -input=false -no-color approved.tfplan || fail "stock's own planfile did not apply on B"
  gauntlet_stage plan_approval pass "a values.yaml edit (controller.config.proxy-body-size 1m -> 8m) re-rendered through helm_template planned as exactly one in-place update of $CM_ADDR, saved with plan -out; the world then moved out of band (the controller Deployment's replicas to 2 with kubectl, never choudoufu) and apply of the saved plan refused with \"The approved plan no longer matches the live system\" at exit 3, nothing applied (kubectl still reads 1m); with the world put back the identical file applied, 0 added, 1 changed, 0 destroyed, and 8m reads back; stock's plan for the same edit on the oracle cluster is the same one update and its planfile applied. BREAK_APPROVAL=1 expects success after the move and correctly fails"
  fi
fi
fi

# ── 7. day2_rename: a moved block over the render's for_each ─────────────
gauntlet_begin_stage day2_rename
log "=== 7. day2_rename: kubernetes_manifest.rest becomes .chart_objects through a moved block ==="
rename_rest "$ADOPTED" || fail "could not rename the rest block in the adopted root"
rename_rest "$ORACLE"  || fail "could not rename the rest block in the oracle root"
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's moved-block plan failed on B"; }
grep -qE "^No changes|Plan: 0 to add, 0 to change, 0 to destroy" <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's moved-block plan on B is not zero churn"; }
quiet stock_b apply -auto-approve -input=false -no-color || fail "stock's moved-block apply failed on B"
R_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_PLAN" | tail -20; fail "the moved-block plan failed"; }
# The renderer aligns a map's "=" across its keys, and the Service carries
# a fixture annotation of its own, so the address line can read
# `"...address"   =` with padding: one or more spaces before the "=".
R_ANN="$(grep -cE '~ +"choudoufu\.intentius\.io/tofu-address" += ".*" -> ".*"' <<< "$R_PLAN")"
if grep -qE 'will be (created|destroyed)|must be replaced' <<< "$R_PLAN"; then
  gauntlet_stage day2_rename fail "the moved-block plan over $REST_N for_each instances keyed by the render proposes a create, a destroy or a replace - not the marker rewritten in place: $(grep -E '^Plan:' <<< "$R_PLAN" | head -1)"
  printf '%s\n' "$R_PLAN" | tail -20
  quiet tofu_a apply -auto-approve -input=false -no-color || fail "could not converge after the rename; nothing below would measure day-2 behaviour"
elif grep -qF "Plan: 0 to add, $REST_N to change, 0 to destroy." <<< "$R_PLAN" && [ "$R_ANN" = "$REST_N" ]; then
  R_APPLY_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$R_APPLY_OUT" | tail -20; fail "the moved-block apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, $REST_N changed, 0 destroyed" <<< "$R_APPLY_OUT" || { printf '%s\n' "$R_APPLY_OUT" | tail -10; fail "the moved-block apply was not exactly $REST_N in-place changes"; }
  [ "$(count_a)" = "$TOTAL_N" ] || fail "$(count_a) labelled objects after the rename, want $TOTAL_N"
  R_REPLAN="$(tofu_a plan -input=false -no-color 2>&1)" || fail "the replan after the rename failed"
  grep -q "No changes." <<< "$R_REPLAN" || { printf '%s\n' "$R_REPLAN" | tail -20; fail "the replan after the rename is not empty"; }
  gauntlet_stage day2_rename pass "moved block over a for_each of $REST_N instances keyed by the render, three of them cluster-scoped (kubernetes_manifest.rest -> .chart_objects, with late's and ingresses' depends_on following it): no add and no destroy, $REST_N in-place changes each confined to the address annotation rewrite ($R_ANN annotation lines, 0 add, $REST_N change, 0 destroy) - the marker rewritten in place, as the AWS lanes assert for a rename; every object still there and labelled ($TOTAL_N), the replan empty; stock's plan for the same moved block on the oracle cluster is zero churn, since stock never writes this annotation"
else
  gauntlet_stage day2_rename fail "the moved-block plan over $REST_N for_each instances is not exactly $REST_N in-place annotation changes: $(grep -E '^Plan:' <<< "$R_PLAN" | head -1), $R_ANN annotation line(s)"
  printf '%s\n' "$R_PLAN" | tail -20
  quiet tofu_a apply -auto-approve -input=false -no-color || fail "could not converge after the rename; nothing below would measure day-2 behaviour"
fi
REST_BLOCK="chart_objects"

# ── 8. day2_remove: a values toggle empties a chart block's key and ours ─
gauntlet_begin_stage day2_remove
CLASS_ON=$'  ingressClassResource:\n    enabled: true'
CLASS_OFF=$'  ingressClassResource:\n    enabled: false'
CLASS_ADDR="$(rest_addr IngressClass "" nginx)"
VIA_ADDR="$(block_addr ingresses nginx)"
log "=== 8. day2_remove: ingressClassResource.enabled=false removes $CLASS_N objects across two blocks; the cluster's own objects must be left alone ==="
COPIES_BEFORE="$(copies_a)"
[ -n "$COPIES_BEFORE" ] || fail "no cluster-created objects exist in $NS before the removal; the controller-copy check would measure nothing"
grep -q '^Lease/' <<< "$COPIES_BEFORE" || fail "the controller's leader-election Lease does not exist before the removal: $(tr '\n' ';' <<< "$COPIES_BEFORE")"
grep -q '^EndpointSlice/' <<< "$COPIES_BEFORE" || fail "the controller Service's EndpointSlice does not exist before the removal: $(tr '\n' ';' <<< "$COPIES_BEFORE")"
if [ "${BREAK_REMOVE:-}" = "1" ]; then
  K_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || fail "BREAK_REMOVE: the plan with the IngressClass kept failed"
  grep -qE "will be destroyed|[1-9][0-9]* to destroy" <<< "$K_PLAN" && fail "BREAK_REMOVE=1: with the IngressClass kept a destroy was still proposed - the destroys below would not be the values change's doing"
  log "  BREAK_REMOVE=1: caught - with the IngressClass kept the plan proposes no destroy"
  gauntlet_stage day2_remove pass "BREAK_REMOVE=1 control: with ingressClassResource kept enabled, no destroy is proposed; the real check is skipped"
else
  # Every way choudoufu can miss is recorded here and the stage carries on,
  # so the world is put back and every later stage is measured: one fail
  # verdict at the end with all of them. fail (which exits) is kept for what
  # leaves nothing to measure - the oracle, a command that could not run.
  REMOVE_FAILS=""
  remove_fail() { REMOVE_FAILS="${REMOVE_FAILS:+$REMOVE_FAILS; }$*"; }
  set_value "$ADOPTED" "$CLASS_ON" "$CLASS_OFF" || fail "could not disable the IngressClass in the adopted root"
  set_value "$ORACLE"  "$CLASS_ON" "$CLASS_OFF" || fail "could not disable the IngressClass in the oracle root"
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -20; fail "stock's remove plan failed on B"; }
  log "  stock's remove plan on B:"; plan_lines "$O_PLAN"
  grep -qF "Plan: 0 to add, 0 to change, $CLASS_N to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -20; fail "stock's remove plan on B is not exactly $CLASS_N destroys"; }
  O_DEST="$(destroyed "$O_PLAN")"
  { grep -qxF "$CLASS_ADDR" <<< "$O_DEST" && grep -qxF "$VIA_ADDR" <<< "$O_DEST"; } || fail "stock's remove plan on B does not destroy $CLASS_ADDR and $VIA_ADDR: [$(joined "$O_DEST")]"
  O_OBJS="$(objects_of <<< "$O_DEST")"
  quiet stock_b apply -auto-approve -input=false -no-color || fail "stock's remove apply failed on B"

  D_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$D_PLAN" | tail -30; fail "the remove plan failed"; }
  log "  choudoufu's remove plan on A:"; plan_lines "$D_PLAN"
  D_LINE="$(gauntlet_plan_line <<< "$D_PLAN")"
  D_DEST="$(destroyed "$D_PLAN")"
  D_OBJS="$(objects_of <<< "$D_DEST")"
  grep -qF "Plan: 0 to add, 0 to change, $CLASS_N to destroy." <<< "$D_PLAN" \
    || remove_fail "the remove plan is ${D_LINE:-no plan line}, not exactly $CLASS_N destroys; it proposes [$(joined "$(planned "$D_PLAN")")]"
  # By address first, the way stock plans it. A difference is #1996 (the
  # sweep files a for_each instance that left the render under
  # <typed resource>.orphan_..., although the object carries the
  # tofu-address annotation naming its instance) and is a fail; it is
  # never accepted as a pass.
  if [ "$D_DEST" != "$O_DEST" ]; then
    remove_fail "choudoufu's remove plan destroys at addresses that differ from stock's: choudoufu [$(joined "$D_DEST")], stock [$(joined "$O_DEST")] (#1996)"
  fi
  # Then by object, whatever the address: the same kind/namespace/name set.
  if [ "$D_OBJS" != "$O_OBJS" ]; then
    remove_fail "choudoufu's remove plan does not destroy the same objects as stock's: choudoufu [$(joined "$D_OBJS")], stock [$(joined "$O_OBJS")]"
  fi
  # The controller-copy half: nothing the cluster generated may be named, by
  # name in a destroy header or as an object.
  D_HEADERS="$({ grep -E 'will be destroyed' <<< "$D_PLAN" || true; })"
  while IFS= read -r copy; do
    [ -n "$copy" ] || continue
    cname="${copy#*/}"; ckind="${copy%%/*}"
    if grep -qF "$cname" <<< "$D_HEADERS" || grep -qxF "$(printf '%s\n' "$ckind" | tr '[:upper:]' '[:lower:]')|orphan_${NS}_${cname}" <<< "$D_OBJS"; then
      remove_fail "the remove plan proposes destroying $copy, which the cluster generated and the root never declared"
    fi
  done <<< "$COPIES_BEFORE"

  # Apply the removal whatever the plan's addresses, so the world is where
  # day2_count expects it; then read the objects back by kind/name.
  APPLY_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)"; APPLY_RC=$?
  if [ "$APPLY_RC" -ne 0 ] || ! grep -qF "0 added, 0 changed, $CLASS_N destroyed" <<< "$APPLY_OUT"; then
    printf '%s\n' "$APPLY_OUT" | tail -30
    remove_fail "the remove apply exited $APPLY_RC and did not report exactly $CLASS_N destroyed: $(grep -E '^Apply complete' <<< "$APPLY_OUT" | head -1) $(gauntlet_first_error_line <<< "$APPLY_OUT")"
  fi
  if kca get ingressclass nginx >/dev/null 2>&1; then
    remove_fail "the IngressClass nginx still exists after the remove apply"
    kca delete ingressclass nginx --ignore-not-found >/dev/null 2>&1
  fi
  if exists_a ingress via-nginx; then
    remove_fail "the Ingress via-nginx still exists after the remove apply"
    kca delete ingress via-nginx -n "$NS" --ignore-not-found >/dev/null 2>&1
  fi
  exists_a deployment "$CTRL" || remove_fail "the controller's Deployment is gone; the removal reached an object the values change kept"
  COPIES_AFTER="$(copies_a)"
  [ "$COPIES_AFTER" = "$COPIES_BEFORE" ] || remove_fail "the cluster-created objects changed across the removal: before [$(joined "$COPIES_BEFORE")], after [$(joined "$COPIES_AFTER")]"
  D_REPLAN="$(tofu_a plan -input=false -no-color 2>&1)"; D_REPLAN_RC=$?
  if [ "$D_REPLAN_RC" -ne 0 ] || ! grep -q "No changes." <<< "$D_REPLAN"; then
    printf '%s\n' "$D_REPLAN" | tail -30
    remove_fail "the replan after the remove is not empty (exit $D_REPLAN_RC, $(gauntlet_plan_line <<< "$D_REPLAN")): [$(joined "$(planned "$D_REPLAN")")] $(gauntlet_first_error_line <<< "$D_REPLAN")"
  fi
  REMAIN=$((TOTAL_N - CLASS_N))
  R_COUNT="$(count_a)"
  [ "$R_COUNT" = "$REMAIN" ] || remove_fail "$R_COUNT labelled objects after the remove, want $REMAIN"
  N_COPIES="$(grep -c . <<< "$COPIES_AFTER")"

  # Put back, so day2_count has an IngressClass whose aliases it can scale
  # and day2_crash a class its Ingresses name: the same toggle on is exactly
  # the two creates, at the instances' own addresses on both sides. Creates
  # are never orphan-named, so this holds whatever the destroy above was
  # filed under - and it is what shows that an orphan-named destroy left no
  # record behind that would stop the instance coming back.
  set_value "$ADOPTED" "$CLASS_OFF" "$CLASS_ON" || fail "could not re-enable the IngressClass in the adopted root"
  set_value "$ORACLE"  "$CLASS_OFF" "$CLASS_ON" || fail "could not re-enable the IngressClass in the oracle root"
  OB_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$OB_PLAN" | tail -20; fail "stock's put-back plan failed on B"; }
  grep -qF "Plan: $CLASS_N to add, 0 to change, 0 to destroy." <<< "$OB_PLAN" || { printf '%s\n' "$OB_PLAN" | tail -20; fail "stock's put-back plan on B is not exactly $CLASS_N creates"; }
  OB_CREATED="$(created "$OB_PLAN")"
  quiet stock_b apply -auto-approve -input=false -no-color || fail "stock's put-back apply failed on B"
  B_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$B_PLAN" | tail -30; fail "the put-back plan failed"; }
  log "  choudoufu's put-back plan on A:"; plan_lines "$B_PLAN"
  B_CREATED="$(created "$B_PLAN")"
  if ! grep -qF "Plan: $CLASS_N to add, 0 to change, 0 to destroy." <<< "$B_PLAN" || [ "$B_CREATED" != "$OB_CREATED" ]; then
    remove_fail "the put-back plan is $(gauntlet_plan_line <<< "$B_PLAN") creating [$(joined "$B_CREATED")], where stock's creates [$(joined "$OB_CREATED")]; it proposes [$(joined "$(planned "$B_PLAN")")]"
  fi
  APPLY_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)"; APPLY_RC=$?
  if [ "$APPLY_RC" -ne 0 ] || ! grep -qF "$CLASS_N added, 0 changed, 0 destroyed" <<< "$APPLY_OUT"; then
    printf '%s\n' "$APPLY_OUT" | tail -30
    remove_fail "the put-back apply exited $APPLY_RC and did not report exactly $CLASS_N added: $(grep -E '^Apply complete' <<< "$APPLY_OUT" | head -1) $(gauntlet_first_error_line <<< "$APPLY_OUT")"
  fi
  # Without these two nothing below measures what it says, so a miss here
  # is the one product failure that ends the run.
  { kca get ingressclass nginx >/dev/null 2>&1 && exists_a ingress via-nginx; } \
    || fail "the IngressClass nginx and the Ingress via-nginx were not both put back on A, which day2_count and day2_crash need${REMOVE_FAILS:+ (this stage also found: $REMOVE_FAILS)}"
  B_COUNT="$(count_a)"
  [ "$B_COUNT" = "$TOTAL_N" ] || remove_fail "$B_COUNT labelled objects after the put-back, want $TOTAL_N"
  B_REPLAN="$(tofu_a plan -input=false -no-color 2>&1)"; B_REPLAN_RC=$?
  if [ "$B_REPLAN_RC" -ne 0 ] || ! grep -q "No changes." <<< "$B_REPLAN"; then
    printf '%s\n' "$B_REPLAN" | tail -30
    remove_fail "the replan after the put-back is not empty (exit $B_REPLAN_RC, $(gauntlet_plan_line <<< "$B_REPLAN")): [$(joined "$(planned "$B_REPLAN")")]"
    quiet tofu_a apply -auto-approve -input=false -no-color || fail "could not converge A after the put-back; nothing below would measure day-2 behaviour"
  fi
  gauntlet_wait_until 240 "the controller on cluster A to publish $CTRL's cluster IP into the recreated Ingress via-nginx's status" -- status_published "$KCA" via-nginx \
    || remove_fail "the controller never published the Service's cluster IP into the recreated Ingress via-nginx's status (it reads '$(status_ip "$KCA" via-nginx)', the Service '$(svc_ip "$KCA")')"

  if [ -n "$REMOVE_FAILS" ]; then
    gauntlet_stage day2_remove fail "ingressClassResource.enabled=false in values.yaml (the IngressClass nginx and the root's Ingress via-nginx out of the render; stock on the oracle destroys [$(joined "$O_DEST")]): $REMOVE_FAILS. The removal was applied on both sides regardless and read back with kubectl, then put back (choudoufu's put-back created [$(joined "$B_CREATED")]), so every later stage starts where it expects"
  else
    gauntlet_stage day2_remove pass "ingressClassResource.enabled=false in values.yaml took the cluster-scoped IngressClass nginx out of the render, and with it the key of the root's own Ingress block, and choudoufu proposed exactly $CLASS_N destroys across the two blocks at the instances' own addresses ($(joined "$D_DEST")), the same addresses and the same objects as stock's $CLASS_N on the oracle cluster; after the apply both are gone (kubectl), the controller's Deployment untouched, the replan empty and $REMAIN objects labelled. The controller-copy exclusion: the $N_COPIES objects the cluster and the controller generated in $NS ($(joined "$COPIES_AFTER")), the leader-election Lease and the Endpoints among them with no owner reference, were never proposed and read back identical. Put back, the same toggle planned and applied exactly $CLASS_N creates at the addresses stock creates ($(joined "$B_CREATED")), the replan was empty, and the controller published the Service's cluster IP into the recreated Ingress' status. BREAK_REMOVE=1 keeps the IngressClass and requires no destroy"
  fi
fi

# ── 9. day2_count: for_each over the render, scaled through values ───────
gauntlet_begin_stage day2_count
log "=== 9. day2_count: ingressClassResource.aliases 0 -> 2 -> 1 -> 2, each entry one IngressClass and one Ingress ==="
CLASS0="$(rest_addr IngressClass "" choudoufu-0)"
CLASS1="$(rest_addr IngressClass "" choudoufu-1)"
VIA0="$(block_addr ingresses choudoufu-0)"
VIA1="$(block_addr ingresses choudoufu-1)"
# As in day2_remove: a miss is recorded and the stage carries on to the
# withdrawal, so day2_replace starts from a converged estate; one verdict at
# the end.
COUNT_FAILS=""
count_fail() { COUNT_FAILS="${COUNT_FAILS:+$COUNT_FAILS; }$*"; }
# aliases_a: the alias IngressClasses and Ingresses on A, for a failure line.
aliases_a() { { kca get ingressclasses -o name; kca get ingresses -n "$NS" -o name; } 2>/dev/null | grep choudoufu | tr '\n' ' '; }
set_aliases "$ADOPTED" 2 || fail "could not add two aliases to the adopted root"
set_aliases "$ORACLE" 2  || fail "could not add two aliases to the oracle root"
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -20; fail "stock's plan for the two aliases failed on B"; }
grep -qF "Plan: 4 to add, 0 to change, 0 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -20; fail "stock's plan for the two aliases on B is not exactly four adds"; }
O_UP_CREATED="$(created "$O_PLAN")"
quiet stock_b apply -auto-approve -input=false -no-color || fail "stock could not create the aliases on B"
set_aliases "$ORACLE" 1 || fail "could not scale the oracle root to 1"
O_DOWN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_DOWN" | tail -20; fail "stock's scale-down plan failed on B"; }
grep -qF "Plan: 0 to add, 0 to change, 2 to destroy." <<< "$O_DOWN" || { printf '%s\n' "$O_DOWN" | tail -20; fail "stock's scale-down plan on B is not exactly two destroys"; }
O_DOWN_DEST="$(destroyed "$O_DOWN")"
{ grep -qxF "$CLASS1" <<< "$O_DOWN_DEST" && grep -qxF "$VIA1" <<< "$O_DOWN_DEST"; } || { plan_lines "$O_DOWN"; fail "stock's scale-down on B does not destroy $CLASS1 and $VIA1"; }
O_DOWN_OBJS="$(objects_of <<< "$O_DOWN_DEST")"
quiet stock_b apply -auto-approve -input=false -no-color || fail "stock's scale-down apply failed on B"

A_UP="$(tofu_a plan -input=false -no-color 2>&1)"; A_UP_RC=$?
log "  choudoufu's plan for two aliases on A:"; plan_lines "$A_UP"
UP_OK=1
if [ "$A_UP_RC" -ne 0 ] || ! grep -qF "Plan: 4 to add, 0 to change, 0 to destroy." <<< "$A_UP" || [ "$(created "$A_UP")" != "$O_UP_CREATED" ]; then
  printf '%s\n' "$A_UP" | tail -30
  count_fail "choudoufu could not plan the two aliases' four objects at stock's addresses: exit $A_UP_RC, $(gauntlet_plan_line <<< "$A_UP"), creating [$(joined "$(created "$A_UP")")] where stock creates [$(joined "$O_UP_CREATED")]; first error: $(gauntlet_first_error_line <<< "$A_UP")"
  UP_OK=0
else
  APPLY_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)"; APPLY_RC=$?
  if [ "$APPLY_RC" -ne 0 ] || ! grep -qF "4 added, 0 changed, 0 destroyed" <<< "$APPLY_OUT"; then
    printf '%s\n' "$APPLY_OUT" | tail -30
    count_fail "the aliases' creating apply exited $APPLY_RC and did not report exactly four added: $(gauntlet_first_error_line <<< "$APPLY_OUT")"
    UP_OK=0
  elif ! { kca get ingressclass choudoufu-0 >/dev/null 2>&1 && kca get ingressclass choudoufu-1 >/dev/null 2>&1 && exists_a ingress via-choudoufu-0 && exists_a ingress via-choudoufu-1; }; then
    count_fail "both aliases' IngressClasses and Ingresses do not exist after choudoufu created them: [$(aliases_a)]"
    UP_OK=0
  else
    A_RE="$(tofu_a plan -input=false -no-color 2>&1)"; A_RE_RC=$?
    if [ "$A_RE_RC" -ne 0 ] || ! grep -q "No changes." <<< "$A_RE"; then
      printf '%s\n' "$A_RE" | tail -30
      count_fail "replanning the root choudoufu itself had just applied with two aliases is not empty (exit $A_RE_RC, $(gauntlet_plan_line <<< "$A_RE")); proposed: $(planned "$A_RE" | head -10 | tr '\n' ';'); first error: $(gauntlet_first_error_line <<< "$A_RE")"
      UP_OK=0
    fi
  fi
fi

if [ "$UP_OK" = "1" ]; then
  set_aliases "$ADOPTED" 1 || fail "could not scale the adopted root to 1"
  C_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$C_PLAN" | tail -30; fail "the scale-down plan failed"; }
  log "  choudoufu's scale-down plan on A:"; plan_lines "$C_PLAN"
  C_DEST="$(destroyed "$C_PLAN")"
  C_ADDR="$(joined "$C_DEST")"
  C_OBJS="$(objects_of <<< "$C_DEST")"
  grep -qF "Plan: 0 to add, 0 to change, 2 to destroy." <<< "$C_PLAN" \
    || count_fail "the scale-down plan is $(gauntlet_plan_line <<< "$C_PLAN"), not exactly two destroys: [$(joined "$(planned "$C_PLAN")")]"
  # The objects must be stock's (alias choudoufu-1's pair). The addresses
  # are reported, not judged: a different address here is the same #1996
  # day2_remove judges, and this stage measures which instance goes.
  [ "$C_OBJS" = "$O_DOWN_OBJS" ] \
    || count_fail "the scale-down plan does not destroy the objects stock's does: choudoufu [$(joined "$C_OBJS")], stock [$(joined "$O_DOWN_OBJS")]"
  if [ "$C_DEST" = "$O_DOWN_DEST" ]; then
    C_ADDR_NOTE="at stock's own addresses"
  else
    C_ADDR_NOTE="at addresses that differ from stock's [$(joined "$O_DOWN_DEST")] (#1996, judged in day2_remove)"
  fi
  APPLY_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)"; APPLY_RC=$?
  if [ "$APPLY_RC" -ne 0 ] || ! grep -qF "0 added, 0 changed, 2 destroyed" <<< "$APPLY_OUT"; then
    printf '%s\n' "$APPLY_OUT" | tail -30
    count_fail "the scale-down apply exited $APPLY_RC and did not report exactly two destroyed: $(gauntlet_first_error_line <<< "$APPLY_OUT")"
  fi
  if [ "${BREAK_COUNT:-}" = "1" ]; then
    kca get ingressclass choudoufu-0 >/dev/null 2>&1 || fail "BREAK_COUNT=1: choudoufu-0 was destroyed - the 'wrong instance' assertion would hold, so the check is not load-bearing"
    log "  BREAK_COUNT=1: caught - choudoufu-0 still exists, so asserting it was the one destroyed correctly fails"
    COUNT_BREAK_DONE=1
  else
    { kca get ingressclass choudoufu-0 >/dev/null 2>&1 && exists_a ingress via-choudoufu-0; } || count_fail "alias choudoufu-0's IngressClass or Ingress was destroyed on the scale-down: [$(aliases_a)]"
    kca get ingressclass choudoufu-1 >/dev/null 2>&1 && count_fail "the IngressClass choudoufu-1 still exists after the scale-down: [$(aliases_a)]"
    exists_a ingress via-choudoufu-1 && count_fail "the Ingress via-choudoufu-1 still exists after the scale-down: [$(aliases_a)]"
    { set_aliases "$ADOPTED" 2 && set_aliases "$ORACLE" 2; } || fail "could not scale back to 2"
    O_UP="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_UP" | tail -20; fail "stock's scale-up plan failed on B"; }
    grep -qF "Plan: 2 to add, 0 to change, 0 to destroy." <<< "$O_UP" || { printf '%s\n' "$O_UP" | tail -20; fail "stock's scale-up plan on B is not exactly two adds"; }
    quiet stock_b apply -auto-approve -input=false -no-color || fail "stock's scale-up apply failed on B"
    U_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$U_PLAN" | tail -30; fail "the scale-up plan failed"; }
    log "  choudoufu's scale-up plan on A:"; plan_lines "$U_PLAN"
    if ! grep -qF "Plan: 2 to add, 0 to change, 0 to destroy." <<< "$U_PLAN" || [ "$(created "$U_PLAN")" != "$(created "$O_UP")" ]; then
      count_fail "the scale-up plan is $(gauntlet_plan_line <<< "$U_PLAN") creating [$(joined "$(created "$U_PLAN")")], not exactly $CLASS1 and $VIA1 as stock's [$(joined "$(created "$O_UP")")]"
    fi
    APPLY_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)"; APPLY_RC=$?
    if [ "$APPLY_RC" -ne 0 ] || ! grep -qF "2 added, 0 changed, 0 destroyed" <<< "$APPLY_OUT"; then
      printf '%s\n' "$APPLY_OUT" | tail -30
      count_fail "the scale-up apply exited $APPLY_RC and did not report exactly two added: $(gauntlet_first_error_line <<< "$APPLY_OUT")"
    fi
  fi
fi

# Withdraw the aliases on both sides whatever happened above, and prove A
# converged: day2_replace asserts an exact plan shape on this root.
{ set_aliases "$ADOPTED" 0 && set_aliases "$ORACLE" 0; } || fail "could not withdraw the aliases"
W_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$W_OUT" | tail -30; count_fail "the apply withdrawing the aliases from A failed: $(gauntlet_first_error_line <<< "$W_OUT")"; }
quiet stock_b apply -auto-approve -input=false -no-color || fail "could not withdraw the aliases from B"
kca delete ingress via-choudoufu-0 via-choudoufu-1 -n "$NS" --ignore-not-found >/dev/null 2>&1
kca delete ingressclass choudoufu-0 choudoufu-1 --ignore-not-found >/dev/null 2>&1
CLEAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$CLEAN" | tail -30; fail "the plan after withdrawing the aliases failed; nothing below would measure day-2 behaviour"; }
grep -q "No changes." <<< "$CLEAN" || { printf '%s\n' "$CLEAN" | tail -30; fail "the plan after withdrawing the aliases is not empty; nothing below would measure day-2 behaviour${COUNT_FAILS:+ (this stage also found: $COUNT_FAILS)}"; }

if [ -n "$COUNT_FAILS" ]; then
  gauntlet_stage day2_count fail "ingressClassResource.aliases 0 -> 2 -> 1 -> 2 (each entry one IngressClass and one Ingress across the chart_objects and ingresses blocks; stock's plans on the oracle are 4 adds, then 2 destroys [$(joined "$O_DOWN_DEST")], then 2 adds): $COUNT_FAILS. The aliases were withdrawn on both sides afterwards and A replans empty"
elif [ -n "${COUNT_BREAK_DONE:-}" ]; then
  gauntlet_stage day2_count pass "BREAK_COUNT=1 control: asserting the remaining alias (choudoufu-0) was destroyed correctly fails to hold; the real check is skipped"
else
  gauntlet_stage day2_count pass "for_each over the render, scaled the way a chart user scales it, across two blocks: ingressClassResource.aliases at two entries rendered two more IngressClasses and the root's ingresses block, keyed by the rendered IngressClasses, two more Ingresses, and choudoufu created all four at stock's addresses, then replanned empty; at one entry it destroyed exactly alias choudoufu-1's pair, the same two objects stock destroys, planned at $C_ADDR, $C_ADDR_NOTE ($CLASS0, $VIA0 untouched, all read with kubectl); back at two it created exactly $CLASS1 and $VIA1; stock's plans for the same three edits on the oracle cluster have the identical shape. Withdrawn afterwards, A replans empty. BREAK_COUNT=1 asserts the remaining alias was destroyed and correctly fails"
fi

# ── 9b. day2_replace: a create_before_destroy rename ─────────────────────
gauntlet_begin_stage day2_replace
log "=== 9b. day2_replace: a content-hashed ConfigMap renamed under create_before_destroy ==="
gauntlet_kind_day2_replace "$ADOPTED" "$ORACLE" "$NS"

# ── 10. day2_crash: an apply of two Ingresses, killed after one ──────────
gauntlet_begin_stage day2_crash
log "=== 10. day2_crash: SIGTERM between the create of one Ingress and the create of the next ==="
gauntlet_kind_day2_crash_rename "$ADOPTED" "$NS"

crash_block first > "$ORACLE/crash.tf" || fail "could not write crash-first to the oracle root"
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's crash-first plan failed on B"; }
grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's crash-first plan on B is not exactly one add"; }
quiet stock_b apply -auto-approve -input=false -no-color || fail "stock's crash-first apply failed on B"
crash_block second >> "$ORACLE/crash.tf" || fail "could not append crash-second to the oracle root"
O_REMAINDER="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_REMAINDER" | tail -10; fail "stock's remainder plan failed on B"; }
grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_REMAINDER" \
  || { printf '%s\n' "$O_REMAINDER" | tail -10; fail "stock's remainder plan on B is not exactly one add - the oracle for this stage is not what it should be"; }
grep -q 'kubernetes_manifest.crash_second' <<< "$O_REMAINDER" || { plan_lines "$O_REMAINDER"; fail "stock's remainder plan on B does not name crash_second"; }
quiet stock_b apply -auto-approve -input=false -no-color || fail "stock's remainder apply failed on B"
log "  oracle: stock at crash-first alone plans exactly one add (crash_second) for the remainder, and applies it"

C_BEFORE="$(count_a)"
{ crash_block first; crash_block second; } > "$ADOPTED/crash.tf" || fail "could not write the crash pair to the adopted root"
X_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$X_PLAN" | tail -30; fail "the pre-crash plan failed"; }
grep -qF "Plan: 2 to add, 0 to change, 0 to destroy." <<< "$X_PLAN" \
  || { printf '%s\n' "$X_PLAN" | tail -30; fail "the pre-crash plan is not exactly two adds - there is no two-object apply to interrupt"; }
X_RECORDS_BEFORE="$(gauntlet_record_envelope_count "$ADOPTED/.tofu-records")"

X_OUT="$(cd "$ADOPTED" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" \
  TOFU_E2E_APPLY_RESOURCE_INTERRUPT="kubernetes_manifest.crash_first" \
  "$TOFU_CRASH" apply -input=false -auto-approve -no-color -parallelism=1 2>&1)"; X_RC=$?
printf '%s\n' "$X_OUT" > "$WORK/day2_crash.log"
log "  interrupted apply exited $X_RC (a genuine crash is not expected to exit 0)"
[ "$X_RC" -ne 0 ] || { printf '%s\n' "$X_OUT" | tail -20; fail "the interrupted apply exited 0 - the engine's self-signal never landed, so nothing was interrupted and this stage would measure a clean apply"; }
exists_a ingress crash-first || { printf '%s\n' "$X_OUT" | tail -20; fail "crash-first does not exist after the interrupted apply - the kill landed before the create committed"; }
exists_a ingress crash-second && { printf '%s\n' "$X_OUT" | tail -20; fail "crash-second exists after the interrupted apply - the kill landed after both creates"; }
{ GET_OUT="$(kca get ingress -n "$NS" -l "tofu-estate=$ESTATE" -o name 2>/dev/null)" && grep -qE '(^|/)crash-first$' <<< "$GET_OUT"; } \
  || { printf '%s\n' "$GET_OUT"; fail "crash-first was created by the interrupted apply but does not come back under tofu-estate=$ESTATE"; }
X_ADDR_ANN="$(kca get ingress crash-first -n "$NS" -o jsonpath='{.metadata.annotations.choudoufu\.intentius\.io/tofu-address}' 2>&1)"
[ "$X_ADDR_ANN" = "kubernetes_manifest.crash_first" ] \
  || fail "crash-first's choudoufu.intentius.io/tofu-address annotation reads '$X_ADDR_ANN', want kubernetes_manifest.crash_first (#1639)"
X_RECORDS_AFTER="$(gauntlet_record_envelope_count "$ADOPTED/.tofu-records")"
X_REC="$(gauntlet_record_file "$ADOPTED/.tofu-records" "kubernetes_manifest.crash_first")"
[ -n "$X_REC" ] || fail "kubernetes_manifest.crash_first has no record file under $ADOPTED/.tofu-records after the interrupted apply committed its create (#1211)"
X_KEYS_L="$(gauntlet_record_manifest_keys "$X_REC" labels)" \
  || fail "the record at $X_REC carries no residue.manifest_metadata_keys entry for labels (#1211)"
X_KEYS_A="$(gauntlet_record_manifest_keys "$X_REC" annotations)" \
  || fail "the record at $X_REC carries no residue.manifest_metadata_keys entry for annotations (#1211)"
[ "$X_KEYS_L" = "tofu-estate" ] || fail "the record at $X_REC says the applied manifest declared metadata.labels [$X_KEYS_L], want exactly tofu-estate (#1211)"
[ "$X_KEYS_A" = "choudoufu.intentius.io/tofu-address" ] || fail "the record at $X_REC says the applied manifest declared metadata.annotations [$X_KEYS_A], want exactly choudoufu.intentius.io/tofu-address (#1211, #1639)"
[ "$X_RECORDS_AFTER" = "$((X_RECORDS_BEFORE + 1))" ] || fail "records went $X_RECORDS_BEFORE -> $X_RECORDS_AFTER across the interrupted apply, want exactly one more"
log "  crash-first exists and is labelled; crash-second does not exist; records $X_RECORDS_BEFORE -> $X_RECORDS_AFTER"

if [ "${BREAK_CRASH_UNBOUND:-}" = "1" ]; then
  kca label ingress crash-first -n "$NS" tofu-estate- >/dev/null 2>&1 || fail "BREAK_CRASH_UNBOUND: could not strip the label off crash-first"
  log "  BREAK_CRASH_UNBOUND=1: stripped tofu-estate off crash-first with kubectl"
fi

R_PLAN="$(tofu_a plan -input=false -no-color 2>&1)"; R_RC=$?
R_LINE="$(grep -E '^Plan:|^No changes' <<< "$R_PLAN" | head -1 | sed 's/\.$//')"
recovered() {
  [ "$R_RC" -eq 0 ] || return 1
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$R_PLAN" || return 1
  grep -qE '^[[:space:]]*# kubernetes_manifest\.crash_second will be created' <<< "$R_PLAN" || return 1
  local r_proposed
  r_proposed="$(grep -E '^[[:space:]]*# .* will be' <<< "$R_PLAN")"
  grep -q 'crash_first\|crash-first' <<< "$r_proposed" && return 1
  return 0
}

if [ "${BREAK_CRASH:-}" = "1" ]; then
  [ "$R_RC" -eq 0 ] || { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK_CRASH=1: the plan after the interrupt exited $R_RC"; }
  grep -qF "No changes." <<< "$R_PLAN" \
    && { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK_CRASH=1: the plan after a real interrupted two-object apply came back empty, so this stage's own check is not load-bearing"; }
  gauntlet_stage day2_crash pass "BREAK_CRASH=1 control: after the same real interrupt the plan proposes work ($R_LINE), so asserting nothing is proposed correctly fails to hold; the real check is skipped $CRASH_RENAME_DETAIL"
  quiet tofu_a apply -auto-approve -input=false -no-color || fail "BREAK_CRASH: the recovery apply failed afterwards"
elif [ "${BREAK_CRASH_UNBOUND:-}" = "1" ]; then
  if recovered; then
    printf '%s\n' "$R_PLAN" | grep -E '^Plan:|will be'
    fail "BREAK_CRASH_UNBOUND=1: the recovery check still holds with crash-first carrying no tofu-estate label - it is not measuring whether the crashed-out object was bound at all"
  fi
  gauntlet_stage day2_crash pass "BREAK_CRASH_UNBOUND=1 control: with the tofu-estate label stripped off the Ingress the interrupted apply created, the recovery check correctly fails to hold ($R_LINE); the real check is skipped $CRASH_RENAME_DETAIL"
  kca delete ingress crash-first -n "$NS" >/dev/null 2>&1 || fail "BREAK_CRASH_UNBOUND: could not delete the unlabelled crash-first afterwards"
  quiet tofu_a apply -auto-approve -input=false -no-color || fail "BREAK_CRASH_UNBOUND: the apply after the cleanup failed"
else
  if ! recovered; then
    printf '%s\n' "$R_PLAN" | grep -E '^Plan:|^No changes|will be' | head -20
    gauntlet_stage day2_crash fail "the plan after a real interrupt between the creates of kubernetes_manifest.crash_first and .crash_second (both Ingresses of the class this estate's render installs) is not exactly the remainder: ${R_LINE:-no plan line} (exit $R_RC). crash-first exists carrying tofu-estate=$ESTATE and crash-second does not, both read with kubectl; stock from the same position plans exactly one add (crash_second). Records $X_RECORDS_BEFORE -> $X_RECORDS_AFTER $CRASH_RENAME_DETAIL"
  else
    R_APPLY="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)"; R_APPLY_RC=$?
    [ "$R_APPLY_RC" -eq 0 ] || { printf '%s\n' "$R_APPLY" | tail -20; fail "the recovery apply exited $R_APPLY_RC"; }
    grep -qF "Apply complete! Resources: 1 added, 0 changed, 0 destroyed" <<< "$R_APPLY" \
      || { printf '%s\n' "$R_APPLY" | tail -5; fail "the recovery apply did not add exactly the one remaining object"; }
    exists_a ingress crash-second || fail "crash-second does not exist after the recovery apply"
    exists_a ingress crash-first || fail "crash-first is gone after the recovery apply - the recovery replaced the object the crash created instead of binding it"
    R_REPLAN="$(tofu_a plan -input=false -no-color 2>&1)"; R_REPLAN_RC=$?
    [ "$R_REPLAN_RC" -eq 0 ] || { printf '%s\n' "$R_REPLAN" | tail -30; fail "the replan after the recovery exited $R_REPLAN_RC: $(grep -E '^Error' <<< "$R_REPLAN" | head -1)"; }
    grep -q "No changes." <<< "$R_REPLAN" || { printf '%s\n' "$R_REPLAN" | tail -30; fail "the replan after the recovery is not empty"; }
    C_AFTER="$(count_a)"
    [ "$C_AFTER" = "$((C_BEFORE + 2))" ] || fail "$C_AFTER labelled objects after the recovery, want $((C_BEFORE + 2)): $(labelled_a)"
    gauntlet_stage day2_crash pass "an apply creating two Ingresses of the class this estate's render installs was interrupted by a real SIGTERM (exit $X_RC), delivered by the engine itself inside the -parallelism=1 walker the instant kubernetes_manifest.crash_first's create committed; crash_second's manifest reads crash_first's name, so the walker cannot have reached it. kubectl confirms crash-first exists carrying tofu-estate=$ESTATE and its address annotation, and crash-second does not; the interrupted apply recorded exactly the one object it created (records $X_RECORDS_BEFORE -> $X_RECORDS_AFTER, labels [$X_KEYS_L], annotations [$X_KEYS_A], #1211). The next plan proposed exactly the remainder ($R_LINE, crash_second created) and nothing for crash-first, matching stock from the same position on the oracle cluster; the recovery apply added one, the replan is empty and $C_AFTER objects carry the label, $C_BEFORE plus the pair. BREAK_CRASH=1 and BREAK_CRASH_UNBOUND=1 both correctly fail $CRASH_RENAME_DETAIL"
  fi
fi
gauntlet_end_stage

# ── 11. day2_teardown: destroy the adopted estate ────────────────────────
gauntlet_begin_stage day2_teardown
log "=== 11. day2_teardown: apply -destroy on the adopted estate, and stock's own destroy on B ==="
T_EXPECT="$(count_a)"
T_OUT="$(tofu_a apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$T_OUT" | tail -20; fail "apply -destroy failed"; }
grep -qF "Resources: 0 added, 0 changed, $T_EXPECT destroyed" <<< "$T_OUT" || { printf '%s\n' "$T_OUT" | tail -5; fail "apply -destroy did not remove exactly the $T_EXPECT remaining objects"; }
# The controller pod's terminationGracePeriodSeconds is the chart's 300, so
# the namespace is given that long and a margin to finish going.
for _ in $(seq 1 170); do kca get namespace "$NS" >/dev/null 2>&1 || break; sleep 2; done
if kca get namespace "$NS" >/dev/null 2>&1; then
  kca get namespace "$NS" -o jsonpath='{.status}' 2>&1; printf '\n'
  kca get all,ingresses,configmaps,leases -n "$NS" 2>&1 | head -30
  fail "the $NS namespace still exists after the destroy"
fi
[ "$(count_a)" = "0" ] || fail "$(count_a) object(s) still carry tofu-estate=$ESTATE after the destroy: $(labelled_a)"
CLUSTER_LEFT=""
kca get clusterrole "$FULL" >/dev/null 2>&1 && CLUSTER_LEFT="$CLUSTER_LEFT clusterrole/$FULL"
kca get clusterrolebinding "$FULL" >/dev/null 2>&1 && CLUSTER_LEFT="$CLUSTER_LEFT clusterrolebinding/$FULL"
kca get ingressclass nginx >/dev/null 2>&1 && CLUSTER_LEFT="$CLUSTER_LEFT ingressclass/nginx"
[ -z "$CLUSTER_LEFT" ] || fail "cluster-scoped objects of the estate survive the destroy:$CLUSTER_LEFT"
CLASSES_LEFT="$(kca get ingressclasses -o jsonpath='{range .items[?(@.spec.controller=="k8s.io/ingress-nginx")]}{.metadata.name}{" "}{end}' 2>/dev/null)"
[ -z "$CLASSES_LEFT" ] || fail "IngressClasses for k8s.io/ingress-nginx survive the destroy: $CLASSES_LEFT"
O_EXPECT="$(managed_n stock_b)"
O_T="$(stock_b apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$O_T" | tail -10; fail "stock's destroy failed on B"; }
grep -qF "Resources: 0 added, 0 changed, $O_EXPECT destroyed" <<< "$O_T" || { printf '%s\n' "$O_T" | tail -10; fail "stock's destroy on B did not remove exactly the $O_EXPECT objects its state held"; }
gauntlet_stage day2_teardown pass "apply -destroy removed exactly the $T_EXPECT remaining objects in one apply; the $NS namespace is gone and with it the controller's Lease, its ReplicaSet and the Service's EndpointSlice and Endpoints; the cluster-scoped ClusterRole, ClusterRoleBinding and IngressClass are gone and no IngressClass for k8s.io/ingress-nginx remains; no object of the estate's kinds carries tofu-estate=$ESTATE (kubectl, every namespace); stock's destroy on the oracle cluster removed exactly the $O_EXPECT its state held"

# ── 12. greenfield: the same shape, fresh, with a live block ─────────────
gauntlet_begin_stage greenfield
log "=== 12. greenfield: choudoufu applies the shape fresh on the now-empty cluster A, in one apply ==="
write_root "$GREEN" live || fail "could not write the greenfield root"
green() { ( cd "$GREEN" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" "$TOFU" "$@" ); }
quiet green init -input=false -no-color || fail "greenfield init failed"

G_OUT="$(green apply -auto-approve -input=false -no-color 2>&1)"; G_RC=$?
if [ "$G_RC" -ne 0 ]; then
  printf '%s\n' "$G_OUT" | tail -40
  gauntlet_stage greenfield fail "choudoufu cannot apply the root fresh in one pass, which stock did at cold deploy against the same kind of empty cluster: exit $G_RC; the first error it printed was \"$(gauntlet_first_error_line <<< "$G_OUT")\""
  quiet green apply -destroy -auto-approve -input=false -no-color
  GREENFIELD_SKIPPED=1
fi
if [ -z "${GREENFIELD_SKIPPED:-}" ]; then
grep -qF "Apply complete! Resources: $TOTAL_N added, 0 changed, 0 destroyed" <<< "$G_OUT" || { printf '%s\n' "$G_OUT" | tail -5; fail "the greenfield apply did not add exactly the $TOTAL_N declared instances"; }
[ ! -f "$GREEN/terraform.tfstate" ] || fail "a terraform.tfstate appeared after a live-block apply"
nginx_ready "$KCA" "cluster A (greenfield)" || fail "ingress-nginx never became ready on A after the greenfield apply"
G_LABELLED="$(count_a)"
G_RECORDS="$(gauntlet_record_envelope_count "$GREEN/.tofu-records")"
G_PLAN="$(green plan -input=false -no-color 2>&1)" || { printf '%s\n' "$G_PLAN" | tail -30; fail "the greenfield replan failed"; }
grep -q "No changes." <<< "$G_PLAN" || { printf '%s\n' "$G_PLAN" | tail -20; fail "the greenfield replan is not empty"; }
rm -f "$GREEN/.terraform/choudoufu-cache.tfstate"
G_PLAN2="$(green plan -input=false -no-color 2>&1)" || { printf '%s\n' "$G_PLAN2" | tail -30; fail "the greenfield replan without the cache failed"; }
grep -q "No changes." <<< "$G_PLAN2" || { printf '%s\n' "$G_PLAN2" | tail -20; fail "the greenfield replan without the cache is not empty"; }

# The lost-store control (#1235), read as reference-k8s-cert-manager reads
# it: every kubernetes_manifest records its declared metadata keys (#1211),
# so the store is full before it is deleted, and a missing key set proposes
# removing nothing. The one typed resource here, the Namespace, may record
# residue of its own; an in-place update is reported against it by name and
# reconverged, never a create or a destroy.
[ "$G_RECORDS" -ge "$MANIFEST_N" ] || fail "the greenfield record store holds $G_RECORDS record envelope(s); every one of the $MANIFEST_N applied kubernetes_manifest instances owes one (#1211), so the lost-store control below would delete a store that is not full"
rm -rf "$GREEN/.tofu-records" "$GREEN/.terraform/choudoufu-cache.tfstate"
L_PLAN="$(green plan -input=false -no-color 2>&1)"; L_RC=$?
L_LINE="$(grep -E '^Plan:|^No changes' <<< "$L_PLAN" | head -1 | sed 's/\.$//')"
[ "$L_RC" -eq 0 ] || { printf '%s\n' "$L_PLAN" | tail -20; fail "the plan with no record store at all exited $L_RC"; }
L_GONE="$(grep -cE '^[[:space:]]*# .* will be (created|destroyed|replaced)|must be replaced' <<< "$L_PLAN")"
[ "$L_GONE" = "0" ] || { printf '%s\n' "$L_PLAN" | grep -E 'will be|must be' | head -20
  fail "with the whole record store deleted the plan proposes $L_GONE create/destroy/replace(s) ($L_LINE) - the objects are not being found by their label and their namespace and name alone"; }
L_MANIFEST_UPDATES="$(grep -cE '^[[:space:]]*# kubernetes_manifest\..* will be updated in-place' <<< "$L_PLAN")"
[ "$L_MANIFEST_UPDATES" = "0" ] || { printf '%s\n' "$L_PLAN" | grep -E 'will be' | head -20
  fail "with the record store deleted the plan proposes $L_MANIFEST_UPDATES in-place update(s) of kubernetes_manifest instances, whose records hold declared metadata KEY SETS and no residue values (#1211) - a missing key set proposes removing nothing"; }
L_OTHER="$(planned "$L_PLAN" | tr '\n' ';' | sed -E 's/;$//; s/;/; /g')"
if [ -n "$L_OTHER" ]; then
  quiet green apply -auto-approve -input=false -no-color || fail "the converging apply after the lost store failed"
fi
log "  lost store: $L_LINE${L_OTHER:+ ($L_OTHER, reconverged)}"
G_WANT="$TOTAL_N"; [ "${BREAK:-}" = "1" ] && G_WANT=$((TOTAL_N + 1))
if [ "${BREAK:-}" = "1" ]; then
  if [ "$G_LABELLED" = "$G_WANT" ]; then
    fail "BREAK=1: the greenfield object count matched a deliberately wrong expectation ($G_WANT) - the count assertion is not load-bearing"
  fi
  gauntlet_stage greenfield pass "BREAK=1 control: expecting $G_WANT labelled objects where the estate has $G_LABELLED makes the count assertion correctly fail; the estate applied ($TOTAL_N added in one pass, no terraform.tfstate) and replanned empty with and without the cache"
else
  [ "$G_LABELLED" = "$TOTAL_N" ] || fail "$G_LABELLED object(s) carry tofu-estate=$ESTATE after the greenfield apply, want $TOTAL_N: $(labelled_a)"
  gauntlet_stage greenfield pass "$TOTAL_N objects applied fresh with a live block and no terraform.tfstate, in ONE apply with no pre-apply - the render read by the data-read phase before the plan, the root's own Ingress keyed off it - every one labelled tofu-estate=$ESTATE (kubectl, 11 kinds), the controller Available and its write of the Service's cluster IP into Ingress via-nginx's status read back; replanned empty with and without the cache; the record store held $G_RECORDS envelope(s) for $TOTAL_N instances, and with that whole store deleted the plan read \"$L_LINE\", nothing created, destroyed or replaced and no kubernetes_manifest updated${L_OTHER:+ (proposed and reconverged: $L_OTHER)}. BREAK=1 expects a deliberately wrong object count and the assertion correctly fails"
fi
# ── no_local_state (#1966): the record store and the cache both gone ────
# live/e2e/lib/gauntlet.sh's gauntlet_kind_no_local_state; its Break line
# strips the tofu-estate label off drift_reconverge's RoleBinding.
gauntlet_kind_no_local_state "$GREEN" rolebinding "$FULL" "$NS"
quiet green apply -destroy -auto-approve -input=false -no-color || log "  note: greenfield teardown did not exit clean; the cluster is deleted below regardless"
fi
[ -n "${GREENFIELD_SKIPPED:-}" ] && gauntlet_stage no_local_state not_run "greenfield did not stand the estate up, so there is no applied root to delete the local state of"

# ── 13. strict: every toggle on, one refusal ─────────────────────────────
gauntlet_begin_stage strict
STRICT="$WORK/strict"
mkdir -p "$STRICT"
strict_block() { # $1 = the secrets setting under test
  cat <<EOF
terraform {
  required_providers {
    random = {
      source  = "hashicorp/random"
      version = ">= 3.0"
    }
  }
  live {
    estate = "reference-k8s-helm-ingress-nginx-strict"
    record_store "local" {
      path = ".tofu-records"
    }
    strict {
      secrets          = "$1"
      no_source_create = "refuse"
      marker_repair    = "never"
      markers "record" {
        types = ["kubernetes_manifest"]
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
S_INIT="$(cd "$STRICT" && "$TOFU" init -input=false -no-color 2>&1)" || { printf '%s\n' "$S_INIT" | tail -20; fail "choudoufu init for the strict-stage scratch estate failed"; }
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
  grep -qF 'strict { secrets = "refuse" }' <<< "$(tr -s ' \n' '  ' <<< "$STRICT_ON")" || { printf '%s\n' "$STRICT_ON"; fail "the refusal does not cite strict { secrets = \"refuse\" }"; }
  gauntlet_stage strict pass "every strict toggle on (secrets = refuse, no_source_create = refuse, marker_repair = never with a markers \"record\" selection naming kubernetes_manifest) against a scratch estate carrying random_password.db: exactly one refusal, Logical resource is not admitted under strict { secrets = \"refuse\" }; the other two toggles are on and silent. BREAK_STRICT=1 turns secrets back to \"store\" and the refusal disappears"
fi
gauntlet_end_stage

gauntlet_end
log "reference-k8s-helm-ingress-nginx: done"
