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

// surveyArtifact is live/survey-full.json narrowed to what this report
// reads: the provider header, and per type only the type name. Roster
// movement (types added or removed by the new release) is a name-set diff;
// nothing else here reads Path or Signals - readinessArtifact already
// carries the tier/status verdict those signals decided, which is the more
// useful axis for a movement report to read a type's shape off.
type surveyArtifact struct {
	Provider        string      `json:"provider"`
	ProviderVersion string      `json:"provider_version"`
	Types           []surveyRow `json:"types"`
}

// surveyRow is one live/survey-full.json entry, narrowed to its type name.
type surveyRow struct {
	Type string `json:"type"`
}

// readinessArtifact is live/readiness.json narrowed to the tier/status
// verdict per type - the tier definitions (#417)'s own partition.
type readinessArtifact struct {
	ProviderVersion string         `json:"provider_version"`
	Types           []readinessRow `json:"types"`
}

// readinessRow is one live/readiness.json entry, narrowed to its verdict.
type readinessRow struct {
	Type   string `json:"type"`
	Tier   string `json:"tier"`
	Status string `json:"status"`
}

// schemaPrecedenceArtifact is live/schema-precedence.json narrowed to what
// this report reads: issue #387's measurement, tools/row-gen/schemafirst.go's
// own artifact shape.
type schemaPrecedenceArtifact struct {
	HasIdentitySchema  int      `json:"has_identity_schema"`
	Reproduced         []string `json:"reproduced"`
	ReproducedCount    int      `json:"reproduced_count"`
	NotReproducedCount int      `json:"not_reproduced_count"`
}

// mismatchesArtifact is live/rowgen-mismatches.json narrowed to the two
// counts this report reads. Deliberately no ratio: issue #695 retired
// adopted-unchanged, and the section below reports movement in unruled
// mismatches, which is a thing to act on, rather than a percentage that was
// read as coverage three sessions running.
type mismatchesArtifact struct {
	Summary struct {
		Compared              int `json:"compared"`
		GenuineMismatches     int `json:"genuine_mismatches"`
		UnannotatedMismatches int `json:"unannotated_mismatches"`
	} `json:"summary"`
}

// bumpArtifacts bundles one side (before or after) of the four committed
// artifacts a provider bump touches.
type bumpArtifacts struct {
	Survey           surveyArtifact
	Readiness        readinessArtifact
	SchemaPrecedence schemaPrecedenceArtifact
	Mismatches       mismatchesArtifact
}

// goldenResult is whether and how internal/live/check's TestIdentityGolden
// (HANDOFF.md's own "1738 rendered identities... pinned by value" guard)
// ran against the working tree after regeneration.
type goldenResult struct {
	Ran    bool
	Passed bool
	Output string
}

// listCap bounds how many individual type names a section prints before
// falling back to "...and N more": the roster-level and schema-precedence
// deltas a real provider release produces are usually single digits, but a
// pathological run (a bad -provider-version, a stale old-ref) should not
// turn this report into a multi-thousand-line dump.
const listCap = 50

