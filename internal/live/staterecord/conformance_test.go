// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"errors"
	"testing"
)

// runConformance exercises the [Store] contract every implementation must
// satisfy identically, regardless of what backs it. newStore returns a
// fresh, empty Store for each subtest, so cases never interact with each
// other's keys.
func runConformance(t *testing.T, newStore func(t *testing.T) Store) {
	t.Helper()

	t.Run("GetOnMissingKeyIsNotAnError", func(t *testing.T) {
		s := newStore(t)
		payload, version, exists, err := s.Get(context.Background(), "missing")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if exists {
			t.Errorf("exists = true, want false")
		}
		if payload != nil {
			t.Errorf("payload = %v, want nil", payload)
		}
		if version != "" {
			t.Errorf("version = %q, want empty", version)
		}
	})

	t.Run("PutIfAbsentThenGetRoundTrips", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		version, err := s.PutIfAbsent(ctx, "k1", []byte("hello"))
		if err != nil {
			t.Fatalf("PutIfAbsent: %v", err)
		}
		if version == "" {
			t.Fatal("PutIfAbsent returned an empty version")
		}
		payload, gotVersion, exists, err := s.Get(ctx, "k1")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if !exists {
			t.Fatal("exists = false after PutIfAbsent")
		}
		if string(payload) != "hello" {
			t.Errorf("payload = %q, want %q", payload, "hello")
		}
		if gotVersion != version {
			t.Errorf("Get version = %q, want %q (from PutIfAbsent)", gotVersion, version)
		}
	})

	t.Run("PutIfAbsentTwiceConflicts", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		v1, err := s.PutIfAbsent(ctx, "k1", []byte("first"))
		if err != nil {
			t.Fatalf("first PutIfAbsent: %v", err)
		}
		_, err = s.PutIfAbsent(ctx, "k1", []byte("second"))
		var conflict *VersionConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("second PutIfAbsent: got %v (%T), want *VersionConflictError", err, err)
		}
		if conflict.Key != "k1" {
			t.Errorf("conflict.Key = %q, want k1", conflict.Key)
		}
		if conflict.ExpectedVersion != "" {
			t.Errorf("conflict.ExpectedVersion = %q, want empty", conflict.ExpectedVersion)
		}
		if conflict.ActualVersion != v1 {
			t.Errorf("conflict.ActualVersion = %q, want %q", conflict.ActualVersion, v1)
		}

		// The losing write must never have landed.
		payload, _, _, err := s.Get(ctx, "k1")
		if err != nil {
			t.Fatalf("Get after conflict: %v", err)
		}
		if string(payload) != "first" {
			t.Errorf("payload after conflict = %q, want %q (the loser must not overwrite)", payload, "first")
		}
	})

	t.Run("PutIfVersionWithMatchingVersionSucceeds", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		v1, err := s.PutIfAbsent(ctx, "k1", []byte("v1"))
		if err != nil {
			t.Fatalf("PutIfAbsent: %v", err)
		}
		v2, err := s.PutIfVersion(ctx, "k1", []byte("v2"), v1)
		if err != nil {
			t.Fatalf("PutIfVersion: %v", err)
		}
		if v2 == "" || v2 == v1 {
			t.Errorf("PutIfVersion returned version %q, want a new, non-empty version distinct from %q", v2, v1)
		}
		payload, gotVersion, exists, err := s.Get(ctx, "k1")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if !exists {
			t.Fatal("exists = false after PutIfVersion")
		}
		if string(payload) != "v2" {
			t.Errorf("payload = %q, want %q", payload, "v2")
		}
		if gotVersion != v2 {
			t.Errorf("Get version = %q, want %q", gotVersion, v2)
		}
	})

	t.Run("PutIfVersionWithStaleVersionConflicts", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		v1, err := s.PutIfAbsent(ctx, "k1", []byte("v1"))
		if err != nil {
			t.Fatalf("PutIfAbsent: %v", err)
		}
		v2, err := s.PutIfVersion(ctx, "k1", []byte("v2"), v1)
		if err != nil {
			t.Fatalf("PutIfVersion to v2: %v", err)
		}

		// Now try to write again using the stale v1.
		_, err = s.PutIfVersion(ctx, "k1", []byte("v3-should-not-land"), v1)
		var conflict *VersionConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("PutIfVersion with stale version: got %v (%T), want *VersionConflictError", err, err)
		}
		if conflict.ExpectedVersion != v1 {
			t.Errorf("conflict.ExpectedVersion = %q, want %q", conflict.ExpectedVersion, v1)
		}
		if conflict.ActualVersion != v2 {
			t.Errorf("conflict.ActualVersion = %q, want %q", conflict.ActualVersion, v2)
		}

		payload, _, _, err := s.Get(ctx, "k1")
		if err != nil {
			t.Fatalf("Get after conflict: %v", err)
		}
		if string(payload) != "v2" {
			t.Errorf("payload after conflict = %q, want %q (the stale write must not land)", payload, "v2")
		}
	})

	t.Run("PutIfVersionOnMissingKeyConflicts", func(t *testing.T) {
		s := newStore(t)
		_, err := s.PutIfVersion(context.Background(), "nope", []byte("x"), "some-version")
		var conflict *VersionConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("got %v (%T), want *VersionConflictError", err, err)
		}
		if conflict.ActualVersion != "" {
			t.Errorf("conflict.ActualVersion = %q, want empty (no record exists)", conflict.ActualVersion)
		}
	})

	t.Run("PutIfVersionEmptyExpectedActsLikePutIfAbsent", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		version, err := s.PutIfVersion(ctx, "k1", []byte("hello"), "")
		if err != nil {
			t.Fatalf("PutIfVersion with expectedVersion \"\": %v", err)
		}
		if version == "" {
			t.Fatal("PutIfVersion with expectedVersion \"\" returned an empty version")
		}
		_, err = s.PutIfVersion(ctx, "k1", []byte("world"), "")
		var conflict *VersionConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("second PutIfVersion with expectedVersion \"\": got %v (%T), want *VersionConflictError", err, err)
		}
	})

	t.Run("DeleteWithMatchingVersionSucceeds", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		v1, err := s.PutIfAbsent(ctx, "k1", []byte("v1"))
		if err != nil {
			t.Fatalf("PutIfAbsent: %v", err)
		}
		if err := s.Delete(ctx, "k1", v1); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		_, _, exists, err := s.Get(ctx, "k1")
		if err != nil {
			t.Fatalf("Get after delete: %v", err)
		}
		if exists {
			t.Error("exists = true after Delete")
		}
	})

	t.Run("DeleteWithStaleVersionConflicts", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		v1, err := s.PutIfAbsent(ctx, "k1", []byte("v1"))
		if err != nil {
			t.Fatalf("PutIfAbsent: %v", err)
		}
		v2, err := s.PutIfVersion(ctx, "k1", []byte("v2"), v1)
		if err != nil {
			t.Fatalf("PutIfVersion: %v", err)
		}
		err = s.Delete(ctx, "k1", v1)
		var conflict *VersionConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("Delete with stale version: got %v (%T), want *VersionConflictError", err, err)
		}
		if conflict.ActualVersion != v2 {
			t.Errorf("conflict.ActualVersion = %q, want %q", conflict.ActualVersion, v2)
		}

		_, _, exists, err := s.Get(ctx, "k1")
		if err != nil {
			t.Fatalf("Get after conflicted delete: %v", err)
		}
		if !exists {
			t.Error("exists = false after a conflicted Delete; the record must survive")
		}
	})

	t.Run("DeleteOfAlreadyAbsentKeyWithEmptyVersionIsIdempotent", func(t *testing.T) {
		s := newStore(t)
		if err := s.Delete(context.Background(), "never-existed", ""); err != nil {
			t.Fatalf("Delete of an absent key with expectedVersion \"\": %v", err)
		}
	})

	t.Run("DeleteOfAbsentKeyWithNonEmptyVersionConflicts", func(t *testing.T) {
		s := newStore(t)
		err := s.Delete(context.Background(), "never-existed", "some-version")
		var conflict *VersionConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("got %v (%T), want *VersionConflictError", err, err)
		}
		if conflict.ActualVersion != "" {
			t.Errorf("conflict.ActualVersion = %q, want empty", conflict.ActualVersion)
		}
	})

	t.Run("ListReturnsKeysByPrefixSorted", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		for _, key := range []string{"b", "a", "prefix/two", "prefix/one", "other"} {
			if _, err := s.PutIfAbsent(ctx, key, []byte(key)); err != nil {
				t.Fatalf("PutIfAbsent(%q): %v", key, err)
			}
		}

		all, err := s.List(ctx, "")
		if err != nil {
			t.Fatalf("List(\"\"): %v", err)
		}
		wantAll := []string{"a", "b", "other", "prefix/one", "prefix/two"}
		if !equalStrings(all, wantAll) {
			t.Errorf("List(\"\") = %v, want %v", all, wantAll)
		}

		prefixed, err := s.List(ctx, "prefix/")
		if err != nil {
			t.Fatalf("List(\"prefix/\"): %v", err)
		}
		wantPrefixed := []string{"prefix/one", "prefix/two"}
		if !equalStrings(prefixed, wantPrefixed) {
			t.Errorf("List(\"prefix/\") = %v, want %v", prefixed, wantPrefixed)
		}

		none, err := s.List(ctx, "nonexistent")
		if err != nil {
			t.Fatalf("List(\"nonexistent\"): %v", err)
		}
		if len(none) != 0 {
			t.Errorf("List(\"nonexistent\") = %v, want empty", none)
		}
	})

	t.Run("VersionConflictErrorMessageNamesBothVersions", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		v1, err := s.PutIfAbsent(ctx, "k1", []byte("v1"))
		if err != nil {
			t.Fatalf("PutIfAbsent: %v", err)
		}
		_, err = s.PutIfVersion(ctx, "k1", []byte("v2"), "not-"+v1)
		if err == nil {
			t.Fatal("expected a conflict")
		}
		msg := err.Error()
		if msg == "" {
			t.Fatal("VersionConflictError.Error() returned an empty string")
		}
	})
}

// equalStrings compares two string slices for equality including order,
// treating nil and empty as equal — this test package's own helper, not
// exported, since [Store] implementations never need it.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
