// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// The types that can reach SummaryUnmarkedApply are not one population, and
// they have been counted as one. GitHub issues #233 and #249 both list them
// by name; the lists disagree, both are stale the moment the provider moves,
// and the disagreement is what sent a .tf grep at the corpus and produced an
// attribution wrong in both directions.
//
// They separate by rule, from two generated sources this package does not
// write - the identity table's own component fields and survey-full.json's
// signals.taggable - into buckets that differ in the only way an operator
// cares about: whether there is an argument they can set.
//
//	unconditional  ServerAssigned, untaggable          no edit exists
//	conditional    a ServerAssignedIfAbsent component  name the object
//	cloudArgument  a cloud component with an argument  name the property
//	cloudBare      a cloud component with none         no edit exists
//
// Only untaggable types appear at all: a taggable one carries the marker and
// never reaches this refusal, which is the marker path working.
//
// The FIRST bucket is empty as of the markerless retraction (#249) and must
// stay that way. "ServerAssigned and untaggable" is the markerless rule's
// own predicate word for word, so a shipped table that still admitted such a
// type would be admitting a type nothing can ever find again - the whole
// point of the retraction. That flips this bucket's assertion: the other
// three must be populated, and this one must be empty, with
// TestMarkerOnlyUnconditionalBucketIsEmptyByVeto checking that the veto's
// roster and the table stay disjoint over it. What that test does NOT check
// is the veto rule itself - see its own doc comment, and
// markerlessdocs_test.go for the guard that does.
//
// The refusal wording it used to serve is NOT dead, which is why the
// rendering half below still exercises it. internal/live/lint admits a type
// the generated table does not cover when the provider's identity schema
// settles it (identity.SynthesizeTypeIdentity), and lint's markerless veto
// can only pre-empt that for a type the roster names - which is every type
// live/survey-full.json covers, and no type it does not. A provider release
// that adds an untaggable server-assigned type, or any type from a provider
// this fork has never surveyed, still reaches the unconditional wall through
// the fallback. The branch is unreachable from the shipped TABLE, not from a
// run.

type markerOnlyBuckets struct {
	unconditional []string
	conditional   []string
	cloudArgument []string
	cloudBare     []string

	// nameBound is the fifth bucket, added by issue #272: ServerAssigned and
	// untaggable exactly like unconditional, and admitted anyway, because
	// the type's row carries an [identity.TypeIdentity.UniqueName] and a
	// listing recognises the object by the name the configuration states.
	//
	// It is a separate bucket rather than an exclusion from unconditional
	// because the two are the same predicate with one field between them,
	// and a reader has to be able to see how many types are in the
	// exception. An empty nameBound alongside a non-empty MarkerlessTypes
	// means the rescue stopped firing; a growing one is it working.
	nameBound []string
}

// markerOnlySplit derives the buckets. Nothing here names a resource type;
// every membership test is a field on the type's own generated row crossed
// with the provider's own taggability.
func markerOnlySplit(t *testing.T) markerOnlyBuckets {
	t.Helper()
	survey := readSurveyTaggable(t)

	var b markerOnlyBuckets
	for _, name := range identity.AdmittedTypes() {
		taggable, known := survey[name]
		if !known || taggable {
			// Unknown to the survey is the logical and effect-only types
			// (null_resource, random_*, terraform_data), which have no
			// cloud object and no marker; taggable is the marker path.
			continue
		}
		entry, ok := identity.LookupType(name)
		if !ok {
			t.Fatalf("%s is in AdmittedTypes but LookupType does not know it", name)
		}
		if entry.ServerAssigned {
			if entry.UniqueName.Set() {
				b.nameBound = append(b.nameBound, name)
			} else {
				b.unconditional = append(b.unconditional, name)
			}
			continue
		}
		for _, comp := range entry.Components {
			if comp.ServerAssignedIfAbsent {
				b.conditional = append(b.conditional, name)
			}
			if comp.Cloud == identity.CloudNone {
				continue
			}
			if len(comp.Attrs) > 0 {
				b.cloudArgument = append(b.cloudArgument, name)
			} else {
				b.cloudBare = append(b.cloudBare, name)
			}
		}
	}
	for _, s := range [][]string{b.unconditional, b.conditional, b.cloudArgument, b.cloudBare, b.nameBound} {
		sort.Strings(s)
	}
	return b
}

