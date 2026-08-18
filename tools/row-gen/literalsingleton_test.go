// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// sesv2VdmAttributesRow mirrors what tools/importdocs-gen actually writes
// for aws_sesv2_account_vdm_attributes (verified against the regenerated
// live/import-grammar.json at the 6.59.0 pin), inlined so this test states
// its evidence rather than depending on the committed artifact's current
// contents.
func sesv2VdmAttributesRow() importGrammarRow {
	return importGrammarRow{
		TFType:              "aws_sesv2_account_vdm_attributes",
		ImportIDExample:     "ses-account-vdm-attributes",
		ComposedOfArguments: nil,
		SoleIDLiteralValue:  "ses-account-vdm-attributes",
		ArgumentReference: []argumentRefEntry{
			{Name: "vdm_enabled", Required: true},
			{Name: "region", CloudDefault: "region"},
			{Name: "dashboard_attributes"},
			{Name: "guardian_attributes"},
		},
	}
}

// TestTryLiteralSingletonID_ResolvesSESv2VDMAttributes is issue #282's exit:
// the CFN registry's primaryIdentifier ⊆ readOnlyProperties signal
// (AWS::SES::VdmAttributes' VdmAttributesResourceId) proposes
// bucketServerAssigned before this rule runs, exactly as
// live/import-grammar.json and the pre-fix report both show. The provider's
// own docs instead say the import ID is the constant word
// "ses-account-vdm-attributes", identical for every account, and this rule
// must overturn the registry's proposal rather than merely fill a gap.
func TestTryLiteralSingletonID_ResolvesSESv2VDMAttributes(t *testing.T) {
	p := proposal{TFType: "aws_sesv2_account_vdm_attributes", Bucket: bucketServerAssigned}
	if !tryLiteralSingletonID(&p, sesv2VdmAttributesRow()) {
		t.Fatal("the literal singleton was not resolved")
	}
	if p.Bucket != bucketAssembled {
		t.Fatalf("bucket = %s, want %s", p.Bucket, bucketAssembled)
	}

	// The rendered row, not the bucket: a green predicate over a component
	// that resolved to the wrong string is this repository's recurring
	// failure mode.
	serverAssigned, components, importSyntax, _, _ := proposedFields(p)
	if serverAssigned {
		t.Error("the proposal still claims the identity is server-assigned")
	}
	want := []identity.Component{{Literal: "ses-account-vdm-attributes"}}
	if !reflect.DeepEqual(components, want) {
		t.Errorf("components = %#v, want %#v", components, want)
	}
	if importSyntax != "ses-account-vdm-attributes" {
		t.Errorf("ImportSyntax = %q, want %q", importSyntax, "ses-account-vdm-attributes")
	}
}

// TestTryLiteralSingletonID_RefusalsEachDoWork removes exactly one clause's
// evidence at a time from a row the rule otherwise fires on, and asserts
// each removal alone is enough to refuse. A refusal that never fires is a
// comment, not a check.
func TestTryLiteralSingletonID_RefusalsEachDoWork(t *testing.T) {
	for name, mutate := range map[string]func(*importGrammarRow){
		"a schemed template belongs to tryAssembledTemplate": func(g *importGrammarRow) {
			g.IDTemplate = &idTemplate{Kind: "arn", Segments: []idTemplateSegment{
				{Literal: "arn:aws:ses:"}, {Cloud: "region"}, {Literal: ":x"},
			}}
		},
		"the doc names the arguments the ID composes from": func(g *importGrammarRow) {
			g.ComposedOfArguments = boolPtr(true)
			g.Arguments = []string{"vdm_enabled"}
		},
		"the identity schema requires an attribute of the resource's own": func(g *importGrammarRow) {
			g.IdentitySchemaRequired = []string{"id"}
		},
		"the doc states no literal word at all": func(g *importGrammarRow) {
			g.SoleIDLiteralValue = ""
		},
	} {
		t.Run(name, func(t *testing.T) {
			g := sesv2VdmAttributesRow()
			mutate(&g)
			p := proposal{TFType: g.TFType, Bucket: bucketServerAssigned}
			if tryLiteralSingletonID(&p, g) {
				t.Fatalf("the rule fired anyway; bucket = %s", p.Bucket)
			}
			if p.Bucket != bucketServerAssigned {
				t.Errorf("bucket = %s, want it left at %s", p.Bucket, bucketServerAssigned)
			}
		})
	}
}

