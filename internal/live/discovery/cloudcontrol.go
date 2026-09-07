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

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// cloudControlSource decides whether typeName can be enumerated through
// Cloud Control, and if so, which CFN type to list.
//
// This is the "absent but mapped+registry-listable" leg of the three-way
// enumeration-source selection issue #47 describes: [scanType] already
// covers "native list resource exists" (unchanged) before ever reaching
// here, and the caller's existing refusal covers "neither" when this
// returns false. A request that never configured CloudControl or Roster at
// all - every caller that predates this fallback - always gets false here,
// which is what keeps the existing refusal the only outcome for them.
func cloudControlSource(req Request, typeName string) (cfnType string, ok bool) {
	if req.CloudControl == nil || req.Roster == nil {
		return "", false
	}
	return req.Roster.EnumerationSource(typeName)
}

// scanTypeCloudControl is [scanType]'s Cloud Control counterpart: same
// contract (a scan row, claims filed against decl, problems and sweep gaps
// appended to res), different transport. It is reached only when the
// provider offers typeName no native list resource and
// [cloudControlSource] found a listable CFN type for it.
//
// Tag filtering here is unconditionally client-side - Cloud Control has no
// server-side tag filter at all, unlike the EC2-style filter blocks
// [supportsTagFilter] looks for on the provider side - via whichever of the
// two paths issue #47 specifies: a listed ResourceDescription's own
// Properties.Tags when Cloud Control sent one, or one GetResource per
// candidate to refine when it did not. The refinement count rides on
// [TypeScan.Refined] rather than staying invisible, and is logged per call
// at [DEBUG] the same way [scanType]'s own client-side fallback is.
func scanTypeCloudControl(ctx context.Context, req Request, decl *declared, typeName, cfnType string, res *Result, sweep, collectUnclaimed bool) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	scan := TypeScan{
		TypeName:  typeName,
		Declared:  len(decl.types[typeName]),
		Sweep:     sweep,
		Source:    SourceCloudControl,
		CFNType:   cfnType,
		Filtering: FilterClientSide,
		Scope:     ScopeAll,
		FilterReason: fmt.Sprintf(
			"%s has no native provider list resource; Cloud Control's ListResources on %s has no server-side tag filter at all",
			typeName, cfnType),
	}

	if taggable, known := req.Roster.TaggableKnown(cfnType); sweep && !taggable {
		res.Scans = append(res.Scans, scan)
		return diags.Append(sweepGapDiag(res, noRegistryRowOrUntaggable(typeName, cfnType, known)))
	}

	// GitHub issue #605's Cloud Control half: during a sweep this listing has
	// usually already been fetched, concurrently with the types before it
	// (sweepconcurrency.go). Everything after it is the sequential body
	// unchanged. takeCloudControl answers false whenever no prefetch is
	// running or it fetched a different CFN type, and then the call is made
	// here exactly as before.
	descs, err, prefetched := req.sweepFetch.takeCloudControl(typeName, cfnType)
	if !prefetched {
		descs, err = req.CloudControl.ListResources(ctx, cfnType)
	}
	if err != nil {
		res.Scans = append(res.Scans, scan)
		if sweep {
			return diags.Append(sweepGapDiag(res, SweepGap{
				TypeName: typeName,
				Reason:   SweepGapListFailed,
				Detail: fmt.Sprintf(
					"Cloud Control ListResources on %s (for %s) failed, so the sweep could not look for resources of that type which this estate owns but no longer declares: %s.",
					cfnType, typeName, err),
			}))
		}
		decl.unscanned[typeName] = true
		return diags.Append(problemDiag(res, Problem{
			Kind:     ProblemListFailed,
			TypeName: typeName,
			Detail: fmt.Sprintf(
				"Cloud Control ListResources on %s failed, so nothing of %s could be discovered: %s.",
				cfnType, typeName, err),
		}))
	}
	scan.Listed = len(descs)
	if sweep {
		res.SweepCovered = append(res.SweepCovered, typeName)
	}

	log.Printf("[DEBUG] stateless/discovery: listing %s via Cloud Control (%s), %d resources, client-side tag filtering (Cloud Control offers no server-side filter)", typeName, cfnType, len(descs))

	// GitHub issue #272's leg, and it REPLACES the marker path below rather
	// than running before it: a name-bound type has no tags argument, so
	// every listed object would raise ProblemNoTags on the way past. See
	// uniquename.go, which carries the whole argument for why matching a
	// name AWS refuses to issue twice is not the content-match guess
	// internal/live/foreign forbids.
	if idx, byName := uniqueNameIndexFor(decl, typeName); byName {
		diags = diags.Append(scanUniqueName(req, decl, typeName, cfnType, descs, idx, &scan, res))
		res.Scans = append(res.Scans, scan)
		return diags
	}

	// sweepUntaggedReported is [scanType]'s own "once per type" guard,
	// mirrored here: an object this leg genuinely could not read is a gap in
	// removal coverage for the type, not one gap per malformed object.
	sweepUntaggedReported := false

	for _, desc := range descs {
		tags, read, refined := cloudControlTags(ctx, req, cfnType, desc)
		taggable := read == ccTagsPresent
		if refined {
			scan.Refined++
			log.Printf("[DEBUG] stateless/discovery: %s identifier %q carried no Tags in its Cloud Control listing; refined with GetResource", typeName, desc.Identifier)
		}

		importID, idOK := resolveCloudControlImportID(typeName, desc.Identifier)
		if !idOK {
			diags = diags.Append(problemDiag(res, Problem{
				Kind:     ProblemUncomposableIdentifier,
				TypeName: typeName,
				LiveIDs:  liveIDs(desc.Identifier),
				Detail: fmt.Sprintf(
					"Cloud Control returned the multi-part identifier %q for a %s, and no entry in the identity table (internal/live/identity) can compose its parts into a TF import identity. The raw \"|\"-joined string is never used as a substitute; see live/MARKERS.md and internal/live/identity's Components.",
					desc.Identifier, typeName),
			}))
			continue
		}

		// Issue #266, the same join [scanType] makes on the native list
		// path and for the same reason: a listed object with no readable
		// marker is one a needs-discovery instance can never bind to. Here
		// it also covers the case cloudControlTags' own GetResource
		// refinement could not settle. See bindtags.go.
		if tags[TagEstate] == "" {
			joined, outcome := req.markers.join(ctx, typeName, importID)
			switch outcome {
			case joinBound:
				tags, taggable = joined, true
				scan.Joined++
				log.Printf("[DEBUG] stateless/discovery: %s %q came back from Cloud Control with no ownership marker; joined one from the estate's tag index", typeName, importID)
			case joinAmbiguous:
				diags = diags.Append(problemDiag(res, Problem{
					Kind:     ProblemAmbiguousTagJoin,
					TypeName: typeName,
					LiveIDs:  liveIDs(importID),
					Detail: fmt.Sprintf(
						"Cloud Control listed a %s (%s) carrying no ownership marker, and more than one resource in estate %q's tag index has that identifier and a tofu-address naming a %s: %s. Nothing in either answer says which is the listed object, so no marker was read off it. Retag or remove the duplicates.",
						typeName, importID, req.Estate, typeName, strings.Join(req.markers.matchedARNs(typeName, importID), ", ")),
				}))
			}
		}
		if tags[TagEstate] == "" && !sweep {
			decl.unreadable[typeName]++
		}

		if !taggable {
			// Issue #355. A Cloud Control Properties map answers "which tags
			// does this object carry", never "can this type carry tags at
			// all": Cloud Control omits a property with no value, so an
			// object with zero tags arrives with no Tags key, byte-identical
			// to what a type with no Tags property in its schema would send.
			// The CFN registry answers the second question on its own -
			// live/registry.json's tagging.taggable, the same fact
			// [scanTypeCloudControl]'s own sweep guard already reads a few
			// lines up - so the two cases are told apart from the registry
			// rather than from the wire.
			//
			// Measured against floci at cdd50ec0: an untagged
			// AWS::EC2::DHCPOptions (and the account's default set, which is
			// only ever an instance of that shape) lists with no Tags key,
			// while a tagged one of the same type lists with Tags populated -
			// so absence here is a statement about the object, not the type.
			// Its native-list twin has always been ordinary: an untagged
			// object comes back with tags = {} and lands in Unclaimed.
			if regTaggable, known := req.Roster.TaggableKnown(cfnType); known && read == ccTagsAbsent {
				if regTaggable {
					// The registry says this type carries Tags, and this
					// object was read successfully without any. It is
					// untagged - the "tagged with nothing" [ccPropertiesTags]
					// already reports for a Tags key holding an empty list,
					// reached by the other spelling of the same fact.
					tags, taggable = map[string]string{}, true
				} else {
					// The registry says no object of this type can carry a
					// tag, so none could ever carry a marker. That is issue
					// #322's ruling for the native leg ([markerCapable]),
					// reached here through the registry instead of the
					// provider schema: the decl.unreadable increment above
					// already feeds [unreadableMarkerProblem]'s per-address
					// WARNING at bind time, and escalating to an ERROR that
					// aborts the whole plan over an address this run already
					// reports gracefully is what #322 rejected.
					continue
				}
			}
		}

		if !taggable {
			// What is left is the case this refusal was written for: the
			// object could not be read at all - GetResource errored or came
			// back empty, and the UnsupportedOperation re-list did not rescue
			// it either. The answer is unknown rather than "untagged", and
			// guessing is what live/MARKERS.md forbids.
			//
			// The second sentence's branch is #168's residual, and it cannot
			// fire today: reaching this function at all means
			// [Roster.EnumerationSource] found live/registry.json's own row
			// for cfnType saying its list handler needs no input, so a
			// registry row provably exists and known above is always true.
			// TestCloudControlNoRegistryRowNeverReachesThisLeg pins that. It
			// stays because "the artifact said nothing" must never be turned
			// into a claim about the resource if the two facts ever decouple.
			//
			// Issue #531: this used to raise [ProblemNoTags] - an ERROR that
			// aborts the whole plan, see [Severity] - unconditionally, even
			// during a sweep of a type the configuration never declares. This
			// leg had no sweep gate here at all, unlike its native-list twin
			// in [scanType], which has always routed an unreadable object to
			// a graceful [SweepGapObjectUntagged] during a sweep and only
			// hard-fails for a DECLARED type's own scan. An AWS-managed
			// default resource this estate does not own, whose GetResource
			// genuinely errors, is not evidence about anything this estate
			// owns, and the same "declared type's own scan still hard-fails,
			// unchanged" split [scanType] already draws applies here
			// identically.
			if sweep {
				if !sweepUntaggedReported {
					sweepUntaggedReported = true
					diags = diags.Append(sweepGapDiag(res, SweepGap{
						TypeName: typeName,
						Reason:   SweepGapObjectUntagged,
						Detail: fmt.Sprintf(
							"The estate-wide sweep listed a %s (via Cloud Control, %s) that could not be read at all, so its ownership markers cannot be read and it cannot be matched to this estate. This is expected for a resource this estate does not own, such as an AWS-managed default; the sweep continues over the rest of this type's objects and every other type.",
							typeName, cfnType),
					}))
				}
				continue
			}
			why := "and refining it with GetResource found none either, so its ownership markers cannot be read"
			if read == ccTagsAbsent {
				why = fmt.Sprintf("and live/registry.json has no row for %s, so nothing says whether that absence is a fact about this object or about the type - see internal/live/registry's TaggableKnown", cfnType)
			}
			diags = diags.Append(problemDiag(res, Problem{
				Kind:     ProblemNoTags,
				TypeName: typeName,
				LiveIDs:  liveIDs(importID),
				Detail: fmt.Sprintf(
					"Cloud Control listed a %s (Cloud Control type %s) with no Tags in its Properties, %s.",
					typeName, cfnType, why),
			}))
			continue
		}

		estate := tags[TagEstate]
		switch {
		case estate == "":
			// See scanType's own estate=="" branch for why sweep alone is
			// not enough here any more: collectUnclaimed distinguishes an
			// ordinary sweep of a type the configuration never declares
			// from GitHub issue #388 edge 3's record-backed exception.
			if sweep && !collectUnclaimed {
				continue
			}
			scan.Unclaimed++
			res.Unclaimed = append(res.Unclaimed, UnclaimedResource{
				TypeName:     typeName,
				ImportID:     importID,
				IdentityAttr: "id",
				Tags:         tags,
			})
			continue
		case estate != req.Estate:
			scan.OtherEstate++
			continue
		}

		raw, corrupt := GatherAddress(tags)
		if corrupt {
			diags = diags.Append(problemDiag(res, Problem{
				Kind:     ProblemMalformedMarker,
				TypeName: typeName,
				LiveIDs:  liveIDs(importID),
				Detail: fmt.Sprintf(
					"A live %s (via Cloud Control) claims estate %q but its tofu-address continuation tags have a gap in them - one of tofu-address-2, tofu-address-3, ... is missing while a later one is present. Per live/MARKERS.md such a resource is malformed - neither owned nor foreign - and a human has to say which address it belongs to; discovery will not guess.",
					typeName, req.Estate),
			}))
			continue
		}
		escaped := EscapeAddress(raw)
		if !ValidMarkerAddress(escaped) {
			what := "carries no tofu-address tag"
			if raw != "" {
				what = fmt.Sprintf("carries the tofu-address value %q, which is not a well-formed escaped address", raw)
			}
			diags = diags.Append(problemDiag(res, Problem{
				Kind:     ProblemMalformedMarker,
				TypeName: typeName,
				Marker:   raw,
				LiveIDs:  liveIDs(importID),
				Detail: fmt.Sprintf(
					"A live %s (via Cloud Control) claims estate %q but %s. Per live/MARKERS.md such a resource is malformed - neither owned nor foreign - and a human has to say which address it belongs to; discovery will not guess.",
					typeName, req.Estate, what),
			}))
			continue
		}

		// bindType is the type every declared-set lookup and reported
		// record below uses to find where this object belongs. It starts
		// as typeName - the type this Cloud Control list call was made
		// for - and is corrected to the marker's own type only for the
		// cases [sweepBindType] knows safe: see its own doc comment for
		// the three-way answer and issue #394 for the bug this closes.
		bindType := typeName
		if markerType := markerTypeOf(escaped); markerType != typeName {
			// recompose recomputes the identifier under markerType's own
			// row from this SAME Cloud Control identifier - the leg's own
			// equivalent of [scanType]'s importIdentityFromResource, needed
			// for an overlapping-list-call sibling pair whose ratified rows
			// disagree ([sameRatifiedIdentity] false: rdsClusterInstanceSibling
			// today) rather than the identity carrying forward unchanged.
			// See [sweepBindType]'s own doc comment.
			corrected, fixedImportID, skip := sweepBindType(decl, markerType, typeName, escaped, func(mt string) (string, bool) {
				return resolveCloudControlImportID(mt, desc.Identifier)
			})
			if skip {
				// The marker's own type is declared and was already
				// visited, correctly, by its own config-driven scan pass
				// before this sweep ran - this is the same live object
				// surfacing a second time under this list call's own type
				// name, not a second object. Nothing to file.
				continue
			}
			if corrected == typeName {
				diags = diags.Append(problemDiag(res, crossTypeMarkerProblem(
					decl, req.Estate, typeName, markerType, raw, liveIDs(importID), " (via Cloud Control)", sweep)))
				continue
			}
			if fixedImportID != "" {
				importID = fixedImportID
			}
			bindType = corrected
		}

		c := claimant{
			importID:     importID,
			identityAttr: "id",
			identity:     cty.NilVal,
			marker:       raw,
			escaped:      escaped,
			normalized:   escaped != raw,
			slot:         tags[TagSlot],
			tags:         tags,
			noIdentity:   importID == "",
		}

		// GitHub issue #906: this object's marker names a declared address,
		// which is exactly the condition the next two branches share. The
		// sighting is filed with the provider configuration that address's
		// block uses, in scope or out, for [Merge] to read across passes.
		noteDeclaredSighting(req, decl, res, bindType, escaped, importID)

		if entry, ok := decl.entryFor(bindType, escaped); ok {
			// [declaredEntry.addClaimant], not a bare append: this leg and
			// the estate-wide tag sweep can both enumerate one declared type
			// in the same pass, and they then see the same live object.
			entry.addClaimant(c)
			continue
		}
		if decl.declares(bindType, escaped) {
			// GitHub issue #244, half 2 - the same check discovery.go's own
			// scan loop makes at the same point, for the same reason. See
			// displaced.go.
			switch want, verdict := decl.displacedFrom(ctx, bindType, escaped, c); verdict {
			case verdictDisplaced:
				diags = diags.Append(problemDiag(res, displacedProblem(req, bindType, escaped, want, c)))
			case verdictOwnObject:
				// Issue #692: the sweep saw this declared instance's own
				// marker on a live object and nothing contradicts it, so
				// the sighting vouches for the instance instead of being
				// discarded - see Result.VerifiedDeclared.
				if addr, ok := decl.vouchAddr(bindType, escaped); ok {
					res.VerifiedDeclared = append(res.VerifiedDeclared, addr)
				}
			case verdictIdentityChanging:
				// Issue #885: neither reported nor vouched. See
				// [verdictIdentityChanging].
			}
			continue
		}
		if cb := decl.countBlockFor(bindType, escaped); cb != nil {
			cb.extra = append(cb.extra, c)
			continue
		}
		if blk, ok := decl.blocks[bindType][escaped]; ok && blk.keyed {
			blk.claimants = append(blk.claimants, c)
			continue
		}
		if orphanAlreadyPresent(res.Orphans, bindType, escaped, importID) {
			// See [orphanAlreadyPresent]'s own doc comment: the same live
			// object, undeclared, found by two admitted types' own
			// independent Cloud Control scans of one shared CFN type
			// (rdsClusterInstanceSibling's aws_db_instance /
			// aws_rds_cluster_instance today, both AWS::RDS::DBInstance).
			continue
		}
		res.Orphans = append(res.Orphans, OwnedResource{
			TypeName:     bindType,
			ImportID:     importID,
			IdentityAttr: "id",
			Marker:       raw,
			Normalized:   escaped,
			Slot:         tags[TagSlot],
			Tags:         tags,
			Swept:        sweep,
			// desc.Properties is this leg's own equivalent of [scanType]'s
			// listed resource object - see [cfnPropertiesAsResource]'s own
			// doc comment for why it needs converting rather than passing
			// straight through.
			Resource: cfnPropertiesAsResource(desc.Properties),
		})
	}

	res.Scans = append(res.Scans, scan)
	return diags
}

