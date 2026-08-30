// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// Issue #488: with N estate PRs open, a plain `git merge` re-conflicts the
// other N-1 the moment any one lands, and each pays another full estate run
// (5-15 minutes) to resolve it - quadratic, and the throughput bottleneck.
// The aggregates in live/gauntlet.json (Artifact.Sets) are fully DERIVED -
// Rebuild recomputes every row's clear flag from its own stages, then every
// SetSummary by walking the rows (artifact.go) - so once the ROWS are
// correct, the aggregate is correct by construction. Two independent
// estates' rows are independent evidence: estate A's measurement is not
// invalidated by estate B's row landing. MergeArtifact does the row-level
// three-way merge that makes that fact usable instead of a plain `git merge`
// (which merges TEXT, not evidence - PR #502's clean-but-wrong auto-merge,
// documented on #488, is exactly what this exists to stop repeating), then
// calls the existing Rebuild rather than reimplementing the tallying.
//
// This is deliberately narrower than "git merge, but for JSON": it refuses
// rather than guesses in every case a guess could produce a plausible-but-
// wrong verdict - the same estate touched on both sides (case 2 below), a
// merged row whose evidence can no longer be verified (issue #509's
// ancestry rule), a merged row that would violate
// TestNonzeroExitCodeImpliesAFailingStage's invariant, or - the safety
// precondition every other check assumes - product code having moved
// between a row's measurement and the merge (exactly what PRs #502 and #503
// each hit when tools/gauntlet/run.go's recording path changed underneath
// them). A refusal always means "re-run", never "resolve by hand": see
// live/GAUNTLET.md's merge-artifact guidance.

// mergeArtifactAllowedPath reports whether p is allowed to differ between
// base and a side without forcing a re-run: the artifact itself, its Hugo
// copy, the rendered progress docs, or an estate's own crossing script. Any
// OTHER file differing means product code moved under a row's measurement -
// the precondition every other check in this file assumes holds.
var mergeArtifactEstateScript = regexp.MustCompile(`^live/e2e/[^/]+/run\.sh$`)

func mergeArtifactAllowedPath(p string) bool {
	if p == ArtifactPath || p == SiteDataPath {
		return true
	}
	if strings.HasPrefix(p, "site/content/docs/progress/") {
		return true
	}
	return mergeArtifactEstateScript.MatchString(p)
}

// resolveCommit resolves a git revision (branch, tag, sha, HEAD, ...) to its
// full commit SHA, so every later step (diff, show, merge-base) operates on
// a stable, reportable identifier rather than a moving ref.
func resolveCommit(root, rev string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--verify", rev+"^{commit}")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("merge-artifact: %q does not resolve to a commit: %w", rev, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// changedPaths returns the paths that differ between two commits' trees -
// exactly `git diff --name-only from to`, a plain tree-to-tree comparison
// (not a three-dot/merge-base diff), matching "differs between base and
// either side" literally.
func changedPaths(root, from, to string) ([]string, error) {
	cmd := exec.Command("git", "diff", "--name-only", from, to)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff --name-only %s %s: %w", from, to, err)
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return nil, nil
	}
	return strings.Split(s, "\n"), nil
}

// checkNoProductCodeMoved enforces the safety precondition: this merge is
// only valid when nothing outside mergeArtifactAllowedPath differs between
// base and either side. PRs #502 and #503 both genuinely needed a full
// re-run for exactly this reason - tools/gauntlet/run.go's recording path
// changed underneath them - so this refuses loudly rather than merge rows
// measured against code that has since changed.
func checkNoProductCodeMoved(root, base, ours, theirs string) error {
	for _, side := range []struct{ name, rev string }{{"ours", ours}, {"theirs", theirs}} {
		paths, err := changedPaths(root, base, side.rev)
		if err != nil {
			return err
		}
		var bad []string
		for _, p := range paths {
			if !mergeArtifactAllowedPath(p) {
				bad = append(bad, p)
			}
		}
		if len(bad) > 0 {
			return fmt.Errorf("merge-artifact: refusing - product code changed between base and %s (%s): %s; a re-run is required, not a merge (live/GAUNTLET.md, \"Merging estate rows across PRs\")", side.name, side.rev, strings.Join(bad, ", "))
		}
	}
	return nil
}

// artifactAtRevision loads live/gauntlet.json's content as of a git
// revision. A revision where the file doesn't exist yet reads as an empty
// artifact - the same rule loadArtifactFile uses for a missing working-tree
// file, extended to a missing historical one. rev must already be a
// resolved commit (resolveCommit), so any gitShowFile error here can only
// mean "the path did not exist at that commit", never "unknown revision".
func artifactAtRevision(root, rev string) (*Artifact, error) {
	b, err := gitShowFile(root, rev, ArtifactPath)
	if err != nil {
		return &Artifact{Schema: 1}, nil
	}
	var a Artifact
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, fmt.Errorf("%s:%s: %w", rev, ArtifactPath, err)
	}
	return &a, nil
}

