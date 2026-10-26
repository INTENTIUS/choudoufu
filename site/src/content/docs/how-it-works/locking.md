---
title: Resource-level concurrency
description: Terraform locks the whole state file. choudoufu moves the apply boundary to each resource, with no lock to take or release.
prompt: >-
  Read https://intentius.io/choudoufu/how-it-works/locking/ and say how our CI
  serializes applies to one estate. Change no files.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

Terraform and OpenTofu lock the state file because the apply boundary is the
state file, so everything in it is locked together. Teams split state into
roots to shrink what one lock covers. choudoufu moves the boundary to each
resource. It keeps one record per resource in a [record store](../values/). A
run's write to a record fails if another run wrote that record
first. That prevents state corruption without a lock.

## Two applies, one estate

Apply A changes `db` while apply B changes `dns`, in the same estate.

| | Stock backend | choudoufu live backend |
|---|---|---|
| Apply A changes `db` | Takes the lock on the whole state | Lands |
| Apply B changes `dns` | Waits for A's lock or fails, though it touches nothing A does | Lands |
| A saved plan for `db`, made before A applied | Refused as stale, as it would be after any change to the state | Refused before anything changes, naming `db` |
| An apply killed part way | Holds the lock until someone runs `force-unlock` | Leaves nothing to release, and `force-unlock` is rejected |

## What refused means

No run silently overwrites another's write. Two kinds of refusal hold that.

`choudoufu apply <planfile>` plans the live system again first. If the result
is not the approved plan, it stops with "The approved plan no longer matches
the live system", exit status 3, before anything changes.

A run that wrote a record after this one read it wins that record. The store
refuses this run's write with "Record store write conflict". The message names
the resource and gives the version this run expected beside the version now
stored. It says nothing was overwritten. Every other record still gets its try,
so one pass shows every conflict in the estate. Re-plan and re-apply, and the
new plan reads what the winner wrote.

## The write

A create goes through only if nothing is there yet (`If-None-Match: *` on S3).
An update or a delete goes through only if the record is unchanged since the
run read it (`If-Match` with the version it read). Azure Blob Storage takes the
same two headers. GCS sends `ifGenerationMatch`, and a Kubernetes Secret is
written conditionally on its `resourceVersion`.

## What it does not do

A run that fails or loses part way does not undo the records it already wrote.
Every write stands alone, so nothing rolls back across records.

A record holds only what a read of the resource leaves out, such as a generated
password. An attribute the provider reads back has no record, so two plain
applies that change it without a saved plan are last writer wins at the cloud
API. Keep one apply at a time per estate in CI anyway.

## Across states

With the boundary at each resource, the lock stops being a reason to split
state. The roots you keep for ownership can plan and apply together:
[One plan, one apply, across roots](../across-estates/).
