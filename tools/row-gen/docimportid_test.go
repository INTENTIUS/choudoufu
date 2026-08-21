// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"reflect"
	"sort"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// sep returns a pointer to s, for a grammar row's Separator.
func docSep(s string) *string { return &s }

// TestDocImportIDPartsReadsANameAndRefusesADescription is the generator's
// half of issue #337's second route: what the scrape has to say before an
// order and a separator become a fact the run time may act on.
//
// The rule under test is the SHAPE of a segment's name, and the reason it has
// to be a shape rule rather than a phrase list is the ordinary one here -
// hashicorp/aws documents 1699 types and a list would cover the phrases
// somebody read.
func TestDocImportIDPartsReadsANameAndRefusesADescription(t *testing.T) {
	cases := []struct {
		name string
		row  importGrammarRow
		want []docImportIDPart
		why  string
	}{
		{
			name: "single-token names, argument attribution carried through",
			row: importGrammarRow{IDParts: []idPart{
				{Token: "REST-API-ID", Source: idPartSourceArgument},
				{Token: "AUTHORIZER-ID", Source: "unknown"},
			}},
			want: []docImportIDPart{{Name: "restapiid", Argument: true}, {Name: "authorizerid"}},
			why:  "the page spells its own placeholder names; the reduction makes them comparable to schema attributes",
		},
		{
			name: "an attribute-sourced segment is not an argument",
			row: importGrammarRow{IDParts: []idPart{
				{Token: "workspace_id", Source: idPartSourceArgument},
				{Token: "service_account_id", Source: idPartSourceAttribute},
			}},
			want: []docImportIDPart{{Name: "workspaceid", Argument: true}, {Name: "serviceaccountid"}},
			why:  "only idPartSourceArgument means the configuration supplies the value",
		},
		{
			name: "an own-id segment is not an argument either",
			row: importGrammarRow{IDParts: []idPart{
				{Token: "cluster_id", Source: idPartSourceArgument},
				{Token: "group_id", Source: idPartSourceOwnID},
			}},
			want: []docImportIDPart{{Name: "clusterid", Argument: true}, {Name: "groupid"}},
			why:  "own-id is the server's value by definition",
		},
		{
			name: "a segment named by a prose phrase",
			row: importGrammarRow{IDParts: []idPart{
				{Token: "the API mapping identifier", Source: "unknown"},
				{Token: "domain name", Source: idPartSourceArgument},
			}},
			why: "a multi-word phrase is a description of a value, not the name of one. Turning it into a name " +
				"would be a guess, and a guessed segment composes a wrong identity.",
		},
		{
			name: "one good name and one phrase is still a refusal",
			row: importGrammarRow{IDParts: []idPart{
				{Token: "identity_store_id", Source: idPartSourceArgument},
				{Token: "the group's own id", Source: "unknown"},
			}},
			why: "a partially readable grammar is not a grammar; the unread segment is exactly the one whose " +
				"position matters",
		},
		{
			name: "two segments reducing to one name",
			row: importGrammarRow{IDParts: []idPart{
				{Token: "api_id", Source: idPartSourceArgument},
				{Token: "APIID", Source: "unknown"},
			}},
			why: "the reduction has lost the difference between them, so both would match the same attribute and " +
				"the composed string would repeat one segment and drop another",
		},
		{
			name: "a token with nothing alphanumeric in it",
			row:  importGrammarRow{IDParts: []idPart{{Token: "api_id", Source: idPartSourceArgument}, {Token: "---", Source: "unknown"}}},
			why:  "reduces to the empty name, which would match every attribute or none",
		},
		{
			name: "one segment",
			row:  importGrammarRow{IDParts: []idPart{{Token: "api_id", Source: idPartSourceArgument}}},
			why:  "not a composite",
		},
		{
			name: "no segments scraped at all",
			row:  importGrammarRow{},
			why:  "importdocs-gen returns nothing unless the names it read account for every segment of the documented example",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := docImportIDParts(tc.row)
			if ok != (tc.want != nil) {
				t.Fatalf("ok = %v, want %v.\n%s", ok, tc.want != nil, tc.why)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parts = %+v, want %+v.\n%s", got, tc.want, tc.why)
			}
		})
	}
}

