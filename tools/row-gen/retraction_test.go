// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestRetractedTypesReportsOnlyDisappearances pins the helper's own rule:
// only a key that leaves is a retraction. An added key is not, a reordered
// key set is not, and neither is a row whose FIELDS change - the fields are
// recomputed on every run by mergeServerAssigned, mergeCloudDefault and
// mergeIdentityAttrs, and a type that stays admitted keeps its ratified
// content in the next run's input either way.
func TestRetractedTypesReportsOnlyDisappearances(t *testing.T) {
	for _, tc := range []struct {
		name     string
		previous []string
		emitted  []string
		want     []string
	}{
		{"nothing changes", []string{"a", "b"}, []string{"a", "b"}, nil},
		{"order does not matter", []string{"a", "b"}, []string{"b", "a"}, nil},
		{"an addition is not a retraction", []string{"a"}, []string{"a", "b"}, nil},
		{"one drop", []string{"a", "b", "c"}, []string{"a", "c"}, []string{"b"}},
		{"drops are sorted", []string{"c", "b", "a"}, nil, []string{"a", "b", "c"}},
		{"a swap is a drop and an add", []string{"a", "b"}, []string{"a", "c"}, []string{"b"}},
		{"empty previous", nil, []string{"a"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := retractedTypes(tc.previous, tc.emitted)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("retractedTypes(%v, %v) = %v, want %v", tc.previous, tc.emitted, got, tc.want)
			}
		})
	}
}

// TestEmitRetractsNothingOnTheCommittedTree is the green half: emitting the
// tree as committed drops no admitted type, so `just emit` needs no opt-in.
//
// On its own this assertion is worth little - it would also pass if the gate
// could never fire - which is what
// TestEmitGateFiresOnAnInputDrivenRetraction is for.
func TestEmitRetractsNothingOnTheCommittedTree(t *testing.T) {
	part := emitPartitionForTest(t, loadSurveyForTest(t))
	emitted := append(append([]string(nil), part.Generated...), part.Override...)
	if retracted := retractedTypes(identity.AdmittedTypes(), emitted); len(retracted) > 0 {
		t.Errorf("a fresh -emit would retract %d admitted type(s) from the committed tree: %v", len(retracted), retracted)
	}
}

// TestEmitGateFiresOnAnInputDrivenRetraction is the non-vacuity control, and
// it drives the REAL generator rather than the helper: it flips one survey
// entry's taggable signal to false and requires buildEmitFiles to drop that
// type and retractedTypes to name it.
//
// The mutation is chosen by rule, never by name - the first admitted,
// currently-taggable type whose ratified row says ServerAssigned, in sorted
// order - because markerless() vetoes exactly untaggable-and-server-assigned
// and the survey's taggable signal is the leg an input can move. Nothing
// here spells out an aws_* type, so live/derivation_guard_test.go has
// nothing to register.
//
// This is also the shape of the accident issue #263 was opened for: a wrong
// signal reaching markerless() retracts rows, and before this gate existed
// -emit wrote the smaller table and exited 0.
func TestEmitGateFiresOnAnInputDrivenRetraction(t *testing.T) {
	survey := loadSurveyForTest(t)

	var candidates []string
	for typeName, entry := range survey {
		row, admitted := identity.DefaultTable[typeName]
		if admitted && entry.Signals.Taggable && row.ServerAssigned {
			candidates = append(candidates, typeName)
		}
	}
	sort.Strings(candidates)
	if len(candidates) == 0 {
		t.Fatal("no admitted, taggable, server-assigned type exists, so this control cannot construct a retraction - " +
			"if that is a real change in the table, this test needs a different lever, not deletion")
	}
	victim := candidates[0]

	mutated := make(map[string]surveyEntry, len(survey))
	for k, v := range survey {
		mutated[k] = v
	}
	entry := mutated[victim]
	entry.Signals.Taggable = false
	mutated[victim] = entry

	part := emitPartitionForTest(t, mutated)
	emitted := append(append([]string(nil), part.Generated...), part.Override...)
	retracted := retractedTypes(identity.AdmittedTypes(), emitted)

	if len(retracted) != 1 || retracted[0] != victim {
		t.Fatalf("flipping one taggable signal retracted %v, want exactly [%s] - if the veto now reads a different "+
			"signal this control is measuring nothing", retracted, victim)
	}

	err := retractionRefusal(retracted, allowRetractionFlag)
	// ratifiedJSONRel is in this list because of what the message used to
	// say. Before -emit read the ratified corpus from a file no generator
	// writes, the refusal told the operator that re-running CANNOT undo the
	// drop and sent them to `git checkout --` on the generated tables. That
	// is now false, and a refusal that misdescribes the recovery is worse
	// than one that says nothing: it points away from the file that actually
	// holds the rows. Requiring the corpus path here is what keeps the two
	// from drifting apart again.
	for _, want := range []string{victim, allowRetractionFlag, identityTableRel, ratifiedJSONRel} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "CANNOT undo") {
		t.Errorf("the refusal still claims re-running cannot undo the retraction, which stopped being true "+
			"when the corpus moved to %s - re-emitting after fixing the cause restores these rows:\n%v", ratifiedJSONRel, err)
	}
}

// TestEmitAddsNoRowTheTableDoesNotAlreadyHold pins the self-reference issue
// #263 named, in the direction the gate does not cover: apart from the
// RecordBacked rows derived from live/logical-schemas.json, -emit emits no
// type [identity.DefaultTable] did not already carry. The fresh classifier
// never contributes a row.
//
// That is why the committed table cannot be reconstructed from row-gen's
// inputs, and why "run -emit twice and diff" proves only that the tree sits
// at SOME fixed point. Measured at 5502e8a3de: emptying DefaultTable's
// literal and running -emit twice yields a 14-row table, byte-identical
// across both runs, exit 0.
//
// If this test ever fails because -emit grew a real derivation, that is the
// fix for #263 landing, and retraction.go's whole premise should be re-read
// rather than the assertion relaxed.
func TestEmitAddsNoRowTheTableDoesNotAlreadyHold(t *testing.T) {
	part := emitPartitionForTest(t, loadSurveyForTest(t))
	emitted := append(append([]string(nil), part.Generated...), part.Override...)

	recordBacked := setOf(recordBackedTypes(loadLogicalSchemasForTest(t)))
	var added []string
	for _, t := range retractedTypes(emitted, identity.AdmittedTypes()) {
		if !recordBacked[t] {
			added = append(added, t)
		}
	}
	if len(added) > 0 {
		t.Errorf("-emit added %d non-RecordBacked type(s) the table did not already hold: %v", len(added), added)
	}
}

// emitPartitionForTest runs buildEmitFiles over the committed inputs with one
// survey substituted, and returns the identity partition - whose two halves
// are exactly emittedRows' key set.
func emitPartitionForTest(t *testing.T, survey map[string]surveyEntry) emitPartition {
	t.Helper()
	_, identityPart, _, err := buildEmitFiles(loadRatifiedForTest(t), loadAllForTest(t), loadAnnotationsForTest(t), loadImportGrammarForTest(t), survey, loadLogicalSchemasForTest(t))
	if err != nil {
		t.Fatalf("buildEmitFiles: %v", err)
	}
	return identityPart
}
