// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"reflect"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file is corpus-alb-complete/test_plan's remaining wall
// (the foundation-order ruling's (#388) write half, applied to
// aws_lb_target_group_attachment): two lambda-target attachments in the real
// corpus refuse "Null identity argument" on port, because a lambda target
// group genuinely has no port and the ratified table's port component is
// (correctly, for an instance target) not [identity.Component.OmitIfAbsent].
// HANDOFF's fifth row: handling it would write a wrong marker (a fabricated
// port), so the instance drops to the record rung instead.
//
// targetGroupAttachmentSchema reproduces the type's real hashicorp/aws
// 6.59.0 wire identity schema, measured directly against the provider
// (pluginschema.ResourceTypes, no tofu in the loop, 2026-08-24):
// required_for_import = [target_group_arn, target_id],
// optional_for_import = [account_id, availability_zone, port, quic_server_id, region].
// Reduced to the attributes this mechanism reads, the same way
// route53RecordSchema above it in this package is.
func targetGroupAttachmentSchema() providers.Schema {
	return providers.Schema{
		Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"target_group_arn":  {Type: cty.String, Required: true},
			"target_id":         {Type: cty.String, Required: true},
			"port":              {Type: cty.Number, Optional: true, Computed: true},
			"availability_zone": {Type: cty.String, Optional: true, Computed: true},
			"quic_server_id":    {Type: cty.String, Optional: true, Computed: true},
			"id":                {Type: cty.String, Computed: true},
		}},
		IdentitySchema: &configschema.Object{
			Nesting: configschema.NestingSingle,
			Attributes: map[string]*configschema.Attribute{
				"target_group_arn":  {Type: cty.String, Required: true},
				"target_id":         {Type: cty.String, Required: true},
				"account_id":        {Type: cty.String, Optional: true},
				"availability_zone": {Type: cty.String, Optional: true},
				"port":              {Type: cty.Number, Optional: true},
				"quic_server_id":    {Type: cty.String, Optional: true},
				"region":            {Type: cty.String, Optional: true},
			},
		},
	}
}

// TestLocatedRecordFromLambdaTargetPortNullIsRecordedWithoutPort is the
// unit's own defect, closed: a lambda-target attachment's applied object -
// port genuinely null, the real shape floci and real AWS both return for
// this target_type - is recorded through the wire identity schema's
// Composite() route with its two required components and NO fabricated
// port, rather than refused.
func TestLocatedRecordFromLambdaTargetPortNullIsRecordedWithoutPort(t *testing.T) {
	if _, recordable := identity.LocatedIdentityPlanFor("aws_lb_target_group_attachment", targetGroupAttachmentSchema()); !recordable {
		t.Fatal("LocatedIdentityPlanFor refused this type; this test's premise (the composite route answers it) no longer holds")
	}

	obj := cty.ObjectVal(map[string]cty.Value{
		"target_group_arn":  cty.StringVal("arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/lambda-tg/abc123"),
		"target_id":         cty.StringVal("arn:aws:lambda:us-east-1:123456789012:function:my-function"),
		"port":              cty.NullVal(cty.Number),
		"availability_zone": cty.NullVal(cty.String),
		"quic_server_id":    cty.NullVal(cty.String),
		"id":                cty.StringVal("arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/lambda-tg/abc123_arn:aws:lambda:us-east-1:123456789012:function:my-function"),
	})

	rec, ok := LocatedRecordFrom("aws_lb_target_group_attachment", targetGroupAttachmentSchema(), obj)
	if !ok {
		t.Fatal("LocatedRecordFrom refused a lambda-target instance whose two required components (target_group_arn, target_id) are both present - a null, genuinely-absent port must never be a reason to withhold the record")
	}
	want := map[string]string{
		"target_group_arn": "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/lambda-tg/abc123",
		"target_id":        "arn:aws:lambda:us-east-1:123456789012:function:my-function",
	}
	if !reflect.DeepEqual(rec.Components, want) {
		t.Errorf("Components = %v, want %v - no \"port\" key at all, not an empty-string or zero placeholder, which would be exactly the fabricated-marker failure HANDOFF's safety rule forbids", rec.Components, want)
	}
	if rec.ImportID != "" {
		t.Errorf("ImportID = %q, want \"\" - this type's identity is the wire identity OBJECT, never a flattened string (issue #105)", rec.ImportID)
	}
}

