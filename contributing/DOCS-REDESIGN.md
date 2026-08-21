# Docs and site redesign: the brief

Design decisions for the internal-docs and site rebuild, taken 2026-08-18.
This file records what was decided and why. It carries no counts: every
number quoted below is an illustration of a shape, and the real ones come
from the artifacts named beside them.

## Who this is for

Someone already running Terraform or OpenTofu. They know what a state file
is, they have felt what it costs, and they do not need the concept
explained. Every page is written to that reader. Nothing on the site
teaches Terraform.

## What the pitch actually is

The old framing was handover: grant a role, inherit an estate. That is
table stakes. Anyone with IAM can already hand someone an account, and a
reader who has one will not find it interesting.

The claim that is actually new is governance *inside* a configuration and
*across* configurations. With a state file the permission unit is the file,
so "who may change the RDS instance but not the subnets" has exactly one
answer, which is to split the state. That forces estate topology to be
designed around blast radius rather than around what the system is, and
then charges rent forever in `remote_state` data sources and cross-stack
ordering.

With per-resource markers the permission unit is the resource, and IAM
already expresses per-resource, per-tag, per-condition access. A role can
hold half of one estate, or parts of five, with no file shared between
them.

Canonical claim sentence:

> IAM governance within an estate, and across them.

Sub-line: a role can hold half of one estate, or parts of five.

This sentence is bound by issue #113 to appear word-identical in
`README.md`'s opening, the site lead in `site/`, and the GitHub repo
description. Changing it changes all three in one commit or none.

## The three measurement axes

The site's honesty rests on showing three different questions as three
different charts, because they have different denominators and merging them
would lie.

| Axis | Question | Source | State |
| --- | --- | --- | --- |
| 1 | Does a config load and pass lint? | `live/corpus-refusals.json` → `ladder` | Committed, ready |
| 2 | Does it round-trip when run? | needs `tools/ladder-gen` | Binary today (`cohort-acceptance.json`) |
| 3 | Can IAM govern it per resource? | `live/iam-reference.json` | Committed, ready |

Axis 1 is scoped to the `published deployment` population only, the configs
written by people who never heard of this project. `corpus-manifest.json`
says that population "reads as a rate" and the other two "read as ranking".
Rolling all three together produces a flattering number that includes
in-repo fixtures written to pass. Do not do it.

Axis 2 has no gradation yet. `cohort-acceptance.json` is pass/fail and
every failure sits at phase `apply`. The gradation exists inside
`live/e2e/*/run.sh`, which has numbered phases, `--expect <phase>` and a
`--json` summary, but nothing aggregates the estates into an artifact.
`tools/ladder-gen` runs each estate, records the furthest passing phase and
writes `live/estate-ladder.json`. That artifact feeds both the second bar
and the per-estate pages.

Axis 3 is a ceiling AWS sets, not one this project chose. `iamref-gen`
already reads AWS's own Service Authorization Reference and records how
many actions per service honour `aws:ResourceTag`. Publishing it is a trust
move precisely because it is unflattering and not our fault.

### The time axis

Every artifact in `live/` is a point-in-time snapshot, so nothing plottable
exists. Starting now, each release commits `live/history/<version>.json`
holding that release's figures for all three axes. The site draws a single
bar today and grows a trend over the next few releases. No backfill from
git history: older artifacts have incomparable schemas and replaying them
would manufacture a trend out of unlike numbers.

## The site

### Information architecture

Mostly a landing page, with four destinations that fan out.

- **The model** — the three pieces, diagram-led
- **Governance** — scoping, the two-statement policy, IAM reach roster
- **Progress** — the ladders, the estates, the coverage ledger
- **Use it** — live-check, migrate, start, day-2, reference

Plus limits, about, and contributing outside the four.

### The landing page, fold by fold

1. **The hero is the state model**, shown as a comparison against the one
   file the reader already operates. Not architecture, not a diagram of our
   internals. Two columns:

   ```
   terraform.tfstate                    under choudoufu

   one file = one permission boundary   one resource = one boundary

   who may change the RDS instance?     who may change the RDS instance?
     whoever can write the state file     whoever your IAM says

   to scope access: split the state     to scope access: write a policy

   one role over three estates:         one role over three estates:
     three state files, shared            a policy
   ```

   Beneath it the three pieces get one line each, each linking to its own
   diagram page: which resource an address refers to is a tag on it; values
   AWS has nowhere to put go in a `record_store`; effects that leave nothing
   to read back get a receipt.

2. **How far it actually goes.** Axis 1 as a stacked bar, axis 2 beneath
   it, axis 3 beneath that. This fold sits *above* any install link. Showing
   the limit before showing a command is what makes the limit believable,
   and it delivers the "a limited set of things work" message structurally
   rather than as a disclaimer.

3. **Is yours one of them.** `choudoufu live-check`, no credentials. The one
   command that turns a global statistic into a personal answer. This is the
   conversion point.

4. **Estates that prove it.** Named estates, each linking to its own page.

5. **The map.** Links into the four sections.

### Ladder class names

