// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"strings"
	"testing"
)

// GitHub issue #1120's first Azure lane run. Both estates' migrate verdicts
// ended `first Error: line: ""`: live-import skipped every azapi_resource
// and printed why beside each one, but a skip is never an Error: line, and
// the verdict read nothing else. gauntlet_live_import_skips names each
// skipped row with its reason; gauntlet_live_import_skipped_addrs is what
// the two gates compare against the instances they expect skipped (the
// untaggable ones).
//
// The input is the -approve report as internal/command/views/live_import.go
// renders it (a group headline, then "  %-42s %-24s %s" per row, then a
// blank line), with the summary line copied from the run's own log,
// live/gauntlet/logs/corpus-azapi-role-assignment.20261021T012212Z.fail.log,
// and the reason the first run gave each azapi_resource.

// approveReport renders rows in the view's group format.
func approveReport(groups map[string][][3]string, order []string, summary string) string {
	var b strings.Builder
	b.WriteString("\nApprove: stamping tofu-estate and tofu-address on every eligible resource, and seeding the record store for every record-backed one. This was a cloud write, one tags-only apply per stamped resource.\n\n")
	for _, g := range order {
		rows := groups[g]
		fmt.Fprintf(&b, "%s (%d) - headline:\n", g, len(rows))
		for _, r := range rows {
			fmt.Fprintf(&b, "  %-42s %-24s %s\n", r[0], r[1], r[2])
		}
		b.WriteString("\n")
	}
	b.WriteString(summary + "\n3 identities recorded.\n")
	return b.String()
}

const firstRunUnadmitted = `Not stamped: There is no identity knowledge for resource type "azapi_resource", so this run cannot read or verify it. See live/LIMITATIONS.md, "unadmitted-type", for the admitted set.`

func TestLiveImportSkipsNameEachSkip(t *testing.T) {
	for estate, site := range map[string]string{
		"reference-azure":              `skipped (want only $WANT_SKIPPED): \"$(gauntlet_live_import_skips <<< "$APPROVE_OUT")\"`,
		"corpus-azapi-role-assignment": `skipped (want only $WANT_SKIPPED): \"$(gauntlet_live_import_skips <<< "$APPROVE_OUT")\"`,
	} {
		verdictScriptFunc(t, estate, "", site)
		verdictScriptFunc(t, estate, "", `SKIPPED_ADDRS="$(gauntlet_live_import_skipped_addrs <<< "$APPROVE_OUT")"`)
	}

	firstRun := approveReport(map[string][][3]string{
		"STAMPED": {{"azurerm_user_assigned_identity.uai", "azurerm_user_assigned_identity", "Wrote tofu-estate and tofu-address."}},
		"SKIPPED": {
			{"azapi_resource.resourceGroup", "azapi_resource", firstRunUnadmitted},
			{"azapi_resource.roleAssignments", "azapi_resource", firstRunUnadmitted},
		},
	}, []string{"STAMPED", "SKIPPED"},
		"1 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 2 skipped.")

	t.Run("the first run's skips are named with their reasons", func(t *testing.T) {
		got := driveVerdict(t, "", "gauntlet_live_import_skips", firstRun)
		want := "azapi_resource.resourceGroup: " + firstRunUnadmitted + "; azapi_resource.roleAssignments: " + firstRunUnadmitted + "\n"
		if got != want {
			t.Errorf("got\n%q\nwant\n%q", got, want)
		}
		if first := driveVerdict(t, "", "gauntlet_first_error_line", firstRun); first != "" {
			t.Errorf("the report has an Error: line after all: %q", first)
		}
	})
	t.Run("the first run fails the corpus gate's skip comparison", func(t *testing.T) {
		got := strings.TrimSpace(driveVerdict(t, "", "gauntlet_live_import_skipped_addrs", firstRun))
		if got != "azapi_resource.resourceGroup azapi_resource.roleAssignments" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("a reason holding a semicolon is not split into addresses", func(t *testing.T) {
		rep := approveReport(map[string][][3]string{
			"SKIPPED": {
				{"azurerm_subnet.app", "azurerm_subnet", "Not stamped: azurerm_subnet has no tags argument; its identity is recorded: see the record store."},
				{"azapi_resource.worker", "azapi_resource", "Not stamped: Provider registry.opentofu.org/hashicorp/azapi could not be used; it is not installed."},
			},
		}, []string{"SKIPPED"}, "0 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 2 skipped.")
		if got := strings.TrimSpace(driveVerdict(t, "", "gauntlet_live_import_skipped_addrs", rep)); got != "azapi_resource.worker azurerm_subnet.app" {
			t.Errorf("got %q", got)
		}
		wantIn(t, driveVerdict(t, "", "gauntlet_live_import_skips", rep),
			"azurerm_subnet.app: Not stamped: azurerm_subnet has no tags argument; its identity is recorded: see the record store.",
			"azapi_resource.worker: Not stamped: Provider registry.opentofu.org/hashicorp/azapi could not be used; it is not installed.")
	})
	t.Run("the fixed run's only skip is the untaggable one", func(t *testing.T) {
		fixed := approveReport(map[string][][3]string{
			"STAMPED": {
				{"azapi_resource.resourceGroup", "azapi_resource", "Wrote tofu-estate and tofu-address."},
				{"azurerm_user_assigned_identity.uai", "azurerm_user_assigned_identity", "Wrote tofu-estate and tofu-address."},
			},
			"SKIPPED": {{"azapi_resource.roleAssignments", "azapi_resource", "Not stamped: Microsoft.Authorization/roleAssignments at API version 2022-04-01 takes no tags, so azapi_resource.roleAssignments carries no ownership marker."}},
		}, []string{"STAMPED", "SKIPPED"},
			"2 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 1 skipped.")
		if got := strings.TrimSpace(driveVerdict(t, "", "gauntlet_live_import_skipped_addrs", fixed)); got != "azapi_resource.roleAssignments" {
			t.Errorf("got %q, want the role assignment alone", got)
		}
	})
	t.Run("no SKIPPED group is empty, not an error", func(t *testing.T) {
		none := approveReport(map[string][][3]string{
			"STAMPED": {{"azurerm_resource_group.app", "azurerm_resource_group", "Wrote tofu-estate and tofu-address."}},
		}, []string{"STAMPED"},
			"1 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 0 skipped.")
		for _, fn := range []string{"gauntlet_live_import_skips", "gauntlet_live_import_skipped_addrs"} {
			if got := strings.TrimSpace(driveVerdict(t, "", fn, none)); got != "" {
				t.Errorf("%s: got %q, want nothing", fn, got)
			}
		}
	})
}