// TestTryLiteralSingletonID_MayNotOverturnARatifiedServerAssignedRow is the
// tiering issue #282 asks for stated positively: the rule is licensed to
// overturn an UNRATIFIED CFN-registry server-assigned PROPOSAL (that is the
// whole point of the fix, and the test above exercises it), but a row
// already ratified ServerAssigned in the shipped DefaultTable is a human
// judgment this scrape may not overturn - the same standing-claim guard
// tryCloudSingletonID holds itself to. aws_acm_certificate is a real,
// unrelated ratified server-assigned row, used only for the fact that
// DefaultTable says ServerAssigned==true.
func TestTryLiteralSingletonID_MayNotOverturnARatifiedServerAssignedRow(t *testing.T) {
	const acm = "aws_acm_certificate"
	row, ok := identity.DefaultTable[acm]
	if !ok || !row.ServerAssigned {
		t.Fatalf("%s is not the ratified server-assigned row this test is about", acm)
	}
	g := importGrammarRow{
		TFType:             acm,
		ImportIDExample:    "acm-certificate",
		SoleIDLiteralValue: "acm-certificate",
	}
	p := proposal{TFType: acm, Bucket: bucketServerAssigned}
	if tryLiteralSingletonID(&p, g) {
		t.Fatal("the rule overturned a row already ratified ServerAssigned in DefaultTable")
	}
	if p.Bucket != bucketServerAssigned {
		t.Errorf("bucket = %s, want it left at %s", p.Bucket, bucketServerAssigned)
	}
}

// TestLiteralSingletonReachOverTheCommittedArtifact is the measurement,
// pinned, and issue #282's own scope question answered: how many types
// carry live/import-grammar.json's sole_id_literal_value at all. The rule
// is derived and names no type, so this reach is a fact about the 6.59.0
// doc cache (literalsingleton.go's own doc comment: three pages, and only
// three, carry the "using the word `...`" phrasing with a worked example
// that agrees) rather than a list anyone chose - one of the three
// (aws_sesv2_account_vdm_attributes) had a wrong CFN-registry
// server-assigned proposal this rule overturns; the other two
// (aws_iam_account_password_policy, aws_spot_datafeed_subscription) are
// CFN-unmodeled and had no competing proposal, so this rule promotes them
// from evidence-only ("no pastable row") to a real one instead.
//
// A doc-pin bump that moves this list is expected to move it; read the diff
// and re-pin. A code change that moves it is the thing this test exists for.
func TestLiteralSingletonReachOverTheCommittedArtifact(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	proposals, err := loadProposals(root)
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, p := range proposals {
		if p.Bucket == bucketAssembled && len(p.Assembled) == 1 &&
			p.Assembled[0].Literal != "" && p.Assembled[0].Cloud == "" && p.Assembled[0].Argument == "" {
			got = append(got, p.TFType)
		}
	}
	sort.Strings(got)

	want := []string{
		"aws_iam_account_password_policy",
		"aws_sesv2_account_vdm_attributes",
		"aws_spot_datafeed_subscription",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the rule reaches %d types:\n%s\n\nwant %d:\n%s",
			len(got), strings.Join(got, "\n"), len(want), strings.Join(want, "\n"))
	}

	// The blindness guard: every assertion above passes vacuously over an
	// empty proposal set.
	if len(proposals) < 1000 {
		t.Fatalf("only %d proposals were classified; the sweep is not reaching the mapped set", len(proposals))
	}
}
