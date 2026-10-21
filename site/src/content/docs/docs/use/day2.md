---
title: Day-2 operations
description: Sharing values between estates, and the plan, review and apply loop.
prompt: >-
  Read https://intentius.io/choudoufu/docs/use/day2/ and propose, as a diff,
  a marker-filtered data source for each `terraform_remote_state` here.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

See also [Rename a resource](../rename-a-resource/) and
[Remove a resource](../remove-a-resource/).

## Sharing values between estates

Read the producer's live resource with a data source. On AWS, filter by its
tags:

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

On Kubernetes, a stock data source reads the object by namespace and name.

For a value no resource holds, `data "terraform_estate_outputs"` returns the
producer's outputs as of its last apply
([`live/OUTPUTS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/OUTPUTS.md)).

## Plan, review, apply

`apply FILE` plans again and applies when every written value matches the saved
plan. Otherwise it prints `The approved plan no longer matches the live system`
and exits 3.
