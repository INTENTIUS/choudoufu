// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #1308. site/data/gauntlet_board.json is generated, committed, and
// conflicts on every merge that moved a verdict. Most of a bad merge of it
// is already caught: StaleFilesReport renders the board fresh and compares
// byte for byte, so a merged banner, a merged cell, a merged duration all
// come back stale. The exception is the three #1264 script-staleness
// fields, which that comparison strips before comparing because holding a
// committed board to a git comparison would make every estate-script pull
// request a re-render pull request.
//
// These tests are about that exception, and about the difference between a
// board that LAGS - one whole answer, just an older one, which #1264
// deliberately allows - and one that is INCOHERENT, which a line-based
// merge produces and which no render ever could.

// mergeBoards runs the same three-way line merge git would, and reports
// whether it conflicted.
func mergeBoards(t *testing.T, ours, base, theirs []byte) (out []byte, conflicted bool) {
	t.Helper()
	dir := t.TempDir()
	w := func(n string, b []byte) string {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cmd := exec.Command("git", "merge-file", "-p", w("ours", ours), w("base", base), w("theirs", theirs))
	o, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() > 0 {
			return o, true
		}
		t.Fatal(err)
	}
	return o, false
}

// boardMergeFixture builds three boards over the same estates: a base with
// nothing stale, "ours" where one estate's script has moved, and "theirs"
// where a different one's has. It is the shape the five conflicted merges
// in this repository's history had on 2026-09-18.
func boardMergeFixture(t *testing.T) (base, ours, theirs Board, names []string) {
	t.Helper()
	names = []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot"}
	var es []Estate
	a := &Artifact{Emulator: "sha256:probe"}
	for _, n := range names {
		es = append(es, Estate{Name: n, Source: "s", Lane: "reference", Set: SetCore, Reason: "r"})
		a.Estates = append(a.Estates, EstateResult{
			Name:    n,
			Stages:  stagesAllNotRun(),
			LastRun: &LastRun{Commit: strings.Repeat("0", 40), Date: "2026-09-01T00:00:00Z", Emulator: "sha256:probe"},
		})
	}
	m := &Manifest{Estates: es}
	a.Rebuild(m, nil, "sha256:probe", OracleVersions{})

	st := func(changed ...string) map[string]ScriptStaleness {
		out := map[string]ScriptStaleness{}
		for _, n := range names {
			out[n] = ScriptStaleness{State: ScriptCurrent}
		}
		for _, n := range changed {
			out[n] = ScriptStaleness{State: ScriptChanged, Changed: []string{"live/e2e/" + n + "/run.sh"}}
		}
		return out
	}
	return buildBoard(m, a, st()), buildBoard(m, a, st("alpha")), buildBoard(m, a, st("foxtrot")), names
}

