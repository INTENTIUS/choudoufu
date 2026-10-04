#!/usr/bin/env bash
# corpus-cloud-platform-components: the kubernetes lane's StorageClass,
# PriorityClass and for_each-over-a-CRD estate (#1881, epic #1885), crossed
# on the kind substrate.
#
# The configuration is the Ministry of Justice Cloud Platform's own
# cluster-components root, ministryofjustice/cloud-platform-infrastructure
# at commit 6e1eca7be0d4224718a993e82508df8b7b19857d (MIT, pinned in
# live/corpus-manifest.json), path
# terraform/aws-accounts/cloud-platform-aws/vpc/eks/core/components, top-level
# resources only:
#
#   storage.tf       three kubernetes_storage_class (gp2-expand, io1-expand,
#                    gp3 as the default class) - the old, unversioned type,
#                    one of the four ratified rows no other estate declares;
#   rbac.tf          three kubernetes_priority_class (cluster-critical,
#                    node-critical, a global default), two
#                    kubernetes_cluster_role_binding and a
#                    kubernetes_service_account in kube-system;
#   manager-vpas.tf  kubernetes_manifest.vpa, a for_each over a static map
#                    of five VerticalPodAutoscalers - the lane's second
#                    custom-resource estate and the first kubernetes_manifest
#                    keyed by for_each (reference-k8s-cert-manager only
#                    uses count).
#
# Plus two files this crossing writes, both declared as the cold-deploy
# pre-apply (#1173) in live/gauntlet/estates.json:
#
#   root/vpa-crd.tf         the VPA project's own two CRDs, converted by
#                           convert.sh (URL, size, sha256, licence and tfk8s
#                           version recorded there);
#   root/vpa-namespaces.tf  the three namespaces the VPAs live in.
#
# 19 instances: 5 pre-applied, then 14. No cloud provider, no module.
#
# ── the deltas, each asserted so a moved pin fails loudly ───────────────
#
#   1. Pruned: every other file in the directory. main.tf (an S3 backend,
#      two terraform_remote_state reads and six aws data sources),
#      providers.tf (EKS exec credentials), versions.tf (replaced, see 5),
#      variables.tf, outputs.tf, ssm.tf, s3.tf and the ~15 files whose only
#      content is a github.com/ministryofjustice/* module call - none of
#      those repositories is in .corpus/_modules, and the module calls are
#      where the cluster's ingress controllers, Concourse and monitoring
#      come from. terraform.tfvars is git-crypt ciphertext and is never
#      copied. Only storage.tf, rbac.tf and manager-vpas.tf are read.
#   2. The VPA CRD and the three namespaces the pruned modules provided are
#      added as the declared pre-apply (see the two files above).
#   3. manager-vpas.tf's `is_manager_workspace = terraform.workspace ==
#      "manager"` becomes `true`: the root creates its VPAs only in the
#      manager workspace, and a crossing in the default workspace would
#      plan an empty for_each and measure nothing. Choosing the workspace
#      in the configuration rather than with `terraform workspace select`
#      keeps stock and choudoufu on the identical text.
#   4. storage.tf's kubectl_manifest.change_sc_default (line 56, the gp2
#      default flip, alekc/kubectl) leaves the estate root and moves to the
#      STOCK side: a two-block root of its own, the same kubectl_manifest
#      with the root's own alekc/kubectl requirement, applied by stock
#      terraform on both clusters after the main apply (its depends_on on
#      the gp3 class becomes that ordering, since the reference cannot
#      cross roots). choudoufu refuses the type - migrate measures the
#      refusal on the unpruned file first - and an estate that kept it
#      could not migrate at all; on the stock side the object exists on
#      the cluster exactly as the root makes it, outside the estate, and
#      day2_teardown checks it is still there, unlabelled, after the estate
#      is gone.
#   5. versions.tf is written here: hashicorp/kubernetes at the lane's pin
#      (live/oracle-versions.json, #1252) in place of the root's own 2.25.2,
#      `provider "kubernetes" {}` reading KUBE_CONFIG_PATH in place of the
#      EKS exec block, and the live block on choudoufu's side only.
#
# .corpus is read, never written. Two kind clusters, both created for the
# run: A holds the estate (stock cold-deploys it, choudoufu adopts it and
# runs every day-2 stage, tears it down, then applies the same shape fresh
# with a live block); B is the oracle, where stock applies the identical
# root and every day-2 change itself.
#
#   go run ./tools/gauntlet run corpus-cloud-platform-components
#   bash live/e2e/corpus-cloud-platform-components/run.sh
#
# Needs `just corpus-fetch` first, then kind, kubectl, terraform (the stock
# binary), Docker and python3 on PATH, and network for the providers.
#
# Env overrides:
#   TOFU_BIN       path to a prebuilt choudoufu binary; skips the go build.
#   BREAK          drift_reconverge, day2_rename and greenfield's negative
#                  controls: a second VPA deleted (the single-object
#                  assertion must fail); the ClusterRoleBinding's own
#                  metadata.name changed (must plan a destroy and a
#                  create); a StorageClass dropped from greenfield's
#                  expected inventory (the match must fail).
#   BREAK_PREAPPLY apply the whole root in ONE pass at cold deploy and
#                  require it to succeed; it must fail.
#   BREAK_REMOVE   keep the node-critical PriorityClass block; no destroy
#                  may be proposed.
#   BREAK_COUNT    assert the wrong VPA shard was destroyed on the
#                  scale-down.
#   BREAK_APPROVAL apply the saved plan after the world moved and expect
#                  success.
#   BREAK_REPLACE  the shared create_before_destroy control
#                  (live/e2e/lib/gauntlet.sh's gauntlet_kind_day2_replace).
#   BREAK_CRASH    after the real interrupt, assert nothing is proposed;
#                  must fail.
#   BREAK_CRASH_UNBOUND
#                  strip the tofu-estate label off the object the
#                  interrupted apply did create; the recovery check must
#                  fail.
#   BREAK_STRICT   turn secrets back to "store"; the refusal must vanish.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$ROOT/live/e2e/lib/gauntlet.sh"

# The shared provider plugin cache, and the cross-process lock real terraform
# needs in order to use it safely (#1300). live/e2e/lib/gauntlet.sh carries the
# measured reasons for both; this is the only place a script chooses either.
gauntlet_plugin_cache
ESTATE="corpus-cloud-platform-components"
PINNED="6e1eca7be0d4224718a993e82508df8b7b19857d"
SRC="$ROOT/.corpus/cloud-platform"
COMP="$SRC/terraform/aws-accounts/cloud-platform-aws/vpc/eks/core/components"
# The estate's kinds, for the label count. verticalpodautoscalers is only
# served once the CRD is installed; kubectl prints nothing for a kind the
# API server does not serve, which counts as zero, which is correct.
KINDS=(namespaces customresourcedefinitions storageclasses priorityclasses clusterrolebindings serviceaccounts verticalpodautoscalers)
PRE_N=5     # two CRDs and three namespaces, the declared pre-apply
MAIN_N=14   # 3 StorageClasses, 3 PriorityClasses, 2 ClusterRoleBindings, a ServiceAccount, 5 VPAs
TOTAL_N=$((PRE_N + MAIN_N))
# manager-vpas.tf's for_each keys, "<namespace>/<kind>/<name>". Never
# written here: vpa_keys evaluates the root's own for_each expression with
# `terraform console` once the stock root is written (delta 3 applied), so a
# pin that moves the map moves these with it, and a pin that changes their
# number fails against VPA_N. At 6e1eca7be0 they are, in console's order:
#   concourse/Deployment/concourse-web
#   concourse/StatefulSet/concourse-postgresql
#   concourse/StatefulSet/concourse-worker
#   ingress-controllers/Deployment/nginx-ingress-default-controller
#   monitoring/Deployment/thanos-compactor
VPA_N=5
VPA_KEYS=()
# The VPA drift_reconverge deletes, and the second one BREAK=1 deletes; both
# are asserted to be among the derived keys.
DRIFT_KEY="monitoring/Deployment/thanos-compactor"
BREAK_KEY="concourse/Deployment/concourse-web"
# The namespace the shared day2_replace and day2_crash blocks are written
# into: one of the three this estate creates.
NS="concourse"
WORK="$(mktemp -d)"
STOCK="$WORK/stock"; ADOPTED="$WORK/adopted"; ORACLE="$WORK/oracle"; GREEN="$WORK/green"; REFUSAL="$WORK/refusal"
SIDE_A="$WORK/stock-side-a"; SIDE_B="$WORK/stock-side-b"
KCA="$WORK/a.kubeconfig"; KCB="$WORK/b.kubeconfig"
CLUSTER_A="${GAUNTLET_KIND_PREFIX:-chdf}-cpc-a-$$"; CLUSTER_B="${GAUNTLET_KIND_PREFIX:-chdf}-cpc-b-$$"  # GAUNTLET_KIND_PREFIX: lets concurrent workers name their own clusters
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
[ -d "$COMP" ] || fail "$COMP is missing - run \`just corpus-fetch\` first"
PIN="$(git -C "$SRC" rev-parse HEAD 2>/dev/null)"
[ "$PIN" = "$PINNED" ] || fail "the fetched cloud-platform-infrastructure is at ${PIN:-nothing}, not the pinned $PINNED - run \`just corpus-fetch\`"
for f in storage.tf rbac.tf manager-vpas.tf versions.tf; do
  [ -f "$COMP/$f" ] || fail "$COMP/$f is missing - the corpus pin has moved"
done
[ -f "$HERE/root/vpa-crd.tf" ] || fail "$HERE/root/vpa-crd.tf is missing; run convert.sh"
[ -f "$HERE/root/vpa-namespaces.tf" ] || fail "$HERE/root/vpa-namespaces.tf is missing"

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
# whatever $TOFU came from, and unconditionally, since the real check and
# the Break controls all need it; identical to $TOFU everywhere else,
# because the hook is a no-op unless the variable names an applied address.
mkdir -p "$WORK/bin"
TOFU_CRASH="$WORK/bin/choudoufu-e2e"
( cd "$ROOT" && env -u PWD go build -ldflags="-X 'main.e2eTestingFeatures=yes'" -o "$TOFU_CRASH" ./cmd/choudoufu ) \
  || fail "go build -ldflags e2eTestingFeatures ./cmd/choudoufu failed"
log "  built $TOFU_CRASH (e2eTestingFeatures=yes, for day2_crash's interrupt)"

# ── the root, copied out of .corpus with its deltas ──────────────────────
# The hashicorp/kubernetes requirement is live/oracle-versions.json's
# kubernetes_provider_version, read once behind one fail (#1252, delta 5).
K8S_REQUIRED_PROVIDER="$(gauntlet_kubernetes_required_provider)" \
  || fail "could not read the hashicorp/kubernetes pin from live/oracle-versions.json"

# The root's own alekc/kubectl requirement, read out of its versions.tf
# rather than restated here, for the two places that still need that
# provider: migrate's refusal measurement and the stock side (delta 4).
KUBECTL_REQUIRED_PROVIDER="$(python3 - "$COMP/versions.tf" <<'PY'
import re, sys
s = open(sys.argv[1]).read()
m = re.search(r'\n(\s*)kubectl\s*=\s*\{\s*source\s*=\s*"([^"]+)"\s*version\s*=\s*"([^"]+)"\s*\}', s)
assert m and m.group(2) == "alekc/kubectl", "versions.tf has no alekc/kubectl requirement in the shape this delta reads - the corpus pin has moved"
print('    kubectl = {\n      source  = "%s"\n      version = "%s"\n    }' % (m.group(2), m.group(3)))
PY
)" || fail "could not read the root's own alekc/kubectl requirement from $COMP/versions.tf"

