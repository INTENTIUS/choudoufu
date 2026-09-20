---
title: "Claim 27: A label is a change"
claim: k8s-a-label-is-a-change
---

# Claim 27: A label is a change

On Kubernetes a great deal of meaning lives in `metadata.labels` and
`metadata.annotations`: ownership, cost centre, ingress class,
`cert-manager.io/*` behaviour, Prometheus scrape config. Editing one is an
ordinary configuration change and has to plan like one - one in-place
update, applied, and the object reading back what the configuration says.

For `kubernetes_manifest` that was not true here until
[#1177](https://github.com/INTENTIUS/choudoufu/issues/1177). The edit was
invisible to the plan and the apply wrote nothing: no refusal to look up,
no warning, just `No changes.` over a configuration that had changed.

This claim needs two substrates. Steps 1 to 8 run on a kind cluster alone.
Step 9 also needs the pinned floci emulator, because the record store it
measures the removal over is a bucket: Docker and the AWS CLI on top of kind
and kubectl. It does not skip when they are missing, it refuses.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm kind and kubectl are
installed, terraform (the stock oracle this scenario compares against),
Docker (`docker info`) and the AWS CLI (`aws --version`) - step 9 keeps the
estate's records in a bucket on the emulator. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke k8s-a-label-is-a-change

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke k8s-a-label-is-a-change and report both "caught" lines:
the first control runs the identical kubectl command against a key the
configuration never declared, and the plan must stay empty; the second
runs steps 8 and 9's two working directories on the local record store,
where the second directory has no record to read and must propose nothing.
```

The steps, in the order they print:

1. `apply` - a Namespace and a ConfigMap, both declared as
   `kubernetes_manifest`, the ConfigMap carrying the configuration's own
   `tier=one` beside the estate's `tofu-estate` label. No state file.
2. `stock is the oracle` - plain terraform, its own object, its own
   `terraform.tfstate`. The same one-label edit, and what stock proposes
   for it: `Plan: 0 to add, 1 to change, 0 to destroy.` That answer is
   measured on this cluster rather than quoted.
3. `the same edit under a live block` - the same one in-place update,
   naming `tier = "one" -> "two"`; the apply writes it, `kubectl` reads it
   back, and the next plan is empty.
4. `the annotation half` - #1177's own reproduction. An annotation added,
   then changed, each planning one in-place update.
5. `what the configuration never declared stays the server's` - the
   Namespace's `kubernetes.io/metadata.name`, written by the API server
   itself, plus a hand-written label and a hand-written annotation
   standing in for every controller that writes one. The plan is empty and
   all three are still on the objects.
6. `a label DELETED from the configuration is removed` - a declared label
   deleted, proposed for removal, applied, and the hand-written keys from
   step 5 left where they are. Then the half that matters more: two
   successive replans, both empty. The same for the last annotation,
   whose map goes with it.
7. `the one difference from stock` - an out-of-band `kubectl label
   --overwrite` of a key the configuration *declares*. Stock says
   `No changes.`; choudoufu proposes restoring it. Both answers are
   printed side by side. It runs after the rest because it leaves the
   object drifted on purpose.
8. `the same removal from a SECOND directory, over records in the cluster` -
   step 6 asked on behalf of anyone but the operator who applied it. Its
   own estate, namespace and objects on the same cluster, with the records
   as Secrets in `tofu-records-smoke-label-cluster`. A applies `tier` and
   `squad`; B has never applied anything, holds no cache and no file of
   A's, deletes `squad` from its configuration and plans the removal,
   applies it, and is quiet on two replans, while A - which still declares
   `squad` - proposes putting it back.
9. `the same removal again, over records in a bucket` - step 8 with one
   thing changed, the store. The records are objects in a bucket on the
   emulator. Sub-step 9b deletes the record object underneath B's
   conditional write and requires the write to be refused by name.

## Steps 8 and 9: the removal only works for one person until the store is shared

The record step 6 reads lives wherever `record_store` says. Left implied it
is a file beside the module, so a second checkout, a CI runner or a fresh
clone has no record at all, and
`internal/live/projection/residue.go` answers a missing or unreadable one by
proposing no removal, with at most a warning
(`SummaryResidueUnreadable`). That is the quiet degradation this page
describes below, and it is correct - but it means the removal is true for
one directory and silently absent everywhere else.

[#1394](https://github.com/INTENTIUS/choudoufu/issues/1394) found that no
Kubernetes claim had ever run against a shared store: claims 21 to 27 were
all on the implied local one, and the bucket backend's claims are all AWS.
So the path a Kubernetes estate writes a record by had not been measured at
all. The same two working directories now run it twice, once on each store
two directories can actually share, and each prints what it reads rather
than asserting it.

Step 8 is `record_store "kubernetes"`
([#1392](https://github.com/INTENTIUS/choudoufu/issues/1392), claim 39),
which is what a Kubernetes team can have without an AWS account. It is
first because it needs nothing the claim does not already have. The block
names no namespace, so the one used is what the estate name derives, and
the step creates it: the records namespace is the read boundary, so making
one is an operator's act and nothing in this fork does it. What the record
carries:

```text
tofu-record-af1e2258ffc03e91dcbe738006a330284ad7998983b052c6aa82f88d8e675658
{"app.kubernetes.io/managed-by":"choudoufu","choudoufu.intentius.io/record-namespace":"tofu-records","tofu-estate":"smoke-label-cluster"}
{"choudoufu.intentius.io/record-key":"tofu-records/smoke-label-cluster/kubernetes_manifest/a3ViZXJuZXRlc19tYW5pZmVzdC5jbQ","encoding":"gzip","tofu-address":"kubernetes_manifest.cm"}
```

The `tofu-estate` label is what `live/kubernetes/estate-boundary.yaml`
fences on, so the record is inside the estate's own fence; the address is an
annotation because a label value stops at 63 characters and an address does
not. That the write B landed was *conditional* is the API server's own
optimistic concurrency: the Secret's `resourceVersion` moves across B's
apply (842 to 895 on the run above), and a `kubectl replace` of the copy
taken before it - carrying the version B would have carried had it not
re-read - is refused with `the object has been modified`. Claim 39 step 1
runs the store's own stale-version case against the same cluster.

Step 9 is `record_store "s3"`, the store a Kubernetes estate had before
that, with everything else identical:

- the record object carries `tofu-estate = smoke-label-shared` and
  `tofu-address = kubernetes_manifest.cm`, read back with
  `aws s3api get-object-tagging`;
- A's write of that record was `if-none-match: *` and the write B's apply
  landed was `if-match: "<the version B read>"`, both read off the wire
  through `live/smoke/s3proxy.py`, because a write that succeeded looks the
  same whether or not it was conditional;
- and with the object deleted while B's conditional write is held at the
  proxy, the emulator answers that write `404` - the same answer real S3
  gives, which is what
  [#1344](https://github.com/INTENTIUS/choudoufu/issues/1344) found - and
  the run reports a named record store write conflict instead of creating
  the record afresh.

One `BREAK=1` control covers both, because what it takes away is the one
thing they have in common. The same two directories with `record_store
"local"`: A's record is then a file under A, B cannot see it, and B's plan
reads `No changes.` for the same deleted label.

## Why it was broken, and it was not the diff

`kubernetes_manifest` declares its whole object in one dynamic `manifest`
argument, and the provider's `computed_fields` argument (default
`metadata.annotations` and `metadata.labels`) tells it to take the *live*
object's value at those paths unless the configuration differs from the
*prior manifest*. A state-backed run's prior manifest is what was last
applied, so an edit differs from it.

choudoufu has no state file. It rebuilds the prior on every plan, and the
seed that fills it in took the *current* configuration - correct for every
provider that does not read the prior, which is nearly all of them. Here it
made the provider compare the configuration with itself. It could never
differ.

The finding was settled against stock rather than inferred: doctor a stock
state file to hold exactly what the seed produces - prior manifest carrying
the new label, `object` left at the old one - and *stock* prints
`No changes.` for the same edit. The fix gives the prior manifest the live
object's value for every key the configuration declares, which is what the
estate marker's own arm already did for `tofu-estate` alone.

## Why the mirror stops at the declared keys

`computed_fields` exists so that a label or an annotation the API server or
a controller writes does not churn the plan. Mirroring the live maps
wholesale would put every server-written key into the prior, the
configuration would then differ from it, and the provider takes the
configuration for the whole path when it does - so every plan would propose
deleting `kubernetes.io/metadata.name`,
`kubectl.kubernetes.io/last-applied-configuration`, `cert-manager.io/*` and
`meta.helm.sh/*`, forever, against a server that re-adds them. Step 5 is
where that is measured rather than assumed, and the `BREAK=1` control is
the same measurement made to fail.

## A key removed from the configuration

Step 6 is the last thing the state file was doing for this type, and it is
a different question from an edited key. "This configuration used to
declare `squad`" is not in the configuration - the key is gone from it -
and it is not on the object either, which holds the label and no memory of
who asked for it. Stock reads it out of its last-applied manifest. A
run with no state file has to record it, so
[#1211](https://github.com/INTENTIUS/choudoufu/issues/1211) writes the
label and annotation keys each apply declared into the estate's own
residue record - the same record that already carries this type's
`wait_for_*` arguments - and the removal set is
`(recorded) \ (currently declared)`.

The object's own `metadata.managedFields` was tried first as the source
and refuted on a real cluster. `computed_fields` makes the apply resend
every key the object already had, foreign ones included, so server-side
apply records this estate as the writer of keys nobody declared. One
apply later it owns `kubernetes.io/metadata.name`, and a removal rule
built on that set proposes deleting a label the API server writes back
every time. `managedFields` survives as a safety rail only: a recorded
key is not removed if another manager owns it now.

Degradation is toward the quiet answer, never toward churn. No record -
a fresh clone, an estate migrated before the member existed - proposes
removing nothing, which is the behaviour before this change. A stale
record still says what the estate last declared, which is the wanted
semantic, and a record naming a key the object no longer carries proposes
nothing either.

## The difference from stock

Step 7's difference is forced, not chosen. "The configuration was edited"
and "the live object drifted" are the same observation - configuration
differs from live - unless you have a last-applied value to tell them
apart. A state file has one, and a run without a state file does not. So making step 3
visible necessarily makes step 7 visible. It is the direction #1177 asks
for: an out-of-band `kubectl label` on a declared key is exactly the mover
a saved plan's staleness check has to be able to see, and stock's cannot.
