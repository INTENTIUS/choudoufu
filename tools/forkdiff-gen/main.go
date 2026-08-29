// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// forkdiff-gen generates live/fork-surface.json, issue #423's measurement of
// the claim every release repeats and nothing had measured: "everything
// outside live resource markers is stock OpenTofu."
//
// It diffs HEAD against the fork point - commit 03743ce6e8, "RFC: Speed up
// tofu show <planfile> by embedding schemas into the planfile (#4239)", the
// commit this checkout diverged from github.com/opentofu/opentofu at - and
// groups every changed path (added, modified, deleted) by top-level root:
// internal/live/, tools/, live/, site/, .github/, rfc/, and a catch-all
// "other" for anything not under one of those. live/forkdiff_test.go (issue
// #423's guard) holds the other bucket to an allowlist: empty, or every
// entry accounted for with a one-line reason.
//
// Usage, from anywhere in the checkout:
//
//	go run ./tools/forkdiff-gen
//
// It shells out to git and needs the fork point reachable in the local
// object database. In this checkout that is usually already true - the
// `upstream` remote points at opentofu/opentofu precisely so it can be - but
// a shallow or fresh clone may need one read-only fetch first:
//
//	git fetch upstream
//
// The tool's own error message says so when the commit is missing; it never
// fetches on its own.
//
// # Limits (read this before trusting a "0" in the other bucket)
//
// The fork point is the fixed commit above, named by hash, not re-derived
// from git ancestry. It cannot be: this checkout's history was purged and
// re-rooted on 2026-08-14 (HANDOFF.md's "choudoufu history rewrite" note),
// and 03743ce6e8 is consequently not an ancestor of HEAD by git's own
// reckoning - `git merge-base --is-ancestor` says no. What this tool runs is
// a content diff between two fixed commits (`git diff 03743ce6e8 HEAD`),
// which is exactly as valid a comparison as it was the day the fork was cut,
// and is unaffected by how the history in between is shaped. But it also
// means the fork point never advances by itself. If choudoufu ever
// backports an upstream commit into internal/ outside internal/live/, that
// backport is real fork-owned history from this artifact's point of view -
// it lands in the other bucket (there is no named root for stock-owned
// internal/ packages) exactly as a hand-written stock edit would, and
// live/forkdiff_test.go's allowlist is where it gets named and justified.
// The fork point moving to a newer upstream commit is a deliberate,
// separate act this tool does not perform.
//
// The one exception the other bucket does not surface file-by-file is the
// module-path rename: every internal/**/*.go file whose only difference
// from the fork point, line for line and order-independently, is the
// quoted Go import path changing from "github.com/opentofu/opentofu" to
// "github.com/intentius/choudoufu" is excluded from the changed-file count
// entirely (its logic is byte-for-byte stock's; only the import spelling
// moved) rather than listed and allowlisted one by one - there are over a
// thousand of them, which is a fact about forking a Go module, not a fact
// about this fork's surface. mechanicalModuleRename in the artifact records
// how many were excluded this way. The substitution is deliberately narrow
// - a quoted import line under internal/, nothing else - because the same
// text substitution is not safe to apply blindly: a shell script's `exec`
// line and a doc comment citing an upstream issue by URL both contain the
// same string and must not be rewritten, and neither is treated as
// mechanical here. Every path outside internal/ is reported file for file
// with no filtering at all.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

const (
	// forkSurfaceJSONRel is where the generated artifact is committed,
	// relative to the repository root.
	forkSurfaceJSONRel = "live/fork-surface.json"

	// forkPointCommit is the fixed commit this fork diverged from
	// opentofu/opentofu at. See the package doc for why it is a literal
	// hash rather than something derived from ancestry.
	forkPointCommit = "03743ce6e8"

	// modulePathOld and modulePathNew are the two ends of the module-path
	// rename the mechanical-change filter recognizes, quoted-import-only
	// (see importRenameQuoted below).
	modulePathOld = "github.com/opentofu/opentofu"
	modulePathNew = "github.com/intentius/choudoufu"

	otherBucket = "other"
)

// namedRoots is the fixed bucket list issue #423 asks for, in the order
// they render in the artifact's ordered summary line. Anything not under
// one of these prefixes falls into otherBucket, which
// live/forkdiff_test.go's guard holds to an allowlist.
var namedRoots = []string{
	"internal/live/",
	"tools/",
	"live/",
	"site/",
	".github/",
	"rfc/",
}

