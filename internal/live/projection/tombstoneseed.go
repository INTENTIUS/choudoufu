// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"

	"github.com/intentius/choudoufu/internal/addrs"
)

// SeedTombstoneForInstance directly adds one destroyed-identity entry to
// addr's record, the same shape [RecordStore.tombstone] writes when an
// apply's own destroy leaves an address with a known-gone occupant (see
// [tombstoneFields]'s own doc comment). The real production path is always
// through [WriteBack] - nothing else in this codebase writes a tombstone -
// so this exists for a caller that needs a real, round-tripped tombstone
// envelope without walking that whole apply flow, the same role
// [SeedLocatedForInstance] plays for a located identity.
//
// Unlike [SeedLocatedForInstance], an existing tombstone is never a
// conflict: [recordEnvelope.Tombstone] is a map precisely because an
// address can accumulate more than one destroyed occupant over an
// estate's history (day2_replace, then day2_remove), so this always adds
// rec as one more entry, keyed by its own identity (see [tombstoneKey]),
// rather than refusing when the map already holds something.
//
// A nil store or an empty rec is a no-op, matching [SeedLocatedForInstance]'s
// own "nothing configured" and "nothing to record" cases respectively,
// except that an empty rec here returns an error rather than silently
// succeeding: there is no legitimate reason to call this without an
// identity to seed.
func SeedTombstoneForInstance(ctx context.Context, store *RecordStore, addr addrs.AbsResourceInstance, rec TombstoneRecord) error {
	if store == nil {
		return nil
	}
	if rec.Empty() {
		return fmt.Errorf("refusing to seed an empty tombstone for %s", addr)
	}
	identity := &identityPayload{ImportID: rec.ImportID, Attrs: rec.Components}
	tk := tombstoneKey(identity)
	if tk == "" {
		return fmt.Errorf("the tombstone for %s carries no identity to key it by", addr)
	}
	_, version, _, err := store.getRaw(ctx, addr)
	if err != nil {
		return fmt.Errorf("reading the existing record for %s before seeding a tombstone: %w", addr, err)
	}
	if _, err := store.mergeEnvelope(ctx, addr, version, func(env *recordEnvelope) {
		if env.Tombstone == nil {
			env.Tombstone = make(map[string]*tombstoneFields, 1)
		}
		env.Tombstone[tk] = &tombstoneFields{
			Identity: identity,
			Provider: rec.Provider,
		}
	}); err != nil {
		return fmt.Errorf("writing the tombstone for %s: %w", addr, err)
	}
	return nil
}
