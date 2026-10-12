#!/usr/bin/env bash
# reference-k8s-helm-argo-cd (#1987): the kubernetes lane's Argo CD Helm estate,
# crossed on the kind substrate, in reference-k8s-helm-template's shape
# (#1963) for a second chart.
#
# The configuration is a Helm chart rendered CLIENT-SIDE through
# hashicorp/helm's `data "helm_template"` and applied as kubernetes_manifest
# blocks - the shape live/kubernetes/COMPATIBILITY.md recommends for a chart.
# No helm_release anywhere: Helm never talks to the cluster, the provider is
# declared for the one data source.
#
#   chart    argoproj argo-helm argo-cd 10.10.2 (Argo CD v3.5.4), Apache-2.0
#   archive  $CHART_URL below, sha256 $CHART_SHA256 below, checked on every run
#   render   59 documents at the chart's defaults; 52 objects over 14 kinds
#            with root/values.yaml's deltas (dex off, the redis-secret-init
#            helm hook off and argocd-redis declared with a fixed password,
#            argocd-notifications-secret declared with data, one rule on the
#            repo server's empty Role, an AppProject and an Application
#            through extraObjects)
#   root     root/main.tf, ours, hence reference-: one data source, a
#            Namespace (helm template renders none), and three
#            kubernetes_manifest blocks - the chart's 3 CRDs, the 43 objects
#            that are neither CRDs nor workloads, and the 6 workloads - each
#            with for_each over the render split and yamldecoded INSIDE the
#            for_each expression, keyed kind/namespace/name, and
#            `manifest = each.value` (#1962)
#
# What it drives that nothing else in the lane does:
#
#   - a declared object a controller writes into: the chart declares
#     argocd-secret with no data, and argocd-server fills it at start-up
#     (server.secretkey, the admin password hash, a self-signed TLS pair).
#     Every stamp, apply and sweep over that Secret has to leave the
#     controller's keys alone;
#   - objects the controller creates in the estate's own namespace with no
#     owner reference at all - the `default` AppProject, a custom resource
#     of the estate's own CRDs, and argocd-initial-admin-secret - which
#     nothing declares and the sweep must leave alone, told apart from the
#     estate's objects by the label alone;
#   - custom resources of the render's CRDs that a controller reconciles
#     and writes a status into through the main resource (the Application
#     CRD has no status subresource): the estate's Application, its day-2
#     shards and day2_crash's pair;
#   - a StatefulSet declared in the render, in the same workload block as
#     five Deployments;
#   - a day-2 removal through values that also edits a survivor: turning
#     the notifications controller off takes eight objects out of the
#     render and withdraws one ingress rule from the repo server's
#     NetworkPolicy, so the plan is eight destroys AND one in-place update;
#   - names shared across kinds, up to seven per name (rel-argocd-server is
#     a NetworkPolicy, ClusterRole, ClusterRoleBinding, Role, RoleBinding,
#     Service and Deployment), and a name shared by two custom kinds
#     (gauntlet is an AppProject and an Application);
#   - a chart whose helm hook (redis-secret-init) and random secret had to
#     be replaced through values rather than rendered.
#
# ── the two applies ──────────────────────────────────────────────────────
#
# kubernetes_manifest builds an object's schema at PLAN time, so a root
# declaring a CRD and an object of it cannot be planned against an empty
# cluster, by stock or by choudoufu. cold_deploy runs the un-targeted plan
# as a CONTROL and requires it to fail, then performs the pre-apply
# live/gauntlet/estates.json declares (the Namespace and
# kubernetes_manifest.crds) on both sides in one gauntlet_pre_apply call,
# then the main apply. See reference-k8s-cert-manager for the first estate
# that took this shape.
#
# Two kind clusters, both created for this run and deleted after it:
#
#   A  the estate. Stock cold-deploys it, choudoufu adopts it from stock's
#      state and runs every day-2 stage on it, tears it down, then applies
#      the same shape fresh with a live block (greenfield).
#   B  the oracle. Stock cold-deploys the identical shape, pre-apply
#      included, and applies every day-2 change itself.
#
# A stage this script cannot pass records fail with the reason and the run
# continues; the runner decides what "clear" means. A fault in the
# substrate itself is a fail on the stage being set up and an exit.
#
#   go run ./tools/gauntlet run reference-k8s-helm-argo-cd
#   bash live/e2e/reference-k8s-helm-argo-cd/run.sh
#
# Needs kind, kubectl, terraform (the stock oracle), Docker, python3, jq and
# curl. Network: the chart archive once (then cached under
# $CHOUDOUFU_CHART_CACHE, default ~/.cache/choudoufu/charts, and re-checked
# against its sha256 on every run) and two public image pulls per cluster
# (quay.io/argoproj/argocd:v3.5.4 for all five Argo CD workloads,
# ecr-public.aws.com/docker/library/redis:8.6.4-alpine; both publish
# linux/arm64 and linux/amd64).
#
# Env overrides:
#   TOFU_BIN       path to a prebuilt choudoufu binary; skips the go build.
#   CHOUDOUFU_CHART_CACHE
#                  where the verified chart archive is kept between runs.
#   BREAK          run drift_reconverge's and greenfield's negative
#                  controls instead of the real checks.
#   BREAK_PREAPPLY apply the whole root in ONE pass at cold deploy and
#                  require it to succeed; it must fail, which is what
#                  makes the pre-apply load-bearing rather than decorative.
#   BREAK_REMOVE   keep notifications enabled and assert no destroy is
#                  proposed.
#   BREAK_COUNT    assert the wrong shard was destroyed on the scale-down.
#   BREAK_APPROVAL apply the saved plan after the world moved and expect
#                  success.
#   BREAK_REPLACE  see live/e2e/lib/gauntlet.sh's gauntlet_kind_day2_replace.
#   BREAK_CRASH    assert, after the same real interrupt, that nothing is
#                  proposed; must fail, because a recovered run proposes
#                  the remainder.
#   BREAK_CRASH_UNBOUND
#                  strip the tofu-estate label off the Application the
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
ESTATE="reference-k8s-helm-argo-cd"
NS="argocd"
RELEASE="rel"
FULL="$RELEASE-argocd"   # the chart's fullname for this release (nameOverride: argocd)

# The chart, pinned by digest. root/main.tf reads it from charts/ beside
# itself; write_root copies the verified archive there.
CHART_VERSION="10.10.2"
CHART_FILE="argo-cd-$CHART_VERSION.tgz"
CHART_URL="https://github.com/argoproj/argo-helm/releases/download/argo-cd-$CHART_VERSION/$CHART_FILE"
CHART_SHA256="1f2bdb0e23b87fdac5222d99d1acf6964e670db9bf63912c58ab4677e505638b"
CHART_CACHE="${CHOUDOUFU_CHART_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/choudoufu/charts}"

# The estate's kinds, for the label count. The two custom kinds only exist
# once the CRDs are installed; kubectl prints nothing for a kind the API
# server does not serve, which counts as zero, which is correct. They are
# spelled with their group so no other installed API can shadow them.
KINDS="namespaces customresourcedefinitions serviceaccounts clusterroles clusterrolebindings roles rolebindings configmaps secrets services networkpolicies deployments statefulsets appprojects.argoproj.io applications.argoproj.io"
CRD_N=3       # kubernetes_manifest.crds
REST_N=49     # kubernetes_manifest.rest (43) and .late (6), one apply
PRE_N=$((1 + CRD_N))          # the Namespace and the CRDs: the pre-apply's population
TOTAL_N=$((PRE_N + REST_N))   # 53 managed instances, 52 of them kubernetes_manifest
MANIFEST_N=$((CRD_N + REST_N))
NOTIF_N=8     # objects notifications.enabled=false takes out of the render
WORK="$(mktemp -d)"
STOCK="$WORK/stock"; ADOPTED="$WORK/adopted"; ORACLE="$WORK/oracle"; GREEN="$WORK/green"
KCA="$WORK/a.kubeconfig"; KCB="$WORK/b.kubeconfig"
CLUSTER_A="${GAUNTLET_KIND_PREFIX:-chdf}-refhac-a-$$"; CLUSTER_B="${GAUNTLET_KIND_PREFIX:-chdf}-refhac-b-$$"  # GAUNTLET_KIND_PREFIX: lets concurrent workers name their own clusters
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
sha256_stdin() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum | cut -d' ' -f1
  else shasum -a 256 | cut -d' ' -f1; fi
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

# set_shards <dir> <n>: day2_count's for_each scaling. Every entry the
# chart's extraObjects list carries is one more rendered document, so n
# Applications named gauntlet-shard-<i> are n more instances of
# kubernetes_manifest.rest. extraObjects is values.yaml's last key, and this
# rewrites everything after its day2_count marker line to the end of the
# file; the four entries above the marker are never touched.
set_shards() {
  python3 - "$1/values.yaml" "$2" <<'PY'
import sys
p, n = sys.argv[1], int(sys.argv[2])
marker = "  # day2_count: Applications shard-<i> below this line, written by run.sh\n"
s = open(p).read()
assert s.count(marker) == 1, "values.yaml does not carry the day2_count marker exactly once"
i = s.find(marker) + len(marker)
block = ""
for k in range(n):
    block += ("  - apiVersion: argoproj.io/v1alpha1\n"
              "    kind: Application\n"
              "    metadata:\n"
              "      name: gauntlet-shard-%d\n"
              "      namespace: argocd\n"
              "    spec:\n"
              "      project: gauntlet\n"
              "      source:\n"
              "        repoURL: https://example.invalid/gauntlet.git\n"
              "        path: shard-%d\n"
              "        targetRevision: HEAD\n"
              "      destination:\n"
              "        server: https://kubernetes.default.svc\n"
              "        namespace: gauntlet\n") % (k, k)
open(p, 'w').write(s[:i] + block)
PY
}

# rename_crds <dir>: the block rename day2_rename's moved block covers. Every
# reference moves with it (rest's depends_on names it).
rename_crds() {
  python3 - "$1/main.tf" <<'PY'
import sys
p = sys.argv[1]; s = open(p).read()
old_block = 'resource "kubernetes_manifest" "crds" {'
assert s.count(old_block) == 1
s = s.replace(old_block, 'resource "kubernetes_manifest" "custom_resource_definitions" {', 1)
assert s.count('kubernetes_manifest.crds') == 1, "rest's depends_on is not the one other reference this edit expects"
s = s.replace('kubernetes_manifest.crds', 'kubernetes_manifest.custom_resource_definitions')
s += '''
moved {
  from = kubernetes_manifest.crds
  to   = kubernetes_manifest.custom_resource_definitions
}
'''
open(p, 'w').write(s)
PY
}

