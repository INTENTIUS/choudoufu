---
title: Running an estate from CI
description: The six pipeline jobs, which ones write, and the role each needs.
prompt: >-
  Read https://intentius.io/choudoufu/docs/use/cicd/ and propose, as a diff for
  my review, the `live-check` and `live-plan` jobs for this repository's CI.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

Six jobs. Three only read; the three that write run on a push and wait for an
approval first.

- `live-check`, on a pull request: refuses a configuration that would be
  refused. No credential.
- `live-plan`, on a pull request: plans and reports drift and unmarked matches.
- `live-apply`, on a push to `main`: plans to a file, waits for approval, applies
  that file.
- `live-adopt`, on a push to `staging`: shows what it would adopt, waits, then
  writes the markers.
- `live-discover`, on a schedule: lists marked resources nobody declares.
- `backend-prepare`, on a push to `bootstrap`: creates or updates the record
  store bucket after approval.

Give each job the narrowest role. Only `live-apply` and `live-adopt` write to
the estate. `backend-prepare` gets its own role, since a role that can rewrite
the bucket policy can lock every run out. An apply that finds the world changed
since approval exits 3; send it back for review.

[`examples/ci-pipelines`](https://github.com/INTENTIUS/choudoufu/blob/main/examples/ci-pipelines/README.md)
generates these jobs for GitHub, Forgejo and GitLab.
