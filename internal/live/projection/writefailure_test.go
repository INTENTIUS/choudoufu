// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #1287. GitHub issue #1283 closed one CAUSE of a record write
// failing - an over-long key - and left the shape standing for every other
// cause: an S3 outage, a denied IAM action, an SSM throttle, a full disk, a
// credential that expired mid-run.
//
// What makes it worse than a failed write is what happens NEXT. The write is
// loud at the time. The read that follows it is not: nothing in the store
// distinguishes "this key was never written" from "this key's write failed a
// moment ago", so the read succeeds, reports absence, and absence is the
// ordinary shape of a resource that does not exist yet. A reader then says
// "The plan will propose creating it" about a live object that already
// exists.
//
// These tests use an injected store error rather than a cause that can be
// arranged for real, because there is no way to arrange an S3 outage in a
// unit test and the cause is not what is under test - the erasure of the
// distinction is.

// writeFailingStore fails every content write while down is true, and is an
// ordinary store otherwise. Deletes are deliberately left alone: a delete
// that failed leaves the record readable and truthful, which is not this
// issue's shape.
type writeFailingStore struct {
	staterecord.Store
	down   bool
	writes int
}

var errSimulatedOutage = errors.New("simulated record-store outage")

func (s *writeFailingStore) PutIfVersion(ctx context.Context, key string, payload []byte, expectedVersion string) (string, error) {
	s.writes++
	if s.down {
		return "", errSimulatedOutage
	}
	return s.Store.PutIfVersion(ctx, key, payload, expectedVersion)
}

func (s *writeFailingStore) PutIfAbsent(ctx context.Context, key string, payload []byte) (string, error) {
	s.writes++
	if s.down {
		return "", errSimulatedOutage
	}
	return s.Store.PutIfAbsent(ctx, key, payload)
}

// GetAll passes the bulk read through, so a [staterecord.RunCache] wrapped
// around this shim takes the same snapshot path a production store gives it
// rather than silently falling back to per-key reads.
func (s *writeFailingStore) GetAll(ctx context.Context, keyPrefix string) (map[string]staterecord.Record, error) {
	return s.Store.(staterecord.BulkReader).GetAll(ctx, keyPrefix)
}

// TestAFailedRecordWriteDoesNotLaterReadAsAbsent is the mechanism in one
// assertion, with no plan and no configuration around it: write, fail, read.
//
// The read is made AFTER the outage passes, which is the honest shape of a
// transient failure and the one that defeats "the read would have failed
// too". The store is perfectly healthy by then; it simply holds nothing,
// because the write that should have put something there did not land.
func TestAFailedRecordWriteDoesNotLaterReadAsAbsent(t *testing.T) {
	ctx := context.Background()
	addr := mustAddr(t, "null_resource.unwritten")
	shim := &writeFailingStore{Store: newTestLocalStore(t), down: true}
	rs := NewRecordEnvelopeStore(shim, seedPrefix)

	_, err := SeedRecordForInstance(ctx, rs, addr, addrs.AbsProviderConfig{},
		cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("already-live-in-the-cloud")}), nil, states.ObjectReady)
	if err == nil {
		t.Fatalf("the premise failed: seeding the record succeeded against a store that refuses writes")
	}
	if shim.writes == 0 {
		t.Fatalf("the premise failed: nothing ever reached the store's write path")
	}

	// The outage passes. Everything from here reads a healthy store.
	shim.down = false

	_, _, keyExists, identityFound, err := rs.GetIdentity(ctx, addr)
	if err == nil {
		t.Fatalf("reading the record after its write failed reported keyExists=%v identityFound=%v and no error; "+
			"\"this run could not write it\" has become \"there is no record\", which every reader treats as "+
			"a resource that does not exist yet", keyExists, identityFound)
	}
	if !strings.Contains(err.Error(), errSimulatedOutage.Error()) {
		t.Errorf("the read's error does not name the write failure that caused it: %s", err)
	}

	// getRaw is the second of the three readers (build.go's projection
	// entry), and getRawFresh the one every read-modify-write uses - so a
	// later concern merging into the same envelope must not read absence
	// either and write a fresh envelope that silently lacks what the failed
	// write was carrying.
	if _, _, _, err := rs.getRaw(ctx, addr); err == nil {
		t.Errorf("getRaw reported no error for an address whose write failed")
	}
	if _, _, _, err := rs.getRawFresh(ctx, addr); err == nil {
		t.Errorf("getRawFresh reported no error for an address whose write failed; a read-modify-write would " +
			"now build a fresh envelope on the assumption that nothing was ever meant to be there")
	}

	// An address whose write never failed is untouched: this is a ledger of
	// what THIS run could not write, not a switch that stops the store.
	other := mustAddr(t, "null_resource.written_fine")
	if _, err := SeedRecordForInstance(ctx, rs, other, addrs.AbsProviderConfig{},
		cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("fine")}), nil, states.ObjectReady); err != nil {
		t.Fatalf("seeding an unrelated address after the outage: %s", err)
	}
	if _, _, exists, err := rs.getRaw(ctx, other); err != nil || !exists {
		t.Errorf("an unrelated address reads as (exists %v, err %v); the ledger must bound itself to the "+
			"addresses whose own write failed", exists, err)
	}
}

