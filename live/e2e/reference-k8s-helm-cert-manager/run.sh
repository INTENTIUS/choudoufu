#!/usr/bin/env bash
# reference-k8s-helm-cert-manager: cert-manager through its Helm chart
# (#1981), the kubernetes lane's helm_template shape (#1963) over the same
# software reference-k8s-cert-manager installs from its static bundle,
# crossed on the kind substrate.
#
# The configuration is jetstack's cert-manager chart rendered CLIENT-SIDE
# through hashicorp/helm's `data "helm_template"` and applied as
# kubernetes_manifest blocks. No helm_release anywhere: Helm never talks to
# the cluster, the provider is declared for the one data source.
#
#   chart    jetstack cert-manager v1.21.2 (app v1.21.2), Apache-2.0
#   archive  $CHART_URL below, sha256 $CHART_SHA256 below, checked on every run
#   render   44 documents at the chart's defaults (no CRDs, four
#            startupapicheck hook documents); 49 objects over 13 kinds with
#            root/values.yaml's deltas (crds.enabled on, startupapicheck
#            off, the three custom resources through extraObjects)
#   root     root/main.tf, ours, hence reference-: one data source, a
#            Namespace (helm template renders none), and four
#            kubernetes_manifest blocks - the 6 CRDs, 37 other installation
#            objects, the 3 Deployments, and the 3 custom resources - each
#            with for_each over the render split and yamldecoded INSIDE the
#            for_each expression, keyed kind/namespace/name, and
#            `manifest = each.value` (#1962)
#   images   quay.io/jetstack/cert-manager-{controller,webhook,cainjector}:v1.21.2,
#            public, linux/arm64 and linux/amd64 among their platforms
#
# Same version as reference-k8s-cert-manager (v1.21.2, the bundle that
# estate converts), and the latest the chart repository publishes. The
# chart's 46 objects match the bundle's 46 non-Namespace objects by kind,
# namespace and name, and field by field except two labels the chart adds
# (app.kubernetes.io/managed-by: Helm, helm.sh/chart); the three custom
# resources are that estate's three. So the two estates hold the same
# objects in two configuration shapes, and a stage that passes on one and
# not the other points at the shape.
#
# What it drives that nothing else in the lane does:
#
#   - the whole installation pre-applied (#1173) as -target over three
#     for_each blocks whose keys exist only once the data-read phase (#179)
#     has read the render: 4 addresses standing for 47 instances, where
#     reference-k8s-cert-manager lists 47 literal addresses and the other
#     Helm estates pre-apply their CRDs alone;
#   - custom resources that come out of the chart render (extraObjects)
#     and are admitted through a failurePolicy: Fail validating webhook,
#     including the plan-time server-side dry run, with no
#     wait { rollout } to lean on: the webhook only serves once cainjector
#     has injected the CA it generated at runtime, which a probe observes;
#   - one name across a cluster-scoped and a namespaced custom kind
#     (ClusterIssuer and Issuer, both selfsigned), the pair
#     drift_reconverge picks apart;
#   - a whole component taken out by one values toggle
#     (cainjector.enabled=false): 7 objects over 7 kinds in three scopes
#     (cluster, cert-manager, kube-system) and two blocks (rest and late),
#     while the caBundle it injected, the Secrets cert-manager wrote and
#     the Leases it took in kube-system stay;
#   - day-2 scaling of custom resources through values: day2_count's
#     Issuers are extraObjects entries.
#
# ── the two applies ──────────────────────────────────────────────────────
#
# kubernetes_manifest builds an object's schema at PLAN time, so a root
# declaring a CRD and an object of it cannot be planned against an empty
# cluster, by stock or by choudoufu, and both cert-manager webhooks are
# failurePolicy: Fail. cold_deploy runs the un-targeted plan as a CONTROL
# and requires it to fail, then performs the pre-apply
# live/gauntlet/estates.json declares (the Namespace and the crds, rest and
# late blocks) on both sides in one gauntlet_pre_apply call, waits -
# bounded, loudly - for the webhook to admit a dry-run Issuer, and only
# then runs the main apply, which creates the custom block.
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
#   go run ./tools/gauntlet run reference-k8s-helm-cert-manager
#   bash live/e2e/reference-k8s-helm-cert-manager/run.sh
#
# Needs kind, kubectl, terraform (the stock oracle), Docker, python3, jq and
# curl. Network: the chart archive once (then cached under
# $CHOUDOUFU_CHART_CACHE, default ~/.cache/choudoufu/charts, and re-checked
# against its sha256 on every run) and three public quay.io image pulls per
# cluster.
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
#   BREAK_REMOVE   keep cainjector enabled and assert no destroy is
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
ESTATE="reference-k8s-helm-cert-manager"
NS="cert-manager"   # the release namespace; the release is named cert-manager too

# The chart, pinned by digest. root/main.tf reads it from charts/ beside
# itself; write_root copies the verified archive there. The digest is the
# one charts.jetstack.io's index.yaml lists for this version.
CHART_VERSION="v1.21.2"
CHART_FILE="cert-manager-$CHART_VERSION.tgz"
CHART_URL="https://charts.jetstack.io/charts/$CHART_FILE"
CHART_SHA256="73a56e1728edd6c99f1f31082618c3259d279a76b7ebd3d4bdc5475c2442d34a"
CHART_CACHE="${CHOUDOUFU_CHART_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/choudoufu/charts}"

# The estate's kinds, for the label count: reference-k8s-cert-manager's 14.
# The last three only exist once the CRDs are installed; kubectl prints
# nothing for a kind the API server does not serve, which counts as zero,
# which is correct. CertificateRequests are deliberately absent: the
# controller creates them, owned by the Certificate, and the root declares
# none.
KINDS="namespaces customresourcedefinitions serviceaccounts clusterroles clusterrolebindings roles rolebindings services deployments mutatingwebhookconfigurations validatingwebhookconfigurations clusterissuers issuers certificates"
CRD_N=6       # kubernetes_manifest.crds
REST_N=37     # kubernetes_manifest.rest
LATE_N=3      # kubernetes_manifest.late, the three Deployments
CUSTOM_N=3    # kubernetes_manifest.custom: ClusterIssuer, Issuer, Certificate
PRE_N=$((1 + CRD_N + REST_N + LATE_N))   # 47: the Namespace and the installation, the pre-apply's population
TOTAL_N=$((PRE_N + CUSTOM_N))            # 50 managed instances, 49 of them kubernetes_manifest
MANIFEST_N=$((CRD_N + REST_N + LATE_N + CUSTOM_N))
CAINJ_N=7     # objects cainjector.enabled=false takes out of the render
WORK="$(mktemp -d)"
STOCK="$WORK/stock"; ADOPTED="$WORK/adopted"; ORACLE="$WORK/oracle"; GREEN="$WORK/green"
KCA="$WORK/a.kubeconfig"; KCB="$WORK/b.kubeconfig"
CLUSTER_A="${GAUNTLET_KIND_PREFIX:-chdf}-refhcm-a-$$"; CLUSTER_B="${GAUNTLET_KIND_PREFIX:-chdf}-refhcm-b-$$"  # GAUNTLET_KIND_PREFIX: lets concurrent workers name their own clusters
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

