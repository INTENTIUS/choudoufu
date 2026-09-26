---
title: "Reading a value from another estate"
weight: 9
---

# Reading a value from another estate

An estate reads another estate's value from the **live resource**, with an
ordinary data source filtered by the producer's marker tags.

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

Stock passes values with `terraform_remote_state`, which reads the
producer's state file. A live root has no state file of record, and one left
from before a migration returns a snapshot frozen on that day. The live
resource is the authority, so the consumer reads it.

## What it needs

The consumer's role needs permission to describe the producer's resource
type, and nothing on the record store beyond its own estate's policy.

The data source reads what exists when the consumer plans, so a producer
that has not applied fails the plan. Order the pipeline producer first, as
the example does.

## A value no live resource holds

Such a value, like a name the producer chose, is read from the root outputs
the producer recorded at its last apply:

```hcl
data "terraform_estate_outputs" "cluster" {
  estate = "cluster-infrastructure"
  names  = ["services_namespace"]
}
```

The plan warns that it is as of that apply. Sensitive outputs never cross.
The bucket policy needs `--reads-outputs-of cluster-infrastructure`, or the
plan stops naming that estate.
