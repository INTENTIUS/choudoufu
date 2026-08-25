// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/listclient"
	"github.com/intentius/choudoufu/internal/live/registry"
)

// sgRuleRoster builds a Roster mapping both aws_vpc_security_group_egress_rule
// and aws_security_group to their real CFN types (both AWS::EC2::…), so
// [identity.SameServiceAffinity] agrees they are the same AWS service - the
// real signal [identity.ParentByConvention] needs first (issue #129: the
// Terraform prefix alone, "vpc" vs "security", would refuse this pair, the
// exact reason the CFN service check exists at all).
func sgRuleRoster(t *testing.T) *registry.Roster {
	t.Helper()
	return ccRoster(t,
		map[string]string{
			"aws_vpc_security_group_egress_rule": "AWS::EC2::SecurityGroupEgress",
			"aws_security_group":                 "AWS::EC2::SecurityGroup",
		},
		nil, nil,
	)
}

// TestClassifyOrphanDestroyDependency_SecurityGroupRule is the narrow,
// pure-function proof for corpus-ecs-fargate's day2_remove unit: given a
// listed resource object and a set of resolutions to search, does
// [classifyOrphanDestroyDependency] find the right parent, the wrong one,
// or none - the same shape [TestDestroyParentDependency_Route53Record]
// proves for [destroyParentDependency]'s own, record-Components-sourced
// version.
//
// A real gauntlet run reproduced the defect this closes directly: after
// choudoufu destroyed both module.ecs_task_definition.aws_security_group.
// this[0] and its own aws_vpc_security_group_egress_rule.this["all"] in one
// apply (both proposed correctly, address-for-address matching stock's
// oracle), the rule object survived on the emulator - fully tagged,
// unchanged - because nothing ordered its own destroy call before its
// parent security group's, and a second plan then rediscovered it as a
// fresh orphan. `aws_security_group` is already one of newFakeCloud's
// default types, so no custom fixture is needed for the parent side.
func TestClassifyOrphanDestroyDependency_SecurityGroupRule(t *testing.T) {
	sgAddr := mustAddr(t, "aws_security_group.this")
	otherSGAddr := mustAddr(t, "aws_security_group.other")

	schemas, diags := listclient.ListSchemas(t.Context(), newFakeCloud())
	if diags.HasErrors() {
		t.Fatalf("building schemas from the fake cloud: %s", diags.Err())
	}
	req := Request{Roster: sgRuleRoster(t)}

	t.Run("finds the matching security group via the object's own security_group_id", func(t *testing.T) {
		res := &Result{Resolutions: []identity.Resolution{
			{Addr: sgAddr, Class: identity.ClassConcrete, ImportID: "sg-0123"},
			{Addr: otherSGAddr, Class: identity.ClassConcrete, ImportID: "sg-9999"},
		}}
		resource := cty.ObjectVal(map[string]cty.Value{
			"id":                cty.StringVal("sgr-abcd"),
			"security_group_id": cty.StringVal("sg-0123"),
		})
		got := classifyOrphanDestroyDependency(req, schemas, res, "aws_vpc_security_group_egress_rule", resource)
		if len(got) != 1 || got[0].String() != sgAddr.String() {
			t.Errorf("got %v, want exactly [%s]", got, sgAddr)
		}
	})

	t.Run("no matching security_group_id value returns nil", func(t *testing.T) {
		res := &Result{Resolutions: []identity.Resolution{
			{Addr: otherSGAddr, Class: identity.ClassConcrete, ImportID: "sg-9999"},
		}}
		resource := cty.ObjectVal(map[string]cty.Value{
			"id":                cty.StringVal("sgr-abcd"),
			"security_group_id": cty.StringVal("sg-0123"),
		})
		got := classifyOrphanDestroyDependency(req, schemas, res, "aws_vpc_security_group_egress_rule", resource)
		if got != nil {
			t.Errorf("got %v, want nil - no resolution names this security_group_id", got)
		}
	})

	t.Run("resource with no parent-shaped attribute returns nil", func(t *testing.T) {
		res := &Result{Resolutions: []identity.Resolution{
			{Addr: sgAddr, Class: identity.ClassConcrete, ImportID: "sg-0123"},
		}}
		resource := cty.ObjectVal(map[string]cty.Value{
			"id": cty.StringVal("sgr-abcd"),
		})
		got := classifyOrphanDestroyDependency(req, schemas, res, "aws_vpc_security_group_egress_rule", resource)
		if got != nil {
			t.Errorf("got %v, want nil - the object carries no parent-shaped argument at all", got)
		}
	})

	t.Run("cty.NilVal resource (no listed object) returns nil", func(t *testing.T) {
		res := &Result{Resolutions: []identity.Resolution{
			{Addr: sgAddr, Class: identity.ClassConcrete, ImportID: "sg-0123"},
		}}
		got := classifyOrphanDestroyDependency(req, schemas, res, "aws_vpc_security_group_egress_rule", cty.NilVal)
		if got != nil {
			t.Errorf("got %v, want nil - fileTaggingCandidate and scanTypeCloudControl's own orphans never carry a Resource", got)
		}
	})

	t.Run("a marked attribute is refused, never unmarked and read", func(t *testing.T) {
		res := &Result{Resolutions: []identity.Resolution{
			{Addr: sgAddr, Class: identity.ClassConcrete, ImportID: "sg-0123"},
		}}
		resource := cty.ObjectVal(map[string]cty.Value{
			"id":                cty.StringVal("sgr-abcd"),
			"security_group_id": cty.StringVal("sg-0123").Mark("sensitive"),
		})
		got := classifyOrphanDestroyDependency(req, schemas, res, "aws_vpc_security_group_egress_rule", resource)
		if got != nil {
			t.Errorf("got %v, want nil - a marked value must be refused, not unmarked and read (internal/live/marksafe)", got)
		}
	})
}

