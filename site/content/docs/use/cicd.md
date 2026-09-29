---
title: "Running an estate from CI"
weight: 12
---

# Running an estate from CI

A choudoufu pipeline is five jobs. Three only read. The two that write run on
a push, and both stop for an approval first.

| Job | Runs on | What it does |
|---|---|---|
| `live-check` | pull request | Refuses the pull request if the configuration would be refused. No cloud call and no credential |
| `live-plan` | pull request | Plans, and reports drift and live resources that match a block and carry no marker |
| `live-apply` | push to `main` | Plans to a file, waits for approval of the rendered plan, then applies that file |
| `live-adopt` | push to `staging` | Shows what it would adopt, waits for approval, then writes the two tags on each resource |
| `live-discover` | a schedule | Lists what carries this estate's marker that nobody declares |

Give each job the narrowest role that works. `live-check` needs none. The
plan and discover jobs need a role that can read, including the estate's
records. Only `live-apply` and `live-adopt` need to write.

An apply that finds the live system changed since its plan was approved
refuses with exit status 3, and the job should send it back for review and
not page anyone.

[`examples/ci-pipelines`](https://github.com/INTENTIUS/choudoufu/blob/main/examples/ci-pipelines/README.md)
is a working project that generates these five jobs for GitHub, Forgejo and
GitLab, with the generated workflows checked in.
[`PIPELINE.md`](https://github.com/INTENTIUS/choudoufu/blob/main/examples/ci-pipelines/PIPELINE.md)
beside it has what differs per forge, the variables and secrets each needs
before its first run, and what was tested where.
[`examples/pipeline-governance`](https://github.com/INTENTIUS/choudoufu/blob/main/examples/pipeline-governance/README.md)
has the branch rules that require the two pull-request jobs and put the apply
behind a reviewer.
