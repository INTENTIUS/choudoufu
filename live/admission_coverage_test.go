// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// This file is issue #246's second "fix regardless" and issue #245's
// measurement: no provider type may fall out of both the identity table and
// the rejection ledger without being counted.
//
// The two committed rosters that decide admission are keyed by type name:
//
//   - internal/live/identity.DefaultTable, the rows a ratification batch
//     admitted;
//   - tools/row-gen/rejected.json, the types a ratification batch looked at
//     and declined.
//
// TestRejectedLedgerIsDisjointFromAdmitted (tools/row-gen) already forbids a
// type appearing in both. Nothing forbade a type appearing in neither, and
// the population that does is not small: naming one of them in a
// configuration is a hard resolve error at internal/live/identity/table.go's
// Resolve, with no ledger entry anywhere saying why. Seven WAF Classic types
// sat there for the whole life of the fork (#246) and were found only by a
// hand sweep.
//
// rejected.json's size is not the parity debt and never was - its own note
// calls it a veto set - so a batch reporting "rejected.json 159 -> 104 -> 86"
// says how many types a batch declined, not how many remain outside
// admission. The number below is the one that measures that, and it does not
// move when a batch shuffles a type between the table and the ledger.

// unreachedRatchetMax is the highest number of provider resource types that
// may be in neither internal/live/identity.DefaultTable nor
// tools/row-gen/rejected.json.
//
// Measured at 669 on 2026-08-16 against hashicorp/aws 6.59.0 (949 admitted,
// 81 vetoed, 669 unreached, summing to live/survey-full.json's 1699 exactly).
// It was also 669 at the earlier measurement in #245, where the split was
// 944/86/669 - the batch that moved five types from the ledger into the table
// did not change this count at all, which is the whole reason this constant
// exists rather than a count of the ledger.
//
// Lower it whenever a batch lands; raising it is admitting a type stopped
// being reachable, and needs to be a deliberate, reviewed edit rather than a
// silent one. A drop is welcome and simply means the constant is stale.
//
// Of the 669 as measured, 60 are nevertheless admitted at run time by
// internal/live/lint's schema fallback (identity.SynthesizeTypeIdentity) when
// the caller supplies provider schemas, leaving 609 hard resolve errors. That
// 60 is a floor, not a ceiling, because a real run also supplies a config
// signal. This test deliberately does not subtract it: the fallback needs a
// live provider plugin, and a ratchet that cannot run without one is a
// ratchet that does not run.
const unreachedRatchetMax = 669

// universeFloor is the anti-tamper leg. The count this file ratchets is a
// difference, so shrinking live/survey-full.json's type roster lowers it just
// as effectively as admitting a type does, and shrinking it is the cheaper
// edit. hashicorp/aws has never lost a hundred resource types in a release.
const universeFloor = 1600

// rejectedLedgerRel is tools/row-gen/rejected.json, read here as JSON rather
// than through row-gen's own loadRejectedTypes: this test is a guard on that
// file's contents and should not depend on the package it guards being able
// to parse it.
const rejectedLedgerRel = "../tools/row-gen/rejected.json"

// providerTypeUniverse returns every resource type the pinned provider
// serves, from live/survey-full.json - which tools/survey-gen writes from the
// provider's own GetProviderSchema response, and which
// TestMeasurementArtifactsShareTheProviderPin already ties to
// pins.AWSProviderVersion. It is the external source in the sense this
// repository's ratchet rule asks for: it is not derived from either roster
// under test, so no edit to the identity table or the rejection ledger can
// make this test agree with itself.
func providerTypeUniverse(t *testing.T) map[string]bool {
	t.Helper()
	var survey struct {
		Counts struct {
			Types int `json:"types"`
		} `json:"counts"`
		Types []struct {
			Type string `json:"type"`
		} `json:"types"`
	}
	decodeInto(t, "survey-full.json", &survey)

	universe := make(map[string]bool, len(survey.Types))
	for _, e := range survey.Types {
		universe[e.Type] = true
	}
	if len(universe) != survey.Counts.Types {
		t.Fatalf("live/survey-full.json lists %d distinct types but its own counts.types says %d; one of the two is stale",
			len(universe), survey.Counts.Types)
	}
	if len(universe) < universeFloor {
		t.Fatalf("live/survey-full.json's type roster is %d types, below the floor of %d (universeFloor); "+
			"the unreached ratchet is a difference against this roster, so a roster that shrank by accident "+
			"would read as admission progress",
			len(universe), universeFloor)
	}
	return universe
}

