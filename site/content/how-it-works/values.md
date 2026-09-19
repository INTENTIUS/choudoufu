---
title: "Records"
weight: 2
description: "Where the record of each managed instance is kept."
deeper:
  - "[Records, in full]({{< relref \"/docs/model/values\" >}}) and [where things are stored]({{< relref \"/docs/use/storage\" >}})."
---

# Records

Every managed instance has one record. For a resource with a live object it
holds what a read cannot return: the arguments a provider never echoes back,
sensitivity marks, taint, a deposed key. Ownership is the marker, so losing
that record costs a slower plan. For a record-backed resource, one with no live object at all, a
`null_resource`, a `time_static`, a `random_pet`, the record is the whole
value and the only copy.

Records are namespaced per estate and written with compare-and-swap under
your own access control. Nothing is locked. An estate that declares no store
gets a local directory, the way stock implies a local state file.

{{< providers key="values" >}}
