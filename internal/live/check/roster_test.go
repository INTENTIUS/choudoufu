// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
)

// These are GitHub issue #790's rung classifier and roster builder, tested
// directly against fabricated schemas and resolutions rather than through a
// loaded configuration - Analyze's own fixture-backed tests already cover
// the walk that gets [Report.Roster] built from real identity resolution and
// findings; this file covers the classification and merge logic in
// isolation, the same way stamp_gate_test.go tests stamp's own gate against
// a fabricated schema rather than a live provider.

func taggableSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":   {Type: cty.String, Computed: true},
			"tags": {Type: cty.Map(cty.String), Optional: true},
		},
	}}
}

func untaggableSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":   {Type: cty.String, Computed: true},
			"name": {Type: cty.String, Required: true},
			// Deliberately no "tags".
		},
	}}
}

// anyMarkerlessType returns one member of identity.MarkerlessTypes,
// arbitrarily but deterministically for one test run - the test only needs
// "some type this generator has already put in the record-only population",
// not a specific one, so it does not pin a type name that a future
// tools/row-gen regeneration could move out of the set.
func anyMarkerlessType(t *testing.T) string {
	t.Helper()
	for typeName := range identity.MarkerlessTypes {
		return typeName
	}
	t.Fatal("identity.MarkerlessTypes is empty; this test needs at least one record-only type to classify")
	return ""
}

func TestRungForType_TagGovernableComesFromTheSchema(t *testing.T) {
	schemas := map[string]providers.Schema{"test_bucket": taggableSchema()}
	if got := rungForType(schemas, "test_bucket"); got != RungTagGovernable {
		t.Errorf("rungForType(taggable) = %q, want %q", got, RungTagGovernable)
	}
}

func TestRungForType_DeclarationCarriedIsTheUntaggableDefault(t *testing.T) {
	schemas := map[string]providers.Schema{"test_named": untaggableSchema()}
	if got := rungForType(schemas, "test_named"); got != RungDeclarationCarried {
		t.Errorf("rungForType(untaggable, admitted) = %q, want %q", got, RungDeclarationCarried)
	}
}

// TestRungForType_NoSchemaCostsPrecisionNeverAWrongClaim: a type this run
// has no schema for and that is not in identity.MarkerlessTypes cannot be
// proven taggable, so it must not be claimed tag-governable - the same
// direction every other schema-less degradation in this package takes.
func TestRungForType_NoSchemaCostsPrecisionNeverAWrongClaim(t *testing.T) {
	if got := rungForType(nil, "test_unknown_to_this_run"); got != RungDeclarationCarried {
		t.Errorf("rungForType(no schema) = %q, want %q (never %q with nothing to prove it)", got, RungDeclarationCarried, RungTagGovernable)
	}
}

func TestRungForType_RecordOnlyComesFromMarkerlessTypes(t *testing.T) {
	markerless := anyMarkerlessType(t)
	// No schema at all supplied: MarkerlessTypes decides this one outright,
	// with no schema check needed - it is generated specifically for types
	// that have neither a tags argument nor a client-suppliable identity.
	if got := rungForType(nil, markerless); got != RungRecordOnly {
		t.Errorf("rungForType(%s) = %q, want %q", markerless, got, RungRecordOnly)
	}
}

func TestRungForType_EmptyTypeIsNeverGuessed(t *testing.T) {
	if got := rungForType(map[string]providers.Schema{"anything": taggableSchema()}, ""); got != "" {
		t.Errorf("rungForType(\"\") = %q, want empty - a site with no recovered type must not get a guessed rung", got)
	}
}

func mustInstance(t *testing.T, addr string) addrs.AbsResourceInstance {
	t.Helper()
	inst, diags := addrs.ParseAbsResourceInstanceStr(addr)
	if diags.HasErrors() {
		t.Fatalf("parsing %q: %s", addr, diags.Err())
	}
	return inst
}

