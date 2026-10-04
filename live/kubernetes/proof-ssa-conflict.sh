#!/usr/bin/env bash
# GitHub issue #1191 (ruled 2026-10-03) and #1110 fault 3: two estates on
# one object, one field each, with the field manager as the boundary.
# Manual: it is not a claim, not a workflow step, and nothing in CI runs it
# yet. It was written with the unit that admits the six field-granular
# types and has NOT been run; live/kubernetes/FAULTS.md section 3 says so.
# Once it runs green it becomes a smoke claim's Kubernetes proof
# (live/smoke/claims.json needs a run before a cell may read proven).
#
#   KUBECONFIG=<a kind cluster's kubeconfig> live/kubernetes/proof-ssa-conflict.sh
#
# The cluster is the caller's: create it from the pin, the way every other
# kind call site does, e.g.
#   kind create cluster --image "$(cat live/kind-node-image)" --name ssa --kubeconfig /tmp/ssa.kubeconfig
# CHOUDOUFU_BIN names a binary to use; otherwise one is built from this
# checkout. The provider is fetched from the registry by init.
#
# What it shows, in order:
#   1. estate ssa-a labels a ConfigMap neither estate owns; managedFields
#      names "choudoufu:ssa-a" as the label's owner, and the replan is empty.
#   2. estate ssa-b declares the same label: the plan warns, naming ssa-a,
#      and the apply is refused by the API server with a 409 naming
#      "choudoufu:ssa-a" - the first estate's name, not "Terraform".
#   3. the same block with force = true is refused by the plan, by name,
#      exit 1, nothing applied; the label still reads ssa-a's value.
#   4. the control: force = true over a label kubectl wrote (a manager that
#      is not an estate's) plans and applies, as force ordinarily does.
#   5. two field-granular blocks of one estate on one object are refused.
#
# BREAK=1 is the control that the refusal in step 3 is the plan's and not
# the server's: it runs the step-3 apply with the stock oracle (`tofu`, which
# must be on PATH), whose force_conflicts goes through and takes the label.
# If the label still read ssa-a's value afterwards, step 3's "nothing
# applied" would prove nothing about this fork.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"
: "${KUBECONFIG:?set KUBECONFIG to a cluster this script may write to}"
export KUBE_CONFIG_PATH="$KUBECONFIG"
command -v kubectl >/dev/null || { echo "FAIL: kubectl is not on PATH" >&2; exit 1; }

WORK="$(mktemp -d)"
NS="ssa-$(date +%s)"
kc() { kubectl --kubeconfig "$KUBECONFIG" --request-timeout=30s "$@"; }
fail() { echo "FAIL: $*" >&2; exit 1; }
step() { echo; echo "== $*"; }
cleanup() { kc delete namespace "$NS" --wait=false >/dev/null 2>&1 || true; rm -rf "$WORK"; }
trap cleanup EXIT

if [ -n "${CHOUDOUFU_BIN:-}" ]; then
  CHDF="$CHOUDOUFU_BIN"
else
  CHDF="$WORK/choudoufu"
  ( cd "$ROOT" && go build -o "$CHDF" ./cmd/choudoufu ) || fail "go build ./cmd/choudoufu"
fi

# estate <dir> <estate> <body>: one root, the body being its resources.
estate() {
  mkdir -p "$1"
  cat > "$1/versions.tf" <<EOF
terraform {
  live {
    estate = "$2"
  }
  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "= 3.2.1"
    }
  }
}

provider "kubernetes" {}
EOF
  printf '%s\n' "$3" > "$1/main.tf"
}

labels_block() { # <name> <labels-hcl> [extra]
  cat <<EOF
resource "kubernetes_labels" "$1" {
  api_version = "v1"
  kind        = "ConfigMap"
  metadata {
    name      = "shared"
    namespace = "$NS"
  }
  labels = $2
  $3
}
EOF
}

# owner_of <label>: the manager that owns metadata.labels.<label> on the
# shared ConfigMap, read off managedFields.
owner_of() {
  kc get configmap shared -n "$NS" -o json --show-managed-fields | python3 -c '
import json, sys
key = "f:" + sys.argv[1]
owners = []
for e in json.load(sys.stdin)["metadata"].get("managedFields", []):
    if e.get("subresource"):
        continue
    if key in e.get("fieldsV1", {}).get("f:metadata", {}).get("f:labels", {}):
        owners.append(e["manager"])
print(",".join(sorted(owners)))
' "$1"
}
label_of() { kc get configmap shared -n "$NS" -o "jsonpath={.metadata.labels.$1}"; }

step "0. an object neither estate owns"
kc create namespace "$NS" >/dev/null
kc create configmap shared -n "$NS" --from-literal=k=v >/dev/null
kc label configmap shared -n "$NS" team=platform >/dev/null
echo "ConfigMap $NS/shared, created by kubectl, label team owned by: $(owner_of team)"

step "1. estate ssa-a labels it"
A="$WORK/a"
estate "$A" ssa-a "$(labels_block owner '{ owner = "a" }')"
( cd "$A" && "$CHDF" init -input=false -no-color >/dev/null ) || fail "init in ssa-a"
OUT="$(cd "$A" && "$CHDF" apply -auto-approve -input=false -no-color 2>&1)" || fail "ssa-a apply: $OUT"
[ "$(label_of owner)" = "a" ] || fail "the label owner does not read a after ssa-a's apply"
[ "$(owner_of owner)" = "choudoufu:ssa-a" ] || fail "metadata.labels.owner is owned by '$(owner_of owner)', want choudoufu:ssa-a alone"
OUT="$(cd "$A" && "$CHDF" plan -input=false -no-color 2>&1)" || fail "ssa-a replan: $OUT"
grep -q "No changes." <<< "$OUT" || fail "ssa-a's replan is not empty: $OUT"
echo "-> label owner=a, written under choudoufu:ssa-a; the replan is empty"

