// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/pkg/azsweep"
	"github.com/intentius/choudoufu/pkg/cloudcontrol"
	"github.com/intentius/choudoufu/pkg/kubesweep"
)

// GitHub issue #2136. A block renamed in configuration without a moved
// block's help is not the case here: the ADDRESS stays, the identity
// argument changes. The configuration used to name "rt-old" (or "b-old", or
// ConfigMap "cfg-old") and now names "rt-new". The object the last apply
// made still exists at the old name and still carries tofu-address = the
// unchanged address. The question each test below answers per platform:
// does the sweep treat that old object as the declared instance's own
// object (and vouch for the address, which lets the projection admit
// whatever sits at the NEW name without reading its tags), as an orphan, or
// as a displaced marker?
//
// The decision is [declared.displacedFrom] -> [configIdentityMismatch]
// (displaced.go). Before #2136 its per-attribute comparison needed an
// identity object on the listed claimant and its fallback compared whole
// import ids only for a type with a one-component identity.DefaultTable
// row; every other doubt answered verdictOwnObject, which vouches. An
// azurerm claimant from Resource Graph has no identity object and no
// azurerm type has a DefaultTable row, so on Azure the comparison never ran
// and the old object vouched. It now compares two ARM ids, case-
// insensitively, whatever the table says. AWS and Kubernetes are the
// controls: their behaviour is unchanged.

const (
	renameOldRT = "rt-old"
	renameNewRT = "rt-new"
	renameRTAdr = "azurerm_route_table.rt"
)

func renameRTID(name string) string {
	return rgID(rgSub1, "Kept") + "/providers/Microsoft.Network/routeTables/" + name
}

// renameOldHit is the route table the last apply made, as Resource Graph
// lists it: the type and the resource group lowercased, still carrying this
// estate's marker for azurerm_route_table.rt.
func renameOldHit() azsweep.Resource {
	return azsweep.Resource{
		ID:             strings.ToLower(renameRTID(renameOldRT)),
		Type:           "microsoft.network/routetables",
		Name:           renameOldRT,
		ResourceGroup:  "kept",
		SubscriptionID: rgSub1,
		Tags: map[string]string{
			markers.TagEstate:  rgEstate,
			markers.TagAddress: markers.EscapeAddress(renameRTAdr),
		},
	}
}

const renameRTConfig = `
resource "azurerm_route_table" "rt" {
  name                = "rt-new"
  resource_group_name = "Kept"
  location            = "westeurope"
}
`

// TestRenameBind_azureOldObjectReportsDisplaced: the route table renamed
// rt-old -> rt-new in configuration. Resource Graph lists rt-old carrying
// this estate's marker for azurerm_route_table.rt. It is reported as a
// DISPLACED_MARKER warning naming both ids, is not an orphan, and does not
// vouch for the address, so the projection reads and judges the tags of
// whatever sits at rt-new: AWS's answer for aws_s3_bucket below. The same
// holds whether or not the resolution carries identity-schema values: the
// Resource Graph claimant has no identity object to compare them with, so
// the ARM-id comparison is what answers.
func TestRenameBind_azureOldObjectReportsDisplaced(t *testing.T) {
	if ti, ok := identity.LookupType("azurerm_route_table"); ok && len(ti.Components) == 1 {
		t.Fatalf("azurerm_route_table now has a one-component identity row; the table fallback would answer before the ARM-id comparison and this test no longer exercises it")
	}
	cfg := azConfigWith(t, renameRTConfig)
	for name, values := range map[string]map[string]string{
		"no identity values": nil,
		"identity-schema values": {
			"name":                renameNewRT,
			"resource_group_name": "Kept",
			"subscription_id":     rgSub1,
		},
	} {
		t.Run(name, func(t *testing.T) {
			old := renameOldHit()
			graph := &fakeGraph{answers: map[string][]azsweep.Resource{rgSub1: {old}}}
			leg := ResourceGraphSweep{Coverage: azsweep.Coverage{rgSub1: graph}, Subscriptions: []string{rgSub1}}
			rt := aksResolution("azurerm_route_table", "rt", renameRTID(renameNewRT))
			rt.IdentityValues = values

			res, diags := discoverAzureWith(t, cfg, leg, keptResolution(rgSub1), rt)
			assertNoErrors(t, diags)

			p := findDisplaced(t, res, old.ID)
			if !strings.Contains(p.Detail, renameNewRT) || !strings.Contains(p.Detail, renameOldRT) {
				t.Errorf("the warning does not name both ids:\n%s", p.Detail)
			}
			// Nothing declares rt-old, so "not read" is true of it and
			// stays (TestDisplacedProblem_saysWhereTheObjectSits).
			if !strings.Contains(p.Detail, "it is bound to no address, it is not read") {
				t.Errorf("a plain rename's warning lost its wording:\n%s", p.Detail)
			}
			assertNotAnOrphan(t, res, old.ID)
			if res.MarkerVerified()[renameRTAdr] {
				t.Errorf("rt-old's sighting vouched for %s, which would admit whatever sits at rt-new unread: %v", renameRTAdr, res.VerifiedDeclared)
			}
		})
	}
}

