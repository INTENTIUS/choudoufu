#!/usr/bin/env bash
# corpus-govuk-cluster-access: typed RBAC through a module called eight
# times (#1879, part of #1885), crossed on the kind substrate.
# alphagov/govuk-infrastructure's own terraform/deployments/cluster-access
# root at commit c02504fa4abb439234669e9ea9b662bdb35209e9 (MIT, already
# pinned in live/corpus-manifest.json): GOV.UK's platform team's cluster
# access layer. Three namespaces, then `module "access-entry"` called eight
# times (platformengineer, fulladmin, developer, licensinguser, ithctester,
# readonly, tempadmin, dguengineer), each declaring a ClusterRole whose
# rules come from a `dynamic "rule"` block, its ClusterRoleBinding, and -
# for the four callers that pass namespace_role_rules - a Role and a
# RoleBinding per namespace through `for_each`. 31 hashicorp/kubernetes
# instances over five kinds once the deltas below drop the EKS access
# entries:
#
#   kubernetes_namespace_v1             x3   apps, licensify, datagovuk
#   kubernetes_cluster_role_v1          x8   one per module instance
#   kubernetes_cluster_role_binding_v1  x8   one per module instance
#   kubernetes_role_v1                  x6   developer[apps,licensify],
#   kubernetes_role_binding_v1          x6   licensinguser[licensify],
#                                            readonly[apps,licensify],
#                                            dguengineer[datagovuk]
#
# What the lane gains (#1879): before this estate the only typed RBAC object
# any kind estate declared was quickpizza's one ClusterRoleBinding. This
# root reaches cluster-scoped and namespaced RBAC kinds through module
# instances, with for_each keys inside them, so identity resolution and the
# address annotation are measured on addresses like
# module.developer.kubernetes_role_binding_v1.namespace_role["licensify"].
#
# Deltas, each asserted so a moved pin fails loudly rather than silently,
# and applied identically to every root (stock's, the oracle's, choudoufu's):
#   1. the `cloud {}` block naming GOV.UK's Terraform Cloud organisation
#      goes, as on every other govuk-infrastructure crossing (#268);
#   2. the tfe and AWS provider requirements, the AWS provider block, the
#      eight data "aws_iam_roles" lookups, data "aws_eks_cluster_auth",
#      and remote.tf's data "aws_region", data "aws_caller_identity" and
#      data "tfe_outputs" go: they exist to reach an EKS cluster and its
#      IAM principals, and on kind the cluster is the run's own;
#   3. the provider "kubernetes" block becomes empty, so KUBE_CONFIG_PATH
#      names the run's kind cluster (it read the endpoint, CA and token from
#      data.tfe_outputs and data.aws_eks_cluster_auth);
#   4. local.cluster_name, read from data.tfe_outputs, reads var.cluster_name
#      instead (variables-common.tf's own default, "govuk"), and each module
#      call's aws_iam_role_arns, read from data.aws_iam_roles, becomes [];
#   5. modules/access-entry loses aws_eks_access_entry and
#      aws_eks_access_policy_association, the EKS half of each instance;
#   6. variables-common.tf, a symlink in the repository, is copied as the
#      file it points at, and the integration environment's own published
#      var files (terraform/variables/integration/common.tfvars and
#      cluster-access.tfvars, the layout live/corpus-manifest.json records)
#      are copied in beside it as *.auto.tfvars, so every required variable
#      has GOV.UK's own value and none is invented here;
#   7. choudoufu's roots get a live block appended to main.tf.
#
# A note for a later floci-eks variant (#1879): with deltas 2 to 5 undone
# and the access entries kept, this root is the mixed-estate shape
# live/kubernetes/COMPATIBILITY.md recommends in place of aws-auth - EKS
# access entries on the AWS leg mapping IAM roles to the Kubernetes groups
# the RBAC leg binds. That variant needs floci's EKS real mode
# (SubstrateFlociEKS), not kind.
#
# And three additions, each for one stage and each in a file of its own,
# because the published shape has none of them: day2_count declares a
# two-instance count ConfigMap (stage 9), day2_crash a Secret and a
# ConfigMap where the second reads the first's name (stage 10), and the
# shared kind bodies in live/e2e/lib/gauntlet.sh add and remove their own
# create_before_destroy ConfigMaps. All of them live in the apps namespace.
#
# The estate boundary (#1066, live/kubernetes/GATE.md) is installed on
# cluster A before the first labelled write, so every write choudoufu makes
# from migrate on is judged by it, and drift_reconverge measures it judging
# an RBAC write: the same patch to a ClusterRole is refused by admission
# under a ServiceAccount that holds RBAC rights to make it but not the
# estate, and admitted once that ServiceAccount is granted the estate with
# live/kubernetes/estate-grant.yaml - that admitted patch is the stage's
# out-of-band drift.
#
# .corpus is read, never written: the root is copied out per run. Two kind
# clusters, both created for the run: A holds the estate (stock cold-deploys
# it, choudoufu adopts it and runs every day-2 stage, tears it down, then
# applies the same shape fresh with a live block); B is the oracle, where
# stock applies the identical root and every day-2 change itself.
#
#   go run ./tools/gauntlet run corpus-govuk-cluster-access   # one estate, local kind clusters
#   bash live/e2e/corpus-govuk-cluster-access/run.sh
#
# Needs `just corpus-fetch` first, then kind, kubectl, terraform (the stock
# binary), python3 and Docker on PATH. No images are pulled: the estate
# declares no workload.
#
# Env overrides:
#   TOFU_BIN       path to a prebuilt choudoufu binary; skips the `go build`.
#   BREAK          set to 1 to run test_plan's, drift_reconverge's,
#                  day2_rename's and greenfield's negative controls: one
#                  expected identity corrupted (exactly that one must
#                  mismatch); a second ClusterRole tampered (the
#                  single-object assertion must fail); the readonly module
#                  call's own name argument changed (an identity change; the
#                  zero-churn assertion must fail); the ithctester
#                  ClusterRole dropped from greenfield's expected inventory
#                  (the match must fail).
#   BREAK_REMOVE   keep the licensinguser module block; no destroy may be
#                  proposed.
#   BREAK_COUNT    assert the wrong instance was destroyed on the scale-down.
#   BREAK_APPROVAL apply the saved plan after the world moved and expect success.
#   BREAK_REPLACE  recreate day2_replace's old object after its apply; the
#                  next plan must propose destroying it.
#   BREAK_CRASH    after the same real interrupt, assert nothing is proposed;
#                  must fail.
#   BREAK_CRASH_UNBOUND
#                  strip the tofu-estate label off the object the
#                  interrupted apply did create, and the recovery check
#                  must fail.
#   BREAK_STRICT   turn secrets back to "store"; the refusal must vanish.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# shellcheck source=live/e2e/lib/gauntlet.sh
source "$ROOT/live/e2e/lib/gauntlet.sh"

# The shared provider plugin cache, and the cross-process lock real terraform
# needs in order to use it safely (#1300). live/e2e/lib/gauntlet.sh carries the
# measured reasons for both; this is the only place a script chooses either.
gauntlet_plugin_cache
ESTATE="corpus-govuk-cluster-access"
NS="apps" # where the script's own additions live; the estate declares it
KINDS="namespaces clusterroles clusterrolebindings roles rolebindings configmaps secrets"
PIN_COMMIT="c02504fa4abb439234669e9ea9b662bdb35209e9"
SRC="$ROOT/.corpus/govuk-infrastructure"
SRC_ROOT="$SRC/terraform/deployments/cluster-access"
SRC_VARS="$SRC/terraform/variables/integration"
BOUNDARY="$ROOT/live/kubernetes/estate-boundary.yaml"
GRANT="$ROOT/live/kubernetes/estate-grant.yaml"
WORK="$(mktemp -d)"
STOCK="$WORK/stock"; ADOPTED="$WORK/adopted"; ORACLE="$WORK/oracle"; GREEN="$WORK/green"
KCA="$WORK/a.kubeconfig"; KCB="$WORK/b.kubeconfig"
CLUSTER_A="chdf-gca-a-$$"; CLUSTER_B="chdf-gca-b-$$"
ANN="choudoufu.intentius.io/tofu-address"
# The eight module calls in eks_access.tf, in file order, and the four that
# pass namespace_role_rules with the namespaces their for_each covers.
MODULES="platformengineer fulladmin developer licensinguser ithctester readonly tempadmin dguengineer"
INSTANCES=31
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
[ -d "$SRC_ROOT" ] || fail "$SRC_ROOT is missing - run \`just corpus-fetch\` first"
PIN="$(git -C "$SRC" rev-parse HEAD 2>/dev/null)"
[ "$PIN" = "$PIN_COMMIT" ] || fail "the fetched govuk-infrastructure is at $PIN, not the pinned $PIN_COMMIT - run \`just corpus-fetch\`"
[ -f "$SRC_VARS/common.tfvars" ] && [ -f "$SRC_VARS/cluster-access.tfvars" ] \
  || fail "the integration environment's var files are missing under $SRC_VARS - the corpus pin has moved"

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
# whatever $TOFU came from; identical to $TOFU everywhere else, because the
# hook is a no-op unless the variable names an applied address.
mkdir -p "$WORK/bin"
TOFU_CRASH="$WORK/bin/choudoufu-e2e"
( cd "$ROOT" && env -u PWD go build -ldflags="-X 'main.e2eTestingFeatures=yes'" -o "$TOFU_CRASH" ./cmd/choudoufu ) \
  || fail "go build -ldflags e2eTestingFeatures ./cmd/choudoufu failed"
log "  built $TOFU_CRASH (e2eTestingFeatures=yes, for day2_crash's interrupt)"

