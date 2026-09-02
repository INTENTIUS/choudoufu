// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// resetRunCacheState clears the process-wide "something has been written"
// switch, which is sticky by design and would otherwise let one test's write
// silently turn every later test's cache off - a green run proving nothing.
func resetRunCacheState(t *testing.T) {
	t.Helper()
	wroteSomething.Store(false)
	t.Cleanup(func() { wroteSomething.Store(false) })
}

const testPrefix = "tofu-records/estate"

// TestRunCacheConformance is the strongest guard here: a cache that changes
// what a Store means is not a cache, it is a bug with better latency. The
// wrapped store has to satisfy the same contract every backend does - missing
// keys, version conflicts, deletes and all - both with a snapshot namespace
// configured and without one.
func TestRunCacheConformance(t *testing.T) {
	for _, prefix := range []string{"", testPrefix} {
		t.Run(fmt.Sprintf("prefix=%q", prefix), func(t *testing.T) {
			runConformance(t, func(t *testing.T) Store {
				resetRunCacheState(t)
				inner, err := NewLocalStore(t.TempDir())
				if err != nil {
					t.Fatalf("NewLocalStore: %v", err)
				}
				return NewRunCache(inner, prefix)
			})
		})
	}
}

// TestCountingStoreConformance holds the counter to the same bar for the same
// reason: it sits in the production path whenever the trip log is on.
func TestCountingStoreConformance(t *testing.T) {
	runConformance(t, func(t *testing.T) Store {
		inner, err := NewLocalStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewLocalStore: %v", err)
		}
		return NewCountingStore(inner, nil)
	})
}

// countedCache builds the production stack - a counter under a cache - over a
// fresh local store seeded with n records under the snapshot namespace, and
// hands back both halves with the counter already zeroed.
func countedCache(t *testing.T, n int) (Store, *CountingStore) {
	t.Helper()
	resetRunCacheState(t)
	inner, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	ctx := context.Background()
	for i := 0; i < n; i++ {
		if _, err := inner.PutIfAbsent(ctx, recKey(i), []byte(fmt.Sprintf("payload-%d", i))); err != nil {
			t.Fatalf("seeding %s: %v", recKey(i), err)
		}
	}
	counting := NewCountingStore(inner, nil)
	return NewRunCache(counting, testPrefix), counting
}

func recKey(i int) string { return fmt.Sprintf("%s/aws_thing/key%02d", testPrefix, i) }

// TestRunCacheLoadsTheNamespaceInOneTrip is the reduction the whole exercise
// is for: reading every instance's record, several times each, costs ONE call
// to the store - what stock pays to read its state file - rather than one per
// instance or one per accessor.
func TestRunCacheLoadsTheNamespaceInOneTrip(t *testing.T) {
	ctx := context.Background()
	const n = 20
	cached, counting := countedCache(t, n)

	for pass := 0; pass < 4; pass++ {
		for i := 0; i < n; i++ {
			payload, _, exists, err := cached.Get(ctx, recKey(i))
			if err != nil {
				t.Fatalf("Get %s: %v", recKey(i), err)
			}
			if !exists {
				t.Fatalf("Get %s: exists=false, want true", recKey(i))
			}
			if want := fmt.Sprintf("payload-%d", i); string(payload) != want {
				t.Fatalf("Get %s returned %q, want %q", recKey(i), payload, want)
			}
		}
	}
	if got := counting.Total(); got != 1 {
		t.Errorf("%d records read %d times each cost %d trips, want 1: %+v", n, 4, got, counting.Trips())
	}
	if trips := counting.Trips(); len(trips) == 1 && trips[0].Method != "GetAll" {
		t.Errorf("the one trip was a %s, want a GetAll", trips[0].Method)
	}
}

