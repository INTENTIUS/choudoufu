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

A producer that has not applied fails a consumer's data source read, so order the
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

## A value the producer has not applied yet

A change that reaches both estates would plan the consumer on the outputs the
producer recorded at its last apply, which the same change is about to
replace. Plan the consumer on the producer's plan instead:

```sh
# in the producer
choudoufu plan -out=producer.tfplan
choudoufu show -json producer.tfplan > producer.json

# in the consumer
choudoufu plan -estate-outputs-from cluster-infrastructure=producer.json -out=consumer.tfplan
```

`-estate-outputs-from <estate>=<file>` answers that estate's
`terraform_estate_outputs` reads from the plan's `planned_values`, not the
record store. It is repeatable, once per estate. `live-plan-set` does the
same for a set of roots: a root that reads another root of the set is planned
after it and marked `"reads": "planned"`.

| The producer's planned output | The consumer's plan |
|---|---|
| known at plan time | reads the new value |
| known only after the producer applies | refused, naming the estate and the output; the plan is never made on an unknown or a placeholder |
| sensitive | refused: a sensitive output never crosses, planned or recorded |
| not in the producer's plan | refused, naming the output |

The consumer's saved plan carries the values it read. `apply <planfile>`
reads the record store again and refuses with exit 3, naming the estate, the
output, and the planned and applied values, until the producer has applied
and recorded those same values. Apply the producer first. A consumer is never
applied on a value the producer did not land.

An output refused as unknown is settled by applying the producer and then
planning the consumer on what it recorded, without the flag.