# set_shards <dir> <n>: day2_count's for_each scaling. Each shard is one
# more extraObjects entry, a self-signed Issuer named shard-<i> in $NS, so
# the number of entries is the number of instances of
# kubernetes_manifest.custom the render adds. extraObjects is values.yaml's
# last key and its shard marker comment the point from which this rewrites
# the file.
set_shards() {
  python3 - "$1/values.yaml" "$2" "$NS" <<'PY'
import sys
p, n, ns = sys.argv[1], int(sys.argv[2]), sys.argv[3]
marker = "  # day2_count's shards: run.sh's set_shards rewrites everything from this line to the end of the file.\n"
s = open(p).read()
assert s.count(marker) == 1, "values.yaml does not carry the shard marker exactly once"
i = s.index(marker) + len(marker)
block = ""
for k in range(n):
    block += ("  - |\n    apiVersion: cert-manager.io/v1\n    kind: Issuer\n    metadata:\n"
              "      name: shard-%d\n      namespace: %s\n    spec:\n      selfSigned: {}\n") % (k, ns)
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
# self-signed Issuers, behind the same failurePolicy: Fail webhook as the
# rendered ones, and crash_second's manifest reads crash_first's name, a
# real edge in the graph, so nothing can create crash-second until
# crash-first has committed (#490's discipline; reference-k8s-cert-manager's
# crash pair, #1262). crash_first depends on the late block, as the custom
# block does and keda's crash pair does: destroyed in reverse at teardown,
# the pair goes before the Deployments and so long before the CRDs that
# serve its kind, where a dependency on the Namespace alone left its
# destroy unordered against the CRDs' and the teardown verdict's order
# claim untrue for these two.
crash_block() {
  if [ "$1" = "first" ]; then
    cat <<EOF

resource "kubernetes_manifest" "crash_first" {
  manifest = {
    "apiVersion" = "cert-manager.io/v1"
    "kind"       = "Issuer"
    "metadata" = {
      "name"      = "crash-first"
      "namespace" = "$NS"
    }
    "spec" = {
      "selfSigned" = {}
    }
  }

  depends_on = [kubernetes_manifest.late]
}
EOF
  else
    cat <<EOF

resource "kubernetes_manifest" "crash_second" {
  manifest = {
    "apiVersion" = "cert-manager.io/v1"
    "kind"       = "Issuer"
    "metadata" = {
      "annotations" = {
        "after" = kubernetes_manifest.crash_first.manifest.metadata.name
      }
      "name"      = "crash-second"
      "namespace" = "$NS"
    }
    "spec" = {
      "selfSigned" = {}
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
# shellcheck disable=SC2086 # KINDS is a space-separated list, split on purpose
count_a() { KUBECONFIG="$KCA" gauntlet_kind_count "$ESTATE" $KINDS; }
# labelled_a: the objects count_a counts, as kind/namespace/name, on one
# line - for a failure line that says which.
labelled_a() {
  local k
  for k in $KINDS; do
    kca get "$k" -A -l "tofu-estate=$ESTATE" -o json 2>/dev/null | jq -r '.items[]? | "\(.kind)/\(.metadata.namespace // "")/\(.metadata.name)"' 2>/dev/null
  done | tr '\n' ' '
}

# copies <kubectl fn>: what cert-manager wrote at runtime that the root never
# declared, as Kind/namespace/name lines - the objects day2_remove's sweep
# must never propose. Every Secret in $NS (the webhook's CA Secret, the
# Certificate's issued Secret; the render declares no Secret), and the
# Leases cert-manager's controllers take in kube-system.
copies() {
  {
    "$1" get secrets -n "$NS" -o json 2>/dev/null | jq -r '.items[]? | "Secret/\(.metadata.namespace)/\(.metadata.name)"'
    "$1" get leases -n kube-system -o json 2>/dev/null | jq -r '.items[]? | select(.metadata.name | startswith("cert-manager")) | "Lease/kube-system/\(.metadata.name)"'
  } | sort
}

# webhook_admits <kubeconfig>: one server-side dry run of an Issuer. It is
# the only honest readiness test for a failurePolicy: Fail webhook - the
# Deployment being Available says the pods are up, not that cainjector has
# injected the CA and the webhook service answers - and kubernetes_manifest
# does exactly this dry run at plan time.
cat > "$WORK/probe-issuer.yaml" <<EOF
apiVersion: cert-manager.io/v1
kind: Issuer
metadata:
  name: webhook-readiness-probe
  namespace: $NS
spec:
  selfSigned: {}
EOF
webhook_admits() { kubectl --kubeconfig "$1" apply --dry-run=server -f "$WORK/probe-issuer.yaml"; }

# cert_manager_ready <kubeconfig> <label>: bounded and loud. The three
# Deployments Available, then the webhook admitting the probe; both fail
# the stage rather than falling through into the admission error a webhook
# that exists but is not yet serving produces (#1173).
cert_manager_ready() {
  local cfg="$1" label="$2"
  gauntlet_k8s_wait_all "$cfg" "$NS" deployment Available 300 "$label" || return 1
  gauntlet_wait_until 180 "the cert-manager validating webhook on $label to admit an Issuer (failurePolicy: Fail)" -- webhook_admits "$cfg"
}
# cert_ready <kubeconfig>: the Certificate issued by the self-signed Issuer.
cert_ready() {
  [ "$(kubectl --kubeconfig "$1" get certificate example-com -n "$NS" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null)" = "True" ]
}

# custom_addr <kind> <namespace> <name>: one instance address of the custom
# block.
custom_addr() { printf 'kubernetes_manifest.custom["%s/%s/%s"]' "$1" "$2" "$3"; }
# planned <plan>: the addresses a plan proposes something for, one per line.
planned() { grep -E '^[[:space:]]*# .+ (will be|must be)' <<< "$1" | sed -E 's/^[[:space:]]*# //; s/ (will be|must be).*$//'; }
# destroyed <plan>: the addresses a plan destroys, sorted, one per line;
# joined <lines>: the same on one line, "; "-separated.
destroyed() { { grep -E '^[[:space:]]*# .+ will be destroyed' <<< "$1" || true; } | sed -E 's/^[[:space:]]*# //; s/ will be destroyed.*$//' | sort; }
joined() { tr '\n' ';' <<< "$1" | sed -E 's/;+$//; s/;/; /g'; }
# plan_lines <plan>: the plan's summary and every action header, indented,
# for the log - what a stage that compares addresses prints whatever it
# decides, so a mismatch is readable from the log alone.
plan_lines() { { grep -E '^Plan:|^No changes|^[[:space:]]*# .+ (will be|must be)' <<< "$1" || true; } | sed -E 's/^[[:space:]]*/    /'; }
# objects_of: the addresses on stdin as the objects they name, one
# "kind|orphan_<ns>_<name>" line each, sorted - independent of the address
# a plan files the object under. An instance of any of the root's blocks
# (keyed kind/namespace/name) and the sweep's two orphan spellings (#1996:
# <typed resource>.orphan_[<ns>_]<name>, and
# kubernetes_manifest.orphan_<kind>_[<ns>_]<name>, kubesweep's
# OrphanResourceName and ManifestOrphanResourceName, which fold ":" in
# cert-manager-cainjector:leaderelection to "_") come out the same for the
# same object. Anything else is printed as "?|<address>", which never
# matches.
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
# record_addrs <records dir>: the address of every record envelope under
# it, sorted, one per line - how a stage reads whether a destroy took its
# record with it.
record_addrs() {
  python3 - "$1" <<'PY'
import json, os, sys
out = set()
for dirpath, _, names in os.walk(sys.argv[1]):
    for n in names:
        if n.endswith('.lock') or '.tmp-' in n or n == '.store-sentinel':
            continue
        try:
            with open(os.path.join(dirpath, n)) as fh:
                d = json.load(fh)
        except Exception:
            continue
        if isinstance(d, dict) and d.get('address'):
            out.add(d['address'])
print("\n".join(sorted(out)))
PY
}
# records_among <records> <addresses>: the addresses (one per line) that
# hold a record in <records> (record_addrs output), one per line; nothing
# when none does.
records_among() {
  local a
  while IFS= read -r a; do
    [ -n "$a" ] || continue
    if grep -qxF "$a" <<< "$1"; then printf '%s\n' "$a"; fi
  done <<< "$2"
  return 0
}

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
quiet stock_a init -input=false -no-color || fail "stock init failed on A"
gauntlet_report_lock "$STOCK"  # the versions stage 1 resolved, for the row (#1739)
quiet stock_b init -input=false -no-color || fail "stock init failed on B"

CTRL="$(stock_a plan -input=false -no-color 2>&1)"; CTRL_RC=$?
if [ "${BREAK_PREAPPLY:-}" = "1" ]; then
  if [ "$CTRL_RC" -eq 0 ]; then
    fail "BREAK_PREAPPLY=1: the un-targeted plan succeeded, so the declared pre-apply is not load-bearing and this estate proves nothing about #1173"
  fi
  log "  BREAK_PREAPPLY=1: caught - the one-pass plan exited $CTRL_RC: $(grep -m1 'no matches for kind' <<< "$CTRL")"
  gauntlet_stage cold_deploy pass "BREAK_PREAPPLY=1 control: planning the whole root in one pass exits $CTRL_RC before creating anything, so 'one apply is enough' correctly fails; the real two-apply crossing is skipped"
else
  [ "$CTRL_RC" -ne 0 ] || fail "the un-targeted plan SUCCEEDED; the CRD plan-time refusal this estate exists to exercise is gone, and the declared pre-apply is now measuring nothing (delete it, or find out what changed)"
  grep -qF "CRD may not be installed" <<< "$CTRL" || { printf '%s\n' "$CTRL" | tail -20; fail "the un-targeted plan failed for some reason other than the missing CRD"; }
  CTRL_N="$(grep -c 'CRD may not be installed' <<< "$CTRL")"
  CTRL_LINE="$(grep -m1 'no matches for kind' <<< "$CTRL" | sed 's/^ *//')"
  log "  control: the one-pass plan fails as documented, $CTRL_N missing-CRD refusal(s) - $CTRL_LINE"

  pre_apply_estate() { stock_a apply -auto-approve -input=false -no-color "$@" >/dev/null; }
  pre_apply_oracle() { stock_b apply -auto-approve -input=false -no-color "$@" >/dev/null; }
  gauntlet_pre_apply "$ESTATE" estate:pre_apply_estate oracle:pre_apply_oracle \
    || fail "the declared pre-apply failed"
  PRE_NOTE="$(gauntlet_pre_apply_note)" || fail "the pre-apply ran but produced no note to put in the verdict"
  [ "$(managed_n stock_a)" = "$PRE_N" ] || fail "the pre-apply on A left $(managed_n stock_a) managed instances in state, want $PRE_N (the Namespace, $CRD_N CRDs, $REST_N other installation objects and $LATE_N Deployments)"

  cert_manager_ready "$KCA" "cluster A" || fail "cert-manager never became ready on A after the pre-apply"
  cert_manager_ready "$KCB" "cluster B" || fail "cert-manager never became ready on B after the pre-apply"

  COLD_OUT="$(stock_a apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$COLD_OUT" | tail -20; fail "stock's main apply failed on A"; }
  grep -qF "Apply complete! Resources: $CUSTOM_N added, 0 changed, 0 destroyed" <<< "$COLD_OUT" || { printf '%s\n' "$COLD_OUT" | tail -30; fail "stock's main apply on A did not add exactly the $CUSTOM_N rendered custom resources"; }
  quiet stock_b apply -auto-approve -input=false -no-color || fail "stock's main apply failed on B"

  [ -f "$STOCK/terraform.tfstate" ] || fail "stock left no terraform.tfstate on A"
  STOCK_N="$(managed_n stock_a)"
  [ "$STOCK_N" = "$TOTAL_N" ] || fail "stock's state holds $STOCK_N managed instances, want $TOTAL_N"
  gauntlet_wait_until 120 "the Certificate example-com to be Ready on cluster A" -- cert_ready "$KCA" || fail "the Certificate is not Ready after stock's cold deploy on A; the self-signed issuance never completed and later stages would measure a broken estate"
  gauntlet_wait_until 120 "the Certificate example-com to be Ready on cluster B" -- cert_ready "$KCB" || fail "the Certificate is not Ready after stock's cold deploy on B"
  UNMARKED="$(count_a)"
  [ "$UNMARKED" = "0" ] || fail "$UNMARKED object(s) already carry tofu-estate=$ESTATE after a plain stock apply - this proves nothing"
  STOCK_REPLAN="$(stock_a plan -detailed-exitcode -input=false -no-color 2>&1)"; STOCK_REPLAN_RC=$?
  [ "$STOCK_REPLAN_RC" -eq 0 ] || { printf '%s\n' "$STOCK_REPLAN" | tail -20; fail "stock's own replan after its cold deploy is not empty (exit $STOCK_REPLAN_RC) - a provider/controller fight in the shape (cainjector's caBundle, say), which would be blamed on choudoufu later"; }
  COPIES="$(copies kca | tr '\n' ';' | sed -E 's/;$//; s/;/; /g')"
  log "  $TOTAL_N objects on A ($PRE_N pre-applied + $CUSTOM_N), written by cert-manager at runtime: $COPIES"
  gauntlet_stage cold_deploy pass "$TOTAL_N objects from plain terraform against kind $K8S_VER: cert-manager chart $CHART_VERSION (sha256 $CHART_SHA256) rendered by data.helm_template.cert_manager into $MANIFEST_N kubernetes_manifest instances over 13 kinds under four for_each blocks keyed kind/namespace/name (the $CRD_N CRDs, $REST_N other installation objects, the $LATE_N Deployments after the RBAC they need, and the $CUSTOM_N custom resources from extraObjects), plus the Namespace; a real terraform.tfstate with $TOTAL_N managed instances, zero tofu-estate labels read back with kubectl, the three Deployments Available, the failurePolicy: Fail webhook admitting a dry-run Issuer before the main apply, the Certificate Ready=True from the self-signed Issuer, and stock's own replan empty. The identical shape cold-deployed by stock on a second cluster as every later stage's oracle. Two applies, and the first is declared: $PRE_NOTE. Control, run first on this same cluster: the un-targeted one-pass plan exits $CTRL_RC with $CTRL_N missing-CRD refusal(s) before creating anything (\"$CTRL_LINE\"), so the pre-apply is load-bearing. Written by cert-manager at runtime, declared nowhere: $COPIES"
fi

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
  if [ "$LABELLED" = "$TOTAL_N" ]; then
    gauntlet_stage migrate pass "$TOTAL_N of $TOTAL_N stamped, 0 skipped (${SUMMARY_LINE:-no summary line}); every object carries tofu-estate=$ESTATE, counted back with kubectl across all 14 kinds including the three custom ones, whose stamping updates went through the failurePolicy: Fail webhook, and the four Roles and RoleBindings in kube-system. The eligibility line was: ${ELIGIBLE_LINE:-none}"
  else
    gauntlet_stage migrate fail "live-import reported $TOTAL_N stamped but $LABELLED object(s) carry tofu-estate=$ESTATE on the cluster"
  fi
else
  gauntlet_stage migrate fail "live-import -approve did not bind every instance: ${SUMMARY_LINE:-no summary line}"
fi

# ── 3. test_plan: replan from nothing ────────────────────────────────────
gauntlet_begin_stage test_plan
log "=== 3. test_plan: choudoufu plan with no state file, identities read with kubectl ==="
PLAN_OUT="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$PLAN_OUT" | tail -20; fail "the post-migration plan failed"; }
# The widest name group (cert-manager-webhook, five kinds over two scopes),
# the custom-kind pair (selfsigned, cluster-scoped and namespaced), the
# kube-system RBAC, a CRD and the Certificate: each must be its own object.
IDS_OK=1; IDS_MISSING=""
for spec in "serviceaccount $NS cert-manager-webhook" "service $NS cert-manager-webhook" "deployment $NS cert-manager-webhook" \
            "mutatingwebhookconfiguration - cert-manager-webhook" "validatingwebhookconfiguration - cert-manager-webhook" \
            "clusterissuer - selfsigned" "issuer $NS selfsigned" "certificate $NS example-com" \
            "customresourcedefinition - certificates.cert-manager.io" \
            "role kube-system cert-manager:leaderelection" "rolebinding kube-system cert-manager:leaderelection" \
            "role kube-system cert-manager-cainjector:leaderelection" "rolebinding kube-system cert-manager-cainjector:leaderelection"; do
  read -r kind where name <<< "$spec"
  if [ "$where" = "-" ]; then
    kca get "$kind" "$name" >/dev/null 2>&1 || { IDS_OK=0; IDS_MISSING="$IDS_MISSING $kind/$name"; }
  else
    kca get "$kind" "$name" -n "$where" >/dev/null 2>&1 || { IDS_OK=0; IDS_MISSING="$IDS_MISSING $where/$kind/$name"; }
  fi
done
if grep -q "No changes." <<< "$PLAN_OUT" && [ "$IDS_OK" = "1" ]; then
  gauntlet_stage test_plan pass "the plan with no state file is empty over $TOTAL_N instances whose for_each keys and identities come from the helm_template render the data-read phase read; cert-manager-webhook, one name across five kinds in two scopes (ServiceAccount, Service, Deployment, MutatingWebhookConfiguration, ValidatingWebhookConfiguration), and selfsigned, one name across a cluster-scoped ClusterIssuer and a namespaced Issuer, confirmed present as seven objects with kubectl, with the four Roles and RoleBindings in kube-system, a CRD and the Certificate"
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
grep -qF "Apply complete! Resources: 0 added, 0 changed, 0 destroyed" <<< "$NOOP_OUT" || { printf '%s\n' "$NOOP_OUT" | tail -30; fail "the no-op apply changed something"; }
AFTER_N="$(count_a)"
[ "$BEFORE_N" = "$AFTER_N" ] || fail "labelled-object count moved across a no-op apply: $BEFORE_N -> $AFTER_N"
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed) over $TOTAL_N instances; objects carrying tofu-estate=$ESTATE unchanged at $BEFORE_N across the estate's 14 kinds, counted with kubectl"

# ── 5. drift_reconverge: the Issuer out of the selfsigned pair ───────────
gauntlet_begin_stage drift_reconverge
DRIFT_ADDR="$(custom_addr Issuer "$NS" selfsigned)"
log "=== 5. drift_reconverge: the Issuer selfsigned is deleted out of band on A and on B; the ClusterIssuer of the same name stays ==="
# A delete, not a patch: every object here is server-side applied, and a
# kubectl patch makes kubectl a field manager (reference-k8s-cert-manager
# measured the conflict on both sides). The deleted object shares its name
# with a cluster-scoped object of another custom kind, so the plan has to
# name it by kind and namespace. The Certificate it issued stays Ready: its
# Secret is already written.
kca delete issuer selfsigned -n "$NS" >/dev/null || fail "could not delete the Issuer out of band on A"
kcb delete issuer selfsigned -n "$NS" >/dev/null || fail "could not delete the Issuer out of band on B"
if [ "${BREAK:-}" = "1" ]; then
  kca delete clusterissuer selfsigned >/dev/null || fail "BREAK: could not delete a second object on A"
fi
ORACLE_PLAN="$(stock_b plan -detailed-exitcode -input=false -no-color 2>&1)"; ORACLE_RC=$?
[ "$ORACLE_RC" -eq 2 ] || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B after the out-of-band delete exited $ORACLE_RC, want 2 (changes)"; }
grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$ORACLE_PLAN" || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B does not propose exactly one create"; }
grep -qF "$DRIFT_ADDR" <<< "$ORACLE_PLAN" || { plan_lines "$ORACLE_PLAN"; fail "stock's plan on B does not name $DRIFT_ADDR"; }
quiet stock_b apply -auto-approve -input=false -no-color || fail "stock could not reconverge B"
DRIFT_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$DRIFT_PLAN" | tail -20; fail "the plan after the out-of-band delete failed"; }
if [ "${BREAK:-}" = "1" ]; then
  if grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$DRIFT_PLAN"; then
    fail "BREAK=1: two objects were deleted but the plan still proposes exactly one create - the single-object assertion is not load-bearing"
  fi
  log "  BREAK=1: caught - with a second object deleted the plan is $(grep -E '^Plan:' <<< "$DRIFT_PLAN" | head -1)"
  quiet tofu_a apply -auto-approve -input=false -no-color || fail "BREAK: could not reconverge A"
  gauntlet_stage drift_reconverge pass "BREAK=1 control: with the same-named ClusterIssuer deleted too the single-object assertion correctly fails to hold ($(grep -E '^Plan:' <<< "$DRIFT_PLAN" | head -1)); reconverged afterwards"
else
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$DRIFT_PLAN" || { printf '%s\n' "$DRIFT_PLAN" | tail -20; fail "the plan after one out-of-band delete does not propose exactly one create"; }
  D_PLANNED="$(planned "$DRIFT_PLAN")"
  [ "$D_PLANNED" = "$DRIFT_ADDR" ] || { printf '%s\n' "$D_PLANNED"; fail "the plan does not propose exactly $DRIFT_ADDR: $(tr '\n' ';' <<< "$D_PLANNED")"; }
  RECONV="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$RECONV" | tail -20; fail "the reconverging apply failed"; }
  grep -qF "Apply complete! Resources: 1 added, 0 changed, 0 destroyed" <<< "$RECONV" || { printf '%s\n' "$RECONV" | tail -10; fail "the reconverging apply did not create exactly one object"; }
  exists_a issuer selfsigned || fail "the Issuer does not exist after reconverging"
  kca get clusterissuer selfsigned >/dev/null 2>&1 || fail "the ClusterIssuer selfsigned is gone after reconverging"
  [ "$(count_a)" = "$TOTAL_N" ] || fail "$(count_a) labelled objects after reconverging, want $TOTAL_N - the recreated object did not get its marker"
  gauntlet_stage drift_reconverge pass "an Issuer that shares its name (selfsigned) with a cluster-scoped ClusterIssuer was deleted out of band with kubectl; choudoufu proposed putting back exactly $DRIFT_ADDR (1 add, 0 change, 0 destroy) and nothing for the ClusterIssuer, matching stock's own plan on the oracle cluster; apply created 1 through the failurePolicy: Fail webhook, both objects read back with kubectl and the recreated one carries the estate label again ($TOTAL_N labelled). A delete rather than a patch because every object is server-side applied. BREAK=1 deletes the same-named ClusterIssuer as well and the single-object assertion correctly fails"
