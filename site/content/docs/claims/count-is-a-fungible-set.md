---
title: "Claim 11: A count pool is a fungible set"
weight: 11
claim: count-is-a-fungible-set
---

# Claim 11: A count pool is a fungible set

A `count` block declares a set, and stock tools treat it as a list:
instance 2 is whatever sits at index 2. Shrinking the count renumbers
the tail and rebuilds it. Where the members are genuinely interchangeable
- nothing in the configuration says which live resource is which -
choudoufu names each one with a `tofu-slot` marker instead, a stable id
minted once and never reused. The lint boundary admits `count.index` in an
identity-bearing argument only where it can prove every instance renders a
distinct value, and a block that does name its members that way needs no
slot: the configuration already says which is which. For the fungible kind,
the index is where a member sits today; the slot is what it is. So a pool
of three scales to two by removing exactly one member and rebuilding
nothing, and the survivors keep their live ids. Strip the slot from one
member where no local record names it, and the set has two rules for
naming its members, so the run refuses rather than guess.

The other kind of `count` block is the one whose members the
configuration itself names - a log group whose `name` is built from
`count.index`. There, the live resource that is instance k is the one the
configuration names, nothing is left for a slot to decide, and none is
written: those members carry `tofu-estate` and `tofu-address` and the
index in the address is what says which instance each one is. A reader
never has to consult a configuration to tell the two kinds apart, because
it can read the set: slots present, bind by slot; slots absent, bind by
`tofu-address`. That correct absence is what an operator reported as a
bug in issue #969, and the reason it read as one is that no claim step
had ever read the tag set back off a member of the second kind. Step 5
does, on both kinds at once.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke count-is-a-fungible-set

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke count-is-a-fungible-set and report the "caught"
line: it deletes the local record, strips the slot marker from one
member, and the plan must refuse the half-slotted set by name rather
than bind the odd member by a guess. Then run
BREAK_SLOT=1 just smoke count-is-a-fungible-set and report its "caught"
line too: that one stamps a tofu-slot onto a member the configuration
names, where none belongs, and the same tag read that passes in the
ordinary run must fail on it.
```

The steps as they print:

1. `stand up a pool of three` - three elastic IPs, three distinct slots.
2. `capture the survivor at the middle seat` - the allocation id that
   holds slot 1 is written down.
3. `scale to two - one removed, nothing rebuilt` - count drops to 2; the
   plan shows one destroy and zero creates, then applies.
4. `the middle survivor is the same live object` - the id from step 2 is
   still allocated. Its seat moved and its identity did not.
5. `both kinds of count instance, read back with the plain AWS CLI` - two
   `count` blocks of one type, `aws_cloudwatch_log_group`, differing in
   exactly one property: one names its members (`name =
   "/svc/${count.index}"`), the other leaves the name to the provider
   (`name_prefix`). Every tag is read back off the live log groups with
   the plain AWS CLI, no choudoufu in the read, and compared as a whole
   key set. The named pair carries exactly `purpose`, `tofu-address` and
   `tofu-estate`, with the estate and the address asserted by value and
   no `tofu-slot`; the `name_prefix` pair carries exactly `tofu-address`,
   `tofu-estate` and `tofu-slot`, with the slots `0` and `1` read off the
   live groups. Then the next plan is empty, so the first pair bound by
   its addresses and the second by its slots. One type, one
   `list-tags-for-resource` call, one apply: a slot coming back missing
   on one pair cannot be a broken query when the identical query answers
   on the pair beside it.
6. `teardown` - the pool is destroyed.

This claim carries two `BREAK` controls, because step 5 asserts a
presence and an absence and no single corruption tests both.

The `BREAK=1` run deletes the local files, cache and record store both,
so nothing but the tags names a member, then deletes the `tofu-slot` tag
from one of them. Two members now answer by slot and one has no answer,
so the plan refuses the half-slotted set and names the slot disagreement
rather than binding the odd member by position. Beside an intact record
the same strip is a repair, not a guess: the record names the member and
the plan re-stamps its slot.

The `BREAK_SLOT=1` run is that control's mirror. Step 5's claim about the
named pair is that a tag is NOT there, so the only corruption that can
test it is a tag that should not be there: it stamps `tofu-slot=0` onto
`/svc/0` with the AWS CLI and then runs the same check the ordinary run
runs - the same function, not a copy of it - which must fail, naming the
key set it read.
