# `-filter`: narrowing the live report

`choudoufu plan -filter=<category>` (and `choudoufu live-plan -filter=<category>`)
prints only the named sections of the live report. GitHub issue #1197 carries
the decision; the 2026-09-26 maintainer ruling on it is the spec.

## Categories

The words are the ones the report and the `-json` document already use.

| Category | What it selects | `-json` key |
|---|---|---|
| `unowned` | Live resources at an identity this configuration declares that carry no marker for this estate | `unowned` |
| `adoptable` | Live resources the estate-wide sweep matched to a declared instance by content | `adoptable` |
| `foreign` | Live resources the sweep found that nothing in this estate claims | `foreign` |

Repeat the flag to show more than one: `-filter=unowned -filter=foreign`
shows both. Repeats union. There is no comma form.

## What it does not change

The filter narrows the report and nothing else. The planned changes, the
resource diff, the omissions, the removals ("Owned and undeclared"), the sweep
gaps and the meaning of `-detailed-exitcode` are the same with or without it.
A filter cannot make an apply partial, and `apply` does not take it.

## Empty and unknown

A category the filter keeps that matches nothing prints a line saying so:
`No unowned resources.`, `No adoptable resources.`, or the foreign section's
own `Foreign resources: none among the N types swept` /
`Foreign resources: nothing was swept`. An empty result never looks like
silence.

Any other word is a usage error that names the three accepted ones:

    $ choudoufu live-plan -no-color -filter=drifted
    Error: Failed to parse command-line flags

    invalid value "drifted" for flag -filter: -filter takes one of unowned,
    adoptable, foreign, not "drifted"

`drifted` and `owned-by` were proposed on the issue and deferred by the
ruling: neither is computed as a per-resource set today. `unclaimed` and
`untagged` would have been second names for `foreign` and `unowned`.

## Under `-json`

The document gains a `filter` key naming the kept categories, in
unowned, adoptable, foreign order. A category the filter left out is `null`;
a kept one with nothing in it is `[]`. `bound`, `omissions`, `swept` and
`diagnostics` are never narrowed; `swept` stays because it is what says what
an empty `adoptable` or `foreign` list means.

## Refusals

`-filter` is refused on a state-backed plan (no live block), which prints
none of these sections, and alongside `-adoption-only`, which prints a
different report.
