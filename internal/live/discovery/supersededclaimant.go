// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is [classifyOrphans]'s record-first disambiguation
// ([recordCurrentClaimant], discovery.go) carried onto the DECLARED side of
// the same wall.
//
// # The defect
//
// A day2_replace under the default destroy-then-create ordering leaves the
// destroyed object's tags visible for a time after the apply that destroyed
// it: `ec2 describe-tags` on a terminated instance still returns its
// tofu-estate/tofu-address/tofu-slot, `describe-instances` still returns it,
// and `resourcegroupstaggingapi get-resources` still lists its ARN.
// Confirmed directly against the emulator with no tofu in the loop, and it
// matches AWS's own documented behaviour for a terminated instance rather
// than being an emulator gap.
//
// So the next plan finds TWO live objects carrying one declared address's
// marker: the object the apply just created, and the shadow of the one it
// just destroyed. The tag sweep alone cannot tell them apart, and every
// declared binding path refused:
//
//   - a scalar or for_each address -> [collisionProblem]'s ProblemCollision,
//     "Two live resources claiming one address" ([bind]'s own default arm,
//     and the [declared.recordBacked] loop beside it);
//   - a count member -> the same function's IntKey branch,
//     ProblemNeedsSlotMarkers, "Indistinguishable instances without
//     per-instance markers" (bindCountByAddress's default arm) - or, when
//     the whole set carries slots, [slots.Match]'s own DuplicateError,
//     since the shadow carries the slot tag it was stamped with and
//     MARKERS.md explicitly allows a retired slot to be handed out again.
//
// Stock proceeds here (its state file names the new object outright), so
// this is HANDOFF's row 1 - choudoufu refusing where stock does not - and
// the block is total: the estate cannot be planned again until AWS forgets
// the terminated object.
//
// # Why the record settles it, and why only the record
//
// rulings/20260823-foundation-order-ruling.md item 1 makes the estate's own
// record authoritative for "which live object does this address own right
// now". It is written on every apply, for every ordinary taggable instance
// as well as for the record-backed ones (see
// [projection.WriteBack]'s writeBackRecordEnvelopes, its "---- identity ----"
// case: "Every other (ordinary taggable) instance now ALSO gets its identity
// recorded"), so day2_replace's OWN apply already overwrote this address's
// record with the new object's identity before the plan that trips over the
// shadow ever runs. That is the read half's write half, and it is the same
// store [recordCurrentClaimant] reads one file over.
//
// The discipline is [matchDeposedClaimant]'s, unchanged: disambiguate only
// when EXACTLY one claimant matches the record, and change nothing about any
// other collision shape. Zero matches (a stale or wrong record) and two or
// more matches are exactly as unresolvable as they were, and still refuse.
//
// # Why the superseded object is reported rather than dropped
//
// A claimant this pass drops is a live object carrying this estate's marker
// that nothing in the run then binds, destroys or retags. That is precisely
// [ProblemDisplacedMarker]'s own population - "a live resource carries this
// estate's marker for an address the configuration still declares, but the
// identity that address resolves to names a different live resource" - and
// it reuses that kind rather than inventing one, at the same WARNING
// severity, for the same reason displaced.go gives: no code path downstream
// of a warning-severity [Problem] reads it, so this can produce no destroy,
// no create and no adoption of its own.
//
// displaced.go cannot reach this population itself, and its own doc comment
// says why: [declared.displacedFrom] compares the identity the
// CONFIGURATION computes, which exists only for [identity.ClassConcrete] -
// "a needs-discovery address never reaches this code at all". Every
// server-assigned type is needs-discovery by construction, so the
// configuration computes nothing to compare and the record is the only
// second opinion there is. This is that comparison, sourced from the record.
//
// # Generic by construction
//
// No resource type name appears anywhere in this file. It reaches every
// declared address the estate holds a current identity record for, of every
// admitted type: the comparison is by import ID for a type identified by one
// server-minted string and by every named identity-schema component for a
// composite one, which is the property every admitted type already has one
// of.

