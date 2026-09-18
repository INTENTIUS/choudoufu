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
)

// This file is issue #1322, and it is the ruling as much as the test:
// live/registry.json's tagging.taggable flag is CloudFormation's claim about
// whether ITS OWN update-tags API writes a type's tags, and the sweep must
// not read it as the answer to "can this live object carry an ownership
// marker". Those are different questions and for every admitted type that
// reaches [sweepViaTagging]'s registry-untaggable arm today they give
// different answers.
//
// Why the flag cannot be repaired where it is generated, which is the
// alternative #1322 asks to be weighed. tools/registry-gen's only input is
// the CloudFormation Registry schema bundle, and the artifact is pinned by a
// digest over exactly those schemas (tools/registry-gen/pin.go). Read out of
// that bundle at the pinned spec:
//
//	AWS::EC2::LaunchTemplate       "tagging": {"taggable": false, ...}
//	AWS::EC2::SecurityGroupEgress  "tagging": {"taggable": false, ...}
//	AWS::EC2::SecurityGroupIngress "tagging": {"taggable": false, ...}
//	AWS::IAM::InstanceProfile      "tagging": {"taggable": false, ...}
//	AWS::IAM::Policy               no "tagging" key at all
//
// Four of the five say taggable:false explicitly, and they are RIGHT: none of
// the four carries a Tags property in its CloudFormation schema, so
// CloudFormation genuinely cannot write their tags. The generator is
// reproducing its source faithfully and there is nothing there to correct.
// Regenerating the field from the provider schema instead would overwrite a
// correct CloudFormation fact with an answer to a different question, in the
// one artifact that records the CloudFormation answer, and would put a value
// in a content-pinned CFN mirror that the pin does not cover. So the repair
// is at the reader, not at the generator.
//
// (AWS::IAM::Policy is a separate, narrower generator defect - CloudFormation
// says NOTHING about tagging for it and registry-gen records that silence as
// an explicit false, for 216 of the bundle's 1,683 schemas. It is filed on
// its own because it settles nothing here: the sweep's verdict for
// aws_iam_policy is already #1320's loud one, and the other four types are
// unaffected by it.)
//
// What the reader-side repair is NOT. The arm's CONDITION stays keyed on the
// registry flag. Putting [typeTaggable] in the condition was tried on #1144
// and reverted: it drops the type through to an ordinary covered scan with
// Listed:0, which claims the sweep established the estate owns none of the
// type and takes it out of [Result.SweepGaps] where internal/live/foreign
// reads it (#1153). A recorded gap under-claims, a covered scan over-claims,
// and over-claiming is the one this project's safety rule forbids. What
// changes here is only WHAT THE GAP SAYS.

