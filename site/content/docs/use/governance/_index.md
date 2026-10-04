---
title: "Scoping a role"
weight: 12
bookCollapseSection: true
---

# Scoping a role

Tag-based IAM scoping is a feature AWS already has. What it needs is tags
that are reliably there, on everything, correct. That is what markers are.

Every resource this fork creates whose type takes tags carries `tofu-estate`
and `tofu-address`, derived from its configuration address. For most types
the marker rides in the create call; a few cannot take tags at creation and
are tagged straight afterwards. That is not a convention someone has to
remember or a `default_tags` block that drifts. Its edges, which a policy
inherits, are in [Where AWS honours the
condition]({{< relref "/docs/use/governance/reach" >}}).

So scoping a role is ordinary tag conditions.
[How to scope a role to an estate]({{< relref "/docs/use/governance/scope-a-role" >}})
has the policies themselves. They are written from AWS's documentation and
not run by this project, except one shape:
[claim 13]({{< relref "/docs/claims/the-tag-is-the-boundary" >}}) fences
`ec2:CreateTags`, `ec2:DeleteTags` and `ec2:TerminateInstances` between two
roles under emulator IAM enforcement, with a real-account CloudTrail record.
Test the rest against your own account.

## Three things a file cannot do

The grants above have rough equivalents in splitting a configuration. These
do not. They work because the permission unit is a cloud resource, so every IAM
feature that applies to resources now applies to your infrastructure, and IAM
has features a file has never had.

- [How to stop a staging role reaching
  production]({{< relref "/docs/use/governance/blast-radius" >}}). A staging role is denied on anything belonging to another
  estate, so the mistake fails at the cloud rather than at review.
- [How to cover every team with one policy]({{< relref "/docs/use/governance/abac" >}}). ABAC over the
  estate tag, so onboarding a team costs a session tag rather than a
  policy.
- [How to deny creating anything unowned]({{< relref "/docs/use/governance/unowned" >}}). Deny creates
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

## Protecting the markers

The grants above rest on tags, so a stripped tag is a real hazard. An AWS
Organizations tag policy cannot block a tag's removal at all. An SCP can
deny the untagging call, but only in member accounts and only where the
condition key is honored, so the last line of defense is the plan: a create
that matches an unowned live resource of its type gains a
`[POSSIBLE DUPLICATE]` warning above the plan diff, naming that resource and
the command that adopts it.
[live/MARKERS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/MARKERS.md#protecting-the-markers)
has the SCP, including the continuation keys `aws:TagKeys` needs, and the
gaps it leaves.