// cloudControlTags reads a listed resource's tags, filtering client-side
// (issue #47): from the ResourceDescription's own Properties.Tags when
// Cloud Control sent one alongside the listing, or - the bounded fallback -
// from one GetResource call when it did not, using the same
// UnsupportedOperation-tolerant fallback the composite-identifier path and
// every other Cloud Control read in this fork uses
// ([cloudcontrol.GetResourceByIdentity]).
//
// refined reports whether the second path was taken, so the caller can
// count and log the cost rather than hide it.
//
// read is the part issue #355 split out of a plain "taggable" bool. That bool
// collapsed two facts a caller has to tell apart: an object this run could not
// read AT ALL, and an object it read cleanly whose Properties simply carry no
// Tags key. Only the first is the "ownership markers cannot be read" the
// caller's [ProblemNoTags] refusal describes; the second is what Cloud Control
// sends for every untagged object of every type, because a CFN Properties map
// omits a property with no value.
func cloudControlTags(ctx context.Context, req Request, cfnType string, desc cloudcontrol.ResourceDescription) (tags map[string]string, read ccTagRead, refined bool) {
	if t, ok := ccPropertiesTags(desc.Properties); ok {
		return t, ccTagsPresent, false
	}

	full, err := cloudcontrol.GetResourceByIdentity(ctx, req.CloudControl, cfnType, desc.Identifier)
	if err != nil || full == nil {
		// A refinement that failed or came back empty is reported as
		// unreadable rather than retried or guessed at: the caller's
		// ProblemNoTags path says so to an operator, which is the same
		// honesty [markers.TagsOf] gives a native-path resource whose
		// object came back with no tags attribute at all.
		return nil, ccTagsUnreadable, true
	}
	if t, ok := ccPropertiesTags(full.Properties); ok {
		return t, ccTagsPresent, true
	}
	return nil, ccTagsAbsent, true
}