// TestRegistryTagFlagNeverClaimsAnObjectCannotCarryAMarker is the decisive
// arm and it runs all three shapes through ONE Discover call, one index
// answer and one set of registry rows, so that no shape can pass because its
// run differed.
//
// The discriminator being proved is the provider's own resource schema.
// [ordinaryFlagType] and [flagUntaggableType] both carry a registry row with
// tagging.taggable false, both join from an ARN, and both come back with
// zero candidates. The only thing that differs between them is whether the
// provider gives the type a tags argument, and the verdict has to differ
// with it.
func TestRegistryTagFlagNeverClaimsAnObjectCannotCarryAMarker(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable(ordinaryFlagType)
	cloud.listableUntagged(flagUntaggableType)

	// A live, marked, undeclared object of the schema-taggable type that
	// the index does not return. Its destroy is the thing the gap has to be
	// honest about: the run proposes none for it.
	const deletedAddr = ordinaryFlagType + ".gone"
	cloud.own(ordinaryFlagType, "i-0deadbeef", deletedAddr)

	// The premise, stated rather than assumed: this type has NO measured
	// index-coverage row, so #1320's loud third verdict declines and this
	// run reaches the arm #1322 is about.
	if taggingAPIRestrictedType(ordinaryFlagType) {
		t.Fatalf("%s has acquired a taggingAPITypeCoverage row, so tagIndexHeldNothingGap takes this run and the "+
			"ordinary-coverage arm #1322 is about is never reached", ordinaryFlagType)
	}

	srv := &taggingServer{}
	server := srv.start(t)
	defer server.Close()

	req := Request{
		Sweep:        true,
		TaggingSweep: true,
		Region:       "us-east-1",
		SweepTypes:   []string{ordinaryFlagType, flagUntaggableType},
		Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: server.URL}),
		// The real registry rows, copied rather than corrected: both CFN
		// types carry tagging.taggable false in the committed
		// live/registry.json. That is what puts both in the one arm, which
		// is what makes the comparison mean anything.
		Roster: ccRoster(t,
			map[string]string{
				ordinaryFlagType:   ordinaryFlagCFNType,
				flagUntaggableType: flagUntaggableCFNType,
			},
			nil,
			map[string]bool{ordinaryFlagCFNType: false, flagUntaggableCFNType: false},
		),
	}
	res, diags := discoverFixture(t, cloud, req)
	assertNoErrors(t, diags)

	if srv.calls != 1 {
		t.Fatalf("GetResources was called %d times, want exactly 1 - the index has to have ANSWERED for its empty "+
			"answer to be the thing under test rather than a call that never happened", srv.calls)
	}

	gaps := gapsByType(res)

	// Half one. The provider gives this type a tags argument and
	// internal/live/stamp writes this estate's marker onto it, so
	// "TYPE_NOT_TAGGABLE" - and the sentence it carries, "so it can carry no
	// ownership marker" - is false about it. The registry row that produced
	// that verdict is CloudFormation's answer to a different question.
	if got := gaps[ordinaryFlagType]; got != SweepGapTagIndexCoverageUnconfirmed {
		t.Errorf("the sweep gap for %s is %q, want %q.\n"+
			"live/registry.json calls %s untaggable because CloudFormation cannot write its tags; the provider "+
			"gives it a tags argument and this estate stamps its marker onto every one of them. %q states the "+
			"opposite of that as the reason a destroy was not proposed.",
			ordinaryFlagType, got, SweepGapTagIndexCoverageUnconfirmed, ordinaryFlagCFNType, SweepGapNotTaggable)
	}
	if detail := detailFor(res, ordinaryFlagType); containsAny(detail, "can carry no ownership marker") {
		t.Errorf("the gap detail for %s still says it can carry no ownership marker:\n%s\n"+
			"That sentence is the whole of #1322. It is not softened by being suppressed - it travels into "+
			"views.StatelessSweepGap and into internal/live/foreign's report.", ordinaryFlagType, detail)
	}

	// Half two, and a change that only did half one would fail here: a type
	// whose provider schema carries no tags argument keeps the verdict that
	// is true about it.
	if got := gaps[flagUntaggableType]; got != SweepGapNotTaggable {
		t.Errorf("the sweep gap for %s is %q, want %q.\n"+
			"Its provider schema has no tags argument at all, so nothing was ever there for the index to hold and "+
			"%q is the truth about it. Replacing it everywhere replaces one conflation with another.",
			flagUntaggableType, got, SweepGapNotTaggable, SweepGapNotTaggable)
	}

	// The scoping, asserted rather than assumed: #1322 changes what the gap
	// SAYS and nothing else. Both types keep a recorded gap, both stay out
	// of Result.SweepCovered, and neither reaches the operator. A change
	// that made the ordinary-coverage type loud would put a warning on
	// every plan in every region that an operator can do nothing about -
	// that is a separate decision with its own cost, and it is not this one.
	if spokenAbout(diags, ordinaryFlagType) {
		t.Errorf("a diagnostic was raised about %s: %s\n"+
			"#1322 corrects a false sentence in a suppressed gap. Raising it to a per-run diagnostic for a type "+
			"whose index coverage is ordinary buries the case where the index is known NOT to behave ordinarily "+
			"(#1320), and is a widening with its own argument to make.", ordinaryFlagType, renderDiags(diags))
	}
	if spokenAbout(diags, flagUntaggableType) {
		t.Errorf("a diagnostic was raised about %s: %s", flagUntaggableType, renderDiags(diags))
	}
	for _, typeName := range []string{ordinaryFlagType, flagUntaggableType} {
		for _, c := range res.SweepCovered {
			if c == typeName {
				t.Errorf("%s is in Result.SweepCovered, so the run claims it searched for resources this estate "+
					"owns but no longer declares and found none. It filed a gap instead; both cannot be true - "+
					"and this is exactly the over-claim #1144's attempt was reverted for.", typeName)
			}
		}
	}

	// And the destroy really is lost, which is why the gap's wording is
	// what an operator has to act on.
	if _, ok := removalsByAddr(res)[deletedAddr]; ok {
		t.Fatalf("%s was proposed for removal, so this fixture is no longer the shape under test - some leg "+
			"enumerated the object:\n%s", deletedAddr, res)
	}
}

