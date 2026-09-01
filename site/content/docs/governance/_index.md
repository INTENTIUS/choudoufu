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
as part of the create call. That is not a convention someone has to remember
or a `default_tags` block that drifts; a resource cannot be created without
it.

So scoping a role is ordinary tag conditions, once the tags underneath them
are guaranteed rather than hoped for.
[How to scope a role to an estate]({{< relref "/docs/governance/scope-a-role" >}})
has the policies themselves.

## Three things a file cannot do

The grants above have rough equivalents in splitting a configuration. These
do not. They work because the permission unit is a cloud resource, so every IAM
feature that applies to resources now applies to your infrastructure, and IAM
has features a file has never had.

- [How to stop a staging role reaching
  production]({{< relref "/docs/governance/blast-radius" >}}). A staging role is denied on anything belonging to another
  estate, so the mistake fails at the cloud rather than at review.
- [How to cover every team with one policy]({{< relref "/docs/governance/abac" >}}). ABAC over the
  estate tag, so onboarding a team costs a session tag rather than a
  policy.
- [How to deny creating anything unowned]({{< relref "/docs/governance/unowned" >}}). Deny creates
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

The grants above rest on tags, so a stripped tag is a real hazard. Two AWS
Organizations mechanisms sound like they cover it. One does not, and the other
only partly.

**Tag policies enforce values, not survival.** A tag policy
checks the value a tag is set to, when a tag is written, on types the feature
supports. Nothing in it inspects a tag removal, and AWS says so directly. It
cannot be configured to block a tag from being removed. Do not rely on one for
this.

**SCPs can block the untagging call**, but the block holds only in the
organization's member accounts and only where the condition key is
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
