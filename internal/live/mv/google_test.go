// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #1119: live-mv cannot rewrite a GCP resource's labels yet,
// and says so by name rather than taking the tag path.
func TestMove_GoogleLabelsRefusedByName(t *testing.T) {
	m := cty.Map(cty.String)
	schema := providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"name":             {Type: cty.String, Required: true},
		"labels":           {Type: m, Optional: true},
		"terraform_labels": {Type: m, Computed: true},
		"effective_labels": {Type: m, Computed: true},
	}}}
	p := &tofu.MockProvider{GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
		Provider:      providers.Schema{Block: &configschema.Block{}},
		ResourceTypes: map[string]providers.Schema{"google_storage_bucket": schema},
	}}
	p.ConfigureProviderCalled = true

	dir := t.TempDir()
	writeFile(t, dir, "main.tf", `
terraform {
  required_providers {
    google = { source = "hashicorp/google" }
  }
}
resource "google_storage_bucket" "renamed" { name = "logs" }
`)
	old := mustAddr(t, "google_storage_bucket.logs")
	renamed := mustAddr(t, "google_storage_bucket.renamed")
	providerAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("google")}
	req := Request{
		Estate:      "prod-net",
		Old:         old,
		New:         renamed,
		Config:      loadConfigDir(t, dir),
		Resolutions: []identity.Resolution{{Addr: renamed, Class: identity.ClassConcrete, ImportID: "p/logs"}},
		Providers:   projection.SingleProvider(providerAddr, p),
	}
	_, diags := Move(t.Context(), req)
	if !diags.HasErrors() || !strings.Contains(diags.Err().Error(), SummaryGCPMoveNotBuilt) {
		t.Fatalf("diags = %v, want %q", diags.Err(), SummaryGCPMoveNotBuilt)
	}
	if p.PlanResourceChangeCalled || p.ApplyResourceChangeCalled {
		t.Error("the refused move planned or applied")
	}
}
