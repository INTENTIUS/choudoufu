// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"errors"
	"testing"

	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// GitHub issue #1301 reaches a user through this leg, and only through this
// leg. [estateScopedNativeSweep] is the one of the four record-store List
// consumers that never re-reads a key: it decodes the TYPE out of each key
// string and uses that as evidence, so a listed key that holds no record is
// evidence of a resource that does not exist, with nothing downstream to
// catch it.
//
// The consequence is not the extra list call [estateScopedNativeSweep]'s own
// comment budgets for. It is the len(keys) == 0 branch above it: an estate
// with NO records sweeps in full, because its markers are the only thing
// that knows what it owns (the charter's rebuild-from-markers rule). One
// phantom key makes that estate look like it has a record of itself, so the
// pass narrows to the evidence the phantom implies - and a type the estate
// really does own an orphaned object of is never listed, so its removal is
// never proposed. A transient throttle during one plan, and a destroy goes
// missing with nothing in the output saying so.
//
// This is the same fixture as
// TestNativeSweepNarrowsToEstateEvidence/"empty record store sweeps in full",
// with the empty store reached through the production cache after one
// transient bulk-read failure.
func TestPhantomRecordKeyDoesNotNarrowTheNativeSweep(t *testing.T) {
	staterecord.ResetRunCacheForTest(t)
	srv := (&taggingServer{}).start(t)
	defer srv.Close()

	ctx := context.Background()
	prefix := projection.RecordKeyPrefix(estateName)

	// An estate with no records at all - the rebuild-from-markers case.
	backend, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	flaky := &flakyBulkStore{Store: backend, inner: backend, failFirst: 1}
	cached := staterecord.NewRunCache(flaky, prefix)

	// Drive #1301's sequence before the pass starts: a read of an absent
	// in-namespace key while the bulk read is still failing, remembered as
	// "no record", then a read that lets ensureLoaded's retry land the
	// snapshot. A plan asks the record store about every declared address,
	// so an absent one being read first is the ordinary case, not a
	// contrivance.
	ghost := projection.RecordKey(prefix, mustAddr(t, `aws_vpc.never_existed`))
	if _, _, exists, err := cached.Get(ctx, ghost); err != nil || exists {
		t.Fatalf("Get(%q) = (exists %v, err %v), want (false, nil)", ghost, exists, err)
	}
	if _, _, exists, err := cached.Get(ctx, projection.RecordKey(prefix, mustAddr(t, `aws_vpc.other`))); err != nil || exists {
		t.Fatalf("the second read = (exists %v, err %v), want (false, nil)", exists, err)
	}
	if flaky.served == 0 {
		t.Fatalf("the bulk read never succeeded, so the snapshot never loaded and this test proves nothing")
	}

	// The premise, asserted rather than assumed: the store really is empty.
	if keys, err := backend.List(ctx, prefix); err != nil {
		t.Fatalf("backend List: %s", err)
	} else if len(keys) != 0 {
		t.Fatalf("the backend holds %v; this test needs an estate with no records", keys)
	}

	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable(nativeLegType)
	req := nativeSweepRequest(t, srv.URL)
	req.HintStore = cached

	res, diags := discoverFixture(t, cloud, req)
	assertNoErrors(t, diags)

	if _, listed := cloud.requestFor(nativeLegType); !listed {
		t.Errorf("%s was not listed even though this estate's record store is EMPTY. One transient bulk-read failure "+
			"left a key in the cache that holds no record, the listing named it, and estateScopedNativeSweep - which "+
			"never re-reads a key - took it for evidence and narrowed the sweep. An estate with no record of itself "+
			"must sweep in full (issue #1301).", nativeLegType)
	}
	if res.NativeSweepSkipped != 0 {
		t.Errorf("the pass reported NativeSweepSkipped = %d over an empty record store, want 0", res.NativeSweepSkipped)
	}
}

// flakyBulkStore fails its first failFirst GetAll calls and then delegates to
// the real local store beneath it. Nothing else is faked: the per-key Get,
// the List and every write go straight through.
type flakyBulkStore struct {
	staterecord.Store
	inner     *staterecord.LocalStore
	failFirst int
	calls     int
	served    int
}

func (s *flakyBulkStore) GetAll(ctx context.Context, keyPrefix string) (map[string]staterecord.Record, error) {
	s.calls++
	if s.calls <= s.failFirst {
		return nil, errors.New("simulated transient bulk-read failure")
	}
	s.served++
	return s.inner.GetAll(ctx, keyPrefix)
}
