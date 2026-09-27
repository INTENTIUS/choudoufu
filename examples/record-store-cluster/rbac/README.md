# RBAC for the record store cluster

Each estate's records namespace needs two identities: one that plans and
one that applies. This page is the RBAC for both, the cluster half of
[`examples/record-store-bucket/iam`](../../record-store-bucket/iam/README.md).
Render it rather than copying it by hand:

```
examples/record-store-cluster/rbac/render-role.sh prod
```

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: choudoufu-apply
  namespace: tofu-records-prod
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: choudoufu-records-apply
  namespace: tofu-records-prod
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get", "list", "create", "update", "delete"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: choudoufu-records-apply
  namespace: tofu-records-prod
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: choudoufu-records-apply
subjects:
  - kind: ServiceAccount
    name: choudoufu-apply
    namespace: tofu-records-prod
```

The script is the single source, the same rule
[`examples/record-store-bucket/iam/render-policy.sh`](../../record-store-bucket/iam/render-policy.sh)
follows for the bucket's role policy: this page shows its output, and a test
fails if the two differ. `examples/record-store-cluster/manifests/records.yaml`,
which `just up` applies, carries both identities' Roles inline rather than
shelling out to this script, and a separate test
(`TestRecordStoreClusterRolesCarryTheStoresVerbs`) holds those verbs to the
same source this renderer reads, so the two cannot drift apart even though
neither generates the other.

## Why a Role, and never a ClusterRole

The records namespace is the whole read boundary. RBAC cannot condition on
a label the way an S3 IAM policy conditions on a tag, and Kubernetes
admission is never consulted for a `get` or a `list` - it sees only
`CREATE`, `UPDATE` and `DELETE` - so nothing else in this cluster fences a
read the way `DenyReadingAnotherEstatesObjects` fences one in the bucket's
policy. An identity bound to a `ClusterRole` with these same rules would be
able to read, and in the apply case overwrite or delete, every other
estate's record Secrets in the cluster. That is `read_isolation` in
[CONTRACT.md](../CONTRACT.md) and in
`internal/live/staterecord/kubernetescontract.go`'s `checkReadIsolation`,
which measures exactly this and refuses a namespace it can prove is not
the boundary. Every manifest this script prints is a `Role` and a
`RoleBinding`, bound in one namespace, never a `ClusterRole`.

## What each identity gets

`internal/live/staterecord/kubernetescontract.go` names the two lists this
script renders from, and nothing here repeats the choice of which verb goes
where - it reads the store's own lists so the two cannot disagree:

**`choudoufu-apply`** (the default rendering, "the grant") gets
`KubernetesRecordVerbs`: `get`, `list`, `create`, `update`, `delete`. That
is everything the store's `KubernetesStore` uses on a Secret and nothing
more - never `patch`, never `watch`. An apply, a `live-mv` that is not a
dry run, and a `live-import -approve` all run as this identity.

**`choudoufu-plan`** (`--read-only`) gets `KubernetesPlanVerbs`: `get` and
`list` alone. A plan reads records and writes none; the one write on its
path is the store's provisioning sentinel, and since #1416 a denial on that
write is carried past to the identity's `List` rather than ending the run,
so a plan identity never needs `create`. This is the same "plans and never
applies" split
[`iam/render-policy.sh --read-only`](../../record-store-bucket/iam/render-policy.sh)
makes for the bucket's role (GitHub issue #1370), and it is why a plan
identity is refused by name until an apply identity has written the
estate's sentinel at least once: a store with no sentinel is
indistinguishable from an empty estate.

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: choudoufu-plan
  namespace: tofu-records-prod
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: choudoufu-records-plan
  namespace: tofu-records-prod
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: choudoufu-records-plan
  namespace: tofu-records-prod
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: choudoufu-records-plan
subjects:
  - kind: ServiceAccount
    name: choudoufu-plan
    namespace: tofu-records-prod
```

## What this does not grant

Neither Role touches `estates.choudoufu.intentius.io`, the virtual resource
[`live/kubernetes/estate-boundary.yaml`](../../../live/kubernetes/estate-boundary.yaml)'s
admission policy fences a write with. That grant is cluster-scoped and
handed out one estate and one principal at a time with
[`live/kubernetes/estate-grant.yaml`](../../../live/kubernetes/estate-grant.yaml)
(the "estate grant"):

```
sed -e 's/ESTATE/prod/g' -e 's/PRINCIPAL_NAMESPACE/tofu-records-prod/g' -e 's/PRINCIPAL/choudoufu-apply/g' \
  live/kubernetes/estate-grant.yaml | kubectl apply -f -
```

An apply identity needs both: this Role, to read and write Secrets in its
own namespace, and that grant, to get past the boundary policy when it
does. A plan identity needs neither the grant nor a write verb here, since
admission is only consulted for a write and a plan makes none -
`checkEstateBoundary` reports the grant as "reported and not required" for
an identity whose `RequiredVerbs` carry no write.

## Parameters

The one required parameter is the estate name; everything else follows the
store's own defaults, the same table
[`examples/record-store-cluster/README.md`](../README.md#parameters)
gives for the whole project:

```
render-role.sh <estate> [--namespace <namespace>] [--read-only]
```

`--namespace` overrides `tofu-records-<estate>`, for an estate whose
`record_store "kubernetes"` block sets its own `namespace` argument. Give
it the same string that block sets, or a plan identity and an apply
identity end up bound in a namespace the estate never uses.
