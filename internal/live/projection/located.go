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
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
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

// materializeLocated is [builder.materialize]'s front door for GitHub issue
// #270's record-located instances (identity.ClassRecordLocated).
//
// It is deliberately thin, and the thinness is the design. All it does is
// answer the one question the configuration cannot: which live object is
// this? Once [LocatedStore.Get] has returned an import identity, the
// instance is handed to [builder.materialize] as an ordinary [wanted], and
// everything downstream of wanted.importID is unchanged - the same import,
// the same read, the same ownership rule, the same encoding, the same
// dependency computation. A located resource is not a special kind of
// prior-state entry; it is an ordinary one whose identity string arrived
// from a different place.
//
// That is the opposite of [builder.materializeRecord], which bypasses
// importAndRead entirely because a record-backed instance has no cloud
// object to read. Here the cloud object is the whole point.
//
// # The three answers, and why "not found" is not an error
//
// A located record that does not exist produces an ordinary
// [ReasonAbsent] omission, so the plan proposes a CREATE. That is the same
// answer for two different situations, and making them indistinguishable is
// the ruling rather than a shortcut: an instance that has never been
// created and one whose record was LOST both want a create proposed, and
// internal/live/foreign then surfaces the live object as unclaimed rather
// than as an orphan to destroy. The failure mode is an announced duplicate,
// never a silent deletion. Issue #270 states the trade in those terms and
// accepts it.
//
// A store that cannot be read at all, or a record that decodes to another
// address, is an error - [LocatedStore.Get]'s own refusals - because
// continuing past either would bind this instance to an identity that is
// not its, and a wrong identity is invisible to every verdict-level check.
//
// # No undeclared parameter
//
// [builder.materializeRecord] takes one, because the record namespace is
// enumerated to find records whose configuration was deleted. Nothing
// enumerates the located namespace, by construction, so there is no way to
// arrive here without a declared address and no parameter to carry the
// distinction. A located key with no configuration is inert: it costs a few
// bytes and is overwritten the next time the same address is created. That
// is the cost of never letting a stored ID drive a cloud deletion, and it
// is the cost issue #270 chose.
func (b *builder) materializeLocated(ctx context.Context, addr addrs.AbsResourceInstance) {
	if b.opts.LocatedStore == nil {
		// Reachable only if a caller resolves a located type without also
		// configuring a store. internal/live/lint's admission gate makes
		// that impossible for a configuration - identity.ClassRecordLocated
		// is produced only when the root module's live block declares a
		// record_store - so this is an internal inconsistency rather than an
		// operator's mistake, and it is stated as one. It is also the
		// backstop that lets tools/estate-plan demote the markerless-type
		// refusal safely: the demotion is only sound while a run that
		// reaches here without a store stops, loudly, naming what is
		// missing.
		detail := fmt.Sprintf(
			"%s resolved to a record-located identity, but no record store was configured for this projection. This is an internal inconsistency: a type whose live object carries no ownership marker should never reach here without a live block's record_store, which is the only thing that can say which object it is.",
			addr,
		)
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryLocatedNoStore, detail))
		b.omitFailed(addr, detail)
		return
	}

	typeName := addr.Resource.Resource.Type

	rec, version, exists, err := b.opts.LocatedStore.Get(ctx, addr)
	if err != nil {
		detail := fmt.Sprintf("Reading the located record for %s failed: %s.", addr, err)
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot read a located record", detail))
		b.omitFailed(addr, detail)
		return
	}
	if !exists {
		b.omit(addr, ReasonAbsent,
			fmt.Sprintf(
				"No record of which live %s %s owns exists yet, so this resource has not been created yet, or its record was lost. Either way the plan will propose creating it: a %s carries no ownership marker, so nothing else can say which object it is. If the object does exist, it is reported as unclaimed rather than destroyed.",
				typeName, addr, typeName,
			),
			fmt.Sprintf("no located record exists yet saying which live %s it owns.", typeName),
		)
		return
	}

	b.locatedVersions = append(b.locatedVersions, RecordVersion{Addr: addr, Version: version})

	// values is what carries a COMPOSITE identity through, and it needs no
	// new transport: [wanted.values] is the identity-attribute-per-component
	// form [identity.Resolution.IdentityValues] already holds for a concrete
	// instance, and importTarget/identityFromValues already turn it into the
	// provider's own identity object, checked against the provider's own
	// identity schema. A single-string record leaves it nil and takes the
	// importID path exactly as before.
	b.materialize(ctx, wanted{
		addr:     addr,
		importID: rec.ImportID,
		values:   rec.Components,
	})
}

