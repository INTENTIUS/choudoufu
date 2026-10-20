# Gate

On AWS the marker is an authorization primitive: an IAM condition on
`aws:ResourceTag` fences reads and writes per resource. Azure RBAC has no
such condition. A role assignment names a scope and nothing about the
resource's tags, so the Azure fence is drawn around resource groups instead
(ruled on [#1122](https://github.com/INTENTIUS/choudoufu/issues/1122)).
This page says what that fence holds and what it does not.

None of it is measured. floci-az has no role assignments and evaluates no
Policy rule ([`live/FLOCI.md`](../FLOCI.md#azure-floci-az)), so every
statement here comes from Azure's documentation until the templates are
applied on real Azure.

## An estate is a set of resource groups

The estate's groups are the ones its configuration declares: its
`azurerm_resource_group` blocks and its `azapi_resource` blocks of type
`Microsoft.Resources/resourceGroups`. The `live` block has no per-platform
setting, so there is no other list. A declared resource whose
`resource_group_name`, `resource_group_id` or `parent_id` names any other
group gets the warning "Resource in a resource group this estate does not
declare", once per group (`internal/live/identity/azure_resourcegroup.go`).

## The two templates

The fence is two ARM templates an operator applies with `az` alone. Neither
is ever a resource in a plan.

```
az deployment sub create --location <region> \
  --template-file live/azure/estate-grant.json \
  --parameters estate=<estate> resourceGroups='["<rg-1>"]' principalId=<object-id>
az deployment sub create --location <region> \
  --template-file live/azure/estate-boundary.json \
  --parameters estate=<estate> resourceGroups='["<rg-1>"]'
```

[`estate-grant.json`](estate-grant.json) is the RBAC half. It assigns one
principal a role at each of the estate's groups, plus read at the
subscription for the plan's Resource Graph sweep. The default role is a
custom one per estate: Contributor's actions without deleting the group
itself, assignable only at the estate's groups. `role=Contributor` uses the
built-in role, which can delete the group.

[`estate-boundary.json`](estate-boundary.json) is the Policy half, assigned
at each of the estate's groups. One definition denies a create or update
whose `tofu-estate` tag is missing or names another estate. The other is a
`denyAction` on deleting a resource tagged with the estate.

`live/MARKERS.md`, "Granting an Azure estate", has both in full, with the
`taggedAfterCreate` parameter the two after-create types need.

## Handover

Handing an estate to another principal is the grant deployed for the
receiving principal and the old assignments removed. Nothing on the
resources changes.

Moving resources to another estate goes by whole groups. The Policy deny
refuses a write that changes `tofu-estate` to another name, and a resource
left in a group the receiving estate does not declare gets the warning
above. So splitting off a group means removing the old estate's
assignments there, retagging its resources with `live-mv -from-estate`,
and deploying both templates for the new estate at that group. That
sequence is read from the templates and has not been run.

## What it does not fence

The fence differs from the AWS one in four ways.

Reads are not fenced. The sweep reads the whole subscription, and the
grant's subscription-wide read role lets the principal read every resource
in it.

Policy is not per principal. Both definitions apply to every caller at the
group, the estate's own principal included. So the `denyAction` refuses the
estate's own deletes too, and removing a resource from the estate takes an
exemption on the delete guard for the duration of the change.

Policy's `Indexed` mode evaluates only types that support tags and
location. An untaggable child (a subnet, a route) gets the group's RBAC and
nothing from Policy, and deleting its parent removes it unseen.

Scope is the gap, more than tags. A type whose id lives at the subscription
or the tenant is outside every group, and the grant does not reach it.
[`live/READINESS.md`](../READINESS.md#azure) counts azurerm's types by
where they live: of the 387 marker-carried types, 375 sit in a resource
group.

## The record store's own fence

The records are blobs, and their read fence is separate: an ABAC role
assignment condition on the blob index tag `tofu-estate`,
[`record-store-abac.condition`](record-store-abac.condition). It binds
Azure AD principals only, so an account key or a SAS token goes around it.
[`examples/record-store-azurerm/CONTRACT.md`](../../examples/record-store-azurerm/CONTRACT.md)
says how to assign it and what it misses.
