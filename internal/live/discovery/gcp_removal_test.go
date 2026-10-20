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
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/listclient"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #2134, item 1: a deleted google block is destroyed from its
// record. The GCP pass turns the record-orphan leg on (Request.Sweep), and
// that leg, which never reads the cache, proposes the removal of both a
// labelled type (the Asset search would also have seen it) and an
// unlabelled one (nothing else can).
func TestRecordLegRemovesDeletedGoogleBlocks(t *testing.T) {
	ctx := context.Background()
	const estate = "prod"
	googleProv := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("google")}
	m := cty.Map(cty.String)
	schemas, diags := listclient.ListSchemas(ctx, schemaOnlyProvider{types: map[string]providers.Schema{
		"google_compute_subnetwork": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id": {Type: cty.String, Computed: true}, "name": {Type: cty.String, Required: true},
		}}},
		// A second type marking project optional-for-import and Computed,
		// which is what makes project provider context to the identity
		// schema rule rather than a value one type alone defaults.
		"google_pubsub_topic": {
			Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
				"id": {Type: cty.String, Computed: true}, "name": {Type: cty.String, Required: true},
				"project": {Type: cty.String, Optional: true, Computed: true},
			}},
			IdentitySchema: &configschema.Object{Nesting: configschema.NestingSingle, Attributes: map[string]*configschema.Attribute{
				"name":    {Type: cty.String, Required: true},
				"project": {Type: cty.String, Optional: true},
			}},
			IdentitySchemaVersion: 1,
		},
		// google_storage_bucket at 8.6.0: labelled, no ratified row, and a
		// resource identity schema of name and project.
		"google_storage_bucket": {
			Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
				"id": {Type: cty.String, Computed: true}, "name": {Type: cty.String, Required: true},
				"project": {Type: cty.String, Optional: true, Computed: true},
				"labels":  {Type: m, Optional: true}, "effective_labels": {Type: m, Computed: true},
			}},
			IdentitySchema: &configschema.Object{Nesting: configschema.NestingSingle, Attributes: map[string]*configschema.Attribute{
				"name":    {Type: cty.String, Required: true},
				"project": {Type: cty.String, Optional: true},
			}},
			IdentitySchemaVersion: 1,
		},
	}})
	if diags.HasErrors() {
		t.Fatalf("schemas: %s", diags.Err())
	}

	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	store := projection.NewRecordEnvelopeStore(raw, projection.RecordKeyPrefix(estate))
	subnet := mustAddr(t, "google_compute_subnetwork.edge")
	bucket := mustAddr(t, "google_storage_bucket.logs")
	for _, seed := range []struct {
		addr addrs.AbsResourceInstance
		id   string
	}{
		{subnet, "projects/p/regions/us-east1/subnetworks/edge"},
		{bucket, "logs"},
	} {
		if _, err := projection.SeedLocatedForInstance(ctx, store, seed.addr, googleProv, projection.LocatedRecord{ImportID: seed.id}); err != nil {
			t.Fatalf("seeding %s: %s", seed.addr, err)
		}
	}

	res := &Result{Estate: estate}
	req := Request{Estate: estate, HintStore: raw, ScopeProvider: googleProv, VouchProvider: googleProv, Sweep: true}
	if d := recordOrphanReadSweep(ctx, req, schemas, res); d.HasErrors() {
		t.Fatalf("record leg: %s", d.Err())
	}
	got := map[string]bool{}
	for _, r := range res.Resolutions {
		if r.Undeclared {
			got[r.Addr.String()] = true
		}
	}
	if !got[subnet.String()] || !got[bucket.String()] || len(got) != 2 {
		t.Fatalf("the record leg proposed %v, want the removals of %s and %s", res.Resolutions, subnet, bucket)
	}
}

// TestGCPHitTheRecordRemovesIsNotAlsoWithheld: once the record leg
// proposes a destroy, the Asset search's withheld report of the same
// labelled object is dropped, and an unrelated hit is kept.
func TestGCPHitTheRecordRemovesIsNotAlsoWithheld(t *testing.T) {
	bucket := mustAddr(t, "google_storage_bucket.logs")
	res := &Result{Orphans: []OwnedResource{
		{Addr: bucket, Removal: true},
		{TypeName: "storage.googleapis.com/Bucket", Marker: markers.GoogleAddressValue(bucket), Withheld: "reported"},
		{TypeName: "pubsub.googleapis.com/Topic", Marker: "aaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbbbbbbbbbbbb", Withheld: "reported"},
	}}
	dropGCPHitsTheRecordsRemove(res)
	if len(res.Orphans) != 2 || res.Orphans[1].TypeName != "pubsub.googleapis.com/Topic" {
		t.Fatalf("orphans after the drop = %+v; want the record's removal and the unrelated topic", res.Orphans)
	}
}

