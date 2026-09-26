---
title: "Claim 13: The tag is the boundary"
claim: the-tag-is-the-boundary
---

# Claim 13: The tag is the boundary

## On AWS

In stock Terraform and OpenTofu, who owns a resource is a line in a state
file. No IAM policy can gate a change to that line, because the cloud
never sees it. Under choudoufu ownership is a tag on the resource, and a
tag write is an API call the cloud's own policy engine evaluates per
resource. A role can be fenced to half an estate by a condition on the
ownership tag, with the grant `live/MARKERS.md` publishes under "Granting
an estate".

That fence binds the credential: the same condition governs a plain AWS
CLI call with no choudoufu anywhere in the process. A carve, one half
moving into an estate of its own, is then a governed write the platform
can refuse. The scenario turns the emulator's IAM enforcement on for its
run. The harness's own key stays privileged, and only the two roles the
scenario creates and assumes are governed.

The boundary this claim proves is narrow, and it is worth stating exactly
that way. The grant fences three actions by name -
`ec2:CreateTags`, `ec2:DeleteTags` and `ec2:TerminateInstances` - on
resources carrying the ownership tag's value for the caller's half. It
says nothing about any other action, and nothing about a resource this
estate does not own. Read it as "this condition governs the actions it
names, on the resources that carry the tag it names," never as a claim
that IAM fences every write a tool-less actor could make.

