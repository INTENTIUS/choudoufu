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

This is the rule behind most of what [Compatibility
reference]({{< relref "/docs/use/compatibility" >}}) refuses: a `for_each`
over a data source, a `count.index` in a resource name, an identity argument
read from another resource's attribute. Each is a different way of asking an
address to resolve before a plan knows what it is naming.

The same rule is why **`count` on a module call is refused permanently**,
where a keyed `for_each` is not. `count` renumbers every address inside the
module on any insertion or removal above the changed index: removing element
zero turns `module.app[1]` into `module.app[0]`, silently pointing every
marker beneath at the wrong live resource. A marker records an address, not a
position, so no future work closes this. Rewrite as a keyed `for_each` over
stable names, move the resources to the root module, or give the module its
own estate.

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
