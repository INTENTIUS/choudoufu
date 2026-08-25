// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// TestComponentsFromValueMatchesDocumentedImportSyntax is the ordinary case,
// against a REAL ratified row (aws_lb_target_group_attachment - the exact
// type corpus-alb-complete's remaining test_plan wall names): every
// component present, joined exactly the way the provider's own documented
// import syntax (see table_generated.go's ImportSyntax field for this row)
// says it must be.
func TestComponentsFromValueMatchesDocumentedImportSyntax(t *testing.T) {
	row, ok := LookupType("aws_lb_target_group_attachment")
	if !ok {
		t.Fatal("aws_lb_target_group_attachment is not in DefaultTable; this test needs a real ratified row")
	}

	val := cty.ObjectVal(map[string]cty.Value{
		"target_group_arn":  cty.StringVal("arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/example/0123456789abcdef"),
		"target_id":         cty.StringVal("i-0123456789abcdef0"),
		"port":              cty.NumberIntVal(80),
		"availability_zone": cty.NullVal(cty.String),
		"quic_server_id":    cty.NullVal(cty.String),
	})

	importID, values, ok := ComponentsFromValue(row, val)
	if !ok {
		t.Fatalf("ComponentsFromValue reported not-found for a fully-populated instance")
	}
	wantID := "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/example/0123456789abcdef,i-0123456789abcdef0,80"
	if importID != wantID {
		t.Errorf("importID = %q, want %q", importID, wantID)
	}
	if values["target_group_arn"] != "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/example/0123456789abcdef" {
		t.Errorf("values[target_group_arn] = %q", values["target_group_arn"])
	}
	if values["port"] != "80" {
		t.Errorf("values[port] = %q, want \"80\"", values["port"])
	}
	if _, present := values["availability_zone"]; present {
		t.Errorf("availability_zone should be absent (OmitIfAbsent, null in configuration), got %q", values["availability_zone"])
	}
}

// TestComponentsFromValuePortNullOmits is issue #399's maintainer ruling,
// applied to the node-seam's evaluated-value resolver the same way
// TestTargetGroupAttachmentPortOmitIfAbsent (identity_test.go's sibling
// file) applies it to the static one: a Lambda target genuinely has no
// port in real AWS (botocore's elbv2 2015-12-01 model documents
// TargetDescription.Port and CreateTargetGroupInput.Port as both not
// applying to a Lambda-type target), and a lambda target group holds one
// target and no port, so two attachments differing only by port is
// structurally impossible for that shape - the collision OmitIfAbsent's
// safety margin exists for cannot occur here. Before this ruling the port
// component carried no OmitIfAbsent and this test pinned the refusal that
// produced (git history: TestComponentsFromValuePortNullIsNotFound); the
// row now carries it, so a null port omits the segment instead, the same
// two-field form the static evaluator renders.
func TestComponentsFromValuePortNullOmits(t *testing.T) {
	row, _ := LookupType("aws_lb_target_group_attachment")

	val := cty.ObjectVal(map[string]cty.Value{
		"target_group_arn":  cty.StringVal("arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/example/0123456789abcdef"),
		"target_id":         cty.StringVal("lambda-arn"),
		"port":              cty.NullVal(cty.Number),
		"availability_zone": cty.NullVal(cty.String),
		"quic_server_id":    cty.NullVal(cty.String),
	})

	importID, values, ok := ComponentsFromValue(row, val)
	if !ok {
		t.Fatalf("ComponentsFromValue reported not-found for a lambda-target instance whose two required components are both present")
	}
	wantID := "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/example/0123456789abcdef,lambda-arn"
	if importID != wantID {
		t.Errorf("importID = %q, want %q - the two-field form, no trailing separator where port used to sit", importID, wantID)
	}
	if _, present := values["port"]; present {
		t.Errorf("port should be absent from values (OmitIfAbsent, null), got %q", values["port"])
	}
}

