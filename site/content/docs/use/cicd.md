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

The gate is a fact on chant's ledger rather than a runner held open: a run
that reaches it finds no resolution, records that it is waiting, and ends.
`chant approve live-apply approve-live-apply` writes the resolution, and
re-running the workflow walks through it, via the plan file `plan -out`
wrote - `apply <planfile>` does not replay that file. Prior state is rebuilt
from the live system on every run, so the apply re-plans against the cloud
as it is now and compares that fresh plan against the approved one, down to
the planned values: agreeing, it applies without re-prompting; disagreeing,
it refuses before anything changes, printing `The approved plan no longer
matches the live system` and exiting **3** - neither an ordinary failure nor
`-detailed-exitcode`'s 2, so a pipeline can route the run back to review
instead of paging someone. That refusal is the gauntlet's `plan_approval`
stage, measured on every estate: see
[the stage table]({{< relref "/docs/progress#the-stages" >}}) and
[Compatibility reference]({{< relref "/docs/use/compatibility" >}}) for what
the two plans are compared on. What a gate resolution does not bind is
covered below.

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
by which Op fires on which trigger.

## Per forge

What each forge's pipeline does. All three generate the full five-job set
now (GitLab crossed over in chant #2268), and the differences below come
from what each forge's dialect can carry:

| | GitHub | Forgejo | GitLab |
|---|---|---|---|
| Jobs | all 5, `permissions:` computed per job | all 5, `permissions:`/`id-token:` dropped (runner ignores them) | all 5, one combined file (`rules:` per job, not `on:`) |
| Credentials | 3 OIDC roles, one per job that needs one | 3 static key pairs, one per job that needs one - `live-check` holds none (#1028) | 3 OIDC roles via `id_tokens:` and a shell script, no marketplace action |
| Plan lands as | a pull-request comment, edited in place | a pull-request comment, edited in place (chant#2291) | a merge-request note, edited in place |
| Sweep reaches | a GitHub issue, edited in place | the run's own log - `issue`'s endpoints are unverified there (#1027) | a GitLab issue, edited in place (chant#2292) |
| Apply gate | chant's gate, plus a `production` environment reviewer | chant's gate only - Forgejo Actions has no environments | chant's gate, plus `production`; an audit trail only on CE |
| Reporting | `comment`/`issue`/`pull-request` via `gh` | `comment` via `gh`, built from `GITHUB_API_URL` (chant#2291); `issue` stays `report` | `comment`/`issue` via a plain REST call to GitLab's own API |

All three have now been run, once each, against no real cloud account:

| | Stack | Result |
|---|---|---|
| GitHub | v0.16.0, [run 34313049854](https://github.com/INTENTIUS/choudoufu/actions/runs/34313049854), floci as a service container, all four triggers plus the gate/approve/re-run loop ([#1026](https://github.com/INTENTIUS/choudoufu/issues/1026)) | `pass=12 fail=0` verdict lines - two jobs could not have started; see below |
| Forgejo | 12.0.4+gitea-1.22.0, `forgejo-runner` v9.1.1, no cloud behind it ([#1027](https://github.com/INTENTIUS/choudoufu/issues/1027)) | checkout, `npm ci`, the pinned binary, `init` and `chant run` all worked; the run ended at the credential call |
| GitLab | CE 17.11.0, `gitlab-runner` 17.11.0, docker executor, floci behind it, 10 pipelines / 17 jobs ([#1026](https://github.com/INTENTIUS/choudoufu/issues/1026)) | `live-check`, `live-plan`, `live-discover` green unmodified; `live-apply` and `live-adopt` red, both fixed by setup steps below |

What running rather than reading found:

- A container job's default shell on GitHub is `sh`, and the generated
  `live-apply` and `live-adopt` jobs both open with `set -o pipefail` and used
  to declare no shell of their own, so two of the five GitHub jobs failed with
  `Illegal option -o pipefail` before chant even started. Fixed in chant 0.62.0
  (chant#2299): the gated step now declares `shell: bash`.
- A gate resolution binds nothing about the plan it approved: approve,
  rename a resource, re-run `live-apply`, and it applies with no refusal.
  The exit-3 refusal above guards a narrower window, Plan-to-Apply only
  (chant#2300).
- No CI checkout sets a git identity, and chant's gate writes a commit to
  record its resolution, so on GitLab `live-apply` and `live-adopt` die at a
  bare `Op "live-apply" failed after 43.8s` with no error line and no
  failing step (chant#2301, forge-independent, not yet checked on the other
  two).
- `live-adopt` had no Init phase, and its Ledger step needs the provider
  schema for marker discovery, so on any fresh checkout - every CI checkout -
  it failed with `Error: Provider unavailable for marker discovery`
  (forge-independent; the local smoke script missed this because `live-apply`
  ran first there and installed the provider). Fixed in chant 0.62.0
  (chant#2302): `TerraformAdoptOp` now has its own Init phase.
- The approve-then-re-run loop does not close on GitLab: a GitLab checkout
  fetches the pipeline's own ref and nothing else, so a re-run after `chant
  approve` reads an empty ledger and gates again, and that same clone's
  `chant approve` then force-writes `chant/lifecycle`, discarding the
  pending record it never fetched (chant#2303).
- `environment: production` is an audit trail on GitLab CE, not a gate:
  protected environments and deployment approvals both answer 404 there
  (Premium only), yet every `live-apply` job still records a deployment
  against it, so a gated run being green (`--gated-exit 0`) reads to GitLab
  as a successful deployment to production that changed nothing.
- `CI_JOB_TOKEN` returns 401 on GitLab's notes API, so `GITLAB_TOKEN`
  (masked, scope `api`) is required for `live-plan`'s note, not a fallback -
  and a protected variable is silently absent from a merge-request job built
  off an unprotected branch, the same symptom and the same 401, with no line
  naming either cause.

None of the three runs had an AWS account behind it: the dialect is proven
and the credential exchange is not. GitLab's `id_tokens:` role assumption
follows GitHub's shape, but floci's static credentials shadowed it end to
end, so no STS call was made ([#807](https://github.com/INTENTIUS/choudoufu/issues/807)
Q2 stays open). Forgejo has no OIDC surface to reach for at all - it ships
static keys and says so - which is the credential model worth replacing
outright rather than the one waiting on a proof. Since #1028, at least each
job holds only the key pair its own Op needs, rather than one pair shared by
every job in the file.

[`examples/ci-pipelines/README.md`](https://github.com/INTENTIUS/choudoufu/blob/main/examples/ci-pipelines/README.md#what-each-forge-gets-and-what-it-refuses)
and its [smoke workflow](https://github.com/INTENTIUS/choudoufu/blob/main/.github/workflows/ci-pipelines-smoke.yml)
header comment carry the full detail, and
[`examples/ci-pipelines/e2e/gitlab/README.md`](https://github.com/INTENTIUS/choudoufu/blob/main/examples/ci-pipelines/e2e/gitlab/README.md#four-things-the-run-found)
carries the GitLab run in full.

## Governance

[`examples/pipeline-governance`](https://github.com/INTENTIUS/choudoufu/tree/main/examples/pipeline-governance)
holds one warden policy per forge - `github`, `forgejo` and `gitlab`
([#1008](https://github.com/INTENTIUS/choudoufu/issues/1008)) - written
against the five job names above. `live-check` and `live-plan` are required
checks before a pull request can merge; `main`, `staging` and
`chant/lifecycle` are all protected branches on every forge
([#1024](https://github.com/INTENTIUS/choudoufu/issues/1024)); the apply
credential is required to be present without its value being read.

GitLab's policy takes its own shape rather than reskinning the other two:
gitlab-warden's config is a flat `nodes:` map rather than the
`orgs: -> repos:` shape the other two share, and two of its rules - the
approval requirement and the protected environment - are Premium-only and
answer 404 on CE, so the policy declares them and says so rather than
pretending they converge. [Where GitLab differs, and why](https://github.com/INTENTIUS/choudoufu/blob/main/examples/pipeline-governance/README.md#where-gitlab-differs-and-why)
has the rest, including the presence-only `GITLAB_TOKEN` variable and its
own apply-time trap.

The approval of record is chant's gate, not the forge's. A forge-side
reviewer stacks on top of it: `live-apply`'s spec carries
`environment: { name: "production" }`, so the generated GitHub and GitLab
jobs both declare it, and GitHub's policy gates the job with a required
reviewer. GitLab CE cannot, per "Per forge" above. Forgejo Actions has no
environments at all, and its generated file says so in a header comment.

What keeps all three current is the currency guard in `examples/ci-pipelines`:
`npm test` regenerates into a scratch directory and diffs the checked-in
workflows byte for byte, so an edited or orphaned workflow fails loudly. That
needs node, which this repository's Go CI does not have, so
[`live/ci_pipelines_test.go`](https://github.com/INTENTIUS/choudoufu/blob/main/live/ci_pipelines_test.go)
is the backstop: every workflow tracked, named to its own Op and forge, the
choudoufu install pinned and checksum-verified, and the per-forge policy
check reading the real forge list rather than a hardcoded pair.

## Before the first run

Each forge's generated YAML reads a set of variables, secrets and platform
objects that nothing in this project provisions. Create these before
pointing a trigger at the generated workflow; the names below are read out
of the generated trees themselves, not assumed.

| | GitHub | Forgejo | GitLab |
|---|---|---|---|
| Region | `AWS_REGION` repository variable, read by every job | `AWS_REGION` repository variable | `AWS_REGION` project CI/CD variable |
| Credentials | 3 role ARNs as repository variables (`CHOUDOUFU_PLAN_ROLE_ARN` for `live-plan`/`live-discover`, `CHOUDOUFU_ADOPT_ROLE_ARN` for `live-adopt`, `CHOUDOUFU_APPLY_ROLE_ARN` for `live-apply`) plus matching IAM roles, OIDC-trusted for `pull_request`+`refs/heads/main` (plan role), `refs/heads/staging` (adopt role), `refs/heads/main` (apply role); `live-check` needs none | 3 static key pairs as repository secrets, one per job that needs one (`CHOUDOUFU_PLAN_ACCESS_KEY_ID`/`_SECRET_ACCESS_KEY` for `live-plan`/`live-discover`, `CHOUDOUFU_ADOPT_*` for `live-adopt`, `CHOUDOUFU_APPLY_*` for `live-apply`) - job-scoped since #1028, so `live-check` holds none | the same three role ARNs as project CI/CD variables (unprotected, or paired with protected source branches - a protected one is silently absent otherwise) plus IAM roles trusting `aud: $CI_SERVER_URL`; `GITLAB_TOKEN` (masked, scope `api`) for `live-plan`'s note and `live-discover`'s issue, required rather than a fallback |
| Compute | - | a runner registered under the `docker` label, reachable to `https://code.forgejo.org/actions/checkout@v4` (every job's first step) | `gitlab-runner` on the docker executor |
| Apply gate | `production` environment, required reviewer - `live-apply` declares `environment: { name: production }` and nothing else gates it | none - Forgejo Actions has no environments | `production` protected environment; Premium only, 404 on CE, where chant's own gate is the only control |
| Governance | [`examples/pipeline-governance/github`](https://github.com/INTENTIUS/choudoufu/tree/main/examples/pipeline-governance/github)'s branch rules | [`examples/pipeline-governance/forgejo`](https://github.com/INTENTIUS/choudoufu/tree/main/examples/pipeline-governance/forgejo)'s branch rules | [`examples/pipeline-governance/gitlab`](https://github.com/INTENTIUS/choudoufu/tree/main/examples/pipeline-governance/gitlab)'s rules |

GitLab also needs a git identity for the jobs - two `git config` lines in a
`default: before_script:` on the consumer `.gitlab-ci.yml` (chant#2301) -
and the `chant/lifecycle` ref fetched in the job, or the approve-then-re-run
loop reads an empty ledger and gates again (chant#2303). Also a Pipeline
Schedule (Settings > CI/CD > Schedules) with its `CHANT_SCHEDULED_OP`
variable set to `live-discover`, since the job's own `rules:` only fire on a
scheduled pipeline carrying that value, and the `include:` of the generated
file in the project's own `.gitlab-ci.yml` - GitLab does not read
`ops.gitlab-ci.yml` on its own.

Forgejo also needs write access, treated as the right to start
`live-apply`: the dispatch endpoint does not check that a workflow declares
`workflow_dispatch:`, so the missing "Run workflow" button on
`live-apply.yml` is not the surface to reason about
([#1027](https://github.com/INTENTIUS/choudoufu/issues/1027)) - what holds
the apply back is chant's own gate. And somewhere else to serialize applies
if two running at once would hurt: `concurrency:` is inert on Forgejo
12.0.4, so two `live-apply` runs on different refs proceed side by side, and
two on the same ref end with the earlier one cancelled mid-step rather than
queued, the opposite of what `cancel-in-progress: false` reads as promising.
