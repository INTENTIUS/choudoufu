// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/armtypes"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// GitHub issue #2147: azapi 2.13.0 sends its provider block's default_tags
// only when the resource's own tags is null (tagsWithDefaultTags step 3,
// internal/services/azapi_resource.go lines 1570-1572), and never merges
// them, so a marker written into a null tags must carry the default tags
// with it or stock's plan loses them.

// defaultTagsFixture loads a configuration with one azapi_resource, vnet,
// under the default azapi provider block written as provider, with each
// root variable given the value vars names, the way a real run's -var and
// tfvars values reach the static evaluator (Meta.rootModuleCall,
// internal/command/meta_config.go). A variable vars does not name takes its
// default.
func defaultTagsFixture(t *testing.T, provider string, vars map[string]cty.Value) *NodeResolver {
	t.Helper()
	dir := t.TempDir()
	src := provider + `
provider "azapi" {
  alias = "plain"
}
resource "azapi_resource" "vnet" {
  type      = "Microsoft.Network/virtualNetworks@2023-09-01"
  name      = "vnet"
  parent_id = "/subscriptions/0/resourceGroups/rg"
}
resource "azapi_resource" "other" {
  provider  = azapi.plain
  type      = "Microsoft.Network/virtualNetworks@2023-09-01"
  name      = "other"
  parent_id = "/subscriptions/0/resourceGroups/rg"
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	parser := configs.NewParser(nil)
	call := configs.NewStaticModuleCall(addrs.RootModule, hcl.Range{},
		func(v *configs.Variable) (cty.Value, hcl.Diagnostics) {
			if val, ok := vars[v.Name]; ok {
				return val, nil
			}
			if v.Required() {
				// What Meta.rootModuleCall returns for a variable given
				// no value with input disabled.
				return cty.NilVal, hcl.Diagnostics{{Severity: hcl.DiagError, Summary: "No value for required variable"}}
			}
			return v.Default, nil
		}, dir, "default")
	mod, diags := parser.LoadConfigDir(dir)
	if diags.HasErrors() {
		t.Fatalf("loading %s: %s", dir, diags.Error())
	}
	cfg, cfgDiags := configs.BuildConfig(context.Background(), mod, call, configs.ModuleWalkerFunc(
		func(_ context.Context, req *configs.ModuleRequest) (*configs.Module, *version.Version, hcl.Diagnostics) {
			t.Fatalf("fixture unexpectedly calls module %q", req.Name)
			return nil, nil, nil
		}, parser.LoadSymbolFilesInDir,
	))
	if cfgDiags.HasErrors() {
		t.Fatalf("building config: %s", cfgDiags.Error())
	}
	return &NodeResolver{Estate: "prod", Config: cfg}
}

func azapiTyped(typ, name string, body, tags cty.Value) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"type":      cty.StringVal(typ),
		"name":      cty.StringVal(name),
		"parent_id": cty.StringVal("/subscriptions/0/resourceGroups/rg"),
		"body":      body,
		"tags":      tags,
	})
}

const ownerDefaultTags = `
provider "azapi" {
  default_tags = { owner = "x" }
}
`

func TestARMStampMergesAzapiDefaultTags(t *testing.T) {
	requireTaggable(t, "Microsoft.Network/virtualNetworks", "2023-09-01", true)
	const vnetType = "Microsoft.Network/virtualNetworks@2023-09-01"
	vnet := armAddr(t, "azapi_resource.vnet")
	nullTags := cty.NullVal(cty.Map(cty.String))
	markersFor := func(addr string, extra map[string]string) map[string]string {
		m := map[string]string{markers.TagEstate: "prod", markers.TagAddress: addr}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}

	t.Run("null tags take the default tags and the markers", func(t *testing.T) {
		got, diags := defaultTagsFixture(t, ownerDefaultTags, nil).AdjustConfigValue(context.Background(), vnet,
			azapiTyped(vnetType, "vnet", cty.EmptyObjectVal, nullTags), azapiSchema())
		if diags.HasErrors() {
			t.Fatalf("refused: %v", diags.Err())
		}
		want := markersFor("azapi_resource.vnet", map[string]string{"owner": "x"})
		if tags := stringMap(t, got.GetAttr("tags")); !reflect.DeepEqual(tags, want) {
			t.Errorf("tags = %v, want %v", tags, want)
		}
	})

	t.Run("a null body behaves as an empty one", func(t *testing.T) {
		got, diags := defaultTagsFixture(t, ownerDefaultTags, nil).AdjustConfigValue(context.Background(), vnet,
			azapiTyped(vnetType, "vnet", cty.NullVal(cty.DynamicPseudoType), nullTags), azapiSchema())
		if diags.HasErrors() {
			t.Fatalf("refused: %v", diags.Err())
		}
		want := markersFor("azapi_resource.vnet", map[string]string{"owner": "x"})
		if tags := stringMap(t, got.GetAttr("tags")); !reflect.DeepEqual(tags, want) {
			t.Errorf("tags = %v, want %v", tags, want)
		}
	})

	t.Run("explicit tags are unchanged by default_tags", func(t *testing.T) {
		own := cty.MapVal(map[string]cty.Value{"env": cty.StringVal("prod")})
		got, diags := defaultTagsFixture(t, ownerDefaultTags, nil).AdjustConfigValue(context.Background(), vnet,
			azapiTyped(vnetType, "vnet", cty.EmptyObjectVal, own), azapiSchema())
		if diags.HasErrors() {
			t.Fatalf("refused: %v", diags.Err())
		}
		want := markersFor("azapi_resource.vnet", map[string]string{"env": "prod"})
		if tags := stringMap(t, got.GetAttr("tags")); !reflect.DeepEqual(tags, want) {
			t.Errorf("tags = %v, want %v", tags, want)
		}
	})

	t.Run("body.tags is unchanged by default_tags", func(t *testing.T) {
		body := cty.ObjectVal(map[string]cty.Value{"tags": cty.ObjectVal(map[string]cty.Value{"env": cty.StringVal("prod")})})
		got, diags := defaultTagsFixture(t, ownerDefaultTags, nil).AdjustConfigValue(context.Background(), vnet,
			azapiTyped(vnetType, "vnet", body, nullTags), azapiSchema())
		if diags.HasErrors() {
			t.Fatalf("refused: %v", diags.Err())
		}
		if !got.GetAttr("tags").IsNull() {
			t.Errorf("top-level tags = %#v, want null: azapi sends body.tags (step 2) and no default tags", got.GetAttr("tags"))
		}
		want := markersFor("azapi_resource.vnet", map[string]string{"env": "prod"})
		if tags := stringMap(t, got.GetAttr("body").GetAttr("tags")); !reflect.DeepEqual(tags, want) {
			t.Errorf("body.tags = %v, want %v", tags, want)
		}
	})

	t.Run("a type that takes no tags is untouched", func(t *testing.T) {
		requireTaggable(t, "Microsoft.Network/virtualNetworks/subnets", "2024-05-01", false)
		got, diags := defaultTagsFixture(t, ownerDefaultTags, nil).AdjustConfigValue(context.Background(), vnet,
			azapiTyped("Microsoft.Network/virtualNetworks/subnets@2024-05-01", "vnet", cty.EmptyObjectVal, nullTags), azapiSchema())
		if diags.HasErrors() {
			t.Fatalf("refused: %v", diags.Err())
		}
		if !got.GetAttr("tags").IsNull() {
			t.Errorf("tags = %#v, want null", got.GetAttr("tags"))
		}
		if !hasSummary(diags, SummaryARMTypeNotStamped) {
			t.Errorf("no %q warning: %v", SummaryARMTypeNotStamped, diags)
		}
	})

	t.Run("a discriminated body gets no default tags", func(t *testing.T) {
		// azapi's canResourceHaveProperty answers false for a
		// DiscriminatedObjectType body, so step 3 never applies there,
		// though the stamp may write tags through its baseProperties.
		const ds, dsVersion = "Microsoft.Resources/deploymentScripts", "2020-10-01"
		requireTaggable(t, ds, dsVersion, true)
		if applies, known := armtypes.DefaultTagsApply(ds, dsVersion); !known || applies {
			t.Fatalf("armtypes.DefaultTagsApply(%s, %s) = %v, %v; the fixture needs false, true", ds, dsVersion, applies, known)
		}
		got, diags := defaultTagsFixture(t, ownerDefaultTags, nil).AdjustConfigValue(context.Background(), vnet,
			azapiTyped(ds+"@"+dsVersion, "vnet", cty.EmptyObjectVal, nullTags), azapiSchema())
		if diags.HasErrors() {
			t.Fatalf("refused: %v", diags.Err())
		}
		want := markersFor("azapi_resource.vnet", nil)
		if tags := stringMap(t, got.GetAttr("tags")); !reflect.DeepEqual(tags, want) {
			t.Errorf("tags = %v, want %v", tags, want)
		}
	})

	t.Run("markers win on their own keys, in any case", func(t *testing.T) {
		provider := `
provider "azapi" {
  default_tags = { owner = "x", "TOFU-ESTATE" = "other", "Tofu-Address" = "elsewhere", "tofu-slot" = "9" }
}
`
		got, diags := defaultTagsFixture(t, provider, nil).AdjustConfigValue(context.Background(), vnet,
			azapiTyped(vnetType, "vnet", cty.EmptyObjectVal, nullTags), azapiSchema())
		if diags.HasErrors() {
			t.Fatalf("refused: %v", diags.Err())
		}
		want := markersFor("azapi_resource.vnet", map[string]string{"owner": "x"})
		if tags := stringMap(t, got.GetAttr("tags")); !reflect.DeepEqual(tags, want) {
			t.Errorf("tags = %v, want %v", tags, want)
		}
	})

	t.Run("another provider configuration's default_tags are not used", func(t *testing.T) {
		got, diags := defaultTagsFixture(t, ownerDefaultTags, nil).AdjustConfigValue(context.Background(), armAddr(t, "azapi_resource.other"),
			azapiTyped(vnetType, "other", cty.EmptyObjectVal, nullTags), azapiSchema())
		if diags.HasErrors() {
			t.Fatalf("refused: %v", diags.Err())
		}
		want := markersFor("azapi_resource.other", nil)
		if tags := stringMap(t, got.GetAttr("tags")); !reflect.DeepEqual(tags, want) {
			t.Errorf("tags = %v, want %v", tags, want)
		}
	})

	fromVar := `
variable "tags" {
  type = map(string)
}
provider "azapi" {
  default_tags = var.tags
}
`
	t.Run("default_tags from a variable given a value", func(t *testing.T) {
		vars := map[string]cty.Value{"tags": cty.MapVal(map[string]cty.Value{"owner": cty.StringVal("x")})}
		got, diags := defaultTagsFixture(t, fromVar, vars).AdjustConfigValue(context.Background(), vnet,
			azapiTyped(vnetType, "vnet", cty.EmptyObjectVal, nullTags), azapiSchema())
		if diags.HasErrors() {
			t.Fatalf("refused: %v", diags.Err())
		}
		want := markersFor("azapi_resource.vnet", map[string]string{"owner": "x"})
		if tags := stringMap(t, got.GetAttr("tags")); !reflect.DeepEqual(tags, want) {
			t.Errorf("tags = %v, want %v", tags, want)
		}
	})

	t.Run("default_tags from a variable given no value", func(t *testing.T) {
		config := azapiTyped(vnetType, "vnet", cty.EmptyObjectVal, nullTags)
		got, diags := defaultTagsFixture(t, fromVar, nil).AdjustConfigValue(context.Background(), vnet, config, azapiSchema())
		if diags.HasErrors() {
			t.Fatalf("refused: %v", diags.Err())
		}
		if !hasSummary(diags, SummaryARMDefaultTagsUnknown) {
			t.Errorf("no %q warning: %v", SummaryARMDefaultTagsUnknown, diags)
		}
		if !got.RawEquals(config) {
			t.Errorf("config changed to %#v; with default tags unknown nothing may be written", got)
		}
	})

	t.Run("empty default_tags is none", func(t *testing.T) {
		provider := `
provider "azapi" {
  default_tags = {}
}
`
		got, diags := defaultTagsFixture(t, provider, nil).AdjustConfigValue(context.Background(), vnet,
			azapiTyped(vnetType, "vnet", cty.EmptyObjectVal, nullTags), azapiSchema())
		if diags.HasErrors() || len(diags) != 0 {
			t.Fatalf("diags: %v", diags)
		}
		want := markersFor("azapi_resource.vnet", nil)
		if tags := stringMap(t, got.GetAttr("tags")); !reflect.DeepEqual(tags, want) {
			t.Errorf("tags = %v, want %v", tags, want)
		}
	})
}
