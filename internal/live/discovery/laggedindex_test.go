// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/registry"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is issue #1318: in us-east-1 a tag index that has not caught up
// with the marker writes reaches [sweepViaTagging]'s SUPPRESSED
// registry-untaggable arm, where issue #881 ordered the loud one.
//
// The door opened with #1144. Before it, [taggingAPIUnservedTypeInRegion]
// answered true for every aws_iam_ type in every region, so the loud arm
// above always won for these types and the quiet one below was unreachable
// for them. #1144 made the term region-aware and the emulator repin (#1152,
// lex00/floci#205) made aws_iam_policy and aws_iam_instance_profile SERVED
// in us-east-1, which is what real AWS does (#1134: 500 of each returned in
// us-east-1, 0 in us-east-2). The loud arm now correctly declines there, and
// an index answer holding none of them lands in an arm whose whole content
// is "this type cannot carry a tag".
//
// It can. The provider gives both types a tags argument, internal/live/stamp
// writes the estate's marker onto them, and #1046 measured the Resource
// Groups Tagging API holding 104 of 1,655 stamped objects about 21 minutes
// after migrate had verified every one of them on a real account. So an
// empty index answer for one of these types in the one region that indexes
// it is not evidence the estate owns none.
//
// The two facts the sweep must not conflate, and the one input that tells
// them apart:
//
//   - "this type cannot carry a tag": the provider's own resource schema has
//     no tags argument ([typeTaggable] false). Nothing was ever there for
//     the index to hold, the gap is a standing fact about the type, and
//     [sweepGapDiag] suppressing it is right.
//   - "this type carries a tag and the index did not return it": the schema
//     HAS the argument. Something may well have been there.
//
// [typeTaggable] is the whole distinction, read from the provider rather
// than from live/registry.json - whose taggable flag is CloudFormation's
// claim about its own update-tags API and is false for AWS::IAM::Policy and
// AWS::IAM::InstanceProfile while every stamped object of both says
// otherwise.
//
// What this file deliberately does NOT do is what #1318's author tried and
// reverted: adding [typeTaggable] to the arm's own condition, which drops
// the type through to an ordinary covered scan with Listed:0. That claims
// the sweep established the estate owns none of the type and takes it out of
// [Result.SweepGaps] entirely. A recorded, suppressed gap under-claims; a
// covered scan over-claims, and over-claiming is what this project's safety
// rule forbids. The third arm here keeps the entry and corrects what it says.

// laggedIndexTypes are the three shapes the registry-untaggable arm has to
// tell apart, and the fixture runs all three through ONE Discover call so
// that no arm can pass because its run differed.
const (
	// laggedType is #1318's own case: registry taggable:false, provider
	// schema taggable:true, index coverage measured as region-restricted
	// ([taggingAPITypeCoverage]), and us-east-1 is the region that serves
	// it. An empty answer here is the lag.
	laggedType    = "aws_iam_policy"
	laggedCFNType = "AWS::IAM::Policy"

	// ordinaryType has the identical registry/schema disagreement and NO
	// recorded index-coverage row, so the index holds it the ordinary way
	// in every region. An empty answer for it is the same evidence the
	// whole tagging leg rests on, and this fixture pins that it stays
	// suppressed, recorded and uncovered - #1322 changed only the sentence
	// such a gap carries.
	ordinaryType    = "aws_instance"
	ordinaryCFNType = "AWS::EC2::Instance"

	// untaggableType is the second half of the decisive arm: a type whose
	// provider schema carries no tags argument at all. It must still get
	// the quiet verdict, and a fix that made everything loud would fail
	// here.
	untaggableType    = "aws_nat_gateway"
	untaggableCFNType = "AWS::EC2::NatGateway"
)

