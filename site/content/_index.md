---
title: choudoufu
type: docs
---

# choudoufu ![](choudoufu-inline-64.png)

OpenTofu with one permission model.

Each resource carries its own ownership record as ordinary cloud tags. AWS
can tell you what an estate contains, and your IAM already decides who may
read or change it. Nothing else to permission, and no lock to manage.
Experimental, AWS only.

A fork of OpenTofu.

![a plate of choudoufu](choudoufu-hero.png)

## Three things have to survive between runs

![Identity as two tags on the resource written by the apply, values in a record store written by choudoufu, effects as a receipt you declare, all three governed by your IAM](diagram-pieces.svg)

**[Identity]({{< relref "/docs/model/identity" >}})**. Pure IAM. Who may change which resource is a
policy you already know how to write.

**[Values]({{< relref "/docs/model/values" >}})**. Resources AWS has no object for.
`null_resource`, `random_pet`, `time_static`.

**[Effects]({{< relref "/docs/model/effects" >}})**. A database migration that ran. The plan shows it
coming before anything fires.

[Why they are separate, and what it changes]({{< relref "/docs/model" >}}).

- [The wrong config cannot reach production]({{< relref "/docs/governance/blast-radius" >}}). The mistake fails at the cloud, not at review.
- [One policy for every team]({{< relref "/docs/governance/abac" >}}). A new team costs a session tag, not a policy.
- [Nothing is created unowned]({{< relref "/docs/governance/unowned" >}}). Ownership becomes a precondition of existing.
- Nothing to lock. Concurrent runs settle at the API.

[The policies that do these]({{< relref "/docs/governance" >}}), and where AWS honours the condition they rest on.

## How far it goes

**Core** is a fixed, representative set — the terraform-aws-modules examples
most people actually deploy, plus real OpenTofu-native projects, plus one
plain reference estate — pinned by tag, meant to reach 100%. **All** adds
every other real estate as it's crossed, with no pin and no target.

{{< gauntlet-bars >}}

## Check yours

```sh
choudoufu live-check
```

In your config directory. No credentials.

## Docs

[The model]({{< relref "/docs/model" >}}), [governance]({{< relref "/docs/governance" >}}), and
[using it]({{< relref "/docs/use" >}}) — or start at the [full docs index]({{< relref "/docs" >}}).

Experimental. AWS only. Built on OpenTofu 1.13.0 from fork point
[`03743ce6e8`](https://github.com/opentofu/opentofu/commit/03743ce6e8).
Plain OpenTofu is documented at [opentofu.org](https://opentofu.org/docs/).

choudoufu is an independent fork. It is not affiliated with or endorsed by
OpenTofu or the Linux Foundation. OpenTofu is a registered trademark of the
Linux Foundation. Code is licensed MPL-2.0. Upstream source lives at
[github.com/opentofu/opentofu](https://github.com/opentofu/opentofu) and all
stock docs at [opentofu.org/docs](https://opentofu.org/docs/).
