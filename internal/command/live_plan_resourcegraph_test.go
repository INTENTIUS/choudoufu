// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/pkg/azsweep"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #1120, unit 11: the command layer's choice of the Azure leg,
// and how several provider blocks split the subscriptions between their
// passes.

// TestAzurePlansThroughTheResourceGraphLeg: an azurerm or azapi provider
// configuration gets the Resource Graph leg, never discovery.NoSweepLeg and
// never the AWS legs, with Request.Sweep off as the Kubernetes leg has it.
// A block no client can be built from still gets the leg, with one
// warning saying so.
func TestAzurePlansThroughTheResourceGraphLeg(t *testing.T) {
	for _, name := range azsweep.EnvNames() {
		t.Setenv(name, "")
	}
	// No credential at all, and the CLI switched off, so the credential
	// chain refuses rather than looking for an az login on this machine.
	t.Setenv("ARM_USE_CLI", "false")
	p := &projectionProviders{}
	for _, typ := range []string{"azurerm", "azapi"} {
		sub, known := substrate.ForProvider(typ)
		if !known || sub != substrate.Azure {
			t.Fatalf("ForProvider(%q) = %v, %v; want the Azure family", typ, sub, known)
		}
		addr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider(typ)}
		legs, sweep, diags := p.liveSweepLegs(context.Background(), sub, known, addr)
		if sweep || len(legs) != 1 {
			t.Fatalf("%s: legs %v, sweep %v; want the Resource Graph leg with Sweep off", typ, legs, sweep)
		}
		if _, ok := legs[0].(discovery.ResourceGraphSweep); !ok {
			t.Errorf("%s: got %#v, want discovery.ResourceGraphSweep", typ, legs[0])
		}
		if diags.HasErrors() || len(diags) == 0 || diags[0].Description().Summary != azsweep.SummarySweepUnavailable || !strings.Contains(diags[0].Description().Detail, addr.String()) {
			t.Errorf("%s: diagnostics %v; want one %q warning naming %s", typ, diags.ErrWithWarnings(), azsweep.SummarySweepUnavailable, addr)
		}
	}
}

// graphFamilyClient is a Resource Graph client that queries nothing and
// names the subscription its block named.
type graphFamilyClient struct {
	sub string
}

func (graphFamilyClient) SweepKind() substrate.Sweep { return substrate.SweepResourceGraph }
func (c graphFamilyClient) Subscription() string     { return c.sub }
func (graphFamilyClient) Search(context.Context, string, string) ([]azsweep.Resource, error) {
	return nil, nil
}

// azureByBlock is the Azure family with a client built from each block's
// subscription_id alone.
type azureByBlock struct{ substrate.Substrate }

func (azureByBlock) NewSweeper(val cty.Value, ok bool) (substrate.Sweeper, error) {
	return graphFamilyClient{sub: substrate.AzureSweepAttrs(val, ok).SubscriptionID}, nil
}

