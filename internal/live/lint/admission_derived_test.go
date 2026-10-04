// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestAdmittedTypesAreTheIdentityTableMinusRecordBacked pins #809's
// derivation by value: every DefaultTable row is in admittedTypesV0 exactly
// when it is not RecordBacked, and nothing else is. The derivation makes
// this true by construction today; the test is what notices a second source
// being added beside it (a generated file, an init() that appends, a literal
// someone pastes a type into).
func TestAdmittedTypesAreTheIdentityTableMinusRecordBacked(t *testing.T) {
	recordBacked := 0
	for typeName, row := range identity.DefaultTable {
		_, inLint := admittedTypesV0[typeName]
		if row.RecordBacked {
			recordBacked++
			if inLint {
				t.Errorf("%s is RecordBacked in identity.DefaultTable but admittedTypesV0 holds it; lint refuses "+
					"RECORD_ADMITTED types by class before resolution", typeName)
			}
			continue
		}
		if !inLint {
			t.Errorf("%s has a DefaultTable row but admittedTypesV0 does not hold it", typeName)
		}
	}
	if got, want := len(admittedTypesV0), len(identity.DefaultTable)-recordBacked; got != want {
		t.Errorf("admittedTypesV0 has %d types, want DefaultTable's %d minus %d record-backed = %d; "+
			"something other than the identity table is adding to it", got, len(identity.DefaultTable), recordBacked, want)
	}
	if recordBacked == 0 {
		t.Error("no DefaultTable row is RecordBacked, so the exclusion above was never exercised")
	}
}

// TestLintHoldsNoGeneratedAdmissionCopy keeps the second generated file
// retired. admission_generated.go was tools/row-gen -emit's copy of the same
// key set; a regenerated one would be read by nothing and agree with the
// table only until the next hand edit.
func TestLintHoldsNoGeneratedAdmissionCopy(t *testing.T) {
	if _, err := os.Stat(filepath.Join(".", "admission_generated.go")); err == nil {
		t.Error("internal/live/lint/admission_generated.go exists again; admittedTypesV0 is derived from " +
			"identity.DefaultTable in admission.go (#809) and must not have a second source")
	}
}
