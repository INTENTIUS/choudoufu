# Cross-estate values

This file answers issue #62. "Split into independent estates" is the
forwarding advice both for an estate that has grown too large (#52) and for
a module that has to leave. As of issue #59's phases 1-2, that is no
longer most modules - a static module tree or a statically-keyed `for_each`
module binds in place, same as the root - but a `count`-expanded module
block is refused permanently and still has to leave, and a genuinely
independent module may still be split out by choice rather than necessity
(#59 phase 3). Once that split happens, the two estates need a way to
share values: a network estate's VPC ID, an IAM estate's role ARN.
`terraform_remote_state` is banned, correctly, because a live-markers run
has no state file for it to read
(`internal/live/lint/lint.go`'s `checkDataResources`, `live/LIMITATIONS.md`'s
"remote-state" entry). This file is the answer to what replaces the
output-passing it used to provide.

## The decision

Read the producer's own live resource with a data source of its own type.
No new construct, no new namespace, and no new lint rule. This was already
the lint refusal's forwarding address in prose; this file makes it the
normative spec, the way `live/RECEIPTS.md` did for receipts.

A first-class "estate output" surface was considered and declined: outputs
written to `aws_ssm_parameter`s under an estate namespace, the receipts
pattern's machinery pointed at outputs instead of effects, read back by an
ordinary `aws_ssm_parameter` data source. See "Why not outputs-as-receipts"
below for why.

## The pattern

A consumer estate declares a data source of the producer's resource type
and filters it down to exactly one live resource. The recommended filter is
the producer's own marker tags (`live/MARKERS.md`), `tofu-estate` and
`tofu-address`, rather than a value invented for this purpose. Both tags
are already written on every managed resource for free, and the pair is
already unique within an account by construction: an address is unique
within its own estate, and an estate name is unique across the account, so
no new naming convention is needed for a consumer to learn.

```hcl
# In the consumer estate, reading a VPC a separate "network" estate owns:
data "aws_vpc" "network" {
  filter {
    name   = "tag:tofu-estate"
    values = ["network"]
  }
  filter {
    name   = "tag:tofu-address"
    values = ["aws_vpc.main"]
  }
}

resource "aws_subnet" "app" {
  vpc_id     = data.aws_vpc.network.id
  cidr_block = "10.0.1.0/24"
}
```

Where a type's data source offers no tag filter, an ARN-identity type, or
one whose list schema has no filter argument (`live/SURVEY.md` and
`live/LIMITATIONS.md`'s "Emulator-blocked"/registry sections name several),
fall back to whatever client-assigned identity that type's data source does
expose: a name, a bucket, an ARN built from a name the consumer already
knows. The point is the same either way. The consumer reads the producer's
live resource through the provider's own read contract for that type,
never through a side channel this mode maintains on the producer's behalf.

This is exactly the "read the live resource with a data source of its own
type" half of `checkDataResources`'s refusal message
(`internal/live/lint/lint.go`). This file is what that half now points to
by name, rather than being a bare sentence with no further spec behind it.

## Why not outputs-as-receipts

The receipts pattern (`live/RECEIPTS.md`) exists for one specific case: "an
effect that has no queryable live state of its own." A migration changes
rows, not a resource an API can list; a cache purge changes what a CDN
serves, not a record OpenTofu can read back. A receipt is memory
manufactured for a fact the live system genuinely cannot answer.

An estate's outputs are the opposite case. A VPC ID, a role ARN, a bucket
name: every one of these already lives on a real, queryable resource that a
data source of its own type reads correctly and current, on every plan,
with no memory at all. Pointing the receipts machinery at outputs would
build memory for a fact the live system already answers, which is
precisely what `live/LIMITATIONS.md`'s recurring test names: "every banned
feature exists to maintain or repair the store. That is the test for edge
cases." An SSM-parameter mirror of a live attribute is a store by that
test, even though it is shaped like a receipt.

Three concrete problems follow from treating it as one anyway.

1. **It fails the leaf rule by design.** `RuleReceiptLeaf` (`live/
   RECEIPTS.md`'s Guard 4) keeps a receipt something nothing depends on, so
   that losing it costs one idempotent re-run and never a wrong plan
   elsewhere. An "output" is only useful if other estates *do* depend on
   it; that is the entire ask in issue #62. Pointing the receipts machinery
   at outputs means either breaking the leaf rule for this one flavor of
   parameter, which unravels the property that makes every other receipt
   safe to lose, or building a second, parallel set of rules that looks
   like a receipt but obeys the opposite constraint. The second option is
   new machinery wearing a receipt's clothes, not the "no new resource
   kinds" this option was supposed to buy.
2. **It is a derivative copy, the exact thing Guard 2 warns against.**
   RECEIPTS.md's Guard 2 rejects hashing raw inputs partly because a
   receipt "keeps [itself] from becoming a second copy of configuration
   data that now has to be kept in sync with the first." An SSM parameter
   mirroring a VPC ID is precisely that second copy. Every producer apply
   has to remember to keep the mirror current, and every consumer plan now
   trusts a value that can go stale relative to the resource it mirrors,
   the exact risk profile removing the state file was meant to retire
   (`live/FAQ.md`, "Why would I want this?": "Every plan re-reads the live
   system, so a stale or missing record costs one re-read, never a wrong
   plan"). A data source reading the producer's resource directly cannot go
   stale this way, because there is nothing between the read and the
   value: the read *is* the value.
3. **It does not buy the stability it is sold on.** The case for a
   first-class output surface is that it "makes the producer's contract
   explicit and stable across producer refactors." But a producer refactor
   that changes a resource's live identity (replacing a VPC, splitting one
   bucket into two) changes what a consumer sees whether the consumer reads
   the resource directly or through a mirrored SSM parameter. The mirror
   does not insulate the consumer from the refactor. It only adds a second
   place the refactor has to remember to update, and a forgotten update
   there is a silent, consumer-visible staleness bug that reading the
   resource directly cannot produce.

Weighed against the resource cost of a first-class output surface, a
namespace convention, a naming spec, and (per the issue) lint support to
keep it statically recognizable, plain data sources cost nothing to build,
cannot drift from what the producer actually holds, and are already the
ordinary way Terraform practitioners read another workspace's resources
when they are not using a backend at all. The cheaper option here is also
the one that delivers the stability the expensive option only promises.

## Demonstrated

`internal/live/lifecycle/cross_estate_live_test.go`'s
`TestCrossEstateDataSourceAgainstFloci` is the live proof: two independent
estates, two independent `choudoufu apply` runs, no state file at any
point. A producer estate creates a VPC. A consumer estate's `aws_vpc` data
source, filtered on the producer's own marker tags rather than
`terraform_remote_state`, resolves to that VPC's real ID, and a subnet
created from it lands inside the producer's real VPC, confirmed by reading
it back independently with the AWS CLI. That readback is the actual
value-flow claim, not merely a plan that parses.

```
TF_FLOCI_TEST=1 go test ./internal/live/lifecycle/ -run TestCrossEstateDataSourceAgainstFloci -v
```