# gp2_flip_block prints storage.tf's kubectl_manifest block (delta 4) as
# the stock side applies it: the block verbatim, minus the one depends_on
# line whose target lives in the other root. cut_gp2_flip <file> removes
# the block and the comment above it from a copy of storage.tf. Both assert
# the block is where #1881 measured it, at line 56, and is the file's last.
gp2_flip_block() {
  python3 - "$COMP/storage.tf" <<'PY'
import sys
lines = open(sys.argv[1]).read().split("\n")
start = 55  # line 56, zero-based
assert lines[start] == 'resource "kubectl_manifest" "change_sc_default" {', "storage.tf line 56 is not the kubectl_manifest block - the corpus pin has moved"
block = "\n".join(lines[start:]).rstrip("\n")
assert block.endswith("YAML\n}"), "the kubectl_manifest block is not the last thing in storage.tf"
dep = "  depends_on = [kubernetes_storage_class.storageclass_gp3]\n"
assert dep in block + "\n", "the kubectl_manifest block has no depends_on on the gp3 class - the corpus pin has moved"
print((block + "\n").replace(dep, "", 1), end="")
PY
}
cut_gp2_flip() {
  python3 - "$1" <<'PY'
import sys
p = sys.argv[1]; s = open(p).read()
marker = "# remvove default from GP2\nresource \"kubectl_manifest\" \"change_sc_default\" {"
i = s.find(marker)
assert i >= 0, "storage.tf does not carry the gp2 flip where #1881 measured it - the corpus pin has moved"
out = s[:i].rstrip("\n") + "\n"
assert "kubectl" not in out, "a kubectl reference survived the cut"
open(p, "w").write(out)
PY
}

