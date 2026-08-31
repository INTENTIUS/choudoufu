---
title: "How to record an effect the cloud cannot report"
weight: 8
---

# How to record an effect the cloud cannot report

Nothing in the live system records that a database migration, a script, or a
one-shot API call happened, so no marker reads back.

`null_resource`, `terraform_data`, `time_*` and `random_*` work as soon as the
configuration has a `live` block. It needs no `record_store` block: an estate
that names none gets an implied local store, a `.tofu-records` directory beside
the module. That includes the secret-generating `random_*`, admitted on the
same terms under the default `strict { secrets = "store" }` and recorded in
clear, which is the reason to read the storage page before choosing a backend.

Declare a `record_store` to put the records somewhere a team can share instead:

```hcl
# estate.chdf.hcl
estate = "my-estate"

record_store "ssm" {}
```

The label picks the backend, one of `local`, `ssm` or `s3`.
[Where things are stored]({{< relref "/docs/use/storage" >}}) has "Choosing a
record store backend" for which one to pick, what each holds, and why a
receipt must not go in there. Those resources then run the stock provider
lifecycle exactly as upstream.
