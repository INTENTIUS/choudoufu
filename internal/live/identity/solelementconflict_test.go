// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// TestSoleElementConflictNeverBindsAConcreteIdentity is GitHub issue #384,
// asserted the way HANDOFF's safety rule demands: by value, not by "the plan
// came out empty".
//
// terraform-aws-modules/security-group's egress_rules = ["all-all"] expands
// into an aws_security_group_rule instance whose module defaults BOTH
// egress_cidr_blocks and egress_ipv6_cidr_blocks, so cidr_blocks and
// ipv6_cidr_blocks - two members of the SAME [Component.SoleElement]
// alternation - are simultaneously real, non-empty, one-element lists. AWS
// creates two separate live rule objects (one IPv4, one IPv6) for the one
// declared instance. Before this fix, [resolver.firstApplicablePresent]
// returned whichever name Attrs listed first (cidr_blocks) without ever
// looking at ipv6_cidr_blocks, so the instance resolved [ClassConcrete] and
// bound the IPv4 rule - silently, with nothing to say the configuration
// never actually settled which of the two objects it named. A wrong marker
// convergence-proofs itself: the plan looks plausible whichever object it
// bound. So this asserts the STRONGER thing: the instance resolves to no
// concrete identity at all, for either candidate value.
func TestSoleElementConflictNeverBindsAConcreteIdentity(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "sole-element-from-value"), nil)

	result, diags := Resolve(context.Background(), cfg)

	if res, ok := result.Get(mustAddr(t, "aws_security_group_rule.egress_all_all")); ok {
		t.Fatalf("egress_all_all resolved to Class=%s ImportID=%q; cidr_blocks and ipv6_cidr_blocks are both genuinely non-empty, so nothing in configuration says which live object (AWS creates one per IP family) this instance names - it must not resolve to any concrete value", res.Class, res.ImportID)
	}

	var summary, detail string
	for _, d := range diags {
		if d.Description().Summary != "Ambiguous list-valued identity argument" {
			continue
		}
		if !strings.Contains(d.Description().Detail, "egress_all_all") {
			continue
		}
		summary, detail = d.Description().Summary, d.Description().Detail
	}
	if summary == "" {
		t.Fatalf("expected an %q diagnostic naming egress_all_all, got: %v", "Ambiguous list-valued identity argument", diags)
	}
	if !strings.Contains(detail, "cidr_blocks") || !strings.Contains(detail, "ipv6_cidr_blocks") {
		t.Fatalf("diagnostic must name both conflicting candidates (cidr_blocks, ipv6_cidr_blocks) so an operator can see which two disagree; got: %s", detail)
	}
}

// securityGroupRuleRealSchemaBlock is aws_security_group_rule's REAL
// hashicorp/aws 6.59.0 top-level shape (confirmed via `terraform providers
// schema -json` against the pinned provider) - the schema every test below
// that needs a real, corroborable schema shares, so the shape is written
// down once rather than drifting between them.
func securityGroupRuleRealSchemaBlock() *configschema.Block {
	stringAttr := func(required bool) *configschema.Attribute {
		if required {
			return &configschema.Attribute{Type: cty.String, Required: true}
		}
		return &configschema.Attribute{Type: cty.String, Optional: true}
	}
	numberAttr := func() *configschema.Attribute {
		return &configschema.Attribute{Type: cty.Number, Required: true}
	}
	return &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":                       {Type: cty.String, Computed: true},
		"security_group_id":        stringAttr(true),
		"type":                     stringAttr(true),
		"protocol":                 stringAttr(true),
		"from_port":                numberAttr(),
		"to_port":                  numberAttr(),
		"cidr_blocks":              {Type: cty.List(cty.String), Optional: true},
		"ipv6_cidr_blocks":         {Type: cty.List(cty.String), Optional: true},
		"prefix_list_ids":          {Type: cty.List(cty.String), Optional: true},
		"source_security_group_id": {Type: cty.String, Optional: true, Computed: true},
	}}
}

