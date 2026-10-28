---
title: "Claim 7: Identity is a tag you can read, move and carve by"
claim: identity-is-a-tag
---

# Claim 7: Identity is a tag you can read, move and carve by

Ownership lives on each resource as a marker the platform holds: two tags
on AWS, one label with the address in an annotation on Kubernetes. Any
tool reads it, a rename is a marker rewrite, and carving a monolith into
estates is a marker write.

Each scenario runs from the repository root and ends on a `PASS` line; its
`BREAK=1` run breaks the thing the proof rests on and must print a
`caught` line. [The README](README.md) says what each needs installed.

## On AWS

### carve-by-retag

    just smoke carve-by-retag
    BREAK=1 just smoke carve-by-retag

Needs Go. A stock estate, two copies of `live/e2e/estate` rendered by
`tools/scenario-estate` (86 resources), is adopted with one command, then
carved into estates by rewriting `tofu-estate` on the resources that leave. Each side
plans clean and nothing is rebuilt; a parent whose marker names another
estate never anchors a child for this one, whatever a left-behind record
says. `BREAK=1` does the repository half of the carve and never rewrites a
tag, and both sides must then plan the two-ledger mess stock lives in.

### count-is-a-fungible-set

    just smoke count-is-a-fungible-set
    BREAK=1 just smoke count-is-a-fungible-set

A `count` pool whose members are interchangeable gets a `tofu-slot` marker
per member, minted once, so a pool of three scales to two by removing one
member and rebuilding nothing. A `count` block the configuration names
through `count.index` gets no slot and binds by `tofu-address`. `BREAK=1`
deletes the local files and strips one member's slot, and the plan must
refuse the half-slotted set by name; `BREAK_SLOT=1` stamps a slot where
none belongs, and the tag check must fail on it.

### terragucci row: tg-migrate

terragucci's harness scenario `tg-migrate` reads the tag in a pipeline.
The base push stamps infra's bucket with its estate's marker (cdf-apply);
app's stock state is adopted under an approval of its digest, and both of
app's queues then carry app's estate; a branch that adds a bucket ignoring
its tags goes red, live-check naming infra (cdf-check). Its `BREAK=1`
names a state key app never wrote, and app is never stamped.

A terragucci row counts toward the claim when it ran choudoufu 0.31.0,
the newest release in `live/history`, and `live/CLAIMS.md` gives each
row's state. The rows stand beside the scenarios above, whose `BREAK=1`
arms rebuild choudoufu from source, which a pipeline running a release
cannot do.

## On Kubernetes

### k8s-greenfield

    just smoke k8s-greenfield
    BREAK=1 just smoke k8s-greenfield

A Kubernetes estate applies with no AWS provider; kubectl reads
`tofu-estate` back; `live-ls` lists the estate by its label; the replan is
empty with the cache deleted; and an `api_version` change of a block's type
is no move. `BREAK=1` strips the label, and `live-ls` must drop the object
while the replan refuses it by name; `BREAK_ANNOTATION=1` strips the
address annotation from an object whose name is read at plan time, and the
replan must refuse that block by name.

### k8s-custom-resource

    just smoke k8s-custom-resource
    BREAK=1 just smoke k8s-custom-resource

A custom resource is bound by the apiVersion, kind, namespace and name
inside its `kubernetes_manifest`, created with `tofu-estate` in its labels
and swept by it. A block whose CRD the cluster does not serve is refused
by name; every planned create or update is sent with `dryRun=All` and the
server's verdict printed above the plan; and `live-import` adopts a
stock-made custom resource with one label merge patch, refusing one an
admission policy would also change. `BREAK=1` runs seven controls, from a
value only the server rejects to a migration a mutating policy would
widen.

### carve by relabel, in k8s-the-label-is-the-boundary

    just smoke k8s-the-label-is-the-boundary

A carve on Kubernetes is one label write through `live-mv -from-estate`
or `kubectl label`. With the admission policy installed the caller must
hold both estates; [claim 13's Kubernetes
scenario](the-tag-is-the-boundary.md#on-kubernetes) shows the refusal
without the grant and the carve with it.
