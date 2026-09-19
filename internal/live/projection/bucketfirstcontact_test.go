// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// bucketBackedStore is a real local store that also answers the bucket
// contract, so the handshake and the sentinel are the production ones and
// only the bucket's settings are scripted.
type bucketBackedStore struct {
	staterecord.Store
	findings   []staterecord.BucketFinding
	checks     int
	namespaces []string
}

func (s *bucketBackedStore) CheckBucketContract(_ context.Context, namespaces []string) ([]staterecord.BucketFinding, error) {
	s.checks++
	s.namespaces = namespaces
	return s.findings, nil
}

func passing() []staterecord.BucketFinding {
	var out []staterecord.BucketFinding
	for _, setting := range staterecord.BucketSettings {
		out = append(out, staterecord.BucketFinding{Setting: setting, OK: true, Found: "fine"})
	}
	return out
}

// firstContact runs the production handshake followed by the first-contact
// assertion, the way newRecordStore does.
func firstContact(t *testing.T, store *bucketBackedStore, rs *configs.LiveRecordStore, estate string) error {
	t.Helper()
	// The production sequence, not a copy of it: see openBuiltStore.
	_, err := openBuiltStore(context.Background(), store, rs, estate)
	return err
}

// TestABadBucketIsRefusedOnEveryFirstContactUntilItIsFixed is GitHub issue
// #1339's first-contact half. The part that matters is the second run: a
// refusal that left its sentinel behind would make the next plan proceed
// against the bucket the first one refused.
func TestABadBucketIsRefusedOnEveryFirstContactUntilItIsFixed(t *testing.T) {
	ctx := context.Background()
	const estate = "prod"
	rs := &configs.LiveRecordStore{Type: "s3", Bucket: "the-bucket"}
	bad := passing()
	bad[0] = staterecord.BucketFinding{Setting: staterecord.BucketVersioning, Found: "versioning has never been enabled"}
	store := &bucketBackedStore{Store: localHintStore(t), findings: bad}

	for run := 1; run <= 2; run++ {
		err := firstContact(t, store, rs, estate)
		if err == nil {
			t.Fatalf("run %d: a bucket with versioning off was accepted", run)
		}
		if !strings.Contains(err.Error(), "versioning") || !strings.Contains(err.Error(), "the-bucket") {
			t.Errorf("run %d: the refusal names neither the setting nor the bucket: %s", run, err)
		}
		keys, listErr := store.List(ctx, staterecord.NamespacePrefix(RecordKeyPrefix(estate)))
		if listErr != nil {
			t.Fatal(listErr)
		}
		if slices.Contains(keys, SentinelKey(RecordKeyPrefix(estate))) {
			t.Fatalf("run %d: the refusal left the sentinel behind, so the next run is no longer a first contact and would not be checked", run)
		}
	}
	if store.checks != 2 {
		t.Errorf("the bucket was checked %d times across two refused runs, want 2", store.checks)
	}
	if want := BucketNamespaces(rs, estate); !slices.Equal(store.namespaces, want) {
		t.Errorf("the check was told the store writes under %q, want %q", store.namespaces, want)
	}

	// Fixed. The third run is accepted and leaves its sentinel.
	store.findings = passing()
	if err := firstContact(t, store, rs, estate); err != nil {
		t.Fatalf("a correct bucket was refused: %s", err)
	}
	// And a fourth run is not a first contact: the ruling on #1339 is that
	// the assertions do not run on every plan.
	if err := firstContact(t, store, rs, estate); err != nil {
		t.Fatal(err)
	}
	if store.checks != 3 {
		t.Errorf("the bucket was checked %d times, want 3: two refusals, one acceptance, and nothing once the sentinel exists", store.checks)
	}
}

// TestAStoreWithNoBucketHasNothingToAssert: the local store is not in a
// bucket, and that is not a failure.
func TestAStoreWithNoBucketHasNothingToAssert(t *testing.T) {
	findings, ok, err := BucketContractFindings(context.Background(), staterecord.NewRunCache(localHintStore(t), RecordKeyPrefix("prod")), nil, "prod")
	if err != nil || ok || findings != nil {
		t.Errorf("BucketContractFindings over a local store = (%v, %v, %v), want (nil, false, nil)", findings, ok, err)
	}
}

// TestTheBucketIsFoundThroughTheProductionWrappers: newRecordStore hands the
// runner a RunCache over a (possibly) CountingStore, and BeforeApply asserts
// through that. A wrapper that hid the bucket would make every apply skip the
// check and say nothing.
func TestTheBucketIsFoundThroughTheProductionWrappers(t *testing.T) {
	inner := &bucketBackedStore{Store: localHintStore(t), findings: passing()}
	wrapped := staterecord.NewRunCache(staterecord.NewCountingStore(inner, nil), RecordKeyPrefix("prod"))
	_, ok, err := BucketContractFindings(context.Background(), wrapped, &configs.LiveRecordStore{Type: "s3", Bucket: "b"}, "prod")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || inner.checks != 1 {
		t.Errorf("the bucket under the wrappers was not reached: ok=%v checks=%d", ok, inner.checks)
	}
}
