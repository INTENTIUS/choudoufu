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

This rewrites the `tofu-address` tag. `-dry-run` previews the write.

## Move a resource to another estate

Declare the block in the destination estate and run `live-mv` there:

```
choudoufu live-mv -from-estate=monolith aws_iam_role.team aws_iam_role.team
```

## On Kubernetes

`live-mv` or the next apply rewrites the address annotation. A move between
estates needs rights in both.

## On Azure

`live-mv` rewrites the `tofu-address` tag, keys matched case-insensitively.
To change a resource's estate, first move its Azure Policy assignments
([Gate](/choudoufu/azure/gate/)).
