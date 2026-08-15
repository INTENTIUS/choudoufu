// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"reflect"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// The templates below mirror what tools/importdocs-gen actually writes for
// the named types (verified against the regenerated live/import-grammar.json
// on 2026-08-15), inlined so these tests state their evidence rather than
// depending on the committed artifact's current contents.

func reportGroupTemplate() *idTemplate {
	return &idTemplate{Kind: "arn", Segments: []idTemplateSegment{
		{Literal: "arn:aws:codebuild:"}, {Cloud: "region"}, {Literal: ":"}, {Cloud: "account-id"},
		{Literal: ":report-group/"}, {Argument: "name", AttributedBy: attrByPlaceholderName},
	}}
}

func ramPermissionTemplate() *idTemplate {
	return &idTemplate{Kind: "arn", Segments: []idTemplateSegment{
		{Literal: "arn:aws:ram:"}, {Cloud: "region"}, {Literal: ":"}, {Cloud: "account-id"},
		{Literal: ":permission/"}, {Argument: "name", AttributedBy: "self-placeholder-required-name"},
	}}
}

// A rule-1 server-assigned proposal is overturned only by own-text
// attribution: the codebuild_report_group shape ("report-group-name" spells
// `name`) fires; the aws_ram_permission shape (a self-placeholder over a
// Required name, contextual tier) must NOT - it is evidence-identical to
// aws_sns_topic on every pinned source while ratified the other way, and
// letting the contextual tier overturn rule-1 flips 16 adopted
// server-assigned rows (measured 2026-08-15, the counterexample
// tryAssembledTemplate's doc comment records).
func TestTryAssembledTemplate_RuleOneServerAssignedNeedsOwnText(t *testing.T) {
	fires := proposal{TFType: "aws_codebuild_report_group", Bucket: bucketServerAssigned}
	if !tryAssembledTemplate(&fires, importGrammarRow{IDTemplate: reportGroupTemplate()}) {
		t.Fatal("own-text template did not overturn the rule-1 server-assigned proposal")
	}
	if fires.Bucket != bucketAssembled {
		t.Fatalf("bucket = %s, want %s", fires.Bucket, bucketAssembled)
	}

	refuses := proposal{TFType: "aws_ram_permission", Bucket: bucketServerAssigned}
	if tryAssembledTemplate(&refuses, importGrammarRow{IDTemplate: ramPermissionTemplate()}) {
		t.Fatal("a contextual-tier template overturned a rule-1 server-assigned proposal; the aws_ram_permission counterexample forbids exactly this")
	}
	if refuses.Bucket != bucketServerAssigned {
		t.Fatalf("bucket = %s, want it left at %s", refuses.Bucket, bucketServerAssigned)
	}
}

// Where the registry left the row unresolved there is no standing claim to
// defeat, and the contextual tier suffices (aws_kinesis_firehose_delivery_
// stream's shape: client-named by "arn", which is no configuration argument
// at all).
func TestTryAssembledTemplate_UnresolvedRowsAcceptContextualTier(t *testing.T) {
	p := proposal{TFType: "aws_ram_permission", Bucket: bucketNeedsHandSeparator}
	if !tryAssembledTemplate(&p, importGrammarRow{IDTemplate: ramPermissionTemplate()}) {
		t.Fatal("contextual-tier template did not resolve a needs-hand-separator proposal")
	}

	clientNamedPhantom := proposal{TFType: "aws_kinesis_firehose_delivery_stream", Bucket: bucketClientNamed, ArgName: "arn"}
	g := importGrammarRow{
		IDTemplate: &idTemplate{Kind: "arn", Segments: []idTemplateSegment{
			{Literal: "arn:aws:firehose:"}, {Cloud: "region"}, {Literal: ":"}, {Cloud: "account-id"},
			{Literal: ":deliverystream/"}, {Argument: "name", AttributedBy: "self-placeholder-required-name"},
		}},
		ArgumentReference: []argumentRefEntry{{Name: "name", Required: true}},
	}
	if !tryAssembledTemplate(&clientNamedPhantom, g) {
		t.Fatal("template did not resolve a client-named proposal whose argument is not documented")
	}

	clientNamedReal := proposal{TFType: "aws_codebuild_fleet", Bucket: bucketClientNamed, ArgName: "name"}
	if tryAssembledTemplate(&clientNamedReal, g) {
		t.Fatal("template overturned a client-named proposal whose argument IS documented; that row was already resolved")
	}
}

