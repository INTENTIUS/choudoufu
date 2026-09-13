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
key that names the object is not known until the value exists. Since the
ruling's second unit the object carries the same one label as every
built-in type: the plan writes `tofu-estate` into
`manifest.metadata.labels` on create, merged with any labels the manifest
declares, so the admission policy fences it like any other object, and
the estate sweep lists every kind the cluster serves, CRDs included, so
an object whose block is removed is found by that label and proposed for
removal at `kubernetes_manifest.orphan_<kind>_<namespace>_<name>` ([claim
24]({{< relref "/docs/claims/k8s-custom-resource" >}})). A block whose
apiVersion and kind the cluster does not serve - the CRD not installed,
or served at another version - is refused by name at the plan's first
contact with the cluster, ahead of the provider's own error: the block,
the kind, the apiVersion and the CRD to install (`Kubernetes kind not
served by the cluster`). `live-check` is offline and cannot ask a
cluster, so it does not raise this; a cluster that cannot answer is a
warning, never a refusal.

Once the plan exists, every planned create or update of a
`kubernetes_manifest` instance is sent to the API server as the apply
would write it, label included, with `dryRun=All`
([#1081](https://github.com/INTENTIUS/choudoufu/issues/1081), item 3):
the server validates it against the kind's schema, applies its defaults
and runs every admission policy, and persists nothing. The answer prints
above the plan, one line per object; a rejection is `Kubernetes API
server rejected the planned object`, quoting the server, and the run
stops with nothing rendered and nothing applied, on `plan` and on
`apply` alike. This reaches the manifest shape only. A built-in type's
block is not submitted, because the mapping from `metadata[0]` and its
spec blocks to the API object is the provider's own and is not reproduced
here; an object whose namespace the same plan creates is reported rather
than submitted, since the server would answer for the apply's order and
not for the object; a server that cannot answer is a warning. `live-check`
does not ask.

`live-mv` runs on every object-metadata type: a rename within an estate
reports nothing to write and exits 0, and `-from-estate` rewrites the
`tofu-estate` label through the provider under your own credential, so the
admission policy judges it like any other write
([#1081](https://github.com/INTENTIUS/choudoufu/issues/1081)); a move of a
`kubernetes_manifest` object is refused by name with the equivalent
`kubectl label`.

Still refused: the handful of types whose block is not object metadata
(`kubernetes_labels`, `kubernetes_annotations`, `kubernetes_env`, the
`*_data` patch types), which act on an object rather than being one.

`helm_release` is refused, and the refusal is the ordinary unadmitted-type
one, with or without the provider's schema
([#1081](https://github.com/INTENTIUS/choudoufu/issues/1081), item 4).
hashicorp/helm 3.2.0 serves the type with no resource identity schema and
no object-metadata block (its `metadata` is a computed record of release
facts, with no labels map), so neither admission route reaches it, and
`live-check` says so in those words. It stays refused rather than joining
the record rung because a release is not one object this tool creates,
reads and deletes: it is a release secret in the release namespace plus
whatever the chart rendered, made by a path this tool never sees, and
those objects carry the chart's labels and Helm's own
`meta.helm.sh/release-name` annotation, not the estate's label. The sweep
never lists them, so nothing needs excluding, and the record rung would
hold a name for a sub-estate with its own state and history that no
marker here reaches. Helm keeps its estate and this tool keeps its own. A
chart's objects can be owned here by declaring them: render the chart
(`helm template`, or the same provider's `helm_template` data source) into
`kubernetes_manifest` blocks and every one of them is admitted, labelled,
swept and fenced. Do not put `tofu-estate` in a chart's values: an object
carrying it that no block declares is an orphan, and the sweep will
propose removing it from under the release.

## Mixed estates

The common shape is an EKS module that also manages the `aws-auth`
ConfigMap. The AWS resources carry markers and fall under your IAM; the
ConfigMap plans and carries nothing. `live-check` reports that root as not
blocked. A root made only of refused types is blocked as a whole, and the
report says which root and why.
