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

// The grammar rows below mirror what tools/importdocs-gen actually writes for
// the named types (verified against the regenerated live/import-grammar.json
// at the 6.59.0 pin), inlined so these tests state their evidence rather than
// depending on the committed artifact's current contents. The one test that
// does read the artifact says so in its own name.

func boolPtr(b bool) *bool { return &b }

// vpcBlockPublicAccessRow is the harder of the two admitted rows: its own
// documented sole ID part is the token "aws_region", which suffix-matches the
// `region` argument, and only that argument's cloud_default tells the two
// readings apart.
func vpcBlockPublicAccessRow() importGrammarRow {
	return importGrammarRow{
		TFType:           "aws_vpc_block_public_access_options",
		ImportIDExample:  "us-east-1",
		SoleIDCloudValue: "region",
		SoleIDPart:       &idPart{Token: "aws_region", Source: "attribute"},
		ArgumentReference: []argumentRefEntry{
			{Name: "region", CloudDefault: "region"},
			{Name: "internet_gateway_block_mode", Required: true},
		},
	}
}

// organizationsAccountRow is the type the rule must not reach: a twelve-digit
// example, a sole ID part the scrape reads as the resource's own, and - the
// clause that actually settles it - an identity schema requiring `id`.
func organizationsAccountRow() importGrammarRow {
	return importGrammarRow{
		TFType:                 "aws_organizations_account",
		ImportIDExample:        "111111111111",
		ComposedOfArguments:    boolPtr(false),
		IdentitySchemaRequired: []string{"id"},
		IdentitySchemaOptional: []string{"account_id"},
		SoleIDPart:             &idPart{Token: "account_id", Source: idPartSourceOwnID},
		ArgumentReference: []argumentRefEntry{
			{Name: "email", Required: true}, {Name: "name", Required: true},
		},
	}
}

