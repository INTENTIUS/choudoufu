// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

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
// the foundation-order ruling (#388) item 1 makes the estate's own
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
// # A deposed object is not a tag shadow
//
// The current identity record is not the whole of what the estate records
// about an address, and reading only it cost reference-ec2-vpc's day2_crash
// stage a regression the day this pass landed. GitHub issue #361's crash
// window - a create_before_destroy replace interrupted after the create
// commits and before the destroy dispatches - leaves TWO GENUINELY LIVE
// objects, and the one write-back the crashed apply did manage records both
// at once: Identity names the new object, and a Deposed entry names the old
// one ([diffDeposedForWrite]). Both carry the address's marker, so this pass
// sees the same two-claimant shape a terminated shadow produces, and the
// current record matches exactly one of them.
//
// The difference is what the OTHER object is. A terminated shadow is a dead
// object whose tags have not been swept yet: nothing can be done to it and
// nothing needs to be. A deposed object is alive, running, billed, and the
// next apply's whole job is to destroy it - which is what
// [matchDeposedClaimant] arranges, by pulling it out of the claimant set
// into a [Result.DeposedBindings] entry. Pruning it here as though it were a
// shadow ran before matchDeposedClaimant could ever see it, and the recovery
// plan then proposed nothing at all.
//
// So a claimant that matches the current record or any of the address's
// recorded deposed objects is kept. The two populations do not overlap in
// practice for the reason [diffDeposedForWrite] gives: a key present in the
// record but no longer deposed in state "is gone: destroyed by this same
// apply's own crash-window recovery ... deleted from the map", so an
// ORDINARY, uninterrupted replace leaves no deposed entry for its shadow to
// match.
//
// # Why a tombstone, and not merely a record that names someone else
//
// GitHub issue #670. Matching neither the record nor a deposed entry is not
// evidence a claimant is dead; it is only evidence that the record does not
// name it. Those are the same sentence for a terminated shadow and for a
// SECOND, GENUINELY LIVE object wearing this address's marker, and the
// second is the exact condition [collisionProblem] exists to refuse - two
// live resources claiming one address, "Indistinguishable instances without
// per-instance markers" on the count path. Pruning on "matches neither"
// turned that refusal into a warning, measured on
// corpus-ec2-instance-complete's BREAK=replace control, and the
// marker-plus-record layer as it stood could not tell the two apart at all:
// nothing in the record said the shadow was dead, only that it was not
// current.
//
// So the write side says it. [projection.supersedeIdentity] records the
// identity a replace overwrites as a tombstone - the same
// [projection.tombstoneFields] entry a destroy already wrote for an address
// that left the final state, now written for the object a replace destroyed
// at an address that stayed - and a claimant is pruned here only when it
// matches one. A claimant matching nothing is kept, and the entry refuses
// through [collisionProblem] with the error it always had.
//
// A tombstone is evidence, never permission, which is what lets it sit
// under the foundation ruling's "a record is never read as permission to
// delete". It is read at exactly one place, this one, and the only thing it
// can cause is a claimant leaving a collision set: no destroy, no create,
// no adoption, no retag. An entry that is wrong therefore costs a refusal
// this estate would otherwise have made, and can reach the live system
// through nothing.
//
// Keeping a deposed claimant is also the conservative direction on its own
// terms. It leaves the entry with two or more claimants, which is the input
// [matchDeposedClaimant] already handles under its own "exactly one match"
// discipline, and every shape that function refuses falls through to
// [collisionProblem] - a loud refusal, never a silent bind.
//
// # Generic by construction
//
// No resource type name appears anywhere in this file. It reaches every
// declared address the estate holds a current identity record for, of every
// admitted type: the comparison is by import ID for a type identified by one
// server-minted string and by every named identity-schema component for a
// composite one, which is the property every admitted type already has one
// of. The deposed leg above adds no type knowledge either: it reuses
// [recordIdentityMatches], the same comparison, over
// [projection.DeposedRecord]'s identical identity shape.

