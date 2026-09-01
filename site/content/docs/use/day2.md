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

Saved plan files are refused. Planning in a PR and applying that exact
artifact has no equivalent yet. Ordinary `apply` re-plans and re-confirms
against the live system - the honest behaviour - but nothing today
detects that the world moved between review and apply.

The design closing that gap is settled.
[#74](https://github.com/INTENTIUS/choudoufu/issues/74) chose a plan
fingerprint, a digest printed at plan time that apply checks against its own
fresh plan, refusing on mismatch. `the plan-approval design (#74)` holds the
design; it is not implemented yet.
