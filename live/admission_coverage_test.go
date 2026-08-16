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
// The three committed rosters that decide admission are keyed by type name:
//
//   - internal/live/identity.DefaultTable, the rows a ratification batch
//     admitted;
//   - tools/row-gen/rejected.json, the types a ratification batch looked at
//     and declined;
//   - internal/live/identity.MarkerlessTypes, the types tools/row-gen's one
//     derived admission rule vetoes (issue #249). A batch never looked at
//     these individually and never will: the ruling is a rule, regenerated
//     on every run, and it carries its own reason in
//     [identity.MarkerlessReason].
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
// may be in none of internal/live/identity.DefaultTable,
// tools/row-gen/rejected.json and internal/live/identity.MarkerlessTypes.
//
// Measured at 621 on 2026-08-16 against hashicorp/aws 6.59.0: 953 aws_ rows
// admitted, 81 vetoed by hand, 44 vetoed by the markerless rule and counted
// by neither of the other two rosters, 621 unreached, summing to
// live/survey-full.json's 1699 exactly.
//
// It stood at 665 before the markerless rule landed (#249), and at 649 while
// that rule read only the CloudFormation registry's own verdict. The rule
// now vetoes 150 types in total; 77 of those are rows the table still admits
// (see markerlessAdmittedOverlapMax) and 29 the hand ledger had already
// reached, so only the remaining 44 - types no batch had looked at, now
// pre-empted - move this count.
//
// The 649 -> 621 step is the second evidence source. For a type
// CloudFormation does not model the registry states nothing, so the
// classifier's bucket cannot decide server-assignment, and 28 untaggable
// types sat unreached that the provider's own import documentation settles
// outright. tools/importdocs-gen/soleid.go scrapes that evidence - the sole
// segment of a one-segment documented import ID, attributed the way
// tools/importdocs-gen's idParts attributes each segment of a composite -
// and tools/row-gen/importgrammar.go's docMintedSegment reads it.
//
// It stood at 669 for the two measurements before that (949/81 and, in #245,
// 944/86) - the batch that moved five types from the ledger into the table
// did not change this count at all, which is the whole reason this constant
// exists rather than a count of the ledger.
//
// Lower it whenever a batch lands; raising it is admitting a type stopped
// being reachable, and needs to be a deliberate, reviewed edit rather than a
// silent one. A drop is welcome and simply means the constant is stale.
//
// When this count stood at 669, 60 of that population were nevertheless
// admitted at run time by internal/live/lint's schema fallback
// (identity.SynthesizeTypeIdentity) with provider schemas supplied, leaving
// 609 hard resolve errors; that 60 was a floor, not a ceiling, because a real
// run also supplies a config signal. The four DocDB types this count dropped
// by were not among the 60 - they refused under real schemas in
// live/corpus-refusals.json's own ladder, which is what put them on the
// demanded list - so the rescued figure is unchanged and the hard population
// is 605. This test deliberately does not subtract it: the fallback needs a
// live provider plugin, and a ratchet that cannot run without one is a
// ratchet that does not run.
const unreachedRatchetMax = 621

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
			if _, vetoed := identity.MarkerlessTypes[typeName]; vetoed {
				continue
			}
			unreached = append(unreached, typeName)
		}
	}
	sort.Strings(unreached)

	if len(unreached) > unreachedRatchetMax {
		sample := unreached
		if len(sample) > 20 {
			sample = sample[:20]
		}
		t.Errorf("%d provider type(s) are in none of internal/live/identity.DefaultTable, %s and "+
			"internal/live/identity.MarkerlessTypes, above the "+
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

// markerlessAdmittedOverlapMax is the number of rows internal/live/identity's
// generated table still admits that the markerless rule vetoes, and it is a
// ratchet down to zero.
//
// It is not zero today and the reason is recorded rather than assumed. The
// rule (tools/row-gen/markerless.go) is derived and is applied in full to
// what may be admitted NEXT - tools/row-gen's PROPOSE stage cannot offer a
// vetoed type, and the count below is the whole of what a batch has already
// let through. Retracting those rows is a separate change, because it
// converts their refusal from internal/live/stamp's hard unmarked-apply
// error into internal/live/lint's unadmitted-type finding, and
// internal/live/check.ClassifyOnboarding (ladder.go's own switch) counts
// unadmitted-type as NON-blocking - so the retraction on its own would move
// corpus estates up the onboarding ladder while no configuration became any
// more applyable. It also empties 21 cohort estates under live/e2e of
// resources whose ratification evidence lives in hand-owned READMEs. Both
// need answering before the rows come out; see issue #249.
//
// Measured at 77 on 2026-08-16 against hashicorp/aws 6.59.0. Lowering it is
// the point. Raising it means a batch admitted a type the rule vetoes,
// which PROPOSE cannot do and a hand-pasted row can, and it needs to be a
// deliberate edit rather than a silent one.
const markerlessAdmittedOverlapMax = 77

// TestMarkerlessVetoOverlapWithAdmittedDoesNotGrow is the anti-tamper leg
// for the third roster, and the debt marker for the rows the rule has not
// been applied to yet.
//
// TestUnreachedTypeRatchet's count is a difference and this roster
// subtracts from it, so a generator that vetoed types it also admits could
// lower that count while changing nothing about what the fork supports.
// Bounding the overlap bounds exactly that: the 44 types the rule
// subtracts from the unreached count are, by this test, types nothing
// admits.
func TestMarkerlessVetoOverlapWithAdmittedDoesNotGrow(t *testing.T) {
	var both []string
	for typeName := range identity.MarkerlessTypes {
		if _, admitted := identity.DefaultTable[typeName]; admitted {
			both = append(both, typeName)
		}
	}
	sort.Strings(both)
	if len(both) > markerlessAdmittedOverlapMax {
		t.Errorf("%d type(s) are in both internal/live/identity.DefaultTable and MarkerlessTypes, above the "+
			"ratchet ceiling of %d (markerlessAdmittedOverlapMax). The markerless rule refuses to admit "+
			"these, so a row for one of them is the table contradicting a derived veto. Retract the row, or "+
			"show why the rule does not reach it - raising this constant is neither. Overlap: %v",
			len(both), markerlessAdmittedOverlapMax, both)
	}
	if len(both) < markerlessAdmittedOverlapMax {
		t.Logf("%d admitted rows overlap the markerless veto, below the ceiling of %d; lower "+
			"markerlessAdmittedOverlapMax to match", len(both), markerlessAdmittedOverlapMax)
	}
}

// TestMarkerlessVetoNamesOnlyTypesTheProviderServes is the same question
// TestNoVetoNamesATypeTheProviderDoesNotServe asks of the hand ledger. A
// veto entry outside the provider's roster subtracts from the unreached
// count without corresponding to anything a configuration could name.
func TestMarkerlessVetoNamesOnlyTypesTheProviderServes(t *testing.T) {
	universe := providerTypeUniverse(t)

	var strays []string
	for typeName := range identity.MarkerlessTypes {
		if !universe[typeName] {
			strays = append(strays, typeName)
		}
	}
	sort.Strings(strays)
	if len(strays) > 0 {
		t.Errorf("%d markerless veto entr(y/ies) name a type live/survey-full.json's provider roster does "+
			"not contain: %v - the rule reads that roster, so an entry outside it cannot have been derived "+
			"from the signal the rule claims to read",
			len(strays), strays)
	}
}

// TestMarkerlessVetoIsUntaggableInTheSurvey checks the rule's own premise
// against the artifact the rule reads, rather than against the rule. Every
// vetoed type must be one live/survey-full.json marks untaggable: the
// conjunction is what makes the veto correct, and the taggable half is the
// one an estate could disprove by writing a marker. internal/live/stamp's
// TestPinnedTaggabilityMatchesTheSurvey ties that signal to the predicate
// the run-time marker writer applies, so this chain ends at the provider
// schema and not at another of this generator's outputs.
func TestMarkerlessVetoIsUntaggableInTheSurvey(t *testing.T) {
	var survey struct {
		Types []struct {
			Type    string `json:"type"`
			Signals struct {
				Taggable bool `json:"taggable"`
			} `json:"signals"`
		} `json:"types"`
	}
	decodeInto(t, "survey-full.json", &survey)
	taggable := make(map[string]bool, len(survey.Types))
	for _, e := range survey.Types {
		taggable[e.Type] = e.Signals.Taggable
	}

	var wrong []string
	for typeName := range identity.MarkerlessTypes {
		if taggable[typeName] {
			wrong = append(wrong, typeName)
		}
	}
	sort.Strings(wrong)
	if len(wrong) > 0 {
		t.Errorf("%d markerless veto entr(y/ies) name a type live/survey-full.json marks TAGGABLE: %v - "+
			"a taggable type has somewhere to write the ownership marker, so the veto's stated reason "+
			"(%s) does not hold for it",
			len(wrong), wrong, identity.MarkerlessReason)
	}
}
