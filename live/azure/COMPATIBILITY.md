# Compatibility

**Layout.** `live/COMPATIBILITY.md` is the substrate-neutral reference;
this page covers the same sections for Azure only (ruled on
[#1602](https://github.com/INTENTIUS/choudoufu/issues/1602)). A section with
nothing Azure-specific to add says so in one line.

```
choudoufu live-check
```

`live-check` needs the providers' schemas for an Azure root. A schemaless
lint run refuses every azurerm type as `unadmitted-type`, because no
azurerm type has a row in the AWS admission table; the Azure rules read
the schema.

## Your provider

hashicorp/azurerm and azure/azapi, stock, as published at the pins this
lane is built against: azurerm 5.10.0 and azapi 2.13.0. No fork, no
wrapper, and no resource the configuration did not declare
([#1122](https://github.com/INTENTIUS/choudoufu/issues/1122)). The two are
one family, `substrate.Azure`, chosen by provider name.

Of azapi's four resource types only `azapi_resource` is admitted.
`azapi_update_resource` and `azapi_resource_action` act on an object they
do not create, and `azapi_data_plane_resource` has no tags and no ARM id,
so none of them owns the object its arguments name. The test is the
schema's shape (`substrate.AzapiResourceShape`), not the type name.

An azurerm importer's 404 is read as absence, not failure, in both forms
the provider renders it (`internal/live/absent`). azapi answers a missing
object with a null import and needs nothing added.

## Your resource types

Every azurerm type whose ARM id template could be read from azurerm's own
id parsers is admitted ([`live/azurerm-ids.json`](../azurerm-ids.json)),
and so is every ARM type declared through `azapi_resource`.
[`live/READINESS.md`](../READINESS.md#azure) generates the counts: of
azurerm's 1,106 types, 843 are in contract, and all 3,397 of azapi's ARM
types are.

### Readiness tiers

The tiers are AWS's. For azurerm at 5.10.0, 387 types have an ARM tags map
and are marker-carried, 489 untaggable types bind from their declaration,
and 230 untaggable types nothing admits are record-carried. For azapi at
2.13.0, 1,031 ARM types take tags at their latest API version and 2,366 do
not; 84 change between versions, and the stamp reads the version the
block's `type` names.

### Refused

The 263 azurerm types short of in-contract, each listed with its reason in
`live/READINESS.md`:

| Why | What happens |
|---|---|
| No id template could be read from azurerm's parsers (235 types) | Refused as `unadmitted-type`. No template is guessed from the schema |
| The template names the parent or is not the object's own or fails a guard (9 types) | Withheld rather than risk a wrong marker |
| On the identity-schema route with no template for an attribute no argument supplies (19 types) | Refused as unadmitted |

On `azapi_resource`, three things stop the stamp:

| Written like this | What happens |
|---|---|
| A `body` not known at plan time | Refused: the stamp cannot tell whether the tags go in `body.tags` or the top-level argument, and azapi refuses both |
| A `type` whose ARM type and version do not take tags, or are unknown | A warning; no marker is written |
| A `body` or `body.tags` the markers cannot be merged into | A warning; the value is left as evaluated |

`unadmitted-type` is in [`live/LIMITATIONS.md`](../LIMITATIONS.md#unadmitted-type),
and the azapi summaries are under "Every refusal, enumerated" there.

## How your configuration is written

### Expansion

No Azure-specific case. `count` and `for_each` expand before any identity
rule runs, as for an `aws_*` resource.

### `for_each` keys

As on AWS. The address goes in the `tofu-address` tag through the same
escaping, and an address longer than one tag value continues into numbered
tags, one for each 256 characters. A `count` member also carries
`tofu-slot`. The markers take tag slots: lint's `arm-tag-limit` warns when
the configuration's own tags plus the markers exceed what the type takes,
which is 15 on a Cosmos DB or storage account and 10 on API Management.

### Identity arguments

An azurerm id is a template over the block's `name`, its
`resource_group_name`, and its parents' names or ids. An `azapi_resource`
id is `parent_id`, a segment derived from `type`, and `name`. The
subscription is the provider block's `subscription_id`. A value set from
`ARM_SUBSCRIPTION_ID` or a variable with no default is not known before
the run, and the identity needs discovery, as an AWS account the run is not
told does.

Seven admitted untaggable azurerm types mint a GUID when their naming
argument is left out: `azurerm_management_group`,
`azurerm_role_definition` and five more. An instance that leaves it out is
held by its record, the way an AWS `name_prefix` instance is. azapi refuses
a block with no `name` itself.

Every comparison of ids is case-insensitive, because ARM's is.

## Your modules

No Azure-specific case. `live/COMPATIBILITY.md`'s "Your modules" applies to
a module holding azurerm or azapi resources as written.

### Crossing a module boundary

As on AWS: `live-mv` rewrites the `tofu-address` tag in place, under the
spelling the object already uses for the key.

## Your accounts and regions

One provider block per subscription, resources pinned with the `provider`
meta-argument. The sweep runs one Resource Graph query per subscription
the estate's resources resolve to, each with the credential of the provider
block that names it. A declared id in a subscription no provider block
names is warned by name: the sweep there is incomplete, not empty. Add a
provider block, an alias will do, whose `subscription_id` is that
subscription.

Location is not part of an ARM id, so it never decides identity.

An estate is a set of resource groups
([Gate](GATE.md)). A declared resource in a group the configuration does
not declare is warned once per group.

### Mixed estates

An AKS cluster beside the `kubernetes` provider is the common one. AKS
creates a node resource group, sets its `managedBy` to the cluster, and
copies the cluster's tags onto it, the estate's marker included. The sweep
reports that group and everything carrying an `aks-managed-` tag as
held, and proposes destroying none of them. A `managedBy` naming anything
but an AKS cluster or a managed application, such as an attached disk's
virtual machine, is not a hold. The cluster's own objects are
the Kubernetes leg's, swept by label; the two fences are judged under their
own credentials, as for EKS in `live/kubernetes/COMPATIBILITY.md`.

## How you run it

The command-level refusals are unchanged; `live/COMPATIBILITY.md`'s "How
you run it" applies as written.

What is Azure's own is what a plan finds at a declared id. An object there
without this estate's `tofu-estate` tag is refused by name, "Unmarked live
object at the declared id would be overwritten", because an ARM create is
a PUT that would update it in place. `declared_untagged = "keep"` or
`"report"` would leave that create standing, so lint's
`policy-overwrite-at-key` refuses both verbs in any configuration with an
azurerm or azapi resource. `"adopt"` and `"converge"` admit the object
before the refusal is checked, and take it over with the markers written in
the plan.

A plan also stops if a subscription has not registered
`Microsoft.ResourceGraph`, or if Resource Graph throttles past its retry
budget. Both are named in [`live/LIMITATIONS.md`](../LIMITATIONS.md#microsoftresourcegraph-not-registered).

## Constructs this page used to refuse, and no longer does

Before [#1120](https://github.com/INTENTIUS/choudoufu/issues/1120) neither
provider had a family of its own. An azurerm type or `azapi_resource` that
publishes a resource identity schema fell through to the AWS
identity-schema route, and every other azurerm type was refused as
`unadmitted-type`. Nothing else on `live/COMPATIBILITY.md`'s list for this
section has an Azure history.

## Effects do work

No Azure-specific case. `random_*`, `time_*` and `terraform_data` are
record-backed on every substrate.

## Two hazards that are now refusals

`lifecycle { ignore_changes = ... }` over the tags is refused as it is on
AWS: `all`, `[tags]`, and any entry naming a marker key. On Azure the key
is matched case-insensitively, so `tags["Tofu-Estate"]` is refused as
`tags["tofu-estate"]` is. Ignoring a tag key of your own stays admitted.

The stateful un-migration guard (#613) reads the ARM tags surface too, so a
state-backed plan that drops `tofu-estate` from an Azure resource is
refused the same way.

The module-alias hazard is OpenTofu-level and applies as written.

## Editors and linters

No Azure-specific case. `live/COMPATIBILITY.md`'s "Editors and linters"
applies to an Azure root as written.
