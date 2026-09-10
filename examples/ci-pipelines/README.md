# ci-pipelines

The CI a choudoufu estate needs, as one chant project rather than one hand-written
YAML file per forge. Five Ops over one live root; the GitHub, Forgejo and GitLab
pipelines are all generated from them and checked in beside them, under a guard
that regenerating leaves the tree clean. GitLab's generator was cron-only through
chant 0.59.0 and got the other four Ops's triggers in chant #2268 (0.60.0), so
this project pins 0.60.0 and all three forges are generated the same way now -
see below.

Nothing here is a template you fill in. It is a project that builds, whose five Op
names are also the five job names a branch-protection rule or a warden policy can
require: `live-check`, `live-plan`, `live-apply`, `live-adopt`, `live-discover`.

## The five Ops

| Op / job | Trigger | What it runs | What it may do |
|---|---|---|---|
| `live-check` | pull request to `main` | `init`, then `choudoufu live-check -json`, then an assertion over its answer | nothing. No cloud call, no state, no credential |
| `live-plan` | pull request to `main` | `init`, then `choudoufu live-plan -detailed-exitcode -json` | read the live system, and post the plan |
| `live-adopt` | push to `staging` | check, adoption ledger, gate, then the marker writes | write two tags per adoptable resource, after an approval |
| `live-apply` | push to `main` | `init`, `plan -out`, gate on `show`, `apply <planfile>` | change the estate, after an approval |
| `live-discover` | cron `0 6 * * *` | `live-ls -consistent`, then the adoption ledger, then a report | read the account, and open an issue |

Three of them are read-only. The two that write are the two on a push trigger, and
both stop at a gate first.

### live-check

The cheapest job in the pipeline, and the first one a pull request should fail on.
`choudoufu live-check` makes no cloud calls, reads no state and needs no estate,
which is why its generated job assumes no role, is granted no `id-token`, and holds
no secret. Its whole cost is a checkout, an install and a parse.

It runs `init` first because choudoufu's own help says so: provider schemas admit
resource types the built-in admission table does not carry, and without them those
types read as refused. `init` reaches the provider registry, not AWS.

The second step is there because chant's `choudoufuLiveCheck` activity deliberately
does not throw on a refusal. It carries the answer back as `refused: true` with
choudoufu's own report, which is the right shape for an activity and the wrong shape
for a gate, so the Op adds the assertion the pull request needs and reads the first
step's own output for it.

### live-plan

`live: true` swaps the plan step from `terraformPlan` to `choudoufuLivePlan`, and
that is the whole difference between a stock watch and this one. A stock plan
answers one question, and a live plan answers three off a single read, because
ownership is a pair of tags on the resources rather than an entry in a state file:

- **Drift**, from `live-plan -detailed-exitcode` coming back 2;
- **Unowned**, live resources sitting at a declared identity with no marker;
- **Adoptable**, the subset an exact content match makes claimable.

Only the human render leaves the Op. The `-json` document stays inside it, because it
carries live identities and attribute values for everything the run touched.

### live-apply

`gate: "always"`, not the default `on-destroy`: this is the authoritative position on
the dial, so every merge stops for an approval rather than only the ones that would
destroy something.

The gate is a fact on a ledger, not a wait. The push run reaches it, finds no
resolution, records the pending fact and ends; it does not hold a runner open.
`chant approve live-apply approve-live-apply --approver you` writes the resolution,
and re-running the workflow walks through the gate and applies. That is the approval
of record.

