---
title: "Operate"
weight: 3
description: "Rename is a config edit, records live in the cluster, and removal skips anything a controller made."
deeper:
  - "[`live/kubernetes/OPERATE.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/kubernetes/OPERATE.md): this page in full, with every measurement and issue."
  - "[#1016](https://github.com/INTENTIUS/choudoufu/issues/1016): the research and the marker decision."
---

# Operate

## Rename

There is no address on the object, so renaming a block changes nothing live
and the plan is empty. Changing a type's version suffix,
`kubernetes_config_map` to `kubernetes_config_map_v1`, is the same. Moving an
object to another estate is a relabel, through `live-mv -from-estate` or
`kubectl label --overwrite`.

## Records

Every managed object has a record
([Records]({{< relref "/docs/model/values" >}})). Most cost nothing to lose.
Two do not: a resource with no live object, such as a `random_password`, and a
`kubernetes_manifest`, whose record holds the label and annotation keys the
configuration last declared.

A team keeps the records in the cluster, and no AWS account is involved:

```hcl
record_store "kubernetes" {}
```

Each record is one Secret in `tofu-records-<estate>`, written conditionally on
`resourceVersion` with nothing locked. Create that namespace yourself, one per
estate: the store does not, and RBAC cannot condition on a label. A plan job
needs `get`, `list` and `create` on those Secrets, because opening the store
asks to create a sentinel that is already there.

## Two runs at once

Server-side apply settles it. The loser gets a 409 that names the other
manager and the contested fields.

## Remove

An object carrying your estate's label that no block declares is proposed for
deletion. Two kinds are excluded first: anything with an owner reference, and
anything only the control plane wrote. A controller copies labels from a pod
template onto Pods nobody declared, and those are never yours to delete.
