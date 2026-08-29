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
	side := bumpArtifacts{
		Survey: surveyArtifact{
			ProviderVersion: "6.59.0",
			Types:           []surveyRow{{Type: "aws_s3_bucket"}, {Type: "aws_vpc"}},
		},
		Readiness: readinessArtifact{
			Types: []readinessRow{
				{Type: "aws_s3_bucket", Tier: "marker-carried", Status: "in-contract"},
				{Type: "aws_vpc", Tier: "marker-carried", Status: "in-contract"},
			},
		},
		Convergence: convergenceArtifact{},
	}
	side.Convergence.Summary.Compared = 100
	side.Convergence.Summary.AdoptedUnchangedPct = 95.5
	side.Convergence.SchemaReproduces.HasIdentitySchema = 10
	side.Convergence.SchemaReproduces.Reproduced = []string{"aws_s3_bucket"}
	side.Convergence.SchemaReproduces.ReproducedCount = 1
	side.Convergence.SchemaReproduces.NotReproducedCount = 9

	report := buildReport(side, side, goldenResult{Ran: true, Passed: true})

	if !strings.Contains(report, "ZERO MOVEMENT") {
		t.Errorf("identical before/after artifacts should report zero movement; got:\n%s", report)
	}
	if strings.Contains(report, "MOVEMENT DETECTED") {
		t.Errorf("identical before/after artifacts should not report movement detected; got:\n%s", report)
	}
	if !strings.Contains(report, "6.59.0 -> 6.59.0") {
		t.Errorf("report should name both provider versions in its header; got:\n%s", report)
	}
}

func TestBuildReportTypesAddedAndRemoved(t *testing.T) {
	old := bumpArtifacts{
		Survey: surveyArtifact{
			ProviderVersion: "6.59.0",
			Types:           []surveyRow{{Type: "aws_s3_bucket"}, {Type: "aws_old_thing"}},
		},
		Readiness: readinessArtifact{
			Types: []readinessRow{
				{Type: "aws_s3_bucket", Tier: "marker-carried", Status: "in-contract"},
				{Type: "aws_old_thing", Tier: "marker-carried", Status: "in-contract"},
			},
		},
	}
	newer := bumpArtifacts{
		Survey: surveyArtifact{
			ProviderVersion: "6.60.0",
			Types:           []surveyRow{{Type: "aws_s3_bucket"}, {Type: "aws_new_thing"}},
		},
		Readiness: readinessArtifact{
			Types: []readinessRow{
				{Type: "aws_s3_bucket", Tier: "marker-carried", Status: "in-contract"},
				{Type: "aws_new_thing", Tier: "marker-carried", Status: "pending-ratification"},
			},
		},
	}

	report := buildReport(old, newer, goldenResult{})

	if !strings.Contains(report, "MOVEMENT DETECTED") {
		t.Errorf("a type added and a type removed should report movement; got:\n%s", report)
	}
	if !strings.Contains(report, "1 added, 1 removed") {
		t.Errorf("report should count exactly one add and one remove; got:\n%s", report)
	}
	if !strings.Contains(report, "+ aws_new_thing") {
		t.Errorf("report should name the added type; got:\n%s", report)
	}
	if !strings.Contains(report, "- aws_old_thing") {
		t.Errorf("report should name the removed type; got:\n%s", report)
	}
}

func TestBuildReportTierMovement(t *testing.T) {
	old := bumpArtifacts{
		Survey: surveyArtifact{ProviderVersion: "6.59.0", Types: []surveyRow{{Type: "aws_thing"}}},
		Readiness: readinessArtifact{Types: []readinessRow{
			{Type: "aws_thing", Tier: "record-carried", Status: "pending-mechanism"},
		}},
	}
	newer := bumpArtifacts{
		Survey: surveyArtifact{ProviderVersion: "6.60.0", Types: []surveyRow{{Type: "aws_thing"}}},
		Readiness: readinessArtifact{Types: []readinessRow{
			{Type: "aws_thing", Tier: "record-carried", Status: "in-contract"},
		}},
	}

	report := buildReport(old, newer, goldenResult{})

	if !strings.Contains(report, "MOVEMENT DETECTED") {
		t.Errorf("a status change with no roster change should still report movement; got:\n%s", report)
	}
	if !strings.Contains(report, "record-carried/pending-mechanism -> record-carried/in-contract") {
		t.Errorf("report should name the exact transition; got:\n%s", report)
	}
	if !strings.Contains(report, "no type added or removed") {
		t.Errorf("the roster itself did not change, so that section should say so; got:\n%s", report)
	}
}

func TestBuildReportSchemaPrecedenceMovement(t *testing.T) {
	old := bumpArtifacts{
		Survey:    surveyArtifact{ProviderVersion: "6.59.0"},
		Readiness: readinessArtifact{},
	}
	old.Convergence.SchemaReproduces.HasIdentitySchema = 2
	old.Convergence.SchemaReproduces.Reproduced = []string{"aws_a"}
	old.Convergence.SchemaReproduces.ReproducedCount = 1
	old.Convergence.SchemaReproduces.NotReproducedCount = 1

	newer := bumpArtifacts{
		Survey:    surveyArtifact{ProviderVersion: "6.60.0"},
		Readiness: readinessArtifact{},
	}
	newer.Convergence.SchemaReproduces.HasIdentitySchema = 2
	newer.Convergence.SchemaReproduces.Reproduced = []string{"aws_a", "aws_b"}
	newer.Convergence.SchemaReproduces.ReproducedCount = 2
	newer.Convergence.SchemaReproduces.NotReproducedCount = 0

	report := buildReport(old, newer, goldenResult{})

	if !strings.Contains(report, "MOVEMENT DETECTED") {
		t.Errorf("a schema-precedence flip should report movement; got:\n%s", report)
	}
	if !strings.Contains(report, "now reproduced: aws_b") {
		t.Errorf("report should name the type that newly reproduces; got:\n%s", report)
	}
	if !strings.Contains(report, "reproduced (schema agrees with the ratified row): 1 -> 2") {
		t.Errorf("report should carry the before/after reproduced count; got:\n%s", report)
	}
}

func TestBuildReportGoldenNotRun(t *testing.T) {
	side := bumpArtifacts{Survey: surveyArtifact{ProviderVersion: "6.59.0"}}
	report := buildReport(side, side, goldenResult{})
	if !strings.Contains(report, "not run (-skip-golden-test)") {
		t.Errorf("a zero-value goldenResult should read as not run; got:\n%s", report)
	}
}

func TestBuildReportGoldenMoved(t *testing.T) {
	side := bumpArtifacts{Survey: surveyArtifact{ProviderVersion: "6.59.0"}}
	report := buildReport(side, side, goldenResult{Ran: true, Passed: false, Output: "--- FAIL: TestIdentityGolden"})
	if !strings.Contains(report, "MOVEMENT DETECTED") {
		t.Errorf("a failed golden test should count as movement even with nothing else changed; got:\n%s", report)
	}
	if !strings.Contains(report, "MOVED") {
		t.Errorf("the golden section should say MOVED; got:\n%s", report)
	}
	if !strings.Contains(report, "FAIL: TestIdentityGolden") {
		t.Errorf("the golden section should carry the test's own output; got:\n%s", report)
	}
}
