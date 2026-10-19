---
title: Day-2 operations
description: Running an estate after the first apply, sharing values between estates, and the plan, review and apply loop.
prompt: >-
  Read https://intentius.io/choudoufu/docs/use/day2/ and find every
  `terraform_remote_state` in this repository. Propose the marker-filtered data
  source that replaces each, as a diff for my review.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

| Task | Where |
|---|---|
| Rename a resource | [Rename a resource](../rename-a-resource/) |
| Stop managing or destroy one | [Remove a resource](../remove-a-resource/) |
| Record an effect the cloud cannot report | [`live/RECEIPTS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/RECEIPTS.md) |
| Look up a `policy` verb | [`live/OWNERSHIP-POLICY.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/OWNERSHIP-POLICY.md) |
| Two runs at once | [`live/CONCURRENCY.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/CONCURRENCY.md) |

## Sharing values between estates

Read another estate's value from its live resource, with an ordinary data
source filtered by the producer's markers. The producer publishes nothing.

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

`terraform_remote_state` is admitted, but a live root writes no state file of
record, so it returns a snapshot frozen on the day the producer migrated. Apply
the producer first; until then the data source read fails.

A value no live resource holds, such as a name the producer chose, comes from
the outputs the producer recorded at its last apply, through
`data "terraform_estate_outputs"`. The plan warns that it is as of that apply,
and sensitive outputs never cross. On a bucket, the consumer's policy needs
`--reads-outputs-of <producer>`. On `record_store "kubernetes"`, the consumer
names the producer in a `reads_outputs_of` block.

[`live/OUTPUTS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/OUTPUTS.md)
has the reasoning, and
[`examples/cross-estate-dependency`](https://github.com/INTENTIUS/choudoufu/tree/main/examples/cross-estate-dependency)
runs it with two estates.

## Plan, review, apply

`plan -out=FILE` writes stock's own plan file, and `apply FILE` reads it as an
approval, not an instruction. The apply re-reads the live system, plans again,
and compares that plan with the approved one, down to the values each change
writes.

Where they agree, the apply runs. Where they differ, nothing changes: it prints
`The approved plan no longer matches the live system` with the rows that moved,
and exits 3. A pipeline can route that back to review instead of paging
someone. [Running an estate from CI](../cicd/) builds on it.
