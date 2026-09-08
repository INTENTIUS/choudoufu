---
title: "Running an estate from CI"
weight: 12
---

# Running an estate from CI

## What a choudoufu pipeline is

A [chant](https://github.com/INTENTIUS/chant) project over one live root,
with the workflows generated from it, rather than a YAML file per forge that
you fill in. chant's terraform lexicon runs a choudoufu root natively under
`binary: "choudoufu"` once the root declares an estate. So the pipeline a
choudoufu estate needs is a set of Ops, and each forge's YAML falls out of
them.
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

The gate is a fact on chant's ledger rather than a runner held open. A run
that reaches it finds no resolution, records that it is waiting, and ends.
`chant approve live-apply approve-live-apply` writes the resolution, and
re-running the workflow walks through it.

**The plan file is what crosses that gate.** `plan -out` writes the plan the
approver reads, and `apply <planfile>` does not replay it. Prior state on a
live root is rebuilt from the live system on every run, so the apply re-plans
against the cloud as it is now and then compares that fresh plan against the
approved one, down to the planned values. Agreeing, it applies without
re-prompting. Disagreeing, it refuses before anything changes: it prints
`The approved plan no longer matches the live system` and exits **3**. That
exit is neither an ordinary failure nor `-detailed-exitcode`'s 2, so a
pipeline can route the run back to review instead of paging someone about a
broken step; chant's apply activity maps it to a refused result rather than a
thrown error. That refusal is the gauntlet's `plan_approval` stage, measured
on every estate: see [the stage table]({{< relref "/docs/progress#the-stages" >}}),
and [Compatibility reference]({{< relref "/docs/use/compatibility" >}}) for
what the two plans are compared on.

## The per-environment dial

chant's lifecycle dial is observe, reconcile, authoritative, chosen per
environment. Here it is a choice of which Op an environment runs:

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

All of that is one `chant.config.ts` over one root, so there is no second
copy of the pipeline to keep in step with the first. An environment differs
by which Op fires on which trigger. Splitting into per-environment roots is a
change to the config's `roots` and to the `root` each Op names, and this
shape survives it.

## Per forge

| Forge | Where it comes from | The plan lands as | AWS credentials |
|---|---|---|---|
| GitHub | generated | a pull-request comment, edited in place by the next push | OIDC, no stored key |
| Forgejo | generated | the run's own log and step summary | a static key from repository secrets, unverified |
| GitLab | generated | a merge-request note, edited in place by the next push | OIDC, no stored key, unverified |

**GitHub** gets all five jobs with everything on. Both CI-native triggers
fire, `permissions:` is computed per job at least privilege, and
`aws-actions/configure-aws-credentials` rides each job's own setup list with
the `id-token: write` it needs. That per-job setup is what buys three roles
rather than one: `live-check` assumes none, `live-plan` and `live-discover`
assume a read role, `live-adopt` a role that can write tags, and `live-apply`
a role that can change the estate.

**Forgejo** gets all five too, because chant's forgejo generator reuses
GitHub's builder, so the triggers cross over unchanged. Three things do not.
`permissions:` is dropped because the Forgejo runner ignores it, and
`id-token: write` goes with it, which is honest: a Forgejo runner mints no
OIDC token off a workflow permission. The gated-apply notice job goes because
it shells to `gh`. So does every posting mode. `comment`, `issue` and
`pull-request` are one chant activity shelling to `gh`, and chant carries no
Forgejo client, so a reporting job that tried to post there would generate
cleanly and fail at its Report step on every run. Both reporting jobs run in
report mode instead. Credentials there are `AWS_ACCESS_KEY_ID` and
`AWS_SECRET_ACCESS_KEY` from repository secrets, and because the generator's
variables become the workflow's top-level `env:`, `live-check` sees a key it
does not need.

**GitLab** gets all five jobs too, generated into one combined file at
[`examples/ci-pipelines/gitlab/scheduled-ops.gitlab-ci.yml`](https://github.com/INTENTIUS/choudoufu/blob/main/examples/ci-pipelines/gitlab/scheduled-ops.gitlab-ci.yml) -
one document rather than one file per Op, because a GitLab trigger lives on
the job's own `rules:` rather than on the file's `on:`, so there is nothing
to split into separate files. `live-plan` posts a merge-request note over a
plain REST call rather than shelling to `gh`; `live-apply` deploys to the
`production` environment the way GitHub's does; `live-discover` still only
reports, because its cron trigger carries no merge request for a note to
land on. GitLab CI has no `uses:` step, so a role assumption there is a
shell script that writes the job's `id_tokens:` JWT to a file and points
the two environment variables every AWS SDK's own "web identity" credential
provider already reads at it, rather than a marketplace action - and for the
same reason there is no gated-apply notice job, which would need a forge API
call this dialect has no shape for. `live-adopt` and `live-apply` still run
with `--gated-exit 0`, so a gated run is a green pipeline, and its pending
gate lands as a downloadable artifact rather than a step summary, which
GitLab has none of. This crossed over in chant #2268; earlier the generator
refused every `pull_request`/`push` trigger and the `comment` finding mode by
name, which is why GitLab used to get `live-discover` alone, hand-written.

## Governance

[`examples/pipeline-governance`](https://github.com/INTENTIUS/choudoufu/tree/main/examples/pipeline-governance)
holds one policy per forge, `github` and `forgejo`, written against the job
names above. `live-check` and `live-plan` are required checks before a pull
request can merge. `main` is protected, and so is `chant/lifecycle`, where
`chant approve` writes the approval that lets an apply through; the apply's
credential is required to be present without its value being read. A policy that requires a job name is only as good as the
guarantee that the job named still does what the policy assumes, which is why
the two halves ship together.

What holds them together is the currency guard in `examples/ci-pipelines`. The
example's `npm test` re-runs the generator into a scratch directory and diffs
the checked-in workflows byte for byte in both directions, so an edited
workflow and a workflow that should no longer exist fail equally loudly. That
run needs node, which this repository's Go CI does not have. So
[`live/ci_pipelines_test.go`](https://github.com/INTENTIUS/choudoufu/blob/main/live/ci_pipelines_test.go)
is the backstop that runs there with nothing but git and the files: every
workflow tracked, the set of workflows exactly the set of Ops, each file
naming its own Op source and forge, the choudoufu install pinned to a version
and verified against that release's published SHA256, and no generator input
committed after the workflows it generates. The guard is what makes "the
workflows are the ones the Ops generate" a checked statement rather than a
convention.

The approval of record is chant's gate, not the forge's. A forge-side
environment reviewer stacks on top of it: `live-apply`'s spec now carries an
`environment: { name: "production" }` option, so the generated GitHub and
GitLab jobs both declare `environment:`, and the github policy's `production`
reviewer gates the job rather than nothing. Forgejo Actions has no
environments at all, so its dialect drops the key and says so in a header
comment on the generated file.

## What is not there yet

**A GitLab governance policy.** `examples/pipeline-governance` holds policies
for `github` and `forgejo` only. GitLab's own Op generator now expresses all
five triggers (chant #2268), so its pipeline runs real merge-request and push
jobs the way GitHub's and Forgejo's do - but nothing requires its checks,
protects `chant/lifecycle` there, or provisions the `production`
protected-environment approval rule its `live-apply` job now names. A
[gitlab-warden](https://github.com/INTENTIUS/gitlab-warden) policy is worth
its own file the day someone runs this pipeline for real.

**AWS auth off GitHub.** OIDC to AWS is proven on GitHub Actions and nowhere
else in this organization. GitLab's role assumption follows the same shape
GitHub's does - a job-level identity token exchanged for role credentials -
over GitLab's own `id_tokens:` surface rather than a marketplace action, but
nothing here has run it against a real GitLab instance and a real AWS IAM
OIDC identity provider. The Forgejo workflows have no OIDC surface to reach
for at all: Forgejo Actions drops both `permissions:` and `id_tokens:`, so
they ship with a static key and say so. Treat the GitLab shape as unverified
and the Forgejo one as the credential model to replace outright: a
long-lived key that can change an estate is worth replacing with whatever
short-lived credential your runner can already mint.
