---
title: "The claims"
weight: 1
bookCollapseSection: true
---

# Claims you can run

In stock Terraform and OpenTofu, the state file is the record of what you
own. Everything defends that file: backends store it and locks serialize
access to it, and if you lose it the tool no longer knows your
infrastructure exists. Choudoufu moves the record onto the platform
itself - identity as tags on each resource, values in a record store,
effects as receipts - and demotes the state file to a disposable cache.

That design implies testable promises. Each one is a smoke
scenario: Docker plus a local AWS emulator, one to three minutes each.
Exit 0 means every assertion held.

Each scenario can also run inverted. Under `BREAK=1` it manufactures the
exact corruption the claim guards against and passes only by catching it.
Claim 15's risk is a refusal that fires unconditionally, so its `BREAK=1`
run removes the fault and requires the run to succeed. The throttling
half of claim 20 is cited rather than run, because the emulator does not
throttle.

{{< claims-table >}}

## Reading a run

Every scenario narrates each step the same way: first why the step
exists and the exact command it runs, then real output indented as
evidence under a verdict line starting with `->`. The final paragraph of
each run recaps what you watched. The
[harness page](https://github.com/INTENTIUS/choudoufu/blob/main/live/smoke/README.md)
documents every knob; pinning the emulator image and the choudoufu
version are both there.
