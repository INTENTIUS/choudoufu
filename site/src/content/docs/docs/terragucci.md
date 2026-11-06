---
title: CI with terragucci
description: Run choudoufu from CI with terragucci.
prompt: >-
  Read https://intentius.io/choudoufu/docs/terragucci/ and
  https://intentius.io/terragucci/guides/use-a-binary/#choudoufu, then
  propose a `terragucci.yml` with `binary: choudoufu` for this repository as
  a diff.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

[terragucci](https://intentius.io/terragucci/) runs choudoufu from CI. It
generates the plan and apply pipeline for GitHub, GitLab or Forgejo, and runs
it as jobs in your own CI, with no server to host.

```bash
npm i -D @intentius/terragucci
npx terragucci init --binary choudoufu
```

`init` picks choudoufu only when asked: pass `--binary choudoufu`, or set
`binary: choudoufu` in `terragucci.yml`.

| You get | How |
|---|---|
| A check on every push | `tf-check` runs `live-check`, with no cloud credentials. A refusal fails the stage and names its rule. |
| Plans on pull requests | Each pull request plans the cone of its change in every live root: the blocks it touches and every block that reads them. One grouped note covers every plan. |
| Approved applies | Canary roots apply first, then the rest in waves. An approval covers exactly the plans it was shown, and a plan that changed after it is refused. |
| Estates that read each other | A root that reads another estate's outputs applies after it. In a pull request that changes both, it plans on the other estate's planned outputs. |
| Drift checks | `tf-drift` plans each live root in full and reports what the plan would change back. |
| Narrow roles | Jobs reach your bucket and cloud over OIDC, with no stored keys. A role scoped to one estate by its ownership tag is refused on another estate's resources. |
| A trace of every run | One OpenTelemetry trace per stage run, with choudoufu's own spans inside it: slow provider calls and lock waits. |

[Use choudoufu with terragucci](https://intentius.io/terragucci/guides/use-a-binary/#choudoufu)
has the full list, and [Getting started](https://intentius.io/terragucci/getting-started/)
sets up a repository.
