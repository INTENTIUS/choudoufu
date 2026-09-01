---
title: "Tutorial: see markers work"
weight: 2
---

# Tutorial: see markers work

The run below stands up a real VPC, subnet and security group, plus an S3
bucket and a log group, inside a local AWS emulator. choudoufu builds them
and deletes its own state file, then rebuilds its bookkeeping from two tags
read straight off the live resources. It then drifts three of them out of
band and corrects exactly what drifted. The whole thing takes about two
minutes, and every claim in this walkthrough is checked by the same run.

## Before you start

You need Docker running - `docker info` must succeed - and nothing else. No
AWS account or credentials, and no cloud spend. The estate stands up inside
a pinned local emulator that speaks the AWS API on your own machine.

## Run it

From the root of a checkout of this repository:

```
bash live/e2e/run.sh --expect 5
```

The output should look something like this, with a couple of minutes between
the first line and the last:

```
=== 0. choudoufu binary ===
...
=== 1. Floci on :4601 (ghcr.io/lex00/floci@sha256:c55d74e1...) ===
...
=== 2. standup — init + apply with plain local state ===
...
=== 3. adopt — delete terraform.tfstate(.backup); the non-event is the demo ===
...
=== 6. drift-exact — one mutation per estate type, each exactly one attribute ===
...
=== 13. drift-reconverge — three simultaneous drifts under plain plan/apply ===
...
=== 14. lint-rejects — every limits fixture is refused by its own named rule ===
...
EXPECT 5: OK -- every step phase<=5 is pass, every step phase>5 is not_implemented
PASS: stateless-mode E2E harness reached the end.
```

Exit code 0 means every one of those steps checked out. Anything else, and
the script names the step that didn't on its way out.

The emulator on line 1 is printed in full, digest and all. Yours will be
whatever `live/floci-image` pins in your checkout - the harness reads that
file, and `FLOCI_IMAGE` overrides it - so the tail of the digest above is
abbreviated rather than something to match against.

## Walk through what just happened

Notice that step 2, `standup`, is the least interesting thing OpenTofu does:
plain `init` and `apply` against a plain local state file, no markers
anywhere yet. That's the baseline every later step gets compared against.

Step 3 is the handover, and it's almost anticlimactic: the script deletes
`terraform.tfstate` in front of you, and nothing else changes, not the
configuration, not the resources sitting in the emulator. That deletion is
the entire migration to marker mode. From here on, choudoufu has no state
file to read. It rebuilds what it needs to know by reading two tags,
`tofu-estate` and `tofu-address`, straight off the live resources.

Steps 4 and 5 ask for a plan right after the handover, once with a
`-target`, once for the whole estate. Both come back empty. The output
should read this as proof, not assertion: an empty plan means every resource
choudoufu just "forgot" was found again, correctly, by its tags alone.

Then the run tests what the tags are for. Step 6 changes one attribute on
each of several resource types using the AWS CLI directly, behind
choudoufu's back, and each drift surfaces as exactly that resource and that
attribute, with no noise on its neighbours. Step 7 creates a security group
choudoufu never declared, and it shows up as foreign, never as something to
delete. Step 8 removes a whole resource block from the configuration and
watches exactly that live resource get destroyed. Step 13 does three
drifts at once, each of a different kind, and reconverges all three in a
single apply.

By step 14, the run turns to what choudoufu refuses. Every fixture under
`live/e2e/limits/` is a configuration this mode is not yet safe to accept,
and the step confirms each one is rejected, by name, for the reason its own
fixture claims and no other.

You have just watched choudoufu build a real, emulated AWS estate, hand its
own bookkeeping over to tags on the live resources, survive three kinds of
drift and a removal without losing track of anything, and refuse the
constructs it isn't ready for, all without touching a credential.

## Next

- [Start a new estate]({{< relref "/docs/use/start" >}}) does this against
  your own AWS account.
- [Migrate an existing estate]({{< relref "/docs/use/migrate" >}}) covers
  resources AWS already holds that choudoufu should take over instead of
  creating fresh.
- [The model]({{< relref "/docs/model" >}}) explains why two tags are enough
  to recover identity.
- Every step, flag and environment knob this harness has is catalogued in
  [`live/e2e/README.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/e2e/README.md).
