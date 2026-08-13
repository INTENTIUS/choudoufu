# example cohort

Cohort: `example`. Ratified by: #48 itself — this directory is not a
ratification batch, it is the proof that the `estates/<cohort>` mechanism
works, landed by the issue that created the mechanism.

Every other directory under `live/e2e/estates/` is expected to follow the
real shape: one estate per ratification batch (service-sized, matching the
row generator's batching for that issue's batch), with a README naming its
cohort and the issue that ratified it, same as this one.

## What this proves

`internal/live/flocitest.CohortDirs` walks `live/e2e/estates/` and returns
every subdirectory it finds, this one included. `internal/live/flocitest.FixtureDirs`
returns the demo estate (`live/e2e/estate/`) plus that list — the union a
`table == estate` pin has generalized to `table == union(estate,
estates/*)`. `TestAdmissionTableCoversEstate`
(`internal/live/lint/lint_test.go`) and `TestTableCoversFixtureTypes`
(`internal/live/identity/identity_test.go`) walk `FixtureDirs` rather than
a hardcoded list or count, so this directory landing at all — even with no
`.tf` files in it — is what proves the walk tolerates a cohort and folds it
into the pinned universe with no test-file edits.

## Why it is empty

This cohort exists to prove the mechanism, not to ratify a type. It
deliberately declares no resources: adding one here would mean adding a new
managed resource type to the admission table and the identity table for
real (`internal/live/lint/admission.go`, `internal/live/identity/table.go`),
which is registry-expansion work belonging to whichever issue ratifies the
next real batch, not to the mechanism that lets that batch land later
without editing a test file. A future cohort with real resources replaces
this file as the reference example; until then this directory is the
existence proof.

## Gating

Nothing here runs against a live or emulated cloud. The pin tests above are
plain Go tests and always run. `live/e2e/run.sh` and the
`internal/live/discovery` floci-gated live test still stand up only the
demo estate — this cohort does not change the default tier's runtime, and
wiring cohort estates into the gated live run is separate follow-on work.
