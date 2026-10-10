#!/usr/bin/env bash
# reference-k8s-helm-cloudnative-pg: the kubernetes lane's second Helm
# estate (#1977), the CloudNativePG operator chart, crossed on the kind
# substrate.
#
# The configuration is a Helm chart rendered CLIENT-SIDE through
# hashicorp/helm's `data "helm_template"` and applied as kubernetes_manifest
# blocks - the shape live/kubernetes/COMPATIBILITY.md recommends for a chart,
# and the shape reference-k8s-helm-template established. No helm_release
# anywhere: Helm never talks to the cluster, the provider is declared for
# the one data source.
#
#   chart    cloudnative-pg 0.29.1 (operator 1.30.1), Apache-2.0
#   archive  $CHART_URL below, sha256 $CHART_SHA256 below, checked on every run
#   render   22 documents at the chart's defaults, 23 with root/values.yaml's
#            deltas (one operator setting so config.data is not an empty map,
#            the mutating webhook's failurePolicy written out, the grafana
#            dashboard ConfigMap on): 11 CRDs and 12 objects over 8 kinds
#   root     root/main.tf, ours, hence reference-: one data source, two
#            Namespaces (helm template renders none), three for_each
#            kubernetes_manifest blocks over the render split and
#            yamldecoded INSIDE the for_each expression, keyed
#            kind/namespace/name, `manifest = each.value` (#1962) - the 11
#            CRDs, the 11 others, and the operator's Deployment with
#            wait { rollout = true } - then one Cluster and a for_each of
#            ImageCatalogs (empty until day2_count) written by hand
#
# What it drives that nothing else in the lane does:
#
#   - a chart whose webhook PKI is the operator's own at runtime: no hook,
#     no cert-gen Job, no caBundle in the render. The operator creates its
#     CA and serving-certificate Secrets (owned by its Deployment) and
#     patches caBundle into the two webhook configurations this estate
#     declares - a field another manager writes into objects the root owns,
#     which every replan has to leave alone;
#   - kubernetes_manifest with `wait { rollout = true }` as the ordering
#     edge: both webhooks are failurePolicy: Fail, so the Cluster cannot be
#     admitted until the operator's pod is Ready;
#   - the lane's largest CRD documents (poolers.postgresql.cnpg.io is about
#     680KB of YAML, clusters about 470KB) and a 250KB dashboard ConfigMap
#     through yamldecode and server-side apply;
#   - one declared custom resource (the Cluster) from which the operator
#     creates the instance Pod, its PersistentVolumeClaim, Secrets,
#     Services, a ServiceAccount, a Role and RoleBinding, a
#     PodDisruptionBudget and an initdb Job (the set copies_a lists at
#     cold deploy is the measured one), and - in the database's namespace -
#     an UNOWNED ConfigMap with the same name as a ConfigMap the chart
#     declares in cnpg-system;
#   - an operator told (INHERITED_LABELS) to copy tofu-estate from the
#     Cluster onto everything it generates, so after migrate those objects
#     match the estate's label selector and carry no address annotation:
#     day2_remove's sweep must leave every one of them alone;
#   - one name across four kinds (rel-cloudnative-pg: ServiceAccount,
#     ClusterRole, ClusterRoleBinding, Deployment);
#   - day-2 changes made through values where the chart has the knob
#     (plan_approval, day2_remove), and in main.tf where it has none
#     (day2_count: the chart renders no list of objects).
#
# ── the two applies ──────────────────────────────────────────────────────
#
# kubernetes_manifest builds an object's schema at PLAN time, so a root
# declaring a CRD and an object of it cannot be planned against an empty
# cluster, by stock or by choudoufu. cold_deploy runs the un-targeted plan
# as a CONTROL and requires it to fail, then performs the pre-apply
# live/gauntlet/estates.json declares (the operator's Namespace and
# kubernetes_manifest.crds) on both sides in one gauntlet_pre_apply call,
# then the main apply.
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
#   go run ./tools/gauntlet run reference-k8s-helm-cloudnative-pg
#   bash live/e2e/reference-k8s-helm-cloudnative-pg/run.sh
#
# Needs kind, kubectl, terraform (the stock oracle), Docker, python3, jq and
# curl. Network: the chart archive once (then cached under
# $CHOUDOUFU_CHART_CACHE, default ~/.cache/choudoufu/charts, and re-checked
# against its sha256 on every run) and two public image pulls per cluster
# (ghcr.io/cloudnative-pg/cloudnative-pg:1.30.1 and
# ghcr.io/cloudnative-pg/postgresql:18.6-minimal-trixie, both linux/arm64
# and linux/amd64).
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
#   BREAK_REMOVE   keep the grafana dashboard on and assert no destroy is
#                  proposed.
#   BREAK_COUNT    assert the wrong shard was destroyed on the scale-down.
#   BREAK_APPROVAL apply the saved plan after the world moved and expect
#                  success.
#   BREAK_REPLACE  see live/e2e/lib/gauntlet.sh's gauntlet_kind_day2_replace.
#   BREAK_CRASH    assert, after the same real interrupt, that nothing is
#                  proposed; must fail, because a recovered run proposes
#                  the remainder.
#   BREAK_CRASH_UNBOUND
#                  strip the tofu-estate label off the custom resource the
#                  interrupted apply did create before replanning. The real
#                  check must then fail to hold.
#   BREAK_STRICT   turn secrets back to "store" and require the refusal to
#                  vanish.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$ROOT/live/e2e/lib/gauntlet.sh"

# The shared provider plugin cache, and the cross-process lock real terraform
# needs in order to use it safely (#1300). live/e2e/lib/gauntlet.sh carries the
# measured reasons for both; this is the only place a script chooses either.
gauntlet_plugin_cache
ESTATE="reference-k8s-helm-cloudnative-pg"
NS="cnpg-system"                 # the operator's namespace, the release namespace
DB_NS="pg"                       # the Cluster's namespace
PG="pg"                          # the Cluster's name
RELEASE="rel"
FULL="$RELEASE-cloudnative-pg"   # the chart's fullname for this release
MWC="cnpg-mutating-webhook-configuration"
VWC="cnpg-validating-webhook-configuration"
DASH="cnpg-grafana-dashboard"
MONCM="cnpg-default-monitoring"  # declared in $NS; the operator's unowned copy in $DB_NS

# The chart, pinned by digest. root/main.tf reads it from charts/ beside
# itself; write_root copies the verified archive there.
CHART_VERSION="0.29.1"
CHART_FILE="cloudnative-pg-$CHART_VERSION.tgz"
CHART_URL="https://github.com/cloudnative-pg/charts/releases/download/cloudnative-pg-v$CHART_VERSION/$CHART_FILE"
CHART_SHA256="b53d3991fe84bcf38767e7702cae78666265427a127a26fff168ab4207d2b1df"
CHART_CACHE="${CHOUDOUFU_CHART_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/choudoufu/charts}"

# The estate's kinds, for the label count. The custom kinds only exist once
# the CRDs are installed; kubectl prints nothing for a kind the API server
# does not serve, which counts as zero, which is correct. Pods, Secrets,
# PersistentVolumeClaims and the rest of what the operator generates are
# deliberately absent: the root declares none of them, and the operator's
# are counted separately (copies_a).
KINDS="namespaces customresourcedefinitions serviceaccounts clusterroles clusterrolebindings configmaps services deployments mutatingwebhookconfigurations validatingwebhookconfigurations clusters.postgresql.cnpg.io imagecatalogs.postgresql.cnpg.io"
CRD_N=11      # kubernetes_manifest.crds
# The main apply: kubernetes_manifest.rest (11), .late (the Deployment), the
# database's Namespace and the Cluster.
REST_N=14
PRE_N=$((1 + CRD_N))          # the operator's Namespace and the CRDs: the pre-apply's population
TOTAL_N=$((PRE_N + REST_N))   # 26 managed instances, 24 of them kubernetes_manifest
MANIFEST_N=$((TOTAL_N - 2))
DASH_N=1      # objects grafanaDashboard.create=false takes out of the render
WORK="$(mktemp -d)"
STOCK="$WORK/stock"; ADOPTED="$WORK/adopted"; ORACLE="$WORK/oracle"; GREEN="$WORK/green"
KCA="$WORK/a.kubeconfig"; KCB="$WORK/b.kubeconfig"
CLUSTER_A="${GAUNTLET_KIND_PREFIX:-chdf}-refhcn-a-$$"; CLUSTER_B="${GAUNTLET_KIND_PREFIX:-chdf}-refhcn-b-$$"  # GAUNTLET_KIND_PREFIX: lets concurrent workers name their own clusters
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

# set_value <dir> <exact old text> <exact new text>: a values.yaml edit, the
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

