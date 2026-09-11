---
title: "Effects"
weight: 3
description: "How something that leaves nothing behind to read back is made visible to a plan."
deeper:
  - "[Effects, in full]({{< relref \"/docs/model/effects\" >}}) and [how to record one]({{< relref \"/docs/use/record-an-effect\" >}})."
  - "[`live/RECEIPTS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/RECEIPTS.md): the pattern and the guards on it."
---

# Effects

A migration that ran, a cache that was invalidated, a notification that was
sent: none leaves anything in the platform to read back, so no plan can say
whether it already happened. A receipt makes it visible: an ordinary
resource you declare, holding a hash of the effect's input, that appears in
your plan and tracks its own staleness.

{{< providers key="effects" >}}