// TestSweepSpeaksWhenAServedTagIndexHeldNothingForATaggableType is the
// decisive arm, and it is decisive in both halves: the taggable,
// indexed-in-this-region type must produce the LOUD verdict and the
// genuinely untaggable one must still produce the quiet one, from the same
// run, the same index answer and the same registry rows.
//
// The lag itself is INJECTED rather than reproduced, and that is worth
// saying plainly: floci writes its tag index synchronously, so no emulator
// run can produce an object that exists and is marked while GetResources
// does not yet hold it. Here the fake account holds a live, marked,
// undeclared aws_iam_policy and the fake index answers the estate's
// GetResources call with an empty list - which is exactly the wire shape
// #1046 measured on a real account, where the index held 104 of 1,655
// objects it had all been told about.
func TestSweepSpeaksWhenAServedTagIndexHeldNothingForATaggableType(t *testing.T) {
	const deletedAddr = laggedType + ".gone"

	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable(laggedType)
	cloud.listable(ordinaryType)
	cloud.listableUntagged(untaggableType)

	// The live object the index has not caught up with. Nothing in this run
	// enumerates it - in us-east-1 the tagging leg is where aws_iam_policy
	// is routed, and that leg's one call is the index - so its destroy is
	// the thing #881 ruled must never be lost in silence.
	cloud.own(laggedType, "arn:aws:iam::000000000000:policy/gone", deletedAddr)

	// The premises, stated rather than assumed. If any of them stops
	// holding, this fixture is exercising something else.
	if taggingAPIUnservedTypeInRegion(laggedRegion, laggedType) {
		t.Fatalf("%s is recorded as unserved from %s, so sweepViaTagging's LOUD unserved arm takes this run and the "+
			"quiet arm #1318 is about is never reached", laggedType, laggedRegion)
	}
	if !taggingAPIRestrictedType(laggedType) {
		t.Fatalf("%s has no taggingAPITypeCoverage row any more, so the region-restricted index coverage this "+
			"fixture rests on is gone", laggedType)
	}
	if taggingAPIRestrictedType(ordinaryType) {
		t.Fatalf("%s has acquired a taggingAPITypeCoverage row, so it is no longer the ordinary-coverage control "+
			"this fixture needs", ordinaryType)
	}

	// The index answers, and holds none of this estate's policies. An empty
	// ResourceTagMappingList IS the lag's shape on the wire - the call
	// succeeds and returns nothing - which is why srv.calls is asserted
	// below: a gap filed because the call never happened would be
	// SweepGapListFailed's case, not this one.
	srv := &taggingServer{}
	server := srv.start(t)
	defer server.Close()

	req := Request{
		Sweep:        true,
		TaggingSweep: true,
		Region:       laggedRegion,
		SweepTypes:   []string{laggedType, ordinaryType, untaggableType},
		Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: server.URL}),
		// The real registry rows, copied rather than corrected: all three
		// CFN types carry tagging.taggable false. That is what puts all
		// three in the one arm, which is what makes the comparison mean
		// something.
		Roster: ccRoster(t,
			map[string]string{
				laggedType:     laggedCFNType,
				ordinaryType:   ordinaryCFNType,
				untaggableType: untaggableCFNType,
			},
			nil,
			map[string]bool{laggedCFNType: false, ordinaryCFNType: false, untaggableCFNType: false},
		),
	}
	res, diags := discoverFixture(t, cloud, req)
	assertNoErrors(t, diags)

	if srv.calls != 1 {
		t.Fatalf("GetResources was called %d times, want exactly 1 - the index has to have ANSWERED for an empty "+
			"answer to be the thing under test rather than a call that never happened", srv.calls)
	}

	gaps := gapsByType(res)

	// Half one: the taggable, indexed-here type. The verdict has to be the
	// new one AND it has to be spoken; SweepGapNotTaggable would reach
	// res.SweepGaps too and never reach the operator, so an assertion that
	// accepted any gap would pass with this fix reverted.
	if got := gaps[laggedType]; got != SweepGapTagIndexHeldNothing {
		t.Errorf("the sweep gap for %s is %q, want %q.\n"+
			"In %s the index holds this type, it answered, and it held none of this estate's - which is either "+
			"\"the estate owns none\" or a lag the account has already been measured taking twenty minutes to "+
			"clear. %q says the type can carry no ownership marker, which the provider's own schema and every "+
			"stamped object of it contradict.\n%s",
			laggedType, got, SweepGapTagIndexHeldNothing, laggedRegion, SweepGapNotTaggable, res)
	}
	if !spokenAbout(diags, laggedType) {
		t.Errorf("nothing reached the operator about %s: %s\n"+
			"A suppressed gap is what #1318 is about - the run looked, established nothing, and said nothing.",
			laggedType, renderDiags(diags))
	}

	// Half two, and a fix that only did half one would fail here: a type
	// that genuinely cannot carry a tag keeps the quiet verdict.
	if got := gaps[untaggableType]; got != SweepGapNotTaggable {
		t.Errorf("the sweep gap for %s is %q, want %q.\n"+
			"Its provider schema has no tags argument at all, so nothing was ever there for the index to hold. "+
			"Reporting that as an index that may not have caught up replaces one conflation with another.",
			untaggableType, got, SweepGapNotTaggable)
	}
	if spokenAbout(diags, untaggableType) {
		t.Errorf("a diagnostic was raised about %s: %s\n"+
			"Whether a provider version's type carries tags is a standing fact, true of every run against it; "+
			"warning about it once per run per type is what buries the gap that means something.",
			untaggableType, renderDiags(diags))
	}

	// The scoping, pinned deliberately rather than left to be discovered.
	// #1318 left the same registry/schema disagreement on an
	// ORDINARY-coverage type on the quiet arm, and that half is unchanged:
	// its empty answer is the evidence the whole tagging leg rests on, and
	// making it loud would put a warning an operator can do nothing about
	// on every plan in every region.
	//
	// Issue #1322 moved what that quiet gap SAYS, and only that. It used to
	// be [SweepGapNotTaggable] - the sentence "so it can carry no ownership
	// marker", about a type the provider gives a tags argument and this
	// fork stamps a marker onto, on the strength of a live/registry.json
	// flag that is CloudFormation's claim about its own update-tags API.
	// #1322 ruled that claim is not an answer to that question and cannot
	// be made into one where it is generated, so the reader stopped asking
	// it: [noRegistryRowOrUntaggable] reads [typeTaggable] for the sentence
	// instead.
	//
	// Nothing else about this type moved, and the three assertions below
	// are what says so rather than this comment: still suppressed, still a
	// recorded gap, still not covered.
	if got := gaps[ordinaryType]; got != SweepGapTagIndexCoverageUnconfirmed {
		t.Errorf("the sweep gap for %s is %q, want %q.\n"+
			"%q here would be the pre-#1322 sentence back - a type that plainly carries this estate's marker told "+
			"it can carry none. A LOUD reason here would be the other error: widening #1318's per-run diagnostic "+
			"to every schema-taggable type is a deliberate decision with its own cost, and it is not this one.",
			ordinaryType, got, SweepGapTagIndexCoverageUnconfirmed, SweepGapNotTaggable)
	}
	if spokenAbout(diags, ordinaryType) {
		t.Errorf("a diagnostic was raised about %s: %s\n"+
			"#1322 corrected a suppressed gap's wording. If correcting it also made it loud, the cost #1318 "+
			"measured for two types in one region is now paid for every schema-taggable type in every region.",
			ordinaryType, renderDiags(diags))
	}

	// None of the three may be recorded as covered: the arm continues
	// before the TypeScan is filed, and that is the half the reverted fix
	// lost.
	for _, typeName := range []string{laggedType, ordinaryType, untaggableType} {
		for _, c := range res.SweepCovered {
			if c == typeName {
				t.Errorf("%s is in Result.SweepCovered, so the run claims it searched for resources this estate "+
					"owns but no longer declares and found none. It filed a gap instead; both cannot be true.", typeName)
			}
		}
	}

	// And the destroy really is lost, which is why the gap has to speak.
	if _, ok := removalsByAddr(res)[deletedAddr]; ok {
		t.Fatalf("%s was proposed for removal, so this fixture is no longer the lagged-index shape - some leg "+
			"enumerated the object and the gap under test would not fire on a real run either:\n%s", deletedAddr, res)
	}
}

