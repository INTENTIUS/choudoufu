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
the apply never replays what the file describes. It re-reads the live system,
plans against what is there now, and then compares its own fresh plan with the
approved one: the resources, the actions, the live object each change was
computed against, and the values planned for them.

Where the two agree, the apply runs without asking again, because the file was
the approval. Where they differ, nothing changes: the apply prints `The
approved plan no longer matches the live system`, names the rows that moved,
and exits 3. That status is neither an ordinary failure nor
`-detailed-exitcode`'s 2, so a pipeline can route the run back to review
instead of paging somebody about a broken step. The way through is the two
commands you already ran - plan over the world as it is now, approve that, and
apply it.

[Compatibility reference]({{< relref "/docs/use/compatibility#how-you-run-it" >}})
has what the two plans are compared on, what is deliberately left outside that
comparison, and which saved-plan invocations stay refused.
[Running an estate from CI]({{< relref "/docs/use/cicd" >}}) has a pipeline
built on it, gate and all.

[#74](https://github.com/INTENTIUS/choudoufu/issues/74) is the history here. It
had chosen a plan fingerprint, a digest printed at plan time for the apply to
check against its own fresh plan, and
[#878](https://github.com/INTENTIUS/choudoufu/issues/878) shipped the
comparison instead, between the two plans rather than between two digests, so
that a refusal can name the address and the attribute that moved.
[Claim 15]({{< relref "/docs/claims#claim-15-apply-exactly-what-was-approved" >}})
is the runnable version, and the gauntlet's `plan_approval` stage measures both
halves - the matched file that applies, the moved world that refuses - on every
estate: see [the stage table]({{< relref "/docs/progress#the-stages" >}}).
