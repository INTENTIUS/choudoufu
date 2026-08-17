// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// This file is the record store answering the OTHER question (issue #270).
//
// A record-BACKED resource has no cloud object at all: the record is the
// object, and deleting the record is deleting the thing. That is why
// builder.discoverOrphanedRecords may list the record namespace and propose
// destroying every key the configuration no longer declares - for that
// class, the key IS the estate's inventory.
//
// A record-LOCATED resource is the opposite shape. The object exists in the
// cloud; what it has nowhere to carry is a marker. The record answers "which
// object is this" and nothing else. Reading it as "may I delete this" would
// turn a lost or stale key into a cloud deletion, which is precisely the
// failure the maintainer ruling of 2026-08-17 forbids: an ID is not a
// permission.
//
// So the two must not share a namespace, and this file is where that stops
// being a thing to remember. It is the third application of a construction
// already load-bearing twice here - record.go's recordNamespaceRoot keeps
// "tofu-records" disjoint from live/RECEIPTS.md's "tofu-receipts", and
// hint_store.go's hintNamespaceRoot extends it to "tofu-hints" so that
// orphan discovery "can never see a hint key". A located key is a fourth
// root under the same rule, and internal/configs' validateRecordStoreKeyPrefix
// refuses an operator-supplied key_prefix rooted at any of them.
//
// The namespace alone is only half of it, because a namespace is a
// convention and a convention can be misread by the next caller. The other
// half is that this file exposes NO ENUMERATION. [LocatedStore] is a
// point-lookup API keyed by [addrs.AbsResourceInstance]: to read a located
// record you must already hold the address of a resource block the
// configuration declares, so there is no way to ask "what is in here" and
// therefore nothing for a destroy path to walk. builder.discoverOrphanedRecords
// takes a staterecord.Store and calls List on it; a LocatedStore cannot be
// passed to it, because it is not a staterecord.Store and has no List of its
// own to reach for.
//
// What a careless caller would have to do to defeat this is written out at
// [LocatedStore] - it is deliberately not one slip.

// locatedNamespaceRoot is the literal segment every record-located key lives
// under. It is a different literal from [recordNamespaceRoot]
// ("tofu-records"), from hint_store.go's hintNamespaceRoot ("tofu-hints")
// and from live/RECEIPTS.md's "tofu-receipts", on purpose and for the reason
// this file's own comment gives: the enumeration that feeds the destroy path
// lists the record root, so a located key must be somewhere that listing
// physically cannot reach.
//
// Note it is NOT derived from a record_store block's key_prefix override.
// The hint's key is rooted the same unconditional way, and for the same
// reason: an override that could move a located key under the record root
// would hand back exactly the collision this root exists to make
// impossible. internal/configs' validateRecordStoreKeyPrefix refuses an
// override rooted here, which closes the other direction.
const locatedNamespaceRoot = "tofu-located"

// LocatedKeyPrefix is the key namespace one estate's record-located
// resources live under. Exported for the same reason [RecordKeyPrefix] and
// [HintKey] are: internal/command's store construction and this package's
// own namespace-safety tests both have to name one definition.
func LocatedKeyPrefix(estate string) string {
	return locatedNamespaceRoot + "/" + estate
}

// LocatedKey is the store key one record-located resource instance's
// identity lives at, for estate.
//
// The address is encoded exactly as [RecordKey] encodes it, by
// [recordKeyEncoding], because the constraint is the same one: a for_each
// key can carry characters SSM parameter names and S3 object keys forbid.
// Sharing the encoding is safe where sharing the ROOT would not be - the
// root is what decides which enumeration can see the key, and these keys
// are under a root nothing enumerates.
//
// There is deliberately no reverse of this function. [RecordAddr] exists
// because orphan discovery must recover an address from a key it finds by
// listing; nothing lists this namespace, so nothing needs to, and adding
// one would be building the first half of the destroy path this whole file
// exists to keep unbuilt.
func LocatedKey(estate string, addr addrs.AbsResourceInstance) string {
	return LocatedKeyPrefix(estate) + "/" + addr.Resource.Resource.Type + "/" + recordKeyEncoding.EncodeToString([]byte(addr.String()))
}

