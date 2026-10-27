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
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
	"github.com/intentius/choudoufu/pkg/staterecord"
)

// The AWS-shaped twin of recordedprovider_test.go's Azure tests, ported to
// main before the azure branch landed; both are kept.
//
// GitHub issue #1120, found on the azure branch: the provider an identity
// record names. A live plan over a root with more than one provider
// configuration runs discovery once per configuration
// ([discovery.Request.ScopeProvider], set to the address the configuration
// resolves, registry.opentofu.org/hashicorp/aws), and the record leg
// proposes a removed block's record only in the pass whose configuration the
// record names. live-import wrote the address the stock state file
// recorded, registry.terraform.io/hashicorp/aws, so an untaggable resource
// found only through its record read as another configuration's in its own
// pass, and removing its block planned no destroy: the merge refused it as
// RECORDED_PROVIDER_ABSENT instead.

// stockAWS is the provider address a stock terraform state file records for
// every aws resource.
var stockAWS = addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.Provider{
	Hostname: "registry.terraform.io", Namespace: "hashicorp", Type: "aws",
}}

// configAWS is the provider configuration a live plan's default aws pass is
// scoped to: the root's required_providers entry for aws.
var configAWS = addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.Provider{
	Hostname: "registry.opentofu.org", Namespace: "hashicorp", Type: "aws",
}}

// recprovConfig is a two-configuration aws root (aws and aws.west) with the
// attachment's block already removed: the providers are still required and
// configured, and nothing is declared.
const recprovConfig = `
terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

provider "aws" {}

provider "aws" {
  alias = "west"
}
`

const (
	recprovTGARN = "arn:aws:elasticloadbalancing:us-east-1:111111111111:targetgroup/app/0123456789abcdef"
	recprovTID   = "i-0abc123def4567890"
)

// recprovAttachmentSchema is aws_lb_target_group_attachment narrowed to its
// identity: no tags argument, so live-import takes the untaggable path and
// seeds a kind=identity record, and a later plan finds the object only
// through that record.
func recprovAttachmentSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":               {Type: cty.String, Computed: true},
		"target_group_arn": {Type: cty.String, Required: true},
		"target_id":        {Type: cty.String, Required: true},
		"port":             {Type: cty.Number, Optional: true},
	}}}
}

// recprovProvider answers the attachment's schema for both live-import and
// discovery, and speaks the list protocol with no list resource types, so
// discovery's record leg is the only route to the attachment.
type recprovProvider struct{ *tofu.MockProvider }

func newRecprovProvider() recprovProvider {
	p := recprovProvider{&tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"aws_lb_target_group_attachment": recprovAttachmentSchema()},
		},
	}}
	p.ConfigureProviderCalled = true
	return p
}

func (p recprovProvider) ConfiguredProvider(context.Context, addrs.AbsProviderConfig) (providers.Interface, error) {
	return p, nil
}

func (recprovProvider) ListResourceStream(context.Context, providers.ListResourceRequest, func(providers.ListResourceEvent) bool) tfdiags.Diagnostics {
	return nil
}

func recprovConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(recprovConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// migrateAttachment runs live-import over a stock state holding the
// untaggable attachment, recorded under stateProvider, and returns the raw
// record store it seeded.
func migrateAttachment(t *testing.T, stateProvider addrs.AbsProviderConfig) (staterecord.Store, *projection.RecordStore) {
	t.Helper()
	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := projection.NewRecordEnvelopeStore(raw, projection.RecordKeyPrefix(testEstate))

	state := states.NewState()
	state.RootModule().SetResourceInstanceCurrent(
		addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "aws_lb_target_group_attachment", Name: "app"}.Instance(addrs.NoKey),
		&states.ResourceInstanceObjectSrc{
			AttrsJSON: []byte(`{"id":"` + recprovTGARN + `-20260101","target_group_arn":"` + recprovTGARN + `","target_id":"` + recprovTID + `","port":80}`),
			Status:    states.ObjectReady,
		},
		stateProvider,
		addrs.NoKey,
	)

	cfg := loadSlotTestConfig(t, recprovConfigDir(t))
	rat, diags := Ratify(context.Background(), Request{Estate: testEstate, State: state, Providers: newRecprovProvider(), Config: cfg, RecordStore: store})
	if diags.HasErrors() {
		t.Fatalf("Ratify: %s", diags.Err())
	}
	if len(rat.Entries) != 1 || rat.Entries[0].Status != StatusUntaggable {
		t.Fatalf("entries = %+v, want the attachment UNTAGGABLE", rat.Entries)
	}
	if _, diags := rat.Approve(context.Background()); diags.HasErrors() {
		t.Fatalf("Approve: %s", diags.Err())
	}
	if _, _, _, found, err := store.GetIdentity(context.Background(), mustAddr(t, "aws_lb_target_group_attachment.app")); err != nil || !found {
		t.Fatalf("live-import recorded no identity for the attachment (found %v, err %v)", found, err)
	}
	return raw, store
}