// TestComponentsFromValueAlternation is aws_route's three-way alternation:
// only one of destination_cidr_block/destination_ipv6_cidr_block/
// destination_prefix_list_id is ever set, and the first one present wins.
func TestComponentsFromValueAlternation(t *testing.T) {
	row, ok := LookupType("aws_route")
	if !ok {
		t.Fatal("aws_route is not in DefaultTable")
	}

	val := cty.ObjectVal(map[string]cty.Value{
		"route_table_id":              cty.StringVal("rtb-0123456789abcdef0"),
		"destination_cidr_block":      cty.NullVal(cty.String),
		"destination_ipv6_cidr_block": cty.StringVal("::/0"),
		"destination_prefix_list_id":  cty.NullVal(cty.String),
	})

	importID, values, ok := ComponentsFromValue(row, val)
	if !ok {
		t.Fatalf("ComponentsFromValue reported not-found")
	}
	if want := "rtb-0123456789abcdef0_::/0"; importID != want {
		t.Errorf("importID = %q, want %q", importID, want)
	}
	if values["destination_ipv6_cidr_block"] != "::/0" {
		t.Errorf("values[destination_ipv6_cidr_block] = %q", values["destination_ipv6_cidr_block"])
	}
	if _, present := values["destination_cidr_block"]; present {
		t.Errorf("destination_cidr_block was not set in configuration and must not appear in values")
	}
}

// TestComponentsFromValueUnknownIsNotFound: a value this run's own graph
// walk has not resolved yet (the ordinary "depends on a resource not yet
// applied" case) must never be treated as absent - it is not yet known,
// which [Component.OmitIfAbsent]'s own doc comment is explicit about being
// a different fact from "the argument was omitted."
func TestComponentsFromValueUnknownIsNotFound(t *testing.T) {
	row, _ := LookupType("aws_route")

	val := cty.ObjectVal(map[string]cty.Value{
		"route_table_id":              cty.UnknownVal(cty.String),
		"destination_cidr_block":      cty.StringVal("10.0.0.0/16"),
		"destination_ipv6_cidr_block": cty.NullVal(cty.String),
		"destination_prefix_list_id":  cty.NullVal(cty.String),
	})

	if _, _, ok := ComponentsFromValue(row, val); ok {
		t.Fatalf("expected not-found: route_table_id is unknown, not absent")
	}
}

// TestComponentsFromValueUnrelatedUnknownAttributeIsIgnored is
// corpus-hongbomiao-labelbox's own greenfield wall (GitHub issue #388's
// plan-node seam): module.labelbox_iam_role.aws_iam_role_policy's `role`
// (a same-module sibling's `name`, a plain literal, so known at plan time)
// and `name` (its own literal) are both known - table_generated.go's row
// for aws_iam_role_policy names only those two components - but its THIRD
// argument, `policy`, is a jsonencode() over a cross-module reference into
// a sibling module's `aws_s3_bucket.id`, a Computed-only attribute that is
// genuinely unknown until that bucket is created. Before this fix,
// [ComponentsFromValue]'s own top-level `!val.IsWhollyKnown()` gate looked
// at the WHOLE config object rather than only the attributes
// t.Components actually reads, so this unrelated, not-yet-known `policy`
// argument vetoed a derivation that never needed it, and
// projection.NodeResolver.ResolveResourceIdentity reported "No source for
// this instance's identity" for an instance whose identity was fully
// determined. Reproduced directly against a real greenfield apply (no
// choudoufu-authored config, hongbo-miao/hongbomiao.com's own unmodified
// module) before this test existed.
func TestComponentsFromValueUnrelatedUnknownAttributeIsIgnored(t *testing.T) {
	row, ok := LookupType("aws_iam_role_policy")
	if !ok {
		t.Fatal("aws_iam_role_policy is not in DefaultTable; this test needs the real ratified row")
	}

	val := cty.ObjectVal(map[string]cty.Value{
		"id":          cty.NullVal(cty.String),
		"name":        cty.StringVal("LabelboxRoleS3Policy-hm-labelbox"),
		"name_prefix": cty.NullVal(cty.String),
		"policy":      cty.UnknownVal(cty.String),
		"role":        cty.StringVal("LabelboxRole-hm-labelbox"),
	})

	importID, values, ok := ComponentsFromValue(row, val)
	if !ok {
		t.Fatalf("ComponentsFromValue reported not-found even though role and name (the two components this row actually reads) are both known - an unrelated unknown attribute (policy) must never veto this")
	}
	if want := "LabelboxRole-hm-labelbox:LabelboxRoleS3Policy-hm-labelbox"; importID != want {
		t.Errorf("importID = %q, want %q", importID, want)
	}
	if values["role"] != "LabelboxRole-hm-labelbox" {
		t.Errorf("values[role] = %q, want %q", values["role"], "LabelboxRole-hm-labelbox")
	}
	if values["name"] != "LabelboxRoleS3Policy-hm-labelbox" {
		t.Errorf("values[name] = %q, want %q", values["name"], "LabelboxRoleS3Policy-hm-labelbox")
	}
}

