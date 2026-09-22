// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/intentius/choudoufu/internal/live/listclient"
	"github.com/intentius/choudoufu/internal/live/servicetags"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #1477: the per-service list leg, one step before #1131's
// per-service tag read.
//
// # The situation it fires in
//
// aws_iam_service_linked_role is admitted and taggable, internal/live/stamp
// writes this estate's markers onto it, and nothing could enumerate it: the
// provider offers no list resource for it, AWS::IAM::ServiceLinkedRole has
// no Cloud Control list handler (live/registry.json), and the Resource
// Groups Tagging API never indexes iam:role (#1134), so the estate's tag
// index - #293's fallback for exactly a type with no list route - held
// nothing either. #302's sibling bind used to catch it, because an
// UNNAMED aws_iam_role beside it made discovery call iam:ListRoles, which
// surfaces service-linked roles too; a configuration whose roles all carry
// names never makes that call, and the provider's aws_iam_role list
// resource filters service-linked roles out regardless. The iam-ecr cohort
// is that configuration, and since the 2026-09-11 emulator repin (#1045,
// floci matching real AWS on GetResources for IAM) its plan proposed a
// second create of a role that exists and carries the estate's marker.
//
// The service's own API lists them: iam:ListRoles with
// PathPrefix=/aws-service-role/ returns every service-linked role in the
// account with its Arn and RoleName, and iam:ListRoleTags reads the marker
// by name. internal/live/servicetags carries both as [servicetags.Lister]
// and [servicetags.Reader], one route table each; this file is the leg
// that drives them.
//
// # Where it sits
//
// [scanType]'s no-native-list-resource branch, after the content-match and
// Cloud Control routes and before #293's tag-index fallback and #341's
// record fallback. It is an enumeration, so it is tried where the other
// enumerations are; the two fallbacks after it are for a type nothing can
// list, and this type can now be listed. [nativeSweepReaches] counts it for
// the same reason, so the sweep routes a lister-covered type to this leg
// instead of to the tagging leg's "no enumeration route" gap.
//
// # What it does with each object
//
// The same thing every other leg does, in the same order, with the same
// safety rule. A listed object's own tags are used when the listing
// carried a tofu-estate (IAM's list operations drop tags by design, so on
// real AWS it never does). Otherwise the estate's tag index is asked about
// this object (#266's join). Otherwise the service's tag API is asked
// (#1131's read, with #1162's per-object gate being exactly those two
// prior checks), keyed by [servicetags.Listed.ReadKey] - the role name -
// rather than by the ARN the object binds under. An object whose read
// failed establishes nothing: a declared type's scan counts it in
// decl.unreadable, which surfaces as #322's per-address warning if an
// instance goes unbound beside it, and a sweep files
// [SweepGapMarkerUnreadable] naming the action to grant. An object whose
// read succeeded and holds no tofu-estate is unclaimed; one naming another
// estate is theirs. What is left is filed through the same candidate rules
// the tag sweep and #293's fallback use ([fileCandidate]): marker parsing,
// the declared-instance match that produces the binding, orphan filing for
// a marker whose block is gone.
//
// # What it costs, said plainly
//
// One paginated list call per covered type per scan, and then #1131's bill:
// one tag read per listed object the index did not answer for. For
// service-linked roles on real AWS that is every service-linked role in
// the account, because GetResources never indexes iam:role anywhere, and an
// account accumulates one per service it has ever used. The scan row
// records both ([TypeScan.Listed] and [TypeScan.ServiceTagReads]), and
// live/costs/plan-cost.md carries the figure beside #1131's.

// serviceListRoute reports whether this run's lister enumerates typeName.
// Costs nothing for a run with no lister or a type with no route.
func serviceListRoute(req Request, typeName string) bool {
	return req.ServiceList != nil && req.ServiceList.ListRoute(typeName)
}

