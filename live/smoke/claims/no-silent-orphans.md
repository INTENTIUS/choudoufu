---
title: "Claim 1: Owned resources never fall out of a plan"
claim: no-silent-orphans
---

# Claim 1: Owned resources never fall out of a plan

Stock forgets a resource the moment its state file does: an apply that
crashes before the state write, a block deleted from source, a delete the
platform accepted and has not finished. The resource is still there and
still billing, and no plan mentions it again. Here a plan reads ownership
off the resources themselves, so anything carrying this estate's marker
walks into the next plan by name, and anything that does not is left
alone.

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
store's own list when its block goes. Where the sweep does not reach a
type, the apply says so up front: degrading to a warning is allowed,
silence is not. `BREAK=1` creates the subnet without the identity tags,
the one shape the claim excludes, and the plan must not claim it.

### a-shadow-is-not-a-claimant (claim 18 until #1817)

    just smoke a-shadow-is-not-a-claimant
    BREAK=1 just smoke a-shadow-is-not-a-claimant

A replaced EC2 instance keeps answering `describe-instances` and keeps its
tags while it terminates, so the next plan finds two objects marked for one
address. The apply that destroyed the old one writes a tombstone into the
address's record, and the plan drops exactly the identities the record
names as destroyed, warns about each, and binds the survivor. A destroy the
platform refused (a role without `ec2:TerminateInstances`) writes no
tombstone, and the deposed object is carried to its destroy instead.
`BREAK=1` puts a second running instance behind the same markers, and the
plan must refuse with `Two live resources claiming one address`; then it
lists a running, deposed instance as a tombstone by hand, and the read must
refuse that.

### a-held-delete-is-not-gone (claim 25 until #1817)

    just smoke a-held-delete-is-not-gone
    BREAK=1 just smoke a-held-delete-is-not-gone

Secrets Manager's `DeleteSecret` with a recovery window answers success and
leaves the secret, tags included, in the account until the window ends.
hashicorp/aws reads it as gone, so the plan after the delete is empty, as
stock's is: no second destroy, which AWS would refuse for the whole
window, and no refusal of the object the sweep can still see. This covers
the half of a held delete AWS can produce; an object the provider still
reads as present after its own delete returned needs an eventually
consistent delete, which the pinned emulator does not model
([lex00/floci#216](https://github.com/lex00/floci/issues/216)).
`BREAK=1` sets `recovery_window_in_days = 0` and requires the opposite: the
secret gone after the destroy and the replan empty.

## On Kubernetes

The sweep is one cluster-wide, label-selected list per kind. Objects a
controller made are excluded before anything reaches a delete
(`kubesweep.ControllerMade`: an owner reference, content only the control
plane wrote, or a live Helm release's). An object no natural key declares
is joined to the block its address annotation names (#1640), and two
objects claiming one block are refused rather than both destroyed (#1641).

### k8s-no-silent-orphans

    just smoke k8s-no-silent-orphans
    BREAK=1 just smoke k8s-no-silent-orphans

A Deployment's pod template carries the estate label, so the controller
copies it onto a ReplicaSet and a Pod nobody declared. Deleting a
ConfigMap's block proposes exactly one destroy, the ConfigMap, and never a
copy; an orphan whose address annotation was stripped is still proposed,
because the label is what makes it the estate's. `BREAK=1` strips the
label from the orphan, and the replan must leave it alone.

### k8s-a-held-delete-is-not-gone (claim 25 until #1817)

    just smoke k8s-a-held-delete-is-not-gone
    BREAK=1 just smoke k8s-a-held-delete-is-not-gone

A finalizer turns a delete into a request: the API returns success and the
object stays with a `deletionTimestamp` and its label. Stock drops it from
state and never mentions it again. Here the plan proposes the same one
destroy on every run until the object is really gone, then reads empty.
The apply's `1 destroyed` is the provider's word for "accepted", which
stock prints too; since #1184 the run adds a warning naming each object
still held, with its finalizers. `BREAK=1` removes the finalizer first and
requires the object gone in one apply and the replan empty.

### k8s-a-deleted-namespace-is-gone (claim 46 until #1817)

    just smoke k8s-a-deleted-namespace-is-gone
    BREAK=1 just smoke k8s-a-deleted-namespace-is-gone

`kubectl delete namespace` takes every object in it in one call no plan
made. Two estates, one declaring its namespace and one living in a
namespace made with kubectl, each beside a stock twin with a state file:
after the delete nothing is listed as present or swept as an orphan, every
plan is the one stock makes from the same position, and one apply
converges. A namespace still terminating (step 6) is reported as stock's
refresh reads it. Step 7, the record store's own namespace deleted, is
[claim 29's](a-wrong-bucket-is-refused.md#on-kubernetes). `BREAK=1` asserts
the plan a blind sweep would make, `No changes.`, which must fail.

What fences a write on the label is the admission policy of
[claim 13 on Kubernetes](the-tag-is-the-boundary.md#on-kubernetes).
