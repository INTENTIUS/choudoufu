# Adopt

Everything on this page is built and none of it has been run against an
emulator or a real subscription. Each claim's Azure cell reads `open` in
[`live/CLAIMS.md`](../CLAIMS.md), and the
[proof page](PROOF.md) says what would close each one.

## Today

An Azure object is yours once it carries the `tofu-estate` tag naming your
estate. The tags are the AWS ones, `tofu-estate`, `tofu-address` with its
continuations, and `tofu-slot` on a `count` member, written lowercase into
the `tags` argument both providers already accept (ruled on
[#1122](https://github.com/INTENTIUS/choudoufu/issues/1122)). ARM treats tag
keys as case-insensitive, so every read folds case: a configuration that
writes `Tofu-Estate` itself is checked as the marker, and when its value is
right the stamp keeps that spelling and adds no second key, since ARM
refuses two keys that differ only by case.

hashicorp/azurerm and azure/azapi are one family, `substrate.Azure`
(`internal/live/substrate/azure.go`). One object declared through both is the
ordinary `duplicate-identity` refusal, compared case-insensitively.

## How a block binds

Identity is the object's ARM resource id, built from the block before any
call. There are three routes.

An azurerm type with an id template reads it from
[`live/azurerm-ids.json`](../azurerm-ids.json), which `tools/azurerm-id-gen`
generates from azurerm 5.10.0's own id parsers. The template fills in
`name`, `resource_group_name` and the parents' names or ids. The
subscription comes from the provider block's `subscription_id`, the way an
AWS region comes from the provider block. A subscription the block does not
state statically (`ARM_SUBSCRIPTION_ID`, or a variable with no default) is
handled as an AWS account the run is not told: the identity needs
discovery, and the plan says why.

azurerm serves a resource identity schema for 196 of its 1,106 types at
5.10.0, subnets and routes among them, and those keep the identity-schema
route, with `subscription_id` from the provider block. The route admits 103
of them on the schema alone. Another 72 are admitted through the generated
template, which a guard first holds to the identity attributes azurerm's
own identity tests read. The other 21 are refused by name: 19 have no
template, and 2 have a template the guard withholds. Each type's route is
recorded in `live/azurerm-ids.json`.

An `azapi_resource` binds by `type`, `parent_id` and `name`, the natural key
inside its own arguments, as `kubernetes_manifest` does. The id is the one
azapi's own builder makes (`internal/live/identity/armid.go` cites it line by
line), and the API version after the `@` takes no part in it.

What admission refuses is in [Compatibility](COMPATIBILITY.md).

## The marker on create

The stamp writes the tags into the create call. For azurerm that is the
`tags` argument. For `azapi_resource` it is the top-level `tags` argument,
or `body.tags` when the configuration already puts its tags in the body,
because azapi refuses a configuration that sets both
(`armTagTarget` in `internal/live/projection/nodestamp_armtags.go`). Either
way the read-back is the top-level `tags` attribute, which azapi fills from
the response.

An azapi type is stamped only where azapi's own type data says the ARM type
takes tags at the API version the block names. The table is
`internal/live/armtypes`, keyed by type and version: 1,031 of 3,397 ARM
types take tags at their latest version, and 84 change between versions. An
untaggable or unknown one is left unstamped with a warning, never given a
marker ARM would refuse.

Two azurerm types cannot carry tags on their create call:
`azurerm_app_service_managed_certificate` and `azurerm_subscription`. They
are created unmarked and tagged straight afterwards through ARM's Tags API
with the provider block's credential, and a failed write fails the apply
naming the object and the `az tag update` line that marks it.

## From a stock state file

```
choudoufu live-import -approve
```

The bulk path is the AWS one ([#1073](https://github.com/INTENTIUS/choudoufu/issues/1073)).
The stock state is read once, each object is verified at its ARM id, and
`tofu-estate` and `tofu-address` go on as tags through a tags-only plan and
apply. On azapi the write goes where the configuration keeps its tags, as
on create. A key the object already carries as `Tofu-Estate` is rewritten
under that spelling. An object tagged for another estate is refused.

If the stock state lives in the `azurerm` backend, `tofu state pull >
stock.tfstate` gives `live-import` its file. Keep that blob until you trust
the migration, since it is the way back to stock.

## An unmarked object at a declared id

This is where Azure differs most from AWS. An ARM create is a PUT, and a
PUT to an id that already exists updates the object there instead of
failing. So a declared object found at its id without this estate's
`tofu-estate` tag is refused by name before any create: "Unmarked live
object at the declared id would be overwritten". On AWS the same plan
proposes a second resource beside it; on Kubernetes the API server answers
409.

The refusal holds under `declared_untagged = "refuse"` (the default),
`"keep"` and `"report"`, and lint's `policy-overwrite-at-key` rule refuses
`"keep"` and `"report"` for any configuration with an azurerm or azapi
resource. `"adopt"` and `"converge"` admit the object before the refusal is
checked, and the plan writes the markers onto it. To take the object over,
set `policy { declared_untagged = "adopt" }`, or write the two tags onto it
by hand. Both are in
[`live/LIMITATIONS.md`](../LIMITATIONS.md#unmarked-live-object-at-the-declared-id-would-be-overwritten).

## Untaggable types

Of azurerm 5.10.0's 1,106 types, 387 have an ARM tags map and 719 do not.
An untaggable type whose id the declaration supplies binds from the
configuration and carries no marker, the declaration-carried tier. A
removed block of one is found again through its record, not through the
sweep. [`live/READINESS.md`](../READINESS.md#azure) has every count and
every type short of in-contract, and
[`live/IDENTITY.md`](../IDENTITY.md#on-azure) has the rule.
