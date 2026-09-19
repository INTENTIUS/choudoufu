---
title: "Reading a value from another estate"
weight: 9
---

# Reading a value from another estate

An estate reads another estate's value from the **live resource**, with an
ordinary data source filtered by the producer's marker tags. Nothing in the
record store is involved, and no run ever reads another estate's objects in
the bucket.

```hcl
data "aws_vpc" "network" {
  filter {
    name   = "tag:tofu-estate"
    values = ["network"]
  }
  filter {
    name   = "tag:tofu-address"
    values = ["aws_vpc.main"]
  }
}

resource "aws_subnet" "app" {
  vpc_id = data.aws_vpc.network.id
}
```

Both tags are already on the producer's resource, and the pair is unique in an
account by construction. The producer publishes nothing and does not know it
is being read.
[`live/OUTPUTS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/OUTPUTS.md)
is the decision, and
[`examples/cross-estate-dependency`](https://github.com/INTENTIUS/choudoufu/tree/main/examples/cross-estate-dependency)
runs it with two estates and an ordered pipeline.

## Why not an output

Stock passes values between roots with `output` and `terraform_remote_state`,
which reads the producer's state file. A live root has no state file of
record. A `terraform_remote_state` pointed at a file from before a migration
still resolves, and returns a snapshot frozen on the day of the migration with
nothing to say so.

A dedicated estate-output surface was considered and declined in
`live/OUTPUTS.md`: a copy of a value is a second thing to keep true, and the
live resource is already the authority.

## What `tofu-outputs/` is, then

Each estate does record its own root output values, under
`tofu-outputs/<estate>/` in its record store. They are there for one reason: a
plan has to be able to tell `~ name = old -> new` from `+ name = value`, and a
live root has no state file to remember the old value in. The estate that
wrote them reads them. An output marked `sensitive` is never written, and
neither is one whose value is not wholly known.

No choudoufu command reads another estate's `tofu-outputs/`. Two things could
make you think otherwise:

- The IAM policy renderer takes `--reads-outputs-of <estate>`, which grants a
  role read access to another estate's `tofu-outputs/` prefix and to nothing
  else of that estate's.
  [Claim 35]({{< relref "/docs/claims/one-bucket-many-estates" >}}) measures
  that the grant is exactly that wide. It exists for a reader you write
  yourself, such as a script or a dashboard. No run needs it.
- The bucket backend's design text describes a declared dependency on another
  estate's outputs as "the one read that crosses an estate boundary". Nothing
  implements that read. If it is built, what crosses will be bounded by what
  is written: non-sensitive, wholly known root outputs.

So the isolation between estates in one bucket has no exception in practice.
A role scoped to estate A is denied estate B's records by prefix and by tag,
and claim 35's role never needed anything of B's to run A.

## What this costs the consumer's role

The data source is an ordinary provider read, so the consumer's role needs the
producer's resource type's describe permission, account-wide or scoped however
that service allows. It needs nothing on the bucket beyond its own estate's
policy.

## Ordering

The data source reads what exists when the consumer plans. If the producer
has not applied yet, the read finds nothing and the consumer's plan fails with
the data source's own error, which is the right outcome and an unhelpful
message. Order the two in the pipeline, producer first, as the example does.
