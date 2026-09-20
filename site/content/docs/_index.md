---
title: "Documentation"
weight: 1
aliases: ["/evidence/"]
lead: "What choudoufu is, how to run it, and the evidence behind each claim it makes. Every figure on these pages names its fixture and its commit."
---

# Documentation

choudoufu is OpenTofu with one thing changed: what you own is written on the
resources themselves, as a marker your platform's access control can read,
and the state file becomes a cache you may delete. Almost everything else in
the fork is stock OpenTofu, unmodified. A configuration with no `live` block
gets stock behaviour exactly, measured at the same number of AWS API calls
as `tofu plan`
([#588](https://github.com/INTENTIUS/choudoufu/issues/588)).

## Start here

| If you want to | Read |
|---|---|
| Understand it in five minutes | [What it is]({{< relref "/docs/model" >}}) |
| Watch it work, with Docker and no AWS account | [Tutorial]({{< relref "/docs/tutorial" >}}) |
| Start a new estate | [Start a new estate]({{< relref "/docs/use/start" >}}) |
| Bring in resources you already run | [Migrate an existing estate]({{< relref "/docs/use/migrate" >}}) |
| Know what you must create first | [What you set up by hand]({{< relref "/docs/use/setup" >}}) |
| Check your own configuration | [Check a configuration]({{< relref "/docs/use/check-a-config" >}}) |

## The commands you will use

`choudoufu init`, `plan` and `apply` work as they do in OpenTofu, and with a
`live` block present they use the live backend. Three commands are new.

| Command | What it does |
|---|---|
| `choudoufu live-import` | Bulk migration: reads a stock state file once, verifies each entry against the live resource, and writes a marker on everything that verifies |
| `choudoufu live-mv <old> <new>` | Renames a resource by rewriting its marker, with an empty plan on both sides |
| `choudoufu live-check` | Says what in a configuration would be refused, before anything runs |

## The promise, and where it is measured

If OpenTofu runs an estate, choudoufu runs it too: an equal plan, or a refusal
this documentation names in advance. Anything else is a defect.
[Plan fidelity]({{< relref "/docs/model/plan-fidelity" >}}) states the
contract. It is measured by running real Terraform and OpenTofu
configurations side by side with stock OpenTofu:

{{< gauntlet-bars >}}

| Evidence | What it is |
|---|---|
| [The claims]({{< relref "/docs/claims" >}}) | Runnable scenarios, one per claim, each with an arm that breaks it on purpose. `just smoke import` runs one in about two minutes |
| [How close AWS is]({{< relref "/docs/progress" >}}) | Every stage and every estate behind the two bars above |
| [What a plan costs]({{< relref "/docs/model/plan-cost" >}}) | The measured cost of a plan, with links to every figure behind it |
| [Resource tier lookup]({{< relref "/docs/use/resource-tiers" >}}) | Every provider resource type, and what recovers its identity |
| [`live/LIMITATIONS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md) | Every construct that is refused, the rule that refuses it, and the remedy |

## The stock base

{{< fork-surface >}}

Everything outside those fork-owned roots is stock OpenTofu, unmodified beyond
the module path it is built under.
[Compatibility reference]({{< relref "/docs/use/compatibility" >}}) covers what
running under this fork changes about how a configuration behaves.
