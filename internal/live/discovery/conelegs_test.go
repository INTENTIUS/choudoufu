// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/intentius/choudoufu/internal/live/azsweep"
	"github.com/intentius/choudoufu/internal/live/gcpsweep"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/registry"
)

// coneProgressRecorder records every progress event a pass reports.
type coneProgressRecorder struct {
	mu     sync.Mutex
	events []ProgressEvent
}

func (r *coneProgressRecorder) record(ev ProgressEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *coneProgressRecorder) sweeps() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, ev := range r.events {
		if ev.Sweep {
			n++
		}
	}
	return n
}

// fakeAddressSearcher is a Cloud Asset searcher that answers address
// searches and counts the estate-wide ones it is asked.
type fakeAddressSearcher struct {
	assets map[string][]gcpsweep.Asset

	mu        sync.Mutex
	searches  int
	addresses []string
}

func (f *fakeAddressSearcher) Search(_ context.Context, project, _ string) ([]gcpsweep.Asset, error) {
	f.mu.Lock()
	f.searches++
	f.mu.Unlock()
	return f.assets[project], nil
}

func (f *fakeAddressSearcher) SearchAddress(_ context.Context, project, estate, address string) ([]gcpsweep.Asset, error) {
	f.mu.Lock()
	f.addresses = append(f.addresses, address)
	f.mu.Unlock()
	var out []gcpsweep.Asset
	for _, a := range f.assets[project] {
		if a.Labels[markers.TagEstate] == estate && a.Labels[markers.TagAddress] == address {
			out = append(out, a)
		}
	}
	return out, nil
}

// TestGCPConeLegBindsByAddressSearchAlone is GitHub issue #2260's GCP leg:
// with Cone set, a server-named google instance is found by a search for
// its own tofu-address label, never by the estate-wide label search, and
// nothing else the project holds is filed as an orphan or reported as a
// sweep.
func TestGCPConeLegBindsByAddressSearchAlone(t *testing.T) {
	const estate = "gcp-slots"
	cfg := loadConfig(t, filepath.Join("testdata", "gcp-count-slots"))
	resolutions := resolveOrFail(t, cfg).All()

	hit := func(addr, id string) gcpsweep.Asset {
		return gcpsweep.Asset{AssetType: "aiplatform.googleapis.com/IndexEndpoint",
			Name:   "//aiplatform.googleapis.com/projects/proj-a/locations/us-central1/indexEndpoints/" + id,
			Labels: map[string]string{markers.TagEstate: estate, markers.TagAddress: markers.GoogleAddressValue(mustAddr(t, addr))}}
	}
	searcher := &fakeAddressSearcher{assets: map[string][]gcpsweep.Asset{"proj-a": {
		hit("google_vertex_ai_index_endpoint.site", "site-key"),
		// A deleted block's endpoint: the estate search would file it as an
		// orphan; the cone never asks for it.
		hit("google_vertex_ai_index_endpoint.gone", "gone-key"),
	}}}
	progress := &coneProgressRecorder{}

	res, diags := Discover(context.Background(), Request{
		Estate:      estate,
		Config:      cfg,
		Resolutions: resolutions,
		Provider:    newFakeCloud(),
		Progress:    progress.record,
		Sweepers: []Sweeper{GCPAssetSweep{Searcher: searcher, Project: "proj-a",
			Labelled: map[string]bool{"google_vertex_ai_index_endpoint": true},
			Cone:     map[string]bool{"google_vertex_ai_index_endpoint": true}}},
	})
	assertNoErrors(t, diags)

	b, ok := res.BindingFor(mustAddr(t, "google_vertex_ai_index_endpoint.site"))
	if !ok || b.ImportID != "projects/proj-a/locations/us-central1/indexEndpoints/site-key" {
		t.Errorf("site binding = %+v (bound %v), want site-key:\n%s", b, ok, res)
	}
	if searcher.searches != 0 {
		t.Errorf("the cone leg ran %d estate-wide search(es); it searches by address only", searcher.searches)
	}
	if len(searcher.addresses) == 0 {
		t.Error("the cone leg made no address search")
	}
	if len(res.Orphans) != 0 {
		t.Errorf("orphans %+v; the cone files none", res.Orphans)
	}
	if n := progress.sweeps(); n != 0 {
		t.Errorf("%d progress event(s) reported a sweep; a cone listing is a scan", n)
	}
	for _, s := range res.Scans {
		if s.Sweep {
			t.Errorf("scan %+v is marked a sweep", s)
		}
	}
}

