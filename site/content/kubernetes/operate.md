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
own scope. On Kubernetes that default is refused until four hazards are
handled, in the order they should worry you:

1. A controller copies template labels. A Deployment's pod-template labels
   reach its ReplicaSets and Pods; a StatefulSet's volumeClaimTemplate labels
   reach its PVCs. A marker in a template lands on objects nobody declared,
   and every one of them becomes a delete candidate. A wrong marker is
   silent; this is a wrong marker nobody wrote.
2. An owner still exists. Deleting a controller-created object triggers a
   recreate, which a plan cannot tell from convergence failing.
3. A finalizer blocks. The delete returns success, sets `deletionTimestamp`,
   and the object stays. The next plan finds it still there and still
   marked.
4. Propagation policy is unset, so deleting a marked Namespace takes
   everything inside it, marked or not.

The cheapest guard for the first two is a non-empty
`metadata.ownerReferences` test before anything reaches a delete quadrant.
The `default` ServiceAccount and `kube-root-ca.crt` exist in every namespace
with no owner reference on some versions; they are safe under a keep default
and a trap for anyone setting an account-wide delete.
