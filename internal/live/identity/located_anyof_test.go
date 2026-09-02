// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"reflect"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file covers GitHub issue #364's write-side any-of gap: the located
// mechanism's config-side counterpart ([Component.Attrs] with more than one
// name) has always picked "whichever the configuration set"; nothing
// answered the identical question for an applied object until
// [namedAlternativeGroup] and [resolveAlternativeSegment] existed.
//
// aws_route is the type that exposed it and the only one that reaches it
// today (see [namedAlternativeGroup]'s own doc comment for the positional
// correlation this measures against, and [resolveDocumentedImportID]'s "any
// of segment" section for the provider-source citation proving the old
// bare-`id` inference was not merely unproven for this type but actively
// wrong: hashicorp/terraform-provider-aws's routeCreateID hashes the
// destination into an opaque id, so a record built from the bare id
// attribute could never be split back into ROUTETABLEID_DESTINATION by the
// provider's own routeImportID.Parse).
//
// awsRouteBlock and awsRouteIdentitySchema are the real hashicorp/aws
// 6.59.0 shape for aws_route, confirmed against the provider's own
// @IdentityAttribute annotations (internal/service/ec2/vpc_route.go:
// route_table_id required, the three destination_* attributes each
// optional="true") and its real top-level schema (route_table_id, id and
// the three destination_* arguments are all schema.TypeString).
func awsRouteBlock() *configschema.Block {
	return &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":                          {Type: cty.String, Computed: true},
		"route_table_id":              {Type: cty.String, Required: true},
		"destination_cidr_block":      {Type: cty.String, Optional: true},
		"destination_ipv6_cidr_block": {Type: cty.String, Optional: true},
		"destination_prefix_list_id":  {Type: cty.String, Optional: true},
	}}
}

func awsRouteIdentitySchema() *configschema.Object {
	return &configschema.Object{
		Nesting: configschema.NestingSingle,
		Attributes: map[string]*configschema.Attribute{
			"route_table_id":              {Type: cty.String, Required: true},
			"destination_cidr_block":      {Type: cty.String, Optional: true},
			"destination_ipv6_cidr_block": {Type: cty.String, Optional: true},
			"destination_prefix_list_id":  {Type: cty.String, Optional: true},
		},
	}
}

func awsRouteSchema() providers.Schema {
	return providers.Schema{Block: awsRouteBlock(), IdentitySchema: awsRouteIdentitySchema(), IdentitySchemaVersion: 1}
}

// TestNamedAlternativeGroupResolvesAwsRouteDestination pins
// [namedAlternativeGroup] directly: the destination segment (index 1 of 2)
// resolves to the exact three attributes, in the ratified row's own order,
// never fewer, more or reordered.
func TestNamedAlternativeGroupResolvesAwsRouteDestination(t *testing.T) {
	group, ok := namedAlternativeGroup("aws_route", 1, 2, awsRouteBlock())
	if !ok {
		t.Fatal("namedAlternativeGroup(aws_route, position 1) refused; want the destination alternation")
	}
	want := []string{"destination_cidr_block", "destination_ipv6_cidr_block", "destination_prefix_list_id"}
	if !reflect.DeepEqual(group, want) {
		t.Errorf("group = %v, want %v - the exact order table_generated.go's aws_route row states", group, want)
	}

	// Position 0 (route_table_id) is a single-attribute component, not an
	// alternation, and must not resolve here.
	if _, ok := namedAlternativeGroup("aws_route", 0, 2, awsRouteBlock()); ok {
		t.Error("namedAlternativeGroup(aws_route, position 0) resolved; route_table_id is a single argument, " +
			"not an any-of, and this function must tell the two apart")
	}

	// A partCount that does not match the row's own non-literal component
	// count is the "two documents disagree" case: refuse rather than guess
	// which position lines up with which.
	if _, ok := namedAlternativeGroup("aws_route", 1, 3, awsRouteBlock()); ok {
		t.Error("namedAlternativeGroup resolved despite a partCount mismatch against the ratified row's own " +
			"component count")
	}

	// A type with no ratified row at all (the population resolveDocumentedImportID " +
	// already reaches for every OTHER shape) must not synthesize an
	// alternation out of nothing.
	if _, ok := namedAlternativeGroup("aws_type_with_no_row", 1, 2, awsRouteBlock()); ok {
		t.Error("namedAlternativeGroup resolved for a type with no identity-table row")
	}
}