# ── the root, copied out of .corpus with its deltas ──────────────────────
# write_root copies deployments/cluster-access into $1/root, dereferencing
# the variables-common.tf symlink, applies deltas 1 to 6, and delta 7 when
# $2 is "live". The root directory is $1/root.
write_root() {
  local dst="$1" mode="$2"
  rm -rf "$dst"; mkdir -p "$dst"
  cp -RL "$SRC_ROOT" "$dst/root" || fail "could not copy the root out of .corpus"
  # A working directory someone once initialised inside .corpus is not part
  # of the published root.
  rm -rf "$dst/root/.terraform" "$dst/root/.terraform.lock.hcl"
  [ -f "$dst/root/variables-common.tf" ] && [ ! -L "$dst/root/variables-common.tf" ] \
    || fail "delta 6: variables-common.tf did not copy as a regular file"
  python3 - "$dst/root" <<'PY' || fail "the deltas did not apply - the corpus pin has moved (the assertion above says which)"
import re, sys
root = sys.argv[1]
def edit(rel, subs, forbid=()):
    p = root + "/" + rel
    s = open(p).read()
    for what, pattern, repl, want in subs:
        s, n = re.subn(pattern, repl, s, flags=re.S | re.M)
        assert n == want, "%s: %s matched %d time(s), want %d" % (rel, what, n, want)
    for word in forbid:
        assert word not in s, "%s still mentions %r after the deltas" % (rel, word)
    open(p, "w").write(s)

edit("main.tf", [
    ("delta 1, the cloud block", r'\n  cloud \{\n    organization = "govuk"\n.*?\n  \}\n', "\n", 1),
    ("delta 2, the tfe requirement", r'    tfe = \{\n[^}]*?\n    \}\n', "", 1),
    ("delta 2, the AWS requirement and its comment", r'    # The AWS provider is only used here.*?\n    aws = \{\n[^}]*?\n    \}\n', "", 1),
    ("delta 2, the AWS provider block", r'^provider "aws" \{\n.*?\n\}\n\n', "", 1),
    ("delta 2, the IAM role lookups", r'^data "aws_iam_roles" "[a-z]+" \{\n[^\n]*\n\}\n\n', "", 8),
    ("delta 2, the cluster token", r'^data "aws_eks_cluster_auth" "cluster_token" \{\n.*?\n\}\n\n', "", 1),
    ("delta 3, the kubernetes provider block", r'^provider "kubernetes" \{\n.*?\n\}\n',
     'provider "kubernetes" {} # delta 3: KUBE_CONFIG_PATH names the run\'s kind cluster\n', 1),
    ("delta 4, local.cluster_name", r'cluster_name = data\.tfe_outputs\.cluster_infrastructure\.nonsensitive_values\.cluster_id',
     'cluster_name = var.cluster_name # delta 4', 1),
], forbid=("tfe", "aws"))
edit("remote.tf", [
    ("delta 2, the remote data sources", r'^data "(aws_region|aws_caller_identity|tfe_outputs)" "[a-z_]+" \{(\}|\n.*?\n\})\n*', "", 3),
], forbid=("data ",))
edit("eks_access.tf", [
    ("delta 4, the IAM role ARNs", r'aws_iam_role_arns(\s+)= data\.aws_iam_roles\.[a-z]+\.arns', r'aws_iam_role_arns\1= [] # delta 4', 8),
], forbid=("data.",))
edit("modules/access-entry/main.tf", [
    ("delta 5, the access entry", r'^resource "aws_eks_access_entry" "entry" \{\n.*?\n\}\n\n', "", 1),
    ("delta 5, the access policy association", r'^resource "aws_eks_access_policy_association" "entry" \{\n.*?\n\}\n\n', "", 1),
], forbid=("aws_",))
PY
  cp "$SRC_VARS/common.tfvars" "$dst/root/common.auto.tfvars" || fail "delta 6: could not copy common.tfvars"
  cp "$SRC_VARS/cluster-access.tfvars" "$dst/root/cluster-access.auto.tfvars" || fail "delta 6: could not copy cluster-access.tfvars"
  local n
  n="$(grep -hc '^module "' "$dst/root/eks_access.tf")"
  [ "$n" = "8" ] || fail "eks_access.tf declares $n module calls, want 8 - the corpus pin has moved"
  if [ "$mode" = "live" ]; then
    cat >> "$dst/root/main.tf" <<EOF

terraform {
  live {
    estate = "$ESTATE"
    record_store "local" {
      path = ".tofu-records"
    }
  }
}
EOF
    grep -q "estate = \"$ESTATE\"" "$dst/root/main.tf" || fail "delta 7 did not land in main.tf"
  fi
}

# ── cluster helpers ──────────────────────────────────────────────────────
# kc_as is every kubectl call this script makes, against one kubeconfig and
# with a bound on each request, as live/smoke/lib.sh's kc_as is.
kc_as() { local cfg="$1"; shift; kubectl --kubeconfig "$cfg" --request-timeout=60s "$@"; }
kca() { kc_as "$KCA" "$@"; }
kcb() { kc_as "$KCB" "$@"; }
stock_b() { ( cd "$ORACLE/root" && KUBECONFIG="$KCB" KUBE_CONFIG_PATH="$KCB" terraform "$@" ); }
chdf_a() { local dir="$1"; shift; ( cd "$dir/root" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" "$TOFU" "$@" ); }
# $KINDS is a word list on purpose: one argument per kind.
# shellcheck disable=SC2086
count_a() { KUBECONFIG="$KCA" gauntlet_kind_count "$ESTATE" $KINDS; }
exists_a() { kca get "$1" "$2" -n "$NS" >/dev/null 2>&1; }
plan_line() { grep -E '^Plan:|^No changes' <<< "$1" | head -1 | sed 's/\.$//'; }

# expected_ids prints the estate's 31 RBAC and namespace objects, one per
# line, as "kind namespace name address" ("-" for a cluster-scoped object),
# where address is the escaped instance address the object's
# choudoufu.intentius.io/tofu-address annotation must carry
# (markers.EscapeAddress: an instance key ["k"] becomes :k). $1 is the name
# the readonly module call goes by (readonly until day2_rename, viewer
# after); $2, when "removed", leaves out the licensinguser instance
# day2_remove deletes.
expected_ids() {
  local readonly_as="$1" removed="${2:-}" m addr ns
  for ns in apps licensify datagovuk; do printf 'namespaces - %s kubernetes_namespace_v1.%s\n' "$ns" "$ns"; done
  for m in $MODULES; do
    [ "$removed" = "removed" ] && [ "$m" = "licensinguser" ] && continue
    addr="module.$m"; [ "$m" = "readonly" ] && addr="module.$readonly_as"
    printf 'clusterroles - %s %s.kubernetes_cluster_role_v1.cluster_role\n' "$m" "$addr"
    printf 'clusterrolebindings - %s-binding %s.kubernetes_cluster_role_binding_v1.cluster_role\n' "$m" "$addr"
    case "$m" in
      developer|readonly) set -- apps licensify ;;
      licensinguser) set -- licensify ;;
      dguengineer) set -- datagovuk ;;
      *) set -- ;;
    esac
    for ns in "$@"; do
      printf 'roles %s %s %s.kubernetes_role_v1.namespace_role:%s\n' "$ns" "$m" "$addr" "$ns"
      printf 'rolebindings %s %s-binding %s.kubernetes_role_binding_v1.namespace_role:%s\n' "$ns" "$m" "$addr" "$ns"
    done
  done
}

# identity_mismatches reads the file of expected identities $2 against
# cluster $1, comparing by value: the object exists by NAMESPACE/NAME (or
# NAME), carries tofu-estate=$ESTATE, and carries the expected address
# annotation. It prints one line per mismatch and nothing when every
# identity matches; a kubectl failure is printed as a mismatch too, so an
# unreachable cluster can never read as a clean comparison.
identity_mismatches() {
  KUBECONFIG="$1" ESTATE="$ESTATE" ANN="$ANN" python3 - "$2" <<'PY'
import json, os, subprocess, sys
est, ann = os.environ["ESTATE"], os.environ["ANN"]
cache = {}
def objs(kind):
    if kind not in cache:
        out = subprocess.run(["kubectl", "get", kind, "-A", "-o", "json", "--request-timeout=60s"], capture_output=True, text=True)
        if out.returncode != 0:
            print("kubectl get %s failed: %s" % (kind, out.stderr.strip()))
            sys.exit(0)
        cache[kind] = {(o["metadata"].get("namespace", "-"), o["metadata"]["name"]): o for o in json.loads(out.stdout).get("items", [])}
    return cache[kind]
n = 0
for line in open(sys.argv[1]):
    f = line.split()
    if not f:
        continue
    kind, ns, name, addr = f
    n += 1
    where = kind + " " + (name if ns == "-" else ns + "/" + name)
    o = objs(kind).get((ns, name))
    if o is None:
        print(where + ": not found")
        continue
    md = o["metadata"]
    label = (md.get("labels") or {}).get("tofu-estate", "")
    got = (md.get("annotations") or {}).get(ann, "")
    if label != est:
        print("%s: tofu-estate=%r, want %r" % (where, label, est))
    if got != addr:
        print("%s: %s=%r, want %r" % (where, ann, got, addr))
if n == 0:
    print("no expected identity was read - the comparison checked nothing")
PY
}

# inventory prints the estate's objects on cluster $1, normalised to what
# the configuration declares - the RBAC objects' rules, role references and
# subjects, and every object's own labels and annotations with the marker
# label and this fork's annotations taken out - so stock's cold deploy and
# choudoufu's greenfield apply compare object by object. Objects are found
# by the app.kubernetes.io/managed-by=Terraform label every block in the
# root sets. $2 is a "kind/name" to drop (BREAK's control).
inventory() {
  KUBECONFIG="$1" DROP="${2:-}" python3 - <<'PY'
import json, os, subprocess
drop = os.environ.get("DROP", "")
def items(kind):
    out = subprocess.run(["kubectl", "get", kind, "-A", "-l", "app.kubernetes.io/managed-by=Terraform", "-o", "json", "--request-timeout=60s"],
                         capture_output=True, text=True)
    if out.returncode != 0:
        raise SystemExit("kubectl get %s failed: %s" % (kind, out.stderr.strip()))
    return json.loads(out.stdout).get("items", [])
def meta(o):
    md = o["metadata"]
    labels = {k: v for k, v in (md.get("labels") or {}).items() if k != "tofu-estate"}
    anns = {k: v for k, v in (md.get("annotations") or {}).items() if "choudoufu" not in k}
    return {"labels": labels, "annotations": anns}
def rules(o):
    return sorted(json.dumps(r, sort_keys=True) for r in (o.get("rules") or []))
def key(kind, o):
    md = o["metadata"]
    return kind + "/" + ((md["namespace"] + "/") if md.get("namespace") else "") + md["name"]
inv = {}
for o in items("namespaces"):
    inv[key("namespace", o)] = meta(o)
for kind in ("clusterroles", "roles"):
    for o in items(kind):
        inv[key(kind[:-1], o)] = dict(meta(o), rules=rules(o))
for kind in ("clusterrolebindings", "rolebindings"):
    for o in items(kind):
        inv[key(kind[:-1], o)] = dict(meta(o), roleRef=o.get("roleRef"), subjects=o.get("subjects"))
if drop:
    inv.pop(drop, None)
print(json.dumps(inv, sort_keys=True, indent=1))
PY
}