// TestSweepNotTaggableVerdictTurnsOnTheProviderSchemaAlone holds the type,
// the registry row, the region and the index answer fixed and flips the one
// input that answers "can this object carry a tag". The verdict has to flip
// with it, or something other than the provider schema is deciding it.
func TestSweepNotTaggableVerdictTurnsOnTheProviderSchemaAlone(t *testing.T) {
	run := func(t *testing.T, schemaTaggable bool) SweepGapReason {
		t.Helper()
		cloud := newFakeCloud()
		ownWholeEstate(cloud)
		if schemaTaggable {
			cloud.listable(ordinaryFlagType)
		} else {
			cloud.listableUntagged(ordinaryFlagType)
		}

		srv := &taggingServer{}
		server := srv.start(t)
		defer server.Close()

		res, diags := discoverFixture(t, cloud, Request{
			Sweep:        true,
			TaggingSweep: true,
			Region:       "us-east-1",
			SweepTypes:   []string{ordinaryFlagType},
			Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: server.URL}),
			Roster:       taggingRoster(t, ordinaryFlagType, ordinaryFlagCFNType, false),
		})
		assertNoErrors(t, diags)
		return gapsByType(res)[ordinaryFlagType]
	}

	withTags := run(t, true)
	withoutTags := run(t, false)

	if withTags != SweepGapTagIndexCoverageUnconfirmed {
		t.Errorf("with a tags argument in the provider schema the gap is %q, want %q", withTags, SweepGapTagIndexCoverageUnconfirmed)
	}
	if withoutTags != SweepGapNotTaggable {
		t.Errorf("with no tags argument in the provider schema the gap is %q, want %q", withoutTags, SweepGapNotTaggable)
	}
	if withTags == withoutTags {
		t.Fatalf("both runs filed %q, so the provider schema is not what decides this verdict and the registry "+
			"flag is still being read as the answer to a question it does not answer", withTags)
	}
}

// TestNoRegistryRowStillOutranksBothTagAnswers pins the third branch #168
// put there, which #1322 must not swallow: a CFN type live/mapping.json
// names and live/registry.json has no row for is an artifact skew, and it
// says so, whatever the provider schema says about tags.
func TestNoRegistryRowStillOutranksBothTagAnswers(t *testing.T) {
	for _, schemaTaggable := range []bool{true, false} {
		got := noRegistryRowOrUntaggable("aws_thing", "AWS::Test::Thing", false, schemaTaggable)
		if got.Reason != SweepGapNoRegistryRow {
			t.Errorf("with no registry row and schemaTaggable=%v the gap is %q, want %q - the missing row is a "+
				"skew between two artifacts, not a fact about the type, and #168's message says which commands "+
				"fix it", schemaTaggable, got.Reason, SweepGapNoRegistryRow)
		}
	}
}

// The three shapes the arm has to tell apart, all reached through the
// registry-untaggable arm with a real committed tagging.taggable of false.
const (
	// ordinaryFlagType stands in for the arm's ordinary-coverage
	// population: registry row taggable:false, provider schema with a tags
	// argument, and NO taggingAPITypeCoverage row, so #1320's loud verdict
	// declines and it lands on the suppressed arm.
	//
	// It is a stand-in rather than one of the three real members
	// (aws_launch_template and the two aws_vpc_security_group_*_rule types)
	// for one reason, and it is a property of the fixture rather than of
	// the change: all three are DECLARED in the P0.1 estate
	// [discoverFixture] loads, so the config-driven scan enumerates them
	// and the sweep never files a gap for them there. #1320 chose this same
	// stand-in for the same reason. The real three are covered against the
	// committed artifacts by TestRegistryUntaggableArmPopulation, which
	// calls [noRegistryRowOrUntaggable] on each of them directly.
	ordinaryFlagType    = "aws_instance"
	ordinaryFlagCFNType = "AWS::EC2::Instance"

	// flagUntaggableType is the control: its registry row says the same
	// thing, and its provider schema agrees. SweepGapNotTaggable is true
	// about it and has to survive.
	flagUntaggableType    = "aws_nat_gateway"
	flagUntaggableCFNType = "AWS::EC2::NatGateway"
)

