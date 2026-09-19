---
title: "Compatibility"
weight: 4
description: "Every type with object metadata plans, custom resources included, and a short list is refused by name."
deeper:
  - "[`live/kubernetes/COMPATIBILITY.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/kubernetes/COMPATIBILITY.md): this page in full, with every measurement and issue."
  - "[#1016](https://github.com/INTENTIUS/choudoufu/issues/1016): the research and the marker decision."
---

# Compatibility

Every `kubernetes_*` resource type whose schema has a `metadata` block works:
it plans, carries the estate label, is swept for orphans and is fenced by the
admission policy. Custom resources work through `kubernetes_manifest`.

Before a plan proposes a `kubernetes_manifest` object, it sends the object to
the API server as a dry run and prints the server's verdict. A manifest the
server would reject refuses the plan, in the server's own words. A block whose
kind the cluster does not serve is refused by name, with the CRD to install.

## Refused

| What | Why |
|---|---|
| `metadata.generate_name` | The server picks the name, so the object cannot be found again. Set `name` |
| A namespaced object with no `namespace` | Refused and not defaulted, for the same reason |
| `kubernetes_labels`, `kubernetes_annotations`, `kubernetes_env`, the `*_data` types | They patch an object and are not one |
| `helm_release` | A release is many objects made by Helm, carrying Helm's labels. Run Helm roots without a `live` block, where they behave as stock |

## Mixed estates

An EKS module that also manages the `aws-auth` ConfigMap works. The AWS
resources carry two tags under your IAM, and the ConfigMap carries one label
under the cluster's admission policy.
