---
title: "Day-2 operations"
weight: 4
---

# Day-2 operations

Running an estate after the first apply: renaming and removing resources,
recording effects the cloud cannot report, and working with other people.

| Task | Page |
|---|---|
| Rename a resource | [How to rename a resource]({{< relref "/docs/use/rename-a-resource" >}}) |
| Stop managing or destroy a resource | [How to stop managing or destroy a resource]({{< relref "/docs/use/remove-a-resource" >}}) |
| Record an effect the cloud cannot report | [How to record an effect the cloud cannot report]({{< relref "/docs/use/record-an-effect" >}}) |
| Look up what a `policy` setting does | [The ownership policy matrix]({{< relref "/docs/use/ownership-policy" >}}) |
| Understand what happens when two runs overlap | [Two runs at once]({{< relref "/docs/model/concurrency" >}}) |

## Sharing values between estates

There is no remote state to read. `live/OUTPUTS.md` covers the cross-estate
pattern, and `data "terraform_remote_state"` is refused.

## Plan, review, apply

`plan -out=FILE` writes stock's own plan file, and `apply FILE` reads it as an
approval rather than as an instruction. A live root keeps no prior state, so
the apply never replays what the file describes. It re-reads the live system
and plans against what is there now, then compares that fresh plan against the
approved one, down to the values each change writes.

Where the two agree, the apply runs without asking again, because the file was
the approval. Where they differ, nothing changes. The apply prints `The
approved plan no longer matches the live system` with the rows that moved, and
exits 3. That status is neither an ordinary failure nor
`-detailed-exitcode`'s 2, so a pipeline can route the run back to review
instead of paging somebody about a broken step. The way through is the two
commands you already ran: plan over the world as it is now, approve that, then
apply it.

[Compatibility reference]({{< relref "/docs/use/compatibility#how-you-run-it" >}})
has what the two plans are compared on and which saved-plan invocations stay
refused. [Running an estate from CI]({{< relref "/docs/use/cicd" >}}) has a
pipeline built on it, gate and all.

[#74](https://github.com/INTENTIUS/choudoufu/issues/74) is the history here. It
had chosen a plan fingerprint, a digest printed at plan time for the apply to
check against its own fresh plan, and
[#878](https://github.com/INTENTIUS/choudoufu/issues/878) shipped a comparison
of the two plans instead, so that a refusal can name the address and the
attribute that moved.
[Claim 15]({{< relref "/docs/claims#claim-15-apply-exactly-what-was-approved" >}})
is the runnable version, and the gauntlet's `plan_approval` stage measures both
halves of it on every estate: the matched file that applies and the moved world
that refuses. See [the stage table]({{< relref "/docs/progress#the-stages" >}}).
