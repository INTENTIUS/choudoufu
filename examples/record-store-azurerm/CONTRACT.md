# The three things a record store storage account must have

choudoufu asserts three settings about the storage account behind
`record_store "azurerm"` and refuses an account that fails one. They are the
[bucket's three](../record-store-bucket/CONTRACT.md), for the same reasons,
read from where Azure keeps them.

Both Azure estates, `reference-azure` and `corpus-azapi-role-assignment`,
clear all 15 gauntlet stages on floci-az `7f079c2` (2026-10-21, azure
`fbbc1c3bea`), with their records in `record_store "azurerm"` on floci-az
blob storage. Their accounts pass all three assertions.

| Setting | What is asserted | What it protects against |
|---|---|---|
| `blob_versioning` | `isVersioningEnabled` is true on the account's blob service | A record can be the only copy of what it says. A record-backed resource carries no marker and cannot be imported under a live block, so without versions an overwrite or a delete is final |
| `lifecycle_management` | Enabled rules for `blockBlob` delete **previous versions** under every prefix an estate writes, and no enabled rule deletes or archives **current** blobs there | With versioning on, every apply adds versions. Without this rule the account keeps all of them forever, and nobody has chosen how long a record destroyed by mistake stays recoverable |
| `public_access` | The account's `allowBlobPublicAccess` is false | Records hold secret material. While the account allows anonymous access, one container setting publishes them |

All three are Azure Resource Manager properties of the account, so they are
read through ARM: `Microsoft.Storage/storageAccounts/blobServices/read`,
`.../managementPolicies/read` and `Microsoft.Storage/storageAccounts/read`.
A store opened with only `access_key` or `sas_token`, and no Azure AD
credential beside it, cannot ask, and each setting is refused as unreadable
with that reason. Encryption at rest is not a fourth: Azure Storage encrypts
every account and it cannot be turned off, so the check could not fail.

## The lifecycle number is a recovery window

The days in the version-delete action are how long you have to notice that a
record was destroyed or overwritten by mistake. A deleted blob's previous
version stays until the rule deletes it, and promoting it back restores the
record. Choose the number as the answer to "how long until we would notice".

What counts, exactly:

- A rule must carry `actions.version.delete.daysAfterCreationGreaterThan`.
  One that only tiers blobs, or only deletes snapshots, does not count.
- `blobTypes` must include `blockBlob`, which is what a record is.
- A rule's `prefixMatch` starts with the **container name**:
  `records/tofu-records/` covers the records in container `records`. A rule
  with no `prefixMatch` covers every container in the account.
- A rule filtered by `blobIndexMatch` never counts.

And one thing refuses the account whatever else it has: an enabled rule
whose `baseBlob` action deletes current blobs, or moves them to the archive
tier, under the estate's prefixes. A converged estate does not rewrite its
records, so they age, and such a rule removes them on a timer; an archived
blob cannot be read until it is rehydrated, which takes hours. The refusal
is headed `The record store storage account's lifecycle policy removes
records`, and `allow_insecure` does not cover it.

A policy that passes, for an account whose records all live in container
`records`:

```json
{
  "rules": [
    {
      "name": "expire-previous-record-versions",
      "enabled": true,
      "type": "Lifecycle",
      "definition": {
        "filters": { "blobTypes": ["blockBlob"], "prefixMatch": ["records/"] },
        "actions": { "version": { "delete": { "daysAfterCreationGreaterThan": 30 } } }
      }
    }
  ]
}
```

```
az storage account management-policy create \
  --account-name <account> --resource-group <group> --policy @policy.json
```

## When it is checked

As for the bucket: on an estate's first contact with the container, before
any run that writes a record (an apply, a `live-mv` that is not a dry run, a
`live-import -approve`), and never on an ordinary plan. `choudoufu
live-bucket` reads S3 only for now.

## Waiving a setting, and what each waiver costs

```hcl
record_store "azurerm" {
  storage_account_name = "acmerecords"
  container_name       = "records"
  resource_group_name  = "records-rg"
  allow_insecure       = ["lifecycle_management"]
}
```

| Waived | The cost |
|---|---|
| `blob_versioning` | An overwritten or deleted record cannot be brought back, and a record can be the only copy of what it says |
| `lifecycle_management` | Nothing is known to delete previous versions: the account may keep every version of every record forever, and nobody has chosen how long a record destroyed by mistake stays recoverable. It does not waive a rule that removes records |
| `public_access` | Nothing is known to stop a container from being made readable anonymously, and the records hold secret material |

Every run that opens the store says so, by name and with the cost.

## The read fence

Each blob carries the estate as the blob index tag `tofu-estate`, written in
the same Put Blob as the record (`x-ms-tags`), so no blob exists untagged.
[`live/azure/record-store-abac.condition`](../../live/azure/record-store-abac.condition)
is a role assignment condition on that tag. It lets a principal read, delete
and read the tags of its own estate's blobs only, write only blobs that carry
its own estate's tag, and list only its estate's three prefixes, since a
listing touches no blob and cannot be conditioned on a tag. Replace `ESTATE`
with the estate name, and assign it on the container with a role that holds
the blob data actions, `tags/write` included (Put Blob with `x-ms-tags`
needs it):

```
az role assignment create --assignee <principal> \
  --role "Storage Blob Data Owner" \
  --scope /subscriptions/<sub>/resourceGroups/<group>/providers/Microsoft.Storage/storageAccounts/<account>/blobServices/default/containers/<container> \
  --condition "$(sed 's/ESTATE/<estate>/g' live/azure/record-store-abac.condition)" \
  --condition-version 2.0
```

What it does not cover:

- An access key or a SAS token is not an Azure AD principal, and no
  condition applies to it. The fence holds only with `use_azuread_auth =
  true` and `allowSharedKeyAccess` off on the account. Stock's default path
  lists the account key with your Azure AD login, which goes around it.
- It does not stop an overwrite of another estate's blob by a write that
  carries this estate's tag; the condition reads the request's tag, not the
  stored one.
- A `key_prefix` override moves the records prefix, and the list clause has
  to name the new one.

The template is written from Azure's ABAC documentation. floci-az stores a
role assignment's ABAC condition and evaluates none, so the estates' runs
exercise the store and not this fence.

## A correct account, stated without a project

A storage account is correct for a record store when blob versioning is on,
an enabled lifecycle rule deletes previous block blob versions under every
prefix an estate writes and no enabled rule deletes or archives current
blobs there, `allowBlobPublicAccess` is false, and the container exists
before the first plan.
