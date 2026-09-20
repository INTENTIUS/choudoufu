---
title: "Proof"
weight: 5
description: "Which claims are proven on a real cluster, and how to run them."
deeper:
  - "[`live/kubernetes/PROOF.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/kubernetes/PROOF.md): this page in full, with every measurement and issue."
  - "[#1016](https://github.com/INTENTIUS/choudoufu/issues/1016): the research and the marker decision."
---

# Proof

Seven claims run on a real API server, a kind cluster in Docker, each with a
`BREAK=1` run that corrupts what the claim guards and must be caught. They run
in CI on every pull request that touches the Kubernetes code.

```
just smoke k8s-greenfield
```

{{< claims-table provider="kubernetes" >}}

## The gauntlet lane

Real Kubernetes configurations, pinned by commit, run through the same stages
as the AWS estates: deploy, migrate from a stock state, plan empty, day-two
changes, destroy.

{{< gauntlet-bars lane="kubernetes" >}}
