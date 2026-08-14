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
