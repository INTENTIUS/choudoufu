---
title: "The three pieces"
weight: 2
bookCollapseSection: true
---

# The three pieces

Three things have to survive between runs: which live object each resource
block owns, what a read of that object cannot return, and whether an effect
has already run. A state file keeps all three in one place. choudoufu keeps
each where the platform can govern it.
[How it works]({{< relref "/how-it-works" >}}) is the short form of this
section, with a per-platform table on each page.

![Where identity, records and effects live, and who writes each](diagram-pieces.svg)

| The job | Where it goes | Who writes it | What reads it |
|---|---|---|---|
| [Identity: which live object a block owns]({{< relref "/docs/model/identity" >}}) | A marker on the resource: two tags on AWS, one label on Kubernetes | The apply, on the create call | Any cloud tool, and your access control |
| [Records: what a read cannot return]({{< relref "/docs/model/values" >}}) | The record store, one record per managed instance: a local directory, an S3 bucket, or Secrets in a cluster | choudoufu | choudoufu |
| [Effects: whether something already ran]({{< relref "/docs/model/effects" >}}) | A receipt, an ordinary resource you declare | You | You, your reviewers, your responder |

Identity is the only one that is authoritative about what you own, and it is
the one the platform can already answer. Because ownership rides on the
resources, everything derived from it may go stale: the record of an ordinary
resource, the [cache]({{< relref "/docs/model/cache" >}}), a projection.
Losing one costs a read and not an estate.
[The stale-state ruling (#604)](https://github.com/INTENTIUS/choudoufu/issues/604)
states that and its one hard limit: a marker's absence proves nothing, so an
entry that cannot be confirmed is re-read and never assumed.

The exception is a resource with no live object at all, a `random_pet` or a
`null_resource`. Its record is the only copy, and
[Records]({{< relref "/docs/model/values" >}}) says what that means.

## What changes for you

If you are coming from a state file, this is the comparison.

![The three jobs of a state file, and where each one goes](diagram-split.svg)

| | `terraform.tfstate` | under choudoufu |
|---|---|---|
| The permission unit | one file | one resource |
| Who may change the RDS but not the subnets | anyone who can write the file | whoever your IAM says |
| To narrow access | split the state | write a policy |
| A role over three estates | three files, shared | one policy |
| Handover | export, migrate, re-import | grant a role |
| What is in it | open the JSON | `aws resourcegroupstaggingapi get-resources` |
| Two applies at once | a lock, which a crash can orphan | a conditional write per record, and nothing held |

Every team has had the argument about how to split their state, and the
answer has always shaped the repository more than the system. That argument
goes away when the permission boundary stops having to match the file.
[How to scope a role to an estate]({{< relref "/docs/use/governance/scope-a-role" >}})
has the policies, and
[where AWS honours the condition]({{< relref "/docs/use/governance/reach" >}})
has the two limits that decide whether this works for your estate.

An estate is also legible without the tool. Whoever inherits one can list
what they got before running anything:

```
aws resourcegroupstaggingapi get-resources \
  --tag-filters Key=tofu-estate,Values=prod-networking
```

## What it costs

Prior state is rebuilt by reading the live system, so a plan does more work
than reading a file. [What a plan costs]({{< relref "/docs/model/plan-cost" >}})
has the measured numbers and which half stock pays too.

Identity must be knowable before anything is created, which bounds what a
configuration may compute. [Identity]({{< relref "/docs/model/identity" >}})
states the rule, and
[Compatibility reference]({{< relref "/docs/use/compatibility" >}}) lists what
that rules out.

The record store holds what a state file holds, secrets included, so it needs
the care a state bucket needs.
[Secrets in the record store]({{< relref "/docs/use/secrets" >}}) starts
there.
