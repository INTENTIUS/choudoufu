// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// merge-guard detects silent content loss in merge commits (issue #92).
//
// The rule: for a merge M with parents P1/P2 and merge-base MB, a line
// present in a parent P but absent from MB was contributed by that side
// after divergence, so the other side never saw it and cannot have
// deliberately deleted it. If the line is also absent from M, the merge
// dropped it silently: no conflict, no test failure, and no trace in
// `git log -S`, which skips merge diffs by default.
//
// The scan covers every merge reachable from -ref (or the -since..ref
// range), not just the first-parent chain: four of the five known losses
// happened on a branch dropping its own new work while merging origin/main
// in, and by the time such a branch lands the base already contains the
// dropped content, so the deletion looks deliberate from main's side.
//
// Membership is set-based over whole files, never positional, so moved or
// reordered lines do not register. Lines are normalized (whitespace runs
// collapsed) and lines below -min-len characters or without a letter are
// ignored as too generic to attribute. Six filters remove non-losses:
//
//   - generated content: *.json and *.sum paths, files whose header says
//     "Code generated" or "DO NOT EDIT", any directory carrying a
//     GENERATED.md, and regions inside <!-- x-gen:begin/end --> spans;
//   - renames: file mapping between MB, P and M follows git's -M rename
//     detection, so a file moved by the merge is compared at its new path;
//   - moved content: a candidate found anywhere in M's changed files, as a
//     contiguous token run in them (prose re-wrapped to different line
//     boundaries), or by a whitespace-tolerant grep over M's whole tree,
//     survived the merge;
//   - superseded variants: a candidate whose merged file holds a line with
//     the same leading token and most tokens shared was edited in place -
//     both sides' versions were on the table and a successor won;
//   - informed deletions: `git rev-list <other-parent> --not <base>` blobs
//     of the file are probed for the lost lines. If the other side's
//     history contains a line but its tip does not, that side saw the
//     content and deleted it - a decision, not an accident;
//   - echoes: merges are scanned oldest-first and each lost line is
//     reported once, at the merge that first dropped it. A branch that
//     merges origin/main in repeatedly re-drops the same still-lost content
//     at every step; those are the same event, not new losses.
//
// Each finding also lists which of its lines are present again at the
// scanned ref ("repaired"): real losses at the time, restored since.
//
// Usage:
//
//	go run ./tools/merge-guard                 # every merge reachable from HEAD
//	go run ./tools/merge-guard -since v1.0     # merges in v1.0..HEAD only
//	go run ./tools/merge-guard -json           # machine-readable findings
//
// How to run this after a merge: right after `git merge <branch>` (or after
// pulling a merge in), run
//
//	go run ./tools/merge-guard -since HEAD^1
//
// which scans the merge you just created plus any origin/main merges the
// branch made along the way - the direction where losses actually happen.
// The exit status is non-zero when anything was lost, so the command works
// as a post-merge hook; there is no CI wiring, the consumer is a human or
// a future hook.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func main() {
	var (
		repoDir = flag.String("repo", ".", "repository to scan")
		ref     = flag.String("ref", "HEAD", "tip whose reachable merges are scanned")
		since   = flag.String("since", "", "only scan merges in <since>..<ref>")
		minLen  = flag.Int("min-len", 12, "ignore normalized lines shorter than this")
		asJSON  = flag.Bool("json", false, "emit findings and stats as JSON")
		verbose = flag.Bool("v", false, "print per-merge progress to stderr")
	)
	flag.Parse()

	res, err := runScan(options{
		repoDir: *repoDir,
		ref:     *ref,
		since:   *since,
		minLen:  *minLen,
		verbose: *verbose,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "merge-guard: %v\n", err)
		os.Exit(2)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			fmt.Fprintf(os.Stderr, "merge-guard: %v\n", err)
			os.Exit(2)
		}
	} else {
		printText(res)
	}
	if len(res.Findings) > 0 {
		os.Exit(1)
	}
}

func printText(res *result) {
	for _, f := range res.Findings {
		repaired := ""
		if n := len(f.Repaired); n > 0 {
			repaired = fmt.Sprintf(" (%d since repaired)", n)
		}
		fmt.Printf("LOSS %s (%s)\n  parent %s contributed to %s, %d line(s) absent from the merge%s:\n",
			short(f.Merge), f.Subject, short(f.Parent), f.File, len(f.Lines), repaired)
		const show = 8
		for i, l := range f.Lines {
			if i == show {
				fmt.Printf("    ... and %d more\n", len(f.Lines)-show)
				break
			}
			fmt.Printf("    | %s\n", truncate(l, 160))
		}
	}
	s := res.Stats
	fmt.Printf("merges scanned: %d (skipped %d without a merge base)\n", s.MergesScanned, s.MergesSkipped)
	fmt.Printf("candidate lines: %d; after moved-content filters: %d; superseded variants dropped: %d; informed deletions dropped: %d; duplicate echoes suppressed: %d; lost: %d in %d finding(s)\n",
		s.CandidateLines, s.AfterMoveFilters, s.SupersededDrop, s.InformedDropped, s.DedupedLines, s.LostLines, len(res.Findings))
}

func short(sha string) string {
	if len(sha) > 9 {
		return sha[:9]
	}
	return sha
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