The re-run only reads that resolution if the job's checkout carries the
`chant/lifecycle` ref, and on GitLab it does not: see "What the generated file does on
a real GitLab". A forge-side environment reviewer stacks on top of it: the spec in
`generate.ts` carries `environment: { name: "production" }` (chant #2264), so
the generated GitHub and GitLab jobs both declare `environment:` and a
protection rule on that environment - `examples/pipeline-governance/github/governance.yml`'s
reviewer, on GitHub - holds the job before any step runs, ahead of the chant
gate below. Forgejo Actions has no environments at all, so its dialect drops
the key and says so in a header comment on the generated file rather than
silently losing the gate the option exists for.

A gated run exits 3, which on a push job would paint `main` red on every merge until
someone approves. The generated push jobs run with `--gated-exit 0`, which maps that
one outcome and nothing else, and on GitHub a follow-up job posts the pending gate on
the pull request the pushed commit belongs to. A broken apply is still red.

The plan file crosses the gate. On a live root `apply <planfile>` does not replay the
file: prior state is rebuilt from the live system every run, so the apply re-plans and
then compares its fresh plan against the approved one, down to the planned values, and
refuses at exit status 3 when they disagree. chant maps that status to a result rather
than a thrown error, so the run comes back refused rather than broken.

### live-adopt

Reconcile, which on a choudoufu estate is adoption rather than regeneration. Stock
Terraform has no typed path from a live resource back to HCL. On a live root the
question is different rather than harder: `live-plan` already names every live
resource that exactly matches a declared block and carries no marker, and it prints
the two tag values that would claim it. So reconcile here is a tag write. Ownership
reconciles back to the estate; not one line of source changes.

An address more than one live resource sits at is reported and never written. A wrong
marker is silent and adopts or displaces a real object, where a refusal is loud.

`choudoufu live-import` is the other path and is deliberately not an Op. An estate
that still has its `terraform.tfstate` should run it once by hand: it reads every
instance, index and key included, straight out of the state file, which is the blind
spot content matching cannot cover.

**This Op does not run on a fresh checkout, which is every CI checkout.**
`TerraformAdoptOp` builds Check, Ledger, Gate, Adopt and no Init, and the Ledger step
needs the provider schema for marker discovery. On GitLab, on a clean clone, that is

```
[phase] Ledger
  ✗ choudoufuLivePlan(root=estate, adoptionOnly=true)   90.7s
│ Error: Provider unavailable for marker discovery
```

every time. One `choudoufu init` in the root beforehand is enough to reach the gate.
The local smoke does not catch it because it runs `live-apply` first, whose Init phase
has already installed the provider into the same working tree. The fix belongs in
chant's composite rather than in a generated pipeline, so nothing here works around
it; chant #2302, with both halves of the measurement in `e2e/gitlab/README.md`.

### live-discover

The job that catches a resource somebody created out of band and nobody ever tagged.
Nothing else in the pipeline is looking, because `live-plan` only runs on a pull
request and so only ever sees an estate somebody is already editing.

Its cron lives on the Op itself, not in `generate.ts`: `generateOpsPipeline` copies a
discovered Op's own cadence onto its spec, so there is one place to change it.

## The per-environment dial

chant's lifecycle dial is observe, reconcile, authoritative, chosen per environment.
Here it is which Op an environment runs, not three copies of the root:

- **dev observes.** Every pull request runs `live-check` and `live-plan`. Neither can
  change anything: one makes no cloud call at all, the other only ever plans.
- **staging reconciles, behind its gate.** A push to `staging` runs `live-adopt`,
  which writes ownership markers and nothing else, after an approval.
- **production applies, behind its gate.** A push to `main` runs `live-apply`.
- **the account is swept regardless.** `live-discover` runs on its cron.

One root, one estate, five Ops. Splitting into per-environment roots is a change to
`chant.config.ts`'s `roots` and to the `root` each Op names, not a change to this
shape.

## Generating

```bash
npm install
npm run generate        # all three forges
npm test                # the assertions, and the currency guard
```

`generate.ts` runs once per forge, in its own process, and the forge comes from
`CHANT_FORGE` rather than from an argument. That is not stylistic. An Op's finding
mode is baked into the Op at build time, `src/forge.ts` reads the variable at module
load, and a second `import()` of the same Op file in one process is served from the
ESM module cache, so one process can only ever build the Ops one way. The generated
workflow sets `CHANT_FORGE` in its own top-level `env:`/`variables:` for the same
reason: the Op the runner builds has to be the Op the workflow was generated from, or
the permissions the workflow grants describe an Op nobody runs.

The generated trees are:

```
github/.github/workflows/*.yml
forgejo/.forgejo/workflows/*.yml
gitlab/ops.gitlab-ci.yml
```

plus `generated-from.json`, the record of which input state they were generated from
(see the currency guard below). GitHub and Forgejo get one file per Op, because their
trigger is workflow-scoped; GitLab gets one combined file with all five jobs in it,
because its trigger is job-scoped (`rules:` on the job, not `on:` on the file), so
there is nothing to split into separate files the way the other two are split.

Each is a repository root as a consumer would lay it out. Copy the contents of
`github/` (or `forgejo/`, or `gitlab/`) into the repository that holds your chant
project, where the project sits at the root, since the jobs run `npm ci` and
`npx chant run` there. GitLab's file is not named `.gitlab-ci.yml`, because a
project that already has one keeps its own jobs; add these with an `include:`

```yaml
include:
  - local: 'ops.gitlab-ci.yml'
```

or rename the file to `.gitlab-ci.yml` if the project has none of its own yet.

## What each forge gets, and what it refuses

**GitHub** gets all five jobs with everything on: the `pull_request` and `push`
triggers, least-privilege `permissions:` computed per finding mode, `id-token: write`
added on top for OIDC, the plan posted as one pull-request comment that the next push
edits in place, the gated-apply notice job, the scheduled sweep opening an issue, and
`live-apply` deploying to the `production` environment.

**Forgejo** gets all five jobs too, because its Op generator reuses GitHub's builder,
so the triggers and the `--gated-exit 0` mapping cross over unchanged. `live-plan`
posts a pull-request comment the same way GitHub's does - chant #2291 built
`reconcilePr`'s `comment` mode from `GITHUB_API_URL`, which a Forgejo Actions job
already sets correctly, so the mode that used to be refused by name now crosses over
unchanged. Three things still do not cross over:

- **`permissions:`**, which the Forgejo runner ignores. The generator drops the whole
  section rather than emitting a control nothing reads. `id-token: write` goes with it,
  which is the honest answer: a Forgejo runner issues no OIDC token off a workflow
  permission.
- **`environment:`**, because Forgejo Actions has no environments at all - no
  protection rule, no required reviewer, no per-environment secret. The generator
  drops the key from `live-apply`'s job and writes a header comment saying so, rather
  than emitting a control that names an object the instance does not have.
- **the gated-apply notice job**, which shells to `gh` against the GitHub API.

`live-discover` stays in `report` mode on Forgejo: `issue` (`gh issue create`) is not
refused there by name, but only `comment`'s endpoints were verified against a real
instance (#1027), so `issue` remains un-refused-but-unverified and this project does
not turn it on.

**GitLab** gets all five jobs now too (chant #2268), in the one file the next section
describes. `live-plan` posts a merge-request note the way GitHub's posts a
pull-request comment - `comment` mode reaches GitLab's own REST API directly rather
than shelling to `gh` - `live-discover` opens and edits an issue the same way GitHub's
does (chant #2292 gave `reconcilePr`'s `issue` mode its own GitLab REST path), and
`live-apply` deploys to the `production` environment the same way GitHub's does.
Three things differ from GitHub, all consequences of GitLab CI's own shape rather
than of a chant refusal:

- **One file, not five.** A GitLab trigger is job-scoped: every job's own `rules:`
  decides whether it runs, so there is one document with five jobs in it rather than
  five separate workflow files.
- **No `uses:` step.** GitLab CI runs `script:` lines only, so a role assumption is a
  shell script the job runs (see "AWS credentials" below) rather than a marketplace
  action, and there is no gated-apply notice job - it would need a `uses:`-shaped
  forge API call this dialect has no shape for.
- **`live-plan` and `live-discover` both need `GITLAB_TOKEN`.** Neither shells to
  `gh`; both go over GitLab's own REST API with the same token resolution
  (`gitlabNoteTokenFrom`), so both need the CI/CD variable described under "AWS
  credentials" below.

### What the carried-over GitHub keys do on a real Forgejo

Observed on Forgejo 12.0.4+gitea-1.22.0 with `forgejo-runner` v9.1.1, one repository,
one `docker`-labelled runner (#1027). The four keys the forgejo dialect inherits from
GitHub's builder do not all mean there what they mean on GitHub:

- **`concurrency:` is not read.** Two `workflow_dispatch` runs of one workflow sharing
  `group: probe-serial` with `cancel-in-progress: false` overlapped in full
  (`04:52:19-04:53:19` and `04:52:21-04:53:21`), and a third overlapped both. Two
  pushes 34 seconds apart do cancel the first run, but that is Forgejo cancelling the
  superseded runs of a ref on its own (`services/actions.CancelPreviousJobs`): a
  control workflow carrying no `concurrency:` block at all was cancelled by the second
  push exactly as the one carrying `cancel-in-progress: false` was. The Forgejo 12.0.4
  binary contains no `cancel-in-progress` string. So two `live-apply` runs on the same
  branch do not overlap, and two on different refs are not held apart by the group.
- **`workflow_dispatch:` works.** Signed in with write access, `live-discover.yml`'s
  page renders the `workflow_dispatch_dropdown` and a "Run workflow" button that
  `live-apply.yml`'s page does not, and
  `POST /api/v1/repos/{owner}/{repo}/actions/workflows/live-discover.yml/dispatches`
  with `{"ref":"main"}` returns 204 and starts a run that reaches `chant run
  live-discover` and the choudoufu binary. The API endpoint does not check the
  trigger, though: the same POST against `live-apply.yml`, which declares no
  `workflow_dispatch`, also returned 204 and ran the job.
- **`GITHUB_STEP_SUMMARY` is set and nothing reads it.** The runner exports
  `/var/run/act/workflow/SUMMARY.md`; appending to it succeeds. Forgejo stores nothing:
  the run's artifact list is empty, the run-view JSON the web UI fetches carries no
  summary field, the REST API has no job resource at all in 12.0.4
  (`/api/v1/repos/{owner}/{repo}/actions/runs/{id}/jobs` is 404), and the server binary
  contains no `step_summary` string. A step summary is visible only if the job also
  prints it to the log, which is why the finding above is the log alone.
- **`outputs:` resolve, and nothing here consumes them.** A second job with
  `needs: producer` read all four values back
  (`CONSUMER sees gated=[true] op=[live-apply] gate=[apply-gate] approve=[chant approve
  live-apply apply-gate]`), so the block is live rather than inert. The REST API does
  not expose it, and this example drops the notice job that would have consumed it, so
  the four keys on `live-apply` and `live-adopt` are cosmetic here.

One correction to the posting bullet above, from the same session. `gh` can be pointed
at a Forgejo instance after all, as long as it is handed a full URL:
`gh api http://forgejo:3000/api/v1/repos/{owner}/{repo}/issues/1/comments` listed and
created comments over plain HTTP and over TLS, and a job's own `${{ github.token }}`
authenticated it, since `github.api_url` inside a Forgejo job is already
`http://forgejo:3000/api/v1`. What returns 404 is the short path form chant serializes,
`gh api repos/{owner}/{repo}/issues/{n}/comments`, which `gh` expands against
`/api/v3`. Chant #2291 taught `reconcilePr` to build the call from `GITHUB_API_URL`
instead, and `live-plan` now posts a sticky comment on Forgejo the same way it does on
GitHub - see "What each forge gets, and what it refuses" above.

## The GitLab pipeline

Through chant 0.59.0 the gitlab Op generator was cron-only, refusing every
`pull_request`/`push` trigger and the `comment` finding mode by name; that is why
`examples/ci-pipelines/gitlab/.gitlab-ci.yml` used to be a hand-written file holding
one job. chant #2268 (0.60.0) gave the generator both triggers and a merge-request
note activity behind `comment`, so this project now generates GitLab exactly as it
generates GitHub and Forgejo - the hand-written file is gone.

It has also been run. `e2e/gitlab/` stands up GitLab CE 17.11.0, a `gitlab-runner`
with the docker executor and the pinned floci emulator on one network, pushes this
file into a project and drives all four of its triggers; "What the generated file
does on a real GitLab" below is that run, and anything in this section that names
17.11.0 is measured rather than read off the generator.

**One file, not five.** GitLab's Op generator returns a single document,
`ops.gitlab-ci.yml`, with all five jobs in it (`generateGitlabOpPipeline`,
in the gitlab lexicon). A GitHub or Forgejo trigger lives on the workflow (`on:`), so
each Op needs its own file; a GitLab trigger lives on the job (`rules:`), so there is
nothing to split into separate files. `generate.ts`'s `WORKFLOW_DIR.gitlab` names a
directory rather than a per-Op path for exactly this reason - the loop that writes
`result.files` does not care how many files a forge's generator returns.

**Triggers**, mapped onto what GitLab's pipeline sources actually distinguish:

| chant trigger | GitLab `rules:` |
|---|---|
| `cron` | `$CI_PIPELINE_SOURCE == "schedule" && $CHANT_SCHEDULED_OP == "<op>"` |
| `pull_request` | `$CI_PIPELINE_SOURCE == "merge_request_event" && $CI_MERGE_REQUEST_TARGET_BRANCH_NAME == "<branch>"` |
| `push` | `$CI_PIPELINE_SOURCE == "push" && $CI_COMMIT_BRANCH == "<branch>"` |

GitLab still has no in-file cron: a schedule is a project-level object that runs the
project's existing pipeline file with a chosen cron and CI/CD variables, so
`live-discover`'s cron trigger becomes a job gated on `$CI_PIPELINE_SOURCE ==
"schedule"` plus the selector variable, exactly as it did before #2268 - only now it
sits beside four jobs with real event triggers rather than alone.

**`live-plan` posts a merge-request note.** `findingMode: "comment"` is `reconcilePr`
either way, and since chant #2268 that activity reaches GitLab's own notes REST API
directly - a plain `fetch` - rather than shelling to `gh`. It needs a `GITLAB_TOKEN`
CI/CD variable, masked, scope `api`; nothing on the project provisions this by
default, and a project access token is the least-privilege way to create it (chant's
own `gitlabNoteTokenFrom`, in `packages/core/src/op/activities/reconcile.ts`, reads
`CHANT_GITLAB_TOKEN` then `GITLAB_TOKEN` first). The job's own `CI_JOB_TOKEN` is read
as a fallback, and on a stock instance it does not work: on GitLab CE 17.11.0, with
`GITLAB_TOKEN` removed, the Report step came back
`answered 401: {"message":"401 Unauthorized"} (token from CI_JOB_TOKEN, sent as
JOB-TOKEN)` and failed the whole job, so the merge request got a red pipeline rather
than a log-only finding. The notes API is not on the default job-token allowlist.
Treat `GITLAB_TOKEN` as required. No `gh` install, no GitHub token, and the same
hidden-marker edit-in-place recipe as the GitHub comment: one note per merge
request, updated on every push rather than stacked. `live-discover` runs in `issue`
mode on GitLab too (chant #2292): the same token resolution, over the issues REST API
rather than the merge-request notes one, since a cron job has no merge request to
post a note on.

**A protected CI/CD variable does not reach an unprotected branch's pipeline.**
GitLab strips a variable marked "Protect variable" from any pipeline whose source
branch is not itself protected - a merge-request pipeline off a feature branch is
exactly that case. Mark `GITLAB_TOKEN` and the three `CHOUDOUFU_*_ROLE_ARN`
variables unprotected, or protect the source branches that open merge requests
against `main`, or `live-plan` silently loses all four on the merge-request jobs
that read them - it degrades to no token and no role to assume, not a build-time
refusal. Push-to-`main` and push-to-`staging` jobs are unaffected either way, since
those only ever run from a protected branch.

Reproduced on 17.11.0: `GITLAB_TOKEN` recreated with `"protected": true`, a merge
request opened from the unprotected branch `feature/retention`, and the job's only
symptom was the 401 above - chant fell through to `CI_JOB_TOKEN` because, from
inside the job, the variable simply was not there. Flipping the same variable to
unprotected and pushing again: `Op "live-plan" completed in 25.5s`, with the note
updated in place. Nothing in the job log names the stripped variable, which is why
this is worth checking before believing a token is misconfigured.

**`live-apply` deploys to the `production` environment.** GitLab has the same
`environment: { name, url? }` key GitHub does, with its own protected-environment
approval rule behind it (Settings > CI/CD > Protected environments) - chant's
`environment` option (#2264) maps onto it the same way it maps onto GitHub's. Nothing
in this project's own `.gitlab-ci.yml` can create that protection rule; it is project
configuration, the same way a GitHub environment's reviewer is repository
configuration.

On GitLab CE there is no such rule to create. Protected environments and deployment
approvals are Premium: on 17.11.0 CE, `GET /api/v4/projects/1/protected_environments`
and `GET /api/v4/deployments/3/approval` both answer `404`. The key is not inert - the
`production` environment is created on the first pipeline and every `live-apply` job
records a deployment against it - but it is an audit trail, not a gate, and chant's
own gate is the only thing holding the apply. Read the deployment list carefully while
you are at it: `--gated-exit 0` makes a gated run a green job, and GitLab records a
green job that deploys nowhere as a **successful** deployment to `production`. Three
deployments to `production` on that instance read `success`; one of them applied
something and two of them stopped at the gate.

**No `uses:` step and no gated-apply notice job.** GitLab CI runs `script:` lines
only; a `setup` entry spelled `{ uses }` is a GitHub Actions marketplace action, which
chant's gitlab generator refuses by name (chant #2242) rather than silently dropping,
and `assumeRole()` in `generate.ts` has a GitLab-specific `{ run }` branch for exactly
that reason (see "AWS credentials" below). The gated-apply notice job that follows
`live-apply`/`live-adopt` on GitHub does not cross over either: it shells to `gh`
against the GitHub API, which nothing on GitLab can reach. What does cross over is the
half that needs no forge API - `live-apply` and `live-adopt` still run with
`--gated-exit 0`, so a run that stops at its gate is a green pipeline rather than a
red one, and the pending-gate block that GitHub Actions writes to
`GITHUB_STEP_SUMMARY` lands in GitLab as a downloadable `chant-gate-<job>.md`
artifact instead, since GitLab has no step-summary surface to write it to.

**`CHANT_FORGE: gitlab`.** `src/forge.ts` takes three values, and each generated job
sets the one it was built for, in the file's own top-level `variables:`. `github`
posts through `gh`; `gitlab` posts through its own REST API, on `live-plan` (a
merge-request note) and `live-discover` (an issue, chant #2292); `forgejo` posts a
`live-plan` comment through `gh` built from `GITHUB_API_URL` (chant #2291) and reports
everywhere else, since `live-discover`'s `issue` mode is unverified there (see "What
each forge gets, and what it refuses"). The value is never left unset: unset defaults
to `github`, whose `live-discover` opens a GitHub issue and would fail on every
scheduled run on another forge. It said `forgejo` before this project could generate
for GitLab at all (#986), which was true of the build and false about the run -
`forgejo` was then the only report-only value on offer; #807 gave GitLab its own.

### What the generated file does on a real GitLab

GitLab CE **17.11.0** (revision `5e1517f7b46`, `enterprise: false`), **gitlab-runner
17.11.0** with the docker executor, the pinned floci emulator, and the example's own
`terraform/` root as the estate. Ten pipelines, seventeen jobs; the harness and the
full record are in `e2e/gitlab/`.

Three of the five ran green on the generated file with nothing changed:
`Op "live-check" completed in 17.6s` on a merge request, `Op "live-plan" completed in
29.9s` with `[outcome] Comment=…/merge_requests/1#note_1`, and `Op "live-discover"
completed in 27.5s` off a Pipeline Schedule carrying `CHANT_SCHEDULED_OP`. A second
push to the same merge request updated note 1 in place rather than adding a second
one, which is the edit-in-place recipe this file claims.

The other two failed, and one of the two reasons is not GitLab's:

**A GitLab CI checkout configures no `user.email`/`user.name`, and chant's gate needs
one.** The gate records its pending fact as a commit on `chant/lifecycle`, so with no
identity the write fails and the job ends at

```
[phase] Plan
  ✓ terraformPlan(root=estate, planFile=chant.tfplan)   21.1s
    [outcome] Changed=true
Op "live-apply" failed after 43.8s
```

with no error line, no failing step record and exit 1. Two `git config` lines ahead of
`npx chant run` turn the same job into `Op "live-apply" is gated on
"approve-live-apply" after 40.6s` and exit 0, with `chant-gate-live-apply.md` uploaded.
`e2e/gitlab/overlay/.gitlab-ci.yml` sets them in a `default: before_script:`, which
reaches every generated job without editing the generated file. Nothing in any of the
three dialects sets an identity, so this is not a GitLab-only gap (chant #2301).

**The approve-then-re-run loop does not close.** `chant approve` writes the resolution
and pushes it, and the retried job gated anyway - it fetches the pipeline's own ref at
depth 20 and nothing else, so it read an empty ledger and recorded a *second* pending
fact (`expires : …T05:15:13Z` on the first run, `…T05:16:39Z` on the retry). The same
commit, in the same image, with `git fetch origin
chant/lifecycle:refs/heads/chant/lifecycle` first, reads `[approved] e2e-operator` and
applies for real: `✓ terraformApply(root=estate, planFile=chant.tfplan) 9.0s`, and the
log group and IAM role are then in the emulator. `GIT_DEPTH: 0` alone is not the fix -
GitLab fetches refspecs, not every branch (chant #2303).

Two smaller things the run settled. The stage was named `scheduled-ops` for all five
jobs at the time, merge-request jobs included, so a merge request's pipeline showed
`live-check` and `live-plan` under a heading that said "scheduled-ops"; chant #2293
(0.62.0) renamed both the stage and the generated file to `ops`. And the
generated `id_tokens:` exports are inert rather than fatal alongside static
credentials: the jobs exported `AWS_WEB_IDENTITY_TOKEN_FILE` and `AWS_ROLE_ARN` and
still planned against floci, because the SDK's env-static provider wins over the
web-identity one, so no STS call was ever attempted.

## AWS credentials

**GitHub: OIDC, and no stored key.** `aws-actions/configure-aws-credentials` is a
`uses:` step, so it rides each Op's `setup` list, and it needs `id-token: write`, so
it rides each Op's additive `permissions`. The run mints a short-lived OIDC token, the
action exchanges it for credentials that expire with the job, and the repository
stores no long-lived key at all.

Three roles, not one, which is the whole reason `setup` is a per-Op option:

| Job | Repository/project variable | What its role needs |
|---|---|---|
| `live-check` | none | nothing. It makes no cloud call |
| `live-plan`, `live-discover` | `CHOUDOUFU_PLAN_ROLE_ARN` | read: describe the declared types, and `tag:GetResources` |
| `live-adopt` | `CHOUDOUFU_ADOPT_ROLE_ARN` | the above, plus the per-service tagging calls that write a marker |
| `live-apply` | `CHOUDOUFU_APPLY_ROLE_ARN` | the above, plus create/update/delete on the declared types, and read/write on the record store's SSM prefix |

`AWS_REGION` is a fourth repository variable, and it is set once in the workflow's
`env:` as both `AWS_REGION` and `TF_VAR_aws_region`. The provider reads the Terraform
variable and choudoufu's own tagging-API sweep reads the SDK one, and one repository
variable means they cannot disagree.

`role-to-assume` reads a variable rather than a secret because a role ARN is not one:
it is an account number and a role name. Set each role's trust policy to this
repository, and for the two write roles to the ref their push trigger fires on.

**GitLab: the same three roles, over its own OIDC surface, and this is
unverified.** GitLab CI has no `uses:` step for `aws-actions/configure-aws-credentials`
to ride - it runs `script:` lines only - so `assumeRole()` in `generate.ts` has a
GitLab-specific branch that assembles the exchange from what a job there already has,
rather than a marketplace action:

1. The job's `permissions: { "id-token": "write" }` (the same additive option GitHub's
   jobs carry) becomes an `id_tokens:` declaration (chant #2257), which lands GitLab's
   own JWT in a `$CHANT_ID_TOKEN` job variable - the *value*, not a file path.
2. The setup script writes that value to a file and points two environment variables
   at it: `AWS_WEB_IDENTITY_TOKEN_FILE` and `AWS_ROLE_ARN` (plus a session name). Every
   official AWS SDK - the Go SDK the Terraform provider and choudoufu's own tagging-API
   client are both built on - already reads those two variables and performs the
   `AssumeRoleWithWebIdentity` exchange itself. No `aws` CLI on the image, no
   hand-rolled STS call: `AssumeRoleWithWebIdentity` is the one STS action that takes
   no request signature, by design, since the identity token itself is what is being
   exchanged.
3. The three project variables and the roles they need are the same table as GitHub's
   above, read the same way (`$CHOUDOUFU_PLAN_ROLE_ARN`, and so on).

This is chant's own documented shape for the option (its `id_tokens:` doc comment
names the AWS STS call by name). The pipeline itself has now run on a real GitLab
(17.11.0, see "What the generated file does on a real GitLab"), but against the floci
emulator with static credentials, which is not an identity provider: GitLab did mint
the JWT and the job did write it to a file, and no STS exchange was ever attempted,
because `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY` were present and the SDK's
env-static provider wins over the web-identity one. So the exports are inert rather
than fatal alongside a static key, and the exchange itself is still unrun - open
question Q2 of issue #807, unresolved. Register the IAM OIDC provider's audience as
`$CI_SERVER_URL` (chant's default, and GitLab's own documented recommendation for an
AWS federation), and confirm the exchange once by hand before relying on it.

**Forgejo: static keys, one pair per Op, and this is unverified for a different
reason.** No OIDC surface is being asked to work here at all: Forgejo Actions has
neither `permissions:` nor `id_tokens:`, so its dialect drops both, and its jobs carry
`AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` from repository secrets instead. Since
#1028 (chant #2290 gave the Op generator a per-Op `variables` option), the pair is a
**job-level** `env:` entry rather than the workflow-wide one it used to be, so a
pull-request job no longer holds a credential that can change the estate:

| Job | Repository secrets | What they can do |
|---|---|---|
| `live-check` | none | nothing. It makes no cloud call |
| `live-plan`, `live-discover` | `CHOUDOUFU_PLAN_ACCESS_KEY_ID` / `CHOUDOUFU_PLAN_SECRET_ACCESS_KEY` | read: describe the declared types, and `tag:GetResources` |
| `live-adopt` | `CHOUDOUFU_ADOPT_ACCESS_KEY_ID` / `CHOUDOUFU_ADOPT_SECRET_ACCESS_KEY` | the above, plus the per-service tagging calls that write a marker |
| `live-apply` | `CHOUDOUFU_APPLY_ACCESS_KEY_ID` / `CHOUDOUFU_APPLY_SECRET_ACCESS_KEY` | the above, plus create/update/delete on the declared types, and read/write on the record store's SSM prefix |

The two write pairs are not the read pair, and `tests/pipelines.test.ts` asserts as
much - the same shape the GitHub role table above is asserted by. What is still
unverified is the credential itself, not its scope: no OIDC path off Forgejo to AWS is
verified anywhere in this organization, so these remain long-lived keys in repository
secrets rather than short-lived role credentials. chant's own forgejo generator notes
the alternative: a Forgejo job can authenticate through whatever the runner already
holds, in which case drop these secrets from `generate.ts` and give the credentials to
the runner instead. That trades a repository secret for a runner-level one and is not
obviously better; it is stated because it is the other real option.

## The estate

`terraform/` is an ordinary AWS root that stock OpenTofu runs unchanged: a CloudWatch
log group and an IAM role, both taggable, and no backend block. What makes it live is
`terraform/estate.chdf.hcl` beside it, a sidecar carrying the live configuration:

```hcl
estate = "ci-pipelines-example"

record_store "ssm" {}
```

The sidecar rather than a `live` block inside `terraform{}` because strict HCL parsers
are right to reject an unknown block there, and the sidecar's extension is one no
stock tool reads. Either form works, and a root may use only one of them.

The `ssm` record store is a CI decision. Every run gets a fresh runner, so the implied
local record store would be empty every time and every instance would fall back to its
marker tags, which is correct and slower. That is the foundation's own rule, not a
workaround: the cache is never consulted for ownership, live always wins, and losing
the record costs a slower run and nothing else. An `ssm` (or `s3`) store is shared and
lives under IAM, which is what a pipeline should declare.

Running this root prints `Resource type has no orphan recovery` warnings under
the v0.15.0 release the generated jobs currently install, and none at all under
v0.16.0. That is [#980](https://github.com/INTENTIUS/choudoufu/issues/980), found
by building this example: the warning was firing for the schema-first admission
path as well as for the type-not-in-the-table path it was written for. It is
fixed in v0.16.0, which fires it only for types nothing can sweep.

`scripts/smoke.sh` counts the warnings on every run and prints the count as its
own verdict line, so the fix is a number rather than a claim. Measured over
`live-check`, `live-plan`, `live-apply`, `live-adopt` and `live-discover`
together: `count=34 per-live-plan=4` on v0.15.0, `count=0 per-live-plan=0` on
v0.16.0. Two corrections to what this file used to say - the warning names
`aws_cloudwatch_log_group` only, never `aws_iam_role`, so it was never "one per
resource"; and a single `live-plan` emits it four times, in the `-json` document
and the human render both.

The IAM role is in the root on purpose. IAM is one of the services whose tagging call
choudoufu does not print a paste-ready adopt command for (Route53 and S3 are the
others), so an unmarked IAM role appears in the adoption ledger as a refusal naming
both marker values rather than as something `live-adopt` claims. That is the honest
half of adoption, and a CI example should carry it.

## Running it locally, against the emulator

All five Ops run end to end against floci with no AWS account. The scripted version
is `scripts/smoke.sh`, or `just smoke-ci-pipelines` from the repository root, which
starts the pinned emulator, runs the five in order, and prints one verdict line per
Op read off that run's own `--json` status:

```
SMOKE op=live-check verdict=pass status=ok
SMOKE op=live-plan verdict=pass status=ok
SMOKE op=live-apply verdict=pass status=gated
SMOKE op=live-apply/approve verdict=pass status=resolved
SMOKE op=live-apply verdict=pass status=ok
SMOKE op=live-apply/plan-moved verdict=pass status=gated
SMOKE op=live-adopt verdict=pass status=gated
SMOKE op=live-adopt/approve verdict=pass status=resolved
SMOKE op=live-adopt verdict=pass status=ok
SMOKE op=live-discover verdict=pass status=ok
SMOKE op=apply-refusal verdict=pass status=exit3
```

It runs in a throwaway repository rather than in this checkout, because chant pushes
its gate ledger to the first configured remote after every gate write. On a forge,
`.github/workflows/ci-pipelines-smoke.yml` (`workflow_dispatch` only) runs the same
script against floci as a service container.

By hand, it is: point the SDK at the emulator, put a `choudoufu` on PATH, and pick
the forge whose finding modes do not need a forge:

```bash
docker run -d --rm -p 4566:4566 "$(cat ../../live/floci-image)"

export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test
export AWS_REGION=us-east-1 AWS_ENDPOINT_URL=http://localhost:4566
export CHANT_FORGE=forgejo   # report mode: nothing tries to post

npx chant run live-check
npx chant run live-plan
npx chant run live-discover
npx chant run live-apply --gated-exit 0 --json   # stops at the gate
```

`live-apply` stops where it should: `"status":"gated"`, the Apply phase skipped, and
the approve command in the record. Approving is `chant approve live-apply
approve-live-apply --approver you`, which is a commit on the `chant/lifecycle` branch;
re-running then applies. `live-adopt` gates the same way, on `approve-live-adopt`.

Two things the emulator run settled that the rest of this file had only asserted
(#1026), and chant#2300 changed one of them. A gate resolution now names the plan
it approved - a `sha256:` digest of the change set, carried on both the pending
fact and the resolution - and `chant approve` copies it from the standing pending
fact by default, so the common path stays one command. A configuration or
live-system change between `chant approve` and the next `chant run live-apply`
re-plans to a different digest, and the run ends `gated` again, naming both
digests (`approved: sha256:...`; `planned: sha256:...`) rather than applying. A
resolution written before chant#2300 carries no digest and does not satisfy a
plan-bound gate, so a gate standing open across the upgrade needs one more
`chant approve` after its next run.

That is the outer guard, spanning runs. The exit-3 refusal is the inner one,
narrower: it is `apply <planfile>` disagreeing with the plan file it was handed,
so it protects the gap between the Op's own Plan phase and its Apply phase inside
a single run. `scripts/smoke.sh` proves both - the digest mismatch by approving,
renaming a resource, and re-running `live-apply`, and the exit-3 case by
tampering between `choudoufu plan -out` and `choudoufu apply`.

## The currency guard

Two of them, because they run in different places. All three forges are generated
now, GitLab included (#807, sub-issue (e)), so both guards treat all three the same
way: "regenerate and diff", never "trust what is on disk".

**`npm test`** is the real one. `tests/currency.test.ts` re-runs `generate.ts` for all
three forges into a scratch directory and diffs byte for byte, in both directions, so
a file that changed and a file that should no longer exist fail equally loudly.
GitLab's single combined file goes through the identical loop as GitHub's and
Forgejo's five-files-each - the check does not care how many files a forge's
generator returns, only that the committed set and the regenerated set agree.
`tests/pipelines.test.ts` reads the checked-in workflows back field by field, asserting
properties a reviewer would want to hold rather than the bytes that happen to be there;
GitLab's assertions are structurally separate from GitHub's and Forgejo's shared ones,
because a GitLab job has no `on:`/`jobs:`/`permissions:` at all - the whole document
is flatter. Both files' blind spot is that they need node and `npm install`, and
choudoufu's Go CI has neither.

**`live/ci_pipelines_test.go`** is the backstop that does run there, using nothing but
git and the files themselves. Its GitHub/Forgejo half (`ciPipelineForges`) is built
around "one tracked file per Op", which is true of those two and not of GitLab, so
GitLab gets a parallel set of tests reading the one file it gets instead: every
workflow is tracked, the set of workflows is exactly the set of `src/*.op.ts` per
forge (or, on GitLab, the one file names every Op by its own job key), each file
names its own forge and its own Op source and sets a matching `CHANT_FORGE`, the
choudoufu install is pinned to a version and verified against the release's published
SHA256 (once per job, on GitLab, since its five jobs share one file), the generator
has been run since the inputs last changed, and no generator input was committed
after the workflows it generates. Its blind spot is stated in the file: with no node
it proves correspondence, input state and ordering, not equality, and a workflow
hand-edited after a regeneration is invisible to all three. Where node is available it
goes on to regenerate and diff, which is the same proof `npm test` gives -
`TestCIPipelineWorkflowsRegenerate` for GitHub and Forgejo,
`TestCIPipelineGitLabRegenerates` for GitLab.

**`generated-from.json`** is how the second-to-last of those is answerable at all.
`generate.ts` writes it at the end of every run: one SHA256 per generator input, which
`live/ci_pipelines_test.go` re-computes and compares. Commit order alone cannot answer
the currency question when an input changes and no emitted byte moves - there is then
nothing to commit beside the source change, and a check asking only "was an input
committed after the workflows" reports stale forever with a remedy that produces no
commit. #807's third forge value (`src/forge.ts` growing `"gitlab"` before this project
could generate for it) was exactly that change when it landed, and #807's later work
generating GitLab is the same shape again. The stamp moves whenever an input moves, so
the remedy is always available, and it catches a source change that never went through
the generator even when the two land in one commit. It is generated: run
`npm run generate`, never edit it.

All of them were proven red before they were trusted green: a hand-edited workflow, a
deleted one, a `CHANT_FORGE` pointing at the wrong forge, a checksum check replaced by
`cat`, a sixth Op with no workflow, a generator input committed after the workflows,
a `CHANT_FORGE` no forge list accepts (both guards, which is how the third value was
proven necessary before it was written), a generator input edited without
regenerating, a recorded hash altered by hand, a stamp removed from git, and a new
file under `src/` the stamp does not record. Repinning to chant 0.60.0 and generating
GitLab (this change) re-proved the same shapes against GitLab's own file: a
hand-edited `gitlab/ops.gitlab-ci.yml` (`npm test`'s `gitlab: and the same
bytes` failed, quoted in the PR), an untracked one (`TestCIPipelineGitLabIsTrackedAndGenerated`),
and `live/pipeline_governance_test.go`'s old
`TestPipelineGovernanceEnvironmentGapIsStillReal`, which was written to fail the day
`live-apply.yml` carried an `environment:` key and did (also quoted in the PR) - the
signal that it was time to replace it with `TestPipelineGovernanceEnvironmentGates`.

## Pinning

The install line every job runs names a choudoufu release by version and verifies it
against the SHA256 that release published. A generated workflow is committed once and
re-run unattended, two of these over a role that can write to the account, so
"whatever was released last" is not a version, and a tag can be moved or an asset
replaced without the run noticing. `gh`, where a job needs it, is pinned the same way.
chant's terraform lexicon refuses a choudoufu older than v0.14.0, the release that made
the `live-plan -json` document reachable on a configuration that names its own estate.

The `uses:` steps (GitHub and Forgejo only - GitLab CI has no such step, see "The
GitLab pipeline" above) are pinned to release tags. chant's generator refuses an
unpinned action reference and refuses a ref that names the action repository's own
default branch, at build time, by name.

## What is not here

- **A GitLab governance policy that covers the environment.**
  `examples/pipeline-governance/gitlab/governance.yml` requires the checks and
  protects `chant/lifecycle` there (#1008, #1021). The `production`
  protected-environment approval rule `live-apply`'s job names is the half it
  cannot carry, and on GitLab CE there is no such object to declare at all - see
  "What the generated file does on a real GitLab".
- **Property-level drift.** chant's terraform lexicon implements entity-level
  observation for a live root and not `observeResourcesDeep()`, so there is no
  property-tree diff and no claimed-field set. `live-plan` is the plan, which is a
  different and older answer to the same question.
- **A verified GitLab-to-AWS OIDC exchange.** The role assumption in "AWS credentials"
  above is chant's documented shape for the option. The pipeline has now run on GitLab
  CE 17.11.0 (`e2e/gitlab/`), but against floci with static credentials, so the STS
  exchange is still unexercised. Open question Q2 of issue #807.
- **The GitLab run, as automation.** `e2e/gitlab/bootstrap.sh` stands the instance,
  runner and emulator up and seeds the project; driving the four triggers and reading
  the verdicts is still by hand, and nothing in `just ci` or `just smoke-ci-pipelines`
  runs any of it.
