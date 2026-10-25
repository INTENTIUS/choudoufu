---
title: Resource-level concurrency
description: Stock backends lock a whole state file. The live backend has no lock and refuses a conflicting write to one resource.
prompt: >-
  Read https://intentius.io/choudoufu/how-it-works/locking/ and say how our CI
  serializes applies to one estate. Change no files.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

| | Stock backends | choudoufu live backend |
|---|---|---|
| What is stored | A state file per root | A record per resource, in a store you own |
| What two applies contend for | The lock on the whole state | One record, only when both change it |
| What serializes them | A lock, taken for the length of a run | A conditional write per record, with no lock |
| What a dead run can leave | A held lock, cleared with `force-unlock` | Nothing, so `force-unlock` is rejected |

On a stock backend the apply boundary is the state file, so everything in it is
locked together. Two applies that change different resources still contend for
that one lock, and the second waits or fails. The choudoufu live backend keeps one record per resource and writes
each one only if it is unchanged since the run read it. An apply writes only
the records it changed, so two applies to different resources both land, and the
store refuses a conflicting write to one resource's record.

## The write

A create goes through only if nothing is there yet (`If-None-Match: *` on S3).
An update or a delete goes through only if the record is unchanged since the
run read it (`If-Match` with the version it read). Azure Blob Storage takes the
same two headers. GCS sends `ifGenerationMatch`, and a Kubernetes Secret is
written conditionally on its `resourceVersion`.

Neither apply holds a lock. The store picks one winner per record.

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

Since a record holds identity and what the cloud cannot return, two plain applies
that change an attribute the provider reads back are last writer wins at the
cloud API. `choudoufu apply <planfile>` re-reads the live system and exits 3 if
a value in its changes moved since approval. Keep one apply at a time per estate
in CI anyway.
