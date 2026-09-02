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

// GitHub issue #196's second half. Measured against
// live/corpus-manifest.json, 60 of the 95 sites blocked by "Identity not
// resolvable from configuration" are join("", <resource>.*.<attr>) reached
// through a conditional that #196's first half already resolves - twelve
// identical sites in each of five terraform-aws-modules/eks examples, all
// of them in that module's own local.tf. Every one of those splats is over
// a resource whose count is `<bool> ? 1 : 0`, which this package already
// evaluates. See splat.go's comment for the schema-backed per-entry
// before/after, and for why those 60 do not simply reappear under
// "Unresolvable identity" the way #196's first half's did.
//
// See splat.go for why the rule is stated as arity rather than as
// knowledge about join().

// TestSplatArityOneResolves is the corpus shape plus the two things the
// arity claim implies and the join-specific reading would not: that the
// separator cannot matter at arity one, and that one() is the same claim.
func TestSplatArityOneResolves(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "splat-arity-one"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	// count = var.create_primary ? 1 : 0 with the variable true, so the
	// one element is instance [0] - the formula naming primary[0] rather
	// than primary is what proves the splat was decomposed against the
	// resource's own expansion and not merely unwrapped.
	fromJoin := resolutionAt(t, result, `aws_route_table_association.from_join`)
	if fromJoin.Class != ClassParentDerived {
		t.Fatalf("from_join resolved %s, want PARENT_DERIVED (both parents are server-assigned)", fromJoin.Class)
	}
	if want := "${aws_subnet.this.id}/${aws_route_table.primary[0].id}"; fromJoin.Formula.String() != want {
		t.Errorf("from_join formula is %q, want %q", fromJoin.Formula.String(), want)
	}

	// aws_route_table.solo has no count at all, so its splat is the
	// one-element wrap of an unrepeated resource and its instance has no
	// key. The separator here is "-", and it must make no difference.
	nonEmpty := resolutionAt(t, result, `aws_route_table_association.nonempty_separator`)
	if want := "${aws_subnet.this.id}/${aws_route_table.solo.id}"; nonEmpty.Formula.String() != want {
		t.Errorf("nonempty_separator formula is %q, want %q - a non-empty separator cannot appear in a one-element join", nonEmpty.Formula.String(), want)
	}

	fromOne := resolutionAt(t, result, `aws_route_table_association.from_one`)
	if want := "${aws_subnet.other.id}/${aws_route_table.primary[0].id}"; fromOne.Formula.String() != want {
		t.Errorf("from_one formula is %q, want %q", fromOne.Formula.String(), want)
	}

	// The unselected branch of from_join's conditional names
	// aws_route_table.ghost, which is not declared. A resolved formula that
	// did not also raise this proves the branch was never consulted.
	for _, d := range diags {
		if d.Description().Summary == "Reference to undeclared resource" {
			t.Fatalf("the unselected branch was consulted: %s", d.Description().Detail)
		}
	}
}

// TestSplatArityMultiRefuses is danger case 1 and 3 together: two
// instances, once with a non-empty separator and once with an empty one.
// Both must refuse. An empty separator is not permission to concatenate
// two live objects' IDs into one identity.
func TestSplatArityMultiRefuses(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "splat-arity-multi"), nil)

	result, diags := Resolve(context.Background(), cfg)

	if !diags.HasErrors() {
		t.Fatalf("expected a refusal; aws_route_table.pair has count = 2")
	}
	for _, name := range []string{"multi", "multi_empty_separator"} {
		if _, ok := result.Get(mustAddr(t, `aws_route_table_association.`+name)); ok {
			t.Errorf("%s resolved; its route_table_id joins two route tables' IDs", name)
		}
		if !hasDiag(diags, "Identity not resolvable from configuration", `aws_route_table_association.`+name) {
			t.Errorf("no refusal naming %s:\n%s", name, renderDiags(diags))
		}
	}
	if !hasDiag(diags, "Identity not resolvable from configuration", "2 instances") {
		t.Errorf("the refusal does not say how many instances the splat had:\n%s", renderDiags(diags))
	}
}

