---
title: "Gate"
weight: 2
description: "RBAC cannot express the fence. One admission policy on the label can, for writes, cluster-wide, and the grant is an ordinary ClusterRole."
deeper:
  - "[#1016](https://github.com/INTENTIUS/choudoufu/issues/1016), \"Claim 13's boundary is advisory\": the exact RBAC and admission surfaces, verified against apimachinery."
  - "[#1066](https://github.com/INTENTIUS/choudoufu/issues/1066): the unit that shipped the policy, and the design point of a virtual resource over a binding per estate."
  - "[The AWS gate page]({{< relref \"/aws/gate\" >}}) for the shape this is being compared to."
  - "Claim [23]({{< relref \"/docs/claims/k8s-the-label-is-the-boundary\" >}}): the fence holds against a plain kubectl call with no choudoufu in the process, on a real API server."
---

# Gate

On AWS the marker is an authorization primitive out of the box: an IAM
condition on `aws:ResourceTag` fences reads and writes per resource, and
handover is two IAM changes. Kubernetes does not have that. What it has is
admission, and this page says in the headline what admission fences and
what it does not.

## What RBAC cannot do

A `PolicyRule` has exactly `verbs`, `apiGroups`, `resources`,
`resourceNames` and `nonResourceURLs`. There is no selector, no condition
and no attribute predicate. `resourceNames` is a static allowlist of names.
Nothing in RBAC can say "may update objects carrying this label".

## What admission does

```
kubectl apply -f live/kubernetes/estate-boundary.yaml
```

One `ValidatingAdmissionPolicy`, GA in `admissionregistration/v1`,
installed once by a cluster admin. Its CEL reads `tofu-estate` off
`oldObject`, the object a write is about to change, which is the
`aws:ResourceTag` semantic, and off `object`, the object the write would
produce, which is the `aws:RequestTag` semantic. For each it asks the API
server's own authorizer whether the caller holds `use` on a virtual
resource named after the estate, `estates.choudoufu.intentius.io/<estate>`.
No such resource exists; the verb lives only in RBAC, which is the point.

So the grant is an ordinary ClusterRole
(`live/kubernetes/estate-grant.yaml`): `use` on `estates` named
`<estate>`, bound to a principal. Handover is that binding moving from one
principal to another. Nothing on the objects changes and the policy is
never edited. A `cluster-admin`'s wildcard rule matches the virtual
resource too, so `cluster-admin` holds every estate, the way the account
root does on AWS. `live/MARKERS.md`, "Granting a Kubernetes estate", has
both templates in full.

The fence binds the credential, not the binary. A plain `kubectl label`
under a ServiceAccount that does not hold the estate is refused by the API
server with the policy's own message, and so is choudoufu's own apply
under the same ServiceAccount. What the fence permits is not hidden from
the tool either: the next plan reads the live object, not a log of who
wrote it.

Splitting an estate is a label rewrite, then a grant. With no address on
the object, the write is `tofu-estate=<new>` on the object - `live-mv
-from-estate` makes it through the provider, and `kubectl label
--overwrite` makes the same write tool-less - and the policy reads both
sides of it: the caller must hold the estate the object is leaving and the
one it is entering.

## What it does not fence

Three things are true of this fence that are not true of the AWS one:

- Admission sees create, update and delete, never get or list. The fence
  is write-only where an IAM condition can fence a describe; reads are
  RBAC's alone.
- It fences the object, not its subresources. A `kubectl scale` or a
  status write arrives as a Scale or a status object carrying no label
  (measured on Kubernetes 1.36; a `*/*` rule does not change it). RBAC on
  `deployments/scale` is the fence for those.
- The policy is one shared cluster object with a wider blast radius than
  two IAM changes. A cluster admin installs it and any cluster admin can
  remove it, which is exactly what claim 23's `BREAK=1` run does.

The fence is also per estate, never per address: the label carries no
address by ruling, so a team that wants two boundaries makes two estates.

## What is exempt

The control plane: nodes, the `kube-system` controllers, the scheduler and
the API server itself, because kubelets write status and controllers write
the copies a pod template makes. And any object carrying an
`ownerReference`, because a controller made it from a template. That
second exemption is the same rule the estate sweep excludes by
([Operate]({{< relref "/kubernetes/operate" >}})), so the fence and the
sweep agree on what an estate contains.

Kyverno and Gatekeeper could express the same policy. Neither has been
verified for this.