// pruneSupersededClaimants is [bind]'s record-first pass over the declared
// addresses more than one live resource claims. For each such address whose
// current identity record matches EXACTLY one of its claimants, every other
// claimant the record ALSO names as destroyed by this estate's own apply is
// not a live claim on this address: it is dropped from the entry before any
// binding decision reads it, and reported as [ProblemDisplacedMarker]. A
// claimant the record names neither way is kept and refuses (issue #670).
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
// record that matches no claimant, a record that matches more than one, and
// - since issue #670 - a claimant the record does not name as destroyed.
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

	deposed, deposedReadable := deposedCandidates(ctx, store, req, entry.res.Addr)
	if !deposedReadable {
		// The one way out this function has that is not "the record said
		// nothing useful": the deposed half of the record could not be read
		// at all, so "matches neither" is not a conclusion this pass is
		// entitled to draw about anything. Leave the entry alone and let
		// [collisionProblem] refuse, the same safe default every other exit
		// here takes.
		return diags
	}

	tombstones, tombstonesReadable := tombstoneCandidates(ctx, store, entry.res.Addr)
	if !tombstonesReadable {
		// The tombstone half is now what a prune is licensed by, so a read
		// that failed outright is the same "not entitled to a conclusion"
		// exit the deposed half takes just above.
		return diags
	}

	kept := make([]claimant, 0, len(entry.claimants))
	superseded := make([]claimant, 0, len(entry.claimants)-1)
	for i := range entry.claimants {
		// A deposed object is live and awaiting destruction, not a dead
		// tag shadow - see this file's "A deposed object is not a tag
		// shadow". Keeping it leaves the set for [matchDeposedClaimant],
		// which is the mechanism that acts on it.
		if i == survivor || claimantMatchesAnyDeposed(deposed, entry.claimants[i]) {
			kept = append(kept, entry.claimants[i])
			continue
		}
		if !claimantMatchesAnyTombstone(tombstones, entry.claimants[i]) {
			// GitHub issue #670: not the survivor, not deposed, and
			// nothing this estate's own apply is recorded as having
			// destroyed. "The record names someone else" is not evidence
			// this object is dead - see this file's "Why a tombstone, and
			// not merely a record that names someone else" - so it is kept,
			// and the entry it is kept in refuses through
			// [collisionProblem] exactly as it did before this pass
			// existed.
			kept = append(kept, entry.claimants[i])
			continue
		}
		superseded = append(superseded, entry.claimants[i])
	}
	if len(superseded) == 0 {
		// Every claimant is accounted for by the record, or none of the
		// unaccounted-for ones is provably dead. Either way the entry is not
		// touched and nothing is reported: the first is the crash window,
		// and it is the deposed machinery's; the second is a collision, and
		// it is [collisionProblem]'s.
		return diags
	}
	// The scan's own claimant order is not guaranteed stable across runs, so
	// the diagnostics are ordered here rather than inherited.
	sort.Slice(superseded, func(i, j int) bool { return superseded[i].displayID() < superseded[j].displayID() })

	entry.claimants = kept
	for _, c := range superseded {
		diags = diags.Append(problemDiag(res, supersededClaimantProblem(req, typeName, escaped, entry.res.Addr, rec, c)))
	}
	return diags
}

// deposedCandidates is every deposed object the estate records for addr,
// from both places one can be recorded: [Request.DeposedRecords], the
// snapshot [matchDeposedClaimant] itself consults (collected per
// needs-discovery address before Discover runs), and the record store this
// pass already has open, which is authoritative and covers every declared
// address rather than that one population.
//
// The union is deliberate, in both directions. Reading only the snapshot
// would let this pass drop a live deposed object at any address the caller
// did not collect - a silent loss, since the address's surviving claimant
// still binds. Reading only the store would work today but would couple this
// pass's correctness to the store read succeeding for a case
// matchDeposedClaimant can already resolve from the snapshot alone (three
// tests in deposed_test.go supply exactly that: DeposedRecords with no
// HintStore).
//
// ok is false only when the store read itself failed. An address with
// nothing recorded is an ordinary empty result, not a failure.
func deposedCandidates(ctx context.Context, store *projection.RecordStore, req Request, addr addrs.AbsResourceInstance) (recs []projection.DeposedRecord, ok bool) {
	fromStore, _, _, err := store.GetDeposed(ctx, addr)
	if err != nil {
		return nil, false
	}
	seen := make(map[string]bool, len(fromStore))
	for dk, rec := range fromStore {
		seen[dk] = true
		recs = append(recs, rec)
	}
	for dk, rec := range req.DeposedRecords[addr.String()] {
		if seen[dk] {
			continue
		}
		recs = append(recs, rec)
	}
	return recs, true
}