# boundary_observed holds once the API server has observed the installed
# policy's current generation, with no type-check warning: the two fields
# live/smoke/scenarios/k8s-the-label-is-the-boundary.sh reads.
boundary_observed() {
  local obs gen tc
  obs="$(kca get validatingadmissionpolicy choudoufu-estate-boundary -o jsonpath='{.status.observedGeneration}' 2>/dev/null)"
  gen="$(kca get validatingadmissionpolicy choudoufu-estate-boundary -o jsonpath='{.metadata.generation}' 2>/dev/null)"
  tc="$(kca get validatingadmissionpolicy choudoufu-estate-boundary -o jsonpath='{.status.typeChecking.expressionWarnings}' 2>/dev/null)"
  [ -n "$obs" ] && [ "$obs" = "$gen" ] && [ -z "$tc" ]
}
namespaces_gone_a() {
  local n
  for n in apps licensify datagovuk; do kca get namespace "$n" >/dev/null 2>&1 && return 1; done
  return 0
}

# ── 1. cold_deploy: stock stands the root up on A (and B, the oracle) ────
gauntlet_begin_stage cold_deploy
log "=== 1. cold_deploy: two kind clusters, stock terraform applies the published root on each ==="
gauntlet_kind_up "$CLUSTER_A" "$KCA" || fail "kind cluster A ($CLUSTER_A) did not come up"
gauntlet_kind_up "$CLUSTER_B" "$KCB" || fail "kind cluster B ($CLUSTER_B) did not come up"
write_root "$STOCK" stock
write_root "$ORACLE" stock
( cd "$STOCK/root" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" gauntlet_locked_init terraform init -input=false -no-color > "$WORK/init.stock-a.log" 2>&1 ) \
  || { tail -20 "$WORK/init.stock-a.log"; fail "stock init failed on A"; }
COLD_OUT="$(cd "$STOCK/root" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" terraform apply -auto-approve -input=false -no-color 2>&1)" \
  || { printf '%s\n' "$COLD_OUT" | tail -20; fail "stock cold deploy failed on A"; }
grep -qF "Apply complete! Resources: $INSTANCES added, 0 changed, 0 destroyed" <<< "$COLD_OUT" \
  || { printf '%s\n' "$COLD_OUT" | tail -5; fail "stock cold deploy did not add exactly $INSTANCES objects on A"; }
STOCK_N="$(cd "$STOCK/root" && terraform state list | wc -l | tr -d ' ')"
[ "$STOCK_N" = "$INSTANCES" ] || fail "stock's state holds $STOCK_N instances, want $INSTANCES"
STOCK_MOD_N="$(cd "$STOCK/root" && terraform state list | grep -c '^module\.')"
[ "$STOCK_MOD_N" = "28" ] || fail "stock's state holds $STOCK_MOD_N instances under module calls, want 28 (8 ClusterRoles, 8 ClusterRoleBindings, 6 Roles, 6 RoleBindings)"
UNMARKED="$(count_a)"
[ "$UNMARKED" = "0" ] || fail "$UNMARKED object(s) already carry tofu-estate=$ESTATE after a plain stock apply"
inventory "$KCA" > "$WORK/inventory.stock.json" || fail "could not read the cold-deployed inventory on A"
INV_N="$(python3 -c 'import json,sys; print(len(json.load(open(sys.argv[1]))))' "$WORK/inventory.stock.json")"
[ "$INV_N" = "$INSTANCES" ] || fail "the cold-deployed inventory on A holds $INV_N objects carrying app.kubernetes.io/managed-by=Terraform, want $INSTANCES"
( cd "$ORACLE/root" && KUBECONFIG="$KCB" KUBE_CONFIG_PATH="$KCB" gauntlet_locked_init terraform init -input=false -no-color > "$WORK/init.stock-b.log" 2>&1 ) \
  || { tail -20 "$WORK/init.stock-b.log"; fail "stock init failed on B"; }
COLD_B="$(stock_b apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$COLD_B" | tail -20; fail "stock cold deploy failed on B"; }
grep -qF "Apply complete! Resources: $INSTANCES added" <<< "$COLD_B" || fail "stock cold deploy did not add exactly $INSTANCES objects on B"
gauntlet_stage cold_deploy pass "$INSTANCES objects (3 Namespaces, then module \"access-entry\" called eight times: 8 ClusterRoles with dynamic rule blocks, 8 ClusterRoleBindings, 6 Roles and 6 RoleBindings through for_each over namespaces) from plain terraform on the published root plus its deltas (Terraform Cloud block, EKS access entries and the AWS and tfe data sources dropped, provider pointed at the run's cluster, GOV.UK's own integration var files), against kind $(kca version 2>/dev/null | gauntlet_k8s_server_version); a real terraform.tfstate with $INSTANCES instances, 28 of them under module calls, zero tofu-estate labels, $INV_N objects inventoried with kubectl; the identical root cold-deployed by stock on a second cluster as every later stage's oracle"

# ── 2. migrate ────────────────────────────────────────────────────────────
gauntlet_begin_stage migrate
log "=== 2. migrate: the estate boundary installed on A, then live-import against the stock state file ==="
kca apply -f "$BOUNDARY" >/dev/null || fail "could not install live/kubernetes/estate-boundary.yaml on A"
gauntlet_wait_until 60 "the estate boundary observed by the API server on A" -- boundary_observed \
  || fail "the API server on A never observed the estate boundary's current generation without a type-check warning"
write_root "$ADOPTED" live
( chdf_a "$ADOPTED" init -input=false -no-color > "$WORK/init.adopted.log" 2>&1 ) || { tail -20 "$WORK/init.adopted.log"; fail "adopted init failed"; }
IMPORT_OUT="$(chdf_a "$ADOPTED" live-import -state="$STOCK/root/terraform.tfstate" -estate="$ESTATE" -no-color 2>&1)" || { printf '%s\n' "$IMPORT_OUT" | tail -20; fail "live-import (dry run) failed"; }
log "  dry run: $(grep -E 'resource instance\(s\) are eligible for stamping' <<< "$IMPORT_OUT" | head -1)"
APPROVE_OUT="$(chdf_a "$ADOPTED" live-import -state="$STOCK/root/terraform.tfstate" -estate="$ESTATE" -approve -no-color 2>&1)" || { printf '%s\n' "$APPROVE_OUT" | tail -20; fail "live-import -approve failed"; }
SUMMARY_LINE="$(grep -E 'resource\(s\) newly stamped' <<< "$APPROVE_OUT" | head -1 | sed 's/\.$//')"
log "  approve: ${SUMMARY_LINE:-no summary line}"
if grep -qF "31 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 0 skipped." <<< "$APPROVE_OUT"; then
  LABELLED="$(count_a)"
  [ "$LABELLED" = "$INSTANCES" ] || fail "live-import reported $INSTANCES stamped but $LABELLED object(s) carry tofu-estate=$ESTATE"
  gauntlet_stage migrate pass "$INSTANCES of $INSTANCES stamped, 0 skipped, from the stock state file, 28 of them addressed through the eight module instances and 12 through for_each keys inside them; every object carries tofu-estate=$ESTATE, read back with kubectl across the estate's five kinds; every write was judged by the estate boundary (live/kubernetes/estate-boundary.yaml), installed on the cluster first and observed by the API server"
else
  gauntlet_stage migrate fail "live-import -approve did not stamp all $INSTANCES cleanly: ${SUMMARY_LINE:-no summary line}"
fi

# ── 3. test_plan ──────────────────────────────────────────────────────────
gauntlet_begin_stage test_plan
log "=== 3. test_plan: choudoufu plan with no state file; every identity compared by value with kubectl ==="
PLAN_OUT="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$PLAN_OUT" | tail -20; fail "the post-migration plan failed"; }
expected_ids readonly > "$WORK/ids.expected"
[ "$(wc -l < "$WORK/ids.expected" | tr -d ' ')" = "$INSTANCES" ] || fail "expected_ids printed $(wc -l < "$WORK/ids.expected" | tr -d ' ') identities, want $INSTANCES"
if [ "${BREAK:-}" = "1" ]; then
  awk '!d && /namespace_role:licensify$/ {sub(/licensify$/, "licensifx"); d=1} {print}' "$WORK/ids.expected" > "$WORK/ids.broken"
  BROKEN="$(identity_mismatches "$KCA" "$WORK/ids.broken")"
  B_N="$(grep -c . <<< "$BROKEN")"
  if [ "$B_N" != "1" ] || ! grep -q 'licensifx' <<< "$BROKEN"; then
    printf '%s\n' "$BROKEN"; fail "BREAK=1: one corrupted expected identity produced $B_N mismatch(es), want exactly the one corrupted"
  fi
  log "  BREAK=1: caught - exactly one mismatch: $BROKEN"
  gauntlet_stage test_plan pass "BREAK=1 control: one expected identity corrupted (a for_each key inside a module instance's address) and the by-value comparison fails on that string and nothing else: $BROKEN"
