// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// A row's verdicts are evidence about ONE version of its crossing script.
// Change the script afterwards and the row still reads as a measurement of
// the script that is in the tree now, because nothing in the artifact
// compares the two (#1264).
//
// The case that produced this: reference-k8s-stateful's row read
// `day2_remove: fail` at commit 56e04ebca, #1179 fixed exactly that stage
// in live/e2e/reference-k8s-stateful/run.sh the next day, and for a day the
// board showed a red cell for a stage that passes. It was found by accident,
// when an unrelated unit re-measured the estate.
//
// That instance pointed the safe way - the board under-claimed. Nothing
// about the mechanism prefers that direction: a script edit that BREAKS a
// stage leaves a green cell standing just as quietly, and that is the one
// this exists to expose. So every branch below that cannot answer says
// "cannot tell" (ScriptUnknown) and never "current": the artifact must
// never upgrade an absence of evidence into a claim, the same rule
// StageCarried's three states hold (artifact.go).
//
// This is per-ROW staleness, a different fact from #1069's per-STAGE
// carried verdict, which is about one run aborting before it reached a
// stage. The two use the same word in their rendered sentences on purpose
// ("**Stale**") and are kept in separate fields so a reader is never left
// guessing which one a cell means: #1069 marks cells and writes
// BoardEstate.StaleNote, this writes BoardEstate.ScriptNote.
const (
	// ScriptCurrent: nothing under the estate's own directory has changed
	// since the run this row records, apart from inert paths.
	ScriptCurrent = "current"
	// ScriptChanged: at least one file the script could read or execute
	// differs from the version the recorded run measured.
	ScriptChanged = "changed"
	// ScriptUnknown: the comparison could not be made. Never treated as
	// either of the other two - see the package note above.
	ScriptUnknown = "unknown"
)

// ScriptStaleness is one row's answer.
type ScriptStaleness struct {
	// State is ScriptCurrent, ScriptChanged or ScriptUnknown.
	State string
	// Changed is every material path under the estate's directory that
	// differs from the recorded run's version, repo-relative and sorted.
	Changed []string
	// Inert is the paths that differ but cannot change what the run
	// measures (isInertEstatePath). Non-empty with State ScriptCurrent is
	// the README-only case, and it is rendered rather than hidden so the
	// inert rule is auditable from the page it affects.
	Inert []string
	// Why is set only for ScriptUnknown: what stopped the comparison.
	Why string
}

// isInertEstatePath reports whether a path under an estate's directory
// cannot change what a run of that estate measures.
//
// Markdown only. An estate directory holds its crossing script, the
// terraform it deploys, and its documentation; the first two are read by
// the run and the third is read by people. A commit that touches only the
// README is the obvious false positive (it is why
// reference-k8s-cert-manager's row would otherwise read stale today), and
// calling it a change would train a reader to ignore the marker.
//
// The rule is held to the tree by TestEstateScriptsReadNoMarkdown: if a
// crossing script ever does read a markdown file out of its own directory,
// that guard fails rather than this quietly becoming wrong.
func isInertEstatePath(p string) bool { return strings.HasSuffix(p, ".md") }

