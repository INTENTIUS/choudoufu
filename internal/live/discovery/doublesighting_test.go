// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"sort"
	"testing"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
)

// doubleSightingFixture wires the shape corpus-overture-tiles' day2_rename
// regressed on: ONE live object of a declared, needs-discovery type that
// BOTH enumeration legs see in the same pass.
//
//   - the config-driven leg reaches it through Cloud Control, because the
//     provider serves no list resource for the type;
//   - the estate-wide sweep leg reaches the SAME object through the Resource
//     Groups Tagging API, because [sweepTypes] adds a declared type in an
//     unserved service back into the sweep universe (issue #692) and
//     [arnJoinReaches] routes it to the tagging leg when the native leg has
//     no route (issue #881).
//
// Both sightings carry the marker for the address the moved block moves
// FROM, so both resolve, correctly, to the one declaredEntry the moved-to
// address owns.
//
// THE SECOND LEG IS GONE, and these tests are narrower than they were.
// Issue #881, reopened: [arnJoinReaches] used to ask "can the native leg
// enumerate this type" as [listclient.Schemas.Supports] alone, which is
// false for aws_iam_instance_profile, so a DECLARED instance profile was
// routed to the tagging leg while the config-driven pass reached the same
// object through Cloud Control - the two sightings this file is named for.
// [nativeSweepReaches] now counts Cloud Control as the route it is, so the
// type goes to the native universe, where [dedupAlreadyConfigScanned] drops
// it because the config-driven pass already listed the whole account for
// it. One leg, one sighting.
//
// [sweepTypes] adds a DECLARED type back into the sweep universe only for a
// [taggingAPIUnservedType], and those are exactly the types the corrected
// routing sends native - so this shape is not merely absent from this
// fixture, it has no remaining producer. What the three tests below still
// pin is the one-claimant outcome and the genuine two-object collision;
// what they no longer exercise is the dedup that had to defuse a second
// sighting. If a future change puts a declared type back on the tagging leg
// while its config-driven pass also enumerates it, that dedup is unguarded
// and this file is where the guard belongs.
//
// OPEN, and deliberately not decided here: the corpus-overture-tiles
// regression these tests were written for was measured on floci BEFORE the
// 2026-09-11 repin (lex00/floci#202, live/flociimage_test.go), when the
// emulator still answered GetResources for IAM - which real AWS does not,
// and has never done (probed directly, recorded on issue #692). Whether the
// two-leg shape was ever reachable on a real AWS account, or was an
// emulator artifact throughout, is unverified. This fixture's own
// taggingServer serving an IAM ARN is the same fiction.
func doubleSightingFixture(t *testing.T, liveName, markerAddr string) (Request, *ccServer, *taggingServer) {
	t.Helper()

	const cfnType = "AWS::IAM::InstanceProfile"

	// The premise, stated rather than assumed.
	if !taggingAPIUnservedType(instanceProfileType) {
		t.Fatalf("%s is no longer in a service taggingAPIUnservedServices names, so this fixture no longer puts a declared type back into the sweep universe", instanceProfileType)
	}

	// newFakeCloud does not list this type, which is the real provider's
	// answer at 6.59.0 too: eight of IAM's types have a list resource and
	// this is not one of them.
	cloud := newFakeCloud()

	cc := newCCServer(t)
	cc.listResources[cfnType] = []ccResource{{
		identifier: liveName,
		properties: tagsProps(estateName, markerAddr),
	}}
	ccSrv := cc.start()
	t.Cleanup(ccSrv.Close)

	arn := "arn:aws:iam::123456789012:instance-profile/" + liveName
	tagging := &taggingServer{
		arns: []string{arn},
		tags: map[string]map[string]string{
			arn: {TagEstate: estateName, TagAddress: markerAddr},
		},
	}
	tagSrv := tagging.start(t)
	t.Cleanup(tagSrv.Close)

	cfg := loadConfig(t, "testdata/moved-sweep-double-sighting")
	req := Request{
		Estate:       estateName,
		Config:       cfg,
		Resolutions:  resolveOrFail(t, cfg).All(),
		Provider:     cloud,
		Sweep:        true,
		TaggingSweep: true,
		CloudControl: cloudcontrol.New(cloudcontrol.Config{Endpoint: ccSrv.URL}),
		Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: tagSrv.URL}),
		Roster: ccRoster(t,
			map[string]string{instanceProfileType: cfnType},
			map[string]bool{cfnType: true},
			map[string]bool{cfnType: true},
		),
	}
	return req, cc, tagging
}