// MergeConflictError is case 2 from issue #488: both sides changed the same
// estate's row. That genuinely needs a human or a re-run - guessing which
// side's measurement is current is how a wrong verdict lands - so
// mergeEstateRows returns this instead of picking one.
type MergeConflictError struct {
	Estate       string
	OursCommit   string
	OursDate     string
	TheirsCommit string
	TheirsDate   string
}

func (e *MergeConflictError) Error() string {
	return fmt.Sprintf(
		"merge-artifact: refusing - estate %q was changed on both sides (ours: last_run.commit=%s last_run.date=%s; theirs: last_run.commit=%s last_run.date=%s); this needs a human or a re-run, not a guess",
		e.Estate, orNone(e.OursCommit), orNone(e.OursDate), orNone(e.TheirsCommit), orNone(e.TheirsDate),
	)
}

func orNone(s string) string {
	if s == "" {
		return "<none>"
	}
	return s
}

// indexRows maps estate name to row for one artifact's Estates.
func indexRows(rows []EstateResult) map[string]EstateResult {
	m := make(map[string]EstateResult, len(rows))
	for _, r := range rows {
		m[r.Name] = r
	}
	return m
}

// rowChanged reports whether a row (present or not, per ok) differs from
// base (present or not, per baseOK). Both absent is unchanged; presence
// differing (added or removed) always counts as changed; both present
// compares the full row by value, maps included.
func rowChanged(baseOK bool, base EstateResult, ok bool, row EstateResult) bool {
	if baseOK != ok {
		return true
	}
	if !baseOK {
		return false
	}
	return !reflect.DeepEqual(base, row)
}

// mergeEstateRows performs the row-granular three-way merge (issue #488,
// case 1 and 2): for each estate, take whichever side changed it relative
// to base; refuse if both did. source records which revision ("base",
// "ours", or "theirs") each surviving row's content actually came from, for
// checkProvenanceAncestry below to check the right commit.
func mergeEstateRows(base, ours, theirs *Artifact) (rows []EstateResult, source map[string]string, err error) {
	baseByName := indexRows(base.Estates)
	oursByName := indexRows(ours.Estates)
	theirsByName := indexRows(theirs.Estates)

	names := map[string]bool{}
	for n := range baseByName {
		names[n] = true
	}
	for n := range oursByName {
		names[n] = true
	}
	for n := range theirsByName {
		names[n] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)

	source = map[string]string{}
	for _, n := range sorted {
		b, bok := baseByName[n]
		o, ook := oursByName[n]
		t, tok := theirsByName[n]

		oursChanged := rowChanged(bok, b, ook, o)
		theirsChanged := rowChanged(bok, b, tok, t)

		switch {
		case oursChanged && theirsChanged:
			return nil, nil, &MergeConflictError{
				Estate:       n,
				OursCommit:   lastRunCommit(ook, o),
				OursDate:     lastRunDate(ook, o),
				TheirsCommit: lastRunCommit(tok, t),
				TheirsDate:   lastRunDate(tok, t),
			}
		case oursChanged:
			if ook {
				rows = append(rows, o)
				source[n] = "ours"
			}
			// !ook: ours removed the row relative to base; drop it.
		case theirsChanged:
			if tok {
				rows = append(rows, t)
				source[n] = "theirs"
			}
		default:
			if bok {
				rows = append(rows, b)
				source[n] = "base"
			}
		}
	}
	return rows, source, nil
}

func lastRunCommit(ok bool, r EstateResult) string {
	if !ok || r.LastRun == nil {
		return ""
	}
	return r.LastRun.Commit
}

func lastRunDate(ok bool, r EstateResult) string {
	if !ok || r.LastRun == nil {
		return ""
	}
	return r.LastRun.Date
}