// TestRunCacheAnswersAMissFromTheSnapshot covers the half a hit-only cache
// leaves behind. The scale-1 estate has 79 declared addresses and 78 record
// files; every read of the missing one returns "nothing recorded", and there
// are six of them.
func TestRunCacheAnswersAMissFromTheSnapshot(t *testing.T) {
	ctx := context.Background()
	cached, counting := countedCache(t, 5)

	// Six DIFFERENT absent keys, not one read six times: a per-key cache
	// answers the repeat for free too, and only a snapshot that knows its
	// namespace is complete can answer keys it has never been asked about.
	for i := 0; i < 6; i++ {
		missing := fmt.Sprintf("%s/aws_thing/never-written-%d", testPrefix, i)
		_, version, exists, err := cached.Get(ctx, missing)
		if err != nil {
			t.Fatalf("Get %d: %v", i, err)
		}
		if exists || version != "" {
			t.Fatalf("Get %d says a key that was never written exists (version %q)", i, version)
		}
	}
	if got := counting.Total(); got != 1 {
		t.Errorf("six reads of six absent keys inside a loaded namespace cost %d trips, want 1 (the bulk load)", got)
	}
}

// TestRunCacheListsFromTheSnapshot: a namespace already loaded knows its own
// key set, and the plan enumerates it three times.
func TestRunCacheListsFromTheSnapshot(t *testing.T) {
	ctx := context.Background()
	cached, counting := countedCache(t, 7)

	// One read first, so the namespace is already loaded: a snapshot answers
	// the enumerations from what it holds, where a per-key list cache would
	// still pay one trip for the first List.
	if _, _, _, err := cached.Get(ctx, recKey(0)); err != nil {
		t.Fatalf("Get: %v", err)
	}
	for i := 0; i < 3; i++ {
		keys, err := cached.List(ctx, testPrefix)
		if err != nil {
			t.Fatalf("List %d: %v", i, err)
		}
		if len(keys) != 7 {
			t.Fatalf("List %d returned %d keys, want 7", i, len(keys))
		}
	}
	if got := counting.Total(); got != 1 {
		t.Errorf("a read plus three enumerations of one namespace cost %d trips, want 1", got)
	}
}

// TestRunCacheIsOffAfterTheFirstWrite is the safety rule stated as a test.
// Nothing the cache serves was ever read after something was written, so no
// read-modify-write, no compare-and-swap and no seeder can be handed a
// remembered value - whatever order they happen to run in.
func TestRunCacheIsOffAfterTheFirstWrite(t *testing.T) {
	ctx := context.Background()
	cached, counting := countedCache(t, 3)

	if _, _, _, err := cached.Get(ctx, recKey(0)); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := counting.Total(); got != 1 {
		t.Fatalf("the first read cost %d trips, want 1", got)
	}

	// Any write, to any key, anywhere.
	if _, err := cached.PutIfAbsent(ctx, testPrefix+"/aws_thing/new", []byte("x")); err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}
	counting.Reset()

	for i := 0; i < 3; i++ {
		if _, _, _, err := cached.Get(ctx, recKey(0)); err != nil {
			t.Fatalf("Get after the write: %v", err)
		}
	}
	if got := counting.Total(); got != 3 {
		t.Errorf("three reads after a write cost %d trips, want 3 - the cache is still serving", got)
	}
}

// TestRunCacheIsOffAfterAnotherCachesWrite is the cross-instance half of the
// same rule: discovery builds a fresh wrapper per call site, and a value
// served by the wrapper that did not make the write is exactly as stale as
// one served by the wrapper that did.
func TestRunCacheIsOffAfterAnotherCachesWrite(t *testing.T) {
	ctx := context.Background()
	resetRunCacheState(t)
	inner, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	counting := NewCountingStore(inner, nil)
	a := NewRunCache(counting, testPrefix)
	b := NewRunCache(counting, testPrefix)

	if _, _, exists, err := a.Get(ctx, recKey(0)); err != nil || exists {
		t.Fatalf("Get through a: exists=%v err=%v, want false/nil", exists, err)
	}
	if _, err := b.PutIfAbsent(ctx, recKey(0), []byte("written-by-b")); err != nil {
		t.Fatalf("PutIfAbsent through b: %v", err)
	}
	payload, _, exists, err := a.Get(ctx, recKey(0))
	if err != nil {
		t.Fatalf("Get through a after b wrote: %v", err)
	}
	if !exists || string(payload) != "written-by-b" {
		t.Errorf("cache a served exists=%v payload=%q after cache b wrote the key, want true/%q", exists, payload, "written-by-b")
	}
}

