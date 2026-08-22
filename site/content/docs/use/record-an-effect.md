---
title: "How to record an effect the cloud cannot report"
weight: 8
---

# How to record an effect the cloud cannot report

Nothing in the live system records that a database migration, a script, or a
one-shot API call happened, so no marker reads back.

`null_resource`, `terraform_data`, `time_*` and non-secret `random_*` work once
the live configuration declares a `record_store`.

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
