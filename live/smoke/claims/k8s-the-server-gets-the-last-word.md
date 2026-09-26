---
title: "Claim 26: The server gets the last word"
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

Warning: Ownership marker was not stored

kubernetes_config_map.app was created, but the object the provider returned
after the write does not carry the ownership marker this run sent:
  - tofu-estate: sent "smoke-k8s", not stored
...
Apply complete! Resources: 1 added, 0 changed, 0 destroyed.
```

and `kubectl get configmap app-config -o jsonpath='{.metadata.labels}'`
returns nothing at all. `1 added` is true - the object really was added -
and the warning is the rest of the truth. Until
[#1192](https://github.com/INTENTIUS/choudoufu/issues/1192) closed there
was no warning: the run said `1 added` and nothing else, and the marker the
whole ownership model rests on was gone with no trace in the output.

## What the next run says

The plan does not pretend. It reads the cluster, finds an object at the
name the block declares carrying no marker, and refuses to treat it as the
estate's. It also refuses to propose the create, because the API server
would answer a create at a name an existing object holds with 409
([#1546](https://github.com/INTENTIUS/choudoufu/issues/1546)):

```text
Error: Unlabelled live object holds the declared name

A live kubernetes_config_map already exists at "smoke-k8s/app-config", the
namespace and name kubernetes_config_map.app declares, and carries no
tofu-estate label, so this estate does not own it. ...
```

Plan and apply both exit 1 there, and the apply never reaches the server.
`live-ls` agrees the estate owns nothing:

```text
Estate "smoke-k8s": 0 resource(s) carry its marker.
Nothing found.
```

So the estate is not silently wrong - it is stuck, which is far better. But
it is wrong about *whose* object this is. It was created seconds earlier by
this estate, and it reads back as somebody else's. Both remedies the
refusal names are writes, and the policy strips them too: a `kubectl label`
leaves no label behind, and `policy { declared_untagged = "adopt" }` prints

```text
Error: Ownership marker was not stored

kubernetes_config_map.app was updated, but the object the provider returned
after the write does not carry the ownership marker this run sent:
  - tofu-estate: sent "smoke-k8s", not stored

Nothing this run wrote to that object lasted ...
```

and exits non-zero, with no `Apply complete!` line. Step 7 runs it twice
and reads the object's `resourceVersion` after each, and requires the two
to be equal. The API server sets that field to the revision of the object's
last write, so a second run that leaves it alone wrote nothing the server
kept - which is the "on every run, for ever" part, measured rather than
asserted.

## Two lines that were not true

Until #1192 closed, the create printed `1 added` and said nothing about the
marker, and the adopting run printed `0 added, 1 changed, 0 destroyed` and
exited 0 - on every run, for ever, over a label that was never stored. A
nightly gate reading exit codes saw an estate converging cleanly and owning
nothing.

The fix needed no read-back, which is worth saying because that was the
obvious shape for it. The object the provider returns from
`ApplyResourceChange` **is** the stored object for any provider that reads
its resource back, and core was already computing the difference and
throwing it away: with `TF_LOG=trace` the run logs
`.metadata[0].labels: element "tofu-estate" has vanished`, at `WARN`,
because `objchange.AssertObjectCompatible`'s findings are deliberately
tolerated for a legacy-SDK provider - which `hashicorp/kubernetes` and
`hashicorp/aws` both are. Tolerating a shimmed type is right; tolerating a
discarded ownership marker is not, so the seam that stamped the marker is
now handed that same value and asked about its own attribute. No extra
request is issued.

The two severities are different on purpose. A create that loses its marker
warns: the object was really added, the count is true about it, and the next
plan is loud on its own. An adopting update that loses its marker is an
error: its entire content was the marker, nothing it wrote lasted, and it is
the one shape with no other alarm anywhere. Nothing is stranded by the
error - the object is exactly as it was before the run, and step 8 adopts it
in a single apply the moment the label scheme permits `tofu-estate`.

Unlike [claim 25](k8s-a-held-delete-is-not-gone.md),
where stock prints the same misleading line, there is no oracle here to be
compatible with: stock has no marker, so the marker write is choudoufu's
own and so was the gap.

The AWS half of #1192 stays open. An Organizations tag policy can rewrite a
tag on the way in, and the same hook would report it - but only where the
provider hands back the tags it stored rather than the ones it was sent,
and that is per resource type and unmeasured.

## The control

`BREAK=1` installs the same stripping policy against a decoy label instead
of `tofu-estate` - same kind, same namespace, same JSONPatch, one key
different - and requires the opposite outcome: the decoy stripped, so the
policy is provably in the admission chain and provably mutating; the marker
landed, with no `Ownership marker was not stored` anywhere in the run;
`live-ls` listing the object as this estate's; and the second apply not
wedging. Without it the third part would read identically if choudoufu
simply never wrote a label at all, and its two new assertions would read
identically if the diagnostic fired whatever the server did.

## What is not measured here

The first fault uses a real webhook; the other two use
`MutatingAdmissionPolicy`, the API server's own in-process admission chain,
because it produces the identical effect on the stored object with no
certificate, no image and no pod to go wrong - the same choice
[claim 23](k8s-the-label-is-the-boundary.md) and
claim 24 already make. What a webhook can do that a policy cannot is not be
there, and that is exactly what the first fault covers.

Nothing here measures a webhook that mutates a `kubernetes_manifest`
object. Those go through the plan-time dry run
([#1101](https://github.com/INTENTIUS/choudoufu/issues/1101)), which sends
the planned object to the server with `dryRun=All` before the plan is
printed, so a rejection is caught one phase earlier. The built-in typed
resources this scenario uses send no such dry run, which is why the
rejection in step 2 arrives at apply time.
