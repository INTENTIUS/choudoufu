---
title: "Values"
weight: 3
---

# Values

Most resources need nothing here. A resource with a cloud twin recovers its
values by reading the live object, the same way it recovers its identity.

The exceptions are resources with no twin at all. Nothing in AWS knows a
`null_resource` ran a script, a `time_static` captured a timestamp, or a
`random_pet` generated a name.

![Which resources need a record store, and which do not](diagram-values.svg)

Those persist as one small record each. Declare a store and they are admitted.
Without one they are refused.

```hcl
# estate.chdf.hcl
estate = "my-estate"

record_store "ssm" {}
```

The label picks the backend, one of `local`, `ssm` or `s3`.
[Where things are stored]({{< relref "/docs/use/storage" >}}) has the arguments.

## Four things to know

**You are not meant to read it.** The payload is a self-describing ctyjson
envelope for this fork's own code. Its format is not a contract.

**Writes are conditional.** A record is written only if it still holds the
version the writer read. A losing writer gets a named failure rather than a
blocking wait or a silent overwrite.

**Losing one is churn, not a lost estate.** The effect re-runs or its value
regenerates, and anything reading it plans as a change. It cannot cost you a
resource, because identity arguments must be statically evaluable, so a
record-backed value can never name one.

**Secrets never go here.** `random_password`, `random_bytes` and every `tls_*`
are refused rather than recorded. Their output is key material, and a record
holding a secret would be exactly the thing this design exists to avoid.

## Why this stays small

Identity is the part that has to be authoritative, and it lives on the
resources. What reaches a record store is a handful of values for resources the
cloud cannot see. Most estates never declare one.

A record store is also not where a receipt goes. A `key_prefix` starting with
`tofu-receipts` is a configuration error.
[Effects]({{< relref "/docs/model/effects" >}}) covers why.
