---
title: "Tutorial: see markers work"
weight: 3
---

# Tutorial: see markers work

The run below stands up a real VPC, subnet and security group, plus an S3
bucket and a log group, inside a local AWS emulator. Every resource is created
with two tags that say who owns it. The run then deletes the state file and
plans again from those tags alone, drifts three resources out of band, and
corrects exactly what drifted. It takes about two minutes, and every claim in
this walkthrough is checked by the same run.

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

## Walk through what just happened

Step 2, `standup`, is a plain `init` and `apply` with a plain local state
file. The one thing to notice is in the fixture: every resource declares two
tags, `tofu-estate` and `tofu-address`, so the apply writes them onto the live
resources as it creates them. Those tags are the markers. This fixture spells
them out so you can see them. In your own estate you write no tags: one
`estate.chdf.hcl` file makes choudoufu add them on every create.

Step 3 deletes `terraform.tfstate` in front of you, and nothing else changes.
That is safe here only because the markers are already on every resource.
On an account whose resources carry no markers, deleting the state file first
is the wrong order, and
[Migrate an existing estate]({{< relref "/docs/use/migrate" >}}) has the right
one. From here on the state file is not the record of what you own. choudoufu
still keeps one as a cache it may find stale or missing, and it rebuilds what
it needs by reading the two tags off the live resources.

Steps 4 and 5 ask for a plan right after the handover, once with a
`-target`, once for the whole estate. Both come back empty. An empty plan
means every resource whose state was just deleted was found again, correctly,
by its tags alone.

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

## Next

- [Start a new estate]({{< relref "/docs/use/start" >}}) does this against
  your own AWS account.
- [Migrate an existing estate]({{< relref "/docs/use/migrate" >}}) covers
  resources AWS already holds that choudoufu should take over instead of
  creating fresh.
- [What it is]({{< relref "/docs/model" >}}) explains why two tags are enough.
- Every step, flag and environment knob this harness has is catalogued in
  [`live/e2e/README.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/e2e/README.md).
