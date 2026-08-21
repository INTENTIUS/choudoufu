// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// SeedRecordForInstance writes one record-backed resource instance's object
// into store, from an object its caller already holds rather than from a
// state file this package reads - GitHub issue #340's migrate-time half of
// the record lifecycle, and the exact sibling of
// [RecordResidueForInstance]'s migrate-time half of issue #275's.
//
// # Why this exists next to WriteBack rather than inside it
//
// [WriteBack] persists the same payload, but it is an APPLY's write-back: it
// walks a final state, it holds the plan-time versions the projection read,
// and it deletes the record of anything the final state no longer has. A
// migration has none of those three. It has one object per instance, taken
// from a tfstate nobody has planned against, no plan-time version because
// this estate's store has never been read, and no authority at all to delete
// a record for an address its state file happens not to mention. So the
// write is the same and the surrounding lifecycle is not, which is why the
// payload encoding is shared and the loop is not.
//
// # Why an existing record is never overwritten
//
// This reads before it writes, and a store that already holds a DIFFERENT
// payload for addr is an error rather than a write. The record is the only
// carrier a record-backed instance's identity has - live/MARKERS.md's "a
// wrong marker outranks a missing one" applies to it exactly as it applies
// to a tag - and the store's value can legitimately be NEWER than the state
// file a migration was pointed at (an estate migrated last week, applied
// since, and migrated again from the old tfstate). Clobbering there would
// replace a correct value with a stale one and produce an EMPTY plan that is
// wrong, which is the invisible failure the whole discipline exists to stop.
//
// A byte-identical existing record is the idempotent case and reports
// [SeedUnchanged] with no error: re-running a migration over the same state
// file is documented as a no-op, and this keeps it one.
//
// # The one byte-difference that is not a conflict
//
// GitHub issue #344. recordPayload.SensitiveAttrs did not always exist. A
// record written before it did carries no sensitivity at all, so re-seeding
// the SAME object after that field landed produces different bytes and hits
// the refusal above - a stale-record error for a record that is not stale.
//
// The rule that lets exactly that case through, and nothing else, is
// [sensitivityOnlyUpgrade]: same value, same private bytes, same status, and
// a stored sensitive-path set that is a strict SUBSET of the one this run
// would write. Same value is what makes it provably not the stale-record
// case #340 refuses - a stale record's whole signature is a DIFFERENT value -
// and strict subset is what makes the rewrite one-way: it can only ever add
// sensitivity to a record, never drop it. Anything else is the hard refusal
// it has always been, including a value that differs by one byte.
//
// The alternative considered and rejected was a format-version field on
// recordPayload with an upgrade path keyed off it. It fails on its own terms:
// a version field is itself new bytes in every record, so adding one makes
// every pre-existing record byte-different from its own re-migration - the
// exact breakage it would be introduced to fix, applied to the whole
// population instead of the sensitive slice of it. The payload's
// compatibility story is "an absent field means the old default", which is
// what unmarshalSensitivePaths and decodeObjectStatus already implement, and
// this is that story's write side.
//
// A nil store makes this an immediate no-op - a configuration with no
// record_store block declared, where a record-backed type is not admitted
// for planning in the first place.
func SeedRecordForInstance(ctx context.Context, store staterecord.Store, keyPrefix string, addr addrs.AbsResourceInstance, val cty.Value, private []byte, status states.ObjectStatus) (SeedResult, error) {
	if store == nil {
		return SeedUnchanged, nil
	}

	payload, err := encodeRecordPayload(val, private, status)
	if err != nil {
		return SeedUnchanged, fmt.Errorf("encoding the record for %s: %w", addr, err)
	}

	key := RecordKey(keyPrefix, addr)
	existing, version, exists, getErr := store.Get(ctx, key)
	if getErr != nil {
		return SeedUnchanged, fmt.Errorf("reading the existing record for %s before writing: %w", addr, getErr)
	}
	if exists {
		if bytes.Equal(existing, payload) {
			return SeedUnchanged, nil
		}
		upgrade, why := sensitivityOnlyUpgrade(existing, payload)
		if !upgrade {
			return SeedUnchanged, fmt.Errorf(
				"the record store already holds a different record for %s (version %s), so this run left it alone: the stored value may be newer than the state file this migration was given, and a record is the only carrier a record-backed resource's value has (%s)",
				addr, displayVersion(version), why)
		}
		// PutIfVersion against the version the Get above returned, not an
		// unconditional put: between the two calls another writer may have
		// replaced the record with something this comparison never saw, and
		// that writer must win rather than be silently clobbered by a
		// decision made about bytes that are no longer there.
		if _, putErr := store.PutIfVersion(ctx, key, payload, version); putErr != nil {
			return SeedUnchanged, fmt.Errorf("adding the recorded sensitivity of %s: %w", addr, putErr)
		}
		log.Printf("[TRACE] projection: rewrote the record for %s to add %s; the value, private bytes and status are unchanged", addr, why)
		return SeedMarksAdded, nil
	}

	// PutIfVersion with the store's "" (no record) sentinel rather than an
	// unconditional put: the Get above can go stale between the two calls,
	// and a second writer creating the record in that window should lose the
	// race loudly, exactly the way every other write to this store does.
	if _, putErr := store.PutIfVersion(ctx, key, payload, ""); putErr != nil {
		return SeedUnchanged, fmt.Errorf("writing the record for %s: %w", addr, putErr)
	}
	return SeedWritten, nil
}