versions_block() { # $1 = live | stock | refusal
  cat <<EOF
terraform {
  required_providers {
$K8S_REQUIRED_PROVIDER
EOF
  if [ "$1" = "refusal" ]; then printf '%s\n' "$KUBECTL_REQUIRED_PROVIDER"; fi
  printf '  }\n'
  if [ "$1" = "live" ] || [ "$1" = "refusal" ]; then cat <<EOF
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
  if [ "$1" = "refusal" ]; then printf '\nprovider "kubectl" {}\n'; fi
}

# write_root <dir> <live|stock|refusal>: the three corpus files and the two
# committed ones, with deltas 3, 4 and 5. "refusal" keeps the kubectl_manifest
# block (and its provider) so migrate can measure what choudoufu says about
# the unpruned file; nothing else is ever applied from that mode.
write_root() {
  local dst="$1" mode="$2" f
  mkdir -p "$dst" || return 1
  for f in storage.tf rbac.tf manager-vpas.tf; do
    cp "$COMP/$f" "$dst/$f" || return 1
  done
  cp "$HERE/root/vpa-crd.tf" "$HERE/root/vpa-namespaces.tf" "$dst/" || return 1
  python3 - "$dst/manager-vpas.tf" <<'PY' || return 1
import sys
p = sys.argv[1]; s = open(p).read()
old = 'is_manager_workspace = terraform.workspace == "manager"'
assert s.count(old) == 1, "delta 3 did not match manager-vpas.tf - the corpus pin has moved"
open(p, "w").write(s.replace(old, 'is_manager_workspace = true # delta 3: the manager workspace, where this root creates its VPAs', 1))
PY
  if [ "$mode" != "refusal" ]; then cut_gp2_flip "$dst/storage.tf" || return 1; fi
  versions_block "$mode" > "$dst/versions.tf"
}

# write_stock_side <dir> <kubeconfig>: delta 4's stock-side root.
write_stock_side() {
  mkdir -p "$1" || return 1
  {
    printf 'terraform {\n  required_providers {\n%s\n  }\n}\n\n' "$KUBECTL_REQUIRED_PROVIDER"
    printf 'provider "kubectl" {\n  config_path = "%s"\n}\n\n' "$2"
    printf '# storage.tf line 56 of the pinned components root, moved here by\n# live/e2e/%s/run.sh (delta 4).\n' "$ESTATE"
    gp2_flip_block
  } > "$1/main.tf"
}

# ── cluster helpers ──────────────────────────────────────────────────────
kca() { kubectl --kubeconfig "$KCA" "$@"; }
kcb() { kubectl --kubeconfig "$KCB" "$@"; }
stock_a() { ( cd "$STOCK"  && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" terraform "$@" ); }
stock_b() { ( cd "$ORACLE" && KUBECONFIG="$KCB" KUBE_CONFIG_PATH="$KCB" terraform "$@" ); }
tofu_a() { ( cd "$ADOPTED" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" "$TOFU" "$@" ); }
green() { ( cd "$GREEN" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" "$TOFU" "$@" ); }
count_a() { KUBECONFIG="$KCA" gauntlet_kind_count "$ESTATE" "${KINDS[@]}"; }
exists_a() { kca get "$1" "$2" -n "$NS" >/dev/null 2>&1; }
plan_line() { gauntlet_plan_line <<< "$1"; }

# vpa_served <kubeconfig>: the API server lists verticalpodautoscalers under
# autoscaling.k8s.io/v1. "The CRD object exists" is not the same fact as
# "discovery serves the kind", and kubernetes_manifest plans against
# discovery, so this is what the wait after a pre-apply asks.
# The answer is captured before it is read, so an early-exiting grep cannot
# turn a served kind into a SIGPIPE failure under pipefail.
vpa_served() {
  local out
  out="$(kubectl --kubeconfig "$1" get --raw /apis/autoscaling.k8s.io/v1 2>/dev/null)" || return 1
  grep -q '"verticalpodautoscalers"' <<< "$out"
}

# vpa_addr <key>: the plan and report spelling of one for_each instance.
vpa_addr() { printf 'kubernetes_manifest.vpa["%s"]' "$1"; }

# vpa_keys <root dir>: the keys of kubernetes_manifest.vpa's for_each, one
# per line, evaluated by `terraform console` from the root's own locals and
# its own for_each expression. Both are lifted verbatim from manager-vpas.tf
# as written in <root dir>, delta 3 included, into a scratch dir with no
# provider, so neither init nor a cluster is involved.
vpa_keys() {
  local scratch out
  scratch="$(mktemp -d "$WORK/vpa-keys.XXXXXX")" || return 1
  python3 - "$1/manager-vpas.tf" "$scratch" <<'PY' || return 1
import re, sys
s = open(sys.argv[1]).read()
i = s.find("\nlocals {")
j = s.find('\nresource "kubernetes_manifest" "vpa" {')
assert 0 <= i < j, "manager-vpas.tf has no locals block before kubernetes_manifest.vpa - the corpus pin has moved"
m = re.search(r'^  for_each = (.+)$', s[j:], re.M)
assert m, "kubernetes_manifest.vpa has no one-line for_each - the corpus pin has moved"
open(sys.argv[2] + "/locals.tf", "w").write(s[i:j] + "\n")
open(sys.argv[2] + "/vpa-keys.expr", "w").write("jsonencode(keys(%s))\n" % m.group(1))
PY
  out="$(cd "$scratch" && terraform console -no-color < vpa-keys.expr 2>&1)" || { printf '%s\n' "$out" >&2; return 1; }
  python3 -c 'import json, sys; [print(k) for k in json.loads(json.loads(sys.stdin.read()))]' <<< "$out"
}

# inventory <kubeconfig> [kind/name to drop]: the estate's objects on one
# cluster, normalised to what the configuration declares - never labels,
# annotations other than the default-class one the root sets, or anything
# the server set - so stock's cold deploy and choudoufu's greenfield apply
# compare object by object. The stock-side gp2 class is not the estate's and
# is not read.
inventory() {
  CFG="$1" DROP="${2:-}" python3 - <<'PY'
import json, os, subprocess
cfg = os.environ["CFG"]; drop = os.environ.get("DROP", "")
def get(args):
    out = subprocess.run(["kubectl", "--kubeconfig", cfg, "get"] + args + ["-o", "json"], capture_output=True, text=True)
    return json.loads(out.stdout) if out.returncode == 0 else None
inv = {}
for name in ("gp2-expand", "io1-expand", "gp3"):
    o = get(["storageclass", name])
    inv["storageclass/" + name] = None if o is None else {
        "provisioner": o.get("provisioner"), "parameters": o.get("parameters"),
        "reclaimPolicy": o.get("reclaimPolicy"), "allowVolumeExpansion": o.get("allowVolumeExpansion"),
        "volumeBindingMode": o.get("volumeBindingMode"),
        "default": (o["metadata"].get("annotations") or {}).get("storageclass.kubernetes.io/is-default-class")}
for name in ("cluster-critical", "node-critical", "default"):
    o = get(["priorityclass", name])
    inv["priorityclass/" + name] = None if o is None else {
        "value": o.get("value"), "globalDefault": o.get("globalDefault", False),
        "description": o.get("description"), "preemptionPolicy": o.get("preemptionPolicy")}
for name in ("webops-cluster-admin", "concourse-build-environments"):
    o = get(["clusterrolebinding", name])
    inv["clusterrolebinding/" + name] = None if o is None else {"roleRef": o.get("roleRef"), "subjects": o.get("subjects")}
inv["serviceaccount/kube-system/concourse-build-environments"] = {
    "exists": get(["serviceaccount", "concourse-build-environments", "-n", "kube-system"]) is not None}
for ns in ("ingress-controllers", "concourse", "monitoring"):
    inv["namespace/" + ns] = {"exists": get(["namespace", ns]) is not None}
for crd in ("verticalpodautoscalers.autoscaling.k8s.io", "verticalpodautoscalercheckpoints.autoscaling.k8s.io"):
    o = get(["crd", crd])
    inv["crd/" + crd] = None if o is None else {
        "group": o["spec"].get("group"), "kind": o["spec"]["names"].get("kind"), "scope": o["spec"].get("scope"),
        "versions": [[v.get("name"), v.get("served"), v.get("storage")] for v in o["spec"].get("versions", [])]}
vpas = get(["verticalpodautoscalers", "-A"]) or {}
for o in vpas.get("items", []):
    m = o["metadata"]
    inv["vpa/%s/%s" % (m["namespace"], m["name"])] = {
        "targetRef": o["spec"].get("targetRef"), "updatePolicy": o["spec"].get("updatePolicy")}
if drop:
    inv.pop(drop, None)
print(json.dumps(inv, sort_keys=True, indent=1))
PY
}

# ── 1. cold_deploy: the control, the declared pre-apply, then the rest ───
gauntlet_begin_stage cold_deploy
log "=== 1. cold_deploy: two kind clusters; the un-targeted plan must fail, then the declared pre-apply on both sides ==="
write_root "$STOCK" stock  || fail "could not write the stock root on A"
write_root "$ORACLE" stock || fail "could not write the oracle root on B"
KEYS_OUT="$(vpa_keys "$STOCK")" || fail "could not evaluate kubernetes_manifest.vpa's for_each keys from $STOCK/manager-vpas.tf with terraform console"
while IFS= read -r k; do [ -n "$k" ] && VPA_KEYS+=("$k"); done <<< "$KEYS_OUT"
[ "${#VPA_KEYS[@]}" = "$VPA_N" ] || { printf '%s\n' "$KEYS_OUT"; fail "manager-vpas.tf's for_each evaluates to ${#VPA_KEYS[@]} key(s), want $VPA_N - the corpus pin or delta 3 has moved, and MAIN_N with it"; }
for want in "$DRIFT_KEY" "$BREAK_KEY"; do
  grep -qxF "$want" <<< "$KEYS_OUT" || { printf '%s\n' "$KEYS_OUT"; fail "$want is not one of manager-vpas.tf's for_each keys - the corpus pin has moved"; }
done
log "  kubernetes_manifest.vpa's for_each keys, from terraform console: ${VPA_KEYS[*]}"
gauntlet_kind_up "$CLUSTER_A" "$KCA" || fail "kind cluster A ($CLUSTER_A) did not come up"
gauntlet_kind_up "$CLUSTER_B" "$KCB" || fail "kind cluster B ($CLUSTER_B) did not come up"
export KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA"
K8S_VER="$(kca version 2>/dev/null | gauntlet_k8s_server_version)"
log "  cluster A: $CLUSTER_A (kubernetes $K8S_VER); cluster B: $CLUSTER_B"
( cd "$STOCK" && gauntlet_locked_init terraform init -input=false -no-color >/dev/null 2>&1 ) || fail "stock init failed on A"
( cd "$ORACLE" && gauntlet_locked_init terraform init -input=false -no-color >/dev/null 2>&1 ) || fail "stock init failed on B"

# The control: without the pre-apply the root cannot be planned at all.
CTRL="$(stock_a plan -input=false -no-color 2>&1)"; CTRL_RC=$?
if [ "${BREAK_PREAPPLY:-}" = "1" ]; then
  [ "$CTRL_RC" -eq 0 ] && fail "BREAK_PREAPPLY=1: the un-targeted plan succeeded, so the declared pre-apply is not load-bearing"
  log "  BREAK_PREAPPLY=1: caught - the one-pass plan exited $CTRL_RC"
  gauntlet_stage cold_deploy pass "BREAK_PREAPPLY=1 control: planning the whole root in one pass exits $CTRL_RC before creating anything ($(gauntlet_first_error_line <<< "$CTRL")), so 'one apply is enough' correctly fails; the real two-apply crossing is skipped, and so is every later stage, which would have nothing deployed to measure"
  gauntlet_end
  exit 0
fi
[ "$CTRL_RC" -ne 0 ] || fail "the un-targeted plan SUCCEEDED against a cluster with no VPA CRD; the plan-time constraint the declared pre-apply exists for is gone (delete the pre-apply, or find out what changed)"
grep -qF "CRD may not be installed" <<< "$CTRL" || { printf '%s\n' "$CTRL" | tail -20; fail "the un-targeted plan failed for some reason other than the missing VPA CRD: $(gauntlet_first_error_line <<< "$CTRL")"; }
CTRL_LINE="$(gauntlet_first_error_line <<< "$CTRL")"
log "  control: the one-pass plan fails as documented - $CTRL_LINE"

pre_apply_estate() { stock_a apply -auto-approve -input=false -no-color "$@" > "$WORK/pre-apply.a.log" 2>&1; }
pre_apply_oracle() { stock_b apply -auto-approve -input=false -no-color "$@" > "$WORK/pre-apply.b.log" 2>&1; }
gauntlet_pre_apply "$ESTATE" estate:pre_apply_estate oracle:pre_apply_oracle \
  || { tail -20 "$WORK/pre-apply.a.log" "$WORK/pre-apply.b.log" 2>/dev/null; fail "the declared pre-apply failed"; }
PRE_NOTE="$(gauntlet_pre_apply_note)" || fail "the pre-apply ran but produced no note to put in the verdict"
# state list is captured, then read: piped straight into an early-exiting
# grep -q under pipefail it dies of SIGPIPE whenever grep matches before
# terraform has written its last line, which a loaded host makes likely.
PRE_STATE="$(stock_a state list 2>"$WORK/state-list.err")" || { cat "$WORK/state-list.err"; fail "stock state list failed on A after the pre-apply"; }
[ "$(grep -c . <<< "$PRE_STATE")" = "$PRE_N" ] || { printf '%s\n' "$PRE_STATE"; fail "the pre-apply on A left $(grep -c . <<< "$PRE_STATE") instances in state, want $PRE_N"; }
gauntlet_wait_until 120 "cluster A to serve verticalpodautoscalers at autoscaling.k8s.io/v1" -- vpa_served "$KCA" || fail "cluster A never served the VPA kind after the pre-apply"
gauntlet_wait_until 120 "cluster B to serve verticalpodautoscalers at autoscaling.k8s.io/v1" -- vpa_served "$KCB" || fail "cluster B never served the VPA kind after the pre-apply"

COLD_OUT="$(stock_a apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$COLD_OUT" | tail -20; fail "stock's main apply failed on A"; }
grep -qF "Apply complete! Resources: $MAIN_N added, 0 changed, 0 destroyed" <<< "$COLD_OUT" || { printf '%s\n' "$COLD_OUT" | tail -5; fail "stock's main apply on A did not add exactly $MAIN_N objects"; }
O_COLD="$(stock_b apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$O_COLD" | tail -20; fail "stock's main apply failed on B"; }
grep -qF "Apply complete! Resources: $MAIN_N added, 0 changed, 0 destroyed" <<< "$O_COLD" || { printf '%s\n' "$O_COLD" | tail -5; fail "stock's main apply on B did not add exactly $MAIN_N objects"; }
[ -f "$STOCK/terraform.tfstate" ] || fail "stock left no terraform.tfstate on A"
STOCK_STATE="$(stock_a state list 2>"$WORK/state-list.err")" || { cat "$WORK/state-list.err"; fail "stock state list failed on A after the main apply"; }
STOCK_N="$(grep -c . <<< "$STOCK_STATE")"
[ "$STOCK_N" = "$TOTAL_N" ] || { printf '%s\n' "$STOCK_STATE"; fail "stock's state holds $STOCK_N instances, want $TOTAL_N"; }
for key in "${VPA_KEYS[@]}"; do
  grep -qxF "$(vpa_addr "$key")" <<< "$STOCK_STATE" || { printf '%s\n' "$STOCK_STATE"; fail "stock's state has no $(vpa_addr "$key")"; }
done
UNMARKED="$(count_a)"
[ "$UNMARKED" = "0" ] || fail "$UNMARKED object(s) already carry tofu-estate=$ESTATE after a plain stock apply"

# Delta 4's stock side, on both clusters, after the main apply (which is
# the ordering the block's own depends_on asked for).
write_stock_side "$SIDE_A" "$KCA" || fail "could not write the stock-side root for A"
write_stock_side "$SIDE_B" "$KCB" || fail "could not write the stock-side root for B"
for side in "$SIDE_A" "$SIDE_B"; do
  ( cd "$side" && gauntlet_locked_init terraform init -input=false -no-color >/dev/null 2>&1 ) || fail "stock init of the stock-side root $side failed"
  SIDE_OUT="$(cd "$side" && terraform apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$SIDE_OUT" | tail -20; fail "stock's apply of the gp2 flip failed ($side)"; }
  grep -qF "Apply complete! Resources: 1 added, 0 changed, 0 destroyed" <<< "$SIDE_OUT" || { printf '%s\n' "$SIDE_OUT" | tail -5; fail "stock's apply of the gp2 flip did not add exactly one object ($side)"; }
done
GP2_DEFAULT="$(kca get storageclass gp2 -o jsonpath='{.metadata.annotations.storageclass\.kubernetes\.io/is-default-class}' 2>/dev/null)"
[ "$GP2_DEFAULT" = "false" ] || fail "the stock-side gp2 StorageClass reads is-default-class=${GP2_DEFAULT:-nothing} on A, want false"
inventory "$KCA" > "$WORK/inventory.stock.json" || fail "could not read the cold-deployed inventory on A"
gauntlet_stage cold_deploy pass "$TOTAL_N objects from plain terraform against kind $K8S_VER: the pinned components root's three StorageClasses (the unversioned kubernetes_storage_class), three PriorityClasses, two ClusterRoleBindings, a ServiceAccount in kube-system and kubernetes_manifest.vpa's five VerticalPodAutoscalers keyed by for_each over a static map (each key read back from stock's own state), plus the VPA project's two CRDs and the three namespaces the VPAs live in; a real terraform.tfstate with $TOTAL_N instances and zero tofu-estate labels read back with kubectl; the identical root cold-deployed by stock on a second cluster as every later stage's oracle. Two applies, and the first is declared: $PRE_NOTE. Control, run first on this same cluster: the un-targeted one-pass plan exits $CTRL_RC before creating anything - \"$CTRL_LINE\" - so the pre-apply is load-bearing. storage.tf line 56's kubectl_manifest (the gp2 default flip) was applied by stock on both clusters from a root of its own (delta 4) and reads is-default-class=false, outside the estate. BREAK_PREAPPLY=1 requires the one-pass plan to succeed and correctly fails"

# ── 2. migrate ────────────────────────────────────────────────────────────
gauntlet_begin_stage migrate
log "=== 2. migrate: what choudoufu says about the unpruned storage.tf, then live-import against the stock state file ==="
# Delta 4's reason, measured rather than asserted from memory: the same
# root with storage.tf intact - the kubectl_manifest block and its provider
# kept - is refused, once, at that block.
write_root "$REFUSAL" refusal || fail "could not write the unpruned root for the refusal measurement"
R_INIT="$(cd "$REFUSAL" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" "$TOFU" init -input=false -no-color 2>&1)" || { printf '%s\n' "$R_INIT" | tail -20; fail "choudoufu init of the unpruned root failed"; }
R_OUT="$(cd "$REFUSAL" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" "$TOFU" plan -input=false -no-color 2>&1)"; R_RC=$?
R_ERRORS="$(grep -cE '^[[:space:]]*(│[[:space:]]*)?Error: ' <<< "$R_OUT" || true)"
R_FIRST="$(gauntlet_first_error_line <<< "$R_OUT")"
if [ "$R_RC" -eq 0 ] || [ "$R_ERRORS" != "1" ] || ! grep -qF "Resource type is outside the live-markers subset" <<< "$R_OUT" \
  || ! grep -qF 'storage.tf line 56' <<< "$R_OUT" || ! grep -qF 'kubectl_manifest' <<< "$R_OUT"; then
  printf '%s\n' "$R_OUT" | tail -30
  fail "the unpruned root's plan is not the one refusal delta 4 is written against (exit $R_RC, $R_ERRORS Error: line(s), first: \"${R_FIRST:-none}\"): the kubectl_manifest at storage.tf line 56 was expected to be refused as \"Resource type is outside the live-markers subset\" and nothing else - if choudoufu now admits it, delta 4 is moving an object the estate could keep"
fi
log "  unpruned storage.tf: exit $R_RC, one refusal - $R_FIRST, at storage.tf line 56"

write_root "$ADOPTED" live || fail "could not write the adopted root"
A_INIT="$(tofu_a init -input=false -no-color 2>&1)" || { printf '%s\n' "$A_INIT" | tail -20; fail "adopted init failed"; }
IMPORT_OUT="$(tofu_a live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -no-color 2>&1)" || { printf '%s\n' "$IMPORT_OUT" | tail -20; fail "live-import (dry run) failed"; }
ELIGIBLE_LINE="$(grep -E 'resource instance\(s\) are eligible for stamping' <<< "$IMPORT_OUT" | head -1)"
log "  dry run: ${ELIGIBLE_LINE:-no eligibility line}"
APPROVE_OUT="$(tofu_a live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -approve -no-color 2>&1)" || { printf '%s\n' "$APPROVE_OUT" | tail -20; fail "live-import -approve failed"; }
SUMMARY_LINE="$(grep -E 'resource\(s\) newly stamped' <<< "$APPROVE_OUT" | head -1 | sed 's/\.$//')"
log "  approve: ${SUMMARY_LINE:-no summary line}"
MISSING_KEYS=""
for key in "${VPA_KEYS[@]}"; do
  grep -qF "  $(vpa_addr "$key") " <<< "$APPROVE_OUT" || MISSING_KEYS="$MISSING_KEYS $(vpa_addr "$key")"
done
if grep -qF "$TOTAL_N resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 0 skipped." <<< "$APPROVE_OUT"; then
  LABELLED="$(count_a)"
  if [ "$LABELLED" != "$TOTAL_N" ]; then
    printf '%s\n' "$APPROVE_OUT" | tail -40
    gauntlet_stage migrate fail "live-import reported $TOTAL_N stamped but $LABELLED object(s) carry tofu-estate=$ESTATE on the cluster"
  elif [ -n "$MISSING_KEYS" ]; then
    printf '%s\n' "$APPROVE_OUT" | tail -40
    gauntlet_stage migrate fail "live-import stamped $TOTAL_N, but the report does not name every for_each instance of kubernetes_manifest.vpa by its key; missing:$MISSING_KEYS"
  else
    gauntlet_stage migrate pass "$TOTAL_N of $TOTAL_N stamped, 0 skipped, from the stock state file; every object carries tofu-estate=$ESTATE, counted back with kubectl across the estate's seven kinds; the five for_each instances of kubernetes_manifest.vpa are each named in the stamped report by their static-map key (\"<namespace>/<kind>/<name>\"). Measured first, on the same cluster: the root with storage.tf unpruned plans to exactly one refusal, \"$R_FIRST\" at storage.tf line 56 (the kubectl_manifest gp2 flip), which is why delta 4 moves that one block to the stock side. The eligibility line was: ${ELIGIBLE_LINE:-none}"
  fi
else
  printf '%s\n' "$APPROVE_OUT" | tail -40
  gauntlet_stage migrate fail "live-import -approve did not stamp all $TOTAL_N cleanly: ${SUMMARY_LINE:-no summary line}"
fi

# ── 3. test_plan ──────────────────────────────────────────────────────────
gauntlet_begin_stage test_plan
log "=== 3. test_plan: choudoufu plan with no state file, identities read with kubectl ==="
PLAN_OUT="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$PLAN_OUT" | tail -20; fail "the post-migration plan failed"; }
IDS_MISSING=""
# id_is <expected NAMESPACE/NAME or NAME> <kubectl get args...>: the
# identity the configuration declares, compared by value with what kubectl
# reports for the same object.
id_is() {
  local want="$1" got; shift
  got="$(kca get "$@" -o jsonpath='{.metadata.namespace}{"/"}{.metadata.name}' 2>/dev/null)"
  got="${got#/}"
  [ "$got" = "$want" ] || IDS_MISSING="$IDS_MISSING $want(got:${got:-nothing})"
}
id_is gp3 storageclass gp3
id_is io1-expand storageclass io1-expand
id_is default priorityclass default
id_is webops-cluster-admin clusterrolebinding webops-cluster-admin
id_is kube-system/concourse-build-environments serviceaccount concourse-build-environments -n kube-system
id_is verticalpodautoscalers.autoscaling.k8s.io customresourcedefinition verticalpodautoscalers.autoscaling.k8s.io
id_is monitoring namespace monitoring
for key in "${VPA_KEYS[@]}"; do
  IFS=/ read -r vns vkind vname <<< "$key"
  id_is "$vns/$vname" verticalpodautoscaler "$vname" -n "$vns"
  TK="$(kca get verticalpodautoscaler "$vname" -n "$vns" -o jsonpath='{.spec.targetRef.kind}' 2>/dev/null)"
  [ "$TK" = "$vkind" ] || IDS_MISSING="$IDS_MISSING $key(targetRef.kind:${TK:-nothing})"
done
if grep -q "No changes." <<< "$PLAN_OUT" && [ -z "$IDS_MISSING" ]; then
  gauntlet_stage test_plan pass "the plan with no state file is empty; all $TOTAL_N objects bind by namespace and name, and twelve identities compared by value with kubectl: a StorageClass of each provisioner (gp3, io1-expand), the global-default PriorityClass, a ClusterRoleBinding, the kube-system ServiceAccount (kube-system/concourse-build-environments), the VPA CRD, a namespace, and all five for_each instances of kubernetes_manifest.vpa, each at the NAMESPACE/NAME its static-map key spells and pointing at the target kind the key names"
else
  printf '%s\n' "$PLAN_OUT" | tail -20
  gauntlet_stage test_plan fail "the plan with no state file is not empty ($(plan_line "$PLAN_OUT")) or an identity did not match by value (mismatched:${IDS_MISSING:- none})"
  ADOPT_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$ADOPT_OUT" | tail -20; fail "the converging apply failed"; }
  grep -q "No changes." <<< "$(tofu_a plan -input=false -no-color 2>&1)" || fail "the replan after the converging apply is not empty"
fi

# ── 4. test_apply ─────────────────────────────────────────────────────────
gauntlet_begin_stage test_apply
log "=== 4. test_apply: apply the empty plan; the labelled-object count must not move ==="
BEFORE_N="$(count_a)"
NOOP_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$NOOP_OUT" | tail -20; fail "the no-op apply failed"; }
grep -qF "Apply complete! Resources: 0 added, 0 changed, 0 destroyed" <<< "$NOOP_OUT" || { printf '%s\n' "$NOOP_OUT" | tail -5; fail "the no-op apply changed something"; }
AFTER_N="$(count_a)"
[ "$BEFORE_N" = "$AFTER_N" ] || fail "labelled-object count moved across a no-op apply: $BEFORE_N -> $AFTER_N"
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed); objects carrying tofu-estate=$ESTATE unchanged at $BEFORE_N across the estate's seven kinds, counted with kubectl"

# ── 5. drift_reconverge ───────────────────────────────────────────────────
gauntlet_begin_stage drift_reconverge
log "=== 5. drift_reconverge: one for_each VPA deleted out of band on A and B; stock's plan on B is the oracle ==="
# A delete rather than a patch, for the reason reference-k8s-cert-manager
# measured: kubernetes_manifest applies server-side, so a kubectl patch makes
# kubectl a field manager and the reconverging apply fails with a
# field-manager conflict on BOTH sides, which measures SSA ownership rather
# than drift. A delete involves no field manager.
DRIFT_ADDR="$(vpa_addr "$DRIFT_KEY")"
IFS=/ read -r DRIFT_NS _ DRIFT_NAME <<< "$DRIFT_KEY"
IFS=/ read -r BREAK_NS _ BREAK_NAME <<< "$BREAK_KEY"
kca delete verticalpodautoscaler "$DRIFT_NAME" -n "$DRIFT_NS" >/dev/null || fail "could not delete the $DRIFT_NAME VPA on A"
kcb delete verticalpodautoscaler "$DRIFT_NAME" -n "$DRIFT_NS" >/dev/null || fail "could not delete the $DRIFT_NAME VPA on B"
if [ "${BREAK:-}" = "1" ]; then
  kca delete verticalpodautoscaler "$BREAK_NAME" -n "$BREAK_NS" >/dev/null || fail "BREAK: could not delete a second VPA ($BREAK_KEY) on A"
fi
ORACLE_PLAN="$(stock_b plan -detailed-exitcode -input=false -no-color 2>&1)"; ORACLE_RC=$?
[ "$ORACLE_RC" -eq 2 ] || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B after the delete exited $ORACLE_RC, want 2"; }
grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$ORACLE_PLAN" || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B does not propose exactly one create"; }
grep -qF "$DRIFT_ADDR" <<< "$ORACLE_PLAN" || { printf '%s\n' "$ORACLE_PLAN" | tail -20; fail "stock's plan on B does not name $DRIFT_ADDR"; }
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock could not reconverge B"
DRIFT_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$DRIFT_PLAN" | tail -20; fail "the plan after the delete failed"; }
if [ "${BREAK:-}" = "1" ]; then
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$DRIFT_PLAN" && fail "BREAK=1: two VPAs were deleted but the plan still proposes exactly one create"
  log "  BREAK=1: caught - with a second VPA deleted the plan is $(plan_line "$DRIFT_PLAN")"
  ( tofu_a apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: could not reconverge A"
  gauntlet_stage drift_reconverge pass "BREAK=1 control: with a second VPA deleted the single-object assertion correctly fails to hold ($(plan_line "$DRIFT_PLAN")); reconverged afterwards"
else
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$DRIFT_PLAN" || { printf '%s\n' "$DRIFT_PLAN" | tail -20; fail "the plan after one delete does not propose exactly one create"; }
  grep -qF "# $DRIFT_ADDR will be created" <<< "$DRIFT_PLAN" || { printf '%s\n' "$DRIFT_PLAN" | grep -E ' will be '; fail "the one create is not $DRIFT_ADDR"; }
  RECONV="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$RECONV" | tail -20; fail "the reconverging apply failed"; }
  grep -qF "Apply complete! Resources: 1 added, 0 changed, 0 destroyed" <<< "$RECONV" || fail "the reconverging apply did not create exactly one object"
  [ "$(kca get verticalpodautoscaler "$DRIFT_NAME" -n "$DRIFT_NS" -o jsonpath='{.spec.targetRef.name}' 2>/dev/null)" = "$DRIFT_NAME" ] || fail "the $DRIFT_NAME VPA is not back with its targetRef after reconverging"
  [ "$(count_a)" = "$TOTAL_N" ] || fail "$(count_a) labelled objects after reconverging, want $TOTAL_N - the recreated VPA did not get its marker"
  gauntlet_stage drift_reconverge pass "one for_each instance deleted out of band with kubectl (the $DRIFT_NAME VPA in $DRIFT_NS); choudoufu proposed putting back exactly $DRIFT_ADDR (1 add, 0 change, 0 destroy) - the instance found missing by its static-map key, its four siblings untouched - matching stock's own plan on the oracle cluster for the same delete; apply created 1, the targetRef reads back as configured, and the recreated object carries the estate label again ($TOTAL_N labelled). A delete rather than a patch because kubernetes_manifest applies server-side and a kubectl patch would measure field-manager ownership, not drift. BREAK=1 deletes a second VPA and the single-object assertion correctly fails"