// detailFor is the Detail of the last gap filed for typeName.
func detailFor(res *Result, typeName string) string {
	var out string
	for _, g := range res.SweepGaps {
		if g.TypeName == typeName {
			out = g.Detail
		}
	}
	return out
}

// containsAny reports whether s contains any of the substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// TestNoSweepGapClaimsUntaggableAgainstTheProviderSchema is #1322's external
// check, and it consults the committed artifacts rather than this package's
// own reasoning: live/mapping.json and live/registry.json for which admitted
// types reach [noRegistryRowOrUntaggable] through [sweepViaTagging] at all,
// and live/survey-full.json's signals.taggable column - the same
// [markers.Taggable] answer [typeTaggable] reads off the live provider - for
// whether each of them can carry a marker.
//
// It runs the real function over the real population, so it cannot pass by
// agreeing with the derivation under test: the verdict it checks is the
// value [sweepViaTagging] files.
//
// The claim it pins is the whole issue in one line: no type whose provider
// schema gives it a tags argument may be told that it can carry no ownership
// marker.
func TestNoSweepGapClaimsUntaggableAgainstTheProviderSchema(t *testing.T) {
	for _, tc := range registryUntaggableArmPopulation(t) {
		got := noRegistryRowOrUntaggable(tc.typeName, tc.cfnType, true, tc.schemaTaggable)
		switch {
		case tc.schemaTaggable && got.Reason == SweepGapNotTaggable:
			t.Errorf("%s (%s) is filed as %q.\n"+
				"live/survey-full.json records the provider giving it a tags argument and internal/live/stamp "+
				"writes this estate's marker onto it. The only thing that said otherwise is live/registry.json's "+
				"tagging.taggable, which is CloudFormation's claim about its OWN update-tags API.",
				tc.typeName, tc.cfnType, SweepGapNotTaggable)
		case tc.schemaTaggable && got.Reason != SweepGapTagIndexCoverageUnconfirmed:
			t.Errorf("%s (%s) is filed as %q, want %q", tc.typeName, tc.cfnType, got.Reason, SweepGapTagIndexCoverageUnconfirmed)
		case !tc.schemaTaggable && got.Reason != SweepGapNotTaggable:
			t.Errorf("%s (%s) is filed as %q, want %q - both sources agree it carries no tags, so the reason that "+
				"says exactly that is the right one and must survive", tc.typeName, tc.cfnType, got.Reason, SweepGapNotTaggable)
		}
		if strings.Contains(got.Detail, "can carry no ownership marker") && tc.schemaTaggable {
			t.Errorf("the gap detail for %s still says it can carry no ownership marker:\n%s", tc.typeName, got.Detail)
		}
	}
}

// armType is one admitted type that reaches [sweepViaTagging]'s
// registry-untaggable arm, with the two facts that decide what is said about
// it.
type armType struct {
	typeName       string
	cfnType        string
	schemaTaggable bool
}

// registryUntaggableArmPopulation recomputes, from the three committed
// artifacts, every admitted type that can reach the registry-untaggable arm.
// TestRegistryUntaggableArmPopulation pins what it returns; this returns it
// so more than one test can measure the same population rather than each
// deriving its own.
func registryUntaggableArmPopulation(t *testing.T) []armType {
	t.Helper()

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

	var out []armType
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
		out = append(out, armType{typeName: typeName, cfnType: cfnType, schemaTaggable: schemaTaggable[typeName]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].typeName < out[j].typeName })
	return out
}
