// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// serverAssignedSingleAttrRows is the rule's domain, read off the table and
// the provider's identity schema together: every ServerAssigned row whose
// identity schema requires exactly one attribute for import, with that
// attribute. Shared by the tests below so none of them can silently be
// looking at a different set from the rule.
func serverAssignedSingleAttrRows(t *testing.T, survey map[string]surveyEntry) map[string]string {
	t.Helper()
	out := map[string]string{}
	for name, entry := range identity.DefaultTable {
		if !entry.ServerAssigned {
			continue
		}
		if attr, ok := survey[name].identityArg(); ok {
			out[name] = attr
		}
	}
	return out
}

// TestServerAssignedRowsCarryTheirSchemaIdentityAttr is the ratchet over
// mergeIdentityAttrs: every server-assigned row whose provider identity
// schema names exactly one attribute must offer that attribute as an
// identity source, so a sibling reading it resolves instead of hitting
// "Not an identity attribute" (#197).
//
// The external source is live/survey-full.json - the provider's own resource
// identity schema, read off a live GetProviderSchema boot by tools/survey-gen,
// not anything this table or this generator derives. Mutating a row's
// IdentityAttrs to disagree fails this test; mutating it to agree with the
// generator's own rule but not the provider's schema fails it too, which is
// the hole the phase-5 audit found in the first identityattr ratchet.
func TestServerAssignedRowsCarryTheirSchemaIdentityAttr(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	survey, err := loadSurvey(root + "/" + surveyJSONRel)
	if err != nil {
		t.Fatalf("loading the survey: %s", err)
	}

	rows := serverAssignedSingleAttrRows(t, survey)

	// The blindness guard. A rule that saw two rows would pass this file
	// while asserting nothing about the table; the count measured when the
	// rule landed was 178 (103 rows that already agreed plus the 75 it
	// filled), so a collapse to a handful means the domain moved, not that
	// the table got better.
	const minRows = 100
	if len(rows) < minRows {
		t.Fatalf("only %d server-assigned row(s) have a single-attribute identity schema; this test can see too little to mean anything (want at least %d)", len(rows), minRows)
	}

	for name, attr := range rows {
		entry := identity.DefaultTable[name]
		found := false
		for _, have := range entry.IdentityAttrs {
			if have == attr {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s is server-assigned and its provider identity schema requires exactly %q for import, but the row's IdentityAttrs are %v.\n"+
				"A sibling reading %s.<name>.%s is refused with \"Not an identity attribute\", and internal/live/discovery's importIdentity looks for \"id\" in an identity object that does not carry one.\n"+
				"Regenerate with: go run ./tools/row-gen -emit",
				name, attr, entry.IdentityAttrs, name, attr)
		}
	}
}

// TestServerAssignedIdentityAttrsAgreeWithTheDocs corroborates the rule
// against the OTHER source, which the rule itself never reads: the scraped
// documentation's own "### Identity Schema" section
// (live/import-grammar.json's identity_schema_required, from
// tools/importdocs-gen). If the wire schema and the docs ever disagree about
// what identifies a server-assigned type, the rule is deriving from a source
// the documentation contradicts and a human has to rule on it.
//
// Measured when the rule landed, over the 75 rows it filled: 73 corroborated
// exactly, 2 (aws_codegurureviewer_repository_association,
// aws_inspector_resource_group) have a docs row with no Identity Schema
// section at all - a coverage gap, not a contradiction. Zero disagreements.
func TestServerAssignedIdentityAttrsAgreeWithTheDocs(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	survey, grammar := loadSourcesForTest(t, root)

	rows := serverAssignedSingleAttrRows(t, survey)

	var corroborated int
	for name, attr := range rows {
		documented := grammar[name].IdentitySchemaRequired
		if len(documented) == 0 {
			continue // coverage gap in the scrape, not a disagreement
		}
		found := false
		for _, d := range documented {
			if d == attr {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s: the provider's identity schema requires %q, the documented Identity Schema section says %v. Two sources disagree about what identifies this type; the row needs a ruling, not a derivation.",
				name, attr, documented)
			continue
		}
		corroborated++
	}
	if corroborated == 0 {
		t.Fatal("no row was corroborated by the documented Identity Schema section, so this test asserted nothing")
	}
}

// TestIdentityAttrRuleNeverTouchesAComponentRow pins the boundary the rule
// stops at, in both directions.
//
// A row with Components asserts its own import ID by construction, and that
// assertion can legitimately disagree with the identity schema's single
// required attribute - aws_codebuild_project and aws_codebuild_fleet import
// by "name" while their identity schemas require "arn". Applying the rule
// there would replace a ratified, documented import ID with a different
// attribute, which is the wrong-marker failure this whole table exists to
// avoid.
//
// The second half matters as much as the first: if no component row
// disagreed with its schema, the restriction would cost nothing and nothing
// would notice its removal.
func TestIdentityAttrRuleNeverTouchesAComponentRow(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	survey, err := loadSurvey(root + "/" + surveyJSONRel)
	if err != nil {
		t.Fatalf("loading the survey: %s", err)
	}

	var disagreeing int
	for name, entry := range identity.DefaultTable {
		if entry.ServerAssigned {
			continue
		}
		got := mergeIdentityAttrs(entry, survey[name])
		if len(got.IdentityAttrs) != len(entry.IdentityAttrs) {
			t.Errorf("mergeIdentityAttrs changed %s, which has components: %v -> %v", name, entry.IdentityAttrs, got.IdentityAttrs)
		}
		attr, ok := survey[name].identityArg()
		if !ok || len(entry.Components) == 0 {
			continue
		}
		named := false
		for _, have := range entry.IdentityAttrs {
			if have == attr {
				named = true
				break
			}
		}
		if !named {
			disagreeing++
		}
	}
	if disagreeing == 0 {
		t.Error("no component row's ratified IdentityAttrs omits its identity schema's single required attribute, so the ServerAssigned-only restriction is guarding nothing and this test would not notice its removal")
	}
}
