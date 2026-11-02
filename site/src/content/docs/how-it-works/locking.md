---
title: Resource-level concurrency
description: Two applies to one estate can run together. choudoufu writes one record per resource, each with a conditional write.
prompt: >-
  Read https://intentius.io/choudoufu/how-it-works/locking/ and say how our CI
  serializes applies to one estate. Change no files.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

OpenTofu and Terraform protect each state file with a lock, which keeps every
apply to it safe. choudoufu keeps one record per resource in a
[record store](../values/), so two applies that touch different resources can
run at the same time. A run's write to a record fails if another run wrote that
record first, which keeps the records consistent without a lock.

## Two applies, one estate

Apply A changes `db` while apply B changes `dns`, in the same estate.

| | With a state lock | With the live backend |
|---|---|---|
| Apply A changes `db` | Takes the lock | Lands |
| Apply B changes `dns` | Runs after A releases the lock | Lands |
| A saved plan for `db`, made before A applied | Refused as stale | Refused before anything changes, naming `db` |
| An apply killed part way | `force-unlock` releases the lock | Leaves nothing to release, and `force-unlock` is rejected |

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

Roots you keep for ownership can also plan and apply together:
[One plan, one apply, across roots](../across-estates/).
