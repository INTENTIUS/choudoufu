---
title: "Resource tier lookup"
weight: 1
---

# Resource tier lookup

"Every type stock supports is admitted" is the type-parity promise. It says
nothing about how an admitted type's identity survives the loss of anything -
a record store, a state file, the tool itself. "100% coverage for AWS" is a
claim people will eventually make about this fork, and this page is the
answer to what that claim actually covers: every one of the provider's
resource types gets exactly one of four readiness tiers, assigned by what
recovers its identity and at what cost when the strongest recovery path is
gone.

"100% coverage" has to mean "every type is classified," not "every type is
marker-carried." Roughly half the provider's resource types have no tag
surface at all, so no scheme that requires every type to reach the strongest
tier can describe AWS honestly. The full definition, in the vocabulary this
page uses, is
[`the tier definitions (#417)`](https://github.com/INTENTIUS/choudoufu/blob/main/the tier definitions (#417)).

## The four tiers

### Marker-carried

Every taggable type: the schema carries a settable top-level `tags`
argument. The tag on the object is both its identity - which configuration
address it binds to - and the governance surface an IAM condition can name.
This is the one tier where losing the record store is not a structural
loss: a lost record is rebuilt from tags where tags exist, using nothing but
the live cloud - no record, no state, no memory of a prior run.

### Declaration-carried

Untaggable types whose identity is fully supplied by the configuration
itself, or composed from a parent's live identity plus configuration data -
the classic case is a client-assigned name, or an attachment named by the
two things it attaches. No marker is ever written for a declaration-carried
instance, because there is no `tags` argument to write one into. Losing the
record store is recoverable by recomputing the same formula against the
current configuration and the parent's current identity, though an
instance derived from a parent's identity is only as recoverable as that
parent is.

### Record-carried

Untaggable and server-minted: the provider mints the identity at create
time, so nothing in configuration recovers it and there is no tag to
discover it by. A declared `record_store` recovers a type the
record-located mechanism already reaches; for the rest, losing the record
loses the object, the way losing a stock state file loses it under plain
OpenTofu.

Two populations share this tier's name. `identity.MarkerlessTypes`
(`internal/live/identity/markerless_generated.go`) is **159** types, derived
from taggability and server-assignment. This page's table counts **471**:
the 158 of those 159 that land here (the 159th, `aws_wafv2_api_key`, is
excluded by design instead) plus **313** more `tools/readiness-gen` adds by
elimination - untaggable, unadmitted, and out of any other tier's survey
path. **159** answers "how many types does the located mechanism cover";
**471** answers "how many types does this table's tier show" - two issues
quoted the second where the first was meant. Recount from
`live/readiness.json` (`facts.markerless`, `tier`) and
`markerless_generated.go`, at provider `hashicorp/aws` `6.59.0`.

### Excluded by design

Three types today - `aws_appstream_directory_config`,
`aws_ivs_playback_key_pair` and `aws_wafv2_api_key` - ruled out ahead of
whatever tier their own schema would otherwise assign: admitting them would
force this fork to persist plaintext credential material it can never read
back and verify again, independent of how recoverable the identity itself is.
No record, located or backed, is ever written for an excluded-by-design type,
so there is nothing to lose because nothing is ever kept.

## Coverage today

Every tier crossed with every status, tallied from `live/readiness.json`'s
own per-type rows, for AWS - the only provider with a survey artifact this
generator can classify against. `in-contract` is the only status that means
"usable today" - every other one is a form of not yet, and the lookup table
below says why for each type that carries it.

{{< readiness "tiers" >}}

## Kubernetes

Tier A's own test is a top-level `tags` argument, an AWS-only shape no
Kubernetes type has ever had; the label surface, `metadata.labels`, is this
substrate's marker instead, and it landed after today's Kubernetes rows were
ratified. The maintainer ruled tier A reads each substrate's own marker -
tags on AWS, labels on Kubernetes - and the four admitted Kubernetes types
(`kubernetes_cluster_role_binding`, `kubernetes_config_map`,
`kubernetes_namespace`, `kubernetes_storage_class`) confirmed against the
pinned provider schema all carry it, so the table below lists them
marker-carried and in-contract. See
[issue #1600](https://github.com/INTENTIUS/choudoufu/issues/1600) and
[issue #1630](https://github.com/INTENTIUS/choudoufu/issues/1630).

{{< readiness "kubernetes" >}}

## Look up your own resource type

Every AWS resource type this fork's provider roster knows about, tiered and
statused exactly once, generated from `live/readiness.json`. Search this
page (Ctrl+F or Cmd+F on most browsers) for the types your own configuration
declares - `aws_instance`, `aws_s3_bucket`, whatever they are - and read off
the tier, the status, and, for anything short of in-contract, why.

`pending-ratification`, `needs-separator` and `needs-evidence` are ordinary
admission debt: a future ratification batch clears them, the same way past
batches admitted `aws_nat_gateway` and `aws_cloudwatch_event_rule`.
`pending-mechanism` is a record-carried type waiting on the located-record
mechanism to reach it. `excluded` is the one status nothing clears - the
three types named above, ruled out by standing policy rather than left
pending.

{{< readiness "types" >}}
