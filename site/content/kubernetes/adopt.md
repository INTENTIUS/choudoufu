---
title: "Adopt"
weight: 1
description: "One label is the marker, objects bind by namespace and name, and a stock state file migrates with one command."
deeper:
  - "[`live/kubernetes/ADOPT.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/kubernetes/ADOPT.md): this page in full, with every measurement and issue."
  - "[#1016](https://github.com/INTENTIUS/choudoufu/issues/1016): the research and the marker decision."
---

# Adopt

On Kubernetes the marker is one label, `tofu-estate`; a plan finds the
object again by its own kind, namespace and name, already in your
configuration, not by address. Since #1639 it also carries the block
address in an annotation beside the label, a join key for the sweep and
`live-mv`.

Every `kubernetes_*` type with a `metadata` block works this way, and so does
every custom resource declared through `kubernetes_manifest`, which binds by
the `apiVersion`, `kind`, namespace and name inside its manifest.

## The bulk path

```
choudoufu live-import -state=stock.tfstate -estate=my-estate
choudoufu live-import -state=stock.tfstate -estate=my-estate -approve
```

Each object is verified by namespace and name, and the label and address
annotation are written. A write that would change anything beyond them is
refused, and so is an object already labelled for another estate.

## What binds on its own

One group, not three: every object binds by the natural key already in your
configuration. Objects a controller made, such as a Deployment's Pods, are
never adopted or deleted. [Compatibility]({{< relref "/kubernetes/compatibility" >}})
has the rest of what is refused by name.

## When it goes wrong

Does not apply: the label is set on the create call itself, with no
separate write for a crash to land between.

## Leaving

If the state is in the `kubernetes` backend, `tofu state pull >
stock.tfstate` gives you the file. The backend's Secret is the way back to
stock, so keep it until you trust the migration and delete it last.