// ccTagRead is what one Cloud Control read established about an object's tags.
// See [cloudControlTags] for why the middle value exists at all.
type ccTagRead int

const (
	// ccTagsPresent: the object's Properties carried a Tags key, and tags is
	// what it said - including the empty list, a real "tagged with nothing".
	ccTagsPresent ccTagRead = iota
	// ccTagsAbsent: the object was read successfully and its Properties carry
	// no Tags key. Whether that is a fact about the object (untagged) or about
	// the type (no Tags property at all) is not answerable from the wire; the
	// CFN registry's own tagging.taggable answers it.
	ccTagsAbsent
	// ccTagsUnreadable: the object could not be read at all - the listing sent
	// no Tags and the GetResource refinement errored or came back empty. This
	// is the genuine "ownership markers cannot be read".
	ccTagsUnreadable
)

// ccPropertiesTags reads a CFN Tags property out of a Cloud Control
// Properties map, in the shape Cloud Control actually sends it: a JSON
// array of {"Key": ..., "Value": ...} objects. ok is false only when the
// Tags key is absent entirely - a Properties map that carries Tags as an
// empty list is a real "tagged with nothing", the same distinction
// [markers.TagsOf] draws for the native path.
func ccPropertiesTags(props map[string]any) (map[string]string, bool) {
	if props == nil {
		return nil, false
	}
	raw, ok := props["Tags"]
	if !ok {
		return nil, false
	}
	list, ok := raw.([]any)
	if !ok {
		// Present but not the list-of-Key/Value shape this fork's admitted
		// types use: reported as "no tags this client can read" rather than
		// guessed at.
		return nil, false
	}
	out := make(map[string]string, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		k, _ := m["Key"].(string)
		v, _ := m["Value"].(string)
		if k == "" {
			continue
		}
		out[k] = v
	}
	return out, true
}