// TestDocImportIDRosterIsBoundedByTheRefusal holds the roster's population to
// what its own doc comment claims, over synthesized rows rather than over the
// committed artifact - so the bound is asserted by construction and not by
// whichever types happen to be in hashicorp/aws today.
func TestDocImportIDRosterIsBoundedByTheRefusal(t *testing.T) {
	parts := []idPart{
		{Token: "api_id", Source: idPartSourceArgument},
		{Token: "route_id", Source: "unknown"},
	}

	grammar := map[string]importGrammarRow{
		"composite_and_unproven": {Separator: docSep("/"), IDParts: parts},
		"composite_but_id_is_proven_whole": {
			Separator:   docSep("/"),
			IDParts:     parts,
			IDAttribute: &idAttributeDoc{StatedSeparator: "/", Description: "Combined ID of the api and the route, separated by `/`."},
		},
		"flat_import": {IDParts: parts},
		"composite_with_a_disagreeing_id_bullet": {
			Separator:   docSep("/"),
			IDParts:     parts,
			IDAttribute: &idAttributeDoc{StatedSeparator: ":", Description: "Combined ID separated by `:`."},
		},
	}

	var got []string
	for _, r := range docImportIDRoster(grammar) {
		got = append(got, r.TFType)
	}
	sort.Strings(got)

	want := []string{"composite_and_unproven", "composite_with_a_disagreeing_id_bullet"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("roster = %v, want %v\n"+
			"A type whose `id` bullet corroborates the Import section's separator is already served by the "+
			"bare-`id` rule; composing a second answer for it would be a competing identity. A flat import "+
			"has no segments to order. A bullet that DISAGREES corroborates nothing, so the type stays "+
			"refused-and-therefore-eligible.", got, want)
	}

	if rows := docImportIDRoster(grammar); len(rows) > 0 {
		r := rows[0]
		if r.Separator != "/" {
			t.Errorf("separator = %q, want the Import section's own %q", r.Separator, "/")
		}
		if len(r.Parts) != 2 || r.Parts[0].Name != "apiid" || !r.Parts[0].Argument || r.Parts[1].Name != "routeid" || r.Parts[1].Argument {
			t.Errorf("parts = %+v, want the documented order with the page's own attribution", r.Parts)
		}
	}
}

// TestNormalizeDocNameMatchesTheGenerator holds the three copies of one
// reduction to one answer.
//
// tools/importdocs-gen writes the names, this package reduces them, and
// internal/live/identity matches them against schema attributes - three
// packages, three declarations, because each reads an artifact rather than
// importing the writer of it. A drift between any two would be invisible: the
// generator would emit names run time could never match, and the whole route
// would silently reach nothing while every test above still passed.
func TestNormalizeDocNameMatchesTheGenerator(t *testing.T) {
	for _, s := range []string{
		"REST-API-ID", "rest_api_id", "policy_store_id", "id", "APIID",
		"Instance Group id", "arn:aws:iam::123", "", "---", "v2Model",
	} {
		if got, want := identity.NormalizeDocName(s), normalizeName(s); got != want {
			t.Errorf("identity's reduction of %q = %q, this package's = %q", s, got, want)
		}
		if got, want := normalizeName(s), normalize(s); got != want {
			t.Errorf("this package's reduction of %q = %q, importdocs-gen's = %q", s, got, want)
		}
	}
}

// normalize is tools/importdocs-gen's own function, copied here as the third
// side of the comparison above rather than imported, because that generator
// is a main package. Copying it is the point: if the original changes and
// this does not, the test above still fails - against the artifact's actual
// contents, which TestDocumentedImportIDsIsBoundedByTheRefusalItReplaces
// checks are in comparison form.
func normalize(s string) string {
	var out []rune
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			out = append(out, r+('a'-'A'))
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			out = append(out, r)
		}
	}
	return string(out)
}
