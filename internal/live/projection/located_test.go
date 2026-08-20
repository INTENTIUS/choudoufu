// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// locatedTestAddr is one declared instance address the tests below key on.
func locatedTestAddr(t *testing.T, typeName, name string) addrs.AbsResourceInstance {
	t.Helper()
	return addrs.Resource{Mode: addrs.ManagedResourceMode, Type: typeName, Name: name}.
		Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
}

// TestLocatedKeysAreInvisibleToOrphanDiscovery is issue #270's central
// safety property, and the one a refactor could plausibly get wrong.
//
// builder.discoverOrphanedRecords lists [Options.RecordKeyPrefix] and
// materializes every key it can decode as an UNDECLARED prior-state entry,
// which is what makes the plan propose destroying it. For a record-BACKED
// resource that is correct, because the record is the object. For a
// record-LOCATED resource it would be a cloud deletion driven by a stored
// ID, which the 2026-08-17 ruling forbids outright: an ID is not a
// permission.
//
// Proven three ways rather than asserted once: lexically (the roots share
// no prefix in either direction), functionally (the exact List call
// discoverOrphanedRecords makes, against a store holding one of each,
// returns only the record), and by decoding (RecordAddr refuses a located
// key even if a listing somehow surfaced one).
func TestLocatedKeysAreInvisibleToOrphanDiscovery(t *testing.T) {
	const estate = "my-estate"
	ctx := context.Background()

	recordPrefix := RecordKeyPrefix(estate)
	locatedPrefix := LocatedKeyPrefix(estate)

	// Lexical, both directions. Neither namespace may be reachable by
	// listing the other.
	if strings.HasPrefix(locatedPrefix, recordPrefix+"/") || locatedPrefix == recordPrefix {
		t.Fatalf("LocatedKeyPrefix(%q) = %q lives under RecordKeyPrefix %q; orphan discovery would list it", estate, locatedPrefix, recordPrefix)
	}
	if strings.HasPrefix(recordPrefix, locatedNamespaceRoot+"/") {
		t.Fatalf("RecordKeyPrefix(%q) = %q lives under the located namespace %q", estate, recordPrefix, locatedNamespaceRoot)
	}
	// And disjoint from the two roots that were already load-bearing, so a
	// fourth root does not quietly re-open a closed hole.
	for _, other := range []string{hintNamespaceRoot, "tofu-receipts"} {
		if locatedNamespaceRoot == other || strings.HasPrefix(locatedNamespaceRoot, other+"/") {
			t.Errorf("the located namespace %q collides with %q", locatedNamespaceRoot, other)
		}
	}

	// Functional: one real record and one located key in the same store.
	store := localHintStore(t)
	recAddr := locatedTestAddr(t, "terraform_data", "seed")
	recordKey := RecordKey(recordPrefix, recAddr)
	if _, err := store.PutIfAbsent(ctx, recordKey, []byte(`{"value_type":"\"string\"","attrs":"\"x\""}`)); err != nil {
		t.Fatalf("writing the record fixture: %s", err)
	}
	locAddr := locatedTestAddr(t, "aws_eip_association", "bastion")
	located := NewLocatedStore(store, estate)
	if _, err := located.Put(ctx, locAddr, LocatedRecord{ImportID: "eipassoc-0123456789abcdef0"}, ""); err != nil {
		t.Fatalf("Put: %s", err)
	}

	keys, err := store.List(ctx, recordPrefix)
	if err != nil {
		t.Fatalf("List(%q): %s", recordPrefix, err)
	}
	if len(keys) != 1 || keys[0] != recordKey {
		t.Errorf("List(%q) = %v, want exactly the one record key %q.\n"+
			"A located key reaching orphan discovery is a cloud deletion driven by a stored ID.", recordPrefix, keys, recordKey)
	}

	// Decoding: even a listing that somehow surfaced a located key must not
	// yield an address for it under the record prefix.
	if got, ok := RecordAddr(recordPrefix, LocatedKey(estate, locAddr)); ok {
		t.Errorf("RecordAddr decoded a located key into %s under the record prefix; it must refuse it", got)
	}
}

// TestLocatedStoreExposesNoEnumeration pins the other half of the
// construction: the namespace is only safe while nothing can ask what is in
// it. [LocatedStore] is a point-lookup API keyed by
// addrs.AbsResourceInstance, so a caller can only ask about an address its
// configuration already declares.
//
// Two things are asserted, and the second is the load-bearing one. First,
// that no method here enumerates. Second, that *LocatedStore does not
// satisfy staterecord.Store - because if it did, it could be passed
// straight into builder.discoverOrphanedRecords, and every argument about
// which prefix that function lists would be beside the point.
func TestLocatedStoreExposesNoEnumeration(t *testing.T) {
	ty := reflect.TypeOf(&LocatedStore{})
	want := map[string]bool{"Get": true, "Put": true, "Delete": true}
	for i := 0; i < ty.NumMethod(); i++ {
		name := ty.Method(i).Name
		if !want[name] {
			t.Errorf("*LocatedStore has an exported method %q. The only three permitted are Get, Put and Delete, all keyed by a declared address; "+
				"anything that can return a set of keys re-creates the enumeration issue #270 exists to prevent.", name)
		}
		delete(want, name)
	}
	for name := range want {
		t.Errorf("*LocatedStore no longer has a %q method", name)
	}

	storeIface := reflect.TypeOf((*staterecord.Store)(nil)).Elem()
	if ty.Implements(storeIface) {
		t.Error("*LocatedStore satisfies staterecord.Store, so it can be handed to builder.discoverOrphanedRecords directly. " +
			"It must not: the whole point is that a located store has no List for a destroy path to walk.")
	}
}