// estateMarkdownReads is isInertEstatePath's evidence: every place a file
// under one of dirs names a markdown file that lives in that same
// directory, outside a comment. A hit means the inert rule is wrong for
// that estate - a run could be reading the document this treats as
// unreadable - and TestEstateScriptsReadNoMarkdown fails rather than
// letting the classification rot.
//
// Full-line comments (`#`, `//`) are skipped, because a script pointing at
// its own MIGRATION.md in prose is not reading it; live/e2e/terralith-scale
// does exactly that today. An inline comment is not parsed out, so a
// reference tucked onto the end of a code line reads as a hit: the fix
// there is to move it to its own line, and the guard's message says so.
func estateMarkdownReads(root string, dirs []string) ([]string, error) {
	var hits []string
	for _, dir := range dirs {
		base := filepath.Join(root, filepath.FromSlash(dir))
		var files, mds []string
		err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if p != base && strings.HasPrefix(d.Name(), ".") {
					return fs.SkipDir
				}
				return nil
			}
			if isInertEstatePath(d.Name()) {
				mds = append(mds, d.Name())
				return nil
			}
			files = append(files, p)
			return nil
		})
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if len(mds) == 0 {
			continue
		}
		for _, f := range files {
			b, err := os.ReadFile(f) //nolint:gosec // paths walked out of the checkout's own estate directories
			if err != nil {
				return nil, err
			}
			for i, line := range strings.Split(string(b), "\n") {
				t := strings.TrimSpace(line)
				if strings.HasPrefix(t, "#") || strings.HasPrefix(t, "//") {
					continue
				}
				for _, md := range mds {
					if strings.Contains(line, md) {
						rel, _ := filepath.Rel(root, f)
						hits = append(hits, fmt.Sprintf("%s:%d names %s", filepath.ToSlash(rel), i+1, md))
					}
				}
			}
		}
	}
	sort.Strings(hits)
	return hits, nil
}

// EstateDir is the directory a row's evidence is about: the one holding its
// crossing script. Empty when the row names no script, or names one with no
// directory at all, which is a row this comparison cannot be made for.
func EstateDir(r EstateResult) string {
	if !strings.Contains(r.Script, "/") {
		return ""
	}
	return path.Dir(r.Script)
}

// pathDiff answers "which paths under dir differ between commit and the
// working tree". An error means the question could not be answered; its
// text becomes ScriptStaleness.Why, so it is written for a reader of the
// board, not for a stack trace.
type pathDiff func(commit, dir string) ([]string, error)

// scriptStaleness is the whole rule, with git injected so the three states
// are testable without a fixture repository (the git half has its own test
// against a real one - a fixture's commits have to actually exist).
func scriptStaleness(r EstateResult, diff pathDiff) ScriptStaleness {
	dir := EstateDir(r)
	switch {
	case dir == "":
		return ScriptStaleness{State: ScriptUnknown, Why: "this row names no crossing script, so there is no directory to compare"}
	case r.LastRun == nil:
		return ScriptStaleness{State: ScriptUnknown, Why: "this row has recorded no run, so there is nothing to compare the script against"}
	case r.LastRun.Commit == "":
		return ScriptStaleness{State: ScriptUnknown, Why: "this row's run recorded no commit, so the version it measured cannot be named"}
	}
	paths, err := diff(r.LastRun.Commit, dir)
	if err != nil {
		return ScriptStaleness{State: ScriptUnknown, Why: err.Error()}
	}
	out := ScriptStaleness{State: ScriptCurrent}
	for _, p := range paths {
		if p == "" {
			continue
		}
		if isInertEstatePath(p) {
			out.Inert = append(out.Inert, p)
			continue
		}
		out.Changed = append(out.Changed, p)
	}
	sort.Strings(out.Changed)
	sort.Strings(out.Inert)
	if len(out.Changed) > 0 {
		out.State = ScriptChanged
	}
	return out
}

