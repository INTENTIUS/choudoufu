---
title: "Claim 50: A removed derived block is destroyed from its record"
claim: a-removed-derived-block-is-destroyed
---

# Claim 50: A removed derived block is destroyed from its record

A type with no marker cannot be found by a sweep that searches on one.
When its block is deleted, the estate's record is the only thing left
that names it, and the plan proposes the destroy from that record. With
the record lost too, nothing can name the address, so the plan cannot
destroy it. It must still warn, naming the type and the estate and never
the address, since the address is what was lost. It
is D3 of [#2119](https://github.com/INTENTIUS/choudoufu/issues/2119), the
sibling of [claim 1](no-silent-orphans.md) for this tier.

Each scenario runs from the repository root and ends on a `PASS` line; its
`BREAK=1` run breaks the thing the proof rests on and must print a
`caught` line. [The README](README.md) says what each needs installed.

## On AWS

No scenario is written yet, so the cell is open.

## On Kubernetes

No scenario is written yet, so the cell is open. The managedFields sweep
is the form it takes there.

## On Azure

### az-a-removed-derived-block-is-destroyed

    just smoke az-a-removed-derived-block-is-destroyed
    BREAK=1 just smoke az-a-removed-derived-block-is-destroyed

A tagged VNet and security group, two security rules and a subnet; the
rules and the subnet take no tags. The ssh rule's block is deleted with
the cache, and its record must propose exactly one destroy at
`azurerm_network_security_rule.ssh`: the Resource Graph pass rebuilds the
ARM id from the record. Then the subnet's record and block go together. No
destroy may be proposed and the subnet stays live, because nothing left in
the run names it. `plan -verbose` lists the untaggable types the
configuration still declares (the rule that stays) under "Not swept for
removal" as `TYPE_NOT_TAGGABLE`, and `live/RECOVERY.md` documents the leak
and its recovery from the record blob's previous version. Untaggable
`azapi_resource` types are not read by that leg yet.

`BREAK=1` loses the rules' records before the ssh rule's block is deleted,
and the destroy must go with them. Floci-az runs the sweep's Resource Graph query, `union`, `mv-expand` and
`bag_keys` included, since dfa33ca (lex00/floci-az#11), not yet run ([live/FLOCI.md](../../FLOCI.md#azure-floci-az)). It has no dated
run yet, so the cell reads unproven.
