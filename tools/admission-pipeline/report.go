// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// mappingUnexplainedNote mirrors tools/mapping-gen/mapping.go's own
// unexplainedNoteText: the generic note a TF type carries when it falls all
// the way through to via:none with no curated overlay judgment behind it -
// the "no source could classify this" subset of live/mapping.json's
// none-count REPORT calls out separately (issue #55's "unclassifiable ...
// count"). Duplicated here for the same reason detect.go's registry digest
// logic is duplicated: mapping-gen is package main, not importable.
const mappingUnexplainedNote = "no CFN counterpart found by name or curated overlay"

// rowGenSummaryRE matches row-gen's own summary line (tools/row-gen/main.go
// run()'s final Fprintf), e.g.:
//
//	row-gen: 919 mapped types (312 server-assigned, 401 client-named, 88 needs-hand-separator, 118 evidence-only)
var rowGenSummaryRE = regexp.MustCompile(`^row-gen: \d+ mapped types \(.*\)$`)

// ArtifactDelta is one committed artifact's before/after: line counts from
// `git diff --numstat` plus its "counts" header object read at HEAD and in
// the working tree, so REPORT can show both the byte-level diff size and
// the headline totals moving.
type ArtifactDelta struct {
	Path                     string
	LinesAdded, LinesRemoved int
	// Before is nil when the path didn't exist at HEAD, or its "counts"
	// object couldn't be read. Same for After but against the working
	// tree.
	Before, After map[string]any
}

// MappingSummary is live/mapping.json's header-level totals REPORT surfaces
// directly: former2's own conflict report and the none/unclassifiable
// split.
type MappingSummary struct {
	Former2Contradictions int
	None                  int // every via:none row, explained or not
	Unclassifiable        int // via:none rows with no curated reason at all
}

// ReportInput is every fact REPORT needs, gathered by WriteReport's I/O and
// consumed by renderReport's pure formatting - split so report generation
// is unit-testable from fixtures, with no git or filesystem involved.
type ReportInput struct {
	GeneratedAt time.Time
	// Drift is nil when DETECT was skipped for this run.
	Drift *DriftReport
	// Regenerated is false when REGENERATE was skipped for this run - the
	// artifact/row-gen/mapping sections below are meaningless in that case
	// and renderReport says so instead of showing stale or zero data.
	Regenerated   bool
	Artifacts     []ArtifactDelta
	RowGenSummary string // row-gen's captured stderr summary line, "" if unavailable
	ProposalPath  string // path (relative to repo root) row-gen's full report was written to
	Mapping       MappingSummary
	MappingErr    string // set instead of Mapping when live/mapping.json couldn't be read
}

// WriteReport gathers a ReportInput (git diff/show, artifact reads,
// regen.Runs) and writes the rendered markdown to
// tmp/admission-pipeline/report-<timestamp>.md, returning that path.
func WriteReport(root string, drift *DriftReport, regen *RegenerateResult, log io.Writer) (string, error) {
	in := ReportInput{
		GeneratedAt: time.Now().UTC(),
		Drift:       drift,
		Regenerated: regen != nil,
	}

	if regen != nil {
		diffs, err := gitDiffNumstat(root, pipelineArtifactPaths)
		if err != nil {
			fmt.Fprintf(log, "admission-pipeline: report: git diff --numstat failed: %v\n", err)
			diffs = map[string][2]int{}
		}
		for _, path := range pipelineArtifactPaths {
			before := artifactCounts(root, "HEAD", path)
			after := artifactCounts(root, "", path)
			if before == nil && after == nil {
				continue // never existed either side - not this run's concern
			}
			stat := diffs[path]
			in.Artifacts = append(in.Artifacts, ArtifactDelta{
				Path:         path,
				LinesAdded:   stat[0],
				LinesRemoved: stat[1],
				Before:       before,
				After:        after,
			})
		}

		if row := regen.runFor("row-gen"); row != nil {
			in.RowGenSummary = findRowGenSummary(row.Stderr)
		}
		if regen.ProposalPath != "" {
			if rel, err := filepath.Rel(root, regen.ProposalPath); err == nil {
				in.ProposalPath = rel
			} else {
				in.ProposalPath = regen.ProposalPath
			}
		}

		summary, err := readMappingSummary(root)
		if err != nil {
			in.MappingErr = err.Error()
		} else {
			in.Mapping = summary
		}
	}

	rendered := renderReport(in)

	dir := reportsDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("report-%s.md", in.GeneratedAt.Format("20060102-150405")))
	if err := os.WriteFile(path, []byte(rendered), 0o644); err != nil { //nolint:gosec // a local scratch report, not a secret
		return "", err
	}
	return path, nil
}

