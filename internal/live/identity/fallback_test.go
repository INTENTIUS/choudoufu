// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"testing"
)

// TestTryFallbackSelectsTheArgumentTheLanguageWould asserts on the rendered
// identity formula, not on whether resolution returned true: what matters is
// WHICH live object each block is bound to, and only the rendered string says
// that. Every argument after the one that must win names an undeclared
// resource, so a formula naming the right route table also proves the later
// arguments were never consulted.
func TestTryFallbackSelectsTheArgumentTheLanguageWould(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "try-fallback-select"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	cases := []struct {
		name string
		want string
	}{
		// The first argument's instance exists, so it is selected even
		// though its value is unknown until apply.
		{"first_arg_lives", "${aws_subnet.this.id}/${aws_route_table.primary[0].id}"},
		// The first argument indexes a resource with no instances, which
		// raises on every run, so the second argument is what the language
		// evaluates.
		{"falls_through", "${aws_subnet.other.id}/${aws_route_table.primary[0].id}"},
		// A bare reference to an unrepeated resource: the no-key instance.
		{"bare_reference", "${aws_subnet.third.id}/${aws_route_table.solo.id}"},
	}
	for _, tc := range cases {
		res := resolutionAt(t, result, "aws_route_table_association."+tc.name)
		if res.Class != ClassParentDerived {
			t.Errorf("%s resolved %s, want PARENT_DERIVED", tc.name, res.Class)
			continue
		}
		if got := res.Formula.String(); got != tc.want {
			t.Errorf("%s formula is %q, want %q", tc.name, got, tc.want)
		}
	}

	for _, d := range diags {
		if d.Description().Summary == "Reference to undeclared resource" {
			t.Fatalf("an argument past the selected one was consulted: %s", d.Description().Detail)
		}
	}
}

// TestTryFallbackRefusesWhatExpansionCannotDecide is the danger half. Both
// blocks below would resolve to a perfectly plausible-looking identity if the
// rule treated "the instance exists" as "this argument is the one selected",
// and both would be wrong: a map index can raise at apply after a plan that
// could not see it, and a function call over another resource is not a
// question about expansion at all.
//
// The assertion is that no identity was produced for either block, which is
// the only statement that rules out a wrong marker; a refusal count alone
// would pass even if one of them had quietly resolved.
func TestTryFallbackRefusesWhatExpansionCannotDecide(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "try-fallback-undecidable"), nil)

	result, diags := Resolve(context.Background(), cfg)
	if !diags.HasErrors() {
		t.Fatalf("expected refusals; neither argument chain is decidable from expansion")
	}

	for _, name := range []string{"deep_index", "wrapped"} {
		addr := "aws_route_table_association." + name
		if res, ok := result.Get(mustAddr(t, addr)); ok {
			t.Errorf("%s resolved to %q; which try() argument applies is not decidable here",
				name, res.ImportID+res.Formula.String())
		}
		if !hasDiag(diags, "Identity not resolvable from configuration", addr) {
			t.Errorf("no refusal naming %s:\n%s", name, renderDiags(diags))
		}
	}
}