else
  MISMATCH="$(identity_mismatches "$KCA" "$WORK/ids.expected")"
  if grep -q "No changes." <<< "$PLAN_OUT" && [ -z "$MISMATCH" ]; then
    gauntlet_stage test_plan pass "the plan with no state file is empty; all $INSTANCES identities compared by value with kubectl - each object found by NAMESPACE/NAME (NAME for the 3 Namespaces, 8 ClusterRoles and 8 ClusterRoleBindings), carrying tofu-estate=$ESTATE and the escaped instance address in $ANN, module.<call>.<type>.<name> for the 28 under module calls and module.<call>.<type>.namespace_role:<namespace> for the 12 reached through for_each inside them. BREAK=1 corrupts one expected address and the comparison fails on exactly that one"
  else
    printf '%s\n' "$MISMATCH" | head -20
    gauntlet_stage test_plan fail "the plan with no state file is not empty or an identity does not match by value ($(grep -c . <<< "$MISMATCH") mismatch(es), first: $(head -1 <<< "$MISMATCH")): $(plan_line "$PLAN_OUT")"
    ADOPT_OUT="$(chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$ADOPT_OUT" | tail -20; fail "the converging apply failed"; }
    grep -q "No changes." <<< "$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || fail "the replan after the converging apply is not empty"
  fi
fi

# ── 4. test_apply ─────────────────────────────────────────────────────────
gauntlet_begin_stage test_apply
log "=== 4. test_apply: apply the empty plan; the labelled-object count must not move ==="
BEFORE_N="$(count_a)"
NOOP_OUT="$(chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$NOOP_OUT" | tail -20; fail "the no-op apply failed"; }
grep -qF "Apply complete! Resources: 0 added, 0 changed, 0 destroyed" <<< "$NOOP_OUT" || { printf '%s\n' "$NOOP_OUT" | tail -5; fail "the no-op apply changed something"; }
AFTER_N="$(count_a)"
[ "$BEFORE_N" = "$AFTER_N" ] || fail "labelled-object count moved across a no-op apply: $BEFORE_N -> $AFTER_N"
[ "$AFTER_N" = "$INSTANCES" ] || fail "$AFTER_N labelled objects after the no-op apply, want $INSTANCES"
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed); objects carrying tofu-estate=$ESTATE unchanged at $BEFORE_N across five kinds, counted with kubectl"

# ── 5. drift_reconverge, with the estate boundary judging an RBAC write ──
gauntlet_begin_stage drift_reconverge
log "=== 5. drift_reconverge: the ithctester ClusterRole's rules replaced out of band; the boundary judges the write first ==="
TAMPER='[{"op":"replace","path":"/rules","value":[{"apiGroups":[""],"resources":["pods"],"verbs":["get"]}]}]'
rule_count() { kc_as "$1" get clusterrole "$2" -o json 2>/dev/null | python3 -c 'import json,sys; print(len(json.load(sys.stdin).get("rules") or []))'; }
ITHC_RULES="$(rule_count "$KCA" ithctester)"
[ "$ITHC_RULES" = "5" ] || fail "the ithctester ClusterRole carries $ITHC_RULES rule(s) before the tamper, want the 5 eks_access.tf declares"