// gitPathDiff compares a commit against the WORKING TREE, not against HEAD.
//
// That is the difference that makes this renderable. A board field computed
// against HEAD cannot be produced in the commit that causes it: an author
// editing a crossing script would render "current" (git cannot see the
// uncommitted edit), commit, and only a second render after the commit
// could say "changed". Against the working tree the render sees the edit
// the moment it is on disk, so "render, then commit" - the order every
// worker brief already gives - produces a board that still matches after
// the commit, after a rebase, and after the merge.
//
// It is a net content comparison (`git diff`), not a commit count
// (`git log`, which #1264 suggests): a file changed and changed back is not
// a change, a merge commit is not counted twice, and the answer does not
// depend on how the branch that made the change was shaped.
//
// Against the working tree, a run made on a dirty tree reads as changed
// straight away: the row records `git rev-parse HEAD` as its commit, and
// that commit does not contain the edited script the run actually
// exercised. That is not a false positive - the row's own provenance is
// what is wrong, the same class TestEveryLastRunCommitIsAnAncestorOfHEAD
// (#511) already refuses in its other direction, and both reach the answer
// through the same isAncestor primitive. Commit the script change, then run
// the estate; that is also the only order in which last_run.commit is true.
//
// Untracked files are invisible to git diff, so a brand new .tf file that
// has never been `git add`ed does not register until it is staged. That
// direction is safe here in the sense that it is not silently wrong for
// long - CI compares a committed tree - but it is the one gap in the
// comparison and it is stated rather than hidden.
func gitPathDiff(root string) pathDiff {
	return func(commit, dir string) ([]string, error) {
		// One call decides both "is this commit here at all" and "is it in
		// this branch's history": isAncestor reports (false, nil) for a
		// commit git can read but that is not an ancestor, and an error for
		// one it cannot read (a shallow clone, a rebased-away commit) or
		// when git itself will not run.
		ok, err := isAncestor(root, commit, "HEAD")
		if err != nil {
			return nil, fmt.Errorf("git could not place commit `%s` in this checkout's history - a shallow clone, a commit rebased away, or git itself unavailable (%v)", short(commit), err)
		}
		if !ok {
			// Comparing against a commit outside HEAD's history would
			// answer from whatever happens to be in the local object
			// database, so two checkouts of the same branch could disagree.
			// Unknown is the honest answer, and it is also the stable one.
			return nil, fmt.Errorf("commit `%s` is not an ancestor of HEAD, so what that run measured is not a version of this branch (rebased away, or a run recorded on a branch that never landed)", short(commit))
		}
		out, err := gitOutput(root, "diff", "--name-only", commit, "--", dir+"/")
		if err != nil {
			return nil, fmt.Errorf("git could not diff `%s` against commit `%s` (%v)", dir, short(commit), err)
		}
		if strings.TrimSpace(out) == "" {
			return nil, nil
		}
		return strings.Split(out, "\n"), nil
	}
}

// AllScriptStaleness answers for every row in a, keyed by estate name.
// Reads git in root, which must be the real checkout - never a render's
// temp write directory (see Render's tt and scale parameters for the same
// rule).
func AllScriptStaleness(root string, a *Artifact) map[string]ScriptStaleness {
	diff := gitPathDiff(root)
	out := make(map[string]ScriptStaleness, len(a.Estates))
	for _, r := range a.Estates {
		out[r.Name] = scriptStaleness(r, diff)
	}
	return out
}

// printScriptStaleness writes `gauntlet check`'s live answer: which rows
// were measured before their own estate directory last changed, and which
// cannot be told. It is computed on every invocation, never read from a
// rendered file, so it is the surface that cannot lag behind the tree.
//
// It never decides an exit code. #1264's ruling is that this is visible,
// not blocking: failing on it would turn every estate-script pull request
// into an estate-run pull request, and some of those runs are half an hour.
func printScriptStaleness(root string, w io.Writer) error {
	a, err := LoadArtifact(root)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, formatScriptStaleness(a, AllScriptStaleness(root, a)))
	return err
}

// formatScriptStaleness is printScriptStaleness's text, over data.
func formatScriptStaleness(a *Artifact, st map[string]ScriptStaleness) string {
	type line struct{ name, state, detail string }
	var lines []line
	changed, total := 0, 0
	for _, r := range a.Estates {
		s, ok := st[r.Name]
		if !ok {
			continue
		}
		total++
		switch s.State {
		case ScriptChanged:
			changed++
			lines = append(lines, line{r.Name, ScriptChanged, strings.Join(s.Changed, ", ")})
		case ScriptUnknown:
			lines = append(lines, line{r.Name, ScriptUnknown, s.Why})
		}
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].name < lines[j].name })
	var b strings.Builder
	if changed == 0 {
		fmt.Fprintf(&b, "script staleness (#1264): every one of %d rows was measured against the estate files in the tree now\n", total)
	} else {
		fmt.Fprintf(&b, "script staleness (#1264): %d of %d rows were measured before their own estate directory last changed; re-run those estates to re-measure them\n", changed, total)
	}
	width := 0
	for _, l := range lines {
		if len(l.name) > width {
			width = len(l.name)
		}
	}
	for _, l := range lines {
		fmt.Fprintf(&b, "  %-*s  %-7s  %s\n", width, l.name, l.state, l.detail)
	}
	return b.String()
}

