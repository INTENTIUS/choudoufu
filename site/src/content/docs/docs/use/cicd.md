---
title: Running an estate from CI
description: The six pipeline jobs and the role each needs.
prompt: >-
  Read https://intentius.io/choudoufu/docs/use/cicd/ and propose the
  `live-check` and `live-plan` jobs for this repository's CI as a diff.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

Three jobs read. Three write, on a push, after an approval.

| Job | Runs on | Does |
|---|---|---|
| `live-check` | Pull request | Lists what would be refused, offline |
| `live-plan` | Pull request | Reports drift and unmarked matches |
| `live-apply` | Push to `main` | Plans to a file, applies it once approved |
| `live-adopt` | Push to `staging` | Shows the adoptions, writes markers once approved |
| `live-discover` | Schedule | Lists marked resources nobody declares |
| `backend-prepare` | Push to `bootstrap` | Creates or updates the S3 record store bucket |

Give each job the narrowest role. `backend-prepare` gets its own, because a
role that can rewrite the bucket policy can lock every run out. An apply that
finds the world changed since approval exits 3; send it back for review.

With records in the cluster, a plan job needs `get` and `list` on the record
Secrets, and an apply job also `create`, `update` and `delete`.

[`examples/ci-pipelines`](https://github.com/INTENTIUS/choudoufu/blob/main/examples/ci-pipelines/README.md)
generates these jobs for GitHub, Forgejo and GitLab.