# The boundary probe. A ServiceAccount with ordinary RBAC to patch
# ClusterRoles - escalate included, so the API server's own escalation
# check cannot be what refuses - and without the estate: the patch must be
# refused by admission, in the policy's own words, and leave the object as
# it was. Then the estate is granted with live/kubernetes/estate-grant.yaml
# and the identical patch is admitted. That admitted patch is the drift.
EDITOR="gauntlet-rbac-editor"; EDITOR_USER="system:serviceaccount:default:$EDITOR"
kca apply -f - >/dev/null <<EOF || fail "could not create the $EDITOR ServiceAccount and its RBAC on A"
apiVersion: v1
kind: ServiceAccount
metadata:
  name: $EDITOR
  namespace: default
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: $EDITOR
rules:
  - apiGroups: ["rbac.authorization.k8s.io"]
    resources: ["clusterroles"]
    verbs: ["get", "list", "watch", "patch", "update", "escalate"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: $EDITOR
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: $EDITOR
subjects:
  - kind: ServiceAccount
    name: $EDITOR
    namespace: default
EOF
editor_can_patch() { [ "$(kca auth can-i patch clusterroles --as="$EDITOR_USER" 2>/dev/null)" = "yes" ] && [ "$(kca auth can-i escalate clusterroles --as="$EDITOR_USER" 2>/dev/null)" = "yes" ]; }
gauntlet_wait_until 30 "RBAC to patch and escalate ClusterRoles for $EDITOR_USER" -- editor_can_patch \
  || fail "$EDITOR_USER never held patch and escalate on clusterroles, so a refusal below could be RBAC's and not the boundary's"
DENY_OUT="$(kca --as="$EDITOR_USER" patch clusterrole ithctester --type json -p "$TAMPER" 2>&1)"; DENY_RC=$?
[ "$DENY_RC" -ne 0 ] || fail "the estate boundary admitted an RBAC write by $EDITOR_USER, which does not hold $ESTATE: the ithctester ClusterRole was patched"
grep -qF "ValidatingAdmissionPolicy 'choudoufu-estate-boundary'" <<< "$DENY_OUT" \
  || fail "the patch by $EDITOR_USER failed, but not as a refusal from ValidatingAdmissionPolicy 'choudoufu-estate-boundary': $DENY_OUT"
grep -qF "is not bound to that estate" <<< "$DENY_OUT" \
  || fail "the boundary refused the patch by $EDITOR_USER, but not in the policy's own words for the estate an object belongs to: $DENY_OUT"
[ "$(rule_count "$KCA" ithctester)" = "5" ] || fail "the ithctester ClusterRole's rules moved although the boundary refused the write"
DENY_LINE="$(grep -o 'denied request: .*' <<< "$DENY_OUT" | head -1)"
log "  boundary: refused for $EDITOR_USER - ${DENY_LINE:-$DENY_OUT}"
sed -e "s/ESTATE/$ESTATE/g" -e 's/PRINCIPAL_NAMESPACE/default/g' -e "s/PRINCIPAL/$EDITOR/g" "$GRANT" | kca apply -f - >/dev/null \
  || fail "could not grant $ESTATE to $EDITOR_USER with live/kubernetes/estate-grant.yaml"
editor_tamper() { kca --as="$EDITOR_USER" patch clusterrole ithctester --type json -p "$TAMPER"; }
gauntlet_wait_until 30 "the patch by $EDITOR_USER admitted once the estate is granted" -- editor_tamper \
  || fail "with $ESTATE granted to $EDITOR_USER the boundary still refused the same patch"
[ "$(rule_count "$KCA" ithctester)" = "1" ] || fail "the admitted patch did not leave the ithctester ClusterRole with its one tampered rule"
kcb patch clusterrole ithctester --type json -p "$TAMPER" >/dev/null || fail "could not tamper the ithctester ClusterRole on B"
if [ "${BREAK:-}" = "1" ]; then
  kca patch clusterrole readonly --type json -p "$TAMPER" >/dev/null || fail "BREAK: could not tamper the readonly ClusterRole on A"
fi
ORACLE_PLAN="$(stock_b plan -detailed-exitcode -input=false -no-color 2>&1)"; ORACLE_RC=$?
[ "$ORACLE_RC" -eq 2 ] || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B after the tamper exited $ORACLE_RC, want 2"; }
grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$ORACLE_PLAN" || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B does not propose exactly one change"; }
grep -qF "module.ithctester.kubernetes_cluster_role_v1.cluster_role will be updated in-place" <<< "$ORACLE_PLAN" || fail "stock's plan on B does not update module.ithctester.kubernetes_cluster_role_v1.cluster_role"
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock could not reconverge B"
DRIFT_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$DRIFT_PLAN" | tail -20; fail "the plan after the tamper failed"; }
if [ "${BREAK:-}" = "1" ]; then
  grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$DRIFT_PLAN" && fail "BREAK=1: two ClusterRoles were tampered but the plan still proposes exactly one change"
  log "  BREAK=1: caught - with a second ClusterRole tampered the plan is $(plan_line "$DRIFT_PLAN")"
  ( chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: could not reconverge A"
  gauntlet_stage drift_reconverge pass "BREAK=1 control: with two ClusterRoles tampered the single-object assertion correctly fails to hold ($(plan_line "$DRIFT_PLAN")); reconverged afterwards. The boundary probe ran as in the real check: ${DENY_LINE:-refused}"
else
  grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$DRIFT_PLAN" || { printf '%s\n' "$DRIFT_PLAN" | tail -20; fail "the plan after one tamper does not propose exactly one change"; }
  grep -qF "module.ithctester.kubernetes_cluster_role_v1.cluster_role will be updated in-place" <<< "$DRIFT_PLAN" || fail "the plan does not update module.ithctester.kubernetes_cluster_role_v1.cluster_role"
  RECONV="$(chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$RECONV" | tail -20; fail "the reconverging apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$RECONV" || fail "the reconverging apply did not change exactly one object"
  [ "$(rule_count "$KCA" ithctester)" = "5" ] || fail "the ithctester ClusterRole does not carry its 5 declared rules after reconverging"
  grep -q "No changes." <<< "$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || fail "the replan after reconverging is not empty"
  gauntlet_stage drift_reconverge pass "the ithctester ClusterRole's dynamic rules replaced out of band with one rule (kubectl patch); choudoufu proposed exactly module.ithctester.kubernetes_cluster_role_v1.cluster_role (0 add, 1 change, 0 destroy), matching stock's own plan on the oracle cluster for the same tamper; apply changed 1, the 5 declared rules read back, the next plan is empty. The write was judged by the estate boundary first: the identical patch by $EDITOR_USER, holding RBAC patch and escalate on clusterroles but not the estate, was refused by ValidatingAdmissionPolicy 'choudoufu-estate-boundary' (\"is not bound to that estate\") with the rules unchanged, and admitted once live/kubernetes/estate-grant.yaml granted it $ESTATE - that admitted write is the drift. BREAK=1 tampers the readonly ClusterRole too and the single-object assertion correctly fails"
fi

# ── 6. plan_approval ──────────────────────────────────────────────────────
gauntlet_begin_stage plan_approval
log "=== 6. plan_approval: the apps namespace gains a label in configuration; a saved plan, an out-of-band label elsewhere, a refusal ==="
add_reviewed() { python3 - "$1/root/namespaces.tf" <<'PY' || fail "the reviewed-label edit did not match namespaces.tf - the corpus pin has moved"
import re, sys
p = sys.argv[1]; s = open(p).read()
s2, n = re.subn(r'(resource "kubernetes_namespace_v1" "apps" \{\n.*?    labels = \{\n)', r'\1      "reviewed" = "yes"\n', s, count=1, flags=re.S)
assert n == 1 and s2.count('"reviewed" = "yes"') == 1
open(p, "w").write(s2)
PY
}
add_reviewed "$ADOPTED"; add_reviewed "$ORACLE"
P_PLAN="$(chdf_a "$ADOPTED" plan -out=approved.tfplan -input=false -no-color 2>&1)" || { printf '%s\n' "$P_PLAN" | tail -20; fail "plan -out failed"; }
grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$P_PLAN" || { printf '%s\n' "$P_PLAN" | tail -10; fail "the saved plan is not exactly one change"; }
kca label clusterrolebinding fulladmin-binding stray=yes >/dev/null || fail "could not move the world (label clusterrolebinding/fulladmin-binding) on A"
P_APPLY="$(chdf_a "$ADOPTED" apply -input=false -no-color approved.tfplan 2>&1)"; P_RC=$?
if [ "${BREAK_APPROVAL:-}" = "1" ]; then
  [ "$P_RC" -eq 0 ] && fail "BREAK_APPROVAL=1: applying the saved plan after the world moved succeeded"
  log "  BREAK_APPROVAL=1: caught - the apply after the world moved exited $P_RC"
  kca label clusterrolebinding fulladmin-binding stray- >/dev/null
  ( chdf_a "$ADOPTED" apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: the saved plan did not apply once the world was put back"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: stock could not apply the reviewed label on B"
  gauntlet_stage plan_approval pass "BREAK_APPROVAL=1 control: applying the saved plan after the world moved exited $P_RC (refused), so the stage's own Break line correctly fails; applied once the world was put back"
else
  [ "$P_RC" -eq 3 ] || { printf '%s\n' "$P_APPLY" | tail -20; fail "apply of the saved plan after the world moved exited $P_RC, want 3"; }
  grep -qF "The approved plan no longer matches the live system" <<< "$P_APPLY" || { printf '%s\n' "$P_APPLY" | tail -20; fail "the refusal does not carry its documented sentence"; }
  [ -z "$(kca get namespace apps -o jsonpath='{.metadata.labels.reviewed}')" ] || fail "the apps namespace gained reviewed despite the refusal"
  kca label clusterrolebinding fulladmin-binding stray- >/dev/null || fail "could not put the world back"
  P_APPLY2="$(chdf_a "$ADOPTED" apply -input=false -no-color approved.tfplan 2>&1)" || { printf '%s\n' "$P_APPLY2" | tail -20; fail "the saved plan did not apply once the world was put back"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$P_APPLY2" || fail "the saved plan's apply did not change exactly one object"
  [ "$(kca get namespace apps -o jsonpath='{.metadata.labels.reviewed}')" = "yes" ] || fail "the apps namespace does not read reviewed=yes after the saved plan applied"
  ( stock_b plan -out=approved.tfplan -input=false -no-color >/dev/null 2>&1 && stock_b apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "stock's own planfile did not apply on B"
  gauntlet_stage plan_approval pass "plan -out wrote one update (the apps namespace gains reviewed=yes); the world then moved out of band (a stray label on the fulladmin-binding ClusterRoleBinding, kubectl, never choudoufu) and apply of the saved plan refused with \"The approved plan no longer matches the live system\" at exit 3, nothing applied; with the label removed the identical file applied, 0 added, 1 changed, 0 destroyed, and reviewed=yes reads back; stock's own planfile applied on the oracle cluster in the unchanged case. BREAK_APPROVAL=1 expects success after the move and correctly fails"
fi

# ── 7. day2_rename: a module call renamed ───────────────────────────────
#
# The rename is a module call's, not a resource block's (#1879): module
# "readonly" becomes module "viewer" through a moved block, so every
# instance inside it - its ClusterRole and ClusterRoleBinding and the Role
# and RoleBinding its for_each puts in apps and licensify - moves address
# at once, keys and all, while every object keeps its own metadata.name.
gauntlet_begin_stage day2_rename
log "=== 7. day2_rename: module.readonly becomes module.viewer through a moved block; six instances move with it ==="
rename_module() { python3 - "$1/root/eks_access.tf" <<'PY' || fail "the module rename did not match eks_access.tf - the corpus pin has moved"
import re, sys
p = sys.argv[1]; s = open(p).read()
s2, n = re.subn(r'^module "readonly" \{$', 'module "viewer" {', s, flags=re.M)
assert n == 1
s2 += '\nmoved {\n  from = module.readonly\n  to   = module.viewer\n}\n'
open(p, "w").write(s2)
PY
}
if [ "${BREAK:-}" = "1" ]; then
  python3 - "$ADOPTED/root/eks_access.tf" <<'PY' || fail "BREAK: could not change the readonly module call's name argument"
import sys
p = sys.argv[1]; s = open(p).read()
assert s.count('name = "readonly"') == 1
open(p, "w").write(s.replace('name = "readonly"', 'name = "readonly-renamed"'))
PY
  R_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK: the plan after renaming the objects failed"; }
  grep -qE 'will be (created|destroyed)|must be replaced' <<< "$R_PLAN" \
    || fail "BREAK=1: changing the objects' own names did not plan a destroy and a create - the zero-churn assertion is not load-bearing: $(plan_line "$R_PLAN")"
  log "  BREAK=1: caught - changing var.name plans $(plan_line "$R_PLAN")"
  python3 - "$ADOPTED/root/eks_access.tf" <<'PY' || fail "BREAK: could not restore the name argument"
import sys
p = sys.argv[1]; s = open(p).read()
open(p, "w").write(s.replace('name = "readonly-renamed"', 'name = "readonly"'))
PY
  rename_module "$ADOPTED"; rename_module "$ORACLE"
  ( chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: the moved-block apply failed"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: stock's moved-block apply failed on B"
  gauntlet_stage day2_rename pass "BREAK=1 control: changing the readonly module call's name argument - the objects' own metadata.name, a genuine identity change - plans $(plan_line "$R_PLAN"), so the zero-churn assertion correctly fails to hold; the moved block then applied"
else
  rename_module "$ADOPTED"; rename_module "$ORACLE"
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's moved-block plan failed on B"; }
  grep -qE "^No changes|Plan: 0 to add, 0 to change, 0 to destroy" <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's moved-block plan on B is not zero churn"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's moved-block apply failed on B"
  R_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_PLAN" | tail -20; fail "the moved-block plan failed"; }
  grep -qE 'will be (created|destroyed)|must be replaced' <<< "$R_PLAN" \
    && { printf '%s\n' "$R_PLAN" | grep -E '^  # .+ (will|must) be'; fail "the module rename proposes a create, a destroy or a replace - not the marker rewritten in place"; }
  grep -qF 'Plan: 0 to add, 6 to change, 0 to destroy.' <<< "$R_PLAN" \
    || { printf '%s\n' "$R_PLAN" | tail -20; fail "the module rename is not exactly six in-place changes (the address annotation rewrites): $(plan_line "$R_PLAN")"; }
  REWRITES="$(grep -cE '~ +"choudoufu\.intentius\.io/tofu-address" = "module\.readonly\.[^"]*" -> "module\.viewer\.' <<< "$R_PLAN")"
  [ "$REWRITES" = "6" ] || { printf '%s\n' "$R_PLAN"; fail "the module rename rewrites $REWRITES address annotation(s) from module.readonly to module.viewer, want 6"; }
  R_APPLY_OUT="$(chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$R_APPLY_OUT" | tail -20; fail "the moved-block apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 6 changed, 0 destroyed" <<< "$R_APPLY_OUT" || { printf '%s\n' "$R_APPLY_OUT" | tail -10; fail "the moved-block apply was not exactly six in-place changes"; }
  expected_ids viewer > "$WORK/ids.renamed"
  MISMATCH="$(identity_mismatches "$KCA" "$WORK/ids.renamed")"
  [ -z "$MISMATCH" ] || { printf '%s\n' "$MISMATCH"; fail "after the module rename an identity does not match by value: $(head -1 <<< "$MISMATCH")"; }
  [ "$(count_a)" = "$INSTANCES" ] || fail "$(count_a) labelled objects after the rename, want $INSTANCES"
  grep -q "No changes." <<< "$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || fail "the replan after the module rename is not empty"
  gauntlet_stage day2_rename pass "a module call renamed through a moved block: module.readonly -> module.viewer moves six instances at once - its ClusterRole, ClusterRoleBinding, and the Role and RoleBinding its for_each keys put in apps and licensify - with no add and no destroy, six in-place changes confined to the address annotation rewrite (0 add, 6 change, 0 destroy, every one module.readonly.* -> module.viewer.*, the for_each keys carried across), the marker rewritten in place; all $INSTANCES identities then compared by value with kubectl at their new addresses, each object's metadata.name untouched, and the next plan empty; stock's plan for the same moved block on the oracle cluster is zero churn, since stock never writes this annotation. The moved-block half only: live-mv also has a Kubernetes leg since #1639, not exercised by this stage. BREAK=1 changes the module call's name argument - the objects' own names - and the zero-churn assertion correctly fails"
fi

# ── 8. day2_remove: one module instance leaves the configuration ────────
#
# module "licensinguser" holds a ClusterRole, a ClusterRoleBinding, and one
# Role and RoleBinding in licensify. Two other module instances (developer
# and viewer, the renamed readonly) hold a Role and RoleBinding each in the
# same namespace: those are what "without touching the others" reads.
gauntlet_begin_stage day2_remove
log "=== 8. day2_remove: the licensinguser module block leaves the configuration ==="
remove_module() { python3 - "$1/root/eks_access.tf" <<'PY'
import re, sys
p = sys.argv[1]; s = open(p).read()
s2, n = re.subn(r'\nmodule "licensinguser" \{\n.*?\n\}\n', '\n', s, count=1, flags=re.S)
assert n == 1 and 'licensinguser' not in s2, "the licensinguser module block did not match eks_access.tf - the corpus pin has moved"
open(p, 'w').write(s2)
PY
}
# destroyed_types reads a plan's destroy lines and prints the type of each,
# with any module prefix and the _v1 suffix taken off, sorted: what was
# proposed, whether at the module address or the sweep's orphan address.
destroyed_types() { grep -E '^[[:space:]]*# .* will be destroyed' <<< "$1" | sed -E 's/^[[:space:]#]*//; s/ will be destroyed.*$//; s/^(module\.[^.]+\.)+//; s/\..*$//; s/_v1$//' | sort | tr '\n' ' ' | sed 's/ $//'; }
if [ "${BREAK_REMOVE:-}" = "1" ]; then
  K_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || fail "BREAK_REMOVE: the plan with the block kept failed"
  grep -qE "will be destroyed|[1-9][0-9]* to destroy" <<< "$K_PLAN" && fail "BREAK_REMOVE=1: with the block kept a destroy was still proposed"
  log "  BREAK_REMOVE=1: caught - with the block kept the plan proposes no destroy"
  gauntlet_stage day2_remove pass "BREAK_REMOVE=1 control: with the licensinguser module block kept, no destroy is proposed; the real check is skipped"
  remove_module "$ADOPTED" || fail "BREAK_REMOVE: could not remove the block afterwards"; remove_module "$ORACLE" || fail "BREAK_REMOVE: could not remove the block from the oracle root afterwards"
  ( chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_REMOVE: the removal apply failed afterwards"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_REMOVE: stock's removal apply failed on B afterwards"
else
  remove_module "$ADOPTED" || fail "could not remove the licensinguser module block from the adopted root"
  remove_module "$ORACLE" || fail "could not remove the licensinguser module block from the oracle root"
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's remove plan failed on B"; }
  grep -qF "Plan: 0 to add, 0 to change, 4 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's remove plan on B is not exactly four destroys"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's remove apply failed on B"
  D_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$D_PLAN" | tail -20; fail "the remove plan failed"; }
  grep -qF "Plan: 0 to add, 0 to change, 4 to destroy." <<< "$D_PLAN" || { printf '%s\n' "$D_PLAN" | tail -20; fail "the remove plan is not exactly four destroys: $(plan_line "$D_PLAN")"; }
  D_ADDRS="$(grep -E '^[[:space:]]*# .* will be destroyed' <<< "$D_PLAN" | sed -E 's/^[[:space:]#]*//; s/ will be destroyed.*$//' | tr '\n' ' ' | sed 's/ $//')"
  D_TYPES="$(destroyed_types "$D_PLAN")"
  [ "$D_TYPES" = "kubernetes_cluster_role kubernetes_cluster_role_binding kubernetes_role kubernetes_role_binding" ] \
    || { printf '%s\n' "$D_PLAN" | grep -E 'destroyed|^Plan:'; fail "the four destroys are not one of each RBAC kind: $D_TYPES ($D_ADDRS)"; }
  for a in $D_ADDRS; do
    grep -q 'licensinguser' <<< "$a" || fail "a proposed destroy, $a, is not one of the licensinguser module instance's objects"
  done
  D_APPLY="$(chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$D_APPLY" | tail -20; fail "the remove apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 0 changed, 4 destroyed" <<< "$D_APPLY" || fail "the remove apply did not destroy exactly four objects"
  kca get clusterrole licensinguser >/dev/null 2>&1 && fail "the licensinguser ClusterRole still exists after the remove apply"
  kca get clusterrolebinding licensinguser-binding >/dev/null 2>&1 && fail "the licensinguser-binding ClusterRoleBinding still exists after the remove apply"
  kca get role licensinguser -n licensify >/dev/null 2>&1 && fail "the licensify/licensinguser Role still exists after the remove apply"
  kca get rolebinding licensinguser-binding -n licensify >/dev/null 2>&1 && fail "the licensify/licensinguser-binding RoleBinding still exists after the remove apply"
  expected_ids viewer removed > "$WORK/ids.removed"
  MISMATCH="$(identity_mismatches "$KCA" "$WORK/ids.removed")"
  [ -z "$MISMATCH" ] || { printf '%s\n' "$MISMATCH"; fail "a module instance the removal should not have touched no longer matches by value: $(head -1 <<< "$MISMATCH")"; }
  grep -q "No changes." <<< "$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || fail "the replan after the remove is not empty"
  [ "$(count_a)" = "27" ] || fail "$(count_a) labelled objects after the remove, want 27"
  gauntlet_stage day2_remove pass "deleting the licensinguser module block - one of eight instances of module \"access-entry\" - proposed exactly four destroys (0 add, 0 change, 4 destroy), one of each RBAC kind and all of them that instance's: $D_ADDRS; applied cleanly, all four gone (kubectl: the ClusterRole, the ClusterRoleBinding, and the Role and RoleBinding in licensify NotFound), while the 27 identities that remain - developer's and viewer's Role and RoleBinding in the same licensify namespace among them - still match by value, and the next plan is empty; stock's plan for the same removal on the oracle cluster is also exactly four destroys. BREAK_REMOVE=1 keeps the block and no destroy is proposed"
fi

# ── 9. day2_count ─────────────────────────────────────────────────────────
gauntlet_begin_stage day2_count
log "=== 9. day2_count: a two-instance count ConfigMap the script declares scales 2 -> 1 -> 2 ==="
write_shards() { # $1 root, $2 count
  cat > "$1/root/count_test.tf" <<EOF
# Added by live/e2e/corpus-govuk-cluster-access/run.sh for the gauntlet's
# day2_count stage: the published root declares no count block of its own.
resource "kubernetes_config_map_v1" "shard" {
  count = $2
  metadata {
    name      = "shard-\${count.index}"
    namespace = kubernetes_namespace_v1.apps.metadata[0].name
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
  { exists_a configmap shard-0 && exists_a configmap shard-1; } || fail "both shards do not exist after the scale-up"
  grep -q "No changes." <<< "$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || fail "the replan after the scale-up is not empty"
  gauntlet_stage day2_count pass "a two-instance count ConfigMap added beside the published root in its apps namespace (the estate's own shape has no count block): scaling 2 to 1 destroyed exactly shard-1, planned at the sweep's orphan address $C_ADDR since the label carries no index (shard-0 untouched, both read with kubectl); back to 2 created exactly kubernetes_config_map_v1.shard[1] under the same name; the next plan is empty; stock's plans for the same two changes on the oracle cluster have the identical shape. BREAK_COUNT=1 asserts the lower index was destroyed and correctly fails"
fi

# ── 9b. day2_replace: a create_before_destroy rename ───────────────────
#
# The stage body is shared by every kind estate: live/e2e/lib/gauntlet.sh's
# gauntlet_kind_day2_replace, which adds its own block and removes it again.
gauntlet_begin_stage day2_replace
log "=== 9b. day2_replace: a content-hashed ConfigMap renamed under create_before_destroy ==="
gauntlet_kind_day2_replace "$ADOPTED/root" "$ORACLE/root" "$NS"

# ── 10. day2_crash ────────────────────────────────────────────────────────
#
# day2_crash on the kind substrate (#1110, #1768), as every kind estate runs
# it: first the create_before_destroy rename window, through the shared
# body; then an apply that creates two objects with a real edge between
# them, killed by the engine's own self-signal inside the -parallelism=1
# walker the instant the first create commits. The first object is a
# kubernetes_secret_v1 because a ConfigMap records nothing (#1235): a Secret
# records residue.wait_for_service_account_token, so what the record store
# contributes to the recovery is read off the plan, not counted.
gauntlet_begin_stage day2_crash
log "=== 10. day2_crash: SIGTERM between the create of one object and the create of the next ==="
gauntlet_kind_day2_crash_rename "$ADOPTED/root" "$NS"

write_crash_pair() { # $1 root, $2 = "first" for crash-first alone
  cat > "$1/root/crash_test.tf" <<EOF
# Added by live/e2e/corpus-govuk-cluster-access/run.sh for the gauntlet's
# day2_crash stage: the published root declares no pair of objects with an
# edge between them that an interrupted apply could be caught halfway
# through. The first is a Secret because a kubernetes_config_map(_v1)
# records nothing (#1188, #1235).
resource "kubernetes_secret_v1" "crash_first" {
  metadata {
    name      = "crash-first"
    namespace = kubernetes_namespace_v1.apps.metadata[0].name
  }
  data = { step = "one" }
}
EOF
  [ "${2:-both}" = "first" ] && return 0
  cat >> "$1/root/crash_test.tf" <<EOF

resource "kubernetes_config_map_v1" "crash_second" {
  metadata {
    name      = "crash-second"
    namespace = kubernetes_namespace_v1.apps.metadata[0].name
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
X_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$X_PLAN" | tail -20; fail "the pre-crash plan failed"; }
grep -qF "Plan: 2 to add, 0 to change, 0 to destroy." <<< "$X_PLAN" \
  || { printf '%s\n' "$X_PLAN" | tail -20; fail "the pre-crash plan is not exactly two adds - there is no two-object apply to interrupt"; }
X_RECORDS_BEFORE="$(gauntlet_record_envelope_count "$ADOPTED/root/.tofu-records")"

# The interrupt is delivered by the engine itself, so this runs in the
# plain foreground: no background process, no output tailing, no poll loop.
# A non-zero exit is the NORMAL outcome - the process was killed.
X_OUT="$( cd "$ADOPTED/root" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" \
  TOFU_E2E_APPLY_RESOURCE_INTERRUPT="kubernetes_secret_v1.crash_first" \
  "$TOFU_CRASH" apply -input=false -auto-approve -no-color -parallelism=1 2>&1 )"; X_RC=$?
printf '%s\n' "$X_OUT" > "$WORK/day2_crash.log"
log "  interrupted apply exited $X_RC (a genuine crash is not expected to exit 0)"
[ "$X_RC" -ne 0 ] || { printf '%s\n' "$X_OUT" | tail -20; fail "the interrupted apply exited 0 - the engine's self-signal never landed, so nothing was interrupted and this stage would measure a clean apply"; }
exists_a secret crash-first || { printf '%s\n' "$X_OUT" | tail -20; fail "crash-first does not exist after the interrupted apply - the kill landed before the create committed, so there is no crash window to recover from"; }
exists_a configmap crash-second && { printf '%s\n' "$X_OUT" | tail -20; fail "crash-second exists after the interrupted apply - the kill landed after both creates, so there is no remainder to propose"; }
# The selector alone, never a selector next to a resource name: kubectl
# refuses that combination outright, which would read as an unlabelled
# object.
kca get secret -n "$NS" -l "tofu-estate=$ESTATE" -o name 2>/dev/null | grep -qx "secret/crash-first" \
  || fail "crash-first was created by the interrupted apply but does not come back under tofu-estate=$ESTATE - the marker the rerun is supposed to find is not there"
X_RECORDS_AFTER="$(gauntlet_record_envelope_count "$ADOPTED/root/.tofu-records")"
log "  crash-first exists and is labelled; crash-second does not exist; records $X_RECORDS_BEFORE -> $X_RECORDS_AFTER"

# What the interrupted apply actually wrote, read off the store by the
# envelope's own address rather than counted (#1235).
X_REC="$(gauntlet_record_file "$ADOPTED/root/.tofu-records" "kubernetes_secret_v1.crash_first")"
[ -n "$X_REC" ] || fail "the interrupted apply created crash-first but wrote no record for kubernetes_secret_v1.crash_first (records $X_RECORDS_BEFORE -> $X_RECORDS_AFTER); there is nothing for the recovery to read back and this stage cannot measure what the record contributes"
[ "$X_RECORDS_AFTER" = "$((X_RECORDS_BEFORE + 1))" ] || fail "records went $X_RECORDS_BEFORE -> $X_RECORDS_AFTER across the interrupted apply, want exactly one more - the apply is supposed to have written the record for the one object it did create, and nothing else"
X_RESIDUE="$(gauntlet_record_residue "$X_REC" | tr '\n' ' ' | sed 's/ $//')"
[ "$X_RESIDUE" = "wait_for_service_account_token" ] || fail "the record the interrupted apply wrote for kubernetes_secret_v1.crash_first carries residue [${X_RESIDUE:-none}], want wait_for_service_account_token (#1188, #1235)"
log "  the interrupted apply wrote one record for kubernetes_secret_v1.crash_first, carrying residue $X_RESIDUE"

if [ "${BREAK_CRASH_UNBOUND:-}" = "1" ]; then
  kca label secret crash-first -n "$NS" tofu-estate- >/dev/null 2>&1 || fail "BREAK_CRASH_UNBOUND: could not strip the label off crash-first"
  log "  BREAK_CRASH_UNBOUND=1: stripped tofu-estate off crash-first with kubectl"
fi

R_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)"; R_RC=$?
R_LINE="$(plan_line "$R_PLAN")"
# What the real check asserts, as one predicate, so the Break controls can
# require the SAME predicate to fail rather than approximating it.
recovered() {
  [ "$R_RC" -eq 0 ] || return 1
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$R_PLAN" || return 1
  grep -qE '^[[:space:]]*# kubernetes_config_map(_v1)?\.crash_second will be created' <<< "$R_PLAN" || return 1
  grep -E '^[[:space:]]*# .* will be' <<< "$R_PLAN" | grep -q 'crash_first\|crash-first' && return 1
  return 0
}

if [ "${BREAK_CRASH:-}" = "1" ]; then
  log "=== 10b (BREAK_CRASH=1). assert nothing is proposed after the interrupt - this must fail ==="
  [ "$R_RC" -eq 0 ] || { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK_CRASH=1: the plan after the interrupt exited $R_RC"; }
  grep -qF "No changes." <<< "$R_PLAN" \
    && { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK_CRASH=1: the plan after a real interrupted two-object apply came back empty, so this stage's own check is not load-bearing"; }
  log "  BREAK_CRASH=1: caught - the plan proposes work ($R_LINE), so 'nothing is proposed' correctly fails to hold"
  gauntlet_stage day2_crash pass "BREAK_CRASH=1 control: after the same real interrupt the plan proposes work ($R_LINE), so the stage's own Break line - interrupt and then assert nothing is proposed - correctly fails to hold; the real check is skipped $CRASH_RENAME_DETAIL"
  ( chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_CRASH: the recovery apply failed afterwards"
elif [ "${BREAK_CRASH_UNBOUND:-}" = "1" ]; then
  log "=== 10b (BREAK_CRASH_UNBOUND=1). the same check against an unbound object - this must fail ==="
  if recovered; then
    printf '%s\n' "$R_PLAN" | grep -E '^Plan:|will be'
    fail "BREAK_CRASH_UNBOUND=1: the recovery check still holds with crash-first carrying no tofu-estate label - it is not measuring whether the crashed-out object was bound at all"
  fi
  log "  BREAK_CRASH_UNBOUND=1: caught - with the label stripped the recovery check fails ($R_LINE)"
  gauntlet_stage day2_crash pass "BREAK_CRASH_UNBOUND=1 control: with the tofu-estate label stripped off the object the interrupted apply created - the unrecovered run this stage exists to catch - the recovery check correctly fails to hold ($R_LINE); the real check is skipped $CRASH_RENAME_DETAIL"
  kca delete secret crash-first -n "$NS" >/dev/null 2>&1 || fail "BREAK_CRASH_UNBOUND: could not delete the unlabelled crash-first afterwards"
  ( chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_CRASH_UNBOUND: the apply after the cleanup failed"
else
  if ! recovered; then
    printf '%s\n' "$R_PLAN" | grep -E '^Plan:|^No changes|will be' | head -20
    gauntlet_stage day2_crash fail "the plan after a real interrupt between the create of kubernetes_secret_v1.crash_first and the create of kubernetes_config_map_v1.crash_second is not exactly the remainder: ${R_LINE:-no plan line} (exit $R_RC). crash-first exists on the cluster carrying tofu-estate=$ESTATE and crash-second does not, both read with kubectl; stock, walked into the same position on the oracle cluster, plans exactly one add (crash_second). The interrupted apply wrote one record for kubernetes_secret_v1.crash_first carrying residue ${X_RESIDUE:-none} (records $X_RECORDS_BEFORE -> $X_RECORDS_AFTER) $CRASH_RENAME_DETAIL"
  else
    # The record's contribution, measured rather than counted (#1235): take
    # the one record the interrupted apply wrote out of the store, replan
    # from the identical position, and the difference between the two plans
    # IS what the record carried. Put it back and the difference has to go.
    mv "$X_REC" "$WORK/crash_first.record" || fail "could not move the crash record aside"
    N_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)"; N_RC=$?
    N_LINE="$(plan_line "$N_PLAN")"
    [ "$N_RC" -eq 0 ] || { printf '%s\n' "$N_PLAN" | tail -20; fail "the replan with the crash record taken out of the store exited $N_RC"; }
    grep -qF "Plan: 1 to add, 1 to change, 0 to destroy." <<< "$N_PLAN" \
      || { printf '%s\n' "$N_PLAN" | grep -E '^Plan:|will be|^ +[+~-] ' | head -20
           fail "with the one record the interrupted apply wrote taken out of the store, the recovery plan is $N_LINE, not the remainder plus one in-place update - so the record contributed nothing this stage can read"; }
    grep -qE '^[[:space:]]+\+ wait_for_service_account_token +=' <<< "$N_PLAN" \
      || { printf '%s\n' "$N_PLAN" | grep -E 'will be|^ +[+~-] ' | head -20
           fail "the extra in-place update the missing record produces does not propose wait_for_service_account_token back - that config-only argument's applied value is the residue the record is supposed to be carrying"; }
    mv "$WORK/crash_first.record" "$X_REC" || fail "could not put the crash record back"
    B_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)"
    grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$B_PLAN" \
      || { printf '%s\n' "$B_PLAN" | grep -E '^Plan:|will be' | head -10
           fail "putting the record back does not restore the exact-remainder plan, so the difference measured above is not the record's"; }
    log "  the record's contribution: with it, $R_LINE; without it, $N_LINE (+ wait_for_service_account_token on crash_first); with it again, the remainder"

    R_APPLY="$(chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)"; R_APPLY_RC=$?
    [ "$R_APPLY_RC" -eq 0 ] || { printf '%s\n' "$R_APPLY" | tail -20; fail "the recovery apply exited $R_APPLY_RC"; }
    grep -qF "Apply complete! Resources: 1 added, 0 changed, 0 destroyed" <<< "$R_APPLY" \
      || { printf '%s\n' "$R_APPLY" | tail -5; fail "the recovery apply did not add exactly the one remaining object"; }
    exists_a configmap crash-second || fail "crash-second does not exist after the recovery apply"
    exists_a secret crash-first || fail "crash-first is gone after the recovery apply - the recovery replaced the object the crash created instead of binding it"
    R_REPLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)"; R_REPLAN_RC=$?
    [ "$R_REPLAN_RC" -eq 0 ] || { printf '%s\n' "$R_REPLAN" | tail -30
      R_ERR="$(grep -E '^Error' <<< "$R_REPLAN" | head -1)"
      fail "the replan after the recovery exited $R_REPLAN_RC: ${R_ERR:-no Error: line; the last 30 lines of the plan are above this verdict in the log}"; }
    grep -q "No changes." <<< "$R_REPLAN" || { printf '%s\n' "$R_REPLAN" | tail -20; fail "the replan after the recovery is not empty"; }
    gauntlet_stage day2_crash pass "a Secret and a ConfigMap added beside the published root in its apps namespace (the estate's own shape has no two objects with an edge between them to be caught halfway through): the apply creating both was interrupted by a real SIGTERM (exit $X_RC), delivered by the engine itself inside the -parallelism=1 graph walker the instant kubernetes_secret_v1.crash_first's create committed (internal/command/apply_e2etesting_crash.go); crash_second reads crash_first's name, so the walker cannot have reached it - kubectl confirms crash-first exists carrying tofu-estate=$ESTATE and crash-second does not. The next plan proposed exactly the remainder ($R_LINE, kubernetes_config_map_v1.crash_second created) and nothing at all for crash-first, which it bound by its label and its namespace and name - not a second create the API server would refuse, not an orphan sweep - matching stock's own plan from the same position on the oracle cluster; the recovery apply added exactly one object, both read back with kubectl, and the plan after it is empty. The record store's contribution is read, not counted: the interrupted apply wrote exactly one record (files $X_RECORDS_BEFORE -> $X_RECORDS_AFTER) for kubernetes_secret_v1.crash_first carrying residue $X_RESIDUE, and taking that one file out of the store and replanning from the identical position turns the recovery plan from $R_LINE into $N_LINE, proposing wait_for_service_account_token back on the object the crash left behind; putting it back restores the exact-remainder plan. BREAK_CRASH=1 asserts nothing is proposed and correctly fails; BREAK_CRASH_UNBOUND=1 strips the label off crash-first and the same recovery check correctly fails $CRASH_RENAME_DETAIL"
  fi
fi
gauntlet_end_stage

# ── 11. day2_teardown ─────────────────────────────────────────────────────
gauntlet_begin_stage day2_teardown
log "=== 11. day2_teardown: apply -destroy on the adopted estate, and stock's own destroy on B ==="
T_EXPECT="$(count_a)"
T_OUT="$(chdf_a "$ADOPTED" apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$T_OUT" | tail -20; fail "apply -destroy failed"; }
grep -qF "Resources: 0 added, 0 changed, $T_EXPECT destroyed" <<< "$T_OUT" || { printf '%s\n' "$T_OUT" | tail -5; fail "apply -destroy did not remove exactly the $T_EXPECT remaining objects"; }
gauntlet_wait_until 120 "the three namespaces gone from A" -- namespaces_gone_a || fail "a namespace of the estate still exists two minutes after the destroy"
[ "$(count_a)" = "0" ] || fail "$(count_a) object(s) still carry tofu-estate=$ESTATE after the destroy"
O_EXPECT="$(stock_b state list 2>/dev/null | wc -l | tr -d ' ')"
O_T="$(stock_b apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$O_T" | tail -10; fail "stock's destroy failed on B"; }
grep -qF "Resources: 0 added, 0 changed, $O_EXPECT destroyed" <<< "$O_T" || fail "stock's destroy on B did not remove exactly the $O_EXPECT objects its state held"
gauntlet_stage day2_teardown pass "apply -destroy removed exactly the $T_EXPECT remaining objects in one apply - the module instances' cluster-scoped and namespaced RBAC objects, the script's own ConfigMaps and Secret, and the namespaces - in an order the API server accepted, the three namespaces gone and no object of any of the estate's five kinds carrying tofu-estate=$ESTATE (kubectl, every namespace); stock's destroy of the same estate on the oracle cluster also removed exactly the $O_EXPECT its state held"

# ── 12. greenfield ────────────────────────────────────────────────────────
gauntlet_begin_stage greenfield
log "=== 12. greenfield: choudoufu applies the published root fresh, with a live block, on the now-empty cluster A ==="
write_root "$GREEN" live
( chdf_a "$GREEN" init -input=false -no-color > "$WORK/init.green.log" 2>&1 ) || { tail -20 "$WORK/init.green.log"; fail "greenfield init failed"; }
G_OUT="$(chdf_a "$GREEN" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$G_OUT" | tail -20; fail "greenfield apply failed"; }
grep -qF "Apply complete! Resources: $INSTANCES added, 0 changed, 0 destroyed" <<< "$G_OUT" || fail "greenfield apply did not add exactly $INSTANCES objects"
[ ! -f "$GREEN/root/terraform.tfstate" ] || fail "a terraform.tfstate appeared after a live-block apply"
[ "$(count_a)" = "$INSTANCES" ] || fail "$(count_a) object(s) carry tofu-estate=$ESTATE after the greenfield apply, want $INSTANCES"
expected_ids readonly > "$WORK/ids.green"
MISMATCH="$(identity_mismatches "$KCA" "$WORK/ids.green")"
[ -z "$MISMATCH" ] || { printf '%s\n' "$MISMATCH"; fail "after the greenfield apply an identity does not match by value: $(head -1 <<< "$MISMATCH")"; }
G_RECORDS="$(gauntlet_record_envelope_count "$GREEN/root/.tofu-records")"
grep -q "No changes." <<< "$(chdf_a "$GREEN" plan -input=false -no-color 2>&1)" || fail "the greenfield replan is not empty"
rm -f "$GREEN/root/.terraform/choudoufu-cache.tfstate"
grep -q "No changes." <<< "$(chdf_a "$GREEN" plan -input=false -no-color 2>&1)" || fail "the greenfield replan without the cache is not empty"

# The lost-store control (#1235), as every kind estate takes it: delete the
# whole record store and the cache, and the next plan may propose residue
# back as in-place updates but nothing created, destroyed or replaced.
rm -rf "$GREEN/root/.tofu-records" "$GREEN/root/.terraform/choudoufu-cache.tfstate"
L_PLAN="$(chdf_a "$GREEN" plan -input=false -no-color 2>&1)"; L_RC=$?
L_LINE="$(plan_line "$L_PLAN")"
[ "$L_RC" -eq 0 ] || { printf '%s\n' "$L_PLAN" | tail -20; fail "the plan with no record store at all exited $L_RC"; }
L_GONE="$(grep -cE '^[[:space:]]*# .* will be (created|destroyed|replaced)' <<< "$L_PLAN")"
[ "$L_GONE" = "0" ] || { printf '%s\n' "$L_PLAN" | grep -E 'will be' | head -20
  fail "with the whole record store deleted the plan proposes $L_GONE create/destroy/replace(s) ($L_LINE) - the objects are not being found by their label and their namespace and name alone"; }
L_CHANGES="$(grep -cE '^[[:space:]]*# .* will be updated in-place' <<< "$L_PLAN")"
L_RESIDUE="$(grep -oE '^[[:space:]]+\+ [a-z_]+ +=' <<< "$L_PLAN" | sed -E 's/^[[:space:]]*\+ //; s/ *=$//' | sort -u | tr '\n' ' ' | sed 's/ $//')"
L_APPLY="$(chdf_a "$GREEN" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$L_APPLY" | tail -20; fail "the apply that should reconverge after a lost record store failed"; }
grep -qF "Apply complete! Resources: 0 added, $L_CHANGES changed, 0 destroyed" <<< "$L_APPLY" \
  || { printf '%s\n' "$L_APPLY" | tail -5; fail "the reconverging apply after a lost record store is not exactly the $L_CHANGES in-place update(s) the plan proposed"; }
grep -q "No changes." <<< "$(chdf_a "$GREEN" plan -input=false -no-color 2>&1)" || fail "the plan after the reconverging apply is not empty"
[ "$(count_a)" = "$INSTANCES" ] || fail "$(count_a) object(s) carry tofu-estate=$ESTATE after the lost-store reconvergence, want $INSTANCES"
log "  lost store: $L_LINE, every object still bound; one apply reconverged and the plan after it is empty"
DROP=""; [ "${BREAK:-}" = "1" ] && DROP="clusterrole/ithctester"
inventory "$KCA" "$DROP" > "$WORK/inventory.green.json" || fail "could not read the greenfield inventory"
if [ "${BREAK:-}" = "1" ]; then
  diff -q "$WORK/inventory.stock.json" "$WORK/inventory.green.json" >/dev/null && fail "BREAK=1: with the ithctester ClusterRole dropped the two inventories still match"
  log "  BREAK=1: caught - the inventories differ once the ClusterRole is dropped"
  gauntlet_stage greenfield pass "BREAK=1 control: dropping the ithctester ClusterRole from the greenfield inventory makes the object-by-object comparison correctly fail; the estate applied ($INSTANCES added, no terraform.tfstate) and replanned empty with and without the cache"
else
  if ! diff -u "$WORK/inventory.stock.json" "$WORK/inventory.green.json"; then
    fail "the greenfield inventory differs from stock's cold-deploy inventory (diff above)"
  fi
  gauntlet_stage greenfield pass "the published root applied fresh with a live block and no terraform.tfstate: $INSTANCES objects, every one labelled tofu-estate=$ESTATE and every identity, module instance and for_each key included, matching by value (kubectl, five kinds); the record store held $G_RECORDS record envelope(s), counted by their own address field (#1291); replanned empty with and without the cache. Deleting the whole record store and the cache and replanning proposed $L_LINE: nothing created, destroyed or swept, every object still bound by its label and its namespace and name, and $L_CHANGES in-place update(s) putting back the residue the store held (${L_RESIDUE:-none}); one apply reconverged and the plan after it is empty (#1188, #1235). The cluster's inventory (each Namespace's labels and annotations, every ClusterRole's and Role's rules as the dynamic blocks rendered them, every binding's role reference and subjects) matches stock's cold deploy on the same cluster object by object, the marker label and this fork's annotations normalised out. BREAK=1 drops the ithctester ClusterRole from the expected inventory and the match correctly fails"
fi
( chdf_a "$GREEN" apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "greenfield teardown failed"

# ── 13. strict ────────────────────────────────────────────────────────────
gauntlet_begin_stage strict
STRICT="$WORK/strict/root"; mkdir -p "$STRICT"
strict_block() { cat <<EOF
terraform {
  required_providers {
    random = {
      source  = "hashicorp/random"
      version = ">= 3.0"
    }
  }
  live {
    estate = "corpus-govuk-cluster-access-strict"
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
log "=== 13. strict: every strict toggle on ==="
strict_block "refuse" > "$STRICT/main.tf"
( chdf_a "$WORK/strict" init -input=false -no-color > "$WORK/init.strict.log" 2>&1 ) || { tail -20 "$WORK/init.strict.log"; fail "choudoufu init for the strict-stage scratch estate failed"; }
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
log "corpus-govuk-cluster-access: done"
