---
title: "What changes from a state file"
weight: 4
---

# What changes from a state file

![What a state file holds, and where each part goes](diagram-split.svg)

| | `terraform.tfstate` | under choudoufu |
|---|---|---|
| The permission unit | one file | one resource |
| Who may change the RDS but not the subnets | anyone who can write the file | whoever your IAM says |
| To narrow access | split the state | write a policy |
| A role over three estates | three files, shared | one policy |
| Handover | export, migrate, re-import | grant a role |
| What is in it | open the JSON | `aws resourcegroupstaggingapi get-resources` |
| Two applies at once | a lock, which a crash can orphan | a conditional write per record, and nothing held |
| Losing it | you no longer know what you own | a slower plan |

Every team has had the argument about how to split their state, and the
answer has always shaped the repository more than the system. That argument
goes away when the permission boundary stops having to match the file.
[How to scope a role to an estate]({{< relref "/docs/use/governance/scope-a-role" >}})
has the policies, and
[where AWS honours the condition]({{< relref "/docs/use/governance/reach" >}})
has the two limits that decide whether this works for your estate.

Because ownership rides on the resources, everything derived from it may go
stale: a record of an ordinary resource, the cache, a projection. Losing one
costs a read.
[The stale-state ruling (#604)](https://github.com/INTENTIUS/choudoufu/issues/604)
states that and its one hard limit: a marker's absence proves nothing, so an
entry that cannot be confirmed is re-read and never assumed.

[Migrate an existing estate]({{< relref "/docs/use/migrate" >}}) is the
procedure.