fi

# ── 6. plan_approval: plan -out, the world moves, apply refuses ──────────
gauntlet_begin_stage plan_approval
log "=== 6. plan_approval: a values edit saved as a plan, an out-of-band move, a refusal; then the same file applies ==="
CERT_ADDR="$(custom_addr Certificate "$NS" example-com)"
set_value "$ADOPTED" "      revisionHistoryLimit: 1" "      revisionHistoryLimit: 2" || fail "could not edit the Certificate's revisionHistoryLimit in the adopted root"
set_value "$ORACLE"  "      revisionHistoryLimit: 1" "      revisionHistoryLimit: 2" || fail "could not edit the Certificate's revisionHistoryLimit in the oracle root"
P_PLAN="$(tofu_a plan -out=approved.tfplan -input=false -no-color 2>&1)"; P_PLAN_RC=$?
P_PLAN_LINE="$(grep -E '^Plan:|^No changes' <<< "$P_PLAN" | head -1 | sed 's/\.$//')"
if [ "$P_PLAN_RC" -ne 0 ] || ! grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$P_PLAN" || ! grep -qF "$CERT_ADDR will be updated in-place" <<< "$P_PLAN"; then
  gauntlet_stage plan_approval fail "the Certificate's revisionHistoryLimit 1 -> 2 in values.yaml's extraObjects did not plan as exactly one in-place update of $CERT_ADDR: ${P_PLAN_LINE:-no summary line} (exit $P_PLAN_RC). What it proposed: $(planned "$P_PLAN" | head -10 | tr '\n' ';')"
  printf '%s\n' "$P_PLAN" | tail -30
  quiet tofu_a apply -auto-approve -input=false -no-color || fail "could not converge A after plan_approval; nothing below would measure day-2 behaviour"
  quiet stock_b apply -auto-approve -input=false -no-color || fail "could not converge B after plan_approval; the oracle would be stale for every stage below"
  PLAN_APPROVAL_SKIPPED=1
