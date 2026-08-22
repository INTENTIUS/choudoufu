---
title: "How to scope a role to an estate"
weight: 2
---

# How to scope a role to an estate

## The whole estate

Two statements, because creating and mutating are conditioned by different
keys.

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
        "StringEquals": {"aws:ResourceTag/tofu-estate": "prod-networking"}
      }
    },
    {
      "Sid": "CreateOnlyIntoThisEstate",
      "Effect": "Allow",
      "Action": ["ec2:RunInstances", "ec2:CreateTags"],
      "Resource": "*",
      "Condition": {
        "StringEquals": {"aws:RequestTag/tofu-estate": "prod-networking"}
      }
    }
  ]
}
```

`aws:ResourceTag` reads a tag off a resource that already exists,
so it governs everything the estate acts on. It cannot govern a create, because
no resource exists yet to carry the tag. What the creating principal supplies is
`aws:RequestTag`, and conditioning on that is what makes the second
statement a grant to create into this estate rather than a grant to create
anything.

The actions above are illustrative. A real grant names the actions your own
types need.

## Part of an estate

One substitution. Condition on `aws:ResourceTag/tofu-address`
instead, and the grant covers named addresses rather than the whole estate.

```json
"Condition": {
  "StringLike": {"aws:ResourceTag/tofu-address": "aws_subnet.*"}
}
```

`aws:RequestTag/tofu-address` is the matching create grant, giving
a principal the right to create one declared address and nothing else.

Both keys are ordinary resource tags, which is the whole reason the
substitution works. Nothing new is configured, and there is no second
permission model to keep in step with your IAM.

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

## Handover

Attach the policy to the receiving role and detach it from the sending one.
Two IAM changes and no tag writes. Nothing about the resources changes, no
state is exported, and the two roles never both hold it unless you want an
overlap.

The receiving team can list what it inherited before running anything.

## Splitting an estate

Rewrite `tofu-estate` on the resources that are leaving, then copy
the policy with the new estate name. The split is a tag write and a policy
copy. Neither half moves.
