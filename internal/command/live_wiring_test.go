// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"os"
	"strings"
	"testing"
)

// TestResidueWarningWiredIntoEveryLiveEntryPoint is the source-level half
// of the wiring pin. TestLivePlan_residueAttributeWarningIsWired proves the
// warning reaches a user through live-plan; this test holds the other two
// entry points to the same call, because the wave-3 audit deleted the
// live_mode wiring and every behavioral test stayed green. A source
// assertion is crude, but it is the same lockstep discipline refusalscan
// applies to diagnostics: the call's presence is the claim, and removing
// it must be a decision made in front of a failing test, not a silent drop.
func TestResidueWarningWiredIntoEveryLiveEntryPoint(t *testing.T) {
	for _, file := range []string{"live_plan.go", "live_mode.go", "live_mv.go"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}
		if !strings.Contains(string(src), "lint.CheckResidueAttributes(") {
			t.Errorf("%s no longer calls lint.CheckResidueAttributes - the attribute-level residue warning (#126) is unwired for that entry point", file)
		}
	}
}

// TestCloudControlFallbackWiredIntoDiscovery pins the #47 fallback's command
// wiring the same way. The engine tolerated a nil client for two issues'
// worth of commits ("nil Request.CloudControl is 'the fallback does not
// apply here', not an error"), so deleting this wiring fails no behavioral
// test that does not also run a live emulator - exactly how the gap survived
// until #124's media cohort hit it at replan. statelessDiscoverOne is the
// one site every live entry point's discovery goes through.
func TestCloudControlFallbackWiredIntoDiscovery(t *testing.T) {
	src, err := os.ReadFile("live_plan.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"registry.Embedded()", "req.CloudControl = cloudcontrol.New("} {
		if !strings.Contains(string(src), want) {
			t.Errorf("live_plan.go no longer contains %q - the Cloud Control fallback (#47) is unwired and every type without a native list resource refuses again", want)
		}
	}
	// The tagging sweep (#51, wired by #128) rides the same gate; losing
	// either line silently reverts to the per-type sweep, which no
	// behavioral test without a live emulator can tell apart.
	//
	// This half is a presence pin and nothing more: it says the sweep is
	// wired, not that enabling it is the right call. Whether the
	// assignment should be conditional is a question about the emulator,
	// and asking it here is what let #229's gate outlive its reason - a
	// source-text pin fails when someone edits the line and at no other
	// time. That question now belongs to
	// TestTaggingSweepPremiseHoldsForThePinnedEmulator, which reads the
	// capability manifest instead, and which owns
	// taggingSweepAssignment's value.
	for _, want := range []string{"req.Tagging = cloudcontrol.NewTagging(", taggingSweepAssignment} {
		if !strings.Contains(string(src), want) {
			t.Errorf("live_plan.go no longer contains %q - the #51 tagging sweep is unwired (see #128)", want)
		}
	}
}
