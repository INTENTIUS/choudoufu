// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestMarkerlessVetoRunsBeforeTheSchemaFallback is the ordering assertion
// the rule exists to make safe, and the only one of these tests that would
// still pass if the veto were consulted in the wrong place.
//
// A type on [identity.MarkerlessTypes] with a provider identity schema
// complete enough for [identity.SynthesizeTypeIdentity] to admit it is
// exactly the shape that makes the ordering load-bearing. Behind the
// fallback the veto is a no-op for such a type and the refusal it exists to
// raise never fires; ahead of it, the type is refused whatever the schema
// says. The fixture builds that schema rather than looking for a real type
// that has one, because which real types ship an identity schema is a
// property of a provider release and would make this test's subject move
// under it.
func TestMarkerlessVetoRunsBeforeTheSchemaFallback(t *testing.T) {
	const vetoed = "aws_thing"

	if _, already := identity.MarkerlessTypes[vetoed]; already {
		t.Fatalf("%s is on the real roster; this test injects it and would not be proving anything", vetoed)
	}
	identity.MarkerlessTypes[vetoed] = struct{}{}
	t.Cleanup(func() { delete(identity.MarkerlessTypes, vetoed) })

	cfg := loadConfigDir(t, "testdata/schema-admitted")
	signal, diags := identity.ScanConfig(t.Context(), cfg)
	if diags.HasErrors() {
		t.Fatalf("scanning the fixture: %s", diags.Err())
	}
	schemas := thingSchema()

	// The control: without the veto this exact call admits the type. That is
	// what TestCheckWithSchemaAdmitsTypeOutsideTable asserts, and it is why
	// a veto behind the fallback would be invisible.
	if _, ok := identity.SynthesizeTypeIdentity(vetoed, schemas, signal); !ok {
		t.Fatalf("the schema fallback no longer admits %s, so this test cannot tell the two orderings apart", vetoed)
	}

	if admitted(vetoed, schemas, signal) {
		t.Errorf("admitted(%s) consulted the schema fallback before the veto: a retracted type would come back with plan-and-create-only support instead of being refused", vetoed)
	}

	issues := CheckWith(t.Context(), cfg, Context{Schemas: schemas})
	if len(issues) != 1 || issues[0].Rule != RuleMarkerlessType {
		t.Fatalf("expected exactly one markerless-type issue with schemas offered, got %v", issues)
	}
}

// TestMarkerlessVetoNeverOverridesARatifiedRow is the other half of the
// ordering, and the one that keeps this rule from retracting anything on
// its own.
//
// The roster and the admission table overlap today - a type row-gen has
// vetoed by rule but not yet stopped emitting a row for - and every type in
// that overlap must keep the support its row describes. The assertion is
// computed against the two committed tables rather than against a named
// type, so it holds as the overlap shrinks and holds vacuously at zero
// rather than pinning a type that has left it.
func TestMarkerlessVetoNeverOverridesARatifiedRow(t *testing.T) {
	var overlap int
	for typeName := range identity.MarkerlessTypes {
		if _, inTable := admittedTypesV0[typeName]; !inTable {
			continue
		}
		overlap++
		if markerlessVetoed(typeName) {
			t.Errorf("%s has a ratified row and the veto claimed it anyway; the row is what ships until row-gen stops emitting it", typeName)
		}
		if !admitted(typeName, nil, nil) {
			t.Errorf("%s has a ratified row and admitted() refused it", typeName)
		}
	}
	t.Logf("%d of %d markerless types still carry a ratified row", overlap, len(identity.MarkerlessTypes))
}