// importsWholeARNString is [identity.ImportsWholeARN], kept as a name this
// package's own call sites read naturally. The predicate itself moved to
// internal/live/identity for GitHub issue #879: internal/live/projection has
// to ask the identical question when it records an object's identity, and
// two copies of "does this type import by its whole ARN" that drift apart is
// precisely the defect #879 measured (a record and a discovered claimant
// naming one live object two different ways, with nothing able to compare
// them).
//
// The reasoning behind both of the predicate's signals - issue #298's
// aws_ecs_task_definition, whose identity SCHEMA is family+revision while
// its documented import string is the whole ARN, and issue #124's
// aws_prometheus_workspace, which is why the arn-first signal was never
// widened to "every ARN-shaped identifier" - is on [identity.ImportsWholeARN]
// itself.
func importsWholeARNString(ti identity.TypeIdentity) bool {
	return identity.ImportsWholeARN(ti)
}

// resolveCloudControlImportID turns a Cloud Control ListResources
// identifier into the TF import identity, enforcing the composite rule
// issue #47 states in one sentence: Cloud Control's "|"-joined identifier is
// not a TF import string, so a multi-part identifier is always composed
// through the identity table's Components and never handed out raw.
//
// A single-part identifier (no "|" at all - every server-assigned type in
// [identity.DefaultTable] today) needs no table entry: it is already the
// whole of what a native list result's identity attribute would have been,
// and is used directly, matching [importIdentity]'s "id" convention for the
// native path.
func resolveCloudControlImportID(typeName, identifier string) (string, bool) {
	parts := strings.Split(identifier, "|")
	if len(parts) == 1 {
		if identifier == "" {
			return "", false
		}
		// An ARN identifier for a type whose identity is NOT its arn: the
		// CFN registry pins several types' primaryIdentifier to the
		// read-only Arn property (AWS::APS::Workspace) while the provider
		// imports by the server-assigned id the ARN's resource segment
		// carries - handing the raw ARN out bound aws_prometheus_workspace
		// to an identity the provider reports ABSENT (#124's aps cohort).
		// This is the same arn-vs-id split [joinTaggedResource] already
		// makes for a Tagging API ResourceARN; a type that genuinely
		// imports by ARN ([importsWholeARNString]) keeps the identifier
		// whole.
		if a, ok := cloudcontrol.ParseARN(identifier); ok && a.ResourceID != "" {
			// The negated compound is the readable form here: the condition
			// being tested is "this type is not arn-identified", and De
			// Morgan's split states it as two unrelated-looking clauses.
			if ti, tok := identity.LookupType(typeName); tok && !importsWholeARNString(ti) { //nolint:staticcheck // QF1001: the negation is the claim
				return a.ResourceID, true
			}
		}
		return identifier, true
	}
	ti, ok := identity.LookupType(typeName)
	if !ok {
		return "", false
	}
	return composeCloudControlIdentifier(ti, parts)
}

