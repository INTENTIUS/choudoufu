# pipeline-governance

The other half of [`examples/ci-pipelines`](../ci-pipelines): a warden policy
for each forge that has one, locking down a repository running those
generated workflows.

Two policies today, and neither is a new idea. `github/governance.yml` is a
[github-warden](https://github.com/INTENTIUS/github-warden) policy and
`forgejo/governance.yml` is a
[forgejo-warden](https://github.com/INTENTIUS/forgejo-warden) policy, each in
its own tool's config shape, so you can drop one into your repository and run
it unchanged. GitLab is the third forge `examples/ci-pipelines` generates a
pipeline for; it ships no [gitlab-warden](https://github.com/INTENTIUS/gitlab-warden)
policy yet (#1008) - see "Anything on GitLab" below. What is specific to
choudoufu is the names: the five Ops in `examples/ci-pipelines/src` are the
five job names, and these policies are written against them rather than
against a description of them.

## What they are written against

Read out of the generated workflows, not out of prose:

| Job | Trigger | Where |
|---|---|---|
| `live-check` | pull request to `main` | all three forges |
| `live-plan` | pull request to `main` | all three forges |
| `live-adopt` | push to `staging` | all three forges |
| `live-apply` | push to `main` | all three forges |
| `live-discover` | cron `0 6 * * *` | all three forges |
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
`live-apply` job carries it too. Since #1008, `gitlab/governance.yml`
provisions the matching `protectedEnvironments:` entry, and
`TestPipelineGovernanceEnvironmentGates` checks that side the same way it
checks GitHub's `environments:` entry - both directions, both forges.
Forgejo Actions has no environments at all, so its dialect drops the key and
says so in a header comment on the generated file, and `chant/lifecycle`
stays the only gate there.

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

## Where GitLab differs, and why

| | GitHub | Forgejo | GitLab |
|---|---|---|---|
| Config shape | `orgs: -> repos:` | `orgs: -> repos:` | `nodes:` keyed by path |
| "Required" mechanism | named status checks | named status checks (glob) | `onlyAllowMergeIfPipelineSucceeds` + a comment naming the jobs |
| Review requirement | branch-rule field | branch-rule field | project-wide `approvalRules` (Premium) |
| Deployment gate | `environments:`, native | none - `chant/lifecycle` only | `protectedEnvironments:`, native (Premium) |
| Apply credential | 3 OIDC role ARNs | 1 static key pair | 3 OIDC role ARNs + `GITLAB_TOKEN` |

**The config shape is not a reskin of the other two.** github-warden and
forgejo-warden share one spine, `orgs: <org>: repos: <repo>:`, which is why
`live/pipeline_governance_test.go` can read both with one parser. gitlab-warden
has no concept of an org or a repo in its config; it has a single top-level
`nodes:` map keyed by full path, one entry per group or project
([`INTENTIUS/gitlab-warden`'s `POLICY.md`](https://github.com/INTENTIUS/gitlab-warden/blob/main/POLICY.md),
`src/config/types.ts`). `gitlab/governance.yml` is a `kind: project` node, and
the Go guard for it is a second, parallel reader rather than a third branch
bolted onto `govPolicyRepo`.

**"Required" is a pipeline setting, not a list of names.** GitHub's
`requiredStatusCheckContexts` and Forgejo's `statusCheckContexts` both name
individual jobs. GitLab has nothing equivalent: a protected branch's pipeline
requirement is `onlyAllowMergeIfPipelineSucceeds`, which blocks the merge
unless the whole pipeline succeeded, not any one named job. Since `live-check`
and `live-plan` are the only jobs the generated pipeline rules onto a merge
request, "the pipeline must succeed" comes down to exactly those two there -
`gitlab/governance.yml` names them in a comment for
`live/pipeline_governance_test.go` to hold against a rename, because the
schema itself has nowhere to put that list.

**A review is a project setting too, not a branch attribute.** `approvalRules`
sets how many approvals a merge request needs project-wide; there is no
per-branch review-count field the way github-warden's
`requiredPullRequestReviews` or forgejo-warden's `requiredApprovals` are part
of the branch rule itself. `protectedBranches` here covers what GitLab does
attach to a branch: who may push or merge it, and whether it can be force
pushed - which is also how "no deletion" is expressed, since GitLab has no
separate delete permission on a protected branch, only the same force-push
gate.

**Two of these blocks are Premium/Ultimate, on GitLab.com and self-managed
alike - not a warden limitation, a GitLab one.** `approvalRules`
(gitlab-warden's `mr-approvals` cycle) and `protectedEnvironments`
(`protected-environments`) both need a paid tier. `INTENTIUS/gitlab-warden`'s
e2e suite runs its full read/apply/converge/drift/delete loop against a real
GitLab CE 17.11 instance for every cycle CE supports, and its coverage table
(`e2e/README.md`) marks these two read-only there: CE reports a 404 rather
than a 403 for a tier-gated endpoint, which warden tolerates as "unmanaged"
rather than a plan NOTE, and an *apply* against either lands that 404 in
`failed[]` instead of converging. This policy declares both anyway, because
the issue this file answers (#1008) asks for the review requirement and the
production gate the same way the other two policies have them, and the
generated `live-apply` job already names the `production` environment it
would bind to. On Premium or above both blocks are real; on CE or Free they
are a stated intent that does not yet converge, and the policy says so in a
comment at each block rather than pretending otherwise. `protectedEnvironments:`
is otherwise the direct GitLab counterpart of GitHub's `environments:` - see
"What the environment does" above.

**One more credential than the other two.** `live-plan`'s merge-request note
goes over GitLab's own REST API rather than through `gh`
(`examples/ci-pipelines/src/forge.ts`), and that call needs a `GITLAB_TOKEN`
CI/CD variable with `api` scope - a credential neither the GitHub nor the
Forgejo policy declares, because neither needs it. It is presence-only here
the same way the role ARNs are, `value` omitted - but "presence-only" is a
third thing on GitLab, not a repeat of the other two, confirmed against a
real GitLab CE 17.11 apply while writing this policy: on create,
gitlab-warden reads `$GITLAB_VAR_<KEY>` from the apply run's own environment
(`INTENTIUS/gitlab-warden`'s `POLICY.md`), and an unset one is not refused by
warden the way a missing GitHub variable is, nor created empty the way a
missing Forgejo secret is - GitLab's own API rejects the empty value outright,
`POST .../variables returned 400: {"message":{"value":["is invalid"]}}`.
Provision the four masked variables via `GITLAB_VAR_<KEY>` before the first
apply, or that apply fails on all four with this error.

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

# GitLab
cp examples/pipeline-governance/gitlab/governance.yml governance.yaml
# edit: the node's key, the project's group/subgroup path
npx @intentius/gitlab-warden reconcile \
  --config governance.yaml \
  --base-url https://gitlab.example.com \
  --token-env GITLAB_TOKEN --mode dry-run
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
`npm test` regenerates all three forges and diffs byte for byte, and
`live/ci_pipelines_test.go` is the backstop for a CI with no node.

**That a role has the permissions its job needs.** The policies assert the
role ARN variables exist. What those roles may do in AWS is an IAM policy, and
`examples/ci-pipelines`' README has the table of what each of the three needs.

**That the GitLab policy's `approvalRules` or `protectedEnvironments` converge
on your instance.** "Where GitLab differs, and why" has the detail: both are
Premium/Ultimate, proven read-only (not applied) against GitLab CE by
`gitlab-warden`'s own e2e suite.

**Anything about `staging` on GitHub or Forgejo.** The GitLab policy protects
`staging` (no force push) because #1008 asked for it in anticipation of
#1024's extension of this example to the other two forges. `github/governance.yml`
and `forgejo/governance.yml` do not yet; add a rule there too if you use it
before #1024 lands.

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
