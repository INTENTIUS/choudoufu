// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// frameworkNamespaceSchema is kubernetes_namespace_v1 exactly as
// hashicorp/kubernetes 3.3.0 serves it (`tofu providers schema -json`):
// its identity schema is byte-identical to 3.2.1's (api_version, kind and
// name, all required_for_import), and the one change is the resource
// block. The type moved to the plugin framework, so "metadata" is a list
// block whose bounds are undeclared (min_items and max_items both 0) and
// whose description says "Exactly one metadata block is required": the
// size limit is a validator, which the wire schema cannot carry. "id" is
// computed only, and wait_for_default_service_account gained computed.
func frameworkNamespaceSchema() providers.Schema {
	return providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":                               {Type: cty.String, Computed: true},
				"wait_for_default_service_account": {Type: cty.Bool, Optional: true, Computed: true},
			},
			BlockTypes: map[string]*configschema.NestedBlock{
				"metadata": {
					Nesting: configschema.NestingList,
					Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
						"annotations":      {Type: cty.Map(cty.String), Optional: true},
						"generate_name":    {Type: cty.String, Optional: true},
						"generation":       {Type: cty.Number, Computed: true},
						"labels":           {Type: cty.Map(cty.String), Optional: true},
						"name":             {Type: cty.String, Optional: true, Computed: true},
						"resource_version": {Type: cty.String, Computed: true},
						"uid":              {Type: cty.String, Computed: true},
					}},
				},
				"timeouts": {
					Nesting: configschema.NestingSingle,
					Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
						"delete": {Type: cty.String, Optional: true},
					}},
				},
			},
		},
		IdentitySchema: &configschema.Object{
			Nesting: configschema.NestingSingle,
			Attributes: map[string]*configschema.Attribute{
				"api_version": {Type: cty.String, Required: true},
				"kind":        {Type: cty.String, Required: true},
				"name":        {Type: cty.String, Required: true},
			},
		},
		IdentitySchemaVersion: 1,
	}
}

const quickpizzaNamespacePair = `
terraform {
  required_providers {
    kubernetes = { source = "hashicorp/kubernetes" }
  }
}

resource "kubernetes_namespace_v1" "quickpizza" {
  metadata {
    name = "quickpizza"
  }
}

resource "kubernetes_config_map_v1" "alloy_config" {
  metadata {
    name      = "alloy-config"
    namespace = kubernetes_namespace_v1.quickpizza.id
  }
  data = { k = "v" }
}
`

func resolveQuickpizzaPair(t *testing.T, ns providers.Schema) (map[string]Resolution, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(quickpizzaNamespacePair), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig(t, dir, nil)
	schemas := map[string]providers.Schema{
		"kubernetes_namespace_v1":  ns,
		"kubernetes_config_map_v1": objectMetaSchema(true),
	}
	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: schemas})
	got := map[string]Resolution{}
	for _, r := range result.All() {
		got[r.Addr.String()] = r
	}
	return got, diags.Err()
}

// TestObjectMetaRuleAdmitsUndeclaredBounds is corpus-quickpizza's
// 2026-10-02 test_plan refusal, reduced: under hashicorp/kubernetes 3.3.0
// the namespace fell past the object-metadata rule (which demanded
// max_items = 1) to the identity-schema route, which refuses it because
// api_version and kind are constants no configuration carries.
func TestObjectMetaRuleAdmitsUndeclaredBounds(t *testing.T) {
	got, err := resolveQuickpizzaPair(t, frameworkNamespaceSchema())
	if err != nil {
		t.Fatalf("resolution refused under 3.3.0's kubernetes_namespace_v1 schema: %s", err)
	}
	if r := got["kubernetes_namespace_v1.quickpizza"]; r.Class != ClassConcrete || r.ImportID != "quickpizza" {
		t.Errorf("kubernetes_namespace_v1.quickpizza resolved %s, want CONCRETE quickpizza", r.String())
	}
	if r := got["kubernetes_config_map_v1.alloy_config"]; r.Class != ClassConcrete || r.ImportID != "quickpizza/alloy-config" {
		t.Errorf("kubernetes_config_map_v1.alloy_config resolved %s, want CONCRETE quickpizza/alloy-config", r.String())
	}
}

// TestUndeclaredBoundsWithoutObjectMetaStillRefuses is the control: the
// same undeclared-bounds block, with no server-minted uid to say it names
// one object, is not object metadata, so it still reaches the
// identity-schema route, and that route still refuses because api_version
// and kind are genuinely unsupplied by this configuration.
func TestUndeclaredBoundsWithoutObjectMetaStillRefuses(t *testing.T) {
	s := frameworkNamespaceSchema()
	delete(s.Block.BlockTypes["metadata"].Block.Attributes, "uid")
	_, err := resolveQuickpizzaPair(t, s)
	if err == nil {
		t.Fatal("a metadata block with undeclared bounds and no uid was admitted")
	}
	if msg := err.Error(); !strings.Contains(msg, `requires "api_version", "kind", or "name"`) {
		t.Errorf("refused, but not by the identity-schema route's unsupplied-attribute wording: %s", msg)
	}
}