// TestSecurityGroupRuleSourceSegmentReachesTheRecordRung is issue #384's
// closing half.
//
// This test used to be named ...StaysRefused and asserted the opposite of
// what it asserts now: at the time it was written, [DocumentedImportIDPart]
// had no way to express "the rest of the string is every element of
// whichever of these sibling collection arguments the configuration sets",
// so [pluralCollectionCollision] firing on the documented "cidr_block"
// segment (against the real cidr_blocks LIST) always refused the type,
// unconditionally, before any instance was ever considered. Its own doc
// comment named the missing piece precisely: knowing the provider's
// importer is order-insensitive over the trailing tokens, verified rather
// than assumed.
//
// That verification is now [VariadicTrailingImportIDTypes]' own doc
// comment: hashicorp/aws's resourceSecurityGroupRuleImport (fetched
// 2026-08-23, no terraform in the loop) classifies every trailing token by
// the SHAPE of its own content - "sg-", a colon, "pl-", or else a bare
// CIDR - never by its position, so concatenating cidr_blocks,
// ipv6_cidr_blocks, prefix_list_ids and source_security_group_id in the
// FIXED order [Component.SoleElement]'s own row already lists them in
// (never sorted - each is schema.TypeList on the real provider schema, so
// its own element order is preserved) is something the importer can always
// parse back correctly. [variadicTrailingGroup] is what reads that family
// off the ratified row, and this test proves the type-level admission by
// value, then the end-to-end resolution the earlier version of this test
// proved was refused.
func TestSecurityGroupRuleSourceSegmentReachesTheRecordRung(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "record-fallback-solelement-conflict"), nil)
	block := securityGroupRuleRealSchemaBlock()
	schemas := map[string]providers.Schema{"aws_security_group_rule": {Block: block}}

	// The mechanics, asserted by value: the five fixed segments resolve as
	// before, and the sixth is now a variadic tail over the ratified
	// family - not a refusal, and not an inferred `id`.
	parts, variadicGroup, _, sep, ok := resolveDocumentedImportID("aws_security_group_rule", block)
	if !ok {
		t.Fatal("resolveDocumentedImportID(real aws_security_group_rule schema) refused; want the variadic tail " +
			"to admit it - see variadicTrailingGroup and VariadicTrailingImportIDTypes")
	}
	if want := []string{"security_group_id", "type", "protocol", "from_port", "to_port"}; !reflect.DeepEqual(parts, want) {
		t.Errorf("fixed parts = %v, want %v", parts, want)
	}
	if want := []string{"cidr_blocks", "ipv6_cidr_blocks", "prefix_list_ids", "source_security_group_id"}; !reflect.DeepEqual(variadicGroup, want) {
		t.Errorf("variadic group = %v, want %v - the exact family order Component.SoleElement's own row states, "+
			"never re-derived or sorted", variadicGroup, want)
	}
	if sep != "_" {
		t.Errorf("separator = %q, want %q", sep, "_")
	}

	if !RecordFallbackType("aws_security_group_rule", schemas) {
		t.Fatal("RecordFallbackType(real aws_security_group_rule schema) = false, want true: the variadic tail " +
			"now lets the record-rung promise (\"the record can be read back as a whole, correct import " +
			"identity\") be kept for this type")
	}

	// The end-to-end behavior: with a record_store declared, the conflicting
	// instance drops to ClassRecordLocated instead of raising the
	// "Ambiguous list-valued identity argument" diagnostic
	// TestSoleElementConflictNeverBindsAConcreteIdentity still asserts for
	// the no-record-store case, unchanged by anything in this file.
	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: schemas})

	res, ok := result.Get(mustAddr(t, "aws_security_group_rule.egress_all_all"))
	if !ok {
		t.Fatalf("egress_all_all did not resolve at all; want ClassRecordLocated. diags: %v", diags)
	}
	if res.Class != ClassRecordLocated {
		t.Errorf("egress_all_all resolved Class=%s, want %s", res.Class, ClassRecordLocated)
	}
	if res.ImportID != "" {
		t.Errorf("egress_all_all carries ImportID %q; ClassRecordLocated's identity comes from the record store, "+
			"never from this package, and a non-empty value here would be a wrong identity nothing verified", res.ImportID)
	}
	for _, d := range diags {
		if d.Description().Summary == "Ambiguous list-valued identity argument" &&
			strings.Contains(d.Description().Detail, "egress_all_all") {
			t.Errorf("still raised %q for egress_all_all even though the record rung now clears it: %s",
				d.Description().Summary, d.Description().Detail)
		}
	}
}