// TestSweepTagIndexVerdictTurnsOnTheProviderSchemaAlone holds everything
// fixed - the type, the registry row, the region, the index answer - and
// flips the single input that answers "can this type carry a tag". The
// verdict has to flip with it, or the distinction the fix claims to draw is
// being drawn by something else.
func TestSweepTagIndexVerdictTurnsOnTheProviderSchemaAlone(t *testing.T) {
	run := func(t *testing.T, taggable bool) SweepGapReason {
		t.Helper()
		cloud := newFakeCloud()
		ownWholeEstate(cloud)
		if taggable {
			cloud.listable(laggedType)
		} else {
			cloud.listableUntagged(laggedType)
		}

		srv := &taggingServer{}
		server := srv.start(t)
		defer server.Close()

		req := Request{
			Sweep:        true,
			TaggingSweep: true,
			Region:       laggedRegion,
			SweepTypes:   []string{laggedType},
			Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: server.URL}),
			Roster:       taggingRoster(t, laggedType, laggedCFNType, false),
		}
		res, diags := discoverFixture(t, cloud, req)
		assertNoErrors(t, diags)
		return gapsByType(res)[laggedType]
	}

	withTags := run(t, true)
	withoutTags := run(t, false)

	if withTags != SweepGapTagIndexHeldNothing {
		t.Errorf("with a tags argument in the provider schema the gap is %q, want %q", withTags, SweepGapTagIndexHeldNothing)
	}
	if withoutTags != SweepGapNotTaggable {
		t.Errorf("with no tags argument in the provider schema the gap is %q, want %q", withoutTags, SweepGapNotTaggable)
	}
	if withTags == withoutTags {
		t.Fatalf("both runs filed %q, so the provider schema is not what decides this verdict and the fix is "+
			"keyed on something else", withTags)
	}
}

