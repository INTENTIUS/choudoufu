// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "testing"

// TestApplyImportGrammarDemotions_ConnectQueueShape is issue #132's exit for
// the Connect family: the grammar row says composed_of_arguments (one
// argument matched), but the doc's own per-segment attribution names the
// other segment as an Attribute Reference export - a server-provided value.
// The demotion must not fire; the registry's server-assigned reading was
// right, and the doc says so itself.
func TestApplyImportGrammarDemotions_ConnectQueueShape(t *testing.T) {
	proposals := []proposal{
		{TFType: "aws_connect_queue", CFNType: "AWS::Connect::Queue", Bucket: bucketServerAssigned},
	}
	composed := true
	sep := ":"
	grammar := map[string]importGrammarRow{
		"aws_connect_queue": {
			TFType:              "aws_connect_queue",
			ImportIDExample:     "f1288a1f-6193:c1d4e5f6-1b3c",
			ComposedOfArguments: &composed,
			Separator:           &sep,
			Arguments:           []string{"instance_id"},
			IDParts: []idPart{
				{Token: "instance_id", Source: "argument"},
				{Token: "queue_id", Source: idPartSourceAttribute},
			},
		},
	}

	applyImportGrammarDemotions(proposals, grammar)

	p := proposals[0]
	if p.Bucket != bucketServerAssigned {
		t.Fatalf("aws_connect_queue bucket = %s, want %s (the doc's own attribution names queue_id server-provided)", p.Bucket, bucketServerAssigned)
	}
	found := false
	for _, n := range p.Notes {
		if n == "import docs name a server-provided segment (queue_id); the ID is not wholly argument-composed, so the server-assigned classification stands" {
			found = true
		}
	}
	if !found {
		t.Errorf("Notes = %v, want the server-provided-segment note", p.Notes)
	}
}

// TestApplyImportGrammarDemotions_UnknownSegmentStillDemotes pins the
// counter-shape that reverted the first attempt at this fix: aws_lambda_alias
// is structurally identical to the Connect rows (one matched argument, a
// two-segment example) but its second prose token ("alias") is shorthand for
// the `name` configuration argument, defined in neither doc section, so its
// attribution is "unknown". An unknown segment establishes nothing, and the
// conservative demotion must still fire.
func TestApplyImportGrammarDemotions_UnknownSegmentStillDemotes(t *testing.T) {
	proposals := []proposal{
		{TFType: "aws_lambda_alias", CFNType: "AWS::Lambda::Alias", Bucket: bucketServerAssigned},
	}
	composed := true
	sep := "/"
	grammar := map[string]importGrammarRow{
		"aws_lambda_alias": {
			TFType:              "aws_lambda_alias",
			ImportIDExample:     "example/production",
			ComposedOfArguments: &composed,
			Separator:           &sep,
			Arguments:           []string{"function_name"},
			IDParts: []idPart{
				{Token: "function_name", Source: "argument"},
				{Token: "alias", Source: "unknown"},
			},
		},
	}

	applyImportGrammarDemotions(proposals, grammar)

	if p := proposals[0]; p.Bucket != bucketEvidenceOnly {
		t.Fatalf("aws_lambda_alias bucket = %s, want %s (an unknown segment is no evidence of server assignment)", p.Bucket, bucketEvidenceOnly)
	}
}

// TestTryDocNamedServerSegment_WAFv2Shape: a needs-hand-separator proposal
// (composite registry primaryIdentifier, not wholly read-only) whose doc
// names every segment and attributes the leading one to the Attribute
// Reference resolves server-assigned, with the prose's own segment names as
// the ImportSyntax placeholder.
func TestTryDocNamedServerSegment_WAFv2Shape(t *testing.T) {
	composed := true
	sep := "/"
	g := importGrammarRow{
		TFType:              "aws_wafv2_web_acl",
		ImportIDExample:     "a1b2c3d4/example/REGIONAL",
		ComposedOfArguments: &composed,
		Separator:           &sep,
		Arguments:           []string{"name", "scope"},
		IDParts: []idPart{
			{Token: "ID", Source: idPartSourceAttribute},
			{Token: "Name", Source: "argument"},
			{Token: "Scope", Source: "argument"},
		},
	}
	p := proposal{
		TFType:            "aws_wafv2_web_acl",
		Bucket:            bucketNeedsHandSeparator,
		PrimaryIdentifier: []string{"Name", "Id", "Scope"},
	}

	if !tryDocNamedServerSegment(&p, g) {
		t.Fatal("tryDocNamedServerSegment = false, want true")
	}
	if p.Bucket != bucketServerAssigned {
		t.Errorf("bucket = %s, want %s", p.Bucket, bucketServerAssigned)
	}
	deriveDocImportSyntax(&p, g) // the placeholder derivation runs as its own pass since issue #176
	if p.DerivedImportSyntax != "ID/NAME/SCOPE" {
		t.Errorf("DerivedImportSyntax = %q, want %q", p.DerivedImportSyntax, "ID/NAME/SCOPE")
	}
	if len(p.DerivedIdentityAttrs) != 0 {
		t.Errorf("DerivedIdentityAttrs = %v, want none (issue #44 non-goal)", p.DerivedIdentityAttrs)
	}
}