fi
if [ -z "${PLAN_APPROVAL_SKIPPED:-}" ]; then
# The world moves on a spec-side field of an object the saved plan does not
# touch: the cainjector Deployment declares replicas: 1 in the render, and
# it is put back below (reference-k8s-cert-manager's mover, on the same
# object).
kca patch deployment cert-manager-cainjector -n "$NS" --type merge -p '{"spec":{"replicas":2}}' >/dev/null || fail "could not move the world (the cainjector Deployment's replicas) on A"
P_APPLY="$(tofu_a apply -input=false -no-color approved.tfplan 2>&1)"; P_RC=$?
if [ "${BREAK_APPROVAL:-}" = "1" ]; then
  [ "$P_RC" -eq 0 ] && fail "BREAK_APPROVAL=1: applying the saved plan after the world moved succeeded - the refusal is not load-bearing"
  log "  BREAK_APPROVAL=1: caught - the apply after the world moved exited $P_RC"
  kca patch deployment cert-manager-cainjector -n "$NS" --type merge -p '{"spec":{"replicas":1}}' >/dev/null
  quiet tofu_a apply -input=false -no-color approved.tfplan || fail "BREAK_APPROVAL: the saved plan did not apply once the world was put back"
  quiet stock_b apply -auto-approve -input=false -no-color || fail "BREAK_APPROVAL: stock could not apply the change on B"
  gauntlet_stage plan_approval pass "BREAK_APPROVAL=1 control: applying the saved plan after the world moved exited $P_RC (refused), so the stage's own Break line correctly fails; applied once the world was put back"
else
  if [ "$P_RC" -ne 3 ] || ! grep -qF "The approved plan no longer matches the live system" <<< "$P_APPLY"; then
    printf '%s\n' "$P_APPLY" | tail -20
    gauntlet_stage plan_approval fail "the saved plan was applied after the world had moved out of band (the cainjector Deployment's replicas 1 -> 2, kubectl) and choudoufu exited $P_RC rather than refusing at 3 with \"The approved plan no longer matches the live system\". The saved plan's own change was one in-place update of $CERT_ADDR from a values edit"
    kca patch deployment cert-manager-cainjector -n "$NS" --type merge -p '{"spec":{"replicas":1}}' >/dev/null
    quiet tofu_a apply -auto-approve -input=false -no-color || fail "could not converge A after plan_approval; nothing below would measure day-2 behaviour"
    quiet stock_b apply -auto-approve -input=false -no-color || fail "could not converge B after plan_approval; the oracle would be stale for every stage below"
    PLAN_APPROVAL_SKIPPED=1
  fi
  if [ -z "${PLAN_APPROVAL_SKIPPED:-}" ]; then
  RHL_NOW="$(kca get certificate example-com -n "$NS" -o jsonpath='{.spec.revisionHistoryLimit}')"
  [ "$RHL_NOW" = "1" ] || fail "the Certificate's revisionHistoryLimit reads $RHL_NOW despite the refusal, want 1"
  kca patch deployment cert-manager-cainjector -n "$NS" --type merge -p '{"spec":{"replicas":1}}' >/dev/null || fail "could not put the world back"
  P_APPLY2="$(tofu_a apply -input=false -no-color approved.tfplan 2>&1)" || { printf '%s\n' "$P_APPLY2" | tail -20; fail "the saved plan did not apply once the world was put back"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$P_APPLY2" || { printf '%s\n' "$P_APPLY2" | tail -30; fail "the saved plan's apply did not change exactly one object"; }
  [ "$(kca get certificate example-com -n "$NS" -o jsonpath='{.spec.revisionHistoryLimit}')" = "2" ] || fail "the Certificate does not read revisionHistoryLimit 2 after the saved plan applied"
  O_PA="$(stock_b plan -out=approved.tfplan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PA" | tail -30; fail "stock's own planfile failed on B"; }
  grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$O_PA" || { printf '%s\n' "$O_PA" | tail -10; fail "stock's plan for the same values edit on B is not exactly one in-place update"; }
  quiet stock_b apply -input=false -no-color approved.tfplan || fail "stock's own planfile did not apply on B"
  gauntlet_stage plan_approval pass "a values.yaml edit (the Certificate's revisionHistoryLimit 1 -> 2, inside extraObjects) re-rendered through helm_template planned as exactly one in-place update of $CERT_ADDR, a custom resource behind the failurePolicy: Fail webhook, saved with plan -out; the world then moved out of band (the cainjector Deployment's replicas to 2 with kubectl, never choudoufu) and apply of the saved plan refused with \"The approved plan no longer matches the live system\" at exit 3, nothing applied (kubectl still reads 1); with the world put back the identical file applied, 0 added, 1 changed, 0 destroyed, and 2 reads back; stock's plan for the same edit on the oracle cluster is the same one update and its planfile applied. BREAK_APPROVAL=1 expects success after the move and correctly fails"
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
# carries an annotation of its own (helm.sh/resource-policy: keep, from the
# chart's crds.keep default), so the address line reads
# `"...address"   =` with padding: one or more spaces before the "=".
R_ANN="$(grep -cE '~ +"choudoufu\.intentius\.io/tofu-address" += ".*" -> ".*"' <<< "$R_PLAN")"
if grep -qE 'will be (created|destroyed)|must be replaced' <<< "$R_PLAN"; then
  gauntlet_stage day2_rename fail "the moved-block plan over $CRD_N for_each CRD instances proposes a create, a destroy or a replace - not the marker rewritten in place: $(grep -E '^Plan:' <<< "$R_PLAN" | head -1)"
  printf '%s\n' "$R_PLAN" | tail -20
  quiet tofu_a apply -auto-approve -input=false -no-color || fail "could not converge after the rename; nothing below would measure day-2 behaviour"
elif grep -qF "Plan: 0 to add, $CRD_N to change, 0 to destroy." <<< "$R_PLAN" && [ "$R_ANN" = "$CRD_N" ]; then
  R_APPLY_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$R_APPLY_OUT" | tail -20; fail "the moved-block apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, $CRD_N changed, 0 destroyed" <<< "$R_APPLY_OUT" || { printf '%s\n' "$R_APPLY_OUT" | tail -10; fail "the moved-block apply was not exactly $CRD_N in-place changes"; }
  [ "$(kca get crd -o name | grep -c 'cert-manager.io')" = "$CRD_N" ] || fail "the CRD count moved across the rename"
  [ "$(count_a)" = "$TOTAL_N" ] || fail "$(count_a) labelled objects after the rename, want $TOTAL_N"
  R_REPLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_REPLAN" | tail -30; fail "the replan after the rename failed"; }
  grep -q "No changes." <<< "$R_REPLAN" || { printf '%s\n' "$R_REPLAN" | tail -20; fail "the replan after the rename is not empty"; }
  gauntlet_stage day2_rename pass "moved block over a for_each of $CRD_N CRDs keyed by the render (kubernetes_manifest.crds -> .custom_resource_definitions, with the rest block's depends_on following it): no add and no destroy, $CRD_N in-place changes each confined to the address annotation rewrite ($R_ANN annotation lines, 0 add, $CRD_N change, 0 destroy) - the marker rewritten in place, as the AWS lanes assert for a rename; every CRD still there and labelled, the replan empty; stock's plan for the same moved block on the oracle cluster is zero churn, since stock never writes this annotation"
