// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"errors"
	"path/filepath"
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
