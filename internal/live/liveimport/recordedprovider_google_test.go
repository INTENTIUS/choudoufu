// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/pkg/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// #2149's red on the google family (gcp-lane). A stock state records every
// google resource under registry.terraform.io/hashicorp/google, and a live
// plan over a root with two google configurations runs one discovery pass
// per configuration on the configuration's host. A firewall has no labels
// argument, so live-import seeds an identity record for it and a later plan
// finds it only through that record. Removing its block must plan the
// destroy in the default google pass.

var stockGoogle = addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.Provider{
	Hostname: "registry.terraform.io", Namespace: "hashicorp", Type: "google",
}}

var configGoogle = addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.Provider{
	Hostname: "registry.opentofu.org", Namespace: "hashicorp", Type: "google",
}}

// recprovGoogleConfig is a two-configuration google root (google and
// google.west) with the firewall's block already removed.
const recprovGoogleConfig = `
terraform {
  required_providers {
    google = {
      source = "hashicorp/google"
    }
  }
}

provider "google" {}

provider "google" {
  alias = "west"
}
`

// recprovFirewallSchema is google_compute_firewall narrowed to its identity:
// no labels argument, so live-import takes the untaggable path.
func recprovFirewallSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":      {Type: cty.String, Computed: true},
		"name":    {Type: cty.String, Required: true},
		"network": {Type: cty.String, Required: true},
		"project": {Type: cty.String, Optional: true, Computed: true},
	}}}
}

type recprovGoogleProvider struct{ *tofu.MockProvider }

func newRecprovGoogleProvider() recprovGoogleProvider {
	p := recprovGoogleProvider{&tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"google_compute_firewall": recprovFirewallSchema()},
		},
	}}
	p.ConfigureProviderCalled = true
	return p
}

func (p recprovGoogleProvider) ConfiguredProvider(context.Context, addrs.AbsProviderConfig) (providers.Interface, error) {
	return p, nil
}

func (recprovGoogleProvider) ListResourceStream(context.Context, providers.ListResourceRequest, func(providers.ListResourceEvent) bool) tfdiags.Diagnostics {
	return nil
}

func recprovGoogleConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(recprovGoogleConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestStockStateGoogleRecordIsRemovedThroughTheConfiguredProvider(t *testing.T) {
	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := projection.NewRecordEnvelopeStore(raw, projection.RecordKeyPrefix(testEstate))
	addr := mustAddr(t, "google_compute_firewall.ssh")

	state := states.NewState()
	state.RootModule().SetResourceInstanceCurrent(
		addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "google_compute_firewall", Name: "ssh"}.Instance(addrs.NoKey),
		&states.ResourceInstanceObjectSrc{
			AttrsJSON: []byte(`{"id":"projects/chdf-p/global/firewalls/ssh","name":"ssh","network":"projects/chdf-p/global/networks/net","project":"chdf-p"}`),
			Status:    states.ObjectReady,
		},
		stockGoogle,
		addrs.NoKey,
	)
	rat, diags := Ratify(context.Background(), Request{Estate: testEstate, State: state, Providers: newRecprovGoogleProvider(), Config: loadSlotTestConfig(t, recprovGoogleConfigDir(t)), RecordStore: store})
	if diags.HasErrors() {
		t.Fatalf("Ratify: %s", diags.Err())
	}
	if len(rat.Entries) != 1 || rat.Entries[0].Status != StatusUntaggable {
		t.Fatalf("entries = %+v, want the firewall UNTAGGABLE", rat.Entries)
	}
	if _, diags := rat.Approve(context.Background()); diags.HasErrors() {
		t.Fatalf("Approve: %s", diags.Err())
	}
	if _, recorded, _, found, err := store.GetIdentityWithProvider(context.Background(), addr); err != nil || !found {
		t.Fatalf("live-import recorded no identity for the firewall (found %v, err %v)", found, err)
	} else if recorded != configGoogle.String() {
		t.Errorf("the record names provider %s, want the configuration's %s", recorded, configGoogle)
	}

	configWest := configGoogle
	configWest.Alias = "west"
	var passes []discovery.Pass
	for _, scope := range []addrs.AbsProviderConfig{configGoogle, configWest} {
		res, diags := discovery.Discover(context.Background(), discovery.Request{
			Estate:        testEstate,
			Config:        loadSlotTestConfig(t, recprovGoogleConfigDir(t)),
			Provider:      newRecprovGoogleProvider(),
			HintStore:     raw,
			Sweep:         true,
			Sweepers:      []discovery.Sweeper{},
			ScopeProvider: scope,
			VouchProvider: scope,
		})
		if diags.HasErrors() {
			t.Fatalf("Discover through %s: %s", scope, diags.Err())
		}
		passes = append(passes, discovery.Pass{Provider: scope, Result: res})
	}
	for _, e := range passes[0].Result.RecordedElsewhere {
		t.Errorf("the default google pass left %s to provider %s (RecordedElsewhere), want its destroy proposed", e.Addr, e.Provider)
	}
	merged, providerOf, mdiags := discovery.Merge(testEstate, passes, false)
	if mdiags.HasErrors() {
		t.Errorf("Merge: %s", mdiags.Err())
	}
	if n := removalsOf(merged, addr); n != 1 {
		t.Errorf("the merge proposes %d removals of %s, want 1:\n%s", n, addr, merged)
	}
	if got, ok := providerOf[addr.String()]; !ok {
		t.Errorf("the merge reads no removal of %s, want it read through %s", addr, configGoogle)
	} else if got.String() != configGoogle.String() {
		t.Errorf("the removal is read through %s, want %s", got, configGoogle)
	}
}