// artifactCounts reads relPath's top-level "counts" JSON object, either
// from a git ref ("HEAD") or the working tree (ref == ""). Any failure
// (missing file, unparsable JSON, no counts field) reports nil rather than
// an error - a brand-new or removed artifact is an expected shape for
// REPORT to show, not a failure.
func artifactCounts(root, ref, relPath string) map[string]any {
	var data []byte
	var err error
	if ref == "" {
		data, err = os.ReadFile(filepath.Join(root, relPath))
	} else {
		data, err = gitShow(root, ref, relPath)
	}
	if err != nil {
		return nil
	}

	var hdr struct {
		Counts map[string]any `json:"counts"`
	}
	if err := json.Unmarshal(data, &hdr); err != nil {
		return nil
	}
	return hdr.Counts
}

func findRowGenSummary(stderr string) string {
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if rowGenSummaryRE.MatchString(line) {
			return line
		}
	}
	return ""
}

func readMappingSummary(root string) (MappingSummary, error) {
	data, err := os.ReadFile(filepath.Join(root, "live", "mapping.json"))
	if err != nil {
		return MappingSummary{}, fmt.Errorf("reading live/mapping.json: %w", err)
	}
	var m struct {
		Counts struct {
			// unclassified: mapping.go's MappingCounts.Unclassified as of
			// issue #53 (renamed from "none" when the taxonomy landed - this
			// struct's own field name, None, is kept as this package's
			// established vocabulary and is not renamed to match).
			None int `json:"unclassified"`
		} `json:"counts"`
		Former2Contradictions []json.RawMessage `json:"former2_contradictions"`
		Rows                  []struct {
			Via  string  `json:"via"`
			Note *string `json:"note"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return MappingSummary{}, fmt.Errorf("parsing live/mapping.json: %w", err)
	}

	summary := MappingSummary{
		Former2Contradictions: len(m.Former2Contradictions),
		None:                  m.Counts.None,
	}
	for _, row := range m.Rows {
		if row.Via == "none" && row.Note != nil && *row.Note == mappingUnexplainedNote {
			summary.Unclassifiable++
		}
	}
	return summary, nil
}

// renderReport is REPORT's pure formatting step: an in-memory ReportInput
// in, a markdown string out, no I/O. Kept separate from WriteReport so
// report generation is testable from fixtures alone (issue #55's
// deliverable #4).
func renderReport(in ReportInput) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# admission-pipeline report\n\n")
	fmt.Fprintf(&b, "Generated %s.\n\n", in.GeneratedAt.Format("2006-01-02 15:04 UTC"))

	renderPinStates(&b, in.Drift)
	fmt.Fprintln(&b)

	if !in.Regenerated {
		fmt.Fprintln(&b, "## REGENERATE was skipped for this run")
		fmt.Fprintln(&b, "\nNo artifact deltas, row-gen proposals, or mapping-gen summary to show.")
		return b.String()
	}

	renderArtifactDeltas(&b, in.Artifacts)
	fmt.Fprintln(&b)
	renderRowGen(&b, in.RowGenSummary, in.ProposalPath)
	fmt.Fprintln(&b)
	renderMapping(&b, in.Mapping, in.MappingErr)

	return b.String()
}

func renderPinStates(b *strings.Builder, drift *DriftReport) {
	fmt.Fprintln(b, "## Pin states")
	if drift == nil {
		fmt.Fprintln(b, "\nDETECT was skipped for this run.")
		return
	}
	fmt.Fprintln(b)
	fmt.Fprintln(b, "| pin | pinned | current | status |")
	fmt.Fprintln(b, "|---|---|---|---|")
	for _, p := range []PinState{drift.Provider, drift.Registry} {
		status := "OK"
		if p.Drifted {
			status = "DRIFT"
		}
		if p.Detail != "" {
			status += " - " + p.Detail
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s |\n", mdEscape(p.Name), mdEscape(p.Pinned), mdEscape(p.Current), mdEscape(status))
	}
}

func renderArtifactDeltas(b *strings.Builder, artifacts []ArtifactDelta) {
	fmt.Fprintln(b, "## Artifact count deltas")
	if len(artifacts) == 0 {
		fmt.Fprintln(b, "\nNo tracked artifact changed.")
		return
	}
	fmt.Fprintln(b)
	fmt.Fprintln(b, "| artifact | lines +/- | counts |")
	fmt.Fprintln(b, "|---|---|---|")
	for _, a := range artifacts {
		fmt.Fprintf(b, "| %s | +%d/-%d | %s |\n", a.Path, a.LinesAdded, a.LinesRemoved, diffCountsLine(a.Before, a.After))
	}
}

func renderRowGen(b *strings.Builder, summary, proposalPath string) {
	fmt.Fprintln(b, "## row-gen proposals")
	fmt.Fprintln(b)
	if summary == "" {
		fmt.Fprintln(b, "No row-gen summary line was captured.")
	} else {
		fmt.Fprintln(b, summary)
	}
	if proposalPath != "" {
		fmt.Fprintf(b, "\nFull report: `%s`\n", proposalPath)
	}
}

func renderMapping(b *strings.Builder, m MappingSummary, mappingErr string) {
	fmt.Fprintln(b, "## mapping-gen: former2 contradictions and unclassifiable types")
	fmt.Fprintln(b)
	if mappingErr != "" {
		fmt.Fprintf(b, "Could not read live/mapping.json: %s\n", mappingErr)
		return
	}
	fmt.Fprintf(b, "- former2 contradictions: %d (see live/mapping.json's former2_contradictions; must be acknowledged in mapping_gen_test.go's allowlist)\n", m.Former2Contradictions)
	fmt.Fprintf(b, "- via:none rows: %d total, %d unclassifiable (no curated reason at all)\n", m.None, m.Unclassifiable)
}

// diffCountsLine renders a "counts" object's before -> after as a
// comma-separated list, one entry per scalar (numeric) key present in
// after. Nested fields (e.g. by_via, primary_identifier_arity) are skipped
// - they're breakdowns of a scalar already shown, not additional totals.
func diffCountsLine(before, after map[string]any) string {
	if after == nil {
		return "(removed)"
	}
	var parts []string
	for _, k := range sortedKeys(after) {
		af, ok := after[k].(float64)
		if !ok {
			continue // a nested object/array, not a scalar count
		}
		bf, bok := before[k].(float64)
		switch {
		case before == nil:
			parts = append(parts, fmt.Sprintf("%s=%s (new)", k, trimFloat(af)))
		case !bok:
			parts = append(parts, fmt.Sprintf("%s=%s (new)", k, trimFloat(af)))
		case af == bf:
			parts = append(parts, fmt.Sprintf("%s=%s", k, trimFloat(af)))
		default:
			delta := af - bf
			sign := ""
			if delta > 0 {
				sign = "+"
			}
			parts = append(parts, fmt.Sprintf("%s=%s (%s%s)", k, trimFloat(af), sign, trimFloat(delta)))
		}
	}
	if len(parts) == 0 {
		return "(no scalar counts)"
	}
	return strings.Join(parts, ", ")
}

// trimFloat renders a JSON-decoded float64 as a plain integer when it holds
// one (every count field in these artifacts does), or with minimal decimals
// otherwise.
func trimFloat(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// mdEscape escapes the one character that would break a markdown table
// cell.
func mdEscape(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}