// rejectedLedger returns the veto set's key set.
func rejectedLedger(t *testing.T) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(rejectedLedgerRel))
	if err != nil {
		t.Fatalf("reading %s: %v", rejectedLedgerRel, err)
	}
	var ledger struct {
		Rejected map[string]json.RawMessage `json:"rejected"`
	}
	if err := json.Unmarshal(data, &ledger); err != nil {
		t.Fatalf("decoding %s: %v", rejectedLedgerRel, err)
	}
	if len(ledger.Rejected) == 0 {
		t.Fatalf("%s decoded to an empty veto set; the shape this test reads has changed", rejectedLedgerRel)
	}
	out := make(map[string]bool, len(ledger.Rejected))
	for typeName := range ledger.Rejected {
		out[typeName] = true
	}
	return out
}

// TestUnreachedTypeRatchet counts the provider types that are in neither
// roster, and fails when that count grows.
func TestUnreachedTypeRatchet(t *testing.T) {
	universe := providerTypeUniverse(t)
	rejected := rejectedLedger(t)

	admitted := make(map[string]bool, len(identity.AdmittedTypes()))
	for _, typeName := range identity.AdmittedTypes() {
		admitted[typeName] = true
	}

	var unreached []string
	for typeName := range universe {
		if !admitted[typeName] && !rejected[typeName] {
			unreached = append(unreached, typeName)
		}
	}
	sort.Strings(unreached)

	if len(unreached) > unreachedRatchetMax {
		sample := unreached
		if len(sample) > 20 {
			sample = sample[:20]
		}
		t.Errorf("%d provider type(s) are in neither internal/live/identity.DefaultTable nor %s, above the "+
			"ratchet ceiling of %d (unreachedRatchetMax). Naming one of these in a configuration is a hard "+
			"resolve error with no ledger entry saying why. Admit it, or record the ruling in the ledger - "+
			"raising this constant is neither. First %d: %v",
			len(unreached), rejectedLedgerRel, unreachedRatchetMax, len(sample), sample)
	}
	if len(unreached) < unreachedRatchetMax {
		t.Logf("%d unreached types, below the ceiling of %d; lower unreachedRatchetMax to match",
			len(unreached), unreachedRatchetMax)
	}
}

// TestNoVetoNamesATypeTheProviderDoesNotServe is the other half of the same
// accounting. A veto entry for a type outside the provider's roster is a
// ruling nothing can ever retire: it would sit in the ledger forever, and it
// would make rejected.json's size look like more debt than exists. This is a
// hard assertion, not a ratchet, because the live count is zero.
func TestNoVetoNamesATypeTheProviderDoesNotServe(t *testing.T) {
	universe := providerTypeUniverse(t)

	var strays []string
	for typeName := range rejectedLedger(t) {
		if !universe[typeName] {
			strays = append(strays, typeName)
		}
	}
	sort.Strings(strays)
	if len(strays) > 0 {
		t.Errorf("%d veto entr(y/ies) in %s name a type live/survey-full.json's provider roster does not "+
			"contain: %v - either the type was renamed or removed upstream and the entry is stale, or the "+
			"entry never named a real type",
			len(strays), rejectedLedgerRel, strays)
	}
}

// TestAdmittedTableNamesOnlyTypesTheProviderServes is the same question asked
// of the other roster. An admitted row for a type the provider does not serve
// resolves against nothing, and it inflates the admitted count that parity is
// measured by.
//
// live/survey-full.json describes one provider, so it can only answer for
// aws_ rows. The table's other ten rows are the effects set the fork keeps in
// the state file (null_resource, terraform_data, the random_* and time_*
// families) and no AWS roster describes them - so rather than exempt them by
// name, this asks the row itself: a non-aws_ row is acceptable exactly when
// it is RecordBacked, which is the property that makes it an effect rather
// than a live object. A hand-written exemption list would go stale; the
// row's own field cannot.
func TestAdmittedTableNamesOnlyTypesTheProviderServes(t *testing.T) {
	universe := providerTypeUniverse(t)

	var strays, unrostered []string
	for typeName, row := range identity.DefaultTable {
		if len(typeName) < 4 || typeName[:4] != "aws_" {
			if !row.RecordBacked {
				unrostered = append(unrostered, typeName)
			}
			continue
		}
		if !universe[typeName] {
			strays = append(strays, typeName)
		}
	}
	if len(unrostered) > 0 {
		sort.Strings(unrostered)
		t.Errorf("internal/live/identity.DefaultTable has %d non-aws_ row(s) that are not RecordBacked: %v - "+
			"live/survey-full.json describes one provider, so this test can say nothing about a live type from "+
			"another one; it needs a roster for that provider before it can claim to check the row",
			len(unrostered), unrostered)
	}
	if len(strays) > 0 {
		sort.Strings(strays)
		t.Errorf("%d admitted row(s) name a type live/survey-full.json's provider roster does not contain: %v",
			len(strays), strays)
	}
}
