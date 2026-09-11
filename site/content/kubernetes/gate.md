---
title: "Gate"
weight: 2
description: "RBAC cannot express the fence. An admission policy can, for writes, cluster-wide, and until one is installed the label is advisory."
deeper:
  - "[#1016](https://github.com/INTENTIUS/choudoufu/issues/1016), \"Claim 13's boundary is advisory\": the exact RBAC and admission surfaces, verified against apimachinery."
  - "[The AWS gate page]({{< relref \"/aws/gate\" >}}) for the shape this is being compared to."
---

# Gate

On AWS the marker is an authorization primitive out of the box: an IAM
condition on `aws:ResourceTag` fences reads and writes per resource, and
handover is two IAM changes. Kubernetes does not have that, and this page
says so in the headline rather than in a caveat.

## What RBAC cannot do

A `PolicyRule` has exactly `verbs`, `apiGroups`, `resources`,
`resourceNames` and `nonResourceURLs`. There is no selector, no condition
and no attribute predicate. `resourceNames` is a static allowlist of names.
Nothing in RBAC can say "may update objects carrying this label".

## What admission can do

`ValidatingAdmissionPolicy` is GA in `admissionregistration/v1`. Its CEL sees
`object`, `oldObject`, `request` and `authorizer`, and its `objectSelector`
is a label selector. `oldObject` is what fences a write by the label an
object already carries, which is the `aws:ResourceTag` semantic.

Two structural differences stay in the headline:

- Admission covers create, update, delete and connect. There is no read or
  list admission, so this fence is write-only where an IAM condition can
  fence a describe.
- A policy is a cluster-wide object a cluster admin installs. Handover on AWS
  is two IAM changes and no tag writes; here it is a change to a shared
  cluster object with a different blast radius.

Kyverno and Gatekeeper are the out-of-tree alternatives. Their exact
capability surface has not been verified for this.

## What that means for the claims

Until an admission policy is installed, the label is advisory: it answers
"who owns this" for any reader, and gates nothing. The claims page marks
claim 13 restated for Kubernetes, with this sentence as the note, and the
carve-by-retag claim the same way.