// composeCloudControlIdentifier builds a TF import identity out of a Cloud
// Control identifier's ordered parts, walking ti.Components the same way
// the identity table already composes any other multi-attribute identity:
// a [identity.Component] with a Literal contributes it verbatim (a
// separator - "_", "/", ","), one with a Cloud value cannot be filled from
// parts at all (Cloud Control does not send the account ID or region
// positionally, and guessing at their position would be exactly the kind of
// guess this package refuses to make elsewhere), and every other component
// consumes the next unconsumed part in order.
//
// ok is false - refusing to compose, never falling back to the raw
// "|"-joined string - whenever a Cloud component is in the way, or the
// parts and the components do not account for each other one for one: both
// mean this type's table entry does not describe the identity Cloud Control
// actually sent, and papering over that with a guess is the one thing
// [DefaultTable]'s whole composition scheme exists to refuse to do (see
// identity/table.go's ComposesIdentity).
func composeCloudControlIdentifier(ti identity.TypeIdentity, parts []string) (string, bool) {
	if len(ti.Components) == 0 {
		return "", false
	}
	var b strings.Builder
	next := 0
	for _, c := range ti.Components {
		switch {
		case c.Literal != "":
			b.WriteString(c.Literal)
		case c.Cloud != identity.CloudNone:
			return "", false
		default:
			if next >= len(parts) {
				return "", false
			}
			b.WriteString(parts[next])
			next++
		}
	}
	if next != len(parts) {
		return "", false
	}
	return b.String(), true
}

