// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
)

// locatedTestAddr is one declared instance address the tests below key on.
func locatedTestAddr(t *testing.T, typeName, name string) addrs.AbsResourceInstance {
	t.Helper()
	return addrs.Resource{Mode: addrs.ManagedResourceMode, Type: typeName, Name: name}.
		Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
}

// TestRecordStoreIdentity_roundTrip is the ordinary path: record an
// identity into the shared envelope, read it back, delete the whole
// envelope. GitHub issue #364 folded what was a standalone *LocatedStore
// into [RecordStore]; this is that type's old TestLocatedStore_roundTrip,
// adapted to the one merged store.
func TestRecordStoreIdentity_roundTrip(t *testing.T) {
	ctx := context.Background()
	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("my-estate"))
	addr := locatedTestAddr(t, "aws_globalaccelerator_listener", "svc")

	if _, _, keyExists, identityFound, err := store.GetIdentity(ctx, addr); err != nil || keyExists || identityFound {
		t.Fatalf("GetIdentity before any write: keyExists=%v identityFound=%v err=%v, want false/false/nil", keyExists, identityFound, err)
	}

	const wantID = "arn:aws:globalaccelerator::123456789012:accelerator/abc/listener/def"
	version, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
		env.Identity = &identityPayload{ImportID: wantID}
	})
	if err != nil {
		t.Fatalf("mergeEnvelope: %s", err)
	}

	gotRec, gotVersion, keyExists, identityFound, err := store.GetIdentity(ctx, addr)
	if err != nil {
		t.Fatalf("GetIdentity: %s", err)
	}
	if !keyExists || !identityFound {
		t.Fatalf("GetIdentity reported keyExists=%v identityFound=%v immediately after the write, want true/true", keyExists, identityFound)
	}
	// The rendered string, not merely "something came back": a wrong
	// identity is invisible to every verdict-level check.
	if gotRec.ImportID != wantID {
		t.Errorf("GetIdentity returned identity %q, want %q", gotRec.ImportID, wantID)
	}
	if gotVersion != version {
		t.Errorf("GetIdentity returned version %q, want the version the write reported, %q", gotVersion, version)
	}

	if err := store.delete(ctx, addr, gotVersion); err != nil {
		t.Fatalf("delete: %s", err)
	}
	if _, _, keyExists, identityFound, err := store.GetIdentity(ctx, addr); err != nil || keyExists || identityFound {
		t.Fatalf("GetIdentity after delete: keyExists=%v identityFound=%v err=%v, want false/false/nil", keyExists, identityFound, err)
	}
}

// TestRecordStoreIdentity_lostRecordReadsUnbound is issue #270's accepted
// failure mode, pinned as behaviour rather than argued in a comment: lose
// the record and the instance reads unbound - keyExists false,
// identityFound false, no error, no identity - which is byte-for-byte what
// "never created" looks like. The two are deliberately indistinguishable,
// because they want the same handling: propose a create, and let
// internal/live/foreign surface the live object as unclaimed rather than as
// an orphan to destroy.
func TestRecordStoreIdentity_lostRecordReadsUnbound(t *testing.T) {
	ctx := context.Background()
	const estate = "my-estate"
	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix(estate))
	addr := locatedTestAddr(t, "aws_eip_association", "bastion")

	version, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
		env.Identity = &identityPayload{ImportID: "eipassoc-0123456789abcdef0"}
	})
	if err != nil {
		t.Fatalf("mergeEnvelope: %s", err)
	}
	// The record is lost - an operator deleted the parameter, a store was
	// recreated, a prefix was renamed.
	if err := store.delete(ctx, addr, version); err != nil {
		t.Fatalf("deleting the record: %s", err)
	}

	gotRec, _, keyExists, identityFound, err := store.GetIdentity(ctx, addr)
	if err != nil {
		t.Fatalf("GetIdentity after the record was lost returned an error: %s.\n"+
			"A lost record must read as unbound, not as a failure: the run proposes a create and foreign surfaces the object as unclaimed.", err)
	}
	if keyExists || identityFound || !gotRec.Empty() {
		t.Errorf("GetIdentity after the record was lost returned keyExists=%v identityFound=%v rec=%+v, want false/false/empty", keyExists, identityFound, gotRec)
	}

	// And nothing was left behind for orphan discovery to find.
	keys, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %s", err)
	}
	if len(keys) != 0 {
		t.Errorf("List() = %v, want nothing", keys)
	}
}

