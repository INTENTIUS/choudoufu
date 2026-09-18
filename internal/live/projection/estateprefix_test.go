// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/states"
)

// GitHub issue #1335. Two estates whose names prefix one another ("prod",
// "prod-eu") share one store, which the bucket backend (#1332) makes the
// recommended arrangement rather than an accident. [staterecord.Store.List]
// and S3's ListObjectsV2 both match an ordinary string prefix, so an estate
// prefix with no trailing delimiter makes "prod" list "prod-eu"'s keys.
//
// seedNeighbourEstates writes one kind=object record under each estate and
// returns the raw backend plus both addresses.
func seedNeighbourEstates(t *testing.T) (raw staterecord.Store, prodAddr, euAddr addrs.AbsResourceInstance) {
	t.Helper()
	ctx := context.Background()
	raw = localHintStore(t)

	prodAddr = locatedTestAddr(t, "null_resource", "in_prod")
	euAddr = locatedTestAddr(t, "null_resource", "in_prod_eu")

	for estate, addr := range map[string]addrs.AbsResourceInstance{"prod": prodAddr, "prod-eu": euAddr} {
		of, err := encodeObjectFields(cty.ObjectVal(map[string]cty.Value{
			"id":       cty.StringVal(estate + "-object-id"),
			"triggers": cty.NullVal(cty.Map(cty.String)),
		}), nil, states.ObjectReady)
		if err != nil {
			t.Fatalf("encoding %s's fixture: %s", estate, err)
		}
		store := NewRecordEnvelopeStore(raw, RecordKeyPrefix(estate))
		if _, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
			env.Kind = recordKindObject
			env.Object = of
		}); err != nil {
			t.Fatalf("writing %s's record: %s", estate, err)
		}
	}
	return raw, prodAddr, euAddr
}

// TestAnEstateListsNoneOfANeighbourEstatesKeys pins the listing itself: every
// key "prod" is handed back is one of its own.
func TestAnEstateListsNoneOfANeighbourEstatesKeys(t *testing.T) {
	staterecord.ResetRunCacheForTest(t)
	ctx := context.Background()
	raw, _, _ := seedNeighbourEstates(t)

	euPrefix := RecordKeyPrefix("prod-eu")
	keys, err := NewRecordEnvelopeStore(raw, RecordKeyPrefix("prod")).List(ctx)
	if err != nil {
		t.Fatalf("List: %s", err)
	}
	if len(keys) != 1 {
		t.Errorf("prod listed %d keys, want exactly its own 1: %q", len(keys), keys)
	}
	for _, key := range keys {
		if strings.HasPrefix(key, euPrefix) {
			t.Errorf("prod's listing returned prod-eu's key %q", key)
		}
	}
}

// TestAnEstateBulkLoadsNoneOfANeighbourEstatesRecords is the same hazard one
// layer down: [staterecord.RunCache] bulk-loads the estate's prefix, so a
// prefix that matches a neighbour reads the neighbour's payloads into this
// run - secret material included - and under the bucket backend's tag
// condition that GET is denied, which fails the whole bulk read.
func TestAnEstateBulkLoadsNoneOfANeighbourEstatesRecords(t *testing.T) {
	staterecord.ResetRunCacheForTest(t)
	ctx := context.Background()
	raw, _, _ := seedNeighbourEstates(t)

	bulk, ok := raw.(staterecord.BulkReader)
	if !ok {
		t.Fatalf("%T is not a BulkReader", raw)
	}
	all, err := bulk.GetAll(ctx, RecordKeyPrefix("prod"))
	if err != nil {
		t.Fatalf("GetAll: %s", err)
	}
	euPrefix := RecordKeyPrefix("prod-eu")
	for key := range all {
		if strings.HasPrefix(key, euPrefix) {
			t.Errorf("prod's bulk read returned prod-eu's record %q", key)
		}
	}
	if len(all) != 1 {
		t.Errorf("prod bulk-read %d records, want exactly its own 1", len(all))
	}
}

// TestAnEstateProposesDestroyingNoneOfANeighbourEstatesRecords is where the
// harm lands. "prod" has no configuration, so every record it believes is
// its own is undeclared and materializes for destruction. Its own record
// must; "prod-eu"'s must not.
func TestAnEstateProposesDestroyingNoneOfANeighbourEstatesRecords(t *testing.T) {
	staterecord.ResetRunCacheForTest(t)
	ctx := context.Background()
	raw, prodAddr, euAddr := seedNeighbourEstates(t)

	cfg := loadConfig(t, writeEmptyFixture(t))
	provs := SingleProvider(nullProvider, nullResourceProvider())
	store := NewRecordEnvelopeStore(raw, RecordKeyPrefix("prod"))

	res, diags := BuildWith(ctx, cfg, nil, provs, Options{RecordStore: store})
	assertNoErrors(t, diags)

	// The control: orphan discovery still does its job for prod's own record.
	assertMaterialized(t, res, []string{prodAddr.String()})
	if res.Has(euAddr) {
		t.Errorf("prod materialized %s, a record under prod-eu's prefix, so prod's plan proposes destroying another estate's resource", euAddr)
	}
}