// importIDFromARN derives a TF import identity for ti's type from one of
// its own ARNs: arnStr itself when the type's identity IS its ARN
// ([importsWholeARNString], the same check [joinTaggedResource] made
// inline before this was factored out), or the ARN's parsed resource-id
// segment, composed exactly as a Cloud Control ListResources identifier
// would be ([resolveCloudControlImportID]) - because an ARN's resource id
// never carries Cloud Control's "|" separator, this always takes that
// function's single-part branch.
//
// Shared by [joinTaggedResource] (the tag sweep's ARN-to-TF-type-and-id
// join, issue #51) and [scanTypeMarkerFallback] (the declared-resource
// marker-index fallback for a type with no list route at all, issue #293):
// both already know which TF type the ARN belongs to - one from the curated
// [arnJoinTable], the other from the tag itself - and only need this last
// step, deriving the identifier a claimant can be built from.
//
// ok is false whenever the ARN cannot be parsed at all, its resource field
// carries no id segment, or the identity table's Components could not
// account for what Cloud Control's composer expects - never a guess.
func importIDFromARN(ti identity.TypeIdentity, arnStr string) (importID, identityAttr string, ok bool) {
	if importsWholeARNString(ti) {
		return arnStr, "arn", true
	}
	a, parsed := cloudcontrol.ParseARN(arnStr)
	if !parsed || a.ResourceID == "" {
		return "", "", false
	}
	// [arnCompositeImportID] rebuilds a.ResourceID into the provider's own
	// documented import string for the handful of ARN shapes where
	// [ParseARN]'s single first-separator cut does not land on it (issue
	// #295's WAFv2 and GuardDuty cases). Every other shape - including one
	// whose id itself legitimately contains "/", a CloudWatch Logs log
	// group name, and Transfer's Agreement, whose ARN resource field
	// already IS its documented "server_id/agreement_id" import string with
	// no cut needed at all - passes through unchanged, exactly as before
	// #295.
	resourceID := a.ResourceID
	if compose, found := arnCompositeImportID[a.Service][a.ResourceType]; found {
		composed, composedOK := compose(a.ResourceID)
		if !composedOK {
			return "", "", false
		}
		resourceID = composed
	}
	importID, composed := resolveCloudControlImportID(ti.Type, resourceID)
	return importID, "id", composed
}