fi

# ── 6. plan_approval ──────────────────────────────────────────────────────
gauntlet_begin_stage plan_approval
log "=== 6. plan_approval: the global-default PriorityClass's description changes; a saved plan, an out-of-band label, a refusal ==="
review_description() {
  python3 - "$1/rbac.tf" <<'PY'
import sys
p = sys.argv[1]; s = open(p).read()
old = 'description    = "Default priority class for all pods."'
assert s.count(old) == 1, "the default PriorityClass's description is not where this edit expects it - the corpus pin has moved"
open(p, "w").write(s.replace(old, 'description    = "Default priority class for all pods. Reviewed."', 1))
PY
}
review_description "$ADOPTED" || fail "could not edit the description in the adopted root"
review_description "$ORACLE"  || fail "could not edit the description in the oracle root"
P_PLAN="$(tofu_a plan -out=approved.tfplan -input=false -no-color 2>&1)" || { printf '%s\n' "$P_PLAN" | tail -20; fail "plan -out failed"; }
grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$P_PLAN" || { printf '%s\n' "$P_PLAN" | tail -10; fail "the saved plan is not exactly one change"; }
grep -qF "# kubernetes_priority_class.default will be updated in-place" <<< "$P_PLAN" || fail "the saved plan's one change is not kubernetes_priority_class.default"
kca label storageclass gp3 stray=yes >/dev/null || fail "could not move the world (label storageclass/gp3) on A"
P_APPLY="$(tofu_a apply -input=false -no-color approved.tfplan 2>&1)"; P_RC=$?
if [ "${BREAK_APPROVAL:-}" = "1" ]; then
  [ "$P_RC" -eq 0 ] && fail "BREAK_APPROVAL=1: applying the saved plan after the world moved succeeded"
  log "  BREAK_APPROVAL=1: caught - the apply after the world moved exited $P_RC"
  kca label storageclass gp3 stray- >/dev/null
  ( tofu_a apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: the saved plan did not apply once the world was put back"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: stock could not apply the description on B"
  gauntlet_stage plan_approval pass "BREAK_APPROVAL=1 control: applying the saved plan after the world moved exited $P_RC (refused), so the stage's own Break line correctly fails; applied once the world was put back"
else
  [ "$P_RC" -eq 3 ] || { printf '%s\n' "$P_APPLY" | tail -20; fail "apply of the saved plan after the world moved exited $P_RC, want 3"; }
  grep -qF "The approved plan no longer matches the live system" <<< "$P_APPLY" || { printf '%s\n' "$P_APPLY" | tail -20; fail "the refusal does not carry its documented sentence"; }
  [ "$(kca get priorityclass default -o jsonpath='{.description}')" = "Default priority class for all pods." ] || fail "the PriorityClass's description changed despite the refusal"
  kca label storageclass gp3 stray- >/dev/null || fail "could not put the world back"
  P_APPLY2="$(tofu_a apply -input=false -no-color approved.tfplan 2>&1)" || { printf '%s\n' "$P_APPLY2" | tail -20; fail "the saved plan did not apply once the world was put back"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$P_APPLY2" || fail "the saved plan's apply did not change exactly one object"
  [ "$(kca get priorityclass default -o jsonpath='{.description}')" = "Default priority class for all pods. Reviewed." ] || fail "the PriorityClass does not read the reviewed description after the saved plan applied"
  ( stock_b plan -out=approved.tfplan -input=false -no-color >/dev/null 2>&1 && stock_b apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "stock's own planfile did not apply on B"
  gauntlet_stage plan_approval pass "plan -out wrote one in-place update (the global-default PriorityClass's description, a mutable field); the world then moved out of band (a stray label on the gp3 StorageClass, kubectl, never choudoufu) and apply of the saved plan refused with \"The approved plan no longer matches the live system\" at exit 3, nothing applied; with the label removed the identical file applied, 0 added, 1 changed, 0 destroyed, and the new description reads back; stock's own planfile applied on the oracle cluster. BREAK_APPROVAL=1 expects success after the move and correctly fails"
fi

# ── 7. day2_rename ────────────────────────────────────────────────────────
gauntlet_begin_stage day2_rename
log "=== 7. day2_rename: kubernetes_cluster_role_binding.webops becomes .webops_admin through a moved block ==="
rename_crb() {
  python3 - "$1/rbac.tf" <<'PY'
import sys
p = sys.argv[1]; s = open(p).read()
old = 'resource "kubernetes_cluster_role_binding" "webops" {'
assert s.count(old) == 1, "the webops ClusterRoleBinding block is not where the rename expects it - the corpus pin has moved"
s = s.replace(old, 'resource "kubernetes_cluster_role_binding" "webops_admin" {', 1)
s += '\nmoved {\n  from = kubernetes_cluster_role_binding.webops\n  to   = kubernetes_cluster_role_binding.webops_admin\n}\n'
open(p, "w").write(s)
PY
}
if [ "${BREAK:-}" = "1" ]; then
  python3 - "$ADOPTED/rbac.tf" <<'PY' || fail "BREAK: could not rename the ClusterRoleBinding's metadata.name"
import sys
p = sys.argv[1]; s = open(p).read()
old = 'name = "webops-cluster-admin"'
assert s.count(old) == 1
open(p, "w").write(s.replace(old, 'name = "webops-cluster-admin-renamed"', 1))
PY
  R_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK: the plan after renaming the object failed"; }
  { grep -q "1 to add" <<< "$R_PLAN" && grep -q "1 to destroy" <<< "$R_PLAN"; } || { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK=1: renaming the object's own name did not plan a destroy and a create: $(plan_line "$R_PLAN")"; }
  log "  BREAK=1: caught - renaming metadata.name plans $(plan_line "$R_PLAN")"
  python3 - "$ADOPTED/rbac.tf" <<'PY' || fail "BREAK: could not put the name back"
import sys
p = sys.argv[1]; s = open(p).read()
open(p, "w").write(s.replace('name = "webops-cluster-admin-renamed"', 'name = "webops-cluster-admin"', 1))
PY
  rename_crb "$ADOPTED" || fail "BREAK: could not rename the block"; rename_crb "$ORACLE" || fail "BREAK: could not rename the oracle's block"
  ( tofu_a apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: the moved-block apply failed"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: stock's moved-block apply failed on B"
  gauntlet_stage day2_rename pass "BREAK=1 control: renaming the ClusterRoleBinding's own metadata.name plans a replace ($(plan_line "$R_PLAN")), so the marker-rewritten-in-place assertion correctly fails to hold; the moved block then applied"
else
  rename_crb "$ADOPTED" || fail "could not rename the block in the adopted root"
  rename_crb "$ORACLE"  || fail "could not rename the block in the oracle root"
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's moved-block plan failed on B"; }
  grep -qE "^No changes|Plan: 0 to add, 0 to change, 0 to destroy" <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's moved-block plan on B is not zero churn"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's moved-block apply failed on B"
  R_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_PLAN" | tail -20; fail "the moved-block plan failed"; }
  grep -qE 'will be (created|destroyed)' <<< "$R_PLAN" \
    && { printf '%s\n' "$R_PLAN" | grep -E '^  # .+ will be'; fail "the moved-block rename proposes a create or a destroy - not the marker rewritten in place"; }
  grep -qF 'Plan: 0 to add, 1 to change, 0 to destroy.' <<< "$R_PLAN" \
    || { printf '%s\n' "$R_PLAN" | tail -20; fail "the moved-block plan is not exactly one in-place change (the address annotation rewrite)"; }
  grep -qE '~ +"choudoufu\.intentius\.io/tofu-address" = ".*" -> ".*"' <<< "$R_PLAN" \
    || { printf '%s\n' "$R_PLAN"; fail "the moved-block plan does not show the tofu-address annotation being rewritten"; }
  R_APPLY_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$R_APPLY_OUT" | tail -20; fail "the moved-block apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$R_APPLY_OUT" || { printf '%s\n' "$R_APPLY_OUT" | tail -10; fail "the moved-block apply was not exactly one in-place change"; }
  kca get clusterrolebinding webops-cluster-admin >/dev/null 2>&1 || fail "the ClusterRoleBinding is gone after the rename"
  [ "$(count_a)" = "$TOTAL_N" ] || fail "$(count_a) labelled objects after the rename, want $TOTAL_N"
  gauntlet_stage day2_rename pass "moved block over a cluster-scoped, unversioned type: kubernetes_cluster_role_binding.webops -> .webops_admin, no add and no destroy, one in-place change confined to the address annotation rewrite (0 add, 1 change, 0 destroy) - the marker rewritten in place, not literal zero churn; the live ClusterRoleBinding untouched and still labelled; stock's plan for the same moved block on the oracle cluster is zero churn, since stock never writes this annotation. The moved-block half only: live-mv also has a Kubernetes leg since #1639, not exercised by this stage. BREAK=1 renames the object's own metadata.name and the assertion correctly fails"
fi

# ── 8. day2_remove ────────────────────────────────────────────────────────
gauntlet_begin_stage day2_remove
log "=== 8. day2_remove: the node-critical PriorityClass block leaves the configuration ==="
remove_node_critical() {
  python3 - "$1/rbac.tf" <<'PY'
import re, sys
p = sys.argv[1]; s = open(p).read()
s2 = re.sub(r'resource "kubernetes_priority_class" "node_critical" \{.*?\n\}\n\n', '', s, count=1, flags=re.S)
assert s2 != s and '"node_critical"' not in s2, "the node_critical block did not match rbac.tf - the corpus pin has moved"
open(p, "w").write(s2)
PY
}
REMAIN_N=$((TOTAL_N - 1))
if [ "${BREAK_REMOVE:-}" = "1" ]; then
  K_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || fail "BREAK_REMOVE: the plan with the block kept failed"
  grep -qE "will be destroyed|[1-9][0-9]* to destroy" <<< "$K_PLAN" && fail "BREAK_REMOVE=1: with the block kept a destroy was still proposed"
  log "  BREAK_REMOVE=1: caught - with the block kept the plan proposes no destroy"
  gauntlet_stage day2_remove pass "BREAK_REMOVE=1 control: with the node-critical PriorityClass block kept, no destroy is proposed; the real check is skipped"
  remove_node_critical "$ADOPTED" || fail "BREAK_REMOVE: could not remove the block afterwards"
  remove_node_critical "$ORACLE"  || fail "BREAK_REMOVE: could not remove the block from the oracle root afterwards"
  ( tofu_a apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_REMOVE: the removal apply failed afterwards"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_REMOVE: stock's removal apply failed on B afterwards"
else
  remove_node_critical "$ADOPTED" || fail "could not remove the block from the adopted root"
  remove_node_critical "$ORACLE"  || fail "could not remove the block from the oracle root"
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's remove plan failed on B"; }
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's remove plan on B is not exactly one destroy"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's remove apply failed on B"
  D_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$D_PLAN" | tail -20; fail "the remove plan failed"; }
  if ! grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$D_PLAN"; then
    printf '%s\n' "$D_PLAN" | tail -20
    fail "$(gauntlet_destroy_gap_verdict '' "the node-critical PriorityClass's removal" "$O_PLAN" <<< "$D_PLAN")"
  fi
  D_LINE="$(grep -E '^[[:space:]]*# .* will be destroyed' <<< "$D_PLAN" | head -1)"
  D_ADDR="$(sed -E 's/^[[:space:]#]*//; s/ will be destroyed.*$//' <<< "$D_LINE")"
  grep -qE '^kubernetes_priority_class(_v1)?\.orphan_.*node-critical$' <<< "$D_ADDR" || { printf '%s\n' "$D_PLAN" | grep -E 'destroyed|^Plan:'; fail "the one destroy is ${D_ADDR:-unnamed}, not the node-critical PriorityClass at its orphan address"; }
  D_APPLY="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$D_APPLY" | tail -20; fail "the remove apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 0 changed, 1 destroyed" <<< "$D_APPLY" || fail "the remove apply did not destroy exactly one object"
  kca get priorityclass node-critical >/dev/null 2>&1 && fail "the node-critical PriorityClass still exists after the remove apply"
  grep -q "No changes." <<< "$(tofu_a plan -input=false -no-color 2>&1)" || fail "the replan after the remove is not empty"
  [ "$(count_a)" = "$REMAIN_N" ] || fail "$(count_a) labelled objects after the remove, want $REMAIN_N"
  gauntlet_stage day2_remove pass "deleting kubernetes_priority_class.node_critical's block proposed exactly one destroy (0 add, 0 change, 1 destroy) at the sweep's orphan address $D_ADDR - a cluster-scoped object found by its label, which carries no address - applied cleanly, the object gone (kubectl get priorityclass: NotFound), the next plan empty, $REMAIN_N objects still labelled; kind's own system-cluster-critical and system-node-critical, which carry no estate label, were never proposed; stock's plan for the same removal on the oracle cluster is also exactly one destroy. BREAK_REMOVE=1 keeps the block and no destroy is proposed"
fi

# ── 9. day2_count ─────────────────────────────────────────────────────────
#
# The published root's for_each is a static map, so the count change is a
# block this stage adds and takes away again: a counted VerticalPodAutoscaler
# in the monitoring namespace, so the instance that leaves the count is a
# custom kind the sweep finds by label under kubernetes_manifest.
gauntlet_begin_stage day2_count
log "=== 9. day2_count: a counted VerticalPodAutoscaler the script declares scales 2 -> 1 -> 2 ==="
write_vpa_shards() { # $1 root, $2 count
  cat > "$1/count_test.tf" <<EOF
# Added by live/e2e/$ESTATE/run.sh for the gauntlet's day2_count stage:
# the published root declares no count block of its own.
resource "kubernetes_manifest" "vpa_shard" {
  count = $2
  manifest = {
    apiVersion = "autoscaling.k8s.io/v1"
    kind       = "VerticalPodAutoscaler"
    metadata = {
      name      = "vpa-shard-\${count.index}"
      namespace = "monitoring"
    }
    spec = {
      targetRef = {
        apiVersion = "apps/v1"
        kind       = "Deployment"
        name       = "shard-\${count.index}"
      }
      updatePolicy = {
        updateMode = "Off"
      }
    }
  }
}
EOF
}
vpa_exists() { kca get verticalpodautoscaler "$1" -n monitoring >/dev/null 2>&1; }
write_vpa_shards "$ADOPTED" 2; write_vpa_shards "$ORACLE" 2
APPLY_OUT="$(stock_b apply -auto-approve -input=false -no-color 2>&1)"
grep -qF "2 added, 0 changed, 0 destroyed" <<< "$APPLY_OUT" || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "stock could not add the two VPA shards on B"; }
APPLY_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)"
grep -qF "2 added, 0 changed, 0 destroyed" <<< "$APPLY_OUT" || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "choudoufu could not add the two VPA shards on A"; }
C_RE="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$C_RE" | tail -20; fail "the replan after adding the shards failed"; }
grep -q "No changes." <<< "$C_RE" || { printf '%s\n' "$C_RE" | grep -E '^Plan:| will be '; fail "replanning the two counted VPAs choudoufu had just applied is not empty: $(plan_line "$C_RE")"; }
write_vpa_shards "$ADOPTED" 1; write_vpa_shards "$ORACLE" 1
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's scale-down plan failed on B"
grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's scale-down plan on B is not exactly one destroy"; }
grep -qF 'kubernetes_manifest.vpa_shard[1]' <<< "$O_PLAN" || fail "stock's scale-down on B does not destroy vpa_shard[1]"
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's scale-down apply failed on B"
C_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$C_PLAN" | tail -20; fail "the scale-down plan failed"; }
grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$C_PLAN" || { printf '%s\n' "$C_PLAN" | tail -20; fail "the scale-down plan is not exactly one destroy"; }
C_LINE="$(grep -E '^[[:space:]]*# .* will be destroyed' <<< "$C_PLAN" | head -1)"
C_ADDR="$(sed -E 's/^[[:space:]#]*//; s/ will be destroyed.*$//' <<< "$C_LINE")"
grep -qE '^kubernetes_manifest\.orphan_[a-z]+_monitoring_vpa-shard-1$' <<< "$C_ADDR" || { printf '%s\n' "$C_PLAN" | grep -E 'destroyed|^Plan:'; fail "the scale-down destroys ${C_ADDR:-nothing named}, not vpa-shard-1 at its orphan address"; }
APPLY_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)"
grep -qF "0 added, 0 changed, 1 destroyed" <<< "$APPLY_OUT" || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "the scale-down apply did not destroy exactly one object"; }
if [ "${BREAK_COUNT:-}" = "1" ]; then
  vpa_exists vpa-shard-0 || fail "BREAK_COUNT=1: vpa-shard-0 was destroyed - the 'wrong instance' assertion would hold"
  log "  BREAK_COUNT=1: caught - vpa-shard-0 still exists, so asserting it was the one destroyed correctly fails"
  gauntlet_stage day2_count pass "BREAK_COUNT=1 control: asserting the lower index (vpa-shard-0) was destroyed correctly fails to hold; the real check is skipped"