// pruneSupersededClaimants is [bind]'s record-first pass over the declared
// addresses more than one live resource claims. For each such address whose
// current identity record matches EXACTLY one of its claimants, the others
// are not this address's live claim: they are dropped from the entry before
// any binding decision reads it, and reported as [ProblemDisplacedMarker].
//
// It runs before every other loop in [bind] on purpose. Dropping a claimant
// here - rather than at each of the four refusal sites - is what makes the
// count paths work too: [bindCountBlock] classifies the whole live set with
// [slots.Classify] before it chooses a binder, so a shadow left in the set
// changes which binder runs and can turn the shape into a duplicate-slot
// refusal that no per-site fix would ever see.
//
// Every way out is "no", deliberately, and each returns the entry untouched:
// no record store on the request, no record for the address, a read error, a
// record that matches no claimant, and a record that matches more than one.
// [collisionProblem] is the correct, safe default for all of them.
func pruneSupersededClaimants(ctx context.Context, req Request, decl *declared, res *Result) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if req.HintStore == nil {
		return diags
	}
	prefix := req.KeyPrefix
	if prefix == "" {
		prefix = projection.RecordKeyPrefix(req.Estate)
	}
	store := projection.NewRecordEnvelopeStore(req.HintStore, prefix)

	for _, typeName := range decl.bindTypeNames() {
		// decl.order is decl.types in address order; sortedRecordBackedAddrs
		// is the same for decl.recordBacked. Both are needed and neither
		// covers the other: an address is filed in exactly one of the two
		// (see [Request.RecordBackedAddrs]), and a count block's entries are
		// the SAME *declaredEntry pointers either map holds, which is why
		// mutating them here reaches bindCountBlock as well.
		for _, escaped := range decl.order[typeName] {
			diags = diags.Append(pruneSupersededEntry(ctx, store, req, res, typeName, escaped, decl.types[typeName][escaped]))
		}
		for _, escaped := range sortedRecordBackedAddrs(decl.recordBacked[typeName]) {
			diags = diags.Append(pruneSupersededEntry(ctx, store, req, res, typeName, escaped, decl.recordBacked[typeName][escaped]))
		}
	}
	return diags
}

// pruneSupersededEntry is [pruneSupersededClaimants] for one declared entry.
func pruneSupersededEntry(ctx context.Context, store *projection.RecordStore, req Request, res *Result, typeName, escaped string, entry *declaredEntry) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if entry == nil || len(entry.claimants) < 2 {
		return diags
	}
	rec, _, _, identityFound, err := store.GetIdentity(ctx, entry.res.Addr)
	if err != nil || !identityFound {
		return diags
	}

	matches, survivor := 0, -1
	for i := range entry.claimants {
		if claimantMatchesRecord(rec, entry.claimants[i]) {
			matches++
			survivor = i
		}
	}
	if matches != 1 {
		return diags
	}

	superseded := make([]claimant, 0, len(entry.claimants)-1)
	for i := range entry.claimants {
		if i != survivor {
			superseded = append(superseded, entry.claimants[i])
		}
	}
	// The scan's own claimant order is not guaranteed stable across runs, so
	// the diagnostics are ordered here rather than inherited.
	sort.Slice(superseded, func(i, j int) bool { return superseded[i].displayID() < superseded[j].displayID() })

	entry.claimants = []claimant{entry.claimants[survivor]}
	for _, c := range superseded {
		diags = diags.Append(problemDiag(res, supersededClaimantProblem(req, typeName, escaped, entry.res.Addr, rec, c)))
	}
	return diags
}