// TestSplatArityZeroRefuses: the splat is empty, so join() of it is "".
// OpenTofu accepts that; an identity component that is the empty string
// names no live object, so this refuses rather than resolving to one.
func TestSplatArityZeroRefuses(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "splat-arity-zero"), nil)

	result, diags := Resolve(context.Background(), cfg)

	if !diags.HasErrors() {
		t.Fatalf("expected a refusal; aws_route_table.primary expands to no instances")
	}
	if _, ok := result.Get(mustAddr(t, `aws_route_table_association.empty`)); ok {
		t.Fatalf("empty resolved; join(\"\", <no instances>) is the empty string")
	}
	if !hasDiag(diags, "Identity not resolvable from configuration", "no instances at all") {
		t.Errorf("no refusal explaining the empty splat:\n%s", renderDiags(diags))
	}
}

// TestSplatUnknownCountRefuses is danger case 2: the source resource's
// count is not statically known, so the splat's arity is not known either.
// "Not known" must not collapse into "assume one".
func TestSplatUnknownCountRefuses(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "splat-unknown-count"), nil)

	result, diags := Resolve(context.Background(), cfg)

	if !diags.HasErrors() {
		t.Fatalf("expected a refusal; var.table_count is required and unset")
	}
	if _, ok := result.Get(mustAddr(t, `aws_route_table_association.unknown`)); ok {
		t.Fatalf("unknown resolved; how many elements the splat has is not knowable from this configuration")
	}
}

// TestSplatForEachKeyedRefuses: the source is keyed by strings and happens
// to have exactly one instance, so an arity-only check would admit it.
// OpenTofu does not splat a map of instances, and this package will not
// invent the ordering that would let it.
func TestSplatForEachKeyedRefuses(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "splat-foreach-keyed"), nil)

	result, diags := Resolve(context.Background(), cfg)

	if !diags.HasErrors() {
		t.Fatalf("expected a refusal; aws_route_table.keyed is for_each'd, not counted")
	}
	if _, ok := result.Get(mustAddr(t, `aws_route_table_association.keyed`)); ok {
		t.Fatalf("keyed resolved; a splat over a string-keyed resource is not a shape this package reads")
	}
	// The generic "cannot be passed through functions or operators" refusal,
	// not an arity one: splatTargets must have declined the shape outright
	// rather than counted the single string-keyed instance and admitted it.
	if !hasDiag(diags, "Identity not resolvable from configuration", "cannot be passed through functions or operators") {
		t.Errorf("expected the generic function refusal, not an arity verdict:\n%s", renderDiags(diags))
	}
	if hasDiag(diags, "Identity not resolvable from configuration", "reduces a list of another resource's attributes") {
		t.Errorf("the arity rule engaged on a string-keyed splat:\n%s", renderDiags(diags))
	}
}

// TestSplatSelectedAttributeComputedRefuses is danger case 4: the arity
// rule succeeds, and the attribute it then reads is Computed in the
// provider's schema. It must refuse at exactly the boundary
// [resolver.siblingLiteralExpr] already draws for a bare reference (#220) -
// reaching an attribute through a splat and a join is not a way past a
// schema check.
func TestSplatSelectedAttributeComputedRefuses(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "splat-computed-boundary"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: siblingTestSchemas()})

	literal := resolutionAt(t, result, `aws_route53_record.reads_literal`)
	if literal.Class != ClassConcrete {
		t.Fatalf("reads_literal resolved %s, want CONCRETE; literal_val is a plain string the sibling's own block wrote", literal.Class)
	}
	if want := "Z1_hello_TXT"; literal.ImportID != want {
		t.Errorf("reads_literal resolved to %q, want %q", literal.ImportID, want)
	}

	if _, ok := result.Get(mustAddr(t, `aws_route53_record.reads_computed`)); ok {
		t.Fatalf("reads_computed resolved; computed_val is Computed and stays refused however it is reached")
	}
	if !hasDiag(diags, "Not an identity attribute", "computed_val") {
		t.Errorf("no \"Not an identity attribute\" diagnostic naming computed_val:\n%s", renderDiags(diags))
	}
}
