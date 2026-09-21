---
title: "Where things are stored"
weight: 8
---

# Where things are stored

| What | Where it lives | Who writes it | Losing it costs |
|---|---|---|---|
| Markers | On the resource: two tags on AWS, one label on Kubernetes | The apply | The resource goes invisible and the next plan proposes a duplicate |
| Records | The record store, one per managed resource | choudoufu | For most resources a slower plan. For a record-backed one, the resource |
| The cache | `.terraform/choudoufu-cache.tfstate`, on the machine that ran | choudoufu | A read |

Only the markers say what you own. [Records]({{< relref "/docs/model/values" >}})
says what a record is, and [the cache]({{< relref "/docs/model/cache" >}}) has
its own page.

## The record store

```hcl
# estate.chdf.hcl
estate = "my-estate"

record_store "s3" {
  bucket = "my-records-bucket"
}
```

| Backend | Where it writes | Arguments |
|---|---|---|
| `local` | A directory beside the module, `.tofu-records` by default | `path` |
| `s3` | A bucket you already own | `bucket` (required), `bucket_owner`, `key_prefix`, `region`, `allow_insecure` |
| `kubernetes` | Secrets in a namespace you already own | `namespace` (default `tofu-records-<estate>`), and the connection arguments of stock's `kubernetes` backend |

An estate that declares no `record_store` gets the local one. Use a bucket or
a cluster for anything more than one person shares. On a CI runner the local
store is empty on every run, so a record-backed resource is proposed for
create again.

A store proves itself before a plan trusts it: the first run writes a
sentinel and reads it back through the same listing a plan uses. A store that
cannot do that is refused by name, and never reads as an empty estate. A
role that may only read can still plan once the sentinel exists.

## The local store

One file per record under `.tofu-records`, readable only by you. Records hold
[secrets]({{< relref "/docs/use/secrets" >}}), so gitignore the directory
before the first run.

## The bucket

One bucket serves any number of estates.
[Set up a record store bucket]({{< relref "/docs/use/bucket" >}}) has the
creating and the policy. An estate writes under three prefixes and nowhere
else.

| Prefix | What is there |
|---|---|
| `tofu-records/<estate>/` | One object per managed resource, and a sentinel |
| `tofu-hints/<estate>/` | Where the last sweep found things |
| `tofu-outputs/<estate>/` | The last value of each root output, never a `sensitive` one |

Every write is conditional and nothing is locked.

## The cluster

A Kubernetes-only estate keeps its records here and needs no AWS account
([Kubernetes]({{< relref "/kubernetes" >}})). `record_store "kubernetes"`
writes each one as a Secret labelled with the estate, in
`tofu-records-<estate>` or the `namespace` you name, conditional on the
`resourceVersion` the writer read and with nothing held. Create the namespace
yourself, one per estate: RBAC cannot condition on a label.

[`live/STORAGE.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/STORAGE.md)
has the rest: the exact requests a run sends, what `destroy` leaves behind,
how a store that cannot be reached is handled, and why receipts are kept out
of the record store.
