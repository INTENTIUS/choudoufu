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
producer's state file. It is admitted here and reads whatever its backend
holds, but a live root has no state file of record, and one left from before
a migration returns a snapshot frozen on that day.

## What it needs

The consumer's role needs permission to describe the producer's resource
type, and nothing on the record store beyond its own estate's policy.

A producer that has not applied fails the consumer's plan, so order the
pipeline producer first, as the example does.

## A value no live resource holds

Such a value, like a name the producer chose, is read from the root outputs
the producer recorded at its last apply:

```hcl
data "terraform_estate_outputs" "cluster" {
  estate = "cluster-infrastructure"
  names  = ["services_namespace"]
}
```

The plan warns that it is as of that apply. The producer's destroy deletes
it. Sensitive outputs never cross.
The bucket policy needs `--reads-outputs-of cluster-infrastructure`, or the
plan stops naming that estate.

When the same change also changes the producer, plan the consumer on the
producer's plan instead:
`plan -estate-outputs-from cluster-infrastructure=producer.json`, where the
file is `show -json` of the producer's saved plan. An output known only
after apply, or sensitive, stops the plan. `apply` of the consumer's plan
refuses with exit 3 until the producer has applied those same values.
`live-plan-set` does this for you when both roots are in the set.

On `record_store "kubernetes"` each estate's records sit in a namespace of
their own, so the consumer names the producer in its record_store block:

```hcl
record_store "kubernetes" {
  reads_outputs_of "cluster-infrastructure" {}
}
```

The read goes to `tofu-records-cluster-infrastructure` (set `namespace`
inside the block if the producer's records are elsewhere) and can only get.
Without the block, the plan stops before reading anything. Without a Role
granting `get` on the producer's output Secrets, the plan stops and prints
the `kubectl` lines that grant it.
