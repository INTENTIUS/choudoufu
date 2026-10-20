---
title: "Claim 52: The fence over derived types is what the docs say"
claim: the-fence-gap-is-as-stated
---

# Claim 52: The fence over derived types is what the docs say

A type with no marker cannot be fenced by one, so whatever fence covers
it is the platform's coarser one, and the docs state where that gap is.
This claim measures the stated gap on a derived type rather than taking
the docs' word for it. It is D5 of
[#2119](https://github.com/INTENTIUS/choudoufu/issues/2119).

Each scenario runs from the repository root and ends on a `PASS` line; its
`BREAK=1` run breaks the thing the proof rests on and must print a
`caught` line. [The README](README.md) says what each needs installed.

## On AWS

No scenario is written yet, so the cell is open. `live/MARKERS.md` states
the AWS gap per service.

## On Kubernetes

No scenario is written yet, so the cell is open. The field manager is
the fence there, with the #1191 conflict warning.

## On GCP

### gcp-the-fence-gap-is-as-stated

    just smoke gcp-the-fence-gap-is-as-stated
    BREAK=1 just smoke gcp-the-fence-gap-is-as-stated

`live/MARKERS.md`, "What a GCP grant cannot reach", says a
declaration-carried type is fenced at project scope only. With floci-gcp's
IAM evaluation on, a principal granted by `live/gcp/estate-grant.sh`
creates a `google_storage_bucket_iam_member` in its own estate's project
and is refused rewriting one in another project. A second estate's
principal, granted on the same project, rewrites the first estate's
member unopposed, and the first estate's next plan proposes it back.

`BREAK=1` removes the first principal's storage binding, and its apply of
the member must be refused. The emulator does not evaluate Compute, so the
type measured is a bucket's IAM member rather than a firewall. It has no
dated run yet, so the cell reads unproven.

## On Azure

### az-the-fence-gap-is-as-stated

    just smoke az-the-fence-gap-is-as-stated
    BREAK=1 just smoke az-the-fence-gap-is-as-stated

`live/MARKERS.md`, "What the Azure grant cannot reach", says an untaggable
child such as a subnet is fenced by the resource group's role assignment
and loses only the Policy half. With `live/azure/estate-boundary.json`
assigned at Alice's group, another estate's tagged create there is refused
by Policy, while the same estate creates a subnet in Alice's VNet
unopposed and an ARM delete of Alice's subnet goes through. Alice's next
plan proposes her subnet back.

`BREAK=1` removes the assignments after the tagged create is refused, and
that create must then land. Policy evaluation and role assignments with
enforcement are served by floci-az since 30000b0, not yet run; the RBAC
half is not in the scenario yet. floci-az's `mode: Indexed` skips only
resource groups, where Azure skips every type without tags, so there the
marker guard denies the untagged subnet create this claim says goes
through, and that step is expected to fail for the emulator's reason.
Resource Graph is served by floci-az since 30000b0, but the
sweep's query uses `union`, `mv-expand` and `bag_keys`, which its KQL
subset refuses ([live/FLOCI.md](../../FLOCI.md#azure-floci-az)). It has no dated run yet, so the cell reads unproven.
