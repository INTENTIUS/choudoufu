// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// GitHub issue #1287 asks one question of this layer: can a bulk read that
// PARTIALLY fails leave [RunCache] answering "no record" for a key it never
// actually saw?
//
// The answer has two halves, and both are pinned here rather than argued.
//
//  1. No [BulkReader] in this package returns a partial map. All three
//     return (nil, err) the moment any part of the namespace cannot be read,
//     so "the snapshot is incomplete" never reaches the cache as data. The
//     local backend is exercised against a real unreadable file below; S3's
//     and SSM's GetAll return on the first error from ListObjectsV2 /
//     GetObject / GetParametersByPath respectively (bulk.go), so neither can
//     produce one either.
//
//  2. A bulk read that fails entirely does NOT become a false absence.
//     [RunCache.ensureLoaded] leaves c.loaded false, so every Get falls
//     through to the wrapped store's own per-key path and reports that
//     store's own error - which projection's three readers turn into a hard
//     "Cannot read a persisted record" refusal.
//
// Together those are why #1287's "the snapshot cannot tell not-present from
// not-asked" is NOT reachable through the bulk read. The reachable route is
// a WRITE that failed; see the poison ledger in internal/live/projection.

// TestLocalGetAllRefusesRatherThanReturningAPartialNamespace is half 1 for
// the one backend that can be driven with no fake: a record file the process
// cannot read must fail the whole bulk read, not silently drop that one key
// out of a map whose contract says a key absent from it holds no record.
func TestLocalGetAllRefusesRatherThanReturningAPartialNamespace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file mode 0 does not deny reads on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode 0 denies nothing")
	}
	ctx := context.Background()
	dir := t.TempDir()
	store, err := NewLocalStore(dir)
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	for _, key := range []string{testPrefix + "/a", testPrefix + "/b"} {
		if _, err := store.PutIfAbsent(ctx, key, []byte(`{"k":"`+key+`"}`)); err != nil {
			t.Fatalf("PutIfAbsent(%q): %v", key, err)
		}
	}

	unreadable := filepath.Join(dir, filepath.FromSlash(testPrefix+"/b"))
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o600) })

	got, err := store.GetAll(ctx, testPrefix)
	if err == nil {
		t.Fatalf("GetAll returned %d records and no error with one of the two unreadable; a partial map is "+
			"indistinguishable from a complete one, and every key missing from it reads as \"no record\"", len(got))
	}
	if got != nil {
		t.Errorf("GetAll returned a non-nil map alongside its error (%d entries); a caller that ignores the "+
			"error would treat it as the namespace", len(got))
	}
}

// TestRunCacheReportsTheStoresErrorWhenTheBulkReadFailed is half 2. The
// wrapped store cannot answer either question, and the cache must pass that
// through rather than invent an absence from a snapshot it never loaded.
func TestRunCacheReportsTheStoresErrorWhenTheBulkReadFailed(t *testing.T) {
	resetRunCacheState(t)
	ctx := context.Background()
	inner := &bulkFailingStore{err: errors.New("simulated S3 outage")}
	cache := NewRunCache(inner, testPrefix)

	_, _, exists, err := cache.Get(ctx, testPrefix+"/aws_instance/whatever")
	if err == nil {
		t.Fatalf("Get reported exists=%v with no error after a failed bulk read; \"the store could not be asked\" "+
			"must never arrive at a reader as \"there is no record\"", exists)
	}
	if exists {
		t.Errorf("Get reported exists=true alongside its error")
	}
	if inner.perKeyGets == 0 {
		t.Errorf("the failed bulk read did not fall back to the per-key path at all, so the error a reader sees " +
			"is not the store's own answer to the key it asked about")
	}
}

// TestRunCacheTrustsACompleteSnapshotsAbsence is the other side of the same
// coin, and the reason half 1 above is load-bearing: given a bulk read that
// SUCCEEDS, a key it did not return is absent, answered with no trip. That
// is [BulkReader]'s documented contract, so the only thing standing between
// it and a false absence is that no implementation ever returns a partial
// map.
func TestRunCacheTrustsACompleteSnapshotsAbsence(t *testing.T) {
	resetRunCacheState(t)
	ctx := context.Background()
	inner := &bulkFailingStore{snapshot: map[string]Record{
		testPrefix + "/aws_instance/present": {Payload: []byte(`{}`), Version: "1"},
	}}
	cache := NewRunCache(inner, testPrefix)

	if _, _, exists, err := cache.Get(ctx, testPrefix+"/aws_instance/absent"); err != nil || exists {
		t.Fatalf("Get = (exists %v, err %v), want (false, nil) from a complete snapshot", exists, err)
	}
	if inner.perKeyGets != 0 {
		t.Errorf("a key absent from a complete snapshot cost %d per-key trips, want 0", inner.perKeyGets)
	}
}

// bulkFailingStore answers GetAll with snapshot when err is nil and with err
// otherwise, and counts the per-key Gets the fallback makes. Its per-key Get
// raises the same err, which is what a real outage does: the bulk call and
// the single call fail for the same reason.
type bulkFailingStore struct {
	Store
	err        error
	snapshot   map[string]Record
	perKeyGets int
}

func (s *bulkFailingStore) GetAll(context.Context, string) (map[string]Record, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make(map[string]Record, len(s.snapshot))
	for k, v := range s.snapshot {
		out[k] = v
	}
	return out, nil
}

func (s *bulkFailingStore) Get(_ context.Context, key string) ([]byte, string, bool, error) {
	s.perKeyGets++
	if s.err != nil {
		return nil, "", false, s.err
	}
	rec, ok := s.snapshot[key]
	if !ok {
		return nil, "", false, nil
	}
	return rec.Payload, rec.Version, true, nil
}
