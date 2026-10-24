---
title: Resource-level locking
description: Two applies meet at one record, and one of them wins.
prompt: >-
  Read https://intentius.io/choudoufu/how-it-works/locking/ and say how our CI
  serializes applies to one estate. Change no files.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

A plain backend locks a whole state for the length of a run. choudoufu keeps
one record per resource and writes each one conditionally. This is resource-level
locking at the cloud level: the conditional write to the cloud's object store
is the lock, so nothing is ever left locked.

## The write

A create goes through only if nothing is there yet (`If-None-Match: *` on S3).
An update or a delete goes through only if the record is unchanged since the
run read it (`If-Match` with the version it read). Azure Blob Storage takes the
same two headers. GCS sends `ifGenerationMatch`, and a Kubernetes Secret is
written conditionally on its `resourceVersion`.

Two applies therefore never hold a lock. The store picks one winner per record.

## What the loser sees

The losing apply stops with the error "Record store write conflict". The message
gives the version this run expected and the version now stored. It says nothing
was overwritten. Every other record still gets its try,
so one pass shows every conflict in the estate.

Re-plan and re-apply. The new plan reads what the winner wrote and proposes
whatever is still left to do.

## What it does not do

A run that fails or loses part way does not undo the records it already wrote.
Every write stands alone, so nothing rolls back across records.

Two runs that change different resources both proceed. An apply writes only the
records it changed, so they never meet.

Since a record holds identity and what the cloud cannot return, two plain applies
that change an attribute the provider reads back are last writer wins at the
cloud API. `choudoufu apply <planfile>` re-reads the live system and exits 3 if
a value in its changes moved since approval. Keep one apply at a time per estate
in CI anyway.

A dead run leaves nothing held. `force-unlock` has no lock to release, so the
command rejects it.
