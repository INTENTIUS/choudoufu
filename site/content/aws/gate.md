---
title: "Gate"
weight: 2
description: "Your IAM is the whole permission model: conditions on the ownership tag decide who may act on what, per resource."
deeper:
  - "[How to scope a role to an estate]({{< relref \"/docs/use/governance/scope-a-role\" >}}): the policies, whole estate, part of one, across several."
  - "[Where AWS honours the condition]({{< relref \"/docs/use/governance/reach\" >}}): the two limits that decide whether this works for your estate."
  - "[Stop a staging role reaching production]({{< relref \"/docs/use/governance/blast-radius\" >}}), [one policy for every team]({{< relref \"/docs/use/governance/abac\" >}}), [deny creating anything unowned]({{< relref \"/docs/use/governance/unowned\" >}})."
  - "Claim [13]({{< relref \"/docs/claims/the-tag-is-the-boundary\" >}}): the fence holds against a plain AWS CLI call with no choudoufu in the process, on the emulator and on a real account with CloudTrail behind it."
---

# Gate

Tag-based IAM scoping is a feature AWS already has. What it needs is tags
that are reliably present and correct on everything, and that is what a
marker is: derived from the configuration address, written as part of the
create call, not a convention someone remembers or a `default_tags` block
that drifts.

So scoping a role is ordinary tag conditions. Two statements, because
creating and mutating are conditioned by different keys.

```json
{
  "Sid": "MutateOnlyThisEstate",
  "Effect": "Allow",
  "Action": ["ec2:CreateTags", "ec2:DeleteTags", "ec2:TerminateInstances"],
  "Resource": "*",
  "Condition": {"StringEquals": {"aws:ResourceTag/tofu-estate": "prod-networking"}}
}
```

`aws:ResourceTag` reads a tag off a resource that exists, so it governs
everything the estate acts on. It cannot govern a create, because nothing
exists yet to carry the tag; what the creating principal supplies is
`aws:RequestTag`, and a second statement conditioned on that is a grant to
create into this estate rather than to create anything.

## What that buys

Handover is two IAM changes: attach the policy to the receiving role, detach
it from the sending one. No export, no file to move, and the receiving team
can list what it inherited before running anything.

Splitting an estate is a tag write and a policy copy. `choudoufu live-mv
-from-estate=<old> <address>` rewrites `tofu-estate` on the resources that
are leaving; neither half moves.

An auditor or an incident responder needs no binary and no write access.
`tag:GetResources` plus the read actions for the services involved makes the
estate legible to them and unchangeable by them.

The fence binds the credential, not the binary. The same condition governs a
plain `aws ec2 create-tags` with no choudoufu anywhere in the process, and
what it lets through is not hidden from the tool either: the next plan reads
live tags, not a log of who wrote them.

## What it cannot reach

A condition on `aws:ResourceTag` governs the types that carry tags and the
actions that honour the key. About half the provider's resource types have
no tag surface; they are identified from configuration and are not governed
by this grant. And a tag policy enforces values, not survival: nothing in AWS
Organizations blocks a tag from being removed except an SCP on the untag
actions, which holds only in member accounts. The plan-time duplicate warning
is the guard that assumes the tags get stripped anyway.
