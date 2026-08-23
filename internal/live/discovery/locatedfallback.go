// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
	"log"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is [scanTypeMarkerFallback]'s companion for a type with no tags
// argument at all.
//
// # Status, 2026-08-22: about to become dead code for one type, on purpose
//
// A second, independent unit (branch gauntlet/autoscaling-markeronly,
// corpus-autoscaling-complete) found the identical shape from the identity
// layer instead of the discovery layer: identity.RecordFallbackType and
// resolver.recordFallback (internal/live/identity/located.go, resolve.go)
// reclassify an instance to ClassRecordLocated - GitHub issue #270's class -
// the moment its own component resolution concludes it needs discovery and
// its type is untaggable, BEFORE resolution ever produces ClassNeedsDiscovery
// and therefore before this package ever sees it. That gate is broader than
// this file's (untaggable is enough; it does not also require no list route),
// so wherever it applies it applies EARLIER than this file could ever run.
//
// For aws_launch_configuration specifically - the one type in today's table
// where both populations actually overlap (aws_autoscaling_group, the other
// type identity.RecordFallbackType names, has a native list route and never
// reaches this file at all) - this function becomes unreachable dead code
// the moment that branch merges: decl.types["aws_launch_configuration"]
// will never be populated for scanType to iterate, because the resolution
// resolves to ClassRecordLocated, not ClassNeedsDiscovery, before scanType's caller
// ever builds decl. It is being kept in this branch anyway, on the
// maintainer's explicit instruction, because it is the only thing making
// this estate's plan clean UNTIL that other branch lands - deleting it here
// would be deleting working code in anticipation of a merge that has not
// happened. What is NOT redundant and must not be removed alongside it:
// internal/live/projection/locatedseed.go and
// internal/live/liveimport/stamp.go's recordLocatedFor, the migrate-time
// WRITE into the exact same LocatedStore/LocatedKey the identity-layer
// mechanism reads from - the other branch has no write mechanism of its own
// anywhere (neither at migrate time nor in projection's writeBackLocated),
// so without this file's sibling write, its own fix would make a migrated
// estate propose CREATING a launch configuration or ASG that already
// exists. See this repository's own session notes (2026-08-22) for the
// full analysis; the short version is in this paragraph so a future cleanup
// does not have to re-derive it.
//
// The follow-up, once both branches are in: delete this file and its call
// site in scanType (the two lines wiring scanTypeLocatedFallback into the
// "!sweep && !typeTaggable" branch), plus the now-unused
// ProblemLocatedRecordUnreadable / SourceRecordStore additions this file
// alone motivated, with a test proving nothing about corpus-eks-basic's or
// corpus-autoscaling-complete's recorded verdicts moved as a result -
// identity.RecordFallbackType's own path should be carrying the exact same
// plan clean by then.
//
// # The shape
//
// [scanType]'s declared-instance branch reaches here only after every
// enumeration route has already failed: no native provider list resource,
// no content-match candidate (#272), no working Cloud Control fallback
// (#47), and - [scanTypeMarkerFallback], immediately above this function's
// call site - no tag to index, because [typeTaggable] gates that fallback to
// types that could carry one. A type with no tags argument reaches this
// function instead: it is a real, admitted, ServerAssigned-shaped row (it
// has a ratified [identity.LookupType] entry, or nothing here reached at all
// - see resolve.go, needs-discovery is what puts a declared instance on
// scanType's table in the first place), its declared instances are
// genuinely waiting to be found, and there is no marker anywhere that could
// ever name them.
//
// Untaggable does not mean unidentifiable. See
// .claude/agents/live-markers.md's invariant, and live/MARKERS.md: a marker
// answers "may I delete this", an identity answers "which object is this",
// and a type can lack the first while still having a knowable second. For
// this population the second is knowable exactly once: a migration
// ([liveimport]'s stamp.go, Approve) already reads such an object's state,
// for residue (#341) - the very thing this file's own [identity.LookupType]
// row calls the type's identity attribute is sitting right there in that
// read - and now also writes it into the estate's record store, at
// [projection.LocatedKey]. This function is the read side of that write.
//
// # Why this reuses [projection.LocatedStore] rather than a fifth namespace
//
// GitHub issue #270 already solved an adjacent problem with exactly this
// shape: "the object exists in the cloud; what it has nowhere to carry is a
// marker" (see internal/live/projection/located.go's own file comment). Its
// population is disjoint from this one - [identity.LocatedType]'s condition
// 1 explicitly excludes any type with a ratified row, because that
// population already has an ordinary admission path (this one) and #270's
// mechanism is reserved for [identity.MarkerlessTypes] that have none. That
// exclusion is an IDENTITY-CLASSIFICATION decision (which [identity.Class] a
// resolution gets) and this file makes no such decision - aws_launch_
// configuration and its siblings keep whatever class resolve.go already
// gives them. What is reused here is one layer down: [projection.LocatedStore]
// itself is a type-agnostic, per-instance, point-lookup identity carrier
// with no enumeration - built for a different POPULATION but not tied to it
// by anything the store enforces. Building a second store with the same
// shape and a different namespace root would be the untaggable-type
// equivalent of the "aws_launch_configuration named in control flow"
// mistake this fork's derivation guard exists to catch: the property is "an
// object exists with nowhere to carry a marker", and one mechanism already
// answers it.
//
// # The population this reaches
//
// "No tags argument in the provider's schema, and no list route this
// package's other three enumeration sources can use" - computed once,
// generically, from live/survey-full.json's taggable and list_resource
// signals joined against [identity.DefaultTable], live/mapping.json and the
// registry roster, with no type name anywhere in the join. It measures 215
// of today's admitted AWS types. aws_launch_configuration (a name-prefixed
// worker launch configuration, whose own name AWS fills in) is the instance
// that found this wall; the property it shares with the other 214 is what
// this function fixes.
//
// # Never a guess
//
// ok is false only when there is nothing to consult at all: [Request.HintStore]
// is nil, meaning no record store was ever opened for this pass (no live
// block, or one whose store could not be built). The caller's existing
// refusal ([ProblemTypeNotListable]) is the honest answer there, exactly as
// it was before this function existed.
//
// ok is true once the store has been asked, whatever it answered. A
// declared instance with no located record is left unbound, which proposes
// a create - the same answer [scanTypeMarkerFallback] gives an instance the
// tag index has nothing for, and the right one: nothing here invented an
// identity that was not already written down. The only way an instance of
// this population is ever bound is a migration having read it once and
// recorded what it read, or a later choudoufu apply doing the same (the
// write side, [liveimport]'s stamp.go).
func scanTypeLocatedFallback(ctx context.Context, req Request, decl *declared, typeName string, res *Result) (tfdiags.Diagnostics, bool) {
	var diags tfdiags.Diagnostics

	if req.HintStore == nil {
		return diags, false
	}
	prefix := req.KeyPrefix
	if prefix == "" {
		prefix = projection.RecordKeyPrefix(req.Estate)
	}
	located := projection.NewRecordEnvelopeStore(req.HintStore, prefix)

	ti, tableOK := identity.LookupType(typeName)
	identityAttr := "id"
	if tableOK && len(ti.IdentityAttrs) > 0 {
		identityAttr = ti.IdentityAttrs[0]
	}

	found := 0
	for _, escaped := range decl.order[typeName] {
		entry := decl.types[typeName][escaped]
		if len(entry.claimants) > 0 {
			// Already claimed by something else that ran first for this
			// type - unreachable today (this is the last enumeration
			// route tried), kept as a guard rather than an assumption.
			continue
		}

		rec, _, _, exists, err := located.GetIdentity(ctx, entry.res.Addr)
		if err != nil {
			diags = diags.Append(problemDiag(res, Problem{
				Kind:     ProblemLocatedRecordUnreadable,
				TypeName: typeName,
				Addr:     entry.res.Addr,
				Detail: fmt.Sprintf(
					"%s has no tags argument and no list route of any kind, so the estate's record store is the only way to find %s again, and reading its stored identity failed: %s.",
					typeName, entry.res.Addr, err),
			}))
			continue
		}
		if !exists {
			// No record yet: never created, or a record genuinely lost.
			// Both want the same answer - unbound, so the plan proposes a
			// create - and [scanTypeMarkerFallback]'s doc comment makes the
			// identical choice for the tag-index case.
			continue
		}
		if rec.ImportID == "" {
			// A composite identity ([projection.LocatedRecord.Components]),
			// which nothing in [identity.DefaultTable]'s ServerAssigned
			// population needs today - every row this function is reached
			// for composes its identity from one leading attribute (see
			// [importIdentityFromResource]'s own doc comment, "only the
			// LEADING identity attribute is tried"). Refusing to flatten an
			// unexpected composite record into a guess, rather than reading
			// one component arbitrarily, is the same discipline as an empty
			// tag-index answer: left unbound, not misbound.
			continue
		}

		entry.claimants = append(entry.claimants, claimant{
			importID:     rec.ImportID,
			identityAttr: identityAttr,
			identity:     cty.NilVal,
			escaped:      escaped,
			displayName:  rec.ImportID,
		})
		found++
	}

	log.Printf("[DEBUG] stateless/discovery: %s has no tags argument and no list route; the estate's record store had an identity for %d of %d declared instance(s)", typeName, found, len(decl.types[typeName]))

	res.Scans = append(res.Scans, TypeScan{
		TypeName:  typeName,
		Declared:  len(decl.types[typeName]),
		Source:    SourceRecordStore,
		Filtering: FilterServerSide,
		Scope:     ScopeEstate,
		Listed:    found,
	})

	return diags, true
}
