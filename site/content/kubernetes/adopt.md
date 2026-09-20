---
title: "Adopt"
weight: 1
description: "One label is the marker, objects bind by namespace and name, and a stock state file migrates with one command."
deeper:
  - "[`live/kubernetes/ADOPT.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/kubernetes/ADOPT.md): this page in full, with every measurement and issue."
  - "[#1016](https://github.com/INTENTIUS/choudoufu/issues/1016): the research and the marker decision."
---

# Adopt

On Kubernetes the marker is one label, `tofu-estate`, written when the object
is created. There is no address on the object. Kubernetes already gives every
object a natural key, its kind, namespace and name, and the name is in your
configuration, so a plan finds the object again by that key.

Every `kubernetes_*` type with a `metadata` block works this way, and so does
every custom resource declared through `kubernetes_manifest`, which binds by
the `apiVersion`, `kind`, namespace and name inside its manifest.

## From a stock state file

```
choudoufu live-import -state=stock.tfstate -estate=my-estate
choudoufu live-import -state=stock.tfstate -estate=my-estate -approve
```

Each object is verified by namespace and name, and the label is written. A
write that would change anything beyond the labels is refused, and so is an
object already labelled for another estate. If the state is in the
`kubernetes` backend, `tofu state pull > stock.tfstate` gives you the file.
Keep the backend's Secret until you trust the migration.

## Refused

`generate_name` is refused, because the server would choose the name and
nothing could find the object again. A namespaced object with no `namespace`
is refused and not defaulted. Objects a controller made, Pods from a
Deployment for example, are never adopted or deleted.