// writeBackLocated is [WriteBack]'s located half: after an apply, every
// record-located instance's import identity is written to the store, and
// every located record whose instance the apply removed is deleted.
//
// This is the half that makes the whole mechanism true rather than
// hypothetical. The plan side reads an identity out of the store; if
// nothing ever writes one, every run reads unbound and proposes a create
// forever. The floci crossing in live/e2e/record-store is what proves the
// pair works end to end - apply, delete the state file, replan, and find
// the same object - and no unit test can make that claim.
//
// # Where the identity comes from
//
// [identity.LocatedImportID] reads the applied object's own "id", which is
// the attribute OpenTofu's import path round-trips. It is the same schema
// fact [identity.LocatedType] required before admitting the type, so a type
// that got this far has one by construction - which is why an object that
// arrives here without a usable one is an ERROR rather than a skip. A skip
// would leave the estate with a live object and no record of it: the next
// run would propose creating a second one, and the operator would learn
// about it from the cloud bill.
//
// # Deleting a record is not deleting an object
//
// The delete loop mirrors the record-backed one and means something
// different. There, deleting the record IS deleting the resource. Here the
// cloud object is destroyed by the ordinary apply, through the provider,
// and this only removes the now-meaningless note saying where it was. The
// reverse case - a record with no instance in the final state and none in
// LocatedVersions either - is unreachable from here and is deliberately
// left inert rather than swept, because sweeping it would need the
// enumeration this whole design refuses to build.
func writeBackLocated(ctx context.Context, req WriteBackRequest) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	if req.LocatedStore == nil {
		return diags
	}

	seen := make(map[string]bool, len(req.LocatedVersions))

	if req.FinalState != nil {
		for _, entry := range req.FinalState.AllResourceInstanceObjectAddrs() {
			if entry.DeposedKey != states.NotDeposed {
				// Same reason the record-backed loop skips one: a deposed
				// object is a mid-replace leftover the graph is about to
				// discard, and the current object is what this address's
				// identity should be read from.
				continue
			}
			addr := entry.Instance
			typeName := addr.Resource.Resource.Type

			res := req.FinalState.Resource(addr.ContainingResource())
			if res == nil {
				continue
			}
			schema, _ := req.Schemas.ResourceTypeConfig(res.ProviderConfig.Provider, addrs.ManagedResourceMode, typeName)
			if schema == nil || schema.Block == nil {
				continue
			}
			// The admission predicate itself, re-asked rather than
			// remembered. Asking the same function the same question is
			// what keeps the set that gets written identical to the set
			// that gets read; a second, looser rule here is how a type
			// would come to be materialized from a record nothing writes,
			// or written to a record nothing reads.
			if !identity.LocatedType(typeName, map[string]providers.Schema{typeName: *schema}) {
				continue
			}

			ri := res.Instance(addr.Resource.Key)
			if ri == nil || ri.Current == nil {
				continue
			}
			seen[addr.String()] = true

			obj, err := ri.Current.Decode(schema.Block.ImpliedType())
			if err != nil {
				diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot record a located identity",
					fmt.Sprintf("Recording which live %s %s owns failed: its final state could not be decoded: %s.", typeName, addr, err),
				))
				continue
			}
			// Which of the two forms this type's identity takes is the
			// provider's own account of it, re-asked here rather than
			// remembered, for the same reason LocatedType is above: one
			// function answering one question is what keeps the set that
			// gets written identical to the set that gets read.
			components, recordable := identity.LocatedIdentityComponents(typeName, *schema)
			rec := LocatedRecord{}
			if recordable {
				if len(components) == 0 {
					rec.ImportID, recordable = identity.LocatedImportID(obj.Value)
				} else {
					// A composite identity is recorded as an OBJECT and with
					// no string at all. "id" holds the bare leaf for such a
					// type, so recording it as the import ID would store a
					// fragment that reads back as a whole identity - the
					// exact defect this branch exists to close.
					rec.Components, recordable = identity.LocatedIdentity(obj.Value, components)
				}
			}
			if !recordable || rec.Empty() {
				diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot record a located identity",
					fmt.Sprintf(
						"Recording which live %s %s owns failed: the applied object carries no usable identity to record. A %s carries no ownership marker, so without this record no later run can find the object again, and the next plan would propose creating a second one.",
						typeName, addr, typeName,
					),
				))
				continue
			}
			if _, err := req.LocatedStore.Put(ctx, addr, rec, priorVersion(req.LocatedVersions, addr)); err != nil {
				diags = diags.Append(writeBackConflictDiag(addr, "Writing", err))
			}
		}
	}

	for _, rv := range req.LocatedVersions {
		if seen[rv.Addr.String()] {
			continue
		}
		if err := req.LocatedStore.Delete(ctx, rv.Addr, rv.Version); err != nil {
			diags = diags.Append(writeBackConflictDiag(rv.Addr, "Deleting", err))
		}
	}

	return diags
}

