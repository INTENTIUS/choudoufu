// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "testing"

// TestApplySchemaFirstArgName_CoversClientNamedPath is issue #428's core
// claim: a bucketEvidenceOnly row whose survey entry proves Path==client-named
// with exactly one required-for-import attribute is promoted, with that
// attribute as ArgName and the #428 provenance marker.
func TestApplySchemaFirstArgName_CoversClientNamedPath(t *testing.T) {
	proposals := []proposal{{TFType: "aws_example_thing", Bucket: bucketEvidenceOnly, NoCFNModel: true}}
	survey := map[string]surveyEntry{
		"aws_example_thing": {
			Type:     "aws_example_thing",
			Path:     surveyPathClientNamed,
			Identity: &surveyIdentity{RequiredForImport: []string{"name"}},
		},
	}
	applySchemaFirstArgName(proposals, survey)

	p := proposals[0]
	if p.Bucket != bucketClientNamed {
		t.Fatalf("bucket = %s, want %s", p.Bucket, bucketClientNamed)
	}
	if p.ArgName != "name" {
		t.Errorf("ArgName = %q, want %q", p.ArgName, "name")
	}
	if p.ArgSource != argSourceIdentitySchemaEvidenceOnly {
		t.Errorf("ArgSource = %q, want %q", p.ArgSource, argSourceIdentitySchemaEvidenceOnly)
	}
}

// TestApplySchemaFirstArgName_CoversParentDerivedPath: parent-derived is the
// same identity.DerivableWith safety check as client-named, differing only
// in an informational cross-reference - see evidenceschema.go's own doc
// comment. A single required attribute promotes exactly the same way.
func TestApplySchemaFirstArgName_CoversParentDerivedPath(t *testing.T) {
	proposals := []proposal{{TFType: "aws_example_child", Bucket: bucketEvidenceOnly, NoCFNModel: true}}
	survey := map[string]surveyEntry{
		"aws_example_child": {
			Type:     "aws_example_child",
			Path:     surveyPathParentDerived,
			Identity: &surveyIdentity{RequiredForImport: []string{"parent_arn"}},
		},
	}
	applySchemaFirstArgName(proposals, survey)

	if proposals[0].Bucket != bucketClientNamed || proposals[0].ArgName != "parent_arn" {
		t.Fatalf("got bucket=%s argName=%q, want bucketClientNamed/parent_arn", proposals[0].Bucket, proposals[0].ArgName)
	}
}

// TestApplySchemaFirstArgName_LeavesMultiAttributeEvidenceOnly: more than
// one required-for-import attribute is the identity-object-only shape
// (issue #105) render.go's bucketClientNamed renderer does not build -
// left evidence-only rather than mis-rendered as a single argument it is
// not.
func TestApplySchemaFirstArgName_LeavesMultiAttributeEvidenceOnly(t *testing.T) {
	proposals := []proposal{{TFType: "aws_example_composite", Bucket: bucketEvidenceOnly, NoCFNModel: true}}
	survey := map[string]surveyEntry{
		"aws_example_composite": {
			Type:     "aws_example_composite",
			Path:     surveyPathClientNamed,
			Identity: &surveyIdentity{RequiredForImport: []string{"a", "b"}},
		},
	}
	applySchemaFirstArgName(proposals, survey)

	if proposals[0].Bucket != bucketEvidenceOnly {
		t.Fatalf("bucket = %s, want unchanged %s", proposals[0].Bucket, bucketEvidenceOnly)
	}
}

// TestApplySchemaFirstArgName_LeavesOtherPathsEvidenceOnly: a schema-carrying
// type whose Path is anything but client-named or parent-derived (marker,
// account-derived, unique-name, the two dead-end tokens) is untouched - see
// evidenceschema.go's own doc comment for why each is excluded.
func TestApplySchemaFirstArgName_LeavesOtherPathsEvidenceOnly(t *testing.T) {
	for _, path := range []string{"marker", "account-derived", "unique-name", "enumerable, unbindable", "moves to Ops"} {
		proposals := []proposal{{TFType: "aws_example", Bucket: bucketEvidenceOnly, NoCFNModel: true}}
		survey := map[string]surveyEntry{
			"aws_example": {Type: "aws_example", Path: path, Identity: &surveyIdentity{RequiredForImport: []string{"name"}}},
		}
		applySchemaFirstArgName(proposals, survey)
		if proposals[0].Bucket != bucketEvidenceOnly {
			t.Errorf("path %q: bucket = %s, want unchanged %s", path, proposals[0].Bucket, bucketEvidenceOnly)
		}
	}
}

// TestApplySchemaFirstArgName_NeverTouchesOtherBuckets: only
// bucketEvidenceOnly rows are candidates - a row already client-named,
// server-assigned, or anything else is never revisited, so this pass can
// never override an argument name some other rule already settled.
func TestApplySchemaFirstArgName_NeverTouchesOtherBuckets(t *testing.T) {
	proposals := []proposal{{TFType: "aws_example", Bucket: bucketClientNamed, ArgName: "existing", ArgSource: argSourceCarveSeed}}
	survey := map[string]surveyEntry{
		"aws_example": {Type: "aws_example", Path: surveyPathClientNamed, Identity: &surveyIdentity{RequiredForImport: []string{"other"}}},
	}
	applySchemaFirstArgName(proposals, survey)
	if proposals[0].ArgName != "existing" || proposals[0].ArgSource != argSourceCarveSeed {
		t.Fatalf("a non-evidence-only row was mutated: %+v", proposals[0])
	}
}
