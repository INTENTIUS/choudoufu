---
title: Records
description: Where choudoufu keeps what a run must remember.
prompt: >-
  Read https://intentius.io/choudoufu/how-it-works/values/ and list the
  record-backed resources here. Change no files.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

Each managed instance has one record. It holds what a run must remember
beyond ownership.

![Record kinds and stores](../../../assets/diagram-values.svg)

## Two kinds

| Kind | For | Losing it costs |
|---|---|---|
| `identity` | A resource with a live object. Holds what a read cannot return | A slower plan |
| `object` | A resource with no live object: `null_resource`, `random_*`, `time_*`, `tls_*`. Holds the whole value | The resource |

Losing a `random_pet` record renames everything built from it.
[Recover an estate](../../docs/use/recover-an-estate/) has the procedure.

## Three stores

| Store | Records are | For |
|---|---|---|
| `local` | Files in `.tofu-records` | One operator (default) |
| `s3` | Objects in your bucket | A team on AWS |
| `kubernetes` | Secrets in your namespace | A team on a cluster |

```hcl
record_store "s3" {
  bucket = "my-records-bucket"
}
```

A write succeeds only if the record is unchanged since it was read. No lock is
held. Records carry their estate's tag, so one bucket can serve many estates.

## It holds secrets

By default the store holds generated passwords in clear. Decide who may read
it first.
[`live/SECRETS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/SECRETS.md)
has `secrets = "refuse"`.
[`live/STORAGE.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/STORAGE.md)
has each store's layout.
