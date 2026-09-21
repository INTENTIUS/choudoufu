# Proof

Eight claims are proven on a real cluster: the marker itself, the sweep
that finds a deleted block's object by it, the admission policy that
fences a write by it (through which claim 13's Kubernetes cell is proven
too), a custom resource bound by the natural key inside its manifest,
carrying the label and swept by it, a delete the platform has accepted and
not finished, admission getting the last word over a plan, a label edit as
an ordinary change, and records kept in the cluster. The rest are stated
per claim in the claims data rather than left implicit. The table
below shows only the claims whose Kubernetes cell is not still open; hover a
cell for its note.

The table is generated from `live/smoke/claims.json` and rendered at
https://intentius.io/choudoufu/kubernetes/proof/.

## In CI

The eight Kubernetes claims run on a kind cluster in GitHub Actions on
every pull request that touches the Kubernetes surface, each with its
`BREAK=1` control, and the nightly gauntlet re-measures the kubernetes
lane's estates on the same cadence as the AWS rows
([#1080](https://github.com/INTENTIUS/choudoufu/issues/1080)). A
Kubernetes verdict on this site is no longer only a laptop's word.

## The harness

```
just smoke k8s-greenfield
```

The AWS claims run against a local emulator in Docker. This harness runs
against a kind cluster in Docker, which is a real API server, and the
scenario shape is the same: a verdict line per step, exit 0 only when
every claim held, and `BREAK=1` to manufacture the fault.

[Claim 21](../smoke/claims/k8s-greenfield.md) applies a
namespace and a ConfigMap under a `live` block with no AWS provider
anywhere, reads the `tofu-estate` label back with kubectl, replans empty,
loses its cache without consequence, and destroys exactly. Its `BREAK=1`
strips the label and requires the replan to refuse the object by name,
because an object carrying no marker is nobody's.

[Claim 22](../smoke/claims/k8s-no-silent-orphans.md) runs the
sweep on the same harness.
[Claim 23](../smoke/claims/k8s-the-label-is-the-boundary.md)
runs the gate. The plan refuses a block declaring another estate's object
before any cluster is consulted. With two ServiceAccounts and two estates,
the API server refuses a plain `kubectl label` across the boundary, and
the policy governs a carve by relabel. Its `BREAK=1` removes the policy and
requires the plan-side refusal to hold without it.

[Claim 24](../smoke/claims/k8s-custom-resource.md) runs a
custom resource on a CRD the scenario installs: refused by name while the
CRD is missing, bound by the key inside its manifest, labelled on create,
dry-run against the server before the apply, restored when the label is
stripped, and swept when its block is removed.

[Claim 25](../smoke/claims/k8s-a-held-delete-is-not-gone.md)
runs a held delete. A finalizer holds an object's delete, so the API
accepts it and the object stays, terminating, with its label. The run's
own summary says destroyed. The sweep on the next plan still reports the
object, and keeps reporting it until the object is really gone. Its
`BREAK=1` takes the finalizer off before the destroying apply and requires
the object to go in one apply.

[Claim 26](../smoke/claims/k8s-the-server-gets-the-last-word.md)
runs the gap between a plan and the write it approved. A real
`ValidatingWebhookConfiguration` with `failurePolicy: Fail` and nothing
behind it refuses the apply of a saved `-out` plan. The run reports the
API server's own `failed calling webhook` message, the object is
untouched, and the same plan file applies unchanged once the webhook is
gone. A mutating policy that strips `tofu-estate` on the way in creates
the object without its marker while the run reports it created, so the
next plan reads the estate's own object as somebody else's and the next
apply wedges on the name
([#1192](https://github.com/INTENTIUS/choudoufu/issues/1192)). Its
`BREAK=1` points the identical policy at a decoy label and requires the
marker landed and the second apply clean.

[Claim 27](../smoke/claims/k8s-a-label-is-a-change.md)
runs the ordinary day-2 edit of a label or annotation on a
`kubernetes_manifest`. The scenario measures stock's own answer for the
edit on the same cluster, requires choudoufu to match it and to write the
object, and requires a Namespace's server-written
`kubernetes.io/metadata.name` to churn nothing. Its `BREAK=1` runs the
identical `kubectl label --overwrite` against a key the configuration does
not declare and requires the plan to stay empty. One difference from stock
is printed: an out-of-band change to a key the configuration declares
plans here and does not plan on stock, because without a last-applied
value an edited configuration and a drifted object look the same.

Its steps 8 and 9 are the only place a Kubernetes claim runs against a
shared record store
([#1394](https://github.com/INTENTIUS/choudoufu/issues/1394)). A removed
label is removed from the estate's record, so on the implied local store
the removal works for the directory that applied it and is silently absent
everywhere else. Each step applies from one working directory and removes
the label from a second that holds nothing of the first's, and requires the
second to propose the removal, write it and settle: step 8 with the records
as Secrets in this cluster (`record_store "kubernetes"`, #1392), step 9
with them as objects in a bucket on the pinned emulator. Both read what the
record itself carries - the estate marker, the address, the record key -
and whether the write that landed was conditional, which no Kubernetes
claim had done before. One control covers both: the same pair of
directories on `record_store "local"`, where the second has no record and
must propose nothing.

## The gauntlet lane

The lane's bars are rendered from `live/gauntlet.json` at
https://intentius.io/choudoufu/kubernetes/proof/.

The `kubernetes` lane ([#1067](https://github.com/INTENTIUS/choudoufu/issues/1067))
runs the same fourteen stages as the AWS lanes against a kind cluster
created for the run, and counts toward its own bar. Two stages do not
apply on Kubernetes and read `n/a`: a replacement under
`create_before_destroy`, and the crash between its create and its destroy.
A Kubernetes name is unique within its namespace, so nothing can be
created before the object it replaces is gone.
[`live/GAUNTLET.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/GAUNTLET.md)
says how every other stage reads on the kind substrate.

Three estates run in the lane. `reference-k8s` is a hand-written shape kept
in this repository. `corpus-quickpizza` is Grafana Labs' own published
deployment root for their QuickPizza demo application at a pinned tag: 26
objects over eight kinds, real images, no cloud provider. It is crossed
with the same deltas every AWS estate gets for its emulator, and one more
for the Grafana Cloud token the run does not have.

`reference-k8s-stateful`
([#1175](https://github.com/INTENTIUS/choudoufu/issues/1175)) is the third,
also hand-written, because
[#1107](https://github.com/INTENTIUS/choudoufu/issues/1107)'s search found
no published stateful root that cleared the bar. It is 14 objects over
eight kinds around two StatefulSets with `volume_claim_template` blocks,
and it is the lane's first red row.

The PersistentVolumeClaims those templates produce are declared by nothing
and held in no state file. On kind they carry neither `ownerReferences` nor
`managedFields`, the two signals the estate sweep tests to tell a
controller's copies from what somebody declared. Removing only the
StatefulSet's block leaves them `Bound`, labelled and unowned, and a label
on one of them is enough for the sweep to propose destroying it. The
estate's own teardown passes, because the Namespace is in the root and its
deletion cascades.

## What it would cost

What follows is the shape, from the API's own properties, and it is what
every Kubernetes claim and the lane's estates run through; the call
counts have not been tabulated the way the AWS scale page tabulates
them.

### The sweep

On AWS the estate sweep is a single `GetResources` call, filtered
server-side on the marker, covering the whole admission table at once.
Kubernetes has no cross-kind label-filtered list. A sweep there is API
discovery (`/api` and `/apis`, which say every kind the cluster serves)
and then one cluster-wide, label-selected list per kind the cluster
serves with list and delete verbs, custom kinds included - not one per
kind per namespace, as the design first estimated: a namespaced kind
lists across every namespace in one call.

Two things survive. A label-selected list returns only the estate's objects
and does not grow with the cluster, so "a plan costs its estate, not its
account" holds in weakened form. And because the universe of kinds is asked
rather than tabulated, an admitted type the generated table did not know
about cannot be owned, orphaned and unreachable. `live-ls DIR` is the same
listing printed as an inventory, each object joined to the block that
declares it ([claim 21](../smoke/claims/k8s-greenfield.md)).

What does not survive is "one call", and the claims page marks claim 14
restated rather than pretending otherwise.

### The read pass

Reading each declared object is one `GET` per object, as it is for stock.
Server-side dry run validates, defaults and runs admission without
persisting, which no AWS plan can do; the plan sends every planned
`kubernetes_manifest` create or update that way and prints the server's
answer above the plan, one more request per such object
([Adopt](https://intentius.io/choudoufu/kubernetes/adopt/), "What Kubernetes does
better").
