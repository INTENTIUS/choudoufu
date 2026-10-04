// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// cmdSetNotes rewrites one row's free-text notes and nothing else (#1374).
//
// A row's notes were seeded once by import-legacy and no other subcommand
// writes them, so before this command the only way to change one was a
// hand edit of live/gauntlet.json, which CLAUDE.md rules out for a
// measured artifact. This is the narrow writer: it reads the committed
// artifact, replaces the named row's Notes, and writes the artifact back in
// its canonical encoding. It does not Rebuild, so no verdict, clear flag,
// set summary or last_run can move, and it refuses outright when:
//
//   - the committed file is not already in canonical form (a re-encode
//     would change bytes this command has no business changing);
//   - the estate has no row, or the new text equals the old;
//   - after the edit, anything other than that row's notes differs (the
//     artifact is compared with the edited field blanked on both sides).
//
// The site copies are not written here; `gauntlet render` refreshes them
// from the artifact as it does after any other change.
func cmdSetNotes(root string, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("set-notes", flag.ContinueOnError)
	estate := fs.String("estate", "", "the row whose notes to replace")
	file := fs.String("file", "", "a file holding the new notes text, taken verbatim (one trailing newline is dropped)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *estate == "" || *file == "" || fs.NArg() != 0 {
		return errors.New("set-notes needs -estate <name> -file <path> and nothing else")
	}
	text, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	notes := strings.TrimSuffix(string(text), "\n")

	path := filepath.Join(root, ArtifactPath)
	committed, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	a, err := LoadArtifact(root)
	if err != nil {
		return err
	}
	out, err := setNotes(a, committed, *estate, notes)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "set-notes: rewrote %s's notes; nothing else in %s changed. Run `go run ./tools/gauntlet render` to refresh the site copies.\n", *estate, ArtifactPath)
	return nil
}

// setNotes is cmdSetNotes without the filesystem: it returns the new
// canonical artifact bytes, or an error naming why it refused.
func setNotes(a *Artifact, committed []byte, estate, notes string) ([]byte, error) {
	before, err := a.Canonical()
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(before, committed) {
		return nil, fmt.Errorf("%s is not in the canonical encoding this tool writes; refusing to rewrite bytes beyond one row's notes (render or re-measure first)", ArtifactPath)
	}
	idx := -1
	for i := range a.Estates {
		if a.Estates[i].Name == estate {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("no row named %q in %s", estate, ArtifactPath)
	}
	old := a.Estates[idx].Notes
	if old == notes {
		return nil, fmt.Errorf("%s's notes already read exactly that; nothing to do", estate)
	}

	// Blank the edited field on both sides and require the rest to be
	// byte-identical. Structurally it cannot differ (only Notes is
	// assigned); this is the check that says so rather than assuming it.
	a.Estates[idx].Notes = ""
	blankBefore, err := a.Canonical()
	if err != nil {
		return nil, err
	}
	a.Estates[idx].Notes = notes
	after, err := a.Canonical()
	if err != nil {
		return nil, err
	}
	a.Estates[idx].Notes = ""
	blankAfter, err := a.Canonical()
	a.Estates[idx].Notes = notes
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(blankBefore, blankAfter) {
		return nil, errors.New("refusing: the edit changed more than one row's notes")
	}
	return after, nil
}
