---
title: choudoufu
type: docs
---

# choudoufu ![](choudoufu-inline-64.png)

**OpenTofu plus identity hooks.** Each resource carries its own identity in
the cloud, as two AWS tags. The apply writes them and the next plan reads them
back live. The state file is therefore a cache you are allowed to lose, and
the IAM you already run decides who may read or change what.

Everything outside those hooks is stock OpenTofu. This fork is experimental,
and it supports AWS only.

## Start here

### [I have a Terraform or OpenTofu estate to migrate]({{< relref "/docs/use/migrate" >}})

Your configuration stays where it is. Adoption stamps the two tags onto the
live estate, and a reorganisation afterwards becomes a tag rename instead of a
state move. Read this before your first apply. Nothing binds a live resource
to your configuration until its markers are on it, so a plan run too early
will propose a duplicate.

### [I am starting fresh with choudoufu]({{< relref "/docs/use/start" >}})

For an estate with nothing in it yet, where choudoufu creates every resource
and marks it on the way. Install a binary, add a `live` block, apply.

### Not sure which one you are

Run `choudoufu live-check` in your configuration directory. It needs no
credentials and reports what this fork would admit and refuse in the code you
already have. [What it checks]({{< relref "/docs/use/check-a-config" >}}), and
the [compatibility reference]({{< relref "/docs/use/compatibility" >}}) behind it.

## Reference

[The model]({{< relref "/docs/model" >}}) explains what each hook stores and why
the three kinds are kept apart.

[Governance]({{< relref "/docs/use/governance" >}}) holds the IAM policies. They scope a
role to one estate and deny the creation of anything unowned.

[Using it]({{< relref "/docs/use" >}}) covers day-2 work such as renaming or
removing a resource.

[Progress]({{< relref "/docs/progress" >}}) tracks which real estates this fork
runs end to end, and [what you pay]({{< relref "/docs/what-you-pay" >}}) is what
it costs against the stock tool.

![a plate of choudoufu](choudoufu-hero.png)

Built on OpenTofu 1.13.0 from fork point
[`03743ce6e8`](https://github.com/opentofu/opentofu/commit/03743ce6e8).

choudoufu is an independent fork. It is not affiliated with or endorsed by
OpenTofu or the Linux Foundation. OpenTofu is a registered trademark of the
Linux Foundation. Code is licensed MPL-2.0. Upstream source lives at
[github.com/opentofu/opentofu](https://github.com/opentofu/opentofu) and all
stock docs at [opentofu.org/docs](https://opentofu.org/docs/).
