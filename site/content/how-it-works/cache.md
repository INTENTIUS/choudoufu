---
title: "The cache"
weight: 4
description: "What the state file is once the record lives elsewhere, and what losing it costs."
deeper:
  - "[The disposable cache]({{< relref \"/docs/model/cache\" >}}): the knobs, and why a default plan ignores it on purpose."
  - "[Staleness costs reads]({{< relref \"/docs/claims/staleness-costs-reads\" >}}) and [the roundtrip]({{< relref \"/docs/claims/roundtrip\" >}}), both runnable."
---

# The cache

Every run keeps an ordinary state file as a cache, and three rules govern
it: it is never consulted for ownership; when it and the live system
disagree, live wins; losing it costs a slower run and nothing else. Stale is
the expected condition. The project is named after fermented tofu.

The file is a stock-format state file on purpose. Copy it into place, remove
the `live` block, and stock OpenTofu plans, converges and destroys with it.
A cache you may lose without cost is also a state file you may keep without
ceremony.

{{< providers key="cache" >}}

The two other per-platform answers that follow from the marker, the gate and
the inventory command, are on each platform's own pages; the table below is
the same data.

{{< providers key="gate" >}}

{{< providers key="inventory" >}}
