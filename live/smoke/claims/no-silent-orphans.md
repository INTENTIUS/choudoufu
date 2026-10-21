---
title: "Claim 1: Owned resources never fall out of a plan"
claim: no-silent-orphans
---

# Claim 1: Owned resources never fall out of a plan

A plan reads ownership off the resources themselves, so anything carrying
this estate's marker walks into the next plan by name, and anything
without it is left alone.

Each scenario runs from the repository root and ends on a `PASS` line; its
`BREAK=1` run breaks the thing the proof rests on and must print a
`caught` line. [The README](README.md) says what each needs installed.

## On AWS

### no-silent-orphans

    just smoke no-silent-orphans
    BREAK=1 just smoke no-silent-orphans

A subnet is created the way a crashed apply leaves one, tagged and recorded
nowhere, and the next plan names it. A block deleted from source surfaces
as a destroy through the same read, and the apply removes exactly those
two. A `terraform_data` with no cloud presence surfaces from the record
store's own list when its block goes. `BREAK=1` creates the subnet without
the identity tags, and the plan must leave it alone.

### a-shadow-is-not-a-claimant

    just smoke a-shadow-is-not-a-claimant
    BREAK=1 just smoke a-shadow-is-not-a-claimant

A replaced EC2 instance keeps its tags while it terminates, so the next
plan finds two objects marked for one address. The apply that destroyed
the old one writes a tombstone into the address's record, and the plan
drops exactly the identities the record names as destroyed, warns about
each, and binds the survivor. A destroy the platform refused writes no
tombstone, and the deposed object is carried to its destroy. `BREAK=1`
puts a second running instance behind the same markers, and the plan must
refuse with `Two live resources claiming one address`; then it lists a
running, deposed instance as a tombstone by hand, and the read must refuse
that.

### a-held-delete-is-not-gone

    just smoke a-held-delete-is-not-gone
    BREAK=1 just smoke a-held-delete-is-not-gone

Secrets Manager's `DeleteSecret` with a recovery window answers success and
leaves the secret, tags included, in the account until the window ends.
The plan after the delete is empty, as stock's is: no second destroy, and
no refusal of the object the sweep still sees. `BREAK=1` sets
`recovery_window_in_days = 0` and requires the secret gone after the
destroy and the replan empty.

## On Kubernetes

The sweep is one cluster-wide, label-selected list per kind. Objects a
controller made are excluded before anything reaches a delete
(`kubesweep.ControllerMade`: an owner reference, content only the control
plane wrote, or a live Helm release's). An object no natural key declares is joined
to the block its address annotation names, and two objects claiming one
block are refused.

### k8s-no-silent-orphans

    just smoke k8s-no-silent-orphans
    BREAK=1 just smoke k8s-no-silent-orphans

A Deployment's pod template carries the estate label, so the controller
copies it onto a ReplicaSet and a Pod nobody declared. Deleting a
ConfigMap's block proposes exactly one destroy, the ConfigMap, and never a
copy; an orphan whose address annotation was stripped is still proposed,
because the label makes it the estate's. `BREAK=1` strips the label from
the orphan, and the replan must leave it alone.

### k8s-a-held-delete-is-not-gone

    just smoke k8s-a-held-delete-is-not-gone
    BREAK=1 just smoke k8s-a-held-delete-is-not-gone

A finalizer turns a delete into a request: the API returns success and the
object stays with a `deletionTimestamp` and its label. The plan proposes
the same one destroy on every run until the object is gone, then reads
empty, and each apply warns naming every object still held, with its
finalizers. `BREAK=1` removes the finalizer first and requires the object
gone in one apply and the replan empty.

### k8s-a-deleted-namespace-is-gone

    just smoke k8s-a-deleted-namespace-is-gone
    BREAK=1 just smoke k8s-a-deleted-namespace-is-gone

`kubectl delete namespace` takes every object in it in one call. Two
estates, one declaring its namespace and one living in a namespace made
with kubectl, each beside a stock twin with a state file: after the
delete nothing is listed as present or swept as an orphan, every plan is
the one stock makes from the same position, and one apply converges. A
namespace still terminating (step 6) is reported as stock's refresh reads
it. Step 7, the record store's own namespace deleted, is
[claim 29's](a-wrong-bucket-is-refused.md#on-kubernetes). `BREAK=1` asserts
the plan a blind sweep would make, `No changes.`, which must fail.