else
  gauntlet_stage day2_rename fail "the moved-block plan over $CRD_N for_each CRD instances is not exactly $CRD_N in-place annotation changes: $(grep -E '^Plan:' <<< "$R_PLAN" | head -1), $R_ANN annotation line(s)"
  printf '%s\n' "$R_PLAN" | tail -20
  quiet tofu_a apply -auto-approve -input=false -no-color || fail "could not converge after the rename; nothing below would measure day-2 behaviour"
fi

# ── 8. day2_remove: one values toggle takes a component out ──────────────
gauntlet_begin_stage day2_remove
CAINJ_ON=$'cainjector:\n  enabled: true'
CAINJ_OFF=$'cainjector:\n  enabled: false'
log "=== 8. day2_remove: cainjector.enabled=false removes $CAINJ_N rendered objects; what cert-manager wrote at runtime must be left alone ==="
COPIES_BEFORE="$(copies kca)"
grep -q "^Secret/$NS/cert-manager-webhook-ca$" <<< "$COPIES_BEFORE" || fail "the webhook's own CA Secret does not exist before the removal; the controller-copy check would measure less than it says: $(tr '\n' ';' <<< "$COPIES_BEFORE")"
grep -q "^Secret/$NS/example-com-tls$" <<< "$COPIES_BEFORE" || fail "the Certificate's issued Secret does not exist before the removal: $(tr '\n' ';' <<< "$COPIES_BEFORE")"
grep -q '^Lease/kube-system/' <<< "$COPIES_BEFORE" || fail "no cert-manager Lease exists in kube-system before the removal: $(tr '\n' ';' <<< "$COPIES_BEFORE")"
if [ "${BREAK_REMOVE:-}" = "1" ]; then
  K_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$K_PLAN" | tail -30; fail "BREAK_REMOVE: the plan with cainjector kept failed"; }
  grep -qE "will be destroyed|[1-9][0-9]* to destroy" <<< "$K_PLAN" && fail "BREAK_REMOVE=1: with cainjector kept a destroy was still proposed - the destroys below would not be the values change's doing"
  log "  BREAK_REMOVE=1: caught - with cainjector kept the plan proposes no destroy"
  gauntlet_stage day2_remove pass "BREAK_REMOVE=1 control: with cainjector kept, no destroy is proposed; the real check is skipped"
  { set_value "$ADOPTED" "$CAINJ_ON" "$CAINJ_OFF" && set_value "$ORACLE" "$CAINJ_ON" "$CAINJ_OFF"; } || fail "BREAK_REMOVE: could not disable cainjector afterwards"
  quiet tofu_a apply -auto-approve -input=false -no-color || fail "BREAK_REMOVE: the removal apply failed afterwards"
  quiet stock_b apply -auto-approve -input=false -no-color || fail "BREAK_REMOVE: stock's removal apply failed on B afterwards"
else
  # Every way choudoufu can miss is recorded here and the stage carries on,
  # so the removal is applied on both sides and every later stage starts
  # where it expects: one fail verdict at the end with all of them. fail
  # (which exits) is kept for what leaves nothing to measure - the oracle, a
  # command that could not run, a webhook that no longer admits.
  REMOVE_FAILS=""
  remove_fail() { REMOVE_FAILS="${REMOVE_FAILS:+$REMOVE_FAILS; }$*"; }
  set_value "$ADOPTED" "$CAINJ_ON" "$CAINJ_OFF" || fail "could not disable cainjector in the adopted root"
  set_value "$ORACLE"  "$CAINJ_ON" "$CAINJ_OFF" || fail "could not disable cainjector in the oracle root"
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -30; fail "stock's remove plan failed on B"; }
  log "  stock's remove plan on B:"; plan_lines "$O_PLAN"
  grep -qF "Plan: 0 to add, 0 to change, $CAINJ_N to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -30; fail "stock's remove plan on B is not exactly $CAINJ_N destroys"; }
  O_DEST="$(destroyed "$O_PLAN")"
  O_OBJS="$(objects_of <<< "$O_DEST")"
  quiet stock_b apply -auto-approve -input=false -no-color || fail "stock's remove apply failed on B"

  D_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$D_PLAN" | tail -30; fail "the remove plan failed"; }
  log "  choudoufu's remove plan on A:"; plan_lines "$D_PLAN"
  D_LINE="$(gauntlet_plan_line <<< "$D_PLAN")"
  D_DEST="$(destroyed "$D_PLAN")"
  D_OBJS="$(objects_of <<< "$D_DEST")"
  grep -qF "Plan: 0 to add, 0 to change, $CAINJ_N to destroy." <<< "$D_PLAN" \
    || remove_fail "the remove plan is ${D_LINE:-no plan line}, not exactly $CAINJ_N destroys; it proposes [$(joined "$(planned "$D_PLAN")")]"
  # By address first, the way stock plans it. A difference is #1996 (the
  # sweep files a for_each instance that left the render under
  # <typed resource>.orphan_..., although the object carries the
  # tofu-address annotation naming its instance) and is a fail; it is never
  # accepted as a pass.
  if [ "$D_DEST" != "$O_DEST" ]; then
    remove_fail "the remove plan's $CAINJ_N destroys are not stock's: choudoufu proposes [$(joined "$D_DEST")], stock on the oracle cluster [$(joined "$O_DEST")] (#1996)"
  fi
  # Then by object, whatever the address: the same kind/namespace/name set.
  if [ "$D_OBJS" != "$O_OBJS" ]; then
    remove_fail "the remove plan does not destroy the same objects as stock's: choudoufu [$(joined "$D_OBJS")], stock [$(joined "$O_OBJS")]"
  fi
  # The controller-copy half: nothing cert-manager wrote may be named, by
  # name in a destroy header or as an object.
  D_HEADERS="$({ grep -E 'will be destroyed' <<< "$D_PLAN" || true; })"
  while IFS= read -r copy; do
    [ -n "$copy" ] || continue
    ckind="${copy%%/*}"; crest="${copy#*/}"; cns="${crest%%/*}"; cname="${crest#*/}"
    cobj="$(printf '%s\n' "$ckind" | tr '[:upper:]' '[:lower:]')|$(printf 'orphan_%s_%s' "$cns" "$cname" | sed -E 's/[^A-Za-z0-9_|-]/_/g')"
    if grep -qF "$cname\"]" <<< "$D_HEADERS" || grep -qxF "$cobj" <<< "$D_OBJS"; then
      remove_fail "the remove plan proposes destroying $copy, which cert-manager wrote and the root never declared"
    fi
  done <<< "$COPIES_BEFORE"

  # Whether a destroy took its record with it. The record is keyed by the
  # instance's address; a destroy filed under an orphan address (#1996) may
  # not reach it. Which of stock's addresses held a record before is read
  # first, so the check says what it measured: live-import records nothing,
  # and only an apply that changed an instance writes one.
  RECS_BEFORE="$(record_addrs "$ADOPTED/.tofu-records")"
  RECS_HELD="$(records_among "$RECS_BEFORE" "$O_DEST")"
  RECS_HELD_N="$(grep -c . <<< "$RECS_HELD")"

  # Apply the removal whatever the plan's addresses, so the world is where
  # day2_count expects it; then read the objects back by kind and name.
  APPLY_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)"; APPLY_RC=$?
  if [ "$APPLY_RC" -ne 0 ] || ! grep -qF "0 added, 0 changed, $CAINJ_N destroyed" <<< "$APPLY_OUT"; then
    printf '%s\n' "$APPLY_OUT" | tail -30
    remove_fail "the remove apply exited $APPLY_RC and did not report exactly $CAINJ_N destroyed: $(grep -E '^Apply complete' <<< "$APPLY_OUT" | head -1) $(gauntlet_first_error_line <<< "$APPLY_OUT")"
  fi
  # Each of the seven, by kind/namespace/name; one still there is recorded
  # and deleted with kubectl, so the stages below see the world stock's
  # apply left on B.
  for spec in "deployment $NS cert-manager-cainjector" "serviceaccount $NS cert-manager-cainjector" "service $NS cert-manager-cainjector" \
              "clusterrole - cert-manager-cainjector" "clusterrolebinding - cert-manager-cainjector" \
              "role kube-system cert-manager-cainjector:leaderelection" "rolebinding kube-system cert-manager-cainjector:leaderelection"; do
    read -r kind where name <<< "$spec"
    obj=("$kind" "$name"); scope="cluster-wide"
    if [ "$where" != "-" ]; then obj+=(-n "$where"); scope="in $where"; fi
    if kca get "${obj[@]}" >/dev/null 2>&1; then
      remove_fail "the $kind $name still exists $scope after the remove apply"
      kca delete "${obj[@]}" --ignore-not-found >/dev/null 2>&1
    fi
  done
  kca get role cert-manager:leaderelection -n kube-system >/dev/null 2>&1 || remove_fail "the controller's kube-system Role cert-manager:leaderelection is gone; the removal reached an object the values change kept"
  kca get namespace kube-system >/dev/null 2>&1 || fail "kube-system is gone"
  # The CA cainjector already wrote into the webhook configurations is a
  # field of its own manager and nothing re-applies those objects, so with
  # cainjector gone the webhook must still admit, for every custom-resource
  # stage below. kubectl's own words are printed if it does not.
  ADMIT_OUT="$(webhook_admits "$KCA" 2>&1)" || { printf '%s\n' "$ADMIT_OUT" | tail -10; fail "the webhook no longer admits a dry-run Issuer after cainjector was removed; every custom-resource stage below would measure the substrate${REMOVE_FAILS:+ (this stage also found: $REMOVE_FAILS)}"; }
  CA_LEN="$(kca get validatingwebhookconfiguration cert-manager-webhook -o jsonpath='{.webhooks[0].clientConfig.caBundle}' 2>/dev/null | wc -c | tr -d ' ')"

  RECS_AFTER="$(record_addrs "$ADOPTED/.tofu-records")"
  RECS_LEFT="$(records_among "$RECS_AFTER" "$(printf '%s\n%s\n' "$O_DEST" "$D_DEST")")"
  if [ -n "$RECS_LEFT" ]; then
    remove_fail "the remove apply destroyed the objects but left their records behind in the record store: [$(joined "$RECS_LEFT")] ($RECS_HELD_N of stock's $CAINJ_N addresses held a record before the removal; #1996 files the destroy under another address)"
  fi
  if [ "$RECS_HELD_N" = "0" ]; then
    RECS_NOTE="none of stock's $CAINJ_N addresses held a record before the removal (live-import records nothing and no apply had changed them), so that no record was left measures nothing here; day2_count measures it on a shard choudoufu itself created"
  else
    RECS_NOTE="$RECS_HELD_N of stock's $CAINJ_N addresses held a record before the removal and none of them, nor any orphan address, holds one after"
  fi

  COPIES_AFTER="$(copies kca)"
  [ "$COPIES_AFTER" = "$COPIES_BEFORE" ] || remove_fail "what cert-manager wrote at runtime changed across the removal: before [$(joined "$COPIES_BEFORE")], after [$(joined "$COPIES_AFTER")]"
  D_REPLAN="$(tofu_a plan -input=false -no-color 2>&1)"; D_REPLAN_RC=$?
  if [ "$D_REPLAN_RC" -ne 0 ] || ! grep -q "No changes." <<< "$D_REPLAN"; then
    printf '%s\n' "$D_REPLAN" | tail -30
    remove_fail "the replan after the remove is not empty (exit $D_REPLAN_RC, $(gauntlet_plan_line <<< "$D_REPLAN")): [$(joined "$(planned "$D_REPLAN")")] $(gauntlet_first_error_line <<< "$D_REPLAN")"
    quiet tofu_a apply -auto-approve -input=false -no-color || fail "could not converge A after the remove; nothing below would measure day-2 behaviour${REMOVE_FAILS:+ (this stage also found: $REMOVE_FAILS)}"
  fi
  REMAIN=$((TOTAL_N - CAINJ_N))
  R_COUNT="$(count_a)"
  [ "$R_COUNT" = "$REMAIN" ] || remove_fail "$R_COUNT labelled objects after the remove, want $REMAIN: $(labelled_a)"
  N_COPIES="$(grep -c . <<< "$COPIES_AFTER")"

  SAME_OBJS="different sets"
  if [ "$D_OBJS" = "$O_OBJS" ]; then SAME_OBJS="the same $CAINJ_N"; fi
  if [ -n "$REMOVE_FAILS" ]; then
    gauntlet_stage day2_remove fail "cainjector.enabled=false in values.yaml ($CAINJ_N objects out of the render; stock on the oracle destroys [$(joined "$O_DEST")]): $REMOVE_FAILS. The removal was applied on both sides regardless and the seven read back gone with kubectl; destroyed as objects, choudoufu's plan and stock's name $SAME_OBJS; records: $RECS_NOTE; the webhook still admits a dry-run Issuer with cainjector gone (its caBundle $CA_LEN base64 characters), so every later stage starts where it expects"
  else
    gauntlet_stage day2_remove pass "cainjector.enabled=false in values.yaml took $CAINJ_N objects out of the render - a Deployment from the late block and a ServiceAccount, a Service, a ClusterRole, a ClusterRoleBinding, and a Role and RoleBinding in kube-system from the rest block, across three scopes and seven kinds - and choudoufu proposed exactly $CAINJ_N destroys at the instances' own addresses, the same addresses and objects stock proposes on the oracle cluster ($(joined "$D_DEST")); after the apply all seven are gone (kubectl), the controller's own kube-system Role untouched, the replan empty and $REMAIN objects labelled, $RECS_NOTE, and the webhook still admits a dry-run Issuer on the CA cainjector had injected (its caBundle $CA_LEN base64 characters). The controller-copy exclusion: the $N_COPIES objects cert-manager wrote at runtime and nothing declares ($(joined "$COPIES_AFTER")), the removed component's own Lease among them, were never proposed and read back identical. BREAK_REMOVE=1 keeps cainjector and requires no destroy"
  fi