// TestFreshUnwrapsEveryCache is what the three must-never-be-cached read
// classes rely on. If this returned the cache, projection's mergeEnvelope,
// currentVersion and the seeders would all silently read a snapshot.
func TestFreshUnwrapsEveryCache(t *testing.T) {
	resetRunCacheState(t)
	inner, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	counting := NewCountingStore(inner, nil)
	stacked := NewRunCache(NewRunCache(counting, testPrefix), testPrefix)
	if got := Fresh(stacked); got != Store(counting) {
		t.Errorf("Fresh returned %T, want the store beneath every cache", got)
	}
	if got := Fresh(counting); got != Store(counting) {
		t.Errorf("Fresh on an uncached store returned %T, want it unchanged", got)
	}
}

// TestRunCacheFreshReadSkipsTheSnapshot proves the bypass end to end: a read
// through [Fresh] reaches the store even while the snapshot holds the key.
func TestRunCacheFreshReadSkipsTheSnapshot(t *testing.T) {
	ctx := context.Background()
	cached, counting := countedCache(t, 3)

	if _, _, _, err := cached.Get(ctx, recKey(0)); err != nil { // loads the snapshot
		t.Fatalf("Get: %v", err)
	}
	counting.Reset()

	if _, _, _, err := Fresh(cached).Get(ctx, recKey(0)); err != nil {
		t.Fatalf("fresh Get: %v", err)
	}
	if got := counting.Total(); got != 1 {
		t.Errorf("a fresh read cost %d trips, want 1 - it was served from the snapshot", got)
	}
}

// TestRunCacheNeverDecidesAConditionalWrite: the compare-and-swap that guards
// against a writer outside this process has to be performed by the store on
// the store's own current version. A cache that answered it from a remembered
// version would let a concurrent run's change be overwritten silently, which
// is the one failure this whole layer exists to prevent.
func TestRunCacheNeverDecidesAConditionalWrite(t *testing.T) {
	ctx := context.Background()
	resetRunCacheState(t)
	dir := t.TempDir()
	inner, err := NewLocalStore(dir)
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	cached := NewRunCache(inner, testPrefix)

	version, err := cached.PutIfAbsent(ctx, recKey(0), []byte("ours"))
	if err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}
	if _, _, _, err := cached.Get(ctx, recKey(0)); err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Somebody else - another process, as far as this cache is concerned -
	// changes the key behind its back, through a store it does not wrap.
	outsider, err := NewLocalStore(dir)
	if err != nil {
		t.Fatalf("NewLocalStore (outsider): %v", err)
	}
	if _, err := outsider.PutIfVersion(ctx, recKey(0), []byte("theirs"), version); err != nil {
		t.Fatalf("outsider PutIfVersion: %v", err)
	}

	_, err = cached.PutIfVersion(ctx, recKey(0), []byte("clobber"), version)
	var conflict *VersionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("PutIfVersion with a version the store no longer holds returned %v, want a *VersionConflictError", err)
	}
	payload, _, _, err := outsider.Get(ctx, recKey(0))
	if err != nil {
		t.Fatalf("outsider Get: %v", err)
	}
	if string(payload) != "theirs" {
		t.Errorf("the outside writer's value is now %q, want %q - the cache clobbered it", payload, "theirs")
	}
}

