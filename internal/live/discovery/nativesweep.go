// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// The estate-wide sweep's native per-type leg has two jobs, and only one of
// them is a correctness requirement.
//
// The first is REMOVAL DETECTION: find a live object this estate owns whose
// resource block the configuration no longer declares, so the plan can
// propose destroying it. That question is bounded by the estate. The tagging
// leg answers it for every type [arnJoinReaches] can place, in one
// estate-filtered GetResources call; [recordOrphanReadSweep] answers it for
// the untaggable types out of the estate's own record store; and this leg
// answers it for the rest - a type that is taggable but has no arnJoinTable
// row, which is #394's aws_db_instance shape.
//
// The second is the ACCOUNT INVENTORY: "what is in my account that I do not
// know about". That one is bounded by the ACCOUNT, not the estate, and it is
// what [Request.CollectUnclaimed] asks for. Answering it means listing every
// admitted type the ARN join cannot place - 992 types at provider 6.59.0, of
// which 435 cost a real ListResources against the pinned emulator - whether
// or not this estate has ever touched one.
//
// Measured on a migrated 79-instance terralith against floci
// sha256:c55d74e1, that second job is nearly the whole difference between
// this fork and stock: 710 API calls against stock's 150, with the native
// leg's account-wide enumeration accounting for 543 of the 560-call gap.
// rulings/20260830-stale-state-charter.md rules that it does not stay
// unconditional, and this file is where that ruling takes effect: with
// [Request.CollectUnclaimed] unset, the native leg is narrowed to the types
// this estate has its own evidence of, and the rest of the admission table
// is not listed.
//
// # What narrowing gives up, exactly
//
// A live object carrying this estate's marker, of a type that (a) the
// configuration does not declare, (b) the estate's record store has no entry
// for, and (c) arnJoinTable cannot place from an ARN. Such an object's
// destroy is not proposed on a narrowed plan. Every other removal is
// unaffected, which is what [TestNarrowedNativeSweepStillProposesRemovals]
// and the day2_remove gauntlet stages check by value rather than by
// argument.
//
// # Why this fails toward doing the work
//
// The charter's rule 2 - "where the tool cannot establish that a hook is
// unnecessary, it runs the hook" - is the whole shape of
// [estateScopedNativeSweep]'s early returns. Narrowing happens only when
// there is a positive evidence source to narrow BY: a record store that
// opened and that holds at least one key. No record store, a store that
// will not list, or an empty one all take the full universe, because an
// estate with no record of itself is exactly the estate whose markers are
// the only thing that knows what it owns.
//
// It is also deliberately confined to the tagging-sweep leg. The other leg
// ([Request.TaggingSweep] unset, which TOFU_LIVE_CLOUDCONTROL=off selects)
// has no cheap estate-wide oracle at all: there is no GetResources call
// covering the types this narrowing would skip, so skipping them there would
// remove coverage with nothing standing behind it.
package discovery

import (
	"context"

	"github.com/intentius/choudoufu/internal/live/projection"
)

// estateScopedNativeSweep narrows native - [partitionSweepTypes]' per-type
// leg - to the types this estate has its own evidence of, and reports how
// many it dropped. A zero second return means nothing was narrowed and the
// caller is sweeping exactly what it always did.
//
// Evidence is deliberately generous, because a false positive here costs one
// list call and a false negative costs a removal nobody proposes: every type
// the configuration declares an instance of, every type the declared set
// routed through discovery or through the record rung, and every type the
// estate's record store holds a key for.
func estateScopedNativeSweep(ctx context.Context, req Request, decl *declared, native []string) (kept []string, skipped int) {
	if req.CollectUnclaimed {
		// The account-inventory question was asked. Answering it is the
		// whole reason the full universe exists.
		return native, 0
	}
	if req.HintStore == nil {
		// No record store opened for this pass, so there is no evidence
		// source to narrow by and no basis for calling any type absent.
		return native, 0
	}

	prefix := req.KeyPrefix
	if prefix == "" {
		prefix = projection.RecordKeyPrefix(req.Estate)
	}
	keys, err := projection.NewRecordEnvelopeStore(req.HintStore, prefix).List(ctx)
	if err != nil {
		// The store is there and would not answer. That is precisely the
		// "cannot establish that the hook is unnecessary" case.
		return native, 0
	}
	if len(keys) == 0 {
		// An estate with no record of itself. Its markers are the only
		// thing that knows what it owns, so sweep for them in full - this
		// is also the rebuild-from-markers path the charter's rule 3 keeps
		// exercised.
		return native, 0
	}

	evidence := make(map[string]bool, len(req.Resolutions))
	for _, r := range req.Resolutions {
		evidence[r.Addr.Resource.Resource.Type] = true
	}
	for typeName := range decl.types {
		evidence[typeName] = true
	}
	for typeName := range decl.recordBacked {
		evidence[typeName] = true
	}
	for _, key := range keys {
		if addr, ok := projection.RecordAddr(prefix, key); ok {
			evidence[addr.Resource.Resource.Type] = true
		}
	}

	kept = make([]string, 0, len(native))
	for _, typeName := range native {
		if evidence[typeName] {
			kept = append(kept, typeName)
		}
	}
	return kept, len(native) - len(kept)
}
