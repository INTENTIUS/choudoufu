// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #1729, part 2. A root that used to configure helm and
// kubernetes drops the kubernetes provider and the kubernetes_config_map
// block together. The record store still holds the ConfigMap's identity,
// written under the kubernetes provider. The run now has one provider, so
// discovery's record-orphan leg is unscoped (#1715's skip applies only to a
// multi-provider root) and proposes the removal, and the projection reads
// it through the one provider the run has, helm. Before this test that
// failed with the raw schema error:
//
//	Provider provider["registry.opentofu.org/hashicorp/helm"] has no schema
//	for managed resource type "kubernetes_config_map", so a projection
//	cannot be built for it. This is either a configuration error that
//	validation should have caught or a provider bug.
//
// Skipping it would drop a removal without a word, so the run refuses by
// name: the recorded address and type, the provider it was recorded under,
// and what to add back.

func helmOnlyDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	const src = `
terraform {
  required_providers {
    helm = {
      source = "hashicorp/helm"
    }
  }
}

resource "helm_release" "app" {
  name  = "app"
  chart = "app"
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func helmOnlyProvider() *tofu.MockProvider {
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider: providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"helm_release": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
				"id":    {Type: cty.String, Computed: true},
				"name":  {Type: cty.String, Required: true},
				"chart": {Type: cty.String, Required: true},
			}}}},
		},
	}
	p.ConfigureProviderCalled = true
	return p
}

func TestRemovalOfATypeNoConfiguredProviderServesIsRefusedByName(t *testing.T) {
	ctx := context.Background()
	cfg := loadConfig(t, helmOnlyDir(t))
	helmProv := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("helm")}
	kubeProv := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("kubernetes")}

	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("quickpizza"))
	hashed := mustAddr(t, "kubernetes_config_map.hashed")
	if _, err := SeedLocatedForInstance(ctx, store, hashed, kubeProv, LocatedRecord{ImportID: "quickpizza/cfg-b"}); err != nil {
		t.Fatalf("seeding the record: %s", err)
	}

	_, diags := BuildWith(ctx, cfg, []identity.Resolution{{
		Addr:       hashed,
		Class:      identity.ClassConcrete,
		ImportID:   "quickpizza/cfg-b",
		Undeclared: true,
	}}, SingleProvider(helmProv, helmOnlyProvider()), Options{UndeclaredProvider: helmProv, RecordStore: store})

	var got tfdiags.Diagnostic
	for _, d := range diags {
		if d.Severity() == tfdiags.Error {
			got = d
			break
		}
	}
	if got == nil {
		t.Fatalf("the removal of %s was planned or dropped without an error; want the named refusal. Diagnostics: %s", hashed, diags.ErrWithWarnings())
	}
	desc := got.Description()
	const want = "No configured provider serves a removed resource"
	if desc.Summary != want {
		t.Fatalf("refusal summary is %q, want %q. Detail: %s", desc.Summary, want, desc.Detail)
	}
	for _, want := range []string{hashed.String(), `"kubernetes_config_map"`, kubeProv.String(), "Add that provider's configuration back"} {
		if !strings.Contains(desc.Detail, want) {
			t.Errorf("refusal detail does not name %q:\n%s", want, desc.Detail)
		}
	}
}
