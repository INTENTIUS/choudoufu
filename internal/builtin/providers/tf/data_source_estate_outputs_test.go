// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tf

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/encryption"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

type fakeEstateOutputs struct {
	estate string
	names  []string
}

func (f *fakeEstateOutputs) ReadEstateOutputs(_ context.Context, estate string, names []string) (map[string]cty.Value, tfdiags.Diagnostics) {
	f.estate, f.names = estate, names
	out := map[string]cty.Value{}
	for _, n := range names {
		out[n] = cty.StringVal("v-" + n)
	}
	return out, nil
}

func estateOutputsConfig(estate string, names ...string) cty.Value {
	vals := make([]cty.Value, len(names))
	for i, n := range names {
		vals[i] = cty.StringVal(n)
	}
	set := cty.SetValEmpty(cty.String)
	if len(vals) > 0 {
		set = cty.SetVal(vals)
	}
	return cty.ObjectVal(map[string]cty.Value{
		"estate": cty.StringVal(estate),
		"names":  set,
		"values": cty.NullVal(cty.DynamicPseudoType),
	})
}

func readEstateOutputs(p providers.Interface, cfg cty.Value) providers.ReadDataSourceResponse {
	addr := addrs.Resource{Mode: addrs.DataResourceMode, Type: EstateOutputsTypeName, Name: "network"}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
	return p.(*Provider).ReadDataSourceEncrypted(context.Background(), providers.ReadDataSourceRequest{TypeName: EstateOutputsTypeName, Config: cfg}, addr, encryption.Disabled())
}

func TestEstateOutputsDataSourceReadsThroughTheReader(t *testing.T) {
	reader := &fakeEstateOutputs{}
	p := NewProviderWithEstateOutputs(reader)
	if _, ok := p.GetProviderSchema(context.Background()).DataSources[EstateOutputsTypeName]; !ok {
		t.Fatalf("the provider does not declare %s", EstateOutputsTypeName)
	}
	resp := readEstateOutputs(p, estateOutputsConfig("network", "b", "a"))
	if resp.Diagnostics.HasErrors() {
		t.Fatal(resp.Diagnostics.Err())
	}
	if reader.estate != "network" || strings.Join(reader.names, ",") != "a,b" {
		t.Errorf("reader asked for %q %v", reader.estate, reader.names)
	}
	if got := resp.State.GetAttr("values").GetAttr("a"); !got.RawEquals(cty.StringVal("v-a")) {
		t.Errorf("values.a = %#v", got)
	}
}

func TestEstateOutputsDataSourceRefusesOutsideALiveRun(t *testing.T) {
	resp := readEstateOutputs(NewProvider(), estateOutputsConfig("network", "a"))
	if !resp.Diagnostics.HasErrors() || resp.Diagnostics[0].Description().Summary != "Estate outputs need a live block" {
		t.Fatalf("got %v", resp.Diagnostics)
	}
}

func TestEstateOutputsDataSourceValidates(t *testing.T) {
	p := NewProvider()
	for cfg, want := range map[string]cty.Value{
		"Invalid estate name": estateOutputsConfig("../network", "a"),
		"No outputs named":    estateOutputsConfig("network"),
	} {
		resp := p.ValidateDataResourceConfig(context.Background(), providers.ValidateDataResourceConfigRequest{TypeName: EstateOutputsTypeName, Config: want})
		if !resp.Diagnostics.HasErrors() || resp.Diagnostics[0].Description().Summary != cfg {
			t.Errorf("want %q, got %v", cfg, resp.Diagnostics)
		}
	}
	resp := p.ValidateDataResourceConfig(context.Background(), providers.ValidateDataResourceConfigRequest{TypeName: EstateOutputsTypeName, Config: estateOutputsConfig("network", "a")})
	if resp.Diagnostics.HasErrors() {
		t.Errorf("a valid block was refused: %s", resp.Diagnostics.Err())
	}
}
