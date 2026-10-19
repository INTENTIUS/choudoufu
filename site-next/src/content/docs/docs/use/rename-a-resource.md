---
title: Rename a resource
description: Rename a block and rewrite its marker, or move a resource to another estate.
prompt: >-
  Follow https://intentius.io/choudoufu/docs/use/rename-a-resource/ to rename
  the resource I name. Rename the block, then run `choudoufu live-mv -dry-run`
  with the old and new addresses and show me what it would write.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

Rename the resource block, then rewrite the marker.

```
choudoufu live-mv aws_vpc.old aws_vpc.new
```

That rewrites the `tofu-address` tag on the live resource carrying the old
address. The tag write is the move, so `moved` blocks are refused. A
destination address missing from your configuration is refused unless you pass
`-allow-missing-config`. `-dry-run` shows what it would write, and
`choudoufu live-mv -help` has the rest.

## Move a resource to another estate

Move the block into the other estate's configuration, then run `live-mv` there
with `-from-estate` naming the estate it leaves.

```
choudoufu live-mv -from-estate=monolith aws_iam_role.team aws_iam_role.team
```

The two addresses may be the same. The write is the `tofu-estate` tag, one
resource per call. The destination must declare the address, and nothing in it
may already carry it. A type with no tags follows its parent's tag and needs no
call. The source estate drops the resource on its next plan.

## On Kubernetes

The address is an annotation beside the `tofu-estate` label. A rename within an
estate rewrites that annotation: run `live-mv`, or let the next plan and apply
do it. A move to another estate is the same `-from-estate` command, judged by
the cluster's admission policy like a `kubectl label`, so you must hold both
estates.
