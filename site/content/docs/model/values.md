---
title: "Records"
weight: 3
---

# Records

Ownership lives on the resource, as a marker. Everything else a run has to
remember between applies lives in the record store: one record for every
managed resource instance, written by choudoufu, in a place you choose.

![Every managed instance has a record, of one of two kinds, in one of three stores](diagram-values.svg)

## Two kinds of record

| `kind` | Written for | Holds | Losing it costs |
|---|---|---|---|
| `identity` | A resource with a live twin, which is most of them | What a read of the live object cannot give back: an import identity, arguments the API never returns, whether a create-time provisioner ran, and on Kubernetes the label and annotation keys the configuration last declared | A slower or noisier plan. Ownership is the marker and does not depend on the record |
| `object` | A resource with no live twin: `null_resource`, `terraform_data`, `random_*`, `time_*`, `tls_*` | The whole value, the provider's private data, and which attributes were sensitive | The resource. The record is the only copy |

The `kind` inside the record decides what a plan may do with a record that no
configuration declares. Only an `object` record is proposed for destroy.

A record is never consulted for ownership, and its values are never folded
into a marker: an identity-bearing argument is evaluated over `var`, `local`,
`path`, `terraform` and `tofu` alone. A lost `object` record can still cost
more than itself. `name = "svc-${random_pet.suffix.id}"` is the ordinary
shape, and losing the pet's record regenerates the pet, so everything named
after it is proposed for create under a new name.
[Recover an estate]({{< relref "/docs/use/recover-an-estate" >}}) has the
procedure.

## Three stores

| Backend | Records are | For |
|---|---|---|
| `local` | Files in `.tofu-records` beside the module | One operator, or a demo. It is what an estate gets when it declares nothing |
| `s3` | Objects in a bucket you own | A team on AWS |
| `kubernetes` | Secrets in a namespace you own | A team on a cluster, with no AWS account involved |

```hcl
# estate.chdf.hcl
estate = "my-estate"

record_store "s3" {
  bucket = "my-records-bucket"
}
```

All three give the same guarantees, and a run cannot tell them apart.

Every write is conditional. A create succeeds only if the record does not
exist, and an update or a delete only if the record still has the version the
writer read. A losing writer gets a named conflict and changes nothing.
Nothing is locked, so a crashed run leaves nothing held.
[Two runs at once]({{< relref "/docs/model/concurrency" >}}) has the cases.

A run reads its estate's records whole before it plans, and the read is
complete or the run fails. A store that errors partway never reaches the plan
as a smaller estate.

Each record is its own object, tagged or labelled with the estate's name the
way a managed resource is. One bucket or one cluster serves any number of
estates, and your IAM or RBAC decides who reads which.

## It holds secrets

A record store holds what a state file would have held. Under the default,
`strict { secrets = "store" }`, a generated password is recorded in clear,
and so is a database password the API never returns. Whoever can read the
store can read them, so pick who that is before you pick a backend.
[Secrets in the record store]({{< relref "/docs/use/secrets" >}}) says exactly
what is recorded and what `secrets = "refuse"` changes.

[Where things are stored]({{< relref "/docs/use/storage" >}}) has the layout
of each store, and [What you set up by hand]({{< relref "/docs/use/setup" >}})
has what must exist before the first plan.
