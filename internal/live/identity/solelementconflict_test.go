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

// TestSoleElementConflictDropsToRecordRung is the record-rung half of issue
// #384: HANDOFF's safety rule says a construct this package cannot yet
// resolve without risking a wrong marker must drop the instance to the
// record rung and let the run proceed, not merely refuse it - and
// [resolver.recordFallback]/[RecordFallbackType] already exist to do exactly
// that for a type with a ratified row whose schema has nowhere to carry a
// marker (aws_autoscaling_group's name_prefix case,
// TestRecordFallbackClassifiesUntaggableNamePrefix). This proves the SAME
// wiring now fires for a [Component.SoleElement] conflict too, wherever the
// type's identity can be recorded in full.
//
// The schema below is synthetic, not aws_security_group_rule's real
// hashicorp/aws one, and that is the point being measured, not an
// oversight: aws_security_group_rule is unconditionally in
// [IDNotProvenWholeTypes], so [LocatedIdentityPlanFor] can only recover a
// recordable identity for it through [resolveDocumentedImportID], which
// requires every documented segment ("securitygroupid", "type", "protocol",
// "fromport", "toport") to be a top-level STRING attribute -
// stringAttrsByDocName only indexes strings. The real provider schema types
// from_port/to_port as cty.Number (confirmed against a live schema dump),
// so RecordFallbackType("aws_security_group_rule", real schemas) is false
// today: the record path this test proves exists does not yet reach
// aws_security_group_rule's actual shape, only a hypothetical schema of the
// identical type whose ports happen to be strings. Extending
// resolveDocumentedImportID to corroborate non-string segments (and to
// render them back exactly the way the provider's own import string does)
// is follow-up work, not done here - see this package's own doc comment on
// [RecordFallbackType] and the PR this test shipped with.
func TestSoleElementConflictDropsToRecordRung(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "record-fallback-solelement-conflict"), nil)

	stringAttr := func(required bool) *configschema.Attribute {
		if required {
			return &configschema.Attribute{Type: cty.String, Required: true}
		}
		return &configschema.Attribute{Type: cty.String, Optional: true}
	}
	schema := providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":                       {Type: cty.String, Computed: true},
		"security_group_id":        stringAttr(true),
		"type":                     stringAttr(true),
		"protocol":                 stringAttr(true),
		"from_port":                stringAttr(true),
		"to_port":                  stringAttr(true),
		"cidr_blocks":              {Type: cty.List(cty.String), Optional: true},
		"ipv6_cidr_blocks":         {Type: cty.List(cty.String), Optional: true},
		"prefix_list_ids":          {Type: cty.List(cty.String), Optional: true},
		"source_security_group_id": {Type: cty.String, Optional: true, Computed: true},
	}}}
	schemas := map[string]providers.Schema{"aws_security_group_rule": schema}

	if !RecordFallbackType("aws_security_group_rule", schemas) {
		t.Fatalf("RecordFallbackType(synthetic all-string aws_security_group_rule schema) = false, want true - the fixture is built so every documented import segment resolves as a top-level string, which is exactly the condition this route requires; if this is false the test below is not exercising the route it claims to")
	}

	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: schemas})
	assertNoErrors(t, diags)

	res := resolutionAt(t, result, "aws_security_group_rule.egress_all_all")
	if res.Class != ClassRecordLocated {
		t.Fatalf("egress_all_all resolved %s, want %s: cidr_blocks and ipv6_cidr_blocks are both genuinely non-empty (a SoleElement conflict), a record_store is declared, and the type's identity is now fully recordable, so it must drop to the record rung rather than refuse or guess", res.Class, ClassRecordLocated)
	}
	if res.ImportID != "" {
		t.Errorf("egress_all_all carries ImportID %q; ClassRecordLocated's identity comes from the store, never from this package, and a non-empty value here would be a wrong identity nothing verified", res.ImportID)
	}
}
