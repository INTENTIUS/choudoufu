---
title: "Claim 42: A killed apply hides nothing it marked"
claim: a-killed-apply-hides-nothing
---

# Claim 42: A killed apply hides nothing it marked

Claims 1 and 5 already cover the crash shape, but both manufacture it with
the AWS CLI: a resource created and tagged by hand, standing in for one a
dead apply left behind. This claim kills a real apply with SIGKILL, at a
point pinned by a resource count read off the live account rather than by a
timer, and then measures what the next plan can say about each thing that
apply left.

The answer is not the same for every resource, and the scenario is built so
that one kill lands inside all three cases at once.

A VPC is marked in its own create request, because `internal/live/stamp`
puts the markers in the call. There is no window: the object is this
estate's from the instant it exists. The next plan proposes nothing for it
and the re-run binds it.

A hosted zone is one of the ten types whose registry row reads
`tag_on_create: false` (#1084). `CreateHostedZone` takes no `Tags`
parameter, so this fork withholds the markers from the create call and
writes them itself once `ApplyResourceChange` returns (#1489, #1518). The
marker write is one round trip; the window it closes is not. The zone is in
the account from the create call, and the markers land only when the whole
provider create step returns - measured at 15.0s apart against the pinned
emulator, with the apply reporting `Creation complete after 15s`. An apply
killed inside that window leaves a zone nothing can claim. The next plan
proposes a second one and the re-run builds it; removing the first is the
one piece of surgery in the whole run.

A `terraform_data` with a provisioner is record-carried: it has no cloud
home, so its record is the only trace that it ran, and
`projection.WriteBack` writes records once, after the whole graph walk
(`internal/backend/local/backend_apply.go`). A killed walk writes none. So
the effect has run and nothing says so, the next plan names the instance as
a create, and the effect runs a second time. That is at-least-once, and it
is the same thing stock does with a state file lost to the same crash. It is
stated by the run rather than left to be discovered.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke a-killed-apply-hides-nothing

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke a-killed-apply-hides-nothing and report the "caught"
line: it strips the ownership markers off everything the killed apply
created, and the re-run must then build a duplicate.
```

1. `init, and start the apply that is going to be killed`. Four resources in
   one chain: `terraform_data.effect`, `aws_vpc.main`, `aws_route53_zone.dns`,
   and `terraform_data.tail`, whose only job is to still be running when the
   kill arrives.
2. `kill it with SIGKILL, pinned by the account's own count`. The pin is
   `aws route53 list-hosted-zones`, polled as fast as the CLI answers, 0
   then 1. The apply and every child of it are killed, so no provider
   plugin outlives the run. The run asserts the apply did not finish and
   did not exit 0.
3. `what the cloud holds, and what the run kept`. The VPC carries both
   markers. The zone carries neither. `effects.log` has one line. The
   record store holds no record for `terraform_data.effect`, and no state
   cache was written at all.
4. `the next plan names what the killed apply marked`. `Plan: 3 to add, 0
   to change, 0 to destroy`: the VPC is not in it, `terraform_data.effect`
   and `terraform_data.tail` are, and so is a second hosted zone.
5. `the re-run: what it binds, and what it duplicates`. One VPC with the
   estate's CIDR, two zones with the estate's name, `effects.log` at two
   lines, and a record for `terraform_data.effect` now that the walk
   finished.
6. `the orphan, and the only surgery in this scenario`. The unmarked zone
   is deleted with `aws route53 delete-hosted-zone`, because nothing in the
   tool will propose an object that carries no marker. The plan is then
   `No changes.`, from markers and records alone.
7. `teardown`. Four destroyed.

Under `BREAK=1` the markers are stripped off everything the killed apply
created, through both the Tagging API the sweep reads and the VPC's own EC2
tags, and the run asserts the strip actually landed before going on. The
re-run must then propose creating the VPC and must leave two of them. A
control run with the strip disabled was made on purpose and failed with
`the markers are still on arn:aws:ec2:...:vpc/...`, so the control catches a
control that broke nothing.

What this claim does not say. It does not say that nothing whatever can be
hidden: the tag-on-create window is real, it is bounded to the ten types
that carry it, and step 6 pays its cost by hand. It does not say a
record-carried effect runs exactly once. And the 15.0s figure is this
emulator's, measured at the commit that added this scenario; the width on
real AWS is whatever the provider's create step costs there and has not
been measured.
