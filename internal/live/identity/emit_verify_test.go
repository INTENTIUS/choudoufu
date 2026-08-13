// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"reflect"
	"testing"
)

// TestGeneratedTablesMatchDefaultTable is issue #96's drift test for
// tools/row-gen -emit's two identity outputs (table_generated.go and
// table_override.go). Neither file is wired into DefaultTable's own
// construction yet (see emit.go's doc comment on why: the 86 per-cohort
// fragment files that build DefaultTable today would double-register every
// type), so nothing catches the two drifting apart from a fresh `go run
// ./tools/row-gen -emit` except this test - the same role
// TestConvergenceArtifactMatchesCommitted plays for
// live/rowgen-convergence.json. A ratification batch that hand-edits
// DefaultTable (via a new or changed table_cohort_*.go) without also
// regenerating these two files fails here, not silently.
func TestGeneratedTablesMatchDefaultTable(t *testing.T) {
	if overlap := intersectTypeIdentityKeys(identityTableGenerated, identityTableOverride); len(overlap) > 0 {
		t.Fatalf("identityTableGenerated and identityTableOverride both claim: %v", overlap)
	}

	merged := make(map[string]TypeIdentity, len(identityTableGenerated)+len(identityTableOverride))
	for k, v := range identityTableGenerated {
		merged[k] = v
	}
	for k, v := range identityTableOverride {
		merged[k] = v
	}

	if len(merged) != len(DefaultTable) {
		t.Errorf("identityTableGenerated (%d) + identityTableOverride (%d) = %d types, DefaultTable has %d - regenerate with `go run ./tools/row-gen -emit`",
			len(identityTableGenerated), len(identityTableOverride), len(merged), len(DefaultTable))
	}

	for tf, want := range DefaultTable {
		got, ok := merged[tf]
		if !ok {
			t.Errorf("%s: in DefaultTable but missing from identityTableGenerated/identityTableOverride - regenerate with `go run ./tools/row-gen -emit`", tf)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: identityTableGenerated/identityTableOverride disagrees with DefaultTable's ratified entry\n got:  %#v\nwant: %#v", tf, got, want)
		}
	}
	for tf := range merged {
		if _, ok := DefaultTable[tf]; !ok {
			t.Errorf("%s: in identityTableGenerated/identityTableOverride but not in DefaultTable at all - stale generation", tf)
		}
	}
}

func intersectTypeIdentityKeys(a, b map[string]TypeIdentity) []string {
	var out []string
	for k := range a {
		if _, ok := b[k]; ok {
			out = append(out, k)
		}
	}
	return out
}