// TestComponentsFromValueMarkedIsNotFound: a sensitive value must never
// reach an identity string or an import call. This is the marksafe rule
// (internal/live/marksafe) applied one layer up from an Unmark - refuse,
// never silently strip the mark.
func TestComponentsFromValueMarkedIsNotFound(t *testing.T) {
	row, _ := LookupType("aws_route")

	val := cty.ObjectVal(map[string]cty.Value{
		"route_table_id":              cty.StringVal("rtb-1").Mark("sensitive"),
		"destination_cidr_block":      cty.StringVal("10.0.0.0/16"),
		"destination_ipv6_cidr_block": cty.NullVal(cty.String),
		"destination_prefix_list_id":  cty.NullVal(cty.String),
	})

	if _, _, ok := ComponentsFromValue(row, val); ok {
		t.Fatalf("expected not-found: a marked value must never flow into an identity string")
	}
}

// TestComponentsFromValueBlockComponent is
// aws_autoscaling_traffic_source_attachment (GitHub issue #310): two of its
// three import-ID components live inside a nested, max_items:1
// "traffic_source" list block rather than at the top level.
func TestComponentsFromValueBlockComponent(t *testing.T) {
	row, ok := LookupType("aws_autoscaling_traffic_source_attachment")
	if !ok {
		t.Skip("aws_autoscaling_traffic_source_attachment is not in DefaultTable in this provider pin")
	}

	val := cty.ObjectVal(map[string]cty.Value{
		"autoscaling_group_name": cty.StringVal("my-asg"),
		"traffic_source": cty.ListVal([]cty.Value{
			cty.ObjectVal(map[string]cty.Value{
				"type":       cty.StringVal("elbv2"),
				"identifier": cty.StringVal("arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/example/abc"),
			}),
		}),
	})

	importID, _, ok := ComponentsFromValue(row, val)
	if !ok {
		t.Fatalf("ComponentsFromValue reported not-found for a populated traffic_source block")
	}
	if importID == "" {
		t.Errorf("importID is empty")
	}
	t.Logf("resolved traffic-source-attachment import ID: %q", importID)
}

// TestComponentsFromValueServerAssignedRowRefuses: a type whose whole
// identity is server-assigned (ServerAssigned) is out of this evaluator's
// scope entirely - it has nothing to read off configuration, by
// construction, and returning ok=false is "nothing to say," the same
// answer every other step in the resolver's chain gives for a type it does
// not otherwise handle.
func TestComponentsFromValueServerAssignedRowRefuses(t *testing.T) {
	for typeName, row := range DefaultTable {
		if row.ServerAssigned {
			if _, _, ok := ComponentsFromValue(row, cty.EmptyObjectVal); ok {
				t.Fatalf("%s is ServerAssigned; ComponentsFromValue must never report found for it", typeName)
			}
			return
		}
	}
	t.Skip("no ServerAssigned row found in DefaultTable to test against")
}

