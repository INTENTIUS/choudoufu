# The live tables, and where a hand ruling goes

Sixty-one source files point here. This is the page they mean.

The live-marker path is generated from four committed artifacts. Almost
nothing about it is hand-maintained, and the parts that are follow one shape:
an **add-only fragment** or a **named ledger**, never an edit to a generated
file. This page says which is which, so that a ruling you make today is still
there after the next regeneration.

## The rule

> Never hand-edit a file carrying `Code generated ... DO NOT EDIT`, or any
> artifact under `live/`.

Those change by running their generator. A ruling written into one survives
until the next run and no longer, which is worse than not recording it: the
loss is silent and the file still looks authoritative.

## The two generated tables have no fragments at all

`internal/live/identity/table_generated.go` (`DefaultTable`) and
`internal/live/lint/admission_generated.go` (`admittedTypesV0`) are written
in full by `go run ./tools/row-gen -emit`.

They used to be assembled from per-cohort fragments — `table_cohort_data.go`,
`admission_cohort_iam_ecr.go` and about fifty others — each registered by an
`init()`. Issue #96 deleted every one of them. `tools/row-gen/emit.go` states
why, and the reason is worth keeping in view:

> Nothing hand-written participates in building either one: no per-cohort
> fragment, no `init()`, no core literal a batch appends to, and no assembly
> statement in a hand-edited file that unions generated pieces together. That
> last one matters as much as the rest — a human-maintained line that says
> "and here is how the generated parts become the table" is the same paste
> cycle in a smaller font.

So there is no fragment mechanism for these two, deliberately. A correction
goes into the generator's rules, or into one of the ledgers below.

### `DefaultTable` is generated, but it is not derived

Written in full by `-emit` and still the only copy of its own contents. Every
non-`RecordBacked` row is copied verbatim out of the `DefaultTable` compiled
into the generator, which is the file `-emit` wrote on the previous run. The
fresh classifier contributes no row, and `annotations.json` records the
*rulings* that justify a row diverging from the classifier, not the rows.

Two things follow, and issue #263 was opened because both were being got
wrong:

- **Re-running the generator does not restore a lost row.** Revert whatever
  caused a row to disappear, re-run `-emit`, and it re-emits the smaller
  table, because the smaller table is now its input. The restore is
  `git checkout --` on the four generated files. `-emit` refuses to write a
  smaller table at all unless you pass `-allow-retraction`; see
  `tools/row-gen/retraction.go`.
- **"Run `-emit` twice and diff" is not evidence the artifact is correct.**
  It shows the tree sits at *a* fixed point. Emptying `DefaultTable`'s literal
  and running `-emit` twice yields a 14-row table — the `RecordBacked` rows
  and nothing else — byte-identical across both runs, exit 0, 878 AWS rows
  gone. Measured at 5502e8a3de.

`admission_generated.go` and `markerless_generated.go` both read
`DefaultTable`, so they inherit this. `logical_type_generated.go` does not —
it comes from `live/logical-schemas.json` alone.

## Where a hand ruling goes

### `tools/row-gen/rejected.json` — types a batch declined

A type considered by a ratification batch and not admitted. PROPOSE reads
this as a veto set so it never re-proposes something already ruled out by
name, which is the one check that catches what the rule-class measurement
structurally cannot: a rejected type never enters `DefaultTable`, so it never
enters the convergence comparison either.

```json
"aws_lambda_alias": {
  "recovered_from": ["internal/live/identity/table_cohort_lambda.go"],
  "reason": "the provider's Import docs show function_name/alias_name, an argument-composed ID"
}
```

`reason` for a fresh ruling; `recovered_from` traces one recovered from
deleted prose. Either field alone is a valid row.

**The invariant, enforced by `TestRejectedLedgerIsDisjointFromAdmitted`:**
this set and `DefaultTable` are disjoint. A type cannot be both admitted and
vetoed. Over-inclusion is safe among types nobody admitted and is a defect
among types somebody did — issue #131 found 58 contradictions that came from
a recovery pass harvesting every type name near the word "Rejected" rather
than the subject of each bullet.

### `tools/row-gen/annotations.json` — why a shipped row differs from the proposal

A ruling that a ratified row is right where the classifier disagrees.
`reason` plus `evidence`, where `evidence` is what was actually inspected
rather than a restatement of the reason.

The two ledgers ask the same question on either side of the admission
boundary: `rejected.json` is "why no row", `annotations.json` is "why this
row and not the proposed one".

### `tools/row-gen/identityattr.go` — the pattern to copy

Eight entries, each carrying the raw evidence its ruling rests on, alongside
a stated rule that covers the rest. Its ratchet checks the rule against the
provider's wire schema rather than against itself, after an audit defeated a
version that did not.

When a fact resists derivation, this is the shape: a rule, a small ledger for
what the rule cannot reach, evidence per entry, and a test consulting
something external.

## The add-only fragment mechanisms

Two generators do take fragments, for the same reason: parallel batches
should not contend for one file. Both refuse a duplicate key rather than
letting one side win silently.

### `tools/mapping-gen/overlay.d/*.json`

Merged into `overlay.json` in sorted filename order. Same schema.

> a key defined twice anywhere across the base and the fragments is refused,
> so a fragment can only ever add.

Add a fragment; do not edit a neighbour's.

### `tools/estate-gen/overrides_cohort_*.go`

Each file registers its cohort's overrides from its own `init()` via
`registerCohortOverrides`, which panics when two cohorts override the same
type. Package-level `var` initialisers all run before any `init()`, so the
core `typeOverrides` literal is complete before the first fragment merges
into it.

Note what these overrides are for: a provider-side requirement that never
reaches `configschema.Attribute.Required`, because the AWS provider enforces
it through plan-time validation rather than through the wire schema. Every
entry cites the `terraform validate` error it exists to silence. They are the
residual hand surface issue #56 asked to keep visible and rare, and #136 is
about shrinking it against sources the pipeline already downloads.

## Cohort directories have an ownership split

Under `live/e2e/estates/<cohort>/`:

- `GENERATED.md` and the `.tf` files are estate-gen's, rewritten in full on
  every run.
- `README.md` is hand-owned. `writeCohort` writes one only when none exists,
  and `ownedFiles` deliberately omits it so `removeStaleOwned` never deletes
  one.

That split exists because twelve of those READMEs carry the ratification
evidence `table_generated.go`'s rows were ratified against, and a
regeneration sweep destroyed all of it once before the split existed.

## Before adding anything by hand

Three questions, the same ones any change in this area answers:

1. Does this add a per-type map, list, or JSON table keyed by an AWS type,
   service, or action?
2. If yes, which upstream source was checked and found not to carry the fact?
3. If a source does carry it, why is this not reading that source?

The sources the pipeline already downloads or can: the CloudFormation
resource schemas (`~/Library/Caches/choudoufu/registry-gen/`), the provider
schema via `GetProviderSchema`, the provider documentation cache
(`~/Library/Caches/choudoufu/importdocs-gen/`), the AWS Service Authorization
Reference, botocore, and `live/mapping.json` for the join between them.

An irreducibly human ruling belongs in a named ledger with its evidence and a
ratchet. Everything else belongs to a generator.
