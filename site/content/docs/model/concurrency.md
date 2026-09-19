---
title: "Two runs at once"
weight: 5
---

# Two runs at once

No lock is held across a run. Ownership lives on the resources themselves, and
contention settles at the API that is being written to. Two simultaneous applies against one estate
resolve one of four ways.

| Race | Outcome |
|---|---|
| Two creates of the same client-named resource | The cloud's uniqueness constraint rejects the second. The loser re-plans, binds to the winner's resource, and comes back clean. |
| Two creates of the same server-assigned resource | Both are created. The next plan reports a marker collision naming both live IDs and refuses rather than guessing. A human deletes one. |
| Divergent in-place updates | Last writer wins at the API. The next plan reads the live system and converges. |
| An update racing a destroy | The loser gets not-found, re-plans, and converges. |

No race orphans a resource silently. Each case is a clean re-plan or a named
collision.

Compare a backend whose lock fails or was never configured, where the last
state write wins and the loser's resource drops silently out of every future
plan. A lock does not help with a crash mid-apply either: a resource created
but not yet recorded is orphaned either way. Under markers the tag rode
the create call itself, so the resource is discoverable and there is nothing to
unlock or recover.

## The record store is not locked either

The table above is about cloud resources. Records are the other thing two runs
can both write, and nothing is held there either. Every record write is one
conditional request: a create carries `If-None-Match: *`, an update or a
delete carries `If-Match` with the version the writer read. The store decides, in one
atomic step, and keeps nothing afterwards. On a bucket that is S3, and on a
cluster it is the API server comparing `resourceVersion`.

| Race | Outcome |
|---|---|
| Two runs create the same record | One `PutObject` wins. The other is told the record now exists, by name, with both versions in the message |
| Two runs update the same record | The first to arrive wins. The second's `If-Match` no longer matches, and it fails with a named write conflict and changes nothing |
| An update racing a delete | The loser is told the version it read is not the version the store holds, whether S3 said `412` or, for a key that is gone, `404` |

[Claim 32]({{< relref "/docs/claims/two-writers-one-record" >}}) holds two
writers at the wire so that both arrive with the same version in hand, and
requires exactly one winner and one named conflict on every round.

The local store is the one place a lock file appears. A plain directory has no
conditional write, so a write takes a `<file>.lock` sidecar for the length of
one file operation, and a sidecar older than thirty seconds is broken by the
next writer. It is never held across an operation, so a killed run cannot
strand an apply behind it.

A conditional write is not a lock. A lock is held across operations and can
be orphaned by a crash. A conditional write succeeds or fails atomically and
retains nothing, so there is nothing a dead run could leave held.
[Claim 4]({{< relref "/docs/claims/backend-sets-itself-up" >}}) kills an apply
with `SIGKILL` in mid-flight and the very next run finishes the work.

That is also why `force-unlock` is refused rather than made a no-op. There is
no lock for it to open, and a command that pretends to open one teaches an
operator that one exists.
[Claim 2]({{< relref "/docs/claims/no-self-managed-locks" >}}) measures the
refusal.

None of that argues for applying concurrently. Serialize applies in CI, where
the real mutex has always been.
