// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
)

// twoRegionProviders serves every provider configuration the two-region
// fixture names from one mock, which is what [SingleProvider] cannot do:
// the fixture declares an aliased second configuration, and the point of
// these tests is which of the two a piece of evidence came from.
func twoRegionProviders(t *testing.T, c *fakeCloud) Providers {
	t.Helper()
	p := c.provider(t)
	return ProviderFunc(func(_ context.Context, _ addrs.AbsProviderConfig) (providers.Interface, error) {
		return p, nil
	})
}

// westProviderKey is the aliased provider configuration testdata/two-region
// declares its west log group under, rendered the way
// [addrs.AbsProviderConfig.String] renders it.
const westProviderKey = `provider["registry.opentofu.org/hashicorp/aws"].west`

// eastProviderKey is the fixture's default (unaliased) configuration.
const eastProviderKey = `provider["registry.opentofu.org/hashicorp/aws"]`

// TestEnvelopeVouchIsPerProviderPass is issue #745. A multi-region estate
// mirrors one client-chosen log group name into two regions. The east
// object is deleted out of band; the west object of the same name is still
// there, unmarked, and the run's listing pass sights it. The record
// envelope still attests east's identity - a record is not ownership
// evidence about a live object, it is what the last run wrote - so the
// only leg of the envelope arm that can notice the deletion is the
// sighting, and a sighting from the west pass is not evidence about east.
//
// The assertion is the read, not a predicate: an instance the arm may not
// vouch takes the ordinary read, the read finds nothing, and the plan
// proposes creating it instead of reporting a dead instance unchanged.
func TestEnvelopeVouchIsPerProviderPass(t *testing.T) {
	const addr = `aws_cloudwatch_log_group.east`
	const id = "/app/logs"
	ctx := context.Background()

	cfg := loadConfig(t, "testdata/two-region")
	// The east object is GONE: nothing in the fake cloud answers to it, so
	// a real read comes back null and the instance is omitted.
	cloud := newFakeCloud()

	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix(ownershipEstate))
	if _, err := store.mergeEnvelope(ctx, mustAddr(t, addr), "", func(env *recordEnvelope) {
		env.Identity = &identityPayload{ImportID: id}
	}); err != nil {
		t.Fatalf("seeding the identity record: %s", err)
	}

	res, diags := BuildWith(ctx, cfg, []identity.Resolution{
		{Addr: mustAddr(t, addr), Class: identity.ClassConcrete, ImportID: id},
	}, twoRegionProviders(t, cloud), Options{
		Ownership:        &Ownership{Estate: ownershipEstate, Verified: map[string]bool{}},
		StateCache:       cachedStateFor(t, addr, id),
		CacheServesReads: true,
		RecordStore:      store,
		// The west pass's sighting, and only it: the east pass listed its
		// own region and saw nothing.
		CacheVouchSightings: VouchSightings{
			westProviderKey: {"aws_cloudwatch_log_group": {id: true}},
		},
		EnvelopeVouchServes: true,
	})
	assertNoErrors(t, diags)

	if got := res.CacheHits(); got != 0 {
		t.Errorf("CacheHits = %d, want 0; another region's sighting of the same name vouched existence for this instance", got)
	}
	if res.Has(mustAddr(t, addr)) {
		t.Error("the deleted instance is in the projection as prior state, so the plan reports it unchanged instead of proposing a create")
	}
}

// TestEnvelopeVouchServesItsOwnPassesSighting is the control that keeps the
// test above from passing for the wrong reason. Same fixture, same deleted-
// object shape inverted: the sighting comes from the instance's OWN
// provider configuration, so the arm must still serve. Without this, a
// change that simply switched the envelope arm off would read as a fix.
func TestEnvelopeVouchServesItsOwnPassesSighting(t *testing.T) {
	const addr = `aws_cloudwatch_log_group.east`
	const id = "/app/logs"
	ctx := context.Background()

	cfg := loadConfig(t, "testdata/two-region")
	cloud := newFakeCloud()
	cloud.putTagged("aws_cloudwatch_log_group", id, map[string]string{
		"id": id, "name": id,
	}, map[string]string{"tofu-estate": ownershipEstate, "tofu-address": addr})

	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix(ownershipEstate))
	if _, err := store.mergeEnvelope(ctx, mustAddr(t, addr), "", func(env *recordEnvelope) {
		env.Identity = &identityPayload{ImportID: id}
	}); err != nil {
		t.Fatalf("seeding the identity record: %s", err)
	}

	res, diags := BuildWith(ctx, cfg, []identity.Resolution{
		{Addr: mustAddr(t, addr), Class: identity.ClassConcrete, ImportID: id},
	}, twoRegionProviders(t, cloud), Options{
		Ownership:        &Ownership{Estate: ownershipEstate, Verified: map[string]bool{}},
		StateCache:       cachedStateFor(t, addr, id),
		CacheServesReads: true,
		RecordStore:      store,
		CacheVouchSightings: VouchSightings{
			eastProviderKey: {"aws_cloudwatch_log_group": {id: true}},
		},
		EnvelopeVouchServes: true,
	})
	assertNoErrors(t, diags)

	if got := res.CacheHits(); got != 1 {
		t.Errorf("CacheHits = %d, want 1; the instance's own pass sighted it and the record attests it, so the arm must still serve", got)
	}
}
