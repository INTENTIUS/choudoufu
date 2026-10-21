// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/substrate"
)

// TestAzureMarkerWriterReadsTheProviderBlock (#1120 unit 12): an azurerm
// configuration gets the ARM Tags API writer, built from that
// configuration's own evaluated block through azsweep's credential chain.
// The block here sets tenant_id from a sensitive value, which the chain
// refuses by name before it picks a credential or makes any call (a
// secret such as client_secret is expected to be sensitive and is read;
// an argument that decides who or where the call goes is not), so the
// refusal is the proof the writer read this block and no other.
func TestAzureMarkerWriterReadsTheProviderBlock(t *testing.T) {
	addr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("azurerm")}
	p := &projectionProviders{configVals: map[string]cty.Value{
		providerCacheKey(addr): cty.ObjectVal(map[string]cty.Value{
			"subscription_id": cty.StringVal("1111"),
			"client_id":       cty.StringVal("app"),
			"tenant_id":       cty.StringVal("tenant").Mark(marks.Sensitive),
		}),
	}}
	w, err := p.markerTagger(addr, substrate.WriteARMTagsAPI)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := w.(projection.ARMTagsWriter); !ok {
		t.Fatalf("azurerm got %T, want the ARM Tags API writer", w)
	}
	created := projection.CreatedInstance{Object: cty.ObjectVal(map[string]cty.Value{
		"id": cty.StringVal("/subscriptions/1111/resourceGroups/rg/providers/Microsoft.Web/certificates/c"),
	})}
	err = w.WriteMarkers(context.Background(), created, map[string]string{"tofu-estate": "prod"})
	if err == nil || !strings.Contains(err.Error(), "tenant_id") || !strings.Contains(err.Error(), "post-create marker write") {
		t.Errorf("WriteMarkers = %v; want the credential chain's refusal of the sensitive tenant_id", err)
	}

	if w, err := p.markerTagger(addr, substrate.WriteTaggingAPI); w != nil || err == nil {
		t.Errorf("azurerm asked for the AWS Tagging API: writer %v, err %v; want a refusal", w, err)
	}
}
