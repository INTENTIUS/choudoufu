---
title: "Adopt"
weight: 1
description: "What binds today, what a marker would look like, and the two shapes that are refused rather than guessed."
deeper:
  - "[#1016](https://github.com/INTENTIUS/choudoufu/issues/1016): the marker decision, the measurements behind it, and what a port drops rather than reimplements."
  - "[#326](https://github.com/INTENTIUS/choudoufu/issues/326): how the four types came to resolve, and why the blocker was never a marker carrier."
  - "[Compatibility reference]({{< relref \"/docs/use/compatibility#your-provider\" >}}): the three cases for a non-AWS provider."
---

# Adopt

## Today

`kubernetes_config_map`, `kubernetes_cluster_role_binding`,
`kubernetes_namespace` and `kubernetes_storage_class` carry ratified identity
rows. Their identity is client-named, `metadata.namespace` and
`metadata.name`, both written in your configuration, so a plan resolves and
binds them with nothing stored anywhere. A missing namespace is refused
rather than defaulted.

Every one of them, and every other type with a `metadata.labels` map, now
carries the marker: one label, `tofu-estate`, written on the create. Strip
it with kubectl and the next plan proposes restoring it. [Claim 21]({{< relref "/docs/claims/k8s-greenfield" >}})
runs that on a real cluster.

What they still do not do: get reached by an estate sweep, or fall under
any ownership condition. Delete one of those blocks from source and the
live object is orphaned with no run that will ever propose removing it.
That is the sweep gap, and it is the next unit.

## The marker

On AWS the marker carries the config address, because AWS hands back opaque
ids and the tag is the only way from a live object back to a line of
configuration. Kubernetes returns the natural key: group, kind, namespace and
name, with the name authored in the configuration this fork already parses.
So the address does not need to be on the object.

The marker is one label, `tofu-estate`, and re-binding goes through the
natural key. Measured against the identity golden set, nearly half of real config
addresses are illegal as a label value and a 63-character cap binds at once
on ordinary module-nested shapes; putting the address in a label would need
three or four continuation labels per object and would break the exact-match
condition a policy wants. An estate-only label fits by construction.

## Refused, not guessed

`generateName` lets the server mint the name, which makes the natural key
unknowable before the create. That is the one shape that would drag the
whole address-in-label machinery back in, so it is refused, the same way a
missing namespace is.

Controller-created objects, ReplicaSets and Pods from a Deployment, PVCs
from a volumeClaimTemplate, Jobs from a CronJob, are excluded by a non-empty
`metadata.ownerReferences` before anything reaches a delete. The author
chose neither the name nor the object.

## What Kubernetes does better

`metadata.managedFields` records which manager last wrote each field, so a
stripped label names who stripped it. Server-side apply refuses a contested
field with a 409 that names the competing manager. Server-side dry run
validates, defaults and runs admission without persisting, which is stronger
evidence than a locally computed plan and something AWS has no equivalent
for.
