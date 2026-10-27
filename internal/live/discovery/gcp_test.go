// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/pkg/gcpsweep"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// GitHub issue #1119: the GCP leg over a fake Cloud Asset Inventory.

type fakeAssetSearcher struct {
	assets map[string][]gcpsweep.Asset
	errs   map[string]error
}

func (f fakeAssetSearcher) Search(_ context.Context, project, _ string) ([]gcpsweep.Asset, error) {
	if err := f.errs[project]; err != nil {
		return nil, err
	}
	return f.assets[project], nil
}

func gcpResolution(t *testing.T, addr, importID string) identity.Resolution {
	t.Helper()
	a, diags := addrs.ParseAbsResourceInstanceStr(addr)
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	return identity.Resolution{Addr: a, Class: identity.ClassConcrete, ImportID: importID}
}

func TestGCPAssetSweep(t *testing.T) {
	declared := gcpResolution(t, "google_storage_bucket.logs", "proj-b/logs")
	estate := "prod-net"
	searcher := fakeAssetSearcher{
		assets: map[string][]gcpsweep.Asset{
			"proj-a": {
				{AssetType: "storage.googleapis.com/Bucket", Name: "//storage.googleapis.com/logs",
					Labels: map[string]string{markers.TagEstate: estate, markers.TagAddress: markers.GoogleAddressValue(declared.Addr)}},
				{AssetType: "compute.googleapis.com/Instance", Name: "//compute.googleapis.com/projects/proj-a/zones/z/instances/gke-node-1",
					Labels: map[string]string{markers.TagEstate: estate, "goog-gke-node": ""}},
				{AssetType: "pubsub.googleapis.com/Topic", Name: "//pubsub.googleapis.com/projects/proj-a/topics/old",
					Labels: map[string]string{markers.TagEstate: estate, markers.TagAddress: "aaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbbbbbbbbbbbb"}},
			},
		},
		errs: map[string]error{"proj-b": &gcpsweep.ServiceDisabledError{Project: "proj-b"}},
	}
	req := Request{Estate: estate, Resolutions: []identity.Resolution{
		declared,
		gcpResolution(t, "google_pubsub_topic.events", "projects/proj-b/topics/events"),
	}}
	res := &Result{}
	diags := GCPAssetSweep{Searcher: searcher, Project: "proj-a"}.sweep(context.Background(), req, nil, res)

	if len(res.ControllerHeld) != 1 || !strings.Contains(res.ControllerHeld[0].ImportID, "gke-node-1") || res.ControllerHeld[0].Controller != "GKE" {
		t.Errorf("controller-held = %+v, want the GKE node alone", res.ControllerHeld)
	}
	if len(res.Orphans) != 1 {
		t.Fatalf("orphans = %+v, want the topic alone: the declared bucket and the held node are neither", res.Orphans)
	}
	o := res.Orphans[0]
	if !strings.Contains(o.ImportID, "topics/old") || o.Removal || o.Withheld == "" || o.Addressable {
		t.Errorf("orphan = %+v, want the topic, reported with a reason and never planned for removal", o)
	}

	var incomplete bool
	for _, d := range diags {
		if d.Description().Summary == SummaryGCPSweepIncomplete && strings.Contains(d.Description().Detail, "proj-b") {
			incomplete = true
		}
	}
	if !incomplete {
		t.Errorf("no %q warning naming proj-b: %v", SummaryGCPSweepIncomplete, diags.Err())
	}
	if diags.HasErrors() {
		t.Errorf("a gap in coverage became an error: %v", diags.Err())
	}
	if len(res.SweepGaps) != 1 || !strings.Contains(res.SweepGaps[0].Detail, "proj-b") {
		t.Errorf("sweep gaps = %+v, want one for proj-b", res.SweepGaps)
	}
}

