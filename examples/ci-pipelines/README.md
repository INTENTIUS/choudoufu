# ci-pipelines

The CI a choudoufu estate needs, as one chant project rather than one hand-written
YAML file per forge. Five Ops over one live root; the GitHub and Forgejo workflows
are generated from them and checked in beside them, under a guard that regenerating
leaves the tree clean. GitLab gets one of the five, hand-written, because chant's
gitlab generator refuses the other four by name - see below.

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
of record. A forge-side environment reviewer stacks on top of it and is not generated
here: `generateOpsPipeline` emits no `environment:` key, so a repository that wants
that second gate adds it to the checked-in workflow and re-adds it whenever the file
is regenerated.

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
npm run generate        # both forges
npm test                # the assertions, and the currency guard
```

`generate.ts` runs once per forge, in its own process, and the forge comes from
`CHANT_FORGE` rather than from an argument. That is not stylistic. An Op's finding
mode is baked into the Op at build time, `src/forge.ts` reads the variable at module
load, and a second `import()` of the same Op file in one process is served from the
ESM module cache, so one process can only ever build the Ops one way. The generated
workflow sets `CHANT_FORGE` in its own top-level `env:` for the same reason: the Op
the runner builds has to be the Op the workflow was generated from, or the
permissions the workflow grants describe an Op nobody runs.

The generated trees are:

```
github/.github/workflows/*.yml
forgejo/.forgejo/workflows/*.yml
```

and there is a third tree that `npm run generate` does not touch:

```
gitlab/.gitlab-ci.yml          # hand-written; one job, on a Pipeline Schedule
```

Each is a repository root as a consumer would lay it out. Copy the contents of
`github/` (or `forgejo/`, or `gitlab/`) into the repository that holds your chant
project, where the project sits at the root, since the jobs run `npm ci` and
`npx chant run` there.

## What each forge gets, and what it refuses

**GitHub** gets all five jobs with everything on: the `pull_request` and `push`
triggers, least-privilege `permissions:` computed per finding mode, `id-token: write`
added on top for OIDC, the plan posted as one pull-request comment that the next push
edits in place, the gated-apply notice job, and the scheduled sweep opening an issue.

**Forgejo** gets all five jobs too, because its Op generator reuses GitHub's builder,
so the triggers and the `--gated-exit 0` mapping cross over unchanged. Three things do
not:

- **`permissions:`**, which the Forgejo runner ignores. The generator drops the whole
  section rather than emitting a control nothing reads. `id-token: write` goes with it,
  which is the honest answer: a Forgejo runner issues no OIDC token off a workflow
  permission.
- **the gated-apply notice job**, which shells to `gh` against the GitHub API.
- **every posting mode.** `comment`, `issue` and `pull-request` are all chant's
  `reconcilePr` activity, and that activity shells to `gh`. chant carries no Forgejo
  client and no way to point `gh` at a Forgejo instance, which is why the forgejo
  generator refuses `comment` by name. `issue` is not refused there, but it is the
  same `gh issue create`, so an Op carrying it on Forgejo would generate cleanly and
  fail at its Report step on every run. This example does not ship that: on Forgejo
  both reporting Ops run in `report` mode, where the finding is the run's own log and
  its step summary.

**GitLab** gets one job of the five, `live-discover`, hand-written in
`gitlab/.gitlab-ci.yml`. The next section is what it is and why it is not generated.

## The GitLab pipeline

chant has a gitlab Op generator, and it is cron-only. Handed this project's five
specs it refuses four of them by name, before it emits any YAML:

| Op | What the generator says |
|---|---|
| `live-check`, `live-plan` | `has a "pull_request" trigger, but GitLab has no pull_request/push event model (#2084) - only a project-level Pipeline Schedule (cron)` |
| `live-adopt`, `live-apply` | the same refusal, for their `push` trigger |
| `live-plan`, again | `has findingMode "comment" ... GitLab has no pull_request event and chant has no GitLab merge-request note activity (#2231)` |

and it refuses two of the per-Op options the other forges use, for the reason there
is no `permissions:` block on a GitLab job at all (chant #2242): a `setup` step
spelled `uses:` is a GitHub Actions marketplace action and GitLab CI runs `script`
lines only, and an additive `permissions` map has no per-job token-scope mapping to
go into - GitLab's OIDC surface is a separate `id_tokens:` declaration the job
exchanges itself, which chant does not generate.

`live-discover` is the one Op that crosses: a cron trigger and, on this forge, no
posting mode. So GitLab gets the nightly sweep and nothing else. There is no
pull-request gate, no gated apply and no adoption run there; a project on GitLab that
wants those runs them somewhere with an event model, or waits for the chant work
filed as #807's sub-issue (e).

The job body in `gitlab/.gitlab-ci.yml` is the shape that generator emits for
`live-discover` - one `stages`, one selector rule, one `chant run` - with the pinned
install line and the credentials added. It is hand-written rather than generated
because generating one job out of a five-Op project whose other four throw would put
a file here that `npm run generate` cannot reproduce, and a generated file nothing
regenerates is the failure the currency guards exist to prevent. The guards therefore
treat it as tracked and hand-written, and assert it never grows a banner claiming
otherwise.

**What to set up.** GitLab has no in-file cron: a schedule is a project-level object
that runs the project's existing `.gitlab-ci.yml`. Create one Pipeline Schedule
(Settings > CI/CD > Schedules) with the cron `src/live-discover.op.ts` declares and
the selector variable the job's rule reads:

```
Description:  live-discover
Interval:     0 6 * * *
Variables:    CHANT_SCHEDULED_OP = live-discover
```

A scheduled pipeline with a different selector runs nothing from this file, and a
push or merge-request pipeline produces no job from it at all.

**`CHANT_FORGE: forgejo`, on GitLab.** `src/forge.ts` takes two values: `github`,
where a finding is posted through `gh`, and `forgejo`, which is report-only - the
finding is the job's own log. GitLab is a third report-only instance, because every
posting mode chant has is the same `reconcilePr` activity shelling to `gh` and there
is no merge-request note activity (chant #2231). The job therefore builds the forgejo
Ops, and says so rather than leaving the variable unset: unset defaults to `github`,
whose `live-discover` opens a GitHub issue and would fail on every scheduled run.
Giving `gitlab` its own value in `src/forge.ts` is the change to make the day that
generator grows the triggers this file works around; `src/` is a generator input, so
it belongs with a regeneration rather than with a hand-written file.

## AWS credentials

**GitHub: OIDC, and no stored key.** `aws-actions/configure-aws-credentials` is a
`uses:` step, so it rides each Op's `setup` list, and it needs `id-token: write`, so
it rides each Op's additive `permissions`. The run mints a short-lived OIDC token, the
action exchanges it for credentials that expire with the job, and the repository
stores no long-lived key at all.

Three roles, not one, which is the whole reason `setup` is a per-Op option:

| Job | Repository variable | What its role needs |
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

**Forgejo: a static key, and this is unverified.** No OIDC path from Forgejo (or
GitLab) to AWS is verified anywhere in this organization, so the Forgejo workflows
carry `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` from repository secrets. Two
things to know before using them:

1. The generator's `variables` become the workflow's **top-level** `env:`, so the
   credentials are workflow-scoped. `live-check` sees them on Forgejo even though it
   needs none. That property is not something this pipeline shape provides on GitHub
   either: what provides it there is per-job OIDC minting. `tests/pipelines.test.ts`
   asserts it rather than hiding it.
2. chant's own forgejo generator notes the alternative: a Forgejo job can authenticate
   through whatever the runner already holds, in which case drop the two secrets from
   `generate.ts` and give the credentials to the runner. That trades a repository
   secret for a runner-level one and is not obviously better; it is stated because it
   is the other real option.

**GitLab: a static key too, and unverified for the same reason.** Set
`AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` as masked, protected project CI/CD
variables on a role that only needs to read - `live-discover` sweeps the tagging API
and reads the adoption ledger, and writes no marker and no resource. They are not
repeated under `variables:` in the file, because a GitLab CI/CD variable is already in
every job's environment; the scoping dial there is the variable's own protection and
environment scope, not the YAML. That is the one place the GitLab shape is better than
the Forgejo one, where the generator's `variables` become a workflow-level `env:` and
`live-check` sees a key it does not need.

GitLab does document an OIDC path - an `id_tokens:` block with an `aud`, exchanged for
credentials by the job through `sts:AssumeRoleWithWebIdentity` - and chant does not
generate it. The file leaves it out rather than sketching it: nothing in this
organization has verified a GitLab-to-AWS exchange, which is open question Q2 of
issue #807, and an unverified auth block that reads as generated is worse than a
static key that says what it is.

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

Running this root prints two `Resource type has no orphan recovery` warnings, one
per resource. They are wrong, and they are choudoufu's rather than the example's:
both types have rows in the generated admission table, and the same run's `-json`
document lists `aws_iam_role` under `swept`. The warning fires for the schema-first
path as well as for the type-not-in-the-table path it was written for. Filed as
[#980](https://github.com/INTENTIUS/choudoufu/issues/980), found by building this
example.

The IAM role is in the root on purpose. IAM is one of the services whose tagging call
choudoufu does not print a paste-ready adopt command for (Route53 and S3 are the
others), so an unmarked IAM role appears in the adoption ledger as a refusal naming
both marker values rather than as something `live-adopt` claims. That is the honest
half of adoption, and a CI example should carry it.

## Running it locally, against the emulator

The three read-only Ops run end to end against floci with no AWS account. Point the
SDK at the emulator, put a `choudoufu` on PATH, and pick the forge whose finding modes
do not need a forge:

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
re-running then applies.

## The currency guard

Two of them, because they run in different places.

**`npm test`** is the real one. `tests/currency.test.ts` re-runs `generate.ts` for both
forges into a scratch directory and diffs byte for byte, in both directions, so a file
that changed and a file that should no longer exist fail equally loudly.
`tests/pipelines.test.ts` reads the checked-in workflows back field by field, asserting
properties a reviewer would want to hold rather than the bytes that happen to be there.
Its blind spot is that it needs node and `npm install`, and choudoufu's Go CI has
neither.

**`live/ci_pipelines_test.go`** is the backstop that does run there, using nothing but
git and the files themselves: every workflow is tracked, the set of workflows is
exactly the set of `src/*.op.ts` per forge, each file names its own forge and its own
Op source and sets a matching `CHANT_FORGE`, the choudoufu install is pinned to a
version and verified against the release's published SHA256, and no generator input
was committed after the workflows it generates. Its blind spot is stated in the file:
with no node it proves correspondence and ordering, not equality, and a hand-edit
committed in the same commit as the source change it pretends to reflect is invisible
to the ordering check by construction. Where node is available it goes on to
regenerate and diff, which is the same proof `npm test` gives.

**`gitlab/.gitlab-ci.yml` is guarded differently**, because "regenerate and diff" is
the wrong question for a file no generator writes. Both guards hold what is left: that
regenerating writes no gitlab tree at all, that the file is tracked and carries no
generated banner (and that nothing in `package.json` or `generate.ts` names gitlab, so
the day the generator does emit it, the guard fails and names itself as the thing that
has to move), that the one job is the one Op that generator can express, gated on both
halves of its schedule rule, and that its install is pinned by version and checksum the
same way the generated ones are.

All of them were proven red before they were trusted green: a hand-edited workflow, a
deleted one, a `CHANT_FORGE` pointing at the wrong forge, a checksum check replaced by
`cat`, a sixth Op with no workflow, a generator input committed after the workflows,
and on the GitLab side an untracked file, a deleted one, a `DO NOT EDIT` banner, a
second job running `live-apply`, a dropped selector, and a `generate.ts` taught to
write a gitlab tree.

## Pinning

The install line every job runs names a choudoufu release by version and verifies it
against the SHA256 that release published. A generated workflow is committed once and
re-run unattended, two of these over a role that can write to the account, so
"whatever was released last" is not a version, and a tag can be moved or an asset
replaced without the run noticing. `gh`, where a job needs it, is pinned the same way.
chant's terraform lexicon refuses a choudoufu older than v0.14.0, the release that made
the `live-plan -json` document reachable on a configuration that names its own estate.

The `uses:` steps are pinned to release tags. chant's generator refuses an unpinned
action reference and refuses a ref that names the action repository's own default
branch, at build time, by name.

## What is not here

- **The governance policies** that require these job names. Issue #807, sub-issue (c).
- **The other four Ops on GitLab.** Not a gap in this example: chant's gitlab Op
  generator refuses them by name, and #807's sub-issue (e) is the chant work that
  would change that. See the GitLab section above.
- **The doc page** on the site. Sub-issue (d).
- **A `.terraform.lock.hcl`.** A real repository commits one; this example leaves the
  provider pinned by `required_providers` only, so a clone of it does not carry a lock
  file for an architecture you may not be on.
- **Property-level drift.** chant's terraform lexicon implements entity-level
  observation for a live root and not `observeResourcesDeep()`, so there is no
  property-tree diff and no claimed-field set. `live-plan` is the plan, which is a
  different and older answer to the same question.
