---
title: "How to deny creating anything unowned"
weight: 5
---

# How to deny creating anything unowned

Tag compliance is normally retroactive. A scanner finds untagged resources, a
ticket asks someone to explain them, and the backlog never empties because new
ones arrive faster than old ones are resolved.

Conditioning creation inverts it. Ownership stops being something a resource
acquires later and becomes a precondition of existing.

## The policy

As a service control policy, applied to the accounts that hold estates.

```json
{
  "Sid": "NothingIsCreatedUnowned",
  "Effect": "Deny",
  "Action": ["ec2:RunInstances", "ec2:CreateVolume"],
  "Resource": "*",
  "Condition": {
    "Null": {"aws:RequestTag/tofu-estate": "true"}
  }
}
```

`Null` with `true` matches when the key is absent, so a create that supplies no
estate tag is denied. An account under this policy cannot accumulate resources
nobody can account for.

## Check which types can tag on create first

This is the bound that decides whether the policy is usable, and it is worth
establishing before you write it.

The stamp pass writes `tofu-estate` and `tofu-address` into the resource's own
`tags` argument. For most types the AWS provider carries tags on the create
call, which is what puts `aws:RequestTag` in the request for this condition to
read. Where a service cannot tag on create, the provider tags immediately
afterwards; the key is then absent from the create call, and this Deny stops a
legitimate one.

Name the actions you have confirmed. Do not reach for a wildcard and find out
in production.

## It governs more than choudoufu

The policy conditions the create call rather than the tool, so it applies to
anything with credentials - the console, the CLI, another pipeline. That is
the point. A resource created by hand either carries an estate tag or does not
get created.

It also means the tag is worth something as evidence. A resource carrying
`tofu-estate` under this policy was claimed at birth rather than labelled
afterwards by whoever ran the scanner.

## Where it stops

`aws:RequestTag` is a different key from `aws:ResourceTag` and services support
them independently, so confirm this one specifically.
[Where AWS honours the condition]({{< relref "/docs/governance/reach" >}}) covers the reach of both.

A service control policy has no effect on the organization's management
account, or on any principal outside the organization.
