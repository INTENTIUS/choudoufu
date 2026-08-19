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

// GitHub issue #324 item 2: concat(A[*].id, B[*].id, [literal])[N], reached
// through a local value - terraform-aws-modules/security-group's own
// this_sg_id accessor. See splat.go's resolveConcatIndex.

// TestConcatSplatIndexResolvesFirstArg is the corpus shape itself: one
// splat with a real instance, one splat that provably expands to zero
// instances, and a trailing literal-list fallback - index 0 must resolve
// through the first splat's own single instance, exactly as
// local.this_sg_id does in terraform-aws-modules/security-group with
// create_sg = true (the module's own default).
func TestConcatSplatIndexResolvesFirstArg(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "concat-splat-index-security-group"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	rule := resolutionAt(t, result, `aws_security_group_rule.ingress`)
	if rule.Class != ClassParentDerived {
		t.Fatalf("aws_security_group_rule.ingress resolved %s, want PARENT_DERIVED (aws_security_group is server-assigned)", rule.Class)
	}
	want := "${aws_security_group.this[0].id}_ingress_tcp_80_80_0.0.0.0/0"
	if rule.Formula.String() != want {
		t.Errorf("formula is %q, want %q", rule.Formula.String(), want)
	}
}

// TestConcatSplatIndexResolvesSecondArg checks the cumulative-length
// bookkeeping across two non-empty splat arguments: "a" contributes one
// element at flattened position 0, "b" contributes two more at 1 and 2, so
// index 2 must land on b[1] - not b[0], and not a[0].
func TestConcatSplatIndexResolvesSecondArg(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "concat-splat-index-second-arg"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	rule := resolutionAt(t, result, `aws_security_group_rule.ingress`)
	if rule.Class != ClassParentDerived {
		t.Fatalf("aws_security_group_rule.ingress resolved %s, want PARENT_DERIVED", rule.Class)
	}
	want := "${aws_security_group.b[1].id}_ingress_tcp_80_80_0.0.0.0/0"
	if rule.Formula.String() != want {
		t.Errorf("formula is %q, want %q (index 2 should land on b[1], the second element b contributes)", rule.Formula.String(), want)
	}
}

// TestConcatSplatIndexLandsOnLiteral: both splat arguments provably expand
// to zero instances, so the provable index [0] lands on the trailing
// literal-list element instead of any resource's attribute. That is not
// identity-bearing via a marker at all - it is whatever string the
// configuration itself wrote - so the result must resolve CONCRETE from the
// literal, not refuse and not fabricate a resource reference.
func TestConcatSplatIndexLandsOnLiteral(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "concat-splat-index-literal-fallback"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	rule := resolutionAt(t, result, `aws_security_group_rule.ingress`)
	if rule.Class != ClassConcrete {
		t.Fatalf("aws_security_group_rule.ingress resolved %s, want CONCRETE (the index lands on a literal, not a resource)", rule.Class)
	}
	want := "sg-fallback_ingress_tcp_80_80_0.0.0.0/0"
	if rule.ImportID != want {
		t.Errorf("import ID is %q, want %q", rule.ImportID, want)
	}
}

// TestConcatSplatIndexOutOfRangeRefuses: concat()'s arguments provably
// contribute exactly one element in total, but the index asks for the
// sixth - concat() itself would error on this at apply time, and this
// package refuses ahead of that with its own specific reason.
func TestConcatSplatIndexOutOfRangeRefuses(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "concat-splat-index-out-of-range"), nil)

	result, diags := Resolve(context.Background(), cfg)

	if _, ok := result.Get(mustAddr(t, `aws_security_group_rule.ingress`)); ok {
		t.Fatalf("out-of-range index resolved; concat()'s arguments provably contribute only 1 element")
	}
	if !hasDiag(diags, "Identity not resolvable from configuration", "out of range") {
		t.Errorf("no refusal explaining the out-of-range index:\n%s", renderDiags(diags))
	}
}

// TestConcatSplatIndexUnrecognizedArgRefuses: the index provably falls past
// the first splat's own one instance and into a second argument that is
// neither a splat over a managed resource nor a literal list - a plain
// variable reference. How many elements that contributes is not knowable
// from configuration alone, so this must refuse with its own specific
// reason rather than guess.
func TestConcatSplatIndexUnrecognizedArgRefuses(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "concat-splat-index-unrecognized-arg"), nil)

	result, diags := Resolve(context.Background(), cfg)

	if _, ok := result.Get(mustAddr(t, `aws_security_group_rule.ingress`)); ok {
		t.Fatalf("unrecognized-argument concat() resolved; var.extra_ids's length is not knowable from configuration")
	}
	if !hasDiag(diags, "Identity not resolvable from configuration", "neither a splat over a managed resource nor a literal list") {
		t.Errorf("no refusal explaining the unrecognized argument:\n%s", renderDiags(diags))
	}
}
