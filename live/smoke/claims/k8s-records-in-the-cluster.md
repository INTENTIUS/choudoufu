---
title: "Claim 39: Records live in the cluster"
claim: k8s-records-in-the-cluster
---

# Claim 39: Records live in the cluster

A Kubernetes-only estate used to have two places to keep its records.
`record_store "local"` is one machine's disk, which a second operator or a
fresh CI runner cannot read. `record_store "s3"` is an AWS account, a
bucket and a role, for a team that runs nothing on AWS. `record_store
"kubernetes"` is the third: one Secret per record, in a namespace of the
estate's own, with `metadata.resourceVersion` as the conditional write.

It is not the stock `backend "kubernetes"` holding the plan cache. That
backend takes a coordination.k8s.io Lease per workspace, and a lock per
estate is what [#1332](https://github.com/INTENTIUS/choudoufu/issues/1332)
ruled out in favour of one conditional write per record. This store takes
no Lease. Steps 3 and 10 measure that the way it can be measured from
outside: after an apply killed with SIGKILL, and again after twelve
contended writes, the records namespace holds no Lease and no object whose
name contains `lock`.

A record's Secret is named `tofu-record-` and the SHA-256 of its key. A
record key carries `/` and base64url runs and is routinely past the 253
characters a Kubernetes object name holds, so the name is a hash and the
key itself is in the `choudoufu.intentius.io/record-key` annotation, which
is what a listing reads its keys back out of. The estate goes on the
object as the `tofu-estate` label, the same marker every other object in
the estate carries; the resource address goes in an annotation, because a
label value caps at 63 characters and an address does not.

Three things bound what the store will do. A Secret holds one MiB, so a
record over that is refused by name, before the request, measured after
compression. An estate name may be 128 characters and a label value may
not, so an estate whose name cannot be a label value is refused at open
rather than writing records outside the fence. And a namespace that does
not exist is refused by name with the `kubectl` line that creates it,
because a list in an absent namespace answers empty and an empty listing
reads as an empty estate.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and that kind, kubectl and Go are installed. From the repo
root run:

  just smoke k8s-records-in-the-cluster

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke k8s-records-in-the-cluster and explain why the first two
controls turn a refusal into a success while the third turns a success into
a refusal, and why the last seven run a step's own check against a world
or a binary built to fail it.
```

The twelve steps. The first five measure the store, the next four measure
what it checks about the cluster before it writes a record, and the last
three are claims 32, 30 and 31 on this store.

1. `the Store contract, against this cluster's own API server` - the
   conformance suite every record store is held to, run against kind. The
   step fails if the suite skips, fails if `go test` matched no test at
   all, and fails unless the number of cases that passed is exactly the 18
   the suite has: client-go's fake clientset assigns no `resourceVersion`,
   so every version case would pass vacuously against a fake and none of
   it would be evidence.
2. `a Kubernetes-only estate applies with no AWS credentials in the
   environment` - every `AWS_` variable unset, `AWS_CONFIG_FILE` and
   `AWS_SHARED_CREDENTIALS_FILE` pointed at `/dev/null`, IMDS disabled.
   A `terraform_data` is in the root, so the run cannot finish without
   the store. The record Secrets are then read back with `kubectl`.
3. `an apply killed with SIGKILL, and the very next run carries on` -
   claim 4's headline on this store. There is no Lease and nothing
   lock-shaped in the records namespace, and the next run does not
   mention a lock.
4. `a role scoped to one records namespace cannot read another estate's
   records` - RBAC has no predicate on a label and admission is never
   consulted for a get, so the namespace is the read boundary. The
   control runs first and reads the other estate's records as
   cluster-admin, and the scoped identity reads its own estate's records
   before being refused the other's, so neither half can pass for the
   wrong reason.
5. `the estate boundary fences a write to a record Secret with no new
   policy` - `live/kubernetes/estate-boundary.yaml` matches every object
   carrying `tofu-estate` and every record Secret carries one, so the
   policy an estate already installs covers its records.

6. `the cluster contract runs on an estate's first contact with the
   cluster, and not on every plan` - the four assertions are asked on an
   estate's first contact with the store, which is the run that creates
   the sentinel, and again before any run that writes a record: an apply,
   a `live-mv` that is not a dry run, a `live-import -approve`. An
   ordinary plan asks none of them. Two measurements say so. The API
   server's own `apiserver_request_total` counter for
   `selfsubjectaccessreviews` moves on first contact and does not move
   across the two plans after it, and a failed read of that counter fails
   the step rather than counting zero. The plans' own output
   is the second: the scoped identity cannot read three of the four - it
   cannot list kube-system's Pods, cannot get a
   `ValidatingAdmissionPolicy`, and cannot list the namespaces the other
   estates keep records in - so the run that asks them names all three,
   and the two plans name none, because they asked nothing.
7. `each assertion refuses by name, on this cluster, for its own reason` -
   `choudoufu live-cluster` asks the same four questions without running
   a plan and without writing anything. kind supplies two of the
   failures itself: its API server carries no
   `--encryption-provider-config`, and a cluster-admin can read every
   records namespace there is. The binding is removed for a third, and a
   namespace that does not exist gets the fourth, in the store's own
   words. `estate_boundary` also says which half of itself it asked: the
   policy is compared against the file this repository ships, and the
   `use` grant the policy's own CEL reads is asked for only when an
   estate is named.
8. `a Role short one verb is refused at first contact` - the same
   assertion stopping an apply rather than reporting on a cluster. The
   Role holds four of the five verbs the store uses, so the sentinel
   write goes through and the contract then refuses the run by name,
   naming the missing verb. The sentinel is taken back out, so the next
   run is refused the same way instead of proceeding.
9. `a plan identity needs get and list on the record Secrets and nothing
   more` - [#1370](https://github.com/INTENTIUS/choudoufu/issues/1370)
   on this store. The plan reads the estate back and proposes nothing,
   and no record Secret's `resourceVersion` moves. The contract agrees:
   the same identity passes the plan question and fails the apply
   question, naming `create`, `update` and `delete`.
10. `two writers, one record, held at the wire` -
    [claim 32](two-writers-one-record.md) on this store, which had only a
    test running one writer after the other until
    [#1441](https://github.com/INTENTIUS/choudoufu/issues/1441). Two
    writers with their own connections to the cluster read one record and
    come away with one `resourceVersion`. A `RoundTripper` wrapped around
    each writer's client parks the first request of its write, unanswered,
    until both are parked, and then lets them through one at a time. Six
    rounds of two updates and six of two creates, each requiring one
    winner, one `VersionConflictError` naming both versions, and no trace
    of the refused writer's payload in the record. Every round reports how
    far apart the two requests arrived and how long each sat on the wire,
    and a round whose requests were not parked together is counted apart
    and fails the run, because a race that did not overlap is a sequence.
11. `a waiver names what it waives on every run, and live-cluster ignores
    it` - [claim 30](a-waiver-names-what-it-waives.md) on this store. Steps
    2 to 5 run under `allow_insecure` naming `read_isolation`,
    `encryption_at_rest` and `estate_boundary`, and until
    [#1441](https://github.com/INTENTIUS/choudoufu/issues/1441) nothing
    asserted that a run says so. A fresh estate's first apply, a plan and
    a second apply must each name all three waived settings with what each
    costs, and exactly three. `choudoufu live-cluster`, run from the same
    directory so it reads the same block, must still report
    `read_isolation` and `encryption_at_rest` as FAIL, read NOT correct
    and exit non-zero, and name each waiver apart from the verdict: two
    hiding a failure, and `estate_boundary`, which this cluster passes by
    then, hiding nothing.
12. `a listing that fails after its first page fails the plan, and never
    reads as a short estate` - [claim 31](a-bulk-read-is-complete-or-it-fails.md)
    on this store. The bucket's bulk read is a LIST and a fan-out of GETs;
    this store's is one paged LIST, with every payload riding along, so
    the page is where it can come back short. The store lists 200 Secrets
    a page, so 199 padding Secrets named to sort first put the store's
    sentinel alone on page one and every record on page two. A proxy
    between the run and the API server (`live/smoke/k8sproxy.py`)
    re-terminates TLS: the run's kubeconfig points at it with
    `insecure-skip-tls-verify`, and it reaches the real API server with
    the kind admin's own CA and client certificate. It answers the second
    page with the API server's own 410 Expired, first on the listing the
    store opens with, then, with that one relayed, on the bulk read after
    it, for a plan and a destroy plan. Each run must exit non-zero naming
    the listing, the namespace and the API server's reason, and print no
    plan at all. A control plan through the unarmed proxy is empty and
    shows the listing paging, and the same plan with the fault lifted is
    empty again.

The `BREAK=1` run takes the three fences away and requires what they
refused to go through: the plan identity is given cluster-wide secret
reads and must then list the other estate's records, the admission policy
is removed and the cross-estate write into a record Secret must then land,
and the scoped identity whose first contact step 6 passed is given
cluster-wide secret reads, after which the same first contact must be
refused on `read_isolation`. A fourth control covers step 10: each write
becomes the stock backend's read-then-update, which is what
`backend "kubernetes"` does, and the same twelve rounds must then end with
both writes landed and no conflict named at all. That writer lives in the
test file and is reached only through an environment variable it reads, so
no build of choudoufu carries it.

Steps 11 and 12 each have a control that rebuilds choudoufu with
`go build -overlay`, as claims 30 and 31 do on the bucket, so the source
tree is never touched. Step 11's binary emits the waiver warning only
while the estate has no state cache, which is its first run: that run
still names all three, and step 11's check must refuse the plan after it
for naming none. Step 12's binary keeps the pages it has when a later one
fails. With the sentinel on page one it opens the store, reads the estate
as holding no record, and plans to create all six resources, which exist;
step 12's check must refuse that run for exiting 0.

Steps 1, 2, 3, 8 and 9 each have a control of their own
([#1448](https://github.com/INTENTIUS/choudoufu/issues/1448)). Each step's
check is one shell function, and the control runs that same function
against a world built to fail it and requires the failure by name: step
1's suite run with no cluster to reach, and again with `-run` narrowed so
one case never runs; step 2's record Secrets stripped of their record-key
annotation, then of their `tofu-estate` label; a Lease and a Secret named
`tofu-state-lock` planted in step 3's namespace; step 8's Role given the
`update` verb it lacked, after which the same apply must go through and
leave records where step 8 found none; and one annotation written to a
record between step 9's two `resourceVersion` dumps, then a dump of a
namespace holding no record. The listings behind steps 3, 8 and 9 are also
run through a kubeconfig whose server is a port nothing listens on, and
each must fail rather than read the listing it never got as empty. A run
under `BREAK=1` ends with one line saying every control held.

Three of the four assertions go unanswered for the identity the docs
recommend, and one of them goes unanswered for everybody. Whether Secrets
are encrypted at rest is an API server flag naming a file that is not an
API object, so a set flag is NOT CHECKED at any permission level and only
a missing one is a refusal; reading the estate boundary policy needs
cluster-scoped `get`; and finding the other estates' records means listing
namespaces. Every run that asks them names each of them and never calls
one a pass; `choudoufu live-cluster`, run by an identity that holds those
reads, answers what can be answered and exits non-zero until it is.

Anyone who can `get secrets` in the records namespace reads every recorded
value, which is the same bargain `s3:GetObject` on the bucket makes for
the other remote store. That is what step 4 is about, and it is why the
namespace is per estate by default rather than shared.
