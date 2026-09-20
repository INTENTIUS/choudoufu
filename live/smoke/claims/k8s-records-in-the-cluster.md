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
BREAK=1 just smoke k8s-records-in-the-cluster and explain why the two
refusals become successes.
```

The five steps:

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

The `BREAK=1` run takes both fences away and requires what they refused to
go through: the plan identity is given cluster-wide secret reads and must
then list the other estate's records, and the admission policy is removed
and the cross-estate write into a record Secret must then land.

Anyone who can `get secrets` in the records namespace reads every recorded
value, which is the same bargain `s3:GetObject` on the bucket makes for
the other remote store. That is what step 4 is about, and it is why the
namespace is per estate by default rather than shared.
