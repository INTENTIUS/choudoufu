---
title: Remove a resource
description: Destroy a resource by deleting its block, or stop managing it and leave it running.
prompt: >-
  Follow https://intentius.io/choudoufu/docs/use/remove-a-resource/ for the
  resource I name and propose the edit as a diff for my review.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

:::caution
Deleting a resource block destroys the resource. The marker stays on the live
object, and the next plan proposes destroying a marked resource nobody
declares, as stock does without a `removed` block.
:::

## Stop managing it, keep it running

Change what happens to a resource you no longer declare, then delete the block.

```hcl
# estate.chdf.hcl
estate = "my-estate"

policy {
  undeclared_tagged = "untag"
}
```

`untag` removes this estate's marker and leaves the resource running. `keep`
leaves both alone.
[`live/OWNERSHIP-POLICY.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/OWNERSHIP-POLICY.md)
has the other verbs.

## Destroy it

Delete the block and apply. Check the plan first: it should destroy exactly
that resource.

A type with no tags, such as `aws_route`, `aws_s3_bucket_policy` or
`aws_iam_role_policy`, has nowhere to hold a marker, so deleting its block
loses the only record of which live resource it was. Destroy it before removing
the block. Every plan lists such types under "Not swept for removal";
`-verbose` itemises them.
[`live/LIMITATIONS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md)
has the orphan cases.

## Tear down the whole estate

`choudoufu destroy` removes everything the estate owns. `-refresh-only` stays
refused, because both sides of its comparison would be the live system.

## The sweep can be a run behind

The sweep may use AWS's tagging index, which is eventually consistent, so an
orphan can be reported one run late. Declared resources are read through their
own service API, so a freshly tagged one is never taken for missing.
