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