The classes in `corpus-refusals.json` carry insider names. The site renames
them to say how far from working a config is, and the mapping lives in the
generator so the artifact stays untouched:

| Artifact | Site |
| --- | --- |
| `clean` | works today |
| `admissions-only` | waiting on a type |
| `data-read-eligible` | works if the read succeeds |
| `language-blocked` | needs language work |
| `unreadable` | could not parse |

`unadmitted_demand` in the same file is already a ranked build-next list
weighted by how many configs each missing type blocks. It becomes the
roadmap section on the Progress page.

### Estate pages

One page per estate, not a gallery. Each carries the HCL, the exact command
to run it, the rung it reached, and real recorded output. These are the
credibility anchors and they are first-class nav, not an appendix.

The flagship is a new one. Nothing in the repo currently proves per-resource
governance end to end: `live/e2e/estate/iam.tf` only declares IAM resources
as things under management, and every `aws:ResourceTag` condition in the
project lives in `live/MARKERS.md` prose. `live/e2e/reference-split-governance`
puts two roles on one estate, each scoped by IAM condition to its own
resources, and asserts that each can change its half and is denied on the
other. A companion `reference-cross-estate` proves the second half of the
claim: one role over parts of two estates, no state file shared, no
`remote_state` anywhere.

The exit code is the verdict, same as the rest of the harness.

### Theme

Fermentation. Keeps the food story the name comes from, drops the pink, and
reads more serious than salmon. Olive is already in the palette as an
accent, so the art retints without losing its identity.

```
base     #4a5334   deep olive
card     #f7f3e6   cream
ink      #241f16   near-black, warm
accent   #c98a3d   brass
signal   #fa8072   salmon, demoted to accent
charts   brass -> olive ramp
```

`docs/images/choudoufu.svg` bakes the old background in as a literal
`<rect fill="#fa8072">`, with a comment saying it must match the page
background. That rect becomes the new base, and the derived PNGs
(`choudoufu-inline-64`, `-128`, `-hero`, `-docs`, the favicons) re-render
from it.

### Tech

Astro. The Go generator in `site/` is retired. The JSON artifacts load at
build time, so the repo's standing rule holds unchanged: every count on the
site renders from a committed artifact and no number is typed by hand.

Cost, accepted knowingly: a Node toolchain in a Go repo and a heavier CI
build.

## The state model section

Three passes at three depths, so a reader never has to understand all three
pieces before understanding any of them.

1. The landing hero comparison, above.
2. One page per piece under **The model**, each answering "where does the
   thing tfstate did for me live now". Light on text, heavy on diagrams.
3. `live/MARKERS.md`, `live/RECEIPTS.md` and the storage page as spec,
   linked and otherwise unchanged.

Diagrams are hand-authored SVG under `docs/diagrams/`, the same approach
`choudoufu.svg` already uses. Diffable in git, no build step, no external
tool, and colours reference the same tokens as the site so a palette change
does not orphan them. The cost is editing XML by hand, accepted for the
control it buys on a three-way split that no auto-layout engine draws well.

## Internal docs

`docs/` is entirely inherited OpenTofu core documentation: architecture,
plugin protocol, diagnostics, the proto files. It stays untouched and stays
upstream's.

Everything this fork wrote moves under `contributing/`, which becomes the
fork's own roof with a real index at `contributing/README.md`. A newcomer
should never have to guess which project a file is about.

Known defects to fix in the same pass:

- `CHARTER.md` and `GOVERNANCE.md` are one-line pointers at `opentofu/org`.
  A fork that has diverged this far needs its own, or an explicit statement
  that it defers.
- `CODE_OF_CONDUCT.md` still carries the upstream template's
  `<!-- TODO: Decide who will handle Code of Conduct reports -->` comment
  and directs reports to `core@opentofu.org`, which is not this project.
- `HANDOFF.md` is 625 lines and is the actual contributor playbook, but
  nothing in the front door points at it and its name does not say what it
  is.
- `CONTRIBUTING.md` is 52 lines and does not function as a front door.

`live/*.md` stays where it is. Those files are the spec, they are cited by
tests, and the site reads several of them in place.

## Build order

1. `tools/ladder-gen` and `live/estate-ladder.json`. Everything visual on
   the Progress page waits on this.
2. `live/e2e/reference-split-governance`. The pitch is unproven until this
   passes.
3. Palette and art retint. Small, unblocks all design work.
4. The state-model diagrams.
5. The Astro site against the three axes.
6. `live/e2e/reference-cross-estate`.
7. Internal docs restructure and the defect fixes above.
8. `live/history/` snapshotting, wired into the release process.

## Traps

- **Do not merge the axes.** Different denominators. Three charts.
- **Do not quote 52 of 250.** Axis 1 is 145 published deployments. The other
  105 configs include 31 written here to pass.
- **Do not lead with the type ledger.** `README.md` already says type
  coverage is rarely what stops a configuration, and 1032-of-1699 is the
  most flattering and least predictive number in the repo. It lives on the
  coverage page.
- **Do not hand-write a rung.** Hand-maintained figures on this project have
  gone stale within the hour before; `HANDOFF.md` says so from experience.
