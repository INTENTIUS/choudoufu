# Operate

## Rename

With an estate-only label there is no address on the object to rewrite. A
`moved` block is a config-line rename and the natural key is unchanged, so
the plan is empty. `live-mv` has nothing governed to do, says so, and
exits 0.

Moving an object between estates is a relabel, `tofu-estate=<new>` on the
object: `live-mv -from-estate=<old>` in the destination's configuration
makes it through the provider, and `kubectl label --overwrite` makes the
same write tool-less. With the admission policy installed either is a
governed one: the caller must hold both the estate the object is leaving
and the one it is entering ([claim
23](../smoke/claims/k8s-the-label-is-the-boundary.md)).
Handing a whole estate over is an RBAC change, the grant's binding moving
to the receiving principal, and nothing on the objects changes.

An `api_version` change needs no `moved` block either. The provider ships
most kinds under two spellings, such as `kubernetes_config_map` and
`kubernetes_config_map_v1`, and the suffix names the API version the block
is written against. Both spellings render the same `NAMESPACE/NAME`, the
sweep files both under the one kind, and the label carries no address to
rewrite, so a block that changes spelling with the same metadata replans
empty. [Claim 21](../smoke/claims/k8s-greenfield.md)'s step 6
measures this on kind: it rewrites the ConfigMap block from the plain
spelling to `_v1` with no `moved` block and the plan is `No changes.`
([#1081](https://github.com/INTENTIUS/choudoufu/issues/1081)). On AWS the
same edit with no `moved` block is a destroy and a create, because there
the type is part of the address the marker carries.

## Records

Every managed object has a record, the same as on AWS
([Records](https://intentius.io/choudoufu/docs/model/values/)). For most objects it costs
nothing to lose. Two kinds depend on it: a resource with no live object, such
as a `random_password` feeding a Secret, and a `kubernetes_manifest`, whose
record holds the label and annotation keys the configuration last declared.
Without that record a label removed from the configuration is not planned
for removal.

A team keeps its records in the cluster, and no AWS account is involved:

```hcl
record_store "kubernetes" {
  namespace = "my-estate-records"
}
```

Each record is one Secret in that namespace, labelled `tofu-estate`, written
conditionally on `resourceVersion` with no Lease. Give each estate its own
namespace, because RBAC cannot condition on a label and the namespace is what
keeps one estate out of another's records.
[Where things are stored](https://intentius.io/choudoufu/docs/use/storage/#the-cluster) has
the rest. A plan job needs `get` and `list` on those Secrets and nothing
more.

## Two runs at once

Server-side apply is the conflict primitive: a 409 names the competing
manager and the contested paths, with force an explicit opt-in. That is a
finer version of what this fork does on AWS, where a collision is two live
objects claiming one address.

## Remove

An object carrying this estate's marker that no block declares is
proposed for deletion under the default policy, on Kubernetes as on AWS
([claim 22](../smoke/claims/k8s-no-silent-orphans.md)). Two
exclusions run before anything reaches a delete quadrant, and either is
sufficient. One is an object with a non-empty `metadata.ownerReferences`,
such as a ReplicaSet's from its Deployment or a PVC's from its
StatefulSet. The other is an object whose every `metadata.managedFields`
manager is the control plane, such as the legacy `Endpoints` the endpoints
controller mirrors a Service's labels onto. A controller copies template
labels, so an estate label in a pod template lands on objects nobody
declared, and the exclusions keep those out.

Two hazards remain the operator's, as they are on AWS: a finalizer makes a
delete return success while the object stays until the finalizer clears,
and deleting a marked Namespace takes everything inside it. The plan names
the object; the approval is yours.
