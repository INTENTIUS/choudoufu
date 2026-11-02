// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// noRetryS3Store is a store over a fresh fake with the SDK's retries off, so
// an injected failure arrives at the store instead of being absorbed.
func noRetryS3Store(t *testing.T) (*S3Store, *fakeS3Server) {
	t.Helper()
	server, fake := newFakeS3Server(t)
	client := s3.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
		Retryer:     func() aws.Retryer { return retry.AddWithMaxAttempts(retry.NewStandard(), 1) },
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

// TestS3StoreMissingBucketIsNeverAbsenceOrConflict is GitHub issue #1383's
// medium item. A bucket that is gone answers 404, the same status a missing
// key gets, and this store used to read the status alone: Get said the record
// was absent, an update and a delete said another writer had changed it. An
// absence is what a plan reads as "create this live resource", and a conflict
// is what internal/live/projection's unwritten-record ledger skips on purpose
// (#1287), so both readings lost the fact that the store was unreachable.
//
// Every operation must fail, name the bucket, and not be a version conflict.
func TestS3StoreMissingBucketIsNeverAbsenceOrConflict(t *testing.T) {
	ctx := context.Background()
	ops := map[string]func(*S3Store) error{
		"get":            func(s *S3Store) error { _, _, _, err := s.Get(ctx, "a/b"); return err },
		"create":         func(s *S3Store) error { _, err := s.PutIfAbsent(ctx, "a/b", []byte("x")); return err },
		"update":         func(s *S3Store) error { _, err := s.PutIfVersion(ctx, "a/b", []byte("x"), `"v1"`); return err },
		"delete":         func(s *S3Store) error { return s.Delete(ctx, "a/b", `"v1"`) },
		"delete-absent":  func(s *S3Store) error { return s.Delete(ctx, "a/b", "") },
		"list":           func(s *S3Store) error { _, err := s.List(ctx, "a/"); return err },
		"bulk":           func(s *S3Store) error { _, err := s.GetAll(ctx, "a/"); return err },
		"get-for-bulk":   func(s *S3Store) error { _, _, err := s.getForBulk(ctx, "a/b"); return err },
		"get-no-prefix":  func(s *S3Store) error { _, _, _, err := s.Get(ctx, "b"); return err },
		"create-no-vers": func(s *S3Store) error { _, err := s.PutIfVersion(ctx, "a/b", []byte("x"), ""); return err },
	}
	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			store, fake := noRetryS3Store(t)
			fake.missingBucket = true

			err := op(store)
			if err == nil {
				t.Fatal("a missing bucket was not an error at all, so it read as an ordinary absence")
			}
			var conflict *VersionConflictError
			if errors.As(err, &conflict) {
				t.Fatalf("a missing bucket was reported as a version conflict, which the #1287 ledger skips: %v", err)
			}
			if !strings.Contains(err.Error(), `"test-bucket"`) {
				t.Errorf("the error does not name the bucket: %v", err)
			}
			if !strings.Contains(err.Error(), "NoSuchBucket") {
				t.Errorf("the error does not say what S3 answered: %v", err)
			}
		})
	}
}

// TestS3StoreGetOfAMissingKeyIsStillAbsence is the other half: reading the
// code must not have turned an ordinary missing record into a failure.
func TestS3StoreGetOfAMissingKeyIsStillAbsence(t *testing.T) {
	store, _ := noRetryS3Store(t)
	payload, version, exists, err := store.Get(context.Background(), "a/b")
	if err != nil {
		t.Fatalf("reading a key that is simply not there failed: %v", err)
	}
	if exists || version != "" || payload != nil {
		t.Errorf("a missing key read back as payload %q version %q exists %v", payload, version, exists)
	}
}

// TestS3StoreCreateMeetingA404StaysAnError pins the create half of
// PutIfVersion's 404 branch. An update that meets 404 NoSuchKey is the same
// conflict from the other side, because the version the caller read is gone.
// A create asserts that NO version exists, so a 404 agrees with it about the
// key and cannot be a conflict; it is a store that answered something the
// create cannot make sense of, and it stays an error.
//
// The mutation this catches: dropping `&& expectedVersion != ""` from that
// branch, which sends a create into conflictError and reports "another writer
// changed it" for a key nobody wrote.
func TestS3StoreCreateMeetingA404StaysAnError(t *testing.T) {
	ctx := context.Background()

	t.Run("create", func(t *testing.T) {
		store, fake := noRetryS3Store(t)
		fake.putNotFoundAsKey = true
		_, err := store.PutIfAbsent(ctx, "a/b", []byte("x"))
		if err == nil {
			t.Fatal("a create answered 404 succeeded")
		}
		var conflict *VersionConflictError
		if errors.As(err, &conflict) {
			t.Fatalf("a create answered 404 was reported as a version conflict against version %q: %v", conflict.ActualVersion, err)
		}
	})

	// The same 404, on an update, IS the conflict (#1344), so the branch is
	// shown to still do its job rather than having been switched off.
	t.Run("update", func(t *testing.T) {
		store, fake := noRetryS3Store(t)
		fake.putNotFoundAsKey = true
		_, err := store.PutIfVersion(ctx, "a/b", []byte("x"), `"etag-1"`)
		var conflict *VersionConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("an update whose key is gone was not a version conflict: %v", err)
		}
		if conflict.ActualVersion != "" {
			t.Errorf("ActualVersion = %q, want \"\": the store holds no record", conflict.ActualVersion)
		}
	})
}

// TestS3StoreDeleteOfAGoneKeyIsAConflict pins Delete's 404 branch. A
// conditional delete whose object is already gone gets 404 NoSuchKey from
// real S3 (assumed for DELETE from #1344's measured PUT; see the fake), and
// the caller's version is not the store's, which is a conflict.
//
// The mutation this catches: deleting the 404 branch from Delete, which turns
// this into a raw wire error and loses the *VersionConflictError callers
// branch on. It survived before because the fake answered 412 here, a shape
// real S3 does not send.
func TestS3StoreDeleteOfAGoneKeyIsAConflict(t *testing.T) {
	ctx := context.Background()
	store, _ := noRetryS3Store(t)

	version, err := store.PutIfAbsent(ctx, "a/b", []byte("x"))
	if err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}
	if err := store.Delete(ctx, "a/b", version); err != nil {
		t.Fatalf("the first Delete: %v", err)
	}

	err = store.Delete(ctx, "a/b", version)
	var conflict *VersionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("deleting an already-gone key with a version is not a *VersionConflictError: %v (%T)", err, err)
	}
	if conflict.ExpectedVersion != version || conflict.ActualVersion != "" {
		t.Errorf("conflict names expected %q actual %q, want expected %q and no record", conflict.ExpectedVersion, conflict.ActualVersion, version)
	}
}

// TestConflictErrorKeepsBothFailures: the re-read that names the current
// version is a courtesy, and when it fails the conditional refusal that
// brought us here is still the fact worth reporting. Returning only the
// re-read's error dropped the 412 entirely.
func TestConflictErrorKeepsBothFailures(t *testing.T) {
	ctx := context.Background()
	store, fake := noRetryS3Store(t)

	if _, err := store.PutIfAbsent(ctx, "a/b", []byte("x")); err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}
	fake.beforeGet = func(string) int { return 500 }

	_, err := store.PutIfVersion(ctx, "a/b", []byte("y"), `"etag-stale"`)
	if err == nil {
		t.Fatal("a stale update succeeded")
	}
	for _, want := range []string{"412", "500"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error lost the %s: %v", want, err)
		}
	}
}