else
  vpa_exists vpa-shard-0 || fail "vpa-shard-0 was destroyed on the scale-down"
  vpa_exists vpa-shard-1 && fail "vpa-shard-1 still exists after the scale-down"
  write_vpa_shards "$ADOPTED" 2; write_vpa_shards "$ORACLE" 2
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's scale-up plan failed on B"
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's scale-up plan on B is not exactly one add"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's scale-up apply failed on B"
  U_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$U_PLAN" | tail -20; fail "the scale-up plan failed"; }
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$U_PLAN" || { printf '%s\n' "$U_PLAN" | tail -20; fail "the scale-up plan is not exactly one add"; }
  grep -qF '# kubernetes_manifest.vpa_shard[1] will be created' <<< "$U_PLAN" || fail "the scale-up does not create vpa_shard[1]"
  APPLY_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)"
  grep -qF "1 added, 0 changed, 0 destroyed" <<< "$APPLY_OUT" || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "the scale-up apply did not create exactly one object"; }
  { vpa_exists vpa-shard-0 && vpa_exists vpa-shard-1; } || fail "both VPA shards do not exist after the scale-up"
  grep -q "No changes." <<< "$(tofu_a plan -input=false -no-color 2>&1)" || fail "the replan after the scale-up is not empty"
  gauntlet_stage day2_count pass "a two-instance counted VerticalPodAutoscaler (kubernetes_manifest.vpa_shard, name vpa-shard-\${count.index} inside the manifest object) added beside the published root, whose own for_each is a static map: the replan right after creating both is empty, scaling 2 to 1 destroyed exactly vpa-shard-1, planned at the sweep's orphan address $C_ADDR since the label carries no index (vpa-shard-0 untouched, both read with kubectl); back to 2 created exactly kubernetes_manifest.vpa_shard[1] under the same name; the next plan is empty; stock's plans for the same two changes on the oracle cluster have the identical shape. BREAK_COUNT=1 asserts the lower index was destroyed and correctly fails"