// TestMarkerlessRefusalOffersNoNextStep holds the one property that
// separates this rule's message from [RuleUnadmittedType]'s.
//
// RuleUnadmittedType closes by asking the reader to open an issue naming
// the type and its documented import ID. For a vetoed type that is an
// invitation to a ratification the derivation has already refused on
// evidence, and an operator who acts on it spends a round trip to be told
// no. The two sentences must not converge.
func TestMarkerlessRefusalOffersNoNextStep(t *testing.T) {
	issues := markerlessFixtureIssues(t)
	detail := issues[0].Detail

	for _, forbidden := range []string{
		"open an issue",
		"admission table, and",
		"Setting ",
		"setting it explicitly",
	} {
		if strings.Contains(detail, forbidden) {
			t.Errorf("the markerless refusal offers %q as a next step; there is none: %s", forbidden, detail)
		}
	}

	// Both facts, and neither of them paraphrased locally: the roster's own
	// reason and the consequence internal/live/stamp states to an operator
	// who reached apply. #111 is what one fact with two wordings costs.
	if !strings.Contains(detail, identity.MarkerlessReason) {
		t.Errorf("the refusal does not carry identity.MarkerlessReason verbatim, so the roster and this message can disagree: %s", detail)
	}
	if !strings.Contains(detail, UnfindableClause) {
		t.Errorf("the refusal does not carry UnfindableClause verbatim: %s", detail)
	}
}

// TestMarkerlessRefusalCarriesNoResidueSentence pins a deliberate omission,
// because it is the kind that reads as an oversight.
//
// [RuleUnadmittedType] appends a residue cohort sentence - a deprecated
// service, a CloudFormation type the Registry ships no handler for - when
// the type is in one. Those sentences explain why a type is not wired, and
// a vetoed type's problem is not wiring: a working handler would not give
// it anywhere to put the marker. Appending one would name a blocker that is
// not the blocker.
func TestMarkerlessRefusalCarriesNoResidueSentence(t *testing.T) {
	issues := markerlessFixtureIssues(t)
	if strings.Contains(issues[0].Detail, "CloudFormation") {
		t.Errorf("a residue cohort sentence reached the markerless refusal: %s", issues[0].Detail)
	}
}

// TestMarkerlessLimitsFixtureIsOnTheRoster is the external check on the
// fixture itself: the limits directory has to name a type the generated
// roster actually vetoes, read from the roster rather than from this
// package's opinion of it. A provider release that adds a tags argument to
// the fixture's type takes it off the roster, and this fails before
// TestLimitsEnforced does, with the reason rather than the symptom.
func TestMarkerlessLimitsFixtureIsOnTheRoster(t *testing.T) {
	dir := filepath.Join(limitsDir(t), "markerless-type")
	cfg := loadConfigDir(t, dir)
	issues := CheckContext(t.Context(), cfg)
	if len(issues) == 0 {
		t.Fatal("the markerless-type fixture produced no issues")
	}
	for _, issue := range issues {
		if _, ok := identity.MarkerlessTypes[issue.Type]; !ok {
			t.Errorf("the fixture declares %s, which is not on identity.MarkerlessTypes", issue.Type)
		}
		if _, inTable := admittedTypesV0[issue.Type]; inTable {
			t.Errorf("the fixture declares %s, which has a ratified row, so it exercises nothing", issue.Type)
		}
		// Two credential types are on the roster and do fire this rule, but
		// their governing exclusion is the credential ruling, and a fixture
		// built on one would leave a reader unable to tell which of the two
		// reasons the rule is expressing. Keyed on the declared type rather
		// than on the file's text, which the fixture's own comment names.
		switch issue.Type {
		case "aws_iam_access_key", "aws_iot_certificate":
			t.Errorf("the fixture declares %s, a credential type whose governing exclusion is the credential ruling rather than this rule", issue.Type)
		}
	}
}

// markerlessFixtureIssues runs the limits fixture and returns its issues,
// having checked there is exactly one and that it is this rule's.
func markerlessFixtureIssues(t *testing.T) []Issue {
	t.Helper()
	cfg := loadConfigDir(t, filepath.Join(limitsDir(t), "markerless-type"))
	issues := CheckContext(t.Context(), cfg)
	if len(issues) != 1 || issues[0].Rule != RuleMarkerlessType {
		t.Fatalf("expected exactly one markerless-type issue, got %v", issues)
	}
	return issues
}
