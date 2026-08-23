// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/states"
)

// encodeRecordPayload and decodeRecordPayload are the pre-GitHub-issue-#364
// whole-payload codec for a kind=object record, kept for tests written
// before the envelope existed. They are not the wire format any more (see
// [recordEnvelope]) but the round trip they perform - a materialized value,
// its provider-private bytes and its [states.ObjectStatus], through JSON and
// back - is identical, so every test that used them to build or read a
// record-backed fixture keeps proving the same thing.
func encodeRecordPayload(val cty.Value, private []byte, status states.ObjectStatus) ([]byte, error) {
	of, err := encodeObjectFields(val, private, status)
	if err != nil {
		return nil, err
	}
	return json.Marshal(recordEnvelope{FormatVersion: envelopeFormatVersion, Kind: recordKindObject, Object: of})
}

// testSeedRecordForInstance is [SeedRecordForInstance]'s pre-#364 signature,
// kept for tests that build their own raw [staterecord.Store] (including
// ones that wrap it to intercept a Get, like raceOnGet in
// recordseed_test.go) and call it with a bare key prefix rather than an
// already-built [*RecordStore]. providerString(addrs.AbsProviderConfig{})
// is "", matching what every one of these fixtures wrote before the
// envelope's Provider field existed.
func testSeedRecordForInstance(ctx context.Context, store staterecord.Store, keyPrefix string, addr addrs.AbsResourceInstance, val cty.Value, private []byte, status states.ObjectStatus) (SeedResult, error) {
	return SeedRecordForInstance(ctx, NewRecordEnvelopeStore(store, keyPrefix), addr, addrs.AbsProviderConfig{}, val, private, status)
}

func decodeRecordPayload(raw []byte) (cty.Value, []byte, states.ObjectStatus, error) {
	env, err := decodeEnvelope(raw)
	if err != nil {
		return cty.NilVal, nil, 0, err
	}
	if env.Object == nil {
		return cty.NilVal, nil, 0, fmt.Errorf("the stored record carries no object to materialize (kind %q)", env.Kind)
	}
	return decodeObjectValue(env.Object)
}

// This file exists so the pre-GitHub-issue-#364 test suite - written
// against four disjoint namespaces and three no-List wrapper types
// (LocatedStore, ResidueStore, ProvisionedStore) - keeps exercising the
// SAME business logic (classifyResidue, fillResidue, declaresCreateProvisioners
// and friends) through the ONE merged [RecordStore] without every call site
// needing to be rewritten by hand. Each type below is a thin, test-only
// view with the OLD type's exact method shape, built entirely on RecordStore's
// exported and package-private surface - no shortcuts into the store that
// production code does not also have.

// testResidueStore is [ResidueStore]'s old shape, kept for tests.
type testResidueStore struct{ rs *RecordStore }

// newTestResidueStore wraps store as estate's residue view, exactly
// [NewResidueStore]'s old signature.
func newTestResidueStore(store staterecord.Store, estate string) *testResidueStore {
	rs := NewRecordEnvelopeStore(store, RecordKeyPrefix(estate))
	if rs == nil {
		return nil
	}
	return &testResidueStore{rs: rs}
}

func (s *testResidueStore) Get(ctx context.Context, addr addrs.AbsResourceInstance) (attrs map[string]cty.Value, version string, exists bool, err error) {
	if s == nil {
		return nil, "", false, nil
	}
	attrs, version, _, found, err := s.rs.getResidue(ctx, addr)
	return attrs, version, found, err
}

func (s *testResidueStore) Put(ctx context.Context, addr addrs.AbsResourceInstance, attrs map[string]cty.Value, expectedVersion string) (string, error) {
	if s == nil {
		return "", errNilTestStore(addr)
	}
	rf, err := encodeResidueFields(attrs)
	if err != nil {
		return "", err
	}
	return s.rs.mergeEnvelope(ctx, addr, expectedVersion, func(env *recordEnvelope) {
		env.Residue = rf
	})
}

