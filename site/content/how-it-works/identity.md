---
title: "Identity"
weight: 1
description: "Which live object a configuration block owns, held on the object itself."
deeper:
  - "[Identity, in full]({{< relref \"/docs/model/identity\" >}}): recovery paths, the static-evaluability rule, and why untaggable is not unidentifiable."
  - "[`live/MARKERS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/MARKERS.md): the marker spec, the one surface external tooling relies on."
---

# Identity

A marker is an ownership record carried on the resource itself, in whatever
the platform lets anyone select on. It is written as part of the create
call, so a resource that exists carries one. The next plan reads it back
live; any tool that can read the marker can list an estate, and any tool
that can write one can adopt a resource, with no dependency on this fork.

Two paths back from a live object, decided by who chose the identity. Where
the configuration named it, the marker confirms ownership. Where the
platform assigned it, the marker is the only way back, and that is why an
identity has to be settleable before the marker is written.

{{< providers key="identity" >}}

A resource with no marker surface is not unidentifiable. An attachment is
the two things it attaches; a record is a role name and a policy name. Those
recompute from configuration on every run and need no carrier at all.
