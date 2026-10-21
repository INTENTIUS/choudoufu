// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// The seventh GCP run's reference-gcp day2_endpoints. hashicorp/google
// 8.6.0's region network endpoint group Read flattens region through
// ConvertSelfLinkToV1 and sets the identity's region from it, so the
// applied object carries a self link. choudoufu recorded that and imported
// the group by it, and the importer's
// projects/(?P<project>[^/]+)/regions/(?P<region>[^/]+)/... refused the id it
// built. The record, and the identity handed to the importer, must carry
// the bare region, which is what the provider's own id names.
func TestRegionNEGRecordsAndImportsTheBareRegion(t *testing.T) {
	const typeName = "google_compute_region_network_endpoint_group"
	const link = "https://www.googleapis.com/compute/v1/projects/chdf-refgcp/regions/us-central1"
	idAttrs := map[string]*configschema.Attribute{
		"name":    {Type: cty.String, Required: true},
		"project": {Type: cty.String, Optional: true},
		"region":  {Type: cty.String, Optional: true},
	}
	schema := providers.Schema{
		Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":      {Type: cty.String, Computed: true},
			"name":    {Type: cty.String, Required: true},
			"project": {Type: cty.String, Optional: true, Computed: true},
			"region":  {Type: cty.String, Required: true},
		}},
		IdentitySchema:        &configschema.Object{Nesting: configschema.NestingSingle, Attributes: idAttrs},
		IdentitySchemaVersion: 1,
	}
	obj := cty.ObjectVal(map[string]cty.Value{
		"id":      cty.StringVal("projects/chdf-refgcp/regions/us-central1/networkEndpointGroups/refgcp-rneg-ip"),
		"name":    cty.StringVal("refgcp-rneg-ip"),
		"project": cty.StringVal("chdf-refgcp"),
		"region":  cty.StringVal(link),
	})

	rec, ok := LocatedRecordFrom(typeName, schema, obj)
	if !ok {
		t.Fatal("no record for the applied group")
	}
	const wantID = "projects/chdf-refgcp/regions/us-central1/networkEndpointGroups/refgcp-rneg-ip"
	if rec.ImportID != wantID {
		t.Errorf("recorded import id = %q, want stock's %q", rec.ImportID, wantID)
	}
	if got, has := rec.Components["region"]; has && got != "us-central1" {
		t.Errorf("recorded region = %q, want the bare region the importer accepts", got)
	}

	// A record written before this fix still carries the link: the identity
	// handed to the importer must not.
	addr := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: typeName, Name: "ip"}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
	w := wanted{addr: addr, values: map[string]string{"name": "refgcp-rneg-ip", "project": "chdf-refgcp", "region": link}}
	target := importTarget(w, schema)
	if target.Identity == cty.NilVal || target.Identity.IsNull() {
		t.Fatalf("no identity import target: %+v", target)
	}
	if got := target.Identity.GetAttr("region").AsString(); got != "us-central1" {
		t.Errorf("imported by region %q, want the bare region", got)
	}
}
