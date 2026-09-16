---
title: "Claim 26: The server gets the last word"
weight: 26
claim: k8s-the-server-gets-the-last-word
---

# Claim 26: The server gets the last word

Every plan is a statement about what the API server will accept, made
before the server is asked. Admission is the gap between the two. A
validating webhook can refuse the write a reviewer already approved; a
mutating one can store something other than what was sent. Kubernetes
clusters run several of these as a matter of course - Kyverno, Gatekeeper,
a service mesh injector, a platform team's own label scheme.

This is the second fault of
[#1110](https://github.com/INTENTIUS/choudoufu/issues/1110), and it has
three parts. Two of them are ordinary. The third is where the ownership
model meets its edge: a policy that rewrites labels it does not recognise
will strip `tofu-estate`, and `tofu-estate` is the only thing that says an
object belongs to an estate.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and kind, kubectl and terraform are installed. If Go is not
installed, export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke k8s-the-server-gets-the-last-word

Explain each step's verdict line to me as it prints, and tell me which of
the three faults each step belongs to. Then run
BREAK=1 just smoke k8s-the-server-gets-the-last-word and report the
"caught" line: the control points the same stripping policy at a decoy
label instead of the marker, and everything must then work.
```

## The three faults

**A fail-closed webhook refuses the write the plan approved.** Step 1 saves
a plan with `-out` - the artifact a reviewer signs off - and then a cluster
admin installs a real `ValidatingWebhookConfiguration` with
`failurePolicy: Fail` whose endpoint is a Service nobody created. That is
the webhook fault operators actually get paged for: the policy engine is
mid-rollout, or its pods are gone, and every write fails closed. Step 2
applies the approved plan and it is refused, in the API server's own words:

```text
Error: Failed to update Config Map: Internal error occurred: failed calling
webhook "gate.smoke.choudoufu.io": failed to call webhook: Post
"https://policy-gate.smoke-k8s.svc:443/validate?timeout=5s": service
"policy-gate" not found
```

Exit 1, the object untouched, its marker intact, and the plan artifact
still on disk. Step 3 removes the webhook and applies **the same file**: it
lands, unchanged, with nothing re-planned and nothing re-approved. A
rejection after approval costs a re-run and nothing else.

**A mutating webhook rewrites a declared field.** Step 4 installs a policy
that forces `owner=platform-team` onto every ConfigMap in the namespace
while the configuration declares `owner=payments-team`. The apply succeeds
- nothing refused anything - and every plan afterwards proposes the same
one change, naming both values, because the field it wrote came back
different. Step 5 runs the same shape through plain stock with its own
state file and gets the same answer, which is the point: this is ordinary
drift, choudoufu reads it exactly as stock does, and the estate keeps its
marker throughout. An `ApplyConfiguration` mutation merges the labels map,
so `tofu-estate` is not involved.

**A mutating webhook strips the marker on the way in.** Step 6 is the
boundary case. The policy removes `tofu-estate` and leaves every other
label alone - exactly what a label-scheme enforcer does to a key it does
not recognise. choudoufu sends the marker on the create; the server stores
the object without it; the run reports:

```text
kubernetes_config_map.app: Creation complete after 0s [id=smoke-k8s/app-config]
Apply complete! Resources: 1 added, 0 changed, 0 destroyed.
```

and `kubectl get configmap app-config -o jsonpath='{.metadata.labels}'`
returns nothing at all.

## What the next run says

The plan does not pretend. It reads the cluster, finds an object at the
name the block declares carrying no marker, and refuses to treat it as the
estate's:

```text
Warning: Live resource outside this estate
A live kubernetes_config_map already exists with identity
"smoke-k8s/app-config" and carries no tofu-estate label, so this estate
does not own it ...
Plan: 1 to add, 0 to change, 0 to destroy.
```

`live-ls` agrees, and the next apply wedges on the name the API server will
not hand out twice:

```text
Estate "smoke-k8s": 0 resource(s) carry its marker.
Nothing found.

Error: configmaps "app-config" already exists
```

So the estate is not silently wrong - it is stuck, which is far better. But
it is wrong about *whose* object this is. It was created seconds earlier by
this estate, and it reads back as somebody else's. Both remedies the
warning names are writes, and the policy strips them too: a `kubectl label`
leaves no label behind, and `policy { declared_untagged = "adopt" }` prints

```text
kubernetes_config_map.app: Modifications complete after 0s [id=smoke-k8s/app-config]
Apply complete! Resources: 0 added, 1 changed, 0 destroyed.
```

with exit 0, on every run, over a label that was never stored.

## The line that is not true, and why it is asserted anyway

`1 added` over an object created without its marker, and `1 changed` over
an adoption that did not happen, are both false. Nothing in the write path
asks whether the marker it sent is the marker the server stored.
[#1192](https://github.com/INTENTIUS/choudoufu/issues/1192) is that gap,
and choudoufu already has the mechanism: `live-import` dry-runs its label
patch and diffs the result against the live object, which is how
[claim 24]({{< relref "/docs/claims/k8s-custom-resource" >}})'s control
catches a policy rewriting a custom resource's spec. The apply path does
not do the equivalent.

This scenario asserts both lines verbatim, because they are what a user
sees today. Closing #1192 changes steps 6 and 7 on purpose.

Unlike [claim 25]({{< relref "/docs/claims/k8s-a-held-delete-is-not-gone" >}}),
where stock prints the same misleading line, there is no oracle here to be
compatible with: stock has no marker, so the marker write is choudoufu's
own and so is the gap.

## The control

`BREAK=1` installs the same stripping policy against a decoy label instead
of `tofu-estate` - same kind, same namespace, same JSONPatch, one key
different - and requires the opposite outcome: the decoy stripped, so the
policy is provably in the admission chain and provably mutating; the marker
landed; `live-ls` listing the object as this estate's; and the second apply
not wedging. Without it the third part would read identically if choudoufu
simply never wrote a label at all.

## What is not measured here

The first fault uses a real webhook; the other two use
`MutatingAdmissionPolicy`, the API server's own in-process admission chain,
because it produces the identical effect on the stored object with no
certificate, no image and no pod to go wrong - the same choice
[claim 23]({{< relref "/docs/claims/k8s-the-label-is-the-boundary" >}}) and
claim 24 already make. What a webhook can do that a policy cannot is not be
there, and that is exactly what the first fault covers.

Nothing here measures a webhook that mutates a `kubernetes_manifest`
object. Those go through the plan-time dry run
([#1101](https://github.com/INTENTIUS/choudoufu/issues/1101)), which sends
the planned object to the server with `dryRun=All` before the plan is
printed, so a rejection is caught one phase earlier. The built-in typed
resources this scenario uses send no such dry run, which is why the
rejection in step 2 arrives at apply time.
