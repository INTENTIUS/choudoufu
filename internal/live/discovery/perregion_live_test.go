// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/registry"
)

// This file is issue #1144's end-to-end half, and it exists because until
// lex00/floci#205 landed it could not.
//
// The routing question #1144 asks is whether the tag index holds a type,
// and the answer real AWS gives for IAM is three different answers:
// iam:role in no region, iam:policy and iam:instance-profile in us-east-1
// only (#1134, measured at scale 50 on a live account). Until the 2026-09-18
// repin the emulator answered "empty" for all three in all regions, so a
// correct narrowing and a broken one produced the identical observable
// result and no test here could tell them apart - which is exactly what
// #1152 was filed to say.
//
// Re-probed directly against the pinned image before this test was written,
// AWS CLI only, no terraform and no choudoufu, four objects tagged
// tofu-estate=p1144 in one container:
//
//	get-resources --region us-east-1 -> instance-profile/p1144-prof, policy/p1144-pol
//	get-resources --region us-west-2 -> volume/vol-5e5599a0b155d1f01
//
// The role is absent from both, the two global IAM types are present in
// us-east-1 and absent from us-west-2, and the us-west-2 call returns a
// tagged EC2 volume in the same response - so an empty IAM answer there is
// the index's own scoping and not a dead endpoint. That is real AWS's shape,
// and it is what makes the four arms below distinguishable.

// perRegionEstate is the fixture's estate name, and it is also the
// tofu-estate marker value testdata/perregion-e2e/main.tf writes.
const perRegionEstate = "perregion-e2e"

// perRegionTypes are the two types under test, and they are here because
// they fail DIFFERENTLY on the leg #1144 routes them off.
//
// aws_iam_instance_profile has no native list resource at the pinned
// provider version, so the per-type leg reaches it only through Cloud
// Control, whose AWS::IAM::InstanceProfile listing carries no tags at all.
// aws_iam_policy has one (iam:ListPolicies) and that call strips tags, so
// the per-type leg reaches the object and then has to join its identifier
// back against the very index the tagging leg would have read directly.
// One type standing for the service is what produced #692's over-broad
// prefix in the first place, so this fixture declines to use one.
var perRegionTypes = []string{"aws_iam_instance_profile", "aws_iam_policy"}

// perRegionAddr is the address a type's tofu-address marker names, and the
// address its removal must be proposed at.
func perRegionAddr(typeName string) string { return typeName + ".demo" }

// withTaggingAPICoverage swaps the package's two coverage tables for the
// duration of one subtest and restores them afterwards.
//
// This is what makes the BREAK arms below RUN rather than be asserted. A
// test that only checked the shipped table would be checking that the table
// says what the table says; these arms install a table that is wrong in a
// specific, plausible way, run the identical production code against the
// identical live emulator, and record that the result differs. Without the
// swap there is no way to show that the shipped routing is load-bearing
// rather than incidental.
func withTaggingAPICoverage(t *testing.T, service map[string]taggingAPICoverage, byType map[string]taggingAPICoverage) {
	t.Helper()
	origService, origType := taggingAPIServiceCoverage, taggingAPITypeCoverage
	taggingAPIServiceCoverage, taggingAPITypeCoverage = service, byType
	t.Cleanup(func() {
		taggingAPIServiceCoverage, taggingAPITypeCoverage = origService, origType
	})
}

// perRegionOutcome is what one arm observed for one type: which leg
// enumerated it, whether the undeclared live object was proposed for
// removal, and whether the run said anything at all when it was not.
type perRegionOutcome struct {
	Source    EnumerationSource
	Recovered bool
	GapReason SweepGapReason
	Covered   bool
}

func (o perRegionOutcome) String() string {
	return "source=" + string(o.Source) +
		" recovered=" + boolWord(o.Recovered) +
		" gap=" + string(o.GapReason) +
		" covered=" + boolWord(o.Covered)
}

