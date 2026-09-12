---
title: "Operate"
weight: 3
description: "Rename is a config edit, a version bump is not a move, and deletion is refused until controller-created objects are excluded."
deeper:
  - "[#1016](https://github.com/INTENTIUS/choudoufu/issues/1016), \"Deletion, which is where I would refuse rather than port\": the four hazards in order."
  - "[The ownership policy matrix]({{< relref \"/docs/use/ownership-policy\" >}}): the verbs this would be refusing."
---

# Operate

## Rename

With an estate-only label there is no address on the object to rewrite. A
`moved` block is a config-line rename and the natural key is unchanged, so
the plan is empty. `live-mv` has nothing governed to do and says so.

An `api_version` change is not a move either. Uniqueness is per group,
resource, namespace and name; the version is a representation. The rename
rule has no vocabulary for that yet.

## Two runs at once

Server-side apply is the conflict primitive: a 409 names the competing
manager and the contested paths, with force an explicit opt-in. That is a
finer version of what this fork does on AWS, where a collision is two live
objects claiming one address.

## Remove

On AWS, an object carrying this estate's marker that no block declares is
proposed for deletion under the default policy, because the marker is its
own scope. The same default holds on Kubernetes since the sweep landed
([claim 22]({{< relref "/docs/claims/k8s-no-silent-orphans" >}})), and it
is safe because two exclusions run before anything reaches a delete
quadrant, either sufficient: an object with a non-empty
`metadata.ownerReferences` (a ReplicaSet's from its Deployment, a Pod's
from its ReplicaSet, a PVC's from its StatefulSet), and an object whose
every `metadata.managedFields` manager is the control plane (the legacy
`Endpoints` the endpoints controller mirrors a Service's labels onto). Both
were made by a controller, not declared. A controller copies template
labels, so an estate label in a pod template lands on objects nobody
declared; those are exactly what the exclusions keep out.

Two hazards remain the operator's, as they are on AWS: a finalizer makes a
delete return success while the object stays until the finalizer clears,
and deleting a marked Namespace takes everything inside it. The plan names
the object; the approval is yours.
