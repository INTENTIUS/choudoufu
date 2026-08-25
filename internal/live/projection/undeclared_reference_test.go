// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestUndeclaredSiblingReferenceSetsDestroyOrder is
// gauntlet:corpus-autoscaling-complete/day2_remove's own reproduction: two
// sibling undeclared orphans (no resource block for either, exactly what
// a day2_remove block deletion leaves behind for BOTH the ASG and its
// launch template - see identity.Resolution.Undeclared) where one's own
// live value names the other's import identity, an ASG's launch template
// id naming its launch template's own id - the real provider's shape is a
// nested `launch_template { id = ... }` block; this caricature schema has
// no nested-block support (see fakeAttrs) so launch_template_id stands in
// as a flat attribute, which is enough because
// [deriveUndeclaredReferenceEdges]'s match is a generic string-leaf scan
// with no attribute name in it at all.
//
// The oracle is the AWS API's own destroy ordering constraint, confirmed
// against floci with no tofu in the loop while building this unit: deleting
// a launch template while an ASG still names it fails with "ValidationError:
// The specified launch template does not exist." This test is the
// value-level assertion that the fix actually produces the edge the destroy
// graph needs (internal/tofu/transform_destroy_edge.go's
// AttachDependenciesTransformer reads exactly this field), not merely that
// the run succeeds.
func TestUndeclaredSiblingReferenceSetsDestroyOrder(t *testing.T) {
	cfg := loadConfig(t, estateDir(t))

	cloud := newFakeCloud()
	cloud.put("aws_launch_template", "lt-0123", map[string]string{
		"id": "lt-0123", "name": "web",
	})
	cloud.put("aws_autoscaling_group", "asg-web", map[string]string{
		"id": "asg-web", "name": "web", "launch_template_id": "lt-0123",
	})

	asg := mustAddr(t, `aws_autoscaling_group.web`)
	lt := mustAddr(t, `aws_launch_template.web`)

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{Addr: asg, Class: identity.ClassConcrete, ImportID: "asg-web", Undeclared: true},
		{Addr: lt, Class: identity.ClassConcrete, ImportID: "lt-0123", Undeclared: true},
	}, cloud.providers(t))
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{`aws_autoscaling_group.web`, `aws_launch_template.web`})

	mod := res.State.Module(addrs.RootModuleInstance)
	if mod == nil {
		t.Fatal("the projection has no root module")
	}

	asgInst := mod.ResourceInstance(asg.Resource)
	if asgInst == nil || asgInst.Current == nil {
		t.Fatalf("the ASG did not materialize into the state:\n%s", res)
	}
	wantDep := lt.ConfigResource().String()
	found := false
	for _, d := range asgInst.Current.Dependencies {
		if d.String() == wantDep {
			found = true
		}
	}
	if !found {
		t.Fatalf("aws_autoscaling_group.web's Dependencies do not include %s (its launch template): got %v - the destroy graph will not know to destroy the ASG before the template", wantDep, asgInst.Current.Dependencies)
	}

	// The launch template must NOT get a reverse edge onto the ASG: only
	// the side whose own live value names the other gets one, or the
	// destroy graph would cycle.
	ltInst := mod.ResourceInstance(lt.Resource)
	if ltInst != nil && ltInst.Current != nil {
		reverseDep := asg.ConfigResource().String()
		for _, d := range ltInst.Current.Dependencies {
			if d.String() == reverseDep {
				t.Fatalf("aws_launch_template.web was given a reverse dependency on the ASG (%v) - that would cycle the destroy graph", ltInst.Current.Dependencies)
			}
		}
	}
}

