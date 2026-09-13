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

On Kubernetes there is no address on the object to rewrite, so a rename is
the config edit alone and the next plan is empty; `live-mv` has nothing
governed to do there. The same holds for changing a block's type between
the two spellings of a kind, `kubernetes_config_map` to
`kubernetes_config_map_v1`: that is an `api_version` change, not a move,
and needs no `moved` block ([Operate]({{< relref "/kubernetes/operate" >}})
on the Kubernetes hub).

## Moving a resource to another estate

The same command moves a resource across an estate boundary. Move the
resource block into the other estate's configuration, then run it there
with `-from-estate` naming the estate the resource is leaving:

```
choudoufu live-mv -from-estate=monolith aws_iam_role.team aws_iam_role.team
```

The two addresses may be the same. The write is the `tofu-estate` tag, one
resource per call. The rename's refusals stand in front of it. The
destination configuration must declare the address. Nothing in the
destination estate may already carry it. A plan that would touch anything
beyond tags is never applied. A resource whose type carries no tags follows
its parent's live tag and needs no call. The source estate keeps its record for the
resource until its next plan, which reads the live tag and leaves the
resource alone. [Claim 12]({{< relref "/docs/claims/carve-by-retag" >}})
walks a whole split this way.
