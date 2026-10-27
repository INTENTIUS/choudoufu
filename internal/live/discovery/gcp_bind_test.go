// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/pkg/gcpsweep"
)

// TestGCPLegBindsByLabelAndSlot is INTENTIUS/choudoufu#2134 items 4 and 6
// through Discover: the GCP leg binds a server-named google instance by
// its tofu-address label, and a count set of them by tofu-slot the way the
// AWS tag sweep's sets bind, so the slot table the stamp writes from holds
// the slots the live set carries.
//
// keys has count 2 and three live members: one labelled keys[0] with slot
// 0, one whose label names an index the block no longer has (keys[4]) with
// slot 3, and one labelled keys[1] with slot 9. The slot decides: the two
// lowest bind indices 0 and 1, the stale label included, and slot 9 is
// surplus at keys[2], whatever its label says.
func TestGCPLegBindsByLabelAndSlot(t *testing.T) {
	const estate = "gcp-slots"
	cfg := loadConfig(t, filepath.Join("testdata", "gcp-count-slots"))
	resolutions := resolveOrFail(t, cfg).All()
	for _, r := range resolutions {
		if r.Class != identity.ClassNeedsDiscovery {
			t.Fatalf("%s resolved %s; the fixture needs every instance server-named", r.Addr, r.Class)
		}
	}

	hit := func(addr, id, slot string) gcpsweep.Asset {
		labels := map[string]string{markers.TagEstate: estate, markers.TagAddress: markers.GoogleAddressValue(mustAddr(t, addr))}
		if slot != "" {
			labels[markers.TagSlot] = slot
		}
		return gcpsweep.Asset{AssetType: "aiplatform.googleapis.com/IndexEndpoint",
			Name: "//aiplatform.googleapis.com/projects/proj-a/locations/us-central1/indexEndpoints/" + id, Labels: labels}
	}
	searcher := fakeAssetSearcher{assets: map[string][]gcpsweep.Asset{"proj-a": {
		hit("google_vertex_ai_index_endpoint.site", "site-key", ""),
		hit("google_vertex_ai_index_endpoint.keys[0]", "k-a", "0"),
		hit("google_vertex_ai_index_endpoint.keys[4]", "k-b", "3"),
		hit("google_vertex_ai_index_endpoint.keys[1]", "k-c", "9"),
	}}}

	res, diags := Discover(context.Background(), Request{
		Estate:      estate,
		Config:      cfg,
		Resolutions: resolutions,
		Provider:    newFakeCloud(),
		Sweepers:    []Sweeper{GCPAssetSweep{Searcher: searcher, Project: "proj-a", Labelled: map[string]bool{"google_vertex_ai_index_endpoint": true}}},
	})
	assertNoErrors(t, diags)

	for addr, want := range map[string]string{
		"google_vertex_ai_index_endpoint.site":    "projects/proj-a/locations/us-central1/indexEndpoints/site-key",
		"google_vertex_ai_index_endpoint.keys[0]": "projects/proj-a/locations/us-central1/indexEndpoints/k-a",
		"google_vertex_ai_index_endpoint.keys[1]": "projects/proj-a/locations/us-central1/indexEndpoints/k-b",
	} {
		b, ok := res.BindingFor(mustAddr(t, addr))
		if !ok {
			t.Errorf("%s is not bound:\n%s", addr, res)
			continue
		}
		if b.ImportID != want {
			t.Errorf("%s bound to %q, want %q", addr, b.ImportID, want)
		}
	}
	if b, _ := res.BindingFor(mustAddr(t, "google_vertex_ai_index_endpoint.keys[1]")); !b.SlotBound || !b.AddressStale {
		t.Errorf("keys[1] binding = %+v; want bound by slot, its stale address label to be rewritten", b)
	}
	if len(res.Surplus) != 1 || res.Surplus[0].Addr.String() != "google_vertex_ai_index_endpoint.keys[2]" || res.Surplus[0].ImportID != "projects/proj-a/locations/us-central1/indexEndpoints/k-c" {
		t.Errorf("surplus = %+v; want k-c at keys[2]", res.Surplus)
	}
	table := res.SlotTable()
	for addr, want := range map[string]string{
		"google_vertex_ai_index_endpoint.keys[0]": "0",
		"google_vertex_ai_index_endpoint.keys[1]": "3",
	} {
		if got := table[EscapeAddress(addr)]; got != want {
			t.Errorf("slot table[%s] = %q, want %q (table %v)", addr, got, want, table)
		}
	}
	if _, ok := table[EscapeAddress("google_vertex_ai_index_endpoint.site")]; ok {
		t.Errorf("slot table has an entry for site, which is no count member: %v", table)
	}
	for _, o := range res.Orphans {
		t.Errorf("orphan %+v; every hit names a declared instance or a member of a slotted set", o)
	}
}

// TestGCPLegRefusesWhatItCouldNotSearch: a project the search could not
// reach leaves every claimed type unscanned and refused by name, never
// unbound, because an unbound instance plans a create of something that may
// exist.
func TestGCPLegRefusesWhatItCouldNotSearch(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "gcp-count-slots"))
	res, diags := Discover(context.Background(), Request{
		Estate:      "gcp-slots",
		Config:      cfg,
		Resolutions: resolveOrFail(t, cfg).All(),
		Provider:    newFakeCloud(),
		Sweepers: []Sweeper{GCPAssetSweep{Project: "proj-a", Labelled: map[string]bool{"google_vertex_ai_index_endpoint": true}, Searcher: fakeAssetSearcher{
			errs: map[string]error{"proj-a": &gcpsweep.PermissionDeniedError{Project: "proj-a"}}}}},
	})
	if !diags.HasErrors() {
		t.Fatalf("an unsearched project produced no error:\n%s", res)
	}
	if len(res.Unbound) != 0 || len(res.Bindings) != 0 {
		t.Errorf("unbound %v, bindings %v; want neither, the type was not searched", res.Unbound, res.Bindings)
	}
	refused := false
	for _, p := range res.Problems {
		refused = refused || (p.Kind == ProblemTypeNotListable && p.TypeName == "google_vertex_ai_index_endpoint")
	}
	if !refused {
		t.Errorf("no %s problem for google_vertex_ai_index_endpoint:\n%s", ProblemTypeNotListable, res)
	}
}

// TestScansDeclaredFailsClosed (GitHub issue #2139, item 8): with no schema
// read the leg claims no type, so the config-driven scan refuses what it
// cannot list instead of leaving it to a search that never binds it.
func TestScansDeclaredFailsClosed(t *testing.T) {
	searcher := fakeAssetSearcher{}
	if (GCPAssetSweep{Searcher: searcher}).ScansDeclared("google_recaptcha_enterprise_key") {
		t.Error("with no schema read, the leg claimed a google type")
	}
	if !(GCPAssetSweep{Searcher: searcher, Labelled: map[string]bool{"google_vertex_ai_index_endpoint": true}}).ScansDeclared("google_vertex_ai_index_endpoint") {
		t.Error("the leg did not claim a type its schema says is labelled")
	}
	if (GCPAssetSweep{Labelled: map[string]bool{"google_vertex_ai_index_endpoint": true}}).ScansDeclared("google_recaptcha_enterprise_key") {
		t.Error("a leg with no searcher claimed a type it cannot search")
	}
}