// buildReport is the whole of this tool's output: pure over its three
// arguments, so report_test.go exercises it with hand-built fixtures and no
// git, no filesystem and no subprocess. old is the artifacts as committed at
// -old-ref (HEAD by default); new is the same four artifacts as they stand
// on disk after `just provider-bump <version>` re-ran survey-gen,
// readiness-gen and row-gen's two measuring modes. golden is the result of running
// internal/live/check's TestIdentityGolden against the regenerated tree, or
// the zero value when -skip-golden-test was passed.
func buildReport(old, new bumpArtifacts, golden goldenResult) string {
	var b strings.Builder
	movement := false

	oldV, newV := old.Survey.ProviderVersion, new.Survey.ProviderVersion
	fmt.Fprintf(&b, "provider-bump report: hashicorp/aws %s -> %s\n\n", oldV, newV)

	// 1. Roster movement: types added or removed by the new release.
	oldTypes := surveyTypeSet(old.Survey)
	newTypes := surveyTypeSet(new.Survey)
	added := setDiff(newTypes, oldTypes)
	removed := setDiff(oldTypes, newTypes)
	fmt.Fprintf(&b, "## Types (live/survey-full.json): %d -> %d\n", len(oldTypes), len(newTypes))
	if len(added) == 0 && len(removed) == 0 {
		b.WriteString("no type added or removed\n\n")
	} else {
		movement = true
		fmt.Fprintf(&b, "%d added, %d removed\n", len(added), len(removed))
		writeList(&b, "+ ", added)
		writeList(&b, "- ", removed)
		b.WriteString("\n")
	}

	// 2. Tier/status movement: every type present in both readiness
	// artifacts whose tier or status changed, tallied by the (before, after)
	// pair actually seen. A type only on one side is already covered by the
	// roster diff above.
	oldReadiness := readinessByType(old.Readiness)
	newReadiness := readinessByType(new.Readiness)
	transitions := map[string]int{}
	var movedTypes []string
	for t, nr := range newReadiness {
		or, ok := oldReadiness[t]
		if !ok || or.Tier == nr.Tier && or.Status == nr.Status {
			continue
		}
		transitions[fmt.Sprintf("%s/%s -> %s/%s", or.Tier, or.Status, nr.Tier, nr.Status)]++
		movedTypes = append(movedTypes, t)
	}
	fmt.Fprintf(&b, "## Tier/status movement (live/readiness.json)\n")
	if len(transitions) == 0 {
		b.WriteString("no type's tier or status changed\n\n")
	} else {
		movement = true
		keys := make([]string, 0, len(transitions))
		for k := range transitions {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "  %d type(s): %s\n", transitions[k], k)
		}
		sort.Strings(movedTypes)
		for i, t := range movedTypes {
			if i >= listCap {
				fmt.Fprintf(&b, "    ...and %d more\n", len(movedTypes)-listCap)
				break
			}
			fmt.Fprintf(&b, "    %s: %s/%s -> %s/%s\n", t, oldReadiness[t].Tier, oldReadiness[t].Status, newReadiness[t].Tier, newReadiness[t].Status)
		}
		b.WriteString("\n")
	}

	// 3. Schema-precedence, issue #387 (ruling 2 of
	// the foundation-order ruling (#388)): whether the provider's own
	// identity schema reproduces the ratified row, for every ratified type
	// carrying one - live/schema-precedence.json, tools/row-gen/
	// schemafirst.go's own measurement.
	oSR, nSR := old.SchemaPrecedence, new.SchemaPrecedence
	fmt.Fprintf(&b, "## Schema-precedence, #387 (live/schema-precedence.json)\n")
	fmt.Fprintf(&b, "  candidates with a provider identity schema: %d -> %d\n", oSR.HasIdentitySchema, nSR.HasIdentitySchema)
	fmt.Fprintf(&b, "  reproduced (schema agrees with the ratified row): %d -> %d\n", oSR.ReproducedCount, nSR.ReproducedCount)
	fmt.Fprintf(&b, "  not reproduced: %d -> %d\n", oSR.NotReproducedCount, nSR.NotReproducedCount)
	oRep, nRep := setOf(oSR.Reproduced), setOf(nSR.Reproduced)
	flippedIn := setDiff(nRep, oRep)
	flippedOut := setDiff(oRep, nRep)
	if len(flippedIn) == 0 && len(flippedOut) == 0 {
		b.WriteString("  no type's schema-precedence verdict changed\n\n")
	} else {
		movement = true
		writeList(&b, "    + now reproduced: ", flippedIn)
		writeList(&b, "    - no longer reproduced: ", flippedOut)
		b.WriteString("\n")
	}

	// 4. Classifier mismatches: NOT a coverage metric. This is row-gen's
	// fresh classifier measured against tools/row-gen/ratified.json, the
	// human-ratified ledger that -emit copies verbatim, so a moved count
	// says the classifier's own agreement with past judgment shifted, never
	// that support changed. Only the unruled count is an alarm: an unruled
	// mismatch is a row nobody has looked at, which is the one thing here a
	// bump can introduce and a human has to act on.
	fmt.Fprintf(&b, "## Classifier mismatches (rowgen-mismatches.json; NOT a coverage metric)\n")
	fmt.Fprintf(&b, "  compared: %d -> %d\n", old.Mismatches.Summary.Compared, new.Mismatches.Summary.Compared)
	fmt.Fprintf(&b, "  genuine mismatches: %d (%d unruled) -> %d (%d unruled)\n",
		old.Mismatches.Summary.GenuineMismatches, old.Mismatches.Summary.UnannotatedMismatches,
		new.Mismatches.Summary.GenuineMismatches, new.Mismatches.Summary.UnannotatedMismatches)
	if new.Mismatches.Summary.UnannotatedMismatches > old.Mismatches.Summary.UnannotatedMismatches {
		movement = true
		b.WriteString("  NEW unruled mismatch(es) - review before ratifying\n")
	}
	b.WriteString("\n")

	// 5. Golden diff summary: internal/live/check's own golden-pinned
	// identity table (HANDOFF.md's enforced-guards table). A version-only
	// bump never touches it on its own - identity.DefaultTable and
	// identity.MarkerlessTypes are compiled Go, written only by
	// `row-gen -emit` against a human-ratified tools/row-gen/ratified.json,
	// not read live from live/survey-full.json - so this section is the
	// proof that stays true, not a formality: if it ever reports MOVED for
	// a plain version bump, something now reads survey data at a layer this
	// report did not expect.
	b.WriteString("## Golden identity table (internal/live/check.TestIdentityGolden)\n")
	switch {
	case !golden.Ran:
		b.WriteString("  not run (-skip-golden-test)\n\n")
	case golden.Passed:
		b.WriteString("  unchanged: every rendered identity still matches the committed golden file. A provider bump alone never rewrites internal/live/identity.DefaultTable; `row-gen -emit` against a re-ratified tools/row-gen/ratified.json is a separate, reviewed step.\n\n")
	default:
		movement = true
		b.WriteString("  MOVED: `go test ./internal/live/check/ -run TestIdentityGolden` failed against the regenerated tree. Review the diff below; something outside this report's expected inputs changed identity resolution.\n\n")
		b.WriteString(indent(golden.Output))
		b.WriteString("\n")
	}

	if movement {
		b.WriteString("MOVEMENT DETECTED - review every section above before committing.\n")
	} else {
		b.WriteString("ZERO MOVEMENT: every type, tier, status, schema-precedence verdict and the golden identity table are unchanged.\n")
	}
	return b.String()
}

