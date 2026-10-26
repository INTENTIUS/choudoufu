// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/gcpsweep"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// fakeTypeLister is a [gcpsweep.TypeLister] serving fixed objects per type
// and scope, recording every scope it is asked about.
type fakeTypeLister struct {
	assets map[gcpsweep.ListScope][]gcpsweep.Asset
	err    error
	asked  *[]gcpsweep.ListScope
}

func (f fakeTypeLister) ListType(_ context.Context, _ string, scope gcpsweep.ListScope, _ string) ([]gcpsweep.Asset, error) {
	if f.asked != nil {
		*f.asked = append(*f.asked, scope)
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.assets[scope], nil
}

// TestGCPTypeListNamesTheCollisionTheIndexMissed is INTENTIUS/choudoufu#2264:
// two applies each created an index endpoint for google_vertex_ai_index_endpoint.site,
// and the Asset Inventory index has caught up with only one of them. The
// type's own list shows both, so the plan names the collision rather than
// binding the one the index holds. With the list failing, the leg binds
// from the index and warns that the collision check did not cover the type.
func TestGCPTypeListNamesTheCollisionTheIndexMissed(t *testing.T) {
	const estate = "gcp-slots"
	cfg := loadConfig(t, filepath.Join("testdata", "gcp-count-slots"))
	resolutions := resolveOrFail(t, cfg).All()

	hit := func(addr, id string) gcpsweep.Asset {
		return gcpsweep.Asset{AssetType: "aiplatform.googleapis.com/IndexEndpoint",
			Name:   "//aiplatform.googleapis.com/projects/proj-a/locations/us-central1/indexEndpoints/" + id,
			Labels: map[string]string{markers.TagEstate: estate, markers.TagAddress: markers.GoogleAddressValue(mustAddr(t, addr))}}
	}
	indexed := fakeAssetSearcher{assets: map[string][]gcpsweep.Asset{"proj-a": {hit("google_vertex_ai_index_endpoint.site", "first")}}}
	scope := gcpsweep.ListScope{Project: "proj-a", Location: "us-central1"}
	labelled := map[string]bool{"google_vertex_ai_index_endpoint": true}

	t.Run("the list sees both", func(t *testing.T) {
		var asked []gcpsweep.ListScope
		lister := fakeTypeLister{asked: &asked, assets: map[gcpsweep.ListScope][]gcpsweep.Asset{scope: {
			hit("google_vertex_ai_index_endpoint.site", "first"),
			hit("google_vertex_ai_index_endpoint.site", "second"),
		}}}
		res, _ := Discover(context.Background(), Request{
			Estate: estate, Config: cfg, Resolutions: resolutions, Provider: newFakeCloud(),
			Sweepers: []Sweeper{GCPAssetSweep{Searcher: indexed, Lister: lister, Project: "proj-a", Labelled: labelled}},
		})
		if len(asked) != 1 || asked[0] != scope {
			t.Errorf("listed at %v, want once at %v (every instance names proj-a and us-central1)", asked, scope)
		}
		collided := false
		for _, p := range res.Problems {
			collided = collided || p.Kind == ProblemCollision
		}
		if !collided {
			t.Errorf("no %s problem for site's two objects:\n%s", ProblemCollision, res)
		}
		if b, ok := res.BindingFor(mustAddr(t, "google_vertex_ai_index_endpoint.site")); ok {
			t.Errorf("site bound to %q; two objects claim it, so it must not bind", b.ImportID)
		}
	})

	t.Run("the list fails", func(t *testing.T) {
		lister := fakeTypeLister{err: &gcpsweep.PermissionDeniedError{Project: "proj-a", Permission: "aiplatform.indexEndpoints.list", Err: errors.New("denied")}}
		res, diags := Discover(context.Background(), Request{
			Estate: estate, Config: cfg, Resolutions: resolutions, Provider: newFakeCloud(),
			Sweepers: []Sweeper{GCPAssetSweep{Searcher: indexed, Lister: lister, Project: "proj-a", Labelled: labelled}},
		})
		warned := false
		for _, d := range diags {
			warned = warned || d.Description().Summary == SummaryGCPTypeListIncomplete
		}
		if !warned {
			t.Errorf("no %q warning when the type's list failed: %v", SummaryGCPTypeListIncomplete, diags.ErrWithWarnings())
		}
		b, ok := res.BindingFor(mustAddr(t, "google_vertex_ai_index_endpoint.site"))
		if !ok || b.ImportID != "projects/proj-a/locations/us-central1/indexEndpoints/first" {
			t.Errorf("site binding = %+v, %v; want the indexed object, as without a lister", b, ok)
		}
	})
}

// TestGCPDerivedTypeScopes: a derived type (one listed by its Asset name's
// parent collection, INTENTIUS/choudoufu#2264 after INTENTIUS/plunge#15) is
// listed where its block puts it. A zone given as a self link counts by its
// last segment, and a block naming none takes the provider block's. A parent
// written out is the path's prefix; one that refers to another resource, or a
// location the block leaves to the provider, cannot be known from
// configuration, and the type is bound from the index with a warning.
func TestGCPDerivedTypeScopes(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "gcp-derived-scope"))
	g := GCPAssetSweep{Project: "proj-a", Region: "us-central1", Zone: "us-central1-f"}
	req := Request{Config: cfg}
	scope := func(addr, typeName string) (gcpsweep.ListScope, string) {
		t.Helper()
		tl, ok := gcpsweep.Covered(typeName)
		if !ok || !tl.Derived {
			t.Fatalf("%s is not a derived covered type", typeName)
		}
		return g.derivedScope(context.Background(), req, mustAddr(t, addr), tl)
	}
	for _, tc := range []struct {
		addr, typeName string
		want           gcpsweep.ListScope
	}{
		{"google_compute_disk.data", "google_compute_disk", gcpsweep.ListScope{Project: "proj-b", Location: "us-east1-b", Collection: "projects/proj-b/zones/us-east1-b/disks"}},
		{"google_compute_disk.boot", "google_compute_disk", gcpsweep.ListScope{Project: "proj-a", Location: "us-central1-f", Collection: "projects/proj-a/zones/us-central1-f/disks"}},
		{"google_kms_crypto_key.pinned", "google_kms_crypto_key", gcpsweep.ListScope{Project: "proj-a", Collection: "projects/proj-a/locations/global/keyRings/kr/cryptoKeys"}},
		{"google_storage_bucket.logs", "google_storage_bucket", gcpsweep.ListScope{Project: "proj-a", Collection: "b", Query: "project=proj-a"}},
	} {
		got, why := scope(tc.addr, tc.typeName)
		if why != "" || got != tc.want {
			t.Errorf("%s: scope %+v (%q), want %+v", tc.addr, got, why, tc.want)
		}
	}
	for _, tc := range []struct{ addr, typeName, says string }{
		{"google_kms_crypto_key.referenced", "google_kms_crypto_key", "key_ring is not known from configuration alone"},
		{"google_artifact_registry_repository.unlocated", "google_artifact_registry_repository", "location"},
	} {
		if _, why := scope(tc.addr, tc.typeName); !strings.Contains(why, tc.says) {
			t.Errorf("%s: why = %q, want it to say %q", tc.addr, why, tc.says)
		}
	}

	// An Asset hit of another type wearing a listed type's address label is
	// not the list's to drop: a disk's list does not answer for an instance.
	disk, _ := gcpsweep.Covered("google_compute_disk")
	if disk.Names(gcpsweep.Asset{AssetType: "compute.googleapis.com/Instance", Name: "//compute.googleapis.com/projects/proj-a/zones/us-central1-f/instances/vm"}) {
		t.Error("a disk's list claimed an instance hit")
	}
}
