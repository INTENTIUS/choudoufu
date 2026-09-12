---
title: "Compatibility"
weight: 4
description: "Every object-metadata type plans through one rule; what is refused by name, and how a mixed EKS estate is reported."
deeper:
  - "[Compatibility reference]({{< relref \"/docs/use/compatibility#your-provider\" >}}): the three cases for a non-AWS provider."
  - "[#326](https://github.com/INTENTIUS/choudoufu/issues/326): how the four types came to resolve."
  - "[#996](https://github.com/INTENTIUS/choudoufu/issues/996): a real estate that spans providers, reported root by root."
---

# Compatibility

```
choudoufu live-check
```

No cluster needed. On a root that declares Kubernetes resources it prints
one of two things per block.

## Plans today

Every type whose schema carries Kubernetes object metadata: a `metadata`
block with a `name`, a `uid`, a `labels` map and, for a namespaced kind, a
`namespace`. That is the object-metadata rule
([#1064](https://github.com/INTENTIUS/choudoufu/issues/1064)): the
identity is `NAMESPACE/NAME`, or `NAME` for a cluster-scoped kind, read
from the block, and no type needs a row of its own to say so. The four
types that had ratified rows before the rule existed keep them as a check
on it. Counted against `live/MARKERS.md`'s figure for the provider at
3.2.1, that is nearly every resource type the provider ships.

A namespace read from another resource's metadata, `namespace =
kubernetes_namespace.x.metadata[0].name`, resolves: the name is that
resource's identity attribute, and the reference is followed into the
block.

## Refused

`metadata.generate_name`, by name: the server mints the object's name, so
nothing in the configuration states the join key back to the block, which
is the one shape that would need the configuration address on the object.
Set `metadata.name` instead. A missing `namespace` on a namespaced kind is
refused rather than defaulted, for the same reason.

`kubernetes_manifest` takes a whole manifest as one attribute and imports
by a different mechanism; it stays `unadmitted-type`. So do the handful of
types whose block is not object metadata (`kubernetes_labels`,
`kubernetes_annotations`, `kubernetes_env`, the `*_data` patch types),
which act on an object rather than being one. `helm_*` has not been
assessed.

## Mixed estates

The common shape is an EKS module that also manages the `aws-auth`
ConfigMap. The AWS resources carry markers and fall under your IAM; the
ConfigMap plans and carries nothing. `live-check` reports that root as not
blocked. A root made only of refused types is blocked as a whole, and the
report says which root and why.