// SeedResult is what [SeedRecordForInstance] did. It is an enumeration and
// not a bool because the migration report counts these separately and two of
// the three write: a fresh record and a sensitivity upgrade are both a store
// write, and reporting the upgrade as "newly recorded" would tell an operator
// something that is not true of an estate that has been migrated before.
type SeedResult int

const (
	// SeedUnchanged: the store already held exactly this record, or there was
	// no store at all. Nothing was written.
	SeedUnchanged SeedResult = iota

	// SeedWritten: no record existed for this address and one was created.
	SeedWritten

	// SeedMarksAdded: a record existed whose value, private bytes and status
	// were identical and whose recorded sensitivity was a strict subset of
	// this run's, and it was rewritten to carry the fuller set. See
	// [sensitivityOnlyUpgrade].
	SeedMarksAdded
)

// Wrote reports whether the store was written to.
func (r SeedResult) Wrote() bool { return r != SeedUnchanged }

// sensitivityOnlyUpgrade decides #344's one narrow question about two
// payloads that are not byte-identical: is the ONLY difference that the
// stored one records less sensitivity than this run would?
//
// It returns false for everything else, and the second return is the reason,
// phrased to be read inside the caller's message either way - what the
// upgrade adds when it is one, and what else differs when it is not.
//
// The comparison is made on the decoded fields rather than on the raw bytes
// minus a substring, for the obvious reason and one less obvious one: the
// obvious one is that a substring edit on JSON is not a comparison; the less
// obvious one is that ValueType, Attrs and Private are compared as the bytes
// [encodeRecordPayload] produced, which is a stricter test than comparing
// decoded cty values with RawEquals. A record whose value decodes equal but
// encodes differently - a different provider version's type, say - is NOT
// the case this is for, and byte comparison refuses it without having to
// reason about how cty compares two types that print the same.
func sensitivityOnlyUpgrade(existing, proposed []byte) (bool, string) {
	var was, now recordPayload
	if err := json.Unmarshal(existing, &was); err != nil {
		return false, "the stored record could not be read to compare it"
	}
	if err := json.Unmarshal(proposed, &now); err != nil {
		// Unreachable: proposed came out of encodeRecordPayload. Proven here
		// rather than assumed across the call.
		return false, "the record this run would write could not be read to compare it"
	}
	switch {
	case !bytes.Equal(was.ValueType, now.ValueType):
		return false, "the stored value's type differs"
	case !bytes.Equal(was.Attrs, now.Attrs):
		return false, "the stored value differs"
	case !bytes.Equal(was.Private, now.Private):
		return false, "the stored provider-private bytes differ"
	case was.Status != now.Status:
		return false, "the stored object status differs"
	}

	wasPaths, err := unmarshalSensitivePaths(was.SensitiveAttrs)
	if err != nil {
		return false, "the stored record's sensitive paths could not be read"
	}
	nowPaths, err := unmarshalSensitivePaths(now.SensitiveAttrs)
	if err != nil {
		return false, "the sensitive paths this run would write could not be read"
	}
	// Strict subset, both halves checked. The count comparison alone would
	// admit a set that swapped one path for another, and the containment
	// check alone would admit an equal set - which cannot reach here anyway,
	// since marshalSensitivePaths sorts its output and so encodes one set to
	// one byte string, but the check costs nothing and does not depend on
	// that.
	if len(wasPaths) >= len(nowPaths) {
		return false, "the stored record already records at least as much sensitivity as this run would write"
	}
	for _, had := range wasPaths {
		found := false
		for _, wants := range nowPaths {
			if had.Equals(wants) {
				found = true
				break
			}
		}
		if !found {
			return false, fmt.Sprintf("the stored record marks %s sensitive and this run would not", tfdiags.FormatCtyPath(had))
		}
	}
	return true, fmt.Sprintf("the sensitivity of %d more attribute path(s) that the stored record predates", len(nowPaths)-len(wasPaths))
}
