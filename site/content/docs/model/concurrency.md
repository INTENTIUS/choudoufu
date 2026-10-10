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
| Divergent in-place updates | Last writer wins at the API, unless a saved plan is refused ([below](#an-attribute-the-cloud-holds)). The next plan converges. |
| An update racing a destroy | The loser gets not-found, re-plans, and converges. |

No race orphans a resource silently. Each case is a clean re-plan or a named
collision.

A lock does not help with a crash mid-apply: a resource created and not yet
recorded is orphaned either way. Under markers the tag rode the create call,
so the resource is found again with nothing to unlock.

## The record store is not locked either

Records are the other thing two runs can both write. Every record write is one
conditional request, `If-None-Match: *` to create and `If-Match` with the
version read to update or delete, decided in one atomic step by S3 or a
cluster's API server, which keeps nothing afterwards.

| Race | Outcome |
|---|---|
| Two runs create the same record | One `PutObject` wins. The other is told by name, with both versions |
| Two runs update the same record | The first wins. The second fails with a named write conflict and changes nothing |
| An update racing a delete | The loser is told its version is not the store's (`412`, or `404` for a gone key) |
| Two runs change different resources | Both land: an apply writes only the records it changed |

[Claim 2]({{< relref "/docs/claims/no-self-managed-locks" >}}) races two
writers at the wire, one named conflict per round, and kills an apply with
`SIGKILL` for the next run to finish.

A dead run leaves nothing held, so `force-unlock` is refused: there is no
lock. The local store takes a lock file for one file write, and the next
writer breaks a stale one.

## An attribute the cloud holds

An ordinary resource's record holds its identity and what the cloud cannot
give back, and an unchanged record is not written. So two plain applies
changing an attribute the provider reads back are last-writer-wins at the
cloud API. `apply <planfile>` re-reads the live system and refuses, exit 3,
when one of its changes' before- or after-values moved since approval, unless
both runs re-read before either writes.
[Storage](https://github.com/INTENTIUS/choudoufu/blob/main/live/STORAGE.md#requests)
has the detail; claim 2 races two saved plans on one queue.

Serialize applies against one estate in CI anyway, where the real mutex has
always been - two estates need none
([Claim 13]({{< relref "/docs/claims/the-tag-is-the-boundary" >}})).
