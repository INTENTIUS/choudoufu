---
title: "Plan fidelity"
weight: 6
---

# Plan fidelity

Every stage of the gauntlet compares a plan, or the cloud after an apply,
against what stock OpenTofu does for the same configuration. The promise
behind all of them, stated once rather than left to be inferred stage by
stage: an equal plan, or a refusal this documentation names in advance.
Anything else is a defect.

This is a contract about shape, not a scoreboard. It says what a difference
from stock is allowed to be, not how many estates currently clear. For the
numbers, see [How close AWS is]({{< relref "/docs/progress" >}}).

## What "equal" excludes

One thing, always the same one: the marker tags choudoufu writes for
identity, `tofu-estate` and `tofu-address` (see
[Identity]({{< relref "/docs/model/identity" >}})). Stock has no argument to
write them into, so a plan that carries them and a plan that does not are not
different plans, and every stage that diffs a plan or the resulting cloud
strips both sides of these tags before comparing. Nothing else is normalised
away. A different resource count, a different argument value, a different
order of operations where order is observable: none of that is excused.

## Where choudoufu is deliberately stricter

A refusal where stock proceeds is ordinarily the plainest kind of defect: the
class of divergence the gauntlet exists to catch. The one exception is a
stage whose own definition commits, in advance, to refusing on purpose, so
that anyone reading it before the estate runs already knows the divergence is
coming and why.

Today that is `plan_approval`: `plan -out` followed by `apply <planfile>`
applies cleanly when the world has not moved since the plan was taken, and
refuses, naming the mismatch, when it has. Stock applies a stale planfile
anyway. choudoufu does not, by design, and the gauntlet asserts that refusal
directly rather than diffing it against stock's more permissive behavior.
`plan_approval` is not active yet; it is a planned stage, listed in
[`live/GAUNTLET.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/GAUNTLET.md)
so the target is visible before it starts counting toward an estate's clear
bar. The contract it will enforce is already decided, though, which is why
it belongs here.

A refusal that isn't written into a stage's own definition this way gets no
such pass. It is scored as choudoufu refusing where stock proceeds, and that
is a defect like any other.

## How a difference gets classified

Once the marker tags are stripped, a remaining difference is one of five
things, the table
[`HANDOFF.md`](https://github.com/INTENTIUS/choudoufu/blob/main/HANDOFF.md)
keeps: choudoufu refusing where stock proceeds is a defect, the plans or the
resulting cloud differing is a defect, stock failing too still leaves the
estate to clear (matching stock's failure is never the finish line), a wrong
answer from the pinned emulator is fixed in the emulator, and an instance
that would need a wrong marker to converge drops to the record rung and the
run proceeds rather than forcing one. Every row leads to a fix, an emulator
change, or a tracked rung ticket; none of them lets a difference stand
unexplained.

## How the promise is enforced

A stage that always passes proves nothing. Every stage in
[`live/GAUNTLET.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/GAUNTLET.md)
that runs choudoufu at all carries a `BREAK=1` control: set it, and the
crossing script deliberately introduces the exact defect the stage exists to
catch: a corrupted identity string, a second object mutated where only one
should be, a stale planfile applied instead of refused. When that happens,
the stage must fail. A check that cannot be made to fail this way is not
evidence for this contract, whatever verdict it reports on an ordinary run.

The exemption is stage 1, `cold_deploy`, and its own Break line says why. It
is the stock binary applying the unmodified configuration with no `live`
block: "Not applicable; this stage has nothing of choudoufu's to break." A
failure there is stock failing, which the gauntlet records as such rather
than counting against this contract. Every other stage carries a control;
`live/GAUNTLET.md`'s Break lines are the list, and they are rendered from
`tools/gauntlet/stages.go` rather than maintained here.
