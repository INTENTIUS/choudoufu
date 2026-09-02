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
	"time"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// This file is issue #109's carrier: guided discovery's hint riding the
// estate's record store. The round trip is proven through the real writer
// (Manager.EnableHint + PersistState) and the real reader (ReadHintStore),
// never hand-rolled JSON, the same discipline the snapshot-era hint tests
// held to.

// localHintStore is a real record store in a temp directory.
func localHintStore(t *testing.T) staterecord.Store {
	t.Helper()
	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	return store
}

// writeHintThroughManager is the production write path end to end: enable,
// write state, persist.
func writeHintThroughManager(t *testing.T, store staterecord.Store, estate string, when time.Time) *Manager {
	t.Helper()
	m := NewManager()
	m.EnableHint(store, estate, func() time.Time { return when })
	if err := m.WriteState(testHintState()); err != nil {
		t.Fatalf("WriteState: %s", err)
	}
	if err := m.PersistState(context.Background(), nil); err != nil {
		t.Fatalf("PersistState: %s", err)
	}
	return m
}

// TestHintStore_roundTrip: what PersistState writes, ReadHintStore reduces
// back to exactly the types and timestamp the state carried.
func TestHintStore_roundTrip(t *testing.T) {
	store := localHintStore(t)
	when := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)

	m := writeHintThroughManager(t, store, "my-estate", when)
	if w := m.HintWarning(); len(w) != 0 {
		t.Fatalf("HintWarning after a clean write: %s", w.Err())
	}

	hint, err := ReadHintStore(context.Background(), store, "my-estate")
	if err != nil {
		t.Fatalf("ReadHintStore: %s", err)
	}
	if hint.Estate != "my-estate" {
		t.Errorf("Estate = %q, want %q", hint.Estate, "my-estate")
	}
	if !hint.WrittenAt.Equal(when) {
		t.Errorf("WrittenAt = %s, want %s", hint.WrittenAt, when)
	}
	wantTypes := map[string]bool{"aws_s3_bucket": true, "aws_sns_topic": true}
	if len(hint.Types) != len(wantTypes) {
		t.Errorf("Types = %v, want %v", hint.Types, wantTypes)
	}
	for typ := range wantTypes {
		if !hint.Types[typ] {
			t.Errorf("Types is missing %q: %v", typ, hint.Types)
		}
	}
}

// TestHintStore_overwrite: a second persist replaces the hint rather than
// accumulating history - the live system is the history now (issue #109).
func TestHintStore_overwrite(t *testing.T) {
	store := localHintStore(t)
	first := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	second := first.Add(2 * time.Hour)

	writeHintThroughManager(t, store, "my-estate", first)
	writeHintThroughManager(t, store, "my-estate", second)

	hint, err := ReadHintStore(context.Background(), store, "my-estate")
	if err != nil {
		t.Fatalf("ReadHintStore: %s", err)
	}
	if !hint.WrittenAt.Equal(second) {
		t.Errorf("WrittenAt = %s, want the second write's %s", hint.WrittenAt, second)
	}
}

// TestHintStore_missing: a store nothing has persisted a hint to yet is a
// plain error, never a panic and never a Hint with zeroed-out fields.
func TestHintStore_missing(t *testing.T) {
	hint, err := ReadHintStore(context.Background(), localHintStore(t), "my-estate")
	if err == nil {
		t.Fatalf("want an error for a store with no hint, got %#v", hint)
	}
	if hint != nil {
		t.Errorf("want a nil Hint alongside the error, got %#v", hint)
	}
}

// TestHintStore_nilStoreAndEmptyEstate: the two caller-misuse shapes are
// errors before the store is ever touched.
func TestHintStore_nilStoreAndEmptyEstate(t *testing.T) {
	if _, err := ReadHintStore(context.Background(), nil, "my-estate"); err == nil {
		t.Error("want an error for a nil store")
	}
	if _, err := ReadHintStore(context.Background(), localHintStore(t), ""); err == nil {
		t.Error("want an error for an unsettled estate name")
	}
}

