// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestDocMintedSegmentRefutations covers the ways a documented
// server-provided segment is refuted. Each refutation exists because
// without it the rule vetoed a real type whose identity the configuration
// or the cloud supplies; none of them names a type in control flow.
func TestDocMintedSegmentRefutations(t *testing.T) {
	own := func(token string) *idPart { return &idPart{Token: token, Source: idPartSourceOwnID} }

	for _, tc := range []struct {
		name string
		row  importGrammarRow
		want string // "" means no server-minted segment
	}{
		{
			name: "a sole segment nothing configures is server-minted",
			row:  importGrammarRow{SoleIDPart: own("the Package ID")},
			want: "the Package ID",
		},
		{
			name: "a composite's attribute segment is server-minted",
			row: importGrammarRow{
				IDParts:               []idPart{{Token: "organization_id", Source: "argument"}, {Token: "group_id", Source: idPartSourceAttribute}},
				ArgumentNamesAnyDepth: []string{"organization_id", "email", "name"},
			},
			want: "group_id",
		},
		{
			name: "the same segment named by a NESTED block's argument is not",
			row: importGrammarRow{
				IDParts:               []idPart{{Token: "identity_provider_config_name", Source: idPartSourceAttribute}},
				ArgumentNamesAnyDepth: []string{"oidc", "identity_provider_config_name"},
			},
			want: "",
		},
		{
			name: "a multi-word prose token whose SUFFIX is an argument is not",
			row: importGrammarRow{
				SoleIDPart:            own("the account ID"),
				ArgumentNamesAnyDepth: []string{"account_id"},
			},
			want: "",
		},
		{
			name: "the provider's own identity schema, all arguments, outranks the prose",
			row: importGrammarRow{
				SoleIDPart:             own("the widget identifier"),
				IdentitySchemaRequired: []string{"graph_identifier", "vpc_id"},
				ArgumentNamesAnyDepth:  []string{"graph_identifier", "vpc_id"},
			},
			want: "",
		},
		{
			name: "an identity schema with a component nothing configures does not refute",
			row: importGrammarRow{
				IDParts:                []idPart{{Token: "rest_api_id", Source: "argument"}, {Token: "id", Source: idPartSourceAttribute}},
				IdentitySchemaRequired: []string{"rest_api_id", "id"},
				ArgumentNamesAnyDepth:  []string{"rest_api_id", "parent_id", "path_part"},
			},
			want: "id",
		},
		{
			name: "an unknown segment establishes nothing",
			row: importGrammarRow{
				IDParts: []idPart{{Token: "function_name", Source: "argument"}, {Token: "alias", Source: "unknown"}},
			},
			want: "",
		},
		{
			name: "a row with no documented import ID at all",
			row:  importGrammarRow{},
			want: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := docMintedSegment(tc.row)
			if tc.want == "" {
				if ok {
					t.Fatalf("docMintedSegment = %q, true; want no server-minted segment", got)
				}
				return
			}
			if !ok || got != tc.want {
				t.Fatalf("docMintedSegment = %q, %v; want %q, true", got, ok, tc.want)
			}
		})
	}
}

// TestDocMintedVetoesAreRefutableFromTheArtifact is the external-source
// leg. Every type the documentation evidence adds to the veto roster is
// checked against two artifacts neither this rule nor this test wrote:
// live/survey-full.json must call it untaggable, and
// live/import-grammar.json's own argument_names_any_depth must not carry
// the segment the veto rests on.
//
// The second half is then MUTATED: the segment's name is inserted into the
// refutation set and the veto must vanish. A rule that survived that would
// be agreeing with itself rather than reading evidence.
func TestDocMintedVetoesAreRefutableFromTheArtifact(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	survey, err := loadSurvey(filepath.Join(root, surveyJSONRel))
	if err != nil {
		t.Fatal(err)
	}
	grammar, err := loadImportGrammar(filepath.Join(root, importGrammarJSONRel))
	if err != nil {
		t.Fatal(err)
	}
	proposals, err := loadProposals(root)
	if err != nil {
		t.Fatal(err)
	}

	var checked int
	for _, p := range proposals {
		if p.Bucket != bucketEvidenceOnly {
			continue
		}
		if _, admitted := identity.DefaultTable[p.TFType]; admitted {
			continue
		}
		entry, inSurvey := survey[p.TFType]
		if !inSurvey || entry.Signals.Taggable {
			continue
		}
		row := grammar[p.TFType]
		segment, ok := docMintedSegment(row)
		if !ok {
			continue
		}
		checked++

		for _, name := range row.ArgumentNamesAnyDepth {
			if normalizeName(name) == normalizeName(segment) {
				t.Errorf("%s: the veto rests on segment %q, which live/import-grammar.json lists as an "+
					"Argument Reference bullet; configuration supplies it", p.TFType, segment)
			}
		}

		// Declaring every documented segment a configuration argument
		// must clear the veto outright. Adding only the first would
		// prove nothing on a row with two server-provided segments
		// (aws_route53_traffic_policy documents "id/version", both
		// exported attributes), so the mutation covers the whole ID.
		mutated := row
		mutated.ArgumentNamesAnyDepth = append([]string(nil), row.ArgumentNamesAnyDepth...)
		for _, part := range row.IDParts {
			mutated.ArgumentNamesAnyDepth = append(mutated.ArgumentNamesAnyDepth, part.Token)
		}
		if row.SoleIDPart != nil {
			mutated.ArgumentNamesAnyDepth = append(mutated.ArgumentNamesAnyDepth, row.SoleIDPart.Token)
		}
		if leftover, stillMinted := docMintedSegment(mutated); stillMinted {
			t.Errorf("%s: declaring every documented segment a configuration argument left %q still "+
				"server-minted; the rule is not reading the refutation set it claims to", p.TFType, leftover)
		}
	}

	if checked == 0 {
		t.Fatal("no type reached the documentation evidence at all; this test would pass while measuring nothing")
	}
	t.Logf("%d untaggable, unadmitted, evidence-only types are vetoed on documentation evidence", checked)
}
