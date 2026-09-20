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

Stock passes values between roots with `terraform_remote_state`, which reads
the producer's state file. A live root has no state file of record, and a
file left over from before a migration still resolves, returning a snapshot
frozen on that day with nothing to say so. The live resource is already the
authority, so that is what the consumer reads.

Each estate does record its own root outputs, under `tofu-outputs/<estate>/`,
so that its own plan can show a change as a change. No choudoufu command reads
another estate's.

## What it needs

The consumer's role needs permission to describe the producer's resource
type, and nothing on the record store beyond its own estate's policy.

The data source reads what exists when the consumer plans. If the producer
has not applied yet, the plan fails with the data source's own error. Order
the two in the pipeline, producer first, as the example does.