// fakeTypeGraph is a Resource Graph client that answers the type-narrowed
// query and counts the estate-wide one.
type fakeTypeGraph struct {
	answers map[string][]azsweep.Resource

	mu       sync.Mutex
	searches int
	types    [][]string
}

func (f *fakeTypeGraph) Search(_ context.Context, subscription, _ string) ([]azsweep.Resource, error) {
	f.mu.Lock()
	f.searches++
	f.mu.Unlock()
	return f.answers[subscription], nil
}

func (f *fakeTypeGraph) SearchTypes(_ context.Context, subscription, _ string, armTypes []string) ([]azsweep.Resource, error) {
	f.mu.Lock()
	f.types = append(f.types, append([]string(nil), armTypes...))
	f.mu.Unlock()
	return f.answers[subscription], nil
}

// TestResourceGraphConeLegNarrowsByType is GitHub issue #2260's Azure leg:
// with Cone set, the leg sends the type-narrowed query, never the estate
// query, binds the cone's needs-discovery instance from what comes back, and
// files nothing else as an orphan.
func TestResourceGraphConeLegNarrowsByType(t *testing.T) {
	graph := &fakeTypeGraph{answers: map[string][]azsweep.Resource{rgSub1: {
		rgResource(rgSub1, "Kept", "azurerm_resource_group.kept", nil),
		rgResource(rgSub1, "Gone", "azurerm_resource_group.gone", nil),
	}}}
	leg := ResourceGraphSweep{
		Coverage:      azsweep.Coverage{rgSub1: graph},
		Subscriptions: []string{rgSub1},
		Cone:          map[string]bool{"azurerm_resource_group": true},
	}
	progress := &coneProgressRecorder{}
	needs := identity.Resolution{Addr: azInstance("kept"), Class: identity.ClassNeedsDiscovery}
	res, diags := Discover(context.Background(), Request{
		Estate:      rgEstate,
		Config:      azConfig(t),
		Provider:    newFakeCloud(),
		Resolutions: []identity.Resolution{needs},
		Progress:    progress.record,
		Sweepers:    []Sweeper{leg},
	})
	assertNoErrors(t, diags)

	if graph.searches != 0 {
		t.Errorf("the cone leg sent the estate query %d time(s)", graph.searches)
	}
	if len(graph.types) != 1 || len(graph.types[0]) != 1 || !strings.Contains(graph.types[0][0], "resource") {
		t.Errorf("type-narrowed queries %v, want one, for the resource group's ARM type", graph.types)
	}
	if b, ok := res.BindingFor(azInstance("kept")); !ok || !strings.EqualFold(b.ImportID, rgID(rgSub1, "Kept")) {
		t.Errorf("kept binding = %+v (bound %v), want the Kept group's ARM id:\n%s", b, ok, res)
	}
	if len(res.Orphans) != 0 {
		t.Errorf("orphans %+v; the cone files none", res.Orphans)
	}
	if n := progress.sweeps(); n != 0 {
		t.Errorf("%d progress event(s) reported a sweep; a cone listing is a scan", n)
	}
}

// TestTaggingTypeFiltersReadTheARNJoinBackwards: the cone's tag index asks
// for each type by the ARN service and resource-type segment the join table
// reads it from, and leaves out a type the table does not join.
func TestTaggingTypeFiltersReadTheARNJoinBackwards(t *testing.T) {
	roster, err := registry.Embedded()
	if err != nil {
		t.Fatalf("registry.Embedded: %s", err)
	}
	filters, unfiltered := TaggingTypeFilters(roster, []string{"aws_vpc", "aws_iam_role", "aws_s3_bucket", "aws_ssm_parameter"})
	if got, want := strings.Join(filters, ","), "ec2:vpc,iam:role,s3"; got != want {
		t.Errorf("filters = %q, want %q", got, want)
	}
	if got := strings.Join(unfiltered, ","); got != "aws_ssm_parameter" {
		t.Errorf("unfiltered = %q, want aws_ssm_parameter (the join table leaves ssm's parameter segment out)", got)
	}
}