// TestRecordStoreIdentity_refusesAMisdirectedRecord: the envelope names the
// address it belongs to, and GetIdentity checks it against the address that
// was asked for. A key copied, renamed or hand-edited into pointing at
// another resource is refused rather than answering with the other
// resource's identity - which would bind an instance to the wrong live
// object, and a wrong marker outranks a missing one.
func TestRecordStoreIdentity_refusesAMisdirectedRecord(t *testing.T) {
	ctx := context.Background()
	const estate = "my-estate"
	prefix := RecordKeyPrefix(estate)
	rawStore := localHintStore(t)
	store := NewRecordEnvelopeStore(rawStore, prefix)

	mine := locatedTestAddr(t, "aws_eip_association", "bastion")
	theirs := locatedTestAddr(t, "aws_eip_association", "desktop")

	// An envelope for "theirs" written under "mine"'s key.
	payload, err := json.Marshal(recordEnvelope{
		FormatVersion: envelopeFormatVersion,
		Address:       theirs.String(),
		Kind:          recordKindIdentity,
		Identity:      &identityPayload{ImportID: "eipassoc-0123456789abcdef0"},
	})
	if err != nil {
		t.Fatalf("marshalling: %s", err)
	}
	if _, err := rawStore.PutIfAbsent(ctx, RecordKey(prefix, mine), payload); err != nil {
		t.Fatalf("writing the misdirected fixture: %s", err)
	}

	_, _, keyExists, identityFound, err := store.GetIdentity(ctx, mine)
	if err == nil {
		t.Fatalf("GetIdentity accepted a record whose payload names %s; it must refuse rather than bind %s to another resource's identity", theirs, mine)
	}
	if keyExists || identityFound {
		t.Error("GetIdentity reported the record as present while refusing it")
	}
	if !strings.Contains(err.Error(), theirs.String()) {
		t.Errorf("the refusal does not name the address the record claims (%s): %s", theirs, err)
	}
}

// TestRecordStoreIdentity_refusesAnUnreadableRecord: an empty identity is
// refused rather than guessed at. The safe direction here is a hard error,
// not an unbound read: an unbound read proposes a create, and proposing a
// create because a payload was one field short would announce a duplicate
// over a record that is actually present.
func TestRecordStoreIdentity_refusesAnUnreadableRecord(t *testing.T) {
	ctx := context.Background()
	const estate = "my-estate"
	prefix := RecordKeyPrefix(estate)
	addr := locatedTestAddr(t, "aws_eip_association", "bastion")

	for name, payload := range map[string][]byte{
		"empty identity": []byte(`{"format_version":2,"address":"aws_eip_association.bastion","kind":"identity","identity":{}}`),
		"not json":       []byte(`{`),
	} {
		t.Run(name, func(t *testing.T) {
			rawStore := localHintStore(t)
			if _, err := rawStore.PutIfAbsent(ctx, RecordKey(prefix, addr), payload); err != nil {
				t.Fatalf("writing the fixture: %s", err)
			}
			if _, _, keyExists, identityFound, err := NewRecordEnvelopeStore(rawStore, prefix).GetIdentity(ctx, addr); err == nil {
				t.Errorf("GetIdentity accepted an unreadable record (keyExists=%v identityFound=%v)", keyExists, identityFound)
			}
		})
	}
}

// TestRecordStoreIdentity_nilIsInert: no record_store declared means no
// record store, and a nil one answers "nothing recorded" for reads while
// refusing writes by name. The refusal is what an operator who declared a
// live block and forgot the record_store has to hit; see the plan-time
// error in build.go.
func TestRecordStoreIdentity_nilIsInert(t *testing.T) {
	ctx := context.Background()
	addr := locatedTestAddr(t, "aws_eip_association", "bastion")

	if got := NewRecordEnvelopeStore(nil, RecordKeyPrefix("my-estate")); got != nil {
		t.Errorf("NewRecordEnvelopeStore(nil, ...) = %v, want nil", got)
	}

	var s *RecordStore
	if _, _, keyExists, identityFound, err := s.GetIdentity(ctx, addr); err != nil || keyExists || identityFound {
		t.Errorf("nil RecordStore GetIdentity: keyExists=%v identityFound=%v err=%v, want false/false/nil", keyExists, identityFound, err)
	}
	if _, err := s.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
		env.Identity = &identityPayload{ImportID: "eipassoc-1"}
	}); err == nil {
		t.Error("nil RecordStore mergeEnvelope succeeded; it must refuse, naming the missing store")
	}
}
