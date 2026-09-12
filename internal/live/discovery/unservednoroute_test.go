// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"testing"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
)

// TestSweepFindsUnservedTypeWhenOnlyCloudControlCanEnumerateIt is issue
// #881 reopened, and it is the same routing question its first fix asked
// with one term corrected.
//
// [arnJoinReaches] routes a [taggingAPIUnservedType] BACK to the tagging
// leg whenever the native leg has no route, and it asks that question as
// [listclient.Schemas.Supports] alone - the provider's own list-resource
// surface. But [scanType] falls through to [scanTypeCloudControl] for
// exactly the types Supports answers false for (discovery.go's
// cloudControlSource branch), so Supports is not the whole question: a type
// the provider cannot list and Cloud Control CAN is a type the native leg
// reaches perfectly well, and routing it to the tagging leg instead is what
// this test is about.
//
// The fixture is the terralith's stage-J shape with the emulator's
// 2026-09-11 behaviour (lex00/floci#202, live/flociimage_test.go): the
// Resource Groups Tagging API serves NO IAM ARNs, exactly as real AWS does
// not. Cloud Control lists the instance profile. Before the fix both legs
// came back with nothing - the tagging leg because it was told to look
// somewhere that never answers for IAM, the native leg because it was never
// asked - and a live, marked, taggable object whose block was deleted was
// silently omitted from the plan.
//
// The existing TestSweepFindsAnUnservedServiceTypeTheProviderCannotList
// passes today only because ITS taggingServer serves the IAM ARN. That is
// the same defect in fixture form: no real AWS account and no floci since
// the repin answers GetResources for IAM at all.
func TestSweepFindsUnservedTypeWhenOnlyCloudControlCanEnumerateIt(t *testing.T) {
	const (
		unservedType = "aws_iam_instance_profile"
		cfnType      = "AWS::IAM::InstanceProfile"
		liveName     = "estate-team-profile"
		deletedAddr  = unservedType + ".removed"
	)

	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	// The real provider's shape at 6.59.0: a managed resource schema for
	// the type, WITH a tags argument, and no list resource for it. listable
	// puts the type in GetProviderSchema's ResourceTypes; unlistable then
	// withholds the list route alone.
	cloud.listable(unservedType)
	cloud.unlistable(unservedType)

	// The premises, stated rather than assumed.
	if !taggingAPIUnservedType(unservedType) {
		t.Fatalf("%s is no longer in a service taggingAPIUnservedServices names, so this fixture no longer exercises #692's routing", unservedType)
	}

	// GetResources serves no IAM, which is what real AWS does and what
	// floci has done since the 2026-09-11 repin.
	tagSrv := &taggingServer{}
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	cc := newCCServer(t)
	cc.listResources[cfnType] = []ccResource{{
		identifier: liveName,
		properties: tagsProps(estateName, deletedAddr),
	}}
	ccServer := cc.start()
	defer ccServer.Close()

	req := Request{
		Sweep:        true,
		TaggingSweep: true,
		Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: tagServer.URL}),
		CloudControl: cloudcontrol.New(cloudcontrol.Config{Endpoint: ccServer.URL}),
		// live/registry.json's ACTUAL row for AWS::IAM::InstanceProfile,
		// copied rather than corrected: handlers.list TRUE, tagging.taggable
		// FALSE. The second value is CloudFormation's claim about whether
		// ITS update-tags API can write the type's tags; it is not a fact
		// about whether the object carries one. The provider gives
		// aws_iam_instance_profile a tags argument, internal/live/stamp
		// writes the marker onto it, and the terralith's stage J0 confirms
		// the live profile carries this estate's marker.
		//
		// Trusting that flag is #881's second half: [scanTypeCloudControl]
		// returned on it before calling ListResources at all, filing a
		// SweepGapNotTaggable that [sweepGapDiag] suppresses. The fixture
		// must reproduce the real row, or the test proves nothing about the
		// real estate.
		Roster: ccRoster(t,
			map[string]string{unservedType: cfnType},
			map[string]bool{cfnType: true},
			map[string]bool{cfnType: false},
		),
	}
	res, diags := discoverFixture(t, cloud, req)
	assertNoErrors(t, diags)

	removals := removalsByAddr(res)
	got, ok := removals[deletedAddr]
	if !ok {
		t.Fatalf("the live %s carrying this estate's marker for %s was not proposed for removal - the silently-omitted destroy #881 is about.\nCloud Control calls: %v\nGetResources calls: %d\n%s",
			unservedType, deletedAddr, cc.calls, tagSrv.calls, res)
	}
	if got.ImportID != liveName {
		t.Errorf("the removal's import identity is %q, want %q - a destroy is planned at the identity", got.ImportID, liveName)
	}
	if got.TypeName != unservedType {
		t.Errorf("the removal's type is %q, want %q", got.TypeName, unservedType)
	}
}