fi
# Withdrawn from both roots, so every later stage counts the estate alone.
rm -f "$ADOPTED/count_test.tf" "$ORACLE/count_test.tf"
( tofu_a apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "could not withdraw the VPA shards from A"
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "could not withdraw the VPA shards from B"
[ "$(count_a)" = "$REMAIN_N" ] || fail "$(count_a) labelled objects after withdrawing the VPA shards, want $REMAIN_N"

# ── 9b. day2_replace ──────────────────────────────────────────────────────
#
# Two halves under one verdict. First the half this estate exists for:
# a StorageClass and a PriorityClass changed on a field the API server will
# not update (a StorageClass's parameters, a PriorityClass's value), which
# the provider marks ForceNew. Both keep their names, so on either tool the
# replace is destroy-then-create - not the create_before_destroy shape the
# stage's kind text measures (#1541), and the first time an estate's own
# immutable field is put through a replace on this substrate. Then the
# shared create_before_destroy rename (live/e2e/lib/gauntlet.sh's
# gauntlet_kind_day2_replace), which reports the verdict with this half's
# sentence in front of it.
gauntlet_begin_stage day2_replace
log "=== 9b. day2_replace: io1-expand's iopsPerGB and cluster-critical's value change; then the shared create_before_destroy rename ==="
replace_immutables() {
  python3 - "$1/storage.tf" "$1/rbac.tf" <<'PY'
import sys
sp, rp = sys.argv[1], sys.argv[2]
s = open(sp).read(); r = open(rp).read()
old_s = 'iopsPerGB = "26"'
old_r = 'value          = 999999000'
assert s.count(old_s) == 1, "io1-expand's iopsPerGB is not where this edit expects it - the corpus pin has moved"
assert r.count(old_r) == 1, "cluster-critical's value is not where this edit expects it - the corpus pin has moved"
open(sp, "w").write(s.replace(old_s, 'iopsPerGB = "50"', 1))
open(rp, "w").write(r.replace(old_r, 'value          = 999998000', 1))
PY
}
# is_immutable_replace <plan> <where>: both blocks replaced, destroy first.
is_immutable_replace() {
  grep -qF "Plan: 2 to add, 0 to change, 2 to destroy." <<< "$1" || { printf '%s\n' "$1" | tail -20; fail "$2: the two immutable-field changes are not two adds and two destroys: $(plan_line "$1")"; }
  grep -qF "# kubernetes_storage_class.io1 must be replaced" <<< "$1" || { printf '%s\n' "$1" | grep -E '# '; fail "$2: kubernetes_storage_class.io1 is not planned as a replace"; }
  grep -qF "# kubernetes_priority_class.cluster_critical must be replaced" <<< "$1" || { printf '%s\n' "$1" | grep -E '# '; fail "$2: kubernetes_priority_class.cluster_critical is not planned as a replace"; }
  grep -qF -- "-/+ destroy and then create replacement" <<< "$1" || fail "$2: the replace is not destroy-first"
  if grep -q "orphan_" <<< "$1"; then
    printf '%s\n' "$1" | grep -E '# '
    fail "$2: an old object is planned as an orphan beside a create rather than as the replace's destroy half"
  fi
}
replace_immutables "$ADOPTED" || fail "could not make the immutable-field edits in the adopted root"
replace_immutables "$ORACLE"  || fail "could not make the immutable-field edits in the oracle root"
I_ORACLE="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$I_ORACLE" | tail -10; fail "stock's immutable-field plan failed on B"; }
is_immutable_replace "$I_ORACLE" "stock on B (the oracle)"
APPLY_OUT="$(stock_b apply -auto-approve -input=false -no-color 2>&1)"
grep -qF "2 added, 0 changed, 2 destroyed" <<< "$APPLY_OUT" || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "stock's immutable-field replace did not apply as two adds and two destroys on B"; }
I_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$I_PLAN" | tail -20; fail "the immutable-field plan failed"; }
is_immutable_replace "$I_PLAN" "choudoufu"
I_APPLY="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$I_APPLY" | tail -20; fail "the immutable-field replace apply failed"; }
grep -qF "Apply complete! Resources: 2 added, 0 changed, 2 destroyed" <<< "$I_APPLY" || { printf '%s\n' "$I_APPLY" | tail -10; fail "the immutable-field replace did not apply as two adds and two destroys"; }
[ "$(kca get storageclass io1-expand -o jsonpath='{.parameters.iopsPerGB}')" = "50" ] || fail "io1-expand does not read iopsPerGB=50 after the replace"
[ "$(kca get priorityclass cluster-critical -o jsonpath='{.value}')" = "999998000" ] || fail "cluster-critical does not read value=999998000 after the replace"
NAMES="$(kca get storageclass -l "tofu-estate=$ESTATE" -o name 2>/dev/null)"
grep -qx "storageclass.storage.k8s.io/io1-expand" <<< "$NAMES" || { printf '%s\n' "$NAMES"; fail "the replaced io1-expand does not carry tofu-estate=$ESTATE"; }
NAMES="$(kca get priorityclass -l "tofu-estate=$ESTATE" -o name 2>/dev/null)"
grep -qx "priorityclass.scheduling.k8s.io/cluster-critical" <<< "$NAMES" || { printf '%s\n' "$NAMES"; fail "the replaced cluster-critical does not carry tofu-estate=$ESTATE"; }
I_REPLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$I_REPLAN" | tail -20; fail "the replan after the immutable-field replace failed"; }
grep -q "No changes." <<< "$I_REPLAN" || { printf '%s\n' "$I_REPLAN" | grep -E '^Plan:| will be | must be '; fail "the replan after the immutable-field replace is not empty: $(plan_line "$I_REPLAN")"; }
[ "$(count_a)" = "$REMAIN_N" ] || fail "$(count_a) labelled objects after the immutable-field replace, want $REMAIN_N"
IMMUTABLE_DETAIL="Immutable fields first: io1-expand's parameters.iopsPerGB (26 -> 50) and cluster-critical's value (999999000 -> 999998000), both ForceNew in the provider and refused as updates by the API server, planned as kubernetes_storage_class.io1 and kubernetes_priority_class.cluster_critical 'must be replaced', -/+ destroy and then create replacement, 2 add and 2 destroy with no orphan beside them - the same plan stock makes on the oracle cluster; applied 2 added, 2 destroyed, the new values and the estate label read back with kubectl, the replan empty, $REMAIN_N objects still labelled. Then the shared create_before_destroy rename:"
gauntlet_kind_day2_replace "$ADOPTED" "$ORACLE" "$NS" "$IMMUTABLE_DETAIL"

# ── 10. day2_crash ────────────────────────────────────────────────────────
#
# The create_before_destroy rename window first (#1768): live/e2e/lib/
# gauntlet.sh's gauntlet_kind_day2_crash_rename, which adds its own blocks,
# interrupts, recovers and removes them again, and leaves
# CRASH_RENAME_DETAIL for every day2_crash verdict below. Then an apply of
# two objects with a real edge between them, killed after the first
# commits, as corpus-quickpizza does it: a kubernetes_secret_v1 first,
# because a Secret records residue (wait_for_service_account_token) and a
# ConfigMap records nothing (#1188, #1235).
gauntlet_begin_stage day2_crash
log "=== 10. day2_crash: SIGTERM between the create of one object and the create of the next ==="
gauntlet_kind_day2_crash_rename "$ADOPTED" "$NS"

