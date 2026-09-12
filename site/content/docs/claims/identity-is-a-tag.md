---
title: "Claim 7: Identity is a tag you can read and move"
weight: 7
claim: identity-is-a-tag
---

# Claim 7: Identity is a tag you can read and move

Because ownership lives on each resource as two tags, three things
follow that stock cannot offer: estates in one account are isolated by
construction, any AWS tool can answer ownership without this tool
present, and renaming a resource in code is a tag rewrite where stock
demands `state mv` surgery.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke identity-is-a-tag

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke identity-is-a-tag and report the "caught" line: it
renames the resource in code but skips the retag, and the plan must
propose the destroy-and-recreate stock would inflict.
```

The steps as they print:

1. `two estates stand up in one account` - two copies of the estate,
   different estate tags, one account. Nothing else separates them.
2. `any AWS tool answers ownership` - the plain CLI's tagging API lists
   each estate's resources and reads a resource's address tag. No
   choudoufu involved.
3. `neither estate can see the other` - both plans are clean, and
   neither plan output ever names the other estate's resources.
4. `a rename is a retag, not surgery` - `aws_vpc.main` becomes
   `aws_vpc.core` in code, `live-mv` rewrites the address tag on the
   live resource, and the next plan is clean. No state file was edited,
   because there is none to edit.
5. `teardown - both estates`, each by its own destroy.

The `BREAK=1` run skips `live-mv` after the code rename. The live vpc
still wears the old address, so the plan must treat the new name as
missing and the old one as orphaned - stock's destroy-and-recreate,
demonstrated as what the retag saves you from.