// importRenameQuoted matches a quoted Go import path naming the pre-fork
// module, so the mechanical-change filter only ever rewrites an import
// line - never a shell script's `exec` line or a doc comment's upstream
// issue URL, both of which contain the same bare string with no leading
// quote.
var importRenameQuoted = regexp.MustCompile(`"` + regexp.QuoteMeta(modulePathOld))

// repoRoot resolves the checkout's root from this file's own location, the
// same trick survey-gen's repoRoot uses, so the tool runs from any
// directory.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve the repository root: runtime.Caller failed")
	}
	// This file lives at tools/forkdiff-gen/main.go.
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "forkdiff-gen: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}

	g := &git{dir: root}

	if err := g.checkForkPointPresent(); err != nil {
		return err
	}

	forkPointSHA, err := g.output("rev-parse", forkPointCommit)
	if err != nil {
		return fmt.Errorf("resolving the fork point: %w", err)
	}
	forkPointSubject, err := g.output("log", "-1", "--format=%s", forkPointCommit)
	if err != nil {
		return fmt.Errorf("reading the fork point's subject line: %w", err)
	}
	head, err := g.output("rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("resolving HEAD: %w", err)
	}

	changes, err := g.diffNameStatus(forkPointCommit, "HEAD")
	if err != nil {
		return err
	}

	surface, err := buildSurface(g, changes, forkPointSHA, forkPointSubject, head)
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(surface, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	out := filepath.Join(root, forkSurfaceJSONRel)
	if err := os.WriteFile(out, data, 0o644); err != nil { //nolint:gosec // a committed artifact, not a secret
		return err
	}

	fmt.Fprintf(os.Stderr, "forkdiff-gen: wrote %s (%s; %d mechanical module-rename file(s) excluded)\n",
		forkSurfaceJSONRel, summarizeCounts(surface.Counts), surface.MechanicalModuleRename.ExcludedCount)
	return nil
}

// summarizeCounts renders the per-root counts in namedRoots order plus
// other, matching the "N files diverge ... grouped as: root (X), ..." shape
// issue #423 asks the PR description to quote.
func summarizeCounts(counts map[string]int) string {
	var parts []string
	total := 0
	for _, r := range namedRoots {
		parts = append(parts, fmt.Sprintf("%s (%d)", r, counts[r]))
		total += counts[r]
	}
	parts = append(parts, fmt.Sprintf("%s (%d)", otherBucket, counts[otherBucket]))
	total += counts[otherBucket]
	return fmt.Sprintf("%d files diverge from the fork point, grouped as: %s", total, strings.Join(parts, ", "))
}

// fileChange is one changed path in the artifact.
type fileChange struct {
	Path   string `json:"path"`
	Status string `json:"status"` // "A", "M" or "D"
}

// mechanicalSummary records how many internal/**/*.go files the module-path
// rename filter excluded from the other bucket, and why - see the package
// doc's Limits section.
type mechanicalSummary struct {
	ExcludedCount int    `json:"excluded_count"`
	Scope         string `json:"scope"`
	Note          string `json:"note"`
}

// forkSurface is live/fork-surface.json's shape.
type forkSurface struct {
	GeneratedBy            string                  `json:"generated_by"`
	ForkPoint              string                  `json:"fork_point"`
	ForkPointShort         string                  `json:"fork_point_short"`
	ForkPointSubject       string                  `json:"fork_point_subject"`
	MeasuredAtHead         string                  `json:"measured_at_head"`
	Counts                 map[string]int          `json:"counts"`
	Files                  map[string][]fileChange `json:"files"`
	MechanicalModuleRename mechanicalSummary       `json:"mechanical_module_rename"`
	Limits                 string                  `json:"limits"`
}

const limitsNote = "The fork point is the fixed commit named in fork_point, not re-derived from git ancestry (this checkout's history was purged and re-rooted 2026-08-14, so the fork point is not an ancestor of HEAD; the comparison is a content diff, not a merge-base walk). A future upstream backport into internal/ outside internal/live/ shows up here as growth in the other bucket - there is no named root for stock-owned internal/ packages - and live/forkdiff_test.go's allowlist is where such an entry gets named and justified; the fork point itself only moves by deliberate, separate action. mechanical_module_rename excludes only the quoted Go-import half of the module-path rename under internal/; every other path, including internal/**/*.go files with any other change on top of the rename, is counted and, if it falls outside the six named roots, must be allowlisted."

