# Cross-estate values

This file answers issue #62. "Split into independent estates" is the
forwarding advice both for an estate that has grown too large (#52) and for
a module that has to leave. As of issue #59's phases 1-2 that is no longer
most modules. A static module tree or a statically-keyed `for_each` module
binds in place, same as the root, while a `count`-expanded module block is
refused permanently and still has to leave. A genuinely independent module
may also be split out by choice rather than necessity (#59 phase 3). Once
that split happens, the two estates need a way to share values: a network
estate's VPC ID, an IAM estate's role ARN.

`terraform_remote_state` is banned because it reads a state file, and prior
state here is a projection rebuilt from the live system on every run
(`internal/live/lint/lint.go`'s `checkDataResources`, `live/LIMITATIONS.md`'s
"remote-state" entry). This file is what replaces the output-passing it used
to provide.

## The decision

Read the producer's live resource with a data source of its own type,
with no new construct, namespace, or lint rule. This was already the lint
refusal's forwarding advice in prose. This file makes it the normative
spec, the way `live/RECEIPTS.md` did for receipts.

A dedicated "estate output" surface was considered and declined: outputs
written to `aws_ssm_parameter`s under an estate namespace, the receipts
pattern's machinery pointed at outputs instead of effects, read back by an
ordinary `aws_ssm_parameter` data source. See "Why not outputs-as-receipts"
below for why. Reading the output values an estate already records is a
narrower question, answered in "What an output read is for".

## The pattern

A consumer estate declares a data source of the producer's resource type
and filters it down to exactly one live resource. The recommended filter is
the producer's marker tags (`live/MARKERS.md`), `tofu-estate` and
`tofu-address`. Both tags are already written on every managed resource
for free, and the pair is already unique within an account: an address is
unique within its estate, and an estate name is unique across the
account, so a consumer needs no new naming convention.

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
knows. Either way the consumer reads the producer's live resource
through the provider's read contract for that type, not through a side
channel this mode maintains on the producer's behalf.

This is the "read the live resource with a data source of its own
type" half of `checkDataResources`'s refusal message
(`internal/live/lint/lint.go`). This file is the spec that half now
points to by name.

## Why not outputs-as-receipts

The receipts pattern (`live/RECEIPTS.md`) exists for one specific case: "an
effect that has no queryable live state of its own." A migration changes
rows, not a resource an API can list. A cache purge changes what a CDN
serves, not a record OpenTofu can read back. A receipt is memory
manufactured for a fact the live system cannot answer.

Most of an estate's outputs are the opposite case. A VPC ID, a role ARN, a
bucket name: every one of these already lives on a real, queryable resource
that a data source of its own type reads correctly and current, on every
plan, with no memory at all. Pointing the receipts machinery at outputs would
build memory for a fact the live system already answers, which is
what `live/LIMITATIONS.md`'s recurring test names: "every banned
feature exists to maintain or repair the store. That is the test for edge
cases." An SSM-parameter mirror of a live attribute is a store by that
test, even though it looks like a receipt.

Three concrete problems follow from treating it as one anyway.

1. **It cannot satisfy the leaf rule.** `RuleReceiptLeaf` (`live/
   RECEIPTS.md`'s Guard 4) keeps a receipt something nothing depends on, so
   that losing it costs one idempotent re-run and never a wrong plan
   elsewhere. An "output" is only useful if other estates *do* depend on
   it, which is the entire ask in issue #62. Pointing the receipts machinery
   at outputs means either breaking the leaf rule for this one flavor of
   parameter, which unravels the property that makes every other receipt
   safe to lose, or building a second, parallel set of rules that looks
   like a receipt but obeys the opposite constraint. The second option is
   new machinery that only resembles a receipt, and it gives up the "no
   new resource kinds" simplicity this option was supposed to buy.
2. **It is a derivative copy, which Guard 2 warns against.**
   RECEIPTS.md's Guard 2 rejects hashing raw inputs partly because a
   receipt "keeps [itself] from becoming a second copy of configuration
   data that now has to be kept in sync with the first." An SSM parameter
   mirroring a VPC ID is that second copy. Every producer apply
   has to remember to keep the mirror current, and every consumer plan now
   trusts a value that can go stale relative to the resource it mirrors.
   That is the risk a projection rebuilt every run avoids, per the docs
   site's "Where does the state live?", where a stale or missing projection
   "costs a re-read, never a wrong plan". A data source reading the
   producer's resource directly cannot go stale this way, because there is
   nothing between the read and the value.
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

A dedicated output surface costs a namespace convention, a naming spec, and
(per the issue) lint support to keep it statically recognizable. Plain data
sources cost nothing to build, cannot drift from what the producer holds,
and are already how Terraform practitioners read another workspace's
resources without a backend. The cheaper option also delivers the stability
the expensive one promises.

## What an output read is for

Issue #1371 asks what reading another estate's recorded outputs gives that
a data source does not. The rule above stays. If a live resource holds the
value, read the live resource. A corpus survey on 2026-09-19 says that
covers most of what estates pass to each other. It matched 81 cross-estate
reads to a producer output, loosely, by name. Of those, 39 were one
attribute a data source returns. Another 32 were lists or built strings a
consumer can rebuild from tag-filtered data sources.

The other 10 had no live resource behind them, and these are what an output
read is for. About 25 of the 311 root outputs of deployments in the corpus
are like this. There are three kinds:

- A value the producer chose. The `cluster-infrastructure` root in
  govuk-infrastructure outputs the literal `"cluster-services"` as
  `cluster_services_namespace`. Its `cluster-services` root reads that
  through `tfe_outputs`.
- The result of running something. In govuk-aws, `app-publishing-amazonmq`
  outputs the decoded result of a Lambda invocation.
- A value held by a system the consumer has no provider for. simpleinfra's
  `fastly-tls-subscription` outputs a Fastly configuration ID.

Without an output read the consumer copies the value into its own
configuration, and nothing keeps that copy true.

A second, smaller gain is the grant. A data-source read needs describe
permission on the producer's service, which often covers every resource of
that type in the account. The output read needs `s3:GetObject` on one
producer's `tofu-outputs/` prefix and shows only what that producer
published. This matters only when the consumer does not already use that
service.

None of this reopens the objection above, which declined building a mirror
of live attributes. Every estate already records its root output values
under `tofu-outputs/<estate>/` for its own plan to read. The only question
is whether another estate may read them.

Staleness still rules out a value that mirrors a live attribute. The record
is as of the producer's last apply and does not move when someone changes
the resource out of band, so those values stay on data sources. A value that
comes from the producer's configuration can change only when the producer
applies, so its record is as current as the value can be.

If the read is built, #1371 carries the checklist. The dependency is
declared in configuration, so that the IAM grant and the declaration name
the same estate. A missing grant refuses and names the other estate. That
rules out reusing `ReadRootOutputValues`, which logs and skips every read
error. What crosses is what is written: root outputs that are non-sensitive
and wholly known. The consumer's plan says the value is as of the producer's
last apply.

An output read is never for a value a data source can read.

## Demonstrated

`internal/live/lifecycle/cross_estate_live_test.go`'s
`TestCrossEstateDataSourceAgainstFloci` is the live proof: two independent
estates, two independent `choudoufu apply` runs, no state file at any
point. A producer estate creates a VPC. A consumer estate's `aws_vpc` data
source, filtered on the producer's marker tags rather than
`terraform_remote_state`, resolves to that VPC's real ID, and a subnet
created from it ends up inside the producer's real VPC, confirmed by
reading it back independently with the AWS CLI. The readback, rather than
the plan alone, is what verifies the value flow.

```
TF_FLOCI_TEST=1 go test ./internal/live/lifecycle/ -run TestCrossEstateDataSourceAgainstFloci -v
```
