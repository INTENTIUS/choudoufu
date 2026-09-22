# The record store cluster

A live estate that declares `record_store "kubernetes"` keeps its records
as Secrets in one namespace of a cluster it already runs on, with no AWS
account anywhere. This project creates that namespace and the two
identities that use it, tells you whether the cluster is correct, and takes
the namespace down again. It is the cluster half of
[`examples/record-store-bucket`](../record-store-bucket/README.md).

```
just up prod       # namespace, two ServiceAccounts, two Roles, two RoleBindings, the estate grant
just verify prod   # is this cluster correct for the estate? asks the choudoufu binary
just down prod     # delete the namespace and the grant; refuses while it holds a record
```

Every recipe works on the current kubeconfig context, as a cluster admin.
Then the estate's live block needs nothing but the store's name, because
`tofu-records-prod` is the namespace `record_store "kubernetes" {}`
resolves to for estate `prod`:

```hcl
terraform {
  live {
    estate = "prod"

    record_store "kubernetes" {}
  }
}
```

One namespace per estate. Adding an estate is another `just up <estate>`,
and the reason is in [CONTRACT.md](CONTRACT.md): RBAC cannot condition on a
label, so the namespace is the only read boundary there is.

## What a correct cluster is

[CONTRACT.md](CONTRACT.md) is the specification: four settings choudoufu
asserts on an estate's first contact with the store and before any run that
writes a record, and what each Role here can and cannot do. This project is
one way to meet the two that are the namespace's - `namespace_access` and
`read_isolation` - plus the grant `estate_boundary` asks for. A namespace and
Roles built with your own tooling are just as correct if the authorizer
answers the same questions the same way. The other two are the cluster's:
the estate boundary policy is installed once per cluster by an admin with
`kubectl apply -f live/kubernetes/estate-boundary.yaml`, and encryption at
rest is an API server flag no namespace can set.

## The two identities

`just up <estate>` creates two ServiceAccounts in the records namespace,
each bound to a Role that holds exactly the verbs one kind of run uses on
Secrets there, taken from the store's own lists in
`internal/live/staterecord/kubernetescontract.go`:

| Identity | Verbs on Secrets in `tofu-records-<estate>` | Estate grant | For |
|---|---|---|---|
| `choudoufu-plan` | `get`, `list` | none | A plan job. It reads records and writes none; the sentinel an apply left behind is what lets it proceed without write access |
| `choudoufu-apply` | `get`, `list`, `create`, `update`, `delete` | `use` on `estates.choudoufu.intentius.io/<estate>` | An apply, a `live-mv` that is not a dry run, a `live-import -approve` |

Neither holds `patch` or `watch`, and both are Roles bound in the records
namespace, never ClusterRoles. To run a job as one of them, mint a token
(`kubectl create token choudoufu-apply -n tofu-records-<estate>`) or bind
its Role to the principal your CI already has: edit the `subjects` of the
RoleBinding in `manifests/records.yaml` and the `PRINCIPAL` of the grant,
and run `just up` again. Nothing in the manifests assumes the
ServiceAccount shape.

A plan-only identity is refused by name until the estate has been applied
once by an identity that may write, because a store with no sentinel is
indistinguishable from an empty estate. Run the first apply as
`choudoufu-apply`.

## `just verify` reports the cluster, not a configuration

`just verify <estate>` runs `choudoufu live-cluster -namespace=tofu-records-<estate> -estate=<estate>`
under the current kubeconfig's identity and passes its verdict on. It asks
the same four questions a run asks, writes nothing, and never honours an
`allow_insecure` waiver: a verify that did would print green for a cluster
that fails. Extra arguments go through, so `just verify prod -plan-identity`
asks what a plan job needs instead of what an apply needs. Run it under the
identity you want the answer for; as a cluster admin `read_isolation` is
truthfully weaker, because an admin reads every namespace.

On kind and on any cluster whose API server Pod is not started with
`--encryption-provider-config`, `encryption_at_rest` fails. On a managed
control plane (EKS, GKE, AKS) it reads `NOT CHECKED`, which is not a pass;
[CONTRACT.md](CONTRACT.md) says why, and what a run does with it.

## Parameters

The one parameter is the estate name. Everything else is derived from it,
so the project agrees with the store's own defaults:

| | Value |
|---|---|
| Namespace | `tofu-records-<estate>` |
| ServiceAccounts | `choudoufu-plan`, `choudoufu-apply`, in that namespace |
| Roles and bindings | `choudoufu-records-plan`, `choudoufu-records-apply`, in that namespace |
| Grant | `ClusterRole choudoufu-estate-<estate>` and `ClusterRoleBinding choudoufu-estate-<estate>-choudoufu-apply`, from `live/kubernetes/estate-grant.yaml` |

A `namespace` argument in the `record_store` block overrides the default;
if you use one, name it here too by editing `manifests/records.yaml`, and
keep one namespace per estate.

## `just down`

Deletes the namespace and the estate grant, and refuses while the namespace
holds a record Secret (one labelled `app.kubernetes.io/managed-by=choudoufu`).
A record can be the only copy of what it says: a resource with no live
object, such as a `random_password`, is known only by its record, and
deleting the namespace deletes every one of them. Destroy the estate first.
The count is read from kubectl's answer and never from its exit code; a
kubectl that could not reach the server refuses too, because a count that
failed is not zero. The estate boundary policy is the cluster's and is left
in place.

## Where this runs

Any cluster. The manifests are plain RBAC and need nothing newer than the
`admissionregistration.k8s.io/v1` the boundary policy needs (Kubernetes
1.30 or later). In CI, `selftest.sh` applies the shipped recipes to a kind
cluster and reads the authorizer back with `kubectl auth can-i --as` for
every identity and verb above, then runs `just down` against a held record
and against an empty namespace; its `BREAK=1` control removes `update` from
the apply Role and must be caught by name. That job is
`.github/workflows/k8s-smoke.yml`, beside the Kubernetes claims, because it
needs a cluster and the ordinary gate has none. `live/record_store_cluster_test.go`
holds the Roles' verbs to the store's own lists and runs `just verify` and
`just down` with stubs in the ordinary gate, so a recipe that swallowed the
binary's verdict or read a failed count as zero fails there.

Run the selftest yourself with `bash examples/record-store-cluster/selftest.sh`
(needs kind, kubectl and just; creates and deletes a throwaway cluster), or
against a cluster you already have with `SELFTEST_KUBECONFIG=<path>`.
