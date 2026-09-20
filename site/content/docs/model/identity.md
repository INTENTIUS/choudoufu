---
title: "Identity"
weight: 2
---

# Identity

Which real resource a block in your configuration refers to. The platform
already knows, once you tell it.

![How a plan binds a configuration address to a live resource](diagram-identity.svg)

On AWS the marker is two tags, written as the resource is created.

```
tofu-estate  = prod-networking
tofu-address = aws_vpc.main
```

Any tool that can write two tags can adopt a resource, and any tool that can
read them can list an estate. A `count` set whose members the configuration
does not tell apart carries a third tag, `tofu-slot`, so that shrinking the
set removes one member and rebuilds nothing.

## Two ways back to a resource

Where your configuration names the resource, an S3 bucket or an IAM role, the
name is already in your code and the tag confirms ownership.

Where AWS assigns the id, a VPC or a subnet, nothing in your code names the
live object. The tag is the only way back, so the plan lists by tag and reads
the address off what it finds.

That second case is why a name has to be computable before the resource
exists. [Will my configuration work?]({{< relref "/docs/use/compatibility" >}})
lists what that allows and what it refuses.

## Resource types with no tags

About half the AWS provider's resource types have no `tags` argument, and
none of them needs one. An `aws_iam_role_policy` is a role name and a policy
name. An `aws_route53_record` is a zone, a name and a type. Every part comes
from your configuration or from a parent that carries a marker, so the
resource is found again on every run with nothing stored.

What a missing tag does limit is access control: an IAM condition on the tag
has nothing to match.
[Where AWS honours the condition]({{< relref "/docs/use/governance/reach" >}})
has that limit.

## Renaming and stripping

`choudoufu live-mv aws_vpc.old aws_vpc.new` rewrites the tag, and a `moved`
block works too. Nothing stops someone stripping a marker except your own
access control. A stripped resource is invisible to the next plan, which
proposes a second one beside it.

[`live/IDENTITY.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/IDENTITY.md)
has the full rule and the measured counts, and
[`live/MARKERS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/MARKERS.md)
is the marker spec.
