---
title: "Claim 29: The record store refuses what it can't trust"
claim: a-wrong-bucket-is-refused
---

# Claim 29: The record store refuses what it can't trust

The store is checked before anything is written: a store that cannot keep
its records is refused by name, a waiver names what it waives on every
run, and a read that comes back short fails the plan.

On AWS the proofs are Go tests (GitHub issue #2288): the smoke scenarios
that stood here rebuilt choudoufu with a fault each test below already
fails on. The ones with no gate run in CI on every commit, and CLAIMS.md
gives the CI run each was last recorded from. Each scenario runs from the
repository root and ends on a `PASS` line; its `BREAK=1` run breaks the
thing the proof rests on and must print a `caught` line. [The
README](README.md) says what each needs installed.

## On AWS

### A wrong bucket is refused

    go test ./pkg/staterecord -run '^TestBucketContract$'
    go test ./internal/live/projection -run '^TestABadBucketIsRefusedOnEveryFirstContactUntilItIsFixed$'
    go test ./internal/command -run '^TestBeforeApplyAssertsTheBucketContract$'

Versioning, a lifecycle rule expiring noncurrent versions, and a
public-access block are asserted; a bucket missing one, or a role that
cannot read one, is refused by name. `TestBucketContract` holds each
finding, and CheckBucketContract reporting nothing, which `just smoke
a-wrong-bucket-is-refused`'s `BREAK=1` built until #2288, fails it.
`TestABadBucketIsRefusedOnEveryFirstContactUntilItIsFixed` refuses the
bucket on every first contact until it is fixed, writing nothing, and
`TestBeforeApplyAssertsTheBucketContract` asserts it before every apply.

### A waiver names what it waives

    go test ./pkg/staterecord -run '^TestAWaiverReachesOnlyWhatItNames$'
    go test ./internal/live/projection -run '^TestFirstContactHonoursTheWaiver$'
    go test ./internal/command -run '^TestAWaiverWarnsOnEveryRunOfAnEstate$'

`allow_insecure = ["versioning"]` waives that one assertion and keeps the
others (`TestAWaiverReachesOnlyWhatItNames`, `TestFirstContactHonoursTheWaiver`),
and every run under it names the waiver and its cost:
`TestAWaiverWarnsOnEveryRunOfAnEstate` runs an estate's first apply, a
plan once the state cache exists and a second apply, and requires the
warning on each. The warning emitted on the first run only, the binary
`just smoke a-waiver-names-what-it-waives`'s `BREAK=1` built until #2288,
fails it on run two.

### A bulk read is complete or it fails

    go test ./pkg/staterecord -run '^TestS3GetAllIsCompleteOrItFails$'
    go test ./pkg/staterecord -run '^TestBoundedFanOutNeverReportsSuccessForWorkItDidNotDo$'
    go test ./internal/live/projection -run '^TestARecordTheListNamesAndTheReadDoesNotIsRefused$'
    go test ./internal/live/projection -run '^TestADestroyOverAStoreThatListsARecordItReadsAsAbsentFails$'

The bulk read is a LIST and a fan-out of GETs; one failed GET fails the
read (`TestS3GetAllIsCompleteOrItFails`), and the fan-out never reports
work it did not do (`TestBoundedFanOutNeverReportsSuccessForWorkItDidNotDo`,
which the pre-#1355 fan-out the scenario's `BREAK=1` built fails). A
record the listing names and the read answers as absent is refused at the
plan (`TestARecordTheListNamesAndTheReadDoesNotIsRefused`) and on the path
an `apply -destroy` plans from, through the run cache
(`TestADestroyOverAStoreThatListsARecordItReadsAsAbsentFails`), where it
would otherwise report one of two destroyed as a success. Removing the one
call to `refuseListedButReadAsAbsent`, the scenario's `BREAK_CROSSCHECK=1`,
fails both.

## On Kubernetes

### k8s-records-in-the-cluster

    just smoke k8s-records-in-the-cluster
    BREAK=1 just smoke k8s-records-in-the-cluster

Needs Go. `record_store "kubernetes"` keeps each record as a Secret in the
estate's own namespace, with `resourceVersion` as the conditional write.
On first contact the store checks the namespace, its RBAC scope,
encryption at rest and the estate boundary, and refuses a broken
assertion by name (steps 6 and 8), a Role one verb short included. A
waiver names what it waives on every run and `choudoufu live-cluster`
ignores it (step 11). Step 3 is
[claim 2's](no-self-managed-locks.md#on-kubernetes) and steps 2, 4, 5 and
9 [claim 28's](a-name-prefix-shares-no-keys.md#on-kubernetes). `BREAK=1`
takes each fence away and requires what it refused to go through.

### Go tests on kind: the Store contract and the paged listing

    just test-kind

Maintainer-run at release. `TestKubernetesStoreConformance` holds the
shared Store contract against a real API server (step 1 of the scenario
until #2288). `TestKubernetesListReadsEveryPageOnARealAPIServer` reads
every page of a paged LIST, and
`TestKubernetesAnExpiredContinueTokenFailsTheListingOnARealAPIServer`
answers the second page 410 Expired and requires List and GetAll to fail
whole, never returning the first page as the estate (step 12 until #2288).

### Go tests: each cluster assertion refuses by name

    go test ./pkg/staterecord -run '^TestClusterContract'

Step 7 of the scenario until #2288, in CI on every commit:
`TestClusterContractNamespaceAccessNamesTheVerbsItLacks`,
`TestClusterContractReadIsolationRefusesAReadableForeignNamespace`,
`TestClusterContractEncryptionAtRest`, `TestClusterContractEstateBoundary`
and `TestClusterContractReportsAnAbsentNamespaceInTheStoresOwnWords`.

### the deleted records namespace, in k8s-a-deleted-namespace-is-gone

    just smoke k8s-a-deleted-namespace-is-gone

Step 7 of [claim 1's scenario](no-silent-orphans.md#on-kubernetes): the
record store's namespace deleted under a converged estate is refused by
name on the plan and the apply, and nothing is written.
