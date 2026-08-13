// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// pipelineArtifactPaths are every committed artifact REGENERATE can touch,
// relative to the repository root - the set REPORT diffs against HEAD and
// -pr commits.
var pipelineArtifactPaths = []string{
	"live/survey.json",
	"live/survey-full.json",
	"live/registry.json",
	"live/mapping.json",
	"live/import-grammar.json",
	"live/SURVEY.md",
}

// StageRun captures one `go run ./tools/<name>` invocation for REPORT to
// read back (its stderr summary lines, in particular).
type StageRun struct {
	Name     string
	Args     []string
	Stdout   string
	Stderr   string
	Duration time.Duration
}

// RegenerateResult is REGENERATE's output.
type RegenerateResult struct {
	Runs []StageRun
	// ProposalPath is where row-gen's captured report was written -
	// tmp/admission-pipeline/row-gen-proposals.txt - since row-gen only
	// prints (issue #44's stance: a human pastes and ratifies every row, so
	// row-gen never writes internal/live directly).
	ProposalPath string
}

// runFor returns the StageRun for the given tool name, or nil.
func (r *RegenerateResult) runFor(name string) *StageRun {
	if r == nil {
		return nil
	}
	for i := range r.Runs {
		if r.Runs[i].Name == name {
			return &r.Runs[i]
		}
	}
	return nil
}

// Regenerate runs every *-gen tool in dependency order: survey-gen -all,
// registry-gen, mapping-gen, importdocs-gen, tagverbs-gen, row-gen (captured
// to a file, not streamed - its report runs to thousands of lines), then
// survey-gen -render to refresh live/SURVEY.md's derived spans from the
// artifacts just written.
//
// Every tool is its own package main (see main.go's package doc), so each
// step shells out to `go run ./tools/<name>` rather than calling a library
// function - "simpler is fine" per issue #55's own orchestrator note.
func Regenerate(root string, accept bool, log io.Writer) (*RegenerateResult, error) {
	res := &RegenerateResult{}

	acceptArgs := func() []string {
		if accept {
			return []string{"-accept"}
		}
		return nil
	}

	steps := []struct {
		pkg  string
		args []string
	}{
		{"survey-gen", append([]string{"-all"}, acceptArgs()...)},
		{"registry-gen", nil},
		{"mapping-gen", nil},
		{"importdocs-gen", acceptArgs()},
		{"tagverbs-gen", acceptArgs()},
	}
	for _, s := range steps {
		run, err := goRunTool(root, s.pkg, s.args, log, true)
		res.Runs = append(res.Runs, run)
		if err != nil {
			return res, err
		}
	}

	rowRun, err := goRunTool(root, "row-gen", nil, log, false)
	res.Runs = append(res.Runs, rowRun)
	if err != nil {
		return res, err
	}
	proposalPath, err := writeProposalReport(root, rowRun.Stdout)
	if err != nil {
		return res, fmt.Errorf("writing row-gen's captured report: %w", err)
	}
	res.ProposalPath = proposalPath
	fmt.Fprintf(log, "admission-pipeline: row-gen's report captured to %s\n", proposalPath)

	renderRun, err := goRunTool(root, "survey-gen", []string{"-render"}, log, true)
	res.Runs = append(res.Runs, renderRun)
	if err != nil {
		return res, err
	}

	return res, nil
}

// reportsDir is tmp/admission-pipeline under root - gitignored (the repo's
// blanket "tmp" pattern), so REGENERATE's captured reports and REPORT's
// markdown summaries never need cleanup or a commit of their own.
func reportsDir(root string) string {
	return filepath.Join(root, "tmp", "admission-pipeline")
}

func writeProposalReport(root, contents string) (string, error) {
	dir := reportsDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "row-gen-proposals.txt")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil { //nolint:gosec // a local scratch report, not a secret
		return "", err
	}
	return path, nil
}

// goRunTool runs `go run ./tools/<pkg> <args...>` from root. When
// streamStdout is true, stdout is mirrored to log as it runs (short,
// human-scale output); when false, only stderr is mirrored live and stdout
// is captured silently (row-gen's report, which can run thousands of lines
// and belongs in a file, not the pipeline's own log).
func goRunTool(root, pkg string, args []string, log io.Writer, streamStdout bool) (StageRun, error) {
	start := time.Now()
	fullArgs := append([]string{"run", "./tools/" + pkg}, args...)
	cmd := exec.Command("go", fullArgs...)
	cmd.Dir = root
	cmd.Env = os.Environ()

	var stdout, stderr bytes.Buffer
	if streamStdout {
		cmd.Stdout = io.MultiWriter(&stdout, log)
	} else {
		cmd.Stdout = &stdout
	}
	cmd.Stderr = io.MultiWriter(&stderr, log)

	fmt.Fprintf(log, "admission-pipeline: running go %s\n", strings.Join(fullArgs, " "))
	err := cmd.Run()
	run := StageRun{Name: pkg, Args: args, Stdout: stdout.String(), Stderr: stderr.String(), Duration: time.Since(start)}
	if err != nil {
		return run, fmt.Errorf("go %s: %w", strings.Join(fullArgs, " "), err)
	}
	return run, nil
}
