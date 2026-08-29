// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Render mode (issue #424, mirroring tools/readiness-gen's own -render):
// `go run ./tools/forkdiff-gen -render` rewrites the docs site's positioning
// page in place, between `<!-- forkdiff-gen:begin fork-surface -->` /
// `<!-- forkdiff-gen:end fork-surface -->` markers, from the
// already-committed live/fork-surface.json - not a fresh diff against the
// fork point. That is the same deliberate choice readiness-gen's -render
// makes against live/readiness.json: reading the committed artifact rather
// than recomputing it is what makes a hand-edited or freshly regenerated
// live/fork-surface.json that never got rendered show up as a doc-render
// diff (TestForkSurfaceRenderedSpanIsCurrent in render_test.go) instead of
// silently passing because the render step re-derived the same numbers
// itself. No git, no network, no other generator's process.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/intentius/choudoufu/internal/live/mdspan"
)

// markers is this generator's marker vocabulary - a distinct tool name
// ("forkdiff-gen") from every other generator's spans, so two generators'
// regions never collide even if they ever render into the same file.
var markers = mdspan.For("forkdiff-gen")

// PositioningMDRel is the docs site's positioning page, issue #424: the page
// a customer reads to learn what choudoufu adds on top of stock OpenTofu,
// with every claim on it rendered from a committed artifact rather than
// typed by hand.
const PositioningMDRel = "site/content/docs/_index.md"

// spanForkSurface is the fork-surface summary this mode writes into
// PositioningMDRel.
const spanForkSurface = "fork-surface"

// forkPointCommitURL is the fork point's commit on the upstream project,
// the same linking convention the docs site's root page already uses for
// this same commit.
const forkPointCommitURL = "https://github.com/opentofu/opentofu/commit/"

// forkSurfaceJSONURL is live/fork-surface.json's own GitHub blob URL, so the
// rendered page can point a reader at the full file-by-file artifact behind
// the summary.
const forkSurfaceJSONURL = "https://github.com/INTENTIUS/choudoufu/blob/main/live/fork-surface.json"

// forkSurfaceGuardURL is live/forkdiff_test.go, issue #423's guard: the
// place every "other"-bucket path is named with its own one-line reason.
const forkSurfaceGuardURL = "https://github.com/INTENTIUS/choudoufu/blob/main/live/forkdiff_test.go"

// loadForkSurfaceArtifact reads and decodes the already-committed
// live/fork-surface.json. Kept separate from readCommitted (render_test.go,
// a test-only helper) because this one is called from production code
// (runRender), not only from tests - the same split readiness-gen's
// loadArtifact/readCommitted pair makes.
func loadForkSurfaceArtifact(root string) (forkSurface, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(forkSurfaceJSONRel))) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		return forkSurface{}, fmt.Errorf("reading %s: %w (run `go run ./tools/forkdiff-gen` and commit the result first)", forkSurfaceJSONRel, err)
	}
	var a forkSurface
	if err := json.Unmarshal(data, &a); err != nil {
		return forkSurface{}, fmt.Errorf("decoding %s: %w", forkSurfaceJSONRel, err)
	}
	return a, nil
}

// renderForkSurfaceSummary builds the fork-surface span's body: a plain-
// language paragraph plus a per-root table, both computed only from the
// artifact's own Counts and MechanicalModuleRename fields - no file I/O, so
// render_test.go's drift guard renders the same bytes runRender would write
// without touching the filesystem.
//
// It deliberately does not claim every changed file sits "under" the six
// named roots: the artifact's own "other" bucket is real, and a truthful
// summary says so and points at where each of those files is justified,
// rather than rounding it away.
func renderForkSurfaceSummary(a forkSurface) string {
	namedTotal := 0
	for _, r := range namedRoots {
		namedTotal += a.Counts[r]
	}
	otherCount := a.Counts[otherBucket]
	total := namedTotal + otherCount

	var b strings.Builder
	fmt.Fprintf(&b, "**%d files** diverge from stock OpenTofu `%s` at fork point [`%s`](%s%s) (\"%s\"). ",
		total, a.BaseOpenTofuVersion, a.ForkPointShort, forkPointCommitURL, a.ForkPoint, a.ForkPointSubject)
	fmt.Fprintf(&b, "%d of them sit under six fork-owned roots; the remaining %d are outside those roots, each named with its own one-line reason in [`live/forkdiff_test.go`](%s)'s guard rather than assumed stock. ",
		namedTotal, otherCount, forkSurfaceGuardURL)
	fmt.Fprintf(&b, "A further %d files (not counted above) change only the Go module import path, `%s` to `%s`, with no other line touched - the mechanical cost of forking a Go module, not a fact about this fork's own surface.\n\n",
		a.MechanicalModuleRename.ExcludedCount, modulePathOld, modulePathNew)

	b.WriteString("| Root | Files |\n|---|---|\n")
	for _, r := range namedRoots {
		fmt.Fprintf(&b, "| `%s` | %d |\n", r, a.Counts[r])
	}
	fmt.Fprintf(&b, "| other (individually justified) | %d |\n", otherCount)
	fmt.Fprintf(&b, "| **Total** | %d |\n", total)
	fmt.Fprintf(&b, "\nFull file-by-file detail: [`live/fork-surface.json`](%s), regenerated by `go run ./tools/forkdiff-gen`.\n", forkSurfaceJSONURL)

	return b.String()
}

// runRender is the -render entry point: read the committed
// live/fork-surface.json, replace the positioning page's span, write back
// only if it changed.
func runRender() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	artifact, err := loadForkSurfaceArtifact(root)
	if err != nil {
		return err
	}
	body := renderForkSurfaceSummary(artifact)
	return renderSpan(root, PositioningMDRel, spanForkSurface, body)
}

// renderSpan rewrites one named span of one doc in place.
func renderSpan(root, rel, span, body string) error {
	path := filepath.Join(root, filepath.FromSlash(rel))
	doc, err := os.ReadFile(path) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		return fmt.Errorf("reading %s: %w", rel, err)
	}

	out, err := markers.Replace(rel, string(doc), span, body)
	if err != nil {
		return err
	}
	if out == string(doc) {
		fmt.Fprintf(os.Stderr, "forkdiff-gen: %s's %q span is already current\n", rel, span)
		return nil
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil { //nolint:gosec // a committed doc, not a secret
		return fmt.Errorf("writing %s: %w", rel, err)
	}
	fmt.Fprintf(os.Stderr, "forkdiff-gen: rewrote %s's %q span\n", rel, span)
	return nil
}
