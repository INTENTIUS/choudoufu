// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// GitHub issue #1301, at the seam every consumer of a record-store listing
// actually goes through. [RecordStore.List] is the one chokepoint: four
// production call sites reach it - [builder.discoverOrphanedRecords] here,
// discovery's recordOrphanReadSweep and estateScopedNativeSweep, and
// live-mv's module-boundary record sweep - and every one of them turns the
// keys it gets back into addresses.
//
// Three of those four re-read each key before acting on it and skip an
// absent one, so the phantom #1301 describes was contained in production.
// Containment by a downstream guard is not the contract, though: this pins
// the contract itself, so that a caller which does NOT re-read - the fourth
// one, estateScopedNativeSweep, only ever reads the key's TYPE out of the
// key string - is right without needing one.
func TestRecordStoreListNamesNoKeyThatHoldsNoRecord(t *testing.T) {
	staterecord.ResetRunCacheForTest(t)
	ctx := context.Background()

	const estate = "phantom-estate"
	prefix := RecordKeyPrefix(estate)

	backend, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}

	// One real record, seeded straight into the backend so the write never
	// passes through the cache and cannot switch it off.
	realAddr := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "aws_sqs_queue", Name: "real"}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
	if _, err := SeedLocatedForInstance(ctx, NewRecordEnvelopeStore(backend, prefix), realAddr, addrs.AbsProviderConfig{}, LocatedRecord{
		Components: map[string]string{"name": "real"},
	}); err != nil {
		t.Fatalf("seeding the real record: %s", err)
	}

	// The production stack, with one transient bulk-read failure injected -
	// a throttled GetParametersByPath, a timed-out ListObjectsV2. Nothing
	// else is faked.
	flaky := &flakyBulkBackend{Store: backend, inner: backend, failFirst: 1}
	store := NewRecordEnvelopeStore(staterecord.NewRunCache(flaky, prefix), prefix)

	// The sequence #1301 needs, and the only one that produces a phantom:
	// a read of an absent in-namespace key while the bulk read is still
	// failing (remembered as "no record"), then a read that lets the retry
	// land the snapshot.
	ghostAddr := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "aws_sqs_queue", Name: "ghost"}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
	if _, _, exists, _, err := store.GetIdentity(ctx, ghostAddr); err != nil || exists {
		t.Fatalf("reading the never-written record for %s = (exists %v, err %v), want (false, nil)", ghostAddr, exists, err)
	}
	if _, _, exists, _, err := store.GetIdentity(ctx, realAddr); err != nil {
		t.Fatalf("reading %s: %s", realAddr, err)
	} else if !exists {
		t.Fatalf("the seeded record for %s reads as absent, so this test's premise is gone", realAddr)
	}
	if flaky.served == 0 {
		t.Fatalf("the bulk read never succeeded, so the snapshot never loaded and this test proves nothing about a loaded one")
	}

	keys, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %s", err)
	}
	ghostKey := RecordKey(prefix, ghostAddr)
	if slices.Contains(keys, ghostKey) {
		t.Errorf("List returned %v, naming %q. RecordAddr decodes that to %s, a resource this estate has never had a record of. "+
			"discoverOrphanedRecords survives it only because peekKind reads the key again and its !kindExists branch skips it; "+
			"estateScopedNativeSweep never re-reads at all (issue #1301)", keys, ghostKey, ghostAddr)
	}

	// And the listing is still the backend's own answer, not a shorter one.
	want, err := backend.List(ctx, prefix)
	if err != nil {
		t.Fatalf("backend List: %s", err)
	}
	if !slices.Equal(keys, want) {
		t.Errorf("List returned %v; the store beneath the cache holds %v", keys, want)
	}
}

// flakyBulkBackend fails its first failFirst GetAll calls and then delegates.
// Every other operation goes straight to the real local store.
type flakyBulkBackend struct {
	staterecord.Store
	inner     *staterecord.LocalStore
	failFirst int
	calls     int
	served    int
}

func (s *flakyBulkBackend) GetAll(ctx context.Context, keyPrefix string) (map[string]staterecord.Record, error) {
	s.calls++
	if s.calls <= s.failFirst {
		return nil, errors.New("simulated transient bulk-read failure")
	}
	s.served++
	return s.inner.GetAll(ctx, keyPrefix)
}