// TestHintStore_corrupted covers the ways a stored hint can exist and still
// be unusable: not JSON at all, JSON of the wrong shape, and a
// formatVersion this build does not recognize. Every case is an error,
// never a partial Hint.
func TestHintStore_corrupted(t *testing.T) {
	cases := []struct {
		name string
		data string
	}{
		{"not JSON", "not json at all {{{"},
		{"wrong shape", `{"hello": "world"}`},
		{"unrecognized format", `{"formatVersion": "some-other-format-v9", "estate": "x", "types": []}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := localHintStore(t)
			if _, err := store.PutIfAbsent(context.Background(), HintKey("my-estate"), []byte(c.data)); err != nil {
				t.Fatalf("planting the corrupted payload: %s", err)
			}
			hint, err := ReadHintStore(context.Background(), store, "my-estate")
			if err == nil {
				t.Fatalf("want an error for %s, got a Hint: %#v", c.name, hint)
			}
			if hint != nil {
				t.Errorf("want a nil Hint alongside the error, got %#v", hint)
			}
		})
	}
}

// TestHintStore_keyDisjointFromRecords is issue #109's trap test: orphan
// discovery (builder.discoverOrphanedRecords) lists the record namespace
// and treats every key it can decode as a resource record whose block was
// removed, so the hint's key must be invisible to that listing by
// construction. Proven both lexically (the namespace roots differ) and
// functionally, against a real store holding both a resource record and
// the hint.
func TestHintStore_keyDisjointFromRecords(t *testing.T) {
	const estate = "my-estate"

	// Lexical: the hint key never lives under the record namespace, nor
	// the record namespace under the hint's.
	recordPrefix := RecordKeyPrefix(estate)
	if strings.HasPrefix(HintKey(estate), recordPrefix+"/") {
		t.Fatalf("HintKey(%q) = %q lives under RecordKeyPrefix %q; orphan discovery would see it", estate, HintKey(estate), recordPrefix)
	}
	if strings.HasPrefix(recordPrefix, hintNamespaceRoot+"/") {
		t.Fatalf("RecordKeyPrefix(%q) = %q lives under the hint namespace %q", estate, recordPrefix, hintNamespaceRoot)
	}

	// Functional: a store holding one real record and the hint. The exact
	// listing discoverOrphanedRecords performs must return the record and
	// never the hint key.
	store := localHintStore(t)
	addr := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "terraform_data", Name: "seed"}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
	recordKey := RecordKey(recordPrefix, addr)
	if _, err := store.PutIfAbsent(context.Background(), recordKey, []byte(`{"value_type":"\"string\"","attrs":"\"x\""}`)); err != nil {
		t.Fatalf("writing the record fixture: %s", err)
	}
	writeHintThroughManager(t, store, estate, time.Now())

	keys, err := store.List(context.Background(), recordPrefix)
	if err != nil {
		t.Fatalf("List(%q): %s", recordPrefix, err)
	}
	for _, key := range keys {
		if key == HintKey(estate) {
			t.Errorf("List(%q) returned the hint key %q; orphan discovery would treat it as a resource record", recordPrefix, key)
		}
	}
	if len(keys) != 1 || keys[0] != recordKey {
		t.Errorf("List(%q) = %v, want exactly the one record key %q", recordPrefix, keys, recordKey)
	}

	// And the hint key can never be mistaken for a record even if a listing
	// somehow surfaced it: RecordAddr must refuse it under the record
	// prefix.
	if got, ok := RecordAddr(recordPrefix, HintKey(estate)); ok {
		t.Errorf("RecordAddr decoded the hint key into %s; it must refuse it", got)
	}
}

// failingHintStore is a staterecord.Store whose writes always fail, for the
// warning contract below.
type failingHintStore struct {
	staterecord.Store
	putErr error
}

func (s failingHintStore) Get(context.Context, string) ([]byte, string, bool, error) {
	return nil, "", false, nil
}

func (s failingHintStore) PutIfVersion(context.Context, string, []byte, string) (string, error) {
	return "", s.putErr
}

// TestHintStore_writeFailureWarnsAndNeverFails: a hint write failure is
// retrievable through HintWarning and PersistState still returns nil - a
// cache that could fail an apply would not be a cache.
func TestHintStore_writeFailureWarnsAndNeverFails(t *testing.T) {
	m := NewManager()
	m.EnableHint(failingHintStore{putErr: errors.New("the backend is on fire")}, "my-estate", nil)
	if err := m.WriteState(testHintState()); err != nil {
		t.Fatalf("WriteState: %s", err)
	}
	if err := m.PersistState(context.Background(), nil); err != nil {
		t.Fatalf("PersistState returned an error for a failed hint write: %s", err)
	}
	warning := m.HintWarning()
	if len(warning) == 0 {
		t.Fatal("no HintWarning after a failed write")
	}
	if got := warning[0].Description().Summary; got != "Could not write the discovery hint" {
		t.Errorf("warning summary = %q", got)
	}
}

// TestHintStore_conflictIsNotAWarning: losing the write race to a
// concurrent run leaves that run's equally fresh hint in place, and is not
// worth a warning.
func TestHintStore_conflictIsNotAWarning(t *testing.T) {
	m := NewManager()
	m.EnableHint(failingHintStore{putErr: &staterecord.VersionConflictError{Key: HintKey("my-estate")}}, "my-estate", nil)
	if err := m.WriteState(testHintState()); err != nil {
		t.Fatalf("WriteState: %s", err)
	}
	if err := m.PersistState(context.Background(), nil); err != nil {
		t.Fatalf("PersistState: %s", err)
	}
	if warning := m.HintWarning(); len(warning) != 0 {
		t.Errorf("a version conflict produced a warning: %s", warning.Err())
	}
}
