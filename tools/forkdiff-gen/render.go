// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Render mode (issue #424, mirroring tools/readiness-gen's own -render):
// `go run ./tools/forkdiff-gen -render` writes the docs site's copy of the
// artifact (SiteDataRel, #1055) from the already-committed
// live/fork-surface.json - not a fresh diff against the fork point. That is the same deliberate choice readiness-gen's -render
// makes against live/readiness.json: reading the committed artifact rather
// than recomputing it is what makes a hand-edited or freshly regenerated
// live/fork-surface.json that never got rendered show up as a doc-render
// diff (TestForkSurfaceSiteDataIsCurrent in render_test.go) instead of
// silently passing because the render step re-derived the same numbers
// itself. No git, no network, no other generator's process.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SiteDataRel is the docs site's copy of the fork-surface artifact
// (#1055): everything in live/fork-surface.json except the per-file lists,
// plus the fixed root order, so the site's fork-surface shortcode
// (site/layouts/shortcodes/fork-surface.html) can render the summary the
// positioning page shows. Before #1055 this mode wrote that summary as a
// markdown span into the page itself; now the generator emits data and the
// site decides how it reads.
const SiteDataRel = "site/data/fork_surface.json"

// forkPointCommitURL is the fork point's commit on the upstream project,
// the same linking convention the docs site's root page already uses for
// this same commit. The site's shortcode composes the link from it.
const forkPointCommitURL = "https://github.com/opentofu/opentofu/commit/"

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

// siteForkSurface is the shape of SiteDataRel: the artifact's summary
// fields, the named roots in their fixed order, the other bucket's name and
// the two module paths, so the site can render the same paragraph and
// table the page used to carry without re-deriving any of it.
type siteForkSurface struct {
	ForkPoint           string            `json:"fork_point"`
	ForkPointShort      string            `json:"fork_point_short"`
	ForkPointSubject    string            `json:"fork_point_subject"`
	ForkPointURL        string            `json:"fork_point_url"`
	MeasuredAtHead      string            `json:"measured_at_head"`
	MeasuredAtHeadShort string            `json:"measured_at_head_short"`
	GeneratedAt         string            `json:"generated_at"`
	BaseOpenTofuVersion string            `json:"base_opentofu_version"`
	NamedRoots          []string          `json:"named_roots"`
	OtherBucket         string            `json:"other_bucket"`
	Counts              map[string]int    `json:"counts"`
	NamedTotal          int               `json:"named_total"`
	OtherCount          int               `json:"other_count"`
	Total               int               `json:"total"`
	ModulePathOld       string            `json:"module_path_old"`
	ModulePathNew       string            `json:"module_path_new"`
	MechanicalModule    mechanicalSummary `json:"mechanical_module_rename"`
}

// buildSiteData computes SiteDataRel's content from the artifact's own
// Counts and summary fields - no file I/O, so render_test.go's drift guard
// builds the same bytes runRender would write without touching the
// filesystem. It deliberately carries the "other" bucket as its own count:
// a truthful summary says those files exist and where each is justified,
// rather than rounding them away.
func buildSiteData(a forkSurface) siteForkSurface {
	d := siteForkSurface{
		ForkPoint:           a.ForkPoint,
		ForkPointShort:      a.ForkPointShort,
		ForkPointSubject:    a.ForkPointSubject,
		ForkPointURL:        forkPointCommitURL + a.ForkPoint,
		MeasuredAtHead:      a.MeasuredAtHead,
		MeasuredAtHeadShort: short(a.MeasuredAtHead),
		GeneratedAt:         a.GeneratedAt,
		BaseOpenTofuVersion: a.BaseOpenTofuVersion,
		NamedRoots:          append([]string(nil), namedRoots...),
		OtherBucket:         otherBucket,
		Counts:              map[string]int{},
		ModulePathOld:       modulePathOld,
		ModulePathNew:       modulePathNew,
		MechanicalModule:    a.MechanicalModuleRename,
	}
	for _, r := range namedRoots {
		d.Counts[r] = a.Counts[r]
		d.NamedTotal += a.Counts[r]
	}
	d.OtherCount = a.Counts[otherBucket]
	d.Counts[otherBucket] = d.OtherCount
	d.Total = d.NamedTotal + d.OtherCount
	return d
}

// renderSiteData is SiteDataRel's on-disk form.
func renderSiteData(a forkSurface) ([]byte, error) {
	out, err := json.MarshalIndent(buildSiteData(a), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// runRender is the -render entry point: read the committed
// live/fork-surface.json and write the site's copy, only if it changed.
func runRender() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	artifact, err := loadForkSurfaceArtifact(root)
	if err != nil {
		return err
	}
	data, err := renderSiteData(artifact)
	if err != nil {
		return err
	}
	path := filepath.Join(root, filepath.FromSlash(SiteDataRel))
	old, err := os.ReadFile(path) //nolint:gosec // a fixed path in the checkout
	if err == nil && string(old) == string(data) {
		fmt.Fprintf(os.Stderr, "forkdiff-gen: %s is already current\n", SiteDataRel)
		return nil
	}
	if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // a committed artifact, not a secret
		return fmt.Errorf("writing %s: %w", SiteDataRel, err)
	}
	fmt.Fprintf(os.Stderr, "forkdiff-gen: wrote %s\n", SiteDataRel)
	return nil
}

// short truncates a full commit sha to the same 10-character width
// forkPointCommit and tools/gauntlet/render.go's own short() use, so the
// stamp line reads like every other commit citation on the site.
func short(sha string) string {
	if len(sha) > 10 {
		return sha[:10]
	}
	return sha
}
