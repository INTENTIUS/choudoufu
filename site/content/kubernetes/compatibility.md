---
title: "Compatibility"
weight: 4
description: "Which Kubernetes resources plan today, which are refused and why, and how a mixed EKS estate is reported."
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

`kubernetes_config_map`, `kubernetes_cluster_role_binding`,
`kubernetes_namespace` and `kubernetes_storage_class`. Each resolves from
`metadata.name` and `metadata.namespace`, literal or from a variable, and
plans without a marker. A block with no namespace is refused rather than
defaulted to `default`.

## Refused

Every other `kubernetes_*` type, as `unadmitted-type`. The provider's
identity schema names `api_version` and `kind`, which are constants it
hardcodes per type and which never appear in a configuration, so the schema
fallback that admits `google_*` types cannot reach it. A rule keyed on the
`metadata` block shape would probably reach most of the provider without a
row per type; that is a guess from four types, not a survey.

`kubernetes_manifest` imports by a different mechanism and is a separate
case. `helm_*` has not been assessed.

## Mixed estates

The common shape is an EKS module that also manages the `aws-auth`
ConfigMap. The AWS resources carry markers and fall under your IAM; the
ConfigMap plans and carries nothing. `live-check` reports that root as not
blocked. A root made only of refused types is blocked as a whole, and the
report says which root and why.