// scanTypeServiceList is the leg. Same contract as [scanType] and
// [scanTypeCloudControl]: a scan row, claims filed against decl, problems
// and sweep gaps appended to res.
func scanTypeServiceList(ctx context.Context, req Request, schemas listclient.Schemas, decl *declared, typeName string, res *Result, sweep, collectUnclaimed bool) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	action := req.ServiceList.ListAction(typeName)
	via := " (via " + action + ")"

	scan := TypeScan{
		TypeName:  typeName,
		Declared:  len(decl.types[typeName]),
		Sweep:     sweep,
		Source:    SourceService,
		Filtering: FilterClientSide,
		Scope:     ScopeAll,
		FilterReason: fmt.Sprintf(
			"%s has no native provider list resource and no Cloud Control list handler; %s is the service's own listing and takes no tag filter",
			typeName, action),
	}

	// [scanType]'s own markerCapable gate, for the sweep: a type with no
	// tags argument could never carry a marker, so listing it for the
	// sweep finds nothing by construction. Read from the provider's own
	// resource schema, which a type with no list schema still has.
	if sweep && !typeTaggable(schemas, typeName) {
		res.Scans = append(res.Scans, scan)
		return diags.Append(sweepGapDiag(res, SweepGap{
			TypeName: typeName,
			Reason:   SweepGapNotTaggable,
			Detail: fmt.Sprintf(
				"A %s carries no tags, so it can carry no ownership marker and the sweep has nothing to search on. Destroy a resource of this type before removing its block, or delete it out of band.",
				typeName),
		}))
	}

	objs, err := req.ServiceList.List(ctx, typeName)
	if err != nil {
		// A failed listing is not an empty account. The declared type's
		// scan is an error, because the plan would otherwise propose
		// creating instances that may exist; the sweep's is a gap, the
		// same split every other leg draws.
		res.Scans = append(res.Scans, scan)
		if sweep {
			return diags.Append(sweepGapDiag(res, SweepGap{
				TypeName: typeName,
				Reason:   SweepGapListFailed,
				Detail: fmt.Sprintf(
					"%s (the service's own listing for %s) failed, so the sweep could not look for resources of that type which this estate owns but no longer declares: %s. Grant %s, or retry if it was throttled, and re-run.",
					action, typeName, err, action),
			}))
		}
		decl.unscanned[typeName] = true
		return diags.Append(problemDiag(res, Problem{
			Kind:     ProblemListFailed,
			TypeName: typeName,
			Detail: fmt.Sprintf(
				"%s (the service's own listing for %s) failed, so nothing of %s could be discovered and the %d declared instance(s) of it cannot be matched to a live resource: %s. Grant %s, or retry if it was throttled, and re-run.",
				action, typeName, typeName, scan.Declared, err, action),
		}))
	}
	scan.Listed = len(objs)
	if sweep {
		res.SweepCovered = append(res.SweepCovered, typeName)
	}

	log.Printf("[DEBUG] stateless/discovery: listing %s through the service's own API (%s), %d resources, client-side tag filtering (the listing takes no tag filter)", typeName, action, len(objs))

	// failed and unread are the sweep's evidence for [serviceListMarkerReadGap]:
	// reads that were made and refused, and objects no route was even able
	// to attempt a read for (no reader wired for this run).
	var failed failedTagReads
	unread := 0

	for _, obj := range objs {
		if obj.ImportID == "" {
			// The lister's contract is that every object carries its
			// import identity; one that does not cannot be bound or
			// filed. Skipped rather than trusted with an empty string.
			continue
		}
		tags := obj.Tags

		// Clause 3 of the per-object gate (#1162), the same join every
		// leg makes: an object whose listing carried no marker may still
		// be in the estate's tag index, and that costs no call.
		if tags[TagEstate] == "" {
			joined, outcome := req.markers.join(ctx, typeName, obj.ImportID)
			switch outcome {
			case joinBound:
				tags = joined
				scan.Joined++
				log.Printf("[DEBUG] stateless/discovery: %s %q came back from %s with no ownership marker; joined one from the estate's tag index", typeName, obj.ImportID, action)
			case joinAmbiguous:
				diags = diags.Append(problemDiag(res, Problem{
					Kind:     ProblemAmbiguousTagJoin,
					TypeName: typeName,
					LiveIDs:  liveIDs(obj.ImportID),
					Detail: fmt.Sprintf(
						"%s listed a %s (%s) carrying no ownership marker, and more than one resource in estate %q's tag index has that identifier and a tofu-address naming a %s: %s. Nothing in either answer says which is the listed object, so no marker was read off it. Retag or remove the duplicates.",
						action, typeName, obj.ImportID, req.Estate, typeName, strings.Join(req.markers.matchedARNs(typeName, obj.ImportID), ", ")),
				}))
			}
		}

		// #1131's read, keyed by the object's ReadKey: for a service-linked
		// role that is its name, while it binds under its ARN.
		if tags[TagEstate] == "" {
			readKey := obj.ReadKey
			if readKey == "" {
				readKey = obj.ImportID
			}
			svcTags, outcome, readErr := serviceTagRead(ctx, req, typeName, readKey, &scan)
			switch outcome {
			case tagReadAnswered:
				tags = svcTags
			case tagReadFailed:
				readAction := ""
				if req.ServiceTags != nil {
					readAction = req.ServiceTags.Action(typeName)
				}
				failed.record(servicetags.ErrorCode(readErr), readAction)
				if !sweep {
					decl.unreadable[typeName]++
				}
				continue
			case tagReadNotAttempted:
				// No reader wired for this run, or no route for the type.
				// The object is enumerated and its ownership cannot be
				// established, which is #1129's shape one leg over.
				unread++
				if !sweep {
					decl.unreadable[typeName]++
				}
				continue
			}
		}

		estate := tags[TagEstate]
		switch {
		case estate == "":
			// An answered read holding no tofu-estate is an ordinary
			// unowned object. See scanType's own estate=="" branch for
			// why collectUnclaimed, not sweep alone, decides whether it
			// is recorded.
			if sweep && !collectUnclaimed {
				continue
			}
			if tags == nil {
				tags = map[string]string{}
			}
			scan.Unclaimed++
			res.Unclaimed = append(res.Unclaimed, UnclaimedResource{
				TypeName:     typeName,
				ImportID:     obj.ImportID,
				IdentityAttr: obj.IdentityAttr,
				Tags:         tags,
			})
			continue
		case estate != req.Estate:
			scan.OtherEstate++
			continue
		}

		diags = diags.Append(fileCandidate(ctx, req, decl, typeName, taggedCandidate{
			importID:     obj.ImportID,
			identityAttr: obj.IdentityAttr,
			tags:         tags,
		}, res, via, sweep))
	}

	if sweep {
		diags = diags.Append(serviceListMarkerReadGap(res, typeName, action, scan.Listed, unread, failed))
	}

	res.Scans = append(res.Scans, scan)
	return diags
}

