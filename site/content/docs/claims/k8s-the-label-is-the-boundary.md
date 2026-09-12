---
title: "Claim 23: The label is the boundary"
weight: 23
claim: k8s-the-label-is-the-boundary
---

# Claim 23: The label is the boundary

On AWS the marker is an authorization primitive out of the box: an IAM
condition on the ownership tag fences reads and writes per resource, and
claim 13 proves it against a plain CLI call. On Kubernetes the label is
advisory until something fences on it, and RBAC cannot: a `PolicyRule`
has verbs, groups, resources and names, and no predicate on a label. The
fence is one `ValidatingAdmissionPolicy`, `live/kubernetes/estate-boundary.yaml`,
installed once, cluster-wide, by a cluster admin. Its CEL reads
`tofu-estate` off the object a write is about to change and off the object
it would produce, and asks the API server's own authorizer whether the
caller holds `use` on a virtual resource named after each estate,
`estates.choudoufu.intentius.io/<estate>`. That verb exists nowhere but in
RBAC, so granting an estate is an ordinary ClusterRole
(`live/kubernetes/estate-grant.yaml`) and handover is a binding moving,
not an edit to the policy. The fence binds the credential, not the
binary: a plain `kubectl label` under the same ServiceAccount is refused
or let through by the identical policy, with no choudoufu anywhere in the
call.

Three things are true of this fence that are not true of the AWS one, and
they belong in the headline. Admission sees create, update and delete and
never get or list, so the fence is write-only where an IAM condition can
fence a describe; the scenario reads the other estate's object as the
refused principal to show exactly that. It fences the object and not its
subresources: a `kubectl scale` arrives as a Scale object carrying no
label, and RBAC on `deployments/scale` is the fence for those. And the
policy is one shared cluster object, with a wider blast radius than two
IAM changes. The fence is also per estate, never per address: the label
carries no address by ruling, so a team that wants two boundaries makes
two estates, and the carve in this scenario is how.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and kind and kubectl are installed. If Go is not
installed, export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke k8s-the-label-is-the-boundary

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke k8s-the-label-is-the-boundary and report the two
"caught" lines: the scenario removes the policy, and the writes it
refused must go through.
```

The steps, in the order they print:

1. `the fence, installed once by a cluster admin` - one policy and one
   binding, with no type-check warning; nothing about an estate is in
   them.
2. `two ServiceAccounts, two estates, one grant shape` - Alice holds
   `app`, Bob holds `net`, and a SubjectAccessReview asks the authorizer
   the exact question the policy will ask.
3. `each principal stands its own estate up` - three objects under Alice,
   two under Bob, every create carrying the label.
4. `Alice converges her estate` - an update on her own object lands.
5. `Bob, through choudoufu, is refused on Alice's estate` - the same
   configuration under his ServiceAccount, and the apply comes back
   `Forbidden` naming the policy and the estate; then Alice applies the
   pending change.
6. `Bob, tool-less, is refused on Alice's object` - a plain `kubectl
   label`, a plain `kubectl delete` and a plain strip of the marker, all
   refused, and a plain `kubectl get` let through.
7. `Bob's own estate, tool-less, and the API server lets it through` -
   the next plan sees the drift and reconciles it.
8. `the carve begins with a git move, and the relabel is refused from
   both sides` - Alice is refused on the estate the object would enter,
   Bob on the estate it is leaving.
9. `handover is an RBAC change` - the grant template for `data` is
   applied to Alice and the same relabel goes through.
10. `every estate plans clean, each under its own principal`.
11. `teardown - each estate by its own destroy, under its own principal`.

The `BREAK=1` run deletes the policy after step 3 and requires the two
writes step 5 and step 6 refuse, Bob's apply on Alice's estate and his
plain `kubectl label` on her object, to succeed. If the API server still
said no, something other than the policy was the fence and the claim
would prove nothing.

What is exempt, and why: the control plane (nodes, the kube-system
controllers, the scheduler and the API server itself) and any object
carrying an `ownerReference`. A controller copies template labels onto
ReplicaSets and Pods nobody declared, and the estate sweep excludes those
by the same rule ([claim 22]({{< relref "/docs/claims/k8s-no-silent-orphans" >}})),
so the fence and the sweep agree on what an estate contains. A
cluster-admin's wildcard rule matches the virtual resource too, so
cluster-admin holds every estate the way the account root does on AWS.
There is no `live-mv` leg for Kubernetes: with no address on the object,
the carve is the label write itself, and any client can make it. Kyverno
and Gatekeeper could express the same policy; neither has been verified
for this.
