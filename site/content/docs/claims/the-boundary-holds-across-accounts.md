---
title: "Claim 19: The boundary holds across accounts"
weight: 19
claim: the-boundary-holds-across-accounts
---

# Claim 19: The boundary holds across accounts

Claim 16 proves the boundary across two regions. This is the same
estate with the other axis swapped: two AWS **accounts**, one region,
one `tofu-estate` marker and one record store. It is a separate claim
because "a cross-account alias is the same mechanism as a cross-region
one" was, until this ran, a piece of reasoning - the provider
configuration is the partition key in both cases, and nothing in the
sweep, the vouch partition or the orphan classifier reads a region as
such - and reasoning is not a measurement.

The case is claim 16's, sharpened. There, two objects with one
client-chosen name were kept apart by their regions. Here they are in the
**same region**, and the only thing that tells them apart is which
account they are in. A log group named `/smoke-two-accounts/app` in
account `000000000000` and one named `/smoke-two-accounts/app` in account
`111111111111` are two distinct objects answering to one import
identity, and evidence about one of them says nothing about the other.
The emulator says so itself: applying both blocks against a single
account fails the second create with
`ResourceAlreadyExistsException: The specified log group already exists`,
and against two accounts both succeed.

Second credentials are all it takes to write this configuration. The
account id is the access key id - the pinned emulator reads a 12-digit
access key id as the account itself, and resolves an `sts:AssumeRole`
session into another account's role the same way - so the two provider
blocks in the fixture differ by exactly one argument, and SigV4's
credential scope then carries the account id on the wire the way it
carries the region.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke the-boundary-holds-across-accounts

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke the-boundary-holds-across-accounts and report the
"caught" line: it swaps the second provider's credential for the first
account's, so the same name in the other account is the only live
evidence for the deleted instance, and the run must fail to name it.
```

The steps as they print:

1. `two accounts, one estate` - `sts:GetCallerIdentity` under each
   credential answers with a different account id, with nothing of this
   fork in the loop. Then one apply over both provider configurations:
   two log groups with the same name in the same region and two
   different account-qualified ARNs, each carrying the same
   `tofu-estate` and its own `tofu-address`, and each **invisible to the
   other account's own listing** - which is what makes them a pair
   rather than one object seen twice. Each account also holds a VPC of
   its own, server-assigned, and one record store beside the module
   holds both accounts' instances. Then a `-refresh=false` plan is empty
   and the work is attributed per account by the credential each request
   was signed with, read off the wire. The estate-wide tag index is
   counted the same way, off this fork's own client's request line: one
   fetch signed as each account and none unsigned, so the second
   account's sweep is measured rather than assumed (#957).
2. `a delete in one account is seen in that account` - the log group in
   account `111111111111` is deleted with the AWS CLI. The identical
   name in account `000000000000` - same name, same region, same
   service - is untouched. The next `-refresh=false` plan must name
   `aws_cloudwatch_log_group.other_account` and nothing else, and the
   home account's own instances - the needs-discovery VPC and the
   client-named log group alike - must still be served from the cache.
   One ordinary apply puts the missing half back: one create in
   `111111111111`, nothing in `000000000000`, and the recreated object
   carries the other account's ARN.
3. `recovery is a re-run in both accounts at once` - claim 5 across two
   accounts. The state cache and the whole record store are deleted and
   the same plan runs again. It must be empty, because both accounts
   come back from what the cloud itself carries: markers for the
   server-assigned VPCs, the configuration's own names for the
   client-named log groups. The run is also required to have signed
   requests as **both** accounts, so an empty plan cannot be an empty
   plan that never looked. Had either account's half been unreachable
   with the record gone, this step would have proposed creating a
   resource that already exists.
4. `teardown` - one destroy removes exactly what the two provider
   configurations hold between them, confirmed by reading each account
   directly afterwards.

The `BREAK=1` run inverts step 2's world rather than its assertion, the
same way claim 16's does. The check that has to be load-bearing is that
the dead instance is *named*, so the control has to manufacture silence:
it strips the ownership markers from the surviving object in
`000000000000` (an unmarked sighting is the only shape the cache's
envelope-vouch arm consumes) and swaps `aws.other_account`'s credential
for the home account's, so the second pass lists the account the
surviving object lives in and sights the same client-chosen name. The
cache then serves the deleted instance - `state cache hit for
aws_cloudwatch_log_group.other_account, listed live this run, ownership
record-attested` - the plan reports it unchanged, and the scenario fails
on exactly that line.