// TestNodeSeamComponentsFromValueResolvesWhatStaticRefuses is this unit's
// headline claim: an identity argument reading another resource's real,
// Computed, non-identity attribute is refused by the static evaluator
// (resolve.go's "Not an identity attribute" - see resolve.go ~line 2916)
// but is an ordinary already-known string by the time
// NodeAbstractResourceInstance.plan evaluates it for real. This is
// corpus-alb-complete's own remaining shape in miniature: a target-group
// attachment's port fed by a value the static evaluator cannot fold.
//
// The fixture (testdata/node-seam-computed-boundary) is deliberately built
// on a REAL ratified type (aws_lb_target_group_attachment) reading a
// fake-schema sibling's Computed attribute, so the refusal comes from the
// same registered-IdentityAttrs boundary corpus-alb-complete's own wall
// does, not from a table row this test invented.
func TestNodeSeamComponentsFromValueResolvesWhatStaticRefuses(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "node-seam-computed-boundary"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: siblingTestSchemas()})

	if _, ok := result.Get(mustAddr(t, "aws_lb_target_group_attachment.reads_computed")); ok {
		t.Fatalf("the static evaluator resolved reads_computed; it should have refused test_sibling.s.computed_val")
	}
	if !hasDiag(diags, "Not an identity attribute", "computed_val") {
		t.Fatalf("expected a \"Not an identity attribute\" refusal naming computed_val:\n%s", renderDiags(diags))
	}

	// Now the node path: the exact same instance, but with the value the
	// node's real graph walk would have handed the resolver once
	// test_sibling.s has actually been read - an ordinary, wholly-known
	// string, because "not yet foldable by the static evaluator" and "not
	// known" are different facts, and this is the case where the first is
	// true and the second is not.
	row, ok := LookupType("aws_lb_target_group_attachment")
	if !ok {
		t.Fatal("aws_lb_target_group_attachment is not in DefaultTable")
	}
	val := cty.ObjectVal(map[string]cty.Value{
		"target_group_arn":  cty.StringVal("arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/example/0123456789abcdef"),
		"target_id":         cty.StringVal("i-0123456789abcdef0"),
		"port":              cty.NumberIntVal(443),
		"availability_zone": cty.NullVal(cty.String),
		"quic_server_id":    cty.NullVal(cty.String),
	})
	importID, values, ok := ComponentsFromValue(row, val)
	if !ok {
		t.Fatalf("ComponentsFromValue reported not-found for a fully-known evaluated value; the whole point of the node seam is that it should not")
	}
	if want := "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/example/0123456789abcdef,i-0123456789abcdef0,443"; importID != want {
		t.Errorf("importID = %q, want %q", importID, want)
	}
	if values["port"] != "443" {
		t.Errorf("values[port] = %q, want \"443\"", values["port"])
	}
}

// TestComponentsUnknown_TrueWhenIdentityAttributeUnknown is
// corpus-dynamodb-table-basic's own greenfield shape, reduced to the
// identity table alone: aws_dynamodb_table's `name` (its only identity
// component) is a formula over random_pet.this.id, and on a genuinely
// fresh estate's first apply random_pet has not run yet, so the node's
// real evaluated value carries `name` as cty.UnknownVal, not a mismatched
// or absent string. [ComponentsUnknown] has to say true here - not merely
// "ComponentsFromValue found nothing," which unknown and absent both
// produce - because the caller (noderesolver.go's ResolveResourceIdentity)
// uses this one signal to tell "nobody has computed this value yet" apart
// from "this run could not derive a real object's identity."
func TestComponentsUnknown_TrueWhenIdentityAttributeUnknown(t *testing.T) {
	row, ok := LookupType("aws_dynamodb_table")
	if !ok {
		t.Fatal("aws_dynamodb_table is not in DefaultTable; this test needs a real ratified row")
	}

	val := cty.ObjectVal(map[string]cty.Value{
		"name": cty.UnknownVal(cty.String),
	})

	if !ComponentsUnknown(row, val) {
		t.Fatalf("ComponentsUnknown = false for a value whose only identity attribute is cty.UnknownVal; want true")
	}

	// And ComponentsFromValue itself still reports not-found for this
	// value, exactly as it did before this test existed - ComponentsUnknown
	// narrows WHY ComponentsFromValue failed, it does not change whether
	// it failed.
	if _, _, ok := ComponentsFromValue(row, val); ok {
		t.Fatalf("ComponentsFromValue reported found=true for an unknown identity attribute; it must never guess")
	}
}

// TestComponentsUnknown_FalseWhenAbsent is the contrasting case at the same
// address: the identity attribute is present in the schema but genuinely
// unset (cty.NullVal), not merely not-yet-known. This is real ambiguity -
// ruling 4 (#365)'s own no-source case - and ComponentsUnknown must say
// false so the caller's default refusal still fires for it.
func TestComponentsUnknown_FalseWhenAbsent(t *testing.T) {
	row, ok := LookupType("aws_dynamodb_table")
	if !ok {
		t.Fatal("aws_dynamodb_table is not in DefaultTable")
	}

	val := cty.ObjectVal(map[string]cty.Value{
		"name": cty.NullVal(cty.String),
	})

	if ComponentsUnknown(row, val) {
		t.Fatalf("ComponentsUnknown = true for a genuinely absent (null) identity attribute; want false - this is ordinary ambiguity, not an unknown-until-apply value")
	}
	if _, _, ok := ComponentsFromValue(row, val); ok {
		t.Fatalf("ComponentsFromValue reported found=true for a null identity attribute")
	}
}

