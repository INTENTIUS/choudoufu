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

// aws_subnet.this[each.key].id: a reference into another for_each'd
// resource through a computed index, rather than a bare reference or a
// literal-key traversal. [resolver.resolveIndexedTraversal]'s whole point:
// the index is evaluated against the same per-instance scope every other
// identity argument already has, and the addressed instance's identity is
// resolved through the same [resolver.parentPart] a direct reference uses.
func TestIndexedTraversalResolvesThroughComputedIndex(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "indexed-traversal"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	for _, key := range []string{"a", "b"} {
		subnet := resolutionAt(t, result, `aws_subnet.this["`+key+`"]`)
		if subnet.Class != ClassNeedsDiscovery {
			t.Fatalf("subnet %q resolved %s; aws_subnet is server-assigned and should be NEEDS_DISCOVERY", key, subnet.Class)
		}

		assoc := resolutionAt(t, result, `aws_route_table_association.assoc["`+key+`"]`)
		if assoc.Class != ClassParentDerived {
			t.Fatalf("association %q resolved %s, want PARENT_DERIVED - it depends on the subnet's live ID", key, assoc.Class)
		}
		want := `${aws_subnet.this["` + key + `"].id}/rtb-fixed`
		if assoc.Formula.String() != want {
			t.Errorf("association %q formula is %q, want %q", key, assoc.Formula.String(), want)
		}
	}
}