fi

# ── 9. day2_count: for_each over the render, scaled through values ───────
# day2_remove's removal is still in effect here (cainjector stays off for
# the rest of the run, on both sides, as keda's metrics server does), so the
# render is $TOTAL_N - $CAINJ_N objects plus the shards.
#
# While #1996 is open the scale-down's destroy of shard-1 is filed under an
# orphan address where stock files it under the instance. day2_remove
# already judges that, so here the destroyed OBJECT must be stock's and the
# address difference is reported, tagged (#1996), without failing the stage
# for it (keda's day2_count reads the same way).
gauntlet_begin_stage day2_count
log "=== 9. day2_count: extraObjects shard Issuers 2 -> 1 -> 2, each entry one rendered Issuer ==="
SHARD0="$(custom_addr Issuer "$NS" shard-0)"
SHARD1="$(custom_addr Issuer "$NS" shard-1)"
COUNT_VERDICT=""
set_shards "$ADOPTED" 2 || fail "could not add two shard Issuers to the adopted root"
set_shards "$ORACLE" 2  || fail "could not add two shard Issuers to the oracle root"
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -30; fail "stock's plan for the two shards failed on B"; }
grep -qF "Plan: 2 to add, 0 to change, 0 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -30; fail "stock's plan for the two shards on B is not exactly two adds"; }
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
if [ "$A_UP_RC" -ne 0 ] || ! grep -qF "Plan: 2 to add, 0 to change, 0 to destroy." <<< "$A_UP"; then
  printf '%s\n' "$A_UP" | tail -30
  COUNT_VERDICT="choudoufu could not plan the two rendered shards' creation: exit $A_UP_RC, $(gauntlet_plan_line <<< "$A_UP"); proposed: $(joined "$(planned "$A_UP")"); first error: $(gauntlet_first_error_line <<< "$A_UP")"
else
  { APPLY_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)" && grep -qF "2 added, 0 changed, 0 destroyed" <<< "$APPLY_OUT"; } || { printf '%s\n' "$APPLY_OUT" | tail -30; fail "the shards' creating apply did not add exactly two objects"; }
  { exists_a issuer shard-0 && exists_a issuer shard-1; } || fail "both shards do not exist after choudoufu created them"
  A_RE="$(tofu_a plan -input=false -no-color 2>&1)"; A_RE_RC=$?
  if [ "$A_RE_RC" -ne 0 ] || ! grep -q "No changes." <<< "$A_RE"; then
    printf '%s\n' "$A_RE" | tail -30
    COUNT_VERDICT="replanning the root choudoufu itself had just applied with two shards is not empty (exit $A_RE_RC, $(gauntlet_plan_line <<< "$A_RE")); proposed: $(planned "$A_RE" | head -10 | tr '\n' ';'); first error: $(gauntlet_first_error_line <<< "$A_RE")"
  fi
fi

if [ -n "$COUNT_VERDICT" ]; then
  gauntlet_stage day2_count fail "$COUNT_VERDICT"
  { set_shards "$ADOPTED" 0 && set_shards "$ORACLE" 0; } || fail "could not withdraw the shards"
  quiet tofu_a apply -auto-approve -input=false -no-color || log "  note: the apply withdrawing the shards from A did not exit clean"
  kca delete issuer shard-0 shard-1 -n "$NS" --ignore-not-found >/dev/null 2>&1
  quiet stock_b apply -auto-approve -input=false -no-color || fail "the oracle root would not converge after the shards were withdrawn"
  CLEAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$CLEAN" | tail -30; fail "the plan after withdrawing the shards failed; nothing below would measure day-2 behaviour"; }
  grep -q "No changes." <<< "$CLEAN" || { printf '%s\n' "$CLEAN" | tail -30; fail "the plan after withdrawing the shards is not empty; nothing below would measure day-2 behaviour"; }