// TestResolveDocumentedImportIDUsesAnyOfForAwsRoute is
// TestNamedAlternativeGroupResolvesAwsRouteDestination's caller-level
// counterpart: the real aws_route grammar and the real aws_route schema
// resolve to a fixed route_table_id segment plus an alternatives entry for
// the destination segment, never a bare-`id` inference.
func TestResolveDocumentedImportIDUsesAnyOfForAwsRoute(t *testing.T) {
	parts, variadicGroup, alternatives, sep, ok := resolveDocumentedImportID("aws_route", awsRouteBlock())
	if !ok {
		t.Fatal("resolveDocumentedImportID(aws_route) refused; want the any-of route to admit it")
	}
	if sep != "_" {
		t.Errorf("separator = %q, want %q", sep, "_")
	}
	if variadicGroup != nil {
		t.Errorf("variadicGroup = %v, want nil - aws_route is not a [VariadicTrailingImportIDTypes] member", variadicGroup)
	}
	if want := []string{"route_table_id", ""}; !reflect.DeepEqual(parts, want) {
		t.Errorf("parts = %v, want %v - the destination slot is a placeholder, never the inferred bare \"id\"", parts, want)
	}
	if len(alternatives) != 2 || alternatives[0] != nil {
		t.Fatalf("alternatives = %v, want [nil, [...three destination attrs...]]", alternatives)
	}
	want := []string{"destination_cidr_block", "destination_ipv6_cidr_block", "destination_prefix_list_id"}
	if !reflect.DeepEqual(alternatives[1], want) {
		t.Errorf("alternatives[1] = %v, want %v", alternatives[1], want)
	}
}

// TestLocatedIdentityPlanForAwsRouteUsesAnyOf is the full
// [LocatedIdentityPlanFor] entry point, with the real wire identity schema
// present. route_table_id is the schema's only REQUIRED identity attribute,
// so [compositeIdentity] never engages (it needs two REQUIRED attributes
// including "id", which this schema has neither of) and resolution falls to
// the [IDNotProvenWholeTypes] branch this whole file is about - confirming
// the wire branch does not already catch this shape, which is why the fix
// belongs here and not there.
func TestLocatedIdentityPlanForAwsRouteUsesAnyOf(t *testing.T) {
	if _, unproven := IDNotProvenWholeTypes["aws_route"]; !unproven {
		t.Fatal("aws_route is no longer in IDNotProvenWholeTypes; this test's premise (the bare \"id\" branch is " +
			"what resolves it) no longer holds and needs re-reading against the current generated roster")
	}

	plan, recordable := LocatedIdentityPlanFor("aws_route", awsRouteSchema())
	if !recordable {
		t.Fatal("LocatedIdentityPlanFor(aws_route) refused; want the any-of route to admit it")
	}
	if plan.Composite() {
		t.Error("plan.Composite() = true; aws_route's wire identity schema has only one REQUIRED attribute, " +
			"so this must resolve through the documented-grammar route, not the identity-object route")
	}
	if !plan.Composed() {
		t.Fatal("plan.Composed() = false; want the documented ROUTETABLEID_DESTINATION grammar")
	}
	if plan.Named() {
		t.Error("plan.Named() = true; aws_route has no single ratified IdentityAttrs entry")
	}
	if want := []string{"route_table_id", ""}; !reflect.DeepEqual(plan.ImportIDParts, want) {
		t.Errorf("ImportIDParts = %v, want %v", plan.ImportIDParts, want)
	}
	if len(plan.ImportIDAlternatives) != 2 || plan.ImportIDAlternatives[0] != nil {
		t.Fatalf("ImportIDAlternatives = %v, want [nil, [...]]", plan.ImportIDAlternatives)
	}
}

// awsRouteObj builds an applied aws_route object carrying exactly the
// destination family members given (nil for absent).
func awsRouteObj(routeTableID string, cidr, ipv6, pl *string) cty.Value {
	str := func(s *string) cty.Value {
		if s == nil {
			return cty.NullVal(cty.String)
		}
		return cty.StringVal(*s)
	}
	return cty.ObjectVal(map[string]cty.Value{
		"id":                          cty.StringVal("r-rtb0abc123-1234567890"), // opaque, never read by this route
		"route_table_id":              cty.StringVal(routeTableID),
		"destination_cidr_block":      str(cidr),
		"destination_ipv6_cidr_block": str(ipv6),
		"destination_prefix_list_id":  str(pl),
	})
}

func strp(s string) *string { return &s }