// locatedFormatVersion identifies the JSON shape [locatedPayload] writes.
// An unrecognized version is refused rather than guessed at: a located
// record that cannot be read means the instance reads unbound and a create
// is proposed, which is the announced-duplicate failure mode issue #270
// accepts, whereas a misread one would be a wrong identity - and a wrong
// identity is invisible to every verdict-level check.
const locatedFormatVersion = "tofu-live-located-v1"

// locatedPayload is what one record-located instance's key holds. It is an
// identity and nothing else: no attribute values, no provider private
// state, no status. That is not an oversight to be filled in later - it is
// the ruling. A record-backed instance's payload ([recordPayload]) carries
// the whole object because it IS the object; a located instance's object is
// in the cloud and is read from the cloud, so anything more here would be a
// second, unreconciled copy of state, which is the thing issue #73 removes.
type locatedPayload struct {
	// FormatVersion is always [locatedFormatVersion].
	FormatVersion string `json:"formatVersion"`

	// Address is the instance address this record is for, written out in
	// full. It is redundant with the key - the key encodes the same string
	// - and that is the point: [LocatedStore.Get] checks the two agree, so
	// a key that was copied, renamed or hand-edited into pointing at
	// another resource is refused instead of answering with another
	// resource's identity.
	Address string `json:"address"`

	// ImportID is the provider's import identity string for the live
	// object this instance owns: the answer to "which object is this", and
	// the whole of what a located record is for.
	ImportID string `json:"importID"`
}

// LocatedStore is the point-lookup view of an estate's located records.
//
// It is NOT a [staterecord.Store] and does not embed one: the underlying
// store is unexported, and the three methods below are the whole API. In
// particular there is no List, no Keys, and no iteration of any kind, so
// the enumeration that feeds the destroy path (builder.discoverOrphanedRecords,
// which takes a staterecord.Store and calls List on it) cannot be handed one
// of these and cannot be given a located key by one.
//
// Every method is keyed by [addrs.AbsResourceInstance] rather than by a
// string key, which is the second half of the same property: a caller can
// only ask about an address it already holds, and the addresses a run holds
// are the ones its configuration declares. "What else is in the store" is
// not a question this type can be asked.
//
// What a careless caller would have to do to defeat it, stated plainly
// because "it is structural" is a claim that should be checkable rather
// than believed. All three of these would be required, not any one:
//
//  1. Reach past this type to the raw [staterecord.Store] the run also
//     holds for record-backed resources - LocatedStore does not hand its
//     own out, so this means using the other one.
//  2. Call List on it with [LocatedKeyPrefix]'s output rather than
//     [Options.RecordKeyPrefix]. Passing the record prefix returns nothing
//     located, because the two roots share no prefix; the caller has to
//     name this namespace deliberately.
//  3. Feed the result to builder.materializeRecord with undeclared=true,
//     which is what writes an undeclared address into the projection so the
//     no-configuration-for-this-entry rule proposes destroying it. That
//     function decodes with [decodeRecordPayload], which refuses a located
//     payload outright ([locatedPayload] is not a [recordPayload] and
//     carries no value_type), so even step 3 fails loudly rather than
//     deleting anything.
//
// TestLocatedKeysAreInvisibleToOrphanDiscovery pins step 2, which is the
// one a refactor could plausibly get wrong.
type LocatedStore struct {
	store  staterecord.Store
	estate string
}