else
  set_shards "$ADOPTED" 1 || fail "could not scale the adopted root to 1"
  C_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$C_PLAN" | tail -30; fail "the scale-down plan failed"; }
  log "  choudoufu's scale-down plan on A:"; plan_lines "$C_PLAN"
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$C_PLAN" || { printf '%s\n' "$C_PLAN" | tail -30; fail "the scale-down plan is not exactly one destroy"; }
  C_DEST="$(destroyed "$C_PLAN")"
  C_OBJS="$(objects_of <<< "$C_DEST")"
  # The object, whatever the address: it must be the one stock destroys.
  [ "$C_OBJS" = "$O_DOWN_OBJS" ] || fail "the scale-down destroys a different object from stock's: choudoufu [$(joined "$C_DEST")] (object $(joined "$C_OBJS")), stock [$(joined "$O_DOWN_DEST")] (object $(joined "$O_DOWN_OBJS"))"
  if [ "$C_DEST" = "$O_DOWN_DEST" ]; then
    C_ADDR_NOTE="planned at stock's own address $C_DEST"
  else
    C_ADDR_NOTE="planned at $(joined "$C_DEST") where stock plans $(joined "$O_DOWN_DEST"), the same object under an orphan address (#1996, judged in day2_remove, not failed again here)"
  fi
  # shard-1 was created by choudoufu's own apply above, so its instance
  # holds a record; whether the destroy, under whatever address, took it.
  C_RECS_HELD="$(records_among "$(record_addrs "$ADOPTED/.tofu-records")" "$SHARD1")"
  { APPLY_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)" && grep -qF "0 added, 0 changed, 1 destroyed" <<< "$APPLY_OUT"; } || { printf '%s\n' "$APPLY_OUT" | tail -30; fail "the scale-down apply did not destroy exactly one object"; }
  if [ "${BREAK_COUNT:-}" = "1" ]; then
    exists_a issuer shard-0 || fail "BREAK_COUNT=1: shard-0 was destroyed - the 'wrong instance' assertion would hold, so the check is not load-bearing"
    log "  BREAK_COUNT=1: caught - shard-0 still exists, so asserting it was the one destroyed correctly fails"
    gauntlet_stage day2_count pass "BREAK_COUNT=1 control: asserting the remaining shard (shard-0) was destroyed correctly fails to hold; the real check is skipped"
  else
    exists_a issuer shard-0 || fail "shard-0 was destroyed on the scale-down"
    exists_a issuer shard-1 && fail "shard-1 still exists after the scale-down"
    C_RECS_LEFT="$(records_among "$(record_addrs "$ADOPTED/.tofu-records")" "$(printf '%s\n%s\n' "$SHARD1" "$C_DEST")")"
    if [ -z "$C_RECS_HELD" ]; then
      C_REC_NOTE="$SHARD1 held no record before the scale-down, so whether the destroy took one was not measured"
    elif [ -n "$C_RECS_LEFT" ]; then
      C_REC_NOTE="the destroy left the record of [$(joined "$C_RECS_LEFT")] behind in the record store (#1996: the destroy was filed under another address); what that costs is read by the scale-up below"
    else
      C_REC_NOTE="the record $SHARD1 held before the scale-down went with the destroy"
    fi
    log "  scale-down: $C_ADDR_NOTE; $C_REC_NOTE"
    { set_shards "$ADOPTED" 2 && set_shards "$ORACLE" 2; } || fail "could not scale back to 2"
    O_UP="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_UP" | tail -30; fail "stock's scale-up plan failed on B"; }
    grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_UP" || { printf '%s\n' "$O_UP" | tail -30; fail "stock's scale-up plan on B is not exactly one add"; }
    quiet stock_b apply -auto-approve -input=false -no-color || fail "stock's scale-up apply failed on B"
    U_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$U_PLAN" | tail -30; fail "the scale-up plan failed"; }
    log "  choudoufu's scale-up plan on A:"; plan_lines "$U_PLAN"
    grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$U_PLAN" || { printf '%s\n' "$U_PLAN" | tail -30; fail "the scale-up plan is not exactly one add"; }
    grep -qF "$SHARD1 will be created" <<< "$U_PLAN" || { plan_lines "$U_PLAN"; fail "the scale-up plan does not create $SHARD1"; }
    { APPLY_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)" && grep -qF "1 added, 0 changed, 0 destroyed" <<< "$APPLY_OUT"; } || { printf '%s\n' "$APPLY_OUT" | tail -30; fail "the scale-up apply did not create exactly one object"; }
    exists_a issuer shard-1 || fail "shard-1 does not exist after the scale-up"
    # A record the scale-down left behind would surface here, as a change
    # proposed for the instance it names.
    U_RE="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$U_RE" | tail -30; fail "the replan after the scale-up failed"; }
    grep -q "No changes." <<< "$U_RE" || { printf '%s\n' "$U_RE" | tail -30; fail "the replan after the scale-up is not empty: $(gauntlet_plan_line <<< "$U_RE"), [$(joined "$(planned "$U_RE")")]"; }
    gauntlet_stage day2_count pass "for_each over the render, scaled the way a chart user scales it: two shard Issuers in extraObjects rendered two custom resources and choudoufu created both through the failurePolicy: Fail webhook (cainjector already gone, on the CA it had injected), then replanned empty; at one entry it destroyed exactly shard-1, the object stock destroys ($(joined "$C_OBJS")), $C_ADDR_NOTE, with $SHARD0 untouched (both read with kubectl); $C_REC_NOTE; back at two it created exactly $SHARD1 and replanned empty; stock's plans for the same three edits on the oracle cluster have the identical shape (2 add, 1 destroy, 1 add). BREAK_COUNT=1 asserts the remaining shard was destroyed and correctly fails"
  fi
  { set_shards "$ADOPTED" 0 && set_shards "$ORACLE" 0; } || fail "could not withdraw the shards"
  quiet tofu_a apply -auto-approve -input=false -no-color || fail "could not withdraw the shards from A"
  quiet stock_b apply -auto-approve -input=false -no-color || fail "could not withdraw the shards from B"
  { exists_a issuer shard-0 || exists_a issuer shard-1; } && fail "a shard Issuer survives its withdrawal from A"
fi

# ── 9b. day2_replace: a create_before_destroy rename ─────────────────────
gauntlet_begin_stage day2_replace
log "=== 9b. day2_replace: a content-hashed ConfigMap renamed under create_before_destroy ==="
gauntlet_kind_day2_replace "$ADOPTED" "$ORACLE" "$NS"

# ── 10. day2_crash: an apply of two custom resources, killed after one ───
gauntlet_begin_stage day2_crash
log "=== 10. day2_crash: SIGTERM between the create of one Issuer and the create of the next ==="
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
exists_a issuer crash-first || { printf '%s\n' "$X_OUT" | tail -20; fail "crash-first does not exist after the interrupted apply - the kill landed before the create committed"; }
exists_a issuer crash-second && { printf '%s\n' "$X_OUT" | tail -20; fail "crash-second exists after the interrupted apply - the kill landed after both creates"; }
{ GET_OUT="$(kca get issuer -n "$NS" -l "tofu-estate=$ESTATE" -o name 2>/dev/null)" && grep -qE '(^|/)crash-first$' <<< "$GET_OUT"; } \
  || { printf '%s\n' "$GET_OUT"; fail "crash-first was created by the interrupted apply but does not come back under tofu-estate=$ESTATE"; }
X_ADDR_ANN="$(kca get issuer crash-first -n "$NS" -o jsonpath='{.metadata.annotations.choudoufu\.intentius\.io/tofu-address}' 2>&1)"
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
  kca label issuer crash-first -n "$NS" tofu-estate- >/dev/null 2>&1 || fail "BREAK_CRASH_UNBOUND: could not strip the label off crash-first"
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
  gauntlet_stage day2_crash pass "BREAK_CRASH_UNBOUND=1 control: with the tofu-estate label stripped off the Issuer the interrupted apply created, the recovery check correctly fails to hold ($R_LINE); the real check is skipped $CRASH_RENAME_DETAIL"
  kca delete issuer crash-first -n "$NS" >/dev/null 2>&1 || fail "BREAK_CRASH_UNBOUND: could not delete the unlabelled crash-first afterwards"
  quiet tofu_a apply -auto-approve -input=false -no-color || fail "BREAK_CRASH_UNBOUND: the apply after the cleanup failed"
else
  if ! recovered; then
    printf '%s\n' "$R_PLAN" | grep -E '^Plan:|^No changes|will be' | head -20
    gauntlet_stage day2_crash fail "the plan after a real interrupt between the creates of kubernetes_manifest.crash_first and .crash_second (both Issuers, a custom kind this estate's own render installed, behind the failurePolicy: Fail webhook) is not exactly the remainder: ${R_LINE:-no plan line} (exit $R_RC). crash-first exists carrying tofu-estate=$ESTATE and crash-second does not, both read with kubectl; stock from the same position plans exactly one add (crash_second). Records $X_RECORDS_BEFORE -> $X_RECORDS_AFTER $CRASH_RENAME_DETAIL"
  else
    R_APPLY="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)"; R_APPLY_RC=$?
    [ "$R_APPLY_RC" -eq 0 ] || { printf '%s\n' "$R_APPLY" | tail -20; fail "the recovery apply exited $R_APPLY_RC"; }
    grep -qF "Apply complete! Resources: 1 added, 0 changed, 0 destroyed" <<< "$R_APPLY" \
      || { printf '%s\n' "$R_APPLY" | tail -30; fail "the recovery apply did not add exactly the one remaining object"; }
    exists_a issuer crash-second || fail "crash-second does not exist after the recovery apply"
    exists_a issuer crash-first || fail "crash-first is gone after the recovery apply - the recovery replaced the object the crash created instead of binding it"
    R_REPLAN="$(tofu_a plan -input=false -no-color 2>&1)"; R_REPLAN_RC=$?
    [ "$R_REPLAN_RC" -eq 0 ] || { printf '%s\n' "$R_REPLAN" | tail -30; fail "the replan after the recovery exited $R_REPLAN_RC: $(grep -E '^Error' <<< "$R_REPLAN" | head -1)"; }
    grep -q "No changes." <<< "$R_REPLAN" || { printf '%s\n' "$R_REPLAN" | tail -30; fail "the replan after the recovery is not empty"; }
    C_AFTER="$(count_a)"
    [ "$C_AFTER" = "$((C_BEFORE + 2))" ] || fail "$C_AFTER labelled objects after the recovery, want $((C_BEFORE + 2)): $(labelled_a)"
    gauntlet_stage day2_crash pass "an apply creating two Issuers - a custom kind served by CRDs this estate's own render installed, admitted through its failurePolicy: Fail webhook - was interrupted by a real SIGTERM (exit $X_RC), delivered by the engine itself inside the -parallelism=1 walker the instant kubernetes_manifest.crash_first's create committed; crash_second's manifest reads crash_first's name, so the walker cannot have reached it. kubectl confirms crash-first exists carrying tofu-estate=$ESTATE and its address annotation, and crash-second does not; the interrupted apply recorded exactly the one object it created (records $X_RECORDS_BEFORE -> $X_RECORDS_AFTER, labels [$X_KEYS_L], annotations [$X_KEYS_A], #1211). The next plan proposed exactly the remainder ($R_LINE, crash_second created) and nothing for crash-first, matching stock from the same position on the oracle cluster; the recovery apply added one, the replan is empty and $C_AFTER objects carry the label, $C_BEFORE plus the pair. BREAK_CRASH=1 and BREAK_CRASH_UNBOUND=1 both correctly fail $CRASH_RENAME_DETAIL"
  fi