// serviceListMarkerReadGap is [sweepMarkerReadGap] for this leg: the sweep
// listed the type and could not read a marker off some of its objects, so
// it must not be recorded as covered, and the gap has to say what was
// refused and what to grant. Reads that were made and failed come first,
// because their sentence names the action; objects no reader could even be
// asked about are the other case, and it names the missing route.
func serviceListMarkerReadGap(res *Result, typeName, listAction string, listed, unread int, failed failedTagReads) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if failed.n > 0 {
		pronoun := "it"
		if failed.n > 1 {
			pronoun = "them"
		}
		_, action, _ := strings.Cut(failed.first, ": ")
		res.SweepCovered = dropCovered(res.SweepCovered, typeName)
		return diags.Append(sweepGapDiag(res, SweepGap{
			TypeName: typeName,
			Reason:   SweepGapMarkerUnreadable,
			Detail: fmt.Sprintf(
				"The sweep could not read an ownership marker off %d of %d %s: %s returned no tags, the tag index did not hold %s, and the service's own tag read failed (%s). A live %s this estate owns and no longer declares WILL NOT be proposed for destruction by this run. Grant %s, or retry if it was throttled, and re-run.",
				failed.n, listed, typeName, listAction, pronoun, failed.quoted(), typeName, action),
		}))
	}
	if unread == 0 {
		return diags
	}
	res.SweepCovered = dropCovered(res.SweepCovered, typeName)
	return diags.Append(sweepGapDiag(res, SweepGap{
		TypeName: typeName,
		Reason:   SweepGapMarkerUnreadable,
		Detail: fmt.Sprintf(
			"The sweep listed %d %s through %s and could read an ownership marker off %d of them: the listing returned no tags, the tag index did not hold them, and this run has no service tag reader wired for the type. A live %s this estate owns and no longer declares WILL NOT be proposed for destruction by this run.",
			listed, typeName, listAction, unread, typeName),
	}))
}
