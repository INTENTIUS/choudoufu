// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/live/check"
)

// This file answers one question the probe could not previously answer about
// its own output: what tree was this measured against, and was the thing it
// measured the corpus the manifest describes?
//
// The failure it exists for is not hypothetical. A fresh worktree has no
// .corpus - it is gitignored, and agents symlink one in rather than refetch
// 250 repositories - and a sweep there reported "entries 31" with exit 0 and
// nothing else said. 31 in-repo fixtures where the manifest describes 250
// configurations, and the summary line looks exactly like a real sweep. Any
// number taken from it is about a corpus 12% the size of the one every other
// figure in this project is quoted against.
//
// The manifest holds globs rather than entries, so "how many should there be"
// is a derivation and not a constant, and hard-coding 250 would only move the
// staleness somewhere else. What IS derivable, per source, is whether the
// glob expanded to anything at all. A source that matches nothing is a source
// whose configurations are not on this disk, whatever the total happens to
// be.

// corpusSource is one manifest source and what it expanded to on this disk.
//
// Recorded in the sweep output because it is the run's own account of what it
// measured: two sweeps whose per-source counts differ measured different
// corpora, however similar their totals look.
type corpusSource struct {
	Glob   string `json:"glob"`
	Origin string `json:"origin"`

	// Matched is how many directories holding configuration this source's
	// glob expands to. Entries is how many of those survived
	// [check.Manifest.Resolve]'s dedupe and were therefore measured; the
	// two differ only when an earlier source in the manifest already
	// claimed a directory.
	//
	// Both are kept because they answer different questions. A source with
	// Matched 0 is not on disk, which is the failure this file exists for.
	// A source with Matched above 0 and Entries 0 is shadowed by an earlier
	// source, which is a manifest property and perfectly fine.
	Matched int `json:"matched"`
	Entries int `json:"entries"`

	// Dir, Pin and Checkout describe a fetched source: where tools/corpus-fetch
	// materializes it, the commit the manifest pins it to, and the commit the
	// working copy on this disk is actually at. Checkout is empty when the
	// directory is absent or is not a git checkout, which is unknown rather
	// than wrong.
	Dir      string `json:"dir,omitempty"`
	Pin      string `json:"pin,omitempty"`
	Checkout string `json:"checkout,omitempty"`
}

// corpusState expands every manifest source against root and reports what it
// found, plus the problems that make a sweep of it something other than a
// sweep of the corpus the manifest describes.
//
// Problems are returned as stable strings rather than a structured type
// because they are written into the sweep output and compared between two
// sweeps: a diff whose two sides disagree about which sources were present is
// not a comparison, and comparing the sorted strings is enough to say so.
func corpusState(root string, m check.Manifest) (sources []corpusSource, problems []string) {
	seen := map[string]bool{}

	for _, source := range m.Sources {
		row := corpusSource{Glob: source.Glob, Origin: source.Origin}

		pattern := source.Glob
		if !filepath.IsAbs(pattern) {
			pattern = filepath.Join(root, pattern)
		}
		matches, err := filepath.Glob(pattern)
		if err != nil {
			problems = append(problems, fmt.Sprintf("bad glob: %s (%v)", source.Glob, err))
			sources = append(sources, row)
			continue
		}
		sort.Strings(matches)

		// Deliberately the same three conditions [check.Manifest.Resolve]
		// applies, in the same order, so Entries sums to the number of
		// entries the sweep actually measured. sweep asserts that sum.
		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil || !info.IsDir() {
				continue
			}
			if !check.HasConfigFiles(match) {
				continue
			}
			row.Matched++
			if seen[match] {
				continue
			}
			seen[match] = true
			row.Entries++
		}

		if f := source.Fetch; f != nil {
			row.Dir = f.Dir
			row.Pin = f.Commit
			row.Checkout = gitHead(filepath.Join(root, f.Dir))
		}

		if row.Matched == 0 {
			problems = append(problems, fmt.Sprintf("no configurations: %s (%s)", source.Glob, source.Origin))
		}
		if row.Checkout != "" && row.Pin != "" && row.Checkout != row.Pin {
			problems = append(problems, fmt.Sprintf("checkout drift: %s is at %s, the manifest pins %s",
				strings.TrimSuffix(row.Dir, "/"), short(row.Checkout), short(row.Pin)))
		}

		sources = append(sources, row)
	}

	sort.Strings(problems)
	return sources, problems
}

