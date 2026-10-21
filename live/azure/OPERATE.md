# Operate

Both Azure estates, `reference-azure` and `corpus-azapi-role-assignment`,
clear all 15 gauntlet stages on floci-az `7f079c2` (2026-10-21, azure
`fbbc1c3bea`), with their records in `record_store "azurerm"` on floci-az
blob storage. The rename, remove, count, replace, crash and teardown stages
are the operations this page describes.

## Rename

A rename rewrites the `tofu-address` tag, as on AWS. The ARM id does not
change, so `live-mv <old> <new>`, or a `moved` block and an ordinary plan,
is one in-place tag update and no replacement. Tag keys are matched
case-insensitively: a marker the object carries as `Tofu-Address` is
rewritten under that spelling, never joined by a lowercase twin.

A rename that changes the object's `name` is a different thing on Azure.
The name is part of the ARM id, so the object at the new id is a new
object. Under `create_before_destroy` it is created first and the old one
destroyed after it, the order the gauntlet's `day2_replace` stage checks on
floci-az.

Moving resources between estates goes by resource group, because the
Policy guard refuses a write that changes `tofu-estate`.
[Gate](GATE.md) has the sequence.

## Records

Every managed object has a record, as on AWS
([Records](https://intentius.io/choudoufu/docs/model/values/)). A team keeps
them where both operators and CI can read them. For an Azure-only estate
that is a Storage container, with no AWS account anywhere:

```hcl
record_store "azurerm" {
  storage_account_name = "acmerecords"
  container_name       = "records"
  resource_group_name  = "records-rg"
}
```

The arguments and the `ARM_*` environment variables are stock's `azurerm`
backend's, and authentication goes through stock's own code. Each record is
one blob under the three prefixes the bucket uses, `tofu-records/`,
`tofu-hints/` and `tofu-outputs/`, carrying `tofu-estate` as a blob index
tag written in the same Put Blob. A create sends `If-None-Match: *`, an
update or delete sends `If-Match` with the ETag the writer read, and no
lease is taken (`internal/live/staterecord/azureblob.go`).

Before it writes a record the store checks three settings on the storage
account: blob versioning on, a lifecycle rule that deletes previous
versions and never current ones, and anonymous access disallowed. They are
read through ARM, so the store needs an Azure AD credential beside any
account key. [`examples/record-store-azurerm/CONTRACT.md`](../../examples/record-store-azurerm/CONTRACT.md)
has the three, their waivers, and the read fence.

Another estate's outputs are read through the same store, as on `s3`. A
`reads_outputs_of` block is refused on `azurerm`; the ABAC condition lists
only the estate's own prefixes, so a consumer needs its own grant on the
producer's `tofu-outputs/` prefix.

## Remove

An object carrying this estate's marker that no block declares is proposed
for deletion under the default policy. The sweep is one Azure Resource
Graph query per subscription the estate's resources resolve to, matching
`tofu-estate` case-insensitively (`internal/live/discovery/resourcegraph.go`).
Resource Graph lists any type, so a type with no admission row is still
found once its last block is gone.

Two kinds of object are held, never swept: one whose ARM `managedBy`
names a resource that owns its lifecycle (an AKS cluster on its node
resource group, a managed application on its managed resource group), and
one carrying an `aks-managed-` tag. AKS copies the cluster's tags onto its
node resource group, the estate's marker among them, so without that rule
the group would read as an orphan. Any other `managedBy` is not a hold: ARM
sets an attached managed disk's to its virtual machine, and that disk is
still the estate's, proposed for removal when its block is gone (#2133). An
object whose ARM id a block declares binds to that block before either
rule is asked, so a declared object is never read as held.

AKS node pools: per Microsoft's "Use Azure tags in AKS"
(learn.microsoft.com/azure/aks/use-tags), a node pool's tags go to its
virtual machine scale set and to every instance, so both carry the node
pool's marker. The instances are in Resource Graph's `computeresources`
table, which the sweep does not query, so they are never listed. The scale
set is held when it carries an `aks-managed-` tag; no Microsoft page lists
which ones AKS writes on it, so that is for a run against real AKS to
confirm (`substrate.ControllerHeld`'s doc comment has the sources).

Resource Graph is an index, and Azure documents a lag before a delete
reaches it. A destroy therefore writes a record tombstone, as on AWS, and
the next plan reads it before calling a lingering marker a second claimant.

A sweep that cannot see everything says so. A subscription a declared id
names that no provider block names is warned by name, and so is one the
credential cannot read; either leaves the sweep incomplete rather than
empty. An unregistered `Microsoft.ResourceGraph` provider is refused by
name, with the `az provider register` line.

Two hazards are the operator's. Deleting a resource group takes every
resource in it, untaggable children included, and the group's role is all
that stands in the way. If the boundary template was applied with
`deleteLock=true`, its `denyAction` refuses the estate's own destroys and
replaces until a policy exemption covers them ([GATE.md](GATE.md#what-it-does-not-fence)).
The plan names the object; the approval is yours.

## Two runs at once

Record writes are conditional, so two runs cannot both win a record. The
cloud writes are not: an ARM write is a PUT, and two plain applies that
touch one resource leave whichever landed last. Apply a saved plan, which
re-reads the live system and refuses if it moved
([`live/CONCURRENCY.md`](../CONCURRENCY.md)).
