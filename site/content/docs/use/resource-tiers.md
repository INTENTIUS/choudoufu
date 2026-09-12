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

Untaggable *and* server-minted: the provider mints this type's identity at
create time and the type carries no `tags` argument, so every instance
would need marker discovery to be found again and there is nowhere to write
the marker. Where the record-located mechanism already reaches a type, a
declared `record_store` holds its identity and recovers it. For the rest,
losing the record loses the object, the same way losing a stock state file
loses it under plain OpenTofu.

**Two different populations answer to this tier's name, and they differ
threefold.** The paragraph above defines
`internal/live/identity.MarkerlessTypes`, the roster derived on every
generator run from taggability and the server-assignment verdict:
**159 types**, in `internal/live/identity/markerless_generated.go`. The count
in this page's table is **471**, and the gap is not a discrepancy. It is that
`tools/readiness-gen` also lands a type here by elimination: untaggable, no
admission row yet, and a survey path ("moves to Ops" or "enumerable,
unbindable") that leaves nowhere else to put it. That is **313 further
types**, *destined* for this tier rather than record-carried today.

The arithmetic closes exactly. 158 of the roster's members are classified
here, plus 313 by elimination, is 471. The 159th, `aws_wafv2_api_key`, is
excluded by design and counted in that tier instead, so it never reaches the
classifier's markerless branch at all.

Both numbers are real and they answer different questions. **159** is how
many types the record-located mechanism is on the hook for. **471** is how
many types this page's table shows in the tier. Quoting the second where the
first is meant overstates that mechanism's population threefold, which two
issues did before it was caught. Recount either at any commit:
`live/readiness.json`'s `facts.markerless` and `tier` fields give the 158 and
the 471, and the map literal in `markerless_generated.go` gives the 159. The
counts named in this section were taken at commit `cfd0dc58d4` against
provider `hashicorp/aws` `6.59.0`.

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
own per-type rows. `in-contract` is the only status that means "usable
today" - every other one is a form of not yet, and the lookup table below
says why for each type that carries it.

{{< readiness "tiers" >}}

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