func (s *testResidueStore) Delete(ctx context.Context, addr addrs.AbsResourceInstance, expectedVersion string) error {
	if s == nil {
		return nil
	}
	_, err := s.rs.mergeEnvelope(ctx, addr, expectedVersion, func(env *recordEnvelope) {
		env.Residue = nil
	})
	return err
}

// testLocatedStore is [LocatedStore]'s old shape, kept for tests.
type testLocatedStore struct{ rs *RecordStore }

func newTestLocatedStore(store staterecord.Store, estate string) *testLocatedStore {
	rs := NewRecordEnvelopeStore(store, RecordKeyPrefix(estate))
	if rs == nil {
		return nil
	}
	return &testLocatedStore{rs: rs}
}

func (s *testLocatedStore) Get(ctx context.Context, addr addrs.AbsResourceInstance) (rec LocatedRecord, version string, exists bool, err error) {
	if s == nil {
		return LocatedRecord{}, "", false, nil
	}
	rec, version, _, found, err := s.rs.GetIdentity(ctx, addr)
	return rec, version, found, err
}

func (s *testLocatedStore) Put(ctx context.Context, addr addrs.AbsResourceInstance, rec LocatedRecord, expectedVersion string) (string, error) {
	if s == nil {
		return "", errNilTestStore(addr)
	}
	// [LocatedStore.Put]'s old validation, restated here rather than
	// relied on from the read side: a caller writing a record this
	// deliberately incomplete is the defect the old store refused before
	// it ever reached disk, and GetIdentity's read-side check alone would
	// let it land first and only be refused on the next read.
	if rec.Empty() {
		return "", fmt.Errorf("refusing to record an empty identity for %s", addr)
	}
	for name, v := range rec.Components {
		if v == "" {
			return "", fmt.Errorf("refusing to record an identity for %s whose %q component is empty", addr, name)
		}
	}
	return s.rs.mergeEnvelope(ctx, addr, expectedVersion, func(env *recordEnvelope) {
		env.Identity = &identityPayload{ImportID: rec.ImportID, Attrs: rec.Components}
	})
}

func (s *testLocatedStore) Delete(ctx context.Context, addr addrs.AbsResourceInstance, expectedVersion string) error {
	if s == nil {
		return nil
	}
	_, err := s.rs.mergeEnvelope(ctx, addr, expectedVersion, func(env *recordEnvelope) {
		env.Identity = nil
	})
	return err
}

// testProvisionedStore is [ProvisionedStore]'s old shape, kept for tests.
type testProvisionedStore struct{ rs *RecordStore }

func newTestProvisionedStore(store staterecord.Store, estate string) *testProvisionedStore {
	rs := NewRecordEnvelopeStore(store, RecordKeyPrefix(estate))
	if rs == nil {
		return nil
	}
	return &testProvisionedStore{rs: rs}
}

func (s *testProvisionedStore) Get(ctx context.Context, addr addrs.AbsResourceInstance) (tainted bool, version string, exists bool, err error) {
	if s == nil {
		return false, "", false, nil
	}
	tainted, version, keyExists, err := s.rs.getProvisioned(ctx, addr)
	return tainted, version, keyExists && tainted, err
}

func (s *testProvisionedStore) Put(ctx context.Context, addr addrs.AbsResourceInstance, expectedVersion string) (string, error) {
	if s == nil {
		return "", errNilTestStore(addr)
	}
	return s.rs.mergeEnvelope(ctx, addr, expectedVersion, func(env *recordEnvelope) {
		env.Provisioned = &provisionedFields{Tainted: true}
	})
}

func (s *testProvisionedStore) Delete(ctx context.Context, addr addrs.AbsResourceInstance, expectedVersion string) error {
	if s == nil {
		return nil
	}
	_, err := s.rs.mergeEnvelope(ctx, addr, expectedVersion, func(env *recordEnvelope) {
		env.Provisioned = nil
	})
	return err
}

func errNilTestStore(addr addrs.AbsResourceInstance) error {
	return &nilTestStoreError{addr: addr}
}

type nilTestStoreError struct{ addr addrs.AbsResourceInstance }

func (e *nilTestStoreError) Error() string {
	return "no record store is configured, so " + e.addr.String() + "'s record cannot be written"
}
