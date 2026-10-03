---
title: "Compatibility"
weight: 4
description: "Every type with object metadata plans, custom resources included, and a short list is refused by name."
deeper:
  - "[`live/kubernetes/COMPATIBILITY.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/kubernetes/COMPATIBILITY.md): this page in full, with every measurement and issue."
  - "[#1016](https://github.com/INTENTIUS/choudoufu/issues/1016): the research and the marker decision."
---

# Compatibility

## The backend

Refused for the same reason as on [AWS]({{< relref "/aws/compatibility" >}}):
the rule is provider-agnostic.

## Expansions and identity arguments

Does not apply: the label holds only the estate name, not an address built
from `count` or `for_each`.

## Resource types

Every `kubernetes_*` resource type whose schema has a `metadata` block works:
it plans, carries the estate label, is swept for orphans and is fenced by the
admission policy. Custom resources work through `kubernetes_manifest`.

Before a plan proposes a `kubernetes_manifest` object, it sends the object to
the API server as a dry run and prints the server's verdict. A manifest the
server would reject refuses the plan, in the server's own words. A block whose
kind the cluster does not serve is refused by name, with the CRD to install.

### Labels, annotations, env, data and taints on someone else's object

`kubernetes_labels`, `kubernetes_annotations`, `kubernetes_env`,
`kubernetes_config_map_v1_data`, `kubernetes_secret_v1_data` and
`kubernetes_node_taint` patch fields of an object they do not own. Their
marker is the server-side-apply field manager, `choudoufu:<estate>`, which
the plan sets for you, so two estates can each own one field of the same
object. A plan that would force a field another estate owns is refused by
name; force against kubectl or a controller works as usual. Keep each
object to one of these blocks per estate. Removing a block does not yet
remove its fields.

### Refused

| What | Why |
|---|---|
| `metadata.generate_name` | The server picks the name, so the object cannot be found again. Set `name` |
| A namespaced object with no `namespace` | Refused and not defaulted, for the same reason |
| `helm_release` | A release is many objects Helm makes. Run Helm roots without a `live` block, where they behave as stock. An object carrying Helm's release annotation is controller-held while that release exists: never swept or adopted, even with `tofu-estate` in chart values; listed with its release |

## Running it

The same plan and apply engine as AWS: `-target` and the plan-file workflow
are not provider-specific.

## Other providers

An EKS module managing the `aws-auth` ConfigMap works. EKS creates it
unlabelled; the plan stops until `declared_untagged = "adopt"` claims it. AWS
resources carry two tags, the ConfigMap one label.
