---
title: What you set up by hand
description: What must exist before the first plan.
prompt: >-
  Follow https://intentius.io/choudoufu/docs/use/setup/ for this repository
  and tell me what is missing. Create nothing in the cloud.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

| Piece | Who creates it |
|---|---|
| Credentials and an access policy | You, before the first plan |
| `estate.chdf.hcl` | You, one line |
| A local record store | The first run |
| A shared record store, a bucket or a namespace | You, before the first plan |
| Markers on resources choudoufu creates | The apply |
| Markers on resources that already exist | You, by [migrating](../migrate/) |

## Credentials and region

Credentials come from the usual SDK chain. A live plan reads widely, so set
`AWS_PROFILE` on purpose. A missing region or credential fails with
`Error: Provider unavailable for marker discovery`.

Pin `required_providers` to the `provider_version` in `live/survey.json` for a
quiet first plan. Other versions print a warning.

## The estate declaration

```hcl
# estate.chdf.hcl
estate = "my-estate"
```

`init` refuses two mistakes.

| Mistake | What prints |
|---|---|
| A `backend` or `cloud` block beside it | `Error: Both a backend and a live configuration are present` |
| Both the sidecar and a `live` block | `Error: Both a live sidecar file and a live block are present` |

## The record store

| Store | What must exist first |
|---|---|
| `local` | Nothing. Gitignore `.tofu-records/`, because records hold secrets |
| `s3` | The bucket, with versioning, a lifecycle rule that expires noncurrent versions, and public-access block |
| `kubernetes` | The namespace, and a role bound to Secrets in it |
| `azurerm` | The storage account and container, with versioning, a lifecycle rule that deletes previous versions, and public access off |

Create the bucket or namespace outside the estate that uses it. The plan opens
the store first, so a store the estate declares fails with `NoSuchBucket`.

[Set up a record store bucket](../bucket/) has the commands. A Kubernetes
namespace defaults to `tofu-records-<estate>`.

## Permissions

A read-only role can run a plan once a writing role has opened the store. A
store no run has opened is refused by name.

A role scoped to fewer services prints an `Incomplete sweep` warning per type
it cannot list.

Adoption needs the service's tagging action, such as `ec2:CreateTags`.
[`live/IAM-REFERENCE.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/IAM-REFERENCE.md)
lists the actions per stage, store and service.
