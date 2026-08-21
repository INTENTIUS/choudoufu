---
title: "Scoping a role"
weight: 1
bookCollapseSection: true
---

# Scoping a role

Tag-based IAM scoping is a feature AWS already has. What it needs is tags
that are reliably there, on everything, correct. That is what markers are.

Every resource this fork creates carries `tofu-estate` and
`tofu-address`, derived from its configuration address and written
as part of the create call. Not a convention someone has to remember, not a
`default_tags` block that drifts, and not something a resource can
be created without.

So the policies below are ordinary tag conditions. They work because the
tags underneath them are guaranteed rather than hoped for.

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

## Three things a file cannot do

The grants above have rough equivalents in splitting a configuration. These
do not. They work because the permission unit is a cloud resource, so every IAM
feature that applies to resources now applies to your infrastructure, and IAM
has features a file has never had.

- [The wrong config cannot reach
  production]({{< relref "/docs/governance/blast-radius" >}}). A staging role is denied on anything belonging to another
  estate, so the mistake fails at the cloud rather than at review.
- [One policy for every team]({{< relref "/docs/governance/abac" >}}). ABAC over the
  estate tag, so onboarding a team costs a session tag rather than a
  policy.
- [Nothing is created unowned]({{< relref "/docs/governance/unowned" >}}). Deny creates
  carrying no estate tag, and ownership becomes a precondition of existing.

## One configuration, many owners

This one has an equivalent today, and the equivalent is the problem. Two
teams sharing a state file both need write on it, so the usual answer is two
root modules with `remote_state` wired between them. Your repository
layout ends up determined by your blast radius decisions and stays that way.

Here the configuration stays one thing and the boundary is a policy. Each
team's role is scoped to its own addresses, so an apply touches only what that
team changed. Moving the boundary is an edit to a condition rather than a
restructuring.

Two things to hold. Reads stay open across the estate, because discovery has
to see everything or a plan proposes duplicates. And if a team plans a change
to something it does not own, the apply fails at that resource, which is the
right answer but is worth knowing before it happens in front of someone.

## Where the key is confirmed

{{< iamref field="named-count" >}} of {{< iamref field="services-count" >}} services in AWS's Service
Authorization Reference name `aws:ResourceTag` on their tagging
action. That is a lower bound. The reference is authoritative about what it
names and silent about what it omits, and AWS documents tag-based
authorization for services it says nothing about, Lambda among them.

{{< iamref field="named" >}}

### Services with no verdict

The reference states nothing either way for these. Check them against AWS's
own IAM documentation and test the policy. This is not a list of services where
scoping fails.

{{< iamref field="unnamed" >}}

## Types that carry no tags

A marker needs somewhere to live, and a minority of admitted types take no
`tags` argument at all. A condition on either marker key is
unmatched on those, so a grant covering them is wider than its condition.

Being identifiable without a tag and being governable by one are different
properties, and an IAM condition needs the second.
[live/MARKERS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/MARKERS.md)
carries the generated count and the per-service breakdown.

## Protecting the markers

The grants above rest on tags, so a stripped tag is a real hazard. Two AWS
Organizations mechanisms sound like they cover it. One does not, and the other
only partly.

**Tag policies enforce values, not survival.** A tag policy
checks the value a tag is set to, when a tag is written, on types the feature
supports. Nothing in it inspects a tag removal, and AWS says so directly. It
cannot be configured to block a tag from being removed. Do not rely on one for
this.

**SCPs can block the untagging call**, but only inside the
organization, only in member accounts, and only where the condition key is
honored. Denying the tag-removal actions for the marker keys, with an exception
for whichever principal runs choudoufu, is the closest thing to a real
backstop. MARKERS.md carries the policy.

Even a correct SCP leaves gaps. The management account, a standalone
account, a misused exemption, a service whose untag action does not honor
`aws:TagKeys`, or a policy nobody wrote yet. Prevention cannot cover
every case, so this fork does not rely on it alone. At plan time a create whose
type matches an unowned live resource gains a
`[POSSIBLE DUPLICATE]` warning naming that resource and the command
that adopts it instead, sitting immediately above the plan diff. That is the
guard which assumes the tags get stripped anyway.

## What a run itself needs

choudoufu makes few AWS calls of its own. Resource reads, writes and lists go
through the provider plugin, so those are the AWS provider's permissions,
exactly as any OpenTofu run. [Reference]({{< relref "/docs/use/reference" >}}) lists the
fork's own call surface per stage, which is short and fixed.

Marker stamping calls the tagging action for the resource's own service. A
role that can create a resource can usually already tag it, which matters when a
policy is scoped tightly.

The rosters on this page are rendered from
`live/iam-reference.json`, generated by `tools/iamref-gen`
from AWS's published reference. That artifact is authoritative about the
condition keys it names and silent about the ones it omits.
