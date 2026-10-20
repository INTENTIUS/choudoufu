# Proof

Nothing is proven on Azure yet. Every Azure cell in
[`live/CLAIMS.md`](../CLAIMS.md) reads `open`, the `azure` lane's one
estate has never run, and no scenario has booted the emulator. A claim is
a promise and proof is per provider
([#1112](https://github.com/INTENTIUS/choudoufu/issues/1112)), so this page
says what is written, what it would prove, and what stands in the way.

## The harness

```
just smoke az-identity-is-a-tag
```

The Azure scenarios run against lex00/floci-az, pinned by digest in
`live/floci-az-image` and started by `azure_up` in `live/smoke/lib.sh`. The
shape is the AWS one: a verdict line per step, exit 0 only when every
claim held, and `BREAK=1` to manufacture the fault.

floci-az at the pin lacks several things the claims need: Resource Graph,
role assignments, Policy evaluation, and the storage settings the record
store checks. [`live/FLOCI.md`](../FLOCI.md#azure-floci-az) lists each one.
Code that needs a missing capability waits for the fork
([`live/ADDING-A-PLATFORM.md`](../ADDING-A-PLATFORM.md#emulator-policy)).

## Four drafts

The four proofs are drafts under `live/smoke/drafts/`, which keeps them out
of the claims table until a first green run of both arms
([`live/smoke/drafts/README.md`](../smoke/drafts/README.md)).

[Claim 7](../smoke/claims/identity-is-a-tag.md#on-azure),
`az-identity-is-a-tag`, applies a resource group, a VNet and an identity
through azurerm and a second identity through `azapi_resource`. It reads
both tags back with ARM GETs and requires an empty replan with no state file
and with the cache deleted. Its `BREAK=1` strips `tofu-estate` from the
VNet, and the replan must refuse the VNet by name rather than PUT over it.

[Claim 1](../smoke/claims/no-silent-orphans.md#on-azure),
`az-no-silent-orphans`, deletes a block and requires the next plan to
propose exactly that destroy, found by its tag through Resource Graph. A
resource tagged for another estate must be left alone. Its `BREAK=1`
strips the orphan's tag first, and the replan must then propose nothing.

[Claim 13](../smoke/claims/the-tag-is-the-boundary.md#on-azure),
`az-the-tag-is-the-boundary`, assigns the two Policy definitions at one
estate's group and requires another estate's apply, an untagged create and
a delete there each to be refused. Its `BREAK=1` deletes the assignments,
and the same three writes must land. The RBAC half is not exercised.

[Claim 29](../smoke/claims/a-wrong-bucket-is-refused.md#on-azure),
`az-records-in-a-storage-account`, writes records to a correct storage
account and requires four wrong accounts to be refused by name with no blob
written. Its `BREAK=1` removes the fences and waives all three settings,
and the same apply must go through naming each waiver.

Each draft's header says what floci-az cannot answer today. Against the
pinned image, claims 7 and 1 stop at "Microsoft.ResourceGraph not
registered", claim 13's main arm cannot see a deny, and claim 29 is refused
on `blob_versioning` against the correct account.

## The gauntlet lane

The `azure` lane ([#1120](https://github.com/INTENTIUS/choudoufu/issues/1120))
runs the same stages as the AWS lanes against floci-az, with a bar of its
own, `lanes.azure` in `live/gauntlet.json`. `live/GAUNTLET.md` says how each
stage reads on the floci-az substrate.

One estate is in it, `reference-azure`, a hand-written shape kept in this
repository (`live/e2e/reference-azure/`): a resource group, a VNet with a
subnet, a network security group with one rule, a storage account, a
user-assigned identity, a public IP, two `count` identities, and one
`azapi_resource` identity. It is built and has not been run. floci-az drops
tags on storage accounts, so the storage account fails every stage that
writes its marker until the fork keeps them, and a choudoufu plan's sweep
is refused for want of Resource Graph before that.

Claim 6 (stock in, stock out) has no Azure scenario. Its intended proof is
`reference-azure`'s `cold_deploy` and `migrate` stages.

## What it would cost

This is the shape, read from the code and Azure's documentation. No call
has been counted.

The sweep is one Resource Graph query per subscription the estate's
resources resolve to, paged, matching `tofu-estate` case-insensitively.
Like AWS's `GetResources` it is filtered on the marker server-side, so its
size follows the estate, not the subscription. Unlike Kubernetes it needs no
per-kind list. Asking one subscription per query costs a call per
subscription, and buys a named warning for one the credential cannot read,
which a query over several would leave out silently.

The read pass is one ARM GET per declared object, as it is for stock. An
after-create tag write adds one Tags API call for each of the two azurerm
types that need it.
