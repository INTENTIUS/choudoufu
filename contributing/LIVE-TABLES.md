# Where a ratification batch adds its rows

Four hand-written tables describe the stateless subset: which types are
admitted, how each one's identity is recovered, whether it carries tags, and
what the estate generator has to fix up by hand. Every ratification batch adds
rows to all four.

Until the per-cohort split, all four were single files and every batch
appended to the same tail position of the same literal. Re-merging main's
history showed 47% of merge commits conflicting, and the conflicts were
positional rather than semantic: two batches adding entirely disjoint types,
both landing at the end of the same map. Hand-resolving those conflicts is
what produced commit d3c77794d ("two truncated entries missing
IdentityAttrs/close-brace") and a staged resolution that silently dropped a
RECORD_ADMITTED skip.

So the tables are now split one file per cohort.

## The rule

A batch ratifying the `sagemaker` cohort adds four files and edits none:

| Table | File |
|---|---|
| `identity.DefaultTable` | `internal/live/identity/table_cohort_sagemaker.go` |
| `lint.admittedTypesV0` | `internal/live/lint/admission_cohort_sagemaker.go` |
| the three stamp pins | `internal/live/stamp/stamp_cohort_sagemaker_test.go` |
| `estate-gen`'s `typeOverrides` | `tools/estate-gen/overrides_cohort_sagemaker.go` |

Each file declares its own literal and registers it from an `init`. Copy the
nearest existing cohort file and change the names; the registration line is
the only boilerplate.

Two batches running concurrently now write four files each, with no path in
common, so git has nothing to merge. That is the whole point of the split: it
is not that conflicts are easier to resolve, it is that concurrent batches no
longer produce them.

## Why cohort, and not per-service

The obvious axis is one file per AWS service. It is the wrong one here.
Batches are not organized by service. Of the 26 batches in main's history only
three (lambda, iot, sagemaker) ratified a single service. The median batch
spans four, `ratify-databases` spans 14, and `ratify-remainder` spans 82. A
per-service split would make a typical batch open four to nine fragments
instead of one, and would still let two batches collide whenever they both
touched a shared service such as `ec2` or `iam`.

The cohort is already the repo's unit of ratification. It is the ratify branch
name, the `live/e2e/estates/<cohort>` directory, the `-cohort` flag on
`estate-gen`, and the banner over each section of the admission table. Splitting
on it means one batch writes one file per package.

## Adding to an existing cohort

Amending a cohort that already landed means editing that cohort's four files.
That still collides with another batch amending the same cohort, which is
correct: two batches changing the same cohort's rows is a real overlap that
deserves a human's attention, not a silent textual merge.

Prefer opening a new cohort over appending to `remainder`. `remainder` is the
catch-all from issue #65's pool and is already the largest fragment at 184
types, so it is the one file most likely to see two batches at once.

## The registration contract

Each package has a `registerCohort*` helper that folds a fragment into the
table. All of them refuse a duplicate key rather than overwriting it, the
same add-only rule `tools/mapping-gen/overlay.d` applies to its own JSON
fragments. Two cohorts claiming one type is a merge accident, and the
alternative is that whichever file sorts last silently wins, which is the
failure this split exists to prevent.

Go runs every package-level var initializer before any `init` function,
whatever order the files are in, so a fragment can never race the core
literal's own construction. `internal/live/identity/table_recordbacked.go` has
relied on that guarantee since issue #73 and is now registered through the
same helper.

The core literal left in each original file holds the pre-registry v0 types
(37 of them), which predate the batches and belong to no cohort.

## Checking a split did not change anything

The four tables are pure data, so a restructuring is verifiable rather than a
matter of review. Dump each table in a canonical form before and after and
diff. A reflect-based walk over `DefaultTable`, `admittedTypesV0`, the two
stamp slices sorted, and `testSchemas()` covers everything except
`typeOverrides`'s `Apply` closures, which are code; those are covered by
extracting each entry's normalized source with `go/ast` and diffing that.

The existing cross-table tests are the standing guard: `TestAdmissionTableCoversEstate`,
`TestTableCoversFixtureTypes` and `TestTaggableSetCoversAdmissionTable` pin the
four tables against each other and against the estate fixtures, so a type
added to one and forgotten in another fails without anyone remembering to
check.