// TestUndeclaredSiblingReferenceNeedsAnActualMatch is the mutation check:
// change the launch template's own live id so it no longer equals what the
// ASG's object names, and the edge this fix adds must disappear. Proves
// [deriveUndeclaredReferenceEdges] is reading the live values rather than
// wiring any two same-batch undeclared orphans together unconditionally,
// which would be exactly as wrong as the type-name heuristic HANDOFF rules
// out - it would invent edges (and destroy-order constraints, and cycles)
// between resources that do not actually reference each other.
func TestUndeclaredSiblingReferenceNeedsAnActualMatch(t *testing.T) {
	cfg := loadConfig(t, estateDir(t))

	cloud := newFakeCloud()
	// The launch template's real id no longer matches what the ASG names -
	// as if the ASG's own launch_template_id pointed at a THIRD launch
	// template this plan never touches.
	cloud.put("aws_launch_template", "lt-0123", map[string]string{
		"id": "lt-0123", "name": "web",
	})
	cloud.put("aws_autoscaling_group", "asg-web", map[string]string{
		"id": "asg-web", "name": "web", "launch_template_id": "lt-DIFFERENT",
	})

	asg := mustAddr(t, `aws_autoscaling_group.web`)
	lt := mustAddr(t, `aws_launch_template.web`)

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{Addr: asg, Class: identity.ClassConcrete, ImportID: "asg-web", Undeclared: true},
		{Addr: lt, Class: identity.ClassConcrete, ImportID: "lt-0123", Undeclared: true},
	}, cloud.providers(t))
	assertNoErrors(t, diags)

	mod := res.State.Module(addrs.RootModuleInstance)
	if mod == nil {
		t.Fatal("the projection has no root module")
	}
	asgInst := mod.ResourceInstance(asg.Resource)
	if asgInst == nil || asgInst.Current == nil {
		t.Fatalf("the ASG did not materialize into the state:\n%s", res)
	}
	if len(asgInst.Current.Dependencies) != 0 {
		t.Fatalf("aws_autoscaling_group.web got Dependencies %v with no actual reference in its live value - the match is not load-bearing", asgInst.Current.Dependencies)
	}
}

// TestContainsStringValueFindsAnyStringLeaf pins the "generic, not a
// type-name heuristic" property directly: the target can sit anywhere in
// the value's structure, not only at the top level, because a real
// provider's own reference (aws_autoscaling_group's launch_template block)
// is nested.
func TestContainsStringValueFindsAnyStringLeaf(t *testing.T) {
	nested := cty.ObjectVal(map[string]cty.Value{
		"id": cty.StringVal("asg-web"),
		"launch_template": cty.ListVal([]cty.Value{
			cty.ObjectVal(map[string]cty.Value{
				"id":      cty.StringVal("lt-0123"),
				"version": cty.StringVal("$Latest"),
			}),
		}),
	})
	if !containsStringValue(nested, "lt-0123") {
		t.Error("containsStringValue did not find a string nested inside a list of objects")
	}
	if containsStringValue(nested, "lt-9999") {
		t.Error("containsStringValue reported a match for a string that is not present anywhere in the value")
	}
	if containsStringValue(nested, "") {
		t.Error("containsStringValue matched an empty target, which cannot identify anything")
	}
}

// TestContainsStringValueSkipsMarkedValues is the marksafe discipline
// asserted by value: a sensitive string equal to the target must not be
// read with AsString, which panics on a marked receiver
// (internal/live/marksafe), and must not be reported as a match either -
// refusing is the correct behaviour here exactly as it is for an identity
// component, per this function's own doc comment.
func TestContainsStringValueSkipsMarkedValues(t *testing.T) {
	marked := cty.ObjectVal(map[string]cty.Value{
		"id": cty.StringVal("lt-0123").Mark(marks.Sensitive),
	})

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("containsStringValue panicked on a marked value instead of refusing it: %v", r)
		}
	}()
	if containsStringValue(marked, "lt-0123") {
		t.Error("containsStringValue matched a marked (sensitive) value - it must refuse rather than unmark and compare")
	}
}

