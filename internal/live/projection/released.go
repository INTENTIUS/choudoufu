// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"log"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/providers"
)

// releasedByOwnDestroy is the third GCP gauntlet run's day2_count wall on
// corpus-k8s-infra-gcp-gcve. A declared instance of a type with no marker
// surface, reached with no current record, whose object is live at the
// identity its configuration computes, used to be admitted by
// [builder.checkOwnershipAt]'s surface == "" case and planned as "No
// changes". When this address's own record carries a tombstone for that
// same identity, this estate's last act at the address was destroying that
// object: the destroy left it in place (google_project_service with
// disable_on_destroy unset drops the resource and leaves the service
// enabled), and stock's state forgot it. Stock then plans a create for the
// re-declared instance, and so does this: the object is not this estate's,
// because this estate released it.
//
// This only ever withdraws an admission. A tombstone is evidence, never
// permission ([tombstoneFields]); here it is evidence that the estate gave
// the object up, which is the safe direction. An instance with no record
// and no tombstone at all is untouched: the declaration-carried tier
// recovers with no record (live/RECOVERY.md), and changing that is a
// separate decision.
//
// The match is by the identity the object would be recorded as now, through
// the same [LocatedRecordFrom] every writer of a record and so of a
// tombstone used, with the configuration's import ID as a second key for a
// type that rule cannot render.
func (b *builder) releasedByOwnDestroy(ctx context.Context, w wanted, typeName, importID string, schema providers.Schema, obj cty.Value, declared bool) bool {
	own := b.opts.Ownership
	switch {
	case own == nil, b.opts.RecordStore == nil, !declared, w.located, w.recordFirst:
		return false
	case own.verified(w.addr), b.envelopeAdmitted[w.addr.String()]:
		return false
	}
	if surface, _ := substrate.SurfaceOf(b.providerType(w.addr, typeName), schema.Block); surface != "" {
		return false
	}
	tombs, _, _, err := b.opts.RecordStore.GetTombstones(ctx, w.addr)
	if err != nil || len(tombs) == 0 {
		if err != nil {
			log.Printf("[WARN] projection: reading %s's tombstones failed, so it is admitted as before: %s", w.addr, err)
		}
		return false
	}
	match := false
	if rec, ok := LocatedRecordFrom(typeName, schema, obj); ok {
		if tk := tombstoneKey(identityPayloadFrom(rec)); tk != "" {
			_, match = tombs[tk]
		}
	}
	if !match && importID != "" {
		for _, t := range tombs {
			if t.ImportID == importID {
				match = true
				break
			}
		}
	}
	if !match {
		return false
	}
	b.releasedList = append(b.releasedList, w.addr)
	b.omit(w.addr, ReasonAbsent,
		fmt.Sprintf(
			"A live %s exists with identity %q, but this estate's own apply destroyed the object at %s and its record says so, and a %s carries no ownership marker that could say it was taken back since. The destroy left the object in place, so this estate does not own it, and the plan proposes creating the resource this configuration declares, as stock does once its state has forgotten the object.",
			typeName, importID, w.addr, typeName,
		),
		fmt.Sprintf("this estate destroyed the %s at %q and has not created one since.", typeName, importID),
	)
	return true
}
