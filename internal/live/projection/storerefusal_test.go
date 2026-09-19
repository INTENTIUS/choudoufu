// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// unreachableStore fails every call the way a store that cannot be reached
// does: an ordinary error, nothing typed.
type unreachableStore struct{ staterecord.Store }

var errUnreachable = errors.New("dial tcp 127.0.0.1:9: connect: connection refused")

func (unreachableStore) PutIfAbsent(context.Context, string, []byte) (string, error) {
	return "", errUnreachable
}
func (unreachableStore) List(context.Context, string) ([]string, error) { return nil, errUnreachable }

// kmsRefusingStore refuses the sentinel write the way S3 relays a KMS denial.
type kmsRefusingStore struct{ staterecord.Store }

func (kmsRefusingStore) PutIfAbsent(context.Context, string, []byte) (string, error) {
	return "", fmt.Errorf("staterecord: s3: writing %q: %w", "k", &staterecord.KMSDeniedError{Action: "kms:GenerateDataKey", Err: errors.New("AccessDenied")})
}

// kmsUnusableStore fails the sentinel write the way S3 relays a KMS key that
// is disabled or gone: a different type from the denial above, with no
// policy remedy anywhere in it.
type kmsUnusableStore struct{ staterecord.Store }

func (kmsUnusableStore) PutIfAbsent(context.Context, string, []byte) (string, error) {
	return "", fmt.Errorf("staterecord: s3: writing %q: %w", "k", &staterecord.KMSKeyUnusableError{Code: "KMS.DisabledException", Err: errors.New("KMS.DisabledException")})
}

// TestStoreOpenFailuresAreRefusalsOrOutages is GitHub issue #1376's
// distinction, held at the one place it is made. `live-plan` and `live-mv`
// go on without a store after an OUTAGE and must stop on a REFUSAL, so a
// refusal that loses its type on the way out of openBuiltStore becomes a
// warning on those commands, which is the bug the issue is about.
func TestStoreOpenFailuresAreRefusalsOrOutages(t *testing.T) {
	ctx := context.Background()
	rs := &configs.LiveRecordStore{Type: "s3", Bucket: "the-bucket"}

	failing := passing()
	failing[0].OK = false
	failing[0].Found = "versioning is Suspended"

	for _, tc := range []struct {
		name    string
		store   staterecord.Store
		refusal bool
	}{
		{"a bucket that fails its contract on first contact", &bucketBackedStore{Store: newTestLocalStore(t), findings: failing}, true},
		{"a store whose List does not return what was written", &brokenListStore{Store: newTestLocalStore(t)}, true},
		{"a KMS key that refused the run", kmsRefusingStore{Store: newTestLocalStore(t)}, true},
		// #1383. A disabled or deleted key was reached and answered, and no
		// retry and no other command gets past it, so it is a refusal by the
		// same rule the denial is.
		{"a KMS key that cannot be used", kmsUnusableStore{Store: newTestLocalStore(t)}, true},
		{"a store that cannot be reached", unreachableStore{Store: newTestLocalStore(t)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := openBuiltStore(ctx, tc.store, rs, "prod")
			if err == nil {
				t.Fatal("the store opened")
			}
			if got := IsStoreRefusal(err); got != tc.refusal {
				t.Errorf("IsStoreRefusal = %v, want %v, for: %v", got, tc.refusal, err)
			}
		})
	}

	t.Run("a store that opens is neither", func(t *testing.T) {
		if _, err := openBuiltStore(ctx, &bucketBackedStore{Store: newTestLocalStore(t), findings: passing()}, rs, "prod"); err != nil {
			t.Fatalf("a correct bucket did not open: %v", err)
		}
	})
}

// TestOpenBuiltStoreAssertsTheBucketOnFirstContact pins the glue itself.
// Removing the first-contact call from the open sequence used to leave every
// test green, because the tests called the two halves themselves.
func TestOpenBuiltStoreAssertsTheBucketOnFirstContact(t *testing.T) {
	rs := &configs.LiveRecordStore{Type: "s3", Bucket: "the-bucket"}
	store := &bucketBackedStore{Store: newTestLocalStore(t), findings: passing()}
	if _, err := openBuiltStore(context.Background(), store, rs, "prod"); err != nil {
		t.Fatalf("open: %v", err)
	}
	if store.checks != 1 {
		t.Errorf("the bucket contract was checked %d time(s) on first contact, want 1", store.checks)
	}
	if _, err := openBuiltStore(context.Background(), store, rs, "prod"); err != nil {
		t.Fatalf("second open: %v", err)
	}
	if store.checks != 1 {
		t.Errorf("the bucket contract was checked again on a second contact (%d checks); it is asserted on first contact and before an apply, never on every open", store.checks)
	}
}