// TestUndeclaredSiblingSharedIdentityIsNotAReference is the regression
// this fix's own second finding, in the same session as the ASG/launch
// template case: aws_vpc_security_group_rules_exclusive's WHOLE identity
// is its security group's own id (identity.Component.IdentityAttr: "*"
// over security_group_id), so a naive "does from's value contain to's
// ImportID" scan finds the security group's own id sitting inside the
// security group's own live value - trivially true - and reads it as "the
// security group references rules_exclusive," backwards from
// [destroyParentDependency]'s own, correctly-directed
// rules_exclusive-depends-on-the-security-group edge for the identical
// pair. Two edges between the same two nodes, pointing opposite ways, is
// exactly what cycled corpus-security-group-complete's day2_remove plan
// ("Error: Cycle: ...aws_security_group.this[0] (destroy),
// ...aws_vpc_security_group_rules_exclusive.this[0] (destroy)") once this
// unit's OTHER fix (moved.Newest) started correctly grouping
// rules_exclusive under the same module address as its security group for
// the first time.
//
// This test proves [deriveUndeclaredReferenceEdges] does not add ITS OWN
// half of that cycle: two siblings sharing one identity string get no
// edge in either direction from this mechanism, leaving the
// correctly-directed edge (destroyParentDependency, a discovery-level
// mechanism this projection-level test does not invoke) as the only one
// that exists.
func TestUndeclaredSiblingSharedIdentityIsNotAReference(t *testing.T) {
	cfg := loadConfig(t, estateDir(t))

	cloud := newFakeCloud()
	cloud.put("aws_security_group", "sg-0123", map[string]string{
		"id": "sg-0123", "name": "postgresql", "vpc_id": "vpc-abc",
	})
	cloud.put("aws_vpc_security_group_rules_exclusive", "sg-0123", map[string]string{
		"id": "sg-0123",
	})

	sg := mustAddr(t, `aws_security_group.postgresql`)
	rex := mustAddr(t, `aws_vpc_security_group_rules_exclusive.postgresql`)

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{Addr: sg, Class: identity.ClassConcrete, ImportID: "sg-0123", Undeclared: true},
		{Addr: rex, Class: identity.ClassConcrete, ImportID: "sg-0123", Undeclared: true},
	}, cloud.providers(t))
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{`aws_security_group.postgresql`, `aws_vpc_security_group_rules_exclusive.postgresql`})

	mod := res.State.Module(addrs.RootModuleInstance)
	if mod == nil {
		t.Fatal("the projection has no root module")
	}
	sgInst := mod.ResourceInstance(sg.Resource)
	if sgInst == nil || sgInst.Current == nil {
		t.Fatalf("the security group did not materialize into the state:\n%s", res)
	}
	if len(sgInst.Current.Dependencies) != 0 {
		t.Errorf("aws_security_group.postgresql got Dependencies %v from sharing an identity string with a sibling - this direction is backwards from destroyParentDependency's own and would cycle the destroy graph", sgInst.Current.Dependencies)
	}
	rexInst := mod.ResourceInstance(rex.Resource)
	if rexInst == nil || rexInst.Current == nil {
		t.Fatalf("rules_exclusive did not materialize into the state:\n%s", res)
	}
	if len(rexInst.Current.Dependencies) != 0 {
		t.Errorf("aws_vpc_security_group_rules_exclusive.postgresql got Dependencies %v from this mechanism - that edge belongs to destroyParentDependency, not deriveUndeclaredReferenceEdges, and adding it here too would be a duplicate, differently-computed source of truth", rexInst.Current.Dependencies)
	}
}