// SummaryLocatedNoStore is the summary of the refusal
// [builder.materializeLocated] raises when a located instance reaches the
// projection with no store to look it up in. Named for the same reason
// [SummaryOutsideEstate] is: internal/live/refusalscan requires every
// diagnostic this fork raises to have a registry entry, and the entry and
// the diagnostic have to name one string.
const SummaryLocatedNoStore = "Record-located instance with no record store"

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
	// object this instance owns: the answer to "which object is this" for
	// every type whose identity is one server-minted string.
	ImportID string `json:"importID"`

	// Identity is the same answer for a type whose identity is COMPOSITE -
	// a server-minted leaf under a config-known parent, where the provider
	// sets "id" to the bare leaf and the import path expects the whole
	// thing. It is one string per component, named as the provider's own
	// identity schema names them, and it is what
	// [identity.LocatedIdentityComponents] decides the shape of.
	//
	// Empty for a type whose identity is the string, which is what every
	// record written before composite identities existed looks like. That
	// is why it is omitempty and why the format version did not move: an
	// old record decodes into this struct unchanged and still means exactly
	// what it meant.
	//
	// The two are not alternatives to be reconciled. A composite record
	// carries the object and an empty ImportID, because there is no string
	// form of a composite identity that this fork is willing to invent -
	// issue #105's rule, kept by having nothing to join. A single-string
	// record carries the string and no object. [LocatedStore.Get] requires
	// one or the other and refuses a record carrying neither.
	Identity map[string]string `json:"identity,omitempty"`
}

// LocatedRecord is one record-located instance's identity as the store holds
// it: exactly one of the two forms [locatedPayload] documents.
//
// It is a struct rather than two parameters because two independently
// written branches extending the same call by position is a merge git
// resolves silently and wrongly; a named field cannot land in the wrong
// slot.
type LocatedRecord struct {
	// ImportID is the identity of a type identified by one string.
	ImportID string

	// Components is the identity of a type identified by several, one
	// string per identity-schema attribute.
	Components map[string]string
}

// Empty reports whether this record says nothing about which object an
// instance owns, which is the one thing a stored record may never do.
func (r LocatedRecord) Empty() bool {
	return r.ImportID == "" && len(r.Components) == 0
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
func (s *LocatedStore) Get(ctx context.Context, addr addrs.AbsResourceInstance) (rec LocatedRecord, version string, exists bool, err error) {
	if s == nil {
		return LocatedRecord{}, "", false, nil
	}
	key := LocatedKey(s.estate, addr)
	payload, version, exists, err := s.store.Get(ctx, key)
	if err != nil {
		return LocatedRecord{}, "", false, fmt.Errorf("reading the located record for %s: %w", addr, err)
	}
	if !exists {
		return LocatedRecord{}, "", false, nil
	}
	var stored locatedPayload
	if err := json.Unmarshal(payload, &stored); err != nil {
		return LocatedRecord{}, "", false, fmt.Errorf("decoding the located record for %s: %w", addr, err)
	}
	if stored.FormatVersion != locatedFormatVersion {
		return LocatedRecord{}, "", false, fmt.Errorf("the located record for %s names format %q, which this version of choudoufu does not understand", addr, stored.FormatVersion)
	}
	if stored.Address != addr.String() {
		// The key said one address and the payload says another. Refusing
		// is the only safe answer: continuing would bind this instance to
		// whatever object the OTHER address names, and a wrong identity is
		// invisible to every verdict-level check - the plan would be empty
		// and the marker would be right about the wrong object.
		return LocatedRecord{}, "", false, fmt.Errorf("the located record stored for %s says it is for %s; refusing to bind an instance to another resource's identity", addr, stored.Address)
	}
	out := LocatedRecord{ImportID: stored.ImportID, Components: stored.Identity}
	if out.Empty() {
		return LocatedRecord{}, "", false, fmt.Errorf("the located record for %s carries an empty identity", addr)
	}
	for name, v := range out.Components {
		if v == "" {
			// A component recorded empty is a partial identity wearing the
			// shape of a whole one. It would build an identity object the
			// provider accepts and no object answers, which is a wrong
			// identity rather than a missing one.
			return LocatedRecord{}, "", false, fmt.Errorf("the located record for %s carries an empty %q component", addr, name)
		}
	}
	return out, version, true, nil
}

// Put records rec as addr's identity, conditional on the key's current
// version - expectedVersion "" asserting that nothing is recorded yet, the
// same convention [staterecord.Store.PutIfVersion] uses. It returns the new
// version.
func (s *LocatedStore) Put(ctx context.Context, addr addrs.AbsResourceInstance, rec LocatedRecord, expectedVersion string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("no record store is configured, so %s's identity cannot be recorded", addr)
	}
	if rec.Empty() {
		return "", fmt.Errorf("refusing to record an empty identity for %s", addr)
	}
	for name, v := range rec.Components {
		if v == "" {
			return "", fmt.Errorf("refusing to record an identity for %s whose %q component is empty", addr, name)
		}
	}
	payload, err := json.Marshal(locatedPayload{
		FormatVersion: locatedFormatVersion,
		Address:       addr.String(),
		ImportID:      rec.ImportID,
		Identity:      rec.Components,
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