// instanceProfileType is spelled once, in the fixture, so the assertions
// below read as being about a shape rather than about IAM.
const instanceProfileType = "aws_iam_instance_profile"

// TestOneLiveObjectSeenByBothLegsIsOneClaimant is the corpus-overture-tiles
// day2_rename regression at unit scale.
//
// Two enumeration legs listing one live object filed TWO claimants on the
// declared instance's single entry, and the second one was read as a second
// live resource racing for the address: [ProblemCollision], "2 live
// aws_iam_instance_profile resources carry estate ... and address ... at
// once", printing the ONE object's own name twice. A refusal where stock
// plans a rename, on an estate choudoufu had already applied.
//
// The assertions are by value - which address bound, to which identity, and
// that the run produced no problem at all - not a predicate about which leg
// won.
func TestOneLiveObjectSeenByBothLegsIsOneClaimant(t *testing.T) {
	const liveName = "estate-f9d5b733c2306d34e34c7395b0"

	req, cc, _ := doubleSightingFixture(t, liveName, instanceProfileType+".original")
	res, diags := Discover(context.Background(), req)

	// The premise again, from the other end: both legs really did run and
	// really did see this object. Without this the test could pass because
	// a routing change silenced one leg, which is not the fix.
	assertOneLegSaw(t, res, cc)

	if diags.HasErrors() {
		t.Fatalf("one live object seen by two legs refused the plan:\n%s\n%s", renderDiags(diags), res)
	}
	if len(res.Problems) != 0 {
		t.Fatalf("one live object seen by two legs produced problems: %v\n%s", describeProblems(res), res)
	}

	want := instanceProfileType + ".renamed"
	b, ok := res.BindingFor(mustAddr(t, want))
	if !ok {
		t.Fatalf("%s did not bind, so the plan proposes creating a resource that already exists:\n%s", want, res)
	}
	if b.ImportID != liveName {
		t.Errorf("%s bound to import identity %q, want %q - a binding is what a plan reads the live object at", want, b.ImportID, liveName)
	}
	if len(res.Bindings) != 1 {
		var got []string
		for _, binding := range res.Bindings {
			got = append(got, binding.Addr.String()+"="+binding.ImportID)
		}
		sort.Strings(got)
		t.Errorf("bound %v, want exactly one binding - one live object is one binding however many legs saw it", got)
	}
	if len(res.Orphans) != 0 {
		t.Errorf("the twice-seen object was also filed as an orphan, which plans a destroy: %v", res.Orphans)
	}
}

// TestOneLiveObjectSeenByBothLegsAtItsOwnAddress is the same fixture with
// the moved block already applied - the marker naming the destination
// address rather than the origin. The moved alias is not what makes the two
// sightings meet; the entry is, and it is reached by the canonical index
// here and by the alias index above. Both spellings have to survive the
// double sighting, or the fix would only cover a pending rename.
func TestOneLiveObjectSeenByBothLegsAtItsOwnAddress(t *testing.T) {
	const liveName = "estate-0a1b2c3d4e5f60718293a4b5c6"

	req, cc, _ := doubleSightingFixture(t, liveName, instanceProfileType+".renamed")
	res, diags := Discover(context.Background(), req)

	assertOneLegSaw(t, res, cc)

	if diags.HasErrors() {
		t.Fatalf("one live object seen by two legs at its own address refused the plan:\n%s\n%s", renderDiags(diags), res)
	}
	if len(res.Problems) != 0 {
		t.Fatalf("one live object seen by two legs at its own address produced problems: %v\n%s", describeProblems(res), res)
	}

	want := instanceProfileType + ".renamed"
	b, ok := res.BindingFor(mustAddr(t, want))
	if !ok {
		t.Fatalf("%s did not bind:\n%s", want, res)
	}
	if b.ImportID != liveName {
		t.Errorf("%s bound to import identity %q, want %q", want, b.ImportID, liveName)
	}
}

