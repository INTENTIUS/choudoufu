// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"strings"
	"testing"
)

// TestCrossCheckRefusesThePhantomECSServiceArn is GitHub issue #106's proven
// victim, with the real evidence from live/import-grammar.json.
//
// The provider's identity schema says aws_ecs_service is identified by
// ["cluster", "name"] and the scraped documentation agrees. Rule 5 matched
// the CloudFormation Registry's primaryIdentifier of ["ServiceArn",
// "Cluster"] instead and proposed service_arn, which is not an argument of
// the type at all. Two authoritative sources agreed, a third derived source
// disagreed, and the wrong one won because nothing compared them.
func TestCrossCheckRefusesThePhantomECSServiceArn(t *testing.T) {
	g := importGrammarRow{
		TFType: "aws_ecs_service",
		ArgumentReference: []argumentRefEntry{
			{Name: "name", Required: true},
			{Name: "cluster"},
			{Name: "task_definition"},
			{Name: "desired_count"},
		},
		IdentitySchemaRequired: []string{"cluster", "name"},
		IdentitySchemaOptional: []string{"account_id", "region"},
	}
	p := &proposal{
		TFType:        "aws_ecs_service",
		Bucket:        bucketComposite,
		CompositeArgs: []string{"cluster", "service_arn"},
		CompositeSep:  "/",
	}

	applyIdentitySchemaCrossCheck(p, g)

	if p.Bucket != bucketNeedsHandSeparator {
		t.Errorf("bucket = %v, want %v: a proposal naming an argument no source knows must not be emitted", p.Bucket, bucketNeedsHandSeparator)
	}
	if p.CompositeArgs != nil {
		t.Errorf("the refused composite kept its arguments: %v", p.CompositeArgs)
	}
	if len(p.CrossCheck) == 0 {
		t.Fatal("the demotion recorded no reason, so the printed block would go quiet about it")
	}
	if !strings.Contains(p.CrossCheck[0].Detail, "service_arn") {
		t.Errorf("the finding does not name the phantom argument: %s", p.CrossCheck[0].Detail)
	}
}

// TestCrossCheckSparesAnIncompleteArgumentReference is the false positive the
// first version of this check shipped, caught by the convergence artifact.
//
// aws_s3control_multi_region_access_point's scraped Argument Reference has
// three entries and does not include "name" - while its Identity Schema says
// "name" is exactly what identifies it. A check that treated the partial
// list as complete demoted twenty-two rows and turned this one from matching
// its ratified row to mismatching it.
func TestCrossCheckSparesAnIncompleteArgumentReference(t *testing.T) {
	g := importGrammarRow{
		TFType: "aws_s3control_multi_region_access_point",
		ArgumentReference: []argumentRefEntry{
			{Name: "account_id"},
			{Name: "details"},
			{Name: "region"},
		},
		IdentitySchemaRequired: []string{"name"},
		IdentitySchemaOptional: []string{"account_id", "region"},
	}
	p := &proposal{
		TFType:        "aws_s3control_multi_region_access_point",
		Bucket:        bucketComposite,
		CompositeArgs: []string{"account_id", "name"},
		CompositeSep:  ":",
	}

	applyIdentitySchemaCrossCheck(p, g)

	if p.Bucket != bucketComposite {
		t.Errorf("bucket = %v, want it left alone: \"name\" is absent from an incomplete Argument Reference but present in the Identity Schema, which is what makes it real", p.Bucket)
	}
	for _, f := range p.CrossCheck {
		if f.Kind == "not-an-argument" {
			t.Errorf("a real argument was called a phantom: %s", f.Detail)
		}
	}
}

// TestCrossCheckNeedsBothSources: with no Identity Schema there is one
// incomplete list and nothing to check it against, so the check declines.
// Absence of evidence is not evidence, and this is where the first version
// went wrong.
func TestCrossCheckNeedsBothSources(t *testing.T) {
	g := importGrammarRow{
		TFType:            "aws_iot_thing",
		ArgumentReference: []argumentRefEntry{{Name: "region"}, {Name: "name"}},
	}
	p := &proposal{
		TFType:        "aws_iot_thing",
		Bucket:        bucketComposite,
		CompositeArgs: []string{"name", "thing_type_name"},
	}

	applyIdentitySchemaCrossCheck(p, g)

	if p.Bucket != bucketComposite {
		t.Errorf("bucket = %v, want it left alone: with no Identity Schema there is nothing corroborating the argument list", p.Bucket)
	}
	if len(p.CrossCheck) != 0 {
		t.Errorf("a finding was recorded with only one source to read: %+v", p.CrossCheck)
	}
}

// TestCrossCheckLeavesSingleArgumentProposalsAlone: resolveArgName already
// ranks the identity schema first for those, and second-guessing it here is
// where most of the first version's false positives came from.
func TestCrossCheckLeavesSingleArgumentProposalsAlone(t *testing.T) {
	g := importGrammarRow{
		TFType:                 "aws_something",
		ArgumentReference:      []argumentRefEntry{{Name: "other"}},
		IdentitySchemaRequired: []string{"other"},
	}
	p := &proposal{TFType: "aws_something", Bucket: bucketClientNamed, ArgName: "name"}

	applyIdentitySchemaCrossCheck(p, g)

	if p.Bucket != bucketClientNamed {
		t.Errorf("bucket = %v, want it left alone", p.Bucket)
	}
	if len(p.CrossCheck) != 0 {
		t.Errorf("a single-argument proposal was cross-checked: %+v", p.CrossCheck)
	}
}