func boolWord(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// TestPerRegionTaggingRoutingAgainstFloci is issue #1144's proof, and it is
// four runs of the same production code against one live emulator, differing
// only in the caller's region and in which coverage table is installed.
//
//	arm            region     coverage table            what it stands for
//	----           ------     --------------            ------------------
//	shipped-east   us-east-1  shipped                   the fix
//	shipped-west   us-west-2  shipped                   the fix, outside the indexing region
//	prefix-only    us-east-1  pre-#1144 aws_iam_ prefix the shape #1144 replaces
//	region-blind   us-west-2  "IAM served everywhere"   a narrowing that forgot the region
//
// The two comparisons at the bottom are the finding; an arm's own numbers
// say nothing on their own. If either pair stops differing, the emulator has
// stopped serving what the 2026-09-18 repin bought and this test proves
// nothing - which is worth being told, so it fails in those words rather
// than passing quietly.
//
// Two predictions this test was first written with turned out to be WRONG,
// and they are recorded here because the corrected assertions look
// unmotivated without them. Both came from a stale premise - that the
// provider serves no list resource for aws_iam_instance_profile, true at
// 6.59.0, which is the version live/ pins its artifacts at and NOT the
// version `terraform init` resolves here.
//
//  1. shipped-west was expected not to recover the removal. It does: the
//     provider's own list resource for the type carries its tags at the
//     resolved version, so the per-type leg reads the marker without the
//     index. That is the right outcome and the arm now asserts it -
//     narrowing the routing must not cost a destroy outside us-east-1.
//
//  2. prefix-only was expected not to recover it either, making the
//     east-region comparison a recovered-vs-lost one. It recovers too, by
//     the same route, so what that comparison measures is the LEG and its
//     cost, not a lost object. The lost object lives in the region-blind
//     arm instead, which is the sharper finding anyway: a narrowing that
//     gets the per-type half right and drops the region half loses a live,
//     marked object's destroy in every region but one.
//
//     TF_FLOCI_TEST=1 go test ./internal/live/discovery/ -run TestPerRegionTaggingRoutingAgainstFloci -v
func TestPerRegionTaggingRoutingAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "discovery/perregion")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, terraformBin)

	ctx := context.Background()
	flociPort := flocitest.StartFloci(t, "cdf-perregion1144")
	endpoint := flocitest.Endpoint(flociPort)

	t.Setenv("AWS_ENDPOINT_URL", endpoint)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)

	dir := copyFixture(t, filepath.Join(flocitest.RepoRoot(t), "internal", "live", "discovery", "testdata", "perregion-e2e"))
	flocitest.PluginCacheDir(t)
	flocitest.Run(t, dir, terraformBin, "init", "-input=false", "-no-color")
	flocitest.Run(t, dir, terraformBin, "apply", "-auto-approve", "-input=false", "-no-color")

	stateFile := filepath.Join(dir, "terraform.tfstate")
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("stock apply left no state file: %v", err)
	}
	if err := os.Remove(stateFile); err != nil {
		t.Fatalf("removing the state file: %v", err)
	}
	_ = os.Remove(stateFile + ".backup")

	provider := launchAWSProvider(t, dir)
	cfg := loadConfig(t, dir)

	roster, err := registry.Load(
		filepath.Join(flocitest.RepoRoot(t), "live", "mapping.json"),
		filepath.Join(flocitest.RepoRoot(t), "live", "registry.json"),
	)
	if err != nil {
		t.Fatalf("loading the real live/mapping.json and live/registry.json: %v", err)
	}

	// The premise, probed through this package's own client rather than
	// taken from the manifest. If the emulator does not actually split the
	// index by region, every arm below is measuring nothing, and saying so
	// here is worth more than four arms that agree for the wrong reason.
	assertEmulatorSplitsTheIndexByRegion(ctx, t, endpoint)

	// run is one arm. Resolutions is nil, so nothing is declared and the
	// estate-wide sweep is the only thing that can find the live objects -
	// the same shape TestTaggingSweepAgainstFloci uses one file over.
	//
	// The Cloud Control client is wired on every arm deliberately. Without
	// it nativeSweepReaches can answer false for a type the provider will
	// not list, and arnJoinReaches then sends that type to the tagging leg
	// as a LAST RESORT whatever the coverage table says - issue #881's
	// fallback - which would make the arms agree for a reason that has
	// nothing to do with #1144.
	run := func(t *testing.T, region string) map[string]perRegionOutcome {
		t.Helper()
		res, diags := Discover(ctx, Request{
			Estate:       perRegionEstate,
			Config:       cfg,
			Resolutions:  nil,
			Provider:     provider,
			Region:       region,
			Sweep:        true,
			SweepTypes:   append([]string(nil), perRegionTypes...),
			Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: endpoint, Region: region}),
			CloudControl: cloudcontrol.New(cloudcontrol.Config{Endpoint: endpoint, Region: region}),
			TaggingSweep: true,
			Roster:       roster,
		})
		assertNoErrors(t, diags)
		t.Logf("region=%s discovery result:\n%s", region, res)

		removals := removalsByAddr(res)
		out := make(map[string]perRegionOutcome, len(perRegionTypes))
		for _, typeName := range perRegionTypes {
			o := perRegionOutcome{}
			if scan, ok := res.ScanFor(typeName); ok {
				o.Source = scan.Source
			}
			_, o.Recovered = removals[perRegionAddr(typeName)]
			for _, g := range res.SweepGaps {
				if g.TypeName == typeName {
					o.GapReason = g.Reason
				}
			}
			for _, c := range res.SweepCovered {
				if c == typeName {
					o.Covered = true
				}
			}
			t.Logf("  %-26s %s", typeName, o)
			out[typeName] = o
		}
		return out
	}

	shippedService := taggingAPIServiceCoverage

	var shippedEast, shippedWest, prefixOnly, regionBlind map[string]perRegionOutcome

	t.Run("shipped-east", func(t *testing.T) {
		shippedEast = run(t, "us-east-1")
		for _, typeName := range perRegionTypes {
			got := shippedEast[typeName]
			if got.Source != SourceTagging {
				t.Errorf("%s was enumerated by %q, want %q.\n"+
					"us-east-1 is the one region the index holds this type in, so arnJoinReaches must send it to "+
					"the one estate-wide GetResources call the sweep has already paid for, rather than to a "+
					"whole-account per-type listing whose tags this repository has measured being stripped "+
					"(#266, #1046). Outcome: %s", typeName, got.Source, SourceTagging, got)
			}
			if !got.Recovered {
				t.Errorf("the live %s carrying this estate's marker for %s was NOT proposed for removal.\n"+
					"This is the destroy issue #881 is about, and in us-east-1 the tag index holds the object with "+
					"both its markers inline - this test's own premise probe saw the ARN come back. Outcome: %s",
					typeName, perRegionAddr(typeName), got)
			}
			if got.GapReason != "" {
				t.Errorf("a sweep gap (%q) was filed for %s on the run that found its object - the gap says the "+
					"search established nothing, on the run that established everything. Outcome: %s",
					got.GapReason, typeName, got)
			}
		}
	})

	t.Run("shipped-west", func(t *testing.T) {
		shippedWest = run(t, "us-west-2")
		for _, typeName := range perRegionTypes {
			got := shippedWest[typeName]
			if got.Source == SourceTagging {
				t.Errorf("%s was enumerated by the tagging leg from us-west-2, where the index does not hold it.\n"+
					"Sending a type to an index that cannot answer for it is what #692 stopped doing for the whole "+
					"of IAM, and what #1144 must keep doing for the regions that still cannot answer. Outcome: %s",
					typeName, got)
			}
			// Narrowing the routing may not COST anything outside
			// us-east-1. The per-type leg is what the caller falls back
			// to there, and #1136's rule applies to it: recover the
			// object, or say the search established nothing. Never both
			// silent.
			if !got.Recovered && got.GapReason == "" {
				t.Errorf("%s was neither recovered nor gapped in us-west-2, so this run is indistinguishable from "+
					"one that established the estate owns none. That is the silent drop #1136 closed, and "+
					"#1144's routing must not re-open it outside the indexing region. Outcome: %s", typeName, got)
			}
		}
	})

	// ── BREAK arms. These install a wrong table and run the same code. ──

	t.Run("break/prefix-only", func(t *testing.T) {
		// The pre-#1144 shape, restated exactly: one service prefix, no
		// per-type rows, no region anywhere.
		withTaggingAPICoverage(t,
			map[string]taggingAPICoverage{"aws_iam_": {Indexed: false, Evidence: "issue #692's prefix, as it stood before #1144"}},
			map[string]taggingAPICoverage{})
		prefixOnly = run(t, "us-east-1")
	})

	t.Run("break/region-blind", func(t *testing.T) {
		// A narrowing that got the per-type half right and dropped the
		// region half - the single most likely way to implement #1144
		// wrongly, and the one no emulator run could have caught before
		// the repin.
		withTaggingAPICoverage(t, shippedService, map[string]taggingAPICoverage{
			"aws_iam_role":             {Indexed: false, Evidence: "correct"},
			"aws_iam_policy":           {Indexed: true, Evidence: "WRONG: served, with no region restriction"},
			"aws_iam_instance_profile": {Indexed: true, Evidence: "WRONG: served, with no region restriction"},
		})
		regionBlind = run(t, "us-west-2")
	})

	// ── The decisive comparisons. ──

	t.Run("prefix-only-pays-a-per-type-list-the-index-had-already-answered", func(t *testing.T) {
		for _, typeName := range perRegionTypes {
			shipped, old := shippedEast[typeName], prefixOnly[typeName]
			if shipped == old {
				t.Fatalf("for %s the shipped per-type/per-region table and the pre-#1144 prefix-only table produced "+
					"the IDENTICAL result in us-east-1 (%s).\n"+
					"That is the state #1152 recorded and this repin was supposed to end: if a correct narrowing "+
					"and the shape it replaces cannot be told apart on the emulator, #1144's behaviour is "+
					"unmeasured here. Check that GetResources still returns this type's ARNs in us-east-1 before "+
					"believing either row.", typeName, shipped)
			}
			if old.Source == SourceTagging {
				t.Errorf("the prefix-only table routed %s to the tagging leg, which it cannot do - its whole content "+
					"is that IAM is unserved everywhere. The break arm is not installing what it claims. %s",
					typeName, old)
			}
			t.Logf("us-east-1 %s: shipped [%s] vs prefix-only [%s]", typeName, shipped, old)
		}
	})

	t.Run("region-blind-narrowing-loses-the-destroy", func(t *testing.T) {
		for _, typeName := range perRegionTypes {
			shipped, broken := shippedWest[typeName], regionBlind[typeName]
			if shipped == broken {
				t.Fatalf("for %s the shipped table and a region-blind narrowing produced the IDENTICAL result in "+
					"us-west-2 (%s).\n"+
					"The region half of #1144 is then unproven: nothing here distinguishes a routing that knows "+
					"IAM indexes only in us-east-1 from one that thinks it indexes everywhere.", typeName, shipped)
			}
			// The property, and it is the fork's safety rule rather than
			// an outcome count: a run either proposes the destroy, or
			// tells the operator it could not establish one. Both are
			// answers. A region-blind narrowing gives neither.
			//
			// Two drafts of this arm asserted the wrong thing before the
			// emulator was allowed to answer, and both are worth recording
			// because a reader will otherwise wonder why the assertions
			// are shaped like this.
			//
			// The first demanded that the shipped table RECOVER in
			// us-west-2. That holds for aws_iam_instance_profile and must
			// NOT hold for aws_iam_policy: iam:ListPolicies strips tags
			// and the us-west-2 index holds no IAM, so there is no route
			// to that object's marker from there at all, and
			// MARKER_UNREADABLE is the correct answer. Demanding recovery
			// was demanding a marker read out of thin air.
			//
			// The second demanded that the broken table say NOTHING. It
			// files something, and what it files changed under issue
			// #1318, which is worth recording because it makes this break
			// LESS harmful than it was when this test was written.
			//
			// Until #1318 the broken table filed SweepGapNotTaggable, on
			// the strength of live/registry.json's stale taggable:false
			// row - a reason [sweepGapDiag] suppresses, so no diagnostic
			// reached the operator at all, and the text it would have
			// carried was false about this object anyway: the profile
			// plainly does carry a marker, which is how the us-east-1 arm
			// read it. #1318 gave [sweepViaTagging]'s registry-untaggable
			// arm a third verdict for exactly that case, so the broken
			// table now files SweepGapTagIndexHeldNothing and SPEAKS.
			//
			// So the region-blind break's harm is no longer "silent". It
			// is the lost destroy alone, plus a reason that is true as far
			// as it goes and still not the truth: the run says the index
			// it was pointed at held none of this type, where the fact is
			// that us-west-2's index holds none of this type EVER and the
			// shipped table knows it. The assertion stays on the REASON,
			// because what the run says is the thing that differs.
			if broken.Recovered {
				t.Errorf("the region-blind table still recovered %s from us-west-2, so it is not the break this arm "+
					"means to run - re-derive what it is installing. region-blind: %s", typeName, broken)
			}
			if broken.GapReason != SweepGapTagIndexHeldNothing {
				t.Errorf("the region-blind table filed %q for %s, want %q.\n"+
					"A region-blind narrowing tells the sweep this type is indexed here, so an empty answer lands "+
					"in the registry-untaggable arm and #1318's third verdict is what that arm now says about a "+
					"type the provider schema calls taggable. %q here would mean the pre-#1318 suppression is "+
					"back and the lost destroy is silent again; any other reason means the run said something "+
					"else and the comparison is measuring something else. region-blind: %s",
					broken.GapReason, typeName, SweepGapTagIndexHeldNothing, SweepGapNotTaggable, broken)
			}
			if !shipped.Recovered && shipped.GapReason != SweepGapMarkerUnreadable {
				t.Errorf("in us-west-2 the shipped table neither recovered %s nor filed %q for it (it filed %q).\n"+
					"Outside the indexing region the correct routing owes the operator one of those two: the "+
					"destroy, or the named reason there is no route to the marker. shipped-west: %s",
					typeName, SweepGapMarkerUnreadable, shipped.GapReason, shipped)
			}
			t.Logf("us-west-2 %s: shipped [%s] vs region-blind [%s] - the correct routing proposes the destroy or "+
				"names why it cannot; the region-blind one loses the destroy and can only report an index that "+
				"held nothing, where the fact is that this region's index holds none of this type at all",
				typeName, shipped, broken)
		}
	})
}