# crash_block <first|second>: day2_crash's two extra custom resources,
# written to crash.tf in a root that already holds the estate. They are
# Applications - a namespaced custom kind this estate's own CRDs serve, in
# the estate's own AppProject - and crash_second's manifest reads
# crash_first's name, a real edge in the graph, so nothing can create
# crash-second until crash-first has committed (#490's discipline;
# reference-k8s-cert-manager's crash pair, #1262). Neither has a sync
# policy, so the application controller only writes their status, and
# neither carries Argo CD's resources-finalizer: the controller adds no
# finalizer of its own to an Application without pre- or post-delete hooks
# (controller/appcontroller.go at v3.5.4), and the repository these name
# does not exist, so no hook is ever found.
#
# crash_first depends on the rest block, not just the Namespace, so that
# the teardown destroys the pair before the rest block and therefore before
# the CRDs: with only the Namespace as an edge, the CRD destroy could run
# first, and kubernetes_manifest cannot map an Application whose CRD is
# already gone. It also orders the pair after the estate's AppProject
# gauntlet, which they name.
crash_block() {
  if [ "$1" = "first" ]; then
    cat <<EOF

resource "kubernetes_manifest" "crash_first" {
  manifest = {
    "apiVersion" = "argoproj.io/v1alpha1"
    "kind"       = "Application"
    "metadata" = {
      "name"      = "crash-first"
      "namespace" = "$NS"
    }
    "spec" = {
      "project" = "gauntlet"
      "source" = {
        "repoURL"        = "https://example.invalid/gauntlet.git"
        "path"           = "crash-first"
        "targetRevision" = "HEAD"
      }
      "destination" = {
        "server"    = "https://kubernetes.default.svc"
        "namespace" = "gauntlet"
      }
    }
  }

  depends_on = [kubernetes_manifest.rest]
}
EOF
  else
    cat <<EOF

resource "kubernetes_manifest" "crash_second" {
  manifest = {
    "apiVersion" = "argoproj.io/v1alpha1"
    "kind"       = "Application"
    "metadata" = {
      "annotations" = {
        "after" = kubernetes_manifest.crash_first.manifest.metadata.name
      }
      "name"      = "crash-second"
      "namespace" = "$NS"
    }
    "spec" = {
      "project" = "gauntlet"
      "source" = {
        "repoURL"        = "https://example.invalid/gauntlet.git"
        "path"           = "crash-second"
        "targetRevision" = "HEAD"
      }
      "destination" = {
        "server"    = "https://kubernetes.default.svc"
        "namespace" = "gauntlet"
      }
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
# NO controller owns. Nothing in Argo CD copies labels onto what it
# creates; the owner filter is kept so the count means what it means in
# the other kind estates.
count_a() {
  local n=0 k c
  for k in $KINDS; do
    c="$(kca get "$k" -A -l "tofu-estate=$ESTATE" -o json 2>/dev/null | jq '[.items[]? | select((.metadata.ownerReferences // []) | length == 0)] | length' 2>/dev/null)"
    n=$((n + ${c:-0}))
  done
  printf '%s\n' "$n"
}

# argocd_own <kubectl fn>: what Argo CD itself wrote in $NS that the root
# never declared, as "kind/name uid" lines - the `default` AppProject and
# argocd-initial-admin-secret, both created by argocd-server with no owner
# reference - plus a digest of the server.secretkey argocd-server wrote into
# the DECLARED argocd-secret. The uids say the two objects were never
# deleted and recreated; the digest says the key was never wiped or
# regenerated. Printed sorted, so two reads compare as strings.
argocd_own() {
  local k
  {
    "$1" get appprojects.argoproj.io default -n "$NS" -o jsonpath='AppProject/default {.metadata.uid}{"\n"}' 2>/dev/null
    "$1" get secret argocd-initial-admin-secret -n "$NS" -o jsonpath='Secret/argocd-initial-admin-secret {.metadata.uid}{"\n"}' 2>/dev/null
    k="$("$1" get secret argocd-secret -n "$NS" -o jsonpath='{.data.server\.secretkey}' 2>/dev/null)"
    if [ -n "$k" ]; then printf 'Secret/argocd-secret server.secretkey sha256:%s\n' "$(printf '%s' "$k" | sha256_stdin | cut -c1-16)"; fi
  } | sort
}
argocd_own_a() { argocd_own kca; }
# labelled_a: the objects count_a counts, as kind/namespace/name lines on
# one line, for a failure line.
labelled_a() {
  local k
  for k in $KINDS; do
    kca get "$k" -A -l "tofu-estate=$ESTATE" -o json 2>/dev/null | jq -r '.items[]? | select((.metadata.ownerReferences // []) | length == 0) | "\(.kind)/\(.metadata.namespace // "")/\(.metadata.name)"' 2>/dev/null
  done | tr '\n' ' '
}
# argocd_own_labelled_a: how many of argocd-server's own two objects carry
# the estate's label. Nothing should ever put it there.
argocd_own_labelled_a() {
  local n=0
  [ -n "$(kca get appprojects.argoproj.io default -n "$NS" -o jsonpath='{.metadata.labels.tofu-estate}' 2>/dev/null)" ] && n=$((n + 1))
  [ -n "$(kca get secret argocd-initial-admin-secret -n "$NS" -o jsonpath='{.metadata.labels.tofu-estate}' 2>/dev/null)" ] && n=$((n + 1))
  printf '%s\n' "$n"
}

# argocd_ready <kubeconfig> <label>: bounded and loud. The five Deployments
# Available; the application controller's StatefulSet ready; argocd-server
# finished initialising (server.secretkey written into argocd-secret, the
# default AppProject and argocd-initial-admin-secret created - the
# undeclared objects day2_remove reads exist only from that point on); and
# the application controller has written a status into the estate's
# Application, which its repository's absence makes a ComparisonError.
ctrl_sts_ready() {
  local ready
  ready="$(kubectl --kubeconfig "$1" get statefulset "$FULL-application-controller" -n "$NS" -o jsonpath='{.status.readyReplicas}' 2>/dev/null)"
  [ "${ready:-0}" -ge 1 ]
}
server_initialised() {
  [ -n "$(kubectl --kubeconfig "$1" get secret argocd-secret -n "$NS" -o jsonpath='{.data.server\.secretkey}' 2>/dev/null)" ] || return 1
  kubectl --kubeconfig "$1" get appprojects.argoproj.io default -n "$NS" >/dev/null 2>&1 || return 1
  kubectl --kubeconfig "$1" get secret argocd-initial-admin-secret -n "$NS" >/dev/null 2>&1
}
# app_reconciled <kubeconfig> <name>: the controller wrote a status.
app_reconciled() {
  [ -n "$(kubectl --kubeconfig "$1" get applications.argoproj.io "$2" -n "$NS" -o jsonpath='{.status.sync.status}{.status.conditions[*].type}' 2>/dev/null)" ]
}
argocd_ready() {
  local cfg="$1" label="$2"
  gauntlet_k8s_wait_all "$cfg" "$NS" deployment Available 300 "$label" || return 1
  gauntlet_wait_until 300 "the application controller StatefulSet on $label to be ready" -- ctrl_sts_ready "$cfg" || return 1
  gauntlet_wait_until 300 "argocd-server on $label to write server.secretkey and create the default AppProject and argocd-initial-admin-secret" -- server_initialised "$cfg" || return 1
  gauntlet_wait_until 300 "the application controller on $label to write a status into the Application gauntlet" -- app_reconciled "$cfg" gauntlet
}

# rest_addr <kind> <namespace> <name>: one instance address of the rest block.
rest_addr() { printf 'kubernetes_manifest.rest["%s/%s/%s"]' "$1" "$2" "$3"; }
# planned <plan>: the addresses a plan proposes something for, one per line.
planned() { grep -E '^[[:space:]]*# .+ (will be|must be)' <<< "$1" | sed -E 's/^[[:space:]]*# //; s/ (will be|must be).*$//'; }
# destroyed / created / updated <plan>: the addresses a plan destroys,
# creates or updates in place, sorted, one per line; joined <lines>: the
# same on one line, "; "-separated.
destroyed() { { grep -E '^[[:space:]]*# .+ will be destroyed' <<< "$1" || true; } | sed -E 's/^[[:space:]]*# //; s/ will be destroyed.*$//' | sort; }
created() { { grep -E '^[[:space:]]*# .+ will be created' <<< "$1" || true; } | sed -E 's/^[[:space:]]*# //; s/ will be created.*$//' | sort; }
updated() { { grep -E '^[[:space:]]*# .+ will be updated in-place' <<< "$1" || true; } | sed -E 's/^[[:space:]]*# //; s/ will be updated in-place.*$//' | sort; }
joined() { tr '\n' ';' <<< "$1" | sed -E 's/;+$//; s/;/; /g'; }
# plan_lines <plan>: the plan's summary and every action header, indented,
# for the log - printed by every stage that compares addresses, whatever it
# decides, so a mismatch is readable from the log alone.
plan_lines() { { grep -E '^Plan:|^No changes|^[[:space:]]*# .+ (will be|must be)' <<< "$1" || true; } | sed -E 's/^[[:space:]]*/    /'; }
# objects_of: the addresses on stdin as the objects they name, one
# "kind|orphan_[<ns>_]<name>" line each, sorted - independent of the
# address a plan files the object under. An instance of any of the root's
# kubernetes_manifest blocks keyed kind/namespace/name, and the sweep's two
# orphan spellings (#1996: <typed resource>.orphan_[<ns>_]<name>, and
# kubernetes_manifest.orphan_<kind>_[<ns>_]<name>, kubesweep's
# OrphanResourceName and ManifestOrphanResourceName) all come out the same
# for the same object. Anything else is printed as "?|<address>", which
# never matches.
objects_of() {
  python3 -c '
import re, sys
def orphan(n, name):
    return re.sub(r"[^A-Za-z0-9_-]", "_", "orphan_" + (n + "_" if n else "") + name)
out = []
for a in sys.stdin.read().splitlines():
    a = a.strip()
    if not a:
        continue
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
'
}

# ── 1. cold_deploy: the control, the declared pre-apply, then the rest ───
gauntlet_begin_stage cold_deploy
log "=== 1. cold_deploy: two kind clusters; the un-targeted plan must fail, then the declared pre-apply on both sides ==="
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

CTRL="$(stock_a plan -input=false -no-color 2>&1)"; CTRL_RC=$?
if [ "${BREAK_PREAPPLY:-}" = "1" ]; then
  if [ "$CTRL_RC" -eq 0 ]; then
    fail "BREAK_PREAPPLY=1: the un-targeted plan succeeded, so the declared pre-apply is not load-bearing and this estate proves nothing about #1173"
  fi
  log "  BREAK_PREAPPLY=1: caught - the one-pass plan exited $CTRL_RC: $(grep -m1 'CRD may not be installed' <<< "$CTRL")"
  gauntlet_stage cold_deploy pass "BREAK_PREAPPLY=1 control: planning the whole root in one pass exits $CTRL_RC before creating anything, so 'one apply is enough' correctly fails; the real two-apply crossing is skipped"
else
  [ "$CTRL_RC" -ne 0 ] || fail "the un-targeted plan SUCCEEDED; the CRD plan-time refusal this estate exists to exercise is gone, and the declared pre-apply is now measuring nothing (delete it, or find out what changed)"
  grep -qF "CRD may not be installed" <<< "$CTRL" || { printf '%s\n' "$CTRL" | tail -20; fail "the un-targeted plan failed for some reason other than the missing CRD"; }
  CTRL_N="$(grep -c 'CRD may not be installed' <<< "$CTRL")"
  log "  control: the one-pass plan fails as documented, $CTRL_N missing-CRD refusal(s)"

  pre_apply_estate() { stock_a apply -auto-approve -input=false -no-color "$@" >/dev/null; }
  pre_apply_oracle() { stock_b apply -auto-approve -input=false -no-color "$@" >/dev/null; }
  gauntlet_pre_apply "$ESTATE" estate:pre_apply_estate oracle:pre_apply_oracle \
    || fail "the declared pre-apply failed"
  PRE_NOTE="$(gauntlet_pre_apply_note)" || fail "the pre-apply ran but produced no note to put in the verdict"
  [ "$(managed_n stock_a)" = "$PRE_N" ] || fail "the pre-apply on A left $(managed_n stock_a) managed instances in state, want $PRE_N (the Namespace and $CRD_N CRDs)"

  # Stock's main apply and rollout on B, the oracle, run while A's do
  # (#2037): its own root and kubeconfig, joined where B's wait used to run.
  oracle_cold_deploy() {
    export KUBECONFIG="$KCB" KUBE_CONFIG_PATH="$KCB"
    quiet stock_b apply -auto-approve -input=false -no-color || fail "stock's main apply failed on B"
    argocd_ready "$KCB" "cluster B" || fail "Argo CD never became ready on B"
  }
  gauntlet_side_start oracle_cold oracle_cold_deploy
  COLD_OUT="$(stock_a apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$COLD_OUT" | tail -20; fail "stock's main apply failed on A"; }
  grep -qF "Apply complete! Resources: $REST_N added, 0 changed, 0 destroyed" <<< "$COLD_OUT" || { printf '%s\n' "$COLD_OUT" | tail -5; fail "stock's main apply on A did not add exactly the $REST_N rendered non-CRD objects"; }

  [ -f "$STOCK/terraform.tfstate" ] || fail "stock left no terraform.tfstate on A"
  STOCK_N="$(managed_n stock_a)"
  [ "$STOCK_N" = "$TOTAL_N" ] || fail "stock's state holds $STOCK_N managed instances, want $TOTAL_N"
  argocd_ready "$KCA" "cluster A" || fail "Argo CD never became ready on A"
  gauntlet_side_wait oracle_cold || fail "$GAUNTLET_SIDE_FAIL"
  UNMARKED="$(count_a)"
  [ "$UNMARKED" = "0" ] || fail "$UNMARKED object(s) already carry tofu-estate=$ESTATE after a plain stock apply - this proves nothing"
  STOCK_REPLAN="$(stock_a plan -detailed-exitcode -input=false -no-color 2>&1)"; STOCK_REPLAN_RC=$?
  [ "$STOCK_REPLAN_RC" -eq 0 ] || { printf '%s\n' "$STOCK_REPLAN" | tail -20; fail "stock's own replan after its cold deploy is not empty (exit $STOCK_REPLAN_RC) - a provider/controller fight in the shape (argocd-server writes into argocd-secret, the controller into the Application), which would be blamed on choudoufu later"; }
  OWN="$(argocd_own_a | tr '\n' ';' | sed -E 's/;$//; s/;/; /g')"
  log "  $TOTAL_N objects on A ($PRE_N pre-applied + $REST_N), written by argocd-server: $OWN"
  gauntlet_stage cold_deploy pass "$TOTAL_N objects from plain terraform against kind $K8S_VER: argo-cd $CHART_VERSION (sha256 $CHART_SHA256) rendered by data.helm_template.argocd into $MANIFEST_N kubernetes_manifest instances over 14 kinds under three for_each blocks keyed kind/namespace/name (the CRDs, the rest, then the five Deployments and the StatefulSet after the Secrets and RBAC they read), plus the Namespace; a real terraform.tfstate with $TOTAL_N managed instances, zero tofu-estate labels read back with kubectl, the five Deployments Available, the application controller's StatefulSet ready, argocd-server initialised and a status written into the Application gauntlet, and stock's own replan empty with those controllers writing into declared objects. The identical shape cold-deployed by stock on a second cluster as every later stage's oracle. Two applies, and the first is declared: $PRE_NOTE. Control, run first on this same cluster: the un-targeted one-pass plan exits $CTRL_RC with $CTRL_N missing-CRD refusal(s) before creating anything, so the pre-apply is load-bearing. Written by argocd-server, declared nowhere: $OWN"
fi

# ── 2. migrate: live-import against stock's state ────────────────────────
gauntlet_begin_stage migrate
log "=== 2. migrate: live-import against the stock state file, read-only then -approve ==="
OWN_BEFORE="$(argocd_own_a)"
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
  OWN_LABELLED="$(argocd_own_labelled_a)"
  OWN_AFTER="$(argocd_own_a)"
  if [ "$LABELLED" = "$TOTAL_N" ] && [ "$OWN_LABELLED" = "0" ] && [ "$OWN_AFTER" = "$OWN_BEFORE" ]; then
    gauntlet_stage migrate pass "$TOTAL_N of $TOTAL_N stamped, 0 skipped (${SUMMARY_LINE:-no summary line}); every declared object carries tofu-estate=$ESTATE, counted back with kubectl across all 15 kinds including AppProjects and Applications. argocd-server's own default AppProject and argocd-initial-admin-secret carry no label and read back with the same uids, and the server.secretkey it wrote into the declared argocd-secret reads back with the same digest after the stamp. The eligibility line was: ${ELIGIBLE_LINE:-none}"
  else
    gauntlet_stage migrate fail "live-import reported $TOTAL_N stamped but $LABELLED declared object(s) carry tofu-estate=$ESTATE on the cluster, $OWN_LABELLED of argocd-server's own two objects carry it, and argocd-server's writes read [$(tr '\n' ';' <<< "$OWN_AFTER")] after the stamp against [$(tr '\n' ';' <<< "$OWN_BEFORE")] before"
  fi
else
  gauntlet_stage migrate fail "live-import -approve did not bind every instance: ${SUMMARY_LINE:-no summary line}"
fi

# ── 3. test_plan: replan from nothing ────────────────────────────────────
gauntlet_begin_stage test_plan
log "=== 3. test_plan: choudoufu plan with no state file, identities read with kubectl ==="
PLAN_OUT="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$PLAN_OUT" | tail -20; fail "the post-migration plan failed"; }
# The densest name group: one name, seven kinds, every one of them must be
# there as its own object; then the two custom kinds sharing gauntlet.
IDS_OK=1; IDS_MISSING=""
for spec in "networkpolicy $NS" "clusterrole -" "clusterrolebinding -" "role $NS" "rolebinding $NS" "service $NS" "deployment $NS"; do
  read -r kind where <<< "$spec"
  if [ "$where" = "-" ]; then
    kca get "$kind" "$FULL-server" >/dev/null 2>&1 || { IDS_OK=0; IDS_MISSING="$IDS_MISSING $kind/$FULL-server"; }
  else
    exists_a "$kind" "$FULL-server" || { IDS_OK=0; IDS_MISSING="$IDS_MISSING $NS/$kind/$FULL-server"; }
  fi
done
for kind in appprojects.argoproj.io applications.argoproj.io; do
  exists_a "$kind" gauntlet || { IDS_OK=0; IDS_MISSING="$IDS_MISSING $NS/$kind/gauntlet"; }
done
kca get crd applications.argoproj.io >/dev/null 2>&1 || { IDS_OK=0; IDS_MISSING="$IDS_MISSING crd/applications.argoproj.io"; }
if grep -q "No changes." <<< "$PLAN_OUT" && [ "$IDS_OK" = "1" ]; then
  gauntlet_stage test_plan pass "the plan with no state file is empty over $TOTAL_N instances whose for_each keys and identities come from the helm_template render the data-read phase read, with argocd-server's keys in argocd-secret and the controller's status in the Application proposed for nothing; $FULL-server, one name across seven kinds (NetworkPolicy, ClusterRole, ClusterRoleBinding, Role, RoleBinding, Service, Deployment), confirmed present as seven objects with kubectl, with gauntlet as both an AppProject and an Application and a CRD"
else
  PLAN_LINE="$(grep -E '^Plan:|No changes' <<< "$PLAN_OUT" | head -1 | sed 's/\.$//')"
  gauntlet_stage test_plan fail "the plan with no state file is not empty (${PLAN_LINE:-no plan line}) or an identity is missing (identities confirmed: $IDS_OK;${IDS_MISSING:- none missing}). Proposed: $(planned "$PLAN_OUT" | head -10 | tr '\n' ';')"
  printf '%s\n' "$PLAN_OUT" | tail -30
fi

# ── 4. test_apply: no-op apply ───────────────────────────────────────────
gauntlet_begin_stage test_apply
log "=== 4. test_apply: apply the empty plan; the labelled-object count and argocd-server's writes must not move ==="
BEFORE_N="$(count_a)"
OWN_BEFORE="$(argocd_own_a)"
NOOP_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$NOOP_OUT" | tail -20; fail "the no-op apply failed"; }
grep -qF "Apply complete! Resources: 0 added, 0 changed, 0 destroyed" <<< "$NOOP_OUT" || { printf '%s\n' "$NOOP_OUT" | tail -5; fail "the no-op apply changed something"; }
AFTER_N="$(count_a)"
[ "$BEFORE_N" = "$AFTER_N" ] || fail "labelled-object count moved across a no-op apply: $BEFORE_N -> $AFTER_N"
[ "$(argocd_own_a)" = "$OWN_BEFORE" ] || fail "argocd-server's writes moved across a no-op apply: [$(tr '\n' ';' <<< "$OWN_BEFORE")] -> [$(argocd_own_a | tr '\n' ';')]"
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed) over $TOTAL_N instances; declared objects carrying tofu-estate=$ESTATE unchanged at $BEFORE_N across the estate's 15 kinds, counted with kubectl, and argocd-server's default AppProject, argocd-initial-admin-secret and server.secretkey read back identical"

# ── 5. drift_reconverge: one object out of a seven-kind name group ───────
gauntlet_begin_stage drift_reconverge
DRIFT_ADDR="$(rest_addr NetworkPolicy "$NS" "$FULL-server")"
log "=== 5. drift_reconverge: the NetworkPolicy $FULL-server is deleted out of band on A and on B ==="
# A delete, not a patch: every object here is server-side applied, and a
# kubectl patch makes kubectl a field manager (reference-k8s-cert-manager
# measured the conflict on both sides). The deleted object shares its name
# with six objects of other kinds, so the plan has to name it by kind.
kca delete networkpolicy "$FULL-server" -n "$NS" >/dev/null || fail "could not delete the NetworkPolicy out of band on A"
kcb delete networkpolicy "$FULL-server" -n "$NS" >/dev/null || fail "could not delete the NetworkPolicy out of band on B"
if [ "${BREAK:-}" = "1" ]; then
  kca delete role "$FULL-server" -n "$NS" >/dev/null || fail "BREAK: could not delete a second object on A"
fi
ORACLE_PLAN="$(stock_b plan -detailed-exitcode -input=false -no-color 2>&1)"; ORACLE_RC=$?
[ "$ORACLE_RC" -eq 2 ] || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B after the out-of-band delete exited $ORACLE_RC, want 2 (changes)"; }
grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$ORACLE_PLAN" || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B does not propose exactly one create"; }
grep -qF "$DRIFT_ADDR" <<< "$ORACLE_PLAN" || { printf '%s\n' "$ORACLE_PLAN" | tail -30; fail "stock's plan on B does not name $DRIFT_ADDR"; }
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
  exists_a networkpolicy "$FULL-server" || fail "the NetworkPolicy does not exist after reconverging"
  [ "$(count_a)" = "$TOTAL_N" ] || fail "$(count_a) labelled objects after reconverging, want $TOTAL_N - the recreated object did not get its marker"
  gauntlet_stage drift_reconverge pass "a NetworkPolicy that shares its name ($FULL-server) with six objects of other kinds was deleted out of band with kubectl; choudoufu proposed putting back exactly $DRIFT_ADDR (1 add, 0 change, 0 destroy) and nothing for the six same-named objects, matching stock's own plan on the oracle cluster; apply created 1 and the recreated object carries the estate label again ($TOTAL_N labelled). A delete rather than a patch because every object is server-side applied. BREAK=1 deletes the same-named Role as well and the single-object assertion correctly fails"
