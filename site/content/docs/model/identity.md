---
title: "Identity"
weight: 2
---

# Identity

Which real resource a configuration address refers to. AWS already knows this,
once you tell it.

![How a plan binds a configuration address to a live resource](diagram-identity.svg)

Two tags, written as the resource is created.

```
tofu-estate  = prod-networking
tofu-address = aws_vpc.main
```

That pair is the entire ownership contract. Any tool that can write two tags
can adopt a resource. Any tool that can read them can tell you what an estate
contains.

## Where the marker comes from

The stamp pass writes `tofu-estate` and `tofu-address` into the resource's own
`tags` argument, so the plan renders them and the apply sends them like any
other tag you declared.

For most types the AWS provider carries tags on the create call itself, which
means a create that succeeds carries its marker already. Where a service cannot
tag on create, the provider tags immediately after, and a crash in that window
leaves a resource nothing can bind to. That case is what
[adoption]({{< relref "/docs/use/migrate" >}}) exists for.

## Recovering an address

Two paths, decided by who chose the identity.

**The configuration named it.** An S3 bucket, an IAM role, a log group. The
name is already in your code, so nothing has to go looking. The tag confirms
ownership rather than establishing it.

**AWS assigned it.** A VPC, a subnet, a security group. Nothing in the
configuration names the live object, so the tag is the only way back. Discovery
lists by tag and reads the address off what comes back.

The second path is why identity arguments must be computable before a provider
runs. The next section states the rule in full.

## The static-evaluability rule

Every `count`, every `for_each`, and every identity-bearing argument must be
computable from `var`, `local`, `path` and `terraform` alone, plus functions
over those.

No data sources. No module outputs. No attributes of other resources.

Markers are written before anything is created, and a marker names which
configuration address a live resource belongs to. If the set of instances is
unknowable until a provider has been called, there is no marker to write.

This is the rule behind what [Compatibility
reference]({{< relref "/docs/use/compatibility" >}}) still refuses, and it is
narrower than "the value is not written in the configuration text". Two
phases run ahead of resolution and feed it: data sources are read before
anything resolves, so a `count` or `for_each` over one expands normally, and
a second pass can answer a reference to a genuinely computed attribute of a
sibling from what the cloud holds. What stops is an expansion or an identity
that no phase can settle before a marker has to be written - a module output
read in a `count` or `for_each`, a `for_each` key that is a parent's live ID,
or a `count.index` two instances render identically.

`count` on a module call is **not** one of them. It is admitted when the
count is statically evaluable and the call's own arguments use `count.index`
only where this fork can prove two instances cannot render the same value -
the same test one paragraph up. Then every resource inside is addressed by
the call's instance key, `module.app[0].aws_x.y` binds exactly as soundly as
`module.app.aws_x.y` does, and the fork stamps that marker for you rather
than leaving you to write it.

The premise this page used to state, that `count` renumbers every address
beneath it, is false for the shape OpenTofu actually produces: shrinking a
`count` retires the highest index and never renumbers a survivor, so an
integer module-instance key is as stable an address component as a resource's
own count key.

## Untaggable is not unidentifiable

About half the AWS provider's resource types carry no `tags` argument at all:
**852 of 1699** at provider 6.59.0, counted from `live/readiness.json`'s
`facts.taggable` at commit `cfd0dc58d4`. None of them can hold a marker. This
gets read as a coverage hole, and it is not one.

An untaggable resource's address is composed rather than looked up.
`aws_iam_role_policy` is a role name and a policy name.
`aws_iam_role_policy_attachment` is the two things it attaches.
`aws_route53_record` is a zone, a name and a type. Every part comes from your
configuration or from a parent that does carry a marker, so the address
resolves identically on every run with nothing stored anywhere. That is what
the [declaration-carried tier]({{< relref "/docs/use/resource-tiers" >}})
names.

They are not a rounding error. On a generated estate shaped like one that had
grown organically, the untaggable share of *instances* was 41 of 79, 164 of
301 and 410 of 745 at three sizes: 52%, 54% and 55%, all three of them made
up entirely of the types named above. A realistic estate is roughly half
resources that hold a marker and half resources that derive their identity
from one.

What untaggability does bound is governance, not identity. An
`aws:ResourceTag` condition has nothing to match on a resource with no tags,
so a grant covering those types is wider than its condition says.
[Where AWS honours the condition]({{< relref "/docs/governance/reach" >}})
has that limit, and
[`live/MARKERS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/MARKERS.md)
has the per-service breakdown. Being identifiable without a tag and being
governable by one are different properties, and only the second one is
missing here.

One thing does follow on the identity side, and it is worth stating so this
section is not read as "nothing changes": a marker proves the resource
carrying it exists. It says nothing about a child derived from it. Existence
of an untaggable resource is settled by reading it, never by reading its
parent's tag.

## Renaming

Rename the block, then rewrite the tag.

```
choudoufu live-mv aws_vpc.old aws_vpc.new
```

The tag write is the move. `moved` blocks have nothing to act on and are
refused.

## Stripping a marker

Nothing in choudoufu prevents it. The tags live in your account and your
account's access controls protect them. A stripped marker hides the resource
from the next plan, which proposes a replacement beside it.

[`live/MARKERS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/MARKERS.md)
is the normative spec. It covers the escaping rule, continuation tags, the
rename rule, and which protections were tested rather than assumed.