// TestResourceGraphSubscriptionsSplitAcrossPasses: three azurerm blocks over
// two subscriptions. Every pass's leg holds the same coverage, each
// subscription is swept by exactly one pass (the first, in pass order,
// whose block names it), and only the first pass takes subscriptions no
// block names.
func TestResourceGraphSubscriptionsSplitAcrossPasses(t *testing.T) {
	const sub1, sub2 = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	cfg := liveLsLoadConfig(t, `
provider "azurerm" {
  subscription_id = "`+sub1+`"
}
provider "azurerm" {
  alias           = "other"
  subscription_id = "`+sub2+`"
}
provider "azurerm" {
  alias           = "same"
  subscription_id = "`+strings.ToUpper(sub1)+`"
}
resource "azurerm_resource_group" "a" {
  name     = "a"
  location = "westeurope"
}
resource "azurerm_resource_group" "b" {
  provider = azurerm.other
  name     = "b"
  location = "westeurope"
}
resource "azurerm_resource_group" "c" {
  provider = azurerm.same
  name     = "c"
  location = "westeurope"
}
`)
	azurerm := addrs.NewDefaultProvider("azurerm")
	p := newProjectionProviders(cfg, plugins.NewLibrary(plugins.ProviderFactories{
		azurerm: providers.FactoryFixed(&tofu.MockProvider{GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider: providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
				"subscription_id": {Type: cty.String, Optional: true},
			}}},
			ResourceTypes: map[string]providers.Schema{},
		}}),
	}, nil))
	family := azureByBlock{substrate.Azure}
	cfgAddr := func(alias string) addrs.AbsProviderConfig {
		return addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: azurerm, Alias: alias}
	}

	swept := map[string][]string{}
	for _, alias := range []string{"", "other", "same"} {
		legs, sweep, diags := p.liveSweepLegs(context.Background(), family, true, cfgAddr(alias))
		if diags.HasErrors() || len(diags) != 0 || sweep || len(legs) != 1 {
			t.Fatalf("pass %q: legs %v, sweep %v, diagnostics %v", alias, legs, sweep, diags.ErrWithWarnings())
		}
		leg, ok := legs[0].(discovery.ResourceGraphSweep)
		if !ok {
			t.Fatalf("pass %q: leg %#v, want the Resource Graph leg", alias, legs[0])
		}
		var covered []string
		for s := range leg.Coverage {
			covered = append(covered, strings.ToLower(s))
		}
		sort.Strings(covered)
		if strings.Join(covered, ",") != sub1+","+sub2 {
			t.Errorf("pass %q covers %v, want both subscriptions", alias, covered)
		}
		if leg.TakesUnnamed != (alias == "") {
			t.Errorf("pass %q TakesUnnamed = %v; only the first pass takes subscriptions no block names", alias, leg.TakesUnnamed)
		}
		for _, s := range leg.Subscriptions {
			swept[strings.ToLower(s)] = append(swept[strings.ToLower(s)], alias)
		}
	}
	if got := swept[sub1]; len(got) != 1 || got[0] != "" {
		t.Errorf("%s swept by passes %q, want the default block's alone", sub1, got)
	}
	if got := swept[sub2]; len(got) != 1 || got[0] != "other" {
		t.Errorf("%s swept by passes %q, want azurerm.other's alone", sub2, got)
	}
}

// TestUntaggableARMTypesClassifiesFromTheSchemaAndAzapiData is GitHub issue
// #2119: a declared azurerm_subnet (no tags argument) and an azapi block
// whose ARM type takes no tags at its own API version are classified
// untaggable for the Resource Graph leg's TYPE_NOT_TAGGABLE gap; a
// taggable azurerm type and a taggable azapi type are not, nor is a type
// of another family.
func TestUntaggableARMTypesClassifiesFromTheSchemaAndAzapiData(t *testing.T) {
	dir := t.TempDir()
	src := `
terraform {
  required_providers {
    azapi = {
      source = "azure/azapi"
    }
  }
}
resource "azurerm_virtual_network" "net" {
  name = "net"
}
resource "azurerm_subnet" "a" {
  name = "a"
}
resource "azapi_resource" "subnet" {
  type = "Microsoft.Network/virtualNetworks/subnets@2024-05-01"
  name = "b"
}
resource "azapi_resource" "net" {
  type = "Microsoft.Network/virtualNetworks@2024-05-01"
  name = "c"
}
resource "aws_vpc_endpoint_route_table_association" "x" {
  vpc_endpoint_id = "e"
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	config := liveTestLoadConfig(t, dir)

	tags := &configschema.Attribute{Type: cty.Map(cty.String), Optional: true}
	name := &configschema.Attribute{Type: cty.String, Required: true}
	schemas := map[string]map[string]providers.Schema{
		"azurerm": {
			"azurerm_virtual_network": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{"name": name, "tags": tags}}},
			"azurerm_subnet":          {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{"name": name}}},
		},
		"azapi": {
			"azapi_resource": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
				"name": name,
				"type": {Type: cty.String, Required: true},
				"body": {Type: cty.DynamicPseudoType, Optional: true},
				"tags": tags,
			}}},
		},
		"aws": {
			"aws_vpc_endpoint_route_table_association": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{"vpc_endpoint_id": name}}},
		},
	}
	got := untaggableARMTypes(t.Context(), config, substrate.Azure.ProviderTypes(), func(p addrs.Provider) map[string]providers.Schema {
		return schemas[p.Type]
	})
	var names []string
	for _, u := range got {
		names = append(names, u.TypeName+": "+u.Why)
	}
	want := []string{
		"azapi_resource (Microsoft.Network/virtualNetworks/subnets@2024-05-01): takes no tags at that API version",
		"azurerm_subnet: carries no tags",
	}
	if strings.Join(names, "\n") != strings.Join(want, "\n") {
		t.Errorf("untaggable types:\n%s\nwant:\n%s", strings.Join(names, "\n"), strings.Join(want, "\n"))
	}
}
