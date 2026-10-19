---
title: What you set up by hand
description: What has to exist before the first plan, which choudoufu will not create for you.
prompt: >-
  Follow https://intentius.io/choudoufu/docs/use/setup/ for this repository
  and tell me what is missing. Create nothing in the cloud.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

Who makes each piece an estate needs:

| Piece | Who creates it |
|---|---|
| Credentials and an access policy | You, before the first plan |
| `estate.chdf.hcl` | You, one line |
| A local record store | The first run |
| A shared record store, a bucket or a namespace | You, before the first plan |
| Markers on resources choudoufu creates | The apply |
| Markers on resources that already exist | You, by [migrating](../migrate/) |

## Credentials and region

A `live` block does not change where credentials come from. On AWS that is the
SDK chain: environment variables, then `~/.aws`, then instance metadata.

A plan under the live backend reads far more of an account than a stock plan,
so set `AWS_PROFILE` on purpose before the first one. A missing region or
credential fails with `Error: Provider unavailable for marker discovery`.

Pin `required_providers` to the `provider_version` in `live/survey.json` for a
quiet first plan. Any other version warns and carries on.

## The estate declaration

```hcl
# estate.chdf.hcl
estate = "my-estate"
```

Until this file exists the binary behaves as stock OpenTofu. Two mistakes are
refused at `init`.

| Mistake | What prints |
|---|---|
| A `backend` or `cloud` block beside it | `Error: Both a backend and a live configuration are present` |
| Both the sidecar and a `live` block | `Error: Both a live sidecar file and a live block are present` |

## The record store

Every estate has one. Declaring none gets the local one.

| Store | What must exist first |
|---|---|
| `local` | Nothing. Gitignore `.tofu-records/`, because records hold secrets |
| `s3` | The bucket, with versioning, a lifecycle rule that expires noncurrent versions, and public-access block |
| `kubernetes` | The namespace, and a role bound to Secrets in it |

The estate that uses a store cannot declare it. The plan opens the store before
it could propose creating it, so the run fails with the same `NoSuchBucket` a
typo gives. Create the bucket or namespace outside the estate.

[Set up a record store bucket](../bucket/) has the commands and the policy. An
estate that runs only on Kubernetes needs no bucket and no AWS account: create
one namespace per estate, `tofu-records-<estate>` by default, and bind the
estate's role to Secrets in it and nothing wider.

## Permissions

A role that may only read can run a plan, record store included, once some
earlier run under a writing role has opened the store. A bucket no run has
opened is refused by name, because an unopened store looks the same as an
empty estate.

A plan reads widely: the sweep for resources whose block was deleted covers
every admitted type. A role scoped to the services you declare prints an
`Incomplete sweep` warning per type it could not list.

Adopting an existing resource needs that service's tagging action, such as
`ec2:CreateTags`.
[`live/IAM-REFERENCE.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/IAM-REFERENCE.md)
lists the actions per stage, per store and per service.
