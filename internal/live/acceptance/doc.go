// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package acceptance is GitHub issue #108's tier: every cohort estate under
// live/e2e/estates is applied against the floci emulator, its state file is
// deleted, and the plan rebuilt from ownership markers alone is asserted
// empty - the definition of done a user can check for themselves, run per
// cohort as a measurement.
//
// The result is recorded per cohort in live/cohort-acceptance.json - pass,
// or the first phase that failed - and the committed artifact is a ratchet:
// a cohort recorded as passing must keep passing, and a run that widens the
// passing set is expected to commit the regenerated artifact. What this
// package deliberately does NOT report is admitted-type counts. #105
// measured why: admitting six types moved unadmitted-type sites from 961 to
// 845 and moved the number of configurations that actually work by zero.
// The only number here is cohorts that apply and replan empty.
//
// Nothing in live/e2e/run.sh is reused. That script is 2515 lines around
// one demo fixture; this tier is built on internal/live/flocitest's
// primitives (CohortDirs, CopyFixtureDir, StartFloci, the cross-process
// init lock) precisely so that adding a cohort means adding a directory,
// not editing a harness.
package acceptance
