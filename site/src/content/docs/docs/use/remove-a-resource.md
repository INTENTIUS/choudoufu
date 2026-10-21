---
title: Remove a resource
description: Destroy a resource by deleting its block, or keep it running unmanaged.
prompt: >-
  Follow https://intentius.io/choudoufu/docs/use/remove-a-resource/ for the
  resource I name and propose the edit as a diff.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

:::caution
Deleting a resource block destroys the resource unless you change
`undeclared_tagged` first.
:::

## Keep it running

Set the policy, then delete the block.

```hcl
policy {
  undeclared_tagged = "untag"
}
```

`untag` removes this estate's marker. `keep` leaves the marker in place.
[`live/OWNERSHIP-POLICY.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/OWNERSHIP-POLICY.md)
lists the verbs.

## Destroy it

Delete the block and apply. Check that the plan destroys exactly that resource.

On AWS, destroy an untaggable type, such as `aws_route`, before removing its block,
because the block is its only record. Plans list these types under "Not swept
for removal".
[`live/LIMITATIONS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md)
covers these orphans.

To remove the whole estate, run `choudoufu destroy`. It refuses `-refresh-only`.

The AWS sweep may read the eventually consistent tagging index, so an orphan
can appear one run late.

On Kubernetes the sweep lists each kind by the `tofu-estate` label. It never
proposes deleting what a controller made: an owned object, a Helm release's
object, or one only the control plane wrote. A finalizer can hold a
deleted object, and deleting a marked Namespace takes everything inside it.
