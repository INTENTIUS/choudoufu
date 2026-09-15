---
title: "Claim 22: No silent orphans on Kubernetes"
weight: 22
claim: k8s-no-silent-orphans
---

# Claim 22: No silent orphans on Kubernetes

Delete a resource block and stock forgets the object: it is in the
cluster, it is nobody's, and no plan will ever mention it again. Here the
object carries the estate's label, and the next plan lists the estate by
that label, one cluster-wide list per kind, so the object walks into the
plan as a removal. The hazard on Kubernetes is that a controller copies a
Deployment's template labels onto ReplicaSets and Pods nobody declared;
this scenario puts the estate label in a template on purpose, and the plan
must propose the one orphan and never a copy.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and kind and kubectl are installed. If Go is not
installed, export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke k8s-no-silent-orphans

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke k8s-no-silent-orphans and report the "caught" line:
the scenario strips the orphan's label and the replan must leave it
alone.
```

The steps, in the order they print:

1. `a Kubernetes estate with a Deployment whose pod template carries the
   estate label` - six objects; the Deployment's template copies
   `tofu-estate` onward.
2. `the controller's copies exist and carry the label` - a ReplicaSet and a
   Pod carry the label, undeclared, each with an owner reference.
3. `delete the ConfigMap's block from source` - the plan proposes exactly
   one destroy, the ConfigMap, and no Pod or ReplicaSet.
4. `apply - the orphan goes, the copies stay`.
5. `the replan is empty, and destroy removes exactly what remains`.

The `BREAK=1` run strips the label from the orphaned ConfigMap after step
3. The replan must leave it alone: with no marker the object is foreign,
and a plan that still destroyed it would be acting on something other
than the marker.

What is excluded, and why: an object with a non-empty
`metadata.ownerReferences` was made by a controller from a template, and
an object whose every `metadata.managedFields` manager is the control
plane was made by a controller too - the legacy `Endpoints` the endpoints
controller mirrors a Service's labels onto is the case that has no owner
reference. Neither is ever an orphan. A kind the provider has no built-in type for,
every custom resource, is listed too, under `kubernetes_manifest`; [claim
24]({{< relref "/docs/claims/k8s-custom-resource" >}}) removes one that
way. What fences a write on the label is the admission
policy of [claim 23]({{< relref "/docs/claims/k8s-the-label-is-the-boundary" >}}),
which excludes a controller's objects by the same owner-reference rule.
