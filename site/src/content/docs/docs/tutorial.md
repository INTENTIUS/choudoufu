---
title: "Tutorial: see markers work"
description: Stand up a small AWS estate in a local emulator, delete its state file, and watch the next plan find everything by its tags.
prompt: >-
  Follow https://intentius.io/choudoufu/docs/tutorial/ in a checkout of
  INTENTIUS/choudoufu. Check that `docker info` succeeds, run
  `bash live/e2e/run.sh --expect 5`, and tell me which steps passed and, if it
  failed, which step it named.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

This run stands up a VPC, a subnet, a security group, an S3 bucket and a log
group inside a local AWS emulator. Every resource is created with two tags that
say who owns it. The run then deletes the state file, plans again from those
tags alone, drifts resources out of band, and corrects exactly what drifted. It
takes about two minutes, and the same run checks every claim on this page.

## Before you start

You need Docker running, so that `docker info` succeeds. No AWS account, no
credentials, no cloud spend: the estate runs inside a pinned emulator on your
machine.

## Run it

From the root of a checkout of this repository:

```
bash live/e2e/run.sh --expect 5
```

The output looks something like this:

```
=== 0. choudoufu binary ===
=== 1. Floci on :4601 (ghcr.io/lex00/floci@sha256:c55d74e1...) ===
=== 2. standup — init + apply with plain local state ===
=== 3. adopt — delete terraform.tfstate(.backup); the non-event is the demo ===
=== 6. drift-exact — one mutation per estate type, each exactly one attribute ===
=== 13. drift-reconverge — three simultaneous drifts under plain plan/apply ===
=== 14. lint-rejects — every limits fixture is refused by its own named rule ===
EXPECT 5: OK -- every step phase<=5 is pass, every step phase>5 is not_implemented
PASS: live-mode E2E harness reached the end.
```

Exit code 0 means every step checked out. Otherwise the script names the step
that failed.

## What just happened

Step 2 is a plain `init` and `apply` with a local state file. Every resource in
the fixture declares the two tags, `tofu-estate` and `tofu-address`, so the
apply writes them as it creates each resource. Those tags are the markers. The
fixture spells them out so you can see them; in your own estate one
`estate.chdf.hcl` file makes choudoufu add them on every create.

Step 3 deletes `terraform.tfstate`, and nothing else changes. That is safe only
because the markers are already on every resource. On resources with no
markers, deleting the state file first is the wrong order;
[migrate an existing estate](../use/migrate/) has the right one. From here the
state file is a cache, and choudoufu finds what it owns by reading the tags.

Steps 4 and 5 plan right after, once with `-target` and once for the whole
estate. Both come back empty: every resource whose state was deleted was found
again by its tags.

Step 6 changes one attribute on several resources with the AWS CLI, behind
choudoufu's back. Each drift shows up as exactly that resource and that
attribute. Step 7 creates a security group nobody declared, and it shows as
foreign, never as something to delete. Step 8 removes a resource block and
watches that one resource get destroyed. Step 13 makes three drifts at once and
reconverges them in one apply.

Step 14 turns to what choudoufu refuses. Every fixture under
`live/e2e/limits/` is a configuration it will not accept yet, and the step
checks that each is refused by name, for the reason its fixture claims.

## Next

- [Start a new estate](../use/start/) does this against your own AWS account.
- [Migrate an existing estate](../use/migrate/) takes over resources AWS
  already holds.
- [What it is](../../how-it-works/) explains why two tags are enough.
- [`live/e2e/README.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/e2e/README.md)
  has every step and flag of the harness.