// Any unattributed segment, a template with no Cloud slot, or a tail that
// does not end in an argument refuses outright, whatever the bucket.
func TestTryAssembledTemplate_FailClosed(t *testing.T) {
	unattributed := &idTemplate{Kind: "arn", Segments: []idTemplateSegment{
		{Literal: "arn:aws:sns:"}, {Cloud: "region"}, {Literal: ":"}, {Cloud: "account-id"},
		{Literal: ":"}, {Unattributed: "my-topic"},
	}}
	noCloud := &idTemplate{Kind: "url", Segments: []idTemplateSegment{
		{Literal: "https://queue.amazonaws.com/"}, {Argument: "name", AttributedBy: attrByPlaceholderName},
	}}
	opaqueTail := &idTemplate{Kind: "arn", Segments: []idTemplateSegment{
		{Literal: "arn:aws:x:"}, {Cloud: "region"}, {Literal: ":"}, {Cloud: "account-id"},
		{Argument: "name", AttributedBy: attrByPlaceholderName}, {Literal: "/"},
	}}
	for name, tpl := range map[string]*idTemplate{"unattributed": unattributed, "no-cloud-slot": noCloud, "opaque-tail": opaqueTail} {
		p := proposal{TFType: "aws_example", Bucket: bucketNeedsHandSeparator}
		if tryAssembledTemplate(&p, importGrammarRow{IDTemplate: tpl}) {
			t.Errorf("%s: template fired, want refusal", name)
		}
		if p.Bucket != bucketNeedsHandSeparator {
			t.Errorf("%s: bucket = %s, want untouched", name, p.Bucket)
		}
	}
}

// proposedFields renders an assembled proposal into the exact Component
// shape the ratified table spells for this class - identityattr.go's
// derived IdentityAttr on every component included. Pinned against the real
// ratified rows so the byte-for-byte convergence claim is tested here, not
// only measured in the artifact.
func TestProposedFields_AssembledMatchesRatifiedComponents(t *testing.T) {
	for _, tf := range []string{
		"aws_codebuild_report_group",
		"aws_sagemaker_space",
		"aws_cloudfront_realtime_log_config",
		"aws_kinesis_firehose_delivery_stream",
	} {
		ratified, ok := identity.DefaultTable[tf]
		if !ok {
			t.Fatalf("%s: not in DefaultTable", tf)
		}
		var segs []idTemplateSegment
		for _, c := range ratified.Components {
			switch {
			case c.Cloud != "":
				segs = append(segs, idTemplateSegment{Cloud: string(c.Cloud)})
			case len(c.Attrs) == 1:
				segs = append(segs, idTemplateSegment{Argument: c.Attrs[0], AttributedBy: attrByPlaceholderName})
			default:
				segs = append(segs, idTemplateSegment{Literal: c.Literal})
			}
		}
		p := proposal{TFType: tf, Bucket: bucketAssembled, Assembled: segs}
		serverAssigned, components, _, _, claimed := proposedFields(p)
		if serverAssigned || claimed {
			t.Errorf("%s: serverAssigned=%v claimedAttrs=%v, want false/false", tf, serverAssigned, claimed)
		}
		if !reflect.DeepEqual(components, ratified.Components) {
			t.Errorf("%s: components differ from the ratified row\n got %+v\nwant %+v", tf, components, ratified.Components)
		}
	}
}