// TestUndeclaredSiblingMutualMatchIsNotAReference is the second shape
// found in the same session, in the SAME estate
// (corpus-security-group-complete) as the direct case above: an ingress
// rule's own live value contains its security group's id (its own
// security_group_id attribute, a perfectly ordinary thing for a rule to
// carry), and aws_vpc_security_group_rules_exclusive's own required
// arguments - ingress_rule_ids/egress_rule_ids, confirmed against the
// provider's docs - are themselves lists of the exact rule ids it
// manages. So the match fires in BOTH directions between the rule and
// rules_exclusive: the rule's value names the security group (which
// rules_exclusive's ImportID happens to equal), and rules_exclusive's own
// value separately names the rule directly. That reproduced as a real
// "Error: Cycle: ...egress_rule..., ...ingress_rule...,
// ...rules_exclusive..." with no security group in it at all -
// TestUndeclaredSiblingSharedIdentityIsNotAReference's direct-pair shape
// alone did not cover it, because the two candidate edges here are for
// two DIFFERENT reasons, not the same trivial self-containment.
//
// The general rule this proves: a candidate found in both directions
// between two siblings is dropped in both directions, regardless of why
// each direction matched.
func TestUndeclaredSiblingMutualMatchIsNotAReference(t *testing.T) {
	cfg := loadConfig(t, estateDir(t))

	cloud := newFakeCloud()
	cloud.put("aws_security_group", "sg-0123", map[string]string{
		"id": "sg-0123", "name": "postgresql", "vpc_id": "vpc-abc",
	})
	cloud.put("aws_vpc_security_group_rules_exclusive", "sg-0123", map[string]string{
		"id": "sg-0123", "security_group_id": "sg-0123",
		// Stands in for one element of the real ingress_rule_ids list -
		// see fakeAttrs' own comment for why the caricature schema has no
		// list support.
		"managed_rule_id": "sgr-ABC",
	})
	cloud.put("aws_vpc_security_group_ingress_rule", "sgr-ABC", map[string]string{
		"id": "sgr-ABC", "security_group_rule_id": "sgr-ABC", "security_group_id": "sg-0123",
		"cidr_ipv4": "10.0.0.0/16", "from_port": "5432", "to_port": "5432", "ip_protocol": "tcp",
	})

	sg := mustAddr(t, `aws_security_group.postgresql`)
	rex := mustAddr(t, `aws_vpc_security_group_rules_exclusive.postgresql`)
	ingress := mustAddr(t, `aws_vpc_security_group_ingress_rule.primary`)

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{Addr: sg, Class: identity.ClassConcrete, ImportID: "sg-0123", Undeclared: true},
		{Addr: rex, Class: identity.ClassConcrete, ImportID: "sg-0123", Undeclared: true},
		{Addr: ingress, Class: identity.ClassConcrete, ImportID: "sgr-ABC", Undeclared: true},
	}, cloud.providers(t))
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{
		`aws_security_group.postgresql`,
		`aws_vpc_security_group_ingress_rule.primary`,
		`aws_vpc_security_group_rules_exclusive.postgresql`,
	})

	mod := res.State.Module(addrs.RootModuleInstance)
	if mod == nil {
		t.Fatal("the projection has no root module")
	}
	// The security group and rules_exclusive matched each other in BOTH
	// directions (mutual - see this test's own doc comment) and so get
	// nothing from this mechanism. The ingress rule and rules_exclusive
	// ALSO matched each other in both directions and so get nothing
	// between THEM either. What is left standing is the one genuinely
	// one-directional match: the ingress rule's own value names the
	// security group, and the security group's own value does not name
	// the rule back - a real reference, correctly kept.
	for _, addr := range []addrs.AbsResourceInstance{sg, rex} {
		inst := mod.ResourceInstance(addr.Resource)
		if inst == nil || inst.Current == nil {
			t.Fatalf("%s did not materialize into the state:\n%s", addr, res)
		}
		if len(inst.Current.Dependencies) != 0 {
			t.Errorf("%s got Dependencies %v from a mutual match (both siblings' live values name each other) - a match found in both directions is never a directed reference", addr, inst.Current.Dependencies)
		}
	}
	ingressInst := mod.ResourceInstance(ingress.Resource)
	if ingressInst == nil || ingressInst.Current == nil {
		t.Fatalf("%s did not materialize into the state:\n%s", ingress, res)
	}
	wantDep := sg.ConfigResource().String()
	if len(ingressInst.Current.Dependencies) != 1 || ingressInst.Current.Dependencies[0].String() != wantDep {
		t.Errorf("%s got Dependencies %v, want exactly [%s] - its own one-directional reference to the security group, with no edge to rules_exclusive (that pair is mutual)", ingress, ingressInst.Current.Dependencies, wantDep)
	}
}