// scriptStaleNote is the estate page's sentence about its own directory.
// Empty for a row whose script is current and whose inert paths have not
// moved either - the quiet case stays quiet, exactly as oracleNote and the
// #1069 notes do.
//
// A zero ScriptStaleness (the nil-map case: a caller that had no checkout
// to read, e.g. a test building a board directly) renders nothing at all
// rather than claiming either answer.
func scriptStaleNote(s ScriptStaleness, dir string) string {
	switch s.State {
	case ScriptChanged:
		return fmt.Sprintf("**Stale**: this estate's own files have changed since the run recorded above - %s. Every verdict in the table above was measured against the earlier version, so a stage those edits fixed still reads `fail` here, and a stage they broke still reads `pass`; only a re-run of this estate settles it (#1264).", changedClause(s.Changed))
	case ScriptUnknown:
		return fmt.Sprintf("**Unverified**: whether `%s` has changed since the run recorded above cannot be told from this checkout - %s. The verdicts above may describe an earlier version of the script (#1264).", dir, s.Why)
	default:
		if len(s.Inert) > 0 {
			return fmt.Sprintf("Only documentation under this estate's directory has changed since the run recorded above (%s); nothing the script reads or runs (#1264).", codeList(s.Inert))
		}
		return ""
	}
}

// changedClause names what differs: the paths themselves while there are
// few enough to read, a count and the first few after that.
func changedClause(changed []string) string {
	switch {
	case len(changed) == 0:
		return "nothing"
	case len(changed) == 1:
		return fmt.Sprintf("`%s` differs from the version that run measured", changed[0])
	case len(changed) <= 4:
		return fmt.Sprintf("%d files differ from the versions that run measured (%s)", len(changed), codeList(changed))
	default:
		return fmt.Sprintf("%d files differ from the versions that run measured (%s, and %d more)", len(changed), codeList(changed[:3]), len(changed)-3)
	}
}

// codeList renders paths as a comma-separated list of backticked paths.
func codeList(paths []string) string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, "`"+p+"`")
	}
	return strings.Join(out, ", ")
}

// scriptStaleBanner is the board-wide sentence: how many rows below were
// measured before their own estate directory last changed. Computed fresh
// on every render from the map the caller passes, never stored, the same
// rule boardBanner holds to (#414).
//
// Empty when the caller had no checkout to read (a nil map), so a board
// built without git says nothing rather than saying everything is fine.
func scriptStaleBanner(a *Artifact, st map[string]ScriptStaleness) string {
	if len(st) == 0 {
		return ""
	}
	var changed, unknown []string
	total := 0
	for _, r := range a.Estates {
		s, ok := st[r.Name]
		if !ok {
			continue
		}
		total++
		switch s.State {
		case ScriptChanged:
			changed = append(changed, r.Name)
		case ScriptUnknown:
			unknown = append(unknown, r.Name)
		}
	}
	if total == 0 {
		return ""
	}
	sort.Strings(changed)
	sort.Strings(unknown)
	unknownClause := ""
	if len(unknown) > 0 {
		unknownClause = fmt.Sprintf(" %d more cannot be checked from this checkout: %s.", len(unknown), strings.Join(unknown, ", "))
	}
	if len(changed) == 0 {
		return fmt.Sprintf("Every row below was measured against the estate files that are in the tree now.%s", unknownClause)
	}
	return fmt.Sprintf("**%d of %d rows below were measured before their own estate directory last changed.** Their verdicts describe an earlier version of the crossing script: a stage one of those edits broke still reads `pass` here until the estate is re-run, and a stage one of them fixed still reads `fail` (#1264). The rows are %s.%s", len(changed), total, strings.Join(changed, ", "), unknownClause)
}
