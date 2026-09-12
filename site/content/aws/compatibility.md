---
title: "Compatibility"
weight: 4
description: "What live-check refuses in a real configuration, and why each refusal exists."
deeper:
  - "[Compatibility reference]({{< relref \"/docs/use/compatibility\" >}}): every construct admitted or refused, and how you may run it."
  - "[How to check a configuration before migrating]({{< relref \"/docs/use/check-a-config\" >}}): reading the output."
  - "[Resource tier lookup]({{< relref \"/docs/use/resource-tiers\" >}}): every provider type with its tier, status and reason."
  - "[`live/LIMITATIONS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md): each limit with the lint rule that enforces it and the fixture that proves it."
---

# Compatibility

```
choudoufu live-check
```

Run it in the directory that holds your configuration. It loads the
provider schemas, walks every resource block, and prints one line per thing
it would refuse: the block, the rule, and what to change. No credentials and
no cloud calls.

## The backend

A `backend` or `cloud` block is refused. There is no state file to back, so
there is nothing for it to do; delete it and declare the estate instead.
Workspaces are refused for the same reason.

## Expansions and identity arguments

Every instance gets its marker on the create call, so `count`, `for_each`
and the argument that names a resource have to be settled before the first
provider call. A `count` over a variable, a local, a data source or a
sibling's attribute is fine. What is refused: a `count` or `for_each` over a
module output, a `for_each` keyed by a parent's live id, and a `count.index`
used where two instances would render the same value. The message names the
block and the expression.

## Resource types

Every type the AWS provider ships is admitted. What differs is where a
type's identity lives once the record store, the state file and the tool
are all gone: on a tag, for the taggable half of the provider; recomputed
from configuration, for attachments, policies and anything else named by
what it joins; or only in the record store, for types AWS names itself and
gives no tag. Three types are excluded on purpose because they mint
credential material. The lookup page lists every type with a reason for
anything short of in-contract.

## Running it

`plan -out` then `apply <planfile>` works, and the apply refuses by name if
the live system moved since the plan. `-target` works. A configuration with
no `live` block gets stock behaviour, measured: the same API calls, exactly.

## Other providers

A resource from another provider is refused only when its identity cannot
be derived from your configuration. Eleven `google_*` types and four
`kubernetes_*` types derive it today and plan without a marker. `github_*`
and `fastly_*` publish no identity and are refused as `unadmitted-type`. A
mixed estate is reported root by root.
