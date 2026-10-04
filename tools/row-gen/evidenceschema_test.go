// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "testing"

// TestApplySchemaFirstArgName_CoversSchemaAdmission is issue #428's core
// claim: a bucketEvidenceOnly row whose survey entry records admission
// "schema" with exactly one required-for-import attribute is promoted, with
// that attribute as ArgName and the #428 provenance marker.
func TestApplySchemaFirstArgName_CoversSchemaAdmission(t *testing.T) {
	proposals := []proposal{{TFType: "aws_example_thing", Bucket: bucketEvidenceOnly, NoCFNModel: true}}
	survey := map[string]surveyEntry{
		"aws_example_thing": {
			Type:      "aws_example_thing",
			Admission: admissionSchema,
			Identity:  &surveyIdentity{RequiredForImport: []string{"name"}},
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

// TestApplySchemaFirstArgName_LeavesMultiAttributeEvidenceOnly: more than
// one required-for-import attribute is the identity-object-only shape
// (issue #105) render.go's bucketClientNamed renderer does not build -
// left evidence-only rather than mis-rendered as a single argument it is
// not.
func TestApplySchemaFirstArgName_LeavesMultiAttributeEvidenceOnly(t *testing.T) {
	proposals := []proposal{{TFType: "aws_example_composite", Bucket: bucketEvidenceOnly, NoCFNModel: true}}
	survey := map[string]surveyEntry{
		"aws_example_composite": {
			Type:      "aws_example_composite",
			Admission: admissionSchema,
			Identity:  &surveyIdentity{RequiredForImport: []string{"a", "b"}},
		},
	}
	applySchemaFirstArgName(proposals, survey)

	if proposals[0].Bucket != bucketEvidenceOnly {
		t.Fatalf("bucket = %s, want unchanged %s", proposals[0].Bucket, bucketEvidenceOnly)
	}
}

// TestApplySchemaFirstArgName_LeavesUnprovenAdmissionEvidenceOnly: a
// schema-carrying type whose admission is anything but "schema" is
// untouched - the schemas do not prove its declaration names it.
func TestApplySchemaFirstArgName_LeavesUnprovenAdmissionEvidenceOnly(t *testing.T) {
	for _, admission := range []string{"", "needs-config-signal"} {
		proposals := []proposal{{TFType: "aws_example", Bucket: bucketEvidenceOnly, NoCFNModel: true}}
		survey := map[string]surveyEntry{
			"aws_example": {Type: "aws_example", Admission: admission, Identity: &surveyIdentity{RequiredForImport: []string{"name"}}},
		}
		applySchemaFirstArgName(proposals, survey)
		if proposals[0].Bucket != bucketEvidenceOnly {
			t.Errorf("admission %q: bucket = %s, want unchanged %s", admission, proposals[0].Bucket, bucketEvidenceOnly)
		}
	}
}

// TestApplySchemaFirstArgName_LeavesTableAssertedEvidenceOnly: a type the
// schemas prove but whose identity-table entry builds the identity with a
// cloud value is left alone - the account/region slot is a hand-ratified
// table fact the schema-first pass cannot build. aws_sagemaker_user_profile
// is the one such type at hashicorp/aws 6.59.0 (admission "schema" in
// live/survey-full.json, a Cloud-valued component in the table); the test
// checks that premise first so it fails loudly rather than passing vacuously
// when the table moves.
func TestApplySchemaFirstArgName_LeavesTableAssertedEvidenceOnly(t *testing.T) {
	const typeName = "aws_sagemaker_user_profile"
	if !tableAssertedBinding(typeName) {
		t.Fatalf("%s no longer carries a cloud-valued or unique-name binding in identity.DefaultTable; pick another table-asserted type for this test", typeName)
	}
	proposals := []proposal{{TFType: typeName, Bucket: bucketEvidenceOnly, NoCFNModel: true}}
	survey := map[string]surveyEntry{
		typeName: {Type: typeName, Admission: admissionSchema, Identity: &surveyIdentity{RequiredForImport: []string{"user_profile_name"}}},
	}
	applySchemaFirstArgName(proposals, survey)
	if proposals[0].Bucket != bucketEvidenceOnly {
		t.Fatalf("bucket = %s, want unchanged %s", proposals[0].Bucket, bucketEvidenceOnly)
	}
	if got := schemaGapClass(survey[typeName]); got != gapTierBTableAsserted {
		t.Errorf("schemaGapClass = %q, want %q", got, gapTierBTableAsserted)
	}
}

// TestApplySchemaFirstArgName_NeverTouchesOtherBuckets: only
// bucketEvidenceOnly rows are candidates - a row already client-named,
// server-assigned, or anything else is never revisited, so this pass can
// never override an argument name some other rule already settled.
func TestApplySchemaFirstArgName_NeverTouchesOtherBuckets(t *testing.T) {
	proposals := []proposal{{TFType: "aws_example", Bucket: bucketClientNamed, ArgName: "existing", ArgSource: argSourceCarveSeed}}
	survey := map[string]surveyEntry{
		"aws_example": {Type: "aws_example", Admission: admissionSchema, Identity: &surveyIdentity{RequiredForImport: []string{"other"}}},
	}
	applySchemaFirstArgName(proposals, survey)
	if proposals[0].ArgName != "existing" || proposals[0].ArgSource != argSourceCarveSeed {
		t.Fatalf("a non-evidence-only row was mutated: %+v", proposals[0])
	}
}
