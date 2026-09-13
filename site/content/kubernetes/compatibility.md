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
block. So does `namespace = kubernetes_namespace_v1.x.id`, the shape
grafana/quickpizza's root writes on every namespaced object: the
provider sets an object's `id` to its own import id (the name for a
cluster-scoped kind, `NAMESPACE/NAME` for a namespaced one), so the rule
claims `id` as an identity attribute and the reference resolves to the
parent's whole identity ([#1067](https://github.com/INTENTIUS/choudoufu/issues/1067)).

A `kubernetes_secret_v1` whose `data` keys read sensitive variables plans
empty after adoption. The same root found the case where it did not: the
provider's schema marks the whole `data` map sensitive, the configuration
marks each key inside it, and a stateless prior read from the cluster
carries only the schema's mark, so the planner's sensitivity comparison
saw a difference on every run and proposed an in-place update it rendered
as unchanged. The comparison now reduces both sides to their minimal
cover first: a mark under an already-marked ancestor is not a change.

## Refused

`metadata.generate_name`, by name: the server mints the object's name, so
nothing in the configuration states the join key back to the block, which
is the one shape that would need the configuration address on the object.
Set `metadata.name` instead. A missing `namespace` on a namespaced kind is
refused rather than defaulted, for the same reason.

`kubernetes_manifest`, and so every custom resource, plans since
[#1079](https://github.com/INTENTIUS/choudoufu/issues/1079)'s first unit:
its identity is the natural key written inside the `manifest` argument's
own object constructor - `apiVersion`, `kind`, `metadata.namespace`,
`metadata.name` - read key by key without evaluating the manifest (a local
or variable the argument is set to is walked the same way) and rendered as
the provider's own import id. A manifest computed some other way,
`yamldecode(file(...))` or a module output, is refused by name, because the
key that names the object is not known until the value exists. What such
an object does not carry yet is the label: the stamp into
`manifest.metadata.labels` is the ruling's next unit, and until it lands
nothing sweeps or fences an object declared this way ([claim
24]({{< relref "/docs/claims/k8s-custom-resource" >}})).

Still refused: the handful of types whose block is not object metadata
(`kubernetes_labels`, `kubernetes_annotations`, `kubernetes_env`, the
`*_data` patch types), which act on an object rather than being one.
`helm_*` has not been assessed.

## Mixed estates

The common shape is an EKS module that also manages the `aws-auth`
ConfigMap. The AWS resources carry markers and fall under your IAM; the
ConfigMap plans and carries nothing. `live-check` reports that root as not
blocked. A root made only of refused types is blocked as a whole, and the
report says which root and why.
