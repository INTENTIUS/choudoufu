// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"strings"
	"testing"
)

func TestBuildReportZeroMovement(t *testing.T) {
	side := gauntletArtifact{
		Oracle: oracleVersions{Terraform: "1.16.0", Tofu: "1.12.6"},
		Sets: map[string]setSummary{
			"core": {Estates: 26, Clear: 25},
			"all":  {Estates: 26, Clear: 25},
		},
		Estates: []estateRow{
			{Name: "corpus-a", Clear: true, Stages: map[string]string{"cold_deploy": "pass"}},
		},
	}

	report := buildReport(side, side)

	if !strings.Contains(report, "ZERO MOVEMENT") {
		t.Errorf("identical before/after artifacts should report zero movement; got:\n%s", report)
	}
	if strings.Contains(report, "MOVEMENT DETECTED") {
		t.Errorf("identical before/after artifacts should not report movement detected; got:\n%s", report)
	}
	if !strings.Contains(report, "terraform 1.16.0 -> 1.16.0, tofu 1.12.6 -> 1.12.6") {
		t.Errorf("report should name both oracle versions in its header; got:\n%s", report)
	}
}

func TestBuildReportPinChangedAloneIsNotMovement(t *testing.T) {
	// The pin text itself moving (a real bump) with nothing measured
	// differently is still zero movement - the same way provider-bump-report's
	// header naming two different provider versions does not by itself set
	// movement. This is what a self-test run against an unchanged tree
	// (the same estates re-measured, same results) should look like right
	// after a version bump that happened to change nothing observable.
	old := gauntletArtifact{
		Oracle:  oracleVersions{Terraform: "1.15.8", Tofu: "1.12.5"},
		Sets:    map[string]setSummary{"core": {Estates: 1, Clear: 1}},
		Estates: []estateRow{{Name: "corpus-a", Clear: true, Stages: map[string]string{"cold_deploy": "pass"}}},
	}
	new := gauntletArtifact{
		Oracle:  oracleVersions{Terraform: "1.16.0", Tofu: "1.12.6"},
		Sets:    map[string]setSummary{"core": {Estates: 1, Clear: 1}},
		Estates: []estateRow{{Name: "corpus-a", Clear: true, Stages: map[string]string{"cold_deploy": "pass"}}},
	}

	report := buildReport(old, new)

	if !strings.Contains(report, "ZERO MOVEMENT") {
		t.Errorf("a pin change with no measured movement should still report zero movement; got:\n%s", report)
	}
	if !strings.Contains(report, "terraform 1.15.8 -> 1.16.0, tofu 1.12.5 -> 1.12.6") {
		t.Errorf("report should still name both oracle versions in its header; got:\n%s", report)
	}
}

func TestBuildReportBoardMovement(t *testing.T) {
	old := gauntletArtifact{
		Sets: map[string]setSummary{"core": {Estates: 2, Clear: 2}},
	}
	new := gauntletArtifact{
		Sets: map[string]setSummary{"core": {Estates: 2, Clear: 1}},
	}

	report := buildReport(old, new)

	if !strings.Contains(report, "MOVEMENT DETECTED") {
		t.Errorf("a board count regression should report movement; got:\n%s", report)
	}
	if !strings.Contains(report, "core: 2/2 clear -> 1/2 clear  MOVED") {
		t.Errorf("report should carry the exact before/after count with a MOVED marker; got:\n%s", report)
	}
}

func TestBuildReportStageMovement(t *testing.T) {
	old := gauntletArtifact{
		Estates: []estateRow{
			{Name: "corpus-a", Clear: true, Stages: map[string]string{"cold_deploy": "pass", "migrate": "pass"}},
			{Name: "corpus-b", Clear: true, Stages: map[string]string{"cold_deploy": "pass"}},
		},
	}
	new := gauntletArtifact{
		Estates: []estateRow{
			{Name: "corpus-a", Clear: false, Stages: map[string]string{"cold_deploy": "pass", "migrate": "fail"}},
			{Name: "corpus-b", Clear: true, Stages: map[string]string{"cold_deploy": "pass"}},
		},
	}

	report := buildReport(old, new)

	if !strings.Contains(report, "MOVEMENT DETECTED") {
		t.Errorf("a stage regression should report movement; got:\n%s", report)
	}
	if !strings.Contains(report, "corpus-a: migrate: pass -> fail") {
		t.Errorf("report should name the exact stage transition; got:\n%s", report)
	}
	if !strings.Contains(report, "corpus-a: clear: true -> false") {
		t.Errorf("report should note the clear-flag flip; got:\n%s", report)
	}
	if strings.Contains(report, "corpus-b:") {
		t.Errorf("corpus-b did not change; it should not appear in the per-estate section; got:\n%s", report)
	}
}

func TestBuildReportRosterOnlyChangeIsNotStageMovement(t *testing.T) {
	// An estate present on only one side (added or removed from the
	// manifest between old-ref and now) is a roster change, not something
	// this report characterises - the estate simply never enters the
	// per-estate diff loop.
	old := gauntletArtifact{
		Estates: []estateRow{{Name: "corpus-a", Stages: map[string]string{"cold_deploy": "pass"}}},
	}
	new := gauntletArtifact{
		Estates: []estateRow{
			{Name: "corpus-a", Stages: map[string]string{"cold_deploy": "pass"}},
			{Name: "corpus-new", Stages: map[string]string{"cold_deploy": "not_run"}},
		},
	}

	report := buildReport(old, new)

	if !strings.Contains(report, "no estate's stage verdicts or clear flag changed") {
		t.Errorf("a roster-only addition should not read as stage movement; got:\n%s", report)
	}
}

func TestBuildReportProvenance(t *testing.T) {
	newPin := oracleVersions{Terraform: "1.16.0", Tofu: "1.12.6"}
	old := gauntletArtifact{Oracle: oracleVersions{Terraform: "1.15.8", Tofu: "1.12.5"}}
	new := gauntletArtifact{
		Oracle: newPin,
		Estates: []estateRow{
			{Name: "corpus-fresh", LastRun: &estateLastRun{Oracle: &newPin}},
			{Name: "corpus-lagging", LastRun: &estateLastRun{Oracle: &oracleVersions{Terraform: "1.15.8", Tofu: "1.12.5"}}},
			{Name: "corpus-never-run"},
		},
	}
	old.Estates = new.Estates // present on both sides so the per-estate loop does not skip them

	report := buildReport(old, new)

	if !strings.Contains(report, "1 estate(s) re-measured against the new pin") {
		t.Errorf("expected exactly one re-measured estate; got:\n%s", report)
	}
	if !strings.Contains(report, "1 estate(s) still carry a different (or no) recorded oracle") {
		t.Errorf("expected exactly one lagging estate; got:\n%s", report)
	}
	if !strings.Contains(report, "corpus-lagging") {
		t.Errorf("report should name the lagging estate; got:\n%s", report)
	}
	if !strings.Contains(report, "1 estate(s) carry no last_run.oracle at all") {
		t.Errorf("expected exactly one estate with no recorded oracle at all; got:\n%s", report)
	}
}

func TestDisplayEmptyVersion(t *testing.T) {
	if got := display(""); got != "(none)" {
		t.Errorf("display(\"\") = %q, want \"(none)\"", got)
	}
	if got := display("1.16.0"); got != "1.16.0" {
		t.Errorf("display(%q) = %q, want unchanged", "1.16.0", got)
	}
}
