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

// SummaryLocatedIdentityNotRecorded is the summary of the warning
// [liveimport]'s stamp.go raises when [SeedLocatedForInstance] cannot write
// the identity a migration just read. Named for the same reason
// [SummaryLocatedNoStore] is: internal/live/refusalscan requires every
// diagnostic this fork raises to have a registry entry, and the entry and
// the diagnostic have to name one string. Declared here rather than beside
// the call site because the call site is in a different package
// ([liveimport]) that carries no refusalscan registry of its own -
// [SummaryResidueNotClassified] is the precedent for a Summary constant one
// package owns and another package's diagnostic reuses.
const SummaryLocatedIdentityNotRecorded = "Located identity could not be recorded"

// SeedLocatedForInstance writes one instance's identity into store, from an
// identity string its caller already read rather than from a live object
// this package reads itself - [liveimport]'s stamp.go's migrate-time half of
// the fix for a type with no tags argument and no list route at all (the
// property internal/live/discovery's scanTypeLocatedFallback consumes this
// same store for), and the exact sibling of [SeedRecordForInstance]: same
// read-before-write idiom, same refusal to clobber a different existing
// value, narrower payload (an identity, not an object).
//
// # Why an existing different identity is never overwritten
//
// Exactly [SeedRecordForInstance]'s own reasoning, restated for identity
// rather than value: the located record is the only carrier this
// population's identity has, live/MARKERS.md's "a wrong marker outranks a
// missing one" applies to it exactly as it applies to a tag, and the store
// can legitimately be newer than the state file a migration was pointed at
// (an estate migrated once, replaced the object since through an ordinary
// apply, migrated again from stale state). Clobbering there would replace a
// correct identity with a stale one, and a wrong identity is invisible to
// every verdict-level check - the next plan would be clean and wrong.
//
// A byte-identical existing record is the idempotent case and reports
// [SeedUnchanged] with no error, so re-running a migration over the same
// state file stays a no-op for this too.
//
// A nil store is an immediate no-op - a configuration with no record_store
// block, where this population is left exactly where it was before this
// mechanism existed: findable only by hand.
func SeedLocatedForInstance(ctx context.Context, store *RecordStore, addr addrs.AbsResourceInstance, provider addrs.AbsProviderConfig, rec LocatedRecord) (SeedResult, error) {
	if store == nil {
		return SeedUnchanged, nil
	}
	if rec.Empty() {
		return SeedUnchanged, fmt.Errorf("refusing to record an empty identity for %s", addr)
	}

	existing, version, _, identityFound, getErr := store.GetIdentityFresh(ctx, addr)
	if getErr != nil {
		return SeedUnchanged, fmt.Errorf("reading the existing located record for %s before writing: %w", addr, getErr)
	}
	if identityFound {
		if existing.ImportID == rec.ImportID && componentsEqual(existing.Components, rec.Components) {
			return SeedUnchanged, nil
		}
		return SeedUnchanged, fmt.Errorf(
			"the record store already holds a different identity for %s (version %s), so this run left it alone: the stored value may be newer than the state file this migration was given, and this is the only carrier such an instance's identity has",
			addr, displayVersion(version))
	}

	if _, putErr := store.mergeEnvelope(ctx, addr, version, func(env *recordEnvelope) {
		env.Identity = &identityPayload{ImportID: rec.ImportID, Attrs: rec.Components}
		env.Provider = providerString(provider)
	}); putErr != nil {
		return SeedUnchanged, fmt.Errorf("writing the located record for %s: %w", addr, putErr)
	}
	return SeedWritten, nil
}

// componentsEqual compares two identity-component maps for exact equality,
// including "both nil" and "both empty" comparing equal - the shapes
// [LocatedRecord.ImportID]-only records take on both sides of the comparison
// above.
func componentsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
