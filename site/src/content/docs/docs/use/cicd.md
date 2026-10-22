---
title: Running an estate from CI
description: The six pipeline jobs and the role each needs.
prompt: >-
  Read https://intentius.io/choudoufu/docs/use/cicd/ and propose the
  `live-check` and `live-plan` jobs for this repository's CI as a diff.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

| Job | Runs on | Does |
|---|---|---|
| `live-check` | Pull request | Checks the configuration, offline |
| `live-plan` | Pull request | Reports drift and unmarked matches |
| `live-apply` | Push to `main` | Plans to a file, applies it once approved |
| `live-adopt` | Push to `staging` | Shows the adoptions, writes markers once approved |
| `live-discover` | Schedule | Sweeps the estate's marked resources and reports what can be adopted |
| `backend-prepare` | Push to `bootstrap` | Creates or updates the S3 record store bucket |

Give each job its own narrow role. An apply exits 3 when the live system moved
since approval; send it back for review.

[`examples/ci-pipelines`](https://github.com/INTENTIUS/choudoufu/blob/main/examples/ci-pipelines/README.md)
generates these jobs for GitHub, Forgejo and GitLab.
