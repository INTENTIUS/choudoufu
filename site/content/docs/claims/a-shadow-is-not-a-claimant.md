---
title: "Claim 18: A replaced object's shadow is not a second claimant"
weight: 18
claim: a-shadow-is-not-a-claimant
---

# Claim 18: A replaced object's shadow is not a second claimant

When an apply replaces a resource, the destroyed object does not stop
answering straight away. A terminated EC2 instance still comes back from
`describe-instances`, still lists in the tagging API, and still wears the
two ownership tags it was stamped with. The next plan therefore finds two
objects claiming one address, and tags alone cannot tell a corpse from a
rival.

So the apply that destroyed the object writes it down. A **tombstone** is
one entry in the address's own record naming an identity this estate's own
apply destroyed - a list of them, capped at eight per address, oldest
evicted first - and it is evidence that an object is dead, never permission
to touch one that is not: the only thing an entry can do is drop a claimant
out of a collision set, so an entry that is wrong costs a refusal and can
reach the live system through nothing.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke a-shadow-is-not-a-claimant

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke a-shadow-is-not-a-claimant and report both "caught"
lines: it puts a second genuinely running instance behind the same address
marker, and the plan must refuse rather than prune it; then it patches the
record to call a running, deposed object destroyed, and the read must
refuse that.
```

The steps as they print:

1. `stand the estate up` - one instance, one record naming it by its
   server-assigned id.
2. `force a replace at the same declared address` - `subnet_id` is
   ForceNew, so moving the instance to the other subnet destroys one
   object and creates another at the same address.
3. `the destroyed object's tags are still readable` - the plain AWS CLI,
   with no choudoufu in the loop, reports the old instance as
   `terminated` and still tagged for this estate and this address.
4. `the record says which one it destroyed` - the record file is read off
   disk: `identity.import_id` is the live object, and `tombstone` is a
   list. A second replace runs and the list grows to both destroyed ids,
   which is what makes the cap of eight a cap on a list rather than a
   flag.
5. `the shadow arm` - the plan runs with both shadows still listed. It
   exits 0, drops exactly the two identities the record names as
   destroyed, names each of them in a `Live resource displaced from the
   address it is marked for` warning that proposes nothing, and binds the
   address to the third.
6. `the honest boundary` - what a tombstone authorises, which is one
   claimant leaving a collision set and nothing else.
7. `a failed destroy leg writes no tombstone` - the other half of the
   write side. A role that may do everything except
   `ec2:TerminateInstances` is created and its fence confirmed with the
   plain CLI first. Under that role, with `create_before_destroy` on, a
   third replace creates the new instance and the platform refuses to
   terminate the old one, so the apply exits non-zero. The CLI reports the
   old instance `running`; the record file names the new one as
   `identity.import_id`, the old one under `deposed`, and the old one
   nowhere under `tombstone`, while step 4's two entries are still there.
   The next plan carries the deposed object to its destroy rather than
   pruning it. Nothing destroyed it, so nothing says it was.
8. `teardown` - the estate destroyed, the deposed object with it.

The `BREAK=1` run has two arms. At step 5 it creates a second, genuinely
running instance carrying the same estate and address markers as the
survivor, with nothing recorded as having destroyed it. The plan must exit
non-zero with `Two live resources claiming one address`, naming both live
ids. This is the arm that makes the claim load-bearing: the same shape used
to be waved through with a warning and exit 0, and a mechanism that quiets
a dead object's marker is only safe if it still refuses a live one. At step
7 it patches the record by hand to list the running, deposed instance under
`tombstone`, the entry the write side produced before #901, and the read
must catch it: an assertion that only ever reads an empty list is not
load-bearing.
