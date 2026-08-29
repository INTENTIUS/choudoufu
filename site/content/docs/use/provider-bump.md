---
title: "How the pinned AWS provider gets bumped"
weight: 11
---

# How the pinned AWS provider gets bumped

The identity table, the tier lookup and every admission ledger this fork
ships are all derived from one pinned `hashicorp/aws` release
(`internal/live/pins.AWSProviderVersion`, `6.59.0` today). The provider
itself moves weekly. A bump is a report, not an event: nothing regenerates
these artifacts on its own, and nothing commits itself.

```
just provider-bump 6.60.0
```

## What it does

Four steps, each a tool this repository already ships on its own:

1. `survey-gen -all -provider-version 6.60.0` downloads that release and
   rewrites `live/survey.json` and `live/survey-full.json` from its schemas.
2. `readiness-gen` rebuilds `live/readiness.json`
   ([the four-tier partition]({{< relref "resource-tiers" >}})) from the new
   survey.
3. `row-gen -convergence` rebuilds `live/rowgen-convergence.json`, measuring
   the classifier against every already-ratified row at the new schemas.
4. `provider-bump-report` reads what changed and prints it.

Nothing here touches `internal/live/pins.AWSProviderVersion`,
`tools/row-gen/ratified.json`, or `internal/live/identity.DefaultTable`.
Regenerating leaves the working tree with whatever the new release actually
changed - `git diff` on the four files above shows it directly - and
`live/pins_drift_test.go` stays red on those files until a human bumps the
pin constant too. Committing the bump is a second, deliberate act.

## Reading the report

`provider-bump-report` compares the artifacts as committed at `HEAD` against
the same three files on disk after the three regeneration steps, in four
sections:

- **Types.** Resource types the new release added or removed from
  `live/survey-full.json`'s roster.
- **Tier/status movement.** Every type whose entry in `live/readiness.json`
  changed tier or status - the number a customer reading
  [the resource-tier lookup]({{< relref "resource-tiers" >}}) actually feels.
- **Schema-precedence (issue #387).** Whether the provider's own identity
  schema still agrees with each ratified row it can be checked against -
  `live/rowgen-convergence.json`'s `schema_reproduces` bucket. A type that
  stops reproducing is worth a second look before the next ratification
  batch trusts the schema over the ledger.
- **Golden identity table.** Runs
  `internal/live/check`'s `TestIdentityGolden`, the 1,700-plus-line pinned
  regression file HANDOFF.md's enforced-guards table names. A version-only
  bump never moves it on its own - `DefaultTable` is written by
  `row-gen -emit` against a human-ratified ledger, not read live from the
  survey - so seeing it move here means something changed identity
  resolution outside what this report expected, and is worth chasing down
  before anything else.

The last line is either `ZERO MOVEMENT` or `MOVEMENT DETECTED`. Ratified-row
convergence's own percentage is printed too, but - as the tool's own output
says - it is not a coverage metric: it is row-gen's fresh classifier measured
against past human judgment, not against what a user can do.

## The self-test

Running the command with the version already at the current pin -
`just provider-bump 6.59.0` while `internal/live/pins.AWSProviderVersion`
already says `6.59.0` - exercises every stage for real: a real download (or
a warm plugin cache), a real classification pass, a real
`TestIdentityGolden` run. It has to report `ZERO MOVEMENT`, because nothing
changed; that is what proves the override plumbing and the report itself are
correct, with no need to reach a hypothetical newer release to find out.

## Reviewing a real bump

1. Run `just provider-bump <version>` and read the report.
2. `git diff live/survey.json live/survey-full.json live/readiness.json
   live/rowgen-convergence.json` for the raw shape of what moved.
3. If any tier or status moved for a type already in
   `identity.DefaultTable`, or the golden section reports `MOVED`, treat it
   like any other regression: read why before deciding whether the new
   release is at fault or the classifier is.
4. If nothing in the ledger needs a human ruling, bump
   `internal/live/pins.AWSProviderVersion` by hand, re-run `just tables` so
   every derived artifact and rendered doc catches up, and commit the whole
   set together - the pin, the regenerated artifacts, and the rendered
   spans - so `just ci`'s staleness guards hold on the next run.
5. If the schema-precedence or convergence sections flag a mismatch worth a
   ratification decision, that is a normal `tools/row-gen` batch, not part of
   the bump itself - see `tools/row-gen/annotations.json` and
   `tools/row-gen/ratified.json`.
