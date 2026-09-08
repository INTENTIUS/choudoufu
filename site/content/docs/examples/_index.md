---
title: "Examples"
weight: 6
bookCollapseSection: true
---

# Examples

Worked examples that run a real problem end to end against a real account,
measure what happened, and leave you something to build on. Each one is a
small, self-contained project you can clone and run.

| Example | What it shows |
|---------|---------------|
| [Terralith migration](terralith-migration/) | Splitting a monolithic terralith into per-team estates by retagging, with no state surgery, and measuring the plan-time difference live. |

`examples/ci-pipelines/` is a worked example of a different shape: the CI a
choudoufu estate needs, as one chant project whose five Ops
(`live-check`, `live-plan`, `live-apply`, `live-adopt`, `live-discover`)
generate the GitHub and Forgejo workflows checked in beside them, under a
guard that regenerating leaves the tree clean.
[Running an estate from CI]({{< relref "/docs/use/cicd" >}}) is what those
jobs do and what each forge gets; its own
[README](https://github.com/INTENTIUS/choudoufu/blob/main/examples/ci-pipelines/README.md)
is the walkthrough for running it.
