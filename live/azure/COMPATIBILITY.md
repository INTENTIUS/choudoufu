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
azurerm's 1,106 types, 849 are in contract, and all 3,397 of azapi's ARM
types are.

### Readiness tiers

The tiers are AWS's. For azurerm at 5.10.0, 387 types have an ARM tags map
and are marker-carried, 492 untaggable types bind from their declaration,
and 227 untaggable types nothing admits are record-carried. For azapi at
2.13.0, 1,031 ARM types take tags at their latest API version and 2,366 do
not; 84 change between versions, and the stamp reads the version the
block's `type` names.

### Refused

The 257 azurerm types short of in-contract, each listed with its reason in
`live/READINESS.md`:

| Why | What happens |
|---|---|
| No id template could be read from azurerm's parsers (235 types) | Refused as `unadmitted-type`. No template is guessed from the schema |
| The template names the parent or is not the object's own or fails a guard (9 types) | Withheld rather than risk a wrong marker |
| On the identity-schema route with no template for an attribute no argument supplies (13 types) | Refused as unadmitted |

On `azapi_resource`, four things stop the stamp:

| Written like this | What happens |
|---|---|
| A `body` not known at plan time, and no known top-level `tags` | Refused: the stamp cannot tell whether the tags go in `body.tags` or the top-level argument, and azapi refuses both. A known top-level `tags` settles it: azapi sends that, or refuses a body that also carries tags |
| A `type` whose ARM type and version do not take tags, or are unknown | A warning; no marker is written |
| A `body` or `body.tags` the markers cannot be merged into | A warning; the value is left as evaluated |
| No `tags` of its own, under a provider block whose `default_tags` cannot be read before the run | A warning; no marker is written, because a marker alone would replace the default tags azapi sends |

azapi sends its provider block's `default_tags` only when the resource's
own `tags` is null and its body carries none, and never merges the two
(`tagsWithDefaultTags`, azapi 2.13.0 `internal/services/azapi_resource.go`
lines 1555-1590). So for such a resource the stamp writes the default tags
with the markers added, and drops a default tag under a marker key in any
case. A resource that sets its own `tags`, at top level or in `body`, gets
no default tags from azapi and is stamped as written. azurerm 5.10.0 has no
`default_tags`.

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
subscription is the provider block's `subscription_id`, or
`ARM_SUBSCRIPTION_ID` when the block sets none, the precedence both
providers apply (azurerm 5.10.0 `internal/provider/provider.go:118`, azapi
2.13.0 `internal/provider/provider.go:364-368`). The sweep reads the
environment through the same function, so the two agree on it. A block
value set from a variable is read with the value the run is given: `-var`,
`-var-file`, `terraform.tfvars`, `*.auto.tfvars` and `TF_VAR_` all reach
the evaluator before the plan (`Meta.rootModuleCall`,
`internal/command/meta_config.go:108`), as they do for an AWS region. Only
a variable given no value at all, with input disabled, leaves it unknown.
The environment does not stand in for it then, because the provider would
not let it: the identity needs discovery, as an AWS account the run is not
told does. The Azure CLI's default subscription, which both providers fall back
to last, is not read.

Seven admitted untaggable azurerm types mint a GUID when their naming
argument is left out: `azurerm_management_group`,
`azurerm_role_definition` and five more. An instance that leaves it out is
held by its record, the way an AWS `name_prefix` instance is. An
`azapi_resource` with no `name` is named by its own provider block's
`default_name` (azapi 2.13.0 `nameWithDefaultNaming`), read as that
block's `subscription_id` is; with none, azapi refuses the block itself
([#2142](https://github.com/INTENTIUS/choudoufu/issues/2142)).

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

AKS also copies the cluster's tags over those of the existing resources a
cluster is given: its route table, public IP, virtual network and the rest
([Microsoft's tag guide](https://learn.microsoft.com/en-us/azure/aks/use-tags)).
A declared route table then carries this estate's `tofu-estate` and the
cluster's `tofu-address`, or another estate's marker if that estate stamped
the cluster. The next plan refuses it by name, "Live object marked for
another owner at the declared id would be overwritten", because a create
there would be a PUT over it. Once the cluster is deleted, AKS removes what
it set, and the plan refuses the unmarked object instead.

`strict { markers "record" { types =
["azurerm_route_table"] } }` holds the type's identity in the record store,
with the subscription from the provider block, so the copied tags decide
nothing ([#2135](https://github.com/INTENTIUS/choudoufu/issues/2135)).
The same selection works for 191 of the 196 azurerm types with an identity
schema, including those whose identity names a parent the block states only
as the parent's id (`azurerm_storage_container`'s `storage_account_name`,
parsed out of `storage_account_id` by the id grammar azurerm parses it
with), a renamed argument or a constant (`azurerm_dns_cname_record`'s
`dns_zone_name` and `record_type`), as azurerm's own identity tests state
them; the generator reads those tests, so the agreement is checked within
one generator run rather than independently
([#2140](https://github.com/INTENTIUS/choudoufu/issues/2140)). A record is written only when it rebuilds into the object's own ARM id.
The other five are refused by name, with the attribute azurerm states no
configurable source for
([#2138](https://github.com/INTENTIUS/choudoufu/issues/2138)). Or
keep the resource out of the estate, or in a resource group no cluster is
given. Copying the estate's tags through the cluster's own `tags` does not
help: AKS writes one tag set, with the cluster's address, onto every
resource it uses.

## How you run it

The command-level refusals are unchanged; `live/COMPATIBILITY.md`'s "How
you run it" applies as written.

What is Azure's own is what a plan finds at a declared id. An object there
without this estate's `tofu-estate` tag is refused by name, "Unmarked live
object at the declared id would be overwritten", because an ARM create is
a PUT that would update it in place. An object carrying another estate's
marker, or this estate's for another address, is refused the same way under
"Live object marked for another owner at the declared id would be
overwritten", naming the marker it found. On AWS and Kubernetes those two
are warnings beside the plan. Only an object carrying this block's own
marker is admitted.

Both providers guard the same case at apply, and the refusal says which
one the resource is under:

| Provider | Its own check before the create PUT | Why the plan still refuses |
|---|---|---|
| azurerm 5.10.0 | Reads the id and fails with "a resource with the ID ... already exists" (`helpers/tf/errors.go:9-12`; e.g. `internal/services/resource/resource_group_resource.go:76-87`), unless `features { skip_import_check_on_create_and_allow_overwriting_existing_resources = true }` (`internal/provider/features.go:26-31`, 544-546). 61 of the 1,086 resource files do no such read, `azurerm_container_app_environment_certificate` among the taggable ones | Earlier: nothing is applied, where azurerm's error comes after the resources ordered before this one were created. Under the flag or such a type it is the only guard |
| azapi 2.13.0 (`azapi_resource`) | Reads the id before every create and fails with "Resource already exists" (`internal/services/azapi_resource.go:873-893`). No provider feature or `ignore_*` argument turns it off | Earlier, for the same reason. Never the only guard |

`azapi_resource` with its tags in `body.tags` reads back the same way as
with the top-level argument: azapi puts `body.tags` into the top-level
`tags` attribute at plan (lines 724 and 1555-1568), after create and update
(line 1103), on refresh (1182-1184) and on import (1419-1421), so the marker
is read from top-level `tags` in both cases. `declared_untagged = "keep"` or
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
