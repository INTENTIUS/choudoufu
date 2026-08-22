---
title: "How to stop a staging role reaching production"
weight: 3
---

# How to stop a staging role reaching production

Pointing a staging configuration at a production account is a familiar outage.
Nothing structurally prevents it today, because it is the same principal making
the same API calls either way. IAM has nothing to tell the two runs apart.

Markers give it something to tell them apart by.

## The policy

Attach this to the role your staging runs use.

```json
{
  "Sid": "NeverMutateAnotherEstate",
  "Effect": "Deny",
  "Action": [
    "ec2:TerminateInstances",
    "ec2:DeleteSubnet",
    "ec2:DeleteVpc",
    "rds:DeleteDBInstance"
  ],
  "Resource": "*",
  "Condition": {
    "StringNotEquals": {"aws:ResourceTag/tofu-estate": "staging"}
  }
}
```

A mistake now fails at the cloud rather than at review. The guardrail sits
below the tool, so it holds whatever the tool was pointed at, whatever
directory someone was standing in, and whatever the plan said.

## Keep reads out of the Deny

Discovery lists across the account to bind declared resources to live ones. A
run that cannot read reports its own resources as absent and proposes creating
them again, which is the duplicate hazard rather than a safety improvement.

Deny mutating actions. Leave `Describe`, `List` and `Get` alone.

## Untagged resources are covered too

A resource carrying no `tofu-estate` tag has no value for the condition to
compare against, which makes `StringNotEquals` true and extends the Deny to it.

That is the safe direction, and it is usually what you want, since a resource
outside every estate is not something a staging run should be deleting. Confirm
the behaviour against your own policy before relying on it.

## Where it stops

The condition is only evaluated where AWS evaluates it, and a resource can only
carry a marker if its type takes tags.
[Where AWS honours the condition]({{< relref "/docs/governance/reach" >}}) has both bounds.

This is a guardrail, not a substitute for separate accounts. It narrows what a
mistake can reach inside an account. Production in its own account remains the
stronger boundary.
