// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// proposeSummaryRE matches row-gen -propose's own summary line
// (tools/row-gen/propose.go's buildProposeReport), e.g.:
//
//	row-gen -propose: 6 rule class(es) considered (min sample 5), 0 qualify at 100% historical adoption, 0 new logical type(s) proposed for auto-admission
var proposeSummaryRE = regexp.MustCompile(`^row-gen -propose: .*$`)

// ProposeResult is PROPOSE's output: where its captured report landed, and
// the one-line summary REPORT folds into the PR body.
type ProposeResult struct {
	// Path is tmp/admission-pipeline/row-gen-propose.txt - row-gen -propose
	// only ever prints (the same #37 stance REGENERATE's own
	// row-gen-proposals.txt capture already honors: a wrong admission row
	// touches live infrastructure, so nothing in this chain writes
	// internal/live directly), so this is where its stdout was captured.
	Path    string
	Summary string
}

// Propose runs issue #65's PROPOSE stage: `go run ./tools/row-gen -propose`
// as a subprocess (row-gen is its own package main - see main.go's package
// doc for why every stage here shells out rather than importing), captured
// the same way REGENERATE captures the ordinary row-gen report - to a file
// under tmp/admission-pipeline/, since the full rule-class ledger and any
// pasted blocks can run long, plus a one-line stderr summary short enough
// for REPORT to quote directly in the PR body.
//
// Deliberately reads whatever live/mapping.json, live/registry.json and
// live/import-grammar.json already say on disk - REGENERATE, run
// immediately before this in main.go's run(), is what puts this week's
// freshly regenerated versions there; PROPOSE never re-derives anything
// itself.
func Propose(root string, log io.Writer) (*ProposeResult, error) {
	run, err := goRunTool(root, "row-gen", []string{"-propose"}, log, false)
	if err != nil {
		return nil, err
	}
	path, err := writeProposeReport(root, run.Stdout)
	if err != nil {
		return nil, fmt.Errorf("writing row-gen -propose's captured report: %w", err)
	}
	fmt.Fprintf(log, "admission-pipeline: row-gen -propose's report captured to %s\n", path)
	return &ProposeResult{Path: path, Summary: findProposeSummary(run.Stderr)}, nil
}

func writeProposeReport(root, contents string) (string, error) {
	dir := reportsDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "row-gen-propose.txt")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil { //nolint:gosec // a local scratch report, not a secret
		return "", err
	}
	return path, nil
}

func findProposeSummary(stderr string) string {
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if proposeSummaryRE.MatchString(line) {
			return line
		}
	}
	return ""
}
