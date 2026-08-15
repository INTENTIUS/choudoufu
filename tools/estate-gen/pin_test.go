// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"testing"

	"golang.org/x/mod/semver"

	"github.com/intentius/choudoufu/internal/live/pins"
)

// TestFixturePinNeverRunsAheadOfTheMeasurementPin is issue #137's guard.
//
// This generator carries its own provider pin, separate from
// internal/live/pins.AWSProviderVersion, and that separation is deliberate:
// the cohort estates are applied artifacts whose verdicts
// live/cohort-acceptance.json records, so bumping this one is a mass fixture
// regeneration plus an acceptance-tier re-run. pins.go's own doc comment says
// so.
//
// What the separation does not license is running AHEAD. Behind is the
// ordinary state: fixtures generated against an older provider, an acceptance
// artifact recording verdicts from it, and nothing claimed about the newer
// one. Ahead means the cohort fixtures are generated against schemas the
// survey, the admission evidence and the corpus have never seen, so
// live/cohort-acceptance.json would be recording round-trip verdicts for a
// provider no other instrument in this repository measures - and nothing
// about that fails, or even looks unusual, while it is happening.
func TestFixturePinNeverRunsAheadOfTheMeasurementPin(t *testing.T) {
	fixture := "v" + providerVersion
	measurement := "v" + pins.AWSProviderVersion

	if !semver.IsValid(fixture) {
		t.Fatalf("estate-gen's providerVersion %q is not a semantic version", providerVersion)
	}
	if !semver.IsValid(measurement) {
		t.Fatalf("pins.AWSProviderVersion %q is not a semantic version", pins.AWSProviderVersion)
	}

	if semver.Compare(fixture, measurement) > 0 {
		t.Errorf("estate-gen's fixture pin (%s) is ahead of pins.AWSProviderVersion (%s).\n"+
			"Behind is legal and expected - fixtures lag the survey until a deliberate regeneration.\n"+
			"Ahead is not: the cohorts would be generated against schemas no other instrument in this "+
			"repository has surveyed, and live/cohort-acceptance.json would record verdicts for a provider "+
			"nothing else measures. Bump pins.AWSProviderVersion first, or lower this one.",
			providerVersion, pins.AWSProviderVersion)
	}
}
