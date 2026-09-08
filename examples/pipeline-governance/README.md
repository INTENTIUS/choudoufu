# pipeline-governance

The other half of [`examples/ci-pipelines`](../ci-pipelines): one warden policy
per forge that locks down a repository running those generated workflows.

Two files, and neither is a new idea. `github/governance.yml` is a
[github-warden](https://github.com/INTENTIUS/github-warden) policy and
`forgejo/governance.yml` is a
[forgejo-warden](https://github.com/INTENTIUS/forgejo-warden) policy, each in
its own tool's config shape, so you can drop one into your repository and run
it unchanged. What is specific to choudoufu is the names: the five Ops in
`examples/ci-pipelines/src` are the five job names, and these policies are
written against them rather than against a description of them.

## What they are written against

Read out of the generated workflows, not out of prose:

| Job | Trigger | Where |
|---|---|---|
| `live-check` | pull request to `main` | both forges |
| `live-plan` | pull request to `main` | both forges |
| `live-adopt` | push to `staging` | both forges |
| `live-apply` | push to `main` | both forges |
| `live-discover` | cron `0 6 * * *` | both forges |
| `live-adopt-gate-notice`, `live-apply-gate-notice` | after their job, when it gated | GitHub only |

Only the first two can be required status checks. A forge reports a check when
a job runs, and the other four never run on a pull request, so requiring
`live-apply` would leave every pull request pending forever. That is the whole
list of jobs a branch-protection rule here can name.

## What each policy locks

Both policies declare the same three things:

**The pull request cannot merge until choudoufu has answered.** `live-check`
and `live-plan` are required status checks on the default branch, alongside one
approving review. `live-check` is the one that matters most cheaply: it makes
no cloud call and needs no credential, and it fails a pull request whose
configuration choudoufu cannot run under live markers at all.

**An apply is behind a reviewer.** chant's gate is a fact on a ledger, and
`chant approve live-apply approve-live-apply` writes that fact as a commit on
the `chant/lifecycle` branch. Whoever can push that branch can let an apply
through, so both policies protect it: an approval becomes a reviewed pull
request rather than a push. On GitHub the policy additionally creates a
`production` environment with a required reviewer, which does less than it
looks like it does; see below.

**The credentials exist, and warden will not invent them.** Each policy
declares exactly the variables and secrets that forge's workflows read, so a
repository missing the apply role surfaces at reconcile time rather than in an
unattended 3am run that then cannot authenticate. `AWS_REGION` is declared with
a value because a pipeline pointed at the wrong region is an incident and a
region is not account-specific. The credentials themselves are presence-only:
the three role ARNs on GitHub, the static key pair on Forgejo.

## What the environment does

The GitHub policy declares a `production` environment with a required reviewer,
`preventSelfReview`, and a deployment branch policy limited to protected
branches. It gates `live-apply` now.

A GitHub environment's protection rules bind a job only when the job declares
`environment: production`, and chant's `generateOpsPipeline` used to emit no
`environment:` key at all - the gap this section used to describe. chant #2264
gave `ScheduledOpSpec` an `environment` option, `examples/ci-pipelines/generate.ts`'s
`live-apply` spec now sets `environment: { name: "production" }`, and the
generated `github/.github/workflows/live-apply.yml` carries the key. warden
keeps the environment and its reviewer in the state the policy declares, the
job references it, and the two now agree: `live/pipeline_governance_test.go`'s
`TestPipelineGovernanceEnvironmentGates` reads both sides and fails if a
rename or a regeneration ever lets them drift apart again.

This stacks on chant's own gate rather than replacing it. `chant approve
live-apply approve-live-apply` writes the approval of record as a commit on
`chant/lifecycle`, which both policies still protect, and a run that reaches
its gate with no resolution stops there regardless of what the forge-side
reviewer does. The environment reviewer is the earlier of the two stops: it
holds the job before any step runs, where chant's gate holds the run after it
has already started and read the live system.

GitLab's own generator maps the same `environment` option to its own
`environment:` key (chant #2268), and `gitlab/scheduled-ops.gitlab-ci.yml`'s
`live-apply` job carries it too - `TestPipelineGovernanceEnvironmentGates`
checks that side as well. This project ships no GitLab governance policy
(see "Anything on GitLab" below), so nothing here provisions the GitLab
protected-environment approval rule the key would need to mean anything on
that forge yet; the job says which environment it deploys to, and that is as
far as this repository goes today. Forgejo Actions has no environments at
all, so its dialect drops the key and says so in a header comment on the
generated file, and `chant/lifecycle` stays the only gate there.

## Where Forgejo differs, and why

**A status-check context is not a job name.** Forgejo builds it as
`"<workflow display name> / <job> (<event>)"`
([`services/actions/commit_status.go`](https://codeberg.org/forgejo/forgejo/src/branch/forgejo/services/actions/commit_status.go)),
and the display name is the workflow's own `name:` key, which the generated
workflows do not carry. The context Forgejo stores for `live-check` is
therefore literally `/ live-check (pull_request)`. Forgejo compiles each
required context as a glob
([`services/pull/commit_status.go`](https://codeberg.org/forgejo/forgejo/src/branch/forgejo/services/pull/commit_status.go),
`gobwas/glob`), so the policy declares `*/ live-check (pull_request)`: it binds
the job name and the event, which are stable, and keeps matching if a display
name ever appears in front of them. The Go guard checks that reasoning rather
than trusting it, matching each pattern against all three context strings
Forgejo could produce.

**There are no deployment environments.** Forgejo has no environment object and
no reviewer gate on a job, so there is no `production` block in that policy and
nothing standing in for one. The `chant/lifecycle` rule is not a substitute
invented for Forgejo; it is the gate that is load-bearing on both forges, and
on Forgejo it is the only one.

**A missing secret is created empty rather than refused.** forgejo-warden's
apply path PUTs the value of `$FORGEJO_SECRET_<NAME>` from the apply run's own
environment, and an empty string when that variable is unset
(`src/cycles/secrets-variables.ts`). github-warden instead reports the missing
secret and writes nothing. Run the Forgejo policy in dry-run and provision both
values before the first apply.

## Applying them

Dry-run reads and changes nothing. Do that first, on both forges.

```bash
# GitHub
cp examples/pipeline-governance/github/governance.yml .github/governance.yml
# edit: the org login, the repo name, the reviewer team id, the region
npx @intentius/github-warden reconcile \
  --config .github/governance.yml --token-env GH_TOKEN --mode dry-run

# Forgejo
cp examples/pipeline-governance/forgejo/governance.yml governance.yml
# edit: the org name, the repo name, the region
npx @intentius/forgejo-warden reconcile \
  --config governance.yml \
  --base-url https://forgejo.example.com \
  --token-env FORGEJO_TOKEN --mode dry-run
```

Both wardens are selective by omission: they manage only what the file
declares, and with no `owned:` declaration they create and update but never
delete. Both defaults are deliberate for a starter policy, which is going into
a repository that already has other things in it.

The reviewer id in the GitHub policy is left at `0` on purpose. It is a numeric
id rather than a slug (`gh api /orgs/<org>/teams/<slug> --jq .id`), and GitHub
rejects `0`, so an unedited policy fails loudly instead of creating an
environment whose reviewer list is empty.

## What these policies cannot check

**That the workflows in your repository are the generated ones.** Nothing in a
policy file points at a workflow file. A policy requiring `live-check` is
satisfied by any job named `live-check`, including one somebody wrote by hand
that exits 0. Holding the workflows to their generator is
`examples/ci-pipelines`' job, and it does it twice: the example's own
`npm test` regenerates both forges and diffs byte for byte, and
`live/ci_pipelines_test.go` is the backstop for a CI with no node.

**That a role has the permissions its job needs.** The policies assert the
role ARN variables exist. What those roles may do in AWS is an IAM policy, and
`examples/ci-pipelines`' README has the table of what each of the three needs.

**Anything on GitLab.** chant #2268 taught the gitlab Op generator
`pull_request`/`push` triggers and a merge-request-note posting mode, so
`examples/ci-pipelines/gitlab/scheduled-ops.gitlab-ci.yml` is generated now
and carries all five jobs, `live-apply` behind its own `environment:
production` key included - the premise "no job runs on a merge request, and
no job applies" this paragraph used to state is no longer true. What is still
true is that nothing here locks any of it: there is no
[gitlab-warden](https://github.com/INTENTIUS/gitlab-warden) policy in this
example, so no required merge-request check, no protected `chant/lifecycle`
rule and no protected-environment approval on GitLab's own `production`
object exist anywhere but in the job's own YAML. A GitLab policy is worth its
own file the day someone runs this pipeline for real; until then, treat the
generated GitLab jobs the way you would an ungoverned copy of the GitHub or
Forgejo ones.

**Anything about `staging`.** The `live-adopt` job runs on a push to `staging`
and writes marker tags after its gate. Neither policy protects that branch;
add a rule for it if you use it.

## The guard

`live/pipeline_governance_test.go` reads both sides, the policy YAML and the
generated workflow YAML, and fails when the names disagree: the required checks
against the jobs that run on a pull request, the protected branch against the
branch those pull requests target, the declared credentials against the `vars.`
and `secrets.` references the workflows read, and the Forgejo globs against the
contexts Forgejo's own code would build. Rename an Op in
`examples/ci-pipelines/src` and this policy has to move with it.

Each of those was proven red before it was trusted green, by tampering with one
name at a time; the failures are quoted in the pull request that added the
file.
