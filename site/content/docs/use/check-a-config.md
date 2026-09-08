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

`choudoufu live-check -json` prints the same verdict as one document, with an
instance roster and its rungs. Its top-level `schemas` field says which of the
two answers you got: `"provider"` when the provider's own schemas backed the
rungs, `"builtin"` when they did not. The two documents are otherwise the same
shape and the same exit code, so a script that does not read that field cannot
tell the accurate answer from the pessimistic one.

`choudoufu live-ls -json DIR` carries the same field for the same reason: its
declared-instance comparison needs DIR's schemas to tell an instance with no
marker to find from one that is genuinely absent. Its `gaps` key is always
present, and `gaps_skipped` names the reason when the comparison did not run,
so an empty list is never mistaken for "no gaps".

## What it does not check

It checks two of five stages. Lint and identity resolution need no provider,
which is what makes the command fast and credential-free. Marker stamping,
discovery and projection need a cloud and go unchecked. A clean result is
necessary, not sufficient. Run a plan against a non-production account before
trusting a migration.

See [Compatibility reference]({{< relref "/docs/use/compatibility" >}}) for
what each refusal means, and [Migrate an existing
estate]({{< relref "/docs/use/migrate" >}}) for the next step.
