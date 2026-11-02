// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/pkg/azsweep"
)

// Follow-ups to GitHub issue #2136 (its 2026-10-20 comment, items 2 and 3):
// an Azure sighting is always compared by ARM id and never reaches the
// "nothing to compare" vouch, and the displaced warning says where the
// object actually sits.

// declaredOf is a [declared] holding resolutions and nothing else, enough
// for [declared.displacedFrom] and [declared.declaredAt].
func declaredOf(resolutions ...identity.Resolution) *declared {
	d := &declared{all: map[string]map[string]*declaredAddress{}}
	for _, r := range resolutions {
		d.record(r.Addr.Resource.Resource.Type, markers.EscapeAddress(r.Addr.String()), r)
	}
	return d
}

func instanceAddr(typ, name string, key addrs.InstanceKey) addrs.AbsResourceInstance {
	return addrs.Resource{Mode: addrs.ManagedResourceMode, Type: typ, Name: name}.Instance(key).Absolute(addrs.RootModuleInstance)
}

// TestDisplacedFrom_azureSightingAlwaysCompares: a sighting from the
// Resource Graph leg carries an ARM id and no identity object, and the
// declared address carries no identity values. The verdict is the ARM id
// comparison in both directions (own id in another case vouches, another
// id is displaced), and where the address has no ARM id to compare with
// (a parent-derived formula, or a concrete resolution whose import id is
// not an ARM id) the answer is verdictUncompared, never the vouching
// default. AWS's no-comparison case answers the same since #2137.
func TestDisplacedFrom_azureSightingAlwaysCompares(t *testing.T) {
	addr := instanceAddr("azurerm_route_table", "rt", addrs.NoKey)
	escaped := markers.EscapeAddress(addr.String())
	own := renameRTID(renameNewRT)
	listed := func(id string) claimant {
		return claimant{importID: id, identityAttr: "id", identity: cty.NilVal, marker: escaped, escaped: escaped}
	}
	for _, tc := range []struct {
		name string
		res  identity.Resolution
		live string
		want addressVerdict
	}{
		{"own id, lowercased", identity.Resolution{Addr: addr, Class: identity.ClassConcrete, ImportID: own}, strings.ToLower(own), verdictOwnObject},
		{"another id", identity.Resolution{Addr: addr, Class: identity.ClassConcrete, ImportID: own}, strings.ToLower(renameRTID(renameOldRT)), verdictDisplaced},
		{"parent-derived address", identity.Resolution{Addr: addr, Class: identity.ClassParentDerived}, strings.ToLower(own), verdictUncompared},
		{"concrete with a non-ARM import id", identity.Resolution{Addr: addr, Class: identity.ClassConcrete, ImportID: "rt-new"}, strings.ToLower(own), verdictUncompared},
		{"concrete with no import id", identity.Resolution{Addr: addr, Class: identity.ClassConcrete}, strings.ToLower(own), verdictUncompared},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, got := declaredOf(tc.res).displacedFrom(context.Background(), "azurerm_route_table", escaped, listed(tc.live))
			if got != tc.want {
				t.Fatalf("verdict %d, want %d", got, tc.want)
			}
		})
	}

	// The AWS control: a listed object with no ARM id against a class with
	// nothing to compare is unproven too since #2137, and does not vouch.
	bucket := instanceAddr("aws_s3_bucket", "b", addrs.NoKey)
	be := markers.EscapeAddress(bucket.String())
	_, got := declaredOf(identity.Resolution{Addr: bucket, Class: identity.ClassParentDerived}).displacedFrom(context.Background(), "aws_s3_bucket", be, claimant{importID: "b-old", escaped: be})
	if got != verdictUncompared {
		t.Errorf("AWS no-comparison verdict %d, want verdictUncompared (%d) since #2137", got, verdictUncompared)
	}
}

// TestResourceGraphLeg_uncomparedSightingDoesNotVouch is the same through
// the sweep: an azurerm_route_table address whose resolution carries no
// ARM id, and a Resource Graph sighting of an object carrying its marker.
// Before this, displacedFrom's class gate answered verdictOwnObject and the
// sighting vouched for the address, so the projection would have admitted
// whatever it read for it unread.
func TestResourceGraphLeg_uncomparedSightingDoesNotVouch(t *testing.T) {
	hit := renameOldHit()
	graph := &fakeGraph{answers: map[string][]azsweep.Resource{rgSub1: {hit}}}
	leg := ResourceGraphSweep{Coverage: azsweep.Coverage{rgSub1: graph}, Subscriptions: []string{rgSub1}}
	rt := identity.Resolution{Addr: instanceAddr("azurerm_route_table", "rt", addrs.NoKey), Class: identity.ClassParentDerived}

	res, diags := discoverAzureWith(t, azConfigWith(t, renameRTConfig), leg, keptResolution(rgSub1), rt)
	assertNoErrors(t, diags)
	if res.MarkerVerified()[renameRTAdr] {
		t.Errorf("a sighting with no ARM id to compare against vouched for %s: %v", renameRTAdr, res.VerifiedDeclared)
	}
	if ps := res.ProblemsOfKind(ProblemDisplacedMarker); len(ps) != 0 {
		t.Errorf("an uncompared sighting was reported displaced: %+v", ps)
	}
	assertNotAnOrphan(t, res, hit.ID)
}