// laggedRegion is the one caller region whose Resource Groups Tagging API
// index holds IAM's global objects (#1134).
const laggedRegion = "us-east-1"

// gapsByType is the last gap filed per type, which is how the other tests in
// this package read res.SweepGaps.
func gapsByType(res *Result) map[string]SweepGapReason {
	out := make(map[string]SweepGapReason, len(res.SweepGaps))
	for _, g := range res.SweepGaps {
		out[g.TypeName] = g.Reason
	}
	return out
}

// spokenAbout reports whether an incomplete-sweep diagnostic naming typeName
// reached the caller. res.SweepGaps cannot answer this: the suppressed
// reasons land there too.
func spokenAbout(diags tfdiags.Diagnostics, typeName string) bool {
	for _, d := range diags {
		desc := d.Description()
		if desc.Summary == SummaryIncompleteSweep && strings.Contains(desc.Detail, typeName) {
			return true
		}
	}
	return false
}

// TestRegistryUntaggableArmPopulation is the external check on #1318's
// scoping, and it consults the three committed artifacts rather than this
// package's own reasoning: live/mapping.json and live/registry.json for
// which admitted types reach [sweepViaTagging]'s registry-untaggable arm at
// all, and live/survey-full.json's signals.taggable column - the same
// [markers.Taggable] answer [typeTaggable] reads off the live provider - for
// whether each of them can carry a marker.
//
// It exists because the scoping is a claim about a POPULATION, and the
// population is generated. Three facts hold today and the fix's narrowness
// is only defensible while they do:
//
//  1. FIVE admitted types reach that arm with a taggable provider schema.
//     (#1318 and #1322 both say six in prose; the lists below have always
//     been 2 + 3, and recomputing them is what settled it. aws_iam_role is
//     the third type with a measured index-coverage row, but AWS::IAM::Role
//     is registry-TAGGABLE, so it never reaches this arm at all - "the IAM
//     three" was counting a coverage row, not an arm occupant.)
//  2. Two of them have a measured index-coverage row, so they are the ones
//     #1318's third verdict reaches; the other three have ordinary coverage
//     and keep a suppressed gap.
//  3. NOT ONE admitted type reaches that arm with an UNtaggable provider
//     schema. So the arm's pre-#1322 wording - "records X as untaggable, so
//     it can carry no ownership marker" - was false about every admitted
//     type that could reach it. Issue #1322 settled where that is repaired:
//     not in live/registry.json's generator, whose only input is the
//     CloudFormation bundle and whose flag is a correct answer to
//     CloudFormation's question, but in the reader - the three now carry
//     [SweepGapTagIndexCoverageUnconfirmed], still suppressed.
//
// A regenerated artifact that moves any of these three means the scoping has
// to be re-derived, which is why this fails with the recomputed lists rather
// than with a count.
func TestRegistryUntaggableArmPopulation(t *testing.T) {
	root := flocitest.RepoRoot(t)
	roster, err := registry.Load(
		filepath.Join(root, "live", "mapping.json"),
		filepath.Join(root, "live", "registry.json"),
	)
	if err != nil {
		t.Fatalf("loading the real live/mapping.json and live/registry.json: %v", err)
	}

	var survey struct {
		Types []struct {
			Type    string `json:"type"`
			Signals struct {
				Taggable bool `json:"taggable"`
			} `json:"signals"`
		} `json:"types"`
	}
	raw, err := os.ReadFile(filepath.Join(root, "live", "survey-full.json"))
	if err != nil {
		t.Fatalf("reading live/survey-full.json: %v", err)
	}
	if err := json.Unmarshal(raw, &survey); err != nil {
		t.Fatalf("parsing live/survey-full.json: %v", err)
	}
	schemaTaggable := make(map[string]bool, len(survey.Types))
	for _, r := range survey.Types {
		schemaTaggable[r.Type] = r.Signals.Taggable
	}

	var loud, quietTaggable, quietUntaggable []string
	for _, typeName := range identity.AdmittedTypes() {
		cfnType, mapped := roster.CloudControlTypeOrService(typeName)
		if !mapped || !arnJoinCovers(cfnType) {
			continue // never routed to the tagging leg at all
		}
		if typeNeedsResourceObjectToRecompose(typeName) {
			continue // routed to the native per-type leg by partitionSweepTypes
		}
		if roster.Taggable(cfnType) {
			continue // never reaches the registry-untaggable arm
		}
		switch {
		case !schemaTaggable[typeName]:
			quietUntaggable = append(quietUntaggable, typeName)
		case taggingAPIRestrictedType(typeName):
			loud = append(loud, typeName)
		default:
			quietTaggable = append(quietTaggable, typeName)
		}
	}
	sort.Strings(loud)
	sort.Strings(quietTaggable)
	sort.Strings(quietUntaggable)

	wantLoud := []string{"aws_iam_instance_profile", "aws_iam_policy"}
	wantQuietTaggable := []string{
		"aws_launch_template",
		"aws_vpc_security_group_egress_rule",
		"aws_vpc_security_group_ingress_rule",
	}

	if strings.Join(loud, " ") != strings.Join(wantLoud, " ") {
		t.Errorf("the types #1318's third verdict can reach are %v, want %v.\n"+
			"These are the types a lagged index can silence: admitted, joined from an ARN, registry-untaggable, "+
			"provider-taggable, and with a MEASURED index-coverage row. A change here means the coverage table or "+
			"live/registry.json moved and the scoping in tagIndexHeldNothingGap has to be re-derived.", loud, wantLoud)
	}
	if strings.Join(quietTaggable, " ") != strings.Join(wantQuietTaggable, " ") {
		t.Errorf("the types left on the suppressed arm with a TAGGABLE provider schema are %v, want %v.\n"+
			"They stay suppressed because their index coverage is ordinary, so an empty answer for them is the "+
			"evidence the whole tagging leg rests on. Since #1322 what they are TOLD is "+
			"SweepGapTagIndexCoverageUnconfirmed rather than a claim that they can carry no marker; "+
			"TestNoSweepGapClaimsUntaggableAgainstTheProviderSchema is the guard on that half.",
			quietTaggable, wantQuietTaggable)
	}
	if len(quietUntaggable) != 0 {
		t.Errorf("admitted types reach the registry-untaggable arm with an untaggable provider schema: %v.\n"+
			"That is new - the arm had no truthful occupant at all when #1318 landed - and it means "+
			"SweepGapNotTaggable now says something true about a real type. Worth knowing; nothing here is wrong.",
			quietUntaggable)
	}
}
