---
title: Running an estate from CI
description: The six pipeline jobs and the role each needs.
prompt: >-
  Read https://intentius.io/choudoufu/docs/use/cicd/ and propose the
  `live-check` and `live-plan` jobs for this repository's CI as a diff.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

Three jobs read. Three write, on a push, after an approval.

- `live-check` lists what would be refused, offline, on a pull request.
- `live-plan` reports drift and unmarked matches on a pull request.
- `live-apply` plans to a file and applies it once approved, on a push to `main`.
- `live-adopt` shows the adoptions and writes markers once approved, on a push to `staging`.
- `live-discover` lists marked resources nobody declares, on a schedule.
- `backend-prepare` creates or updates the record store bucket, on a push to `bootstrap`.

Give each job the narrowest role. `backend-prepare` gets its own, because a
role that can rewrite the bucket policy can lock every run out. An apply that
finds the world changed since approval exits 3; send it back for review.

[`examples/ci-pipelines`](https://github.com/INTENTIUS/choudoufu/blob/main/examples/ci-pipelines/README.md)
generates these jobs for GitHub, Forgejo and GitLab.
