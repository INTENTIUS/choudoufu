// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
)

// GitHub issue #324 item 1: element(coalescelist(A[*].id, B[*].id), idx),
// reached directly (not through a local) - terraform-aws-modules/vpc's own
// route_table_id accessor for aws_route_table_association.database. See
// splat.go's resolveElementCoalescelist.

// TestCoalescelistElementFirstArgWins is the corpus shape itself:
// aws_route_table.database provably expands to a nonzero instance count,
// so coalescelist() selects it over aws_route_table.private, and
// element()'s own wraparound then picks the count.index-th instance of
// THAT splat.
func TestCoalescelistElementFirstArgWins(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "coalescelist-element-first-arg-wins"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	for i := 0; i < 3; i++ {
		addr := addrIndex(`aws_route_table_association.database`, i)
		assoc := resolutionAt(t, result, addr)
		if assoc.Class != ClassParentDerived {
			t.Fatalf("%s resolved %s, want PARENT_DERIVED (both parents are server-assigned)", addr, assoc.Class)
		}
		want := "${aws_subnet.database[" + strconv.Itoa(i) + "].id}/${aws_route_table.database[" + strconv.Itoa(i) + "].id}"
		if assoc.Formula.String() != want {
			t.Errorf("%s formula is %q, want %q (should resolve through database, not private)", addr, assoc.Formula.String(), want)
		}
	}
}

// TestCoalescelistElementSecondArgWraparound: aws_route_table.database
// provably expands to zero instances, so coalescelist() skips it and
// selects aws_route_table.private instead - and because private has fewer
// instances (2) than this resource's own count (5), element()'s wraparound
// must apply to PRIVATE's own length, not database's (which contributes
// nothing to the picture at all).
func TestCoalescelistElementSecondArgWraparound(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "coalescelist-element-second-arg-wraparound"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	for i := 0; i < 5; i++ {
		addr := addrIndex(`aws_route_table_association.database`, i)
		assoc := resolutionAt(t, result, addr)
		if assoc.Class != ClassParentDerived {
			t.Fatalf("%s resolved %s, want PARENT_DERIVED", addr, assoc.Class)
		}
		wantPrivateIdx := i % 2
		want := "${aws_subnet.database[" + strconv.Itoa(i) + "].id}/${aws_route_table.private[" + strconv.Itoa(wantPrivateIdx) + "].id}"
		if assoc.Formula.String() != want {
			t.Errorf("%s formula is %q, want %q (element()'s own wraparound against private's length)", addr, assoc.Formula.String(), want)
		}
	}
}

// TestCoalescelistElementLandsOnLiteral: both splat arguments provably
// expand to zero instances, so the provable index [0] lands on the
// trailing literal-list element instead of any resource's attribute. That
// is not identity-bearing via a marker at all, so the result must resolve
// CONCRETE from the literal, not refuse and not fabricate a resource
// reference.
func TestCoalescelistElementLandsOnLiteral(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "coalescelist-element-literal-fallback"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	assoc := resolutionAt(t, result, `aws_route_table_association.database`)
	if assoc.Class != ClassConcrete {
		t.Fatalf("aws_route_table_association.database resolved %s, want CONCRETE (the index lands on a literal, not a resource)", assoc.Class)
	}
	want := "subnet-fake/rtb-fallback"
	if assoc.ImportID != want {
		t.Errorf("import ID is %q, want %q", assoc.ImportID, want)
	}
}

// TestCoalescelistElementAllEmptyRefuses: every coalescelist() argument
// provably expands to no elements at all, and there is no trailing
// literal-list fallback. coalescelist() itself errors at apply time in
// exactly this case, and this package should refuse ahead of that with its
// own specific reason.
func TestCoalescelistElementAllEmptyRefuses(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "coalescelist-element-all-empty"), nil)

	result, diags := Resolve(context.Background(), cfg)

	if _, ok := result.Get(mustAddr(t, `aws_route_table_association.empty_branches`)); ok {
		t.Fatalf("all-empty coalescelist() resolved; both aws_route_table.database and aws_route_table.private expand to no instances")
	}
	if !hasDiag(diags, "Identity not resolvable from configuration", "provably expands to no elements at all") {
		t.Errorf("no refusal explaining the all-empty coalescelist():\n%s", renderDiags(diags))
	}
}

// TestCoalescelistElementUnrecognizedArgRefuses: the first argument
// provably expands to zero instances, so coalescelist() would move on to
// its second argument - but that argument is a plain variable reference,
// neither a splat over a managed resource nor a literal list. Whether IT is
// empty is not knowable from configuration alone, so this must refuse with
// its own specific reason rather than guess.
func TestCoalescelistElementUnrecognizedArgRefuses(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "coalescelist-element-unrecognized-arg"), nil)

	result, diags := Resolve(context.Background(), cfg)

	if _, ok := result.Get(mustAddr(t, `aws_route_table_association.unrecognized`)); ok {
		t.Fatalf("unrecognized-argument coalescelist() resolved; var.extra_route_table_ids's length is not knowable from configuration")
	}
	if !hasDiag(diags, "Identity not resolvable from configuration", "neither a splat over a managed resource nor a literal list") {
		t.Errorf("no refusal explaining the unrecognized argument:\n%s", renderDiags(diags))
	}
}
