// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// twoRegionResolutions is the fixture's three declared instances, every one
// of them client-named (ClassConcrete): exactly the population the vouch
// pass exists for, since nothing here is waiting on marker discovery and
// the ordinary config-driven scan therefore lists none of it.
//
// The two log groups share one import ID on purpose - that is the mirroring
// the issue is about.
func twoRegionResolutions(t *testing.T) []identity.Resolution {
	t.Helper()
	return []identity.Resolution{
		{Addr: mustAddr(t, "aws_cloudwatch_log_group.east"), Class: identity.ClassConcrete, ImportID: "/app/logs"},
		{Addr: mustAddr(t, "aws_cloudwatch_log_group.west"), Class: identity.ClassConcrete, ImportID: "/app/logs"},
		{Addr: mustAddr(t, "aws_kms_key.east"), Class: identity.ClassConcrete, ImportID: "key-east"},
	}
}

// listCallsFor counts the list calls one pass made for a type.
func listCallsFor(c *fakeCloud, typeName string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, r := range c.requests {
		if r.TypeName == typeName {
			n++
		}
	}
	return n
}

// TestCacheVouchPassListsOnlyItsOwnScopesTypes is the adjacent cost the
// second reviewer recorded on issue #745: decl.all is populated from every
// resolution before the ScopeProvider filter, so every pass vouch-listed
// every vouch type - one duplicate list per type per extra provider
// configuration, against an account that declares none of it.
//
// aws_kms_key is declared under the default (east) configuration alone, so
// the west pass has no instance of it to vouch and must not list it. The
// log group, declared on both sides, is the control: pruning by scope must
// not prune a type this pass really does own.
func TestCacheVouchPassListsOnlyItsOwnScopesTypes(t *testing.T) {
	cfg := loadConfig(t, "testdata/vouch-two-region")
	resolutions := twoRegionResolutions(t)
	vouchTypes := []string{"aws_cloudwatch_log_group", "aws_kms_key"}

	east := testProviderAddr(t, "")
	west := testProviderAddr(t, "west")

	for _, tc := range []struct {
		name     string
		scope    addrs.AbsProviderConfig
		wantKMS  int
		wantLogs int
	}{
		{name: "east declares both types", scope: east, wantKMS: 1, wantLogs: 1},
		{name: "west declares only the log group", scope: west, wantKMS: 0, wantLogs: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cloud := newFakeCloud()
			cloud.listable("aws_cloudwatch_log_group")
			cloud.noFilter("aws_cloudwatch_log_group")

			_, diags := Discover(context.Background(), Request{
				Estate:          estateName,
				Config:          cfg,
				Resolutions:     resolutions,
				Provider:        cloud,
				ScopeProvider:   tc.scope,
				CacheVouchTypes: vouchTypes,
			})
			assertNoErrors(t, diags)

			if got := listCallsFor(cloud, "aws_kms_key"); got != tc.wantKMS {
				t.Errorf("aws_kms_key list calls = %d, want %d; this pass's provider configuration declares %d instance(s) of it", got, tc.wantKMS, tc.wantKMS)
			}
			if got := listCallsFor(cloud, "aws_cloudwatch_log_group"); got != tc.wantLogs {
				t.Errorf("aws_cloudwatch_log_group list calls = %d, want %d", got, tc.wantLogs)
			}
		})
	}
}

// TestCacheVouchSightingsStayWithTheirPass is issue #745 at the layer that
// produces the evidence. Two passes over one estate that mirrors a log
// group name into two regions: the east object has been deleted out of
// band, so the east pass lists its own region and sights nothing, while the
// west pass sights the west object of the same name. Merge must keep those
// two facts apart, because they are facts about two different accounts.
//
// The assertions are on the merged evidence by value - which provider
// configuration vouched which identity - not on whether some flag is set.
func TestCacheVouchSightingsStayWithTheirPass(t *testing.T) {
	cfg := loadConfig(t, "testdata/vouch-two-region")
	resolutions := twoRegionResolutions(t)
	vouchTypes := []string{"aws_cloudwatch_log_group"}

	east := testProviderAddr(t, "")
	west := testProviderAddr(t, "west")

	// The east region: the log group is gone. Nothing to list, nothing to
	// sight.
	eastCloud := newFakeCloud()
	eastCloud.listable("aws_cloudwatch_log_group")
	eastCloud.noFilter("aws_cloudwatch_log_group")

	// The west region: the mirrored log group is still there, unmarked,
	// answering to the same import identity the east instance uses.
	westCloud := newFakeCloud()
	westCloud.listable("aws_cloudwatch_log_group")
	westCloud.noFilter("aws_cloudwatch_log_group")
	westCloud.obj("aws_cloudwatch_log_group", "/app/logs", map[string]string{"Name": "app"})

	pass := func(provider addrs.AbsProviderConfig, cloud *fakeCloud) Pass {
		t.Helper()
		res, diags := Discover(context.Background(), Request{
			Estate:          estateName,
			Config:          cfg,
			Resolutions:     resolutions,
			Provider:        cloud,
			ScopeProvider:   provider,
			VouchProvider:   provider,
			CacheVouchTypes: vouchTypes,
		})
		assertNoErrors(t, diags)
		return Pass{Provider: provider, Result: res}
	}

	merged, _, diags := Merge(estateName, []Pass{pass(east, eastCloud), pass(west, westCloud)})
	assertNoErrors(t, diags)

	if merged.CacheVouchSightings.Sighted(east, "aws_cloudwatch_log_group", "/app/logs") {
		t.Errorf("the west region's object vouched existence for the east instance; merged sightings: %v", merged.CacheVouchSightings)
	}
	if !merged.CacheVouchSightings.Sighted(west, "aws_cloudwatch_log_group", "/app/logs") {
		t.Errorf("the west pass's own sighting was lost in the merge; merged sightings: %v", merged.CacheVouchSightings)
	}
}
