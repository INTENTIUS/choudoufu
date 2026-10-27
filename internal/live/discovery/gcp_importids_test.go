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
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/pkg/gcpsweep"
	"github.com/intentius/choudoufu/pkg/staterecord"
)

// TestRecordLegComposesGoogleCompositeRecords is INTENTIUS/choudoufu#2139
// item 3. A google type with no identity-table row whose identity schema
// requires two attributes is recorded as Components with no import ID.
// google_kms_crypto_key's record is composed by its importer's documented
// form, {{key_ring}}/cryptoKeys/{{name}}, and its removal is proposed.
// google_compute_backend_bucket_signed_url_key states no import form at
// 8.6.0, so its record is refused by name rather than skipped.
func TestRecordLegComposesGoogleCompositeRecords(t *testing.T) {
	ctx := context.Background()
	const estate = "prod"
	googleProv := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("google")}
	m := cty.Map(cty.String)
	str := func(req bool) *configschema.Attribute {
		return &configschema.Attribute{Type: cty.String, Required: req, Optional: !req, Computed: !req}
	}
	schemas, diags := listclient.ListSchemas(ctx, schemaOnlyProvider{types: map[string]providers.Schema{
		"google_kms_crypto_key": {
			Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
				"id": {Type: cty.String, Optional: true, Computed: true}, "key_ring": str(true), "name": str(true),
				"labels": {Type: m, Optional: true}, "effective_labels": {Type: m, Computed: true},
			}},
			IdentitySchema: &configschema.Object{Nesting: configschema.NestingSingle, Attributes: map[string]*configschema.Attribute{
				"key_ring": {Type: cty.String, Required: true}, "name": {Type: cty.String, Required: true},
			}},
			IdentitySchemaVersion: 1,
		},
		"google_compute_backend_bucket_signed_url_key": {
			Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
				"id": {Type: cty.String, Optional: true, Computed: true}, "backend_bucket": str(true), "name": str(true),
				"key_value": {Type: cty.String, Required: true, Sensitive: true}, "project": str(false),
			}},
			IdentitySchema: &configschema.Object{Nesting: configschema.NestingSingle, Attributes: map[string]*configschema.Attribute{
				"backend_bucket": {Type: cty.String, Required: true}, "name": {Type: cty.String, Required: true},
				"project": {Type: cty.String, Optional: true},
			}},
			IdentitySchemaVersion: 1,
		},
		// A second type marking project optional for import and computed,
		// which is what makes project provider context to the identity
		// schema rule.
		"google_pubsub_topic": {
			Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
				"id": {Type: cty.String, Computed: true}, "name": str(true), "project": str(false),
			}},
			IdentitySchema: &configschema.Object{Nesting: configschema.NestingSingle, Attributes: map[string]*configschema.Attribute{
				"name": {Type: cty.String, Required: true}, "project": {Type: cty.String, Optional: true},
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
	key := mustAddr(t, "google_kms_crypto_key.k")
	signed := mustAddr(t, "google_compute_backend_bucket_signed_url_key.s")
	for _, seed := range []struct {
		addr       addrs.AbsResourceInstance
		components map[string]string
	}{
		{key, map[string]string{"key_ring": "projects/p/locations/us/keyRings/ring", "name": "k"}},
		{signed, map[string]string{"backend_bucket": "assets", "name": "s", "project": "p"}},
	} {
		if _, err := projection.SeedLocatedForInstance(ctx, store, seed.addr, googleProv, projection.LocatedRecord{Components: seed.components}); err != nil {
			t.Fatalf("seeding %s: %s", seed.addr, err)
		}
	}

	// The key is labelled, so its record proposes a destroy only when the
	// Asset search saw it this run wearing the deleted address's label
	// (GitHub issue #2139, item 1).
	res := &Result{Estate: estate, Verdicts: Verdicts{Orphans: []OwnedResource{{TypeName: "cloudkms.googleapis.com/CryptoKey", Marker: markers.GoogleAddressValue(key), Withheld: "reported"}}}}
	req := Request{Estate: estate, HintStore: raw, ScopeProvider: googleProv, VouchProvider: googleProv, Sweep: true}
	diags = recordOrphanReadSweep(ctx, req, schemas, res)

	var proposed []identity.Resolution
	for _, r := range res.Resolutions {
		if r.Undeclared {
			proposed = append(proposed, r)
		}
	}
	if len(proposed) != 1 || proposed[0].Addr.String() != key.String() || proposed[0].ImportID != "projects/p/locations/us/keyRings/ring/cryptoKeys/k" {
		t.Fatalf("the record leg proposed %+v; want only %s, by projects/p/locations/us/keyRings/ring/cryptoKeys/k", proposed, key)
	}

	refused := res.ProblemsOfKind(ProblemUncomposableRecord)
	if len(refused) != 1 || refused[0].TypeName != "google_compute_backend_bucket_signed_url_key" || refused[0].Addr.String() != signed.String() {
		t.Fatalf("refusals = %+v; want one %s naming %s", refused, ProblemUncomposableRecord, signed)
	}
	if !diags.HasErrors() || diags[0].Description().Summary != problemSummaries[ProblemUncomposableRecord] {
		t.Fatalf("diagnostics = %v; want the %q error", diags.Err(), problemSummaries[ProblemUncomposableRecord])
	}
}

// TestGCPClaimantImportIDs is INTENTIUS/choudoufu#2139 item 7: a claimant's
// import ID comes from the generated table, never from the Asset name
// with its host stripped.
func TestGCPClaimantImportIDs(t *testing.T) {
	hit := func(assetType, name string) gcpsweep.Asset {
		return gcpsweep.Asset{AssetType: assetType, Name: name, Labels: map[string]string{markers.TagAddress: "addr"}}
	}

	// A compute disk's Asset path is its importer's first form.
	disk := gcpClaimant(hit("compute.googleapis.com/Disk", "//compute.googleapis.com/projects/p/zones/us-east1-b/disks/data"), "google_compute_disk", "addr", "addr")
	if disk.noIdentity || disk.importID != "projects/p/zones/us-east1-b/disks/data" {
		t.Errorf("compute disk claimant = %q (noIdentity %v); want its Asset path as it is", disk.importID, disk.noIdentity)
	}

	// A Dataproc workflow template's Asset path says regions/, its
	// importer locations/: the parts are moved into the accepted form.
	wf := gcpClaimant(hit("dataproc.googleapis.com/WorkflowTemplate", "//dataproc.googleapis.com/projects/p/regions/us-east1/workflowTemplates/nightly"), "google_dataproc_workflow_template", "addr", "addr")
	if wf.noIdentity || wf.importID != "projects/p/locations/us-east1/workflowTemplates/nightly" {
		t.Errorf("workflow template claimant = %q (noIdentity %v); want the rewritten form", wf.importID, wf.noIdentity)
	}

	// A disk name with another host is not the table's shape.
	if c := gcpClaimant(hit("compute.googleapis.com/Disk", "//storage.googleapis.com/projects/p/zones/z/disks/d"), "google_compute_disk", "addr", "addr"); !c.noIdentity {
		t.Errorf("a disk hit with the wrong host was given %q", c.importID)
	}

	// A compute instance's source states no Asset name; Google documents
	// //compute.googleapis.com/projects/PROJECT_ID/zones/ZONE/instances/INSTANCE,
	// which is its importer's first form.
	vm := gcpClaimant(hit("compute.googleapis.com/Instance", "//compute.googleapis.com/projects/p/zones/us-east1-b/instances/web"), "google_compute_instance", "addr", "addr")
	if vm.noIdentity || vm.importID != "projects/p/zones/us-east1-b/instances/web" {
		t.Errorf("compute instance claimant = %q (noIdentity %v); want its documented Asset path as it is", vm.importID, vm.noIdentity)
	}

	// Nor does a bucket's; Google documents //storage.googleapis.com/BUCKET,
	// and the bucket importer reads a one-part ID as the bucket's name.
	bucket := gcpClaimant(hit("storage.googleapis.com/Bucket", "//storage.googleapis.com/logs"), "google_storage_bucket", "addr", "addr")
	if bucket.noIdentity || bucket.importID != "logs" {
		t.Errorf("bucket claimant = %q (noIdentity %v); want the bucket name logs", bucket.importID, bucket.noIdentity)
	}

	// Any other spelling of a bucket is refused by name at binding. The
	// JSON API's projects/_/buckets/logs would reach the importer as project
	// "projects" and bucket "_", so it is not read as a bucket at all.
	for _, name := range []string{
		"//storage.googleapis.com/projects/_/buckets/logs",
		"//storage.googleapis.com/",
		"storage.googleapis.com/logs",
		"//compute.googleapis.com/logs",
	} {
		bucket := gcpClaimant(hit("storage.googleapis.com/Bucket", name), "google_storage_bucket", "addr", "addr")
		if !bucket.noIdentity || bucket.importID != "" {
			t.Errorf("bucket hit %s gave import ID %q; want it refused", name, bucket.importID)
			continue
		}
		res := &Result{}
		diag, refused := bindClaimant(res, map[string]Binding{}, "google_storage_bucket", "addr", mustAddr(t, "google_storage_bucket.logs"), bucket)
		if !refused || diag.Description().Summary != problemSummaries[ProblemNoIdentity] ||
			!strings.Contains(diag.Description().Detail, "is not of the form //storage.googleapis.com/{{bucket}}") || len(res.Bindings) != 0 {
			t.Errorf("bucket hit %s bound or was refused without saying why: %v", name, diag)
		}
	}
}
