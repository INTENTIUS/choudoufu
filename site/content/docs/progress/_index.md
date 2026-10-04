---
title: "How close AWS is"
weight: 30
bookCollapseSection: true
---

# How close AWS is

This page is for people building choudoufu. If you are deciding whether to
use it, [the claims]({{< relref "/docs/claims" >}}) are the proof surface:
each promise is a scenario you can run, with an arm that breaks it on
purpose. That ruling is
[#643](https://github.com/INTENTIUS/choudoufu/issues/643)'s, building on
[#522](https://github.com/INTENTIUS/choudoufu/issues/522)'s split of the old
single headline number.

What this page measures is breadth and regression. An estate is a real
OpenTofu or Terraform configuration someone else wrote, pinned by commit, run
through every stage below side by side with stock OpenTofu. It is clear when
every headline stage passes. Real, externally authored configurations
surface defects no purpose-built scenario anticipates, so the board is the
net that catches them. It is re-measured on a cadence, when the maintainer
dispatches the Gauntlet workflow (typically before a release), not on every
change, so a row is as current as its commit and date.

{{< gauntlet-bars >}}

The two AWS bars count every estate that runs on the emulator. The third is
the `kubernetes` lane, whose estates run against a real API server on a kind
cluster and count toward neither AWS bar.

{{< gauntlet-board "banner" >}}

{{< gauntlet-board "script-staleness" >}}

A row is evidence about its crossing script as it stood the day it ran. When
the script or the shared protocol library changes afterwards, the row keeps
its old verdicts until someone re-runs the estate. The line above counts the
rows in that state. It does not fail the build, because a re-run can take
half an hour.

The behaviors-proven line counts how many of the
{{< gauntlet-board "stage-count" >}} stages have a fast fixture that runs in
minutes. A stage without one is still proven by the estates, only slowly.

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