fi

# ── 6. plan_approval: plan -out, the world moves, apply refuses ──────────
gauntlet_begin_stage plan_approval
log "=== 6. plan_approval: a values edit saved as a plan, an out-of-band move, a refusal; then the same file applies ==="
RBAC_ADDR="$(rest_addr ConfigMap "$NS" argocd-rbac-cm)"
rbac_default_a() { kca get configmap argocd-rbac-cm -n "$NS" -o jsonpath='{.data.policy\.default}'; }
set_value "$ADOPTED" "    policy.default: role:readonly" "    policy.default: role:admin" || fail "could not edit configs.rbac's policy.default in the adopted root"
set_value "$ORACLE"  "    policy.default: role:readonly" "    policy.default: role:admin" || fail "could not edit configs.rbac's policy.default in the oracle root"
P_PLAN="$(tofu_a plan -out=approved.tfplan -input=false -no-color 2>&1)"; P_PLAN_RC=$?
P_PLAN_LINE="$(grep -E '^Plan:|^No changes' <<< "$P_PLAN" | head -1 | sed 's/\.$//')"
if [ "$P_PLAN_RC" -ne 0 ] || ! grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$P_PLAN" || ! grep -qF "$RBAC_ADDR will be updated in-place" <<< "$P_PLAN"; then
  gauntlet_stage plan_approval fail "configs.rbac's policy.default role:readonly -> role:admin in values.yaml did not plan as exactly one in-place update of $RBAC_ADDR: ${P_PLAN_LINE:-no summary line} (exit $P_PLAN_RC). What it proposed: $(planned "$P_PLAN" | head -10 | tr '\n' ';')"
  printf '%s\n' "$P_PLAN" | tail -30
  quiet tofu_a apply -auto-approve -input=false -no-color || fail "could not converge A after plan_approval; nothing below would measure day-2 behaviour"
  quiet stock_b apply -auto-approve -input=false -no-color || fail "could not converge B after plan_approval; the oracle would be stale for every stage below"
  PLAN_APPROVAL_SKIPPED=1