step "2. estate ssa-b declares the same label: warned by name, refused by the server by name"
B="$WORK/b"
estate "$B" ssa-b "$(labels_block owner '{ owner = "b" }')"
( cd "$B" && "$CHDF" init -input=false -no-color >/dev/null ) || fail "init in ssa-b"
OUT="$(cd "$B" && "$CHDF" plan -input=false -no-color 2>&1)" || fail "ssa-b plan: $OUT"
grep -q "Field owned by another estate" <<< "$OUT" || fail "ssa-b's plan does not warn that the label is ssa-a's: $OUT"
grep -q 'the estate "ssa-a" owns' <<< "$(tr '\n' ' ' <<< "$OUT" | tr -s ' ')" || fail "the warning does not name ssa-a: $OUT"
RC=0
OUT="$(cd "$B" && "$CHDF" apply -auto-approve -input=false -no-color 2>&1)" || RC=$?
[ "$RC" != "0" ] || fail "ssa-b's apply over ssa-a's label succeeded"
grep -q 'conflict with "choudoufu:ssa-a"' <<< "$OUT" || fail "the API server's conflict does not name choudoufu:ssa-a: $OUT"
[ "$(label_of owner)" = "a" ] || fail "the label changed under a refused apply"
echo "-> the plan named ssa-a; the server refused the apply naming choudoufu:ssa-a; owner=a"

step "3. the same block with force = true: refused by the plan, by name"
estate "$B" ssa-b "$(labels_block owner '{ owner = "b" }' 'force = true')"
RC=0
OUT="$(cd "$B" && "$CHDF" plan -input=false -no-color 2>&1)" || RC=$?
[ "$RC" = "1" ] || fail "ssa-b's forced plan exited $RC, want 1: $OUT"
grep -q "Force refused over another estate's field" <<< "$OUT" || fail "the forced plan is not refused by name: $OUT"
grep -q 'the estate "ssa-a" owns' <<< "$(tr '\n' ' ' <<< "$OUT" | tr -s ' ')" || fail "the refusal does not name ssa-a: $OUT"
RC=0
OUT="$(cd "$B" && "$CHDF" apply -auto-approve -input=false -no-color 2>&1)" || RC=$?
[ "$RC" != "0" ] || fail "ssa-b's forced apply went through"
[ "$(label_of owner)" = "a" ] || fail "the label changed under a refused forced apply"
[ "$(owner_of owner)" = "choudoufu:ssa-a" ] || fail "the label's owner moved to '$(owner_of owner)'"
echo "-> refused at plan, naming ssa-a; nothing applied"

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK - the stock oracle forces the same write, and the label moves"
  command -v tofu >/dev/null || fail "BREAK needs the stock oracle, tofu, on PATH"
  S="$WORK/stock"
  mkdir -p "$S"
  cat > "$S/main.tf" <<EOF
terraform {
  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "= 3.2.1"
    }
  }
}
provider "kubernetes" {}
$(labels_block owner '{ owner = "b" }' 'force = true
  field_manager = "choudoufu:ssa-b"')
EOF
  ( cd "$S" && tofu init -input=false -no-color >/dev/null && tofu apply -auto-approve -input=false -no-color >/dev/null ) \
    || fail "BREAK: the stock forced apply failed, so step 3's refusal cannot be told apart from the server's"
  [ "$(label_of owner)" = "b" ] || fail "BREAK: the stock forced apply did not take the label"
  echo "-> caught: without this fork's refusal the same forced write takes ssa-a's label"
  exit 0
fi

step "4. control - force over a label kubectl wrote keeps its ordinary meaning"
estate "$B" ssa-b "$(labels_block team '{ team = "b" }' 'force = true')"
OUT="$(cd "$B" && "$CHDF" plan -input=false -no-color 2>&1)" || fail "ssa-b's forced plan over kubectl's label failed: $OUT"
if grep -q "Force refused" <<< "$OUT"; then fail "force over a non-estate manager was refused: $OUT"; fi
OUT="$(cd "$B" && "$CHDF" apply -auto-approve -input=false -no-color 2>&1)" || fail "ssa-b's forced apply over kubectl's label failed: $OUT"
[ "$(label_of team)" = "b" ] || fail "the forced apply did not take the team label"
[ "$(owner_of team)" = "choudoufu:ssa-b" ] || fail "metadata.labels.team is owned by '$(owner_of team)', want choudoufu:ssa-b"
echo "-> force took kubectl's label, now owned by choudoufu:ssa-b"

step "5. two blocks of one estate on one object are refused"
estate "$B" ssa-b "$(labels_block team '{ team = "b" }')
resource \"kubernetes_annotations\" \"team\" {
  api_version = \"v1\"
  kind        = \"ConfigMap\"
  metadata {
    name      = \"shared\"
    namespace = \"$NS\"
  }
  annotations = { team = \"b\" }
}"
RC=0
OUT="$(cd "$B" && "$CHDF" plan -input=false -no-color 2>&1)" || RC=$?
[ "$RC" = "1" ] || fail "two blocks on one object planned with exit $RC, want 1: $OUT"
grep -q "Two field-granular blocks patch one object" <<< "$OUT" || fail "not refused by name: $OUT"
echo "-> refused: both would write under choudoufu:ssa-b and erase each other"

echo
echo "PASS: two estates met on one object, and the field manager was the boundary"