func TestTryCloudSingletonID_ResolvesTheRegionSingleton(t *testing.T) {
	p := proposal{TFType: "aws_vpc_block_public_access_options", Bucket: bucketServerAssigned}
	if !tryCloudSingletonID(&p, vpcBlockPublicAccessRow()) {
		t.Fatal("the region singleton was not resolved")
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
	want := []identity.Component{{Cloud: identity.CloudRegion}}
	if !reflect.DeepEqual(components, want) {
		t.Errorf("components = %#v, want %#v", components, want)
	}
	if importSyntax != "REGION" {
		t.Errorf("ImportSyntax = %q, want %q", importSyntax, "REGION")
	}
}

// TestTryCloudSingletonID_RefusalsEachDoWork removes exactly one clause's
// evidence at a time from a row the rule otherwise fires on, and asserts each
// removal alone is enough to refuse. A refusal that never fires is a comment,
// not a check.
func TestTryCloudSingletonID_RefusalsEachDoWork(t *testing.T) {
	for name, mutate := range map[string]func(*importGrammarRow){
		"a schemed template belongs to tryAssembledTemplate": func(g *importGrammarRow) {
			g.IDTemplate = &idTemplate{Kind: "arn", Segments: []idTemplateSegment{
				{Literal: "arn:aws:ec2:"}, {Cloud: "region"}, {Literal: ":x"},
			}}
		},
		"the doc names the arguments the ID composes from": func(g *importGrammarRow) {
			g.ComposedOfArguments = boolPtr(true)
			g.Arguments = []string{"region"}
		},
		"the identity schema requires an attribute of the resource's own": func(g *importGrammarRow) {
			g.IdentitySchemaRequired = []string{"id"}
		},
		"the sole ID part names an argument with a disagreeing cloud default": func(g *importGrammarRow) {
			g.ArgumentReference = []argumentRefEntry{{Name: "region"}} // no cloud_default
		},
		"the example is not a cloud value at all": func(g *importGrammarRow) {
			g.SoleIDCloudValue = ""
		},
	} {
		t.Run(name, func(t *testing.T) {
			g := vpcBlockPublicAccessRow()
			mutate(&g)
			p := proposal{TFType: g.TFType, Bucket: bucketServerAssigned}
			if tryCloudSingletonID(&p, g) {
				t.Fatalf("the rule fired anyway; bucket = %s", p.Bucket)
			}
			if p.Bucket != bucketServerAssigned {
				t.Errorf("bucket = %s, want it left at %s", p.Bucket, bucketServerAssigned)
			}
		})
	}
}

// TestTryCloudSingletonID_OrganizationsAccountIsOutsideTheRuleTwiceOver is the
// counterexample the scouting pass named: the CFN registry's primary
// identifier is ["AccountId"] on this type and on
// aws_vpc_block_public_access_options alike, and the doc's own sole ID part
// is "own-id" on one and "attribute" on the other, so neither source splits
// them. Two independent clauses do, and both are asserted here because
// removing either one alone must still refuse.
func TestTryCloudSingletonID_OrganizationsAccountIsOutsideTheRuleTwiceOver(t *testing.T) {
	g := organizationsAccountRow()

	p := proposal{TFType: g.TFType, Bucket: bucketServerAssigned}
	if tryCloudSingletonID(&p, g) {
		t.Fatal("aws_organizations_account was resolved as a cloud singleton")
	}

	// Clause one alone: even given a region example and no identity schema at
	// all, the ratified server-assigned row standing in DefaultTable refuses
	// it. This is the tier, and it reads the shipped table rather than the
	// type name.
	if !identity.DefaultTable[g.TFType].ServerAssigned {
		t.Fatal("aws_organizations_account is no longer ratified server-assigned; this test's premise is stale")
	}
	noSchema := g
	noSchema.IdentitySchemaRequired = nil
	noSchema.SoleIDCloudValue = "region"
	noSchema.ImportIDExample = "us-east-1"
	q := proposal{TFType: g.TFType, Bucket: bucketServerAssigned}
	if tryCloudSingletonID(&q, noSchema) {
		t.Error("the ratified server-assigned row did not refuse the rule on its own")
	}

	// Clause two alone: on a type with no ratified row at all, the identity
	// schema's required attribute still refuses.
	unadmitted := g
	unadmitted.TFType = "aws_not_in_the_table_at_all"
	unadmitted.SoleIDCloudValue = "region"
	unadmitted.ImportIDExample = "us-east-1"
	r := proposal{TFType: unadmitted.TFType, Bucket: bucketServerAssigned}
	if tryCloudSingletonID(&r, unadmitted) {
		t.Error("a required identity attribute did not refuse the rule on its own")
	}
}

// TestTryCloudSingletonID_MayNotOverturnARatifiedServerAssignedRow is the
// tiering stated positively: a ratified row that makes NO server-assigned
// claim is fair game, which is what lets the fresh proposal reproduce
// aws_arczonalshift_autoshift_observer_notification_status and retire that
// type's annotations.json ruling.
func TestTryCloudSingletonID_MayNotOverturnARatifiedServerAssignedRow(t *testing.T) {
	const arc = "aws_arczonalshift_autoshift_observer_notification_status"
	row, ok := identity.DefaultTable[arc]
	if !ok || row.ServerAssigned {
		t.Fatalf("%s is not the ratified non-server-assigned row this test is about", arc)
	}
	g := importGrammarRow{
		TFType:                 arc,
		ImportIDExample:        "us-east-1",
		SoleIDCloudValue:       "region",
		IdentitySchemaOptional: []string{"account_id", "region"},
		ArgumentReference: []argumentRefEntry{
			{Name: "status", Required: true}, {Name: "region", CloudDefault: "region"},
		},
	}
	p := proposal{TFType: arc, Bucket: bucketServerAssigned}
	if !tryCloudSingletonID(&p, g) {
		t.Fatal("the rule refused a ratified row that makes no server-assigned claim")
	}
	if _, _, importSyntax, _, _ := proposedFields(p); importSyntax != row.ImportSyntax {
		t.Errorf("proposed ImportSyntax %q, ratified %q", importSyntax, row.ImportSyntax)
	}
}

// TestCloudSingletonReachOverTheCommittedArtifact is the measurement, pinned.
//
// The rule is derived and names no type, so what it reaches is a fact about
// the 6.59.0 doc cache rather than a list anyone chose - but a rule whose
// reach nobody records is a rule whose next widening is invisible. This pins
// the set at the size and membership measured when the rule landed. Two of
// these twenty are admitted in DefaultTable by this batch, one
// (aws_arczonalshift_...) was already ratified and is now reproduced, and the
// other seventeen are proposals no ratification batch has reached.
//
// A doc-pin bump that moves this list is expected to move it; read the diff
// and re-pin. A code change that moves it is the thing this test exists for.
func TestCloudSingletonReachOverTheCommittedArtifact(t *testing.T) {
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
		if p.Bucket == bucketAssembled && len(p.Assembled) == 1 && p.Assembled[0].Cloud != "" {
			got = append(got, p.TFType)
		}
	}
	sort.Strings(got)

	want := []string{
		"aws_apprunner_default_auto_scaling_configuration_version",
		"aws_arczonalshift_autoshift_observer_notification_status",
		"aws_auditmanager_account_registration",
		"aws_bedrock_model_invocation_logging_configuration",
		"aws_cloudwatch_otel_enrichment",
		"aws_devopsguru_event_sources_config",
		"aws_devopsguru_service_integration",
		"aws_ec2_allowed_images_settings",
		"aws_glue_resource_policy",
		"aws_iot_event_configurations",
		"aws_kinesis_account_settings",
		"aws_macie2_classification_export_configuration",
		"aws_observabilityadmin_telemetry_enrichment",
		"aws_observabilityadmin_telemetry_evaluation",
		"aws_observabilityadmin_telemetry_evaluation_for_organization",
		"aws_sagemaker_servicecatalog_portfolio_status",
		"aws_servicequotas_auto_management",
		"aws_vpc_block_public_access_options",
		"aws_xray_encryption_config",
		"aws_xray_trace_segment_destination",
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
