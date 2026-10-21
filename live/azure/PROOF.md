# Proof

The `azure` lane ([#1120](https://github.com/INTENTIUS/choudoufu/issues/1120))
runs the gauntlet's stages against floci-az, with a bar of its own,
`lanes.azure` in `live/gauntlet.json`. Both of its estates clear all 15
stages, measured at `01d69a5a75` on 2026-10-21
([the board](../BOARD.md)):

| Estate | Source |
|---|---|
| [`reference-azure`](../BOARD.md#reference-azure) | a hand-written shape kept in this repository (`live/e2e/reference-azure/`): a resource group, a VNet with a subnet, a network security group with one rule, a storage account, a user-assigned identity, a public IP, two `count` identities, and one `azapi_resource` identity |
| [`corpus-azapi-role-assignment`](../BOARD.md#corpus-azapi-role-assignment) | the azapi provider's own published role-assignment example (Azure/terraform-provider-azapi, `v2.13.0`): a resource group through `azapi_resource`, a user-assigned identity, and an `azapi_resource` role assignment at the group's scope |

The stages are cold deploy, migrate, replan from nothing, no-op apply,
drift and reconverge, rename, remove a block, change count, replace with
`create_before_destroy`, crash mid-apply, teardown, plan-review-apply,
greenfield apply, the strict profile, and plan with no local state.
[`live/GAUNTLET.md`](../GAUNTLET.md) says what each one proves and how it
reads on floci-az. The migrate stage is
[Adopt](ADOPT.md#from-a-stock-state-file)'s `live-import` path, run against
each estate's stock state, and the records live in `record_store "azurerm"`
on floci-az blob storage.