// TestRenameBind_azureRecordNamingTheOldObjectIsAReplace is #885's bound on
// the fix above, on Azure. Renaming azurerm_route_table's name is a ForceNew
// replace, and the estate's record for the address names rt-old, written in
// the configuration's casing while Resource Graph lists it lowercased. The
// record names this object, so it is the replace's old half: no warning, no
// orphan, and still no vouch. With the record compared exactly, every
// ordinary Azure rename would have read as displaced.
func TestRenameBind_azureRecordNamingTheOldObjectIsAReplace(t *testing.T) {
	old := renameOldHit()
	graph := &fakeGraph{answers: map[string][]azsweep.Resource{rgSub1: {old}}}
	leg := ResourceGraphSweep{Coverage: azsweep.Coverage{rgSub1: graph}, Subscriptions: []string{rgSub1}}
	rawStore, seedStore := supersededHintStore(t, rgEstate)
	seedCurrentIdentity(t, seedStore, renameRTAdr, projection.LocatedRecord{ImportID: renameRTID(renameOldRT)})

	res, diags := Discover(context.Background(), Request{
		Estate:      rgEstate,
		Config:      azConfigWith(t, renameRTConfig),
		Provider:    newFakeCloud(),
		Resolutions: []identity.Resolution{keptResolution(rgSub1), aksResolution("azurerm_route_table", "rt", renameRTID(renameNewRT))},
		Sweepers:    []Sweeper{leg},
		HintStore:   rawStore,
	})
	assertNoErrors(t, diags)
	for _, p := range res.ProblemsOfKind(ProblemDisplacedMarker) {
		t.Fatalf("an ordinary ForceNew rename was reported as a displacement:\n%s", p.Detail)
	}
	assertNotAnOrphan(t, res, old.ID)
	if res.MarkerVerified()[renameRTAdr] {
		t.Errorf("the object a replace is about to destroy vouched for %s", renameRTAdr)
	}
}

// TestRenameBind_azureOwnIDInAnyCaseVouches is the other bound: an object
// at the id its address resolves to, listed in Resource Graph's lowercased
// spelling, is that address's own object and vouches for it, with no
// warning. An exact comparison would report every Azure object as
// displaced from itself.
func TestRenameBind_azureOwnIDInAnyCaseVouches(t *testing.T) {
	hit := renameOldHit()
	hit.ID, hit.Name = strings.ToLower(renameRTID(renameNewRT)), renameNewRT
	graph := &fakeGraph{answers: map[string][]azsweep.Resource{rgSub1: {hit}}}
	leg := ResourceGraphSweep{Coverage: azsweep.Coverage{rgSub1: graph}, Subscriptions: []string{rgSub1}}
	res, diags := discoverAzureWith(t, azConfigWith(t, renameRTConfig), leg, keptResolution(rgSub1), aksResolution("azurerm_route_table", "rt", renameRTID(renameNewRT)))
	assertNoErrors(t, diags)
	if len(res.Orphans) != 0 || len(res.Problems) != 0 {
		t.Errorf("orphans %+v, problems %+v; want none", res.Orphans, res.Problems)
	}
	if !res.MarkerVerified()[renameRTAdr] {
		t.Errorf("the object at %s's own id did not vouch for it: %v", renameRTAdr, res.VerifiedDeclared)
	}
}

