---
title: "Claim 43: Two estates in one account apply at the same moment and both finish clean"
claim: two-estates-at-once
---

# Claim 43: Two estates in one account apply at the same moment and both finish clean

The estate boundary is what keeps them apart, not a queue. Two roots in
this scenario declare the identical resource address
(`aws_iam_role.svc`) and read the identical data source (the account's
own identity) - only the `tofu-estate` value differs between them, and
that difference alone is what lets both applies run at once with
nothing to serialize.

`site/content/docs/model/concurrency.md` says serialize applies anyway
in CI, where the real mutex has always been - and that sentence is
about two applies against **one** estate, the ownership boundary they
share. This claim is the other side of it: two estates that share
nothing but the account do not need that mutex at all.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke two-estates-at-once

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke two-estates-at-once and report the "caught" line:
it gives both roots the same tofu-estate value and the next plan must
name both live roles rather than reading clean.
```

Step by step:

1. `both estates apply at the same moment` - two roots, two different
   `tofu-estate` values, started together with no coordination. Both
   exit 0, the wall clock is well under the sum of what each apply took
   on its own, and neither root's output names the other's role.
2. `each estate re-plans clean` - both roots read `No changes.`. The
   other estate's role, its data-source read, and the shared account
   were never in question.
3. `teardown` - each estate destroys exactly its own role.

The `BREAK=1` control collapses the one thing that told the two apart:
both roots get the same `tofu-estate` value, while the address, the
shared read and the account all stay identical. The two live roles
still exist separately - AWS gave them different names - but they now
carry the identical ownership marker. Nothing about the concurrent
applies themselves needs to fail for this to show up: what changes is
the next plan, which names both live roles rather than reading clean,
either as discovery's own two-claimants collision or as a live object
displaced from the address it is marked for, identity beside identity.
Silence there - a plan still reading `No changes.` with neither live
role named - is what this control exists to catch, and did not happen:
the estate value, not the address or the account, is the boundary.