fi
if [ -z "${PLAN_APPROVAL_SKIPPED:-}" ]; then
# The world moves on a spec-side field of an object the saved plan does not
# touch: the repo server's Deployment declares replicas: 1, and it is put
# back below.
kca patch deployment "$FULL-repo-server" -n "$NS" --type merge -p '{"spec":{"replicas":2}}' >/dev/null || fail "could not move the world (the repo server's replicas) on A"
P_APPLY="$(tofu_a apply -input=false -no-color approved.tfplan 2>&1)"; P_RC=$?
if [ "${BREAK_APPROVAL:-}" = "1" ]; then
  [ "$P_RC" -eq 0 ] && fail "BREAK_APPROVAL=1: applying the saved plan after the world moved succeeded - the refusal is not load-bearing"
  log "  BREAK_APPROVAL=1: caught - the apply after the world moved exited $P_RC"
  kca patch deployment "$FULL-repo-server" -n "$NS" --type merge -p '{"spec":{"replicas":1}}' >/dev/null
  quiet tofu_a apply -input=false -no-color approved.tfplan || fail "BREAK_APPROVAL: the saved plan did not apply once the world was put back"
  quiet stock_b apply -auto-approve -input=false -no-color || fail "BREAK_APPROVAL: stock could not apply the change on B"
  gauntlet_stage plan_approval pass "BREAK_APPROVAL=1 control: applying the saved plan after the world moved exited $P_RC (refused), so the stage's own Break line correctly fails; applied once the world was put back"
else
  if [ "$P_RC" -ne 3 ] || ! grep -qF "The approved plan no longer matches the live system" <<< "$P_APPLY"; then
    printf '%s\n' "$P_APPLY" | tail -20
    gauntlet_stage plan_approval fail "the saved plan was applied after the world had moved out of band (the repo server's replicas 1 -> 2, kubectl) and choudoufu exited $P_RC rather than refusing at 3 with \"The approved plan no longer matches the live system\". The saved plan's own change was one in-place update of $RBAC_ADDR from a values edit"
    kca patch deployment "$FULL-repo-server" -n "$NS" --type merge -p '{"spec":{"replicas":1}}' >/dev/null
    quiet tofu_a apply -auto-approve -input=false -no-color || fail "could not converge A after plan_approval; nothing below would measure day-2 behaviour"
    quiet stock_b apply -auto-approve -input=false -no-color || fail "could not converge B after plan_approval; the oracle would be stale for every stage below"
    PLAN_APPROVAL_SKIPPED=1
  fi
  if [ -z "${PLAN_APPROVAL_SKIPPED:-}" ]; then
  RBAC_NOW="$(rbac_default_a)"
  [ "$RBAC_NOW" = "role:readonly" ] || fail "argocd-rbac-cm's policy.default reads $RBAC_NOW despite the refusal, want role:readonly"
  kca patch deployment "$FULL-repo-server" -n "$NS" --type merge -p '{"spec":{"replicas":1}}' >/dev/null || fail "could not put the world back"
  P_APPLY2="$(tofu_a apply -input=false -no-color approved.tfplan 2>&1)" || { printf '%s\n' "$P_APPLY2" | tail -20; fail "the saved plan did not apply once the world was put back"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$P_APPLY2" || { printf '%s\n' "$P_APPLY2" | tail -30; fail "the saved plan's apply did not change exactly one object"; }
  [ "$(rbac_default_a)" = "role:admin" ] || fail "argocd-rbac-cm does not read policy.default role:admin after the saved plan applied"
  O_PA="$(stock_b plan -out=approved.tfplan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PA" | tail -30; fail "stock's own planfile failed on B"; }
  grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$O_PA" || { printf '%s\n' "$O_PA" | tail -10; fail "stock's plan for the same values edit on B is not exactly one in-place update"; }
  quiet stock_b apply -input=false -no-color approved.tfplan || fail "stock's own planfile did not apply on B"
  gauntlet_stage plan_approval pass "a values.yaml edit (configs.rbac's policy.default role:readonly -> role:admin) re-rendered through helm_template planned as exactly one in-place update of $RBAC_ADDR, which no pod template checksums, saved with plan -out; the world then moved out of band (the repo server's replicas to 2 with kubectl, never choudoufu) and apply of the saved plan refused with \"The approved plan no longer matches the live system\" at exit 3, nothing applied (kubectl still reads role:readonly); with the world put back the identical file applied, 0 added, 1 changed, 0 destroyed, and role:admin reads back; stock's plan for the same edit on the oracle cluster is the same one update and its planfile applied. BREAK_APPROVAL=1 expects success after the move and correctly fails"
  fi
fi
fi

# ── 7. day2_rename: a moved block over a for_each of CRDs ────────────────
gauntlet_begin_stage day2_rename
log "=== 7. day2_rename: kubernetes_manifest.crds becomes .custom_resource_definitions through a moved block ==="
rename_crds "$ADOPTED" || fail "could not rename the CRD block in the adopted root"
rename_crds "$ORACLE"  || fail "could not rename the CRD block in the oracle root"
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's moved-block plan failed on B"; }
grep -qE "^No changes|Plan: 0 to add, 0 to change, 0 to destroy" <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's moved-block plan on B is not zero churn"; }
quiet stock_b apply -auto-approve -input=false -no-color || fail "stock's moved-block apply failed on B"
R_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_PLAN" | tail -20; fail "the moved-block plan failed"; }
# The renderer aligns a map's "=" across its keys, and every chart CRD
# carries an annotation of its own (helm.sh/resource-policy: keep), so the
# address line reads `"...address"   =` with padding: one or more spaces
# before the "=".
R_ANN="$(grep -cE '~ +"choudoufu\.intentius\.io/tofu-address" += ".*" -> ".*"' <<< "$R_PLAN")"
if grep -qE 'will be (created|destroyed)|must be replaced' <<< "$R_PLAN"; then
  gauntlet_stage day2_rename fail "the moved-block plan over $CRD_N for_each CRD instances proposes a create, a destroy or a replace - not the marker rewritten in place: $(grep -E '^Plan:' <<< "$R_PLAN" | head -1)"
  printf '%s\n' "$R_PLAN" | tail -20
  quiet tofu_a apply -auto-approve -input=false -no-color || fail "could not converge after the rename; nothing below would measure day-2 behaviour"
elif grep -qF "Plan: 0 to add, $CRD_N to change, 0 to destroy." <<< "$R_PLAN" && [ "$R_ANN" = "$CRD_N" ]; then
  R_APPLY_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$R_APPLY_OUT" | tail -20; fail "the moved-block apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, $CRD_N changed, 0 destroyed" <<< "$R_APPLY_OUT" || { printf '%s\n' "$R_APPLY_OUT" | tail -10; fail "the moved-block apply was not exactly $CRD_N in-place changes"; }
  [ "$(kca get crd -o name | grep -c '\.argoproj\.io$')" = "$CRD_N" ] || fail "the CRD count moved across the rename"
  [ "$(count_a)" = "$TOTAL_N" ] || fail "$(count_a) labelled objects after the rename, want $TOTAL_N"
  R_REPLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_REPLAN" | tail -30; fail "the replan after the rename failed"; }
  grep -q "No changes." <<< "$R_REPLAN" || { printf '%s\n' "$R_REPLAN" | tail -20; fail "the replan after the rename is not empty"; }
  gauntlet_stage day2_rename pass "moved block over a for_each of $CRD_N CRDs keyed by the render (kubernetes_manifest.crds -> .custom_resource_definitions, with the rest block's depends_on following it): no add and no destroy, $CRD_N in-place changes each confined to the address annotation rewrite ($R_ANN annotation lines, 0 add, $CRD_N change, 0 destroy) - the marker rewritten in place, as the AWS lanes assert for a rename; every CRD still there and labelled, the replan empty; stock's plan for the same moved block on the oracle cluster is zero churn, since stock never writes this annotation"
