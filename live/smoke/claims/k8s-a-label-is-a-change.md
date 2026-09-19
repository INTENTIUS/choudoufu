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

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm kind and kubectl are
installed, and terraform (the stock oracle this scenario compares against).
If Go is not installed, export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke k8s-a-label-is-a-change

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke k8s-a-label-is-a-change and report the "caught" line:
the control runs the identical kubectl command against a key the
configuration never declared, and the plan must stay empty.
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
   printed side by side. It runs last because it leaves the object
   drifted on purpose.

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
