// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package statpages renders the generated stat pages under live/ (#2092):
// the gauntlet board, the readiness tiers, the claims table, the IAM
// reference, the fork surface and the substrate matrix, as plain
// GitHub-flavoured markdown.
//
// This reverses #1055's "generators emit data only" for these pages, by the
// maintainer's ruling of 2026-10-19. The Hugo site and its site/data/*.json
// copies were retired at the cutover; the site links these pages.
//
// Every page is hand-written text around one or more spans between
// `<!-- <tool>:begin <name> -->` and `<!-- <tool>:end <name> -->` markers,
// the style toggles-gen and tagverbs-gen use. A render replaces only what
// is between the markers, so the text around them survives.
//
// Every render here is a pure function of committed files: no git, no
// network, no clock. That is what lets live/statpages_test.go re-render
// every span in memory and compare it with the tree. Three of the inputs
// are derived summaries a generator writes beside its artifact (the board,
// readiness and fork-surface summaries); they lived under site/data/ until
// the Hugo site was retired (#2092), and the constants below are the one
// place that names them.
//
// `go run ./tools/gauntlet render` refreshes every page (WriteAll). Each
// other generator's -render mode refreshes its own page after writing its
// data, so a re-measured artifact and its page move together.
package statpages

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/live/mdspan"
)

// RefreshCommand is the one command that re-renders every page.
const RefreshCommand = "go run ./tools/gauntlet render"

// Input paths, relative to the repository root.
const (
	GauntletArtifactRel = "live/gauntlet.json"
	GauntletBoardRel    = "live/gauntlet-board.json"
	ReadinessSiteRel    = "live/readiness-summary.json"
	ForkSurfaceSiteRel  = "live/fork-surface-summary.json"
	ClaimsRel           = "live/smoke/claims.json"
	IAMReferenceRel     = "live/iam-reference.json"
	SubstratesRel       = "live/substrates.json"
)

// Page is one generated markdown file.
type Page struct {
	// Path is the page, relative to the repository root.
	Path string
	// Tool is the marker vocabulary: `<!-- Tool:begin name -->`.
	Tool string
	// Command regenerates the data this page is drawn from and then the
	// page itself. RefreshCommand re-renders the page from data already
	// committed.
	Command string
	// Inputs are the committed files the spans are rendered from.
	Inputs []string
	// spans renders every span body, by span name, from root.
	spans func(root string) (map[string]string, error)
}

// Board, Readiness, Claims, IAMReference, ForkSurface and Substrates are
// the pages; Pages lists them in that order.
var (
	Board = Page{
		Path: "live/BOARD.md", Tool: "board-gen",
		Command: "go run ./tools/gauntlet render",
		Inputs:  []string{GauntletBoardRel, GauntletArtifactRel},
		spans:   boardSpans,
	}
	Readiness = Page{
		Path: "live/READINESS.md", Tool: "readiness-gen",
		Command: "go run ./tools/readiness-gen -render",
		Inputs:  []string{ReadinessSiteRel},
		spans:   readinessSpans,
	}
	Claims = Page{
		Path: "live/CLAIMS.md", Tool: "claims-gen",
		Command: "go run ./tools/gauntlet render",
		Inputs:  []string{ClaimsRel, GauntletArtifactRel, "live/smoke/evidence/*.json", GoTestEvidenceDir + "/*.json"},
		spans:   claimsSpans,
	}
	IAMReference = Page{
		Path: "live/IAM-REFERENCE.md", Tool: "iamref-gen",
		Command: "go run ./tools/iamref-gen -render",
		Inputs:  []string{IAMReferenceRel},
		spans:   iamSpans,
	}
	ForkSurface = Page{
		Path: "live/FORK-SURFACE.md", Tool: "forkdiff-gen",
		Command: "go run ./tools/forkdiff-gen -render",
		Inputs:  []string{ForkSurfaceSiteRel},
		spans:   forkSpans,
	}
	Substrates = Page{
		Path: "live/SUBSTRATES.md", Tool: "substrates-gen",
		Command: "go run ./tools/substrates-gen -render",
		Inputs:  []string{SubstratesRel},
		spans:   substratesSpans,
	}
)

// Pages is every generated stat page.
func Pages() []Page {
	return []Page{Board, Readiness, Claims, IAMReference, ForkSurface, Substrates}
}

// Render returns the page as it should read: the committed file with every
// span re-rendered from the committed inputs. It writes nothing.
func Render(root string, p Page) (string, error) {
	doc, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p.Path)))
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", p.Path, err)
	}
	spans, err := p.spans(root)
	if err != nil {
		return "", fmt.Errorf("%s: %w", p.Path, err)
	}
	m := mdspan.For(p.Tool)
	out := string(doc)
	for _, name := range sortedKeys(spans) {
		out, err = m.Replace(p.Path, out, name, spans[name])
		if err != nil {
			return "", err
		}
	}
	return out, nil
}

// Write re-renders one page in place, writing only when it changed. It
// reports whether it wrote.
func Write(root string, p Page) (bool, error) {
	want, err := Render(root, p)
	if err != nil {
		return false, err
	}
	path := filepath.Join(root, filepath.FromSlash(p.Path))
	got, err := os.ReadFile(path)
	if err == nil && string(got) == want {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil { //nolint:gosec // a committed doc
		return false, fmt.Errorf("writing %s: %w", p.Path, err)
	}
	return true, nil
}

// WriteAll re-renders every page and returns the ones it changed.
func WriteAll(root string) ([]string, error) {
	var changed []string
	for _, p := range Pages() {
		wrote, err := Write(root, p)
		if err != nil {
			return changed, err
		}
		if wrote {
			changed = append(changed, p.Path)
		}
	}
	return changed, nil
}

// readJSON decodes one committed input.
func readJSON(root, rel string, v any) error {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return fmt.Errorf("reading %s: %w", rel, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("decoding %s: %w", rel, err)
	}
	return nil
}

// cell makes s safe inside a table cell: one line, no bare pipe.
func cell(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.ReplaceAll(s, "|", `\|`)
}

// short is a commit sha at the 10-character width the rest of the repo uses.
func short(sha string) string {
	if len(sha) > 10 {
		return sha[:10]
	}
	return sha
}

// day is the date part of an RFC 3339 timestamp.
func day(ts string) string {
	if len(ts) >= 10 {
		return ts[:10]
	}
	return ts
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