// TestTwoDifferentLiveObjectsStillCollide is the mutation control for the
// fix above, and it is the half that matters most: the deduplication is by
// import identity, so it must refuse exactly when two DIFFERENT live
// objects claim one address. Silencing that would be a wrong marker - one
// of the two would be bound and the other quietly left carrying a marker
// for an address it does not own - which live/MARKERS.md and HANDOFF's
// safety rule both put above any refusal.
//
// It was TestTwoDifferentLiveObjectsAcrossTheTwoLegsStillCollide, and it
// put the second object in the tagging fake. Issue #881's routing fix takes
// a declared aws_iam_instance_profile off the tagging leg, so that object
// became invisible and the collision stopped firing. Moving it into the
// Cloud Control listing is not a workaround for the assertion: it is where
// a real second object is actually found. The config-driven Cloud Control
// pass lists the whole account (scan.Scope ScopeAll), and GetResources
// returns nothing for IAM on real AWS or on floci since the 2026-09-11
// repin - so the tagging leg was never the leg that would have caught this
// on a real estate, only in this fixture.
func TestTwoDifferentLiveObjectsStillCollide(t *testing.T) {
	const firstObject = "estate-1111111111111111111111"
	const secondObject = "estate-2222222222222222222222"
	const markerAddr = instanceProfileType + ".original"

	req, cc, _ := doubleSightingFixture(t, firstObject, markerAddr)

	// A second, genuinely different live object carrying the same marker,
	// in the same account-wide listing the first came from.
	cc.listResources["AWS::IAM::InstanceProfile"] = append(
		cc.listResources["AWS::IAM::InstanceProfile"],
		ccResource{identifier: secondObject, properties: tagsProps(estateName, markerAddr)},
	)

	res, diags := Discover(context.Background(), req)
	assertOneLegSaw(t, res, cc)

	if !diags.HasErrors() {
		t.Fatalf("two different live objects claiming one address did not refuse the plan - the deduplication is masking a real collision:\n%s", res)
	}
	var found *Problem
	for i, p := range res.Problems {
		if p.Kind == ProblemCollision {
			found = &res.Problems[i]
		}
	}
	if found == nil {
		t.Fatalf("no %s problem was reported: %v\n%s", ProblemCollision, describeProblems(res), res)
	}
	got := append([]string(nil), found.LiveIDs...)
	sort.Strings(got)
	want := []string{firstObject, secondObject}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("the collision names %v, want %v - a collision that does not name both real objects cannot be resolved by the human it is addressed to", got, want)
	}
	if len(res.Bindings) != 0 {
		t.Errorf("a contested address bound anyway: %v", res.Bindings)
	}
}

// assertOneLegSaw is what assertBothLegsSaw became when issue #881's
// routing fix removed the second leg (see doubleSightingFixture's own
// comment for the whole argument).
//
// It used to fail unless the config-driven Cloud Control leg and the
// estate-wide tagging sweep BOTH enumerated the type - the control that
// kept the tests below from passing vacuously if a routing change left only
// one leg running. That control did its job: it is what caught the
// behaviour change rather than letting it through silently.
//
// The half that still earns its place is the config-driven sighting: every
// assertion below is about what happened to an enumerated object, so a pass
// that enumerated nothing must not read as agreement. The tagging half is
// now asserted in the negative, because a declared aws_iam_instance_profile
// reaching the tagging leg would mean the routing fix had regressed.
func assertOneLegSaw(t *testing.T, res *Result, cc *ccServer) {
	t.Helper()

	var configDriven, swept bool
	for _, s := range res.Scans {
		if s.TypeName != instanceProfileType {
			continue
		}
		if s.Sweep && s.Source == SourceTagging {
			swept = true
		}
		if !s.Sweep && s.Source == SourceCloudControl {
			configDriven = true
		}
	}
	if !configDriven {
		t.Fatalf("no config-driven Cloud Control scan of %s ran, so nothing was enumerated and the assertions below say nothing; scans: %+v", instanceProfileType, res.Scans)
	}
	if swept {
		t.Fatalf("the estate-wide tagging sweep enumerated %s, which issue #881's routing fix sends to the native leg instead - [nativeSweepReaches] or [dedupAlreadyConfigScanned] has regressed; scans: %+v", instanceProfileType, res.Scans)
	}
	if len(cc.calls) == 0 {
		t.Fatalf("the Cloud Control fake was never called")
	}
}

func describeProblems(res *Result) []string {
	out := make([]string, 0, len(res.Problems))
	for _, p := range res.Problems {
		out = append(out, string(p.Kind)+": "+p.Detail)
	}
	return out
}
