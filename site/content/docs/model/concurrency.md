---
title: "Two runs at once"
weight: 5
---

# Two runs at once

No lock is held across a run. Ownership lives on the resources themselves, and
contention settles at the API being written to. Two simultaneous applies against one estate
resolve one of four ways.

| Race | Outcome |
|---|---|
| Two creates of the same client-named resource | The cloud's uniqueness constraint rejects the second. The loser re-plans, binds to the winner's resource, and comes back clean. |
| Two creates of the same server-assigned resource | Both are created. The next plan reports a marker collision naming both live IDs and refuses rather than guessing. A human deletes one. |
| Divergent in-place updates | Last writer wins at the API. The next plan reads the live system and converges. |
| An update racing a destroy | The loser gets not-found, re-plans, and converges. |

No race orphans a resource silently. Each case is a clean re-plan or a named
collision.

A lock does not help with a crash mid-apply: a resource created and not yet
recorded is orphaned either way. Under markers the tag rode the create call,
so the resource is found again with nothing to unlock.

## The record store is not locked either

The table above is about cloud resources. Records are the other thing two runs
can both write, and nothing is held there either. Every record write is one
conditional request: a create carries `If-None-Match: *`; an update or delete
carries `If-Match` with the version read. The store decides in one atomic
step and keeps nothing afterwards - a bucket's S3, a cluster's API server
comparing `resourceVersion`.

| Race | Outcome |
|---|---|
| Two runs create the same record | One `PutObject` wins. The other is told the record now exists, by name, with both versions in the message |
| Two runs update the same record | The first to arrive wins. The second's `If-Match` no longer matches, and it fails with a named write conflict and changes nothing |
| An update racing a delete | The loser is told the version it read is not the version the store holds, whether S3 said `412` or, for a key that is gone, `404` |

[Claim 32]({{< relref "/docs/claims/two-writers-one-record" >}}) holds two
writers at the wire so both arrive at one version; every round yields one
winner and one named conflict.
[Claim 4]({{< relref "/docs/claims/backend-sets-itself-up" >}}) kills an apply
with `SIGKILL` and the next run finishes the work.

A conditional write succeeds or fails in one step and keeps nothing, so a dead
run leaves nothing held. That is why `force-unlock` is refused: no lock
exists to open. The local store is the one place a lock file appears, for one
file write, and a stale one is broken by the next writer.

Serialize applies against one estate in CI anyway, where the real mutex has
always been - two estates need none
([Claim 43]({{< relref "/docs/claims/two-estates-at-once" >}})).
