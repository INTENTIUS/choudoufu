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

	req, cc, tagging := doubleSightingFixture(t, liveName, instanceProfileType+".original")
	res, diags := Discover(context.Background(), req)

	// The premise again, from the other end: both legs really did run and
	// really did see this object. Without this the test could pass because
	// a routing change silenced one leg, which is not the fix.
	assertBothLegsSaw(t, res, cc, tagging)

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

	req, cc, tagging := doubleSightingFixture(t, liveName, instanceProfileType+".renamed")
	res, diags := Discover(context.Background(), req)

	assertBothLegsSaw(t, res, cc, tagging)

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

// assertBothLegsSaw fails unless the config-driven Cloud Control leg and the
// estate-wide tagging sweep BOTH enumerated the type in this pass. It is the
// control that keeps the two tests above load-bearing: a routing change that
// leaves only one leg running would make them pass while saying nothing.
func assertBothLegsSaw(t *testing.T, res *Result, cc *ccServer, tagging *taggingServer) {
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
		t.Fatalf("no config-driven Cloud Control scan of %s ran, so this fixture is not the double-sighting shape at all; scans: %+v", instanceProfileType, res.Scans)
	}
	if !swept {
		t.Fatalf("no estate-wide tagging sweep of %s ran, so this fixture is not the double-sighting shape at all; scans: %+v", instanceProfileType, res.Scans)
	}
	if len(cc.calls) == 0 {
		t.Fatalf("the Cloud Control fake was never called")
	}
	if tagging.calls == 0 {
		t.Fatalf("the Resource Groups Tagging API fake was never called")
	}
}

func describeProblems(res *Result) []string {
	out := make([]string, 0, len(res.Problems))
	for _, p := range res.Problems {
		out = append(out, string(p.Kind)+": "+p.Detail)
	}
	return out
}
