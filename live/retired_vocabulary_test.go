// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// retiredWord is the fork's original name for live mode, retired from the
// whole tree by GitHub issue #1374 (maintainer, 2026-09-19: "case
// insensitive, that word should appear nowhere in the source, docs, or
// anywhere in this repo"). It is assembled from two fragments so that this
// file, which has to name it, does not contain it and needs no exemption
// for itself.
var retiredWord = "state" + "less"

// retiredWordAllowed is every tracked file still allowed to contain the
// word, with the number of lines that contain it. Nothing else in the tree
// may. Each entry says why it cannot be reworded here.
//
// exact entries must match their count exactly, so a new line in an
// allowed file fails too. atMost entries may only shrink, because their
// producers rewrite them: the gauntlet artifact's free-text notes and two
// stage details carry the word until the rows are re-run or the notes get
// a writer (#1374 questions 0.1 and 0.2), and every release snapshot under
// live/history/ is a byte copy of live/gauntlet.json at that release.
var retiredWordAllowed = struct {
	exact        map[string]int
	atMost       map[string]int
	atMostPrefix map[string]int
}{
	exact: map[string]int{
		// AWS-owned names: the Network Firewall rule-group and policy
		// arguments that start with the word, the CloudFormation enum
		// member, and the AWS provider docs page whose ECS example is
		// named with the word. The tree cannot rename what AWS named.
		"live/import-grammar.json":                                     12,
		"live/registry-schema-facts.json":                              1,
		"tools/estate-gen/overrides_cohort_networking_advanced.go":     7,
		"live/e2e/estates/networking-advanced/README.md":               2,
		"tools/importdocs-gen/testdata/docs/ecs_cluster.html.markdown": 3,
	},
	atMost: map[string]int{
		"live/gauntlet.json":            6,
		"site/data/gauntlet.json":       6,
		"site/data/gauntlet_board.json": 6,
	},
	atMostPrefix: map[string]int{
		// Frozen release snapshots; a new one copies live/gauntlet.json.
		"live/history/": 6,
	},
}

// TestRetiredVocabulary is #1374's guard. It reads the path list from
// `git ls-files` and then each file from the working tree, so it sees
// staged and modified content that a `git grep` sweep run before a commit
// does not (#1172 found a grep-based guard measuring an empty set that
// way). There is no skip path: a failing `git ls-files` fails the test.
func TestRetiredVocabulary(t *testing.T) {
	root := repoRoot(t)

	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}

	var paths []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	// A walk that read nothing reports no violations. There are over
	// 7,000 tracked files; a few hundred means the listing went wrong.
	if len(paths) < 5000 {
		t.Fatalf("git ls-files listed %d paths; expected several thousand, so this guard would pass by not looking", len(paths))
	}

	needle := []byte(retiredWord)
	seen := map[string]bool{}
	var violations []string
	for _, p := range paths {
		if strings.Contains(strings.ToLower(p), retiredWord) {
			violations = append(violations, p+": the path itself contains "+retiredWord)
		}
		data, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			if os.IsNotExist(err) {
				// Deleted in the working tree but not yet staged.
				continue
			}
			t.Fatalf("reading %s: %v", p, err)
		}
		lines := 0
		for _, line := range bytes.Split(data, []byte("\n")) {
			if bytes.Contains(bytes.ToLower(line), needle) {
				lines++
			}
		}
		if msg := retiredWordVerdict(p, lines); msg != "" {
			violations = append(violations, msg)
		}
		if lines > 0 {
			seen[p] = true
		}
	}

	// An exact entry whose file no longer carries the word is stale: the
	// allowance would let the word come back.
	for p := range retiredWordAllowed.exact {
		if !seen[p] {
			violations = append(violations, p+": allowed "+retiredWord+" lines, and has none now; remove its entry from retiredWordAllowed.exact")
		}
	}

	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}

// retiredWordVerdict returns "" when path may carry lines matching lines,
// and a failure message otherwise.
func retiredWordVerdict(path string, lines int) string {
	if want, ok := retiredWordAllowed.exact[path]; ok {
		if lines != want {
			return path + ": " + strconv.Itoa(lines) + " lines contain " + retiredWord + ", allowed exactly " + strconv.Itoa(want) + "; a new one is a regression, a removed one means lower the count"
		}
		return ""
	}
	limit := 0
	if n, ok := retiredWordAllowed.atMost[path]; ok {
		limit = n
	}
	for prefix, n := range retiredWordAllowed.atMostPrefix {
		if strings.HasPrefix(path, prefix) {
			limit = n
		}
	}
	if lines > limit {
		if limit == 0 {
			return path + ": " + strconv.Itoa(lines) + " lines contain " + retiredWord + ", the fork's retired name for live mode (#1374); say live mode, a live run, the live pipeline, or what actually happens"
		}
		return path + ": " + strconv.Itoa(lines) + " lines contain " + retiredWord + ", allowed at most " + strconv.Itoa(limit)
	}
	return ""
}

// TestRetiredWordVerdict is the guard's own red proof, run without the
// tree: a planted line in an ordinary file fails, an exact allowance fails
// in both directions, and an at-most allowance fails only above its bound.
func TestRetiredWordVerdict(t *testing.T) {
	cases := []struct {
		path  string
		lines int
		fail  bool
	}{
		{"internal/command/plan.go", 0, false},
		{"internal/command/plan.go", 1, true},
		{"live/import-grammar.json", 12, false},
		{"live/import-grammar.json", 13, true},
		{"live/import-grammar.json", 11, true},
		{"live/gauntlet.json", 6, false},
		{"live/gauntlet.json", 2, false},
		{"live/gauntlet.json", 7, true},
		{"live/history/v9.9.9.json", 6, false},
		{"live/history/v9.9.9.json", 7, true},
	}
	for _, c := range cases {
		got := retiredWordVerdict(c.path, c.lines) != ""
		if got != c.fail {
			t.Errorf("retiredWordVerdict(%q, %d) failed=%v, want %v", c.path, c.lines, got, c.fail)
		}
	}
}