// TestMarkerOnlySplitIsDerivedAndPopulated is the split itself: it holds
// without any list of type names, and every bucket that the refusal wording
// distinguishes, and that the shipped table can still reach, has somebody in
// it.
//
// A bucket falling to zero is not a pass. It means either the provider
// stopped publishing that shape or the derivation stopped seeing it, and in
// both cases a branch of UnmarkedDiscoveryDetail is now unreachable while
// its test still renders it from hand-built arguments - which is exactly how
// a message defect stays green here.
//
// The unconditional bucket is the deliberate exception and is checked in the
// opposite direction by TestMarkerOnlyUnconditionalBucketIsEmptyByVeto; see
// this file's own doc comment.
func TestMarkerOnlySplitIsDerivedAndPopulated(t *testing.T) {
	b := markerOnlySplit(t)

	for _, tc := range []struct {
		name  string
		types []string
	}{
		{"conditional (ServerAssignedIfAbsent, untaggable)", b.conditional},
		{"cloud component with an argument, untaggable", b.cloudArgument},
		{"cloud component with no argument, untaggable", b.cloudBare},
	} {
		if len(tc.types) == 0 {
			t.Errorf("bucket %q is empty; the wording branch that serves it can no longer be reached from the shipped table", tc.name)
		}
	}

	// ServerAssigned is a whole-type verdict and the other three are
	// per-component, so a type in the first bucket and in any other would
	// mean the table says both "no argument reconstructs this" and "this
	// argument does". The resolver returns on ServerAssigned before it
	// looks at a single component, so such a row would resolve one way and
	// be counted the other.
	unconditional := map[string]bool{}
	for _, name := range b.unconditional {
		unconditional[name] = true
	}
	for _, other := range [][]string{b.conditional, b.cloudArgument, b.cloudBare} {
		for _, name := range other {
			if unconditional[name] {
				t.Errorf("%s is ServerAssigned and also carries components that name its identity; resolution takes the first and the count takes the second", name)
			}
		}
	}
}

// TestMarkerOnlySplitDecidesWhetherAnEditExists is the split's whole point,
// asserted on the sentence rather than on the bucket: a type in a bucket with
// an argument must produce a refusal that names one, and a type in a bucket
// without must not produce a refusal that pretends there is one.
//
// The arguments handed to each rendering come from the type's own row, so a
// bucket whose rows carry no argument name cannot accidentally satisfy the
// positive half.
func TestMarkerOnlySplitDecidesWhetherAnEditExists(t *testing.T) {
	b := markerOnlySplit(t)
	addr := addrs.ConfigResource{
		Module: addrs.RootModule,
		Resource: addrs.Resource{
			Mode: addrs.ManagedResourceMode,
			Type: "aws_glue_catalog_table",
			Name: "example",
		},
	}

	// Positive: every untaggable type whose identity has a conditional or
	// cloud-defaulted argument renders a sentence naming that argument.
	for _, name := range append(append([]string{}, b.conditional...), b.cloudArgument...) {
		entry, _ := identity.LookupType(name)
		for _, comp := range entry.Components {
			var disco identity.BlockDiscovery
			switch {
			case comp.ServerAssignedIfAbsent:
				disco = identity.BlockDiscovery{Cause: identity.DiscoveryNameOmitted, Args: comp.Attrs}
			case comp.Cloud != identity.CloudNone && len(comp.Attrs) > 0:
				disco = identity.BlockDiscovery{
					Cause: identity.DiscoveryCloudUnknown,
					Args:  append([]string{string(comp.Cloud)}, comp.Attrs...),
				}
			default:
				continue
			}
			got := UnmarkedDiscoveryDetail(addr, disco)
			if !strings.Contains(got, comp.Attrs[0]) {
				t.Errorf("%s: the refusal does not name %s, the argument that resolves it:\n  %s", name, comp.Attrs[0], got)
			}
			if !strings.Contains(got, "needs no marker at all") {
				t.Errorf("%s: the refusal names %s but does not say setting it removes the need for a marker:\n  %s", name, comp.Attrs[0], got)
			}
		}
	}

	// Negative: the unconditional wall must not acquire a next step. This
	// is the half that matters for safety - a sentence offering an edit
	// that does not exist sends an operator to change a configuration that
	// will refuse identically afterwards.
	//
	// Its population is [identity.MarkerlessTypes], not b.unconditional:
	// since #249 the shipped table admits none of these, and the wall is
	// reached through internal/live/lint's schema fallback for a type the
	// roster cannot pre-empt. Reading the roster keeps the guard anchored to
	// a real, non-empty set instead of skipping when the bucket empties.
	if len(identity.MarkerlessTypes) == 0 {
		t.Fatal("identity.MarkerlessTypes is empty; the population this half guards has vanished, not been fixed")
	}
	got := UnmarkedDiscoveryDetail(addr, identity.BlockDiscovery{Cause: identity.DiscoveryServerAssigned})
	if strings.Contains(got, "needs no marker at all") || strings.Contains(got, "Setting ") {
		t.Errorf("the server-assigned wall was offered a configuration edit:\n  %s", got)
	}
}

