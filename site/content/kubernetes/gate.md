---
title: "Gate"
weight: 2
description: "One admission policy on the label fences every write, and granting an estate is an ordinary ClusterRole."
deeper:
  - "[`live/kubernetes/GATE.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/kubernetes/GATE.md): this page in full, with every measurement and issue."
  - "[#1016](https://github.com/INTENTIUS/choudoufu/issues/1016): the research and the marker decision."
---

# Gate

RBAC cannot say "may update objects carrying this label". It has no
conditions. One admission policy can.

```
kubectl apply -f live/kubernetes/estate-boundary.yaml
```

A cluster admin installs that `ValidatingAdmissionPolicy` once. On every
create, update and delete it reads the `tofu-estate` label on the object as
it is and as it would become, and asks the API server whether the caller
holds `use` on `estates.choudoufu.intentius.io/<estate>`.

Granting an estate is therefore an ordinary ClusterRole and binding
(`live/kubernetes/estate-grant.yaml`). Handing an estate over is moving that
binding. Moving an object between estates is a label rewrite, and the caller
must hold both estates.

The policy binds the credential and not the tool: `kubectl` under a
ServiceAccount that does not hold the estate is refused by the API server,
and so is choudoufu under the same account.

## What it does not fence

Reads. `get` and `list` never reach admission, so use namespaces for those.
It does not fence subresources such as `scale` and `status`. And
`cluster-admin` holds every estate, the way an account root does on AWS.
