---
title: "Claim 1: Owned resources cannot fall out of plans unnoticed"
claim: no-silent-orphans
---

# Claim 1: Owned resources cannot fall out of plans unnoticed

## On AWS

When an apply crashes after the create call but before the write to
state, stock tooling orphans the resource: it exists and it bills, but no
plan will ever mention it again. Here the plan reads identity from the
resource's own tags, so a resource nobody remembers still walks into the
next plan by name.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke no-silent-orphans

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke no-silent-orphans and report the "caught" line: the
scenario creates the one shape the claim excludes and must fail to
claim it.
```

The steps, in the order they print:

1. `stand the estate up` - an apply builds a small VPC estate; every
   create call carries the two identity tags, estate and address.
2. `the crash shape` - a subnet is created the way a crashed apply
   leaves one: real resource, tags written, recorded nowhere. Stock
   tooling can never see this subnet again.
3. `the next plan finds it` - the forgotten subnet appears as a named
   plan line. Nobody re-imported it and no file remembered it; the tags
   did.
4. `a deleted block is the same story` - a resource removed from the
   configuration surfaces as a destroy the same way, through the same
   read.
5. `applying removes them - loudly, exactly` - the plan proposes
   exactly two destroys and the apply performs exactly two.
6. `where the machinery does not reach, it says so out loud` - two of
   the estate's types sit outside the sweep today, and the apply names
   them and the consequence up front. Degrading to a warning is
   allowed; silence is not.
7. `the same claim where values live in the record store` - a
   `terraform_data` resource has no cloud presence to tag, so its
   record lives in the record store; delete its block and it surfaces
   from the store's own list. No state file or cloud is involved.
8. `teardown` - the estate is destroyed to an exact count.

The `BREAK=1` run creates the subnet without identity tags. That is
the one shape the claim excludes, so the scenario must refuse to claim
it.

## On Kubernetes

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
24](k8s-custom-resource.md) removes one that
way. What fences a write on the label is the admission
policy of [claim 13 on Kubernetes](the-tag-is-the-boundary.md#on-kubernetes),
which excludes a controller's objects by the same owner-reference rule.
