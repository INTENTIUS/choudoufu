# Running the generated GitLab pipeline on a real GitLab

`examples/ci-pipelines/gitlab/scheduled-ops.gitlab-ci.yml` was generated,
asserted against and never run. This directory runs it: a throwaway GitLab CE
instance, a `gitlab-runner` with the docker executor, and the floci AWS
emulator on one network, with the example's own `terraform/` root as the
estate. Every claim below has a job trace behind it (#1026 item 3).

## The stack

| Piece | Version |
|---|---|
| GitLab CE | 17.11.0, revision `5e1517f7b46`, `enterprise: false` |
| gitlab-runner | 17.11.0 (`0f67ff19`), docker executor, arm64 |
| floci | the digest in `live/floci-image` |
| choudoufu | v0.16.0, downloaded by the job's own install line |
| chant | 0.60.0, the version `package.json` pins |

```bash
bash examples/ci-pipelines/e2e/gitlab/bootstrap.sh
# …drive it, see below…
docker compose -f examples/ci-pipelines/e2e/gitlab/docker-compose.yml down -v
```

`bootstrap.sh` brings the stack up, mints a root API token with
`gitlab-rails`, creates the project, registers the runner, sets the CI/CD
variables and pushes the seeded project. It prints `GITLAB_E2E_URL`,
`GITLAB_E2E_TOKEN` and `GITLAB_E2E_PROJECT_ID`. It was run twice: once by
hand, step by step, to produce everything below, and once as the script, from
`down -v` to a green gated `live-apply` and a green merge-request pipeline
with the note on it, to check that the script is the run.

Two things about the stack are not cosmetic:

`external_url` is `http://gitlab:8929`, not `http://localhost:8929`. Every
`CI_*` URL a job receives - the clone, `CI_API_V4_URL`, the notes endpoint -
is derived from it, and inside a job container `localhost` is the job
container. The host still reaches the same instance on `localhost:8929`.

The compose network is named, and `gitlab-runner` is registered with
`--docker-network-mode choudoufu-gitlab-e2e`, so the containers it spawns can
reach `gitlab:8929` and `floci:4566`. Without it they land on the default
bridge and can reach neither.

## Registering a runner on 17.x

GitLab 17 removed registration tokens. The runner is created first, as an
object, and the authentication token it hands back is what `register`
consumes:

```bash
curl -X POST -H "PRIVATE-TOKEN: $GITLAB_E2E_TOKEN" -H 'content-type: application/json' \
  -d '{"runner_type":"project_type","project_id":1,"description":"e2e","run_untagged":true}' \
  "$GITLAB_E2E_URL/api/v4/user/runners"
# {"id":1,"token":"glrt-…","token_expires_at":null}

gitlab-runner register --non-interactive --url http://gitlab:8929 --token glrt-… \
  --executor docker --docker-image node:22 --docker-network-mode choudoufu-gitlab-e2e
```

## Driving it

The pipeline has four triggers and each needs its own push. With
`P=$GITLAB_E2E_URL/api/v4/projects/1` and `H="PRIVATE-TOKEN: $GITLAB_E2E_TOKEN"`:

- **`live-check` and `live-plan`** - push a branch and open a merge request
  onto `main`: `curl -X POST -H "$H" -H 'content-type: application/json' -d
  '{"source_branch":"feature/x","target_branch":"main","title":"x"}'
  "$P/merge_requests"`. Pushing the branch alone creates no pipeline: no
  job's `rules:` matches a push to a branch that is not `main` or `staging`.
- **`live-apply`** - merge that request, or push to `main`.
- **`live-adopt`** - push a `staging` branch.
- **`live-discover`** - create a schedule with a `CHANT_SCHEDULED_OP`
  variable and play it: `curl -X POST -H "$H" -H 'content-type: application/json'
  -d '{"description":"nightly","ref":"main","cron":"0 6 * * *","active":true}'
  "$P/pipeline_schedules"`, then `POST $P/pipeline_schedules/1/variables` with
  `{"key":"CHANT_SCHEDULED_OP","value":"live-discover"}`, then
  `POST $P/pipeline_schedules/1/play`.

## What ran, 2026-09-09

Ten pipelines, seventeen jobs, on the file exactly as the generator wrote it
plus the `default: before_script:` in `overlay/.gitlab-ci.yml`.

| Op | Trigger | Result |
|---|---|---|
| `live-check` | merge request | green, unmodified. `Op "live-check" completed in 17.6s` |
| `live-plan` | merge request | green, unmodified, note posted. `[outcome] Comment=…/merge_requests/1#note_1` |
| `live-apply` | push to `main` | red unmodified; green with a git identity, `Op "live-apply" is gated on "approve-live-apply" after 40.6s` |
| `live-adopt` | push to `staging` | red, and no overlay fixes it: the Op has no Init phase |
| `live-discover` | schedule | green, unmodified. `Op "live-discover" completed in 27.5s` |

`live-plan` posted its note over the plain REST call, and a second run
updated note 1 in place rather than adding a second one, which is the
edit-in-place recipe the README claims.

`live-apply`, once past the gate, applied for real. With `chant/lifecycle`
fetched into the clone the approved run reads
`✓ gate:approve-live-apply() 3ms` / `[approved] e2e-operator` and
`✓ terraformApply(root=estate, planFile=chant.tfplan) 9.0s`, and the two
resources are then in floci - `/choudoufu-ci-pipelines-example/app` with
retention 120, and the IAM role.

## Four things the run found

**A GitLab CI checkout has no git identity, and the gate needs one.** chant's
gate writes its pending fact as a commit on `chant/lifecycle`. With no
`user.email`/`user.name` the write fails, and the whole error is:

```
[phase] Plan
  ✓ terraformPlan(root=estate, planFile=chant.tfplan)   19.6s
    [outcome] Changed=true
Op "live-apply" failed after 43.8s
```

No error line, no failing step, exit 1. Two `git config` lines ahead of the
run turn the same job into `Op "live-apply" is gated on "approve-live-apply"`
and exit 0. This is not GitLab-specific - nothing in any of the three
generated dialects sets an identity. Filed as chant #2301.

**`CI_JOB_TOKEN` does not work for the merge-request note on 17.11.** With
`GITLAB_TOKEN` removed, chant falls through to the job token and the Report
step fails:

```
GitLab API GET http://gitlab:8929/api/v4/projects/1/merge_requests/1/notes?per_page=100&page=1
answered 401: {"message":"401 Unauthorized"} (token from CI_JOB_TOKEN, sent as JOB-TOKEN).
```

The notes API is not on the job-token allowlist, so `live-plan` turns the
merge request's pipeline red rather than degrading to a log-only finding.
`GITLAB_TOKEN` with `api` scope is required, not optional.

**The protected-variable trap is real, and it looks identical to a missing
variable.** `GITLAB_TOKEN` set with `protected: true`, a merge-request
pipeline from the unprotected branch `feature/retention`: the same 401, the
same `token from CI_JOB_TOKEN`. The variable exists on the project and is
absent from the job. Flipping it to unprotected and pushing again gives
`Op "live-plan" completed in 25.5s` and the note. The same applies to the
three `CHOUDOUFU_*_ROLE_ARN` variables on merge-request jobs.

**`live-adopt` never runs `init`, so it fails on every fresh checkout.** Its
phases are Check, Ledger, Gate, Adopt - no Init - and the Ledger step needs
the provider schema:

```
[phase] Ledger
  ✗ choudoufuLivePlan(root=estate, adoptionOnly=true)   90.7s
│ Error: Provider unavailable for marker discovery
│ Finding the live resources of this estate needs provider
│ provider["registry.opentofu.org/hashicorp/aws"], which could not be used
```

A `choudoufu init` in `terraform/` before the run is enough:
`✓ choudoufuLivePlan(root=estate, adoptionOnly=true) 13.6s`, then
`Op "live-adopt" is gated on "approve-live-adopt"`. The fix belongs in
chant's `TerraformAdoptOp`, not in the pipeline, which is why the overlay
does not paper over it. Filed as chant #2302.

## The approve loop does not close on GitLab

`chant approve` writes the resolution to `chant/lifecycle`, and the pushed
branch carries it. Re-running the job does not clear the gate: a GitLab CI
checkout fetches the pipeline's own ref at depth 20 and nothing else, so the
job reads an empty ledger, records a fresh pending fact and gates again
(`expires : …T05:15:13Z` on the first run, `…T05:16:39Z` on the retry). The
same commit, in the same container, with
`git fetch origin chant/lifecycle:refs/heads/chant/lifecycle` first, walks
through the gate and applies.

Anyone wiring this up for real needs the ref in the job. `GIT_DEPTH: 0` is
not enough on its own - GitLab fetches refspecs, not all branches. Filed as
chant #2303, with the `chant approve` force-write below.

Related, and worth its own fix upstream: `chant approve` run in a clone that
has not fetched `chant/lifecycle` force-writes the branch from scratch rather
than appending, discarding the pending fact already recorded there. The
branch went from one commit holding the pending record to one commit holding
only the resolution.

## The environment gate, on CE

`environment: { name: production }` does create the environment - it appears
as environment 1, tier `production`, and each `live-apply` job records a
deployment against it. Nothing stops the job:

```
GET /api/v4/projects/1/protected_environments -> 404 {"error":"404 Not Found"}
GET /api/v4/deployments/3/approval             -> 404
```

Protected environments and deployment approvals are Premium features, so on
CE the key is an audit trail and not a gate. chant's own gate is the control
that actually held. Note also that a gated run records a **successful**
deployment to `production`, because `--gated-exit 0` makes the job green:
the deployment list reads `success` for a run that applied nothing.

## Two smaller notes

The stage is named `scheduled-ops` for all five jobs, merge-request jobs
included, so a merge request's pipeline shows `live-check` and `live-plan`
under a heading that says "scheduled-ops" (chant #2293). The name is
`generateGitlabOpPipeline`'s, not this project's.

The generated jobs export `AWS_WEB_IDENTITY_TOKEN_FILE` and `AWS_ROLE_ARN`
and were still able to plan against floci with `AWS_ACCESS_KEY_ID` and
`AWS_SECRET_ACCESS_KEY` set: the SDK's env-static credentials win over the
web-identity provider, so the OIDC exports are inert rather than fatal when
static credentials are present. Nothing here exercises a real OIDC exchange,
and floci is not an identity provider; that claim stays unverified.