func surveyTypeSet(s surveyArtifact) map[string]bool {
	out := make(map[string]bool, len(s.Types))
	for _, r := range s.Types {
		out[r.Type] = true
	}
	return out
}

func readinessByType(a readinessArtifact) map[string]readinessRow {
	out := make(map[string]readinessRow, len(a.Types))
	for _, r := range a.Types {
		out[r.Type] = r
	}
	return out
}

func setOf(ss []string) map[string]bool {
	out := make(map[string]bool, len(ss))
	for _, s := range ss {
		out[s] = true
	}
	return out
}

// setDiff returns the sorted members of a that are not in b.
func setDiff(a, b map[string]bool) []string {
	var out []string
	for k := range a {
		if !b[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// writeList prints one prefixed line per name, capped at listCap with a
// trailing "...and N more" note - see listCap's own doc comment. A nil or
// empty names writes nothing at all.
func writeList(b *strings.Builder, prefix string, names []string) {
	for i, n := range names {
		if i >= listCap {
			fmt.Fprintf(b, "  ...and %d more\n", len(names)-listCap)
			return
		}
		fmt.Fprintf(b, "  %s%s\n", prefix, n)
	}
}

// indent prefixes every line of s with two spaces, for nesting go test's own
// output under this report's golden-diff section.
func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n") + "\n"
}
