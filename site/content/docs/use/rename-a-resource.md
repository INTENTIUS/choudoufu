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
resource per call. The rename's refusals stand in front of it. The
destination configuration must declare the address. Nothing in the
destination estate may already carry it. A plan that would touch anything
beyond tags is never applied. A resource whose type carries no tags follows
its parent's live tag and needs no call. The source estate keeps its record for the
resource until its next plan, which reads the live tag and leaves the
resource alone. [Claim 12]({{< relref "/docs/claims/carve-by-retag" >}})
walks a whole split this way.

## On Kubernetes

The marker is one label, `tofu-estate`, and the object carries no
address: it is bound to its block by its own kind, namespace and name. A
rename within an estate therefore has nothing to write. Rename the block;
`live-mv` run out of habit reports `Nothing to write` and exits 0, and the
next plan is empty. Moving an object to another estate is the same
`-from-estate` command as above, run in the destination's configuration.
It rewrites the label through the provider, as a labels-only plan and
apply on that one object, under your own credential, so the cluster's
admission policy judges it exactly as it judges a plain `kubectl label`:
you must hold both the estate the object is leaving and the one it is
entering ([claim 23]({{< relref "/docs/claims/k8s-the-label-is-the-boundary" >}})).
An object declared through a manifest block is refused by name with the
equivalent `kubectl label` command.
