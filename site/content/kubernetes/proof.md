---
title: "Proof"
weight: 6
description: "Which claims are restated for Kubernetes, which do not apply, and what the evidence path would be."
deeper:
  - "[The claims]({{< relref \"/docs/claims\" >}}): every claim's AWS scenario, and the Kubernetes note on each."
  - "[Add an estate]({{< relref \"/docs/progress/add-an-estate\" >}}): providers are lanes in the same manifest."
---

# Proof

Nothing is proven on Kubernetes yet. What exists is a statement, per claim,
of what would be true there, written into the claims data rather than left
implicit. The table below shows only the claims whose Kubernetes cell is not
still open; hover a cell for its note.

{{< claims-table provider="kubernetes" >}}

## The evidence path

The AWS claims run against a local emulator in Docker. A kind or k3d cluster
in Docker is a real API server, so the same scenario shape, verdict line per
step and `BREAK=1` inversion, transfers without the emulator-fidelity question
that stopped a second cloud. That is the plan, not a measurement; no scenario
exists yet.

A Kubernetes estate would enter the gauntlet manifest with its own lane, run
the same stages against its own substrate, and count toward its own bar,
never toward the AWS ones.
