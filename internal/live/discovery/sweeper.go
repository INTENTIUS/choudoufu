// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"

	"github.com/intentius/choudoufu/internal/live/listclient"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// The estate sweep behind an interface (GitHub issue #1580, under #1579).
//
// The marker question has been behind [substrate.Substrate] since #1572;
// the sweep was not. [Request] carried the Kubernetes client and its type
// universe as named fields, the AWS legs ran inline in [Discover], and the
// command chose between them by provider name. A third family's sweep
// (Cloud Asset Inventory, Resource Graph) had nowhere to plug in but a
// third hand-wired path. Now each leg is a [Sweeper] that speaks the
// vocabulary every leg already wrote - [TypeScan], [SweepGap], orphans
// into [Result] - and the caller hands [Discover] a list of them, chosen
// by the substrate's [substrate.Sweep] property.
//
// It is an extraction. Each leg's body is the code that ran in its place
// before, in the same order: the AWS legs where the "if req.Sweep" block
// stood, the Kubernetes leg right after, both ahead of bind.

// Sweeper is one estate-sweep leg. Leg is the [substrate.Sweep] it
// serves; Sweep lists what the leg can see and files it into
// in.Result as the other legs do: a [TypeScan] per type it covered, a
// [SweepGap] per type it could not, an orphan per marked object nothing
// declares. The diagnostics are the leg's own.
type Sweeper interface {
	Leg() substrate.Sweep
	Sweep(ctx context.Context, in *SweepInput) tfdiags.Diagnostics
}

// SweepInput is what a leg is handed: the pass's request and the result
// every leg writes into, plus the pass's own working state the AWS legs
// share with the config-driven scan (the provider's list schemas, the
// declared-instance index and the progress counters).
type SweepInput struct {
	Request Request
	Result  *Result

	schemas        listclient.Schemas
	decl           *declared
	typesScanned   *int
	resourcesFound *int
}

// sweepLegs is the pass's legs: [Request.Sweepers], or the one leg every
// caller before that field existed ran when it is nil. An empty, non-nil
// list is a caller saying no leg sweeps through this provider (GitHub
// issue #1707: a provider no family claims whose schema has nothing a
// marker is written onto), and runs none.
func sweepLegs(req Request) []Sweeper {
	if req.Sweepers != nil {
		return req.Sweepers
	}
	return []Sweeper{TaggingIndexSweep{}}
}

// SweepGapNoSweepLeg is a provider family whose sweep no leg serves
// ([NoSweepLeg]): nothing it holds was listed, so nothing of it can be
// proposed for removal.
const SweepGapNoSweepLeg SweepGapReason = "NO_SWEEP_LEG"

// NoSweepLeg stands in for a family whose [substrate.Sweep] no leg serves,
// or for a provider no family claims at all (GitHub issue #1707). It
// lists nothing and files one [SweepGapNoSweepLeg] gap naming the family
// and the sweep it asked for, or the unclaimed provider, with the
// incomplete-sweep warning, so a missing leg reads as a named gap in
// coverage and never as an estate with nothing to remove.
type NoSweepLeg struct {
	// Family is the provider family's name ([substrate.Substrate.Name]),
	// or, when Kind is empty, the provider no family claims
	// (its addrs.Provider.ForDisplay).
	Family string
	// Kind is the sweep the family asked for, empty for a provider no
	// family claims.
	Kind substrate.Sweep
}

// Leg is the sweep the family asked for.
func (n NoSweepLeg) Leg() substrate.Sweep { return n.Kind }

