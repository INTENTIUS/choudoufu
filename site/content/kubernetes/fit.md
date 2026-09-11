---
title: "Fit"
weight: 4
description: "Which Kubernetes resources plan today, which are refused, and how a mixed EKS estate is reported."
deeper:
  - "[Compatibility reference]({{< relref \"/docs/use/compatibility#your-provider\" >}}): the three cases for a non-AWS provider."
  - "[#996](https://github.com/INTENTIUS/choudoufu/issues/996): a real estate that spans providers, reported root by root."
  - "[Resource tier lookup]({{< relref \"/docs/use/resource-tiers\" >}}) is AWS-only; a Kubernetes tier table does not exist yet."
---

# Fit

Run `choudoufu live-check` in your configuration directory; it needs no
cluster and reports each refusal by name.

## What plans today

The four types with ratified rows: `kubernetes_config_map`,
`kubernetes_cluster_role_binding`, `kubernetes_namespace`,
`kubernetes_storage_class`. Each resolves from `metadata.name` and
`metadata.namespace` and plans without a marker.

## What does not

Every other `kubernetes_*` type is refused as `unadmitted-type`. The
provider's published identity schema names `api_version` and `kind`, which
are constants the provider hardcodes per type and which never appear in a
configuration, so the schema fallback that admits eleven `google_*` types
cannot reach this provider. A structural rule keyed on the `metadata` block
convention could plausibly reach most of the provider's types with no
per-type row, but that is a four-type sample, not a survey.

`kubernetes_manifest` is a documented exception: its import flow does not
use a name-based id at all. `helm_*` has not been assessed.

## Mixed estates

The common shape is an EKS module that also manages the `aws-auth`
ConfigMap. The AWS resources are marker-carried and governed; the ConfigMap
resolves and plans and carries no marker. `live-check` reports that root as
not blocked, and the ConfigMap's gaps are the ones the [adopt
page]({{< relref "/kubernetes/adopt" >}}) names. An estate with a root made
only of unadmitted types is blocked as a whole, and the report says which
root and why.
