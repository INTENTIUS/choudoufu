// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"maps"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/states"
)

// tagCapturingStore is a real local store that remembers the object tags
// each write's context carried, keyed by the key written. It sits where the
// S3 store would, at the bottom of the production wrapper stack.
type tagCapturingStore struct {
	staterecord.Store
	mu   sync.Mutex
	tags map[string]map[string]string
}

func (s *tagCapturingStore) PutIfVersion(ctx context.Context, key string, payload []byte, expectedVersion string) (string, error) {
	s.mu.Lock()
	if s.tags == nil {
		s.tags = map[string]map[string]string{}
	}
	s.tags[key] = maps.Clone(staterecord.ObjectTags(ctx))
	s.mu.Unlock()
	return s.Store.PutIfVersion(ctx, key, payload, expectedVersion)
}

// TestARecordsAddressTagReachesTheBackendThroughTheProductionWrappers: the
// address travels in the context from the envelope store, down through the
// run cache and the trip counter, to the backend. A wrapper that built a
// fresh context would strip every record's tofu-address and nothing else
// would notice - the write would still succeed. GitHub issue #1337.
func TestARecordsAddressTagReachesTheBackendThroughTheProductionWrappers(t *testing.T) {
	staterecord.ResetRunCacheForTest(t)
	ctx := context.Background()
	bottom := &tagCapturingStore{Store: localHintStore(t)}
	prefix := RecordKeyPrefix("prod")
	store := NewRecordEnvelopeStore(staterecord.NewRunCache(staterecord.NewCountingStore(bottom, nil), prefix), prefix)

	addr := mustAddr(t, `module.net.aws_thing.x["a.b"]`)
	of, err := encodeObjectFields(cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("i")}), nil, states.ObjectReady)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
		env.Kind = recordKindObject
		env.Object = of
	}); err != nil {
		t.Fatalf("writing the record: %s", err)
	}
	if got, want := bottom.tags[RecordKey(prefix, addr)], markers.AddressObjectTags(addr); !maps.Equal(got, want) {
		t.Errorf("the record's write reached the backend tagged %v, want %v", got, want)
	}

	// A move writes a NEW object, tagged with where it is going.
	to := mustAddr(t, `module.net.aws_thing.renamed`)
	moved, err := store.MoveRecord(ctx, addr, to)
	if err != nil || !moved {
		t.Fatalf("MoveRecord = (%v, %v)", moved, err)
	}
	if got, want := bottom.tags[RecordKey(prefix, to)], markers.AddressObjectTags(to); !maps.Equal(got, want) {
		t.Errorf("the moved record reached the backend tagged %v, want %v: the copy must name the address it now records, not the one it left", got, want)
	}
}

// TestTheS3StoreIsOpenedWithItsEstatesTag: tofu-estate is set where the
// store is opened for an estate, from that same argument, so it cannot name
// another one.
func TestTheS3StoreIsOpenedWithItsEstatesTag(t *testing.T) {
	cfg := s3StoreConfig(aws.Config{Region: "us-east-1"}, &configs.LiveRecordStore{Type: "s3", Bucket: "b"}, recordStoreOptions{estate: "prod-eu"})
	if got, want := cfg.BaseTags, (map[string]string{markers.TagEstate: "prod-eu"}); !maps.Equal(got, want) {
		t.Errorf("BaseTags = %v, want %v", got, want)
	}
}