func short(commit string) string {
	if len(commit) > 10 {
		return commit[:10]
	}
	return commit
}

// corpusProblemRefusal is what a sweep prints instead of a number when the
// corpus on disk is not the corpus the manifest describes.
//
// It names what was found, what was expected, and the two ways out, because
// the shape that produced this rule was an agent reading "entries 31" and
// having no reason at all to doubt it.
func corpusProblemRefusal(root string, sources []corpusSource, problems []string) error {
	var total, present int
	for _, s := range sources {
		total++
		if s.Matched > 0 {
			present++
		}
	}
	var entries int
	for _, s := range sources {
		entries += s.Entries
	}

	var b strings.Builder
	fmt.Fprintf(&b, "the corpus under %s is not the corpus %s describes.\n", root, "live/corpus-manifest.json")
	fmt.Fprintf(&b, "found %d of the manifest's %d sources, expanding to %d configuration(s).\n", present, total, entries)
	b.WriteString("\n")
	for _, p := range problems {
		fmt.Fprintf(&b, "  %s\n", p)
	}
	b.WriteString("\nA sweep of a partial corpus prints a summary line indistinguishable from a full one,\n")
	b.WriteString("and every figure in this project is quoted against the full one. Fix it with either:\n")
	b.WriteString("\n  go run ./tools/corpus-fetch          # materialize .corpus/ at the pinned commits\n")
	b.WriteString("  ln -s <other-checkout>/.corpus .corpus   # or borrow a tree that already has it\n")
	b.WriteString("\nTo measure the partial corpus anyway, pass -allow-partial-corpus. The sweep then\n")
	b.WriteString("records what was missing, and -diff refuses to compare it against a full sweep.")
	return fmt.Errorf("%s", b.String())
}

// gitHead reports the commit a checkout is at, or "" when dir is absent or is
// not a git checkout. Unknown, not wrong: corpus-fetch is free to stop
// leaving a .git behind, and that should not become a refusal.
func gitHead(dir string) string {
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return ""
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// treeCommit describes the tree a sweep ran against, for the output's own
// record. Dirty is marked because a sweep of uncommitted work is not a sweep
// of the commit it names, and that distinction has been lost in a report
// here before.
func treeCommit(root string) string {
	head := gitHead(root)
	if head == "" {
		return ""
	}
	// --untracked-files=no on purpose: .corpus is untracked and enormous,
	// and its presence is already recorded per source above.
	out, err := exec.Command("git", "-C", root, "status", "--porcelain", "--untracked-files=no").Output()
	if err == nil && len(strings.TrimSpace(string(out))) > 0 {
		return head + "+dirty"
	}
	return head
}

// realPath is root with symlinks resolved.
//
// The resolution is the point, not a nicety. /Users/alex/checkouts is a
// symlink to /Users/alex/Documents/checkouts on the machine this project is
// developed on - the same trap that makes `env -u PWD` mandatory for the
// tests - so two sweeps of the identical tree can record two different
// spellings of its root. A guard comparing the unresolved strings would
// refuse those, which is a guard that refuses working configurations.
func realPath(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		return root
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

// fileDigest is the sha256 of a file's bytes, or "" if it cannot be read.
//
// Used on the manifest: its PATH is already compared between two sweeps, but
// two trees can hold different manifests at the same path, and the manifest
// is what decides which directories are measured and which tfvars files each
// one is measured with.
func fileDigest(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// modulesInstalled reports whether an entry has had its module calls
// installed.
//
// This is the largest unrecorded input a sweep had. tools/corpus-fetch
// installs modules now, and check.Load reads an installed module's
// configuration where it can only count an uninstalled one as unresolved -
// one corpus entry went from 59 refused sites to 394 on nothing but this. Two
// trees whose .corpus differs only in whether the install ran produce site
// totals that cannot be subtracted, and neither summary line would say so.
func modulesInstalled(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".terraform", "modules", "modules.json"))
	return err == nil
}
