---
title: Day-2 operations
description: Sharing values between estates, and the plan, review and apply loop.
prompt: >-
  Read https://intentius.io/choudoufu/docs/use/day2/ and propose, as a diff,
  a marker-filtered data source for each `terraform_remote_state` here.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

See also [Rename a resource](../rename-a-resource/),
[Remove a resource](../remove-a-resource/),
[receipts](https://github.com/INTENTIUS/choudoufu/blob/main/live/RECEIPTS.md)
and [concurrent runs](https://github.com/INTENTIUS/choudoufu/blob/main/live/CONCURRENCY.md).

## Sharing values between estates

Read the producer's live resource with a data source filtered by its markers.

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
```

Apply the producer first. `terraform_remote_state` returns a snapshot from the
day the producer migrated.

For a value no resource holds, `data "terraform_estate_outputs"` returns the
producer's outputs as of its last apply. Sensitive outputs stay private. A
bucket consumer's policy needs `--reads-outputs-of <producer>`; a Kubernetes
consumer names the producer in a `reads_outputs_of` block.
[`live/OUTPUTS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/OUTPUTS.md)
and
[`examples/cross-estate-dependency`](https://github.com/INTENTIUS/choudoufu/tree/main/examples/cross-estate-dependency)
show it in use.

## Plan, review, apply

`apply FILE` treats a `plan -out=FILE` as an approval. It plans again and
compares each written value. On a match it applies. On a mismatch it prints
`The approved plan no longer matches the live system` and exits 3, ready for
[CI](../cicd/) to route back to review.
