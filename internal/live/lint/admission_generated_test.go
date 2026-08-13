// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import "testing"

// TestGeneratedTablesMatchAdmittedTypesV0 is issue #96's drift test for
// tools/row-gen -emit's two admission outputs (admission_generated.go and
// admission_override.go) - the same role
// internal/live/identity/emit_verify_test.go's twin plays for DefaultTable.
// Neither file is wired into admittedTypesV0's own construction yet (see
// tools/row-gen/emit.go's doc comment on why), so this is what catches the
// two drifting apart from a fresh `go run ./tools/row-gen -emit`.
func TestGeneratedTablesMatchAdmittedTypesV0(t *testing.T) {
	if overlap := intersectStructKeys(admittedTypesGenerated, admittedTypesOverride); len(overlap) > 0 {
		t.Fatalf("admittedTypesGenerated and admittedTypesOverride both claim: %v", overlap)
	}

	merged := make(map[string]struct{}, len(admittedTypesGenerated)+len(admittedTypesOverride))
	for k := range admittedTypesGenerated {
		merged[k] = struct{}{}
	}
	for k := range admittedTypesOverride {
		merged[k] = struct{}{}
	}

	if len(merged) != len(admittedTypesV0) {
		t.Errorf("admittedTypesGenerated (%d) + admittedTypesOverride (%d) = %d types, admittedTypesV0 has %d - regenerate with `go run ./tools/row-gen -emit`",
			len(admittedTypesGenerated), len(admittedTypesOverride), len(merged), len(admittedTypesV0))
	}

	for tf := range admittedTypesV0 {
		if _, ok := merged[tf]; !ok {
			t.Errorf("%s: in admittedTypesV0 but missing from admittedTypesGenerated/admittedTypesOverride - regenerate with `go run ./tools/row-gen -emit`", tf)
		}
	}
	for tf := range merged {
		if _, ok := admittedTypesV0[tf]; !ok {
			t.Errorf("%s: in admittedTypesGenerated/admittedTypesOverride but not in admittedTypesV0 at all - stale generation", tf)
		}
	}
}

func intersectStructKeys(a, b map[string]struct{}) []string {
	var out []string
	for k := range a {
		if _, ok := b[k]; ok {
			out = append(out, k)
		}
	}
	return out
}
