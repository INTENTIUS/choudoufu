// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package acceptance is GitHub issue #108's tier: every cohort estate is
// applied against the floci emulator, its state file is deleted, and the plan
// rebuilt from ownership markers alone is asserted empty - the definition of
// done a user can check for themselves, run per cohort as a measurement.
//
// The cohorts are rendered by tools/estate-gen into the run's own temporary
// directory from the roster in internal/live/cohorts, and thrown away
// afterwards. Until issue #699 they were 32 committed directories under
// live/e2e/estates; that directory now holds the hand-written notes and
// nothing the loader reads.
//
// The result is recorded per cohort in live/cohort-acceptance.json - pass,
// or the first phase that failed - and the committed artifact is a ratchet:
// a cohort recorded as passing must keep passing, and a run that widens the
// passing set is expected to commit the regenerated artifact. A cohort's
// resource count may not fall below what is committed either, pass or
// fail: issue #539 found a fixture that lost its failing resources and
// converted a red cohort to green with no other signal, and
// ratchetViolations (acceptance_live_test.go) fails the run on that just as
// hard as on a regression - see its doc comment for why a shrink is a hard
// failure rather than a warning. What this
// package deliberately does NOT report is admitted-type counts. #105
// measured why: admitting six types moved unadmitted-type sites from 961 to
// 845 and moved the number of configurations that actually work by zero.
// The only number here is cohorts that apply and replan empty.
//
// Nothing in live/e2e/run.sh is reused. That script is 2515 lines around
// one demo fixture; this tier is built on internal/live/flocitest's
// primitives (GenerateCohorts, CopyFixtureDir, StartFloci, the cross-process
// init lock) precisely so that adding a cohort means adding a roster entry,
// not editing a harness.
package acceptance
