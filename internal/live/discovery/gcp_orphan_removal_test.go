// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/pkg/gcpsweep"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/listclient"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/pkg/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// TestRemovedGoogleBlockAfterCBDReplaceIsDestroyed is the fifth GCP run's
// day2_replace on corpus-k8s-infra-gcp-gcve, through Discover. A scratch
// google_storage_bucket.cbd was renamed under create_before_destroy
// (gcve-cbd-a to gcve-cbd-b), so the record for the block names gcve-cbd-b,
// and the stage then deleted the block. The Asset search finds gcve-cbd-b
// wearing the estate's label and the deleted address's hashed label.
//
// The record leg must propose its destroy at google_storage_bucket.cbd,
// stock's own address for it, and nothing may refuse: the run instead
// raised "Malformed ownership marker" for the hit, because orphan
// classification read the GCP hit as an escaped AWS marker (its Normalized
// value is empty, so it printed tofu-address "") before the record leg had
// run. The removal apply failed, cbd-b leaked, and every later stage
// failed on the same refusal.
func TestRemovedGoogleBlockAfterCBDReplaceIsDestroyed(t *testing.T) {
	ctx := context.Background()
	const estate = "corpus-k8s-infra-gcp-gcve"
	const project = "broadcom-451918"
	googleProv := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("google")}

	m := cty.Map(cty.String)
	bucketSchema := providers.Schema{
		Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id": {Type: cty.String, Computed: true}, "name": {Type: cty.String, Required: true},
			"project":  {Type: cty.String, Optional: true, Computed: true},
			"location": {Type: cty.String, Required: true},
			"labels":   {Type: m, Optional: true}, "effective_labels": {Type: m, Computed: true},
		}},
		IdentitySchema: &configschema.Object{Nesting: configschema.NestingSingle, Attributes: map[string]*configschema.Attribute{
			"name":    {Type: cty.String, Required: true},
			"project": {Type: cty.String, Optional: true},
		}},
		IdentitySchemaVersion: 1,
	}
	prov := schemaOnlyProvider{types: map[string]providers.Schema{
		"google_storage_bucket": bucketSchema,
		// The identity-schema rule corroborates project as provider
		// context against a second type, as in the other record-leg tests.
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
	}}
	if _, has := identity.LookupType("google_storage_bucket"); has {
		t.Fatal("google_storage_bucket has a ratified row now; this test pins the identity-schema route the run took")
	}

	// The root after the stage deleted day2_replace.tf: the block is gone.
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
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig(t, dir)
	resolutions := resolveOrFail(t, cfg).All()

	// The record the replace apply left: write-back's own shape for the new
	// bucket, read off the object it applied.
	cbd := mustAddr(t, "google_storage_bucket.cbd")
	newName := project + "-gcve-cbd-b"
	rec, ok := projection.LocatedRecordFrom("google_storage_bucket", bucketSchema, cty.ObjectVal(map[string]cty.Value{
		"id": cty.StringVal(newName), "name": cty.StringVal(newName), "project": cty.StringVal(project),
		"location": cty.StringVal("US"), "labels": cty.NullVal(m), "effective_labels": cty.NullVal(m),
	}))
	if !ok {
		t.Fatal("write-back records nothing for the bucket; the test needs the record the replace apply wrote")
	}
	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	store := projection.NewRecordEnvelopeStore(raw, projection.RecordKeyPrefix(estate))
	if _, err := projection.SeedLocatedForInstance(ctx, store, cbd, googleProv, rec); err != nil {
		t.Fatalf("seeding %s: %s", cbd, err)
	}

	label := markers.GoogleAddressValue(cbd)
	hit := gcpsweep.Asset{
		AssetType: "storage.googleapis.com/Bucket",
		Name:      "//storage.googleapis.com/" + newName,
		Labels:    map[string]string{markers.TagEstate: estate, markers.TagAddress: label},
	}
	discover := func(hint *staterecord.LocalStore) (*Result, tfdiags.Diagnostics) {
		return Discover(ctx, Request{
			Estate:        estate,
			Config:        cfg,
			Resolutions:   resolutions,
			Provider:      prov,
			HintStore:     hint,
			VouchProvider: googleProv,
			Sweep:         true,
			Sweepers: []Sweeper{GCPAssetSweep{
				Searcher: fakeAssetSearcher{assets: map[string][]gcpsweep.Asset{project: {hit}}},
				Project:  project,
				Labelled: map[string]bool{"google_storage_bucket": true},
			}},
		})
	}
	res, diags := discover(raw)
	for _, d := range diags {
		desc := d.Description()
		if desc.Summary == "Malformed ownership marker" {
			t.Errorf("the GCP hit was refused as a malformed AWS marker: %s", desc.Detail)
		}
	}
	assertNoErrors(t, diags)

	var removals []string
	for _, r := range res.Resolutions {
		if r.Undeclared {
			removals = append(removals, r.Addr.String())
		}
	}
	if len(removals) != 1 || removals[0] != cbd.String() {
		t.Fatalf("removals = %v; want exactly stock's destroy of %s", removals, cbd)
	}
	if hasDiag(diags, SummaryGCPOrphanUnaddressed, newName) {
		t.Errorf("the bucket the record destroys is also warned about as left alone:\n%s", renderDiags(diags))
	}
	for _, o := range res.Orphans {
		if !o.Removal && strings.Contains(o.ImportID, newName) {
			t.Errorf("the bucket the record destroys is still reported as left alone: %+v", o)
		}
	}

	// With no record for the block (an empty store), nothing names the
	// address the hash encodes: the hit is reported by name, nothing is
	// destroyed, and nothing is refused as malformed.
	empty, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	res, diags = discover(empty)
	assertNoErrors(t, diags)
	if !hasDiag(diags, SummaryGCPOrphanUnaddressed, newName) {
		t.Errorf("with no record the hit was not reported by name: %v", diags)
	}
	for _, r := range res.Resolutions {
		if r.Undeclared {
			t.Errorf("with no record, %s was proposed for removal", r.Addr)
		}
	}
}

// TestGCPOrphanIsNotReadAsAnEscapedMarker: orphan classification decodes
// an AWS or Kubernetes marker by the escaping rule. A GCP hit carries a
// one-way hash, matched and never decoded, and the GCP leg has already
// said what it is, so classification leaves it alone: no "Malformed
// ownership marker" for it, and its withheld reason and marker kept for
// the record leg to read.
func TestGCPOrphanIsNotReadAsAnEscapedMarker(t *testing.T) {
	cbd := mustAddr(t, "google_storage_bucket.cbd")
	const why = "reported by the GCP leg"
	res := &Result{Orphans: []OwnedResource{{
		TypeName:  "storage.googleapis.com/Bucket",
		ImportID:  "//storage.googleapis.com/p-cbd-b",
		Marker:    markers.GoogleAddressValue(cbd),
		Swept:     true,
		LabelHash: true,
		Withheld:  why,
	}}}
	diags := classifyOrphans(context.Background(), Request{Estate: "e"}, listclient.Schemas{}, res)
	if diags.HasErrors() || len(res.Problems) != 0 {
		t.Fatalf("classification refused a GCP hit: %v %+v", diags.Err(), res.Problems)
	}
	if got := res.Orphans[0]; got.Withheld != why || got.Marker != markers.GoogleAddressValue(cbd) || got.Removal {
		t.Fatalf("classification rewrote the GCP hit: %+v", got)
	}
}
