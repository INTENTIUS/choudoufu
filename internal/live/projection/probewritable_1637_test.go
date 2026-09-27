// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1637: [RecordStore.ProbeWritable] is what lets #950's
// unmarked-apply refusal step aside, so a wrong "true" creates an object
// nothing can find again. These cases drive it with a store that refuses
// every write regardless of who runs the test, which the command tier's
// read-only arm (a chmodded directory) cannot do under root.

// writeDenyingStore refuses every conditional write the way a role with no
// s3:PutObject does, and passes reads through.
type writeDenyingStore struct {
	staterecord.Store
	denial func(key string) error
}

func (s *writeDenyingStore) PutIfVersion(_ context.Context, key string, _ []byte, _ string) (string, error) {
	return "", s.denial(key)
}

func (s *writeDenyingStore) PutIfAbsent(_ context.Context, key string, _ []byte) (string, error) {
	return "", s.denial(key)
}

func probeStore(t *testing.T) staterecord.Store {
	t.Helper()
	inner, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return inner
}

func TestRecordStore_probeWritable(t *testing.T) {
	ctx := context.Background()
	const prefix = "tofu-records/probe-1637"

	t.Run("a writable store with its sentinel", func(t *testing.T) {
		inner := probeStore(t)
		if _, err := inner.PutIfAbsent(ctx, SentinelKey(prefix), []byte(sentinelPayload)); err != nil {
			t.Fatal(err)
		}
		before, _, _, _ := inner.Get(ctx, SentinelKey(prefix))
		ok, err := NewRecordEnvelopeStore(inner, prefix).ProbeWritable(ctx)
		if err != nil || !ok {
			t.Fatalf("ProbeWritable = %v, %v; want true, nil", ok, err)
		}
		after, _, _, _ := inner.Get(ctx, SentinelKey(prefix))
		if string(after) != string(before) {
			t.Errorf("the probe changed the sentinel's content: %q -> %q", before, after)
		}
	})

	t.Run("a writable store with no sentinel yet", func(t *testing.T) {
		ok, err := NewRecordEnvelopeStore(probeStore(t), prefix).ProbeWritable(ctx)
		if err != nil || !ok {
			t.Fatalf("ProbeWritable = %v, %v; want true, nil", ok, err)
		}
	})

	for name, denial := range map[string]func(string) error{
		"AccessDenied": s3AccessDenied,
		"bare 403":     s3Forbidden,
	} {
		t.Run("a store that denies writes: "+name, func(t *testing.T) {
			inner := probeStore(t)
			if _, err := inner.PutIfAbsent(ctx, SentinelKey(prefix), []byte(sentinelPayload)); err != nil {
				t.Fatal(err)
			}
			ok, err := NewRecordEnvelopeStore(&writeDenyingStore{Store: inner, denial: denial}, prefix).ProbeWritable(ctx)
			if err != nil || ok {
				t.Fatalf("ProbeWritable = %v, %v; want false, nil: this run may read the store and not write it", ok, err)
			}
		})
	}

	t.Run("a store that fails the write", func(t *testing.T) {
		inner := probeStore(t)
		ok, err := NewRecordEnvelopeStore(&writeDenyingStore{Store: inner, denial: s3ServerError}, prefix).ProbeWritable(ctx)
		if ok || err == nil {
			t.Fatalf("ProbeWritable = %v, %v; want false and the error", ok, err)
		}
	})

	t.Run("a conflict proves the write was authorised", func(t *testing.T) {
		inner := probeStore(t)
		conflict := func(key string) error { return &staterecord.VersionConflictError{Key: key} }
		ok, err := NewRecordEnvelopeStore(&writeDenyingStore{Store: inner, denial: conflict}, prefix).ProbeWritable(ctx)
		if err != nil || !ok {
			t.Fatalf("ProbeWritable = %v, %v; want true, nil", ok, err)
		}
	})

	t.Run("no store", func(t *testing.T) {
		var s *RecordStore
		if ok, err := s.ProbeWritable(ctx); ok || err != nil {
			t.Fatalf("ProbeWritable on a nil store = %v, %v; want false, nil", ok, err)
		}
	})
}

// TestApplyRecordsIdentity pins which types the write-back can record,
// because #1637's exemption is only sound for those. aws_iam_group_policy
// is claim 17's type: no tags, and a ratified group:name row the apply
// fills in. A type with no ratified row and no identity the schema can
// record gets no record from an apply, so it must answer false.
func TestApplyRecordsIdentity(t *testing.T) {
	schema := func(names ...string) providers.Schema {
		attrs := map[string]*configschema.Attribute{}
		for _, n := range names {
			attrs[n] = &configschema.Attribute{Type: cty.String, Optional: true, Computed: true}
		}
		return providers.Schema{Block: &configschema.Block{Attributes: attrs}}
	}
	if !ApplyRecordsIdentity("aws_iam_group_policy", schema("id", "group", "name", "name_prefix", "policy")) {
		t.Error("aws_iam_group_policy: want true, its ratified row records group:name")
	}
	if ApplyRecordsIdentity("choudoufu_test_no_row_1637", schema("opaque")) {
		t.Error("a type with no ratified row and no recordable identity: want false")
	}
	if ApplyRecordsIdentity("aws_iam_group_policy", providers.Schema{}) {
		t.Error("no schema block: want false")
	}
}