// TestLocatedPayloadIsNotAReadableRecord closes the last step of the
// three-step chain a careless caller would have to complete to turn a
// located key into a destroy proposal (see [LocatedStore]). Even having
// reached past this type to the raw store, listed the located prefix by
// name, and fed the result to materializeRecord, the payload itself does
// not decode as a record - so the attempt fails loudly instead of writing
// an undeclared entry into the projection.
func TestLocatedPayloadIsNotAReadableRecord(t *testing.T) {
	payload, err := json.Marshal(locatedPayload{
		FormatVersion: locatedFormatVersion,
		Address:       "aws_eip_association.bastion",
		ImportID:      "eipassoc-0123456789abcdef0",
	})
	if err != nil {
		t.Fatalf("marshalling the located payload: %s", err)
	}
	if _, _, _, err := decodeRecordPayload(payload); err == nil {
		t.Error("decodeRecordPayload accepted a located payload. A located record carries an identity and nothing else; " +
			"reading one as a record-backed object's whole value would materialize a fabricated object.")
	}
}

// TestLocatedStore_roundTrip is the ordinary path: record an identity, read
// it back, delete it.
func TestLocatedStore_roundTrip(t *testing.T) {
	ctx := context.Background()
	store := localHintStore(t)
	located := NewLocatedStore(store, "my-estate")
	addr := locatedTestAddr(t, "aws_globalaccelerator_listener", "svc")

	if _, _, exists, err := located.Get(ctx, addr); err != nil || exists {
		t.Fatalf("Get before Put: exists=%v err=%v, want false/nil", exists, err)
	}

	const wantID = "arn:aws:globalaccelerator::123456789012:accelerator/abc/listener/def"
	version, err := located.Put(ctx, addr, LocatedRecord{ImportID: wantID}, "")
	if err != nil {
		t.Fatalf("Put: %s", err)
	}

	gotRec, gotVersion, exists, err := located.Get(ctx, addr)
	if err != nil {
		t.Fatalf("Get: %s", err)
	}
	if !exists {
		t.Fatal("Get reported the record absent immediately after Put")
	}
	// The rendered string, not merely "something came back": a wrong
	// identity is invisible to every verdict-level check.
	if gotRec.ImportID != wantID {
		t.Errorf("Get returned identity %q, want %q", gotRec.ImportID, wantID)
	}
	if gotVersion != version {
		t.Errorf("Get returned version %q, want the version Put reported, %q", gotVersion, version)
	}

	if err := located.Delete(ctx, addr, gotVersion); err != nil {
		t.Fatalf("Delete: %s", err)
	}
	if _, _, exists, err := located.Get(ctx, addr); err != nil || exists {
		t.Fatalf("Get after Delete: exists=%v err=%v, want false/nil", exists, err)
	}
}

// TestLocatedStore_lostRecordReadsUnbound is issue #270's accepted failure
// mode, pinned as behaviour rather than argued in a comment: lose the
// record and the instance reads unbound - exists false, no error, no
// identity - which is byte-for-byte what "never created" looks like. The
// two are deliberately indistinguishable, because they want the same
// handling: propose a create, and let internal/live/foreign surface the
// live object as unclaimed rather than as an orphan to destroy.
func TestLocatedStore_lostRecordReadsUnbound(t *testing.T) {
	ctx := context.Background()
	store := localHintStore(t)
	located := NewLocatedStore(store, "my-estate")
	addr := locatedTestAddr(t, "aws_eip_association", "bastion")

	version, err := located.Put(ctx, addr, LocatedRecord{ImportID: "eipassoc-0123456789abcdef0"}, "")
	if err != nil {
		t.Fatalf("Put: %s", err)
	}
	// The record is lost - an operator deleted the parameter, a store was
	// recreated, a prefix was renamed.
	if err := store.Delete(ctx, LocatedKey("my-estate", addr), version); err != nil {
		t.Fatalf("deleting the located record: %s", err)
	}

	gotRec, _, exists, err := located.Get(ctx, addr)
	if err != nil {
		t.Fatalf("Get after the record was lost returned an error: %s.\n"+
			"A lost located record must read as unbound, not as a failure: the run proposes a create and foreign surfaces the object as unclaimed.", err)
	}
	if exists || !gotRec.Empty() {
		t.Errorf("Get after the record was lost returned exists=%v rec=%+v, want false and empty", exists, gotRec)
	}

	// And nothing was left behind for an enumeration to find, in either
	// namespace.
	for _, prefix := range []string{RecordKeyPrefix("my-estate"), LocatedKeyPrefix("my-estate")} {
		keys, err := store.List(ctx, prefix)
		if err != nil {
			t.Fatalf("List(%q): %s", prefix, err)
		}
		if len(keys) != 0 {
			t.Errorf("List(%q) = %v, want nothing", prefix, keys)
		}
	}
}

