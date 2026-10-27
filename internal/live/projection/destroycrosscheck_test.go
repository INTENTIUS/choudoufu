// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/pkg/staterecord"
)

// contradictingBulkStore is GitHub issue #1355's store as a run meets it
// through the run cache. Its listing names hide; its bulk read fails, as
// pkg/staterecord's does since #1355 when a listed key is not found; and the
// per-key read the run cache falls back to answers that hide holds no
// record. Everything else is a real local store.
type contradictingBulkStore struct {
	absentForKeyStore
}

func (s *contradictingBulkStore) GetAll(context.Context, string) (map[string]staterecord.Record, error) {
	return nil, fmt.Errorf("the listing named %q and two reads of it found nothing", s.hide)
}

// TestADestroyOverAStoreThatListsARecordItReadsAsAbsentFails pins the one
// call to refuseListedButReadAsAbsent in discoverOrphanedRecords (GitHub
// issues #1355 and #2288). `apply -destroy` plans from the prior state this
// build returns: an instance left out of it as absent is not a destroy that
// failed, it is a destroy that is never proposed, and the run reports one of
// two destroyed under a line saying it succeeded. So a build over a store
// that lists a declared instance's record and reads it as absent must fail,
// naming the key, on the path a real run takes: through the run cache, whose
// bulk read fails and whose per-key read then answers absent.
//
// Until #2288 this call was held by the BREAK_CROSSCHECK arm of the smoke
// scenario a-bulk-read-is-complete-or-it-fails, which rebuilt choudoufu
// without it and caught the destroy reporting 1 destroyed with the record
// still in the bucket. Deleting the call fails this test and
// TestARecordTheListNamesAndTheReadDoesNotIsRefused.
func TestADestroyOverAStoreThatListsARecordItReadsAsAbsentFails(t *testing.T) {
	ctx := context.Background()
	cfg := loadConfig(t, writeTwoNullResourceFixture(t))
	kept := mustAddr(t, `null_resource.trigger["kept"]`)
	missed := mustAddr(t, `null_resource.trigger["missed"]`)

	const prefix = "tofu-records/test-estate"
	backing, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}
	seedRecordObject(t, backing, prefix, kept)
	seedRecordObject(t, backing, prefix, missed)
	hidden := RecordKey(prefix, missed)

	resolutions := []identity.Resolution{
		{Addr: kept, Class: identity.ClassRecordBacked},
		{Addr: missed, Class: identity.ClassRecordBacked},
	}
	provs := SingleProvider(nullProvider, nullResourceProvider())
	build := func(inner staterecord.Store) (*Result, string, bool) {
		store := NewRecordEnvelopeStore(staterecord.NewRunCache(inner, prefix), prefix)
		res, diags := BuildWith(ctx, cfg, resolutions, provs, Options{RecordStore: store})
		return res, renderDiags(diags), diags.HasErrors()
	}

	// The control: the same estate over a store that answers consistently
	// is clean and holds both instances, so a destroy planned from it
	// proposes both. Without it, a build that failed on everything would
	// pass the arm below.
	res, rendered, failed := build(backing)
	if failed {
		t.Fatalf("the build over a consistent store failed:\n%s", rendered)
	}
	assertMaterialized(t, res, []string{kept.String(), missed.String()})

	res, rendered, failed = build(&contradictingBulkStore{absentForKeyStore{Store: backing, hide: hidden}})
	if !failed {
		var got []string
		for _, a := range res.Materialized {
			got = append(got, a.String())
		}
		t.Fatalf("the build is clean although the store lists %s and reads it as absent; prior state holds %v, so `apply -destroy` proposes destroying %d of 2 and reports success with %s still recorded:\n%s",
			hidden, got, len(got), missed, rendered)
	}
	for _, want := range []string{"The record store contradicts itself about a record", hidden, missed.String()} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, rendered)
		}
	}
}
