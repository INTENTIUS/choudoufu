// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "testing"

// oracleReleasesThatBreakTheOracle names stock releases that cannot serve as
// the gauntlet's oracle, and says why in the value. Stock is the oracle
// (HANDOFF, "The engine"), so a release whose own plan graph is
// nondeterministic does not produce a verdict at all: it produces a coin
// flip that the artifact then records as a stage's verdict. Keeping the list
// here rather than in a comment means a pin that walks back onto one of
// these fails a test instead of quietly reappearing in a nightly.
//
// Add an entry when a release is measured to be nondeterministic or plainly
// wrong on a configuration the gauntlet runs, with the numbers in the
// message. Remove one only when the release is no longer reachable at all.
var oracleReleasesThatBreakTheOracle = map[string]string{
	"terraform 1.16.0": "hashicorp/terraform#39089 (spurious `Error: Cycle` on an acyclic configuration) " +
		"and #39076 (a planned destroy-then-create applied create-before-destroy, destroy dropped), " +
		"both introduced at 038c6f72 / PR #38840 and both fixed by PR #39091 in 1.16.1. Measured on " +
		"corpus-rds-complete-postgres's day2_replace stock oracle plan, 20 runs against one frozen " +
		"cold_deploy state: 1.16.0 gave 9 cycles and 11 clean plans; 1.16.1 gave 0 cycles and 20 clean " +
		"plans. See choudoufu #947 and #1005. Pin 1.16.1 or later.",
}

// TestOraclePinIsNotAKnownBrokenRelease reads the REAL live/oracle-versions.json
// - the file CI feeds to setup-terraform and setup-opentofu - and refuses a
// pin on the list above.
//
// Proven load-bearing: setting terraform_version back to "1.16.0" in
// live/oracle-versions.json makes this test fail with the 1.16.0 entry's
// message.
func TestOraclePinIsNotAKnownBrokenRelease(t *testing.T) {
	root := repoRootForTest(t)
	pin := oracleVersions(root)
	if pin.Terraform == "" && pin.Tofu == "" {
		t.Fatalf("%s did not parse: an unreadable pin reads as no pin at all, and CI would install whatever it liked", OracleVersionsPin)
	}
	for _, got := range []struct{ tool, version string }{
		{"terraform", pin.Terraform},
		{"tofu", pin.Tofu},
	} {
		if got.version == "" {
			t.Errorf("%s pins no %s version; CI's setup step would resolve its own latest, which is what issue #544 exists to stop", OracleVersionsPin, got.tool)
			continue
		}
		if why, bad := oracleReleasesThatBreakTheOracle[got.tool+" "+got.version]; bad {
			t.Errorf("%s pins %s %s, which cannot serve as the oracle: %s", OracleVersionsPin, got.tool, got.version, why)
		}
	}
}