// arnCompositeImportID rebuilds a.ResourceID into the provider's own
// documented import string for the ARN service-and-resource-type shapes
// where [ParseARN]'s single first-separator cut leaves behind something
// other than that string outright.
//
// Issue #295 started as "WAFv2's import id is wrong" and a first pass here
// took the ARN's trailing segment alone (the WAFv2 "id" attribute's own
// bare value, confirmed against a live floci crossing:
// `aws wafv2 list-web-acls` answers Id="<uuid>", matching exactly). That
// passed every unit test and was WRONG anyway: a second live crossing, now
// with the bare id as the import string, failed differently -
// "Unexpected format of ID (\"<uuid>\"), expected ID/NAME/SCOPE", straight
// from the provider's own ImportResourceState, before it ever calls AWS.
// aws_wafv2_web_acl's Importer does not accept its own "id" attribute value
// alone; it requires the three-part string wafv2_web_acl.html.markdown's
// Import section documents verbatim ("ID/Name/Scope"), and GuardDuty's
// three child types (ipset, threatIntelSet, publishingDestination)
// document the same requirement in their own shape ("DETECTORID:ID"). The
// generic Cloud-Control-identifier composer (composeCloudControlIdentifier,
// [identity.TypeIdentity.Components]) exists for exactly this - a type
// whose import string is not its bare id - but populating it needs a
// [identity.TypeIdentity] row, outside this package.
//
// This table is the same fix at the ARN-parsing layer instead: each entry
// takes a.ResourceID (the ARN's resource field, ParseARN's type marker
// already cut off) and returns the exact string the provider's own "##
// Import" doc section names, confirmed against
// ~/Library/Caches/choudoufu/importdocs-gen for every entry, never guessed
// at from the ARN's shape alone:
//
//   - wafv2's "global" and "regional" scope segments:
//     global/webacl/{name}/{id} -> "{id}/{name}/CLOUDFRONT",
//     regional/webacl/{name}/{id} -> "{id}/{name}/REGIONAL". The scope
//     word is NOT the ARN's own "global"/"regional" spelling - the
//     Importer rejects those - it is CLOUDFRONT/REGIONAL, the `scope`
//     argument's own accepted values. All four WAFv2 ServerAssigned types
//     (web_acl, ip_set, regex_pattern_set, rule_group) share this exact
//     ARN and import-string shape, so keying by "global"/"regional" - not
//     by TF type - covers all four without a per-type row.
//   - guardduty's "detector" segment:
//     detector/{detectorID}/ipset/{id} (and the same shape with
//     "threatintelset" or "publishingdestination" in the third position,
//     none of which the import string keeps at all) ->
//     "{detectorID}:{id}".
//
// Transfer Family's Agreement (agreement/{serverID}/{agreementID}) has no
// row here on purpose: its ARN resource field, with only the "agreement/"
// type marker cut off by ParseARN, already reads "{serverID}/{agreementID}"
// - byte-for-byte the "server_id/agreement_id" string
// transfer_agreement.html.markdown's Import section documents. Adding a row
// that reassembled it would be a no-op at best; an early draft of this
// table instead cut it down to the bare agreement id alone, which would
// have broken an import that ParseARN's unmodified output already gets
// right.
var arnCompositeImportID = map[string]map[string]func(resourceID string) (string, bool){
	"wafv2": {
		"global":   wafv2CompositeImportID("CLOUDFRONT"),
		"regional": wafv2CompositeImportID("REGIONAL"),
	},
	"guardduty": {
		"detector": guarddutyChildCompositeImportID,
	},
}

// wafv2CompositeImportID returns a resolver for one WAFv2 scope: resourceID
// is "{cfnTypeLiteral}/{name}/{id}" (webacl, ipset, regexpatternset or
// rulegroup in the first position - never inspected, since the caller
// already knows it is one of these four from the type the marker named),
// and the provider's documented import string is "{id}/{name}/{scope}".
func wafv2CompositeImportID(scope string) func(string) (string, bool) {
	return func(resourceID string) (string, bool) {
		parts := strings.SplitN(resourceID, "/", 3)
		if len(parts) != 3 {
			return "", false
		}
		name, id := parts[1], parts[2]
		return id + "/" + name + "/" + scope, true
	}
}

