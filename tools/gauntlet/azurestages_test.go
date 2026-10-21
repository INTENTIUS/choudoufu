// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// GitHub issue #1120's stage audit: every stage from drift_reconverge on, in
// both Azure estates, read against floci-az at 30000b0 before the lane's
// next run, so that run fails only on filed emulator gaps.
//
// Two findings are about the removal stages. A removed block's object of a
// type that takes tags is found only by its marker through the Resource
// Graph sweep (internal/live/discovery/recordorphan_read.go skips such a
// type: the sweep covers it), and floci-az's KQL subset refuses the sweep's
// query (lex00/floci-az#11). So reference-azure's day2_remove (a public IP)
// and both estates' day2_count scale-downs (identities) plan no destroy on
// floci-az. Each of those stages used to fail() there, which exits the
// script, so the gap at day2_remove also cost every stage after it. They now
// record the failure naming the gap (gauntlet_azure_sweep_note), put back
// the shape the later stages expect by hand, and go on.

func TestAzureSweepNoteNamesTheGap(t *testing.T) {
	warned := "Warning: Azure sweep unavailable\n\nQuerying Azure Resource Graph for subscription 00000000-0000-0000-0000-000000000001 failed, so this is an incomplete sweep and not an empty one.\n\nNo changes. Your infrastructure matches the configuration.\n"
	got := driveVerdict(t, "", "gauntlet_azure_sweep_note", warned)
	wantIn(t, got, `"Azure sweep unavailable"`, "lex00/floci-az#11", "a type that takes tags")
	if !strings.HasPrefix(got, "; ") {
		t.Errorf("the note is a clause appended to a verdict and starts with \"; \": %q", got)
	}

	if got := driveVerdict(t, "", "gauntlet_azure_sweep_note", "Plan: 0 to add, 0 to change, 2 to destroy.\n"); got != "" {
		t.Errorf("a plan with no sweep warning failed for some other reason, and the note must not blame the emulator: %q", got)
	}
}

func TestAzureRemovalStagesGoOnPastASweepGap(t *testing.T) {
	for estate, sites := range map[string][]string{
		"reference-azure": {
			`gauntlet_stage day2_remove fail "the plan without the public IP's block does not destroy exactly azurerm_public_ip.app ($(gauntlet_plan_line <<< "$M_PLAN"); first Error: line: \"$(gauntlet_first_error_line <<< "$M_PLAN")\")$(gauntlet_azure_sweep_note <<< "$M_PLAN")"`,
			`on_a gauntlet_arm DELETE "$PIP_ID?api-version=$NET_API" >/dev/null || fail`,
			`gauntlet_stage day2_count fail "the scale-down 2 -> 1 does not destroy exactly azurerm_user_assigned_identity.shard[1] ($(gauntlet_plan_line <<< "$C_PLAN"); first Error: line: \"$(gauntlet_first_error_line <<< "$C_PLAN")\")$(gauntlet_azure_sweep_note <<< "$C_PLAN")"`,
			"  current_block \"$ADOPTED\" live 2\nelse\n",
		},
		"corpus-azapi-role-assignment": {
			`gauntlet_stage day2_remove fail "the plan without the role assignment's block does not destroy exactly azapi_resource.role_assignment ($(gauntlet_plan_line <<< "$M_PLAN"); first Error: line: \"$(gauntlet_first_error_line <<< "$M_PLAN")\")$(gauntlet_azure_sweep_note <<< "$M_PLAN")"`,
			`on_a gauntlet_arm DELETE "$RA_ID?api-version=$RA_API" >/dev/null || fail`,
			`gauntlet_stage day2_count fail "the scale-down 2 -> 1 does not destroy exactly azapi_resource.shard[1] ($(gauntlet_plan_line <<< "$C_PLAN"); first Error: line: \"$(gauntlet_first_error_line <<< "$C_PLAN")\")$(gauntlet_azure_sweep_note <<< "$C_PLAN")"`,
			"  REVIEWED=1 RA=none MOVED=1 SHARDS=2 write_root \"$ADOPTED\" live\nelse\n",
		},
	} {
		for _, site := range sites {
			verdictScriptFunc(t, estate, "", site)
		}
		script := readEstateScript(t, estate)
		for _, exit := range []string{
			`fail "the plan does not destroy`,
			`fail "the scale-down does not destroy shard[1]"`,
			`fail "the plan destroys more than`,
			`fail "the scale-down is not exactly one destroy`,
		} {
			if strings.Contains(script, exit) {
				t.Errorf("%s still exits at %q, so a removal floci-az's sweep gap hides stops every stage after it", estate, exit)
			}
		}
	}
}

// The corpus estate's day2_rename moves the role assignment's block with a
// moved block. The role assignment takes no tags, so its record is its only
// marker, and the apply writes that record under the new address while the
// one under the old address stays (internal/live/projection/located.go,
// locatedIdentityWithAliases: a moved block's origin key is never re-keyed).
// The gate asserted the old record gone, which the product never does, so
// day2_rename would have failed on its own expectation. And day2_remove then
// dropped moved.tf, leaving two records of one role assignment for the
// record leg to read as two removed blocks. moved.tf now stays in every
// stage after the rename.
func TestCorpusKeepsTheMovedBlockAfterTheRename(t *testing.T) {
	script := readEstateScript(t, "corpus-azapi-role-assignment")
	start := strings.Index(script, "gauntlet_begin_stage day2_rename")
	end := strings.Index(script, "gauntlet_begin_stage day2_teardown")
	if start < 0 || end < start {
		t.Fatal("corpus-azapi-role-assignment/run.sh no longer has day2_rename before day2_teardown; this test is reading the wrong span")
	}
	n := 0
	for _, ln := range strings.Split(script[start:end], "\n") {
		if !strings.Contains(ln, `write_root "$ADOPTED"`) && !strings.Contains(ln, `write_root "$ORACLE"`) {
			continue
		}
		n++
		if !strings.Contains(ln, "MOVED=1") {
			t.Errorf("a root written after the rename has no moved block, so the old address's record reads as a removed block: %s", strings.TrimSpace(ln))
		}
	}
	if n < 10 {
		t.Errorf("found %d write_root calls between day2_rename and day2_teardown, want at least 10; this test is reading the wrong span", n)
	}
	if strings.Contains(script[start:end], "azapi_resource.roleAssignments || true") {
		t.Error("day2_rename asserts no record names the old address, which a moved block never removes (located.go's locatedIdentityWithAliases)")
	}
}

func readEstateScript(t *testing.T, estate string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(testRoot(t), "live", "e2e", estate, "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
