// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package listclient

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// childSchema is azurerm 5.10.0's azurerm_subnet list resource in the part
// that matters: one required argument naming the parent. The real
// provider's SubnetListResource.List dereferences that argument when it is
// null and panics, which killed the plugin for the rest of reference-azure's
// no_local_state plan (#1120, run seven): the -refresh=false plan's cache
// vouch pass lists every cached concrete type with an empty configuration,
// and every ImportResourceState after it answered "Plugin did not respond".
func childSchema() providers.GetProviderSchemaResponse {
	return providers.GetProviderSchemaResponse{
		ResourceTypes: map[string]providers.Schema{
			"azurerm_subnet": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
				"id": {Type: cty.String, Computed: true},
			}}},
		},
		ListResourceTypes: map[string]providers.Schema{
			"azurerm_subnet": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
				"virtual_network_id": {Type: cty.String, Required: true},
				"subscription_id":    {Type: cty.String, Optional: true},
			}}},
		},
	}
}

func TestStreamNeverSendsAConfigMissingARequiredArgument(t *testing.T) {
	ts, _ := mustSchemas(t, &fakeLister{schema: childSchema()}).Get("azurerm_subnet")
	for name, config := range map[string]cty.Value{
		"nil config":     cty.NilVal,
		"empty config":   ts.EmptyConfig(),
		"unknown parent": cty.ObjectVal(map[string]cty.Value{"virtual_network_id": cty.UnknownVal(cty.String), "subscription_id": cty.NullVal(cty.String)}),
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeLister{schema: childSchema()}
			_, diags := List(context.Background(), f, "azurerm_subnet", config, true)
			if f.streamed {
				t.Fatalf("the provider was asked to list with %s unset: %#v", "virtual_network_id", f.gotReq.Config)
			}
			if !diags.HasErrors() || !strings.Contains(diags.Err().Error(), `"virtual_network_id"`) {
				t.Errorf("want an error naming virtual_network_id, got %v", diags.Err())
			}
		})
	}

	// The parent set: the call goes out unchanged.
	f := &fakeLister{schema: childSchema()}
	set := cty.ObjectVal(map[string]cty.Value{"virtual_network_id": cty.StringVal("/subscriptions/s/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/v"), "subscription_id": cty.NullVal(cty.String)})
	if _, diags := List(context.Background(), f, "azurerm_subnet", set, true); diags.HasErrors() || !f.streamed {
		t.Errorf("a configuration with its required argument set was not listed: streamed=%v %v", f.streamed, diags.Err())
	}

	// A type whose list schema requires nothing is listed with an empty
	// configuration, as before.
	f = &fakeLister{schema: testSchema()}
	if _, diags := List(context.Background(), f, "test_thing", cty.NilVal, true); diags.HasErrors() || !f.streamed {
		t.Errorf("a type with no required list argument was not listed: streamed=%v %v", f.streamed, diags.Err())
	}
}
