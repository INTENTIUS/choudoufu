// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"log"

	"github.com/intentius/choudoufu/internal/live/listclient"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #1321: the served-but-empty tag index, and the route the run
// already had and did not take.
//
// # The situation
//
// [taggingAPITypeCoverage] records that GetResources holds
// aws_iam_policy and aws_iam_instance_profile in us-east-1 and nowhere else
// (#1134: 500 of each returned there at scale 50, 0 in us-east-2, on two
// estates). So in us-east-1 [taggingAPIUnservedTypeInRegion] is false for
// them, [arnJoinReaches] sends them to the tagging leg, and that leg's one
// estate-wide GetResources call is the ONLY enumeration they get all run.
//
// When that call answers holding none of this estate's objects, the run
// cannot tell "this estate owns none" from #1046's lag - the Resource
// Groups Tagging API holding 104 of 1,655 stamped objects about twenty-one
// minutes after migrate had verified every one of them on a real account.
// Issue #1318 made the run say so ([SweepGapTagIndexHeldNothing]) instead of
// filing the suppressed [SweepGapNotTaggable], which is the honest statement
// of a run that established nothing. It is still not an answer.
//
// # Why this is a routing fallback and not a new leg
//
// The identical fixture in any other region already produces the destroy.
// There the coverage row says the index does not serve the type,
// [arnJoinReaches] routes it to the native per-type leg,
// [scanTypeCloudControl] enumerates it, and #1131's per-service tag read
// (servicetagread.go) supplies the marker Cloud Control's schema cannot
// carry - TestPerRegionTaggingRoutingAgainstFloci's shipped-west arm and
// servicetagread_test.go's own fixtures, which set no Region and so take
// exactly that path.
//
// Nothing is missing from the service-tag-read leg for the us-east-1 run
// either. Its third gate clause is per object since #1162 - the index did
// not answer for THIS object - and on a lagged run that clause PASSES for
// every object the index does not hold, so the leg is not gated off.
// What the run lacks is an ENUMERATION - nothing lists the type, because the
// type went to the tagging leg instead of to [scanTypeCloudControl], and the
// leg reads a marker off an object it is handed rather than finding objects.
// Supplying the enumeration is the whole repair.
//
// # What it costs, and why it is bounded
//
// One Cloud Control listing plus one [serviceTagRead] per listed object, for
// a type whose index answered. That is not flat in estate size, which is
// what #1037/#1039 established for the sweep and site/content/docs/model/
// plan-cost.md publishes, so it is worth stating what bounds it:
//
//   - The caller is [sweepViaTagging]'s registry-untaggable arm, and only
//     the branch [tagIndexHeldNothingGap] claims. That needs a
//     [taggingAPIRestrictedType] with a taggable provider schema, read from
//     a region its own coverage row says serves it. Measured over
//     identity.AdmittedTypes() at provider 6.59.0 that is two types,
//     aws_iam_policy and aws_iam_instance_profile.
//   - And only when their candidate list came back empty. An estate that
//     owns some of the type never reaches the arm at all.
//   - And only when there is a native route to fall back TO
//     ([nativeSweepReaches]). With no Cloud Control configured and no
//     provider list resource the run makes no extra call whatsoever and
//     #1318's gap is still the whole answer.
//
// # What it must not do
//
// Make the gap disappear. #1318 made it loud on purpose and #881 requires
// it. A fallback that FINDS the object turns the gap into an answer, and the
// type is recorded in [Result.SweepCovered] by the ordinary scan body that
// found it. A fallback that fails - the listing errored, or no leg could
// read a marker off anything it listed - has established nothing, so it
// files its own refusal ([SweepGapListFailed], [SweepGapMarkerUnreadable])
// and the caller then files #1318's gap on top, unchanged.

// sweepTagIndexFallback gives typeName the native per-type enumeration the
// tagging leg's empty answer left it without, and reports whether the run
// ended up with an answer for the type.
//
// answered is read from [Result.SweepCovered] rather than from what this
// function believes it did, because that is the same field every other leg
// writes and the same field [scanTypeCloudControl] REMOVES the type from
// when it lists objects it cannot read a marker off ([dropCovered]). A
// fallback that scored itself would be the ratchet shape this repository
// has been caught by before: a check that passes whenever its own rule
// reproduces its own answer.
func sweepTagIndexFallback(ctx context.Context, req Request, schemas listclient.Schemas, decl *declared, typeName string, res *Result) (tfdiags.Diagnostics, bool) {
	var diags tfdiags.Diagnostics

	if !nativeSweepReaches(req, schemas, typeName) {
		// Nothing to fall back to. No call is made and the caller's gap is
		// the whole answer, exactly as #1318 shipped it.
		return diags, false
	}

	// [dedupAlreadyConfigScanned]'s test, for its reason. [sweepTypes] adds
	// a [taggingAPIRestrictedType] back into the sweep universe even when
	// the configuration declares needs-discovery instances of it, and such a
	// type has ALREADY been listed in full by the config-driven loop, at
	// ScopeAll, before the sweep began - res.Orphans is appended there with
	// no `sweep` gate, so the undeclared object this fallback is for has
	// already been found if it could be. Listing again would pay the type's
	// whole per-object provider Read a second time (for aws_iam_policy,
	// #1039's GetPolicyVersion once per policy in the account) to find
	// nothing new.
	//
	// Returning "not answered" rather than claiming the config-driven pass's
	// coverage is deliberate, and it is the narrow choice. That pass really
	// did list the whole account for the type, and [dedupAlreadyConfigScanned]
	// makes exactly that claim on the native leg for exactly this condition
	// - it appends the type to [Result.SweepCovered] and moves on. Making the
	// same claim here would retire #1318's gap for a declared type as well.
	// It is not made, because the arm's own history is a warning about this:
	// converting a recorded gap into "covered, nothing found" is what #1144
	// tried and reverted. A declared type keeps exactly the verdict it had
	// before this fallback existed.
	if len(decl.types[typeName]) > 0 {
		return diags, false
	}

	log.Printf("[DEBUG] stateless/discovery: the estate's tag index serves %s in %s and answered holding none of this estate's; sweeping it natively as well (issue #1321)", typeName, req.Region)

	// The same collectUnclaimed the native sweep loop computes for a type it
	// owns (discovery.go's TaggingSweep leg), for the same reason: a type
	// present only because its declared instances are all record-backed
	// still owes an unclaimed population to a caller that asked for one.
	collectUnclaimed := req.CollectUnclaimed && decl.recordBacked[typeName] != nil

	diags = diags.Append(scanType(ctx, req, schemas, decl, typeName, res, true, collectUnclaimed))

	// decl.unscanned is deliberately not touched here, and the reason is
	// worth recording because the opposite looks necessary. [bind] skips
	// every declared instance of an unscanned type, so a failed extra call
	// that set the flag would start suppressing bindings the tagging leg
	// never suppressed - the cache-vouch pass in [Discover] restores the
	// flag for exactly that reason. But no leg [scanType] reaches with
	// sweep=true ever sets it: every `decl.unscanned[typeName] = true` in
	// scanType, [scanTypeCloudControl] and [scanTypeContentMatch] sits after
	// an `if sweep { return }`, because a sweep's failure to list a type the
	// configuration never mentions is a [SweepGap] rather than a fact about
	// any declared instance. A restoration here would be code that cannot
	// run, which is worse than none.
	return diags, sweepCovered(res, typeName)
}

// sweepCovered reports whether the sweep recorded typeName as searched.
func sweepCovered(res *Result, typeName string) bool {
	for _, c := range res.SweepCovered {
		if c == typeName {
			return true
		}
	}
	return false
}
