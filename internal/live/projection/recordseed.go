// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"bytes"
	"context"
	"fmt"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/states"
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
// wrote=false with no error: re-running a migration over the same state file
// is documented as a no-op, and this keeps it one.
//
// A nil store makes this an immediate no-op - a configuration with no
// record_store block declared, where a record-backed type is not admitted
// for planning in the first place.
func SeedRecordForInstance(ctx context.Context, store staterecord.Store, keyPrefix string, addr addrs.AbsResourceInstance, val cty.Value, private []byte, status states.ObjectStatus) (wrote bool, err error) {
	if store == nil {
		return false, nil
	}

	payload, err := encodeRecordPayload(val, private, status)
	if err != nil {
		return false, fmt.Errorf("encoding the record for %s: %w", addr, err)
	}

	key := RecordKey(keyPrefix, addr)
	existing, version, exists, getErr := store.Get(ctx, key)
	if getErr != nil {
		return false, fmt.Errorf("reading the existing record for %s before writing: %w", addr, getErr)
	}
	if exists {
		if bytes.Equal(existing, payload) {
			return false, nil
		}
		return false, fmt.Errorf(
			"the record store already holds a different record for %s (version %s), so this run left it alone: the stored value may be newer than the state file this migration was given, and a record is the only carrier a record-backed resource's value has",
			addr, displayVersion(version))
	}

	// PutIfVersion with the store's "" (no record) sentinel rather than an
	// unconditional put: the Get above can go stale between the two calls,
	// and a second writer creating the record in that window should lose the
	// race loudly, exactly the way every other write to this store does.
	if _, putErr := store.PutIfVersion(ctx, key, payload, ""); putErr != nil {
		return false, fmt.Errorf("writing the record for %s: %w", addr, putErr)
	}
	return true, nil
}