// TestConfigIdentityMismatchARMIDs pins the comparison itself, below
// fileHit's respelling of a declared id (which hides the case question from
// every sweep test above): two ARM ids equal but for case are one object,
// two that differ are not, and a non-ARM id keeps its exact comparison.
func TestConfigIdentityMismatchARMIDs(t *testing.T) {
	declared := identity.Resolution{ImportID: renameRTID(renameNewRT)}
	for _, tc := range []struct {
		name, live string
		mismatched bool
	}{
		{"same id", renameRTID(renameNewRT), false},
		{"same id, lowercased", strings.ToLower(renameRTID(renameNewRT)), false},
		{"another name", strings.ToLower(renameRTID(renameOldRT)), true},
		{"another resource group", rgID(rgSub1, "Other") + "/providers/Microsoft.Network/routeTables/" + renameNewRT, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want, got := configIdentityMismatch("azurerm_route_table", declared, claimant{importID: tc.live})
			if got != tc.mismatched {
				t.Fatalf("mismatched %v, want %v", got, tc.mismatched)
			}
			if got && want != declared.ImportID {
				t.Errorf("want %q, the declared id %q", want, declared.ImportID)
			}
		})
	}
	// Not ARM ids: a type with no one-component row still declines, and
	// sameImportID keeps a case-sensitive id case-sensitive.
	if _, got := configIdentityMismatch("azurerm_route_table", identity.Resolution{ImportID: "rt-new"}, claimant{importID: "rt-old"}); got {
		t.Errorf("two non-ARM ids of a type with no one-component row were compared")
	}
	if sameImportID("Bucket", "bucket") {
		t.Errorf("sameImportID folded the case of a non-ARM id")
	}
	if !sameImportID(renameRTID(renameOldRT), strings.ToLower(renameRTID(renameOldRT))) {
		t.Errorf("sameImportID did not fold the case of an ARM id")
	}
}

// TestRenameBind_azureControlAtItsOwnID is the control: an object whose ARM
// id IS the declared one but whose marker names an address the
// configuration no longer declares binds by id (fileHit's declaredID
// branch) and vouches for nothing.
func TestRenameBind_azureControlAtItsOwnID(t *testing.T) {
	cfg := azConfigWith(t, renameRTConfig)
	hit := azsweep.Resource{
		ID:             renameRTID(renameNewRT),
		Type:           "microsoft.network/routetables",
		Name:           renameNewRT,
		ResourceGroup:  "kept",
		SubscriptionID: rgSub1,
		Tags: map[string]string{
			markers.TagEstate:  rgEstate,
			markers.TagAddress: markers.EscapeAddress("azurerm_route_table.gone"),
		},
	}
	graph := &fakeGraph{answers: map[string][]azsweep.Resource{rgSub1: {hit}}}
	leg := ResourceGraphSweep{Coverage: azsweep.Coverage{rgSub1: graph}, Subscriptions: []string{rgSub1}}
	res, diags := discoverAzureWith(t, cfg, leg, keptResolution(rgSub1), aksResolution("azurerm_route_table", "rt", renameRTID(renameNewRT)))
	assertNoErrors(t, diags)
	if len(res.Orphans) != 0 || len(res.Problems) != 0 {
		t.Errorf("orphans %+v, problems %+v; want none", res.Orphans, res.Problems)
	}
	if res.MarkerVerified()[renameRTAdr] {
		t.Errorf("an object marked for another address vouched for %s", renameRTAdr)
	}
}

// TestRenameBind_awsNativeScanReportsDisplaced: the AWS fixture's
// aws_s3_bucket.data now names "tofu-live-e2e-data"; the bucket the last
// apply made under the old name still carries aws_s3_bucket.data. On the
// per-type scan (discovery.go) the old bucket is reported as a
// DISPLACED_MARKER warning, is not an orphan, and does not vouch for the
// address, so the projection reads the tags of whatever sits at the new
// name. Run once with nothing at the new name and once with an untagged
// bucket there.
func TestRenameBind_awsNativeScanReportsDisplaced(t *testing.T) {
	const old = "tofu-live-e2e-data-old"
	for name, newTagged := range map[string]bool{"nothing at the new name": false, "untagged bucket at the new name": true} {
		t.Run(name, func(t *testing.T) {
			cloud := newFakeCloud()
			ownWholeEstate(cloud)
			cloud.listable("aws_s3_bucket")
			cloud.own("aws_s3_bucket", old, `aws_s3_bucket.data`)
			if newTagged {
				cloud.obj("aws_s3_bucket", "tofu-live-e2e-data", map[string]string{"team": "other"})
			}
			res, diags := discoverFixture(t, cloud, Request{Sweep: true})
			assertNoErrors(t, diags)
			findDisplaced(t, res, old)
			assertNotAnOrphan(t, res, old)
			if res.MarkerVerified()[`aws_s3_bucket.data`] {
				t.Errorf("the old bucket's sighting vouched for aws_s3_bucket.data: %v", res.VerifiedDeclared)
			}
		})
	}
}

