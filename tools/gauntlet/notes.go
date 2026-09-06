// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
)

// notes.go implements `gauntlet notes <old-snapshot.json> <new-snapshot.json>`
// (#422): diffs two live/history/*.json snapshots (or live/gauntlet.json
// itself) and prints paste-ready markdown release highlights, so a release
// no longer ships with the milestone recorded in the artifact and nothing
// rendering it into the release body - v0.4.0's own boilerplate notes,
// while its snapshot recorded the board going core 20/25 -> 25/25, is
// exactly this gap.
//
// Schema note: live/history/v0.3.0.json is a byte-copy taken by cmdSnapshot
// before #414, so its artifact still carries top-level `commit` and
// `generated` fields that the post-#414 Artifact struct (artifact.go) no
// longer declares; live/history/v0.4.0.json was itself snapshotted at
// cab5355690, one commit BEFORE f27f19d443 landed the per-row-provenance
// change, so its rows in turn lack `last_run.emulator` even though the
// current schema (live/gauntlet.json today) has it on every row. Rather
// than branch on which schema a file is, this diff only ever reads fields
// present, in the same shape, in every schema seen so far: the top-level
// `emulator` and `sets` (both present since before v0.3.0.json), and each
// estate's `name` and `clear` (same). It deliberately never reads the
// top-level `commit`/`generated` pair (gone from the type since #414, for
// the reason recorded on Artifact's own doc comment: no procedure ever
// produced a board-wide "measured at commit X" fact honestly) or a fixed
// `last_run` shape - see latestCommit below for how it gets a commit
// reference without that field. loadArtifactFile (artifact.go) already
// unmarshals leniently - extra fields ignored, missing ones zero-valued -
// so no version switch is needed to read either file; what needs care is
// only choosing which fields to depend on.
func cmdNotes(root string, args []string) error {
	fs := flag.NewFlagSet("notes", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	pos := fs.Args()
	if len(pos) != 2 {
		return fmt.Errorf("notes needs exactly two snapshot paths: gauntlet notes <old-snapshot.json> <new-snapshot.json>")
	}
	oldA, err := loadArtifactFile(pos[0])
	if err != nil {
		return fmt.Errorf("old snapshot %s: %w", pos[0], err)
	}
	newA, err := loadArtifactFile(pos[1])
	if err != nil {
		return fmt.Errorf("new snapshot %s: %w", pos[1], err)
	}
	fmt.Print(RenderNotes(root, oldA, newA))
	return nil
}

// RenderNotes builds the release-highlights markdown from two already-loaded
// snapshots (old, then new). root is used only to look up live/readiness.json
// at each snapshot's own commit, when that file exists there; see
// readinessSection.
func RenderNotes(root string, oldA, newA *Artifact) string {
	var buf bytes.Buffer

	buf.WriteString("## Board movement\n\n")
	for _, key := range []string{"core", "all"} {
		o, n := oldA.Sets[key], newA.Sets[key]
		label := n.Label
		if label == "" {
			label = SetLabels[key]
		}
		fmt.Fprintf(&buf, "- %s: %d/%d clear -> %d/%d clear (%s)\n",
			label, o.Clear, o.Estates, n.Clear, n.Estates, signedDelta(n.Clear-o.Clear))
	}

	newlyCleared, regressed := estateMovement(oldA, newA)
	buf.WriteString("\n## Newly cleared\n\n")
	writeEstateList(&buf, newlyCleared)
	buf.WriteString("\n## Regressed\n\n")
	writeEstateList(&buf, regressed)

	if oldA.Emulator != "" && newA.Emulator != "" && oldA.Emulator != newA.Emulator {
		buf.WriteString("\n## Emulator\n\n")
		fmt.Fprintf(&buf, "- Repinned from `%s` to `%s`\n", oldA.Emulator, newA.Emulator)
	}

	if section := readinessSection(root, oldA, newA); section != "" {
		buf.WriteString("\n")
		buf.WriteString(section)
	}

	return buf.String()
}

func writeEstateList(buf *bytes.Buffer, names []string) {
	if len(names) == 0 {
		buf.WriteString("- none\n")
		return
	}
	for _, n := range names {
		fmt.Fprintf(buf, "- `%s`\n", n)
	}
}

func signedDelta(n int) string {
	if n > 0 {
		return fmt.Sprintf("+%d", n)
	}
	return fmt.Sprintf("%d", n)
}

// estateMovement compares newA's estates against oldA's by name and returns
// the two categories the Do note asks for: newly cleared and regressed. An
// estate absent from oldA (added to the manifest since) is neither - it has
// no prior verdict to move from - so it is silently skipped here, exactly
// as an estate absent from newA (dropped from the manifest since) is: this
// is a diff of shared estates' movement, not a manifest-membership diff.
func estateMovement(oldA, newA *Artifact) (newlyCleared, regressed []string) {
	for _, r := range newA.Estates {
		o, ok := oldA.Result(r.Name)
		if !ok {
			continue
		}
		switch {
		case !o.Clear && r.Clear:
			newlyCleared = append(newlyCleared, r.Name)
		case o.Clear && !r.Clear:
			regressed = append(regressed, r.Name)
		}
	}
	sort.Strings(newlyCleared)
	sort.Strings(regressed)
	return newlyCleared, regressed
}

// latestCommit approximates "the commit this snapshot was measured at"
// without the top-level `commit` field #414 removed (Artifact no longer
// declares it, on purpose - see this file's package comment). It reads the
// same per-row evidence the board banner itself now derives things from:
// the newest `last_run.commit` across the snapshot's own estate rows. Empty
// when no row has ever run, which is the honest answer, not a guess.
func latestCommit(a *Artifact) string {
	best, bestDate := "", ""
	for _, r := range a.Estates {
		if r.LastRun == nil || r.LastRun.Commit == "" {
			continue
		}
		// RFC3339 timestamps (time.RFC3339, always UTC here - run.go) sort
		// correctly as plain strings.
		if r.LastRun.Date > bestDate {
			bestDate, best = r.LastRun.Date, r.LastRun.Commit
		}
	}
	return best
}

// readinessSection renders readiness movement between the two snapshots'
// commits, or "" when it has nothing honest to say: no commit reference (a
// brand new snapshot with no runs yet), or live/readiness.json missing at
// either commit, both take this path. The diff (diffReadiness) is a
// shallow key-by-key compare for every key except `types`, which
// tools/readiness-gen (the code that defines the file) gives a specific,
// bounded render - see readinessTypesLine and #897.
func readinessSection(root string, oldA, newA *Artifact) string {
	oldCommit, newCommit := latestCommit(oldA), latestCommit(newA)
	if oldCommit == "" || newCommit == "" {
		return ""
	}
	oldB, err := gitShowFile(root, oldCommit, "live/readiness.json")
	if err != nil {
		return ""
	}
	newB, err := gitShowFile(root, newCommit, "live/readiness.json")
	if err != nil {
		return ""
	}
	section, err := diffReadiness(oldB, newB)
	if err != nil {
		return ""
	}
	return section
}

// gitShowFile reads path as it existed at commit, or an error if it did not
// exist there (or commit is unknown to this checkout) - both read the same
// way to the caller, which treats any error as "nothing to report".
func gitShowFile(root, commit, path string) ([]byte, error) {
	cmd := exec.Command("git", "show", commit+":"+path)
	cmd.Dir = root
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// diffReadiness renders a shallow key-by-key diff of two JSON objects: keys
// added, removed, or whose value's JSON text changed. It has no opinion on
// most of readiness.json's keys - that is for the code that defines the
// file to render more specifically, if a key other than `types` ever also
// needs it. `types` is the one exception (#897): tools/readiness-gen's own
// Artifact.Types is a 1699-entry array, essentially the whole file, so the
// same whole-value render that is fine for every scalar key here would
// print both arrays in full - 51,800 lines, 1.5 MB, measured against
// v0.12.0 -> v0.13.0. readinessTypesLine replaces it with a bounded count
// summary; every other key keeps the render below unchanged.
func diffReadiness(oldB, newB []byte) (string, error) {
	var oldM, newM map[string]json.RawMessage
	if err := json.Unmarshal(oldB, &oldM); err != nil {
		return "", err
	}
	if err := json.Unmarshal(newB, &newM); err != nil {
		return "", err
	}
	seen := map[string]bool{}
	var keys []string
	for k := range oldM {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for k := range newM {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	changed := false
	for _, k := range keys {
		ov, oOk := oldM[k]
		nv, nOk := newM[k]

		if k == readinessTypesKey {
			line, err := readinessTypesLine(ov, nv, oOk, nOk)
			if err != nil {
				return "", err
			}
			if line != "" {
				buf.WriteString(line)
				changed = true
			}
			continue
		}

		switch {
		case !oOk:
			fmt.Fprintf(&buf, "- `%s` added: %s\n", k, nv)
			changed = true
		case !nOk:
			fmt.Fprintf(&buf, "- `%s` removed (was %s)\n", k, ov)
			changed = true
		case string(ov) != string(nv):
			fmt.Fprintf(&buf, "- `%s`: %s -> %s\n", k, ov, nv)
			changed = true
		}
	}
	if !changed {
		return "", nil
	}
	return "## Readiness\n\n" + buf.String(), nil
}

// readinessTypesKey is live/readiness.json's one large top-level key
// (tools/readiness-gen's Artifact.Types, `json:"types"`) - the array
// diffReadiness must never render whole (#897).
const readinessTypesKey = "types"

// maxNamedReadinessMovers bounds how many type names readinessTypesLine
// lists per category before folding the remainder into a "+N more" tail.
// This is the fix for #897: without a bound, a release with many tier
// movements at once reproduces the same unbounded-output problem at a
// smaller scale.
const maxNamedReadinessMovers = 10

// readinessTypeRow mirrors the fields of tools/readiness-gen's Row
// (tools/readiness-gen/build.go) that this diff needs: `type`, the array's
// identifying key every row is keyed on when the artifact is built, and
// `tier`, the field HANDOFF.md's "696 of 1699 types can be held only by a
// record" line is about. It does not import tools/readiness-gen (a
// separate main package) or depend on Row's other fields (status, facts) -
// pulling those in would recouple this tool to a schema it does not
// otherwise read.
type readinessTypeRow struct {
	Type string `json:"type"`
	Tier string `json:"tier"`
}

// readinessTypesLine renders the `types` key as one bounded bullet: total
// count, how many changed tier (with named movers), how many were added,
// how many removed. oOk/nOk false (the key absent from one snapshot - a
// readiness.json that did not have a `types` array yet) is treated as an
// empty array on that side, so a freshly-added file still gets a bounded
// line rather than diffReadiness's default "added: <whole value>" render.
// Returns "" when the two arrays parse to the same set of (type, tier)
// pairs - nothing to report.
func readinessTypesLine(ov, nv json.RawMessage, oOk, nOk bool) (string, error) {
	if !oOk {
		ov = json.RawMessage("[]")
	}
	if !nOk {
		nv = json.RawMessage("[]")
	}
	var oldRows, newRows []readinessTypeRow
	if err := json.Unmarshal(ov, &oldRows); err != nil {
		return "", err
	}
	if err := json.Unmarshal(nv, &newRows); err != nil {
		return "", err
	}

	oldByType := make(map[string]readinessTypeRow, len(oldRows))
	for _, r := range oldRows {
		oldByType[r.Type] = r
	}
	newByType := make(map[string]readinessTypeRow, len(newRows))
	for _, r := range newRows {
		newByType[r.Type] = r
	}

	var tierMovers, added, removed []string
	for _, n := range newRows {
		o, ok := oldByType[n.Type]
		switch {
		case !ok:
			added = append(added, n.Type)
		case o.Tier != n.Tier:
			tierMovers = append(tierMovers, fmt.Sprintf("%s %s->%s", n.Type, o.Tier, n.Tier))
		}
	}
	for _, o := range oldRows {
		if _, ok := newByType[o.Type]; !ok {
			removed = append(removed, o.Type)
		}
	}
	if len(tierMovers) == 0 && len(added) == 0 && len(removed) == 0 {
		return "", nil
	}
	sort.Strings(tierMovers)
	sort.Strings(added)
	sort.Strings(removed)

	return fmt.Sprintf("- `types`: %d types, %d changed tier%s, %d added, %d removed\n",
		len(newRows), len(tierMovers), namedTail(tierMovers), len(added), len(removed)), nil
}

// namedTail renders a bounded " (name, name, ... +N more)" suffix for a
// sorted slice of names, or "" for an empty slice. The bound -
// maxNamedReadinessMovers, the whole fix for #897 - means anything past it
// collapses into a single "+N more" count rather than another name.
func namedTail(names []string) string {
	if len(names) == 0 {
		return ""
	}
	shown := names
	extra := 0
	if len(shown) > maxNamedReadinessMovers {
		extra = len(shown) - maxNamedReadinessMovers
		shown = shown[:maxNamedReadinessMovers]
	}
	tail := " (" + strings.Join(shown, ", ")
	if extra > 0 {
		tail += fmt.Sprintf(", +%d more", extra)
	}
	tail += ")"
	return tail
}
