---
title: "Claim 16: The boundary holds across provider configurations"
claim: the-boundary-holds-across-regions
---

# Claim 16: The boundary holds across provider configurations

A state file has no notion of a region: it is one ledger, and two regions
in it are two sets of rows that nothing keeps apart except the addresses
you chose. Here the boundary is real. An estate spans regions and
accounts under one `tofu-estate` marker and one record store, and every
answer a plan gives is about exactly one provider configuration - which
region it listed, which region it read, which region it is proposing to
change.

The case that makes this concrete is the one people actually write:
mirroring a client-chosen name into two regions. A log group named
`/app/logs` in `us-east-1` and a log group named `/app/logs` in
`us-west-2` are two distinct objects that answer to one import identity,
and evidence about one of them says nothing about the other. Choudoufu
got this wrong once - the cache's existence vouches were keyed by
identity alone, so the surviving region's object could vouch for a
deleted instance in the other one, and the plan reported a dead resource
as unchanged (issue #745, fixed in #837). Step 2 is that failure, made
into a claim you can run.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke the-boundary-holds-across-regions

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke the-boundary-holds-across-regions and report the
"caught" line: it points the west provider at the east region so the
same name in the other region is the only live evidence for the deleted
instance, and the run must fail to name it.
```

The steps as they print:

1. `two regions, one estate` - two aliased provider configurations,
   `aws.east` and `aws.west`, one region each. Both hold a log group
   with the same name, each holds a VPC of its own, and a single S3
   bucket is declared only under `aws.east`. The AWS CLI reads both
   regions directly and shows two distinct region-qualified ARNs for one
   name, both carrying the same estate marker and each its own address
   marker. Then a `-refresh=false` plan is empty, and the work is
   attributed per provider configuration by the region each request was
   signed for. The same stream shows the mirrored type listed once per
   region and the east-only bucket listed once across both passes,
   because S3's list is account-global.
2. `a delete in one region is seen in that region` - `aws.west`'s log
   group is deleted with the AWS CLI. `aws.east`'s identical name is
   untouched. The next `-refresh=false` plan must name
   `aws_cloudwatch_log_group.west` and nothing else, and `aws.east`'s
   own instances must still be served from the cache. One ordinary
   apply puts the missing half back - one create in `us-west-2`,
   nothing in `us-east-1`.
3. `an orphan in a region nothing declares any more` - **the unit of the
   sweep is the provider configuration.** Drop one block while `aws.west`
   is still configured for something else and the sweep still lists
   `us-west-2` and names the orphan there. Drop `aws.west`'s last
   declaration and the provider configuration goes with it: the sweep
   stops looking, and the marked objects sit in `us-west-2` with no plan
   mentioning them. Their markers still say whose they are, and putting
   a provider configuration for that region back brings them back into
   the sweep, which is what step 4 does before it destroys them.
4. `recovery is a re-run in both regions at once` - claim 5 with two
   provider configurations. The state cache and the whole record store
   are deleted and the same plan runs again. The change set must be
   identical, because both regions come back from what the cloud itself
   carries - markers for the server-assigned instances, the
   configuration's own names for the client-named ones. The coverage
   report must *not* be identical, and the scenario checks that too: with
   no record to read, each region has to be listed for markers, and a run
   reporting the same coverage would be a run that never noticed the
   files were gone.
5. `a region change is a replace: refused by default, permitted by name` -
   one VPC's `provider` moves from `aws.west` to `aws.east`. That is a
   replace. An address
   carries exactly one provider configuration in the plan graph, so the
   destroy of the object left behind in `us-west-2` cannot be planned.
   **By default the run refuses**: the plan names the live VPC, the
   region it is in, the region its address now points at, and the four
   things an operator can do about it. **With
   `strict { provider_change = "recreate" }` the plan proceeds**, and the
   same finding comes back as a warning naming the object and the fact
   that no plan will ever propose anything for it
   ([issue #906](https://github.com/INTENTIUS/choudoufu/issues/906)).
6. `teardown` - one destroy removes exactly what the two provider
   configurations hold between them.

This scenario runs the region axis. The account axis is
[claim 19](the-boundary-holds-across-accounts.md), a sibling
scenario with the same estate and the same steps, differing by account
instead of by region - the reasoning that the two are one mechanism (the
provider configuration is the partition key in both cases, and nothing in
the sweep, the vouch partition or the orphan classifier reads a region as
such) is now measured rather than asserted.

The `BREAK=1` run inverts step 2's world rather than its assertion. The
check that has to be load-bearing is that the dead instance is *named*,
and the defect's symptom was silence, so the control has to make silence
happen: it strips the ownership markers from the surviving object (an
unmarked sighting is the only shape the cache's envelope-vouch arm
consumes) and points `aws.west` at `us-east-1`, so the west pass lists
the region the surviving object lives in and sights the same name. The
cache then serves the deleted instance and the plan reports it
unchanged - and the scenario fails on exactly that line.
