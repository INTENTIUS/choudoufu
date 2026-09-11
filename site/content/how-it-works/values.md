---
title: "Values"
weight: 2
description: "Where the few values the platform cannot hold are kept."
deeper:
  - "[Values, in full]({{< relref \"/docs/model/values\" >}}) and [where things are stored]({{< relref \"/docs/use/storage\" >}})."
---

# Values

Most resources need nothing here: a resource with a live twin recovers its
values by reading it. The exceptions are resources with no twin at all,
a `null_resource` that ran a script, a `time_static`, a `random_pet`, plus
the arguments a provider never echoes back, sensitivity marks, taint, and a
deposed key.

Every managed instance has one small record for those, namespaced per
estate, written with compare-and-swap under your own access control. An
estate that declares no store gets a local one by default, the way stock
implies a local state file.

{{< providers key="values" >}}
