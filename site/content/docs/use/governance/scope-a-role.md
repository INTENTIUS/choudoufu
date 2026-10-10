---
title: "How to scope a role to an estate"
weight: 2
---

# How to scope a role to an estate

## The whole estate

Creating and mutating are conditioned by different keys, and tagging is
both.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "MutateOnlyThisEstate",
      "Effect": "Allow",
      "Action": ["ec2:CreateTags", "ec2:DeleteTags", "ec2:TerminateInstances"],
      "Resource": "*",
      "Condition": {
        "StringEquals": {"aws:ResourceTag/tofu-estate": "prod-networking"},
        "StringEqualsIfExists": {"aws:RequestTag/tofu-estate": "prod-networking"}
      }
    },
    {
      "Sid": "CreateOnlyIntoThisEstate",
      "Effect": "Allow",
      "Action": ["ec2:RunInstances"],
      "Resource": "*",
      "Condition": {
        "StringEquals": {"aws:RequestTag/tofu-estate": "prod-networking"}
      }
    },
    {
      "Sid": "TagOnlyAsPartOfThatCreate",
      "Effect": "Allow",
      "Action": ["ec2:CreateTags"],
      "Resource": "*",
      "Condition": {
        "StringEquals": {
          "aws:RequestTag/tofu-estate": "prod-networking",
          "ec2:CreateAction": "RunInstances"
        }
      }
    }
  ]
}
```

`aws:ResourceTag` governs what the estate already owns; a create has no
resource yet, so the second statement conditions on `aws:RequestTag`. The
first statement's `aws:RequestTag` line stops a retag into another estate,
and the third's `ec2:CreateAction` stops the role stamping its estate onto
resources it did not create.
[live/MARKERS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/MARKERS.md#granting-an-estate)
has the reasoning, and the grant for a type tagged after its create and for
a marker removed by `DeleteTags`.

The actions are illustrative and all EC2. The tagging action differs by
service; [Marker stamping]({{< relref "/docs/use/reference#marker-stamping" >}})
has the generated per-service table, and the services on which a run cannot
stamp a marker at all.

## Part of an estate

One substitution. Condition on `aws:ResourceTag/tofu-address`
instead, and the grant covers named addresses rather than the whole estate.

```json
"Condition": {
  "StringLike": {"aws:ResourceTag/tofu-address": ["module.app.*", "module.app:*"]}
}
```

`aws:RequestTag/tofu-address` is the matching create grant, giving
a principal the right to create one declared address and nothing else.

[Claim 13]({{< relref "/docs/claims/the-tag-is-the-boundary" >}}) runs
the change half: in one estate the role changes `module.app` and AWS
refuses it on `module.db`. `module.app:*` covers a keyed module call;
[live/MARKERS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/MARKERS.md#what-this-grant-cannot-reach)
has the limits.

## Across estates

A role holding parts of several estates is one policy with several
statements, or one statement whose condition names several values. No state
file is shared, and no estate is split to make it possible.

```json
"Condition": {
  "StringEquals": {
    "aws:ResourceTag/tofu-estate": ["prod-networking", "prod-data"]
  }
}
```

## Read without write

Listing what an estate contains is a tagging API call, so an auditor or an
incident responder needs no `choudoufu` binary and no write access.

```sh
aws resourcegroupstaggingapi get-resources \
  --tag-filters Key=tofu-estate,Values=prod-networking
```

Grant `tag:GetResources` plus the read actions for the services
involved. The estate is legible to them and unchangeable by them.

## Handover and splitting

Handover is attaching the policy to the receiving role and detaching it from
the sending one: two IAM changes, no tag writes. Splitting is a rewrite of
`tofu-estate` on the resources leaving, plus a copy of the policy under the
new name. `choudoufu live-mv -from-estate=<old> <address> <address>`, run in
the new estate's configuration after the block moves there, makes that tag
write, with its refusals in
[How to rename a resource]({{< relref "/docs/use/rename-a-resource#moving-a-resource-to-another-estate" >}});
its role needs both estate names in its `aws:RequestTag` condition.
[live/MARKERS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/MARKERS.md#granting-an-estate)
has both in full.