// git wraps the plumbing calls this tool needs, all run with the repo root
// as the working directory so the tool works from anywhere in the checkout.
type git struct {
	dir string
}

func (g *git) command(args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Dir = g.dir
	return cmd
}

func (g *git) output(args ...string) (string, error) {
	cmd := g.command(args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// checkForkPointPresent fails with a directive error - not a fetch of its
// own - when the fork point is not in the local object database.
func (g *git) checkForkPointPresent() error {
	if err := g.command("cat-file", "-e", forkPointCommit).Run(); err != nil {
		return fmt.Errorf("fork point %s is not present in this checkout's object database.\n"+
			"The `upstream` remote (see `git remote -v`) points at opentofu/opentofu precisely so it can be fetched read-only:\n"+
			"\tgit fetch upstream\n"+
			"then rerun this tool. It never fetches on its own", forkPointCommit)
	}
	return nil
}

// change is one line of `git diff --name-status`.
type change struct {
	Status string
	Path   string
}

// diffNameStatus runs `git diff --no-renames --name-status a b` and parses
// its output. --no-renames is deliberate: a moved-and-edited file (this
// history has one class of these, cmd/tofu/* becoming cmd/choudoufu/*)
// reads more honestly as a delete plus an add than as a rename whose
// similarity score depends on git's heuristics, and it keeps the status
// vocabulary to the three letters this tool understands.
func (g *git) diffNameStatus(a, b string) ([]change, error) {
	out, err := g.output("diff", "--no-renames", "--name-status", a, b)
	if err != nil {
		return nil, fmt.Errorf("diffing %s..%s: %w", a, b, err)
	}
	var changes []change
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) != 2 {
			return nil, fmt.Errorf("unexpected `git diff --name-status` line: %q", line)
		}
		status := fields[0]
		if status != "A" && status != "M" && status != "D" {
			// --no-renames should make this unreachable; fail loudly
			// rather than mis-bucket a status this tool has never seen.
			return nil, fmt.Errorf("git diff --no-renames produced status %q for %q; forkdiff-gen only understands A/M/D", status, fields[1])
		}
		changes = append(changes, change{Status: status, Path: fields[1]})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, nil
}

// batchShow resolves multiple "<rev>:<path>" blobs in one `git cat-file
// --batch` invocation. Checking ~1300 internal/ files individually for the
// mechanical-rename exemption at one exec.Command per blob measured in the
// tens of seconds; one batched process is what keeps this tool fast enough
// to run before every commit rather than occasionally.
func batchShow(g *git, specs []string) (map[string][]byte, error) {
	cmd := g.command("cat-file", "--batch")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting git cat-file --batch: %w", err)
	}

	writeErrCh := make(chan error, 1)
	go func() {
		defer stdin.Close()
		w := bufio.NewWriter(stdin)
		for _, s := range specs {
			if _, err := io.WriteString(w, s+"\n"); err != nil {
				writeErrCh <- err
				return
			}
		}
		writeErrCh <- w.Flush()
	}()

	reader := bufio.NewReader(stdout)
	result := make(map[string][]byte, len(specs))
	for _, s := range specs {
		header, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("reading git cat-file --batch header for %s: %w", s, err)
		}
		fields := strings.Fields(header)
		if len(fields) == 0 {
			return nil, fmt.Errorf("empty git cat-file --batch header for %s", s)
		}
		if fields[len(fields)-1] == "missing" {
			result[s] = nil
			continue
		}
		if len(fields) != 3 {
			return nil, fmt.Errorf("unexpected git cat-file --batch header for %s: %q", s, header)
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("parsing blob size for %s from %q: %w", s, header, err)
		}
		buf := make([]byte, size)
		if _, err := io.ReadFull(reader, buf); err != nil {
			return nil, fmt.Errorf("reading blob content for %s: %w", s, err)
		}
		if _, err := reader.ReadByte(); err != nil { // the trailing newline git cat-file appends after each blob
			return nil, fmt.Errorf("reading blob trailer for %s: %w", s, err)
		}
		result[s] = buf
	}

	if err := <-writeErrCh; err != nil {
		return nil, fmt.Errorf("writing git cat-file --batch requests: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("git cat-file --batch: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return result, nil
}

// mechanicalModuleRenameOnly reports whether newContent differs from
// oldContent only by the quoted-import half of the module-path rename,
// order-independently: oldContent has the rename applied, then the two are
// compared as multisets of lines rather than byte-for-byte, because the
// same rename commonly triggers a goimports re-sort of the import block
// that reorders unrelated lines without changing any of them.
func mechanicalModuleRenameOnly(oldContent, newContent []byte) bool {
	renamed := importRenameQuoted.ReplaceAll(oldContent, []byte(`"`+modulePathNew))
	return lineMultisetEqual(renamed, newContent)
}

func lineMultisetEqual(a, b []byte) bool {
	la := bytes.Split(a, []byte("\n"))
	lb := bytes.Split(b, []byte("\n"))
	if len(la) != len(lb) {
		return false
	}
	counts := make(map[string]int, len(la))
	for _, l := range la {
		counts[string(l)]++
	}
	for _, l := range lb {
		counts[string(l)]--
	}
	for _, c := range counts {
		if c != 0 {
			return false
		}
	}
	return true
}

// buildSurface buckets every change by top-level root, applies the
// mechanical module-rename exemption to candidates in the other bucket, and
// assembles the artifact. Split out of run so the bucketing and mechanical
// checks it calls (bucketOf, mechanicalModuleRenameOnly) stay unit-testable
// on their own, in main_test.go, with no repository needed; buildSurface
// itself still needs one, for the git cat-file --batch call the mechanical
// check makes, and is exercised end to end by live/forkdiff_test.go's guard
// against the committed artifact.
func buildSurface(g *git, changes []change, forkPointSHA, forkPointSubject, head string) (*forkSurface, error) {
	buckets := make(map[string][]fileChange, len(namedRoots)+1)
	for _, r := range namedRoots {
		buckets[r] = []fileChange{}
	}
	buckets[otherBucket] = []fileChange{}

	// Only a modified internal/**/*.go file that would otherwise land in
	// the other bucket is a mechanical-rename candidate: an added or
	// deleted file has no "before" to diff against the rename, and a file
	// under a named root never needs the exemption to begin with.
	var mechCandidates []string
	for _, c := range changes {
		if c.Status == "M" && bucketOf(c.Path) == otherBucket &&
			strings.HasPrefix(c.Path, "internal/") && strings.HasSuffix(c.Path, ".go") {
			mechCandidates = append(mechCandidates, c.Path)
		}
	}

	mechanical := make(map[string]bool, len(mechCandidates))
	if len(mechCandidates) > 0 {
		specs := make([]string, 0, len(mechCandidates)*2)
		for _, p := range mechCandidates {
			specs = append(specs, forkPointCommit+":"+p, "HEAD:"+p)
		}
		blobs, err := batchShow(g, specs)
		if err != nil {
			return nil, fmt.Errorf("checking the module-rename exemption: %w", err)
		}
		for _, p := range mechCandidates {
			oldC, newC := blobs[forkPointCommit+":"+p], blobs["HEAD:"+p]
			if oldC == nil || newC == nil {
				// An M-status path should exist on both sides; if it
				// doesn't, treat it as a real change rather than silently
				// skipping it.
				continue
			}
			if mechanicalModuleRenameOnly(oldC, newC) {
				mechanical[p] = true
			}
		}
	}

	excluded := 0
	for _, c := range changes {
		if mechanical[c.Path] {
			excluded++
			continue
		}
		b := bucketOf(c.Path)
		buckets[b] = append(buckets[b], fileChange{Path: c.Path, Status: c.Status})
	}

	counts := make(map[string]int, len(buckets))
	for k, v := range buckets {
		counts[k] = len(v)
	}

	return &forkSurface{
		GeneratedBy:      "tools/forkdiff-gen (go run ./tools/forkdiff-gen)",
		ForkPoint:        forkPointSHA,
		ForkPointShort:   forkPointCommit,
		ForkPointSubject: forkPointSubject,
		MeasuredAtHead:   head,
		Counts:           counts,
		Files:            buckets,
		MechanicalModuleRename: mechanicalSummary{
			ExcludedCount: excluded,
			Scope:         "internal/**/*.go files (M-status) that would otherwise land in the other bucket",
			Note:          "excluded because every changed line, order-independently, is the quoted Go import path moving from " + modulePathOld + " to " + modulePathNew + " - see the package doc's Limits section",
		},
		Limits: limitsNote,
	}, nil
}

// bucketOf assigns path to the first namedRoots prefix it matches, or
// otherBucket.
func bucketOf(path string) string {
	for _, r := range namedRoots {
		if strings.HasPrefix(path, r) {
			return r
		}
	}
	return otherBucket
}
