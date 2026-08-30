// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"sort"
	"strings"
)

// oracleVersions mirrors tools/gauntlet's OracleVersions (artifact.go) - the
// stock terraform/tofu releases a gauntlet.json snapshot pins (top-level
// "oracle") or a row's own last_run recorded ("last_run.oracle").
type oracleVersions struct {
	Terraform string `json:"terraform"`
	Tofu      string `json:"tofu"`
}

// setSummary narrows live/gauntlet.json's "sets" entries to the two counts
// a board-movement line needs.
type setSummary struct {
	Estates int `json:"estates"`
	Clear   int `json:"clear"`
}

// estateLastRun narrows an estate row's "last_run" to the one field this
// report reads: which oracle that row's verdicts were last measured
// against.
type estateLastRun struct {
	Oracle *oracleVersions `json:"oracle,omitempty"`
}

// estateRow is one live/gauntlet.json "estates" entry, narrowed to what
// this report reads: its stage verdicts, its clear flag, and its
// provenance.
type estateRow struct {
	Name    string            `json:"name"`
	Clear   bool              `json:"clear"`
	Stages  map[string]string `json:"stages"`
	LastRun *estateLastRun    `json:"last_run,omitempty"`
}

// gauntletArtifact is live/gauntlet.json narrowed to what this report
// reads: the pinned oracle, the headline set counts, and every estate row.
type gauntletArtifact struct {
	Oracle  oracleVersions        `json:"oracle"`
	Sets    map[string]setSummary `json:"sets"`
	Estates []estateRow           `json:"estates"`
}

// listCap bounds how many estate names a section prints before falling back
// to "...and N more" - see tools/provider-bump-report/report.go's own
// listCap for the identical reasoning.
const listCap = 50