// TestGCPAddressCollisionIsRefusedByName (#2134 item 3): two instances, or
// two blocks, that encode to one label value are refused, each by name.
func TestGCPAddressCollisionIsRefusedByName(t *testing.T) {
	a, b := mustAddr(t, "google_storage_bucket.a"), mustAddr(t, "google_storage_bucket.b")
	res := []identity.Resolution{{Addr: a}, {Addr: b}}
	same := func(addrs.AbsResourceInstance) string { return "same" }
	_, diags := declaredGoogleAddressesWith(res, same, markers.GoogleBlockValue)
	if !diags.HasErrors() || diags[0].Description().Summary != SummaryGCPAddressCollision ||
		!strings.Contains(diags[0].Description().Detail, a.String()) || !strings.Contains(diags[0].Description().Detail, b.String()) {
		t.Fatalf("an instance collision gave %v; want %q naming both addresses", diags.Err(), SummaryGCPAddressCollision)
	}
	sameBlock := func(addrs.AbsResource) string { return "same" }
	if _, diags := declaredGoogleAddressesWith(res, markers.GoogleAddressValue, sameBlock); !diags.HasErrors() {
		t.Fatal("a block-part collision was not refused")
	}
	two := []identity.Resolution{{Addr: mustAddr(t, "google_storage_bucket.a[0]")}, {Addr: mustAddr(t, "google_storage_bucket.a[1]")}}
	if _, diags := declaredGoogleAddresses(two); diags.HasErrors() {
		t.Fatalf("two members of one block share a block part by design, and were refused: %s", diags.Err())
	}
}

// TestGCPLegWithNoClientStillFilesGaps (#2134 item 2): with no searcher
// the leg searches nothing, and the unlabelled types it was told about are
// still TYPE_NOT_TAGGABLE gaps.
func TestGCPLegWithNoClientStillFilesGaps(t *testing.T) {
	in := &SweepInput{Request: Request{Estate: "prod", Resolutions: []identity.Resolution{{Addr: mustAddr(t, "google_compute_network.main")}}}, Result: &Result{}}
	diags := GCPAssetSweep{Unlabelled: map[string]bool{"google_compute_network": true}}.Sweep(context.Background(), in)
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	if len(in.Result.SweepGaps) != 1 || in.Result.SweepGaps[0].Reason != SweepGapNotTaggable {
		t.Fatalf("SweepGaps = %+v; want google_compute_network's TYPE_NOT_TAGGABLE gap", in.Result.SweepGaps)
	}
}

// TestGCPRenamedObjectDoesNotVouch (GitHub issue #2134 item 13, the GCP
// side of #2136): a configuration renames a network from rt-old to rt-new,
// and the old network still wears the address label. Its full resource
// name, not the label, says which object it is, so it is a displaced
// claimant and not the new name's object; one whose path carries every
// configured value is.
func TestGCPRenamedObjectDoesNotVouch(t *testing.T) {
	res := identity.Resolution{
		Addr:           mustAddr(t, "google_compute_network.main"),
		Class:          identity.ClassConcrete,
		ImportID:       "projects/p/global/networks/rt-new",
		IdentityValues: map[string]string{"project": "p", "name": "rt-new"},
	}
	old := claimant{importID: "projects/p/global/networks/rt-old", resourcePath: "projects/p/global/networks/rt-old"}
	if want, mismatched := configIdentityMismatch("google_compute_network", res, old); !mismatched || want != "name=rt-new" {
		t.Errorf("the renamed-away network vouched: mismatch=%v want=%q", mismatched, want)
	}
	same := claimant{importID: "projects/p/global/networks/rt-new", resourcePath: "projects/p/global/networks/rt-new"}
	if want, mismatched := configIdentityMismatch("google_compute_network", res, same); mismatched {
		t.Errorf("the network at the configured name was refused, want %q", want)
	}
	otherProject := claimant{resourcePath: "projects/q/global/networks/rt-new"}
	if _, mismatched := configIdentityMismatch("google_compute_network", res, otherProject); !mismatched {
		t.Error("a same-named network in another project vouched")
	}
	bucketRes := identity.Resolution{Addr: mustAddr(t, "google_storage_bucket.logs"), IdentityValues: map[string]string{"name": "logs-new"}}
	if _, mismatched := configIdentityMismatch("google_storage_bucket", bucketRes, claimant{resourcePath: "projects/_/buckets/logs-old"}); !mismatched {
		t.Error("a renamed bucket vouched")
	}
}
