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

## Moving a resource to another estate

The same command moves a resource across an estate boundary. Move the
resource block into the other estate's configuration, then run it there
with `-from-estate` naming the estate the resource is leaving:

```
choudoufu live-mv -from-estate=monolith aws_iam_role.team aws_iam_role.team
```

The two addresses may be the same. The write is the `tofu-estate` tag, one
resource per call, and the refusals are the rename's: the destination
configuration must declare the address, nothing in the destination estate
may already carry it, and a plan that would touch anything beyond tags is
never applied. A resource whose type carries no tags follows its parent's
live tag and needs no call. The source estate keeps its record for the
resource until its next plan, which reads the live tag and leaves the
resource alone. [Claim 12]({{< relref "/docs/claims#claim-12-carve-by-retag" >}})
walks a whole split this way.