// TestAFailedRecordWriteDoesNotBecomeAProposedCreate is the decisive arm,
// and it is deliberately the same shape issue #1283's own decisive arm has:
// a write that failed, then the projection and plan the rest of the run
// builds over that store.
//
// The claim is a PAIR, because only one half of it can be true at once and
// either is acceptable:
//
//   - the run refuses, naming the record it could not read; or
//   - the plan proposes anything other than creating a live resource again.
//
// Before the write-failure ledger existed, neither held: the build produced
// no diagnostic at all, omitted the address with ReasonAbsent and the words
// "The plan will propose creating it", and the plan then did exactly that.
func TestAFailedRecordWriteDoesNotBecomeAProposedCreate(t *testing.T) {
	ctx := context.Background()
	staterecord.ResetRunCacheForTest(t)
	addr := mustAddr(t, "null_resource.trigger")
	cfg := loadConfig(t, writeNullResourceFixture(t))

	// Wrapped the way [NewRecordStore] wraps every production store: the
	// bulk-loading [staterecord.RunCache], whose own doc comment is where
	// #1287 locates the erasure ("a key absent from entries answer[s] 'no
	// record' without a trip"). ResetRunCacheForTest restores the cache's
	// process-wide kill switch, which an earlier test's write would
	// otherwise have thrown for the rest of this binary.
	shim := &writeFailingStore{Store: newTestLocalStore(t), down: true}
	rs := NewRecordEnvelopeStore(staterecord.NewRunCache(shim, seedPrefix), seedPrefix)

	// The migration half, exactly what liveimport's recordOne does for a
	// record-rung instance it found in the state file.
	if _, err := SeedRecordForInstance(ctx, rs, addr, addrs.AbsProviderConfig{},
		cty.ObjectVal(map[string]cty.Value{
			"id":       cty.StringVal("already-live-in-the-cloud"),
			"triggers": cty.MapVal(map[string]cty.Value{"input": cty.StringVal("value")}),
		}), nil, states.ObjectReady); err == nil {
		t.Fatalf("the premise failed: the seed succeeded against a store that refuses writes")
	}

	// The outage passes, and the rest of the run goes on reading a healthy
	// store that happens to hold nothing for this address.
	//
	// Worth being precise about what the cache is and is not doing by now:
	// the failed write above already threw RunCache's process-wide kill
	// switch (every write attempt calls noteWrite, successful or not), so
	// every read below goes straight to the backend. The absence the reader
	// sees is the store's own, not a snapshot's - which is why the fix for
	// this cannot live in RunCache.
	shim.down = false

	res, diags := BuildWith(ctx, cfg,
		[]identity.Resolution{{Addr: addr, Class: identity.ClassRecordBacked}},
		SingleProvider(nullProvider, nullResourceProvider()),
		Options{RecordStore: rs})

	if diags.HasErrors() {
		// First half of the pair: the run refuses rather than planning.
		// That is the outcome the ledger produces, and there is no plan to
		// inspect beyond it.
		if !strings.Contains(diags.Err().Error(), addr.String()) {
			t.Errorf("the refusal does not name %s: %s", addr, diags.Err())
		}
		return
	}

	for _, om := range res.Omitted {
		if om.Addr.Equal(addr) && strings.Contains(om.Detail, "The plan will propose creating it") {
			t.Errorf("the projection omitted %s as absent - %q - although this run is the one that could not "+
				"write its record", addr, om.Detail)
		}
	}

	core, ctxDiags := tofu.NewContext(&tofu.ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("null"): func() (providers.Interface, error) { return nullResourceProvider(), nil },
		}, nil),
	})
	if ctxDiags.HasErrors() {
		t.Fatalf("tofu.NewContext: %s", ctxDiags.Err())
	}
	plan, planDiags := core.Plan(ctx, cfg, res.State, &tofu.PlanOpts{Mode: plans.NormalMode})
	if planDiags.HasErrors() {
		t.Fatalf("planning against the projected state: %s", planDiags.Err())
	}
	change := plan.Changes.ResourceInstance(addr)
	if change != nil && change.Action == plans.Create {
		t.Fatalf("the plan proposes CREATING %s, which already exists live, and the run raised no error on the "+
			"way: the record write failed and the read that followed it reported an ordinary absence", addr)
	}
}
