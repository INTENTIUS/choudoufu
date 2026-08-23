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

// TestComponentsFromValuePortNullIsNotFound is HANDOFF's fifth row read
// honestly: a Lambda target genuinely has no port in real AWS, so a null
// port (no OmitIfAbsent on that component - see table_generated.go) must
// report ok=false, the same refusal the static evaluator gives an absent
// required component, not a guessed empty string. See the gauntlet detail
// for corpus-alb-complete (live/gauntlet.json): "a lambda target genuinely
// has no port... the null is the honest answer, not a defect."
func TestComponentsFromValuePortNullIsNotFound(t *testing.T) {
	row, _ := LookupType("aws_lb_target_group_attachment")

	val := cty.ObjectVal(map[string]cty.Value{
		"target_group_arn":  cty.StringVal("arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/example/0123456789abcdef"),
		"target_id":         cty.StringVal("lambda-arn"),
		"port":              cty.NullVal(cty.Number),
		"availability_zone": cty.NullVal(cty.String),
		"quic_server_id":    cty.NullVal(cty.String),
	})

	if _, _, ok := ComponentsFromValue(row, val); ok {
		t.Fatalf("expected not-found for a null, non-OmitIfAbsent port; a null port is not identical to zero")
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
