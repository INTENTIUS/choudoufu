---
title: "How close AWS is"
weight: 30
bookCollapseSection: true
---

# How close AWS is

**Core estates clear** and **all estates clear**, read from artifacts the
test suite writes, are the headline: the answer to whether choudoufu works
across real-world configurations, which is the question a customer is
asking. An estate is a real OpenTofu or Terraform configuration, pinned by
commit, run through every active stage below side by side with stock
OpenTofu against the pinned emulator. It is clear when every headline stage
passes - an active stage not marked "no" in the Headline column below. A
stage marked "tier-1 gated" activates on a fast fixture rather than on
per-estate sections (#999): an estate that has never run it stays clear,
but a genuine fail on it still breaks clear.

{{< gauntlet-bars >}}

The two AWS bars count every estate that runs on the emulator. The third
bar is the `kubernetes` lane ([#1067](https://github.com/INTENTIUS/choudoufu/issues/1067)):
its estates run against a kind cluster, a real API server rather than an
emulator, and count toward neither AWS bar. A stage that cannot run on
that substrate reads `n/a` in the estate's row, with the reason on the
estate's own page, and is neutral for clear; `live/GAUNTLET.md` says
under each stage how it reads there. The
[Kubernetes proof page]({{< relref "/kubernetes/proof" >}}) shows the
same bar beside the Kubernetes claims.

{{< gauntlet-board "banner" >}}

The behaviors-proven line above counts how many of the
{{< gauntlet-board "stage-count" >}} stages below have a FAST tier-1 fixture
(`live/behaviors.json`) - a small, purpose-built script that runs in minutes
rather than an estate's own hours - whose representative set (a real `count`
block, a real `for_each` map, a module-nested case, and, for a stage
touching identity resolution, one fixture per identity kind) all pass. **A
stage with no tier-1 fixture is not unproven** - it is proven by the estates
above, just slowly; this number says only how many stages have a fast
signal for contributors.

Every table on this page is rendered from `site/data/gauntlet_board.json`
and `site/data/gauntlet.json`, both written by `go run ./tools/gauntlet
render`; the prose around them is the only thing typed by hand.

## The stages

{{< gauntlet-board "stages" >}}

Planned stages are listed so the target is visible. They do not count toward
clear until they are activated, and, for a headline stage, activating one
lowers the bars until the estates catch up - a non-headline stage can be
active, and measured per estate, without moving either bar. The full
definition of every stage, including what stock's answer is and how each
check is proven non-vacuous, is
[`live/GAUNTLET.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/GAUNTLET.md).

## The estates

{{< gauntlet-board "estates" >}}

## Run time

{{< gauntlet-board "runtime" >}}

## Live-AWS certification

Separate from the two bars above, and never counted toward either of
them: a real-AWS run for the named estate, at the date and account
below, is evidence about ONE run against a real account, not a
repeatable comparison against stock the way an emulator row is. See
[HANDOFF.md](https://github.com/INTENTIUS/choudoufu/blob/main/HANDOFF.md)
"What a measurement is worth" for why the two are never averaged
together.

{{< gauntlet-board "livecert" >}}

To add an estate, see [Add an estate]({{< relref "add-an-estate" >}}).
