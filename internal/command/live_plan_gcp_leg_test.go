// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/substrate"
)

// TestAssetSearchLegTurnsOnTheRemovalLegs (GitHub issue #2139, item 5): the
// GCP pass runs the record-orphan leg a deleted google block is destroyed
// by, through recordRemovalLegs; and with no Cloud Asset client it
// still returns a leg, one that searches nothing, so the unlabelled types'
// gaps are filed. Each half goes red on the mutation the audit made.
func TestAssetSearchLegTurnsOnTheRemovalLegs(t *testing.T) {
	addr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("google")}
	// A credentials file that does not exist, so no client can be built
	// whatever this machine's Application Default Credentials hold.
	cfg := cty.ObjectVal(map[string]cty.Value{"credentials": cty.StringVal(filepath.Join(t.TempDir(), "absent.json"))})
	p := &projectionProviders{configVals: map[string]cty.Value{providerCacheKey(addr): cfg}}

	build, ok := sweepLegBuilders[substrate.SweepAssetSearch]
	if !ok {
		t.Fatal("no sweep leg builder for the asset-search sweep")
	}
	leg, sweep, diags := build(context.Background(), p, substrate.Google, addr)
	if sweep {
		t.Error("the GCP pass turns Request.Sweep on; its record leg runs through recordRemovalLegs instead (#2139)")
	}
	if !recordRemovalLegs[substrate.Google.Sweep()] {
		t.Error("the GCP pass runs no record leg, so no deleted google block is ever destroyed")
	}
	gcp, isGCP := leg.(discovery.GCPAssetSweep)
	if !isGCP {
		t.Fatalf("with no client the builder returned %T, want a GCP leg that searches nothing", leg)
	}
	if gcp.Searcher != nil {
		t.Error("a leg was given a searcher although no client could be built")
	}
	if len(diags) == 0 || diags[0].Description().Summary != discovery.SummaryGCPSweepIncomplete {
		t.Errorf("no client gave %v, want the %q warning", diags, discovery.SummaryGCPSweepIncomplete)
	}
}
