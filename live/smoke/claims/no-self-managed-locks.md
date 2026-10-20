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

Two approved plans that change one attribute the provider reads back from
the cloud are refereed by the apply's re-read, which is claim 15's
mechanism: that race is steps 8-14 of
[apply-what-was-approved](apply-what-was-approved.md#on-aws), and was
two-saved-plans-one-attribute here until it merged there.
backend-sets-itself-up (claim 4) was dropped: a killed apply stranding
nothing is two-writers-one-record's last step, and the bucket `just up`
builds is the recommended secure configuration demo's.

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

## On Azure

Open. Nothing has run on Azure yet. `record_store "azurerm"` creates with `If-None-Match: *`, updates and deletes with `If-Match` on the ETag, and takes no lease ([the contract](../../../examples/record-store-azurerm/CONTRACT.md)); no race of two writers is written for floci-az.