// TestLocatedComposedImportIDRendersVariadicTail is
// TestSecurityGroupRuleSourceSegmentReachesTheRecordRung's write-back half:
// the string [projection.LocatedRecordFrom] would actually write to the
// record store, once a real apply has produced an object - pinned by VALUE,
// the only assertion that can tell the right string from a plausible one.
func TestLocatedComposedImportIDRendersVariadicTail(t *testing.T) {
	parts := []string{"security_group_id", "type", "protocol", "from_port", "to_port"}
	variadicGroup := []string{"cidr_blocks", "ipv6_cidr_blocks", "prefix_list_ids", "source_security_group_id"}

	obj := func(cidrs, ipv6s, pls cty.Value, sgID cty.Value) cty.Value {
		return cty.ObjectVal(map[string]cty.Value{
			"security_group_id":        cty.StringVal("sg-6777656e646f6c796e"),
			"type":                     cty.StringVal("egress"),
			"protocol":                 cty.StringVal("-1"),
			"from_port":                cty.NumberIntVal(0),
			"to_port":                  cty.NumberIntVal(0),
			"cidr_blocks":              cidrs,
			"ipv6_cidr_blocks":         ipv6s,
			"prefix_list_ids":          pls,
			"source_security_group_id": sgID,
		})
	}
	nullList := cty.NullVal(cty.List(cty.String))
	nullStr := cty.NullVal(cty.String)

	t.Run("both families set at once, the issue's own reproduction", func(t *testing.T) {
		o := obj(
			cty.ListVal([]cty.Value{cty.StringVal("0.0.0.0/0")}),
			cty.ListVal([]cty.Value{cty.StringVal("::/0")}),
			nullList, nullStr,
		)
		got, ok := LocatedComposedImportID(o, parts, variadicGroup, nil, "_")
		if !ok {
			t.Fatal("refused an object carrying every fixed segment plus two real family members")
		}
		if want := "sg-6777656e646f6c796e_egress_-1_0_0_0.0.0.0/0_::/0"; got != want {
			t.Errorf("composed = %q, want %q - cidr_blocks' own element(s) before ipv6_cidr_blocks' own, the "+
				"ratified family order, never re-sorted or interleaved", got, want)
		}
	})

	t.Run("a single family set, unchanged from the ordinary one-source shape", func(t *testing.T) {
		o := obj(cty.ListVal([]cty.Value{cty.StringVal("10.0.3.0/24")}), nullList, nullList, nullStr)
		got, ok := LocatedComposedImportID(o, parts, variadicGroup, nil, "_")
		if !ok {
			t.Fatal("refused an object with exactly one family member set")
		}
		if want := "sg-6777656e646f6c796e_egress_-1_0_0_10.0.3.0/24"; got != want {
			t.Errorf("composed = %q, want %q - one source renders exactly one trailing token, the same shape "+
				"the documented single-CIDR example shows", got, want)
		}
	})

	t.Run("one family with several elements keeps its own configured order", func(t *testing.T) {
		o := obj(
			cty.ListVal([]cty.Value{cty.StringVal("10.1.0.0/16"), cty.StringVal("10.2.0.0/16")}),
			nullList, nullList, nullStr,
		)
		got, ok := LocatedComposedImportID(o, parts, variadicGroup, nil, "_")
		if !ok {
			t.Fatal("refused an object with one family carrying two elements")
		}
		if want := "sg-6777656e646f6c796e_egress_-1_0_0_10.1.0.0/16_10.2.0.0/16"; got != want {
			t.Errorf("composed = %q, want %q - a TypeList's own element order is preserved, never sorted "+
				"([Component.PerElement]'s sorting rule is for a SET; these are lists on the real schema", got, want)
		}
	})

	t.Run("a marked element refuses the whole render rather than unmarking it", func(t *testing.T) {
		o := obj(
			cty.ListVal([]cty.Value{cty.StringVal("10.0.3.0/24").Mark("secret")}),
			nullList, nullList, nullStr,
		)
		if got, ok := LocatedComposedImportID(o, parts, variadicGroup, nil, "_"); ok {
			t.Errorf("composed %q from a marked element; a forcibly unmarked value must never flow into an "+
				"identity component", got)
		}
	})

	t.Run("an unknown element refuses rather than guesses", func(t *testing.T) {
		o := obj(
			cty.ListVal([]cty.Value{cty.UnknownVal(cty.String)}),
			nullList, nullList, nullStr,
		)
		if got, ok := LocatedComposedImportID(o, parts, variadicGroup, nil, "_"); ok {
			t.Errorf("composed %q from an unknown element; this function is called on an applied object and "+
				"an unknown value there is not a value to guess a token from", got)
		}
	})

	t.Run("every family member absent refuses on the segment-count floor", func(t *testing.T) {
		o := obj(nullList, nullList, nullList, nullStr)
		if got, ok := LocatedComposedImportID(o, parts, variadicGroup, nil, "_"); ok {
			t.Errorf("composed %q with no source at all; the real schema's AtLeastOneOf makes this "+
				"configuration impossible, but the function must not silently compose a five-token string as "+
				"though the sixth were optional", got)
		}
	})
}
