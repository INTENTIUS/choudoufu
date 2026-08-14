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
// Two of the four reach hcl.Diagnostic through [stamper.unstampableAt]'s
// summary parameter rather than as a literal field, which is invisible to
// any scanner reading the diagnostic literals. internal/live/identity hit
// the same problem with schema_verify.go and settled on this convention: a
// Summary-prefixed package constant, which [TestRefusalsRegistered] finds
// wherever it is used.
const (
	SummaryMarkerConflict    = "Ownership marker conflict"
	SummaryMarkerUncheckable = "Ownership marker could not be checked"
	SummaryNotStamped        = "Ownership markers not stamped"
	SummaryUnmarkedApply     = "Unmarked apply of a marker-only resource"
)