func canonBoard(t *testing.T, b Board) []byte {
	t.Helper()
	c, err := b.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestTextualBoardMergeConflictsOnlyInAdvisoryFields is the measurement the
// fix rests on: two branches whose boards differ only in #1264's fields
// conflict, and every hunk of that conflict is in a field the render
// comparison strips. So whichever side a human picks for each hunk
// independently, the result is a board the render comparison will not
// block - which is fine when the hunks are resolved consistently and is
// #1308's hazard when they are not.
func TestTextualBoardMergeConflictsOnlyInAdvisoryFields(t *testing.T) {
	base, ours, theirs, _ := boardMergeFixture(t)
	merged, conflicted := mergeBoards(t, canonBoard(t, ours), canonBoard(t, base), canonBoard(t, theirs))
	if !conflicted {
		t.Fatalf("expected the two renders to conflict; they did not, so this test no longer measures what it was written for:\n%s", firstDiffLine(canonBoard(t, ours), canonBoard(t, theirs)))
	}
	for _, line := range strings.Split(string(merged), "\n") {
		if !strings.HasPrefix(line, "<<<<<<<") {
			continue
		}
		return // at least one conflict hunk, which is all this asserts
	}
	t.Fatal("git reported a conflict but wrote no conflict markers")
}

// TestHunkResolvedBoardIsCaught is the arm the issue asks for. Resolve the
// banner hunk from one side and the row hunks from the other - the
// mechanical result of `git checkout --theirs` on one region and `--ours`
// on another, or of a merge tool applied hunk by hunk - and the board
// claims one row is stale while a different row carries the badge.
//
// Before: the render comparison strips exactly these fields, so the board
// reads as advisory and lands. After: it is rejected, because the sentence
// no longer describes the rows under it.
func TestHunkResolvedBoardIsCaught(t *testing.T) {
	base, ours, theirs, _ := boardMergeFixture(t)

	// The banner from "ours" (alpha is the stale row), the badges from
	// "theirs" (foxtrot is).
	franken := theirs
	franken.ScriptBanner = ours.ScriptBanner
	if franken.ScriptBanner == theirs.ScriptBanner {
		t.Fatal("the two sides' banners are identical; the fixture no longer builds the mixed board")
	}

	// Before: indistinguishable from a lagging board as far as the render
	// comparison is concerned.
	truth := canonBoard(t, base)
	if !boardsDifferOnlyInScriptStaleness(truth, canonBoard(t, franken)) {
		t.Fatal("the mixed board differs from a fresh render outside the advisory fields, so this test is not exercising the hole it was written for")
	}

	// After.
	if err := BoardSelfConsistent(franken); err == nil {
		t.Fatal("a board whose banner counts one row while a different row carries the badge was accepted")
	} else if !strings.Contains(err.Error(), "script_banner") {
		t.Fatalf("rejected for the wrong reason: %v", err)
	}

	// And the whole point of #1264 survives: each side on its own, which is
	// a real render that has merely gone behind the tree, is accepted.
	for name, b := range map[string]Board{"base": base, "ours": ours, "theirs": theirs} {
		if err := BoardSelfConsistent(b); err != nil {
			t.Errorf("%s is a board some render really produced and must be accepted: %v", name, err)
		}
	}
}

// TestStaleFilesReportBlocksAnIncoherentBoard is the same claim at the
// surface that actually gates a merge: the advisory bucket must not absorb
// a board that contradicts itself, and must still absorb one that only
// lags.
func TestStaleFilesReportBlocksAnIncoherentBoard(t *testing.T) {
	_, ours, theirs, _ := boardMergeFixture(t)
	franken := theirs
	franken.ScriptBanner = ours.ScriptBanner

	if err := committedBoardSelfConsistent(canonBoard(t, franken)); err == nil {
		t.Error("the mixed board passed the gate StaleFilesReport puts on its advisory bucket")
	}
	if err := committedBoardSelfConsistent(canonBoard(t, ours)); err != nil {
		t.Errorf("a board that merely lags must stay advisory (#1264): %v", err)
	}
	// Bytes that are not a board at all are a difference in their own
	// right; this gate says nothing about them rather than guessing.
	if err := committedBoardSelfConsistent([]byte("<<<<<<< ours")); err != nil {
		t.Errorf("unparseable bytes should be left to the byte comparison: %v", err)
	}
}

// TestBoardSelfConsistencyRejectsEachWayItCanBreak: the check is written
// from what the banner promises, and every promise is proven breakable.
func TestBoardSelfConsistencyRejectsEachWayItCanBreak(t *testing.T) {
	base, _, theirs, _ := boardMergeFixture(t)

	cases := []struct {
		name string
		want string
		of   func(b Board) Board
	}{
		{"a badge with no banner behind it", "script_banner is empty", func(b Board) Board {
			b.ScriptBanner = ""
			return b
		}},
		{"a badge dropped from the rows", "script_banner", func(b Board) Board {
			for i := range b.Estates {
				b.Estates[i].ScriptStale = ""
				b.Estates[i].ScriptNote = ""
			}
			return b
		}},
		{"a note kept while its badge was dropped", "no script_stale badge", func(b Board) Board {
			for i := range b.Estates {
				if b.Estates[i].ScriptStale != "" {
					b.Estates[i].ScriptStale = ""
				}
			}
			return b
		}},
		{"a badge with no note", "does not say since when", func(b Board) Board {
			for i := range b.Estates {
				if b.Estates[i].ScriptStale != "" {
					b.Estates[i].ScriptNote = ""
				}
			}
			return b
		}},
		{"a badge nothing recognises", "neither", func(b Board) Board {
			for i := range b.Estates {
				if b.Estates[i].ScriptStale != "" {
					b.Estates[i].ScriptStale = "probably"
				}
			}
			return b
		}},
		{"the shared-library count moved off its rows", "script_banner", func(b Board) Board {
			for i := range b.Estates {
				if b.Estates[i].ScriptStale == ScriptChanged {
					b.Estates[i].ScriptNote = scriptStaleNote(
						ScriptStaleness{State: ScriptChanged, Shared: []string{SharedLibDir + "/gauntlet.sh"}},
						"live/e2e/"+b.Estates[i].Name)
				}
			}
			return b
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := c.of(copyBoard(theirs))
			err := BoardSelfConsistent(b)
			if err == nil {
				t.Fatalf("accepted a board with %s", c.name)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("rejected, but not for %q: %v", c.want, err)
			}
		})
	}

	// The control: a board with no staleness claim anywhere, which is what
	// a render in a checkout with no git history produces, is accepted.
	blank := copyBoard(base)
	blank.ScriptBanner = ""
	for i := range blank.Estates {
		blank.Estates[i].ScriptStale = ""
		blank.Estates[i].ScriptNote = ""
	}
	if err := BoardSelfConsistent(blank); err != nil {
		t.Errorf("a board that says nothing about staleness must be accepted (#1264's shallow-checkout case): %v", err)
	}
}

func copyBoard(b Board) Board {
	c := b
	c.Estates = append([]BoardEstate(nil), b.Estates...)
	return c
}

// TestCommittedBoardIsSelfConsistent holds the real board to the same rule,
// so a bad merge that reaches main is caught by name rather than as a
// generic staleness difference.
func TestCommittedBoardIsSelfConsistent(t *testing.T) {
	root := testRoot(t)
	b, err := os.ReadFile(filepath.Join(root, SiteBoardPath))
	if err != nil {
		t.Fatal(err)
	}
	var bd Board
	if err := json.Unmarshal(b, &bd); err != nil {
		t.Fatalf("%s does not parse as a board: %v", SiteBoardPath, err)
	}
	if err := BoardSelfConsistent(bd); err != nil {
		t.Errorf("%s: %v", SiteBoardPath, err)
	}
}

// TestRenderedMergePathsAreRoutedByGitattributes: every path the merge
// driver claims to handle is actually routed to it, and live/gauntlet.json
// is not - it is the source, and merging it is merge-artifact's row-level
// job, not a keep-ours.
func TestRenderedMergePathsAreRoutedByGitattributes(t *testing.T) {
	root := testRoot(t)
	b, err := os.ReadFile(filepath.Join(root, ".gitattributes"))
	if err != nil {
		t.Fatal(err)
	}
	attrs := string(b)
	for _, p := range RenderedMergePaths {
		if !strings.Contains(attrs, p+" merge=gauntlet-rendered") {
			t.Errorf(".gitattributes does not route %s to the rendered-file merge driver", p)
		}
	}
	if strings.Contains(attrs, ArtifactPath+" merge=") {
		t.Errorf("%s must not get a merge driver: it is the source the rendered files are derived from, and `gauntlet merge-artifact` merges it by row", ArtifactPath)
	}
}

// TestMergeRenderedKeepsOurs: the driver leaves git's copy of our version
// exactly as it found it, and refuses a path it was never meant to handle
// rather than silently keeping ours for some unrelated file.
func TestMergeRenderedKeepsOurs(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ours")
	const content = "{\n  \"schema\": 1\n}\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := cmdMergeRendered([]string{SiteBoardPath, p}, &out); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Errorf("the driver rewrote our version:\n%s", got)
	}
	if !strings.Contains(out.String(), "gauntlet render") {
		t.Errorf("the driver did not say a re-render is still required: %q", out.String())
	}
	if err := cmdMergeRendered([]string{ArtifactPath, p}, &out); err == nil {
		t.Errorf("the driver accepted %s, which it must never keep-ours", ArtifactPath)
	}
	if err := cmdMergeRendered([]string{SiteBoardPath, filepath.Join(dir, "absent")}, &out); err == nil {
		t.Error("the driver accepted a missing ours-file")
	}
}

func firstDiffLine(a, b []byte) string {
	al, bl := strings.Split(string(a), "\n"), strings.Split(string(b), "\n")
	for i := range al {
		if i >= len(bl) {
			return al[i]
		}
		if al[i] != bl[i] {
			return al[i] + "\n" + bl[i]
		}
	}
	return ""
}
