# estate-references fixture

GitHub issue #790's fixture for `references[]`: `choudoufu live-check
-json`'s declared roster now states every cross-estate edge a
configuration's own data sources make visible, the pattern
[`live/OUTPUTS.md`](../../OUTPUTS.md) documents as the replacement for
`terraform_remote_state`. This directory is the smallest configuration that
exercises it - one data source whose filters name a producer estate's
marker tags, read by one resource in the same module - so the shape is
testable without depending on `live/e2e/estate`'s much larger surface for
one new field.

## Why a separate directory

`live/e2e/estate` already carries the roster's other half (a plain mix of
tag-governable, declaration-carried and record-only instances) but declares
no cross-estate reference anywhere in it, and adding one there would mix a
new concern into a fixture whose coverage table is already documented and
pinned (its own README, and `internal/live/check/testdata/identity-golden.txt`
via `live/identity_golden_pin_test.go`). A fixture this small keeps that
pin's diff to exactly the one new resource below.

## No `run.sh`, no provider block

Every other top-level `live/e2e/*` sibling (`estate`, `estate-block`,
`estate-module`) stands up against the floci emulator for a real
`choudoufu apply`. This one does not need to: `references[]` is read from
configuration alone (`internal/live/check/references.go`), and
`choudoufu live-check` itself makes no cloud call by design. `live/e2e/limits/*`
already sets the precedent for a static-analysis-only fixture living
directly under `live/e2e/` with no provider wiring.

## What each resource is for

- `data.aws_vpc.network` - the consumer side of `live/OUTPUTS.md`'s worked
  example, filtered on `tag:tofu-estate` (`estate-references-network`, a
  producer this fixture never declares) and `tag:tofu-address`
  (`aws_vpc.main`). `crossEstateFilters` reads both literals with no
  provider schema needed, the same way a live-plan would send them to the
  provider verbatim.
- `aws_subnet.app` - reads the data source's `id` in `vpc_id`, an argument
  that is not part of `aws_subnet`'s own identity, so it never surfaces in
  the ordinary identity-resolution diagnostics `internal/live/dataread`
  classifies. `references[]`'s own `read_by` list is what makes this
  reference visible at all; nothing else in `choudoufu live-check`'s output
  would have named it.
