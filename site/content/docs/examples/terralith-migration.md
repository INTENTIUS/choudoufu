---
title: "Terralith migration"
weight: 1
---

# Migrating a terralith into per-team estates

A terralith is one configuration that owns everything: every team's roles,
policies, and infrastructure under a single state. It plans slowly because
every plan refreshes the whole account, and ownership is a social convention
rather than a boundary the tool enforces.

This example takes a small terralith, splits it into an estate per team by
rewriting one ownership tag per resource, and then measures the difference.
No state file is hand-edited. choudoufu keeps a state file the same way
OpenTofu does, but it is a non-authoritative cache over the account rather than
the source of truth: ownership lives in a tag on the live resource, so moving a
resource between estates is a tag write, and the cache catches up on the next
plan. It runs against a real AWS account, with a live governance check at the
end proving the handover left nothing behind.

The code is a `uv` project under
[`examples/terralith-migration/`](https://github.com/INTENTIUS/choudoufu/tree/main/examples/terralith-migration).

## What it stands up

A monolith estate owning three teams' worth of resources. Each team has an IAM
role, an inline policy on that role, a managed policy and its attachment, and
three CloudWatch log groups. Everything is free and quick, so a full run costs
nothing and tears down in under a minute, while still carrying the IAM glue the
migration is really about: the inline policy is untaggable, so it cannot carry
an ownership marker of its own and has to follow its parent role.

## Before you run it

- [`uv`](https://docs.astral.sh/uv/) for the Python project.
- The AWS CLI, with credentials for the account you intend to use. The example
  is fenced to one account id in `tlmig/config.py` (`ACCOUNT_ID`); change it to
  yours, or it will refuse to run rather than touch the wrong cloud.
- The pinned `choudoufu` release. The version is set in `config.py`
  (`CHOUDOUFU_VERSION`) and `preflight` asserts the binary matches it. If you
  have run the smoke suite the example finds the cached binary automatically;
  otherwise download the release and point `CHOUDOUFU_BIN` at it:

  ```text
  gh release download <version> -R INTENTIUS/choudoufu \
    --pattern "choudoufu_<version>_$(uname -s | tr A-Z a-z)_*.tar.gz"
  tar xzf choudoufu_*.tar.gz
  export CHOUDOUFU_BIN="$PWD/choudoufu"
  ```

Docker is only needed for the reproducible receipt at the end, not for the live
run.

## Running it

Each phase is a separate command that reuses a run directory, so you can run
them one at a time and narrate between them:

```text
cd examples/terralith-migration
uv run tlmig setup                 # prints a new run id
uv run tlmig slow-plan --run <id>
uv run tlmig decompose --run <id>
...
uv run tlmig teardown --run <id>
```

Add `--auto` to skip the keypress between steps and auto-confirm the
destructive operations, for a rehearsal. `uv run tlmig all --auto` runs every phase in
order and tears down after. `uv run tlmig status --run <id>` reads what
is live at any point. `teardown` is always safe to run.

## The phases

**preflight** asserts the account and the binary version before anything
touches AWS. A mis-set profile or the wrong binary stops here.

**setup** applies the monolith: one estate, `tlmig-<id>-monolith`, owning all
three teams' resources.

**slow-plan** plans the whole monolith with a full refresh. This is the cost a
terralith pays on every plan: one provider request per resource read, for every
resource in the account. On the sample fixture it is around 56 requests.

**decompose** is the migration. For each team it writes a per-team
configuration under that team's estate, then moves each taggable resource with
`live-mv -from-estate`:

```text
choudoufu live-mv -from-estate tlmig-<id>-monolith \
  aws_iam_role.team_a aws_iam_role.team_a
```

That call rewrites one tag on one live resource, from the monolith's estate to
the team's. No state is edited and no `moved` block is written. The untaggable
children follow their parent role automatically. A recording apply on the team
estate then binds the adopted resources into its own store. Applying a team's
configuration by itself would not do this: choudoufu reads the resources as
belonging to another estate and refuses to adopt them as a side effect. Moving
a resource across an estate boundary is always a deliberate retag by its owner.

**fast-plan** plans one team's estate with `-refresh=false`. Because that
estate's resources are recorded and unchanged, the plan serves them from the
cache instead of reading each one, so it makes far fewer requests than the
monolith did. On the sample fixture it is around 29, against the monolith's 56.
The cache is a non-authoritative copy of what the account already holds: losing
it costs reads, never results, and a default plan still refreshes.

**carve** is the sharper case: team-a is being dissolved, and its resources
have to live on under team-b with no downtime. The resource blocks move into
team-b's configuration, and the same `live-mv -from-estate` retags each one
from team-a's estate to team-b's. Team-a's configuration is left declaring
nothing.

**guard** proves the carve left nothing behind, and it reads that proof from
neutral sources rather than from choudoufu's own report of itself:

- `aws iam list-role-tags` shows the moved role now carries team-b's estate.
- `aws iam list-role-policies` shows the inline policy still on the role.
- A plan of team-a's estate says `No changes` with nothing to destroy.
- A plan of team-b's estate says `No changes`.

If any of those failed, the guard would say so. On a clean carve all four hold,
and the handover is done.

**teardown** destroys everything the run applied, working from a manifest
rather than a guess, then refuses to call the run clean until it has listed the
account by tag and by name prefix and found nothing left.

## Why it is safe to run live

Every command the example issues goes through a single guarded executor with
four checks, so it is safe to run against a real account and to improvise on
top of:

1. Preflight asserts the one allowed account and the pinned binary.
2. A destructive `choudoufu` command must run inside the run's own working
   tree; a raw `aws` delete must name a resource carrying the run's prefix. The
   check is on the target, so it holds whatever a command asks for.
3. Destructive operations confirm before running, unless `--auto`.
4. Every command is appended to a transcript, so there is an exact record of
   what touched the account.

Reads are not fenced or confirmed, because they are how the governance guard
gathers its evidence and gating them would slow the demo without making it
safer.

## The reproducible receipt

The request counts above are real, and they vary from run to run. The figures
the write-up quotes come from the claim smokes running against the emulator,
which anyone can reproduce with `just smoke`. The `receipt` phase reads those
and shows them beside the live numbers, labelled as the receipt, so the two are
never confused for each other.

## Scripts you could write

The project is a small library as well as a demo: the guarded verbs, the run
config, and the reads are importable, and every command is fenced to the run.
That makes it a safe place to build. A few directions, each a short script on
top of what is here:

- Give a carve a cost preview. List the resources a move would take with it and
  estimate their monthly cost, so a handover comes with a number.
- Sweep for drift. Plan every estate with a refresh and report the ones that
  have drifted, reusing the plan-parsing the guard already does.
- Report a role's blast radius. For a given role, find every resource in every
  estate that references it, so you know what a move touches first.
- Reverse a carve. Move a resource back where it came from and check the round
  trip is symmetric.
- Check the IAM boundary. Assert that a role scoped to one estate cannot act on
  another's resources, and read the denial from CloudTrail.

Each of these reuses the guarded verbs and reads, so it inherits the fences.
This is where the example stops and your own work starts.