// assertEmulatorSplitsTheIndexByRegion is the premise the four arms rest on,
// asked of the running container through this package's own Tagging client.
//
// Two calls, and the second one is the control. A us-west-2 GetResources
// that returns nothing proves nothing by itself - the endpoint could be
// dead, the credentials wrong, the estate never tagged. It means something
// only once the same client, against the same container, is shown returning
// SOMETHING in the other region. The IAM objects are global and tagged; if
// us-west-2 answers empty while us-east-1 answers with their ARNs, the index
// is region-scoped, which is the whole premise.
func assertEmulatorSplitsTheIndexByRegion(ctx context.Context, t *testing.T, endpoint string) {
	t.Helper()

	filters := []cloudcontrol.TagFilter{{Key: "tofu-estate", Values: []string{perRegionEstate}}}

	east := cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: endpoint, Region: "us-east-1"})
	eastRes, err := east.GetResources(ctx, nil, filters)
	if err != nil {
		t.Fatalf("GetResources in us-east-1 failed: %v", err)
	}
	for _, seg := range []string{":instance-profile/", ":policy/"} {
		var found bool
		for _, r := range eastRes {
			if strings.Contains(r.ResourceARN, seg) {
				found = true
			}
		}
		if !found {
			t.Fatalf("the pinned emulator returned no %s ARN from GetResources in us-east-1 for estate %q.\n"+
				"Real AWS returns 500 of each there (#1134) and lex00/floci#205 taught this image to match. "+
				"Without that, every arm collapses to the pre-repin state #1152 describes, where a correct "+
				"narrowing and a broken one are indistinguishable. ARNs returned: %v", seg, perRegionEstate, arns(eastRes))
		}
	}

	west := cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: endpoint, Region: "us-west-2"})
	westRes, err := west.GetResources(ctx, nil, filters)
	if err != nil {
		t.Fatalf("GetResources in us-west-2 failed: %v", err)
	}
	for _, r := range westRes {
		if strings.Contains(r.ResourceARN, ":iam:") || strings.Contains(r.ResourceARN, "arn:aws:iam:") {
			t.Fatalf("the pinned emulator returned an IAM ARN from GetResources in us-west-2 (%s).\n"+
				"IAM is global and real AWS indexes it in us-east-1 ONLY (#1134), so this image no longer matches "+
				"AWS on the one behaviour #1144's region half is about. ARNs returned: %v", r.ResourceARN, arns(westRes))
		}
	}
	t.Logf("premise holds: GetResources(tofu-estate=%s) returns %d ARN(s) in us-east-1 (%v) and %d in us-west-2 (%v)",
		perRegionEstate, len(eastRes), arns(eastRes), len(westRes), arns(westRes))
}

func arns(rs []cloudcontrol.TaggedResource) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.ResourceARN)
	}
	return out
}
