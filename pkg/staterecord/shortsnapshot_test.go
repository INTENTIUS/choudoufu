// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// GitHub issue #1355: one `apply -destroy` of a two-instance record-backed
// estate destroyed one instance and printed a success. No log survived, so
// what this file answers is the question the issue could not: which reads of
// this store can come back SHORT with no error, since a short read of the
// record namespace is prior state with an instance missing from it, and a
// destroy plan built from that proposes nothing for the instance it lost.
//
// Three of them are pinned here. The first two are the fixes; the third is
// the reason they have to be fixes rather than warnings.

// s3StoreOverFake is a store over a fresh fake server, for the tests below
// that need to reach into the fake rather than only drive the store.
func s3StoreOverFake(t *testing.T) (*S3Store, *fakeS3Server) {
	t.Helper()
	server, fake := newFakeS3Server(t)
	client := s3.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
	}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(server.URL)
		o.UsePathStyle = true
	})
	store, err := NewS3Store(S3Config{Client: client, Bucket: "test-bucket"})
	if err != nil {
		t.Fatalf("NewS3Store: %v", err)
	}
	return store, fake
}

// TestS3ListRefusesATruncatedPageWithNoContinuationToken is the listing half.
// The loop used to leave on either "not truncated" OR "no token", so a page
// that said it was truncated and handed back nothing to ask for the rest with
// ended the listing and returned what it had, with no error at all. Every
// consumer downstream reads a key's absence from that listing as a record's
// absence.
//
// Real S3 always sends the token beside the flag. An S3-compatible store
// need not, and this store is the recommended way to run against one.
func TestS3ListRefusesATruncatedPageWithNoContinuationToken(t *testing.T) {
	ctx := context.Background()
	store, fake := s3StoreOverFake(t)
	for _, key := range []string{"tofu-records/prod/a", "tofu-records/prod/b", "tofu-records/prod/c", "tofu-records/prod/d"} {
		if _, err := store.PutIfAbsent(ctx, key, []byte(key)); err != nil {
			t.Fatalf("seeding %q: %v", key, err)
		}
	}

	// The control: paginated honestly, the listing is whole.
	fake.pageSize = 2
	whole, err := store.List(ctx, "tofu-records/prod/")
	if err != nil {
		t.Fatalf("the honestly paginated listing failed: %v", err)
	}
	if len(whole) != 4 {
		t.Fatalf("the honestly paginated listing returned %d keys, want 4: this test's premise is that pagination works when the token is there", len(whole))
	}

	fake.truncateWithoutToken = true
	got, err := store.List(ctx, "tofu-records/prod/")
	if err == nil {
		t.Fatalf("List returned %d of 4 keys and no error: a listing short by an unknown number of keys is indistinguishable from a whole one, and every key missing from it reads as a record that is not there", len(got))
	}
	if got != nil {
		t.Errorf("List returned an error AND %d keys; a caller that ignores the error has a short namespace", len(got))
	}
	if !strings.Contains(err.Error(), "test-bucket") {
		t.Errorf("the refusal does not name the bucket that answered it: %v", err)
	}
}

// shortBulkStore is a [BulkReader] that drops one key from an otherwise
// correct namespace read and reports no error - the shape both fixes above
// exist to make unreachable, injected directly so the CONSEQUENCE can be
// measured without depending on how it is produced.
type shortBulkStore struct {
	Store
	inner *LocalStore
	drop  string
}

func (s *shortBulkStore) GetAll(ctx context.Context, keyPrefix string) (map[string]Record, error) {
	all, err := s.inner.GetAll(ctx, keyPrefix)
	if err != nil {
		return nil, err
	}
	delete(all, s.drop)
	return all, nil
}