// supersededClaimantProblem is the finding [pruneSupersededEntry] produces:
// [displacedProblem]'s own kind and severity, for the case that function
// cannot reach, with the record named as the second opinion instead of the
// configuration's computed identity.
func supersededClaimantProblem(req Request, typeName, escaped string, addr addrs.AbsResourceInstance, rec projection.LocatedRecord, c claimant) Problem {
	return Problem{
		Kind:     ProblemDisplacedMarker,
		TypeName: typeName,
		Addr:     addr,
		Marker:   escaped,
		LiveIDs:  liveIDs(c.importID),
		Detail: fmt.Sprintf(
			"A live %s with identity %q carries estate %q and the address %q, but the estate's own record for %s names %s as the live resource that address owns right now, and one of the other resources carrying this marker is that resource. The ordinary cause is a replace: a destroyed object's tags stay readable for a time after the apply that destroyed it, so its marker outlives it. Nothing is proposed for this resource: it is not read, not changed and not destroyed, and it will disappear from this report on its own once the cloud stops listing it. If it is instead a second, genuinely live resource, remove its markers to disown it, or use choudoufu live-mv (or a moved block) to say which address it belongs to.",
			typeName, c.displayID(), req.Estate, escaped, addr, recordIdentityDisplay(rec)),
	}
}

// recordIdentityDisplay renders what a current identity record names, for a
// message: its import ID, or its named components in sorted order for a
// composite type. Generic by construction - the two shapes are the two every
// admitted type already has one of, and no type name appears here.
func recordIdentityDisplay(rec projection.LocatedRecord) string {
	if rec.ImportID != "" {
		return fmt.Sprintf("%q", rec.ImportID)
	}
	if len(rec.Components) == 0 {
		return "an empty identity"
	}
	names := make([]string, 0, len(rec.Components))
	for name := range rec.Components {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s=%q", name, rec.Components[name]))
	}
	return strings.Join(parts, ", ")
}

// claimantMatchesRecord reports whether a live claimant is the object rec
// names. [orphanMatchesRecord]'s own comparison, for a [claimant] rather
// than an [OwnedResource]; both go through [recordIdentityMatches], which is
// where the mark discipline lives.
func claimantMatchesRecord(rec projection.LocatedRecord, c claimant) bool {
	return recordIdentityMatches(rec.ImportID, rec.Components, c.importID, c.identity)
}

// recordIdentityMatches is the one comparison behind
// [claimantMatchesRecord], [orphanMatchesRecord], [orphanMatchesTombstone]
// and [deposedClaimantMatches]: whether a live object, identified by
// liveImportID and the identity object the provider served for it, is the
// object a record naming importID/components describes.
//
// It is by import ID for a type identified by one server-minted string, and
// by every named identity-schema component for a composite type. Generic by
// construction - no resource type name appears in it, only the property
// (identified by one string, or by several named components) every admitted
// type already has one of.
//
// The four callers are separate functions because their record types are
// four different Go types ([projection.LocatedRecord],
// [projection.TombstoneRecord], [projection.DeposedRecord]) over two live
// shapes; the comparison itself is shared so that the mark discipline below
// exists in exactly one place rather than four.
func recordIdentityMatches(importID string, components map[string]string, liveImportID string, liveIdentity cty.Value) bool {
	if importID != "" {
		return importID == liveImportID
	}
	if len(components) == 0 {
		return false
	}
	if liveIdentity == cty.NilVal || liveIdentity.IsNull() || !liveIdentity.IsKnown() || liveIdentity.IsMarked() || !liveIdentity.Type().IsObjectType() {
		return false
	}
	ty := liveIdentity.Type()
	for name, want := range components {
		if !ty.HasAttribute(name) {
			return false
		}
		v := liveIdentity.GetAttr(name)
		// v.IsMarked() before AsString(): cty panics rather than errors on a
		// marked receiver, and a sensitive input variable is the ordinary
		// way to produce one. A marked component simply does not match -
		// refused, never unmarked, since the alternative is letting a value
		// nothing here proved safe flow into an identity comparison.
		if v.IsMarked() || v.IsNull() || !v.IsKnown() || v.Type() != cty.String || v.AsString() != want {
			return false
		}
	}
	return true
}
