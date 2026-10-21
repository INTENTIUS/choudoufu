---
title: "Claim 29: The record store refuses what it can't trust"
claim: a-wrong-bucket-is-refused
---

# Claim 29: The record store refuses what it can't trust

The store is checked before anything is written: a store that cannot keep
its records is refused by name, a waiver names what it waives on every
run, and a read that comes back short fails the plan.

Each scenario runs from the repository root and ends on a `PASS` line; its
`BREAK=1` run breaks the thing the proof rests on and must print a
`caught` line. [The README](README.md) says what each needs installed.

## On AWS

### a-wrong-bucket-is-refused

    just smoke a-wrong-bucket-is-refused
    BREAK=1 just smoke a-wrong-bucket-is-refused

Versioning, a lifecycle rule expiring noncurrent versions, and a
public-access block are asserted; a bucket missing one, or a role that
cannot read one, is refused by name before anything is applied. `BREAK=1`
rebuilds choudoufu with the check reporting nothing (needs Go), and an
apply against a suspended-versioning bucket must be caught going through.

### a-waiver-names-what-it-waives

    just smoke a-waiver-names-what-it-waives
    BREAK=1 just smoke a-waiver-names-what-it-waives

`allow_insecure = ["versioning"]` waives that one assertion and keeps the
others, and every run under it names the waiver and its cost. `BREAK=1`
builds a binary that warns on the first run only, and run two must be
caught proceeding in silence.

### a-bulk-read-is-complete-or-it-fails

    just smoke a-bulk-read-is-complete-or-it-fails
    BREAK=1 just smoke a-bulk-read-is-complete-or-it-fails

The bulk read is a LIST and a fan-out of GETs; one failed GET fails the
read, and a record listed but read as absent is refused at the plan.
`BREAK=1` builds the pre-#1355 fan-out that drops a failed key, and its
plan must be caught creating a resource that exists; `BREAK_CROSSCHECK=1`
removes the listed-but-absent check, and must be caught too.

## On Kubernetes

### k8s-records-in-the-cluster

    just smoke k8s-records-in-the-cluster
    BREAK=1 just smoke k8s-records-in-the-cluster

Needs Go. `record_store "kubernetes"` keeps each record as a Secret in the
estate's own namespace, with `resourceVersion` as the conditional write.
On first contact the store checks the namespace, its RBAC scope,
encryption at rest and the estate boundary, and refuses each broken
assertion by name (steps 6 to 8), a Role one verb short included. A
waiver names what it waives on every run and `choudoufu live-cluster`
ignores it (step 11). A listing that fails after its first page fails the
plan (step 12). Steps 3 and 10 are
[claim 2's](no-self-managed-locks.md#on-kubernetes) and steps 2, 4, 5 and
9 [claim 28's](a-name-prefix-shares-no-keys.md#on-kubernetes). `BREAK=1`
takes each fence away and requires what it refused to go through.

### the deleted records namespace, in k8s-a-deleted-namespace-is-gone

    just smoke k8s-a-deleted-namespace-is-gone

Step 7 of [claim 1's scenario](no-silent-orphans.md#on-kubernetes): the
record store's namespace deleted under a converged estate is refused by
name on the plan and the apply, and nothing is written.
