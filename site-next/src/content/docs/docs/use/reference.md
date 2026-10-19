---
title: Reference
description: The commands choudoufu adds and the arguments of the live configuration.
prompt: >-
  Use https://intentius.io/choudoufu/docs/use/reference/ to check this
  repository's `estate.chdf.hcl` or `live` block. List any argument that is
  misspelled, unknown, or set to a value the page does not name. Change no
  files.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

`choudoufu <command> -help` is authoritative for flags. The language, CLI,
providers and backends are otherwise OpenTofu's; see
[opentofu.org/docs](https://opentofu.org/docs/).

## Commands

| Command | What it does |
|---|---|
| `plan`, `apply` | Ordinary plan and apply; with a live configuration, against markers |
| `plan -adoption-only` | Which live resource each declared instance binds to, and the command that adopts it |
| `live-check DIR` | What in a configuration would be refused, with no cloud call |
| `live-import` | Bulk migration from a stock state file |
| `live-mv <old> <new>` | Rewrites a marker; replaces `moved` blocks |
| `live-plan` | The live plan, invoked directly; [`-filter`](https://github.com/INTENTIUS/choudoufu/blob/main/live/REPORT-FILTER.md) narrows its report |
| `live-history <address>` | A record's versions, newest first |
| `live-bucket -bucket <name>` | Checks a record store bucket's settings |
| `live-cone`, `plan -cone` | [The plan cone](https://github.com/INTENTIUS/choudoufu/blob/main/live/PLAN-CONE.md) |
| `force-unlock` | Refused: there is no lock |

## The live configuration

Write it in the sidecar `estate.chdf.hcl` at the configuration root, which
stock tooling never parses, or in a `live` block inside `terraform`. Both at
once is an error, and so is a `backend` or `cloud` block beside either.

```hcl
estate = "prod-networking"

record_store "s3" {
  bucket = "my-records-bucket"
}
```

| Argument | Meaning |
|---|---|
| `estate` | The value the `tofu-estate` marker carries. A literal string, not an expression |
| `reads` | `"selective"` (default) lets a `-refresh=false` run serve unchanged instances from the cache; `"full"` reads everything. Default plans read fully either way |

### `record_store`

One label picks the store: `"local"`, `"s3"` or `"kubernetes"`.

| Argument | Store | Meaning |
|---|---|---|
| `path` | `local` | Directory for the records |
| `bucket`, `region` | `s3` | The bucket and its region |
| `bucket_owner` | `s3` | The account that must own the bucket |
| `key_prefix` | `s3`, `kubernetes` | Replaces `tofu-records/<estate>/` |
| `namespace` | `kubernetes` | Defaults to `tofu-records-<estate>` |
| `host`, `config_path`, `exec` and the rest | `kubernetes` | Spelled as stock's `kubernetes` backend spells them |
| `control_plane` | `kubernetes` | `"eks"`, `"gke"` or `"aks"`, so encryption at rest can be checked |
| `allow_insecure` | `s3`, `kubernetes` | A list of the checks this estate waives, by name |

### `policy`

One verb for each of `declared_tagged`, `declared_untagged`,
`undeclared_tagged` and `undeclared_untagged`, plus `tag_key`, `tag_value` and
a delete `threshold`.
[`live/OWNERSHIP-POLICY.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/OWNERSHIP-POLICY.md)
has the verbs.

### `strict`

Toggles whose defaults match stock OpenTofu: `marker_repair`, `secrets`,
`no_source_create` and `provider_change`.
[`live/STRICT.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/STRICT.md)
has each value and what it refuses.

## Specifications

- [`live/MARKERS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/MARKERS.md), the marker spec
- [`live/LIMITATIONS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md), every refusal
- [`live/IAM-REFERENCE.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/IAM-REFERENCE.md), the permissions a run needs and the tagging verbs
- [`live/RECEIPTS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/RECEIPTS.md), receipts
- [`live/OUTPUTS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/OUTPUTS.md), sharing values between estates
- [`live/e2e/README.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/e2e/README.md), the end-to-end harness
