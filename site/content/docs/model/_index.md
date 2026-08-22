---
title: "The three pieces"
weight: 1
bookCollapseSection: true
---

# The three pieces

Three things have to survive between runs: which live object each resource
block owns, the values the cloud cannot hold, and whether an effect has
already run. Each one lives somewhere AWS already has, and your IAM governs
each one per resource.

![Where identity, values and effects live, and who writes each](diagram-pieces.svg)

| The job | Where it goes | What reads it |
|---|---|---|
| [Identity: which live object a block owns]({{< relref "/docs/model/identity" >}}) | Two tags on the resource | Any cloud tool, and your IAM |
| [Values: what the cloud cannot hold]({{< relref "/docs/model/values" >}}) | A record store you declare | choudoufu only |
| [Effects: whether something already ran]({{< relref "/docs/model/effects" >}}) | A receipt you declare | You, your reviewers, your responder |

Everything else on this site follows from those three rows.

## What changes for you

If you are coming from a state file, this is the comparison. It is the only
place on this site that argues by contrast, because the rest describes what is
here rather than what is absent.

![The three jobs of a state file, and where each one goes](diagram-split.svg)

| | `terraform.tfstate` | under choudoufu |
|---|---|---|
| The permission unit | one file | one resource |
| Who may change the RDS but not the subnets | anyone who can write the file | whoever your IAM says |
| To narrow access | split the state | write a policy |
| A role over three estates | three files, shared | one policy |
| Handover | export, migrate, re-import | grant a role |
| What is in it | open the JSON | `aws resourcegroupstaggingapi get-resources` |

The row that matters most is the second. Every team has had the argument about
how to split their state, and the answer has always shaped the repository
rather than the system. That argument goes away when the permission boundary
stops having to match the file.

[How to scope a role to an estate]({{< relref "/docs/governance/scope-a-role" >}}) has the policies,
and [where AWS honours the condition]({{< relref "/docs/governance/reach" >}}) has the two limits that
decide whether this works for your estate.

## Why they are separate

The three have nothing in common except that they all have to persist. Keeping
them together is what turns persistence into a permission boundary, a secret,
and a thing to lock.

Identity is the only one that must be authoritative, and it is the one AWS can
already answer. Because ownership rides on the resources, a projection of it is
allowed to go stale. Rebuilding one costs a read.

Values and effects stay small. Most estates declare no record store at all.

## What this buys

**Your IAM governs your state.** Reading ownership is a tagging API call.
Reading a record is a `GetParameter`. Both authorize per resource through
policies you already run. [How to scope a role to an estate]({{< relref "/docs/governance/scope-a-role" >}}) has the mechanism,
and [where AWS honours the condition]({{< relref "/docs/governance/reach" >}}) has its limits.

**Handover is granting a role.** No export, no migration, no file to move.

**An estate is legible without the tool.** Whoever inherits one can list what
they got with any cloud tool before running anything.

```
aws resourcegroupstaggingapi get-resources \
  --tag-filters Key=tofu-estate,Values=prod-networking
```

## What it costs

Prior state is rebuilt by reading the live system, so a plan does more work
than reading a file. Identity must be knowable before anything is created,
which bounds what a configuration may compute.
[Identity]({{< relref "/docs/model/identity" >}}) states the rule, and
[Compatibility reference]({{< relref "/docs/use/compatibility" >}}) lists
what that rules out.
