#!/usr/bin/env bash
# The RBAC one identity needs on the record store cluster's own namespace,
# for one estate: a ServiceAccount, a Role and a RoleBinding.
#
#   render-role.sh <estate> [--namespace <namespace>] [--read-only]
#
# This script is the single source of that manifest, the way
# examples/record-store-bucket/iam/render-policy.sh is the single source of
# the bucket's role policy (GitHub issue #1342, mirrored here on #1603). The
# documentation page shows its output and a test holds the two together.
#
# The default rendering is the grant: an apply identity, with every verb
# internal/live/staterecord/kubernetescontract.go's KubernetesRecordVerbs
# names (get, list, create, update, delete). --read-only renders the same
# manifest for a plan identity instead, with only KubernetesPlanVerbs (get,
# list) - the same "plans and never applies" distinction
# examples/record-store-bucket/iam/render-policy.sh's own --read-only makes
# for the bucket (GitHub issue #1370). A plan identity is refused by name
# until the estate has been applied once by an identity that may write,
# because a store with no sentinel is indistinguishable from an empty
# estate; see examples/record-store-cluster/CONTRACT.md.
#
# Everything here is namespaced. The records namespace IS the read
# boundary (CONTRACT.md, read_isolation): RBAC cannot condition on a label
# and admission never sees a get or a list, so a ClusterRole bound to
# either identity would let it read every other estate's records in the
# cluster. Never widen either Role to a ClusterRole.
#
# What this does NOT grant: `use` on the virtual estate resource that
# live/kubernetes/estate-boundary.yaml's admission policy fences writes
# with. That is a separate, cluster-scoped grant handed out one estate at a
# time with live/kubernetes/estate-grant.yaml (the "estate grant"), and an
# apply identity needs both this Role and that grant to actually write a
# record. A plan identity needs neither the grant nor a write verb here,
# since it writes nothing.
set -euo pipefail

usage() { sed -n '2,4p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 2; }
[ $# -ge 1 ] || usage
estate="$1"; shift
namespace=""; read_only=false
while [ $# -gt 0 ]; do
  case "$1" in
    --namespace) namespace="${2:?--namespace needs a Kubernetes namespace}"; shift 2 ;;
    --read-only) read_only=true; shift ;;
    *) usage ;;
  esac
done

# An estate name is ^[a-z][a-z0-9-]{0,127}$ (markers.ValidEstateName). It
# reaches both a Kubernetes object name and, by default, the namespace name
# below unescaped, so a stray "*" or "/" here is a malformed manifest at
# best and a manifest that means something else at worst.
[[ "$estate" =~ ^[a-z][a-z0-9-]{0,127}$ ]] || { echo "not an estate name: $estate" >&2; exit 2; }

if [ -z "$namespace" ]; then
  # internal/live/staterecord's KubernetesRecordNamespacePrefix, plus the
  # estate: what `record_store "kubernetes" {}` resolves to with no
  # `namespace` argument.
  namespace="tofu-records-${estate}"
fi
# A Kubernetes namespace name is a DNS-1123 label: lowercase alphanumerics
# and "-", starting and ending with an alphanumeric, at most 63 characters.
[[ "$namespace" =~ ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ ]] || { echo "not a Kubernetes namespace: $namespace" >&2; exit 2; }

if $read_only; then
  role="choudoufu-records-plan"
  account="choudoufu-plan"
  verbs=("get" "list")
else
  role="choudoufu-records-apply"
  account="choudoufu-apply"
  verbs=("get" "list" "create" "update" "delete")
fi

verbs_yaml=""
for v in "${verbs[@]}"; do
  verbs_yaml+="\"${v}\", "
done
verbs_yaml="${verbs_yaml%, }"

cat <<EOF
apiVersion: v1
kind: ServiceAccount
metadata:
  name: ${account}
  namespace: ${namespace}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: ${role}
  namespace: ${namespace}
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: [${verbs_yaml}]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: ${role}
  namespace: ${namespace}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: ${role}
subjects:
  - kind: ServiceAccount
    name: ${account}
    namespace: ${namespace}
EOF
