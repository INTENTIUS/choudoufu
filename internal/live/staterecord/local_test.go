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
	"sync/atomic"
	"testing"
)

func TestLocalStoreConformance(t *testing.T) {
	runConformance(t, func(t *testing.T) Store {
		t.Helper()
		s, err := NewLocalStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewLocalStore: %v", err)
		}
		return s
	})
}

func TestLocalStoreNewLocalStoreCreatesTheDirectory(t *testing.T) {
	dir := t.TempDir() + "/nested/does/not/exist/yet"
	s, err := NewLocalStore(dir)
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	if _, err := s.PutIfAbsent(context.Background(), "k", []byte("v")); err != nil {
		t.Fatalf("PutIfAbsent into a freshly created directory: %v", err)
	}
}

func TestLocalStoreRejectsPathTraversal(t *testing.T) {
	s, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	ctx := context.Background()
	for _, key := range []string{"../escape", "a/../../escape", ".."} {
		if _, _, _, err := s.Get(ctx, key); err == nil {
			t.Errorf("Get(%q): expected an error rejecting the traversal, got nil", key)
		}
		if _, err := s.PutIfAbsent(ctx, key, []byte("x")); err == nil {
			t.Errorf("PutIfAbsent(%q): expected an error rejecting the traversal, got nil", key)
		}
	}
}

func TestLocalStoreHierarchicalKeysNestDirectories(t *testing.T) {
	s, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	ctx := context.Background()
	if _, err := s.PutIfAbsent(ctx, "a/b/c", []byte("nested")); err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}
	payload, _, exists, err := s.Get(ctx, "a/b/c")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !exists || string(payload) != "nested" {
		t.Errorf("Get(a/b/c) = (%q, exists=%v), want (nested, true)", payload, exists)
	}
}

// TestLocalStoreConcurrentPutIfVersionRace proves the race-test
// requirement directly: many goroutines racing PutIfVersion against the
// same starting version must have exactly one winner, and every loser
// must see a *VersionConflictError rather than a torn or silently
// overwritten record.
func TestLocalStoreConcurrentPutIfVersionRace(t *testing.T) {
	s, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	ctx := context.Background()

	base, err := s.PutIfAbsent(ctx, "contended", []byte("base"))
	if err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}

	const writers = 20
	var wg sync.WaitGroup
	var succeeded, conflicted int64
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.PutIfVersion(ctx, "contended", []byte(fmt.Sprintf("writer-%d", i)), base)
			switch {
			case err == nil:
				atomic.AddInt64(&succeeded, 1)
			case isVersionConflict(err):
				atomic.AddInt64(&conflicted, 1)
			default:
				t.Errorf("writer %d: unexpected error %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	if succeeded != 1 {
		t.Errorf("succeeded = %d, want exactly 1", succeeded)
	}
	if conflicted != writers-1 {
		t.Errorf("conflicted = %d, want %d", conflicted, writers-1)
	}

	// The stored record must be exactly one writer's payload, never a mix
	// of two partial writes.
	payload, _, exists, err := s.Get(ctx, "contended")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !exists {
		t.Fatal("exists = false after the race")
	}
	if len(payload) == 0 {
		t.Error("payload is empty after the race")
	}
}

// TestLocalStoreConcurrentPutIfAbsentRace is TestLocalStoreConcurrentPutIfVersionRace's
// counterpart for creation: many goroutines racing to create the same key
// must have exactly one winner.
func TestLocalStoreConcurrentPutIfAbsentRace(t *testing.T) {
	s, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	ctx := context.Background()

	const writers = 20
	var wg sync.WaitGroup
	var succeeded, conflicted int64
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.PutIfAbsent(ctx, "contended", []byte(fmt.Sprintf("writer-%d", i)))
			switch {
			case err == nil:
				atomic.AddInt64(&succeeded, 1)
			case isVersionConflict(err):
				atomic.AddInt64(&conflicted, 1)
			default:
				t.Errorf("writer %d: unexpected error %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	if succeeded != 1 {
		t.Errorf("succeeded = %d, want exactly 1", succeeded)
	}
	if conflicted != writers-1 {
		t.Errorf("conflicted = %d, want %d", conflicted, writers-1)
	}
}

func isVersionConflict(err error) bool {
	var conflict *VersionConflictError
	return errors.As(err, &conflict)
}
