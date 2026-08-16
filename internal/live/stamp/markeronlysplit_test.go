// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
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

type markerOnlyBuckets struct {
	unconditional []string
	conditional   []string
	cloudArgument []string
	cloudBare     []string
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
			b.unconditional = append(b.unconditional, name)
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
	for _, s := range [][]string{b.unconditional, b.conditional, b.cloudArgument, b.cloudBare} {
		sort.Strings(s)
	}
	return b
}

// TestMarkerOnlySplitIsDerivedAndPopulated is the split itself: it holds
// without any list of type names, and every bucket that the refusal wording
// distinguishes has somebody in it.
//
// A bucket falling to zero is not a pass. It means either the provider
// stopped publishing that shape or the derivation stopped seeing it, and in
// both cases a branch of UnmarkedDiscoveryDetail is now unreachable while
// its test still renders it from hand-built arguments - which is exactly how
// a message defect stays green here.
func TestMarkerOnlySplitIsDerivedAndPopulated(t *testing.T) {
	b := markerOnlySplit(t)

	for _, tc := range []struct {
		name  string
		types []string
	}{
		{"unconditional (ServerAssigned, untaggable)", b.unconditional},
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
	if len(b.unconditional) == 0 {
		t.Fatal("no unconditional types to check the negative half against")
	}
	got := UnmarkedDiscoveryDetail(addr, identity.BlockDiscovery{Cause: identity.DiscoveryServerAssigned})
	if strings.Contains(got, "needs no marker at all") || strings.Contains(got, "Setting ") {
		t.Errorf("the server-assigned wall was offered a configuration edit:\n  %s", got)
	}
}