else
  gauntlet_stage day2_rename fail "the moved-block plan over $CRD_N for_each CRD instances is not exactly $CRD_N in-place annotation changes: $(grep -E '^Plan:' <<< "$R_PLAN" | head -1), $R_ANN annotation line(s)"
  printf '%s\n' "$R_PLAN" | tail -20
  quiet tofu_a apply -auto-approve -input=false -no-color || fail "could not converge after the rename; nothing below would measure day-2 behaviour"
fi

# ── 8. day2_remove: a values change takes a component out of the render ──
gauntlet_begin_stage day2_remove
NOTIF_NP_ADDR="$(rest_addr NetworkPolicy "$NS" "$FULL-repo-server")"
log "=== 8. day2_remove: notifications.enabled=false removes $NOTIF_N rendered objects and edits one; argocd-server's own objects must be left alone ==="
# set_notifications <dir> <true|false>: the one `  enabled:` line under the
# top-level notifications key. Several top-level keys carry an `enabled:`,
# so this edits the first one after `notifications:` and nothing else.
set_notifications() {
  python3 - "$1/values.yaml" "$2" <<'PY'
import sys
p, to = sys.argv[1], sys.argv[2]
frm = "false" if to == "true" else "true"
lines = open(p).read().split("\n")
start = lines.index("notifications:")
for i in range(start + 1, len(lines)):
    if lines[i] and not lines[i].startswith(" "):
        raise SystemExit("no `  enabled:` line under notifications:")
    if lines[i] == "  enabled: " + frm:
        lines[i] = "  enabled: " + to
        break
    assert not lines[i].startswith("  enabled:"), "notifications' enabled already reads %r" % lines[i]
else:
    raise SystemExit("no `  enabled:` line under notifications:")
open(p, "w").write("\n".join(lines))
PY
}
OWN_BEFORE="$(argocd_own_a)"
[ "$(grep -c . <<< "$OWN_BEFORE")" = "3" ] || fail "argocd-server's own writes are not all there before the removal (want the default AppProject, argocd-initial-admin-secret and server.secretkey): $(tr '\n' ';' <<< "$OWN_BEFORE")"
if [ "${BREAK_REMOVE:-}" = "1" ]; then
  K_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$K_PLAN" | tail -30; fail "BREAK_REMOVE: the plan with notifications kept failed"; }
  grep -qE "will be destroyed|[1-9][0-9]* to destroy" <<< "$K_PLAN" && fail "BREAK_REMOVE=1: with notifications kept a destroy was still proposed - the destroys below would not be the values change's doing"
  log "  BREAK_REMOVE=1: caught - with notifications kept the plan proposes no destroy"
  gauntlet_stage day2_remove pass "BREAK_REMOVE=1 control: with notifications kept, no destroy is proposed; the real check is skipped"
  { set_notifications "$ADOPTED" false && set_notifications "$ORACLE" false; } || fail "BREAK_REMOVE: could not disable notifications afterwards"
  quiet tofu_a apply -auto-approve -input=false -no-color || fail "BREAK_REMOVE: the removal apply failed afterwards"
  quiet stock_b apply -auto-approve -input=false -no-color || fail "BREAK_REMOVE: stock's removal apply failed on B afterwards"
else
  # Every way choudoufu can miss is recorded here and the stage carries on,
  # so the removal is in effect on both sides and every later stage is
  # measured: one fail verdict at the end with all of them. fail (which
  # exits) is kept for what leaves nothing to measure - the oracle, a
  # command that could not run.
  REMOVE_FAILS=""
  remove_fail() { REMOVE_FAILS="${REMOVE_FAILS:+$REMOVE_FAILS; }$*"; }
  # The eight objects the values change takes out of the render, as
  # "kubectl-kind namespace name" ("-" for cluster-scoped), and the
  # addresses stock destroys them at.
  NOTIF_OBJECTS="deployment $NS $FULL-notifications-controller
serviceaccount $NS argocd-notifications-controller
configmap $NS argocd-notifications-cm
role $NS $FULL-notifications-controller
rolebinding $NS $FULL-notifications-controller
networkpolicy $NS $FULL-notifications-controller
clusterrole - $FULL-notifications-controller
clusterrolebinding - $FULL-notifications-controller"
  O_WANT_DEST="$(printf '%s\n' \
    "$(rest_addr ServiceAccount "$NS" argocd-notifications-controller)" \
    "$(rest_addr ConfigMap "$NS" argocd-notifications-cm)" \
    "$(rest_addr Role "$NS" "$FULL-notifications-controller")" \
    "$(rest_addr RoleBinding "$NS" "$FULL-notifications-controller")" \
    "$(rest_addr NetworkPolicy "$NS" "$FULL-notifications-controller")" \
    "$(rest_addr ClusterRole "" "$FULL-notifications-controller")" \
    "$(rest_addr ClusterRoleBinding "" "$FULL-notifications-controller")" \
    "kubernetes_manifest.late[\"Deployment/$NS/$FULL-notifications-controller\"]" | sort)"
  set_notifications "$ADOPTED" false || fail "could not disable notifications in the adopted root"
  set_notifications "$ORACLE" false  || fail "could not disable notifications in the oracle root"

  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -30; fail "stock's remove plan failed on B"; }
  log "  stock's remove plan on B:"; plan_lines "$O_PLAN"
  grep -qF "Plan: 0 to add, 1 to change, $NOTIF_N to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -30; fail "stock's remove plan on B is not exactly $NOTIF_N destroys and one update"; }
  O_DEST="$(destroyed "$O_PLAN")"
  O_UPD="$(updated "$O_PLAN")"
  [ "$O_DEST" = "$O_WANT_DEST" ] || fail "stock's remove plan on B does not destroy exactly the eight notifications instances: [$(joined "$O_DEST")], want [$(joined "$O_WANT_DEST")]"
  [ "$O_UPD" = "$NOTIF_NP_ADDR" ] || fail "stock's remove plan's one update on B is not $NOTIF_NP_ADDR: [$(joined "$O_UPD")]"
  O_OBJS="$(objects_of <<< "$O_DEST")"
  quiet stock_b apply -auto-approve -input=false -no-color || fail "stock's remove apply failed on B"

  D_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$D_PLAN" | tail -30; fail "the remove plan failed"; }
  log "  choudoufu's remove plan on A:"; plan_lines "$D_PLAN"
  D_LINE="$(gauntlet_plan_line <<< "$D_PLAN")"
  D_DEST="$(destroyed "$D_PLAN")"
  D_UPD="$(updated "$D_PLAN")"
  D_OBJS="$(objects_of <<< "$D_DEST")"
  grep -qF "Plan: 0 to add, 1 to change, $NOTIF_N to destroy." <<< "$D_PLAN" \
    || remove_fail "the remove plan is ${D_LINE:-no plan line}, not exactly $NOTIF_N destroys and one update; it proposes [$(joined "$(planned "$D_PLAN")")]"
  # The in-place update is a declared instance that stays in the render, so
  # nothing about #1996 touches it: it must be stock's, exactly.
  [ "$D_UPD" = "$O_UPD" ] \
    || remove_fail "the remove plan's in-place update(s) [$(joined "$D_UPD")] are not stock's [$(joined "$O_UPD")]"
  # The destroys by address first, the way stock plans them. A difference is
  # #1996 (the sweep files a for_each instance that left the render under
  # <typed resource>.orphan_..., although the object carries the
  # tofu-address annotation naming its instance) and is a fail; it is never
  # accepted as a pass.
  if [ "$D_DEST" != "$O_DEST" ]; then
    remove_fail "choudoufu's remove plan destroys at addresses that differ from stock's: choudoufu [$(joined "$D_DEST")], stock [$(joined "$O_DEST")] (#1996)"
  fi
  # Then by object, whatever the address: the same kind/namespace/name set.
  if [ "$D_OBJS" != "$O_OBJS" ]; then
    remove_fail "choudoufu's remove plan does not destroy the same objects as stock's: choudoufu [$(joined "$D_OBJS")], stock [$(joined "$O_OBJS")]"
  fi
  # The undeclared-object half: nothing argocd-server created may be named,
  # by name in any action header or as an object.
  D_ALL_OBJS="$(planned "$D_PLAN" | objects_of)"
  for own in "appproject|orphan_${NS}_default" "secret|orphan_${NS}_argocd-initial-admin-secret"; do
    grep -qxF "$own" <<< "$D_ALL_OBJS" && remove_fail "the remove plan proposes something for ${own%%|*} ${own#*orphan_"${NS}"_}, which argocd-server created and the root never declared"
  done
  grep -qF "argocd-initial-admin-secret" <<< "$(planned "$D_PLAN")" && remove_fail "the remove plan names argocd-initial-admin-secret: [$(joined "$(planned "$D_PLAN")")]"

  # Apply the removal whatever the plan's addresses, so the world is where
  # day2_count expects it; then read the objects back by kind/name.
  APPLY_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)"; APPLY_RC=$?
  if [ "$APPLY_RC" -ne 0 ] || ! grep -qF "0 added, 1 changed, $NOTIF_N destroyed" <<< "$APPLY_OUT"; then
    printf '%s\n' "$APPLY_OUT" | tail -30
    remove_fail "the remove apply exited $APPLY_RC and did not report exactly $NOTIF_N destroyed and one changed: $(grep -E '^Apply complete' <<< "$APPLY_OUT" | head -1) $(gauntlet_first_error_line <<< "$APPLY_OUT")"
  fi
  STILL=""
  while read -r kind where name; do
    [ -n "$kind" ] || continue
    if [ "$where" = "-" ]; then
      if kca get "$kind" "$name" >/dev/null 2>&1; then STILL="$STILL $kind/$name"; kca delete "$kind" "$name" --ignore-not-found >/dev/null 2>&1; fi
    else
      if kca get "$kind" "$name" -n "$where" >/dev/null 2>&1; then STILL="$STILL $where/$kind/$name"; kca delete "$kind" "$name" -n "$where" --ignore-not-found >/dev/null 2>&1; fi
    fi
  done <<< "$NOTIF_OBJECTS"
  [ -z "$STILL" ] || remove_fail "these notifications objects still existed after the remove apply (deleted with kubectl so the next stage starts where it expects):$STILL"
  exists_a secret argocd-notifications-secret || remove_fail "argocd-notifications-secret is gone; it is declared through extraObjects, which the values change kept"
  exists_a deployment "$FULL-server" || remove_fail "the Deployment $FULL-server is gone; the removal reached an object the values change kept"
  NP_FROM="$(kca get networkpolicy "$FULL-repo-server" -n "$NS" -o jsonpath='{.spec.ingress[0].from[*].podSelector.matchLabels.app\.kubernetes\.io/name}' 2>&1)"
  grep -q 'notifications' <<< "$NP_FROM" && remove_fail "the repo server's NetworkPolicy still admits the notifications controller after the remove apply: $NP_FROM"
  # The update's result, compared object to object with what stock's update
  # left on the oracle cluster.
  NP_SPEC_A="$(kca get networkpolicy "$FULL-repo-server" -n "$NS" -o json 2>/dev/null | jq -cS '.spec' 2>/dev/null)"
  NP_SPEC_B="$(kcb get networkpolicy "$FULL-repo-server" -n "$NS" -o json 2>/dev/null | jq -cS '.spec' 2>/dev/null)"
  { [ -n "$NP_SPEC_A" ] && [ "$NP_SPEC_A" = "$NP_SPEC_B" ]; } \
    || remove_fail "the repo server's NetworkPolicy spec after choudoufu's update on A ($NP_SPEC_A) is not what stock's update left on B ($NP_SPEC_B)"
  OWN_AFTER="$(argocd_own_a)"
  [ "$OWN_AFTER" = "$OWN_BEFORE" ] || remove_fail "argocd-server's own writes changed across the removal: before [$(joined "$OWN_BEFORE")], after [$(joined "$OWN_AFTER")]"
  D_REPLAN="$(tofu_a plan -input=false -no-color 2>&1)"; D_REPLAN_RC=$?
  if [ "$D_REPLAN_RC" -ne 0 ] || ! grep -q "No changes." <<< "$D_REPLAN"; then
    printf '%s\n' "$D_REPLAN" | tail -30
    remove_fail "the replan after the remove is not empty (exit $D_REPLAN_RC, $(gauntlet_plan_line <<< "$D_REPLAN")): [$(joined "$(planned "$D_REPLAN")")] $(gauntlet_first_error_line <<< "$D_REPLAN")"
    quiet tofu_a apply -auto-approve -input=false -no-color || fail "could not converge A after the remove; nothing below would measure day-2 behaviour${REMOVE_FAILS:+ (this stage also found: $REMOVE_FAILS)}"
  fi
  REMAIN=$((TOTAL_N - NOTIF_N))
  R_COUNT="$(count_a)"
  [ "$R_COUNT" = "$REMAIN" ] || remove_fail "$R_COUNT labelled objects after the remove, want $REMAIN: $(labelled_a)"

  if [ -n "$REMOVE_FAILS" ]; then
    gauntlet_stage day2_remove fail "notifications.enabled=false in values.yaml (eight objects out of the render and one ingress rule withdrawn from the repo server's NetworkPolicy; stock on the oracle destroys [$(joined "$O_DEST")] and updates [$(joined "$O_UPD")]): $REMOVE_FAILS. The removal was applied on both sides regardless and read back with kubectl, so every later stage starts where it expects"
  else
    gauntlet_stage day2_remove pass "notifications.enabled=false in values.yaml took $NOTIF_N objects out of the render - the controller's Deployment, ServiceAccount, Role, RoleBinding, ClusterRole, ClusterRoleBinding, NetworkPolicy and ConfigMap, across the rest and late blocks and two scopes - and withdrew the notifications controller's ingress rule from the repo server's NetworkPolicy; choudoufu proposed exactly $NOTIF_N destroys at the instances' own addresses ($(joined "$D_DEST")) and the one in-place update of $NOTIF_NP_ADDR, the same addresses and the same objects as stock's plan on the oracle cluster; after the apply all eight are gone and the NetworkPolicy's spec reads back identical to the one stock's update left on the oracle (kubectl), the replan empty and $REMAIN objects labelled. The undeclared-object exclusion: argocd-server's own default AppProject and argocd-initial-admin-secret, which carry no owner reference and no label, were never proposed and read back with the same uids, and the server.secretkey it wrote into the declared argocd-secret reads back with the same digest ($(joined "$OWN_AFTER")). BREAK_REMOVE=1 keeps notifications and requires no destroy"
  fi
