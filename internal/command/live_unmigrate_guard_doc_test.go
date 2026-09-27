// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// unmigrateGuardDocHeading is the heading live/LIMITATIONS.md carries for
// statefulMarkerGuard's section (issue #1702). It is a "## " heading, not a
// "### <name>" one: those are reserved for the limits wing
// (TestLimitationsDocCoversDirs, TestLimitsDirsMatchTable in
// internal/live/lint/limits_test.go require one live/e2e/limits/<name>/
// fixture per "### " heading in the whole document), and this guard fires on
// a full plan against a pre-stamped prior state, not on a bare configuration
// load the way a lint fixture does - forcing it into that machinery would be
// the wrong shape, not a smaller version of the right one.
const unmigrateGuardDocHeading = "## The un-migration guard"

// unmigrateGuardDocSection extracts the heading's own section: everything up
// to (not including) the next "## " heading, or end of file.
var unmigrateGuardDocSection = regexp.MustCompile(`(?s)` + regexp.QuoteMeta(unmigrateGuardDocHeading) + `\n(.*?)(\n## |\z)`)

// TestLimitationsDocCoversUnmigrateGuard is issue #1702's guard.
//
// statefulMarkerGuard (GitHub issue #613, widened to Kubernetes labels by
// #1649 via markerstrip.Scan) has no entry in live/LIMITATIONS.md at all: an
// operator hitting summaryUnmigrateRefused today has no doc page to read
// past the CLI's own diagnostic text. It is not in internal/live/check's
// AllRefusals catalog either, so tools/limits-gen generates nothing for it -
// this is a hand-written-section gap, not a generator gap, and nothing else
// in the tree pins the doc to this guard's actual wording and escape hatch.
//
// Proving it red: this failed before live/LIMITATIONS.md carried
// unmigrateGuardDocHeading at all, let alone a section under it naming the
// guard's real diagnostics and covering both substrates.
func TestLimitationsDocCoversUnmigrateGuard(t *testing.T) {
	raw, err := os.ReadFile("../../live/LIMITATIONS.md")
	if err != nil {
		t.Fatalf("read live/LIMITATIONS.md: %v", err)
	}
	doc := string(raw)

	m := unmigrateGuardDocSection.FindStringSubmatch(doc)
	if m == nil {
		t.Fatalf("live/LIMITATIONS.md has no %q section for statefulMarkerGuard (#613, #1649)", unmigrateGuardDocHeading)
	}
	section := m[1]

	for _, want := range []string{
		summaryUnmigrateRefused,
		summaryUnmigrateApproved,
		UnmigrateEnvVar,
		"#613",
		"#1649",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the %q section does not mention %q", unmigrateGuardDocHeading, want)
		}
	}

	// Both substrates: the section must not describe only the AWS tags half
	// of #1649's widened guard.
	for _, want := range []string{"tags", "label"} {
		if !strings.Contains(section, want) {
			t.Errorf("the %q section does not mention %q; #1649 widened the guard to Kubernetes labels and the doc must cover both surfaces", unmigrateGuardDocHeading, want)
		}
	}
}
