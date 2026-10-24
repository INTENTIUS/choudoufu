// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"errors"
	"testing"

	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

type fakeLister struct {
	items map[DataPlaneKind][]DataPlaneItem
	errs  map[DataPlaneKind]error
	asked []DataPlaneScope
}

func (f *fakeLister) List(_ context.Context, s DataPlaneScope) ([]DataPlaneItem, error) {
	f.asked = append(f.asked, s)
	if err := f.errs[s.Kind]; err != nil {
		return nil, err
	}
	return f.items[s.Kind], nil
}

const kvBase = "https://kv.vault.azure.net"

func TestDataPlaneLegFindsAnOrphanedSecretByItsMarker(t *testing.T) {
	versioned := kvBase + "/secrets/old/0123456789abcdef"
	lister := &fakeLister{items: map[DataPlaneKind][]DataPlaneItem{DataPlaneSecrets: {
		{ID: versioned, Name: "old", Tags: map[string]string{
			markers.TagEstate: rgEstate, markers.TagAddress: markers.EscapeAddress("azurerm_key_vault_secret.old"),
		}},
		// Another estate's secret and an unmarked one are not this estate's.
		{ID: kvBase + "/secrets/other/1", Name: "other", Tags: map[string]string{markers.TagEstate: "someone-else"}},
		{ID: kvBase + "/secrets/bare/1", Name: "bare"},
	}}}
	leg := DataPlaneSweep{Lister: lister, Scopes: []DataPlaneScope{{BaseURL: kvBase, Kind: DataPlaneSecrets}}}

	res, diags := discoverWith(t, leg)
	assertNoErrors(t, diags)
	o, ok := removalsByAddr(res)["azurerm_key_vault_secret.old"]
	if !ok || o.ImportID != versioned {
		t.Fatalf("no removal for the marked secret at its versioned id; got %+v (%v), orphans %+v", o, ok, res.Orphans)
	}
	if len(removalsByAddr(res)) != 1 {
		t.Errorf("removals %v, want only the marked secret", removalsByAddr(res))
	}
	if len(res.SweepCovered) != 1 || res.SweepCovered[0] != "azurerm_key_vault_secret" {
		t.Errorf("SweepCovered = %v", res.SweepCovered)
	}
}

func TestDataPlaneLegReportsAFailedListingAsAGapNotAnEmptyAnswer(t *testing.T) {
	lister := &fakeLister{errs: map[DataPlaneKind]error{DataPlaneKeys: errors.New("403 forbidden")}}
	leg := DataPlaneSweep{Lister: lister, Scopes: []DataPlaneScope{
		{BaseURL: kvBase, Kind: DataPlaneKeys}, {BaseURL: kvBase, Kind: DataPlaneCertificates},
	}}
	res, diags := discoverWith(t, leg)
	assertNoErrors(t, diags)
	if len(res.SweepGaps) != 1 || res.SweepGaps[0].TypeName != "azurerm_key_vault_key" || res.SweepGaps[0].Reason != SweepGapListFailed {
		t.Fatalf("gaps %+v, want one LIST_FAILED for the keys", res.SweepGaps)
	}
	for _, c := range res.SweepCovered {
		if c == "azurerm_key_vault_key" {
			t.Errorf("a key listing that failed was reported covered")
		}
	}
	if len(lister.asked) != 2 {
		t.Errorf("asked %v, want both collections tried", lister.asked)
	}
}

func discoverWith(t *testing.T, leg Sweeper) (*Result, tfdiags.Diagnostics) {
	t.Helper()
	return Discover(context.Background(), Request{
		Estate:   rgEstate,
		Config:   azConfig(t),
		Provider: newFakeCloud(),
		Sweepers: []Sweeper{leg},
	})
}
