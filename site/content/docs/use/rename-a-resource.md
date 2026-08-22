---
title: "How to rename a resource"
weight: 5
---

# How to rename a resource

Rename the resource block, then rewrite the marker.

```
choudoufu live-mv aws_vpc.old aws_vpc.new
```

That rewrites the `tofu-address` tag on the live resource carrying the old
address. The tag write is the move, so `moved` blocks are refused. Resources
never adopted are left alone.

A destination address absent from your configuration is refused unless you pass
`-allow-missing-config`. `-dry-run` shows what it would write. Full options in
`choudoufu live-mv -help`.