// TestRunCacheTreatsAShortSnapshotAsTheEstate is the third read, and it is
// not a fix: it is the measurement that says why the other two had to be
// refusals rather than warnings.
//
// [RunCache] holds one bulk read for the whole read phase and answers every
// later question about a key inside the namespace from it, without going back
// to the store - that is the whole point of it. So a snapshot short by one
// key does not cost one stale answer. It makes "there is no record for this
// instance" the run's settled position, from a store that holds the record,
// with no error and nothing logged. internal/live/projection turns that
// answer into an omitted instance, and a destroy plan over prior state with
// an instance missing from it proposes nothing for that instance and prints
// a success.
//
// If the bulk read is ever allowed to come back short again, this test still
// passes and the estate is still wrong. That is what makes completeness the
// store's own obligation.
func TestRunCacheTreatsAShortSnapshotAsTheEstate(t *testing.T) {
	resetRunCacheState(t)
	ctx := context.Background()
	inner, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	kept := testPrefix + "terraform_data/kept"
	dropped := testPrefix + "terraform_data/dropped"
	for _, key := range []string{kept, dropped} {
		if _, err := inner.PutIfAbsent(ctx, key, []byte(`{"key":"`+key+`"}`)); err != nil {
			t.Fatalf("seeding %q: %v", key, err)
		}
	}

	// The store itself holds both, read per key.
	if _, _, exists, err := inner.Get(ctx, dropped); err != nil || !exists {
		t.Fatalf("the backend does not hold %q (exists=%v, err=%v): this test's premise is that the record IS there", dropped, exists, err)
	}

	cache := NewRunCache(&shortBulkStore{Store: inner, inner: inner, drop: dropped}, testPrefix)
	payload, version, exists, err := cache.Get(ctx, dropped)
	if err != nil {
		t.Fatalf("Get through the cache errored (%v); the point of this test is the answer it gives with NO error", err)
	}
	if exists {
		t.Fatalf("the cache answered that %q exists; then a short snapshot costs nothing and this test is measuring the wrong thing", dropped)
	}
	if payload != nil || version != "" {
		t.Errorf("Get answered absent but returned payload %q version %q", payload, version)
	}

	// And the listing through the cache agrees with the snapshot, not with
	// the store: there is no second opinion to be had once a snapshot loaded.
	keys, err := cache.List(ctx, testPrefix)
	if err != nil {
		t.Fatalf("List through the cache: %v", err)
	}
	for _, key := range keys {
		if key == dropped {
			t.Fatalf("List named %q, so the cache has a second source for this key and the short snapshot is recoverable from inside it", dropped)
		}
	}
	if len(keys) != 1 {
		t.Errorf("List through the cache returned %d keys, want 1 (the snapshot's own)", len(keys))
	}
}

// TestLocalGetAllRefusesAFileTheWalkSawAndTheReadDidNot is the local store's
// half of the same contract - see [LocalStore.GetAll]. Removing the file
// between the walk and the read used to leave the key out of the map with no
// error.
func TestLocalGetAllRefusesAFileTheWalkSawAndTheReadDidNot(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := NewLocalStore(dir)
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	keep := testPrefix + "terraform_data/keep"
	vanish := testPrefix + "terraform_data/vanish"
	for _, key := range []string{keep, vanish} {
		if _, err := store.PutIfAbsent(ctx, key, []byte(`{"key":"`+key+`"}`)); err != nil {
			t.Fatalf("seeding %q: %v", key, err)
		}
	}

	// The control: nothing removed, the read is whole.
	all, err := store.GetAll(ctx, testPrefix)
	if err != nil {
		t.Fatalf("the undisturbed bulk read failed: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("the undisturbed bulk read returned %d records, want 2", len(all))
	}

	// The window is a directory entry the walk lists and a read cannot open:
	// deleting the file outright makes the walk never see it, which is a
	// namespace of one and a different case entirely. A dangling symlink is
	// that entry with no second goroutine involved - WalkDir lstats it and
	// reports a file, os.ReadFile follows it and gets ENOENT, which is
	// exactly the "removed between the walk and the read" branch.
	path := filepath.Join(dir, filepath.FromSlash(vanish))
	if err := os.Remove(path); err != nil {
		t.Fatalf("removing %q: %v", vanish, err)
	}
	if err := os.Symlink(filepath.Join(dir, "no-such-file"), path); err != nil {
		t.Skipf("this platform will not make a dangling symlink to stage the window: %v", err)
	}
	got, gotErr := store.GetAll(ctx, testPrefix)
	if gotErr == nil {
		t.Fatalf("GetAll returned %d records and no error with one of the two unreadable as a record: a map short by a key the walk named reads as an estate with one fewer instance", len(got))
	}
	if got != nil {
		t.Errorf("GetAll returned an error AND a map of %d records", len(got))
	}
}
