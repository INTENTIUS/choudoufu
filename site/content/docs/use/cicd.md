---
title: "Running an estate from CI"
weight: 12
---

# Running an estate from CI

## What a choudoufu pipeline is

Not a YAML file per forge that you fill in. It is a
[chant](https://github.com/INTENTIUS/chant) project over one live root, and
the workflows are generated from it. chant's terraform lexicon runs a
choudoufu root natively (`binary: "choudoufu"`, a `live` block or an
`estate.chdf.hcl` sidecar), so the pipeline a choudoufu estate needs is a set
of Ops, and each forge's YAML falls out of them.
[`examples/ci-pipelines`](https://github.com/INTENTIUS/choudoufu/blob/main/examples/ci-pipelines/README.md)
is that project, with the generated workflows checked in beside it.

Five Ops, whose names are also the five job names:

| Job | Trigger | What it does |
|---|---|---|
| `live-check` | pull request | Runs `choudoufu live-check` and fails the pull request on a refusal. No cloud call, no state, no credential. |
| `live-plan` | pull request | Runs `choudoufu live-plan -detailed-exitcode -json` and reports drift, live resources sitting at a declared identity with no marker, and the subset an exact content match makes adoptable. |
| `live-apply` | push to `main` | Init, `plan -out`, a gate on the rendered plan, then `apply <planfile>`. |
| `live-adopt` | push to `staging` | Check, the adoption ledger, a gate, then the two marker tags per adoptable resource. Nothing in the source changes. |
| `live-discover` | cron | Sweeps the account with `live-ls -consistent`, reads the adoption ledger over it, and reports what carries this estate's marker that nobody declared. |

Three of them read and nothing else. The two that write are the two on a push
trigger, and both stop at an approval first.

The gate is a fact on chant's ledger rather than a runner held open: the run
reaches the gate, finds no resolution, records that it is waiting and ends.
`chant approve live-apply approve-live-apply` writes the resolution, and
re-running the workflow walks through it.

**The plan file is what crosses that gate.** `plan -out` writes the plan the
approver reads, and `apply <planfile>` does not replay it. Prior state on a
live root is rebuilt from the live system on every run, so the apply re-plans
against the cloud as it is now and then compares that fresh plan against the
approved one, down to the planned values. Agreeing, it applies without
re-prompting. Disagreeing, it refuses before anything changes, with
`The approved plan no longer matches the live system`, and exits **3**. That
exit is neither an ordinary failure nor `-detailed-exitcode`'s 2, so a
pipeline can route the run back to review instead of paging someone about a
broken step; chant's apply activity maps it to a refused result rather than a
thrown error. It is the gauntlet's `plan_approval` stage, measured on every
estate: see
[the stage table]({{< relref "/docs/progress#the-stages" >}}) and
[Compatibility reference]({{< relref "/docs/use/compatibility" >}}) for the
comparison rules.

## The per-environment dial

chant's lifecycle dial is observe, reconcile, authoritative, chosen per
environment. Here that is which Op an environment runs, not three copies of
the root:

- dev observes. Every pull request runs `live-check` and `live-plan`. One
  makes no cloud call at all and the other only ever plans, so neither can
  change anything.
- staging reconciles, behind its gate. A push to `staging` runs `live-adopt`,
  which writes ownership markers and nothing else, after an approval.
  Reconcile on a choudoufu estate is adoption rather than regeneration: the
  live plan already names every resource that matches a declared block and
  carries no marker, so claiming it is a tag write.
- production applies, behind its gate. A push to `main` runs `live-apply`.
- the account is swept regardless. `live-discover` runs on its cron, which is
  the only job looking at resources nobody has opened a pull request about.

That is one `chant.config.ts` over one root, not three templates that drift
apart. An environment differs by which Op fires on which trigger, and
splitting into per-environment roots is a change to the config's `roots` and
to the `root` each Op names, not a change to this shape.

## Per forge

| Forge | Where it comes from | The plan lands as | AWS credentials |
|---|---|---|---|
| GitHub | generated | a pull-request comment, edited in place by the next push | OIDC, no stored key |
| Forgejo | generated | the run's own log and step summary | a static key from repository secrets, unverified |
| GitLab | hand-written, cron only | the scheduled pipeline's own log | a static key from CI variables, unverified |

**GitHub** gets all five jobs with everything on: the `pull_request` and
`push` triggers, least-privilege `permissions:` computed per job,
`id-token: write` on the jobs that need a role, and
`aws-actions/configure-aws-credentials` as a per-job setup step. Three roles
rather than one is the point of that: `live-check` assumes none, `live-plan`
and `live-discover` assume a read role, `live-adopt` a role that can write
tags, `live-apply` a role that can change the estate.

**Forgejo** gets all five too, because chant's forgejo generator reuses
GitHub's builder, so the triggers cross over unchanged. Three things do not.
`permissions:` is dropped, because the Forgejo runner ignores it, and
`id-token: write` goes with it, which is honest: a Forgejo runner mints no
OIDC token off a workflow permission. The gated-apply notice job goes, because
it shells to `gh`. And so does every posting mode: `comment`, `issue` and
`pull-request` are all the same chant activity shelling to `gh`, and chant has
no Forgejo client, so both reporting jobs run in report mode instead of
generating cleanly and failing at their Report step on every run. Credentials
there are `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` from repository
secrets, and because the generator's variables become the workflow's top-level
`env:`, `live-check` sees a key it does not need.

**GitLab** gets `live-discover` alone, hand-written at
[`examples/ci-pipelines/gitlab/.gitlab-ci.yml`](https://github.com/INTENTIUS/choudoufu/blob/main/examples/ci-pipelines/gitlab/.gitlab-ci.yml),
on a project-level Pipeline Schedule. It is hand-written because chant's
gitlab Op generator refuses, by name, the three things the other four jobs
need: a `pull_request` or `push` event model (a GitLab schedule is a
project-level cron object rather than an event), a setup step spelled `uses:`
(GitLab CI has `script` and nothing else), and an additive `permissions` map.
The sweep needs none of them, which is why it is the one job that survives.

## Governance

[`examples/pipeline-governance`](https://github.com/INTENTIUS/choudoufu/tree/main/examples/pipeline-governance)
holds one policy per forge, `github` and `forgejo`, written against the job
names above: `live-check` and `live-plan` are required checks before a pull
request can merge, the branches the two writing jobs trigger on are protected,
and the apply's credential is required to be present without its value being
read. A policy that requires a job name is only as good as the guarantee that
the job named still does what the policy assumes, which is why the two halves
ship together.

What holds them together is the currency guard in `examples/ci-pipelines`. The
example's `npm test` re-runs the generator into a scratch directory and diffs
the checked-in workflows byte for byte in both directions, so an edited
workflow and a workflow that should no longer exist fail equally loudly. That
needs node, which this repository's Go CI does not have, so
[`live/ci_pipelines_test.go`](https://github.com/INTENTIUS/choudoufu/blob/main/live/ci_pipelines_test.go)
is the backstop that runs there with nothing but git and the files: every
workflow tracked, the set of workflows exactly the set of Ops, each file
naming its own Op source and forge, the choudoufu install pinned to a version
and verified against that release's published SHA256, and no generator input
committed after the workflows it generates. The guard is what makes "the
workflows are the ones the Ops generate" a checked statement rather than a
convention.

The approval of record is chant's gate, not the forge's. A forge-side
environment reviewer stacks on top of it and is not generated: a repository
that wants that second gate adds it to the checked-in workflow, and re-adds it
whenever the file is regenerated.

## What is not there yet

**GitLab beyond the sweep.** Pull-request and push triggers for the gitlab Op
generator, and a merge-request note activity to post a plan with, are chant's
to add rather than this repository's. They are sub-issue (e) of
[#807](https://github.com/INTENTIUS/choudoufu/issues/807). Until they land,
GitLab runs the scheduled sweep and a merge request there gets no plan.

**AWS auth off GitHub.** OIDC to AWS is proven on GitHub Actions and nowhere
else in this organization. Neither the Forgejo workflows nor the GitLab
pipeline has a verified OIDC path, so both ship with a static key and say so.
Treat them as the shape to copy rather than the credential model to keep: a
long-lived key that can change an estate is worth replacing with whatever
short-lived credential your runner can already mint.