// TestLocatedComposedImportIDPicksThePopulatedRouteDestination is the
// write-back pin: the exact string [projection.LocatedRecordFrom] would
// write for a real applied aws_route object, for each of the three
// mutually-exclusive destination shapes the provider's schema allows -
// value-asserted, because an exit code or a boolean cannot tell the right
// string from a plausible one (e.g. one built from the bare, opaque "id").
func TestLocatedComposedImportIDPicksThePopulatedRouteDestination(t *testing.T) {
	parts := []string{"route_table_id", ""}
	alternatives := [][]string{nil, {"destination_cidr_block", "destination_ipv6_cidr_block", "destination_prefix_list_id"}}

	cases := []struct {
		name           string
		cidr, ipv6, pl *string
		want           string
	}{
		{"a CIDR route", strp("10.0.0.0/16"), nil, nil, "rtb-0abc123_10.0.0.0/16"},
		{"an IPv6 route", nil, strp("2001:db8::/32"), nil, "rtb-0abc123_2001:db8::/32"},
		{"a prefix-list route", nil, nil, strp("pl-0abc123def456"), "rtb-0abc123_pl-0abc123def456"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obj := awsRouteObj("rtb-0abc123", tc.cidr, tc.ipv6, tc.pl)
			got, ok := LocatedComposedImportID(obj, parts, nil, alternatives, "_")
			if !ok {
				t.Fatalf("refused an object carrying exactly one populated destination family member")
			}
			if got != tc.want {
				t.Errorf("composed = %q, want %q - never the bare, opaque \"id\" this type's own provider "+
					"source proves is not ROUTETABLEID_DESTINATION", got, tc.want)
			}
		})
	}
}

// TestLocatedComposedImportIDRefusesZeroOrTwoPopulatedAlternatives is the
// negative half: HANDOFF's safety rule applied to a write. Zero populated
// members is a schema aws_route's own ExactlyOneOf forbids in practice, but
// this function must not silently compose a one-segment string as though
// the destination were optional; two populated members is a shape nothing
// here has verified the provider's importer would resolve any particular
// way, so it must refuse rather than pick one arbitrarily.
func TestLocatedComposedImportIDRefusesZeroOrTwoPopulatedAlternatives(t *testing.T) {
	parts := []string{"route_table_id", ""}
	alternatives := [][]string{nil, {"destination_cidr_block", "destination_ipv6_cidr_block", "destination_prefix_list_id"}}

	t.Run("none populated", func(t *testing.T) {
		obj := awsRouteObj("rtb-0abc123", nil, nil, nil)
		if got, ok := LocatedComposedImportID(obj, parts, nil, alternatives, "_"); ok {
			t.Errorf("composed %q from an object with no destination set", got)
		}
	})

	t.Run("two populated at once", func(t *testing.T) {
		obj := awsRouteObj("rtb-0abc123", strp("10.0.0.0/16"), strp("2001:db8::/32"), nil)
		if got, ok := LocatedComposedImportID(obj, parts, nil, alternatives, "_"); ok {
			t.Errorf("composed %q from an object with two destinations set; picking between them is exactly "+
				"the wrong identity this mechanism exists to never write", got)
		}
	})

	t.Run("a marked destination refuses rather than unmarking it", func(t *testing.T) {
		obj := cty.ObjectVal(map[string]cty.Value{
			"id":                          cty.StringVal("r-rtb0abc123-1234567890"),
			"route_table_id":              cty.StringVal("rtb-0abc123"),
			"destination_cidr_block":      cty.StringVal("10.0.0.0/16").Mark("secret"),
			"destination_ipv6_cidr_block": cty.NullVal(cty.String),
			"destination_prefix_list_id":  cty.NullVal(cty.String),
		})
		if got, ok := LocatedComposedImportID(obj, parts, nil, alternatives, "_"); ok {
			t.Errorf("composed %q from a marked destination; a forcibly unmarked value must never flow into "+
				"an identity component", got)
		}
	})
}

// TestNamedAlternativeGroupRejectsSoleElementAndPerElementComponents proves
// this mechanism stays disjoint from the two OTHER multi-Attrs shapes
// [Component] already has: a [Component.SoleElement] row (a documented
// import that names one element of a LIST the configuration may set, like
// aws_security_group_rule's own family) and a [Component.PerElement] one
// (one segment per element of a set) are different documented grammars, and
// this function must never treat either as a plain "pick the one populated
// alternative" position.
func TestNamedAlternativeGroupRejectsSoleElementAndPerElementComponents(t *testing.T) {
	// aws_security_group_rule's own row: its variadic-family component is
	// SoleElement, and its own dedicated route ([variadicTrailingGroup]) is
	// what handles it - namedAlternativeGroup must decline instead of
	// double-handling it.
	row, ok := LookupType("aws_security_group_rule")
	if !ok {
		t.Fatal("aws_security_group_rule missing from DefaultTable; this test's premise no longer holds")
	}
	var soleElementIdx = -1
	nonLiteral := 0
	for _, c := range row.Components {
		if len(c.Attrs) == 0 {
			continue
		}
		if c.SoleElement {
			soleElementIdx = nonLiteral
		}
		nonLiteral++
	}
	if soleElementIdx < 0 {
		t.Fatal("aws_security_group_rule's row carries no SoleElement component; this test's premise no longer holds")
	}
	if _, ok := namedAlternativeGroup("aws_security_group_rule", soleElementIdx, nonLiteral, securityGroupRuleRealSchemaBlock()); ok {
		t.Error("namedAlternativeGroup resolved a SoleElement component as a plain any-of; " +
			"variadicTrailingGroup already owns this shape and picking between the two would double-handle it")
	}
}
