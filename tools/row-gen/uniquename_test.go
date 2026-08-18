// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestUniqueNameArgumentCrossesBothSources exercises [uniqueNameArgument]
// directly, over every shape the crossing has to refuse.
//
// It is a unit test rather than a corpus read for a reason the mutation check
// found: at hashicorp/aws 6.59.0 the DeclaredUnique condition excludes NO
// type that the registry half has not already excluded, so deleting it leaves
// every artifact-driven test in this repository green (see
// TestUniqueNameDocHalfExcludesNothingFurtherAtThePin below for the
// measurement). The condition is still the rule - two sources, never one -
// and this is where it is proved to work.
func TestUniqueNameArgumentCrossesBothSources(t *testing.T) {
	arg := func(name string, required, unique bool) argumentRefEntry {
		return argumentRefEntry{Name: name, Required: required, DeclaredUnique: unique}
	}
	for _, tc := range []struct {
		name string
		prop string
		row  importGrammarRow
		want string
		ok   bool
	}{
		{
			name: "both sources agree on a nested Name",
			prop: "CachePolicyConfig/Name",
			row:  importGrammarRow{ArgumentReference: []argumentRefEntry{arg("name", true, true)}},
			want: "name", ok: true,
		},
		{
			name: "both sources agree on a top-level Name",
			prop: "Name",
			row:  importGrammarRow{ArgumentReference: []argumentRefEntry{arg("name", true, true)}},
			want: "name", ok: true,
		},
		{
			// The provider half missing. The registry says the name is
			// unique; the provider's own argument reference does not, and
			// one source is not the agreement this rule requires.
			name: "the docs do not make the claim",
			prop: "CachePolicyConfig/Name",
			row:  importGrammarRow{ArgumentReference: []argumentRefEntry{arg("name", true, false)}},
			ok:   false,
		},
		{
			// An optional name is one a configuration may leave out, and a
			// name the configuration does not state is a name no listing can
			// be matched against.
			name: "the argument is optional",
			prop: "CachePolicyConfig/Name",
			row:  importGrammarRow{ArgumentReference: []argumentRefEntry{arg("name", false, true)}},
			ok:   false,
		},
		{
			// The two sources are talking about different things. Nothing
			// guarantees a schema calling PolicyName unique and a doc calling
			// `name` unique describe one value, and binding on the confusion
			// would read a claim about one thing as a claim about another.
			name: "the two sources name different arguments",
			prop: "PolicyName",
			row:  importGrammarRow{ArgumentReference: []argumentRefEntry{arg("name", true, true)}},
			ok:   false,
		},
		{
			name: "the snake_case spelling is how they are matched",
			prop: "PolicyName",
			row:  importGrammarRow{ArgumentReference: []argumentRefEntry{arg("policy_name", true, true)}},
			want: "policy_name", ok: true,
		},
		{
			name: "no grammar row at all",
			prop: "CachePolicyConfig/Name",
			row:  importGrammarRow{},
			ok:   false,
		},
		{
			name: "no registry property at all",
			prop: "",
			row:  importGrammarRow{ArgumentReference: []argumentRefEntry{arg("name", true, true)}},
			ok:   false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := uniqueNameArgument(tc.prop, tc.row)
			if got != tc.want || ok != tc.ok {
				t.Errorf("uniqueNameArgument(%q, %+v) = (%q, %v), want (%q, %v)", tc.prop, tc.row, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// uniqueNameStageCounts measures the rescue one condition at a time, over the
// committed artifacts, restricted to the population that has anything to gain
// - the types the markerless veto would otherwise refuse.
func uniqueNameStageCounts(t *testing.T) (vetoed, docHalf, registryHalf, crossed []string) {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	ratified, err := loadRatified(filepath.Join(root, ratifiedJSONRel))
	if err != nil {
		t.Fatal(err)
	}
	proposals := loadAllForTest(t)
	grammar := loadImportGrammarForTest(t)
	survey := loadSurveyForTest(t)

	byType := indexByType(proposals)
	classified, documented := serverAssignmentVerdicts(proposals, grammar)

	for typeName, entry := range survey {
		if _, admitted := ratified[typeName]; admitted {
			continue
		}
		agree := sourcesAgree(typeName, byType, grammar)
		if !markerless(entry.Signals.Taggable, false, false, identity.TypeIdentity{}, classified[typeName], documented[typeName], agree, false) {
			continue
		}
		vetoed = append(vetoed, typeName)
		for _, a := range grammar[typeName].ArgumentReference {
			if a.Required && a.DeclaredUnique {
				docHalf = append(docHalf, typeName)
				break
			}
		}
		p, ok := byType[typeName]
		if !ok || p.UniqueNameProp == "" {
			continue
		}
		registryHalf = append(registryHalf, typeName)
		if _, ok := uniqueNameArgument(p.UniqueNameProp, grammar[typeName]); ok {
			crossed = append(crossed, typeName)
		}
	}
	for _, s := range [][]string{vetoed, docHalf, registryHalf, crossed} {
		sort.Strings(s)
	}
	return vetoed, docHalf, registryHalf, crossed
}

// TestUniqueNameDocHalfExcludesNothingFurtherAtThePin records what the
// mutation check found, so nobody has to find it again.
//
// The crossing is real and it narrows: of the 145 types the markerless veto
// refuses at hashicorp/aws 6.59.0, the provider's own argument reference
// calls the name unique for NINE, and the CloudFormation registry schema
// agrees for FOUR. The five it drops - aws_ec2_traffic_mirror_filter_rule,
// aws_kms_custom_key_store, aws_lambda_layer_version, aws_opensearch_package
// and aws_sagemaker_image_version - are the whole value of asking two
// sources.
//
// What is NOT true is that the narrowing runs both ways. Within this
// population the registry half is doing all of the excluding: every type it
// keeps also clears the doc half, so deleting the DeclaredUnique condition
// from [uniqueNameArgument] admits the same four types and leaves every
// artifact-driven test in this repository green. That was measured by
// deleting it, not reasoned about.
//
// So this test pins the shape rather than pretending the condition is load
// bearing here. It fails if the doc half stops being a superset - which is
// the interesting direction, because a registry-kept type the docs do NOT
// call unique is a type admitted on one source, and that is the bar this
// whole mechanism claims to hold.
func TestUniqueNameDocHalfExcludesNothingFurtherAtThePin(t *testing.T) {
	vetoed, docHalf, registryHalf, crossed := uniqueNameStageCounts(t)

	if len(vetoed) == 0 {
		t.Fatal("no type is vetoed as markerless at all, so every count below is over an empty set")
	}
	if len(docHalf) <= len(registryHalf) {
		t.Errorf("the provider docs assert a unique name for %d of the %d vetoed types and the registry for %d; "+
			"the crossing is supposed to NARROW, and a doc half no wider than the registry half means one of the two scrapes has stopped finding anything",
			len(docHalf), len(vetoed), len(registryHalf))
	}
	if len(crossed) != len(registryHalf) {
		t.Errorf("crossing both sources admits %d types and the registry half alone admits %d: %v vs %v.\n"+
			"That is a CHANGE from the pinned shape, and it is the good direction - the doc half now excludes something "+
			"the registry half kept. Update this test's prose to say which type and why, rather than deleting the assertion.",
			len(crossed), len(registryHalf), crossed, registryHalf)
	}

	// The superset relation, which is the claim that actually matters: every
	// type the registry half keeps must also be one the provider's own
	// documentation calls unique. A type kept here on the registry alone
	// would be a type admitted on one source.
	inDocHalf := make(map[string]bool, len(docHalf))
	for _, name := range docHalf {
		inDocHalf[name] = true
	}
	for _, name := range registryHalf {
		if !inDocHalf[name] {
			t.Errorf("%s clears the registry half and not the provider documentation half, so it would be admitted on one source", name)
		}
	}
}

// TestUniqueNameRowsAreDerivedNotRatified pins the two properties that make
// this a derived class of row rather than a hand-written one, both of which a
// half-landed change would break silently.
func TestUniqueNameRowsAreDerivedNotRatified(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	ratified, err := loadRatified(filepath.Join(root, ratifiedJSONRel))
	if err != nil {
		t.Fatal(err)
	}
	survey := loadSurveyForTest(t)
	rows := uniqueNameRows(ratified, survey, loadAllForTest(t), loadImportGrammarForTest(t))
	if len(rows) == 0 {
		t.Fatal("the unique-name derivation produced no rows at all, so every assertion below passes over nothing")
	}

	for _, typeName := range sortedUniqueNameTypes(rows) {
		row := rows[typeName]
		// The precondition, asserted through the survey rather than through
		// [identity.MarkerlessTypes] - a rescued type has left that roster by
		// the time the table ships, so reading it would prove nothing.
		//
		// Like the DeclaredUnique condition above, the precondition excludes
		// nothing further at this pin: mutating it away leaves the derived
		// set at the same four types, because the only unratified types that
		// clear the crossing happen to be vetoed ones. It is kept, and this
		// is the assertion that keeps it honest, because it is what bounds
		// the rescue's blast radius as the artifacts move - a taggable type
		// is found by its own marker and has no business being handed a
		// second, weaker way to be found.
		if entry, known := survey[typeName]; !known {
			t.Errorf("%s is derived but live/survey-full.json has never heard of it; the rescue is for types the veto catches, and the veto only ever sees surveyed types", typeName)
		} else if entry.Signals.Taggable {
			t.Errorf("%s is derived and the provider's own schema says it is taggable; a taggable type is found by its ownership marker and must not be handed a name match instead", typeName)
		}
		if _, alreadyRatified := ratified[typeName]; alreadyRatified {
			t.Errorf("%s is derived here AND carried by the ratified corpus; a ratified row is what ships for its type and a generator must not override one", typeName)
		}
		if !row.ServerAssigned {
			t.Errorf("%s is derived without ServerAssigned; the identity is still minted by the provider - what changed is how the object is FOUND", typeName)
		}
		if len(row.Components) != 0 {
			t.Errorf("%s is derived with %d identity component(s); a server-assigned row builds no import ID from configuration", typeName, len(row.Components))
		}
		if row.Reason != uniqueNameReason {
			t.Errorf("%s carries a reason other than the one ruling; the whole class shares one sentence so a batch cannot edit one copy and diverge", typeName)
		}
		if !row.UniqueName.Set() {
			t.Errorf("%s is derived with no UniqueName, so discovery would have nothing to match on", typeName)
		}
		if !strings.HasSuffix(row.UniqueName.Property, snakeToCFNTail(row.UniqueName.Attrs[0])) {
			t.Errorf("%s's property path %q does not end in the property its argument %q corresponds to", typeName, row.UniqueName.Property, row.UniqueName.Attrs[0])
		}
		if row.ImportSyntax == "" {
			t.Errorf("%s is derived with no import syntax; identity.TestTableEntriesWellFormed requires one on every non-record row", typeName)
		}
	}
}

// snakeToCFNTail is the test's own inverse of [snakeCase], used only to check
// that a derived row's two halves describe one argument. It is deliberately
// not shared with the derivation: a test that reused the production spelling
// would agree with it by construction.
func snakeToCFNTail(arg string) string {
	var b strings.Builder
	for _, part := range strings.Split(arg, "_") {
		if part == "" {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]) + part[1:])
	}
	return b.String()
}
