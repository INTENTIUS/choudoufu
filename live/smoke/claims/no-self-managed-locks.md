---
title: "Claim 2: Nothing is held"
claim: no-self-managed-locks
---

# Claim 2: Nothing is held

Nothing takes a lock. The platform's uniqueness rules referee racing
applies on a resource, one conditional write referees racing writes on a
record, and a run killed at any point leaves nothing for the next run to
clear.

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
output, and the loser's next plan is `No changes.` `BREAK=1` strips the
race winner's marker, and convergence must fail.

### two-writers-one-record

    just smoke two-writers-one-record
    BREAK=1 just smoke two-writers-one-record

Every record write is one conditional `PutObject` carrying the version it
read (`If-Match`). A proxy holds two applies' writes until both are in
flight, over several rounds: exactly one lands, the other is told the
version it expected and the one it found, and its recovery is an ordinary
re-plan. A writer killed mid-write strands nothing. Needs Go. `BREAK=1`
rebuilds choudoufu with no `If-Match` (`go build -overlay`), and both
racing applies must be caught reporting success over one record.

Two approved plans that change one attribute the provider reads back are
refereed by the apply's re-read: steps 8-14 of
[apply-what-was-approved](apply-what-was-approved.md#on-aws).

### cas-holds-under-every-sse-flavour

    SMOKE_REAL_AWS=1 just smoke cas-holds-under-every-sse-flavour
    SMOKE_REAL_AWS=1 BREAK=1 just smoke cas-holds-under-every-sse-flavour

Real AWS, maintainer-run with `SMOKE_REAL_AWS=1`. Needs Go. The
conditional write holds under SSE-S3, SSE-KMS with the AWS-managed key,
SSE-KMS with a customer managed key and DSSE-KMS, because the store
treats the ETag as opaque. `BREAK=1` builds a binary that checks each
ETag is the MD5 of the payload; it must work under SSE-S3 and fail, on
that check alone, under all three KMS flavours.

## On Kubernetes

### k8s-records-in-the-cluster, steps 3 and 10

    just smoke k8s-records-in-the-cluster
    BREAK=1 just smoke k8s-records-in-the-cluster

The scenario is [claim 29's](a-wrong-bucket-is-refused.md#on-kubernetes);
steps 3 and 10 are this claim on the cluster's record store
(`record_store "kubernetes"`, one Secret per record, `resourceVersion` as
the conditional write). Step 3 kills an apply with SIGKILL and the next
run carries on, with no Lease and nothing lock-shaped in the records
namespace. Step 10 holds two writers' first requests on the wire until
both are parked: in each of twelve rounds one write lands and the other
is refused with a conflict naming both versions, and no Lease is taken.
`BREAK=1` swaps each write for the stock `backend "kubernetes"`
read-then-update, and all twelve rounds must end with both writes landed.

## On GCP

Expected to be proven by steps of `just smoke gcp-records-in-a-bucket`, as
`k8s-records-in-the-cluster` proves it on Kubernetes: the `gcs` record
store writes with `ifGenerationMatch` and holds no lock
([#2122](https://github.com/INTENTIUS/choudoufu/issues/2122)). The cell is
open until that scenario is registered and runs.