fi

# ── 9. day2_count: for_each over the render, scaled through values ───────
# day2_remove's removal stays in effect on A and B from here to the
# teardown (the eight notifications objects are gone, which every count
# below reads from the cluster); greenfield starts again from the committed
# root.
gauntlet_begin_stage day2_count
log "=== 9. day2_count: Applications in extraObjects 0 -> 2 -> 1 -> 2, each entry one rendered Application ==="
SHARD0="$(rest_addr Application "$NS" gauntlet-shard-0)"
SHARD1="$(rest_addr Application "$NS" gauntlet-shard-1)"
# As in day2_remove: a miss is recorded and the stage carries on to the
# withdrawal, so day2_replace starts from a converged estate; one verdict at
# the end.
COUNT_FAILS=""
count_fail() { COUNT_FAILS="${COUNT_FAILS:+$COUNT_FAILS; }$*"; }
# shards_a: the shard Applications on A, for a failure line.
shards_a() { kca get applications.argoproj.io -n "$NS" -o name 2>/dev/null | grep shard | tr '\n' ' '; }
set_shards "$ADOPTED" 2 || fail "could not add two Application shards to the adopted root"
set_shards "$ORACLE" 2  || fail "could not add two Application shards to the oracle root"
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -30; fail "stock's plan for the two shards failed on B"; }
grep -qF "Plan: 2 to add, 0 to change, 0 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -30; fail "stock's plan for the two shards on B is not exactly two adds"; }
O_UP_CREATED="$(created "$O_PLAN")"
[ "$O_UP_CREATED" = "$(printf '%s\n' "$SHARD0" "$SHARD1" | sort)" ] || { plan_lines "$O_PLAN"; fail "stock's plan for the two shards on B does not create $SHARD0 and $SHARD1"; }
quiet stock_b apply -auto-approve -input=false -no-color || fail "stock could not create the shards on B"
set_shards "$ORACLE" 1 || fail "could not scale the oracle root to 1"
O_DOWN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_DOWN" | tail -30; fail "stock's scale-down plan failed on B"; }
grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$O_DOWN" || { printf '%s\n' "$O_DOWN" | tail -30; fail "stock's scale-down plan on B is not exactly one destroy"; }
O_DOWN_DEST="$(destroyed "$O_DOWN")"
[ "$O_DOWN_DEST" = "$SHARD1" ] || { plan_lines "$O_DOWN"; fail "stock's scale-down on B does not destroy exactly $SHARD1: [$(joined "$O_DOWN_DEST")]"; }
O_DOWN_OBJS="$(objects_of <<< "$O_DOWN_DEST")"
quiet stock_b apply -auto-approve -input=false -no-color || fail "stock's scale-down apply failed on B"

A_UP="$(tofu_a plan -input=false -no-color 2>&1)"; A_UP_RC=$?
log "  choudoufu's plan for two shards on A:"; plan_lines "$A_UP"
UP_OK=1
if [ "$A_UP_RC" -ne 0 ] || ! grep -qF "Plan: 2 to add, 0 to change, 0 to destroy." <<< "$A_UP" || [ "$(created "$A_UP")" != "$O_UP_CREATED" ]; then
  printf '%s\n' "$A_UP" | tail -30
  count_fail "choudoufu could not plan the two rendered shards at stock's addresses: exit $A_UP_RC, $(gauntlet_plan_line <<< "$A_UP"), creating [$(joined "$(created "$A_UP")")] where stock creates [$(joined "$O_UP_CREATED")]; first error: $(gauntlet_first_error_line <<< "$A_UP")"
  UP_OK=0
else
  APPLY_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)"; APPLY_RC=$?
  if [ "$APPLY_RC" -ne 0 ] || ! grep -qF "2 added, 0 changed, 0 destroyed" <<< "$APPLY_OUT"; then
    printf '%s\n' "$APPLY_OUT" | tail -30
    count_fail "the shards' creating apply exited $APPLY_RC and did not report exactly two added: $(gauntlet_first_error_line <<< "$APPLY_OUT")"
    UP_OK=0
  elif ! { exists_a applications.argoproj.io gauntlet-shard-0 && exists_a applications.argoproj.io gauntlet-shard-1; }; then
    count_fail "both shards do not exist after choudoufu created them: [$(shards_a)]"
    UP_OK=0
  # The controller writes a status into each new Application (the CRD has
  # no status subresource, so into the main resource); the replan has to
  # propose nothing for it.
  elif ! { gauntlet_wait_until 120 "the application controller to write a status into gauntlet-shard-0" -- app_reconciled "$KCA" gauntlet-shard-0 \
      && gauntlet_wait_until 120 "the application controller to write a status into gauntlet-shard-1" -- app_reconciled "$KCA" gauntlet-shard-1; }; then
    count_fail "the application controller never wrote a status into both shards within 120s: $(kca get applications.argoproj.io gauntlet-shard-0 gauntlet-shard-1 -n "$NS" -o jsonpath='{range .items[*]}{.metadata.name} sync={.status.sync.status} conditions={.status.conditions[*].type}{"; "}{end}' 2>&1)"
    UP_OK=0
  else
    A_RE="$(tofu_a plan -input=false -no-color 2>&1)"; A_RE_RC=$?
    if [ "$A_RE_RC" -ne 0 ] || ! grep -q "No changes." <<< "$A_RE"; then
      printf '%s\n' "$A_RE" | tail -30
      count_fail "replanning the root choudoufu itself had just applied with two shards, after the application controller had written their status, is not empty (exit $A_RE_RC, $(gauntlet_plan_line <<< "$A_RE")); proposed: [$(joined "$(planned "$A_RE")")]; first error: $(gauntlet_first_error_line <<< "$A_RE")"
      UP_OK=0
    fi
  fi
fi