// Sweep files the gap.
func (n NoSweepLeg) Sweep(_ context.Context, in *SweepInput) tfdiags.Diagnostics {
	detail := fmt.Sprintf("No sweep leg serves provider family %s's %q sweep, so nothing estate %q owns through it was listed this run and a resource whose block was deleted is not proposed for removal.",
		n.Family, string(n.Kind), in.Request.Estate)
	if n.Kind == "" {
		detail = fmt.Sprintf("No provider family claims provider %s, so no sweep leg lists what estate %q marks through it: a resource of one of its marker-carrying types whose block was deleted is not proposed for removal this run. Resources the estate's record store holds are still found.",
			n.Family, in.Request.Estate)
	}
	return sweepGapDiag(in.Result, SweepGap{
		TypeName: n.Family,
		Reason:   SweepGapNoSweepLeg,
		Detail:   detail,
	})
}

// TaggingIndexSweep is the AWS legs ([substrate.SweepTaggingIndex]): the
// Resource Groups Tagging API index when the request carries it
// ([Request.TaggingSweep], issue #51) with the per-type list loop for
// what it carves out, or the per-type loop over [sweepTypes] alone, both
// through the configured provider. It does nothing unless
// [Request.Sweep] is set.
type TaggingIndexSweep struct{}

// Leg is [substrate.SweepTaggingIndex].
func (TaggingIndexSweep) Leg() substrate.Sweep { return substrate.SweepTaggingIndex }

