---
title: "Claim 28: An estate reaches only its own records, and what it declares"
claim: a-name-prefix-shares-no-keys
---

# Claim 28: An estate reaches only its own records, and what it declares

Every estate's records can share one store, and each estate reaches only
its own: a key prefix and IAM keep it there on a bucket, a namespace and
RBAC on a cluster. An estate reads another's recorded root outputs when
its configuration names that estate and its access grants that one read.

Each scenario runs from the repository root and ends on a `PASS` line; its
`BREAK=1` run breaks the thing the proof rests on and must print a
`caught` line. [The README](README.md) says what each needs installed.

## On AWS

### TestS3StoreConformance/NeighbourNamespacesShareNoListingOrBulkRead

    go test ./pkg/staterecord -run '^TestS3StoreConformance$/^NeighbourNamespacesShareNoListingOrBulkRead$'

Two estates whose names prefix one another (`prod`, `prod-eu`) share a
bucket, and the delimiter that ends every namespace keeps each listing and
each bulk read to its own estate. The same case runs against every store
in CI, and dropping the delimiter, which `just smoke
a-name-prefix-shares-no-keys`'s `BREAK=1` did until #2288, fails it on
each. Callers build list prefixes through `NamespacePrefix`.

### one-bucket-many-estates

    SMOKE_REAL_AWS=1 just smoke one-bucket-many-estates
    SMOKE_REAL_AWS=1 BREAK=1 just smoke one-bucket-many-estates
    SMOKE_REAL_AWS=1 BREAK_FIRST_WRITE=1 just smoke one-bucket-many-estates

Real AWS, maintainer-run with `SMOKE_REAL_AWS=1`. Two new estates' first
writes go through the published policy, whose create condition is
`s3:RequestObjectTag`. Every object either estate writes carries
`tofu-estate`, and a record also `tofu-address` (a dot in an instance key
escaped as `@d`). A mis-scoped prefix is stopped by the object-tag Deny,
and a relabel by its own Deny (#1381). Updates and deletes under
`If-Match` work as the role, and a record retagged as another estate's,
under the estate's own prefix, is refused to it by name.

`BREAK=1` widens the prefix and removes one Deny at a time, and each time
the neighbour's record must read back. `BREAK_FIRST_WRITE=1` swaps the
create condition to `s3:ExistingObjectTag`, and a new estate's first
create must be denied. That the store tags every object it writes is
`TestS3StoreTagsEveryObjectItWrites` (pkg/staterecord, in CI), which
replaced this scenario's `BREAK_UNTAGGED` arm (#2288).

### a-read-only-role-can-plan

    SMOKE_REAL_AWS=1 just smoke a-read-only-role-can-plan
    SMOKE_REAL_AWS=1 BREAK=1 just smoke a-read-only-role-can-plan

Real AWS, maintainer-run with `SMOKE_REAL_AWS=1`. A role with the
read-only rendering of the policy plans an established estate and writes
nothing, and a store with no sentinel is refused by name. `BREAK=1`
restores the pre-#1416 sentinel write, and the role must then be unable
to plan at all.

### an-estate-reads-another-by-declaring-it

    just smoke an-estate-reads-another-by-declaring-it
    BREAK=1 just smoke an-estate-reads-another-by-declaring-it

Every estate records its root output values under `tofu-outputs/<estate>/`
in the record store (#1371). The consumer's configuration declares the
read:

```hcl
data "terraform_estate_outputs" "network" {
  estate = "network"
  names  = ["cluster_services_namespace"]
}
```

and its role is rendered with `render-policy.sh app <bucket>
--reads-outputs-of network`, which adds one statement,
`ReadDeclaredDependenciesOutputs`, a GetObject on `tofu-outputs/network/*`.

1. `one bucket, two roles`. A bucket that meets the bucket contract, and
   two emulator roles from the published renderer: `network`, and `app`
   with `--reads-outputs-of network`.
2. `the producer applies and records its output`. `network` applies as its
   own role, and its one root output is one object under
   `tofu-outputs/network/`.
3. `the consumer plans with the value`. `app` plans as its own role, in its
   own directory. `terraform_data.service` is planned with
   `input = "cluster-services"`, read from network's record, and the plan
   carries the warning `Values from another estate are as of its last
   apply` with the time the record was written.
4. `the producer is destroyed, and its values go with it`. `network` runs
   `apply -destroy`, nothing is left under `tofu-outputs/network/`, and the
   consumer's same plan stops with
   `Another estate has not recorded this output`, naming estate network
   and the output.

`BREAK=1` renders the consumer's role without `--reads-outputs-of network`
while its configuration still declares the read. The plan must fail with
`This estate may not read another estate's outputs`, naming estate network
and the `--reads-outputs-of network` flag, and must not show network's
value.

## On Kubernetes

### k8s-records-in-the-cluster, steps 2, 4, 5 and 9

    just smoke k8s-records-in-the-cluster
    BREAK=1 just smoke k8s-records-in-the-cluster

The scenario is [claim 29's](a-wrong-bucket-is-refused.md#on-kubernetes).
Step 2: a Kubernetes-only estate writes its first record as a Secret with
no AWS credentials anywhere. Step 4: a role scoped to one records
namespace reads only that estate's records. Step 5: record Secrets carry
`tofu-estate`, so the estate-boundary policy fences writes to them. Step
9: an identity with get and list plans and writes nothing. `BREAK=1`
widens the plan role and removes the policy, and the other estate's
records must become readable and writable.

### k8s-an-estate-reads-another-by-declaring-it

    just smoke k8s-an-estate-reads-another-by-declaring-it
    BREAK=1 just smoke k8s-an-estate-reads-another-by-declaring-it

Needs `kind` and `kubectl`. The declaration is the reader's `record_store`
block: `reads_outputs_of "network" {}` reads network's records namespace
through a store that can only get. With nothing declared, the read is
refused before it is sent. Declared, and with a Role granting one `get` on
network's output Secret, the consumer plans with the value, says when it
was recorded, and changes nothing of network's; after network's destroy
deletes that output, the consumer is told so by name. `BREAK=1` declares
the read and withholds the Role, and the plan must refuse, naming the
estate, its namespace and the Role to grant.
