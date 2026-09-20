---
title: "Plan fidelity"
weight: 6
---

# Plan fidelity

The promise: for any configuration stock OpenTofu accepts, choudoufu gives an
equal plan, or a refusal this documentation names in advance. Anything else
is a defect. [How close AWS is]({{< relref "/docs/progress" >}}) has the
numbers. This page is the contract they are measured against.

## What "equal" excludes

One thing: the marker tags, `tofu-estate` and `tofu-address`. Stock has
nowhere to write them, so both sides are stripped of them before a plan or
the resulting cloud is compared. Nothing else is excused. A different
resource count, a different argument value or a different observable order is
a difference.

## Where choudoufu is deliberately stricter

`plan -out` followed by `apply <planfile>` applies when the world has not
moved since the plan was taken. When it has, the apply refuses with `The
approved plan no longer matches the live system`, exit status 3. Stock
applies the stale planfile anyway. That refusal is written into the stage's
own definition in advance, so it is asserted and not counted as a defect
([#878](https://github.com/INTENTIUS/choudoufu/issues/878)).

A refusal no stage names in advance is a defect like any other.

## How it is enforced

A stage that always passes proves nothing. Every stage that runs choudoufu
has a `BREAK=1` control that introduces the exact defect the stage exists to
catch, and the stage must then fail.
[`live/GAUNTLET.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/GAUNTLET.md)
lists the stages, their controls, and how each kind of difference is
classified.
