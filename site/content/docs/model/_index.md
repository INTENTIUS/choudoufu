---
title: "What it is"
weight: 2
bookCollapseSection: true
---

# What it is

OpenTofu keeps a state file: a list of which real resource each block in your
configuration created, plus the values it cannot read back. Whoever can write
that file can change anything in it, and losing it means OpenTofu no longer
knows what it owns.

choudoufu is OpenTofu with a different backend, the live backend. It puts
ownership on the resources themselves and keeps the rest in small records,
so there is no single file to guard, lock or lose.

![What choudoufu keeps, and where](diagram-pieces.svg)

## Four words

An **estate** is one configuration and everything it owns. You name it once,
in a file called `estate.chdf.hcl`, and the name is the unit of ownership and
of access control.

A **marker** is the estate's name written on a resource, with the address of
the block that declares it. On AWS it is two tags, `tofu-estate` and
`tofu-address`. A `count` instance whose members the configuration does not
tell apart carries a third, `tofu-slot`. On Kubernetes it is one label. The apply writes it on the
create call, so a resource that exists has one.

A **record** holds what cannot be read back from the live resource: a
password the API never returns, a random name, the fact that a script ran.
There is one per resource, in the **record store**, which is a local
directory, an S3 bucket, or Secrets in a cluster.

## 1. The marker says what you own

A plan finds your resources by their markers, reads each one live, and
compares it with your configuration. Nothing else is asked who owns what. So
any cloud tool can list an estate, and your own IAM or RBAC decides who may
change which resource, one resource at a time.

```
aws resourcegroupstaggingapi get-resources \
  --tag-filters Key=tofu-estate,Values=prod-networking
```

[Identity]({{< relref "/docs/model/identity" >}}) has the rules, including
what happens for a resource type that has no tags.

## 2. The record holds the rest

For most resources, losing the record costs a slower plan, because the marker
still owns the resource. A few resources have no live object at all, a
`random_pet` or a `null_resource`. These are called **record-backed**, and
for them the record is the only copy.

Every write to a record is conditional: it succeeds only if nobody else
changed the record first. Nothing is locked, so a crashed run leaves nothing
held. [Records]({{< relref "/docs/model/values" >}}) has the two kinds and the
three stores, and [Two runs at once]({{< relref "/docs/model/concurrency" >}})
has the races.

The record store holds what a state file holds, secrets included, so it
needs the care a state bucket needs.
[Secrets in the record store]({{< relref "/docs/use/secrets" >}}) starts
there.

## The state file is still there, as a cache

Every run writes an ordinary state file on the machine that ran. It is never
asked who owns what, and deleting it changes no plan.
[The disposable cache]({{< relref "/docs/model/cache" >}}) says what it buys.
Remove the `live` block, keep that file, and stock OpenTofu carries on with
it, which is what makes leaving cheap.

## What it costs

A plan reads the live system and not a file, so it does more work.
[What a plan costs]({{< relref "/docs/model/plan-cost" >}}) has the numbers.
A marker has to be knowable before the resource exists, which limits what a
configuration may compute in a name.
[Compatibility reference]({{< relref "/docs/use/compatibility" >}}) lists what
that rules out.

Coming from Terraform or OpenTofu?
[What changes from a state file]({{< relref "/docs/model/from-a-state-file" >}})
is the side-by-side.