// TestComponentsUnknown_FalseWhenFullyResolved proves ComponentsUnknown
// does not fire on the ordinary success path, so a caller gating on it
// never withholds a refusal it should have raised, and never treats an
// instance the table CAN resolve as if it could not be checked.
func TestComponentsUnknown_FalseWhenFullyResolved(t *testing.T) {
	row, ok := LookupType("aws_dynamodb_table")
	if !ok {
		t.Fatal("aws_dynamodb_table is not in DefaultTable")
	}

	val := cty.ObjectVal(map[string]cty.Value{
		"name": cty.StringVal("my-table-humane-bunny"),
	})

	if ComponentsUnknown(row, val) {
		t.Fatalf("ComponentsUnknown = true for a fully-known value; want false")
	}
	importID, _, ok := ComponentsFromValue(row, val)
	if !ok || importID != "my-table-humane-bunny" {
		t.Fatalf("ComponentsFromValue = %q, %v; want \"my-table-humane-bunny\", true", importID, ok)
	}
}

// TestComponentsUnknown_ServerAssignedAndRecordBackedNeverUnknown proves
// the caller never needs ComponentsUnknown to protect these two: they are
// already unconditionally exempt from the "No source" refusal
// (row.ServerAssigned / row.RecordBacked short-circuit noderesolver.go's
// sourceExpected before ComponentsUnknown is even consulted), and this
// function agrees on its own terms - it has nothing to derive for either
// shape, known or not.
func TestComponentsUnknown_ServerAssignedAndRecordBackedNeverUnknown(t *testing.T) {
	saRow, ok := LookupType("aws_vpc")
	if !ok {
		t.Fatal("aws_vpc is not in DefaultTable; this test needs a real ServerAssigned row")
	}
	if !saRow.ServerAssigned {
		t.Fatal("aws_vpc is expected to be ServerAssigned")
	}
	if ComponentsUnknown(saRow, cty.EmptyObjectVal) {
		t.Errorf("ComponentsUnknown = true for a ServerAssigned row; want false")
	}

	rbRow, ok := LookupType("random_pet")
	if !ok {
		t.Skip("random_pet is not in DefaultTable; this test needs a real RecordBacked row")
	}
	if !rbRow.RecordBacked {
		t.Fatal("random_pet is expected to be RecordBacked")
	}
	if ComponentsUnknown(rbRow, cty.EmptyObjectVal) {
		t.Errorf("ComponentsUnknown = true for a RecordBacked row; want false")
	}
}

// TestComponentsServerAssignedIfAbsent_TrueWhenNameOmitted is
// corpus-autoscaling-complete's own greenfield shape: aws_iam_role's
// "name" component carries ServerAssignedIfAbsent because the provider's
// own Argument Reference documents IAM assigning a unique name when
// configuration leaves it blank (the *_prefix convention). A blank name
// here - a genuine null, not unknown - must report true, the identical
// "no source to be missing" signal [ComponentsUnknown] gives for a
// not-yet-computed value.
func TestComponentsServerAssignedIfAbsent_TrueWhenNameOmitted(t *testing.T) {
	row, ok := LookupType("aws_iam_role")
	if !ok {
		t.Fatal("aws_iam_role is not in DefaultTable")
	}
	if len(row.Components) != 1 || !row.Components[0].ServerAssignedIfAbsent {
		t.Fatalf("aws_iam_role's row no longer matches this test's premise: %+v", row.Components)
	}

	val := cty.ObjectVal(map[string]cty.Value{
		"name": cty.NullVal(cty.String),
	})

	if !ComponentsServerAssignedIfAbsent(row, val) {
		t.Fatalf("ComponentsServerAssignedIfAbsent = false for a blank ServerAssignedIfAbsent argument; want true")
	}
	if _, _, ok := ComponentsFromValue(row, val); ok {
		t.Fatalf("ComponentsFromValue reported found=true for a null identity attribute; it must never guess")
	}
}

// TestComponentsServerAssignedIfAbsent_FalseWhenGenuinelyAmbiguous proves
// this signal does not widen into a general amnesty for absence:
// aws_route's route_table_id component carries no ServerAssignedIfAbsent,
// no Default and no OmitIfAbsent, so a null value is ruling 4 (#365)'s
// real ambiguous case and must still report false.
func TestComponentsServerAssignedIfAbsent_FalseWhenGenuinelyAmbiguous(t *testing.T) {
	row, ok := LookupType("aws_route")
	if !ok {
		t.Fatal("aws_route is not in DefaultTable")
	}

	val := cty.ObjectVal(map[string]cty.Value{
		"route_table_id":              cty.NullVal(cty.String),
		"destination_cidr_block":      cty.StringVal("10.0.0.0/16"),
		"destination_ipv6_cidr_block": cty.NullVal(cty.String),
		"destination_prefix_list_id":  cty.NullVal(cty.String),
	})

	if ComponentsServerAssignedIfAbsent(row, val) {
		t.Fatalf("ComponentsServerAssignedIfAbsent = true for a genuinely missing, non-server-assigned argument; want false")
	}
}

