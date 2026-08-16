// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package configs

import (
	"fmt"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/zclconf/go-cty/cty"
)

// carryScope builds a static scope over src and returns the diagnostics
// evaluating local.<name> produced. The variables function refuses
// everything, matching the other tests in this package: none of these
// scenarios reads a variable.
func carryScope(t *testing.T, src map[string]string, top StaticIdentifier, name string) tfdiags.Diagnostics {
	t.Helper()
	p := testParser(src)
	call := NewStaticModuleCall(
		addrs.RootModule, hcl.Range{},
		func(v *Variable) (cty.Value, hcl.Diagnostics) {
			var diags tfdiags.Diagnostics
			diags = diags.Append(fmt.Errorf("no variables here"))
			return cty.DynamicVal, diags.ToHCL()
		},
		".",
		"irrelevant",
	)
	mod, diags := p.LoadConfigDir(".", call)
	assertNoDiagnostics(t, diags)

	scope := newStaticScope(NewStaticEvaluator(mod, call), top)
	_, moreDiags := scope.Data.GetLocalValue(t.Context(), addrs.LocalValue{Name: name}, tfdiags.SourceRange{Filename: "test.tf"})
	if !moreDiags.HasErrors() {
		t.Fatal("unexpected success; want errors")
	}
	return moreDiags
}

// split returns the leading refusals and the trailing "Unable to compute
// static value" diagnostics, in the order they were raised.
func split(diags tfdiags.Diagnostics) (leaders, trailers []tfdiags.Diagnostic) {
	for _, d := range diags {
		if d.Severity() != tfdiags.Error {
			continue
		}
		if d.Description().Summary == "Unable to compute static value" {
			trailers = append(trailers, d)
			continue
		}
		leaders = append(leaders, d)
	}
	return leaders, trailers
}

// TestEnhanceDiagnosticsCarriesTheReference pins that the trailing "Unable
// to compute static value" restates the SAME refusal its leader carries.
//
// The anchor is external to the carrying rule on purpose: every expectation
// below is read off the leading diagnostic, which
// [staticScopeData.StaticValidateReferences] built from the reference itself
// with no knowledge of enhanceDiagnostics. Mutating the carry rule to agree
// with itself cannot pass this - the leader would have to change too, and it
// is built somewhere else.
func TestEnhanceDiagnosticsCarriesTheReference(t *testing.T) {
	top := StaticIdentifier{Subject: "aws_instance.web.subnet_id"}

	t.Run("data source through one local", func(t *testing.T) {
		diags := carryScope(t, map[string]string{
			"test.tf": `
				locals {
					subnet = data.aws_subnet.selected.id
				}
			`,
		}, top, "subnet")

		leaders, trailers := split(diags)
		if len(leaders) != 1 || len(trailers) != 1 {
			t.Fatalf("want 1 leader and 1 trailer, got %d and %d: %s", len(leaders), len(trailers), diags.Err())
		}
		want := tfdiags.ExtraInfo[RefusedReference](leaders[0])
		if want.Subject == nil {
			t.Fatalf("the leader carries no reference; this test's anchor is gone")
		}
		got := tfdiags.ExtraInfo[RefusedReference](trailers[0])
		if got.Subject == nil {
			t.Fatalf("the trailer carries no reference")
		}
		if !sameRefusedReference(want, got) {
			t.Errorf("trailer names a different reference\nleader:  %#v\ntrailer: %#v", want, got)
		}
		// The category rides the same unwrap chain, which is what the
		// check layer's re-homing actually reads.
		if c := tfdiags.ExtraInfo[ReferenceCategory](trailers[0]); c != want.Category {
			t.Errorf("trailer category = %q, leader = %q", c, want.Category)
		}
		// NeededBy is the one field that must NOT be copied: it names
		// what needed the reference at THIS frame.
		if got.NeededBy != top.String() {
			t.Errorf("trailer NeededBy = %q, want %q", got.NeededBy, top.String())
		}
	})

	t.Run("propagates through a chain of locals", func(t *testing.T) {
		diags := carryScope(t, map[string]string{
			"test.tf": `
				locals {
					a = data.aws_subnet.selected.id
					b = local.a
					c = local.b
				}
			`,
		}, top, "c")

		leaders, trailers := split(diags)
		if len(leaders) != 1 {
			t.Fatalf("want 1 leader, got %d: %s", len(leaders), diags.Err())
		}
		if len(trailers) < 2 {
			t.Fatalf("want the chain to produce several trailers, got %d", len(trailers))
		}
		want := tfdiags.ExtraInfo[RefusedReference](leaders[0])
		for i, tr := range trailers {
			got := tfdiags.ExtraInfo[RefusedReference](tr)
			if got.Subject == nil {
				t.Fatalf("trailer %d carries no reference; the chain stopped propagating", i)
			}
			if !sameRefusedReference(want, got) {
				t.Errorf("trailer %d names a different reference\nleader:  %#v\ntrailer: %#v", i, want, got)
			}
		}
	})

	t.Run("two different references carry nothing", func(t *testing.T) {
		diags := carryScope(t, map[string]string{
			"test.tf": `
				locals {
					both = "${data.aws_subnet.a.id}-${data.aws_subnet.b.id}"
				}
			`,
		}, top, "both")

		leaders, trailers := split(diags)
		if len(leaders) != 2 {
			t.Fatalf("want 2 leaders, got %d: %s", len(leaders), diags.Err())
		}
		if len(trailers) != 1 {
			t.Fatalf("want 1 trailer, got %d", len(trailers))
		}
		if got := tfdiags.ExtraInfo[RefusedReference](trailers[0]); got.Subject != nil {
			t.Errorf("trailer carried %v, but two independent refusals disagree and it must carry nothing", got.Subject)
		}
	})

	t.Run("an unstructured error carries nothing", func(t *testing.T) {
		// local.broken fails inside a function call, and that diagnostic
		// has no RefusedReference at all. Carrying local.subnet's data
		// source past it would report the outer frame as one clean
		// data-source dependency the read phase resolves, when it also has
		// an error no read can fix.
		//
		// Both branches have to be reached by reference rather than
		// written inline: a refused reference stops lang.Scope.EvalExpr
		// before the expression is evaluated at all, so an inline
		// jsondecode next to an inline data source never runs.
		diags := carryScope(t, map[string]string{
			"test.tf": `
				locals {
					subnet = data.aws_subnet.a.id
					broken = jsondecode("{")
					mixed  = "${local.subnet}${local.broken}"
				}
			`,
		}, top, "mixed")

		leaders, trailers := split(diags)
		if len(leaders) != 2 {
			t.Fatalf("want both branches to raise, got %d leaders: %s", len(leaders), diags.Err())
		}
		if got := tfdiags.ExtraInfo[RefusedReference](trailers[len(trailers)-1]); got.Subject != nil {
			t.Errorf("outermost trailer carried %v past an error with no structured reference", got.Subject)
		}
	})
}