// TestLocatedStore_refusesAMisdirectedRecord: the payload names the address
// it belongs to, and Get checks it against the address that was asked for.
// A key copied, renamed or hand-edited into pointing at another resource is
// refused rather than answering with the other resource's identity - which
// would bind an instance to the wrong live object, and a wrong marker
// outranks a missing one.
func TestLocatedStore_refusesAMisdirectedRecord(t *testing.T) {
	ctx := context.Background()
	store := localHintStore(t)
	const estate = "my-estate"
	located := NewLocatedStore(store, estate)

	mine := locatedTestAddr(t, "aws_eip_association", "bastion")
	theirs := locatedTestAddr(t, "aws_eip_association", "desktop")

	// A payload for "theirs" written under "mine"'s key.
	payload, err := json.Marshal(locatedPayload{
		FormatVersion: locatedFormatVersion,
		Address:       theirs.String(),
		ImportID:      "eipassoc-0123456789abcdef0",
	})
	if err != nil {
		t.Fatalf("marshalling: %s", err)
	}
	if _, err := store.PutIfAbsent(ctx, LocatedKey(estate, mine), payload); err != nil {
		t.Fatalf("writing the misdirected fixture: %s", err)
	}

	_, _, exists, err := located.Get(ctx, mine)
	if err == nil {
		t.Fatalf("Get accepted a record whose payload names %s; it must refuse rather than bind %s to another resource's identity", theirs, mine)
	}
	if exists {
		t.Error("Get reported the record as present while refusing it")
	}
	if !strings.Contains(err.Error(), theirs.String()) {
		t.Errorf("the refusal does not name the address the record claims (%s): %s", theirs, err)
	}
}

// TestLocatedStore_refusesAnUnreadableRecord: an unrecognized format
// version and an empty identity are both refused rather than guessed at.
// The safe direction here is a hard error, not an unbound read: an
// unbound read proposes a create, and proposing a create because a payload
// was one field short would announce a duplicate over a record that is
// actually present.
func TestLocatedStore_refusesAnUnreadableRecord(t *testing.T) {
	ctx := context.Background()
	const estate = "my-estate"
	addr := locatedTestAddr(t, "aws_eip_association", "bastion")

	for name, payload := range map[string][]byte{
		"unknown format": []byte(`{"formatVersion":"tofu-live-located-v99","address":"aws_eip_association.bastion","importID":"eipassoc-1"}`),
		"empty identity": []byte(`{"formatVersion":"` + locatedFormatVersion + `","address":"aws_eip_association.bastion","importID":""}`),
		"not json":       []byte(`{`),
	} {
		t.Run(name, func(t *testing.T) {
			store := localHintStore(t)
			if _, err := store.PutIfAbsent(ctx, LocatedKey(estate, addr), payload); err != nil {
				t.Fatalf("writing the fixture: %s", err)
			}
			if _, _, exists, err := NewLocatedStore(store, estate).Get(ctx, addr); err == nil {
				t.Errorf("Get accepted an unreadable record (exists=%v)", exists)
			}
		})
	}
}

// TestLocatedStore_nilIsInert: no record_store declared means no located
// store, and a nil one answers "nothing recorded" for reads while refusing
// writes by name. The refusal is what an operator who declared a live block
// and forgot the record_store has to hit; see the plan-time error in
// build.go.
func TestLocatedStore_nilIsInert(t *testing.T) {
	ctx := context.Background()
	addr := locatedTestAddr(t, "aws_eip_association", "bastion")

	if got := NewLocatedStore(nil, "my-estate"); got != nil {
		t.Errorf("NewLocatedStore(nil, estate) = %v, want nil", got)
	}
	if got := NewLocatedStore(localHintStore(t), ""); got != nil {
		t.Errorf("NewLocatedStore(store, \"\") = %v, want nil", got)
	}

	var s *LocatedStore
	if _, _, exists, err := s.Get(ctx, addr); err != nil || exists {
		t.Errorf("nil LocatedStore Get: exists=%v err=%v, want false/nil", exists, err)
	}
	if _, err := s.Put(ctx, addr, LocatedRecord{ImportID: "eipassoc-1"}, ""); err == nil {
		t.Error("nil LocatedStore Put succeeded; it must refuse, naming the missing store")
	}
}
