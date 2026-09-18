// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// GitHub issue #1335, at the one place a neighbour estate's key does harm.
//
// Measured on #1335: an estate prefix with no trailing delimiter makes this
// estate's listing return the keys of any estate whose name starts the same
// way. Three of the four record-store List consumers are safe from that
// anyway, because [projection.RecordAddr] refuses a key outside the prefix
// and they re-read under their own prefix before acting. This leg is the
// fourth. It refuses the neighbour's key as evidence too - but it has
// already taken the len(keys) == 0 branch's answer from the raw listing, and
// that branch is the rebuild-from-markers rule: an estate with no record of
// itself sweeps in full.
//
// So an estate with NO records, sharing a store with a neighbour that has
// one, looks like it has a record of itself. The pass narrows, a type this
// estate owns an orphaned object of is never listed, and its removal is
// never proposed. Same consequence as #1301, reached by a neighbour instead
// of a phantom.
func TestNeighbourEstatesRecordDoesNotNarrowTheNativeSweep(t *testing.T) {
	staterecord.ResetRunCacheForTest(t)
	srv := (&taggingServer{}).start(t)
	defer srv.Close()

	ctx := context.Background()
	backend, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}

	// The neighbour: an estate whose name this one's prefixes, holding one
	// record, in the same store.
	neighbour := estateName + "-eu"
	neighbourKey := projection.RecordKey(projection.RecordKeyPrefix(neighbour), mustAddr(t, `aws_vpc.neighbours`))
	if _, err := backend.PutIfAbsent(ctx, neighbourKey, []byte(`{}`)); err != nil {
		t.Fatalf("seeding the neighbour's record: %s", err)
	}

	// The premise, asserted rather than assumed: this estate holds nothing.
	own := projection.RecordKeyPrefix(estateName)
	if keys, err := backend.List(ctx, own); err != nil {
		t.Fatalf("backend List: %s", err)
	} else if len(keys) != 0 {
		t.Fatalf("List(%q) = %v, want nothing: this estate has no records, and a listing that says otherwise is the defect", own, keys)
	}

	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable(nativeLegType)
	req := nativeSweepRequest(t, srv.URL)
	req.HintStore = staterecord.NewRunCache(backend, own)

	res, diags := discoverFixture(t, cloud, req)
	assertNoErrors(t, diags)

	if _, listed := cloud.requestFor(nativeLegType); !listed {
		t.Errorf("%s was not listed even though estate %q has NO records. Estate %q's key %q came back in this estate's "+
			"listing, the pass took a non-empty listing to mean the estate has a record of itself, and it narrowed the sweep. "+
			"An estate with no record of itself must sweep in full (issue #1335).", nativeLegType, estateName, neighbour, neighbourKey)
	}
	if res.NativeSweepSkipped != 0 {
		t.Errorf("the pass reported NativeSweepSkipped = %d for an estate with no records, want 0", res.NativeSweepSkipped)
	}
}
