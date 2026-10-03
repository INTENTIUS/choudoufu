---
title: "Claim 28: An estate reaches only its own records"
claim: a-name-prefix-shares-no-keys
---

# Claim 28: An estate reaches only its own records

Every estate's records can share one store. What keeps an estate to its
own is the store's layout and the platform's access control: a key
prefix and IAM on a bucket, a namespace and RBAC on a cluster. Each proof
below takes one of those away and shows another estate's records become
reachable.

Each scenario runs from the repository root and ends on a `PASS` line; its
`BREAK=1` run breaks the thing the proof rests on and must print a
`caught` line. [The README](README.md) says what each needs installed.

## On AWS

### a-name-prefix-shares-no-keys

    just smoke a-name-prefix-shares-no-keys
    BREAK=1 just smoke a-name-prefix-shares-no-keys

Two estates whose names prefix one another (`smoke-prod`,
`smoke-prod-eu`) share a bucket; the trailing slash on the prefix is all
that keeps one listing out of the other, since an object tag cannot
condition a LIST. `BREAK=1` rebuilds choudoufu without the slash (Go
needed), and the wire must show one estate fetching the other's record.

### a-new-estate-writes-its-first-record (claim 34 until #1817)

    SMOKE_REAL_AWS=1 just smoke a-new-estate-writes-its-first-record
    SMOKE_REAL_AWS=1 BREAK=1 just smoke a-new-estate-writes-its-first-record

Under the published policy a new estate's first write succeeds: the
create condition is `s3:RequestObjectTag`, because `s3:ExistingObjectTag`
reads tags an object that does not exist yet does not have. `BREAK=1`
swaps the key, and the first create must be denied.

### one-bucket-many-estates (claim 35 until #1817)

    SMOKE_REAL_AWS=1 just smoke one-bucket-many-estates
    SMOKE_REAL_AWS=1 BREAK=1 just smoke one-bucket-many-estates

Reading a neighbour's records takes two mistakes: a mis-scoped prefix is
still stopped by the object-tag Deny, and a relabel is stopped by its own
Deny (#1381). `BREAK=1` widens the prefix and removes one Deny at a time,
and each time the neighbour's record must read back.

### objects-carry-the-estate-tag (claim 36 until #1817)

    SMOKE_REAL_AWS=1 just smoke objects-carry-the-estate-tag
    SMOKE_REAL_AWS=1 BREAK=1 just smoke objects-carry-the-estate-tag

Every object written to the bucket carries `tofu-estate` (and
`tofu-address` on a record), and the published policy denies a foreign
tag. `BREAK=1` builds a binary that sends no tags, and its first write
must be denied.

### a-read-only-role-can-plan (claim 38 until #1817)

    SMOKE_REAL_AWS=1 just smoke a-read-only-role-can-plan
    SMOKE_REAL_AWS=1 BREAK=1 just smoke a-read-only-role-can-plan

A role with the read-only rendering of the policy plans an established
estate and writes nothing; a store with no sentinel is still refused by
name. `BREAK=1` restores the pre-#1416 sentinel write, and the role must
then be unable to plan at all.

The real-AWS proofs are maintainer-run: the pinned emulator does not
evaluate `s3:ExistingObjectTag` or `s3:RequestObjectTag`.

## On Kubernetes

### k8s-records-in-the-cluster, steps 2, 4, 5 and 9

    just smoke k8s-records-in-the-cluster
    BREAK=1 just smoke k8s-records-in-the-cluster

The scenario is [claim 29's](a-wrong-bucket-is-refused.md#on-kubernetes).
Step 2: a Kubernetes-only estate writes its first record as a Secret with
no AWS credentials anywhere. Step 4: a role scoped to one records
namespace cannot read another estate's records, while a role with list
and get on a shared namespace reads all of them, which is why the
namespace is per estate. Step 5: record Secrets carry `tofu-estate`, so
the estate-boundary policy fences writes to them with no new policy.
Step 9: an identity with get and list plans and writes nothing. `BREAK=1`
widens the plan role and removes the policy, and the other estate's
records must become readable and writable. Two estates whose key
prefixes prefix one another are not measured here.
