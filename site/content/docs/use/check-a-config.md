---
title: "How to check a configuration before migrating"
weight: 2
---

# How to check a configuration before migrating

Run `choudoufu live-check` against any OpenTofu configuration:

```
choudoufu live-check ./
```

Point it at any OpenTofu configuration. No `live` block, no cloud calls, no
requirement that the directory has heard of this fork. It prints a verdict,
then every refusal that fired. Each refusal comes with its site count, the
types responsible, and what to do about it.

Run `choudoufu init` first if you can. With provider schemas available it
judges types from the provider's own identity schema as well as the built-in
table, and admits more. Without them it says the answer is pessimistic.

## What it does not check

It checks two of five stages. Lint and identity resolution need no provider,
which is what makes the command fast and credential-free. Marker stamping,
discovery and projection need a cloud and go unchecked. A clean result is
necessary, not sufficient. Run a plan against a non-production account before
trusting a migration.

See [Compatibility reference]({{< relref "/docs/use/compatibility" >}}) for
what each refusal means, and [Migrate an existing
estate]({{< relref "/docs/use/migrate" >}}) for the next step.
