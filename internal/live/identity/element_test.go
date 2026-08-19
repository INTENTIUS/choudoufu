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

// GitHub issue #321: element(<resource>[*].id, count.index), the shape
// #313's data-source fix revealed underneath terraform-aws-modules/vpc's
// own aws_route_table_association.private (12 sites in
// corpus-security-group-complete). See splat.go's resolveElementCall.

// TestElementSplatResolvesThroughCountIndex is the corpus shape itself:
// both operands (aws_subnet.private, aws_route_table.private) are
// server-assigned resources, and each aws_route_table_association.private
// instance's subnet_id and route_table_id both pick the same-indexed
// instance, exactly matching what a direct R[count.index].attr traversal
// would resolve to.
func TestElementSplatResolvesThroughCountIndex(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "element-splat-count-index"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	for i := 0; i < 3; i++ {
		addr := addrIndex(`aws_route_table_association.private`, i)
		assoc := resolutionAt(t, result, addr)
		if assoc.Class != ClassParentDerived {
			t.Fatalf("%s resolved %s, want PARENT_DERIVED (both parents are server-assigned)", addr, assoc.Class)
		}
		want := formatFormula(i, i)
		if assoc.Formula.String() != want {
			t.Errorf("%s formula is %q, want %q", addr, assoc.Formula.String(), want)
		}
	}
}

// TestElementSplatWraparoundMatchesElementFunc: aws_subnet.small has 2
// instances and aws_route_table_association.wrap has 5, so element()'s own
// modulo wraparound must be reproduced exactly - instances 2, 3 and 4
// resolve against aws_subnet.small[0], [1] and [0], the same instances
// element(aws_subnet.small[*].id, count.index) would pick at apply time.
func TestElementSplatWraparoundMatchesElementFunc(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "element-splat-wraparound"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	wantSubnet := map[int]int{0: 0, 1: 1, 2: 0, 3: 1, 4: 0}
	for i, subnetIdx := range wantSubnet {
		addr := addrIndex(`aws_route_table_association.wrap`, i)
		wrap := resolutionAt(t, result, addr)
		if wrap.Class != ClassParentDerived {
			t.Fatalf("%s resolved %s, want PARENT_DERIVED", addr, wrap.Class)
		}
		want := "${aws_subnet.small[" + strconv.Itoa(subnetIdx) + "].id}/rtb-" + strconv.Itoa(i)
		if wrap.Formula.String() != want {
			t.Errorf("%s formula is %q, want %q (element()'s own wraparound)", addr, wrap.Formula.String(), want)
		}
	}
}

// TestElementSplatEmptySourceRefuses: the source resource expands to no
// instances, so element() would error on an empty list at apply time. This
// package refuses ahead of that, the same way the arity-collapse rule
// refuses an empty splat (splat-arity-zero).
func TestElementSplatEmptySourceRefuses(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "element-splat-empty-source"), nil)

	result, diags := Resolve(context.Background(), cfg)

	if _, ok := result.Get(mustAddr(t, `aws_route_table_association.empty_source`)); ok {
		t.Fatalf("empty_source resolved; aws_subnet.empty expands to no instances")
	}
	if !hasDiag(diags, "Identity not resolvable from configuration", "no instances at all") {
		t.Errorf("no refusal explaining the empty splat source:\n%s", renderDiags(diags))
	}
}

func formatFormula(subnetIdx, rtIdx int) string {
	return "${aws_subnet.private[" + strconv.Itoa(subnetIdx) + "].id}/${aws_route_table.private[" + strconv.Itoa(rtIdx) + "].id}"
}
