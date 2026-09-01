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

Those persist as one small record each, and every estate already has somewhere
to put them. A `live` block that names no `record_store` gets an implied local
one, a `.tofu-records` directory beside the module, so these resources are
admitted with no `record_store` block present. What is still refused is a
configuration with no `live` block at all.

Declare the block to send the records somewhere a team can share instead:

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

**The record store may hold any value the state file would have held,
including secrets, unless you set `strict { secrets = "refuse" }`.** The
default is `strict { secrets = "store" }`, which keeps what a stock state file
keeps: `random_password`, `random_bytes` and the `tls_*` types are admitted
and their generated values are recorded in clear. That is the thing to weigh
when picking a backend, because it decides who ends up able to read them:
[what the store may contain]({{< relref "/docs/use/storage#what-the-store-may-contain-and-who-can-read-it" >}})
has the per-backend answer.

## Why this stays small

Identity is the part that has to be authoritative, and it lives on the
resources. What reaches a record store is a handful of values for resources the
cloud cannot see. Most estates never declare one.

A record store is also not where a receipt goes. A `key_prefix` starting with
`tofu-receipts` is a configuration error.
[Effects]({{< relref "/docs/model/effects" >}}) covers why.
