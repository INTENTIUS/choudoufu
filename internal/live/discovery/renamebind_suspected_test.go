// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/live/azsweep"
	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// SUSPECTED DEFECT, pinned as today's behaviour, not as a demand.
//
// A block renamed in configuration without a moved block's help is not the
// case here: the ADDRESS stays, the identity argument changes. The
// configuration used to name "rt-old" (or "b-old", or ConfigMap "cfg-old")
// and now names "rt-new". The object the last apply made still exists at
// the old name and still carries tofu-address = the unchanged address. The
// question each test below answers per platform: does the sweep treat that
// old object as the declared instance's own object (and vouch for the
// address, which lets the projection admit whatever sits at the NEW name
// without reading its tags), as an orphan, or as a displaced marker?
//
// The decision is [declared.displacedFrom] -> [configIdentityMismatch]
// (displaced.go): the per-attribute comparison needs an identity object on
// the listed claimant, and the fallback compares whole import ids only for a
// type with a one-component identity.DefaultTable row. Every other doubt
// answers verdictOwnObject, which vouches. An azurerm claimant from Resource
// Graph has no identity object and no azurerm type has a DefaultTable row,
// so the comparison never runs on Azure. The tests name the gap so a fix
// flips them.

const (
	renameOldRT = "rt-old"
	renameNewRT = "rt-new"
	renameRTAdr = "azurerm_route_table.rt"
)

func renameRTID(name string) string {
	return rgID(rgSub1, "Kept") + "/providers/Microsoft.Network/routeTables/" + name
}

// TestSuspectedRenameBind_azureOldObjectVouchesForTheAddress: the route
// table renamed rt-old -> rt-new in configuration. Resource Graph lists
// rt-old carrying this estate's marker for azurerm_route_table.rt. Today:
// no orphan, no problem (no displaced-marker warning), and the sighting of
// rt-old is filed in VerifiedDeclared for azurerm_route_table.rt - so
// MarkerVerified, which the command layer hands the projection as
// Ownership.Verified, vouches for the address whose object the projection
// will read at rt-new. The same holds whether or not the resolution carries
// identity-schema values: the Resource Graph claimant has no identity
// object to compare them with.
func TestSuspectedRenameBind_azureOldObjectVouchesForTheAddress(t *testing.T) {
	if ti, ok := identity.LookupType("azurerm_route_table"); ok && len(ti.Components) == 1 {
		t.Fatalf("azurerm_route_table now has a one-component identity row; configIdentityMismatch's fallback would compare ARM ids and this pin no longer describes the path")
	}
	cfg := azConfigWith(t, `
resource "azurerm_route_table" "rt" {
  name                = "rt-new"
  resource_group_name = "Kept"
  location            = "westeurope"
}
`)
	for name, values := range map[string]map[string]string{
		"no identity values": nil,
		"identity-schema values": {
			"name":                renameNewRT,
			"resource_group_name": "Kept",
			"subscription_id":     rgSub1,
		},
	} {
		t.Run(name, func(t *testing.T) {
			old := azsweep.Resource{
				ID:             renameRTID(renameOldRT),
				Type:           "microsoft.network/routetables",
				Name:           renameOldRT,
				ResourceGroup:  "kept",
				SubscriptionID: rgSub1,
				Tags: map[string]string{
					markers.TagEstate:  rgEstate,
					markers.TagAddress: markers.EscapeAddress(renameRTAdr),
				},
			}
			graph := &fakeGraph{answers: map[string][]azsweep.Resource{rgSub1: {old}}}
			leg := ResourceGraphSweep{Coverage: azsweep.Coverage{rgSub1: graph}, Subscriptions: []string{rgSub1}}
			rt := aksResolution("azurerm_route_table", "rt", renameRTID(renameNewRT))
			rt.IdentityValues = values

			res, diags := discoverAzureWith(t, cfg, leg, keptResolution(rgSub1), rt)
			assertNoErrors(t, diags)

			if len(res.Orphans) != 0 {
				t.Errorf("orphans %+v; this pin records that rt-old is NOT an orphan today", res.Orphans)
			}
			if len(res.Problems) != 0 {
				t.Errorf("problems %+v; this pin records that no displaced-marker warning is raised today", res.Problems)
			}
			if len(diags) != 0 {
				t.Errorf("diagnostics %s; this pin records a silent sweep", diags.ErrWithWarnings())
			}
			if !res.MarkerVerified()[renameRTAdr] {
				t.Errorf("MarkerVerified %v; this pin records that rt-old's sighting vouches for %s today", res.MarkerVerified(), renameRTAdr)
			}
		})
	}
}

// TestSuspectedRenameBind_azureControlAtItsOwnID is the control: an object
// whose ARM id IS the declared one but whose marker names an address the
// configuration no longer declares binds by id (fileHit's declaredID
// branch). That rule compares ids; the rename above has a marker for a
// DECLARED address and an id nobody declares, and fileHit never compares.
func TestSuspectedRenameBind_azureControlAtItsOwnID(t *testing.T) {
	cfg := azConfigWith(t, `
resource "azurerm_route_table" "rt" {
  name                = "rt-new"
  resource_group_name = "Kept"
  location            = "westeurope"
}
`)
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

// TestSuspectedRenameBind_awsNativeScanReportsDisplaced: the AWS fixture's
// aws_s3_bucket.data now names "tofu-live-e2e-data"; the bucket the last
// apply made under the old name still carries aws_s3_bucket.data. On the
// per-type scan (discovery.go) the old bucket is reported as a
// DISPLACED_MARKER warning, is not an orphan, and does not vouch for the
// address, so the projection reads the tags of whatever sits at the new
// name. Run once with nothing at the new name and once with an untagged
// bucket there.
func TestSuspectedRenameBind_awsNativeScanReportsDisplaced(t *testing.T) {
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

// TestSuspectedRenameBind_awsTaggingSweepReportsDisplaced is the same rename
// through the Resource Groups Tagging API leg, which files its candidate
// through the very function the Azure leg uses ([fileCandidate], then
// displacedFrom). The only difference from the Azure test is the claimant:
// aws_s3_bucket has a one-component identity row, so the import-id fallback
// compares "tofu-live-e2e-data-old" with "tofu-live-e2e-data" and answers
// displaced.
func TestSuspectedRenameBind_awsTaggingSweepReportsDisplaced(t *testing.T) {
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

// TestSuspectedRenameBind_kubernetesJoinsByNaturalKey: a ConfigMap renamed
// cfg-old -> cfg-new. The Kubernetes leg vouches only by natural key
// (kubernetes.go, vouchesDeclared) and binds by annotation only when the
// declared key is not listed (kubernetes_address.go, addressCandidate).
// With cfg-new listed, cfg-old is an orphan (stock's plan for a renamed
// object outside create_before_destroy is a replace; the old object's
// destroy is half of it). With cfg-new absent, cfg-old is rebound to the
// address at its own id (#1541), so the projection reads cfg-old, not
// whatever might sit at cfg-new. Neither shape vouches for the address on
// the strength of an object at another key.
func TestSuspectedRenameBind_kubernetesJoinsByNaturalKey(t *testing.T) {
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