func TestGCPSweepProjects(t *testing.T) {
	got := GCPSweepProjects("proj-z", []identity.Resolution{
		gcpResolution(t, "google_pubsub_topic.a", "projects/proj-a/topics/a"),
		gcpResolution(t, "google_pubsub_topic.b", "projects/proj-a/topics/b"),
		gcpResolution(t, "google_compute_network.n", "projects/proj-b/global/networks/n"),
		gcpResolution(t, "aws_s3_bucket.x", "projects/not-gcp/x"),
		{Addr: gcpResolution(t, "google_pubsub_topic.c", "").Addr, Class: identity.ClassNeedsDiscovery, ImportID: "projects/proj-c/topics/c"},
	})
	if want := []string{"proj-a", "proj-b", "proj-z"}; !reflect.DeepEqual(got, want) {
		t.Errorf("GCPSweepProjects = %v, want %v", got, want)
	}
}

// TestGCPSweepProjectsInReadsNeedsDiscoveryProjects: a labelled google
// type whose name the API assigns resolves needs-discovery with no import
// id, so [GCPSweepProjects] could not see the project its block names. A
// block in proj-b under a proj-a provider was never searched, came back
// unbound, and planned a second create of an object that exists. Its
// `project` argument is configuration, read here with each.value bound; a
// project another resource's output names is not known until apply and is
// left to the provider's.
func TestGCPSweepProjectsInReadsNeedsDiscoveryProjects(t *testing.T) {
	dir := t.TempDir()
	src := `
provider "google" { project = "proj-a" }

resource "google_recaptcha_enterprise_key" "site" {
  display_name = "site"
  project      = "proj-b"
}

resource "google_recaptcha_enterprise_key" "per" {
  for_each     = { one = "proj-c" }
  display_name = each.key
  project      = each.value
}

resource "google_recaptcha_enterprise_key" "later" {
  display_name = "later"
  project      = google_project.p.project_id
}

resource "google_project" "p" {
  name       = "p"
  project_id = "proj-d"
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig(t, dir)
	needs := func(addr string) identity.Resolution {
		r := gcpResolution(t, addr, "")
		r.Class = identity.ClassNeedsDiscovery
		return r
	}
	resolutions := []identity.Resolution{
		needs("google_recaptcha_enterprise_key.site"),
		needs(`google_recaptcha_enterprise_key.per["one"]`),
		needs("google_recaptcha_enterprise_key.later"),
	}
	if got, want := GCPSweepProjects("proj-a", resolutions), []string{"proj-a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("GCPSweepProjects = %v, want %v: the import-id read alone sees no project for a needs-discovery instance", got, want)
	}
	got := GCPSweepProjectsIn(context.Background(), cfg, "proj-a", resolutions)
	if want := []string{"proj-a", "proj-b", "proj-c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("GCPSweepProjectsIn = %v, want %v", got, want)
	}
}

// TestUnlabelledGoogleTypesAreSweepGaps (GitHub issue #2119, D3): a declared
// google type the leg was told is unlabelled files one TYPE_NOT_TAGGABLE
// gap, however many instances declare it; a labelled one files none.
func TestUnlabelledGoogleTypesAreSweepGaps(t *testing.T) {
	res := &Result{}
	req := Request{Estate: "prod", Resolutions: []identity.Resolution{
		{Addr: mustGoogleAddr(t, "google_storage_bucket.logs"), Class: identity.ClassConcrete},
		{Addr: mustGoogleAddr(t, "google_compute_subnetwork.edge"), Class: identity.ClassConcrete},
		{Addr: mustGoogleAddr(t, "google_compute_subnetwork.core"), Class: identity.ClassConcrete},
	}}
	unlabelledNotSwept(req, map[string]bool{"google_compute_subnetwork": true, "google_compute_network": true}, res)
	if len(res.SweepGaps) != 1 || res.SweepGaps[0].TypeName != "google_compute_subnetwork" || res.SweepGaps[0].Reason != SweepGapNotTaggable {
		t.Fatalf("SweepGaps = %+v; want one TYPE_NOT_TAGGABLE gap, for google_compute_subnetwork", res.SweepGaps)
	}
}

func mustGoogleAddr(t *testing.T, s string) addrs.AbsResourceInstance {
	t.Helper()
	a, diags := addrs.ParseAbsResourceInstanceStr(s)
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	return a
}
