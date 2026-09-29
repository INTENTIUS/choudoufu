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

On Kubernetes the write is the address annotation beside the label,
since #1639: `live-mv <old> <new>` rewrites it, or the next plan and
apply do it unasked, and neither needs a `moved` block. The two
spellings of a kind, `kubernetes_config_map` to `kubernetes_config_map_v1`,
are an `api_version` change, not a move, and still replan empty
([Operate]({{< relref "/kubernetes/operate" >}}) on the Kubernetes hub).

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

## On Kubernetes

Since #1639 the object also carries the block address in an
annotation beside the label, `tofu-estate`. A rename within an estate
rewrites just that annotation: run `live-mv <old> <new>`, or let the next
plan and apply do it. `live-mv` reports `Nothing to write` once that
annotation already names the new address.

Moving an object to another estate is the same `-from-estate` command as
above, run in the destination's configuration. It rewrites the label
through the provider under your own credential, so the cluster's admission
policy judges it as it judges a plain `kubectl label`: you must hold both
the estate the object is leaving and the one it is entering
([claim 13 on Kubernetes]({{< relref "/docs/claims/the-tag-is-the-boundary" >}})).
An object declared through a manifest block is refused by name with the
equivalent `kubectl label` command.
