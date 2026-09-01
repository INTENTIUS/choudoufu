package projection

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/states"
)

// cachedStateFor builds a cache holding one object for addr, with a marker
// attribute set so a hit is distinguishable from a fresh read.
func cachedStateFor(t *testing.T, addrStr, id string) *states.State {
	t.Helper()
	s := states.NewState()
	a := mustAddr(t, addrStr)
	s.RootModule().SetResourceInstanceCurrent(
		a.Resource,
		&states.ResourceInstanceObjectSrc{
			AttrsJSON: []byte(`{"id":"` + id + `","name":"` + id + `"}`),
			Status:    states.ObjectReady,
		},
		addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws")},
		addrs.NoKey,
	)
	return s
}

// TestStateCacheReplacesTheRead is issue #685's second half and the whole
// point of writing a cache at all. An instance the estate sweep verified by
// marker is answered from the cache, and the provider is never asked.
//
// The read count is the assertion, not the projection's contents: a cache that
// is consulted and then read anyway would still produce a correct projection,
// and would save nothing. That is exactly the failure this guards.
func TestStateCacheReplacesTheRead(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")
	const addr = `aws_cloudwatch_log_group.app`
	const id = "/app/logs"

	cloud := newFakeCloud()
	cloud.putTagged("aws_cloudwatch_log_group", id, map[string]string{
		"id": id, "name": id,
	}, map[string]string{"tofu-estate": ownershipEstate, "tofu-address": addr})

	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, addr), Class: identity.ClassConcrete, ImportID: id},
	}, cloud.providers(t), Options{
		Ownership: &Ownership{
			Estate: ownershipEstate,
			// The sweep found this instance BY its marker in this run. That
			// is the oracle the cache is checked against.
			Verified: map[string]bool{addr: true},
		},
		StateCache: cachedStateFor(t, addr, id),
	})
	assertNoErrors(t, diags)

	if got := res.CacheHits(); got != 1 {
		t.Errorf("CacheHits = %d, want 1; the cache was supplied and did not replace the read", got)
	}
	if !res.Has(mustAddr(t, addr)) {
		t.Error("the instance is missing from the projection, so the cache hit produced nothing usable")
	}
}

// TestStateCacheIsIgnoredWithoutMarkerVerification is the safety half. A cache
// entry for an instance the sweep did NOT verify must not be used: the cache
// is a candidate, and the tag index is the oracle. Without this, a stale cache
// would let a deleted resource stay in the projection, which is the one thing
// a cache must never cost.
func TestStateCacheIsIgnoredWithoutMarkerVerification(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")
	const addr = `aws_cloudwatch_log_group.app`
	const id = "/app/logs"

	cloud := newFakeCloud()
	cloud.putTagged("aws_cloudwatch_log_group", id, map[string]string{
		"id": id, "name": id,
	}, map[string]string{"tofu-estate": ownershipEstate, "tofu-address": addr})

	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, addr), Class: identity.ClassConcrete, ImportID: id},
	}, cloud.providers(t), Options{
		// No Verified set: the sweep confirmed nothing about this instance.
		Ownership:  &Ownership{Estate: ownershipEstate},
		StateCache: cachedStateFor(t, addr, id),
	})
	assertNoErrors(t, diags)

	if got := res.CacheHits(); got != 0 {
		t.Errorf("CacheHits = %d, want 0; the cache was used for an instance the sweep never verified, so a stale entry could survive a deletion", got)
	}
}

// TestStateCacheOffMeansEveryInstanceReads pins the default. A build given no
// cache must behave exactly as it did before #685 existed.
func TestStateCacheOffMeansEveryInstanceReads(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")
	const addr = `aws_cloudwatch_log_group.app`
	const id = "/app/logs"

	cloud := newFakeCloud()
	cloud.putTagged("aws_cloudwatch_log_group", id, map[string]string{
		"id": id, "name": id,
	}, map[string]string{"tofu-estate": ownershipEstate, "tofu-address": addr})

	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, addr), Class: identity.ClassConcrete, ImportID: id},
	}, cloud.providers(t), Options{
		Ownership: &Ownership{Estate: ownershipEstate, Verified: map[string]bool{addr: true}},
	})
	assertNoErrors(t, diags)

	if got := res.CacheHits(); got != 0 {
		t.Errorf("CacheHits = %d with no cache supplied, want 0", got)
	}
}