// TestComponentsServerAssignedIfAbsent_FalseWhenFullyResolved proves this
// signal does not fire on the ordinary success path.
func TestComponentsServerAssignedIfAbsent_FalseWhenFullyResolved(t *testing.T) {
	row, ok := LookupType("aws_iam_role")
	if !ok {
		t.Fatal("aws_iam_role is not in DefaultTable")
	}

	val := cty.ObjectVal(map[string]cty.Value{
		"name": cty.StringVal("my-role"),
	})

	if ComponentsServerAssignedIfAbsent(row, val) {
		t.Fatalf("ComponentsServerAssignedIfAbsent = true for a fully-known value; want false")
	}
	importID, _, ok := ComponentsFromValue(row, val)
	if !ok || importID != "my-role" {
		t.Fatalf("ComponentsFromValue = %q, %v; want \"my-role\", true", importID, ok)
	}
}

// TestComponentsServerAssignedIfAbsent_StopsAtTheFirstFailure is the
// precision this function's own doc comment promises over
// [ComponentsUnknown]'s "any" style: a genuinely ambiguous absence earlier
// in the component list must win over a ServerAssignedIfAbsent one later
// in it, because [ComponentsFromValue]'s own walk would have stopped at
// the FIRST one and never reached the second. Built directly against a
// synthetic two-component row - not a real ratified one - because no row
// in the table happens to combine the two shapes in this order, and the
// mechanism itself, not any particular type, is what this pins.
func TestComponentsServerAssignedIfAbsent_StopsAtTheFirstFailure(t *testing.T) {
	row := TypeIdentity{
		Type: "test_two_component",
		Components: []Component{
			{Attrs: []string{"ambiguous_first"}, IdentityAttr: "*"},
			{Literal: "_"},
			{Attrs: []string{"assigned_second"}, ServerAssignedIfAbsent: true, IdentityAttr: "*"},
		},
	}

	val := cty.ObjectVal(map[string]cty.Value{
		"ambiguous_first": cty.NullVal(cty.String),
		"assigned_second": cty.NullVal(cty.String),
	})

	if ComponentsServerAssignedIfAbsent(row, val) {
		t.Fatalf("ComponentsServerAssignedIfAbsent = true when the FIRST unresolved component is genuinely ambiguous; want false - a later ServerAssignedIfAbsent component never gets reached")
	}

	// And the mirror: swap which component is ServerAssignedIfAbsent so
	// it is the first (and only) one the walk actually reaches.
	rowFirst := TypeIdentity{
		Type: "test_two_component_first",
		Components: []Component{
			{Attrs: []string{"assigned_first"}, ServerAssignedIfAbsent: true, IdentityAttr: "*"},
		},
	}
	valFirst := cty.ObjectVal(map[string]cty.Value{
		"assigned_first": cty.NullVal(cty.String),
	})
	if !ComponentsServerAssignedIfAbsent(rowFirst, valFirst) {
		t.Fatalf("ComponentsServerAssignedIfAbsent = false when the only unresolved component IS ServerAssignedIfAbsent; want true")
	}
}

