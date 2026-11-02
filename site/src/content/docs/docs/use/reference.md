---
title: Reference
description: The commands choudoufu adds and the live backend's arguments.
prompt: >-
  Use https://intentius.io/choudoufu/docs/use/reference/ to check this
  repository's `estate.chdf.hcl` or `live` block and list any unknown or
  misspelled argument or value. Change no files.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

`choudoufu <command> -help` lists every flag. Everything else follows
[OpenTofu](https://opentofu.org/docs/).

## Commands

| Command | What it does |
|---|---|
| `plan`, `apply` | Plan and apply against markers |
| `plan -adoption-only` | Each instance's live match and adopt command |
| `live-check DIR` | Checks a configuration, offline |
| `live-import` | Bulk migration from a state file |
| `live-mv <old> <new>` | Rewrites a marker |
| `live-plan` | The live plan; [`-filter`](https://github.com/INTENTIUS/choudoufu/blob/main/live/REPORT-FILTER.md) narrows it |
| `live-history <address>` | A record's versions, newest first |
| `live-graph DIR -json` | The estate's graph |
| `investigate`, `mcp` | Read-only questions, also over MCP |
| `live-ls -estate=<name>` | Lists an estate |
| `live-bucket -bucket <name>` | Checks a bucket |
| `live-cluster` | Checks a cluster record store |
| `live-cone`, `plan -cone` | [The plan cone](https://github.com/INTENTIUS/choudoufu/blob/main/live/PLAN-CONE.md) |

## The live backend

The live backend takes the place of the `backend` block. Write it in
`estate.chdf.hcl` at the root, or in a `live` block inside `terraform`. A
`backend` block left beside it is ignored with a warning. Unless it is
`local`, an apply writes the estate's non-sensitive outputs where it points.

```hcl
estate = "prod-networking"

record_store "s3" {
  bucket = "my-records-bucket"
}
```

`estate` is the `tofu-estate` marker value. `reads = "full"` reads every
instance live; the default `"selective"` serves unchanged instances from the
cache under `-refresh=false`.

## Blocks

`record_store` takes the label `"local"`, `"s3"`, `"kubernetes"`, `"gcs"` or `"azurerm"`.

| Argument | Store | Meaning |
|---|---|---|
| `path` | `local` | Records directory |
| `bucket`, `region`, `bucket_owner` | `s3` | The bucket, its region and owning account |
| `key_prefix` | `s3`, `kubernetes`, `gcs`, `azurerm` | Replaces `tofu-records/<estate>/` |
| `namespace` | `kubernetes` | Defaults to `tofu-records-<estate>` |
| `host`, `config_path`, `exec` and the rest | `kubernetes` | As in stock's `kubernetes` backend |
| `control_plane "eks"`, `"gke"` or `"aks"` block | `kubernetes` | The managed cluster whose encryption at rest is checked |
| `bucket`, `credentials`, `impersonate_service_account`, `kms_encryption_key` and the rest | `gcs` | As in stock's `gcs` backend |
| `storage_account_name`, `container_name`, `resource_group_name`, `use_azuread_auth` and the rest | `azurerm` | As in stock's `azurerm` backend |
| `allow_insecure` | `s3`, `kubernetes`, `gcs`, `azurerm` | Checks this estate waives, by name |

`policy` sets a verb per case, such as `undeclared_tagged`
([`live/OWNERSHIP-POLICY.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/OWNERSHIP-POLICY.md)).

`strict` holds `marker_repair`, `secrets`, `no_source_create` and
`provider_change`
([`live/STRICT.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/STRICT.md)).

## Specifications

[Markers](https://github.com/INTENTIUS/choudoufu/blob/main/live/MARKERS.md),
[refusals](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md),
[IAM permissions](https://github.com/INTENTIUS/choudoufu/blob/main/live/IAM-REFERENCE.md),
[outputs](https://github.com/INTENTIUS/choudoufu/blob/main/live/OUTPUTS.md).
What is verified, and how, is indexed in
[EVIDENCE.md](https://github.com/INTENTIUS/choudoufu/blob/main/EVIDENCE.md).
