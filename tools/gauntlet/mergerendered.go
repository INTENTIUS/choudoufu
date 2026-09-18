// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// RenderedMergePaths are the rendered files .gitattributes routes to this
// merge driver: the ones that carry measurements and so change on every run
// that moves a verdict. They are derived bytes with a single writer,
// `gauntlet render`, and a line-based merge of two of them is not a merge
// of anything - the inputs it would be combining are in live/gauntlet.json,
// one derivation back.
//
// live/gauntlet.json itself is deliberately NOT here. It is the source the
// others are derived from, its merge is a row-level question, and
// `gauntlet merge-artifact` is the tool that answers it (live/GAUNTLET.md,
// "Merging estate rows across PRs"). Nor are the manifest and the two agent
// briefs, which Render also writes: they carry no measurement, so two
// branches rarely touch the same line of them, and a conflict there is a
// real disagreement worth reading.
var RenderedMergePaths = []string{
	SpecPath,
	SiteDataPath,
	SiteBoardPath,
	SiteScalePath,
}

// cmdMergeRendered is the `merge=gauntlet-rendered` driver .gitattributes
// names. Git hands it the path being merged and the file holding OUR
// version, which git has already placed where the result belongs; the
// driver keeps that version and exits 0, so the merge completes with no
// conflict markers in bytes nobody should be reading.
//
// Keeping ours is not a claim that ours is right. It is the same resolution
// HANDOFF.md already prescribes by hand (`git checkout --ours`, then re-run
// the estate and render), with the hand-resolution step removed - and
// removing it is the point twice over. It saved six conflict resolutions on
// 2026-09-18 alone, across five branches; and because ours is a whole board
// that some render really produced, it cannot be the half-and-half board a
// hunk-by-hunk resolution produces, which is #1308's hazard: replaying this
// repository's merge history, three of the conflict hunks across five
// merges were in the #1264 script-staleness fields alone, and resolving
// those from one side while resolving the banner hunk from the other yields
// a board whose headline contradicts its own rows.
//
// What makes keeping ours SAFE is the guard on the other side, not the
// driver: TestRenderedDocsAreCurrent compares every one of these files
// against a fresh render and fails until someone runs `gauntlet render`.
// The driver decides nothing about content; it only declines to invent any.
//
// Deliberately not attempted: regenerating the file from the merged
// artifact. The driver cannot see it. Git's merge computes every path
// before writing any of them, so live/gauntlet.json in the working tree
// during this call is still the pre-merge one whichever order the paths
// sort in, and a driver that rendered from it would write a board for an
// artifact that is about to be replaced - a wrong answer where keeping ours
// is an honest stale one.
func cmdMergeRendered(args []string, out io.Writer) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: gauntlet merge-rendered <path> <ours-file>\n\nThis is a git merge driver, normally invoked by git through .gitattributes.\nInstall it with `just merge-drivers`.")
	}
	path, ours := args[0], args[1]
	if !isRenderedMergePath(path) {
		return fmt.Errorf("merge-rendered: %q is not one of the rendered files this driver handles (%s); merge it normally", path, strings.Join(RenderedMergePaths, ", "))
	}
	// Git has already written our version to this file; the driver's
	// contract is that whatever it leaves there is the merge result. Read
	// it only to fail loudly rather than silently emptying the path.
	if _, err := os.Stat(ours); err != nil {
		return fmt.Errorf("merge-rendered: cannot read our version of %s at %s: %w", path, ours, err)
	}
	fmt.Fprintf(out, "merge-rendered: kept our %s unmerged - it is generated, and a line-based merge of two renders is not a merge of anything.\n", path)
	fmt.Fprintf(out, "  Run `go run ./tools/gauntlet render` once the merge is committed; TestRenderedDocsAreCurrent fails until you do.\n")
	return nil
}

func isRenderedMergePath(p string) bool {
	p = strings.TrimPrefix(p, "./")
	for _, r := range RenderedMergePaths {
		if r == p {
			return true
		}
	}
	return false
}