// TestComponentsCloudPending_TrueForRealAwsSqsQueueRow is corpus-sqs-basic's
// own greenfield regression, reduced to the identity table alone: the real
// ratified aws_sqs_queue row's url component chain reads region and
// account-id ([identity.CloudContext]) before it ever reaches name, so
// ComponentsFromValue hard-fails on the region component regardless of
// whether name is set - a structural gap in this evaluator, not an
// ambiguous instance. Proven against name PRESENT (the ordinary case a
// brand-new greenfield queue actually has), because that is what the
// gauntlet's own regression looked like: a resource whose configuration is
// completely unambiguous still fell into ruling 4's "No source" refusal.
func TestComponentsCloudPending_TrueForRealAwsSqsQueueRow(t *testing.T) {
	row, ok := LookupType("aws_sqs_queue")
	if !ok {
		t.Fatal("aws_sqs_queue is not in DefaultTable; this test needs the real ratified row")
	}
	hasCloud := false
	for _, c := range row.Components {
		if c.Cloud != CloudNone {
			hasCloud = true
		}
	}
	if !hasCloud {
		t.Fatal("aws_sqs_queue's row no longer names a Cloud component; this test's premise has changed")
	}

	val := cty.ObjectVal(map[string]cty.Value{
		"name": cty.StringVal("my-queue"),
	})

	if !ComponentsCloudPending(row, val) {
		t.Fatalf("ComponentsCloudPending = false for aws_sqs_queue with a fully-known name; want true - the region component blocks first")
	}
	// And ComponentsFromValue itself still reports not-found, exactly as it
	// did before this exemption existed - ComponentsCloudPending narrows
	// WHY it failed, it does not change whether it failed.
	if _, _, ok := ComponentsFromValue(row, val); ok {
		t.Fatalf("ComponentsFromValue reported found=true for aws_sqs_queue; the node has no CloudContext to have derived a url from")
	}
	// The other exemptions must NOT already cover this - if either did, the
	// bug this function fixes would never have shipped.
	if ComponentsUnknown(row, val) {
		t.Errorf("ComponentsUnknown = true for aws_sqs_queue; want false - the region component hard-fails, it is not merely unknown")
	}
	if ComponentsServerAssignedIfAbsent(row, val) {
		t.Errorf("ComponentsServerAssignedIfAbsent = true for aws_sqs_queue; want false - the walk stops at the region component, before name is ever reached")
	}
}

// TestComponentsCloudPending_FalseWhenGenuinelyAmbiguousComesFirst proves
// this signal does not widen into a general amnesty for absence: a
// synthetic row with a real ambiguity (no OmitIfAbsent, no Default, no
// ServerAssignedIfAbsent) BEFORE its Cloud component must still report
// false, because [ComponentsFromValue]'s own walk would have stopped at the
// ambiguous component first and never reached the Cloud one - the same
// precision [ComponentsServerAssignedIfAbsent]'s own
// TestComponentsServerAssignedIfAbsent_StopsAtTheFirstFailure test pins for
// its sibling exemption.
func TestComponentsCloudPending_FalseWhenGenuinelyAmbiguousComesFirst(t *testing.T) {
	row := TypeIdentity{
		Type: "test_ambiguous_then_cloud",
		Components: []Component{
			{Attrs: []string{"ambiguous_first"}, IdentityAttr: "*"},
			{Literal: "_"},
			{Cloud: CloudAccountID, IdentityAttr: "*"},
		},
	}
	val := cty.ObjectVal(map[string]cty.Value{
		"ambiguous_first": cty.NullVal(cty.String),
	})

	if ComponentsCloudPending(row, val) {
		t.Fatalf("ComponentsCloudPending = true when a genuinely ambiguous component comes BEFORE the Cloud one; want false - the walk never reaches the Cloud component")
	}

	// And the mirror: swap the order so Cloud is first and genuinely reached.
	rowFirst := TypeIdentity{
		Type: "test_cloud_first",
		Components: []Component{
			{Cloud: CloudAccountID, IdentityAttr: "*"},
			{Attrs: []string{"ambiguous_second"}, IdentityAttr: "*"},
		},
	}
	valFirst := cty.ObjectVal(map[string]cty.Value{
		"ambiguous_second": cty.NullVal(cty.String),
	})
	if !ComponentsCloudPending(rowFirst, valFirst) {
		t.Fatalf("ComponentsCloudPending = false when the FIRST component the walk reaches IS Cloud; want true")
	}
}

// TestComponentsCloudPending_FalseWhenOtherHardFailComesFirst proves a
// different hard-fail reason (a marked, sensitive value) reached before any
// Cloud component is never reported as cloud-pending - that would let a
// genuinely sensitive value's refusal be silently withheld for the wrong
// reason.
func TestComponentsCloudPending_FalseWhenOtherHardFailComesFirst(t *testing.T) {
	row := TypeIdentity{
		Type: "test_marked_then_cloud",
		Components: []Component{
			{Attrs: []string{"secret_first"}, IdentityAttr: "*"},
			{Cloud: CloudRegion, IdentityAttr: "*"},
		},
	}
	val := cty.ObjectVal(map[string]cty.Value{
		"secret_first": cty.StringVal("shh").Mark("sensitive"),
	})

	if ComponentsCloudPending(row, val) {
		t.Fatalf("ComponentsCloudPending = true when a marked value hard-fails BEFORE the Cloud component; want false")
	}
}

