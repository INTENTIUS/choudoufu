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

## On GCP

### gcp-a-removed-derived-block-is-destroyed

    just smoke gcp-a-removed-derived-block-is-destroyed
    BREAK=1 just smoke gcp-a-removed-derived-block-is-destroyed

A network, a firewall and a subnetwork, none labelled. The firewall's
block is deleted with the cache, and its record must propose exactly one
destroy at `google_compute_firewall.ssh`. Then the subnetwork's record and
block go together. No destroy may be proposed and the subnetwork stays live,
because nothing left in the run names it. The plan reports the gap instead:
`plan -verbose` lists the unlabelled types the configuration still declares
(the network) under "Not swept for removal" as `TYPE_NOT_TAGGABLE`, and
`live/RECOVERY.md` documents the leak and its recovery, restoring the
record's noncurrent version, with the subnetwork as its example.

`BREAK=1` loses the firewall's record before its block is deleted, and
the destroy must go with it. It has no dated run yet, so the cell reads
unproven.