// tombstoneCandidates is every identity this estate's own apply is recorded
// as having destroyed at addr - [projection.RecordStore.tombstone]'s
// entries for an address that left the final state, and, since GitHub issue
// #670, [projection.supersedeIdentity]'s entry for the object a replace
// destroyed at an address that stayed.
//
// It is the store's answer alone, with no [Request] snapshot to union in:
// unlike a deposed object, which the caller may have collected before
// Discover ran, a destroyed identity exists nowhere but the record.
//
// ok is false only when the store read itself failed. An address with
// nothing recorded is an ordinary empty result, not a failure - and, with
// the prune now licensed BY an entry rather than merely unblocked by the
// absence of one, an empty result simply prunes nothing.
func tombstoneCandidates(ctx context.Context, store *projection.RecordStore, addr addrs.AbsResourceInstance) (recs []projection.TombstoneRecord, ok bool) {
	fromStore, _, _, err := store.GetTombstones(ctx, addr)
	if err != nil {
		return nil, false
	}
	for _, rec := range fromStore {
		recs = append(recs, rec)
	}
	return recs, true
}

// claimantMatchesAnyTombstone reports whether a live claimant is one of the
// destroyed objects recs names. [orphanMatchesTombstone] for a [claimant]
// rather than an [OwnedResource], over a set - and through the same
// [recordIdentityMatches] every other matcher here uses, so the mark
// discipline and the "by import ID or by every named component" genericity
// are the shared ones rather than a fifth copy.
func claimantMatchesAnyTombstone(recs []projection.TombstoneRecord, c claimant) bool {
	for _, rec := range recs {
		if recordIdentityMatches(rec.ImportID, rec.Components, c.importID, c.identity) {
			return true
		}
	}
	return false
}

// claimantMatchesAnyDeposed reports whether a live claimant is one of the
// deposed objects recs names. [deposedClaimantMatches] over a set, and
// through the same [recordIdentityMatches] every other matcher here uses -
// so the mark discipline, and the "by import ID or by every named component"
// genericity, are the shared ones rather than a second copy.
func claimantMatchesAnyDeposed(recs []projection.DeposedRecord, c claimant) bool {
	for _, rec := range recs {
		if deposedClaimantMatches(rec, c) {
			return true
		}
	}
	return false
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
			"A %s with identity %q carries estate %q and the address %q, but the estate's own record for %s names %s as the live resource that address owns right now, and records this one as destroyed by an earlier apply of this estate. The ordinary cause is a replace: a destroyed object's tags stay readable for a time after the apply that destroyed it, so its marker outlives it. Nothing is proposed for this resource: it is not read, not changed and not destroyed, and it will disappear from this report on its own once the cloud stops listing it. A second, genuinely live resource wearing this marker is a different case and is refused rather than reported here, because the record would not name it as destroyed.",
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
		if v.IsMarked() || v.IsNull() || !v.IsKnown() {
			return false
		}
		switch v.Type() {
		case cty.String:
			if v.AsString() != want {
				return false
			}
		case cty.Number:
			// Records render number components as plain decimal digits
			// (identity's renderIntegralNumber; issue #742 made records of
			// this shape real - an ECS task definition's revision), but a
			// live identity object is typed by the provider's identity
			// schema, so revision arrives here as a cty.Number. Compare
			// through the same canonical conversion the writer used, or
			// every record #742 writes is unmatchable by exactly the
			// recovery machinery it exists to feed (review finding B1 on
			// #742).
			conv, err := convert.Convert(v, cty.String)
			if err != nil || conv.AsString() != want {
				return false
			}
		default:
			return false
		}
	}
	return true
}
