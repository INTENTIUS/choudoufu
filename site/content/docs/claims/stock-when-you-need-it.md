---
title: "Claim 8: Stock when you need it"
weight: 8
claim: stock-when-you-need-it
---

# Claim 8: Stock when you need it

Stock behavior is not a mode you leave behind - it is the fallback,
whole and exact, one deleted live block away. The scenario measures
that rather than promising it: choudoufu and the pinned stock oracle
plan the same state-backed estate side by side with debug logging on,
and the plan texts match and so do the request counts, exactly. And
with the live backend on, what you pay scales with your estate rather
than the account around it.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke stock-when-you-need-it

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke stock-when-you-need-it and report the
"caught" line: it runs the choudoufu leg with the live block ON, and
the measurement must show the difference.
```

The steps as they print:

1. `a stock estate, stood up by choudoufu with no live block` - the
   fixture's live block is removed and choudoufu applies the ordinary
   way: a real `terraform.tfstate`, no markers, no hooks.
2. `same plan, same requests` - choudoufu and the pinned oracle each
   plan the estate with `TF_LOG=debug`; the scenario asserts the
   filtered plan texts are equal and the request counts identical.
   This is the #588 parity measurement as a two-minute demo.
3. `the live backend on - and you pay for your estate, not your
   account` - the
   live estate goes up and its plan's request count is measured. Twenty
   foreign resources then appear in the account and the count is
   measured again; it must not move.
4. `teardown` - estate and clutter both removed.

The `BREAK=1` run plans the choudoufu leg with the live block on. The
asked-for machinery must show up in the measurement - a live plan that
measured identical to stock would mean the parity comparison compares
nothing.
