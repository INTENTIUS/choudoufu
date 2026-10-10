---
title: "Claim 2: Nothing is held"
claim: no-self-managed-locks
---

# Claim 2: Nothing is held

Stock takes a lock before it touches state, because two writers corrupting
one file is fatal when the file is the record, and a run that dies holding
the lock strands the next one until someone runs `force-unlock`. Nothing
here takes a lock. Two applies racing on a resource are refereed by the
platform's own uniqueness rules, two racing on a record by one
conditional write that lands or is refused, and two saved plans racing on
an attribute the provider reads back by the apply's re-read of the live
system. A run killed at any point leaves nothing behind for the next run
to clear.

Two plain `apply` runs that change the same attribute of one cloud
resource, one the provider reads back, are last-writer-wins at the cloud
API, as they are under stock.

Each scenario runs from the repository root and ends on a `PASS` line; its
`BREAK=1` run breaks the thing the proof rests on and must print a
`caught` line. [The README](README.md) says what each needs installed.

## On AWS

### no-self-managed-locks

    just smoke no-self-managed-locks
    BREAK=1 just smoke no-self-managed-locks

`force-unlock` refuses with the true reason, that there is no lock. Two
applies of one client-named IAM role start together; the cloud's
name-uniqueness referees, "Acquiring state lock" appears in neither
output, and the loser's next plan is `No changes.` Server-assigned
resources can really duplicate, and the duplicate surfaces as a named pair
for a human to resolve with one delete. `BREAK=1` strips the race winner's
marker, and convergence must fail.

### two-writers-one-record (claim 32 until #1817)

    just smoke two-writers-one-record
    BREAK=1 just smoke two-writers-one-record

Every record write is one conditional `PutObject` carrying the version it
read (`If-Match`). A proxy holds two applies' writes until both are in
flight, over several rounds: exactly one lands, the other is told the
version it expected and the one it found, and its recovery is an ordinary
re-plan. A writer killed mid-write strands nothing. `BREAK=1` rebuilds
choudoufu with no `If-Match` (`go build -overlay`, so it needs Go), and
both racing applies must be caught reporting success over one record.

### two-saved-plans-one-attribute (#1504)

    just smoke two-saved-plans-one-attribute
    BREAK=1 just smoke two-saved-plans-one-attribute

An ordinary cloud resource's record holds its identity and what the cloud
cannot give back, so an attribute the provider reads back is not in it and
no conditional write referees a race on it. Two checkouts of one estate
save plans changing one SQS queue's `visibility_timeout_seconds` from 30,
one to 60 and one to 90; the scenario first proves the provider reads that
attribute back from the cloud. The first plan applies. The second re-reads
the queue, finds 60 where its approval said 30, and is refused with exit 3
naming `before.visibility_timeout_seconds` (#878); the queue keeps 60, and
the second checkout's recovery is a re-plan. The two applies run one after
the other: two that both re-read before either writes are not covered.
`BREAK=1` rebuilds choudoufu with the before-values comparison removed
(`go build -overlay`, so it needs Go), and the second apply must be caught
reporting success with the queue on 90.

### backend-sets-itself-up (claim 4 until #1817; real AWS)

    SMOKE_REAL_AWS=1 just smoke backend-sets-itself-up
    SMOKE_REAL_AWS=1 BREAK=1 just smoke backend-sets-itself-up

Maintainer-run against a real account: it stands the record store bucket
up with the shipped `just up`, and the pinned emulator's CloudFormation
reports `CREATE_COMPLETE` while applying none of the bucket's properties.
Stock's day one is a bucket, versioning, a lock table and IAM for both.
Here the lock table is gone, and with it the lock: a run killed in the
middle of an apply strands nothing, and the next run goes ahead. The
bucket still wants versioning, a lifecycle rule and a public-access block,
so the list is no shorter; what changes is that none of those sits in the
path of every apply. `BREAK=1` makes the store unreachable and requires
the run to refuse by name with nothing proposed.

### cas-holds-under-every-sse-flavour (claim 33 until #1817; real AWS)

    SMOKE_REAL_AWS=1 just smoke cas-holds-under-every-sse-flavour
    SMOKE_REAL_AWS=1 BREAK=1 just smoke cas-holds-under-every-sse-flavour

Maintainer-run: an emulator does not reproduce the ETag semantics that are
the subject. The conditional write holds under SSE-S3, SSE-KMS with the
AWS-managed key, SSE-KMS with a customer managed key and DSSE-KMS, because
the store treats the ETag as opaque. `BREAK=1` builds a binary that checks
each ETag is the MD5 of the payload, a check someone might add in good
faith; it must work under SSE-S3 and fail, on that check alone, under all
three KMS flavours.

## On Kubernetes

### k8s-records-in-the-cluster, steps 3 and 10

    just smoke k8s-records-in-the-cluster
    BREAK=1 just smoke k8s-records-in-the-cluster

The scenario is [claim 29's](a-wrong-bucket-is-refused.md#on-kubernetes);
two of its steps are this claim on the cluster's record store
(`record_store "kubernetes"`, one Secret per record, `resourceVersion` as
the conditional write). Step 3 kills an apply with SIGKILL and the next
run carries on, with no Lease and nothing lock-shaped in the records
namespace. Step 10 holds two writers' first requests on the wire until
both are parked: twelve rounds, each landing one write and refusing the
other with a conflict naming both versions, no Lease taken. Its `BREAK=1`
control swaps each write for the stock `backend "kubernetes"`
read-then-update, and all twelve rounds must end with both writes landed.