// NewLocatedStore wraps store as estate's located-record store. store is
// ordinarily the same [staterecord.Store] the run's record-backed resources
// use - one store, two disjoint namespaces in it - and estate is the
// resolved estate name.
//
// A nil store yields a nil LocatedStore, so a run with no record_store
// declared has nothing to consult and every located type stays refused; see
// internal/live/lint's admission gate and builder.materializeLocated's error.
func NewLocatedStore(store staterecord.Store, estate string) *LocatedStore {
	if store == nil || estate == "" {
		return nil
	}
	return &LocatedStore{store: store, estate: estate}
}

// Get reads the import identity recorded for addr. exists is false when
// nothing has been recorded for it, which is the ordinary "not created yet"
// answer and is not an error - and is also what a LOST record looks like,
// deliberately indistinguishable, because the two want the same handling:
// the instance reads unbound, a create is proposed, and internal/live/foreign
// surfaces the live object as unclaimed rather than as an orphan to destroy.
//
// version is the store's version for the key, for a later conditional Put
// or Delete.
func (s *LocatedStore) Get(ctx context.Context, addr addrs.AbsResourceInstance) (importID string, version string, exists bool, err error) {
	if s == nil {
		return "", "", false, nil
	}
	key := LocatedKey(s.estate, addr)
	payload, version, exists, err := s.store.Get(ctx, key)
	if err != nil {
		return "", "", false, fmt.Errorf("reading the located record for %s: %w", addr, err)
	}
	if !exists {
		return "", "", false, nil
	}
	var rec locatedPayload
	if err := json.Unmarshal(payload, &rec); err != nil {
		return "", "", false, fmt.Errorf("decoding the located record for %s: %w", addr, err)
	}
	if rec.FormatVersion != locatedFormatVersion {
		return "", "", false, fmt.Errorf("the located record for %s names format %q, which this version of choudoufu does not understand", addr, rec.FormatVersion)
	}
	if rec.Address != addr.String() {
		// The key said one address and the payload says another. Refusing
		// is the only safe answer: continuing would bind this instance to
		// whatever object the OTHER address names, and a wrong identity is
		// invisible to every verdict-level check - the plan would be empty
		// and the marker would be right about the wrong object.
		return "", "", false, fmt.Errorf("the located record stored for %s says it is for %s; refusing to bind an instance to another resource's identity", addr, rec.Address)
	}
	if rec.ImportID == "" {
		return "", "", false, fmt.Errorf("the located record for %s carries an empty identity", addr)
	}
	return rec.ImportID, version, true, nil
}

// Put records importID as addr's identity, conditional on the key's current
// version - expectedVersion "" asserting that nothing is recorded yet, the
// same convention [staterecord.Store.PutIfVersion] uses. It returns the new
// version.
func (s *LocatedStore) Put(ctx context.Context, addr addrs.AbsResourceInstance, importID, expectedVersion string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("no record store is configured, so %s's identity cannot be recorded", addr)
	}
	if importID == "" {
		return "", fmt.Errorf("refusing to record an empty identity for %s", addr)
	}
	payload, err := json.Marshal(locatedPayload{
		FormatVersion: locatedFormatVersion,
		Address:       addr.String(),
		ImportID:      importID,
	})
	if err != nil {
		return "", fmt.Errorf("encoding the located record for %s: %w", addr, err)
	}
	return s.store.PutIfVersion(ctx, LocatedKey(s.estate, addr), payload, expectedVersion)
}

// Delete removes addr's located record, conditional on expectedVersion.
//
// Deleting the RECORD is not deleting the OBJECT, and this method is the
// only place that distinction has to be got right by hand rather than by
// construction: it is called when the configuration destroys the resource
// and the cloud object is already gone. The reverse direction - a record
// present with no configuration - deliberately reaches nothing, because
// nothing enumerates this namespace to find it. Such a key is inert: it
// costs a few bytes and is overwritten the next time the same address is
// created.
func (s *LocatedStore) Delete(ctx context.Context, addr addrs.AbsResourceInstance, expectedVersion string) error {
	if s == nil {
		return nil
	}
	return s.store.Delete(ctx, LocatedKey(s.estate, addr), expectedVersion)
}
