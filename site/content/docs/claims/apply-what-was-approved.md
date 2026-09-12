---
title: "Claim 15: Apply exactly what was approved"
weight: 15
claim: apply-what-was-approved
---

# Claim 15: Apply exactly what was approved

CI runs Terraform as: plan on the pull request, a human approves, apply
exactly what was approved. The artifact that crosses that gate is the
plan file, and here it stays the stock one - `plan -out=FILE`, `apply
FILE`. What changes is what the apply does with it. It never replays the
file. It reads the live system and plans against what is there now, the
way every live-markers run does, and then compares its own fresh plan
with the one the file describes: same resources, same actions, same live
objects, and the same values planned for them. Matching, it applies
without asking again, because the file was the approval. Differing, it
refuses by name and exits 3, which is a pipeline's signal to send the
change back to review rather than to page somebody about a broken run.

Values are compared canonically, not byte for byte: map and object keys
sorted, sets compared by their elements rather than their order, every
scalar carrying its type so the string `"3"` is not the number `3`. Two
things are deliberately outside the comparison. An attribute that is
unknown at plan time - "known after apply" - on either side is skipped,
so a value the provider only settles during the apply can never make a
matched artifact refuse. And a sensitive value is compared as a stable
`sha256` digest of its canonical rendering: a moved secret still
refuses, and no secret is ever printed.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. From the repo root run:

  just smoke apply-what-was-approved

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke apply-what-was-approved and report the "caught" line:
it leaves the world unmoved, and the same file must APPLY - a comparison
that refuses every plan file it is handed would prove nothing.
```

The steps as they print:

1. `stand the estate up` - the fixture applies, every resource carrying
   its ownership markers.
2. `the change under review` - a log group's retention goes from one day
   to three, and `plan -out=approved.tfplan` writes the stock-format
   file a pipeline would attach to the pull request.
3. `the world moves while the approval waits` - a subnet appears in the
   account carrying this estate's markers for an address the
   configuration does not declare, so the next plan proposes destroying
   it: a change nobody approved.
4. `apply the approved plan` - the apply re-reads the live system,
   compares, and refuses. The scenario asserts the refusal's own summary
   line, that the row it prints is `aws_subnet.crashed  Delete
   subnet-...`, and that the exit status is 3.
5. `the same change, a different value` - the subtler failure, and the
   one a comparison over resource names alone would wave through. The
   out-of-band subnet is removed so the change sets agree exactly, and
   the configuration is edited after the approval: fourteen days of
   retention instead of the three that were reviewed. Same resource,
   same action, same live log group, different planned value. The
   scenario requires exit 3 again, the refusal saying the two plans
   `disagree about the values it writes`, and the attribute named -
   `after.retention_in_days`.
6. `re-plan, re-approve, apply` - the way forward the refusal names. The
   same two commands over the world as it now is, and the approved
   change lands: the log group's retention reads 3.
7. `teardown` - the estate destroyed.

The `BREAK=1` run is the inverse control, and it is the one this claim
needs. A refusal that fires for every plan file handed to it is not a
check, and it would pass step 4 forever. So `BREAK=1` skips the
out-of-band change and the same file must apply cleanly; the scenario
fails if it refuses.
