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
// commits, or "" when it has nothing honest to say. live/readiness.json
// does not exist yet as of #422 - this is written for when it does, and
// degrades to "" (no section, no error) rather than fail while it doesn't:
// no commit reference (a brand new snapshot with no runs yet), or the file
// missing at either commit (true today, for every commit), both take this
// path. The diff itself is schema-agnostic (a shallow key-by-key compare of
// whatever JSON object is there) on purpose - nothing in this repository
// has defined live/readiness.json's shape yet, so this does not guess one.
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
// what readiness.json's keys mean - that is for the code that defines the
// file to render more specifically, once it exists.
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
