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
nothing to lose: the object is in the cluster and a read gives it back. Two
kinds depend on the record.

A resource with no live object is known only by its record. The `random_*`,
`tls_*` and `time_*` types are the usual ones, and so is `terraform_data`;
a `random_password` feeding a Secret's `data` is the shape a Kubernetes
estate meets first. Lose the record and the next plan proposes creating the
resource again, with a new value.

A `kubernetes_manifest`'s record holds the label and annotation keys the
configuration last declared, because the object itself cannot say which of
its keys came from your configuration and which a controller or a webhook
added. Without that record a label removed from the configuration is not
planned for removal
([claim 27](../smoke/claims/k8s-a-label-is-a-change.md)).

One operator can leave the records in the implied `local` store, on the
machine that applies. A team needs a store both operators and CI can read,
and for a Kubernetes-only estate that is the cluster itself, with no AWS
account anywhere:

```hcl
record_store "kubernetes" {}
```

Each record is one Secret, named `tofu-record-` and the SHA-256 of its key,
labelled `tofu-estate` with the record's key in an annotation, written
conditionally on `resourceVersion` with no Lease. It goes in
`tofu-records-<estate>` unless a `namespace` argument names another. Create
that namespace yourself, one per estate: the store does not create it, and a
namespace that is not there is refused by name with the `kubectl` line that
makes it, because a list in an absent namespace answers empty and an empty
listing reads as an estate with no records. RBAC cannot condition on a label,
so the namespace is what keeps one estate out of another's records
([claim 39](../smoke/claims/k8s-records-in-the-cluster.md)).

The Role that estate's identity needs is `get`, `list`, `create`, `update`
and `delete` on `secrets` in that namespace and nowhere else. A job that only
plans needs `get`, `list` and `create`, and not `update` or `delete`. It
writes nothing: measured on kind, no record Secret's `resourceVersion` moved
across a plan. `create` is there because opening the store asks the API
server to create the sentinel, which is already there, and RBAC answers the
verb before the object's existence comes into it. Taking `create` away stops
the run at the handshake, not at a record:

```
Error: Cannot open the record store

The live block's record_store "kubernetes" could not be opened: record_store:
provisioning the sentinel at "tofu-records/<estate>/.store-sentinel":
staterecord: kubernetes: creating "tofu-records/<estate>/.store-sentinel" in
namespace "tofu-records-<estate>": secrets is forbidden: User
"system:serviceaccount:default:planner" cannot create resource "secrets" in
API group "" in the namespace "tofu-records-<estate>".
```

[#1370](https://github.com/INTENTIUS/choudoufu/issues/1370)'s tolerance for a
run that may read the store and not write it does not reach this store yet. It
turns on `staterecord.IsAccessDenied`, which knows S3's `AccessDenied` and a
bare 403 and the local store's `EACCES`, and a Kubernetes `Forbidden` is
neither, so the denial goes down `provisionStoreSentinel`'s default branch and
ends the run. [#1393](https://github.com/INTENTIUS/choudoufu/issues/1393) has
the question; until it is answered, grant the plan job `create`.
[Where things are stored](https://intentius.io/choudoufu/docs/use/storage/#the-cluster) has
the rest, including what a `get secrets` in that namespace is worth to
whoever holds it.

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