// TestComponentsCloudPending_FalseWhenFullyResolved proves this signal does
// not fire on a row with no Cloud component at all - the ordinary success
// or ordinary-ambiguity path is untouched.
func TestComponentsCloudPending_FalseWhenFullyResolved(t *testing.T) {
	row, ok := LookupType("aws_dynamodb_table")
	if !ok {
		t.Fatal("aws_dynamodb_table is not in DefaultTable")
	}
	val := cty.ObjectVal(map[string]cty.Value{
		"name": cty.StringVal("my-table"),
	})
	if ComponentsCloudPending(row, val) {
		t.Fatalf("ComponentsCloudPending = true for a row with no Cloud component; want false")
	}
}

// TestComponentsCloudPending_PerElementNeverExempted proves PerElement is
// deliberately excluded: [ComponentsFromValue]'s own doc comment gives no
// evidence a PerElement hard-fail is this same "structurally never
// derivable" shape, so a synthetic PerElement component must not be waved
// through by this function.
func TestComponentsCloudPending_PerElementNeverExempted(t *testing.T) {
	row := TypeIdentity{
		Type: "test_per_element",
		Components: []Component{
			{PerElement: true, IdentityAttr: "*"},
		},
	}
	if ComponentsCloudPending(row, cty.EmptyObjectVal) {
		t.Fatalf("ComponentsCloudPending = true for a PerElement component; want false - PerElement is not this exemption's business")
	}
}

// TestComponentsCloudPending_ServerAssignedAndRecordBackedNever mirrors the
// same boundary test both sibling exemptions carry: these two shapes are
// already unconditionally exempt from the "No source" refusal before this
// signal is ever consulted.
func TestComponentsCloudPending_ServerAssignedAndRecordBackedNever(t *testing.T) {
	saRow, ok := LookupType("aws_vpc")
	if !ok {
		t.Fatal("aws_vpc is not in DefaultTable; this test needs a real ServerAssigned row")
	}
	if !saRow.ServerAssigned {
		t.Fatal("aws_vpc is expected to be ServerAssigned")
	}
	if ComponentsCloudPending(saRow, cty.EmptyObjectVal) {
		t.Errorf("ComponentsCloudPending = true for a ServerAssigned row; want false")
	}

	rbRow, ok := LookupType("random_pet")
	if !ok {
		t.Skip("random_pet is not in DefaultTable; this test needs a real RecordBacked row")
	}
	if !rbRow.RecordBacked {
		t.Fatal("random_pet is expected to be RecordBacked")
	}
	if ComponentsCloudPending(rbRow, cty.EmptyObjectVal) {
		t.Errorf("ComponentsCloudPending = true for a RecordBacked row; want false")
	}
}

// TestComponentsServerAssignedIfAbsent_ServerAssignedAndRecordBackedNever
// mirrors TestComponentsUnknown_ServerAssignedAndRecordBackedNeverUnknown:
// these two shapes are already unconditionally exempt from the "No
// source" refusal before this signal is ever consulted, and it agrees on
// its own terms.
func TestComponentsServerAssignedIfAbsent_ServerAssignedAndRecordBackedNever(t *testing.T) {
	saRow, ok := LookupType("aws_vpc")
	if !ok {
		t.Fatal("aws_vpc is not in DefaultTable; this test needs a real ServerAssigned row")
	}
	if !saRow.ServerAssigned {
		t.Fatal("aws_vpc is expected to be ServerAssigned")
	}
	if ComponentsServerAssignedIfAbsent(saRow, cty.EmptyObjectVal) {
		t.Errorf("ComponentsServerAssignedIfAbsent = true for a ServerAssigned row; want false")
	}

	rbRow, ok := LookupType("random_pet")
	if !ok {
		t.Skip("random_pet is not in DefaultTable; this test needs a real RecordBacked row")
	}
	if !rbRow.RecordBacked {
		t.Fatal("random_pet is expected to be RecordBacked")
	}
	if ComponentsServerAssignedIfAbsent(rbRow, cty.EmptyObjectVal) {
		t.Errorf("ComponentsServerAssignedIfAbsent = true for a RecordBacked row; want false")
	}
}
