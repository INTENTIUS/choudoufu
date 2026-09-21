// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"log"

	"github.com/intentius/choudoufu/internal/live/servicetags"
)

// GitHub issue #1131: the per-service tag-read leg, and the repair for
// #881.
//
// # The situation it fires in
//
// A sweep lists AWS::IAM::InstanceProfile through Cloud Control, gets the
// identifiers, and can read a marker off none of them: the CFN schema
// carries no Tags property so neither ListResources nor GetResource returns
// one, and the estate's tag index does not hold the type either. The
// profile does carry tofu-estate - internal/live/stamp wrote it and the AWS
// CLI reads it back - so a block deleted from the configuration leaves a
// live, owned object the plan proposes nothing about. #1129 made that a
// loud refusal ([SweepGapMarkerUnreadable]). This file makes it a destroy,
// by asking the service that owns the object: iam:ListInstanceProfileTags
// returns the marker.
//
// # Why this is a fourth route and not a fourth guess
//
// live/MARKERS.md's identity rule forbids inferring ownership from
// anything but a marker, and nothing here infers. The marker is read, off
// the object, through an API AWS documents for exactly that purpose; the
// only thing that changes is which call carries it. An object whose tag
// read fails keeps the refusal it already had, and an object whose tag read
// succeeds and holds no tofu-estate is an ordinary unowned object, the same
// as one whose list call said so.
//
// # The gate, and why it is per object (GitHub issue #1162)
//
// [serviceTagRead] runs for an object only when all of the following hold,
// and each one is either free to evaluate or already evaluated:
//
//  1. The run has a reader, and the reader has a route for the type
//     (servicetags.Reader.Route). Costs nothing for the 1690-odd types
//     nothing is wired for.
//  2. The object's own listing produced no tofu-estate. An object that told
//     the truth about itself needs no second opinion - the same rule
//     [markerIndex.join]'s own comment states.
//  3. The estate's tag index did not answer for THIS OBJECT: its own
//     [markerIndex.join] came back anything but joinBound. Both call sites
//     make that join first and reach this function only when the object's
//     tags still carry no tofu-estate, so the clause is the call sites'
//     `if tags[TagEstate] == ""` and needs no state of its own here.
//
// Clause 3 used to be per TYPE ([markerIndex.servesType]: the index holding
// any marked object of the type switched the leg off for every object of
// it), and that was wrong about exactly the shape #1046 measured on a real
// account - the index lagging the marker writes, holding some objects of a
// type and not others. There the unindexed object got joinNone and no read,
// so its destroy was never proposed; and on the native leg its indexed
// sibling's bound join refuted [sweepMarkerReadGap], so nothing was said
// either. The maintainer ruled the per-object gate on 2026-09-21.
//
// The decision is still evidence-scoped rather than service-scoped, which is
// what the per-type clause was for: #1134 measured GetResources serving
// iam:instance-profile and iam:policy on a real account in us-east-1 and
// iam:role nowhere, and the emulator pin of the time serving no IAM at all
// (#1152, since repinned to match), so which objects need the leg differs by
// TARGET. Asking the index about each object answers that per run, from the
// one GetResources call the run already paid for, and an object the index
// did answer for costs no call.
//
// # What it costs, said plainly
//
// One call per candidate object the index did not answer for, per sweep.
// Not one per type. #1037 and #1039 made the native sweep flat in estate
// size and live/costs/plan-cost.md publishes that flatness; this leg does
// not preserve it for the types it covers, and the honest statement of why
// is that IAM offers no batch tag read to build a flat shape out of:
// iam:ListInstanceProfiles omits tags by design ("this operation does not
// return tags, even though they are an attribute of the returned object" -
// AWS's own API reference), GetInstanceProfile and ListInstanceProfileTags
// are both per-object, and neither takes a tag filter. #1129's guard
// achieves one call per affected TYPE because it is deciding whether to
// list at all; this one has to look at each object, because tags are what
// tell the objects apart.
//
// What the per-object gate does NOT do is keep the bill off a target whose
// index serves the type. There the index answers for this estate's indexed
// objects and for nothing else, so every other listed object of a routed
// type - somebody else's roles, AWS's own service-linked ones, and this
// estate's objects the index lags on, which are indistinguishable until
// read - costs one call. The per-type clause bought flatness on those
// targets by assuming the index was complete, and #1046 measured that it is
// not. [TypeScan.ServiceTagReads] records the count, so the cost is in the
// scan row rather than in a comment.

