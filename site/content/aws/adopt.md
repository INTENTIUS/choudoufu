---
title: "Adopt"
weight: 1
description: "Bring resources that already run under management, one marker at a time, with no state migration."
deeper:
  - "[Migrate an existing estate]({{< relref \"/docs/use/migrate\" >}}): the three groups and the bulk path."
  - "[Start a new estate]({{< relref \"/docs/use/start\" >}}): the `live` block from a first apply."
  - "Claims [1]({{< relref \"/docs/claims/no-silent-orphans\" >}}), [5]({{< relref \"/docs/claims/recovery-is-a-rerun\" >}}), [6]({{< relref \"/docs/claims/roundtrip\" >}}) and [12]({{< relref \"/docs/claims/carve-by-retag\" >}})."
---

# Adopt

Most people arrive with an OpenTofu configuration that already manages live
AWS resources. Adoption binds those resources to that configuration by
writing two tags on each one. Until a resource carries them it is not yours,
and that is the one thing to hold in mind before the first apply.

{{< hint warning >}}
Turning markers on does not bind resources you already manage. A resource
with no marker reads as absent on the first plan, so the plan proposes a
second one beside it; applying it creates the duplicate. Run
`choudoufu plan` and read its `Adoptable` and `Unowned` sections.
{{< /hint >}}

## The bulk path

Keep the state file until the migration is done; it is the only input the
bulk path has.

```
choudoufu live-import -approve
```

That reads the stock state file once, verifies each entry against the live
object, and stamps a marker on everything that verifies. Its summary line
names what it skipped. Then delete the state file and plan: the plan should be
empty, because every resource is found again from its tags.

## What binds on its own

Three groups, and which one a resource falls into decides the work.

Already marked: a resource carrying this estate's two tags binds on the first
plan. After `live-import`, this is everything it verified.

Named by the configuration: an S3 bucket, an IAM role, a log group. The name
is in your code, so nothing has to go looking. About half the provider's types
have no tag surface, and most of those compose their identity from a
parent that does; they need no marker and get none.

Assigned by AWS: a VPC, a subnet, a security group. Nothing in the
configuration names the live object, so the tag is the only way back.

## When it goes wrong

An apply that crashes after the create call and before the marker write
leaves a resource nothing can bind to. The next plan flags a create whose
type matches an unowned live resource as a `[POSSIBLE DUPLICATE]` and names
the command that adopts it. Recovery is a re-run, never state surgery.

## Leaving

The cache is a stock-format state file. Copy it to `terraform.tfstate`,
remove the `live` block, and stock OpenTofu plans, converges and destroys
with it.
