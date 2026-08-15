// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestRejectedLedgerIsDisjointFromAdmitted is issue #131's guard.
//
// tools/row-gen/rejected.json is PROPOSE's veto set: types a ratification
// batch looked at by name and declined. Its own note calls over-inclusion
// safe by design, and that is true among types nobody admitted. Among
// admitted types it is not conservatism, it is two rulings that contradict
// each other, and the ledger's own note already said so while 58 entries
// broke it.
//
// They broke it because the recovery that rebuilt this file after #96
// deleted the per-cohort fragments harvested every type name that appeared
// near the word "Rejected", rather than the subject of each "- <type>:"
// bullet. The prose of a rejection names other types constantly - the
// dynamodb fragment rejects aws_dynamodb_global_secondary_index and
// explains why by referring to "already-admitted aws_dynamodb_table" - so a
// proximity harvest sweeps in the very types the batch admitted.
//
// selectProposeCandidates happens to check `admitted` before it checks
// `rejected`, so the contradiction was unreachable rather than harmless.
// This test does not depend on that ordering, deliberately: a veto set is
// the kind of thing another consumer will read one day, and it should be
// true rather than true-given-the-current-call-order.
func TestRejectedLedgerIsDisjointFromAdmitted(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	rejected, err := loadRejectedTypes(root)
	if err != nil {
		t.Fatalf("loadRejectedTypes: %v", err)
	}

	var contradictions []string
	for _, admitted := range identity.AdmittedTypes() {
		if rejected[admitted] {
			contradictions = append(contradictions, admitted)
		}
	}
	if len(contradictions) > 0 {
		t.Errorf("%d type(s) are both admitted by internal/live/identity.DefaultTable and vetoed by %s: %v\n"+
			"A type cannot be both. If it was genuinely rejected and later admitted anyway, the ledger entry is "+
			"stale and goes; if the recovery swept it up from a rejection's prose rather than its subject, it was "+
			"never a ruling.",
			len(contradictions), rejectedTypesJSONRel, contradictions)
	}
}

// TestProposeContractNamesTheLedgerItReads is issue #140's guard.
//
// The printed contract used to tell a reviewer to record a rejection "in
// table.go's or admission.go's own comments, so the next run's Rejected scan
// finds it". That scan stopped existing at #96, when both tables became
// generated in full; rejectedTypesJSONRel's own doc comment says so 140
// lines above the contract that contradicted it. A reviewer following the
// printed instructions would have written a ruling into a DO NOT EDIT file,
// watched -emit overwrite it, and had PROPOSE re-propose the type with a
// clean record behind it.
//
// The path is a constant in the same file as the header, so the two cannot
// drift apart if the header simply quotes it. This asserts that it does.
func TestProposeContractNamesTheLedgerItReads(t *testing.T) {
	if !strings.Contains(proposeContractHeader, rejectedTypesJSONRel) {
		t.Errorf("the printed contract does not name %s, the ledger loadRejectedTypes actually reads;\n"+
			"a reviewer following it would record a ruling somewhere nothing consults", rejectedTypesJSONRel)
	}
	// The two generated tables must not be named as places to record a
	// ruling. They are overwritten in full by -emit, so an instruction to
	// edit them is an instruction to lose the ruling.
	for _, generated := range []string{"table.go's", "admission.go's"} {
		if strings.Contains(proposeContractHeader, generated) {
			t.Errorf("the printed contract still tells a reviewer to edit %s, which -emit overwrites", generated)
		}
	}
}
