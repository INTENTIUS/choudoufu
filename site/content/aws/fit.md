---
title: "Fit"
weight: 4
description: "Will my configuration work here? One command answers, with no credentials, and the reference behind it names every refusal."
deeper:
  - "[How to check a configuration before migrating]({{< relref \"/docs/use/check-a-config\" >}}): reading `live-check`'s output."
  - "[Compatibility reference]({{< relref \"/docs/use/compatibility\" >}}): every construct admitted or refused, and how you may run it."
  - "[Resource tier lookup]({{< relref \"/docs/use/resource-tiers\" >}}): search for your own types and read off tier, status and reason."
  - "[`live/LIMITATIONS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md): every limit with its lint rule and its fixture."
---

# Fit

Run this in your configuration directory. It needs no cloud credentials.

```
choudoufu live-check
```

It reports what this fork would admit and refuse in the code you already
have, by name, with the rule and the remedy for each refusal.

## Types

Every type stock supports is admitted. What varies per type is how its
identity survives losing everything else: marker-carried (a live tag),
declaration-carried (recomputed from configuration, no marker ever written),
record-carried (only the record store remembers it), or excluded by design
(credential material this fork will not persist). The lookup page classifies
every one of the provider's resource types into exactly one of those, and a
type short of in-contract says why.

## Constructs

Type coverage is rarely what stops a configuration. What does is a construct
that asks an address or an identity to resolve before a provider can answer:
a `count` or `for_each` over a module output, a `for_each` keyed by a
parent's live ID, or a `count.index` two instances would render identically.
A `backend "s3"` block and a non-default workspace are refused because there
is no state to back. Data sources are read before anything resolves, so a
`count` over one expands normally.

## How you run it

`plan -out` followed by `apply <planfile>` runs, and refuses by name when the
world has moved. `-target` works. Workspaces do not. Everything outside the
live hooks is stock OpenTofu, and a configuration with no `live` block gets
stock behaviour, measured: the same API calls, exactly.

## Other providers

A resource from another provider is not refused on sight. Its identity has to
derive from configuration: eleven `google_*` types and four `kubernetes_*`
types do that today and plan without a marker; a provider that publishes no
identity at all, `github_*` among them, is refused as `unadmitted-type`, and
a mixed estate is reported root by root.
