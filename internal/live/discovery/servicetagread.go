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
// # The gate, and why every clause of it is load-bearing
//
// [serviceTagRead] runs only when all of the following hold, and each one
// is either free to evaluate or already evaluated:
//
//  1. The run has a reader, and the reader has a route for the type
//     (servicetags.Reader.Route). Costs nothing for the 1690-odd types
//     nothing is wired for.
//  2. The object's own listing produced no tofu-estate. An object that told
//     the truth about itself needs no second opinion - the same rule
//     [markerIndex.join]'s own comment states.
//  3. The estate's tag index does not serve the type on this target
//     ([markerIndex.servesType]). This is the clause that makes the leg
//     evidence-scoped rather than service-scoped: #1134 measured
//     GetResources serving iam:instance-profile on a real account in
//     us-east-1 and serving no IAM at all on the pinned emulator, so the
//     correct answer to "does this type need the leg" differs by TARGET,
//     not by service name. Asking the index what it holds answers it per
//     run, per type, from evidence this run already paid for.
//
// # What it costs, said plainly
//
// One call per candidate object of a covered type, per sweep. Not one per
// type. #1037 and #1039 made the native sweep flat in estate size and
// site/content/docs/model/plan-cost.md publishes that flatness; this leg
// does not preserve it for the types it covers, and the honest statement of
// why is that IAM offers no batch tag read to build a flat shape out of:
// iam:ListInstanceProfiles omits tags by design ("this operation does not
// return tags, even though they are an attribute of the returned object" -
// AWS's own API reference), GetInstanceProfile and ListInstanceProfileTags
// are both per-object, and neither takes a tag filter. #1129's guard
// achieves one call per affected TYPE because it is deciding whether to
// list at all; this one has to look at each object, because tags are what
// tell the objects apart.
//
// Clause 3 is what keeps the bill off the runs that do not need it. On a
// real account where GetResources indexes the type, the index answers, the
// leg never runs, and the sweep is flat exactly as published. On the pinned
// emulator - and on a real account for a type GetResources genuinely never
// indexes - it runs, and [TypeScan.ServiceTagReads] records how many times,
// so the cost is in the scan row rather than in a comment.

// serviceTagRead is the leg. It returns the object's real tags and true
// when the service answered, and nil/false in every other case - including
// "there is no reader", "no route for this type" and "the index already
// serves this type", none of which are failures and none of which change
// what the caller does next.
//
// scan may be nil for a caller with no row to charge the call to.
func serviceTagRead(ctx context.Context, req Request, typeName, importID string, scan *TypeScan) (map[string]string, bool) {
	if req.ServiceTags == nil || importID == "" || !req.ServiceTags.Route(typeName) {
		return nil, false
	}
	if req.markers.servesType(ctx, typeName) {
		// The index holds this type for this estate, so its silence about
		// this particular object is an answer about the object rather than
		// the absence of one - [markerIndex.join] already made it, as
		// joinNone. Reading the service would cost a call per object to
		// re-derive what the index said.
		return nil, false
	}

	tags, err := req.ServiceTags.ReadTags(ctx, typeName, importID)
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
			return nil, false
		}
		// A failed read establishes nothing. The caller keeps whatever it
		// had - for the sweep that is #1129's SweepGapMarkerUnreadable,
		// which is the honest answer when no route could read the marker.
		log.Printf("[DEBUG] stateless/discovery: service tag read for %s %q failed, leaving the marker unread: %s", typeName, importID, err)
		return nil, false
	}
	log.Printf("[DEBUG] stateless/discovery: %s %q carried no readable marker on any enumeration or index route; read %d tag(s) from the service's own tag API", typeName, importID, len(tags))
	return tags, true
}
