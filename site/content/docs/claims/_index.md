---
title: "The claims"
weight: 1
bookCollapseSection: true
---

# Claims you can run

Each promise here is a scenario you can run against a local AWS emulator or
a kind cluster. Exit 0 means every assertion held.

Under `BREAK=1` each scenario manufactures the corruption its claim guards
against, and must catch it.

{{< claims-table >}}

Each title links to the claim's page beside its script. The
[harness page](https://github.com/INTENTIUS/choudoufu/blob/main/live/smoke/README.md)
documents every knob.