// TestDisplacedProblem_saysWhereTheObjectSits pins both wordings of the
// displaced warning, on AWS and Azure alike since the text is shared.
//
//   - At an identity nothing declares (a plain rename): the object is bound
//     to no address and not read. True, and kept.
//   - At the identity another declared instance computes (a renumbered
//     count list's leftover: the object marked r[2] now sits where r[1]
//     points): it IS read, there, by r[1]. The old text said "not read",
//     which was false; the new one names r[1].
func TestDisplacedProblem_saysWhereTheObjectSits(t *testing.T) {
	const notRead = "it is bound to no address, it is not read"

	type platform struct {
		typ          string
		r1ID, r2ID   string
		nobodysID    string
		otherTypesID string
	}
	for name, p := range map[string]platform{
		"aws": {
			typ:  "aws_s3_bucket",
			r1ID: "bucket-1", r2ID: "bucket-2",
			nobodysID: "bucket-old",
			// An id of another type spelling the same string must not count:
			// a non-ARM id is matched within its own type only.
			otherTypesID: "bucket-old",
		},
		"azure": {
			typ:          "azurerm_route_table",
			r1ID:         renameRTID("rt-1"),
			r2ID:         renameRTID("rt-2"),
			nobodysID:    strings.ToLower(renameRTID("rt-old")),
			otherTypesID: "",
		},
	} {
		t.Run(name, func(t *testing.T) {
			r1 := instanceAddr(p.typ, "r", addrs.IntKey(1))
			r2 := instanceAddr(p.typ, "r", addrs.IntKey(2))
			resolutions := []identity.Resolution{
				{Addr: r1, Class: identity.ClassConcrete, ImportID: p.r1ID},
				{Addr: r2, Class: identity.ClassConcrete, ImportID: p.r2ID},
			}
			if p.otherTypesID != "" {
				resolutions = append(resolutions, identity.Resolution{Addr: instanceAddr("aws_iam_role", "x", addrs.NoKey), Class: identity.ClassConcrete, ImportID: p.otherTypesID})
			}
			decl := declaredOf(resolutions...)
			escaped := markers.EscapeAddress(r2.String())
			req := Request{Estate: "e"}

			// r2's marker on the object at r1's identity, listed in another
			// case on Azure.
			at := p.r1ID
			if strings.HasPrefix(at, "/") {
				at = strings.ToLower(at)
			}
			got := displacedProblem(req, decl, p.typ, escaped, p.r2ID, claimant{importID: at}).Detail
			if strings.Contains(got, notRead) {
				t.Errorf("an object at %s's identity is said not to be read:\n%s", r1, got)
			}
			for _, want := range []string{
				"It sits at the identity this configuration computes for " + r1.String(),
				"read there as " + r1.String() + "'s object and judged by its own markers",
				"read the plan for " + r1.String() + " for what is proposed for it",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("the warning does not say %q:\n%s", want, got)
				}
			}

			// r2's marker on an object nothing declares.
			got = displacedProblem(req, decl, p.typ, escaped, p.r2ID, claimant{importID: p.nobodysID}).Detail
			if !strings.Contains(got, notRead) {
				t.Errorf("an object at an undeclared identity lost the true wording:\n%s", got)
			}
			if strings.Contains(got, "It sits at the identity") {
				t.Errorf("an object at an undeclared identity is said to sit at a declared one:\n%s", got)
			}
		})
	}
}

// TestDisplacedFrom_azureSightingWithNoSingleEntry is GitHub issue #2140,
// finding 5: with no declared entry for the marker, or an ambiguous one,
// an ARM-id sighting is still the ARM comparison's question and answers
// verdictUncompared, never the vouching default the nil and ambiguous
// checks gave every platform before #2137. The AWS control now answers the
// same.
func TestDisplacedFrom_azureSightingWithNoSingleEntry(t *testing.T) {
	addr := instanceAddr("azurerm_route_table", "rt", addrs.NoKey)
	escaped := markers.EscapeAddress(addr.String())
	armSighting := claimant{importID: strings.ToLower(renameRTID(renameNewRT)), identityAttr: "id", identity: cty.NilVal, marker: escaped, escaped: escaped}

	ambiguous := declaredOf()
	ambiguous.record("azurerm_route_table", escaped, identity.Resolution{Addr: addr, Class: identity.ClassConcrete, ImportID: renameRTID(renameNewRT)})
	ambiguous.record("azurerm_route_table", escaped, identity.Resolution{Addr: instanceAddr("azurerm_route_table", "rt2", addrs.NoKey), Class: identity.ClassConcrete, ImportID: renameRTID(renameOldRT)})
	if !ambiguous.all["azurerm_route_table"][escaped].ambiguous {
		t.Fatal("setup: the entry is not ambiguous")
	}

	for name, d := range map[string]*declared{"no entry": declaredOf(), "ambiguous entry": ambiguous} {
		if _, got := d.displacedFrom(context.Background(), "azurerm_route_table", escaped, armSighting); got != verdictUncompared {
			t.Errorf("%s: ARM sighting verdict %d, want verdictUncompared (%d)", name, got, verdictUncompared)
		}
	}

	bucket := instanceAddr("aws_s3_bucket", "b", addrs.NoKey)
	be := markers.EscapeAddress(bucket.String())
	if _, got := declaredOf().displacedFrom(context.Background(), "aws_s3_bucket", be, claimant{importID: "b-old", escaped: be}); got != verdictUncompared {
		t.Errorf("AWS no-entry verdict %d, want verdictUncompared (%d) since #2137", got, verdictUncompared)
	}
}