write_crash_pair() { # $1 root, $2 = "first" for crash-first alone
  cat > "$1/crash_test.tf" <<EOF
# Added by live/e2e/$ESTATE/run.sh for the gauntlet's day2_crash stage: the
# published root declares no pair of objects with an edge between them that
# an interrupted apply could be caught halfway through.
resource "kubernetes_secret_v1" "crash_first" {
  metadata {
    name      = "crash-first"
    namespace = kubernetes_namespace_v1.concourse.metadata[0].name
  }
  data = { step = "one" }
}
EOF
  [ "${2:-both}" = "first" ] && return 0
  cat >> "$1/crash_test.tf" <<EOF

resource "kubernetes_config_map_v1" "crash_second" {
  metadata {
    name      = "crash-second"
    namespace = kubernetes_namespace_v1.concourse.metadata[0].name
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
X_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$X_PLAN" | tail -20; fail "the pre-crash plan failed"; }
grep -qF "Plan: 2 to add, 0 to change, 0 to destroy." <<< "$X_PLAN" \
  || { printf '%s\n' "$X_PLAN" | tail -20; fail "the pre-crash plan is not exactly two adds - there is no two-object apply to interrupt"; }
X_RECORDS_BEFORE="$(gauntlet_record_envelope_count "$ADOPTED/.tofu-records")"

# The interrupt is delivered by the engine itself, so this runs in the
# plain foreground. A non-zero exit is the NORMAL outcome.
X_OUT="$( cd "$ADOPTED" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" \
  TOFU_E2E_APPLY_RESOURCE_INTERRUPT="kubernetes_secret_v1.crash_first" \
  "$TOFU_CRASH" apply -input=false -auto-approve -no-color -parallelism=1 2>&1 )"; X_RC=$?
log "  interrupted apply exited $X_RC (a genuine crash is not expected to exit 0)"
[ "$X_RC" -ne 0 ] || { printf '%s\n' "$X_OUT" | tail -20; fail "the interrupted apply exited 0 - the engine's self-signal never landed"; }
exists_a secret crash-first || { printf '%s\n' "$X_OUT" | tail -20; fail "crash-first does not exist after the interrupted apply - the kill landed before the create committed"; }
exists_a configmap crash-second && { printf '%s\n' "$X_OUT" | tail -20; fail "crash-second exists after the interrupted apply - the kill landed after both creates"; }
NAMES="$(kca get secret -n "$NS" -l "tofu-estate=$ESTATE" -o name 2>/dev/null)"
grep -qx "secret/crash-first" <<< "$NAMES" || { printf '%s\n' "$NAMES"; fail "crash-first was created by the interrupted apply but does not come back under tofu-estate=$ESTATE"; }
X_RECORDS_AFTER="$(gauntlet_record_envelope_count "$ADOPTED/.tofu-records")"
X_REC="$(gauntlet_record_file "$ADOPTED/.tofu-records" "kubernetes_secret_v1.crash_first")"
[ -n "$X_REC" ] || fail "the interrupted apply created crash-first but wrote no record for kubernetes_secret_v1.crash_first (records $X_RECORDS_BEFORE -> $X_RECORDS_AFTER)"
[ "$X_RECORDS_AFTER" = "$((X_RECORDS_BEFORE + 1))" ] || fail "records went $X_RECORDS_BEFORE -> $X_RECORDS_AFTER across the interrupted apply, want exactly one more"
X_RESIDUE="$(gauntlet_record_residue "$X_REC" | tr '\n' ' ' | sed 's/ $//')"
[ "$X_RESIDUE" = "wait_for_service_account_token" ] || fail "the record for kubernetes_secret_v1.crash_first carries residue [${X_RESIDUE:-none}], want wait_for_service_account_token (#1188, #1235)"

if [ "${BREAK_CRASH_UNBOUND:-}" = "1" ]; then
  kca label secret crash-first -n "$NS" tofu-estate- >/dev/null 2>&1 || fail "BREAK_CRASH_UNBOUND: could not strip the label off crash-first"
  log "  BREAK_CRASH_UNBOUND=1: stripped tofu-estate off crash-first with kubectl"
fi

R_PLAN="$(tofu_a plan -input=false -no-color 2>&1)"; R_RC=$?
R_LINE="$(plan_line "$R_PLAN")"
recovered() {
  [ "$R_RC" -eq 0 ] || return 1
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$R_PLAN" || return 1
  grep -qE '^[[:space:]]*# kubernetes_config_map(_v1)?\.crash_second will be created' <<< "$R_PLAN" || return 1
  local will
  will="$(grep -E '^[[:space:]]*# .* will be' <<< "$R_PLAN")"
  grep -q 'crash_first\|crash-first' <<< "$will" && return 1
  return 0
}

if [ "${BREAK_CRASH:-}" = "1" ]; then
  [ "$R_RC" -eq 0 ] || { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK_CRASH=1: the plan after the interrupt exited $R_RC"; }
  grep -qF "No changes." <<< "$R_PLAN" && fail "BREAK_CRASH=1: the plan after a real interrupted two-object apply came back empty"
  gauntlet_stage day2_crash pass "BREAK_CRASH=1 control: after the same real interrupt the plan proposes work ($R_LINE), so 'nothing is proposed' correctly fails to hold; the real check is skipped. $CRASH_RENAME_DETAIL"
  ( tofu_a apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_CRASH: the recovery apply failed afterwards"
elif [ "${BREAK_CRASH_UNBOUND:-}" = "1" ]; then
  if recovered; then
    printf '%s\n' "$R_PLAN" | grep -E '^Plan:|will be'
    fail "BREAK_CRASH_UNBOUND=1: the recovery check still holds with crash-first carrying no tofu-estate label"
  fi
  gauntlet_stage day2_crash pass "BREAK_CRASH_UNBOUND=1 control: with the tofu-estate label stripped off the object the interrupted apply created, the recovery check correctly fails to hold ($R_LINE); the real check is skipped. $CRASH_RENAME_DETAIL"
  kca delete secret crash-first -n "$NS" >/dev/null 2>&1 || fail "BREAK_CRASH_UNBOUND: could not delete the unlabelled crash-first afterwards"
  ( tofu_a apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_CRASH_UNBOUND: the apply after the cleanup failed"
elif ! recovered; then
  printf '%s\n' "$R_PLAN" | grep -E '^Plan:|^No changes|will be' | head -20
  gauntlet_stage day2_crash fail "the plan after a real interrupt between the create of kubernetes_secret_v1.crash_first and the create of kubernetes_config_map_v1.crash_second is not exactly the remainder: ${R_LINE:-no plan line} (exit $R_RC). crash-first exists carrying tofu-estate=$ESTATE and crash-second does not, both read with kubectl; stock from the same position plans exactly one add. $CRASH_RENAME_DETAIL"
else
  R_APPLY="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$R_APPLY" | tail -20; fail "the recovery apply failed"; }
  grep -qF "Apply complete! Resources: 1 added, 0 changed, 0 destroyed" <<< "$R_APPLY" || { printf '%s\n' "$R_APPLY" | tail -5; fail "the recovery apply did not add exactly the one remaining object"; }
  exists_a configmap crash-second || fail "crash-second does not exist after the recovery apply"
  exists_a secret crash-first || fail "crash-first is gone after the recovery apply"
  R_REPLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_REPLAN" | tail -30; fail "the replan after the recovery failed: $(gauntlet_first_error_line <<< "$R_REPLAN")"; }
  grep -q "No changes." <<< "$R_REPLAN" || { printf '%s\n' "$R_REPLAN" | tail -20; fail "the replan after the recovery is not empty"; }
  gauntlet_stage day2_crash pass "a Secret and a ConfigMap added beside the published root in the concourse namespace, the second reading the first's name: the apply creating both was interrupted by a real SIGTERM (exit $X_RC) delivered by the engine inside the -parallelism=1 walker the instant kubernetes_secret_v1.crash_first's create committed; kubectl confirms crash-first exists carrying tofu-estate=$ESTATE and crash-second does not; the interrupted apply wrote exactly one record (envelopes $X_RECORDS_BEFORE -> $X_RECORDS_AFTER) carrying residue $X_RESIDUE. The next plan proposed exactly the remainder ($R_LINE, crash_second created) and nothing for crash-first, bound by its label and its namespace and name, matching stock's plan from the same position on the oracle cluster; the recovery apply added one and the replan is empty. BREAK_CRASH=1 and BREAK_CRASH_UNBOUND=1 correctly fail. $CRASH_RENAME_DETAIL"
fi
gauntlet_end_stage
# The crash pair is not withdrawn: crash-first and crash-second stay in the
# concourse namespace until day2_teardown, whose expected count adds them.

# ── 11. day2_teardown ─────────────────────────────────────────────────────
gauntlet_begin_stage day2_teardown
log "=== 11. day2_teardown: apply -destroy on the adopted estate, and stock's own destroy on B ==="
T_EXPECT="$(count_a)"
T_EXPECT=$((T_EXPECT + $(KUBECONFIG="$KCA" gauntlet_kind_count "$ESTATE" secrets configmaps)))
T_OUT="$(tofu_a apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$T_OUT" | tail -20; fail "apply -destroy failed"; }
grep -qF "Resources: 0 added, 0 changed, $T_EXPECT destroyed" <<< "$T_OUT" || { printf '%s\n' "$T_OUT" | tail -5; fail "apply -destroy did not remove exactly the $T_EXPECT remaining objects"; }
ns_gone() { ! kca get namespace ingress-controllers >/dev/null 2>&1 && ! kca get namespace concourse >/dev/null 2>&1 && ! kca get namespace monitoring >/dev/null 2>&1; }
gauntlet_wait_until 180 "the estate's three namespaces to finish terminating on A" -- ns_gone || fail "an estate namespace still exists after the destroy"
[ "$(count_a)" = "0" ] || fail "$(count_a) object(s) still carry tofu-estate=$ESTATE after the destroy"
CRDS_LEFT="$(kca get crd -o name 2>/dev/null | grep -c 'autoscaling.k8s.io' || true)"
[ "$CRDS_LEFT" = "0" ] || fail "$CRDS_LEFT VPA CRD(s) survive the destroy"
GP2_AFTER="$(kca get storageclass gp2 -o jsonpath='{.metadata.annotations.storageclass\.kubernetes\.io/is-default-class}/{.metadata.labels.tofu-estate}' 2>/dev/null)"
[ "$GP2_AFTER" = "false/" ] || fail "the stock-side gp2 StorageClass reads '${GP2_AFTER:-nothing}' after the estate's destroy, want 'false/' - present, not default, and carrying no estate label: the estate destroyed or adopted an object it never declared"
O_EXPECT="$(stock_b state list 2>/dev/null | wc -l | tr -d ' ')"
O_T="$(stock_b apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$O_T" | tail -10; fail "stock's destroy failed on B"; }
grep -qF "Resources: 0 added, 0 changed, $O_EXPECT destroyed" <<< "$O_T" || fail "stock's destroy on B did not remove exactly the $O_EXPECT objects its state held"
gauntlet_stage day2_teardown pass "apply -destroy removed exactly the $T_EXPECT remaining objects in one apply, in an order the API server accepted - the three namespaces gone, both VPA CRDs gone, and no object of any of the estate's kinds carrying tofu-estate=$ESTATE (kubectl, every namespace); the stock-side gp2 StorageClass (delta 4, never the estate's) is still there, not default and unlabelled; stock's destroy of the same estate on the oracle cluster also removed exactly the $O_EXPECT its state held"

# ── 12. greenfield ────────────────────────────────────────────────────────
gauntlet_begin_stage greenfield
log "=== 12. greenfield: choudoufu applies the root fresh, with a live block, on the now-empty cluster A ==="
write_root "$GREEN" live || fail "could not write the greenfield root"
( green init -input=false -no-color >/dev/null 2>&1 ) || fail "greenfield init failed"

# The refusal before the CRD exists. The cluster is empty again, so the
# kind the VPAs name is not served, and choudoufu refuses the un-targeted
# plan by name (#1079's fourth ruling) rather than leaving it to the
# provider: one refusal for the one block, however many for_each instances
# it has. The diagnostic renderer wraps a detail at the terminal width, so
# the block, kind and apiVersion are matched with the gutter stripped and
# the lines folded.
N_OUT="$(green plan -input=false -no-color 2>&1)"; N_RC=$?
N_FLAT="$(sed -E 's/^[[:space:]]*│[[:space:]]?//' <<< "$N_OUT" | tr '\n' ' ' | tr -s ' ')"
N_KIND="$(grep -c 'Error: Kubernetes kind not served by the cluster' <<< "$N_OUT" || true)"
N_ERRORS="$(grep -cE '^[[:space:]]*(│[[:space:]]*)?Error: ' <<< "$N_OUT" || true)"
if [ "$N_RC" -eq 0 ] || [ "$N_KIND" != "1" ] || [ "$N_ERRORS" != "1" ] \
  || ! grep -qF "kubernetes_manifest.vpa declares kind VerticalPodAutoscaler at apiVersion autoscaling.k8s.io/v1" <<< "$N_FLAT"; then
  printf '%s\n' "$N_OUT" | tail -30
  fail "the un-targeted greenfield plan against a cluster with no VPA CRD is not exactly one \"Kubernetes kind not served by the cluster\" refusal naming kubernetes_manifest.vpa, VerticalPodAutoscaler and autoscaling.k8s.io/v1: exit $N_RC, $N_KIND such refusal(s), $N_ERRORS Error: line(s), first \"$(gauntlet_first_error_line <<< "$N_OUT")\""
fi
log "  before the pre-apply: exit $N_RC, one refusal naming kubernetes_manifest.vpa's kind"

# The same declared list the cold deploy used, read from the manifest.
G_TARGETS=()
while IFS= read -r a; do [ -n "$a" ] && G_TARGETS+=("-target=$a"); done < <(gauntlet_pre_apply_targets "$ESTATE")
[ "${#G_TARGETS[@]}" = "$PRE_N" ] || fail "the declared pre-apply list has ${#G_TARGETS[@]} addresses, want $PRE_N"
G_PRE="$(green apply -auto-approve -input=false -no-color "${G_TARGETS[@]}" 2>&1)" || { printf '%s\n' "$G_PRE" | tail -40; fail "choudoufu could not perform its own declared pre-apply (the same $PRE_N -target arguments stock accepted at cold deploy): $(gauntlet_first_error_line <<< "$G_PRE")"; }
grep -qF "Apply complete! Resources: $PRE_N added" <<< "$G_PRE" || { printf '%s\n' "$G_PRE" | tail -10; fail "the greenfield pre-apply did not add exactly $PRE_N objects"; }
gauntlet_wait_until 120 "cluster A to serve verticalpodautoscalers again after the greenfield pre-apply" -- vpa_served "$KCA" || fail "cluster A never served the VPA kind after the greenfield pre-apply"
G_PLAN0="$(green plan -input=false -no-color 2>&1)" || { printf '%s\n' "$G_PLAN0" | tail -20; fail "the greenfield plan after the pre-apply failed: $(gauntlet_first_error_line <<< "$G_PLAN0")"; }
grep -qF "Plan: $MAIN_N to add, 0 to change, 0 to destroy." <<< "$G_PLAN0" || { printf '%s\n' "$G_PLAN0" | tail -20; fail "the greenfield plan after the pre-apply is not the $MAIN_N creates the cold deploy's main apply made: $(plan_line "$G_PLAN0")"; }
grep -q "Kubernetes kind not served" <<< "$G_PLAN0" && fail "the refusal is still raised after the CRD is served"
G_OUT="$(green apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$G_OUT" | tail -20; fail "greenfield apply failed"; }
grep -qF "Apply complete! Resources: $MAIN_N added, 0 changed, 0 destroyed" <<< "$G_OUT" || { printf '%s\n' "$G_OUT" | tail -5; fail "the greenfield main apply did not add exactly $MAIN_N objects"; }
[ ! -f "$GREEN/terraform.tfstate" ] || fail "a terraform.tfstate appeared after a live-block apply"
G_LABELLED="$(count_a)"
[ "$G_LABELLED" = "$TOTAL_N" ] || fail "$G_LABELLED object(s) carry tofu-estate=$ESTATE after the greenfield apply, want $TOTAL_N"
G_RECORDS="$(gauntlet_record_envelope_count "$GREEN/.tofu-records")"
grep -q "No changes." <<< "$(green plan -input=false -no-color 2>&1)" || fail "the greenfield replan is not empty"
rm -f "$GREEN/.terraform/choudoufu-cache.tfstate"
grep -q "No changes." <<< "$(green plan -input=false -no-color 2>&1)" || fail "the greenfield replan without the cache is not empty"

# The lost-store control (#1235), as corpus-quickpizza takes it: nothing
# may be created, destroyed or replaced with the whole record store gone;
# whatever residue the store held comes back as in-place updates, and one
# apply reconverges.
rm -rf "$GREEN/.tofu-records" "$GREEN/.terraform/choudoufu-cache.tfstate"
L_PLAN="$(green plan -input=false -no-color 2>&1)"; L_RC=$?
L_LINE="$(plan_line "$L_PLAN")"
[ "$L_RC" -eq 0 ] || { printf '%s\n' "$L_PLAN" | tail -20; fail "the plan with no record store at all exited $L_RC"; }
L_GONE="$(grep -cE '^[[:space:]]*# .* (will be (created|destroyed)|must be replaced)' <<< "$L_PLAN" || true)"
[ "$L_GONE" = "0" ] || { printf '%s\n' "$L_PLAN" | grep -E 'will be|must be' | head -20
  fail "with the whole record store deleted the plan proposes $L_GONE create/destroy/replace(s) ($L_LINE) - the objects are not being found by their label and their namespace and name alone"; }
L_CHANGES="$(grep -cE '^[[:space:]]*# .* will be updated in-place' <<< "$L_PLAN" || true)"
L_RESIDUE="$(grep -oE '^[[:space:]]+\+ [a-z_]+ +=' <<< "$L_PLAN" | sed -E 's/^[[:space:]]*\+ //; s/ *=$//' | sort -u | tr '\n' ' ' | sed 's/ $//')"
L_APPLY="$(green apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$L_APPLY" | tail -20; fail "the apply that should reconverge after a lost record store failed"; }
grep -qF "Apply complete! Resources: 0 added, $L_CHANGES changed, 0 destroyed" <<< "$L_APPLY" \
  || { printf '%s\n' "$L_APPLY" | tail -5; fail "the reconverging apply after a lost record store is not exactly the $L_CHANGES in-place update(s) the plan proposed"; }
