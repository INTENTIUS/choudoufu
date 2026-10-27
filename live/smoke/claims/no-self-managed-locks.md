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

### Go tests: two writers, one record

    go test ./pkg/staterecord -run '^TestS3StoreConformance$/^PutIfVersionWithStaleVersionConflicts$'
    go test ./internal/live/projection -run '^TestWriteBackVersionConflict$'

Every record write is one conditional `PutObject` carrying the version it
read (`If-Match`). `TestS3StoreConformance/PutIfVersionWithStaleVersionConflicts`
races two writers on one record against an in-process S3 that enforces
`If-Match` as S3 does: once one has moved the record, the other's write
is refused with a conflict naming both versions. A `PutObject` with no
`If-Match`, the corruption `just smoke two-writers-one-record`'s
`BREAK=1` built until #2288, fails it. `TestWriteBackVersionConflict` is
the loser at the write-back: the record is not overwritten and the run
stops on an error naming both versions. Both run in CI on every commit,
and CLAIMS.md gives the CI run each was last recorded from.

Two approved plans that change one attribute the provider reads back are
refereed by the apply's re-read: steps 8-14 of
[apply-what-was-approved](apply-what-was-approved.md#on-aws). A run killed
with SIGKILL leaving nothing behind is
[a-killed-apply-hides-nothing](a-killed-apply-hides-nothing.md).

### TestS3StoreCASUnderEverySSEFlavour, on real AWS

    SMOKE_REAL_AWS=1 just test-sse

Real AWS, maintainer-run, and only when `pkg/staterecord/s3.go` changes.
`just test-sse` makes four buckets and a KMS key, runs the conditional-write
suite under SSE-S3, SSE-KMS with the AWS-managed key, SSE-KMS with a
customer managed key and DSSE-KMS, and removes what it made. Under the
three KMS flavours the ETag is not the payload's MD5, and the store must
treat it as opaque. Record its log with `go run ./tools/gotest-record -log`.

## On Kubernetes

### k8s-records-in-the-cluster, step 3

    just smoke k8s-records-in-the-cluster
    BREAK=1 just smoke k8s-records-in-the-cluster

The scenario is [claim 29's](a-wrong-bucket-is-refused.md#on-kubernetes);
step 3 is this claim on the cluster's record store (`record_store
"kubernetes"`, one Secret per record, `resourceVersion` as the conditional
write). It kills an apply with SIGKILL and the next run carries on, with
no Lease and nothing lock-shaped in the records namespace. `BREAK=1`
plants a Lease and a lock-named Secret, and step 3's check must fail.

### TestKubernetesTwoWritersOneRecord, on kind

    just test-kind

Two writers' first requests are held on the wire until both are parked:
in each of twelve rounds one write lands and the other is refused with a
conflict naming both versions. With
`CHOUDOUFU_K8S_RECORD_RACE_BREAK=1` each write becomes the stock `backend
"kubernetes"` read-then-update, and the test must fail with both writes
landed. Step 10 of the scenario until #2288; maintainer-run at release.