// TestLocatedRecordFromInstanceTargetPortIsRecordedAsDisambiguator is the
// boundary the lambda fix must not cost: an ordinary instance-target
// attachment's port IS present, and recording it - even though the wire
// schema marks it merely optional - is what keeps two attachments of the
// SAME target_group_arn/target_id at two different ports (a real, supported
// AWS shape: one target registered to a target group at more than one port)
// from reading back as the identical record. This is the mutation this
// unit's own OptionalComponents plumbing exists to prevent: a required-only
// record would pass the lambda test above just as well while silently
// losing this disambiguator for every OTHER instance of the type, lambda or
// not.
func TestLocatedRecordFromInstanceTargetPortIsRecordedAsDisambiguator(t *testing.T) {
	obj := cty.ObjectVal(map[string]cty.Value{
		"target_group_arn":  cty.StringVal("arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/inst-tg/def456"),
		"target_id":         cty.StringVal("i-0123456789abcdef0"),
		"port":              cty.NumberIntVal(80),
		"availability_zone": cty.NullVal(cty.String),
		"quic_server_id":    cty.NullVal(cty.String),
		"id":                cty.StringVal("whatever"),
	})

	rec, ok := LocatedRecordFrom("aws_lb_target_group_attachment", targetGroupAttachmentSchema(), obj)
	if !ok {
		t.Fatal("LocatedRecordFrom refused an instance-target attachment with every component present")
	}
	want := map[string]string{
		"target_group_arn": "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/inst-tg/def456",
		"target_id":        "i-0123456789abcdef0",
		"port":             "80",
	}
	if !reflect.DeepEqual(rec.Components, want) {
		t.Errorf("Components = %v, want %v - port must ride along whenever the object actually carries one, rendered as the plain decimal an import string would use, not dropped just because the wire schema marks it optional", rec.Components, want)
	}

	// A second attachment of the SAME target at a DIFFERENT port must record
	// a DIFFERENT identity - the collision this test's own doc comment
	// names, made concrete.
	obj2 := cty.ObjectVal(map[string]cty.Value{
		"target_group_arn":  cty.StringVal("arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/inst-tg/def456"),
		"target_id":         cty.StringVal("i-0123456789abcdef0"),
		"port":              cty.NumberIntVal(9090),
		"availability_zone": cty.NullVal(cty.String),
		"quic_server_id":    cty.NullVal(cty.String),
		"id":                cty.StringVal("whatever-2"),
	})
	rec2, ok2 := LocatedRecordFrom("aws_lb_target_group_attachment", targetGroupAttachmentSchema(), obj2)
	if !ok2 {
		t.Fatal("LocatedRecordFrom refused the second attachment")
	}
	if reflect.DeepEqual(rec.Components, rec2.Components) {
		t.Fatalf("two attachments of the same target at ports 80 and 9090 recorded the IDENTICAL identity %v - a record collision between two distinct live objects, exactly the wrong-marker shape HANDOFF's safety rule forbids", rec.Components)
	}
}

// TestLocatedRecordFromTargetGroupAttachmentMissingRequiredComponentRefuses
// is the boundary on the REQUIRED half: target_id absent must still refuse
// the whole record, port or no port - [LocatedIdentityOptional]'s
// never-refuses-the-whole rule applies only to OptionalComponents, and must
// never bleed into Components' own all-or-nothing rule.
func TestLocatedRecordFromTargetGroupAttachmentMissingRequiredComponentRefuses(t *testing.T) {
	obj := cty.ObjectVal(map[string]cty.Value{
		"target_group_arn":  cty.StringVal("arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/inst-tg/def456"),
		"target_id":         cty.NullVal(cty.String),
		"port":              cty.NumberIntVal(80),
		"availability_zone": cty.NullVal(cty.String),
		"quic_server_id":    cty.NullVal(cty.String),
		"id":                cty.StringVal("whatever"),
	})
	if rec, ok := LocatedRecordFrom("aws_lb_target_group_attachment", targetGroupAttachmentSchema(), obj); ok {
		t.Fatalf("LocatedRecordFrom recorded %+v for an object with no target_id, a required component - a present, disambiguating port must never paper over a missing required one", rec)
	}
}
