// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// These live in their own file rather than in refusals.go because the
// registry file is the one file [TestRefusalsRegistered] does not scan: it
// holds the summaries as data, and counting it would make every entry
// justify itself. Declaring the constants there would have left the scanner
// with nothing to find, which is exactly the "scanner has stopped working"
// state it fails on.

// The summaries themselves, as constants.
//
// GitHub issue #644 retired five of the eight. What is left is what a run
// can still produce now that the marker writer is
// [projection.NodeResolver.AdjustConfigValue] rather than an HCL rewrite;
// each is raised outside this package, which is why they are constants
// here rather than literals at the raising site. See refusals.go for the
// per-entry account of what went and why.
const (
	SummaryMarkerConflict = "Ownership marker conflict"
	SummaryNotStamped     = "Ownership markers not stamped"
	SummaryUnmarkedApply  = "Unmarked apply of a marker-only resource"
)
