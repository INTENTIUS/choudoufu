// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// GitHub issue #1301. [Store.List]'s contract is "every key currently stored
// whose name begins with keyPrefix". [RunCache] has one way to break it: its
// entries map holds negative entries - a key read individually and found
// absent, remembered as cacheEntry{exists: false} so the second read of the
// same missing key is free - and the snapshot branch of List enumerates that
// map.
//
// A negative entry only ever lands for a key inside the snapshot namespace
// when the bulk read had not yet succeeded, because a loaded snapshot
// answers every in-namespace miss without ever reaching the per-key path
// ([RunCache.Get]'s !ok && c.loaded && c.covers branch). So the sequence
// that produces a listable key holding no record is exactly: bulk read
// fails, a miss is read per-key and remembered, bulk read is retried by the
// next Get and succeeds. [RunCache.ensureLoaded] retries on every Get, so
// one transient GetAll failure is enough - a throttled
// GetParametersByPath, a timed-out ListObjectsV2.
//
// flakyBulkStore below is that transient failure and nothing else: every
// other operation, the per-key Get included, goes to a real [LocalStore], so
// what these tests drive is the production object over a production backend.

// flakyBulkStore is a Store whose GetAll fails its first failN calls and
// then behaves exactly like the store beneath it. Everything else - Get,
// List, the writes - passes straight through, so the only fault injected is
// the transient bulk-read failure.
type flakyBulkStore struct {
	Store
	inner  *LocalStore
	failN  int
	calls  int
	getAll int
}

func (s *flakyBulkStore) GetAll(ctx context.Context, keyPrefix string) (map[string]Record, error) {
	s.calls++
	if s.calls <= s.failN {
		return nil, errors.New("simulated transient bulk-read failure")
	}
	s.getAll++
	return s.inner.GetAll(ctx, keyPrefix)
}

// newFlakyCache seeds present under a real local store, wraps it in a
// GetAll that fails failN times, and returns the RunCache over that plus the
// backend itself so a test can compare the cache's answer against the
// store's own.
func newFlakyCache(t *testing.T, failN int, present ...string) (Store, *LocalStore) {
	t.Helper()
	resetRunCacheState(t)
	inner, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	ctx := context.Background()
	for _, key := range present {
		if _, err := inner.PutIfAbsent(ctx, key, []byte(`{"key":"`+key+`"}`)); err != nil {
			t.Fatalf("seeding %q: %v", key, err)
		}
	}
	// The seed writes went to the local store directly, beneath any cache,
	// so the process-wide write switch is still off.
	return NewRunCache(&flakyBulkStore{Store: inner, inner: inner, failN: failN}, testPrefix), inner
}

const (
	phantomKey = testPrefix + "aws_thing/ghost"
	realKey    = testPrefix + "aws_thing/real"
)

// TestRunCacheListOmitsAKeyThatHoldsNoRecord is the decisive arm: a
// transient GetAll failure, a per-key miss remembered while the snapshot was
// still unloaded, then a successful GetAll - after which List must name the
// one key that is stored and not the one that never was.
func TestRunCacheListOmitsAKeyThatHoldsNoRecord(t *testing.T) {
	ctx := context.Background()
	cached, inner := newFlakyCache(t, 1, realKey)

	// First Get: the bulk read fails, so this falls through to the per-key
	// path and remembers "no record at phantomKey".
	if _, _, exists, err := cached.Get(ctx, phantomKey); err != nil || exists {
		t.Fatalf("Get(%q) = (exists %v, err %v), want (false, nil)", phantomKey, exists, err)
	}
	// Second Get: ensureLoaded retries and this time the snapshot lands.
	if _, _, exists, err := cached.Get(ctx, realKey); err != nil || !exists {
		t.Fatalf("Get(%q) = (exists %v, err %v), want (true, nil)", realKey, exists, err)
	}

	keys, err := cached.List(ctx, testPrefix)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if slices.Contains(keys, phantomKey) {
		t.Errorf("List returned %v, which names %q - a key that holds no record and never did. "+
			"Store.List's contract is every key CURRENTLY STORED under the prefix; a caller that turns "+
			"keys into addresses without re-reading each one inherits a resource that does not exist "+
			"(issue #1301)", keys, phantomKey)
	}

	// The backend is the oracle: whatever the cache says, it must be what
	// the store beneath it would have said.
	want, err := inner.List(ctx, testPrefix)
	if err != nil {
		t.Fatalf("backend List: %v", err)
	}
	if !slices.Equal(keys, want) {
		t.Errorf("List through the cache returned %v; the store beneath it returns %v", keys, want)
	}
}