// tagReadOutcome is what [serviceTagRead] did, for the one caller that has
// to tell "no read was attempted" from "a read was attempted and failed":
// [scanType], whose [sweepMarkerReadGap] must stay loud for an object whose
// own read was refused even when a sibling's marker was read.
type tagReadOutcome int

const (
	// tagReadNotAttempted: no reader, no route for the type, or no
	// identifier. Not a fact about the object.
	tagReadNotAttempted tagReadOutcome = iota
	// tagReadAnswered: the service returned the object's whole tag set,
	// which may be empty.
	tagReadAnswered
	// tagReadFailed: the call was made and returned an error. Nothing was
	// established about the object.
	tagReadFailed
)

// serviceTagRead is the leg. It returns the object's real tags and
// [tagReadAnswered] when the service answered, and nil in every other case.
// "There is no reader" and "no route for this type" are
// [tagReadNotAttempted]: neither is a failure and neither changes what the
// caller does next. [tagReadFailed] is a read that was made and refused,
// which the caller must not let a sibling's success hide.
//
// The caller has already established that neither the object's own listing
// nor its own tag-index join produced a tofu-estate; that is the per-object
// gate, and it is the caller's because the join is the caller's.
//
// scan may be nil for a caller with no row to charge the call to.
func serviceTagRead(ctx context.Context, req Request, typeName, importID string, scan *TypeScan) (map[string]string, tagReadOutcome) {
	return serviceTagReadWith(ctx, req.ServiceTags, typeName, importID, scan)
}

// serviceTagReadWith is the leg itself, over the one thing it actually
// needs rather than over a whole [Request]: the reader.
//
// Split out for [MarkerFallback], which is the same leg for a caller that
// runs no [Discover] pass and so has no Request to carry it - internal/live/
// mv's live-mv sweep, GitHub issue #1274. The alternative was a second
// implementation of "read the marker through the service's own tag API" in
// that package, which would have diverged from this one the first time
// either moved.
//
// reader may be nil, which means this run has no such route and is not a
// fact about the object.
func serviceTagReadWith(ctx context.Context, reader servicetags.Reader, typeName, importID string, scan *TypeScan) (map[string]string, tagReadOutcome) {
	if reader == nil || importID == "" || !reader.Route(typeName) {
		return nil, tagReadNotAttempted
	}

	tags, err := reader.ReadTags(ctx, typeName, importID)
	if scan != nil {
		scan.ServiceTagReads++
	}
	if err != nil {
		if servicetags.ErrNoRoute(err) {
			// Route said yes and ReadTags said no route: the two disagree,
			// which is a programming error in the reader rather than
			// anything about the object. Logged and treated as a failed
			// read, so the caller's existing refusal stands.
			log.Printf("[WARN] stateless/discovery: service tag read for %s %q: %s", typeName, importID, err)
			return nil, tagReadFailed
		}
		// A failed read establishes nothing. The caller keeps whatever it
		// had - for the sweep that is #1129's SweepGapMarkerUnreadable,
		// which is the honest answer when no route could read the marker.
		log.Printf("[DEBUG] stateless/discovery: service tag read for %s %q failed, leaving the marker unread: %s", typeName, importID, err)
		return nil, tagReadFailed
	}
	log.Printf("[DEBUG] stateless/discovery: %s %q carried no readable marker on any enumeration or index route; read %d tag(s) from the service's own tag API", typeName, importID, len(tags))
	return tags, tagReadAnswered
}
