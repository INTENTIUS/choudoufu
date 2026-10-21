// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/listclient"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
)

// TestRecordLegImportsGoogleRecordsByIdentity is INTENTIUS/choudoufu#2145
// item 4. A google record that composes no import ID is proposed for
// removal by its identity, where the type's importer at hashicorp/google
// 8.6.0 takes one (identity.GoogleIdentityImports, read off the source by
// tools/gcp-row-gen), one case per importer shape:
//
//   - parse_import_id, rowed: google_scc_v2_folder_mute_config's row has a
//     location component with a schema Default, which the composer will
//     not render, so its record composed nothing.
//   - parse_import_id, markerless: google_scc_source's row has no
//     components (the provider mints the name), so its record composed
//     the empty string and was proposed with no identity at all.
//   - importer: google_sql_user has no row and no import form made of its
//     identity, and its importer has its own identity branch.
//   - iam_parent: google_service_directory_namespace_iam_member has no row,
//     and the IAM framework's parent identity parser reads its identity.
//
// google_apigee_endpoint_attachment's importer needs name, which its
// identity does not carry, and google_compute_backend_service_signed_url_key
// has no importer: both stay refused by name.
func TestRecordLegImportsGoogleRecordsByIdentity(t *testing.T) {
	ctx := context.Background()
	const estate = "prod"
	googleProv := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("google")}
	str := func(req bool) *configschema.Attribute {
		return &configschema.Attribute{Type: cty.String, Required: req, Optional: !req, Computed: !req}
	}
	idAttr := func(req bool) *configschema.Attribute {
		return &configschema.Attribute{Type: cty.String, Required: req, Optional: !req}
	}
	// typ builds a schema whose block and identity carry the same
	// attributes, required as given.
	typ := func(attrs map[string]bool) providers.Schema {
		block := map[string]*configschema.Attribute{"id": {Type: cty.String, Optional: true, Computed: true}}
		ident := map[string]*configschema.Attribute{}
		for name, req := range attrs {
			block[name] = str(req)
			ident[name] = idAttr(req)
		}
		return providers.Schema{
			Block:                 &configschema.Block{Attributes: block},
			IdentitySchema:        &configschema.Object{Nesting: configschema.NestingSingle, Attributes: ident},
			IdentitySchemaVersion: 1,
		}
	}
	schemas, diags := listclient.ListSchemas(ctx, schemaOnlyProvider{types: map[string]providers.Schema{
		"google_scc_v2_folder_mute_config":              typ(map[string]bool{"folder": true, "mute_config_id": true, "location": false}),
		"google_scc_source":                             typ(map[string]bool{"organization": true, "name": true}),
		"google_sql_user":                               typ(map[string]bool{"instance": true, "name": true, "host": false, "project": false}),
		"google_service_directory_namespace_iam_member": typ(map[string]bool{"name": true, "role": true, "member": true, "condition_title": false}),
		"google_apigee_endpoint_attachment":             typ(map[string]bool{"org_id": true, "endpoint_attachment_id": true}),
		"google_compute_backend_service_signed_url_key": typ(map[string]bool{"backend_service": true, "name": true, "project": false}),
		// A second type marking project optional for import and computed,
		// which is what makes project provider context to the identity
		// schema rule that admits an unrowed type.
		"google_pubsub_topic": typ(map[string]bool{"name": true, "project": false}),
	}})
	if diags.HasErrors() {
		t.Fatalf("schemas: %s", diags.Err())
	}

	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	store := projection.NewRecordEnvelopeStore(raw, projection.RecordKeyPrefix(estate))
	recovered := map[string]map[string]string{
		"google_scc_v2_folder_mute_config.m":              {"folder": "123", "mute_config_id": "quiet", "location": "global"},
		"google_scc_source.s":                             {"organization": "456", "name": "organizations/456/sources/789"},
		"google_sql_user.u":                               {"instance": "db", "name": "app", "project": "p"},
		"google_service_directory_namespace_iam_member.i": {"name": "projects/p/locations/us/namespaces/ns", "role": "roles/viewer", "member": "user:a@example.com"},
	}
	refused := map[string]map[string]string{
		"google_apigee_endpoint_attachment.a":             {"org_id": "organizations/o", "endpoint_attachment_id": "ea"},
		"google_compute_backend_service_signed_url_key.k": {"backend_service": "web", "name": "key", "project": "p"},
	}
	for _, seeds := range []map[string]map[string]string{recovered, refused} {
		for addr, components := range seeds {
			if _, err := projection.SeedLocatedForInstance(ctx, store, mustAddr(t, addr), googleProv, projection.LocatedRecord{Components: components}); err != nil {
				t.Fatalf("seeding %s: %s", addr, err)
			}
		}
	}

	res := &Result{Estate: estate}
	req := Request{Estate: estate, HintStore: raw, ScopeProvider: googleProv, VouchProvider: googleProv, Sweep: true}
	diags = recordOrphanReadSweep(ctx, req, schemas, res)

	got := map[string]identity.Resolution{}
	for _, r := range res.Resolutions {
		if r.Undeclared {
			got[r.Addr.String()] = r
		}
	}
	for addr, components := range recovered {
		r, ok := got[addr]
		if !ok {
			t.Errorf("%s was not proposed for removal", addr)
			continue
		}
		if r.ImportID != "" || !reflect.DeepEqual(r.IdentityValues, components) {
			t.Errorf("%s was proposed with import ID %q and identity %v; want no import ID and identity %v", addr, r.ImportID, r.IdentityValues, components)
		}
	}
	if len(got) != len(recovered) {
		t.Errorf("proposed %d removals, want %d: %v", len(got), len(recovered), got)
	}

	var names []string
	for _, p := range res.ProblemsOfKind(ProblemUncomposableRecord) {
		names = append(names, p.Addr.String())
	}
	sort.Strings(names)
	want := []string{"google_apigee_endpoint_attachment.a", "google_compute_backend_service_signed_url_key.k"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("refused %v, want %v", names, want)
	}
	if !diags.HasErrors() {
		t.Error("the two refusals raised no error")
	}
}

// TestRecordLegIdentityImportNeedsEveryRequiredAttribute: a record missing
// an attribute its importer needs to take the identity is refused by name,
// never imported with that segment empty. google_scc_v2_folder_mute_config's
// importer reads location out of its first regex; a record without it would
// import folders/123/locations//muteConfigs/quiet.
func TestRecordLegIdentityImportNeedsEveryRequiredAttribute(t *testing.T) {
	if _, ok := identity.GoogleIdentityImportValues("google_scc_v2_folder_mute_config", map[string]string{"folder": "123", "mute_config_id": "quiet"}); ok {
		t.Fatal("a mute config record with no location was accepted for identity import")
	}
	if _, ok := identity.GoogleIdentityImportValues("google_scc_v2_folder_mute_config", map[string]string{"folder": "123", "mute_config_id": "quiet", "location": "global"}); !ok {
		t.Fatal("a whole mute config record was refused for identity import")
	}
	if _, ok := identity.GoogleIdentityImportValues("google_apigee_endpoint_attachment", map[string]string{"org_id": "organizations/o", "endpoint_attachment_id": "ea"}); ok {
		t.Fatal("google_apigee_endpoint_attachment was accepted for identity import, though its importer reads name")
	}
}
