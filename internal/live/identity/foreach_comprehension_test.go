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

// A for_each over a for-comprehension of another resource's instance keys
// (issue #178's parity gap with stock OpenTofu, the sibling of the direct
// forEachOverResource shape countlength_test.go documents for count): the
// key clause and the "if" filter read only the loop key and locally
// evaluable data, never the loop value, so the result is knowable from the
// parent's own already-computed keys. TestForEachComprehensionValueGuard
// below pins the boundary these tests must not cross.
func TestForEachOverComprehension(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "foreach-comprehension"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	for _, key := range []string{"a", "c"} {
		res := resolutionAt(t, result, `aws_route_table_association.selected["`+key+`"]`)
		if res.Class != ClassConcrete {
			t.Fatalf("instance %q resolved %s; subnet_id and route_table_id are both configuration data", key, res.Class)
		}
		want := "subnet-" + key + "/rtb-fixed"
		if res.ImportID != want {
			t.Errorf("instance %q resolved to %q, want %q", key, res.ImportID, want)
		}
	}

	// "b" fails the if clause (contains(local.selected, k)), so it must not
	// exist - a wrong filter evaluation would invent or drop instances the
	// same way a wrong count would, and #178's history is full of exactly
	// that kind of silent divergence.
	if _, ok := result.Get(mustAddr(t, `aws_route_table_association.selected["b"]`)); ok {
		t.Fatalf(`aws_route_table_association.selected["b"] resolved; local.selected excludes "b"`)
	}
}

// TestForEachComprehensionValueGuard is the boundary
// TestForEachOverComprehension's success must not cross: an "if" clause
// that reads the comprehension's iterated value (v, standing for a live
// subnet) rather than only its key. Refusing this is parity with stock
// OpenTofu ("cannot be determined until apply"), not a gap - see #178's
// scoping comment on the data-source-attribute half of this rule, which
// this shape belongs to structurally even though its source is a managed
// resource rather than a data source.
func TestForEachComprehensionValueGuard(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "foreach-comprehension-value-guard"), nil)

	_, diags := Resolve(context.Background(), cfg)
	if !diags.HasErrors() {
		t.Fatalf("expected an error; the if clause reads the comprehension's iterated value")
	}
	found := false
	for _, d := range diags {
		if d.Description().Summary == "Non-static for_each expression" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a %q diagnostic, got: %v", "Non-static for_each expression", diags)
	}

	result, _ := Resolve(context.Background(), cfg)
	if _, ok := result.Get(mustAddr(t, `aws_route_table_association.selected["a"]`)); ok {
		t.Fatalf(`aws_route_table_association.selected["a"] resolved; a failed resolution is an omission plus an error, never a guess`)
	}
}
