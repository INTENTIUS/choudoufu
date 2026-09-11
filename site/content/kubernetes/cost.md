---
title: "Cost"
weight: 5
description: "The one-call sweep does not survive; a label-selected list per kind per namespace does, and it does not grow with the cluster."
deeper:
  - "[#1016](https://github.com/INTENTIUS/choudoufu/issues/1016), \"The one-call sweep, and claim 14 with it\"."
  - "[What a plan costs]({{< relref \"/docs/model/plan-cost\" >}}) for the AWS measurement this is compared to."
---

# Cost

Nothing has been measured on a cluster. What follows is the shape, from the
API's own properties.

## The sweep

On AWS the estate sweep is a single `GetResources` call, filtered
server-side on the marker, covering the whole admission table at once.
Kubernetes has no cross-kind label-filtered list. A sweep there is discovery
(`/apis` enumerates every kind the cluster serves, CRDs included) and then
one list per kind per namespace.

Two things survive. A label-selected list returns only the estate's objects
and does not grow with the cluster, so "a plan costs its estate, not its
account" holds in weakened form. And because the universe of kinds is asked
rather than tabulated, the AWS failure mode where an admitted type outside
the generated table is owned, orphaned and unreachable cannot occur.

What does not survive is "one call", and the claims page marks claim 14
restated rather than pretending otherwise.

## The read pass

Reading each declared object is one `GET` per object, as it is for stock.
Server-side dry run validates, defaults and runs admission without
persisting, which no AWS plan can do.
