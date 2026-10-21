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

// googleListSchema is hashicorp/google 8.6.0's list resource schemas for
// the types below, attribute for attribute as `terraform providers schema
// -json` prints them at that pin (list_resource_schemas). 88 of the
// provider's 152 list resources require at least one argument, a parent
// the list call is scoped to, as azurerm_subnet's requires its network.
func googleListSchema() providers.GetProviderSchemaResponse {
	str := func(required bool) *configschema.Attribute {
		return &configschema.Attribute{Type: cty.String, Required: required, Optional: !required}
	}
	list := map[string]map[string]*configschema.Attribute{
		"google_storage_bucket_iam_member":             {"bucket": str(true), "member": str(false), "role": str(false)},
		"google_dns_record_set":                        {"managed_zone": str(true), "name": str(false), "project": str(false), "type": str(false)},
		"google_compute_region_network_endpoint_group": {"project": str(false), "region": str(true)},
		"google_discovery_engine_assistant":            {"collection_id": str(true), "engine_id": str(true), "location": str(true), "project": str(false)},
		"google_storage_bucket":                        {"project": str(false)},
		"google_pubsub_topic":                          {"project": str(false)},
	}
	resp := providers.GetProviderSchemaResponse{
		ResourceTypes:     map[string]providers.Schema{},
		ListResourceTypes: map[string]providers.Schema{},
	}
	for name, attrs := range list {
		resp.ResourceTypes[name] = providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id": {Type: cty.String, Computed: true},
		}}}
		resp.ListResourceTypes[name] = providers.Schema{Block: &configschema.Block{Attributes: attrs}}
	}
	return resp
}

// TestStreamGuardHoldsForGoogleListTypes puts google's own list resources
// through the guard azurerm_subnet's panic produced: a google list type
// that requires a parent argument (a bucket, a managed zone, a region, or
// three discovery-engine scopes) is never sent with it unset, and the
// refusal names every missing argument; one that requires nothing is
// listed with an empty configuration. The guard reads the list schema and
// names no provider, so this is the GCP lane's check that it holds there
// too, not a second mechanism.
func TestStreamGuardHoldsForGoogleListTypes(t *testing.T) {
	want := map[string][]string{
		"google_storage_bucket_iam_member":             {"bucket"},
		"google_dns_record_set":                        {"managed_zone"},
		"google_compute_region_network_endpoint_group": {"region"},
		"google_discovery_engine_assistant":            {"collection_id", "engine_id", "location"},
		"google_storage_bucket":                        nil,
		"google_pubsub_topic":                          nil,
	}
	for typeName, missing := range want {
		t.Run(typeName, func(t *testing.T) {
			ts, ok := mustSchemas(t, &fakeLister{schema: googleListSchema()}).Get(typeName)
			if !ok {
				t.Fatalf("%s has no list schema", typeName)
			}
			for name, config := range map[string]cty.Value{"nil config": cty.NilVal, "empty config": ts.EmptyConfig()} {
				f := &fakeLister{schema: googleListSchema()}
				_, diags := List(context.Background(), f, typeName, config, true)
				if len(missing) == 0 {
					if diags.HasErrors() || !f.streamed {
						t.Errorf("%s: a type that requires no list argument was not listed: streamed=%v %v", name, f.streamed, diags.Err())
					}
					continue
				}
				if f.streamed {
					t.Errorf("%s: the provider was asked to list with %v unset: %#v", name, missing, f.gotReq.Config)
				}
				if !diags.HasErrors() {
					t.Errorf("%s: no refusal for a configuration missing %v", name, missing)
					continue
				}
				for _, arg := range missing {
					if !strings.Contains(diags.Err().Error(), `"`+arg+`"`) {
						t.Errorf("%s: the refusal does not name %q: %v", name, arg, diags.Err())
					}
				}
			}
		})
	}
}
