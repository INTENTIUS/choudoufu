---
title: "Proof"
weight: 5
description: "Which claims are restated for Kubernetes, which do not apply, what a sweep would cost, and the kind-cluster harness the next units run against."
deeper:
  - "[#1016](https://github.com/INTENTIUS/choudoufu/issues/1016), \"The one-call sweep, and claim 14 with it\"."
  - "[The claims]({{< relref \"/docs/claims\" >}}): every claim's AWS scenario, and the Kubernetes note on each."
  - "[Add an estate]({{< relref \"/docs/progress/add-an-estate\" >}}): providers are lanes in the same manifest."
---

# Proof

One claim is proven on a real cluster, the marker itself, and the rest are
stated per claim in the claims data rather than left implicit. The table
below shows only the claims whose Kubernetes cell is not still open; hover a
cell for its note.

{{< claims-table provider="kubernetes" >}}

## The harness

```
just smoke k8s-greenfield
```

The AWS claims run against a local emulator in Docker. This runs against a
kind cluster in Docker, which is a real API server, so the same scenario
shape carries over without the emulator-fidelity question that stopped a
second cloud: a verdict line per step, exit 0 only when every claim held,
`BREAK=1` manufacturing the fault. The scenario applies a namespace and a
ConfigMap under a `live` block with no AWS provider anywhere, reads the
`tofu-estate` label back with kubectl, replans empty, loses its cache
without consequence, and destroys exactly; its `BREAK=1` strips the label
and requires the replan to propose restoring it
([claim 21]({{< relref "/docs/claims/k8s-greenfield" >}})).

A Kubernetes estate would enter the gauntlet manifest with its own lane, run
the same stages against its own substrate, and count toward its own bar,
never toward the AWS ones.

## What it would cost

Nothing has been measured on a cluster. What follows is the shape, from the
API's own properties.

### The sweep

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

### The read pass

Reading each declared object is one `GET` per object, as it is for stock.
Server-side dry run validates, defaults and runs admission without
persisting, which no AWS plan can do.
