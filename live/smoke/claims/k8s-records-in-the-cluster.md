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
no Lease and has no lock of any kind.

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
BREAK=1 just smoke k8s-records-in-the-cluster and explain why the three
refusals become successes.
```

The nine steps. The first five measure the store; the last four measure
what it checks about the cluster before it writes a record.

1. `the Store contract, against this cluster's own API server` - the
   conformance suite every record store is held to, run against kind. The
   step fails if the suite skips, and fails if fewer than its 17 cases
   ran: client-go's fake clientset assigns no `resourceVersion`, so every
   version case would pass vacuously against a fake and none of it would
   be evidence.
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
   cluster, and not on every plan` - the four assertions are facts about
   the cluster, so they are asked once, on the run that created the
   sentinel. The API server's own `apiserver_request_total` counter for
   `selfsubjectaccessreviews` is the measurement: it moves on first
   contact and does not move across the two plans after it. The scoped
   identity cannot read two of the four, and says so by name on every
   run rather than reporting them as passes.
7. `each assertion refuses by name, on this cluster, for its own reason` -
   `choudoufu live-cluster` asks the same four questions without running
   a plan and without writing anything. kind supplies two of the
   failures itself: its API server carries no
   `--encryption-provider-config`, and a cluster-admin can read every
   records namespace there is. The binding is removed for a third, and a
   namespace that does not exist gets the fourth, in the store's own
   words.
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

The `BREAK=1` run takes the three fences away and requires what they
refused to go through: the plan identity is given cluster-wide secret
reads and must then list the other estate's records, the admission policy
is removed and the cross-estate write into a record Secret must then land,
and the scoped identity whose first contact step 6 passed is given
cluster-wide secret reads, after which the same first contact must be
refused on `read_isolation`.

Two of the four assertions cannot be answered on every cluster. Whether
Secrets are encrypted at rest is an API server flag, readable where the
API server's own Pod is and not on a managed control plane; reading the
estate boundary policy needs cluster-scoped `get`. A run says so on every
run, by name, and never calls it a pass; `choudoufu live-cluster`, run by
an identity that holds those reads, answers the question and exits
non-zero until it can.

Anyone who can `get secrets` in the records namespace reads every recorded
value, which is the same bargain `s3:GetObject` on the bucket makes for
the other remote store. That is what step 4 is about, and it is why the
namespace is per estate by default rather than shared.