// TestRenameBind_awsTaggingSweepReportsDisplaced is the same rename
// through the Resource Groups Tagging API leg, which files its candidate
// through the very function the Azure leg uses ([fileCandidate], then
// displacedFrom). The claimant differs from Azure's: aws_s3_bucket has a
// one-component identity row, so the table fallback compares
// "tofu-live-e2e-data-old" with "tofu-live-e2e-data", exactly, and answers
// displaced, as it did before #2136.
func TestRenameBind_awsTaggingSweepReportsDisplaced(t *testing.T) {
	const old = "tofu-live-e2e-data-old"
	if ti, ok := identity.LookupType("aws_s3_bucket"); !ok || len(ti.Components) != 1 {
		t.Fatalf("aws_s3_bucket's identity row is not one-component (%+v, %v); the fallback this pin rests on has moved", ti, ok)
	}
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	arn := "arn:aws:s3:::" + old
	srv := &taggingServer{
		arns: []string{arn},
		tags: map[string]map[string]string{arn: {TagEstate: estateName, TagAddress: `aws_s3_bucket.data`}},
	}
	server := srv.start(t)
	defer server.Close()
	res, diags := discoverFixture(t, cloud, Request{
		Sweep:        true,
		Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: server.URL}),
		TaggingSweep: true,
		Roster:       taggingRoster(t, "aws_s3_bucket", "AWS::S3::Bucket", true),
	})
	assertNoErrors(t, diags)
	if srv.calls == 0 {
		t.Fatal("the tagging sweep was never called")
	}
	findDisplaced(t, res, old)
	assertNotAnOrphan(t, res, old)
	if res.MarkerVerified()[`aws_s3_bucket.data`] {
		t.Errorf("the old bucket's sighting vouched for aws_s3_bucket.data: %v", res.VerifiedDeclared)
	}
}

// TestRenameBind_kubernetesJoinsByNaturalKey: a ConfigMap renamed
// cfg-old -> cfg-new. The Kubernetes leg vouches only by natural key
// (kubernetes.go, vouchesDeclared) and binds by annotation only when the
// declared key is not listed (kubernetes_address.go, addressCandidate).
// With cfg-new listed, cfg-old is an orphan (stock's plan for a renamed
// object outside create_before_destroy is a replace; the old object's
// destroy is half of it). With cfg-new absent, cfg-old is rebound to the
// address at its own id (#1541), so the projection reads cfg-old, not
// whatever might sit at cfg-new. Neither shape vouches for the address on
// the strength of an object at another key.
func TestRenameBind_kubernetesJoinsByNaturalKey(t *testing.T) {
	cfg := k8sInstance(t, "kubernetes_config_map_v1", "cfg")
	for name, newListed := range map[string]bool{"cfg-new listed": true, "cfg-new absent": false} {
		t.Run(name, func(t *testing.T) {
			objs := []kubesweep.Object{labelled("ConfigMap", "ns", "cfg-old", markers.EscapeAddress(cfg.String()))}
			if newListed {
				objs = append(objs, labelled("ConfigMap", "ns", "cfg-new", ""))
			}
			sweeper := &stubSweeper{kinds: []kubesweep.Kind{configMapKind()}, objects: map[string][]kubesweep.Object{"ConfigMap": objs}}
			req := Request{
				Estate:   "m1116",
				Sweepers: []Sweeper{KubernetesSweep{Client: sweeper, Types: k8sTypes(), ManifestType: "kubernetes_manifest"}},
				Resolutions: []identity.Resolution{
					{Addr: cfg, Class: identity.ClassConcrete, ImportID: "ns/cfg-new", IdentityValues: map[string]string{"name": "cfg-new", "namespace": "ns"}},
				},
			}
			res := &Result{}
			res.Resolutions = append(res.Resolutions, req.Resolutions...)
			if diags := sweepKubernetes(context.Background(), req, res); diags.HasErrors() {
				t.Fatalf("unexpected errors: %s", diags.Err())
			}
			r, _ := resolutionAt(res, cfg.String())
			orphaned := len(res.Orphans) == 1 && res.Orphans[0].ImportID == "ns/cfg-old"
			if newListed {
				if !orphaned || r.ImportID != "ns/cfg-new" {
					t.Errorf("orphans %+v, resolution %+v; want cfg-old an orphan and the address read at ns/cfg-new", res.Orphans, r)
				}
				return
			}
			if len(res.Orphans) != 0 || r.ImportID != "ns/cfg-old" {
				t.Errorf("orphans %+v, resolution %+v; want cfg-old rebound to %s at its own id", res.Orphans, r, cfg)
			}
		})
	}
}
