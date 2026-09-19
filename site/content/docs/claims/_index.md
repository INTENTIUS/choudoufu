---
title: "The claims"
weight: 1
bookCollapseSection: true
---

# Claims you can run

Each promise this site makes is a scenario you can run: Docker, a local AWS
emulator or a kind cluster, one to six minutes. Exit 0 means every assertion
held. `just smoke` lists them.

Every scenario also runs inverted. Under `BREAK=1` it manufactures the exact
corruption the claim guards against, and passes only by catching it.

{{< claims-table >}}

Each title links to that claim's page in the repository, beside the script
that runs it. The
[harness page](https://github.com/INTENTIUS/choudoufu/blob/main/live/smoke/README.md)
documents every knob, including pinning the emulator and the choudoufu
version.
