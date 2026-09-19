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
marks each key inside it, and a prior read from the cluster
carries only the schema's mark, so the planner's sensitivity comparison
saw a difference on every run and proposed an in-place update it rendered
as unchanged. The comparison now reduces both sides to their minimal
cover first: a mark under an already-marked ancestor is not a change.

## Custom resources

`kubernetes_manifest`, and so every custom resource, plans
([#1079](https://github.com/INTENTIUS/choudoufu/issues/1079)). Its
identity is the natural key written inside the `manifest` argument's own
object constructor: `apiVersion`, `kind`, `metadata.namespace` and
`metadata.name`. The key is read without evaluating the manifest, and a
local or variable the argument is set to is walked the same way. A
manifest computed some other way, `yamldecode(file(...))` or a module
output, is refused by name, because the key that names the object is not
known until the value exists.

The object carries the same one label as every built-in type. The plan
writes `tofu-estate` into `manifest.metadata.labels` on create, merged
with any labels the manifest declares, so the admission policy fences it
like any other object. The estate sweep lists every kind the cluster
serves, CRDs included, so an object whose block is removed is found by
that label and proposed for removal at
`kubernetes_manifest.orphan_<kind>_<namespace>_<name>` ([claim
24]({{< relref "/docs/claims/k8s-custom-resource" >}})).

A block whose apiVersion and kind the cluster does not serve is refused by
name at the plan's first contact with the cluster (`Kubernetes kind not
served by the cluster`), naming the CRD to install. `live-check` is offline
and does not raise this, and a cluster that cannot answer is a warning.

Once the plan exists, every planned create or update of a
`kubernetes_manifest` instance is sent to the API server as the apply
would write it, label included, with `dryRun=All`
([#1081](https://github.com/INTENTIUS/choudoufu/issues/1081), item 3).
The server validates it against the kind's schema, applies its defaults
and runs every admission policy, and persists nothing. The answer prints
above the plan, one line per object. A rejection is `Kubernetes API
server rejected the planned object`, quoting the server, and the run
stops with nothing rendered and nothing applied, on `plan` and on
`apply` alike.

This reaches the manifest shape only. A built-in type's block is not
submitted. An object whose namespace the same plan creates is reported
rather than submitted, and a server that cannot answer is a warning.
`live-check` does not ask.

## Refused

`metadata.generate_name`, by name: the server mints the object's name, so
nothing in the configuration states the join key back to the block, which
is the one shape that would need the configuration address on the object.
Set `metadata.name` instead. A missing `namespace` on a namespaced kind is
refused rather than defaulted, for the same reason.

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

`helm_release` is refused in a live root, and the refusal is the ordinary
unadmitted-type one
([#1081](https://github.com/INTENTIUS/choudoufu/issues/1081), item 4;
ruled 2026-09-13 in
[#1105](https://github.com/INTENTIUS/choudoufu/issues/1105)).
hashicorp/helm 3.2.0 serves the type with no resource identity schema and
no object-metadata block, so neither admission route reaches it, and
`live-check` says so in those words. A release is more than one object:
it is a release secret in the release namespace plus whatever the chart
rendered. Those objects carry the chart's labels and Helm's own
`meta.helm.sh/release-name` annotation, and the estate's label is on none
of them, so the type stays refused.

Nothing about Helm is limited in stock OpenTofu, and this fork changes
nothing there: a root with no `live` block installs, upgrades and
uninstalls a `helm_release` exactly as stock does, with the release in the
state file and Helm's own release secret in the cluster. So a team on Helm
has two honest choices, and the trade between them is Helm's lifecycle
against ownership:

- **Keep the release root stock.** Put the Helm roots in a root of their
  own with no `live` block, beside the live estate. Helm keeps `helm
  rollback`, release history, hooks and chart-managed upgrades; the estate
  never claims the chart's objects, never sweeps them and never fences
  them, and `live-ls` does not show them. This is the default the ruling
  keeps.
- **Render the chart into manifests.** `helm template`, or the same
  provider's `helm_template` data source, renders the chart to YAML, and
  each object is declared as a `kubernetes_manifest` block. Every one of
  them is then admitted, labelled, swept, fenced and dry-run like any
  custom resource, and found again with no state file. What is given up is
  Helm's lifecycle: no rollback, no release history, no hooks, and an
  upgrade is a re-render and a plan.

A chart's own objects are never the estate's by accident. Under the ruling
an object carrying Helm's release annotation is controller-held: never
swept, never adopted, reported with its release name. That exclusion is
#1105's one unit and is not built yet, so until it lands do not put
`tofu-estate` in a chart's values: an object carrying it that no block
declares is an orphan today, and the sweep will propose removing it from
under the release. The opt-in that would bring a release inside the
boundary (identity through the release secret, the label written by a
post-renderer) is designed on #1105 and not built.

## Mixed estates

The common shape is an EKS module that also manages the `aws-auth`
ConfigMap. The AWS resources carry two tags and fall under your IAM; the
ConfigMap carries the one label and falls under the cluster's admission
policy, and each substrate's sweep lists its own. `live-check` reports
that root as not blocked. A root made only of refused types is blocked as a whole, and the
report says which root and why.