// TestBuildRoster_ResolvedInstancesCarryTheirRung is the roster's ordinary
// case: two resolved instances of different rungs, neither refused.
func TestBuildRoster_ResolvedInstancesCarryTheirRung(t *testing.T) {
	schemas := map[string]providers.Schema{
		"test_bucket": taggableSchema(),
		"test_named":  untaggableSchema(),
	}
	identities := []identity.Resolution{
		{Addr: mustInstance(t, "test_bucket.data"), Class: identity.ClassConcrete, ImportID: "data"},
		{Addr: mustInstance(t, "test_named.attach"), Class: identity.ClassConcrete, ImportID: "attach"},
	}

	roster := buildRoster(schemas, identities, nil)
	if len(roster) != 2 {
		t.Fatalf("got %d roster entries, want 2: %+v", len(roster), roster)
	}

	// Sorted by address: "test_bucket.data" < "test_named.attach".
	bucket, named := roster[0], roster[1]
	if bucket.Address != "test_bucket.data" || bucket.Type != "test_bucket" || bucket.Rung != RungTagGovernable || bucket.Refused {
		t.Errorf("bucket entry = %+v, want tag-governable, unrefused", bucket)
	}
	if named.Address != "test_named.attach" || named.Type != "test_named" || named.Rung != RungDeclarationCarried || named.Refused {
		t.Errorf("named entry = %+v, want declaration-carried, unrefused", named)
	}
}

// TestBuildRoster_RefusedSiteCarriesRuleAndReason is #790's other half: a
// refused instance's roster entry names the rule that refused it and the
// reason - the same text the human report already prints, restated here per
// instance instead of grouped under one finding heading.
func TestBuildRoster_RefusedSiteCarriesRuleAndReason(t *testing.T) {
	f := Finding{
		Refusal: Refusal{Layer: LayerIdentity, ID: "Non-static identity argument", Title: "Non-static identity argument"},
		Sites: []Site{
			{Address: "test_named.broken", Type: "test_named", Detail: "the argument reads a value this run cannot prove statically"},
		},
	}

	roster := buildRoster(nil, nil, []Finding{f})
	if len(roster) != 1 {
		t.Fatalf("got %d roster entries, want 1: %+v", len(roster), roster)
	}
	entry := roster[0]
	if !entry.Refused {
		t.Fatalf("entry is not marked refused: %+v", entry)
	}
	if entry.Rule != "Non-static identity argument" {
		t.Errorf("Rule = %q, want the finding's own ID", entry.Rule)
	}
	if entry.Reason != "the argument reads a value this run cannot prove statically" {
		t.Errorf("Reason = %q, want the site's own Detail", entry.Reason)
	}
	if entry.Address != "test_named.broken" || entry.Type != "test_named" {
		t.Errorf("entry = %+v, want address/type carried from the site", entry)
	}
}

// TestBuildRoster_SkipsSitesWithNoAddress: a sourceless finding (projection's
// two offline refusals raise these - see
// TestSourcelessSiteRendersAsNothingRatherThanABlankLine in
// internal/command/views) has nothing to key a roster row on, and must not
// produce one rather than a row with an empty address.
func TestBuildRoster_SkipsSitesWithNoAddress(t *testing.T) {
	f := Finding{
		Refusal: Refusal{Layer: LayerProjection, ID: "Empty import identity", Title: "Empty import identity"},
		Sites:   []Site{{}},
	}
	roster := buildRoster(nil, nil, []Finding{f})
	if len(roster) != 0 {
		t.Errorf("got %d roster entries for a sourceless site, want 0: %+v", len(roster), roster)
	}
}

// TestBuildRoster_DedupesAnAddressSeenTwice guards the merge's own
// invariant structurally: a resolved instance's address is never also
// listed as refused, even if some future caller violated the assumption
// that the two sources are disjoint.
func TestBuildRoster_DedupesAnAddressSeenTwice(t *testing.T) {
	identities := []identity.Resolution{
		{Addr: mustInstance(t, "test_named.dup"), Class: identity.ClassConcrete, ImportID: "dup"},
	}
	f := Finding{
		Refusal: Refusal{Layer: LayerIdentity, ID: "some-rule", Title: "some-rule"},
		Sites:   []Site{{Address: "test_named.dup", Type: "test_named", Detail: "would-be refusal"}},
	}

	roster := buildRoster(nil, identities, []Finding{f})
	if len(roster) != 1 {
		t.Fatalf("got %d roster entries for one address seen twice, want 1: %+v", len(roster), roster)
	}
	if roster[0].Refused {
		t.Errorf("the resolved entry was overwritten by the refused one: %+v", roster[0])
	}
}
