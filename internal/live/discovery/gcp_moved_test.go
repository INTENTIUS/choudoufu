// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/pkg/gcpsweep"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
)

// TestGCPLegBindsAMovedLabelledInstanceByItsOldLabel is reference-gcp's
// day2_rename through Discover: google_pubsub_topic.events becomes .stream
// through a moved block, and the live topic still carries the label the
// stamp wrote for .events. A moved block legitimately leaves the old
// address on the object until the plan's label rewrite lands, so the GCP
// leg binds the hit to .stream (its pending move), as the AWS tag sweep
// files a declared instance under each moved-from address. It is neither
// an orphan nor unbound; unbound, the plan proposes creating .stream at a
// name the old topic holds.
func TestGCPLegBindsAMovedLabelledInstanceByItsOldLabel(t *testing.T) {
	const estate = "reference-gcp"
	const project = "chdf-refgcp"
	dir := t.TempDir()
	src := `terraform {
  required_providers {
    google = {
      source = "hashicorp/google"
    }
  }
}

provider "google" {
  project = "` + project + `"
}

resource "google_pubsub_topic" "stream" {
  project = "` + project + `"
  name    = "refgcp-events"
  labels = {
    tier = "events"
  }
}

moved {
  from = google_pubsub_topic.events
  to   = google_pubsub_topic.stream
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig(t, dir)
	resolutions := resolveOrFail(t, cfg).All()

	m := cty.Map(cty.String)
	prov := schemaOnlyProvider{types: map[string]providers.Schema{
		"google_pubsub_topic": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id": {Type: cty.String, Computed: true}, "name": {Type: cty.String, Required: true},
			"project": {Type: cty.String, Optional: true, Computed: true},
			"labels":  {Type: m, Optional: true}, "effective_labels": {Type: m, Computed: true},
		}}},
	}}

	events, stream := mustAddr(t, "google_pubsub_topic.events"), mustAddr(t, "google_pubsub_topic.stream")
	hit := gcpsweep.Asset{
		AssetType: "pubsub.googleapis.com/Topic",
		Name:      "//pubsub.googleapis.com/projects/" + project + "/topics/refgcp-events",
		Labels:    map[string]string{markers.TagEstate: estate, markers.TagAddress: markers.GoogleAddressValue(events), "tier": "events"},
	}
	res, diags := Discover(context.Background(), Request{
		Estate:      estate,
		Config:      cfg,
		Resolutions: resolutions,
		Provider:    prov,
		Sweep:       true,
		Sweepers: []Sweeper{GCPAssetSweep{
			Searcher: fakeAssetSearcher{assets: map[string][]gcpsweep.Asset{project: {hit}}},
			Project:  project,
			Labelled: map[string]bool{"google_pubsub_topic": true},
		}},
	})
	assertNoErrors(t, diags)
	if hasDiag(diags, SummaryGCPOrphanUnaddressed, "refgcp-events") {
		t.Errorf("the moved topic is reported as carrying no declared address:\n%s", renderDiags(diags))
	}
	for _, o := range res.Orphans {
		t.Errorf("orphan %+v; want the moved topic bound to %s", o, stream)
	}
	if !res.MarkerVerified()[stream.String()] {
		t.Errorf("%s is not marker-verified by its old label; verified = %v", stream, res.MarkerVerified())
	}
}