// TestSweepReportsAGapWhenNeitherLegCanEnumerateAnUnservedType is the
// backstop for the case the fix above cannot repair: no Cloud Control
// client configured (TOFU_LIVE_CLOUDCONTROL=off), no provider list
// resource, and a service GetResources does not index. Nothing in the run
// can enumerate the type.
//
// [arnJoinReaches]'s own doc comment promises that falling back to the
// tagging leg "cannot cost a wrong marker: on an account where the API
// really does not serve the service the candidate list is empty and
// sweepViaTagging reports its own gap, loudly". It does not. A TAGGABLE
// type with zero joined candidates falls straight through
// [sweepViaTagging]'s switch to a TypeScan with Listed:0 and no gap at all
// - only an UNTAGGABLE one reports [SweepGapNotTaggable]. So the sweep
// records the type as covered while having looked nowhere it could be
// found, and a deleted block's live object is omitted in silence.
func TestSweepReportsAGapWhenNeitherLegCanEnumerateAnUnservedType(t *testing.T) {
	const (
		unservedType = "aws_iam_instance_profile"
		cfnType      = "AWS::IAM::InstanceProfile"
	)

	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable(unservedType)
	cloud.unlistable(unservedType)

	tagSrv := &taggingServer{}
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	req := Request{
		Sweep:        true,
		TaggingSweep: true,
		Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: tagServer.URL}),
		// No CloudControl: the type has no enumeration route at all.
		// The real registry row again: taggable FALSE. Without the
		// [typeTaggable] tiebreaker this lands in the suppressed
		// NotTaggable case and the operator is told nothing at all.
		Roster: taggingRoster(t, unservedType, cfnType, false),
	}
	res, diags := discoverFixture(t, cloud, req)
	assertNoErrors(t, diags)

	// The REASON matters, not merely that some gap exists: the registry's
	// taggable:false would already produce a [SweepGapNotTaggable] here,
	// and [sweepGapDiag] suppresses that one - it reaches res.SweepGaps and
	// never reaches the operator. An assertion that accepted any gap would
	// pass with this fix reverted (it did, before this was tightened).
	var reason SweepGapReason
	for _, g := range res.SweepGaps {
		if g.TypeName == unservedType {
			reason = g.Reason
		}
	}
	if reason != SweepGapNoEnumerationRoute {
		var covered bool
		for _, c := range res.SweepCovered {
			if c == unservedType {
				covered = true
			}
		}
		t.Fatalf("the sweep gap for %s is %q, want %q (reported as covered=%v). A sweep that looked nowhere must say so: an operator reading this run has no way to know a deleted block's live object went unlooked-for.\n%s",
			unservedType, reason, SweepGapNoEnumerationRoute, covered, res)
	}

	// And that it was actually SPOKEN. This is the half res.SweepGaps
	// cannot show: SweepGapNotTaggable lands in that slice too and is
	// suppressed on the way to the operator.
	var spoken bool
	for _, d := range diags {
		if d.Description().Summary == SummaryIncompleteSweep {
			spoken = true
		}
	}
	if !spoken {
		t.Fatalf("the gap for %s produced no diagnostic at all, so nothing reaches the operator; diagnostics: %s", unservedType, renderDiags(diags))
	}
}