// guarddutyChildCompositeImportID composes GuardDuty's documented
// "DETECTORID:ID" import string from resourceID's
// "{detectorID}/{cfnTypeLiteral}/{id}" shape, dropping the literal
// sub-resource-kind segment ("ipset", "threatintelset",
// "publishingdestination") that carries no identity information at all.
func guarddutyChildCompositeImportID(resourceID string) (string, bool) {
	parts := strings.SplitN(resourceID, "/", 3)
	if len(parts) != 3 {
		return "", false
	}
	detectorID, id := parts[0], parts[2]
	return detectorID + ":" + id, true
}

// cfnPropertiesAsResource turns a Cloud Control ResourceDescription's own
// decoded Properties (CFN PascalCase names, e.g. "GroupId") into the SAME
// shape [OwnedResource.Resource] carries for a native-list orphan
// ([scanType]'s own append site) - a cty object [classifyOrphanDestroyDependency]
// can apply [identity.ParentByConvention] against - so this leg's own
// orphans reach the identical, already-tested destroy-ordering path a
// native-list orphan does, rather than a second one.
//
// The property names have to be converted first, via
// [identity.GuessArgNameFromCFNProperty]: Cloud Control's own vocabulary is
// CFN's, never the TF provider schema's, and [identity.ParentByConvention]
// only ever recognizes the latter ("security_group_id", never "GroupId").
// Only string-valued properties are kept - a parent-linking value is always
// an id/arn/url string, and a non-string property (a nested object, a list,
// a bool) can never be one, so converting it would only be more work spent
// on a value the caller's own string-type check would immediately discard
// anyway. Two CFN properties guessing to the same TF name (rare; none of
// today's admitted types collide this way) keep whichever json.Unmarshal
// happened to order last - the same "no ordering promise, just no crash"
// discipline a lossy convention accepts elsewhere in this file.
//
// cty.NilVal for an object with nothing this shape can carry (no string
// properties at all, or Properties itself empty) - not an error, the same
// "no listed object" state a type with no native list route's own
// OwnedResource.Resource already has, and classifyOrphanDestroyDependency's
// own cty.NilVal guard already handles it as "no computed dependency"
// rather than a special case here.
func cfnPropertiesAsResource(props map[string]any) cty.Value {
	fields := make(map[string]cty.Value, len(props))
	for cfnName, v := range props {
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		fields[identity.GuessArgNameFromCFNProperty(cfnName)] = cty.StringVal(s)
	}
	if len(fields) == 0 {
		return cty.NilVal
	}
	return cty.ObjectVal(fields)
}

// resolveOrphanResourceForDependency is classifyOrphans' own fallback for
// the one leg [cfnPropertiesAsResource] never reaches: fileTaggingCandidate
// (tagging.go), whose whole design is "one shared GetResources call, ARN
// plus tags, never a resource's other properties" - see
// typeNeedsResourceObjectToRecompose's own doc comment. A type reachable
// ONLY that way (aws_vpc_security_group_egress_rule/ingress_rule: their
// sole arnJoinTable entry is [ambiguous], so [arnJoinReaches] keeps them in
// the tagging universe even though neither has a native provider list
// route either) never gets an OwnedResource.Resource from any append site
// at all - found via corpus-ecs-fargate's day2_remove unit, a real
// gauntlet run's own debug trace confirmed resourceNil=true reaching
// [classifyOrphanDestroyDependency] for exactly this type.
//
// Called only for an undeclared removal candidate classifyOrphans is about
// to propose destroying - never a sweep-wide cost, the same "one call per
// undeclared parent" budget [parentReadSweepType]'s own doc comment already
// accepts for the identical class of question (issue #60's own leg reads
// one parent per undeclared child; this reads one child's own object per
// undeclared child needing a dependency, at most as many calls as this
// pass's own destroy count). cty.NilVal on any failure to resolve
// (unmapped type, GetResource error, no CloudControl client configured) -
// the same "no computed dependency" outcome a genuinely absent Resource
// already produces, never a hard error: an operator sees one destroy issued
// without a computed ordering hint, not a failed plan.
func resolveOrphanResourceForDependency(ctx context.Context, req Request, o OwnedResource) cty.Value {
	if req.CloudControl == nil || req.Roster == nil || o.ImportID == "" {
		return cty.NilVal
	}
	cfnType, ok := req.Roster.CloudControlType(o.TypeName)
	if !ok {
		return cty.NilVal
	}
	desc, err := req.CloudControl.GetResource(ctx, cfnType, o.ImportID)
	if err != nil || desc == nil {
		return cty.NilVal
	}
	return cfnPropertiesAsResource(desc.Properties)
}