// TestRunCacheListStillNamesEveryStoredKey is the other direction, so the
// fix cannot be "return fewer keys". A negative entry sitting in the map
// must not cost the listing any key that IS stored, including one the same
// run read individually before the snapshot loaded.
func TestRunCacheListStillNamesEveryStoredKey(t *testing.T) {
	ctx := context.Background()
	second := testPrefix + "aws_thing/second"
	cached, inner := newFlakyCache(t, 1, realKey, second)

	// A miss and a hit, both served per-key while the bulk read was still
	// failing, then a Get that lets the snapshot land.
	if _, _, exists, err := cached.Get(ctx, phantomKey); err != nil || exists {
		t.Fatalf("Get(%q) = (exists %v, err %v), want (false, nil)", phantomKey, exists, err)
	}
	if _, _, exists, err := cached.Get(ctx, realKey); err != nil || !exists {
		t.Fatalf("Get(%q) = (exists %v, err %v), want (true, nil)", realKey, exists, err)
	}
	if _, _, exists, err := cached.Get(ctx, second); err != nil || !exists {
		t.Fatalf("Get(%q) = (exists %v, err %v), want (true, nil)", second, exists, err)
	}

	keys, err := cached.List(ctx, testPrefix)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want, err := inner.List(ctx, testPrefix)
	if err != nil {
		t.Fatalf("backend List: %v", err)
	}
	if !slices.Equal(keys, want) {
		t.Errorf("List returned %v, want %v", keys, want)
	}
}

// TestRunCacheListAgreesWithGetAfterAFlakyBulkRead is the invariant the
// whole class reduces to, and the one a future caller is entitled to lean
// on: every key List names answers exists=true when Get is asked about it.
// A listing and a read that disagree is the defect whatever produced it, so
// this holds for any entry the map is left holding, not only for the one
// sequence above.
func TestRunCacheListAgreesWithGetAfterAFlakyBulkRead(t *testing.T) {
	ctx := context.Background()
	cached, _ := newFlakyCache(t, 1, realKey)

	for _, key := range []string{phantomKey, testPrefix + "aws_thing/ghost2", realKey} {
		if _, _, _, err := cached.Get(ctx, key); err != nil {
			t.Fatalf("Get(%q): %v", key, err)
		}
	}

	keys, err := cached.List(ctx, testPrefix)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) == 0 {
		t.Fatalf("List returned nothing; the namespace holds %q", realKey)
	}
	for _, key := range keys {
		_, _, exists, err := cached.Get(ctx, key)
		if err != nil {
			t.Fatalf("Get(%q) after List named it: %v", key, err)
		}
		if !exists {
			t.Errorf("List named %q but Get says no record is stored there", key)
		}
	}
}

// TestRunCacheListCacheHoldsOnlyStoredKeys asks issue #1301's second
// question - whether the non-snapshot lists cache can hold a phantom too.
// It cannot by construction: c.lists is written only from the wrapped
// store's own List result, never from entries. This pins that, because the
// obvious "optimization" of answering an uncovered prefix out of entries
// would reintroduce exactly the defect above one branch further down.
func TestRunCacheListCacheHoldsOnlyStoredKeys(t *testing.T) {
	ctx := context.Background()
	// prefix "" disables the bulk load entirely, so every List here takes
	// the lists-cache branch.
	resetRunCacheState(t)
	inner, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	if _, err := inner.PutIfAbsent(ctx, realKey, []byte(`{}`)); err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}
	cached := NewRunCache(inner, "")

	// A miss, remembered as a negative entry, before anything is listed.
	if _, _, exists, err := cached.Get(ctx, phantomKey); err != nil || exists {
		t.Fatalf("Get(%q) = (exists %v, err %v), want (false, nil)", phantomKey, exists, err)
	}
	for i := 0; i < 2; i++ {
		keys, err := cached.List(ctx, testPrefix)
		if err != nil {
			t.Fatalf("List %d: %v", i, err)
		}
		if !slices.Equal(keys, []string{realKey}) {
			t.Errorf("List %d returned %v, want %v", i, keys, []string{realKey})
		}
	}
}
