package projection

import (
	"context"
	"strings"
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

// TestStateCacheReplacesTheRead is the -refresh=false path (issue #712
// gated what was #685's second half): an instance the estate sweep verified
// by marker is answered from the cache and the provider is never asked -
// but ONLY because this build opts in with CacheServesReads, the projection
// of the user's own -refresh=false. Without the opt-in the hit rule stands
// down and every instance reads; TestStateCacheNeverServesADefaultPlan
// below is that half.
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
		// The user's own -refresh=false, without which no hit may occur.
		CacheServesReads: true,
	})
	assertNoErrors(t, diags)

	if got := res.CacheHits(); got != 1 {
		t.Errorf("CacheHits = %d, want 1; the cache was supplied and did not replace the read", got)
	}
	if !res.Has(mustAddr(t, addr)) {
		t.Error("the instance is missing from the projection, so the cache hit produced nothing usable")
	}
}

// TestStateCacheNeverServesADefaultPlan is issue #712's guard, written
// from the smoke test's own catch: a baseline apply wrote a fresh cache,
// an out-of-band edit changed a live attribute, and the next default plan
// did not see the drift, because the hit rule was serving cached
// attributes whenever the sweep vouched for the marker. A default plan
// (no -refresh=false) must read every instance - the read IS drift
// detection - so here the cache holds the PRE-drift object, the live
// system holds the drifted one, and the projection must carry the
// drifted value with zero cache hits.
func TestStateCacheNeverServesADefaultPlan(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")
	const addr = `aws_cloudwatch_log_group.app`
	const id = "/app/logs"

	cloud := newFakeCloud()
	// The live object DRIFTED after the cache was written: name carries
	// the out-of-band edit the plan must surface.
	cloud.putTagged("aws_cloudwatch_log_group", id, map[string]string{
		"id": id, "name": "/app/logs-drifted",
	}, map[string]string{"tofu-estate": ownershipEstate, "tofu-address": addr})

	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, addr), Class: identity.ClassConcrete, ImportID: id},
	}, cloud.providers(t), Options{
		Ownership: &Ownership{
			Estate: ownershipEstate,
			// Verified on purpose: the sweep DID vouch for the marker.
			// Existence and ownership are not the question; attribute
			// freshness is, and only a read answers it.
			Verified: map[string]bool{addr: true},
		},
		// The cache holds the pre-drift object.
		StateCache: cachedStateFor(t, addr, id),
		// No CacheServesReads: this is a default plan.
	})
	assertNoErrors(t, diags)

	if got := res.CacheHits(); got != 0 {
		t.Errorf("CacheHits = %d, want 0; a default plan served a verified instance from the cache, which is how the smoke's drift went invisible (issue #712)", got)
	}
	is := res.State.ResourceInstance(mustAddr(t, addr))
	if is == nil || is.Current == nil {
		t.Fatal("the instance is missing from the projection entirely")
	}
	if got := string(is.Current.AttrsJSON); !strings.Contains(got, "/app/logs-drifted") {
		t.Errorf("projection does not carry the DRIFTED live value:\n%s\nwant it to contain %q; the plan would diff against stale attributes", got, "/app/logs-drifted")
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