[On Kubernetes](#on-kubernetes) the boundary is one admission policy on
the estate label, write-only and cluster-wide, with the grant an ordinary
ClusterRole.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke the-tag-is-the-boundary

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke the-tag-is-the-boundary and report both "caught"
lines: the first drops the conditions from Bob's grant, and Bob's write
on Alice's half through choudoufu must go through, which proves the
condition and not the credentials was the boundary; the second repeats
that with no choudoufu in the call at all, a plain AWS CLI write, and it
must go through too.
```

The steps as they print:

1. `the platform stands one estate up, two halves in it` - two instances
   in estate app, one under module.net and one under module.data, with
   markers stamped by the account.
2. `two roles, two halves, one grant shape` - Alice may act on
   module.data.* and create into data; Bob may act on module.net.* and
   create into net. The evidence line prints the conditions.
3. `Alice converges her half` - a tag change on the database, applied
   under Alice's session.
4. `Alice is denied on Bob's half - by AWS, not by this tool` - the same
   kind of change on the gateway. The provider's CreateTags comes back
   403 and the gateway is untouched.
5. `Bob converges the same change` - his session, his half.
6. `Bob, tool-less, is refused on Alice's half - by AWS, with no
   choudoufu in the call path` - under Bob's session, with nothing of
   this tool anywhere in the process, a plain `aws ec2 create-tags` and a
   plain `aws ec2 terminate-instances` against the database both come
   back refused. The same condition that governs choudoufu's own writes
   governs a script's.
7. `Bob's own half, tool-less, and the platform lets it through - the
   next plan sees it` - the identical plain CLI call against the
   gateway, Bob's own half, lands with no choudoufu involved, and the
   next `choudoufu plan` names the drift and proposes reconciling it -
   nothing the fence permits is invisible to the tool. Bob then
   reconciles it with an ordinary apply.
8. `the carve begins with a git move, and Bob's attempt at the retag is
   denied` - the data module moves to a new root, and Bob's
   `live-mv -from-estate=app` is refused by the platform before anything
   moves.
9. `Alice completes the carve: one governed tag write` - the same
   command under Alice's session, and tofu-estate becomes data.
10. `both estates plan clean, each under its own role` - No changes in
   data under Alice and in app under Bob.
11. `teardown - each estate by its own destroy`.

The `BREAK=1` run replaces Bob's grant with the same reach and no
conditions, then has Bob change a tag on Alice's half. The write must go
through. If the platform still refused, something other than the
condition was the boundary and the claim would prove nothing. It then
repeats the write with no choudoufu at all - a plain `aws ec2 create-tags`
under Bob's session - and that must go through too, or step 6's refusal
above would have measured a check this tool runs before calling the API
rather than the condition itself.

One emulator note. Real EC2 refuses with `UnauthorizedOperation`; the
emulator refuses with a 403 whose body the EC2 SDK cannot parse, so the
provider prints `api error UnknownError`. The scenario matches both, and
the gap is filed as lex00/floci#189.

On the real account, the same carve ran in us-east-2 on 2026-09-03 with
the roles assumed through STS. Every governed write was in the account's
own CloudTrail event history within a minute. The two refusals
carry the code real EC2 uses, and each record names the session that was
refused:

```text
04:39:31Z  alice  OK                            Name=database-v2           i-01e1006285c2b37b3
04:39:47Z  alice  Client.UnauthorizedOperation  Name=gateway-v2            i-0d3d2031d0b946a23
04:40:02Z  bob    OK                            Name=gateway-v2            i-0d3d2031d0b946a23
04:40:32Z  bob    Client.UnauthorizedOperation  tofu-estate=boundary-data  i-01e1006285c2b37b3
04:40:40Z  alice  OK                            tofu-estate=boundary-data  i-01e1006285c2b37b3
```

Each line is one `ec2:CreateTags` event, and
`live/smoke/evidence/the-tag-is-the-boundary.cloudtrail.json` holds the
five with their event IDs and the lookup that returned them. No state
file could have produced that record, because a state edit is not an API
call. The estate was torn down afterwards and the account listed back to
baseline.

## On Kubernetes

On AWS an IAM condition on the ownership tag fences reads and writes per
resource, and the AWS proof above runs it against a plain CLI call. On Kubernetes
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
10. `what the exemption costs: a labelled workload's copies are still
    made, and still cleaned up` - a Deployment whose pod template carries
    the label still fans out into a labelled ReplicaSet and a labelled
    Pod, written by the control plane's named controllers. So do a Job's
    Pod, a StatefulSet's PersistentVolumeClaim, which binds, and a
    Service's EndpointSlice. The garbage collector still removes the
    Deployment's copies, and a labelled namespace finishes terminating.
10b. `living in kube-system exempts nothing` - a ServiceAccount in
    `kube-system` that is not one of those controllers, and a user named
    `system:kube-proxy`, each create an unlabelled ConfigMap and are each
    refused one carrying `tofu-estate=app`, by name
    ([#1448](https://github.com/INTENTIUS/choudoufu/issues/1448)).
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

The `BREAK=1` run deletes the policy after step 4 and requires the
writes the main run refuses to succeed: Bob's apply on Alice's estate,
his plain `kubectl label` on her object, Alice's
`live-mv -from-estate=app` into an estate she was never granted, and
step 10b's two labelled creates. Step 4
is the one assertion that must not change when the policy goes. The same
arm runs it again with the policy deleted and requires the identical
refusal, because "never write a wrong marker" is a property of the plan.

Only the control plane is exempt, by name: nodes, the API server, the
scheduler and the controller manager's own controllers. That is what keeps
a controller's copies out of the fence, and step 10 measures it. If
anything else in kube-system is refused with "is not bound to it", grant
it the estate with `live/kubernetes/estate-grant.yaml`. Owned
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
owned objects ([claim 1 on Kubernetes](no-silent-orphans.md#on-kubernetes)), so the fence now
judges more than the sweep discovers. A cluster-admin's wildcard rule
matches the virtual resource too, so cluster-admin holds every estate the
way the account root does on AWS.

`live-mv` is the same command on both substrates
([#1081](https://github.com/INTENTIUS/choudoufu/issues/1081)).
`-from-estate` is the one label write, made through the provider under
the caller's credential so the policy judges it like any other client's.
Kyverno and Gatekeeper could express the same policy, and neither has
been verified for this.
