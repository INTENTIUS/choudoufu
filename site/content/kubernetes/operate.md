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

Renaming a block rewrites the address annotation beside the label
(#1639), through `live-mv <old> <new>` or the next plan and apply.
Changing a type's version suffix, `kubernetes_config_map` to
`kubernetes_config_map_v1`, still replans empty. Moving an object to
another estate is a relabel, through `live-mv -from-estate` or `kubectl
label --overwrite`; for a `kubernetes_manifest` object `live-mv` sends it
as one label patch through the cluster's API (#1104), the same governed
write `kubectl label` makes.

### Records

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
needs `get` and `list` on those Secrets and nothing more, once an identity
that may write has applied the estate once.

The first contact also checks the cluster: the records namespace and this
identity's access, read isolation, the API server's encryption flag, and the
estate boundary policy. Each failure names its fix. A stock
kind cluster fails the last two, so a demo adds
`allow_insecure = ["encryption_at_rest", "estate_boundary"]` to the
`record_store` block. `choudoufu live-cluster` prints the report.

## Remove

An object carrying your estate's label that no block declares is proposed for
deletion. Excluded first: owner-referenced objects, anything only the control plane
wrote, and a live Helm release's objects. A controller copies a pod
template's labels onto Pods nobody declared; those are never yours to delete.

## Scale

Does not apply: the natural key already includes each instance's name, so
scaling needs no extra tag.

## Plan, review, apply

The same as [AWS]({{< relref "/aws/operate" >}}): the approval check belongs
to the engine, not the substrate.

## From CI

The same pipeline as [AWS]({{< relref "/aws/operate" >}}): the jobs run the
same commands, regardless of substrate.

## Two runs at once

Server-side apply settles it. The loser gets a 409 that names the other
manager and the contested fields.
