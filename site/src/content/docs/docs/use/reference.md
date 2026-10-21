---
title: Reference
description: The commands choudoufu adds and the live configuration's arguments.
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
| `live-check DIR` | What would be refused, offline |
| `live-import` | Bulk migration from a state file |
| `live-mv <old> <new>` | Rewrites a marker |
| `live-plan` | The live plan; [`-filter`](https://github.com/INTENTIUS/choudoufu/blob/main/live/REPORT-FILTER.md) narrows it |
| `live-history <address>` | A record's versions, newest first |
| `live-ls -estate=<name>` | Lists what the account or cluster holds under an estate |
| `live-bucket -bucket <name>` | Checks a bucket |
| `live-cluster` | Checks a cluster record store and names each fix |
| `live-cone`, `plan -cone` | [The plan cone](https://github.com/INTENTIUS/choudoufu/blob/main/live/PLAN-CONE.md) |
| `force-unlock` | Refused: there is no lock |

## The live configuration

Write it in `estate.chdf.hcl` at the root or in a `live` block inside
`terraform`. Pick one. Stock tooling ignores the sidecar file. A `backend` or
`cloud` block beside it is an error.

```hcl
estate = "prod-networking"

record_store "s3" {
  bucket = "my-records-bucket"
}
```

`estate` is the `tofu-estate` marker value, a literal string. `reads =
"selective"` (default) lets a `-refresh=false` run serve unchanged instances
from the cache; `"full"` reads everything.

## Blocks

`record_store` takes the label `"local"`, `"s3"` or `"kubernetes"`.

| Argument | Store | Meaning |
|---|---|---|
| `path` | `local` | Records directory |
| `bucket`, `region`, `bucket_owner` | `s3` | The bucket, its region and owning account |
| `key_prefix` | `s3`, `kubernetes` | Replaces `tofu-records/<estate>/` |
| `namespace` | `kubernetes` | Defaults to `tofu-records-<estate>` |
| `host`, `config_path`, `exec` and the rest | `kubernetes` | As in stock's `kubernetes` backend |
| `control_plane` | `kubernetes` | `"eks"`, `"gke"` or `"aks"`, to check encryption at rest |
| `allow_insecure` | `s3`, `kubernetes` | Checks this estate waives, by name |

`policy` sets a verb for each declared or undeclared, tagged or untagged case,
such as `undeclared_tagged`. It also takes `tag_key`, `tag_value` and a delete
`threshold`. See
[`live/OWNERSHIP-POLICY.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/OWNERSHIP-POLICY.md).

`strict` holds toggles that default to stock behaviour: `marker_repair`,
`secrets`, `no_source_create` and `provider_change`. See
[`live/STRICT.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/STRICT.md).

## Specifications

[Markers](https://github.com/INTENTIUS/choudoufu/blob/main/live/MARKERS.md),
[refusals](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md),
[IAM permissions](https://github.com/INTENTIUS/choudoufu/blob/main/live/IAM-REFERENCE.md),
[receipts](https://github.com/INTENTIUS/choudoufu/blob/main/live/RECEIPTS.md),
[outputs](https://github.com/INTENTIUS/choudoufu/blob/main/live/OUTPUTS.md),
[end-to-end harness](https://github.com/INTENTIUS/choudoufu/blob/main/live/e2e/README.md).