// buildReport is the whole of this tool's output: pure over its two
// arguments, so report_test.go exercises it with hand-built fixtures and no
// git, no filesystem and no subprocess. old is live/gauntlet.json as
// committed at -old-ref (HEAD by default); new is the same artifact as it
// stands on disk after a real `go run ./tools/gauntlet run` re-measured
// against a bumped live/oracle-versions.json.
func buildReport(old, new gauntletArtifact) string {
	var b strings.Builder
	movement := false

	// 0. The pin itself - printed unconditionally, the same way
	// provider-bump-report's header names both provider versions without
	// that line alone counting as movement; the sections below are what
	// decide movement.
	fmt.Fprintf(&b, "oracle-bump report: terraform %s -> %s, tofu %s -> %s\n\n",
		display(old.Oracle.Terraform), display(new.Oracle.Terraform),
		display(old.Oracle.Tofu), display(new.Oracle.Tofu))

	// 1. Board movement: each set's clear/estate counts, before and after.
	fmt.Fprintf(&b, "## Board (live/gauntlet.json sets)\n")
	setKeys := unionKeys(old.Sets, new.Sets)
	anySetMoved := false
	for _, k := range setKeys {
		o, n := old.Sets[k], new.Sets[k]
		line := fmt.Sprintf("  %s: %d/%d clear -> %d/%d clear", k, o.Clear, o.Estates, n.Clear, n.Estates)
		if o != n {
			anySetMoved = true
			line += "  MOVED"
		}
		fmt.Fprintln(&b, line)
	}
	if len(setKeys) == 0 {
		b.WriteString("  no sets recorded on either side\n")
	} else if !anySetMoved {
		b.WriteString("  no set's clear/estate counts changed\n")
	}
	if anySetMoved {
		movement = true
	}
	b.WriteString("\n")

	// 2. Per-estate stage movement: every stage id whose verdict differs,
	// for every estate present on both sides - an estate only on one side
	// is a roster change (an estate added/removed from the manifest), which
	// is not this report's concern and is left to what added it.
	oldByName := estateByName(old.Estates)
	newByName := estateByName(new.Estates)
	names := unionKeys(oldByName, newByName)
	var movedEstates []string
	estateLines := map[string][]string{}
	for _, name := range names {
		o, ook := oldByName[name]
		n, nok := newByName[name]
		if !ook || !nok {
			continue
		}
		var lines []string
		for _, sid := range unionKeys(o.Stages, n.Stages) {
			ov, nv := o.Stages[sid], n.Stages[sid]
			if ov == nv {
				continue
			}
			lines = append(lines, fmt.Sprintf("%s: %s -> %s", sid, display(ov), display(nv)))
		}
		if o.Clear != n.Clear {
			lines = append(lines, fmt.Sprintf("clear: %v -> %v", o.Clear, n.Clear))
		}
		if len(lines) > 0 {
			movedEstates = append(movedEstates, name)
			estateLines[name] = lines
		}
	}
	fmt.Fprintf(&b, "## Per-estate stage movement\n")
	if len(movedEstates) == 0 {
		b.WriteString("no estate's stage verdicts or clear flag changed\n\n")
	} else {
		movement = true
		sort.Strings(movedEstates)
		for i, name := range movedEstates {
			if i >= listCap {
				fmt.Fprintf(&b, "  ...and %d more estate(s)\n", len(movedEstates)-listCap)
				break
			}
			for _, line := range estateLines[name] {
				fmt.Fprintf(&b, "  %s: %s\n", name, line)
			}
		}
		b.WriteString("\n")
	}

	// 3. Provenance: which rows on the AFTER side actually carry the new
	// pin as their own measured evidence (last_run.oracle), versus which
	// were simply not touched by this run's -set and so still show
	// whatever they last recorded (older, or never recorded at all). This
	// is not "movement" on its own - a partial -set run is expected and
	// legitimate - but a reviewer deciding whether the report above can be
	// trusted as complete needs to see it.
	fmt.Fprintf(&b, "## Provenance (last_run.oracle vs the new pin, %s / %s)\n", display(new.Oracle.Terraform), display(new.Oracle.Tofu))
	var reflects, lagging []string
	for _, name := range names {
		n, ok := newByName[name]
		if !ok || n.LastRun == nil || n.LastRun.Oracle == nil {
			continue
		}
		if *n.LastRun.Oracle == new.Oracle {
			reflects = append(reflects, name)
		} else {
			lagging = append(lagging, name)
		}
	}
	sort.Strings(reflects)
	sort.Strings(lagging)
	fmt.Fprintf(&b, "  %d estate(s) re-measured against the new pin\n", len(reflects))
	if len(lagging) > 0 {
		fmt.Fprintf(&b, "  %d estate(s) still carry a different (or no) recorded oracle - not touched by this run's -set:\n", len(lagging))
		writeList(&b, "    ", lagging)
	}
	unrecorded := len(names) - len(reflects) - len(lagging)
	if unrecorded > 0 {
		fmt.Fprintf(&b, "  %d estate(s) carry no last_run.oracle at all (from before issue #544, or never run since)\n", unrecorded)
	}
	b.WriteString("\n")

	if movement {
		b.WriteString("MOVEMENT DETECTED - review every section above before committing.\n")
	} else {
		b.WriteString("ZERO MOVEMENT: no set's clear/estate counts and no estate's stage verdicts or clear flag changed.\n")
	}
	return b.String()
}

// display renders an empty version string as "(none)" rather than a blank,
// so a report section never silently loses a field to whitespace.
func display(v string) string {
	if v == "" {
		return "(none)"
	}
	return v
}

func estateByName(rows []estateRow) map[string]estateRow {
	out := make(map[string]estateRow, len(rows))
	for _, r := range rows {
		out[r.Name] = r
	}
	return out
}

// unionKeys returns the sorted union of two maps' keys, generic over the
// value type so it serves both map[string]setSummary and map[string]estateRow.
func unionKeys[V any](a, b map[string]V) []string {
	seen := make(map[string]bool, len(a)+len(b))
	var out []string
	for k := range a {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for k := range b {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// writeList prints one prefixed line per name, capped at listCap with a
// trailing "...and N more" note.
func writeList(b *strings.Builder, prefix string, names []string) {
	for i, n := range names {
		if i >= listCap {
			fmt.Fprintf(b, "%s...and %d more\n", prefix, len(names)-listCap)
			return
		}
		fmt.Fprintf(b, "%s%s\n", prefix, n)
	}
}