// recprovPasses runs one discovery pass per scope over the configuration
// with the attachment's block removed, and merges them the way live-plan
// does.
func recprovPasses(t *testing.T, raw staterecord.Store, scopes ...addrs.AbsProviderConfig) ([]discovery.Pass, *discovery.Result, map[string]addrs.AbsProviderConfig, tfdiags.Diagnostics) {
	t.Helper()
	cfg := loadSlotTestConfig(t, recprovConfigDir(t))
	var passes []discovery.Pass
	for _, scope := range scopes {
		res, diags := discovery.Discover(context.Background(), discovery.Request{
			Estate:        testEstate,
			Config:        cfg,
			Provider:      newRecprovProvider(),
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
	merged, providerOf, mdiags := discovery.Merge(testEstate, passes, false)
	return passes, merged, providerOf, mdiags
}

func removalsOf(merged *discovery.Result, addr addrs.AbsResourceInstance) int {
	var n int
	for _, r := range merged.Resolutions {
		if r.Addr.String() == addr.String() && r.Undeclared {
			n++
		}
	}
	return n
}

// TestStockStateRecordIsRemovedThroughTheConfiguredProvider is the red: an
// untaggable attachment migrated out of a stock state, then its block
// removed. The default aws pass must propose its destroy, not leave it to a
// configuration that does not exist.
func TestStockStateRecordIsRemovedThroughTheConfiguredProviderAWS(t *testing.T) {
	raw, store := migrateAttachment(t, stockAWS)
	addr := mustAddr(t, "aws_lb_target_group_attachment.app")
	if _, recorded, _, _, err := store.GetIdentityWithProvider(context.Background(), addr); err != nil {
		t.Fatal(err)
	} else {
		t.Logf("the record names provider %s", recorded)
	}

	configWest := configAWS
	configWest.Alias = "west"
	passes, merged, providerOf, mdiags := recprovPasses(t, raw, configAWS, configWest)
	for _, e := range passes[0].Result.RecordedElsewhere {
		t.Errorf("the default aws pass left %s to provider %s (RecordedElsewhere), want its destroy proposed", e.Addr, e.Provider)
	}
	if mdiags.HasErrors() {
		t.Errorf("Merge: %s", mdiags.Err())
	}
	if n := removalsOf(merged, addr); n != 1 {
		t.Errorf("the merge proposes %d removals of %s, want 1:\n%s", n, addr, merged)
	}
	if got, ok := providerOf[addr.String()]; !ok {
		t.Errorf("the merge reads no removal of %s, want it read through %s", addr, configAWS)
	} else if got.String() != configAWS.String() {
		t.Errorf("the removal is read through %s, want %s", got, configAWS)
	}
}

// TestRecordNamingAnotherAliasStaysThatAliassRemoval is the honest
// "recorded elsewhere" control: the stock state recorded the attachment
// under aws.west, so the record names the west configuration (on the
// configuration's host), the default aws pass leaves it to that
// configuration, and the west pass proposes the destroy.
func TestRecordNamingAnotherAliasStaysThatAliassRemovalAWS(t *testing.T) {
	stockWest := stockAWS
	stockWest.Alias = "west"
	raw, store := migrateAttachment(t, stockWest)
	addr := mustAddr(t, "aws_lb_target_group_attachment.app")
	configWest := configAWS
	configWest.Alias = "west"
	if _, recorded, _, _, err := store.GetIdentityWithProvider(context.Background(), addr); err != nil {
		t.Fatal(err)
	} else if recorded != configWest.String() {
		t.Errorf("the record names provider %s, want %s", recorded, configWest)
	}

	passes, merged, providerOf, mdiags := recprovPasses(t, raw, configAWS, configWest)
	if got := passes[0].Result.RecordedElsewhere; len(got) != 1 || got[0].Provider != configWest.String() {
		t.Errorf("the default aws pass's RecordedElsewhere is %+v, want the attachment left to %s", got, configWest)
	}
	if mdiags.HasErrors() {
		t.Fatalf("Merge: %s", mdiags.Err())
	}
	if n := removalsOf(merged, addr); n != 1 {
		t.Errorf("the merge proposes %d removals of %s, want 1:\n%s", n, addr, merged)
	}
	if got, ok := providerOf[addr.String()]; !ok {
		t.Errorf("the merge reads no removal of %s, want it read through %s", addr, configWest)
	} else if got.String() != configWest.String() {
		t.Errorf("the removal is read through %s, want %s", got, configWest)
	}
}

// TestRecordedProviderAddrKeepsAnUnrecognisedSource: only the host is ever
// taken from the configuration. A state provider whose namespace or type the
// configuration does not resolve the type to is recorded as the state wrote
// it, and the module path and alias always are.
func TestRecordedProviderAddrKeepsAnUnrecognisedSourceAWS(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
terraform {
  required_providers {
    aws = {
      source = "example/aws"
    }
  }
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	forked := loadSlotTestConfig(t, dir)
	if got := recordedProviderAddr(forked, "aws_lb_target_group_attachment", stockAWS); got.String() != stockAWS.String() {
		t.Errorf("with aws resolved to example/aws: %s, want the state's %s", got, stockAWS)
	}

	bare := loadSlotTestConfig(t, t.TempDir())
	stockNet := addrs.AbsProviderConfig{Module: addrs.RootModule.Child("net"), Alias: "east", Provider: stockAWS.Provider}
	want := `module.net.provider["registry.opentofu.org/hashicorp/aws"].east`
	if got := recordedProviderAddr(bare, "aws_iam_role", stockNet); got.String() != want {
		t.Errorf("a stock aws address: %s, want %s", got, want)
	}
}
