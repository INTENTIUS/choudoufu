// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"bytes"
	"context"
	"fmt"
	"log"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
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
// object for addr is an error rather than a write. The record is the only
// carrier a record-backed instance's identity has - live/MARKERS.md's "a
// wrong marker outranks a missing one" applies to it exactly as it applies
// to a tag - and the store's value can legitimately be NEWER than the state
// file a migration was pointed at (an estate migrated last week, applied
// since, and migrated again from the old tfstate). Clobbering there would
// replace a correct value with a stale one and produce an EMPTY plan that is
// wrong, which is the invisible failure the whole discipline exists to stop.
//
// The comparison is made on the DECODED object fields
// ([objectFields]) rather than on whole-envelope bytes, and that is GitHub
// issue #364's own adaptation: a pre-#364 record decodes as a v1 payload
// (see record.go's package comment) and a pre-existing v1 record's bytes
// never equal a freshly-encoded v2 envelope's bytes even when the object
// they describe is identical. Comparing the decoded fields is what keeps a
// migration idempotent across that format boundary; every field compared is
// still the exact bytes [encodeObjectFields] produced for each side, which
// is what makes it a stricter test than comparing decoded cty values with
// RawEquals - see [sensitivityOnlyUpgrade].
//
// A byte-identical existing record is the idempotent case and reports
// [SeedUnchanged] with no error: re-running a migration over the same state
// file is documented as a no-op, and this keeps it one. Nothing this package
// writes goes out as anything but a v2 envelope, so an unchanged v1 record
// is simply left as v1 rather than being rewritten just to modernize its
// wire shape.
//
// # The one byte-difference that is not a conflict
//
// GitHub issue #344. objectFields.SensitiveAttrs did not always exist. A
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
// A nil store makes this an immediate no-op - a configuration with no
// record_store block declared, where a record-backed type is not admitted
// for planning in the first place.
func SeedRecordForInstance(ctx context.Context, store *RecordStore, addr addrs.AbsResourceInstance, provider addrs.AbsProviderConfig, val cty.Value, private []byte, status states.ObjectStatus) (SeedResult, error) {
	if store == nil {
		return SeedUnchanged, nil
	}

	proposed, err := encodeObjectFields(val, private, status)
	if err != nil {
		return SeedUnchanged, fmt.Errorf("encoding the record for %s: %w", addr, err)
	}

	env, version, exists, getErr := store.getRaw(ctx, addr)
	if getErr != nil {
		return SeedUnchanged, fmt.Errorf("reading the existing record for %s before writing: %w", addr, getErr)
	}
	if exists {
		if env.Object == nil {
			// A key exists at this address but carries no object - e.g. an
			// earlier incarnation of this address recorded residue or a
			// provisioner taint under identity.ClassRecordLocated or an
			// ordinary marker-tracked class. Object and
			// identity/residue/provisioned addresses are mutually exclusive
			// by construction (identity.Class), so this should not arise
			// from a key this package wrote for the SAME class; refusing
			// rather than silently overwriting is the same discipline this
			// function applies to a genuinely different object.
			return SeedUnchanged, fmt.Errorf(
				"the record store already holds a non-object record for %s (version %s); refusing to overwrite it with a record-backed object",
				addr, displayVersion(version))
		}
		existing := env.Object
		if objectFieldsEqual(existing, proposed) {
			return SeedUnchanged, nil
		}
		upgrade, why := sensitivityOnlyUpgrade(existing, proposed)
		if !upgrade {
			return SeedUnchanged, fmt.Errorf(
				"the record store already holds a different record for %s (version %s), so this run left it alone: the stored value may be newer than the state file this migration was given, and a record is the only carrier a record-backed resource's value has (%s)",
				addr, displayVersion(version), why)
		}
		// mergeEnvelope against the version the read above returned, not an
		// unconditional put: between the two calls another writer may have
		// replaced the record with something this comparison never saw, and
		// that writer must win rather than be silently clobbered by a
		// decision made about bytes that are no longer there.
		if _, putErr := store.mergeEnvelope(ctx, addr, version, func(e *recordEnvelope) {
			e.Kind = recordKindObject
			e.Object = proposed
			e.Provider = providerString(provider)
		}); putErr != nil {
			return SeedUnchanged, fmt.Errorf("adding the recorded sensitivity of %s: %w", addr, putErr)
		}
		log.Printf("[TRACE] projection: rewrote the record for %s to add %s; the value, private bytes and status are unchanged", addr, why)
		return SeedMarksAdded, nil
	}

	// mergeEnvelope with the store's "" (no record) sentinel rather than an
	// unconditional put: the read above can go stale between the two calls,
	// and a second writer creating the record in that window should lose the
	// race loudly, exactly the way every other write to this store does.
	if _, putErr := store.mergeEnvelope(ctx, addr, "", func(e *recordEnvelope) {
		e.Kind = recordKindObject
		e.Object = proposed
		e.Provider = providerString(provider)
	}); putErr != nil {
		return SeedUnchanged, fmt.Errorf("writing the record for %s: %w", addr, putErr)
	}
	return SeedWritten, nil
}

// objectFieldsEqual compares two decoded object records field for field,
// each field the exact bytes [encodeObjectFields] produced - see
// [SeedRecordForInstance]'s own doc comment for why this replaces a
// whole-envelope byte comparison.
func objectFieldsEqual(a, b *objectFields) bool {
	return bytes.Equal(a.ValueType, b.ValueType) &&
		bytes.Equal(a.Attrs, b.Attrs) &&
		bytes.Equal(a.Private, b.Private) &&
		bytes.Equal(a.SensitiveAttrs, b.SensitiveAttrs) &&
		a.Status == b.Status
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
// object records that are not field-for-field identical: is the ONLY
// difference that the stored one records less sensitivity than this run
// would?
//
// It returns false for everything else, and the second return is the reason,
// phrased to be read inside the caller's message either way - what the
// upgrade adds when it is one, and what else differs when it is not.
func sensitivityOnlyUpgrade(was, now *objectFields) (bool, string) {
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