grep -q "No changes." <<< "$(green plan -input=false -no-color 2>&1)" || fail "the plan after the reconverging apply is not empty"
[ "$(count_a)" = "$TOTAL_N" ] || fail "$(count_a) object(s) carry tofu-estate=$ESTATE after the lost-store reconvergence, want $TOTAL_N"
log "  lost store: $L_LINE, every object still bound; one apply reconverged and the plan after it is empty"

DROP=""; [ "${BREAK:-}" = "1" ] && DROP="storageclass/io1-expand"
inventory "$KCA" "$DROP" > "$WORK/inventory.green.json" || fail "could not read the greenfield inventory"
if [ "${BREAK:-}" = "1" ]; then
  diff -q "$WORK/inventory.stock.json" "$WORK/inventory.green.json" >/dev/null && fail "BREAK=1: with io1-expand dropped the two inventories still match"
  log "  BREAK=1: caught - the inventories differ once io1-expand is dropped"
  gauntlet_stage greenfield pass "BREAK=1 control: dropping the io1-expand StorageClass from the greenfield inventory makes the object-by-object comparison correctly fail; the estate applied ($PRE_N pre-applied, then $MAIN_N, no terraform.tfstate) and replanned empty with and without the cache"
elif ! diff -u "$WORK/inventory.stock.json" "$WORK/inventory.green.json"; then
  gauntlet_stage greenfield fail "the greenfield inventory differs from stock's cold-deploy inventory on the same cluster (diff above this verdict in the log)"
else
  gauntlet_stage greenfield pass "the root applied fresh with a live block and no terraform.tfstate, in the same two applies the cold deploy took. Before the pre-apply, against the empty cluster, the un-targeted plan exits $N_RC with exactly one refusal, \"Kubernetes kind not served by the cluster\", naming kubernetes_manifest.vpa, VerticalPodAutoscaler and autoscaling.k8s.io/v1 - one for the block, not one per for_each instance - and nothing else; choudoufu then performs the declared pre-apply itself ($PRE_N addresses, read from live/gauntlet/estates.json), and once the kind is served the same plan is clean, $MAIN_N to add. $TOTAL_N objects, every one labelled tofu-estate=$ESTATE (kubectl, seven kinds); the record store held $G_RECORDS record envelope(s); replanned empty with and without the cache. With the whole record store and the cache deleted the plan proposed $L_LINE: nothing created, destroyed or replaced, $L_CHANGES in-place update(s) putting back the residue the store held (${L_RESIDUE:-none}); one apply reconverged and the plan after it is empty (#1235). The inventory - every StorageClass's provisioner, parameters, reclaim policy, expansion and default-class annotation, every PriorityClass's value, default flag and description, both ClusterRoleBindings' role and subjects, the ServiceAccount, the three namespaces, both CRDs' group, kind, scope and versions, and all five VPAs' targetRef and updatePolicy - matches stock's cold deploy on the same cluster object by object, labels never compared. BREAK=1 drops io1-expand from the expected inventory and the match correctly fails"
fi
( green apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || log "  note: greenfield teardown did not exit clean; the cluster is deleted below regardless"

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
    estate = "$ESTATE-strict"
    record_store "local" {
      path = ".tofu-records"
    }
    strict {
      secrets          = "$1"
      no_source_create = "refuse"
      marker_repair    = "never"
      markers "record" {
        types = ["kubernetes_priority_class"]
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
( cd "$STRICT" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "choudoufu init for the strict-stage scratch estate failed"
STRICT_ON="$(cd "$STRICT" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" "$TOFU" plan -input=false -no-color 2>&1)"; STRICT_ON_RC=$?
if [ "${BREAK_STRICT:-}" = "1" ]; then
  strict_block "store" > "$STRICT/main.tf"
  STRICT_OFF="$(cd "$STRICT" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" "$TOFU" plan -input=false -no-color 2>&1)"; STRICT_OFF_RC=$?
  [ "$STRICT_OFF_RC" -eq 0 ] || { printf '%s\n' "$STRICT_OFF" | tail -20; fail "BREAK_STRICT=1: the plan with secrets = \"store\" exited $STRICT_OFF_RC"; }
  grep -q "^Error:" <<< "$STRICT_OFF" && fail "BREAK_STRICT=1: turning secrets off did not clear every refusal"
  grep -qF 'random_password.db will be created' <<< "$STRICT_OFF" || fail "BREAK_STRICT=1: the plan with secrets = \"store\" does not propose creating random_password.db"
  gauntlet_stage strict pass "BREAK_STRICT=1 control: with secrets back to \"store\" the refusal is gone and the plan is an ordinary create"
else
  [ "$STRICT_ON_RC" -eq 1 ] || { printf '%s\n' "$STRICT_ON" | tail -20; fail "the every-toggle-on plan exited $STRICT_ON_RC, not the refusal's usual 1"; }
  [ "$(grep -c '^Error:' <<< "$STRICT_ON")" -eq 1 ] || { printf '%s\n' "$STRICT_ON"; fail "every strict toggle on refused more than one thing"; }
  grep -qF 'Error: Logical resource is not admitted' <<< "$STRICT_ON" || { printf '%s\n' "$STRICT_ON"; fail "the one refusal is not \"Logical resource is not admitted\""; }
  grep -qF 'strict { secrets = "refuse" }' <<< "$STRICT_ON" || { printf '%s\n' "$STRICT_ON"; fail "the refusal does not cite strict { secrets = \"refuse\" }"; }
  gauntlet_stage strict pass "every strict toggle on (secrets = refuse, no_source_create = refuse, marker_repair = never with a markers \"record\" selection naming kubernetes_priority_class) against a scratch estate carrying random_password.db: exactly one refusal, Logical resource is not admitted under strict { secrets = \"refuse\" }; the other two toggles are on and silent. BREAK_STRICT=1 turns secrets back to \"store\" and the refusal disappears"
fi
gauntlet_end_stage

gauntlet_end
log "$ESTATE: done"