# set_shards <dir> <n>: day2_count's for_each scaling. main.tf's
# kubernetes_manifest.catalog has one ImageCatalog per shard, keyed
# shard-<i> and named catalog-shard-<i>; local.catalog_shards is the count.
# The chart itself renders no list of objects a value could scale, so the
# knob is in main.tf, edited by exact match.
set_shards() {
  python3 - "$1/main.tf" "$2" <<'PY'
import re, sys
p, n = sys.argv[1], int(sys.argv[2])
s = open(p).read()
pat = re.compile(r"(?m)^  catalog_shards = [0-9]+$")
assert len(pat.findall(s)) == 1, "main.tf does not carry the catalog_shards line exactly once"
open(p, 'w').write(pat.sub("  catalog_shards = %d" % n, s, count=1))
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
# ImageCatalogs - a namespaced custom kind this estate's own CRDs serve, that
# no webhook admits and the operator generates nothing from - and
# crash_second's manifest reads crash_first's name, a real edge in the
# graph, so nothing can create crash-second until crash-first has committed
# (#490's discipline; reference-k8s-cert-manager's crash pair, #1262).
crash_block() {
  if [ "$1" = "first" ]; then
    cat <<EOF

resource "kubernetes_manifest" "crash_first" {
  manifest = {
    "apiVersion" = "postgresql.cnpg.io/v1"
    "kind"       = "ImageCatalog"
    "metadata" = {
      "name"      = "crash-first"
      "namespace" = "$DB_NS"
    }
    "spec" = {
      "images" = [{ "major" = 17, "image" = "ghcr.io/cloudnative-pg/postgresql:17.6-minimal-trixie" }]
    }
  }

  depends_on = [kubernetes_namespace_v1.pg]
}
EOF
  else
    cat <<EOF

resource "kubernetes_manifest" "crash_second" {
  manifest = {
    "apiVersion" = "postgresql.cnpg.io/v1"
    "kind"       = "ImageCatalog"
    "metadata" = {
      "annotations" = {
        "after" = kubernetes_manifest.crash_first.manifest.metadata.name
      }
      "name"      = "crash-second"
      "namespace" = "$DB_NS"
    }
    "spec" = {
      "images" = [{ "major" = 18, "image" = "ghcr.io/cloudnative-pg/postgresql:18.6-minimal-trixie" }]
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
exists_db_a() { kca get "$1" "$2" -n "$DB_NS" >/dev/null 2>&1; }
# managed_n <stock fn>: managed instances in that side's state; the
# helm_template data source sits in state too and is not an object.
managed_n() { "$1" state list 2>/dev/null | grep -vc '^data\.'; }

# count_a: objects of the estate's kinds carrying tofu-estate=$ESTATE that
# NO controller owns. INHERITED_LABELS has the operator copy the label from
# the Cluster onto what it generates; every generated object it labels also
# carries an ownerReference to the Cluster, which is what keeps it out of
# this count. copies_a counts the other side of the line.
count_a() {
  local n=0 k c
  for k in $KINDS; do
    c="$(kca get "$k" -A -l "tofu-estate=$ESTATE" -o json 2>/dev/null | jq '[.items[]? | select((.metadata.ownerReferences // []) | length == 0)] | length' 2>/dev/null)"
    n=$((n + ${c:-0}))
  done
  printf '%s\n' "$n"
}
# copies_a: what the operator generated, labelled or not, as kind/name
# lines - the objects the root never declared and the sweep must never
# propose. In $DB_NS: every object of the kinds the operator writes there,
# owned or not (its clone of $MONCM has no owner), except the two the API
# server writes into every namespace; the instance Pod only, not the initdb
# Job's, and every PVC with its phase. In $NS: the operator's two PKI
# Secrets, owned by its Deployment.
copies_a() {
  {
    kca get pods,secrets,services,configmaps,serviceaccounts,roles,rolebindings,poddisruptionbudgets -n "$DB_NS" -o json 2>/dev/null \
      | jq -r '.items[]?
          | select(.metadata.name != "kube-root-ca.crt" and .metadata.name != "default")
          | select(.kind != "Pod" or ((.metadata.ownerReferences // []) | any(.kind == "Cluster")))
          | "\(.kind)/\(.metadata.name)"'
    kca get persistentvolumeclaims -n "$DB_NS" -o json 2>/dev/null | jq -r '.items[]? | "\(.kind)/\(.metadata.name) \(.status.phase)"'
    kca get secrets -n "$NS" -o json 2>/dev/null \
      | jq -r '.items[]? | select((.metadata.ownerReferences // []) | length > 0) | "\(.kind)/\(.metadata.name)"'
  } | sort
}
# copies_labelled_a: how many of those carry the estate's label.
copies_labelled_a() {
  kca get pods,secrets,services,configmaps,serviceaccounts,roles,rolebindings,poddisruptionbudgets,persistentvolumeclaims -n "$DB_NS" -l "tofu-estate=$ESTATE" -o json 2>/dev/null \
    | jq '.items | length'
}

# cnpg_ready <kubeconfig> <label>: bounded and loud. The operator's
# Deployment Available, then the Cluster reporting one ready instance in a
# healthy state with its claim Bound - the operator-created objects
# day2_remove reads exist only from that point on.
pg_ready() {
  local cfg="$1" ready phase
  ready="$(kubectl --kubeconfig "$cfg" get cluster.postgresql.cnpg.io "$PG" -n "$DB_NS" -o jsonpath='{.status.readyInstances}' 2>/dev/null)"
  [ "${ready:-0}" -ge 1 ] || return 1
  phase="$(kubectl --kubeconfig "$cfg" get cluster.postgresql.cnpg.io "$PG" -n "$DB_NS" -o jsonpath='{.status.phase}' 2>/dev/null)"
  [ "$phase" = "Cluster in healthy state" ] || return 1
  [ -n "$(kubectl --kubeconfig "$cfg" get pvc -n "$DB_NS" -o jsonpath='{.items[?(@.status.phase=="Bound")].metadata.name}' 2>/dev/null)" ]
}
cnpg_ready() {
  local cfg="$1" label="$2"
  gauntlet_k8s_wait_all "$cfg" "$NS" deployment Available 300 "$label" || return 1
  gauntlet_wait_until 600 "the Cluster $DB_NS/$PG on $label to report one ready instance, healthy, with its claim Bound" -- pg_ready "$cfg"
}

# rest_addr <kind> <namespace> <name>: one instance address of the rest block.
rest_addr() { printf 'kubernetes_manifest.rest["%s/%s/%s"]' "$1" "$2" "$3"; }
# planned <plan>: the addresses a plan proposes something for, one per line.
planned() { grep -E '^[[:space:]]*# .+ (will be|must be)' <<< "$1" | sed -E 's/^[[:space:]]*# //; s/ (will be|must be).*$//'; }

# ── 1. cold_deploy: the control, the declared pre-apply, then the rest ───
gauntlet_begin_stage cold_deploy
log "=== 1. cold_deploy: two kind clusters; the un-targeted plan must fail, then the declared pre-apply on both sides ==="
gauntlet_kind_up "$CLUSTER_A" "$KCA" || fail "kind cluster A ($CLUSTER_A) did not come up"
gauntlet_kind_up "$CLUSTER_B" "$KCB" || fail "kind cluster B ($CLUSTER_B) did not come up"
export KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA"
K8S_VER="$(kca version 2>/dev/null | gauntlet_k8s_server_version)"
log "  cluster A: $CLUSTER_A (kubernetes $K8S_VER); cluster B: $CLUSTER_B"
write_root "$STOCK" stock  || fail "could not write the stock root on A"
write_root "$ORACLE" stock || fail "could not write the oracle root on B"
( stock_a init -input=false -no-color >/dev/null 2>&1 ) || fail "stock init failed on A"
gauntlet_report_lock "$STOCK"  # the versions stage 1 resolved, for the row (#1739)
( stock_b init -input=false -no-color >/dev/null 2>&1 ) || fail "stock init failed on B"

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

  COLD_OUT="$(stock_a apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$COLD_OUT" | tail -20; fail "stock's main apply failed on A"; }
  grep -qF "Apply complete! Resources: $REST_N added, 0 changed, 0 destroyed" <<< "$COLD_OUT" || { printf '%s\n' "$COLD_OUT" | tail -5; fail "stock's main apply on A did not add exactly the $REST_N non-CRD instances (11 rendered objects, the Deployment, the database's Namespace, the Cluster)"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's main apply failed on B"

  [ -f "$STOCK/terraform.tfstate" ] || fail "stock left no terraform.tfstate on A"
  STOCK_N="$(managed_n stock_a)"
  [ "$STOCK_N" = "$TOTAL_N" ] || fail "stock's state holds $STOCK_N managed instances, want $TOTAL_N"
  cnpg_ready "$KCA" "cluster A" || fail "CloudNativePG and its Cluster never became ready on A"
  cnpg_ready "$KCB" "cluster B" || fail "CloudNativePG and its Cluster never became ready on B"
  CABUNDLE_N="$(kca get mutatingwebhookconfiguration "$MWC" -o json 2>/dev/null | jq '[.webhooks[] | select((.clientConfig.caBundle // "") != "")] | length')"
  [ "${CABUNDLE_N:-0}" -ge 1 ] || fail "the operator never patched a caBundle into $MWC - the webhooks the Cluster was admitted through are not the operator's own PKI"
  UNMARKED="$(count_a)"
  [ "$UNMARKED" = "0" ] || fail "$UNMARKED object(s) already carry tofu-estate=$ESTATE after a plain stock apply - this proves nothing"
  STOCK_REPLAN="$(stock_a plan -detailed-exitcode -input=false -no-color 2>&1)"; STOCK_REPLAN_RC=$?
  [ "$STOCK_REPLAN_RC" -eq 0 ] || { printf '%s\n' "$STOCK_REPLAN" | tail -20; fail "stock's own replan after its cold deploy is not empty (exit $STOCK_REPLAN_RC) - a provider/controller fight in the shape (the operator's caBundle patch is the first suspect), which would be blamed on choudoufu later"; }
  COPIES="$(copies_a | tr '\n' ';' | sed -E 's/;$//; s/;/; /g')"
  log "  $TOTAL_N objects on A ($PRE_N pre-applied + $REST_N), operator-created: $COPIES"
  gauntlet_stage cold_deploy pass "$TOTAL_N objects from plain terraform against kind $K8S_VER: cloudnative-pg $CHART_VERSION (sha256 $CHART_SHA256) rendered by data.helm_template.cnpg into 23 kubernetes_manifest instances over 9 kinds under three for_each blocks keyed kind/namespace/name (the CRDs, the rest, then the operator's Deployment waited for its rollout), plus two Namespaces and the Cluster; a real terraform.tfstate with $TOTAL_N managed instances, zero tofu-estate labels read back with kubectl, the operator Available with caBundle patched into $CABUNDLE_N of the mutating webhooks by the operator itself, the Cluster healthy with one ready instance and its claim Bound, and stock's own replan empty. The identical shape cold-deployed by stock on a second cluster as every later stage's oracle. Two applies, and the first is declared: $PRE_NOTE. Control, run first on this same cluster: the un-targeted one-pass plan exits $CTRL_RC with $CTRL_N missing-CRD refusal(s) before creating anything, so the pre-apply is load-bearing. Operator-created objects the root never declared: $COPIES"
fi

# ── 2. migrate: live-import against stock's state ────────────────────────
gauntlet_begin_stage migrate
log "=== 2. migrate: live-import against the stock state file, read-only then -approve ==="
write_root "$ADOPTED" live || fail "could not write the adopted root"
( tofu_a init -input=false -no-color >/dev/null 2>&1 ) || fail "adopted init failed"
IMPORT_OUT="$(tofu_a live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -no-color 2>&1)" || { printf '%s\n' "$IMPORT_OUT" | tail -20; fail "live-import (dry run) failed"; }
ELIGIBLE_LINE="$(grep -E 'resource instance\(s\) are eligible for stamping' <<< "$IMPORT_OUT" | head -1)"
log "  dry run: ${ELIGIBLE_LINE:-no eligibility line}"
APPROVE_OUT="$(tofu_a live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -approve -no-color 2>&1)" || { printf '%s\n' "$APPROVE_OUT" | tail -20; fail "live-import -approve failed"; }
SUMMARY_LINE="$(grep -E 'resource\(s\) newly stamped' <<< "$APPROVE_OUT" | head -1 | sed 's/\.$//')"
log "  approve: ${SUMMARY_LINE:-no summary line}"
if grep -qF "$TOTAL_N resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 0 skipped" <<< "$APPROVE_OUT"; then
  LABELLED="$(count_a)"
  # The operator copies the label on its next reconcile of the Cluster, not
  # synchronously with the stamp; the count below is read once it has.
  copies_labelled_some() { [ "$(copies_labelled_a)" -ge 1 ]; }
  gauntlet_wait_until 120 "the operator to copy tofu-estate from the stamped Cluster onto what it generated" -- copies_labelled_some \
    || log "  note: no operator-generated object carries tofu-estate within 120s of the stamp"
  COPIES_LABELLED="$(copies_labelled_a)"
  if [ "$LABELLED" = "$TOTAL_N" ]; then
    gauntlet_stage migrate pass "$TOTAL_N of $TOTAL_N stamped, 0 skipped (${SUMMARY_LINE:-no summary line}); every declared object carries tofu-estate=$ESTATE, counted back with kubectl across all 12 kinds, the cluster-scoped CRDs, ClusterRoles, ClusterRoleBinding and both webhook configurations included, and the Cluster. Operator-generated objects in $DB_NS carrying the label after the stamp, by the operator's own copying from the Cluster it was stamped on (INHERITED_LABELS): ${COPIES_LABELLED:-0} (excluded from the count by their ownerReference). The eligibility line was: ${ELIGIBLE_LINE:-none}"
  else
    gauntlet_stage migrate fail "live-import reported $TOTAL_N stamped but $LABELLED declared object(s) carry tofu-estate=$ESTATE on the cluster (operator-generated copies carrying it: ${COPIES_LABELLED:-0})"
  fi
else
  gauntlet_stage migrate fail "live-import -approve did not bind every instance: ${SUMMARY_LINE:-no summary line}"
fi

# ── 3. test_plan: replan from nothing ────────────────────────────────────
gauntlet_begin_stage test_plan
log "=== 3. test_plan: choudoufu plan with no state file, identities read with kubectl ==="
PLAN_OUT="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$PLAN_OUT" | tail -20; fail "the post-migration plan failed"; }
# The name group: one name, four kinds, every one of them must be there as
# its own object; then the ConfigMap name the chart declares in $NS and the
# operator writes again, unowned, in $DB_NS.
IDS_OK=1; IDS_MISSING=""
for spec in "serviceaccount $NS" "clusterrole -" "clusterrolebinding -" "deployment $NS"; do
  read -r kind where <<< "$spec"
  if [ "$where" = "-" ]; then
    kca get "$kind" "$FULL" >/dev/null 2>&1 || { IDS_OK=0; IDS_MISSING="$IDS_MISSING $kind/$FULL"; }
  else
    exists_a "$kind" "$FULL" || { IDS_OK=0; IDS_MISSING="$IDS_MISSING $NS/$kind/$FULL"; }
  fi
done
exists_a configmap "$MONCM" || { IDS_OK=0; IDS_MISSING="$IDS_MISSING $NS/configmap/$MONCM"; }
exists_db_a configmap "$MONCM" || { IDS_OK=0; IDS_MISSING="$IDS_MISSING $DB_NS/configmap/$MONCM (the operator's copy)"; }
for w in "mutatingwebhookconfiguration $MWC" "validatingwebhookconfiguration $VWC" "crd clusters.postgresql.cnpg.io"; do
  read -r kind name <<< "$w"
  kca get "$kind" "$name" >/dev/null 2>&1 || { IDS_OK=0; IDS_MISSING="$IDS_MISSING $kind/$name"; }
done
exists_db_a cluster.postgresql.cnpg.io "$PG" || { IDS_OK=0; IDS_MISSING="$IDS_MISSING $DB_NS/cluster/$PG"; }
if grep -q "No changes." <<< "$PLAN_OUT" && [ "$IDS_OK" = "1" ]; then
  gauntlet_stage test_plan pass "the plan with no state file is empty over $TOTAL_N instances whose for_each keys and identities come from the helm_template render the data-read phase read; $FULL, one name across four kinds (ServiceAccount, ClusterRole, ClusterRoleBinding, Deployment), confirmed present as four objects with kubectl, with both webhook configurations whose caBundle the operator patched, a CRD, the Cluster, the declared $NS/$MONCM and the operator's unowned same-named $DB_NS/$MONCM"
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
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed) over $TOTAL_N instances; declared objects carrying tofu-estate=$ESTATE unchanged at $BEFORE_N across the estate's 12 kinds, counted with kubectl"

# ── 5. drift_reconverge: one object out of a four-kind name group ────────
gauntlet_begin_stage drift_reconverge
DRIFT_ADDR="$(rest_addr ClusterRole "" "$FULL")"
log "=== 5. drift_reconverge: the ClusterRole $FULL is deleted out of band on A and on B ==="
# A delete, not a patch: every object here is server-side applied, and a
# kubectl patch makes kubectl a field manager (reference-k8s-cert-manager
# measured the conflict on both sides). The deleted object shares its name
# with three objects of other kinds, so the plan has to name it by kind. Not
# the ServiceAccount: a recreated one has a new UID and the running
# operator's token would stop authenticating.
kca delete clusterrole "$FULL" >/dev/null || fail "could not delete the ClusterRole out of band on A"
kcb delete clusterrole "$FULL" >/dev/null || fail "could not delete the ClusterRole out of band on B"
if [ "${BREAK:-}" = "1" ]; then
  kca delete clusterrolebinding "$FULL" >/dev/null || fail "BREAK: could not delete a second object on A"
fi
ORACLE_PLAN="$(stock_b plan -detailed-exitcode -input=false -no-color 2>&1)"; ORACLE_RC=$?
[ "$ORACLE_RC" -eq 2 ] || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B after the out-of-band delete exited $ORACLE_RC, want 2 (changes)"; }
grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$ORACLE_PLAN" || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B does not propose exactly one create"; }
grep -qF "$DRIFT_ADDR" <<< "$ORACLE_PLAN" || fail "stock's plan on B does not name $DRIFT_ADDR"
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock could not reconverge B"
DRIFT_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$DRIFT_PLAN" | tail -20; fail "the plan after the out-of-band delete failed"; }
if [ "${BREAK:-}" = "1" ]; then
  if grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$DRIFT_PLAN"; then
    fail "BREAK=1: two objects were deleted but the plan still proposes exactly one create - the single-object assertion is not load-bearing"
  fi
  log "  BREAK=1: caught - with a second object deleted the plan is $(grep -E '^Plan:' <<< "$DRIFT_PLAN" | head -1)"
  ( tofu_a apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: could not reconverge A"
  gauntlet_stage drift_reconverge pass "BREAK=1 control: with the same-named ClusterRoleBinding deleted too the single-object assertion correctly fails to hold ($(grep -E '^Plan:' <<< "$DRIFT_PLAN" | head -1)); reconverged afterwards"
else
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$DRIFT_PLAN" || { printf '%s\n' "$DRIFT_PLAN" | tail -20; fail "the plan after one out-of-band delete does not propose exactly one create"; }
  D_PLANNED="$(planned "$DRIFT_PLAN")"
  [ "$D_PLANNED" = "$DRIFT_ADDR" ] || { printf '%s\n' "$D_PLANNED"; fail "the plan does not propose exactly $DRIFT_ADDR: $(tr '\n' ';' <<< "$D_PLANNED")"; }
  RECONV="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$RECONV" | tail -20; fail "the reconverging apply failed"; }
  grep -qF "Apply complete! Resources: 1 added, 0 changed, 0 destroyed" <<< "$RECONV" || { printf '%s\n' "$RECONV" | tail -10; fail "the reconverging apply did not create exactly one object"; }
  kca get clusterrole "$FULL" >/dev/null 2>&1 || fail "the ClusterRole does not exist after reconverging"
  [ "$(count_a)" = "$TOTAL_N" ] || fail "$(count_a) labelled objects after reconverging, want $TOTAL_N - the recreated object did not get its marker"
  gauntlet_stage drift_reconverge pass "a ClusterRole that shares its name ($FULL) with three objects of other kinds was deleted out of band with kubectl; choudoufu proposed putting back exactly $DRIFT_ADDR (1 add, 0 change, 0 destroy) and nothing for the three same-named objects, matching stock's own plan on the oracle cluster; apply created 1 and the recreated object carries the estate label again ($TOTAL_N labelled). A delete rather than a patch because every object is server-side applied. BREAK=1 deletes the same-named ClusterRoleBinding as well and the single-object assertion correctly fails"
fi

# ── 6. plan_approval: plan -out, the world moves, apply refuses ──────────
gauntlet_begin_stage plan_approval
log "=== 6. plan_approval: a values edit saved as a plan, an out-of-band move, a refusal; then the same file applies ==="
MWC_ADDR="$(rest_addr MutatingWebhookConfiguration "" "$MWC")"
FP_FAIL=$'  mutating:\n    failurePolicy: Fail'
FP_IGNORE=$'  mutating:\n    failurePolicy: Ignore'
fp_a() { kca get mutatingwebhookconfiguration "$MWC" -o jsonpath='{.webhooks[*].failurePolicy}' 2>/dev/null | tr ' ' '\n' | sort -u | tr '\n' ' ' | sed -E 's/ $//'; }
set_value "$ADOPTED" "$FP_FAIL" "$FP_IGNORE" || fail "could not edit the mutating webhook's failurePolicy in the adopted root"
set_value "$ORACLE"  "$FP_FAIL" "$FP_IGNORE" || fail "could not edit the mutating webhook's failurePolicy in the oracle root"
P_PLAN="$(tofu_a plan -out=approved.tfplan -input=false -no-color 2>&1)"; P_PLAN_RC=$?
P_PLAN_LINE="$(grep -E '^Plan:|^No changes' <<< "$P_PLAN" | head -1 | sed 's/\.$//')"
if [ "$P_PLAN_RC" -ne 0 ] || ! grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$P_PLAN" || ! grep -qF "$MWC_ADDR will be updated in-place" <<< "$P_PLAN"; then
  gauntlet_stage plan_approval fail "webhook.mutating.failurePolicy Fail -> Ignore in values.yaml did not plan as exactly one in-place update of $MWC_ADDR: ${P_PLAN_LINE:-no summary line} (exit $P_PLAN_RC). What it proposed: $(planned "$P_PLAN" | head -10 | tr '\n' ';')"
  printf '%s\n' "$P_PLAN" | tail -30
  ( tofu_a apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "could not converge A after plan_approval; nothing below would measure day-2 behaviour"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "could not converge B after plan_approval; the oracle would be stale for every stage below"
  PLAN_APPROVAL_SKIPPED=1
fi
if [ -z "${PLAN_APPROVAL_SKIPPED:-}" ]; then
# The world moves on a spec-side field of an object the saved plan does not
# touch: the operator's Deployment declares replicas: 1, and it is put back
# below. A second operator replica waits on the leader lease and changes
# nothing else.
kca patch deployment "$FULL" -n "$NS" --type merge -p '{"spec":{"replicas":2}}' >/dev/null || fail "could not move the world (the operator Deployment's replicas) on A"
P_APPLY="$(tofu_a apply -input=false -no-color approved.tfplan 2>&1)"; P_RC=$?
if [ "${BREAK_APPROVAL:-}" = "1" ]; then
  [ "$P_RC" -eq 0 ] && fail "BREAK_APPROVAL=1: applying the saved plan after the world moved succeeded - the refusal is not load-bearing"
  log "  BREAK_APPROVAL=1: caught - the apply after the world moved exited $P_RC"
  kca patch deployment "$FULL" -n "$NS" --type merge -p '{"spec":{"replicas":1}}' >/dev/null
  ( tofu_a apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: the saved plan did not apply once the world was put back"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: stock could not apply the change on B"
  gauntlet_stage plan_approval pass "BREAK_APPROVAL=1 control: applying the saved plan after the world moved exited $P_RC (refused), so the stage's own Break line correctly fails; applied once the world was put back"
else
  if [ "$P_RC" -ne 3 ] || ! grep -qF "The approved plan no longer matches the live system" <<< "$P_APPLY"; then
    printf '%s\n' "$P_APPLY" | tail -20
    gauntlet_stage plan_approval fail "the saved plan was applied after the world had moved out of band (the operator Deployment's replicas 1 -> 2, kubectl) and choudoufu exited $P_RC rather than refusing at 3 with \"The approved plan no longer matches the live system\". The saved plan's own change was one in-place update of $MWC_ADDR from a values edit"
    kca patch deployment "$FULL" -n "$NS" --type merge -p '{"spec":{"replicas":1}}' >/dev/null
    ( tofu_a apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "could not converge A after plan_approval; nothing below would measure day-2 behaviour"
    ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "could not converge B after plan_approval; the oracle would be stale for every stage below"
    PLAN_APPROVAL_SKIPPED=1
  fi
  if [ -z "${PLAN_APPROVAL_SKIPPED:-}" ]; then
  FP_NOW="$(fp_a)"
  [ "$FP_NOW" = "Fail" ] || fail "the mutating webhooks' failurePolicy reads '$FP_NOW' despite the refusal, want Fail"
  kca patch deployment "$FULL" -n "$NS" --type merge -p '{"spec":{"replicas":1}}' >/dev/null || fail "could not put the world back"
  P_APPLY2="$(tofu_a apply -input=false -no-color approved.tfplan 2>&1)" || { printf '%s\n' "$P_APPLY2" | tail -20; fail "the saved plan did not apply once the world was put back"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$P_APPLY2" || fail "the saved plan's apply did not change exactly one object"
  [ "$(fp_a)" = "Ignore" ] || fail "the mutating webhooks do not all read failurePolicy Ignore after the saved plan applied: '$(fp_a)'"
  P_CA="$(kca get mutatingwebhookconfiguration "$MWC" -o json 2>/dev/null | jq '[.webhooks[] | select((.clientConfig.caBundle // "") != "")] | length')"
  O_PA="$(stock_b plan -out=approved.tfplan -input=false -no-color 2>&1)" || fail "stock's own planfile failed on B"
  grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$O_PA" || { printf '%s\n' "$O_PA" | tail -10; fail "stock's plan for the same values edit on B is not exactly one in-place update"; }
  ( stock_b apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "stock's own planfile did not apply on B"
  gauntlet_stage plan_approval pass "a values.yaml edit (webhook.mutating.failurePolicy Fail -> Ignore) re-rendered through helm_template planned as exactly one in-place update of $MWC_ADDR, saved with plan -out; the world then moved out of band (the operator Deployment's replicas to 2 with kubectl, never choudoufu) and apply of the saved plan refused with \"The approved plan no longer matches the live system\" at exit 3, nothing applied (kubectl still reads Fail on every webhook); with the world put back the identical file applied, 0 added, 1 changed, 0 destroyed, every webhook reads Ignore, and the operator's caBundle is still on $P_CA of them; stock's plan for the same edit on the oracle cluster is the same one update and its planfile applied. BREAK_APPROVAL=1 expects success after the move and correctly fails"
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
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's moved-block apply failed on B"
R_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_PLAN" | tail -20; fail "the moved-block plan failed"; }
# The renderer aligns a map's "=" across its keys, and every chart CRD
# carries two annotations of its own (controller-gen.kubebuilder.io/version,
# helm.sh/resource-policy), so the address line reads `"...address"   =`
# with padding: one or more spaces before the "=".
R_ANN="$(grep -cE '~ +"choudoufu\.intentius\.io/tofu-address" += ".*" -> ".*"' <<< "$R_PLAN")"
if grep -qE 'will be (created|destroyed)|must be replaced' <<< "$R_PLAN"; then
  gauntlet_stage day2_rename fail "the moved-block plan over $CRD_N for_each CRD instances proposes a create, a destroy or a replace - not the marker rewritten in place: $(grep -E '^Plan:' <<< "$R_PLAN" | head -1)"
  printf '%s\n' "$R_PLAN" | tail -20
  ( tofu_a apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "could not converge after the rename; nothing below would measure day-2 behaviour"
elif grep -qF "Plan: 0 to add, $CRD_N to change, 0 to destroy." <<< "$R_PLAN" && [ "$R_ANN" = "$CRD_N" ]; then
  R_APPLY_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$R_APPLY_OUT" | tail -20; fail "the moved-block apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, $CRD_N changed, 0 destroyed" <<< "$R_APPLY_OUT" || { printf '%s\n' "$R_APPLY_OUT" | tail -10; fail "the moved-block apply was not exactly $CRD_N in-place changes"; }
  [ "$(kca get crd -o name | grep -c 'postgresql.cnpg.io')" = "$CRD_N" ] || fail "the CRD count moved across the rename"
  [ "$(count_a)" = "$TOTAL_N" ] || fail "$(count_a) labelled objects after the rename, want $TOTAL_N"
  R_REPLAN="$(tofu_a plan -input=false -no-color 2>&1)" || fail "the replan after the rename failed"
  grep -q "No changes." <<< "$R_REPLAN" || { printf '%s\n' "$R_REPLAN" | tail -20; fail "the replan after the rename is not empty"; }
  gauntlet_stage day2_rename pass "moved block over a for_each of $CRD_N CRDs keyed by the render, two of them over 450KB of schema (kubernetes_manifest.crds -> .custom_resource_definitions, with the rest block's depends_on following it): no add and no destroy, $CRD_N in-place changes each confined to the address annotation rewrite ($R_ANN annotation lines, 0 add, $CRD_N change, 0 destroy) - the marker rewritten in place, as the AWS lanes assert for a rename; every CRD still there and labelled, the replan empty; stock's plan for the same moved block on the oracle cluster is zero churn, since stock never writes this annotation"
else
  gauntlet_stage day2_rename fail "the moved-block plan over $CRD_N for_each CRD instances is not exactly $CRD_N in-place annotation changes: $(grep -E '^Plan:' <<< "$R_PLAN" | head -1), $R_ANN annotation line(s)"
  printf '%s\n' "$R_PLAN" | tail -20
  ( tofu_a apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "could not converge after the rename; nothing below would measure day-2 behaviour"
fi

# ── 8. day2_remove: a values change takes the dashboard out of the render
gauntlet_begin_stage day2_remove
DASH_ON=$'  grafanaDashboard:\n    create: true'
DASH_OFF=$'  grafanaDashboard:\n    create: false'
DASH_ADDR="$(rest_addr ConfigMap "$NS" "$DASH")"
log "=== 8. day2_remove: monitoring.grafanaDashboard.create=false removes $DASH_N rendered object; the operator's objects must be left alone ==="
COPIES_BEFORE="$(copies_a)"
[ -n "$COPIES_BEFORE" ] || fail "no operator-created objects exist before the removal; the controller-copy check would measure nothing"
grep -q "^Pod/$PG-1$" <<< "$COPIES_BEFORE" || fail "the Cluster's instance Pod $PG-1 does not exist before the removal: $(tr '\n' ';' <<< "$COPIES_BEFORE")"
grep -q '^PersistentVolumeClaim/.* Bound$' <<< "$COPIES_BEFORE" || fail "no Bound claim exists before the removal: $(tr '\n' ';' <<< "$COPIES_BEFORE")"
grep -q "^ConfigMap/$MONCM$" <<< "$COPIES_BEFORE" || fail "the operator's unowned $DB_NS/$MONCM does not exist before the removal: $(tr '\n' ';' <<< "$COPIES_BEFORE")"
LABELLED_COPIES_BEFORE="$(copies_labelled_a)"
if [ "${BREAK_REMOVE:-}" = "1" ]; then
  K_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || fail "BREAK_REMOVE: the plan with the dashboard kept failed"
  grep -qE "will be destroyed|[1-9][0-9]* to destroy" <<< "$K_PLAN" && fail "BREAK_REMOVE=1: with the dashboard kept a destroy was still proposed - the destroy below would not be the values change's doing"
  log "  BREAK_REMOVE=1: caught - with the dashboard kept the plan proposes no destroy"
  gauntlet_stage day2_remove pass "BREAK_REMOVE=1 control: with grafanaDashboard.create kept true, no destroy is proposed; the real check is skipped"
  { set_value "$ADOPTED" "$DASH_ON" "$DASH_OFF" && set_value "$ORACLE" "$DASH_ON" "$DASH_OFF"; } || fail "BREAK_REMOVE: could not switch the dashboard off afterwards"
  ( tofu_a apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_REMOVE: the removal apply failed afterwards"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_REMOVE: stock's removal apply failed on B afterwards"
else
  set_value "$ADOPTED" "$DASH_ON" "$DASH_OFF" || fail "could not switch the dashboard off in the adopted root"
  set_value "$ORACLE"  "$DASH_ON" "$DASH_OFF" || fail "could not switch the dashboard off in the oracle root"
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's remove plan failed on B"; }
  grep -qF "Plan: 0 to add, 0 to change, $DASH_N to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's remove plan on B is not exactly $DASH_N destroy"; }
  O_DESTROYED="$(planned "$O_PLAN" | tr '\n' ';' | sed -E 's/;$//; s/;/; /g')"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's remove apply failed on B"
  D_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$D_PLAN" | tail -20; fail "the remove plan failed"; }
  D_DESTROYED="$(planned "$D_PLAN" | tr '\n' ';' | sed -E 's/;$//; s/;/; /g')"
  grep -qF "Plan: 0 to add, 0 to change, $DASH_N to destroy." <<< "$D_PLAN" || { printf '%s\n' "$D_PLAN" | grep -E 'will be|must be|^Plan:' | head -20; fail "the remove plan is not exactly $DASH_N destroy: $(grep -E '^Plan:' <<< "$D_PLAN" | head -1). Proposed: $D_DESTROYED"; }
  [ "$D_DESTROYED" = "$DASH_ADDR" ] || fail "the remove plan destroys $D_DESTROYED, want exactly $DASH_ADDR"
  # The controller-copy half: nothing the operator generated may be named,
  # labelled or not - and the unowned $MONCM in $DB_NS shares its name with
  # the declared one in $NS, which is not being removed either.
  while IFS= read -r copy; do
    [ -n "$copy" ] || continue
    cname="${copy#*/}"; cname="${cname%% *}"
    grep -qF "$cname" <<< "$(grep -E 'will be destroyed' <<< "$D_PLAN")" && fail "the remove plan proposes destroying $copy, which the operator generated and the root never declared"
  done <<< "$COPIES_BEFORE"
  { APPLY_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)" && grep -qF "0 added, 0 changed, $DASH_N destroyed" <<< "$APPLY_OUT"; } || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "the remove apply did not destroy exactly $DASH_N object"; }
  exists_a configmap "$DASH" && fail "the dashboard ConfigMap $DASH still exists after the remove apply"
  exists_a configmap "$MONCM" || fail "the declared ConfigMap $NS/$MONCM is gone; the removal reached an object the values change kept"
  exists_db_a cluster.postgresql.cnpg.io "$PG" || fail "the Cluster is gone; the removal reached an object the values change kept"
  COPIES_AFTER="$(copies_a)"
  [ "$COPIES_AFTER" = "$COPIES_BEFORE" ] || fail "the operator-created objects changed across the removal: before [$(tr '\n' ';' <<< "$COPIES_BEFORE")], after [$(tr '\n' ';' <<< "$COPIES_AFTER")]"
  pg_ready "$KCA" || fail "the Cluster is no longer healthy with one ready instance after the removal"
  D_REPLAN="$(tofu_a plan -input=false -no-color 2>&1)" || fail "the replan after the remove failed"
  grep -q "No changes." <<< "$D_REPLAN" || { printf '%s\n' "$D_REPLAN" | tail -20; fail "the replan after the remove is not empty"; }
  REMAIN=$((TOTAL_N - DASH_N))
  [ "$(count_a)" = "$REMAIN" ] || fail "$(count_a) labelled objects after the remove, want $REMAIN"
  N_COPIES="$(grep -c . <<< "$COPIES_AFTER")"
  gauntlet_stage day2_remove pass "monitoring.grafanaDashboard.create=false in values.yaml took the dependency chart's one object, the dashboard ConfigMap, out of the render and choudoufu proposed exactly $DASH_N destroy ($D_DESTROYED), matching stock's on the oracle cluster ($O_DESTROYED); after the apply it is gone (kubectl), the replan is empty and $REMAIN objects labelled. The controller-copy exclusion: the $N_COPIES objects the operator generated ($(tr '\n' ';' <<< "$COPIES_AFTER" | sed -E 's/;$//; s/;/; /g')) - $LABELLED_COPIES_BEFORE of those in $DB_NS carrying tofu-estate=$ESTATE by INHERITED_LABELS, and $DB_NS/$MONCM unowned and same-named with a declared ConfigMap - were never proposed and read back identical, the claim still Bound and the Cluster healthy. BREAK_REMOVE=1 keeps the dashboard and requires no destroy"
fi

# ── 9. day2_count: a for_each of custom resources, scaled ────────────────
gauntlet_begin_stage day2_count
log "=== 9. day2_count: local.catalog_shards 2 -> 1 -> 2, each shard one ImageCatalog ==="
SHARD0='kubernetes_manifest.catalog["shard-0"]'
SHARD1='kubernetes_manifest.catalog["shard-1"]'
COUNT_VERDICT=""
set_shards "$ADOPTED" 2 || fail "could not add two catalog shards to the adopted root"
set_shards "$ORACLE" 2  || fail "could not add two catalog shards to the oracle root"
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's plan for the two shards failed on B"; }
grep -qF "Plan: 2 to add, 0 to change, 0 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's plan for the two shards on B is not exactly two adds"; }
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock could not create the shards on B"
set_shards "$ORACLE" 1 || fail "could not scale the oracle root to 1"
O_DOWN="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's scale-down plan failed on B"
grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$O_DOWN" || { printf '%s\n' "$O_DOWN" | tail -10; fail "stock's scale-down plan on B is not exactly one destroy"; }
grep -qF "$SHARD1" <<< "$O_DOWN" || fail "stock's scale-down on B does not destroy $SHARD1"
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's scale-down apply failed on B"

A_UP="$(tofu_a plan -input=false -no-color 2>&1)"; A_UP_RC=$?
if [ "$A_UP_RC" -ne 0 ] || ! grep -qF "Plan: 2 to add, 0 to change, 0 to destroy." <<< "$A_UP"; then
  COUNT_VERDICT="choudoufu could not plan the two catalog shards' creation: $(grep -E '^Plan:|^Error' <<< "$A_UP" | head -1)"
else
  { APPLY_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)" && grep -qF "2 added, 0 changed, 0 destroyed" <<< "$APPLY_OUT"; } || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "the shards' creating apply did not add exactly two objects"; }
  { exists_db_a imagecatalog.postgresql.cnpg.io catalog-shard-0 && exists_db_a imagecatalog.postgresql.cnpg.io catalog-shard-1; } || fail "both shards do not exist after choudoufu created them"
  A_RE="$(tofu_a plan -input=false -no-color 2>&1)"; A_RE_RC=$?
  if [ "$A_RE_RC" -ne 0 ] || ! grep -q "No changes." <<< "$A_RE"; then
    COUNT_VERDICT="replanning the root choudoufu itself had just applied with two shards is not empty (exit $A_RE_RC, $(gauntlet_plan_line <<< "$A_RE")); proposed: $(planned "$A_RE" | head -10 | tr '\n' ';'); first error: $(gauntlet_first_error_line <<< "$A_RE")"
  fi
fi

if [ -n "$COUNT_VERDICT" ]; then
  gauntlet_stage day2_count fail "$COUNT_VERDICT"
  { set_shards "$ADOPTED" 0 && set_shards "$ORACLE" 0; } || fail "could not withdraw the shards"
  kca delete imagecatalog.postgresql.cnpg.io catalog-shard-0 catalog-shard-1 -n "$DB_NS" --ignore-not-found >/dev/null 2>&1
  kcb delete imagecatalog.postgresql.cnpg.io catalog-shard-0 catalog-shard-1 -n "$DB_NS" --ignore-not-found >/dev/null 2>&1
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "the oracle root would not converge after the shards were withdrawn"
  CLEAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$CLEAN" | tail -20; fail "the plan after withdrawing the shards failed; nothing below would measure day-2 behaviour"; }
  grep -q "No changes." <<< "$CLEAN" || { printf '%s\n' "$CLEAN" | tail -20; fail "the plan after withdrawing the shards is not empty; nothing below would measure day-2 behaviour"; }
else
  set_shards "$ADOPTED" 1 || fail "could not scale the adopted root to 1"
  C_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$C_PLAN" | tail -20; fail "the scale-down plan failed"; }
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$C_PLAN" || { printf '%s\n' "$C_PLAN" | tail -20; fail "the scale-down plan is not exactly one destroy"; }
  C_ADDR="$(planned "$C_PLAN" | head -1)"
  { APPLY_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)" && grep -qF "0 added, 0 changed, 1 destroyed" <<< "$APPLY_OUT"; } || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "the scale-down apply did not destroy exactly one object"; }
  if [ "${BREAK_COUNT:-}" = "1" ]; then
    exists_db_a imagecatalog.postgresql.cnpg.io catalog-shard-0 || fail "BREAK_COUNT=1: shard-0 was destroyed - the 'wrong instance' assertion would hold, so the check is not load-bearing"
    log "  BREAK_COUNT=1: caught - shard-0 still exists, so asserting it was the one destroyed correctly fails"
    gauntlet_stage day2_count pass "BREAK_COUNT=1 control: asserting the remaining shard (shard-0) was destroyed correctly fails to hold; the real check is skipped"
  else
    exists_db_a imagecatalog.postgresql.cnpg.io catalog-shard-0 || fail "shard-0 was destroyed on the scale-down"
    exists_db_a imagecatalog.postgresql.cnpg.io catalog-shard-1 && fail "shard-1 still exists after the scale-down"
    { set_shards "$ADOPTED" 2 && set_shards "$ORACLE" 2; } || fail "could not scale back to 2"
    O_UP="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's scale-up plan failed on B"
    grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_UP" || { printf '%s\n' "$O_UP" | tail -10; fail "stock's scale-up plan on B is not exactly one add"; }
    ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's scale-up apply failed on B"
    U_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$U_PLAN" | tail -20; fail "the scale-up plan failed"; }
    grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$U_PLAN" || { printf '%s\n' "$U_PLAN" | tail -20; fail "the scale-up plan is not exactly one add"; }
    grep -qF "$SHARD1 will be created" <<< "$U_PLAN" || fail "the scale-up plan does not create $SHARD1"
    { APPLY_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)" && grep -qF "1 added, 0 changed, 0 destroyed" <<< "$APPLY_OUT"; } || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "the scale-up apply did not create exactly one object"; }
    gauntlet_stage day2_count pass "for_each over ImageCatalogs, a namespaced custom kind of the chart's own CRDs: at two shards choudoufu created both, then replanned empty; at one it destroyed exactly shard-1, planned at $C_ADDR ($SHARD0 untouched, both read with kubectl); back at two it created exactly $SHARD1; stock's plans for the same three edits on the oracle cluster have the identical shape. The knob is main.tf's local.catalog_shards, because the chart renders no list of objects a value could scale. BREAK_COUNT=1 asserts the remaining shard was destroyed and correctly fails"
    { set_shards "$ADOPTED" 0 && set_shards "$ORACLE" 0; } || fail "could not withdraw the shards"
    ( tofu_a apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "could not withdraw the shards from A"
    ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "could not withdraw the shards from B"
  fi
fi

# ── 9b. day2_replace: a create_before_destroy rename ─────────────────────
gauntlet_begin_stage day2_replace
log "=== 9b. day2_replace: a content-hashed ConfigMap renamed under create_before_destroy ==="
gauntlet_kind_day2_replace "$ADOPTED" "$ORACLE" "$NS"

# ── 10. day2_crash: an apply of two custom resources, killed after one ───
gauntlet_begin_stage day2_crash
log "=== 10. day2_crash: SIGTERM between the create of one ImageCatalog and the create of the next ==="
gauntlet_kind_day2_crash_rename "$ADOPTED" "$NS"

crash_block first > "$ORACLE/crash.tf" || fail "could not write crash-first to the oracle root"
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's crash-first plan failed on B"; }
grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's crash-first plan on B is not exactly one add"; }
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's crash-first apply failed on B"
crash_block second >> "$ORACLE/crash.tf" || fail "could not append crash-second to the oracle root"
O_REMAINDER="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_REMAINDER" | tail -10; fail "stock's remainder plan failed on B"; }
grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_REMAINDER" \
  || { printf '%s\n' "$O_REMAINDER" | tail -10; fail "stock's remainder plan on B is not exactly one add - the oracle for this stage is not what it should be"; }
grep -q 'kubernetes_manifest.crash_second' <<< "$O_REMAINDER" || fail "stock's remainder plan on B does not name crash_second"
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's remainder apply failed on B"
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
exists_db_a imagecatalog.postgresql.cnpg.io crash-first || { printf '%s\n' "$X_OUT" | tail -20; fail "crash-first does not exist after the interrupted apply - the kill landed before the create committed"; }
exists_db_a imagecatalog.postgresql.cnpg.io crash-second && { printf '%s\n' "$X_OUT" | tail -20; fail "crash-second exists after the interrupted apply - the kill landed after both creates"; }
{ GET_OUT="$(kca get imagecatalog.postgresql.cnpg.io -n "$DB_NS" -l "tofu-estate=$ESTATE" -o name 2>/dev/null)" && grep -qE '(^|/)crash-first$' <<< "$GET_OUT"; } \
  || { printf '%s\n' "$GET_OUT"; fail "crash-first was created by the interrupted apply but does not come back under tofu-estate=$ESTATE"; }
X_ADDR_ANN="$(kca get imagecatalog.postgresql.cnpg.io crash-first -n "$DB_NS" -o jsonpath='{.metadata.annotations.choudoufu\.intentius\.io/tofu-address}' 2>&1)"
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
  kca label imagecatalog.postgresql.cnpg.io crash-first -n "$DB_NS" tofu-estate- >/dev/null 2>&1 || fail "BREAK_CRASH_UNBOUND: could not strip the label off crash-first"
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
  ( tofu_a apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_CRASH: the recovery apply failed afterwards"
elif [ "${BREAK_CRASH_UNBOUND:-}" = "1" ]; then
  if recovered; then
    printf '%s\n' "$R_PLAN" | grep -E '^Plan:|will be'
    fail "BREAK_CRASH_UNBOUND=1: the recovery check still holds with crash-first carrying no tofu-estate label - it is not measuring whether the crashed-out object was bound at all"
  fi
  gauntlet_stage day2_crash pass "BREAK_CRASH_UNBOUND=1 control: with the tofu-estate label stripped off the ImageCatalog the interrupted apply created, the recovery check correctly fails to hold ($R_LINE); the real check is skipped $CRASH_RENAME_DETAIL"
  kca delete imagecatalog.postgresql.cnpg.io crash-first -n "$DB_NS" >/dev/null 2>&1 || fail "BREAK_CRASH_UNBOUND: could not delete the unlabelled crash-first afterwards"
  ( tofu_a apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_CRASH_UNBOUND: the apply after the cleanup failed"
else
  if ! recovered; then
    printf '%s\n' "$R_PLAN" | grep -E '^Plan:|^No changes|will be' | head -20
    gauntlet_stage day2_crash fail "the plan after a real interrupt between the creates of kubernetes_manifest.crash_first and .crash_second (both ImageCatalogs, a custom kind this estate's own CRDs serve) is not exactly the remainder: ${R_LINE:-no plan line} (exit $R_RC). crash-first exists carrying tofu-estate=$ESTATE and crash-second does not, both read with kubectl; stock from the same position plans exactly one add (crash_second). Records $X_RECORDS_BEFORE -> $X_RECORDS_AFTER $CRASH_RENAME_DETAIL"
  else
    R_APPLY="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)"; R_APPLY_RC=$?
    [ "$R_APPLY_RC" -eq 0 ] || { printf '%s\n' "$R_APPLY" | tail -20; fail "the recovery apply exited $R_APPLY_RC"; }
    grep -qF "Apply complete! Resources: 1 added, 0 changed, 0 destroyed" <<< "$R_APPLY" \
      || { printf '%s\n' "$R_APPLY" | tail -5; fail "the recovery apply did not add exactly the one remaining object"; }
    exists_db_a imagecatalog.postgresql.cnpg.io crash-second || fail "crash-second does not exist after the recovery apply"
    exists_db_a imagecatalog.postgresql.cnpg.io crash-first || fail "crash-first is gone after the recovery apply - the recovery replaced the object the crash created instead of binding it"
    R_REPLAN="$(tofu_a plan -input=false -no-color 2>&1)"; R_REPLAN_RC=$?
    [ "$R_REPLAN_RC" -eq 0 ] || { printf '%s\n' "$R_REPLAN" | tail -30; fail "the replan after the recovery exited $R_REPLAN_RC: $(grep -E '^Error' <<< "$R_REPLAN" | head -1)"; }
    grep -q "No changes." <<< "$R_REPLAN" || { printf '%s\n' "$R_REPLAN" | tail -30; fail "the replan after the recovery is not empty"; }
    C_AFTER="$(count_a)"
    [ "$C_AFTER" = "$((C_BEFORE + 2))" ] || fail "$C_AFTER labelled objects after the recovery, want $((C_BEFORE + 2))"
    gauntlet_stage day2_crash pass "an apply creating two ImageCatalogs - a custom kind served by CRDs this estate's own render installed - was interrupted by a real SIGTERM (exit $X_RC), delivered by the engine itself inside the -parallelism=1 walker the instant kubernetes_manifest.crash_first's create committed; crash_second's manifest reads crash_first's name, so the walker cannot have reached it. kubectl confirms crash-first exists carrying tofu-estate=$ESTATE and its address annotation, and crash-second does not; the interrupted apply recorded exactly the one object it created (records $X_RECORDS_BEFORE -> $X_RECORDS_AFTER, labels [$X_KEYS_L], annotations [$X_KEYS_A], #1211). The next plan proposed exactly the remainder ($R_LINE, crash_second created) and nothing for crash-first, matching stock from the same position on the oracle cluster; the recovery apply added one, the replan is empty and $C_AFTER objects carry the label, $C_BEFORE plus the pair. BREAK_CRASH=1 and BREAK_CRASH_UNBOUND=1 both correctly fail $CRASH_RENAME_DETAIL"
  fi
fi
gauntlet_end_stage

# ── 11. day2_teardown: destroy the adopted estate ────────────────────────
gauntlet_begin_stage day2_teardown
log "=== 11. day2_teardown: apply -destroy on the adopted estate, and stock's own destroy on B ==="
T_EXPECT="$(count_a)"
T_OUT="$(tofu_a apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$T_OUT" | tail -20; fail "apply -destroy failed"; }
grep -qF "Resources: 0 added, 0 changed, $T_EXPECT destroyed" <<< "$T_OUT" || { printf '%s\n' "$T_OUT" | tail -5; fail "apply -destroy did not remove exactly the $T_EXPECT remaining objects"; }
for _ in $(seq 1 90); do
  { kca get namespace "$NS" >/dev/null 2>&1 || kca get namespace "$DB_NS" >/dev/null 2>&1; } || break
  sleep 2
done
kca get namespace "$NS" >/dev/null 2>&1 && fail "the $NS namespace still exists after the destroy"
kca get namespace "$DB_NS" >/dev/null 2>&1 && fail "the $DB_NS namespace still exists after the destroy"
[ "$(count_a)" = "0" ] || fail "$(count_a) object(s) still carry tofu-estate=$ESTATE after the destroy"
CRDS_LEFT="$(kca get crd -o name 2>/dev/null | grep -c 'postgresql.cnpg.io')"
[ "$CRDS_LEFT" = "0" ] || fail "$CRDS_LEFT postgresql.cnpg.io CRD(s) survive the destroy"
# The cluster-scoped objects the render declared, by name: nothing the
# operator wrote is cluster-scoped, so every one of them must be gone.
for cs in "clusterrole $FULL" "clusterrole $FULL-view" "clusterrole $FULL-edit" "clusterrolebinding $FULL" "mutatingwebhookconfiguration $MWC" "validatingwebhookconfiguration $VWC"; do
  read -r kind name <<< "$cs"
  kca get "$kind" "$name" >/dev/null 2>&1 && fail "the cluster-scoped $kind $name survives the destroy"
done
kca get namespace kube-system >/dev/null 2>&1 || fail "kube-system is gone"
O_EXPECT="$(managed_n stock_b)"
O_T="$(stock_b apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$O_T" | tail -10; fail "stock's destroy failed on B"; }
grep -qF "Resources: 0 added, 0 changed, $O_EXPECT destroyed" <<< "$O_T" || fail "stock's destroy on B did not remove exactly the $O_EXPECT objects its state held"
gauntlet_stage day2_teardown pass "apply -destroy removed exactly the $T_EXPECT remaining objects in one apply - the Cluster before the operator's Deployment by its depends_on, the rendered objects before the CRDs and the Namespace by rest's; both namespaces are gone and with them the Pod, the claim, the Secrets and Services and the unowned $MONCM the operator generated, all $CRD_N postgresql.cnpg.io CRDs are gone, the three ClusterRoles, the ClusterRoleBinding and both webhook configurations are gone (kubectl, by name), and no object of the estate's kinds carries tofu-estate=$ESTATE in any namespace; stock's destroy on the oracle cluster removed exactly the $O_EXPECT its state held"

# ── 12. greenfield: the same shape, fresh, with a live block ─────────────
gauntlet_begin_stage greenfield
log "=== 12. greenfield: choudoufu applies the shape fresh on the now-empty cluster A ==="
write_root "$GREEN" live || fail "could not write the greenfield root"
( cd "$GREEN" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "greenfield init failed"
green() { ( cd "$GREEN" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" "$TOFU" "$@" ); }

G_TARGETS=()
while IFS= read -r a; do [ -n "$a" ] && G_TARGETS+=("-target=$a"); done < <(gauntlet_pre_apply_targets "$ESTATE")
[ "${#G_TARGETS[@]}" = "2" ] || fail "the declared pre-apply list has ${#G_TARGETS[@]} addresses, want 2 (the operator's Namespace and kubernetes_manifest.crds)"
G_PRE="$(green apply -auto-approve -input=false -no-color "${G_TARGETS[@]}" 2>&1)"; G_PRE_RC=$?
if [ "$G_PRE_RC" -ne 0 ]; then
  printf '%s\n' "$G_PRE" | tail -40
  gauntlet_stage greenfield fail "choudoufu cannot perform the pre-apply its own estate declares: the same ${#G_TARGETS[@]} -target arguments stock accepted at cold deploy, against the same empty cluster, exited $G_PRE_RC; the first error it printed was \"$(gauntlet_first_error_line <<< "$G_PRE")\""
  ( green apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 )
  GREENFIELD_SKIPPED=1
fi
if [ -z "${GREENFIELD_SKIPPED:-}" ]; then
grep -qF "Apply complete! Resources: $PRE_N added, 0 changed, 0 destroyed" <<< "$G_PRE" || { printf '%s\n' "$G_PRE" | tail -5; fail "the greenfield pre-apply did not add exactly the $PRE_N declared instances"; }
G_OUT="$(green apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$G_OUT" | tail -20; fail "greenfield apply failed"; }
grep -qF "Apply complete! Resources: $REST_N added, 0 changed, 0 destroyed" <<< "$G_OUT" || { printf '%s\n' "$G_OUT" | tail -5; fail "the greenfield main apply did not add exactly the $REST_N non-CRD instances"; }
[ ! -f "$GREEN/terraform.tfstate" ] || fail "a terraform.tfstate appeared after a live-block apply"
cnpg_ready "$KCA" "cluster A (greenfield)" || fail "CloudNativePG and its Cluster never became ready on A after the greenfield apply"
G_LABELLED="$(count_a)"
G_RECORDS="$(gauntlet_record_envelope_count "$GREEN/.tofu-records")"
G_PLAN="$(green plan -input=false -no-color 2>&1)" || fail "the greenfield replan failed"
grep -q "No changes." <<< "$G_PLAN" || { printf '%s\n' "$G_PLAN" | tail -20; fail "the greenfield replan is not empty"; }
rm -f "$GREEN/.terraform/choudoufu-cache.tfstate"
G_PLAN2="$(green plan -input=false -no-color 2>&1)" || fail "the greenfield replan without the cache failed"
grep -q "No changes." <<< "$G_PLAN2" || { printf '%s\n' "$G_PLAN2" | tail -20; fail "the greenfield replan without the cache is not empty"; }

# The lost-store control (#1235), read as reference-k8s-cert-manager reads
# it: every kubernetes_manifest records its declared metadata keys (#1211),
# so the store is full before it is deleted, and a missing key set proposes
# removing nothing. The two typed resources here, the Namespaces, may record
# residue of their own; an in-place update is reported against them by name
# and reconverged, never a create or a destroy.
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
  ( green apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "the converging apply after the lost store failed"
fi
log "  lost store: $L_LINE${L_OTHER:+ ($L_OTHER, reconverged)}"
G_WANT="$TOTAL_N"; [ "${BREAK:-}" = "1" ] && G_WANT=$((TOTAL_N + 1))
if [ "${BREAK:-}" = "1" ]; then
  if [ "$G_LABELLED" = "$G_WANT" ]; then
    fail "BREAK=1: the greenfield object count matched a deliberately wrong expectation ($G_WANT) - the count assertion is not load-bearing"
  fi
  gauntlet_stage greenfield pass "BREAK=1 control: expecting $G_WANT labelled objects where the estate has $G_LABELLED makes the count assertion correctly fail; the estate applied ($REST_N added after the pre-apply, no terraform.tfstate) and replanned empty with and without the cache"
else
  [ "$G_LABELLED" = "$TOTAL_N" ] || fail "$G_LABELLED object(s) carry tofu-estate=$ESTATE after the greenfield apply, want $TOTAL_N"
  gauntlet_stage greenfield pass "$TOTAL_N objects applied fresh with a live block and no terraform.tfstate - the render read by the data-read phase before the plan, the same declared pre-apply the cold deploy took ($PRE_N instances), then $REST_N, the Cluster admitted through the operator's own webhooks once its Deployment rolled out - every one labelled tofu-estate=$ESTATE (kubectl, 12 kinds) and the Cluster healthy with its claim Bound; replanned empty with and without the cache; the record store held $G_RECORDS envelope(s) for $TOTAL_N instances, and with that whole store deleted the plan read \"$L_LINE\", nothing created, destroyed or replaced and no kubernetes_manifest updated${L_OTHER:+ (proposed and reconverged: $L_OTHER)}. BREAK=1 expects a deliberately wrong object count and the assertion correctly fails"
fi
( green apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || log "  note: greenfield teardown did not exit clean; the cluster is deleted below regardless"
fi

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
    estate = "reference-k8s-helm-cloudnative-pg-strict"
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
  grep -qF 'strict { secrets = "refuse" }' <<< "$(tr -s ' \n' '  ' <<< "$STRICT_ON")" || { printf '%s\n' "$STRICT_ON"; fail "the refusal does not cite strict { secrets = \"refuse\" }"; }
  gauntlet_stage strict pass "every strict toggle on (secrets = refuse, no_source_create = refuse, marker_repair = never with a markers \"record\" selection naming kubernetes_manifest) against a scratch estate carrying random_password.db: exactly one refusal, Logical resource is not admitted under strict { secrets = \"refuse\" }; the other two toggles are on and silent. BREAK_STRICT=1 turns secrets back to \"store\" and the refusal disappears"
fi
gauntlet_end_stage

gauntlet_end
log "reference-k8s-helm-cloudnative-pg: done"
