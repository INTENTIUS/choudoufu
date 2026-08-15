# Reference

The normative specifications live in the repository, next to the code and the
tests that hold them to it. This page is the index into them.

They are written for people integrating with choudoufu or working on it. If you
are trying to get an estate running, the path pages are what you want.

## Specifications

| Document | What it settles |
|---|---|
| [`live/MARKERS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/MARKERS.md) | The marker tag spec. Key names, the escaping rule, continuation tags, ownership semantics, the rename rule, and what protects the tags. The one surface external tooling can rely on. |
| [`live/LIMITATIONS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md) | Every construct the mode bounds or rejects, per rule, each with its lint rule and fixture. |
| [`live/RECEIPTS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/RECEIPTS.md) | Recording an effect that leaves nothing in the live system to read back, and the guards on the pattern. |
| [`live/OUTPUTS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/OUTPUTS.md) | Sharing values between estates with no remote state. |

## Coverage and evidence

| Document | What it settles |
|---|---|
| [`live/COVERAGE.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/COVERAGE.md) | Which AWS resource types are covered, in layers, and what each layer means. |
| [`live/SURVEY.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/SURVEY.md) | How admission is decided per type, the method, and the raw signals behind it. |

## The demo, which is also the test suite

[`live/e2e/README.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/e2e/README.md)
documents the harness, covering what each step proves, the environment knobs,
and what each exit code means.

```
bash live/e2e/run.sh --expect 5
```

## Commands

`choudoufu <command> -help` is authoritative for flags. These are the
live-specific commands.

| Command | What it does |
|---|---|
| `choudoufu plan` / `apply` | Ordinary plan and apply. With a `live` block present, these run against markers. |
| `choudoufu live-mv <old> <new>` | Rewrites the `tofu-address` tag. The replacement for `moved` blocks. |
| `choudoufu live-import` | Bulk migration. Reads an existing state file once, verifies each entry, stamps markers on what verifies. |
| `choudoufu live-plan` | The live plan, invoked directly. |

## The live configuration

Two places to write it, one dialect. The leading form is the sidecar file
`estate.chdf.hcl` at the configuration root. Its body is the live
configuration itself, and because the extension is not `.tf`, stock
OpenTofu, Terraform, fmt and linters never parse it.

```hcl
# estate.chdf.hcl
estate = "prod-networking"

record_store "ssm" {
  key_prefix = "tofu-records/prod-networking"
}
```

The same content may instead live in a `live` block inside the `terraform`
block. Both forms are fully supported, and both present at once is an error
naming the file and the block. A `backend` or `cloud` block alongside
either form is refused in the decoder, before any command runs.

### Arguments

| Argument | Meaning |
|---|---|
| `estate` | The estate this configuration owns, the value the `tofu-estate` marker carries. Deliberately a literal string, because a name assembled from variables could differ between plan and apply, and the estate name is an identity rather than a computed value. Optional. Omitted, the name derives from the markers this configuration stamps. |

`snapshots` and `snapshot_path` are tombstones. The observational-snapshot
subsystem they configured was removed, and setting either produces an
error naming what replaced it (guided discovery's hint now rides the
`record_store`).

### `record_store` block

One label picks the backend, `"local"`, `"ssm"`, or `"s3"`. It stores the
values of logical resources (`null_resource`, `terraform_data`, `time_*`,
non-secret `random_*`), and declaring it is also what admits those types.
Writes are conditional rather than locked. The trade-offs per backend
are in [Storage](storage.html).

| Argument | Applies to | Meaning |
|---|---|---|
| `path` | `local` | Directory for the records, relative to the module. |
| `bucket` | `s3` | The bucket holding the records. |
| `key_prefix` | `ssm`, `s3` | Namespace for this estate's records. A prefix whose first segment is `tofu-receipts` or `tofu-hints` is a decode error, because those namespaces belong to receipts (ordinary declared resources) and the guided-discovery hint respectively. |
| `region` | `ssm`, `s3` | Region of the store. Unset, the AWS SDK's own default-configuration chain decides. |

### `policy` block

The ownership matrix. One verb per quadrant of declared-or-not against
tagged-or-not, plus the marker key overrides and the delete guard. The
verbs, defaults, and the reasoning live in
[Day 2 operations](day2.html). These are the arguments.

| Argument | Meaning |
|---|---|
| `declared_tagged`, `declared_untagged`, `undeclared_tagged`, `undeclared_untagged` | The verb for each quadrant. |
| `tag_key`, `tag_value` | Override the marker tag names. |
| `threshold` | Guard for a delete quadrant. The run refuses when more resources than this would be deleted. The decoder accepts any non-negative whole number, and lint refuses zero. |

The `undeclared_untagged = "delete"` quadrant does account reconciliation, and
requires a nested `scope` block bounding what a sweep may touch, through
`services`, `types`, and `regions`, each a list. The other quadrants'
delete verbs (including `undeclared_tagged`'s, the default estate-scoped
sweep) need none.

## Everything else is OpenTofu

The language, the CLI, providers and backends are all unmodified. Use
[opentofu.org/docs](https://opentofu.org/docs/).
