---
title: "Proof"
weight: 5
description: "Which claims are proven on a real cluster, and how to run them."
deeper:
  - "[`live/kubernetes/PROOF.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/kubernetes/PROOF.md): this page in full, with every measurement and issue."
  - "[#1016](https://github.com/INTENTIUS/choudoufu/issues/1016): the research and the marker decision."
---

# Proof

## Claims you can run

Each scenario runs by hand on a kind cluster, with a `BREAK=1` run that
corrupts what the claim guards and is caught. A cell reads proven on a
dated run of every proof in it, and open when it has none.

{{< claims-table provider="kubernetes" >}}

## Real estates through fixed stages

Real Kubernetes configurations, pinned by commit, run through the same stages
as the AWS estates: deploy, migrate from a stock state, plan empty, day-two
changes, destroy.

{{< gauntlet-bars lane="kubernetes" >}}

## Run one now

```
just smoke k8s-greenfield
```

## What it costs against stock

Not measured yet: [plan-cost accounting]({{< relref "/docs/model/plan-cost" >}})
has run only against AWS estates so far.
