---
title: Rename a resource
description: Rename a block and its marker, or move a resource to another estate.
prompt: >-
  Follow https://intentius.io/choudoufu/docs/use/rename-a-resource/ to rename
  the resource I name, then show me `choudoufu live-mv -dry-run` for it.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

Rename the block, then rewrite the marker.

```
choudoufu live-mv aws_vpc.old aws_vpc.new
```

This rewrites the live resource's `tofu-address` tag, which replaces `moved`
blocks. The new address must be in your configuration unless you pass
`-allow-missing-config`. `-dry-run` previews the write.

## Move a resource to another estate

Move the block into the other estate, then run `live-mv` there with
`-from-estate` naming the source.

```
choudoufu live-mv -from-estate=monolith aws_iam_role.team aws_iam_role.team
```

This rewrites one resource's `tofu-estate` tag. The destination must declare
the address, and no resource there may already carry it. An untaggable type
follows its parent.

## On Kubernetes

The address is an annotation beside the `tofu-estate` label. `live-mv` or the
next apply rewrites it. A move between estates passes the cluster's admission
policy like a `kubectl label`, so you need rights in both.
