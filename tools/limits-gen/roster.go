// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/live/check"
	"github.com/intentius/choudoufu/internal/live/docsref"
)

// The limits wing's fixture tree. live/LIMITATIONS.md's own preamble states
// the correspondence this file turns into a rendered column: every construct
// the document names has a directory here whose name is the heading, and
// internal/live/lint's TestLimitationsDocCoversDirs enforces it in both
// directions for the "### " headings.
const fixtureDirRel = "live/e2e/limits"

// spanRoster is the generated index of the hand-written half of this
// document.
//
// GitHub issue #698's scope correction: the per-refusal spans this generator
// already writes cover the 188 refusals nobody wrote prose for, and stop at
// the 28 lint rules, whose entries under "Enforced today" are the Construct /
// Why banned / Forwarding address / Enforcement treatment that #110
// deliberately left hand-written. Leaving them out of every generated span
// entirely meant the machine-known half of those entries - which rule, how
// fatal, which document, which fixture directory - was retyped prose like
// everything else, and a lint rule added with no entry, or an entry whose
// fixture directory had been renamed, was invisible here.
//
// So the prose stays hand-written and its roster is generated. The table
// below is derived from internal/live/lint's own rule table and from the
// fixture tree on disk, and [renderRoster] refuses to render at all when the
// two disagree.
const spanRoster = "lint-roster"

// renderRoster renders one row per lint refusal: the rule, its summary, its
// severity, where its hand-written entry is, and the fixture directory that
// entry's construct is pinned by.
//
// It returns an error rather than rendering a gap. A row saying "no fixture"
// for a rule whose directory had simply been renamed would read as a
// deliberate exemption, and this document has been wrong in exactly that
// direction before - the three receipt rules, which genuinely have no
// directory, are the reason a reader cannot treat a blank as impossible.
// Those three are recognised by citing a document other than this one, which
// is the fact that makes them different rather than a list of their names.
func renderRoster(catalog []check.Refusal, root string) (string, error) {
	rules := make([]check.Refusal, 0, len(catalog))
	for _, r := range catalog {
		if r.RaisedBy == check.RaisedByLint {
			rules = append(rules, r)
		}
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })

	var b strings.Builder
	b.WriteString("| Rule | Summary | Severity | Documented at | Fixture |\n")
	b.WriteString("|---|---|---|---|---|\n")
	withFixtures := 0
	for _, r := range rules {
		if strings.TrimSpace(r.Title) == "" {
			return "", fmt.Errorf("lint rule %q has no summary, so its row in %s's %s span would name it and say nothing", r.ID, limitationsRel, spanRoster)
		}
		ref, err := docsref.Parse(r.DocsRef)
		if err != nil {
			return "", fmt.Errorf("lint rule %q: %w", r.ID, err)
		}
		fixtures, err := fixturesFor(root, r.ID, ref)
		if err != nil {
			return "", err
		}
		cell := "none"
		if len(fixtures) > 0 {
			withFixtures++
			quoted := make([]string, len(fixtures))
			for i, f := range fixtures {
				quoted[i] = "`" + f + "`"
			}
			cell = strings.Join(quoted, ", ")
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s |\n",
			r.ID, mdCell(r.Title), severityLabel(r), mdCell(documentedAt(r)), cell)
	}

	fmt.Fprintf(&b, "\n**%d lint rules**, from `internal/live/lint`'s own rule table. "+
		"The entries below this table are hand-written and stay that way - a rule's "+
		"Construct / Why banned / Forwarding address / Enforcement treatment is "+
		"prose nobody should generate - but the roster of them is not, so a rule "+
		"added with no entry, or an entry whose fixture directory was renamed, "+
		"fails `just limits` rather than sitting here unnoticed. **Fixture** is "+
		"`%s/<heading>/` for each heading the rule cites in this document, checked "+
		"to exist when this table was rendered; %d of the %d rules have one. The "+
		"remaining %d cite `live/RECEIPTS.md`, which specifies them alongside the "+
		"pattern they guard and has no fixture directory here. **Documented at** "+
		"drops this document's own filename, so a bare quoted heading is a "+
		"section below. **Severity** is read the way \"Every refusal, "+
		"enumerated\" reads it: `error` unless marked `warning`.\n",
		len(rules), fixtureDirRel, withFixtures, len(rules), len(rules)-withFixtures)
	return b.String(), nil
}

// fixturesFor resolves the fixture directories a lint rule's hand-written
// entries are pinned by, and fails when one is missing.
//
// A reference into another document (live/RECEIPTS.md) has no fixture
// directory here by design and returns none. A reference into this document
// must have one per heading: that is what live/LIMITATIONS.md's preamble
// promises a reader, and what TestLimitationsDocCoversDirs already holds the
// heading side of.
func fixturesFor(root, rule string, ref docsref.Ref) ([]string, error) {
	if ref.Doc != limitationsRel {
		return nil, nil
	}
	out := make([]string, 0, len(ref.Headings))
	for _, heading := range ref.Headings {
		rel := fixtureDirRel + "/" + heading
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("lint rule %q is documented at %s, %q, and there is no fixture directory %s/ to pin that entry's construct; "+
				"rename the heading and the directory together, or move the rule's reference to the document that does specify it",
				rule, limitationsRel, heading, rel)
		}
		out = append(out, rel+"/")
	}
	return out, nil
}
