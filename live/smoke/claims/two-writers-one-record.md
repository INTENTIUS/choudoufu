---
title: "Claim 32: Two writers, one record: the loser is named, nothing is clobbered, and nothing is held"
claim: two-writers-one-record
---

# Claim 32: Two writers, one record: the loser is named, nothing is clobbered, and nothing is held

Nothing here takes a lock. Two applies that touch the same record both
go ahead, and the record store settles it. Every record write is one
conditional `PutObject` carrying the version it read (`If-Match`), so
exactly one of two racing writers lands, and the other is told which
version it expected and which it found.

[Claim 2](no-self-managed-locks.md) measures
contention at the platform's own API: a client-named create that
collides, a named collision for a server-assigned resource, the
`force-unlock` refusal. It says nothing about two writers racing on one
record object, which the bucket backend leans on entirely. This is that
measurement. It is its own claim and not a step of Claim 2 because
Claim 2's name is about the platform API, and a name that also covered
the record store would be a name that tried to say two things.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info), the AWS CLI and python3 are installed, and Go is
installed. From the repo root run:

  just smoke two-writers-one-record

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke two-writers-one-record and report the "caught" line:
it rebuilds choudoufu so the record write carries no precondition, and
both racing applies must be caught reporting success over one record.
```

As the run prints them:

1. `one estate, one record, two checkouts of it` - writers `a` and `b`
   share an estate name, a bucket and a resource address, so they
   contend for one object.
2. `the proxy that makes the race a race` - two applies started together
   usually do not overlap. One finishes writing before the other has
   read, which is a sequence. A proxy in front of S3 holds each writer's
   `PutObject` until both have arrived, so both were planned against the
   same version, then lets them through one at a time in a chosen order.
3. `the race` - several rounds, alternating the order the writes arrived
   in and its reverse, so the loser is not always the slower writer.
   Every round exactly one apply lands and the record holds the value of
   the write that was judged first. The other fails with `Record store
   write conflict`, naming the version it expected and the version the
   store now holds, and saying nothing was overwritten. The scenario
   reads both versions out of the message and requires them to differ.
   `Acquiring state lock` appears in neither writer's output.
4. `the loser's recovery is an ordinary re-plan` - no unlock and no
   repair verb. The writer that lost plans again, sees the winner's
   record, and applies over it.
5. `a writer killed mid-write strands nothing` - writer `a` is killed
   with `SIGKILL` while its write is in the proxy's hands, and the write
   is then discarded. Nothing lock-shaped is left in the bucket, and
   writer `b` applies straight after. A conditional write holds nothing
   between operations, so a crash has nothing to leave held. With a lock
   table this is the moment `force-unlock` comes out.
6. `teardown` - one resource, one destroyed, checked by count.

The `BREAK=1` binary drops the `If-Match`. Both applies report success
in the first round, one envelope silently replaces the other, and
neither run says anything. That is last-write-wins, and it is what the
conditional write exists to prevent.
