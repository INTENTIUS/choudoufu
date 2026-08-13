// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindProposeSummary(t *testing.T) {
	stderr := "row-gen: reading live/registry.json\n" +
		"row-gen -propose: 6 rule class(es) considered (min sample 5), 0 qualify at 100% historical adoption, 0 new logical type(s) proposed for auto-admission\n" +
		"row-gen: done\n"
	got := findProposeSummary(stderr)
	want := "row-gen -propose: 6 rule class(es) considered (min sample 5), 0 qualify at 100% historical adoption, 0 new logical type(s) proposed for auto-admission"
	if got != want {
		t.Errorf("findProposeSummary = %q, want %q", got, want)
	}
}

func TestFindProposeSummary_NoMatch(t *testing.T) {
	// The plain row-gen summary line (no "-propose") must not be mistaken
	// for PROPOSE's own summary - the two run in the same pipeline and
	// their stderr could plausibly both carry a "row-gen" prefix line.
	stderr := "row-gen: 919 mapped types (312 server-assigned, 401 client-named, 88 needs-hand-separator, 118 evidence-only)\n"
	if got := findProposeSummary(stderr); got != "" {
		t.Errorf("findProposeSummary with no -propose line = %q, want \"\"", got)
	}
}

func TestWriteProposeReport(t *testing.T) {
	root := t.TempDir()
	path, err := writeProposeReport(root, "the report body\n")
	if err != nil {
		t.Fatalf("writeProposeReport: %v", err)
	}
	wantPath := filepath.Join(root, "tmp", "admission-pipeline", "row-gen-propose.txt")
	if path != wantPath {
		t.Errorf("writeProposeReport path = %q, want %q", path, wantPath)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading written report: %v", err)
	}
	if string(data) != "the report body\n" {
		t.Errorf("written report contents = %q, want %q", data, "the report body\n")
	}
}