// TestTryDocNamedServerSegment_AllArgumentsRefuses: prose-named segments
// that all attribute to configuration arguments say nothing about server
// assignment - the rule must decline and leave the proposal for the
// argument-reconstruction rules behind it.
func TestTryDocNamedServerSegment_AllArgumentsRefuses(t *testing.T) {
	sep := ","
	g := importGrammarRow{
		TFType:          "aws_example",
		ImportIDExample: "RUNTIME123,example-endpoint",
		Separator:       &sep,
		IDParts: []idPart{
			{Token: "agent_runtime_id", Source: "argument"},
			{Token: "name", Source: "argument"},
		},
	}
	p := proposal{TFType: "aws_example", Bucket: bucketNeedsHandSeparator}
	if tryDocNamedServerSegment(&p, g) {
		t.Fatal("tryDocNamedServerSegment = true, want false (no segment is attributed to the Attribute Reference)")
	}
	if p.Bucket != bucketNeedsHandSeparator {
		t.Errorf("bucket = %s, want unchanged", p.Bucket)
	}
}

// TestTryDocNamedServerSegment_OwnIDShape is the GuardDuty family: the
// doc's plain prose names the second segment as the resource's own ID
// ("IPSet ID" on the IPSet page), which is server-minted by construction -
// the same promotion the Attribute Reference source earns, from the
// prose-only sibling attribution.
func TestTryDocNamedServerSegment_OwnIDShape(t *testing.T) {
	sep := ":"
	g := importGrammarRow{
		TFType:          "aws_guardduty_ipset",
		ImportIDExample: "00b00fd5aecc:123456789012",
		Separator:       &sep,
		IDParts: []idPart{
			{Token: "the primary GuardDuty detector ID", Source: "argument"},
			{Token: "IPSet ID", Source: idPartSourceOwnID},
		},
	}
	p := proposal{
		TFType:            "aws_guardduty_ipset",
		Bucket:            bucketNeedsHandSeparator,
		PrimaryIdentifier: []string{"Id", "DetectorId"},
	}
	if !tryDocNamedServerSegment(&p, g) {
		t.Fatal("tryDocNamedServerSegment = false, want true")
	}
	if p.Bucket != bucketServerAssigned {
		t.Errorf("bucket = %s, want %s", p.Bucket, bucketServerAssigned)
	}
}

// TestTryDocNamedServerSegment_NoSeparatorRefuses: without a pinned
// separator there is no placeholder to build and the scrape-side arity gate
// could not have run against anything, so the rule declines.
func TestTryDocNamedServerSegment_NoSeparatorRefuses(t *testing.T) {
	g := importGrammarRow{
		TFType: "aws_example",
		IDParts: []idPart{
			{Token: "id", Source: idPartSourceAttribute},
			{Token: "name", Source: "argument"},
		},
	}
	p := proposal{TFType: "aws_example", Bucket: bucketNeedsHandSeparator}
	if tryDocNamedServerSegment(&p, g) {
		t.Fatal("tryDocNamedServerSegment = true, want false (no separator)")
	}
}

// TestTryDocNamedServerSegment_QuickSightFolderShape is issue #296's exit:
// the doc's per-segment attribution names "folder ID name" as an Attribute
// Reference export (importdocs-gen's best guess about which section defines
// the segment), but `folder_id` is ALSO independently documented as a
// Required, Forces-new Argument Reference bullet on the very same page - a
// real, client-supplied argument, not a computed one. The values here are
// aws_quicksight_folder's actual live/import-grammar.json row. Before this
// issue's fix, docNamesServerSegment took the "attribute" attribution at
// face value and this rule wrongly promoted the type to server-assigned;
// the fix (routing through docMintedSegment, which subtracts
// ArgumentNamesAnyDepth) must decline, leaving the type for the
// argument-reconstruction rules that correctly bucket the other nine
// CFN-modeled aws_quicksight_* types "needs hand separator".
func TestTryDocNamedServerSegment_QuickSightFolderShape(t *testing.T) {
	sep := ","
	g := importGrammarRow{
		TFType:          "aws_quicksight_folder",
		ImportIDExample: "123456789012,example-id",
		Separator:       &sep,
		IDParts: []idPart{
			{Token: "the AWS account ID", Source: "unknown"},
			{Token: "folder ID name", Source: idPartSourceAttribute},
		},
		ArgumentNamesAnyDepth: []string{
			"folder_id", "name", "aws_account_id", "folder_type",
			"parent_folder_arn", "permissions", "region", "tags",
			"actions", "principal",
		},
	}
	p := proposal{
		TFType:            "aws_quicksight_folder",
		Bucket:            bucketNeedsHandSeparator,
		PrimaryIdentifier: []string{"AwsAccountId", "FolderId"},
	}
	if tryDocNamedServerSegment(&p, g) {
		t.Fatal("tryDocNamedServerSegment = true, want false (folder_id is independently a documented Argument Reference bullet, not server-minted)")
	}
	if p.Bucket != bucketNeedsHandSeparator {
		t.Errorf("bucket = %s, want unchanged %s", p.Bucket, bucketNeedsHandSeparator)
	}
}
