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
// admission. internal/live/harness's "unreached-types" entry is the number
// that measures that, and it does not move when a batch shuffles a type
// between the table and the ledger.

// The paragraphs below are the provenance for two bounds that no longer live
// in this file. Both moved into the burndown registry in
// internal/live/harness on 2026-08-16 - as entries "unreached-types" and
// "markerless-veto-admitted-overlap" - so that a bound, the measurement
// behind it and the roster it is counted against cannot drift apart the way
// tools/row-gen's annotation ledger did twice in two days. The text is kept
// here because it is the evidence, and the registry entry's History points
// back at it.
//
// unreachedRatchetMax was the highest number of provider resource types that
// may be in none of internal/live/identity.DefaultTable,
// tools/row-gen/rejected.json and internal/live/identity.MarkerlessTypes.
//
// Measured at 621 on 2026-08-16 against hashicorp/aws 6.59.0: 876 aws_ rows
// admitted, 81 vetoed by hand, 121 vetoed by the markerless rule and counted
// by neither of the other two rosters, 621 unreached, summing to
// live/survey-full.json's 1699 exactly.
//
// It stood at 665 before the markerless rule landed (#249), and at 649 while
// that rule read only the CloudFormation registry's own verdict. The rule
// vetoes 150 types in total, of which 29 the hand ledger had already reached;
// the remaining 121 move this count. That split used to read 44 and 77,
// because 77 vetoed types were still admitted; -emit now retracts them, so
// they moved from the admitted column to the vetoed one and this difference
// did not move at all - which is the property this constant exists to have.
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
//
// It is now internal/live/harness's "unreached-types" entry.

// universeFloor was the anti-tamper leg. The count this file ratchets is a
// difference, so shrinking live/survey-full.json's type roster lowers it just
// as effectively as admitting a type does, and shrinking it is the cheaper
// edit. hashicorp/aws has never lost a hundred resource types in a release.
//
// It is now that entry's Denominator, which every other migrated bound had
// to answer for too: two of the four had no equivalent recorded anywhere.

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
	// The roster floor that used to sit here is the "unreached-types" entry's
	// Denominator in internal/live/harness, checked before that entry's bound
	// rather than beside it. The tests below use this roster to ask whether a
	// named type exists, which a shrunken roster answers wrongly in the
	// direction of a false failure, not a false pass.
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
// aws_ rows. The table's other rows are one of two things: the effects set
// the fork keeps in the state file (null_resource, terraform_data, the
// random_* and time_* families), which are not live objects at all, or -
// since issue #326 - a live object served by a real provider that simply
// is not AWS (the four Kubernetes-provider rows). No AWS roster describes
// either kind, so rather than exempt them by name, this asks the row
// itself: a non-aws_ row is acceptable when it is RecordBacked (the
// property that makes it an effect rather than a live object) or when it
// is NonAWSProvider (the property that names the roster gap explicitly -
// tools/survey-gen/untaggable_render.go's own nonAWSAdmittedUntaggable
// ledger records the same fact and the same reason). A hand-written
// exemption list would go stale; the row's own field cannot.
func TestAdmittedTableNamesOnlyTypesTheProviderServes(t *testing.T) {
	universe := providerTypeUniverse(t)

	var strays, unrostered []string
	for typeName, row := range identity.DefaultTable {
		if len(typeName) < 4 || typeName[:4] != "aws_" {
			if !row.RecordBacked && !row.NonAWSProvider {
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
		t.Errorf("internal/live/identity.DefaultTable has %d non-aws_ row(s) that are neither RecordBacked nor "+
			"NonAWSProvider: %v - live/survey-full.json describes one provider, so this test can say nothing "+
			"about a live type from another one; it needs a roster for that provider before it can claim to "+
			"check the row",
			len(unrostered), unrostered)
	}
	if len(strays) > 0 {
		sort.Strings(strays)
		t.Errorf("%d admitted row(s) name a type live/survey-full.json's provider roster does not contain: %v",
			len(strays), strays)
	}
}

// markerlessAdmittedOverlapMax was the number of rows internal/live/identity's
// generated table still admits that the markerless rule vetoes, and it is a
// ratchet down to zero.
//
// It reached zero on 2026-08-16 (issue #249). It stood at 77 for as long as
// the rule (tools/row-gen/markerless.go) was applied only to what may be
// admitted NEXT - tools/row-gen's PROPOSE stage has never been able to offer
// a vetoed type - while 77 rows an earlier batch let through stayed in the
// table. tools/row-gen's -emit now filters the emitted rows by the same
// roster, so the two rosters are disjoint by construction and this test is
// the anti-tamper leg for that.
//
// The retraction waited on internal/live/lint's RuleMarkerlessType, because
// on its own it would have been a measurement change dressed as a support
// change: a plain unadmitted-type finding is NON-blocking in
// internal/live/check.ClassifyOnboarding (ladder.go's own switch), so corpus
// estates would have climbed the onboarding ladder while no configuration
// became any more applyable. RuleMarkerlessType is blocking and fires ahead
// of RuleUnadmittedType, and it is consulted before
// identity.SynthesizeTypeIdentity so a retracted row cannot fall through to
// schema-fallback admission. The ladder moves the other way as a result,
// which is the honest direction for a change that removes support.
//
// Zero is now the ceiling AND the floor: a non-zero count means a row for a
// vetoed type reached the table by some route -emit does not filter, which a
// hand-pasted row can do and PROPOSE cannot, and it needs to be a deliberate
// edit rather than a silent one.
//
// It is now internal/live/harness's "markerless-veto-admitted-overlap"
// entry, whose Denominator is the size of MarkerlessTypes itself: the
// overlap also falls to zero if the veto roster is emptied, and nothing
// recorded that before.

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