// Sweep runs the legs.
func (TaggingIndexSweep) Sweep(ctx context.Context, in *SweepInput) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	req, res, schemas, decl := in.Request, in.Result, in.schemas, in.decl
	if !req.Sweep {
		return diags
	}
	if req.TaggingSweep && req.Tagging != nil && req.Roster != nil {
		// Issue #51: one estate-wide GetResources call replaces the
		// per-type loop below, for every type [partitionSweepTypes]
		// doesn't carve out. See [sweepViaTagging] and
		// [Request.TaggingSweep]. It is one round trip rather than one
		// per type, so it gets no progress events of its own - there is
		// nothing to report between, only before and after.
		taggingUniverse, nativeUniverse := partitionSweepTypes(req, schemas, decl)
		diags = diags.Append(sweepViaTagging(ctx, req, schemas, decl, res, taggingUniverse))
		// the stale-state ruling's (#604) CollectUnclaimed
		// ruling. The tagging leg above is untouched by it - it is one
		// call and it covers every ARN-placeable type across the whole
		// account - and so is every removal leg below. What narrows is
		// the per-type list loop, which is the term that tracks the
		// admission table rather than the estate. See nativesweep.go
		// for what that gives up and why it fails toward sweeping.
		nativeUniverse, res.NativeSweepSkipped = estateScopedNativeSweep(ctx, req, decl, nativeUniverse)
		// GitHub issue #1037/#1039: a type sweepTypes() adds back purely
		// for being a taggingAPIUnservedType (today, every aws_iam_*
		// type) was ALSO scanned a moment ago by the config-driven loop
		// above whenever the configuration declares a needs-discovery
		// instance of it - decl.types[typeName] != nil is exactly that
		// condition (declared.typeNames(), the loop's own universe). That
		// scan already ran with scan.Scope = ScopeAll (supportsTagFilter
		// is false for these types regardless of sweep=true/false, so the
		// two calls would build the identical list configuration) and
		// already appends every one of this estate's own markers found on
		// an undeclared address to res.Orphans - res.Orphans is filled
		// without a `sweep` gate anywhere above line ~2420 - so listing
		// the same type again here would refetch the whole account
		// (paying its per-object provider Read a second time, in
		// aws_iam_policy's case a GetPolicyVersion per policy on top of
		// the config-driven pass's own) and then discard every result:
		// orphanAlreadyPresent's dedup guard rejects a repeat orphan
		// and decl.entryFor/decl.declares handle a repeat claimant the
		// same way. Removed from nativeUniverse before the prefetch
		// plans anything, not skipped in the consuming loop below, so
		// [sweepPrefetch.finish] never reports a wasted plan for it.
		nativeUniverse = dedupAlreadyConfigScanned(nativeUniverse, decl, res)
		// GitHub issue #605: the list calls this loop is about to make
		// go out concurrently, up to [Request.SweepParallelism] at a
		// time, and the loop below is unchanged - it consumes each
		// type's answer in this same order, from the same scanType
		// body, so every diagnostic, scan row, claim and gap is produced
		// by exactly the code that produced it sequentially. See
		// sweepconcurrency.go.
		req.sweepFetch = startSweepPrefetch(ctx, req, schemas, decl, nativeUniverse, func(typeName string) bool {
			return req.CollectUnclaimed && decl.recordBacked[typeName] != nil
		})
		// Issue #394: a companion pair whose identities diverge
		// ([typeNeedsResourceObjectToRecompose]) can only ever bind
		// through a native list call's own resource object, which the
		// tag sweep's ARN-joined candidate never carries - so these few
		// types still go through the per-type loop even though
		// TaggingSweep is set.
		for _, typeName := range nativeUniverse {
			// GitHub issue #388 edge 3's foreign-coverage fix: a type
			// [partitionSweepTypes] routed here purely because it is
			// entirely record-backed (see that function's own doc
			// comment) still owes this run its unclaimed population
			// when the caller asked for one - sweepViaTagging's single
			// GetResources call is server-side estate-filtered and
			// structurally could never have seen an unmarked sibling,
			// which is exactly why partitionSweepTypes sends it here
			// instead. Every other type in nativeUniverse is a true
			// [typeNeedsResourceObjectToRecompose] companion the
			// configuration may not even declare, for which
			// "unclaimed" keeps its original, narrower meaning (see
			// TypeScan.Sweep's own doc comment).
			collectUnclaimed := req.CollectUnclaimed && decl.recordBacked[typeName] != nil
			diags = diags.Append(scanTypeReporting(ctx, req, schemas, decl, typeName, res, true, collectUnclaimed, in.typesScanned, in.resourcesFound))
		}
		res.sweepPrefetchWasted = append(res.sweepPrefetchWasted, req.sweepFetch.finish()...)
		res.sweepPrefetchUnplanned = append(res.sweepPrefetchUnplanned, req.sweepFetch.unplannedCalls()...)
		res.sweepPrefetchMismatched += req.sweepFetch.mismatches()
		req.sweepFetch = nil
	} else {
		// #64's guided leg: guidedSweepUniverse returns sweepTypes(req,
		// decl) unmodified (and an empty fallback reason) whenever
		// Request.Guided is false, so this is a no-op for every
		// existing caller. See the Request.Guided doc comment and
		// guided.go for what changes when it is set.
		universe, skipped, fallback := guidedSweepUniverse(ctx, req, decl)
		res.Guided = req.Guided && fallback == ""
		res.GuidedFallback = fallback
		res.GuidedSweepSkipped = skipped
		// Issue #605's other leg, the same shape as the one above.
		req.sweepFetch = startSweepPrefetch(ctx, req, schemas, decl, universe, func(typeName string) bool {
			return req.CollectUnclaimed && decl.recordBacked[typeName] != nil
		})
		for _, typeName := range universe {
			// Same reasoning as the TaggingSweep leg just above: a type
			// present here only because every one of its declared
			// instances is record-backed still needs its unclaimed
			// population collected when the caller asked for one.
			collectUnclaimed := req.CollectUnclaimed && decl.recordBacked[typeName] != nil
			diags = diags.Append(scanTypeReporting(ctx, req, schemas, decl, typeName, res, true, collectUnclaimed, in.typesScanned, in.resourcesFound))
		}
		res.sweepPrefetchWasted = append(res.sweepPrefetchWasted, req.sweepFetch.finish()...)
		res.sweepPrefetchUnplanned = append(res.sweepPrefetchUnplanned, req.sweepFetch.unplannedCalls()...)
		res.sweepPrefetchMismatched += req.sweepFetch.mismatches()
		req.sweepFetch = nil
	}
	return diags
}