// checkExitFailShape re-enforces TestNonzeroExitCodeImpliesAFailingStage's
// invariant (gauntlet_test.go) directly against the MERGED rows, not just
// the inputs: a nonzero last_run.exit_code with no stage reading fail is
// never legitimate. The row-level merge above cannot itself produce this
// shape from two individually-valid rows (each side's own row already
// passed the guard when it was recorded), but this checks the actual
// output rather than trusting that reasoning - better to refuse loudly on
// a bug here than emit an artifact the guard would fail on the next run of
// `go test ./tools/gauntlet/`.
func checkExitFailShape(rows []EstateResult) error {
	for _, r := range rows {
		if r.LastRun == nil || r.LastRun.ExitCode == 0 {
			continue
		}
		hasFail := false
		for _, v := range r.Stages {
			if v == VerdictFail {
				hasFail = true
				break
			}
		}
		if !hasFail {
			return fmt.Errorf("merge-artifact: refusing - estate %q: last_run.exit_code=%d but no stage reads %q anywhere in the merged row (TestNonzeroExitCodeImpliesAFailingStage's invariant); the merge would produce an artifact that guard fails", r.Name, r.LastRun.ExitCode, VerdictFail)
		}
	}
	return nil
}

// checkProvenanceAncestry enforces issue #509's rule: a surviving row's
// last_run.commit must be a real ancestor of the revision that actually
// produced it - not a rebased-away or otherwise dangling object, exactly
// #509's corpus-iam-read-only-policy defect.
//
// "the revision that produced it" is source[name] (base/ours/theirs,
// resolved to a commit SHA via commits), not a single fixed HEAD: at the
// moment merge-artifact runs, the eventual merge commit does not exist yet,
// so theirs's commits are not yet reachable from ours (or vice versa) -
// checking against a pre-merge process HEAD would reject the exact case
// this tool exists to handle. Once the real git merge completes, HEAD is
// necessarily a descendant of both ours and theirs, so "ancestor of the row's
// own source" here guarantees "ancestor of the eventual HEAD" too - a
// strictly more precise version of the same rule.
func checkProvenanceAncestry(root string, rows []EstateResult, source map[string]string, commits map[string]string) error {
	for _, r := range rows {
		if r.LastRun == nil || r.LastRun.Commit == "" {
			continue
		}
		target, ok := commits[source[r.Name]]
		if !ok {
			continue
		}
		isAnc, err := isAncestor(root, r.LastRun.Commit, target)
		if err != nil {
			return fmt.Errorf("merge-artifact: estate %q: checking whether last_run.commit %s is an ancestor of %s (%s): %w", r.Name, r.LastRun.Commit, source[r.Name], target, err)
		}
		if !isAnc {
			return fmt.Errorf("merge-artifact: refusing - estate %q: last_run.commit %s is not an ancestor of %s (%s); a dangling or rebased-away provenance pointer (issue #509's class) - a re-run is required, not a merge", r.Name, r.LastRun.Commit, source[r.Name], target)
		}
	}
	return nil
}

// isAncestor reports whether commit is an ancestor of (or equal to) target.
func isAncestor(root, commit, target string) (bool, error) {
	cmd := exec.Command("git", "merge-base", "--is-ancestor", commit, target)
	cmd.Dir = root
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// MergeArtifact is the whole of `gauntlet merge-artifact <base> <ours>
// <theirs>`: verify the safety precondition, three-way-merge the rows,
// refuse on conflict or a guard violation, then Rebuild (never
// reimplemented) against the current working tree's manifest and emulator
// pin. Every revision argument is a git commit-ish (sha, branch, tag,
// HEAD, ...), resolved once up front so every later diagnostic names a
// stable commit.
func MergeArtifact(root, baseRev, oursRev, theirsRev string) (*Artifact, error) {
	base, err := resolveCommit(root, baseRev)
	if err != nil {
		return nil, err
	}
	ours, err := resolveCommit(root, oursRev)
	if err != nil {
		return nil, err
	}
	theirs, err := resolveCommit(root, theirsRev)
	if err != nil {
		return nil, err
	}

	if err := checkNoProductCodeMoved(root, base, ours, theirs); err != nil {
		return nil, err
	}

	baseArt, err := artifactAtRevision(root, base)
	if err != nil {
		return nil, err
	}
	oursArt, err := artifactAtRevision(root, ours)
	if err != nil {
		return nil, err
	}
	theirsArt, err := artifactAtRevision(root, theirs)
	if err != nil {
		return nil, err
	}

	rows, source, err := mergeEstateRows(baseArt, oursArt, theirsArt)
	if err != nil {
		return nil, err
	}

	if err := checkExitFailShape(rows); err != nil {
		return nil, err
	}

	commits := map[string]string{"base": base, "ours": ours, "theirs": theirs}
	if err := checkProvenanceAncestry(root, rows, source, commits); err != nil {
		return nil, err
	}

	m, err := LoadManifest(root)
	if err != nil {
		return nil, err
	}
	bi, err := LoadBehaviorIndex(root)
	if err != nil {
		return nil, err
	}
	merged := &Artifact{Estates: rows}
	merged.Rebuild(m, bi, emulatorPin(root), oracleVersions(root))
	return merged, nil
}