fi
gauntlet_end_stage

# ── 11. day2_teardown: destroy the adopted estate ────────────────────────
gauntlet_begin_stage day2_teardown
log "=== 11. day2_teardown: apply -destroy on the adopted estate, and stock's own destroy on B ==="
T_EXPECT="$(count_a)"
T_OUT="$(tofu_a apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$T_OUT" | tail -20; fail "apply -destroy failed"; }
grep -qF "Resources: 0 added, 0 changed, $T_EXPECT destroyed" <<< "$T_OUT" || { printf '%s\n' "$T_OUT" | tail -30; fail "apply -destroy did not remove exactly the $T_EXPECT remaining objects"; }
for _ in $(seq 1 60); do kca get namespace "$NS" >/dev/null 2>&1 || break; sleep 2; done
kca get namespace "$NS" >/dev/null 2>&1 && { kca get namespace "$NS" -o jsonpath='{.status}' 2>&1; echo; kca get all,issuers,certificates -n "$NS" 2>&1 | tail -20; fail "the $NS namespace still exists after the destroy"; }
[ "$(count_a)" = "0" ] || fail "$(count_a) object(s) still carry tofu-estate=$ESTATE after the destroy: $(labelled_a)"
CRDS_LEFT="$(kca get crd -o name 2>/dev/null | grep -c 'cert-manager.io')"
[ "$CRDS_LEFT" = "0" ] || fail "$CRDS_LEFT cert-manager CRD(s) survive the destroy"
KS_RBAC="$(kca get roles,rolebindings -n kube-system -o name 2>/dev/null | grep -c 'cert-manager')"
[ "$KS_RBAC" = "0" ] || fail "$KS_RBAC of the estate's Roles and RoleBindings survive in kube-system"
kca get namespace kube-system >/dev/null 2>&1 || fail "kube-system is gone"
# cert-manager's Leases in kube-system are the controllers' own, written at
# runtime with no owner reference, and nothing declares them, so no destroy
# removes them: stock's destroy on B leaves them too, checked below.
ks_leases() { "$1" get leases -n kube-system -o name 2>/dev/null | grep 'cert-manager' | sort | tr '\n' ' ' | sed -E 's/ $//'; }
KS_LEASES="$(ks_leases kca)"
O_EXPECT="$(managed_n stock_b)"
O_T="$(stock_b apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$O_T" | tail -10; fail "stock's destroy failed on B"; }
grep -qF "Resources: 0 added, 0 changed, $O_EXPECT destroyed" <<< "$O_T" || { printf '%s\n' "$O_T" | tail -10; fail "stock's destroy on B did not remove exactly the $O_EXPECT objects its state held"; }
O_KS_LEASES="$(ks_leases kcb)"
[ "$KS_LEASES" = "$O_KS_LEASES" ] || fail "the cert-manager Leases left in kube-system after the destroy differ from stock's: A '$KS_LEASES', B '$O_KS_LEASES'"
gauntlet_stage day2_teardown pass "apply -destroy removed exactly the $T_EXPECT remaining objects in one apply, the custom resources before the Deployments, the Deployments before the RBAC and webhook configurations, and those before the CRDs and the Namespace, by the blocks' depends_on; the $NS namespace is gone and with it every Secret cert-manager wrote, all $CRD_N cert-manager CRDs are gone, none of the estate's Roles or RoleBindings survive in kube-system while kube-system itself is untouched (cert-manager's own Leases, ${KS_LEASES:-none}, which nothing declares, are left exactly as stock's destroy leaves them on B), and no object of the estate's kinds carries tofu-estate=$ESTATE (kubectl, every namespace); stock's destroy on the oracle cluster removed exactly the $O_EXPECT its state held"

# ── 12. greenfield: the same shape, fresh, with a live block ─────────────
gauntlet_begin_stage greenfield
log "=== 12. greenfield: choudoufu applies the shape fresh on the now-empty cluster A ==="
write_root "$GREEN" live || fail "could not write the greenfield root"
green() { ( cd "$GREEN" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" "$TOFU" "$@" ); }
quiet green init -input=false -no-color || fail "greenfield init failed"

G_TARGETS=()
while IFS= read -r a; do [ -n "$a" ] && G_TARGETS+=("-target=$a"); done < <(gauntlet_pre_apply_targets "$ESTATE")
[ "${#G_TARGETS[@]}" = "4" ] || fail "the declared pre-apply list has ${#G_TARGETS[@]} addresses, want 4 (the Namespace and the crds, rest and late blocks)"
G_PRE="$(green apply -auto-approve -input=false -no-color "${G_TARGETS[@]}" 2>&1)"; G_PRE_RC=$?
if [ "$G_PRE_RC" -ne 0 ]; then
  printf '%s\n' "$G_PRE" | tail -40
  gauntlet_stage greenfield fail "choudoufu cannot perform the pre-apply its own estate declares: the same ${#G_TARGETS[@]} -target arguments stock accepted at cold deploy, against the same empty cluster, exited $G_PRE_RC; the first error it printed was \"$(gauntlet_first_error_line <<< "$G_PRE")\""
  quiet green apply -destroy -auto-approve -input=false -no-color
  GREENFIELD_SKIPPED=1
fi
if [ -z "${GREENFIELD_SKIPPED:-}" ]; then
grep -qF "Apply complete! Resources: $PRE_N added, 0 changed, 0 destroyed" <<< "$G_PRE" || { printf '%s\n' "$G_PRE" | tail -30; fail "the greenfield pre-apply did not add exactly the $PRE_N declared instances"; }
cert_manager_ready "$KCA" "cluster A (greenfield)" || fail "cert-manager never became ready on A after the greenfield pre-apply"
G_OUT="$(green apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$G_OUT" | tail -20; fail "greenfield apply failed"; }
grep -qF "Apply complete! Resources: $CUSTOM_N added, 0 changed, 0 destroyed" <<< "$G_OUT" || { printf '%s\n' "$G_OUT" | tail -30; fail "the greenfield main apply did not add exactly the $CUSTOM_N rendered custom resources"; }
[ ! -f "$GREEN/terraform.tfstate" ] || fail "a terraform.tfstate appeared after a live-block apply"
gauntlet_wait_until 120 "the greenfield Certificate example-com to be Ready on cluster A" -- cert_ready "$KCA" || fail "the greenfield Certificate is not Ready; stock's cold deploy produced a Ready one, so this is not the same estate"
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
  gauntlet_stage greenfield pass "BREAK=1 control: expecting $G_WANT labelled objects where the estate has $G_LABELLED makes the count assertion correctly fail; the estate applied ($CUSTOM_N added after the pre-apply, no terraform.tfstate) and replanned empty with and without the cache"
else
  [ "$G_LABELLED" = "$TOTAL_N" ] || fail "$G_LABELLED object(s) carry tofu-estate=$ESTATE after the greenfield apply, want $TOTAL_N: $(labelled_a)"
  gauntlet_stage greenfield pass "$TOTAL_N objects applied fresh with a live block and no terraform.tfstate - the render read by the data-read phase before the plan, the same declared pre-apply the cold deploy took (${#G_TARGETS[@]} addresses, $PRE_N instances), the webhook admitting a dry-run Issuer, then the $CUSTOM_N custom resources - every one labelled tofu-estate=$ESTATE (kubectl, 14 kinds) and the Certificate Ready=True from the self-signed Issuer, as stock's cold deploy left it; replanned empty with and without the cache; the record store held $G_RECORDS envelope(s) for $TOTAL_N instances, and with that whole store deleted the plan read \"$L_LINE\", nothing created, destroyed or replaced and no kubernetes_manifest updated${L_OTHER:+ (proposed and reconverged: $L_OTHER)}. BREAK=1 expects a deliberately wrong object count and the assertion correctly fails"
fi
# ── no_local_state (#1966): the record store and the cache both gone ────
# live/e2e/lib/gauntlet.sh's gauntlet_kind_no_local_state; its Break line
# strips the tofu-estate label off drift_reconverge's Issuer.
gauntlet_kind_no_local_state "$GREEN" issuer selfsigned "$NS"
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
    estate = "reference-k8s-helm-cert-manager-strict"
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
  grep -qF 'random_password.db will be created' <<< "$STRICT_OFF" || { printf '%s\n' "$STRICT_OFF" | tail -20; fail "BREAK_STRICT=1: the plan with secrets = \"store\" does not propose creating random_password.db"; }
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
log "reference-k8s-helm-cert-manager: done"