// TestClassifyOrphans_UndeclaredSiblingsGetOrderedDestroy is the
// integration-level proof: classifyOrphans itself, given two orphans of a
// removed block - a security group and its own egress rule, the shape this
// unit's real gauntlet run reproduced - sets DestroyDependsOn on the rule's
// own resolution pointing at the security group's, and leaves the security
// group's own resolution with none (it has no parent of its own left in
// this pass).
func TestClassifyOrphans_UndeclaredSiblingsGetOrderedDestroy(t *testing.T) {
	schemas, diags := listclient.ListSchemas(t.Context(), newFakeCloud())
	if diags.HasErrors() {
		t.Fatalf("building schemas from the fake cloud: %s", diags.Err())
	}

	res := &Result{
		Orphans: []OwnedResource{
			{
				TypeName:   "aws_security_group",
				ImportID:   "sg-0123",
				Marker:     `module.x.aws_security_group.this`,
				Normalized: `module.x.aws_security_group.this`,
				Swept:      true,
			},
			{
				TypeName:   "aws_vpc_security_group_egress_rule",
				ImportID:   "sgr-abcd",
				Marker:     `module.x.aws_vpc_security_group_egress_rule.this:all`,
				Normalized: `module.x.aws_vpc_security_group_egress_rule.this:all`,
				Swept:      true,
				Resource: cty.ObjectVal(map[string]cty.Value{
					"id":                cty.StringVal("sgr-abcd"),
					"security_group_id": cty.StringVal("sg-0123"),
				}),
			},
		},
	}

	diags = classifyOrphans(Request{Estate: "sg-rule-order", Roster: sgRuleRoster(t)}, schemas, res)
	if diags.HasErrors() {
		t.Fatalf("classifying the sibling pair reported errors: %s", diags.Err())
	}
	if len(res.Resolutions) != 2 {
		t.Fatalf("got %d resolutions, want 2:\n%+v", len(res.Resolutions), res.Resolutions)
	}

	var sgResolved, ruleResolved *identity.Resolution
	for i := range res.Resolutions {
		r := &res.Resolutions[i]
		switch r.Type() {
		case "aws_security_group":
			sgResolved = r
		case "aws_vpc_security_group_egress_rule":
			ruleResolved = r
		}
	}
	if sgResolved == nil || ruleResolved == nil {
		t.Fatalf("did not resolve both the security group and its rule: %+v", res.Resolutions)
	}
	if len(sgResolved.DestroyDependsOn) != 0 {
		t.Errorf("the security group's own resolution carries a DestroyDependsOn (%v), want none - it has no parent left in this pass", sgResolved.DestroyDependsOn)
	}
	if len(ruleResolved.DestroyDependsOn) != 1 || ruleResolved.DestroyDependsOn[0].String() != sgResolved.Addr.String() {
		t.Errorf("the rule's own DestroyDependsOn = %v, want exactly [%s] - it must be destroyed before its own security group, which this same run also destroys", ruleResolved.DestroyDependsOn, sgResolved.Addr)
	}
}