// TestRunCacheFallsBackWithoutABulkReader: a backend that cannot enumerate
// values still works, and still pays only one trip per key rather than one
// per accessor.
func TestRunCacheFallsBackWithoutABulkReader(t *testing.T) {
	ctx := context.Background()
	resetRunCacheState(t)
	inner, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	if _, err := inner.PutIfAbsent(ctx, recKey(0), []byte("v")); err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}
	counting := NewCountingStore(inner, nil)
	cached := NewRunCache(bulkless{counting}, testPrefix)

	for i := 0; i < 4; i++ {
		payload, _, exists, err := cached.Get(ctx, recKey(0))
		if err != nil || !exists || string(payload) != "v" {
			t.Fatalf("Get %d: payload=%q exists=%v err=%v", i, payload, exists, err)
		}
	}
	// The seed write went straight to the local store, beneath the counter,
	// so one read is the whole expected cost.
	if got := counting.Total(); got != 1 {
		t.Errorf("four reads through a bulkless store cost %d trips, want 1", got)
	}
}

// bulkless hides a store's bulk read, standing in for a backend that has
// none.
type bulkless struct{ Store }

// TestRunCacheDoesNotHandOutItsOwnBuffer: a caller that modified a returned
// slice would corrupt every later read of that key.
func TestRunCacheDoesNotHandOutItsOwnBuffer(t *testing.T) {
	ctx := context.Background()
	cached, _ := countedCache(t, 2)

	first, _, _, err := cached.Get(ctx, recKey(0))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	first[0] = 'X'

	second, _, _, err := cached.Get(ctx, recKey(0))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(second) != "payload-0" {
		t.Errorf("after a caller wrote to the first result, the second read returned %q, want %q", second, "payload-0")
	}

	keys, err := cached.List(ctx, testPrefix)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	keys[0] = "mutated"
	again, err := cached.List(ctx, testPrefix)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if again[0] == "mutated" {
		t.Errorf("List returned its own cached slice: second call gave %v", again)
	}
}

// TestRunCacheIsConcurrencySafe runs under -race. #585's concurrent read pass
// reaches the record store from one goroutine per instance, so the bulk load
// itself is raced by 78 callers at once.
func TestRunCacheIsConcurrencySafe(t *testing.T) {
	ctx := context.Background()
	const n = 12
	cached, _ := countedCache(t, n)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				payload, _, exists, err := cached.Get(ctx, recKey(i))
				if err != nil || !exists {
					t.Errorf("Get %s: exists=%v err=%v", recKey(i), exists, err)
					return
				}
				if want := fmt.Sprintf("payload-%d", i); string(payload) != want {
					t.Errorf("Get %s returned %q, want %q", recKey(i), payload, want)
					return
				}
				if _, err := cached.List(ctx, testPrefix); err != nil {
					t.Errorf("List: %v", err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
}

// TestLocalStoreGetAllMatchesPerKeyReads pins the bulk read against the
// per-key one it replaces: same keys, same payloads, same versions. A bulk
// read that disagreed with Get would make the snapshot a second, quieter
// source of truth.
func TestLocalStoreGetAllMatchesPerKeyReads(t *testing.T) {
	ctx := context.Background()
	inner, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := inner.PutIfAbsent(ctx, recKey(i), []byte(fmt.Sprintf("payload-%d", i))); err != nil {
			t.Fatalf("PutIfAbsent: %v", err)
		}
	}
	if _, err := inner.PutIfAbsent(ctx, "other-namespace/k", []byte("elsewhere")); err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}

	all, err := inner.GetAll(ctx, testPrefix)
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("GetAll returned %d records, want 5 (it must not reach outside its prefix)", len(all))
	}
	keys, err := inner.List(ctx, testPrefix)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != len(all) {
		t.Errorf("GetAll returned %d records but List returned %d keys", len(all), len(keys))
	}
	for _, key := range keys {
		payload, version, exists, err := inner.Get(ctx, key)
		if err != nil || !exists {
			t.Fatalf("Get %s: exists=%v err=%v", key, exists, err)
		}
		got, ok := all[key]
		if !ok {
			t.Errorf("GetAll omitted %s, which Get returns", key)
			continue
		}
		if string(got.Payload) != string(payload) {
			t.Errorf("%s: GetAll payload %q, Get payload %q", key, got.Payload, payload)
		}
		if got.Version != version {
			t.Errorf("%s: GetAll version %q, Get version %q", key, got.Version, version)
		}
	}
}
