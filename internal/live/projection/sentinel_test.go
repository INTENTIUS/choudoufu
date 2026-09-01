// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// brokenListStore wraps a real store and makes List return nothing, which
// is exactly the failure #688's terralith run hit: writes succeed, reads
// succeed, and enumeration silently reports an empty estate.
type brokenListStore struct {
	staterecord.Store
}

func (b *brokenListStore) List(context.Context, string) ([]string, error) {
	return nil, nil
}

func newTestLocalStore(t *testing.T) staterecord.Store {
	t.Helper()
	s, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	return s
}

// TestSentinelProvisionsAndVerifies is #693's happy path: a working store
// gains the sentinel, List returns it, and a second provision is a no-op
// because PutIfAbsent's conflict is the success case.
func TestSentinelProvisionsAndVerifies(t *testing.T) {
	ctx := context.Background()
	store := newTestLocalStore(t)

	if err := provisionStoreSentinel(ctx, store, "records/est-a"); err != nil {
		t.Fatalf("first provision: %v", err)
	}
	if err := provisionStoreSentinel(ctx, store, "records/est-a"); err != nil {
		t.Fatalf("second provision (must treat the existing sentinel as success): %v", err)
	}
	keys, err := store.List(ctx, "records/est-a/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := SentinelKey("records/est-a")
	found := false
	for _, k := range keys {
		if k == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("List = %v, want it to contain %q", keys, want)
	}
}

// TestSentinelRefusesAStoreWhoseListIsBroken is the red case this whole
// mechanism exists for: a store that accepts writes but enumerates nothing
// must be refused loudly at construction, never carried into a plan where
// its emptiness reads as an empty estate.
func TestSentinelRefusesAStoreWhoseListIsBroken(t *testing.T) {
	ctx := context.Background()
	store := &brokenListStore{Store: newTestLocalStore(t)}

	err := provisionStoreSentinel(ctx, store, "records/est-a")
	if err == nil {
		t.Fatal("provision succeeded against a store whose List returns nothing; a plan against it would propose re-creating the whole estate")
	}
	if !strings.Contains(err.Error(), "#693") || !strings.Contains(err.Error(), "List") {
		t.Fatalf("err = %v, want the loud #693 refusal naming List", err)
	}
}

// TestSentinelIsInvisibleToRecordAddr pins the foreign-key contract the
// sentinel leans on: no List consumer that decodes record keys may mistake
// it for a resource record.
func TestSentinelIsInvisibleToRecordAddr(t *testing.T) {
	if _, ok := RecordAddr("records/est-a", SentinelKey("records/est-a")); ok {
		t.Fatal("RecordAddr decoded the sentinel as a resource record; the sentinel's last segment must never parse as an address")
	}
}