if [ "$UP_OK" = "1" ]; then
  set_shards "$ADOPTED" 1 || fail "could not scale the adopted root to 1"
  C_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$C_PLAN" | tail -30; fail "the scale-down plan failed"; }
  log "  choudoufu's scale-down plan on A:"; plan_lines "$C_PLAN"
  C_DEST="$(destroyed "$C_PLAN")"
  C_ADDR="$(joined "$C_DEST")"
  C_OBJS="$(objects_of <<< "$C_DEST")"
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$C_PLAN" \
    || count_fail "the scale-down plan is $(gauntlet_plan_line <<< "$C_PLAN"), not exactly one destroy: [$(joined "$(planned "$C_PLAN")")]"
  # The object must be stock's (shard-1). The address is reported, not
  # judged: a different address here is the same #1996 day2_remove judges,
  # and this stage measures which instance goes.
  [ "$C_OBJS" = "$O_DOWN_OBJS" ] \
    || count_fail "the scale-down plan does not destroy the object stock's does: choudoufu [$(joined "$C_OBJS")], stock [$(joined "$O_DOWN_OBJS")]"
  if [ "$C_DEST" = "$O_DOWN_DEST" ]; then
    C_ADDR_NOTE="at stock's own address"
  else
    C_ADDR_NOTE="at an address that differs from stock's $O_DOWN_DEST (#1996, judged in day2_remove)"
  fi
  APPLY_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)"; APPLY_RC=$?
  if [ "$APPLY_RC" -ne 0 ] || ! grep -qF "0 added, 0 changed, 1 destroyed" <<< "$APPLY_OUT"; then
    printf '%s\n' "$APPLY_OUT" | tail -30
    count_fail "the scale-down apply exited $APPLY_RC and did not report exactly one destroyed: $(gauntlet_first_error_line <<< "$APPLY_OUT")"
  fi
  if [ "${BREAK_COUNT:-}" = "1" ]; then
    exists_a applications.argoproj.io gauntlet-shard-0 || fail "BREAK_COUNT=1: shard-0 was destroyed - the 'wrong instance' assertion would hold, so the check is not load-bearing"
    log "  BREAK_COUNT=1: caught - shard-0 still exists, so asserting it was the one destroyed correctly fails"
    COUNT_BREAK_DONE=1
  else
    exists_a applications.argoproj.io gauntlet-shard-0 || count_fail "shard-0 was destroyed on the scale-down: [$(shards_a)]"
    exists_a applications.argoproj.io gauntlet-shard-1 && count_fail "shard-1 still exists after the scale-down: [$(shards_a)]"
    { set_shards "$ADOPTED" 2 && set_shards "$ORACLE" 2; } || fail "could not scale back to 2"
    O_UP="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_UP" | tail -30; fail "stock's scale-up plan failed on B"; }
    grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_UP" || { printf '%s\n' "$O_UP" | tail -30; fail "stock's scale-up plan on B is not exactly one add"; }
    quiet stock_b apply -auto-approve -input=false -no-color || fail "stock's scale-up apply failed on B"
    # Creates are never orphan-named, so this holds whatever the destroy
    # above was filed under - and it shows an orphan-named destroy left no
    # record behind that would stop the instance coming back.
    U_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$U_PLAN" | tail -30; fail "the scale-up plan failed"; }
    log "  choudoufu's scale-up plan on A:"; plan_lines "$U_PLAN"
    if ! grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$U_PLAN" || [ "$(created "$U_PLAN")" != "$(created "$O_UP")" ]; then
      count_fail "the scale-up plan is $(gauntlet_plan_line <<< "$U_PLAN") creating [$(joined "$(created "$U_PLAN")")], not exactly $SHARD1 as stock's [$(joined "$(created "$O_UP")")]"
    fi
    APPLY_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)"; APPLY_RC=$?
    if [ "$APPLY_RC" -ne 0 ] || ! grep -qF "1 added, 0 changed, 0 destroyed" <<< "$APPLY_OUT"; then
      printf '%s\n' "$APPLY_OUT" | tail -30
      count_fail "the scale-up apply exited $APPLY_RC and did not report exactly one added: $(gauntlet_first_error_line <<< "$APPLY_OUT")"
    fi
    exists_a applications.argoproj.io gauntlet-shard-1 || count_fail "shard-1 does not exist after the scale-up: [$(shards_a)]"
  fi
fi

# Withdraw the shards on both sides whatever happened above, and prove A
# converged: day2_replace asserts an exact plan shape on this root. The
# withdrawal's destroys are orphan-named too while #1996 is open, which is
# why its count is not asserted here.
{ set_shards "$ADOPTED" 0 && set_shards "$ORACLE" 0; } || fail "could not withdraw the shards"
W_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$W_OUT" | tail -30; count_fail "the apply withdrawing the shards from A failed: $(gauntlet_first_error_line <<< "$W_OUT")"; }
quiet stock_b apply -auto-approve -input=false -no-color || fail "could not withdraw the shards from B"
if [ -n "$(shards_a)" ]; then
  count_fail "shard Applications survive the withdrawal on A (deleted with kubectl): [$(shards_a)]"
  kca delete applications.argoproj.io gauntlet-shard-0 gauntlet-shard-1 -n "$NS" --ignore-not-found >/dev/null 2>&1
fi
CLEAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$CLEAN" | tail -30; fail "the plan after withdrawing the shards failed; nothing below would measure day-2 behaviour"; }
grep -q "No changes." <<< "$CLEAN" || { printf '%s\n' "$CLEAN" | tail -30; fail "the plan after withdrawing the shards is not empty; nothing below would measure day-2 behaviour${COUNT_FAILS:+ (this stage also found: $COUNT_FAILS)}"; }

if [ -n "$COUNT_FAILS" ]; then
  gauntlet_stage day2_count fail "extraObjects' Application shards 0 -> 2 -> 1 -> 2 (each entry one rendered Application in kubernetes_manifest.rest; stock's plans on the oracle are 2 adds, then 1 destroy [$(joined "$O_DOWN_DEST")], then 1 add): $COUNT_FAILS. The shards were withdrawn on both sides afterwards and A replans empty"
elif [ -n "${COUNT_BREAK_DONE:-}" ]; then
  gauntlet_stage day2_count pass "BREAK_COUNT=1 control: asserting the remaining shard (shard-0) was destroyed correctly fails to hold; the real check is skipped"
else
  gauntlet_stage day2_count pass "for_each over the render, scaled the way a chart user scales it: extraObjects at two Application entries rendered two Applications and choudoufu created both at stock's addresses, then replanned empty once the application controller had written their status; at one entry it destroyed exactly shard-1, the same object stock destroys, planned at $C_ADDR, $C_ADDR_NOTE ($SHARD0 untouched, both read with kubectl); back at two it created exactly $SHARD1; stock's plans for the same three edits on the oracle cluster have the identical shape. Withdrawn afterwards, A replans empty. BREAK_COUNT=1 asserts the remaining shard was destroyed and correctly fails"
fi

# ── 9b. day2_replace: a create_before_destroy rename ─────────────────────
gauntlet_begin_stage day2_replace
log "=== 9b. day2_replace: a content-hashed ConfigMap renamed under create_before_destroy ==="
gauntlet_kind_day2_replace "$ADOPTED" "$ORACLE" "$NS"

# ── 10. day2_crash: an apply of two custom resources, killed after one ───
gauntlet_begin_stage day2_crash
log "=== 10. day2_crash: SIGTERM between the create of one Application and the create of the next ==="
gauntlet_kind_day2_crash_rename "$ADOPTED" "$NS"

crash_block first > "$ORACLE/crash.tf" || fail "could not write crash-first to the oracle root"
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's crash-first plan failed on B"; }
grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's crash-first plan on B is not exactly one add"; }
quiet stock_b apply -auto-approve -input=false -no-color || fail "stock's crash-first apply failed on B"
crash_block second >> "$ORACLE/crash.tf" || fail "could not append crash-second to the oracle root"
O_REMAINDER="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_REMAINDER" | tail -10; fail "stock's remainder plan failed on B"; }
grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_REMAINDER" \
  || { printf '%s\n' "$O_REMAINDER" | tail -10; fail "stock's remainder plan on B is not exactly one add - the oracle for this stage is not what it should be"; }
grep -q 'kubernetes_manifest.crash_second' <<< "$O_REMAINDER" || { printf '%s\n' "$O_REMAINDER" | tail -30; fail "stock's remainder plan on B does not name crash_second"; }
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
exists_a applications.argoproj.io crash-first || { printf '%s\n' "$X_OUT" | tail -20; fail "crash-first does not exist after the interrupted apply - the kill landed before the create committed"; }
exists_a applications.argoproj.io crash-second && { printf '%s\n' "$X_OUT" | tail -20; fail "crash-second exists after the interrupted apply - the kill landed after both creates"; }
{ GET_OUT="$(kca get applications.argoproj.io -n "$NS" -l "tofu-estate=$ESTATE" -o name 2>/dev/null)" && grep -qE '(^|/)crash-first$' <<< "$GET_OUT"; } \
  || { printf '%s\n' "$GET_OUT"; fail "crash-first was created by the interrupted apply but does not come back under tofu-estate=$ESTATE"; }
X_ADDR_ANN="$(kca get applications.argoproj.io crash-first -n "$NS" -o jsonpath='{.metadata.annotations.choudoufu\.intentius\.io/tofu-address}' 2>&1)"
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
  kca label applications.argoproj.io crash-first -n "$NS" tofu-estate- >/dev/null 2>&1 || fail "BREAK_CRASH_UNBOUND: could not strip the label off crash-first"
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
  gauntlet_stage day2_crash pass "BREAK_CRASH_UNBOUND=1 control: with the tofu-estate label stripped off the Application the interrupted apply created, the recovery check correctly fails to hold ($R_LINE); the real check is skipped $CRASH_RENAME_DETAIL"
  kca delete applications.argoproj.io crash-first -n "$NS" >/dev/null 2>&1 || fail "BREAK_CRASH_UNBOUND: could not delete the unlabelled crash-first afterwards"
  quiet tofu_a apply -auto-approve -input=false -no-color || fail "BREAK_CRASH_UNBOUND: the apply after the cleanup failed"