// TestMarkerOnlyUnconditionalBucketIsEmptyByVeto is the inverted assertion
// for the one bucket the markerless retraction emptied (#249).
//
// WHAT IT CHECKS, stated after issue #257 corrected an earlier version of
// this comment that claimed more:
//
//   - the shipped table admits no untaggable ServerAssigned type. That is
//     the retraction holding, and it is checked twice over, once against
//     [identity.AdmittedTypes] and once against the survey's own roster, so
//     the two failures name the same set;
//   - the veto's roster and the table are DISJOINT. A type cannot be both
//     refused as unfindable and admitted as resolvable;
//   - every type on the roster is untaggable per live/survey-full.json.
//     That is the veto's other leg, and the count equality below is what
//     makes it an assertion: a roster carrying a taggable type would be
//     smaller here than [identity.MarkerlessTypes] is.
//
// WHAT IT DOES NOT CHECK is server-assignment - the leg that decides which
// untaggable types get retracted. The version of this comment 6bb23bcbf8
// shipped said it did, on the strength of the loop below re-deriving the
// bucket's predicate "over live/survey-full.json's whole type roster". It
// does not: the loop's own membership test is [identity.MarkerlessTypes],
// which markerlessRoster has already filtered to untaggable types out of
// the same survey file, so the set it builds IS the roster and consults
// ServerAssigned nowhere. An audit deleted the rule's server-assignment leg
// outright, retracting 217 further rows, and this test passed.
//
// TestMarkerlessVetoIsNotRefutedByTheImportDocs, in markerlessdocs_test.go,
// is the guard for that leg. It reads live/import-grammar.json, which no
// part of the roster's construction can edit into agreement with itself,
// and it fails under that mutation with 196 named types.
func TestMarkerOnlyUnconditionalBucketIsEmptyByVeto(t *testing.T) {
	if got := markerOnlySplit(t).unconditional; len(got) > 0 {
		t.Errorf("%d admitted type(s) are still ServerAssigned and untaggable: %v - "+
			"nothing finds these objects again, which is what the markerless rule "+
			"(tools/row-gen/markerless.go) exists to refuse; retract the row rather than raising this",
			len(got), got)
	}

	if len(identity.MarkerlessTypes) == 0 {
		t.Fatal("identity.MarkerlessTypes is empty; every assertion below is a loop over it and would pass over nothing")
	}
	survey := readSurveyTaggable(t)
	var vetoedAndUntaggable, stillAdmitted []string
	for name, taggable := range survey {
		if taggable {
			continue
		}
		if entry, ok := identity.LookupType(name); ok && entry.ServerAssigned && !entry.UniqueName.Set() {
			// Admitted and matching the predicate: caught above, and
			// recorded here too so the two failures name the same set.
			//
			// A row carrying a UniqueName is issue #272's exception and is
			// counted in the nameBound bucket instead. The exclusion reads
			// the ROW's own field rather than a list of types, so a
			// regenerated table where the two-source evidence stopped
			// crossing puts its rows straight back here and fails, which is
			// the direction that matters.
			stillAdmitted = append(stillAdmitted, name)
		}
		if _, vetoed := identity.MarkerlessTypes[name]; vetoed {
			vetoedAndUntaggable = append(vetoedAndUntaggable, name)
		}
	}
	sort.Strings(stillAdmitted)

	// The veto's untaggability leg, asserted rather than assumed. Every roster
	// member has to have been seen here, so a roster carrying a type the
	// survey calls taggable - or one the survey has never heard of, which is
	// how a veto lands "on silence" - leaves a name behind.
	seen := make(map[string]bool, len(vetoedAndUntaggable))
	for _, name := range vetoedAndUntaggable {
		seen[name] = true
	}
	var notUntaggable []string
	for name := range identity.MarkerlessTypes {
		if seen[name] {
			continue
		}
		if taggable, known := survey[name]; !known {
			notUntaggable = append(notUntaggable, name+" (absent from the survey)")
		} else {
			notUntaggable = append(notUntaggable, fmt.Sprintf("%s (survey says taggable=%v)", name, taggable))
		}
	}
	sort.Strings(notUntaggable)

	var unrostered []string
	for _, name := range vetoedAndUntaggable {
		if _, admitted := identity.LookupType(name); admitted {
			unrostered = append(unrostered, name)
		}
	}
	sort.Strings(unrostered)

	if len(notUntaggable) > 0 {
		t.Errorf("%d of the %d type(s) on the markerless roster are not untaggable per live/survey-full.json:\n%s\n"+
			"the veto's second leg is that there is nowhere to write a marker, and a taggable type has somewhere",
			len(notUntaggable), len(identity.MarkerlessTypes), indentedSample(notUntaggable))
	}
	if len(unrostered) > 0 {
		t.Errorf("%d type(s) are on the markerless roster and still admitted: %v - "+
			"the roster and internal/live/identity.DefaultTable must be disjoint", len(unrostered), unrostered)
	}
	if len(stillAdmitted) > 0 {
		t.Errorf("%d untaggable ServerAssigned type(s) remain in the admission table: %v", len(stillAdmitted), stillAdmitted)
	}
}
