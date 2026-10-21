---
title: Remove a resource
description: Destroy a resource by deleting its block, or keep it running unmanaged.
prompt: >-
  Follow https://intentius.io/choudoufu/docs/use/remove-a-resource/ for the
  resource I name and propose the edit as a diff.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

## Keep it running

Set the policy, then delete the block.

```hcl
policy {
  undeclared_tagged = "untag"
}
```

`untag` removes this estate's marker. `keep` leaves it
([`live/OWNERSHIP-POLICY.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/OWNERSHIP-POLICY.md)).

## Destroy it

Delete the block and apply. To remove the whole estate, run `choudoufu destroy`.

On Kubernetes the sweep finds each kind by the `tofu-estate` label and leaves
what a controller or a Helm release made.
