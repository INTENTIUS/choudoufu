---
title: "Proof"
weight: 5
description: "Which claims are proven on a real cluster, and how to run them."
deeper:
  - "[`live/kubernetes/PROOF.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/kubernetes/PROOF.md): this page in full, with every measurement and issue."
---

# Proof

## Claims you can run

Each scenario runs by hand on a kind cluster, and its `BREAK=1` run breaks
the claim on purpose. A cell reads proven once every proof in it has a dated
run.

{{< claims-table provider="kubernetes" >}}

## Real estates through fixed stages

Real Kubernetes configurations, pinned by commit, run the same stages as
the AWS estates.

{{< gauntlet-bars lane="kubernetes" >}}

## Run one now

```
just smoke k8s-greenfield
```

## What it costs against stock

At commit `20bc8ccbcf`, a four-object estate's plan sent 74 requests, and 74
again with 80 foreign ConfigMaps added: every list carries the estate's
selector, and the floor is one list per kind
([claim 14]({{< relref "/docs/claims/plan-cost-tracks-the-estate" >}})).
The change cone
([claim 47]({{< relref "/docs/claims/plan-cost-tracks-the-change" >}})) is
open.
