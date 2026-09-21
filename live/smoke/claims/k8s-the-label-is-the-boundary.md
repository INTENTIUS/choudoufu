---
title: "Claim 23: The label is the boundary"
claim: k8s-the-label-is-the-boundary
---

# Claim 23: The label is the boundary

On AWS an IAM condition on the ownership tag fences reads and writes per
resource, and claim 13 proves it against a plain CLI call. On Kubernetes
the label is advisory until something fences on it, and RBAC has no
predicate on a label. The fence is one `ValidatingAdmissionPolicy`,
`live/kubernetes/estate-boundary.yaml`, installed once, cluster-wide, by
a cluster admin.

Its CEL reads `tofu-estate` off the object a write is about to change and
off the object it would produce, and asks the API server's own authorizer
whether the caller holds `use` on a virtual resource named after each
estate, `estates.choudoufu.intentius.io/<estate>`. Granting an estate is
an ordinary ClusterRole (`live/kubernetes/estate-grant.yaml`). The fence
binds the credential: a plain `kubectl label` under the same
ServiceAccount is judged by the identical policy.

This fence differs from the AWS one in three ways. Admission never sees
get or list, so the fence is write-only, and the scenario reads the
other estate's object as the refused principal to show that. A
`kubectl scale` arrives as a Scale object carrying no label, so RBAC on
`deployments/scale` fences subresources. The policy is one shared
cluster object, with a wider blast radius than two IAM changes. The
fence is also per estate: a team that wants two boundaries makes two
estates, and the carve in this scenario is how.

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
4. `a block declaring an object another estate owns - the PLAN refuses
   it, with nothing in the cluster consulted` - a block in `net/` names
   the ConfigMap `app` owns, by namespace and name, and the plan refuses
   it by name in the sentence a declared AWS resource carrying another
   estate's `tofu-estate` tag gets, proposes only the create the block
   declares, and leaves the live object alone
   ([#1108](https://github.com/INTENTIUS/choudoufu/issues/1108)).
5. `Alice converges her estate` - an update on her own object lands.
6. `Bob, through choudoufu, is refused on Alice's estate` - the same
   configuration under his ServiceAccount, and the apply comes back
   `Forbidden` naming the policy and the estate; then Alice applies the
   pending change.
7. `Bob, tool-less, is refused on Alice's object` - a plain `kubectl
   label`, a plain `kubectl delete` and a plain strip of the marker, all
   refused, and a plain `kubectl get` let through.
8. `an owned object keeps its estate: the owner field is no way out of
   one` - Alice's relabel of an `app` object into `net` is refused with no
   `ownerReference` on it, she then puts one on (allowed: the label does
   not change), and the same relabel is refused again; so is adding the
   owner and changing the label in one request, so is Bob's strip of the
   label off the owned object and his delete of it. Bob's plain update of
   it, leaving `tofu-estate` alone, goes through with no grant on `app`
   ([#1449](https://github.com/INTENTIUS/choudoufu/issues/1449)).
9. `the owner field is no way INTO an estate either: the create arm` -
   three creates by Alice for an estate she does not hold, all refused:
   a ConfigMap labelled `net` with no owner, the same one owned, and a
   Secret shaped like a record the Kubernetes record store writes, owned.
10. `what the exemption costs: a Deployment's ReplicaSet and Pods are
    still made` - a Deployment whose pod template carries the label still
    fans out into a labelled ReplicaSet and a labelled Pod, written by
    kube-system controllers, and the garbage collector still removes them.
11. `Bob's own estate, tool-less, and the API server lets it through` -
    the next plan sees the drift and reconciles it.
12. `a rename is a configuration edit: live-mv has nothing governed to
    write` - Bob renames the router block, runs the same `live-mv` an AWS
    runbook ends a rename with, and it reports `Nothing to write` and exits
    0; the next plan is empty.
13. `the carve begins with a git move, and the relabel is refused from
    both sides` - Alice runs `live-mv -from-estate=app` in `data/` and is
    refused by the policy on the estate the object would enter, as is her
    plain `kubectl label`; Bob is refused on the estate it is leaving.
14. `handover is an RBAC change: grant Alice data, and the same live-mv
    goes through` - the grant template for `data` is applied to Alice,
    the same `live-mv` lands, and `kubectl` reads `tofu-estate=data` back.
15. `every estate plans clean, each under its own principal` - `app` no
    longer declares the block and the object no longer carries its label,
    so its plan is honestly empty.
16. `teardown - each estate by its own destroy, under its own principal`.

The `BREAK=1` run deletes the policy after step 4 and requires the three
writes the main run refuses to succeed: Bob's apply on Alice's estate,
his plain `kubectl label` on her object, and Alice's
`live-mv -from-estate=app` into an estate she was never granted. Step 4
is the one assertion that must not change when the policy goes. The same
arm runs it again with the policy deleted and requires the identical
refusal, because "never write a wrong marker" is a property of the plan.

The exempt callers are the control plane: nodes, the kube-system
controllers, the scheduler and the API server itself. That is what keeps a
controller's copies out of the fence, and step 10 measures it. Owned
objects keep their estate: an object already carrying an `ownerReference`
may be updated with no grant while its `tofu-estate` label stays as it
was, which is what a third-party operator's writes on a labelled child
need, and an operator that creates labelled children of its own needs
`use` on that estate, one binding. Changing the label, stripping it,
deleting the object or creating a new labelled one needs the grant, owner
or no owner: `ownerReferences` is a field the caller writes, and until
[#1449](https://github.com/INTENTIUS/choudoufu/issues/1449) an identity
holding one estate could add an owner to its own object and then relabel
it into an estate it was never granted. The estate sweep still excludes
owned objects ([claim 22](k8s-no-silent-orphans.md)), so the fence now
judges more than the sweep discovers. A cluster-admin's wildcard rule
matches the virtual resource too, so cluster-admin holds every estate the
way the account root does on AWS.

`live-mv` is the same command on both substrates
([#1081](https://github.com/INTENTIUS/choudoufu/issues/1081)).
`-from-estate` is the one label write, made through the provider under
the caller's credential so the policy judges it like any other client's.
Kyverno and Gatekeeper could express the same policy, and neither has
been verified for this.