else
  if ! recovered; then
    printf '%s\n' "$R_PLAN" | grep -E '^Plan:|^No changes|will be' | head -20
    gauntlet_stage day2_crash fail "the plan after a real interrupt between the creates of kubernetes_manifest.crash_first and .crash_second (both Applications, a custom kind this estate's own CRDs serve) is not exactly the remainder: ${R_LINE:-no plan line} (exit $R_RC). crash-first exists carrying tofu-estate=$ESTATE and crash-second does not, both read with kubectl; stock from the same position plans exactly one add (crash_second). Records $X_RECORDS_BEFORE -> $X_RECORDS_AFTER $CRASH_RENAME_DETAIL"
  else
    R_APPLY="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)"; R_APPLY_RC=$?
    [ "$R_APPLY_RC" -eq 0 ] || { printf '%s\n' "$R_APPLY" | tail -20; fail "the recovery apply exited $R_APPLY_RC"; }
    grep -qF "Apply complete! Resources: 1 added, 0 changed, 0 destroyed" <<< "$R_APPLY" \
      || { printf '%s\n' "$R_APPLY" | tail -5; fail "the recovery apply did not add exactly the one remaining object"; }
    exists_a applications.argoproj.io crash-second || fail "crash-second does not exist after the recovery apply"
    exists_a applications.argoproj.io crash-first || fail "crash-first is gone after the recovery apply - the recovery replaced the object the crash created instead of binding it"
    R_REPLAN="$(tofu_a plan -input=false -no-color 2>&1)"; R_REPLAN_RC=$?
    [ "$R_REPLAN_RC" -eq 0 ] || { printf '%s\n' "$R_REPLAN" | tail -30; fail "the replan after the recovery exited $R_REPLAN_RC: $(grep -E '^Error' <<< "$R_REPLAN" | head -1)"; }
    grep -q "No changes." <<< "$R_REPLAN" || { printf '%s\n' "$R_REPLAN" | tail -30; fail "the replan after the recovery is not empty"; }
    C_AFTER="$(count_a)"
    [ "$C_AFTER" = "$((C_BEFORE + 2))" ] || fail "$C_AFTER labelled objects after the recovery, want $((C_BEFORE + 2))"
    gauntlet_stage day2_crash pass "an apply creating two Applications - a custom kind served by CRDs this estate's own render installed, reconciled by the application controller - was interrupted by a real SIGTERM (exit $X_RC), delivered by the engine itself inside the -parallelism=1 walker the instant kubernetes_manifest.crash_first's create committed; crash_second's manifest reads crash_first's name, so the walker cannot have reached it. kubectl confirms crash-first exists carrying tofu-estate=$ESTATE and its address annotation, and crash-second does not; the interrupted apply recorded exactly the one object it created (records $X_RECORDS_BEFORE -> $X_RECORDS_AFTER, labels [$X_KEYS_L], annotations [$X_KEYS_A], #1211). The next plan proposed exactly the remainder ($R_LINE, crash_second created) and nothing for crash-first, matching stock from the same position on the oracle cluster; the recovery apply added one, the replan is empty and $C_AFTER objects carry the label, $C_BEFORE plus the pair. BREAK_CRASH=1 and BREAK_CRASH_UNBOUND=1 both correctly fail $CRASH_RENAME_DETAIL"
  fi
fi
gauntlet_end_stage

# ── 11. day2_teardown: destroy the adopted estate ────────────────────────
gauntlet_begin_stage day2_teardown
log "=== 11. day2_teardown: apply -destroy on the adopted estate, and stock's own destroy on B ==="
T_EXPECT="$(count_a)"
T_LABELLED="$(labelled_a)"
# What finalizers the Applications carry going in, for the log and the
# verdict: none is expected (see crash_block), and one the application
# controller has to clear would hang the CRD destroy once it is gone.
APP_FINS="$(kca get applications.argoproj.io -n "$NS" -o json 2>/dev/null | jq -r '.items[]? | select((.metadata.finalizers // []) | length > 0) | "\(.metadata.name) [\(.metadata.finalizers | join(","))]"' 2>/dev/null | tr '\n' ' ')"
log "  Application finalizers before the destroy: ${APP_FINS:-none}"
T_OUT="$(tofu_a apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$T_OUT" | tail -20; fail "apply -destroy failed"; }
grep -qF "Resources: 0 added, 0 changed, $T_EXPECT destroyed" <<< "$T_OUT" || { printf '%s\n' "$T_OUT" | grep -E 'Destroy|^Apply complete|^Error' | tail -60; fail "apply -destroy did not remove exactly the $T_EXPECT remaining objects ($(grep -E '^Apply complete' <<< "$T_OUT" | head -1)); labelled before it: $T_LABELLED"; }
# argocd-server's own objects go with the Namespace and the CRDs, never by
# name in the destroy.
if grep -qE 'argocd-initial-admin-secret|orphan_appproject_'"$NS"'_default' <<< "$T_OUT"; then
  printf '%s\n' "$T_OUT" | grep -E 'argocd-initial-admin-secret|orphan_appproject_' | head -10
  fail "apply -destroy named one of argocd-server's own objects, which nothing declares"
fi
for _ in $(seq 1 90); do kca get namespace "$NS" >/dev/null 2>&1 || break; sleep 2; done
kca get namespace "$NS" >/dev/null 2>&1 && fail "the $NS namespace still exists 180s after the destroy: $(kca get namespace "$NS" -o jsonpath='{.status.phase} {.status.conditions[*].message}' 2>&1); left in it: $(kca get all,applications.argoproj.io,appprojects.argoproj.io -n "$NS" -o name 2>&1 | tr '\n' ' ')"
[ "$(count_a)" = "0" ] || fail "$(count_a) object(s) still carry tofu-estate=$ESTATE after the destroy: $(labelled_a)"
CRDS_LEFT="$(kca get crd -o name 2>/dev/null | grep -c '\.argoproj\.io$')"
[ "$CRDS_LEFT" = "0" ] || fail "$CRDS_LEFT argoproj.io CRD(s) survive the destroy: $(kca get crd -o name 2>&1 | grep argoproj | tr '\n' ' ')"
CR_LEFT="$(kca get clusterroles,clusterrolebindings -o name 2>/dev/null | grep -c "/$FULL-")"
[ "$CR_LEFT" = "0" ] || fail "$CR_LEFT of the estate's ClusterRoles or ClusterRoleBindings survive the destroy: $(kca get clusterroles,clusterrolebindings -o name 2>&1 | grep "/$FULL-" | tr '\n' ' ')"
kca get namespace kube-system >/dev/null 2>&1 || fail "kube-system is gone"
O_EXPECT="$(managed_n stock_b)"
O_T="$(stock_b apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$O_T" | tail -10; fail "stock's destroy failed on B"; }
grep -qF "Resources: 0 added, 0 changed, $O_EXPECT destroyed" <<< "$O_T" || { printf '%s\n' "$O_T" | tail -10; fail "stock's destroy on B did not remove exactly the $O_EXPECT objects its state held"; }
gauntlet_stage day2_teardown pass "apply -destroy removed exactly the $T_EXPECT remaining objects in one apply, the workloads and day2_crash's pair of Applications before the rest and the rest before the CRDs and the Namespace by the blocks' depends_on (Application finalizers going in: ${APP_FINS:-none}); the $NS namespace is gone and with it argocd-server's own default AppProject and argocd-initial-admin-secret, which nothing declared and the destroy never named, all $CRD_N argoproj.io CRDs are gone, none of the estate's ClusterRoles or ClusterRoleBindings survive, and no object of the estate's kinds carries tofu-estate=$ESTATE (kubectl, every namespace); stock's destroy on the oracle cluster removed exactly the $O_EXPECT its state held"

# ── 12. greenfield: the same shape, fresh, with a live block ─────────────
gauntlet_begin_stage greenfield
log "=== 12. greenfield: choudoufu applies the shape fresh on the now-empty cluster A ==="
write_root "$GREEN" live || fail "could not write the greenfield root"
green() { ( cd "$GREEN" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" "$TOFU" "$@" ); }
quiet green init -input=false -no-color || fail "greenfield init failed"

G_TARGETS=()
while IFS= read -r a; do [ -n "$a" ] && G_TARGETS+=("-target=$a"); done < <(gauntlet_pre_apply_targets "$ESTATE")
[ "${#G_TARGETS[@]}" = "2" ] || fail "the declared pre-apply list has ${#G_TARGETS[@]} addresses, want 2 (the Namespace and kubernetes_manifest.crds)"
G_PRE="$(green apply -auto-approve -input=false -no-color "${G_TARGETS[@]}" 2>&1)"; G_PRE_RC=$?
if [ "$G_PRE_RC" -ne 0 ]; then
  printf '%s\n' "$G_PRE" | tail -40
  gauntlet_stage greenfield fail "choudoufu cannot perform the pre-apply its own estate declares: the same ${#G_TARGETS[@]} -target arguments stock accepted at cold deploy, against the same empty cluster, exited $G_PRE_RC; the first error it printed was \"$(gauntlet_first_error_line <<< "$G_PRE")\""
  quiet green apply -destroy -auto-approve -input=false -no-color
  GREENFIELD_SKIPPED=1
fi
if [ -z "${GREENFIELD_SKIPPED:-}" ]; then
grep -qF "Apply complete! Resources: $PRE_N added, 0 changed, 0 destroyed" <<< "$G_PRE" || { printf '%s\n' "$G_PRE" | tail -5; fail "the greenfield pre-apply did not add exactly the $PRE_N declared instances"; }
G_OUT="$(green apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$G_OUT" | tail -20; fail "greenfield apply failed"; }
grep -qF "Apply complete! Resources: $REST_N added, 0 changed, 0 destroyed" <<< "$G_OUT" || { printf '%s\n' "$G_OUT" | tail -5; fail "the greenfield main apply did not add exactly the $REST_N rendered non-CRD objects"; }
[ ! -f "$GREEN/terraform.tfstate" ] || fail "a terraform.tfstate appeared after a live-block apply"
argocd_ready "$KCA" "cluster A (greenfield)" || fail "Argo CD never became ready on A after the greenfield apply"
G_LABELLED="$(count_a)"
G_OWN_LABELLED="$(argocd_own_labelled_a)"
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
  gauntlet_stage greenfield pass "BREAK=1 control: expecting $G_WANT labelled objects where the estate has $G_LABELLED makes the count assertion correctly fail; the estate applied ($REST_N added after the pre-apply, no terraform.tfstate) and replanned empty with and without the cache"
else
  [ "$G_LABELLED" = "$TOTAL_N" ] || fail "$G_LABELLED object(s) carry tofu-estate=$ESTATE after the greenfield apply, want $TOTAL_N: $(labelled_a)"
  [ "$G_OWN_LABELLED" = "0" ] || fail "$G_OWN_LABELLED of argocd-server's own two objects carry tofu-estate=$ESTATE after the greenfield apply"
  gauntlet_stage greenfield pass "$TOTAL_N objects applied fresh with a live block and no terraform.tfstate - the render read by the data-read phase before the plan, the same declared pre-apply the cold deploy took ($PRE_N instances), then $REST_N - every one labelled tofu-estate=$ESTATE (kubectl, 15 kinds), Argo CD ready with argocd-server's own default AppProject and argocd-initial-admin-secret created unlabelled and a status written into the Application; replanned empty with and without the cache; the record store held $G_RECORDS envelope(s) for $TOTAL_N instances, and with that whole store deleted the plan read \"$L_LINE\", nothing created, destroyed or replaced and no kubernetes_manifest updated${L_OTHER:+ (proposed and reconverged: $L_OTHER)}. BREAK=1 expects a deliberately wrong object count and the assertion correctly fails"
fi
# ── no_local_state (#1966): the record store and the cache both gone ────
# live/e2e/lib/gauntlet.sh's gauntlet_kind_no_local_state; its Break line
# strips the tofu-estate label off drift_reconverge's NetworkPolicy.
gauntlet_kind_no_local_state "$GREEN" networkpolicy "$FULL-server" "$NS"
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
    estate = "reference-k8s-helm-argo-cd-strict"
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
strict_tofu() { ( cd "$STRICT" && "$TOFU" "$@" ); }
quiet strict_tofu init -input=false -no-color || fail "choudoufu init for the strict-stage scratch estate failed"
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
log "reference-k8s-helm-argo-cd: done"
