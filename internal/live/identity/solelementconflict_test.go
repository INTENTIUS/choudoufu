// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
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

// TestSecurityGroupRuleSourceSegmentStaysRefused is the record-rung
// investigation issue #384's own regression opened, resolved honestly rather
// than rushed.
//
// HANDOFF's safety rule says a construct this package cannot yet resolve
// without risking a wrong marker must drop the instance to the record rung,
// not merely refuse it - and [resolver.recordFallback]/[RecordFallbackType]
// already do exactly that for a type with a ratified row whose schema has
// nowhere to carry a marker (aws_autoscaling_group's name_prefix case,
// TestRecordFallbackClassifiesUntaggableNamePrefix). An earlier pass of this
// test asserted the SAME wiring fires for this [Component.SoleElement]
// conflict too, using a schema that stringified from_port/to_port to dodge
// the real gap ([attrsByDocName] not yet admitting cty.Number) - and never
// checked the resulting identity STRING, only the resolved CLASS.
//
// Fixing the number gap (this file's own change) uncovers a second,
// unrelated problem that the earlier test's synthetic schema was
// accidentally exercising, unchecked, the whole time: with
// security_group_id/type/protocol/from_port/to_port all resolved, the
// documented import string's sixth segment - "cidr_block" - is STILL
// unresolved (the real schema carries only the LIST cidr_blocks, never a
// scalar cidr_block), which makes it the type's one "id"-inferred segment.
// But aws_security_group_rule's `id` is confirmed, from the provider's own
// source (securityGroupRuleCreateID: `"sgrule-" + hash(sgID, ports,
// protocol, type, ALL sources)`), to be a hash of the WHOLE rule, not any
// one source - so recording it in the source's place would be a wrong
// identity the provider's own importer refuses to parse
// (resourceSecurityGroupRuleImport's source-format check), not a working
// one. [pluralCollectionCollision] is the guard this file adds to stop that
// substitution generically (see [resolveDocumentedImportID]'s doc comment
// and TestResolveDocumentedImportIDCorroboratesEveryNameAgainstTheSchema's
// "collides with a real plural collection attribute" case for the guard on
// its own), and this test is its by-value proof against the type that forced
// it: the schema is aws_security_group_rule's REAL 6.59.0 shape (confirmed
// via `terraform providers schema -json` against the pinned provider), not a
// hypothetical.
//
// The deeper fact this leaves standing: aws_security_group_rule's
// documented "SOURCE[_SOURCE]*" import grammar is variadic - the Import
// section's own second example joins FOUR source tokens after one
// FROMPORT_TOPORT pair - and [DocumentedImportIDPart]'s model is a fixed
// one-attribute-per-segment list with no way to express "the rest of the
// string is every element of these list arguments, in some order". Building
// that safely means knowing the provider's importer is order-insensitive
// over the trailing tokens, which needs its own verification and its own
// review; it is not a corroboration gap and is not done here. Until it
// exists, this conflict correctly stays a refusal - the same
// "Ambiguous list-valued identity argument" diagnostic
// TestSoleElementConflictNeverBindsAConcreteIdentity already asserts,
// unchanged by anything in this file.
func TestSecurityGroupRuleSourceSegmentStaysRefused(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "record-fallback-solelement-conflict"), nil)

	stringAttr := func(required bool) *configschema.Attribute {
		if required {
			return &configschema.Attribute{Type: cty.String, Required: true}
		}
		return &configschema.Attribute{Type: cty.String, Optional: true}
	}
	numberAttr := func() *configschema.Attribute {
		return &configschema.Attribute{Type: cty.Number, Required: true}
	}
	block := &configschema.Block{Attributes: map[string]*configschema.Attribute{
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
	schemas := map[string]providers.Schema{"aws_security_group_rule": {Block: block}}

	// The mechanics, asserted directly: every Argument segment now resolves
	// (proving the number fix reached this type at all), and the grammar as
	// a whole is still refused (proving the plural-collision guard, not
	// some unrelated failure, is what stops it - if this were true for the
	// wrong reason, e.g. a typo in the schema above, the case below would
	// pass vacuously).
	if parts, _, ok := resolveDocumentedImportID("aws_security_group_rule", block); ok {
		t.Fatalf("resolveDocumentedImportID(real aws_security_group_rule schema) = %v, ok=true; want a refusal - "+
			"the \"cidr_block\" segment has no safe resolution and must not be filled by inferring `id`", parts)
	}
	if RecordFallbackType("aws_security_group_rule", schemas) {
		t.Fatal("RecordFallbackType(real aws_security_group_rule schema) = true, want false: the type's " +
			"identity cannot be recorded in full while its \"cidr_block\" segment has no safe resolution, so " +
			"the record-rung promise (\"the record can be read back as a whole, correct import identity\") " +
			"cannot be kept for it yet")
	}

	// The end-to-end behavior, unchanged from before this file's fix: the
	// conflicting instance still resolves to no concrete identity and still
	// raises the same operator-facing diagnostic, record_store declared or
	// not.
	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: schemas})

	if res, ok := result.Get(mustAddr(t, "aws_security_group_rule.egress_all_all")); ok {
		t.Fatalf("egress_all_all resolved to Class=%s ImportID=%q; the source segment has no safe resolution, "+
			"so it must resolve to no concrete value rather than a guessed one", res.Class, res.ImportID)
	}
	var found bool
	for _, d := range diags {
		if d.Description().Summary == "Ambiguous list-valued identity argument" &&
			strings.Contains(d.Description().Detail, "egress_all_all") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the %q diagnostic naming egress_all_all even with a record_store declared and the "+
			"number gap fixed, got: %v", "Ambiguous list-valued identity argument", diags)
	}
}
